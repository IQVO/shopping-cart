package cart

import (
	"errors"
	"testing"
)

func TestSetDestinationStoresThePrefixAndClearsTheQuote(t *testing.T) {
	c := quoted(t)
	if err := c.SetDestination("100", at(5)); err != nil {
		t.Fatal(err)
	}
	if c.DestinationPostalPrefix() != "100" || c.Quote() != nil || len(c.Selections()) != 0 || len(c.PullEvents()) != 0 {
		t.Fatal("prefix stored, quote and selections cleared, no event")
	}
}

func TestSetDestinationValidatesThePrefix(t *testing.T) {
	for _, p := range []string{"", " 78", "12345678901", "78 7", "78-"} {
		if err := newCart(t).SetDestination(p, at(1)); err == nil {
			t.Fatalf("prefix %q must be refused", p)
		}
	}
	for _, p := range []string{"7", "787", "1234567890", "SW1A"} {
		if err := newCart(t).SetDestination(p, at(1)); err != nil {
			t.Fatalf("prefix %q must be accepted: %v", p, err)
		}
	}
}

func TestQuoteDeliveryNeedsADestination(t *testing.T) {
	c := withLine(t, newCart(t), "item-a", 1)
	q := DeliveryQuote{QuotedAt: at(2)}
	if err := c.QuoteDelivery(q, at(2)); !errors.Is(err, ErrNoDestination) {
		t.Fatalf("got %v, want ErrNoDestination", err)
	}
}

func TestQuoteDeliveryHoldsTheQuoteAndRaisesTheEvent(t *testing.T) {
	c := withLine(t, newCart(t), "item-a", 1)
	_ = c.SetDestination("787", at(2))
	q := DeliveryQuote{QuotedAt: at(3), Groups: []QuoteGroup{{GroupNo: 1, LineNos: []int{1}, Offers: []QuotedOffer{offer("o1", 1, TierStandard, at(30))}}}}
	if err := c.QuoteDelivery(q, at(3)); err != nil {
		t.Fatal(err)
	}
	if got := c.Quote(); got == nil || len(got.Groups) != 1 || got.Groups[0].Offers[0].OfferID != "o1" {
		t.Fatalf("quote = %+v", got)
	}
	ev := c.PullEvents()
	e, ok := ev[0].(CartDeliveryQuoted)
	if len(ev) != 1 || !ok || e.DestinationPostalPrefix != "787" || e.GroupCount != 1 || e.CartID != "cart-1" {
		t.Fatalf("events = %+v", ev)
	}
}

func TestQuoteDeliveryReplacesThePreviousQuoteAndSelections(t *testing.T) {
	c := quoted(t)
	_ = c.SelectDelivery(1, "o1", at(4))
	q := DeliveryQuote{QuotedAt: at(5), Groups: []QuoteGroup{{GroupNo: 1, LineNos: []int{1, 2}, Offers: []QuotedOffer{offer("n1", 1, TierStandard, at(40))}}}}
	if err := c.QuoteDelivery(q, at(5)); err != nil {
		t.Fatal(err)
	}
	if len(c.Selections()) != 0 || len(c.Quote().Groups) != 1 {
		t.Fatal("a new quote replaces the old one and drops the selections")
	}
}

func TestQuoteDeliveryRejectsAnInvalidQuote(t *testing.T) {
	bad := map[string]DeliveryQuote{
		"no quotedAt":        {},
		"group 0":            {QuotedAt: at(3), Groups: []QuoteGroup{{GroupNo: 0}}},
		"duplicate group":    {QuotedAt: at(3), Groups: []QuoteGroup{{GroupNo: 1}, {GroupNo: 1}}},
		"line out of range":  {QuotedAt: at(3), Groups: []QuoteGroup{{GroupNo: 1, LineNos: []int{2}}}},
		"line twice":         {QuotedAt: at(3), Groups: []QuoteGroup{{GroupNo: 1, LineNos: []int{1}}, {GroupNo: 2, LineNos: []int{1}}}},
		"offer of other grp": {QuotedAt: at(3), Groups: []QuoteGroup{{GroupNo: 1, Offers: []QuotedOffer{offer("o1", 2, TierStandard, at(9))}}}},
		"repeated offer id": {QuotedAt: at(3), Groups: []QuoteGroup{
			{GroupNo: 1, Offers: []QuotedOffer{offer("o1", 1, TierStandard, at(9))}},
			{GroupNo: 2, Offers: []QuotedOffer{offer("o1", 2, TierStandard, at(9))}}}},
		"bad tier":       {QuotedAt: at(3), Groups: []QuoteGroup{{GroupNo: 1, Offers: []QuotedOffer{offer("o1", 1, "FAST", at(9))}}}},
		"unknown tier u": {QuotedAt: at(3), Unavailable: []TierUnavailable{{Tier: "FAST", Constraint: "X"}}},
		"no constraint":  {QuotedAt: at(3), Unavailable: []TierUnavailable{{Tier: TierStandard}}},
	}
	for name, q := range bad {
		c := withLine(t, newCart(t), "item-a", 1)
		_ = c.SetDestination("787", at(2))
		if err := c.QuoteDelivery(q, at(3)); err == nil {
			t.Fatalf("%s: must be refused", name)
		}
	}
}

func TestOfferFieldsAreValidated(t *testing.T) {
	cases := map[string]func(o *QuotedOffer){
		"bad id":       func(o *QuotedOffer) { o.OfferID = "bad id" },
		"bad earliest": func(o *QuotedOffer) { o.DeliverByEarliest = "tomorrow" },
		"bad latest":   func(o *QuotedOffer) { o.DeliverByLatest = "2026-10-26" },
		"no expiry":    func(o *QuotedOffer) { o.ExpiresAt = timeZero() },
	}
	for name, mutate := range cases {
		o := offer("o1", 1, TierStandard, at(9))
		mutate(&o)
		if err := o.validate(); err == nil {
			t.Fatalf("%s: must be refused", name)
		}
	}
	if err := offer("o1", 1, TierStandard, at(9)).validate(); err != nil {
		t.Fatal(err)
	}
}
