// Package bdd runs the Gherkin scenarios of ../../features against the real
// HTTP router and use cases wired to the in-memory doubles.
package bdd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cucumber/godog"

	httpadapter "github.com/IQVO/shopping-cart/internal/adapters/inbound/http"
	"github.com/IQVO/shopping-cart/internal/adapters/outbound/memory"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

// TestFeatures runs the Gherkin scenarios of ../../features.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"../../features"}, TestingT: t, Strict: true},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// world is the state of one scenario.
type world struct {
	store  *memory.Store
	clock  *memory.Clock
	quoter *memory.Quoter
	deps   usecases.Deps
	srv    *httptest.Server

	first, second string
	keys, events  int
	// versions keeps the next version of each versioned read-model write.
	versions map[string]int64

	status int
	body   string
	header http.Header
}

func newWorld() *world {
	clock := memory.NewClock(t0)
	s := memory.NewStore(clock)
	w := &world{store: s, clock: clock, quoter: &memory.Quoter{}, versions: map[string]int64{}}
	w.deps = usecases.Deps{
		Carts: s.Carts(), Items: s.Items(), Schedules: s.Schedules(), TaxTables: s.TaxTables(), Quoter: w.quoter,
		Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: clock, IDs: &memory.SequentialIDs{}, Currency: "USD",
	}
	return w
}

func (w *world) consumer() usecases.Consumer { return usecases.Consumer{Deps: w.deps} }

func (w *world) meta() usecases.Meta {
	w.events++
	return usecases.Meta{EventID: fmt.Sprintf("ev-%d", w.events), Type: "test", At: w.clock.Now()}
}

// api serves the real router of apis/openapi.yaml over the scenario's world.
func (w *world) api() *httptest.Server {
	if w.srv == nil {
		d := w.deps
		w.srv = httptest.NewServer(httpadapter.NewRouter(&httpadapter.Server{
			CreateCart: usecases.CreateCart{Deps: d}, AddLine: usecases.AddLine{Deps: d}, SetLineQuantity: usecases.SetLineQuantity{Deps: d},
			RemoveLine: usecases.RemoveLine{Deps: d}, SetDestination: usecases.SetDestination{Deps: d}, QuoteDelivery: usecases.QuoteDelivery{Deps: d},
			SelectDelivery: usecases.SelectDelivery{Deps: d}, MergeInto: usecases.MergeInto{Deps: d},
			AcceptRequotedOffers: usecases.AcceptRequotedOffers{Deps: d}, GetCart: usecases.GetCart{Deps: d}, ListCarts: usecases.ListCarts{Deps: d},
			IdempotencyStore: w.store.Idempotency(), Clock: d.Clock,
		}, nil, ""))
	}
	return w.srv
}

func initializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		if w.srv != nil {
			w.srv.Close()
		}
		*w = *newWorld()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.srv != nil {
			w.srv.Close()
			w.srv = nil
		}
		return ctx, err
	})
	registerReadModels(sc, w)
	registerCommands(sc, w)
	registerLifecycle(sc, w)
	registerAssertions(sc, w)
}
