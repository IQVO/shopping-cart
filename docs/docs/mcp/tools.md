---
id: tools
title: MCP tools
sidebar_label: MCP tools
sidebar_position: 2
description: "The two read-only MCP tools of shopping-cart, every one declaring authority L1, plus the cart resource and the review_cart prompt, from the pinned tool registry."
---

# MCP tools

`cmd/mcp` serves a read-only [Model Context Protocol](https://modelcontextprotocol.io) server over **Streamable HTTP
only** ([ADR 0009](/docs/adr/0009-read-only-mcp-server)). It is unauthenticated like the rest of the fleet: the
in-cluster ClusterIP boundary is the access control.

| | |
| --- | --- |
| Listen address | `MCP_ADDR`, default `:8090` |
| Endpoint | `/mcp` (also mounted at `/`) |
| Probe | `GET /healthz` returns `200 {"status":"ok"}` |
| Needs | `DATABASE_URL` only; it dials no Kafka, no delivery-promise and registers no write seam |
| Gateway | `/api/mcp/shopping-cart`, rewritten to `/mcp` (`mcp.route.path` in the chart, behind `mcp.route.enabled`) |
| Deployed by | the chart behind `mcp.enabled` (default off); the binary ships in the same image as `/app/mcp` |

Source: `cmd/mcp/main.go`, `cmd/mcp/router.go`, `internal/adapters/inbound/mcp/{server,tools,tools_views,errors,authority,resources,prompts}.go`.

**How this page was produced.** The list below comes from the pinned tool registry
`internal/adapters/inbound/mcp/testdata/tool_registry.golden.json`, which `TestToolRegistryGolden` compares with the
server's `tools/list` on every test run; it was not captured from a running server. Run `tools/list` against a live
`cmd/mcp` to see the same two tools.

## Annotations and authority

Both tools carry `readOnlyHint: true`, `idempotentHint: true` and `openWorldHint: false`. There is no write tool: the
governance tests in `internal/adapters/inbound/mcp/governance_test.go` (`TestToolSurface` with its write-verb and
write-seam sensors) fail on a write-named tool or a write seam, and `TestMCPAdapterDependencyRule` limits the adapter
to the application and domain layers. A tool call adds no `outbox_events` row
(`cmd/mcp/main_integration_test.go`).

Every tool also declares its **agent authority level** `L1` ([ADR 0009](/docs/adr/0009-read-only-mcp-server)): a pure
read of this context's own state. The declaration travels in `tools/list` as `_meta` under the key
`com.warehouse/authority`; the same level is pinned per tool in the golden registry and by the static sensor
`internal/architecture/mcp_authority_fitness_test.go`. Both A2A skills carry the equivalent tag `authority:L1`.

| Tool | Required input | Optional input | Annotation | Authority | Use case | REST counterpart |
| --- | --- | --- | --- | --- | --- | --- |
| `get_cart` | `cartId` | none | read-only | `L1` | `GetCart` | `GET /carts/{cartId}` |
| `list_shopper_carts` | `shopperId` | `state`, `limit` | read-only | `L1` | `ListCarts` | `GET /carts?shopperId=&state=&limit=` |

Every input schema is `additionalProperties: false`. A failure is an `isError` tool result whose text is
`<slug>: <detail>` with the REST slug (`not-found`, `validation-failed`); an untyped error is logged
(`mcp tool failed with an unexpected error`) and answered as `internal-error: an unexpected internal error occurred`,
with no DSN or SQL in the text.

## `get_cart`

Reads one cart, of any state.

- **Input:** `cartId` (string, required, for example `cart-123`).
- **Output:** the cart document of `GET /carts/{cartId}`, field for field: `cartId`, `shopperId` (string or null for
  a guest cart), `state` (`OPEN`, `CHECKED_OUT`, `ABANDONED`, `MERGED`), `lines` (each `catalogItemId` and `quantity`),
  `destinationPostalPrefix` (string or null), `deliveryQuote` (absent until quoted: `groups` with their `offers`,
  `unavailableTiers`, `unavailable`, `quotedAt`), `selections` (each `groupNo` and `offerId`), `estimate`
  (`currency`, `itemsMinor`, `deliveryMinor`, `taxMinor`, `totalMinor`, `estimateComplete`; a non-binding estimate in
  minor units), `checkoutReadiness` (`ready`, `allGroupsSelected`, `noOfferExpired`) and `version`.
  `views.Cart.LastChangedAt`, which REST withholds, is not exposed.
- **Errors:** an unknown `cartId`, one longer than 128 characters or one carrying a NUL is `not-found`; a blank
  `cartId` is `validation-failed`.

## `list_shopper_carts`

Lists the carts of one shopper, newest first.

- **Input:** `shopperId` (required, for example `shopper-42`); `state` (optional: `OPEN`, `CHECKED_OUT`, `ABANDONED`
  or `MERGED`; omitted means every state); `limit` (optional integer, 1 to 100; omitted means 20).
- **Output:** `carts` (each the same document `get_cart` returns; an empty array when the shopper has none) and
  `hasMore` (true when an older cart exists). It answers ONE page: there is no cursor, so the surface is narrower than
  REST, never wider; narrow with `state` to reach older carts, or page `GET /carts` over REST.
- **Errors:** a missing or invalid `shopperId`, an unknown `state`, a NUL in an argument or a `limit` outside 1 to 100 is
  `validation-failed`.

## Not exposed

Every cart mutation stays REST-only and is not an MCP tool: `POST /carts`, adding, setting or removing a line,
setting the destination, `POST /carts/{cartId}/delivery-quotes` (it calls delivery-promise), choosing a delivery
selection, merging carts and accepting requoted offers. Nothing lists carts without a shopper (REST offers no such
read).

## Transport

The Streamable HTTP handler is **stateless** ([ADR 0009](/docs/adr/0009-read-only-mcp-server)): no `Mcp-Session-Id`
is issued, the server answers the 2026-07-28 revision's per-request handshake (`server/discover`, no `initialize`
needed) and a legacy `initialize` still answers `200`. Nothing in the server relies on cross-call session state;
pinned by the raw-POST tests in `internal/adapters/inbound/mcp/stateless_test.go`.

## Resource: cart

`warehouse://shopping-cart/carts/{cartId}` (`application/json`) is the one resource of this server: the cart document
of ONE cart, built from the same `GetCart` read use case as the `get_cart` tool and the REST
`GET /carts/{cartId}`. Bounded by construction: one cart, the document's fixed fields, JSON text, never a dump of a
table. A malformed URI or an unknown cart is an error.

## Prompt: `review_cart`

The one prompt of this server is the operator procedure for this context: read the cart (or find it with
`list_shopper_carts`), interpret the non-binding estimate (`estimateComplete`) and `checkoutReadiness`, and escalate a
cart that is not ready to a human, because nothing in this server can change a cart. It takes one typed, required
argument (`cartId`); it names tools and never calls them. `prompts/get` returns the text with the cart id
interpolated; a missing argument degrades to a `<cartId>` placeholder, never to a hidden call.
