# Technical Design Review Findings — 2026-04-07 (Iteration 8)

**Document reviewed:** `docs/technical-design.md` (8,649 lines)
**Review framework:** `docs/review-povs.md`
**Iteration:** 8 (25 agents, 1 per perspective)
**Total findings:** ~60 (0 Critical, 5 High, ~53 Medium, 2 Low)

## High Findings

| # | ID | Finding | Section |
|---|-----|---------|---------|
| 1 | K8S-025 | WarmPoolController RBAC missing `create`/`delete` Sandbox + SandboxClaim access | 4.6.3 |
| 2 | STR-028 | Billing stream flusher has no replica coordination — concurrent duplicate billing events | 12.3 |
| 3 | OBS-037 | Availability SLO burn-rate formula is mathematically inverted | 16.5 |
| 4 | OBS-038 | Head-based sampling at 10% incompatible with 100%-error-sampling requirement | 16.3 |
| 5 | API-038 | §15.1 "Comprehensive Admin API" table omits 12 operational endpoints | 15.1 |

## Key Design Flaws Found This Iteration

- **K8S-025**: RBAC table has WPC with only `update` on Sandbox — missing `create`, `delete`, and all SandboxClaim verbs. Implementor gets 403 on every pod creation.
- **STR-028**: All N gateway replicas concurrently flush the same Redis billing stream entries to Postgres — doubling billing events with distinct sequence numbers.
- **OBS-037**: Burn-rate formula `(1 - error_rate) / (1 - slo_target)` yields max burn at 0% errors and zero burn at 100% errors — completely inverted.
- **OBS-038**: Head-based sampling decides discard before errors/latency are known — 100% error sampling is architecturally impossible with head-based decisions.
- **API-038**: 12 endpoints (6 pool upgrade lifecycle, bootstrap-override, credential pool membership, preflight, quota reconcile, token rotation, billing reasons) are in §24 lenny-ctl but not in the "Comprehensive" §15.1 table.

_Per-perspective detailed findings are in the subagent outputs and individual files in this directory._
