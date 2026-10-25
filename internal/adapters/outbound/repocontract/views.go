package repocontract

import (
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

func testItems(t *testing.T, e Env) {
	t.Run("a miss is ErrNotFound", func(t *testing.T) {
		_, err := e.Items.Get(bg(), "item-nope")
		wantErr(t, err, ports.ErrNotFound)
	})
	t.Run("a price before the catalogue event leaves a stub that completes", func(t *testing.T) {
		must(t, e.Items.ApplyPrice(bg(), "item-s", "USD", 900, 1, at(1)))
		it, err := e.Items.Get(bg(), "item-s")
		must(t, err)
		if it.Catalogued || !it.Priced || it.PriceMinor != 900 || it.Currency != "USD" || it.Purchasable() {
			t.Fatalf("stub = %+v", it)
		}
		must(t, e.Items.MarkCatalogued(bg(), "item-s", at(2)))
		it, _ = e.Items.Get(bg(), "item-s")
		if !it.Purchasable() || it.PriceMinor != 900 || !it.UpdatedAt.Equal(at(2)) {
			t.Fatalf("completed = %+v", it)
		}
	})
	t.Run("price writes are guarded by the version", func(t *testing.T) { itemPriceVersions(t, e) })
	t.Run("a discontinued item stays discontinued", func(t *testing.T) {
		must(t, e.Items.MarkCatalogued(bg(), "item-d", at(1)))
		must(t, e.Items.ApplyPrice(bg(), "item-d", "USD", 5, 1, at(1)))
		must(t, e.Items.MarkDiscontinued(bg(), "item-d", at(2)))
		must(t, e.Items.MarkCatalogued(bg(), "item-d", at(3)))
		it, _ := e.Items.Get(bg(), "item-d")
		if !it.Discontinued || it.Purchasable() {
			t.Fatalf("%+v", it)
		}
	})
	t.Run("GetMany returns only the known items", func(t *testing.T) {
		must(t, e.Items.MarkCatalogued(bg(), "item-m1", at(1)))
		must(t, e.Items.MarkCatalogued(bg(), "item-m2", at(1)))
		got, err := e.Items.GetMany(bg(), []string{"item-m1", "item-m2", "item-missing"})
		must(t, err)
		if len(got) != 2 || !got["item-m1"].Catalogued {
			t.Fatalf("%+v", got)
		}
		got, err = e.Items.GetMany(bg(), nil)
		must(t, err)
		if len(got) != 0 {
			t.Fatalf("%+v", got)
		}
	})
}

func itemPriceVersions(t *testing.T, e Env) {
	must(t, e.Items.ApplyPrice(bg(), "item-v", "USD", 100, 2, at(1)))
	must(t, e.Items.ApplyPrice(bg(), "item-v", "USD", 50, 1, at(2))) // older: ignored
	must(t, e.Items.ApplyPrice(bg(), "item-v", "USD", 60, 2, at(2))) // equal: ignored
	it, _ := e.Items.Get(bg(), "item-v")
	if it.PriceMinor != 100 || it.PriceVersion != 2 {
		t.Fatalf("stale writes must be ignored: %+v", it)
	}
	must(t, e.Items.ApplyWithdrawn(bg(), "item-v", 3, at(3)))
	it, _ = e.Items.Get(bg(), "item-v")
	if it.Priced || it.PriceMinor != 0 || it.PriceVersion != 3 {
		t.Fatalf("withdrawn = %+v", it)
	}
	must(t, e.Items.ApplyPrice(bg(), "item-v", "USD", 70, 2, at(4))) // late older price after the withdrawal
	must(t, e.Items.ApplyWithdrawn(bg(), "item-v", 1, at(4)))
	it, _ = e.Items.Get(bg(), "item-v")
	if it.Priced || it.PriceVersion != 3 {
		t.Fatalf("a late older price must not revive a withdrawn one: %+v", it)
	}
	must(t, e.Items.ApplyPrice(bg(), "item-v", "EUR", 80, 4, at(5)))
	it, _ = e.Items.Get(bg(), "item-v")
	if !it.Priced || it.PriceMinor != 80 || it.Currency != "EUR" {
		t.Fatalf("a newer price re-prices: %+v", it)
	}
	must(t, e.Items.ApplyWithdrawn(bg(), "item-w", 1, at(5)))
	if it, err := e.Items.Get(bg(), "item-w"); err != nil || it.Priced || it.PriceVersion != 1 {
		t.Fatalf("a withdrawal for an unknown item leaves a stub: %+v %v", it, err)
	}
}

func testEstimateViews(t *testing.T, e Env) {
	_, err := e.Schedules.Current(bg())
	wantErr(t, err, ports.ErrNotFound)
	_, err = e.TaxTables.Current(bg())
	wantErr(t, err, ports.ErrNotFound)
	s2 := estimate.Schedule{Version: 2, Tiers: map[cart.Tier]estimate.TierCharge{
		cart.TierStandard: {ChargeMinor: 500, FreeOverMinor: 5000}, cart.TierNextDay: {ChargeMinor: 1200}}}
	ok, err := e.Schedules.Replace(bg(), s2)
	must(t, err)
	if !ok {
		t.Fatal("the first schedule must be stored")
	}
	for _, v := range []int64{1, 2} {
		ok, err = e.Schedules.Replace(bg(), estimate.Schedule{Version: v, Tiers: map[cart.Tier]estimate.TierCharge{cart.TierStandard: {ChargeMinor: 1}}})
		must(t, err)
		if ok {
			t.Fatalf("version %d must not replace version 2", v)
		}
	}
	got, err := e.Schedules.Current(bg())
	must(t, err)
	if got.Version != 2 || got.Tiers[cart.TierStandard] != s2.Tiers[cart.TierStandard] || len(got.Tiers) != 2 {
		t.Fatalf("schedule = %+v", got)
	}
	taxTableRoundTrip(t, e)
}

func taxTableRoundTrip(t *testing.T, e Env) {
	t1 := estimate.TaxTable{Version: 1, Bands: []estimate.TaxBand{{From: "100", To: "199", RateBasisPoints: 500}, {From: "700", To: "799", RateBasisPoints: 825}}}
	ok, err := e.TaxTables.Replace(bg(), t1)
	must(t, err)
	if !ok {
		t.Fatal("the first table must be stored")
	}
	ok, _ = e.TaxTables.Replace(bg(), estimate.TaxTable{Version: 1, Bands: t1.Bands[:1]})
	if ok {
		t.Fatal("an equal version must not replace")
	}
	ok, err = e.TaxTables.Replace(bg(), estimate.TaxTable{Version: 2, Bands: t1.Bands[1:]})
	must(t, err)
	got, err := e.TaxTables.Current(bg())
	must(t, err)
	if !ok || got.Version != 2 || len(got.Bands) != 1 || got.Bands[0] != t1.Bands[1] {
		t.Fatalf("table = %+v", got)
	}
}
