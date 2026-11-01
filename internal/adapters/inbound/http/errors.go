package http

import (
	"errors"
	"net/http"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// problemBase is the namespace of this service's RFC 9457 `type` URIs.
const problemBase = "/problems/"

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

// problemCatalogue maps every typed error to its problem, in match order. The
// slugs are the ones of apis/openapi.yaml and .claude/rules/rest-api.md.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{http.StatusBadRequest, "malformed-request", "Malformed request"}},
	{ports.ErrNotFound, notFound},
	{cart.ErrLineNotFound, notFound},
	{cart.ErrCartClosed, conflict("cart-closed", "A checked-out, abandoned or merged cart never changes")},
	{cart.ErrStaleDeliverySelection, conflict("stale-delivery-selection", "A selection must name an offer of the current delivery quote")},
	{cart.ErrItemNotPurchasable, unprocessable("item-not-purchasable", "The item is not purchasable")},
	{cart.ErrQuantityOutOfRange, unprocessable("quantity-out-of-range", "Quantity must be between 1 and 99")},
	{cart.ErrTooManyLines, unprocessable("too-many-lines", "A cart holds at most 100 lines")},
	{cart.ErrNoDestination, unprocessable("no-destination", "Set a destination postal prefix first")},
	{cart.ErrMergeTarget, unprocessable("merge-target", "The merge target must be another open cart of the same shopper")},
	{ports.ErrDeliveryPromiseUnavailable, deliveryUnavailable},
	{ports.ErrQuoteRejected, deliveryUnavailable},
	{ports.ErrConcurrentUpdate, conflict("concurrent-update", "The resource was modified by another request; retry")},
	{shared.ErrIllegalTransition, conflict("illegal-transition", "The cart does not allow this change in its current state")},
	{shared.ErrValidation, unprocessable("validation-failed", "Validation failed")},
}

// problemFor returns the problem of the first catalogue entry err matches, or
// the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
