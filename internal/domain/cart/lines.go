package cart

import (
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

func validQuantity(q int) bool { return q >= 1 && q <= MaxQuantity }

func (c *Cart) indexOf(item string) int { return indexIn(c.lines, item) }

// AddLine adds quantity of an item. An item already in the cart merges
// quantities (the merged quantity is bounded too). The caller states whether
// the item is purchasable in the local views; it is only consulted for a
// request that is otherwise valid. Raises CartLineAdded with the merged
// quantity; clears the quote and the selections.
func (c *Cart) AddLine(item string, quantity int, purchasable bool, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if !shared.ValidID(item) {
		return shared.Invalid("catalogItemId must be a valid id")
	}
	if !validQuantity(quantity) {
		return ErrQuantityOutOfRange
	}
	if !purchasable {
		return ErrItemNotPurchasable
	}
	i := c.indexOf(item)
	merged := quantity
	if i >= 0 {
		merged += c.lines[i].Quantity
	}
	if !validQuantity(merged) {
		return ErrQuantityOutOfRange
	}
	if i < 0 && len(c.lines) >= MaxLines {
		return ErrTooManyLines
	}
	if i >= 0 {
		c.lines[i].Quantity = merged
	} else {
		c.lines = append(c.lines, Line{CatalogItemID: item, Quantity: merged})
	}
	c.clearQuote()
	at := c.touch(now)
	c.pending = append(c.pending, CartLineAdded{
		Header: shared.Header{Version: c.version, At: at}, CartID: c.id, CatalogItemID: item, Quantity: merged,
	})
	return nil
}

// SetLineQuantity sets the quantity of an existing line; it raises no event.
func (c *Cart) SetLineQuantity(item string, quantity int, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	if !validQuantity(quantity) {
		return ErrQuantityOutOfRange
	}
	i := c.indexOf(item)
	if i < 0 {
		return ErrLineNotFound
	}
	c.lines[i].Quantity = quantity
	c.clearQuote()
	c.touch(now)
	return nil
}

// RemoveLine drops the line of an item, keeping the order of the others; it
// raises no event.
func (c *Cart) RemoveLine(item string, now time.Time) error {
	if err := c.open(); err != nil {
		return err
	}
	i := c.indexOf(item)
	if i < 0 {
		return ErrLineNotFound
	}
	c.lines = append(c.lines[:i], c.lines[i+1:]...)
	c.clearQuote()
	c.touch(now)
	return nil
}
