// Package deliverypromise is the outbound HTTP client of delivery-promise, the
// only synchronous call shopping-cart makes (the model's sync_calls
// QuoteDeliveryOffers, REST POST /delivery-offers:quote on the network's Open
// Host Service). It restates the one route it uses and nothing else; the DTOs
// here are not shared with the service.
package deliverypromise

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

// DefaultTimeout bounds one call; a slower delivery-promise is unavailable.
const DefaultTimeout = 5 * time.Second

// maxResponseBytes bounds what the client reads of one response.
const maxResponseBytes = 1 << 20

const quotePath = "/delivery-offers:quote"

// Gateway implements ports.QuoteOffers over HTTP.
type Gateway struct {
	base   string
	client *http.Client
	now    func() time.Time
	logger *slog.Logger
	zones  *zones
}

var _ ports.QuoteOffers = (*Gateway)(nil)

// Option configures a Gateway.
type Option func(*Gateway)

// WithLogger sets the logger of the gateway's warnings (an unusable window
// time zone); the default is slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(g *Gateway) {
		if logger != nil {
			g.logger = logger
		}
	}
}

// New builds a Gateway for the delivery-promise base URL (no trailing slash
// needed). A nil client gets one with DefaultTimeout; a nil now reads the wall
// clock (it stamps a quote whose offers carry no quotedAt).
func New(baseURL string, client *http.Client, now func() time.Time, opts ...Option) *Gateway {
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}
	if now == nil {
		now = time.Now
	}
	g := &Gateway{base: strings.TrimRight(baseURL, "/"), client: client, now: now, logger: slog.Default()}
	for _, opt := range opts {
		opt(g)
	}
	g.zones = newZones(g.logger)
	return g
}

type lineDTO struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type quoteRequest struct {
	DestinationPostalPrefix string    `json:"destinationPostalPrefix"`
	Lines                   []lineDTO `json:"lines"`
}

// windowDTO is the offer's DeliveryWindow: two instants and, optionally, the
// IANA zone of the delivery station that serves the destination (absent on
// offers quoted before delivery-promise named it).
type windowDTO struct {
	Earliest time.Time `json:"earliest"`
	Latest   time.Time `json:"latest"`
	TimeZone string    `json:"timeZone,omitempty"`
}

type offerDTO struct {
	OfferID   string    `json:"offerId"`
	State     string    `json:"state"`
	Tier      string    `json:"tier"`
	GroupNo   int       `json:"groupNo"`
	Lines     []lineDTO `json:"lines"`
	Window    windowDTO `json:"window"`
	QuotedAt  time.Time `json:"quotedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type unavailableDTO struct {
	Tier       string `json:"tier"`
	Constraint string `json:"constraint"`
	GroupNo    int    `json:"groupNo"`
}

type quoteResponse struct {
	Offers      []offerDTO       `json:"offers"`
	Unavailable []unavailableDTO `json:"unavailable"`
}

// Quote implements ports.QuoteOffers: POST /delivery-offers:quote with the
// cart's lines and the destination prefix, forwarding the Idempotency-Key.
func (g *Gateway) Quote(ctx context.Context, idempotencyKey, destination string, lines []cart.Line) (cart.DeliveryQuote, error) {
	req := quoteRequest{DestinationPostalPrefix: destination, Lines: make([]lineDTO, len(lines))}
	for i, l := range lines {
		req.Lines[i] = lineDTO(l)
	}
	var resp quoteResponse
	if err := g.post(ctx, idempotencyKey, req, &resp); err != nil {
		return cart.DeliveryQuote{}, err
	}
	return g.toQuote(resp, lines), nil
}

// post sends the JSON request and decodes a 2xx answer into out. A transport
// error, a timeout, an undecodable body and any 5xx are
// ports.ErrDeliveryPromiseUnavailable; a 4xx is ports.ErrQuoteRejected (our
// request was wrong, a contract bug the caller must not retry blindly).
func (g *Gateway) post(ctx context.Context, key string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request to delivery-promise: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.base+quotePath, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build request to delivery-promise: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, application/problem+json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ports.ErrDeliveryPromiseUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: read response: %w", ports.ErrDeliveryPromiseUnavailable, err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("%w: undecodable response: %w", ports.ErrDeliveryPromiseUnavailable, err)
		}
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return fmt.Errorf("%w: status %d: %s", ports.ErrQuoteRejected, resp.StatusCode, problemDetail(payload))
	}
	return fmt.Errorf("%w: status %d", ports.ErrDeliveryPromiseUnavailable, resp.StatusCode)
}

func problemDetail(payload []byte) string {
	var p struct{ Type, Title, Detail string }
	_ = json.Unmarshal(payload, &p)
	if p.Detail != "" {
		return p.Detail
	}
	return p.Title
}
