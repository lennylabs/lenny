# Iter7 Review — Perspective 21: Experimentation & A/B Testing Primitives

**Spec snapshot:** main @ 8604ce9 ("Fix iteration 6: applied fixes for Critical/High/Medium findings + docs sync").
**Scope:** `spec/10_gateway-internals.md` §10.7; `spec/15_external-api-surface.md` §15.1/§15.4 (error catalog, dryRun, `RegisterAdapterUnderTest`); `spec/16_observability.md` §16.1/§16.5/§16.6/§16.7; `spec/08_recursive-delegation.md` `experimentContext`; `spec/22_explicit-non-decisions.md`; `docs/reference/error-catalog.md`; `docs/reference/cloudevents-catalog.md`; `docs/reference/metrics.md`; `docs/operator-guide/observability.md`.
**Numbering:** continues from iter6 (last EXP-025). This iteration opens EXP-026 and EXP-027.

---

## Iter6 Fix-pass verification

Iter6 declared P21 converged with zero new findings, only eight Low carry-forwards (EXP-017 through EXP-025). The iter6 → iter7 fix pass (commit `8604ce9`) was scoped to C/H/M findings from other perspectives. Consistent with iter6's explicit recommendation that the eight Lows "can be batched and the perspective converged in iter6", no experimentation-related text changes were made in the fix pass. Verified via `git diff c941492 HEAD -- spec/10_gateway-internals.md` (no diff) and `git diff c941492 HEAD -- spec/16_observability.md` (no experiment-related hunks).

**Ripple checks.** The iter6 fix pass did touch `spec/15_external-api-surface.md` (§15.2.1 `RegisterAdapterUnderTest` matrix split `GIT_CLONE_REF_UNRESOLVABLE` into two codes to close API-023). This edit does not regress any EXP carry-forward — `VARIANT_ISOLATION_UNAVAILABLE` remains in the matrix and no experiment-specific session-creation rejection codes were removed. `experiment.status_changed` and the five `experiment.*` operational events in §16.6 are unchanged. No iter6 Fixed items regressed into P21 scope.

---

## Prior-iteration carry-forward findings (open at iter6 close, unchanged at iter7)

All eight iter6 carry-forwards remain open. Each retains its iter5/iter6 severity per the `feedback_severity_calibration_iter5.md` anchoring rubric. Verified by re-reading the relevant spec text at HEAD.

### EXP-017. `INVALID_QUERY_PARAMS` referenced by §10.7 is still not in the §15.1 error catalog [Low]

**Section:** `spec/10_gateway-internals.md` line 952; `spec/15_external-api-surface.md` §15.1 error catalog and `RegisterAdapterUnderTest` matrix.

Unchanged from iter6. §10.7's Results API query-parameter table at line 952 still rejects `?delegation_depth=0&breakdown_by=delegation_depth` with `400 INVALID_QUERY_PARAMS`. Grep across `spec/15_external-api-surface.md` at HEAD returns zero matches for the token; the same applies to `docs/reference/error-catalog.md`. The adapter contract-test matrix (§15.2.1 `RegisterAdapterUnderTest`) enumerates the codes adapters MUST exercise; this code is outside that enumeration, so REST/MCP consistency tests cannot cover the rejection path. This is the sixth consecutive iteration the finding has remained open (iter2 EXP-002, iter3 EXP-005, iter4 EXP-010, iter5 EXP-017, iter6 EXP-017, iter7 EXP-017).

**Recommendation (unchanged from iter5/iter6):**

- (a) Rewrite the line 952 closing clause to `"... is rejected with 400 VALIDATION_ERROR with details.fields[0].rule: \"breakdown_collision\""` and add `breakdown_collision` to the `rule` vocabulary in the validation-error example at §15.1; **or**
- (b) Add `INVALID_QUERY_PARAMS` as a 400 row to §15.1, append it to `RegisterAdapterUnderTest`, and mirror it in `docs/reference/error-catalog.md`.

### EXP-018. Variant count still unbounded; `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` remain unspecified [Low]

**Section:** `spec/10_gateway-internals.md` line 862 (variants "typically 2–5"), line 943 (Results API bound); `spec/15_external-api-surface.md` line 1129 (dryRun row); `spec/22_explicit-non-decisions.md`.

Unchanged from iter6. §10.7 line 943 still references "bounded by operator configuration (typically 2–5)", referring to a configuration key that does not exist. Grep for `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` across `spec/` returns zero matches. §22 still contains no non-decision entry recording the absence. This is the sixth consecutive iteration the finding has been open (iter2 EXP-003, iter3 EXP-007, iter4 EXP-011, iter5 EXP-018, iter6 EXP-018, iter7 EXP-018).

The operational consequences called out in prior iterations remain live: a `POST /v1/admin/experiments` with 500 weight-0.001 variants would create 500 `SandboxWarmPool` CRDs, make the bucketing walk at line 757 (`for _, v := range variants`) O(500) on every session assignment, and give the paused-experiment `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*` scan an unbounded keyspace. The line 943 narrative ("typically 2–5") is also indirectly referenced at iter7 by EXP-026 below (cloudevents-catalog event-count implications) — if the variant count is truly unbounded, the cloudevents consumer has no cardinality budget for `variant_id` labels either.

**Recommendation (unchanged from iter5/iter6):** Either (a) add `maxVariantsPerExperiment` tenant-config (default 10, max 100), a `TOO_MANY_VARIANTS` 422 code, and corresponding dryRun/adapter-matrix updates; or (b) record the non-decision in §22 with rationale so future iterations stop re-surfacing it.

### EXP-019. Admission-time isolation check uses response-side field path, not pool/runtime CRD field [Low]

**Section:** `spec/10_gateway-internals.md` line 854.

Unchanged from iter6. Line 854 still reads: *"resolves the variant's referenced pool and compares `sessionIsolationLevel.isolationProfile` against the base runtime's default pool `sessionIsolationLevel.isolationProfile`"*. `sessionIsolationLevel` is the response-side object returned by `POST /v1/sessions` (§07.1 line 65, §15.1 line 751); grep confirms it appears only in response-shape contexts. On CRDs, the field is the top-level `isolationProfile` on the Runtime (§05.1 line 66) and the top-level pool-scoped override (§05.3). `defaultPoolConfig` (§05.1 line 97) does not nest `isolationProfile` under `sessionIsolationLevel`. An implementer following line 854 literally cannot locate the field.

The adjacent paragraph at line 856 (iter5 EXP-023 fix) already uses the correct language: *"compare each variant pool's resolved `isolationProfile` against the tenant-level `minIsolationProfile` floor"* — same rewrite EXP-019 has recommended three iterations running for line 854. The two adjacent paragraphs describing closely related checks continue to disagree about what CRD field they are comparing.

**Recommendation (unchanged from iter5/iter6):** Rewrite line 854 to: *"For each variant, the gateway resolves the variant's referenced pool (field `variants[].pool`) and reads its effective `isolationProfile` (pool-level override, falling back to the Runtime's top-level `isolationProfile`). It compares this against the base runtime's effective `isolationProfile` (resolved from the Runtime named in `baseRuntime`, §5.1). If any variant's resolved `isolationProfile` is weaker than the base runtime's — using the canonical ordering `standard < sandboxed < microvm` defined in §5.3 — the request is rejected with `422 CONFIGURATION_CONFLICT` …"* Also clarify the fallback when `variants[].pool` is absent.

### EXP-020. Sticky-cache `paused → active` sentence still contradicts the preceding flush rule [Low]

**Section:** `spec/10_gateway-internals.md` line 1096.

Unchanged from iter6. Line 1096 still reads, verbatim: *"On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid."* The preceding clause flushes all keys on `active → paused` via `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*`; line 850 establishes that paused experiments are not evaluated by the `ExperimentRouter` (first-match rule walks only active experiments), so no entries can be populated while paused. On `paused → active`, the "existing cached assignment" refers to a cache just purged. Iter2 EXP-004, iter3 EXP-008, iter4 EXP-012, iter5 EXP-020, iter6 EXP-020, iter7 EXP-020 each identified this; no edit has landed across five fix passes. The invariant that actually makes re-activation correct — HMAC-SHA256 determinism (line 751) — is never stated in this paragraph. The spec also remains silent on whether sessions created during the paused window are retroactively enrolled on re-activation (they are not, per `experimentContext: null` on their session record, but readers must infer this).

**Recommendation (unchanged):** Replace line 1096's second clause with: *"On `paused → active` re-activation, no re-seeding is required: percentage-mode assignment is deterministic (HMAC-SHA256 of `assignment_key + experiment_id`, line 751), so the first post-re-activation session for a given user recomputes the same variant as before the pause. The cache is lazily repopulated on demand. For `mode: external` experiments, re-evaluation is delegated to the OpenFeature provider per session. Sessions created during the paused window carry `experimentContext: null` and are not retroactively enrolled on re-activation, regardless of `sticky` mode."*

### EXP-021. `BreakdownResponse` example still has unexplained per-bucket dimension-set divergence [Low]

**Section:** `spec/10_gateway-internals.md` lines 1011–1086.

Unchanged from iter6. The example JSON at lines 1036–1038 (`control.breakdowns[bucket_value=0]` contains `coherence` and `safety` but not `relevance`) versus the flat response at line 977 (same `control` variant includes `relevance`) still has no per-bucket sample-population explanation. The treatment buckets (lines 1070, 1077) omit the `dimensions` field entirely, while the control buckets include it with the omission pattern above. The spec still does not state whether `dimensions` is present-but-empty (`{}`), absent, or something else when a bucket has no non-null dimensional scores.

**Recommendation (unchanged):** Insert after line 1014 (current end of per-dimension rules inside the Breakdown block): *"A bucket's dimension key set is the union of non-null `scores` keys across that bucket's rows only, so a bucket may omit dimensions that appear in the variant's flat response. In the example below, `control.breakdowns[bucket_value=0]` omits `relevance` because no row in that bucket submitted a `relevance` score, even though the variant's flat response includes it. When a bucket has no non-null dimensional scores for a given scorer, the bucket's `scorers[scorer].dimensions` field is **omitted** (not present as an empty object); the treatment buckets in the example below demonstrate this."* Then re-check the example for internal consistency.

### EXP-022. `VARIANT_ISOLATION_UNAVAILABLE` still has no documented retry / opt-out pathway (or explicit non-decision) [Low]

**Section:** `spec/10_gateway-internals.md` line 852; `spec/15_external-api-surface.md` error catalog; `spec/22_explicit-non-decisions.md`.

Unchanged from iter6. Grep for `experimentOptOut`, `experiment_opt_out`, or any opt-out pathway returns zero matches in `spec/`. §22 still has no non-decision entry recording the absence. Iter3 EXP-009, iter4 EXP-016, iter5 EXP-022, iter6 EXP-022, iter7 EXP-022 each surfaced the problem. The iter5 fix for EXP-023 (admission-time tenant-floor advisory) reduces the likelihood of callers hitting `VARIANT_ISOLATION_UNAVAILABLE` at runtime — operators see the warning event at experiment creation and can defer activation — but still does not give individual callers an escape hatch. The unauditable-selection-bias observation (rejected sessions never appear in `EvalResult`) is also unaddressed.

**Recommendation (unchanged from iter5/iter6):** Pick (a) add `experimentOptOut: true` session-create flag with scoped permission `session:experiment:opt-out`, routing to base runtime unconditionally with `experimentContext: null` and emitting `experiment.opt_out` info; or (b) document the non-decision in §22.

### EXP-024. `experiment.status_changed` audit event does not record the sticky-cache flush outcome [Low]

**Section:** `spec/10_gateway-internals.md` line 1096 (flush invariant); `spec/16_observability.md` line 670 (audit event payload).

Unchanged from iter6. The audit event payload at §16.7 line 670 carries `tenant_id`, `experiment_id`, `previous_status`, `new_status`, `actor_sub`, `transition_at`. The cache flush at line 1096 is a side effect of `active → paused` and `*→ concluded` transitions; if the `DEL` call fails (Redis unavailable, partial scan), `lenny_experiment_sticky_cache_invalidations_total` under-counts but the audit trail retains no per-transition record of whether the flush succeeded. Post-incident "did stale sticky assignments continue routing after I paused?" forensic queries have only the metric's rollup. Fix was not applied in iter5, iter6, or iter7 fix passes.

**Recommendation (unchanged):** Add two optional payload fields to `experiment.status_changed` at §16.7: `sticky_cache_flushed` (bool — `true` when flush attempted and zero errors; `false` when skipped or with errors; absent when the transition did not trigger a flush) and `sticky_cache_flush_keys_deleted` (int — `DEL` reply count; absent when no flush attempted). Cross-reference from §10.7 line 1096's flush paragraph.

### EXP-025. Multi-experiment `created_at` ordering tiebreak undefined [Low]

**Section:** `spec/10_gateway-internals.md` line 774 (first-match rule); line 850 (multi-experiment restatement).

Unchanged from iter6. Line 774 and line 850 still specify "ascending order of `created_at`" with no secondary sort key. For bulk-imported experiments from a seed job, or for two admin requests landing within the same millisecond under Postgres's default `TIMESTAMP WITH TIME ZONE` precision, `created_at` collisions are plausible. Without a stable tiebreak, a session that hashes to non-control in both experiments `A` and `B` has a non-deterministic assignment (Go map-iteration order or index-order drift across minor versions silently re-buckets the same user between `A` and `B`). This violates the "single experiment per session" guarantee and `sticky: user` cross-replica semantics. Fix was not applied in iter5, iter6, or iter7 fix passes.

**Recommendation (unchanged):** Amend lines 774 and 850: *"... evaluates them in ascending order of `(created_at, experiment_id)` — `experiment_id` is the secondary sort key to guarantee deterministic ordering across replicas when two experiments share a `created_at` value."*

---

## New iter7 findings

Two new docs-sync gaps surfaced on a focused re-read of deployer-facing reference docs against `spec/16_observability.md` §16.6. Both anchor to Low severity per the iter5 rubric: iter6 POL-033 (a directly analogous `docs/reference/cloudevents-catalog.md` drift for circuit-breaker payload fields) was Low, and `feedback_docs_sync_after_spec_changes.md` establishes that reference-doc drift of this kind is a docs-sync class, not a spec-contract defect. The `EXP-018`, `EXP-020`, `EXP-022` originals are also Low, and these new findings are of the same text-sync character — no runtime correctness is affected.

### EXP-026. `docs/reference/cloudevents-catalog.md` omits the entire `experiment.*` event family [Low]

**Section:** `docs/reference/cloudevents-catalog.md` (whole file — 41 `dev.lenny.*` entries, 0 experiment entries); `spec/16_observability.md` §16.6 lines 637–644 (Experiment events block, five operational events) and §16.7 line 670 (`experiment.status_changed` audit event).

The `docs/reference/cloudevents-catalog.md` is the deployer-facing CloudEvents contract — it enumerates every `type` value a SIEM, webhook consumer, or SSE subscriber can receive, with each event's `data` payload highlights. It lists 41 `dev.lenny.*` entries across Alerts, Pools and upgrades, Circuit breakers, Credentials, Sessions, Delegation, Backups and platform, and `lenny-ops`-emitted sections. It has **zero** entries for any of the six experiment events that SPEC §16.6/§16.7 declare the gateway emits:

- `experiment.unknown_variant_from_provider` (operational, warning) — §16.6 line 639
- `experiment.unknown_external_id` (operational, info) — §16.6 line 640
- `experiment.targeting_failed` (operational, warning) — §16.6 line 641
- `experiment.multi_eligible_skipped` (operational, info) — §16.6 line 642
- `experiment.isolation_mismatch` (operational, warning) — §16.6 line 643
- `experiment.variant_weaker_than_tenant_floor` (operational, warning) — §16.6 line 644 (added by iter5 EXP-023 fix)
- `experiment.status_changed` (audit) — §16.7 line 670

Grep for `experiment` in `docs/reference/cloudevents-catalog.md` returns zero matches; grep for `dev.lenny` returns 41 matches. This is the same docs-sync failure class as iter6 POL-033 (opener/closer payload-field drift for circuit-breaker events in the same file), but wider in scope: where POL-033 flagged two stale rows, EXP-026 flags six entirely missing rows. The consequence for deployers wiring CloudEvents consumers is identical: the public reference contract omits event types the platform actually emits, so receivers built against `cloudevents-catalog.md` will not schema-validate, filter on, or route the experiment events; SIEM rulesets indexed against this catalog will have blind spots for the experiment-platform audit trail.

This was not caught in earlier EXP reviews because iter4 EXP-013 scoped to `spec/16_observability.md` §16.6 (which was fixed in iter4 and re-verified in iter5/iter6) but not to the docs-side CloudEvents catalog. Iter6 POL-033 established precedent that `docs/reference/cloudevents-catalog.md` is in-scope for docs-sync, per `feedback_docs_sync_after_spec_changes.md`.

**Recommendation:** Add a new "Experiments" subsection to `docs/reference/cloudevents-catalog.md` between the current "Sessions" / "Delegation" sections (which together form the session-lifecycle family) and "Backups and platform". The subsection enumerates all six `dev.lenny.experiment.*` types with payload highlights matching §16.6/§16.7. Concretely:

```markdown
### Experiments

| `type` | Trigger | `data` highlights |
|---|---|---|
| `dev.lenny.experiment.unknown_variant_from_provider` | OpenFeature provider returns a `Variant` or `Value` the gateway cannot resolve to a registered variant ID; session falls back to `"control"` | `tenant_id`, `user_id`, `experiment_id`, `provider`, `raw_variant` |
| `dev.lenny.experiment.unknown_external_id` | Provider returns an experiment/flag ID not registered in Lenny's `mode: external` catalog; experiment is skipped | `tenant_id`, `user_id`, `provider`, `external_experiment_id` |
| `dev.lenny.experiment.targeting_failed` | OpenFeature client timeout, transport error, or `ErrorResolutionDetails`; no experiment assignment | `tenant_id`, `user_id`, `provider`, `error` |
| `dev.lenny.experiment.multi_eligible_skipped` | Under the first-match rule, an earlier-created experiment already assigned non-control, so later experiments are skipped | `tenant_id`, `user_id`, `enrolled_experiment_id`, `skipped_experiment_ids[]` |
| `dev.lenny.experiment.isolation_mismatch` | `ExperimentRouter` fails closed at runtime — variant pool `isolationProfile` weaker than session's `minIsolationProfile`; session rejected with `VARIANT_ISOLATION_UNAVAILABLE` | `tenant_id`, `user_id`, `experiment_id`, `variant_id`, `sessionMinIsolation`, `variantPoolIsolation` |
| `dev.lenny.experiment.variant_weaker_than_tenant_floor` | Admission-time advisory — a variant pool is weaker than the tenant `minIsolationProfile` floor; experiment is still creatable but future sessions at the floor will be rejected | `tenant_id`, `experiment_id`, `variant_id`, `variant_pool_isolation`, `tenant_floor`, `actor_sub`, `emitted_at` |
```

`experiment.status_changed` belongs in the "Audit-bearing events" discussion (it is a §16.7 audit event) — cross-reference from the Experiments subsection with a one-liner: "Every admin-initiated status transition (`active`↔`paused`, `*→concluded`) also emits `dev.lenny.experiment.status_changed` as an audit-bearing event — see Audit-bearing events below."

### EXP-027. `docs/operator-guide/observability.md` operational-events table lists only one of six gateway-emitted experiment events [Low]

**Section:** `docs/operator-guide/observability.md` lines 194–201 (Operational and audit events table); `spec/16_observability.md` §16.6 lines 637–644.

The "Operational and audit events" table in the operator-guide — introduced by the iter5 EXP-023 fix-pass docs sync — lists one experiment event (`experiment.variant_weaker_than_tenant_floor`, line 200) and one unrelated audit event (`circuit_breaker.state_changed`, line 201). Five experiment events remain absent from the operator-guide:

- `experiment.unknown_variant_from_provider` (warning)
- `experiment.unknown_external_id` (info)
- `experiment.targeting_failed` (warning)
- `experiment.multi_eligible_skipped` (info)
- `experiment.isolation_mismatch` (warning)

The iter5 EXP-023 fix added the tenant-floor advisory row as part of its scope ("introduces a new 'Operational Events' table row for this event"); however, the opportunity to include the other five experiment events that SPEC §16.6 already enumerated in earlier iterations (iter4 EXP-013 fix) was not taken. The table's opening paragraph at line 196 asserts it covers "admission-time advisory checks and security-salient state transitions" — `experiment.isolation_mismatch` (runtime fail-closed) and `experiment.targeting_failed` (external provider outage) are squarely security- and availability-salient, and their absence means operators walking the operator-guide alone will not know these events exist until SPEC §16.6 is reached.

This is the sibling surface of EXP-026: both `docs/reference/cloudevents-catalog.md` and `docs/operator-guide/observability.md` are deployer-facing; neither includes the iter4-finalized `experiment.*` event family. EXP-026 is the broader contract omission (no event from the family); EXP-027 is the operator-workflow documentation omission (only one of six).

**Recommendation:** Extend the `docs/operator-guide/observability.md` "Operational and audit events" table with a block of five new rows, one per missing event, matching the `(event, severity, when, key payload fields)` shape already used at lines 200–201. Exact text mirrors the SPEC §16.6 catalog entries and the EXP-026 recommended rows for `docs/reference/cloudevents-catalog.md` — consistent field names and severities across the two docs prevent future `opener`/`closer`-style drift (iter6 POL-033 pattern).

---

## Severity-anchoring note

Per `feedback_severity_calibration_iter5.md`, all eight carry-forward findings retain their iter5/iter6 Low severity. The two new findings (EXP-026, EXP-027) are Low because (i) they are docs-sync surfaces rather than spec-contract defects, (ii) the iter6 POL-033 precedent (same class of drift on the same `cloudevents-catalog.md` file) was Low, (iii) the `experiment.*` event payloads are defined normatively in SPEC §16.6/§16.7 which remains the source of truth, so a CloudEvents receiver written against the SPEC will be correct — the docs gap is a discoverability / deployer-experience defect, not a runtime-correctness one. No severity drift is introduced.

No new Critical, High, or Medium findings surfaced.

---

## Convergence assessment

**Open finding count this iteration:** 10 (all Low) — 8 carry-forward from iter6 (EXP-017, EXP-018, EXP-019, EXP-020, EXP-021, EXP-022, EXP-024, EXP-025) + 2 new (EXP-026, EXP-027).
**Iter6 Fixed items verified:** n/a (iter6 had no Fixed items for P21 — the perspective's last Medium closed in the iter5 fix pass per iter6 verification). No regressions introduced by the iter6 → iter7 fix pass (commit `8604ce9`), which did not touch §10.7 or the experiment blocks in §16.6/§16.7.
**Severity distribution:** 0 Critical, 0 High, 0 Medium, 10 Low, 0 Info.

**Convergence trend.** The perspective has now been Low-only for three consecutive iterations (iter5 opened EXP-023 Medium which closed in the iter5 fix pass; iter6 was Low-only carry-forward + 0 new; iter7 is Low-only carry-forward + 2 new docs-sync). Runtime correctness (HMAC determinism, fail-closed isolation routing, admission-time tenant-floor warning, first-match rule, `PoolScalingController` variant-pool teardown, cache flush on status transition) is fully specified. The ten open findings decompose as:

- **Five text rewrites that have been open for 3+ iterations** (EXP-017, EXP-018, EXP-019, EXP-020, EXP-022) — concrete replacement text already drafted; ~30 minutes of edit work in aggregate.
- **Two polish items from iter5** (EXP-021 dimension-set rewrite; EXP-024 audit-event payload extension; EXP-025 sort-tiebreak) — each is a single-paragraph or single-line edit.
- **Two new docs-sync rows** (EXP-026, EXP-027) — copy-paste from SPEC §16.6 to the two doc files, ~10 minutes.

**Recurring-finding concern (stronger than iter6).** EXP-017 and EXP-018 have now been open for **six consecutive review iterations** (iter2 → iter7) without either a fix landing or a §22 non-decision being recorded. EXP-020 has been open equally long. EXP-022 has been open for five iterations. Per `feedback_severity_calibration_iter5.md` the severity remains anchored at Low, but the re-verification cost each cycle is non-trivial and the finding continues to regenerate because neither closure path (fix text or explicit non-decision) has been chosen. The iter6 report flagged this; the iter7 report flags it more strongly: the fact that the iter6 fix pass did not touch these items, despite iter6's own recommendation that "the remaining Low findings can be batched and the perspective converged in iter6", means the iter6 convergence declaration was correct in the "no C/H/M" sense but the expected batched cleanup pass never materialised.

**CONVERGED: Yes (for C/H/M).** The perspective meets the iter5/iter6 convergence criteria: no Critical, no High, no Medium findings; iter6 items verified with no regressions; no new C/H/M findings introduced. Per the iter6 convergence guidance, the perspective remains declared converged.

**Recommended follow-up (non-blocking).** A single batched cleanup pass addressing the ten open Lows:
- EXP-017: pick path (a) or (b) from the recommendation.
- EXP-018: pick path (a) or (b); path (b) (record the non-decision in §22) is now preferred given six iterations of regeneration.
- EXP-019: rewrite §10.7 line 854 with the drafted text.
- EXP-020: rewrite §10.7 line 1096 with the drafted text.
- EXP-021: insert the drafted dimension-set rewrite at §10.7 line 1014 and align the example.
- EXP-022: pick path (a) or (b); path (b) (record the non-decision in §22) is now preferred given five iterations of regeneration.
- EXP-024: add two optional payload fields to `experiment.status_changed` at §16.7.
- EXP-025: amend §10.7 lines 774 and 850 with `(created_at, experiment_id)`.
- EXP-026: add the Experiments subsection to `docs/reference/cloudevents-catalog.md`.
- EXP-027: extend the `docs/operator-guide/observability.md` operational events table with five new rows.

Total editing effort ~100 minutes, no design work; all text is drafted in the recommendations above.
