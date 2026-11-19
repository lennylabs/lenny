---
id: GWCOORD-I1
status: open
kind: test-gap
severity: low
requirements: []
legacy: T-BED.33
---

# The co-location race test cannot tell a missed timing window from a fail-closed regression

## Specification

§10.1.2 (Coordinator Handoff Protocol): a replica that acquires the coordination lease runs the pre-CAS read, the generation increment, and the fence before it sends any operational RPC to the pod, and a request that lands on the new coordinator before the binding is published does not serve over the unpublished binding.

## Evidence

- `tests/load_local/coordination_colocation_race_test.go`, `TestColocationInvariantUnderConcurrentHandoff_spec_10_1`, runs a takeover sweep on one goroutine and request-path probes on others, and counts the probes that fail closed before the binding is published. At the end it reads `if fc == 0 {` and reports `no fail-closed observation recorded; the pre-publish window was not exercised`.
- The test injects a lease-acquire hook (`afterAcquire`) that drives the terminal-session case into the window between the adopt decision and the handoff bump. Nothing holds the live session's handoff in its pre-publish window, so whether a probe samples that window depends on host scheduling.
- Recorded on branch `theme-t-bed` as T-BED.33, and checked against `main` at `092ef92f6` on 2026-11-19.

## Gap

When the host schedules the takeover through the pre-publish window before any probe runs, the test fails with the same message a regression in the fail-closed guard produces. A failure therefore does not say whether the guard broke or the timing missed, and a pass on a fast host does not show that the window was exercised.

## Suggested resolution

Add a seam that blocks the handoff after the generation increment and the fence and before it publishes the binding, release it only after the probes have recorded at least one fail-closed observation, and keep the `fc == 0` assertion as the regression check. The test then exercises the pre-publish window on every run.
