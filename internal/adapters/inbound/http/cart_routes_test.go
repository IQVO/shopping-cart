package http_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func (e *env) stock(id string, price int64) {
	e.t.Helper()
	ctx := context.Background()
	if err := e.store.Items().MarkCatalogued(ctx, id, t0); err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.Items().ApplyPrice(ctx, id, "USD", price, 1, t0); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) newCart(shopper string) string {
	e.t.Helper()
	body := "{}"
	if shopper != "" {
		body = `{"shopperId":"` + shopper + `"}`
	}
	r := e.post("/carts", body)
	e.wantStatus(r, http.StatusCreated)
	return r.json(e.t)["cartId"].(string)
}

func TestCreateCartAcceptsAnEmptyBodyAndAShopper(t *testing.T) {
	e := newEnv(t)
	r := e.do(http.MethodPost, "/carts", "k-empty", "")
	e.wantStatus(r, http.StatusCreated)
	m := r.json(t)
	if m["state"] != "OPEN" || m["shopperId"] != nil || m["version"].(float64) != 1 {
		t.Fatalf("%v", m)
	}
	est := m["estimate"].(map[string]any)
	if est["estimateComplete"] != false || est["totalMinor"].(float64) != 0 {
		t.Fatalf("estimate = %v", est)
	}
	e.wantProblem(e.post("/carts", `{"unknown":1}`), http.StatusBadRequest, "malformed-request")
	e.wantProblem(e.post("/carts", `{"shopperId":"bad id"}`), http.StatusUnprocessableEntity, "validation-failed")
}

func TestAddLineMergesAndMapsItsErrors(t *testing.T) {
	e := newEnv(t)
	e.stock("sku-1", 1000)
	id := e.newCart("")
	line := `{"catalogItemId":"sku-1","quantity":2}`
	e.wantStatus(e.post("/carts/"+id+"/lines", line), http.StatusOK)
	m := e.post("/carts/"+id+"/lines", line).json(t)
	lines := m["lines"].([]any)
	if len(lines) != 1 || lines[0].(map[string]any)["quantity"].(float64) != 4 {
		t.Fatalf("lines = %v", lines)
	}
	if m["estimate"].(map[string]any)["itemsMinor"].(float64) != 4000 {
		t.Fatalf("estimate = %v", m["estimate"])
	}
	e.wantProblem(e.post("/carts/"+id+"/lines", `{"catalogItemId":"ghost","quantity":1}`), http.StatusUnprocessableEntity, "item-not-purchasable")
	e.wantProblem(e.post("/carts/"+id+"/lines", `{"catalogItemId":"sku-1","quantity":100}`), http.StatusUnprocessableEntity, "quantity-out-of-range")
	e.wantProblem(e.post("/carts/nope/lines", line), http.StatusNotFound, "not-found")
	e.wantProblem(e.post("/carts/"+id+"/lines", `{"catalogItemId":`), http.StatusBadRequest, "malformed-request")
}

func TestQuantityRemoveAndDestinationRoutes(t *testing.T) {
	e := newEnv(t)
	e.stock("sku-1", 1000)
	id := e.newCart("")
	e.wantStatus(e.post("/carts/"+id+"/lines", `{"catalogItemId":"sku-1","quantity":2}`), http.StatusOK)
	r := e.post("/carts/"+id+"/lines/sku-1:set-quantity", `{"quantity":5}`)
	e.wantStatus(r, http.StatusOK)
	e.wantProblem(e.post("/carts/"+id+"/lines/sku-1:set-quantity", `{"quantity":0}`), http.StatusUnprocessableEntity, "quantity-out-of-range")
	e.wantProblem(e.post("/carts/"+id+"/lines/zzz:set-quantity", `{"quantity":1}`), http.StatusNotFound, "not-found")
	m := e.post("/carts/"+id+":set-destination", `{"destinationPostalPrefix":"787"}`).json(t)
	if m["destinationPostalPrefix"] != "787" {
		t.Fatalf("%v", m)
	}
	e.wantProblem(e.post("/carts/"+id+":set-destination", `{"destinationPostalPrefix":"7 8"}`), http.StatusUnprocessableEntity, "validation-failed")
	e.wantStatus(e.post("/carts/"+id+"/lines/sku-1:remove", ""), http.StatusOK)
	e.wantProblem(e.post("/carts/"+id+"/lines/sku-1:remove", ""), http.StatusNotFound, "not-found")
}

func TestGetAndListCarts(t *testing.T) {
	e := newEnv(t)
	id := e.newCart("shopper-1")
	e.wantStatus(e.get("/carts/"+id), http.StatusOK)
	e.wantProblem(e.get("/carts/nope"), http.StatusNotFound, "not-found")
	got := e.get("/carts?shopperId=shopper-1&state=OPEN").json(t)["carts"].([]any)
	if len(got) != 1 {
		t.Fatalf("%v", got)
	}
	e.wantProblem(e.get("/carts"), http.StatusUnprocessableEntity, "validation-failed")
	e.wantProblem(e.get("/carts?shopperId=shopper-1&state=GONE"), http.StatusUnprocessableEntity, "validation-failed")
}

func TestMergeRoute(t *testing.T) {
	e := newEnv(t)
	e.stock("sku-1", 1000)
	guest := e.newCart("")
	mine := e.newCart("shopper-1")
	e.wantStatus(e.post("/carts/"+guest+"/lines", `{"catalogItemId":"sku-1","quantity":1}`), http.StatusOK)
	m := e.post("/carts/"+guest+":merge-into", `{"targetCartId":"`+mine+`"}`).json(t)
	if m["cartId"] != mine || len(m["lines"].([]any)) != 1 {
		t.Fatalf("%v", m)
	}
	e.wantProblem(e.post("/carts/"+guest+":merge-into", `{"targetCartId":"`+mine+`"}`), http.StatusConflict, "cart-closed")
	e.wantProblem(e.post("/carts/"+mine+":merge-into", `{"targetCartId":"`+mine+`"}`), http.StatusUnprocessableEntity, "merge-target")
}

func readyQuote(e *env) string {
	e.stock("sku-1", 1000)
	id := e.newCart("")
	e.post("/carts/"+id+"/lines", `{"catalogItemId":"sku-1","quantity":2}`)
	e.post("/carts/"+id+":set-destination", `{"destinationPostalPrefix":"787"}`)
	return id
}

func TestQuoteSelectAndRequoteRoutes(t *testing.T) {
	e := newEnv(t)
	id := readyQuote(e)
	e.quoter.Respond(oneGroupQuote(t0.Add(time.Hour)), nil)
	r := e.do(http.MethodPost, "/carts/"+id+":quote-delivery", "key-quote", "")
	e.wantStatus(r, http.StatusOK)
	if calls := e.quoter.Calls(); len(calls) != 1 || calls[0].IdempotencyKey != "key-quote" {
		t.Fatalf("the Idempotency-Key must reach delivery-promise: %+v", calls)
	}
	if len(r.json(t)["deliveryQuote"].(map[string]any)["groups"].([]any)) != 1 {
		t.Fatalf("%s", r.Body)
	}
	e.wantProblem(e.post("/carts/"+id+":select-delivery", `{"groupNo":1,"offerId":"zzz"}`), http.StatusConflict, "stale-delivery-selection")
	m := e.post("/carts/"+id+":select-delivery", `{"groupNo":1,"offerId":"o1"}`).json(t)
	if m["checkoutReadiness"].(map[string]any)["ready"] != true {
		t.Fatalf("%v", m)
	}
	body := `{"offers":[{"offerId":"r1","groupNo":1,"tier":"NEXT_DAY","deliverByEarliest":"2026-10-27","deliverByLatest":"2026-10-28","expiresAt":"2026-10-25T12:00:00Z"}]}`
	m = e.post("/carts/"+id+"/delivery/requoted", body).json(t)
	if m["selections"].([]any)[0].(map[string]any)["offerId"] != "r1" || len(e.quoter.Calls()) != 1 {
		t.Fatalf("%v", m)
	}
	e.wantProblem(e.post("/carts/"+id+"/delivery/requoted", `{"offers":[]}`), http.StatusUnprocessableEntity, "validation-failed")
}

func TestQuoteNeedsADestinationAndMapsDeliveryPromiseOutages(t *testing.T) {
	e := newEnv(t)
	e.stock("sku-1", 1000)
	id := e.newCart("")
	e.post("/carts/"+id+"/lines", `{"catalogItemId":"sku-1","quantity":1}`)
	e.wantProblem(e.post("/carts/"+id+":quote-delivery", ""), http.StatusUnprocessableEntity, "no-destination")
	e.post("/carts/"+id+":set-destination", `{"destinationPostalPrefix":"787"}`)
	e.quoter.Respond(emptyQuote(), errDown)
	e.wantProblem(e.post("/carts/"+id+":quote-delivery", ""), http.StatusServiceUnavailable, "delivery-promise-unavailable")
}
