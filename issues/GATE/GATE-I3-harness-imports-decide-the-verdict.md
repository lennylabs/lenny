---
id: GATE-I3
status: open
kind: doc-gap
severity: low
requirements: []
---

# The record of the harness's imported packages understates what they control

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/git-workflow.md` ("Review changes to the merge gate") define. Change 0158 (decision 6) leaves the scripts and the packages `lenny-test` imports outside the merge-gate review rule.

## Evidence

- `go list -deps ./cmd/lenny-test` lists packages of this module outside `cmd/lenny-test`, among them `scripts/specshift/scope`, `scripts/specshift/defs`, `scripts/specshift/ref`, `scripts/specshift/annotation`, `scripts/specshift/survey`, and `tests/testinfra/testtmp`.
- Item 9 of the merge gate's environment list in `TESTING.md` names these packages only as a writer of the golangci-lint cache through the fill: "the fill itself, which runs `lenny-test` as the branch builds it, including the packages it imports, which the merge-gate review rule leaves out".
- The same binary runs `lenny-test gate --here`, runs every check, and writes the verdict (`gateContainerScript` in `cmd/lenny-test/cmd_gate.go`).

## Gap

Code in those packages runs inside the program that decides the verdict, so a branch that changes them can change any check or the verdict itself without the merge-gate review. The record states the limit only for the cache fill.

## Suggested resolution

State in item 9, and in "Review changes to the merge gate", that the packages `lenny-test` imports run inside the harness that decides the verdict, or add the module packages that `go list -deps ./cmd/lenny-test` lists to the paths that rule names. The second option is an owner decision that revisits change 0158, decision 6.
