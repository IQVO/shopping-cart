package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

func jsonQuote(offers ...cart.QuotedOffer) *cart.DeliveryQuote {
	return &cart.DeliveryQuote{QuotedAt: time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC),
		Groups: []cart.QuoteGroup{{GroupNo: 1, LineNos: []int{1}, Offers: offers}}}
}

func jsonOffer(id string, tier cart.Tier) cart.QuotedOffer {
	return cart.QuotedOffer{OfferID: id, GroupNo: 1, Tier: tier, DeliverByEarliest: "2026-10-27", DeliverByLatest: "2026-10-28",
		ExpiresAt: time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)}
}

func TestQuoteJSONRoundTripsTheConsolidatedOfferFields(t *testing.T) {
	for _, eligible := range []bool{true, false} {
		cons := jsonOffer("o2", cart.TierConsolidated)
		cons.ConsolidationEligible, cons.CapacityBand = &eligible, cart.BandScarce
		raw, err := marshalQuote(jsonQuote(jsonOffer("o1", cart.TierStandard), cons))
		if err != nil {
			t.Fatal(err)
		}
		back, err := unmarshalQuote(raw)
		if err != nil {
			t.Fatal(err)
		}
		offers := back.Groups[0].Offers
		if offers[1].Tier != cart.TierConsolidated || offers[1].ConsolidationEligible == nil || *offers[1].ConsolidationEligible != eligible || offers[1].CapacityBand != cart.BandScarce {
			t.Fatalf("consolidated offer = %+v", offers[1])
		}
		if offers[0].ConsolidationEligible != nil || offers[0].CapacityBand != "" {
			t.Fatalf("classic offer = %+v", offers[0])
		}
	}
}

// A cart stored before the CONSOLIDATED tier has neither key, and an offer
// without them is written exactly as before.
func TestQuoteJSONOfAnOfferWithoutTheOptionalFieldsIsUnchanged(t *testing.T) {
	raw, err := marshalQuote(jsonQuote(jsonOffer("o1", cart.TierStandard)))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); strings.Contains(s, "consolidationEligible") || strings.Contains(s, "capacityBand") {
		t.Fatalf("stored offer = %s", s)
	}
	old := `{"quotedAt":"2026-10-25T10:00:00Z","groups":[{"groupNo":1,"lineNos":[1],"offers":[{"offerId":"o1","groupNo":1,"tier":"STANDARD","deliverByEarliest":"2026-10-27","deliverByLatest":"2026-10-28","expiresAt":"2026-10-25T11:00:00Z"}]}],"unavailable":[]}`
	back, err := unmarshalQuote([]byte(old))
	if err != nil {
		t.Fatal(err)
	}
	if o := back.Groups[0].Offers[0]; o.ConsolidationEligible != nil || o.CapacityBand != "" || o.Tier != cart.TierStandard {
		t.Fatalf("%+v", o)
	}
}
