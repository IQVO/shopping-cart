package usecases_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/idempotency"
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

func TestPurgeExpiredIdempotencyKeysDeletesOnlyExpiredAnswers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	store := e.store.Idempotency()
	put := func(key string, age time.Duration) {
		t.Helper()
		_, replayed, err := store.Do(ctx, key, "h", e.clock.Now().Add(-age), func(context.Context) (idempotency.Record, error) {
			return idempotency.Record{Status: http.StatusCreated, ContentType: "text/plain", Body: []byte(key)}, nil
		})
		if err != nil || replayed {
			t.Fatalf("store %s: replayed=%v err=%v", key, replayed, err)
		}
	}
	put("k-old", idempotency.TTL+time.Minute)
	put("k-live", time.Minute)
	n, err := usecases.PurgeExpiredIdempotencyKeys{Store: store}.Execute(ctx, 10)
	if err != nil || n != 1 {
		t.Fatalf("purge = %d %v, want 1 (only the expired answer)", n, err)
	}
	// The live answer still replays (a 500 fn is never called on a replay):
	_, replayed, err := store.Do(ctx, "k-live", "h", e.clock.Now(), func(context.Context) (idempotency.Record, error) {
		return idempotency.Record{Status: http.StatusInternalServerError}, nil
	})
	if err != nil || !replayed {
		t.Fatalf("the live answer was deleted: replayed=%v err=%v", replayed, err)
	}
	if n, _ := (usecases.PurgeExpiredIdempotencyKeys{Store: store}).Execute(ctx, 10); n != 0 {
		t.Fatalf("a second sweep deleted %d", n)
	}
}
