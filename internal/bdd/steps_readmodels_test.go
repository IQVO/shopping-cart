package bdd

import (
	"context"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

func registerReadModels(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^item "([^"]*)" is purchasable at (\d+)$`, func(id string, price int) error {
		return w.stock(id, int64(price))
	})
	sc.Step(`^item "([^"]*)" is catalogued but not priced$`, func(id string) error {
		return w.store.Items().MarkCatalogued(context.Background(), id, t0)
	})
	sc.Step(`^item "([^"]*)" is discontinued$`, func(id string) error {
		return w.store.Items().MarkDiscontinued(context.Background(), id, t0)
	})
	sc.Step(`^a price of (\d+) arrives for "([^"]*)" before it is catalogued$`, func(price int, id string) error {
		return w.store.Items().ApplyPrice(context.Background(), id, "USD", int64(price), 1, t0)
	})
	sc.Step(`^a price of (\d+) arrives for "([^"]*)" with version (\d+)$`, func(price int, id string, version int) error {
		return w.store.Items().ApplyPrice(context.Background(), id, "USD", int64(price), int64(version), t0)
	})
	sc.Step(`^"([^"]*)" is catalogued$`, func(id string) error {
		return w.store.Items().MarkCatalogued(context.Background(), id, t0)
	})
	sc.Step(`^the charge schedule charges (\d+) for (\w+) and (\d+) for (\w+) free over (\d+)$`, func(c1 int, t1 string, c2 int, t2 string, free int) error {
		s := estimate.Schedule{Version: 1, Tiers: map[cart.Tier]estimate.TierCharge{
			cart.Tier(t1): {ChargeMinor: int64(c1)}, cart.Tier(t2): {ChargeMinor: int64(c2), FreeOverMinor: int64(free)},
		}}
		_, err := w.store.Schedules().Replace(context.Background(), s)
		return err
	})
	sc.Step(`^the tax table charges (\d+) basis points for prefixes "([^"]*)" to "([^"]*)"$`, func(bp int, from, to string) error {
		_, err := w.store.TaxTables().Replace(context.Background(), estimate.TaxTable{Version: 1, Bands: []estimate.TaxBand{{From: from, To: to, RateBasisPoints: bp}}})
		return err
	})
	sc.Step(`^delivery-promise offers "([^"]*)" (\w+) and "([^"]*)" (\w+) for group 1 expiring in (\d+) minutes$`, func(o1, t1, o2, t2 string, min int) error {
		w.quoter.Respond(w.quote(1, min, offerSpec{o1, t1}, offerSpec{o2, t2}), nil)
		return nil
	})
	sc.Step(`^delivery-promise is unavailable$`, func() error {
		w.quoter.Respond(cart.DeliveryQuote{}, ports.ErrDeliveryPromiseUnavailable)
		return nil
	})
}

func (w *world) stock(id string, price int64) error {
	ctx := context.Background()
	if err := w.store.Items().MarkCatalogued(ctx, id, t0); err != nil {
		return err
	}
	return w.store.Items().ApplyPrice(ctx, id, "USD", price, 1, t0)
}

type offerSpec struct{ id, tier string }

func (w *world) quote(group, minutes int, offers ...offerSpec) cart.DeliveryQuote {
	q := cart.QuoteGroup{GroupNo: group, LineNos: []int{1}}
	for _, o := range offers {
		q.Offers = append(q.Offers, cart.QuotedOffer{
			OfferID: o.id, GroupNo: group, Tier: cart.Tier(o.tier), DeliverByEarliest: "2026-10-27", DeliverByLatest: "2026-10-28",
			ExpiresAt: w.clock.Now().Add(time.Duration(minutes) * time.Minute),
		})
	}
	return cart.DeliveryQuote{QuotedAt: w.clock.Now(), Groups: []cart.QuoteGroup{q}}
}
