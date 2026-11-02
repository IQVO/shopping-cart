---
id: quickstart
title: Quickstart
sidebar_label: Quickstart
sidebar_position: 3
description: "Build, test and run Shopping Cart locally on Postgres and Kafka and make the first REST and A2A calls."
---

# Quickstart

Build the service, learn what it refuses to boot without, run it on Postgres
and Kafka and make the first calls.

:::note What was and was not run
This page was written from the code on the branch (`cmd/api`, `cmd/a2a`, the
HTTP adapter, the migration and the `Makefile`). No binary was started while
writing it, so the response bodies below show the shape the code produces
(`internal/adapters/inbound/http/dto.go`), not a captured session; ids and
timestamps are placeholders.
:::

## Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| Go | `1.27.3` (`go.mod`) | everything |
| `make` | any | the targets below |
| Docker | any recent | the throwaway Postgres below and `make integration` (testcontainers boots its own Postgres and Kafka) |
| golangci-lint | `v2.14.0` (`GOLANGCI_VERSION` in the `Makefile`, pinned in CI) | `make lint`, `make check` |
| gremlins | `v0.6.0` (`GREMLINS_VERSION`) | `make mutation`, `make mutation-full` |
| govulncheck | latest | `make vuln` |
| Node 20 | | building this documentation site (`docs/`) |
| lefthook | optional | `lefthook install` wires `fmt-check` + `vet` + `lint` at pre-commit and `make check` at pre-push |

The fleet runs **one** Kafka broker, reachable at `localhost:9092` through the
kind cluster's external access; the docker-compose Kafka is retired, and this
repo ships no compose file. Point `KAFKA_BROKERS` at that broker. Postgres is
yours to provide (below).

## Build and test

```bash
make help          # list the targets
make build         # go build ./...
make test          # go test ./... -race (unit + httptest + godog, no database needed)
make arch-test     # go test ./internal/architecture/... -v
make bdd           # go test ./... -run TestFeatures -v
make coverage      # tests with -race plus the 90% gate over domain + application
make check         # fmt-check vet build lint test
make check-all     # check + coverage + arch-test + bdd
make check-fast    # fmt-check vet arch-test + tests of the changed packages
make integration   # go test -tags=integration ./... -race -count=1 (needs Docker)
```

## Boot refusals

`cmd/api` validates its environment before it opens a connection or binds a
port. With nothing set:

```bash
go build -o /tmp/shopping-cart-api ./cmd/api
/tmp/shopping-cart-api
```

it logs `service exited with error` and exits with status 1. The error names
the first missing mandatory variable in this order: `DATABASE_URL`,
`KAFKA_BROKERS`, `DELIVERY_PROMISE_URL`, then each consumer group
(`OFFER_AVAILABILITY_CONSUMER_GROUP`, `PRICING_CONSUMER_GROUP`,
`CHECKOUT_CONSUMER_GROUP`). A malformed `DELIVERY_PROMISE_TIMEOUT` or
`CART_CURRENCY` is refused the same way. `cmd/a2a` instead reports all of its
problems at once (`DATABASE_URL`, `A2A_AGENT_URL`, `CART_CURRENCY`). The
variable list is on [Configuration](/docs/operations/configuration).

## Run on Postgres and Kafka

Start a throwaway database and the service:

```bash
docker run -d --rm --name cart-pg -e POSTGRES_PASSWORD=pw -e POSTGRES_DB=cart \
  -p 127.0.0.1:55432:5432 postgres:16-alpine

export DATABASE_URL='postgres://postgres:pw@127.0.0.1:55432/cart?sslmode=disable'
export KAFKA_BROKERS=localhost:9092
export DELIVERY_PROMISE_URL=http://localhost:8082
export OFFER_AVAILABILITY_CONSUMER_GROUP=shopping-cart-offer-availability-dev
export PRICING_CONSUMER_GROUP=shopping-cart-pricing-dev
export CHECKOUT_CONSUMER_GROUP=shopping-cart-checkout-dev
go run ./cmd/api      # REST on :8080
```

Notes:

- Migrations run at boot (`internal/adapters/outbound/postgres/migrations`,
  one migration `0001_shopping_cart_schema`).
- The service never creates topics. The producing topic is
  `retailnetwork.shopping-cart.analytics`; the consumed topics
  (`retailnetwork.offer-availability.events`, `retailnetwork.pricing.events`,
  `retailnetwork.checkout.events`) exist once their producers published. See the
  [Runbook](/docs/operations/runbook#kafka).
- `DELIVERY_PROMISE_URL` is only called by `quote-delivery`. Everything else
  works with nothing listening there.
- Startup log lines (JSON on stdout), in order: `telemetry configured`,
  `postgres adapters configured`, one `consumer running` per consumer
  (`consumer`, `topic`, `group_id`, `brokers`), `outbox relay running`, one
  `scheduler running` per policy and `http server listening`.
- `GET /readyz` is 200 only while Postgres answers a ping and a Kafka broker
  answers a metadata request. With the broker unreachable the process stays up
  and live and `/readyz` answers 503.
- Stop with Ctrl-C: `/readyz` flips to 503, the process waits
  `SHUTDOWN_DRAIN_DELAY`, drains HTTP, stops the consumers and policies, then
  the relay, and closes the pool.

## First calls

REST is unauthenticated. Every `POST` needs an `Idempotency-Key` header
(without it the answer is `400` `idempotency-key-required`).

```bash
curl -s localhost:8080/healthz     # {"status":"ok"}
curl -s localhost:8080/readyz      # ready once Postgres and Kafka answer

curl -s -X POST localhost:8080/carts \
  -H 'Idempotency-Key: demo-1' -H 'Content-Type: application/json' \
  -d '{"shopperId":"shopper-1"}'
```

`201 Created` with the cart view: `cartId`, `shopperId`, `state` `OPEN`, empty
`lines`, `destinationPostalPrefix` null, `estimate`
(`currency`, `itemsMinor`, `deliveryMinor`, `taxMinor`, `totalMinor`,
`estimateComplete`), `checkoutReadiness` and `version`. Then add a line:

```bash
CART=<cartId from the answer>
curl -si -X POST localhost:8080/carts/$CART/lines \
  -H 'Idempotency-Key: demo-2' -H 'Content-Type: application/json' \
  -d '{"catalogItemId":"ITEM-1","quantity":2}'
```

On a fresh database this is `422` `item-not-purchasable`: the cart only lets an
item in when its local copy says it is catalogued, not discontinued and priced
(`readmodel.Item.Purchasable`), and that copy is fed by Kafka
(`CatalogItemCreated` plus `ItemPriceChanged`). In a live cluster those events
arrive by themselves. For a quick local look you can insert the copy by hand
(columns as in the migration; a development shortcut, not a supported
interface):

```bash
docker exec cart-pg psql -U postgres -d cart -c "INSERT INTO rm_items(catalog_item_id, catalogued, priced, price_minor, currency, price_version, updated_at) VALUES ('ITEM-1', true, true, 1999, 'USD', 1, now())"
```

Repeat the add-line call with a new key and it returns `200` with the line.
Continue the journey:

```bash
curl -s -X POST localhost:8080/carts/$CART:set-destination \
  -H 'Idempotency-Key: demo-3' -H 'Content-Type: application/json' \
  -d '{"destinationPostalPrefix":"941"}'
curl -s -X POST localhost:8080/carts/$CART:quote-delivery -H 'Idempotency-Key: demo-4'
curl -s localhost:8080/carts/$CART
curl -s "localhost:8080/carts?shopperId=shopper-1&state=OPEN"
```

`quote-delivery` calls delivery-promise; without one running it answers `503`
`delivery-promise-unavailable` (retry with the same key). Select an offer with
`:select-delivery` (`{"groupNo":1,"offerId":"..."}`) using an `offerId` from
the quote. Error documents are RFC 9457 `application/problem+json` with `type`
`/problems/<slug>`; the slugs are listed on
[API reference](/docs/api-reference). Retrying a `POST` with the same key and
body replays the stored answer; the same key with another body is `422`
`idempotency-key-reused`.

After `AddLine` the outbox holds the analytics event; the relay publishes it
within `OUTBOX_RELAY_INTERVAL`:

```bash
docker exec cart-pg psql -U postgres -d cart -At \
  -c "select id,topic,event_type,subject,attempts,published_at is not null from outbox_events"
```

## A2A agent

`cmd/a2a` serves the same two reads over A2A. It needs only Postgres:

```bash
A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a     # :8093
curl -s localhost:8093/.well-known/agent-card.json
curl -s localhost:8093/healthz
```

The JSON-RPC call shape is on [API reference](/docs/api-reference#a2a).

## Building these docs

```bash
cd docs
npm ci
npm run build       # also fails on broken links
npm run gen-api-docs  # regenerate docs/api-reference/rest from apis/openapi.yaml
```
