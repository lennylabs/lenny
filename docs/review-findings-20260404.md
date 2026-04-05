# Technical Design Review Findings — 2026-04-04

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md` (25 perspectives)
**Total findings:** 388 across 25 review perspectives

## Findings Summary

| Severity | Count |
|----------|-------|
| Critical | 10 |
| High | 68 |
| Medium | 178 |
| Low | 102 |
| Info | 30 |

### Critical Findings by Perspective

| # | Perspective | Finding | Section |
|---|-----------|---------|---------|
| KIN-001 | 1. K8s Infrastructure | `kubernetes-sigs/agent-sandbox` dependency — 5-month-old project with no stability guarantees as load-bearing CRD foundation | 4.6.1 |
| SEC-001 | 2. Security | SIGSTOP/SIGCONT checkpoint mechanism unvalidated under gVisor and Kata | 4.4 |
| SCA-001 | 4. Scalability | No concrete performance targets or capacity planning numbers at any scale tier | 16.5 | FIXED |
| SCA-002 | 4. Scalability | Gateway LLM reverse proxy as single throughput bottleneck for all LLM traffic | 4.9 |
| DEV-001 | 6. Developer Experience | Insufficient standalone specification to build a Minimum-tier runtime | 15.4 | FIXED |
| TEN-001 | 8. Multi-Tenancy | Postgres RLS `SET app.current_tenant` race condition under PgBouncer transaction mode | 4.2, 12.3 |
| COM-001 | 13. Compliance | GDPR erasure flow incomplete — billing event immutability conflicts with deletion obligation | 12.8 | FIXED |
| OSS-001 | 15. Competitive | No differentiation narrative — "Why Lenny?" never articulated | 23 | FIXED |
| BLD-001 | 19. Build Sequence | Credential leasing (Phase 11) arrives too late for realistic integration testing | 18 | FIXED |
| BLD-002 | 19. Build Sequence | Security hardening (Phase 14) dangerously late — real credentials exposed without network isolation | 18 | FIXED |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes Infrastructure & Controller Design

### KIN-001. `kubernetes-sigs/agent-sandbox` Dependency Maturity and Lock-in Risk [Critical] — FIXED
**Section:** 4.6.1

The entire pod lifecycle depends on a ~5-month-old upstream project (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim` CRDs). The project is almost certainly at `v1alpha1` stability with no breaking-change guarantees. The "pre-commit requirement" to verify optimistic-locking guarantees is noted but unresolved, with no fallback plan.

**Recommendation:** Define an internal `PodLifecycleManager` interface abstracting over agent-sandbox CRDs. Resolve the pre-commit requirement as an ADR before further design work. Pin to a specific release with a one-release-delay upgrade cadence. Document a fallback plan with estimated effort for a custom kubebuilder replacement.

**Status:** FIXED — Added two-interface abstraction layer (`PodLifecycleManager` + `PoolManager` with shared `PoolReader`) so no Lenny component touches agent-sandbox CRDs directly. Converted pre-commit requirement to ADR. Added dependency pinning policy with one-release-delay upgrade cadence. Documented fallback plan with 2-3 engineering-week effort estimate for custom kubebuilder replacement.

### KIN-002. Etcd Pressure Mitigations Insufficient at Scale [High] — FIXED
**Section:** 4.6.1

At 1000 concurrent sessions with ~2-minute lifetimes, CRD status updates generate ~80+ writes/second — exceeding the 10 QPS rate limiter, causing unbounded queue growth. No etcd compaction/defrag guidance. The single rate limiter bucket starves pod creation during scale-up.

**Recommendation:** Use separate rate limiters for pod creation and status updates. Consider moving the authoritative state machine to Postgres for >500 sessions, using CRD status only for K8s-native concerns. Add etcd tuning guidance (compaction interval, defrag schedule, quota monitoring).

**Status:** FIXED — Replaced single rate limiter with two dedicated buckets (pod creation: 20 QPS/burst 50; status updates: 30 QPS/burst 100) with configurable controller flags. Added comprehensive etcd operational tuning guidance (compaction, defragmentation, quota monitoring, snapshot frequency). Updated Section 17.8 controller tuning table with per-tier rate limiter and etcd settings. Moving the authoritative state machine to Postgres was not applied — it is a significant architectural change beyond the scope of this fix.

### KIN-003. Controller Split Has Unclear CRD Ownership Boundary [High]
**Section:** 4.6.1, 4.6.2

Both WarmPoolController and PoolScalingController interact with the same CRD types. Leader election parameters differ or are unspecified. Manual `kubectl edit` of CRDs would be silently overwritten by PoolScalingController.

**Recommendation:** Document explicit CRD field ownership per controller. Use RBAC to enforce write boundaries. Define separate leader election leases. Add a validating webhook to prevent manual CRD edits that conflict with Postgres-authoritative state.

**Status:** FIXED — Added Section 4.6.3 with CRD field ownership table, per-controller RBAC boundaries, and a validating admission webhook for Postgres-authoritative state protection. Added explicit separate leader election lease names (`lenny-warm-pool-controller`, `lenny-pool-scaling-controller`) in Sections 4.6.1 and 4.6.2.

### KIN-004. PodSecurityStandards Warn+Audit Without Enforce [High]
**Section:** 17.2

OPA/Gatekeeper policies stated as "must" but not reflected in Helm chart or build sequence. `shareProcessNamespace: false` requires separate policy not in build sequence. Policy/spec drift can cause warm pool deadlock.

**Recommendation:** Add an explicit build phase for admission policy deployment. Include manifests in Helm chart. Add integration tests verifying controller-generated pod specs pass admission policies. Use `enforce` for runc pods where seccomp concerns don't apply.

**Status:** FIXED — Section 17.2 now specifies full Restricted PSS enforcement for runc pods via RuntimeClass-aware admission policies (OPA/Gatekeeper or Kyverno), with relaxed RuntimeClass-specific constraints for gVisor/Kata. Admission policy manifests added to Helm chart component list (Section 17.6). Admission policy deployment and integration tests added to Phase 3.5 build sequence (Section 18). `shareProcessNamespace` validation policy explicitly included in the admission policy set.

### KIN-005. Namespace Layout Lacks Tenant-Level Isolation [Medium]
**Section:** 17.2

All tenants share `lenny-agents`. NetworkPolicy is not tenant-aware. Resource quotas cannot be enforced per-tenant at namespace level.

**Recommendation:** Recommend per-tenant namespaces for multi-tenant production. If deferred, require `microvm` isolation for multi-tenant workloads and document the shared-namespace risk.

### KIN-006. PDB Label Inconsistency [Medium]
**Section:** 4.6.1, 6.2

PDB references `lenny.dev/pod-state: idle` but actual coarse labels use `lenny.dev/state: idle`. Node drain behavior with both idle and active pods undefined.

**Recommendation:** Fix label naming. Document drain behavior. Consider `maxUnavailable` instead of `minAvailable`.

### KIN-007. SandboxClaim Orphan Risk Without ownerReference [Medium]
**Section:** 4.6.1

No ownerReference means no automatic GC. Gateway crash after SandboxClaim creation but before session setup leaves orphans.

**Recommendation:** Add reconciliation loop for orphaned SandboxClaims. Add TTL/lease mechanism.

### KIN-008. Topology Spread Defaults Too Permissive [Medium]
**Section:** 5.2

`ScheduleAnyway` default allows single-zone concentration. Zone failure could eliminate the warm pool.

**Recommendation:** Change zone-level default to `DoNotSchedule` for pools with `minWarm >= 3`.

### KIN-009. cert-manager as SPOF for Pod Identity [Medium]
**Section:** 10.3

Unavailability cascades: no new pods can join warm pool, expiring-cert pods are drained without replacement. No circuit breaker on cert-driven draining.

**Recommendation:** Add circuit breaker — pause cert-expiry draining during cert-manager outage. Extend cert TTL from 4h to 8h. Add `CertManagerUnavailable` critical alert.

### KIN-010. No ResourceQuota/LimitRange for Agent Namespaces [Medium]
**Section:** 17.2

Bug or runaway scaling can create unbounded pods without namespace-level guardrails.

**Recommendation:** Add ResourceQuota and LimitRange per agent namespace, configurable via Helm.

### KIN-011. Finalizer Stuck Alert Has No Automated Remediation [Low]
**Section:** 4.6.1

Manual finalizer removal is error-prone under pressure. Node failure causes burst of alerts needing manual intervention.

**Recommendation:** Auto-remove finalizer after 5-minute threshold when checkpoint is confirmed. Reserve manual path for unknown checkpoint status.

### KIN-012. Controller Work Queue No Resync Period [Low]
**Section:** 4.6.1

Dropped reconciliation events with default 10h resync means stale state for hours.

**Recommendation:** Configure 30-60s resync period. Document overflow behavior.

### KIN-013. No CRD Storage Overhead Estimation [Info]
**Section:** 4.6.1

At 1000+ concurrent sessions, CR storage in etcd could become significant.

**Recommendation:** Estimate per-CR storage size and document expected etcd requirements.

### KIN-014. HPA Custom Metrics Pipeline Complexity [Info]
**Section:** 10.1

Three-hop pipeline (gateway->Prometheus->Adapter->HPA) introduces latency and failure points.

**Recommendation:** Default to KEDA if present. Document failure modes of the metrics pipeline.

---

## 2. Security & Threat Modeling

### SEC-001. SIGSTOP/SIGCONT Checkpoint Under gVisor/Kata Unvalidated [Critical] — FIXED
**Section:** 4.4

With `shareProcessNamespace: false`, the adapter cannot directly signal the agent process. gVisor and Kata have different signal delivery semantics. Inconsistent checkpoints could corrupt session state on resume.

**Recommendation:** Validate in PoC before committing. Clarify cooperative checkpoint via lifecycle channel is the only reliable path under sandboxed/microvm. Document that Minimum-tier runtimes cannot produce consistent checkpoints.

**Resolution:** Checkpoint quiescence restructured as tier-dependent in Section 4.4. Cooperative lifecycle channel (`checkpoint_request`/`checkpoint_ready`/`checkpoint_complete`) is the primary path for consistent checkpoints (Full-tier). Minimum/Standard-tier runtimes produce best-effort checkpoints only. `SIGSTOP`/`SIGCONT` restricted to embedded adapter mode under runc only. Signal-based checkpointing explicitly unsupported under gVisor/Kata.

### SEC-002. Adapter-Agent Shared emptyDir Volume Escape [High] — FIXED
**Section:** 4.7

Malicious agent can write arbitrary files to shared volume. Adapter manifest on shared path creates tamper/race risks.

**Recommendation:** Separate volumes for socket, workspace, and manifest. Mount manifest read-only into agent container. Consider abstract Unix sockets.

**Resolution:** Replaced single shared `emptyDir` with abstract Unix sockets (`\0` namespace) for adapter-agent communication — no filesystem path needed. Manifest volume (`/run/lenny/`) is now a dedicated `emptyDir` mounted read-only into the agent container and read-write into the adapter container. Workspace remains a separate `emptyDir` (`/workspace/`). Updated manifest JSON to use abstract socket names (`@lenny-platform-mcp`, `@lenny-connector-github`). Added `SO_PEERCRED` peer UID verification on abstract sockets. No shared writable volume between adapter and agent.

### SEC-003. Prompt Injection via Delegation Chains [High] -- FIXED
**Section:** 8

Gateway validates structure but not content. Compromised parent can craft malicious `TaskSpec.input`. `GuardrailsInterceptor` disabled by default.

**Recommendation:** Add `contentPolicy` to `DelegationPolicy`. Restrict `lenny/send_message` to direct children/parent. Rate-limit message injection. Warn deployers about prompt injection risk without guardrails.

**Resolution:** Added `contentPolicy` (with `maxInputSize` default 128KB and `interceptorRef`) to `DelegationPolicy` (Section 8.3). Added `PreDelegation` phase and full gRPC protobuf interface for external `RequestInterceptor` (Section 4.8). Added Section 13.5 (Delegation Chain Content Security) documenting the layered mitigation model. Strengthened Section 22.3 warning about residual risk without content scanning. Messaging scope and rate limits were already in place.

### SEC-004. Credential Material via Environment Variables [High] — FIXED
**Section:** 4.7

Env vars are weak for credential delivery — readable via `/proc`, persist in crash dumps, often logged by frameworks.

**Recommendation:** Deliver via tmpfs file (mode 0400) instead. Recommend proxy mode as default for multi-tenant. Clear credential env var after agent reads it.

**Status:** FIXED — Replaced environment variable credential delivery with tmpfs-backed file (`/run/lenny/credentials.json`, mode `0400`, agent UID-owned). Proxy mode now recommended as default for multi-tenant deployments. Security boundaries in Section 4.9 updated to reflect tmpfs file delivery.

### SEC-005. Isolation Monotonicity Gap for Task Mode Pod Reuse [High]
**Section:** 5.2, 8.3

Task-mode pod reuse between different tenants has no isolation boundary. Residual data in memory, caches, `/dev/shm` can leak.

**Recommendation:** Enforce tenant pinning — task-mode pods never reuse across tenants. Require `microvm` for any cross-tenant reuse consideration.

**Status:** FIXED — Added tenant-pinning rule to Section 5.2: task-mode pods record `tenantId` on first assignment, gateway enforces match on subsequent assignments, cross-tenant reuse only permitted with `microvm` isolation. Also addresses TEN-005.

### SEC-006. DNS Exfiltration via DoH/DoT Bypass [Medium]
**Section:** 13.2

Pods with `internet` egress can bypass dedicated CoreDNS via DNS-over-HTTPS on port 443.

**Recommendation:** Document as known residual risk for `internet` profile. Add subdomain query length limits. Consider egress proxy for `internet` pods.

### SEC-007. Rate Limit Fail-Open Exploitable in Multi-Tenant [Medium]
**Section:** 12.4

10 replicas = 10x effective rate limit during Redis outage. Attacker could intentionally trigger Redis exhaustion.

**Recommendation:** Reduce default fail-open to 15s. Apply per-replica limit as `user_limit / N`. Add automatic session creation blocking option for security-critical deployments.

### SEC-008. Semantic Cache Poisoning Across Tenants [Medium]
**Section:** 4.9

Shared credential pool with semantic caching can leak responses between tenants.

**Recommendation:** Mandate `tenant_id` as cache key prefix. Enforce in `CachePolicy` schema.

### SEC-009. Elicitation Chain Phishing via Deep Delegation [Medium]
**Section:** 9.2

OAuth flows exempt from depth suppression. Compromised connector OAuth endpoint creates phishing risk.

**Recommendation:** Add `maxConnectorElicitationDepth`. Validate `redirect_uri` against registered callback URL.

### SEC-010. RLS Bypass During Schema Migrations [Medium]
**Section:** 4.2, 10.5

Migration role may bypass RLS during DML operations, corrupting tenant isolation.

**Recommendation:** Migration role should have DDL-only privileges. DML in migrations must set `app.current_tenant`. Add CI check for DML in tenant-scoped tables.

### SEC-011. Task Mode Workspace Scrub Undefined [Medium]
**Section:** 5.2

"Best-effort scrub" never defined. No specification of what is cleaned or failure behavior.

**Recommendation:** Define explicit scrub procedure. Include runtime restart between tasks. Document data residual risks.

### SEC-012. `runtimeOptions` Passthrough Without Validation [Medium]
**Section:** 14

64KB of attacker-controlled JSON with no schema requirement in production. Deserialization attack surface.

**Recommendation:** Require `runtimeOptionsSchema` for multi-tenant. Apply structural validation (depth, string length, array size).

### SEC-013. `callbackSecret` Lifetime Uncontrolled [Medium]
**Section:** 14

No max lifetime, no rotation mechanism, no minimum entropy. Encrypted storage compromise exposes all secrets.

**Recommendation:** Generate server-side. Set max lifetime tied to session TTL. Delete after webhook delivery retries exhausted.

### SEC-014. LLM Proxy Uses HTTP Not HTTPS [Medium]
**Section:** 4.9

Lease token transmitted in cleartext over pod-to-gateway network. No mutual authentication.

**Recommendation:** Require mTLS on proxy endpoint. Bind lease token to pod SPIFFE identity. Fix example URL scheme.

### SEC-015. Resource Exhaustion via Delegation Trees [Medium]
**Section:** 8.3, 11.1

Unclear if child sessions count toward parent user's concurrency limit. Single user could consume hundreds of pods.

**Recommendation:** Delegated children must count toward originating user's concurrency. Add `maxTotalPodsPerUser` and `maxTotalPodsPerTenant` quotas.

### SEC-016. Audit Log Startup Check Too Weak [Low]
**Section:** 11.7

Only warns on UPDATE/DELETE grants, doesn't refuse to start. Grants added mid-session undetected until restart.

**Recommendation:** Make startup check fail-hard in production. Run grants check periodically (every 5 min). Alert on drift.

### SEC-017. `publishedMetadata` XSS Vector [Low]
**Section:** 5.1

Public metadata with HTML/JS content served without sanitization could enable stored XSS.

**Recommendation:** Serve with `application/octet-stream`, `nosniff`, and `Content-Disposition: attachment`.

### SEC-018. SSE Connection Exhaustion [Low]
**Section:** 7.2

No per-user concurrent stream limit. Malicious client can exhaust file descriptors.

**Recommendation:** Add per-user max concurrent streams (e.g., 5). Add minimum read rate requirement.

### SEC-019. Concurrent Execution Cross-Slot Isolation [Info]
**Section:** 5.2

Process-level isolation between slots explicitly weak. Correctly flagged, needs operational guidance.

**Recommendation:** Enforce same-user constraint on slot assignment. Document per-slot resource contention.

---

## 3. Network Security & Isolation

### NET-001. Missing NetworkPolicies for `lenny-agents-kata` Namespace [High] — FIXED
**Section:** 13.2, 17.2

All three NetworkPolicy manifests apply only to `lenny-agents`. `lenny-agents-kata` defaults to allow-all.

**Recommendation:** Templatize NetworkPolicies in Helm chart to apply to all agent namespaces.

**Status:** FIXED — Section 13.2 NetworkPolicy manifests now specify they apply to all agent namespaces (`lenny-agents`, `lenny-agents-kata`, and any future additions) via Helm chart templatization over `.Values.agentNamespaces`. YAML examples annotated with `# repeated per agent namespace via Helm range`.

### NET-002. Lateral Movement via `internet` Egress Profile [High] — FIXED
**Section:** 13.2

`0.0.0.0/0` includes pod CIDR. Agent pods with `internet` profile can reach other agent pods.

**Recommendation:** Exclude cluster pod/service CIDRs from `internet` profile. Require gVisor/Kata isolation for `internet` profile.

**Status:** FIXED — The `internet` egress profile table entry now specifies cluster pod/service CIDR exclusions. Added hardening note documenting `except` clauses via Helm values (`egressCIDRs.excludeClusterPodCIDR`, `egressCIDRs.excludeClusterServiceCIDR`) and the requirement that `internet` profile pools must use `sandboxed` or `microvm` isolation (controller rejects `standard` + `internet` combinations at validation time).

### NET-003. Direct Credential Mode Bypasses Gateway Data Plane [High] — FIXED
**Section:** 4.9

`direct` mode with `provider-direct` egress creates a network path bypassing the gateway, violating gateway-centric principle.

**Recommendation:** Document `proxy` as recommended default. Add monitoring channel for direct-mode usage. Flag `direct` + `runc` as dangerous combination.

**Status:** FIXED — Proxy mode was already documented as recommended default for multi-tenant deployments (SEC-004). Added warning about `direct` + `standard` (runc) isolation as a dangerous combination, with controller warning event. Added monitoring guidance: `lenny_gateway_credential_leases_active{delivery_mode="direct"}` metric, `deliveryMode` field on `credential.leased` audit event, and recommendation for admin approval in regulated environments.

### NET-004. Gateway Egress Rule Lacks Port Restrictions [Medium]
**Section:** 13.2

Agent pods can reach gateway on any port, including admin and metrics endpoints.

**Recommendation:** Add explicit `ports` constraints limiting to gRPC control port and LLM proxy port.

### NET-005. DNS Exfiltration Mitigation Gaps [Medium]
**Section:** 13.2

CNAME chaining, query length limits, domain allowlists, and HA for dedicated CoreDNS not addressed.

**Recommendation:** Add response-size limits across all record types. Add query-name-length limits. Document HA requirements. Support DNS domain allowlist for high-security.

### NET-006. Certificate Revocation Propagation Delay [Medium]
**Section:** 10.3

Redis pub/sub is not guaranteed delivery. Postgres LISTEN/NOTIFY also lossy. No deny-list persistence across restarts.

**Recommendation:** Populate deny list from Postgres on startup. Use Redis Streams instead of pub/sub. Add revocation propagation latency metric.

### NET-007. Token Service Ingress Not NetworkPolicy-Restricted [Medium]
**Section:** 4.3

No NetworkPolicy restricts which pods can reach the Token Service. Agent pods with `internet` profile could attempt connection.

**Recommendation:** Add NetworkPolicy restricting Token Service ingress to gateway pods only. Validate SAN/SPIFFE URI of incoming connections.

### NET-008. Callback Worker SSRF NetworkPolicy Architecturally Impossible [Medium]
**Section:** 14

Callback HTTP requests are from gateway goroutines, not separate pods. Cannot apply different NetworkPolicies to goroutines.

**Recommendation:** Extract callback worker to separate Deployment with restrictive NetworkPolicy, or rely on application-level SSRF controls and document as limitation.

### NET-009. `type: mcp` Proxy Lacks Inline Policy Enforcement [Medium]
**Section:** 15

Unclear which policy controls apply to proxied MCP tool calls.

**Recommendation:** Document that Environment `mcpRuntimeFilters` apply to every proxied tool call, not just at discovery.

### NET-010. Setup Command Network Blocking Only True for `restricted` Profile [Medium]
**Section:** 7.5, 13.2

"Network blocked during setup" is only true for `restricted` egress profile. Other profiles have full access during setup.

**Recommendation:** Clarify the limitation. Consider two-phase label approach for per-phase NetworkPolicies.

### NET-011. No Egress Restriction on `lenny-system` Namespace [Medium]
**Section:** 13.2

Gateway, Token Service, and controllers can make arbitrary outbound connections.

**Recommendation:** Apply least-privilege egress per component (Token Service: only Postgres/KMS/Redis; Controller: only API server/Postgres).

### NET-012. No Internal Segmentation Within `lenny-system` [Low]
**Section:** 17.2

Compromised gateway has unrestricted access to all `lenny-system` components.

**Recommendation:** Apply defense-in-depth NetworkPolicies: only PgBouncer reaches Postgres, only gateway reaches PgBouncer, etc.

### NET-013. Pod Does Not Verify Gateway Identity [Low]
**Section:** 10.3

Pod-initiated gRPC connection but no explicit server certificate verification.

**Recommendation:** Specify that adapter validates gateway mTLS certificate SAN.

### NET-014. Deny-List Mechanisms Share Redis Pub/Sub Failure Mode [Info]
**Section:** 10.3, 11.4

Certificate and credential deny lists both depend on Redis pub/sub.

**Recommendation:** Consolidate into single reliable delivery channel. Document shared failure mode.

---

## 4. Scalability & Performance Engineering

### SCA-001. No Concrete Performance Targets [Critical] — FIXED
**Section:** 16.5

SLO targets stated without specifying at what scale. No max concurrent sessions, sessions/second, delegation throughput, tenant count, or gateway RPS targets.

**Recommendation:** Define capacity tiers (100/1000/10000 concurrent sessions) with infrastructure sizing. Run capacity modeling. This becomes the basis for all performance decisions.

**Status:** FIXED — Added capacity tier definitions (Tier 1/2/3) in Section 16.5, centralized per-tier infrastructure sizing reference in Section 17.8, and cross-references in 7 high-priority sections (controller tuning, etcd pressure, warm pool sizing, PgBouncer, gateway HPA, Redis topology, operational defaults).

### SCA-002. Gateway LLM Reverse Proxy Bottleneck [Critical] — FIXED
**Section:** 4.9

500 concurrent sessions in proxy mode = 1000+ LLM proxy requests/second through gateway, with streaming responses. No subsystem isolation for proxy path.

**Recommendation:** Design LLM proxy as independently scalable component from day one. Add dedicated HPA metrics, goroutine pool, and circuit breaker. Consider sidecar proxy as alternative.

**Status:** FIXED — LLM Proxy added as 4th gateway subsystem (Section 4.1) with dedicated goroutine pool, circuit breaker, per-subsystem metrics (`lenny_gateway_llm_proxy_active_connections`, `lenny_gateway_llm_proxy_request_duration_seconds`, `lenny_gateway_llm_proxy_circuit_state`), HPA metric (`active LLM proxy connections`), and extraction trigger for independent scaling. Subsystem isolation note added in Section 4.9.

### SCA-003. Startup Latency Based on Estimates, Not Benchmarks [High] — FIXED
**Section:** 6.3

Competitors cite 150-300ms. Lenny's estimates are in seconds with unknown hot-path additions.

**Recommendation:** Establish benchmarks per phase. Build benchmark harness in Phase 2. Measure SDK-warm vs pod-warm to validate complexity tradeoff.

**Status:** FIXED — Section 6.3 latency numbers explicitly marked as targets pending benchmark validation. Added per-phase histogram instrumentation requirement (`lenny_session_startup_phase_duration_seconds`). Added startup benchmark harness to Phase 2 build sequence (pod-warm vs SDK-warm comparison per runtime class, CI-integrated). Added startup latency SLO targets to Section 16.5 (P95 < 2s runc, P95 < 5s gVisor, excluding file upload).

### SCA-004. Redis as Scalability Ceiling [High] — FIXED
**Section:** 12.4

Per-session leases, per-request quota increments, and pub/sub on single Sentinel topology. No analysis of when Sentinel becomes insufficient.

**Recommendation:** Separate Redis into ephemeral coordination and quota enforcement. Consider in-memory budgets with Postgres reconciliation for high-value limits.

**Status:** FIXED — Section 12.4 now documents the scalability ceiling of single Sentinel topology with specific monitoring signals (CPU saturation, memory pressure, operation latency, pub/sub fan-out, ops rate). Added logical separation of Redis concerns into three instance groups (coordination, quota/rate-limiting, cache/pub-sub) as a deployment-time configuration change when ceiling signals trigger. Added in-memory quota budgets with Postgres reconciliation as an opt-in mode for high-value limits. Section 17.8 Redis table updated with per-tier concern separation guidance and capacity ceiling monitoring recommendations.

### SCA-005. HPA Scaling Lag for Gateway [High] — FIXED
**Section:** 10.1

30-60s minimum lag between spike and new replica. Bursty workloads exhaust existing replicas.

**Recommendation:** Aggressive scale-up policies. Leading metrics (queue depth, rejection rate). Higher `minReplicas` for burst absorption.

**Status:** FIXED — Section 10.1 now includes an HPA scale-up policy paragraph with aggressive scale-up behavior (stabilization window 0s, 100%/15s + 4 pods/15s via selectPolicy Max), leading-indicator metrics (queue depth and rejection rate) surfaced through Prometheus Adapter/KEDA, and minReplicas burst-absorption guidance. Section 17.8 Gateway tier table updated with per-tier queue depth targets, scale-up stabilization windows, and scale-up max policies.

### SCA-006. Checkpoint SIGSTOP Duration Impact [High]
**Section:** 4.4

500MB workspace tar+upload during SIGSTOP could pause agent for 5-10+ seconds per checkpoint.

**Recommendation:** Benchmark checkpoint duration. Set SLO (<2s for <100MB). Consider copy-on-write snapshots or incremental checkpoints.

**Status:** FIXED — Section 4.4 now includes a checkpoint duration SLO (P95 < 2s for ≤ 100MB workspaces), documents workspace size impact on checkpoint duration per tier (best-effort, cooperative, SIGSTOP), and references the Phase 2 checkpoint duration benchmark (Section 18). Section 16.5 SLO table updated with the checkpoint duration target. Incremental checkpoints noted as deferred mitigation for larger workspaces.

### SCA-007. Postgres Connection Pressure Under Scale [High] — FIXED
**Section:** 12.3

Multiple components hitting Postgres: quota checkpoints, audit inserts, billing writes, session state updates. Write amplification not analyzed.

**Recommendation:** Estimate write IOPS at capacity tiers. Batch audit/billing writes. Consider separate Postgres for write-heavy paths.

**Status:** FIXED — Added per-tier write IOPS estimation table (4 write sources, 3 tiers) to Section 12.3 with burst headroom analysis. Added batching guidance for billing events and audit logs with configurable flush intervals and batch sizes. Documented optional separate Postgres instance for billing/audit writes at Tier 3. Added write IOPS and batching parameters to Section 17.8 tier reference table.

### SCA-008. Delegation Tree Unbounded Gateway Memory [High] — FIXED
**Section:** 8.2

50-node tree with virtual child interfaces, event buffers, and elicitation state in single replica memory.

**Recommendation:** Calculate per-node memory footprint. Set `maxTreeMemoryBytes`. Offload completed subtree results to Postgres.

**Status:** FIXED — Added per-node memory footprint table (~12 KB/node, ~600 KB for 50-node tree) to Section 8.2. Added `maxTreeMemoryBytes` field (default 2 MB) to the delegation lease with atomic Redis enforcement. Added completed subtree offloading to Postgres (`session_tree_archive` table) with lightweight in-memory stubs and on-demand LRU-cached fetch.

### SCA-009. Warm Pool Claim Contention Under Burst [Medium]
**Section:** 4.6.1

10 replicas * 50 claims = 500 API server attempts, 450 failures from optimistic locking conflicts.

**Recommendation:** Consider Redis-based pre-claim or claim broker pattern. Benchmark claim success rate at various burst levels.

### SCA-010. Elicitation Chain Latency at Depth [Medium]
**Section:** 9.2

5 hops = 150s worst-case forwarding latency before user sees elicitation.

**Recommendation:** Consider gateway shortcut path (O(1) vs O(depth)). Document expected latency per depth.

### SCA-011. Per-Session Coordination Lease TTL Gap [Medium]
**Section:** 10.1

Unspecified TTL means undefined session stall duration during replica failure.

**Recommendation:** Document TTL (10-15s). Add session heartbeat from gateway to client for proactive reconnection.

### SCA-012. MinIO Upload/Download Bottleneck via Gateway Proxy [Medium]
**Section:** 12.5

100 sessions checkpointing 100MB simultaneously = 10GB through gateway.

**Recommendation:** Consider pre-signed URLs for direct pod-to-MinIO checkpoint uploads.

### SCA-013. No Back-Pressure on Session Creation [Medium]
**Section:** 11.1

No queuing during warm pool exhaustion. Burst requests fail while system scales up.

**Recommendation:** Implement bounded admission queue. Emit queue depth metrics for HPA.

### SCA-014. Semantic Cache Memory/Latency Overhead [Medium]
**Section:** 4.9

100K entries = ~300MB Redis memory. Embedding computation adds 10-50ms per lookup.

**Recommendation:** Document overhead. Consider dedicated Redis or pgvector. Specify embedding model and computation location.

### SCA-015. No Gateway Load Shedding Strategy [Medium]
**Section:** 11.6

Overloaded gateway degrades all sessions rather than maintaining quality for a subset.

**Recommendation:** Implement adaptive load shedding. Prioritize existing sessions over new creation.

### SCA-016. Controller Queue Overflow Drops Events [Medium]
**Section:** 4.6.1

Default 500 depth, default 10h resync. Dropped events unprocessed for hours.

**Recommendation:** 5-10min resync period. Alert on overflow. Analyze queue depth at target capacity.

### SCA-017. Billing Event Synchronous Write on Hot Path [Medium]
**Section:** 11.2.1, 11.8

Every session lifecycle operation performs synchronous Postgres INSERT.

**Recommendation:** Consider local write-ahead buffer or batch multi-row inserts to amortize overhead.

### SCA-018. Missing Concurrent Mode Scaling Analysis [Low]
**Section:** 5.2

8 concurrent bursty LLM workloads sharing CPU/memory with no contention analysis.

**Recommendation:** Provide per-slot sizing guidance. Benchmark at various slot counts.

### SCA-019. Duplicate Billing Event Stream Definitions [Info]
**Section:** 11.2.1, 11.8

Identical topic with different field names (`tokens_in`/`tokens_out` vs `tokens_input`/`tokens_output`, `token.checkpoint` vs `token_usage.checkpoint`).

**Recommendation:** Consolidate into single authoritative section.

---

## 5. Protocol Design & Future-Proofing

### PRO-001. `ExternalProtocolAdapter` Interface Too Thin for Stateful Protocols [High] — FIXED
**Section:** 15

Only `HandleInbound`, `HandleDiscovery`, `Capabilities`. No lifecycle hooks for A2A's task lifecycle or push notifications.

**Recommendation:** Extend with `OnSessionCreated`, `OnSessionEvent`, `OnSessionTerminated`. Add `OutboundCapabilities` declaration.

**Status:** FIXED — Extended `ExternalProtocolAdapter` interface with three lifecycle hooks (`OnSessionCreated`, `OnSessionEvent`, `OnSessionTerminated`) and `OutboundCapabilities()` declaration. All new methods are optional via `BaseAdapter` embedding with no-op defaults, so existing adapters require no changes.

### PRO-002. MCP Task Semantics Hardcoded into Core [High]
**Section:** 7, 8, 9.2

Session lifecycle, task tree, and delegation deeply structured around MCP Tasks. A2A's `canceled`/`unknown` states and artifact model unmapped. No inbound A2A task entry path.

**Recommendation:** Define canonical task state machine as Lenny-native. Add `canceled` state now. Define inbound A2A task mapping to session creation.

**Status:** FIXED — Defined Lenny-native canonical task state machine in Section 8.9 with all states (`submitted`, `running`, `completed`, `failed`, `cancelled`, `expired`, `input_required`) and explicit transitions. Added protocol mapping table for MCP and A2A (including `canceled`/`unknown`/`expired` mappings). Extended session state machine in Section 7 with `cancelled` and `expired` transitions. Expanded Section 21.1 with inbound A2A task-to-session mapping details, artifact mapping, and `canceled`/`unknown` state handling.

### PRO-003. MCP Spec Version Pinning Missing [High] — FIXED
**Section:** 15

No target MCP version specified. No version negotiation. No compatibility matrix for client/server version mismatch.

**Recommendation:** Pin target MCP version. Add version negotiation to `MCPAdapter`. Support two concurrent versions.

**Status:** FIXED — Pinned target MCP spec version to 2025-03-26 in Section 15.2. Added version negotiation protocol to `MCPAdapter` (client sends `protocolVersion`, gateway responds with highest mutual version, rejects unsupported with `MCP_VERSION_UNSUPPORTED`). Documented compatibility policy: two concurrent versions (current + previous) with 6-month deprecation window. Updated Section 15.5 item 2 to cross-reference negotiation details.

### PRO-004. `publishedMetadata` Lacks Schema Validation [Medium]
**Section:** 5.1

Opaque pass-through with no validation. Malformed A2A agent cards served to clients.

**Recommendation:** Add optional `schemaRef` field. Validate at write time when present. Add `version` field.

### PRO-005. `OutputPart` Lossy Translation Undocumented [Medium]
**Section:** 15.4.1

Round-trip through multiple adapters causes information loss. No fidelity contract.

**Recommendation:** Define translation tables per adapter. Add `protocolHints` in annotations for round-trip fidelity.

### PRO-006. Elicitation Chain MCP-Native Without Abstraction [Medium]
**Section:** 9.2

No translation path for elicitation via OpenAI or A2A adapters.

**Recommendation:** Abstract into protocol-agnostic "human input request" primitive. Define per-adapter surfacing.

### PRO-007. A2A Outbound Delegation Authentication Undefined [Medium]
**Section:** 21.1

`ConnectorDefinition` doesn't account for A2A auth patterns (mutual auth, bearer tokens).

**Recommendation:** Extend `ConnectorDefinition` with `protocol` field and protocol-specific auth configs.

### PRO-008. Discovery Format Fragmentation [Medium]
**Section:** 9.1

Four different discovery formats (MCP, REST, OpenAI, future A2A). Dynamic discovery data not in `publishedMetadata`.

**Recommendation:** Introduce `DiscoveryProjection` per adapter. Add `RuntimeStatus` cache for dynamic data.

### PRO-009. Binary Protocol No Version Negotiation [Medium]
**Section:** 15.4.1

stdin/stdout protocol has no handshake. Unknown message types silently ignored on primary data channel.

**Recommendation:** Add `init`/`init_ack` handshake as first message on stdin.

### PRO-010. `type: mcp` Runtimes Tightly Coupled to MCP Transport [Low]
**Section:** 15

No mechanism to expose MCP runtime tools through other protocol adapters.

**Recommendation:** Consider tool-bridging in `ExternalAdapterRegistry`. Make tool previews protocol-agnostic.

### PRO-011. Delegation Lease `allowedExternalEndpoints` Untyped [Low]
**Section:** 8.3

Empty array with no schema. Will require breaking migration when A2A ships.

**Recommendation:** Define structured schema now: `[{ protocol, endpoint, connectorRef, matchPattern }]`.

### PRO-012. No Protocol Health Probing for Adapters [Low]
**Section:** 15

No mechanism to detect unhealthy adapters or unsupported protocol features.

**Recommendation:** Add `HealthCheck` method to adapter interface.

### PRO-013. `MessageEnvelope.from.kind` Insufficient for Multi-Protocol [Low]
**Section:** 15.4.1

`external` doesn't distinguish A2A vs AP vs MCP origin.

**Recommendation:** Add `from.protocol` field alongside `from.kind`.

---

## 6. Developer Experience (Runtime Authors)

### DEV-001. Insufficient Specification for Minimum-Tier Runtime [Critical] — FIXED
**Section:** 15.4.1, 15.4.3

Message schemas (inbound/outbound), heartbeat format, shutdown expectations, and exit codes all undefined. Echo runtime is prose description only. A developer cannot build a runtime from the spec alone.

**Recommendation:** Add Protocol Reference with complete JSON schemas for every message type. Include annotated protocol trace. Provide echo runtime as actual code or detailed pseudocode.

**Status:** FIXED — Added Protocol Reference with JSON schemas for all message types (message, heartbeat, shutdown, tool_result, response, tool_call, heartbeat_ack, status). Full MessageEnvelope on stdin with ignore-unknown-fields convention documented. Exit codes table added (0, 1, 2, 137). Annotated protocol trace for a complete Minimum-tier session. Minimum-tier description updated to explicitly require heartbeat/shutdown handling. Echo runtime pseudocode added to Section 15.4.4. `heartbeat_ack` added to outbound message table.

### DEV-002. OutputPart Complexity for Minimum-Tier [High] — FIXED
**Section:** 15.4.1

Minimum-tier runtimes must produce `OutputPart[]` but minimal valid form undefined. `from_mcp_content` helper implies SDK dependency contradicting "zero knowledge" promise.

**Recommendation:** Allow simplified response shorthand (`{type: "response", text: "hello"}`). Document minimal required fields. Make SDK helpers explicitly optional.

**Status:** FIXED — Added minimal required fields documentation (only `type` and `inline` required, all other fields optional with defaults). Added simplified text-only response shorthand (`{type: "response", text: "..."}`) normalized by adapter. Made `from_mcp_content` helper explicitly optional with no-SDK-dependency clarification. Updated protocol reference and annotated trace to show shorthand form.

### DEV-003. Degraded Experience for Minimum-Tier Poorly Documented [High]
**Section:** 4.7, 15.4.3

"Fallback-only mode" never fully enumerated. Scattered implications: no checkpoint, no interrupt, no credential rotation, no deadline warning.

**Recommendation:** Add Tier Comparison Matrix listing every capability by tier with fallback behaviors.

**Status:** FIXED — Added comprehensive Tier Comparison Matrix in Section 15.4.3 enumerating all tier-sensitive capabilities (checkpoint/restore, interrupt, credential rotation, deadline warning, graceful drain, protocol features) with explicit fallback behaviors for Minimum and Standard tiers.

### DEV-004. tool_call/tool_result Format Unspecified [High] — FIXED
**Section:** 15.4.1

No JSON schema for tool calls or results. No correlation mechanism. Sync vs async delivery undefined. Minimum-tier tool access unclear.

**Recommendation:** Add complete schemas with correlation mechanism. Clarify tool access per tier.

**Status:** FIXED — Added formal JSON schemas with field descriptions for both `tool_call` (outbound) and `tool_result` (inbound) in Protocol Reference. Added correlation mechanism documentation (`id` field uniqueness, adapter validation, out-of-order delivery). Specified synchronous request/response delivery semantics with interleaved message handling. Added tool access per tier table (Minimum: adapter-local tools only, no MCP; Standard: platform + connector MCP servers; Full: same as Standard plus lifecycle).

### DEV-005. No Quickstart Path or Tutorial Structure [High] — FIXED
**Section:** 15.4

Relevant runtime-author info scattered across 7+ sections in a 3700-line doc.

**Recommendation:** Create separate "Runtime Author Guide" with tutorial-order presentation.

**Status:** FIXED — Added Section 15.4.5 "Runtime Author Roadmap" providing a tier-organized reading order through 15 sections across the spec. Covers Minimum-tier (6 sections: echo runtime, binary protocol, state machine, tiers, filesystem layout, local dev), Standard-tier (4 sections: adapter component, MCP integration, delegation, runtime definition), and Full-tier (5 sections: pool config, session lifecycle, security, workspace schema, API versioning). All cross-references validated.

### DEV-006. Adapter Manifest Discovery Underspecified [Medium]
**Section:** 4.7

Path not documented as env var or constant. Schema shown only as example. Minimum-tier reading requirements unclear.

**Recommendation:** Define `LENNY_ADAPTER_MANIFEST` env var. Provide formal JSON schema. State Minimum-tier doesn't need manifest.

### DEV-007. Two Overlapping Shutdown Mechanisms [Medium]
**Section:** 4.7, 15.4.1

stdin `{type: "shutdown"}` and lifecycle channel `terminate` overlap for Full-tier runtimes.

**Recommendation:** Clarify per-tier shutdown protocol. Document which signal is authoritative.

### DEV-008. Echo Runtime Insufficient as Reference Implementation [Medium]
**Section:** 15.4.4

Only covers Minimum-tier. No reference for Standard/Full-tier integration points.

**Recommendation:** Provide at least two reference implementations (Minimum echo + Standard tool-calling).

### DEV-009. No Runtime Image Packaging Guidance [Medium]
**Section:** 4.7

No base image expectations, UID requirements clarification, filesystem layout creation responsibilities.

**Recommendation:** Add "Runtime Image Requirements" section covering UID/GID, writable paths, sidecar injection model.

### DEV-010. Credential Delivery Environment Variables Unclear [Medium]
**Section:** 4.7, 14

Env var names unspecified. Apparent contradiction with env blocklist.

**Recommendation:** Document exact env var names. Clarify blocklist applies to client-provided vars, not platform-injected credentials.

### DEV-011. No Error Handling Guidance for Runtime Authors [Medium]
**Section:** 16.3

Error categories defined but no guidance on how runtimes report errors (stdout format, exit codes, stderr handling).

**Recommendation:** Add "Error Reporting" section with schema for error responses, exit code mapping, and stderr treatment.

### DEV-012. SDK Minimization Goal at Risk for Standard Tier [Medium]
**Section:** 15.4.1

Standard tier requires MCP client protocol implementation without library support.

**Recommendation:** Provide lightweight runtime-side MCP client library (Go + one other language) or document wire-level MCP patterns.

### DEV-013. Lifecycle Channel Transport Mechanism Unspecified [Medium]
**Section:** 4.7

"Separate stdin/stdout stream pair" never explained mechanically. How do two stdin/stdout pairs coexist?

**Recommendation:** Specify transport (likely separate Unix socket). Add to adapter manifest. Provide JSON schemas.

### DEV-014. `type: mcp` Runtime Implementation Unclear [Medium]
**Section:** 5.1, 15

Not clear if it's "bring any MCP server" or requires adapter compliance.

**Recommendation:** Add dedicated subsection for `type: mcp` requirements.

### DEV-015. MessageEnvelope Complexity for Minimum-Tier [Low]
**Section:** 15.4.1

Full envelope on stdin strains "zero knowledge" claim.

**Recommendation:** Have adapter strip envelope for Minimum-tier, delivering only essential payload.

### DEV-016. Concurrent-Workspace slotId Thinly Described [Low]
**Section:** 15.4.1

No slot lifecycle, acknowledgment, or capacity discovery.

**Recommendation:** Mark as requiring Full-tier integration. Note protocol will be fully specified separately.

### DEV-017. Runtime Registration Admin-Only [Low]
**Section:** 5.1, 10.2

No self-service path for community developers.

**Recommendation:** Consider dev-mode sandbox registration without platform-admin access.

### DEV-018. Duplicate Billing Event Sections [Info]
**Section:** 11.2.1, 11.8

Same finding as 4.17.

---

## 7. Operator & Deployer Experience

### OPS-001. No Bootstrap Seed Mechanism for Day-1 [High] — FIXED
**Section:** 15.1

After `helm install`, Postgres is empty. No runtimes, pools, or credentials. Operator must manually call dozens of API endpoints.

**Recommendation:** Define bootstrap seed mechanism (Helm values section, init ConfigMap, or `lenny-ctl bootstrap` CLI). Make idempotent.

**Status:** FIXED — Added idempotent bootstrap seed mechanism in Section 17.6: Helm `bootstrap` values section, `lenny-bootstrap` init Job (post-install/post-upgrade hook), `lenny-ctl bootstrap` CLI command with dry-run and force-update modes. Defined minimum Day-1 seed (default tenant, runtime, pool). Added `POST /v1/admin/bootstrap` endpoint. Integrated into build sequence Phase 4.5. Local dev `make run` auto-applies seed.

### OPS-002. No Preflight Validation for Infrastructure Dependencies [High] — FIXED
**Section:** 12.3, 12.4, 12.5

Missing prerequisite (wrong PgBouncer mode, no CNI support, etc.) causes cryptic failures.

**Recommendation:** Add `lenny-preflight` Job validating Postgres, PgBouncer, Redis, MinIO, RuntimeClasses, cert-manager, CNI support.

**Status:** FIXED — Added `lenny-preflight` Job specification in Section 17.6: Helm pre-install/pre-upgrade hook (`hook-weight: "-10"`) validating all infrastructure dependencies before deployment. Checks: Postgres (connectivity, version ≥ 14), PgBouncer (transaction-mode, connect_query sentinel), Redis (connectivity, AUTH, TLS), MinIO (connectivity, SSE), RuntimeClasses, cert-manager, CNI NetworkPolicy support, Kubernetes version. Configurable via `preflight.enabled` and `preflight.timeoutSeconds`. Dev mode skips non-essential checks. CLI equivalent via `lenny-ctl preflight`. Updated existing RuntimeClass pre-install hook reference in Section 5 to point to full preflight spec.

### OPS-003. Helm Upgrade Doesn't Update CRDs [High] — FIXED
**Section:** 10.5, 17.6

Helm's known CRD limitation. Silently stale CRDs cause production incidents.

**Recommendation:** Document separate CRD application before `helm upgrade`. Add startup check validating CRD schema version.

**Status:** FIXED — Added Helm CRD upgrade limitation warning and controller startup schema-version validation in Section 10.5. Added required CRD upgrade procedure (apply CRDs before `helm upgrade`, GitOps sync-wave guidance) and preflight CRD version check in Section 17.6.

### OPS-004. SQLite in Tier 1 Dev Mode Behavioral Divergence [Medium]
**Section:** 17.4

RLS, advisory locks, LISTEN/NOTIFY, pgvector all unavailable in SQLite.

**Recommendation:** Acknowledge limitation explicitly. Consider embedded Postgres. Add CI testing against both backends.

### OPS-005. Expand-Contract Migration Needs Practical Guardrails [Medium]
**Section:** 10.5

No dry-run, no CI backward-compat testing, no three-release cadence documentation.

**Recommendation:** Add CI step running previous version tests against new schema. Add `--dry-run` flag. Document release cadence.

### OPS-006. Operational Runbooks Only Listed, Not Specified [Medium]
**Section:** 17.7

Seven runbooks listed as "must ship" with no structure, steps, or alert linkage.

**Recommendation:** Define per-runbook: triggering alert, diagnostic commands, remediation steps, escalation, verification.

### OPS-007. No CRD/Postgres Drift Detection [Medium]
**Section:** 4.6.2

Manual kubectl edits or stale controller create silent drift. No GitOps annotation guidance.

**Recommendation:** Add `lenny_controller_crd_drift` metric. Document GitOps managed-by annotations. Add "reconcile now" endpoint.

### OPS-008. PgBouncer Transaction-Mode RLS Interaction [Medium]
**Section:** 12.3

`SET` outside explicit transaction can leak to next borrower. Advisory locks are session-scoped, wrong for transaction-mode.

**Recommendation:** Specify `server_reset_query = DISCARD ALL`. Always use `SET LOCAL` inside transactions. Use `pg_advisory_xact_lock` for migrations.

### OPS-009. Token Service SPOF for New Sessions [Medium]
**Section:** 4.3

No health endpoint, no Section 16.5 alert, no circuit breaker parameters specified.

**Recommendation:** Add `TokenServiceUnavailable` critical alert. Specify health check endpoints. Define circuit breaker parameters.

### OPS-010. No Credential Pool Key Rotation Procedure [Medium]
**Section:** 4.9, 10.5

No procedure for adding new key, draining old, removing. No `draining`/`disabled` status per credential.

**Recommendation:** Add per-credential `status` field (active/draining/disabled). Expose via admin API. Add to runbooks.

### OPS-011. Helm Values Surface Underspecified [Medium]
**Section:** 17.6

Five sample values for a system needing dozens of configuration points.

**Recommendation:** Define complete `values.yaml` skeleton. Provide "minimal production" example.

### OPS-012. No Capacity Planning Guidance [Medium]
**Section:** various

No synthesis of resource requirements into deployer-facing sizing guidance.

**Recommendation:** Add T-shirt-sized reference architectures (Small/Medium/Large) with infrastructure estimates.

### OPS-013. Smoke Test Doesn't Cover Critical Path [Low]
**Section:** 17.4

Only tests echo runtime happy path. Missing: upload, setup, checkpoint, credential, delegation.

**Recommendation:** Add `integration` test level covering upload, setup, checkpoint/resume, single-level delegation.

### OPS-014. Observability Opt-In and Underdocumented in Dev Mode [Low]
**Section:** 17.4

Pre-built Grafana dashboard mentioned but not specified.

**Recommendation:** Ship dashboard JSON as checked-in artifact. Make observability default in Tier 2.

### OPS-015. Controller Failover Sizing Assumes Steady-State [Low]
**Section:** 4.6.1

Formula doesn't account for burst patterns.

**Recommendation:** Present formula as lower bound. Add burst sizing guidance.

### OPS-016. Duplicate Billing Event Stream [Medium]
**Section:** 11.2.1, 11.8

Same finding — consolidate into single section.

---

## 8. Multi-Tenancy & Tenant Isolation

### TEN-001. Postgres RLS Race Condition Under PgBouncer Transaction Mode [Critical]
**Section:** 4.2, 12.3
**Status:** FIXED — Spec updated to mandate `SET LOCAL` within explicit transactions, `current_setting(..., false)` for hard error on unset, PgBouncer `connect_query` sentinel value, and startup integration test for tenant isolation.

`SET app.current_tenant` outside explicit transaction can execute on one connection while the next query uses a different connection where the tenant was never set (or set by prior tenant).

**Recommendation:** Mandate `BEGIN; SET LOCAL app.current_tenant = '<id>'; ... COMMIT;` — `SET LOCAL` is explicitly transaction-scoped. RLS policy must use `current_setting('app.current_tenant', false)` to error on unset. Add startup integration test verifying isolation. Set sentinel value on connection init.

### TEN-002. Redis Tenant Isolation Absent [High] — FIXED
**Section:** 12.2, 12.4
**Status:** FIXED — Spec updated to mandate `t:{tenant_id}:` key prefix convention for all Redis-backed roles (Section 12.2 cross-reference, Section 12.4 detailed convention). Prefix enforced in Redis wrapper layer. Integration test (`TestRedisTenantKeyIsolation`) required.

No key-naming convention with tenant prefix. Cache poisoning and routing cross-contamination risk.

**Recommendation:** Mandate `t:{tenant_id}:` key prefix convention. Document as implementation requirement. Add integration test.

### TEN-003. MinIO Tenant Isolation Unspecified [High] — FIXED
**Section:** 4.5, 12.5

No per-tenant bucket or path-based isolation strategy. `DeleteByTenant` implies scoping but mechanism undefined.

**Recommendation:** Specify path-based isolation with mandatory `tenant_id` prefix validation at `ArtifactStore` interface level.

**Status:** FIXED — Added mandatory `/{tenant_id}/` path prefix for all MinIO object keys with interface-level validation in Sections 4.5 and 12.5. Added `ArtifactStore` tenant isolation requirement to Section 12.2 storage roles. `DeleteByTenant` now maps to a deterministic prefix-scoped bulk delete.

### TEN-004. Tenant Deletion Path Incomplete [High] — FIXED
**Section:** 12.8

No active session termination, credential revocation, Redis cleanup, environment teardown, CRD cleanup, ordering, or legal hold interaction specified.

**Recommendation:** Define full tenant deletion lifecycle with phases (soft-disable -> terminate sessions -> revoke credentials -> delete data -> clean CRDs -> produce receipt). Add `TenantState` enum.

**Status:** FIXED — Added six-phase tenant deletion lifecycle (soft-disable, terminate sessions, revoke credentials, delete data, clean CRDs, produce receipt) with `TenantState` enum (`active`, `disabling`, `deleting`, `deleted`). Added legal hold interaction check before data deletion with audit trail and force-delete escape hatch. Added idempotency and resumption guarantees.

### TEN-005. Task-Mode Pod Reuse Cross-Tenant Risk [High]
**Section:** 5.2

No explicit prohibition of cross-tenant task-mode pod reuse.

**Recommendation:** Add explicit statement: task-mode pods NEVER reuse across tenants. Enforce in gateway task assignment logic.

**Status:** FIXED — Addressed by SEC-005 fix. Tenant-pinning rule added to Section 5.2 with gateway enforcement of `tenantId` match on task-mode pod assignment.

### TEN-006. Three-Role RBAC Lacks Granularity [Medium]
**Section:** 10.2

No billing-admin, read-only user, or custom roles.

**Recommendation:** Add `tenant-viewer` role. Document extensibility path. Enumerate per-role permissions.

### TEN-007. `noEnvironmentPolicy` Semantics Unclear in Multi-Tenant [Medium]
**Section:** 10.6

`allow-all` meaning unclear — all runtimes system-wide or tenant-scoped? Security implications undocumented.

**Recommendation:** Clarify as tenant-scoped. Warn against `allow-all` in multi-tenant. Rename to `unmatchedUserRuntimeAccess`.

### TEN-008. Event/Checkpoint Store Missing RLS Confirmation [Medium]
**Section:** 4.4

RLS not explicitly confirmed for EventStore tables.

**Recommendation:** Confirm RLS coverage. Validate tenant ownership in checkpoint artifact references.

### TEN-009. MemoryStore Tenant Isolation Application-Only [Medium]
**Section:** 9.4

No interface contract requiring tenant isolation. Custom plugins could leak memories.

**Recommendation:** Add tenant isolation as interface contract requirement. Mandate RLS on default implementation. Add integration tests.

### TEN-010. Credential Pool Scoping Not Tied to Tenant [Medium]
**Section:** 4.9

Unclear if pools are global or tenant-scoped. Noisy-neighbor risk.

**Recommendation:** Specify scoping model. Add per-tenant limits if shared. Ensure lease records include tenant_id under RLS.

### TEN-011. Semantic Cache Lacks Tenant Partitioning [Medium]
**Section:** 4.9

Cross-tenant cache hits possible. Same as 2.7.

### TEN-012. Cross-Environment Delegation May Span Tenants [Medium]
**Section:** 10.6

No constraint preventing cross-tenant cross-environment delegation.

**Recommendation:** Add explicit constraint: cross-environment delegation only within same tenant.

### TEN-013. Tenant Identity from OIDC Underspecified [Medium]
**Section:** 4.2

No configurable claim name, no fail-closed behavior, no auto-provisioning specification.

**Recommendation:** Specify configurable OIDC claim. Mandate fail-closed on missing claim. Document service-to-service tenant identity.

### TEN-014. Billing Sequence Numbers Info Disclosure [Low]
**Section:** 11.2.1, 11.8

Gap-free sequences reveal activity volume if exposed cross-tenant.

**Recommendation:** Ensure strict tenant scoping on billing APIs.

### TEN-015. Public Metadata May Leak Tenant Info [Low]
**Section:** 5.1

`public` visibility serves content without auth to anyone.

**Recommendation:** Require platform-admin approval for `public` metadata in multi-tenant mode.

---

## 9. Storage Architecture & Data Management

### STO-001. Redis Fail-Open Enables Per-Tenant Budget Bypass [High] — FIXED
**Section:** 12.4

10 gateway replicas = 10x per-tenant budget during outage. Per-tenant budgets have no local enforcement.

**Recommendation:** Add per-tenant in-memory counters. Apply conservative ceiling per replica during fail-open.

**Resolution:** Added per-tenant fail-open budget enforcement to Section 12.4. Each replica enforces `per_replica_limit = tenant_limit / replica_count` during Redis unavailability. Added cumulative fail-open timer (`quotaFailOpenCumulativeMaxSeconds`, default 300s/1h window) that transitions to fail-closed after threshold. Updated quota counters table row to reference per-replica ceiling. Also resolves FAI-002 and POL-003.

### STO-002. Data-at-Rest Encryption Gaps [High] — FIXED
**Section:** 12.3, 12.4

Postgres, Redis, WAL archives, and backup encryption never mentioned.

**Recommendation:** Require Postgres encryption at rest. Document Redis data as ephemeral with app-layer encryption. Require encrypted WAL archives/backups.

**Resolution:** Added encryption-at-rest requirements to Section 12.3: Postgres storage must use volume-level encryption (managed or LUKS), WAL archives and backups must be encrypted (SSE or client-side). Updated backup paragraph to cross-reference encryption requirement. Added data-at-rest posture paragraph to Section 12.4: Redis documented as ephemeral with durable fallbacks, sensitive cached values require app-layer encryption, volume-level encryption recommended for defense-in-depth.

### STO-003. Orphaned MinIO Objects from Failed Checkpoints [Medium]
**Section:** 4.4, 12.5

Workspace uploaded to MinIO but Postgres metadata write fails = orphaned object invisible to GC.

**Recommendation:** Add periodic MinIO bucket scan cross-referencing Postgres. Clean objects >1hr old with no record.

### STO-004. Checkpoint Cadence Undefined [Medium]
**Section:** 5.2, 12.5

Listed as pool dimension but no default specified. GC throughput requirements unquantified.

**Recommendation:** Define default cadence (e.g., 15min). Document GC throughput requirements. Consider inline cleanup.

### STO-005. File Export Latency for Delegation Not Quantified [Medium]
**Section:** 8.8

200MB repo through 3 hops + 2 MinIO I/Os = 10-30s per delegation level.

**Recommendation:** Document expected latency. Consider workspace snapshot caching and content-addressed dedup.

### STO-006. Postgres SPOF for All Durable Writes [Medium]
**Section:** 12.3

30s RTO = 30s full platform blackout for all writes.

**Recommendation:** Consider write-ahead buffer for billing/audit events. Document expected behavior during failover. Serve hot-path reads from replica/cache.

### STO-007. Gap-Free Billing Sequences Infeasible Under Concurrent Writes [Medium]
**Section:** 11.2.1, 11.8

Postgres sequences have gaps on rollback. Gap-free requires serialized writes.

**Recommendation:** Clarify contract. Either use serialized writer pattern or relax to "monotonic with detectable gaps" + reconciliation endpoint.

### STO-008. PgBouncer Transaction Mode Breaks LISTEN/NOTIFY [Medium]
**Section:** 10.3, 12.3

LISTEN registrations lost when connection returns to pool.

**Recommendation:** Use dedicated direct Postgres connection for LISTEN/NOTIFY (bypassing PgBouncer), or rely on polling fallback.

### STO-009. pgvector Scaling Guidance Missing [Low]
**Section:** 9.4

Performance degrades at millions of rows. No partitioning or index guidance.

**Recommendation:** Document HNSW over IVFFlat, partition by tenant_id, add query latency metric.

### STO-010. No Autovacuum Tuning for High-Churn Tables [Low]
**Section:** 16.4

Sessions and quotas tables accumulate dead tuples.

**Recommendation:** Document aggressive autovacuum settings. Recommend partition-by-date for sessions. Monitor dead tuple count.

### STO-011. SQLite Dev Mode Schema Drift [Low]
**Section:** 17.4

Same as 7.4.

### STO-012. Legal Hold Degrades GC Performance [Low]
**Section:** 12.8

GC repeatedly fetches and skips held artifacts.

**Recommendation:** Add `WHERE legal_hold = false` to GC query. Index on `(expires_at, legal_hold)`.

---

## 10. Recursive Delegation & Task Trees

### DEL-001. Permanent Lease Extension Rejection Too Aggressive [High]
**Section:** 8.6

Rejection permanently starves entire tree. No scoped rejection, no cool-off, no admin reset.

**Recommendation:** Allow per-subtree rejection. Add cool-off period. Provide admin API to clear flag.

**Status:** FIXED — Rejection now scoped to the requesting subtree only (other subtrees unaffected). Added configurable `rejectionCoolOffSeconds` (default 300s, same layering as other lease extension settings) after which the subtree may request extensions again. Added admin API endpoint (`DELETE /admin/v1/trees/{treeId}/subtrees/{sessionId}/extension-denial`) to clear the denial flag immediately.

### DEL-002. Deep Tree Recovery Has No Timeout or Ordering [High]
**Section:** 8.11

Multi-level failure recovery has no ordering guarantee, no per-level timeout, no total tree recovery bound.

**Recommendation:** Specify bottom-up recovery. Add `maxTreeRecoverySeconds`. Document interaction with `maxResumeWindowSeconds`.

**Status:** FIXED — Section 8.11 now specifies bottom-up (leaves-first) recovery ordering. Added `maxLevelRecoverySeconds` (default 120s) and `maxTreeRecoverySeconds` (default 600s) with clear semantics for timeout expiry. Documented interaction with `maxResumeWindowSeconds` (effective window is the minimum of both).

### DEL-003. Orphan Cleanup Interval Underspecified [Medium]
**Section:** 8.11

No default `cascadeTimeoutSeconds`, no job interval, no metrics/alerts. Detached children could run indefinitely.

**Recommendation:** Specify defaults (3600s timeout, 60s interval). Add metrics. Count orphans against user quota.

### DEL-004. Credential Propagation `inherit` Lacks Depth Semantics [Medium]
**Section:** 8.3

15 nodes inheriting from same pool can silently exhaust `maxConcurrentSessions`.

**Recommendation:** Pre-flight credential capacity check at delegation time. Document interaction with tree size limits.

### DEL-005. Cross-Environment Delegation Policy Interaction [Medium]
**Section:** 10.6

Further delegation from cross-environment child unclear. `inherit` credential propagation likely wrong across environments.

**Recommendation:** Children use target environment's declarations. Prohibit `inherit` for cross-environment. Add worked example.

### DEL-006. `maxTokenBudget` Overshoot in Deep Trees [Medium]
**Section:** 11.2

Concurrent children racing against shared Redis counter. Overshoot = N * max_tokens_per_request.

**Recommendation:** Use per-child budget reservation. Add `budgetWarningThreshold` for proactive notification.

### DEL-007. `await_children` Mode `any` Leaves Orphaned Children [Medium]
**Section:** 8.9

Non-winning children run unchecked with no automatic cleanup.

**Recommendation:** Add `cancelRemaining: true` parameter. Add `maxUncollectedChildAge`. Emit warning when parent completes without collecting.

### DEL-008. `cancel_all` Cascade No Ordering or Timeout [Medium]
**Section:** 8.11

Deep tree cancellation has no per-node timeout. Parent `cancel_all` vs child `await_completion` precedence undefined.

**Recommendation:** Enforce top-down with per-node 30s timeout. Parent policy overrides descendant policies. Add total cascade timeout.

### DEL-009. No Fan-Out Rate Limiting [Medium]
**Section:** 8

10 children * 10 grandchildren = 100 pod claims in seconds.

**Recommendation:** Add `delegationRateLimit` (e.g., 5/sec/session). Queue excess.

### DEL-010. `send_message` Allows Arbitrary Cross-Tree Communication [Medium]
**Section:** 7.2, 8.5

No restriction that target must be in same tree. Information leak in multi-tenant.

**Recommendation:** Default restrict to same-tree. Add explicit `crossTree` option requiring policy authorization. Enforce tenant isolation.

### DEL-011. `children_reattached` Event Underspecified [Low]
**Section:** 8.2

No schema, no cursor/sequence, no completed-while-down result handling.

**Recommendation:** Define formal schema with child states, pending results, and last known sequence.

### DEL-012. `LeaseSlice` Never Defined [Low]
**Section:** 8.2

Referenced in `delegate_task` signature but no schema anywhere.

**Recommendation:** Define schema (subset of lease fields parent can override). Specify reserved vs shared budget.

### DEL-013. No Platform-Level Max Delegation Depth [Low]
**Section:** 8.3

Deployer could set `maxDepth: 100`. Elicitation, recovery, tracing all degrade.

**Recommendation:** Add platform-level `maxDelegationDepth` ceiling (e.g., 10) in Helm config.

### DEL-014. Non-Cascading Interrupt Creates Inconsistent State [Low]
**Section:** 6.2

Parent suspended but children keep running and consuming budget.

**Recommendation:** Add `cascade: true` option to interrupt API. Pause tree-wide budget while root suspended.

### DEL-015. Missing Section 8.7 [Info]
**Section:** 8

Numbering gap, likely from deleted content.

**Recommendation:** Renumber or note gap.

---

## 11. Session Lifecycle & State Management

### SES-001. Checkpoint Failure May Permanently Freeze Agent [Critical] — FIXED
**Section:** 4.4

No guarantee `SIGCONT` sent on all failure paths. Adapter crash mid-checkpoint leaves agent SIGSTOPped with no recovery.

**Recommendation:** Add `defer SIGCONT` invariant. Add checkpoint-level timeout (60s) with unconditional SIGCONT. Document adapter crash behavior.

**Resolution:** Added 60-second checkpoint timeout for both Full-tier (lifecycle channel) and embedded adapter paths in Section 4.4. Full-tier runtimes autonomously resume after timeout. Embedded adapter requires `defer SIGCONT` invariant and a 60-second watchdog timer with unconditional SIGCONT on expiry. Adapter crash recovery documented: liveness probe failure triggers pod restart, session resumes from last successful checkpoint per Section 7.2.

### SES-002. Pod State Machine Missing Failure Transitions [High] — FIXED
**Section:** 6.2

No `running_setup -> failed` or `finalizing_workspace -> failed` transitions shown.

**Recommendation:** Add explicit failure transitions from all pre-`attached` states. Define retry policy for pre-attached failures.

**Resolution:** Added explicit failure transitions from all six pre-attached states (`warming`, `sdk_connecting`, `receiving_uploads`, `running_setup`, `finalizing_workspace`, `starting_session`) to the state machine diagram in Section 6.2. Added pre-attached failure retry policy: 2 retries with exponential backoff (500ms, 1s), fresh pod per retry, non-retryable for validation/policy errors, correlation ID on exhaustion.

### SES-003. Generation Counter Race During Postgres Fallback [High] — FIXED
**Section:** 10.1

Brief lock release window allows coordinator thrashing. Stale replica handling undefined.

**Recommendation:** Specify coordinator handoff protocol. Stale replica must stop RPCs, clear state, back off.

**Resolution:** Added coordinator handoff protocol to Section 10.1: new coordinator must atomically increment generation, send a `CoordinatorFence` RPC to the pod before issuing any other RPCs, and include the generation stamp on all subsequent RPCs. Added stale replica behavior: on generation-stale rejection, the replica must immediately stop RPCs, clear cached session state, apply jittered exponential backoff (500ms–8s) before re-contending, and emit structured log/metric (`lenny_coordinator_handoff_stale_total`).

### SES-004. `awaiting_client_action` Expiry Orphans Children [High] — FIXED
**Section:** 7.3

Transition to `expired` doesn't trigger `cascadeOnFailure`. Running children left orphaned.

**Recommendation:** `awaiting_client_action -> expired` must trigger cascade policy. Add `expired` as terminal state.

**Resolution:** Section 7.3 `awaiting_client_action` expiry now explicitly triggers `cascadeOnFailure`. `expired` added as terminal state in sections 7.3, 8.4 (budget return), 8.6 (`settled` mode), 8.8 (delegation cascade trigger), and 14 (webhook events).

### SES-005. Concurrent Checkpoint and Interrupt Operations [High] — FIXED
**Section:** 4.7

Both target the running process. No mutual exclusion. Interrupt during SIGSTOP is undeliverable.

**Recommendation:** Define mutual exclusion via adapter per-session lock. Queue operations. Document ordering semantics.

**Resolution:** Section 4.7 now specifies a per-session operation lock in the adapter that serializes `Checkpoint` and `Interrupt` RPCs. Defines queuing behavior for interrupt-during-checkpoint and checkpoint-during-interrupt, queue depth limits, and coalescing/drop semantics.

### SES-006. Session Derive Credential State Undefined [Medium]
**Section:** 19 #12

No specification of how derived session handles credential lease.

**Recommendation:** Derived sessions are independent with own lease. Reuse stored token for same user without re-elicitation.

### SES-007. SSE Buffer Overflow No Client Feedback [Medium]
**Section:** 7.2

Disconnection indistinguishable from network failure. `checkpoint_boundary` behavior undefined.

**Recommendation:** Send `buffer_overflow` event before drop. Define `checkpoint_boundary` schema.

### SES-008. `suspended` Timer vs `maxSessionAge` in Delegation Trees [Medium]
**Section:** 6.2

Children continue consuming budget while parent timer paused. No max suspend duration.

**Recommendation:** Add `maxSuspendedTime`. Clarify child timers continue independently.

### SES-009. Task State Machine Missing `cancelled` State [Medium]
**Section:** 8.9

`cancelled` used in `await_children` but absent from state machine. Transitions undefined.

**Recommendation:** Add `cancelled` as terminal state. Define transitions from all active states.

### SES-010. SDK-Warm Race Between Workspace Check and Pod Selection [Medium]
**Section:** 6.1

Fallback if SDK-warm claim fails undefined. Late-upload of blocking files after SDK-warm claim undefined.

**Recommendation:** Define fallback to pod-warm. Reject blocking-path uploads after SDK-warm claim.

### SES-011. `resume_pending`/`resuming` Missing Timeouts [Medium]
**Section:** 6.2

Infinite wait possible on pool exhaustion or checkpoint restoration hang.

**Recommendation:** Timeout `resume_pending` at `podClaimQueueTimeout`. Timeout `resuming` at restoration timeout (300s).

### SES-012. Elicitation Chain Fragile Across Gateway Failover [Medium]
**Section:** 9.2

Multi-hop elicitation state in gateway memory. Not persisted for failover reconstruction.

**Recommendation:** Persist elicitation chain state in SessionStore. Reconstruct on failover.

### SES-013. `claimed` State No Timeout [Low]
**Section:** 6.2

Client claims pod but never uploads. Pod held indefinitely.

**Recommendation:** Add `claimTimeout` (120s). Revert to `idle` on expiry.

### SES-014. Seal-and-Export Retry Unbounded [Medium]
**Section:** 7.1

No max retries, no total timeout. MinIO outage accumulates draining pods.

**Recommendation:** 3 retries, 300s total. Transition to `terminated` with `seal_failed` flag. Alert.

### SES-015. Workspace Materialization Override Semantics [Low]
**Section:** 5.1

Silent overwrites with no warnings for client uploads overwriting derived defaults.

**Recommendation:** Emit warnings for all overwrites. Consider configurable `overwritePolicy`.

### SES-016. `attached -> resume_pending` Trigger Undefined [Medium]
**Section:** 6.2

Multiple detection mechanisms (gRPC break, health check, eviction) with different latencies. Not enumerated.

**Recommendation:** Enumerate detection mechanisms with expected latencies. Define final-checkpoint attempt.

### SES-017. Concurrent Mode No Session-Level State Machine [Medium]
**Section:** 5.2

State machines defined for session mode only. Per-slot aggregation, draining, checkpoint, suspend all undefined.

**Recommendation:** Define slot-level state machine and aggregation rules. Or explicitly state simplified semantics with limitations.

### SES-018. Duplicate Billing Event Sections [Info]
**Section:** 11.2.1, 11.8

Same finding — consolidate.

---

## 12. Observability & Operational Monitoring

### OBS-001. No Elicitation Chain Metrics [High] — FIXED
**Section:** 16.1

Zero metrics for elicitation round-trip latency, pending count, suppression, timeouts.

**Recommendation:** Add `lenny_elicitation_roundtrip_seconds`, `_pending`, `_suppressed_total`, `_timeout_total`. Add `ElicitationBacklogHigh` alert.

**Status:** FIXED — Added four elicitation metrics to Section 16.1 (`lenny_elicitation_roundtrip_seconds` histogram, `lenny_elicitation_pending` gauge, `lenny_elicitation_suppressed_total` counter, `lenny_elicitation_timeout_total` counter) and `ElicitationBacklogHigh` warning alert to Section 16.5.

### OBS-002. No Credential Lifecycle Tracing Spans [High] — FIXED
**Section:** 16.3

No spans for credential assignment, rotation, or fallback chain. Critical latency path invisible.

**Recommendation:** Add `credential.assign`, `credential.rotate`, `credential.fallback_chain`, `credential.proxy_request` spans.

**Status:** FIXED — Added four credential lifecycle spans to Section 16.3's span boundaries table: `credential.assign`, `credential.rotate`, `credential.fallback_chain` (Gateway credential service), and `credential.proxy_request` (Gateway LLM proxy).

### OBS-003. Delegation Budget Consumption Not Tracked [High] — FIXED
**Section:** 16.1

No metrics for token budget utilization, extension rates, or tree-wide consumption trends.

**Recommendation:** Add `lenny_delegation_budget_utilization_ratio`, `_lease_extension_total`, `_tree_token_usage_total`. Add `DelegationBudgetNearExhaustion` alert.

**Status:** FIXED — Added three delegation budget metrics to Section 16.1 (`lenny_delegation_budget_utilization_ratio` gauge, `lenny_delegation_lease_extension_total` counter, `lenny_delegation_tree_token_usage_total` counter) and `DelegationBudgetNearExhaustion` warning alert to Section 16.5.

### OBS-004. Warm Pool Claim Queue Wait Missing [Medium]
**Section:** 16.1

Time-to-claim exists but no queue wait time, conflict count, or timeout metrics.

**Recommendation:** Add `lenny_pod_claim_queue_wait_seconds`, `_conflict_total`, `_timeout_total`.

### OBS-005. Gateway Subsystem Metrics Not Enumerated [Medium]
**Section:** 16.1

Per-subsystem metrics mentioned but not named. No circuit breaker state metrics.

**Recommendation:** Enumerate all per-subsystem metrics with explicit names.

### OBS-006. Token Service Metrics and Alert Missing [Medium]
**Section:** 16.1, 16.5

No latency, error, or circuit breaker metrics. No unavailability alert.

**Recommendation:** Add request duration, error counter, circuit breaker state. Add `TokenServiceUnavailable` alert.

### OBS-007. No SLO for Delegation Spawn Latency [Medium]
**Section:** 16.5

Delegation is user-facing but has no SLO.

**Recommendation:** Add P95 <15s SLO for delegation spawn. Add `lenny_delegation_spawn_latency_seconds` metric.

### OBS-008. Checkpoint Observability Insufficient [Medium]
**Section:** 16.1

No success/failure rates, failure reasons, resume success by checkpoint age, or data loss window.

**Recommendation:** Add `lenny_checkpoint_total` (by outcome), `_age_at_resume_seconds`, `_resume_total`, `_data_loss_window_seconds`.

### OBS-009. No Setup Command Metrics [Medium]
**Section:** 16.1

Setup commands on hot path with no duration, timeout, or failure metrics.

**Recommendation:** Add `lenny_setup_command_duration_seconds`, `_rejected_total`, `_phase_duration_seconds`.

### OBS-010. Workspace Materialization Tracing Too Coarse [Medium]
**Section:** 16.3

`session.upload` span doesn't distinguish network transfer, extraction, validation.

**Recommendation:** Add child spans: `workspace.stream_to_staging`, `workspace.validate`, `workspace.extract_archive`, `workspace.promote`.

### OBS-011. No Concurrent Mode Slot Metrics [Medium]
**Section:** 16.1

No slot utilization, allocation latency, or saturation metrics.

**Recommendation:** Add `lenny_concurrent_slots_active`, `_total`, `_utilization_ratio`, `_wait_seconds`.

### OBS-012. PgBouncer/Read Replica Monitoring Not in Alerting [Medium]
**Section:** 16.5

No alerts for PgBouncer saturation or read replica lag.

**Recommendation:** Add `PgBouncerPoolSaturated`, `PgBouncerClientWaitHigh`, `PostgresReadReplicaLag` alerts.

### OBS-013. No LLM Reverse Proxy Metrics [Medium]
**Section:** 4.9

Critical path with no visibility. Per-pool comparison impossible.

**Recommendation:** Add `lenny_llm_proxy_request_duration_seconds`, `_request_total`, `_active_requests`, `_upstream_duration_seconds`.

### OBS-014. Various Low/Info Findings

Including: FinalizerStuck not in Section 16.5, CredentialPoolExhausted alert missing, controller queue overflow no alert, no experiment variant labels on metrics, Redis fail-open observability gap, trace sampling may lose delegation context.

---

## 13. Compliance, Governance & Data Sovereignty

### COM-001. GDPR Erasure Flow Incomplete [Critical] — FIXED
**Section:** 12.8

MemoryStore, Redis, semantic cache, shared workspace snapshots, and external SIEM not in scope. Billing event immutability directly conflicts with erasure obligation.

**Recommendation:** Enumerate every storage backend in erasure spec. Use pseudonymization for billing events. Define erasure propagation contract to external sinks. Address workspace deduplication.

**Status:** FIXED — Expanded erasure scope to cover all 11 storage backends in a detailed table. Added tenant-controlled billing erasure policy: pseudonymize by default, with an exempt option backed by GDPR Article 17(3)(b). Added external sink propagation via `erasure.requested` event with acknowledgment tracking. Added workspace deduplication safeguard using reference counting.

### COM-002. Data Residency Delegation Insufficient [High]
**Section:** 12.8

Delegation trees can span regions. No region-awareness in routing. No per-tenant storage routing.

**Recommendation:** Add optional `dataResidencyRegion` on Tenant/Environment. Enforce region constraints on pod pools and storage. Provide multi-region reference architecture.

**Status:** FIXED — Added optional `dataResidencyRegion` field on Tenant and Environment configuration. Region constraints enforced at three levels: pod pool routing (including transitive delegation), storage routing via `StorageRouter` interface, and session creation validation. Added inheritance model (tenant → environment, restrict-only). Added multi-region reference architecture with region-local control planes and global tenant catalog.

### COM-003. Audit Log Integrity Best-Effort [High]
**Section:** 11.7

Startup check warns only. No continuous monitoring. No cryptographic integrity. Superuser bypass unaddressed.

**Recommendation:** Hard-fail startup check in production. Add periodic background check. Add hash chaining. Validate SIEM connectivity.

**Status:** FIXED — Section 11.7 rewritten with four layered integrity controls: (1) startup grant check hard-fails in production, (2) periodic background grant and chain verification every 5 minutes with critical alerts and optional graceful shutdown on drift, (3) SHA-256 hash chaining on audit entries for tamper-evident sequencing, (4) SIEM connectivity validated at startup (hard-fail) and monitored at runtime. Superuser bypass explicitly mitigated via hash chain detection and independent SIEM copy.

### COM-004. Billing Event No Correction Mechanism [High]
**Section:** 11.2.1, 11.8

Absolute immutability makes error correction impossible.

**Recommendation:** Add `billing_correction` event type referencing original. Consumers reconstruct by applying corrections.

**Status:** FIXED — Added `billing_correction` event type to the billing event types table in Section 11.2.1, with `corrects_sequence` and `correction_reason` fields in the event schema. Documented consumer reconstruction semantics: corrections carry replacement values, are applied in sequence-number order, and the latest correction to a given original takes precedence. Append-only immutability is preserved — corrections are new events, original events remain unchanged.

### COM-005. No Data Classification Tiers [High]
**Section:** various

All data treated uniformly despite PII, PHI, credentials, business data having different requirements.

**Recommendation:** Define classification tiers. Allow per-tenant workspace classification. Drive controls from classification.

**Status:** FIXED — Added Section 12.9 Data Classification. Defines four tiers (Public, Internal, Confidential, Restricted) with default mappings for all data types. Per-tenant workspace classification override via `dataClassification.workspaceTier`. Controls table specifying encryption, access, audit, retention, erasure, and residency requirements per tier. Enforcement at storage interface boundary. Cross-references to existing legal hold, data residency, credential encryption, and erasure mechanisms.

### COM-006. Retention Doesn't Align With Regulations [Medium]
**Section:** 7.1, 16.4, 11.8

7-day artifact default below most regulatory minimums. 90-day audit below HIPAA (6yr) and SOX (7yr).

**Recommendation:** Provide regulation-aligned retention presets. Allow per-tenant overrides. Add `retentionPolicyPreset`.

### COM-007. No Physical Isolation Path [Medium]
**Section:** 4.2

Logical isolation only. Shared Postgres, Redis, MinIO across tenants.

**Recommendation:** Prioritize namespace-per-tenant. Document dedicated-tenant reference architecture. Add `tenantIsolationLevel` config.

### COM-008. No Consent Management [Medium]
**Section:** various

No consent records, processing purpose tracking, or withdrawal mechanism.

**Recommendation:** Add `consentRecord` to session creation. Add `processingPurpose` enum. Provide consent management API.

### COM-009. No FIPS Compliance Path [Medium]
**Section:** 10.3, 10.5, 12.4

No FIPS-validated crypto modules. No TLS version minimum. No key type requirements.

**Recommendation:** Add `fipsMode` flag. Specify TLS 1.2+ minimum. Document FIPS KMS backends.

### COM-010. No Cross-Border Transfer Controls [Medium]
**Section:** various

LLM API calls, webhooks, connector calls may transit data across borders with no visibility.

**Recommendation:** Add region metadata to credentials/connectors. Add `allowedTransferRegions` on tenants. Log transfers.

### COM-011. Legal Hold Implementation Gaps [Medium]
**Section:** 12.8

No cascading holds, no hold inventory, no expiry mechanism, no audit event hold coverage.

**Recommendation:** Implement cascading holds. Add `GET /v1/admin/legal-holds`. Ensure partition drops check holds.

### COM-012. Various Low Findings

Including: no PIA/DPIA integration, experiment compliance safeguards, SOC 2 Trust Service gaps.

---

## 14. API Design & External Interface Quality

### API-001. REST/MCP Consistency Not Enforceable [High] — FIXED
**Section:** 15.2.1

"Shared service layer" is implementation aspiration without testing strategy.

**Recommendation:** Generate MCP schemas from OpenAPI. Add contract tests calling both surfaces.

**Status:** FIXED — Section 15.2.1 now specifies OpenAPI as the single authoritative schema with MCP tool schemas generated from OpenAPI definitions (item 4). Added contract testing requirement covering success paths, validation errors, and authz rejections across both API surfaces (item 5). Contract tests added to Phase 5 build sequence (Section 18) alongside OpenAPI→MCP schema generation build step.

### API-002. Admin API Error Response Schema Absent [High]
**Section:** 15.1

One inline example. No error code catalog, no per-endpoint docs, no validation error format.

**Recommendation:** Add canonical error envelope, error code table, validation error format, rate-limit headers.

**Status:** FIXED — Section 15.1 now includes: canonical error response JSON envelope with all required fields, error code catalog (18 codes across all four categories with HTTP status mappings), structured validation error format with per-field details for 400 responses, and rate-limit headers specification (X-RateLimit-Limit/Remaining/Reset, Retry-After). Error codes cross-reference Section 16.3 taxonomy. Shared error taxonomy in Section 15.2.1 item 3 remains consistent.

### API-003. `dryRun` Semantics Undefined [High] — FIXED
**Section:** 15.1, 21.5

No definition of behavior, supported endpoints, response format, or interaction with etags.

**Recommendation:** Define full semantics: performs validation without persistence. Enumerate supported endpoints.

**Status:** FIXED — Section 15.1 now includes full `dryRun` specification: validation-only semantics (no persistence, no side effects, no audit events), enumerated supported endpoints (all admin POST/PUT), response format (identical to non-dry-run with `X-Dry-Run: true` header), ETag interaction (validates `If-Match` on PUT, ignores on POST), and explicit exclusions (DELETE and action endpoints). Section 21.5 cross-references the Section 15.1 definition.

### API-004. ETag Concurrency Completely Unspecified [High] — FIXED
**Section:** 15.1

No generation mechanism, required headers, mismatch behavior, or interaction with DELETE.

**Recommendation:** Specify ETags on all GET responses. Require `If-Match` on PUT (428 on missing, 412 on mismatch). Use version counters.

**Status:** FIXED — Section 15.1 now includes full "ETag-based optimistic concurrency" specification: Postgres integer `version` column as generation mechanism, ETag header on all GET responses (including per-item in lists), `If-Match` required on PUT (428 `ETAG_REQUIRED` on missing, 412 `ETAG_MISMATCH` on mismatch), optional `If-Match` on DELETE, and `UPDATE ... WHERE version = $2` implementation pattern. Added `ETAG_REQUIRED` to the error code catalog.

### API-005. No Pagination Specification [High]
**Section:** 15.1

Multiple list endpoints with no pagination contract.

**Recommendation:** Define cursor-based pagination envelope. Standard query parameters (`cursor`, `limit`, `sort`).

**Status:** FIXED — Section 15.1 now includes full "Cursor-based pagination" specification: standard query parameters (`cursor`, `limit` with default 50/max 200, `sort` with `field:asc`/`field:desc` syntax), JSON response envelope (`items`, `cursor`, `hasMore`), opaque URL-safe cursors with 24-hour expiry, and explicit enumeration of all list endpoints covered by the pagination contract.

### API-006. Admin API Resource Schemas Not Defined [Medium]
**Section:** 15.1

No mapping from internal YAML to REST JSON. No metadata fields (timestamps, versions).

**Recommendation:** Publish OpenAPI spec as normative reference or add representative request/response examples.

### API-007. `POST /messages` Schema Undefined [Medium]
**Section:** 7.2, 15.1

Primary interaction endpoint with no request/response schema.

**Recommendation:** Add clear request/response examples for normal message and `inReplyTo` reply.

### API-008. No API Discoverability [Medium]
**Section:** 15.1

No `GET /v1/` endpoint, no version header, no HATEOAS links.

**Recommendation:** Add `GET /v1/` returning API version and resource types. Add `X-Lenny-API-Version` header.

### API-009. SSE Stream Contract Not Specified for REST [Medium]
**Section:** 7.2, 15.1

No endpoint definition, event format, or `Last-Event-ID` mapping for REST clients.

**Recommendation:** Define `GET /v1/sessions/{id}/stream` SSE endpoint with event format and cursor semantics.

### API-010. Idempotency Key Mechanism Unspecified [Medium]
**Section:** 11.5

Header vs body? Dedup window? Concurrent duplicate behavior?

**Recommendation:** `Idempotency-Key` header, 24hr retention, per-tenant scope, cached response for duplicates.

### API-011. Webhook Schema Inconsistency [Medium]
**Section:** 14, 11.8

Session callbacks and billing webhooks use different envelope schemas and verification mechanisms.

**Recommendation:** Unify delivery envelope. Same HMAC-SHA256 verification algorithm.

### API-012. External Adapter Registration Security [Medium]
**Section:** 15

Runtime-registered adapters could intercept traffic. No validation/sandboxing.

**Recommendation:** Require `platform-admin`. Scope to registered path prefix. Audit trail. Two-phase register+enable.

### API-013. Various Low Findings

Including: no bulk admin operations, derive endpoint unspecified, no rate-limit headers, usage API lacks grouping, session state matrix for API consumers, pool status endpoint lacks operational detail.

---

## 15. Competitive Positioning & Open Source Strategy

### OSS-001. No "Why Lenny?" Narrative [Critical] — FIXED
**Section:** 23

Competitors listed but differentiation never articulated. No value proposition statement.

**Recommendation:** Add "Why Lenny?" section with 3-5 concrete differentiators: runtime-agnostic adapter contract, recursive delegation primitive, self-hosted K8s-native, multi-protocol gateway, enterprise controls.

**Status:** FIXED — Added Section 23.1 "Why Lenny?" with 5 concrete differentiators grounded in spec sections: runtime-agnostic adapter contract (Section 15.4), recursive delegation primitive (Section 5, Principle 5), self-hosted K8s-native (Section 17, 17.4), multi-protocol gateway (Section 15, 3), enterprise controls (Sections 2, 8, 16).

### OSS-002. `agent-sandbox` Upstream Risk Unaddressed [High] — SKIPPED
**Section:** 4.6.1

Same as C1 — no abstraction layer, engagement strategy, or fallback plan.

**Status:** SKIPPED — Already resolved by KIN-001 fix. Section 4.6.1 now includes: (1) two-interface abstraction layer (`PodLifecycleManager` + `PoolManager`) so no Lenny component touches agent-sandbox CRDs directly, (2) dependency pinning with one-release-delay upgrade cadence as engagement strategy, (3) documented fallback plan with 2-3 engineering-week effort estimate for custom kubebuilder replacement.

### OSS-003. No Community Adoption Strategy [High]
**Section:** 18

No target persona, adoption funnel, "time to hello world" metric, or governance model.

**Recommendation:** Define personas. Set <5min TTHW target. Decide governance early (Phase 1-2). Plan comparison guides.

**Status:** FIXED — Section 23.2 adds community adoption strategy with: (1) three target personas (runtime authors, platform operators, enterprise teams) with entry points, (2) < 5-minute TTHW target validated by CI smoke test, (3) BDfN governance model with steering committee transition plan and ADR-based decision process established in Phase 2, (4) comparison guides planned for Phase 17. Section 18 open-source readiness note updated to cross-reference Section 23.2.

### OSS-004. Missing Temporal/Modal/LangGraph Comparison [High]
**Section:** 23

Most direct competitive threats omitted.

**Recommendation:** Expand Section 23 with Temporal, Modal, LangGraph entries and specific differentiation.

**Status:** FIXED — Section 23 comparison table expanded with Temporal, Modal, and LangGraph entries including specific differentiation points. Section 23.1 differentiators updated to reference new competitors (runtime-agnostic vs SDK-coupled, self-hosted vs hosted-only). Section 23.2 comparison guides list updated to include all three.

### OSS-005. "Hooks and Defaults" May Create Hollow Day-One Experience [Medium]
**Section:** 22.6

No memory, guardrails, eval, caching, or routing out of the box.

**Recommendation:** Ship 1-2 reference implementations per interface. Plan plugin registry. Consider "batteries-included" Helm variant.

### OSS-006. Configuration Surface Overwhelming [Medium]
**Section:** various

Dozens of config points with no progressive disclosure.

**Recommendation:** Define "Simple mode" (<20 lines). Create progressive disclosure guide.

### OSS-007. No Licensing Decision [Medium]
**Section:** 18

License choice undocumented. Affects enterprise adoption and contributor onboarding.

**Recommendation:** Decide before Phase 2. Apache 2.0 for maximum adoption.

### OSS-008. 17-Phase Build Sequence "Never Ships" Risk [Medium]
**Section:** 18

No MVP definition, no timeline, no minimum viable product milestone.

**Recommendation:** Define "ready for" milestones. Front-load MVP track. Add timeline estimates.

### OSS-009. gVisor Default Barrier to Entry [Medium]
**Section:** 5.3

Not available on most dev machines or managed K8s.

**Recommendation:** Make runc default in Helm with warning. gVisor default in `values-production.yaml`.

### OSS-010. No Ecosystem Integration Beyond MCP [Medium]
**Section:** various

No CI/CD patterns, pre-built dashboards, provider marketplace, or dev tool integrations.

**Recommendation:** Ship Grafana dashboards. Document CI/CD patterns. Plan integrations directory.

### OSS-011. Low/Info Findings

Including: multi-protocol may dilute focus, billing open-core boundary undefined, no devrel strategy.

---

## 16. Warm Pool & Pod Lifecycle Management

### WAR-001. SDK-Warm Creates Dual-Pool Inventory Problem [High] — FIXED
**Section:** 6.1

No mechanism to control SDK-warm vs pod-warm ratio. Degradation path for wrong pod type undefined.

**Recommendation:** Introduce `sdkWarmRatio` or make SDK-warm pods degradable to pod-warm with documented penalty.

**Resolution:** Added `sdkWarmRatio` field (0.0–1.0) on `SandboxWarmPool` CRD to control SDK-warm vs pod-warm split. Documented degradation path: SDK-warm pods can be demoted to pod-warm with SDK teardown penalty (1–3 s). Added `lenny_warmpool_sdk_demotions_total` metric. Updated CRD field ownership table and CEL validation rules.

### WAR-002. Pool Sizing Formula Lacks Burst Term [High] — FIXED
**Section:** 4.6.1, 4.6.2

Formula doesn't model refill rate or burst absorption.

**Recommendation:** Extend formula with burst absorption term: `burst_p99_claims * pod_warmup_seconds`.

**Status:** FIXED — Added `burst_p99_claims * pod_warmup_seconds` burst absorption term to the PoolScalingController default formula (Section 4.6.2), the failover sizing guidance (Section 4.6.1), and the per-tier sizing formula (Section 17.8). Documented refill rate rationale and variable definitions.

### WAR-003. Execution Mode / Warm Pool Interaction Underspecified [High] — FIXED
**Section:** 5.2, 4.6

Task mode reuse and concurrent mode slot division not reflected in scaling formulas.

**Recommendation:** Add "Execution Mode Scaling Implications" subsection with per-mode formula variants.

**Resolution:** Added "Execution Mode Scaling Implications" subsection in Section 5.2 with per-mode `mode_factor` adjustment (session=1.0, task=avg reuse count, concurrent=maxConcurrent), adjusted formula, and caveats for cold start and slot saturation. Added cross-reference in Section 4.6.2 default formula.

### WAR-004. `sdkWarmBlockingPaths` Fragile and Static [Medium]
**Section:** 6.1

Static per-runtime. Archives invisible at claim time. No discovery mechanism.

**Recommendation:** Default archives to pod-warm. Consider explicit client signal for SDK-warm eligibility.

### WAR-005. Pod Eviction During SDK-Warm [Medium]
**Section:** 4.6.1, 6.2

No finalizer protection, no PDB coverage, preStop checkpoint pointless.

**Recommendation:** Clarify `sdk_connecting` pods carry `idle` label for PDB. Skip checkpoint in preStop.

### WAR-006. Experiment Variant Pool Waste [Medium]
**Section:** 10.7

Per-variant pools with safety factor multiply warm pod count linearly with experiments.

**Recommendation:** Consider shared pool with claim-time variant assignment. Use lower safety factor for low-traffic variants.

### WAR-007. Controller Failover Queue Cascading Timeouts [Medium]
**Section:** 4.6.1

30s `podClaimQueueTimeout` = 30s client request timeout. Post-failover burst from multiple replicas.

**Recommendation:** Set queue timeout < client timeout (15s vs 30s). Add jitter. Add queue depth limit.

### WAR-008. No Warm Pool Priority/Reservation [Medium]
**Section:** 4.6.1

First-come-first-served. Low-priority batch can claim last pod over high-priority production.

**Recommendation:** Consider priority field on claims. Reserve subset of warm pool for high-priority.

### WAR-009. Work Queue Overflow No Alert/Resync [Medium]
**Section:** 4.6.1

Same as 4.14 — add alert and periodic resync.

### WAR-010. No Warm Pod Staleness/Health Degradation Model [Medium]
**Section:** 16.1

Only cert expiry recycles pods. Memory fragmentation, runtime drift unaddressed.

**Recommendation:** Introduce `maxIdleAge` per pool (e.g., 2hr). Track `stale_warm_pods` metric.

### WAR-011. Certificate Expiry Creates Steady Churn [Low]
**Section:** 4.6.1, 10.3

~12-13 replacements/hr during idle periods for 50-pod pool.

**Recommendation:** Document expected churn rate. Make `certExpiryDrainThreshold` configurable.

### WAR-012. Scale-to-Zero Resume Latency Not Quantified [Low]
**Section:** 4.6.1

Cold-start path never measured.

**Recommendation:** Add cold-start latency table. Add `lenny_pool_cold_start_seconds` metric.

### WAR-013. `podClaimQueueTimeout` Invisible to Clients [Low]
**Section:** 4.6.1

Client blocks up to 30s with no feedback.

**Recommendation:** Consider returning session ID immediately in `queued` state.

---

## 17. Credential Management & Secret Handling

### CRD-001. LLM Proxy as SPOF and Latency Bottleneck [High] — SKIPPED
**Section:** 4.9

No subsystem isolation. Gateway outage kills all LLM calls in proxy mode.

**Recommendation:** Define as fourth gateway subsystem. Consider separate scalable deployment. Document availability tradeoff.

**Status:** SKIPPED — Already resolved by the SCA-002 fix, which added LLM Proxy as the 4th gateway subsystem (Section 4.1) with dedicated goroutine pool, circuit breaker, per-subsystem metrics, HPA metric for independent scaling, and documented extraction trigger. All three aspects of the recommendation (subsystem definition, scalable deployment path, availability tradeoff) are covered.

### CRD-002. Credential Rotation Unreliable for Non-Full Runtimes [High] — FIXED
**Section:** 4.7

Minimum/Standard tiers cannot receive rotated credentials. No restart/resume path defined.

**Recommendation:** Document per-tier rotation behavior. Auto-trigger checkpoint+restart for non-Full tiers.

**Resolution:** Added per-tier credential rotation behavior table in Section 4.7 Runtime Integration Tiers. Full tier uses lifecycle channel in-place rebind; Standard/Minimum tiers use checkpoint+restart with new lease. Updated Fallback Flow (Section 4.7) step 6 to reference tier-specific delivery.

### CRD-003. Credential Pool Exhaustion Handling Gaps [High] -- FIXED
**Section:** 4.9

No pre-claim availability check. Pod claimed then fails at credential assignment.

**Recommendation:** Pre-claim credential availability check. Define `CREDENTIAL_POOL_EXHAUSTED` error code. Track mismatch metric.

**Resolution:** Added pre-claim credential availability check in Section 4.9 (new "Pre-Claim Credential Availability Check" subsection) and Section 7.1 (new step 3 in session creation flow). Defined `CREDENTIAL_POOL_EXHAUSTED` error code under POLICY category in Section 16.3. Added `lenny_gateway_credential_preclaim_mismatch_total` metric in Section 16.1. Race condition between check and assignment is handled by releasing the pod back to the warm pool.

### CRD-004. Three Credential Modes Operator Confusion [Medium]
**Section:** 4.9, 14

Dual schema (Runtime vs WorkspacePlan). Precedence undefined. `userCredentialMode` workflows unclear.

**Recommendation:** Unify schema. Add operator decision tree. Specify precedence: Runtime defines envelope, WorkspacePlan selects within it.

### CRD-005. Gateway Memory Credential Material Exposure [Medium]
**Section:** 4.3

Materialized credentials cached in gateway memory. Lifetime, encryption, zeroing unspecified.

**Recommendation:** Zero after transmission. If cached, store only lease IDs (not material). Document gateway as trust boundary.

### CRD-006. Direct Mode Env Var Credential Exposure [Medium]
**Section:** 4.7

Same as 2.3 — credentials in env vars readable, persistent in crash dumps.

### CRD-007. No Revocation Propagation for Direct Mode Active Calls [Medium]
**Section:** 4.9, 11.4

Race window between revocation and pod termination allows unauthorized calls.

**Recommendation:** Document timing guarantees per mode. Recommend proxy for providers without short-lived tokens.

### CRD-008. Credential Health Scoring Unspecified [Medium]
**Section:** 4.9

No algorithm, thresholds, recovery mechanism, or update frequency.

**Recommendation:** Define states (healthy/degraded/cooldown/failed). Specify automatic recovery. Add health check for failed credentials.

### CRD-009. Delegation `inherit` May Leak Pool Boundaries [Medium]
**Section:** 8.3

User-scoped credential inherited by all children. Tree-wide concurrent session count not tracked.

**Recommendation:** Add tree-wide credential tracking. Document `inherit` as identity propagation.

### CRD-010. Credential Lifecycle Not in Audit Trail [Medium]
**Section:** 11.7

Only billing stream has credential events. Compliance requires full audit trail.

**Recommendation:** Add `credential.assigned`, `_rotated`, `_revoked`, `_released`, `_assignment_failed` to audit events.

### CRD-011. No Emergency Credential Rotation Procedure [Medium]
**Section:** 10.5, 17.7

No documented response for credential compromise.

**Recommendation:** Add emergency revocation procedure and admin endpoint. Add to runbooks.

### CRD-012. Semantic Cache Tenant Isolation [Low]
**Section:** 4.9

Same as 2.7/8.10.

### CRD-013. Token Service Lacks Rate Limiting [Low]
**Section:** 4.3

No per-replica quotas or anomaly detection on high-value target.

**Recommendation:** Add per-gateway rate limits. Add anomaly detection alerts.

### CRD-014. `callbackSecret` Storage Unspecified [Low]
**Section:** 14

Same as 2.12.

### CRD-015. Build Sequence Places Credentials Late [Info]
**Section:** 18

Same as C9.

---

## 18. Content Model, Data Formats & Schema Design

### SCH-001. `OutputPart` Lacks Schema Version [High] — FIXED
**Section:** 15.4.1

No version identifier on the universal content model. Future schema evolution breaks durable data.

**Recommendation:** Add `schemaVersion` integer field (default 1). Define forward-compatibility contract.

**Status:** FIXED — Added `schemaVersion` integer field (default `1`) to `OutputPart` schema. Defined forward-compatibility contract: producers must set `schemaVersion`, consumers must ignore unknown fields and must not reject higher versions. Updated minimum required fields to list `schemaVersion` as optional with default.

### SCH-002. `OutputPart` Translation Fidelity Undocumented [High] — FIXED
**Section:** 15, 15.4.1

Round-trip through multiple adapters causes undocumented information loss.

**Recommendation:** Define translation fidelity matrix per adapter. Add `protocolHints` annotations.

**Status:** FIXED — Added "Translation Fidelity Matrix" subsection under 15.4.1 documenting field-level fidelity (lossless/lossy/dropped) for all `OutputPart` fields across MCP, OpenAI Completions, REST, and A2A adapters. Added `protocolHints` annotation field specification with per-adapter directive structure.

### SCH-003. Durable Data Schema Versioning Missing [High]
**Section:** 15.5

No version stamp on Postgres records. 13-month billing events will span schema changes.

**Recommendation:** Add `schemaVersion` to all durable records: TaskRecord, BillingEvent, AuditEvent, CheckpointMetadata, SessionRecord.

**Status:** FIXED — Added `schemaVersion` cross-cutting requirement as item 7 in Section 15.5, covering all durable record types (TaskRecord, billing events, audit events, checkpoint metadata, session records). Added `schema_version` field to the billing event schema table (Section 11.2.1) and `schemaVersion` to the TaskRecord JSON schema (Section 8.9). Added `schema_version` to session record field list (Section 5).

### SCH-004. `MessageEnvelope` Missing Metadata Field [Medium]
**Section:** 15.4.1

No extension point for protocol-specific or deployer-specific data.

**Recommendation:** Add `metadata: map[string]any` with namespace convention (`lenny.` for platform, `x-` for deployer).

### SCH-005. `RuntimeDefinition` Capability Inheritance Ambiguous [Medium]
**Section:** 5.1

Only `capabilities.interaction` explicitly locked. Other sub-fields ambiguous.

**Recommendation:** Make entire `capabilities` block non-overridable on derived runtimes.

### SCH-006. `agentInterface` No Schema Validation [Medium]
**Section:** 5.1

No JSON Schema. A2A card auto-generation depends on specific fields.

**Recommendation:** Define JSON Schema. Validate at registration.

### SCH-007. `WorkspacePlan` Missing Source Types [Medium]
**Section:** 14

No `artifactRef` (for derive) or `delegationExport` (for delegation) source types.

**Recommendation:** Add both source types for full audit/replay coverage.

### SCH-008. `TaskRecord.messages` Role Enum Under-Specified [Medium]
**Section:** 8.9

`caller | agent` doesn't match `MessageEnvelope.from.kind` (client|agent|system|external).

**Recommendation:** Align with `MessageEnvelope.from.kind`. Include `from.id` for provenance.

### SCH-009. `RuntimeDefinition` Missing `type: mcp` Constraints [Medium]
**Section:** 5.1

No spec of which fields are forbidden/ignored for `type: mcp`.

**Recommendation:** Add schema constraints table by type. Validate at registration.

### SCH-010. `MessageEnvelope.delivery` Under-Defined for Multi-Thread [Medium]
**Section:** 15.4.1

Interaction with `threadId` and `slotId` undefined.

**Recommendation:** Document intended interaction. Note delivery scope extension needed for multi-thread.

### SCH-011. No `OutputPart.inline` Size Limit [Medium]
**Section:** 15.4.1

Unbounded inline content could exhaust gateway memory and create blob-like Postgres records.

**Recommendation:** Define `maxInlineSize` (e.g., 10MB). Auto-promote oversized content to ArtifactStore.

### SCH-012. Billing Event Schema Duplicated [Medium]
**Section:** 11.2.1, 11.8

Same finding — consolidate.

### SCH-013. Various Low Findings

Including: `CredentialLease` missing extensibility, `WorkspacePlan.env` no allowlist mode, `SemanticCache` schema too thin, `TaskSpec.input` notation ambiguous, `publishedMetadata` no validation hook.

---

## 19. Build Sequence & Implementation Risk

### BLD-001. Credential Leasing (Phase 11) Too Late [Critical] — FIXED
**Section:** 18

Phases 4-10 (core session + delegation) can only test with echo runtime. First end-to-end test with real LLM after 10 phases of code.

**Recommendation:** Split credential leasing. Phase 5.5: basic pool-based direct leasing. Phase 11: rotation, fallback, proxy, user-scoped, health scoring.

**Status:** FIXED — Basic credential leasing moved to Phase 5.5 (CredentialProvider interface, anthropic_direct provider, single-pool assignment, AssignCredentials RPC). Phase 11 narrowed to advanced features only. Real LLM testing possible from Phase 6.

### BLD-002. Security Hardening (Phase 14) Dangerously Late [Critical] — FIXED
**Section:** 18

NetworkPolicies, gVisor enforcement, and image signing come after real credentials (Phase 11) and OAuth (Phase 12) are in play. Credentials can be exfiltrated by misbehaving agents.

**Recommendation:** Introduce Phase 3.5: default-deny NetworkPolicy, gVisor validation, digest-pinned images. Full hardening stays at Phase 14.

**Status:** FIXED — Basic security hardening moved to Phase 3.5 (default-deny NetworkPolicy, gVisor validation, digest-pinned images). Phase 14 narrowed to comprehensive hardening. Network isolation in place before credentials are introduced at Phase 5.5.

### BLD-003. Observability (Phase 13) After All Complex Subsystems [High]
**Section:** 18

Phases 6-12 (streaming, delegation, credentials) built without distributed tracing.

**Recommendation:** Phase 2.5: structured logging with correlation fields + OpenTelemetry trace propagation. Full observability stack at Phase 13.

**Status:** FIXED — Phase 2.5 added with structured logging (correlation fields: tenantId, sessionId, taskId, sandboxId), OpenTelemetry trace propagation, and request-scoped correlation IDs. Phase 13 updated to reference Phase 2.5 as foundation; now scoped to full observability stack (metrics, dashboards, alerting, SLO monitoring).

### BLD-004. Missing Load Testing Phase [High]
**Section:** 18

No phase for benchmarking, capacity planning, or load testing.

**Recommendation:** Add Phase 13.5/14.5 for load testing covering all documented scaling concerns.

**Status:** FIXED — Phase 13.5 added between full observability (Phase 13) and security hardening (Phase 14). Covers seven key load testing scenarios: concurrent session creation latency, checkpoint SLO validation, gateway horizontal scaling to 10K sessions, pool scaling under burst demand, delegation chain throughput, streaming reconnect under load, and credential rotation latency. Produces capacity planning baselines and validates all documented SLOs before production hardening.

### BLD-005. Phase 12 Bundles Too Many Concerns [High]
**Section:** 18

MCP runtimes, concurrent execution, Token Service, and OAuth in one phase.

**Recommendation:** Split: 12a (Token Service + KMS), 12b (type:mcp support), 12c (concurrent modes).

**Status:** FIXED — Phase 12 split into three sub-phases: 12a (Token/Connector service with KMS and OAuth), 12b (`type: mcp` runtime support), 12c (concurrent execution modes with `slotId` multiplexing). Total scope preserved; each sub-phase has a single concern and distinct milestone.

### BLD-006. Echo Runtime Insufficient for Delegation Testing [High]
**Section:** 18

Echo can't call MCP tools. Phases 9-10 delegation untestable.

**Recommendation:** Build "delegation-capable test runtime" (scripted tool call sequences) as Phase 2/9 deliverable.

**Status:** FIXED — Added `delegation-echo` test runtime as a Phase 9 deliverable: a scripted test runtime that executes pre-defined tool call sequences (`lenny/delegate_task`, `lenny/send_message`), delegates to child sessions, and handles results. Clarified in Section 17.4 that the echo runtime cannot invoke MCP tools and that delegation testing requires `delegation-echo`.

### BLD-007. Admin API Foundation Should Precede Phase 3 [Medium]
**Section:** 18

PoolScalingController needs Postgres config but admin API is Phase 4.5.

**Recommendation:** Move admin API foundation to Phase 2.5 or document file-based bootstrap for Phase 3.

### BLD-008. No Database Schema Phase [Medium]
**Section:** 18

Phase 1 says "core types" but doesn't mention Postgres, migrations, PgBouncer, or RLS.

**Recommendation:** Phase 1 must include Postgres schema foundation, migration tooling, PgBouncer config, RLS.

### BLD-009. No Parallelization Identified [Medium]
**Section:** 18

17 phases presented as linear. Many are independent after Phase 7.

**Recommendation:** Add dependency graph. Identify parallel tracks (core, infra, platform).

### BLD-010. Community Phase 17 Bundles Features + Docs [Medium]
**Section:** 18

MemoryStore and semantic caching mixed with documentation and community guides.

**Recommendation:** Separate feature work from documentation. Make docs a continuous deliverable.

### BLD-011. Missing Compliance Validation Phase [Medium]
**Section:** 18

Legal hold, GDPR erasure, billing immutability, audit integrity not validated end-to-end.

**Recommendation:** Add compliance validation to Phase 13 or as Phase 13.5.

### BLD-012. Phase 1 Overloaded Without Testable Output [Medium]
**Section:** 18

Massive schema definition with no runnable deliverable.

**Recommendation:** Phase 1 produces Go module with types, CRDs applied to kind cluster, and validation tests.

### BLD-013. `agent-sandbox` Spike Missing [Medium]
**Section:** 18

No Phase 0 validation of upstream dependency. Same as C1.

### BLD-014. No Critical Path Analysis [Medium]
**Section:** 18

No effort estimates, no critical path, no timeline.

**Recommendation:** Add T-shirt sizing and identify critical path.

### BLD-015. Low/Info Findings

Including: Helm chart not in any phase, runbooks not in any phase, Phase 6 before Phase 7 unprotected window, build sequence creates concurrent mode testing gap.

---

## 20. Failure Modes & Resilience Engineering

### FAI-001. MinIO Failure Causes Silent Session Loss [Critical] — FIXED
**Section:** 4.4, 12.5

Checkpoint fails during eviction. No fallback storage. Agent SIGSTOPped then killed. Session state permanently lost.

**Recommendation:** Define fallback (node-local emergency storage or extended retry). Add `CheckpointStorageUnavailable` critical alert. Resume agent if checkpoint fails and pod isn't evicting.

**Resolution:** Added checkpoint storage failure recovery in Section 4.4: all MinIO uploads retried with exponential backoff (~5s total). Non-eviction checkpoints resume the agent and log failure (`lenny_checkpoint_storage_failure_total` metric). Eviction checkpoints accept loss — session marked `checkpoint_failed` in Postgres, `CheckpointStorageUnavailable` critical alert fires (added to Section 16.5). Section 12.5 updated with deployer guidance on monitoring. No node-local fallback — accept-loss semantics for eviction scenarios.

### FAI-002. Redis Fail-Open Quota Bypass [High] — FIXED
**Section:** 12.4

Same as 9.1 — per-tenant counters, cumulative timer, worst-case documentation.

**Resolution:** Fixed via STO-001. Per-tenant in-memory counters with conservative ceiling and cumulative fail-open timer added to Section 12.4.

### FAI-003. Postgres Failover Coordination Gap [High]
**Section:** 12.3, 10.1

Dual-store (Redis + Postgres) simultaneous unavailability orphans sessions for 30s. Billing blocks on Postgres.

**Recommendation:** Define behavior during dual-store unavailability. Clarify billing write blocking. Consider gateway-local coordination fallback.

**Resolution:** Fixed. Added dual-store unavailability behavior to Section 10.1 (existing sessions continue, new sessions rejected, coordination handoffs frozen, bounded duration, observability). Added Postgres failover cross-reference to Section 12.3. Added bounded in-memory billing write-ahead buffer to Section 11.2.1 with backpressure (reject new requests when buffer full).

### FAI-004. etcd Unavailability Not Addressed [High]
**Section:** 4.6.1

Pod claims, state transitions, pool reconciliation all freeze.

**Recommendation:** Document etcd as critical dependency. Add `EtcdUnavailable` alert. Define degraded-mode serving existing sessions from Postgres.

**Resolution:** Fixed. Added "etcd unavailability (degraded mode)" paragraph to Section 4.6.1 documenting etcd as critical dependency, defining degraded-mode behavior (existing sessions continue, new sessions rejected, pool replenishment frozen), and referencing the new `EtcdUnavailable` critical alert. Added `EtcdUnavailable` alert to Section 16.5 critical alerts table.

### FAI-005. PgBouncer as SPOF [High]
**Section:** 12.3

Bad rolling update takes all replicas down. No readiness probe verifying backend connectivity.

**Recommendation:** Add PDB. Implement readiness probe with backend verification. Consider gateway direct-to-Postgres fallback.

**Resolution:** Fixed. Added PDB requirement (`minAvailable: 1`) to Section 12.3 to prevent simultaneous replica termination during rolling updates. Added readiness probe specification with backend Postgres connectivity verification (lightweight query through PgBouncer, recommended settings). Documented full PgBouncer unavailability behavior: system treats it as Postgres outage, Redis-backed roles continue, Postgres-dependent operations rejected with 503, ties to dual-store unavailability rules in Section 10.1. Gateway direct-to-Postgres fallback was not added — the PDB + readiness probe combination is the standard Kubernetes approach; a direct fallback would bypass connection pooling and risk exhausting Postgres connection limits.

### FAI-006. Gateway preStop SIGKILL Cliff [Medium]
**Section:** 10.1

Long-running sessions never drain in 60s.

**Recommendation:** Send `session_relocating` event. Two-phase drain. Increase `terminationGracePeriodSeconds`.

### FAI-007. Controller Crash Blast Radius [Medium]
**Section:** 4.6.1

Stuck finalizers, cert expiry, orphan accumulation during crash loop.

**Recommendation:** Define max acceptable outage window. Add crash-loop detection. Document blast radius timeline.

### FAI-008. Seal-and-Export Holds Pods Indefinitely [Medium]
**Section:** 7.1

Same as 11.14 — bounded retries, terminal state, alert.

### FAI-009. No Postgres Circuit Breaker [Medium]
**Section:** 11.6

Slow Postgres piles up goroutines without circuit breaker.

**Recommendation:** Add circuit breaker (P99 >2s for 30s trips to half-open). PgBouncer `query_timeout`.

### FAI-010. Coordination Takeover Race [Medium]
**Section:** 10.1

Partitioned replica makes stale gateway-side decisions before generation mismatch detected.

**Recommendation:** Periodic lease validation (every 5s). Heartbeat mechanism at half-TTL.

### FAI-011. No Backpressure for Tree Recovery [Medium]
**Section:** 8.11

10 simultaneous pod failures in one tree overwhelm warm pool, Postgres, gateway.

**Recommendation:** Per-tree concurrent recovery limit (e.g., 3). Tree circuit breaker for mass failure.

### FAI-012. Credential Lease Renewal Protocol Undefined [Medium]
**Section:** 4.9

Who initiates, failure handling, grace period calculation all unspecified.

**Recommendation:** Define gateway-initiated renewal at `renewBefore`. Ensure materialized TTL >= 2x lease TTL. Add renewal failure alert.

### FAI-013. Event Replay Unbounded Storage [Medium]
**Section:** 7.2

2-hour sessions with event replay create large storage dependency.

**Recommendation:** Define max replay window. Fall back to checkpoint-based state recovery beyond window.

### FAI-014. Cosign Webhook Fail-Closed Deadlocks Warm Pool [Medium]
**Section:** 5.3

Webhook outage prevents all pod creation.

**Recommendation:** Deploy webhook HA. Add `timeoutSeconds`. Document break-glass procedure.

### FAI-015. Orphan Cleanup Job No Defined Failure Behavior [Low]
**Section:** 8.11

Same as 10.3.

### FAI-016. Dual-Controller Coordination Gap [Low]
**Section:** 4.6

Rapid config changes from PoolScalingController cause WarmPoolController oscillation.

**Recommendation:** Add debounce interval. Add `PoolConfigOscillation` alert.

### FAI-017. Token Service Stale Cache on Revocation [Low]
**Section:** 4.3

Same as credential revocation propagation concerns.

---

## 21. Experimentation & A/B Testing Primitives

### EXP-001. Eval Score Ingestion Pipeline Undefined [High] — FIXED
**Section:** 10.7

Results API references scores by variant but no schema, storage, or aggregation logic defined.

**Recommendation:** Define `EvalResult` schema. Gateway auto-associates with variant. Define Results API response.

**Resolution:** Added `EvalResult` Postgres schema, gateway auto-association logic, eval submission request body, and Results API aggregated response format to Section 10.7.

### EXP-002. Health-Based Rollback Unspecified [High] — FIXED
**Section:** 10.7

No metrics, conditions, or automation for experiment status transitions.

**Recommendation:** Either make explicit non-goal (admin-only transitions) or define pluggable `ExperimentHealthEvaluator`.

**Resolution:** Made admin-only status transitions the explicit v1 design in Section 10.7. Defined valid transition graph, audit event on transition, and reserved `ExperimentHealthEvaluator` interface as a declared future extension point. Updated "will not build" list to include auto-rollback.

### EXP-003. Experiment Propagation Through Delegation Underspecified [High] — FIXED
**Section:** 10.7

`inherit`/`control`/`independent` meanings unclear. Cross-experiment interaction undefined.

**Recommendation:** Define each mode precisely. "Innermost wins" for cross-experiment. Specify eval attribution.

**Resolution:** Added precise definition table for all three propagation modes (`inherit`, `control`, `independent`) in Section 10.7. Specified cross-experiment conflict resolution rule (innermost wins, only possible under `independent` mode). Defined eval result attribution semantics per mode. Clarified the `inherited` field in the experiment context JSON.

### EXP-004. PoolScalingController Variant Pool Waste [Medium]
**Section:** 4.6.2, 10.7

Same as 16.6.

### EXP-005. No Experiment-Aware Metrics [Medium]
**Section:** 16

No `experimentId`/`variantId` labels on metrics, traces, or billing events.

**Recommendation:** Add experiment labels to key metrics. Include in billing events and root trace spans.

### EXP-006. Experiment Assignment Not Audited [Medium]
**Section:** 10.7

Hash function, input, and stickiness mechanism unspecified. No audit trail.

**Recommendation:** Specify hash inputs. Record `experiment.assigned` audit event. Add `experimentContext` to session record.

### EXP-007. Results API Should Be Raw Data Export [Medium]
**Section:** 10.7

Pre-aggregation insufficient for real statistical analysis. External platforms need raw data.

**Recommendation:** Reframe as raw data export. Separate convenience aggregation endpoint.

### EXP-008. Replay Endpoint Lacks Experiment Semantics [Medium]
**Section:** 15.1

Replay/experiment interaction undefined. Could pollute experiment results.

**Recommendation:** Tag replays with `replay: true`. Exclude from experiment results by default. Accept `targetVariant` parameter.

### EXP-009. Various Low/Info Findings

Including: no experiment lifecycle hooks, cohort targeting undefined, experiment/environment interaction unclear, Phase 1 should include experiment data model slots.

---

## 22. Document Quality, Consistency & Completeness

### DOC-001. Duplicate Billing Event Sections [High] — FIXED
**Section:** 11.2.1, 11.8

Two definitions with divergent field names. Same finding as 4.17.

**Status:** FIXED — Consolidated into single authoritative definition in Section 11.2.1 using the more detailed content. Standardized on `tokens_input`/`tokens_output`, `token_usage.checkpoint`, and `credential.leased` naming. Flattened cost dimensions into top-level schema fields. Section 11.8 replaced with cross-reference to 11.2.1.

### DOC-002. Duplicate Section 17.5 [High]
**Section:** 17.5

"Cloud Portability" and "Operational Defaults" both numbered 17.5.

**Recommendation:** Renumber second to 17.8.

**Status:** FIXED — Renumbered second 17.5 ("Operational Defaults — Quick Reference") to 17.9, since 17.8 was already taken by "Capacity Tier Reference".

### DOC-003. Broken Cross-References to Section 14 [High]
**Section:** 14

"Webhook system" and "env blocklist" references point to WorkspacePlan schema. `session.awaiting_action` event referenced but not defined.

**Recommendation:** Add missing webhook event. Extract webhooks to subsection. Extract env blocklist.

**Status:** FIXED — Added `session.awaiting_action` to webhook event types in Section 14. Clarified cross-references at lines 718 and 1372 to point to specific fields (`env` field and `callbackUrl` respectively) within Section 14, making the references unambiguous without needing subsection extraction.

### DOC-004. Missing Section 8.7 [Medium]
**Section:** 8

Numbering gap.

### DOC-005. Alerts in Component Sections Missing from 16.5 [Medium]
**Section:** 16.5

`WarmPoolIdleCostHigh`, `FinalizerStuck`, `CosignWebhookUnavailable` not in centralized table.

**Recommendation:** Add all inline alerts to Section 16.5.

### DOC-006. Inconsistent Metric Names [Medium]
**Section:** 10.1

`lenny_gateway_active_sessions` vs `lenny_gateway_active_streams`.

**Recommendation:** Use one consistent name.

### DOC-007. Document Length [Medium]
**Section:** all

3700 lines in single document. Several sections are standalone specs.

**Recommendation:** Extract largest sections (credentials, delegation, API surface) to companion documents.

### DOC-008. `ReportUsage` RPC Missing from Table [Medium]
**Section:** 4.7, 11.2

Referenced but not in adapter RPC table.

**Recommendation:** Add to RPC table or clarify alternative mechanism.

### DOC-009. Terms Used Without Definition [Low]
**Section:** various

"generation", "MCP Fabric", "Streamable HTTP", "OutputPart", "LeaseSlice", "seal" used before definition.

**Recommendation:** Add glossary or "Key Terms" section.

### DOC-010. CRD Versioning Inconsistency [Low]
**Section:** 10.5, 15.5

`v1alpha1` vs `v1beta1` initial version.

**Recommendation:** Pick one and update the other.

### DOC-011. Various Low/Info Findings

Including: empty Section 20, inconsistent event naming, empty code block, no table of contents, Section 4.6.1 too long, runtime tiers described twice, `session.awaiting_action` webhook undefined.

---

## 23. Messaging, Conversational Patterns & Multi-Turn Interactions

### MSG-001. "Runtime Available" Under-Specified [FIXED]
**Section:** 7.2

Delivery path 2 undefined for runtimes mid-tool-call or not reading stdin.

**Recommendation:** Define precisely. Add delivery timeout. Document unread message behavior.

**Resolution:** Defined "runtime available" as `ready_for_input` state (between tool calls, after output, explicit input-wait). Added 30s configurable delivery timeout with fallback to inbox buffering. Documented that undelivered messages are never dropped — they buffer in FIFO order until consumed or session termination.

### MSG-002. No Dead-Letter for Inter-Session Messages [High] — RESOLVED
**Section:** 7.2, 8.5

Message to terminated task silently lost. No TTL, no confirmation, no dead-letter.

**Recommendation:** Return error for terminal targets. Queue with TTL for recovering targets. Add delivery receipt.

**Resolution:** Added dead-letter handling to Section 7.2 message delivery routing. Terminal targets (completed/failed/cancelled/expired) return `TARGET_TERMINAL` error immediately. Recovering targets (resume_pending/awaiting_client_action) queue messages in a DLQ with configurable TTL tied to `maxResumeWindowSeconds`. Added `deliveryReceipt` return value to all `send_message`/`send_to_child` calls with status `delivered|queued|error`. Added `message_expired` event for TTL-exceeded DLQ messages. Updated Section 8.5 tool table to reference delivery receipts.

### MSG-003. Agent Teams / Sibling Coordination No First-Class Support [High] — RESOLVED
**Section:** 8.5

Children can't discover siblings. No sibling messaging or broadcast mechanism.

**Recommendation:** Clarify `get_task_tree` child visibility. Consider `get_siblings()` or `broadcast(scope)` tools.

**Resolution:** Clarified in Section 8.5 that `get_task_tree` exposes sibling tasks (taskId, state, runtimeRef), enabling sibling discovery. Combined with `send_message` under `siblings` messaging scope (Section 7.2, added by SEC-003), this provides first-class sibling coordination without additional tools.

### MSG-004. Potential Deadlock in Circular request_input/await_children [High] — RESOLVED
**Section:** 8.9, 9.2

Multiple children in `input_required` with no re-await semantics. All-blocked subtree has no detection.

**Recommendation:** Specify re-await semantics. Document multi-child handling pattern. Add deadlock detection.

**Resolution:** Section 8.9 now specifies re-await semantics for multiple `input_required` children (stream yields independent partial results per child, no close/reopen needed). Added a multi-child handling pattern with example sequence. Added subtree deadlock detection: gateway detects all-blocked subtrees, emits `deadlock_detected` event, and fails deepest tasks with `DEADLOCK_TIMEOUT` after configurable `maxDeadlockWaitSeconds`. Section 9.2 updated to clarify the interaction between elicitation chains and `input_required` deadlock detection.

### MSG-005. `input_required` No Independent Timeout [Medium]
**Section:** 8.9

Child waiting for parent can block indefinitely, bounded only by `maxSessionAge`.

**Recommendation:** Add `maxInputWaitSeconds` timeout distinct from `maxElicitationWait`.

### MSG-006. SSE Buffer Overflow Reconnect Storm [Medium]
**Section:** 7.2

Slow clients repeatedly disconnect and reconnect.

**Recommendation:** Send `buffer_warning` at 80%. Consider agent backpressure. Implement reconnect backoff.

### MSG-007. `delivery: immediate` During Suspended State [Medium]
**Section:** 6.2, 7.2

Three delivery paths don't cover `suspended` state.

**Recommendation:** Add suspended as explicit delivery path case.

### MSG-008. No Turn Sequencing Guarantees [Medium]
**Section:** 15.4.1

Multiple rapid messages may reorder during failover. No sequence number.

**Recommendation:** Add monotonic `sequence` field to `MessageEnvelope`.

### MSG-009. `request_input` vs Elicitation Boundary Unclear [Medium]
**Section:** 8.5, 9.2

Root-level `request_input` behavior undefined.

**Recommendation:** Define request_input as inter-agent, elicitation as human-in-loop. Specify root-level behavior.

### MSG-010. Concurrent Mode `slotId` Messaging Under-Specified [Medium]
**Section:** 5.2, 15.4.1

Platform tools (`request_input`, `send_message`) interaction with `slotId` undefined.

**Recommendation:** All tools scoped per-task (mapped to slotId). Adapter routes by taskId->slotId.

### MSG-011. Reconnect `checkpoint_boundary` Under-Specified [Medium]
**Section:** 7.2

Schema, client handling, and replay window undefined.

**Recommendation:** Define schema (session state, active tasks, pending requests). Specify replay window.

### MSG-012. No Discovery of Pending `input_required` After Reconnect [Medium]
**Section:** 7.2

Client sees state change but may miss original request payload.

**Recommendation:** Include `pending_input_requests` in reconnection response.

### MSG-013. Message Delivery to Terminated/Draining Pods [Medium]
**Section:** 7.2

Gateway behavior for unreachable target pods undefined.

**Recommendation:** Reject for terminal/draining sessions. Queue for recovering sessions with TTL.

### MSG-014. Various Low Findings

Including: no elicitation ordering spec, `threadId` vestigial, one_shot + request_input inconsistency, send_to_child vs send_message redundancy, no output backpressure.

---

## 24. Policy Engine & Admission Control

### POL-001. Budget Propagation Lacks Atomic Reservation [Critical] — FIXED
**Section:** 8.3, 11.2

No specification of whether delegation budget slices are reserved or just ceilings. Parent can spawn children exceeding total budget. Default slice when `lease_slice` omitted undefined.

**Recommendation:** Atomic budget reservation via Redis `INCR` at delegation time. Default slice = remaining parent budget or configurable fraction. Return unused budget on completion.

**Status:** FIXED — Defined `LeaseSlice` type in Section 8.2. Added atomic reservation model in Section 8.3 with Redis `DECRBY`/`INCRBY`, default slice (50% remaining, configurable via `defaultDelegationFraction`), budget return on child completion, and concurrency safety via atomic operations. Added cross-reference in Section 11.2 linking delegation budget enforcement to the reservation model and confirming same durability model as token usage counters.

### POL-002. RequestInterceptor Chain Order/Short-Circuit Unspecified [High] — FIXED
**Section:** 4.8

No priority, no short-circuit behavior, no mutability, no timeout failure mode.

**Recommendation:** Explicit numeric priority. Rejection short-circuits. Timeout defaults to fail-closed. Document built-in ordering.

**Status:** FIXED — Added numeric `priority` field to interceptor registration (default 500, lower runs first). Documented built-in interceptor ordering table with default priorities (AuthEvaluator 100, QuotaEvaluator 200, ExperimentRouter 300, GuardrailsInterceptor 400). Specified short-circuit on REJECT and MODIFY payload propagation. Changed `failPolicy` default from fail-open to fail-closed. Added interceptor registration table with priority, failPolicy, and timeout fields.

### POL-003. Fail-Open Rate Limiting Per-Replica Only [High] — FIXED
**Section:** 12.4

Same as 2.6 / 4.4 — 10x effective limit with 10 replicas.

**Resolution:** Fixed via STO-001. Per-replica ceiling formula (`tenant_limit / replica_count`) bounds total cluster-wide usage to the tenant's configured budget.

### POL-004. Cross-Session Budget Enforcement Race [High] — FIXED
**Section:** 8.3

`maxTreeSize` and `maxTokenBudget` under concurrent delegation lack atomic enforcement.

**Recommendation:** Redis `INCR` for tree-size. Budget reservation model (not just ceiling).

**Resolution:** Section 8.3 already had atomic budget reservation via Redis DECRBY/INCRBY (from POL-001 fix) and tree-size INCR. The concurrency safety paragraph (step 4) was expanded to explicitly describe the tree-size overflow check: INCR, compare against maxTreeSize, rollback with DECR on overflow. Both token and tree-size rollbacks are coordinated on any single-delegation failure.

### POL-005. Quota Redis/Postgres Consistency Gap [High] — FIXED
**Section:** 11.2

60s sync = 60s stale data on crash recovery. Drift unbounded during fail-open.

**Recommendation:** Configurable sync interval (min 10s). Recover from pod-side usage on crash. Document max overshoot formula.

**Resolution:** Section 11.2 updated: sync interval is now configurable (`quotaSyncIntervalSeconds`, default 30s, min 10s). Added crash recovery paragraph — gateway reconstructs counters from Postgres checkpoints and pod-side `ReportUsage` cumulative totals on reconnection, taking the maximum. Added max overshoot formula for both normal operation and fail-open windows, with cross-reference to Section 12.4 per-replica ceiling and cumulative fail-open timer.

### POL-006. Timeout Table Incompleteness [Medium]
**Section:** 11.3

10+ operations with timeouts mentioned elsewhere but absent from the table.

**Recommendation:** Expand to cover all RPCs, network calls, and bounded computations.

### POL-007. Evaluator Ordering Not Specified [Medium]
**Section:** 4.8

Auth before/after quota? Pessimistic vs optimistic consumption?

**Recommendation:** Document ordered sequence. Commit quota only after all evaluators pass.

### POL-008. DelegationPolicy Intersection Lacks Formal Definition [Medium]
**Section:** 8.3

Multi-rule OR, include/exclude override, session-level `maxDelegationPolicy` composition undefined.

**Recommendation:** Formal per-rule subset requirement. Derived can add excludes only. Worked examples.

### POL-009. Lease Extension Across Delegation Depth [Medium]
**Section:** 8.6

Grandchild extension may exceed child's allocation. No cascading reservation check.

**Recommendation:** Extensions require cascading reservation up the tree. Cap if ancestor lacks capacity.

### POL-010. GuardrailsInterceptor Default-Disabled Gap [Medium]
**Section:** 4.8

No startup warning. Deployers may not realize no content filtering exists.

**Recommendation:** Startup warning. Security model note. Optional `requireGuardrails` flag.

### POL-011. Elicitation Budget Not Tree-Wide [Medium]
**Section:** 9.2

Per-session limit * tree size = potentially hundreds of user elicitations.

**Recommendation:** Add `maxElicitationsPerTree` on delegation lease.

### POL-012. AdmissionController Semantics Undefined [Medium]
**Section:** 4.8

Queue depth, priority, timeout, circuit breaker interaction all unspecified.

**Recommendation:** Define per-pool admission queue with fair-queuing. Distinguish from pod claim queue.

### POL-013. No Lease Extension Rate Limit in `auto` Mode [Medium]
**Section:** 8.6

Tight loop can exhaust `maxExtendableBudget` invisibly.

**Recommendation:** Add per-session rate limit. Add `maxExtensionsPerSession` counter. Alert on bursts.

### POL-014. Conflicting Credential Policies in Delegation [Medium]
**Section:** 8.3, 4.9

`inherit` vs child's own `CredentialPolicy` precedence undefined.

**Recommendation:** `inherit` = parent's resolved source. `independent` = child's policy from scratch. Document failures.

### POL-015. Various Low Findings

Including: env blocklist pattern rules, cross-env policy concurrent update, billing sequence Postgres write interaction, idle timer pause exploitation.

---

## 25. Execution Modes & Concurrent Workloads

### EXM-001. Task Mode Cleanup Lacks Concrete Failure Definitions [High] — FIXED
**Section:** 5.2

"Lenny scrub" undefined. `onCleanupFailure` behavior unspecified. Deployer acknowledgment mechanism missing.

**Recommendation:** Define explicit scrub procedure. Specify both failure mode behaviors. Define acknowledgment mechanism. Consider `replace` option.

**Resolution:** Defined 6-step Lenny scrub procedure (process kill, workspace removal, env var purge, tmp/shm clear, log truncation, verification). Specified `warn` (return to pool with annotation) and `fail` (terminate pod and replace) behaviors. Added `acknowledgeBestEffortScrub` required flag to `taskPolicy`. Updated all YAML examples for consistency.

### EXM-002. Concurrent-Workspace No Per-Slot Failure Semantics [High] — FIXED
**Section:** 5.2, 15.4.1

Slot failure, reclamation, checkpoint, resource contention all undefined.

**Recommendation:** Add "Concurrent Mode Internals" section with per-slot state machine, failure isolation, cleanup, checkpoint granularity.

**Resolution:** Added per-slot failure semantics inline in Section 5.2 concurrent-workspace mode: failure isolation (slot fails independently, pod continues), slot cleanup procedure with timeout, per-slot checkpoint granularity, and resource contention guidance with mode_factor degradation. Kept as concise addition rather than a separate section.

### EXM-003. Execution Mode / Warm Pool Interaction Underspecified [High] — FIXED
**Section:** 5.2, 4.6

Same as 16.3 — scaling formulas assume session mode.

**Resolution:** Duplicate of WAR-003. Fixed via "Execution Mode Scaling Implications" subsection in Section 5.2 and cross-reference in Section 4.6.2.

### EXM-004. Concurrent-Workspace Filesystem Layout Undefined [High] — FIXED
**Section:** 5.2, 6.4

Single `/workspace/current` shown but per-slot workspace structure never specified.

**Recommendation:** Define per-slot layout, cwd strategy, and adapter vs runtime responsibility for slot directories.

**Status:** FIXED — Added per-slot filesystem layout to Section 6.4: `/workspace/slots/{slotId}/current/` and `/workspace/slots/{slotId}/staging/` with per-slot `/sessions/{slotId}/` and `/artifacts/{slotId}/` directories. Defined adapter vs runtime vs gateway responsibility split for slot directory lifecycle, cwd assignment, and checkpoint export. Updated Section 5.2 concurrent-workspace description with cross-reference to Section 6.4 and explicit adapter responsibility for slot directory creation and cwd assignment.

### EXM-005. Concurrent-Stateless vs Connector Distinction Fuzzy [Medium]
**Section:** 5.2

"Should" vs "must" unclear. Criteria for "expensive shared state" undefined. K8s Service routing implications unexplored.

**Recommendation:** Enumerate criteria. Clarify LLM credential access and task lifecycle as distinguishing factors.

### EXM-006. Task Mode Lifecycle Race for Minimum-Tier [Medium]
**Section:** 5.2, 15.4.1

Minimum-tier runtimes have no lifecycle channel for between-task signaling.

**Recommendation:** Require lifecycle channel for task mode, or define fallback protocol. Reject task mode for incompatible runtimes.

### EXM-007. SDK-Warm Pod Destruction on Failed Claim [Medium]
**Section:** 6.1

Pod destroyed after pre-connecting SDK due to transient claim failure.

**Recommendation:** Acknowledge explicitly. Recommend pod-warm for claim-failure-sensitive deployments.

### EXM-008. No Execution Mode / Capability Validation Matrix [Medium]
**Section:** 5.2

Invalid combinations (one_shot + concurrent, etc.) accepted at registration.

**Recommendation:** Add compatibility matrix. Reject invalid combinations at registration.

### EXM-009. Concurrent Mode No Deployer Acknowledgment Mechanism [Medium]
**Section:** 5.2

Required but undefined. Stateless variant requires no acknowledgment despite different routing model.

**Recommendation:** Define flags for both concurrent variants.

### EXM-010. Task Mode Credential Lease Lifecycle [Medium]
**Section:** 5.2, 4.9

Per-pod or per-task? Cross-tenant task reuse with same credential?

**Recommendation:** Specify per-task leasing. Mandatory for cross-user scenarios.

### EXM-011. Suspended State in Non-Session Modes [Low]
**Section:** 6.2

Suspension semantics undefined for task and concurrent modes.

**Recommendation:** Clarify per-mode support. Define behavior for interrupt on non-session modes.

### EXM-012. Various Low Findings

Including: executionMode pool/runtime validation, graph mode migration guidance, build sequence concurrent testing gap.

---

## Cross-Cutting Themes

The 388 findings cluster around several systemic themes:

### 1. Specification Completeness (est. 80+ findings)
Many subsystems are described at architecture level but lack implementation-level detail: message schemas, state machine transitions, timeout values, error codes, failure behaviors. This is the most common finding type.

### 2. Multi-Tenancy Isolation Gaps (est. 30+ findings)
Postgres RLS is well-designed but Redis, MinIO, semantic cache, and event stores lack tenant isolation specifications. Task-mode cross-tenant pod reuse is the most acute risk.

### 3. Failure Mode Cascades (est. 25+ findings)
Individual component failure behaviors are partially defined, but multi-component failure scenarios (MinIO + eviction, Redis + Postgres, etcd + pod lifecycle) are not analyzed.

### 4. Build Sequence Risk Ordering (est. 15+ findings)
Security hardening and credential leasing come too late. Observability too late for debugging. No load testing phase. No parallelization.

### 5. Protocol Abstraction for Multi-Protocol (est. 15+ findings)
MCP semantics deeply embedded in core. ExternalProtocolAdapter interface too thin. Translation fidelity undocumented. Elicitation chain MCP-native.

### 6. Operational Readiness (est. 25+ findings)
No bootstrap mechanism, no preflight validation, no capacity planning, incomplete runbooks, missing alerts, duplicate billing schema.

### 7. Document Quality (est. 20+ findings)
Duplicate sections, numbering errors, missing cross-references, terms used before definition, 3700-line monolith needing extraction.
