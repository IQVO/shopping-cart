package usecases_test

import (
	"context"
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/pagination"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

func TestGetCartShowsTheItemSubtotalOnlyWithoutADestination(t *testing.T) {
	e := newEnv(t)
	e.stock(t, "sku-1", 1000)
	c := e.add(t, e.newCart(t, "").CartID, "sku-1", 3)
	v, _ := usecases.GetCart{Deps: e.deps}.Execute(context.Background(), c.CartID)
	want := estimate.Estimate{Currency: "USD", ItemsMinor: 3000, TotalMinor: 3000}
	if v.Estimate != want {
		t.Fatalf("estimate = %+v, want %+v", v.Estimate, want)
	}
}

func TestGetCartEstimateUsesTheChargeScheduleAndTheTaxTable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := readyCart(t, e)
	e.quoter.Respond(oneGroupQuote(t0.Add(3600e9)), nil)
	_, _ = usecases.QuoteDelivery{Deps: e.deps}.Execute(ctx, id, "k")
	_, _ = usecases.SelectDelivery{Deps: e.deps}.Execute(ctx, id, 1, "o2")
	_, _ = e.store.Schedules().Replace(ctx, estimate.Schedule{Version: 1, Tiers: map[cart.Tier]estimate.TierCharge{cart.TierNextDay: {ChargeMinor: 500}}})
	_, _ = e.store.TaxTables().Replace(ctx, estimate.TaxTable{Version: 1, Bands: []estimate.TaxBand{{From: "700", To: "799", RateBasisPoints: 1000}}})
	v, _ := usecases.GetCart{Deps: e.deps}.Execute(ctx, id)
	want := estimate.Estimate{Currency: "USD", ItemsMinor: 2000, DeliveryMinor: 500, TaxMinor: 250, TotalMinor: 2750, Complete: true}
	if v.Estimate != want {
		t.Fatalf("estimate = %+v, want %+v", v.Estimate, want)
	}
}

func TestListCartsFiltersByShopperAndState(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.newCart(t, "shopper-1")
	e.clock.Advance(1e9)
	b := e.newCart(t, "shopper-1")
	_ = e.newCart(t, "shopper-2")
	got, err := usecases.ListCarts{Deps: e.deps}.Execute(ctx, "shopper-1", "", nil, 10)
	if err != nil || len(got.Items) != 2 || got.Items[0].CartID != b.CartID || got.Items[1].CartID != a.CartID || got.Next != nil {
		t.Fatalf("%+v %v", got, err)
	}
	got, _ = usecases.ListCarts{Deps: e.deps}.Execute(ctx, "shopper-1", cart.StateMerged, nil, 10)
	if len(got.Items) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestListCartsWalksEveryPageOnce(t *testing.T) {
	e := newEnv(t)
	want := map[string]bool{}
	for i := 0; i < 7; i++ {
		// Carts 0-2 and 3-5 share an instant (the clock only advances between groups).
		if i%3 == 0 {
			e.clock.Advance(1e9)
		}
		want[e.newCart(t, "shopper-1").CartID] = true
	}
	for _, limit := range []int{1, 2, 3, 7, 8} {
		seen := map[string]bool{}
		for _, id := range walkCartPages(t, e, limit) {
			if seen[id] || !want[id] {
				t.Fatalf("limit %d: %s listed twice or unknown", limit, id)
			}
			seen[id] = true
		}
		if len(seen) != len(want) {
			t.Fatalf("limit %d: walked %d of %d carts", limit, len(seen), len(want))
		}
	}
}

// walkCartPages follows Next until the last page and returns every cartId in
// the order served; no page may exceed limit.
func walkCartPages(t *testing.T, e *env, limit int) []string {
	t.Helper()
	var ids []string
	var after *pagination.Keyset
	for {
		pg, err := usecases.ListCarts{Deps: e.deps}.Execute(context.Background(), "shopper-1", "", after, limit)
		if err != nil || len(pg.Items) > limit {
			t.Fatalf("limit %d: %d items, %v", limit, len(pg.Items), err)
		}
		for _, v := range pg.Items {
			ids = append(ids, v.CartID)
		}
		if pg.Next == nil {
			return ids
		}
		after = pg.Next
	}
}
