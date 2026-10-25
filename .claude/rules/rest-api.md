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
- `POST /carts/{cartId}/lines/{catalogItemId}:set-quantity` -> `SetLineQuantity`
- `POST /carts/{cartId}/lines/{catalogItemId}:remove` -> `RemoveLine`
- `POST /carts/{cartId}:set-destination` -> `SetDestination`
- `POST /carts/{cartId}:quote-delivery` -> `QuoteDelivery` (calls delivery-promise)
- `POST /carts/{cartId}:select-delivery` -> `SelectDelivery`
- `POST /carts/{cartId}:merge-into` -> `MergeInto`
- `POST /carts/{cartId}/delivery/requoted` -> `AcceptRequotedOffers` (never calls delivery-promise)
- `GET /healthz` (liveness) and `GET /readyz` (drain gate + Postgres ping + Kafka metadata)

## Conventions

- Error shape: RFC 9457 `application/problem+json`, `type` = `/problems/<slug>`
  (catalogue in `errors.go`); every POST needs an `Idempotency-Key` (missing = 400
  `idempotency-key-required`, same key with another body = 422
  `idempotency-key-reused`, replay marked `Idempotent-Replayed: true`); a NUL in the
  path, query or body is a 400 before any key is recorded.
- Auth: none. Fleet-wide auth was deliberately reverted 2026-09-11; do not add
  bearer/JWT/API-key middleware (`TestNoAuthMiddlewareReintroduced`).
