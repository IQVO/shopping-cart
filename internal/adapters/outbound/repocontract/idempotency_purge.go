package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/idempotency"
)

// testIdempotencyPurge is the retention contract of PurgeExpired: the memory
// double and the Postgres store both run it (the replay contract of Do lives
// above). Rows are aged by the stamp the suite passes to Do (Record.CreatedAt /
// created_at), the same clock the implementation judges the TTL with, so a test
// reaches an expired row without waiting a day.
func testIdempotencyPurge(t *testing.T, e Env) {
	t.Helper()
	t.Run("deletes expired rows and keeps live ones", func(t *testing.T) { purgeKeepsLive(t, e) })
	t.Run("one batch per pass, the next passes drain the rest", func(t *testing.T) { purgeBatches(t, e) })
	t.Run("oldest first", func(t *testing.T) { purgeOldestFirst(t, e) })
	t.Run("a purge overlapping a request holding the key", func(t *testing.T) { purgeHeldKey(t, e) })
}

// putAged stores a 201 answer under key stamped age ago.
func putAged(t *testing.T, e Env, key string, age time.Duration) {
	t.Helper()
	at := e.Clock.Now().UTC().Add(-age)
	_, replayed, err := e.Idempotency.Do(bg(), key, "h", at, func(context.Context) (idempotency.Record, error) {
		return idempotency.Record{Status: 201, ContentType: "text/plain", Body: []byte(key)}, nil
	})
	if err != nil || replayed {
		t.Fatalf("store %s: replayed=%v err=%v", key, replayed, err)
	}
}

// wantReplay asserts the key still answers with its stored body.
func wantReplay(t *testing.T, e Env, key, want string) {
	t.Helper()
	rec, replayed, err := idemDo(e, key, "other", func(context.Context) (idempotency.Record, error) {
		return idempotency.Record{Status: 200, Body: []byte("ran")}, nil
	})
	if err != nil || !replayed || string(rec.Body) != want {
		t.Fatalf("replay %s = %q replayed=%v err=%v, want the stored %q", key, rec.Body, replayed, err, want)
	}
}

// purgeKeepsLive: a row past the TTL goes, a row a minute short of it and a
// fresh one stay and still replay; a second pass has nothing left to delete.
func purgeKeepsLive(t *testing.T, e Env) {
	putAged(t, e, "old", idempotency.TTL+time.Minute)
	putAged(t, e, "edge", idempotency.TTL-time.Minute)
	putAged(t, e, "live", time.Minute)
	if n, err := e.Idempotency.PurgeExpired(bg(), 100); err != nil || n != 1 {
		t.Fatalf("purge = %d %v, want 1 (only the expired row)", n, err)
	}
	wantReplay(t, e, "edge", "edge")
	wantReplay(t, e, "live", "live")
	if n, err := e.Idempotency.PurgeExpired(bg(), 100); err != nil || n != 0 {
		t.Fatalf("second purge = %d %v, want 0", n, err)
	}
}

// purgeBatches: one pass deletes at most limit rows, the next ones finish.
func purgeBatches(t *testing.T, e Env) {
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		putAged(t, e, k, idempotency.TTL+time.Hour)
	}
	for _, want := range []int{2, 2, 1, 0} {
		if n, err := e.Idempotency.PurgeExpired(bg(), 2); err != nil || n != want {
			t.Fatalf("purge = %d %v, want %d", n, err, want)
		}
	}
}

// purgeOldestFirst: with two expired rows and a limit of one, the older goes;
// the survivor is gone on the next pass. (The suite's Env is shared by its
// subtests, so earlier rows are still stored: the OLDEST survivor being the
// newer of the two is what proves the older one was deleted.)
func purgeOldestFirst(t *testing.T, e Env) {
	putAged(t, e, "older", idempotency.TTL+2*time.Hour)
	putAged(t, e, "newer", idempotency.TTL+30*time.Minute)
	if n, err := e.Idempotency.PurgeExpired(bg(), 1); err != nil || n != 1 {
		t.Fatalf("purge = %d %v, want 1", n, err)
	}
	if got := e.IdempotencyKeys(t); len(got) == 0 || got[0] != "newer" {
		t.Fatalf("rows left = %v, want the newer expired row as the oldest survivor", got)
	}
	if n, err := e.Idempotency.PurgeExpired(bg(), 1); err != nil || n != 1 {
		t.Fatalf("the next pass = %d %v, want the survivor gone", n, err)
	}
}

// purgeHeldKey: a purge that overlaps a request taking over the (expired) key
// neither fails nor breaks the request: the claim refreshes the row, so it is
// live when the purge looks, the request answers fresh, and its answer replays
// afterwards. (The memory double serialises on the store mutex, so its purge
// waits for fn and then finds the fresh answer; Postgres skips the locked row.)
func purgeHeldKey(t *testing.T, e Env) {
	putAged(t, e, "held", idempotency.TTL+time.Hour)
	inFlight, release := make(chan struct{}), make(chan struct{})
	type result struct {
		body     string
		replayed bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		rec, replayed, err := e.Idempotency.Do(bg(), "held", "h2", e.Clock.Now().UTC(),
			func(context.Context) (idempotency.Record, error) {
				close(inFlight)
				<-release
				return idempotency.Record{Status: 201, ContentType: "text/plain", Body: []byte("fresh")}, nil
			})
		done <- result{string(rec.Body), replayed, err}
	}()
	<-inFlight
	purged := make(chan error, 1)
	go func() { _, err := e.Idempotency.PurgeExpired(bg(), 100); purged <- err }()
	time.Sleep(20 * time.Millisecond) // let the purge overlap the request
	close(release)
	if err := <-purged; err != nil {
		t.Fatalf("purge during an in-flight request: %v", err)
	}
	if r := <-done; r.err != nil || r.replayed || r.body != "fresh" {
		t.Fatalf("the request holding the key = %q replayed=%v err=%v, want a fresh answer", r.body, r.replayed, r.err)
	}
	wantReplay(t, e, "held", "fresh")
}
