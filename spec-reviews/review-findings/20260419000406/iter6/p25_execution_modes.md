# Iter6 Review — Perspective 25: Execution Modes & Concurrent Workloads

**Scope:** `executionMode` (session/task/concurrent) state-machine correctness, concurrent-workspace slot semantics, task-mode retirement policy, pool-scaling implications, cross-mode retry/retirement symmetry.

**Primary source files examined:**
- `spec/06_warm-pod-model.md` §6.2 (pod state machine; preConnect interactions; per-slot sub-states; concurrent-workspace pod lifecycle).
- `spec/05_runtime-registry-and-pool-model.md` §5.2 ("Pool Configuration and Execution Modes", "Task-mode pod retirement policy", "Execution Mode Scaling Implications", `mode_factor` formula and Caveats).

**Iter5 baseline:** EXM-013, EXM-014, EXM-015 (all Low severity; all three were iter4 persistences that were not addressed by the iter5 fix pass because the iter5 fix pass only covered Critical/High/Medium findings per `commit c941492`).

Severity rubric applied (iter6, anchored to iter5 per `feedback_severity_calibration_iter5`):
- **Critical/High:** concurrency-correctness bugs in session/task/concurrent modes.
- **Medium:** incomplete mode specification with a documented workaround.
- **Low/Info:** polish, guard symmetry, deployer-facing ambiguity where a prose clarification exists elsewhere in the spec.

No new C/H/M-class findings surfaced on re-examination of §5.2 and §6.2 in this iter. EXM-013/014/015 are re-verified against current spec text and re-raised at Low (no severity drift).

---

### EXM-016. `cancelled → task_cleanup` still does not define retirement-counter increment (iter4 EXM-010 → iter5 EXM-013 persists) [Low]

**Section:** `spec/06_warm-pod-model.md` §6.2 (line 146); `spec/05_runtime-registry-and-pool-model.md` §5.2 "Task-mode pod retirement policy" (lines 447–453).

Line 146 of §6.2 still reads verbatim:

```
cancelled ──→ task_cleanup         (cancellation acknowledged — pod runs scrub, then proceeds to idle or draining per normal task_cleanup rules)
```

Neither this arrow nor §5.2's retirement-policy bullet list states whether a cancelled task increments the pod's completed-task count for `maxTasksPerPod`. §5.2:449 specifies the retirement trigger as:

> "The pod's **completed** task count reaches `maxTasksPerPod`"

The literal reading (emphasised "completed") excludes cancellations, which would let a cancellation-heavy workload silently serve many more than `maxTasksPerPod` tasks per pod, defeating the explicit deployer reuse-limit choice that §5.2:471 describes as "forces deployer choice". The same ambiguity applies to three cascading sub-questions:

1. Whether scrub failures that occur during **cancellation cleanup** count toward `maxScrubFailures` (§5.2:451) — the retirement trigger is phrased "cumulative scrub failure count" without qualifying the triggering task outcome.
2. Whether the preConnect re-warm rules at lines 152–155 (both `task_cleanup → draining` eviction-fallback and `task_cleanup → sdk_connecting` re-warm) fire on the cancelled→task_cleanup path the same way they do on a natural completion.
3. Whether `uptime_limit` retirement (`maxPodUptimeSeconds`, §5.2:450) also applies on the cancellation path — the prose says "checked before assigning the next task" without clarifying that a cancellation-then-retirement cycle is "assigning the next task".

A re-read of iter5 EXM-013 confirms the recommendation was never applied in the iter5 fix pass — that pass only addressed Critical/High/Medium findings (`commit c941492`), and EXM-013 is Low.

**Recommendation:** Apply the iter5 EXM-013 fix verbatim. Add a companion sentence to §6.2 line 146 or (preferred for deployer discoverability) to the §5.2 retirement-policy bullet list:

> *"Cancelled tasks DO count toward `maxTasksPerPod` — a cancellation that reaches `task_cleanup` is equivalent to a completion for retirement-counter purposes, since scrub runs regardless of task outcome. Scrub failures during cancellation cleanup count toward `maxScrubFailures` identically to post-completion scrub. The preConnect re-warm rules (§6.2 lines 152–155) apply uniformly: a cancelled task on a preConnect pool routes through `sdk_connecting` if the standard guards pass, or through `draining` if the host node is unschedulable. `maxPodUptimeSeconds` evaluation also applies: a cancellation that exceeds the uptime limit during `task_cleanup` retires the pod on the same `idle_or_draining` branch as a natural completion."*

Explicit "DO count" vs "do NOT count" is a deployer-facing choice; either answer is defensible, but the spec must commit. The recommended answer ("DO count") is consistent with the existing §5.2:449 prose "since scrub runs regardless of task outcome" implicit rationale (cancelled tasks still touch the workspace before cancellation and still consume the pod's reuse cycle).

---

### EXM-017. Retirement-config-change staleness on `mode_factor` unaddressed (iter4 EXM-011 → iter5 EXM-014 persists) [Low]

**Section:** `spec/05_runtime-registry-and-pool-model.md` §5.2 "Execution Mode Scaling Implications" → "Caveats" bullet, line 569 (task-mode `mode_factor` convergence clause).

Line 569 still reads verbatim:

> "For task mode, `mode_factor` is derived from observed reuse metrics and converges toward `maxTasksPerPod` over time. During cold start (no historical data), the controller falls back to `mode_factor = 1.0` (session-mode sizing) until sufficient samples are collected (default: 100 completed tasks). Once converged, `mode_factor` is bounded above by `maxTasksPerPod` (pods cannot serve more tasks than the configured limit)."

The bound "`mode_factor` is bounded above by `maxTasksPerPod`" is applied only at convergence — **not dynamically** on a deployer edit to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`. When a deployer tightens `maxTasksPerPod` from 50 → 10 (a security-posture hardening — the primary reason to edit this field), the PoolScalingController continues to size against a stale `mode_factor ≈ 50` until 100 fresh samples arrive, **under-provisioning the pool by up to 5×** relative to the new target. At low request rates, 100 samples is hours of exposure; during that window the pool's warm-pod headroom is proportionally insufficient and claim-driven cold-starts will surface.

The worst case is a security-driven tightening coinciding with a traffic burst: a deployer who discovers a residual-state vulnerability in task mode and responds by dropping `maxTasksPerPod` from 50 to 5 gets 5× worse warm-pool headroom for the duration of the re-convergence window. Neither Grep on the spec nor a line-by-line read of §5.2 shows any edit addressing this after iter5; the iter5 fix pass did not apply iter5 EXM-014.

A symmetric concern applies to `maxScrubFailures` (lowering the threshold makes early retirement more likely, which should reduce observed `mode_factor`) and `maxPodUptimeSeconds` (lowering it truncates long-lived pods' reuse, same direction). The current spec does not clamp `mode_factor` against any of these fields at config-change time.

**Recommendation:** Apply iter5 EXM-014's fix verbatim. Append to §5.2:569 (or as a dedicated "Config-change response" sentence immediately after the existing sentence):

> *"On deployer config changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`, the PoolScalingController immediately clamps `mode_factor ← min(mode_factor_current, maxTasksPerPod_new)` and resets the observed-sample window so subsequent pod cycles re-converge against the new retirement limits."*

Equivalent formulation (also acceptable and mechanically simpler): hard-clamp `mode_factor ≤ maxTasksPerPod` on **every** scaling evaluation (not only at convergence). Either formulation closes the staleness window without invalidating the 100-sample convergence mechanism for steady-state operation. The first formulation is preferable because it also resets the sample window, which avoids a separate staleness bug where old samples from the pre-change config continue to pull `mode_factor` up toward the old `maxTasksPerPod` for the duration of the rolling histogram.

---

### EXM-018. `attached → failed` transition still lacks symmetric retries-exhausted guard (iter4 EXM-012 → iter5 EXM-015 persists) [Low]

**Section:** `spec/06_warm-pod-model.md` §6.2 "Task-mode state transitions (from attached)", lines 144 and 145.

The task-mode fragment still defines two transitions from `attached` with overlapping triggers and asymmetric guards (verbatim):

```
attached ──→ failed                (pod crash / node failure / unrecoverable gRPC error during active task)
attached ──→ resume_pending        (pod crash / gRPC error during active task, retryCount < maxTaskRetries)
```

Line 144 has **no guard**. Line 145 carries an explicit `retryCount < maxTaskRetries` guard. The prose at lines 185–190 ("Pod crash during active task-mode task") does establish that line 144 is the retries-exhausted / non-retryable branch and line 145 is the retries-remain branch, but the diagram itself is ambiguous without reading the follow-on prose.

Other fragments in the same §6.2 diagram consistently carry symmetric guards on both sides of a retry-split pair:
- Lines 102–103 `starting_session → failed | resume_pending` — both sides carry retry predicates.
- Lines 126–127 `input_required → resume_pending | failed` — both sides carry retry predicates.
- Lines 130–131 `resuming → resume_pending | awaiting_client_action` — both sides carry retry predicates.

Task-mode `attached → failed | resume_pending` (lines 144–145) is the **sole outlier** in the diagram where one side of a retry-split pair omits the predicate. Iter5 EXM-015 flagged this with a one-line recommendation; no edit landed in iter5's fix pass (Low-severity findings were not addressed).

**Recommendation:** Apply iter5 EXM-015's fix verbatim. Replace line 144 with:

```
attached ──→ failed                (pod crash / node failure / unrecoverable gRPC error during active task, retries exhausted or non-retryable)
```

This matches the symmetric-guard pattern already used on lines 102, 114, 118–119, 127, 131 of the same diagram, and eliminates the ambiguity without changing intended behavior.

---

## Carry-forward ledger

| iter3 | iter4 | iter5 | iter6 | Severity | Status |
|-------|-------|-------|-------|----------|--------|
| — | EXM-009 (scrub_warning preConnect schedulability precondition) | resolved (WPL-004 cascade; `§6.2:181` now carries unified rule) | still resolved | Medium | Closed |
| — | EXM-010 | EXM-013 | **EXM-016** | Low | Open |
| — | EXM-011 | EXM-014 | **EXM-017** | Low | Open |
| — | EXM-012 | EXM-015 | **EXM-018** | Low | Open |

All three open findings are iter4 persistences: their recommendations have been unchanged across iter4, iter5, and iter6. Severity has remained Low across all three iterations (no drift, per `feedback_severity_calibration_iter5`).

## Convergence assessment

**Perspective 25 is NOT converged in iter6,** but remains one iter from convergence, identical to the iter5 assessment.

**Why not converged:** EXM-016, EXM-017, and EXM-018 all persist. Each is a one-line or one-sentence edit — none requires architectural change. The iter5 fix pass (`commit c941492`) explicitly scoped itself to Critical/High/Medium findings and so did not apply these three Low fixes.

**Blockers to declaring convergence in iter7:**
1. Apply the three recommended fixes (or attach an explicit "accepted risk / will-not-fix" disposition to each). All three are one-edit fixes; none requires architectural change.
2. No `docs/` reconciliation implications from these fixes — the changes are contained to §5.2 prose and §6.2 state-diagram guards. Per `feedback_docs_sync_after_spec_changes`, a brief scan of any `docs/` execution-mode references should still be performed once the edits land, but nothing in this perspective's scope drives a cross-file sync at this iter.
3. No new regressions introduced: EXM-009's iter5 resolution remains intact — `§6.2:181` still carries the unified "applies identically to the scrub-success and scrub-warning preConnect edges" rule; `§6.2:152` still carries the unschedulable `task_cleanup → draining` fallback for scrub-success; `§6.2:153` carries the same fallback for scrub-warning.

**If iter7 applies the three fixes without regression, perspective 25 converges.** If any is deferred again without explicit disposition, the same findings re-raise at Low in iter7 with no severity drift (per the iter5 severity-calibration guidance and the pattern established across iter4/iter5/iter6).
