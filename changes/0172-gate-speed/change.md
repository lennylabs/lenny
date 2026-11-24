# Merge gate wall time

Status: `In progress`, revision 2, 2026-11-24.

## Problem

A gate run whose selection reaches the test tiers takes about 22 minutes on the owner's laptop, and every such run holds the machine for that time. Under v1 program decision 13 ([`changes/programs/v1.md`](../programs/v1.md), and "v1 merge policy" in `.claude/rules/git-workflow.md`), branches merge on `lenny-test run --changed` and the secret scan, and `lenny-test gate` runs on the head of `main` after a batch of merges, against a base that is the commit of the latest `PASS` run on `main`. The gate's wall time therefore sets how often `main` is checked and how long a red `main` goes unseen.

The host has 12 CPUs. Docker Desktop's VM has 8 GB of the host's 38.6 GB, and that VM holds the gate container (the compiles, the `-race` test binaries, the envtest control planes, and golangci-lint) and every testcontainer the component and integration tiers start.

The gate verdicts of the runs that reached every test tier record these durations:

| Run | Head | Base | Static | Unit | Component | Contract | Integration | Docs | Sum |
| :-- | :-- | :-- | --: | --: | --: | --: | --: | --: | --: |
| `e5441f6ed9f27074fcb7e8ef` | `72b121793` | `f858b6adb` | 249 s | 364 s (16,229 tests) | 219 s | 115 s | 390 s | 5 s | 22.4 min |
| `5b4973d3561ab5b97840c4b2` | `791e693e0` | `b8bba2f99` | 248 s | 376 s (16,229 tests) | 226 s | 116 s | 384 s | 5 s | 22.6 min |
| `10acfc5256c00211c7396b58` | `eb6579b62` | `b8bba2f99` | 248 s | 368 s (16,229 tests) | 203 s | 115 s | 379 s | 5 s | 22.0 min |

The tiers run one after another (`gateRunner.run` in `cmd/lenny-test/cmd_gate.go`), so the run's wall time is the sum of its tiers. In run `10acfc52` the static tier's detail reports two checks over their time budgets: `go test ./tests/static/structure/...` took 1m33s against 1m, and `go test ./tests/static/lint/...` took 43s against 30s.

The 22 minutes come from these costs:

- **Selection width.** The gate already selects by package from the diff against its base (see "Current state"), but the selection for the diff of run `10acfc52` reaches 145 unit package patterns, 26 component packages, 45 contract packages, and the whole integration package. Of the 145 unit patterns, 123 come from the `spec/**` row of `tests/non-go-inputs.yaml`, which names `pkg/observability/tracing` and `pkg/api/openapi` as targets. The selector treats a target package as changed and walks its dependents, although only the tests of those packages read the specification.
- **Serial tiers.** The static, unit, and contract tiers start no container, and the component and integration tiers do, yet each tier waits for the one before it.
- **Coverage in the gate's unit run.** The unit tier runs `go test -race -count=1 -coverprofile=tests/results/cover.out -covermode=atomic` (`unitTierFlags` in `cmd/lenny-test/cmd_run.go`). Nothing in the gate reads the profile.

The host has little headroom for concurrent work. On 2026-11-23 the gate run `5b4973d3561ab5b97840c4b2` failed one container-start timeout in `tests/component/stores` while a second Docker job ran, and the test passed on rerun (merge commit `77a8115d6`). The log of gate run `9b5269e8` on the gate follow-up branch records the golangci-lint cache fill ending on `signal: killed`, the signal the kernel sends when it ends a process for lack of memory. Gate runs that shared the host with other work recorded component tiers of 516 s and 780 s and a contract tier of 382 s (runs `4e7b22fe` and `2a71ae1c`), against 203 s to 255 s and 115 s to 128 s in the runs above.

## Scope

The change covers:

- The selection the gate reads: the row form of `tests/non-go-inputs.yaml` for a package whose own tests read an input, and the rows that use it (GS-1).
- The gate's verdict: a summary of what each tier selected, so a selected run reads apart from a full one (GS-1).
- The full-run cadence on `main` in the v1 merge policy (GS-2).
- Concurrent execution of the gate's tiers, bounded for the 8 GB VM (GS-3).
- The coverage flags of the gate's unit run (GS-4).
- `TESTING.md` "Merge gate" and "Selection algorithm", `cmd/lenny-test/README.md`, and the v1 merge policy text in `.claude/rules/git-workflow.md` and `.claude/rules/test-coverage.md`.

It leaves out:

- Splitting the integration package. Change 0116 decision 6 splits it area by area during M3, so the selection reaches only the affected part ([0116 decisions](../archive/2026-11-18-0116-coverage-and-selection/decisions.md)).
- The time budgets of the static tier's structure and lint checks, which the static tier reports and which set the floor of every run (4.1 to 4.9 minutes for a run that selects only the static tier and the general documentation gates).
- `lenny-test run --changed`, which keeps its coverage profile, because `lenny-test coverage --diff` reads it (see GS-4).
- Docker Desktop's memory allocation. Raising it from 8 GB is an owner action on the host, outside the repository, and it changes the bound GS-3 needs.

## Terms

| Term | Meaning |
| :-- | :-- |
| Base | The commit the gate compares with: the merge base of `--base` (default `main`) and `HEAD`. On `main` under the v1 merge policy, it is the commit of the latest `PASS` run on `main`, which the run reads by running in a clone whose local `main` points at that commit. |
| Selected run | A gate run whose unit, component, contract, and integration tiers are the `--changed` selection against the base (`gateChangedPlan`). This is the gate's default today. |
| Full run | A gate run with `--all`, which runs every package of the unit tier and every test of the component, contract, and integration tiers whatever the diff. The verdict records it as `gate.all`. |
| Row target | A tier name or a package directory that a row of `tests/non-go-inputs.yaml` lists under `runs`. |
| Container tier | A tier whose tests start containers on the host Docker daemon: component and integration. |
| Lane | A sequence of tiers the gate runs one after another. Lanes run at the same time. |

## High-level design

The gate already runs only what a batch of merges reaches. This change narrows what a specification edit reaches, records the selection in the verdict, and writes into the v1 merge policy when a full run happens instead. It then runs the tiers that start no container alongside the tiers that do, within a bound chosen for the 8 GB VM, and stops writing a coverage profile that nothing in the gate reads.

## Requirements

None. The harness is contributor tooling, which `spec-driven-development.md` places outside the specification's obligations.

## Current state

The state on `main` at `eedd9770e`:

- `cmd/lenny-test/cmd_gate.go`, `gateChangedPlan`: the gate resolves `selector{changed: true, since: base, gate: gateMerge, rdepsDepth: rdepsDepth}` and keeps the unit, component, contract, and integration tiers (`gateChangedTiers`). `--all` resolves those tiers whole. The verdict records `gate.base`, `gate.merge_base`, `gate.integration_merge_base`, `gate.rdeps_depth`, and `gate.all`.
- `cmd/lenny-test/changed_select.go`: the unit, component, and contract tiers are package-scoped (`packageScopedTiers`). The integration tier is one package and runs whole when the selection reaches it. `addTarget` returns a row's package target as a changed package, and `selectChanged` adds it to the roots it walks for dependents to the full closure (`defaultRdepsDepth` is -1).
- `tests/non-go-inputs.yaml`: the `spec/**` row lists `tests/conformance`, `pkg/observability/tracing`, and `pkg/api/openapi`. The files that read the specification are `pkg/observability/tracing/tracing_test.go` and `pkg/api/openapi/completeness_test.go`, which are tests. Change 0116 CS-6 states that "the tests named in each row are the tests that read the input, rather than the tests of the code it configures" ([0116 change](../archive/2026-11-18-0116-coverage-and-selection/change.md)).
- A dry run of the selection for the diff of run `10acfc52` (`lenny-test run --changed --since b8bba2f99..eb6579b62 --gate merge --dry-run`, read against the tree at `eedd9770e`) lists 145 unit patterns, 26 component, 45 contract, and the integration tier. `--pkg pkg/observability/tracing,pkg/api/openapi` alone lists 123 unit, 24 component, and 45 contract patterns and the integration tier. The packages the rest of that diff changed (`pkg/storage/issuedtokenstore`, and the targets of the `tests/registers/**` row) list 61 unit, 15 component, and 27 contract patterns and the integration tier.
- Gate runs whose diff holds only records select the static tier and the general documentation gates. The verdicts of runs `dc645a0e`, `70285643`, `7fc3360e`, and `eeadaf5b` record 4.1 to 4.9 minutes. The logs of runs `c0ab85a7dc25b55f6d040384` and `b2966b6f87ce54b7bf261ade` on `main` show the same two tiers. A run whose diff reached a few leaf packages (`bc08db5e`, `d7dafc2d`) ran 869 to 917 unit tests in 9.5 to 9.8 minutes.
- `cmd/lenny-test/cmd_run.go`, `runUnitTier`: the unit tier writes the coverage profile to `coverProfilePath()`, which is `tests/results/cover.out` unless `LENNY_COVER_PROFILE` names another path or is set empty. The gate does not set it.
- Readers of `cover.out`: `lenny-test coverage --go` and `lenny-test coverage --diff` (`cmd/lenny-test/cmd_coverage.go`), and the `unit` job of `.github/workflows/pr.yml`, which runs `lenny-test --tier unit`, then `lenny-test coverage --diff origin/main --threshold 0.80`, and uploads the profile. No file under `tests/static/`, `tests/gate/`, or `tests/docs/` reads it, and `cmd_gate.go` does not.
- `cmd/lenny-test/timeouts.go`: the component tier's cap is 600 s and the integration tier's is 1800 s. The fixtures under `tests/testinfra/containers` wait for a container to become ready within their own startup timeouts, such as 60 s for the fake GCS server.

## Detailed design

### GS-1 Selection a specification edit reaches, and the selection in the verdict

A row target gets a form that runs a package's own tests without walking its dependents, for a package whose tests read the input. The present form stays for a helper package that reads the input on behalf of its importers, such as the envtest helper that loads the chart's CRDs. One way to write it is a per-target suffix or a row field, such as `runs: [pkg/observability/tracing (tests)]` or `scope: package`; the build step picks the spelling that `lenny-test validate` checks most simply. The `spec/**` row moves `pkg/observability/tracing` and `pkg/api/openapi` to the new form. The build step reads every other row with a package target and moves each one whose package reads the input only in its tests.

For the diff of run `10acfc52`, the narrowed selection would hold about 63 unit patterns (61, plus the two packages' own tests), 15 component, and 27 contract patterns, against 145, 26, and 45. The integration tier still runs whole, because `pkg/storage/issuedtokenstore` reaches it.

The verdict's `gate` section gains a `selection` record per tier: `whole`, or the count of packages run whole and of functions run by name. A full run records `whole` for every tier and `gate.all: true`. A reader then tells a selected run from a full one by `gate.all`, and tells how much a selected run covered by `gate.selection`. The gate's printed summary shows the same per tier.

### GS-2 Full runs on `main`

A selected run on `main` covers what the merges since the base reach. A full run covers what the selection misses: a dependency through a path that no package owns and no row matches, a change to `go.mod` (which the selection reports and does not widen), and drift in the environment. The v1 merge policy states when a full run happens: before a milestone is reported complete, and once a week on a day a merge landed. A full run is a gate run with `--all` against the same base, and its `PASS` counts as the latest `PASS` run on `main` like any other. The merge commit message and the step's digest name a full run by its run identifier as they do any run.

The text lands in "The gate on `main`" of `.claude/rules/git-workflow.md`, in rule 1 of the merge policy in `.claude/rules/test-coverage.md`, and in the v1 program's decision 13 as the owner's amendment.

### GS-3 Concurrent tiers

`gateRunner.run` runs the tiers in lanes instead of one sequence. Every tier still runs whatever another tier reports, and the verdict still records each tier's own duration. The verdict gains the run's wall time, because the sum of the tiers no longer equals it. `gateStateChange` keeps reading the state after the last tier of every lane. Each tier's `go test` output goes to its own result, so lanes do not interleave results, and the progress lines on standard error name their tier.

The bound has two parts: which tiers overlap, and the `go test -p` each tier gets while it overlaps. Both are gate options with defaults that open decision 5 sets, so the owner or a host with more memory changes them without a code edit. The fixtures' container startup timeouts stay as they are.

Estimates from the per-tier durations above (static 4.1, unit 6.1 to 6.3, component 3.4 to 3.8, contract 1.9, integration 6.3 to 6.5, docs 0.1 minutes), with no slowdown from sharing the machine:

| Layout | Lanes | Wall time |
| :-- | :-- | --: |
| Serial (today) | static, unit, component, contract, integration, docs | 22.0 to 22.7 min |
| Unit alone | [static, contract] beside [component, integration]; then unit; then docs | 15.9 to 16.7 min |
| Two lanes | [static, unit, contract] beside [component, integration]; then docs | 12.2 to 12.4 min |

These are lower bounds. Tiers that share the machine slow each other, as the runs under load in "Problem" show, and the unit tier under `-race` uses every CPU the container sees. The "Unit alone" layout keeps the unit tier, which compiles and runs the most code and starts the envtest control planes, away from the containers. The "Two lanes" layout overlaps the unit tier with the integration tier, which starts the gateway as a subprocess per test against a shared Postgres container. A run with GS-4 has a shorter unit tier in either layout.

### GS-4 No coverage profile in the gate's unit run

The gate's unit run keeps `-race` and `-count=1` and drops `-coverprofile` and `-covermode=atomic`. The gate turns the profile off for its own run (for example by setting `LENNY_COVER_PROFILE` empty in the gate container, or by a field of `tierPlan`), so `lenny-test run --changed` and `lenny-test --tier unit` keep writing `tests/results/cover.out`. Those keep it because `lenny-test coverage --diff`, which `test-coverage.md` asks for before a change is declared done, reads the profile the last unit run wrote, and the `unit` job of `.github/workflows/pr.yml` reads it after `lenny-test --tier unit`. A coverage report on `main` comes from `lenny-test --tier unit` followed by `lenny-test coverage`, run when it is wanted.

The saving is not measured. The build step measures the unit tier with and without the coverage flags on one commit, as "Validation owed" in `steps.md` states, and records the result in the step's digest.

## Limits, and alternatives rejected

- **A new `--changed` mode for the gate.** The gate's default is already the `--changed` selection against the base, and `--all` is the full mode. A second mode would duplicate it.
- **Narrowing the dependent walk with `--rdeps-depth`.** A depth of 0 or more makes the verdict `UNVERIFIED`, because a dependent's build can still break. GS-1 narrows only targets whose dependents do not read the input.
- **Overlapping every tier with no bound.** The component tier already runs its packages in parallel, and each package starts its own containers in the 8 GB VM. An unbounded overlap adds the integration tier's containers and the unit tier's memory on top.
- **Concurrency guarantees.** GS-3 does not guarantee that a container starts within its fixture's timeout under load. A container-start timeout that a rerun on the same commit does not reproduce stays a load failure, as "A red gate on `main`" in `.claude/rules/git-workflow.md` states.
- **Coverage on `main`.** After GS-4, no gate run on `main` produces a coverage profile. The 80% floor on new code stays a check a branch runs with `lenny-test coverage --diff` on its own `run --changed` profile.

## Specification and non-specification changes

- No specification page.
- `cmd/lenny-test/changed_select.go` and `cmd/lenny-test/non_go_inputs.go`: the row target form of GS-1, and its check in `lenny-test validate`.
- `tests/non-go-inputs.yaml`: the `spec/**` row and any other row GS-1 moves.
- `cmd/lenny-test/cmd_gate.go` and `cmd/lenny-test/verdict.go`: `gate.selection`, the lanes and their options, and the wall time (GS-1, GS-3).
- `cmd/lenny-test/cmd_run.go`: the gate's unit run without coverage (GS-4).
- `.claude/rules/git-workflow.md` and `.claude/rules/test-coverage.md`: the full-run cadence (GS-2).
- `changes/programs/v1.md`: the amendment of decision 13 (GS-2).

The change touches `cmd/lenny-test/`, so each step's diff is reviewed with the `review` skill before it merges, under "Review changes to the merge gate" in `.claude/rules/git-workflow.md`.

## Documentation

- `TESTING.md` (contributor): "Selection algorithm" states the row target form for a package whose own tests read an input. "Merge gate" states `gate.selection`, the lanes and their options, the wall time in the verdict, and that the gate's unit run writes no coverage profile.
- `cmd/lenny-test/README.md` (contributor): the gate's new options.
- `tests/README.md` (contributor): `results/cover.out` comes from `lenny-test --tier unit` and `run --changed`, and the gate does not write it.
- No reader-facing behavior changes for any specification page.

## Tests

The harness is contributor tooling, so its tests carry no `// spec:` annotation and say so in a comment.

- Unit, `cmd/lenny-test`: a row target in the new form selects the package's tests and none of its dependents, and the present form still walks them; `lenny-test validate` refuses a malformed target; the `spec/**` row of the committed file selects no dependent of `pkg/observability/tracing`; `gate.selection` records `whole` for a full run and the counts for a selected one.
- Unit, `cmd/lenny-test`, with fake tier executors (`gateRunner`): each lane runs its tiers in order; the lanes run at the same time; a failing tier does not stop another lane; every tier is recorded with its own duration; the wall time is recorded; the state check runs after every lane ends; the overlap and `-p` options reach the executors.
- Unit, `cmd/lenny-test`: the gate's unit tier arguments hold `-race` and no `-coverprofile` or `-covermode`, and `run --changed` still writes the profile (`scoped_runs_test.go` covers the second today).
- Measurement: the validation runs listed in `steps.md`.
