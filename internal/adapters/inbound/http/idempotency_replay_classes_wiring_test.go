package http_test

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	httpadapter "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/idempotency"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// wiringT0 is the fixed instant the memory clock reads.
var wiringT0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// newReplayOps returns the six operations over ONE clean store: the in-memory
// double of ports.IdempotencyStore (the same double the handler tests use; the
// Postgres store is held to the same behaviour by the repository contract suite
// and by cmd/api/idempotency_retryable_integration_test.go). A write is a cart
// saved THROUGH the request's context, i.e. in the unit of work the store opened
// for the request; releasing is the state restore the memory store does for a
// 5xx, so KeyRows 0 and Committed 0 are real proofs.
func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	clock := memory.NewClock(wiringT0)
	store := memory.NewStore(clock)
	errs := map[string]error{}
	for _, row := range httpadapter.Catalogue() {
		if _, ok := errs[row.Slug]; !ok {
			errs[row.Slug] = row.Err
		}
	}
	var (
		mu    sync.Mutex
		saved = map[string][]string{} // tag -> the cart ids written under that tag
	)
	nextID := func(tag string) string {
		mu.Lock()
		defer mu.Unlock()
		id := fmt.Sprintf("cart-%s-%d", tag, len(saved[tag])+1)
		saved[tag] = append(saved[tag], id)
		return id
	}
	return replayOps{
		Catalogue: func() []problemEntry {
			var out []problemEntry
			seen := map[string]bool{}
			for _, row := range httpadapter.Catalogue() {
				// Two catalogue entries can share one problem (not-found,
				// delivery-promise-unavailable): they collapse to one row here,
				// and a same-slug entry declaring ANOTHER class would surface
				// as a duplicate the ADR test flags.
				k := row.Slug + "\x00" + strconv.Itoa(row.Status) + "\x00" + row.Class
				if seen[k] {
					continue
				}
				seen[k] = true
				out = append(out, problemEntry{Slug: row.Slug, Status: row.Status, Class: row.Class})
			}
			return out
		},
		Middleware: func(_ *testing.T, next http.Handler) http.Handler {
			return httpadapter.RequireIdempotencyKey(store.Idempotency(), clock)(next)
		},
		Refuse: func(w http.ResponseWriter, r *http.Request, e problemEntry) {
			httpadapter.WriteError(w, r, errs[e.Slug])
		},
		Write: func(ctx context.Context, tag string) error {
			c, err := cart.Create(cart.ID(nextID(tag)), "shopper-replay", wiringT0)
			if err != nil {
				return err
			}
			return store.Carts().Save(ctx, c)
		},
		Committed: func(t *testing.T, tag string) int {
			t.Helper()
			mu.Lock()
			ids := append([]string(nil), saved[tag]...)
			mu.Unlock()
			n := 0
			for _, id := range ids {
				if _, err := store.Carts().Get(context.Background(), cart.ID(id)); err == nil {
					n++
				}
			}
			return n
		},
		KeyRows: func(t *testing.T, key string) int {
			t.Helper()
			// The probe answers a 5xx, which is never stored and undoes its
			// writes: it finds a live record (replayed) or runs and leaves
			// nothing behind.
			_, replayed, err := store.Idempotency().Do(context.Background(), key, "probe", clock.Now(),
				func(context.Context) (idempotency.Record, error) {
					return idempotency.Record{Status: http.StatusInternalServerError}, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if replayed {
				return 1
			}
			return 0
		},
	}
}
