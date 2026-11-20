package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/shopping-cart/internal/adapters/inbound/http/page"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// problemBase is the namespace of this service's RFC 9457 `type` URIs.
const problemBase = "/problems/"

// errUnknownQueryParameter marks a query parameter the operation does not declare.
var errUnknownQueryParameter = errors.New("query parameter is not declared by this operation")

// errMalformedRequest marks a body or parameter the adapter itself rejects
// (bad JSON, unknown field, wrong type).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title) of one error category; the detail
// comes from the error at write time.
type problem struct {
	status int
	slug   string
	title  string
}

var internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

// Problems the adapter writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required"}
	idempotencyKeyInvalid  = problem{http.StatusBadRequest, "idempotency-key-invalid", "Idempotency-Key header is invalid"}
	idempotencyKeyReused   = problem{http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request"}
	invalidLimit           = problem{http.StatusBadRequest, "invalid-limit", "limit must be an integer between 1 and 500"}
	invalidCursor          = problem{http.StatusBadRequest, "invalid-cursor", "cursor is not a valid page token"}
	unknownQueryParameter  = problem{http.StatusBadRequest, "unknown-query-parameter", "The request carries a query parameter this operation does not declare"}
)

func unprocessable(slug, title string) problem {
	return problem{http.StatusUnprocessableEntity, slug, title}
}

func conflict(slug, title string) problem {
	return problem{http.StatusConflict, slug, title}
}

var (
	notFound            = problem{http.StatusNotFound, "not-found", "Not found"}
	deliveryUnavailable = problem{http.StatusServiceUnavailable, "delivery-promise-unavailable", "delivery-promise is unavailable; retry with the same Idempotency-Key"}
)

// replayClass is what the Idempotency-Key middleware does with the answer of a
// problem type (ADR 0011): FINAL and OUTCOME answers are stored and replayed, a
// RETRYABLE one is not stored and its key is released. A 2xx is stored and a 5xx
// never is, whatever the class. The empty class is "unclassified".
type replayClass string

const (
	// replayFinal: the answer is a function of the request (or of state that
	// only ever moves one way) alone; stored and replayed.
	replayFinal replayClass = "FINAL"
	// replayOutcome: the command committed state and then refused; the writes
	// commit with the stored answer. No problem type of this service is one
	// (every command's writes ride the unit of work, which rolls its savepoint
	// back when the attempt returns the error).
	replayOutcome replayClass = "OUTCOME"
	// replayRetryable: nothing committed and the answer depends on state that
	// can change; not stored, the key is released, a retry executes again.
	replayRetryable replayClass = "RETRYABLE"
)

// stored reports whether an error answer of this class is remembered under its key.
func (c replayClass) stored() bool { return c == replayFinal || c == replayOutcome }

// catalogueEntry is one typed error, its problem and the replay class of that problem.
type catalogueEntry struct {
	err    error
	p      problem
	replay replayClass
}

// problemCatalogue maps every typed error to its problem and its replay class,
// in match order. The slugs are the ones of apis/openapi.yaml and
// .claude/rules/rest-api.md. ADR 0011 holds the reason of every class and a
// test keeps its table equal to this one; a new entry without a class fails
// TestEveryProblemTypeDeclaresAReplayClass. When unsure the class is RETRYABLE:
// a released refusal costs one more execution, a wrongly stored one pins a
// stale answer for 24 hours.
var problemCatalogue = []catalogueEntry{
	// decodeJSON rejects the body before any use case runs (response.go): a function of the request alone.
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}, replayFinal},
	// GET /carts only (a list parameter); classified for completeness.
	{page.ErrInvalidLimit, invalidLimit, replayFinal},
	// GET only.
	{page.ErrInvalidCursor, invalidCursor, replayFinal},
	// GET only.
	{errUnknownQueryParameter, unknownQueryParameter, replayFinal},
	// Shared slug, mixed causes: a cart of the path (ids minted here, but the PurgeAbandonedCarts policy DELETES abandoned carts), a line not in the cart yet (ErrLineNotFound below), and an empty or overlong path id (handlers.go pathID): RETRYABLE.
	{ports.ErrNotFound, notFound, replayRetryable},
	// The cart holds no line for the item (lines.go SetLineQuantity/RemoveLine): another request can add it.
	{cart.ErrLineNotFound, notFound, replayRetryable},
	// CHECKED_OUT, ABANDONED and MERGED are terminal (cart.go: a closed cart never changes; no command reopens one), so the answer cannot change by retrying.
	{cart.ErrCartClosed, conflict("cart-closed", "A checked-out, abandoned or merged cart never changes"), replayFinal},
	// The selection must name an offer of the CURRENT quote (delivery.go SelectDelivery); a re-quote replaces the quote and can bring that offer back.
	{cart.ErrStaleDeliverySelection, conflict("stale-delivery-selection", "A selection must name an offer of the current delivery quote"), replayRetryable},
	// The PurchasabilityView is a read model fed by offer-availability events (ADR 0003): the fact can arrive after the request.
	{cart.ErrItemNotPurchasable, unprocessable("item-not-purchasable", "The item is not purchasable"), replayFinal},
	// The request's own quantity against 1..99 (lines.go): the request alone.
	{cart.ErrQuantityOutOfRange, unprocessable("quantity-out-of-range", "Quantity must be between 1 and 99"), replayFinal},
	// The 100-line cap counts the cart's CURRENT lines (lines.go AddLine; lifecycle.go mergedLines): remove a line and the same request passes.
	{cart.ErrTooManyLines, unprocessable("too-many-lines", "A cart holds at most 100 lines"), replayRetryable},
	// The destination is set by another request (PUT /destination; delivery.go, and quote_delivery.go raises it before the remote call).
	{cart.ErrNoDestination, unprocessable("no-destination", "Set a destination postal prefix first"), replayRetryable},
	// Mixed: the target that IS the request's own cart (request-only) and the target's state and shopper read from the store (lifecycle.go MergeInto). One class per slug, so RETRYABLE; splitting the self-target cause out is a recorded follow-up.
	{cart.ErrMergeTarget, unprocessable("merge-target", "The merge target must be another open cart of the same shopper"), replayRetryable},
	// A 5xx is never stored; delivery-promise comes back.
	{ports.ErrDeliveryPromiseUnavailable, deliveryUnavailable, replayRetryable},
	// Shares the 503 of delivery-promise-unavailable: a 5xx is never stored.
	{ports.ErrQuoteRejected, deliveryUnavailable, replayRetryable},
	// The optimistic-lock version guard (postgres/carts.go Save: zero affected rows) after the use case's own commandAttempts (deps.go transact).
	{ports.ErrConcurrentUpdate, conflict("concurrent-update", "The resource was modified by another request; retry"), replayFinal},
	// No code path of this service raises it today; unsure, so RETRYABLE.
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The cart does not allow this change in its current state"), replayRetryable},
	// Every raise is a value-object rule on the request's own fields (shared.Invalid: ids, the destination prefix format, the offers of a requote body).
	{shared.ErrValidation, unprocessable("validation-failed", "Validation failed"), replayFinal},
}

// entryFor returns the first catalogue entry err matches; an unmatched error is
// the internal-error problem with no class (a 5xx, never stored).
func entryFor(err error) catalogueEntry {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry
		}
	}
	return catalogueEntry{p: internalProblem}
}
