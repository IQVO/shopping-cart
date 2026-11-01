---
id: domain-events
title: Domain events
sidebar_label: Domain events
description: "Every event Shopping Cart publishes and consumes, one block per event: full CloudEvents type, topic, key, payload, producing command, consumers and effect."
---

# Domain events

The events as design artifacts: one block per event, the business meaning next to
the wire facts. The contract and the envelope, with the golden example and the
delivery semantics, are on [Event catalogue](/docs/api-reference/events); the
decision is [ADR 0004](/docs/adr/0004-cloudevents-envelope-and-type-catalogue).
Producing sources: `internal/domain/cart/events.go` (the four event structs) and
`internal/adapters/outbound/kafka/encoder.go` (type, topic, key, payload).
Consuming sources: `internal/adapters/inbound/kafka/handlers.go`,
`handlers_pricing.go` and `internal/application/usecases/consume.go`.

## Envelope in one table

| Attribute | Value for this context |
| --- | --- |
| `specversion` | `1.0`, structured JSON mode |
| `id` | minted once when the event is encoded into the outbox row; a redelivery carries the same `id` |
| `source` | `/retailnetwork/shopping-cart` |
| `type` | `com.retailnetwork.storefront.shopping-cart.cart.<EventName>` |
| `subject` | `cartId` |
| `time` | the instant of the change, UTC |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:shopping-cart:analytics:<EventName>:v1` |
| Kafka topic | `retailnetwork.shopping-cart.analytics`, for all four events |
| Kafka key | `cartId`, so one cart's events stay on one partition, in order |

## Published

Every event is appended to `outbox_events` in the same transaction as the cart save
(`Deps.persist`) and published by the relay, at-least-once. All four are
**analytics-only**: no storefront or network context subscribes to a cart fact
([Context map](/docs/ddd/context-map)). Only these four commands raise events:
removal, quantity change, destination, selection, merge, requote and creation
raise nothing, by design.

### CartLineAdded

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.shopping-cart.cart.CartLineAdded` |
| Topic / key | `retailnetwork.shopping-cart.analytics` / `cartId` |
| Dataschema | `urn:retailnetwork:shopping-cart:analytics:CartLineAdded:v1` |
| Raised by | `AddLine`; adding an item already in the cart merges quantities and the event carries the merged line |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `cartId` | string | The cart the line was added to. |
| `catalogItemId` | string | The item added. |
| `quantity` | integer | The line's quantity after the merge (1 to 99). |
| `at` | timestamp | When the add happened. |

### CartDeliveryQuoted

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.shopping-cart.cart.CartDeliveryQuoted` |
| Topic / key | `retailnetwork.shopping-cart.analytics` / `cartId` |
| Dataschema | `urn:retailnetwork:shopping-cart:analytics:CartDeliveryQuoted:v1` |
| Raised by | `QuoteDelivery`, after `delivery-promise` answered and the quote was stored |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `cartId` | string | The cart quoted. |
| `destinationPostalPrefix` | string | The prefix the quote was computed for. |
| `groupCount` | integer | How many shipment groups the quote holds. |
| `at` | timestamp | When the quote was taken. |

### CartAbandoned

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.shopping-cart.cart.CartAbandoned` |
| Topic / key | `retailnetwork.shopping-cart.analytics` / `cartId` |
| Dataschema | `urn:retailnetwork:shopping-cart:analytics:CartAbandoned:v1` |
| Raised by | the `AbandonInactive` sweep: an `OPEN` cart with no change for 30 days |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `cartId` | string | The cart abandoned. |
| `lastChangedAt` | timestamp | The cart's last change, the fact the 30-day rule fired on. |

### CartCheckedOut

| Item | Value |
| --- | --- |
| Full type | `com.retailnetwork.storefront.shopping-cart.cart.CartCheckedOut` |
| Topic / key | `retailnetwork.shopping-cart.analytics` / `cartId` |
| Dataschema | `urn:retailnetwork:shopping-cart:analytics:CartCheckedOut:v1` |
| Raised by | the policy `CloseOnCheckoutCompleted`, on checkout's `CheckoutCompleted` |
| Consumers | analytics only |

| Field | Type | Meaning |
| --- | --- | --- |
| `cartId` | string | The cart closed. |
| `checkoutSessionId` | string | The session whose completion closed it. |
| `at` | timestamp | When the cart was closed. |

A complete example envelope (`CartLineAdded`, the encoder's golden file) is on
[Event catalogue](/docs/api-reference/events#published-retailnetworkshopping-cartanalytics).

## Consumed

Every consumer applies an event once: the CloudEvents `id` is claimed in
`processed_events` in the same transaction as the effect, and a duplicate is
acknowledged without effect. Consumers dispatch on the **full type string**. The
group ids come from mandatory variables (`OFFER_AVAILABILITY_CONSUMER_GROUP`,
`PRICING_CONSUMER_GROUP`, `CHECKOUT_CONSUMER_GROUP`); the service refuses to start
without them. A payload that can never be applied is dead-lettered to
`<topic>.dlq`.

### From offer-availability (`retailnetwork.offer-availability.events`, Conformist)

| Event | Full type | Fields used | What this context does |
| --- | --- | --- | --- |
| `CatalogItemCreated` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `catalogItemId` | Marks the item catalogued in `PurchasabilityView` (`rm_items`). |
| `CatalogItemDiscontinued` | `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued` | `catalogItemId` | Marks the item discontinued, terminally: it can no longer be added; lines already in carts stay and checkout's quote refuses them. |

### From pricing (`retailnetwork.pricing.events`, Customer/Supplier, we are the Customer)

| Event | Full type | Fields used | What this context does |
| --- | --- | --- | --- |
| `ItemPriceChanged` | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` | `catalogItemId`, `currency`, `effectivePriceMinor`, `version` | Records the effective price when `version` is newer; feeds purchasability and the estimate. |
| `ItemPriceWithdrawn` | `com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn` | `catalogItemId`, `version` | Clears the price when `version` is newer; an unpriced item is not purchasable. |
| `DeliveryChargeScheduleChanged` | `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` | `version`, `tiers[{tier, chargeMinor, freeOverMinor?}]` | Replaces the current schedule when `version` is higher. |
| `TaxTableChanged` | `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` | `version`, `bands[{postalPrefixFrom, postalPrefixTo, rateBasisPoints}]` | Replaces the current tax table when `version` is higher. |

### From checkout (`retailnetwork.checkout.events`, Customer/Supplier, we are the Customer)

| Event | Full type | Fields used | What this context does |
| --- | --- | --- | --- |
| `CheckoutCompleted` | `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted` | `cartId`, `sessionId` | `OPEN -> CHECKED_OUT` and `CartCheckedOut` is raised. An unknown cart, or one that is not `OPEN`, is acknowledged and logged at WARN. |

## Idempotency and ordering

- **Inbox.** One row in `processed_events` per consumed `id`, in the effect's
  transaction, so redelivery is a no-op.
- **Versions.** Price, schedule and tax events are guarded by their `version`:
  reordered or replayed events cannot move a view backwards.
- **Published ordering.** Per cart, because the key is the cart id. Across carts
  there is none.
- **No cross-key ordering on input.** A price or a discontinuation can arrive
  before the item's `CatalogItemCreated`; the view then holds a stub row
  (`catalogued` false) that is not purchasable until the creation arrives.
- **Evolution.** A breaking payload change is a new `.v2` type; consumers ignore
  unknown types and unknown fields.
