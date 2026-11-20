# ADR 0011: Every problem type has a replay class; a retryable refusal is not stored

## Status

Accepted (2026-11-20). Narrows the sentence "A 2xx or 4xx answer is stored for 24 hours" of
[ADR 0008](./0008-adopt-the-fleet-rest-api-guideline.md); the fleet decision is the owner's of 2026-11-18
(warehouse-harness-template `docs/idempotency-replay-classes.md` and
`.claude/rules/fleet/idempotency-and-outbox.md`).

## Context

`RequireIdempotencyKey` (`internal/adapters/inbound/http/idempotency.go`) stored every answer below 500 under
the key for 24 hours, a 4xx too, and replayed it with `Idempotency-Replayed: true`. That is right for a refusal
that is final for the request (a quantity out of range) and wrong for one a retry could pass: a `409
concurrent-update` that was a lost race, a `422 item-not-purchasable` for a fact an offer-availability event
delivers a moment later, a `422 no-destination` another request (or the shopper) can clear. Each was pinned for
24 hours under a key the client had every right to retry.

The store (`internal/adapters/outbound/postgres/idempotency.go`) claims the key row INSIDE the transaction of
the request (`claimSQL`), runs the handler behind `SAVEPOINT handler_run` and stores the answer in the same
commit, so releasing a key is the rollback a 5xx already takes: the claim and every write of the request
disappear together. The in-memory double implements the same contract and both run the same repository
contract suite.

## Decision

Every problem type of the catalogue has exactly one replay class, declared on its catalogue entry
(`problemCatalogue` in `internal/adapters/inbound/http/errors.go`, the field `replay`):

| Class | Meaning | Stored and replayed |
| --- | --- | --- |
| `FINAL` | the answer is a function of the request (or of state that only moves one way) | yes |
| `OUTCOME` | the command committed state and then refused; the writes commit with the stored answer | yes |
| `RETRYABLE` | nothing committed and the answer depends on state that can change | no, the key is released |

- A 2xx is stored. A 5xx and a panic are never stored. The middleware holds no list of slugs and no status
  range beyond those two: `writeError` marks the capturing writer with the class of the problem it answers
  (`markReplay`), the capture turns that into `idempotency.Record.Release`, and the store returns before its
  `UPDATE` so the deferred rollback drops the claim and the writes (`postgres/idempotency.go`; the in-memory
  double does the same).
- A 4xx answer whose problem declares no class (a handler that wrote a bare status) is released like a
  RETRYABLE one: the safe fallback. The catalogue has no default class: `TestEveryProblemTypeDeclaresAReplayClass`
  fails for an entry without one, and `TestTheADRDeclaresTheClassOfEveryProblem` keeps the table below equal
  to the catalogue. `TestHandlersAnswerErrorsOnlyThroughWriteError` keeps `handlers.go` from writing a
  problem by hand.
- Releasing the key means the request is forgotten: the same body under the same key runs again, and a different
  body under the same key is a **new request**, not `409 idempotency-key-reused`.
- A replay still carries `Idempotency-Replayed: true`; the 24-hour window is unchanged.
- **A concurrent duplicate while the first ends RETRYABLE.** The duplicate blocks on the unique index of the key
  row until the first transaction ends (`claimSQL`). The first rolls back and the row never existed, so the
  blocked claim inserts it and runs the handler itself: it never replays the released refusal and never runs
  alongside the first; the copies queued behind it replay its answer. The repository contract case
  `a copy waiting on a released key runs fn itself` (memory and Postgres) and
  `TestACopyWaitingOnARetryableFirstRunsItself` pin it.
- **Released means only that the same key may be tried again by whoever sent it.** No consumer-side or
  client-side retry behaviour is added by this change.
- The MCP server (`cmd/mcp`) and the A2A agent (`cmd/a2a`) are read-only (ADR 0006, ADR 0009): no other
  surface stores answers.
- **There is no OUTCOME entry.** No use case of this service commits state and then refuses: every command's
  writes ride the unit of work the request's transaction carries, and the attempt's error rolls the unit of
  work (its savepoint) back before the handler answers, so a refusal never carries committed writes. The
  mechanism exists unchanged — a 4xx whose record is not released still commits the handler's writes with the
  stored answer — but no problem type uses it; the fleet template's OUTCOME test skips until one does.

## The class of every problem type

`FINAL` rows are stored; the reason of every `RETRYABLE` row says what can change.

| Problem type | Class | Reason |
| --- | --- | --- |
| `malformed-request` | FINAL | decodeJSON rejects the body before any use case runs (`response.go`): the request alone. |
| `invalid-limit` | FINAL | GET /carts only (a list parameter); classified for completeness. |
| `invalid-cursor` | FINAL | GET only. |
| `unknown-query-parameter` | FINAL | GET only. |
| `not-found` | RETRYABLE | Mixed causes share the slug: a cart of the path (ids minted here, but the `PurgeAbandonedCarts` policy deletes abandoned carts), a line not in the cart yet (`cart.ErrLineNotFound`, `lines.go`: another request can add it), and an empty or overlong path id (`handlers.go pathID`). |
| `cart-closed` | FINAL | CHECKED_OUT, ABANDONED and MERGED are terminal (`cart.go`: a closed cart never changes; no command reopens one), so the answer cannot change by retrying. |
| `stale-delivery-selection` | RETRYABLE | The selection must name an offer of the CURRENT quote (`delivery.go`); a re-quote replaces the quote and can bring that offer back. |
| `item-not-purchasable` | RETRYABLE | The PurchasabilityView is a read model fed by offer-availability events (ADR 0003): the fact can arrive after the request. |
| `quantity-out-of-range` | FINAL | The request's own quantity against 1..99 (`lines.go`). |
| `too-many-lines` | RETRYABLE | The 100-line cap counts the cart's CURRENT lines (`lines.go AddLine`, `lifecycle.go mergedLines`): remove a line and the same request passes. |
| `no-destination` | RETRYABLE | The destination is set by another request (PUT /destination; `delivery.go`, and `quote_delivery.go` raises it before the remote call). |
| `merge-target` | RETRYABLE | Mixed: the target that IS the request's own cart (request-only) and the target's state and shopper read from the store (`lifecycle.go MergeInto`). One class per slug; splitting the self-target cause out is a recorded follow-up. |
| `delivery-promise-unavailable` | RETRYABLE | A 5xx is never stored; delivery-promise comes back. `ErrDeliveryPromiseUnavailable` and `ErrQuoteRejected` share the slug. |
| `concurrent-update` | RETRYABLE | The optimistic-lock version guard (`postgres/carts.go Save`: zero affected rows) after the use case's own `commandAttempts` (`deps.go transact`). |
| `illegal-transition` | RETRYABLE | No code path of this service raises it today; unsure, so RETRYABLE. |
| `validation-failed` | FINAL | Every raise is a value-object rule on the request's own fields (`shared.Invalid`: ids, the destination prefix format, the offers of a requote body). |

The problems the adapter writes around the store — `idempotency-key-required` and `idempotency-key-invalid`
(written before the key is claimed), `idempotency-key-reused` (written on the replay path, never captured),
`resource-not-found` (the NUL rejection runs before the Idempotency-Key middleware) and
`dependency-unavailable` (GET /readyz) — are not catalogue entries and never reach the store. An unmapped
error is `internal-error` (500): a 5xx, never stored.

## Open points

- Expired `idempotency_keys` rows are purged by nothing: the 24-hour window is enforced on read only
  (`readSQL`). Retention is a recorded follow-up (the fleet fix shape: a bounded background sweep), not part
  of this change.
- `QuoteDelivery` forwards the caller's own `Idempotency-Key` to delivery-promise's `POST /delivery-quotes`
  (`outbound/deliverypromise/gateway.go`, `usecases/quote_delivery.go`) and reuses it across its internal
  `commandAttempts`: once delivery-promise releases a RETRYABLE refusal under that key, the second internal
  attempt executes upstream instead of replaying the stored refusal. Callers of THIS service that derive a
  key (checkout reads cart state here) may retry a released refusal the same way; caller-side behaviour is
  out of scope and only reported.
