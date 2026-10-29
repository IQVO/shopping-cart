package http

import (
	"time"

	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// Wire shapes of apis/openapi.yaml (camelCase JSON). The domain never sees
// them; these structs are the only place JSON names live.

type lineBody struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type createRequest struct {
	ShopperID string `json:"shopperId"`
}

type quantityRequest struct {
	Quantity int `json:"quantity"`
}

type destinationRequest struct {
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
}

type selectRequest struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
}

type mergeRequest struct {
	TargetCartID string `json:"targetCartId"`
}

type offerBody struct {
	OfferID           string    `json:"offerId"`
	GroupNo           int       `json:"groupNo"`
	Tier              string    `json:"tier"`
	DeliverByEarliest string    `json:"deliverByEarliest"`
	DeliverByLatest   string    `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type requoteRequest struct {
	Offers []offerBody `json:"offers"`
}

type groupBody struct {
	GroupNo int         `json:"groupNo"`
	LineNos []int       `json:"lineNos"`
	Offers  []offerBody `json:"offers"`
}

type unavailableBody struct {
	Tier       string `json:"tier"`
	Constraint string `json:"constraint"`
	GroupNo    int    `json:"groupNo,omitempty"`
}

type quoteBody struct {
	Groups           []groupBody       `json:"groups"`
	UnavailableTiers []string          `json:"unavailableTiers"`
	Unavailable      []unavailableBody `json:"unavailable"`
	QuotedAt         time.Time         `json:"quotedAt"`
}

type estimateBody struct {
	Currency         string `json:"currency"`
	ItemsMinor       int64  `json:"itemsMinor"`
	DeliveryMinor    int64  `json:"deliveryMinor"`
	TaxMinor         int64  `json:"taxMinor"`
	TotalMinor       int64  `json:"totalMinor"`
	EstimateComplete bool   `json:"estimateComplete"`
}

type readinessBody struct {
	Ready             bool `json:"ready"`
	AllGroupsSelected bool `json:"allGroupsSelected"`
	NoOfferExpired    bool `json:"noOfferExpired"`
}

type selectionBody struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
}

type cartBody struct {
	CartID                  string          `json:"cartId"`
	ShopperID               *string         `json:"shopperId"`
	State                   string          `json:"state"`
	Lines                   []lineBody      `json:"lines"`
	DestinationPostalPrefix *string         `json:"destinationPostalPrefix"`
	DeliveryQuote           *quoteBody      `json:"deliveryQuote,omitempty"`
	Selections              []selectionBody `json:"selections"`
	Estimate                estimateBody    `json:"estimate"`
	CheckoutReadiness       readinessBody   `json:"checkoutReadiness"`
	Version                 int64           `json:"version"`
}

type cartListBody struct {
	Carts []cartBody `json:"carts"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func offerOut(o cart.QuotedOffer) offerBody {
	return offerBody{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: string(o.Tier), DeliverByEarliest: o.DeliverByEarliest,
		DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt}
}

func (o offerBody) domain() cart.QuotedOffer {
	return cart.QuotedOffer{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: cart.Tier(o.Tier), DeliverByEarliest: o.DeliverByEarliest,
		DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt}
}

func quoteOut(q *cart.DeliveryQuote) *quoteBody {
	if q == nil {
		return nil
	}
	out := &quoteBody{QuotedAt: q.QuotedAt, Groups: make([]groupBody, len(q.Groups)), UnavailableTiers: []string{}, Unavailable: []unavailableBody{}}
	for i, g := range q.Groups {
		gb := groupBody{GroupNo: g.GroupNo, LineNos: append([]int{}, g.LineNos...), Offers: make([]offerBody, len(g.Offers))}
		for j, o := range g.Offers {
			gb.Offers[j] = offerOut(o)
		}
		out.Groups[i] = gb
	}
	seen := map[string]bool{}
	for _, u := range q.Unavailable {
		out.Unavailable = append(out.Unavailable, unavailableBody{Tier: string(u.Tier), Constraint: u.Constraint, GroupNo: u.GroupNo})
		if !seen[string(u.Tier)] {
			seen[string(u.Tier)] = true
			out.UnavailableTiers = append(out.UnavailableTiers, string(u.Tier))
		}
	}
	return out
}

func cartOut(v views.Cart) cartBody {
	b := cartBody{
		CartID: v.CartID, ShopperID: strPtr(v.ShopperID), State: string(v.State), Lines: make([]lineBody, len(v.Lines)),
		DestinationPostalPrefix: strPtr(v.DestinationPostalPrefix), DeliveryQuote: quoteOut(v.DeliveryQuote),
		Selections: make([]selectionBody, len(v.Selections)), Version: v.Version,
		Estimate: estimateBody{
			Currency: v.Estimate.Currency, ItemsMinor: v.Estimate.ItemsMinor, DeliveryMinor: v.Estimate.DeliveryMinor,
			TaxMinor: v.Estimate.TaxMinor, TotalMinor: v.Estimate.TotalMinor, EstimateComplete: v.Estimate.Complete,
		},
		CheckoutReadiness: readinessBody{Ready: v.Readiness.Ready, AllGroupsSelected: v.Readiness.AllGroupsSelected, NoOfferExpired: v.Readiness.NoOfferExpired},
	}
	for i, l := range v.Lines {
		b.Lines[i] = lineBody{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	for i, s := range v.Selections {
		b.Selections[i] = selectionBody{GroupNo: s.GroupNo, OfferID: s.OfferID}
	}
	return b
}
