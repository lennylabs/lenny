---
proposal: 0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry
title: A failed session bind leaves a stale adapter slot registry entry
kind: fix
status: Reviewed
drafted-date: 2026-09-16
drafted-by: change-proposal
reviewed-date: 2026-09-24
reviewed-by: change-proposal
approved-date: 
approved-by: 
implemented-date: 
implemented-by: 
---

## Review history

On 2026-09-10, an adversarial review run executed two loops. The spec loop ran 3 rounds and converged, performing 1 full-pool sweep. The non-spec loop ran 7 rounds and converged, performing 3 full-pool sweeps. Across both loops, 32 findings were fixed.

On 2026-09-16, the proposal was revised by hand from the fifth determination recorded in `scratchpad/attempt-fence-determination.md`, after five determinations and eight adversarial rounds found the staged bind epoch self-defeating under its own latch rule. The revision replaces the epoch with a caller-minted per-attempt token compared inside the adapter's registry resolve step, adds an explicit `unconditional_teardown` flag to `Shutdown` so destruction requires an affirmative act, folds the started-entry refusal and the per-slot guard into the proposal's own lane, removes the pod-exclusion mechanism, and re-cuts the checklist as a dependency graph.

On 2026-09-16, an adversarial spec-loop review ran 6 rounds and did not converge, performing 1 full-pool sweep. Forty findings were fixed. The non-spec loop was not run. Findings the spec loop had not closed remain open.

On 2026-09-17, an adversarial review run executed two loops. The spec loop ran 14 rounds and converged, performing 3 full-pool sweeps. The non-spec loop ran 15 rounds and did not converge, performing 5 full-pool sweeps. Across both loops, 89 findings were fixed. Findings the non-spec loop had not closed remain open.

On 2026-09-20, an adversarial review run executed the spec loop. The spec loop ran 15 rounds and did not converge, performing 5 full-pool sweeps. Eleven findings were fixed. The non-spec loop was not run. Findings the spec loop had not closed remain open.

On 2026-09-21, an adversarial review run executed both loops. The spec loop ran 26 rounds and converged, performing 12 full-pool sweeps. The non-spec loop ran 13 rounds and did not converge, performing 5 full-pool sweeps. Across both loops, 12 findings were fixed. Findings the non-spec loop had not closed remain open. The run did not converge.

On 2026-09-22, an adversarial review run executed the non-spec loop. The spec loop was not run. The non-spec loop ran 16 rounds and did not converge, performing 5 full-pool sweeps. Ten findings were fixed. Findings the non-spec loop had not closed remain open. The run did not converge.

On 2026-09-23, an adversarial review run executed the non-spec loop. The spec loop was not run. The non-spec loop ran 4 rounds and did not converge, performing 1 full-pool sweep. Thirty-four findings were fixed. Findings the non-spec loop had not closed remain open. The run did not converge.

On 2026-09-24, an adversarial review run executed both loops. The spec loop ran 5 rounds and converged, performing 2 full-pool sweeps. The non-spec loop ran 3 rounds and converged, performing 1 full-pool sweep. Across both loops, 6 findings were fixed. The run converged.
