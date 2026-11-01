# shopping-cart

[![CI](https://github.com/IQVO/shopping-cart/actions/workflows/ci.yml/badge.svg?branch=develop)](https://github.com/IQVO/shopping-cart/actions/workflows/ci.yml?query=branch%3Adevelop) [![coverage gate](https://img.shields.io/badge/coverage%20gate-90%25-brightgreen)](https://github.com/IQVO/shopping-cart/blob/develop/Makefile) [![Go](https://img.shields.io/badge/go-1.27.3-00ADD8?logo=go&logoColor=white)](https://github.com/IQVO/shopping-cart/blob/develop/go.mod) [![release](https://img.shields.io/github/v/release/IQVO/shopping-cart?sort=semver)](https://github.com/IQVO/shopping-cart/releases) [![docs](https://img.shields.io/badge/docs-iqvo.github.io-blue)](https://iqvo.github.io/shopping-cart/) [![license](https://img.shields.io/badge/license-MIT-blue)](https://github.com/IQVO/shopping-cart/blob/develop/LICENSE)

Storefront (e-commerce) domain of the retail-network organisation, bounded context **Shopping Cart** (Supporting, storefront tier).

Holds a shopper's intent: cart lines, the destination postal prefix, the delivery quote delivery-promise returned for the cart (per shipment group, with its offers and expiry), the shopper's tier selection per group, and a labelled, non-binding estimate (effective prices + selected tier charges + tax band) so the total cost is visible up front [1].

Status: built to the model (domain, application, Postgres, Kafka, REST, BDD). This repository is part of the storefront domain: it consumes the retail network's Open Host Service routes and its published `retailnetwork.*` events; it never calls an in-org or restricted network route.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

Full documentation: **<https://iqvo.github.io/shopping-cart/>** (Docusaurus, built from `docs/` and
published from `main`). The site is written from the code; the design pack it
mirrors lives in [warehouse-docs](https://iqvo.github.io/warehouse-docs/storefront/contexts/shopping-cart).

| Area | Pages |
|---|---|
| Overview | [Introduction](https://iqvo.github.io/shopping-cart/docs/overview/introduction), [Architecture](https://iqvo.github.io/shopping-cart/docs/overview/architecture), [Quickstart](https://iqvo.github.io/shopping-cart/docs/overview/quickstart) |
| Operations | [Configuration](https://iqvo.github.io/shopping-cart/docs/operations/configuration), [Runbook](https://iqvo.github.io/shopping-cart/docs/operations/runbook) (source: [`docs/docs/operations/runbook.md`](docs/docs/operations/runbook.md)), [Observability](https://iqvo.github.io/shopping-cart/docs/operations/observability), [Troubleshooting](https://iqvo.github.io/shopping-cart/docs/operations/troubleshooting), [Before the first release](https://iqvo.github.io/shopping-cart/docs/operations/before-first-release) (source: [`docs/docs/operations/before-first-release.md`](docs/docs/operations/before-first-release.md)) |
| Development | [Testing](https://iqvo.github.io/shopping-cart/docs/development/testing) |
| Domain-driven design | [Ubiquitous language](https://iqvo.github.io/shopping-cart/docs/ddd/ubiquitous-language), [Use cases](https://iqvo.github.io/shopping-cart/docs/ddd/use-cases), [Subdomain classification](https://iqvo.github.io/shopping-cart/docs/ddd/subdomain-classification) |
| DDD artifacts (ddd-crew) | [Index](https://iqvo.github.io/shopping-cart/docs/ddd/ddd-artifacts), [Core domain chart](https://iqvo.github.io/shopping-cart/docs/ddd/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/shopping-cart/docs/ddd/bounded-context-canvas), [Context map](https://iqvo.github.io/shopping-cart/docs/ddd/context-map), [Aggregate design canvas](https://iqvo.github.io/shopping-cart/docs/ddd/aggregate-design-canvas), [Domain message flow](https://iqvo.github.io/shopping-cart/docs/ddd/domain-message-flow), [EventStorming](https://iqvo.github.io/shopping-cart/docs/ddd/eventstorming), [Class diagrams](https://iqvo.github.io/shopping-cart/docs/ddd/class-diagram), [Entity-relationship](https://iqvo.github.io/shopping-cart/docs/ddd/entity-relationship), [Sequence diagrams](https://iqvo.github.io/shopping-cart/docs/ddd/sequence-diagrams), [Domain events](https://iqvo.github.io/shopping-cart/docs/ddd/domain-events) |
| API reference | [Overview (REST and A2A)](https://iqvo.github.io/shopping-cart/docs/api-reference), the generated REST reference from [`apis/openapi.yaml`](apis/openapi.yaml), [Event catalogue](https://iqvo.github.io/shopping-cart/docs/api-reference/events) from [`apis/asyncapi.yaml`](apis/asyncapi.yaml) |
| Ecosystem | [Integration](https://iqvo.github.io/shopping-cart/docs/ecosystem/integration) |
| Decisions | [ADR index](https://iqvo.github.io/shopping-cart/docs/adr) (sources in [`docs/adr/`](docs/adr/)); the [storefront ADR index](https://iqvo.github.io/warehouse-docs/storefront/adr/) |

Preview locally with `cd docs && npm ci && npm start`.

## Architecture

Hexagonal / ports & adapters; dependencies point **inward only**.

```
cmd/api/                    composition root: REST API, outbox relay, the three Kafka
                            consumers and the two clock-driven policies
cmd/a2a/                    read-only A2A agent over the same read use cases (ADR 0006)
internal/
  domain/                   pure business logic: cart (the aggregate), estimate, shared
  application/
    ports/                  OUT interfaces only (repos, EventPublisher, Clock, metrics)
    usecases/               one struct per use case
    readmodel/              the local sibling read models (ADR 0003)
    idempotency/            idempotency-key pool
    outbox/                 transactional outbox
    views/                  read views
  adapters/
    inbound/                http (chi), kafka (the three consumers), a2a, requestlog, scheduler
    outbound/               postgres, kafka, outbox relay, telemetry, deliverypromise
                            (the one sibling client, ADR 0003), clock, ids, memory
                            (test doubles), repocontract
    kafka/cloudevents/      the CloudEvents 1.0 Kafka writer
  architecture/             the fitness tests below
  bootretry/                bounded Postgres/migration boot retry
  bdd/                      godog wiring for features/
```

There is no in-memory mode and no log sink: `cmd/api` needs a database and a
broker, and exits at boot naming the missing variable. The layering is enforced
as an executable test, not a convention:
`internal/architecture/architecture_test.go` (`TestHexagonalArchitecture`,
arch-go) fails the build if the domain ever imports an outer layer or an
adapter reaches sideways into a sibling; the same package adds fitness tests
for the event catalogue vs `apis/asyncapi.yaml`, the A2A adapter's read-only
rule, Kafka writer acks/topics/DLQ shutdown, the outbox relay cap, and
no-Go-files-at-root.

## Domain events

Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml); the type catalogue is ADR 0004.

**Published** — four events, all analytics-only (decision H1: removal, quantity
changes and merge raise none), through the transactional outbox
(at-least-once, stable CloudEvents `id` for dedupe) to
`retailnetwork.shopping-cart.analytics`:

`CartLineAdded` (AddLine), `CartDeliveryQuoted` (QuoteDelivery),
`CartAbandoned` and `CartCheckedOut` (the clock-driven policies).

**Consumed** — three topics, three stable consumer groups (mandatory env vars):

- from `retailnetwork.offer-availability.events`: `CatalogItemCreated`,
  `CatalogItemDiscontinued`
- from `retailnetwork.pricing.events`: `ItemPriceChanged`,
  `ItemPriceWithdrawn`, `DeliveryChargeScheduleChanged`, `TaxTableChanged`
- from `retailnetwork.checkout.events`: `CheckoutCompleted`

They feed the local read models `PurchasabilityView` and `EstimateView` and
run the `CloseOnCheckoutCompleted` policy (ADR 0003). The messages list only
the fields this service relies on; the producer's own AsyncAPI is
authoritative, unknown fields are ignored.

**Envelope** (mandatory): CloudEvents 1.0, structured content mode, Kafka
header `content-type: application/cloudevents+json; charset=UTF-8`; `source` =
`/retailnetwork/shopping-cart`, `subject` and Kafka key = the cartId,
`dataschema` = `urn:retailnetwork:shopping-cart:analytics:<EventName>:v1`.
Type convention, real examples:

```
com.retailnetwork.storefront.shopping-cart.cart.CartLineAdded
com.retailnetwork.storefront.shopping-cart.cart.CartCheckedOut
```

A breaking payload change is a new `.v2` type, never a mutation.

## Running it

Real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS`,
`DELIVERY_PROMISE_URL` and the three consumer groups are mandatory; a boot
error names the missing variable). Migrations run automatically at boot.
There is no compose file in this repo — point the variables at any
Postgres/Kafka (the fleet's `warehouse-infra` kind cluster or the integration
tests' testcontainers both work).

```sh
export DATABASE_URL="postgres://user:***@localhost:5432/shopping_cart?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export DELIVERY_PROMISE_URL="http://localhost:8082"
export OFFER_AVAILABILITY_CONSUMER_GROUP=shopping-cart-offer-availability
export PRICING_CONSUMER_GROUP=shopping-cart-pricing
export CHECKOUT_CONSUMER_GROUP=shopping-cart-checkout
go run ./cmd/api      # REST on :8080; relay, consumers and policies in the same process
```

`GET /readyz` is 200 only while Postgres answers a ping AND a Kafka broker
answers a metadata request; `/healthz` is liveness only.

### Configuration

`cmd/api`:

| Env var | Default | Meaning |
|---|---|---|
| `HTTP_ADDR` | `:8080` | REST listen address |
| `DATABASE_URL` | **required** | Postgres DSN; unset ⇒ boot error naming the variable |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct (non-pooled) DSN for the boot migrations |
| `KAFKA_BROKERS` | **required** | Comma-separated brokers (relay, consumers, `/readyz`) |
| `DELIVERY_PROMISE_URL` | **required** | Base URL of delivery-promise (QuoteDeliveryOffers) |
| `DELIVERY_PROMISE_TIMEOUT` | `5s` | Per-call timeout (Go duration) |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | **required** | Stable group ids of the three consumers (boot error naming the variable) |
| `PRICING_CONSUMER_GROUP` | **required** | |
| `CHECKOUT_CONSUMER_GROUP` | **required** | |
| `CART_CURRENCY` | `USD` | Estimate currency before any price is known |
| `ABANDON_SWEEP_INTERVAL` | `1h` | Policy AbandonInactive tick (Go duration) |
| `PURGE_SWEEP_INTERVAL` | `6h` | Policy PurgeAbandonedCarts tick (Go duration) |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Relay poll interval (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row (`0` = unlimited) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait after flipping `/readyz` before closing |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `shopping-cart` | `service.name` of spans and metrics |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | in-cluster Collector | OTLP/gRPC endpoint; export never blocks |
| `SERVICE_VERSION` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `deployment.environment.name` |

`cmd/a2a` reads `DATABASE_URL` and `A2A_AGENT_URL` (the absolute URL the card
advertises; both **mandatory**), `A2A_ADDR` (default `:8093`),
`MIGRATIONS_DATABASE_URL` (default `DATABASE_URL`), `CART_CURRENCY` (set it to
the API's value so both surfaces agree), `LOG_LEVEL` and the shared telemetry
variables (`OTEL_SERVICE_NAME` defaults to `shopping-cart-a2a`). No Kafka.

## Observability

Logs are JSON on stdout (`LOG_LEVEL`) with `trace_id`/`span_id` when a span is
active. Traces and metrics leave over OTLP/gRPC to
`OTEL_EXPORTER_OTLP_ENDPOINT` (default the in-cluster Collector; export never
blocks the service), named by `OTEL_SERVICE_NAME` (default `shopping-cart`),
`SERVICE_VERSION` and `ENVIRONMENT`. Every request has a server span and a
request-log line sharing a trace id; the outbox carries `traceparent` into
Kafka and each consumer continues the trace. See
[ADR 0005](docs/adr/0005-fleet-observability-standard.md).

| Instrument | Kind | Source |
|---|---|---|
| `cart.lines.added` | counter, attributes `outcome`, `reason` | **this service's business metric** |
| HTTP request duration | histogram + one server span and request-log line per call | the HTTP adapter, sharing a trace id |
| `go.*` runtime metrics | various | OTel Go runtime instrumentation |

## API

Contract: [`apis/openapi.yaml`](apis/openapi.yaml). No authentication. Errors
are RFC 9457 problem details (`application/problem+json`) whose `type` is
`/problems/<slug>`. Every `POST` takes an `Idempotency-Key` header (1 to 128
printable ASCII characters, one fresh UUID per logical submit): a retry with the
same key and the same request replays the first response with
`Idempotency-Replayed: true`, the same key with another request is
`409 idempotency-key-reused`, no key is `400 idempotency-key-required`. `PUT` and
`DELETE` are idempotent by method and need no key. `GET /carts` is keyset-paged:
`limit` (1 to 500, default 100) and `cursor`, answered as
`{"items": [...], "nextCursor": "..."}` (`nextCursor` is absent on the last page;
a bad value is `400 invalid-limit` / `invalid-cursor`). Follow `nextCursor` until
it is absent. See ADR [0008](docs/adr/0008-adopt-the-fleet-rest-api-guideline.md).

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/carts` | Create a cart |
| `GET` | `/carts?shopperId=&state=&limit=&cursor=` | List carts, one page (`{items, nextCursor}`) |
| `GET` | `/carts/{cartId}` | Get one cart |
| `POST` | `/carts/{cartId}/lines` | Add a line |
| `PUT` | `/carts/{cartId}/lines/{catalogItemId}` | Set a line's quantity |
| `DELETE` | `/carts/{cartId}/lines/{catalogItemId}` | Remove a line |
| `PUT` | `/carts/{cartId}/destination` | Set the destination postal prefix (clears the quote and the selections) |
| `POST` | `/carts/{cartId}/delivery-quotes` | Quote delivery offers for the cart |
| `PUT` | `/carts/{cartId}/delivery-selection` | Select the delivery tier per shipment group |
| `POST` | `/carts/{cartId}/merges` | Merge this cart into another |
| `POST` | `/carts/{cartId}/delivery-requote-acceptances` | Accept re-quoted offers |
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness (Postgres ping + Kafka metadata) |

## Helm chart

[`charts/shopping-cart`](charts/shopping-cart) — one always-on component: the
`api` Deployment (REST, relay, the three consumers and the two policies in one
process); the read-only A2A agent is an optional extra component
(`a2a.enabled=true`, default off). The chart refuses to render without
`database.*`, `kafka.brokers`, `deliveryPromise.url` and the three
`config.*ConsumerGroup` values (named in the failure). `DATABASE_URL` always
comes from a Secret; consumer groups must be stable, service-prefixed ids —
never the pod name (a rescheduled pod must resume from its committed offsets).

```sh
helm install shopping-cart ./charts/shopping-cart \
  --set database.existingSecret=shopping-cart-db \
  --set kafka.brokers=kafka:9092 \
  --set deliveryPromise.url=http://delivery-promise:80 \
  --set config.offerAvailabilityConsumerGroup=shopping-cart-offer-availability \
  --set config.pricingConsumerGroup=shopping-cart-pricing \
  --set config.checkoutConsumerGroup=shopping-cart-checkout
```

Telemetry is the `otel` values block (`enabled: true` by default, safe where
no Collector is deployed). Chart invariants are asserted by
`charts/shopping-cart/tests/` (`render_test.sh`, `test_service_selectors.py`),
run in CI by the `helm-lint` (`ct lint`) and `chart-tests` jobs.

### Running the A2A agent in Kubernetes

`cmd/a2a` serves an Agent2Agent agent beside the REST API (ADR
[0006](docs/adr/0006-a2a-inbound-adapter.md)): the same read use cases as the
two REST GETs, JSON-RPC 2.0 over HTTP, the Agent Card at
`/.well-known/agent-card.json` (JSON-RPC at `/` and `/a2a`, `GET /healthz`).
It never writes and exposes exactly what REST exposes.

| Skill | Arguments | Same as |
|---|---|---|
| `get_cart` | `{"cartId": "..."}` | `GET /carts/{cartId}` |
| `list_shopper_carts` | `{"shopperId": "...", "state": "OPEN"}` (`state` optional) | `GET /carts?shopperId=&state=` with every page followed (the newest 1000 carts at most) |

Send a data part `{"skill": "get_cart", "arguments": {"cartId": "..."}}`; the
reply is one message with a data part `{"skill", "data" | "problem"}` and a
one-line text summary. Rejections are answered in-band as problems with the
REST status classes (404, 422, 400, 500), never as JSON-RPC errors.
Unauthenticated, like every endpoint of the fleet.

- Layout: `cmd/a2a` (composition root), `internal/adapters/inbound/a2a` (card, skills, DTOs).
- Environment: `DATABASE_URL` and `A2A_AGENT_URL` mandatory; `A2A_ADDR` (default `:8093`), `MIGRATIONS_DATABASE_URL`, `CART_CURRENCY`, `LOG_LEVEL`, `OTEL_*`. No Kafka.
- Helm: `a2a.enabled=true` (default off), see `charts/shopping-cart/values.yaml`.

## Local development / quality gate

Every CI sensor is a `make` target; `make help` lists them all.

```sh
make check-fast  # fmt-check, vet, arch-test (+ tests of changed packages)
make check       # fmt-check, vet, build, lint, test
make check-all   # check + coverage (90% gate over domain+application) + arch-test + bdd
make integration # testcontainers Postgres/Kafka — needs only Docker
make mutation    # gremlins over ./internal/domain
make vuln        # govulncheck ./...
```

Git hooks are managed by [lefthook](https://github.com/evilmartians/lefthook)
(`lefthook.yml`): pre-commit runs `fmt-check` + `vet` + `lint`; pre-push runs
`make check`. Activate once per clone:

```sh
brew install lefthook   # or: go install github.com/evilmartians/lefthook@latest
lefthook install
```

## Tests

```sh
go test ./... -race                        # unit suites
make coverage                              # domain+application coverage, CI gate >= 90%
go test -tags=integration ./...            # testcontainers Postgres/Kafka (Docker only)
go test ./features/ -run TestFeatures -v   # godog/BDD over the real HTTP API
go test ./internal/architecture/... -v     # architecture fitness tests (arch-go)
gremlins unleash ./internal/domain         # mutation, thresholds in .gremlins.yaml
```

CI (`.github/workflows/ci.yml`) runs `lint`, `guide-lint`, `complexity`,
`test` (unit + the 90% coverage gate), `bdd`, `integration`, `mutation-fast`
(the blocking subset), `api-lint` (Spectral over `apis/openapi.yaml`), `vuln`
(govulncheck), `helm-lint` (`ct lint`), `chart-tests`, and `arch-test` on
every push (branches `main`, `develop`) and pull request. The exhaustive
`mutation` job runs only on the weekly schedule (Monday 06:00 UTC) and manual
dispatch, opening a `harness:red` issue on a scheduled failure.
`docker-publish` (GHCR, cosign-signed) and `release` (tag `vX.Y.Z` + chart
publish) run only on pushes to `main`, after every other job is green.

## Architecture Decision Records

[`docs/adr/`](docs/adr/) — 8 ADRs (0001 bounded context, 0002 assumptions,
0003 consumed contracts and local read model, 0004 CloudEvents envelope and
type catalogue, 0005 fleet observability standard, 0006 A2A inbound adapter,
0007 delivery dates in the destination's local calendar, 0008 fleet REST API
guideline).

## License

[MIT](LICENSE).
