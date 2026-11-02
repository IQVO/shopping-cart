package http_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	httpadapter "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
)

// TestRetiredRoutesAreGone: the pre-guideline routes (`:custom-method` suffixes
// and the verb sub-path) are removed, not aliased; each answers 404 or 405.
func TestRetiredRoutesAreGone(t *testing.T) {
	e := newEnv(t)
	id := e.newCart("shopper-1")
	cases := []struct{ name, method, path, body string }{
		{"set-quantity", http.MethodPost, "/carts/" + id + "/lines/sku-1:set-quantity", `{"quantity":5}`},
		{"remove", http.MethodPost, "/carts/" + id + "/lines/sku-1:remove", ``},
		{"set-destination", http.MethodPost, "/carts/" + id + ":set-destination", `{"destinationPostalPrefix":"787"}`},
		{"quote-delivery", http.MethodPost, "/carts/" + id + ":quote-delivery", ``},
		{"select-delivery", http.MethodPost, "/carts/" + id + ":select-delivery", `{"groupNo":1,"offerId":"o1"}`},
		{"merge-into", http.MethodPost, "/carts/" + id + ":merge-into", `{"targetCartId":"other"}`},
		{"delivery/requoted", http.MethodPost, "/carts/" + id + "/delivery/requoted", `{"offers":[]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.do(c.method, c.path, "retired-"+c.name, c.body)
			if r.Status != http.StatusNotFound && r.Status != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s answered %d, want 404 or 405: %s", c.method, c.path, r.Status, r.Body)
			}
		})
	}
}

// TestNoRouteIsACustomMethod walks the router: no registered pattern carries a
// `:custom-method` suffix or one of the retired verb segments.
func TestNoRouteIsACustomMethod(t *testing.T) {
	routes, ok := httpadapter.NewRouter(&httpadapter.Server{}, slog.New(slog.DiscardHandler), "").(chi.Routes)
	if !ok {
		t.Fatal("the router is not a chi.Routes")
	}
	n := 0
	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		n++
		if strings.Contains(route, ":") || strings.HasSuffix(route, "/requoted") {
			t.Errorf("%s %s is a retired route shape", method, route)
		}
		return nil
	})
	if err != nil || n == 0 {
		t.Fatalf("walked %d routes: %v", n, err)
	}
}

// PUT and DELETE are idempotent by method: a key sent to them is accepted and
// ignored, so a second request with the same key runs again (no replay, no 409).
func TestPutAndDeleteAcceptAnIdempotencyKeyAndIgnoreIt(t *testing.T) {
	e := newEnv(t)
	e.stock("sku-1", 1000)
	id := e.newCart("")
	e.wantStatus(e.post("/carts/"+id+"/lines", `{"catalogItemId":"sku-1","quantity":2}`), http.StatusOK)

	first := e.do(http.MethodPut, "/carts/"+id+"/destination", "put-key", `{"destinationPostalPrefix":"787"}`)
	e.wantStatus(first, http.StatusOK)
	second := e.do(http.MethodPut, "/carts/"+id+"/destination", "put-key", `{"destinationPostalPrefix":"900"}`)
	e.wantStatus(second, http.StatusOK)
	if second.Header.Get("Idempotency-Replayed") != "" || second.json(t)["destinationPostalPrefix"] != "900" {
		t.Fatalf("a PUT must run again whatever its key: %v %s", second.Header, second.Body)
	}

	e.wantStatus(e.do(http.MethodDelete, "/carts/"+id+"/lines/sku-1", "del-key", ""), http.StatusOK)
	again := e.do(http.MethodDelete, "/carts/"+id+"/lines/sku-1", "del-key", "")
	e.wantProblem(again, http.StatusNotFound, "not-found")
	if again.Header.Get("Idempotency-Replayed") != "" {
		t.Fatalf("a DELETE must not be replayed: %v", again.Header)
	}
}
