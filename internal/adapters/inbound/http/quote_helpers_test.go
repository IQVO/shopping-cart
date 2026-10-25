package http_test

import (
	"time"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

var errDown = ports.ErrDeliveryPromiseUnavailable

func emptyQuote() cart.DeliveryQuote { return cart.DeliveryQuote{} }

func oneGroupQuote(expires time.Time) cart.DeliveryQuote {
	offer := func(id string, tier cart.Tier) cart.QuotedOffer {
		return cart.QuotedOffer{OfferID: id, GroupNo: 1, Tier: tier, DeliverByEarliest: "2026-10-27", DeliverByLatest: "2026-10-28", ExpiresAt: expires}
	}
	return cart.DeliveryQuote{QuotedAt: t0, Groups: []cart.QuoteGroup{{GroupNo: 1, LineNos: []int{1},
		Offers: []cart.QuotedOffer{offer("o1", cart.TierStandard), offer("o2", cart.TierNextDay)}}}}
}
