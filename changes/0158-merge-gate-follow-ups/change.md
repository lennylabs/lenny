# Merge gate follow-ups

Status: `Draft`, revision 1, 2026-11-17. Programs: [v1](../programs/v1.md), milestone M1.

## Problem

The security review of the merge gate (change 0154, archived at [`changes/archive/2026-11-17-0154-merge-gate/`](../archive/2026-11-17-0154-merge-gate/change.md)) raised three questions that only the owner could settle, and the owner settled them on 2026-11-17 (`decisions.md`). Two of the answers need work: no secret scan runs before a push, and the gate builds its own harness from the branch under test, so a branch that edits the harness can turn a check off and still pass.

## Scope

- **In scope.** `gitleaks` in the gate's static tier, pinned in the gate image, with a reviewed allowlist for the repository's test fixtures; and the rule that a diff to `cmd/lenny-test/`, `tests/gate/`, or `tests/flake-budget.yaml` is reviewed before it merges, in `.claude/rules/git-workflow.md` and the `review` and `build` skills.
- **Out of scope.** Building the harness from the merge base, which the owner declined. Isolating the gate from the host waits on open decision 1.

## Documentation

Contributors: `TESTING.md` names the secret scan and the review rule.

## Tests

The secret scan fails on a planted secret in a fixture and passes on the tree; the gate's static tier lists it.
