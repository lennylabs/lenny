---
layout: default
title: "Flakiness"
parent: "Testing"
nav_order: 5
description: How to detect, diagnose, and quarantine flaky tests per §17.10 and §21.
---

# Flakiness

§17.10 sets the flake budget: every test must pass 50 consecutive runs. §21 defines the quarantine workflow and root-cause categories.

## Detecting a flake

The local check is `lenny-test stress`:

```bash
lenny-test stress --test TestSandboxClaim --runs 50
lenny-test stress --pattern 'TestSession.*' --runs 25
```

The harness runs `go test -count=1 -run <name>` once per iteration. It stops at the first failure (the §17.10 budget is zero tolerance) and prints the last 40 lines of stdout/stderr.

CI runs the sweep weekly via `.github/workflows/flake-budget.yml`:
- Cadence: Sundays 08:00 UTC
- Scope: `--pattern 'Test.*' --runs 50` against `./pkg/...`
- Output: appended to `$GITHUB_STEP_SUMMARY`; `stress.log` uploaded with 90-day retention

## Diagnosing the failure

When a test fails the 50-run check, classify the root cause per §21.2:

| Category | Symptoms | Likely fix |
|:---------|:---------|:-----------|
| `flaky-time` | Pass/fail correlated with wall clock or load | Replace `time.Now()` with `testinfra/timectl`; replace `time.Sleep` with `testinfra/wait.For` |
| `flaky-network` | DNS / port-bind failures, timing-out HTTP calls | Use `testinfra/ports.NewListener(t)`; add `wait.For` for readiness |
| `flaky-ordering` | Subtest depends on a sibling's side effect | Add `t.Parallel()` + `t.Cleanup`; isolate state per subtest |
| `flaky-resource` | Disk full, file handles exhausted | `t.TempDir()` for files; close every reader/writer |
| `flaky-goroutine` | Spurious panic, race detector fires | `defer testinfra/goleak.VerifyNone(t)`; review `pkg/.../...` for missing `defer Close()` |
| `genuine` | Real bug surfaced by the variance | Land the fix; the regression test that caught it stays |

Re-run with `-race` if not already on:

```bash
go test -race -count=10 -run TestSandboxClaim ./pkg/sandboxclaim/...
```

## Quarantine workflow

When stress fails and a fix is more than 30 minutes away:

1. Add the test to `tests/flake-budget.yaml` (gitignored history; create when needed):
   ```yaml
   quarantined:
     - test: TestSandboxClaim
       category: flaky-time
       owner: alice@acme.com
       opened: 2026-05-14
       issue: https://github.com/lennylabs/lenny/issues/123
   ```
2. Skip the test with `t.Skip("quarantined: <category>: see flake-budget.yaml")`.
3. File the GitHub issue with the stress.log artifact attached.
4. Owner has 7 days to land a fix or extend the quarantine with a new justification.

## Promoting the gate

`flake-budget.yml` is `continue-on-error: true` today — failures don't block the release. To promote:

1. Remove the `continue-on-error: true` from the stress step.
2. Ensure every weekly run stays clean for 4 consecutive weeks.
3. Update §17.10 to mark the gate as enforced.

## Common pitfalls

- **Subtests sharing state**: `for _, tc := range cases { t.Run(...) }` — the closure captures `tc`. Use the canonical fix:
  ```go
  for _, tc := range cases {
      tc := tc  // capture for the goroutine that t.Parallel() may create
      t.Run(tc.name, func(t *testing.T) {
          t.Parallel()
          // ...
      })
  }
  ```
- **Background goroutines**: `defer goleak.VerifyNone(t)` at the top of any test that spawns one.
- **TestMain teardown leaking state**: each test should set up everything it needs; `TestMain` is for compilation-time helpers only.
- **Determinism violations**: see `docs/testing/determinism.html` for the canonical `timectl` / `randctl` / `wait` / `ports` usage.
