---
id: GATE-I3
status: open
kind: build-gap
severity: low
requirements: []
---

# The shared module cache can change between go mod verify and the build of lenny-test

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") and `.claude/rules/test-coverage.md` ("Gates") define. The issue is filed under `GATE`, the issue code that `issues/README.md` ("Codes outside the specification") assigns to the merge gate. It is item 2 of the third review of the branch `fix/gate-follow-ups`. The owner deferred it on 2026-11-20, until outside contributors arrive, by the answer to question GT-19: "Do 3 and 4 as a small follow-up and file 1, 2, 5 as GATE issues."

## Evidence

- `TESTING.md` ("Merge gate", item 5 of the environment list) states that the gate keeps the Go module cache in the named volume `lenny-gate-gomod`, which every gate run on the host mounts, and that the container runs `go mod verify` against `go.sum` before it compiles anything from the module cache.
- `TESTING.md` ("Merge gate", item 9) states that the module cache volume stays writable to the gate user and that `go build` checks a module's recorded hash rather than its extracted files. A process that writes the volume between `go mod verify` and the compile of `lenny-test`, such as a test of a gate container that another worktree runs at the same time or a container that a test starts through the host Docker socket, can change the source the compile reads.
- The build step of `gateContainerScript` in `cmd/lenny-test/cmd_gate.go` runs `go mod verify` and then `go build` in the export of `HEAD`. Both read the module cache at `/go/pkg/mod`, which the container mounts from that volume (`gateModVolume`).

## Gap

The verification and the compile read the module cache at two different times. A dependency's extracted source that changes between them is compiled into the program that decides the verdict, although `go mod verify` passed.

## Suggested resolution

The review named one option: give the build of `lenny-test` a private module cache, a copy of the modules it needs under `GOTMPDIR` that no other process can write, run `go mod verify` against that copy, and compile from it. The copy adds the time to copy or download the modules to every gate run. The owner decides when outside contributors arrive.
