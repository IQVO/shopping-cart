// Package views holds the read shapes the use cases return to the inbound
// adapters: plain data, no behaviour.
package views

import (
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
)

// Cart is a cart with its labelled estimate and its checkout readiness.
type Cart struct {
	CartID                  string
	ShopperID               string
	State                   cart.State
	Lines                   []cart.Line
	DestinationPostalPrefix string
	DeliveryQuote           *cart.DeliveryQuote
	Selections              []cart.Selection
	Estimate                estimate.Estimate
	Readiness               cart.CheckoutReadiness
	Version                 int64
	LastChangedAt           time.Time
}
