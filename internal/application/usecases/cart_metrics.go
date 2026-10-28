package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// rejectionReasons maps an AddLine failure to the problem slug the REST adapter
// answers with (same vocabulary as apis/openapi.yaml), in match order.
var rejectionReasons = []struct {
	err    error
	reason string
}{
	{cart.ErrItemNotPurchasable, "item-not-purchasable"},
	{cart.ErrQuantityOutOfRange, "quantity-out-of-range"},
	{cart.ErrTooManyLines, "too-many-lines"},
	{cart.ErrCartClosed, "cart-closed"},
	{ports.ErrNotFound, "not-found"},
	{ports.ErrConcurrentUpdate, "concurrent-update"},
	{shared.ErrValidation, "validation-failed"},
}

// internalErrorReason is the reason of every failure that is not a refusal the
// caller can act on (a database error, a bug).
const internalErrorReason = "internal-error"

// recordOutcome counts one AddLine attempt: added when err is nil, else
// rejected with the reason of err.
func (u AddLine) recordOutcome(ctx context.Context, err error) {
	if err == nil {
		u.metrics().LineAdded(ctx)
		return
	}
	u.metrics().LineRejected(ctx, rejectionReason(err))
}

func rejectionReason(err error) string {
	for _, r := range rejectionReasons {
		if errors.Is(err, r.err) {
			return r.reason
		}
	}
	return internalErrorReason
}
