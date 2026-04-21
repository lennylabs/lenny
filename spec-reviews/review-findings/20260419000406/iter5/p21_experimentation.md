# Iter5 Review — Perspective 21: Experimentation & A/B Testing Primitives

**Spec snapshot:** main @ 4027314.
**Scope:** `spec/10_gateway-internals.md` §10.7, `spec/15_external-api-surface.md` §15.1 error catalog + dryRun + `RegisterAdapterUnderTest` matrix, `spec/16_observability.md` §16.1/§16.6/§16.7, `spec/08_recursive-delegation.md` `experimentContext`, `spec/21_planned-post-v1.md` §21.6, `spec/22_explicit-non-decisions.md`.
**Numbering:** continues from iter4 (last EXP-016), so this iteration begins at EXP-017.

---

## Iter4 Fixed-item verification

| Iter4 finding | Status claim | Verification (iter5) |
| --- | --- | --- |
| EXP-013 (experiment events not in §16.6 catalog) | Fixed | Verified. §16.6 lines 607–613 list all five operational `experiment.*` events with payload fields; §16.7 line 636 lists `experiment.status_changed` as an audit event. Anchor `#107-experiment-primitives` resolves. |
| EXP-014 (admission-time isolation-monotonicity) | Fixed | Paragraph added at §10.7 line 854; §15.1 dryRun row at line 1123 echoes the check; §16.1 line 152 registers `lenny_experiment_isolation_rejections_total`; docs propagated to `docs/reference/metrics.md` line 398 and `docs/reference/error-catalog.md` line 166. Residual field-path issue captured in EXP-019 below. |
| EXP-010 (`INVALID_QUERY_PARAMS` undefined) | Not fixed | §10.7 line 950 still references `400 INVALID_QUERY_PARAMS`; catalog at §15.1 lines 971+ still omits it. Carried forward as EXP-017. |
| EXP-011 (variant count unbounded) | Not fixed | `maxVariantsPerExperiment` / `TOO_MANY_VARIANTS` not present anywhere in the spec; §10.7 line 941 still says "typically 2–5". Carried forward as EXP-018. |
| EXP-012 (sticky cache `paused → active` wording) | Not fixed | §10.7 line 1094 is verbatim what iter4 flagged: "no flush is required — the existing cached assignment remains valid." Carried forward as EXP-020. |
| EXP-015 (`BreakdownResponse` example divergence) | Not fixed | §10.7 lines 1014–1082 unchanged from iter4. Carried forward as EXP-021. |
| EXP-016 (no retry-with-fallback pathway) | Not fixed | §10.7 line 852 and §15.1 line 1045 still describe fail-closed behavior without an opt-out or a "no opt-out by design" statement. Carried forward as EXP-022. |

No iter4 Fixed items regressed. Five iter4 findings (EXP-010/011/012/015/016) remained open across the iter4 → iter5 fix pass and are carried forward below, retaining iter3/iter4's Low severity per the iter5 severity-anchoring rubric. New iter5 findings are EXP-019 and EXP-023–EXP-025.

---

## Carry-forward findings (from iter4, unchanged severity)

### EXP-017. `INVALID_QUERY_PARAMS` referenced by §10.7 is still not in the §15.1 error catalog [Low]

**Section:** `spec/10_gateway-internals.md` line 950; `spec/15_external-api-surface.md` §15.1 error catalog (lines 971–1077) and `RegisterAdapterUnderTest` matrix (line 1384).

§10.7's Results API query-parameter table (line 950) still rejects `?delegation_depth=0&breakdown_by=delegation_depth` with `400 INVALID_QUERY_PARAMS`. Iter2 EXP-002 introduced this; iter3 EXP-005 flagged it; iter4 EXP-010 re-flagged it. `INVALID_QUERY_PARAMS` remains absent from the canonical error-code table in §15.1 (grep across the spec tree confirms the token appears only at §10.7 line 950). The contract-test matrix at §15.1 line 1384 enumerates the codes adapters MUST exercise; this code is outside that enumeration, so REST/MCP consistency tests cannot cover the rejection path.

**Recommendation:** Choose one of two paths, consistent with the `cursor_expired` / `VALIDATION_ERROR` precedent at §15.1 line 1229:

- (a) Rewrite §10.7 line 950's closing clause to `"... is rejected with 400 VALIDATION_ERROR with details.fields[0].rule: \"breakdown_collision\""` and add `breakdown_collision` to the `rule` vocabulary in the validation-error-format example at §15.1 lines 1082–1105; **or**
- (b) Add `INVALID_QUERY_PARAMS` as a 400 row to §15.1 (lines 971+), append it to the `RegisterAdapterUnderTest` error-class list at line 1384, and mirror it in `docs/reference/error-catalog.md`.

### EXP-018. Variant count still unbounded; `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` remain unspecified [Low]

**Section:** `spec/10_gateway-internals.md` line 862 (variants "typically 2–5"), line 941 (Results API bound); `spec/15_external-api-surface.md` line 1123 (dryRun row); `spec/22_explicit-non-decisions.md`.

Iter2 EXP-003, iter3 EXP-007, and iter4 EXP-011 all flagged the absence of a variant-count cap. Iter5 grep for `maxVariantsPerExperiment` and `TOO_MANY_VARIANTS` across `spec/` returns zero matches. §10.7 line 941 ("bounded by operator configuration (typically 2–5)") still references a configuration key that does not exist. The consequences called out in iter4 are still live: a `POST /v1/admin/experiments` request with, say, 500 weight-0.001 variants would create 500 `SandboxWarmPool` CRDs, make the bucketing walk at line 756 (`for _, v := range variants`) O(500) on every session assignment, and give the paused-experiment `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*` scan an unbounded keyspace to traverse.

**Recommendation:** Either (a) accept the recurring proposal — add a `maxVariantsPerExperiment` tenant-config key (default 10, maximum 100), enforce it in `POST/PUT /v1/admin/experiments` validation with a new `TOO_MANY_VARIANTS` 422 code in §15.1, echo the limit in the dryRun narrative at line 1123, rewrite line 941 to "bounded by `maxVariantsPerExperiment` (default 10)", and add the code to the `RegisterAdapterUnderTest` matrix at line 1384; or (b) if the platform-team's stance has changed, record the non-decision in §22 with rationale so future iterations stop re-surfacing it. The current ambiguity (no cap, no explicit non-decision) is what keeps the finding regenerating each cycle.

### EXP-020. Sticky-cache `paused → active` sentence still contradicts the preceding flush rule [Low]

**Section:** `spec/10_gateway-internals.md` line 1094.

Line 1094 still reads, verbatim: *"On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid."* The preceding clause flushes all keys on `active → paused` via `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*`; line 850 establishes that paused experiments are not evaluated by the `ExperimentRouter` (first-match rule walks only active experiments), so no entries can be populated while paused. On `paused → active`, "the existing cached assignment" refers to a cache that was just purged and cannot have been repopulated. Iter2 EXP-004 / iter3 EXP-008 / iter4 EXP-012 each identified this; no edit landed. The invariant that actually makes re-activation correct — HMAC-SHA256 determinism at line 751 — is never stated in this paragraph. The spec also remains silent on whether sessions created during the paused window are retroactively enrolled on re-activation (they are not, per the `experimentContext: null` carried forward on their session record, but readers must infer this).

**Recommendation:** Replace the second half of line 1094 with:

> "On `paused → active` re-activation, no re-seeding is required: percentage-mode assignment is deterministic (HMAC-SHA256 of `assignment_key + experiment_id`, line 751), so the first post-re-activation session for a given user recomputes the same variant as before the pause. The cache is lazily repopulated on demand. For `mode: external` experiments, re-evaluation is delegated to the OpenFeature provider per session. Sessions created during the paused window carry `experimentContext: null` and are not retroactively enrolled on re-activation, regardless of `sticky` mode."

### EXP-021. `BreakdownResponse` example still has unexplained per-bucket dimension-set divergence [Low]

**Section:** `spec/10_gateway-internals.md` lines 933 and 1014–1082.

Iter3 EXP-006's fix specified that each bucket's `dimensions` keys are the union of non-null `scores` keys within that bucket's rows only, so a bucket may legitimately omit a dimension that appears in the variant's default (flat) response. The example JSON at lines 1034–1036 shows `control.breakdowns[bucket_value=0]` containing `coherence` and `safety` but not `relevance` — yet the flat response example at lines 969–981 includes `relevance` for the same `control` variant. Additionally, the treatment buckets' `llm-judge` objects (lines 1068, 1075) omit the `dimensions` field entirely; the control buckets include it. The spec does not state whether `dimensions` is present-but-empty (`{}`), absent, or something else when a bucket contains no non-null dimensional scores.

**Recommendation:** Insert after line 1012 (current end of per-dimension rules inside the Breakdown block):

> "A bucket's dimension key set is the union of non-null `scores` keys across that bucket's rows only, so a bucket may omit dimensions that appear in the variant's flat response. In the example below, `control.breakdowns[bucket_value=0]` omits `relevance` because no row in that bucket submitted a `relevance` score, even though the variant's flat response includes it. When a bucket has no non-null dimensional scores for a given scorer, the bucket's `scorers[scorer].dimensions` field is **omitted** (not present as an empty object); the treatment buckets in the example below demonstrate this."

Then re-check the example that either every bucket includes a `dimensions` field or each missing one corresponds to a bucket with no dimensional rows.

### EXP-022. `VARIANT_ISOLATION_UNAVAILABLE` still has no documented retry / opt-out pathway (or explicit non-decision) [Low]

**Section:** `spec/10_gateway-internals.md` line 852; `spec/15_external-api-surface.md` line 1045; `spec/22_explicit-non-decisions.md`.

Iter3 EXP-009's fail-closed rule and iter4 EXP-014's admission-time validation together ensure operators learn of isolation-monotonicity problems before a rejection storm — but iter4 EXP-016's question remains: callers whose `minIsolationProfile` is set by tenant default have no machine-discoverable remediation other than "relax minIsolationProfile" (which requires a policy change the caller may not own) or wait for re-provisioning (unbounded). Iter4 EXP-016 proposed a binary choice — (a) add an `experimentOptOut: true` (or equivalent) flag on session create that bypasses experiment routing and runs the base runtime with `experimentContext: null`, OR (b) document the non-decision explicitly. Neither landed. The spec's silence here interacts badly with iter4 EXP-014's own acknowledgement that rejected sessions never appear in `EvalResult` (so the rejection population is unauditable): callers have no way to route around the experiment while analysts have no way to detect the rejected subset.

**Recommendation:** Pick one:

- (a) Add the opt-out flag. Session-create accepts `experimentOptOut: true` (requires a scoped permission, e.g., `session:experiment:opt-out`), routes the session to the base runtime unconditionally, tags `experimentContext: null` (not `variant_id: "control"` — preserves iter3 EXP-009's control-purity invariant), and emits `experiment.opt_out` (info) with `tenant_id`, `user_id`, `experiment_id`, `reason`. Reference the flag from `VARIANT_ISOLATION_UNAVAILABLE.details.remediation` in §15.1. Update §10.7 line 852 to list it as a third remediation option. **OR**
- (b) Add a paragraph to §10.7 after line 852 and a §22 bullet stating: "No per-session experiment opt-out is provided. Callers whose `minIsolationProfile` is incompatible with an active experiment's variant pool must either (1) have tenant policy relax `minIsolationProfile` for the affected cohort, or (2) accept 422 rejections until the operator re-provisions the variant pool. The tradeoff is deliberate: an opt-out would undermine randomization when used selectively." Cross-reference from `VARIANT_ISOLATION_UNAVAILABLE`'s `details.remediation`.

Either commitment unblocks clients; leaving the question open a fifth iteration is the worst outcome.

---

## New iter5 findings

### EXP-019. Admission-time isolation check uses response-side field path, not pool/runtime CRD field [Low]

**Section:** `spec/10_gateway-internals.md` line 854.

The iter4 EXP-014 fix paragraph describes the admission-time check as: *"the gateway resolves the variant's referenced pool and compares `sessionIsolationLevel.isolationProfile` against the base runtime's default pool `sessionIsolationLevel.isolationProfile`"*. `sessionIsolationLevel` is the **response-side** object returned by `POST /v1/sessions` to give clients visibility into assigned isolation (§07.1 line 65, §15.1 line 751). It is not a field on pool or runtime CRDs. On CRDs, the field is the top-level `isolationProfile` on the Runtime (§05.1 line 66) and the top-level pool-scoped override (§05.3). The `defaultPoolConfig` block on a Runtime (§05.1 line 97) does not nest `isolationProfile` under `sessionIsolationLevel` either. An implementer following line 854 literally would be unable to locate the field they are asked to compare.

Additionally, the comparison target "base runtime's **default pool**" is ambiguous: a runtime can register multiple pools (§05.3); only one of them might be tagged as the default. The spec's `defaultPoolConfig` block is the Runtime's template for its bootstrap default pool, not a persistent "default pool" pointer that the admission check can dereference at validation time.

**Recommendation:** Rewrite line 854 to use CRD field paths and an unambiguous comparison target:

> "For each variant, the gateway resolves the variant's referenced pool (field `variants[].pool`) and reads its effective `isolationProfile` (pool-level override, falling back to the Runtime's top-level `isolationProfile`). It compares this against the base runtime's effective `isolationProfile` (resolved from the Runtime named in `baseRuntime`, §5.1). If any variant's resolved `isolationProfile` is weaker than the base runtime's — using the canonical ordering `standard < sandboxed < microvm` defined in §5.3 — the request is rejected with `422 CONFIGURATION_CONFLICT` ..."

Also clarify what happens when the variant `pool` field is absent (the example at line 693 shows it, but the field is not formally required anywhere): either state that `pool` is required for variants, or specify the fallback (e.g., "`runtime`'s `defaultPoolConfig` is used and its `isolationProfile` is the Runtime's top-level value").

### EXP-023. Admission isolation check compares only against base runtime's pool, not against sessions the base runtime accepts [Medium]

**Section:** `spec/10_gateway-internals.md` lines 852 (runtime-time check), 854 (admission-time check); `spec/07_session-lifecycle.md` (`minIsolationProfile` resolution).

The runtime-time fail-closed rule (line 852) rejects a session when the **variant pool's** isolation is weaker than **that session's `minIsolationProfile`**. The iter4 admission-time rule (line 854) prevents only one subset of this: variants whose isolation is weaker than the **base runtime's default pool**. This leaves a gap. A tenant policy can specify a `minIsolationProfile: microvm` floor for session creation while the base runtime's default pool is `sandboxed` and a variant pool is also `sandboxed`. The admission check passes (variant == base), the experiment goes live, and every microvm-floor session is rejected with `VARIANT_ISOLATION_UNAVAILABLE` — exactly the silent availability regression iter4 EXP-014 aimed to prevent. The admission check's choice to compare "variant pool vs. base runtime" rather than "variant pool vs. the stricter of the tenant/runtime/per-caller defaults" makes the check operationally incomplete.

This matters more than EXP-019 because the gap allows a configuration that is strictly "valid" at admission yet guaranteed to reject a known, in-production traffic class at runtime, and the spec framed iter4's fix as solving this exact problem.

**Recommendation:** Extend line 854's admission check to additionally compare each variant pool's resolved `isolationProfile` against the **tenant-level `minIsolationProfile` floor** (if one is configured) and surface a warning — not a hard reject — when a variant pool's isolation is weaker than the tenant floor. Concretely: add a `details.warnings[]` array to the admission response body that lists each `(variant_id, variant_pool_isolation, tenant_floor)` tuple where `variant_pool_isolation < tenant_floor`. The response is still 2xx (the experiment is creatable), but the `?dryRun=true` path and the non-dryRun create/update both emit this warning so operators see it before rejections appear. Document the warning key (e.g., `warning_code: "variant_weaker_than_tenant_floor"`) in §15.1 and echo it in `docs/reference/error-catalog.md` under a new "Warnings" section. If introducing warning responses is too large a surface change, alternatively emit a new operational event `experiment.variant_weaker_than_tenant_floor` at creation/activation time (distinct from the runtime `experiment.isolation_mismatch`) and register it in §16.6 lines 607–613.

### EXP-024. `experiment.status_changed` audit event does not record the sticky-cache flush outcome [Low]

**Section:** `spec/10_gateway-internals.md` line 1092 (audit event description), line 1094 (flush invariant); `spec/16_observability.md` line 636.

The audit event payload at §16.7 line 636 carries `tenant_id`, `experiment_id`, `previous_status`, `new_status`, `actor_sub`, `transition_at`. The cache flush documented at line 1094 is a side effect of `active → paused` and `active → concluded` / `paused → concluded` transitions, and it drives the `lenny_experiment_sticky_cache_invalidations_total` metric. If the `DEL` call fails (Redis unavailable, partial scan), the metric will under-count but nothing in the audit trail records whether the flush succeeded for a given transition. Operators auditing a post-incident "did stale sticky assignments continue routing after I paused this experiment?" have only the metric's rollup; they cannot join to an individual transition record.

**Recommendation:** Add two optional payload fields to the `experiment.status_changed` audit event documented at §16.7 line 636: `sticky_cache_flushed` (boolean — `true` if flush was attempted and reported zero errors, `false` if skipped or reported errors, absent when the transition did not trigger a flush) and `sticky_cache_flush_keys_deleted` (integer — count of keys the `DEL` reported; absent when flush was not attempted). Document these in §10.7 line 1094's flush paragraph so the cross-reference is bidirectional. This is Low severity because the primary correctness guarantee (HMAC determinism on re-activation) does not depend on flush success; the audit gap is an observability-for-forensics concern.

### EXP-025. Multi-experiment `created_at` ordering tiebreak undefined [Low]

**Section:** `spec/10_gateway-internals.md` line 774 (first-match rule), line 850 (multi-experiment restatement).

Line 774 specifies: "When multiple active experiments are defined for a tenant, the `ExperimentRouter` evaluates them in ascending order of `created_at` (experiment creation timestamp). For each experiment in that order, `assignVariant` is called independently. The router stops at the **first experiment** where the result is a non-control variant..." Line 850 restates the rule. Neither specifies a tiebreaker for experiments with identical `created_at` values — which is plausible when experiments are bulk-imported by a seed job or when two admin requests land within the same millisecond under Postgres's default `TIMESTAMP WITH TIME ZONE` precision. Without a stable tiebreak, the router's assignment for a session that hashes to non-control in both `A` and `B` is non-deterministic: a replica's Go map-iteration order or a Postgres index-order drift across minor versions would silently re-bucket the same user between `A` and `B`. This is a correctness invariant for the "single experiment per session" guarantee and for `sticky: user` semantics across replicas.

**Recommendation:** Amend line 774 and line 850 identically to read: "... evaluates them in ascending order of `(created_at, experiment_id)` — `experiment_id` is the secondary sort key to guarantee deterministic ordering across replicas when two experiments share a `created_at` value." Add a matching invariant to the `lenny-adapter.proto` or gateway state-machine documentation if such a tiebreaker already exists in the implementation plan; otherwise document it as a new constraint on the Postgres query. Low severity because bulk-create at identical timestamps is rare in practice, but the invariant is worth nailing down before v1.

---

## Convergence assessment

**Open finding count this iteration:** 9 (5 carry-forward + 4 new), all Low except EXP-023 (Medium).
**Iter4 Fixed items verified:** 2/2 intact (EXP-013, EXP-014).
**Regression count:** 0.

**Convergence trend:** Iter5 is close to convergence for this perspective. The iter4 iteration drove the two Medium-severity findings (EXP-013, EXP-014) to closure. The remaining five carry-forwards (EXP-017/018/020/021/022) are all Low and have been open for two or more iterations; each has a concrete, small-edit recommendation already drafted in prior iterations. Iter5's three new Low findings (EXP-019, EXP-024, EXP-025) are polish items exposed by reading the iter4 fix text closely. The one new Medium (EXP-023) is a substantive gap in the iter4 admission-time check's completeness — it preserves the "silent availability regression" class the admission check was written to close.

**Recommended next step:** one short fix pass addressing EXP-023 (Medium) and EXP-019 (to correct the field-path language introduced by iter4); at that point the remaining Low findings can be batched and the perspective converged in iter6.
