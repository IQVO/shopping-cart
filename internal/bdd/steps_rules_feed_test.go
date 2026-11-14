package bdd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cucumber/godog"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// lastEvent returns the newest published event of type T that match accepts.
func lastEvent[T shared.Event](w *world, match func(T) bool) (T, bool) {
	var zero T
	evs := w.store.Outbox().Events()
	for i := len(evs) - 1; i >= 0; i-- {
		if e, ok := evs[i].(T); ok && match(e) {
			return e, true
		}
	}
	return zero, false
}

// registerRuleEvents asserts the payload of the events the cart published.
func registerRuleEvents(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the CartLineAdded event for "([^"]*)" carries quantity (\d+)$`, func(item string, q int) error {
		e, ok := lastEvent(w, func(e cart.CartLineAdded) bool { return e.CatalogItemID == item })
		if !ok || e.Quantity != q {
			return fmt.Errorf("CartLineAdded for %s = %+v (found %v), want quantity %d", item, e, ok, q)
		}
		return nil
	})
	sc.Step(`^the CartDeliveryQuoted event carries prefix "([^"]*)" and (\d+) groups?$`, func(prefix string, n int) error {
		e, ok := lastEvent(w, func(cart.CartDeliveryQuoted) bool { return true })
		if !ok || e.DestinationPostalPrefix != prefix || e.GroupCount != n {
			return fmt.Errorf("CartDeliveryQuoted = %+v (found %v), want prefix %s and %d groups", e, ok, prefix, n)
		}
		return nil
	})
	sc.Step(`^the CartAbandoned event carries lastChangedAt "([^"]*)"$`, func(at string) error {
		e, ok := lastEvent(w, func(cart.CartAbandoned) bool { return true })
		if !ok || e.LastChangedAt.Format("2006-01-02T15:04:05Z") != at {
			return fmt.Errorf("CartAbandoned = %+v (found %v), want lastChangedAt %s", e, ok, at)
		}
		return nil
	})
	sc.Step(`^the CartCheckedOut event carries session "([^"]*)"$`, func(session string) error {
		e, ok := lastEvent(w, func(cart.CartCheckedOut) bool { return true })
		if !ok || e.CheckoutSessionID != session {
			return fmt.Errorf("CartCheckedOut = %+v (found %v), want session %s", e, ok, session)
		}
		return nil
	})
}

// registerRuleFeeds drives the use cases the Kafka consumers call, one step
// per event of another context, with an optional explicit CloudEvents id.
func registerRuleFeeds(sc *godog.ScenarioContext, w *world) {
	ctx := context.Background()
	sc.Step(`^item "([^"]*)" is purchasable at (\d+) in "([A-Z]{3})"$`, func(id string, price int, currency string) error {
		if err := w.store.Items().MarkCatalogued(ctx, id, t0); err != nil {
			return err
		}
		return w.store.Items().ApplyPrice(ctx, id, currency, int64(price), 1, t0)
	})
	sc.Step(`^pricing publishes ItemPriceChanged for "([^"]*)" at (\d+) version (\d+)$`, func(id string, price, version int) error {
		return w.priceChanged(id, price, version, "")
	})
	sc.Step(`^pricing publishes ItemPriceChanged for "([^"]*)" at (\d+) version (\d+) as event "([^"]*)"$`, w.priceChanged)
	sc.Step(`^pricing withdraws the price of "([^"]*)" at version (\d+)$`, func(id string, version int) error {
		return w.consumer().ApplyItemPriceWithdrawn(ctx, w.meta(), usecases.ItemPriceWithdrawn{CatalogItemID: id, Version: int64(version)})
	})
	sc.Step(`^offer-availability announces "([^"]*)"$`, func(id string) error {
		return w.consumer().ApplyCatalogItemCreated(ctx, w.meta(), usecases.CatalogItemCreated{CatalogItemID: id})
	})
	sc.Step(`^offer-availability discontinues "([^"]*)"$`, func(id string) error {
		return w.consumer().ApplyCatalogItemDiscontinued(ctx, w.meta(), usecases.CatalogItemDiscontinued{CatalogItemID: id})
	})
	sc.Step(`^pricing publishes charge schedule version (\d+) charging (\d+) for (\w+)$`, func(version, charge int, tier string) error {
		return w.consumer().ApplyDeliveryChargeScheduleChanged(ctx, w.meta(), usecases.DeliveryChargeScheduleChanged{
			Version: int64(version), Tiers: []usecases.TierCharge{{Tier: tier, ChargeMinor: int64(charge)}},
		})
	})
	sc.Step(`^pricing publishes tax table version (\d+) charging (\d+) basis points for prefixes "([^"]*)" to "([^"]*)"$`, func(version, bp int, from, to string) error {
		return w.consumer().ApplyTaxTableChanged(ctx, w.meta(), usecases.TaxTableChanged{
			Version: int64(version), Bands: []usecases.TaxBand{{PostalPrefixFrom: from, PostalPrefixTo: to, RateBasisPoints: bp}},
		})
	})
	sc.Step(`^CheckoutCompleted event "([^"]*)" names the unknown cart "([^"]*)"$`, func(eventID, cartID string) error {
		return w.checkoutEvent(eventID, cartID, "sess-x")
	})
	sc.Step(`^CheckoutCompleted event "([^"]*)" names the first cart with session "([^"]*)"$`, func(eventID, session string) error {
		return w.checkoutEvent(eventID, w.first, session)
	})
	sc.Step(`^the poison event "([^"]*)" is delivered$`, func(kind string) error {
		w.doc.err = w.deliverPoison(kind)
		return nil
	})
	sc.Step(`^the event is refused as invalid$`, func() error {
		if !errors.Is(w.doc.err, shared.ErrValidation) {
			return fmt.Errorf("the consumer answered %w, want a validation error", w.doc.err)
		}
		return nil
	})
}

func (w *world) priceChanged(id string, price, version int, eventID string) error {
	m := w.meta()
	if eventID != "" {
		m.EventID = eventID
	}
	return w.consumer().ApplyItemPriceChanged(context.Background(), m, usecases.ItemPriceChanged{
		CatalogItemID: id, Currency: "USD", EffectivePriceMinor: int64(price), Version: int64(version),
	})
}

func (w *world) checkoutEvent(eventID, cartID, session string) error {
	m := w.meta()
	m.EventID = eventID
	return w.consumer().ApplyCheckoutCompleted(context.Background(), m, usecases.CheckoutCompleted{CartID: cartID, SessionID: session})
}

// deliverPoison hands the consumer an event that can never be applied and
// returns what it answered.
func (w *world) deliverPoison(kind string) error {
	ctx, c, m := context.Background(), w.consumer(), w.meta()
	switch kind {
	case "CheckoutCompleted without a cartId":
		return c.ApplyCheckoutCompleted(ctx, m, usecases.CheckoutCompleted{SessionID: "sess-1"})
	case "CheckoutCompleted without a sessionId":
		return c.ApplyCheckoutCompleted(ctx, m, usecases.CheckoutCompleted{CartID: w.first})
	case "CatalogItemCreated without a catalogItemId":
		return c.ApplyCatalogItemCreated(ctx, m, usecases.CatalogItemCreated{})
	case "ItemPriceChanged with a negative price":
		return c.ApplyItemPriceChanged(ctx, m, usecases.ItemPriceChanged{CatalogItemID: "sku-1", Currency: "USD", EffectivePriceMinor: -1, Version: 2})
	case "ItemPriceChanged with a lower-case currency":
		return c.ApplyItemPriceChanged(ctx, m, usecases.ItemPriceChanged{CatalogItemID: "sku-1", Currency: "usd", EffectivePriceMinor: 900, Version: 2})
	case "ItemPriceChanged with version 0":
		return c.ApplyItemPriceChanged(ctx, m, usecases.ItemPriceChanged{CatalogItemID: "sku-1", Currency: "USD", EffectivePriceMinor: 900, Version: 0})
	case "ItemPriceWithdrawn with version 0":
		return c.ApplyItemPriceWithdrawn(ctx, m, usecases.ItemPriceWithdrawn{CatalogItemID: "sku-1", Version: 0})
	case "DeliveryChargeScheduleChanged without tiers":
		return c.ApplyDeliveryChargeScheduleChanged(ctx, m, usecases.DeliveryChargeScheduleChanged{Version: 2})
	case "DeliveryChargeScheduleChanged with an unknown tier":
		return c.ApplyDeliveryChargeScheduleChanged(ctx, m, usecases.DeliveryChargeScheduleChanged{Version: 2, Tiers: []usecases.TierCharge{{Tier: "OVERNIGHT", ChargeMinor: 100}}})
	case "DeliveryChargeScheduleChanged with version 0":
		return c.ApplyDeliveryChargeScheduleChanged(ctx, m, usecases.DeliveryChargeScheduleChanged{Tiers: []usecases.TierCharge{{Tier: "NEXT_DAY", ChargeMinor: 100}}})
	case "TaxTableChanged with version 0":
		return c.ApplyTaxTableChanged(ctx, m, usecases.TaxTableChanged{Bands: []usecases.TaxBand{{PostalPrefixFrom: "700", PostalPrefixTo: "799", RateBasisPoints: 500}}})
	case "TaxTableChanged without bands":
		return c.ApplyTaxTableChanged(ctx, m, usecases.TaxTableChanged{Version: 2})
	case "TaxTableChanged with an inverted band":
		return c.ApplyTaxTableChanged(ctx, m, usecases.TaxTableChanged{Version: 2, Bands: []usecases.TaxBand{{PostalPrefixFrom: "800", PostalPrefixTo: "700", RateBasisPoints: 500}}})
	}
	return fmt.Errorf("unknown poison event %q", kind)
}

var offerSpecRe = regexp.MustCompile(`"([^"]+)" (\w+)`)

// registerRuleQuoter scripts delivery-promise group by group and reads back
// what it was asked.
func registerRuleQuoter(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^delivery-promise quotes group (\d+) for line (\d+) with offers? (.*) expiring in (\d+) minutes$`, func(group, line int, offers string, minutes int) error {
		specs := offerSpecRe.FindAllStringSubmatch(offers, -1)
		if len(specs) == 0 {
			return fmt.Errorf("no offers in %q", offers)
		}
		g := cart.QuoteGroup{GroupNo: group, LineNos: []int{line}}
		for _, s := range specs {
			g.Offers = append(g.Offers, w.quote(group, minutes, offerSpec{s[1], s[2]}).Groups[0].Offers[0])
		}
		w.doc.quote.QuotedAt = w.clock.Now()
		w.doc.quote.Groups = append(w.doc.quote.Groups, g)
		w.quoter.Respond(w.doc.quote, nil)
		return nil
	})
	sc.Step(`^delivery-promise reports (\w+) unavailable with the constraint "([^"]*)"$`, func(tier, constraint string) error {
		w.doc.quote.Unavailable = append(w.doc.quote.Unavailable, cart.TierUnavailable{Tier: cart.Tier(tier), Constraint: constraint})
		w.quoter.Respond(w.doc.quote, nil)
		return nil
	})
	sc.Step(`^delivery-promise rejects the request$`, func() error {
		w.quoter.Respond(cart.DeliveryQuote{}, ports.ErrQuoteRejected)
		return nil
	})
	sc.Step(`^delivery-promise was called (\d+) times?$`, func(n int) error {
		if got := len(w.quoter.Calls()); got != n {
			return fmt.Errorf("delivery-promise was called %d times, want %d", got, n)
		}
		return nil
	})
	sc.Step(`^delivery-promise was asked for lines "([^"]*)" at "([^"]*)"$`, func(lines, prefix string) error {
		calls := w.quoter.Calls()
		if len(calls) == 0 {
			return errors.New("delivery-promise was not called")
		}
		last := calls[len(calls)-1]
		var got []string
		for _, l := range last.Lines {
			got = append(got, fmt.Sprintf("%s x%d", l.CatalogItemID, l.Quantity))
		}
		if strings.Join(got, ", ") != lines || last.Destination != prefix {
			return fmt.Errorf("delivery-promise was asked for %v at %q, want %s at %q", got, last.Destination, lines, prefix)
		}
		return nil
	})
	sc.Step(`^delivery-promise received the idempotency key "([^"]*)" (\d+) times?$`, func(key string, n int) error {
		got := 0
		for _, c := range w.quoter.Calls() {
			if c.IdempotencyKey == key {
				got++
			}
		}
		if got != n {
			return fmt.Errorf("delivery-promise received key %q %d times, want %d", key, got, n)
		}
		return nil
	})
}
