# Domain model: ubiquitous language, aggregates, events, use cases

## Ubiquitous Language (use these exact names)

- **Cart** — one shopper's held intent: lines, destination postal prefix, delivery quote and selections. One line per item.
- **Delivery Quote (cart)** — the offers delivery-promise returned for the cart; holds no capacity. Not the network's DeliveryOffer.
- **Delivery Selection** — the offer picked per shipment group; must belong to the current quote.
- **Estimate** — labelled, non-binding total (prices + selected tier charges + tax band); `estimateComplete=false` until everything is known. Not checkout's PriceQuote.
- **Checkout Readiness** — every group selected and no selected offer expired; derived, never stored.
- **PurchasabilityView / EstimateView** — event-fed local read models (catalogued, not discontinued, priced; current schedule and tax table). Lenient by design.

## Aggregates

- **Cart** (`internal/domain/cart`): OPEN -> CHECKED_OUT | ABANDONED | MERGED. Quantity 1..99, 100 lines, merge on re-add, any line/destination change clears quote and selections (`ErrQuantityOutOfRange`, `ErrTooManyLines`, `ErrItemNotPurchasable`, `ErrNoDestination`, `ErrStaleDeliverySelection`, `ErrCartClosed`, `ErrMergeTarget`).
- `internal/domain/estimate`: pure Estimate computation. `internal/domain/shared`: ids, errors, Event.

## Domain events

- `CartLineAdded` — AddLine (merged quantity). `CartDeliveryQuoted` — QuoteDelivery. `CartAbandoned` — AbandonInactive (30 idle days). `CartCheckedOut` — CloseOnCheckoutCompleted. All analytics-only; removal, merge, requote raise none.

## Key use cases (`internal/application/usecases`)

- `CreateCart`, `AddLine`, `SetLineQuantity`, `RemoveLine`, `SetDestination`, `SelectDelivery`, `MergeInto`, `AcceptRequotedOffers` — load, call the aggregate, save + outbox in one UnitOfWork.
- `QuoteDelivery` — calls the `QuoteOffers` port (delivery-promise) outside the transaction, then stores the quote.
- `AbandonInactive`, `PurgeAbandonedCarts` — scheduled policies. `Consumer.Apply*` — inbox claim + read-model update / CloseOnCheckoutCompleted.
