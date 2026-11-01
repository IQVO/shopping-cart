---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: "Symptom, cause, check and fix for Shopping Cart: boot refusals, readiness, empty read models, REST problem types, consumers and dead letters, the outbox relay, sweepers and the A2A agent."
---

# Troubleshooting

Each entry is symptom, then cause, then how to check, then the fix. Causes come
from the code paths named in the entry; log messages are quoted exactly as the
code writes them (see [Observability](/docs/operations/observability) for the
fields). Procedures such as re-driving a dead letter or re-arming an outbox row
are in the [Runbook](/docs/operations/runbook).

## Boot and readiness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `cmd/api` exits 1 at once with `service exited with error` and `DATABASE_URL is required`, `KAFKA_BROKERS is required`, `DELIVERY_PROMISE_URL is required`, or `<NAME> is required: the consumer always runs on real Kafka` | A mandatory variable is empty. `cmd/api` reports the first missing one in the order database, brokers, delivery-promise URL, then the three consumer groups (`cmd/api/config.go`) | the error text names the variable | Set it; the full table is on [Configuration](/docs/operations/configuration). In Helm the chart refuses to render and names the missing value |
| `DELIVERY_PROMISE_TIMEOUT: "..." is not a positive Go duration` or `CART_CURRENCY: "..." is not a three-letter upper-case currency code` at boot | Malformed value | the error quotes it | Use `5s` style durations and a code such as `USD` (upper case) |
| `cmd/a2a` exits 1 with `A2A_AGENT_URL is required` or `A2A_AGENT_URL=... is not an absolute http(s) URL` | The Agent Card must advertise the URL clients dial, so a relative or schemeless URL is refused (ADR 0006) | `cmd/a2a` prints every problem at once | Set `A2A_AGENT_URL` to an absolute URL; in Helm set `a2a.agentUrl` when the agent must be reachable outside the cluster |
| Repeated `retrying` WARN lines with `op` = `run migrations` or `ping postgres`, then `service exited with error` | Postgres is unreachable or the first dial was reset (sidecar not ready). Both steps retry with bounded backoff (`internal/bootretry`) and then give up | the `err` field of the last `retrying` line | Fix connectivity or the DSN. The startup probe allows about 60 s |
| Migration hangs and the pod never becomes live | `MIGRATIONS_DATABASE_URL` falls back to `DATABASE_URL`, which goes through a transaction-mode pooler; the migration takes a session-scoped advisory lock | no `postgres adapters configured` line | Populate `MIGRATIONS_DATABASE_URL` with a direct, non-pooled DSN |
| Pod is live but `/readyz` answers 503 with `dependency-unavailable` and detail `not ready: kafka` (or `postgres`, or both) | The broker did not answer a topic-less metadata request, or Postgres did not answer a ping, within 2 s (verdict cached 3 s) | WARN `readiness: dependency is not ready` with `dependency` and `error` | Restore the dependency. The pod is deliberately not ready but alive, so Kong stops routing to it; no restart is needed. Check `KAFKA_BROKERS` for typos |
| `/readyz` answers 503 `{"status":"not_ready"}` | Shutdown has started: the drain gate flipped first | `shutdown: readiness flipped to not-ready` in the log | Expected during a rollout. If it never recovers the pod is terminating |
| Pod restarts in a loop; last log line is `<worker> stopped unexpectedly: exiting so the orchestrator restarts the service` | The relay, a consumer or a sweeper returned on its own. For a consumer this is a hard broker error (connection refused, protocol or group error) or more than 10 consecutive lost-connection retries | `kubectl logs --previous`; the ERROR before it (`consumer stopped`, `outbox relay stopped`, `scheduler stopped`) carries the cause | Fix the broker or network; the restart is by design. The `WarehousePodRestarting` alert fires at two restarts in 15 minutes |
| Shutdown is cut short, `worker did not stop before the shutdown drain deadline` | A worker needed more than 10 s to stop | the WARN names the `worker` | Keep `terminationGracePeriodSeconds` above the drain delay plus 75 s (the chart default is 80) |

## Carts and the read models

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Every `POST /carts/{cartId}/lines` answers `422` `item-not-purchasable` | The local copy of the catalogue (`rm_items`) does not mark the item as catalogued, not discontinued **and** priced. It is fed only by Kafka (`CatalogItemCreated`, `ItemPriceChanged`); it is empty on a fresh database, and an item discontinued upstream or whose price was withdrawn is refused | `SELECT * FROM rm_items WHERE catalog_item_id = '<id>'`; the `cart.lines.added` counter with `reason=item-not-purchasable` | Make sure offer-availability and pricing publish to **this** broker and that the pricing and offer-availability consumers are running (`consumer running` lines, no lag). A producer that published before this service's groups first started is only read if the topic retained the messages |
| Add-line works for some items and is refused for others | The row exists with `catalogued = false` (a price event arrived before `CatalogItemCreated`, so the row is a stub) or `priced = false` | the same query | Wait for the missing event, or ask the producer to republish |
| The estimate shows `estimateComplete: false` or zero charges | `rm_current_schedule` or `rm_current_tax_table` is empty or an item has no price; the estimate is labelled non-binding and incomplete rather than guessed | `SELECT version FROM rm_current_schedule`; same for `rm_current_tax_table` | Same as above: the pricing events `DeliveryChargeScheduleChanged` and `TaxTableChanged` must arrive. `CART_CURRENCY` is only the fallback currency before any price states one |
| A retry after a failed call keeps returning the same error even though the cause is fixed | Idempotency: the first response below 500 for a `(route, key)` is stored for 24 hours and **replayed** (header `Idempotent-Replayed: true`), including a 4xx. A 5xx is never stored | the response header; `SELECT status, created_at FROM idempotency_keys WHERE key = '<key>'` | Use a new `Idempotency-Key` for a new attempt after a 4xx; reuse the same key only to retry an unanswered or 5xx call |
| `POST` answers `400` `idempotency-key-required` | The `Idempotency-Key` header is missing or longer than 128 characters (every `POST` route requires it) | request headers | Send a unique key of 1 to 128 characters per logical request |
| `POST` answers `422` `idempotency-key-reused` | The same key was sent with a different method, path, query or body | compare with the first request | Use a fresh key for a different request |
| `POST` answers `409` `concurrent-update` | Another writer changed the cart between its load and save (version guard). The command is already retried once internally | repeats under load on one cart | Retry the call; the client holds a stale view |
| `POST :quote-delivery` answers `503` `delivery-promise-unavailable` | delivery-promise timed out (`DELIVERY_PROMISE_TIMEOUT`, default 5 s), refused the connection, answered 5xx or an undecodable body, **or** answered 4xx (a rejected request is reported the same way and is a contract bug) | `DELIVERY_PROMISE_URL`; reach `POST /delivery-offers:quote` on it by hand; a 4xx detail is inside the error chain, not in the response | Fix the URL or the dependency, then retry with the same `Idempotency-Key`; a 5xx was not stored. Nothing else in the service needs delivery-promise |
| Delivery dates are one day off, or WARN `delivery window time zone unusable; delivery dates fall back to UTC` | The offer window named no `timeZone`, `Local`, or a name the zone database does not know, so dates use UTC instead of the destination's calendar day (ADR 0007). The image embeds tzdata (`cmd/api/tzdata.go`) | the `timeZone` and `reason` fields; logged once per zone name | Fix delivery-promise to send an IANA zone for the serving station |
| `422` `no-destination` on `:quote-delivery` | The cart has no destination postal prefix yet | `GET /carts/{cartId}`, `destinationPostalPrefix` is null | Call `:set-destination` first |
| `409` `stale-delivery-selection` on `:select-delivery` | The offer id is not in the cart's **current** quote (the destination or lines changed and the quote was cleared, or a requote replaced it) | the `quote` of the cart | Quote again and select from the new offers |
| `409` `cart-closed` | The cart is `CHECKED_OUT`, `ABANDONED` or `MERGED`; those states never change | `state` of the cart | Create or use another cart |
| `422` `merge-target` on `:merge-into` | The target is the cart itself, is not `OPEN`, belongs to no shopper (a guest cart), or the source has a shopper different from the target's | both carts' `state` and `shopperId` | Merge a guest cart or a cart of the same shopper into an open cart that has a shopper |
| `422` `quantity-out-of-range` or `too-many-lines` | Quantity must be 1 to 99; a cart holds at most 100 lines | the request body | Correct the request |
| `400` `malformed-request` | Bad JSON, an unknown field, a wrong type, a body over the size limit, or a NUL character (U+0000) in the query string or a JSON body (a NUL in the URL path is `404` `resource-not-found`) | the `detail` | Fix the request. NUL is a uniform client error (`internal/adapters/inbound/http/nul.go`), never a 500 |
| `404` `not-found` for a cart that existed | A cart that is no longer `OPEN` and idle for 30 days is deleted by `PurgeAbandonedCarts`. A path identifier over 128 characters also answers 404 | `GET /carts?shopperId=...` | Expected after retention |
| `500` `internal-error` | An unmapped error. The response never shows the cause | the ERROR `http request` line with the same `trace_id`, and the log lines of that span | Read the log; the usual cause is a database error |

### REST problem types

Every error is `application/problem+json` with `type` `/problems/<slug>`
(`internal/adapters/inbound/http/errors.go`).

| Slug | Status | Raised when |
| --- | --- | --- |
| `malformed-request` | 400 | body or parameter the adapter rejects |
| `idempotency-key-required` | 400 | missing or over-long `Idempotency-Key` |
| `not-found` | 404 | unknown cart, or a line that is not in the cart |
| `resource-not-found` | 404 | a NUL character in the request path |
| `cart-closed` | 409 | the cart is no longer `OPEN` |
| `stale-delivery-selection` | 409 | the selection names an offer outside the current quote |
| `illegal-transition` | 409 | the state machine does not allow the change |
| `concurrent-update` | 409 | version guard lost the race twice |
| `item-not-purchasable` | 422 | the item is not catalogued, discontinued or unpriced |
| `quantity-out-of-range` | 422 | quantity outside 1 to 99 |
| `too-many-lines` | 422 | more than 100 lines |
| `no-destination` | 422 | quote requested before a destination |
| `merge-target` | 422 | invalid merge target |
| `validation-failed` | 422 | a value the domain refuses |
| `idempotency-key-reused` | 422 | the same key with a different request |
| `delivery-promise-unavailable` | 503 | delivery-promise failed or rejected the quote |
| `dependency-unavailable` | 503 | `/readyz`: Postgres or Kafka unreachable |
| `internal-error` | 500 | anything else |

## Consumers and dead letters

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `consumer running` but no `event processed` even at `LOG_LEVEL=debug`, and `rm_items` stays empty | The topic does not exist yet or nothing is published to it on this broker. The service never creates topics for consuming | `kafka-topics.sh --list`; consumer-group lag with `kafka-consumer-groups.sh --describe --group <group>` | Make the producing context publish to the fleet broker (`localhost:9092` locally); no restart is needed once the topic exists |
| WARN `skipping a message that is not a valid CloudEvent` | A message on a consumed topic is not CloudEvents 1.0 structured JSON | the `topic` and `error` fields | Fix the producer. The offset is committed past it |
| Events arrive but nothing changes | The `type` is not one the consumer handles, so it is ignored silently. Dispatch is on the **full** type string (for example `com.retailnetwork.storefront.pricing.itemprice.ItemPriceChanged`) | compare the type with [Events](/docs/api-reference/events) | Fix the producer's type string |
| ERROR `... message can never be applied; dead-lettering it without retry`, WARN `dead-lettering an event that can never be applied` | The payload is undecodable, a required field (`catalogItemId`, `currency`, `cartId`, `sessionId`) is empty or over 200 characters, a schedule has zero or more than 16 tiers, a tax table zero or more than 1000 bands, or the value fails domain validation | the `error` field; the headers of the message on `<topic>.dlq` (`x-dlq-error`) | Fix the producer, then re-drive (Runbook). The message is on the DLQ and the partition has moved on |
| ERROR `... handling failed; retrying the same message` with the same `partition` and `offset` | A transient failure (usually Postgres) repeats; five attempts with 200 ms doubling to 5 s, then it is dead-lettered | the `error` field | Fix the database; the dead-lettered message can be re-driven |
| ERROR `... dead-letter publish failed; retrying the same message` repeating | The DLQ write fails (broker down). It retries forever and never drops the message, so this partition is blocked | broker health | Restore the broker; the consumer resumes by itself |
| WARN `... broker connection stalled or lost; fetching again after the backoff` | An idle connection timed out or was reset; the reader redials. Up to 10 in a row are tolerated | `attempt` and `backoff` | Nothing, unless it reaches 10 and the pod restarts |
| Duplicate-looking effects after a replay | Redelivery is a no-op through the inbox (`processed_events` on the CloudEvents `id`) | `SELECT * FROM processed_events WHERE event_id = '<id>'` | None. A producer that mints a new `id` per retry defeats it |
| A price or schedule did not change although an event arrived | Price, schedule and tax events are applied only when their `version` is above the stored one (version guard), so stale or reordered events are acknowledged and ignored | `rm_items.price_version`, `rm_current_schedule.version` | Producers must send increasing versions |
| WARN `CheckoutCompleted names an unknown cart` / `for a cart that is not open` | checkout reported a cart that was purged, never existed here, or is already closed. It is acknowledged and nothing changes | the `cartId` and `state` | Usually none; check the checkout side if frequent |

## Outbox relay

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Analytics events are not appearing on `retailnetwork.shopping-cart.analytics`; ERROR `outbox relay pass failed` | The broker is unreachable or rejects the write. The row stays unpublished and its `attempts` and `last_error` grow | `SELECT id, event_type, attempts, last_error FROM outbox_events WHERE published_at IS NULL ORDER BY id LIMIT 20` | Restore the broker; the relay drains in `id` order by itself. No shopper request is affected |
| ERROR `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | One row failed `OUTBOX_MAX_ATTEMPTS` times (default 20) so it cannot block the rest | the `outbox_id` and `error` fields | Fix the cause, then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>` |
| Backlog grows slowly with no errors | `OUTBOX_RELAY_INTERVAL` is large, or the rate exceeds 100 rows per pass | `outbox relay running` shows the `interval` | Lower the interval; a full batch is followed by another pass at once |
| A consumer of the analytics topic sees the same event twice | Delivery is at-least-once; the CloudEvents `id` is stable across retries | the `id` | Consumers must dedupe on `id` |

## Sweepers

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Idle open carts never become `ABANDONED` | The cart is idle for less than 30 days, or the tick did not run or failed | `scheduled sweep done` / `scheduled sweep failed; trying again at the next tick` with `job` `AbandonInactive` and `handled`; `ABANDON_SWEEP_INTERVAL` | Wait for the next tick (default 1 h) or fix the database error shown |
| Closed carts pile up | `PurgeAbandonedCarts` deletes a non-open cart idle for 30 days; it ticks every `PURGE_SWEEP_INTERVAL` (default 6 h) | the same lines with `job` `PurgeAbandonedCarts` | Wait, or fix the error |
| Warning `invalid ABANDON_SWEEP_INTERVAL; using the default` | The value is not a positive Go duration | the `value` field | Use `30m` style values |

## A2A agent

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| The Agent Card advertises an unreachable URL | `A2A_AGENT_URL` is what the card says; the default in-cluster Service DNS is wrong off-cluster | `curl .../.well-known/agent-card.json` | Set `a2a.agentUrl` (or `A2A_AGENT_URL`) to the gateway URL |
| A skill call answers a problem inside a normal reply | Skill errors are answered in-band, not as JSON-RPC errors: `malformed`, unknown skill, `cart not found`, validation. The request must be one data part (or a text part holding the same JSON) with `skill` and `arguments`, and unknown fields are refused | the `problem` of the reply envelope | Send `{"skill":"get_cart","arguments":{"cartId":"..."}}`; see [API reference](/docs/api-reference) |
| The agent answers but REST shows a different currency | `CART_CURRENCY` differs between the two binaries | both environments | Set the same value on both |
| The agent fails to start while REST works | It reads only Postgres; a missing `A2A_AGENT_URL` or `DATABASE_URL` is the usual cause | the boot error | Set both |

## Telemetry

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| No traces or metrics in Grafana | The Collector is down or `OTEL_EXPORTER_OTLP_ENDPOINT` points elsewhere. Export never blocks, so the service stays healthy and drops telemetry silently | `LOG_LEVEL=debug` shows `opentelemetry error`; the `telemetry configured` line shows the endpoint | Fix the endpoint or the Collector |
| Log lines have no `trace_id` | Only `*Context` slog calls inside a span carry it; boot, shutdown and background lines never do | the line's origin | Expected |
