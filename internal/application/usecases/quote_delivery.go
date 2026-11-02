package usecases

import (
	"context"
	"errors"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// QuoteDelivery is the command QuoteDelivery (POST /carts/{cartId}/delivery-quotes):
// the cart's only synchronous call, to delivery-promise QuoteDeliveryOffers.
type QuoteDelivery struct{ Deps }

// Execute quotes the cart's lines to its destination. The remote call runs
// OUTSIDE any transaction: the cart is read, delivery-promise is asked, then
// the cart is reloaded and the quote stored in one unit of work. When the cart
// changed in between, the quote is stale and the whole attempt runs again once
// before the conflict is reported.
func (u QuoteDelivery) Execute(ctx context.Context, cartID, idempotencyKey string) (views.Cart, error) {
	var err error
	for attempt := 0; attempt < commandAttempts; attempt++ {
		var v views.Cart
		if v, err = u.attempt(ctx, cartID, idempotencyKey); !errors.Is(err, ports.ErrConcurrentUpdate) {
			return v, err
		}
	}
	return views.Cart{}, err
}

func (u QuoteDelivery) attempt(ctx context.Context, cartID, key string) (views.Cart, error) {
	c, err := u.Carts.Get(ctx, cart.ID(cartID))
	if err != nil {
		return views.Cart{}, err
	}
	if c.State() != cart.StateOpen {
		return views.Cart{}, cart.ErrCartClosed
	}
	if c.DestinationPostalPrefix() == "" {
		return views.Cart{}, cart.ErrNoDestination
	}
	quote, err := u.Quoter.Quote(ctx, key, c.DestinationPostalPrefix(), c.Lines())
	if err != nil {
		return views.Cart{}, err
	}
	seen := c.Version()
	return transact(ctx, u.UoW, 1, func(ctx context.Context) (views.Cart, error) {
		fresh, err := u.Carts.Get(ctx, cart.ID(cartID))
		if err != nil {
			return views.Cart{}, err
		}
		if fresh.Version() != seen {
			return views.Cart{}, ports.ErrConcurrentUpdate
		}
		if err := fresh.QuoteDelivery(quote, u.now()); err != nil {
			return views.Cart{}, err
		}
		if err := u.persist(ctx, fresh); err != nil {
			return views.Cart{}, err
		}
		return u.view(ctx, fresh)
	})
}
