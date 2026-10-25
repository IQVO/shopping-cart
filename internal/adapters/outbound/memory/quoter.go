package memory

import (
	"context"
	"sync"

	"github.com/IQVO/shopping-cart/internal/application/ports"
	"github.com/IQVO/shopping-cart/internal/domain/cart"
)

var _ ports.QuoteOffers = (*Quoter)(nil)

// QuoteCall is one recorded call of the Quoter double.
type QuoteCall struct {
	IdempotencyKey string
	Destination    string
	Lines          []cart.Line
}

// Quoter is a scripted ports.QuoteOffers: it answers with Quote (or Err) and
// records every call.
type Quoter struct {
	mu    sync.Mutex
	quote cart.DeliveryQuote
	err   error
	calls []QuoteCall
}

// Respond scripts the next answers: the quote, or the error when err is set.
func (q *Quoter) Respond(quote cart.DeliveryQuote, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.quote, q.err = quote, err
}

// Quote implements ports.QuoteOffers.
func (q *Quoter) Quote(_ context.Context, key, destination string, lines []cart.Line) (cart.DeliveryQuote, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.calls = append(q.calls, QuoteCall{IdempotencyKey: key, Destination: destination, Lines: append([]cart.Line(nil), lines...)})
	return q.quote, q.err
}

// Calls returns the recorded calls in order.
func (q *Quoter) Calls() []QuoteCall {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]QuoteCall(nil), q.calls...)
}
