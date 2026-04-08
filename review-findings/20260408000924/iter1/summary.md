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
| 6 | EXP-033B | Multi-variant hash bucketing formula undefined | Fixed |
| 7 | EXP-033C | Gateway creating materialized view at runtime contradicts DDL-through-migrations pattern | Fixed |
| 8 | POL-041 | Cross-phase priority ordering error (re-reported as POL-045) | **Skipped** — carried forward, previously skipped |
| 9 | MSG-037 | `delivery_receipt` schema omits `error` from populated-status list | **Skipped** — carried forward, previously skipped |
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
| 1 | SCL-035 | Scalability | All performance targets are first-principles estimates lacking benchmark validation gates before GA | 6.3, 4.1, 16.5, 18 | **Fixed** |
| 2 | OPS-045 | Operator Experience | `kubeApiServerCIDR` has no default and causes fail-closed webhook outage if wrong | 13.2, 17.6 | **Fixed** |
| 3 | BLD-036 | Build Sequence | LLM Proxy subsystem has no phase assignment | 18, 4.1, 4.9 | **Fixed** |
| 4 | EXM-040 | Execution Modes | `terminate(task_complete)` lifecycle protocol contradiction — exit vs stay-alive | 4.7, 5.2, 15.4.1 | **Fixed** |

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

### K8S-040. PoolScalingController default formula includes `variant_weight` for non-experiment pools [Medium] ✓ Fixed
**Section:** 4.6.2, 17.8.2
The default formula includes `variant_weight` but this is only meaningful for A/B experiment pools. Non-experiment pools have no defined default for this variable.
**Recommendation:** Remove `variant_weight` from the default formula; document it separately for variant pools only.
**Resolution:** Removed `variant_weight` from the default formula in Section 4.6.2 (renamed to "Default formula (non-experiment pools)"). Added a separate "Variant pool formula (experiment pools only)" block with `variant_weight` and an explanation that it applies only in experiment context. Applied the same split to the mode-adjusted formula in Section 5.2, which now references Section 4.6.2 for the variant case.

### K8S-041. PoolScalingController writes `SandboxWarmPool.status` in violation of SSA field ownership table [Medium] ✓ Fixed
**Section:** 6.1, 4.6.3
Section 6.1 says PoolScalingController sets `status.sdkWarmDisabled: true`, but Section 4.6.3's SSA table assigns `SandboxWarmPool.status.*` exclusively to WarmPoolController.
**Recommendation:** Move circuit-breaker state to `SandboxWarmPool.spec` (PoolScalingController's domain) or reassign status ownership with RBAC grants.
**Resolution:** Moved the circuit-breaker flag from `status.sdkWarmDisabled` to `spec.sdkWarmDisabled` (PoolScalingController's domain). Section 6.1 now says PSC sets `spec.sdkWarmDisabled: true` and WarmPoolController reads `spec.sdkWarmDisabled`. Section 4.6.3's SSA ownership table updated to include `spec.sdkWarmDisabled` in PoolScalingController's owned fields with an explanatory note. No RBAC changes required — PSC already holds `create`/`update`/`delete` on `SandboxWarmPool` resources, which covers spec writes. K8S-044 is also resolved as a consequence (the RBAC gap no longer exists because no status write is performed).

### K8S-042. `lenny-pool-config` ValidatingAdmissionWebhook referenced but never defined [Medium]
**Section:** 13.2, 4.6.1
Two references invoke this webhook but no definition exists — no manifest, rules, failurePolicy, or Helm template.
**Recommendation:** Either fully define the webhook or remove the two references and rely on other enforcement layers.
**Status: Fixed** — Replaced both references to the undefined `lenny-pool-config` webhook with the already-defined `lenny-direct-mode-isolation` webhook (Section 6.2). In the network policy table (Section 13.2, line 5577), `lenny-pool-config` was replaced with `lenny-direct-mode-isolation` and `lenny-sandboxclaim-guard` (both fully defined in the spec). In the `provider-direct` + `deliveryMode: proxy` enforcement note (Section 13.2, line 5596), the undefined webhook name was replaced with `lenny-direct-mode-isolation` with an accurate description of its scope.

### K8S-043. `topologySpreadConstraints` for agent pods attributed to wrong controller [Medium] ✓ Fixed
**Section:** 5.2, 4.6.3
Section 5.2 attributes topology constraints to PoolScalingController, but pod spec is owned by WarmPoolController per the SSA table.
**Recommendation:** Clarify the two-step propagation: PSC writes to `SandboxTemplate.spec`, WPC copies to `Sandbox.spec`.
**Resolution:** Section 5.2 updated to explicitly describe the two-step propagation: PSC writes defaults into `SandboxTemplate.spec.topologySpreadConstraints`; WPC copies them into `Sandbox.spec` when creating/updating agent pods. Cross-reference to Section 4.6.3 SSA table added.

### K8S-044. PoolScalingController RBAC grants insufficient for `SandboxWarmPool.status` writes [Medium] ✓ Already Fixed
**Section:** 4.6.3, 6.1
RBAC grants are read-only on status subresources, but Section 6.1 requires a status write. Dependent on K8S-041 resolution.
**Recommendation:** If circuit-breaker state stays in `status`, add `patch` grant on `SandboxWarmPool/status`.
**Resolution:** K8S-041's fix moved the circuit-breaker flag from `status.sdkWarmDisabled` to `spec.sdkWarmDisabled`, which is already within PoolScalingController's SSA ownership domain (Section 4.6.3 table). PSC no longer writes to `SandboxWarmPool/status` at all, so no additional `patch` grant is needed. No spec changes required.

---

## 2. Security & Threat Modeling (SEC)

### SEC-038. `ReportUsage` trust model allows malicious runtime to underreport token consumption [Medium] — ✅ Fixed
**Section:** 4.7, 11.2, 4.9
In direct delivery mode, the gateway has no independent token count — a malicious pod can underreport.
**Recommendation:** In proxy mode, extract token counts from proxied responses as authoritative. In direct mode, document as residual risk with anomaly detection metric.
**Resolution:** Section 4.9 proxy mode step 4 updated to state that the gateway extracts `input_tokens`/`output_tokens` from upstream provider responses as the authoritative record, and that `ReportUsage` is ignored for proxy-mode sessions. Section 11.2 "Quota Update Timing" split into proxy-mode and direct-mode bullet points: proxy mode is documented as gateway-authoritative; direct mode documented as accepted residual risk (restricted to single-tenant/dev deployments) with `lenny_gateway_token_usage_anomaly_total` anomaly detection metric.

### SEC-039. `uploadToken` has no documented TTL, scope binding, or cryptographic protection [Medium] — ✅ Fixed
**Section:** 7.1, 7.4, 15.1
Format, TTL, session binding, and replay protection are all unspecified.
**Recommendation:** Specify as a short-lived signed token (HMAC-SHA256) with explicit TTL, invalidated after `FinalizeWorkspace`.
**Status:** Fixed — Specified uploadToken as HMAC-SHA256 signed token structured as `<session_id>.<expiry_unix_seconds>.<hmac_hex>` with single-use invalidation upon successful `FinalizeWorkspace`.

### SEC-040. `respond_to_elicitation` does not specify session-scoped authorization check [Medium] — ✅ Fixed
**Section:** 9.2
No validation that `elicitation_id` belongs to the calling session — a foreign client could inject responses.
**Recommendation:** Validate `(session_id, user_id, elicitation_id)` triple; return 404 for foreign IDs.
**Status:** Fixed — Added `(session_id, user_id, elicitation_id)` triple validation for `respond_to_elicitation`.

### SEC-041. `allowSymlinks: true` archive symlinks re-resolved at promotion time against new root [Medium] — ✅ Fixed
**Section:** 7.4
Symlinks validated against `/workspace/staging` may escape `/workspace/current` after promotion.
**Recommendation:** Re-validate all symlinks after staging→current promotion.
**Status:** Fixed — Added explicit re-validation of every symlink in the promoted tree after staging→current promotion.

### SEC-042. OAuth connector flow lacks PKCE and `state` parameter anti-CSRF protection [Medium] — ✅ Fixed
**Section:** 9.3, 9.4
No `state` parameter or PKCE in the OAuth flow, enabling CSRF and token injection attacks.
**Recommendation:** Generate per-request `state` bound to session; require PKCE (S256) for public clients.
**Status:** Fixed — Added cryptographic random `state` parameter (anti-CSRF) and PKCE (S256) for public clients to the OAuth connector flow.

### SEC-043. gVisor `SO_PEERCRED` semantics remain unvalidated — nonce-only fallback weakens adapter-agent boundary [Medium] — ✅ Fixed
**Section:** 4.7, 13.1
If gVisor diverges, the nonce-only mode is activated indefinitely with no escalation path.
**Recommendation:** Add a Phase 3.5 hard gate; supplement nonce with per-connection challenge-response if SO_PEERCRED fails.
**Status:** Fixed — Added Phase 3.5 `SO_PEERCRED` integration test gate and challenge-response fallback mechanism.

### SEC-044. Pre-upload storage quota check trusts client-supplied `Content-Length` [Medium] — ✅ Fixed
**Section:** 11.2, 7.4
A client can declare a small Content-Length but stream more data, bypassing the pre-check.
**Recommendation:** Enforce `io.LimitedReader` hard cap on inbound body based on remaining quota.
**Status:** Fixed — Gateway now wraps every upload request body in `io.LimitedReader` bounded by `remaining_quota_bytes`.

### SEC-045. Task-mode scrub does not address `shmget`-allocated POSIX shared memory segments [Medium] — ✅ Fixed
**Section:** 5.2, 13.1
POSIX shared memory segments persist across task boundaries — documented residual risk with no mitigation.
**Recommendation:** Add `ipcrm -m` step to scrub procedure; verify gVisor IPC namespace scoping for gVisor pods.
**Status:** Fixed — Added `ipcrm --all=shm` step to purge `shmget`-allocated IPC shared memory segments in the scrub procedure.

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

### NET-037. `lenny-drain-readiness` webhook NetworkPolicy blocks its required gateway callback [Medium] — ✅ Fixed
**Section:** 13.2, 12.5
No egress rule for the webhook to reach the gateway's `/internal/drain-readiness`. Under default-deny, all pod evictions are permanently blocked.
**Recommendation:** Add egress from admission-webhook pods to gateway internal port; add corresponding gateway ingress rule.
**Status:** Fixed — Added egress rule from admission-webhook pods to the gateway's `/internal/drain-readiness` endpoint and corresponding gateway ingress rule.

### NET-038. Gateway ingress from Ingress controller namespace has no specified selector [Medium] — ✅ Fixed
**Section:** 13.2
No Helm value or YAML for the ingress namespace selector. If wrong, gateway is unreachable from the internet.
**Recommendation:** Add `{{ .Values.ingressControllerNamespace }}` Helm value; include gateway ingress NetworkPolicy YAML.
**Status:** Fixed — Added `{{ .Values.ingressControllerNamespace }}` Helm value (default: `ingress-nginx`) with `kubernetes.io/metadata.name` namespace selector in NetworkPolicy.

### NET-039. Gateway `lenny-system` NetworkPolicy has no egress rule for in-cluster external interceptor gRPC calls [Medium]
**Section:** 13.2, 4.8
External interceptors are gRPC services, but the gateway's egress explicitly excludes cluster pod CIDRs.
**Recommendation:** Add a mechanism for deployers to declare interceptor namespaces; render corresponding gateway egress rules.
**Status:** Fixed
**Resolution:** Added `gateway.interceptorNamespaces` (list, default `[]`) and `gateway.interceptorGRPCPort` (default `50053`) Helm values. The Helm chart now iterates over `gateway.interceptorNamespaces` and renders one supplemental `allow-gateway-egress-interceptor-<namespace>` NetworkPolicy per declared namespace in `lenny-system`, permitting TCP `interceptorGRPCPort` egress from gateway pods to that namespace using an immutable `kubernetes.io/metadata.name` namespace selector. The gateway component row in the Section 13.2 table was updated to document this egress path. A detailed NET-039 callout note was added after the existing NET-038 note, including example YAML, operational guidance (empty list is safe; external interceptors reachable via external IP do not need a namespace entry), and a `lenny-preflight` validation check. Section 4.8's interceptor registration table was updated to add a documented `endpoint` field that cross-references the `gateway.interceptorNamespaces` requirement for in-cluster deployments.

### NET-040. SPIFFE trust domain `lenny` is hardcoded — collides across co-located deployments [Medium]
**Section:** 10.3
Two Lenny deployments sharing a cluster and CA have overlapping trust domains, enabling cross-deployment pod impersonation.
**Recommendation:** Add Helm value `global.spiffeTrustDomain`; add preflight warning for default value in shared clusters.
**Status:** Fixed
**Resolution:** Section 10.3 updated to make the SPIFFE trust domain configurable via Helm value `global.spiffeTrustDomain` (default: `lenny`). The "Pod identity" paragraph now documents the full SPIFFE URI format with `<trust-domain>` placeholder, explains the cross-deployment impersonation risk when the trust domain is shared, requires deployers to override to a deployment-specific value (e.g., `lenny-<cluster-name>-<namespace>`) in multi-instance environments, and adds a `lenny-preflight` Job warning when the default value is detected alongside more than one Lenny Deployment in the cluster — consistent with the existing `global.saTokenAudience` pattern. The certificate lifecycle table and the proxy-mode SPIFFE-binding example (Section 4.9) were also updated to reference `<trust-domain>` instead of the hardcoded `lenny`.

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
**Status: Fixed.** The Section 16.5 SLO table header was renamed to "SLO targets — PROVISIONAL (first-principles estimates; must be validated before GA)" and a blockquote callout was added above the table making explicit that: (1) all values are first-principles estimates, not load-test measurements; (2) they MUST NOT be used in customer-facing SLA commitments until Phase 14.5 is complete; (3) any deployment with default values and `slo.validated` unset MUST emit a startup warning. The "Target" column was renamed "Target (provisional)". A "Startup warning requirement" paragraph was added after the table specifying the `slo.validated` Helm flag mechanism and tying it to the Phase 14.5 benchmark gate. Sections 4.1 and 6.3 already had adequate provisional language and were not modified.

### SCL-036. `minReplicas` burst-absorption formula promised by Section 10.1 is missing from Section 17.8 [Medium]
**Section:** 10.1, 17.8.2
Section 10.1 promises a formula in 17.8 for non-KEDA Tier 3. It doesn't exist.
**Recommendation:** Add the formula with worked examples for both KEDA and Prometheus Adapter paths.
**Status: Fixed.** Added a "`minReplicas` burst-absorption formula (SCL-036)" subsection immediately after the gateway tier table in §17.8.2. The subsection defines the general formula `minReplicas >= ceil(burst_arrival_rate * pipeline_lag_seconds / sessions_per_replica)` and provides two complete worked-example tables: Path A (KEDA, 20s lag) covering all three tiers, and Path B (Prometheus Adapter / non-KEDA, 60s lag) covering all three tiers. Notes explain why the Tier 3 KEDA path is viable with `minReplicas: 5` when the aggressive scale-up policy is in place (and how to eliminate scale-up reliance by setting `minReplicas: 10`), and why the non-KEDA path requires `minReplicas: 30` at Tier 3 (i.e., equals `maxReplicas`, confirming the §10.1 mandatory-KEDA requirement). Section 10.1 text was not changed — it already correctly cross-references §17.8.

### SCL-037. etcd write rate at Tier 3 extrapolates to ~800+ writes/s but no write ceiling or alert is defined [Medium]
**Section:** 4.6.1, 17.8.2, 16.5
No etcd write ceiling, write-latency alert, or work queue backlog metric for Tier 3.
**Recommendation:** Add Tier 3 etcd write rate estimate; add `EtcdWriteLatencyHigh` alert; add `ControllerWorkQueueDepth` metric.
**Status: Fixed.** Three changes made: (1) §4.6.1 "etcd write pressure at scale" paragraph replaced with a three-row table showing per-tier estimated write rates (Tier 1: ~8/s, Tier 2: ~80/s, Tier 3: ~800 raw / ~120 QPS after dedup), with derivation assumptions and an explicit statement that the `EtcdWriteLatencyHigh` alert fires when p99 WAL fsync latency exceeds 25ms. The `ControllerWorkQueueDepth` metric (labeled by controller/queue) is defined there, along with the `ControllerWorkQueueDepthHigh` alert threshold (50% of configured max depth for > 2 min). (2) §16.5 Warning alerts table gained `EtcdWriteLatencyHigh` (p99 `etcd_disk_wal_fsync_duration_seconds` > 25ms for > 2 min; cross-references §4.6.1 write rate estimates and §17.8.2 for mitigation options). (3) §16.5 Warning alerts table gained `ControllerWorkQueueDepthHigh` (queue depth > 50% of configured max for > 2 min; notes that sustained backlog precedes etcd write bursts and cross-references §17.8.2 tuning knobs).

### SCL-038. Tier 3 "linear horizontal scaling only" claim has undocumented prerequisite: LLM Proxy extraction [Medium]
**Section:** 2, 4.1, 16.5
Without extraction, `maxSessionsPerReplica` is 200, requiring 50 replicas (above HPA max 30).
**Recommendation:** Qualify the claim with the LLM Proxy extraction prerequisite.
**Status: Fixed.** Sections 4.1 and 16.5 already documented the LLM Proxy extraction prerequisite and the revert-to-200 fallback in full detail. The only gap was Section 2's Goals list, which stated "horizontal scaling only" without qualification. The goal bullet was updated to add a parenthetical noting the extraction prerequisite and pointing to §4.1 for the extraction thresholds and the `maxSessionsPerReplica` fallback. No changes were needed to §4.1 or §16.5.

### SCL-039. Session creation rate SLO (200/s at Tier 3) has no end-to-end latency budget [Medium]
**Section:** 16.5, 7.1, 4.1
Creation rate is informational only — no SLO, burn-rate alert, or throughput constraint backs it.
**Recommendation:** Add session creation P99 latency SLO with burn-rate alert.
**Status: Fixed.** Added "Session creation latency: P99 < 500ms" row to the SLO targets table in §16.5. The measurement definition covers steps 1–8 of §7.1 (auth, policy, credential pre-check, pod claim, Postgres persist, credential assignment, pod assignment) through `session_id` response, excluding workspace upload. Added a corresponding `SessionCreationLatencyBurnRate` row (1 h at 14×, 6 h at 3×, Critical fast / Warning slow) to the burn-rate alerts table in §16.5. The 500ms P99 target is consistent with the architecture: all creation steps use pre-warmed pods and in-memory/Postgres paths with no cold-start cost; the target is flagged provisional and subject to Phase 14.5 validation alongside all other SLO targets.

### SCL-040. Postgres write ceiling relies on instance-class estimates not validated against actual workload [Medium]
**Section:** 12.3, 17.8.2
Only ~23% margin at Tier 3; RLS trigger overhead and quota flush patterns not captured.
**Recommendation:** Add Lenny-specific write-pattern benchmark to Phase 2/13.5; add burst IOPS alert.
**Status: Fixed.** Three targeted changes made: (1) Added a `PostgresWriteBurstIops` alert (Section 16.5) that fires when instantaneous write IOPS exceed the burst ceiling for the configured instance class; repeated triggers (> 3 in 10 minutes) signal quota flush storms or session-creation spikes saturating burst headroom — providing early warning before sustained saturation. (2) Added a workload-specific callout note immediately after the Section 12.3 write ceiling table explicitly naming the two unmodeled overheads: RLS trigger (`lenny_tenant_guard`) per-write overhead (WAL amplification under high concurrency) and quota flush burst patterns (Redis→Postgres sync producing IOPS spikes at each flush boundary well above the ~100/s steady-state estimate), plus `pg_stat_user_functions` monitoring guidance. (3) Extended Phase 13.5 (Section 18 roadmap) with an explicit scenario 8 — "Lenny-specific Postgres write-pattern benchmark" — requiring measurement of RLS trigger execution overhead under peak session throughput, quota flush instantaneous IOPS spike under 10,000 concurrent sessions, and comparison against the Section 12.3 ceiling table with a Helm `postgres.writeCeilingIops` update gate (>10% deviation). The `PostgresWriteBurstIops` alert threshold calibration against this measured burst pattern is named as a Phase 13.5 deliverable. The existing Phase 2 generic validation path (Tier 1/2) was already sufficient; only the Tier 3 Lenny-specific benchmark and the burst alert were missing.

### SCL-041. Delegation fan-out with `orchestrator` preset creates unquantified warm pool demand [Medium]
**Section:** 8.3, 16.5, 17.8
10,000 sessions × 10 children = 110,000 pods, far exceeding `minWarm: 1050`.
**Recommendation:** Add delegation fan-out sizing formula to Section 17.8.
**Status: Fixed.** The 110,000-pod figure is unrealistic: the capacity tier table (§16.5) already caps system-wide concurrent delegations at 500 for Tier 3, not `sessions × maxParallelChildren`. Added a **"Delegation fan-out sizing (SCL-041)"** block to §17.8.2 immediately after the warm pool formula paragraph. The block: (a) explains that demand is bounded by the tier concurrent-delegation cap (500 at Tier 3), not by the naive product; (b) provides a formula `delegation_claim_rate = concurrent_delegations / avg_child_session_seconds` and a delegation-adjusted `minWarm` formula; (c) includes a Tier 3 worked example showing the adjusted target is ~1,600 (vs. the baseline 1,050 which covers session-creation only); (d) notes that when `orchestrator`-preset sessions are rare (< 10% of load) the baseline 1,050 is sufficient; and (e) shows how distributing across 10 hot pools reduces per-pool demand to ~160. The existing §8.3 line "aggregate child pod demand can reach hundreds of thousands — deployers should size warm pools accordingly" is retained as a conservative upper-bound advisory.

### SCL-042. Quota drift bound of ~30,000 requests at Tier 3 has no per-tenant impact analysis [Medium]
**Section:** 11.2, 12.4, 17.8
30,000-request overshoot represents 300× a 100-req/min limit during a 60s outage.
**Recommendation:** Document effective drift after applying `per_replica_hard_cap` and `quotaFailOpenCumulativeMaxSeconds`.

**Status: Fixed.** The 300× concern conflates two distinct failure scenarios. The §17.8 operational-defaults table was restructured to separate them: (1) **Normal-operation drift** (row ¹) — the ~30,000 req figure represents Postgres-sync-window overshoot during a gateway crash (`quotaSyncIntervalSeconds × peak_rate`), bounded by the MAX-rule recovery and limited to one sync interval; (2) **Redis fail-open drift** (row ²) — bounded independently by `per_replica_hard_cap` (default `tenant_limit / 2`) and `quotaFailOpenCumulativeMaxSeconds` (default 300s). A worked example shows that at 100 req/min with default caps the actual per-replica fail-open exposure is ~250 requests (~2.5×), not 300×. A note directs deployers with tight financial exposure to tune `quotaFailOpenCumulativeMaxSeconds` and `per_replica_hard_cap` for their risk tolerance.

### SCL-043. Redis Lua script serialization under high delegation fan-out has no cross-tenant contention model [Medium]
**Section:** 8.3, 12.4
500 concurrent delegations × 100μs = 50ms aggregate blocking — exceeds `LeaseStore` 5ms SLO.
**Recommendation:** Add aggregate Lua contention analysis; update Redis instance separation trigger.
**Status:** Fixed
**Resolution:** Added "Cross-tenant aggregate Lua contention" analysis block in Section 8.3 after the per-session ceiling table. The new block: (1) explains that cross-tenant Lua serialization is distinct from per-session fan-out — all `budget_reserve.lua` invocations across all tenants serialize on the same Redis primary; (2) provides the aggregate blocking formula `T_block = N_concurrent_scripts × T_script_duration` with a concrete derivation showing the 5 ms `LeaseStore` SLO is breached at N > 50 concurrent scripts; (3) identifies the cross-tenant instance separation trigger: Delegation Lua P99 > 2 ms sustained OR cluster-wide `delegate_task` rate > 50/s; (4) describes the remediation (dedicate `DelegationBudgetStore` to a separate Redis connection string or Cluster shard, configuration-only change). Updated Section 12.4 in two places: the "Delegation budget decrements (Lua)" row in the Tier 3 write throughput table now notes the 50-concurrent-script threshold, and a new "Delegation Lua aggregate blocking" bullet was added to the "When Sentinel becomes insufficient" trigger list with a cross-reference to the Section 8.3 formula and remediation steps.

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

### PRT-037. MCP nonce injected via non-standard `initialize` field path [Medium] ✓ Fixed
**Section:** 15.4.2, 4.7
`_lennyNonce` uses undefined MCP extension locations with no migration path.
**Recommendation:** Define a canonical injection location with migration path to out-of-band channel.
**Resolution:** Canonicalized the nonce injection location to `params._lennyNonce` (top-level in the MCP `initialize` `params` object), replacing the previously ambiguous `params.clientInfo.extensions._lennyNonce` location. Added explicit note that this is a Lenny-private intra-pod convention, not a general MCP extension. Deprecated the `clientInfo.extensions` path with a removal notice at adapter manifest `version: 2`. Added a concrete migration path: `version: 2` will move to a pre-`initialize` out-of-band handshake line (`{"type":"lenny_nonce","nonce":"..."}`) sent before the MCP exchange, with a two-release backward-compat window. Section 4.7 item 7 updated to reference the canonical location in Section 15.4.3.

### PRT-038. A2A agent card auto-generation produces stale snapshot with no invalidation path [Medium]
**Section:** 5.1, 21.1
Stored card format is frozen at registration time with no bulk-regeneration mechanism.
**Recommendation:** Add `generatedAt`/`generatorVersion` fields; add admin endpoint for bulk regeneration.
**Resolution:** Fixed in Section 5.1 (`publishedMetadata` field description). Auto-generated A2A cards now include `generatedAt` (RFC 3339 timestamp) and `generatorVersion` (Lenny semver) envelope fields injected by the gateway generator, allowing operators to detect staleness after a Lenny upgrade. Added a `POST /v1/admin/runtimes/regenerate-cards` bulk-regeneration endpoint with an optional `generatorVersionBefore` filter and `dryRun` mode; it iterates all runtimes with `agentInterface`, regenerates their `agent-card` publishedMetadata entry, and skips hand-crafted entries. Hand-crafted cards (no `agentInterface` field) are explicitly excluded from both the convention and the endpoint. Section 21.1 required no change — the post-v1 A2A text refers to auto-generation without describing the storage format, and remains accurate.

### PRT-039. `AdapterCapabilities.SupportsElicitation` not propagated to discovery output [Medium]
**Section:** 15, 9.2, 21.1
Discovery consumers have no way to know that elicitation-dependent workflows will fail.
**Recommendation:** Extend `HandleDiscovery` contract so each adapter can inject capability annotations.
**Status:** Fixed
**Resolution:** Extended `HandleDiscovery` signature to accept `caps AdapterCapabilities` alongside the runtime list (`HandleDiscovery(ctx, w, r, runtimes []AuthorizedRuntime, caps AdapterCapabilities) error`). The contract description in Section 15 now requires every adapter to embed its `AdapterCapabilities` as an `adapterCapabilities` annotation in its discovery output (top-level object in REST and `list_runtimes` responses), with `supportsElicitation` explicitly required. The Runtime Discovery subsection (Section 9) updated to state that all discovery responses include `adapterCapabilities` and that consumers must check `supportsElicitation` before starting elicitation-dependent workflows. `GET /v1/runtimes` endpoint table updated to list `adapterCapabilities` as a returned field.

### PRT-040. A2A `/.well-known/agent.json` discovery URL not addressed [Medium]
**Section:** 5.1, 15, 21.1
External A2A callers performing standards-based discovery cannot find Lenny runtimes.
**Recommendation:** Add `GET /.well-known/agent.json` endpoint aggregating public A2A cards.
**Status:** Fixed
**Resolution:** Added `GET /.well-known/agent.json` as a Post-V1 (A2A) endpoint in two places: (1) Section 21.1 — new sub-paragraph "A2A standards-based discovery" specifying the endpoint behavior: aggregates all runtimes with a public `agent-card` `publishedMetadata` entry, returns a JSON array of verbatim stored cards (gateway pass-through), requires no auth, capped at configurable `wellKnownAgentJsonMaxCards` (default 100) to prevent unbounded responses, only active when `A2AAdapter` is registered. (2) Section 15.1 discovery table — new row for `/.well-known/agent.json` annotated Post-V1 with cross-reference to Section 21.1. No new data model changes required; existing `publishedMetadata` public visibility contract (Section 5.1) already provides the backing data.

### PRT-041. Agent Protocol section has no state mapping, content-type mapping, or fidelity matrix entry [Low]
**Section:** 21.3
AP section is a stub compared to the detailed A2A specification.
**Recommendation:** Expand minimally with state mapping, step execution model, and placeholder fidelity matrix column.

### PRT-042. Intra-pod MCP `2024-11-05` backward compatibility has no removal trigger [Medium]
**Section:** 15.2, 15.5
Gateway has a rolling two-version policy, but intra-pod servers have no analogous lifecycle.
**Recommendation:** Add note that intra-pod version support follows the same rolling policy as the gateway.
**Status:** Fixed — Extended the "Protocol version" bullet in Section 15.4 (Standard-tier intra-pod MCP) to explicitly state that intra-pod MCP version support follows the same rolling two-version policy as the gateway (Section 15.5 item 2): the oldest accepted version enters a 6-month deprecation window when a new spec version is adopted, and removal applies only to new connection negotiations (active sessions on the deprecated version are not forcibly terminated).

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
**Status:** Fixed — Removed "timestamp" and "session ID" from the section 15.4.4 narrative description. The intro sentence now reads "echoes back messages with a sequence number", matching the pseudocode which only uses `seq`.

### DXP-038. Echo runtime pseudocode has unsafe `input[0].inline` access [Low]
**Section:** 15.4.4
Direct index access fails on empty arrays or non-text parts.
**Recommendation:** Replace with safe iteration over parts array.

### DXP-039. Roadmap step 3 sends Minimum-tier authors to the adapter's state machine [Medium]
**Section:** 15.4.5, 15.4.2
Minimum-tier authors don't implement the adapter — the step is misleading.
**Recommendation:** Rewrite annotation to explain binary perspective, or move to Standard/Full tier.
**Status:** Fixed — Rewrote the step 3 annotation in section 15.4.5 to make clear that the adapter (not the binary) owns the state machine. The annotation now reads: "Read for context: the adapter (not your binary) owns this state machine. Knowing it helps you understand when your binary will start receiving messages (`ACTIVE`), and that `shutdown` arrives only during `DRAINING` — your binary never drives these transitions." This preserves the reference as useful orientation material without implying Minimum-tier authors need to implement it.

### DXP-040. Standard-tier roadmap step 7 annotation obscures relevant content in Section 4.7 [Medium]
**Section:** 15.4.5, 4.7
Annotation emphasizes the gRPC RPC table (irrelevant to binary authors) over the manifest and lifecycle schemas.
**Recommendation:** Rewrite to direct authors to the relevant subsections and away from the gRPC table.
**Status:** Fixed — Rewrote step 7 annotation in Section 15.4.5 to name the specific subsections a Standard-tier binary author needs (adapter manifest field reference, lifecycle channel message schemas) and explicitly note that the gRPC RPC table at the top of Section 4.7 is the gateway↔adapter contract and not relevant to binary authors.

### DXP-041. No sample runtime for Standard or Full tier [Medium]
**Section:** 15.4.4, 15.4.5
Only Minimum-tier pseudocode exists. Standard introduces a Lenny-specific nonce handshake; Full introduces a custom lifecycle channel.
**Recommendation:** Add annotated pseudocode samples for Standard-tier (nonce + MCP) and Full-tier (lifecycle channel).
**Status:** Fixed — Added two annotated pseudocode blocks in Section 15.4.4 immediately after the existing Minimum-tier block. The Standard-tier sample shows manifest read, `mcpNonce` extraction, Unix-socket MCP connection, and injection of `_lennyNonce` into `initialize` params before any tool call. The Full-tier sample extends Standard with lifecycle channel connect, `lifecycle_capabilities`/`lifecycle_support` capability negotiation, and a background handler covering `checkpoint_request`/`checkpoint_ready`, `interrupt_request`/`interrupt_acknowledged`, `credentials_rotated`/`credentials_acknowledged`, `deadline_approaching`, and `terminate`.

### DXP-042. Integration tier is determined behaviorally but is not documented as such [Low]
**Section:** 15.4.3, 5.1
No `integrationTier` field exists; tier is inferred from runtime behavior but this isn't stated.
**Recommendation:** Add "Tier determination" note explaining behavioral inference.

### DXP-043. Adapter-local tool catalog for Minimum-tier runtimes is absent [Medium] — **Fixed**
**Section:** 15.4.1, 15.4.3
`tool_call` for adapter-local tools is mentioned but no tool names, schemas, or discovery mechanism exist.
**Recommendation:** Add an "Adapter-Local Tool Reference" table, or remove the mention if tools don't exist.
**Resolution:** Added an "Adapter-Local Tool Reference" block immediately after the `tool_call` schema in Section 15.4.1. The block defines four built-in tools (`read_file`, `write_file`, `list_dir`, `delete_file`) with name, description, and full JSON Schema `inputSchema` for each. Added a discovery mechanism: agents read `adapterLocalTools` from the adapter manifest (`/run/lenny/adapter-manifest.json`) to enumerate available tools at runtime. Added workspace-confinement rule and error behavior for out-of-bounds paths. Custom adapters may extend the list by declaring additional entries before spawning the runtime.

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
**Status:** Fixed
**Resolution:** Added a "stdout flushing requirement" paragraph to Section 15.4.1 immediately after the stderr note. The paragraph states the MUST-flush rule and includes a language-specific guidance table covering Python, Node.js, Ruby, Java, Go, Rust, and C/C++. Updated the Section 15.4.4 Minimum-tier echo runtime pseudocode to show explicit `flush(stdout)` calls after each `write_line`, cross-referenced to Section 15.4.1.

---

## 7. Operator & Deployer Experience (OPS)

### OPS-044. Missing end-to-end "Day 0" installation walkthrough [Medium]
**Section:** 17.6, 18
No single sequential procedure from "empty cluster" to "first echo session."
**Recommendation:** Add a Day 0 walkthrough with minimal annotated `values.yaml`.
**Status:** Fixed. Added a "Day 0 installation walkthrough" block at the end of Section 17.6, immediately before Section 17.7. The block provides a 7-step sequential procedure (apply CRDs → create values.yaml → preflight → helm install → retrieve admin token → verify warm pool → first echo session) with a fully annotated minimal `values.yaml` covering postgres, redis, minio, cert-manager, kubeApiServerCIDR, gateway replicas, pools, and bootstrap seed. Covers the exact path from empty cluster to first live echo session.

### OPS-045. `kubeApiServerCIDR` has no default and causes fail-closed webhook outage if wrong [High]
**Section:** 13.2, 17.6
Misconfiguration blocks all admission webhooks, halting warm pool replenishment.
**Recommendation:** Add cloud-specific discovery guidance; consider `0.0.0.0/0` as safe default for webhooks.
**Status:** Fixed. Split the single `kubeApiServerCIDR` value into two distinct Helm values to address the root cause: (1) `kubeApiServerCIDR` (required, no default) continues to govern gateway egress to the kube-apiserver Service ClusterIP — where a narrow CIDR is meaningful and preflight-validated; (2) `webhookIngressCIDR` (new, default `0.0.0.0/0`) governs webhook pod ingress from the kube-apiserver — where the source IP is a node or cloud-control-plane IP that varies by environment and cannot be safely defaulted to a narrow CIDR. The `0.0.0.0/0` default is safe in practice because `lenny-system` enforces default-deny and webhook pods use mTLS. A new callout note (NET-040) was added after the component table in Section 13.2 explaining the distinction and providing cloud-specific discovery commands (kubectl, EKS/aws-cli, GKE/gcloud, AKS/az) for both values. The preflight check was updated to validate `kubeApiServerCIDR` only (with an improved failure message that includes the discovery command) and to explicitly document that `webhookIngressCIDR` requires no validation. The Day 0 `values.yaml` example in Section 17.6 was updated with explanatory comments for both values and a commented-out `webhookIngressCIDR` line showing the default.

### OPS-046. No runbook for Token Service outage despite a Critical alert [Medium]
**Section:** 16.5, 17.7
`TokenServiceUnavailable` fires but no runbook exists for diagnosis or remediation.
**Recommendation:** Add Token Service outage runbook stub.
**Status:** Fixed — Added `docs/runbooks/token-service-outage.md` runbook stub in §17.7 with Trigger (circuit breaker open > 30s), Diagnosis (pod health, KMS reachability, RBAC, circuit breaker state metric), and Remediation (restart, KMS fix, RBAC re-apply, circuit breaker auto-reset, impact assessment). Also added cross-reference in the §16.5 `TokenServiceUnavailable` alert table row pointing to the new runbook.

### OPS-047. PgBouncer saturation runbook is referenced but not defined [Medium]
**Section:** 16.5, 17.7
`PgBouncerPoolSaturated` alert cross-references a nonexistent runbook.
**Recommendation:** Add `pgbouncer-saturation.md` runbook stub.
**Status:** Fixed — Added `docs/runbooks/pgbouncer-saturation.md` runbook stub in §17.7 with Trigger (`PgBouncerPoolSaturated` fires when `cl_waiting_time` > 1s for > 60s), Diagnosis (PgBouncer admin socket `SHOW POOLS;`, compare `cl_active`+`cl_waiting` against `default_pool_size`, `pgbouncer_exporter` metrics, Postgres `pg_stat_activity` bottleneck check), and Remediation (runtime pool size increase via admin socket, persistent Helm values update, horizontal PgBouncer scaling, Postgres `max_connections` hard-limit handling, post-incident review). The §16.5 `PgBouncerPoolSaturated` alert table row already contained the cross-reference `§17.7 runbook docs/runbooks/pgbouncer-saturation.md`; no change to §16.5 was needed.

### OPS-048. AdmissionWebhookUnavailable and CosignWebhookUnavailable have no remediation runbooks [Medium]
**Section:** 16.5, 17.7
Both are Critical alerts that halt warm pool replenishment; no operator guidance exists.
**Recommendation:** Add `admission-webhook-outage.md` runbook covering both webhooks.
**Status:** Fixed — Added `docs/runbooks/admission-webhook-outage.md` runbook stub in §17.7 covering both `AdmissionWebhookUnavailable` (RuntimeClass-aware policy webhook) and `CosignWebhookUnavailable` (cosign image-verification webhook). The stub includes Trigger (both alerts and the warm pool replenishment halt symptom), Diagnosis (identify failing webhook, check pod health, inspect logs, verify TLS certificate validity, confirm admission blockage via kubectl events), and Remediation (webhook pod restart, certificate rotation recovery via cert-manager, endpoint verification, emergency `failurePolicy: Ignore` bypass procedure with caution guidance, and post-recovery verification). Also added `See §17.7 runbook docs/runbooks/admission-webhook-outage.md` cross-references to both alert table rows in §16.5.

### OPS-049. `lenny-ctl` command reference is missing key Day-2 commands [Medium]
**Section:** 24
Missing: session investigation, erasure job management, migration status, tenant deletion.
**Recommendation:** Add the missing command groups or mark them as future.
**Status:** Fixed
**Resolution:** Added three new command subsections to Section 24 covering all four missing areas: (1) **24.9 Tenant Management** expanded with `tenants delete` and `tenants force-delete` commands mapping to `DELETE /v1/admin/tenants/{id}` and `POST /v1/admin/tenants/{id}/force-delete`; (2) **24.10 Session Investigation** (new) with `sessions get` and `sessions force-terminate` commands mapping to `GET /v1/admin/sessions/{id}` and `POST /v1/admin/sessions/{id}/force-terminate`; (3) **24.11 Erasure Job Management** (new) with `erasure-jobs get`, `erasure-jobs retry`, and `erasure-jobs clear-restriction` commands mapping to the corresponding Section 15.1 endpoints. Migration status is noted as not implemented in v1 with a future-release marker, directing operators to `GET /v1/admin/pools/{name}/upgrade-status` in the interim. The orphan session note in 24.3 was updated to reference the new 24.10 commands. Policy Management renumbered to 24.12.

### OPS-050. Expand-contract migration strategy requires manual phase coordination with no tooling [Medium]
**Section:** 10.5
No mechanism for tracking migration phase state or blocking premature Phase 3 deployment.
**Recommendation:** Add `lenny-ctl migrate status` command; document Phase 3 gate query performance.
**Status:** Fixed
**Resolution:** Added `### 24.12 Migration Management` section with `lenny-ctl migrate status` command backed by `GET /v1/admin/schema/migrations/status`. The command surfaces per-migration `phase` (`phase1_applied` | `phase2_deployed` | `phase3_applied` | `complete`) and `gateCheckResult` (`pass`, `fail:<N>_rows`, `not_run`), enabling operators to confirm inter-phase readiness without direct DB access. Also added Phase 3 gate query performance guidance to Section 10.5: migration files must declare the covering partial index for the gate `COUNT(*)` query; the migration runner warns when no partial index is found; tables >1M rows require operator EXPLAIN confirmation before Phase 3 DDL is applied. The existing 24.12 (Policy Management) was renumbered to 24.13. The new endpoint was added to the admin API reference table.

### OPS-051. Tier 2 → Tier 3 promotion has no operator checklist or decision guide [Medium]
**Section:** 4.1, 17.8.2
Prerequisites are scattered across dense technical sections, not structured for operator decisions.
**Recommendation:** Add "Tier Promotion Guide" subsection with ordered steps and go/no-go criteria.
**Status:** Fixed — Added new §17.8.3 "Tier Promotion Guide" immediately before §17.9. The subsection consolidates prerequisites from §4.1 (LLM Proxy extraction ratio, GC pause threshold, maxSessionsPerReplica calibration) and §17.8.2 (KEDA, etcd, Redis topology) into a 4-step operator workflow: (1) Run Phase 13.5 load tests with explicit pass/fail table, (2) Structured go/no-go decision checklist, (3) Ordered Helm value changes for Tier 3, (4) Post-promotion validation metrics and rollback trigger. No existing content was modified.

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

### TNT-038. OIDC `tenant_id` claim extraction is underspecified [Medium] ✅ Fixed
**Section:** 4.2, 10.2
No configurable claim name, no behavior for absent/unrecognized claims.
**Recommendation:** Add `auth.tenantIdClaim` Helm value; define rejection behavior.
**Resolution:** Added `auth.tenantIdClaim` Helm value (default: `tenant_id`) in Section 10.2 with a full behavior table covering: single-tenant (claim ignored), claim present + tenant registered (proceed), claim absent/empty (401 `TENANT_CLAIM_MISSING`), claim present but tenant unregistered (403 `TENANT_NOT_FOUND`). Both rejection paths log at INFO level with `user_id`/`jti` and emit `auth_failure` audit events; no silent fallback in multi-tenant mode. Section 4.2 updated with a matching `auth.tenantIdClaim` paragraph cross-referencing Section 10.2 for full extraction semantics.

### TNT-039. Billing Redis stream not deleted in tenant deletion Phase 4 [Medium]
**Section:** 12.8, 11.2.1
The `t:{tenant_id}:billing:stream` and its consumer group are not in the deletion order.
**Recommendation:** Add explicit `DEL` and `XGROUP DESTROY` step in Phase 3.
**Status:** Fixed
**Resolution:** Added explicit billing Redis stream cleanup to Phase 4's deletion order in Section 12.8 (tenant deletion lifecycle table). The new step — `XGROUP DESTROY t:{tenant_id}:billing:stream billing-flusher` followed by `DEL t:{tenant_id}:billing:stream` — is inserted immediately after "Redis caches" in the Phase 4 dependency chain, before `QuotaStore`. The consumer group name `billing-flusher` is taken from the definition in Section 11.2.1. Note: the finding's recommendation referenced Phase 3 (credential revocation), but billing stream data belongs in Phase 4 (data deletion) alongside other Redis data, which is where the fix was applied.

### TNT-040. T4 tenant KMS key teardown absent from tenant deletion lifecycle [Medium]
**Section:** 12.8, 12.5
Per-tenant KMS key remains active after tenant deletion, violating least-privilege.
**Recommendation:** Add Phase 4 sub-step for KMS key deletion with provider-standard delay.
**Status:** Fixed
**Resolution:** Added Phase 4a "Schedule KMS key deletion" to the tenant deletion lifecycle table in Section 12.8. The new phase applies only to T4 tenants (`workspaceTier: T4`) and specifies provider-standard procedures: AWS KMS `ScheduleKeyDeletion` with 7-day minimum pending window, GCP Cloud KMS `DestroyCryptoKeyVersion` (24-hour provider-enforced delay), and HashiCorp Vault `DELETE /transit/keys/{tenant_id}` (immediate). The controller first disables the key to immediately block encrypt/decrypt before the pending window elapses, records the scheduled deletion in the erasure receipt, and emits a `KmsKeyDeletionFailed` warning audit event (non-blocking) if the operation fails. Added corresponding `KmsKeyDeletionFailed` alert in Section 16.5 to surface lingering keys requiring manual cleanup.

### TNT-041. Runtime and pool resources are platform-global but `tenant-admin` manages them as if tenant-scoped [Medium]
**Section:** 10.2, 4.2, 15.1
Runtime and pool records have no `tenant_id` field or RLS policy.
**Recommendation:** Clarify whether these are global with application-layer filtering, or tenant-scoped with RLS.
**Status:** Fixed — Chose platform-global with application-layer filtering. Added explicit text to sections 4.2, 10.2, and 15.1 stating that runtime and pool records carry no `tenant_id` and are not subject to RLS. Visibility and write authorization are enforced via `runtime_tenant_access` and `pool_tenant_access` join tables. `platform-admin` creates global definitions and grants tenant access; `tenant-admin` can update only records already granted to their tenant. `tenant-viewer` and `tenant-admin` list/get endpoints are filtered to access-table entries for their tenant. Admin API table descriptions in 15.1 updated to reflect `platform-admin`-only create/delete and tenant-scoped update semantics. `tenant-admin` role description updated to mention the runtime/pool access restriction.

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
**Status:** Fixed — Added a "T4 cross-tenant reuse prohibition" paragraph immediately after the existing `allowCrossTenantReuse` paragraph in Section 5.2. The pool controller now explicitly rejects `allowCrossTenantReuse: true` on any pool whose Runtime has `workspaceTier: T4`, with a named error message referencing Section 6.4. A second defense-in-depth gate is also specified: the gateway rejects T4 session assignments to task-mode pods already used by a different tenant and retires such pods from the cross-tenant pool, guarding against misconfigured pools that bypass the pool-controller check.

---

## 9. Storage Architecture (STR)

### STR-043. Storage quota Redis failure mode undefined [Medium]
**Section:** 11.2, 12.4
`storage_bytes_used` counter absent from Redis failure behavior table.
**Recommendation:** Add a storage quota row; recommended fail-closed or Postgres fallback.
**Status:** Fixed
**Resolution:** Added a `storage_bytes_used` row to the Redis failure behavior table in Section 12.4. The defined behavior is: on Redis unavailability, the gateway rehydrates the per-tenant storage counter from the sum of `artifact_size_bytes` in Postgres (consistent with the rehydration path already described in §11.2). Upload pre-checks use the Postgres-derived value during the outage window. On dual-store outage (Redis + Postgres both down), storage uploads are rejected with 503 (fail closed). On Redis recovery, the rehydrated counter is written back to Redis and normal fast-path enforcement resumes. FLR-040 and POL-046 are duplicates — marked "Already Fixed" referencing this finding.

### STR-044. Eviction context MinIO objects never explicitly deleted [Medium]
**Section:** 4.4, 12.5, 12.8
Eviction context objects at `/{tenant_id}/eviction/{session_id}/context` have no GC path.
**Recommendation:** Delete on session terminal state; add to GC sweep and erasure scope.
**Status:** Fixed
**Resolution:** Three targeted additions: (1) Section 4.4 — specifies that when `session_eviction_state` is cleaned up on terminal state, the gateway also deletes the corresponding MinIO object (`/{tenant_id}/eviction/{session_id}/context`) before removing the Postgres row, with retry on failure and GC fallback; (2) Section 12.5 — adds an explicit GC sweep bullet for eviction context objects: the GC job queries terminal-state `session_eviction_state` rows where `last_message_context` is a MinIO object key and deletes orphaned objects; (3) Section 12.8 erasure scope table — adds eviction context objects to the `ArtifactStore` row so GDPR/tenant-deletion erasure also covers this prefix.

### STR-045. Partial checkpoint manifest objects lack a defined cleanup path [Medium]
**Section:** 4.4, 12.5
Partial MinIO parts from timed-out eviction checkpoints can become permanently orphaned.
**Recommendation:** Delete after successful/failed resume; add periodic sweep in GC job.
**Status:** Fixed — Added "Partial checkpoint manifest cleanup" paragraph to Section 4.4 specifying that the gateway MUST delete all referenced MinIO parts (via `AbortMultipartUpload`/`DeleteObject`) and the Postgres row on resume completion (success or failure), with exponential-backoff retry on failure. Added "Partial checkpoint manifests" bullet to the Section 12.5 checkpoint retention policy specifying the GC backstop sweep: on each cycle the GC job collects all `partial: true` rows where the session is terminal or `created_at` is older than `maxResumeWindowSeconds`, deletes the referenced MinIO parts, and removes the Postgres rows. Added `lenny_partial_manifest_cleanup_total` counter (labeled by `outcome: success|failed_deleted|gc_collected`) for observability.

### STR-046. No MinIO write throughput budget or IOPS analysis for checkpoint load at Tier 3 [Medium]
**Section:** 12.5, 17.8
~17 checkpoints/s at ~1.7 GB/s upload bandwidth is unquantified.
**Recommendation:** Add MinIO throughput estimates to Section 17.8.
**Status:** Fixed — Added five rows to the Object storage table in Section 17.8.2: estimated checkpoint write rate per tier (~0.2/s at Tier 1, ~1.7/s at Tier 2, ~17/s at Tier 3, derived from 10,000 sessions ÷ 600s interval), estimated upload bandwidth at average (100 MB) and max (512 MB) workspace sizes, and minimum required MinIO aggregate throughput for both sustained and burst scenarios. Added a "Tier 3 MinIO throughput budget" note block explaining the derivation (~17/s checkpoint rate → ~1.7 GB/s sustained at avg workspace, ~8.5 GB/s burst at max workspace), the recommended 8-node NVMe MinIO cluster providing ~10–12 GB/s (~40% headroom), key observability hooks (`lenny_checkpoint_duration_seconds` P95, MinIO `s3_requests_errors_total`), and the cloud-managed equivalent guidance (provider auto-scales but per-bucket request-rate quotas should be verified).

### STR-047. Cloud-managed object storage bucket versioning and lifecycle rules not specified [Medium]
**Section:** 12.5, 17.9
Self-managed MinIO rules documented but cloud equivalents (S3, GCS, Azure Blob) are not.
**Recommendation:** Add cloud-profile lifecycle rule requirements; add preflight validation.
**Status:** Fixed
**Resolution:** Three changes made to `technical-design.md`: (1) Section 17.9 cloud profile table note for Object storage updated to clarify that versioning and lifecycle rules must be deployer-configured and references the new subsection. (2) New subsection "Cloud Object Storage Lifecycle Requirements" added to Section 17.9 (Cloud-Managed Profile), specifying required rules (bucket versioning, delete-marker expiration ≤1 day, noncurrent-version expiration ≤1 day) with concrete CLI commands for S3, GCS, and Azure Blob. (3) New preflight check row "Cloud object storage lifecycle rules" added to the Section 17.6 preflight checks table, covering S3 `GetBucketVersioning`/`GetBucketLifecycleConfiguration`, GCS bucket lifecycle GET, and Azure `BlobServiceProperties`/`ManagementPolicy` checks; skipped for `provider=minio`.

### STR-048. T2 data "storage-layer encryption" requirement has no defined enforcement mechanism [Medium]
**Section:** 12.9
Redis encryption is "recommended" not "required"; no runtime enforcement for T2.
**Recommendation:** Clarify T2 means volume-level encryption; upgrade Redis language to "required."
**Status:** Fixed
**Resolution:** Added a "T2 storage-layer encryption definition" paragraph immediately after the Enforcement paragraph in Section 12.9. The paragraph: (1) defines "storage-layer" as volume-level encryption (CSI/node-disk encryption) distinct from T3/T4 application-layer encryption; (2) makes Redis AUTH, ACLs, and TLS explicitly **required** (referencing Section 12.4); (3) requires the preflight Job (Section 17.2) to validate encrypted backing volumes for Redis and Postgres, with a deployer attestation escape hatch (`preflight.attestVolumeEncryption: true`) when the cloud API is unavailable; (4) exempts dev mode from the check. This closes the enforcement gap without introducing new interfaces or scope.

### STR-049. GC leader election loss mid-cycle allows double-decrement of storage quota counter [Medium]
**Section:** 12.5
Redis counter decrement can happen before Postgres commit, causing double-decrement on crash recovery.
**Recommendation:** Issue Redis decrement only after Postgres commit succeeds.
**Status:** Fixed
**Resolution:** Added an explicit ordering requirement in two places. (1) Section 11.2 step 3 now states the Redis decrement MUST be issued only after the Postgres `artifact_store` row has been durably committed with `deleted_at` set, and explains the double-decrement hazard: crash after Redis decrement but before Postgres commit leaves the row `active`, rehydration restores the bytes, and the next GC cycle decrements again. Committing Postgres first eliminates this race. (2) Section 12.5 GC idempotency bullet now lists the correct step sequence (delete MinIO → commit Postgres `deleted_at` → decrement Redis) and cross-references Section 11.2 for rationale. The idempotency guarantee is preserved: the Postgres query filters on `WHERE deleted_at IS NULL`, so already-processed artifacts are never selected again.

### STR-050. `artifact_store` Postgres row not created for eviction context objects — quota not decremented [Medium]
**Section:** 4.4, 11.2, 12.5
Eviction context bytes are never reflected in `storage_bytes_used` counter.
**Recommendation:** Insert `artifact_store` rows for eviction context objects, or document as excluded.
**Status:** Fixed
**Resolution:** Added a new **"Storage quota accounting for eviction context objects"** paragraph in Section 4.4 (after the eviction fallback Postgres transaction description). It specifies that when the gateway writes an eviction context object to MinIO (the context > 2KB path), it MUST insert an `artifact_store` row (with `artifact_size_bytes` = confirmed object size, `object_key`, and `artifact_type = eviction_context`) in the same Postgres transaction as the `session_eviction_state` row. The Redis quota increment follows the standard post-upload path (Section 11.2 step 2) after both rows commit. This closes all three gaps: (1) Redis counter is incremented for eviction context bytes, (2) Redis rehydration on restart includes these bytes (via the `artifact_store` sum), (3) GC-triggered decrement has a valid row to reference. The inline truncation path (context ≤ 2KB or MinIO unavailable) explicitly skips both the `artifact_store` insert and the quota increment. Also updated Section 12.5's quota enforcement paragraph to state that `artifact_store` covers all MinIO-backed artifacts including eviction context objects, with a cross-reference to Section 4.4.

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
**Status:** Fixed. Added a **Durability** bullet to Section 8.6 step 4 ("User rejects") specifying that the `extension-denied` flag and rejection cool-off expiry timestamp are persisted to the `delegation_tree_budget` Postgres table (keyed by `root_session_id`) as part of the same periodic checkpoint transaction. The new gateway replica reads these fields on coordinator handoff before accepting any extension requests, preventing user rejections from being silently bypassed across gateway restarts or rolling updates.

### DEL-042. `treeId` path parameter in the admin API is undefined [Medium]
**Section:** 8.6, 15.1
`DELETE /v1/admin/trees/{treeId}/...` uses `treeId` which is never defined anywhere.
**Recommendation:** Define as alias for `root_session_id` or add as a distinct identifier.
**Status:** Fixed. Renamed `{treeId}` to `{rootSessionId}` in both Section 8.6 (inline reference) and Section 15.1 (admin API table). Added a clarifying note in the table description: "`rootSessionId` is the `root_session_id` of the delegation tree (the `session_id` of the root session that originated the tree)." This aligns the path parameter with the existing `root_session_id` concept used throughout the spec (Sections 8.6, 11.1, etc.) without introducing a new identifier.

### DEL-043. `maxTreeMemoryBytes` Redis counter not in Postgres checkpoint or crash-recovery [Medium]
**Section:** 8.2, 11.2, 12.4
On Redis recovery, the memory counter resets to zero, allowing trees to exceed their cap.
**Recommendation:** Add to `delegation_tree_budget` checkpoint; restore from archived node count.
**Status:** Fixed. Three targeted edits across Sections 8.2, 11.2, and 12.4:
- **Section 8.2:** Added a sentence noting that the `maxTreeMemoryBytes` counter is included in the periodic Postgres checkpoint and reconstructed on Redis recovery (cross-reference to Section 11.2).
- **Section 11.2 — Checkpoint list:** Added `maxTreeMemoryBytes` current accumulated value to the explicit list of counters written to `delegation_tree_budget` at each checkpoint interval. Added a rationale note explaining why it must be checkpointed independently of `maxTreeSize`: completed subtrees are offloaded and their memory reclaimed, so the two counters can diverge.
- **Section 11.2 — Crash Recovery for Delegation Budget Counters:** Expanded the reconstruction procedure to include step (d): compute `liveMemoryBytes` as (non-archived alive node count × `nodeMemoryFootprintBytes`, configurable via `delegationNodeMemoryFootprintBytes`, default 12 KB), and step (e): set the Redis memory counter to `max(postgres_checkpoint_memory, liveMemoryBytes)`. The MAX rule ensures a stale checkpoint never resets the counter below the live estimate.
- **Section 12.4 — Delegation budget counter reconciliation on Redis recovery:** Explicitly called out that the memory counter must be restored as part of Redis recovery reconciliation, and stated the consequence of omitting it (post-recovery zero counter allows over-cap delegations).

### DEL-044. `perChildMaxAge` extension does not retroactively extend running children's deadlines [Medium]
**Section:** 8.6
The extension is motivated by in-progress children, but only affects future children.
**Recommendation:** Add explicit note documenting this semantic mismatch.
**Status:** Skipped. The finding's premise — that the extension is "motivated by in-progress children" — is not supported by any spec text. Section 8.6 already contains an explicit "Scope" block (lines 3341–3343) stating: "Existing children are **unaffected** — their leases remain as originally granted / Only new children spawned after the extension benefit from the expanded parent budget." This rule is stated uniformly for all extendable fields; `perChildMaxAge` is no special case. The behavior is intentional (extending a policy field on the parent governs future children, not retroactively renegotiating already-granted leases), consistent with all other extendable fields (`maxTokenBudget`, `maxChildrenTotal`, etc.), and already documented. Adding a per-field repetition of the same scope note would be documentation noise, not a genuine correction. No change warranted.

### DEL-045. `maxParallelChildren` omitted from extendable/non-extendable field lists [Low]
**Section:** 8.6
Also missing: `maxDelegationPolicy`.
**Recommendation:** Add both to the appropriate list.

### DEL-046. `credentialPropagation: inherit` behavior undefined for cross-environment delegations [Medium]
**Section:** 8.3, 10.6
The child runtime may have different `supportedProviders`; behavior when pools don't match is unspecified.
**Recommendation:** Define cross-environment `inherit` semantics; specify rejection or fallback.
**Status:** Fixed
**Resolution:** Added a "cross-environment compatibility check" paragraph to the credential propagation pre-check block in Section 8.3. The gateway now computes the intersection of the parent's credential pool providers and the child runtime's `supportedProviders` before approving a cross-environment `delegate_task` with `credentialPropagation: inherit`. If the intersection is non-empty, delegation proceeds using a compatible credential. If empty, the delegation is rejected with `CREDENTIAL_PROVIDER_MISMATCH` before pod allocation. No automatic fallback to `independent` — explicit rejection requires the caller to use `independent` mode intentionally. This is consistent with the existing pre-check pattern and does not conflict with Section 10.6's bilateral declaration model, which governs whether the cross-environment delegation is permitted at all (orthogonal concern).

### DEL-047. Cross-environment bilateral declaration change semantics mid-tree unspecified [Medium]
**Section:** 10.6, 8.3
Whether checks are point-in-time or continuously enforced for grandchild delegations is unclear.
**Recommendation:** State explicitly whether cross-environment checks are live or snapshotted.
**Status:** Fixed
**Resolution:** The finding is genuine but narrow. Section 8.3 already states DelegationPolicy evaluation is point-in-time at each `delegate_task` call (including grandchild delegations), and the "Gateway enforcement at delegation time" header in 10.6 signals the same for bilateral declaration checks. However, the spec did not explicitly state whether cross-environment bilateral declaration checks (outbound/inbound declarations in steps 2 and 3) are live or snapshotted, nor did it clarify that `snapshotPolicyAtLease` does not cover them. Added a "Cross-environment check evaluation semantics" paragraph in Section 10.6 immediately after the enforcement step list, explicitly stating: bilateral declaration checks (steps 2–3) are always live (re-evaluated at each `delegate_task` call, including grandchild/deeper); active already-delegated sessions are not retroactively revoked; `snapshotPolicyAtLease` applies only to pool-label matching in step 4 and does not affect the bilateral declaration checks.

### DEL-048. Cycle detection does not cover external agent (connector/A2A) delegation targets [Medium]
**Section:** 8.2
External agents have no `(runtime_name, pool_name)` tuple for cycle detection.
**Recommendation:** Include external agents using `(connector_id, endpoint_url)` identity.
**Status:** Skipped
**Rationale:** External agent delegation via `lenny/delegate_task` is not a v1 feature. The spec explicitly marks `allowedExternalEndpoints` as a "v1 slot for future A2A support" — no external agent target is resolvable through `lenny/delegate_task` today. More fundamentally, full cycle detection through external agents is architecturally infeasible: external agents are opaque, meaning they may internally delegate back through a completely different Lenny entry point or through their own agent graph. The `(connector_id, endpoint_url)` tuple the finding recommends would only catch the shallow case where the same external endpoint appears twice in the Lenny-visible delegation chain — it cannot detect cycles that transit through the external agent's own graph. Recording a tuple for a target whose internal graph is invisible to the platform gives false confidence. The correct approach is to defer this to the external agent delegation design phase, where the spec can address what guarantees (if any) can be offered for opaque targets — likely only depth/budget limits with an explicit caveat that cycle detection is best-effort. No spec change warranted now.

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
**Status:** Fixed
**Resolution:** The finding is genuine — unlike `terminate` (which had explicit "adapter sends SIGTERM on timeout"), `interrupt_request` had no defined timeout outcome. SIGTERM is not the right fallback here because interrupt is a *pause*, not a termination. The chosen fix: on `deadlineMs` expiry without `interrupt_acknowledged`, the adapter transitions the session to `suspended` anyway (best-effort — deadline elapsed means the runtime is assumed to have stopped progressing) and returns `INTERRUPT_TIMEOUT` in the `Interrupt` RPC response to the gateway. The gateway logs the timeout and proceeds normally in `suspended` state. Three locations updated: (1) the lifecycle message table in Section 4.7 (`interrupt_request` Notes column), (2) the `suspended` state transition block in Section 6.2, and (3) the canonical session state machine diagram (also in Section 6.2).

### SLC-045. `starting` state absent from `maxIdleTimeSeconds` timer table [Low]
**Section:** 6.2
`maxSessionAge` includes `starting`; `maxIdleTimeSeconds` omits it.
**Recommendation:** Add `starting` row with behavior **Paused**.

### SLC-046. `resume_pending` state has no bounded wall-clock expiry [Medium]
**Section:** 6.2, 7.3
All timers are paused; warm pool exhaustion can cause indefinite stuck state.
**Recommendation:** Define that `maxResumeWindowSeconds` begins when `resume_pending` is entered.
**Status:** Fixed
**Resolution:** Validated as a genuine issue — permanent pool exhaustion or scheduler bugs can cause indefinite stuck state with no escape path. Implemented `maxResumeWindowSeconds` wall-clock timer that starts on entry to `resume_pending`. If it fires before a pod is allocated, the session transitions to `awaiting_client_action` (same path as retry exhaustion) rather than being killed outright, preserving client agency. Changes: (1) `maxSessionAge` timer table row updated to document the cap; (2) `maxIdleTimeSeconds` table row cross-referenced; (3) dedicated `resume_pending` wall-clock cap paragraph added after timer tables in §6.2; (4) session state machine in §7.2 gains two new transitions (`resume_pending → resuming` and `resume_pending → awaiting_client_action`); (5) `awaiting_client_action` semantics in §7.3 gains an "Entry paths" bullet listing both entry paths; (6) resume flow steps in §7.3 updated to show timer start and timeout escalation. No new config fields added — `maxResumeWindowSeconds` already existed.

### SLC-047. Derive lock released before copy — stale snapshot object race on live sessions [Medium]
**Section:** 7.1
TOCTOU window between lock release and MinIO copy; no error code for `NoSuchKey`.
**Recommendation:** Define `503 SNAPSHOT_UNAVAILABLE` error; document staleness bound.
**Status:** Fixed
**Resolution:** The TOCTOU framing was partially misleading: the lock correctly protects only the snapshot reference read, and each checkpoint creates a new MinIO object at a new path (never overwrites in-place), so the resolved object key is stable and immutable after lock release — there is no write-write race on the object during the copy. The only real gap was the missing error code for the object-not-found case (dangling reference due to GC bug or premature TTL). Two changes made: (1) Section 7.1 derive serialization paragraph extended with an explicit rationale explaining why releasing the lock before the copy is safe, and defining `503 DERIVE_SNAPSHOT_UNAVAILABLE` for the NoSuchKey failure path; (2) `DERIVE_SNAPSHOT_UNAVAILABLE` (`TRANSIENT`, 503) added to the error code table with description and `details.snapshotRef` field. Staleness bound was already documented ("up to the full checkpoint interval, default 10 minutes") in the same paragraph — no change needed there.

### SLC-048. SSE buffer overflow silently drops connection with no pre-drop event [Medium]
**Section:** 7.2
Client has no structured signal that events were lost.
**Recommendation:** Send `error(CLIENT_BUFFER_OVERFLOW)` event before dropping, or document as silent.
**Status:** Skipped
**Resolution:** The finding is invalid on close reading of the full section. The spec's SSE buffer policy (§7.2, line describing `maxInboxSize`) is not silent loss: when the buffer overflows the gateway drops the TCP connection (FIN), the client detects disconnect and reconnects with its last-seen cursor, and the gateway either (a) replays all missed events from the EventStore (if within the 20-minute replay window) or (b) emits a `checkpoint_boundary` event with an `events_lost` count when the cursor is outside the window. The `checkpoint_boundary` mechanism is an explicit, structured signal that events were lost, and clients are already required to treat `events_lost > 0` as a data-loss event. Adding an `error(CLIENT_BUFFER_OVERFLOW)` SSE frame before the drop would be unreliable (the frame itself may be dropped if the buffer is full) and is redundant with the reconnect-time signal. The existing design is sound; no spec change is needed.

### SLC-049. `finalizing` and `ready` states have no expiry timeout [Medium]
**Section:** 6.2, 7.1, 11.3
Sessions can be stuck indefinitely in these pre-run states.
**Recommendation:** Add `maxFinalizingTimeoutSeconds` and `maxReadyTimeoutSeconds`; clarify `terminate` preconditions.
**Status:** Fixed — `maxCreatedStateTimeoutSeconds` and `maxSessionAge` do not cover `finalizing` or `ready`; both states were genuinely unbounded. Three changes made: (1) §6.2 `maxSessionAge` timer table: added `finalizing` row (dedicated `maxFinalizingTimeoutSeconds` watchdog, default 600s, fires `failed/FINALIZE_TIMEOUT`; must be ≥ `setupTimeoutSeconds`) and `ready` row (dedicated `maxReadyTimeoutSeconds` watchdog, default 300s, fires `failed/READY_TIMEOUT` if client never calls `start`). (2) §11.3 timeouts table: added `Max finalizing state lifetime` (600s, `gateway.maxFinalizingTimeoutSeconds`) and `Max ready state lifetime` (300s, `gateway.maxReadyTimeoutSeconds`) rows. (3) §15.3 state-mutating endpoint preconditions: added `finalizing` and `ready` to `terminate` valid preconditions with a note explaining gateway aborts in-progress setup, releases pod, marks session `completed`.

### SLC-050. Coordinator handoff CAS retry loop has no retry limit [Low]
**Section:** 10.1
Under rapid failover, replicas could loop indefinitely.
**Recommendation:** Add 5-attempt limit with jittered backoff.

### SLC-051. Seal-and-export `draining` retry has no max duration or retry count [Medium]
**Section:** 7.1
Permanent MinIO unavailability holds pods in `draining` indefinitely.
**Recommendation:** Define `maxWorkspaceSealDurationSeconds` and `maxSealRetries`; add `WorkspaceSealStuck` alert.
**Status:** Fixed
**Resolution:** The finding is valid — a pod in `draining` awaiting a seal-and-export retry is still running and is not subject to Kubernetes `terminationGracePeriodSeconds`, which only applies once SIGTERM is sent. Kubernetes provides no automatic bound here. The "Seal-and-export invariant" paragraph in Section 7.1 (line 2591) was expanded to define: exponential backoff (initial 5s, factor 2×, cap 60s/attempt), a `maxWorkspaceSealDurationSeconds` pool-level config parameter (default: 300s) as the hard total deadline, and failure semantics — on exhaustion the session transitions to `failed` with reason `workspace_seal_timeout`, a `workspaceSealFailed` audit event is emitted, the pod is terminated anyway, and the `WorkspaceSealStuck` alert fires. A `lenny_workspace_seal_duration_seconds` histogram (labeled by `pool` and `outcome`) was also added. The `WorkspaceSealStuck` Warning alert entry was added to the Section 16.5 alert table. A single total-duration bound was chosen over a per-retry count to mirror the eviction checkpoint retry pattern (30s total budget) and to be simpler to reason about operationally.

---

## 12. Observability (OBS)

### OBS-038. Seven instrumented metrics missing from Section 16.1 canonical table [Medium]
**Section:** 4.6.1, 4.6.2, 8.3, 8.10, 16.1
Metrics used in runbooks and alerts but absent from the authoritative instrumentation checklist.
**Recommendation:** Add all seven metrics to Section 16.1.
**Status: Fixed** — Cross-referenced all sections named in the finding plus full audit. Found 13 metrics missing (not 7): 6 from §4.6.1 (`lenny_sandboxclaim_guard_rejections_total`, `lenny_warmpool_idle_pod_minutes`, `lenny_pod_claim_fallback_total`, `lenny_controller_leader_lease_renewal_age_seconds`, `lenny_controller_queue_overflow_total`, `lenny_orphaned_claims_total`), 3 from §8.3 (`lenny_redis_lua_script_duration_seconds`, `lenny_delegation_parallel_children_high_watermark`, `lenny_delegation_budget_return_usage_lag_total`), and 4 from §8.10 (`lenny_orphan_cleanup_runs_total`, `lenny_orphan_tasks_terminated`, `lenny_orphan_tasks_active`, `lenny_orphan_tasks_active_per_tenant`). All 13 added to §16.1 in two new subsections ("Warm Pool Controller" and "Delegation Tree Recovery") plus inline with existing delegation metrics. No entries were found to already exist.

### OBS-039. `SandboxClaimGuardUnavailable` and `OrphanTasksPerTenantHigh` absent from Section 16.5 alert table [Medium]
**Section:** 4.6.1, 8.10, 16.5
Both alerts are explicitly defined with "see Section 16.5" cross-references but don't appear there.
**Recommendation:** Add both to the Section 16.5 alert table.
**Status: Fixed** — Confirmed both alerts were genuinely absent from Section 16.5. Added `SandboxClaimGuardUnavailable` (Critical) to the Critical alerts table after `AdmissionWebhookUnavailable`, describing the `lenny-sandboxclaim-guard` webhook becoming unreachable for >30s with `failurePolicy: Fail`, blocking pod claims. Added `OrphanTasksPerTenantHigh` (Warning) to the Warning alerts table after `SandboxClaimOrphanRateHigh`, describing the per-tenant orphan task gauge exceeding 80% of `maxOrphanTasksPerTenant`. Both entries cross-reference their defining sections (4.6.1 and 8.10 respectively). WPL-032 is marked Already Fixed referencing this finding.

### OBS-040. `lenny_warmpool_idle_pods` vs `lenny_warmpool_ready_pods` — two names for the same concept [Medium]
**Section:** 4.6.1, 10.7, 16.1, 16.5, 17.7
Section 16.1 has no canonical name; runbooks and experiments use different names.
**Recommendation:** Assign canonical name `lenny_warmpool_idle_pods`; update all references.
**Status: Fixed** — Confirmed `idle` is the canonical pod state in the Sandbox CRD state machine (`warming → idle → claimed`). `lenny_warmpool_ready_pods` appeared once (Section 10.7 experiment monitoring table, line 4711) and is a stray inconsistency. Added canonical definition to Section 16.1: "Warm pods available (`lenny_warmpool_idle_pods`, gauge labeled by `pool` — number of pods in `idle` state ready to be claimed; used by `WarmPoolLow`, `WarmPoolExhausted`, and `PodClaimQueueSaturated` alerts; see Section 4.6.1)". Updated the one stray `lenny_warmpool_ready_pods` reference in Section 10.7 to `lenny_warmpool_idle_pods`. WPL-033 is marked Fixed as a duplicate of this finding.

### OBS-041. `lenny_redis_lua_script_duration_seconds` has no alert rule despite a defined threshold [Medium]
**Section:** 8.3, 16.1, 16.5
5ms threshold defined as operational guidance but never formalized as an alert.
**Recommendation:** Add `DelegationLuaScriptLatencyHigh` warning alert.
**Status: Fixed** — Validated that SCL-043's fix added the contention analysis and metric definition but did not add a formal alert rule. Added `DelegationLuaScriptLatencyHigh` (Warning) to the Section 16.5 warning alerts table. The alert fires when `lenny_redis_lua_script_duration_seconds{script="budget_reserve"}` P99 exceeds 5 ms for more than 2 minutes, matching the hard ceiling defined in Section 8.3. The alert description explains the SLO impact (lease renewal delays risking false expirations), cross-references Section 8.3 for the aggregate blocking formula, and notes the pre-alert operational threshold of P99 > 2 ms (the separation trigger also from Section 8.3).

### OBS-042. `lenny_session_last_checkpoint_age_seconds` has high-cardinality `session_id` label [Medium]
**Section:** 4.4, 16.1, 16.5
10,000 individual time series at Tier 3 with no aggregation guidance.
**Recommendation:** Add derived metric at Tier 3; include recommended PromQL.

**Status: Skipped** — The finding mistakes "cardinality exists" for "cardinality is a problem." 10,000 time series is entirely trivial for modern Prometheus/Thanos deployments (typical production stacks handle tens of millions). The `session_id` label is *required* for the `CheckpointStale` alert to be operationally useful: the alert fires when any session exceeds the staleness threshold, and operators need `session_id` to identify which session to investigate and act on — dropping the label would make the alert unfixable in practice. The Section 16.5 `CheckpointStale` condition already provides implicit aggregation guidance ("any active session has `lenny_session_last_checkpoint_age_seconds` > threshold"), which maps directly to `max(lenny_session_last_checkpoint_age_seconds) by (session_id) > periodicCheckpointIntervalSeconds`. Adding a recording rule or a derived aggregate metric for a 10k-series gauge is premature optimization that would clutter the spec without addressing any real problem. No change warranted.

### OBS-043. `lenny_warmpool_warmup_failure_total` is unnamed in Section 16.1, no direct alert for sustained failures [Medium]
**Section:** 4.6.1, 16.1, 17.7
Primary runbook diagnosis signal but no canonical definition or alert for sustained failure rate.
**Recommendation:** Add to Section 16.1; add `WarmPoolReplenishmentFailing` alert.
**Status: Fixed** — Validated that OBS-038's fix added 13 metrics but did NOT include `lenny_warmpool_warmup_failure_total` (which was only mentioned in the §17.7 runbook diagnosis step, not in the sections OBS-038 audited). Two changes made: (1) Added `lenny_warmpool_warmup_failure_total` (Counter) to Section 16.1 under "Warm Pool Replenishment" group, labeled by `pool`, `runtime_class`, `reason` (`image_pull_error`, `setup_command_failed`, `resource_quota_exceeded`, `node_pressure`), with cross-reference to `WarmPoolReplenishmentFailing` alert and §4.6.1. (2) Added `WarmPoolReplenishmentFailing` (Warning) alert to Section 16.5 after `WarmPoolReplenishmentSlow` — fires when `lenny_warmpool_warmup_failure_total` rate exceeds 1 failure/min for any pool for > 5 min, with `reason` label guidance and cross-reference to §4.6.1 and §17.7 runbook.

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
**Status:** Fixed
**Resolution:** The finding is genuine. Although tenant deletion writes its own receipt *after* Phase 4 clears the audit store (so tenant-deletion receipts are inherently safe), user-level erasure receipts ARE vulnerable: a subsequent `DeleteByUser` or `DeleteByTenant` call on `EventStore (audit)` could sweep the `gdpr.*` completion records that prove prior erasures occurred. Fixed by: (1) updating the `EventStore (audit)` row in the "Storage backends in erasure scope" table to explicitly note the `gdpr.*` exemption; (2) adding a new **`gdpr.*` audit event exemption from user-level deletion** paragraph specifying that `DeleteByUser` MUST filter `event_type LIKE 'gdpr.%'`, that `DeleteByTenant` similarly skips these rows, and that they are instead retained under the standard `auditRetentionDays` (default 7 years) before GC expiry.

### CMP-044. Legal hold `note` required when setting hold, but no `reason` required when clearing [Low]
**Section:** 12.8, 15.1
Clearing a hold produces an audit record with no justification.
**Recommendation:** Make `note`/`reason` required for both set and clear.

### CMP-045. No GDPR Data Subject Access Request (DSAR) or right-to-portability support [Medium]
**Section:** 12.8, 15.1
GDPR Articles 15, 16, 20 are unaddressed.
**Recommendation:** Add export endpoint or document as deployer responsibility with guidance.
**Status:** Fixed — Documented as deployer responsibility. Added a "Data Subject Access, Rectification, and Portability (GDPR Articles 15, 16, 20)" block to Section 12.8 explaining the processor/controller split: Lenny is the data processor; the deployer (data controller) owns DSAR obligations. The block documents platform primitives available for DSAR fulfillment (user-scoped admin API queries, JSON export of session/audit/billing records, erasure endpoint), clarifies Article 16 rectification scope limits (opaque agent content is deployer-managed), explains why a pre-built DSAR bundle endpoint is intentionally absent in v1, and directs deployers to document their DSAR procedures in their RoPAs. No new API endpoints were added — the finding did not justify them for a platform that operates as a processor.

### CMP-046. No security breach / incident notification mechanism [Medium]
**Section:** 12.8, 11.7, 16.5
No incident taxonomy, first-responder steps, or breach notification runbook.
**Recommendation:** Add "Security Incident Response" section.
**Status:** Fixed
**Resolution:** Added §11.8 "Security Incident Response" between §11.7 and §12. The section deliberately avoids an incident taxonomy (operational, deployer-specific) but covers: (1) a table of platform-surfaced security signals with section cross-references and severity (AuditGrantDrift, CredentialCompromised, DataResidencyViolationAttempt, AuditChainGap, AuditSIEMNotConfigured); (2) platform first-responder primitives (credential revocation endpoint, user invalidation, legal hold, audit trail); (3) breach notification responsibility attribution (deployer as data controller under GDPR Art 33/34, HIPAA §164.410); (4) IR plan guidance checklist delegating full procedures to deployer. Full incident taxonomy and runbooks are correctly left as deployer responsibilities — the spec defines what the platform provides, not how deployers run their SOC.

### CMP-047. `complianceProfile: fedramp` lacks impact-level distinction [Medium]
**Section:** 11.7, 16.4
FedRAMP Low/Moderate/High have materially different requirements.
**Recommendation:** Either expand to `fedramp-low`/`fedramp-moderate`/`fedramp-high` or document claimed baseline.
**Status:** Fixed — Documented claimed baseline rather than splitting into three profiles. `complianceProfile: fedramp` is a controls-enablement knob, not a certification claim. Added a FedRAMP baseline note to Section 11.7 stating that the profile targets FedRAMP Moderate (the controls it enforces — SIEM, AU-11 1-year retention, AU-9 INSERT-only grants, AU-12 pgaudit — align with Moderate). FedRAMP Low is a subset and is fully covered. FedRAMP High deployments additionally require `audit.retentionPreset: fedramp-high` (already present in the spec). Section 16.4 retention table updated with a `complianceProfile` pairing column and a paragraph clarifying that the profile and the retention preset are independent knobs that must both be set for FedRAMP High. Splitting into three separate profile values was rejected as over-engineering: the only material platform-level difference between Low/Moderate/High is retention duration, which is already handled by the existing `audit.retentionPreset` mechanism.

### CMP-048. Default retention preset named `soc2` is misleading for non-SOC 2 deployments [Low]
**Section:** 16.4
Default 365-day preset is labeled `soc2`, implying compliance readiness without the full control set.
**Recommendation:** Rename default to `standard` or `default-365d`.

### CMP-049. Erasure receipt durability not guaranteed for the regulatory retention period [Medium]
**Section:** 12.8, 16.4
365-day default audit retention is shorter than most GDPR enforcement windows (4-6 years).
**Recommendation:** Add minimum retention floor for compliance-class audit rows (e.g., 6 years).
**Status:** Fixed
**Resolution:** The finding was valid: §12.8 claimed `gdpr.*` events were retained for "default: 7 years" under `auditRetentionDays`, but §16.4 defines `auditRetentionDays` to default to 365 days — an internal inconsistency that would cause the standard audit GC to purge erasure receipts after 1 year. Fixed by:
1. Introducing a dedicated `audit.gdprRetentionDays` setting (default: 2555 days / 7 years) with a hard minimum floor of 2190 days (6 years) when `complianceProfile` is `gdpr` or `hipaa`. The audit GC applies this window exclusively to `gdpr.*` rows, independent of the general `audit.retentionDays`.
2. §16.4 "Log retention" paragraph updated to explicitly state that `gdpr.*` rows are exempt from the standard partition GC and governed by `audit.gdprRetentionDays`.
3. Defaults table (§18/appendix) updated with the new `GDPR erasure receipt retention` row.
The `auditRetentionDays` configurable presets (including `hipaa` at 6 years) remain for general audit rows; erasure receipts are now independently protected at a 7-year floor with a startup-rejection enforcement check.

### CMP-050. `billingErasurePolicy: exempt` not validated against `complianceProfile: hipaa` [Medium]
**Section:** 12.8, 11.7
Retaining original `user_id` in billing records may conflict with HIPAA minimum-necessary.
**Recommendation:** Emit warning/audit event when `exempt` is combined with regulated profiles.
**Status: Fixed**
Added a new paragraph to §12.8 immediately after the `billingErasurePolicy: exempt` description. The spec now requires that when a tenant is created or updated with both `billingErasurePolicy: exempt` and any regulated `complianceProfile` (`hipaa`, `fedramp`, `soc2`), the platform emits a `compliance.billing_erasure_exempt_regulated` audit event carrying `tenant_id`, `complianceProfile`, and the exempt policy value. The combination is not rejected — retaining identifiable billing records is a legitimate use case (HIPAA 45 C.F.R. §164.502(a)(2)(ii) payment operations) — but the policy decision is made explicitly visible in the audit trail and SIEM stream. The HIPAA minimum-necessary principle (45 C.F.R. §164.502(b)) is now cited directly, with a requirement that deployers document legal basis in their BAA. The event is also re-emitted on every gateway startup while the combination is active, preventing silent persistence across redeployments.

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
**Status:** Fixed — Changed `PUT` to `POST` for `/v1/admin/credential-pools/{name}/credentials/{credId}/re-enable`. The idempotency argument for `PUT` is weak: the `/re-enable` suffix is an action verb, making this an action endpoint by convention. Other action endpoints that are equally idempotent (e.g., `POST /revoke`) correctly use `POST`. Consistency and correct REST idioms both require `POST` here.

### API-055. `GET /v1/admin/sessions/{id}` referenced but never defined [Medium]
**Section:** 24.3, 15.1
Operators directed to an endpoint that doesn't exist in the API table.
**Recommendation:** Add the endpoint or replace with client-facing equivalent.
**Status:** Fixed — OPS-049's fix (Section 24.10) added the `lenny-ctl admin sessions get <id>` CLI command with `GET /v1/admin/sessions/{id}` as its API mapping, but did not add the endpoint to the canonical admin API table in Section 15.1. Added `GET /v1/admin/sessions/{id}` to the Section 15.1 admin API table immediately before the existing `POST /v1/admin/sessions/{id}/force-terminate` row. The new row documents that this endpoint returns the standard session state model plus internal pod assignment and pool details, requires `platform-admin`, and cross-references Section 24.10.

### API-056. `DELETE /v1/admin/erasure-jobs/{job_id}/processing-restriction` carries a required request body [Medium]
**Section:** 15.1
Many HTTP clients silently drop DELETE bodies.
**Recommendation:** Change to `POST /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction`.
**Status:** Fixed — Validated: the `justification` body field is required (used for audit trail), making the DELETE body non-optional. While some APIs use DELETE with bodies (e.g., Elasticsearch), Go's `net/http`, various proxies, and some load balancers (AWS ALB) silently strip DELETE bodies, creating a genuine interoperability risk for an action that requires operator-supplied data. Changed to `POST /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction` in both the admin API table (Section 15.1, line 6634) and the CLI reference table (Section 24.11). The `/clear-processing-restriction` action-verb suffix is consistent with other action endpoints in the spec (e.g., `/retry`, `/force-terminate`, `/re-enable`).

### API-057. Operational-plane items listed as API-managed have no corresponding admin endpoints [Medium]
**Section:** 15.1
~5 resource categories (Webhooks, Egress Profiles, Scaling Policies, Memory Store Config, User Role Assignments) have no API backing.
**Recommendation:** Add CRUD endpoints or move to Bootstrap plane.
**Status:** Fixed — Investigated each of the 5 items:

- **Webhooks**: Not a separately-managed resource. `callbackUrl` is a per-session field set at session creation; the term "Webhooks" in the operational plane list was misleading. Removed from the list.
- **Egress Profiles**: An enum field (`restricted`, `provider-direct`, `internet`, `none`) on pool/runtime definitions. Managed through existing pool endpoints (`PUT /v1/admin/pools/{name}`), not a separate CRUD resource. Removed from the list.
- **Scaling Policies**: A `scalePolicy` sub-field within pool definitions. Already managed through `PUT /v1/admin/pools/{name}` and `PUT /v1/admin/pools/{name}/warm-count`. Removed from the list.
- **Memory Store Config**: Pluggable at deploy time via Helm/bootstrap (choose Postgres+pgvector or custom backend). Not runtime API-managed. Moved to Bootstrap plane description.
- **User Role Assignments**: Genuine gap. The spec's permission matrix and Section 4.3 describe a platform-managed `user_id → role` Postgres mapping that `tenant-admin` can control, but no endpoints existed. Added: `GET /v1/admin/tenants/{id}/users`, `PUT /v1/admin/tenants/{id}/users/{user_id}/role`, `DELETE /v1/admin/tenants/{id}/users/{user_id}/role`, and `POST /v1/admin/users/{user_id}/invalidate` (the latter was referenced in Section 11.4 narrative but absent from the endpoint table).
- **Quotas**: Already covered — quota config is embedded in tenant records and managed via `PUT /v1/admin/tenants/{id}`. Added a clarifying parenthetical in the operational plane list.

The operational plane description was also corrected to remove the 3 non-resource items and add a note explaining their actual nature.

### API-058. `POST /v1/admin/billing-corrections/{id}/reject` absent from admin API table [Medium]
**Section:** 15.1, 11.2.1
The `approve` endpoint is listed; `reject` is not.
**Recommendation:** Add the `reject` endpoint.
**Status: Fixed** — Added `POST /v1/admin/billing-corrections/{id}/reject` row immediately after the `approve` row in the Section 15.1 admin API table. The description mirrors Section 11.2.1 semantics (platform-admin required, self-rejection rejected, pending record retained with `rejected` outcome for audit).

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
**Status: Fixed**
Added a sentence to the "Contribution path" bullet in Section 23.2 requiring the Phase 2 `CONTRIBUTING.md` to include a prominent early-development notice: unsolicited PRs will not be reviewed or merged until Phase 17a; bug reports and discussion via the issue tracker are welcome at any phase; the notice is removed/replaced as part of Phase 17a. This is consistent with the existing Phase 17a "no external contributor PR solicitation before 17a completes" gate and does not require any changes to the build sequence table.

### CPS-039. "Phase 17 deliverables" — no such phase in build sequence [Low]
**Section:** 23.2
Should be "Phase 17a deliverables".
**Recommendation:** Fix the cross-reference.

### CPS-040. BDfN governance has two conflicting exit triggers [Medium]
**Section:** 23.2
Phase-based (ends Phase 4) vs contributor-based (3+ contributors, only reachable Phase 17a+).
**Recommendation:** Remove phase-based qualifier; rely solely on contributor-based criterion.
**Status: Fixed** — The "(Phases 1-4)" qualifier was removed from the BDfN bullet in Section 23.2. The exit trigger is now solely contributor-based: "Single maintainer makes final decisions until the project reaches 3+ regular contributors, at which point governance transitions to a multi-maintainer steering committee." This is consistent with the community launch strategy (PRs discouraged until Phase 17a, meaning 3+ contributors cannot realistically be reached before then). The `GOVERNANCE.md` reference in the same section continues to state it documents the transition criteria, which is now unambiguous.

---

## 16. Warm Pool (WPL)

### WPL-032. `SandboxClaimGuardUnavailable` alert absent from Section 16.5 [Medium]
**Section:** 4.6.1, 16.5
Critical alert defined in prose with "see Section 16.5" but not in the table.
**Recommendation:** Add to Critical alerts table. *(Duplicate of OBS-039)*
**Status: Already Fixed** — Fixed as part of OBS-039. `SandboxClaimGuardUnavailable` added to the Critical alerts table in Section 16.5.

### WPL-033. `lenny_warmpool_idle_pods` vs `lenny_warmpool_ready_pods` — two names for same metric [Medium]
**Section:** 16.1, 16.5, 17.7, 10.7
Section 16.1 has no canonical name at all.
**Recommendation:** Assign canonical name; unify all references. *(Duplicate of OBS-040)*
**Status: Fixed** — Fixed as part of OBS-040. Canonical name `lenny_warmpool_idle_pods` added to Section 16.1; stray `lenny_warmpool_ready_pods` in Section 10.7 updated.

### WPL-034. `pod_startup_seconds` vs `pod_warmup_seconds` conflation risk in sizing guidance [Medium]
**Section:** 4.6.1, 4.6.2, 17.8.2
For SDK-warm pools the two values diverge significantly; inline example uses same value for both.
**Recommendation:** Add parenthetical note distinguishing the two for SDK-warm pools.
**Status: Fixed** — Two targeted additions made. (1) Section 4.6.1: added a parenthetical sentence immediately after the inline example (`minWarm >= 2 * 35 + 4 * 10 = 110`) noting that the example coincidentally uses the same value for simplicity, and that for SDK-warm pools `pod_startup_seconds` (container pull + runtime startup only) diverges significantly from `pod_warmup_seconds` (which adds SDK initialization, typically 20–60s extra), with a cross-reference to Section 4.6.2 for precise definitions. (2) Section 17.8.2 first-deployment sizing paragraph: added a **Note** sentence explaining that baseline table values use only `pod_startup_seconds = 10s` and that for SDK-warm pools the `pod_warmup_seconds` value in the burst term is typically 30–90s, requiring operators to substitute the observed SDK-warm time rather than relying on the table baseline.

### WPL-035. `active → paused` experiment transition leaves warm pods with no eviction deadline [Medium]
**Section:** 4.6.2, 10.7
`minWarm` set to 0 but `maxWarm` unchanged; idle pods persist until cert expiry (4h).
**Recommendation:** Also set `maxWarm` to 0 on pause; restore on reactivation.
**Status: Skipped (clarification added instead)** — The finding misidentifies an intentional design choice as a gap. Leaving `maxWarm` unchanged on `active → paused` is correct: existing warm pods must not be pre-terminated because they serve in-flight sessions already assigned to the variant. Draining them aggressively (by setting `maxWarm=0`) would kill active sessions. The 4h cert-expiry upper bound on lingering pods is acceptable and bounded. The contrast with `concluded` (which does set `maxWarm=0` because no re-activation is possible) is already present in the table. Rather than changing the behavior, the `active → paused` table row was clarified to explicitly state that `maxWarm` is intentionally left unchanged, explain the in-flight session continuity rationale, note the natural drain via cert expiry, and cross-reference the `concluded` row for comparison. No behavioral change was made.

### WPL-036. SDK-warm circuit-breaker trips but existing SDK-warm idle pods not flushed [Medium]
**Section:** 6.1, 4.6.1
Pool remains mixed SDK-warm/pod-warm for up to 4h after circuit break.
**Recommendation:** Drain existing idle SDK-warm pods on circuit-breaker activation.
**Status: Skipped (clarification added instead)** — The recommendation to drain existing idle SDK-warm pods on circuit-breaker activation is incorrect. Those pods completed SDK initialization successfully and are known-good; draining them would waste functional warm capacity and introduce a 30–90s pool gap precisely when the pool is under stress. The circuit breaker fires because the *workload* triggers demotion on nearly every claim — existing idle pods would be demoted on claim via the normal `requiresDemotion: true` + `DemoteSDK` path, paying the same penalty as before the circuit tripped. The mixed-state window (up to 4h) is intentional and harmless: the circuit stops producing *new* SDK-warm inventory while existing pods drain naturally through use and cert expiry. Section 6.1 was updated to make this design intent explicit: a new paragraph immediately following the circuit-breaker paragraph documents that existing idle SDK-warm pods are intentionally not drained, explains the known-good rationale, describes how they are served via the normal demotion path, and notes the bounded 4h mixed-state window. No behavioral change was made to the spec.

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
**Status:** Fixed. Added `llm_request_started` and `llm_request_completed` Runtime → Adapter messages to the lifecycle channel schema table (Section 4.7) and to the direction summary. Updated the in-flight gate prose (credential rotation step 1) to reference these messages by name and describe the per-provider in-flight counter mechanism. The undocumented `request_active` flag reference is fully replaced.

### CRD-035. `anthropic_direct` proxy mode provides no real revocability for compromised keys [Medium]
**Section:** 4.9
In direct mode, the actual API key persists at the provider after revocation.
**Recommendation:** Document direct-mode residual risk; require provider-side key rotation step.
**Status:** Fixed. The spec did not previously make this residual risk explicit — the "Leases are revocable" security boundary bullet was unqualified and could be misread as complete revocation in all modes. Three changes made: (1) In the Emergency Credential Revocation step 5 (direct-delivery mode), added an explicit "Residual risk (direct mode only)" callout stating that the materialized key continues to exist at the provider after Lenny-side revocation, that any party who extracted it can continue using it directly, and that operators MUST rotate or delete the key at the provider to achieve complete revocation. (2) In the emergency revocation runbook, extended step 6 to mark provider-side rotation as mandatory (not optional) for direct-mode pools, clarifying that updating only the Kubernetes Secret is insufficient until the provider also invalidates the key. (3) In the Security Boundaries section, added a direct-mode caveat to the "Leases are revocable" bullet, contrasting proxy mode (complete revocation — key never left the gateway) with direct mode (provider-side rotation required). The existing text noting direct mode is appropriate only for single-tenant/development deployments (line 1333) remains unchanged and provides the broader context.

### CRD-036. Section 17.8 cross-reference for credential pool sizing is a dead end [Medium]
**Section:** 4.9, 17.8
"See Section 17.8" leads to nothing on credential sizing.
**Recommendation:** Add credential pool sizing formula and per-tier starting values.
**Status:** Fixed
**Resolution:** Validated — the dead-end cross-reference was real. Section 17.8 had no credential pool sizing content despite multiple references pointing to it. Added a "Credential pool sizing" subsection at the end of §17.8.2 with: the sizing formula (`min_credentials >= ceil(peak_concurrent_sessions / maxConcurrentSessions_per_credential)`), a safety-margin formula with per-tier safety factors (1.3/1.2/1.2), a per-tier starting-values table (13/120/1,200 credentials at Tier 1/2/3 for `maxConcurrentSessions: 10`), and notes covering rotation impact, cloud-provider credentials with higher concurrency, and multi-provider deployments. Updated the cross-reference in §4.9 (line 1044) to point specifically to "Section 17.8.2 (Credential pool sizing)" and expanded it with a concrete description of what that section provides.

### CRD-037. Audit event catalog omits most credential lifecycle events [Medium]
**Section:** 4.9, 11.2.1
~9 credential events have no canonical table or storage destination.
**Recommendation:** Add "Credential Audit Events" summary table.
**Status:** Fixed — Added Section 4.9.2 "Credential Audit Events" table listing all 10 credential audit events (`credential.registered`, `credential.deleted`, `credential.rotated`, `credential.user_revoked`, `credential.leased`, `credential.revoked`, `credential.re_enabled`, `credential.renewed`, `credential.fallback_exhausted`, `credential.lease_spiffe_mismatch`) with their fields and storage destination (`EventStore` / Postgres). Clarified that `credential.leased` and `credential.revoked` are dual-written to the billing event stream (Section 11.2.1); all others are audit-only.

### CRD-038. KMS key rotation procedure missing rollback and partial-failure handling [Medium]
**Section:** 4.9.1
No idempotency guarantee, rollback path, or verification step before old key is disabled.
**Recommendation:** Add idempotency guarantee, rollback path, old key retention policy.
**Status: Fixed** — Section 4.9.1 extended with: (1) explicit idempotency guarantee on the re-encryption job (rows at `current_version` are skipped, safe to restart), (2) verification step made its own explicit sub-step gating the disable action, (3) old key retention policy (90 days minimum after disabling), and (4) a new "Rollback procedure" numbered item explaining how to safely abort mid-migration by reverting the default key ID and why partial migration is fully reversible.

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
**Status:** Fixed
**Resolution:** Validated that `OpenResponsesAdapter` (`/v1/responses`) and `OpenAICompletionsAdapter` (`/v1/chat/completions`) are distinct adapters with different wire formats — the Responses API output schema differs meaningfully from Chat Completions (typed output items, per-item IDs, file/image native types). A shared column or note would be inaccurate. Added a full `Open Responses` column to the Translation Fidelity Matrix with field-level fidelity tags for all 10 `OutputPart` fields, reflecting the adapter's richer type mapping (`id` is `[extended]` vs `[dropped]` for Completions; `status` is `[lossy]` vs `[dropped]`; `annotations` and `parts` nesting are both `[dropped]` as in Completions). Updated the Round-trip asymmetry summary table to reference `Open Responses` alongside MCP and OpenAI Completions for the fields that share asymmetric behavior (`schemaVersion`, `ref`, `annotations`, `type reasoning_trace`).

### SCH-045. `lenny-blob://` URI scheme omits session generation component [Medium]
**Section:** 15.4.1
A `ref` from generation 1 refers to different blob context than one from generation 3.
**Recommendation:** Add `gen=` query parameter or document content-addressed immutability.
**Status:** Fixed — Finding's premise (generation ambiguity) was invalid as described: the `coordination_generation` counter (Section 10.1) is a coordinator fencing mechanism, not a blob versioning concept. Part IDs are globally unique within a session and blobs are write-once per `(tenant_id, session_id, part_id)` triple, so no generation component is needed. Fixed by adding an explicit **Immutability guarantee** paragraph in Section 15.4.1 documenting the write-once property, the uniqueness of part IDs, and the safe-to-cache semantics of `lenny-blob://` URIs. The `gen=` query parameter was rejected as unnecessary complexity.

### SCH-046. `MessageEnvelope` carries no `schemaVersion` field despite being persisted [Medium]
**Section:** 15.4.1, 15.5
All other persisted types have `schemaVersion`; `MessageEnvelope` does not.
**Recommendation:** Add `"schemaVersion": 1` to the schema.
**Status:** Fixed — Validation confirmed the finding is valid: `MessageEnvelope` is persisted independently in the Postgres `session_messages` table (Section 15.4.1), not only as a nested sub-object inside `TaskRecord`. It is not always nested, so adding `schemaVersion` is not redundant. Three changes made: (1) Added `"schemaVersion": 1` to the `MessageEnvelope` JSON schema example in Section 15.4.1. (2) Added a prose paragraph explaining the field's semantics, gateway-injection rule, and forward-compatibility obligations (live vs. durable consumers). (3) Added `MessageEnvelope` to the canonical list of `schemaVersion`-bearing persisted types in Section 15.5 item 7.

### SCH-047. `WorkspacePlan` schema missing per-source conflict resolution mode [Medium]
**Section:** 14
No `onConflict` field when sources collide on the same path.
**Recommendation:** Add `onConflict: replace | skip | error` per source or at top level.
**Status:** Fixed — Finding is valid: path collision within the `sources` array is possible (e.g., an `inlineFile` entry and a subsequent `uploadArchive` that contains the same path). However, adding an `onConflict` enum field was rejected as over-engineering given that the cross-tier materialization order in Section 5.1 already establishes a last-writer-wins convention, and delegation exports (Section 8.7) apply the same rule. Fix: added a **Path collision rule** paragraph in Section 14.1 documenting that sources are applied in declaration order with last-writer-wins semantics, consistent with the cross-tier ordering. The paragraph also notes that a future `schemaVersion` may introduce a `onConflict` override field, and specifies that the gateway MUST emit a `workspace_plan_path_collision` warning event (with `path`, `winningSourceIndex`, `losingSourceIndex` fields) whenever a collision is detected, enabling operator and client visibility into unintended overwrites. No new schema field was added in this iteration.

### SCH-048. `WorkspacePlan.setupCommands` per-command `timeoutSeconds` has no global default [Medium]
**Section:** 14
Optional field with no defined fallback when omitted.
**Recommendation:** Document the fallback explicitly.
**Status:** Fixed
**Resolution:** The per-command `timeoutSeconds` is distinct from the runtime-level `setupPolicy.timeoutSeconds` (Section 5.1). The `setupPolicy.timeoutSeconds` is an aggregate wall-clock cap for the entire setup phase, not a per-command default. When the per-command field is absent a command runs unbounded until `setupPolicy.timeoutSeconds` (if set) terminates the whole phase, or indefinitely if that is also absent. Added a `workspacePlan.setupCommands[].timeoutSeconds` bullet to the Field notes in Section 14 that documents this fallback chain and advises clients to set the field explicitly when per-command bounds are needed.

### SCH-049. `RuntimeDefinition` merge table omits `capabilityInferenceMode` field [Medium]
**Section:** 5.1
Derived runtime authors can't determine merge behavior for this field.
**Recommendation:** Add to the Normative Merge Algorithm table.
**Status:** Fixed — Added `capabilityInferenceMode` row to the Normative Merge Algorithm table in Section 5.1 with **Override** behavior and a note that it does not affect tools with explicit `toolCapabilityOverrides`.

### SCH-050. `TaskRecord` `messages` array entries lack per-entry `schemaVersion` [Medium]
**Section:** 8.8
Mid-session gateway upgrades can write different schema versions into the same record.
**Recommendation:** Add per-entry `schemaVersion` or document top-level governs all entries.
**Status:** Fixed — Added a normative paragraph in Section 8.8 immediately after the `TaskRecord` JSON example clarifying the two-level versioning model: the top-level `TaskRecord.schemaVersion` (immutable once set, per Section 15.5 item 7) governs the outer record envelope fields; `OutputPart.schemaVersion` (per-entry, already present per Section 15.4.1) independently governs the parts content. The note explicitly addresses the rolling-upgrade scenario — messages written by different gateway replicas at different `OutputPart` schema versions are fully handled by per-entry `OutputPart.schemaVersion`, and durable-consumer forward-read rules (Section 15.5 item 7) MUST be applied independently at both levels. Adding a redundant per-entry `schemaVersion` on the message envelope itself was rejected: the `{ role, parts, state }` envelope is intentionally minimal and stable, and the `OutputPart` type already carries the fine-grained versioning needed for intra-record variation.

### SCH-051. `billing_correction` cross-version application guidance absent [Low]
**Section:** 11.2.1, 15.5
Consumers applying corrections across schema versions have no guidance.
**Recommendation:** Clarify that correction event's `schema_version` governs interpretation.

### SCH-052. `WorkspacePlan` schema omits concurrent-workspace slot-scoped materialization [Medium]
**Section:** 14, 5.3
No per-slot workspace differentiation is possible in the schema.
**Recommendation:** Document as out-of-scope or add `slotOverrides` field.
**Status:** Fixed
**Resolution:** Added an explicit scope note at the top of Section 14 documenting that per-slot workspace differentiation is intentionally out of scope. In `concurrencyStyle: workspace` pools, the `WorkspacePlan` is a shared template materialized independently for each slot into its own directory — the pool model depends on this uniformity for pre-warming. Clients needing per-task workspace variation should create separate sessions. A `slotOverrides` field would conflict with the architectural invariant that all slots on a pod share one workspace template; documenting the boundary is the correct resolution.

### SCH-053. `runtimeOptionsSchema` override rule allows silent breaking changes [Medium]
**Section:** 5.1, 14
"MAY NOT declare properties that the base schema forbids" has no defined validation algorithm.
**Recommendation:** Specify the exact validation algorithm and error code.
**Status:** Fixed — Added validation algorithm and error code to both locations.
- Section 5.1 Normative Merge Algorithm table: Notes column for `runtimeOptionsSchema` now states that a derived schema may only reference property names present in the base schema's `properties` map, and that violation is rejected at registration with `INVALID_DERIVED_RUNTIME: runtimeOptionsSchema declares forbidden property '<name>'`.
- Section 14 prose: Appended a **Validation:** sentence defining the set-difference algorithm (`derived.properties.keys() − base.properties.keys()`) and the error code, plus a clarifying note that constraints on existing properties (tightened bounds, added `enum`, changed `default`) are permitted.

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

### BLD-036. LLM Proxy subsystem has no phase assignment [High] ✓ Fixed
**Section:** 18, 4.1, 4.9
Core gateway component required for recommended multi-tenant configuration has no phase.
**Recommendation:** Add explicit phase between 5.5 and 6, or add to Phase 5.5 with limitations noted.
**Resolution:** Added new Phase 5.8 (between Phase 5.75 and Phase 6) to Section 18 that explicitly implements the LLM Proxy subsystem — the gateway's fourth internal subsystem boundary. Phase 5.8 covers: lease-token validation, credential injection, upstream forwarding (unary + streaming), `PreLLMRequest`/`PostLLMResponse` interceptor wiring, `deliveryMode: proxy` pool registration, SPIFFE-binding, per-subsystem goroutine pool/metrics/circuit-breaker, admission control enforcement (`lenny-direct-mode-isolation` webhook), and integration tests. Proxy mode is limited to `anthropic_direct` at Phase 5.8; multi-provider proxy injection and deny-list enforcement are deferred to Phase 11 (which was updated to reference Phase 5.8 as its foundation). Phase 5.5's "Limitations" block was also updated to explicitly state that `deliveryMode: proxy` is not yet available at that stage and will be introduced in Phase 5.8.

### BLD-037. Phase 5.5 "Limitations" omits proxy mode absence [Medium] ✓ Already Fixed
**Section:** 18, 4.9
Operators could deploy below recommended security baseline without knowing.
**Recommendation:** Add explicit limitation bullet about proxy mode deferral.
**Resolution:** Already addressed by BLD-036's fix. The Phase 5.5 row in Section 18 now explicitly states: "LLM proxy delivery mode (`deliveryMode: proxy`) is not yet available — all credential delivery in this phase is direct mode only (`deliveryMode: direct`). Proxy mode is introduced in Phase 5.8; until then, multi-tenant deployments must use sandboxed isolation (`isolationProfile: sandboxed`) with direct mode, and direct mode with `isolationProfile: standard` is blocked by admission control in `tenancy.mode: multi` (see Section 4.9)." This is a direct limitation bullet about proxy mode deferral embedded within the Phase 5.5 "Limitations at this stage" text. No further changes required.

### BLD-038. `DelegationPolicy` created in Phase 3 but not evaluated until Phase 9 [Medium] — **Fixed**
**Section:** 18, 4.8, 8.3
Resources exist for 6 phases without enforcement testing.
**Recommendation:** Add Phase 9 milestone gate testing policies from earlier phases.
**Resolution:** Validated that the 6-phase gap is architecturally sound: `DelegationPolicy` CRD is inert during Phases 3–8 because `lenny/delegate_task` does not exist until Phase 9 — there are no delegation calls for `DelegationPolicyEvaluator` to intercept. The gap is a normal incremental build dependency, not a risk window. However, Phase 9 lacked an explicit gate confirming that policies registered from Phase 3 onward are correctly enforced when delegation first becomes active. Fixed by adding a **`DelegationPolicy` enforcement gate** to the Phase 9 build sequence entry (Section 18): integration tests covering (1) allow-rule satisfaction, (2) deny-rule rejection with expected error codes, (3) `lenny/discover_agents` policy scoping, and (4) budget propagation across parent→child chains — all exercisable via `delegation-echo` runtime without LLM credentials, required before Phase 9 is complete. The milestone text was also updated to reflect this gate.

### BLD-039. Audit logging arrives at Phase 13 but auditable events exist from Phase 5.5 [Medium]
**Section:** 18, 12.4, 11.7
7-phase gap where audit events are silently dropped.
**Recommendation:** Extract minimal audit sink to Phase 5.5 or document the gap.
**Status:** Skipped — gap is intentional and acceptable; documented in spec.
**Resolution:** The finding overstates the gap. Phase 7 already introduces basic policy-decision audit events (auth denials, quota rejections, rate-limit hits) as part of the policy engine — the spec explicitly states "Full rate-limit rules, DelegationPolicyEvaluator, and audit logging ship in Phase 7." The true gap is only Phase 6 (one phase of developer integration testing). Phase 13 adds the *durable* audit infrastructure: append-only Postgres tables, hash-chain integrity, SIEM connectivity, and compliance profile enforcement (Section 11.7). All pre-Phase 13 activity occurs in development/testing environments with no regulated tenants; the full audit stack is not a compliance requirement during these phases. The "silently dropped" framing is misleading — structured logs from Phase 2.5 cover operational events throughout, and Phase 7 covers policy decisions. The Phase 13 row in Section 18 has been clarified to distinguish Phase 7 basic policy audit events from the Phase 13 durable audit storage and integrity controls, and to explicitly acknowledge the pre-Phase 13 gap as an accepted development-phase trade-off.

### BLD-040. Phases 12a/12b/12c are forced sequential but are independent [Medium]
**Section:** 18
All three could proceed in parallel after Phase 11.5.
**Recommendation:** Add note that they may proceed in parallel.
**Status:** Fixed — Added a note block between Phase 11.5 and Phase 12a explicitly stating that Phases 12a, 12b, and 12c are independent of each other and may proceed in parallel once Phase 11.5 is complete, with each phase's actual prerequisites identified (12a→Phase 5.5; 12b→Phase 5.5 + core session infra; 12c→Phase 5.5 + Phase 4). The table was split at the 11.5/12a boundary to accommodate the note block, matching the established pattern used elsewhere in Section 18.
**Resolution:** The finding is valid. Although no explicit sequential ordering was stated, sequential table listing implies ordering to implementers and AI agents alike. The fix eliminates the ambiguity by making parallelism explicit. No phase dependency semantics were changed — only the omitted parallelism note was added.

### BLD-041. `noEnvironmentPolicy: deny-all` blocks all users during Phases 5–14 [Medium]
**Section:** 18, 4.2, 17.6
No environments exist before Phase 15; default deny-all makes the platform unusable.
**Recommendation:** Configure `allow-all` in bootstrap seed for pre-Phase 15 deployments.
**Status:** Fixed — Two changes made to the spec: (1) A note block added after the Phase 5 note in Section 18 explicitly calling out the `noEnvironmentPolicy` access gap in Phases 5–14, explaining that the platform default of `deny-all` will block all `user`-role principals from Phase 5 onward unless the Phase 4.5 bootstrap seed is configured appropriately, and providing two resolution options (set `allow-all` on the default tenant, or seed at least one environment covering all initial users). (2) Section 17.6 bootstrap seed Day-1 minimum table updated: a new "Tenant RBAC config" row added (marked required when no environments are seeded), followed by an explanatory note block detailing both remediation options and the security trade-off between them. `platform-admin` and `tenant-admin` roles are explicitly noted as unaffected.
**Resolution:** The finding is valid. Phase 5 activates `noEnvironmentPolicy` enforcement 10 phases before the Environment resource arrives in Phase 15. The platform default of `deny-all` means a fresh deployment following the minimum Day-1 seed guidance (which marks Environment as optional) will produce a system where regular users cannot access any runtime. The fix makes the bootstrap seed requirement explicit at both the build-sequence level (Section 18 Phase 5 note) and the installation guidance level (Section 17.6 Day-1 table), without altering the platform default or the security model. No phase dependencies were changed.

### BLD-042. Phase 16 adds PoolScalingController logic after Phase 14.5 load-test sign-off [Medium]
**Section:** 18, 11.5, 16.5
Experiment integration invalidates the Phase 14.5 SLO baseline.
**Recommendation:** Move Phase 16 before 14.5, or add Phase 16.5 SLO re-validation.
**Status:** Fixed
**Resolution:** The finding is valid. Phase 16's PoolScalingController experiment integration adds new code paths (variant pool sizing, base pool `minWarm` adjustment on experiment activation/deactivation, status-transition reconciliation) that were not exercised during Phase 14.5. Although these paths are inactive when no `ExperimentDefinition` exists — preserving the Phase 14.5 baseline for non-experiment deployments — any deployment that does use experiments runs untested scaling behavior after GA. Moving Phase 16 before 14.5 is not feasible: experiments depend on the Environment resource introduced in Phase 15. The adopted fix is to add a **Phase 16.5** entry to the build sequence (Section 18) that re-runs the Phase 14.5 SLO scenarios with at least one active A/B experiment routing 20% of traffic to a variant pool. The note explicitly scopes the requirement: SLO regression relative to the Phase 14.5 baseline must be resolved before Phase 17a; deployments that never define experiments may treat Phase 14.5 as sufficient. No changes were made to Section 11.5 (credential load test) or Section 16.5 (SLO table) — only the build sequence table in Section 18 was modified.

### BLD-043. Client SDKs (Go + TypeScript) have no phase assignment [Medium]
**Section:** 18, 15.6
v1 deliverables with no build schedule.
**Recommendation:** Assign to Phase 6 or Phase 4.
**Status:** Fixed — Section 15.6 confirms client SDKs are v1 deliverables, and the build table in Section 18 had no phase row for them. Phase 6 was selected because the SDKs specifically encapsulate streaming with automatic reconnect-with-cursor (per Section 15.6), which is the feature delivered in Phase 6 (interactive session model). The Phase 6 row in Section 18 now includes the client SDK deliverable, with the milestone updated to reflect that official Go and TypeScript SDKs are available alongside the interactive model.

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
**Status:** Already Fixed — duplicate of STR-043. See STR-043 for resolution details.

### FLR-041. Eviction fallback chain silent data loss when both MinIO and Postgres fail [Medium]
**Section:** 4.4, 12.3, 12.4
Triple failure (MinIO + Postgres + node drain) leaves session unrecoverable with no signal.
**Recommendation:** Emit `session.lost` event; add `lenny_session_eviction_total_loss_total` counter.
**Status:** Fixed
**Resolution:** The finding was validated as a real (if rare) silent-failure gap. While the pre-drain MinIO health check webhook (Section 12.5) eliminates most planned-drain instances, spontaneous node failures and forced-drain bypasses remain live exposure paths. A new **"Total-loss path: MinIO and Postgres both unavailable during eviction"** paragraph was added to Section 4.4 specifying: (1) `session.lost` event emitted on the session event stream with `reason: "eviction_total_loss"` and diagnostic error fields (best-effort delivery, skipped if stream unavailable); (2) `lenny_session_eviction_total_loss_total` counter (labels: `pool`, `had_prior_checkpoint: true|false`) distinct from the existing `lenny_checkpoint_eviction_fallback_total`; (3) CRITICAL-level structured log. A `SessionEvictionTotalLoss` Critical alert was added to the Section 16.5 alerting table, firing immediately on any non-zero counter increment. Sections 12.3 and 12.4 required no changes — the triple-failure scenario is already bounded by the Postgres HA (Section 12.3) and the pre-drain MinIO webhook (Section 12.5), and the fix lives entirely in the Section 4.4 eviction fallback prose and the 16.5 alert table.

### FLR-042. KMS/JWTSigner unavailability not addressed as a failure mode [Medium]
**Section:** 10.2
No fallback, circuit breaker, or degraded-mode definition for KMS outage.
**Recommendation:** Add JWT cache, circuit breaker, and `KMSSigningUnavailable` alert.
**Status:** Fixed — Validation confirmed the finding is distinct from OPS-046/Token Service outage: the `JWTSigner` KMS path (gateway signing new session JWTs) is separate from the Token Service KMS path (decrypting stored OAuth tokens). Two changes made: (1) §10.2 — added "KMS signing failure mode" paragraph specifying that KMS unavailability during JWT minting causes new session creation to fail with retryable `KMS_SIGNING_UNAVAILABLE` (HTTP 503), that existing sessions are unaffected (verification uses cached public keys), and that the `JWTSigner` subsystem is wrapped in the Section 11.6 circuit breaker (trips open after > 3 consecutive failures within 30s). (2) §16.5 warning alerts table — added `KMSSigningUnavailable` alert keyed on `lenny_gateway_kms_signing_errors_total` rate > 1/30s for > 60s, with cross-reference to §10.2 and operator remediation guidance. Note: a "JWT cache" as originally recommended is not applicable to signing (you cannot cache a signed token as a fallback for signing a new one); the circuit breaker + retryable error pattern is the correct degraded-mode definition.

### FLR-043. `MinIOUnavailable` alert lacks canonical condition definition [Low]
**Section:** 4.4, 12.5, 16.5
Runbook references it but it's absent from the alert table.
**Recommendation:** Add to Section 16.5 with defined condition.

### FLR-044. Postgres failover window and eviction race not fully bounded [Medium]
**Section:** 12.3, 4.4
Checkpoint metadata record cannot be committed during Postgres failover.
**Recommendation:** Add retry within grace period; log MinIO object keys for manual recovery.
**Status:** Fixed
**Resolution:** The gap was genuine: the Postgres fallback write (after MinIO retry exhaustion) had no retry of its own — a single transaction failure during a 15–30s managed failover would immediately trigger total-loss. The `terminationGracePeriodSeconds` (240s/300s) had the headroom but the spec never directed the code to use it.

Fixed in Section 4.4 by:
1. Added an explicit **60-second Postgres fallback retry budget** (exponential backoff: 500ms initial, 2× factor, 5s cap) on the `session_eviction_state` write. This fully covers the 15–30s managed failover window (RDS Multi-AZ, Cloud SQL HA) while fitting well within `terminationGracePeriodSeconds`.
2. Added **MinIO object key logging** (`WARN`-level, fields: `committed_minio_keys`, `session_id`, `tenant_id`, `generation`, `minio_error`) before entering the total-loss path, enabling manual recovery from committed checkpoint parts.
3. Added `lenny_checkpoint_eviction_partial_keys_logged_total` counter (labels: `pool`, `keys_committed: 0|1+`) for observability.
4. Updated the total-loss path description to correctly state it is reached after Postgres retry exhaustion, not after a single write failure.
5. Fixed an incidental inconsistency in the same paragraph: `terminationGracePeriodSeconds` was cited as "120s" — corrected to "240s at Tier 1/2, 300s at Tier 3 (Section 17.8)".

### FLR-045. Controller crash during active scale-down drain has no fencing [Medium]
**Section:** 4.6.1, 10.1
New leader may re-signal draining pods or miss mid-checkpoint pods.
**Recommendation:** Add generation-stamped drain operation record in `SandboxWarmPool.status`.

**Status: Fixed** — Recommendation partially invalidated on review; addressed with a targeted spec clarification rather than a new status field.

**Resolution:**
The finding's concern is legitimate in framing but the proposed fix (generation-stamped drain record) is unnecessary. Analysis shows two distinct cases:

1. **Scale-down drain of idle pods** — idle pods have no active session and no in-flight checkpoint. The new leader's reconciler re-lists `Sandbox` resources on startup and finds pods already in `draining` phase; it advances them idempotently toward deletion. Re-processing a `draining` pod produces the same outcome as the original drain signal — no double-drain hazard, no missed checkpoint (there is none).

2. **Admin `DrainPool` with active sessions** — checkpoint coordination is owned by the gateway, not the controller. The `coordination_generation` CAS mechanism (Section 10.1) already provides full fencing: a new coordinator must increment the generation and receive a `CoordinatorFence` acknowledgement from the pod before issuing any operational RPCs. The controller only removes the finalizer after the gateway writes a terminal session state; it does not drive checkpointing directly and therefore needs no independent drain fencing record.

The K8s Lease-based leader election single-writer guarantee ensures the new leader reads the current authoritative `Sandbox.status.phase` from etcd before acting. The spec was silent on what the new leader does with in-progress `draining` pods — this was the real gap. Fixed by adding an explicit "Controller crash during active scale-down drain — no additional fencing required" paragraph in Section 4.6.1 explaining the idempotent reconciler behaviour and the reliance on `coordination_generation` for active-session cases. No new `SandboxWarmPool.status` field added.

### FLR-046. GC job gateway-internal leader failure has no explicit recovery time bound [Low]
**Section:** 12.5
No early-warning signal for GC leader loss separate from backlog accumulation.
**Recommendation:** Add `lenny_gc_last_successful_cycle_age_seconds` gauge and `GCLeaderStuck` alert.

### FLR-047. `dualStoreUnavailableMaxSeconds` timer reset on coordinator crash creates unbounded degraded window [Medium]
**Section:** 10.1
Each replacement replica resets its timer, preventing graceful termination from ever triggering.
**Recommendation:** Anchor timer to session's last successful store interaction timestamp.
**Status: Fixed**
**Resolution:** The finding's "unbounded" characterization and its premise that replacement coordinators reset the timer were both incorrect. Item 3 of the dual-store protocol explicitly states "no handoffs occur" while both stores are down — no replacement coordinator can acquire a session and restart the countdown, because coordinator handoff requires a Postgres CAS write. The recommended fix (anchoring to a Postgres timestamp) is also self-contradictory: reading that timestamp from Postgres is impossible during a dual-store outage. The actual bound is `max(dualStoreUnavailableMaxSeconds, coordinatorHoldTimeoutSeconds)`. However, the spec was genuinely ambiguous about (a) whether the timer is per-replica or per-session, and (b) how coordinator crashes during dual-store outage interact with the timer. Fixed by adding a **Timer anchoring** note to Section 10.1 item 4 that clarifies: the countdown is per-replica, anchored to detection time, not reset by coordinator crashes; sessions whose coordinator crashes are governed by `coordinatorHoldTimeoutSeconds` on the pod (120s). Also added `gateway.dualStoreUnavailableMaxSeconds` to the configuration parameters table (it was previously absent).

### FLR-048. CheckpointBarrier protocol has no defined behavior when Postgres is unavailable during preStop [Medium]
**Section:** 10.1
Barrier correctness depends on Postgres write; failure causes duplicate tool execution risk.
**Recommendation:** Include `last_tool_call_id` in CheckpointBarrierAck payload as fallback.
**Status: Fixed**
**Resolution:** Validated as real. The original spec stored `last_tool_call_id` only in `session_checkpoint_meta` (Postgres) and explicitly stated it was "not part of the workspace snapshot." If Postgres was unavailable during preStop while MinIO was healthy, the checkpoint workspace would persist to MinIO but the deduplication key would be lost — the new coordinator would not know which tool calls had completed and could re-dispatch them. The eviction fallback does not cover this scenario: it only triggers on MinIO failure, and it does not track `last_tool_call_id` regardless. Fixed by making the MinIO checkpoint manifest the **primary durable source** for `last_tool_call_id`: the adapter now embeds `barrier_meta.last_tool_call_id` in the checkpoint object before emitting `CheckpointBarrierAck` (manifest write is part of the checkpoint flush and covered by the same retry budget). `session_checkpoint_meta` in Postgres remains as a secondary fast-lookup path. On coordinator handoff, the new coordinator reads from Postgres first and falls back to the MinIO manifest if the record is absent. A `coordinator_resume_meta_source` label (`postgres` | `checkpoint_manifest`) on `coordinator_resume_deduplicated_total` tracks how often the MinIO fallback is exercised. The recommended fix of passing `last_tool_call_id` in the ack payload was already implicitly true; the actual gap was the absence of a Postgres-independent durable store for this field.

---

## 21. Experimentation (EXP)

### EXP-033B. Multi-variant hash bucketing formula undefined [Medium] — CARRIED FORWARD
**Section:** 10.7
Only binary control/treatment documented; multi-variant partitioning algorithm absent.
**Recommendation:** Provide full bucketing algorithm with cumulative weight partitioning.
**Status: Fixed** — Added a dedicated "Bucketing algorithm (percentage mode)" block in Section 10.7, immediately after the targeting schema YAML. The block defines the full HMAC-SHA256-based cumulative-weight partitioning algorithm: (1) derive a bucket value in [0.0, 1.0) via HMAC-SHA256(key=experiment_id, message=assignment_key); (2) walk variants in definition order accumulating cumulative weight, assigning the session to the first variant whose cumulative upper boundary exceeds the bucket; (3) fall through to "control" if no variant matched. Properties documented include: determinism, ordering sensitivity (append-only guidance for mid-flight changes), cross-experiment independence, and concrete two-variant and three-variant examples. The YAML comment for percentage mode was updated to reference this block instead of repeating the (now-superseded) binary-only formula.

### EXP-033C. Gateway creating materialized view at runtime contradicts DDL-through-migrations pattern [Medium] — FIXED
**Section:** 10.7
`CREATE MATERIALIZED VIEW` at runtime bypasses migration tooling.
**Recommendation:** Move creation to schema migration system.
**Resolution:** Section 10.7 updated. The materialized view (`lenny_eval_aggregates`) is now explicitly defined as part of the schema migration system (created at migration time, never at runtime). The Helm parameter `evalAggregationRefreshSeconds` now controls only whether the gateway schedules periodic `REFRESH MATERIALIZED VIEW CONCURRENTLY` calls — not whether the view is created. When the parameter is `0`, the gateway reads from the base `eval_results` table directly; when positive, it routes to the pre-existing materialized view and refreshes it on the configured interval.

### EXP-037. `dryRun` validation says "sum to 1.0" but constraint is "sum < 1.0" [Medium]
**Section:** 15.1, 4.6.2, 10.7
Contradictory validation rule; control group gets 0% traffic at 1.0.
**Recommendation:** Fix to "Σ variant_weights must be in [0, 1)".
**Status:** Fixed
**Resolution:** Section 15.1 (line ~6913) dryRun endpoint-specific semantics for experiments stated "variant weight normalization (sum to 1.0)" — contradicting the correct constraint defined in Section 10.7 (bucketing algorithm comment: `Σ weights < 1.0 — remainder is control`) and Section 4.6.2 (base pool adjustment formula rejects `Σ variant_weights >= 1`). Fixed the Section 15.1 text to: "variant weight constraint (Σ variant_weights must be in [0, 1) — remainder is reserved for the control group)". Sections 4.6.2 and 10.7 were already correct and required no changes.

### EXP-038. `EvalResult` records absent from GDPR erasure scope [Medium]
**Section:** 12.8, 10.7
Postgres records keyed by `session_id` not in erasure scope table or deletion order.
**Recommendation:** Add `EvalResult` to erasure scope table and Phase 4 deletion order.
**Status:** Fixed
**Validation:** `EvalResult` records contain `session_id` (a pseudonymous identifier linkable to a `user_id`), numeric scores, and a `metadata` jsonb field explicitly described as "arbitrary key-value pairs" that may contain deployer-supplied PII. Even if scores alone were not PII, the `session_id` FK makes these records linkable to the data subject, and the open-ended `metadata` field can carry personal content. Erasure is warranted.
**Resolution:** Two changes made to Section 12.8 of `technical-design.md`: (1) Added `EvalResultStore` row to the "Storage backends in erasure scope" table, noting that `metadata` may contain PII and that deletion must precede `SessionStore` due to the FK dependency; (2) Inserted `EvalResultStore` into the Phase 4 tenant deletion order immediately before `SessionStore` (`→ EvalResultStore → SessionStore`). This satisfies the FK constraint (child rows deleted before parent) and closes the GDPR erasure gap.

### EXP-039. PoolScalingController base pool adjustment incorrect with multiple concurrent experiments [Medium]
**Section:** 4.6.2, 10.7
Per-experiment scoping causes last-write-wins; correct adjustment requires cross-experiment aggregation.
**Recommendation:** Clarify that `Σ variant_weights` aggregates across all active experiments on the same pool.
**Status:** Fixed
**Validation:** The original text on line 575 defined `Σ variant_weights` as "the sum of `variant_weight` values across all active variants **for the experiment**" — scoping the sum to a single experiment. With two concurrent experiments on the same base pool (e.g., weights 0.1 and 0.2), separate per-experiment reconcile passes would each overwrite the base pool's `minWarm` using only their own weight, resulting in last-write-wins (final value based on `1 - 0.2` instead of the correct `1 - 0.3`). Fixed by: (1) changing the introductory sentence on line 566 from "When **an** experiment creates one or more variant pools" to "When one or more experiments create variant pools on the same base pool"; (2) rewriting the `Σ variant_weights` definition on line 575 to explicitly scope the summation to "all active variants of **all active experiments** targeting the same base pool" and adding a sentence stating the controller aggregates in a single pass — not per-experiment — to avoid last-write-wins. The status-transition table in Section 10.7 is consistent and required no change: its "with this variant's weight removed from the sum" language correctly describes a delta on the cross-experiment aggregate.

### EXP-040. `session:eval:write` permission not defined in RBAC framework [Low]
**Section:** 10.7, 9.1
Permission string referenced but absent from the permission matrix.
**Recommendation:** Add row to permission matrix.

### EXP-041. No assignment rule for sessions eligible for multiple simultaneous percentage-mode experiments [Medium]
**Section:** 10.7
Session record holds single `experimentContext`; no tie-breaking rule.
**Recommendation:** Define tie-breaking rule (priority, mutual exclusivity, or one-experiment-per-runtime).
**Status:** Fixed
**Resolution:** Validated that multiple active experiments CAN produce simultaneous non-control assignments for the same session (the hash is per-experiment and independent, so a user can land in a variant for both Experiment A and Experiment B). Pool-level scoping does NOT inherently prevent this — two experiments can share the same `baseRuntime` while routing to different variant pools, and the ExperimentRouter must decide which variant pool to use before claiming a pod. Added two spec additions to Section 10.7: (1) a new "Multi-experiment evaluation order" bullet under "Properties of this algorithm" stating experiments are evaluated in ascending `created_at` order and evaluation stops at the first non-control result; (2) a new "Multi-experiment assignment rule (first-match)" paragraph before the isolation monotonicity check, specifying the full first-match semantics, the guidance to use different base runtimes for truly independent concurrent experiments, and an `experiment.multi_eligible_skipped` informational event for observability.

---

## 22. Document Quality (DOC)

### DOC-036. Orphaned footnote ⁴ [Low] — CARRIED FORWARD
**Section:** 8.6
No corresponding ¹ ² ³.
**Recommendation:** Renumber to ¹ or convert to inline note.

### DOC-037. Error codes reference "Section 13 (Eval API)" — Section 13 is Security Model [Medium] — FIXED
**Section:** 15.1
**Recommendation:** Change to "Section 10.7."
**Resolution:** Both references in Section 15.1's error code table (`SESSION_NOT_EVAL_ELIGIBLE` and `EVAL_QUOTA_EXCEEDED`) changed from "See Section 13 (Eval API)" to "See Section 10.7." Section 10.7 (Experiment Primitives) contains the Eval result schema, eval submission contract, and `POST /v1/sessions/{id}/eval` endpoint — the correct target. Section 13 is the Security Model.

### DOC-038. Pagination note references "Section 11.5" — Section 11.5 is Idempotency [Medium] — FIXED
**Section:** 15.1
**Recommendation:** Change to "Section 10.7."
**Resolution:** In Section 15.1's cursor-based pagination note, `(see Section 11.5)` changed to `(see Section 10.7)`. Section 10.7 (Experiment Primitives) is the correct target for `GET /v1/admin/experiments/{name}/results` behavior; Section 11.5 is Idempotency and is unrelated.

### DOC-039. Platform operators entry point references "Section 17" for Admin API [Medium] — FIXED
**Section:** 23.2
Section 17 is Deployment Topology; Admin API is Section 15.1.
**Recommendation:** Change to "Section 15.1."
**Resolution:** In Section 23.2's community adoption table, the Platform operators entry point changed from `Admin API (Section 17)` to `Admin API (Section 15.1)`. Section 15.1 is the REST API section; Section 17 is Deployment Topology.

### DOC-040. "Phase 17 deliverables" — no such phase exists [Low]
**Section:** 23.2
**Recommendation:** Change to "Phase 17a deliverables." *(Duplicate of CPS-039)*

### DOC-041. Stale CRD name `AgentSession` used in two places [Medium] ✓ FIXED
**Section:** 12.8
Should be `SandboxClaim` per the CRD mapping table.
**Recommendation:** Replace both occurrences.
**Resolution:** Replaced stale `AgentSession` with `SandboxClaim` in two locations: (1) the Phase 5 "Clean CRDs" row of the tenant deletion lifecycle table (Section 12.8), and (2) the data residency admission control webhook description also in Section 12.8. The CRD mapping table's "Replaces (old Lenny CRD)" column entry correctly retains `AgentSession` as the historical name and was left unchanged.

### DOC-042. Two billing-correction admin endpoints missing from Section 15.1 [Medium]
**Section:** 11.2.1, 15.1
`reject` and `billing-correction-reasons` endpoints absent.
**Recommendation:** Add both. *(Duplicate of API-058)*
**Status: Fixed (via API-058)** — The `reject` endpoint was added as part of API-058. Note: `billing-correction-reasons` (`POST /v1/admin/billing-correction-reasons`) is referenced in the Section 11.2.1 narrative but is also absent from the Section 15.1 table; that gap is a distinct finding tracked separately and is not addressed here.

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

### MSG-037. `delivery_receipt` `reason` field populated-status list omits `error` [Medium] — SKIPPED
**Section:** 15.4.1
**Status:** Skipped — carried forward from previous iterations, previously skipped.
**Recommendation:** Add `error` to the populated-status list.

### MSG-041. `message_expired` async notification has no defined sender-tracking mechanism [Medium] — SKIPPED
**Section:** 7.2
**Status:** Skipped — not a genuine issue. DLQ entries are full MessageEnvelopes which already contain the `from` field (§15.4.1) with `kind` and `id`, providing the sender identity needed to route the `message_expired` notification to the correct event stream. The finding's premise that "DLQ entries don't persist sender_session_id" is incorrect; the `from` object on each enqueued MessageEnvelope serves exactly this purpose. No spec change needed.
**Recommendation:** Persist `sender_id` in DLQ entries; define routing for external client senders.

### MSG-042. `delivery: "immediate"` exception for `input_required` undocumented in delivery enum table [Medium] — FIXED
**Section:** 15.4.1, 7.2
**Status:** Fixed — partially valid. The `delivery: "immediate"` exception for `input_required` was already fully documented in §15.4.1's delivery field table, so the claim of "undocumented" was overstated. However, §7.2 path 4 (the primary delivery routing reference) did not cross-reference this exception, creating a clarity gap where readers could assume `delivery: "immediate"` overrides path 4 buffering. Added an explicit note to path 4 in §7.2 clarifying that `input_required` buffering applies even with `delivery: "immediate"`, with a cross-reference to §15.4.1. The recommendation to add `reason: "target_input_required"` to the receipt was skipped as a nice-to-have enhancement — the `queued` status is already correct and the receipt schema does not define per-path reasons for queued status.
**Recommendation:** Add explicit note to path 4; add `reason: "target_input_required"` to receipt.

### MSG-043. `inbox_cleared` event emitted on target's stream, not sender's [Medium] — FIXED
**Section:** 7.2
**Status:** Fixed — the finding correctly identified that the spec told senders to "listen for `inbox_cleared`" on a stream they cannot access. However, the recommended fix (emit on sender's stream) is impossible because in-memory inbox loss on coordinator crash also loses sender identities. The actual fix clarifies that `inbox_cleared` is correctly emitted on the target session's own event stream (for the target's client), explicitly states that senders cannot be notified because sender identity is lost with the inbox, and provides three concrete alternatives for reliable delivery: `durableInbox: true`, DLQ path, or application-level ACK with timeout-based re-send.
**Recommendation:** Emit on sender's stream keyed by `targetSessionId`.

### MSG-044. Path 3 + path 4 overlap case unspecified [Medium] — FIXED
**Section:** 7.2
Session simultaneously in `await_children` and child `input_required`; ordering undefined.
**Recommendation:** Clarify ordering between inbox-buffered messages and `await_children` events.
**Resolution:** Added explicit path precedence preamble to the six-path routing list in §7.2 establishing that path 4 (`input_required`) takes precedence over path 3 (`await_children`) when both conditions hold simultaneously during concurrent tool execution. Added exclusion clause to path 3 ("and session is NOT in `input_required` state") and an overlap clarification paragraph to path 4 explaining that `input_required` is the authoritative routing signal (gateway-tracked session state vs. runtime-level blocking condition) and that delivery occurs when `ready_for_input` is reached (all tool calls settled).

### MSG-045. Sibling task-tree snapshot race during rapid child spawning [Low]
**Section:** 7.2
Early siblings permanently miss late-spawned siblings.
**Recommendation:** Document coordinator pattern with concrete protocol for late arrivals.

### MSG-046. SSE buffer overflow behavior contradicts `OutboundChannel` back-pressure policy [Medium] — FIXED
**Section:** 7.2, 15
Two incompatible trigger mechanisms (buffer-fill vs write-block) and conflicting buffer sizes.
**Recommendation:** Unify the mechanism; reconcile `MaxOutboundBufferDepth: 256` with "1000 events."
**Status:** Fixed. Rewrote the §7.2 "SSE buffer policy" paragraph (now "SSE back-pressure policy") to remove the contradictory 1000-event/10MB in-memory buffer model and instead explicitly reference §15's bounded-error `OutboundChannel` policy for connection-coupled adapters (SSE, long-poll). The paragraph now describes the same mechanism as §15: non-blocking write attempt, error within 100ms if blocked, gateway closes channel and drops connection, client reconnects with cursor for EventStore replay. Removed the independent buffer size that conflicted with `MaxOutboundBufferDepth: 256`.

### MSG-047. `message_expired` event schema missing `reason: "session_terminal"` variant [Low]
**Section:** 7.2, 7.3
Only `"target_ttl_exceeded"` documented; `"session_terminal"` appears elsewhere.
**Recommendation:** Enumerate all reason values in the schema.

### MSG-048. `lenny/await_children` deadlock detection excludes `suspended` children [Medium]
**Section:** 8.8, 6.2
A perpetually-suspended child blocks `await_children` without triggering deadlock detection.
**Recommendation:** Extend detection to include `suspended` children; add timeout parameter.
**Status:** Skipped
**Resolution:** Not a genuine problem. `suspended` is a session-level state (Section 6.2), not a task state (Section 8.8 task state machine). The deadlock detector operates on task states. More importantly, `suspended` is the result of a deliberate `interrupt_request` by an external actor (client or parent) who retains full ability to resume the session at any time. Including `suspended` in deadlock detection would incorrectly flag intentional pauses as deadlocks. The client who issued `interrupt_request` is responsible for managing the suspended session's lifecycle. Additionally, `interrupt_request` does not cascade to children (Section 6.2), reinforcing that suspension is an externally-managed action, not an unresolvable blocking condition. If a parent is awaiting a suspended child, the parent blocks as expected until the child is resumed — this is correct behavior, not a deadlock.

### MSG-049. Delegation policy routing interaction with `siblings` messaging scope undefined [Medium]
**Section:** 7.2, 8.3
`SCOPE_DENIED` error conflates messaging scope with delegation policy.
**Recommendation:** Clarify `messagingScope` is the sole policy for message routing; rename error description.
**Status:** Fixed
**Resolution:** The `SCOPE_DENIED` error description in the error code table incorrectly stated "sender's delegation scope does not permit messaging" — conflating `messagingScope` (§7.2, which governs inter-session message routing) with delegation policy (§8.3, which governs runtime/pool targeting). Fixed by replacing "delegation scope" with "effective `messagingScope`" in the error description. The rest of §7.2 and §8.3 correctly distinguish between the two concepts; only the error table entry had the wrong terminology.

### MSG-050. `GET /v1/sessions/{id}/messages` endpoint has no defined response schema [Low]
**Section:** 15.1
No field list, example, or schema reference.
**Recommendation:** Add response schema with pagination envelope and field definitions.

---

## 24. Policy Engine (POL)

### POL-041. Cross-phase priority ordering error [Medium] — SKIPPED (carried forward, previously skipped)
**Section:** 4.8
**Status:** Skipped — This finding was skipped in the previous iteration (20260407224656) and is carried forward as skipped per policy.

### POL-044. `PostRoute` immutable fields omitted from enforcement list [Medium] — FIXED
**Section:** 4.8
Resolved runtime and credential pool are prohibited from modification but not snapshotted.
**Recommendation:** Add `PostRoute` to immutable field enforcement with `resolved_runtime_name` and `credential_pool_id`.
**Status:** Fixed — Added `resolved_runtime_name`, `credential_pool_id` at `PostRoute` to the parenthetical enumeration in the "Immutable field enforcement on MODIFY" paragraph (§4.8), making the enforcement snapshot list consistent with the phase payload table's declared immutability constraints for `PostRoute`.

### POL-045. POL-041 regression — cross-phase priority ordering statement still misleading [Medium] — FIXED
**Section:** 4.8
Statement implies a unified chain across phases, which is architecturally incorrect.
**Recommendation:** Rewrite to distinguish within-phase ordering from across-phase sequencing.
**Status:** Fixed — Three changes in §4.8: (1) Rewrote the "Interceptor chain execution order" paragraph to explicitly state that each phase runs its own interceptor chain independently and that priority ordering applies within a phase. (2) Replaced the misleading cross-phase sentence ("External interceptors registered between 201 and 249 run after QuotaEvaluator but before DelegationPolicyEvaluator") in the "Built-in interceptor field dependencies" paragraph with an explicit explanation that cross-phase priority comparisons do not imply execution ordering, with concrete per-phase examples. (3) Fixed the "Short-circuit interaction with MODIFY" paragraph to scope the priority-between-built-ins example to a specific phase rather than implying a cross-phase chain.

### POL-046. Storage quota counter missing from Redis failure behavior table [Medium]
**Section:** 11.2, 12.4
*(Duplicate of STR-043)*
**Status:** Already Fixed — duplicate of STR-043. See STR-043 for resolution details.

### POL-047. `defaultDelegationFraction = 1.0` allows child to consume entire parent budget [Medium]
**Section:** 8.3
Upper bound of 1.0 lets a single child hollow out the tree in one call.
**Recommendation:** Add guidance warning against values above 0.5; consider reducing max to 0.9.
**Status:** Skipped — finding is based on a misreading of the spec. The spec already sets the default to 50% (0.5), not 1.0. The configurable range upper bound of 1.0 is a deliberate deployer knob for legitimate use cases (e.g., a single sequential child needing the full remaining budget). Budget return on completion (§8.3 step 3) means unused budget is credited back to the parent, so allowing 1.0 does not permanently "hollow out" the tree. A deployer who explicitly configures 1.0 is making an intentional operational choice. No spec change needed.

### POL-048. `contentPolicy.interceptorRef` `failPolicy` change silently weakens active leases [Medium] — **Fixed**
**Section:** 8.3
No audit event, warning, or alert when `failPolicy` is weakened.
**Recommendation:** Emit audit events on failPolicy changes; query affected active policies.
**Resolution:** Added `interceptor.fail_policy_weakened` and `interceptor.fail_policy_strengthened` audit events. §4.8 now defines the events with full field specs (interceptor_ref, old/new failPolicy, affected policy count/names). §8.3 updated to reference the new audit event instead of deferring to "deployer operational concern." Event types and event-specific fields added to §11.2.1 audit event catalog. Pattern follows the existing `pool.isolation_warning` precedent.

### POL-049. `DelegationPolicy` deletion while leases are active — behavior undefined [Medium]
**Section:** 8.3
No error code, rejection, or fallback when a referenced policy is deleted.
**Recommendation:** Define fail-closed behavior (`POLICY_REFERENCE_INVALID`) or prevent deletion with `POLICY_IN_USE`.
**Resolution:** Fixed. Added deletion guard to §8.3: `DELETE /v1/admin/delegation-policies/{name}` is rejected with `RESOURCE_HAS_DEPENDENTS` (HTTP 409) if any runtime, derived runtime, or active delegation lease references the policy. Uses the existing `RESOURCE_HAS_DEPENDENTS` error code and `details.dependents` schema. Updated the admin API table (§15.1) and the deletion semantics rules (§15.1, "Deletion semantics for resources with dependents") to include active delegation leases alongside the pre-existing runtime reference check. Deployers must wait for referencing sessions to terminate or update runtime references before deleting a policy.

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

### EXM-040. `terminate(task_complete)` lifecycle protocol contradiction [High] — **Fixed**
**Section:** 4.7, 5.2, 15.4.1
`terminate` means "exit" but task mode requires the runtime to stay alive. No acknowledgment message defined.
**Recommendation:** Introduce `task_complete` (Runtime→Adapter) and `task_ready` (Adapter→Runtime) message pair.
**Resolution:** Removed `"task_complete"` from `terminate` reason enum (§4.7). Introduced three new lifecycle channel messages: `task_complete` (Adapter→Runtime, signals end of current task), `task_complete_acknowledged` (Runtime→Adapter, confirms resource release with 30s timeout), and `task_ready` (Adapter→Runtime, signals scrub complete and new workspace ready). Added `"task_lifecycle"` to the `lifecycle_capabilities` negotiation. Updated all references in §5.2 (task mode lifecycle, integration tiers), §6.2 (state transitions), and §15.4.1 (between-task signaling description) and §15.4.3 (tier comparison matrix) to use the new message names. `terminate` now exclusively means "exit the process" with no ambiguity.

### EXM-041. `microvmScrubMode` and `acknowledgeMicrovmResidualState` absent from `taskPolicy` YAML [Medium] — **Fixed**
**Section:** 5.2
Described in prose but absent from all schema examples.
**Recommendation:** Add as commented fields in the primary `taskPolicy` YAML block.
**Resolution:** Added `microvmScrubMode` and `acknowledgeMicrovmResidualState` as commented fields in both `taskPolicy` YAML blocks in §5.2 (the primary schema example at the start of the task-mode section and the deployer-acknowledgment block). Fields are commented out with descriptive inline comments explaining their purpose, valid values, defaults, and when they are required, consistent with the prose description in the Kata/microvm scrub variant paragraph.

### EXM-042. Concurrent-workspace pod state machine absent from Section 6.2 [Medium] — **Fixed**
**Section:** 6.2, 5.2
Fundamentally different lifecycle (partial slot occupancy) with no defined state transitions.
**Recommendation:** Add concurrent-workspace pod state machine to Section 6.2.
**Resolution:** Added a two-level concurrent-workspace state machine to the §6.2 state machine code block. Pod-level transitions cover `idle → slot_active → idle/draining → terminated` with triggers for slot assignment, slot completion, unhealthy threshold (`ceil(maxConcurrent/2)` failures in 5 min), and uptime limit. Per-slot sub-states cover the individual slot lifecycle: `slot_assigned → receiving_uploads → running → slot_cleanup → released/leaked`, with a `failed` terminal for non-retryable errors. Added explanatory prose covering pod failure during active slots (all slots fail, per-slot retry per §5.2), partial occupancy behavior (concurrent assignment gated by atomic Redis INCR), and draining semantics (no new slots, existing slots run to completion, `terminationGracePeriodSeconds` validation). Pod-level `slot_active` maps to existing `lenny.dev/state: active` label; `idle` maps to `lenny.dev/state: idle`, consistent with the state storage model.

### EXM-043. Concurrent-workspace mode has no tenant isolation statement [Medium] — **Fixed**
**Section:** 5.2, 13.1
Task mode has two-layer tenant pinning; concurrent mode has nothing.
**Recommendation:** Add "Tenant model for concurrent-workspace mode" paragraph.
**Resolution:** Added a "Tenant pinning (concurrent-workspace)" paragraph to §5.2, immediately after the deployer acknowledgment block for concurrent-workspace mode. The paragraph establishes that concurrent-workspace pods are pinned to a single tenant for their entire lifetime, with enforcement reusing the same two-layer mechanism as task mode: (1) gateway-level `tenantId` match on slot assignment, and (2) the `lenny-tenant-label-immutability` ValidatingAdmissionWebhook. The paragraph explains the stronger rationale (simultaneous process-level cotenancy is strictly worse than task mode's sequential reuse) and explicitly prohibits cross-tenant slot sharing with no `allowCrossTenantReuse` equivalent (since there is no isolation boundary like task mode's microvm option).

### EXM-044. `maxConcurrent` defined at two schema levels for workspace mode [Low]
**Section:** 5.2
Top-level and inside `concurrentWorkspacePolicy` — no canonical source designated.
**Recommendation:** Designate a single canonical location.

### EXM-045. `preConnect` (SDK-warm) compatibility with task and concurrent modes unspecified [Medium]
**Section:** 5.1, 5.2, 6.1
SDK-warm assumptions are incompatible with between-task lifecycle; no compatibility matrix.
**Recommendation:** Add explicit compatibility table for `preConnect` vs `executionMode`.
**Status:** Fixed
**Resolution:** Added a "`preConnect` compatibility with execution modes" subsection at the end of Section 6.1 with a four-row compatibility table covering session (supported, primary target), task (supported, with per-task `sdkWarmBlockingPaths` evaluation and SDK re-warm after scrub), concurrent-workspace (not supported, pool controller rejects at validation), and concurrent-stateless (not supported, pool controller rejects at validation). Task mode specifies that the adapter re-establishes SDK-warm state after each scrub/`task_ready` cycle, and that demotion decisions are per-task. Both concurrent modes specify explicit pool controller validation errors.

### EXM-046. `concurrencyStyle: stateless` warm pool integration model unspecified [Low]
**Section:** 5.2, 4.6.1, 4.6.2
Unclear whether stateless pods use the `SandboxClaim` model or bypass it entirely.
**Recommendation:** Clarify pod lifecycle model for stateless mode.

---

## Cross-Cutting Themes

1. **Phantom references and missing canonical entries**: Metrics, alerts, API endpoints, and CLI commands referenced in narrative text but absent from their canonical tables (OBS-038/039, API-057/058/059, OPS-049, DOC-042/043, FLR-043). This is the most pervasive class of finding — 14 prior iterations have not fully eradicated it because fixes to one section create new references that aren't propagated to canonical tables.

2. **Undefined failure modes for secondary infrastructure**: KMS signing (FLR-042), storage quota during Redis outage (STR-043/FLR-040), GC leader loss (STR-049/FLR-046), Postgres failover during checkpoint metadata write (FLR-044), and dual-store timer reset (FLR-047) all represent failure scenarios where secondary infrastructure interactions have no defined behavior.

3. **Execution mode lifecycle gaps**: Task mode's `terminate(task_complete)` contradiction (EXM-040, **fixed** — replaced with dedicated `task_complete`/`task_complete_acknowledged`/`task_ready` message pair), concurrent-workspace's missing state machine (EXM-042, **fixed** — two-level state machine added to §6.2) and tenant isolation (EXM-043, **fixed** — tenant pinning paragraph added to §5.2), and SDK-warm compatibility (EXM-045) form a cluster of underspecified non-session execution mode behaviors.

4. **Cross-reference errors**: DOC-037/038/039/040/041 identify five incorrect section cross-references — a persistent document quality issue.

5. **Storage accounting gaps**: Eviction context objects (STR-044/050), partial checkpoint manifests (STR-045), and GC double-decrement risk (STR-049) create potential storage leaks or quota inaccuracies.

6. **Build sequence phasing**: LLM Proxy has no phase (BLD-036), client SDKs have no phase (BLD-043), audit logging arrives too late (BLD-039), and `deny-all` default blocks users for 10 phases (BLD-041).

7. **Delegation tree durability**: Extension-denied flag (DEL-041), `maxTreeMemoryBytes` counter (DEL-043), and cross-environment semantics (DEL-046/047) have gaps in crash-recovery and mid-tree policy change handling.
