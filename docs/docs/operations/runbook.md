# Shopping Cart — Operations Runbook

Shopping-cart owns cart lines, delivery quotes and a labelled cost estimate. One
deployable: `cmd/api` (REST, the transactional outbox relay, the three Kafka
consumers — offer-availability, pricing, checkout — and the two clock-driven
sweep policies in a single process). Runtime: real Postgres and real Kafka only
— there is no in-memory or permissive mode, so a pod that cannot reach either
refuses to boot and never pretends to be healthy.

## Deploy

The chart is `charts/shopping-cart` (packaged by the `release` job as
`oci://ghcr.io/iqvo/shopping-cart:<version>`; the image is
`ghcr.io/iqvo/shopping-cart:v<version>`). The kind cluster deploys it through
the warehouse-infra ArgoCD app with the local image `warehouse/shopping-cart`.

Mandatory values — the chart refuses to render without them:

| Value | Env var | Why |
| --- | --- | --- |
| `database.url` or `database.existingSecret` | `DATABASE_URL` | Postgres DSN (from a Secret, never the ConfigMap) |
| `database.migrationsExistingSecretKey` / `database.migrationsUrl` | `MIGRATIONS_DATABASE_URL` | Direct (non-pooled) DSN for boot migrations; falls back to `DATABASE_URL` |
| `kafka.brokers` | `KAFKA_BROKERS` | Comma-separated brokers (relay, consumers, `/readyz`) |
| `deliveryPromise.url` | `DELIVERY_PROMISE_URL` | The cart quotes delivery offers through delivery-promise |
| `config.offerAvailabilityConsumerGroup` | `OFFER_AVAILABILITY_CONSUMER_GROUP` | Stable, service-prefixed group ids — never the pod name |
| `config.pricingConsumerGroup` | `PRICING_CONSUMER_GROUP` | (same) |
| `config.checkoutConsumerGroup` | `CHECKOUT_CONSUMER_GROUP` | (same) |

Useful optionals: `config.cartCurrency` (`CART_CURRENCY`, default `USD`),
`config.abandonSweepInterval` (default 1h), `config.purgeSweepInterval`
(default 6h), `config.outboxRelayInterval` (default 1s),
`config.outboxMaxAttempts` (default 20, 0 = unlimited),
`deliveryPromise.timeout` (default 5s), `config.shutdownDrainDelay` (default
5s). `terminationGracePeriodSeconds` must exceed drain + 70s (HTTP 10s + the
three consumers and two sweeps 10s each + relay 10s); the chart refuses
anything shorter.

Deploy / upgrade:

```sh
helm upgrade --install shopping-cart charts/shopping-cart \
  --namespace shopping-cart --create-namespace \
  --set database.existingSecret=shopping-cart-db \
  --set kafka.brokers=kafka.kafka.svc.cluster.local:9092 \
  --set deliveryPromise.url=http://delivery-promise.delivery-promise.svc.cluster.local:8080 \
  --set config.offerAvailabilityConsumerGroup=shopping-cart-offer-availability \
  --set config.pricingConsumerGroup=shopping-cart-pricing \
  --set config.checkoutConsumerGroup=shopping-cart-checkout
kubectl -n shopping-cart rollout status deployment/shopping-cart
```

## Rollback

```sh
helm history shopping-cart -n shopping-cart
helm rollback shopping-cart <revision> -n shopping-cart
kubectl -n shopping-cart rollout status deployment/shopping-cart
```

The boot migrations are forward-only; before rolling back across a release
that added a migration, confirm the older binary tolerates the newer schema
(columns are additive fleet-wide). The consumer groups are stable, so the
rolled-back pods resume from the committed offsets — no replay storm.

## Health

- `GET /healthz` — liveness only (startup + liveness probes).
- `GET /readyz` — 200 only while Postgres answers a ping AND a Kafka broker
  answers a metadata request (verdict cached 3s, bounded 2s). Flips to 503
  first during shutdown, before the listener drains.

```sh
kubectl -n shopping-cart port-forward svc/shopping-cart 8080:80
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

A pod that exits non-zero on its own means the relay, a consumer or a sweep
policy died — the process exits deliberately so the orchestrator restarts it;
check `kubectl -n shopping-cart logs deploy/shopping-cart --previous` for the
worker name in the final log line.

## Kafka

| Topic | Direction | Notes |
| --- | --- | --- |
| `retailnetwork.shopping-cart.analytics` | produced (outbox relay) | cart analytics events |
| `retailnetwork.offer-availability.events` | consumed | group `shopping-cart-offer-availability` (values-overridable) |
| `retailnetwork.pricing.events` | consumed | group `shopping-cart-pricing` (values-overridable) |
| `retailnetwork.checkout.events` | consumed | group `shopping-cart-checkout` (values-overridable) |
| `<topic>.dlq` | dead letters | poison messages land here; re-drive after fixing the cause |

The relay excludes an outbox row after `OUTBOX_MAX_ATTEMPTS` failures; re-arm a
row with `UPDATE outbox_events SET attempts = 0 WHERE id = ...`.

## Postgres

The service connects as role `shopping-cart` (least privilege: its own schema
only — carts, the pricing read-model tables (`rm_items`, `rm_current_schedule`,
`rm_current_tax_table`), inbox (`processed_events`), outbox, idempotency). Boot
migrations run against `MIGRATIONS_DATABASE_URL` (direct, session-mode
connection); when the pooled DSN goes through a transaction-mode pooler, keep
the direct key populated or migrations hang. The migrations/first ping retry
with bounded backoff (`internal/bootretry`), so the startup probe allows ~60s.

## Dashboards and telemetry

Logs are stdout JSON (level from `LOG_LEVEL`) collected by the cluster log
pipeline. Dashboards and alerts for the fleet live in the warehouse-infra
repository (Grafana dashboards under its observability stack, backed by
Prometheus); look for the shopping-cart service panels there.
