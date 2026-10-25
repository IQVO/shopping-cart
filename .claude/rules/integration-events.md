---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

shopping-cart PUBLISHES four analytics-only events to `retailnetwork.shopping-cart.analytics`
(`.events` is unused) and CONSUMES seven events from offer-availability, pricing and checkout.
Full contract: `docs/adr/0003-consumed-contracts-and-local-read-model.md`,
`docs/adr/0004-cloudevents-envelope-and-type-catalogue.md` and `apis/asyncapi.yaml`.

## Events: CloudEvents 1.0 is MANDATORY

Every message produced or consumed is a CloudEvents 1.0 event in structured content
mode; read `.claude/rules/fleet/cloudevents.md`. There is no flat envelope, no
dual-write and no envelope toggle (`TestNoEventEnvelopeToggleOrFlatEnvelope`).

- Build and decode only through `internal/adapters/kafka/cloudevents` (`New`,
  `Decode`, `ContentTypeHeader`); transport is `segmentio/kafka-go`.
- Required attributes: `specversion=1.0`; `id` (UUID minted once per domain event and
  stored with the outbox row); `source=/retailnetwork/shopping-cart`; `type`;
  `subject` (aggregate id, never empty); `time` (UTC); `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:shopping-cart:analytics:<Event>:v1`. Message key =
  subject, `kafkago.Hash{}` balancer.
- Consumers `Decode` (validates), dispatch on the FULL `type` string, ignore unknown
  types, dedupe on `id` through the inbox, dead-letter a payload that can never be
  applied to `<topic>.dlq`, and never crash or block the partition.
- Tests: a golden exact-JSON test per published type
  (`internal/adapters/outbound/kafka/testdata`), a flat-message-rejected test per
  consumer, Kafka integration tests via testcontainers only.

### Published types

| `type` | topic | `subject` |
| --- | --- | --- |
| `com.retailnetwork.storefront.shopping-cart.cart.CartLineAdded` | `retailnetwork.shopping-cart.analytics` | cartId |
| `com.retailnetwork.storefront.shopping-cart.cart.CartDeliveryQuoted` | `retailnetwork.shopping-cart.analytics` | cartId |
| `com.retailnetwork.storefront.shopping-cart.cart.CartAbandoned` | `retailnetwork.shopping-cart.analytics` | cartId |
| `com.retailnetwork.storefront.shopping-cart.cart.CartCheckedOut` | `retailnetwork.shopping-cart.analytics` | cartId |

### Consumed types

| `type` | topic | producer |
| --- | --- | --- |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` / `CatalogItemDiscontinued` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged` / `ItemPriceWithdrawn` | `retailnetwork.pricing.events` | pricing |
| `com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged` | `retailnetwork.pricing.events` | pricing |
| `com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged` | `retailnetwork.pricing.events` | pricing |
| `com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted` | `retailnetwork.checkout.events` | checkout |

## Consumer group id

The group comes from the mandatory environment variable
`OFFER_AVAILABILITY_CONSUMER_GROUP`, `PRICING_CONSUMER_GROUP` and `CHECKOUT_CONSUMER_GROUP` (read in `cmd/api/config.go`, never a literal;
`TestKafkaConsumerGroupNeverHardcodedInline`). The consumer resumes from its committed
offset (it is not a replay-on-start cache), so a stable shared group is correct.
