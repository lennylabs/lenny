---
id: GATE-I4
status: open
kind: build-gap
severity: low
requirements: []
---

# The gate container's own variables reach the tests of every tier

## Specification

No specification page owns the merge gate, which `TESTING.md` ("Merge gate") defines. A test is expected to behave the same inside the gate container and on a development host, apart from the environment differences the gate documents.

## Evidence

- `gateContainerScript` in `cmd/lenny-test/cmd_gate.go` exports `LENNY_GATE_HARNESS`, and since commit `a317cba1b` on the branch `fix/gate-follow-ups` it exports `LENNY_GATE_LINT_CACHE_FILL=failed` when the golangci-lint cache fill does not finish, before it runs `lenny-test gate --here`.
- The tier runners in `cmd/lenny-test/cmd_run.go` start `go test` without setting `cmd.Env`, so every test inherits these variables.
- The review of `a317cba1b` found that `TestGateContainerScriptReportsAFillThatDidNotFinish` built its shell environment from `os.Environ()` and failed with `LENNY_GATE_LINT_CACHE_FILL=failed` inherited. The commit that closed that finding sets the variable to empty in the test.

## Gap

Any test that reads a `LENNY_GATE_*` variable, or builds a child environment from `os.Environ()`, can behave differently inside the gate than on a host, and the difference depends on how the gate's own setup went.

## Suggested resolution

Remove the `LENNY_GATE_*` variables that only the harness reads from the environment of the tiers' `go test` runs, and list by name any variable that a test reads on purpose.
