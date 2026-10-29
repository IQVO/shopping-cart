# ADR 0002: Aggregates, invariants and assumptions

## Status

Accepted (2026-10-25).

## Context

The design source is warehouse-docs `storefront/model/model.yaml`, context
`shopping-cart`, with the FINAL model-gap decisions of the storefront campaign applied
on top (they override the model where they differ): H1 only the modelled events (no
event for removal, merge, quantity change), H2 no `ErrDuplicateLine` (re-adding merges
quantities), H3 PurchasabilityView is lenient by design (checkout's quote is the
gate), H4 no cart consumer (checkout reads the cart synchronously), H5 the policy
PurgeAbandonedCarts, H6 the estimate without a destination shows the item subtotal
only with `estimateComplete=false`, and checkout H10 the command AcceptRequotedOffers.
This ADR records the aggregate as built and every assumption (A1, A2, ...) taken where
the model and the decisions are silent. Each is also reported as a `model_gap`.

## Decision

### Aggregate

- **Cart** (`internal/domain/cart`, root `cartId`): optional `shopperId`, lines
  `{catalogItemId, quantity}` (one per item, at most 100, quantity 1..99),
  `destinationPostalPrefix`, `deliveryQuote` (groups with line numbers and offers
  `{offerId, groupNo, tier, deliverByEarliest, deliverByLatest, expiresAt}`, plus the
  unavailable tiers with their constraint), `selections` (one offer per group),
  `version`.
  - States `OPEN -> CHECKED_OUT | ABANDONED | MERGED`; the three targets are terminal.
  - Errors: `ErrQuantityOutOfRange`, `ErrTooManyLines`, `ErrItemNotPurchasable`,
    `ErrNoDestination`, `ErrStaleDeliverySelection`, `ErrCartClosed`, `ErrMergeTarget`,
    plus `ErrLineNotFound` (A2) and `ErrNotIdle` (never reaches a caller).
  - Any line or destination change clears the quote and the selections.
- **estimate** (`internal/domain/estimate`): the pure computation of the labelled,
  non-binding Estimate. It decides nothing; it reports what is known.
- **shared** (`internal/domain/shared`): errors, `Event`, id and time helpers.

### Assumptions

- **A1 Merge target.** The model says "an OPEN cart of the same shopper". The
  sign-in moment (D4) merges a GUEST cart (no `shopperId`) into the shopper's cart, so
  the source is a guest cart or a cart of the same shopper, and the target must be
  another OPEN cart that HAS a `shopperId` (never the cart itself; never a guest
  target). Quantities of items in both carts add up, clamped at 99 (a merge is not
  refused for a quantity); if the target would exceed 100 lines the merge is refused
  with `ErrTooManyLines` and neither cart changes. `MergeInto` returns the target.
- **A2 Unknown line.** `SetLineQuantity` and `RemoveLine` on an item the cart does not
  hold return `ErrLineNotFound`, problem type `not-found` (404).
- **A3 Postal prefix.** 1 to 10 ASCII letters or digits (no spaces). Tax bands compare
  the first three characters, upper-cased, exactly as pricing's `RateFor`; a prefix
  shorter than three characters finds no band, so the estimate stays incomplete.
- **A4 AddLine check order.** Cart open, `catalogItemId` valid, requested quantity in
  1..99, item purchasable, MERGED quantity in 1..99 (adding 40 to a line of 60 is
  `ErrQuantityOutOfRange`), line cap (a merge into an existing line never hits it).
- **A5 Purchasability and ordering.** See ADR 0003: `catalogued AND NOT discontinued
  AND priced`, stub rows, price writes guarded by the event `version`.
- **A6 Estimate arithmetic.** Mirrors pricing's quote: goods = sum of effective price
  x quantity; a group is charged its selected tier from the current schedule and is
  free when its goods reach the tier's `freeOverMinor` (0 means none); tax = band rate
  applied to goods plus delivery, rounded half up to a minor unit; total = goods +
  delivery + tax. Without a destination only the goods show (H6). The estimate is
  complete only when the destination, a band, the schedule, a price for every line and
  a selection for every group are all known; otherwise the figures cover what is known.
- **A7 Currency.** The estimate currency is the currency of the priced lines; with no
  priced line it is `CART_CURRENCY` (default `USD`).
- **A8 Readiness.** Derived at read time: every group of the quote has a selected offer
  of that quote and no selected offer has `expiresAt <= now`. A quote without a group is
  never ready. Expiry is a fact about the clock, not a stored flag.
- **A9 Concurrency.** Every save is conditional on the loaded `version`; a command runs
  twice before it reports 409 `concurrent-update`. `QuoteDelivery` calls
  delivery-promise OUTSIDE any transaction, then reloads the cart; when the cart
  changed meanwhile the quote is stale and the whole attempt reruns once.
- **A10 Idempotency key forwarding.** The request's `Idempotency-Key` is also sent to
  delivery-promise on `QuoteDelivery`, so a retry inside the offers' TTL returns the
  same offers.
- **A11 AcceptRequotedOffers.** The supplied set (one offer per group, distinct
  `offerId`s, valid tiers and dates) REPLACES the quote and each offer is its group's
  selection. A group keeps the line numbers of the previous quote's group with the same
  number; a single new group without predecessor spans every line; the unavailable
  tiers are dropped; `quotedAt` is the command's clock. It never calls delivery-promise
  and raises no event; checkout re-verifies every offer.
- **A12 Retention.** AbandonInactive abandons an OPEN cart idle for 30 days.
  PurgeAbandonedCarts deletes every cart that is NOT open (ABANDONED, CHECKED_OUT,
  MERGED) and was last changed 30 days ago or more, so an idle cart is gone about 60
  days after its last change. Both run on a timer (`ABANDON_SWEEP_INTERVAL` default 1h,
  `PURGE_SWEEP_INTERVAL` default 6h) in batches of 200 and are idempotent.
- **A13 CheckoutCompleted anomalies.** For an unknown or non-OPEN cart the event is
  acknowledged, the inbox row is written, a warning is logged and nothing is published.
- **A14 Quote mapping.** From delivery-promise's answer only `QUOTED` offers are kept;
  the group number and the line numbers (1-based, cart order) come from the offers'
  `groupNo` and `lines`; a group known only through a constraint spans every line when
  it is the only group; `deliverBy*` are the UTC dates of the offer window; `quotedAt`
  is the earliest `quotedAt` of the offers (the clock when there is none). The domain
  checks only that line numbers are in range and not shared by two groups.
- **A15 Lists.** `GET /carts` requires `shopperId` (guest carts are reachable by id
  only), newest first by creation time then id.
- **A16 Idle clock.** Every successful change (including a selection) moves
  `lastChangedAt`.
- **A17 Timestamps.** Truncated to the microsecond Postgres stores, so a round trip
  never changes an instant.

## Consequences

- The aggregate rejects with typed errors; the HTTP adapter maps each to the problem
  type in `apis/openapi.yaml`.
- Every assumption is testable: each has a domain, use case or repository contract test.
