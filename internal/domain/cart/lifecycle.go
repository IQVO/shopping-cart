package cart

import (
	"errors"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// ErrNotIdle: AbandonInactive was called before the cart idled IdleAfter.
var ErrNotIdle = errors.New("cart not idle")

// IdleAfter is how long a cart may stay unchanged before it is abandoned
// (policy AbandonInactive) or, once idle, purged (policy PurgeAbandonedCarts).
const IdleAfter = 30 * 24 * time.Hour

// IdleSince reports whether the cart has been unchanged for IdleAfter at now.
func (c *Cart) IdleSince(now time.Time) bool { return !now.Before(c.lastChangedAt.Add(IdleAfter)) }

// AbandonInactive moves an OPEN cart that idled IdleAfter to ABANDONED and
// raises CartAbandoned carrying the last change instant.
func (c *Cart) AbandonInactive(now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if !c.IdleSince(now) {
		return ErrNotIdle
	}
	last := c.lastChangedAt
	c.state = StateAbandoned
	at := c.touch(now)
	c.pending = append(c.pending, CartAbandoned{
		Header: shared.Header{Version: c.version, At: at}, CartID: c.id, LastChangedAt: last,
	})
	return nil
}

// CloseOnCheckoutCompleted moves an OPEN cart to CHECKED_OUT and raises
// CartCheckedOut carrying the checkout session id.
func (c *Cart) CloseOnCheckoutCompleted(sessionID string, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if !shared.ValidID(sessionID) {
		return shared.Invalid("checkout sessionId must be a valid id")
	}
	c.state = StateCheckedOut
	at := c.touch(now)
	c.pending = append(c.pending, CartCheckedOut{
		Header: shared.Header{Version: c.version, At: at}, CartID: c.id, CheckoutSessionID: sessionID,
	})
	return nil
}

// MergeInto takes this cart's lines into the target (merging quantities,
// clamped at MaxQuantity), clears the target's quote and selections, and marks
// this cart MERGED. The target must be another OPEN cart of a shopper; this
// cart is either a guest cart or one of the same shopper (ADR 0002, A1).
// Both carts are untouched when it fails. Raises no event.
func (c *Cart) MergeInto(target *Cart, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if target == c || target.id == c.id || target.shopperID == "" || (c.shopperID != "" && c.shopperID != target.shopperID) {
		return ErrMergeTarget
	}
	if err := target.open(); err != nil {
		return ErrMergeTarget
	}
	lines, err := mergedLines(target.lines, c.lines)
	if err != nil {
		return err
	}
	target.lines = lines
	target.clearQuote()
	target.touch(now)
	c.state = StateMerged
	c.clearQuote()
	c.touch(now)
	return nil
}

func mergedLines(into, from []Line) ([]Line, error) {
	out := append([]Line(nil), into...)
	for _, l := range from {
		i := indexIn(out, l.CatalogItemID)
		switch {
		case i >= 0:
			out[i].Quantity = min(out[i].Quantity+l.Quantity, MaxQuantity)
		case len(out) >= MaxLines:
			return nil, ErrTooManyLines
		default:
			out = append(out, l)
		}
	}
	return out, nil
}

func indexIn(lines []Line, item string) int {
	for i, l := range lines {
		if l.CatalogItemID == item {
			return i
		}
	}
	return -1
}
