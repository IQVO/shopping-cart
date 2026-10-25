package usecases

import (
	"context"

	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// GetCart is the query GetCart (GET /carts/{cartId}), also called by checkout
// at session open.
type GetCart struct{ Deps }

// Execute returns the cart with its labelled estimate and checkout readiness.
func (u GetCart) Execute(ctx context.Context, cartID string) (views.Cart, error) {
	c, err := u.Carts.Get(ctx, cart.ID(cartID))
	if err != nil {
		return views.Cart{}, err
	}
	return u.view(ctx, c)
}

// ListCarts is the query ListCarts (GET /carts?shopperId=&state=).
type ListCarts struct{ Deps }

// Execute returns the shopper's carts, newest first, optionally of one state.
func (u ListCarts) Execute(ctx context.Context, shopperID string, state cart.State) ([]views.Cart, error) {
	carts, err := u.Carts.ListByShopper(ctx, shopperID, state)
	if err != nil {
		return nil, err
	}
	out := make([]views.Cart, 0, len(carts))
	for _, c := range carts {
		v, err := u.view(ctx, c)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
