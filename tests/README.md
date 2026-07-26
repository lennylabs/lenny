# `tests/`

Test infrastructure for Lenny. The architecture and conventions are defined in [`../TESTING.md`](../TESTING.md). The developer-facing onramp is at [`../docs/testing/`](../docs/testing/index.md). Local install and setup is in [`../TESTING_DEPENDENCIES.md`](../TESTING_DEPENDENCIES.md).

## Files in this directory

| File | Purpose |
|:-----|:--------|
| `spec-map.json` | Maps every spec leaf section to its tests, packages, schemas, migrations, and chart templates. See TESTING.md §5. |
| `change-graph.json` | Maps source packages, schemas, migrations, and chart templates to the tests that exercise them. Drives `lenny-test --changed`. |
| `groups.yaml` | Named test selection groups (`pr`, `pr-fast`, `nightly`, `pre-release`, `phase-<N>-gate`). |
| `groups.subsets.yaml` | Concrete subset definitions referenced by `groups.yaml`. |
| `spec-map-exceptions.yaml` | Spec sections explicitly exempt from the "every section has at least one test" rule, with justifications. |
| `spec-map-inpackage-pending.txt` | In-package test files `validate-maps` tolerates as absent from `spec-map.json`, one repo-relative path per line. Waives the backlog the in-package orphan sweep inherited so the check ratchets on new drift. |
| `results/` | Latest verdict (`latest.json`, `latest.junit.xml`). Gitignored. |

## Subdirectories

Each tier has its own directory and build tag (see TESTING.md §4 for the canonical layout). Phase 0 ships the skeleton; subsequent phases populate each tier.

- `testinfra/` — shared infrastructure (containers, Kind, fixtures, mocks, assertions, verdict producer, time and randomness control)
- `tier0_static/` — lint, schema validation, traceability validation
- `tier2_component/` — single component plus real backing services
- `tier3_contract/` — wire-format equivalence across surfaces
- `tier4_integration/` — multi-component flows via compose
- `tier5_e2e_kind/` — full deployment on Kind
- `tier6_e2e_cloud/` — full deployment on GKE, EKS, AKS
- `tier7b_load_kind/` — performance and SLO scenarios
- `tier8_chaos/` — failure injection
- `tier9_security/` — security controls and adversarial scenarios
- `tier10_conformance/` — runtime adapter conformance
- `tier11_docs/` — documentation tests

Tier 1 unit tests are co-located with the code they cover under `pkg/`. This directory is the index, not the location.

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
lenny-test list --spec 4.2,12.3   # tests for spec sections
lenny-test list --pkg pkg/quota   # tests covering a package
lenny-test list --changed         # tests for the current diff

# Cached container daemon (compose profile)
lenny-test infra up --profile compose
lenny-test infra status

# Watch
lenny-test watch --tier unit
lenny-test watch --changed

# Stress (50 consecutive runs)
lenny-test stress --test TestSandboxClaim --runs 50
lenny-test stress --pattern 'TestSession.*' --runs 25

# Coverage
lenny-test coverage --go         # Go coverage from tests/results/cover.out
lenny-test coverage --spec       # spec-section coverage

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
