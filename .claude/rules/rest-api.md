---
paths:
  - "internal/adapters/inbound/http/**"
  - "apis/openapi*.yaml"
  - "apis/openapi/**"
---

# REST API (inbound adapter)

Every route -> use case mapping, kept in sync with `apis/openapi.yaml` (routes live
in `internal/adapters/inbound/http/server.go`). This file is documentation for an
agent, not generated: keep it manually accurate.

- `POST /carts` -> `CreateCart`
- `GET /carts?shopperId=&state=` -> `ListCarts`
- `GET /carts/{cartId}` -> `GetCart` (cart + estimate + checkoutReadiness)
- `POST /carts/{cartId}/lines` -> `AddLine` (merges an existing item's quantity)
- `PUT /carts/{cartId}/lines/{catalogItemId}` -> `SetLineQuantity`
- `DELETE /carts/{cartId}/lines/{catalogItemId}` -> `RemoveLine`
- `PUT /carts/{cartId}/destination` -> `SetDestination`
- `POST /carts/{cartId}/delivery-quotes` -> `QuoteDelivery` (calls delivery-promise)
- `PUT /carts/{cartId}/delivery-selection` -> `SelectDelivery`
- `POST /carts/{cartId}/merges` -> `MergeInto`
- `POST /carts/{cartId}/delivery-requote-acceptances` -> `AcceptRequotedOffers` (never calls delivery-promise)
- `GET /healthz` (liveness) and `GET /readyz` (drain gate + Postgres ping + Kafka metadata)

## Conventions

- Error shape: RFC 9457 `application/problem+json`, `type` = `/problems/<slug>`
  (catalogue in `errors.go`); every POST needs an `Idempotency-Key` (missing = 400
  `idempotency-key-required`, same key with another body = 409
  `idempotency-key-reused`, replay marked `Idempotency-Replayed: true`); a NUL in the
  path, query or body is a 400 before any key is recorded.
- Auth: none. Fleet-wide auth was deliberately reverted 2026-09-11; do not add
  bearer/JWT/API-key middleware (`TestNoAuthMiddlewareReintroduced`).
