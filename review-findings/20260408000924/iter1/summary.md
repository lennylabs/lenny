# Technical Design Review Findings — 2026-04-08 (Iteration 1)

**Document reviewed:** `technical-design.md` (8,735 lines)
**Review framework:** `review-povs.md` (25 perspectives)
**Iteration:** 1 of 8 — continuation from 14 prior iterations
**Total findings:** ~217 across 25 review perspectives
**Deduplicated findings:** ~211 (6 cross-perspective duplicates removed)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 4     |
| Medium   | ~147  |
| Low      | ~65   |
| Info     | 1     |

### Carried Forward from Iteration 14 (still present / skipped)

| # | ID | Finding | Status |
|---|------|---------|--------|
| 1 | K8S-035 / NET-034 | `lenny-pool-config` ghost webhook — referenced but never formally defined | Skipped |
| 2 | WPL-030 | Failover formula 25s — intentionally conservative | Skipped |
| 3 | DEL-039 | `settled=all` redundant mode in `lenny/await_children` | Skipped |
| 4 | FLR-038 | Redis runbook references phantom metrics/alerts/config params | Skipped |
| 5 | CMP-041 | Salt rotation cannot re-pseudonymize billing records | Skipped |
| 6 | EXP-033B | Multi-variant hash bucketing formula undefined | Carried forward |
| 7 | EXP-033C | Gateway creating materialized view at runtime contradicts DDL-through-migrations pattern | Carried forward |
| 8 | POL-041 | Cross-phase priority ordering error (re-reported as POL-045) | Carried forward |
| 9 | MSG-037 | `delivery_receipt` schema omits `error` from populated-status list | Carried forward |
| 10 | CRD-031/032 | Secret shape table missing rows for `vault_transit` and `github` providers | Skipped |
| 11 | DOC-036 | Orphaned footnote number ⁴ | Carried forward |

### Cross-Perspective Duplicates (removed from totals)

| Primary | Duplicate | Topic |
|---------|-----------|-------|
| OBS-039 | WPL-032 | `SandboxClaimGuardUnavailable` missing from Section 16.5 |
| OBS-040 | WPL-033 | `lenny_warmpool_idle_pods` vs `lenny_warmpool_ready_pods` naming |
| STR-043 | POL-046 | Storage quota counter missing from Redis failure table |
| API-058 | DOC-042 | Billing-corrections reject endpoint missing from Section 15.1 |
| CPS-039 | DOC-040 | "Phase 17 deliverables" should be "Phase 17a" |
| CPS-038 | BLD-044 | CONTRIBUTING.md published Phase 2 vs PR solicitation Phase 17a |

### High Findings

| # | ID | Perspective | Finding | Section |
|---|------|-------------|---------|---------|
| 1 | SCL-035 | Scalability | All performance targets are first-principles estimates lacking benchmark validation gates before GA | 6.3, 4.1, 16.5, 18 |
| 2 | OPS-045 | Operator Experience | `kubeApiServerCIDR` has no default and causes fail-closed webhook outage if wrong | 13.2, 17.6 |
| 3 | BLD-036 | Build Sequence | LLM Proxy subsystem has no phase assignment | 18, 4.1, 4.9 |
| 4 | EXM-040 | Execution Modes | `terminate(task_complete)` lifecycle protocol contradiction — exit vs stay-alive | 4.7, 5.2, 15.4.1 |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes Infrastructure (K8S)

### K8S-038. `lenny-system` namespace still missing PSS labels [Low]
**Section:** 17.2
PSS labels specified for agent namespaces but not for `lenny-system`. Defense-in-depth gap for high-privilege components.
**Recommendation:** Add `warn: restricted` and `audit: restricted` PSS labels to `lenny-system`.

### K8S-039. CRD admission webhook cross-resource `terminationGracePeriodSeconds` mechanism still unspecified [Low]
**Section:** 10.1
The webhook references the gateway pod spec's `terminationGracePeriodSeconds` but the mechanism for obtaining this cross-resource value is unspecified.
**Recommendation:** Read from a Helm-supplied webhook configuration constant; add preflight check confirming the values match.

### K8S-040. PoolScalingController default formula includes `variant_weight` for non-experiment pools [Medium]
**Section:** 4.6.2, 17.8.2
The default formula includes `variant_weight` but this is only meaningful for A/B experiment pools. Non-experiment pools have no defined default for this variable.
**Recommendation:** Remove `variant_weight` from the default formula; document it separately for variant pools only.

### K8S-041. PoolScalingController writes `SandboxWarmPool.status` in violation of SSA field ownership table [Medium]
**Section:** 6.1, 4.6.3
Section 6.1 says PoolScalingController sets `status.sdkWarmDisabled: true`, but Section 4.6.3's SSA table assigns `SandboxWarmPool.status.*` exclusively to WarmPoolController.
**Recommendation:** Move circuit-breaker state to `SandboxWarmPool.spec` (PoolScalingController's domain) or reassign status ownership with RBAC grants.

### K8S-042. `lenny-pool-config` ValidatingAdmissionWebhook referenced but never defined [Medium]
**Section:** 13.2, 4.6.1
Two references invoke this webhook but no definition exists — no manifest, rules, failurePolicy, or Helm template.
**Recommendation:** Either fully define the webhook or remove the two references and rely on other enforcement layers.

### K8S-043. `topologySpreadConstraints` for agent pods attributed to wrong controller [Medium]
**Section:** 5.2, 4.6.3
Section 5.2 attributes topology constraints to PoolScalingController, but pod spec is owned by WarmPoolController per the SSA table.
**Recommendation:** Clarify the two-step propagation: PSC writes to `SandboxTemplate.spec`, WPC copies to `Sandbox.spec`.

### K8S-044. PoolScalingController RBAC grants insufficient for `SandboxWarmPool.status` writes [Medium]
**Section:** 4.6.3, 6.1
RBAC grants are read-only on status subresources, but Section 6.1 requires a status write. Dependent on K8S-041 resolution.
**Recommendation:** If circuit-breaker state stays in `status`, add `patch` grant on `SandboxWarmPool/status`.

---

## 2. Security & Threat Modeling (SEC)

### SEC-038. `ReportUsage` trust model allows malicious runtime to underreport token consumption [Medium]
**Section:** 4.7, 11.2, 4.9
In direct delivery mode, the gateway has no independent token count — a malicious pod can underreport.
**Recommendation:** In proxy mode, extract token counts from proxied responses as authoritative. In direct mode, document as residual risk with anomaly detection metric.

### SEC-039. `uploadToken` has no documented TTL, scope binding, or cryptographic protection [Medium]
**Section:** 7.1, 7.4, 15.1
Format, TTL, session binding, and replay protection are all unspecified.
**Recommendation:** Specify as a short-lived signed token (HMAC-SHA256) with explicit TTL, invalidated after `FinalizeWorkspace`.

### SEC-040. `respond_to_elicitation` does not specify session-scoped authorization check [Medium]
**Section:** 9.2
No validation that `elicitation_id` belongs to the calling session — a foreign client could inject responses.
**Recommendation:** Validate `(session_id, user_id, elicitation_id)` triple; return 404 for foreign IDs.

### SEC-041. `allowSymlinks: true` archive symlinks re-resolved at promotion time against new root [Medium]
**Section:** 7.4
Symlinks validated against `/workspace/staging` may escape `/workspace/current` after promotion.
**Recommendation:** Re-validate all symlinks after staging→current promotion.

### SEC-042. OAuth connector flow lacks PKCE and `state` parameter anti-CSRF protection [Medium]
**Section:** 9.3, 9.4
No `state` parameter or PKCE in the OAuth flow, enabling CSRF and token injection attacks.
**Recommendation:** Generate per-request `state` bound to session; require PKCE (S256) for public clients.

### SEC-043. gVisor `SO_PEERCRED` semantics remain unvalidated — nonce-only fallback weakens adapter-agent boundary [Medium]
**Section:** 4.7, 13.1
If gVisor diverges, the nonce-only mode is activated indefinitely with no escalation path.
**Recommendation:** Add a Phase 3.5 hard gate; supplement nonce with per-connection challenge-response if SO_PEERCRED fails.

### SEC-044. Pre-upload storage quota check trusts client-supplied `Content-Length` [Medium]
**Section:** 11.2, 7.4
A client can declare a small Content-Length but stream more data, bypassing the pre-check.
**Recommendation:** Enforce `io.LimitedReader` hard cap on inbound body based on remaining quota.

### SEC-045. Task-mode scrub does not address `shmget`-allocated POSIX shared memory segments [Medium]
**Section:** 5.2, 13.1
POSIX shared memory segments persist across task boundaries — documented residual risk with no mitigation.
**Recommendation:** Add `ipcrm -m` step to scrub procedure; verify gVisor IPC namespace scoping for gVisor pods.

### SEC-046. Delegation chain `contentPolicy.interceptorRef` does not apply to elicitation content flowing upward [Low]
**Section:** 13.5, 9.2
Elicitation content propagating up the chain is not subject to content scanning.
**Recommendation:** Add elicitation to the residual risk list; consider a `PreElicitationForward` interceptor phase.

### SEC-047. Direct-mode token budget over-run bounded only by advisory guidance [Low]
**Section:** 8.3
In direct mode, over-run window multiplies with delegation depth. Only guidance, not enforcement.
**Recommendation:** Add `maxOverrunFactor` to `DelegationPolicy`; document residual risk more prominently.

---

## 3. Network Security (NET)

### NET-037. `lenny-drain-readiness` webhook NetworkPolicy blocks its required gateway callback [Medium]
**Section:** 13.2, 12.5
No egress rule for the webhook to reach the gateway's `/internal/drain-readiness`. Under default-deny, all pod evictions are permanently blocked.
**Recommendation:** Add egress from admission-webhook pods to gateway internal port; add corresponding gateway ingress rule.

### NET-038. Gateway ingress from Ingress controller namespace has no specified selector [Medium]
**Section:** 13.2
No Helm value or YAML for the ingress namespace selector. If wrong, gateway is unreachable from the internet.
**Recommendation:** Add `{{ .Values.ingressControllerNamespace }}` Helm value; include gateway ingress NetworkPolicy YAML.

### NET-039. Gateway `lenny-system` NetworkPolicy has no egress rule for in-cluster external interceptor gRPC calls [Medium]
**Section:** 13.2, 4.8
External interceptors are gRPC services, but the gateway's egress explicitly excludes cluster pod CIDRs.
**Recommendation:** Add a mechanism for deployers to declare interceptor namespaces; render corresponding gateway egress rules.

### NET-040. SPIFFE trust domain `lenny` is hardcoded — collides across co-located deployments [Medium]
**Section:** 10.3
Two Lenny deployments sharing a cluster and CA have overlapping trust domains, enabling cross-deployment pod impersonation.
**Recommendation:** Add Helm value `global.spiffeTrustDomain`; add preflight warning for default value in shared clusters.

### NET-041. `provider-direct` CIDR staleness has no drift detection or alerting [Low]
**Section:** 13.2
`internet` profile has drift detection, but `provider-direct` does not despite equivalent accuracy requirements.
**Recommendation:** Add `ProviderCIDRStale` warning alert; point deployers to provider CIDR update feeds.

### NET-042. Dedicated CoreDNS `filter` plugin does not address CNAME-chain exfiltration [Low]
**Section:** 13.2
Filter blocks record types but not query-label or CNAME-chain based exfiltration channels.
**Recommendation:** Document as residual risk with rate-limiting bound on bandwidth.

---

## 4. Scalability & Performance (SCL)

### SCL-035. All performance targets are first-principles estimates lacking benchmark validation gates before GA [High]
**Section:** 6.3, 4.1, 16.5, 18
Section 16.5 SLO table presents provisional values without a "provisional" label.
**Recommendation:** Add "PROVISIONAL" callout to Section 16.5 SLO table; emit startup warning when defaults are unchanged.

### SCL-036. `minReplicas` burst-absorption formula promised by Section 10.1 is missing from Section 17.8 [Medium]
**Section:** 10.1, 17.8.2
Section 10.1 promises a formula in 17.8 for non-KEDA Tier 3. It doesn't exist.
**Recommendation:** Add the formula with worked examples for both KEDA and Prometheus Adapter paths.

### SCL-037. etcd write rate at Tier 3 extrapolates to ~800+ writes/s but no write ceiling or alert is defined [Medium]
**Section:** 4.6.1, 17.8.2, 16.5
No etcd write ceiling, write-latency alert, or work queue backlog metric for Tier 3.
**Recommendation:** Add Tier 3 etcd write rate estimate; add `EtcdWriteLatencyHigh` alert; add `ControllerWorkQueueDepth` metric.

### SCL-038. Tier 3 "linear horizontal scaling only" claim has undocumented prerequisite: LLM Proxy extraction [Medium]
**Section:** 2, 4.1, 16.5
Without extraction, `maxSessionsPerReplica` is 200, requiring 50 replicas (above HPA max 30).
**Recommendation:** Qualify the claim with the LLM Proxy extraction prerequisite.

### SCL-039. Session creation rate SLO (200/s at Tier 3) has no end-to-end latency budget [Medium]
**Section:** 16.5, 7.1, 4.1
Creation rate is informational only — no SLO, burn-rate alert, or throughput constraint backs it.
**Recommendation:** Add session creation P99 latency SLO with burn-rate alert.

### SCL-040. Postgres write ceiling relies on instance-class estimates not validated against actual workload [Medium]
**Section:** 12.3, 17.8.2
Only ~23% margin at Tier 3; RLS trigger overhead and quota flush patterns not captured.
**Recommendation:** Add Lenny-specific write-pattern benchmark to Phase 2/13.5; add burst IOPS alert.

### SCL-041. Delegation fan-out with `orchestrator` preset creates unquantified warm pool demand [Medium]
**Section:** 8.3, 16.5, 17.8
10,000 sessions × 10 children = 110,000 pods, far exceeding `minWarm: 1050`.
**Recommendation:** Add delegation fan-out sizing formula to Section 17.8.

### SCL-042. Quota drift bound of ~30,000 requests at Tier 3 has no per-tenant impact analysis [Medium]
**Section:** 11.2, 12.4, 17.8
30,000-request overshoot represents 300× a 100-req/min limit during a 60s outage.
**Recommendation:** Document effective drift after applying `per_replica_hard_cap` and `quotaFailOpenCumulativeMaxSeconds`.

### SCL-043. Redis Lua script serialization under high delegation fan-out has no cross-tenant contention model [Medium]
**Section:** 8.3, 12.4
500 concurrent delegations × 100μs = 50ms aggregate blocking — exceeds `LeaseStore` 5ms SLO.
**Recommendation:** Add aggregate Lua contention analysis; update Redis instance separation trigger.

### SCL-044. No throughput SLO for warm pool replenishment rate under delegation-driven demand spikes [Low]
**Section:** 4.6.2, 16.5, 17.8
No alert for the case where the claim queue is saturated AND all pods are claimed.
**Recommendation:** Add `WarmPoolCapacityExceeded` alert for sustained demand exceeding pool capacity.

### SCL-045. Controller status update rate limiter (120 QPS at Tier 3) may starve time-critical state transitions [Low]
**Section:** 4.6.1, 17.8.2
Terminal state updates (pod failure detection) could be queued behind 10,000 other status updates.
**Recommendation:** Define a high-priority rate limiter bucket for terminal state transitions.

### SCL-046. No analysis of gateway → pod gRPC connection count ceiling and FD limit at Tier 3 [Low]
**Section:** 4.1, 4.7, 17.8
10,000 concurrent gRPC streams require FD limits above default Linux `ulimit -n` of 1,024.
**Recommendation:** Add FD sizing note to Section 17.1; recommend minimum 65,536 at Tier 3.

---

## 5. Protocol Design (PRT)

### PRT-037. MCP nonce injected via non-standard `initialize` field path [Medium]
**Section:** 15.4.2, 4.7
`_lennyNonce` uses undefined MCP extension locations with no migration path.
**Recommendation:** Define a canonical injection location with migration path to out-of-band channel.

### PRT-038. A2A agent card auto-generation produces stale snapshot with no invalidation path [Medium]
**Section:** 5.1, 21.1
Stored card format is frozen at registration time with no bulk-regeneration mechanism.
**Recommendation:** Add `generatedAt`/`generatorVersion` fields; add admin endpoint for bulk regeneration.

### PRT-039. `AdapterCapabilities.SupportsElicitation` not propagated to discovery output [Medium]
**Section:** 15, 9.2, 21.1
Discovery consumers have no way to know that elicitation-dependent workflows will fail.
**Recommendation:** Extend `HandleDiscovery` contract so each adapter can inject capability annotations.

### PRT-040. A2A `/.well-known/agent.json` discovery URL not addressed [Medium]
**Section:** 5.1, 15, 21.1
External A2A callers performing standards-based discovery cannot find Lenny runtimes.
**Recommendation:** Add `GET /.well-known/agent.json` endpoint aggregating public A2A cards.

### PRT-041. Agent Protocol section has no state mapping, content-type mapping, or fidelity matrix entry [Low]
**Section:** 21.3
AP section is a stub compared to the detailed A2A specification.
**Recommendation:** Expand minimally with state mapping, step execution model, and placeholder fidelity matrix column.

### PRT-042. Intra-pod MCP `2024-11-05` backward compatibility has no removal trigger [Medium]
**Section:** 15.2, 15.5
Gateway has a rolling two-version policy, but intra-pod servers have no analogous lifecycle.
**Recommendation:** Add note that intra-pod version support follows the same rolling policy as the gateway.

### PRT-043. `ExternalProtocolAdapter` interface has no versioning guarantee or stability tier [Low]
**Section:** 15, 15.5
Third-party adapter authors have no protection against breaking interface changes.
**Recommendation:** Assign a stability tier (`beta`/`stable`) in Section 15.5; add to breaking-change definition.

---

## 6. Developer Experience (DXP)

### DXP-037. Echo runtime description and pseudocode are inconsistent [Medium]
**Section:** 15.4.4
Narrative promises timestamp and session ID; pseudocode only uses sequence number.
**Recommendation:** Update narrative to match pseudocode.

### DXP-038. Echo runtime pseudocode has unsafe `input[0].inline` access [Low]
**Section:** 15.4.4
Direct index access fails on empty arrays or non-text parts.
**Recommendation:** Replace with safe iteration over parts array.

### DXP-039. Roadmap step 3 sends Minimum-tier authors to the adapter's state machine [Medium]
**Section:** 15.4.5, 15.4.2
Minimum-tier authors don't implement the adapter — the step is misleading.
**Recommendation:** Rewrite annotation to explain binary perspective, or move to Standard/Full tier.

### DXP-040. Standard-tier roadmap step 7 annotation obscures relevant content in Section 4.7 [Medium]
**Section:** 15.4.5, 4.7
Annotation emphasizes the gRPC RPC table (irrelevant to binary authors) over the manifest and lifecycle schemas.
**Recommendation:** Rewrite to direct authors to the relevant subsections and away from the gRPC table.

### DXP-041. No sample runtime for Standard or Full tier [Medium]
**Section:** 15.4.4, 15.4.5
Only Minimum-tier pseudocode exists. Standard introduces a Lenny-specific nonce handshake; Full introduces a custom lifecycle channel.
**Recommendation:** Add annotated pseudocode samples for Standard-tier (nonce + MCP) and Full-tier (lifecycle channel).

### DXP-042. Integration tier is determined behaviorally but is not documented as such [Low]
**Section:** 15.4.3, 5.1
No `integrationTier` field exists; tier is inferred from runtime behavior but this isn't stated.
**Recommendation:** Add "Tier determination" note explaining behavioral inference.

### DXP-043. Adapter-local tool catalog for Minimum-tier runtimes is absent [Medium]
**Section:** 15.4.1, 15.4.3
`tool_call` for adapter-local tools is mentioned but no tool names, schemas, or discovery mechanism exist.
**Recommendation:** Add an "Adapter-Local Tool Reference" table, or remove the mention if tools don't exist.

### DXP-044. `DemoteSDK` requirement is misframed for runtime authors [Low]
**Section:** 15.4 preamble
Framed for embedded adapter authors, but most runtime authors use the sidecar model.
**Recommendation:** Reframe for sidecar model; cross-reference sidecar vs embedded trade-offs.

### DXP-045. `from_mcp_content()` SDK import path advertises non-existent package [Low]
**Section:** 15.4.1
Go import path is a Phase 2 deliverable but presented as if available now.
**Recommendation:** Mark as "not yet published; Phase 2 deliverable."

### DXP-046. stdout flushing requirement not documented for JSON Lines protocol [Medium]
**Section:** 15.4.1, 15.4.4
Many languages buffer stdout by default; missing flush causes silent hangs.
**Recommendation:** Add explicit flushing requirement with language-specific guidance.

---

## 7. Operator & Deployer Experience (OPS)

### OPS-044. Missing end-to-end "Day 0" installation walkthrough [Medium]
**Section:** 17.6, 18
No single sequential procedure from "empty cluster" to "first echo session."
**Recommendation:** Add a Day 0 walkthrough with minimal annotated `values.yaml`.

### OPS-045. `kubeApiServerCIDR` has no default and causes fail-closed webhook outage if wrong [High]
**Section:** 13.2, 17.6
Misconfiguration blocks all admission webhooks, halting warm pool replenishment.
**Recommendation:** Add cloud-specific discovery guidance; consider `0.0.0.0/0` as safe default for webhooks.

### OPS-046. No runbook for Token Service outage despite a Critical alert [Medium]
**Section:** 16.5, 17.7
`TokenServiceUnavailable` fires but no runbook exists for diagnosis or remediation.
**Recommendation:** Add Token Service outage runbook stub.

### OPS-047. PgBouncer saturation runbook is referenced but not defined [Medium]
**Section:** 16.5, 17.7
`PgBouncerPoolSaturated` alert cross-references a nonexistent runbook.
**Recommendation:** Add `pgbouncer-saturation.md` runbook stub.

### OPS-048. AdmissionWebhookUnavailable and CosignWebhookUnavailable have no remediation runbooks [Medium]
**Section:** 16.5, 17.7
Both are Critical alerts that halt warm pool replenishment; no operator guidance exists.
**Recommendation:** Add `admission-webhook-outage.md` runbook covering both webhooks.

### OPS-049. `lenny-ctl` command reference is missing key Day-2 commands [Medium]
**Section:** 24
Missing: session investigation, erasure job management, migration status, tenant deletion.
**Recommendation:** Add the missing command groups or mark them as future.

### OPS-050. Expand-contract migration strategy requires manual phase coordination with no tooling [Medium]
**Section:** 10.5
No mechanism for tracking migration phase state or blocking premature Phase 3 deployment.
**Recommendation:** Add `lenny-ctl migrate status` command; document Phase 3 gate query performance.

### OPS-051. Tier 2 → Tier 3 promotion has no operator checklist or decision guide [Medium]
**Section:** 4.1, 17.8.2
Prerequisites are scattered across dense technical sections, not structured for operator decisions.
**Recommendation:** Add "Tier Promotion Guide" subsection with ordered steps and go/no-go criteria.

### OPS-052. `make run` macOS limitation for Standard/Full-tier runtimes has no positive workaround path [Low]
**Section:** 17.4
No guidance on IDE debugging for the Tier 2 Docker Compose path on macOS.
**Recommendation:** Add brief debugging note for containerized adapter process.

### OPS-053. Bootstrap seed's `--force-update` blocked fields are underspecified [Low]
**Section:** 17.6
Only examples given, not an exhaustive table of blocked fields per resource type.
**Recommendation:** Replace examples with a definitive table.

### OPS-054. No guidance on rotating the initial `lenny-admin-token` without service disruption [Low]
**Section:** 17.6
Immediate invalidation creates a disruption window with no documented zero-downtime procedure.
**Recommendation:** Add a zero-downtime rotation procedure (create second user, update consumers, then rotate).

### OPS-055. `lenny-upgrade.sh` script referenced inconsistently between Sections 10.5 and 17.6 [Low]
**Section:** 10.5, 17.6
Section 17.6 shows manual steps without referencing the superior upgrade script.
**Recommendation:** Add prominent reference to the script in Section 17.6.

---

## 8. Multi-Tenancy (TNT)

### TNT-038. OIDC `tenant_id` claim extraction is underspecified [Medium]
**Section:** 4.2, 10.2
No configurable claim name, no behavior for absent/unrecognized claims.
**Recommendation:** Add `auth.tenantIdClaim` Helm value; define rejection behavior.

### TNT-039. Billing Redis stream not deleted in tenant deletion Phase 4 [Medium]
**Section:** 12.8, 11.2.1
The `t:{tenant_id}:billing:stream` and its consumer group are not in the deletion order.
**Recommendation:** Add explicit `DEL` and `XGROUP DESTROY` step in Phase 3.

### TNT-040. T4 tenant KMS key teardown absent from tenant deletion lifecycle [Medium]
**Section:** 12.8, 12.5
Per-tenant KMS key remains active after tenant deletion, violating least-privilege.
**Recommendation:** Add Phase 4 sub-step for KMS key deletion with provider-standard delay.

### TNT-041. Runtime and pool resources are platform-global but `tenant-admin` manages them as if tenant-scoped [Medium]
**Section:** 10.2, 4.2, 15.1
Runtime and pool records have no `tenant_id` field or RLS policy.
**Recommendation:** Clarify whether these are global with application-layer filtering, or tenant-scoped with RLS.

### TNT-042. `lenny-ctl tenant management` CLI has only `list` and `get` [Low]
**Section:** 24.9
Missing: `create`, `delete`, `rotate-erasure-salt`.
**Recommendation:** Add the missing CLI commands to match REST API coverage.

### TNT-043. `noEnvironmentPolicy` audit interceptor fires on every `PUT` including no-op writes [Low]
**Section:** 10.6
GitOps reconcile loops inflate the counter without state change.
**Recommendation:** Emit only on value transitions.

### TNT-044. Cross-tenant `allowCrossTenantReuse: true` allows T4-tier tenants into shared microvm pods [Medium]
**Section:** 5.2, 12.9, 6.4
T4 isolation requires dedicated node pools — cross-tenant reuse violates this.
**Recommendation:** Reject `allowCrossTenantReuse: true` when any associated tenant has `workspaceTier: T4`.

---

## 9. Storage Architecture (STR)

### STR-043. Storage quota Redis failure mode undefined [Medium]
**Section:** 11.2, 12.4
`storage_bytes_used` counter absent from Redis failure behavior table.
**Recommendation:** Add a storage quota row; recommended fail-closed or Postgres fallback.

### STR-044. Eviction context MinIO objects never explicitly deleted [Medium]
**Section:** 4.4, 12.5, 12.8
Eviction context objects at `/{tenant_id}/eviction/{session_id}/context` have no GC path.
**Recommendation:** Delete on session terminal state; add to GC sweep and erasure scope.

### STR-045. Partial checkpoint manifest objects lack a defined cleanup path [Medium]
**Section:** 4.4, 12.5
Partial MinIO parts from timed-out eviction checkpoints can become permanently orphaned.
**Recommendation:** Delete after successful/failed resume; add periodic sweep in GC job.

### STR-046. No MinIO write throughput budget or IOPS analysis for checkpoint load at Tier 3 [Medium]
**Section:** 12.5, 17.8
~17 checkpoints/s at ~1.7 GB/s upload bandwidth is unquantified.
**Recommendation:** Add MinIO throughput estimates to Section 17.8.

### STR-047. Cloud-managed object storage bucket versioning and lifecycle rules not specified [Medium]
**Section:** 12.5, 17.9
Self-managed MinIO rules documented but cloud equivalents (S3, GCS, Azure Blob) are not.
**Recommendation:** Add cloud-profile lifecycle rule requirements; add preflight validation.

### STR-048. T2 data "storage-layer encryption" requirement has no defined enforcement mechanism [Medium]
**Section:** 12.9
Redis encryption is "recommended" not "required"; no runtime enforcement for T2.
**Recommendation:** Clarify T2 means volume-level encryption; upgrade Redis language to "required."

### STR-049. GC leader election loss mid-cycle allows double-decrement of storage quota counter [Medium]
**Section:** 12.5
Redis counter decrement can happen before Postgres commit, causing double-decrement on crash recovery.
**Recommendation:** Issue Redis decrement only after Postgres commit succeeds.

### STR-050. `artifact_store` Postgres row not created for eviction context objects — quota not decremented [Medium]
**Section:** 4.4, 11.2, 12.5
Eviction context bytes are never reflected in `storage_bytes_used` counter.
**Recommendation:** Insert `artifact_store` rows for eviction context objects, or document as excluded.

### STR-051. "No shared RWX storage" non-goal not validated against real agent workflows [Low]
**Section:** 2, 8.7
No rationale documenting the trade-off or alternatives for collaborative multi-agent workflows.
**Recommendation:** Add brief rationale and alternatives documentation.

### STR-052. Redis ephemeral designation conflicts with billing stream durability requirement [Low]
**Section:** 12.4, 11.2.1
Billing stream functions as a durable intermediate buffer but Redis is labeled "ephemeral."
**Recommendation:** Note billing stream as exception; require Redis persistence for billing data.

---

## 10. Recursive Delegation (DEL)

### DEL-041. `extension-denied` flag has no specified persistence or durability guarantee [Medium]
**Section:** 8.6
On coordinator handoff, the denial flag resets silently — user rejection can be bypassed.
**Recommendation:** Store in `delegation_tree_budget` Postgres table; restore on handoff.

### DEL-042. `treeId` path parameter in the admin API is undefined [Medium]
**Section:** 8.6, 15.1
`DELETE /v1/admin/trees/{treeId}/...` uses `treeId` which is never defined anywhere.
**Recommendation:** Define as alias for `root_session_id` or add as a distinct identifier.

### DEL-043. `maxTreeMemoryBytes` Redis counter not in Postgres checkpoint or crash-recovery [Medium]
**Section:** 8.2, 11.2, 12.4
On Redis recovery, the memory counter resets to zero, allowing trees to exceed their cap.
**Recommendation:** Add to `delegation_tree_budget` checkpoint; restore from archived node count.

### DEL-044. `perChildMaxAge` extension does not retroactively extend running children's deadlines [Medium]
**Section:** 8.6
The extension is motivated by in-progress children, but only affects future children.
**Recommendation:** Add explicit note documenting this semantic mismatch.

### DEL-045. `maxParallelChildren` omitted from extendable/non-extendable field lists [Low]
**Section:** 8.6
Also missing: `maxDelegationPolicy`.
**Recommendation:** Add both to the appropriate list.

### DEL-046. `credentialPropagation: inherit` behavior undefined for cross-environment delegations [Medium]
**Section:** 8.3, 10.6
The child runtime may have different `supportedProviders`; behavior when pools don't match is unspecified.
**Recommendation:** Define cross-environment `inherit` semantics; specify rejection or fallback.

### DEL-047. Cross-environment bilateral declaration change semantics mid-tree unspecified [Medium]
**Section:** 10.6, 8.3
Whether checks are point-in-time or continuously enforced for grandchild delegations is unclear.
**Recommendation:** State explicitly whether cross-environment checks are live or snapshotted.

### DEL-048. Cycle detection does not cover external agent (connector/A2A) delegation targets [Medium]
**Section:** 8.2
External agents have no `(runtime_name, pool_name)` tuple for cycle detection.
**Recommendation:** Include external agents using `(connector_id, endpoint_url)` identity.

### DEL-049. Deep-tree recovery formula does not clearly connect to non-parallelism constraint [Low]
**Section:** 8.10
The formula already accounts for worst-case but the text doesn't explain this connection.
**Recommendation:** Add cross-reference note.

### DEL-050. Lease extension `rejectionCoolOffSeconds` timer basis and persistence unspecified [Low]
**Section:** 8.6
Wall-clock vs gateway-clock, coordinator-handoff survival, and reset conditions are all undefined.
**Recommendation:** Store as `deniedUntil: ISO8601` timestamps in the delegation record.

---

## 11. Session Lifecycle (SLC)

### SLC-044. `interrupt_request` `deadlineMs` timeout outcome undefined [Medium]
**Section:** 4.7, 6.2
If the runtime fails to send `interrupt_acknowledged` within `deadlineMs`, the session state is undefined.
**Recommendation:** Define adapter behavior (fallback to SIGTERM, or remain `running` with error).

### SLC-045. `starting` state absent from `maxIdleTimeSeconds` timer table [Low]
**Section:** 6.2
`maxSessionAge` includes `starting`; `maxIdleTimeSeconds` omits it.
**Recommendation:** Add `starting` row with behavior **Paused**.

### SLC-046. `resume_pending` state has no bounded wall-clock expiry [Medium]
**Section:** 6.2, 7.3
All timers are paused; warm pool exhaustion can cause indefinite stuck state.
**Recommendation:** Define that `maxResumeWindowSeconds` begins when `resume_pending` is entered.

### SLC-047. Derive lock released before copy — stale snapshot object race on live sessions [Medium]
**Section:** 7.1
TOCTOU window between lock release and MinIO copy; no error code for `NoSuchKey`.
**Recommendation:** Define `503 SNAPSHOT_UNAVAILABLE` error; document staleness bound.

### SLC-048. SSE buffer overflow silently drops connection with no pre-drop event [Medium]
**Section:** 7.2
Client has no structured signal that events were lost.
**Recommendation:** Send `error(CLIENT_BUFFER_OVERFLOW)` event before dropping, or document as silent.

### SLC-049. `finalizing` and `ready` states have no expiry timeout [Medium]
**Section:** 6.2, 7.1, 11.3
Sessions can be stuck indefinitely in these pre-run states.
**Recommendation:** Add `maxFinalizingTimeoutSeconds` and `maxReadyTimeoutSeconds`; clarify `terminate` preconditions.

### SLC-050. Coordinator handoff CAS retry loop has no retry limit [Low]
**Section:** 10.1
Under rapid failover, replicas could loop indefinitely.
**Recommendation:** Add 5-attempt limit with jittered backoff.

### SLC-051. Seal-and-export `draining` retry has no max duration or retry count [Medium]
**Section:** 7.1
Permanent MinIO unavailability holds pods in `draining` indefinitely.
**Recommendation:** Define `maxWorkspaceSealDurationSeconds` and `maxSealRetries`; add `WorkspaceSealStuck` alert.

---

## 12. Observability (OBS)

### OBS-038. Seven instrumented metrics missing from Section 16.1 canonical table [Medium]
**Section:** 4.6.1, 4.6.2, 8.3, 8.10, 16.1
Metrics used in runbooks and alerts but absent from the authoritative instrumentation checklist.
**Recommendation:** Add all seven metrics to Section 16.1.

### OBS-039. `SandboxClaimGuardUnavailable` and `OrphanTasksPerTenantHigh` absent from Section 16.5 alert table [Medium]
**Section:** 4.6.1, 8.10, 16.5
Both alerts are explicitly defined with "see Section 16.5" cross-references but don't appear there.
**Recommendation:** Add both to the Section 16.5 alert table.

### OBS-040. `lenny_warmpool_idle_pods` vs `lenny_warmpool_ready_pods` — two names for the same concept [Medium]
**Section:** 4.6.1, 10.7, 16.1, 16.5, 17.7
Section 16.1 has no canonical name; runbooks and experiments use different names.
**Recommendation:** Assign canonical name `lenny_warmpool_idle_pods`; update all references.

### OBS-041. `lenny_redis_lua_script_duration_seconds` has no alert rule despite a defined threshold [Medium]
**Section:** 8.3, 16.1, 16.5
5ms threshold defined as operational guidance but never formalized as an alert.
**Recommendation:** Add `DelegationLuaScriptLatencyHigh` warning alert.

### OBS-042. `lenny_session_last_checkpoint_age_seconds` has high-cardinality `session_id` label [Medium]
**Section:** 4.4, 16.1, 16.5
10,000 individual time series at Tier 3 with no aggregation guidance.
**Recommendation:** Add derived metric at Tier 3; include recommended PromQL.

### OBS-043. `lenny_warmpool_warmup_failure_total` is unnamed in Section 16.1, no direct alert for sustained failures [Medium]
**Section:** 4.6.1, 16.1, 17.7
Primary runbook diagnosis signal but no canonical definition or alert for sustained failure rate.
**Recommendation:** Add to Section 16.1; add `WarmPoolReplenishmentFailing` alert.

### OBS-044. Section 16.2 latency breakpoints do not map to named histogram metrics [Low]
**Section:** 16.2, 6.1
Four semantic timestamps are not cross-referenced to `phase` label values.
**Recommendation:** Add mapping table to Section 16.2.

### OBS-045. `DelegationBudgetNearExhaustion` alert lacks burn-rate companion [Low]
**Section:** 16.5
Threshold alert at 90% — no earlier signal for runaway budget consumption rate.
**Recommendation:** Add `DelegationBudgetBurnRateHigh` warning alert.

---

## 13. Compliance (CMP)

### CMP-043. Erasure receipt stored in `EventStore` which is itself erased [Medium]
**Section:** 12.8
Erasure receipt may be caught in subsequent user/tenant deletions.
**Recommendation:** Exempt `gdpr.*` event types from user-level deletion; retain for full audit period.

### CMP-044. Legal hold `note` required when setting hold, but no `reason` required when clearing [Low]
**Section:** 12.8, 15.1
Clearing a hold produces an audit record with no justification.
**Recommendation:** Make `note`/`reason` required for both set and clear.

### CMP-045. No GDPR Data Subject Access Request (DSAR) or right-to-portability support [Medium]
**Section:** 12.8, 15.1
GDPR Articles 15, 16, 20 are unaddressed.
**Recommendation:** Add export endpoint or document as deployer responsibility with guidance.

### CMP-046. No security breach / incident notification mechanism [Medium]
**Section:** 12.8, 11.7, 16.5
No incident taxonomy, first-responder steps, or breach notification runbook.
**Recommendation:** Add "Security Incident Response" section.

### CMP-047. `complianceProfile: fedramp` lacks impact-level distinction [Medium]
**Section:** 11.7, 16.4
FedRAMP Low/Moderate/High have materially different requirements.
**Recommendation:** Either expand to `fedramp-low`/`fedramp-moderate`/`fedramp-high` or document claimed baseline.

### CMP-048. Default retention preset named `soc2` is misleading for non-SOC 2 deployments [Low]
**Section:** 16.4
Default 365-day preset is labeled `soc2`, implying compliance readiness without the full control set.
**Recommendation:** Rename default to `standard` or `default-365d`.

### CMP-049. Erasure receipt durability not guaranteed for the regulatory retention period [Medium]
**Section:** 12.8, 16.4
365-day default audit retention is shorter than most GDPR enforcement windows (4-6 years).
**Recommendation:** Add minimum retention floor for compliance-class audit rows (e.g., 6 years).

### CMP-050. `billingErasurePolicy: exempt` not validated against `complianceProfile: hipaa` [Medium]
**Section:** 12.8, 11.7
Retaining original `user_id` in billing records may conflict with HIPAA minimum-necessary.
**Recommendation:** Emit warning/audit event when `exempt` is combined with regulated profiles.

### CMP-051. Session log retention (30 days) shorter than erasure SLA context [Low]
**Section:** 16.4, 12.8, 12.9
Session logs may be deleted before erasure verification is needed.
**Recommendation:** Clarify whether session logs contain PII; add to erasure scope if so.

### CMP-052. No explicit data minimization controls on interceptor payloads [Low]
**Section:** 4.8, 11.7
No enforcement that PII/PHI redaction interceptors are configured for regulated tenants.
**Recommendation:** Document deployer responsibility; optionally gate session creation with warning.

### CMP-053. Section 12.8 does not link compliance operations to SIEM requirement [Low]
**Section:** 11.7, 12.8
Operators reading Section 12.8 alone won't know erasure receipts require SIEM for tamper-evidence.
**Recommendation:** Add callout box cross-referencing Section 11.7 SIEM requirements.

---

## 14. API Design (API)

### API-054. `PUT` used for state-transition action on credential re-enable [Medium]
**Section:** 15.1
Every other action endpoint uses `POST`; this one uses `PUT`.
**Recommendation:** Change to `POST`.

### API-055. `GET /v1/admin/sessions/{id}` referenced but never defined [Medium]
**Section:** 24.3, 15.1
Operators directed to an endpoint that doesn't exist in the API table.
**Recommendation:** Add the endpoint or replace with client-facing equivalent.

### API-056. `DELETE /v1/admin/erasure-jobs/{job_id}/processing-restriction` carries a required request body [Medium]
**Section:** 15.1
Many HTTP clients silently drop DELETE bodies.
**Recommendation:** Change to `POST /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction`.

### API-057. Operational-plane items listed as API-managed have no corresponding admin endpoints [Medium]
**Section:** 15.1
~5 resource categories (Webhooks, Egress Profiles, Scaling Policies, Memory Store Config, User Role Assignments) have no API backing.
**Recommendation:** Add CRUD endpoints or move to Bootstrap plane.

### API-058. `POST /v1/admin/billing-corrections/{id}/reject` absent from admin API table [Medium]
**Section:** 15.1, 11.2.1
The `approve` endpoint is listed; `reject` is not.
**Recommendation:** Add the `reject` endpoint.

### API-059. Circuit breaker endpoints omitted from Section 15.1 admin API table [Low]
**Section:** 15.1, 11.6
Four endpoints defined in Section 11.6 but absent from the "includes all endpoints" table.
**Recommendation:** Add all four or remove the "includes all" claim.

### API-060. No `X-Request-ID` / correlation header contract defined [Low]
**Section:** 15.1, 15.2.1
Third-party CLI/UI authors have no standard correlation handle.
**Recommendation:** Define `X-Request-ID` header contract.

### API-061. MCP tool list is missing REST counterparts for `get_session_logs`, `get_token_usage` [Low]
**Section:** 15.2, 15.2.1
Contract test matrix omits these overlapping operations.
**Recommendation:** Add to contract test matrix.

### API-062. `dryRun` exceptions list omits upgrade action endpoints [Low]
**Section:** 15.1
Five `upgrade/*` POST actions not listed as exceptions.
**Recommendation:** Add to exceptions list or define `dryRun` semantics for them.

---

## 15. Competitive Positioning (CPS)

### CPS-038. `CONTRIBUTING.md` published at Phase 2 with no policy for unsolicited PRs before Phase 17a [Medium]
**Section:** 18, 23.2
Creates a ~15-phase window where the project looks open-source but doesn't want PRs.
**Recommendation:** Define explicit policy for unsolicited contributions during Phase 2–17a.

### CPS-039. "Phase 17 deliverables" — no such phase in build sequence [Low]
**Section:** 23.2
Should be "Phase 17a deliverables".
**Recommendation:** Fix the cross-reference.

### CPS-040. BDfN governance has two conflicting exit triggers [Medium]
**Section:** 23.2
Phase-based (ends Phase 4) vs contributor-based (3+ contributors, only reachable Phase 17a+).
**Recommendation:** Remove phase-based qualifier; rely solely on contributor-based criterion.

---

## 16. Warm Pool (WPL)

### WPL-032. `SandboxClaimGuardUnavailable` alert absent from Section 16.5 [Medium]
**Section:** 4.6.1, 16.5
Critical alert defined in prose with "see Section 16.5" but not in the table.
**Recommendation:** Add to Critical alerts table. *(Duplicate of OBS-039)*

### WPL-033. `lenny_warmpool_idle_pods` vs `lenny_warmpool_ready_pods` — two names for same metric [Medium]
**Section:** 16.1, 16.5, 17.7, 10.7
Section 16.1 has no canonical name at all.
**Recommendation:** Assign canonical name; unify all references. *(Duplicate of OBS-040)*

### WPL-034. `pod_startup_seconds` vs `pod_warmup_seconds` conflation risk in sizing guidance [Medium]
**Section:** 4.6.1, 4.6.2, 17.8.2
For SDK-warm pools the two values diverge significantly; inline example uses same value for both.
**Recommendation:** Add parenthetical note distinguishing the two for SDK-warm pools.

### WPL-035. `active → paused` experiment transition leaves warm pods with no eviction deadline [Medium]
**Section:** 4.6.2, 10.7
`minWarm` set to 0 but `maxWarm` unchanged; idle pods persist until cert expiry (4h).
**Recommendation:** Also set `maxWarm` to 0 on pause; restore on reactivation.

### WPL-036. SDK-warm circuit-breaker trips but existing SDK-warm idle pods not flushed [Medium]
**Section:** 6.1, 4.6.1
Pool remains mixed SDK-warm/pod-warm for up to 4h after circuit break.
**Recommendation:** Drain existing idle SDK-warm pods on circuit-breaker activation.

### WPL-037. `initialMinWarm` sizing formula in Section 10.7 omits burst term [Low]
**Section:** 10.7, 4.6.2
Deploys undersize `initialMinWarm` during the riskiest window (first burst).
**Recommendation:** Include burst term in the formula.

### WPL-038. `SDKWarmDemotionRateHigh` is a Kubernetes Event only, not a Prometheus alert [Low]
**Section:** 6.1, 16.5
SREs should receive this via normal alerting channels, not by checking K8s Events.
**Recommendation:** Add corresponding Prometheus alert to Section 16.5.

---

## 17. Credential Management (CRD)

### CRD-034. Direct-mode in-flight gate relies on undocumented `request_active` lifecycle flag [Medium]
**Section:** 4.7
No `request_active` message exists in the lifecycle channel schema.
**Recommendation:** Add `llm_request_started`/`completed` messages or document alternative.

### CRD-035. `anthropic_direct` proxy mode provides no real revocability for compromised keys [Medium]
**Section:** 4.9
In direct mode, the actual API key persists at the provider after revocation.
**Recommendation:** Document direct-mode residual risk; require provider-side key rotation step.

### CRD-036. Section 17.8 cross-reference for credential pool sizing is a dead end [Medium]
**Section:** 4.9, 17.8
"See Section 17.8" leads to nothing on credential sizing.
**Recommendation:** Add credential pool sizing formula and per-tier starting values.

### CRD-037. Audit event catalog omits most credential lifecycle events [Medium]
**Section:** 4.9, 11.2.1
~9 credential events have no canonical table or storage destination.
**Recommendation:** Add "Credential Audit Events" summary table.

### CRD-038. KMS key rotation procedure missing rollback and partial-failure handling [Medium]
**Section:** 4.9.1
No idempotency guarantee, rollback path, or verification step before old key is disabled.
**Recommendation:** Add idempotency guarantee, rollback path, old key retention policy.

### CRD-039. `maxRotationsPerSession` cross-provider budget has no multi-provider sizing guidance [Low]
**Section:** 4.9
Shared budget; one flaky provider can exhaust the entire budget.
**Recommendation:** Add guidance for multi-provider sessions.

### CRD-040. `credential_rotation_timeout` is a warning event, not an audit event [Low]
**Section:** 4.7, 4.9
Security signal that should be in the immutable audit trail.
**Recommendation:** Promote to audit event `credential.rotation_timeout`.

---

## 18. Content Model & Schemas (SCH)

### SCH-044. Translation Fidelity Matrix omits `Open Responses` adapter column [Medium]
**Section:** 15.4.1
First-class V1 adapter with no fidelity specification.
**Recommendation:** Add column or note that `OpenAI Completions` column covers both.

### SCH-045. `lenny-blob://` URI scheme omits session generation component [Medium]
**Section:** 15.4.1
A `ref` from generation 1 refers to different blob context than one from generation 3.
**Recommendation:** Add `gen=` query parameter or document content-addressed immutability.

### SCH-046. `MessageEnvelope` carries no `schemaVersion` field despite being persisted [Medium]
**Section:** 15.4.1, 15.5
All other persisted types have `schemaVersion`; `MessageEnvelope` does not.
**Recommendation:** Add `"schemaVersion": 1` to the schema.

### SCH-047. `WorkspacePlan` schema missing per-source conflict resolution mode [Medium]
**Section:** 14
No `onConflict` field when sources collide on the same path.
**Recommendation:** Add `onConflict: replace | skip | error` per source or at top level.

### SCH-048. `WorkspacePlan.setupCommands` per-command `timeoutSeconds` has no global default [Medium]
**Section:** 14
Optional field with no defined fallback when omitted.
**Recommendation:** Document the fallback explicitly.

### SCH-049. `RuntimeDefinition` merge table omits `capabilityInferenceMode` field [Medium]
**Section:** 5.1
Derived runtime authors can't determine merge behavior for this field.
**Recommendation:** Add to the Normative Merge Algorithm table.

### SCH-050. `TaskRecord` `messages` array entries lack per-entry `schemaVersion` [Medium]
**Section:** 8.8
Mid-session gateway upgrades can write different schema versions into the same record.
**Recommendation:** Add per-entry `schemaVersion` or document top-level governs all entries.

### SCH-051. `billing_correction` cross-version application guidance absent [Low]
**Section:** 11.2.1, 15.5
Consumers applying corrections across schema versions have no guidance.
**Recommendation:** Clarify that correction event's `schema_version` governs interpretation.

### SCH-052. `WorkspacePlan` schema omits concurrent-workspace slot-scoped materialization [Medium]
**Section:** 14, 5.3
No per-slot workspace differentiation is possible in the schema.
**Recommendation:** Document as out-of-scope or add `slotOverrides` field.

### SCH-053. `runtimeOptionsSchema` override rule allows silent breaking changes [Medium]
**Section:** 5.1, 14
"MAY NOT declare properties that the base schema forbids" has no defined validation algorithm.
**Recommendation:** Specify the exact validation algorithm and error code.

### SCH-054. `tool_result` messages carry no `from` envelope for connector attribution [Low]
**Section:** 15.4.1
Multi-connector runtimes can't determine which connector produced a result.
**Recommendation:** Add optional `sourceConnectorId` field or document correlation pattern.

### SCH-055. Billing event `schema_version` uses snake_case vs camelCase everywhere else [Low]
**Section:** 11.2.1, 15.5
Only billing events use `schema_version`; all others use `schemaVersion`.
**Recommendation:** Rename to `schemaVersion` for consistency.

---

## 19. Build Sequence (BLD)

### BLD-036. LLM Proxy subsystem has no phase assignment [High]
**Section:** 18, 4.1, 4.9
Core gateway component required for recommended multi-tenant configuration has no phase.
**Recommendation:** Add explicit phase between 5.5 and 6, or add to Phase 5.5 with limitations noted.

### BLD-037. Phase 5.5 "Limitations" omits proxy mode absence [Medium]
**Section:** 18, 4.9
Operators could deploy below recommended security baseline without knowing.
**Recommendation:** Add explicit limitation bullet about proxy mode deferral.

### BLD-038. `DelegationPolicy` created in Phase 3 but not evaluated until Phase 9 [Medium]
**Section:** 18, 4.8, 8.3
Resources exist for 6 phases without enforcement testing.
**Recommendation:** Add Phase 9 milestone gate testing policies from earlier phases.

### BLD-039. Audit logging arrives at Phase 13 but auditable events exist from Phase 5.5 [Medium]
**Section:** 18, 12.4, 11.7
7-phase gap where audit events are silently dropped.
**Recommendation:** Extract minimal audit sink to Phase 5.5 or document the gap.

### BLD-040. Phases 12a/12b/12c are forced sequential but are independent [Medium]
**Section:** 18
All three could proceed in parallel after Phase 11.5.
**Recommendation:** Add note that they may proceed in parallel.

### BLD-041. `noEnvironmentPolicy: deny-all` blocks all users during Phases 5–14 [Medium]
**Section:** 18, 4.2, 17.6
No environments exist before Phase 15; default deny-all makes the platform unusable.
**Recommendation:** Configure `allow-all` in bootstrap seed for pre-Phase 15 deployments.

### BLD-042. Phase 16 adds PoolScalingController logic after Phase 14.5 load-test sign-off [Medium]
**Section:** 18, 11.5, 16.5
Experiment integration invalidates the Phase 14.5 SLO baseline.
**Recommendation:** Move Phase 16 before 14.5, or add Phase 16.5 SLO re-validation.

### BLD-043. Client SDKs (Go + TypeScript) have no phase assignment [Medium]
**Section:** 18, 15.6
v1 deliverables with no build schedule.
**Recommendation:** Assign to Phase 6 or Phase 4.

### BLD-044. Phase 17a community launch realism — `CONTRIBUTING.md` published in Phase 2 but PR solicitation gated to Phase 17a [Low]
**Section:** 18, 23.2
**Recommendation:** Phase 2 `CONTRIBUTING.md` should include early-development note. *(Duplicate of CPS-038)*

### BLD-045. No phase for compliance validation (SOC 2, FedRAMP, HIPAA) [Low]
**Section:** 18, 11.7, 12.8
Compliance features span many phases but never validated end-to-end.
**Recommendation:** Add compliance profile smoke test phase.

### BLD-046. Managed Kubernetes CI gate for Phase 5.4 etcd encryption is undefined [Info]
**Section:** 18
Critical path bottleneck with no defined cloud-specific verification.
**Recommendation:** Add cloud-specific verification commands for Phase 5.4.

---

## 20. Failure Modes (FLR)

### FLR-040. Storage quota counter undefined behavior during Redis unavailability [Medium]
**Section:** 11.2, 12.4
`storage_bytes_used` absent from Redis failure behavior table.
**Recommendation:** Add row; recommended fail-closed.

### FLR-041. Eviction fallback chain silent data loss when both MinIO and Postgres fail [Medium]
**Section:** 4.4, 12.3, 12.4
Triple failure (MinIO + Postgres + node drain) leaves session unrecoverable with no signal.
**Recommendation:** Emit `session.lost` event; add `lenny_session_eviction_total_loss_total` counter.

### FLR-042. KMS/JWTSigner unavailability not addressed as a failure mode [Medium]
**Section:** 10.2
No fallback, circuit breaker, or degraded-mode definition for KMS outage.
**Recommendation:** Add JWT cache, circuit breaker, and `KMSSigningUnavailable` alert.

### FLR-043. `MinIOUnavailable` alert lacks canonical condition definition [Low]
**Section:** 4.4, 12.5, 16.5
Runbook references it but it's absent from the alert table.
**Recommendation:** Add to Section 16.5 with defined condition.

### FLR-044. Postgres failover window and eviction race not fully bounded [Medium]
**Section:** 12.3, 4.4
Checkpoint metadata record cannot be committed during Postgres failover.
**Recommendation:** Add retry within grace period; log MinIO object keys for manual recovery.

### FLR-045. Controller crash during active scale-down drain has no fencing [Medium]
**Section:** 4.6.1, 10.1
New leader may re-signal draining pods or miss mid-checkpoint pods.
**Recommendation:** Add generation-stamped drain operation record in `SandboxWarmPool.status`.

### FLR-046. GC job gateway-internal leader failure has no explicit recovery time bound [Low]
**Section:** 12.5
No early-warning signal for GC leader loss separate from backlog accumulation.
**Recommendation:** Add `lenny_gc_last_successful_cycle_age_seconds` gauge and `GCLeaderStuck` alert.

### FLR-047. `dualStoreUnavailableMaxSeconds` timer reset on coordinator crash creates unbounded degraded window [Medium]
**Section:** 10.1
Each replacement replica resets its timer, preventing graceful termination from ever triggering.
**Recommendation:** Anchor timer to session's last successful store interaction timestamp.

### FLR-048. CheckpointBarrier protocol has no defined behavior when Postgres is unavailable during preStop [Medium]
**Section:** 10.1
Barrier correctness depends on Postgres write; failure causes duplicate tool execution risk.
**Recommendation:** Include `last_tool_call_id` in CheckpointBarrierAck payload as fallback.

---

## 21. Experimentation (EXP)

### EXP-033B. Multi-variant hash bucketing formula undefined [Medium] — CARRIED FORWARD
**Section:** 10.7
Only binary control/treatment documented; multi-variant partitioning algorithm absent.
**Recommendation:** Provide full bucketing algorithm with cumulative weight partitioning.

### EXP-033C. Gateway creating materialized view at runtime contradicts DDL-through-migrations pattern [Medium] — CARRIED FORWARD
**Section:** 10.7
`CREATE MATERIALIZED VIEW` at runtime bypasses migration tooling.
**Recommendation:** Move creation to schema migration system.

### EXP-037. `dryRun` validation says "sum to 1.0" but constraint is "sum < 1.0" [Medium]
**Section:** 15.1, 4.6.2, 10.7
Contradictory validation rule; control group gets 0% traffic at 1.0.
**Recommendation:** Fix to "Σ variant_weights must be in [0, 1)".

### EXP-038. `EvalResult` records absent from GDPR erasure scope [Medium]
**Section:** 12.8, 10.7
Postgres records keyed by `session_id` not in erasure scope table or deletion order.
**Recommendation:** Add `EvalResult` to erasure scope table and Phase 4 deletion order.

### EXP-039. PoolScalingController base pool adjustment incorrect with multiple concurrent experiments [Medium]
**Section:** 4.6.2, 10.7
Per-experiment scoping causes last-write-wins; correct adjustment requires cross-experiment aggregation.
**Recommendation:** Clarify that `Σ variant_weights` aggregates across all active experiments on the same pool.

### EXP-040. `session:eval:write` permission not defined in RBAC framework [Low]
**Section:** 10.7, 9.1
Permission string referenced but absent from the permission matrix.
**Recommendation:** Add row to permission matrix.

### EXP-041. No assignment rule for sessions eligible for multiple simultaneous percentage-mode experiments [Medium]
**Section:** 10.7
Session record holds single `experimentContext`; no tie-breaking rule.
**Recommendation:** Define tie-breaking rule (priority, mutual exclusivity, or one-experiment-per-runtime).

---

## 22. Document Quality (DOC)

### DOC-036. Orphaned footnote ⁴ [Low] — CARRIED FORWARD
**Section:** 8.6
No corresponding ¹ ² ³.
**Recommendation:** Renumber to ¹ or convert to inline note.

### DOC-037. Error codes reference "Section 13 (Eval API)" — Section 13 is Security Model [Medium]
**Section:** 15.1
**Recommendation:** Change to "Section 10.7."

### DOC-038. Pagination note references "Section 11.5" — Section 11.5 is Idempotency [Medium]
**Section:** 15.1
**Recommendation:** Change to "Section 10.7."

### DOC-039. Platform operators entry point references "Section 17" for Admin API [Medium]
**Section:** 23.2
Section 17 is Deployment Topology; Admin API is Section 15.1.
**Recommendation:** Change to "Section 15.1."

### DOC-040. "Phase 17 deliverables" — no such phase exists [Low]
**Section:** 23.2
**Recommendation:** Change to "Phase 17a deliverables." *(Duplicate of CPS-039)*

### DOC-041. Stale CRD name `AgentSession` used in two places [Medium]
**Section:** 12.8
Should be `SandboxClaim` per the CRD mapping table.
**Recommendation:** Replace both occurrences.

### DOC-042. Two billing-correction admin endpoints missing from Section 15.1 [Medium]
**Section:** 11.2.1, 15.1
`reject` and `billing-correction-reasons` endpoints absent.
**Recommendation:** Add both. *(Duplicate of API-058)*

### DOC-043. `lenny_billing_correction_pending_total` metric absent from Section 16.1 [Low]
**Section:** 11.2.1, 16.1
Referenced by alert but not in canonical metrics table.
**Recommendation:** Add to Section 16.1.

### DOC-044. Imprecise cross-reference "Section 16" for DNS alerts [Low]
**Section:** 13.2
Should be "Section 16.5."
**Recommendation:** Fix cross-reference.

### DOC-045. SCL constraint label SCL-025 never defined [Low]
**Section:** 4.1, 4.8, 10.1
Gap in numbering sequence (SCL-023, SCL-024, SCL-026).
**Recommendation:** Define, retire, or renumber.

---

## 23. Messaging (MSG)

### MSG-037. `delivery_receipt` `reason` field populated-status list omits `error` [Medium] — CARRIED FORWARD
**Section:** 15.4.1
**Recommendation:** Add `error` to the populated-status list.

### MSG-041. `message_expired` async notification has no defined sender-tracking mechanism [Medium]
**Section:** 7.2
DLQ entries don't persist `sender_session_id`; gateway can't locate sender's event stream.
**Recommendation:** Persist `sender_id` in DLQ entries; define routing for external client senders.

### MSG-042. `delivery: "immediate"` exception for `input_required` undocumented in delivery enum table [Medium]
**Section:** 15.4.1, 7.2
Path 4 doesn't mention the `immediate` flag; no `reason` on the `queued` receipt.
**Recommendation:** Add explicit note to path 4; add `reason: "target_input_required"` to receipt.

### MSG-043. `inbox_cleared` event emitted on target's stream, not sender's [Medium]
**Section:** 7.2
Sender cannot observe it without subscribing to a foreign session's event stream.
**Recommendation:** Emit on sender's stream keyed by `targetSessionId`.

### MSG-044. Path 3 + path 4 overlap case unspecified [Medium]
**Section:** 7.2
Session simultaneously in `await_children` and child `input_required`; ordering undefined.
**Recommendation:** Clarify ordering between inbox-buffered messages and `await_children` events.

### MSG-045. Sibling task-tree snapshot race during rapid child spawning [Low]
**Section:** 7.2
Early siblings permanently miss late-spawned siblings.
**Recommendation:** Document coordinator pattern with concrete protocol for late arrivals.

### MSG-046. SSE buffer overflow behavior contradicts `OutboundChannel` back-pressure policy [Medium]
**Section:** 7.2, 15
Two incompatible trigger mechanisms (buffer-fill vs write-block) and conflicting buffer sizes.
**Recommendation:** Unify the mechanism; reconcile `MaxOutboundBufferDepth: 256` with "1000 events."

### MSG-047. `message_expired` event schema missing `reason: "session_terminal"` variant [Low]
**Section:** 7.2, 7.3
Only `"target_ttl_exceeded"` documented; `"session_terminal"` appears elsewhere.
**Recommendation:** Enumerate all reason values in the schema.

### MSG-048. `lenny/await_children` deadlock detection excludes `suspended` children [Medium]
**Section:** 8.8, 6.2
A perpetually-suspended child blocks `await_children` without triggering deadlock detection.
**Recommendation:** Extend detection to include `suspended` children; add timeout parameter.

### MSG-049. Delegation policy routing interaction with `siblings` messaging scope undefined [Medium]
**Section:** 7.2, 8.3
`SCOPE_DENIED` error conflates messaging scope with delegation policy.
**Recommendation:** Clarify `messagingScope` is the sole policy for message routing; rename error description.

### MSG-050. `GET /v1/sessions/{id}/messages` endpoint has no defined response schema [Low]
**Section:** 15.1
No field list, example, or schema reference.
**Recommendation:** Add response schema with pagination envelope and field definitions.

---

## 24. Policy Engine (POL)

### POL-041. Cross-phase priority ordering error [Medium] — CARRIED FORWARD (re-reported as POL-045)
**Section:** 4.8

### POL-044. `PostRoute` immutable fields omitted from enforcement list [Medium]
**Section:** 4.8
Resolved runtime and credential pool are prohibited from modification but not snapshotted.
**Recommendation:** Add `PostRoute` to immutable field enforcement with `resolved_runtime_name` and `credential_pool_id`.

### POL-045. POL-041 regression — cross-phase priority ordering statement still misleading [Medium]
**Section:** 4.8
Statement implies a unified chain across phases, which is architecturally incorrect.
**Recommendation:** Rewrite to distinguish within-phase ordering from across-phase sequencing.

### POL-046. Storage quota counter missing from Redis failure behavior table [Medium]
**Section:** 11.2, 12.4
*(Duplicate of STR-043)*

### POL-047. `defaultDelegationFraction = 1.0` allows child to consume entire parent budget [Medium]
**Section:** 8.3
Upper bound of 1.0 lets a single child hollow out the tree in one call.
**Recommendation:** Add guidance warning against values above 0.5; consider reducing max to 0.9.

### POL-048. `contentPolicy.interceptorRef` `failPolicy` change silently weakens active leases [Medium]
**Section:** 8.3
No audit event, warning, or alert when `failPolicy` is weakened.
**Recommendation:** Emit audit events on failPolicy changes; query affected active policies.

### POL-049. `DelegationPolicy` deletion while leases are active — behavior undefined [Medium]
**Section:** 8.3
No error code, rejection, or fallback when a referenced policy is deleted.
**Recommendation:** Define fail-closed behavior (`POLICY_REFERENCE_INVALID`) or prevent deletion with `POLICY_IN_USE`.

### POL-050. Timeout table missing `credentials_acknowledged` timeout and LLM proxy half-open interval [Low]
**Section:** 11.3, 4.7, 4.9
Two operation-level timeouts with specific defaults are absent from the comprehensive table.
**Recommendation:** Add both entries.

### POL-051. `snapshotPolicyAtLease` snapshots pool IDs only — policy rule changes remain live [Low]
**Section:** 8.3
Creates a false expectation of full stability; `contentPolicy` changes still affect snapshotted leases.
**Recommendation:** Clarify what is and isn't snapshotted.

### POL-052. `PostAgentOutput` empty MODIFY effectively suppresses delivery without REJECT [Low]
**Section:** 4.8
Returning empty `OutputPart[]` array delivers nothing — functionally equivalent to suppression.
**Recommendation:** Validate against empty array, or document as permitted.

---

## 25. Execution Modes (EXM)

### EXM-040. `terminate(task_complete)` lifecycle protocol contradiction [High]
**Section:** 4.7, 5.2, 15.4.1
`terminate` means "exit" but task mode requires the runtime to stay alive. No acknowledgment message defined.
**Recommendation:** Introduce `task_complete` (Runtime→Adapter) and `task_ready` (Adapter→Runtime) message pair.

### EXM-041. `microvmScrubMode` and `acknowledgeMicrovmResidualState` absent from `taskPolicy` YAML [Medium]
**Section:** 5.2
Described in prose but absent from all schema examples.
**Recommendation:** Add as commented fields in the primary `taskPolicy` YAML block.

### EXM-042. Concurrent-workspace pod state machine absent from Section 6.2 [Medium]
**Section:** 6.2, 5.2
Fundamentally different lifecycle (partial slot occupancy) with no defined state transitions.
**Recommendation:** Add concurrent-workspace pod state machine to Section 6.2.

### EXM-043. Concurrent-workspace mode has no tenant isolation statement [Medium]
**Section:** 5.2, 13.1
Task mode has two-layer tenant pinning; concurrent mode has nothing.
**Recommendation:** Add "Tenant model for concurrent-workspace mode" paragraph.

### EXM-044. `maxConcurrent` defined at two schema levels for workspace mode [Low]
**Section:** 5.2
Top-level and inside `concurrentWorkspacePolicy` — no canonical source designated.
**Recommendation:** Designate a single canonical location.

### EXM-045. `preConnect` (SDK-warm) compatibility with task and concurrent modes unspecified [Medium]
**Section:** 5.1, 5.2, 6.1
SDK-warm assumptions are incompatible with between-task lifecycle; no compatibility matrix.
**Recommendation:** Add explicit compatibility table for `preConnect` vs `executionMode`.

### EXM-046. `concurrencyStyle: stateless` warm pool integration model unspecified [Low]
**Section:** 5.2, 4.6.1, 4.6.2
Unclear whether stateless pods use the `SandboxClaim` model or bypass it entirely.
**Recommendation:** Clarify pod lifecycle model for stateless mode.

---

## Cross-Cutting Themes

1. **Phantom references and missing canonical entries**: Metrics, alerts, API endpoints, and CLI commands referenced in narrative text but absent from their canonical tables (OBS-038/039, API-057/058/059, OPS-049, DOC-042/043, FLR-043). This is the most pervasive class of finding — 14 prior iterations have not fully eradicated it because fixes to one section create new references that aren't propagated to canonical tables.

2. **Undefined failure modes for secondary infrastructure**: KMS signing (FLR-042), storage quota during Redis outage (STR-043/FLR-040), GC leader loss (STR-049/FLR-046), Postgres failover during checkpoint metadata write (FLR-044), and dual-store timer reset (FLR-047) all represent failure scenarios where secondary infrastructure interactions have no defined behavior.

3. **Execution mode lifecycle gaps**: Task mode's `terminate(task_complete)` contradiction (EXM-040), concurrent-workspace's missing state machine (EXM-042) and tenant isolation (EXM-043), and SDK-warm compatibility (EXM-045) form a cluster of underspecified non-session execution mode behaviors.

4. **Cross-reference errors**: DOC-037/038/039/040/041 identify five incorrect section cross-references — a persistent document quality issue.

5. **Storage accounting gaps**: Eviction context objects (STR-044/050), partial checkpoint manifests (STR-045), and GC double-decrement risk (STR-049) create potential storage leaks or quota inaccuracies.

6. **Build sequence phasing**: LLM Proxy has no phase (BLD-036), client SDKs have no phase (BLD-043), audit logging arrives too late (BLD-039), and `deny-all` default blocks users for 10 phases (BLD-041).

7. **Delegation tree durability**: Extension-denied flag (DEL-041), `maxTreeMemoryBytes` counter (DEL-043), and cross-environment semantics (DEL-046/047) have gaps in crash-recovery and mid-tree policy change handling.
