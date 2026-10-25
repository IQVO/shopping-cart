---
name: how-to-add-a-rest-endpoint
description: Add or change a REST endpoint in this service in the hexagonal order (domain invariant, use case, handler, apis/openapi.yaml, godog scenario). Use when touching internal/adapters/inbound/http, apis/openapi.yaml, or exposing a use case over HTTP.
---

# How to add a REST endpoint

Follow this order: domain first, adapter last. A handler written before the
invariant it relies on validates nothing and gets bypassed.

The worked example is `POST /carts/{cartId}:select-delivery`:
`internal/domain/cart/delivery.go` `SelectDelivery` ->
`internal/application/usecases/cart_commands.go` `SelectDelivery` ->
`internal/adapters/inbound/http/handlers.go` `handleSelectDelivery`. The read side
example is `GET /carts/{cartId}` (`usecases/queries.go`
`GetCart` -> `handleGet`).

## 1. Domain first

Does the rule already exist in `internal/domain/<cart|estimate|shared>/`? A
handler holds no business logic. A new rule goes into the aggregate with a
table-driven test in the same package (`commands_test.go`, `create_test.go`, ...)
and a named `Err...` variable (`cart/cart.go`).
Pure Go: no I/O, `now` and ids passed in. Then re-run the mutation gate for that
package (`gremlins unleash ./internal/domain/<pkg>`).

## 2. Application: the use case

Add it to `internal/application/usecases/` (`cart_commands.go`, `quote_delivery.go` for
commands; `queries.go` for reads). A command embeds `Deps` (`deps.go`):
`transact` runs load, domain call, `Save` and `publish` (outbox append) in ONE
`ports.UnitOfWork` and retries on `ports.ErrConcurrentUpdate`. A query holds only
its read port. Return a view from `internal/application/views`. If a new repository
method is needed, add it to `internal/application/ports/ports.go` (interfaces only),
implement it in `internal/adapters/outbound/postgres/` AND
`internal/adapters/outbound/memory/`, and extend the shared suite in
`internal/adapters/outbound/repocontract/` so both run it. Unit-test the use case in
`internal/application/usecases/*_test.go`.

## 3. Adapter: the handler

In `internal/adapters/inbound/http/`:

- add the request body to `dto.go` (strict decoding, unknown fields are 400);
- add the use-case field to `Server` and the route to `NewRouter` in `server.go`; a
  state-changing route goes through `s.idempotent(routeName)` (a new `route...`
  constant), a read uses `r.Get`;
- write the handler in `handlers.go` (decode, call the use case, encode the view,
  `writeError` for every failure);
- map a new domain `Err` in `errors.go` `problemCatalogue` (kebab-case slug, the
  status the OpenAPI documents) and add it to a route test in `cart_routes_test.go` that asserts its status and slug;
- wire the use case in `cmd/api/main.go` `buildServer`.

## 4. Contract: apis/openapi.yaml

Describe the operation, its `Idempotency-Key` parameter (writes), the success body
and every problem it can return, then lint:
`npx --yes @stoplight/spectral-cli lint apis/openapi.yaml --ruleset .spectral.yaml`
(the `api-lint` CI job runs the same). Keep `.claude/rules/rest-api.md` and the
README in step.

## 5. Behaviour

Add the happy path and each refusal to `features/*.feature` (`cart_lines.feature`,
`delivery.feature`, ...; the steps are in `internal/bdd/http_test.go`
and `steps_assertions_test.go`, refusals are named by problem slug) and a case to
`internal/adapters/inbound/http/cart_routes_test.go`. The real middleware, Postgres
and Kafka are proven by `cmd/api/e2e_flow_integration_test.go`.

## 6. Verify before opening the PR

`make check`, `make coverage`,
`go test -tags=integration ./cmd/... -race -count=1 -timeout 900s`, and the spectral
lint above.
