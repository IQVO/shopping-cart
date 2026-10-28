// Package usecases holds the application's use cases: one per operation of
// apis/openapi.yaml, one per policy and one per consumed event. Every command
// follows one shape inside ONE ports.UnitOfWork: load the aggregate, call the
// domain, save it and append the raised events to the outbox, then return a
// view. Domain, shared and port errors pass through unchanged (errors.Is keeps
// working): the HTTP adapter maps them to problem types.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Deps bundles the ports the use cases need.
type Deps struct {
	Carts     ports.CartRepository
	Items     ports.ItemRepository
	Schedules ports.ScheduleRepository
	TaxTables ports.TaxTableRepository
	Quoter    ports.QuoteOffers
	Inbox     ports.Inbox
	Outbox    ports.EventOutbox
	UoW       ports.UnitOfWork
	Clock     ports.Clock
	IDs       ports.IDGenerator
	// Currency is the configured currency of the estimate when no priced item
	// states one (pricing runs one currency, integer minor units).
	Currency string
	// Log receives anomalies that are acknowledged, not failed (a closed or
	// unknown cart named by CheckoutCompleted). Nil discards them.
	Log *slog.Logger
	// Metrics records the business outcome of each AddLine; nil records
	// nothing, so unit tests need no wiring.
	Metrics ports.CartMetrics
}

const (
	// commandAttempts is how often a command runs when another writer got in
	// between its load and its save.
	commandAttempts = 2
	// consumerAttempts is the same for consumed events.
	consumerAttempts = 5
)

func (d Deps) now() time.Time { return d.Clock.Now().UTC() }

// metrics returns the configured CartMetrics or a no-op.
func (d Deps) metrics() ports.CartMetrics {
	if d.Metrics != nil {
		return d.Metrics
	}
	return noMetrics{}
}

// noMetrics is the CartMetrics of a Deps without one.
type noMetrics struct{}

func (noMetrics) LineAdded(context.Context)            {}
func (noMetrics) LineRejected(context.Context, string) {}

func (d Deps) publish(ctx context.Context, events []shared.Event) error {
	if err := d.Outbox.Append(ctx, events...); err != nil {
		return fmt.Errorf("append events to outbox: %w", err)
	}
	return nil
}

// persist saves the cart and appends the events it raised, in the unit of work
// of ctx.
func (d Deps) persist(ctx context.Context, c *cart.Cart) error {
	events := c.PullEvents()
	if err := d.Carts.Save(ctx, c); err != nil {
		return err
	}
	return d.publish(ctx, events)
}

// transact runs attempt in a unit of work. A ports.ErrConcurrentUpdate means
// another writer got in between the load and the save, so the whole attempt
// (load, domain call, save, outbox) runs again on fresh state, up to attempts
// times, before the conflict is reported.
func transact[T any](ctx context.Context, uow ports.UnitOfWork, attempts int, attempt func(ctx context.Context) (T, error)) (T, error) {
	var out T
	run := func(ctx context.Context) error {
		v, err := attempt(ctx)
		if err != nil {
			return err
		}
		out = v
		return nil
	}
	var err error
	for i := 0; i < attempts; i++ {
		if err = uow.Do(ctx, run); !errors.Is(err, ports.ErrConcurrentUpdate) {
			break
		}
	}
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

func (d Deps) warn(ctx context.Context, msg string, args ...any) {
	if d.Log != nil {
		d.Log.WarnContext(ctx, msg, args...)
	}
}

func isNotFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }
