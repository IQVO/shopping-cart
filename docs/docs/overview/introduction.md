---
id: introduction
title: Introduction
sidebar_label: Introduction
sidebar_position: 1
description: "What the Shopping Cart bounded context owns and does not own: cart lines, destination, delivery quote and selections, and the labelled non-binding estimate. Supporting subdomain of the storefront."
---

# Shopping Cart

`shopping-cart` is a **Supporting** bounded context of the **Storefront** area
(code `SC`, tier `storefront`, CloudEvents types
`com.retailnetwork.storefront.shopping-cart.cart.<EventName>`). It holds a
shopper's intent between "I want this" and "I pay for this": the lines, the
destination postal prefix, the delivery quote that
[delivery-promise](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise)
returned for them, the tier the shopper picked for each shipment group, and a
**labelled, non-binding estimate** (effective prices, plus the selected tiers'
charges, plus the destination's tax band) so the total cost is visible up front.
The binding total is never the cart's: it is the checkout session's price quote.

:::info What runs on develop
Two binaries, both on real Postgres only. `cmd/api` serves REST (`:8080`) and runs
the transactional outbox relay, three always-on Kafka consumers
(offer-availability, pricing, checkout) and two clock-driven sweeps in one
process; it also needs real Kafka and the delivery-promise service. `cmd/a2a`
(`:8093`, [ADR 0006](/docs/adr/0006-a2a-inbound-adapter)) is a separate, read-only
Agent2Agent agent over the same database. There is no in-memory mode, no MCP
server and no authentication: REST and A2A are unauthenticated fleet-wide.

Packaged: a `Dockerfile` that builds every `cmd/*`, and the Helm chart
`charts/shopping-cart` (the A2A component is off by default). The kind cluster
deploys it through warehouse-infra, which also ships a Grafana dashboard and
alert rules for it ([Observability](/docs/operations/observability)).
:::

## What the context owns

| Concept | Where it lives | Rule in one line |
| --- | --- | --- |
| **Cart** (aggregate root) | `internal/domain/cart` | One shopper's intent. States `OPEN`, `CHECKED_OUT`, `ABANDONED`, `MERGED`; the last three are terminal. Optimistic `version`. `shopperId` is optional (guest-first). |
| **Lines** | `Cart` | One line per catalogue item, quantity 1 to 99, at most 100 lines. Adding an item already in the cart merges quantities. |
| **Destination** | `Cart` | A postal **prefix** of 1 to 10 letters or digits, never an address. |
| **Delivery quote and selections** | `Cart` | The groups and offers delivery-promise returned (a quote holds no capacity), one selected offer per group. Any line or destination change clears both. |
| **Checkout readiness** | derived | Every group selected and no selected offer expired. Expiry is read from the clock, not stored. |
| **Estimate** | `internal/domain/estimate` | Items, delivery, tax and total in minor units, with `estimateComplete` saying whether everything it needs was known. |
| **Local read models** | tables `rm_items`, `rm_current_schedule`, `rm_current_tax_table` | Event-fed copies of "is the item catalogued, not discontinued and priced" and of the current delivery charge schedule and tax table ([ADR 0003](/docs/adr/0003-consumed-contracts-and-local-read-model)). Not aggregate state. |

## What it does not own

| Not here | Where it lives |
| --- | --- |
| Item prices, tier charges, tax bands, the binding price quote | `pricing` (Core). The cart consumes its events and never calls it. |
| Feasible delivery paths, offers, capacity, promises | `delivery-promise` (network). The cart calls it once, to quote. |
| The purchase saga and the session that closes the cart | `checkout`. It reads the cart over REST and announces `CheckoutCompleted`. |
| Orders, ship-to addresses, shopper PII | `customer-ordering` (network). The cart stores a `shopperId` reference and a postal prefix only. |
| The catalogue and purchasability verdicts for search | `offer-availability` and `product-discovery`. |
| Payments, product content, identity | `customer-payments`, `product-content`, the deferred identity context. |

## Classification and area

- **Supporting subdomain, Storefront area.** The shopper does not choose a
  storefront for its cart, but a broken cart loses the sale. The differentiated
  parts (the price of speed, the binding quote) belong to pricing
  ([Subdomain classification](/docs/ddd/subdomain-classification)).
- **Business model: conversion.** Hold the intent across sessions (guest-first,
  merge on sign-in), keep it honest (estimate labelled, quote cleared on every
  change) and hand checkout a cart that is ready.

## Main capabilities

1. Create a cart (guest or for a shopper), add, re-quantify and remove lines, set
   the destination prefix (`POST /carts`, `/carts/{cartId}/lines`, `:set-quantity`,
   `:remove`, `:set-destination`).
2. Quote delivery through delivery-promise (`:quote-delivery`), the cart's only
   synchronous call, and select one offer per shipment group (`:select-delivery`).
3. Merge a guest cart into a shopper's open cart at sign-in (`:merge-into`) and
   accept the offers of a checkout requote without a call to delivery-promise
   (`/carts/{cartId}/delivery/requoted`).
4. Read a cart with its quote, selections, estimate and checkout readiness
   (`GET /carts/{cartId}`), or a shopper's carts (`GET /carts?shopperId=&state=`);
   the same two reads are served to agents as the A2A skills `get_cart` and
   `list_shopper_carts`.
5. Keep the purchasability and estimate views current from Kafka
   (`CatalogItemCreated`, `CatalogItemDiscontinued`, `ItemPriceChanged`,
   `ItemPriceWithdrawn`, `DeliveryChargeScheduleChanged`, `TaxTableChanged`).
6. Close a cart when checkout completes (`CheckoutCompleted` makes it
   `CHECKED_OUT`), abandon carts idle for 30 days and purge closed carts 30 days
   later.
7. Publish four analytics events, `CartLineAdded`, `CartDeliveryQuoted`,
   `CartAbandoned` and `CartCheckedOut`, on `retailnetwork.shopping-cart.analytics`
   through the transactional outbox ([Events](/docs/api-reference/events)).

## Design decisions in force

These are the business decisions of the model as built (ADR
[0001](/docs/adr/0001-shopping-cart-bounded-context),
[0002](/docs/adr/0002-shopping-cart-assumptions)):

1. One line per item; re-adding merges quantities (no duplicate-line error).
2. A quote holds no capacity; a selection must name an offer of the **current**
   quote.
3. Any line or destination change clears the quote and the selections; a stale
   quote is never silently kept.
4. The estimate is labelled non-binding and says whether it is complete.
5. Guest-first: `shopperId` is optional, and merging is an explicit command.
6. Removal, quantity changes, destination, selection and merge raise no event;
   analytics only sees adds, quotes, abandonment and checkout.

## Where to go next

| You want to | Read |
| --- | --- |
| See the layers, packages, binaries and the rules that police them | [Architecture](/docs/overview/architecture) |
| Run it and make the first calls | [Quickstart](/docs/overview/quickstart) |
| Know every setting | [Configuration](/docs/operations/configuration) |
| Deploy and operate it | [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Before first release](/docs/operations/before-first-release) |
| Fix something that broke | [Troubleshooting](/docs/operations/troubleshooting) |
| Know what tests and CI gates exist | [Testing](/docs/development/testing) |
| See who talks to whom | [Integration](/docs/ecosystem/integration) |
| See the use cases and their invariants | [Use cases](/docs/ddd/use-cases) |
| Read the design as ddd-crew artifacts | [DDD artifacts](/docs/ddd/ddd-artifacts), [Ubiquitous language](/docs/ddd/ubiquitous-language) |
| Read the contracts | [API reference](/docs/api-reference), [Event catalogue](/docs/api-reference/events) |
| Read the decisions | [ADR index](/docs/adr) |

Published at [https://iqvo.github.io/shopping-cart/](https://iqvo.github.io/shopping-cart/).
