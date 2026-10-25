package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func TestCreateCartStoresAnOpenCartAndPublishesNothing(t *testing.T) {
	e := newEnv(t)
	v := e.newCart(t, "shopper-1")
	if v.CartID != "cart-1" || v.State != cart.StateOpen || v.ShopperID != "shopper-1" || v.Version != 1 {
		t.Fatalf("%+v", v)
	}
	if len(e.eventNames(t)) != 0 {
		t.Fatal("CreateCart publishes nothing")
	}
	if _, err := (usecases.CreateCart{Deps: e.deps}).Execute(context.Background(), "bad id"); err == nil {
		t.Fatal("an invalid shopperId must be refused")
	}
}

func TestAddLineNeedsAPurchasableItemAndPublishesCartLineAdded(t *testing.T) {
	e := newEnv(t)
	c := e.newCart(t, "")
	add := usecases.AddLine{Deps: e.deps}
	if _, err := add.Execute(context.Background(), c.CartID, "ghost", 1); !errors.Is(err, cart.ErrItemNotPurchasable) {
		t.Fatalf("unknown item: got %v", err)
	}
	_ = e.store.Items().MarkCatalogued(context.Background(), "unpriced", t0)
	if _, err := add.Execute(context.Background(), c.CartID, "unpriced", 1); !errors.Is(err, cart.ErrItemNotPurchasable) {
		t.Fatalf("unpriced item: got %v", err)
	}
	e.stock(t, "sku-1", 1000)
	v := e.add(t, c.CartID, "sku-1", 2)
	v = e.add(t, v.CartID, "sku-1", 1)
	if len(v.Lines) != 1 || v.Lines[0].Quantity != 3 || v.Version != 3 {
		t.Fatalf("%+v", v)
	}
	if got := e.eventNames(t); !reflect.DeepEqual(got, []string{"CartLineAdded", "CartLineAdded"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestAddLineToAnUnknownCartIsNotFound(t *testing.T) {
	e := newEnv(t)
	if _, err := (usecases.AddLine{Deps: e.deps}).Execute(context.Background(), "nope", "sku-1", 1); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestADiscontinuedItemCanNoLongerBeAdded(t *testing.T) {
	e := newEnv(t)
	e.stock(t, "sku-1", 1000)
	_ = e.store.Items().MarkDiscontinued(context.Background(), "sku-1", t0)
	c := e.newCart(t, "")
	if _, err := (usecases.AddLine{Deps: e.deps}).Execute(context.Background(), c.CartID, "sku-1", 1); !errors.Is(err, cart.ErrItemNotPurchasable) {
		t.Fatalf("got %v", err)
	}
}

func TestLineAndDestinationCommandsPublishNothing(t *testing.T) {
	e := newEnv(t)
	e.stock(t, "sku-1", 1000)
	ctx := context.Background()
	c := e.add(t, e.newCart(t, "").CartID, "sku-1", 2)
	before := len(e.eventNames(t))
	v, err := usecases.SetLineQuantity{Deps: e.deps}.Execute(ctx, c.CartID, "sku-1", 5)
	if err != nil || v.Lines[0].Quantity != 5 {
		t.Fatalf("%+v %v", v, err)
	}
	if v, err = (usecases.SetDestination{Deps: e.deps}).Execute(ctx, c.CartID, "787"); err != nil || v.DestinationPostalPrefix != "787" {
		t.Fatalf("%+v %v", v, err)
	}
	if v, err = (usecases.RemoveLine{Deps: e.deps}).Execute(ctx, c.CartID, "sku-1"); err != nil || len(v.Lines) != 0 {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err = (usecases.RemoveLine{Deps: e.deps}).Execute(ctx, c.CartID, "sku-1"); !errors.Is(err, cart.ErrLineNotFound) {
		t.Fatalf("got %v", err)
	}
	if len(e.eventNames(t)) != before {
		t.Fatal("removal, quantity and destination changes raise no event")
	}
}

func TestAFailedCommandChangesNothing(t *testing.T) {
	e := newEnv(t)
	c := e.newCart(t, "")
	before := e.store.Counts()
	_, err := usecases.SetLineQuantity{Deps: e.deps}.Execute(context.Background(), c.CartID, "nope", 3)
	if !errors.Is(err, cart.ErrLineNotFound) || e.store.Counts() != before {
		t.Fatalf("err %v, counts %+v -> %+v", err, before, e.store.Counts())
	}
}

func TestMergeIntoMovesTheGuestLinesIntoTheShoppersCart(t *testing.T) {
	e := newEnv(t)
	e.stock(t, "sku-1", 1000)
	guest := e.add(t, e.newCart(t, "").CartID, "sku-1", 2)
	mine := e.add(t, e.newCart(t, "shopper-1").CartID, "sku-1", 1)
	v, err := usecases.MergeInto{Deps: e.deps}.Execute(context.Background(), guest.CartID, mine.CartID)
	if err != nil || v.CartID != mine.CartID || v.Lines[0].Quantity != 3 {
		t.Fatalf("%+v %v", v, err)
	}
	g, _ := usecases.GetCart{Deps: e.deps}.Execute(context.Background(), guest.CartID)
	if g.State != cart.StateMerged {
		t.Fatalf("source state = %s", g.State)
	}
	if _, err := (usecases.MergeInto{Deps: e.deps}).Execute(context.Background(), guest.CartID, mine.CartID); !errors.Is(err, cart.ErrCartClosed) {
		t.Fatalf("merging twice: %v", err)
	}
	if _, err := (usecases.MergeInto{Deps: e.deps}).Execute(context.Background(), mine.CartID, "nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown target: %v", err)
	}
}
