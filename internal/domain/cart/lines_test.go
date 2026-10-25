package cart

import (
	"errors"
	"fmt"
	"testing"
)

func TestAddLineMergesQuantitiesOfAnExistingItem(t *testing.T) {
	c := withLine(t, newCart(t), "item-a", 2)
	if err := c.AddLine("item-a", 3, true, at(2)); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	got := c.Lines()
	if len(got) != 1 || got[0].Quantity != 5 {
		t.Fatalf("lines = %+v, want one line of 5", got)
	}
	ev := c.PullEvents()
	added, ok := ev[0].(CartLineAdded)
	if len(ev) != 1 || !ok || added.Quantity != 5 || added.CatalogItemID != "item-a" || added.CartID != "cart-1" {
		t.Fatalf("events = %+v, want one CartLineAdded with the merged quantity 5", ev)
	}
	if added.AggregateVersion() != c.Version() || !added.OccurredAt().Equal(at(2)) {
		t.Fatalf("event header = %+v, cart version %d", added.Header, c.Version())
	}
}

func TestAddLineRejectsQuantityBounds(t *testing.T) {
	for _, q := range []int{0, -1, 100} {
		c := newCart(t)
		if err := c.AddLine("item-a", q, true, at(1)); !errors.Is(err, ErrQuantityOutOfRange) {
			t.Fatalf("quantity %d: got %v, want ErrQuantityOutOfRange", q, err)
		}
	}
	c := withLine(t, newCart(t), "item-a", 60)
	if err := c.AddLine("item-a", 40, true, at(2)); !errors.Is(err, ErrQuantityOutOfRange) {
		t.Fatalf("merged 100: got %v, want ErrQuantityOutOfRange", err)
	}
	if err := c.AddLine("item-a", 39, true, at(2)); err != nil {
		t.Fatalf("merged 99 must be accepted: %v", err)
	}
	if err := newCart(t).AddLine("item-a", 99, true, at(1)); err != nil {
		t.Fatalf("quantity 99 must be accepted: %v", err)
	}
	if err := newCart(t).AddLine("item-a", 1, true, at(1)); err != nil {
		t.Fatalf("quantity 1 must be accepted: %v", err)
	}
}

func TestAddLineRefusesAnItemThatIsNotPurchasable(t *testing.T) {
	c := newCart(t)
	if err := c.AddLine("item-a", 1, false, at(1)); !errors.Is(err, ErrItemNotPurchasable) {
		t.Fatalf("got %v, want ErrItemNotPurchasable", err)
	}
	if len(c.Lines()) != 0 || c.Version() != 1 || len(c.PullEvents()) != 0 {
		t.Fatal("a refused command must change nothing")
	}
}

func TestAddLineCapsTheCartAtOneHundredLines(t *testing.T) {
	c := newCart(t)
	for i := 0; i < MaxLines; i++ {
		withLine(t, c, fmt.Sprintf("item-%d", i), 1)
	}
	if err := c.AddLine("one-more", 1, true, at(2)); !errors.Is(err, ErrTooManyLines) {
		t.Fatalf("got %v, want ErrTooManyLines", err)
	}
	if err := c.AddLine("item-0", 1, true, at(2)); err != nil {
		t.Fatalf("merging into a full cart must be accepted: %v", err)
	}
}

func TestAddLineRejectsAnInvalidItemID(t *testing.T) {
	if err := newCart(t).AddLine("bad id!", 1, true, at(1)); err == nil {
		t.Fatal("an invalid catalogItemId must be refused")
	}
}

func TestLineChangesClearTheQuoteAndSelections(t *testing.T) {
	c := quoted(t)
	if err := c.SelectDelivery(1, "o1", at(4)); err != nil {
		t.Fatal(err)
	}
	if err := c.AddLine("item-c", 1, true, at(5)); err != nil {
		t.Fatal(err)
	}
	if c.Quote() != nil || len(c.Selections()) != 0 {
		t.Fatal("AddLine must clear the quote and the selections")
	}
}

func TestSetLineQuantityChangesTheLineAndRaisesNoEvent(t *testing.T) {
	c := quoted(t)
	if err := c.SetLineQuantity("item-b", 7, at(5)); err != nil {
		t.Fatal(err)
	}
	if got := c.Lines()[1].Quantity; got != 7 {
		t.Fatalf("quantity = %d, want 7", got)
	}
	if c.Quote() != nil || len(c.PullEvents()) != 0 {
		t.Fatal("clears the quote, raises no event")
	}
	if err := c.SetLineQuantity("item-b", 0, at(6)); !errors.Is(err, ErrQuantityOutOfRange) {
		t.Fatalf("got %v, want ErrQuantityOutOfRange", err)
	}
	if err := c.SetLineQuantity("item-b", 100, at(6)); !errors.Is(err, ErrQuantityOutOfRange) {
		t.Fatalf("got %v, want ErrQuantityOutOfRange", err)
	}
	if err := c.SetLineQuantity("nope", 2, at(6)); !errors.Is(err, ErrLineNotFound) {
		t.Fatalf("got %v, want ErrLineNotFound", err)
	}
}

func TestRemoveLineDropsTheLineKeepsOrderAndRaisesNoEvent(t *testing.T) {
	c := withLine(t, quoted(t), "item-c", 1)
	if err := c.RemoveLine("item-b", at(5)); err != nil {
		t.Fatal(err)
	}
	got := c.Lines()
	if len(got) != 2 || got[0].CatalogItemID != "item-a" || got[1].CatalogItemID != "item-c" {
		t.Fatalf("lines = %+v", got)
	}
	if c.Quote() != nil || len(c.PullEvents()) != 0 {
		t.Fatal("clears the quote, raises no event")
	}
	if err := c.RemoveLine("item-b", at(6)); !errors.Is(err, ErrLineNotFound) {
		t.Fatalf("got %v, want ErrLineNotFound", err)
	}
}

func TestLineCommandsRefuseAClosedCart(t *testing.T) {
	c := withLine(t, newCart(t), "item-a", 1)
	if err := c.AbandonInactive(at(60 * 24 * 31)); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"add":    c.AddLine("item-a", 1, true, at(60*24*32)),
		"set":    c.SetLineQuantity("item-a", 2, at(60*24*32)),
		"remove": c.RemoveLine("item-a", at(60*24*32)),
	} {
		if !errors.Is(err, ErrCartClosed) {
			t.Fatalf("%s: got %v, want ErrCartClosed", name, err)
		}
	}
}
