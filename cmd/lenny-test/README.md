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

- Flag parsing and selector resolution against `tests/groups.yaml`, `--changed` and `--pkg` resolution through the Go import graph, the specification edits, and `tests/non-go-inputs.yaml`, and `--spec` resolution through the annotation index.
- The `list`, `validate`, `validate-diagnosis`, `infra`, and `version` subcommands.
- A `--dry-run` mode that prints the resolved selector and the tests it would run.
- The default run command, which dispatches the static tier to `go vet ./...` and the unit tier to `go test ./...` over whatever exists in `pkg/`. Higher tiers are recorded as `skipped` with reason `phase-0-not-implemented`.
- Verdict writing to `tests/results/latest.json` per TESTING.md §7.

Phase 1+ extends this with real executors for each tier, the cached container daemon, the JUnit and GitHub-annotation emitters, and the PR-comment integration.

## Quick reference

```bash
lenny-test --help
lenny-test version
lenny-test list
lenny-test list --groups
lenny-test list --tiers
lenny-test list --specs
lenny-test list --json
lenny-test validate
lenny-test validate-diagnosis
lenny-test infra status

# Dry-run a selection (no execution; shows what would run)
lenny-test --group pr --dry-run
lenny-test --tier component --dry-run
lenny-test --changed --dry-run
lenny-test --since main --dry-run                   # branch diff from its merge base; static, unit, component, and contract
lenny-test --since main --tier unit --dry-run       # the same diff, unit tier only
lenny-test --since main --gate merge --dry-run      # the same diff within the merge gate's tiers
lenny-test run --since main --container             # the same selection inside the gate image, with the gate's container setup (TESTING.md "lenny-test run in the gate container")
lenny-test --since A...B --dry-run                   # any explicit range
lenny-test --spec POOL-R5,§4.6.1 --dry-run     # tests whose // spec: annotations cite them

# The merge gate, in the gate container (TESTING.md "Merge gate")
lenny-test gate
lenny-test gate --here      # the gate's tiers in this environment; UNVERIFIED outside the gate image
lenny-test gate --tier static,unit,component,contract --cover   # the check before a code branch merges (v1 merge policy); then:
lenny-test coverage --diff main --threshold 0.80                 # reads the profile that --cover wrote
lenny-test gate --layout static-first              # static alone, then contract beside component and integration, then unit, then the documentation gates (default serial: every tier one after another)
lenny-test gate --layout unit-alone                 # static and contract beside component and integration, then unit, then the documentation gates
lenny-test gate --layout static-first --go-test-p component=2,integration=2  # the go test -p of a tier while it runs beside another lane
lenny-test gate --gocache-cap-gb 50   # empty the build cache volume first when it holds more than 50 GB (default 30, LENNY_GATE_GOCACHE_CAP_GB; 0 turns the check off)

# Actually run (Phase 0: static + unit only)
lenny-test --tier static
lenny-test --tier unit
lenny-test --group pr-fast
```

## Coverage view

`lenny-test coverage --spec` reports what the `// spec:` annotations say about the specification. The harness builds an annotation index of the tracked Go files on each run (`scripts/specshift/annotation`), joins it with `spec/registry.json`, the successor stubs of the numbered files, and the open and planned issues under `issues/`, and commits nothing. TESTING.md §5 ("Coverage view") states what each row holds.

```bash
lenny-test coverage --spec                     # one line per page, identifier, and numbered heading
lenny-test coverage --spec POOL-R5             # its tests by tier, direct and through a stub, its code, and its issues
lenny-test coverage --spec POOL                # a page and its identifiers
lenny-test coverage --spec §4.6.1              # a numbered heading and the identifiers its stub lists
lenny-test coverage --spec --area sessions     # the pages of one area
lenny-test coverage --spec --json              # the same rows as JSON
lenny-test coverage --spec --threshold 0.40    # exit 1 when fewer rows have a citing test
lenny-test list --specs                        # the page codes, identifiers, and numbered sections
lenny-test list --spec POOL-R5,§4.6.1          # the tests that cite them
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
| `go list` of the selection's import graph | 2m | `LENNY_TEST_GO_LIST_TIMEOUT` |

## Files

| File | Purpose |
|:-----|:--------|
| `main.go` | Entry point; dispatches subcommands |
| `cmd_run.go` | Default run command: parses selectors, resolves into tier plan, executes |
| `cmd_gate.go` | `gate` subcommand: builds the gate image from `tests/gate/Dockerfile`, runs the merge gate in it, and writes `tests/results/gate-<run>.json` |
| `run_container.go` | `run --container`: the `run` selection inside the gate image, with the gate's container setup and the worktree owner's user |
| `gate_lanes.go` | The gate's layouts (`--layout static-first`, `unit-alone`, or `serial`): the phases and lanes its tiers run in, and the `go test -p` of a tier that runs beside another lane (`--go-test-p`) |
| `gate_snapshot.go` | The static tier's snapshot: the read-only checkout of the commit the gate resolved `HEAD` to, which the static tier's checks read, and `lenny-test static-snapshot`, which the gate container runs to make it |
| `gate_gocache.go` | The size cap of the gate's build cache volume `lenny-gate-gocache`: measured with `du -sk` before the gate container starts and emptied with `go clean -cache` when it holds more than `--gocache-cap-gb` (default 30 GB, `LENNY_GATE_GOCACHE_CAP_GB`, 0 turns the check off) and no running container has it mounted |
| `cmd_validate.go` | `validate` and `validate-diagnosis` subcommands |
| `cmd_validate_annotated.go` | The `validate` check that every test from the component tier on carries a `// spec:` annotation or says it verifies no requirement |
| `tier_bounds.go` | The tiers of the merge, area, and release gates, `--tier` lists, and the bound of an unbounded discovery selection |
| `cmd_list.go` | `list` subcommand |
| `cmd_infra.go` | `infra up/down/status/prune` (Phase 0 stubs) |
| `tiers.go` | Tier ordering and group-to-tier resolution |
| `cmd_run_resolve.go` | `--changed`, `--spec`, and `--pkg` resolution into a tier plan |
| `spec_index.go` | The specification model: the annotation index, the identifier registry, the successor stubs of the numbered files, and the issue records, and the resolution of an identifier, a page code, or `§N.M` to its tests |
| `cmd_coverage_spec.go`, `cmd_coverage_spec_text.go` | `coverage --spec`: the coverage view and its text rendering |
| `gotest_json.go` | `go test -json` parsing, and the `spec_sections` of each failure from the annotation index |
| `changed_select.go` | `--changed` and `--pkg` selection: the import graph loaded with every tier's build tag, the owning packages of a changed path, their dependents, and each selected package's tier |
| `spec_edit_select.go` | The tests a specification edit selects: the numbered headings a hunk touches, and the changed identifiers of the new tree with the code that cites them |
| `non_go_inputs.go` | `tests/non-go-inputs.yaml`: the inputs no Go package owns, the tests that read them, and the `validate` check of the file |
| `scoped_runs.go` | Running a scoped tier plan: whole packages in one `go test` invocation and functions by name in another |
| `verdict.go` | JSON verdict writer per TESTING.md §7 |
