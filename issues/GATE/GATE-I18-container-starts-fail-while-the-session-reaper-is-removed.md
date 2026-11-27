---
id: GATE-I18
status: open
kind: test-gap
severity: medium
requirements: []
---

# Container starts fail while the testcontainers reaper of the session is being removed

## Specification

None: the defect is in the test infrastructure, `tests/testinfra/containers`, and in the testcontainers-go module it uses. The issue is filed under `GATE`.

## Evidence

- Gate-main workflow run `46051812584`, the selected run on `main` at `1ce7a1941` (the merge of `change/0177.S3-S4-audit-tail-and-key-incidents`), 2026-11-27. Attempt 1 (gate run `96e1d28f305eb79a35d0e264`) failed 6 component tests: `TestOpsRollStartupHookDurable_spec_25_8_3508` in `cmd/lenny-ops`, and `TestVerifyAcceptsMigratedSchema`, `TestVerifyErasureGuardDetectsStrippedGuard`, `TestVerifyErasureRoleScopeDetectsNonErasureGrant`, `TestVerifyGrantsDetectsForbiddenLedgerGrant`, and `TestVerifyTriggersDetectsDisabledAndDropped` in `tests/component/auditintegrity`. Attempt 2 (gate run `0d0ff547a1dc9bbf362cf9f2`), a rerun of the whole job on the same commit, failed the same 6 and 32 tests in `tests/component/auditstore`, 38 in all. Every other test of the component tier passed, and so did the static, unit, contract, and integration tiers.
- Every failure is the same error at `StartPostgres`: `run postgres container: run postgres: generic container: create container: reaper: from container "<id>": wait for reaper <id>: detect internal port: check target: retries: 0, last err: unexpected container status "removing"`. All failures of one attempt name one reaper container, `1ffc6707` in attempt 1 and `5a1c5f46` in attempt 2. The tests that failed first in each package took 17 to 25 seconds, and the later tests of `tests/component/auditstore` failed in about half a second each, so the reaper was still in the `removing` state while the later tests ran.
- The component tier runs its selected packages in one `go test` invocation (`runScopedTier` in `cmd/lenny-test`). testcontainers-go derives the session ID from the PID and start time of the test binary's parent (`internal/core/bootstrap.go`), which is the go command, so every test binary of the invocation shares one session and one reaper container, named `reaper_<session ID>`. Ryuk exits once no client has been connected for its reconnection timeout, 10 seconds by default (`ryuk.reconnection.timeout`), and Docker removes the container, which testcontainers-go runs with auto-remove.
- In testcontainers-go v0.42.0, the version in `go.mod`, `reaperSpawner.lookupContainer` lists the reaper with `All: true` and accepts a container in any state, and `fromContainer` waits on it with `wait.ForExposedPort`, which fails at once on a container that is not running. `retryError` treats that error as final, so the start fails, and every later start of the binary fails the same way until Docker has removed the container. A reaper that is not running is otherwise detected only for the instance a binary already holds (`isRunning`), which returns a retryable not-found error.
- The upstream fix is testcontainers-go pull request 3868, "fix(reaper): recover from stopped, orphaned and unready reapers", merged on 2026-11-09 as commit `f31e98d4ee`, which closes upstream issues 3867 and 3918. Its description names this case: the test binaries of one `go test` invocation share a reaper, Ryuk exits in a gap with no client, and the next package that starts a container fails, the same package on each run because a CI runner schedules packages in nearly the same order. As of 2026-11-27 the newest release is v0.45.0, of 2026-11-05, and no release carries the fix (checked against the GitHub releases and the `reaper.go` of the v0.43.0, v0.44.0, and v0.45.0 tags).
- The previous selected run on `main`, `46037202709` on `07cad2fc0`, passed. The local gate run `a0151c2be38228e016c5b8d0` of the same branch on the owner's workstation passed the same packages. Whether a binary starts its first container in the window depends on when the go command finishes building it, so the failure depends on the selection and the host.
- Reproduction: `TestContainerStartsAfterTheSessionReaperStopped` in `tests/testinfra/containers/reaper_component_test.go` runs two child test processes of one session, plants a reaper container of that session in the `created` state, which testcontainers-go treats as it treats `removing`, and removes it after 3 seconds. Without the retry, the child's `StartRedis` fails at once with `reaper: from container "9349d650": wait for reaper 9349d650: detect internal port: check target: retries: 0, last err: unexpected container status "created"`. With the retry, the child starts Redis once the planted reaper is gone.
- GATE-I16 records Postgres start timeouts on the same runner, a separate failure. Its local run `1848ac5608074f4ac886208f` left 61 Postgres containers of one session running after the run, which is what a session whose reaper exited early leaves behind; its failing tests recorded no output, so the cause there is not confirmed.

## Gap

A component package whose test binary starts its first container while the shared reaper is being removed fails every test that starts a container, and the gate on `main` fails until a rerun schedules the packages differently. The branch `fix/testcontainers-reaper-removing` makes every starter of `tests/testinfra/containers` retry the start for up to two minutes while the error names a reaper that is not running (`startContainer` in `tests/testinfra/containers/reaper.go`). Containers that tests start through testcontainers-go directly, such as those in `tests/component/pgbouncerchart`, are outside the retry; they start after a `StartPostgres` of the same process, which leaves the process connected to a running reaper.

## Suggested resolution

When a testcontainers-go release carries pull request 3868, update `go.mod` to it, delete `startContainer`, `genericContainer`, and the stale-reaper matcher from `tests/testinfra/containers`, keep `TestContainerStartsAfterTheSessionReaperStopped` as the check that the release recovers, and close this issue.

## Coverage waiver

The merge of `fix/testcontainers-reaper-removing` relied on a coverage waiver: `lenny-test coverage --diff main --threshold 0.80 --require-gate` on gate rerun `aabbc90ced25cb98ef9b71f5` read 27 of 52 changed lines (51.9 percent), and the 25 uncovered lines are the call sites in the container starters of `tests/testinfra`, which only the component tests reach. The owner waived the floor for those lines on 2026-11-27 with the words "RF-W: waive" (M3 readiness decision page, item RF-W, answered in chat).
