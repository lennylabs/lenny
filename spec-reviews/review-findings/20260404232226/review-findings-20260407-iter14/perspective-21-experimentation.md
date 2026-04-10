# Review Findings — Perspective 21: Experimentation & A/B Testing Primitives (Iteration 14)

**Spec file:** `technical-design.md` (8,691 lines)
**Perspective:** Experimentation & A/B Testing Primitives
**Prior findings still open:** EXP-033A, EXP-033B, EXP-033C (from iter13)

## Status of Prior Findings

| ID | Status | Notes |
|----|--------|-------|
| EXP-033A | **STILL PRESENT** | Results API cursor example at line 4517 uses `AES256:v1:...` format. Line 4523 says MUST use "platform-standard opaque cursor encoding (Section 15.1)" but MUST NOT use "plain base64-encoded JSON". The Section 15.1 standard example (line 6573) IS base64-encoded JSON (`eyJpZCI6...`). The Results API cannot simultaneously conform to the platform standard and avoid base64-encoded JSON — the standard IS base64-encoded JSON. |
| EXP-033B | **STILL PRESENT** | Hash bucketing formula (line 4294) `hash(user_id + experiment_id) mod 100 < variant weight` only describes binary control/treatment assignment. With multiple variants (the `variants` list at line 4263 is an array), the bucketing scheme for assigning users to variant A vs variant B vs control is undefined. |
| EXP-033C | **STILL PRESENT** | Line 4521: "the gateway creates a materialized view" at runtime based on a Helm parameter. This contradicts the spec's own pattern that all DDL goes through schema migrations (e.g., line 4935: triggers "must be created by the Lenny schema migration, not left to manual operator action"). The gateway is a runtime service, not a schema migration tool. |

## New Findings

### EXP-034 `variant_weight` Unit Mismatch Between YAML Definition and PoolScalingController Formulas [Medium]

**Lines:** 4267, 509-510, 524-525, 528, 4282, 4294

**Problem:** The `ExperimentDefinition` YAML (line 4267) defines `weight: 10 # percentage` — weight is an integer percentage (0-100). The `initialMinWarm` sizing guidance (line 4282) correctly converts: `weight_fraction = variant_weight / 100`. The hash assignment (line 4294) correctly uses `mod 100 < variant weight`.

However, the PoolScalingController formula (line 509) uses `variant_weight` as a raw multiplier without dividing by 100:

```
target_minWarm = ceil(base_demand_p95 x variant_weight x safety_factor x ...)
```

With `variant_weight = 10` (percentage), this computes 10x the expected demand instead of 10%. The same error appears in the base pool adjustment formula (lines 524-525) and the execution mode variant (lines 1975-1976).

Additionally, line 528 clamps `Sigma variant_weights` to `[0, 1)` and rejects configurations where `Sigma variant_weights >= 1`. With percentage-unit weights, a single 10% variant gives `Sigma variant_weights = 10`, which exceeds 1 and would be rejected. The clamping range `[0, 1)` is correct for fractional weights (0.0-1.0) but wrong for percentage weights (0-100). The correct range would be `[0, 100)`.

**Impact:** Either the formulas silently produce wildly wrong pool sizes (10x over-provisioning for a 10% variant), or the admission check incorrectly rejects valid experiment configurations.

### EXP-035 `lenny_eval_score` Metric Type Is Gauge but Rollback Triggers Require Aggregation [Medium]

**Lines:** 7485, 4561

**Problem:** The `lenny_eval_score` metric (line 7485) is declared as a **Gauge** type, labeled by `tenant_id`, `scorer`, `variant_id`. A Prometheus Gauge represents a single current value per unique label combination.

The rollback trigger table (line 4561) references: "Mean safety score drops below 0.95 for the variant" using this metric. Computing a mean requires either a **Histogram** (derive mean from `rate(sum)/rate(count)`) or a **Summary** (pre-computed quantiles). A Gauge only stores the last observed value — each new eval submission for the same `(tenant_id, scorer, variant_id)` label set overwrites the previous one.

With a Gauge, there is no way to compute "mean safety score" across multiple eval submissions via PromQL. The metric type should be Histogram (with `_sum` and `_count` sub-metrics) to support the aggregation that the rollback trigger table requires.

### EXP-036 Results API Response Does Not Follow Platform Pagination Envelope [Medium]

**Lines:** 4463, 4466-4518, 6556-6579

**Problem:** The Results API (line 4463) claims to use "cursor-based pagination per Section 15." Section 15.1 (line 6556) explicitly lists `GET /v1/admin/experiments/{name}/results` as a paginated endpoint. The standard pagination envelope (lines 6566-6579) requires:

```json
{ "items": [...], "cursor": "...", "hasMore": true, "total": 1247 }
```

But the Results API response (lines 4466-4518) uses a completely different structure:

```json
{ "experiment_id": "...", "status": "...", "variants": [...], "cursor": "..." }
```

The response uses `variants` instead of `items`, and is missing `hasMore` and `total`. This is a structural violation of the pagination standard that the spec itself defines and claims to follow. Additionally, the response contains aggregated summary data per variant (typically 2-3 entries) — there is nothing meaningful to paginate in this response shape.

## Summary

| ID | Severity | Category | One-line |
|----|----------|----------|----------|
| EXP-033A | Medium | Factual error | Results API cursor format contradicts Section 15.1 standard it claims to follow |
| EXP-033B | Medium | Design gap | Multi-variant hash bucketing undefined for >1 variant |
| EXP-033C | Medium | Design contradiction | Gateway creates materialized view at runtime, contradicting migration-only DDL pattern |
| EXP-034 | Medium | Factual error | `variant_weight` used as raw multiplier in formulas but defined as percentage in YAML |
| EXP-035 | Medium | Design contradiction | `lenny_eval_score` Gauge cannot support mean computation required by rollback triggers |
| EXP-036 | Medium | Design contradiction | Results API response uses non-standard envelope despite being listed as paginated endpoint |

**Total: 6 findings (0 Critical, 0 High, 6 Medium)**
- 3 carried from iter13 (still present)
- 3 new (EXP-034, EXP-035, EXP-036)
