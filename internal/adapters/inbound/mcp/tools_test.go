package mcp_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/IQVO/shopping-cart/internal/application/pagination"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// linesOf is the cart document's "lines" array.
func linesOf(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	raw, ok := doc["lines"].([]any)
	if !ok {
		t.Fatalf("cart.lines = %#v, want an array", doc["lines"])
	}
	out := make([]map[string]any, len(raw))
	for i, l := range raw {
		out[i], _ = l.(map[string]any)
	}
	return out
}

func cartsOf(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["carts"].([]any)
	if !ok {
		t.Fatalf("carts = %#v, want an array (never null)", out["carts"])
	}
	carts := make([]map[string]any, len(raw))
	for i, c := range raw {
		carts[i], _ = c.(map[string]any)
	}
	return carts
}

func ids(carts []map[string]any) []string {
	out := make([]string, len(carts))
	for i, c := range carts {
		out[i], _ = c["cartId"].(string)
	}
	return out
}

// at walks a decoded JSON document along keys (and array indexes as decimal
// strings) and returns what is there, or nil.
func at(doc any, path ...string) any {
	for _, key := range path {
		switch node := doc.(type) {
		case map[string]any:
			doc = node[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			doc = node[i]
		default:
			return nil
		}
	}
	return doc
}

// get_cart answers the cart document GET /carts/{cartId} answers: lines,
// destination, quote, selections, the labelled estimate and readiness.
func TestGetCart_AnswersTheRESTDocument(t *testing.T) {
	h := newHarness(t)
	doc := h.ok(t, "get_cart", map[string]any{"cartId": h.world.ready})

	for _, want := range []struct {
		path []string
		val  any
	}{
		{[]string{"cartId"}, h.world.ready},
		{[]string{"state"}, "OPEN"},
		{[]string{"shopperId"}, "shopper-1"},
		{[]string{"destinationPostalPrefix"}, "787"},
		{[]string{"lines", "0", "catalogItemId"}, "sku-1"},
		{[]string{"lines", "0", "quantity"}, float64(2)},
		{[]string{"selections", "0", "offerId"}, "o2"},
		{[]string{"deliveryQuote", "groups", "0", "groupNo"}, float64(1)},
		{[]string{"estimate", "currency"}, "USD"},
		{[]string{"estimate", "itemsMinor"}, float64(2000)},
		{[]string{"estimate", "deliveryMinor"}, float64(500)},
		{[]string{"checkoutReadiness", "ready"}, true},
		{[]string{"checkoutReadiness", "allGroupsSelected"}, true},
		{[]string{"checkoutReadiness", "noOfferExpired"}, true},
	} {
		if got := at(doc, want.path...); got != want.val {
			t.Errorf("get_cart %s = %v, want %v", strings.Join(want.path, "."), got, want.val)
		}
	}
	if n := len(linesOf(t, doc)); n != 1 {
		t.Errorf("lines = %d, want 1", n)
	}
	if _, leaked := doc["lastChangedAt"]; leaked {
		t.Error("lastChangedAt is withheld by REST and must not be exposed over MCP")
	}
}

// A guest cart answers shopperId null and no quote, like REST.
func TestGetCart_GuestCartHasNullShopperAndNoQuote(t *testing.T) {
	h := newHarness(t)
	doc := h.ok(t, "get_cart", map[string]any{"cartId": h.world.guest})
	if v, present := doc["shopperId"]; !present || v != nil {
		t.Errorf("shopperId = %v (present %v), want an explicit null", v, present)
	}
	if _, quoted := doc["deliveryQuote"]; quoted {
		t.Errorf("deliveryQuote present on an unquoted cart: %v", doc["deliveryQuote"])
	}
	if len(linesOf(t, doc)) != 0 {
		t.Errorf("lines = %v, want none", doc["lines"])
	}
}

func TestGetCart_Errors(t *testing.T) {
	h := newHarness(t)
	h.failWith(t, "get_cart", map[string]any{"cartId": "cart-nobody"}, "not-found")
	h.failWith(t, "get_cart", map[string]any{"cartId": strings.Repeat("x", 129)}, "not-found")
	h.failWith(t, "get_cart", map[string]any{"cartId": "cart\x00x"}, "not-found")
	h.failWith(t, "get_cart", map[string]any{"cartId": "  "}, "validation-failed")
}

// list_shopper_carts answers the shopper's carts newest first and nobody
// else's.
func TestListShopperCarts_NewestFirstAndOnlyThatShopper(t *testing.T) {
	h := newHarness(t)
	out := h.ok(t, "list_shopper_carts", map[string]any{"shopperId": "shopper-1"})
	got := ids(cartsOf(t, out))
	want := []string{h.world.open, h.world.ready}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("carts = %v, want %v (newest first, shopper-1 only)", got, want)
	}
	if out["hasMore"] != false {
		t.Errorf("hasMore = %v, want false", out["hasMore"])
	}
}

func TestListShopperCarts_StateFilterAndEmptyList(t *testing.T) {
	h := newHarness(t)
	open := h.ok(t, "list_shopper_carts", map[string]any{"shopperId": "shopper-1", "state": "OPEN"})
	if len(cartsOf(t, open)) != 2 {
		t.Errorf("OPEN carts = %v, want 2", open["carts"])
	}
	closed := h.ok(t, "list_shopper_carts", map[string]any{"shopperId": "shopper-1", "state": "CHECKED_OUT"})
	if got := cartsOf(t, closed); len(got) != 0 {
		t.Errorf("CHECKED_OUT carts = %v, want none (an empty array, not an error)", got)
	}
	none := h.ok(t, "list_shopper_carts", map[string]any{"shopperId": "shopper-nobody"})
	if len(cartsOf(t, none)) != 0 {
		t.Errorf("an unknown shopper has no carts: %v", none)
	}
}

// limit bounds the page; hasMore says an older cart exists.
func TestListShopperCarts_LimitBoundsThePage(t *testing.T) {
	h := newHarness(t)
	out := h.ok(t, "list_shopper_carts", map[string]any{"shopperId": "shopper-1", "limit": 1})
	if got := ids(cartsOf(t, out)); len(got) != 1 || got[0] != h.world.open {
		t.Fatalf("limit 1 carts = %v, want just the newest (%s)", got, h.world.open)
	}
	if out["hasMore"] != true {
		t.Errorf("hasMore = %v, want true: the ready cart is older", out["hasMore"])
	}
}

func TestListShopperCarts_Validation(t *testing.T) {
	h := newHarness(t)
	for name, args := range map[string]map[string]any{
		"no shopper":         {"shopperId": ""},
		"shopper with NUL":   {"shopperId": "shopper\x00"},
		"unknown state":      {"shopperId": "shopper-1", "state": "DONE"},
		"state with NUL":     {"shopperId": "shopper-1", "state": "OPEN\x00"},
		"negative limit":     {"shopperId": "shopper-1", "limit": -1},
		"limit over the cap": {"shopperId": "shopper-1", "limit": 101},
	} {
		t.Run(name, func(t *testing.T) { h.failWith(t, "list_shopper_carts", args, "validation-failed") })
	}
}

// An internal failure never reaches the model: no DSN, no SQL, a fixed text.
func TestInternalErrorsAreOpaque(t *testing.T) {
	h := newHarness(t)
	broken := h.readOnly
	broken.Carts = failingCarts{CartRepository: h.readOnly.Carts}
	session := connectSession(t, readDeps(broken))
	for name, args := range map[string]map[string]any{
		"get_cart":           {"cartId": h.world.ready},
		"list_shopper_carts": {"shopperId": "shopper-1"},
	} {
		res := call(t, session, name, args)
		if !res.IsError {
			t.Fatalf("%s: expected an isError result", name)
		}
		got := text(res)
		if !strings.HasPrefix(got, "internal-error: ") || strings.Contains(got, "postgres://") {
			t.Errorf("%s error = %q, want internal-error with no infrastructure detail", name, got)
		}
	}
}

// failingCarts is a cart repository whose every read fails with a DSN in the
// message, the kind of detail that must never reach a model.
type failingCarts struct{ ports.CartRepository }

var errBoom = errors.New("dial postgres://user:secret@db.internal:5432/carts: connection refused")

func (failingCarts) Get(context.Context, cart.ID) (*cart.Cart, error) { return nil, errBoom }

func (failingCarts) ListByShopper(context.Context, string, cart.State, *pagination.Keyset, int) ([]*cart.Cart, error) {
	return nil, errBoom
}

// Every call of every tool leaves the store exactly as it was: reads only.
func TestReadsNeverChangeTheStore(t *testing.T) {
	h := newHarness(t)
	before := h.store.Counts()
	h.ok(t, "get_cart", map[string]any{"cartId": h.world.ready})
	h.ok(t, "list_shopper_carts", map[string]any{"shopperId": "shopper-1"})
	h.failWith(t, "get_cart", map[string]any{"cartId": "cart-nobody"}, "not-found")
	if after := h.store.Counts(); after != before {
		t.Fatalf("store counts %+v -> %+v: a read changed state", before, after)
	}
}
