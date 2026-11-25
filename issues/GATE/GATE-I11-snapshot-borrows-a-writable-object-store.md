---
id: GATE-I11
status: open
kind: defect
severity: medium
requirements: []
---

# The static tier's snapshot borrows a writable object store

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. The merge-gate review of step 0172.S6 raised it, and change 0172 decision 21 keeps the design during the v1 program and files the alternative here.

## Evidence

- `createStaticSnapshot` in `cmd/lenny-test/gate_snapshot.go` points the snapshot's object store at the worktree's objects through git's alternates file.
- git does not check an object's content against its name when it reads it. The security reviewer substituted a loose object, and `git cat-file --batch`, `git reset --hard`, and `git diff` returned the substitute without error.
- In a main checkout, such as the clone the gate on `main` runs in, the object store lies inside the writable worktree mount. A linked worktree's object store is mounted read-only.

## Gap

A process of the gate user can change what the secret scan, the lint's register read at the merge base, and `buf breaking` read from the snapshot, in the same run or for a later run.

## Suggested resolution

Fill the snapshot's own object store with `git fetch` from the worktree, which re-hashes every object it receives, and lock it with the snapshot (option (a) of GS-16). Measure the fetch's cost per run before building it.
