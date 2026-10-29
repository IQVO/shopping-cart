package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/IQVO/shopping-cart/internal/domain/cart"
	"github.com/IQVO/shopping-cart/internal/domain/estimate"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Meta is what the Kafka adapter hands over about the CloudEvents envelope.
type Meta struct {
	EventID string
	Type    string
	At      time.Time
}

// Consumer applies the events shopping-cart consumes (ADR 0003). Every method
// runs in one unit of work together with the inbox claim, so an event is
// applied at most once. An invalid payload is shared.ErrValidation: a poison
// message the adapter dead-letters.
type Consumer struct{ Deps }

// CatalogItemCreated is the consumed offer-availability event of the same name.
type CatalogItemCreated struct{ CatalogItemID string }

// CatalogItemDiscontinued is the consumed offer-availability event of the same name.
type CatalogItemDiscontinued struct{ CatalogItemID string }

// ItemPriceChanged is the consumed pricing event of the same name.
type ItemPriceChanged struct {
	CatalogItemID       string
	Currency            string
	EffectivePriceMinor int64
	Version             int64
}

// ItemPriceWithdrawn is the consumed pricing event of the same name.
type ItemPriceWithdrawn struct {
	CatalogItemID string
	Version       int64
}

// TierCharge is one tier of DeliveryChargeScheduleChanged.
type TierCharge struct {
	Tier          string
	ChargeMinor   int64
	FreeOverMinor int64
}

// DeliveryChargeScheduleChanged is the consumed pricing event of the same name.
type DeliveryChargeScheduleChanged struct {
	Version int64
	Tiers   []TierCharge
}

// TaxBand is one band of TaxTableChanged.
type TaxBand struct {
	PostalPrefixFrom string
	PostalPrefixTo   string
	RateBasisPoints  int
}

// TaxTableChanged is the consumed pricing event of the same name.
type TaxTableChanged struct {
	Version int64
	Bands   []TaxBand
}

// CheckoutCompleted is the consumed checkout event of the same name.
type CheckoutCompleted struct {
	CartID    string
	SessionID string
}

// handle claims the event and runs apply in the same unit of work.
func (c Consumer) handle(ctx context.Context, m Meta, apply func(ctx context.Context) error) error {
	_, err := transact(ctx, c.UoW, consumerAttempts, func(ctx context.Context) (struct{}, error) {
		first, err := c.Inbox.Claim(ctx, m.EventID, m.Type)
		if err != nil || !first {
			return struct{}{}, err
		}
		return struct{}{}, apply(ctx)
	})
	return err
}

// when returns m.At, or now when the producer sent no time.
func (c Consumer) when(m Meta) time.Time {
	if m.At.IsZero() {
		return c.now()
	}
	return m.At.UTC()
}

// ApplyCatalogItemCreated marks the item catalogued in PurchasabilityView.
func (c Consumer) ApplyCatalogItemCreated(ctx context.Context, m Meta, in CatalogItemCreated) error {
	if !shared.ValidID(in.CatalogItemID) {
		return shared.Invalid("catalogItemId is not a valid id")
	}
	return c.handle(ctx, m, func(ctx context.Context) error { return c.Items.MarkCatalogued(ctx, in.CatalogItemID, c.when(m)) })
}

// ApplyCatalogItemDiscontinued marks the item discontinued; it can no longer
// be added to a cart. Lines already in carts stay: checkout's quote refuses them.
func (c Consumer) ApplyCatalogItemDiscontinued(ctx context.Context, m Meta, in CatalogItemDiscontinued) error {
	if !shared.ValidID(in.CatalogItemID) {
		return shared.Invalid("catalogItemId is not a valid id")
	}
	return c.handle(ctx, m, func(ctx context.Context) error { return c.Items.MarkDiscontinued(ctx, in.CatalogItemID, c.when(m)) })
}

// ApplyItemPriceChanged records the item's effective price, guarded by the
// event version.
func (c Consumer) ApplyItemPriceChanged(ctx context.Context, m Meta, in ItemPriceChanged) error {
	if !shared.ValidID(in.CatalogItemID) || in.EffectivePriceMinor < 0 || in.Version < 1 || !validCurrency(in.Currency) {
		return shared.Invalid("ItemPriceChanged needs a catalogItemId, a currency, a non-negative price and a version")
	}
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Items.ApplyPrice(ctx, in.CatalogItemID, in.Currency, in.EffectivePriceMinor, in.Version, c.when(m))
	})
}

// ApplyItemPriceWithdrawn clears the item's price, guarded by the event version.
func (c Consumer) ApplyItemPriceWithdrawn(ctx context.Context, m Meta, in ItemPriceWithdrawn) error {
	if !shared.ValidID(in.CatalogItemID) || in.Version < 1 {
		return shared.Invalid("ItemPriceWithdrawn needs a catalogItemId and a version")
	}
	return c.handle(ctx, m, func(ctx context.Context) error {
		return c.Items.ApplyWithdrawn(ctx, in.CatalogItemID, in.Version, c.when(m))
	})
}

func validCurrency(s string) bool {
	if len(s) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if s[i] < 'A' || s[i] > 'Z' {
			return false
		}
	}
	return true
}

// ApplyDeliveryChargeScheduleChanged replaces the current schedule when the
// event's version is higher than the stored one.
func (c Consumer) ApplyDeliveryChargeScheduleChanged(ctx context.Context, m Meta, in DeliveryChargeScheduleChanged) error {
	tiers := map[cart.Tier]estimate.TierCharge{}
	for _, t := range in.Tiers {
		tier := cart.Tier(t.Tier)
		if !tier.Valid() || t.ChargeMinor < 0 || t.FreeOverMinor < 0 {
			return shared.Invalid("tier %q or its amounts are invalid", t.Tier)
		}
		tiers[tier] = estimate.TierCharge{ChargeMinor: t.ChargeMinor, FreeOverMinor: t.FreeOverMinor}
	}
	if in.Version < 1 || len(tiers) == 0 {
		return shared.Invalid("DeliveryChargeScheduleChanged needs a version and at least one tier")
	}
	return c.handle(ctx, m, func(ctx context.Context) error {
		_, err := c.Schedules.Replace(ctx, estimate.Schedule{Version: in.Version, Tiers: tiers})
		return err
	})
}

// ApplyTaxTableChanged replaces the current tax table when the event's version
// is higher than the stored one.
func (c Consumer) ApplyTaxTableChanged(ctx context.Context, m Meta, in TaxTableChanged) error {
	bands := make([]estimate.TaxBand, 0, len(in.Bands))
	for _, b := range in.Bands {
		if b.PostalPrefixFrom == "" || b.PostalPrefixTo < b.PostalPrefixFrom || b.RateBasisPoints < 0 {
			return shared.Invalid("band %q-%q is invalid", b.PostalPrefixFrom, b.PostalPrefixTo)
		}
		bands = append(bands, estimate.TaxBand{From: b.PostalPrefixFrom, To: b.PostalPrefixTo, RateBasisPoints: b.RateBasisPoints})
	}
	if in.Version < 1 || len(bands) == 0 {
		return shared.Invalid("TaxTableChanged needs a version and at least one band")
	}
	return c.handle(ctx, m, func(ctx context.Context) error {
		_, err := c.TaxTables.Replace(ctx, estimate.TaxTable{Version: in.Version, Bands: bands})
		return err
	})
}

// ApplyCheckoutCompleted is the policy CloseOnCheckoutCompleted: the cart named
// by the event becomes CHECKED_OUT and CartCheckedOut is raised. A cart that is
// unknown or no longer OPEN has no transition: the inbox row is still written
// and the anomaly is logged.
func (c Consumer) ApplyCheckoutCompleted(ctx context.Context, m Meta, in CheckoutCompleted) error {
	if !shared.ValidID(in.CartID) || !shared.ValidID(in.SessionID) {
		return shared.Invalid("CheckoutCompleted needs a cartId and a sessionId")
	}
	return c.handle(ctx, m, func(ctx context.Context) error {
		cc, err := c.Carts.Get(ctx, cart.ID(in.CartID))
		if isNotFound(err) {
			c.warn(ctx, "CheckoutCompleted names an unknown cart", "cartId", in.CartID)
			return nil
		}
		if err != nil {
			return err
		}
		if err := cc.CloseOnCheckoutCompleted(in.SessionID, c.when(m)); err != nil {
			if errors.Is(err, cart.ErrCartClosed) {
				c.warn(ctx, "CheckoutCompleted for a cart that is not open", "cartId", in.CartID, "state", string(cc.State()))
				return nil
			}
			return err
		}
		return c.persist(ctx, cc)
	})
}
