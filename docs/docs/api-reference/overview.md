---
id: overview
title: API overview
sidebar_label: Overview
slug: /api-reference
sidebar_position: 1
description: "Shopping Cart interfaces: REST, Kafka events and the A2A agent. Endpoint table, status codes, the Idempotency-Key and RFC 9457 problem conventions, and the A2A skills."
---

# API overview

:::info Implemented by cmd/api and cmd/a2a
`cmd/api` serves every REST route below (`internal/adapters/inbound/http`) on
`HTTP_ADDR` (default `:8080`); `cmd/a2a` serves the agent on `A2A_ADDR` (default
`:8093`). The contracts are `apis/openapi.yaml` and `apis/asyncapi.yaml`. The
router registers exactly the eleven API routes plus `/healthz` and `/readyz`; the
only HTTP route that is not in the OpenAPI file is the A2A endpoint `/a2a`,
described below. See the [Quickstart](/docs/overview/quickstart) for the shapes of
the answers.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` (OpenAPI 3.0.3, version 0.1.0) | The pages under *REST* in the sidebar are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. The spec names `http://localhost:8080`; in the kind cluster the service is behind Kong at `/api/shopping-cart` (prefix stripped). |
| Events | `apis/asyncapi.yaml` (AsyncAPI 3.0.0, version 1.0.0) | Four published events and seven consumed ones, on the [event catalogue](/docs/api-reference/events); every field on [Domain events](/docs/ddd/domain-events). |
| MCP | none | There is no `cmd/mcp`. |
| A2A | [ADR 0006](/docs/adr/0006-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a`, JSON-RPC 2.0 on `/` and `/a2a` (port 8093; through Kong `/api/a2a/shopping-cart`), Agent Card at `/.well-known/agent-card.json`. Two read-only skills, no write skill. No auth, by fleet decision. |

## Endpoints

All carry the cart view on success (see [cart view](#the-cart-view)). Every `POST`
requires an `Idempotency-Key` header.

| Tag | Endpoint | Operation id | Success | Errors |
| --- | --- | --- | --- | --- |
| Carts | `POST /carts` | `createCart` | `201`, an empty `OPEN` cart; body optional `{"shopperId"}` | `422` |
| Carts | `GET /carts?shopperId=&state=` | `listCarts` | `200 {"carts": [...]}`, newest first | `422` (missing or invalid `shopperId`, unknown `state`) |
| Carts | `GET /carts/{cartId}` | `getCart` | `200`, the cart with quote, selections, estimate, readiness | `404` |
| Lines | `POST /carts/{cartId}/lines` | `addCartLine` | `200`; `{"catalogItemId","quantity"}`; merges into an existing line | `404`, `409`, `422` |
| Lines | `POST /carts/{cartId}/lines/{catalogItemId}:set-quantity` | `setCartLineQuantity` | `200`; `{"quantity"}` | `404`, `409`, `422` |
| Lines | `POST /carts/{cartId}/lines/{catalogItemId}:remove` | `removeCartLine` | `200` | `404`, `409` |
| Delivery | `POST /carts/{cartId}:set-destination` | `setCartDestination` | `200`; `{"destinationPostalPrefix"}` | `404`, `409`, `422` |
| Delivery | `POST /carts/{cartId}:quote-delivery` | `quoteCartDelivery` | `200`; no body; calls delivery-promise | `404`, `409`, `422`, `503` |
| Delivery | `POST /carts/{cartId}:select-delivery` | `selectCartDelivery` | `200`; `{"groupNo","offerId"}` | `404`, `409`, `422` |
| Carts | `POST /carts/{cartId}:merge-into` | `mergeCartInto` | `200`, the **target** cart; `{"targetCartId"}` | `404`, `409`, `422` |
| Delivery | `POST /carts/{cartId}/delivery/requoted` | `acceptRequotedOffers` | `200`; `{"offers": [...]}` replaces quote and selections without calling delivery-promise | `404`, `409`, `422` |
| Health | `GET /healthz`, `GET /readyz` | none (not in the spec) | `200 {"status":"ok"}` / `200 {"status":"ready"}`; `/readyz` answers `503` once shutdown starts or Postgres or Kafka is unreachable | |

The colon routes (`:set-quantity`, `:quote-delivery`, ...) are literal paths of
the chi router. `/healthz` and `/readyz` are registered by the router but are not
described in `apis/openapi.yaml`.

## The cart view

Every cart answer is the same document (`cartBody`,
`internal/adapters/inbound/http/dto.go`):

| Field | Meaning |
| --- | --- |
| `cartId`, `shopperId` (null for a guest), `state` (`OPEN`, `CHECKED_OUT`, `ABANDONED`, `MERGED`) | identity and lifecycle |
| `lines` | `[{catalogItemId, quantity}]`, one per item, in cart order |
| `destinationPostalPrefix` | null until set |
| `deliveryQuote` | absent until quoted: `groups[{groupNo, lineNos, offers[{offerId, groupNo, tier, deliverByEarliest, deliverByLatest, expiresAt}]}]`, `unavailable[{tier, constraint, groupNo?}]`, `unavailableTiers`, `quotedAt` |
| `selections` | `[{groupNo, offerId}]` |
| `estimate` | `currency`, `itemsMinor`, `deliveryMinor`, `taxMinor`, `totalMinor`, `estimateComplete`. Labelled and non-binding |
| `checkoutReadiness` | `ready`, `allGroupsSelected`, `noOfferExpired` |
| `version` | the optimistic-concurrency token; +1 per change |

## Conventions in the contract

- **Idempotency.** `Idempotency-Key` (1 to 128 characters) is required on all nine
  `POST` routes: absent or longer is `400` `idempotency-key-required`. The first
  answer below 500 for a `(route, key)` is stored for 24 hours and replayed with
  the header `Idempotent-Replayed: true`; the same key with a different method,
  path, query or body is `422` `idempotency-key-reused`. A 5xx is never stored.
  `:quote-delivery` forwards its key to delivery-promise.
- **Errors** are RFC 9457 `application/problem+json` with `type`
  `/problems/<slug>`. The slugs: `malformed-request`, `idempotency-key-required`,
  `not-found`, `resource-not-found`, `cart-closed`, `stale-delivery-selection`,
  `illegal-transition`, `concurrent-update`, `item-not-purchasable`,
  `quantity-out-of-range`, `too-many-lines`, `no-destination`, `merge-target`,
  `validation-failed`, `idempotency-key-reused`, `delivery-promise-unavailable`,
  `dependency-unavailable` and `internal-error`. Status and cause of each are on
  [Troubleshooting](/docs/operations/troubleshooting#rest-problem-types); the
  detail of a 500 is fixed text, the cause is only in the log.
- **Ids.** Opaque, 1 to 64 characters from `A-Z a-z 0-9 . _ : -`, starting with a
  letter or digit.
- **Bounds.** Quantity 1 to 99 per line; at most 100 lines; destination prefix 1 to
  10 letters or digits; request bodies are size-limited and unknown JSON fields
  are rejected (`malformed-request`).
- **Money** is integer minor units; `estimateComplete: false` means the figures
  cover only what is known (no destination, an unpriced line, an unselected
  group, or no tax band for the prefix).
- **Concurrency.** A write that loses the version race is retried once, then
  `409` `concurrent-update`.
- **Auth:** none. REST, A2A and (absent) MCP are unauthenticated fleet-wide;
  `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns.
- **Local calendar.** `deliverByEarliest` and `deliverByLatest` are calendar dates
  in the destination's local time zone ([ADR 0007](/docs/adr/0007-delivery-dates-in-the-destinations-local-calendar)).

## A2A

`cmd/a2a` is a message-only Agent2Agent agent (no streaming, no task lifecycle).
The Agent Card is served at `GET /.well-known/agent-card.json`; it advertises the
URL in `A2A_AGENT_URL` as a JSON-RPC interface and declares no security scheme.
The JSON-RPC 2.0 endpoint is mounted at both `/` and `/a2a`; `GET /healthz` answers
liveness.

A skill call is an A2A `SendMessage` request (JSON-RPC method `SendMessage`) whose
first part is a data part (or a text part holding the same JSON) of the form
`{"skill": "<id>", "arguments": {...}}`.
The parsing is strict: unknown fields are rejected.

| Skill | Arguments | Same as | Answer |
| --- | --- | --- | --- |
| `get_cart` | `cartId` (required) | `GET /carts/{cartId}` | `data.cart`, the cart view, for a cart of any state |
| `list_shopper_carts` | `shopperId` (required), `state` (optional: `OPEN`, `CHECKED_OUT`, `ABANDONED`, `MERGED`) | `GET /carts?shopperId=&state=` | `data.carts`, newest first; an empty list for a shopper with none |

```bash
curl -s localhost:8093/a2a -H 'Content-Type: application/json' -d '{
  "jsonrpc":"2.0","id":1,"method":"SendMessage",
  "params":{"message":{"role":"ROLE_USER","messageId":"m-1",
    "parts":[{"data":{"skill":"get_cart","arguments":{"cartId":"cart-123"}}}]}}}'
```

The request shape above is the one `cmd/a2a/router_test.go` sends; the reply is one agent message with a data part carrying the envelope
`{skill, data | problem}` and a text part with a one-line summary. Domain
rejections are answered **in the envelope** as a problem with the REST slug and
status (`malformed-request` and `unknown-skill` as 400, `validation-failed` as 422,
`not-found` and `resource-not-found` as 404, `internal-error` as 500), never as a
JSON-RPC error. The agent is
read-only, calls no other context and has no outbound A2A client
(`TestA2ASurfaceIsReadOnly`).
