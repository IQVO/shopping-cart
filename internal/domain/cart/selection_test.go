package cart

import (
	"errors"
	"testing"
)

func TestSelectDeliveryNeedsAnOfferOfTheCurrentQuote(t *testing.T) {
	c := quoted(t)
	for name, tc := range map[string]struct {
		group int
		offer string
	}{
		"unknown offer":    {1, "zzz"},
		"offer of group 2": {1, "o3"},
		"unknown group":    {9, "o1"},
	} {
		if err := c.SelectDelivery(tc.group, tc.offer, at(4)); !errors.Is(err, ErrStaleDeliverySelection) {
			t.Fatalf("%s: got %v, want ErrStaleDeliverySelection", name, err)
		}
	}
	if err := withLine(t, newCart(t), "item-a", 1).SelectDelivery(1, "o1", at(4)); !errors.Is(err, ErrStaleDeliverySelection) {
		t.Fatalf("no quote: got %v, want ErrStaleDeliverySelection", err)
	}
}

func TestSelectDeliveryRecordsAndReplacesTheGroupSelection(t *testing.T) {
	c := quoted(t)
	v := c.Version()
	if err := c.SelectDelivery(2, "o3", at(4)); err != nil {
		t.Fatal(err)
	}
	if err := c.SelectDelivery(1, "o1", at(4)); err != nil {
		t.Fatal(err)
	}
	if err := c.SelectDelivery(1, "o2", at(5)); err != nil {
		t.Fatal(err)
	}
	got := c.Selections()
	if len(got) != 2 || got[0] != (Selection{1, "o2"}) || got[1] != (Selection{2, "o3"}) {
		t.Fatalf("selections = %+v, want sorted by group with o2 replacing o1", got)
	}
	if c.Version() != v+3 || len(c.PullEvents()) != 0 {
		t.Fatalf("version %d (from %d), events must be none", c.Version(), v)
	}
}

func TestReadinessNeedsEveryGroupSelectedAndNoExpiredOffer(t *testing.T) {
	c := quoted(t)
	if r := c.Readiness(at(4)); r.Ready || r.AllGroupsSelected || !r.NoOfferExpired {
		t.Fatalf("nothing selected: %+v", r)
	}
	_ = c.SelectDelivery(1, "o1", at(4))
	if r := c.Readiness(at(4)); r.Ready || r.AllGroupsSelected {
		t.Fatalf("one group of two: %+v", r)
	}
	_ = c.SelectDelivery(2, "o3", at(4))
	if r := c.Readiness(at(29)); !r.Ready || !r.AllGroupsSelected || !r.NoOfferExpired {
		t.Fatalf("all selected, nothing expired: %+v", r)
	}
	if r := c.Readiness(at(30)); r.Ready || r.NoOfferExpired || !r.AllGroupsSelected {
		t.Fatalf("expiry instant counts as expired: %+v", r)
	}
}

func TestReadinessOfACartWithoutAQuoteIsNotReady(t *testing.T) {
	r := withLine(t, newCart(t), "item-a", 1).Readiness(at(1))
	if r.Ready || r.AllGroupsSelected {
		t.Fatalf("%+v", r)
	}
	q := DeliveryQuote{QuotedAt: at(3)}
	c := withLine(t, newCart(t), "item-a", 1)
	_ = c.SetDestination("787", at(2))
	_ = c.QuoteDelivery(q, at(3))
	if r := c.Readiness(at(4)); r.Ready || r.AllGroupsSelected {
		t.Fatalf("a quote with no group is never ready: %+v", r)
	}
}

func TestAcceptRequotedOffersWritesTheSelectionWithoutQuoting(t *testing.T) {
	c := quoted(t)
	offers := []QuotedOffer{offer("r1", 1, TierNextDay, at(50)), offer("r2", 2, TierStandard, at(50))}
	if err := c.AcceptRequotedOffers(offers, at(10)); err != nil {
		t.Fatal(err)
	}
	q := c.Quote()
	if len(q.Groups) != 2 || q.Groups[0].LineNos[0] != 1 || q.Groups[1].LineNos[0] != 2 || !q.QuotedAt.Equal(at(10)) {
		t.Fatalf("quote = %+v, lineNos kept from the previous groups", q)
	}
	sel := c.Selections()
	if len(sel) != 2 || sel[0] != (Selection{1, "r1"}) || sel[1] != (Selection{2, "r2"}) {
		t.Fatalf("selections = %+v", sel)
	}
	if len(c.PullEvents()) != 0 || !c.Readiness(at(11)).Ready {
		t.Fatal("no event; the cart is ready")
	}
}

func TestAcceptRequotedOffersOfASingleNewGroupSpansEveryLine(t *testing.T) {
	c := withLine(t, withLine(t, newCart(t), "item-a", 1), "item-b", 1)
	if err := c.AcceptRequotedOffers([]QuotedOffer{offer("r1", 1, TierStandard, at(50))}, at(10)); err != nil {
		t.Fatal(err)
	}
	if got := c.Quote().Groups[0].LineNos; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("lineNos = %v, want [1 2]", got)
	}
}

func TestAcceptRequotedOffersRejectsABadSet(t *testing.T) {
	bad := map[string][]QuotedOffer{
		"empty":         nil,
		"two per group": {offer("r1", 1, TierStandard, at(50)), offer("r2", 1, TierNextDay, at(50))},
		"repeated id":   {offer("r1", 1, TierStandard, at(50)), offer("r1", 2, TierStandard, at(50))},
		"bad tier":      {offer("r1", 1, "FAST", at(50))},
	}
	for name, set := range bad {
		c := quoted(t)
		err := c.AcceptRequotedOffers(set, at(10))
		if err == nil || errors.Is(err, ErrCartClosed) {
			t.Fatalf("%s: got %v, want a validation error", name, err)
		}
		if len(c.Quote().Groups) != 2 {
			t.Fatalf("%s: a refused set must leave the quote alone", name)
		}
	}
}
