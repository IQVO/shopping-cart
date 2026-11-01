---
id: configuration
title: Configuration
sidebar_label: Configuration
description: "Every environment variable read by cmd/api and cmd/a2a: default, required or not, meaning."
---

# Configuration

Shopping Cart ships two binaries and each reads its own environment. Both run on
real Postgres only; `cmd/api` additionally needs real Kafka and the
delivery-promise service. There is no in-memory mode and no permissive mode: a
missing mandatory variable is a boot error that names the variable, raised
before telemetry, any connection or the listener opens.

Durations are Go durations (`30s`, `5m`, `1h`) unless the row says otherwise.
Source: `cmd/api/main.go`, `cmd/api/config.go`, `cmd/api/telemetry.go`,
`cmd/a2a/main.go`, `internal/adapters/outbound/telemetry/telemetry.go`.

## `cmd/api` (REST, consumers, relay, sweepers)

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `HTTP_ADDR` | `:8080` | no | Listen address of the REST server. |
| `DATABASE_URL` | none | **yes** | Postgres DSN of the service role. Boot error `DATABASE_URL is required` when empty. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct (non-pooled, session-mode) DSN used only for the embedded boot migrations. Set it when `DATABASE_URL` goes through a transaction-mode pooler. |
| `KAFKA_BROKERS` | none | **yes** | Comma-separated broker list (blanks trimmed). Used by the outbox relay, the three consumers and the `/readyz` broker check. |
| `DELIVERY_PROMISE_URL` | none | **yes** | Base URL of the delivery-promise service the cart calls to quote delivery offers. |
| `DELIVERY_PROMISE_TIMEOUT` | `5s` | no | Per-call timeout of the delivery-promise client. Must be a positive Go duration, otherwise boot fails naming the variable. |
| `OFFER_AVAILABILITY_CONSUMER_GROUP` | none | **yes** | Kafka consumer group id of the offer-availability consumer. Must be stable and service-prefixed, never the pod name. |
| `PRICING_CONSUMER_GROUP` | none | **yes** | Consumer group id of the pricing consumer. |
| `CHECKOUT_CONSUMER_GROUP` | none | **yes** | Consumer group id of the checkout consumer. |
| `CART_CURRENCY` | `USD` | no | Currency of the labelled estimate when no priced item states one. Exactly three upper-case letters, otherwise boot fails. |
| `ABANDON_SWEEP_INTERVAL` | `1h` | no | Tick of the `AbandonInactive` policy. Invalid or non-positive values log a warning and fall back to the default. |
| `PURGE_SWEEP_INTERVAL` | `6h` | no | Tick of the `PurgeAbandonedCarts` policy. Same fallback rule. |
| `OUTBOX_RELAY_INTERVAL` | `1s` | no | Outbox relay poll interval, a Go duration or plain seconds (`2`, `0.5`). Invalid values warn and use the default. |
| `OUTBOX_MAX_ATTEMPTS` | `20` | no | Failures before the relay stops claiming an outbox row. `0` means unlimited; negative or unparsable values warn and use `20`. |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | no | Wait between flipping `/readyz` to 503 and closing the listener. `0` disables the wait; negative or unparsable values warn and use the default. |
| `LOG_LEVEL` | `info` | no | `debug`, `info`, `warn` (or `warning`), `error`, case-insensitive. Anything else means `info`. |
| `OTEL_SERVICE_NAME` | `shopping-cart` | no | `service.name` of spans and metrics. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector, `host:port` or URL. Export never blocks startup. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. |

All three consumer groups are validated even though each consumer is always on:
a missing one aborts boot with `<NAME> is required: the consumer always runs on
real Kafka`.

## `cmd/a2a` (read-only A2A agent)

`cmd/a2a` is a second deployable (ADR 0006). It reads the same Postgres and
needs no Kafka, no outbox, no consumers, no schedulers and no delivery-promise
client, so none of those variables apply to it. All configuration problems are
reported together, each naming its variable.

| Variable | Default | Required? | Meaning |
| --- | --- | --- | --- |
| `A2A_ADDR` | `:8093` | no | Listen address of the JSON-RPC server. |
| `A2A_AGENT_URL` | none | **yes** | Absolute `http(s)` URL of this agent's JSON-RPC endpoint, advertised on the Agent Card. A relative or schemeless URL is a boot error. |
| `DATABASE_URL` | none | **yes** | Postgres DSN, the same database as `cmd/api`. |
| `MIGRATIONS_DATABASE_URL` | value of `DATABASE_URL` | no | Direct DSN for the boot migrations (they are idempotent, so either binary may start first). |
| `CART_CURRENCY` | `USD` | no | Fallback estimate currency. Validated like `cmd/api`; set it to the same value so both surfaces agree. |
| `LOG_LEVEL` | `info` | no | Same values as `cmd/api`. |
| `OTEL_SERVICE_NAME` | `shopping-cart-a2a` | no | `service.name` of spans. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | no | OTLP/gRPC endpoint of the Collector. |
| `SERVICE_VERSION` | `dev` | no | `service.version` resource attribute. |
| `ENVIRONMENT` | `local` | no | `deployment.environment.name` resource attribute. |

## Helm values to environment

The chart `charts/shopping-cart` maps values to these variables; the mandatory
ones make `helm template` fail when empty. See the
[runbook](/docs/operations/runbook) for the value names.
