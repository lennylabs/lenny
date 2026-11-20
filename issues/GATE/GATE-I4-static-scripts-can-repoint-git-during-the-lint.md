---
id: GATE-I4
status: open
kind: build-gap
severity: low
requirements: []
---

# A static-tier script can repoint the worktree's .git while golangci-lint runs

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. It is item 5 of the third review of the branch `fix/gate-follow-ups`. The owner deferred it on 2026-11-20, until outside contributors arrive, by the answer to question GT-19: "Do 3 and 4 as a small follow-up and file 1, 2, 5 as GATE issues."

## Evidence

- `runStaticTier` in `cmd/lenny-test/cmd_run.go` lists the golangci-lint check and the branch's shell checks under `scripts/`, such as `scripts/lint-schema.sh`, `scripts/check-action-pins.sh`, and `scripts/check-markdown-links.sh`, in one table.
- `runStaticChecks` in the same file runs every check of that table concurrently, bounded by `staticCheckConcurrency`.
- The golangci-lint check reads the repository through git: it resolves the merge base, writes the diff that `--new-from-patch` reads, and reads the register `tests/registers/golangci-lint-findings.yaml` at the merge base (`TESTING.md`, item 7 of the static-tier check list).
- `gateStateChange` in `cmd/lenny-test/cmd_gate.go` reads `HEAD`, the tree's state, and both merge bases again after the last tier, and keeps the verdict from `PASS` when any of them changed.

## Gap

A branch's script runs with the gate user's rights, so it can rewrite the worktree's `.git` file, or the `.git` directory of a main checkout, to point at another repository while golangci-lint runs, and restore it before the tiers end. The lint then compares the tree with a merge base and a register that the other repository holds, and the end-of-run check reads the restored repository and finds no change.

## Suggested resolution

The review named the following options:

1. Run the shell checks after the golangci-lint check rather than alongside it, so a script cannot change the repository the lint reads while it runs.
2. Set `GIT_DIR` and `GIT_COMMON_DIR` for the whole run to the directories the gate resolved before the static tier, so a rewritten `.git` file does not change which repository a git command reads.

The owner chooses between them when outside contributors arrive.
