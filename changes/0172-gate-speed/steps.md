# Steps: 0172 Merge gate wall time

## Validation owed

- **The cost of coverage in the unit tier.** Run the gate's unit tier twice on one commit with every package selected, once with the coverage flags and once without, in the gate image with no other Docker job or test run on the host. Record both durations in the digest of 0172.S3. 0172.S3 waits on it.
- **The concurrent layout under load.** Run `lenny-test gate --all` three times on one commit with the layout open decision 5 chooses, with no other Docker job on the host, and record each run's wall time, each tier's duration, the peak memory of the Docker Desktop VM (`docker stats` sampled during the run), and any container-start timeout. A container-start timeout that recurs, or a VM memory peak within 10% of its limit, sends the bound back to the owner. 0172.S2 makes the layout the default only after these runs.

## Steps

### 0172.S1 Selection width and the verdict's selection record
- status: Building
- lanes: [code, documentation]
- depends-on: []
- closes: []

GS-1 and GS-2. The row target form for a package whose own tests read an input, its check in `lenny-test validate`, the move of the `spec/**` row's `pkg/observability/tracing` and `pkg/api/openapi` and of any other row whose package reads its input only in tests, and `gate.selection` in the verdict and the printed summary, with their unit tests. Then the full-run cadence in "The gate on `main`" of `.claude/rules/git-workflow.md`, rule 1 of the merge policy in `.claude/rules/test-coverage.md`, and decision 13 of `changes/programs/v1.md`, as open decision 2 settles; and "Selection algorithm" and "Merge gate" in `TESTING.md`. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S2 Concurrent tiers
- status: Planned
- lanes: [code, documentation]
- depends-on: [0172.S3]
- closes: []

GS-3, with the layout and bound open decision 5 settles. The lanes in `gateRunner.run`, the gate options for the layout and the `-p` of each tier, and the run's wall time in the verdict, with their unit tests on fake tier executors. Then "Merge gate" in `TESTING.md` and the options in `cmd/lenny-test/README.md`. It lands after 0172.S3 so the validation runs measure the unit tier as the gate will run it. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.

### 0172.S3 No coverage profile in the gate's unit run
- status: Planned
- lanes: [code, documentation]
- depends-on: []
- closes: []

GS-4. The gate's unit run without `-coverprofile` and `-covermode=atomic`, with `-race` kept, and `run --changed` and `--tier unit` still writing `tests/results/cover.out`, with the unit tests of both. Then "Merge gate" in `TESTING.md` and `results/cover.out` in `tests/README.md`. The diff touches `cmd/lenny-test/`, so it is reviewed with the `review` skill before it merges.
