package repocontract

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

func testInbox(t *testing.T, e Env) {
	first, err := e.Inbox.Claim(bg(), "ev-1", "type.A")
	must(t, err)
	again, err := e.Inbox.Claim(bg(), "ev-1", "type.A")
	must(t, err)
	other, err := e.Inbox.Claim(bg(), "ev-2", "type.A")
	must(t, err)
	if !first || again || !other {
		t.Fatalf("claims = %v %v %v, want true false true", first, again, other)
	}
}

var errBoom = errors.New("boom")

func testUnitOfWork(t *testing.T, e Env) {
	t.Run("a unit that fails rolls back every table", func(t *testing.T) {
		err := e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Carts.Save(ctx, newCart(t, "cart-rb", "", t0)))
			must(t, e.Items.MarkCatalogued(ctx, "item-rb", at(1)))
			_, serr := e.Schedules.Replace(ctx, estimate.Schedule{Version: 9, Tiers: map[cart.Tier]estimate.TierCharge{cart.TierStandard: {ChargeMinor: 1}}})
			must(t, serr)
			first, err := e.Inbox.Claim(ctx, "ev-rb", "type.A")
			must(t, err)
			if !first {
				t.Fatal("the claim inside the unit must be the first")
			}
			must(t, e.Outbox.Append(ctx, mustEvents(t)...))
			return errBoom
		})
		wantErr(t, err, errBoom)
		_, err = e.Carts.Get(bg(), "cart-rb")
		wantErr(t, err, ports.ErrNotFound)
		_, err = e.Items.Get(bg(), "item-rb")
		wantErr(t, err, ports.ErrNotFound)
		_, err = e.Schedules.Current(bg())
		wantErr(t, err, ports.ErrNotFound)
		if first, _ := e.Inbox.Claim(bg(), "ev-rb", "type.A"); !first {
			t.Fatal("the rolled back claim must be free again")
		}
		if n := e.OutboxCount(t); n != 0 {
			t.Fatalf("outbox holds %d events after a rollback", n)
		}
	})
	t.Run("a unit that succeeds commits everything", func(t *testing.T) {
		must(t, e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Carts.Save(ctx, newCart(t, "cart-ok", "", t0)))
			must(t, e.Outbox.Append(ctx, mustEvents(t)...))
			return nil
		}))
		if _, err := e.Carts.Get(bg(), "cart-ok"); err != nil {
			t.Fatalf("committed cart missing: %v", err)
		}
		if n := e.OutboxCount(t); n != 2 {
			t.Fatalf("outbox holds %d events, want 2", n)
		}
	})
	t.Run("a nested unit joins the outer one", func(t *testing.T) {
		err := e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.UoW.Do(ctx, func(ctx context.Context) error { return e.Carts.Save(ctx, newCart(t, "cart-nest", "", t0)) }))
			return errBoom
		})
		wantErr(t, err, errBoom)
		_, err = e.Carts.Get(bg(), "cart-nest")
		wantErr(t, err, ports.ErrNotFound)
	})
	t.Run("a nested unit that fails undoes only itself", func(t *testing.T) {
		// The use cases retry a whole attempt on ErrConcurrentUpdate: the failed
		// attempt must leave nothing behind inside the unit it joined.
		err := e.UoW.Do(bg(), func(ctx context.Context) error {
			must(t, e.Carts.Save(ctx, newCart(t, "cart-keep", "", t0)))
			inner := e.UoW.Do(ctx, func(ctx context.Context) error {
				must(t, e.Carts.Save(ctx, newCart(t, "cart-undone", "", t0)))
				return errBoom
			})
			wantErr(t, inner, errBoom)
			return nil
		})
		must(t, err)
		if _, err := e.Carts.Get(bg(), "cart-keep"); err != nil {
			t.Fatalf("the outer write must commit: %v", err)
		}
		_, err = e.Carts.Get(bg(), "cart-undone")
		wantErr(t, err, ports.ErrNotFound)
	})
}

// mustEvents returns two events the cart raises.
func mustEvents(t *testing.T) []shared.Event {
	t.Helper()
	c := newCart(t, "cart-ev", "", t0)
	must(t, c.AddLine("item-ev", 1, true, at(1)))
	must(t, c.CloseOnCheckoutCompleted("sess-ev", at(2)))
	return c.PullEvents()
}
