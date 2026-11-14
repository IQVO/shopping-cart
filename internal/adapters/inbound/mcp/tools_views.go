package mcp

import (
	"time"

	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// --- outputs (the REST bodies, camelCase like the REST contract) --------------
//
// The views below carry EXACTLY the fields of the REST Cart wire shape
// (apis/openapi.yaml): the MCP surface exposes what GET /carts/{cartId} and
// GET /carts already expose and nothing more. In particular
// views.Cart.LastChangedAt, which REST withholds, is not mapped.

type lineView struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type offerView struct {
	OfferID           string    `json:"offerId"`
	GroupNo           int       `json:"groupNo"`
	Tier              string    `json:"tier"`
	DeliverByEarliest string    `json:"deliverByEarliest"`
	DeliverByLatest   string    `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type groupView struct {
	GroupNo int         `json:"groupNo"`
	LineNos []int       `json:"lineNos"`
	Offers  []offerView `json:"offers"`
}

type unavailableView struct {
	Tier       string `json:"tier"`
	Constraint string `json:"constraint"`
	GroupNo    int    `json:"groupNo,omitempty"`
}

type quoteView struct {
	Groups           []groupView       `json:"groups"`
	UnavailableTiers []string          `json:"unavailableTiers"`
	Unavailable      []unavailableView `json:"unavailable"`
	QuotedAt         time.Time         `json:"quotedAt"`
}

type estimateView struct {
	Currency         string `json:"currency"`
	ItemsMinor       int64  `json:"itemsMinor"`
	DeliveryMinor    int64  `json:"deliveryMinor"`
	TaxMinor         int64  `json:"taxMinor"`
	TotalMinor       int64  `json:"totalMinor"`
	EstimateComplete bool   `json:"estimateComplete"`
}

type readinessView struct {
	Ready             bool `json:"ready"`
	AllGroupsSelected bool `json:"allGroupsSelected"`
	NoOfferExpired    bool `json:"noOfferExpired"`
}

type selectionView struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
}

// cartView is the wire shape of one cart. shopperId and
// destinationPostalPrefix are null until set (a guest cart has no shopper).
type cartView struct {
	CartID                  string          `json:"cartId"`
	ShopperID               *string         `json:"shopperId"`
	State                   string          `json:"state"`
	Lines                   []lineView      `json:"lines"`
	DestinationPostalPrefix *string         `json:"destinationPostalPrefix"`
	DeliveryQuote           *quoteView      `json:"deliveryQuote,omitempty"`
	Selections              []selectionView `json:"selections"`
	Estimate                estimateView    `json:"estimate"`
	CheckoutReadiness       readinessView   `json:"checkoutReadiness"`
	Version                 int64           `json:"version"`
}

// cartListView is the list_shopper_carts result: one page of the shopper's
// carts, newest first, and whether an older cart follows.
type cartListView struct {
	Carts   []cartView `json:"carts"`
	HasMore bool       `json:"hasMore"`
}

// --- mapping ------------------------------------------------------------------

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toOffer(o cart.QuotedOffer) offerView {
	return offerView{
		OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: string(o.Tier),
		DeliverByEarliest: o.DeliverByEarliest, DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt,
	}
}

func toQuote(q *cart.DeliveryQuote) *quoteView {
	if q == nil {
		return nil
	}
	out := &quoteView{QuotedAt: q.QuotedAt, Groups: make([]groupView, len(q.Groups)), UnavailableTiers: []string{}, Unavailable: []unavailableView{}}
	for i, g := range q.Groups {
		out.Groups[i] = groupView{GroupNo: g.GroupNo, LineNos: append([]int{}, g.LineNos...), Offers: make([]offerView, len(g.Offers))}
		for j, o := range g.Offers {
			out.Groups[i].Offers[j] = toOffer(o)
		}
	}
	seen := map[string]bool{}
	for _, u := range q.Unavailable {
		out.Unavailable = append(out.Unavailable, unavailableView{Tier: string(u.Tier), Constraint: u.Constraint, GroupNo: u.GroupNo})
		if !seen[string(u.Tier)] {
			seen[string(u.Tier)] = true
			out.UnavailableTiers = append(out.UnavailableTiers, string(u.Tier))
		}
	}
	return out
}

func toCart(v views.Cart) cartView {
	out := cartView{
		CartID: v.CartID, ShopperID: optional(v.ShopperID), State: string(v.State), Lines: make([]lineView, len(v.Lines)),
		DestinationPostalPrefix: optional(v.DestinationPostalPrefix), DeliveryQuote: toQuote(v.DeliveryQuote),
		Selections: make([]selectionView, len(v.Selections)), Version: v.Version,
		Estimate: estimateView{
			Currency: v.Estimate.Currency, ItemsMinor: v.Estimate.ItemsMinor, DeliveryMinor: v.Estimate.DeliveryMinor,
			TaxMinor: v.Estimate.TaxMinor, TotalMinor: v.Estimate.TotalMinor, EstimateComplete: v.Estimate.Complete,
		},
		CheckoutReadiness: readinessView{Ready: v.Readiness.Ready, AllGroupsSelected: v.Readiness.AllGroupsSelected, NoOfferExpired: v.Readiness.NoOfferExpired},
	}
	for i, l := range v.Lines {
		out.Lines[i] = lineView{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	for i, s := range v.Selections {
		out.Selections[i] = selectionView{GroupNo: s.GroupNo, OfferID: s.OfferID}
	}
	return out
}
