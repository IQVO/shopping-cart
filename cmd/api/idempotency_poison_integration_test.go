//go:build integration

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	inboundhttp "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/shopping-cart/internal/application/idempotency"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

// The tcpostgres reference keeps the testcontainers postgres module import
// reachable in this file (internal/architecture's
// TestPostgresIntegrationTestsUseTestcontainers); the database itself comes
// from the shared pgtest helper.
var _ = tcpostgres.Run

type poisonClock struct{}

func (poisonClock) Now() time.Time { return time.Now() }

// poisonCtx is poisonTx for a bare context (the store-level test below).
func poisonCtx(t *testing.T, ctx context.Context) {
	t.Helper()
	tx, ok := pgtx.From(ctx)
	if !ok {
		t.Error("the middleware did not bind a transaction into the request context")
		return
	}
	const dup = `INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
		VALUES ('poison', 'dup', 'h', 0, '', ''::bytea, now())`
	for range 2 {
		if _, err := tx.Exec(ctx, dup); err != nil {
			return // the second insert aborts the transaction (23505)
		}
	}
	t.Error("the duplicate insert did not abort the transaction")
}

// poisonTx simulates what a mapped domain conflict does to the middleware's
// transaction: a statement fails on the server (a unique violation, like a lost
// race on a unique index), the repository translates it to a typed error and
// the handler answers 4xx, but the transaction is ABORTED from then on.
func poisonTx(t *testing.T, r *http.Request) {
	t.Helper()
	poisonCtx(t, r.Context())
}

func poisonRequest(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/carts/cart-x/lines", strings.NewReader(`{}`))
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	return req
}

func poisonCount(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestIdempotencyAFinalRefusalOverAnAbortedTransactionIsStillStored: a domain
// conflict that fails a statement server-side leaves the transaction ABORTED
// while the handler answers 4xx. The store heals that transaction (ROLLBACK TO
// SAVEPOINT discards only the handler's partial writes) and stores the refusal
// when its class is FINAL (Record.Release false, ADR 0011): the 409 commits and
// a retry under the same key replays it. A RETRYABLE refusal over the same
// aborted transaction simply rolls back and releases the key, as
// idempotency_retryable_integration_test.go proves through the real router.
func TestIdempotencyAFinalRefusalOverAnAbortedTransactionIsStillStored(t *testing.T) {
	pool := pgtest.NewPool(t)
	store := postgres.NewIdempotencyStore(pool)
	var calls atomic.Int64
	refusal := func(ctx context.Context) (idempotency.Record, error) {
		calls.Add(1)
		poisonCtx(t, ctx)
		// What capture.record() yields for a FINAL refusal: a 4xx, not released.
		return idempotency.Record{Status: http.StatusConflict, ContentType: "application/problem+json",
			Body: []byte(`{"type":"/problems/concurrent-update","title":"Conflict","status":409}`)}, nil
	}

	rec, replayed, err := store.Do(context.Background(), "k-poison", "h", time.Now().UTC(), refusal)
	if err != nil || replayed || rec.Status != http.StatusConflict {
		t.Fatalf("first call: rec=%+v replayed=%v err=%v, want the truthful 409 answered fresh", rec, replayed, err)
	}
	if n := poisonCount(t, pool, `SELECT count(*) FROM idempotency_keys WHERE route = '' AND key = 'k-poison' AND status = 409`); n != 1 {
		t.Fatalf("the FINAL 409 outcome was not committed (%d rows)", n)
	}
	if n := poisonCount(t, pool, `SELECT count(*) FROM idempotency_keys WHERE route = 'poison'`); n != 0 {
		t.Fatalf("the handler's partial write survived (%d rows)", n)
	}

	again, replayed, err := store.Do(context.Background(), "k-poison", "h", time.Now().UTC(), refusal)
	if err != nil || !replayed || again.Status != http.StatusConflict || string(again.Body) != string(rec.Body) {
		t.Fatalf("replay: rec=%+v replayed=%v err=%v, want the cached 409", again, replayed, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1 (the replay must not re-run it)", calls.Load())
	}
}

// TestIdempotencyA2xxOverAnAbortedTransactionIsNeverCached: a success answer
// whose writes died with the transaction is a lie if returned or cached. The
// request fails and the same key retries the real work.
func TestIdempotencyA2xxOverAnAbortedTransactionIsNeverCached(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			poisonTx(t, r) // only the first attempt dies inside the transaction
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"cartId":"c1"}`))
	})
	mw := inboundhttp.RequireIdempotencyKey(postgres.NewIdempotencyStore(pool), poisonClock{})(handler)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, poisonRequest("k-lie"))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first = %d %s, want 500 (the writes never happened)", first.Code, first.Body.String())
	}
	if n := poisonCount(t, pool, `SELECT count(*) FROM idempotency_keys WHERE key = 'k-lie'`); n != 0 {
		t.Fatalf("a failed success left %d idempotency rows: the key could not retry", n)
	}

	second := httptest.NewRecorder()
	mw.ServeHTTP(second, poisonRequest("k-lie"))
	if second.Code != http.StatusCreated {
		t.Fatalf("retry = %d %s, want the real work to succeed", second.Code, second.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", calls.Load())
	}
}
