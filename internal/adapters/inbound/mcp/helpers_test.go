package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/IQVO/shopping-cart/internal/adapters/inbound/mcp"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

// t0 is the fixture's fixed "now".
var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// world is the seeded shopping-cart every tool test reads. Writes go through
// the REAL write use cases on a full Deps; the server under test gets the
// read-only Deps cmd/mcp wires (no outbox, unit of work, id generator or
// quoter), so a read that touched one of them would panic on a nil port.
type world struct {
	// ready is a shopper-1 cart with a line, a destination, a quote with one
	// group (two offers) and one chosen offer; open is a later, empty
	// shopper-1 cart; guest has no shopper; other belongs to shopper-2.
	ready, open, guest, other string
}

// harness is a connected in-memory MCP client over the real server.
type harness struct {
	session  *sdk.ClientSession
	store    *memory.Store
	readOnly usecases.Deps
	world    world
}

func newDeps(store *memory.Store, clock *memory.Clock) (full, readOnly usecases.Deps, quoter *memory.Quoter) {
	quoter = &memory.Quoter{}
	full = usecases.Deps{
		Carts: store.Carts(), Items: store.Items(), Schedules: store.Schedules(), TaxTables: store.TaxTables(),
		Quoter: quoter, Inbox: store.Inbox(), Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: clock,
		IDs: &memory.SequentialIDs{}, Currency: "USD",
	}
	readOnly = usecases.Deps{
		Carts: store.Carts(), Items: store.Items(), Schedules: store.Schedules(), TaxTables: store.TaxTables(),
		Clock: clock, Currency: "USD",
	}
	return full, readOnly, quoter
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func readDeps(d usecases.Deps) inboundmcp.Deps {
	return inboundmcp.Deps{GetCart: usecases.GetCart{Deps: d}, ListCarts: usecases.ListCarts{Deps: d}}
}

// seed builds the world through the write use cases.
func seed(t *testing.T, store *memory.Store, clock *memory.Clock, full usecases.Deps, quoter *memory.Quoter) world {
	t.Helper()
	ctx := context.Background()
	create := func(shopper string) string {
		t.Helper()
		v, err := usecases.CreateCart{Deps: full}.Execute(ctx, shopper)
		must(t, err)
		return v.CartID
	}
	must(t, store.Items().MarkCatalogued(ctx, "sku-1", t0))
	must(t, store.Items().ApplyPrice(ctx, "sku-1", "USD", 1000, 1, t0))
	_, err := store.Schedules().Replace(ctx, estimate.Schedule{Version: 1, Tiers: map[cart.Tier]estimate.TierCharge{cart.TierNextDay: {ChargeMinor: 500}}})
	must(t, err)
	_, err = store.TaxTables().Replace(ctx, estimate.TaxTable{Version: 1, Bands: []estimate.TaxBand{{From: "700", To: "799", RateBasisPoints: 1000}}})
	must(t, err)

	w := world{ready: create("shopper-1")}
	expires := t0.Add(time.Hour)
	offer := func(id string, tier cart.Tier) cart.QuotedOffer {
		return cart.QuotedOffer{OfferID: id, GroupNo: 1, Tier: tier, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: expires}
	}
	quoter.Respond(cart.DeliveryQuote{QuotedAt: t0, Groups: []cart.QuoteGroup{{GroupNo: 1, LineNos: []int{1},
		Offers: []cart.QuotedOffer{offer("o1", cart.TierStandard), offer("o2", cart.TierNextDay)}}},
		Unavailable: []cart.TierUnavailable{{Tier: cart.TierSameDay, Constraint: "PAST_ORDER_DEADLINE", GroupNo: 1}}}, nil)
	_, err = usecases.AddLine{Deps: full}.Execute(ctx, w.ready, "sku-1", 2)
	must(t, err)
	_, err = usecases.SetDestination{Deps: full}.Execute(ctx, w.ready, "787")
	must(t, err)
	_, err = usecases.QuoteDelivery{Deps: full}.Execute(ctx, w.ready, "k-1")
	must(t, err)
	_, err = usecases.SelectDelivery{Deps: full}.Execute(ctx, w.ready, 1, "o2")
	must(t, err)

	clock.Advance(time.Second)
	w.open = create("shopper-1")
	w.guest = create("")
	w.other = create("shopper-2")
	return w
}

// newHarness seeds the world and connects an in-memory client to the server.
func newHarness(t *testing.T) *harness {
	t.Helper()
	clock := memory.NewClock(t0)
	store := memory.NewStore(clock)
	full, readOnly, quoter := newDeps(store, clock)
	w := seed(t, store, clock, full, quoter)
	return &harness{session: connectSession(t, readDeps(readOnly)), store: store, readOnly: readOnly, world: w}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts with
// the slug want.
func (h *harness) failWith(t *testing.T, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, h.session, name, args)
	if !res.IsError {
		t.Fatalf("%s %v: expected an isError tool result, got %#v", name, args, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s %v: error text %q does not start with %q", name, args, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
