package kafka_test

import (
	"context"
	"testing"

	kafkaconsumer "github.com/IQVO/shopping-cart/internal/adapters/inbound/kafka"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func handle(t *testing.T, c *kafkaconsumer.Consumer, value []byte) error {
	t.Helper()
	return c.HandleMessage(context.Background(), value)
}

func TestCatalogEventsMakeAnItemPurchasableTogetherWithItsPrice(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	price := event("p1", kafkaconsumer.TypeItemPriceChanged, map[string]any{
		"catalogItemId": "sku-1", "currency": "USD", "listPriceMinor": 1200, "effectivePriceMinor": 900, "version": 1, "unknown": "ignored"})
	if err := handle(t, w.pricing(), price); err != nil {
		t.Fatal(err)
	}
	if err := handle(t, w.offers(), event("c1", kafkaconsumer.TypeCatalogItemCreated, map[string]any{"catalogItemId": "sku-1", "title": "x"})); err != nil {
		t.Fatal(err)
	}
	if it, _ := w.store.Items().Get(ctx, "sku-1"); !it.Purchasable() || it.PriceMinor != 900 {
		t.Fatalf("%+v", it)
	}
	if err := handle(t, w.offers(), event("c2", kafkaconsumer.TypeCatalogItemDiscontinued, map[string]any{"catalogItemId": "sku-1"})); err != nil {
		t.Fatal(err)
	}
	if it, _ := w.store.Items().Get(ctx, "sku-1"); it.Purchasable() {
		t.Fatal("discontinued")
	}
	if err := handle(t, w.pricing(), event("w1", kafkaconsumer.TypeItemPriceWithdrawn, map[string]any{"catalogItemId": "sku-1", "version": 2})); err != nil {
		t.Fatal(err)
	}
	if it, _ := w.store.Items().Get(ctx, "sku-1"); it.Priced {
		t.Fatalf("%+v", it)
	}
}

func TestScheduleAndTaxTableEventsFillTheEstimateView(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	sched := event("s1", kafkaconsumer.TypeChargeScheduleChanged, map[string]any{"scheduleId": "sc", "version": 3, "tiers": []map[string]any{
		{"tier": "STANDARD", "chargeMinor": 0, "freeOverMinor": 9900}, {"tier": "NEXT_DAY", "chargeMinor": 1490}}})
	if err := handle(t, w.pricing(), sched); err != nil {
		t.Fatal(err)
	}
	s, err := w.store.Schedules().Current(ctx)
	if err != nil || s.Version != 3 || s.Tiers[cart.TierStandard].FreeOverMinor != 9900 || s.Tiers[cart.TierNextDay].ChargeMinor != 1490 {
		t.Fatalf("%+v %v", s, err)
	}
	tax := event("t1", kafkaconsumer.TypeTaxTableChanged, map[string]any{"taxTableId": "tx", "version": 2, "bands": []map[string]any{
		{"postalPrefixFrom": "010", "postalPrefixTo": "099", "rateBasisPoints": 1800}}})
	if err := handle(t, w.pricing(), tax); err != nil {
		t.Fatal(err)
	}
	tt, err := w.store.TaxTables().Current(ctx)
	if err != nil || tt.Version != 2 || tt.Bands[0].RateBasisPoints != 1800 || tt.Bands[0].From != "010" {
		t.Fatalf("%+v %v", tt, err)
	}
}

func TestCheckoutCompletedChecksOutTheCart(t *testing.T) {
	w := newWorld(t)
	if err := handle(t, w.checkout(), completed("e1")); err != nil {
		t.Fatal(err)
	}
	if w.state(t) != cart.StateCheckedOut {
		t.Fatalf("state = %s", w.state(t))
	}
	if err := handle(t, w.checkout(), completed("e1")); err != nil {
		t.Fatalf("a redelivery is acknowledged: %v", err)
	}
}

func TestUnknownTypesAndNonCloudEventsAreIgnored(t *testing.T) {
	w := newWorld(t)
	before := w.uow.count()
	for _, v := range [][]byte{
		event("x", "com.retailnetwork.storefront.checkout.checkoutsession.CheckoutStarted", map[string]any{}),
		event("y", "warehouse.something.Else", map[string]any{}),
		[]byte(`{"hello":"world"}`),
	} {
		if err := handle(t, w.checkout(), v); err != nil {
			t.Fatalf("got %v", err)
		}
	}
	if w.uow.count() != before {
		t.Fatal("ignored messages open no unit of work")
	}
}

func TestInvalidPayloadsArePoisonNotTransient(t *testing.T) {
	w := newWorld(t)
	for name, v := range map[string][]byte{
		"no cartId":       event("a", kafkaconsumer.TypeCheckoutCompleted, map[string]any{"sessionId": "s"}),
		"bad id":          event("b", kafkaconsumer.TypeCatalogItemCreated, map[string]any{"catalogItemId": "bad id"}),
		"negative price":  event("c", kafkaconsumer.TypeItemPriceChanged, map[string]any{"catalogItemId": "a", "currency": "USD", "effectivePriceMinor": -1, "version": 1}),
		"no tiers":        event("d", kafkaconsumer.TypeChargeScheduleChanged, map[string]any{"version": 1, "tiers": []any{}}),
		"unknown tier":    event("e", kafkaconsumer.TypeChargeScheduleChanged, map[string]any{"version": 1, "tiers": []map[string]any{{"tier": "FAST", "chargeMinor": 1}}}),
		"no bands":        event("f", kafkaconsumer.TypeTaxTableChanged, map[string]any{"version": 1, "bands": []any{}}),
		"wrong data type": event("g", kafkaconsumer.TypeCatalogItemDiscontinued, map[string]any{"catalogItemId": 7}),
	} {
		err := handle(t, w.consumerFor(name), v)
		if err == nil {
			t.Fatalf("%s: a poison message must be reported to the loop", name)
		}
	}
}
