# ADR 0008: Adopt the fleet REST API guideline

## Status

Accepted (2026-11-01). Replaces the per-route `Idempotency-Key` store, the 422 answer for key reuse and every
`:verb` route of the cart contract.

## Context

The 2026-10-30 application portfolio assessment measured the fleet's REST contracts: write operations only
partly keyed, two spellings of the replay header, key reuse answered 422 in most services and 409 in one,
three command-URL styles (`:verb`, verb sub-paths, sub-resources) and lists without paging. shopping-cart
showed four of those:

- seven command routes in the `:verb` or verb-segment style (`:set-quantity`, `:remove`, `:set-destination`,
  `:quote-delivery`, `:select-delivery`, `:merge-into`, `/delivery/requoted`), and a consumed route in the same
  style (`POST /delivery-offers:quote` on delivery-promise);
- the replay header was spelled `Idempotent-Replayed` and a reused key was answered 422;
- keys were scoped per route and stored by a get-then-put that ran after, and outside the unit of work of, the
  cart change: two concurrent first requests could both run, and a crash between the change and the put left
  a cart change without its key row;
- `GET /carts` answered a bare `{carts: [...]}` with no paging.

The fleet standard (warehouse-docs, "REST API guideline", rules R1-R8) removes each of those. This ADR adopts
it here, as a hard cutover: no alias route, no dual header, no toggle.

## Decision

1. **Noun routes (R1, R2).** Status codes and response bodies do not change with a rename; `operationId`s stay,
   so the A2A and MCP mappings do not move.

   | Old | New |
   |---|---|
   | `POST /carts/{cartId}/lines/{catalogItemId}:set-quantity` | `PUT /carts/{cartId}/lines/{catalogItemId}` |
   | `POST /carts/{cartId}/lines/{catalogItemId}:remove` | `DELETE /carts/{cartId}/lines/{catalogItemId}` |
   | `POST /carts/{cartId}:set-destination` | `PUT /carts/{cartId}/destination` |
   | `POST /carts/{cartId}:quote-delivery` | `POST /carts/{cartId}/delivery-quotes` |
   | `POST /carts/{cartId}:select-delivery` | `PUT /carts/{cartId}/delivery-selection` |
   | `POST /carts/{cartId}:merge-into` | `POST /carts/{cartId}/merges` |
   | `POST /carts/{cartId}/delivery/requoted` | `POST /carts/{cartId}/delivery-requote-acceptances` |

   `TestRetiredRoutesAreGone` asserts every old route answers 404 or 405.
2. **Idempotency (R3-R6, R8).** Every `POST` (create, add line, delivery-quotes, merges,
   delivery-requote-acceptances) sits behind `RequireIdempotencyKey(store, clock)`, applied per route. `PUT` and
   `DELETE` are idempotent by method; a key sent to them is accepted and ignored. The key is 1 to 128 printable
   ASCII characters: absent is 400 `idempotency-key-required`, malformed is 400 `idempotency-key-invalid`. The
   fingerprint is method + path + SHA-256 of the body: the same key and fingerprint replays the stored answer
   (status, body, `Content-Type`) with `Idempotency-Replayed: true`, and only a replay carries the header; the
   same key with another fingerprint is 409 `idempotency-key-reused`, never 422.
3. **A global, durable key store joined to the unit of work (R4, STANDARD 2.5).** The key is global, no longer
   per route (the `route` column of `idempotency_keys` holds the empty string; no migration). The port is
   `ports.IdempotencyStore.Do(ctx, key, requestHash, at, fn)`: Postgres claims the key row inside the
   transaction it opens, hands `fn` that transaction through the context (every unit of work joins it instead
   of opening its own), and commits the claim, the cart change, the outbox rows and the stored answer
   together. Concurrent requests with one key are serialised by the unique index and the loser replays the
   winner's answer. A 2xx or 4xx answer is stored for 24 hours; a 5xx or a panic rolls everything back and is
   never stored, so the retry re-executes. The in-memory store implements the same contract and is run by the
   same repository contract suite.
4. **Keyset paging (R7).** `GET /carts` takes `limit` (1 to 500, default 100) and `cursor`, orders by
   `(created_at, cart_id)` descending in both the Postgres and the in-memory repository, uses the fleet `page`
   package (`internal/adapters/inbound/http/page`) and answers `{items, nextCursor}`; a bad value is 400
   `invalid-limit` or `invalid-cursor`. The A2A `list_shopper_carts` skill keeps its own tool schema but
   follows every page, 100 carts at a time, up to 1000 carts (the newest), so it still returns complete data.
5. **Contract and enforcement.** `apis/openapi.yaml` declares the `Idempotency-Key` parameter (required: the
   middleware is active in every runtime mode), 400 and 409 and the `Idempotency-Replayed` header on every 2xx
   of every `POST`, and the `CartPage` schema. The `rest-guideline` Spectral rule (`.spectral.yaml`,
   `.spectral/functions/restGuideline.js`) runs in the `api-lint` job with `--fail-severity=warn`.
6. **Consumers changed in this repo.** The delivery-promise gateway
   (`internal/adapters/outbound/deliverypromise`) now calls `POST /delivery-quotes` and always sends the
   `Idempotency-Key` of the cart request (ADR 0002 A10), and its contract-test copy of the provider spec is the
   renamed one. There is no web remote and no seed script here. Callers of this service (storefront-bff, via
   its allow-list) update in their own pull requests.

### Exemptions

None. `GET /carts` is the only collection and it is paged. `GET /carts/{cartId}`, `/healthz` and `/readyz`
are not collections.

## Consequences

- Breaking for every caller: new URLs, the list envelope, a key required on every `POST`. This pull request
  must deploy together with the rest of the REST cutover set (storefront-bff, delivery-promise and the other
  services); `develop` is half-migrated until the last of them lands.
- A client that reused one `Idempotency-Key` across two different routes used to get two answers; it now gets
  409 `idempotency-key-reused`. A UUID per logical submit avoids it.
- The key row, the cart change and the outbox rows can no longer diverge: a crash rolls all three back.
- Rows the per-route store wrote (under their route name) are not read by the global lookup and expire in 24
  hours.
- Keyset paging cannot skip or repeat a cart that shares a creation instant with its neighbour; the walk is
  covered by the repository contract suite (memory and Postgres) and by the HTTP tests.
