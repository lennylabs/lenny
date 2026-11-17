# Steps: 0154 Green main line and merge gate

## Validation owed

## Steps

### 0154.S1 Repair tier 0 and sweep for accidental deletions
- status: Planned
- lanes: [code]

Restore `hack/boilerplate.go.txt`, make the runtime supervisor binary tests skip off Linux with a registered reason, and sweep the deletions of design-only commits since 2026-10-01 for others like it.

### 0154.S2 The gate container, the gate command, and concurrent static checks
- status: Planned
- lanes: [code, documentation]
- depends-on: [0154.S1]

The container that pins the gate's toolchain, `lenny-test gate` with its verdict file, the static checks run concurrently with every failure reported, and `TESTING.md`.

### 0154.S3 The merge policy
- status: Planned
- lanes: [documentation]
- depends-on: [0154.S2]

The policy in `test-coverage.md`, `git-workflow.md`, and the `build` skill, the quarantine cap in `tests/flake-budget.yaml`, the `pr` workflow as decision 2 settles, and the deletion of the tier-0 category D gates.
