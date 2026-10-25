package repocontract

import (
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func ids(cs []*cart.Cart) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c.ID())
	}
	return out
}

func equalIDs(got []*cart.Cart, want ...string) bool {
	g := ids(got)
	if len(g) != len(want) {
		return false
	}
	for i := range g {
		if g[i] != want[i] {
			return false
		}
	}
	return true
}

func listByShopper(t *testing.T, e Env) {
	older := newCart(t, "cart-q1", "shopper-q", at(0))
	newer := newCart(t, "cart-q2", "shopper-q", at(5))
	tie := newCart(t, "cart-q3", "shopper-q", at(5))
	other := newCart(t, "cart-q4", "shopper-other", at(9))
	must(t, tie.CloseOnCheckoutCompleted("sess-1", at(6)))
	for _, c := range []*cart.Cart{older, newer, tie, other} {
		must(t, e.Carts.Save(bg(), c))
	}
	got, err := e.Carts.ListByShopper(bg(), "shopper-q", "")
	must(t, err)
	if !equalIDs(got, "cart-q3", "cart-q2", "cart-q1") {
		t.Fatalf("order = %v", ids(got))
	}
	got, err = e.Carts.ListByShopper(bg(), "shopper-q", cart.StateOpen)
	must(t, err)
	if !equalIDs(got, "cart-q2", "cart-q1") {
		t.Fatalf("open = %v", ids(got))
	}
	got, err = e.Carts.ListByShopper(bg(), "nobody", "")
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("unknown shopper = %v", ids(got))
	}
}

func listIdle(t *testing.T, e Env) {
	for i, m := range []int{30, 10, 20, 50} {
		must(t, e.Carts.Save(bg(), newCart(t, "cart-i"+string(rune('a'+i)), "", at(m))))
	}
	done := newCart(t, "cart-idone", "", at(0))
	must(t, done.CloseOnCheckoutCompleted("sess-1", at(1)))
	must(t, e.Carts.Save(bg(), done))
	got, err := e.Carts.ListIdleOpen(bg(), at(30), 10)
	must(t, err)
	if !equalIDs(got, "cart-ib", "cart-ic", "cart-ia") {
		t.Fatalf("idle = %v", ids(got))
	}
	got, err = e.Carts.ListIdleOpen(bg(), at(30), 2)
	must(t, err)
	if !equalIDs(got, "cart-ib", "cart-ic") {
		t.Fatalf("limited = %v", ids(got))
	}
}

func deleteClosed(t *testing.T, e Env) {
	open := newCart(t, "cart-d-open", "", at(0))
	closedOld := newCart(t, "cart-d-old", "", at(0))
	closedNew := newCart(t, "cart-d-new", "", at(0))
	must(t, closedOld.CloseOnCheckoutCompleted("sess-1", at(10)))
	must(t, closedNew.CloseOnCheckoutCompleted("sess-2", at(100)))
	for _, c := range []*cart.Cart{open, closedOld, closedNew} {
		must(t, e.Carts.Save(bg(), c))
	}
	n, err := e.Carts.DeleteClosedBefore(bg(), at(10).Add(-time.Second), 10)
	must(t, err)
	if n != 0 {
		t.Fatalf("before the cutoff: deleted %d", n)
	}
	n, err = e.Carts.DeleteClosedBefore(bg(), at(10), 10)
	must(t, err)
	if n != 1 {
		t.Fatalf("deleted %d, want 1", n)
	}
	for id, want := range map[string]bool{"cart-d-open": true, "cart-d-old": false, "cart-d-new": true} {
		_, err := e.Carts.Get(bg(), cart.ID(id))
		if (err == nil) != want {
			t.Fatalf("%s present = %v, want %v", id, err == nil, want)
		}
	}
	n, _ = e.Carts.DeleteClosedBefore(bg(), at(1000), 0)
	if n != 0 {
		t.Fatalf("a zero limit deletes nothing, deleted %d", n)
	}
}
