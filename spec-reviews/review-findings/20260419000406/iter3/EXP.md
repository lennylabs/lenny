# Iter3 EXP Review

## Regression check — iter2 fixes

**EXP-002 (Results API filters):** Fixed correctly. `spec/10_gateway-internals.md` §10.7 now documents `delegation_depth`, `inherited`, `exclude_post_conclusion`, and `breakdown_by` query parameters with clear materialized-view bypass semantics and performance trade-offs. `spec/15_external-api-surface.md` line 613 cross-references the new params. Line 767 ("Sample contamination warning") now references the actual filter param names, so prescriptive guidance is reachable via the platform API.

**EXP-003 (no variant count cap):** NOT fixed. The iter2 commit message did not list EXP-003, and grep confirms no `maxVariantsPerExperiment` / `TOO_MANY_VARIANTS` appears anywhere in the spec. Line 840 still claims "bounded by operator configuration (typically 2–5)" but no such configuration field exists. Re-surfaced below.

**EXP-004 (paused sticky cache wording):** NOT fixed. Line 916 is unchanged — still says "On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid" despite the preceding sentence having flushed all entries on the `active → paused` transition. Re-surfaced below.

**EXP-001 (dimension aggregation, iter1):** Confirmed addressed — line 908 now documents per-dimension aggregation semantics and the selection-bias caveat for cross-dimension comparisons.

---

## New findings

### EXP-005 Results API filters reference undocumented error code `INVALID_QUERY_PARAMS` [Low]
**Files:** `spec/10_gateway-internals.md` §10.7 line 849; `spec/15_external-api-surface.md` §15.1 error catalog (lines 735, 816)

The iter2 EXP-002 fix introduces a mutual-exclusion rule between `?delegation_depth=…&breakdown_by=delegation_depth` and rejects it with "`400 INVALID_QUERY_PARAMS`". No such code exists in the §15.1 error catalog — it lists only `VALIDATION_ERROR` (400) for general query-parameter validation and family-specific codes like `INVALID_DELIVERY_VALUE`. A client encountering the rejection cannot look up the code, and the error-code-consistency integration test (§15.1 line 1089 — "All error classes … each exercised with a canonical triggering input") will not cover it.

**Recommendation:** Either (a) change line 849 to `"400 VALIDATION_ERROR with details.fields[0].rule: \"breakdown_collision\""` (reuses the existing catalog), or (b) add `INVALID_QUERY_PARAMS` as a new 400 row to the §15.1 error catalog and append it to the error-consistency test list at line 1089.

---

### EXP-006 Results API response shape undefined for `breakdown_by` requests [Medium]
**Files:** `spec/10_gateway-internals.md` §10.7 lines 849, 853–906

The `breakdown_by` query parameter is specified to "split" each variant bucket into sub-buckets ("one sub-aggregate per unique value"), but the JSON response example (lines 853–906) shows only the unfiltered shape with `variants[*].scorers[*].{mean, p50, p95, count, dimensions}`. The spec never states:

1. Whether the sub-aggregate is a new field (e.g., `variants[*].breakdown[delegation_depth][0].scorers[…]`) or replaces `scorers` directly with a keyed sub-object.
2. Whether `sample_count` is split per sub-bucket or stays at the variant level.
3. How clients enumerate sub-buckets when the splitting field is a `bool` (2 buckets) vs. `uint32` `delegation_depth` (unbounded — could be 0..N where N = tree depth).
4. Whether sub-buckets with zero records are omitted or returned with `count: 0`.

Without a concrete schema, implementations and client code will diverge. This is a real interop gap, not just documentation polish — the endpoint is admin-only but is consumed by dashboards and scripts that operators write against whatever the response happens to look like.

**Recommendation:** Add a second JSON example under §10.7 showing the broken-down shape, e.g. a `breakdown` sub-object keyed by the stringified field value:

```json
{
  "variant_id": "treatment",
  "sample_count": 45,
  "breakdown_field": "delegation_depth",
  "breakdown": {
    "0": { "sample_count": 30, "scorers": { … } },
    "1": { "sample_count": 12, "scorers": { … } },
    "2": { "sample_count": 3, "scorers": { … } }
  }
}
```

Explicitly state: (a) sub-bucket keys are stringified values of the breakdown field, (b) sub-buckets with zero records are omitted, (c) `sample_count` is recomputed per sub-bucket, and (d) `dimensions` aggregation rules (EXP-001 semantics) apply within each sub-bucket.

---

### EXP-007 Variant count still unbounded — iter2 EXP-003 unresolved [Low]
**Files:** `spec/10_gateway-internals.md` §10.7 lines 586–602, 840; `spec/15_external-api-surface.md` line 873

Re-surfacing iter2 EXP-003. The iter2 fix commit does not address variant count. Line 873 (`POST/PUT /v1/admin/experiments` dryRun description) validates only `Σ variant_weights ∈ [0, 1)`. Line 840 still claims bounding "by operator configuration" without citing such a config. An experiment with, e.g., 500 variants each weighted 0.001 would create 500 `SandboxWarmPool` CRDs, make bucketing O(500) on the session-create hot path, and make the paused-cache `DEL` scan unbounded.

**Recommendation:** As originally proposed — add an explicit `maxVariantsPerExperiment` tenant-config key (default 10) enforced at `POST/PUT /v1/admin/experiments` validation time with a new `TOO_MANY_VARIANTS` 422 code in §15.1. Update line 840's "typically 2–5" to cite the concrete default.

---

### EXP-008 Sticky cache wording still internally contradictory — iter2 EXP-004 unresolved [Low]
**Files:** `spec/10_gateway-internals.md` §10.7 line 916

Re-surfacing iter2 EXP-004. Line 916 still reads: *"On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid."* But the preceding clause has just described flushing all entries via `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*` on `active → paused`. After a flush no entries exist, and paused experiments are not evaluated by the `ExperimentRouter` (line 751 — router only walks active experiments), so the cache cannot be repopulated during the paused window. The "existing cached assignment remains valid" clause refers to a state that cannot exist. Correctness under `paused → active` relies on HMAC-SHA256 determinism (line 652), not cache persistence.

**Recommendation:** Rewrite line 916's second half to: *"On `paused → active` re-activation, no re-seeding is required: percentage-mode assignment is deterministic (HMAC-SHA256 of `assignment_key + experiment_id`, line 652), so the first post-re-activation session for a given user recomputes the same variant as before the pause. The cache is lazily repopulated on demand. For `mode: external` experiments, re-evaluation is delegated to the OpenFeature provider per session."* Also add a sentence stating that sessions created during the paused window have `experimentContext: null` and are not retroactively enrolled on re-activation, even under `sticky: session`.

---

### EXP-009 Isolation-mismatch fallthrough silently contaminates the control bucket [Medium]
**Files:** `spec/10_gateway-internals.md` §10.7 line 753, lines 604, 840

Line 753 states: when a variant pool's isolation profile is weaker than a session's `minIsolationProfile`, "the gateway falls through to the base runtime with no experiment assignment, and a `experiment.isolation_mismatch` warning event is emitted." But line 604 defines: "Sessions not assigned to any named variant run the `baseRuntime` and are assigned `variant_id: \"control\"` automatically by the gateway." So isolation-mismatched sessions that should have been enrolled in `treatment` instead silently land in the `control` bucket and their eval results aggregate into control's mean/p50/p95.

This creates an **asymmetric selection bias**: sessions with stricter isolation requirements (typically the higher-risk or more sensitive sessions) are always routed to control. Treatment vs. control comparisons therefore correlate isolation strictness with variant — any effect observed in the comparison could be driven by the workload population rather than the runtime change. Operators cannot distinguish a true variant effect from this bias because no Results API filter exists for "was this session an isolation-mismatch fallthrough."

The three Results API filters added in iter2 (`delegation_depth`, `inherited`, `exclude_post_conclusion`) do not cover this case — these sessions have `inherited: false`, `delegation_depth: 0`, `submitted_after_conclusion: false`.

**Recommendation:** Choose one of:
- **(A, preferred)** Exclude isolation-mismatch fallthroughs from the experiment entirely: set `experimentContext: null` (not `{variant_id: "control"}`), so eval results do not attribute to the experiment. This matches the "no experiment assignment" wording on line 753 and is consistent with how `variantPool unavailable` or `provider failure` (line 734) are handled (exclusion, not control).
- **(B)** Add a new `EvalResult.isolation_fallthrough` boolean column (mirroring `inherited`) and a matching Results API filter, so operators can exclude these rows.

Pick (A) unless there is a specific reason treatment-eligible-but-isolation-mismatched sessions should contribute to control aggregates. Document the chosen behavior explicitly.

---

## PARTIAL/SKIPPED

None. All areas in scope (pod-variant pools, deterministic routing, assignment, score storage, Results API, OpenFeature integration, variant filtering, statistical validity, rollout/rollback semantics) were reviewed. The iter2 fix addressed EXP-002 correctly but silently dropped EXP-003 and EXP-004, both re-surfaced here. The new findings (EXP-005, EXP-006, EXP-009) arose from close reading of the iter2 fix itself and from an area not previously reviewed (isolation fallthrough → control bucket).
