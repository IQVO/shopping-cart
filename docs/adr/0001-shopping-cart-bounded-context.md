# 1. Shopping Cart is a bounded context of the storefront domain

Status: Accepted (bootstrap). Design source: warehouse-docs `storefront/model/model.yaml`, context `shopping-cart`.

## Context

The storefront is the e-commerce domain of the retail-network organisation: six bounded contexts, each its own
service and database. Holds a shopper's intent: cart lines, the destination postal prefix, the delivery quote delivery-promise returned for the cart (per shipment group, with its offers and expiry), the shopper's tier selection per group, and a labelled, non-binding estimate (effective prices + selected tier charges + tax band) so the total cost is visible up front [1].

## Decision

- This repo owns the aggregates Cart; the invariants and error names are those of the model.
- Business decisions of the context:
- One line per item; adding an item already in the cart merges quantities.
- A quote holds no capacity: it is the set of offers delivery-promise returned; a selection must name an offer of the current quote.
- Any line or destination change clears the quote and the selections.
- The estimate is labelled non-binding; the binding total is the checkout session's PriceQuote (gap S2).
- Guest-first: shopperId is optional; sign-in is a synthetic shopper lookup by e-mail (D4).
- Events use the storefront CloudEvents namespace `com.retailnetwork.storefront.shopping-cart.<entity>.<Event>` on `retailnetwork.shopping-cart.events`
  (analytics-only events on `.analytics`). Published: CartLineAdded, CartDeliveryQuoted, CartAbandoned, CartCheckedOut. Consumed: CatalogItemCreated, CatalogItemDiscontinued, CheckoutCompleted, DeliveryChargeScheduleChanged, ItemPriceChanged, ItemPriceWithdrawn, TaxTableChanged. No `warehouse.*` topic is consumed or produced.
- The storefront calls only the network's Open Host Service routes (model inbound rows for `shopper / storefront`),
  never an in-org or restricted route; sibling data used in a decision is a local event-fed read model, except the
  calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet and the network. This context stores no PII and no card data.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
