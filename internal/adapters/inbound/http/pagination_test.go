package http_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/inbound/http/page"
)

// walkCarts follows nextCursor from the first page of query until it is
// absent and returns every cartId in the order served, and the page count.
func walkCarts(e *env, query string, limit int) (ids []string, pages int) {
	e.t.Helper()
	cursor := ""
	for {
		target := "/carts?" + query + "&limit=" + strconv.Itoa(limit)
		if cursor != "" {
			target += "&cursor=" + url.QueryEscape(cursor)
		}
		body := e.get(target)
		e.wantStatus(body, http.StatusOK)
		doc := body.json(e.t)
		if _, old := doc["carts"]; old {
			e.t.Fatalf("the per-resource key `carts` is retired: %s", body.Body)
		}
		items, ok := doc["items"].([]any)
		if !ok || len(items) > limit {
			e.t.Fatalf("items = %v (limit %d)", doc["items"], limit)
		}
		for _, it := range items {
			ids = append(ids, it.(map[string]any)["cartId"].(string))
		}
		pages++
		next, more := doc["nextCursor"].(string)
		if !more {
			return ids, pages
		}
		if pages > 100 {
			e.t.Fatal("the walk never ends")
		}
		cursor = next
	}
}

// More carts than the page size, many created at the same instant (the test
// clock is frozen between the Advance calls): every page is walked and no cart
// is repeated or skipped.
func TestListCartsWalksEveryPageWithTiedCreatedAt(t *testing.T) {
	e := newEnv(t)
	want := map[string]bool{}
	for i := 0; i < 7; i++ {
		if i == 3 || i == 5 {
			e.clock.Advance(time.Second)
		}
		want[e.newCart("shopper-1")] = true
	}
	e.newCart("shopper-2")
	for _, limit := range []int{1, 2, 3, 7, 8, 500} {
		ids, pages := walkCarts(e, "shopperId=shopper-1&state=OPEN", limit)
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] || !want[id] {
				t.Fatalf("limit %d: %s repeated or foreign in %v", limit, id, ids)
			}
			seen[id] = true
		}
		if len(seen) != len(want) {
			t.Fatalf("limit %d: walked %d of %d carts: %v", limit, len(seen), len(want), ids)
		}
		if wantPages := (len(want) + limit - 1) / limit; pages != wantPages {
			t.Fatalf("limit %d: %d pages, want %d", limit, pages, wantPages)
		}
	}
}

func TestListCartsLastPageHasNoCursorAndEmptyIsAnEmptyPage(t *testing.T) {
	e := newEnv(t)
	e.newCart("shopper-1")
	one := e.get("/carts?shopperId=shopper-1&limit=1").json(t)
	if _, has := one["nextCursor"]; has || len(one["items"].([]any)) != 1 {
		t.Fatalf("exactly limit carts is the last page: %v", one)
	}
	none := e.get("/carts?shopperId=nobody").json(t)
	if items, ok := none["items"].([]any); !ok || len(items) != 0 || none["nextCursor"] != nil {
		t.Fatalf("no carts is {items: []}: %v", none)
	}
}

func TestListCartsRejectsBadLimitAndCursor(t *testing.T) {
	e := newEnv(t)
	e.newCart("shopper-1")
	for _, limit := range []string{"0", "501", "-1", "abc", "1.5"} {
		e.wantProblem(e.get("/carts?shopperId=shopper-1&limit="+limit), http.StatusBadRequest, "invalid-limit")
	}
	notATime := page.Encode(page.Position{Sort: "yesterday", ID: "cart-1"})
	for _, cursor := range []string{"%25%25", "bm90LWpzb24", "e30", url.QueryEscape(notATime)} {
		e.wantProblem(e.get("/carts?shopperId=shopper-1&cursor="+cursor), http.StatusBadRequest, "invalid-cursor")
	}
}
