# tier7a_load_local

Local, in-process load and concurrency tests. Tier 7a is the first stage of the two-stage tier-7 gate (TESTING.md §12.7.a). The companion tier-7b (`tests/tier7b_load_kind/`) runs the same family of scenarios end-to-end on Kind; tier-12 (`tests/tier12_load_cloud/`) runs the cloud-scale envelope.

## Scope

Tier-7a binds together:

- **Mode A — per-component benches.** Go benchmark-style hot-path loops against the package's public surface with `-race` enabled. Located under `pkg/<package>/bench_load_test.go`. Discovered by the `lenny-test` harness via build tag `load_local`.
- **Mode B — multi-component in-process harness.** A single-binary Lenny boot via `tests/testinfra/inproc`, with `miniredis` for the slot counter and idempotency store, an embedded Postgres adapter for `sessionstore`/`auditstore`, and `tests/testinfra/fakekube` for the Kubernetes API surface. Scenarios live under `scenarios/<name>/scenario.go`.

The pure-Go load driver lives in `tests/testinfra/loadgen`. k6 is not used at this tier; the tier-7b and tier-12 surfaces continue to use k6.

## Build tag and invocation

```bash
lenny-test --tier load_local
go test -tags load_local -race -count=1 -timeout 10m ./tests/tier7a_load_local/...
```

Wall-clock budget: total tier ≤ 5 minutes on a developer laptop. Per-scenario budget: ≤ 15 seconds.

## Layout

| Path | Role |
|:--|:--|
| `scaffolds_test.go` | Go test wrapper that resolves the `Scenario` registry, configures the load profile, and drives each scenario through `tests/testinfra/loadgen`. |
| `scenarios/<name>/scenario.go` | One file per scenario, implementing the `loadgen.Scenario` interface. Run with the race detector. |

## Cross-references

- TESTING.md §12.7.a — tier 7a definition
- TESTING.md §12.7.b — companion Kind suite
- TESTING.md §12.12 — cloud-scale suite
- `tests/testinfra/loadgen/` — pure-Go driver
- `tests/testinfra/inproc/` — in-process Lenny bootstrap
- `tests/testinfra/fakekube/` — fake Kubernetes API surface
