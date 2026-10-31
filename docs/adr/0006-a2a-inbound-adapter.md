# ADR 0006: A2A inbound adapter (read-only agent)

## Status

Accepted (2026-10-30).

## Context

The warehouse-systems fleet moves information over three transports with no overlap:
Kafka + CloudEvents carries FACTS (ADR 0004), MCP carries an agent's TOOLS, and A2A
(Agent2Agent) carries the DELEGATION of a goal between agents, never data sync. The
other contexts already run an A2A agent beside their REST API; the delegating agents
of the fleet (for example the warehouse ops agent) discover a context through its
Agent Card at `/.well-known/agent-card.json` and ask it a goal-shaped question.

shopping-cart has no `cmd/mcp` and no MCP adapter, so there is no MCP surface to
derive skills from, and no MCP write to inherit. The only agent-facing surface this
context has is its REST API (`apis/openapi.yaml`). Two things follow:

- The agent is READ-ONLY. A write skill is allowed elsewhere only when the context's
  MCP surface already offers that write; this context offers none, and a delegation
  channel must never widen what the context exposes.
- A cart is customer data: a `shopperId` reference (ADR 0001: the context stores no
  PII and no card data, but the id ties a cart to a shopper), quantities and a
  delivery postal prefix. An agent surface is a new way to read it, so the question
  is not only which reads to offer but what each one may return.

## Decision

1. **A third driving adapter, `internal/adapters/inbound/a2a`**, built on the
   official A2A Go SDK (`github.com/a2aproject/a2a-go/v2`, pinned v2.7.0). It speaks
   JSON-RPC 2.0 over HTTP and is a message-only agent: both skills are transactional
   request/response reads, so there is no Task lifecycle. It depends only on the
   application and domain layers; nothing depends on it (`TestA2AAdapterDependencyRule`).

2. **Exactly two goal-shaped READ skills**, each a use case a REST GET already calls,
   with no new query and no new behaviour:

   | Skill | Use case | REST equivalent |
   |---|---|---|
   | `get_cart` `{cartId}` | `GetCart` | `GET /carts/{cartId}` |
   | `list_shopper_carts` `{shopperId, state?}` | `ListCarts` | `GET /carts?shopperId=&state=` |

   There is no skill per endpoint, no write skill, and no skill that lists carts
   without a shopper (REST offers no such read: `GET /carts` requires `shopperId`,
   ADR 0002 A15).

3. **The skills expose exactly what REST exposes, and nothing else.** The answer
   carries the `Cart` wire shape of `apis/openapi.yaml` field for field: the same
   document, the same `null` for a guest cart's `shopperId`, and `views.Cart.LastChangedAt`,
   which REST withholds, is not mapped. Two tests pin it: every answered object has
   exactly the properties its OpenAPI schema declares, and the A2A answer equals the
   REST router's answer for the same carts (guest, shopper, quoted, empty) after JSON
   decoding. A skill never takes or returns anything REST withholds, so an agent
   learns nothing about a cart or a shopper that `GET /carts` would not tell a REST
   caller. The text summary names no shopper.

4. **Domain rejections and bad requests are ANSWERED in-band** as RFC 7807-style
   `problem`s in the message envelope (`{"skill", "data" | "problem"}`), never as
   JSON-RPC errors, with the status classes the REST adapter gives the same two GETs
   and its `/problems/<slug>` type URIs: `404 not-found` (unknown cart, over-long
   `cartId`), `404 resource-not-found` (a NUL in the `cartId`), `422 validation-failed`
   (`shopperId` missing or not a valid id, `state` not one of OPEN, CHECKED_OUT,
   ABANDONED, MERGED: the list handler's own checks), `400 malformed-request`
   (unparsable or unknown argument, a NUL in a filter) and `500 internal-error`. `409`
   and `503` belong to writes and to the delivery-promise call this agent never makes,
   so they are not mapped. A 500 never echoes the underlying error (it is logged with
   the skill name); an id too long to name a cart is not echoed back. `unknown-skill`
   (400) is adapter-owned.

5. **Strict input.** The request is `{"skill": "<id>", "arguments": {...}}` as a data
   part, or a text part holding the same JSON. It is decoded with unknown fields
   refused and trailing data rejected, so a misspelled argument (`stat`) cannot silently
   drop the state filter.

6. **Observability.** One OTel span per KNOWN skill (`a2a.skill <id>`, attribute
   `a2a.skill`); an unknown skill or a malformed request gets no span. The binary uses
   the fleet telemetry setup of ADR 0005 under the service name `shopping-cart-a2a`.

7. **A separate binary, `cmd/a2a`**, beside `cmd/api`, clones only what the two reads
   need: Postgres (the cart repository and the three read-model repositories behind
   the estimate) and the system clock. It has no HTTP routes of the REST API, no
   outbox, no relay, no Kafka consumer or producer, no schedulers and no delivery-promise
   client. It listens on `A2A_ADDR` (default `:8093`), serves the card at
   `/.well-known/agent-card.json`, JSON-RPC at both `/` and `/a2a`, and `GET /healthz`.
   Mandatory configuration is refused at boot, before telemetry and before the listener
   binds, with an error naming the variable: `DATABASE_URL` and `A2A_AGENT_URL` (an
   absolute http(s) URL, the one the card advertises). Optional: `MIGRATIONS_DATABASE_URL`,
   `CART_CURRENCY`, `LOG_LEVEL`, `OTEL_*`, `SERVICE_VERSION`, `ENVIRONMENT`.

8. **No authentication, on purpose.** The A2A endpoints are unauthenticated like every
   REST and MCP endpoint of the fleet (static-bearer auth was reverted 2026-09-11 until
   a new auth-model decision is recorded); the Agent Card declares no security schemes.
   `TestNoAuthMiddlewareReintroduced` already covers `internal/adapters/inbound`. The
   exposure guard of point 3 is therefore the only barrier: it is why the surface is not
   allowed to be wider than REST.

9. **No outbound A2A client.** Delegation here is inbound only. The adapter and
   `cmd/a2a` import no `a2aclient`, no other bounded context and no event adapter.

10. **Fitness.** `internal/architecture/a2a_fitness_test.go` encodes the dependency
    rule and a read-only ALLOWLIST: the adapter and `cmd/a2a` may reference only
    `usecases.GetCart`, `usecases.ListCarts`, `usecases.Deps` and `ports.ErrNotFound`,
    and import none of the outbound kafka, outbox, delivery-promise or memory adapters,
    the REST, Kafka or scheduler inbound adapters, or the A2A client. A new write use
    case is refused by default rather than missed by a denylist; a fixture test proves
    the sensor flags each violation shape.

11. **Packaging.** The Helm chart gains an optional `a2a` component (default OFF):
    `a2a-deployment.yaml` (command `/app/a2a`, port 8093, `A2A_AGENT_URL` defaulting to
    `http://<release>-a2a.<ns>.svc.cluster.local:8093/` and overridable with
    `a2a.agentUrl`), `a2a-service.yaml` and an optional `a2a-route.yaml` that exposes
    `/api/a2a/shopping-cart` rewritten to `/`. The Deployment does not mount the api
    ConfigMap and renders no Kafka, outbox, consumer or delivery-promise variable
    (`tests/render_test.sh` asserts their absence, and that every variable `cmd/a2a`
    reads is rendered). The image already ships every `cmd/*` binary.

## Consequences

- An agent can ask what a cart holds and which carts a shopper has, with the same data and
  the same problem classes as REST, and cannot change anything.
- `cmd/a2a` applies the embedded migrations at boot like `cmd/api`. They are idempotent and
  guarded by Postgres's advisory lock, so the order in which the two binaries start does not
  matter; the cost is that the agent's database role needs the migration privileges the API's
  has. The `MIGRATIONS_DATABASE_URL` split of the API (a direct, non-pooled DSN) applies.
- The estimate of a cart without a priced item falls back to `CART_CURRENCY`; the agent and the
  API must be given the same value, so the chart renders `config.cartCurrency` into both.
- The agent reads the same local read models (items, charge schedule, tax table) the API does;
  they are filled by the API's Kafka consumers, so the agent's estimates are as fresh as the
  API's, and no fresher.
- One more Deployment per release when `a2a.enabled` is set; warehouse-infra turns it on and
  supplies the gateway `parentRefs`.
- Adding a write skill later needs its own ADR: it would have to go through the same use case
  and transactional outbox as REST, the allowlist above would be widened deliberately, and the
  unauthenticated surface would then be a decision to revisit first.
