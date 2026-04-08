# Technical Design Review Findings — 2026-04-07 (Iteration 9)

**Document reviewed:** `docs/technical-design.md` (8,671 lines)
**Iteration:** 9 (25 agents, 1 per perspective)
**Total findings:** ~44 after deduplication (0 Critical, ~8 High, ~30 Medium, ~7 Low)

## High Findings

| # | ID | Finding | Section |
|---|-----|---------|---------|
| 1 | K8S-025 | WPC RBAC still missing SandboxClaim, PDB, Leases verbs (partial iter8 fix) | 4.6.3 |
| 2 | NET-032 | Gateway NetworkPolicy egress missing external HTTPS for LLM proxy, connectors, webhooks | 13.2 |
| 3 | PRT-031/SCH-037 | "Rejection for durable storage" contradicts normative forward-read rule | 15.4.1 |
| 4 | OBS-042 | `AuditSIEMNotConfigured` Critical variant is self-defeating (gateway refuses to start → alert can't fire) | 11.7, 16.5 |
| 5 | OBS-043 | `lenny_task_reuse_count` typed as Gauge in §16.1 but used as "histogram (p50)" in §4.6.2 formula | 4.6.2, 16.1 |
| 6 | API-039 | `GET /v1/admin/preflight` should be POST (not idempotent, makes outbound probes) | 15.1 |
| 7 | API-040/DXP-032/MSG-034 | `MessageEnvelope.from` examples use bare string `"client"` but schema defines it as object `{kind, id}` | 15.4.1 |
| 8 | DOC-138 | §10.1 BarrierAck floor rule + defaults example are self-contradictory (45s < 90s tier cap) | 10.1, 17.8.1 |

## Medium Findings (~30)

**K8S:** K8S-026 (variant_weight undefined for base pools), K8S-027 ("single atomic write" false), K8S-028 (3 alert cross-refs §5.3→§4.6.1), K8S-029 (failover formula 25s wrong — should be 15s)
**SEC:** SEC-042 (targeting webhook SSRF), SEC-043 (PostAuth MODIFY enforcement), SEC-044 (connector mcpServerUrl SSRF)
**SCL:** SCL-030 (Redis table 2000/s→200/s regression), SCL-031 (§10.1 attributes Tier 3 rate to Tier 2)
**TNT:** TNT-027 (concurrent-workspace no tenant pinning), TNT-028 (billing_seq SQL injection), TNT-029 (tenant_id routing hint trust model)
**STR:** STR-032 (billing stream TTL doesn't slide)
**DEL:** DEL-032 (settled=all dead alias), DEL-033 ("task DAG" should be "task tree")
**SLC:** SLC-034 (resuming classified both internal and external)
**OBS:** OBS-044 (PgBouncerAllReplicasDown in Warning table but marked Critical), OBS-045 (deployment bullet still lists active_sessions as HPA metric)
**OPS:** OPS-035 (§17.8.2 warm pool sizing numerical errors)
**CPS:** CPS-025 (E2B license claim wrong — Apache-2.0 not AGPL), CPS-026 (A2A governance claim wrong)
**WPL:** WPL-026 (burst term missing variant_weight)
**CRD:** CRD-026 (privateKeyJson in omission list but not in schema), CRD-027 (github/vault_transit missing from provider table), CRD-028 (anthropic_direct "short-lived token" claim wrong)
**BLD:** BLD-028 (Phase 2 benchmark can't measure real RuntimeClasses)
**FLR:** FLR-030 (resume_pending unbounded under pool exhaustion), FLR-031 (DLQ TTL inconsistency with resume_pending), FLR-032 (maxTreeRecoverySeconds < maxResumeWindowSeconds for depth-1)
**EXP:** EXP-027 (cursor example violates own prohibition), EXP-028 (sticky:session/none undefined), EXP-029 (hash algorithm unspecified), EXP-030 (stale cache on weight change after pause)
**DOC:** DOC-137 (terminationGracePeriodSeconds stale "60-120s")
**API:** API-041 (dryRun bootstrap audit exception contradicts general rule)
**POL:** POL-038 (interceptorRef condition 2 unenforceable with scalar field)

_Detailed findings from all 25 perspectives are preserved in the subagent outputs above and in the per-perspective files in this directory._
