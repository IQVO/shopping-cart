package usecases

import (
	"context"

	"github.com/IQVO/shopping-cart/internal/application/pagination"
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

// ListCarts is the query ListCarts (GET /carts?shopperId=&state=&limit=&cursor=).
type ListCarts struct{ Deps }

// CartPage is one page of ListCarts: at most the requested number of carts,
// newest first, and the keyset to resume after when more carts follow.
type CartPage struct {
	Items []views.Cart
	// Next is nil on the last page.
	Next *pagination.Keyset
}

// Execute returns one page of the shopper's carts, newest first (created-at,
// id descending), optionally of one state, starting strictly after the
// keyset (nil is the first page). limit must be at least 1.
func (u ListCarts) Execute(ctx context.Context, shopperID string, state cart.State, after *pagination.Keyset, limit int) (CartPage, error) {
	// One row more than the page tells whether another page exists.
	carts, err := u.Carts.ListByShopper(ctx, shopperID, state, after, limit+1)
	if err != nil {
		return CartPage{}, err
	}
	var next *pagination.Keyset
	if len(carts) > limit {
		carts = carts[:limit]
		last := carts[limit-1]
		next = &pagination.Keyset{CreatedAt: last.CreatedAt(), ID: string(last.ID())}
	}
	out := make([]views.Cart, 0, len(carts))
	for _, c := range carts {
		v, err := u.view(ctx, c)
		if err != nil {
			return CartPage{}, err
		}
		out = append(out, v)
	}
	return CartPage{Items: out, Next: next}, nil
}
