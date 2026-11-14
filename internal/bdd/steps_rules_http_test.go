package bdd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cucumber/godog"
)

// registerRuleHTTP registers the steps that send a request with a hand-picked
// key, body or path, as the idempotency and list rules need.
func registerRuleHTTP(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I create a cart with key "([^"]*)" and no body$`, func(key string) error {
		return w.send(http.MethodPost, "/carts", key, "")
	})
	sc.Step(`^I create a cart with a key of (\d+) characters$`, func(n int) error {
		return w.send(http.MethodPost, "/carts", strings.Repeat("k", n), "{}")
	})
	sc.Step(`^shopper "([^"]*)" creates a cart with key "([^"]*)"$`, func(shopper, key string) error {
		return w.send(http.MethodPost, "/carts", key, fmt.Sprintf(`{"shopperId":%q}`, shopper))
	})
	sc.Step(`^shopper "([^"]*)" opens (\d+) carts?$`, func(shopper string, n int) error {
		for i := 0; i < n; i++ {
			if err := w.mustPost("/carts", fmt.Sprintf(`{"shopperId":%q}`, shopper)); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^I quote delivery with key "([^"]*)"$`, func(key string) error {
		return w.send(http.MethodPost, w.cartPath("/delivery-quotes"), key, "")
	})
	sc.Step(`^I quote delivery of the second cart with key "([^"]*)"$`, func(key string) error {
		return w.send(http.MethodPost, "/carts/"+w.second+"/delivery-quotes", key, "")
	})
	sc.Step(`^I set the destination "([^"]*)" without a key$`, func(p string) error {
		return w.send(http.MethodPut, w.cartPath("/destination"), "", fmt.Sprintf(`{"destinationPostalPrefix":%q}`, p))
	})
	sc.Step(`^I remove "([^"]*)" without a key$`, func(item string) error {
		return w.send(http.MethodDelete, w.cartPath("/lines/"+url.PathEscape(item)), "", "")
	})
	sc.Step(`^I add a line with the raw body (.*)$`, func(body string) error { return w.post(w.cartPath("/lines"), body) })
	sc.Step(`^I read the cart with an id of (\d+) characters$`, func(n int) error { return w.get("/carts/" + strings.Repeat("c", n)) })
	sc.Step(`^I GET the next page of "([^"]*)"$`, func(path string) error {
		v, ok := w.lookup("nextCursor")
		if !ok {
			return errors.New("the last answer has no nextCursor")
		}
		return w.get(path + "&cursor=" + url.QueryEscape(show(v)))
	})
	sc.Step(`^kafka is unreachable$`, func() error {
		w.kafkaDown.Store(true)
		return nil
	})
	sc.Step(`^the service starts draining$`, func() error {
		w.readiness.SetNotReady()
		return nil
	})
	sc.Step(`^I accept these requoted offers (.*)$`, func(list string) error {
		var specs []string
		for _, m := range offerIDSpecRe.FindAllStringSubmatch(list, -1) {
			specs = append(specs, m[1])
		}
		return w.acceptSpec(specs)
	})
}

// registerRuleChecks registers the read-back assertions of the rule features.
func registerRuleChecks(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the response field "([^"]*)" exists$`, func(path string) error {
		if _, ok := w.lookup(path); !ok {
			return fmt.Errorf("field %s is absent: %s", path, w.body)
		}
		return nil
	})
	sc.Step(`^the response field "([^"]*)" reads "([^"]*)"$`, w.fieldIs)
	sc.Step(`^the response list "([^"]*)" has (\d+) items?$`, func(path string, n int) error {
		v, _ := w.lookup(path)
		if arr, _ := v.([]any); len(arr) != n {
			return fmt.Errorf("list %s has %d items, want %d: %s", path, len(arr), n, w.body)
		}
		return nil
	})
	sc.Step(`^the answer is not a replay$`, func() error {
		if w.header.Get("Idempotency-Replayed") != "" {
			return fmt.Errorf("the answer is a replay: %v", w.header)
		}
		return nil
	})
	sc.Step(`^the cart has the line "([^"]*)" with quantity (\d+)$`, func(item string, q int) error {
		return w.hasLine(w.first, item, q)
	})
	sc.Step(`^the second cart has the line "([^"]*)" with quantity (\d+)$`, func(item string, q int) error {
		return w.hasLine(w.second, item, q)
	})
	sc.Step(`^the second cart has (\d+) lines?$`, func(n int) error {
		if err := w.get("/carts/" + w.second); err != nil {
			return err
		}
		return w.countLines(n)
	})
}

// hasLine reads the cart and checks the quantity of the line of an item.
func (w *world) hasLine(cartID, item string, want int) error {
	if err := w.get("/carts/" + cartID); err != nil {
		return err
	}
	var c struct {
		Lines []struct {
			CatalogItemID string `json:"catalogItemId"`
			Quantity      int    `json:"quantity"`
		} `json:"lines"`
	}
	if err := json.Unmarshal([]byte(w.body), &c); err != nil {
		return err
	}
	for _, l := range c.Lines {
		if l.CatalogItemID == item {
			if l.Quantity != want {
				return fmt.Errorf("line %s has quantity %d, want %d", item, l.Quantity, want)
			}
			return nil
		}
	}
	return fmt.Errorf("the cart has no line for %s: %s", item, w.body)
}
