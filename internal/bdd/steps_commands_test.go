package bdd

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

func (w *world) cartPath(suffix string) string { return "/carts/" + w.first + suffix }

func registerCommands(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^an open cart$`, func() error { return w.newCart(&w.first, "") })
	sc.Step(`^an open cart of shopper "([^"]*)"$`, func(s string) error { return w.newCart(&w.first, s) })
	sc.Step(`^a second open cart of shopper "([^"]*)"$`, func(s string) error { return w.newCart(&w.second, s) })
	sc.Step(`^I add (\d+) of "([^"]*)"$`, func(q int, item string) error {
		return w.post(w.cartPath("/lines"), fmt.Sprintf(`{"catalogItemId":%q,"quantity":%d}`, item, q))
	})
	sc.Step(`^I add (\d+) of "([^"]*)" to the second cart$`, func(q int, item string) error {
		return w.mustPost("/carts/"+w.second+"/lines", fmt.Sprintf(`{"catalogItemId":%q,"quantity":%d}`, item, q))
	})
	sc.Step(`^the cart holds 100 lines$`, w.fillCart)
	sc.Step(`^I set the quantity of "([^"]*)" to (\d+)$`, func(item string, q int) error {
		return w.post(w.cartPath("/lines/"+url.PathEscape(item)+":set-quantity"), fmt.Sprintf(`{"quantity":%d}`, q))
	})
	sc.Step(`^I remove "([^"]*)"$`, func(item string) error { return w.post(w.cartPath("/lines/"+url.PathEscape(item)+":remove"), "") })
	sc.Step(`^I set the destination "([^"]*)"$`, func(p string) error {
		return w.post(w.cartPath(":set-destination"), fmt.Sprintf(`{"destinationPostalPrefix":%q}`, p))
	})
	sc.Step(`^I quote delivery$`, func() error { return w.post(w.cartPath(":quote-delivery"), "") })
	sc.Step(`^I select offer "([^"]*)" for group (\d+)$`, func(offer string, group int) error {
		return w.post(w.cartPath(":select-delivery"), fmt.Sprintf(`{"groupNo":%d,"offerId":%q}`, group, offer))
	})
	sc.Step(`^I accept the requoted offer "([^"]*)" (\w+) for group (\d+) expiring in (\d+) minutes$`, func(offer, tier string, group, min int) error {
		exp := w.clock.Now().Add(time.Duration(min) * time.Minute).Format(time.RFC3339)
		return w.post(w.cartPath("/delivery/requoted"), fmt.Sprintf(
			`{"offers":[{"offerId":%q,"groupNo":%d,"tier":%q,"deliverByEarliest":"2026-10-27","deliverByLatest":"2026-10-28","expiresAt":%q}]}`, offer, group, tier, exp))
	})
	sc.Step(`^I accept no requoted offers$`, func() error { return w.post(w.cartPath("/delivery/requoted"), `{"offers":[]}`) })
	sc.Step(`^I read the cart$`, func() error { return w.get(w.cartPath("")) })
}

func (w *world) newCart(into *string, shopper string) error {
	body := "{}"
	if shopper != "" {
		body = fmt.Sprintf(`{"shopperId":%q}`, shopper)
	}
	if err := w.mustPost("/carts", body); err != nil {
		return err
	}
	*into = jsonField(w.body, "cartId")
	return nil
}

// fillCart puts 100 distinct purchasable items in the cart through the use case.
func (w *world) fillCart() error {
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("bulk-%d", i)
		if err := w.stock(id, 100); err != nil {
			return err
		}
		if _, err := (usecases.AddLine{Deps: w.deps}).Execute(context.Background(), w.first, id, 1); err != nil {
			return err
		}
	}
	return nil
}
