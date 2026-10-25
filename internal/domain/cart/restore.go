package cart

import (
	"errors"
	"fmt"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Snapshot is the persistable state of a Cart. Round-tripping through
// Snapshot and Restore loses nothing (repository doubles rely on it).
type Snapshot struct {
	ID            ID
	ShopperID     string
	State         State
	Lines         []Line
	Destination   string
	Quote         *DeliveryQuote
	Selections    []Selection
	CreatedAt     time.Time
	LastChangedAt time.Time
	Version       int64
}

// Snapshot returns the persistable state.
func (c *Cart) Snapshot() Snapshot {
	return Snapshot{
		ID: c.id, ShopperID: c.shopperID, State: c.state, Lines: c.Lines(), Destination: c.destination,
		Quote: c.Quote(), Selections: c.Selections(), CreatedAt: c.createdAt, LastChangedAt: c.lastChangedAt, Version: c.version,
	}
}

// Restore rebuilds a persisted cart, validating every invariant a stored row
// must keep. A violation is shared.ErrInvalidSnapshot.
func Restore(s Snapshot) (*Cart, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("cart %q: %v", s.ID, err)
	}
	c := &Cart{
		id: s.ID, shopperID: s.ShopperID, state: s.State, lines: append([]Line(nil), s.Lines...), destination: s.Destination,
		selections: append([]Selection(nil), s.Selections...), createdAt: shared.UTC(s.CreatedAt),
		lastChangedAt: shared.UTC(s.LastChangedAt), version: s.Version, loaded: s.Version,
	}
	if s.Quote != nil {
		c.quote = s.Quote.clone()
	}
	return c, nil
}

func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) || (s.ShopperID != "" && !shared.ValidID(s.ShopperID)) {
		return errors.New("cartId and shopperId must be valid ids")
	}
	if !s.State.Valid() {
		return fmt.Errorf("state %q is unknown", s.State)
	}
	if s.Version < 1 || s.CreatedAt.IsZero() || s.LastChangedAt.IsZero() {
		return errors.New("version, createdAt and lastChangedAt are required")
	}
	if s.Destination != "" && !ValidPrefix(s.Destination) {
		return errors.New("destination is not a postal prefix")
	}
	if err := checkLines(s.Lines); err != nil {
		return err
	}
	return checkQuoteState(s)
}

func checkLines(lines []Line) error {
	if len(lines) > MaxLines {
		return errors.New("too many lines")
	}
	seen := map[string]bool{}
	for _, l := range lines {
		if !shared.ValidID(l.CatalogItemID) || !validQuantity(l.Quantity) || seen[l.CatalogItemID] {
			return fmt.Errorf("line %q is invalid or repeated", l.CatalogItemID)
		}
		seen[l.CatalogItemID] = true
	}
	return nil
}

func checkQuoteState(s Snapshot) error {
	if s.Quote == nil {
		if len(s.Selections) != 0 {
			return errors.New("selections need a quote")
		}
		return nil
	}
	if s.Destination == "" {
		return errors.New("a quote needs a destination")
	}
	if err := s.Quote.validate(len(s.Lines)); err != nil {
		return err
	}
	last := 0
	for _, sel := range s.Selections {
		if sel.GroupNo <= last || !s.Quote.hasOffer(sel.GroupNo, sel.OfferID) {
			return errors.New("selections are ordered by group and name an offer of the quote")
		}
		last = sel.GroupNo
	}
	return nil
}
