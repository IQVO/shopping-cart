---
id: observability
title: Observability
sidebar_label: Observability
description: "Metrics, traces and logs of Shopping Cart: every OpenTelemetry instrument and span, the log fields, the exporter variables and the dashboard and alerts that exist in warehouse-infra."
---

# Observability

Shopping Cart follows the fleet observability standard (ADR 0005): structured
JSON logs on stdout, OpenTelemetry traces and metrics exported over OTLP/gRPC,
and trace-correlated log lines. Both binaries (`cmd/api` and `cmd/a2a`) call
`telemetry.Setup` (`internal/adapters/outbound/telemetry/telemetry.go`); only
`cmd/api` has the HTTP, consumer and business instrumentation.

## Exporter

Export is deliberately non-blocking: the gRPC exporters connect lazily, so an
absent or dead Collector costs telemetry (exporter errors are logged at DEBUG as
`opentelemetry error`) and never makes the service refuse to start or hang a
request. On shutdown the providers are flushed with a 5 s deadline, last of all,
so the spans of the drain still leave the process.

| Variable | `cmd/api` default | `cmd/a2a` default | Effect |
| --- | --- | --- | --- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | `localhost:4317` | Collector OTLP/gRPC endpoint, `host:port` or URL. A bare `host:port` is rewritten to `http://host:port`. The Helm chart sets `otel-collector.observability.svc.cluster.local:4317` |
| `OTEL_SERVICE_NAME` | `shopping-cart` | `shopping-cart-a2a` | `service.name` of every span and metric, and the name of the server spans and HTTP metrics |
| `SERVICE_VERSION` | `dev` | `dev` | `service.version` |
| `ENVIRONMENT` | `local` | `local` | `deployment.environment.name` |
| `LOG_LEVEL` | `info` | `info` | `debug`, `info`, `warn`/`warning`, `error` |

`OTEL_RESOURCE_ATTRIBUTES` is honoured by the SDK as usual. Metrics are pushed
every 15 s by a periodic reader. The propagator is W3C `tracecontext` plus
`baggage`. Startup logs `telemetry configured` with `service_name` and
`otlp_endpoint`.

## Metrics

Instruments registered by this repository, plus the ones its middleware and the
Go runtime collector register.

| Instrument | Type | Unit | Attributes | Where | Meaning |
| --- | --- | --- | --- | --- | --- |
| `cart.lines.added` | Int64 counter | `{line}` | `outcome` (`added` or `rejected`); `reason` (only when `rejected`) | `cmd/api`, `internal/adapters/outbound/telemetry/cart_metrics.go`, meter `github.com/IQVO/shopping-cart` | Every `AddLine` attempt by outcome. `reason` is the REST problem slug: `item-not-purchasable`, `quantity-out-of-range`, `too-many-lines`, `cart-closed`, `not-found`, `concurrent-update`, `validation-failed`, or `internal-error` for anything else (`internal/application/usecases/cart_metrics.go`) |
| `http.server.request.duration` | Float64 histogram | `s` | `http.method`, `http.scheme`, `http.route` | `cmd/api`, `otelchimetric` middleware | Duration of every REST request, route label is the chi pattern (`/carts/{cartId}`), never the raw path. Buckets 5 ms to 10 s |
| Go runtime metrics (goroutines, memory, GC) | gauges and counters | per instrument | none | `cmd/api` and `cmd/a2a`, `go.opentelemetry.io/contrib/instrumentation/runtime` | `runtime.Start` on the same MeterProvider; the dashboard charts the goroutine count and memory used |

The first panel to read is the business counter: a falling `added` share with a
rising `item-not-purchasable` means shoppers are being refused items the
storefront shows, so the local catalogue and price copies (`rm_items`) are stale
or empty (see [Troubleshooting](/docs/operations/troubleshooting)); a rising
`internal-error` means the service itself is failing. Prometheus exposes the
counter as `cart_lines_added_total`.

There is no `/metrics` endpoint and no Prometheus scrape of the pod: metrics
leave through OTLP only. `cmd/a2a` registers no metric of its own and has no
HTTP duration histogram.

## Traces

| Span | Kind | Created by | Attributes |
| --- | --- | --- | --- |
| `<METHOD> <route pattern>` (for example `POST /carts/{cartId}/lines`) | server | `otelchi` middleware on the `cmd/api` router | `http.route`, `http.status_code`, plus the otelchi defaults. One span name per route, not per cart id |
| `<topic> process` (for example `retailnetwork.pricing.events process`) | consumer | `internal/adapters/inbound/kafka/tracing.go`, one per handling attempt | `messaging.system=kafka`, `messaging.destination.name`, `messaging.operation.name=process`; an error is recorded on the span and sets status `Error` |
| `a2a.skill <skill>` (`get_cart`, `list_shopper_carts`) | internal | `internal/adapters/inbound/a2a/skills.go`, only for a known skill | `a2a.skill`; `a2a.problem.status` when the skill answered a problem, status `Error` when that status is 5xx |

Trace context travels across Kafka in both directions:

- **Out.** When an event is appended to the outbox, the active span context is
  injected as `traceparent`/`tracestate` into the row's headers, in the same
  transaction (`internal/adapters/outbound/postgres/outbox_trace.go`). The relay
  publishes them as ordinary Kafka headers, so a consumer of
  `retailnetwork.shopping-cart.analytics` can continue the trace of the request
  that added the line.
- **In.** The consumers extract `traceparent` from the message headers, so a
  `<topic> process` span is a child of the producer's span (a message without
  headers starts a new trace), and anything the handler enqueues continues it.

## Logs

JSON via `log/slog` on stdout, one object per line with `time`, `level`, `msg`
and the fields below. The handler stamps `trace_id` and `span_id` on every record
written through the `*Context` slog variants while a span is active
(`internal/adapters/outbound/telemetry/slogotel.go`); startup, shutdown and
background lines have none. Cluster log collection adds the `app` label that the
dashboard filters on (`{app="shopping-cart"}`).

| Where | Level | `msg` (abridged) | Fields |
| --- | --- | --- | --- |
| HTTP access log | INFO / WARN (4xx) / ERROR (5xx) | `http request` (one per request, `/healthz` and `/readyz` skipped) | `method`, `route` (pattern, `unmatched` when none), `status`, `duration_ms`, `bytes`, `request_id`, `trace_id`, `span_id` |
| Boot | INFO | `telemetry configured`, `postgres adapters configured`, `http server listening` | `service_name`, `otlp_endpoint` / `addr` |
| Boot | INFO | `consumer running` | `consumer` (`offer-availability consumer`, `pricing consumer`, `checkout consumer`), `topic`, `group_id`, `brokers` |
| Boot | INFO | `outbox relay running`, `scheduler running` | `interval`, `max_attempts`, `topic` / `job`, `interval` |
| Boot retry | WARN / INFO | `retrying` / `succeeded after retry` | `op` (`run migrations`, `ping postgres`), `attempt`, `in`, `err` |
| Boot | WARN | `invalid ABANDON_SWEEP_INTERVAL; using the default` (and the same for `PURGE_SWEEP_INTERVAL`), `invalid OUTBOX_RELAY_INTERVAL; using the default`, `invalid OUTBOX_MAX_ATTEMPTS; using the default`, `ignoring invalid SHUTDOWN_DRAIN_DELAY` | `value`, `default` |
| Boot failure | ERROR | `service exited with error` (api) / `a2a agent exited with error` (a2a); the process exits 1 | `error` |
| Shutdown | INFO / WARN | `shutdown: readiness flipped to not-ready; waiting for traffic to drain` / `worker did not stop before the shutdown drain deadline` / `telemetry flush failed on shutdown` | `drain_delay` / `worker` / `error` |
| Worker death | ERROR | `consumer stopped`, `outbox relay stopped`, `scheduler stopped`; then the process exits with `<worker> stopped unexpectedly: exiting so the orchestrator restarts the service` | `consumer` / `job`, `error` |
| Consumer | WARN | `skipping a message that is not a valid CloudEvent` | `consumer`, `topic`, `error` |
| Consumer | WARN | `dead-lettering an event that can never be applied` / `skipping an event that can never be applied` | `consumer`, `type`, `event_id`, `error` |
| Consumer | WARN | `<consumer> broker connection stalled or lost; fetching again after the backoff` | `error`, `attempt`, `backoff` (at most 10 consecutive attempts) |
| Consumer | ERROR | `<consumer> handling failed; retrying the same message` | `error`, `partition`, `offset`, `attempt`, `max_attempts` (5), `retry_in` |
| Consumer | ERROR | `<consumer> handling failed after the maximum attempts; dead-lettering the message` / `<consumer> message can never be applied; dead-lettering it without retry` | `error`, `partition`, `offset` (`attempts` for the first) |
| Consumer | ERROR | `<consumer> offset commit failed; retrying the same message`, `<consumer> dead-letter publish failed; retrying the same message` | `error`, `partition`, `offset`, `attempt`, `retry_in` |
| Consumer | DEBUG | `event processed` | `type`, `event_id`, `what` |
| Domain | WARN | `CheckoutCompleted names an unknown cart`, `CheckoutCompleted for a cart that is not open` (acknowledged, nothing changes) | `cartId` (`state` for the second) |
| Relay | ERROR | `outbox relay pass failed` | `error` (starts `outbox row <id> (<type>):`) |
| Relay | ERROR | `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | `outbox_id`, `topic`, `event_type`, `key`, `attempts`, `error` |
| Sweepers | INFO / ERROR | `scheduled sweep done` / `scheduled sweep failed; trying again at the next tick` | `job` (`AbandonInactive`, `PurgeAbandonedCarts`), `handled`, `error` |
| Readiness | WARN | `readiness: dependency is not ready` (at most one round per 3 s) | `dependency` (`postgres` or `kafka`), `error` |
| Delivery quote | WARN | `delivery window time zone unusable; delivery dates fall back to UTC` (once per distinct zone name, ADR 0007) | `timeZone`, `reason` |
| A2A | ERROR | `a2a skill failed` (an unrecognised error, answered as a 500 problem without the cause) | `skill`, `error`, `trace_id`, `span_id` |

REST errors are RFC 9457 `application/problem+json` documents whose `type` is
`/problems/<slug>`; a 500 never echoes the underlying error, which exists only
in the log. The slug and status table is on
[Troubleshooting](/docs/operations/troubleshooting#rest-problem-types).

## Dashboards and alerts that exist

Both live in warehouse-infra on `origin/develop` and are provisioned by its
Terraform. This repository ships neither.

**Dashboard** `warehouse-shopping-cart` ("Warehouse — Shopping Cart",
`terraform/dashboards/contexts/shopping-cart.json`):

| Row | Panels |
| --- | --- |
| API Gateway (Kong) | request rate by status code, 5xx ratio, latency p95 (Kong request vs upstream) |
| Service HTTP RED | request rate by route (`http_server_request_duration_seconds_count`), 5xx rate by Kong route, request duration p95 by process |
| Shopping Cart business metrics | `cart_lines_added_total` by `outcome` |
| Go runtime | goroutines, memory used |
| Logs | live stream `{app="shopping-cart"}`, volume by level, errors and warnings only |

**Alert rules** (`terraform/alerting/warehouse-alert-rules.json`), all labelled
`context: shopping-cart`:

| Group | Alert | Fires when | Severity |
| --- | --- | --- | --- |
| `warehouse-shopping-cart` | `WarehouseKong5xxRatioHigh` | more than 5% of requests on a Kong route of this context answer 5xx for 5 min | warning |
| `warehouse-shopping-cart` | `WarehousePodRestarting` | a `shopping-cart-*` container restarted 2 or more times in 15 min (boot-retry exhaustion, a failed migration, a consumer or the relay that exits on purpose, an OOM kill) | warning |
| `warehouse-shopping-cart-slo` | `WarehouseApiAvailabilityBurnFast` / `BurnSlow` | 5xx error budget (99% availability) burning at 14.4x over 1 h and 5 m / 3x over 6 h and 30 m | critical / warning |
| `warehouse-shopping-cart-slo` | `WarehouseApiLatencyBurnFast` / `BurnSlow` | upstream latency above 400 ms for more than 5% of requests, same burn windows | critical / warning |

There is no alert on the outbox backlog, consumer lag or dead letters, and no
metric for them. Until one exists, use the checks below.

## Suggested checks

These are built from data that exists; they are not rules that ship with the
repo.

| Signal | How to read it |
| --- | --- |
| Outbox backlog | `SELECT count(*), min(created_at) FROM outbox_events WHERE published_at IS NULL` (an oldest row older than a few minutes means the relay or the broker is down) |
| Rows stuck at the attempt cap | the ERROR line `row reached the attempt cap`, or `SELECT id, event_type, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20` (use your `OUTBOX_MAX_ATTEMPTS`) |
| Dead letters | ERROR lines containing `dead-lettering`, and the depth of `retailnetwork.pricing.events.dlq`, `retailnetwork.offer-availability.events.dlq`, `retailnetwork.checkout.events.dlq` |
| Consumer stuck on one message | repeated `handling failed; retrying the same message` with the same `partition` and `offset` |
| Readiness | `GET /readyz` answers 503 `dependency-unavailable` naming `postgres` or `kafka` |
| Empty or stale read models | `SELECT count(*) FROM rm_items`; the `item-not-purchasable` share of `cart.lines.added` |
| Table growth | row counts of `outbox_events`, `idempotency_keys`, `processed_events` (nothing deletes them in this repo) |
