package cart

import (
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Tier is a delivery service tier.
type Tier string

// The tiers.
const (
	TierSameDay  Tier = "SAME_DAY"
	TierNextDay  Tier = "NEXT_DAY"
	TierTwoDay   Tier = "TWO_DAY"
	TierStandard Tier = "STANDARD"
)

// Valid reports whether t is one of the four tiers.
func (t Tier) Valid() bool {
	return t == TierSameDay || t == TierNextDay || t == TierTwoDay || t == TierStandard
}

// DateLayout is the layout of the delivery-by dates (calendar dates).
const DateLayout = "2006-01-02"

// QuotedOffer is one offer delivery-promise returned. A quote holds no
// capacity: the offer is only something the shopper may select.
type QuotedOffer struct {
	OfferID           string
	GroupNo           int
	Tier              Tier
	DeliverByEarliest string
	DeliverByLatest   string
	ExpiresAt         time.Time
}

// QuoteGroup is one shipment group: the cart lines (1-based, cart order) it
// covers and the offers for it.
type QuoteGroup struct {
	GroupNo int
	LineNos []int
	Offers  []QuotedOffer
}

// TierUnavailable reports why a tier has no offer; GroupNo 0 means the whole quote.
type TierUnavailable struct {
	Tier       Tier
	Constraint string
	GroupNo    int
}

// DeliveryQuote is the set of offers delivery-promise returned for the cart.
type DeliveryQuote struct {
	Groups      []QuoteGroup
	Unavailable []TierUnavailable
	QuotedAt    time.Time
}

// Selection is the offer the shopper picked for one group.
type Selection struct {
	GroupNo int
	OfferID string
}

func (o QuotedOffer) validate() error {
	if !shared.ValidID(o.OfferID) {
		return shared.Invalid("offerId must be a valid id")
	}
	if o.GroupNo < 1 {
		return shared.Invalid("offer %q: groupNo must be at least 1", o.OfferID)
	}
	if !o.Tier.Valid() {
		return shared.Invalid("offer %q: tier %q is unknown", o.OfferID, o.Tier)
	}
	early, err := time.Parse(DateLayout, o.DeliverByEarliest)
	if err != nil {
		return shared.Invalid("offer %q: deliverByEarliest must be a date", o.OfferID)
	}
	late, err := time.Parse(DateLayout, o.DeliverByLatest)
	if err != nil || late.Before(early) {
		return shared.Invalid("offer %q: deliverByLatest must be a date not before deliverByEarliest", o.OfferID)
	}
	if o.ExpiresAt.IsZero() {
		return shared.Invalid("offer %q: expiresAt is required", o.OfferID)
	}
	return nil
}

func (o QuotedOffer) normalized() QuotedOffer {
	o.ExpiresAt = shared.UTC(o.ExpiresAt)
	return o
}

// validate checks a quote against the number of cart lines.
func (q DeliveryQuote) validate(lineCount int) error {
	if q.QuotedAt.IsZero() {
		return shared.Invalid("quotedAt is required")
	}
	groups := map[int]bool{}
	offers := map[string]bool{}
	covered := map[int]bool{}
	for _, g := range q.Groups {
		if g.GroupNo < 1 || groups[g.GroupNo] {
			return shared.Invalid("group numbers start at 1 and are unique")
		}
		groups[g.GroupNo] = true
		if err := g.validate(lineCount, covered, offers); err != nil {
			return err
		}
	}
	for _, u := range q.Unavailable {
		if !u.Tier.Valid() || u.Constraint == "" || u.GroupNo < 0 {
			return shared.Invalid("an unavailable tier needs a known tier and a constraint")
		}
	}
	return nil
}

func (g QuoteGroup) validate(lineCount int, covered map[int]bool, offers map[string]bool) error {
	for _, n := range g.LineNos {
		if n < 1 || n > lineCount || covered[n] {
			return shared.Invalid("group %d: line number %d is out of range or in two groups", g.GroupNo, n)
		}
		covered[n] = true
	}
	for _, o := range g.Offers {
		if err := o.validate(); err != nil {
			return err
		}
		if o.GroupNo != g.GroupNo || offers[o.OfferID] {
			return shared.Invalid("offer %q: belongs to another group or repeats", o.OfferID)
		}
		offers[o.OfferID] = true
	}
	return nil
}

func (q DeliveryQuote) clone() *DeliveryQuote {
	out := DeliveryQuote{QuotedAt: shared.UTC(q.QuotedAt), Unavailable: append([]TierUnavailable(nil), q.Unavailable...)}
	for _, g := range q.Groups {
		ng := QuoteGroup{GroupNo: g.GroupNo, LineNos: append([]int(nil), g.LineNos...)}
		for _, o := range g.Offers {
			ng.Offers = append(ng.Offers, o.normalized())
		}
		out.Groups = append(out.Groups, ng)
	}
	return &out
}
