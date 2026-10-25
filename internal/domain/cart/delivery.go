package cart

import (
	"sort"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Quote returns a copy of the delivery quote, nil when there is none.
func (c *Cart) Quote() *DeliveryQuote {
	if c.quote == nil {
		return nil
	}
	return c.quote.clone()
}

// Selections returns a copy of the selections ordered by group number.
func (c *Cart) Selections() []Selection { return append([]Selection(nil), c.selections...) }

// CheckoutReadiness says whether checkout may start from this cart.
type CheckoutReadiness struct {
	Ready             bool
	AllGroupsSelected bool
	NoOfferExpired    bool
}

// Readiness is derived from the cart itself at the given instant: every group
// of the quote is selected and no selected offer has expired. Expiry is a fact
// about the clock, not a stored flag.
func (c *Cart) Readiness(now time.Time) CheckoutReadiness {
	r := CheckoutReadiness{NoOfferExpired: true}
	if c.quote == nil || len(c.quote.Groups) == 0 {
		return r
	}
	r.AllGroupsSelected = true
	for _, g := range c.quote.Groups {
		o, ok := c.selectedOffer(g)
		if !ok {
			r.AllGroupsSelected = false
			continue
		}
		if !now.Before(o.ExpiresAt) {
			r.NoOfferExpired = false
		}
	}
	r.Ready = r.AllGroupsSelected && r.NoOfferExpired
	return r
}

func (c *Cart) selectedOffer(g QuoteGroup) (QuotedOffer, bool) {
	for _, s := range c.selections {
		if s.GroupNo != g.GroupNo {
			continue
		}
		for _, o := range g.Offers {
			if o.OfferID == s.OfferID {
				return o, true
			}
		}
	}
	return QuotedOffer{}, false
}

// ValidPrefix reports whether s is a destination postal prefix: 1 to
// MaxPrefixLen letters or digits (a prefix, never an address).
func ValidPrefix(s string) bool {
	if s == "" || len(s) > MaxPrefixLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < '0' || ch > '9' && ch < 'A' || ch > 'Z' && ch < 'a' || ch > 'z' {
			return false
		}
	}
	return true
}

// SetDestination sets the destination postal prefix; it clears the quote and
// the selections and raises no event.
func (c *Cart) SetDestination(prefix string, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if !ValidPrefix(prefix) {
		return shared.Invalid("destinationPostalPrefix must be 1 to %d letters or digits", MaxPrefixLen)
	}
	c.destination = prefix
	c.clearQuote()
	c.touch(now)
	return nil
}

// QuoteDelivery holds the quote delivery-promise returned for the cart's
// lines and destination. A quote holds no capacity. Raises CartDeliveryQuoted;
// drops the selections of any previous quote.
func (c *Cart) QuoteDelivery(q DeliveryQuote, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if c.destination == "" {
		return ErrNoDestination
	}
	if err := q.validate(len(c.lines)); err != nil {
		return err
	}
	c.quote = q.clone()
	c.selections = nil
	at := c.touch(now)
	c.pending = append(c.pending, CartDeliveryQuoted{
		Header: shared.Header{Version: c.version, At: at}, CartID: c.id,
		DestinationPostalPrefix: c.destination, GroupCount: len(q.Groups),
	})
	return nil
}

// SelectDelivery picks an offer of the current quote for its group; an
// earlier pick for the group is replaced. Raises no event.
func (c *Cart) SelectDelivery(groupNo int, offerID string, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if !c.quote.hasOffer(groupNo, offerID) {
		return ErrStaleDeliverySelection
	}
	c.setSelection(Selection{GroupNo: groupNo, OfferID: offerID})
	c.touch(now)
	return nil
}

func (q *DeliveryQuote) hasOffer(groupNo int, offerID string) bool {
	if q == nil {
		return false
	}
	for _, g := range q.Groups {
		if g.GroupNo != groupNo {
			continue
		}
		for _, o := range g.Offers {
			if o.OfferID == offerID {
				return true
			}
		}
	}
	return false
}

func (c *Cart) setSelection(s Selection) {
	for i := range c.selections {
		if c.selections[i].GroupNo == s.GroupNo {
			c.selections[i] = s
			return
		}
	}
	c.selections = append(c.selections, s)
	sort.Slice(c.selections, func(i, j int) bool { return c.selections[i].GroupNo < c.selections[j].GroupNo })
}
