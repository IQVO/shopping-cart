---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: "The terms Shopping Cart speaks, each mapped to its code identifier, plus the neighbour terms it uses and the terms most often confused with them."
---

# Ubiquitous language

The terms this context speaks, each mapped to the identifier that implements it. The
first table is the context's own vocabulary, the second the model elements of its
aggregate, the third neighbour terms it uses but does not own (their definitions
come from the owning context). The storefront-wide glossary is
[Ubiquitous Language](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/ubiquitous-language)
in warehouse-docs.

## Own terms

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Cart** | One shopper's held intent: lines, destination, delivery quote and selections. | `cart.Cart` | The network's customer order (`customer-ordering`): a cart is intent, not a transaction. |
| **Delivery Quote (cart)** | The offers delivery-promise returned for the cart's lines and destination, held by the cart; a quote holds no capacity. | `cart.DeliveryQuote` | The network's **DeliveryOffer** (one offer). |
| **Delivery Selection** | The offer the shopper picked per shipment group. | `cart.Selection` | The network's **Promise**: selecting an offer holds nothing; capacity is held only when checkout places the order. |
| **Estimate** | The labelled, non-binding total in the cart: effective prices plus selected tier charges plus tax band. | `estimate.Estimate` | The binding **PriceQuote** of a checkout session. |
| **Checkout Readiness** | Every group selected and no selected offer expired. | `cart.CheckoutReadiness`, `Cart.Readiness` | A checkout session's own state: readiness is a fact about the cart that checkout enforces at session open. |

## Model elements of this context

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Cart Line** | One item with a quantity; the only line shape the cart stores. | `cart.Line{CatalogItemID, Quantity}` | A checkout session's numbered line: the session numbers lines 1..n when it opens. |
| **Quoted Offer** | One offer held by the quote: `offerId`, `groupNo`, `tier`, delivery dates and expiry. | `cart.QuotedOffer` | The network's `DeliveryOffer` aggregate, which owns the witness path behind the window. |
| **Shipment Group (cart)** | One group of the quote: the lines that ship together (by 1-based `LineNos`) and its offers. | `cart.QuoteGroup` | The network's shipment group in delivery-promise's `Promise`: the cart mirrors the grouping the quote returned. |
| **Tier Unavailable** | Why a tier has no offer, with a constraint and an optional group. | `cart.TierUnavailable` | A tier that was never requested. |
| **Tier** | `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`. | `cart.Tier` | The delivery **charge** (pricing's tier charge): the tier is the promise's vocabulary, re-declared locally. |
| **Destination Postal Prefix** | The 1 to 10 character prefix the destination resolves to; the input both the quote and the tax band key on (the tax band uses the first three characters). | `Cart.DestinationPostalPrefix`, `cart.ValidPrefix` | A ship-to address: PII that lives only in `customer-ordering`. |
| **Cart States** | `OPEN`, `CHECKED_OUT`, `ABANDONED`, `MERGED`. | `cart.State` | The checkout session's states: a `CHECKED_OUT` cart is the effect of a completed session, not a session state. |
| **Purchasability View** | The local, event-fed verdict of whether an item may sit in a cart: catalogued, not discontinued, priced. | table `rm_items`, `readmodel.Item.Purchasable` | `product-discovery`'s **Purchasable**: more inputs. |
| **Estimate View** | The local, event-fed prices, the current delivery charge schedule and the current tax table the estimate is computed from. | `rm_items` prices, `estimate.Schedule`, `estimate.TaxTable`, tables `rm_current_schedule`, `rm_current_tax_table` | A stored total: the estimate is derived at read time. |
| **Estimate Complete** | True only when every line is priced, every group is selected with a known charge and a tax band covers the prefix. | `estimate.Estimate.Complete`, wire `estimateComplete` | A guarantee of the final price. |
| **Abandon Inactive** | Policy: an `OPEN` cart with no change for 30 days is `ABANDONED`. | `usecases.AbandonInactive`, `cart.IdleAfter` | Deletion. |
| **Purge Abandoned Carts** | Policy: a cart that is not `OPEN` and unchanged for 30 days is deleted; raises no event. | `usecases.PurgeAbandonedCarts` | Abandonment. |
| **Close On Checkout Completed** | Policy: on `CheckoutCompleted` the cart is `CHECKED_OUT`. | `Consumer.ApplyCheckoutCompleted`, `Cart.CloseOnCheckoutCompleted` | Checkout's own completion. |
| **Merge** | Take a guest cart's (or the same shopper's) lines into an open cart of a shopper; the source becomes `MERGED`. | `Cart.MergeInto`, `usecases.MergeInto` | Merging quantities of one item inside a cart (`AddLine`). |
| **Requote** | Checkout's replacement of the offers; the cart accepts them without calling delivery-promise. | `Cart.AcceptRequotedOffers` | `QuoteDelivery`, which does call delivery-promise. |
| **Idempotency Key** | The caller's token that makes a `POST` safe to retry for 24 hours. | header `Idempotency-Key`, table `idempotency_keys` | The CloudEvents `id`, which makes consumers idempotent. |
| **Outbox / Inbox** | The tables that make publishing and consuming exactly-once in effect. | `outbox_events`, `processed_events` | Kafka offsets. |

## Neighbour terms this context uses

| Term | Definition (owner's) | Owner |
| --- | --- | --- |
| **DeliveryOffer** | A short-lived quote for (item, quantity, destination prefix, tier) with a delivery window, the witness path that proves it, and an expiry. | `delivery-promise` ([network](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise)). Not to be confused with the cart's **Delivery Quote**, the set of such offers held by the cart. |
| **Service Tier** | `SAME_DAY`, `NEXT_DAY`, `TWO_DAY` or `STANDARD`. | `delivery-promise`. |
| **Effective Price** | The lowest active sale price, else the list price. | `pricing`. One of the estimate's three inputs. |
| **Price Quote** | A binding, immutable, expiring price snapshot for one checkout. | `pricing`. Not the cart's non-binding **Estimate**. |
| **Checkout Session** | The purchase transaction from review to placed order. | `checkout`. It reads the cart at open and closes it on completion; the cart learns of a session only at `CheckoutCompleted`. |
| **Purchasable** | Item active, content published, price active, and at least one live listing with stock. | `product-discovery`. The cart's **Purchasability View** is narrower. |
| **Catalog Item** | The network's own entry for a product, independent of who ships it. | `offer-availability` ([network](https://iqvo.github.io/warehouse-docs/network/contexts/offer-availability)). A cart line references its `catalogItemId` and adds the shopper's quantity. |

## Fleet translation

None. The cart never touches the warehouse-systems fleet and never produces or
consumes a `warehouse.*` topic.

Source: `internal/domain/cart/*.go`, `internal/domain/estimate/estimate.go`,
`internal/application/readmodel/readmodel.go`,
`internal/application/usecases/*.go`.
