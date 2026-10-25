# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-25).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured
mode (the repo rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue
of every type shopping-cart publishes. `TestEventCatalogueMatchesContract` checks it
against `apis/asyncapi.yaml`. The types shopping-cart only consumes are listed in
ADR 0003, not here.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across relay
  retries; consumers dedupe on it); `source` `/retailnetwork/shopping-cart`;
  `subject` the `cartId`; `time` the instant of the change, in UTC; `datacontenttype`
  `application/json`; `dataschema`
  `urn:retailnetwork:shopping-cart:analytics:<EventName>:v1`.
- Kafka value = the JSON event format; header
  `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the
  subject, so all events of one cart stay ordered on one partition.
- `data` is camelCase; timestamps are RFC 3339 UTC.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the raising aggregate, lowercase: `cart`. Tier is `storefront`.

### Published types

All four types are analytics-only (decision H1) and go to the topic
`retailnetwork.shopping-cart.analytics`. The cart writes to no other topic.

```text
type: com.retailnetwork.storefront.shopping-cart.cart.CartLineAdded stream: analytics
type: com.retailnetwork.storefront.shopping-cart.cart.CartDeliveryQuoted stream: analytics
type: com.retailnetwork.storefront.shopping-cart.cart.CartAbandoned stream: analytics
type: com.retailnetwork.storefront.shopping-cart.cart.CartCheckedOut stream: analytics
```

| Event | Subject / key | Raised when | `data` |
|---|---|---|---|
| `CartLineAdded` | `cartId` | AddLine is saved | `{cartId, catalogItemId, quantity (merged), at}` |
| `CartDeliveryQuoted` | `cartId` | QuoteDelivery is saved | `{cartId, destinationPostalPrefix, groupCount, at}` |
| `CartAbandoned` | `cartId` | AbandonInactive is saved | `{cartId, lastChangedAt}` |
| `CartCheckedOut` | `cartId` | CloseOnCheckoutCompleted is saved | `{cartId, checkoutSessionId, at}` |

Payload rules:

- The `data` field lists are the model's `publishes[].data` lists. Wire types are
  recorded in `apis/asyncapi.yaml`.
- Removal, quantity changes, destination, delivery selection, merge, requote
  acceptance and purge raise nothing, by design (decision H1 and H5).
- No event carries PII or card data: the cart stores a `shopperId` reference and a
  postal PREFIX only.
- A breaking payload change is a new `.v2` type and dataschema, never a mutation of v1.

## Consequences

- Consumers need nothing but this catalogue and `apis/asyncapi.yaml`; there is no
  shared Go code.
- Adding a published type requires a line here in the same commit as its asyncapi
  message, or the architecture fitness test fails.
