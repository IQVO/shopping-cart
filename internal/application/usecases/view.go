package usecases

import (
	"context"

	"github.com/IQVO/shopping-cart/internal/application/readmodel"
	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

// view renders a cart with its labelled estimate (EstimateView) and its
// checkout readiness at the use case's clock.
func (d Deps) view(ctx context.Context, c *cart.Cart) (views.Cart, error) {
	est, err := d.estimateFor(ctx, c)
	if err != nil {
		return views.Cart{}, err
	}
	return views.Cart{
		CartID: string(c.ID()), ShopperID: c.ShopperID(), State: c.State(), Lines: c.Lines(),
		DestinationPostalPrefix: c.DestinationPostalPrefix(), DeliveryQuote: c.Quote(), Selections: c.Selections(),
		Estimate: est, Readiness: c.Readiness(d.now()), Version: c.Version(), LastChangedAt: c.LastChangedAt(),
	}, nil
}

func (d Deps) estimateFor(ctx context.Context, c *cart.Cart) (estimate.Estimate, error) {
	lines := c.Lines()
	ids := make([]string, len(lines))
	for i, l := range lines {
		ids[i] = l.CatalogItemID
	}
	items, err := d.Items.GetMany(ctx, ids)
	if err != nil {
		return estimate.Estimate{}, err
	}
	sched, err := optional(d.Schedules.Current(ctx))
	if err != nil {
		return estimate.Estimate{}, err
	}
	table, err := optional(d.TaxTables.Current(ctx))
	if err != nil {
		return estimate.Estimate{}, err
	}
	prices, currency := priceMap(items, d.Currency)
	return estimate.Compute(estimate.Input{
		Currency: currency, Lines: lines, Prices: prices, Destination: c.DestinationPostalPrefix(),
		Groups: estimateGroups(c), Schedule: sched, TaxTable: table,
	}), nil
}

// optional turns a port miss into a nil pointer: the view is not populated yet.
func optional[T any](v T, err error) (*T, error) {
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func priceMap(items map[string]readmodel.Item, fallback string) (map[string]int64, string) {
	prices := map[string]int64{}
	currency := fallback
	for id, it := range items {
		if it.Priced {
			prices[id] = it.PriceMinor
			currency = it.Currency
		}
	}
	return prices, currency
}

func estimateGroups(c *cart.Cart) []estimate.Group {
	q := c.Quote()
	if q == nil {
		return nil
	}
	selected := map[int]string{}
	for _, s := range c.Selections() {
		selected[s.GroupNo] = s.OfferID
	}
	out := make([]estimate.Group, 0, len(q.Groups))
	for _, g := range q.Groups {
		eg := estimate.Group{GroupNo: g.GroupNo, LineNos: g.LineNos}
		for _, o := range g.Offers {
			if o.OfferID == selected[g.GroupNo] {
				eg.Tier, eg.Selected = o.Tier, true
			}
		}
		out = append(out, eg)
	}
	return out
}
