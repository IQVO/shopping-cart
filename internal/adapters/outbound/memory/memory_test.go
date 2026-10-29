package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/repocontract"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func TestMemoryDoublesHonourTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Env {
		s := memory.NewStore(memory.NewClock(t0))
		return repocontract.Env{
			Carts: s.Carts(), Items: s.Items(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Inbox: s.Inbox(),
			Outbox: s.Outbox(), UoW: s.UnitOfWork(), Idempotency: s.Idempotency(), Clock: memory.NewClock(t0),
			OutboxCount: func(*testing.T) int { return len(s.Outbox().Events()) },
		}
	})
}

// t0 is an arbitrary FIXED instant: the store judges TTL on the injected
// clock, never on the wall, so this date never goes stale.
var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func TestQuoterRecordsCallsAndReplaysTheScriptedAnswer(t *testing.T) {
	q := &memory.Quoter{}
	boom := errors.New("boom")
	q.Respond(cart.DeliveryQuote{}, boom)
	_, err := q.Quote(context.Background(), "k", "787", []cart.Line{{CatalogItemID: "a", Quantity: 1}})
	if !errors.Is(err, boom) || len(q.Calls()) != 1 || q.Calls()[0].Destination != "787" {
		t.Fatalf("%v %+v", err, q.Calls())
	}
}

func TestClockAndIDs(t *testing.T) {
	c := memory.NewClock(t0)
	c.Advance(time.Hour)
	if !c.Now().Equal(t0.Add(time.Hour)) {
		t.Fatal("advance")
	}
	c.Set(t0)
	if !c.Now().Equal(t0) {
		t.Fatal("set")
	}
	g := &memory.SequentialIDs{}
	if g.NewCartID() != "cart-1" || g.NewCartID() != "cart-2" {
		t.Fatal("ids")
	}
}

func TestAMissIsThePortSentinel(t *testing.T) {
	s := memory.NewStore(memory.NewClock(t0))
	if _, err := s.Carts().Get(context.Background(), "x"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatal(err)
	}
}
