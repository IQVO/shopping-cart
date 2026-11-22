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
	// TierConsolidated is delivery-promise's opt-in tier of a customer-chosen
	// delivery day (its ADR 0024). A quote carries it only when the caller asked.
	TierConsolidated Tier = "CONSOLIDATED"
)

// Valid reports whether t is one of the five tiers.
func (t Tier) Valid() bool {
	return t == TierSameDay || t == TierNextDay || t == TierTwoDay || t == TierStandard || t == TierConsolidated
}

// CapacityBand is delivery-promise's quote-time reading of the capacity
// pressure on an offer's window (its ADR 0023). A signal only: it changes no
// offer, window or order, and the network computes no fee from it.
type CapacityBand string

// The bands.
const (
	BandAmple  CapacityBand = "AMPLE"
	BandTight  CapacityBand = "TIGHT"
	BandScarce CapacityBand = "SCARCE"
)

// Valid reports whether b is one of the three bands.
func (b CapacityBand) Valid() bool {
	return b == BandAmple || b == BandTight || b == BandScarce
}

// DateLayout is the layout of the delivery-by dates (calendar dates).
const DateLayout = "2006-01-02"

// QuotedOffer is one offer delivery-promise returned. A quote holds no
// capacity: the offer is only something the shopper may select.
//
// ConsolidationEligible is set only for the tier CONSOLIDATED (nil, unknown,
// otherwise; never false by default). CapacityBand is empty when
// delivery-promise gave none.
type QuotedOffer struct {
	OfferID               string
	GroupNo               int
	Tier                  Tier
	DeliverByEarliest     string
	DeliverByLatest       string
	ExpiresAt             time.Time
	ConsolidationEligible *bool
	CapacityBand          CapacityBand
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
	if o.ConsolidationEligible != nil && o.Tier != TierConsolidated {
		return shared.Invalid("offer %q: consolidationEligible belongs to the CONSOLIDATED tier only", o.OfferID)
	}
	if o.CapacityBand != "" && !o.CapacityBand.Valid() {
		return shared.Invalid("offer %q: capacityBand %q is unknown", o.OfferID, o.CapacityBand)
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
	if o.ConsolidationEligible != nil {
		v := *o.ConsolidationEligible // own the flag: the quote must not alias the caller's
		o.ConsolidationEligible = &v
	}
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
