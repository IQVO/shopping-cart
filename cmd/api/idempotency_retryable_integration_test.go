//go:build integration

package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/testing/kafkatest"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

// The replay classes over the REAL router and REAL Postgres: a RETRYABLE
// refusal rolls the request back, key row included, so the same key retries
// the work and is then stored and replayed like any success (the fleet rule,
// warehouse-harness-template docs/idempotency-replay-classes.md; ADR 0011).
//
// These run as subtests of TestTheShippedBinaryIsWiredEndToEnd, BEFORE its
// kafka-down readiness subtest terminates the shared broker: they need it (a
// booted service requires Kafka). The rig boots its own service on its own
// database with its OWN consumer groups, so it never fights the flow's
// instances for partitions.

// retryRig is a booted service plus a pool on ITS database, so the tests can
// count key rows and install triggers from outside any request.
type retryRig struct {
	svc   *service
	count func(t *testing.T, query string, args ...any) int
	exec  func(t *testing.T, sql string)
}

func newRetryRig(t *testing.T) *retryRig {
	t.Helper()
	brokers := kafkatest.Brokers(t)
	dsn := pgtest.URL(t)
	dp := fakeDeliveryPromise(t)
	env := map[string]string{
		"DATABASE_URL": dsn, "KAFKA_BROKERS": strings.Join(brokers, ","), "DELIVERY_PROMISE_URL": dp.URL,
		"OFFER_AVAILABILITY_CONSUMER_GROUP": "sc-retry-offers", "PRICING_CONSUMER_GROUP": "sc-retry-pricing", "CHECKOUT_CONSUMER_GROUP": "sc-retry-checkout",
		"OUTBOX_RELAY_INTERVAL": "100ms", "SHUTDOWN_DRAIN_DELAY": "0", "LOG_LEVEL": "warn",
	}
	svc := boot(t, env)
	pool, err := postgres.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	r := &retryRig{svc: svc}
	r.count = func(t *testing.T, query string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	r.exec = func(t *testing.T, sql string) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	return r
}

// keyRows is how many rows the idempotency store holds for key (0 after the
// key was released).
func (r *retryRig) keyRows(t *testing.T, key string) int {
	t.Helper()
	return r.count(t, `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key)
}

func (r *retryRig) createCart(t *testing.T, key, shopper string) string {
	t.Helper()
	reply := r.svc.call(t, http.MethodPost, "/carts", key, `{"shopperId":"`+shopper+`"}`)
	if reply.status != http.StatusCreated {
		t.Fatalf("create cart = %d %s", reply.status, reply.body)
	}
	return reply.json(t)["cartId"].(string)
}

// wantRefusal asserts the reply is the problem named by slug with status.
func (r *retryRig) wantRefusal(t *testing.T, reply reply, status int, slug string) {
	t.Helper()
	if reply.status != status || reply.json(t)["type"] != "/problems/"+slug {
		t.Fatalf("answer = %d %s, want %d %s", reply.status, reply.body, status, slug)
	}
}

// replayClassesOverPostgres is the real-router proof of the RETRYABLE class.
func replayClassesOverPostgres(t *testing.T) {
	t.Helper()
	t.Run("a concurrent-update refusal is not stored, so its key retries", func(t *testing.T) { concurrentUpdateRefusal(t) })
	t.Run("an item-not-purchasable refusal is not stored, so its key retries", func(t *testing.T) { itemNotPurchasableRefusal(t) })
}

// concurrent-update is RETRYABLE. The version guard of CartRepository.Save
// (UPDATE ... WHERE version = loaded) is what a lost optimistic-lock race
// sees, and the use case retries it commandAttempts times before answering.
// The trigger makes every UPDATE of carts match no row, exactly that, on both
// attempts: the refused request must leave no key row; the same key then runs
// again once the race is gone, is stored and is replayed.
func concurrentUpdateRefusal(t *testing.T) {
	r := newRetryRig(t)
	source := r.createCart(t, "cu-src", "shopper-cu")
	target := r.createCart(t, "cu-tgt", "shopper-cu")
	const key = "cu-m"
	path := "/carts/" + source + "/merges"
	body := `{"targetCartId":"` + target + `"}`

	r.exec(t, `CREATE FUNCTION sc_drop_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$`)
	r.exec(t, `CREATE TRIGGER sc_drop_update BEFORE UPDATE ON carts FOR EACH ROW EXECUTE FUNCTION sc_drop_update()`)
	refused := r.svc.call(t, http.MethodPost, path, key, body)
	r.wantRefusal(t, refused, http.StatusConflict, "concurrent-update")
	if n := r.keyRows(t, key); n != 0 {
		t.Fatalf("the refused request left %d row(s) under its key, want 0: a concurrent-update refusal is RETRYABLE and must release the key", n)
	}
	r.exec(t, `DROP TRIGGER sc_drop_update ON carts`)
	r.exec(t, `DROP FUNCTION sc_drop_update()`)

	retry := r.svc.call(t, http.MethodPost, path, key, body)
	if retry.status != http.StatusOK || retry.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the retry under the released key = %d %s (replayed %q), want a fresh 200", retry.status, retry.body, retry.header.Get("Idempotency-Replayed"))
	}
	if n := r.keyRows(t, key); n != 1 {
		t.Fatalf("%d row(s) under the key after the retry succeeded, want 1 (a 2xx is stored)", n)
	}
	again := r.svc.call(t, http.MethodPost, path, key, body)
	if again.status != http.StatusOK || again.body != retry.body || again.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third call = %d %s (replayed %q), want the stored 200 replayed", again.status, again.body, again.header.Get("Idempotency-Replayed"))
	}
}

// publishPurchasable feeds the local views for sku-200 the way the producers
// do (offer-availability and pricing events through the inbox).
func publishPurchasable(t *testing.T) {
	t.Helper()
	kafkatest.Produce(t, topicOffers, event("oa-np-1", typeCatalogCreated, "sku-200", map[string]any{"catalogItemId": "sku-200", "title": "Bottle"}))
	kafkatest.Produce(t, topicPricing,
		event("pr-np-1", typePriceChanged, "sku-200", map[string]any{"catalogItemId": "sku-200", "currency": "USD", "listPriceMinor": 1500, "effectivePriceMinor": 1000, "version": 1}),
		event("pr-np-2", typeScheduleChange, "sched-np", map[string]any{"scheduleId": "sched-np", "version": 1, "tiers": []map[string]any{
			{"tier": "STANDARD", "chargeMinor": 500, "freeOverMinor": 50000}, {"tier": "NEXT_DAY", "chargeMinor": 1200}}}),
		event("pr-np-3", typeTaxChange, "tax-np", map[string]any{"taxTableId": "tax-np", "version": 1, "bands": []map[string]any{
			{"postalPrefixFrom": "700", "postalPrefixTo": "799", "rateBasisPoints": 1000}}}),
	)
}

// item-not-purchasable is RETRYABLE: the PurchasabilityView is fed by
// offer-availability events, so the fact the request needs can arrive a moment
// later. The refusal must leave no key row and no replay; once the event is
// consumed the SAME key runs again, succeeds, and is then stored and replayed.
func itemNotPurchasableRefusal(t *testing.T) {
	r := newRetryRig(t)
	cartID := r.createCart(t, "np-c", "shopper-np")
	const key = "np-1"
	path := "/carts/" + cartID + "/lines"
	body := `{"catalogItemId":"sku-200","quantity":1}`

	refused := r.svc.call(t, http.MethodPost, path, key, body)
	r.wantRefusal(t, refused, http.StatusUnprocessableEntity, "item-not-purchasable")
	if n := r.keyRows(t, key); n != 0 {
		t.Fatalf("the refused request left %d row(s) under its key, want 0: an item-not-purchasable refusal is RETRYABLE and must release the key", n)
	}
	if again := r.svc.call(t, http.MethodPost, path, key, body); again.header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("the same request under the released key was answered from the store: a RETRYABLE refusal must never be replayed")
	}

	publishPurchasable(t)
	var stored reply
	eventually(t, 90*time.Second, "the consumed item to become purchasable and the same key to run again", func() bool {
		stored = r.svc.call(t, http.MethodPost, path, key, body)
		return stored.status == http.StatusOK
	})
	if n := r.keyRows(t, key); n != 1 {
		t.Fatalf("%d row(s) under the key after the retry succeeded, want 1 (a 2xx is stored)", n)
	}
	if rep := r.svc.call(t, http.MethodPost, path, key, body); rep.status != http.StatusOK || rep.body != stored.body || rep.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third call = %d %s (replayed %q), want the stored 200 replayed", rep.status, rep.body, rep.header.Get("Idempotency-Replayed"))
	}
}
