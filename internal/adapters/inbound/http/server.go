// Package http is the REST inbound adapter: every route of apis/openapi.yaml on
// a chi router, RFC 9457 problem documents for every error and the liveness and
// readiness endpoints. It has no auth middleware (fleet decision 2026-09-11;
// internal/architecture's TestNoAuthMiddlewareReintroduced).
package http

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/IQVO/shopping-cart/internal/adapters/inbound/requestlog"
	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

// Route names under which the Idempotency-Key records are stored: the five
// POST routes. PUT and DELETE are idempotent by method and take no middleware
// (a key sent to them is accepted and ignored).

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

// DefaultServiceName names the server spans and the HTTP metrics when the
// composition root passes no service name.
const DefaultServiceName = "shopping-cart"

// NewRouter builds the chi router for every endpoint of apis/openapi.yaml. A nil
// logger defaults to slog.Default(); an empty serviceName to DefaultServiceName.
//
// Middleware order is the fleet observability standard: RequestID, then otelchi
// (the server span, named after the chi route pattern, so one span name per
// route and not per cart id), then the request-duration histogram, then
// RequestLogger (it runs inside the span, so its record carries trace_id and
// span_id), then Recoverer. telemetry.Setup must have run before this call:
// otelchimetric binds the global MeterProvider when it is constructed.
func NewRouter(s *Server, logger *slog.Logger, serviceName string) http.Handler {
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(logger))
	r.Use(middleware.Recoverer)
	// A NUL (U+0000) in the path, the query or a body is a uniform client error,
	// never a Postgres 22021 -> 500; it runs before the Idempotency-Key
	// middleware so a rejected request never records its key.
	r.Use(rejectNUL)

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", s.handleReadyz)

	r.With(s.idempotent).Post("/carts", s.handleCreate)
	r.Get("/carts", s.handleList)
	r.Get("/carts/{cartId}", s.handleGet)
	r.With(s.idempotent).Post("/carts/{cartId}/lines", s.handleAddLine)
	r.Put("/carts/{cartId}/lines/{catalogItemId}", s.handleSetQuantity)
	r.Delete("/carts/{cartId}/lines/{catalogItemId}", s.handleRemoveLine)
	r.Put("/carts/{cartId}/destination", s.handleSetDestination)
	r.With(s.idempotent).Post("/carts/{cartId}/delivery-quotes", s.handleQuoteDelivery)
	r.Put("/carts/{cartId}/delivery-selection", s.handleSelectDelivery)
	r.With(s.idempotent).Post("/carts/{cartId}/merges", s.handleMerge)
	r.With(s.idempotent).Post("/carts/{cartId}/delivery-requote-acceptances", s.handleRequoted)
	return r
}

func (s *Server) idempotent(next http.Handler) http.Handler {
	return RequireIdempotencyKey(s.IdempotencyStore, s.Clock)(next)
}
