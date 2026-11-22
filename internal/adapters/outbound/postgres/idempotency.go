package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres/pgtx"
	"github.com/IQVO/shopping-cart/internal/application/idempotency"
	"github.com/IQVO/shopping-cart/internal/application/ports"
)

// IdempotencyStore is the Postgres ports.IdempotencyStore over idempotency_keys.
type IdempotencyStore struct{ pool *pgxpool.Pool }

var _ ports.IdempotencyStore = (*IdempotencyStore)(nil)

// NewIdempotencyStore constructs an IdempotencyStore over pool.
func NewIdempotencyStore(pool *pgxpool.Pool) *IdempotencyStore { return &IdempotencyStore{pool: pool} }

func ttlSeconds() float64 { return idempotency.TTL.Seconds() }

// claimAttempts bounds how often Do re-claims a key whose live row vanished
// between the failed claim and the read (an expiry race).
const claimAttempts = 3

// httpClientError is the first status that is not a success.
const httpClientError = 400

// httpServerError is the first status that is never stored (a 5xx).
const httpServerError = 500

// claimSQL inserts the key as a placeholder inside the request's transaction. A
// concurrent claimant of the same key blocks on the unique index until this
// transaction ends: it then finds the committed answer (no row is affected) or,
// after a rollback, claims the key itself. An expired row is taken over.
const claimSQL = `
	INSERT INTO idempotency_keys (route, key, request_hash, status, content_type, body, created_at)
	VALUES ('', $1, $2, 0, '', ''::bytea, $3)
	ON CONFLICT (route, key) DO UPDATE SET request_hash = EXCLUDED.request_hash, status = 0,
		content_type = '', body = ''::bytea, created_at = EXCLUDED.created_at
	WHERE idempotency_keys.created_at <= now() - make_interval(secs => $4)`

const storeSQL = `UPDATE idempotency_keys SET status = $2, content_type = $3, body = $4 WHERE route = '' AND key = $1`

const readSQL = `
	SELECT request_hash, status, content_type, body, created_at FROM idempotency_keys
	WHERE route = '' AND key = $1 AND created_at > now() - make_interval(secs => $2)`

// purgeSQL deletes the oldest expired rows. The subselect takes them FOR
// UPDATE SKIP LOCKED, so a row another transaction holds (a claim in flight)
// is left for a later pass instead of waited on, and the age test is repeated
// on the DELETE itself, so a row a claim just refreshed (the take-over of an
// expired key resets created_at) is never deleted. It takes no advisory lock:
// an expired row is already invisible to Do, so a request holding its key
// neither waits for the purge nor sees it.
const purgeSQL = `
	DELETE FROM idempotency_keys
	WHERE (route, key) IN (
		SELECT route, key FROM idempotency_keys
		WHERE created_at <= now() - make_interval(secs => $1)
		ORDER BY created_at
		LIMIT $2
		FOR UPDATE SKIP LOCKED)
	AND created_at <= now() - make_interval(secs => $1)`

// Do implements ports.IdempotencyStore: the claim, fn's writes (through the
// transaction carried by the ctx it is handed, so every unit of work joins it)
// and the stored answer commit together.
//
// fn runs behind SAVEPOINT handler_run. A mapped domain conflict (a lost race
// on a unique index, 23505 answered as a truthful 409, say) fails a statement
// on the server and leaves the shared transaction ABORTED even though the
// handler answers 4xx; storing that outcome would then fail with 25P02 and the
// client would see a 500. Releasing the savepoint exposes the abort: the
// release fails, ROLLBACK TO SAVEPOINT discards only the handler's partial
// writes and heals the transaction (the claim before the savepoint survives),
// and the 4xx is stored and committed like any other. A success answer over an
// aborted transaction is a lie (its writes are gone): it rolls everything back
// and fails the request, so the same key retries the real work.
func (s *IdempotencyStore) Do(ctx context.Context, key, requestHash string, at time.Time,
	fn func(ctx context.Context) (idempotency.Record, error),
) (idempotency.Record, bool, error) {
	for range claimAttempts {
		rec, replayed, retry, err := s.attempt(ctx, key, requestHash, at, fn)
		if !retry {
			return rec, replayed, err
		}
	}
	return idempotency.Record{}, false, fmt.Errorf("claim idempotency key: the stored record kept expiring under the claim")
}

func (s *IdempotencyStore) attempt(ctx context.Context, key, requestHash string, at time.Time,
	fn func(ctx context.Context) (idempotency.Record, error),
) (rec idempotency.Record, replayed, retry bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return rec, false, false, fmt.Errorf("begin idempotent request: %w", err)
	}
	// A rollback after Commit is a harmless no-op; it also covers a panic in fn.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, claimSQL, key, requestHash, at.UTC(), ttlSeconds())
	if err != nil {
		return rec, false, false, fmt.Errorf("claim idempotency key: %w", err)
	}
	if tag.RowsAffected() == 0 {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return s.replay(ctx, key)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT handler_run"); err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("open handler savepoint: %w", err)
	}
	if rec, err = fn(pgtx.With(ctx, tx)); err != nil {
		return idempotency.Record{}, false, false, err
	}
	if rec.Release || rec.Status >= httpServerError {
		return rec, false, false, nil // rolled back by the defer, key row included: a released answer and a 5xx are never stored
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT handler_run"); err != nil {
		// A statement inside the handler failed on the server: the
		// transaction is aborted. Roll back to the savepoint to discard the
		// handler's partial writes and return the transaction to health.
		if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT handler_run"); rerr != nil {
			return idempotency.Record{}, false, false, fmt.Errorf("heal aborted transaction: %w", rerr)
		}
		if rec.Status < httpClientError {
			// A success whose writes are gone must be neither returned nor
			// cached: fail (the defer rolls back) so the key retries.
			return idempotency.Record{}, false, false, fmt.Errorf("release handler savepoint: %w", err)
		}
	}
	body := rec.Body
	if body == nil {
		body = []byte{}
	}
	if _, err := tx.Exec(ctx, storeSQL, key, rec.Status, rec.ContentType, body); err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("store idempotency key: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("commit idempotent request: %w", err)
	}
	rec.RequestHash, rec.CreatedAt = requestHash, at.UTC()
	return rec, false, false, nil
}

// replay reads the committed record that made the claim fail; it asks for a new
// claim when the record expired or was removed in between.
func (s *IdempotencyStore) replay(ctx context.Context, key string) (rec idempotency.Record, replayed, retry bool, err error) {
	err = s.pool.QueryRow(ctx, readSQL, key, ttlSeconds()).
		Scan(&rec.RequestHash, &rec.Status, &rec.ContentType, &rec.Body, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.Record{}, false, true, nil
	}
	if err != nil {
		return idempotency.Record{}, false, false, fmt.Errorf("read idempotency key: %w", err)
	}
	rec.CreatedAt = rec.CreatedAt.UTC()
	return rec, true, false, nil
}

// PurgeExpired implements ports.IdempotencyStore: the retention. At most limit
// rows older than the TTL go, oldest first (idempotency_keys_created_idx); a
// result equal to limit means more may remain.
func (s *IdempotencyStore) PurgeExpired(ctx context.Context, limit int) (int, error) {
	tag, err := s.pool.Exec(ctx, purgeSQL, ttlSeconds(), limit)
	if err != nil {
		return 0, fmt.Errorf("purge idempotency keys: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
