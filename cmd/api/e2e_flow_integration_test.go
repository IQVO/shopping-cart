//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/testing/kafkatest"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

const (
	typeCatalogCreated = "com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated"
	typePriceChanged   = "com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged"
	typeScheduleChange = "com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged"
	typeTaxChange      = "com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged"
	typeCheckoutDone   = "com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted"
)

func bootEnv(t *testing.T, dsn string, brokers []string, dp *fakeDP) map[string]string {
	t.Helper()
	for _, topic := range []string{topicAnalytics, topicOffers, topicPricing, topicCheckout} {
		kafkatest.CreateTopic(t, topic)
	}
	return map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": dp.URL,
		"OFFER_AVAILABILITY_CONSUMER_GROUP": "sc-e2e-offers", "PRICING_CONSUMER_GROUP": "sc-e2e-pricing", "CHECKOUT_CONSUMER_GROUP": "sc-e2e-checkout",
		"OUTBOX_RELAY_INTERVAL": "100ms", "ABANDON_SWEEP_INTERVAL": "200ms", "PURGE_SWEEP_INTERVAL": "200ms",
		"SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	}
}

// publishReadModels feeds the local views the way the producers do.
func publishReadModels(t *testing.T) {
	t.Helper()
	kafkatest.Produce(t, topicOffers, event("oa-1", typeCatalogCreated, "sku-100", map[string]any{"catalogItemId": "sku-100", "title": "Bottle"}))
	kafkatest.Produce(t, topicPricing,
		event("pr-1", typePriceChanged, "sku-100", map[string]any{"catalogItemId": "sku-100", "currency": "USD", "listPriceMinor": 1500, "effectivePriceMinor": 1000, "version": 1}),
		event("pr-2", typeScheduleChange, "sched-1", map[string]any{"scheduleId": "sched-1", "version": 1, "tiers": []map[string]any{
			{"tier": "STANDARD", "chargeMinor": 500, "freeOverMinor": 50000}, {"tier": "NEXT_DAY", "chargeMinor": 1200}}}),
		event("pr-3", typeTaxChange, "tax-1", map[string]any{"taxTableId": "tax-1", "version": 1, "bands": []map[string]any{
			{"postalPrefixFrom": "700", "postalPrefixTo": "799", "rateBasisPoints": 1000}}}),
	)
}

func addFirstLine(t *testing.T, svc *service, cartID string) {
	t.Helper()
	// The item becomes purchasable once both views consumed their events; an
	// earlier attempt is refused and (as a client error) stored under its key,
	// so every attempt uses a fresh one.
	attempt := 0
	eventually(t, 90*time.Second, "the consumed item to be purchasable", func() bool {
		attempt++
		r := svc.call(t, "POST", "/carts/"+cartID+"/lines", fmt.Sprintf("add-%d", attempt), `{"catalogItemId":"sku-100","quantity":2}`)
		return r.status == http.StatusOK
	})
}

func TestTheShippedBinaryIsWiredEndToEnd(t *testing.T) {
	dsn := pgtest.URL(t)
	dp := fakeDeliveryPromise(t)
	env := bootEnv(t, dsn, kafkatest.Brokers(t), dp)
	svc := boot(t, env)
	publishReadModels(t)

	created := svc.call(t, "POST", "/carts", "create-1", `{"shopperId":"shopper-1"}`)
	if created.status != http.StatusCreated {
		t.Fatalf("create = %d %s", created.status, created.body)
	}
	cartID := created.json(t)["cartId"].(string)
	if again := svc.call(t, "POST", "/carts", "create-1", `{"shopperId":"shopper-1"}`); again.body != created.body || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %d %s", again.status, again.body)
	}
	addFirstLine(t, svc, cartID)
	assertAnalytics(t, cartID, 1, "CartLineAdded")
	quoteSelectAndCheckOut(t, svc, cartID, dp)
	assertAnalytics(t, cartID, 3, "CartCheckedOut")

	// A restart keeps everything: the state lives in Postgres.
	if err := svc.stop(); err != nil && err != context.Canceled {
		t.Fatalf("stop: %v", err)
	}
	svc = boot(t, env)
	if got := svc.call(t, "GET", "/carts/"+cartID, "", ""); got.status != 200 || got.json(t)["state"] != "CHECKED_OUT" {
		t.Fatalf("after restart = %d %s", got.status, got.body)
	}
	t.Run("an idle cart is abandoned and later purged by the schedulers", func(t *testing.T) { idleCartLifecycle(t, svc, dsn) })
	t.Run("readyz is 503 dependency-unavailable while kafka is down", func(t *testing.T) { assertKafkaDownReadiness(t, svc) })
}

func assertAnalytics(t *testing.T, cartID string, n int, last string) {
	t.Helper()
	msgs := kafkatest.ReadKeyed(t, topicAnalytics, cartID, n, 60*time.Second)
	if got := string(msgs[len(msgs)-1].Value); !strings.Contains(got, "cart."+last) || msgs[0].Key != cartID {
		t.Fatalf("analytics events = %d, last %s", len(msgs), got)
	}
}

func quoteSelectAndCheckOut(t *testing.T, svc *service, cartID string, dp *fakeDP) {
	t.Helper()
	if r := svc.call(t, "POST", "/carts/"+cartID+":quote-delivery", "quote-0", ""); r.status != 422 || r.json(t)["type"] != "/problems/no-destination" {
		t.Fatalf("quote without destination = %d %s", r.status, r.body)
	}
	svc.call(t, "POST", "/carts/"+cartID+":set-destination", "dest-1", `{"destinationPostalPrefix":"787"}`)
	quoted := svc.call(t, "POST", "/carts/"+cartID+":quote-delivery", "quote-1", "")
	if quoted.status != 200 || dp.callCount() != 1 || dp.keys[0] != "quote-1" {
		t.Fatalf("quote = %d %s (calls %d)", quoted.status, quoted.body, dp.callCount())
	}
	selected := svc.call(t, "POST", "/carts/"+cartID+":select-delivery", "sel-1", `{"groupNo":1,"offerId":"of-1"}`).json(t)
	est := selected["estimate"].(map[string]any)
	// goods 2 x 1000 + NEXT_DAY 1200 = 3200; 10% tax = 320; total 3520.
	if est["totalMinor"].(float64) != 3520 || est["estimateComplete"] != true || selected["checkoutReadiness"].(map[string]any)["ready"] != true {
		t.Fatalf("estimate = %v readiness = %v", est, selected["checkoutReadiness"])
	}
	kafkatest.Produce(t, topicCheckout, event("co-1", typeCheckoutDone, "sess-1", map[string]any{"cartId": cartID, "sessionId": "sess-1"}))
	eventually(t, 60*time.Second, "the cart to be CHECKED_OUT", func() bool {
		return svc.call(t, "GET", "/carts/"+cartID, "", "").json(t)["state"] == "CHECKED_OUT"
	})
}

func idleCartLifecycle(t *testing.T, svc *service, dsn string) {
	t.Helper()
	id := svc.call(t, "POST", "/carts", "idle-1", `{}`).json(t)["cartId"].(string)
	age := func() {
		pgtest.ExecOn(t, dsn, `UPDATE carts SET last_changed_at = now() - interval '31 days' WHERE cart_id = $1`, id)
	}
	age()
	eventually(t, 60*time.Second, "the idle cart to be ABANDONED", func() bool {
		return svc.call(t, "GET", "/carts/"+id, "", "").json(t)["state"] == "ABANDONED"
	})
	assertAnalytics(t, id, 1, "CartAbandoned")
	age()
	eventually(t, 60*time.Second, "the abandoned cart to be purged", func() bool {
		return svc.call(t, "GET", "/carts/"+id, "", "").status == http.StatusNotFound
	})
}

func assertKafkaDownReadiness(t *testing.T, svc *service) {
	t.Helper()
	kafkatest.Stop(t)
	eventually(t, 60*time.Second, "readyz to report 503", func() bool {
		return svc.call(t, "GET", "/readyz", "", "").status == http.StatusServiceUnavailable
	})
	ready := svc.call(t, "GET", "/readyz", "", "")
	if ready.status != http.StatusServiceUnavailable || !strings.Contains(ready.body, "dependency-unavailable") {
		t.Fatalf("readyz = %d %s", ready.status, ready.body)
	}
	if health := svc.call(t, "GET", "/healthz", "", ""); health.status != http.StatusOK {
		t.Fatalf("healthz = %d %s", health.status, health.body)
	}
	if write := svc.call(t, "POST", "/carts", "offline-1", `{}`); write.status != http.StatusCreated {
		t.Fatalf("write while kafka is down = %d %s", write.status, write.body)
	}
	if err := svc.stop(); err != nil && err != context.Canceled {
		t.Fatalf("stop: %v", err)
	}
}
