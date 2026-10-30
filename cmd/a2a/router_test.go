package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// newTestAgent builds the router exactly as run() does (newAgent), over the
// in-memory test doubles holding one shopper cart with one line. The memory
// adapter is a test double: it is imported by this _test file only, never by
// the binary. The Deps passed to newAgent is the read-only slice buildDeps
// wires: no outbox, unit of work, quoter or id generator.
func newTestAgent(t *testing.T) (http.Handler, string) {
	t.Helper()
	ctx := context.Background()
	clock := memory.NewClock(t0)
	store := memory.NewStore(clock)
	write := usecases.Deps{
		Carts: store.Carts(), Items: store.Items(), Schedules: store.Schedules(), TaxTables: store.TaxTables(),
		Inbox: store.Inbox(), Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "USD",
	}
	if err := store.Items().MarkCatalogued(ctx, "sku-1", t0); err != nil {
		t.Fatal(err)
	}
	if err := store.Items().ApplyPrice(ctx, "sku-1", "USD", 1000, 1, t0); err != nil {
		t.Fatal(err)
	}
	v, err := usecases.CreateCart{Deps: write}.Execute(ctx, "shopper-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (usecases.AddLine{Deps: write}).Execute(ctx, v.CartID, "sku-1", 2); err != nil {
		t.Fatal(err)
	}
	readOnly := usecases.Deps{Carts: store.Carts(), Items: store.Items(), Schedules: store.Schedules(), TaxTables: store.TaxTables(), Clock: clock, Currency: "USD"}
	return newAgent(readOnly, testAgentURL, "test"), v.CartID
}

func TestRouter_HealthzServesOK(t *testing.T) {
	h, _ := newTestAgent(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: got %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != `{"status":"ok"}` {
		t.Fatalf("GET /healthz body = %q, want {\"status\":\"ok\"}", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("GET /healthz Content-Type = %q, want application/json", ct)
	}
}

func TestRouter_ServesTheAgentCardAtTheWellKnownPath(t *testing.T) {
	h, _ := newTestAgent(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent card: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"shopping-cart"`, testAgentURL, `"get_cart"`, `"list_shopper_carts"`} {
		if !strings.Contains(body, want) {
			t.Errorf("agent card lacks %s: %s", want, body)
		}
	}
}

// TestRouter_BothMountsReachTheJSONRPCHandler pins the fleet's
// <CTX>_A2A_ENDPOINT convention: the endpoint answers at "/" and at "/a2a", and
// the use cases behind it are the wired ones (the seeded cart is read).
func TestRouter_BothMountsReachTheJSONRPCHandler(t *testing.T) {
	h, cartID := newTestAgent(t)
	for _, path := range []string{"/", "/a2a"} {
		t.Run(path, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"get_cart","arguments":{"cartId":"` + cartID + `"}}}]}}}`
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("A2A-Version", "1.0")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			raw, _ := io.ReadAll(rec.Body)
			if rec.Code == http.StatusNotFound || !strings.Contains(string(raw), `"jsonrpc"`) {
				t.Fatalf("POST %s: %d %q does not look like a JSON-RPC reply", path, rec.Code, raw)
			}
			if !strings.Contains(string(raw), `"shopperId":"shopper-1"`) || !strings.Contains(string(raw), `"itemsMinor":2000`) {
				t.Fatalf("POST %s: reply %q lacks the seeded cart read through the wired use case", path, raw)
			}
		})
	}
}

// TestRouter_ACardFetchedFromTheBinaryIsDialable runs the discovery flow a
// delegating agent runs against the real router: fetch the card, build a client
// from it, call a skill.
func TestRouter_ACardFetchedFromTheBinaryIsDialable(t *testing.T) {
	h, cartID := newTestAgent(t)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	// The card advertises the configured URL, not the test server's: point the
	// client at the test server the way a gateway-rewritten route would.
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser,
		a2a.NewDataPart(map[string]any{"skill": "list_shopper_carts", "arguments": map[string]any{"shopperId": "shopper-1"}}))})
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T", resp)
	}
	if text, _ := msg.Parts[1].Content.(a2a.Text); !strings.Contains(string(text), cartID) {
		t.Fatalf("summary = %q, want the seeded cart listed", text)
	}
}
