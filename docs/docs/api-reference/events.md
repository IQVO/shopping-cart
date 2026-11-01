---
id: events
title: Event catalogue
sidebar_label: Events
description: "The CloudEvents Shopping Cart publishes and consumes: types, topics, keys, payload fields, the envelope, delivery guarantees and the dead-letter topics."
---

# Event catalogue

The contract is `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0); the code
that implements it is `internal/adapters/outbound/kafka/encoder.go` (published)
and `internal/adapters/inbound/kafka/handlers*.go` (consumed). The type catalogue
is pinned by [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue)
and enforced by `TestEventCatalogueMatchesContract`. The same events as design
artifacts are on [Domain events](/docs/ddd/domain-events).

## Envelope

Every message, in both directions, is **CloudEvents 1.0 in structured mode**
(`internal/adapters/kafka/cloudevents`). The Kafka header is
`content-type: application/cloudevents+json; charset=UTF-8`.

| Attribute | Value for the events this service publishes |
| --- | --- |
| `specversion` | `1.0` |
| `id` | a UUID minted once when the event is encoded and stored with the outbox row, so a relay retry republishes the **same** id. Consumers dedupe on it |
| `source` | `/retailnetwork/shopping-cart` |
| `type` | `com.retailnetwork.storefront.shopping-cart.cart.<EventName>` |
| `subject` | the `cartId`; it is also the Kafka message key, so every event of one cart lands on one partition in order |
| `time` | the instant of the change, RFC 3339 UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:shopping-cart:analytics:<EventName>:v1` |
| `data` | the payload below, camelCase JSON; instants RFC 3339 UTC |

A breaking payload change is a new `.v2` type, never a mutation. Consumers
dispatch on the **full** `type`, ignore unknown types and dedupe on `id`.

## Published: `retailnetwork.shopping-cart.analytics`

All four events are **analytics-only** (no sibling context consumes a cart
event); this is the service's only topic. They go through the transactional
outbox and an always-on relay: at-least-once, acks from all replicas, key = the
cart id. Removal, quantity changes, destination, selection and merge raise **no**
event, by design.

| Event | Full `type` | Raised by | `data` fields |
| --- | --- | --- | --- |
| `CartLineAdded` | `com.retailnetwork.storefront.shopping-cart.cart.CartLineAdded` | `AddLine` | `cartId`, `catalogItemId`, `quantity` (the **merged** line quantity, 1 to 99), `at` |
| `CartDeliveryQuoted` | `com.retailnetwork.storefront.shopping-cart.cart.CartDeliveryQuoted` | `QuoteDelivery`, after delivery-promise answered | `cartId`, `destinationPostalPrefix`, `groupCount`, `at` |
| `CartAbandoned` | `com.retailnetwork.storefront.shopping-cart.cart.CartAbandoned` | `AbandonInactive`, an `OPEN` cart idle for 30 days | `cartId`, `lastChangedAt` |
| `CartCheckedOut` | `com.retailnetwork.storefront.shopping-cart.cart.CartCheckedOut` | `CloseOnCheckoutCompleted` | `cartId`, `checkoutSessionId`, `at` |

Example, the golden file `internal/adapters/outbound/kafka/testdata/CartLineAdded.golden.json`
(the encoder test compares its bytes exactly):

```json
{"specversion":"1.0","id":"3f6c2a9e-8b1d-4c7a-9e55-0d2f6b7a1c40","source":"/retailnetwork/shopping-cart","type":"com.retailnetwork.storefront.shopping-cart.cart.CartLineAdded","subject":"cart-1","datacontenttype":"application/json","dataschema":"urn:retailnetwork:shopping-cart:analytics:CartLineAdded:v1","time":"2026-10-23T10:15:30.123456Z","data":{"cartId":"cart-1","catalogItemId":"item-100","quantity":2,"at":"2026-10-23T10:15:30.123456Z"}}
```

Besides `content-type`, a published message carries the W3C `traceparent` (and
`tracestate`) of the request or consumer span that raised it, written into the
outbox row in the same transaction. A purged cart raises nothing: the policy
`PurgeAbandonedCarts` deletes it 30 days after it closed, silently.

## Consumed

The cart reads only the fields listed and ignores unknown ones; the producers'
own AsyncAPI files are authoritative. Each consumer has a stable group id from an
environment variable and a dead-letter topic `<topic>.dlq`.

| Topic (group variable) | Event | Full `type` | `data` fields read | Effect |
| --- | --- | --- | --- | --- |
| `retailnetwork.offer-availability.events` (`OFFER_AVAILABILITY_CONSUMER_GROUP`) | `CatalogItemCreated` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `catalogItemId` | item catalogued in `rm_items` |
| | `CatalogItemDiscontinued` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued` | `catalogItemId` | item discontinued (terminal); lines already held stay |
| `retailnetwork.pricing.events` (`PRICING_CONSUMER_GROUP`) | `ItemPriceChanged` | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` | `catalogItemId`, `currency`, `effectivePriceMinor`, `version` | price recorded when `version` is newer |
| | `ItemPriceWithdrawn` | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn` | `catalogItemId`, `version` | price cleared when `version` is newer; an unpriced item is not purchasable |
| | `DeliveryChargeScheduleChanged` | `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` | `version`, `tiers[{tier, chargeMinor, freeOverMinor?}]` (tier one of `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`; 1 to 16 tiers) | current schedule replaced when `version` is higher |
| | `TaxTableChanged` | `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` | `version`, `bands[{postalPrefixFrom, postalPrefixTo, rateBasisPoints}]` (1 to 1000 bands) | current tax table replaced when `version` is higher |
| `retailnetwork.checkout.events` (`CHECKOUT_CONSUMER_GROUP`) | `CheckoutCompleted` | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted` | `cartId`, `sessionId` | the cart becomes `CHECKED_OUT` and `CartCheckedOut` is raised; an unknown or closed cart is acknowledged and logged |

## Delivery and failure semantics

- **Publishing.** At-least-once. A row is marked published only after the broker
  acknowledged it; a row that fails `OUTBOX_MAX_ATTEMPTS` times (default 20) is
  excluded from claims until re-armed
  ([Runbook](/docs/operations/runbook#outbox-relay)).
- **Consuming.** At-least-once with an atomic effect: the inbox claim on the
  CloudEvents `id` (`processed_events`) and the effect commit together, and the
  offset is committed only afterwards. A transient failure retries five times
  (200 ms doubling to 5 s) and then the message goes to the dead-letter topic; a
  payload that can never be applied (undecodable `data`, an empty required field,
  a field over 200 characters, a value the use case refuses) is dead-lettered at
  once; a message that is not a valid CloudEvent, or of a type the consumer does
  not handle, is skipped and committed past. Dead letters keep the original key,
  value and headers and add `x-dlq-source-topic`, `x-dlq-source-partition`,
  `x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at`.
- **Ordering.** Per cart on the analytics topic (key = cart id). Inbound ordering
  is the producers' business; the cart guards price, schedule and tax events with
  their `version`, so reordered or replayed events cannot move a view backwards.
- **Topics.** The service never creates a topic for consuming; the broker
  auto-creates business topics and dead-letter topics on first write.

## Discrepancies with the contract

- `apis/asyncapi.yaml` lists `scheduleId` and `taxTableId` in the example
  payloads of the schedule and tax events; the consumer does not read them
  (`scheduleChangedData`, `taxTableChangedData` carry only `version` and the
  tiers or bands).
- The asyncapi example `CheckoutCompleted` says the producer is "not built yet";
  the checkout service on the fleet does publish `retailnetwork.checkout.events`.
