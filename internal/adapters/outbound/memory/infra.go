package memory

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/idempotency"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

var (
	_ ports.Inbox            = (*Inbox)(nil)
	_ ports.EventOutbox      = (*Outbox)(nil)
	_ ports.UnitOfWork       = (*UnitOfWork)(nil)
	_ ports.IdempotencyStore = (*IdempotencyStore)(nil)
)

// Inbox is the in-memory ports.Inbox.
type Inbox struct{ s *Store }

// Claim implements ports.Inbox.
func (i *Inbox) Claim(ctx context.Context, eventID, eventType string) (bool, error) {
	defer i.s.enter(ctx)()
	if _, seen := i.s.inbox[eventID]; seen {
		return false, nil
	}
	i.s.inbox[eventID] = eventType
	return true, nil
}

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn holding the store. When fn returns an error or panics the state
// taken before fn is restored; the error is returned unchanged. A Do inside a
// Do joins it but is atomic on its own (the state taken before the inner fn is
// restored when only the inner fn fails), like the Postgres savepoint.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		before := u.s.capture()
		if err = fn(ctx); err != nil {
			u.s.restore(before)
		}
		return err
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.capture()
	committed := false
	defer func() {
		if !committed {
			u.s.restore(before)
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, struct{}{}))
	committed = err == nil
	return err
}

// Outbox is the in-memory ports.EventOutbox.
type Outbox struct{ s *Store }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.outbox = append(o.s.outbox, events...)
	return nil
}

// Events returns the appended events in order (a copy of the slice).
func (o *Outbox) Events() []shared.Event {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return append([]shared.Event(nil), o.s.outbox...)
}

// IdempotencyStore is the in-memory ports.IdempotencyStore. Records older than
// idempotency.TTL are treated as absent.
type IdempotencyStore struct{ s *Store }

// Do implements ports.IdempotencyStore. Like a unit of work it holds the store
// while fn runs, so same-key requests are serialised and a failed, 5xx or
// released fn leaves no write behind (ADR 0011).
func (i *IdempotencyStore) Do(ctx context.Context, key, requestHash string, at time.Time,
	fn func(ctx context.Context) (idempotency.Record, error),
) (idempotency.Record, bool, error) {
	i.s.mu.Lock()
	defer i.s.mu.Unlock()
	k := key
	if old, ok := i.s.idempotency[k]; ok && i.s.now().Sub(old.CreatedAt) <= idempotency.TTL {
		return old, true, nil
	}
	before := i.s.capture()
	stored := false
	defer func() {
		if !stored {
			i.s.restore(before)
		}
	}()
	rec, err := fn(context.WithValue(ctx, txKey{i.s}, struct{}{}))
	if err != nil {
		return idempotency.Record{}, false, err
	}
	if rec.Release || rec.Status >= http.StatusInternalServerError {
		return rec, false, nil
	}
	rec.RequestHash, rec.CreatedAt = requestHash, at
	i.s.idempotency[k] = rec
	stored = true
	return rec, false, nil
}

// PurgeExpired implements ports.IdempotencyStore: it deletes at most limit
// answers that are no longer replayed (older than idempotency.TTL), oldest
// first, and returns how many. Do holds the same mutex while it runs fn, so a
// purge that overlaps a request waits for it and then finds its answer live.
func (i *IdempotencyStore) PurgeExpired(_ context.Context, limit int) (int, error) {
	i.s.mu.Lock()
	defer i.s.mu.Unlock()
	expired := make([]string, 0, len(i.s.idempotency))
	for k, rec := range i.s.idempotency {
		if i.s.now().Sub(rec.CreatedAt) > idempotency.TTL {
			expired = append(expired, k)
		}
	}
	sort.Slice(expired, func(a, b int) bool {
		return i.s.idempotency[expired[a]].CreatedAt.Before(i.s.idempotency[expired[b]].CreatedAt)
	})
	if len(expired) > limit {
		expired = expired[:max(limit, 0)]
	}
	for _, k := range expired {
		delete(i.s.idempotency, k)
	}
	return len(expired), nil
}

// Keys returns the stored keys oldest first (test support: the repocontract
// purge suite asks which rows survived a pass).
func (i *IdempotencyStore) Keys() []string {
	i.s.mu.Lock()
	defer i.s.mu.Unlock()
	keys := make([]string, 0, len(i.s.idempotency))
	for k := range i.s.idempotency {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(a, b int) bool {
		return i.s.idempotency[keys[a]].CreatedAt.Before(i.s.idempotency[keys[b]].CreatedAt)
	})
	return keys
}
