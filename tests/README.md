# `tests/`

Test infrastructure for Lenny. The architecture and conventions are defined in [`../TESTING.md`](../TESTING.md). The developer-facing onramp is at [`../docs/testing/`](../docs/testing/index.md). Local install and setup is in [`../TESTING_DEPENDENCIES.md`](../TESTING_DEPENDENCIES.md).

## Files in this directory

| File | Purpose |
|:-----|:--------|
| `spec-map.json` | Maps every spec leaf section to its tests, packages, schemas, migrations, and chart templates. See TESTING.md §5. |
| `change-graph.json` | Maps source packages, schemas, migrations, and chart templates to the tests that exercise them. `validate-maps` checks it; no selector reads it. |
| `non-go-inputs.yaml` | Maps the inputs no Go package owns, such as the chart, the compose files, and the supervisor's C sources, to the tests that read them. `lenny-test --changed` reads it beside the import graph and the specification edits. See TESTING.md §6, "Selection algorithm". |
| `groups.yaml` | Named test selection groups (`pr`, `pr-fast`, `nightly`, `pre-release`, `phase-<N>-gate`). |
| `groups.subsets.yaml` | Concrete subset definitions referenced by `groups.yaml`. |
| `spec-map-exceptions.yaml` | Spec sections explicitly exempt from the "every section has at least one test" rule, with justifications. |
| `results/` | Latest verdict (`latest.json`, `latest.junit.xml`). Gitignored. |

## Subdirectories

Each tier has its own directory and build tag (see TESTING.md §4 for the canonical layout). Phase 0 ships the skeleton; subsequent phases populate each tier.

- `testinfra/` — shared infrastructure (containers, Kind, fixtures, mocks, assertions, verdict producer, time and randomness control)
- `static/` — the static tier's Go test packages: `build/` (build and generated-artifact drift), `chart/` (the chart render and the deployment boundary), `lint/` (code invariants and test hygiene), and `structure/` (the specification's structure, the migration gates, and the maps)
- `component/` — single component plus real backing services
- `contract/` — wire-format equivalence across surfaces
- `integration/` — multi-component flows via compose
- `e2e_kind/` — full deployment on Kind
- `e2e_cloud/` — full deployment on GKE, EKS, AKS
- `load_local/` — in-process load and concurrency scenarios
- `load_kind/` — performance and SLO scenarios on Kind
- `load_cloud/` — cloud-scale load scenarios
- `chaos/` — failure injection
- `security/` — security controls and adversarial scenarios
- `conformance/` — runtime adapter conformance
- `docs/` — documentation tests

Unit tests are co-located with the code they cover under `pkg/`. This directory is the index, not the location.

## Quick commands

```bash
# Developer inner loop (changed-only, max-tier component)
lenny-test --group pr-fast

# Full PR gate
lenny-test --group pr

# Validate the spec map and change graph
lenny-test validate-maps

# Validate that every component-and-up test has a diagnosis comment
lenny-test validate-diagnosis

# Discovery
lenny-test list --tier unit       # enumerate test functions
lenny-test list --spec POOL-R5,§4.2  # tests whose annotations cite them
lenny-test list --pkg pkg/quota   # tests covering a package
lenny-test list --changed         # tests for the current diff

# Cached container daemon (compose profile)
lenny-test infra up --profile compose
lenny-test infra status

# Watch
lenny-test watch --tier unit
lenny-test watch --changed

# Stress (50 consecutive runs)
lenny-test stress --test TestPodClaim --runs 50
lenny-test stress --pattern 'TestSession.*' --runs 25

# Coverage
lenny-test coverage --go         # Go coverage from tests/results/cover.out
lenny-test coverage --spec       # coverage view from the // spec: annotations

# Baselines
lenny-test baseline diff --before old.json --after new.json --threshold 0.15

# Aggregate verdicts
lenny-test report --dir tests/results --output markdown

# PR comment
lenny-test comment --verdict tests/results/latest.json
```

The full list is in `lenny-test --help`.

## Test data

`testdata/` ships canonical fixtures:

- `migrations/` — Phase 1.5 fixture migrations
- `anthropic/`, `openai_chat/`, `openai_responses/` — translator
  golden corpora (request + response pairs, including streaming)
- `uploads/` — multipart and archive fixtures for §13.4 validators

## Verdict outputs

- `results/latest.json` — most recent verdict (overwritten every run)
- `results/verdict-<run_id>.json` — rotated history (20 most recent retained)
- `results/cover.out` — Go coverage profile from `runUnitTier`
