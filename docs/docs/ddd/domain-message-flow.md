---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
description: "Four Shopping Cart scenarios as ddd-crew domain message flows: building a cart and quoting delivery, a price change landing, selecting delivery per group, and checkout closing the cart."
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling).
Four scenarios. Participants are contexts and actors. Every arrow is a message,
prefixed `cmd:` (command), `qry:` (query) or `evt:` (event). A `Note` names a rule
the context applies. Kafka is drawn as one participant. Event names are written
`entity.EventName`; the full type is
`com.retailnetwork.<tier>.<producer>.<entity>.<EventName>` (see
[Domain events](/docs/ddd/domain-events)). REST path parameters are written without
braces. The storefront-wide flows follow from the
[storefront context map](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/context-map).

## 1. A shopper builds a cart and quotes delivery

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor S as Shopper
    participant BFF as storefront-bff
    participant SC as shopping-cart
    participant DP as delivery-promise
    participant K as Kafka analytics
    S->>BFF: add to cart, set destination
    BFF->>SC: cmd: CreateCart<br/>POST /carts
    SC-->>BFF: cmd: reply 201 cartId
    BFF->>SC: cmd: AddLine<br/>POST /carts/cartId/lines
    Note over SC: ErrItemNotPurchasable against<br/>PurchasabilityView,<br/>ErrQuantityOutOfRange,<br/>ErrTooManyLines
    SC->>K: evt: cart.CartLineAdded
    SC-->>BFF: cmd: reply 200 cart
    BFF->>SC: cmd: SetDestination<br/>POST /carts/cartId:set-destination
    Note over SC: clears quote and selections
    BFF->>SC: cmd: QuoteDelivery<br/>POST /carts/cartId:quote-delivery
    Note over SC: ErrNoDestination without a prefix
    SC->>DP: qry: QuoteDeliveryOffers<br/>POST /delivery-offers:quote
    DP-->>SC: qry: reply groups, offers,<br/>windows, expiries,<br/>unavailable tiers
    Note over SC: quote held, holds no capacity
    SC->>K: evt: cart.CartDeliveryQuoted
    SC-->>BFF: cmd: reply 200 cart with quote<br/>and labelled estimate
```

Source: `internal/adapters/inbound/http/server.go` (routes),
`internal/application/usecases/cart_commands.go`, `quote_delivery.go`,
`internal/domain/cart/lines.go`, `delivery.go`,
`internal/adapters/outbound/deliverypromise/gateway.go`,
`internal/adapters/outbound/kafka/encoder.go`.

Omitted: the outbox write and relay (see
[Sequence diagrams](/docs/ddd/sequence-diagrams)); the merge of a re-added item's
quantities (the event carries the merged line); `SetLineQuantity`, `RemoveLine` and
`MergeInto`, which share `AddLine`'s shape without its event.

## 2. A price, charge or tax change lands

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    participant PR as pricing
    participant K as Kafka
    participant SC as shopping-cart
    participant BFF as storefront-bff
    PR->>K: evt: itemprice.ItemPriceChanged
    K->>SC: evt: itemprice.ItemPriceChanged
    Note over SC: rm_items effective price,<br/>applied if the version is newer
    PR->>K: evt: deliverychargeschedule.<br/>DeliveryChargeScheduleChanged
    K->>SC: evt: deliverychargeschedule.<br/>DeliveryChargeScheduleChanged
    Note over SC: rm_current_schedule replaced<br/>if the version is higher
    PR->>K: evt: taxtable.TaxTableChanged
    K->>SC: evt: taxtable.TaxTableChanged
    Note over SC: rm_current_tax_table replaced<br/>if the version is higher
    Note over SC: no cart event:<br/>lines and quote untouched
    BFF->>SC: qry: GetCart GET /carts/cartId
    SC-->>BFF: qry: reply cart,<br/>estimate recomputed at read
```

Source: `internal/adapters/inbound/kafka/handlers_pricing.go`,
`internal/application/usecases/consume.go`,
`internal/application/usecases/view.go` (`estimateFor`).

Omitted: `ItemPriceWithdrawn` (same channel: the item stops being purchasable and
the estimate can no longer price the line) and `CatalogItemCreated` /
`CatalogItemDiscontinued` from offer-availability, which feed `rm_items` the same
way. The quote is never cleared by a price event; only a line or destination change
clears it.

## 3. The shopper selects a delivery offer per group

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor S as Shopper
    participant BFF as storefront-bff
    participant SC as shopping-cart
    S->>BFF: pick a tier per shipment group
    BFF->>SC: cmd: SelectDelivery<br/>POST /carts/cartId:select-delivery
    alt the offer is not of the current quote
        SC-->>BFF: cmd: reply 409<br/>ErrStaleDeliverySelection
    else quote already cleared by a change
        SC-->>BFF: cmd: reply 409<br/>ErrStaleDeliverySelection
    else the offer belongs to the quote
        Note over SC: selections groupNo to offerId.<br/>estimate adds the tier charges.<br/>CheckoutReadiness: every group<br/>selected, no offer expired
        SC-->>BFF: cmd: reply 200 cart,<br/>estimate, readiness
    end
```

Source: `internal/domain/cart/delivery.go` (`SelectDelivery`, `hasOffer`,
`Readiness`), `internal/application/usecases/cart_commands.go`,
`internal/domain/estimate/estimate.go`.

Omitted: the expiry comparison (readiness compares each `QuotedOffer`'s `expiresAt`
with the clock; no event announces an expiry) and the estimate arithmetic.

## 4. Checkout reads the cart and completes it

```mermaid
%%{init: {"sequence": {"wrap": true, "width": 120, "actorMargin": 24, "messageMargin": 28, "boxMargin": 6, "noteMargin": 6, "mirrorActors": false}}}%%
sequenceDiagram
    autonumber
    actor S as Shopper
    participant CK as checkout
    participant SC as shopping-cart
    participant K as Kafka
    participant AN as Kafka analytics
    S->>CK: cmd: OpenSession<br/>POST /checkout-sessions
    CK->>SC: qry: GetCart<br/>GET /carts/cartId
    Note over CK: ErrCartNotCheckoutReady:<br/>the cart's CheckoutReadiness<br/>must hold at session open
    SC-->>CK: qry: reply cart, version,<br/>groups, selections, readiness
    Note over CK: saga runs: quote, authorize,<br/>place the order (checkout's own pages)
    CK->>K: evt: checkoutsession.<br/>CheckoutCompleted
    K->>SC: evt: checkoutsession.<br/>CheckoutCompleted
    Note over SC: CloseOnCheckoutCompleted:<br/>OPEN to CHECKED_OUT<br/>acknowledged if not OPEN
    SC->>AN: evt: cart.CartCheckedOut<br/>checkoutSessionId
```

Source: `checkout/internal/adapters/outbound/shoppingcart/gateway.go` (the caller of
`GET /carts/{cartId}`), `internal/application/usecases/consume.go`
(`ApplyCheckoutCompleted`), `internal/domain/cart/lifecycle.go`
(`CloseOnCheckoutCompleted`).

Omitted: the saga itself (pricing's `PriceQuote`, `customer-payments`, the network's
order placement through checkout's anti-corruption layer; checkout's own pages), the
inbox on the CloudEvents `id`, and the offer pre-check checkout runs against
delivery-promise before authorizing, which is checkout's edge, not the cart's.
