package postgres

import (
	"encoding/json"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// Stored forms of the cart's value objects (jsonb columns).

type lineJSON struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type offerJSON struct {
	OfferID           string    `json:"offerId"`
	GroupNo           int       `json:"groupNo"`
	Tier              string    `json:"tier"`
	DeliverByEarliest string    `json:"deliverByEarliest"`
	DeliverByLatest   string    `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type groupJSON struct {
	GroupNo int         `json:"groupNo"`
	LineNos []int       `json:"lineNos"`
	Offers  []offerJSON `json:"offers"`
}

type unavailableJSON struct {
	Tier       string `json:"tier"`
	Constraint string `json:"constraint"`
	GroupNo    int    `json:"groupNo,omitempty"`
}

type quoteJSON struct {
	Groups      []groupJSON       `json:"groups"`
	Unavailable []unavailableJSON `json:"unavailable"`
	QuotedAt    time.Time         `json:"quotedAt"`
}

type selectionJSON struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
}

func marshalLines(ls []cart.Line) ([]byte, error) {
	out := make([]lineJSON, len(ls))
	for i, l := range ls {
		out[i] = lineJSON(l)
	}
	return json.Marshal(out)
}

func unmarshalLines(b []byte) ([]cart.Line, error) {
	var in []lineJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	out := make([]cart.Line, len(in))
	for i, l := range in {
		out[i] = cart.Line(l)
	}
	return out, nil
}

func marshalSelections(ss []cart.Selection) ([]byte, error) {
	out := make([]selectionJSON, len(ss))
	for i, s := range ss {
		out[i] = selectionJSON(s)
	}
	return json.Marshal(out)
}

func unmarshalSelections(b []byte) ([]cart.Selection, error) {
	var in []selectionJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	out := make([]cart.Selection, len(in))
	for i, s := range in {
		out[i] = cart.Selection(s)
	}
	return out, nil
}

// marshalQuote stores a nil quote as SQL NULL.
func marshalQuote(q *cart.DeliveryQuote) ([]byte, error) {
	if q == nil {
		return nil, nil
	}
	out := quoteJSON{QuotedAt: q.QuotedAt, Groups: make([]groupJSON, len(q.Groups)), Unavailable: make([]unavailableJSON, len(q.Unavailable))}
	for i, g := range q.Groups {
		jg := groupJSON{GroupNo: g.GroupNo, LineNos: append([]int{}, g.LineNos...), Offers: make([]offerJSON, len(g.Offers))}
		for j, o := range g.Offers {
			jg.Offers[j] = offerJSON{o.OfferID, o.GroupNo, string(o.Tier), o.DeliverByEarliest, o.DeliverByLatest, o.ExpiresAt}
		}
		out.Groups[i] = jg
	}
	for i, u := range q.Unavailable {
		out.Unavailable[i] = unavailableJSON{string(u.Tier), u.Constraint, u.GroupNo}
	}
	return json.Marshal(out)
}

func unmarshalQuote(b []byte) (*cart.DeliveryQuote, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var in quoteJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	q := &cart.DeliveryQuote{QuotedAt: in.QuotedAt}
	for _, g := range in.Groups {
		qg := cart.QuoteGroup{GroupNo: g.GroupNo, LineNos: g.LineNos}
		for _, o := range g.Offers {
			qg.Offers = append(qg.Offers, cart.QuotedOffer{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: cart.Tier(o.Tier),
				DeliverByEarliest: o.DeliverByEarliest, DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt})
		}
		q.Groups = append(q.Groups, qg)
	}
	for _, u := range in.Unavailable {
		q.Unavailable = append(q.Unavailable, cart.TierUnavailable{Tier: cart.Tier(u.Tier), Constraint: u.Constraint, GroupNo: u.GroupNo})
	}
	return q, nil
}
