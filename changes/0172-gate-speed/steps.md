# Steps: 0172 Merge gate wall time

## Validation owed

- **The cost of coverage in the unit tier.** Run the gate's unit tier twice on one commit with every package selected, once with the coverage flags and once without, in the gate image with no other Docker job or test run on the host. Record both durations in the digest of 0172.S3. 0172.S3 waits on it.
- **The concurrent layout under load.** Run `lenny-test gate --all` once on one commit with the layout decision 5 chooses, with no other Docker job on the host, and record the run's wall time, each tier's duration and test counts, the peak memory of the Docker Desktop VM (`docker stats` sampled during the run), and any container-start timeout. Each tier's test counts match those of a serial run on the same commit. A container-start timeout, or a VM memory peak within 10% of its limit, sends the bound back to the owner. 0172.S2 makes the layout the default after this run, keeps an option that runs the tiers serially, and the gate runs on `main` that follow continue the validation as decision 7 states.
- **The static-first layout under load.** Run `lenny-test gate --all` once on one commit with the layout of decision 15, with no other Docker job on the host, and record what the run of 0172.S2 recorded: the wall time, each tier's duration and test counts, the VM's memory peak, and any container-start timeout. A clean run makes the layout the default; a container-start timeout or a memory peak within 10% of the limit returns the layout to the owner. 0172.S6 waits on it.

## Steps

### 0172.S1 Selection width and the verdict's selection record
- status: Landed
- lanes: [code, documentation]
- depends-on: []
- closes: []

GS-1 and GS-2. The row target form for a package whose own tests read an input, its check in `lenny-test validate`, the move of the `spec/**` row's `pkg/observability/tracing` and `pkg/api/openapi` and of any other row whose package reads its input only in tests, and `gate.selection` in the verdict and the printed summary, with their unit tests. Then the full-run cadence in "The gate on `main`" of `.claude/rules/git-workflow.md`, rule 1 of the merge policy in `.claude/rules/test-coverage.md`, and decision 13 of `changes/programs/v1.md`, as open decision 2 settles; and "Selection algorithm" and "Merge gate" in `TESTING.md`. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S2 Concurrent tiers
- status: Landed
- lanes: [code, documentation]
- depends-on: [0172.S3]
- closes: []

GS-3, with the layout and bound open decision 5 settles. The lanes in `gateRunner.run`, the gate options for the layout and the `-p` of each tier, and the run's wall time in the verdict, with their unit tests on fake tier executors. Then "Merge gate" in `TESTING.md` and the options in `cmd/lenny-test/README.md`. It lands after 0172.S3 so the validation runs measure the unit tier as the gate will run it. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S3 No coverage profile in the gate's unit run
- status: Landed
- lanes: [code, documentation]
- depends-on: []
- closes: []

GS-4. The gate's unit run without `-coverprofile` and `-covermode=atomic`, with `-race` kept, and `run --changed` and `--tier unit` still writing `tests/results/cover.out`, with the unit tests of both. Then "Merge gate" in `TESTING.md` and `results/cover.out` in `tests/README.md`. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S4 The gate's tier and coverage options, and the check before a merge
- status: Landed
- lanes: [code, documentation]
- depends-on: [0172.S2]
- closes: []

Decisions 8 and 9. `--tier` and `--cover` on `lenny-test gate`, their record in the verdict and the printed summary, and their unit tests: a restricted run keeps the diff selection within the named tiers, an unknown or non-gate tier is refused, and `--cover` writes the profile that `lenny-test coverage --diff` reads. Then the check before a merge of decision 8 in "v1 merge policy" of `.claude/rules/git-workflow.md`, rule 1 and item 3 of "How to apply when editing" in `.claude/rules/test-coverage.md`, decision 13 of `changes/programs/v1.md`, the v1 paragraph of "Building" in `.claude/skills/build/SKILL.md`, and "Merge gate" in `TESTING.md` and `cmd/lenny-test/README.md`. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S5 `lenny-test run` in the gate container
- status: Landed
- lanes: [code, documentation]
- depends-on: [0172.S4]
- closes: []

Decision 10. `--container` on `lenny-test run`, reusing the gate's container setup, with unit tests on the arguments it builds, and the repository fix for the `go vet` failure on macOS hosts. Then the developer loop in `.claude/rules/test-coverage.md` ("How to run"), `TESTING.md`, and `cmd/lenny-test/README.md`. It depends on 0172.S4 because both edit the gate's container setup and the same pages. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S6 The static tier reads a snapshot
- status: Building
- lanes: [code, documentation]
- depends-on: [0172.S2]
- closes: []

Decision 14. The gate resolves `HEAD` once and passes the commit to the secret scan and the static tier's git reads, and the static tier's checks that read the working tree run on an export of that commit that the gate user cannot write, so a test in a concurrent lane cannot change what the static tier reads. It also adds the layout of decision 15, which runs the static tier in a phase of its own ahead of the lanes of decision 5, and makes it the default when its validation run under "Validation owed" is clean. Then "Merge gate" in `TESTING.md`. It is built after 0172.S1 to 0172.S5 merge. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S7 The full run's unit tier and two selection rows
- status: Building
- lanes: [code, documentation]
- depends-on: [0172.S6]
- closes: []

Decisions 16 and 17. The unit tier of a `--all` run leaves out `tests/docs`, with a test of the package list it runs; issue GATE-I9 records the failing gap-record tests. The row of `.github/workflows/release.yml` in `tests/non-go-inputs.yaml` runs `tests/security`, and the runbooks row names the chaos, security, and load_kind tiers. Then "Merge gate" in `TESTING.md`. The diff touches `cmd/lenny-test/` and `tests/non-go-inputs.yaml`, so it is reviewed with the `review` skill before it merges.

### 0172.S8 The coverage profile's hash and the container messages
- status: Building
- lanes: [code, documentation]
- depends-on: [0172.S7]
- closes: []

Decision 18. The verdict of a gate run with `--cover` records the SHA-256 of `tests/results/cover.out`, and `lenny-test coverage --diff` refuses a profile whose hash does not match the latest restricted gate verdict for the checked-out commit. The image-build messages name the command that runs them. Then `cmd/lenny-test/README.md`. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.
