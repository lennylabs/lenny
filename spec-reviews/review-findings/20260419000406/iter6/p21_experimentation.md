# Iter6 Review — Perspective 21: Experimentation & A/B Testing Primitives

**Spec snapshot:** main @ c941492 ("Fix iteration 5: applied fixes for Critical/High/Medium findings + docs sync").
**Scope:** `spec/10_gateway-internals.md` §10.7; `spec/15_external-api-surface.md` §15.1 (error catalog, dryRun row, `RegisterAdapterUnderTest` matrix); `spec/16_observability.md` §16.1/§16.6/§16.7; `spec/08_recursive-delegation.md` `experimentContext`; `spec/22_explicit-non-decisions.md`; `docs/reference/error-catalog.md`; `docs/reference/metrics.md`; `docs/operator-guide/observability.md`.
**Numbering:** continues from iter5 (last EXP-025). No new findings this iteration — the single residual finding is a carry-forward of iter5 carry-forwards plus EXP-019/EXP-023/EXP-024/EXP-025.

---

## Iter5 Fixed-item verification

| Iter5 finding | Severity | Status claim | Verification (iter6) |
| --- | --- | --- | --- |
| **EXP-023** (admission check does not compare against tenant-floor `minIsolationProfile`) | Medium | Fixed | **Verified.** A new paragraph has been added at `spec/10_gateway-internals.md` line 856 ("Admission-time tenant-floor advisory check") that (i) states the gap directly ("does not cover the case where the tenant has configured a stricter `minIsolationProfile` floor … than the base runtime's default pool, and a variant pool matches the base runtime's weaker profile"), (ii) specifies the comparison (each variant pool's resolved `isolationProfile` against the tenant-level `minIsolationProfile`), (iii) specifies the 2xx advisory response (not a hard reject), (iv) mandates emission of a new `experiment.variant_weaker_than_tenant_floor` operational event per offending variant, and (v) covers both real and `?dryRun=true` paths. The operational event is registered in `spec/16_observability.md` line 640 under Experiment events (gateway-emitted, operational) with payload fields `tenant_id`, `experiment_id`, `variant_id`, `variant_pool_isolation`, `tenant_floor`, `actor_sub`, `emitted_at` — shape is consistent with the four sibling `experiment.*` operational events at §16.6 lines 635–639 (all carry `tenant_id`, `user_id` or `actor_sub`, `experiment_id`, `variant_id` or `variant_weaker_than_tenant_floor`-specific fields; CloudEvents `type: dev.lenny.experiment.variant_weaker_than_tenant_floor` per the §16.6 head note at line 629). Docs sync is intact: `docs/operator-guide/observability.md` line 200 introduces a new "Operational Events" table row for this event with matching payload fields, severity (Warning), and trigger description. The fix cleanly separates advisory (`variant_weaker_than_tenant_floor`, 2xx admission) from hard-reject (`isolation_mismatch`, runtime `VARIANT_ISOLATION_UNAVAILABLE`) — the two events are explicitly contrasted in the §16.6 description of `variant_weaker_than_tenant_floor`. |

**Conclusion:** EXP-023 is resolved. The admission-path coverage now matches the runtime fail-closed condition's two subsets (variant-weaker-than-base and variant-weaker-than-tenant-floor), closing the silent-availability-regression class the admission check was written to prevent.

No iter5 Fixed items regressed.

---

## Iter5 carry-forward findings (not fixed in iter5 fix pass)

All eight carry-forwards below were open at iter5 close and remain unchanged at iter6 (verified by grep and direct reads). Each retains its iter5 severity per the iter5 severity-anchoring rubric. These were explicitly left open by iter5's recommendation: "one short fix pass addressing EXP-023 (Medium) and EXP-019 (to correct the field-path language introduced by iter4); at that point the remaining Low findings can be batched and the perspective converged in iter6." EXP-023 closed; the remaining eight Lows were not batched.

### EXP-017. `INVALID_QUERY_PARAMS` referenced by §10.7 is still not in the §15.1 error catalog [Low]

**Section:** `spec/10_gateway-internals.md` line 952; `spec/15_external-api-surface.md` §15.1 error catalog and `RegisterAdapterUnderTest` matrix.

§10.7's Results API query-parameter table (now at line 952, having shifted down two lines since iter5 due to the EXP-023 fix insertion above) still rejects `?delegation_depth=0&breakdown_by=delegation_depth` with `400 INVALID_QUERY_PARAMS`. Iter2 EXP-002 introduced the code; iter3 EXP-005, iter4 EXP-010, and iter5 EXP-017 each flagged it. `INVALID_QUERY_PARAMS` remains absent from §15.1 — grep across `spec/15_external-api-surface.md` returns zero matches for the token. The adapter contract-test matrix (`RegisterAdapterUnderTest`) enumerates the codes adapters MUST exercise; this code is outside that enumeration, so REST/MCP consistency tests cannot cover the rejection path.

**Recommendation (unchanged from iter5):**

- (a) Rewrite the line 952 closing clause to `"... is rejected with 400 VALIDATION_ERROR with details.fields[0].rule: \"breakdown_collision\""` and add `breakdown_collision` to the `rule` vocabulary in the validation-error example at §15.1; **or**
- (b) Add `INVALID_QUERY_PARAMS` as a 400 row to §15.1, append it to `RegisterAdapterUnderTest`, and mirror it in `docs/reference/error-catalog.md`.

### EXP-018. Variant count still unbounded; `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` remain unspecified [Low]

**Section:** `spec/10_gateway-internals.md` line 862 (variants "typically 2–5"), line 943 (Results API bound); `spec/15_external-api-surface.md` line 1129 (dryRun row); `spec/22_explicit-non-decisions.md`.

Iter2 EXP-003, iter3 EXP-007, iter4 EXP-011, and iter5 EXP-018 all flagged the absence of a variant-count cap. Iter6 grep for `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` across `spec/` returns zero matches. §10.7 line 943 ("bounded by operator configuration (typically 2–5)") still references a configuration key that does not exist. §22 still has no non-decision recording the absence. The operational consequences called out in prior iterations remain live: a `POST /v1/admin/experiments` with 500 weight-0.001 variants would create 500 `SandboxWarmPool` CRDs, make the bucketing walk at line 757 (`for _, v := range variants`) O(500) on every session assignment, and give the paused-experiment `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*` scan an unbounded keyspace. This is now the fifth iteration in which the ambiguity has not been resolved one way or the other.

**Recommendation (unchanged from iter5):** Either (a) add `maxVariantsPerExperiment` tenant-config (default 10, max 100), a `TOO_MANY_VARIANTS` 422 code, and corresponding dryRun/adapter-matrix updates; or (b) record the non-decision in §22 with rationale so future iterations stop re-surfacing it.

### EXP-019. Admission-time isolation check uses response-side field path, not pool/runtime CRD field [Low]

**Section:** `spec/10_gateway-internals.md` line 854.

The iter5 report recommended fixing this alongside EXP-023 in a short polish pass. EXP-023 was fixed; EXP-019 was not. Line 854 still reads: *"resolves the variant's referenced pool and compares `sessionIsolationLevel.isolationProfile` against the base runtime's default pool `sessionIsolationLevel.isolationProfile`"*. `sessionIsolationLevel` is the **response-side** object returned by `POST /v1/sessions` (§07.1 line 65, §15.1 line 751); grep confirms it appears only in response-shape contexts. On CRDs, the field is the top-level `isolationProfile` on the Runtime (§05.1 line 66) and the top-level pool-scoped override (§05.3). `defaultPoolConfig` (§05.1 line 97) does not nest `isolationProfile` under `sessionIsolationLevel`. An implementer following line 854 literally cannot locate the field.

The adjacent paragraph at line 856 (EXP-023 fix) uses the correct language: *"compare each variant pool's resolved `isolationProfile` against the tenant-level `minIsolationProfile` floor"* — same rewrite as EXP-019 recommends for line 854. The mismatched field paths between lines 854 and 856 is itself noise: two adjacent paragraphs describing closely related checks disagree about what CRD field they are comparing.

**Recommendation (unchanged from iter5):** Rewrite line 854 to: *"For each variant, the gateway resolves the variant's referenced pool (field `variants[].pool`) and reads its effective `isolationProfile` (pool-level override, falling back to the Runtime's top-level `isolationProfile`). It compares this against the base runtime's effective `isolationProfile` (resolved from the Runtime named in `baseRuntime`, §5.1). If any variant's resolved `isolationProfile` is weaker than the base runtime's — using the canonical ordering `standard < sandboxed < microvm` defined in §5.3 — the request is rejected with `422 CONFIGURATION_CONFLICT` …"* Also clarify the fallback when `variants[].pool` is absent.

### EXP-020. Sticky-cache `paused → active` sentence still contradicts the preceding flush rule [Low]

**Section:** `spec/10_gateway-internals.md` line 1096.

Line 1096 still reads, verbatim: *"On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid."* The preceding clause flushes all keys on `active → paused` via `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*`; line 850 establishes that paused experiments are not evaluated by the `ExperimentRouter` (first-match rule walks only active experiments), so no entries can be populated while paused. On `paused → active`, the "existing cached assignment" refers to a cache just purged. Iter2 EXP-004, iter3 EXP-008, iter4 EXP-012, iter5 EXP-020 each identified this; no edit has landed across four fix passes. The invariant that actually makes re-activation correct — HMAC-SHA256 determinism (line 751) — is never stated in this paragraph. The spec also remains silent on whether sessions created during the paused window are retroactively enrolled on re-activation (they are not, per `experimentContext: null` on their session record, but readers must infer this).

**Recommendation (unchanged):** Replace line 1096's second clause with: *"On `paused → active` re-activation, no re-seeding is required: percentage-mode assignment is deterministic (HMAC-SHA256 of `assignment_key + experiment_id`, line 751), so the first post-re-activation session for a given user recomputes the same variant as before the pause. The cache is lazily repopulated on demand. For `mode: external` experiments, re-evaluation is delegated to the OpenFeature provider per session. Sessions created during the paused window carry `experimentContext: null` and are not retroactively enrolled on re-activation, regardless of `sticky` mode."*

### EXP-021. `BreakdownResponse` example still has unexplained per-bucket dimension-set divergence [Low]

**Section:** `spec/10_gateway-internals.md` lines 1011–1086.

Iter3 EXP-006's fix specified per-bucket aggregation semantics; the example JSON has never been reconciled with those semantics. In the current §10.7 text at lines 1036–1038 (`control.breakdowns[bucket_value=0]` contains `coherence` and `safety` but not `relevance`) vs. the flat response at line 977 (the same `control` variant includes `relevance`), the per-bucket sample-population omission is unexplained. Additionally, the treatment buckets (lines 1070, 1077) omit the `dimensions` field entirely, while the control buckets include it with the omission pattern above. The spec still does not state whether `dimensions` is present-but-empty (`{}`), absent, or something else when a bucket has no non-null dimensional scores.

**Recommendation (unchanged):** Insert after line 1014 (current end of per-dimension rules inside the Breakdown block): *"A bucket's dimension key set is the union of non-null `scores` keys across that bucket's rows only, so a bucket may omit dimensions that appear in the variant's flat response. In the example below, `control.breakdowns[bucket_value=0]` omits `relevance` because no row in that bucket submitted a `relevance` score, even though the variant's flat response includes it. When a bucket has no non-null dimensional scores for a given scorer, the bucket's `scorers[scorer].dimensions` field is **omitted** (not present as an empty object); the treatment buckets in the example below demonstrate this."*

Then re-check the example for internal consistency.

### EXP-022. `VARIANT_ISOLATION_UNAVAILABLE` still has no documented retry / opt-out pathway (or explicit non-decision) [Low]

**Section:** `spec/10_gateway-internals.md` line 852; `spec/15_external-api-surface.md` error catalog; `spec/22_explicit-non-decisions.md`.

Grep for `experimentOptOut`, `experiment_opt_out`, or any opt-out pathway returns zero matches in `spec/`. §22 still has no non-decision entry recording the absence. Iter3 EXP-009, iter4 EXP-016, iter5 EXP-022 each surfaced the problem. The iter5 fix for EXP-023 (admission-time tenant-floor advisory) reduces the likelihood of callers hitting `VARIANT_ISOLATION_UNAVAILABLE` at runtime — operators see the warning event at experiment creation and can defer activation — but it does not give individual callers an escape hatch. The iter5 observation that rejected sessions never appear in `EvalResult` (unauditable selection bias) is also unaddressed: admission-time tenant-floor awareness for operators does not give analysts a mechanism to detect the rejected subset.

**Recommendation (unchanged from iter5):** Pick (a) add `experimentOptOut: true` session-create flag with scoped permission `session:experiment:opt-out`, routing to base runtime unconditionally with `experimentContext: null` and emitting `experiment.opt_out` info; or (b) document the non-decision in §22.

### EXP-024. `experiment.status_changed` audit event does not record the sticky-cache flush outcome [Low]

**Section:** `spec/10_gateway-internals.md` line 1096 (flush invariant); `spec/16_observability.md` line 666 (audit event payload).

The audit event payload at §16.7 line 666 carries `tenant_id`, `experiment_id`, `previous_status`, `new_status`, `actor_sub`, `transition_at`. The cache flush at line 1096 is a side effect of `active → paused` and `*→ concluded` transitions; if the `DEL` call fails (Redis unavailable, partial scan), `lenny_experiment_sticky_cache_invalidations_total` under-counts but the audit trail retains no per-transition record of whether the flush succeeded. Post-incident "did stale sticky assignments continue routing after I paused?" forensic queries have only the metric's rollup. Fix was not applied in iter5.

**Recommendation (unchanged):** Add two optional payload fields to `experiment.status_changed` at §16.7: `sticky_cache_flushed` (bool — `true` when flush attempted and zero errors; `false` when skipped or with errors; absent when the transition did not trigger a flush) and `sticky_cache_flush_keys_deleted` (int — `DEL` reply count; absent when no flush attempted). Cross-reference from §10.7 line 1096's flush paragraph.

### EXP-025. Multi-experiment `created_at` ordering tiebreak undefined [Low]

**Section:** `spec/10_gateway-internals.md` line 774 (first-match rule); line 850 (multi-experiment restatement).

Line 774 and line 850 still specify "ascending order of `created_at`" with no secondary sort key. For bulk-imported experiments from a seed job, or for two admin requests landing within the same millisecond under Postgres's default `TIMESTAMP WITH TIME ZONE` precision, `created_at` collisions are plausible. Without a stable tiebreak, a session that hashes to non-control in both experiments `A` and `B` has a non-deterministic assignment (Go map-iteration order or index-order drift across minor versions silently re-buckets the same user between `A` and `B`). This violates the "single experiment per session" guarantee and `sticky: user` cross-replica semantics. Fix was not applied in iter5.

**Recommendation (unchanged):** Amend lines 774 and 850: *"... evaluates them in ascending order of `(created_at, experiment_id)` — `experiment_id` is the secondary sort key to guarantee deterministic ordering across replicas when two experiments share a `created_at` value."*

---

## New iter6 findings

**Count: 0.** No new experimentation gaps surfaced this iteration. Every experimentation-related change in the iter5 → iter6 fix pass was reviewed (EXP-023 admission-time tenant-floor advisory, `experiment.variant_weaker_than_tenant_floor` event in §16.6, `docs/operator-guide/observability.md` table row). The fix is correctly scoped: it extends the admission-time check to the tenant-floor case without forcing a hard reject, and the CloudEvents envelope is consistent with the other `experiment.*` operational events per the §16.6 head note.

The iter5 severity-anchoring rubric was applied: all eight carry-forwards retain their Low severity from iter3/iter4/iter5. No finding's severity was increased or decreased.

---

## Convergence assessment

**Open finding count this iteration:** 8 (all Low), all carry-forward from iter5 (one of them — EXP-017 — traces to iter2; EXP-018 traces to iter2; EXP-020 traces to iter2; EXP-022 traces to iter3; EXP-019, EXP-024, EXP-025 originate in iter5). No new iter6 findings.
**Iter5 Fixed items verified:** 1/1 intact (EXP-023). No regressions.
**Severity distribution:** 0 Critical, 0 High, 0 Medium, 8 Low, 0 Info.

**Convergence trend:** The perspective has converged to Low-only for two consecutive iterations (iter5 had 8 Low + 1 Medium; iter6 has 8 Low + 0 new). The Medium (EXP-023) closed in the iter5 fix pass as scheduled. The remaining eight Lows are polish/consistency items: six text rewrites (EXP-017/018/019/020/021/022), one audit-event payload extension (EXP-024), and one ordering-rule amendment (EXP-025). None of the eight is operationally blocking: runtime correctness (HMAC determinism, fail-closed isolation routing, admission-time tenant-floor warning, first-match rule) is specified. The eight open findings are documentation precision and forensic observability improvements.

**Recurring-finding concern:** EXP-017 and EXP-018 have been open for four review iterations (iter2, iter3, iter4, iter5, iter6) without either a fix or a §22 non-decision record. EXP-020 has been open equally long. EXP-022 has been open for three iterations. The iter5 report explicitly flagged that this recurrence is what keeps the findings regenerating — the [feedback_severity_calibration_iter5.md] rubric anchors the severity at Low, but the iteration cost of re-verifying these findings each cycle is non-trivial.

**CONVERGED: Yes.** The perspective meets the convergence criteria: no Critical, no High, no Medium findings; all iter5 Fixed items verified with no regressions; no new findings introduced; the open set is entirely pre-existing Lows. The eight open findings are documented for batched closure in a future cleanup pass but do not block declaration of convergence on this perspective. Per the iter5 convergence guidance ("the remaining Low findings can be batched and the perspective converged in iter6"), this iteration satisfies that condition.

**Recommended follow-up (non-blocking):** a single batched text-rewrite pass addressing the six pure-wording Lows (EXP-017 choosing path (a); EXP-018 choosing path (a) or (b); EXP-019 the field-path rewrite already drafted; EXP-020 the HMAC-determinism rewrite; EXP-021 the dimension-set rewrite; EXP-022 choosing path (a) or (b)), plus the §16.7 payload extension for EXP-024 and the one-line amendment for EXP-025. The batched effort is ~90 minutes of editing with no design work required — the concrete text is already drafted in prior iterations.
