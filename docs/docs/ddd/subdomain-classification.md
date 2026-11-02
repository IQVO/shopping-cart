---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: "Why Shopping Cart is a Supporting subdomain of the storefront, with the reasoning from the code and the classification of its neighbours as recorded in the warehouse-docs strategic design."
---

# Subdomain classification

**Shopping Cart is a Supporting subdomain** (area Storefront, tier `storefront`,
business model *conversion*, evolution *custom-built*). The classification is the
aggregator's verdict; this page gives the reasoning from the code and copies the
neighbours' classes from warehouse-docs
([storefront Subdomain Classification](https://iqvo.github.io/warehouse-docs/storefront/strategic-design/subdomain-classification),
[network Subdomain Classification](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification)).

## Why Supporting

Domain-Driven Design classifies by competitive advantage, not by size or
difficulty.

- **A shopper does not choose a storefront for its cart, but a broken cart loses
  the sale.** It must work; working is not the differentiator. That is the
  definition of Supporting.
- **Moderate model complexity.** One aggregate (`Cart`, `internal/domain/cart`),
  four states, and rules that are not CRUD: the delivery quote carries a foreign
  lifecycle (offers expire by the network's clock, so readiness is derived from
  `now`, never stored), one clearing rule ("any line or destination change clears
  the quote and the selections") touches every mutating command, and correctness
  depends on two upstream feeds (`ErrItemNotPurchasable` is only as current as
  `rm_items`, the estimate only as current as the schedule and tax table).
- **Moderate differentiation.** The quote-and-selection shape, one group per
  shipment with an offer per tier and a window and expiry each, is specific to this
  network's promise model, and no off-the-shelf cart holds it. The differentiated
  parts of the money story, the price of speed and the binding total, are not here:
  the cart's estimate is explicitly labelled non-binding, and the binding total is
  the checkout session's price quote from
  [pricing](https://iqvo.github.io/warehouse-docs/storefront/contexts/pricing).
- **Plumbing is commodity.** The outbox, inbox, CloudEvents envelope, idempotency
  keys and the 30-day abandonment sweep are the fleet's standard building blocks.
- **Evolution: custom-built.** Holding the network's per-group offers with clearing
  semantics is this design's shape; the purchasability and estimate views are
  projections negotiated with the events of other contexts, not a product to buy.

What would move it: promotions inside the estimate (a later phase) would raise its
complexity before becoming their own context; a deferred identity context would
formalise the guest-first `shopperId` and merge handling the cart now owns.

## Problem space: the storefront subdomains

Copied from warehouse-docs `storefront/docs/strategic-design/subdomain-classification.md`
(`origin/main`):

| Subdomain | What the business must be good at | Verdict |
| --- | --- | --- |
| Pricing & Delivery Charges | Item prices, sale windows, the price of speed per tier, a binding total at purchase | Core |
| Product Discovery | Finding the right purchasable item fast | Supporting |
| **Cart** | Holding intent, delivery quote and tier choice per shipment group | **Supporting** |
| Checkout | One purchase transaction across price, payment and the network's promise confirmation | Supporting |
| Product Content | Descriptions, media, attributes, categories for an item the network already knows | Generic |
| Customer Payments | Authorize, capture, release, refund through a payment provider | Generic |
| Customer Identity | Sign-in, credentials | Generic, deferred |
| Notifications / Returns / Promotions / Reviews / Recommendations | - | Later |

## Solution space: the storefront bounded contexts

| Bounded context | Classification | Tier | Business model | Evolution |
| --- | --- | --- | --- | --- |
| `pricing` | Core | `storefront` | Revenue | Custom-built |
| `product-content` | Generic | `storefront` | Engagement enabler | Commodity |
| `product-discovery` | Supporting | `storefront` | Conversion enabler | Custom-built |
| **`shopping-cart`** | **Supporting** | `storefront` | Conversion (holding intent without losing the total-cost-up-front rule) | Custom-built |
| `checkout` | Supporting | `storefront` | Conversion (the purchase transaction) | Custom-built |
| `customer-payments` | Generic | `storefront` | Revenue collection | Commodity |

The aggregator counts 3 Supporting, 2 Generic and 1 Core among the six.

## The cart's neighbours

Only the contexts this service has an edge with (see
[Integration](/docs/ecosystem/integration) and [Context map](/docs/ddd/context-map)):

| Neighbour | Area | Classification | Edge with the cart |
| --- | --- | --- | --- |
| `pricing` | Storefront | Core | upstream: Kafka events feed the price, schedule and tax views |
| `checkout` | Storefront | Supporting | reads the cart over REST; announces `CheckoutCompleted` |
| `offer-availability` | Network (`commerce`) | Supporting | upstream: Kafka catalogue events |
| `delivery-promise` | Network (`commerce`) | Core | upstream: the one synchronous REST call, `QuoteDeliveryOffers` |
| `storefront-bff` | Experience | not a bounded context | the shopper-facing caller of the REST routes |

`customer-ordering` (Generic, network) and `product-content` (Generic) have no
edge with the cart: the cart never calls the former, reaching it is checkout's job,
and it consumes no content events.
