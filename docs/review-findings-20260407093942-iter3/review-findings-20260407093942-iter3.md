# Technical Design Review Findings — 2026-04-07 (Iteration 3)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 3 of 5
**Total findings:** 3 (1 Critical, 2 High — all fixed)

## Findings Summary

| Severity | Count | Status |
| -------- | ----- | ------ |
| Critical | 1     | Fixed  |
| High     | 2     | Fixed  |

### Findings

1. **K8S-001-PARTIAL** [Critical] — SandboxClaim double-claim residual risk during failover window. **Fixed** — Added `lenny-sandboxclaim-guard` ValidatingAdmissionWebhook that rejects PATCH/PUT on already-claimed claims.

2. **DOC-STR-REGRESSION-001** [High] — §11.2 quota formula still said `replica_count` instead of `cached_replica_count`. **Fixed** — One-line prose correction.

3. **API-ERR-CATALOG-INCOMPLETE** [High] — 15 error codes referenced in spec but missing from error catalog. **Fixed** — Added all 15 missing error code rows.

### Comparison Across Iterations

| Severity | Iter 1 | Iter 2 | Iter 3 | Trend |
|----------|--------|--------|--------|-------|
| Critical | 30     | 4      | 1      | ↓97%  |
| High     | 105    | 26     | 2      | ↓98%  |
| Medium   | 138    | 61     | 0*     | —     |
| Low      | 71     | 40     | 0*     | —     |
| Total    | 353    | 137    | 3      | ↓99%  |

*Iteration 3 was scoped to Critical/High only.

### Spec Clean at Critical/High Level

After iteration 3 fixes, all areas checked show no remaining Critical or High findings. Key verification:
- §4.1, §4.6, §8.3, §10.1, §10.6, §11.2, §12.3, §12.4, §13.2, §15.1, §15.2, §15.4.1, §15.5, §17.7 — all clean.
- No regressions from iteration 2 fixes detected.
- The spec is **clean at Critical and High severity level**.
