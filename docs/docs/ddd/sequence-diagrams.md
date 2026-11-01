---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
description: "UML sequence diagrams of Shopping Cart as built: the idempotent write path, add a line, quote delivery outside the transaction, merge, consuming an event, the sweeps and the outbox relay, with their error branches."
---

# Sequence diagrams

One diagram per distinct shape of the code, traced from the adapters through the
use cases to Postgres and Kafka. Participants are the packages as built:

- `http` is `internal/adapters/inbound/http` (chi router, the Idempotency-Key
  middleware, the problem mapper in `errors.go`).
- `UC` is a use case in `internal/application/usecases`; `Cart` is the aggregate in
  `internal/domain/cart`.
- `Postgres` stands for the repositories, the outbox, the inbox and the idempotency
  store. Inside a use case they share one transaction through `ports.UnitOfWork`.
- `delivery-promise` is the one outbound REST client (`deliverypromise.Gateway`).
- `Relay` is the outbox relay and `Kafka` the broker.

Every command follows one shape inside a unit of work: load the aggregate, call the
domain, save it with its `version` checked, append the raised events to the outbox,
return the view (`Deps.persist`, `mutate` in `cart_commands.go`). A save that lost a
race returns `ports.ErrConcurrentUpdate` and the whole attempt runs again on fresh
state: `commandAttempts = 2` for commands, `consumerAttempts = 5` for consumed
events; then `409 concurrent-update`. Event types are on
[Domain events](/docs/ddd/domain-events).

## 1. The idempotent write path (every `POST`)

All nine `POST` routes pass through `RequireIdempotencyKey` first.

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant C as caller (BFF)
    participant H as http middleware
    participant DB as Postgres
    participant UC as handler and UC
    C->>H: POST with Idempotency-Key
    alt key missing or over 128 characters
        H-->>C: 400 idempotency-key-required
    else body over the size limit
        H-->>C: 400 malformed-request
    else key valid
        H->>DB: Get(route, key)
        alt a record exists
            alt same method, path, query and body
                DB-->>H: stored status and body
                H-->>C: replay, header Idempotent-Replayed true
            else another request under the same key
                H-->>C: 422 idempotency-key-reused
            end
        else none
            H->>UC: run the handler
            UC-->>H: response
            alt status below 500
                H->>DB: Put(route, key, record), kept 24 hours
            else 5xx
                Note over H,DB: never stored,<br/>the key can retry the real work
            end
            H-->>C: response
        end
    end
```

Source: `internal/adapters/inbound/http/idempotency.go`, `errors.go`,
`server.go` (`idempotent`, the nine routes).

Omitted: the NUL-byte guard that runs before the middleware
([Troubleshooting](/docs/operations/troubleshooting)). A stored response includes a
4xx (a rejected `AddLine` replays as the same 422). Two concurrent first requests
may both run; the store keeps the first record.

## 2. AddLine (`POST /carts/{cartId}/lines`)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as AddLine
    participant DB as Postgres
    participant A as Cart
    participant R as Relay and Kafka
    H->>UC: Execute(cartId, catalogItemId, quantity)
    UC->>DB: BEGIN, load Cart and rm_items row
    alt cart unknown
        UC-->>H: 404 not-found
    else found
        UC->>A: AddLine(item, quantity, purchasable, now)
        alt cart not OPEN
            A-->>UC: ErrCartClosed
            UC-->>H: 409 cart-closed
        else item id not a valid id
            A-->>UC: ErrValidation
            UC-->>H: 422 validation-failed
        else quantity not in 1 to 99
            A-->>UC: ErrQuantityOutOfRange
            UC-->>H: 422 quantity-out-of-range
        else item missing, stub, discontinued or unpriced
            A-->>UC: ErrItemNotPurchasable
            UC-->>H: 422 item-not-purchasable
        else merged quantity over 99, or a 101st line
            A-->>UC: ErrQuantityOutOfRange or ErrTooManyLines
            UC-->>H: 422 problem
        else line added or merged
            A-->>UC: quote and selections cleared, CartLineAdded raised
            UC->>DB: update cart where version matches
            UC->>DB: append outbox row CartLineAdded
            alt another writer got in
                DB-->>UC: ErrConcurrentUpdate
                Note over UC: run the whole attempt once more,<br/>then 409 concurrent-update
            else saved
                UC->>DB: COMMIT
                UC-->>H: 200 cart with estimate and readiness
                R->>DB: poll unpublished rows
                R->>R: publish to the analytics topic, key cartId
            end
        end
    end
```

Source: `internal/application/usecases/cart_commands.go` (`AddLine`, `mutate`),
`deps.go` (`transact`, `persist`), `internal/domain/cart/lines.go`,
`internal/adapters/inbound/http/errors.go`.

The use case also records the outcome in the `cart.lines.added` counter (`outcome`,
`reason`). `SetLineQuantity`, `RemoveLine`, `SetDestination`, `SelectDelivery` and
`AcceptRequotedOffers` have the same shape through `mutate`; they differ in the
domain call, they raise no event, and a missing line is `404 not-found`
(`ErrLineNotFound`). `SelectDelivery` fails with `409 stale-delivery-selection` when
the offer is not in the current quote, `SetDestination` with `422 validation-failed`
for a prefix that is not 1 to 10 letters or digits.

## 3. QuoteDelivery (`POST /carts/{cartId}:quote-delivery`)

The remote call runs outside any transaction.

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as QuoteDelivery
    participant DP as delivery-promise
    participant DB as Postgres
    participant A as Cart
    H->>UC: Execute(cartId, idempotencyKey)
    UC->>DB: load Cart (no transaction)
    alt cart unknown
        UC-->>H: 404 not-found
    else cart not OPEN
        UC-->>H: 409 cart-closed
    else no destination prefix
        UC-->>H: 422 no-destination
    else ready to quote
        UC->>DP: POST /delivery-offers:quote<br/>lines, prefix, Idempotency-Key forwarded
        alt timeout (5 s), transport error, 5xx or undecodable reply
            DP-->>UC: ErrDeliveryPromiseUnavailable
            UC-->>H: 503 delivery-promise-unavailable
        else a 4xx answer
            DP-->>UC: ErrQuoteRejected
            UC-->>H: 503 delivery-promise-unavailable
        else 2xx
            DP-->>UC: groups, QUOTED offers, unavailable tiers
            UC->>DB: BEGIN, load Cart again
            alt version differs from the one quoted
                UC-->>H: ErrConcurrentUpdate, the whole attempt runs once more,<br/>then 409 concurrent-update
            else same version
                UC->>A: QuoteDelivery(quote, now)
                A-->>UC: quote held, old selections dropped, CartDeliveryQuoted raised
                UC->>DB: save and append outbox row, COMMIT
                UC-->>H: 200 cart with quote
            end
        end
    end
```

Source: `internal/application/usecases/quote_delivery.go`,
`internal/adapters/outbound/deliverypromise/gateway.go`
(`DefaultTimeout`, the error classes), `internal/adapters/inbound/http/errors.go`.

A rejected quote (4xx) maps to the same `503` as an unavailable one, with the detail
telling the caller to retry with the same `Idempotency-Key`. The lines and the
prefix are kept in both cases; no quote is stored.

## 4. MergeInto (`POST /carts/{cartId}:merge-into`)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant H as http
    participant UC as MergeInto
    participant DB as Postgres
    participant S as Cart (source)
    participant T as Cart (target)
    H->>UC: Execute(sourceId, targetId)
    UC->>DB: BEGIN, load source, load target
    alt either unknown
        UC-->>H: 404 not-found
    else both found
        UC->>S: MergeInto(target, now)
        alt source not OPEN
            S-->>UC: ErrCartClosed
            UC-->>H: 409 cart-closed
        else target is the source, is not OPEN, has no shopper, or is another shopper's
            S-->>UC: ErrMergeTarget
            UC-->>H: 422 merge-target
        else a new line would be the 101st
            S-->>UC: ErrTooManyLines
            UC-->>H: 422 too-many-lines
        else merged
            S-->>UC: lines added to the target, source MERGED
            UC->>DB: save source, save target (version checked)
            UC->>DB: COMMIT, both or neither
            UC-->>H: 200 the target cart
        end
    end
```

Source: `internal/application/usecases/cart_commands.go` (`MergeInto`),
`internal/domain/cart/lifecycle.go`.

Neither save publishes an event. The target's quote and selections are cleared too.
Quantities of an item present in both carts are summed and clamped at 99 (`min(...,
MaxQuantity)`), not rejected.

## 5. Consuming an event (pricing, offer-availability, checkout)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant K as Kafka topic
    participant KC as kafka consumer
    participant UC as Consumer.Apply*
    participant DB as Postgres
    participant D as topic.dlq
    K->>KC: FetchMessage
    alt not a valid CloudEvent, or a type this consumer does not handle
        KC->>K: commit the offset, skipped
    else known type
        KC->>UC: Apply*(Meta, payload)
        alt payload can never apply (validation error)
            UC-->>KC: ErrValidation
            KC->>D: dead-letter at once, no retry
            KC->>K: commit the offset
        else payload valid
            UC->>DB: BEGIN, claim the CloudEvents id in processed_events
            alt already claimed
                UC->>DB: COMMIT, nothing applied
            else first time
                UC->>DB: apply: MarkCatalogued, MarkDiscontinued, ApplyPrice,<br/>ApplyWithdrawn, Replace schedule or tax table,<br/>or close the cart
                alt cart unknown or not OPEN (CheckoutCompleted)
                    Note over UC: logged at WARN, acknowledged,<br/>inbox row still written
                end
                UC->>DB: COMMIT
            end
            alt transient failure
                UC-->>KC: error
                KC->>KC: retry with backoff, 200 ms doubling to 5 s, 5 attempts
                KC->>D: then dead-letter
            end
            KC->>K: commit the offset after success or dead-letter
        end
    end
```

Source: `internal/adapters/inbound/kafka/consumer.go` (`HandleMessage`, `Run`),
`deadletter.go`, `handlers.go`, `handlers_pricing.go`,
`internal/application/usecases/consume.go` (`Consumer.handle`).

For a `CheckoutCompleted` that closes an `OPEN` cart, `CartCheckedOut` is appended to
the outbox in the same transaction. A price, schedule or tax table event is applied
only if its `version` is newer than the stored one. Discontinuing an item does not
touch carts that already hold it; checkout's quote refuses it.

## 6. The sweeps (`AbandonInactive`, `PurgeAbandonedCarts`)

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant S as scheduler
    participant UC as AbandonInactive
    participant P as PurgeAbandonedCarts
    participant DB as Postgres
    participant A as Cart
    S->>UC: tick (at boot, then every ABANDON_SWEEP_INTERVAL)
    UC->>DB: ListIdleOpen(now minus 30 days, limit)
    loop each idle cart
        UC->>DB: BEGIN, load Cart
        UC->>A: AbandonInactive(now)
        alt changed or closed since the scan (ErrNotIdle, ErrCartClosed,<br/>ErrConcurrentUpdate, not found)
            Note over UC: skipped, never failed
        else idle
            A-->>UC: ABANDONED, CartAbandoned raised
            UC->>DB: save, append outbox row, COMMIT
        end
    end
    S->>P: tick (at boot, then every PURGE_SWEEP_INTERVAL)
    P->>DB: BEGIN, DeleteClosedBefore(now minus 30 days, limit)
    P->>DB: COMMIT
    Note over P,DB: deleted carts raise no event
```

Source: `internal/application/usecases/policies.go`,
`internal/adapters/inbound/scheduler/scheduler.go`.

A failing tick is logged and retried at the next interval; a sweep that did work
logs `handled`. Both are safe on several replicas (see
[Runbook](/docs/operations/runbook#sweepers)).

## 7. The outbox relay

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant R as outbox relay
    participant DB as Postgres
    participant K as Kafka
    loop until shutdown
        R->>DB: claim up to 100 unpublished rows below the attempt cap, in id order
        loop each row
            R->>K: publish to the analytics topic<br/>key cartId, acks from all replicas
            alt broker acknowledged
                R->>DB: set published_at
            else failed
                R->>DB: increment attempts, store last_error
                Note over R,DB: at OUTBOX_MAX_ATTEMPTS the row is excluded<br/>and logged at ERROR, re-arm by hand
            end
        end
        Note over R: sleep OUTBOX_RELAY_INTERVAL after a batch that was not full
    end
```

Source: `internal/adapters/outbound/outbox/relay.go`,
`internal/adapters/outbound/kafka/writer.go`,
`internal/adapters/outbound/postgres/outbox.go` (`Drain`, `claim`, `recordFailure`),
[Runbook: outbox relay](/docs/operations/runbook#outbox-relay).

Delivery is at-least-once: the CloudEvents `id` is minted once at encode time and
stored with the row, so a retry republishes the same id and consumers dedupe on it.
