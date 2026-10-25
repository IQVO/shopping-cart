// Package http is the REST inbound adapter: every route of apis/openapi.yaml on
// a chi router, RFC 9457 problem documents for every error and the liveness and
// readiness endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

// Route names under which the Idempotency-Key records are stored.
const (
	routeCreate      = "POST /carts"
	routeAddLine     = "POST /carts/{cartId}/lines"
	routeSetQuantity = "POST /carts/{cartId}/lines/{catalogItemId}:set-quantity"
	routeRemoveLine  = "POST /carts/{cartId}/lines/{catalogItemId}:remove"
	routeDestination = "POST /carts/{cartId}:set-destination"
	routeQuote       = "POST /carts/{cartId}:quote-delivery"
	routeSelect      = "POST /carts/{cartId}:select-delivery"
	routeMerge       = "POST /carts/{cartId}:merge-into"
	routeRequoted    = "POST /carts/{cartId}/delivery/requoted"
)

// Server holds the use cases the handlers call.
type Server struct {
	CreateCart           usecases.CreateCart
	AddLine              usecases.AddLine
	SetLineQuantity      usecases.SetLineQuantity
	RemoveLine           usecases.RemoveLine
	SetDestination       usecases.SetDestination
	QuoteDelivery        usecases.QuoteDelivery
	SelectDelivery       usecases.SelectDelivery
	MergeInto            usecases.MergeInto
	AcceptRequotedOffers usecases.AcceptRequotedOffers
	GetCart              usecases.GetCart
	ListCarts            usecases.ListCarts

	// IdempotencyStore and Clock back the Idempotency-Key middleware of the
	// POST routes.
	IdempotencyStore ports.IdempotencyStore
	Clock            ports.Clock
	// Readiness backs GET /readyz; nil means always ready (unit tests).
	Readiness *Readiness
}

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml.
func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	// A NUL (U+0000) in the path, the query or a body is a uniform client error,
	// never a Postgres 22021 -> 500; it runs before the Idempotency-Key
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.With(s.idempotent(routeCreate)).Post("/carts", s.handleCreate)
	r.Get("/carts", s.handleList)
	r.Get("/carts/{cartId}", s.handleGet)
	r.With(s.idempotent(routeAddLine)).Post("/carts/{cartId}/lines", s.handleAddLine)
	r.With(s.idempotent(routeSetQuantity)).Post("/carts/{cartId}/lines/{catalogItemId}:set-quantity", s.handleSetQuantity)
	r.With(s.idempotent(routeRemoveLine)).Post("/carts/{cartId}/lines/{catalogItemId}:remove", s.handleRemoveLine)
	r.With(s.idempotent(routeDestination)).Post("/carts/{cartId}:set-destination", s.handleSetDestination)
	r.With(s.idempotent(routeQuote)).Post("/carts/{cartId}:quote-delivery", s.handleQuoteDelivery)
	r.With(s.idempotent(routeSelect)).Post("/carts/{cartId}:select-delivery", s.handleSelectDelivery)
	r.With(s.idempotent(routeMerge)).Post("/carts/{cartId}:merge-into", s.handleMerge)
	r.With(s.idempotent(routeRequoted)).Post("/carts/{cartId}/delivery/requoted", s.handleRequoted)
	return r
}

func (s *Server) idempotent(route string) func(http.Handler) http.Handler {
	return RequireIdempotencyKey(s.IdempotencyStore, s.Clock, route)
}
