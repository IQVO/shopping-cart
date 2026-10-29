package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

func TestItemEventsFoldIntoPurchasabilityInAnyOrder(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.consumer()
	if err := c.ApplyItemPriceChanged(ctx, e.meta("e1", "x"), usecases.ItemPriceChanged{CatalogItemID: "sku", Currency: "USD", EffectivePriceMinor: 900, Version: 1}); err != nil {
		t.Fatal(err)
	}
	if it, _ := e.store.Items().Get(ctx, "sku"); it.Purchasable() {
		t.Fatal("a price alone does not make an uncatalogued item purchasable")
	}
	if err := c.ApplyCatalogItemCreated(ctx, e.meta("e2", "x"), usecases.CatalogItemCreated{CatalogItemID: "sku"}); err != nil {
		t.Fatal(err)
	}
	if it, _ := e.store.Items().Get(ctx, "sku"); !it.Purchasable() || it.PriceMinor != 900 {
		t.Fatalf("%+v", it)
	}
	if err := c.ApplyItemPriceWithdrawn(ctx, e.meta("e3", "x"), usecases.ItemPriceWithdrawn{CatalogItemID: "sku", Version: 2}); err != nil {
		t.Fatal(err)
	}
	if it, _ := e.store.Items().Get(ctx, "sku"); it.Purchasable() {
		t.Fatal("a withdrawn price is not purchasable")
	}
	_ = c.ApplyItemPriceChanged(ctx, e.meta("e4", "x"), usecases.ItemPriceChanged{CatalogItemID: "sku", Currency: "USD", EffectivePriceMinor: 5, Version: 1})
	if it, _ := e.store.Items().Get(ctx, "sku"); it.Priced {
		t.Fatal("a late older price must be dropped")
	}
}

func TestADiscontinuedItemIsNotPurchasable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.stock(t, "sku", 100)
	if err := e.consumer().ApplyCatalogItemDiscontinued(ctx, e.meta("e1", "x"), usecases.CatalogItemDiscontinued{CatalogItemID: "sku"}); err != nil {
		t.Fatal(err)
	}
	if it, _ := e.store.Items().Get(ctx, "sku"); it.Purchasable() {
		t.Fatal("discontinued")
	}
}

func TestAConsumedEventIsAppliedOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.consumer()
	in := usecases.ItemPriceChanged{CatalogItemID: "sku", Currency: "USD", EffectivePriceMinor: 900, Version: 1}
	_ = c.ApplyItemPriceChanged(ctx, e.meta("same", "x"), in)
	in.EffectivePriceMinor, in.Version = 1, 2
	_ = c.ApplyItemPriceChanged(ctx, e.meta("same", "x"), in)
	if it, _ := e.store.Items().Get(ctx, "sku"); it.PriceMinor != 900 {
		t.Fatalf("the replayed event id must be ignored: %+v", it)
	}
}

func TestPoisonPayloadsAreValidationErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.consumer()
	m := e.meta("p", "x")
	errs := []error{
		c.ApplyCatalogItemCreated(ctx, m, usecases.CatalogItemCreated{CatalogItemID: "bad id"}),
		c.ApplyCatalogItemDiscontinued(ctx, m, usecases.CatalogItemDiscontinued{}),
		c.ApplyItemPriceChanged(ctx, m, usecases.ItemPriceChanged{CatalogItemID: "a", Currency: "usd", Version: 1}),
		c.ApplyItemPriceChanged(ctx, m, usecases.ItemPriceChanged{CatalogItemID: "a", Currency: "USD", EffectivePriceMinor: -1, Version: 1}),
		c.ApplyItemPriceWithdrawn(ctx, m, usecases.ItemPriceWithdrawn{CatalogItemID: "a"}),
		c.ApplyDeliveryChargeScheduleChanged(ctx, m, usecases.DeliveryChargeScheduleChanged{Version: 1}),
		c.ApplyDeliveryChargeScheduleChanged(ctx, m, usecases.DeliveryChargeScheduleChanged{Version: 1, Tiers: []usecases.TierCharge{{Tier: "FAST"}}}),
		c.ApplyTaxTableChanged(ctx, m, usecases.TaxTableChanged{Version: 1}),
		c.ApplyTaxTableChanged(ctx, m, usecases.TaxTableChanged{Version: 1, Bands: []usecases.TaxBand{{PostalPrefixFrom: "9", PostalPrefixTo: "1"}}}),
		c.ApplyCheckoutCompleted(ctx, m, usecases.CheckoutCompleted{CartID: "c"}),
	}
	for i, err := range errs {
		if !errors.Is(err, shared.ErrValidation) {
			t.Fatalf("case %d: got %v, want ErrValidation", i, err)
		}
	}
	if e.store.Counts().Inbox != 0 {
		t.Fatal("a poison message claims nothing")
	}
}

func TestScheduleAndTaxTableKeepTheHighestVersion(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.consumer()
	tiers := func(charge int64) []usecases.TierCharge {
		return []usecases.TierCharge{{Tier: "STANDARD", ChargeMinor: charge}}
	}
	_ = c.ApplyDeliveryChargeScheduleChanged(ctx, e.meta("s2", "x"), usecases.DeliveryChargeScheduleChanged{Version: 2, Tiers: tiers(200)})
	_ = c.ApplyDeliveryChargeScheduleChanged(ctx, e.meta("s1", "x"), usecases.DeliveryChargeScheduleChanged{Version: 1, Tiers: tiers(100)})
	if s, _ := e.store.Schedules().Current(ctx); s.Version != 2 || s.Tiers[cart.TierStandard].ChargeMinor != 200 {
		t.Fatalf("%+v", s)
	}
	band := []usecases.TaxBand{{PostalPrefixFrom: "100", PostalPrefixTo: "199", RateBasisPoints: 5}}
	_ = c.ApplyTaxTableChanged(ctx, e.meta("t2", "x"), usecases.TaxTableChanged{Version: 2, Bands: band})
	_ = c.ApplyTaxTableChanged(ctx, e.meta("t1", "x"), usecases.TaxTableChanged{Version: 1, Bands: band})
	if tt, _ := e.store.TaxTables().Current(ctx); tt.Version != 2 {
		t.Fatalf("%+v", tt)
	}
}

func TestCheckoutCompletedChecksOutTheCartAndPublishesCartCheckedOut(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.newCart(t, "").CartID
	in := usecases.CheckoutCompleted{CartID: id, SessionID: "sess-1"}
	if err := e.consumer().ApplyCheckoutCompleted(ctx, e.meta("c1", "x"), in); err != nil {
		t.Fatal(err)
	}
	v, _ := usecases.GetCart{Deps: e.deps}.Execute(ctx, id)
	if v.State != cart.StateCheckedOut {
		t.Fatalf("state = %s", v.State)
	}
	if names := e.eventNames(t); len(names) != 1 || names[0] != "CartCheckedOut" {
		t.Fatalf("events = %v", names)
	}
	// A new event id for an already closed cart, and one for an unknown cart:
	// acknowledged, claimed, nothing published.
	if err := e.consumer().ApplyCheckoutCompleted(ctx, e.meta("c2", "x"), in); err != nil {
		t.Fatal(err)
	}
	if err := e.consumer().ApplyCheckoutCompleted(ctx, e.meta("c3", "x"), usecases.CheckoutCompleted{CartID: "ghost", SessionID: "s"}); err != nil {
		t.Fatal(err)
	}
	if len(e.eventNames(t)) != 1 || e.store.Counts().Inbox != 3 {
		t.Fatalf("events %v, inbox %d", e.eventNames(t), e.store.Counts().Inbox)
	}
}
