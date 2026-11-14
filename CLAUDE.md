# CLAUDE.md — shopping-cart

`shopping-cart` is the **Shopping Cart** bounded context (Supporting subdomain,
`storefront` tier) of the retail-network storefront domain (GitHub org `IQVO`,
Go module `github.com/IQVO/shopping-cart`). It holds a shopper's intent: cart
lines, the destination postal prefix, the delivery quote delivery-promise
returned for the cart (per shipment group, with offers and expiry), the tier
selection per group, and a labelled, non-binding estimate (effective prices +
selected tier charges + tax band). It owns the `Cart` aggregate and ships two
binaries: `cmd/api` (REST, the outbox relay, three consumers and the
AbandonInactive / PurgeAbandonedCarts policies in one process) and `cmd/a2a`
(read-only Agent2Agent agent, ADR 0006).

## Hard rules (each one is fitness-tested or decided in an ADR)

1. **Hexagonal dependency rule.** `internal/domain` imports nothing outside the
   domain; `internal/application` only domain and application; inbound and
   outbound adapters never import each other; only `cmd/` wires layers
   (`TestHexagonalArchitecture`, `make arch-test`).
2. **CloudEvents 1.0 is MANDATORY** on every Kafka message produced or consumed
   (structured mode, no flat envelope, no dual-write, no envelope toggle:
   `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope`). Build and
   decode only through `internal/adapters/kafka/cloudevents/`. Catalogue:
   ADR 0004, payloads: `apis/asyncapi.yaml`
   (`TestEventCatalogueMatchesContract`).
3. **All four published events are analytics-only** (`CartLineAdded`,
   `CartDeliveryQuoted`, `CartAbandoned`, `CartCheckedOut` on
   `retailnetwork.shopping-cart.analytics`; `.events` is unused). They leave
   through the transactional outbox only: the cart row and its encoded events
   commit in ONE unit of work and the relay publishes; every relay passes an
   attempt cap (`OUTBOX_MAX_ATTEMPTS`, `TestOutboxRelaysPassAttemptCap`).
4. **Consumed facts become local read models** (ADR 0003):
   `PurchasabilityView` and `EstimateView` are fed by offer-availability and
   pricing events through the inbox (dedupe on CloudEvents `id`); checkout's
   completion closes the cart (CloseOnCheckoutCompleted). A poison message goes
   to `<topic>.dlq`, a cancelled context never does
   (`TestDeadLetterCallsGuardCancellation`). Group ids come from the mandatory
   `*_CONSUMER_GROUP` variables (`TestKafkaConsumerGroupNeverHardcodedInline`).
5. **One synchronous call**: `QuoteDelivery` calls delivery-promise's
   `POST /delivery-quotes` through the `QuoteOffers` port OUTSIDE the
   transaction, then stores the quote; `AcceptRequotedOffers` never calls it.
   The storefront calls only Open Host Service routes, never an in-org or
   restricted route (ADR 0001). The pinned copy of the producer's spec must
   equal its `develop` (`contracts/pinned.json`, CI job `pinned-contracts`):
   refresh it by copying, never by editing.
6. **Cart invariants** live in `internal/domain/cart`: quantity 1..99, at most
   100 lines, re-adding an item merges, any line or destination change clears
   the quote and selections, a selection must name an offer of the current
   quote, a closed cart never changes. Delivery dates are days of the
   destination's local calendar (ADR 0007).
7. **Every POST is behind the `Idempotency-Key` middleware** (missing = 400
   `idempotency-key-required`, same key with another body = 409
   `idempotency-key-reused`, replay marked `Idempotency-Replayed: true`); PUT and
   DELETE are idempotent by method. Errors are RFC 9457
   `application/problem+json`. REST guideline: ADR 0008.
8. **No auth layer on REST or A2A** (fleet-wide revert 2026-09-11): never add
   bearer/JWT/API-key middleware (`TestNoAuthMiddlewareReintroduced`). The
   context stores no PII and no card data.
9. **The A2A agent is read-only**: an allowlist of read use cases and ports, no
   write-side or sibling-calling import (`TestA2ASurfaceIsReadOnly`).
10. **Real infrastructure only**: `cmd/*` never imports the in-memory adapters
    (`TestRuntimeBinaryHasNoInMemoryAdapters`); Kafka and Postgres integration
    tests use testcontainers. No Go file at the repo root
    (`TestNoGoFilesAtRepoRoot`).
11. **An ADR comes first** for a new consumed or published contract, a new
    synchronous call or any auth. ADRs live in `docs/adr/`.

## Commands

```bash
make check-fast   # fmt-check + vet + arch-test + tests of changed packages: run before saying "done"
make check        # fmt-check vet build lint test
make check-all    # check + coverage (90% gate on domain+application) + arch-test + bdd
make bdd          # godog scenarios in features/
make integration  # real Postgres/Kafka via testcontainers (needs Docker)
make mutation     # gremlins on each internal/domain package (CI job mutation-fast, blocking)
make guide-lint   # these guides: skills load, references resolve, context budget
make harness-test # the harness scripts' own tests (hook, pinned-contract gate)
```

Run locally: `DATABASE_URL=... KAFKA_BROKERS=... DELIVERY_PROMISE_URL=...
OFFER_AVAILABILITY_CONSUMER_GROUP=... PRICING_CONSUMER_GROUP=...
CHECKOUT_CONSUMER_GROUP=... go run ./cmd/api` (REST `:8080`, migrations at
boot, a missing variable is a boot error); `cmd/a2a` serves the read-only agent.

## Where things are written down (read before editing the matching area)

- `.claude/rules/domain-model.md` (always loaded): ubiquitous language, the
  aggregate, events, use cases. `.claude/rules/rest-api.md`: every route and
  the error conventions. `.claude/rules/integration-events.md`: published and
  consumed events, the outbox, the type table.
- `.claude/rules/fleet/*.md`: fleet-wide rules copied from the harness
  template (CloudEvents, A2A, idempotency and outbox, Kafka testing, no auth);
  never edit them here.
- `docs/adr/`: decisions (index: `docs/adr/index.md`). `apis/openapi.yaml`,
  `apis/asyncapi.yaml`: contracts. Operating docs: `docs/docs/operations/`
  (configuration, runbook, observability, troubleshooting),
  `docs/docs/development/testing.md`, `docs/docs/overview/quickstart.md`.
  `HARNESS.md`: what each sensor runs and why. `INCIDENTS.md`: the fleet
  incident log behind the fitness tests.

harness-template: v4
(hooks, guide-lint).

<!-- harness:scoped-rules:start (generated by tools/migrate_v3.py in warehouse-harness-template; do not hand-edit) -->
## Scoped rules and harness

Claude Code loads each rule below automatically when you touch the matching paths. OpenCode and Codex do NOT: read the rule BEFORE editing matching files.

| When touching | Read |
|---|---|
| `internal/adapters/**/kafka/**`, `internal/adapters/outbound/outbox/**`, `apis/asyncapi*` | `.claude/rules/integration-events.md` |
| `internal/adapters/inbound/http/**`, `apis/openapi*.yaml`, `apis/openapi/**` | `.claude/rules/rest-api.md` |

Hooks (`scripts/harness/hook.py`, wired for Claude Code, Codex and OpenCode) block pushes to develop/main, `--no-verify`, bare `rm -rf`, and edits to generated files, and feed gofmt/vet findings back after each edit. Before saying "done" run `make check-fast`; the full gate is `make check-all`. `HARNESS_OFF=1` disables the hooks when debugging the harness itself.
<!-- harness:scoped-rules:end -->
