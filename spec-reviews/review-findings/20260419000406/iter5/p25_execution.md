# Iter5 Review — Perspective 25: Execution Modes & Concurrent Workloads

**Scope:** `executionMode` (session/task/concurrent) state-machine correctness, concurrent-workspace slot semantics, task-mode retirement policy, pool-scaling implications.

**Primary source files examined:**
- `spec/06_warm-pod-model.md` §6.2 (pod state machine) and §6.1 (preConnect/SDK-warm interactions).
- `spec/05_runtime-registry-and-pool-model.md` §5.2 (Pool Configuration and Execution Modes), "Execution Mode Scaling Implications".

**Iter4 baseline:** EXM-009 through EXM-012.
- **EXM-009** (scrub_warning re-warm schedulability precondition, Medium) — **confirmed Fixed** via WPL-004 cascade. `spec/06_warm-pod-model.md:153` adds `task_cleanup → draining [scrub_warning]` fallback; `:155` carries `host node is schedulable` guard on `task_cleanup → sdk_connecting [scrub_warning]`; the `:181` "Host-node schedulability precondition" paragraph now explicitly states "The rule applies identically to the scrub-success and scrub-warning preConnect edges". No regression.
- **EXM-010, EXM-011, EXM-012** — remain unfixed in the current spec and are re-raised below at their iter4 severities (Low), per severity-calibration guidance.

Severity rubric applied (per iter5 instructions):
- **Critical/High:** concurrency-correctness bugs in workspace/stateless/task modes only.
- **Medium:** incomplete mode specification with a documented workaround.
- **Low/Info:** polish, guard symmetry, deployer-facing ambiguity where a prose clarification exists elsewhere in the spec.

No new C/H-class findings surfaced in this perspective's scope for iter5. The three persisting Low-severity items were re-verified against the current spec text.

---

### EXM-013. `cancelled → task_cleanup` transition still does not define retirement-counter increment (iter4 EXM-010 persists) [Low]

**Section:** `spec/06_warm-pod-model.md` §6.2 (line 146); `spec/05_runtime-registry-and-pool-model.md` §5.2 "Task-mode pod retirement policy" (lines 447–453).

Line 146 of §6.2 still reads: `cancelled ──→ task_cleanup (cancellation acknowledged — pod runs scrub, then proceeds to idle or draining per normal task_cleanup rules)`. Neither this arrow nor §5.2's retirement-policy bullet list states whether a cancelled task increments the pod's completed-task count for `maxTasksPerPod`. §5.2:449 specifies the retirement trigger as "The pod's **completed** task count reaches `maxTasksPerPod`" — the literal reading excludes cancellations, which would let a cancellation-heavy workload silently serve many more than `maxTasksPerPod` tasks per pod, defeating the explicit deployer reuse-limit choice that §5.2:471 describes as "forces deployer choice". The same ambiguity applies to whether scrub failures during cancellation cleanup count toward `maxScrubFailures`, and whether the preConnect re-warm rules at lines 152–155 fire on the cancelled→task_cleanup path the same way they do on a natural completion. Iter4 raised this as EXM-010 with an identical recommendation; no edit landed.

**Recommendation:** Apply the iter4 EXM-010 fix verbatim. Add a companion sentence to §6.2 line 146 or to the §5.2 retirement-policy bullet list: *"Cancelled tasks DO count toward `maxTasksPerPod` — a cancellation that reaches `task_cleanup` is equivalent to a completion for retirement-counter purposes, since scrub runs regardless of task outcome. Scrub failures during cancellation cleanup count toward `maxScrubFailures` identically to post-completion scrub. The preConnect re-warm rules (§6.2 lines 152–155) apply uniformly: a cancelled task on a preConnect pool routes through `sdk_connecting` if the standard guards pass."* Explicit "DO count" vs "do NOT count" is a deployer-facing choice; either answer is defensible, but the spec must commit.

---

### EXM-014. Retirement-config-change staleness on `mode_factor` unaddressed (iter4 EXM-011 persists) [Low]

**Section:** `spec/05_runtime-registry-and-pool-model.md` §5.2 "Execution Mode Scaling Implications" → "Caveats" bullet, lines 569 (task-mode `mode_factor` convergence) and 547 (formula assumption).

Line 569 still reads: "converges toward `maxTasksPerPod` over time … falls back to `mode_factor = 1.0` … until sufficient samples are collected (default: 100 completed tasks). Once converged, `mode_factor` is bounded above by `maxTasksPerPod`." The bound is applied only at convergence — not dynamically on a deployer edit to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`. When a deployer tightens `maxTasksPerPod` from 50 → 10 (a security-posture hardening, which is the primary reason to edit this field at all), the PoolScalingController continues to size against a stale `mode_factor ≈ 50` — underprovisioning the pool by up to 5× relative to the new target until 100 fresh samples arrive, which at low request rates is hours of exposure. The iter4 recommendation was not applied; line 569 is unchanged.

**Recommendation:** Apply iter4 EXM-011's fix. Add to §5.2:569 (or as a dedicated "Config-change response" sentence immediately after): *"On deployer config changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`, the PoolScalingController immediately clamps `mode_factor ← min(mode_factor_current, maxTasksPerPod_new)` and resets the observed-sample window so subsequent pod cycles re-converge against the new retirement limits."* Equivalently: hard-clamp `mode_factor ≤ maxTasksPerPod` on every scaling evaluation (not only at convergence). Either formulation closes the staleness window without invalidating the 100-sample convergence mechanism for steady-state operation.

---

### EXM-015. `attached → failed` transition still lacks symmetric retries-exhausted guard (iter4 EXM-012 persists) [Low]

**Section:** `spec/06_warm-pod-model.md` §6.2 "Task-mode state transitions (from attached)", lines 144 and 145.

The task-mode fragment still defines two transitions from `attached` with overlapping triggers and asymmetric guards:
- Line 144: `attached ──→ failed (pod crash / node failure / unrecoverable gRPC error during active task)` — no guard.
- Line 145: `attached ──→ resume_pending (pod crash / gRPC error during active task, retryCount < maxTaskRetries)` — explicit `retryCount < maxTaskRetries` guard.

The prose at lines 185–190 ("Pod crash during active task-mode task") clarifies that line 144 is the retries-exhausted / non-retryable branch and line 145 is the retries-remain branch, but the diagram itself is ambiguous without reading the follow-on prose. Other fragments in the same §6.2 diagram consistently carry symmetric guards on both sides of a retry-split pair (e.g., lines 102–103 for `starting_session`, lines 126–127 for `input_required`, lines 130–131 for `resuming`). Task-mode is the sole outlier. Iter4 EXM-012 flagged this with a one-line recommendation; no edit landed.

**Recommendation:** Apply iter4 EXM-012's fix verbatim. Replace line 144 with: `attached ──→ failed (pod crash / node failure / unrecoverable gRPC error during active task, retries exhausted or non-retryable)`. Matches the pattern used on lines 102, 114, 118–119, 127, 131, and eliminates the ambiguity without changing intended behavior.

---

## Convergence assessment

**Perspective 25 is one iter from convergence** for this scope. All three open findings (EXM-013/014/015) are iter4 persistences at Low severity with concrete, single-line or single-sentence recommendations that were not applied in iter4's fix pass. No new Critical/High/Medium findings surfaced on re-examination of §5.2 (execution modes, slot atomicity, scaling implications) or §6.2 (state machine, preConnect interactions, concurrent-workspace lifecycle). EXM-009 was resolved as a clean side-effect of WPL-004 (schedulability-label propagation to both scrub-success and scrub-warning edges) — the associated §6.2 paragraph at line 181 explicitly documents the uniform rule.

Blockers to declaring convergence on this perspective:
1. The three Low findings above must land a fix (or an explicit "accepted risk / will-not-fix" disposition). All three are one-edit fixes; none require architectural change.
2. No docs/ reconciliation implications from these fixes — the changes are contained to §5.2 prose and §6.2 state-diagram guards. Per `feedback_docs_sync_after_spec_changes`, a brief scan of any `docs/` execution-mode references should still be performed once the edits land, but nothing in this perspective's scope drives a cross-file sync at this iter.

If iter6 applies the three fixes without regression, perspective 25 converges. If any of them is deferred again without disposition, the same findings re-raise at Low in iter6 with no severity drift.
