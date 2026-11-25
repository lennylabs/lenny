---
id: GATE-I10
status: open
kind: defect
severity: high
requirements: []
---

# The static tier's working files and caches are writable by the gate user

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. The merge-gate review of step 0172.S6 raised it (security reviewer, High), and change 0172 decision 20 accepts it as a limit during the v1 program and files the fix here.

## Evidence

- The secret scan writes its tree and `report.json` under a directory from `os.MkdirTemp("", "lenny-secretscan-")` in `runSecretScanAt`, and the lint writes its patch and whole-tree report with `os.CreateTemp("", "lenny-lint-*.patch")` in `cmd/lenny-test/lint_changed.go`. Both directories sit under the fixture root that the gate user owns.
- `go test` links the static tier's test binaries under `GOTMPDIR`, which the gate user owns.
- The static tier builds from the module cache and the Go build cache, which the gate user can write in the same run.
- The static tier runs the branch's own `scripts/*.sh` as the gate user alongside the secret scan and the lint, in every layout.

## Gap

A process of the gate user can rewrite a working file between its write and its read, or a cache entry before the static tier builds from it, and so change what the secret scan or the lint reports. The static-first default (decision 19) removes the concurrent tests; the branch's scripts remain, as in GATE-I7.

## Suggested resolution

Run the static tier's checks under a second user with a private `TMPDIR`, `GOTMPDIR`, and build cache, so no test and no branch script can write what they read (option (a) of GS-15). Revisit when contributors other than the owner run the gate.
