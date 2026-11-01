---
id: class-diagram
title: Class diagrams
sidebar_label: Class diagram
description: "UML class diagrams of Shopping Cart as built: the Cart aggregate and its quote, the estimate, events and policies, and the hexagonal ports and adapters."
---

# Class diagrams

UML class diagrams of the `internal/domain/**` packages as built, plus the
hexagonal ports and adapters. Fields are unexported in Go and shown with `-`;
methods are shown with `+`. Exported domain types: package `cart`: `ID`, `State`,
`Line`, `Cart`, `CheckoutReadiness`, `Tier`, `QuotedOffer`, `QuoteGroup`,
`TierUnavailable`, `DeliveryQuote`, `Selection`, `Snapshot` and the four events
`CartLineAdded`, `CartDeliveryQuoted`, `CartAbandoned`, `CartCheckedOut`; package
`estimate`: `TierCharge`, `Schedule`, `TaxBand`, `TaxTable`, `Group`, `Input`,
`Estimate`. The identifiers are on [Ubiquitous language](/docs/ddd/ubiquitous-language).

## 1. The Cart aggregate and its held quote

```mermaid
classDiagram
    direction TB
    class Cart {
        <<AggregateRoot>>
        -id ID
        -shopperID string
        -state State
        -lines Line[]
        -destination string
        -quote DeliveryQuote nullable
        -selections Selection[]
        -createdAt time.Time
        -lastChangedAt time.Time
        -version int64
        +Create(id, shopperID, now)$ Cart
        +Restore(snapshot)$ Cart
        +AddLine(item, quantity, purchasable, now) error
        +SetLineQuantity(item, quantity, now) error
        +RemoveLine(item, now) error
        +SetDestination(prefix, now) error
        +QuoteDelivery(quote, now) error
        +SelectDelivery(groupNo, offerID, now) error
        +AcceptRequotedOffers(offers, now) error
        +MergeInto(target, now) error
        +AbandonInactive(now) error
        +CloseOnCheckoutCompleted(sessionID, now) error
        +Readiness(now) CheckoutReadiness
        +PullEvents() Event[]
    }
    class State {
        <<Enumeration>>
        OPEN
        CHECKED_OUT
        ABANDONED
        MERGED
    }
    class Line {
        <<ValueObject>>
        +CatalogItemID string
        +Quantity int
    }
    class DeliveryQuote {
        <<ValueObject>>
        +Groups QuoteGroup[]
        +Unavailable TierUnavailable[]
        +QuotedAt time.Time
    }
    class QuoteGroup {
        <<ValueObject>>
        +GroupNo int
        +LineNos int[]
        +Offers QuotedOffer[]
    }
    class QuotedOffer {
        <<ValueObject>>
        +OfferID string
        +GroupNo int
        +Tier Tier
        +DeliverByEarliest string
        +DeliverByLatest string
        +ExpiresAt time.Time
    }
    class TierUnavailable {
        <<ValueObject>>
        +Tier Tier
        +Constraint string
        +GroupNo int
    }
    class Selection {
        <<ValueObject>>
        +GroupNo int
        +OfferID string
    }
    class Tier {
        <<Enumeration>>
        SAME_DAY
        NEXT_DAY
        TWO_DAY
        STANDARD
    }
    class CheckoutReadiness {
        <<ValueObject>>
        +Ready bool
        +AllGroupsSelected bool
        +NoOfferExpired bool
    }
    Cart --> State : state
    Cart *-- "0..100" Line : lines
    Cart *-- "0..1" DeliveryQuote : quote
    Cart *-- "0..*" Selection : selections
    DeliveryQuote *-- "0..*" QuoteGroup : groups
    DeliveryQuote *-- "0..*" TierUnavailable : unavailable
    QuoteGroup *-- "0..*" QuotedOffer : offers
    QuotedOffer --> Tier : tier
    TierUnavailable --> Tier : tier
    Cart ..> CheckoutReadiness : derives
```

Source: `internal/domain/cart/cart.go`, `lines.go`, `delivery.go`, `quote.go`,
`lifecycle.go`, `requote.go`, `restore.go`.

Omitted: the `Err...` variables (listed on the
[Aggregate design canvas](/docs/ddd/aggregate-design-canvas)) and the quote
clearing, which every mutating line or destination method performs. `Tier` is the
delivery-promise vocabulary, re-declared locally: there is no shared kernel.

## 2. Estimate, read models, events and policies

```mermaid
classDiagram
    direction TB
    class Item {
        <<ReadModelRow>>
        +CatalogItemID string
        +Catalogued bool
        +Discontinued bool
        +Priced bool
        +PriceMinor int64
        +Currency string
        +PriceVersion int64
        +Purchasable() bool
    }
    class Schedule {
        <<ReadModel>>
        +Version int64
        +Tiers map Tier to TierCharge
    }
    class TaxTable {
        <<ReadModel>>
        +Version int64
        +Bands TaxBand[]
        +RateFor(prefix) int
    }
    class Estimate {
        <<ValueObject>>
        +Currency string
        +ItemsMinor int64
        +DeliveryMinor int64
        +TaxMinor int64
        +TotalMinor int64
        +Complete bool
    }
    class Compute {
        <<Function>>
        estimate.Compute(Input) Estimate
    }
    Compute ..> Estimate : derives
    Compute ..> Schedule : reads
    Compute ..> TaxTable : reads
    Compute ..> Item : reads prices
    class CartLineAdded {
        <<DomainEvent>>
        +CartID ID
        +CatalogItemID string
        +Quantity int
    }
    class CartDeliveryQuoted {
        <<DomainEvent>>
        +CartID ID
        +DestinationPostalPrefix string
        +GroupCount int
    }
    class CartAbandoned {
        <<DomainEvent>>
        +CartID ID
        +LastChangedAt time.Time
    }
    class CartCheckedOut {
        <<DomainEvent>>
        +CartID ID
        +CheckoutSessionID string
    }
```

```mermaid
classDiagram
    direction TB
    class AbandonInactive {
        <<Policy>>
        an OPEN cart with no change
        for 30 days is ABANDONED
    }
    class PurgeAbandonedCarts {
        <<Policy>>
        a non-OPEN cart idle for
        30 days is deleted
    }
    class CloseOnCheckoutCompleted {
        <<Policy>>
        on CheckoutCompleted
        the cart is CHECKED_OUT
    }
    class CartAbandoned {
        <<DomainEvent>>
    }
    class CartCheckedOut {
        <<DomainEvent>>
    }
    AbandonInactive ..> CartAbandoned : leads to
    CloseOnCheckoutCompleted ..> CartCheckedOut : leads to
```

Source: `internal/domain/estimate/estimate.go`,
`internal/application/readmodel/readmodel.go`,
`internal/domain/cart/events.go`, `internal/application/usecases/policies.go`,
`consume.go`.

Omitted: the CloudEvents envelope (built in the Kafka adapter). Every event also
embeds `shared.Header` (the aggregate version after the change and the instant).
The estimate is never stored: `estimate.Compute` derives it at read time and the
cart view labels it non-binding through `Complete`.

## 3. Hexagonal ports and adapters

```mermaid
flowchart TB
    subgraph IN["internal/adapters/inbound"]
        HTTP["http<br/>REST writes and reads,<br/>probes"]
        A2A["a2a<br/>get_cart, list_shopper_carts<br/>(cmd/a2a)"]
        KC["kafka<br/>consumers: offer-availability,<br/>pricing, checkout"]
        SCH["scheduler<br/>abandon and purge sweeps"]
    end
    APP["<b>internal/application/usecases</b><br/>CreateCart, AddLine, SetLineQuantity<br/>RemoveLine, SetDestination<br/>QuoteDelivery, SelectDelivery<br/>AcceptRequotedOffers, MergeInto<br/>GetCart, ListCarts<br/>AbandonInactive, PurgeAbandonedCarts<br/>Consumer.Apply*"]
    DOM["<b>internal/domain</b><br/>cart.Cart, cart.DeliveryQuote<br/>cart.QuotedOffer, cart.Selection<br/>estimate.Estimate"]
    PORTS["<b>internal/application/ports</b><br/>CartRepository, ItemRepository<br/>ScheduleRepository, TaxTableRepository<br/>QuoteOffers (delivery-promise)<br/>EventOutbox, Inbox, IdempotencyStore<br/>UnitOfWork, Clock, IDGenerator, CartMetrics"]
    subgraph OUT["internal/adapters/outbound"]
        A1["postgres<br/>repositories, outbox, inbox,<br/>idempotency store, UoW"]
        A2["kafka + outbox<br/>encoder, writer, relay"]
        A3["deliverypromise<br/>REST POST /delivery-offers:quote<br/>(the one outbound client)"]
        A4["telemetry, clock, ids"]
    end
    HTTP --> APP
    A2A --> APP
    KC --> APP
    SCH --> APP
    APP --> DOM
    APP --> PORTS
    PORTS --- A1
    PORTS --- A3
    PORTS --- A4
    A1 -. outbox rows .-> A2

    classDef outblue fill:#e7f5ff,stroke:#1c7ed6,color:#000
    class A3 outblue
```

Source: `internal/application/ports/ports.go`, `cmd/api/main.go`
(`buildAdapters`, `startConsumers`, `startOutboxRelay`, `startSchedulers`),
`cmd/a2a/main.go` (`buildDeps`),
`internal/adapters/outbound/deliverypromise/gateway.go`.

The **only outbound REST client** is the `deliverypromise` adapter behind the
`QuoteOffers` port. Omitted: the composition roots and the in-memory test double.
The scheduler is an inbound adapter: time triggers `AbandonInactive` and
`PurgeAbandonedCarts`. `GetCart` and `ListCarts` are use cases over the same
repositories; `cmd/a2a` wires only those two.
