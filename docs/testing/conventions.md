---
layout: default
title: "Test conventions"
parent: "Testing"
nav_order: 1
description: Naming, annotations, table-driven form, determinism, cleanup, parallelism, and skipping rules every Lenny test follows.
---

# Test conventions

The full conventions live in [TESTING.md §17](../../TESTING.md). This page is the cheat sheet.

## Naming

Test functions follow `Test<Subject><Behavior>`. The Subject is a noun (the component or store under test). The Behavior is a sentence fragment in declarative voice.

```
TestSessionStateMachineRejectsStartFromCreated
TestQuotaCheckFiresAtEightyPercent
TestIdempotencyKeyReplaysCachedResponse
```

File names match the subject. `pkg/store/session/session.go` → `pkg/store/session/session_test.go`. Component-tier suites mirror the package path under `tests/tier2_component/`.

Build tags are mandatory above the unit tier:

| Tier | Tag |
|:-----|:----|
| 1 (unit) | _(none)_ |
| 2 (component) | `//go:build component` |
| 3 (contract) | `//go:build contract` |
| 4 (integration) | `//go:build integration` |
| 5 (e2e Kind) | `//go:build e2e_kind` |
| 6 (e2e cloud) | `//go:build e2e_cloud` |
| 7 (load) | `//go:build load` |
| 8 (chaos) | `//go:build chaos` |
| 9 (security) | `//go:build security` |
| 10 (conformance) | `//go:build conformance` |

## Annotations

Above every test function from tier-2 onward:

```go
// spec: 4.6.1 (warm pool controller — pod lifecycle), 12.3 (postgres ha requirements)
// diagnosis: ClaimSandbox returned a row already claimed by another goroutine.
//            Likely missing transaction isolation in pkg/controller/warmpool/claim.go,
//            or SELECT ... FOR UPDATE SKIP LOCKED applied to the wrong query.
func TestSandboxClaimSkipLocked(t *testing.T) { ... }
```

- `spec:` is mandatory on every test from Tier 2 onward. It lists every spec section the test encodes.
- `diagnosis:` is mandatory on every test from Tier 2 onward.
- The harness extracts both at compile time via `lenny-test validate-diagnosis`. Missing or malformed annotations fail Tier 0 lint.

Scaffold variant: tests that aren't yet implementing the assertion may carry the diagnosis inside a `t.Skip("not implemented: §X.Y — ...")` call instead. The validator recognises this form.

## Table-driven form

Tests with more than one input/output pair use table-driven form:

```go
func TestWorkspacePlanMode(t *testing.T) {
    cases := []struct {
        name     string
        mode     string
        wantCode string
    }{
        {"valid_644", "0644", ""},
        {"setuid_rejected", "4755", "WORKSPACE_PLAN_INVALID"},
        {"sticky_on_file_rejected", "1644", "WORKSPACE_PLAN_INVALID"},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) { ... })
    }
}
```

Each row is a subtest with a deterministic name. `go test -run TestWorkspacePlanMode/setuid_rejected` runs a single row.

## Determinism

- No `time.Now()` in tests. Use `testinfra/timectl`.
- No `crypto/rand` or `math/rand` directly in tests. Use `testinfra/randctl`.
- No naked `time.Sleep` in tests. Use `testinfra/wait.For` with an explicit timeout and a descriptive message.
- Goroutine leaks fail the test. Spawning goroutines? `defer testinfra/goleak.VerifyNone(t)`.
- No port hardcoding. Open ports via `testinfra/ports.NewListener(t)`.

## Cleanup

- Every test that creates a resource (a tenant, a session, a sandbox) registers a `t.Cleanup` that deletes it.
- Every test that allocates infrastructure (a schema, a Redis namespace, a MinIO bucket) registers a `t.Cleanup` that returns it.
- Cleanup is best-effort and silent on failure. The infrastructure layer reaps orphans on the next test run.

## Parallelism

- Tier 1 tests are `t.Parallel()` by default.
- Tier 2 component tests are `t.Parallel()` within a suite. Each test reserves its own schema or namespace.
- Tier 4 integration tests are serial by default within a suite. The compose stack has finite capacity.
- Tier 5 e2e and beyond are serial within a suite; parallelism is at the suite level managed by CI.

## Assertions

- Standard `testing` plus the `testinfra/assertions` package.
- No `testify` or `gomega`. The dependency surface stays narrow.

## Logging

- `t.Log` is for human debugging.
- Structured assertions emit machine-readable failure context that the verdict producer captures.
- No `fmt.Println` in tests.

## Skipping

- A test that requires unavailable infrastructure (no Kind, no gVisor, no cloud access) calls `t.Skip` with a reason that the harness records.
- A test gated by a phase calls `t.Skip` with reason `not-yet-applicable: phase-<N>: <message>`.
- The `*.SkipUnless<X>` helpers in `testinfra/*` are the canonical entry points.

## Flake budget

Tests must pass 50 consecutive runs. `lenny-test stress --test <Name> --runs 50` (or `--pattern '<regex>' --runs 50`) is the local check. A test that fails in this loop is quarantined and the owner is paged.

## Cryptography-convention identifiers

Use placeholder names from the cryptography convention when an example needs a human user. First user is `alice`, second `bob`, then `carol`, `dave`. Tenants are `acme`, `globex`, `initech`.

```go
"alice@acme.com"
"acme-default-runc"
"git@github.com:alice/repo.git"
```

The `testinfra/fixtures` package exports these as constants.
