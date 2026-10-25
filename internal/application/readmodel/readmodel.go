// Package readmodel holds the plain data of the event-fed local read models
// PurchasabilityView and EstimateView (ADR 0003). The ports package stays
// interfaces-only, so the structs live here.
package readmodel

import "time"

// Item is one row of PurchasabilityView: what the cart knows of a catalogue
// item. A price or a discontinuation can arrive before the item's
// CatalogItemCreated (no cross-key ordering), so a row may be a STUB:
// Catalogued false means "never seen created" and the item is not purchasable.
type Item struct {
	CatalogItemID string
	Catalogued    bool
	Discontinued  bool
	// Priced is true while pricing holds an effective price for the item.
	Priced       bool
	PriceMinor   int64
	Currency     string
	PriceVersion int64
	UpdatedAt    time.Time
}

// Purchasable is the cart's lenient rule (decision H3): the item is
// catalogued, not discontinued and has an effective price. Checkout's price
// quote is the real gate.
func (i Item) Purchasable() bool { return i.Catalogued && !i.Discontinued && i.Priced }
