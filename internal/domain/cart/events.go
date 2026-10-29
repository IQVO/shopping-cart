package cart

import (
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

const entity = "cart"

// CartLineAdded is raised by AddLine; Quantity is the merged line quantity.
type CartLineAdded struct {
	shared.Header
	CartID        ID
	CatalogItemID string
	Quantity      int
}

// EventName returns "CartLineAdded".
func (CartLineAdded) EventName() string { return "CartLineAdded" }

// Entity returns "cart".
func (CartLineAdded) Entity() string { return entity }

// SubjectID returns the cartId.
func (e CartLineAdded) SubjectID() string { return string(e.CartID) }

// CartDeliveryQuoted is raised by QuoteDelivery.
type CartDeliveryQuoted struct {
	shared.Header
	CartID                  ID
	DestinationPostalPrefix string
	GroupCount              int
}

// EventName returns "CartDeliveryQuoted".
func (CartDeliveryQuoted) EventName() string { return "CartDeliveryQuoted" }

// Entity returns "cart".
func (CartDeliveryQuoted) Entity() string { return entity }

// SubjectID returns the cartId.
func (e CartDeliveryQuoted) SubjectID() string { return string(e.CartID) }

// CartAbandoned is raised by AbandonInactive.
type CartAbandoned struct {
	shared.Header
	CartID        ID
	LastChangedAt time.Time
}

// EventName returns "CartAbandoned".
func (CartAbandoned) EventName() string { return "CartAbandoned" }

// Entity returns "cart".
func (CartAbandoned) Entity() string { return entity }

// SubjectID returns the cartId.
func (e CartAbandoned) SubjectID() string { return string(e.CartID) }

// CartCheckedOut is raised by CloseOnCheckoutCompleted.
type CartCheckedOut struct {
	shared.Header
	CartID            ID
	CheckoutSessionID string
}

// EventName returns "CartCheckedOut".
func (CartCheckedOut) EventName() string { return "CartCheckedOut" }

// Entity returns "cart".
func (CartCheckedOut) Entity() string { return entity }

// SubjectID returns the cartId.
func (e CartCheckedOut) SubjectID() string { return string(e.CartID) }

var (
	_ shared.Event = CartLineAdded{}
	_ shared.Event = CartDeliveryQuoted{}
	_ shared.Event = CartAbandoned{}
	_ shared.Event = CartCheckedOut{}
)
