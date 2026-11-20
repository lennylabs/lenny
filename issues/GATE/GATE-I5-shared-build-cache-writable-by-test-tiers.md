---
id: GATE-I5
status: open
kind: build-gap
severity: medium
requirements: []
---

# The test tiers can write Go build cache entries that later gate runs reuse

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. It is item 1 of the third review of the branch `fix/gate-follow-ups`. The owner deferred it on 2026-11-20, until outside contributors arrive, by the answer to question GT-19: "Do 3 and 4 as a small follow-up and file 1, 2, 5 as GATE issues."

## Evidence

- `TESTING.md` ("Merge gate", item 5 of the environment list) states that the gate keeps the Go build cache in the named volume `lenny-gate-gocache`, which every gate run on the host mounts, including a gate that another worktree runs at the same time.
- `TESTING.md` ("Merge gate", item 9) states that every go command of a run other than the build of `lenny-test` uses that volume: the builds of the test tiers, the static tier's checks, and the golangci-lint run of the cache fill. It states that an entry a branch's tests write can change what another test builds or what the lint reads in a later run, and that the Go build cache on the volume stays writable to the test tiers.
- Since the change for GT-15 of 2026-11-20, the program that decides the verdict is compiled from an export of `HEAD` with an empty build cache of its own under `GOTMPDIR`, and root installs it read-only at `/usr/local/libexec/lenny-gate/lenny-test` (`cmd/lenny-test/cmd_gate.go`). Since GT-11, golangci-lint's own cache directory on the volume is mounted read-only for the test tiers. Neither change covers the Go build cache entries.

## Gap

A test of one gate run runs as the gate user, which owns the build cache volume, so it can write or replace an entry in the Go build cache. A later gate run, in the same worktree or in another one, then links that entry into a test binary or reads it as the export data that the static tier's lint analyzes, so the later run's test results and lint findings can come from code that no commit holds. The verdict program itself is not affected.

## Suggested resolution

The review named the following options:

1. Mount the volume read-only and give each run a writable layer of its own over it, such as an overlay mount whose upper directory is on the container's own file system and is discarded after the run.
2. Give each worktree a build cache volume of its own, so a branch's tests cannot change the builds of another worktree's gate. A later run in the same worktree still reads what an earlier run's tests wrote.
3. Accept the limit and keep it stated in `TESTING.md`.

The owner chooses among them when outside contributors arrive.
