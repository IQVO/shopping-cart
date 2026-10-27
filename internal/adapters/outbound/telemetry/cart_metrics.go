package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/IQVO/shopping-cart/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/IQVO/shopping-cart"

// linesAddedName is the Tier 2 business metric of the context (the fleet's
// standard metrics convention): every AddLine attempt, by outcome.
const linesAddedName = "cart.lines.added"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeAdded    = "added"
	outcomeRejected = "rejected"
)

// CartMetrics implements ports.CartMetrics against the global MeterProvider.
// Until Setup installs a real provider the global one is a no-op, so recording
// is cheap and safe in tests and local runs.
type CartMetrics struct {
	counter metric.Int64Counter
}

var _ ports.CartMetrics = (*CartMetrics)(nil)

// NewCartMetrics registers the counter. It only fails if the instrument name
// is invalid, which is a programming error, not a runtime condition. Call it
// after Setup so the instrument binds to the real MeterProvider.
func NewCartMetrics() (*CartMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		linesAddedName,
		metric.WithDescription("AddLine attempts by outcome (added or rejected; reason says why a rejection happened, "+
			"as a problem slug such as item-not-purchasable or quantity-out-of-range). "+
			"A falling added share with a rising item-not-purchasable means shoppers are being refused items the storefront shows, "+
			"so the catalogue or pricing read model is stale or missing; a rising internal-error means the service itself is failing."),
		metric.WithUnit("{line}"),
	)
	if err != nil {
		return nil, err
	}
	return &CartMetrics{counter: counter}, nil
}

// LineAdded counts a line that was added.
func (m *CartMetrics) LineAdded(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeAdded)))
}

// LineRejected counts an AddLine that stored nothing, with the reason why.
func (m *CartMetrics) LineRejected(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRejected), reasonKey.String(reason)))
}
