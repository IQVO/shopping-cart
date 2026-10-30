package a2a

import (
	"fmt"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/views"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// problemBase is the namespace of this service's RFC 9457 `type` URIs. It is
// the SAME namespace as the REST adapter's, so a delegating agent sees the
// identifiers a REST caller would for the outcomes both surfaces share.
const problemBase = "/problems/"

// problem is the RFC 7807-shaped rejection an agent client receives instead of
// a result: domain rejections and request-shape failures are business answers,
// not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func newProblem(status int, slug, title, detail string) *problem {
	return &problem{Type: problemBase + slug, Title: title, Status: status, Detail: detail}
}

// The statuses and slugs below are the ones the REST adapter gives the two GETs
// these skills mirror (internal/adapters/inbound/http problemCatalogue): 404
// not-found / resource-not-found, 400 malformed-request, 422 validation-failed
// and 500 internal-error. 409 and 503 belong to writes and to delivery-promise
// calls this read-only agent never makes, so they are not mapped.

func problemMalformed(detail string) *problem {
	return newProblem(400, "malformed-request", "Malformed request", detail)
}

func problemUnknownSkill(skill string) *problem {
	return newProblem(400, "unknown-skill", "Unknown skill",
		fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill))
}

func problemValidation(cause error) *problem {
	return newProblem(422, "validation-failed", "Validation failed", cause.Error())
}

// problemCartNotFound answers a cartId that names no cart. An id too long to
// name one is not echoed back.
func problemCartNotFound(cartID string) *problem {
	detail := "no cart exists with that cartId"
	if len(cartID) <= maxCartIDLength {
		detail = fmt.Sprintf("no cart exists with cartId %q", cartID)
	}
	return newProblem(404, "not-found", "Not found", detail)
}

func problemNoSuchResource() *problem {
	return newProblem(404, "resource-not-found", "Resource not found", "no resource matches the requested cartId")
}

func problemInternal() *problem {
	return newProblem(500, "internal-error", "Internal server error", "an unexpected error occurred")
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id the
// answer belongs to.
type envelope struct {
	Skill   string    `json:"skill"`
	Data    *skillDTO `json:"data,omitempty"`
	Problem *problem  `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs; each skill fills its own
// field.
type skillDTO struct {
	Cart  *cartDTO     `json:"cart,omitempty"`
	Carts *cartListDTO `json:"carts,omitempty"`
}

// skillResult is a skill's answer: the structured data and its one-line text.
type skillResult struct {
	data    skillDTO
	summary string
}

// The DTOs below carry EXACTLY the fields of the REST Cart wire shape
// (apis/openapi.yaml, internal/adapters/inbound/http/dto.go): the A2A surface
// exposes what GET /carts/{cartId} and GET /carts already expose and nothing
// more. In particular views.Cart.LastChangedAt, which REST withholds, is not
// mapped. parity_test.go pins the field sets against the OpenAPI schemas.

type lineDTO struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type offerDTO struct {
	OfferID           string    `json:"offerId"`
	GroupNo           int       `json:"groupNo"`
	Tier              string    `json:"tier"`
	DeliverByEarliest string    `json:"deliverByEarliest"`
	DeliverByLatest   string    `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type groupDTO struct {
	GroupNo int        `json:"groupNo"`
	LineNos []int      `json:"lineNos"`
	Offers  []offerDTO `json:"offers"`
}

type unavailableDTO struct {
	Tier       string `json:"tier"`
	Constraint string `json:"constraint"`
	GroupNo    int    `json:"groupNo,omitempty"`
}

type quoteDTO struct {
	Groups           []groupDTO       `json:"groups"`
	UnavailableTiers []string         `json:"unavailableTiers"`
	Unavailable      []unavailableDTO `json:"unavailable"`
	QuotedAt         time.Time        `json:"quotedAt"`
}

type estimateDTO struct {
	Currency         string `json:"currency"`
	ItemsMinor       int64  `json:"itemsMinor"`
	DeliveryMinor    int64  `json:"deliveryMinor"`
	TaxMinor         int64  `json:"taxMinor"`
	TotalMinor       int64  `json:"totalMinor"`
	EstimateComplete bool   `json:"estimateComplete"`
}

type readinessDTO struct {
	Ready             bool `json:"ready"`
	AllGroupsSelected bool `json:"allGroupsSelected"`
	NoOfferExpired    bool `json:"noOfferExpired"`
}

type selectionDTO struct {
	GroupNo int    `json:"groupNo"`
	OfferID string `json:"offerId"`
}

type cartDTO struct {
	CartID                  string         `json:"cartId"`
	ShopperID               *string        `json:"shopperId"`
	State                   string         `json:"state"`
	Lines                   []lineDTO      `json:"lines"`
	DestinationPostalPrefix *string        `json:"destinationPostalPrefix"`
	DeliveryQuote           *quoteDTO      `json:"deliveryQuote,omitempty"`
	Selections              []selectionDTO `json:"selections"`
	Estimate                estimateDTO    `json:"estimate"`
	CheckoutReadiness       readinessDTO   `json:"checkoutReadiness"`
	Version                 int64          `json:"version"`
}

// cartListDTO is the list_shopper_carts result: the shopper's carts, newest
// first, like GET /carts.
type cartListDTO struct {
	Carts []cartDTO `json:"carts"`
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func offerOut(o cart.QuotedOffer) offerDTO {
	return offerDTO{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: string(o.Tier), DeliverByEarliest: o.DeliverByEarliest,
		DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt}
}

func quoteOut(q *cart.DeliveryQuote) *quoteDTO {
	if q == nil {
		return nil
	}
	out := &quoteDTO{QuotedAt: q.QuotedAt, Groups: make([]groupDTO, len(q.Groups)), UnavailableTiers: []string{}, Unavailable: []unavailableDTO{}}
	for i, g := range q.Groups {
		gd := groupDTO{GroupNo: g.GroupNo, LineNos: append([]int{}, g.LineNos...), Offers: make([]offerDTO, len(g.Offers))}
		for j, o := range g.Offers {
			gd.Offers[j] = offerOut(o)
		}
		out.Groups[i] = gd
	}
	seen := map[string]bool{}
	for _, u := range q.Unavailable {
		out.Unavailable = append(out.Unavailable, unavailableDTO{Tier: string(u.Tier), Constraint: u.Constraint, GroupNo: u.GroupNo})
		if !seen[string(u.Tier)] {
			seen[string(u.Tier)] = true
			out.UnavailableTiers = append(out.UnavailableTiers, string(u.Tier))
		}
	}
	return out
}

func cartOut(v views.Cart) cartDTO {
	d := cartDTO{
		CartID: v.CartID, ShopperID: strPtr(v.ShopperID), State: string(v.State), Lines: make([]lineDTO, len(v.Lines)),
		DestinationPostalPrefix: strPtr(v.DestinationPostalPrefix), DeliveryQuote: quoteOut(v.DeliveryQuote),
		Selections: make([]selectionDTO, len(v.Selections)), Version: v.Version,
		Estimate: estimateDTO{
			Currency: v.Estimate.Currency, ItemsMinor: v.Estimate.ItemsMinor, DeliveryMinor: v.Estimate.DeliveryMinor,
			TaxMinor: v.Estimate.TaxMinor, TotalMinor: v.Estimate.TotalMinor, EstimateComplete: v.Estimate.Complete,
		},
		CheckoutReadiness: readinessDTO{Ready: v.Readiness.Ready, AllGroupsSelected: v.Readiness.AllGroupsSelected, NoOfferExpired: v.Readiness.NoOfferExpired},
	}
	for i, l := range v.Lines {
		d.Lines[i] = lineDTO{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	for i, s := range v.Selections {
		d.Selections[i] = selectionDTO{GroupNo: s.GroupNo, OfferID: s.OfferID}
	}
	return d
}

// summarizeCart is the one-line text form of one cart. It names no shopper.
func summarizeCart(c cartDTO) string {
	ready := "not ready for checkout"
	if c.CheckoutReadiness.Ready {
		ready = "ready for checkout"
	}
	complete := "incomplete"
	if c.Estimate.EstimateComplete {
		complete = "complete"
	}
	return fmt.Sprintf("%s: %s, %d lines, estimate %d %s minor units (%s), %s",
		c.CartID, c.State, len(c.Lines), c.Estimate.TotalMinor, c.Estimate.Currency, complete, ready)
}

// summarizeCarts is the one-line text form of a shopper's carts. It names no
// shopper.
func summarizeCarts(list cartListDTO) string {
	if len(list.Carts) == 0 {
		return "0 carts"
	}
	out := fmt.Sprintf("%d carts:", len(list.Carts))
	for i, c := range list.Carts {
		sep := " "
		if i > 0 {
			sep = ", "
		}
		out += fmt.Sprintf("%s%s (%s)", sep, c.CartID, c.State)
	}
	return out
}
