package usecases

import (
	"context"

	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// mutate is the shape of every command on one existing cart: load it, let the
// domain change it, save it with the events it raised, return the view.
func (d Deps) mutate(ctx context.Context, id string, change func(ctx context.Context, c *cart.Cart) error) (views.Cart, error) {
	return transact(ctx, d.UoW, commandAttempts, func(ctx context.Context) (views.Cart, error) {
		c, err := d.Carts.Get(ctx, cart.ID(id))
		if err != nil {
			return views.Cart{}, err
		}
		if err := change(ctx, c); err != nil {
			return views.Cart{}, err
		}
		if err := d.persist(ctx, c); err != nil {
			return views.Cart{}, err
		}
		return d.view(ctx, c)
	})
}

// CreateCart is the command CreateCart (POST /carts). It raises no event.
type CreateCart struct{ Deps }

// Execute opens an empty OPEN cart for the optional shopper.
func (u CreateCart) Execute(ctx context.Context, shopperID string) (views.Cart, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.Cart, error) {
		c, err := cart.Create(cart.ID(u.IDs.NewCartID()), shopperID, u.now())
		if err != nil {
			return views.Cart{}, err
		}
		if err := u.persist(ctx, c); err != nil {
			return views.Cart{}, err
		}
		return u.view(ctx, c)
	})
}

// AddLine is the command AddLine (POST /carts/{cartId}/lines).
type AddLine struct{ Deps }

// Execute adds the item, merging quantities when the cart already holds it.
// Purchasability is the lenient PurchasabilityView rule (decision H3).
func (u AddLine) Execute(ctx context.Context, cartID, catalogItemID string, quantity int) (views.Cart, error) {
	return u.mutate(ctx, cartID, func(ctx context.Context, c *cart.Cart) error {
		item, err := u.Items.Get(ctx, catalogItemID)
		if err != nil && !isNotFound(err) {
			return err
		}
		return c.AddLine(catalogItemID, quantity, err == nil && item.Purchasable(), u.now())
	})
}

// SetLineQuantity is the command SetLineQuantity.
type SetLineQuantity struct{ Deps }

// Execute sets the quantity of the cart's line for the item.
func (u SetLineQuantity) Execute(ctx context.Context, cartID, catalogItemID string, quantity int) (views.Cart, error) {
	return u.mutate(ctx, cartID, func(_ context.Context, c *cart.Cart) error {
		return c.SetLineQuantity(catalogItemID, quantity, u.now())
	})
}

// RemoveLine is the command RemoveLine.
type RemoveLine struct{ Deps }

// Execute removes the cart's line for the item.
func (u RemoveLine) Execute(ctx context.Context, cartID, catalogItemID string) (views.Cart, error) {
	return u.mutate(ctx, cartID, func(_ context.Context, c *cart.Cart) error { return c.RemoveLine(catalogItemID, u.now()) })
}

// SetDestination is the command SetDestination.
type SetDestination struct{ Deps }

// Execute sets the destination postal prefix.
func (u SetDestination) Execute(ctx context.Context, cartID, prefix string) (views.Cart, error) {
	return u.mutate(ctx, cartID, func(_ context.Context, c *cart.Cart) error { return c.SetDestination(prefix, u.now()) })
}

// SelectDelivery is the command SelectDelivery.
type SelectDelivery struct{ Deps }

// Execute records the shopper's pick of an offer of the current quote.
func (u SelectDelivery) Execute(ctx context.Context, cartID string, groupNo int, offerID string) (views.Cart, error) {
	return u.mutate(ctx, cartID, func(_ context.Context, c *cart.Cart) error { return c.SelectDelivery(groupNo, offerID, u.now()) })
}

// AcceptRequotedOffers is the command AcceptRequotedOffers
// (POST /carts/{cartId}/delivery/requoted). It never calls delivery-promise.
type AcceptRequotedOffers struct{ Deps }

// Execute writes the offers of a checkout requote as the cart's quote and
// selection; checkout re-verifies them.
func (u AcceptRequotedOffers) Execute(ctx context.Context, cartID string, offers []cart.QuotedOffer) (views.Cart, error) {
	return u.mutate(ctx, cartID, func(_ context.Context, c *cart.Cart) error { return c.AcceptRequotedOffers(offers, u.now()) })
}

// MergeInto is the command MergeInto (POST /carts/{cartId}:merge-into).
type MergeInto struct{ Deps }

// Execute takes the source cart's lines into the target and closes the source;
// both saves are one unit of work. It returns the target cart.
func (u MergeInto) Execute(ctx context.Context, sourceID, targetID string) (views.Cart, error) {
	return transact(ctx, u.UoW, commandAttempts, func(ctx context.Context) (views.Cart, error) {
		src, err := u.Carts.Get(ctx, cart.ID(sourceID))
		if err != nil {
			return views.Cart{}, err
		}
		dst, err := u.Carts.Get(ctx, cart.ID(targetID))
		if err != nil {
			return views.Cart{}, err
		}
		if err := src.MergeInto(dst, u.now()); err != nil {
			return views.Cart{}, err
		}
		for _, c := range []*cart.Cart{src, dst} {
			if err := u.persist(ctx, c); err != nil {
				return views.Cart{}, err
			}
		}
		return u.view(ctx, dst)
	})
}
