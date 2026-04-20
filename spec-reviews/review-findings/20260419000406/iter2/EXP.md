### EXP-002 Results API Has No Filters for delegation_depth / inherited / submitted_after_conclusion [Medium]
**Files:** `10_gateway-internals.md` §10.7 (lines 752, 754, 789–791, 821, 827, 884), `15_external-api-surface.md` line 423

The spec stores three analysis-critical per-`EvalResult` fields and prescribes filtering by them:

- `delegation_depth` (uint32) — "distinguish direct eval results (depth 0) from propagated child results" (line 752).
- `inherited` (boolean) — mirrors `experimentContext.inherited`.
- `submitted_after_conclusion` (boolean) — "Enables operators to filter post-conclusion submissions in analysis" (line 791).

The **Sample contamination warning for `control` propagation mode** paragraph (line 754) explicitly directs operators: *"filter by `delegation_depth == 0` (or `inherited == false`) to obtain uncontaminated per-variant aggregates, or segment results by `delegation_depth` to analyze the effect at each level separately."*

However, `GET /v1/admin/experiments/{name}/results` accepts no query parameters. The response (line 827–882) is a single pre-aggregated object with one bucket per variant — `sample_count`, `scorers[*].{mean,p50,p95,count}`. There is no way for an operator to:

1. Exclude `inherited == true` rows (the recommended sample-contamination mitigation under `control` propagation).
2. Exclude `submitted_after_conclusion == true` rows (recommended for post-conclusion eval hygiene).
3. Segment by `delegation_depth`.

The advice is operationally unreachable via the platform's own API. Operators would need direct Postgres access (bypassing RLS-on-API) or a per-row export endpoint that does not exist. This is a contradiction between prescriptive guidance ("operators should filter by X") and the available interface.

**Recommendation:** Pick one of the following and document it in §10.7:

- **A (preferred):** Add query-string filters to the Results endpoint: `?delegation_depth=0`, `?inherited=false`, `?exclude_post_conclusion=true`. Aggregation is recomputed over the filtered subset; the `lenny_eval_aggregates` materialized view retains its pre-aggregated role only when no filter is supplied.
- **B:** Add a `?breakdown_by=` param (`delegation_depth`, `inherited`, `submitted_after_conclusion`) that splits each variant bucket into sub-buckets.
- **C:** Add a per-row export endpoint (`GET /v1/admin/experiments/{name}/eval-results`, cursor-paginated) and explicitly state §10.7 guidance uses that endpoint.

Whichever path is chosen, line 754's guidance and line 922's rollback-trigger signal ("Mean eval score degradation") must match the actual interface. Today they point at a capability the API does not expose.

---

### EXP-003 `ExperimentDefinition` Has No Hard Cap on Variant Count [Low]
**Files:** `10_gateway-internals.md` §10.7 (lines 578–589, 827), `15_external-api-surface.md` line 730

Line 827 claims: *"The number of variants per experiment is bounded by operator configuration (typically 2–5) and the aggregation is pre-computed, so the response size is inherently bounded."* But no such "operator configuration" field exists — there is no `maxVariantsPerExperiment` tenant setting and no explicit schema limit on the `variants:` list at `POST/PUT /v1/admin/experiments`. Dry-run validation (line 730) checks only that `Σ variant_weights ∈ [0, 1)`.

Consequences:
- Each variant allocates a `SandboxWarmPool`; an experiment with, e.g., 500 tiny-weight variants would create 500 variant warm pools. The `(1 - Σ variant_weights)` clamp guards traffic share but not pool count.
- `bucket` evaluation is O(N variants) per session create on the hot path.
- The `sticky: user` Redis `DEL` pattern-scan on pause/conclude is also unbounded.

**Recommendation:** Add an explicit hard cap (e.g., `maxVariantsPerExperiment`, default 10) enforced at `POST/PUT /v1/admin/experiments` with a new error code (`TOO_MANY_VARIANTS`, 422). Replace "typically 2–5" on line 827 with the concrete default so the bounded-response claim is actually guaranteed.

---

### EXP-004 Paused-Experiment Sticky Cache Wording Is Internally Contradictory [Low]
**Files:** `10_gateway-internals.md` §10.7 (lines 738, 892)

Line 738: `ExperimentRouter` only evaluates **active** experiments — paused experiments are skipped entirely.
Line 892: on `active → paused` the sticky cache is flushed (`DEL` on all `t:{tenant_id}:exp:{experiment_id}:sticky:*`); on `paused → active` "no flush is required — the existing cached assignment remains valid."

After a flush-on-pause no entries exist, and during the paused window the paused experiment is not evaluated at all (line 738), so the cache cannot be repopulated for that experiment. The "existing cached assignment remains valid" clause describes a state that cannot exist after the flush. The actual correctness argument is determinism (HMAC_SHA256 of `user_id + experiment_id`), not cache persistence.

**Recommendation:** Rewrite the second half of line 892 to: *"On `paused → active`, no re-seeding is required: percentage-mode assignment is deterministic (HMAC-SHA256 of `user_id + experiment_id`), so the first post-re-activation session for a given user recomputes the same variant as before the pause. The cache is repopulated lazily on demand. For `mode: external` experiments, re-evaluation is delegated to the OpenFeature provider per session."* Also add an explicit statement that sessions created while the experiment is paused have `experimentContext: null` and are not retroactively enrolled on re-activation, even under `sticky: session`.
