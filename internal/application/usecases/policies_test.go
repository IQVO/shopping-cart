package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func TestAbandonInactiveAbandonsOnlyCartsIdleThirtyDaysAndPublishes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.newCart(t, "")
	e.clock.Advance(20 * 24 * time.Hour)
	fresh := e.newCart(t, "")
	e.clock.Advance(10 * 24 * time.Hour)
	n, err := usecases.AbandonInactive{Deps: e.deps}.Execute(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	o, _ := usecases.GetCart{Deps: e.deps}.Execute(ctx, old.CartID)
	f, _ := usecases.GetCart{Deps: e.deps}.Execute(ctx, fresh.CartID)
	if o.State != cart.StateAbandoned || f.State != cart.StateOpen {
		t.Fatalf("states %s %s", o.State, f.State)
	}
	if names := e.eventNames(t); len(names) != 1 || names[0] != "CartAbandoned" {
		t.Fatalf("events = %v", names)
	}
	if n, _ := (usecases.AbandonInactive{Deps: e.deps}).Execute(ctx, 10); n != 0 {
		t.Fatalf("a second sweep abandons %d", n)
	}
}

func TestAbandonInactiveHonoursTheLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 3; i++ {
		e.newCart(t, "")
	}
	e.clock.Advance(31 * 24 * time.Hour)
	n, err := usecases.AbandonInactive{Deps: e.deps}.Execute(context.Background(), 2)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestPurgeAbandonedCartsDeletesClosedCartsIdleThirtyDaysWithoutAnEvent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.newCart(t, "")
	e.clock.Advance(31 * 24 * time.Hour)
	if _, err := (usecases.AbandonInactive{Deps: e.deps}).Execute(ctx, 10); err != nil {
		t.Fatal(err)
	}
	open := e.newCart(t, "")
	events := len(e.eventNames(t))
	e.clock.Advance(29 * 24 * time.Hour)
	if n, _ := (usecases.PurgeAbandonedCarts{Deps: e.deps}).Execute(ctx, 10); n != 0 {
		t.Fatalf("29 days after abandoning: purged %d", n)
	}
	e.clock.Advance(24 * time.Hour)
	n, err := usecases.PurgeAbandonedCarts{Deps: e.deps}.Execute(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, err := (usecases.GetCart{Deps: e.deps}).Execute(ctx, "cart-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("purged cart: %v", err)
	}
	if _, err := (usecases.GetCart{Deps: e.deps}).Execute(ctx, open.CartID); err != nil {
		t.Fatalf("an OPEN cart is never purged by this policy: %v", err)
	}
	if len(e.eventNames(t)) != events {
		t.Fatal("purge publishes no event")
	}
}
