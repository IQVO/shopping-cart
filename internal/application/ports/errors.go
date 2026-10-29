package ports

import "errors"

var (
	// ErrNotFound is what every repository returns for a Get that matches
	// nothing (problem type not-found, 404). An adapter wraps its own miss (no
	// rows, a missing map key) into this sentinel.
	ErrNotFound = errors.New("not found")
	// ErrConcurrentUpdate is what Save returns when the stored version is not
	// the one the aggregate was loaded at (problem type concurrent-update,
	// 409): the expectation is `stored version == aggregate.LoadedVersion()`,
	// and a create (LoadedVersion 0) expects that the id is not stored yet.
	ErrConcurrentUpdate = errors.New("concurrent update")
	// ErrDeliveryPromiseUnavailable is what QuoteOffers returns when
	// delivery-promise cannot be reached, times out or answers with a server
	// error (problem type delivery-promise-unavailable, 503).
	ErrDeliveryPromiseUnavailable = errors.New("delivery-promise unavailable")
	// ErrQuoteRejected is what QuoteOffers returns when delivery-promise
	// refuses the request as invalid (422): a contract bug on our side, never a
	// shopper error (problem type delivery-promise-unavailable would hide it).
	ErrQuoteRejected = errors.New("delivery-promise rejected the quote request")
)
