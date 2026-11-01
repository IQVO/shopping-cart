---
id: runbook
title: Operations runbook
sidebar_label: Runbook
description: "Deploying, probing, migrating and operating Shopping Cart: Helm chart, Kafka topics and consumer groups, outbox relay, dead letters, sweepers and routine procedures."
---

# Operations runbook

Shopping Cart owns cart lines, delivery quotes and a labelled cost estimate. It
ships two deployables from one image family:

| Binary | Role | Port | Chart component |
| --- | --- | --- | --- |
| `cmd/api` | REST, the transactional outbox relay, three Kafka consumers (offer-availability, pricing, checkout) and two clock-driven sweep policies, all in one process | `:8080` | `deployment.yaml`, always on |
| `cmd/a2a` | Read-only Agent2Agent agent over the same Postgres (`get_cart`, `list_shopper_carts`, ADR 0006) | `:8093` | `a2a-deployment.yaml`, off unless `a2a.enabled=true` |

Runtime is real Postgres and real Kafka only. There is no in-memory or
permissive mode, so a pod that cannot reach either refuses to boot or reports
not-ready and never pretends to be healthy. Every variable is on
[Configuration](/docs/operations/configuration); metrics, logs and alerts are on
[Observability](/docs/operations/observability); symptoms are on
[Troubleshooting](/docs/operations/troubleshooting). The first-release checklist
is [Before first release](/docs/operations/before-first-release).

## Deploy

The chart is `charts/shopping-cart`, packaged by the `release` job as
`oci://ghcr.io/iqvo/shopping-cart:<version>` with the image
`ghcr.io/iqvo/shopping-cart:v<version>`. The kind cluster deploys it through the
warehouse-infra ArgoCD app with the local image `warehouse/shopping-cart`
(`helm-values/shopping-cart.yaml` in warehouse-infra sets the three consumer
groups, `deliveryPromise.url`, `database.existingSecret: shopping-cart-db`, the
Gateway route `/api/shopping-cart` and `a2a.enabled: true` with the route
`/api/a2a/shopping-cart`).

Mandatory values: the chart refuses to render without them and names the
missing one.

| Value | Env var | Why |
| --- | --- | --- |
| `database.url` or `database.existingSecret` | `DATABASE_URL` | Postgres DSN, always injected from a Secret (`secretKeyRef`), never the ConfigMap |
| `database.migrationsExistingSecretKey` / `database.migrationsUrl` | `MIGRATIONS_DATABASE_URL` | Direct (non-pooled, session-mode) DSN for the boot migrations; the Secret key is `optional: true`, the binary falls back to `DATABASE_URL` |
| `kafka.brokers` | `KAFKA_BROKERS` | Comma-separated brokers for the relay, the consumers and `/readyz` |
| `deliveryPromise.url` | `DELIVERY_PROMISE_URL` | `:quote-delivery` calls delivery-promise |
| `config.offerAvailabilityConsumerGroup` | `OFFER_AVAILABILITY_CONSUMER_GROUP` | Stable, service-prefixed group id, never the pod name |
| `config.pricingConsumerGroup` | `PRICING_CONSUMER_GROUP` | same |
| `config.checkoutConsumerGroup` | `CHECKOUT_CONSUMER_GROUP` | same |

The chart also refuses a `*_MODE` variable in `extraEnv`, any variable it
renders itself, and any `*_CONSUMER_GROUP` in `extraEnv`. Useful optionals:
`config.cartCurrency`, `config.abandonSweepInterval` (1h), `config.purgeSweepInterval`
(6h), `config.outboxRelayInterval` (1s), `config.outboxMaxAttempts` (20, `0` =
unlimited), `deliveryPromise.timeout` (5s), `config.shutdownDrainDelay` (5s),
`otel.*` (on by default, Collector `otel-collector.observability.svc.cluster.local:4317`),
`autoscaling.api.*` (off; N>1 replicas are safe for the relay, the version-guarded
writes and the consumers, but it has not been load-tested) and `ingress` /
`gatewayApi` (off).

`terminationGracePeriodSeconds` defaults to `80`. The sequential shutdown needs
the drain delay (5 s) + HTTP drain (10 s) + three consumers and two sweeps
awaited up to 10 s each (50 s) + the relay (10 s) = 75 s worst case, and the
chart refuses any value that does not exceed that sum for the configured drain
delay.

```sh
helm upgrade --install shopping-cart charts/shopping-cart \
  --namespace shopping-cart --create-namespace \
  --set database.existingSecret=shopping-cart-db \
  --set kafka.brokers=kafka.kafka.svc.cluster.local:9092 \
  --set deliveryPromise.url=http://delivery-promise.warehouse-systems.svc.cluster.local:80 \
  --set config.offerAvailabilityConsumerGroup=shopping-cart-offer-availability \
  --set config.pricingConsumerGroup=shopping-cart-pricing \
  --set config.checkoutConsumerGroup=shopping-cart-checkout
kubectl -n shopping-cart rollout status deployment/shopping-cart
```

To add the A2A agent set `--set a2a.enabled=true`. Its `a2a.agentUrl` defaults to
the in-cluster Service DNS (`http://<release>-a2a.<ns>.svc.cluster.local:8093/`);
override it with the gateway URL when the agent must be reachable off-cluster,
because that URL is what the Agent Card advertises.

Static chart checks run offline: `charts/shopping-cart/tests/render_test.sh`
and `charts/shopping-cart/tests/test_service_selectors.py` (see
[Testing](/docs/development/testing)).

## Rollback

```sh
helm history shopping-cart -n shopping-cart
helm rollback shopping-cart <revision> -n shopping-cart
kubectl -n shopping-cart rollout status deployment/shopping-cart
```

The boot migrations are forward-only. Before rolling back across a release that
added a migration, confirm the older binary tolerates the newer schema (columns
are additive fleet-wide). The consumer groups are stable, so rolled-back pods
resume from the committed offsets with no replay storm.

## Health, probes and readiness

| Endpoint | Binary | Meaning |
| --- | --- | --- |
| `GET /healthz` | api and a2a | Liveness only: `{"status":"ok"}` |
| `GET /readyz` | api | 200 only while Postgres answers a ping **and** a Kafka broker answers a topic-less metadata request. The verdict is cached 3 s and each check is bounded to 2 s. It flips to 503 first during shutdown |

Probes in the chart: the api `startupProbe` hits `/healthz` every 2 s with a
failure threshold of 30 (about 60 s, because the migrations and the first ping
run inside a bounded retry, `internal/bootretry`); `livenessProbe` is
`/healthz` every 10 s; `readinessProbe` is `/readyz` every 5 s. The a2a
deployment uses `/healthz` for all three, since it has no Kafka to wait for.

A broker outage therefore makes the pod **not ready but alive**: Kong stops
routing to it and the consumers and relay keep retrying. The consumers dial
Kafka lazily, so a broker that is down at boot never blocks startup.

```sh
kubectl -n shopping-cart port-forward svc/shopping-cart 8080:80
curl -i http://localhost:8080/healthz
curl -i http://localhost:8080/readyz
```

A pod that exits non-zero on its own means the relay, a consumer or a sweep
policy returned unexpectedly: the process exits deliberately
(`<worker> stopped unexpectedly: exiting so the orchestrator restarts the service`)
so the orchestrator restarts it. Read
`kubectl -n shopping-cart logs deploy/shopping-cart --previous` for the worker
name in the final log line.

## Migrations

One migration, `0001_shopping_cart_schema`
(`internal/adapters/outbound/postgres/migrations/`), embedded in the binary and
applied at boot over `MIGRATIONS_DATABASE_URL`. Both `cmd/api` and `cmd/a2a` run
it and it is idempotent, so either may start first. When `DATABASE_URL` goes
through a transaction-mode pooler keep the direct key populated: the migration
holds a session-scoped advisory lock and hangs behind a pooler. Migrations and
the first ping retry with bounded backoff; on exhaustion the last error is
logged (`service exited with error`) and the process exits 1.

Tables: `carts` (state `OPEN`, `CHECKED_OUT`, `ABANDONED`, `MERGED`), the
event-fed read models `rm_items`, `rm_current_schedule`, `rm_current_tax_table`,
the inbox `processed_events`, `idempotency_keys` and the transactional outbox
`outbox_events`.

## Kafka

The service never creates topics for consuming; the broker auto-creates business
topics and dead-letter topics on first write.

| Topic | Direction | Group variable (suggested id) | Handled `type`s |
| --- | --- | --- | --- |
| `retailnetwork.shopping-cart.analytics` | produced by the outbox relay | n/a | `CartLineAdded`, `CartDeliveryQuoted`, `CartAbandoned`, `CartCheckedOut` |
| `retailnetwork.offer-availability.events` | consumed | `OFFER_AVAILABILITY_CONSUMER_GROUP` (`shopping-cart-offer-availability`) | `CatalogItemCreated`, `CatalogItemDiscontinued` |
| `retailnetwork.pricing.events` | consumed | `PRICING_CONSUMER_GROUP` (`shopping-cart-pricing`) | `ItemPriceChanged`, `ItemPriceWithdrawn`, `DeliveryChargeScheduleChanged`, `TaxTableChanged` |
| `retailnetwork.checkout.events` | consumed | `CHECKOUT_CONSUMER_GROUP` (`shopping-cart-checkout`) | `CheckoutCompleted` |
| `<topic>.dlq` | produced by a consumer | n/a | dead letters of the three consumed topics |

Full type names and payloads are on [Events](/docs/api-reference/events). Every
message is a CloudEvents 1.0 structured-mode JSON message; anything else is
skipped with a `skipping a message that is not a valid CloudEvent` warning.

### Consumer behaviour

- One message at a time per consumer; the offset is committed **only after** the
  handler returned nil (`FetchMessage` then `CommitMessages`, no auto-commit).
- Each message is handled in one unit of work: the inbox claim on the CloudEvents
  `id` (`processed_events`) and the effect commit or roll back together, so
  redelivery is a no-op.
- A transient failure (database error, for example) is retried with capped
  exponential backoff (200 ms doubling to 5 s) up to 5 attempts, then the message
  is dead-lettered and the partition moves on.
- A payload that can never be applied (undecodable `data`, an empty required
  field, a value the use case refuses) is dead-lettered at once without retry.
  A message that is not a valid CloudEvent, or whose `type` the consumer does not
  handle, is skipped and committed past.
- A lost or stalled broker connection (idle read timeout, reset, EOF) is retried
  with the same backoff for up to 10 consecutive attempts; beyond that, or on a
  hard error such as connection refused, the consumer returns and the **process
  exits** so the orchestrator restarts it.
- If the dead-letter publish itself fails it is retried forever and the message
  is never dropped, so a dead broker blocks that partition.

### Dead letters

A dead-lettered message lands on `<source topic>.dlq` with its original key,
value and headers plus `x-dlq-source-topic`, `x-dlq-source-partition`,
`x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at` (RFC 3339 UTC). The
dead-letter writer uses RequireAll acks and a hash balancer on the key.

To inspect one:

```sh
kafka-console-consumer.sh --bootstrap-server localhost:9092 \
  --topic retailnetwork.pricing.events.dlq --from-beginning \
  --property print.headers=true
```

To re-drive after fixing the cause (a producer bug, or a missing deploy of this
service), publish the message value back to the source topic. The inbox claim of
a dead-lettered message was rolled back, so reprocessing applies it exactly
once. There is no replay tool in the repo.

## Outbox relay

Domain events are written to `outbox_events` in the same transaction as the cart
(`AddLine`, `QuoteDelivery`, the abandon sweep, the checkout consumer). The relay
claims up to 100 unpublished rows per pass in `id` order, sends them one by one
to `retailnetwork.shopping-cart.analytics` (key = cart id, RequireAll acks), marks a
row published only after the broker acknowledged it, and sleeps
`OUTBOX_RELAY_INTERVAL` after a pass that did not fill a batch. Delivery is
at-least-once; the CloudEvents `id` is minted once at encode time and persisted,
so a retry republishes the same id.

A row that fails `OUTBOX_MAX_ATTEMPTS` times (default 20) is excluded from
future claims so one poison row cannot block the rest. The relay logs it once at
ERROR with the message `outbox relay: row reached the attempt cap and is excluded
from future claims; re-arm it with attempts = 0 after fixing the cause`, with
`outbox_id`, `topic`, `event_type`, `key`, `attempts` and `error`. Look at the
backlog and re-arm:

```sql
SELECT id, event_type, subject, attempts, last_error, created_at
  FROM outbox_events WHERE published_at IS NULL ORDER BY id LIMIT 50;
UPDATE outbox_events SET attempts = 0 WHERE id = <id>;
```

The only consumers of the analytics topic are analytics services, so a stalled
relay never blocks a shopper.

## Sweepers

Two clock-driven policies run inside `cmd/api`, each ticking immediately at boot
and then every interval, draining their backlog in batches of 200:

| Policy | Interval | Effect |
| --- | --- | --- |
| `AbandonInactive` | `ABANDON_SWEEP_INTERVAL` (1h) | An `OPEN` cart idle for 30 days becomes `ABANDONED` and `CartAbandoned` is published |
| `PurgeAbandonedCarts` | `PURGE_SWEEP_INTERVAL` (6h) | A cart that is no longer `OPEN` and has been idle for 30 days is deleted; no event |

A cart that changed or closed since the scan is skipped, so a second sweep or a
second replica is harmless. A failing tick logs `scheduled sweep failed; trying
again at the next tick` and is retried at the next interval; a sweep that did
work logs `scheduled sweep done` with `handled`.

## Postgres

The service connects as role `shopping-cart` (least privilege, its own schema).
The pool and the migrations are separate DSNs (see Migrations above). The carts
hold no PII and no card data: a shopper is an opaque reference and the
destination is a postal prefix.

## Routine procedures

- **Rotate the database credential**: update the `shopping-cart-db` Secret (keys
  `DATABASE_URL`, `MIGRATIONS_DATABASE_URL`) and `kubectl rollout restart`; the
  boot retry absorbs a short window of refused connections.
- **Change a consumer group**: avoid it. The ids must stay stable so a
  rescheduled pod resumes from its committed offsets; a new id is a new group
  with its own offsets and re-reads the topic from wherever the reader starts.
  Replays are harmless because of the inbox claim, but they are load.
- **Check consumer lag**: describe the three groups with the broker's tooling
  (`kafka-consumer-groups.sh --describe --group shopping-cart-pricing`).
- **Rebuild a read model**: the local copies (`rm_items`, `rm_current_schedule`,
  `rm_current_tax_table`) are fed only by Kafka; an empty copy means the
  producers have not published to this broker, or the groups started after their
  retention. See [Troubleshooting](/docs/operations/troubleshooting).
- **Scale**: raise `replicaCount` or enable `autoscaling.api`; the version-guarded
  writes answer `409` `concurrent-update` on a lost race and the client retries.

## Dashboards and alerts

warehouse-infra (`origin/develop`) ships the Grafana dashboard
`warehouse-shopping-cart` (`terraform/dashboards/contexts/shopping-cart.json`) and
the alert groups `warehouse-shopping-cart` and `warehouse-shopping-cart-slo`.
They are listed on [Observability](/docs/operations/observability).
