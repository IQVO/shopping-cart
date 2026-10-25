package deliverypromise

import (
	"sort"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// toQuote maps the answer to a cart quote. Only QUOTED offers are kept; a
// shipment group is named by the offers (and constraints) that carry its
// groupNo, and its line numbers (1-based, cart order) are the cart lines whose
// item appears in the group's offers. A group known only through a constraint
// spans every line when it is the only group, else no known line.
func (g *Gateway) toQuote(resp quoteResponse, lines []cart.Line) cart.DeliveryQuote {
	groups := map[int]*cart.QuoteGroup{}
	items := map[int]map[string]bool{}
	var quotedAt time.Time
	ensure := func(no int) *cart.QuoteGroup {
		if groups[no] == nil {
			groups[no] = &cart.QuoteGroup{GroupNo: no}
			items[no] = map[string]bool{}
		}
		return groups[no]
	}
	for _, o := range resp.Offers {
		if o.State != "" && o.State != "QUOTED" {
			continue
		}
		grp := ensure(o.GroupNo)
		grp.Offers = append(grp.Offers, cart.QuotedOffer{
			OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: cart.Tier(o.Tier),
			DeliverByEarliest: o.Window.Earliest.UTC().Format(cart.DateLayout), DeliverByLatest: o.Window.Latest.UTC().Format(cart.DateLayout),
			ExpiresAt: o.ExpiresAt.UTC(),
		})
		for _, l := range o.Lines {
			items[o.GroupNo][l.CatalogItemID] = true
		}
		if quotedAt.IsZero() || (!o.QuotedAt.IsZero() && o.QuotedAt.Before(quotedAt)) {
			quotedAt = o.QuotedAt
		}
	}
	quote := cart.DeliveryQuote{QuotedAt: quotedAt.UTC()}
	if quotedAt.IsZero() {
		quote.QuotedAt = g.now().UTC()
	}
	for _, u := range resp.Unavailable {
		if u.GroupNo > 0 {
			ensure(u.GroupNo)
		}
		quote.Unavailable = append(quote.Unavailable, cart.TierUnavailable{Tier: cart.Tier(u.Tier), Constraint: u.Constraint, GroupNo: u.GroupNo})
	}
	nos := make([]int, 0, len(groups))
	for no := range groups {
		nos = append(nos, no)
	}
	sort.Ints(nos)
	for _, no := range nos {
		grp := groups[no]
		grp.LineNos = lineNos(lines, items[no], len(nos) == 1)
		quote.Groups = append(quote.Groups, *grp)
	}
	return quote
}

func lineNos(lines []cart.Line, items map[string]bool, only bool) []int {
	var out []int
	for i, l := range lines {
		if items[l.CatalogItemID] || (only && len(items) == 0) {
			out = append(out, i+1)
		}
	}
	return out
}
