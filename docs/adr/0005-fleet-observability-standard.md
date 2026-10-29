# ADR 0005: Adopt the fleet observability standard (logs, metrics, traces)

## Status

Accepted (2026-10-26).

## Context

shopping-cart logged JSON with `log/slog` and nothing else: no trace or span id on
a log line, no request id, no request log, no metrics, no traces and no
`traceparent` on the events it publishes. The warehouse-systems services
(inventory-storage, wes-work-planning) and customer-ordering (ADR 0007) already
share one pipeline and one convention (the "standard metrics convention" ADRs):

- pods export OTLP/gRPC to the in-cluster Collector
  (`otel-collector.observability.svc.cluster.local:4317`), which feeds Jaeger
  (traces) and a Prometheus exporter (metrics);
- logs stay on stdout as JSON for Alloy and Loki.

Without the same wiring a cart request could not be followed through the Kafka
event to a consumer in another service, a log line could not be joined to its
trace, and no dashboard could show add-to-cart rate, errors or latency. This ADR
replicates customer-ordering's shape; the alternatives (a service-specific setup,
auto-instrumentation agents) were rejected there for the same reasons.

## Decision

1. **Telemetry adapter.** `internal/adapters/outbound/telemetry` holds `Setup`
   (OTLP/gRPC traces and metrics, W3C TraceContext + Baggage propagator, Go
   runtime metrics; exporters dial lazily, so no Collector means dropped
   telemetry, never a blocked boot or request; returns a shutdown func) and
   `WithTraceContext` (stamps `trace_id`/`span_id` on a record written with a
   span-carrying context). Only the composition root (`cmd/api`) and adapters
   import it; `internal/domain` and `internal/application` never import
   OpenTelemetry.
2. **Logger.** `slog.New(telemetry.WithTraceContext(slog.NewJSONHandler(os.Stdout, ...)))`,
   `LOG_LEVEL`. Request and message paths already use the `*Context` slog
   variants and keep doing so.
3. **HTTP middleware order** on the chi router: `RequestID` -> `otelchi` ->
   `otelchimetric.NewServerRequestDuration` -> `RequestLogger` -> `Recoverer`;
   `rejectNUL` and the Idempotency-Key middleware stay after them.
   `telemetry.Setup` runs before the router is built and is flushed last on
   shutdown. The request line carries `method`, `route` (the chi pattern, never
   the raw path), `status`, `duration_ms`, `bytes`, `request_id`; 5xx is ERROR,
   4xx WARN, the rest INFO; `/healthz` and `/readyz` are not logged.
   `RequestLogger` lives in `internal/adapters/inbound/requestlog` so a future
   `cmd/mcp` can share it without importing the REST adapter.
4. **Traces over Kafka.** `OutboxRepo.Append` stamps `traceparent`/`tracestate`
   from the active span into the stored outbox headers, inside the transaction
   that writes the aggregate. The relay publishes them unchanged; each of the
   three inbound consumers (offer-availability, pricing, checkout) extracts them
   and runs every handling attempt in a consumer span named `<topic> process`.
   Only extra headers are added: CloudEvents attributes and bodies are unchanged
   (ADR 0004).
5. **Environment contract.** `cmd/api` reads `OTEL_SERVICE_NAME` (default
   `shopping-cart`), `OTEL_EXPORTER_OTLP_ENDPOINT` (default
   `telemetry.DefaultEndpoint`), `SERVICE_VERSION`, `ENVIRONMENT` and
   `LOG_LEVEL`.
6. **Tier 2 business metric.** One counter, `cart.lines.added`, unit `{line}`,
   attribute `outcome` (`added` | `rejected`) and, on a rejection, `reason` (the
   problem slug the REST adapter answers with: `item-not-purchasable`,
   `quantity-out-of-range`, `too-many-lines`, `cart-closed`, `not-found`,
   `concurrent-update`, `validation-failed`, `internal-error`).
   `ports.CartMetrics` is the application port; `AddLine` calls it once per
   attempt; the OTel implementation is in the telemetry adapter and injected from
   `cmd/api`; a nil `Deps.Metrics` is a no-op.

### Why `cart.lines.added`

ADR 0001 and `apis/openapi.yaml` make the cart a held intent whose value to the
business is the shopper putting items in it; `AddLine` is the only command that
depends on the lenient PurchasabilityView (decision H3), the one place this
context can refuse a shopper for reasons outside their control. A falling `added`
share with a rising `item-not-purchasable` says the event-fed read model is stale
or the catalogue/pricing feed is broken, which no HTTP RED metric shows (those
refusals are 422s). `cart.carts.checked_out` was weighed and rejected: it is
raised by a consumer applying another context's event, not by this context's
command, and it duplicates checkout's own counter.

## Assumptions (where the brief was silent)

- **No chart.** Ecommerce repos have no Helm chart yet, so the `otel.*` values and
  chart render assertions are skipped; they land with the chart.
- **No MCP binary, no other cmd.** `cmd/api` is the only deployable, so only one
  router and one set of environment variables are covered.
- A merged re-add (existing item, quantity summed) counts as `added`.
- Dashboards and alert rules live in warehouse-infra and are generated per
  deployed context; shopping-cart is not deployed, so none are added here.

## Consequences

- A request, its log lines, the events it enqueued and the consumers that handle
  them share one trace id.
- Request rate, errors and duration come from `http.server.request.duration`
  with no hand-rolled counter.
- A retried message shows as several consumer spans under the same parent.
- The parent of a consumed event is the span active at the outbox write, not a
  separate publish span (the relay is asynchronous).
- `OutboxRepo.Insert` (the raw path used by tests) stamps nothing; only `Append`
  does.
