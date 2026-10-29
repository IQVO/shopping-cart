package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func readyCart(t *testing.T, e *env) string {
	t.Helper()
	e.stock(t, "sku-1", 1000)
	c := e.add(t, e.newCart(t, "").CartID, "sku-1", 2)
	if _, err := (usecases.SetDestination{Deps: e.deps}).Execute(context.Background(), c.CartID, "787"); err != nil {
		t.Fatal(err)
	}
	return c.CartID
}

func TestQuoteDeliveryCallsDeliveryPromiseOnceAndPublishesCartDeliveryQuoted(t *testing.T) {
	e := newEnv(t)
	id := readyCart(t, e)
	e.quoter.Respond(oneGroupQuote(t0.Add(time.Hour)), nil)
	v, err := usecases.QuoteDelivery{Deps: e.deps}.Execute(context.Background(), id, "key-1")
	if err != nil || v.DeliveryQuote == nil || len(v.DeliveryQuote.Groups) != 1 {
		t.Fatalf("%+v %v", v, err)
	}
	calls := e.quoter.Calls()
	if len(calls) != 1 || calls[0].IdempotencyKey != "key-1" || calls[0].Destination != "787" || calls[0].Lines[0].Quantity != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if names := e.eventNames(t); names[len(names)-1] != "CartDeliveryQuoted" {
		t.Fatalf("events = %v", names)
	}
}

func TestQuoteDeliveryNeedsADestinationAndNeverCallsDeliveryPromiseWithout(t *testing.T) {
	e := newEnv(t)
	e.stock(t, "sku-1", 1000)
	c := e.add(t, e.newCart(t, "").CartID, "sku-1", 1)
	if _, err := (usecases.QuoteDelivery{Deps: e.deps}).Execute(context.Background(), c.CartID, "k"); !errors.Is(err, cart.ErrNoDestination) {
		t.Fatalf("got %v", err)
	}
	if len(e.quoter.Calls()) != 0 {
		t.Fatal("no call without a destination")
	}
}

func TestQuoteDeliveryKeepsTheCartWhenDeliveryPromiseIsDown(t *testing.T) {
	e := newEnv(t)
	id := readyCart(t, e)
	e.quoter.Respond(cart.DeliveryQuote{}, ports.ErrDeliveryPromiseUnavailable)
	before := e.store.Counts()
	_, err := usecases.QuoteDelivery{Deps: e.deps}.Execute(context.Background(), id, "k")
	if !errors.Is(err, ports.ErrDeliveryPromiseUnavailable) || e.store.Counts() != before {
		t.Fatalf("err %v, counts changed %v", err, e.store.Counts() != before)
	}
}

func TestQuoteDeliveryRefusesAClosedCartAndAnUnknownCart(t *testing.T) {
	e := newEnv(t)
	if _, err := (usecases.QuoteDelivery{Deps: e.deps}).Execute(context.Background(), "nope", "k"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	id := readyCart(t, e)
	e.clock.Advance(31 * 24 * time.Hour)
	if _, err := (usecases.AbandonInactive{Deps: e.deps}).Execute(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := (usecases.QuoteDelivery{Deps: e.deps}).Execute(context.Background(), id, "k"); !errors.Is(err, cart.ErrCartClosed) {
		t.Fatalf("got %v", err)
	}
}

func TestSelectDeliveryMakesTheCartCheckoutReadyUntilTheOfferExpires(t *testing.T) {
	e := newEnv(t)
	id := readyCart(t, e)
	e.quoter.Respond(oneGroupQuote(t0.Add(time.Hour)), nil)
	if _, err := (usecases.QuoteDelivery{Deps: e.deps}).Execute(context.Background(), id, "k"); err != nil {
		t.Fatal(err)
	}
	sel := usecases.SelectDelivery{Deps: e.deps}
	if _, err := sel.Execute(context.Background(), id, 1, "zzz"); !errors.Is(err, cart.ErrStaleDeliverySelection) {
		t.Fatalf("got %v", err)
	}
	v, err := sel.Execute(context.Background(), id, 1, "o2")
	if err != nil || !v.Readiness.Ready || len(v.Selections) != 1 {
		t.Fatalf("%+v %v", v, err)
	}
	e.clock.Advance(2 * time.Hour)
	g, _ := usecases.GetCart{Deps: e.deps}.Execute(context.Background(), id)
	if g.Readiness.Ready || g.Readiness.NoOfferExpired {
		t.Fatalf("expired offer: %+v", g.Readiness)
	}
}

func TestAcceptRequotedOffersNeverCallsDeliveryPromise(t *testing.T) {
	e := newEnv(t)
	id := readyCart(t, e)
	v, err := usecases.AcceptRequotedOffers{Deps: e.deps}.Execute(context.Background(), id,
		[]cart.QuotedOffer{offer("r1", 1, cart.TierNextDay, t0.Add(time.Hour))})
	if err != nil || !v.Readiness.Ready || v.Selections[0].OfferID != "r1" {
		t.Fatalf("%+v %v", v, err)
	}
	if len(e.quoter.Calls()) != 0 {
		t.Fatal("AcceptRequotedOffers must not call delivery-promise")
	}
	if _, err := (usecases.AcceptRequotedOffers{Deps: e.deps}).Execute(context.Background(), id, nil); err == nil {
		t.Fatal("an empty set must be refused")
	}
}
