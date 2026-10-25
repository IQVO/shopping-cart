package cart

import (
	"sort"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// AcceptRequotedOffers writes the offers of a checkout requote (one per
// group) into the cart: the set REPLACES the delivery quote and every offer is
// the selection of its group, WITHOUT any call to delivery-promise (checkout
// re-verifies each offer before authorizing). A group keeps the line numbers
// of the previous quote's group of the same number; a single new group with
// no predecessor spans every line. Raises no event.
func (c *Cart) AcceptRequotedOffers(offers []QuotedOffer, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if len(offers) == 0 {
		return shared.Invalid("at least one requoted offer is required")
	}
	byGroup := map[int]QuotedOffer{}
	ids := map[string]bool{}
	for _, o := range offers {
		if err := o.validate(); err != nil {
			return err
		}
		if _, dup := byGroup[o.GroupNo]; dup || ids[o.OfferID] {
			return shared.Invalid("requoted offers must be one per group with distinct offerIds")
		}
		byGroup[o.GroupNo] = o.normalized()
		ids[o.OfferID] = true
	}
	quote := DeliveryQuote{QuotedAt: shared.UTC(now)}
	for no, o := range byGroup {
		quote.Groups = append(quote.Groups, QuoteGroup{GroupNo: no, LineNos: c.requoteLineNos(no, len(byGroup)), Offers: []QuotedOffer{o}})
	}
	sort.Slice(quote.Groups, func(i, j int) bool { return quote.Groups[i].GroupNo < quote.Groups[j].GroupNo })
	c.quote = &quote
	c.selections = nil
	for _, g := range quote.Groups {
		c.setSelection(Selection{GroupNo: g.GroupNo, OfferID: g.Offers[0].OfferID})
	}
	c.touch(now)
	return nil
}

func (c *Cart) requoteLineNos(groupNo, groups int) []int {
	if c.quote != nil {
		for _, g := range c.quote.Groups {
			if g.GroupNo == groupNo {
				return append([]int(nil), g.LineNos...)
			}
		}
	}
	if groups != 1 {
		return nil
	}
	all := make([]int, len(c.lines))
	for i := range all {
		all[i] = i + 1
	}
	return all
}
