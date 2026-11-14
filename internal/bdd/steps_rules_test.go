package bdd

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// docState is the extra per-scenario state of the documentation-derived rules.
type docState struct {
	third   string             // a third cart
	quote   cart.DeliveryQuote // the quote delivery-promise is scripted with, built group by group
	version int64              // a remembered cart version
	err     error              // the last error a consumer returned
}

// registerRules registers the steps of features/*_rules.feature. The steps are
// grouped by what they drive: carts and their closing, the clock-driven
// policies, the events of other contexts, delivery-promise, and raw HTTP.
func registerRules(sc *godog.ScenarioContext, w *world) {
	registerRuleCarts(sc, w)
	registerRuleClosing(sc, w)
	registerRulePolicies(sc, w)
	registerRuleEvents(sc, w)
	registerRuleFeeds(sc, w)
	registerRuleQuoter(sc, w)
	registerRuleHTTP(sc, w)
	registerRuleChecks(sc, w)
}

func registerRuleCarts(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a second open cart$`, func() error { return w.newCart(&w.second, "") })
	sc.Step(`^a third open cart of shopper "([^"]*)"$`, func(s string) error { return w.newCart(&w.doc.third, s) })
	sc.Step(`^the second cart holds (\d+) lines$`, func(n int) error { return w.fillCart(w.second, n) })
	sc.Step(`^I read the second cart$`, func() error { return w.get("/carts/" + w.second) })
	sc.Step(`^the second cart is "([^"]*)"$`, func(state string) error {
		if err := w.get("/carts/" + w.second); err != nil {
			return err
		}
		return w.fieldIs("state", state)
	})
	sc.Step(`^I add (\d+) of "([^"]*)" with key "([^"]*)"$`, func(q int, item, key string) error {
		return w.send("POST", w.cartPath("/lines"), key, fmt.Sprintf(`{"catalogItemId":%q,"quantity":%d}`, item, q))
	})
	sc.Step(`^I merge the second cart into the first cart$`, func() error {
		return w.post("/carts/"+w.second+"/merges", fmt.Sprintf(`{"targetCartId":%q}`, w.first))
	})
	sc.Step(`^I merge the first cart into the second cart with key "([^"]*)"$`, func(key string) error {
		return w.send("POST", w.cartPath("/merges"), key, fmt.Sprintf(`{"targetCartId":%q}`, w.second))
	})
	sc.Step(`^I merge the first cart into the third cart$`, func() error {
		return w.post(w.cartPath("/merges"), fmt.Sprintf(`{"targetCartId":%q}`, w.doc.third))
	})
	sc.Step(`^I merge the second cart into the third cart$`, func() error {
		return w.post("/carts/"+w.second+"/merges", fmt.Sprintf(`{"targetCartId":%q}`, w.doc.third))
	})
	sc.Step(`^I merge the first cart into the unknown cart "([^"]*)"$`, func(id string) error {
		return w.post(w.cartPath("/merges"), fmt.Sprintf(`{"targetCartId":%q}`, id))
	})
	sc.Step(`^I remember the cart version$`, func() error {
		v, err := w.firstCartVersion()
		w.doc.version = v
		return err
	})
	sc.Step(`^the cart version is unchanged$`, func() error {
		v, err := w.firstCartVersion()
		if err == nil && v != w.doc.version {
			err = fmt.Errorf("cart version = %d, was %d", v, w.doc.version)
		}
		return err
	})
}

func (w *world) firstCartVersion() (int64, error) {
	if err := w.get(w.cartPath("")); err != nil {
		return 0, err
	}
	v, ok := w.lookup("version")
	f, isNum := v.(float64)
	if !ok || !isNum {
		return 0, fmt.Errorf("no version in %s", w.body)
	}
	return int64(f), nil
}

// registerRuleClosing registers the steps that close a cart in each of the
// three terminal states and then try every command on it.
func registerRuleClosing(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the cart has been closed as "([^"]*)"$`, w.closeFirst)
	sc.Step(`^I try to (.+) on the closed cart$`, w.tryOnClosed)
	sc.Step(`^checkout completes the second cart with session "([^"]*)"$`, func(session string) error {
		return w.consumer().ApplyCheckoutCompleted(context.Background(), w.meta(), usecases.CheckoutCompleted{CartID: w.second, SessionID: session})
	})
}

func (w *world) closeFirst(state string) error {
	ctx := context.Background()
	switch state {
	case "CHECKED_OUT":
		return w.consumer().ApplyCheckoutCompleted(ctx, w.meta(), usecases.CheckoutCompleted{CartID: w.first, SessionID: "sess-close"})
	case "ABANDONED":
		w.clock.Advance(31 * 24 * time.Hour)
		_, err := usecases.AbandonInactive{Deps: w.deps}.Execute(ctx, 100)
		return err
	case "MERGED":
		if err := w.newCart(&w.doc.third, "closer-1"); err != nil {
			return err
		}
		return w.mustPost(w.cartPath("/merges"), fmt.Sprintf(`{"targetCartId":%q}`, w.doc.third))
	}
	return fmt.Errorf("unknown closing state %q", state)
}

// tryOnClosed sends the named command to the first cart; every command of the
// model is on the list so that a typo in a feature fails instead of passing.
func (w *world) tryOnClosed(command string) error {
	switch command {
	case "add a line":
		return w.post(w.cartPath("/lines"), `{"catalogItemId":"sku-1","quantity":1}`)
	case "set a quantity":
		return w.put(w.cartPath("/lines/sku-1"), `{"quantity":3}`)
	case "remove a line":
		return w.del(w.cartPath("/lines/sku-1"))
	case "set the destination":
		return w.put(w.cartPath("/destination"), `{"destinationPostalPrefix":"787"}`)
	case "quote delivery":
		return w.post(w.cartPath("/delivery-quotes"), "")
	case "select a delivery":
		return w.put(w.cartPath("/delivery-selection"), `{"groupNo":1,"offerId":"of-1"}`)
	case "accept requoted offers":
		return w.acceptSpec([]string{"re-1/1/NEXT_DAY"})
	case "merge into another cart":
		if err := w.newCart(&w.second, "shopper-9"); err != nil {
			return err
		}
		return w.post(w.cartPath("/merges"), fmt.Sprintf(`{"targetCartId":%q}`, w.second))
	}
	return fmt.Errorf("unknown command %q", command)
}

var offerIDSpecRe = regexp.MustCompile(`"([^"/]+/\d+/\w+)"`)

// acceptSpec posts a requote of offers given as id/group/tier, each valid for
// 30 minutes and delivering on 2026-10-27..28.
func (w *world) acceptSpec(specs []string) error {
	offers := make([]map[string]any, 0, len(specs))
	for _, s := range specs {
		parts := strings.Split(s, "/")
		if len(parts) != 3 {
			return fmt.Errorf("offer spec %q is not id/group/tier", s)
		}
		group, err := strconv.Atoi(parts[1])
		if err != nil {
			return err
		}
		offers = append(offers, map[string]any{
			"offerId": parts[0], "groupNo": group, "tier": parts[2], "deliverByEarliest": "2026-10-27", "deliverByLatest": "2026-10-28",
			"expiresAt": w.clock.Now().Add(30 * time.Minute).Format(time.RFC3339),
		})
	}
	b, err := json.Marshal(map[string]any{"offers": offers})
	if err != nil {
		return err
	}
	return w.post(w.cartPath("/delivery-requote-acceptances"), string(b))
}

// registerRulePolicies registers the clock-driven policies, run one at a time.
func registerRulePolicies(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the AbandonInactive policy runs$`, func() error {
		_, err := usecases.AbandonInactive{Deps: w.deps}.Execute(context.Background(), 100)
		return err
	})
	sc.Step(`^the PurgeAbandonedCarts policy runs$`, func() error {
		_, err := usecases.PurgeAbandonedCarts{Deps: w.deps}.Execute(context.Background(), 100)
		return err
	})
	sc.Step(`^(\d+) days?, (\d+) hours? and (\d+) minutes? pass$`, func(d, h, m int) error {
		w.clock.Advance(time.Duration(d)*24*time.Hour + time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
		return nil
	})
	sc.Step(`^(\d+) hours? pass$`, func(h int) error {
		w.clock.Advance(time.Duration(h) * time.Hour)
		return nil
	})
}
