---
id: GATE-I2
status: open
kind: question
severity: low
requirements: []
---

# The static tier's other checks run alongside golangci-lint and can write what it reads

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The owner accepted a list of writers outside the read-only golangci-lint cache mount on 2026-11-20 (GT-16), which item 9 of the merge gate's environment list in `TESTING.md` records.

## Evidence

- `runStaticChecks` in `cmd/lenny-test/cmd_run.go` runs the static tier's checks concurrently, bounded by `staticCheckConcurrency`. The check table runs the branch's own scripts with `exec.Command("bash", script)`, such as `scripts/lint-schema.sh`, `scripts/lint-queries.sh`, and `scripts/lint-migrations.sh`, and the static tier's Go test packages, as the gate user.
- The golangci-lint check (`cmd/lenny-test/lint_changed.go`) reads the worktree, the register `tests/registers/golangci-lint-findings.yaml`, and a whole-tree JSON report it writes under the temporary directory, which in the gate container is the fixture root that the gate user owns.
- Since the branch `fix/gate-follow-ups` (commit `a317cba1b`), a gate whose cache fill did not finish runs both golangci-lint runs with a private cache under the same temporary directory (`privateLintCache`). The concurrent checks can write that cache between the changed-line run and the whole-tree run. `TESTING.md` states this, and the GT-16 list does not name these writers.
- The end-of-run state check (`gateStateChange` in `cmd/lenny-test/cmd_gate.go`) compares `HEAD`, the tree's dirty state, and the merge bases, so a script that edits a source file or the register and restores it during the lint is not detected.
- The `scripts/` checks are outside the merge-gate review rule in `.claude/rules/git-workflow.md` ("Review changes to the merge gate").

## Gap

A branch can change what its own golangci-lint check reads while the check runs, through a script the static tier executes alongside it, and so hide a finding from the verdict. The private cache of the fallback adds one more such target. The tree has no record of whether the owner accepts this.

## Suggested resolution

The owner chooses one of the following:

1. Record the static tier's concurrent checks as accepted writers, beside the GT-16 list in item 9 of the merge gate's environment list.
2. Run the golangci-lint check of the static tier before, and apart from, the checks that execute branch scripts or branch tests, at the cost of the static tier's wall time. This also keeps the fallback's cold runs from sharing memory with `go vet ./...` and the static Go test packages.

The review of `a317cba1b` recommends option 2.
