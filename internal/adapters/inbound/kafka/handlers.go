package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
	"github.com/IQVO/shopping-cart/internal/domain/shared"
)

// Consumed topics: the producers' integration topics (their contracts are
// restated in apis/asyncapi.yaml, never imported).
const (
	OfferAvailabilityTopic = "retailnetwork.offer-availability.events"
	PricingTopic           = "retailnetwork.pricing.events"
	CheckoutTopic          = "retailnetwork.checkout.events"

	typePrefixCatalogItem = "com.retailnetwork.commerce.offer-availability.catalogitem."
	typePrefixItemPrice   = "com.retailnetwork.storefront.pricing.itemprice."
	typePrefixSchedule    = "com.retailnetwork.storefront.pricing.deliverychargeschedule."
	typePrefixTaxTable    = "com.retailnetwork.storefront.pricing.taxtable."
	typePrefixCheckout    = "com.retailnetwork.storefront.checkout.checkoutsession."
)

// FULL CloudEvents types of the consumed events, byte-identical to the
// producers' contracts.
const (
	TypeCatalogItemCreated      = typePrefixCatalogItem + "CatalogItemCreated"
	TypeCatalogItemDiscontinued = typePrefixCatalogItem + "CatalogItemDiscontinued"
	TypeItemPriceChanged        = typePrefixItemPrice + "ItemPriceChanged"
	TypeItemPriceWithdrawn      = typePrefixItemPrice + "ItemPriceWithdrawn"
	TypeChargeScheduleChanged   = typePrefixSchedule + "DeliveryChargeScheduleChanged"
	TypeTaxTableChanged         = typePrefixTaxTable + "TaxTableChanged"
	TypeCheckoutCompleted       = typePrefixCheckout + "CheckoutCompleted"
	maxTiers, maxBands          = 16, 1000
	maxFieldLength              = 200
)

// Payloads restate the producers' v1 `data` (camelCase JSON); unknown fields
// are ignored so a producer may add fields without breaking us.
type (
	catalogItemData struct {
		CatalogItemID string `json:"catalogItemId"`
	}
	itemPriceChangedData struct {
		CatalogItemID       string `json:"catalogItemId"`
		Currency            string `json:"currency"`
		EffectivePriceMinor int64  `json:"effectivePriceMinor"`
		Version             int64  `json:"version"`
	}
	itemPriceWithdrawnData struct {
		CatalogItemID string `json:"catalogItemId"`
		Version       int64  `json:"version"`
	}
	tierData struct {
		Tier          string `json:"tier"`
		ChargeMinor   int64  `json:"chargeMinor"`
		FreeOverMinor int64  `json:"freeOverMinor"`
	}
	scheduleChangedData struct {
		Version int64      `json:"version"`
		Tiers   []tierData `json:"tiers"`
	}
	bandData struct {
		PostalPrefixFrom string `json:"postalPrefixFrom"`
		PostalPrefixTo   string `json:"postalPrefixTo"`
		RateBasisPoints  int    `json:"rateBasisPoints"`
	}
	taxTableChangedData struct {
		Version int64      `json:"version"`
		Bands   []bandData `json:"bands"`
	}
	checkoutCompletedData struct {
		CartID    string `json:"cartId"`
		SessionID string `json:"sessionId"`
	}
)

// meta is the identity of the consumed CloudEvent.
func meta(e ce.Event) usecases.Meta {
	return usecases.Meta{EventID: e.ID(), Type: e.Type(), At: e.Time().UTC()}
}

// need returns an errBadPayload naming the first empty required field.
func need(what string, pairs ...string) error {
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			return fmt.Errorf("%w: %s.%s is required", errBadPayload, what, pairs[i])
		}
		if len(pairs[i+1]) > maxFieldLength {
			return fmt.Errorf("%w: %s.%s is longer than %d characters", errBadPayload, what, pairs[i], maxFieldLength)
		}
	}
	return nil
}

// handler decodes the payload of an event, validates it and applies it. A
// value the use case itself refuses (shared.ErrValidation) can never be
// applied, so it is a bad payload too.
func handler[T any](what string, validate func(T) error, apply func(ctx context.Context, m usecases.Meta, d T) error) HandlerFunc {
	return func(ctx context.Context, e ce.Event) error {
		d, err := decodeData[T](e)
		if err != nil {
			return err
		}
		if err := validate(d); err != nil {
			return err
		}
		if err := apply(ctx, meta(e), d); err != nil {
			if errors.Is(err, shared.ErrValidation) {
				return fmt.Errorf("%w: %w", errBadPayload, err)
			}
			return err
		}
		slog.DebugContext(ctx, "event processed", "type", e.Type(), "event_id", e.ID(), "what", what)
		return nil
	}
}

// OfferAvailabilityHandlers maps offer-availability's events to the consumer use cases.
func OfferAvailabilityHandlers(c usecases.Consumer) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeCatalogItemCreated: handler("CatalogItemCreated", func(d catalogItemData) error {
			return need("CatalogItemCreated", "catalogItemId", d.CatalogItemID)
		}, func(ctx context.Context, m usecases.Meta, d catalogItemData) error {
			return c.ApplyCatalogItemCreated(ctx, m, usecases.CatalogItemCreated{CatalogItemID: d.CatalogItemID})
		}),
		TypeCatalogItemDiscontinued: handler("CatalogItemDiscontinued", func(d catalogItemData) error {
			return need("CatalogItemDiscontinued", "catalogItemId", d.CatalogItemID)
		}, func(ctx context.Context, m usecases.Meta, d catalogItemData) error {
			return c.ApplyCatalogItemDiscontinued(ctx, m, usecases.CatalogItemDiscontinued{CatalogItemID: d.CatalogItemID})
		}),
	}
}

// CheckoutHandlers maps checkout's events to the consumer use cases.
func CheckoutHandlers(c usecases.Consumer) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeCheckoutCompleted: handler("CheckoutCompleted", func(d checkoutCompletedData) error {
			return need("CheckoutCompleted", "cartId", d.CartID, "sessionId", d.SessionID)
		}, func(ctx context.Context, m usecases.Meta, d checkoutCompletedData) error {
			return c.ApplyCheckoutCompleted(ctx, m, usecases.CheckoutCompleted{CartID: d.CartID, SessionID: d.SessionID})
		}),
	}
}
