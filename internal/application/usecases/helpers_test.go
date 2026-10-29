package usecases_test

import (
	"context"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

type env struct {
	store  *memory.Store
	clock  *memory.Clock
	quoter *memory.Quoter
	deps   usecases.Deps
}

func newEnv(t *testing.T) *env {
	t.Helper()
	clock := memory.NewClock(t0)
	store := memory.NewStore(clock)
	quoter := &memory.Quoter{}
	return &env{store: store, clock: clock, quoter: quoter, deps: usecases.Deps{
		Carts: store.Carts(), Items: store.Items(), Schedules: store.Schedules(), TaxTables: store.TaxTables(),
		Quoter: quoter, Inbox: store.Inbox(), Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: clock,
		IDs: &memory.SequentialIDs{}, Currency: "USD",
	}}
}

func (e *env) meta(id, typ string) usecases.Meta {
	return usecases.Meta{EventID: id, Type: typ, At: e.clock.Now()}
}

func (e *env) consumer() usecases.Consumer { return usecases.Consumer{Deps: e.deps} }

// stock makes an item purchasable: catalogued and priced.
func (e *env) stock(t *testing.T, id string, price int64) {
	t.Helper()
	ctx := context.Background()
	if err := e.store.Items().MarkCatalogued(ctx, id, t0); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Items().ApplyPrice(ctx, id, "USD", price, 1, t0); err != nil {
		t.Fatal(err)
	}
}

func (e *env) newCart(t *testing.T, shopper string) views.Cart {
	t.Helper()
	v, err := usecases.CreateCart{Deps: e.deps}.Execute(context.Background(), shopper)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) add(t *testing.T, cartID, item string, qty int) views.Cart {
	t.Helper()
	v, err := usecases.AddLine{Deps: e.deps}.Execute(context.Background(), cartID, item, qty)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (e *env) eventNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, ev := range e.store.Outbox().Events() {
		out = append(out, ev.EventName())
	}
	return out
}

func offer(id string, group int, tier cart.Tier, expires time.Time) cart.QuotedOffer {
	return cart.QuotedOffer{OfferID: id, GroupNo: group, Tier: tier, DeliverByEarliest: "2026-10-27", DeliverByLatest: "2026-10-28", ExpiresAt: expires}
}

func oneGroupQuote(expires time.Time) cart.DeliveryQuote {
	return cart.DeliveryQuote{QuotedAt: t0, Groups: []cart.QuoteGroup{{GroupNo: 1, LineNos: []int{1}, Offers: []cart.QuotedOffer{
		offer("o1", 1, cart.TierStandard, expires), offer("o2", 1, cart.TierNextDay, expires)}}}}
}

var _ shared.Event = cart.CartLineAdded{}
