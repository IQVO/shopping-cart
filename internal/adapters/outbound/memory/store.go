// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every repository round-trips its aggregate through Snapshot and Restore, so
// a loaded aggregate never aliases stored state, a corrupt row can not become a
// live aggregate and the doubles keep the persistence contract of the real
// adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store
// for the whole unit and restores the state taken at the start when the unit
// fails or panics, outbox, inbox and read models included.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/idempotency"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/readmodel"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu          sync.Mutex
	carts       map[cart.ID]cart.Snapshot
	items       map[string]readmodel.Item
	schedule    *estimate.Schedule
	taxTable    *estimate.TaxTable
	inbox       map[string]string
	outbox      []shared.Event
	idempotency map[string]idempotency.Record
	clock       ports.Clock
}

// NewStore returns an empty store. The clock is the authority the
// IdempotencyStore judges record TTL against: the doubles exist to test a
// domain whose clock is frozen, so TTL must be measured on that same clock,
// never on the wall.
func NewStore(clock ports.Clock) *Store {
	return &Store{
		carts: map[cart.ID]cart.Snapshot{}, items: map[string]readmodel.Item{},
		inbox: map[string]string{}, idempotency: map[string]idempotency.Record{}, clock: clock,
	}
}

func (s *Store) now() time.Time { return s.clock.Now() }

// state is a copy of everything a unit of work can change. Stored values are
// replaced, never edited in place, so copying the maps is enough.
type state struct {
	carts    map[cart.ID]cart.Snapshot
	items    map[string]readmodel.Item
	schedule *estimate.Schedule
	taxTable *estimate.TaxTable
	inbox    map[string]string
	outbox   []shared.Event
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (s *Store) capture() state {
	return state{
		carts: cloneMap(s.carts), items: cloneMap(s.items), schedule: s.schedule, taxTable: s.taxTable,
		inbox: cloneMap(s.inbox), outbox: append([]shared.Event(nil), s.outbox...),
	}
}

func (s *Store) restore(st state) {
	s.carts, s.items, s.schedule, s.taxTable, s.inbox, s.outbox = st.carts, st.items, st.schedule, st.taxTable, st.inbox, st.outbox
}

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Carts returns the CartRepository double.
func (s *Store) Carts() *CartRepository { return &CartRepository{s} }

// Items returns the ItemRepository double.
func (s *Store) Items() *ItemRepository { return &ItemRepository{s} }

// Schedules returns the ScheduleRepository double.
func (s *Store) Schedules() *ScheduleRepository { return &ScheduleRepository{s} }

// TaxTables returns the TaxTableRepository double.
func (s *Store) TaxTables() *TaxTableRepository { return &TaxTableRepository{s} }

// Inbox returns the Inbox double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Idempotency returns the IdempotencyStore double.
func (s *Store) Idempotency() *IdempotencyStore { return &IdempotencyStore{s} }

// Counts is how many rows each table of the store holds.
type Counts struct {
	Carts, Items, Inbox, OutboxEvents int
	// VersionSum is the sum of every aggregate version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate
// versions, so a test can prove that a failed use case stored and changed
// nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var versions int64
	for _, c := range s.carts {
		versions += c.Version
	}
	return Counts{Carts: len(s.carts), Items: len(s.items), Inbox: len(s.inbox), OutboxEvents: len(s.outbox), VersionSum: versions}
}
