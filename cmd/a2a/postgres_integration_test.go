//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inboundhttp "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/clock"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/ids"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/kafka"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

// Integration test for cmd/a2a over REAL Postgres: buildDeps (the same
// migrations + pool + ping the binary runs at boot) behind newAgent, the same
// composition run() serves, driven by the SDK's own client through the real
// router. It proves the skills answer from Postgres-backed use cases, that the
// answer is the document the REST GET returns for the same rows, and that a
// read-only agent leaves the outbox untouched.
//
// Postgres comes from testcontainers (internal/testing/pgtest); never an
// external DATABASE_URL, never t.Skip.

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

func TestA2AReadsFromRealPostgresAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	dsn := pgtest.URL(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The agent: exactly the read-only wiring the binary boots.
	deps, closeAdapters, err := buildDeps(ctx, dsn, dsn, "USD", quiet)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeAdapters)

	// The seed: the REAL write use cases on the same database (what cmd/api
	// runs), so the rows are exactly what production writes.
	pool, err := postgres.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	write := deps
	write.Outbox = postgres.NewOutboxRepo(pool, kafka.NewEncoder())
	write.UoW = postgres.NewUnitOfWork(pool)
	write.Inbox = postgres.NewInbox(pool)
	write.IDs = ids.Generator{}
	write.Clock = clock.System{}
	if err := write.Items.MarkCatalogued(ctx, "sku-1", clock.System{}.Now()); err != nil {
		t.Fatal(err)
	}
	if err := write.Items.ApplyPrice(ctx, "sku-1", "USD", 1000, 1, clock.System{}.Now()); err != nil {
		t.Fatal(err)
	}
	cartID := seedCart(t, write, "shopper-1")
	_ = seedCart(t, write, "")

	var outboxBefore int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&outboxBefore); err != nil {
		t.Fatal(err)
	}
	if outboxBefore == 0 {
		t.Fatal("the seed should have written outbox rows; the test would prove nothing")
	}

	srv := httptest.NewServer(newAgent(deps, testAgentURL, "itest"))
	t.Cleanup(srv.Close)
	rest := inboundhttp.NewRouter(&inboundhttp.Server{GetCart: usecases.GetCart{Deps: deps}, ListCarts: usecases.ListCarts{Deps: deps}}, quiet, "")
	client := clientFor(t, srv.URL+"/a2a")

	t.Run("get_cart is the REST document of the same row", func(t *testing.T) {
		data, text := send(t, client, map[string]any{"skill": "get_cart", "arguments": map[string]any{"cartId": cartID}})
		got := data["data"].(map[string]any)["cart"]
		if want := restJSON(t, rest, "/carts/"+cartID); !reflect.DeepEqual(got, want) {
			t.Fatalf("A2A cart differs from REST:\n a2a: %v\nrest: %v", got, want)
		}
		if got.(map[string]any)["estimate"].(map[string]any)["itemsMinor"].(float64) != 2000 {
			t.Fatalf("estimate = %v", got.(map[string]any)["estimate"])
		}
		if text == "" {
			t.Fatal("no summary")
		}
	})
	t.Run("list_shopper_carts is the REST list of the same rows", func(t *testing.T) {
		data, _ := send(t, client, map[string]any{"skill": "list_shopper_carts", "arguments": map[string]any{"shopperId": "shopper-1"}})
		got := data["data"].(map[string]any)["carts"].(map[string]any)["carts"]
		if want := restJSON(t, rest, "/carts?shopperId=shopper-1"); !reflect.DeepEqual(got, want) {
			t.Fatalf("A2A list differs from REST:\n a2a: %v\nrest: %v", got, want)
		}
	})
	t.Run("an unknown cart is an in-band 404", func(t *testing.T) {
		data, _ := send(t, client, map[string]any{"skill": "get_cart", "arguments": map[string]any{"cartId": "ghost"}})
		if p := data["problem"].(map[string]any); p["status"].(float64) != 404 || p["type"] != "/problems/not-found" {
			t.Fatalf("problem = %v", p)
		}
	})
	t.Run("a NUL in the cartId never reaches Postgres as a 500", func(t *testing.T) {
		data, _ := send(t, client, map[string]any{"skill": "get_cart", "arguments": map[string]any{"cartId": "a\x00b"}})
		if p := data["problem"].(map[string]any); p["status"].(float64) != 404 {
			t.Fatalf("problem = %v", p)
		}
	})

	var outboxAfter int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&outboxAfter); err != nil {
		t.Fatal(err)
	}
	if outboxAfter != outboxBefore {
		t.Fatalf("outbox_events went from %d to %d rows across read-only skills", outboxBefore, outboxAfter)
	}
}

func seedCart(t *testing.T, d usecases.Deps, shopper string) string {
	t.Helper()
	ctx := context.Background()
	v, err := usecases.CreateCart{Deps: d}.Execute(ctx, shopper)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (usecases.AddLine{Deps: d}).Execute(ctx, v.CartID, "sku-1", 2); err != nil {
		t.Fatal(err)
	}
	return v.CartID
}

func restJSON(t *testing.T, h http.Handler, target string) any {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", target, rec.Code, rec.Body.String())
	}
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if m, ok := v.(map[string]any); ok && m["items"] != nil {
		return m["items"]
	}
	return v
}

func clientFor(t *testing.T, endpoint string) *a2aclient.Client {
	t.Helper()
	card := &a2a.AgentCard{
		Name:                "shopping-cart",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(endpoint, a2a.TransportProtocolJSONRPC)},
	}
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// send returns the reply's envelope (data part) and its summary (text part).
func send(t *testing.T, client *a2aclient.Client, req map[string]any) (map[string]any, string) {
	t.Helper()
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(req))})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		raw, _ := json.Marshal(resp)
		t.Fatalf("reply = %s", raw)
	}
	data, _ := msg.Parts[0].Content.(a2a.Data)
	raw, _ := json.Marshal(data.Value)
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text)
}
