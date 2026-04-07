# Technical Design Review Findings — 2026-04-07 (Iteration 4)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 4 of 5
**Total findings:** 74 across 25 review perspectives
**Scope:** Critical, High, and Medium (no fixes applied this iteration — review only)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 11    |
| Medium   | 63    |
| Info     | 0     |

### Comparison Across Iterations

| Severity | Iter 1 | Iter 2 | Iter 3 | Iter 4 | Trend |
|----------|--------|--------|--------|--------|-------|
| Critical | 30     | 4      | 1      | 0      | Clean |
| High     | 105    | 26     | 2      | 11     | ↓90%  |
| Medium   | 138    | 61     | 0*     | 63     | Carry-forward |
| Total    | 353    | 137    | 3      | 74     | ↓79%  |

*Iteration 3 was scoped to Critical/High only.

### Key Observations

1. **Zero Critical findings** — The spec is clean at Critical severity for the first time.
2. **11 High findings** — Mix of carry-forwards from iter1 Medium (now elevated based on scale/security impact) and 1 regression (NET-021 Redis TLS port).
3. **63 Medium findings** — Predominantly carry-forwards from iter1 that were out of scope for iterations 2-3 (which fixed Critical+High only). These represent the remaining specification gaps.
4. **No regressions from iter3 fixes** — The 3 iter3 fixes are all confirmed clean.

---

## High Findings

| # | ID | Perspective | Finding | Section |
|---|-----|-------------|---------|---------|
| 1 | NET-021 | Network | Redis TLS port regression in lenny-system NetworkPolicy table (6379 instead of 6380) | 13.2, 10.3 |
| 2 | PRT-020 | Protocol | Annotated protocol trace contains invalid `delivery: "at-least-once"` value | 15.4.1 |
| 3 | SCH-029 | Schema | `EvalResult.scores` JSONB has no minimum schema or dimension vocabulary | 10.7 |
| 4 | SLC-026 | Session | `await_children` re-attach protocol after parent resume unspecified | 8.8, 8.10 |
| 5 | DEL-021 | Delegation | Detached children's own cascade behavior and budget-return unspecified | 8.10 |
| 6 | MSG-023 | Messaging | `lenny/request_input` expiry not surfaced to awaiting parent | 8.8, 9.2 |
| 7 | BLD-021 | Build | Real-LLM testing precedes auth completion — Phase 5.75 gate is documentation-only | 18 |
| 8 | FLR-021 | Failure | Coordinator hold-state timeout produces orphaned pod with no session record | 10.1 |
| 9 | WPL-021 | Warm Pool | `sdkWarmBlockingPaths` default not overridable — derived runtimes with CLAUDE.md always demote | 6.1 |
| 10 | CRD-021 | Credentials | Proactive lease renewal race — exhaustion silently falls through to fault rotation | 4.9 |
| 11 | EXP-021 | Experiments | External targeting `sticky: user` cache has no invalidation path | 10.7 |

---

## Medium Findings by Perspective

### Kubernetes Infrastructure (K8S)
- **K8S-020** Controller anti-affinity remains advisory — not enforced by Helm chart (§4.6.1, 17.8)
- **K8S-021** agent-sandbox CRD presence not validated at controller startup (§4.6)
- **K8S-022** Kata node pool taint has no post-scheduling validation (§5.3, 17.2)

### Network Security (NET)
- **NET-022** Internet egress CIDR exclusions require manually-maintained Helm values with no validation (§13.2)
- **NET-023** PgBouncer-to-Postgres NetworkPolicy not specified in lenny-system table (§13.2)

### Scalability (SCL)
- **SCL-023** Experiment targeting webhook on session creation hot path has no circuit breaker (§10.7)
- **SCL-024** KEDA and standalone HPA coexistence not specified (§10.1, 17.8)
- **SCL-025** `statusUpdateDeduplicationWindow` controller flag undocumented (§4.6.1, 17.8)

### Security (SEC)
- **SEC-029** Agent-initiated URL-mode elicitation allowlist requires no domain constraint (§9.2)
- **SEC-030** Session inbox `from` field origin not documented as security invariant (§7.2)
- **SEC-031** Webhook callbackUrl DNS pinning has no re-validation at delivery time (§14)
- **SEC-032** No content-type/MIME validation on uploaded files (§7.4, 13.4)
- **SEC-033** Admin bootstrap endpoint has no explicit audit logging specification (§15.1, 17.6)
- **SEC-034** Semantic cache `user_id` scoping not required for pool-scoped credentials (§4.9)

### Compliance (CMP)
- **CMP-020** No compliance controls mapping for SOC2/HIPAA/FedRAMP (§12.8)
- **CMP-021** KMS key residency not required to match dataResidencyRegion (§12.8, 4.3)
- **CMP-022** Erasure SLA has no hard stop on new data processing for the subject (§12.8)
- **CMP-023** Task-mode residual state can retain PHI without routing enforcement (§5.2, 12.9)

### Policy Engine (POL)
- **POL-024** Circuit breaker specification is a bare stub (§11.6)
- **POL-025** Canonical timeout table is incomplete — 9+ operation timeouts only in prose (§11.3)
- **POL-026** `maxDelegationPolicy` field in delegation lease is undefined (§8.3)
- **POL-027** Interceptor timeout has no distinct error code (§4.8)
- **POL-028** Budget return script does not specify in-flight usage quiescence (§8.3)
- **POL-029** DelegationPolicy tag evaluation uses live labels — policy window undocumented (§8.3)

### Protocol Design (PRT)
- **PRT-021** `publishedMetadata` auto-generation contradicts opaque pass-through design (§5.1, 21.1)
- **PRT-022** MCP feature dependency vs adapter-layer version not distinguished (§15.2, 15.5)
- **PRT-023** OpenAI Completions adapter lifecycle limitations not declared in AdapterCapabilities (§15)

### Schema Design (SCH)
- **SCH-030** DelegationLease budget fields have no overflow semantics (§8.3)
- **SCH-031** Capability inference default for unannotated tools is counterintuitively `admin` (§5.1)
- **SCH-032** BillingEvent sequence_number gap-detection remediation undefined (§11.2.1)
- **SCH-033** Adapter manifest version is integer not semver; minPlatformVersion absent (§4.7)

### API Design (API)
- **API-027** OpenAPI spec has no published well-known URL (§15.1, 15.5)
- **API-028** `RESOURCE_HAS_DEPENDENTS` details omit per-resource IDs (§15.1)
- **API-029** Sortable fields not enumerated per resource type (§15.1)
- **API-030** No PATCH endpoints for complex admin resources (§15.1)

### Developer Experience (DXP)
- **DXP-022** No "For Runtime Authors: Start Here" entry in §1 (§1, 15.4.5)
- **DXP-023** Local dev does not document custom runtime substitution (§17.4)
- **DXP-024** Abstract Unix socket transport documented without macOS compatibility note (§15.4.3, 4.7)

### Session Lifecycle (SLC)
- **SLC-027** Session `created` state has no maximum TTL — pod and credential held indefinitely (§15.1, 7.1)
- **SLC-028** `resuming` timeout and `coordinatorHoldTimeoutSeconds` gap (§6.2, 10.1)

### Delegation (DEL)
- **DEL-022** `maxTreeRecoverySeconds` default shorter than `maxResumeWindowSeconds` — no formula (§8.10, 7.3)
- **DEL-023** No cycle detection in delegation target resolution (§8.2, 8.3)
- **DEL-024** Detached orphan pods not counted toward concurrency quota (§8.10)

### Messaging (MSG)
- **MSG-024** `ready_for_input` signal undefined for concurrent tool execution (§7.2)
- **MSG-025** `await_children(mode: any)` cascade on parent completion unspecified (§8.8, 8.5)
- **MSG-026** Sibling membership instability — no documented limitation (§7.2)

### Operator Experience (OPS)
- **OPS-024** `lenny-ctl` command surface undocumented (throughout)
- **OPS-025** Scale-to-zero cron timezone unspecified (§5.2)
- **OPS-026** cert-manager minimum version not specified (§10.3, 17.6)

### Storage (STR)
- **STR-021** `last_message_context` 64KB TEXT triggers TOAST — contradicts §12.1 principle (§4.4)
- **STR-022** GC cycle interval tier-configurability incomplete (§12.5, 17.8)
- **STR-023** Custom semantic cache implementations have no runtime tenant enforcement (§4.9, 9.4)

### Multi-Tenancy (TNT)
- **TNT-019** `session_eviction_state` table has no tenant_id column or RLS policy (§4.4, 12.3)
- **TNT-020** Detached orphan sessions exempt from quota enforcement (§8.10)
- **TNT-021** Tenant deletion lifecycle has no SLA or overdue alert (§12.8)

### Observability (OBS)
- **OBS-027** Budget operation OTel spans absent from tracing specification (§8.3, 16.3)
- **OBS-028** Metric name inconsistency for pod claim wait time (§16.1, 17.8.2)
- **OBS-029** Memory store operation metrics absent from canonical table (§9.4, 16.1)
- **OBS-030** PgBouncer alerts missing from §16.5 canonical alert table (§12.3, 16.5)

### Build Sequence (BLD)
- **BLD-022** Phase 16 after Phase 15 despite PoolScalingController needing both (§18)

### Failure Modes (FLR)
- **FLR-022** Dual-store forced termination event delivery gap (§10.1)

### Warm Pool (WPL)
- **WPL-022** Pool fill grace period not applied during experiment re-activation (§4.6.1, 16.5)

### Credentials (CRD)
- **CRD-022** `maxRotationsPerSession` not per-provider — one noisy provider blocks all (§4.9)

### Execution Modes (EXM)
- **EXM-026** Concurrent-workspace slot retry has no atomic reservation (§5.2)

### Experiments (EXP)
- **EXP-022** Results API cursor leaks cross-variant ordering information (§10.7)

### Document Quality (DOC)
- **DOC-023** §17.8 referenced 30+ times but content partially specified (§17.8)

### Competitive (CPS)
- **CPS-021** §23.1 MCP Tasks differentiator needs clarification on internal vs external (§23.1)
