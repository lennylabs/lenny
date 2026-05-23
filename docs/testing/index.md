---
layout: default
title: "Testing"
nav_order: 8
has_children: true
description: How Lenny's test infrastructure works. Tier model, the lenny-test harness, how to write a new test, and where each tier lives.
---

# Testing

Lenny's test surface follows a strict tier model. Tier 0 (static) runs first; each higher tier gates on the one below. The full design lives in [TESTING.md](../../TESTING.md); this section is the developer-facing onramp.

## The tier model at a glance

| Tier | What runs | Wall clock |
|:----:|:----------|:-----------|
| 0 | go vet, gofumpt, goimports, golangci-lint, schema/query/migration linters, ADR catalog, markdown link check, validate-diagnosis, validate-maps, JSON-schema breaking-change check | < 60s |
| 1 | `go test ./...` with `-race -count=1`; helm-unittest cases when the chart is present | < 60s |
| 2 | Component tests against real backing stores (Postgres/Redis/MinIO via testcontainers-go); each suite covers one Lenny subsystem | < 5min |
| 3 | Contract tests: REST/MCP consistency, OpenAI translator fidelity, adapter JSONL, OAuth token exchange, SDK contract harness | < 4min |
| 4 | Integration tests through the compose stack (gateway + stores + stubs) | < 10min |
| 5 | End-to-end on a real Kind cluster with the Helm chart installed | < 30min |
| 6 | End-to-end on GKE / EKS / AKS via Terraform | < 2h |
| 7 | Load: k6 scenarios against the cluster with SLO baselines | per-phase |
| 8 | Chaos: fault injection via toxiproxy or chaos-mesh | nightly subset |
| 9 | Security: tenant isolation, TLS enforcement, admission policies, OWASP ZAP fuzz, kube-bench, cosign + SBOM | per release |
| 10 | Conformance: runtime adapter validation against the published contracts (`lenny-compliance`) | per release |
| 11 | Documentation: link-check, code-block parse, runbook structure, ADR continuity | < 60s |

## Writing your first test

1. Pick the tier. Most new tests are tier-1 unit tests under `pkg/<area>/*_test.go`. The rule of thumb: if the test needs another process running, it isn't tier-1.
2. Follow the §17 conventions (see [conventions](conventions.html)). Every test from tier-2 onward needs a `// spec:` and `// diagnosis:` annotation above the function — see [Conventions / §17.2](conventions.html#annotations) for the exact format.
3. Run locally:
   ```bash
   go build -o bin/lenny-test ./cmd/lenny-test
   ./bin/lenny-test --tier unit
   ```
4. Verify your test reaches the verdict's tier breakdown:
   ```bash
   ./bin/lenny-test --tier unit --output json | jq '.tiers.unit'
   ```

## Common workflows

### Run the fast PR feedback loop

`pr-fast` resolves through `--changed` capped at the component tier. The harness inspects `git diff`, maps each changed path through `tests/change-graph.json`, and emits the smallest tier plan that covers the diff.

```bash
./bin/lenny-test --group pr-fast
```

### Update a baseline

Load-tier baselines under `tests/tier7b_load_kind/baselines/<scenario>.json` are pinned. When intentional change happens (a new scenario, an SLO loosening), regenerate:

```bash
LENNY_UPDATE_BASELINE=1 ./bin/lenny-test --tier load
# Or with the CLI flag:
./bin/lenny-test --tier load --update-baseline
```

### Update a golden file

Golden comparisons in `tests/testinfra/golden` use the same opt-in via `GOLDEN_UPDATE=1` or `--update-golden`.

### Watch for changes

Re-run a selector on every file change under `pkg/`, `cmd/`, `tests/`, `schemas/`, `scripts/`:

```bash
./bin/lenny-test watch --tier unit
./bin/lenny-test watch --changed
```

### Stress-test a flaky test

Per §17.10 every test must pass 50 consecutive runs. Investigate via:

```bash
./bin/lenny-test stress --test TestSandboxClaimSkipLocked --runs 50
./bin/lenny-test stress --pattern 'TestSession.*' --runs 25
```

### Aggregate verdicts across a CI matrix run

```bash
./bin/lenny-test report --dir tests/results --output markdown
```

## Where each tier lives

| Tier | Directory | Build tag |
|:----:|:----------|:----------|
| 0 | `tests/tier0_static/` | _(none)_ |
| 1 | `pkg/<area>/*_test.go` and `tests/tier1_unit/` | _(none)_ |
| 2 | `tests/tier2_component/<subsystem>/` | `component` |
| 3 | `tests/tier3_contract/<surface>/` | `contract` |
| 4 | `tests/tier4_integration/` | `integration` |
| 5 | `tests/tier5_e2e_kind/` | `e2e_kind` |
| 6 | `tests/tier6_e2e_cloud/` | `e2e_cloud` |
| 7 | `tests/tier7b_load_kind/scenarios/<name>/` | `load` |
| 8 | `tests/tier8_chaos/` | `chaos` |
| 9 | `tests/tier9_security/` | `security` |
| 10 | `tests/tier10_conformance/` | `conformance` |
| 11 | `tests/tier11_docs/` | _(none)_ |

## Test infrastructure helpers

The `tests/testinfra/` packages are shared building blocks:

- `assertions` — typed assertions (Equal, ErrorIs, StringContains, JSONEqual)
- `wait` — condition-wait with explicit timeout (the only sleep helper)
- `ports` — fresh OS-assigned ports
- `goleak` — goroutine-leak detection
- `fixtures` — reference-data identifiers (acme, alice, …)
- `fixtures/generators` — rapid-driven WorkspacePlan / TaskRecord / OutputPart generators
- `fixtures/seed` — Postgres + Redis seed statements for the compose stack
- `golden` — golden-file comparison with `--update-golden`
- `stubs/oidc` — in-process OIDC stub
- `stubs/kms` — AES-GCM KMS stub with fault toggles
- `stubs/llmprovider` — recording HTTP server for Anthropic / OpenAI shapes
- `stubs/siem` — SIEM batch receiver with HMAC verification
- `containers` — testcontainers-go Postgres helper
- `compose` — wraps `docker compose -f compose/default.yml`
- `kind` — Kind cluster lifecycle + skip helpers
- `envtest` — sigs.k8s.io controller-runtime envtest wrapper
- `chaos` — toxiproxy + chaos-mesh dispatch
- `security/{zap,cosign,sbom,kubebench,pentest}` — tier-9 wrappers
- `cloud` — per-provider cluster bring-up
- `load` — k6 wrapper + baseline comparator
- `randctl` + `timectl` — deterministic random + clock helpers
- `mocks/otelcollector` — in-process OTLP collector recorder

## Further reading

- [Test conventions](conventions.html) — naming, annotations, table-driven form, cleanup, parallelism, skipping
- [Determinism](determinism.html) — `timectl`, `randctl`, `wait`, `goleak`, `ports`
- [Domain suites](domain-suites.html) — RLS, workspace plan, credentials, delegation, MCP elicitation, operability, multi-protocol, interceptor chain, pool lifecycle, compliance/erasure, T4 controls, web playground, SDKs
- [Testing `lenny-ctl`](lenny-ctl.html) — operability tests across the 14 command categories
- [Flakiness](flakiness.html) — §17.10 stress sweep, quarantine workflow, root-cause categories
- [Documentation tests](documentation-tests.html) — tier-11 markdown / code-block / runbook / ADR checks
- [Forward compatibility](forward-compatibility.html) — §23 v2 surfaces
- [TESTING.md](../../TESTING.md) — the authoritative design
- [TESTING_DEPENDENCIES.md](../../TESTING_DEPENDENCIES.md) — local tool setup
