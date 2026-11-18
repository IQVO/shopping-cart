package deliverypromise_test

import (
	"context"
	"os"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// offerJSON is a QUOTED delivery-promise offer of the given tier, with extra
// raw JSON members spliced in.
func offerJSON(id, tier, extra string) string {
	return `{"offerId":"` + id + `","state":"QUOTED","tier":"` + tier + `","groupNo":1,"lines":[{"catalogItemId":"sku-1","quantity":2}],` +
		`"window":{"earliest":"2026-10-28T00:00:00Z","latest":"2026-10-28T00:00:00Z"},"expiresAt":"2026-10-25T13:30:00Z"` + extra + `}`
}

func quoteOf(t *testing.T, offers ...string) cart.DeliveryQuote {
	t.Helper()
	body := `{"offers":[`
	for i, o := range offers {
		if i > 0 {
			body += ","
		}
		body += o
	}
	g, _ := serve(t, 200, "application/json", body+`],"unavailable":[{"tier":"CONSOLIDATED","constraint":"NO_WAVE"}]}`)
	q, err := g.Quote(context.Background(), "k", "787", cartLines[:1])
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestGatewayRelaysTheConsolidatedTierItsEligibilityAndTheCapacityBand(t *testing.T) {
	for _, eligible := range []string{"true", "false"} {
		q := quoteOf(t,
			offerJSON("a", "STANDARD", `,"capacityBand":"SCARCE"`),
			offerJSON("b", "CONSOLIDATED", `,"consolidationEligible":`+eligible+`,"capacityBand":"AMPLE"`))
		a, b := q.Groups[0].Offers[0], q.Groups[0].Offers[1]
		if a.Tier != cart.TierStandard || a.CapacityBand != cart.BandScarce || a.ConsolidationEligible != nil {
			t.Fatalf("standard offer = %+v", a)
		}
		if b.Tier != cart.TierConsolidated || b.CapacityBand != cart.BandAmple || b.ConsolidationEligible == nil || *b.ConsolidationEligible != (eligible == "true") {
			t.Fatalf("consolidated offer = %+v", b)
		}
		if q.Unavailable[0].Tier != cart.TierConsolidated {
			t.Fatalf("unavailable = %+v", q.Unavailable)
		}
	}
}

func TestGatewayLeavesAnOfferWithoutTheOptionalFieldsUntouched(t *testing.T) {
	q := quoteOf(t, offerJSON("a", "STANDARD", ""), offerJSON("b", "CONSOLIDATED", ""))
	for _, o := range q.Groups[0].Offers {
		if o.ConsolidationEligible != nil || o.CapacityBand != "" {
			t.Fatalf("%+v", o)
		}
	}
}

func TestGatewayDropsWhatItMustNotRelay(t *testing.T) {
	// eligibility is defined for CONSOLIDATED only; a band added later is a signal this service does not know yet.
	q := quoteOf(t, offerJSON("a", "STANDARD", `,"consolidationEligible":true,"capacityBand":"FULL"`))
	o := q.Groups[0].Offers[0]
	if o.ConsolidationEligible != nil || o.CapacityBand != "" || o.OfferID != "a" {
		t.Fatalf("%+v", o)
	}
}

func TestGatewayDoesNotAliasTheDecodedEligibilityFlag(t *testing.T) {
	q := quoteOf(t, offerJSON("a", "CONSOLIDATED", `,"consolidationEligible":true`))
	if got := q.Groups[0].Offers[0].ConsolidationEligible; got == nil || !*got {
		t.Fatalf("%v", got)
	}
}

// The pinned producer contract is what the relay is built on: the tier, the
// optional band and the optional eligibility must stay declared in it.
func TestTheSpecCopyDeclaresTheConsolidatedTierAndTheOptionalOfferFields(t *testing.T) {
	raw, err := os.ReadFile("testdata/delivery-promise.openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Components struct {
			Schemas map[string]struct {
				Enum       []string       `yaml:"enum"`
				Required   []string       `yaml:"required"`
				Properties map[string]any `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	sch := s.Components.Schemas
	for _, tier := range []cart.Tier{cart.TierSameDay, cart.TierNextDay, cart.TierTwoDay, cart.TierStandard, cart.TierConsolidated} {
		if !slices.Contains(sch["Tier"].Enum, string(tier)) {
			t.Errorf("the spec copy's Tier lacks %s", tier)
		}
	}
	for _, band := range []cart.CapacityBand{cart.BandAmple, cart.BandTight, cart.BandScarce} {
		if !slices.Contains(sch["CapacityBand"].Enum, string(band)) {
			t.Errorf("the spec copy's CapacityBand lacks %s", band)
		}
	}
	for _, f := range []string{"capacityBand", "consolidationEligible"} {
		if _, ok := sch["DeliveryOffer"].Properties[f]; !ok {
			t.Errorf("DeliveryOffer does not declare %s", f)
		}
		if slices.Contains(sch["DeliveryOffer"].Required, f) {
			t.Errorf("%s must stay optional", f)
		}
	}
}
