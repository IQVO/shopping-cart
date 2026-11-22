//go:build integration

package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/testing/kafkatest"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

// The retention proof (ADR 0011, the Open point "Expired idempotency_keys rows
// are purged by nothing"): the sweep deletes the rows the 24-hour window has
// already retired, a live row is never touched, and a key whose row expired
// between two requests EXECUTES again instead of answering the stored response
// (the read-side age guard of readSQL). Driven through the shipped binary.

// retentionEnv boots the service with the retention sweep ticking fast.
func retentionEnv(t *testing.T, dsn string) map[string]string {
	t.Helper()
	env := bootEnv(t, dsn, kafkatest.Brokers(t), fakeDeliveryPromise(t))
	env["IDEMPOTENCY_PURGE_INTERVAL"] = "200ms"
	return env
}

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

// TestExpiredIdempotencyKeyRowsArePurgedAndLiveOnesKept: a row older than the
// TTL is deleted by the background sweep, a row inside the window is kept and
// still replays.
func TestExpiredIdempotencyKeyRowsArePurgedAndLiveOnesKept(t *testing.T) {
	dsn := pgtest.URL(t)
	svc := boot(t, retentionEnv(t, dsn))

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
}

// TestAReplayOfAnExpiredKeyExecutes: the read-side age guard. The row is still
// on disk (the sweep has not run: default interval) but older than the TTL, so
// the replay must NOT answer it: the key is taken over and the request runs
// again, answering a fresh cart without Idempotency-Replayed.
func TestAReplayOfAnExpiredKeyExecutes(t *testing.T) {
	dsn := pgtest.URL(t)
	svc := boot(t, bootEnv(t, dsn, kafkatest.Brokers(t), fakeDeliveryPromise(t)))

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
}
