// Package repocontract is the repository contract suite: the behaviours every
// persistence adapter (the in-memory doubles and Postgres) must show. It is
// test support (it imports "testing") kept out of _test.go files so both
// adapters can run it; the shipped binary never links it (enforced by
// internal/architecture).
package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// Env is what an adapter hands to the suite. Every test gets a fresh Env.
type Env struct {
	Carts       ports.CartRepository
	Items       ports.ItemRepository
	Schedules   ports.ScheduleRepository
	TaxTables   ports.TaxTableRepository
	Inbox       ports.Inbox
	Outbox      ports.EventOutbox
	UoW         ports.UnitOfWork
	Idempotency ports.IdempotencyStore
	// Clock is the store's time authority: testIdempotency stamps records
	// with it, so the expired-record case expires relative to the SAME clock
	// the implementation under test judges TTL with (the memory double is
	// driven by a frozen clock; the Postgres store by the database's now()).
	Clock ports.Clock
	// OutboxCount returns how many events the outbox holds.
	OutboxCount func(t *testing.T) int
}

// Run executes the whole suite; newEnv must return an empty Env each time.
func Run(t *testing.T, newEnv func(t *testing.T) Env) {
	t.Helper()
	suites := map[string]func(*testing.T, Env){
		"carts":         testCarts,
		"listbyshopper": listByShopper,
		"listidle":      listIdle,
		"deleteclosed":  deleteClosed,
		"items":         testItems,
		"estimate":      testEstimateViews,
		"inbox":         testInbox,
		"unitofwork":    testUnitOfWork,
		"idempotency":   testIdempotency,
	}
	for name, fn := range suites {
		t.Run(name, func(t *testing.T) { fn(t, newEnv(t)) })
	}
}

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// at returns t0 plus m minutes.
func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

var bg = context.Background

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

func newCart(t *testing.T, id, shopper string, created time.Time) *cart.Cart {
	t.Helper()
	c, err := cart.Create(cart.ID(id), shopper, created)
	must(t, err)
	return c
}

// fullCart has two lines, a destination, a two-group quote and one selection.
func fullCart(t *testing.T, id string) *cart.Cart {
	t.Helper()
	c := newCart(t, id, "shopper-1", t0)
	must(t, c.AddLine("item-a", 2, true, at(1)))
	must(t, c.AddLine("item-b", 1, true, at(1)))
	must(t, c.SetDestination("787", at(2)))
	exp := at(60)
	offer := func(id string, g int, tier cart.Tier) cart.QuotedOffer {
		return cart.QuotedOffer{OfferID: id, GroupNo: g, Tier: tier, DeliverByEarliest: "2026-10-27", DeliverByLatest: "2026-10-28", ExpiresAt: exp}
	}
	must(t, c.QuoteDelivery(cart.DeliveryQuote{QuotedAt: at(3),
		Groups: []cart.QuoteGroup{
			{GroupNo: 1, LineNos: []int{1}, Offers: []cart.QuotedOffer{offer("o1", 1, cart.TierStandard), offer("o2", 1, cart.TierNextDay)}},
			{GroupNo: 2, LineNos: []int{2}, Offers: []cart.QuotedOffer{offer("o3", 2, cart.TierStandard)}},
		},
		Unavailable: []cart.TierUnavailable{{Tier: cart.TierSameDay, Constraint: "PAST_ORDER_DEADLINE", GroupNo: 1}, {Tier: cart.TierTwoDay, Constraint: "DESTINATION_NOT_SERVED"}},
	}, at(3)))
	must(t, c.SelectDelivery(1, "o2", at(4)))
	c.PullEvents()
	return c
}
