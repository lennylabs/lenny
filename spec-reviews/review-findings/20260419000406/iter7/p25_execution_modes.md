# Iter7 Review — Perspective 25: Execution Modes & Concurrent Workloads

**Scope:** `executionMode` (session/task/concurrent) state-machine correctness, concurrent-workspace slot semantics, task-mode retirement policy, pool-scaling implications, cross-mode retry/retirement symmetry.

**Primary source files examined:**
- `spec/06_warm-pod-model.md` §6.2 (pod state machine; preConnect interactions; per-slot sub-states; concurrent-workspace pod lifecycle; pod-crash-during-active-task prose).
- `spec/05_runtime-registry-and-pool-model.md` §5.2 (Execution Modes, Task-mode pod retirement policy, Concurrent-workspace / concurrent-stateless sub-variants, Execution Mode Scaling Implications, `mode_factor` formula and Caveats).

**Iter6 baseline:** EXM-016, EXM-017, EXM-018 (all Low severity; all three carry-forwards from iter4 EXM-010/011/012 → iter5 EXM-013/014/015). None were addressed by the iter6 fix pass because that pass (`commit 8604ce9`) explicitly scoped itself to the 14 identified Critical/High/Medium findings per its commit message.

Severity rubric applied (iter7, anchored to iter1..iter6 per `feedback_severity_calibration_iter5.md`):
- **Critical/High:** concurrency-correctness bugs in session/task/concurrent modes — a violated invariant or a wrong outcome the spec contract cannot defend.
- **Medium:** incomplete mode specification with a documented workaround or a meaningful ambiguity that a careful reader cannot resolve without guessing.
- **Low/Info:** polish, guard symmetry, deployer-facing ambiguity where a prose clarification exists elsewhere in the spec or where the spec's intent is recoverable from adjacent paragraphs.

Verification performed this iter:
- Re-read §6.2 lines 141–158 (task-mode state-transition fragment, including `cancelled → task_cleanup`, `attached → failed`, `attached → resume_pending`) — text is **byte-identical** to the iter6 baseline. No iter6 fix touched these lines.
- Re-read §5.2 lines 447–471 (task-mode retirement policy + deployer acknowledgment block) — text is **byte-identical** to the iter6 baseline. No iter6 fix touched the retirement-policy bullet list.
- Re-read §5.2 lines 567–571 (Scaling-implications Caveats) — text is **byte-identical** to the iter6 baseline. No iter6 fix touched the `mode_factor` convergence clause.
- `git log` of `spec/05_runtime-registry-and-pool-model.md` and `spec/06_warm-pod-model.md` confirms the last touch was the iter4 fix pass (`commit 5c8c86a`). Iter5 and iter6 fix commits did not modify either file.

No new C/H/M-class findings surfaced on re-examination of §5.2 and §6.2 in this iter. EXM-016/017/018 re-raise verbatim at Low (no severity drift, per the iter5 calibration feedback and the pattern across iter4/iter5/iter6).

---

### EXM-019. `cancelled → task_cleanup` still does not define retirement-counter increment (iter4 EXM-010 → iter5 EXM-013 → iter6 EXM-016 persists) [Low]

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

A re-read of iter6 EXM-016 confirms the recommendation was never applied in the iter6 fix pass — that pass only addressed Critical/High/Medium findings (`commit 8604ce9`), and EXM-016 is Low. This is the third iteration in which the recommendation has persisted with identical text (EXM-010 → EXM-013 → EXM-016 → EXM-019).

**Recommendation:** Apply the iter6 EXM-016 fix verbatim. Add a companion sentence to §6.2 line 146 or (preferred for deployer discoverability) to the §5.2 retirement-policy bullet list:

> *"Cancelled tasks DO count toward `maxTasksPerPod` — a cancellation that reaches `task_cleanup` is equivalent to a completion for retirement-counter purposes, since scrub runs regardless of task outcome. Scrub failures during cancellation cleanup count toward `maxScrubFailures` identically to post-completion scrub. The preConnect re-warm rules (§6.2 lines 152–155) apply uniformly: a cancelled task on a preConnect pool routes through `sdk_connecting` if the standard guards pass, or through `draining` if the host node is unschedulable. `maxPodUptimeSeconds` evaluation also applies: a cancellation that exceeds the uptime limit during `task_cleanup` retires the pod on the same `idle_or_draining` branch as a natural completion."*

Explicit "DO count" vs "do NOT count" is a deployer-facing choice; either answer is defensible, but the spec must commit. The recommended answer ("DO count") is consistent with the existing §5.2:449 prose "since scrub runs regardless of task outcome" implicit rationale (cancelled tasks still touch the workspace before cancellation and still consume the pod's reuse cycle).

---

### EXM-020. Retirement-config-change staleness on `mode_factor` unaddressed (iter4 EXM-011 → iter5 EXM-014 → iter6 EXM-017 persists) [Low]

**Section:** `spec/05_runtime-registry-and-pool-model.md` §5.2 "Execution Mode Scaling Implications" → "Caveats" bullet, line 569 (task-mode `mode_factor` convergence clause).

Line 569 still reads verbatim:

> "For task mode, `mode_factor` is derived from observed reuse metrics and converges toward `maxTasksPerPod` over time. During cold start (no historical data), the controller falls back to `mode_factor = 1.0` (session-mode sizing) until sufficient samples are collected (default: 100 completed tasks). Once converged, `mode_factor` is bounded above by `maxTasksPerPod` (pods cannot serve more tasks than the configured limit)."

The bound "`mode_factor` is bounded above by `maxTasksPerPod`" is applied only at convergence — **not dynamically** on a deployer edit to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`. When a deployer tightens `maxTasksPerPod` from 50 → 10 (a security-posture hardening — the primary reason to edit this field), the PoolScalingController continues to size against a stale `mode_factor ≈ 50` until 100 fresh samples arrive, **under-provisioning the pool by up to 5×** relative to the new target. At low request rates, 100 samples is hours of exposure; during that window the pool's warm-pod headroom is proportionally insufficient and claim-driven cold-starts will surface.

The worst case is a security-driven tightening coinciding with a traffic burst: a deployer who discovers a residual-state vulnerability in task mode and responds by dropping `maxTasksPerPod` from 50 to 5 gets 5× worse warm-pool headroom for the duration of the re-convergence window. Neither Grep on the spec nor a line-by-line read of §5.2 shows any edit addressing this after iter6; the iter6 fix pass did not apply iter6 EXM-017.

A symmetric concern applies to `maxScrubFailures` (lowering the threshold makes early retirement more likely, which should reduce observed `mode_factor`) and `maxPodUptimeSeconds` (lowering it truncates long-lived pods' reuse, same direction). The current spec does not clamp `mode_factor` against any of these fields at config-change time.

**Recommendation:** Apply iter6 EXM-017's fix verbatim. Append to §5.2:569 (or as a dedicated "Config-change response" sentence immediately after the existing sentence):

> *"On deployer config changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`, the PoolScalingController immediately clamps `mode_factor ← min(mode_factor_current, maxTasksPerPod_new)` and resets the observed-sample window so subsequent pod cycles re-converge against the new retirement limits."*

Equivalent formulation (also acceptable and mechanically simpler): hard-clamp `mode_factor ≤ maxTasksPerPod` on **every** scaling evaluation (not only at convergence). Either formulation closes the staleness window without invalidating the 100-sample convergence mechanism for steady-state operation. The first formulation is preferable because it also resets the sample window, which avoids a separate staleness bug where old samples from the pre-change config continue to pull `mode_factor` up toward the old `maxTasksPerPod` for the duration of the rolling histogram.

---

### EXM-021. `attached → failed` transition still lacks symmetric retries-exhausted guard (iter4 EXM-012 → iter5 EXM-015 → iter6 EXM-018 persists) [Low]

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

Task-mode `attached → failed | resume_pending` (lines 144–145) is the **sole outlier** in the diagram where one side of a retry-split pair omits the predicate. Iter6 EXM-018 flagged this with a one-line recommendation; no edit landed in iter6's fix pass (Low-severity findings were not addressed).

**Recommendation:** Apply iter6 EXM-018's fix verbatim. Replace line 144 with:

```
attached ──→ failed                (pod crash / node failure / unrecoverable gRPC error during active task, retries exhausted or non-retryable)
```

This matches the symmetric-guard pattern already used on lines 102, 114, 118–119, 127, 131 of the same diagram, and eliminates the ambiguity without changing intended behavior.

---

## Prior-iteration carry-forwards

| iter4 | iter5 | iter6 | iter7 | Severity | Status |
|-------|-------|-------|-------|----------|--------|
| EXM-009 (scrub_warning preConnect schedulability precondition) | resolved (WPL-004 cascade; `§6.2:181` now carries unified rule) | still resolved | still resolved | Medium | Closed |
| EXM-010 | EXM-013 | EXM-016 | **EXM-019** | Low | Open |
| EXM-011 | EXM-014 | EXM-017 | **EXM-020** | Low | Open |
| EXM-012 | EXM-015 | EXM-018 | **EXM-021** | Low | Open |

All three open findings are iter4 persistences: their recommendations have been unchanged across iter4, iter5, iter6, and iter7. Severity has remained Low across all four iterations (no drift, per `feedback_severity_calibration_iter5`).

---

## New findings

None in iter7. Re-examination of §5.2 (execution modes, slot atomicity, scaling implications, concurrent-stateless, tenant pinning) and §6.2 (state machine, preConnect interactions, concurrent-workspace lifecycle, per-slot sub-states, `input_required`/`suspended`/`resuming` adjacencies) against the iter6 baseline surfaced no new Critical/High/Medium issues and no new Low issues that are not already captured by the three carry-forwards above.

Scan coverage performed this iter (no finding raised for each — recorded for audit trail):
- **Task-mode cleanup as "best-effort, not a security boundary"** (§5.2:434): The prose explicitly enumerates residual-state vectors (TCP `TIME_WAIT`, DNS cache, page cache, `inotify`, named pipes / UDS) and the Kata/microvm scrub variant with guest-VM restart is documented. `acknowledgeBestEffortScrub` is required and enforced at validation time. No new gap. Matches the must-check example in the P25 prompt.
- **Concurrent `concurrencyStyle: workspace` slotId multiplexing failure semantics** (§5.2:510–530): Per-slot failure isolation, slot-retry policy, whole-pod replacement trigger (`ceil(maxConcurrent/2)` fail-or-leak in 5-min window), and rehydration atomicity are all specified. The §6.2 state-machine fragment (lines 160–177) carries per-slot sub-states and `leaked` semantics are defined in §6.2:179. No new gap. Matches the must-check example.
- **Concurrent-stateless vs. "should just be a connector"** (§5.2:502–508): The spec explicitly recommends connectors as the preferred alternative and narrows `stateless` to "already-deployed Lenny pods with minimal statefulness". No new gap. Matches the must-check example.
- **Graph mode elimination** (§5.2:377): The spec states "Graph mode is removed as a separate concept — graph-aware runtimes are session-mode runtimes" and defers `lenny/emit_span` MCP tool to post-v1. No new gap. Matches the must-check example.
- **Execution mode interaction with warm pool strategy and pod lifecycle** (§5.2 "Execution Mode Scaling Implications"; §6.2 pod state machine): `mode_factor` and `burst_mode_factor` are defined per-mode; preConnect re-warm on `task_cleanup → sdk_connecting` carries a host-node-schedulable precondition that applies uniformly to scrub-success and scrub-warning edges (EXM-009 resolution intact); concurrent-workspace pods carry per-slot sub-states and a pod-level `slot_active` phase. The sole residual gap in this must-check area is EXM-020 (mode_factor staleness on config change), already raised. No new gap beyond the carry-forwards.

---

## Convergence assessment

**Perspective 25 is NOT converged in iter7,** but remains one iter from convergence, identical to the iter5 and iter6 assessments.

**Why not converged:** EXM-019, EXM-020, and EXM-021 all persist. Each is a one-line or one-sentence edit — none requires architectural change. The iter6 fix pass (`commit 8604ce9`) explicitly scoped itself to the 14 identified Critical/High/Medium findings and so did not apply these three Low fixes. This is the fourth consecutive iteration (iter4 → iter5 → iter6 → iter7) in which the same three Low recommendations have persisted with identical text and identical severity.

**Blockers to declaring convergence in iter8:**

1. Apply the three recommended fixes (or attach an explicit "accepted risk / will-not-fix" disposition to each). All three are one-edit fixes; none requires architectural change. Three specific edits:
   - §6.2:146 — add cancelled-counts-as-completed sentence (or add to §5.2 retirement-policy bullet list as preferred).
   - §5.2:569 — add config-change clamp-and-reset sentence to the existing Caveats bullet.
   - §6.2:144 — replace the unguarded `attached → failed` transition description with the explicitly-guarded `retries exhausted or non-retryable` variant.
2. No `docs/` reconciliation implications from these fixes — the changes are contained to §5.2 prose and §6.2 state-diagram guards, neither of which is mirrored in the user-facing `docs/` tree. Per `feedback_docs_sync_after_spec_changes`, a brief scan of any `docs/` execution-mode references should still be performed once the edits land, but nothing in this perspective's scope drives a cross-file sync at this iter. `docs/runbooks/` and `docs/operator-guide/` do not discuss the `cancelled → task_cleanup` edge, the `mode_factor` formula internals, or the state-machine guard format; the execution-mode user-facing surface is limited to Helm values and session-creation API contracts, neither of which changes under any of the three fixes.
3. No new regressions introduced this iter: EXM-009's iter5 resolution remains intact — `§6.2:181` still carries the unified "applies identically to the scrub-success and scrub-warning preConnect edges" rule; `§6.2:152` still carries the unschedulable `task_cleanup → draining` fallback for scrub-success; `§6.2:153` carries the same fallback for scrub-warning.

**If iter8 applies the three fixes without regression, perspective 25 converges.** If any is deferred again without explicit disposition, the same findings re-raise at Low in iter8 with no severity drift (per the iter5 severity-calibration guidance and the pattern established across iter4/iter5/iter6/iter7). Given the four-iteration persistence, an alternative to continued deferral is to attach a "will-not-fix / accepted risk" disposition to each of the three findings in the iter7 fix pass — this would remove them from the open ledger without requiring the spec edits, provided the disposition records the deployer-facing consequence (EXM-019: undocumented retirement-counter policy; EXM-020: up-to-5× under-provisioning window after retirement-config tightening; EXM-021: state-diagram guard asymmetry readable only with adjacent prose).
