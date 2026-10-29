// Package cart is the Cart aggregate of the shopping-cart bounded context: one
// shopper's held intent (lines, destination postal prefix, the delivery quote
// delivery-promise returned and the per-group selections).
//
// Lifecycle (model): OPEN -> CHECKED_OUT (CloseOnCheckoutCompleted), OPEN ->
// ABANDONED (AbandonInactive), OPEN -> MERGED (MergeInto). The three targets
// are terminal. Adding an item that is already in the cart merges quantities
// (there is no ErrDuplicateLine, decision H2). Any line or destination change
// clears the quote and the selections. Removal, merge and quantity changes
// raise no event, by design (decision H1).
//
// The package imports only the shared domain package and the standard
// library. What it needs from the rest of the context (whether an item is
// purchasable) is handed in by the caller as a plain value.
package cart

import (
	"errors"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Domain errors, one per Err of the model (problem types are their kebab-case
// names, see apis/openapi.yaml).
var (
	// ErrQuantityOutOfRange: a line quantity must be in 1..MaxQuantity.
	ErrQuantityOutOfRange = errors.New("quantity out of range")
	// ErrTooManyLines: at most MaxLines lines (customer-ordering A15).
	ErrTooManyLines = errors.New("too many lines")
	// ErrItemNotPurchasable: the item is not purchasable in the local views.
	ErrItemNotPurchasable = errors.New("item not purchasable")
	// ErrNoDestination: a delivery quote needs a destination postal prefix.
	ErrNoDestination = errors.New("no destination")
	// ErrStaleDeliverySelection: a selection must name an offer of the current quote.
	ErrStaleDeliverySelection = errors.New("stale delivery selection")
	// ErrCartClosed: a CHECKED_OUT, ABANDONED or MERGED cart never changes.
	ErrCartClosed = errors.New("cart closed")
	// ErrMergeTarget: a merge target is an OPEN cart of the same shopper, never the cart itself.
	ErrMergeTarget = errors.New("invalid merge target")
	// ErrLineNotFound: the cart holds no line for the item (problem type not-found).
	ErrLineNotFound = errors.New("line not found")
)

// Bounds.
const (
	MaxQuantity  = 99
	MaxLines     = 100
	MaxPrefixLen = 10
)

// ID is the cartId, the aggregate root id.
type ID string

// State is the lifecycle state of the cart.
type State string

// The states.
const (
	StateOpen       State = "OPEN"
	StateCheckedOut State = "CHECKED_OUT"
	StateAbandoned  State = "ABANDONED"
	StateMerged     State = "MERGED"
)

// Valid reports whether s is one of the four states.
func (s State) Valid() bool {
	return s == StateOpen || s == StateCheckedOut || s == StateAbandoned || s == StateMerged
}

// Line is one cart line; a cart holds at most one line per catalog item.
type Line struct {
	CatalogItemID string
	Quantity      int
}

// Cart is the aggregate root.
type Cart struct {
	id            ID
	shopperID     string
	state         State
	lines         []Line
	destination   string
	quote         *DeliveryQuote
	selections    []Selection
	createdAt     time.Time
	lastChangedAt time.Time
	version       int64
	loaded        int64
	pending       []shared.Event
}

// Create opens an empty OPEN cart. The shopperId is optional (guest-first).
func Create(id ID, shopperID string, now time.Time) (*Cart, error) {
	if !shared.ValidID(string(id)) {
		return nil, shared.Invalid("cartId must be a valid id")
	}
	if shopperID != "" && !shared.ValidID(shopperID) {
		return nil, shared.Invalid("shopperId must be a valid id")
	}
	if now.IsZero() {
		return nil, shared.Invalid("a creation instant is required")
	}
	at := shared.UTC(now)
	return &Cart{id: id, shopperID: shopperID, state: StateOpen, createdAt: at, lastChangedAt: at, version: 1}, nil
}

// ID returns the cartId.
func (c *Cart) ID() ID { return c.id }

// ShopperID returns the shopper reference, empty for a guest.
func (c *Cart) ShopperID() string { return c.shopperID }

// State returns the lifecycle state.
func (c *Cart) State() State { return c.state }

// Lines returns a copy of the lines in cart order.
func (c *Cart) Lines() []Line { return append([]Line(nil), c.lines...) }

// DestinationPostalPrefix returns the destination prefix, empty when unset.
func (c *Cart) DestinationPostalPrefix() string { return c.destination }

// CreatedAt returns when the cart was created.
func (c *Cart) CreatedAt() time.Time { return c.createdAt }

// LastChangedAt returns the instant of the last change (the idle clock).
func (c *Cart) LastChangedAt() time.Time { return c.lastChangedAt }

// Version returns the aggregate version (1 on creation, +1 per change).
func (c *Cart) Version() int64 { return c.version }

// LoadedVersion returns the version the aggregate was loaded at (0 when new).
func (c *Cart) LoadedVersion() int64 { return c.loaded }

// MarkPersisted records that the current version is stored.
func (c *Cart) MarkPersisted() { c.loaded = c.version }

// PullEvents returns the events raised since the last call and clears them.
func (c *Cart) PullEvents() []shared.Event {
	out := c.pending
	c.pending = nil
	return out
}

func (c *Cart) open() error {
	if c.state != StateOpen {
		return ErrCartClosed
	}
	return nil
}

// touch bumps the version and the idle clock.
func (c *Cart) touch(now time.Time) time.Time {
	at := shared.UTC(now)
	c.version++
	c.lastChangedAt = at
	return at
}

// clearQuote drops the delivery quote and every selection.
func (c *Cart) clearQuote() {
	c.quote = nil
	c.selections = nil
}
