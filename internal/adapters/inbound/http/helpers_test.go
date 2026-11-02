package http_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	httpadapter "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

type env struct {
	t      *testing.T
	store  *memory.Store
	clock  *memory.Clock
	quoter *memory.Quoter
	deps   usecases.Deps
	ready  *httpadapter.Readiness
	srv    *httptest.Server
	keys   int
}

func newEnv(t *testing.T) *env { return newEnvLogged(t, slog.New(slog.DiscardHandler)) }

// newEnvLogged is newEnv with the router's request logger set (newEnv discards
// it so the access lines do not flood the test output).
func newEnvLogged(t *testing.T, logger *slog.Logger) *env {
	t.Helper()
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	quoter := &memory.Quoter{}
	deps := usecases.Deps{
		Carts: s.Carts(), Items: s.Items(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quoter: quoter,
		Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "USD",
	}
	ready := httpadapter.NewReadiness(0, 0)
	api := &httpadapter.Server{
		CreateCart: usecases.CreateCart{Deps: deps}, AddLine: usecases.AddLine{Deps: deps},
		SetLineQuantity: usecases.SetLineQuantity{Deps: deps}, RemoveLine: usecases.RemoveLine{Deps: deps},
		SetDestination: usecases.SetDestination{Deps: deps}, QuoteDelivery: usecases.QuoteDelivery{Deps: deps},
		SelectDelivery: usecases.SelectDelivery{Deps: deps}, MergeInto: usecases.MergeInto{Deps: deps},
		AcceptRequotedOffers: usecases.AcceptRequotedOffers{Deps: deps}, GetCart: usecases.GetCart{Deps: deps},
		ListCarts: usecases.ListCarts{Deps: deps}, IdempotencyStore: s.Idempotency(), Clock: clock, Readiness: ready,
	}
	srv := httptest.NewServer(httpadapter.NewRouter(api, logger, ""))
	t.Cleanup(srv.Close)
	return &env{t: t, store: s, clock: clock, quoter: quoter, deps: deps, ready: ready, srv: srv}
}

type reply struct {
	Status int
	Header http.Header
	Body   string
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("body is not JSON: %q (%v)", r.Body, err)
	}
	return m
}

// do sends one request; key "" sends no Idempotency-Key.
func (e *env) do(method, path, key, body string) reply {
	e.t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: string(b)}
}

// post sends a POST with a fresh Idempotency-Key.
func (e *env) post(path, body string) reply {
	e.t.Helper()
	e.keys++
	return e.do(http.MethodPost, path, "key-"+strconv.Itoa(e.keys), body)
}

func (e *env) get(path string) reply { e.t.Helper(); return e.do(http.MethodGet, path, "", "") }

// put sends a PUT: idempotent by method, so it takes no Idempotency-Key.
func (e *env) put(path, body string) reply {
	e.t.Helper()
	return e.do(http.MethodPut, path, "", body)
}

// del sends a DELETE (idempotent by method, no Idempotency-Key).
func (e *env) del(path string) reply { e.t.Helper(); return e.do(http.MethodDelete, path, "", "") }

func (e *env) wantStatus(r reply, status int) {
	e.t.Helper()
	if r.Status != status {
		e.t.Fatalf("status = %d, want %d: %s", r.Status, status, r.Body)
	}
}

func (e *env) wantProblem(r reply, status int, slug string) {
	e.t.Helper()
	e.wantStatus(r, status)
	if ct := r.Header.Get("Content-Type"); ct != "application/problem+json" {
		e.t.Fatalf("content-type = %q", ct)
	}
	if got := r.json(e.t)["type"]; got != "/problems/"+slug {
		e.t.Fatalf("problem type = %v, want /problems/%s (%s)", got, slug, r.Body)
	}
}
