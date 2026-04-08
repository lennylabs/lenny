# Technical Design Review Findings — 2026-04-07 (Iteration 13)

**Document reviewed:** `docs/technical-design.md` (8,694 lines)
**Iteration:** 13 (25 agents, 1 per perspective)
**Total findings:** ~23 (0 Critical, 1 High, ~22 Medium)
**Clean perspectives:** SEC, DXP, TNT, OBS, CPS, BLD, DOC, EXM (8 of 25)

## High

| # | ID | Finding | Section |
|---|-----|---------|---------|
| 1 | K8S-034 | lenny-system default-deny blocks kube-apiserver→webhook callbacks; all fail-closed webhooks silently broken | 13.2 |

## Medium (~22)

**Factual errors:** PRT-035 (artifact:// URI scheme x2), OPS-042 (Postgres Tier1 8GB→4GB), API-048 (DEADLOCK_TIMEOUT HTTP 408 wrong), API-049 (ETAG_REQUIRED duplicate entry), SCL-036 (quota drift T1/T3 arithmetic), EXP-033A (cursor example violates own prohibition), POL-041 (cross-phase priority ordering error), NET-034 (lenny-pool-config ghost webhook)

**Design contradictions:** CMP-041 (salt rotation vs erasure deletion), EXP-033C (on-read aggregation vs materialized view Helm param), SCH-040 (MessageEnvelope missing type field), MSG-037 (delivery_receipt missing error status), MSG-038 (inbox-to-DLQ undefined for durableInbox:true)

**Design gaps:** SLC-040 (running→resume_pending missing from §7.2), STR-039 (XAUTOCLAIM billing duplicate race — need ON CONFLICT), DEL-039 (settled=all redundant mode), FLR-038 (Redis runbook phantom metrics), WPL-030 (failover formula 25s wrong — should be 17s), CRD-031/032 (vault_transit/github category errors in LLM provider table), EXP-033B (multi-variant hash bucketing undefined)
