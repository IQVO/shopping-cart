//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

// retentionDB is one pool on the booted service's database plus the helpers
// the retention proof needs (counting key rows, backdating them, purging on
// demand). ONE pool: a fresh pool per poll would eat the server's connection
// slots (SQLSTATE 53300).
type retentionDB struct {
	pool  *pgxpool.Pool
	store *postgres.IdempotencyStore
}

func newRetentionDB(t *testing.T, dsn string) *retentionDB {
	t.Helper()
	pool, err := postgres.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return &retentionDB{pool: pool, store: postgres.NewIdempotencyStore(pool)}
}

// keyRows is how many idempotency_keys rows the key has.
func (r *retentionDB) keyRows(t *testing.T, key string) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM idempotency_keys WHERE key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count idempotency_keys: %v", err)
	}
	return n
}

// idempotencyRetentionOverPostgres is the retention contract over the real
// service: expired rows go, live rows stay and replay, an expired key executes
// again, and the purge racing a request on an expired key breaks nothing.
func idempotencyRetentionOverPostgres(t *testing.T, svc *service, dsn string) {
	t.Helper()
	db := newRetentionDB(t, dsn)
	t.Run("expired rows are purged and live ones kept and still replay", func(t *testing.T) {
		expired := svc.call(t, "POST", "/carts", "k-ret-old", `{"shopperId":"shopper-ret-old"}`)
		live := svc.call(t, "POST", "/carts", "k-ret-live", `{"shopperId":"shopper-ret-live"}`)
		if expired.status != http.StatusCreated || live.status != http.StatusCreated {
			t.Fatalf("create old = %d %s, live = %d %s", expired.status, expired.body, live.status, live.body)
		}
		pgtest.ExecOn(t, dsn, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours' WHERE key = $1`, "k-ret-old")

		eventually(t, 30*time.Second, "the retention sweep to delete the expired key row", func() bool {
			return db.keyRows(t, "k-ret-old") == 0
		})
		if n := db.keyRows(t, "k-ret-live"); n != 1 {
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
	t.Run("the purge racing requests on an expired key breaks nothing", func(t *testing.T) {
		const rounds = 10
		for i := range rounds {
			key := fmt.Sprintf("k-ret-race-%d", i)
			shopper := fmt.Sprintf("shopper-ret-race-%d", i)
			body := `{"shopperId":"` + shopper + `"}`
			seed := svc.call(t, "POST", "/carts", key, body)
			if seed.status != http.StatusCreated {
				t.Fatalf("round %d: seed = %d %s", i, seed.status, seed.body)
			}
			pgtest.ExecOn(t, dsn, `UPDATE idempotency_keys SET created_at = now() - interval '25 hours' WHERE key = $1`, key)

			var wg sync.WaitGroup
			codes := make([]int, 3)
			for j := range codes {
				wg.Add(1)
				go func() {
					defer wg.Done()
					r := svc.call(t, "POST", "/carts", key, body)
					codes[j] = r.status
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = db.store.PurgeExpired(context.Background(), 100)
			}()
			wg.Wait()
			for _, c := range codes {
				if c != http.StatusCreated {
					t.Fatalf("round %d: a request racing the purge answered %d, want 201", i, c)
				}
			}
			if got := countCarts(t, svc, shopper); got != 1 {
				t.Fatalf("round %d: %d carts for one key, want exactly 1 (one execution, the rest replay)", i, got)
			}
		}
	})
}

// TestThePurgeSkipsARowARequestHoldsAndNeverWaits: the only row a purge could
// fight a request for is one that is locked AND expired — a claim stamped more
// than a TTL ago whose handler is still running (claimSQL takes the row lock
// until its transaction ends). The purge must skip it without waiting and take
// it on a later pass. Store-level over a real Postgres, Kafka-free: it runs
// after the flow test, which stops the shared broker.
func TestThePurgeSkipsARowARequestHoldsAndNeverWaits(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
		VALUES ('', 'locked', 'h', 201, 'application/json', '{}'::bytea, now() - interval '25 hours')`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// What claimSQL does for a request whose key expired under it: the row is
	// taken over (and stays expired: created_at is the request's own stamp)
	// and locked until the request's transaction ends.
	if _, err := tx.Exec(ctx, `INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
		VALUES ('', 'locked', 'h2', 0, '', ''::bytea, now() - interval '25 hours')
		ON CONFLICT (route, key) DO UPDATE SET request_hash = 'h2', status = 0,
			content_type = '', body = ''::bytea, created_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}

	store := postgres.NewIdempotencyStore(pool)
	purgeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	start := time.Now()
	n, err := store.PurgeExpired(purgeCtx, 100)
	if err != nil || n != 0 {
		t.Fatalf("purge while a request holds the row = %d %v, want 0 and no wait", n, err)
	}
	if wait := time.Since(start); wait > 5*time.Second {
		t.Fatalf("the purge waited %s on the locked row: it must skip it", wait)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PurgeExpired(ctx, 100); err != nil || n != 1 {
		t.Fatalf("purge after the request committed = %d %v, want the still-expired row gone", n, err)
	}
}
