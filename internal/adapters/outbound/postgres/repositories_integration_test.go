//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/kafka"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/repocontract"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

func TestPostgresHonoursTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Env {
		pool := pgtest.NewPool(t)
		return repocontract.Env{
			Carts: postgres.NewCartRepository(pool), Items: postgres.NewItemRepository(pool),
			Schedules: postgres.NewScheduleRepository(pool), TaxTables: postgres.NewTaxTableRepository(pool),
			Inbox: postgres.NewInbox(pool), Outbox: postgres.NewOutboxRepo(pool, kafka.NewEncoder()),
			UoW: postgres.NewUnitOfWork(pool), Idempotency: postgres.NewIdempotencyStore(pool),
			// The Postgres store judges TTL with the database's now(); the
			// contract stamps records with this wall clock.
			Clock: wallClock{},
			OutboxCount: func(t *testing.T) int {
				var n int
				if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				return n
			},
			IdempotencyKeys: func(t *testing.T) []string {
				var keys []string
				rows, err := pool.Query(context.Background(), `SELECT key FROM idempotency_keys ORDER BY created_at, key`)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				for rows.Next() {
					var k string
					if err := rows.Scan(&k); err != nil {
						t.Fatal(err)
					}
					keys = append(keys, k)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				return keys
			},
		}
	})
}

// wallClock is the real time: created_at and now() live in the same database.
type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }
