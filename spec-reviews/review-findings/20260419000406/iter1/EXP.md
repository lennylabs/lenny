### EXP-001 Results API Dimension Aggregation Mismatch [Medium]
**Files:** `10_gateway-internals.md` §10.7 (lines 814-871), `04_system-components.md` (line 720)

The Results API response schema (Section 10.7) states dimensions object is present only when at least one EvalResult has a non-null scores field; dimension keys are union of all keys. However, the `EvalResult` schema defines `scores` as `jsonb` (Optional, Multi-dimensional scores).

The inconsistency: computing dimension aggregates (mean, p50, p95, count per dimension) is not specified for:
1. Sessions that submitted only some dimensions (e.g., some `{"coherence": 0.9}`, others `{"relevance": 0.8, "coherence": 0.85}`).
2. Whether `count` for a dimension equals scorer's total count (denominator bias) or counts only sessions with that dimension (numerator bias).

This affects statistical interpretation — a dimension with 390 samples vs. 412 total has inherent selection bias not documented.

**Recommendation:** Add explicit aggregation semantics to Section 10.7. Document whether dimension aggregates use full union of dimension keys. Define: "`count` for a dimension = number of EvalResult records where `scores[dimension]` is non-null; mean/percentiles computed only over non-null values for that dimension." Optionally add a note about selection bias in cross-dimension comparisons.
