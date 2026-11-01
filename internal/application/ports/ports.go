// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// sentinels of errors.go and the data packages readmodel and idempotency.
//
// Optimistic concurrency: an aggregate is saved under the rule "stored version
// == LoadedVersion()" (several commands may run between a load and a save); a
// create (LoadedVersion 0) expects that the id is not stored. Any mismatch,
// including an update of a row that no longer exists, is ErrConcurrentUpdate.
// After a successful Save the adapter calls MarkPersisted(). Inside a
// UnitOfWork every repository joins the transaction carried by the ctx the unit
// handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/idempotency"
	"github.com/IQVO/shopping-cart/internal/application/pagination"
	"github.com/IQVO/shopping-cart/internal/application/readmodel"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// CartRepository persists Cart aggregates.
type CartRepository interface {
	// Get returns the cart, or ErrNotFound.
	Get(ctx context.Context, id cart.ID) (*cart.Cart, error)
	// Save persists the cart snapshot under the concurrency rule of the
	// package comment.
	Save(ctx context.Context, c *cart.Cart) error
	// ListByShopper returns up to limit of the shopper's carts ordered by
	// (created-at, id) descending, optionally only those in state (empty state
	// means every state). A non-nil after resumes strictly after that keyset,
	// so carts sharing a created-at are never skipped or repeated.
	ListByShopper(ctx context.Context, shopperID string, state cart.State, after *pagination.Keyset, limit int) ([]*cart.Cart, error)
	// ListIdleOpen returns up to limit OPEN carts last changed at or before the
	// instant, oldest first (policy AbandonInactive).
	ListIdleOpen(ctx context.Context, cutoff time.Time, limit int) ([]*cart.Cart, error)
	// DeleteClosedBefore deletes up to limit carts that are not OPEN and were
	// last changed at or before the instant, and returns how many it deleted
	// (policy PurgeAbandonedCarts; no event).
	DeleteClosedBefore(ctx context.Context, cutoff time.Time, limit int) (int, error)
}

// ItemRepository stores PurchasabilityView (ADR 0003). Writes are upserts that
// tolerate any arrival order; price writes are guarded by the event version.
type ItemRepository interface {
	// Get returns the item, or ErrNotFound.
	Get(ctx context.Context, catalogItemID string) (readmodel.Item, error)
	// GetMany returns the known items by id; unknown ids are absent.
	GetMany(ctx context.Context, ids []string) (map[string]readmodel.Item, error)
	// MarkCatalogued records CatalogItemCreated; a known row keeps its
	// discontinuation and price.
	MarkCatalogued(ctx context.Context, catalogItemID string, at time.Time) error
	// MarkDiscontinued records CatalogItemDiscontinued (terminal).
	MarkDiscontinued(ctx context.Context, catalogItemID string, at time.Time) error
	// ApplyPrice records an effective price when version is newer than the
	// stored price version; an older or equal version is ignored.
	ApplyPrice(ctx context.Context, catalogItemID, currency string, priceMinor, version int64, at time.Time) error
	// ApplyWithdrawn clears the price when version is newer than the stored
	// price version, and remembers the version so a late older price is dropped.
	ApplyWithdrawn(ctx context.Context, catalogItemID string, version int64, at time.Time) error
}

// ScheduleRepository stores the current delivery charge schedule (EstimateView).
type ScheduleRepository interface {
	// Current returns the stored schedule, or ErrNotFound.
	Current(ctx context.Context) (estimate.Schedule, error)
	// Replace stores the schedule when its version is higher than the stored
	// one (or none is stored) and reports whether it did.
	Replace(ctx context.Context, s estimate.Schedule) (bool, error)
}

// TaxTableRepository stores the current tax table (EstimateView).
type TaxTableRepository interface {
	// Current returns the stored table, or ErrNotFound.
	Current(ctx context.Context) (estimate.TaxTable, error)
	// Replace stores the table when its version is higher than the stored one
	// (or none is stored) and reports whether it did.
	Replace(ctx context.Context, t estimate.TaxTable) (bool, error)
}

// QuoteOffers is the cart's only synchronous dependency: delivery-promise
// QuoteDeliveryOffers (REST POST /delivery-quotes).
type QuoteOffers interface {
	// Quote asks for the offers of the lines to the destination prefix and
	// returns them as a cart quote stamped quotedAt. The idempotency key is
	// forwarded so a repeat inside the offers' TTL returns the same offers.
	Quote(ctx context.Context, idempotencyKey, destinationPostalPrefix string, lines []cart.Line) (cart.DeliveryQuote, error)
}

// Inbox makes consumers idempotent on the CloudEvents id.
type Inbox interface {
	// Claim records the event id and reports whether this is the first time
	// it is seen. Called with the ctx of a unit of work, the claim rolls back
	// with it.
	Claim(ctx context.Context, eventID, eventType string) (first bool, err error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository, inbox and outbox
// call fn makes with the ctx it is handed commits together when fn returns
// nil, or rolls back together when fn returns an error (or panics). Do is
// re-entrant; the error fn returns is returned unchanged. A Do inside a Do
// joins the outer unit but is atomic on its own: when the inner fn fails only
// the inner writes are undone (the retry loops of the use cases rely on it).
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// IdempotencyStore remembers the first answer per Idempotency-Key for 24 hours and
// serialises the requests that share a key (REST guideline 2.3 and 2.5). The key is
// global: the same key on another route is a different fingerprint (a reuse).
type IdempotencyStore interface {
	// Do runs fn at most once for a live key.
	//
	// A live record is returned with replayed true and fn does not run; the
	// caller compares the record's RequestHash with its own. Otherwise Do claims
	// the key and runs fn with a ctx that carries the unit of work: everything fn
	// writes through the repositories, the outbox included, commits atomically
	// with the record fn returns, stamped with at. A record with a status below
	// 500 is stored (a 4xx too, so a retry is deterministic). A 5xx record, an
	// error or a panic rolls everything back and stores nothing, so the retry
	// runs fn again. A concurrent Do for the same key waits for the claimant,
	// then replays its answer (or runs fn itself when nothing was stored).
	Do(ctx context.Context, key, requestHash string, at time.Time,
		fn func(ctx context.Context) (idempotency.Record, error)) (rec idempotency.Record, replayed bool, err error)
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the ids of new aggregates.
type IDGenerator interface {
	// NewCartID mints the id of a new cart.
	NewCartID() string
}

// CartMetrics records the business outcome of every AddLine attempt (Tier 2 of
// the fleet metrics convention). The use case calls it once per attempt:
// LineAdded when the line was stored (a merged quantity counts), LineRejected
// otherwise, with the problem slug that says why (the `type` suffix the REST
// adapter answers with: "item-not-purchasable", "quantity-out-of-range", ...).
// The implementation lives in internal/adapters/outbound/telemetry; a nil
// Deps.Metrics records nothing.
type CartMetrics interface {
	// LineAdded counts a line that was added to a cart.
	LineAdded(ctx context.Context)
	// LineRejected counts an AddLine that stored nothing; reason is a stable,
	// low-cardinality slug.
	LineRejected(ctx context.Context, reason string)
}
