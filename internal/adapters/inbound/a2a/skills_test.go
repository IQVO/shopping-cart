package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// fixture holds the seeded carts. Writes go through the REAL write use cases on
// a full Deps; the agent under test gets a read-only Deps (the same slice
// cmd/a2a wires), so a read that touched the outbox, the unit of work or the
// quoter would panic on a nil port.
type fixture struct {
	store *memory.Store
	// full is the seeding Deps; readOnly is what the agent sees.
	full, readOnly usecases.Deps
	// ready is a shopper-1 cart with a quote and a selection; open is a later,
	// empty shopper-1 cart; guest has no shopper; other belongs to shopper-2.
	ready, open, guest, other string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	clock := memory.NewClock(t0)
	store := memory.NewStore(clock)
	quoter := &memory.Quoter{}
	full := usecases.Deps{
		Carts: store.Carts(), Items: store.Items(), Schedules: store.Schedules(), TaxTables: store.TaxTables(),
		Quoter: quoter, Inbox: store.Inbox(), Outbox: store.Outbox(), UoW: store.UnitOfWork(), Clock: clock,
		IDs: &memory.SequentialIDs{}, Currency: "USD",
	}
	f := &fixture{store: store, full: full, readOnly: usecases.Deps{
		Carts: store.Carts(), Items: store.Items(), Schedules: store.Schedules(), TaxTables: store.TaxTables(),
		Clock: clock, Currency: "USD",
	}}
	create := func(shopper string) string {
		t.Helper()
		v, err := usecases.CreateCart{Deps: full}.Execute(ctx, shopper)
		if err != nil {
			t.Fatal(err)
		}
		return v.CartID
	}
	if err := store.Items().MarkCatalogued(ctx, "sku-1", t0); err != nil {
		t.Fatal(err)
	}
	if err := store.Items().ApplyPrice(ctx, "sku-1", "USD", 1000, 1, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Schedules().Replace(ctx, estimate.Schedule{Version: 1, Tiers: map[cart.Tier]estimate.TierCharge{cart.TierNextDay: {ChargeMinor: 500}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TaxTables().Replace(ctx, estimate.TaxTable{Version: 1, Bands: []estimate.TaxBand{{From: "700", To: "799", RateBasisPoints: 1000}}}); err != nil {
		t.Fatal(err)
	}

	f.ready = create("shopper-1")
	f.seedReady(t, quoter)
	clock.Advance(time.Second)
	f.open = create("shopper-1")
	f.guest = create("")
	f.other = create("shopper-2")
	return f
}

// seedReady takes the ready cart to: one line, a destination, a quote with one
// group (two offers) and one unavailable tier, and the NEXT_DAY offer chosen.
func (f *fixture) seedReady(t *testing.T, quoter *memory.Quoter) {
	t.Helper()
	ctx := context.Background()
	expires := t0.Add(time.Hour)
	offer := func(id string, tier cart.Tier) cart.QuotedOffer {
		return cart.QuotedOffer{OfferID: id, GroupNo: 1, Tier: tier, DeliverByEarliest: "2026-10-26", DeliverByLatest: "2026-10-27", ExpiresAt: expires}
	}
	quoter.Respond(cart.DeliveryQuote{QuotedAt: t0, Groups: []cart.QuoteGroup{{GroupNo: 1, LineNos: []int{1},
		Offers: []cart.QuotedOffer{offer("o1", cart.TierStandard), offer("o2", cart.TierNextDay)}}},
		Unavailable: []cart.TierUnavailable{{Tier: cart.TierSameDay, Constraint: "PAST_ORDER_DEADLINE", GroupNo: 1}}}, nil)
	steps := []func() error{
		func() error { _, err := usecases.AddLine{Deps: f.full}.Execute(ctx, f.ready, "sku-1", 2); return err },
		func() error { _, err := usecases.SetDestination{Deps: f.full}.Execute(ctx, f.ready, "787"); return err },
		func() error { _, err := usecases.QuoteDelivery{Deps: f.full}.Execute(ctx, f.ready, "k-1"); return err },
		func() error {
			_, err := usecases.SelectDelivery{Deps: f.full}.Execute(ctx, f.ready, 1, "o2")
			return err
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
}

// deps is the agent's read-only Deps over the fixture.
func (f *fixture) deps() Deps {
	return Deps{GetCart: usecases.GetCart{Deps: f.readOnly}, ListCarts: usecases.ListCarts{Deps: f.readOnly}}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  *struct {
		Cart  json.RawMessage `json:"cart"`
		Carts *struct {
			Carts []json.RawMessage `json:"carts"`
		} `json:"carts"`
	} `json:"data"`
	Problem *wireProblem `json:"problem"`
}

type wireProblem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// wireCart is the subset of the cart document the assertions read.
type wireCart struct {
	CartID                  string  `json:"cartId"`
	ShopperID               *string `json:"shopperId"`
	State                   string  `json:"state"`
	DestinationPostalPrefix *string `json:"destinationPostalPrefix"`
	Lines                   []struct {
		CatalogItemID string `json:"catalogItemId"`
		Quantity      int    `json:"quantity"`
	} `json:"lines"`
	Selections []struct {
		GroupNo int    `json:"groupNo"`
		OfferID string `json:"offerId"`
	} `json:"selections"`
	DeliveryQuote *struct {
		Groups []struct {
			Offers []struct {
				OfferID string `json:"offerId"`
			} `json:"offers"`
		} `json:"groups"`
		UnavailableTiers []string `json:"unavailableTiers"`
	} `json:"deliveryQuote"`
	Estimate struct {
		Currency         string `json:"currency"`
		ItemsMinor       int64  `json:"itemsMinor"`
		DeliveryMinor    int64  `json:"deliveryMinor"`
		TaxMinor         int64  `json:"taxMinor"`
		TotalMinor       int64  `json:"totalMinor"`
		EstimateComplete bool   `json:"estimateComplete"`
	} `json:"estimate"`
	CheckoutReadiness struct {
		Ready bool `json:"ready"`
	} `json:"checkoutReadiness"`
	Version int64 `json:"version"`
}

func (e wireEnvelope) cart(t *testing.T) wireCart {
	t.Helper()
	if e.Problem != nil || e.Data == nil || e.Data.Cart == nil {
		t.Fatalf("envelope = %+v, want a cart", e)
	}
	var c wireCart
	if err := json.Unmarshal(e.Data.Cart, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func (e wireEnvelope) carts(t *testing.T) []wireCart {
	t.Helper()
	if e.Problem != nil || e.Data == nil || e.Data.Carts == nil {
		t.Fatalf("envelope = %+v, want a cart list", e)
	}
	out := make([]wireCart, len(e.Data.Carts.Carts))
	for i, raw := range e.Data.Carts.Carts {
		if err := json.Unmarshal(raw, &out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// call sends one message part and returns the decoded envelope and the
// one-line text summary of the agent's reply.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != problemBase+slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func TestWireGetCartReturnsTheFullDocument(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())

	env, text := w.skill(t, skillGetCart, map[string]any{"cartId": f.ready})
	c := env.cart(t)
	assertReadyCartContent(t, c, f.ready)
	assertReadyCartQuote(t, c)
	assertReadyCartEstimate(t, c)
	if want := f.ready + ": OPEN, 1 lines, estimate 2750 USD minor units (complete), ready for checkout"; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

func assertReadyCartContent(t *testing.T, c wireCart, id string) {
	t.Helper()
	if c.CartID != id || c.ShopperID == nil || *c.ShopperID != "shopper-1" || c.State != "OPEN" ||
		c.DestinationPostalPrefix == nil || *c.DestinationPostalPrefix != "787" || c.Version == 0 || !c.CheckoutReadiness.Ready {
		t.Fatalf("cart = %+v", c)
	}
	if len(c.Lines) != 1 || c.Lines[0].CatalogItemID != "sku-1" || c.Lines[0].Quantity != 2 {
		t.Fatalf("lines = %+v", c.Lines)
	}
	if len(c.Selections) != 1 || c.Selections[0].OfferID != "o2" {
		t.Fatalf("selections = %+v", c.Selections)
	}
}

func assertReadyCartQuote(t *testing.T, c wireCart) {
	t.Helper()
	q := c.DeliveryQuote
	if q == nil || len(q.Groups) != 1 || len(q.Groups[0].Offers) != 2 || len(q.UnavailableTiers) != 1 || q.UnavailableTiers[0] != "SAME_DAY" {
		t.Fatalf("quote = %+v", q)
	}
}

func assertReadyCartEstimate(t *testing.T, c wireCart) {
	t.Helper()
	e := c.Estimate
	if e.Currency != "USD" || e.ItemsMinor != 2000 || e.DeliveryMinor != 500 || e.TaxMinor != 250 || e.TotalMinor != 2750 || !e.EstimateComplete {
		t.Fatalf("estimate = %+v", e)
	}
}

func TestWireGuestCartHasNoShopperAndNoQuote(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())

	env, text := w.skill(t, skillGetCart, map[string]any{"cartId": f.guest})
	c := env.cart(t)
	if c.ShopperID != nil || c.DeliveryQuote != nil || c.DestinationPostalPrefix != nil || c.CheckoutReadiness.Ready {
		t.Fatalf("guest cart = %+v", c)
	}
	if !strings.Contains(text, "not ready for checkout") {
		t.Fatalf("summary = %q", text)
	}
}

func TestWireListShopperCartsIsNewestFirstAndScopedToTheShopper(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())

	env, text := w.skill(t, skillListShopperCarts, map[string]any{"shopperId": "shopper-1"})
	got := env.carts(t)
	if len(got) != 2 || got[0].CartID != f.open || got[1].CartID != f.ready {
		t.Fatalf("carts = %+v, want [%s %s]", got, f.open, f.ready)
	}
	if want := "2 carts: " + f.open + " (OPEN), " + f.ready + " (OPEN)"; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
	// Another shopper's cart, and the guest cart, never appear.
	for _, c := range got {
		if c.CartID == f.other || c.CartID == f.guest {
			t.Fatalf("the list leaks cart %s", c.CartID)
		}
	}
}

func TestWireListShopperCartsFiltersByState(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())

	env, _ := w.skill(t, skillListShopperCarts, map[string]any{"shopperId": "shopper-1", "state": "OPEN"})
	if got := env.carts(t); len(got) != 2 {
		t.Fatalf("OPEN carts = %+v", got)
	}
	env, _ = w.skill(t, skillListShopperCarts, map[string]any{"shopperId": "shopper-1", "state": "MERGED"})
	if got := env.carts(t); len(got) != 0 {
		t.Fatalf("MERGED carts = %+v", got)
	}
}

func TestWireShopperWithoutCartsIsAnEmptyListNotAProblem(t *testing.T) {
	w := newWire(t, newFixture(t).deps())

	env, text := w.skill(t, skillListShopperCarts, map[string]any{"shopperId": "nobody"})
	if env.Problem != nil || env.Data == nil || env.Data.Carts == nil || env.Data.Carts.Carts == nil || len(env.Data.Carts.Carts) != 0 {
		t.Fatalf("envelope = %+v, want an empty carts array", env)
	}
	if text != "0 carts" {
		t.Fatalf("summary = %q", text)
	}
}

// An unknown cart is a 404 answered in-band with the type and status the REST
// adapter gives GET /carts/{cartId}.
func TestWireUnknownCartIsAnsweredAs404NotRaised(t *testing.T) {
	w := newWire(t, newFixture(t).deps())

	env, text := w.skill(t, skillGetCart, map[string]any{"cartId": "ghost"})
	wantProblem(t, env, 404, "not-found")
	if env.Problem.Detail != `no cart exists with cartId "ghost"` {
		t.Fatalf("detail = %q", env.Problem.Detail)
	}
	if !strings.Contains(text, "(404)") {
		t.Fatalf("summary = %q", text)
	}
}

// The REST adapter answers a path that can never name a cart the same way: a
// NUL is resource-not-found, an over-long id is not-found, and neither is a
// 500 from Postgres.
func TestWireIdsThatCanNeverNameACartAreNotFound(t *testing.T) {
	w := newWire(t, newFixture(t).deps())

	env, _ := w.skill(t, skillGetCart, map[string]any{"cartId": "cart\x00-1"})
	wantProblem(t, env, 404, "resource-not-found")

	long := strings.Repeat("x", maxCartIDLength+1)
	env, text := w.skill(t, skillGetCart, map[string]any{"cartId": long})
	wantProblem(t, env, 404, "not-found")
	if strings.Contains(env.Problem.Detail, "xxxxxxxx") || strings.Contains(text, "xxxxxxxx") {
		t.Fatalf("an over-long id is echoed back: %q", env.Problem.Detail)
	}
}

// failingCarts makes every cart read fail, standing in for a database outage.
type failingCarts struct{ ports.CartRepository }

var errBoom = errors.New("pgx: connection refused to 10.0.0.7:5432 password=hunter2")

func (failingCarts) Get(context.Context, cart.ID) (*cart.Cart, error) { return nil, errBoom }
func (failingCarts) ListByShopper(context.Context, string, cart.State) ([]*cart.Cart, error) {
	return nil, errBoom
}

// An unexpected failure is answered as a 500 problem, never a JSON-RPC error,
// and its raw text (which may carry infrastructure detail) is not echoed.
func TestWireUnexpectedFailureIs500AndDoesNotLeakTheCause(t *testing.T) {
	f := newFixture(t)
	broken := f.readOnly
	broken.Carts = failingCarts{f.readOnly.Carts}
	w := newWire(t, Deps{GetCart: usecases.GetCart{Deps: broken}, ListCarts: usecases.ListCarts{Deps: broken}})

	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"get":  {skillGetCart, map[string]any{"cartId": f.ready}},
		"list": {skillListShopperCarts, map[string]any{"shopperId": "shopper-1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 500, "internal-error")
			for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
				if strings.Contains(s, "hunter2") || strings.Contains(s, "10.0.0.7") {
					t.Fatalf("a 500 answer leaks the underlying error: %q", s)
				}
			}
		})
	}
}

// The argument problems keep REST's classes: a malformed request is 400, and a
// value the list handler rejects is 422 validation-failed, as on GET /carts.
func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, newFixture(t).deps())
	cases := map[string]struct {
		skill  string
		args   map[string]any
		status int
		slug   string
	}{
		"get: missing cartId":        {skillGetCart, map[string]any{}, 400, "malformed-request"},
		"get: blank cartId":          {skillGetCart, map[string]any{"cartId": "   "}, 400, "malformed-request"},
		"get: cartId wrong type":     {skillGetCart, map[string]any{"cartId": 7}, 400, "malformed-request"},
		"get: unknown argument":      {skillGetCart, map[string]any{"cartId": "c", "force": true}, 400, "malformed-request"},
		"get: typo'd argument":       {skillGetCart, map[string]any{"cartID2": "c"}, 400, "malformed-request"},
		"get: wrong-skill argument":  {skillGetCart, map[string]any{"cartId": "c", "shopperId": "s"}, 400, "malformed-request"},
		"list: missing shopperId":    {skillListShopperCarts, map[string]any{}, 422, "validation-failed"},
		"list: blank shopperId":      {skillListShopperCarts, map[string]any{"shopperId": ""}, 422, "validation-failed"},
		"list: invalid shopperId":    {skillListShopperCarts, map[string]any{"shopperId": "has space"}, 422, "validation-failed"},
		"list: shopperId too long":   {skillListShopperCarts, map[string]any{"shopperId": strings.Repeat("s", 65)}, 422, "validation-failed"},
		"list: unknown state":        {skillListShopperCarts, map[string]any{"shopperId": "s", "state": "DELETED"}, 422, "validation-failed"},
		"list: lower-case state":     {skillListShopperCarts, map[string]any{"shopperId": "s", "state": "open"}, 422, "validation-failed"},
		"list: unknown argument":     {skillListShopperCarts, map[string]any{"shopperId": "s", "stat": "OPEN"}, 400, "malformed-request"},
		"list: state wrong type":     {skillListShopperCarts, map[string]any{"shopperId": "s", "state": 1}, 400, "malformed-request"},
		"list: wrong-skill argument": {skillListShopperCarts, map[string]any{"cartId": "c"}, 400, "malformed-request"},
		"list: NUL in shopperId":     {skillListShopperCarts, map[string]any{"shopperId": "s\x00"}, 400, "malformed-request"},
		"list: NUL in state":         {skillListShopperCarts, map[string]any{"shopperId": "s", "state": "OPEN\x00"}, 400, "malformed-request"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, tc.status, tc.slug)
		})
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())

	env, _ := w.call(t, a2a.NewTextPart("not json at all"))
	wantProblem(t, env, 400, "malformed-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}))
	wantProblem(t, env, 400, "malformed-request")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": skillGetCart, "arguments": map[string]any{"cartId": f.ready}, "extra": 1}))
	wantProblem(t, env, 400, "malformed-request")

	// Trailing data after the JSON document is refused too.
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"get_cart","arguments":{"cartId":"`+f.ready+`"}} {"skill":"get_cart"}`))
	wantProblem(t, env, 400, "malformed-request")

	env, _ = w.skill(t, "launch_rocket", map[string]any{})
	wantProblem(t, env, 400, "unknown-skill")

	// A text part holding the same JSON is accepted (documented contract).
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"get_cart","arguments":{"cartId":"`+f.ready+`"}}`))
	if c := env.cart(t); c.CartID != f.ready {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", c)
	}
}

// The write operations of the REST surface are not offered here: asking for
// one is an unknown skill, answered in-band, and nothing is written.
func TestWireOffersNoWriteSkill(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())
	before := f.store.Counts()

	for _, skill := range []string{"create_cart", "add_line", "set_line_quantity", "remove_line", "set_destination", "quote_delivery", "select_delivery", "merge_into", "checkout_cart"} {
		env, _ := w.skill(t, skill, map[string]any{"cartId": f.ready, "catalogItemId": "sku-1", "quantity": 1})
		wantProblem(t, env, 400, "unknown-skill")
	}
	if f.store.Counts() != before {
		t.Fatal("an unknown write skill changed the store")
	}
	env, _ := w.skill(t, skillGetCart, map[string]any{"cartId": f.ready})
	if c := env.cart(t); len(c.Lines) != 1 || c.Lines[0].Quantity != 2 {
		t.Fatalf("the cart changed: %+v", c)
	}
}

// Reads never touch the write side: the same call, twice, leaves the store
// (carts, outbox, inbox, read models) exactly as it was.
func TestWireReadsPersistNothing(t *testing.T) {
	f := newFixture(t)
	w := newWire(t, f.deps())
	before := f.store.Counts()

	for range 2 {
		w.skill(t, skillGetCart, map[string]any{"cartId": f.ready})
		w.skill(t, skillListShopperCarts, map[string]any{"shopperId": "shopper-1"})
	}
	if f.store.Counts() != before {
		t.Fatalf("store counts changed by reads: %+v -> %+v", before, f.store.Counts())
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	f := newFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(f.deps())))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	w := &wire{client: client}
	env, _ := w.skill(t, skillGetCart, map[string]any{"cartId": f.ready})
	if env.cart(t).CartID != f.ready {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}
