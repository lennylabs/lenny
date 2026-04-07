# Technical Design Review Findings — 2026-04-07 (Iteration 2)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 2 of 5
**Total findings:** 137 across 25 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 4     |
| High     | 26    |
| Medium   | 61    |
| Low      | 40    |
| Info     | 6     |

### Critical Findings

| # | Finding | Section |
|---|---------|---------|
| 1 | K8S-013 Tier 3 maxSessionsPerReplica identical to Tier 2 — no scaling path | 4.1, 2 |
| 2 | SCL-015 maxSessionsPerReplica arithmetic doesn't scale to Tier 3 without extraction | 4.1, 2, 16.5 |
| 3 | DOC-119 API table contradicts CMP-006 erasure-salt fix (regression) | 15.1 |
| 4 | CRD-016 Admin API circuit-breaker override field undocumented in REST table | 15.1 |

### Comparison with Iteration 1

| Severity | Iter 1 | Iter 2 | Change |
|----------|--------|--------|--------|
| Critical | 30     | 4      | -87%   |
| High     | 105    | 26     | -75%   |
| Medium   | 138    | 61     | -56%   |
| Low      | 71     | 40     | -44%   |
| Info     | 16     | 6      | -63%   |
| **Total** | **353** | **137** | **-61%** |

### Key Themes in Iteration 2

1. **Regressions from iter1 fixes**: DOC-119 (API table contradicts erasure-salt fix), SEC-026/OPS-017 (Redis runbook still says `replica_count=1`), SLC-019 (Section 15.1 derive preconditions not updated), CRD-016 (circuit-breaker field undocumented)
2. **Carry-forward Medium/Low from iter1**: ~80% of iter2 findings are Medium/Low items that were out of scope for iter1 fixes (Critical+High only)
3. **Observability gaps persist**: 10 OBS findings from iter1 remain unresolved (metrics not in canonical table, alerting gaps)
4. **Tier 3 capacity remains unvalidated**: K8S-013 and SCL-015 identify that the 10,000-session claim has no empirical support

---

_Detailed findings from each perspective are available in the subagent outputs. The findings above represent the consolidated summary._
