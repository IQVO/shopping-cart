//go:build integration

package main

import (
	"net/http"
	"sync"
	"testing"
)

// idempotencyOverPostgres drives the Idempotency-Key contract through the
// shipped binary (real router, real Postgres store joined to the unit of work).
func idempotencyOverPostgres(t *testing.T, svc *service) {
	t.Helper()
	t.Run("a missing key is 400 idempotency-key-required", func(t *testing.T) {
		r := svc.call(t, "POST", "/carts", "", `{"shopperId":"shopper-nokey"}`)
		if r.status != http.StatusBadRequest || r.json(t)["type"] != "/problems/idempotency-key-required" {
			t.Fatalf("no key = %d %s", r.status, r.body)
		}
		if n := countCarts(t, svc, "shopper-nokey"); n != 0 {
			t.Fatalf("a refused request created %d carts", n)
		}
	})
	t.Run("the same key with another body is 409 idempotency-key-reused", func(t *testing.T) {
		first := svc.call(t, "POST", "/carts", "k-reuse", `{"shopperId":"shopper-reuse"}`)
		if first.status != http.StatusCreated || first.header.Get("Idempotency-Replayed") != "" {
			t.Fatalf("first = %d %s (%v)", first.status, first.body, first.header)
		}
		r := svc.call(t, "POST", "/carts", "k-reuse", `{"shopperId":"shopper-other"}`)
		if r.status != http.StatusConflict || r.json(t)["type"] != "/problems/idempotency-key-reused" {
			t.Fatalf("reuse = %d %s", r.status, r.body)
		}
		if n := countCarts(t, svc, "shopper-other"); n != 0 {
			t.Fatalf("a reused key created %d carts", n)
		}
	})
	t.Run("concurrent requests with one key create exactly one cart", func(t *testing.T) { concurrentSameKey(t, svc) })
	t.Run("a 4xx answer is cached and replayed byte for byte", func(t *testing.T) {
		const path, body = "/carts/ghost-cart/lines", `{"catalogItemId":"sku-100","quantity":1}`
		first := svc.call(t, "POST", path, "k-404", body)
		again := svc.call(t, "POST", path, "k-404", body)
		if first.status != http.StatusNotFound || again.status != http.StatusNotFound || again.body != first.body ||
			first.header.Get("Idempotency-Replayed") != "" || again.header.Get("Idempotency-Replayed") != "true" {
			t.Fatalf("first = %d %s (%v), again = %d %s (%v)", first.status, first.body, first.header, again.status, again.body, again.header)
		}
	})
}

// concurrentSameKey sends n identical keyed creates at once: exactly one runs,
// the others replay its answer.
func concurrentSameKey(t *testing.T, svc *service) {
	t.Helper()
	const n = 16
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		fresh   int
		cartIDs = map[string]bool{}
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := svc.call(t, "POST", "/carts", "k-race", `{"shopperId":"shopper-race"}`)
			mu.Lock()
			defer mu.Unlock()
			if r.status != http.StatusCreated {
				t.Errorf("concurrent create = %d %s", r.status, r.body)
				return
			}
			if r.header.Get("Idempotency-Replayed") == "" {
				fresh++
			}
			cartIDs[r.json(t)["cartId"].(string)] = true
		}()
	}
	wg.Wait()
	if fresh != 1 || len(cartIDs) != 1 {
		t.Fatalf("%d executions and %d distinct carts from %d identical requests, want 1 and 1", fresh, len(cartIDs), n)
	}
	if got := countCarts(t, svc, "shopper-race"); got != 1 {
		t.Fatalf("%d carts stored for shopper-race, want 1", got)
	}
}

// countCarts is the number of carts GET /carts lists for the shopper.
func countCarts(t *testing.T, svc *service, shopper string) int {
	t.Helper()
	r := svc.call(t, "GET", "/carts?shopperId="+shopper, "", "")
	if r.status != http.StatusOK {
		t.Fatalf("list = %d %s", r.status, r.body)
	}
	return len(r.json(t)["items"].([]any))
}
