package cart

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func guest(t *testing.T, id string) *Cart {
	t.Helper()
	c, err := Create(ID(id), "", t0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMergeIntoMovesLinesMergesQuantitiesAndClosesTheSource(t *testing.T) {
	src := withLine(t, withLine(t, guest(t, "g1"), "item-a", 2), "item-x", 1)
	dst := withLine(t, quoted(t), "item-c", 1)
	if err := src.MergeInto(dst, at(9)); err != nil {
		t.Fatal(err)
	}
	if src.State() != StateMerged || len(src.PullEvents()) != 0 || len(dst.PullEvents()) != 0 {
		t.Fatal("source MERGED, no events")
	}
	got := dst.Lines()
	if len(got) != 4 || got[0] != (Line{"item-a", 3}) || got[3] != (Line{"item-x", 1}) {
		t.Fatalf("target lines = %+v", got)
	}
	if dst.Quote() != nil || len(dst.Selections()) != 0 {
		t.Fatal("the target's lines changed, so its quote is cleared")
	}
	if !dst.LastChangedAt().Equal(at(9)) || !src.LastChangedAt().Equal(at(9)) {
		t.Fatal("both carts are touched")
	}
}

func TestMergeIntoClampsAMergedQuantityAtTheMaximum(t *testing.T) {
	src := withLine(t, guest(t, "g1"), "item-a", 80)
	dst := withLine(t, newCart(t), "item-a", 80)
	if err := src.MergeInto(dst, at(9)); err != nil {
		t.Fatal(err)
	}
	if got := dst.Lines()[0].Quantity; got != MaxQuantity {
		t.Fatalf("quantity = %d, want %d", got, MaxQuantity)
	}
}

func TestMergeIntoRefusesWhenTheTargetCannotHoldTheLines(t *testing.T) {
	src := withLine(t, guest(t, "g1"), "extra", 1)
	dst := newCart(t)
	for i := 0; i < MaxLines; i++ {
		withLine(t, dst, fmt.Sprintf("item-%d", i), 1)
	}
	if err := src.MergeInto(dst, at(9)); !errors.Is(err, ErrTooManyLines) {
		t.Fatalf("got %v, want ErrTooManyLines", err)
	}
	if src.State() != StateOpen || len(dst.Lines()) != MaxLines {
		t.Fatal("a refused merge changes neither cart")
	}
}

func TestMergeIntoRefusesAnInvalidTarget(t *testing.T) {
	src := newCart(t)
	other, _ := Create("c2", "shopper-2", t0)
	closed := newCart(t)
	_ = closed.AbandonInactive(t0.Add(31 * 24 * time.Hour))
	guestTarget := guest(t, "g2")
	for name, dst := range map[string]*Cart{"self": src, "other shopper": other, "closed": closed, "guest": guestTarget} {
		err := src.MergeInto(dst, at(9))
		if !errors.Is(err, ErrMergeTarget) && !errors.Is(err, ErrCartClosed) {
			t.Fatalf("%s: got %v, want ErrMergeTarget or ErrCartClosed", name, err)
		}
	}
	if err := closed.MergeInto(newCart(t), at(9)); !errors.Is(err, ErrCartClosed) {
		t.Fatalf("closed source: got %v, want ErrCartClosed", err)
	}
	mine, _ := Create("c3", "shopper-1", t0)
	if err := mine.MergeInto(newCart(t), at(9)); err != nil {
		t.Fatalf("a shopper's own cart merges into the same shopper's cart: %v", err)
	}
}

func TestAbandonInactiveNeedsThirtyIdleDays(t *testing.T) {
	c := withLine(t, newCart(t), "item-a", 1)
	last := c.LastChangedAt()
	if err := c.AbandonInactive(last.Add(30*24*time.Hour - time.Second)); !errors.Is(err, ErrNotIdle) {
		t.Fatalf("got %v, want ErrNotIdle", err)
	}
	if err := c.AbandonInactive(last.Add(30 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c.State() != StateAbandoned {
		t.Fatalf("state = %s", c.State())
	}
	ev := c.PullEvents()
	e, ok := ev[0].(CartAbandoned)
	if len(ev) != 1 || !ok || !e.LastChangedAt.Equal(last) || e.CartID != "cart-1" {
		t.Fatalf("events = %+v", ev)
	}
	if err := c.AbandonInactive(last.Add(60 * 24 * time.Hour)); !errors.Is(err, ErrCartClosed) {
		t.Fatalf("second sweep: got %v, want ErrCartClosed", err)
	}
}

func TestCloseOnCheckoutCompletedMovesAnOpenCartToCheckedOut(t *testing.T) {
	c := quoted(t)
	if err := c.CloseOnCheckoutCompleted("sess-1", at(20)); err != nil {
		t.Fatal(err)
	}
	if c.State() != StateCheckedOut {
		t.Fatalf("state = %s", c.State())
	}
	ev := c.PullEvents()
	e, ok := ev[0].(CartCheckedOut)
	if len(ev) != 1 || !ok || e.CheckoutSessionID != "sess-1" || !e.OccurredAt().Equal(at(20)) {
		t.Fatalf("events = %+v", ev)
	}
	if err := c.CloseOnCheckoutCompleted("sess-1", at(21)); !errors.Is(err, ErrCartClosed) {
		t.Fatalf("got %v, want ErrCartClosed", err)
	}
	if err := newCart(t).CloseOnCheckoutCompleted("bad id", at(1)); err == nil {
		t.Fatal("an invalid session id must be refused")
	}
}

func TestPurgeEligibility(t *testing.T) {
	c := newCart(t)
	if c.IdleSince(t0.Add(30*24*time.Hour - time.Second)) {
		t.Fatal("not idle yet")
	}
	if !c.IdleSince(t0.Add(30 * 24 * time.Hour)) {
		t.Fatal("idle at exactly 30 days")
	}
}
