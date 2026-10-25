package bdd

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

func registerLifecycle(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^(\d+) (minutes?|days?) pass(?:es)?$`, func(n int, unit string) error {
		d := time.Minute
		if unit[0] == 'd' {
			d = 24 * time.Hour
		}
		w.clock.Advance(time.Duration(n) * d)
		return nil
	})
	sc.Step(`^the idle policies run$`, w.runIdlePolicies)
	sc.Step(`^checkout completes the cart with session "([^"]*)"(?: again)?$`, func(session string) error {
		return w.consumer().ApplyCheckoutCompleted(context.Background(), w.meta(), usecases.CheckoutCompleted{CartID: w.first, SessionID: session})
	})
	sc.Step(`^checkout completes the unknown cart "([^"]*)"$`, func(id string) error {
		return w.consumer().ApplyCheckoutCompleted(context.Background(), w.meta(), usecases.CheckoutCompleted{CartID: id, SessionID: "sess-x"})
	})
	sc.Step(`^I merge the first cart into the second cart$`, func() error {
		return w.post(w.cartPath(":merge-into"), fmt.Sprintf(`{"targetCartId":%q}`, w.second))
	})
	sc.Step(`^I merge the first cart into itself$`, func() error {
		return w.post(w.cartPath(":merge-into"), fmt.Sprintf(`{"targetCartId":%q}`, w.first))
	})
	sc.Step(`^I POST "([^"]*)" without a key and body (.*)$`, func(path, body string) error {
		return w.send(http.MethodPost, path, "", body)
	})
	sc.Step(`^I POST "([^"]*)" with key "([^"]*)" and body (.*)$`, func(path, key, body string) error {
		return w.send(http.MethodPost, path, key, body)
	})
}

func (w *world) runIdlePolicies() error {
	ctx := context.Background()
	if _, err := (usecases.AbandonInactive{Deps: w.deps}).Execute(ctx, 100); err != nil {
		return err
	}
	_, err := usecases.PurgeAbandonedCarts{Deps: w.deps}.Execute(ctx, 100)
	return err
}
