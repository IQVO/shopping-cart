package kafka

import (
	"context"
	"fmt"

	"github.com/IQVO/shopping-cart/internal/application/usecases"
)

// PricingHandlers maps pricing's events to the consumer use cases.
func PricingHandlers(c usecases.Consumer) map[string]HandlerFunc {
	return map[string]HandlerFunc{
		TypeItemPriceChanged: handler("ItemPriceChanged", func(d itemPriceChangedData) error {
			return need("ItemPriceChanged", "catalogItemId", d.CatalogItemID, "currency", d.Currency)
		}, func(ctx context.Context, m usecases.Meta, d itemPriceChangedData) error {
			return c.ApplyItemPriceChanged(ctx, m, usecases.ItemPriceChanged{
				CatalogItemID: d.CatalogItemID, Currency: d.Currency, EffectivePriceMinor: d.EffectivePriceMinor, Version: d.Version,
			})
		}),
		TypeItemPriceWithdrawn: handler("ItemPriceWithdrawn", func(d itemPriceWithdrawnData) error {
			return need("ItemPriceWithdrawn", "catalogItemId", d.CatalogItemID)
		}, func(ctx context.Context, m usecases.Meta, d itemPriceWithdrawnData) error {
			return c.ApplyItemPriceWithdrawn(ctx, m, usecases.ItemPriceWithdrawn{CatalogItemID: d.CatalogItemID, Version: d.Version})
		}),
		TypeChargeScheduleChanged: handler("DeliveryChargeScheduleChanged", func(d scheduleChangedData) error {
			if len(d.Tiers) == 0 || len(d.Tiers) > maxTiers {
				return fmt.Errorf("%w: DeliveryChargeScheduleChanged needs 1 to %d tiers", errBadPayload, maxTiers)
			}
			return nil
		}, func(ctx context.Context, m usecases.Meta, d scheduleChangedData) error {
			in := usecases.DeliveryChargeScheduleChanged{Version: d.Version}
			for _, t := range d.Tiers {
				in.Tiers = append(in.Tiers, usecases.TierCharge(t))
			}
			return c.ApplyDeliveryChargeScheduleChanged(ctx, m, in)
		}),
		TypeTaxTableChanged: handler("TaxTableChanged", func(d taxTableChangedData) error {
			if len(d.Bands) == 0 || len(d.Bands) > maxBands {
				return fmt.Errorf("%w: TaxTableChanged needs 1 to %d bands", errBadPayload, maxBands)
			}
			return nil
		}, func(ctx context.Context, m usecases.Meta, d taxTableChangedData) error {
			in := usecases.TaxTableChanged{Version: d.Version}
			for _, b := range d.Bands {
				in.Bands = append(in.Bands, usecases.TaxBand(b))
			}
			return c.ApplyTaxTableChanged(ctx, m, in)
		}),
	}
}
