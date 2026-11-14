# ADR 0009: A read-only MCP server (authority L1, complete surface)

## Status

Accepted (2026-11-14). Adds the MCP surface the fleet's Agentic Standard v2 (AS-9)
asks of a storefront context that has an A2A agent ([ADR 0006](./0006-a2a-inbound-adapter.md))
and no MCP server. Amends the sentence of ADR 0006 "shopping-cart has no `cmd/mcp`
and no MCP adapter": it now has both. The tool set of that ADR (two read skills) is
unchanged.

## Context

Every other bounded context of the fleet that exposes an HTTP API also ships an MCP
server so that agents (warehouse-ops-agent first) can read its data without a
bespoke REST client (the fleet's ADR 0008 in warehouse-docs: one MCP server per bounded context, an additive
inbound adapter, the official MCP Go SDK, Streamable HTTP only, a separate `cmd/mcp`
binary). shopping-cart had an A2A agent and a REST API but no MCP server.

The Agentic Standard v2 also asks every MCP server for an authority level on every
tool (AS-1), a surface beyond tools, at least one resource and one prompt (AS-3), and
a stateless Streamable HTTP handler (AS-4). A cart is customer data (a `shopperId`
reference, quantities, a delivery postal prefix, ADR 0001/0006), so the surface may
not be wider than the REST GETs.

## Decision

- **A driving adapter and a separate binary.** `internal/adapters/inbound/mcp`
  is built on the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`
  v1.8.0) and served over Streamable HTTP only (no stdio, no SSE) by `cmd/mcp`,
  mounted at `/mcp` and `/` with an open `GET /healthz`. It is a third deployable
  beside `cmd/api` and `cmd/a2a`, shipped in the SAME image as `/app/mcp`. The
  Helm chart gains an `mcp` component (default OFF) with an optional gateway
  HTTPRoute at `/api/mcp/shopping-cart`.
- **Two read tools, one per read GET of `apis/openapi.yaml`.** Each tool is a thin
  wrapper over a use case that already backs a REST GET and an A2A skill, with the
  same wire shape as REST (`views.Cart.LastChangedAt`, which REST withholds, is not
  mapped). All are authority L1 (tool metadata `com.warehouse/authority`) and
  annotated read-only, idempotent and closed-world:

  | Tool | Input | Authority | Use case | REST counterpart | A2A skill |
  |---|---|---|---|---|---|
  | `get_cart` | `cartId` | L1 | `GetCart` | `GET /carts/{cartId}` | `get_cart` |
  | `list_shopper_carts` | `shopperId`, `state?`, `limit?` | L1 | `ListCarts` (first page) | `GET /carts?shopperId=&state=&limit=` | `list_shopper_carts` |

  `list_shopper_carts` answers ONE page, newest first (`limit` 1 to 100, default 20),
  with `hasMore` when older carts exist. It takes no cursor: the MCP surface is
  narrower than REST, never wider. A guest cart is reachable through `get_cart` only.
- **One resource, one prompt (AS-3).**
  - `warehouse://shopping-cart/carts/{cartId}` (resource template, `application/json`):
    the cart document of ONE cart, the same `GetCart` use case as the tool, a scoped
    read model and never a table dump.
  - `review_cart` (prompt, one required argument `cartId`): this context's operator
    procedure: read the cart, interpret the estimate (a labelled, non-binding
    estimate) and the checkout readiness, and name what nothing here can do. It
    names tools and never calls them.
- **Stateless transport (AS-4).** The handler is built with
  `&mcp.StreamableHTTPOptions{Stateless: true}`: no `Mcp-Session-Id` is issued, the
  server answers the 2026-07-28 per-request handshake (`server/discover`) and a
  legacy `initialize` still answers 200 (`stateless_test.go`).
- **Read-only is structural.** The adapter's `Deps` holds `usecases.GetCart` and
  `usecases.ListCarts` and nothing else; `cmd/mcp` wires only the cart repository and
  the three read-model repositories the estimate needs, plus the clock. No unit of
  work, outbox, inbox, id generator, delivery-promise quoter or Kafka client exists in
  the binary, so a read can neither write nor call another context. A cart MUTATION
  (create, add, set or remove a line, destination, quoting, selecting a delivery,
  merge, accepting requoted offers) is NOT exposed and stays REST. Quoting is not a
  tool: `POST /carts/{cartId}/delivery-quotes` calls delivery-promise and is a write.
  `TestToolSurface` pins the exact set (budget 2), the read-only annotations and the
  absence of write verbs in tool names; `TestWriteSeamSensorFailsOnWriteSeams` fails
  the build when the adapter names a write use case or a transactional seam;
  `TestToolRegistryGolden` pins the whole advertised registry, `_meta` included.
- **Authority declarations.** `authority.go` carries the fleet's pinned const
  ladder; every `mcp.Tool` literal sets `Meta: authorityMeta(authorityL1)`. Both A2A
  skills of ADR 0006 gain the tag `authority:L1` (the only change to the Agent Card;
  the fleet agent catalogue is regenerated from it). The unused `authorityL2..L4`
  constants of the fleet's pinned shape are not declared here: nothing in this
  context recommends or writes over MCP, and the sensor
  (`internal/architecture/mcp_authority_fitness_test.go`) reads only the key, the
  helper and the level a tool uses.
- **Errors.** Typed errors carry the REST problem slug as the prefix of the tool
  error: `not-found` (unknown cart, an over-long or NUL-carrying `cartId`),
  `validation-failed` (empty `cartId`, `shopperId` missing or invalid, `state` not
  one of OPEN, CHECKED_OUT, ABANDONED, MERGED, `limit` out of range). Anything else
  is logged and answered as `internal-error` with no detail.
- **No auth, no sibling calls.** Unauthenticated like every fleet endpoint (reverted
  2026-09-11); access control is the in-cluster ClusterIP boundary.
  `TestNoAuthMiddlewareReintroduced` covers the adapter. The server reads this
  context's own tables only.
- **Observability.** `cmd/mcp` brings up the same fleet telemetry as `cmd/api`
  (ADR 0005) under the service name `shopping-cart-mcp`, with the same middleware
  order: RequestID, otelchi, the request-duration histogram, RequestLogger,
  Recoverer. No new metric is added.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode); the binary runs the
  idempotent embedded migrations on start (`MIGRATIONS_DATABASE_URL` for a direct
  DSN), wrapped in the `bootretry` budget `cmd/api` uses. `MCP_ADDR` (default
  `:8090`), `CART_CURRENCY`, `LOG_LEVEL`, `SERVICE_VERSION` and `ENVIRONMENT`
  complete the environment.
- **Fitness.** `TestMCPAdapterDependencyRule` (nothing but `cmd/mcp` imports the
  adapter, and it imports no `inbound/http`), `TestMCPOptionalToolInputsCarryOmitempty`
  and the authority and surface sensors of `mcp_authority_fitness_test.go`.

## Consequences

- An agent can answer "what does this cart hold, what would it cost, is it ready for
  checkout" and "which carts does this shopper have" without REST glue, with the same
  data as REST, and cannot change anything.
- Adding a tool is a reviewed act: the tool budget, the curated list and the golden
  file change together. A write tool would be a new ADR (L3, `initiated_by`
  attribution) and would reopen the question of an unauthenticated surface.
- One more Deployment per release when `mcp.enabled` is set; warehouse-infra turns
  it on and supplies the gateway `parentRefs`.
- Left out on purpose (no existing read use case backs them): a tool that lists
  carts without a shopper (REST offers none, ADR 0002 A15), a cursor on
  `list_shopper_carts`, and a quote tool (a write to delivery-promise).
