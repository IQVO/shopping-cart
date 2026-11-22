//go:build integration

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

// The retention proof (ADR 0011, the Open point "Expired idempotency_keys rows
// are purged by nothing"): the sweep deletes the rows the 24-hour window has
// already retired, a live row is never touched, and a key whose row expired
// between two requests EXECUTES again instead of answering the stored response
// (the read-side age guard of readSQL). Driven through the shipped binary; it
// runs inside the flow test because the shared test broker is stopped at its
// end and every later test must be Kafka-free.

// countKeyRows is how many idempotency_keys rows the key has.
func countKeyRows(t *testing.T, dsn, key string) int {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count idempotency_keys: %v", err)
	}
	return n
}

// idempotencyRetentionOverPostgres is the retention contract over the real
// service: expired rows go, live rows stay and replay, and an expired key
// executes again.
func idempotencyRetentionOverPostgres(t *testing.T, svc *service, dsn string) {
	t.Helper()
	t.Run("expired rows are purged and live ones kept and still replay", func(t *testing.T) {
		expired := svc.call(t, "POST", "/carts", "k-ret-old", `{"shopperId":"shopper-ret-old"}`)
		live := svc.call(t, "POST", "/carts", "k-ret-live", `{"shopperId":"shopper-ret-live"}`)
		if expired.status != http.StatusCreated || live.status != http.StatusCreated {
			t.Fatalf("create old = %d %s, live = %d %s", expired.status, expired.body, live.status, live.body)
		}
		pgtest.ExecOn(t, dsn, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours' WHERE key = $1`, "k-ret-old")

		eventually(t, 30*time.Second, "the retention sweep to delete the expired key row", func() bool {
			return countKeyRows(t, dsn, "k-ret-old") == 0
		})
		if n := countKeyRows(t, dsn, "k-ret-live"); n != 1 {
			t.Fatalf("live key row = %d, want 1: a row inside the TTL is never deleted", n)
		}
		replay := svc.call(t, "POST", "/carts", "k-ret-live", `{"shopperId":"shopper-ret-live"}`)
		if replay.status != http.StatusCreated || replay.header.Get("Idempotency-Replayed") != "true" ||
			replay.json(t)["cartId"] != live.json(t)["cartId"] {
			t.Fatalf("the live key = %d %s (%v), want its stored answer replayed", replay.status, replay.body, replay.header)
		}
	})
	t.Run("a replay of an expired key executes, not answers", func(t *testing.T) {
		const body = `{"shopperId":"shopper-ret-exec"}`
		first := svc.call(t, "POST", "/carts", "k-ret-exec", body)
		if first.status != http.StatusCreated {
			t.Fatalf("first = %d %s", first.status, first.body)
		}
		pgtest.ExecOn(t, dsn, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours' WHERE key = $1`, "k-ret-exec")

		again := svc.call(t, "POST", "/carts", "k-ret-exec", body)
		if again.status != http.StatusCreated || again.header.Get("Idempotency-Replayed") != "" ||
			again.json(t)["cartId"] == first.json(t)["cartId"] {
			t.Fatalf("an expired key = %d %s (%v), want a FRESH execution (a new cart, no replay header)",
				again.status, again.body, again.header)
		}
	})
}
