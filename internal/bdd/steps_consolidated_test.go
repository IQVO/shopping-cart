package bdd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/cucumber/godog"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// registerConsolidated registers the steps of features/consolidated_tier.feature:
// delivery-promise's opt-in CONSOLIDATED tier and the capacity band an offer
// may carry.
func registerConsolidated(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^delivery-promise offers "([^"]*)" STANDARD and "([^"]*)" CONSOLIDATED eligible (true|false) in band (\w+) for group 1 expiring in (\d+) minutes$`,
		func(std, cons string, eligible string, band string, minutes int) error {
			q := w.quote(1, minutes, offerSpec{std, "STANDARD"}, offerSpec{cons, "CONSOLIDATED"})
			v, err := strconv.ParseBool(eligible)
			if err != nil {
				return err
			}
			q.Groups[0].Offers[1].ConsolidationEligible = &v
			q.Groups[0].Offers[1].CapacityBand = cart.CapacityBand(band)
			w.quoter.Respond(q, nil)
			return nil
		})
	sc.Step(`^I accept the requoted offer "([^"]*)" CONSOLIDATED eligible (true|false) in band (\w+) for group (\d+) expiring in (\d+) minutes$`,
		func(offer, eligible, band string, group, minutes int) error {
			exp := w.clock.Now().Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
			return w.post(w.cartPath("/delivery-requote-acceptances"), fmt.Sprintf(
				`{"offers":[{"offerId":%q,"groupNo":%d,"tier":"CONSOLIDATED","deliverByEarliest":"2026-10-27","deliverByLatest":"2026-10-28","expiresAt":%q,"consolidationEligible":%s,"capacityBand":%q}]}`,
				offer, group, exp, eligible, band))
		})
	sc.Step(`^I accept the requoted offer "([^"]*)" STANDARD eligible (true|false) for group (\d+) expiring in (\d+) minutes$`,
		func(offer, eligible string, group, minutes int) error {
			exp := w.clock.Now().Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339)
			return w.post(w.cartPath("/delivery-requote-acceptances"), fmt.Sprintf(
				`{"offers":[{"offerId":%q,"groupNo":%d,"tier":"STANDARD","deliverByEarliest":"2026-10-27","deliverByLatest":"2026-10-28","expiresAt":%q,"consolidationEligible":%s}]}`,
				offer, group, exp, eligible))
		})
}
