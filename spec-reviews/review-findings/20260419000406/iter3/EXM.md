# Iter3 EXM Review

## Regression-check of iter2 fixes

**EXM-004 (concurrent-stateless `residualStateWarning`)** — fixed in `spec/07_session-lifecycle.md:73`. The `residualStateWarning` field is now `true` when `executionMode` is `task` (any scrub variant) or `concurrent` (either `concurrencyStyle`), and the description explicitly enumerates that concurrent-stateless residual state is strictly worse than concurrent-workspace (process space, network stack, `/tmp`, page cache shared across same-tenant concurrent requests with no per-request scrub). Clean fix; no regression.

**EXM-005 (preConnect task-mode `scrub_warning` re-warm)** — fixed across three files. `06_warm-pod-model.md:134` adds the new `task_cleanup → sdk_connecting [scrub_warning]` transition with the correct guard conditions, and the `preConnect re-warm on scrub_warning` paragraph (6.2:160) explicitly preserves the "all idle pods are SDK-warm" invariant for preConnect pools. `05_runtime-registry-and-pool-model.md:429` carries a consistent cross-reference with the new transition name. The `scrub_warning` annotation persisting through re-warm is stated in both files. Clean fix.

**EXM-006 (mode_factor bootstrap caveat)** — treated as LOW in iter2; the iter2 fix note discusses preConnect inter-task SDK re-warm adding to cycle time and recommending observed `lenny_task_reuse_count` p50. This addresses a different concern (preConnect overhead) than the one EXM-006 flagged (retirement-config-change guard). The config-change guard was not addressed, which is acceptable for a LOW severity finding but surfaces again as EXM-010 below.

## New findings

### EXM-007 `warn_within_budget` introduced in transition but never defined [MEDIUM]
**Files:** `06_warm-pod-model.md:133`

The new EXM-005 fix introduces the term `scrub_outcome ∈ {succeeded, warn_within_budget}` in the `task_cleanup → sdk_connecting` transition, but `warn_within_budget` is a neologism that appears exactly once in the entire spec and is not defined in 5.2, 6.2, or elsewhere. The preceding transition (6.2:134) uses the alternative phrasing "scrub fails with `onCleanupFailure: warn`, `maxScrubFailures` not reached, `maxTasksPerPod` not reached, `maxPodUptimeSeconds` not reached" — which is effectively what `warn_within_budget` means, but the compact token form is never tied back to this definition. Readers/implementers have no source for the set `{succeeded, warn_within_budget}` values. The analogous transition at 6.2:134 also uses the long-form phrasing.

**Recommendation:** Either (a) replace `scrub_outcome ∈ {succeeded, warn_within_budget}` in 6.2:133 with the long-form condition (consistent with 6.2:134): "scrub succeeds or scrub fails with `onCleanupFailure: warn` and `maxScrubFailures`/`maxTasksPerPod`/`maxPodUptimeSeconds` not reached"; or (b) add an explicit one-sentence definition in 6.2 or 5.2 introducing the `scrub_outcome` enum with values `{succeeded, warn_within_budget, failed, failed_over_budget}` and reference it consistently. Option (a) is simpler and matches the style used elsewhere.

---

### EXM-008 `node not draining` precondition introduced in new transition but undefined [MEDIUM]
**Files:** `06_warm-pod-model.md:133`

The EXM-005 fix adds a "node not draining" precondition to the `task_cleanup → sdk_connecting` transition, but this condition appears nowhere else in Section 6.2 or Section 5.2 and is not a precondition on the sibling transition at 6.2:134 (scrub_warning path). If a node-drain signal should suppress the re-warm (to avoid starting a 60s `sdk_connecting` phase when the pod will be evicted), the logic should apply uniformly to both successful-scrub and scrub-warning re-warm paths. Additionally, the SIGTERM/`sdk_connecting` behavior is already specified at 6.1:54 (adapter calls `DemoteSDK` on SIGTERM during `sdk_connecting`, then exits) — which means the existing SIGTERM handler already covers the eviction case, making the new "node not draining" precondition redundant and slightly inconsistent.

**Recommendation:** Remove "node not draining" from 6.2:133 and let the existing SIGTERM handling (6.1:54) cover the eviction case — a pod that enters `sdk_connecting` and then receives SIGTERM due to drain will cleanly demote and exit. If a pre-drain check is genuinely wanted (to avoid wasted re-warm work), add it to both 6.2:133 and 6.2:134 (it applies to scrub_warning re-warm too) and define the signal source (e.g., "WarmPoolController has observed `node.kubernetes.io/unschedulable` on the pod's node").

---

### EXM-009 `cancelled → task_cleanup` transition skips retirement-check semantics [LOW]
**Files:** `06_warm-pod-model.md:127`

6.2:127 adds `cancelled ──→ task_cleanup (cancellation acknowledged — pod runs scrub, then proceeds to idle or draining per normal task_cleanup rules)`. "Per normal task_cleanup rules" is ambiguous about how `retryCount`, the cancelled task's contribution to `maxTasksPerPod`, and preConnect re-warm apply. In particular: (1) does a cancelled task increment the pod's completed-task count toward `maxTasksPerPod`? (2) does a cancelled task on a preConnect pool still go through `sdk_connecting` before idle (if budget remaining) or skip directly to idle? Neither 5.2 nor 6.2 specifies this. A deployer with `maxTasksPerPod: 50` will observe different effective pod reuse depending on whether cancellations count — and cancelled tasks are the kind of workload where config clarity matters (e.g., chatty users, runaway sessions).

**Recommendation:** Clarify at 6.2:127 or in 5.2's task-mode retirement section: "Cancelled tasks DO count toward `maxTasksPerPod` (a scrub is performed regardless of task outcome), and the same preConnect re-warm rules apply as for successful-scrub or warn-within-budget outcomes." Also clarify that `maxScrubFailures` counting is orthogonal to task-level success/failure — only scrub outcome determines the count.

---

### EXM-010 Retirement-policy config-change staleness re-surfaces after EXM-006 partial fix [LOW]
**Files:** `05_runtime-registry-and-pool-model.md:554`

The EXM-006 iter2 fix at 5.2:554 adds language about preConnect inter-task SDK re-warm reducing `mode_factor`, but the original EXM-006 concern — that deployer changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds` leave the pool sized against a stale 100-sample histogram — was not addressed. When a deployer tightens `maxTasksPerPod` from 50 to 10, the converged `mode_factor ≈ 50` continues to under-provision the pool until the new samples dominate the histogram (which can take hours at low request rates). 5.2:554 says the converged `mode_factor` is "bounded above by `maxTasksPerPod`", but does not say whether this bound is applied dynamically to the already-computed metric or only to new samples.

**Recommendation:** Add a sentence to 5.2:554: "On deployer config changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`, the PoolScalingController immediately clamps `mode_factor ← min(mode_factor_current, maxTasksPerPod_new)` and resets the observed-sample window. This prevents under-provisioning when retirement limits tighten." Alternatively, hard-clamp `mode_factor ≤ maxTasksPerPod` on every evaluation (not just at convergence).

---

## Coverage

Regression-checked all three iter2 fix targets. Graph-mode elimination remains clean (no stray `executionMode: graph` references). Concurrent-stateless vs connector distinction (5.2:487-493) remains intact. Tenant pinning across all three execution modes (task, concurrent-workspace, concurrent-stateless) remains consistent. `preConnect` × `executionMode` compatibility table (6.1:58-66) correctly documents the four combinations. Concurrent-workspace `fail or leak` whole-pod replacement trigger remains consistent between 5.2:514 and 6.2:158.

No other regressions or missed mode issues found.
