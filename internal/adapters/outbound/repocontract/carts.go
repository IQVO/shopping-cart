package repocontract

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func testCarts(t *testing.T, e Env) {
	t.Run("a miss is ErrNotFound", func(t *testing.T) {
		_, err := e.Carts.Get(bg(), "cart-nope")
		wantErr(t, err, ports.ErrNotFound)
	})
	t.Run("a saved cart reads back identically", func(t *testing.T) {
		c := fullCart(t, "cart-rt")
		must(t, e.Carts.Save(bg(), c))
		if c.LoadedVersion() != c.Version() {
			t.Fatalf("Save must MarkPersisted: loaded = %d", c.LoadedVersion())
		}
		got, err := e.Carts.Get(bg(), "cart-rt")
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), c.Snapshot()) {
			t.Fatalf("round trip changed the cart:\n got %+v\nwant %+v", got.Snapshot(), c.Snapshot())
		}
	})
	t.Run("an empty guest cart round-trips", func(t *testing.T) {
		c := newCart(t, "cart-guest", "", t0)
		must(t, e.Carts.Save(bg(), c))
		got, err := e.Carts.Get(bg(), "cart-guest")
		must(t, err)
		if !reflect.DeepEqual(got.Snapshot(), c.Snapshot()) {
			t.Fatalf("round trip changed the cart:\n got %+v\nwant %+v", got.Snapshot(), c.Snapshot())
		}
	})
	t.Run("every state round-trips", func(t *testing.T) { cartsRoundTripEveryState(t, e) })
	t.Run("optimistic concurrency", func(t *testing.T) { cartsConcurrency(t, e) })
}

func cartsRoundTripEveryState(t *testing.T, e Env) {
	for i, finish := range []func(c *cart.Cart) error{
		func(c *cart.Cart) error { return c.CloseOnCheckoutCompleted("sess-1", at(10)) },
		func(c *cart.Cart) error { return c.AbandonInactive(at(60 * 24 * 31)) },
	} {
		c := newCart(t, fmt.Sprintf("cart-state-%d", i), "shopper-1", t0)
		must(t, finish(c))
		must(t, e.Carts.Save(bg(), c))
		got, err := e.Carts.Get(bg(), c.ID())
		must(t, err)
		if got.State() != c.State() || !got.LastChangedAt().Equal(c.LastChangedAt()) {
			t.Fatalf("state %s / %v became %s / %v", c.State(), c.LastChangedAt(), got.State(), got.LastChangedAt())
		}
	}
	src := newCart(t, "cart-merge-src", "", t0)
	dst := newCart(t, "cart-merge-dst", "shopper-1", t0)
	must(t, src.MergeInto(dst, at(5)))
	must(t, e.Carts.Save(bg(), src))
	must(t, e.Carts.Save(bg(), dst))
	got, err := e.Carts.Get(bg(), "cart-merge-src")
	must(t, err)
	if got.State() != cart.StateMerged {
		t.Fatalf("state = %s", got.State())
	}
}

func cartsConcurrency(t *testing.T, e Env) {
	c := newCart(t, "cart-cc", "shopper-1", t0)
	must(t, e.Carts.Save(bg(), c))
	wantErr(t, e.Carts.Save(bg(), newCart(t, "cart-cc", "shopper-1", t0)), ports.ErrConcurrentUpdate) // a create expects a free id
	a, err := e.Carts.Get(bg(), "cart-cc")
	must(t, err)
	b, err := e.Carts.Get(bg(), "cart-cc")
	must(t, err)
	must(t, a.SetDestination("787", at(1)))
	must(t, e.Carts.Save(bg(), a))
	must(t, b.SetDestination("100", at(2)))
	wantErr(t, e.Carts.Save(bg(), b), ports.ErrConcurrentUpdate) // b loaded a version that is gone
	must(t, a.SetDestination("200", at(3)))
	must(t, e.Carts.Save(bg(), a)) // a refreshed LoadedVersion with MarkPersisted
	ghost, err := cart.Restore(cart.Snapshot{ID: "cart-ghost", State: cart.StateOpen, CreatedAt: t0, LastChangedAt: t0, Version: 3})
	must(t, err)
	wantErr(t, e.Carts.Save(bg(), ghost), ports.ErrConcurrentUpdate) // loaded at a version that was never stored
}
