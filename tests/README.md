# `tests/`

Test infrastructure for Lenny. The architecture and conventions are defined in [`../TESTING.md`](../TESTING.md). The local install and setup steps are in [`../TESTING_DEPENDENCIES.md`](../TESTING_DEPENDENCIES.md).

## Files in this directory

| File | Purpose |
|:-----|:--------|
| `spec-map.json` | Maps every spec leaf section to its tests, packages, schemas, migrations, and chart templates. See TESTING.md §5. |
| `change-graph.json` | Maps source packages, schemas, migrations, and chart templates to the tests that exercise them. Drives `lenny-test --changed`. |
| `groups.yaml` | Named test selection groups (`pr`, `pr-fast`, `nightly`, `pre-release`, `phase-<N>-gate`). |
| `groups.subsets.yaml` | Concrete subset definitions referenced by `groups.yaml`. |
| `spec-map-exceptions.yaml` | Spec sections explicitly exempt from the "every section has at least one test" rule, with justifications. |
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
- `tier7_load/` — performance and SLO scenarios
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

# Bring up the cached container daemon
lenny-test infra up --profile containers

# Status
lenny-test infra status
```
