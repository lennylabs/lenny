# `cmd/lenny-test`

The single entry point for running Lenny's test suite. Architecture and conventions live in [`../../TESTING.md`](../../TESTING.md); local install steps live in [`../../TESTING_DEPENDENCIES.md`](../../TESTING_DEPENDENCIES.md).

## Build

```bash
go build -o bin/lenny-test ./cmd/lenny-test
# or, to install onto PATH:
go install ./cmd/lenny-test
```

## Phase 0 status

This is the skeleton. It implements:

- Flag parsing and selector resolution against `tests/groups.yaml`, `tests/spec-map.json`, and `tests/change-graph.json`.
- The `list`, `validate-maps`, `validate-diagnosis`, `infra`, and `version` subcommands.
- A `--dry-run` mode that prints the resolved selector and the tests it would run.
- The default run command, which dispatches the static tier to `go vet ./...` and the unit tier to `go test ./...` over whatever exists in `pkg/`. Higher tiers are recorded as `skipped` with reason `phase-0-not-implemented`.
- Verdict writing to `tests/results/latest.json` per TESTING.md §7.

Phase 1+ extends this with real executors for each tier, the cached container daemon, the JUnit and GitHub-annotation emitters, the PR-comment integration, and full change-graph resolution.

## Quick reference

```bash
lenny-test --help
lenny-test version
lenny-test list
lenny-test list --groups
lenny-test list --tiers
lenny-test list --specs
lenny-test list --json
lenny-test validate-maps
lenny-test validate-diagnosis
lenny-test infra status

# Dry-run a selection (no execution; shows what would run)
lenny-test --group pr --dry-run
lenny-test --tier component --dry-run
lenny-test --changed --dry-run
lenny-test --since main --max-tier unit --dry-run   # branch diff from its merge base
lenny-test --since main --tier unit --dry-run       # the same diff, unit tier only
lenny-test --since A...B --dry-run                   # any explicit range
lenny-test --spec 4.6.1,12.3 --dry-run

# Actually run (Phase 0: static + unit only)
lenny-test --tier static
lenny-test --tier unit
lenny-test --group pr-fast
```

## Per-tier timeouts

Each tier passes a `go test -timeout` budget. `go test` aborts the whole package binary when the budget runs out, so each default sits above the tier's recorded runtime with margin. An environment variable overrides each budget with a Go duration such as `45m`; an unparseable value is logged and the default applies. `timeouts.go` records the basis of each default.

| Tier | Default | Override |
|:-----|:--------|:---------|
| unit (per package) | 30m | `LENNY_TEST_UNIT_TIMEOUT` |
| component | 10m | `LENNY_TEST_COMPONENT_TIMEOUT` |
| integration | 30m | `LENNY_TEST_INTEGRATION_TIMEOUT` |
| e2e_kind | 60m | `LENNY_TEST_E2E_KIND_TIMEOUT` |
| e2e_cloud | 30m | `LENNY_TEST_E2E_CLOUD_TIMEOUT` |
| load_local, load_kind, chaos, load_cloud | 10m | `LENNY_TEST_LONG_TIMEOUT` |
| security | 60m | `LENNY_TEST_SECURITY_TIMEOUT` |
| docs | 1m | `LENNY_TEST_DOCS_TIMEOUT` |

## Files

| File | Purpose |
|:-----|:--------|
| `main.go` | Entry point; dispatches subcommands |
| `cmd_run.go` | Default run command: parses selectors, resolves into tier plan, executes |
| `cmd_validate.go` | `validate-maps` and `validate-diagnosis` subcommands |
| `cmd_list.go` | `list` subcommand |
| `cmd_infra.go` | `infra up/down/status/prune` (Phase 0 stubs) |
| `tiers.go` | Tier ordering and group-to-tier resolution |
| `cmd_run_resolve.go` | `--changed`, `--spec`, and `--pkg` resolution into a tier plan |
| `changed_select.go` | Unit-tier package scope for `--changed`: owning packages, change-graph unit targets, and in-module importers |
| `verdict.go` | JSON verdict writer per TESTING.md §7 |
