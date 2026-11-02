# ADR 0003: Consumed contracts and the local read models

## Status

Accepted (2026-10-25).

## Context

The cart decides on sibling data (is the item purchasable, what does it cost, what
does a tier charge, what is the tax band) but never calls a sibling for it. The only
synchronous call is delivery-promise `QuoteDeliveryOffers`. Everything else arrives as
events into local read models (the repo rule `.claude/rules/context-boundaries.md`).

## Decision

### Consumed events

All dispatched on the FULL CloudEvents `type`; unknown types are ignored; the inbox
(`processed_events`) dedupes on the CloudEvents `id`; a payload that can never be
applied is dead-lettered to `<topic>.dlq`.

```text
com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated
com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued
com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged
com.retailnetwork.storefront.pricing.itemprice.ItemPriceWithdrawn
com.retailnetwork.storefront.pricing.deliverychargeschedule.DeliveryChargeScheduleChanged
com.retailnetwork.storefront.pricing.taxtable.TaxTableChanged
com.retailnetwork.storefront.checkout.checkoutsession.CheckoutCompleted
```

| Topic | Group variable | Events |
|---|---|---|
| `retailnetwork.offer-availability.events` | `OFFER_AVAILABILITY_CONSUMER_GROUP` | CatalogItemCreated, CatalogItemDiscontinued |
| `retailnetwork.pricing.events` | `PRICING_CONSUMER_GROUP` | ItemPriceChanged, ItemPriceWithdrawn, DeliveryChargeScheduleChanged, TaxTableChanged |
| `retailnetwork.checkout.events` | `CHECKOUT_CONSUMER_GROUP` | CheckoutCompleted |

`CheckoutCompleted` is coded against the model's shape `{cartId, sessionId}`: checkout
is not built yet, so the shape is reported as a `model_gap` until checkout publishes
its AsyncAPI.

### Local read models

- **PurchasabilityView** (`rm_items`): per item `catalogued`, `discontinued`,
  `priced`, the effective price, its currency and the last applied price `version`.
  `Purchasable = catalogued AND NOT discontinued AND priced`. It is lenient by
  design (decision H3): checkout's price quote is the real gate, so a line already in
  a cart is NOT removed when its item is discontinued or withdrawn.
- **No cross-key ordering** (the fold rule): a price, withdrawal or discontinuation
  that arrives before `CatalogItemCreated` upserts a STUB row (not purchasable);
  `CatalogItemCreated` completes it. Price writes apply only above the stored
  `version`, so a replay or a late older event is dropped; a withdrawal keeps its
  version for the same reason.
- **EstimateView** (`rm_current_schedule`, `rm_current_tax_table`): the one current
  delivery charge schedule and tax table, replaced only by a higher `version`.
- **CheckoutReadiness** is not stored: it is derived from the cart at read time
  (every group selected, no selected offer expired).

### The synchronous call

`QuoteDeliveryOffers`: `POST /delivery-quotes` on delivery-promise (the network's
Open Host Service route for the `shopper / storefront` caller). The outbound port is
`ports.QuoteOffers`; the HTTP adapter is `internal/adapters/outbound/deliverypromise`
with a 5 s timeout (`DELIVERY_PROMISE_TIMEOUT`); the fake is
`memory.Quoter`. A contract test checks the request and a response example against a
byte copy of the network OpenAPI (`internal/adapters/outbound/deliverypromise/testdata`,
source commit recorded in its README). The cart never calls an in-org or restricted
route (`POST /promises`, `/promises/{promiseId}/confirmations`, `/promises/{promiseId}/cancellations`).

## Consequences

- A cart keeps working while pricing or offer-availability are down; the estimate is
  merely `estimateComplete=false` until the views have data.
- The views rebuild from the producers' topics (replay) or the producers' OHS reads.
