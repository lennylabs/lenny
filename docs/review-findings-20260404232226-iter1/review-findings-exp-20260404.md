# Experimentation & A/B Testing Primitives — Review Findings

**Spec:** `docs/technical-design.md`
**Perspective:** 21. Experimentation & A/B Testing Primitives
**Date:** 2026-04-04
**Category code:** EXP

---

## Summary

Section 10.7 is one of the more self-contained sections in the spec and its core design is sound: eval result attribution through delegation, gateway auto-association, and the explicit non-goals boundary are well-thought-out. However, seven gaps were found ranging from high to low severity. The most significant are: (1) the PoolScalingController has no specified behavior when an experiment is paused or concluded — variant pools that should wind down are left in an indeterminate state; (2) the `cohort` targeting mode references an "explicit whitelist" with no schema definition for how that whitelist is supplied; (3) there are no platform metrics for experiment assignment routing, making experiment observability a blind spot; and (4) the control group's `variant_id` value (used in Results API responses) is never defined — the example shows `"control"` as a string but the ExperimentDefinition schema never names it. These gaps would require implementers to make undocumented design decisions.

---

## Findings

### EXP-001 PoolScalingController Variant Pool Behavior on Experiment Pause/Conclude Is Unspecified [High]
**Section:** 4.6.2, 10.7

Section 4.6.2 states the PoolScalingController "integrates with experiment definitions to automatically size variant pools" and Section 10.7 confirms variant warm counts are "derived from base pool demand signals × variant weight × safety factor." However, the spec never describes what the PoolScalingController does when an experiment transitions to `paused` or `concluded`. Does it immediately set the variant pool's `minWarm` to 0? Does it let sessions drain and then scale down? Does it delete the `SandboxTemplate` CRD? Does the variant pool persist indefinitely, burning warm pod capacity? The "pool phases: pre-warm, ramp, steady state, wind-down — all automatic" note in Section 4.6.2 is generic and does not address experiment-lifecycle-driven teardown. This is a direct cost and operational waste concern: concluded experiments whose variant pools remain at full `minWarm` silently consume cluster capacity.

**Recommendation:** Add a subsection to 10.7 (and reference it from 4.6.2) specifying the PoolScalingController's behavior for each status transition:
- `active → paused`: PoolScalingController sets variant pool `minWarm` to 0; existing warm pods are not pre-terminated; the pool remains available for in-flight sessions that were already assigned the variant, but no new warm pods are created.
- `active/paused → concluded`: PoolScalingController sets variant pool `minWarm` to 0 and `maxWarm` to 0, triggering full drain. Once `status.readyCount == 0`, the PoolScalingController deletes the variant's `SandboxWarmPool` (but does not delete the `SandboxTemplate`, which may be shared with other experiments or baseline pools). Deletion follows the same safe-rotation sequence as Section 13.1.
- On re-activation (`paused → active`): PoolScalingController restores `minWarm` from the experiment definition.

---

### EXP-002 Control Group `variant_id` Value Is Never Defined [High]
**Section:** 10.7

The ExperimentDefinition YAML schema defines only treatment variants explicitly. The `variants` list in the example contains only `id: treatment`. The control group — sessions routed to the `baseRuntime` — is implicit. Yet the Results API response example at line 2834 shows `"variant_id": "control"` as a literal string value, and the `EvalResult` schema includes a `variant_id` field that is described as "auto-populated by gateway from session's experiment context." There is no statement in the spec that defines: what string value is assigned as `variant_id` for sessions enrolled in the control group; whether `control` is a reserved keyword; whether deployers can name their control group something else; or what happens if a deployer creates an explicit variant with `id: "control"`. The Results API and EvalResult store would behave differently depending on undocumented implementation choices.

**Recommendation:** Add a normative statement to Section 10.7 immediately after the ExperimentDefinition schema: "Sessions not assigned to any named variant run the `baseRuntime` and are assigned `variant_id: 'control'` automatically by the gateway. `'control'` is a reserved variant identifier — deployers cannot define a variant with `id: 'control'` in the `variants` list (enforced at experiment creation via `POST /v1/admin/experiments` validation). The control group's `minWarm` is not managed by experiment machinery; it falls through to the base runtime's existing pool." Also add `RESERVED_IDENTIFIER` as an error code for this case.

---

### EXP-003 Cohort Targeting Mode Has No Schema Definition [High]
**Section:** 10.7

Section 10.7 defines three targeting modes — `percentage`, `cohort`, and `combined` — but only `percentage` is mechanically described ("deterministic hash"). The `cohort` mode is described only as "explicit whitelist" and `combined` as "percentage within cohort." No schema is specified for how the whitelist is represented in the `ExperimentDefinition`: Is it an inline list of user IDs? A reference to an external resource? A tag/attribute match expression? What is the maximum cohort size? How does the gateway resolve whether a given user is in the cohort at request time — does it load the full list into memory, or is it a set lookup in Redis/Postgres? What happens if a user in the cohort also matches a different active `percentage` experiment? This is a blocking gap for any implementation of the cohort targeting path.

**Recommendation:** Extend the ExperimentDefinition schema in Section 10.7 with a concrete `targeting` block for each mode. For `cohort`: define a `cohort.userIds` field (list of strings, max 10,000 entries enforced at validation) stored in Postgres alongside the experiment record, loaded into a per-experiment Redis Set by the PoolScalingController on experiment activation, and checked via `SISMEMBER` at assignment time. Document the maximum cohort size and that the gateway enforces it at `POST /v1/admin/experiments`. For `combined`: specify that `percentage` is evaluated first within the cohort population.

---

### EXP-004 No Platform Metrics for Experiment Assignment Routing [Medium]
**Section:** 10.7, 16.1

Section 16.1 contains no metrics for the `ExperimentRouter` interceptor. There is no counter for experiment assignments by variant, no counter for assignment skips (user not eligible, experiment paused), no counter for targeting evaluation errors, and no histogram for assignment latency. Without these, operators cannot observe: what fraction of sessions are being routed to each variant; whether the variant weight (10% in the example) is being honored at the actual assignment rate; whether the `ExperimentRouter` is adding measurable latency to session creation; or whether targeting evaluation is failing silently. This also means there is no way to detect a misconfigured experiment that is routing 0% or 100% of sessions to a variant instead of the intended weight.

**Recommendation:** Add to Section 16.1: `lenny_experiment_assignment_total` (counter, labels: `experiment_id`, `variant_id`) counting assignments including `variant_id: "control"` for the control group; `lenny_experiment_assignment_skip_total` (counter, labels: `experiment_id`, `reason`) for ineligible or paused-experiment skips; `lenny_experiment_router_duration_seconds` (histogram) for interceptor evaluation latency. Add a `ExperimentVariantWeightDrift` warning alert to Section 16.5 that fires when the observed assignment ratio deviates from the configured weight by more than 5 percentage points over a 10-minute window.

---

### EXP-005 Eval Submission Has No Authorization Model or Rate Limit Specification [Medium]
**Section:** 10.7

`POST /v1/sessions/{id}/eval` is listed in the API surface (Section 15) but the spec does not define: who is authorized to submit evals against a session (the session owner? any authenticated tenant member? an admin-only operation?); whether evals can be submitted against sessions in terminal states (completed, failed, expired); whether there is a per-session limit on the number of eval submissions; or whether there is a rate limit on eval submissions globally or per tenant. Because `EvalResult` rows are aggregated into the Results API response, unrestricted or unauthenticated eval submissions from any party could corrupt experiment outcome data. A compromised or misbehaving client could inflate or deflate scores for any session it can reference, skewing experiment conclusions without the system detecting it.

**Recommendation:** Add an authorization and constraint subsection for `POST /v1/sessions/{id}/eval` in Section 10.7:
- Authorization: callers must be authenticated and have the `session:eval_submit` permission scoped to the session's tenant. Session ownership is not required (to allow external evaluator services), but tenant membership is.
- State constraint: eval submissions are accepted for sessions in any state including terminal states, within a configurable post-session window (`evalSubmissionWindowSeconds`, default: 86400 — 24 hours after `terminated_at`). After the window, submissions are rejected with `EVAL_SUBMISSION_EXPIRED`.
- Rate limits: per-session limit of 1000 eval records; per-tenant rate limit of 10,000 eval submissions per minute (configurable).
- Score validation: `score` must be in [0.0, 1.0] if provided; `scores` values must each be in [0.0, 1.0]; both cannot be absent. Violated submissions return `VALIDATION_ERROR`.

---

### EXP-006 Concurrent Active Experiments on Overlapping Runtimes Not Addressed [Medium]
**Section:** 10.7

The spec does not address whether multiple active experiments can target the same `baseRuntime` simultaneously. If experiment A routes 10% of `claude-worker` sessions to `claude-worker-v2` and experiment B simultaneously routes 20% of `claude-worker` sessions to `claude-worker-v3`, three pools need warm pods: the base `claude-worker` pool (serving 70% of sessions), the `v2` pool (10%), and the `v3` pool (20%). The PoolScalingController formula at Section 4.6.2 applies `variant_weight` per variant, but it is unclear how demand signals are shared across experiments competing for the same base pool demand. More critically, the ExperimentRouter's priority/ordering when a session is eligible for multiple active experiments is undefined — which experiment wins assignment, and does the other experiment's `ExperimentRouter` evaluation still count the session as "evaluated" for its targeting?

**Recommendation:** Add a paragraph to Section 10.7 specifying: (1) Multiple active experiments may target the same `baseRuntime`. The ExperimentRouter evaluates experiments in deterministic order (e.g., by experiment `created_at` ascending). A session is assigned to at most one experiment; once assigned, subsequent experiment evaluations are skipped for that session. (2) The PoolScalingController computes variant pool demand independently per experiment — base pool demand is not double-counted. Specify that the PoolScalingController validates at experiment creation that the sum of all active `variant_weight` values across all experiments targeting the same `baseRuntime` does not exceed 100% (returning `EXPERIMENT_WEIGHT_CONFLICT` if it does), so the base pool always retains a positive traffic fraction.

---

### EXP-007 `ExperimentDefinition` DELETE Semantics Are Undefined for Active Experiments [Low]
**Section:** 10.7, 15.1

The admin API table lists `DELETE /v1/admin/experiments` as a supported method, and concluded experiments are described as "immutable." However, the spec does not specify whether an `active` or `paused` experiment can be deleted directly (without first transitioning to `concluded`), and if so, what happens to: in-flight sessions currently enrolled in the experiment; `EvalResult` rows referencing the now-deleted `experiment_id`; and the variant pools that were being managed by the PoolScalingController for this experiment. Allowing deletion of an active experiment without a forced `concluded` transition would leave orphaned pool resources and break any Results API query that references the deleted experiment ID.

**Recommendation:** Add a deletion constraint to Section 10.7: `DELETE /v1/admin/experiments/{id}` is only permitted when the experiment is in `concluded` status. Attempts to delete an `active` or `paused` experiment return `409 CONFLICT` with error code `EXPERIMENT_NOT_CONCLUDED`. `EvalResult` rows referencing a deleted experiment retain their `experiment_id` foreign key (nullable after delete, or soft-delete the experiment record with a `deleted_at` column) so historical data is not destroyed. Document data retention behavior for experiment records explicitly: concluded experiments are retained indefinitely by default (configurable via a `retainExperimentResults` Helm value).

---
