package cart

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func newCart(t *testing.T) *Cart {
	t.Helper()
	c, err := Create("cart-1", "shopper-1", t0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return c
}

func withLine(t *testing.T, c *Cart, item string, qty int) *Cart {
	t.Helper()
	if err := c.AddLine(item, qty, true, at(1)); err != nil {
		t.Fatalf("add line: %v", err)
	}
	c.PullEvents()
	return c
}

func offer(id string, group int, tier Tier, expires time.Time) QuotedOffer {
	return QuotedOffer{OfferID: id, GroupNo: group, Tier: tier, DeliverByEarliest: "2026-10-27", DeliverByLatest: "2026-10-28", ExpiresAt: expires}
}

// quoted returns a cart with two lines, a destination and a two-group quote.
func quoted(t *testing.T) *Cart {
	t.Helper()
	c := withLine(t, withLine(t, newCart(t), "item-a", 1), "item-b", 2)
	if err := c.SetDestination("787", at(2)); err != nil {
		t.Fatalf("destination: %v", err)
	}
	q := DeliveryQuote{QuotedAt: at(3), Groups: []QuoteGroup{
		{GroupNo: 1, LineNos: []int{1}, Offers: []QuotedOffer{offer("o1", 1, TierStandard, at(30)), offer("o2", 1, TierNextDay, at(30))}},
		{GroupNo: 2, LineNos: []int{2}, Offers: []QuotedOffer{offer("o3", 2, TierStandard, at(30))}},
	}}
	if err := c.QuoteDelivery(q, at(3)); err != nil {
		t.Fatalf("quote: %v", err)
	}
	c.PullEvents()
	return c
}

func timeZero() time.Time { return time.Time{} }
