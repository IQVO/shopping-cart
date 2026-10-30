# shopping-cart

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Shopping Cart** (Supporting, storefront tier).

Holds a shopper's intent: cart lines, the destination postal prefix, the delivery quote delivery-promise returned for the cart (per shipment group, with its offers and expiry), the shopper's tier selection per group, and a labelled, non-binding estimate (effective prices + selected tier charges + tax band) so the total cost is visible up front [1].

Status: built to the model (domain, application, Postgres, Kafka, REST, BDD); the PR is awaiting review. The design lives in [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/) `storefront/model/model.yaml` (single source of truth).
This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its
published `retailnetwork.*` events; it never calls an in-org or restricted network route.

Runtime: Go, hexagonal, real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` are mandatory), CloudEvents 1.0 on every message
(`com.retailnetwork.storefront.shopping-cart.<entity>.<Event>`), transactional outbox.

## Observability

Logs are JSON on stdout (`LOG_LEVEL`) with `trace_id`/`span_id` when a span is active. Traces and metrics leave over OTLP/gRPC to
`OTEL_EXPORTER_OTLP_ENDPOINT` (default the in-cluster Collector; export never blocks the service), named by `OTEL_SERVICE_NAME`
(default `shopping-cart`), `SERVICE_VERSION` and `ENVIRONMENT`. Every request has a server span and a request-log line sharing a trace id;
the outbox carries `traceparent` into Kafka and each consumer continues the trace. Business metric: `cart.lines.added`
(`outcome`, `reason`). See [ADR 0005](docs/adr/0005-fleet-observability-standard.md).

## A2A agent (read-only)

`cmd/a2a` serves an Agent2Agent agent beside the REST API (ADR [0006](docs/adr/0006-a2a-inbound-adapter.md)): the
same read use cases as the two REST GETs, JSON-RPC 2.0 over HTTP, the Agent Card at `/.well-known/agent-card.json`
(JSON-RPC at `/` and `/a2a`, `GET /healthz`). It never writes and exposes exactly what REST exposes.

| Skill | Arguments | Same as |
|---|---|---|
| `get_cart` | `{"cartId": "..."}` | `GET /carts/{cartId}` |
| `list_shopper_carts` | `{"shopperId": "...", "state": "OPEN"}` (`state` optional) | `GET /carts?shopperId=&state=` |

Send a data part `{"skill": "get_cart", "arguments": {"cartId": "..."}}`; the reply is one message with a data part
`{"skill", "data" | "problem"}` and a one-line text summary. Rejections are answered in-band as problems with the REST
status classes (404, 422, 400, 500), never as JSON-RPC errors. Unauthenticated, like every endpoint of the fleet.

Environment: `DATABASE_URL` and `A2A_AGENT_URL` (the absolute URL the card advertises) are mandatory; `A2A_ADDR`
(default `:8093`), `MIGRATIONS_DATABASE_URL`, `CART_CURRENCY` (use the API's value), `LOG_LEVEL`, `OTEL_*`. No Kafka.
Helm: `a2a.enabled=true` (default off), see `charts/shopping-cart/values.yaml`.

Layout: `cmd/a2a` (composition root), `internal/adapters/inbound/a2a` (card, skills, DTOs).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

See `docs/adr/` for decisions and `docs/before-first-release.md` for the packaging checklist.
