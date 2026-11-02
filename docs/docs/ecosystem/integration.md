---
id: integration
title: Integration
sidebar_label: Integration
description: "Every upstream and downstream of Shopping Cart: protocol, contract, failure behaviour, and the edges that are absent."
---

# Integration

Shopping Cart integrates through exactly three channels: **one synchronous REST
call** out (delivery-promise), **Kafka events** in (offer-availability, pricing,
checkout) and out (analytics), and the **REST and A2A reads** others make of it.
It never calls a sibling's restricted route, never imports another context's Go
packages (it restates the contracts it needs; the strings are identical on both
sides) and never touches a `warehouse.*` topic. The context map is on
[Context map](/docs/ddd/context-map); the classification of each neighbour is on
[Subdomain classification](/docs/ddd/subdomain-classification).

## Edge summary

| Neighbour | Direction | Protocol | Contract | Failure behaviour |
| --- | --- | --- | --- | --- |
| `delivery-promise` (network, Core) | cart calls it | REST `POST /delivery-offers:quote` | the network's Open Host Service route; the cart restates the one route and its DTOs in `internal/adapters/outbound/deliverypromise` | timeout, transport error, 5xx or undecodable answer: `503` `delivery-promise-unavailable`; 4xx: reported the same way (a contract bug); nothing is stored, retry with the same `Idempotency-Key` |
| `offer-availability` (network, Supporting) | it publishes, cart consumes | Kafka `retailnetwork.offer-availability.events` | `CatalogItemCreated`, `CatalogItemDiscontinued` | retry, then dead letter; a conformist consumer |
| `pricing` (Core) | it publishes, cart consumes | Kafka `retailnetwork.pricing.events` | `ItemPriceChanged`, `ItemPriceWithdrawn`, `DeliveryChargeScheduleChanged`, `TaxTableChanged` | retry, then dead letter; version-guarded, stale events ignored |
| `checkout` (Supporting) | it publishes, cart consumes | Kafka `retailnetwork.checkout.events` | `CheckoutCompleted` | retry, then dead letter; an unknown or closed cart is acknowledged and logged |
| `checkout` | it calls the cart | REST `GET /carts/{cartId}` | the cart view with `checkoutReadiness` | `404` for an unknown cart |
| `storefront-bff` (Experience) | it calls the cart | REST under `/api/shopping-cart` via Kong | every route of `apis/openapi.yaml` | RFC 9457 problems; `409`/`503` are retryable with the same `Idempotency-Key` |
| agents | they call the cart | A2A JSON-RPC 2.0 on `cmd/a2a` `/` and `/a2a`, card at `/.well-known/agent-card.json` | skills `get_cart`, `list_shopper_carts` | in-band problem answers |
| analytics consumers | cart publishes | Kafka `retailnetwork.shopping-cart.analytics` | `CartLineAdded`, `CartDeliveryQuoted`, `CartAbandoned`, `CartCheckedOut` | at-least-once through the outbox; consumers dedupe on the CloudEvents `id` |

REST and A2A are **unauthenticated** fleet-wide; the Agent Card declares no
security scheme. Every Kafka message is CloudEvents 1.0 in structured mode
(`application/cloudevents+json`), dispatched on the full `type` string
(`internal/adapters/kafka/cloudevents`).

## Outbound: delivery-promise

- **When:** only in `QuoteDelivery`. Every other use case works with nothing
  listening there.
- **Call:** `POST {DELIVERY_PROMISE_URL}/delivery-offers:quote` with
  `{"destinationPostalPrefix": "...", "lines": [{"catalogItemId", "quantity"}]}`,
  `Content-Type: application/json`, and the `Idempotency-Key` of the cart request
  forwarded, so a repeat inside the offers' TTL returns the same offers. The
  deployment value is `http://delivery-promise.warehouse-systems.svc.cluster.local:80`
  in the kind cluster.
- **Timeout:** `DELIVERY_PROMISE_TIMEOUT`, default `5s`; the answer is read up to
  1 MiB.
- **Mapping:** only offers whose `state` is `QUOTED` (or empty) are kept; groups
  are named by the offers' `groupNo`; each offer's `window.earliest` and
  `window.latest` become the calendar dates `deliverByEarliest` and
  `deliverByLatest` in the **destination's local calendar** using the offer's
  `window.timeZone` (IANA zone of the serving delivery station), falling back to
  UTC with a WARN when the zone is absent, `Local` or unknown
  ([ADR 0007](/docs/adr/0007-delivery-dates-in-the-destinations-local-calendar));
  `expiresAt` is stored in UTC; `unavailable` tiers keep their constraint and
  group; a group's line numbers are the cart lines whose item the group's offers
  carry.
- **Failure:** `ports.ErrDeliveryPromiseUnavailable` (transport, timeout, 5xx,
  undecodable) and `ports.ErrQuoteRejected` (4xx) both answer `503`
  `delivery-promise-unavailable`. The call runs outside any transaction, so the
  cart is unchanged by a failure and `/readyz` does not depend on delivery-promise.
- **Contract guard:** `internal/adapters/outbound/deliverypromise/spec_test.go`
  checks the restated shapes against a vendored copy of delivery-promise's spec
  (`testdata/delivery-promise.openapi.yaml`).

## Inbound: Kafka

Three consumers always run in `cmd/api`, one per topic, each with its own stable
group id from `OFFER_AVAILABILITY_CONSUMER_GROUP`, `PRICING_CONSUMER_GROUP` and
`CHECKOUT_CONSUMER_GROUP`. The cart is a **Conformist** to offer-availability
and **Customer** of pricing and checkout.

| Topic | Types handled (full `type` in [Events](/docs/api-reference/events)) | Effect |
| --- | --- | --- |
| `retailnetwork.offer-availability.events` | `CatalogItemCreated`, `CatalogItemDiscontinued` | `rm_items` catalogued / discontinued |
| `retailnetwork.pricing.events` | `ItemPriceChanged`, `ItemPriceWithdrawn`, `DeliveryChargeScheduleChanged`, `TaxTableChanged` | item price, current schedule, current tax table |
| `retailnetwork.checkout.events` | `CheckoutCompleted` | the cart named by `cartId` becomes `CHECKED_OUT` |

Failure behaviour, identical for the three: one message at a time, offset
committed only after success; transient errors retry five times with a 200 ms to
5 s backoff and then the message goes to `<topic>.dlq`; a payload that can never
be applied is dead-lettered at once; a message that is not a CloudEvent, or whose
type is not handled, is skipped. A lost broker connection is retried up to ten
times and then the process exits so the orchestrator restarts it. The operational
detail is on [Runbook](/docs/operations/runbook#kafka).

The cart only reads the fields it needs (`catalogItemId`; `currency`,
`effectivePriceMinor`, `version`; `version` and tiers or bands; `cartId`,
`sessionId`) and ignores unknown fields, so a producer may add fields without
breaking it. The producers' own AsyncAPI files are authoritative; this service
restates the shapes in `apis/asyncapi.yaml`.

## Outbound: Kafka

One topic, `retailnetwork.shopping-cart.analytics`, key = the cart id. The four
events are written to `outbox_events` in the same transaction as the cart change
and published by the always-on relay (at-least-once, acks from all replicas, the
CloudEvents `id` minted once and persisted, so a retry republishes the same id).
No sibling consumes a cart event: all four are analytics-only, so a stalled relay
never blocks a shopper.

## Inbound: REST

Callers reach the service through Kong at `/api/shopping-cart` (the prefix is
stripped) or directly on `:8080`. Writes require an `Idempotency-Key`; the first
answer below 500 is stored 24 hours and replayed. Checkout reads
`GET /carts/{cartId}` when a session opens and relies on `checkoutReadiness`
(every group selected, no offer expired); the shopper's calls come through
`storefront-bff`. The route list is on [API reference](/docs/api-reference).

`POST /carts/{cartId}/delivery/requoted` is the route checkout is meant to use
when its own requote replaces the offers. No caller of it was found in the local
checkouts of `checkout` and `storefront-bff`, so today it is served and tested but
unused by a sibling.

## Inbound: A2A

`cmd/a2a` serves two read-only skills over the same use cases as the REST GETs.
It calls no other context and contains no outbound A2A client; facts between
contexts still travel only as CloudEvents on Kafka. In the kind cluster it is
reached at `/api/a2a/shopping-cart` (the gateway rewrites the prefix to `/`).

## Absent edges

Stated so nobody looks for them:

| Not present | Why |
| --- | --- |
| Calls to `pricing` | the cart follows pricing's events and never reads prices synchronously |
| Calls to `customer-ordering`, `customer-payments`, `product-content`, `product-discovery` | reaching the order record is checkout's job; the cart consumes no content events and keeps its own purchasability view |
| Any `warehouse.*` topic | the cart is a retail-network storefront context, not part of the warehouse |
| A consumer of the analytics topic | no context consumes cart events yet |
| MCP | there is no `cmd/mcp` |
| Authentication | removed fleet-wide; none exists here |
| A cart-owned webhook, push or websocket | none; callers poll the REST reads |
