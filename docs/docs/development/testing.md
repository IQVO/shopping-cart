---
id: testing
title: Testing
sidebar_label: Testing
description: "The Shopping Cart test pyramid as it is: unit, godog features, integration with testcontainers, repository and external contract tests, architecture fitness, mutation, chart tests, the make targets and the CI jobs."
---

# Testing

The pyramid below is what is on `develop`. Go is `1.27.3` (`go.mod`); the pinned
tools are golangci-lint `v2.14.0` and gremlins `v0.6.0` (`Makefile`). Nothing
needs a database for `make check`; only `make integration` needs Docker.

## Layers

| Layer | Where | What it proves | Needs |
| --- | --- | --- | --- |
| Domain unit tests | `internal/domain/cart/*_test.go` (lines, delivery, selection, lifecycle, restore), `internal/domain/estimate`, `internal/domain/shared` | the aggregate invariants: quantity 1 to 99, at most 100 lines, merge rules, quote and selection rules, the 30-day idle rule, the estimate maths | nothing |
| Application tests | `internal/application/usecases/*_test.go` (commands, queries, delivery, consume, policies, `cart_metrics_test.go`) | use cases over the in-memory test doubles: one unit of work per command, events raised and appended to the outbox, inbox idempotency, the business counter | nothing |
| Adapter unit tests | `internal/adapters/**/**_test.go` | HTTP routes, problem documents and idempotency with `httptest` (`internal/adapters/inbound/http`), the consumer run loop, retries, dead letters and tracing (`inbound/kafka`), the scheduler, the A2A skills, Agent Card and REST parity (`inbound/a2a`), the relay (`outbound/outbox`), the CloudEvents encoder against golden files (`outbound/kafka/testdata/*.golden.json`), the delivery-promise client (`outbound/deliverypromise`), telemetry and log correlation, the boot retry | nothing |
| Composition-root tests | `cmd/api/main_test.go`, `cmd/api/telemetry_test.go`, `cmd/a2a/*_test.go` | boot refusals, environment parsing, shutdown order, the router, one request yielding a server span and a log line that carry the same `trace_id` | nothing |
| Godog features | `features/*.feature` (5 files, 38 `Scenario` and `Scenario Outline` headings), steps in `internal/bdd` | the business behaviour through the real HTTP router and use cases, wired to the in-memory doubles (`TestFeatures`, strict mode) | nothing |
| Integration | 8 files tagged `//go:build integration`: `cmd/api/e2e_integration_test.go`, `cmd/api/e2e_flow_integration_test.go`, `cmd/a2a/postgres_integration_test.go`, `internal/adapters/outbound/postgres/{main,migrate,repositories,outbox_trace}_integration_test.go`, `internal/adapters/outbound/outbox/main_integration_test.go` | the real `run` entry points on a real Postgres and Kafka, the embedded migration, the repositories, the outbox with trace headers, the relay | Docker; every test boots its own containers through `internal/testing/pgtest` and `internal/testing/kafkatest` |
| Contract | `internal/adapters/outbound/repocontract` (one suite run against the in-memory store and against Postgres), `deliverypromise/spec_test.go` and `testdata/delivery-promise.openapi.yaml` (the consumed delivery-promise route), `inbound/a2a/parity_test.go` (A2A answers carry exactly the properties of the `apis/openapi.yaml` schemas), the encoder golden files, `internal/architecture/catalogue_fitness_test.go` (every type in `apis/asyncapi.yaml` is in the CloudEvents ADR) | the ports behave the same on every adapter and the wire shapes do not drift | nothing, except the Postgres run of the repository contract (integration) |
| Architecture fitness | `internal/architecture/*_test.go` (13 files) | see below | nothing |
| Mutation | `.gremlins.yaml`, `make mutation`, `make mutation-full` | the domain tests assert, not just execute. Thresholds: efficacy 85, mutant coverage 90, one worker, timeout coefficient 30 | `gremlins` |
| Chart tests | `charts/shopping-cart/tests/render_test.sh` (needs `helm`), `test_service_selectors.py` (needs PyYAML) | the chart refuses to render without the mandatory values, renders every variable the binaries read, hardens the pod, sizes the grace period against the shutdown bounds, keeps the A2A agent read-only and off by default, every Service selects one Deployment | `helm`, Python 3 |

### Architecture fitness tests

All are in `internal/architecture` and run with `make arch-test`:

| Test | Rule |
| --- | --- |
| `TestHexagonalArchitecture` | six rules: `internal/domain` imports only itself; `internal/application` only domain and application; inbound adapters never import outbound ones and the reverse; nothing under `internal` imports `cmd`; the `ports` package holds interfaces only |
| `TestRuntimeBinaryHasNoInMemoryAdapters` (and `...SensorBites`) | `cmd/**` never imports the in-memory adapter: the runtime is Postgres and Kafka only |
| `TestA2AAdapterDependencyRule`, `TestA2ASurfaceIsReadOnly` (and `TestA2ASensorBites`) | the A2A adapter depends only on application and domain, calls no other context and cannot write |
| `TestNoAuthMiddlewareReintroduced` | no auth middleware exists in the REST adapter (fleet decision) |
| `TestKafkaConsumerGroupNeverHardcodedInline` | consumer group ids come from the environment, never a literal |
| `TestCloudEventsOnly`, `TestNoEventEnvelopeToggleOrFlatEnvelope` | every Kafka message is a CloudEvents 1.0 envelope, with no flat-envelope toggle |
| `TestKafkaWritersSetRequiredAcks`, `TestKafkaWriterTopicNotSetTwice`, `TestDeadLetterCallsGuardCancellation`, `TestReplayConsumersSetCommitInterval` | writer and consumer settings that would silently lose or duplicate messages |
| `TestOutboxRelaysPassAttemptCap` | the relay is built with an attempt cap so a poison row cannot block it |
| `TestEventCatalogueMatchesContract` | every type `apis/asyncapi.yaml` declares is listed in the CloudEvents ADR |
| `TestPostgresIntegrationTestsUseTestcontainers`, `TestKafkaIntegrationTestsUseTestcontainers` | integration tests boot their own containers, with no skip gate |
| `TestNoGoFilesAtRepoRoot` | no Go file at the repository root |
| `TestMCPAdapterDependencyRule`, `TestMCPOptionalToolInputsCarryOmitempty` | template sensors about an MCP adapter; this repository has no `cmd/mcp` |

Each sensor that scans source has a companion test (`...SensorBites`,
`...FailsOnBadFixtures`) proving it fails on a bad fixture.

## Make targets

| Target | Runs | Notes |
| --- | --- | --- |
| `make help` | lists the targets | default |
| `make build` | `go build ./...` | |
| `make vet` | `go vet ./...` | |
| `make fmt`, `make fmt-check` | `gofmt -w .` / fail if `gofmt -l .` is non-empty | |
| `make lint` | `golangci-lint run --allow-parallel-runners ./...` | pinned `v2.14.0`; config `.golangci.yml` includes complexity limits |
| `make test` | `go test ./... -race` | unit, `httptest` and godog; no database |
| `make coverage` | tests with `-race` and `-coverpkg=./internal/domain/...,./internal/application/...`, then the 90% gate | the only definition of the coverage scope |
| `make bdd` | `go test ./... -run TestFeatures -v` | godog features |
| `make arch-test` | `go test ./internal/architecture/... -v` | fitness tests |
| `make integration` | `go test -tags=integration ./... -race -count=1` | Docker; not in `check` |
| `make mutation` | gremlins over each `./internal/domain/*/` package in turn | the fast, blocking gate |
| `make mutation-full` | gremlins over `./internal/domain` with `--workers 1 --timeout-coefficient 30` | the scheduled, exhaustive run |
| `make vuln` | `govulncheck ./...` | |
| `make check` | `fmt-check vet build lint test` | the pre-push bundle (lefthook) |
| `make check-all` | `check coverage arch-test bdd` | run before opening a PR |
| `make check-fast` | `fmt-check vet arch-test` plus the tests of the packages changed against `HEAD` | the agent stop hook |
| `make guide-lint`, `make harness-test` | lints the agent guides; tests the agent hooks | |

Chart tests, run from the repository root:

```bash
bash charts/shopping-cart/tests/render_test.sh
python3 charts/shopping-cart/tests/test_service_selectors.py
```

Documentation site, from `docs/`: `npm ci`, `npx tsc --noEmit -p .`,
`npm run build` (fails on a broken link or an MDX error).

## CI jobs

`.github/workflows/ci.yml` runs on pushes and pull requests to `main` and
`develop`, weekly on Monday 06:00 UTC for the mutation job, and on demand.

| Job | What it runs | Blocking |
| --- | --- | --- |
| `lint` | golangci-lint `v2.14.0` | yes |
| `guide-lint` | `scripts/harness/guide_lint.py`, `test_hook.py` | advisory (`continue-on-error`) |
| `complexity` | golangci-lint with only `gocyclo`, `gocognit`, `cyclop`, `funlen`, `nestif`, plus an informational gocyclo report | yes |
| `test` | `go build`, `go vet`, then `make coverage` (90% gate), uploads `coverage.out` | yes |
| `bdd` | `go test ./... -run TestFeatures -v` | yes |
| `integration` | `go test -tags=integration ./... -race -count=1` | yes |
| `mutation-fast` | gremlins on every `internal/domain/*/` package | yes |
| `mutation` | gremlins on `./internal/domain`; schedule or manual only; opens or closes a `harness:red` issue on a scheduled failure | schedule |
| `api-lint` | Spectral on `apis/openapi.yaml` and `apis/asyncapi.yaml` with `--fail-severity=warn` | yes |
| `vuln` | `govulncheck ./...` | yes |
| `helm-lint` | `ct lint` and `helm lint` with both `ci/*-values.yaml`; pull requests only | yes |
| `chart-tests` | every `charts/shopping-cart/tests/*.sh` and `test_*.py`; a printed `SKIP` fails the job | yes |
| `arch-test` | `go test ./internal/architecture/... -v` | yes |
| `docker-publish` | builds and pushes `ghcr.io/iqvo/shopping-cart`, signs with cosign, attaches an SBOM; push to `main` after the gates above | main only |
| `release` | tags `vX.Y.Z`, retags the image, packages and pushes the Helm chart to `oci://ghcr.io/iqvo`, creates the GitHub release | main only |

Other workflows: `docs.yml` builds this site on every pull request touching
`docs/**` or `apis/**` and deploys to GitHub Pages from `main`; `codeql.yml` runs
code scanning; `ai-review.yml` runs the AI review.

## Notes

- `.gremlins.yaml` and the `Makefile` header mention a `drift` job and a
  scaffold script; `ci.yml` has no `drift` job on this branch.
- The godog features and the application tests use `internal/adapters/outbound/memory`.
  It is test-only: `TestRuntimeBinaryHasNoInMemoryAdapters` keeps it out of
  `cmd/**`.
- A new consumed or published event type needs, in the same change: the
  `apis/asyncapi.yaml` entry, the ADR 0004 catalogue entry
  (`TestEventCatalogueMatchesContract`), and for a published type a golden file
  in `internal/adapters/outbound/kafka/testdata`.
