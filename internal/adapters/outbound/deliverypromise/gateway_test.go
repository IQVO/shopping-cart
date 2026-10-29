package deliverypromise_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/deliverypromise"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

var fixedNow = time.Date(2026, 10, 25, 13, 1, 0, 0, time.UTC)

type seen struct {
	method, path, key, contentType string
	body                           map[string]any
}

// serve answers every request with status and body and records the last one.
func serve(t *testing.T, status int, contentType, body string) (*deliverypromise.Gateway, *seen) {
	t.Helper()
	got := &seen{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got.method, got.path, got.key, got.contentType = r.Method, r.URL.EscapedPath(), r.Header.Get("Idempotency-Key"), r.Header.Get("Content-Type")
		got.body = nil
		_ = json.Unmarshal(raw, &got.body)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return deliverypromise.New(srv.URL+"/", nil, func() time.Time { return fixedNow }), got
}

var cartLines = []cart.Line{{CatalogItemID: "sku-1", Quantity: 2}, {CatalogItemID: "sku-2", Quantity: 1}}

func example(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/quote-response.example.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGatewaySendsTheQuoteRequestOfTheSpec(t *testing.T) {
	g, got := serve(t, 200, "application/json", `{"offers":[],"unavailable":[]}`)
	if _, err := g.Quote(context.Background(), "key-7", "787", cartLines); err != nil {
		t.Fatal(err)
	}
	if got.method != "POST" || got.path != "/delivery-offers:quote" || got.key != "key-7" || got.contentType != "application/json" {
		t.Fatalf("request = %+v", got)
	}
	if got.body["destinationPostalPrefix"] != "787" || len(got.body) != 2 {
		t.Fatalf("body = %v", got.body)
	}
	line := got.body["lines"].([]any)[1].(map[string]any)
	if line["catalogItemId"] != "sku-2" || line["quantity"] != float64(1) {
		t.Fatalf("lines = %v", got.body["lines"])
	}
}

func decodeExample(t *testing.T) cart.DeliveryQuote {
	t.Helper()
	g, _ := serve(t, 200, "application/json", example(t))
	q, err := g.Quote(context.Background(), "k", "787", cartLines)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestGatewayDecodesTheGroupsOfTheSpecExample(t *testing.T) {
	q := decodeExample(t)
	if len(q.Groups) != 2 || !q.QuotedAt.Equal(time.Date(2026, 10, 25, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("quote = %+v", q)
	}
	g1, g2 := q.Groups[0], q.Groups[1]
	if g1.GroupNo != 1 || len(g1.Offers) != 2 || len(g1.LineNos) != 1 || g1.LineNos[0] != 1 || g2.LineNos[0] != 2 {
		t.Fatalf("groups = %+v", q.Groups)
	}
}

func TestGatewayDecodesTheOffersAndConstraintsOfTheSpecExample(t *testing.T) {
	q := decodeExample(t)
	o := q.Groups[0].Offers[0]
	if o.OfferID != "of-1" || o.Tier != cart.TierNextDay || o.DeliverByEarliest != "2026-10-26" || o.DeliverByLatest != "2026-10-26" ||
		!o.ExpiresAt.Equal(time.Date(2026, 10, 25, 13, 30, 0, 0, time.UTC)) {
		t.Fatalf("offer = %+v", o)
	}
	if len(q.Unavailable) != 2 || q.Unavailable[0] != (cart.TierUnavailable{Tier: cart.TierSameDay, Constraint: "PAST_ORDER_DEADLINE", GroupNo: 1}) {
		t.Fatalf("unavailable = %+v", q.Unavailable)
	}
}

func TestGatewayKeepsOnlyQuotedOffersAndStampsAQuoteWithoutQuotedAt(t *testing.T) {
	body := `{"offers":[
	 {"offerId":"a","state":"EXPIRED","tier":"STANDARD","groupNo":1,"lines":[{"catalogItemId":"sku-1","quantity":2}],"window":{"earliest":"2026-10-28T00:00:00Z","latest":"2026-10-28T00:00:00Z"},"expiresAt":"2026-10-25T13:30:00Z"},
	 {"offerId":"b","state":"QUOTED","tier":"STANDARD","groupNo":1,"lines":[{"catalogItemId":"sku-1","quantity":2}],"window":{"earliest":"2026-10-28T00:00:00Z","latest":"2026-10-28T00:00:00Z"},"expiresAt":"2026-10-25T13:30:00Z"}],
	 "unavailable":[{"tier":"NEXT_DAY","constraint":"DESTINATION_NOT_SERVED"}]}`
	g, _ := serve(t, 200, "application/json", body)
	q, err := g.Quote(context.Background(), "k", "787", cartLines[:1])
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Groups) != 1 || len(q.Groups[0].Offers) != 1 || q.Groups[0].Offers[0].OfferID != "b" || !q.QuotedAt.Equal(fixedNow) {
		t.Fatalf("quote = %+v", q)
	}
	if q.Unavailable[0].GroupNo != 0 {
		t.Fatalf("a whole-quote constraint has no group: %+v", q.Unavailable)
	}
}

func TestAGroupKnownOnlyByAConstraintSpansEveryLineWhenAlone(t *testing.T) {
	g, _ := serve(t, 200, "application/json", `{"offers":[],"unavailable":[{"tier":"STANDARD","constraint":"NODE_CAPACITY_EXHAUSTED","groupNo":1}]}`)
	q, err := g.Quote(context.Background(), "k", "787", cartLines)
	if err != nil || len(q.Groups) != 1 || len(q.Groups[0].Offers) != 0 || len(q.Groups[0].LineNos) != 2 {
		t.Fatalf("%+v %v", q, err)
	}
}

func TestGatewayMapsFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   error
	}{
		"server error": {500, `{}`, ports.ErrDeliveryPromiseUnavailable},
		"bad gateway":  {502, ``, ports.ErrDeliveryPromiseUnavailable},
		"rejected":     {422, `{"type":"/problems/validation-failed","title":"Validation failed","detail":"lines are required"}`, ports.ErrQuoteRejected},
		"not found":    {404, ``, ports.ErrQuoteRejected},
		"garbage":      {200, `not json`, ports.ErrDeliveryPromiseUnavailable},
	} {
		g, _ := serve(t, tc.status, "application/json", tc.body)
		if _, err := g.Quote(context.Background(), "k", "787", cartLines); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", name, err, tc.want)
		}
	}
}

func TestGatewayTreatsATransportErrorAndATimeoutAsUnavailable(t *testing.T) {
	g := deliverypromise.New("http://127.0.0.1:1", &http.Client{Timeout: time.Second}, nil)
	if _, err := g.Quote(context.Background(), "k", "787", cartLines); !errors.Is(err, ports.ErrDeliveryPromiseUnavailable) {
		t.Fatalf("got %v", err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer slow.Close()
	g = deliverypromise.New(slow.URL, &http.Client{Timeout: 50 * time.Millisecond}, nil)
	if _, err := g.Quote(context.Background(), "k", "787", cartLines); !errors.Is(err, ports.ErrDeliveryPromiseUnavailable) {
		t.Fatalf("got %v", err)
	}
}
