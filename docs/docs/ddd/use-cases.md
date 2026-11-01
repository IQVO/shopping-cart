---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: "Every Shopping Cart application use case: trigger, inputs, invariants, errors, events raised and idempotency, from internal/application/usecases."
---

# Use cases

Every use case is a struct in `internal/application/usecases` with an `Execute`
method over the shared `Deps` (the ports of
`internal/application/ports`). The inbound adapters decode and call them; they
never hold a rule. The step-by-step flows are on
[Sequence diagrams](/docs/ddd/sequence-diagrams), the payloads on
[Events](/docs/api-reference/events) and the REST contract under
[API reference](/docs/api-reference).

## At a glance

| Use case | Trigger | Inbound adapter | Event raised |
| --- | --- | --- | --- |
| `CreateCart` | the shopper, via storefront-bff | REST `POST /carts` | none |
| `AddLine` | the shopper | REST `POST /carts/{cartId}/lines` | `CartLineAdded` |
| `SetLineQuantity` | the shopper | REST `POST /carts/{cartId}/lines/{catalogItemId}:set-quantity` | none |
| `RemoveLine` | the shopper | REST `POST /carts/{cartId}/lines/{catalogItemId}:remove` | none |
| `SetDestination` | the shopper | REST `POST /carts/{cartId}:set-destination` | none |
| `QuoteDelivery` | the shopper | REST `POST /carts/{cartId}:quote-delivery` | `CartDeliveryQuoted` |
| `SelectDelivery` | the shopper | REST `POST /carts/{cartId}:select-delivery` | none |
| `AcceptRequotedOffers` | checkout, after a requote | REST `POST /carts/{cartId}/delivery/requoted` | none |
| `MergeInto` | the shopper at sign-in | REST `POST /carts/{cartId}:merge-into` | none |
| `GetCart` | the shopper, checkout, agents | REST `GET /carts/{cartId}`, A2A skill `get_cart` | none |
| `ListCarts` | the shopper, agents | REST `GET /carts?shopperId=&state=`, A2A skill `list_shopper_carts` | none |
| `AbandonInactive` | the clock | scheduler job, every `ABANDON_SWEEP_INTERVAL` | `CartAbandoned` |
| `PurgeAbandonedCarts` | the clock | scheduler job, every `PURGE_SWEEP_INTERVAL` | none |
| `Consumer.ApplyCatalogItemCreated`, `ApplyCatalogItemDiscontinued` | offer-availability events | Kafka consumer, `retailnetwork.offer-availability.events` | none |
| `Consumer.ApplyItemPriceChanged`, `ApplyItemPriceWithdrawn`, `ApplyDeliveryChargeScheduleChanged`, `ApplyTaxTableChanged` | pricing events | Kafka consumer, `retailnetwork.pricing.events` | none |
| `Consumer.ApplyCheckoutCompleted` (policy `CloseOnCheckoutCompleted`) | checkout event | Kafka consumer, `retailnetwork.checkout.events` | `CartCheckedOut` |

## Common mechanics

- **One unit of work.** Every command loads the cart, calls the aggregate, saves
  it and appends the events it raised to the outbox, inside one
  `ports.UnitOfWork`. A rolled-back command leaves neither a cart change nor an
  outbox row (`Deps.mutate`, `Deps.persist`).
- **Optimistic concurrency.** A save is conditional on the `version` the cart was
  loaded at. A lost race is `ErrConcurrentUpdate`; a command re-runs the whole
  attempt once on fresh state (`commandAttempts = 2`), then the REST adapter
  answers `409` `concurrent-update`.
- **Idempotency.** Every `POST` route requires an `Idempotency-Key` header of 1
  to 128 characters. The first response below 500 for a `(route, key)` is stored
  for 24 hours and replayed with `Idempotent-Replayed: true`; the same key with a
  different request is `422` `idempotency-key-reused`; a 5xx is never stored.
- **Ids.** A `cartId`, `shopperId`, `catalogItemId` or `offerId` is an opaque id
  of 1 to 64 characters from `A-Z a-z 0-9 . _ : -`, starting with a letter or a
  digit (`shared.ValidID`). A request path identifier longer than 128 characters
  is a `404`.
- **Every change refreshes the idle clock.** A change bumps `version` and
  `lastChangedAt`; the 30-day abandonment rule counts from `lastChangedAt`.
- **Views.** Each command returns the cart view: lines, destination, quote,
  selections, the labelled estimate and checkout readiness, computed at the
  use case's clock from the local read models.
- **Error mapping.** Domain, shared and port errors pass through unchanged and
  the REST adapter maps them to problem slugs
  ([Troubleshooting](/docs/operations/troubleshooting#rest-problem-types)).

## CreateCart

- **Trigger:** `POST /carts`. **Input:** optional `shopperId`; the body may be
  empty (a guest cart).
- **Invariants:** `shopperId`, when present, is a valid id; the creation instant
  is set. The new cart is `OPEN`, empty, `version` 1, with no destination.
- **Result:** `201` with the cart view.
- **Errors:** `422` `validation-failed`.
- **Events:** none.

## AddLine

- **Trigger:** `POST /carts/{cartId}/lines`. **Input:** `catalogItemId`,
  `quantity`.
- **Invariants:** the cart is `OPEN` (`ErrCartClosed`); the id is valid; quantity
  is 1 to 99 (`ErrQuantityOutOfRange`); the item is purchasable in the local
  `rm_items` copy, that is catalogued, not discontinued and priced
  (`ErrItemNotPurchasable`); an item already in the cart merges quantities and
  the **merged** quantity must stay in 1 to 99; a new line must not make more
  than 100 lines (`ErrTooManyLines`).
- **Effect:** the line is added or merged; the quote and every selection are
  cleared.
- **Events:** `CartLineAdded` with the merged quantity.
- **Metric:** every attempt, success or failure, counts on `cart.lines.added`
  with `outcome` and, when rejected, the problem slug as `reason`.
- **Errors:** `404` `not-found`, `409` `cart-closed` and `concurrent-update`,
  `422` `item-not-purchasable`, `quantity-out-of-range`, `too-many-lines`,
  `validation-failed`.

## SetLineQuantity and RemoveLine

- **Triggers:** `POST .../lines/{catalogItemId}:set-quantity` with `quantity`,
  and `POST .../lines/{catalogItemId}:remove`.
- **Invariants:** the cart is `OPEN`; the cart holds a line for the item
  (`ErrLineNotFound`, answered `404` `not-found`); for a new quantity, 1 to 99.
- **Effect:** the line changes or is dropped (the others keep their order); the
  quote and selections are cleared.
- **Events:** none (by design, ADR 0002: analytics sees adds only).
- **Errors:** `404`, `409` `cart-closed` / `concurrent-update`, `422`
  `quantity-out-of-range`.

## SetDestination

- **Trigger:** `POST /carts/{cartId}:set-destination`. **Input:**
  `destinationPostalPrefix`.
- **Invariants:** the cart is `OPEN`; the prefix is 1 to 10 letters or digits (a
  prefix, never an address).
- **Effect:** the prefix is stored; the quote and selections are cleared. The
  prefix reaches the tax band through the next estimate.
- **Events:** none.
- **Errors:** `404`, `409`, `422` `validation-failed`.

## QuoteDelivery

- **Trigger:** `POST /carts/{cartId}:quote-delivery` (no body).
- **Preconditions:** the cart is `OPEN` and has a destination (`ErrNoDestination`).
- **Flow:** the cart is read; **outside any transaction** the use case calls
  delivery-promise `POST /delivery-offers:quote` with the lines and the prefix,
  forwarding the request's `Idempotency-Key`; then the cart is reloaded and, if
  its `version` is unchanged, the quote is stored in one unit of work. If the cart
  changed in between the quote is stale and the whole attempt runs once more
  before `409` `concurrent-update`.
- **Invariants on the quote:** group numbers start at 1 and are unique; line
  numbers lie within the cart's lines and belong to one group; each offer belongs
  to its group, has a valid id, a known tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`,
  `STANDARD`), two dates (`deliverByEarliest` not after `deliverByLatest`) and an
  `expiresAt`. A quote holds no capacity.
- **Effect:** the quote replaces any previous one; the selections are cleared.
- **Events:** `CartDeliveryQuoted` (destination prefix and group count; no offer
  detail).
- **Errors:** `422` `no-destination`; `503` `delivery-promise-unavailable`
  (timeout, transport error, 5xx, undecodable answer, or a 4xx, which is a contract
  bug); `409` `cart-closed` / `concurrent-update`; `404`.

## SelectDelivery

- **Trigger:** `POST /carts/{cartId}:select-delivery`. **Input:** `groupNo`,
  `offerId`.
- **Invariants:** the cart is `OPEN`; the offer belongs to that group of the
  **current** quote (`ErrStaleDeliverySelection`, also when there is no quote).
- **Effect:** the selection of the group is set or replaced. Checkout readiness
  holds when every group has a selection and no selected offer is expired
  (`now` is not before `expiresAt`).
- **Events:** none.
- **Errors:** `404`, `409` `stale-delivery-selection` / `cart-closed` /
  `concurrent-update`.

## AcceptRequotedOffers

- **Trigger:** `POST /carts/{cartId}/delivery/requoted`, called by checkout when
  its own requote replaced the offers. **Input:** `offers`, one per group, each
  with `offerId`, `groupNo`, `tier`, `deliverByEarliest`, `deliverByLatest`,
  `expiresAt`.
- **Invariants:** the cart is `OPEN`; at least one offer; one offer per group and
  distinct offer ids; each offer valid as above.
- **Effect:** the offers **replace** the quote and each is selected for its group,
  **without calling delivery-promise** (checkout re-verifies each offer before
  authorizing). A group keeps the line numbers of the previous quote's group of
  the same number; a single new group with no predecessor spans every line.
- **Events:** none. **Errors:** `404`, `409`, `422` `validation-failed`.

## MergeInto

- **Trigger:** `POST /carts/{cartId}:merge-into`. **Input:** `targetCartId`. The
  path cart is the source.
- **Invariants:** the source is `OPEN`; the target is another `OPEN` cart that has
  a shopper; the source is a guest cart or belongs to the same shopper
  (`ErrMergeTarget` otherwise); the merged line list must fit 100 lines
  (`ErrTooManyLines`) and merged quantities clamp at 99.
- **Effect:** the source's lines are taken into the target, the target's quote and
  selections are cleared, the source becomes `MERGED` (terminal). Both carts are
  saved in one unit of work, each version-checked; the target is returned.
- **Events:** none. **Errors:** `404`, `409`, `422` `merge-target`.

## GetCart and ListCarts

- **GetCart:** `GET /carts/{cartId}`, any state; `404` `not-found` when absent.
  Checkout calls it at session open and reads the checkout readiness.
- **ListCarts:** `GET /carts?shopperId=&state=`, newest first. `shopperId` is
  required and must be a valid id, `state` is optional and one of `OPEN`,
  `CHECKED_OUT`, `ABANDONED`, `MERGED`; a violation is `422`
  `validation-failed`. A shopper with no carts gets an empty list.
- Both are also the A2A skills `get_cart` and `list_shopper_carts`, over the same
  use case structs.

## Estimate (the view of every command)

`estimate.Compute` sums price times quantity over the lines the read model prices
(items). Without a destination it stops there and `estimateComplete` is `false`.
With one it adds the selected groups' tier charges from the current schedule (a
group's charge is waived when the group's goods reach the tier's `freeOverMinor`),
then tax: the band covering the first three characters of the prefix (upper-cased),
rate in basis points, rounded half up on goods plus delivery. `estimateComplete`
is `true` only when every line is priced, every group is selected with a known
tier charge, and a tax band covers the prefix. The currency is the one the priced
items state, falling back to `CART_CURRENCY`.

## AbandonInactive (policy)

- **Trigger:** the scheduler, immediately at boot and then every
  `ABANDON_SWEEP_INTERVAL` (default 1 h), in batches of 200 until a batch comes
  back short.
- **Rule:** an `OPEN` cart unchanged for 30 days (`cart.IdleAfter`) becomes
  `ABANDONED`.
- **Events:** `CartAbandoned` carrying `lastChangedAt`.
- **Tolerance:** a cart that changed, closed or vanished since the scan is skipped,
  never failed, so a second sweep or a second replica is harmless.

## PurgeAbandonedCarts (policy)

- **Trigger:** the scheduler, every `PURGE_SWEEP_INTERVAL` (default 6 h), batches
  of 200.
- **Rule:** a cart that is not `OPEN` and unchanged for 30 days is deleted
  (`FOR UPDATE SKIP LOCKED`). This covers `CHECKED_OUT`, `ABANDONED` and `MERGED`.
- **Events:** none.

## Consumer use cases

Each runs in one unit of work together with the inbox claim on the CloudEvents
`id` (`processed_events`): a redelivery is a no-op, a rollback releases the claim.
A transient failure re-runs the unit up to five times (`consumerAttempts`).
Validation failures are poison messages and are dead-lettered by the adapter.

| Use case | Effect | Validation |
| --- | --- | --- |
| `ApplyCatalogItemCreated` | marks the item catalogued in `rm_items` | valid `catalogItemId` |
| `ApplyCatalogItemDiscontinued` | marks the item discontinued (terminal); lines already in carts stay | valid `catalogItemId` |
| `ApplyItemPriceChanged` | records the effective price, applied only when `version` is above the stored price version | valid id, three upper-case letter currency, non-negative price, `version` at least 1 |
| `ApplyItemPriceWithdrawn` | clears the price when the version is newer and remembers it, so a late older price is dropped | valid id, `version` at least 1 |
| `ApplyDeliveryChargeScheduleChanged` | replaces `rm_current_schedule` when the version is higher | `version` at least 1; 1 to 16 tiers; a known tier; non-negative amounts |
| `ApplyTaxTableChanged` | replaces `rm_current_tax_table` when the version is higher | `version` at least 1; 1 to 1000 bands; `postalPrefixFrom` set and not after `postalPrefixTo`; non-negative rate |
| `ApplyCheckoutCompleted` | policy `CloseOnCheckoutCompleted`: the cart named by `cartId` becomes `CHECKED_OUT` and `CartCheckedOut` is raised with the session id | valid `cartId` and `sessionId`. An unknown cart or one that is not `OPEN` is acknowledged, logged (`CheckoutCompleted names an unknown cart`, `... for a cart that is not open`) and left unchanged |

The adapter additionally rejects empty required fields and fields longer than 200
characters as poison (`internal/adapters/inbound/kafka/handlers.go`).
