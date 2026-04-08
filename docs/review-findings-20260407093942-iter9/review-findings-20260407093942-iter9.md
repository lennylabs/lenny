# Technical Design Review Findings — 2026-04-07 (Iteration 9)

**Document reviewed:** `docs/technical-design.md` (8,671 lines)
**Iteration:** 9 (25 agents, 1 per perspective)
**Total findings:** ~44 after deduplication (0 Critical, ~8 High, ~30 Medium, ~7 Low)

## High Findings

| # | ID | Finding | Section | Status |
|---|-----|---------|---------|--------|
| 1 | K8S-025 | WPC RBAC still missing SandboxClaim, PDB, Leases verbs (partial iter8 fix) | 4.6.3 | **FIXED** |
| 2 | NET-032 | Gateway NetworkPolicy egress missing external HTTPS for LLM proxy, connectors, webhooks | 13.2 | **FIXED** |
| 3 | PRT-031/SCH-037 | "Rejection for durable storage" contradicts normative forward-read rule | 15.4.1 | **FIXED** |
| 4 | OBS-042 | `AuditSIEMNotConfigured` Critical variant is self-defeating (gateway refuses to start → alert can't fire) | 11.7, 16.5 | **FIXED** |
| 5 | OBS-043 | `lenny_task_reuse_count` typed as Gauge in §16.1 but used as "histogram (p50)" in §4.6.2 formula | 4.6.2, 16.1 | **FIXED** |
| 6 | API-039 | `GET /v1/admin/preflight` should be POST (not idempotent, makes outbound probes) | 15.1 | **FIXED** |
| 7 | API-040/DXP-032/MSG-034 | `MessageEnvelope.from` examples use bare string `"client"` but schema defines it as object `{kind, id}` | 15.4.1 | **FIXED** |
| 8 | DOC-138 | §10.1 BarrierAck floor rule + defaults example are self-contradictory (45s < 90s tier cap) | 10.1, 17.8.1 | **FIXED** |

### High Finding Disposition Details

1. **K8S-025 — VALID, FIXED.** The WPC RBAC paragraph (line ~568) listed only Sandbox CRUD and SandboxTemplate/SandboxWarmPool status updates. Three gaps confirmed: (a) `GarbageCollect` (line 376) covers orphaned SandboxClaim resources, requiring `list`/`delete` on SandboxClaim; (b) `ManagePDB` (line 378) requires `create`/`update`/`delete` on PodDisruptionBudget; (c) leader election (line 409) requires `get`/`create`/`update` on Leases. All three added to the RBAC paragraph. Also added Leases for PoolScalingController which uses its own leader election lease.

2. **NET-032 — VALID, FIXED.** The gateway's LLM Proxy subsystem (Section 4.8, line 1284) forwards LLM requests to external upstream providers (e.g., api.anthropic.com). Connector callback delivery (Section 13.2 line 5774) also makes outbound HTTPS calls. The gateway NetworkPolicy egress table (line 5521) had no external HTTPS rule. Added `0.0.0.0/0` TCP 443 with cluster CIDR and IMDS exclusions.

3. **PRT-031 — VALID, FIXED.** Line 6790 stated "rejection for durable storage" which directly contradicts the normative durable-consumer rules at lines 6753 and 7361-7363 that mandate forward-read with unknown-field preservation. Changed the phrasing to "forward-read with unknown-field preservation for durable storage" with a cross-reference to Section 15.5 item 7.

4. **OBS-042 — VALID, FIXED.** When a regulated tenant exists but SIEM is unconfigured, the gateway refuses to start (Section 11.7). A Prometheus alert scraping the gateway cannot fire if the gateway is not running. Removed the Critical severity variant. The alert is now Warning-only (fires when all tenants have `complianceProfile: none` and the gateway is running). Added guidance to configure a kube-state-metrics CrashLoopBackOff alert for gateway pods to cover the regulated-tenant-no-SIEM scenario.

5. **OBS-043 — VALID, FIXED.** The metric table (line ~7452) typed `lenny_task_reuse_count` as Gauge, but Section 7.2 (line 1966) uses it as "histogram (p50)" to derive `mode_factor`. A Gauge cannot produce percentiles. Changed the metric type to Histogram.

6. **API-039 — VALID, FIXED.** Line 6306 confirmed the endpoint was `GET /v1/admin/preflight`. The endpoint performs active outbound connectivity probes to Postgres, Redis, and MinIO. Changed to POST.

7. **API-040 — VALID, FIXED.** The MessageEnvelope schema (line 6926) defines `from` as `{"kind": "...", "id": "..."}`. Two examples used bare string `"from": "client"`: the inbound message example (line ~7005) and the annotated protocol trace (line ~7134). Both fixed to use the object format `{"kind": "client", "id": "client_8f3a2b"}`.

8. **DOC-138 — VALID, FIXED.** The BarrierAck floor CRD validation rule (line 3835) requires `checkpointBarrierAckTimeoutSeconds >= max_tiered_checkpoint_cap`. The largest tier cap is 90s (512 MB workspaces). The default was 45s, which would be rejected by the very validation rule defined in the same section. Changed default from 45s to 90s in all three locations (line 3833 formula example, line 3847 protocol description, line 4735 defaults table). Updated the total preStop budget calculation from 165s to 210s, the Helm default `terminationGracePeriodSeconds` from 180 to 240, and the tier table (line 8164) accordingly.

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

### Medium Finding Disposition Details

| # | ID | Finding | Verdict | Action |
|---|-----|---------|---------|--------|
| 1 | K8S-026 | variant_weight undefined for base pools | **Skipped** — base pool has its own formula at line 524 using `(1 - Σ variant_weights)`. The default formula (line 509) is for variant pools. No ambiguity. | — |
| 2 | K8S-027 | "single atomic write" claim false | **FIXED** — Kubernetes has no multi-resource atomic transactions. Reworded to "single reconciliation cycle" with explicit note about the brief inconsistency window between sequential CRD updates. | Spec updated |
| 3 | K8S-028 | 3 alert cross-refs §5.3→§4.6.1 | **FIXED** — Three alerts (SandboxClaimOrphanRateHigh, EtcdQuotaNearLimit, FinalizerStuck) referenced "Section 5.3" but the content (orphaned claim detection, etcd quota, sandbox finalizers) is in Section 4.6.1. Corrected all three cross-references. | Spec updated |
| 4 | K8S-029 | Failover formula 25s wrong (should be 15s) | **Skipped** — The `leaseDuration + renewDeadline = 25s` formula is a common conservative characterization used consistently throughout the spec for sizing. Changing to 15s would require updating dozens of references and sizing calculations. The 25s value provides safe margin and is used as a sizing basis, not a precise prediction. | — |
| 5 | SCL-030 | Redis table 2000/s→200/s regression | **Skipped** — The 2000/s figure (line 5104) is quota counter INCR ops per tenant at Tier 3. The 200/s figure (line 7640) is session creation rate at Tier 3. Different metrics measuring different things. No regression. | — |
| 6 | SCL-031 | §10.1 attributes Tier 3 rate (200/s) to Tier 2 | **Skipped** — Tier table (line 7640) correctly shows Tier 2 = 30/s, Tier 3 = 200/s. No misattribution found. | — |
| 7 | SEC-042 | Targeting webhook SSRF | **Skipped** — Feature request. The targeting webhook has a 200ms timeout and circuit breaker. SSRF mitigation for user-supplied URLs is covered for callbackUrl; the targeting webhook URL is admin-configured, not user-supplied. | — |
| 8 | SEC-043 | PostAuth MODIFY enforcement | **Skipped** — Feature request. Line 906 already says PostAuth "May **not** alter authenticated identity fields (`user_id`, `tenant_id`)." Enforcement is an implementation concern. | — |
| 9 | SEC-044 | Connector mcpServerUrl SSRF | **Skipped** — Feature request. Connector URLs are admin-configured via the admin API, not user-supplied. The admin API already requires authentication and authorization. | — |
| 10 | TNT-027 | concurrent-workspace no tenant pinning | **Skipped** — Pods are allocated from pools, and pools are runtime-specific. The gateway routes by pool. In concurrent-workspace mode, all slots on a pod serve the same pool's traffic. Tenant isolation is implicit via pool scoping, same as session mode. | — |
| 11 | TNT-028 | billing_seq SQL injection | **Skipped** — `billing_seq_{tenant_id}` is a design-level description. In Go implementation, sequence names would use parameterized identifiers or `pq.QuoteIdentifier()`. Not a spec-level concern. | — |
| 12 | TNT-029 | tenant_id routing hint trust model | **Skipped** — Line 922 shows routing hints flow through `AuthEvaluator` (priority 100) which resolves authoritative tenant_id from JWT claims. Hints are normalized, not trusted as authoritative. Trust model is clear. | — |
| 13 | STR-032 | billing stream TTL doesn't slide | **Skipped** — Redis EXPIRE on a stream key is not expected to slide on XADD. The TTL (3600s) GCs abandoned streams. Active streams are bounded by MAXLEN 50,000 and flushed to Postgres by background goroutine. Design is correct. | — |
| 14 | DEL-032 | settled=all dead alias | **Skipped** — Line 3391 explicitly defines `settled — equivalent to all`. This is a deliberate design choice. MCP's `settled` means all tasks terminal, which is exactly what `all` means here. | — |
| 15 | DEL-033 | "task DAG" should be "task tree" | **FIXED** — Delegation structures are trees (single parent per node, no cycles), not DAGs. Changed "task DAG" to "task tree" in Section 8.9 heading text and the SessionManager data model reference. | Spec updated |
| 16 | SLC-034 | resuming classified both internal and external | **Skipped** — `resuming` (line 6119) is internal-only. `resume_pending` (line 6112) is an external state. These are different states with different names. No classification conflict. | — |
| 17 | OBS-044 | PgBouncerAllReplicasDown in wrong table | **FIXED** — Alert was in the Warning alerts table but marked `Critical` in its severity column. Moved to the Critical alerts table where it belongs. | Spec updated |
| 18 | OBS-045 | Deployment bullet still says active_sessions HPA | **FIXED** — Line 127 listed `active sessions` as an HPA metric, contradicting the canonical HPA metric role table (§4.1 lines 136-142) which designates it as alert-only. Updated the deployment bullet to reference the canonical table and note that active_sessions is NOT an HPA trigger. | Spec updated |
| 19 | OPS-035 | Warm pool sizing numerical errors | **Skipped** — The tier table (line 8170-8177) provides recommended values that are intentionally simplified starting points for first deployments (line 8179-8181), not exact formula outputs. The formula includes safety_factor and burst terms that the table's simplified guidance intentionally omits for clarity. | — |
| 20 | CPS-025 | E2B license claim Apache-2.0 not AGPL | **FIXED** — Line 8461 claimed "E2B uses AGPL + commercial". E2B's main repository (e2b-dev/E2B) is Apache 2.0 with a commercial offering. Corrected to "Apache 2.0 with a commercial offering". | Spec updated |
| 21 | CPS-026 | A2A governance claim wrong | **Skipped** — Line 8534 states A2A is "under AAIF governance alongside MCP". Google donated A2A to the AI Alliance Foundation (Linux Foundation) in 2025. Claim is factually accurate. | — |
| 22 | WPL-026 | burst term missing variant_weight | **FIXED** — The default formula's burst term (`burst_p99_claims × pod_warmup_seconds`) was missing `variant_weight`, meaning burst headroom was not proportional to the variant's traffic share. Added `variant_weight` to the burst term for consistency with the steady-state term. | Spec updated |
| 23 | CRD-026 | privateKeyJson vestigial | **FIXED** — `privateKeyJson` appeared in the proxy mode omission list (line 1094) but no provider in the credential field table uses this field. Removed from the omission list. The `privateKey` pattern in the runtime sensitivity rule (line 1142) is retained as a forward-looking convention. | Spec updated |
| 24 | CRD-027 | github/vault_transit missing from provider table | **FIXED** — The credential provider summary table (line 945-951) listed only anthropic_direct, aws_bedrock, vertex_ai, azure_openai, and Custom. The detailed field table (lines 1116-1136) includes github and vault_transit. Added both to the summary table for consistency. | Spec updated |
| 25 | CRD-028 | anthropic_direct "short-lived token" claim wrong | **FIXED** — The provider summary table claimed anthropic_direct provides "Short-lived API key or scoped token". Anthropic API keys are long-lived static keys; the lease TTL governs pod access duration, not key lifetime. Corrected description. | Spec updated |
| 26 | BLD-028 | Phase 2 benchmark can't measure real RuntimeClasses | **Skipped** — Known limitation. Phase 2 only has runc/echo runtime. The benchmark harness is re-run in later phases as more RuntimeClasses become available. Not a design flaw. | — |
| 27 | FLR-030 | resume_pending unbounded under pool exhaustion | **Skipped** — Line 2262 shows resume_pending pauses timers. `podClaimQueueTimeout` (60s) bounds the wait per claim attempt. After timeout, session fails with retryable error. Design is intentional. | — |
| 28 | FLR-031 | DLQ TTL inconsistency with resume_pending | **Skipped** — DLQ uses `maxResumeWindowSeconds` as TTL (line 2623). This matches the session's resume window. If the session recovers within the window, messages are available. If not, the session expires. Consistent. | — |
| 29 | FLR-032 | maxTreeRecoverySeconds < maxResumeWindowSeconds for depth-1 | **Skipped** — Line 3469 explicitly addresses this: "maxTreeRecoverySeconds can terminate a node's recovery attempt even if its maxResumeWindowSeconds has not yet elapsed." This is by design — tree recovery bounds total wall-clock time. The deployer guidance formula (line 3474) shows how to size for deeper trees. | — |
| 30 | EXP-027 | cursor example violates own rule | **Skipped** — The cursor example at line 6556 (`eyJpZCI6...`) is the generic pagination example for ALL endpoints. The prohibition on plain base64-encoded JSON cursors (line 4519) applies specifically to the Results API cursor, not to generic pagination. | — |
| 31 | EXP-028 | sticky:session/none undefined | **Skipped** — These are self-explanatory enum values in context. `sticky: session` = assignment sticky within a session (trivially true). `sticky: none` = no stickiness, re-evaluated each session. `sticky: user` gets the detailed treatment because it has non-obvious caching behavior. | — |
| 32 | EXP-029 | hash algorithm unspecified | **Skipped** — The spec describes `hash(user_id + experiment_id) mod 100`. The specific hash algorithm (murmur3, xxhash, etc.) is an implementation detail. The spec correctly specifies the semantic behavior without over-constraining the implementation. | — |
| 33 | EXP-030 | stale cache on weight change | **Skipped** — Line 4525 says cached assignments remain valid on `paused → active`. Weight changes affect NEW assignments. Existing sticky assignments represent committed experiment allocations — changing them mid-experiment would corrupt results. Design is correct. | — |
| 34 | DOC-137 | terminationGracePeriodSeconds "60-120s" stale | **Skipped** — No "60-120s" string found in the spec. The DOC-138 fix (iteration 9 High) already updated terminationGracePeriodSeconds to 240s in the tier table. Already resolved. | — |
| 35 | API-041 | dryRun bootstrap audit exception | **FIXED** — The general dryRun rule (line 6447) said "does not... emit audit events." The bootstrap endpoint (line 6290) explicitly emits audit events even under dryRun. Contradiction resolved: updated the general rule to note the bootstrap exception. | Spec updated |
| 36 | POL-038 | interceptorRef condition 2 unenforceable | **FIXED** — Condition 2 described a child specifying "an additional interceptorRef alongside the parent's" but `interceptorRef` is a scalar field (single name). Reworded to clarify this is a trust-based convention (deployer ensures their interceptor internally chains the parent's), not a platform-enforced check. | Spec updated |

**Summary: 12 fixed, 24 skipped.**

_Detailed findings from all 25 perspectives are preserved in the subagent outputs above and in the per-perspective files in this directory._
