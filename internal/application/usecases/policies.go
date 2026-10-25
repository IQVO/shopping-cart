package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// AbandonInactive is the policy AbandonInactive: an OPEN cart with no change
// for 30 days is ABANDONED and CartAbandoned is raised.
type AbandonInactive struct{ Deps }

// Execute abandons up to limit idle carts and returns how many it abandoned.
// A cart that changed or closed since the scan is skipped, never failed, so a
// second sweep is harmless.
func (u AbandonInactive) Execute(ctx context.Context, limit int) (int, error) {
	idle, err := u.Carts.ListIdleOpen(ctx, u.now().Add(-cart.IdleAfter), limit)
	if err != nil {
		return 0, err
	}
	abandoned := 0
	for _, c := range idle {
		_, err := u.mutate(ctx, string(c.ID()), func(_ context.Context, c *cart.Cart) error { return c.AbandonInactive(u.now()) })
		switch {
		case err == nil:
			abandoned++
		case errors.Is(err, cart.ErrNotIdle), errors.Is(err, cart.ErrCartClosed),
			errors.Is(err, ports.ErrConcurrentUpdate), isNotFound(err):
		default:
			return abandoned, err
		}
	}
	return abandoned, nil
}

// PurgeAbandonedCarts is the policy PurgeAbandonedCarts (decision H5): carts
// that are no longer OPEN and have been idle for 30 days are deleted. It
// raises no event.
type PurgeAbandonedCarts struct{ Deps }

// Execute deletes up to limit such carts and returns how many.
func (u PurgeAbandonedCarts) Execute(ctx context.Context, limit int) (int, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (int, error) {
		return u.Carts.DeleteClosedBefore(ctx, u.now().Add(-cart.IdleAfter), limit)
	})
}
