---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
description: "Process-level EventStorming boards for Shopping Cart: cart building, the delivery quote and selection, the estimate views and the cart lifecycle policies, with the hotspots and how the code resolved them."
---

# EventStorming

Process-level boards in the ddd-crew
[EventStorming glossary and cheat sheet](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet)
notation, reconstructed from the code. Events published by a neighbour are drawn
as orange stickies prefixed with the producer. Every hotspot is a gap or a risk
visible in the design, with its resolution in the code. Boards are drawn left to
right.

## Legend

| Colour | Sticky | Meaning here |
| --- | --- | --- |
| Pale yellow `#fff59d` | Actor | The shopper (through the BFF), the clock |
| Blue `#a5d8ff` | Command | A command of the `Cart` aggregate |
| Yellow `#ffec99` | Aggregate | The `Cart` aggregate and its held quote |
| Orange `#ffc078` | Domain event | Ours (published) or a neighbour's (consumed, prefixed with the producer) |
| Lilac `#d0bfff` | Policy | `AbandonInactive`, `PurgeAbandonedCarts`, `CloseOnCheckoutCompleted` |
| Green `#b2f2bb` | Read model | `PurchasabilityView`, `EstimateView`, `CheckoutReadiness`, a read route |
| Pink `#fcc2d7` | External system | A sibling context or the network (`pricing`, `offer-availability`, `delivery-promise`, `checkout`, analytics) |
| Red `#ff8787` | Hotspot | A gap or risk (see the table at the end) |

## 1. Cart building

```mermaid
flowchart LR
    S["Shopper<br/>via storefront-bff"]:::actor
    C0["CreateCart<br/>POST /carts"]:::command
    C1["AddLine<br/>POST /carts/cartId/lines"]:::command
    C2["SetLineQuantity<br/>POST ...:set-quantity"]:::command
    C3["RemoveLine<br/>POST ...:remove"]:::command
    C4["MergeInto<br/>POST /carts/cartId:merge-into"]:::command
    C4b["AcceptRequotedOffers<br/>POST /carts/cartId/delivery/requoted<br/>OPEN to OPEN, no delivery-promise call"]:::command
    RM0["PurchasabilityView"]:::readmodel
    G1["Cart"]:::aggregate
    E1["CartLineAdded"]:::event
    AN(["analytics"]):::actor

    S --> C0
    S --> C1
    S --> C2
    S --> C3
    S --> C4
    S --> C4b
    C1 --> RM0
    RM0 -->|purchasable| G1
    C0 --> G1
    C1 --> G1
    C2 --> G1
    C3 --> G1
    C4 --> G1
    C4b --> G1
    G1 --> E1
    E1 -->|only AddLine publishes, by design| AN

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef hotspot fill:#ff8787,stroke:#c92a2a,color:#000
```

Source: `internal/application/usecases/cart_commands.go`,
`internal/domain/cart/lines.go`, `lifecycle.go`, `requote.go`, `events.go`,
`internal/application/readmodel/readmodel.go` (`Item.Purchasable`).

Omitted: the invariant errors on the add path (`ErrQuantityOutOfRange`,
`ErrTooManyLines`, `ErrItemNotPurchasable`, `ErrCartClosed`, `ErrMergeTarget`) and
the outbox. Adding an existing item merges quantities into the one line; nothing is
rejected as a duplicate. Only `AddLine` publishes, by design: analytics needs add,
quote, abandon and checkout. Every mutating command on this board clears the quote
and the selections, and `AcceptRequotedOffers` replaces them with the supplied
offers (each selected) without calling delivery-promise; checkout re-verifies the
offers before authorizing.

## 2. Destination, quote and selection

```mermaid
flowchart LR
    S["Shopper<br/>via storefront-bff"]:::actor
    C5["SetDestination<br/>POST ...:set-destination"]:::command
    C6["QuoteDelivery<br/>POST ...:quote-delivery"]:::command
    C7["SelectDelivery<br/>POST ...:select-delivery"]:::command
    DP["delivery-promise<br/>(network)"]:::external
    Q["Delivery Quote<br/>groups, QuotedOffers,<br/>unavailable tiers"]:::aggregate
    G1["Cart"]:::aggregate
    E2["CartDeliveryQuoted"]:::event
    RM1["CheckoutReadiness<br/>every group selected,<br/>no offer expired"]:::readmodel
    AN(["analytics"]):::actor
    ES["Estimate without a destination<br/>item subtotal only,<br/>estimateComplete=false"]:::readmodel

    S --> C5
    S --> C6
    S --> C7
    C6 -->|qry QuoteDeliveryOffers<br/>POST /delivery-offers:quote| DP
    DP -->|groups with offers,<br/>windows, expiries| Q
    C6 -->|ErrNoDestination<br/>without a prefix| G1
    Q --> G1
    C7 -->|selections groupNo to offerId,<br/>ErrStaleDeliverySelection| G1
    G1 --> E2
    E2 --> AN
    G1 --> RM1
    C5 --> ES

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Source: `internal/application/usecases/quote_delivery.go`,
`internal/domain/cart/delivery.go` and `quote.go`,
`internal/adapters/outbound/deliverypromise/mapping.go`,
`internal/domain/estimate/estimate.go` (`Compute`: no destination returns after
the item subtotal).

Before a destination is set the estimate shows the item subtotal only with
`estimateComplete=false`; the delivery charge and tax appear once a destination
exists. A quote holds no capacity: the offers are delivery-promise facts frozen at
quote time.

## 3. The estimate and the views

```mermaid
flowchart LR
    PR["pricing"]:::external
    OA["offer-availability<br/>(network)"]:::external
    E3["PR ItemPriceChanged,<br/>ItemPriceWithdrawn"]:::event
    E4["PR DeliveryChargeScheduleChanged,<br/>TaxTableChanged"]:::event
    E5["OA CatalogItemCreated,<br/>CatalogItemDiscontinued"]:::event
    RM0["PurchasabilityView"]:::readmodel
    RM2["EstimateView<br/>effective prices, tier<br/>charges, tax bands"]:::readmodel
    RM3["Estimate<br/>labelled, non-binding"]:::readmodel
    AL["AddLine"]:::command

    PR --> E3
    PR --> E4
    OA --> E5
    E3 --> RM0
    E5 --> RM0
    E3 --> RM2
    E4 --> RM2
    RM2 --> RM3
    RM0 -->|ErrItemNotPurchasable| AL

    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef readmodel fill:#b2f2bb,stroke:#2f9e44,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Source: `internal/application/usecases/consume.go`,
`internal/adapters/inbound/kafka/handlers.go` and `handlers_pricing.go`,
`internal/application/usecases/view.go`.

The cart is lenient by design: it accepts any catalogued, priced, non-discontinued
item, and checkout's binding quote is the gate. No pricing or catalogue event
touches the `Cart` aggregate: a price move never clears a quote, it moves the
estimate, which is why the estimate is labelled non-binding.

## 4. Cart lifecycle

```mermaid
flowchart LR
    T(["Clock"]):::actor
    CK["checkout"]:::external
    E6["CK CheckoutCompleted"]:::event
    P1["AbandonInactive<br/>no change for 30 days"]:::policy
    P2["CloseOnCheckoutCompleted"]:::policy
    C8["AbandonInactive"]:::command
    C9["CloseOnCheckoutCompleted"]:::command
    G1["Cart<br/>OPEN to terminal states"]:::aggregate
    E7["CartAbandoned"]:::event
    E8["CartCheckedOut"]:::event
    AN(["analytics"]):::actor
    P3["PurgeAbandonedCarts<br/>closed and idle 30 days,<br/>deleted, no event"]:::policy

    T --> P1
    P1 --> C8
    E6 --> P2
    P2 --> C9
    C8 --> G1
    C9 --> G1
    CK --> E6
    G1 --> E7
    G1 --> E8
    E7 --> AN
    E8 --> AN
    G1 --> P3
    T --> P3

    classDef actor fill:#fff59d,stroke:#b59f00,color:#000
    classDef command fill:#a5d8ff,stroke:#1971c2,color:#000
    classDef aggregate fill:#ffec99,stroke:#e67700,color:#000
    classDef event fill:#ffc078,stroke:#d9480f,color:#000
    classDef policy fill:#d0bfff,stroke:#6741d9,color:#000
    classDef external fill:#fcc2d7,stroke:#c2255c,color:#000
```

Source: `internal/application/usecases/policies.go`, `consume.go`
(`ApplyCheckoutCompleted`), `internal/domain/cart/lifecycle.go` (`IdleAfter`,
`AbandonInactive`, `CloseOnCheckoutCompleted`),
`internal/adapters/inbound/scheduler/scheduler.go`.

Omitted: `MergeInto` (board 1) and the inbox. `CHECKED_OUT`, `ABANDONED` and
`MERGED` are terminal: `ErrCartClosed` rejects every later change. A
`CheckoutCompleted` for a cart that is not `OPEN` has no transition; it is
acknowledged and logged.

## Hotspots

| # | Hotspot | Resolution in the code |
| --- | --- | --- |
| H1 | Only `AddLine` (plus the quote, abandon and checkout transitions) publishes; removals, quantity changes, destination changes, selections and merges raise nothing. | By design: analytics needs add, quote, abandon and checkout. `internal/domain/cart/cart.go` documents it ("Removal, merge and quantity changes raise no event, by design"). |
| H2 | Adding an existing item: duplicate error or merge? | Merge. There is no duplicate-line error; the merged quantity must stay in 1 to 99 (`Cart.AddLine`). |
| H3 | `PurchasabilityView` is narrower than discovery's `Purchasable`. | Lenient by design (`readmodel.Item.Purchasable`: catalogued, not discontinued, priced); checkout's price quote is the gate. |
| H4 | No cart consumer exists. | All four events are analytics-only; checkout reads the cart synchronously. Undecided whether a sibling should ever consume one. |
| H5 | Who purges abandoned carts? | The policy `PurgeAbandonedCarts` (scheduler, every `PURGE_SWEEP_INTERVAL`) deletes non-open carts idle for 30 days; it raises no event. |
| H6 | The estimate before a destination exists. | The item subtotal only, `estimateComplete=false`; delivery and tax appear once a destination is set. |
| H7 | `AcceptRequotedOffers` has no caller. | The route is served and tested; no caller was found in the local checkouts of `checkout` and `storefront-bff`. |
