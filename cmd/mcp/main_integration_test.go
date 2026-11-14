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
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundhttp "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/clock"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/ids"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/kafka"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/postgres"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/testing/pgtest"
)

// Integration test for cmd/mcp over REAL Postgres: buildDeps (the same
// migrations + pool + ping the binary runs at boot) behind newMCP, the same
// composition run() serves, driven by the SDK's own Streamable HTTP client
// through the real router. It proves the server boots against a migrated
// database, lists exactly the curated read-only tools (all L1), answers a read
// from Postgres-backed use cases that equals the document the REST GET returns
// for the same row, serves /healthz, and that a read-only server leaves the
// outbox untouched.
//
// Postgres comes from testcontainers (internal/testing/pgtest); never an
// external DATABASE_URL, never t.Skip.

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// itest is the booted server, a client connected to it, the REST router over
// the same rows and the id of the seeded shopper cart.
type itest struct {
	baseURL string
	session *sdk.ClientSession
	rest    http.Handler
	cartID  string
}

// boot seeds a fresh database through the REAL write use cases (what cmd/api
// runs), boots the read-only server over it and returns the outbox row counter.
func boot(t *testing.T) (*itest, func() int) {
	t.Helper()
	ctx := context.Background()
	dsn := pgtest.URL(t) // a fresh, already migrated database: the boot migration must be a no-op
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	// The server: exactly the read-only wiring the binary boots.
	deps, closeAdapters, err := buildDeps(ctx, quiet, dsn, dsn, "USD")
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeAdapters)

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
	now := clock.System{}.Now()
	if err := write.Items.MarkCatalogued(ctx, "sku-1", now); err != nil {
		t.Fatal(err)
	}
	if err := write.Items.ApplyPrice(ctx, "sku-1", "USD", 1000, 1, now); err != nil {
		t.Fatal(err)
	}
	it := &itest{cartID: seedCart(t, write, "shopper-1")}
	_ = seedCart(t, write, "")

	srv := httptest.NewServer(newMCP(deps, quiet, "shopping-cart-mcp"))
	t.Cleanup(srv.Close)
	it.baseURL = srv.URL
	it.rest = inboundhttp.NewRouter(&inboundhttp.Server{GetCart: usecases.GetCart{Deps: deps}, ListCarts: usecases.ListCarts{Deps: deps}}, quiet, "")
	client := sdk.NewClient(&sdk.Implementation{Name: "itest", Version: "0"}, nil)
	it.session, err = client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = it.session.Close() })

	return it, func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
}

func TestMCPReadsFromRealPostgresAndWritesNothing(t *testing.T) {
	it, outboxRows := boot(t)
	before := outboxRows()
	if before == 0 {
		t.Fatal("the seed should have written outbox rows; the test would prove nothing")
	}

	t.Run("/healthz answers ok", it.healthz)
	t.Run("tools/list is exactly the curated read-only set, all L1", it.toolsList)
	t.Run("get_cart is the REST document of the same row", it.getCartEqualsREST)
	t.Run("list_shopper_carts is the REST list of the same rows", it.listEqualsREST)
	t.Run("an unknown cart is a not-found tool error and a NUL never reaches Postgres as a 500", it.notFound)
	t.Run("the cart resource and the prompt are served", it.resourceAndPrompt)

	if after := outboxRows(); after != before {
		t.Fatalf("outbox_events went from %d to %d rows across read-only tools", before, after)
	}
}

func (it *itest) healthz(t *testing.T) {
	resp, err := http.Get(it.baseURL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if body, _ := io.ReadAll(resp.Body); resp.StatusCode != http.StatusOK || string(body) != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %s", resp.StatusCode, body)
	}
}

func (it *itest) toolsList(t *testing.T) {
	tools, err := it.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint || tool.Meta["com.warehouse/authority"] != "L1" {
			t.Errorf("tool %q is not a read-only L1 tool: %+v %v", tool.Name, tool.Annotations, tool.Meta)
		}
	}
	sort.Strings(names)
	if got := strings.Join(names, ","); got != "get_cart,list_shopper_carts" {
		t.Fatalf("tools = %s, want get_cart,list_shopper_carts", got)
	}
}

func (it *itest) call(t *testing.T, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := it.session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func (it *itest) getCartEqualsREST(t *testing.T) {
	res := it.call(t, "get_cart", map[string]any{"cartId": it.cartID})
	if res.IsError {
		t.Fatalf("get_cart returned a tool error: %+v", res.Content)
	}
	if want := restJSON(t, it.rest, "/carts/"+it.cartID); !reflect.DeepEqual(res.StructuredContent, want) {
		t.Fatalf("MCP cart differs from REST:\n mcp: %v\nrest: %v", res.StructuredContent, want)
	}
}

func (it *itest) listEqualsREST(t *testing.T) {
	res := it.call(t, "list_shopper_carts", map[string]any{"shopperId": "shopper-1"})
	if res.IsError {
		t.Fatalf("list_shopper_carts returned a tool error: %+v", res.Content)
	}
	got := res.StructuredContent.(map[string]any)["carts"]
	if want := restJSON(t, it.rest, "/carts?shopperId=shopper-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("MCP list differs from REST:\n mcp: %v\nrest: %v", got, want)
	}
}

func (it *itest) notFound(t *testing.T) {
	for _, id := range []string{"ghost", "a\x00b"} {
		res := it.call(t, "get_cart", map[string]any{"cartId": id})
		if !res.IsError || !strings.HasPrefix(res.Content[0].(*sdk.TextContent).Text, "not-found: ") {
			t.Fatalf("get_cart %q = %+v, want a not-found tool error", id, res)
		}
	}
}

func (it *itest) resourceAndPrompt(t *testing.T) {
	ctx := context.Background()
	read, err := it.session.ReadResource(ctx, &sdk.ReadResourceParams{URI: "warehouse://shopping-cart/carts/" + it.cartID})
	if err != nil || len(read.Contents) != 1 {
		t.Fatalf("read resource: %v %+v", err, read)
	}
	if _, err := it.session.GetPrompt(ctx, &sdk.GetPromptParams{Name: "review_cart", Arguments: map[string]string{"cartId": it.cartID}}); err != nil {
		t.Fatalf("get prompt: %v", err)
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
