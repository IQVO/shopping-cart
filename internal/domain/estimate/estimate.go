// Package estimate computes the labelled, non-binding Estimate shown in the
// cart: effective prices plus the selected tiers' delivery charges plus the
// destination's tax band. The binding total is the checkout session's
// PriceQuote (gap S2); the arithmetic mirrors pricing's (goods plus delivery,
// tax rounded half up on that base) but this package decides nothing, it only
// reports what is known and says whether that is everything (EstimateComplete).
//
// It imports the cart package for the Line and Tier vocabulary only.
package estimate

import (
	"strings"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

const (
	basisPointsDivisor = 10000
	taxPrefixLength    = 3
)

// TierCharge is the charge of one tier; FreeOverMinor 0 means no threshold.
type TierCharge struct {
	ChargeMinor   int64
	FreeOverMinor int64
}

// Schedule is the current delivery charge schedule (EstimateView).
type Schedule struct {
	Version int64
	Tiers   map[cart.Tier]TierCharge
}

// TaxBand is a basis-point rate for a destination postal-prefix range.
type TaxBand struct {
	From            string
	To              string
	RateBasisPoints int
}

// TaxTable is the current tax table (EstimateView).
type TaxTable struct {
	Version int64
	Bands   []TaxBand
}

// RateFor returns the rate for the first three characters of the (upper-cased)
// destination prefix, 0 when no band covers it.
func (t *TaxTable) RateFor(prefix string) int {
	rate, _ := t.rate(prefix)
	return rate
}

func (t *TaxTable) rate(prefix string) (int, bool) {
	if len(prefix) < taxPrefixLength {
		return 0, false
	}
	p := strings.ToUpper(prefix[:taxPrefixLength])
	for _, b := range t.Bands {
		if p >= b.From && p <= b.To {
			return b.RateBasisPoints, true
		}
	}
	return 0, false
}

// Group is one shipment group of the delivery quote with the tier the shopper
// selected for it (Selected false while no offer is picked).
type Group struct {
	GroupNo  int
	LineNos  []int
	Tier     cart.Tier
	Selected bool
}

// Input is everything an estimate reads.
type Input struct {
	Currency    string
	Lines       []cart.Line
	Prices      map[string]int64
	Destination string
	Groups      []Group
	Schedule    *Schedule
	TaxTable    *TaxTable
}

// Estimate is the non-binding total. Complete is false while anything it
// needs is unknown: the figures then cover only what is known.
type Estimate struct {
	Currency      string
	ItemsMinor    int64
	DeliveryMinor int64
	TaxMinor      int64
	TotalMinor    int64
	Complete      bool
}

// Compute builds the estimate. Without a destination only the item subtotal
// is shown (decision H6).
func Compute(in Input) Estimate {
	items, priced := goods(in)
	e := Estimate{Currency: in.Currency, ItemsMinor: items, TotalMinor: items}
	if in.Destination == "" {
		return e
	}
	delivery, deliveryKnown := deliveryCharge(in)
	e.DeliveryMinor = delivery
	base := items + delivery
	rate, banded := 0, false
	if in.TaxTable != nil {
		rate, banded = in.TaxTable.rate(in.Destination)
	}
	e.TaxMinor = (base*int64(rate) + basisPointsDivisor/2) / basisPointsDivisor
	e.TotalMinor = base + e.TaxMinor
	e.Complete = priced && deliveryKnown && banded
	return e
}

// goods sums price times quantity over the priced lines.
func goods(in Input) (total int64, allPriced bool) {
	allPriced = true
	for _, l := range in.Lines {
		p, ok := in.Prices[l.CatalogItemID]
		if !ok {
			allPriced = false
			continue
		}
		total += p * int64(l.Quantity)
	}
	return total, allPriced
}

// deliveryCharge sums the selected groups' charges; known is false when the
// quote has no group, a group is unselected, or the schedule is unknown.
func deliveryCharge(in Input) (total int64, known bool) {
	if len(in.Groups) == 0 || in.Schedule == nil {
		return 0, false
	}
	known = true
	for _, g := range in.Groups {
		if !g.Selected {
			known = false
			continue
		}
		tc, ok := in.Schedule.Tiers[g.Tier]
		if !ok {
			known = false
			continue
		}
		if tc.FreeOverMinor > 0 && groupGoods(in, g) >= tc.FreeOverMinor {
			continue
		}
		total += tc.ChargeMinor
	}
	return total, known
}

func groupGoods(in Input, g Group) int64 {
	var sum int64
	for _, n := range g.LineNos {
		if n < 1 || n > len(in.Lines) {
			continue
		}
		l := in.Lines[n-1]
		sum += in.Prices[l.CatalogItemID] * int64(l.Quantity)
	}
	return sum
}
