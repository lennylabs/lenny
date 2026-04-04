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
| C1 | 1. K8s Infrastructure | `kubernetes-sigs/agent-sandbox` dependency — 5-month-old project with no stability guarantees as load-bearing CRD foundation | 4.6.1 |
| C2 | 2. Security | SIGSTOP/SIGCONT checkpoint mechanism unvalidated under gVisor and Kata | 4.4 |
| C3 | 4. Scalability | No concrete performance targets or capacity planning numbers at any scale tier | 16.5 |
| C4 | 4. Scalability | Gateway LLM reverse proxy as single throughput bottleneck for all LLM traffic | 4.9 |
| C5 | 6. Developer Experience | Insufficient standalone specification to build a Minimum-tier runtime | 15.4 |
| C6 | 8. Multi-Tenancy | Postgres RLS `SET app.current_tenant` race condition under PgBouncer transaction mode | 4.2, 12.3 |
| C7 | 13. Compliance | GDPR erasure flow incomplete — billing event immutability conflicts with deletion obligation | 12.8 |
| C8 | 15. Competitive | No differentiation narrative — "Why Lenny?" never articulated | 23 |
| C9 | 19. Build Sequence | Credential leasing (Phase 11) arrives too late for realistic integration testing | 18 |
| C10 | 19. Build Sequence | Security hardening (Phase 14) dangerously late — real credentials exposed without network isolation | 18 |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes Infrastructure & Controller Design

### C1. `kubernetes-sigs/agent-sandbox` Dependency Maturity and Lock-in Risk [Critical]
**Section:** 4.6.1

The entire pod lifecycle depends on a ~5-month-old upstream project (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim` CRDs). The project is almost certainly at `v1alpha1` stability with no breaking-change guarantees. The "pre-commit requirement" to verify optimistic-locking guarantees is noted but unresolved, with no fallback plan.

**Recommendation:** Define an internal `PodLifecycleManager` interface abstracting over agent-sandbox CRDs. Resolve the pre-commit requirement as an ADR before further design work. Pin to a specific release with a one-release-delay upgrade cadence. Document a fallback plan with estimated effort for a custom kubebuilder replacement.

### 1.1. Etcd Pressure Mitigations Insufficient at Scale [High]
**Section:** 4.6.1

At 1000 concurrent sessions with ~2-minute lifetimes, CRD status updates generate ~80+ writes/second — exceeding the 10 QPS rate limiter, causing unbounded queue growth. No etcd compaction/defrag guidance. The single rate limiter bucket starves pod creation during scale-up.

**Recommendation:** Use separate rate limiters for pod creation and status updates. Consider moving the authoritative state machine to Postgres for >500 sessions, using CRD status only for K8s-native concerns. Add etcd tuning guidance (compaction interval, defrag schedule, quota monitoring).

### 1.2. Controller Split Has Unclear CRD Ownership Boundary [High]
**Section:** 4.6.1, 4.6.2

Both WarmPoolController and PoolScalingController interact with the same CRD types. Leader election parameters differ or are unspecified. Manual `kubectl edit` of CRDs would be silently overwritten by PoolScalingController.

**Recommendation:** Document explicit CRD field ownership per controller. Use RBAC to enforce write boundaries. Define separate leader election leases. Add a validating webhook to prevent manual CRD edits that conflict with Postgres-authoritative state.

### 1.3. PodSecurityStandards Warn+Audit Without Enforce [High]
**Section:** 17.2

OPA/Gatekeeper policies stated as "must" but not reflected in Helm chart or build sequence. `shareProcessNamespace: false` requires separate policy not in build sequence. Policy/spec drift can cause warm pool deadlock.

**Recommendation:** Add an explicit build phase for admission policy deployment. Include manifests in Helm chart. Add integration tests verifying controller-generated pod specs pass admission policies. Use `enforce` for runc pods where seccomp concerns don't apply.

### 1.4. Namespace Layout Lacks Tenant-Level Isolation [Medium]
**Section:** 17.2

All tenants share `lenny-agents`. NetworkPolicy is not tenant-aware. Resource quotas cannot be enforced per-tenant at namespace level.

**Recommendation:** Recommend per-tenant namespaces for multi-tenant production. If deferred, require `microvm` isolation for multi-tenant workloads and document the shared-namespace risk.

### 1.5. PDB Label Inconsistency [Medium]
**Section:** 4.6.1, 6.2

PDB references `lenny.dev/pod-state: idle` but actual coarse labels use `lenny.dev/state: idle`. Node drain behavior with both idle and active pods undefined.

**Recommendation:** Fix label naming. Document drain behavior. Consider `maxUnavailable` instead of `minAvailable`.

### 1.6. SandboxClaim Orphan Risk Without ownerReference [Medium]
**Section:** 4.6.1

No ownerReference means no automatic GC. Gateway crash after SandboxClaim creation but before session setup leaves orphans.

**Recommendation:** Add reconciliation loop for orphaned SandboxClaims. Add TTL/lease mechanism.

### 1.7. Topology Spread Defaults Too Permissive [Medium]
**Section:** 5.2

`ScheduleAnyway` default allows single-zone concentration. Zone failure could eliminate the warm pool.

**Recommendation:** Change zone-level default to `DoNotSchedule` for pools with `minWarm >= 3`.

### 1.8. cert-manager as SPOF for Pod Identity [Medium]
**Section:** 10.3

Unavailability cascades: no new pods can join warm pool, expiring-cert pods are drained without replacement. No circuit breaker on cert-driven draining.

**Recommendation:** Add circuit breaker — pause cert-expiry draining during cert-manager outage. Extend cert TTL from 4h to 8h. Add `CertManagerUnavailable` critical alert.

### 1.9. No ResourceQuota/LimitRange for Agent Namespaces [Medium]
**Section:** 17.2

Bug or runaway scaling can create unbounded pods without namespace-level guardrails.

**Recommendation:** Add ResourceQuota and LimitRange per agent namespace, configurable via Helm.

### 1.10. Finalizer Stuck Alert Has No Automated Remediation [Low]
**Section:** 4.6.1

Manual finalizer removal is error-prone under pressure. Node failure causes burst of alerts needing manual intervention.

**Recommendation:** Auto-remove finalizer after 5-minute threshold when checkpoint is confirmed. Reserve manual path for unknown checkpoint status.

### 1.11. Controller Work Queue No Resync Period [Low]
**Section:** 4.6.1

Dropped reconciliation events with default 10h resync means stale state for hours.

**Recommendation:** Configure 30-60s resync period. Document overflow behavior.

### 1.12. No CRD Storage Overhead Estimation [Info]
**Section:** 4.6.1

At 1000+ concurrent sessions, CR storage in etcd could become significant.

**Recommendation:** Estimate per-CR storage size and document expected etcd requirements.

### 1.13. HPA Custom Metrics Pipeline Complexity [Info]
**Section:** 10.1

Three-hop pipeline (gateway->Prometheus->Adapter->HPA) introduces latency and failure points.

**Recommendation:** Default to KEDA if present. Document failure modes of the metrics pipeline.

---

## 2. Security & Threat Modeling

### C2. SIGSTOP/SIGCONT Checkpoint Under gVisor/Kata Unvalidated [Critical]
**Section:** 4.4

With `shareProcessNamespace: false`, the adapter cannot directly signal the agent process. gVisor and Kata have different signal delivery semantics. Inconsistent checkpoints could corrupt session state on resume.

**Recommendation:** Validate in PoC before committing. Clarify cooperative checkpoint via lifecycle channel is the only reliable path under sandboxed/microvm. Document that Minimum-tier runtimes cannot produce consistent checkpoints.

### 2.1. Adapter-Agent Shared emptyDir Volume Escape [High]
**Section:** 4.7

Malicious agent can write arbitrary files to shared volume. Adapter manifest on shared path creates tamper/race risks.

**Recommendation:** Separate volumes for socket, workspace, and manifest. Mount manifest read-only into agent container. Consider abstract Unix sockets.

### 2.2. Prompt Injection via Delegation Chains [High]
**Section:** 8

Gateway validates structure but not content. Compromised parent can craft malicious `TaskSpec.input`. `GuardrailsInterceptor` disabled by default.

**Recommendation:** Add `contentPolicy` to `DelegationPolicy`. Restrict `lenny/send_message` to direct children/parent. Rate-limit message injection. Warn deployers about prompt injection risk without guardrails.

### 2.3. Credential Material via Environment Variables [High]
**Section:** 4.7

Env vars are weak for credential delivery — readable via `/proc`, persist in crash dumps, often logged by frameworks.

**Recommendation:** Deliver via tmpfs file (mode 0400) instead. Recommend proxy mode as default for multi-tenant. Clear credential env var after agent reads it.

### 2.4. Isolation Monotonicity Gap for Task Mode Pod Reuse [High]
**Section:** 5.2, 8.3

Task-mode pod reuse between different tenants has no isolation boundary. Residual data in memory, caches, `/dev/shm` can leak.

**Recommendation:** Enforce tenant pinning — task-mode pods never reuse across tenants. Require `microvm` for any cross-tenant reuse consideration.

### 2.5. DNS Exfiltration via DoH/DoT Bypass [Medium]
**Section:** 13.2

Pods with `internet` egress can bypass dedicated CoreDNS via DNS-over-HTTPS on port 443.

**Recommendation:** Document as known residual risk for `internet` profile. Add subdomain query length limits. Consider egress proxy for `internet` pods.

### 2.6. Rate Limit Fail-Open Exploitable in Multi-Tenant [Medium]
**Section:** 12.4

10 replicas = 10x effective rate limit during Redis outage. Attacker could intentionally trigger Redis exhaustion.

**Recommendation:** Reduce default fail-open to 15s. Apply per-replica limit as `user_limit / N`. Add automatic session creation blocking option for security-critical deployments.

### 2.7. Semantic Cache Poisoning Across Tenants [Medium]
**Section:** 4.9

Shared credential pool with semantic caching can leak responses between tenants.

**Recommendation:** Mandate `tenant_id` as cache key prefix. Enforce in `CachePolicy` schema.

### 2.8. Elicitation Chain Phishing via Deep Delegation [Medium]
**Section:** 9.2

OAuth flows exempt from depth suppression. Compromised connector OAuth endpoint creates phishing risk.

**Recommendation:** Add `maxConnectorElicitationDepth`. Validate `redirect_uri` against registered callback URL.

### 2.9. RLS Bypass During Schema Migrations [Medium]
**Section:** 4.2, 10.5

Migration role may bypass RLS during DML operations, corrupting tenant isolation.

**Recommendation:** Migration role should have DDL-only privileges. DML in migrations must set `app.current_tenant`. Add CI check for DML in tenant-scoped tables.

### 2.10. Task Mode Workspace Scrub Undefined [Medium]
**Section:** 5.2

"Best-effort scrub" never defined. No specification of what is cleaned or failure behavior.

**Recommendation:** Define explicit scrub procedure. Include runtime restart between tasks. Document data residual risks.

### 2.11. `runtimeOptions` Passthrough Without Validation [Medium]
**Section:** 14

64KB of attacker-controlled JSON with no schema requirement in production. Deserialization attack surface.

**Recommendation:** Require `runtimeOptionsSchema` for multi-tenant. Apply structural validation (depth, string length, array size).

### 2.12. `callbackSecret` Lifetime Uncontrolled [Medium]
**Section:** 14

No max lifetime, no rotation mechanism, no minimum entropy. Encrypted storage compromise exposes all secrets.

**Recommendation:** Generate server-side. Set max lifetime tied to session TTL. Delete after webhook delivery retries exhausted.

### 2.13. LLM Proxy Uses HTTP Not HTTPS [Medium]
**Section:** 4.9

Lease token transmitted in cleartext over pod-to-gateway network. No mutual authentication.

**Recommendation:** Require mTLS on proxy endpoint. Bind lease token to pod SPIFFE identity. Fix example URL scheme.

### 2.14. Resource Exhaustion via Delegation Trees [Medium]
**Section:** 8.3, 11.1

Unclear if child sessions count toward parent user's concurrency limit. Single user could consume hundreds of pods.

**Recommendation:** Delegated children must count toward originating user's concurrency. Add `maxTotalPodsPerUser` and `maxTotalPodsPerTenant` quotas.

### 2.15. Audit Log Startup Check Too Weak [Low]
**Section:** 11.7

Only warns on UPDATE/DELETE grants, doesn't refuse to start. Grants added mid-session undetected until restart.

**Recommendation:** Make startup check fail-hard in production. Run grants check periodically (every 5 min). Alert on drift.

### 2.16. `publishedMetadata` XSS Vector [Low]
**Section:** 5.1

Public metadata with HTML/JS content served without sanitization could enable stored XSS.

**Recommendation:** Serve with `application/octet-stream`, `nosniff`, and `Content-Disposition: attachment`.

### 2.17. SSE Connection Exhaustion [Low]
**Section:** 7.2

No per-user concurrent stream limit. Malicious client can exhaust file descriptors.

**Recommendation:** Add per-user max concurrent streams (e.g., 5). Add minimum read rate requirement.

### 2.18. Concurrent Execution Cross-Slot Isolation [Info]
**Section:** 5.2

Process-level isolation between slots explicitly weak. Correctly flagged, needs operational guidance.

**Recommendation:** Enforce same-user constraint on slot assignment. Document per-slot resource contention.

---

## 3. Network Security & Isolation

### 3.1. Missing NetworkPolicies for `lenny-agents-kata` Namespace [High]
**Section:** 13.2, 17.2

All three NetworkPolicy manifests apply only to `lenny-agents`. `lenny-agents-kata` defaults to allow-all.

**Recommendation:** Templatize NetworkPolicies in Helm chart to apply to all agent namespaces.

### 3.2. Lateral Movement via `internet` Egress Profile [High]
**Section:** 13.2

`0.0.0.0/0` includes pod CIDR. Agent pods with `internet` profile can reach other agent pods.

**Recommendation:** Exclude cluster pod/service CIDRs from `internet` profile. Require gVisor/Kata isolation for `internet` profile.

### 3.3. Direct Credential Mode Bypasses Gateway Data Plane [High]
**Section:** 4.9

`direct` mode with `provider-direct` egress creates a network path bypassing the gateway, violating gateway-centric principle.

**Recommendation:** Document `proxy` as recommended default. Add monitoring channel for direct-mode usage. Flag `direct` + `runc` as dangerous combination.

### 3.4. Gateway Egress Rule Lacks Port Restrictions [Medium]
**Section:** 13.2

Agent pods can reach gateway on any port, including admin and metrics endpoints.

**Recommendation:** Add explicit `ports` constraints limiting to gRPC control port and LLM proxy port.

### 3.5. DNS Exfiltration Mitigation Gaps [Medium]
**Section:** 13.2

CNAME chaining, query length limits, domain allowlists, and HA for dedicated CoreDNS not addressed.

**Recommendation:** Add response-size limits across all record types. Add query-name-length limits. Document HA requirements. Support DNS domain allowlist for high-security.

### 3.6. Certificate Revocation Propagation Delay [Medium]
**Section:** 10.3

Redis pub/sub is not guaranteed delivery. Postgres LISTEN/NOTIFY also lossy. No deny-list persistence across restarts.

**Recommendation:** Populate deny list from Postgres on startup. Use Redis Streams instead of pub/sub. Add revocation propagation latency metric.

### 3.7. Token Service Ingress Not NetworkPolicy-Restricted [Medium]
**Section:** 4.3

No NetworkPolicy restricts which pods can reach the Token Service. Agent pods with `internet` profile could attempt connection.

**Recommendation:** Add NetworkPolicy restricting Token Service ingress to gateway pods only. Validate SAN/SPIFFE URI of incoming connections.

### 3.8. Callback Worker SSRF NetworkPolicy Architecturally Impossible [Medium]
**Section:** 14

Callback HTTP requests are from gateway goroutines, not separate pods. Cannot apply different NetworkPolicies to goroutines.

**Recommendation:** Extract callback worker to separate Deployment with restrictive NetworkPolicy, or rely on application-level SSRF controls and document as limitation.

### 3.9. `type: mcp` Proxy Lacks Inline Policy Enforcement [Medium]
**Section:** 15

Unclear which policy controls apply to proxied MCP tool calls.

**Recommendation:** Document that Environment `mcpRuntimeFilters` apply to every proxied tool call, not just at discovery.

### 3.10. Setup Command Network Blocking Only True for `restricted` Profile [Medium]
**Section:** 7.5, 13.2

"Network blocked during setup" is only true for `restricted` egress profile. Other profiles have full access during setup.

**Recommendation:** Clarify the limitation. Consider two-phase label approach for per-phase NetworkPolicies.

### 3.11. No Egress Restriction on `lenny-system` Namespace [Medium]
**Section:** 13.2

Gateway, Token Service, and controllers can make arbitrary outbound connections.

**Recommendation:** Apply least-privilege egress per component (Token Service: only Postgres/KMS/Redis; Controller: only API server/Postgres).

### 3.12. No Internal Segmentation Within `lenny-system` [Low]
**Section:** 17.2

Compromised gateway has unrestricted access to all `lenny-system` components.

**Recommendation:** Apply defense-in-depth NetworkPolicies: only PgBouncer reaches Postgres, only gateway reaches PgBouncer, etc.

### 3.13. Pod Does Not Verify Gateway Identity [Low]
**Section:** 10.3

Pod-initiated gRPC connection but no explicit server certificate verification.

**Recommendation:** Specify that adapter validates gateway mTLS certificate SAN.

### 3.14. Deny-List Mechanisms Share Redis Pub/Sub Failure Mode [Info]
**Section:** 10.3, 11.4

Certificate and credential deny lists both depend on Redis pub/sub.

**Recommendation:** Consolidate into single reliable delivery channel. Document shared failure mode.

---

## 4. Scalability & Performance Engineering

### C3. No Concrete Performance Targets [Critical]
**Section:** 16.5

SLO targets stated without specifying at what scale. No max concurrent sessions, sessions/second, delegation throughput, tenant count, or gateway RPS targets.

**Recommendation:** Define capacity tiers (100/1000/10000 concurrent sessions) with infrastructure sizing. Run capacity modeling. This becomes the basis for all performance decisions.

### C4. Gateway LLM Reverse Proxy Bottleneck [Critical]
**Section:** 4.9

500 concurrent sessions in proxy mode = 1000+ LLM proxy requests/second through gateway, with streaming responses. No subsystem isolation for proxy path.

**Recommendation:** Design LLM proxy as independently scalable component from day one. Add dedicated HPA metrics, goroutine pool, and circuit breaker. Consider sidecar proxy as alternative.

### 4.1. Startup Latency Based on Estimates, Not Benchmarks [High]
**Section:** 6.3

Competitors cite 150-300ms. Lenny's estimates are in seconds with unknown hot-path additions.

**Recommendation:** Establish benchmarks per phase. Build benchmark harness in Phase 2. Measure SDK-warm vs pod-warm to validate complexity tradeoff.

### 4.2. Redis as Scalability Ceiling [High]
**Section:** 12.4

Per-session leases, per-request quota increments, and pub/sub on single Sentinel topology. No analysis of when Sentinel becomes insufficient.

**Recommendation:** Separate Redis into ephemeral coordination and quota enforcement. Consider in-memory budgets with Postgres reconciliation for high-value limits.

### 4.3. HPA Scaling Lag for Gateway [High]
**Section:** 10.1

30-60s minimum lag between spike and new replica. Bursty workloads exhaust existing replicas.

**Recommendation:** Aggressive scale-up policies. Leading metrics (queue depth, rejection rate). Higher `minReplicas` for burst absorption.

### 4.4. Checkpoint SIGSTOP Duration Impact [High]
**Section:** 4.4

500MB workspace tar+upload during SIGSTOP could pause agent for 5-10+ seconds per checkpoint.

**Recommendation:** Benchmark checkpoint duration. Set SLO (<2s for <100MB). Consider copy-on-write snapshots or incremental checkpoints.

### 4.5. Postgres Connection Pressure Under Scale [High]
**Section:** 12.3

Multiple components hitting Postgres: quota checkpoints, audit inserts, billing writes, session state updates. Write amplification not analyzed.

**Recommendation:** Estimate write IOPS at capacity tiers. Batch audit/billing writes. Consider separate Postgres for write-heavy paths.

### 4.6. Delegation Tree Unbounded Gateway Memory [High]
**Section:** 8.2

50-node tree with virtual child interfaces, event buffers, and elicitation state in single replica memory.

**Recommendation:** Calculate per-node memory footprint. Set `maxTreeMemoryBytes`. Offload completed subtree results to Postgres.

### 4.7. Warm Pool Claim Contention Under Burst [Medium]
**Section:** 4.6.1

10 replicas * 50 claims = 500 API server attempts, 450 failures from optimistic locking conflicts.

**Recommendation:** Consider Redis-based pre-claim or claim broker pattern. Benchmark claim success rate at various burst levels.

### 4.8. Elicitation Chain Latency at Depth [Medium]
**Section:** 9.2

5 hops = 150s worst-case forwarding latency before user sees elicitation.

**Recommendation:** Consider gateway shortcut path (O(1) vs O(depth)). Document expected latency per depth.

### 4.9. Per-Session Coordination Lease TTL Gap [Medium]
**Section:** 10.1

Unspecified TTL means undefined session stall duration during replica failure.

**Recommendation:** Document TTL (10-15s). Add session heartbeat from gateway to client for proactive reconnection.

### 4.10. MinIO Upload/Download Bottleneck via Gateway Proxy [Medium]
**Section:** 12.5

100 sessions checkpointing 100MB simultaneously = 10GB through gateway.

**Recommendation:** Consider pre-signed URLs for direct pod-to-MinIO checkpoint uploads.

### 4.11. No Back-Pressure on Session Creation [Medium]
**Section:** 11.1

No queuing during warm pool exhaustion. Burst requests fail while system scales up.

**Recommendation:** Implement bounded admission queue. Emit queue depth metrics for HPA.

### 4.12. Semantic Cache Memory/Latency Overhead [Medium]
**Section:** 4.9

100K entries = ~300MB Redis memory. Embedding computation adds 10-50ms per lookup.

**Recommendation:** Document overhead. Consider dedicated Redis or pgvector. Specify embedding model and computation location.

### 4.13. No Gateway Load Shedding Strategy [Medium]
**Section:** 11.6

Overloaded gateway degrades all sessions rather than maintaining quality for a subset.

**Recommendation:** Implement adaptive load shedding. Prioritize existing sessions over new creation.

### 4.14. Controller Queue Overflow Drops Events [Medium]
**Section:** 4.6.1

Default 500 depth, default 10h resync. Dropped events unprocessed for hours.

**Recommendation:** 5-10min resync period. Alert on overflow. Analyze queue depth at target capacity.

### 4.15. Billing Event Synchronous Write on Hot Path [Medium]
**Section:** 11.2.1, 11.8

Every session lifecycle operation performs synchronous Postgres INSERT.

**Recommendation:** Consider local write-ahead buffer or batch multi-row inserts to amortize overhead.

### 4.16. Missing Concurrent Mode Scaling Analysis [Low]
**Section:** 5.2

8 concurrent bursty LLM workloads sharing CPU/memory with no contention analysis.

**Recommendation:** Provide per-slot sizing guidance. Benchmark at various slot counts.

### 4.17. Duplicate Billing Event Stream Definitions [Info]
**Section:** 11.2.1, 11.8

Identical topic with different field names (`tokens_in`/`tokens_out` vs `tokens_input`/`tokens_output`, `token.checkpoint` vs `token_usage.checkpoint`).

**Recommendation:** Consolidate into single authoritative section.

---

## 5. Protocol Design & Future-Proofing

### 5.1. `ExternalProtocolAdapter` Interface Too Thin for Stateful Protocols [High]
**Section:** 15

Only `HandleInbound`, `HandleDiscovery`, `Capabilities`. No lifecycle hooks for A2A's task lifecycle or push notifications.

**Recommendation:** Extend with `OnSessionCreated`, `OnSessionEvent`, `OnSessionTerminated`. Add `OutboundCapabilities` declaration.

### 5.2. MCP Task Semantics Hardcoded into Core [High]
**Section:** 7, 8, 9.2

Session lifecycle, task tree, and delegation deeply structured around MCP Tasks. A2A's `canceled`/`unknown` states and artifact model unmapped. No inbound A2A task entry path.

**Recommendation:** Define canonical task state machine as Lenny-native. Add `canceled` state now. Define inbound A2A task mapping to session creation.

### 5.3. MCP Spec Version Pinning Missing [High]
**Section:** 15

No target MCP version specified. No version negotiation. No compatibility matrix for client/server version mismatch.

**Recommendation:** Pin target MCP version. Add version negotiation to `MCPAdapter`. Support two concurrent versions.

### 5.4. `publishedMetadata` Lacks Schema Validation [Medium]
**Section:** 5.1

Opaque pass-through with no validation. Malformed A2A agent cards served to clients.

**Recommendation:** Add optional `schemaRef` field. Validate at write time when present. Add `version` field.

### 5.5. `OutputPart` Lossy Translation Undocumented [Medium]
**Section:** 15.4.1

Round-trip through multiple adapters causes information loss. No fidelity contract.

**Recommendation:** Define translation tables per adapter. Add `protocolHints` in annotations for round-trip fidelity.

### 5.6. Elicitation Chain MCP-Native Without Abstraction [Medium]
**Section:** 9.2

No translation path for elicitation via OpenAI or A2A adapters.

**Recommendation:** Abstract into protocol-agnostic "human input request" primitive. Define per-adapter surfacing.

### 5.7. A2A Outbound Delegation Authentication Undefined [Medium]
**Section:** 21.1

`ConnectorDefinition` doesn't account for A2A auth patterns (mutual auth, bearer tokens).

**Recommendation:** Extend `ConnectorDefinition` with `protocol` field and protocol-specific auth configs.

### 5.8. Discovery Format Fragmentation [Medium]
**Section:** 9.1

Four different discovery formats (MCP, REST, OpenAI, future A2A). Dynamic discovery data not in `publishedMetadata`.

**Recommendation:** Introduce `DiscoveryProjection` per adapter. Add `RuntimeStatus` cache for dynamic data.

### 5.9. Binary Protocol No Version Negotiation [Medium]
**Section:** 15.4.1

stdin/stdout protocol has no handshake. Unknown message types silently ignored on primary data channel.

**Recommendation:** Add `init`/`init_ack` handshake as first message on stdin.

### 5.10. `type: mcp` Runtimes Tightly Coupled to MCP Transport [Low]
**Section:** 15

No mechanism to expose MCP runtime tools through other protocol adapters.

**Recommendation:** Consider tool-bridging in `ExternalAdapterRegistry`. Make tool previews protocol-agnostic.

### 5.11. Delegation Lease `allowedExternalEndpoints` Untyped [Low]
**Section:** 8.3

Empty array with no schema. Will require breaking migration when A2A ships.

**Recommendation:** Define structured schema now: `[{ protocol, endpoint, connectorRef, matchPattern }]`.

### 5.12. No Protocol Health Probing for Adapters [Low]
**Section:** 15

No mechanism to detect unhealthy adapters or unsupported protocol features.

**Recommendation:** Add `HealthCheck` method to adapter interface.

### 5.13. `MessageEnvelope.from.kind` Insufficient for Multi-Protocol [Low]
**Section:** 15.4.1

`external` doesn't distinguish A2A vs AP vs MCP origin.

**Recommendation:** Add `from.protocol` field alongside `from.kind`.

---

## 6. Developer Experience (Runtime Authors)

### C5. Insufficient Specification for Minimum-Tier Runtime [Critical]
**Section:** 15.4.1, 15.4.3

Message schemas (inbound/outbound), heartbeat format, shutdown expectations, and exit codes all undefined. Echo runtime is prose description only. A developer cannot build a runtime from the spec alone.

**Recommendation:** Add Protocol Reference with complete JSON schemas for every message type. Include annotated protocol trace. Provide echo runtime as actual code or detailed pseudocode.

### 6.1. OutputPart Complexity for Minimum-Tier [High]
**Section:** 15.4.1

Minimum-tier runtimes must produce `OutputPart[]` but minimal valid form undefined. `from_mcp_content` helper implies SDK dependency contradicting "zero knowledge" promise.

**Recommendation:** Allow simplified response shorthand (`{type: "response", text: "hello"}`). Document minimal required fields. Make SDK helpers explicitly optional.

### 6.2. Degraded Experience for Minimum-Tier Poorly Documented [High]
**Section:** 4.7, 15.4.3

"Fallback-only mode" never fully enumerated. Scattered implications: no checkpoint, no interrupt, no credential rotation, no deadline warning.

**Recommendation:** Add Tier Comparison Matrix listing every capability by tier with fallback behaviors.

### 6.3. tool_call/tool_result Format Unspecified [High]
**Section:** 15.4.1

No JSON schema for tool calls or results. No correlation mechanism. Sync vs async delivery undefined. Minimum-tier tool access unclear.

**Recommendation:** Add complete schemas with correlation mechanism. Clarify tool access per tier.

### 6.4. No Quickstart Path or Tutorial Structure [High]
**Section:** 15.4

Relevant runtime-author info scattered across 7+ sections in a 3700-line doc.

**Recommendation:** Create separate "Runtime Author Guide" with tutorial-order presentation.

### 6.5. Adapter Manifest Discovery Underspecified [Medium]
**Section:** 4.7

Path not documented as env var or constant. Schema shown only as example. Minimum-tier reading requirements unclear.

**Recommendation:** Define `LENNY_ADAPTER_MANIFEST` env var. Provide formal JSON schema. State Minimum-tier doesn't need manifest.

### 6.6. Two Overlapping Shutdown Mechanisms [Medium]
**Section:** 4.7, 15.4.1

stdin `{type: "shutdown"}` and lifecycle channel `terminate` overlap for Full-tier runtimes.

**Recommendation:** Clarify per-tier shutdown protocol. Document which signal is authoritative.

### 6.7. Echo Runtime Insufficient as Reference Implementation [Medium]
**Section:** 15.4.4

Only covers Minimum-tier. No reference for Standard/Full-tier integration points.

**Recommendation:** Provide at least two reference implementations (Minimum echo + Standard tool-calling).

### 6.8. No Runtime Image Packaging Guidance [Medium]
**Section:** 4.7

No base image expectations, UID requirements clarification, filesystem layout creation responsibilities.

**Recommendation:** Add "Runtime Image Requirements" section covering UID/GID, writable paths, sidecar injection model.

### 6.9. Credential Delivery Environment Variables Unclear [Medium]
**Section:** 4.7, 14

Env var names unspecified. Apparent contradiction with env blocklist.

**Recommendation:** Document exact env var names. Clarify blocklist applies to client-provided vars, not platform-injected credentials.

### 6.10. No Error Handling Guidance for Runtime Authors [Medium]
**Section:** 16.3

Error categories defined but no guidance on how runtimes report errors (stdout format, exit codes, stderr handling).

**Recommendation:** Add "Error Reporting" section with schema for error responses, exit code mapping, and stderr treatment.

### 6.11. SDK Minimization Goal at Risk for Standard Tier [Medium]
**Section:** 15.4.1

Standard tier requires MCP client protocol implementation without library support.

**Recommendation:** Provide lightweight runtime-side MCP client library (Go + one other language) or document wire-level MCP patterns.

### 6.12. Lifecycle Channel Transport Mechanism Unspecified [Medium]
**Section:** 4.7

"Separate stdin/stdout stream pair" never explained mechanically. How do two stdin/stdout pairs coexist?

**Recommendation:** Specify transport (likely separate Unix socket). Add to adapter manifest. Provide JSON schemas.

### 6.13. `type: mcp` Runtime Implementation Unclear [Medium]
**Section:** 5.1, 15

Not clear if it's "bring any MCP server" or requires adapter compliance.

**Recommendation:** Add dedicated subsection for `type: mcp` requirements.

### 6.14. MessageEnvelope Complexity for Minimum-Tier [Low]
**Section:** 15.4.1

Full envelope on stdin strains "zero knowledge" claim.

**Recommendation:** Have adapter strip envelope for Minimum-tier, delivering only essential payload.

### 6.15. Concurrent-Workspace slotId Thinly Described [Low]
**Section:** 15.4.1

No slot lifecycle, acknowledgment, or capacity discovery.

**Recommendation:** Mark as requiring Full-tier integration. Note protocol will be fully specified separately.

### 6.16. Runtime Registration Admin-Only [Low]
**Section:** 5.1, 10.2

No self-service path for community developers.

**Recommendation:** Consider dev-mode sandbox registration without platform-admin access.

### 6.17. Duplicate Billing Event Sections [Info]
**Section:** 11.2.1, 11.8

Same finding as 4.17.

---

## 7. Operator & Deployer Experience

### 7.1. No Bootstrap Seed Mechanism for Day-1 [High]
**Section:** 15.1

After `helm install`, Postgres is empty. No runtimes, pools, or credentials. Operator must manually call dozens of API endpoints.

**Recommendation:** Define bootstrap seed mechanism (Helm values section, init ConfigMap, or `lenny-ctl bootstrap` CLI). Make idempotent.

### 7.2. No Preflight Validation for Infrastructure Dependencies [High]
**Section:** 12.3, 12.4, 12.5

Missing prerequisite (wrong PgBouncer mode, no CNI support, etc.) causes cryptic failures.

**Recommendation:** Add `lenny-preflight` Job validating Postgres, PgBouncer, Redis, MinIO, RuntimeClasses, cert-manager, CNI support.

### 7.3. Helm Upgrade Doesn't Update CRDs [High]
**Section:** 10.5, 17.6

Helm's known CRD limitation. Silently stale CRDs cause production incidents.

**Recommendation:** Document separate CRD application before `helm upgrade`. Add startup check validating CRD schema version.

### 7.4. SQLite in Tier 1 Dev Mode Behavioral Divergence [Medium]
**Section:** 17.4

RLS, advisory locks, LISTEN/NOTIFY, pgvector all unavailable in SQLite.

**Recommendation:** Acknowledge limitation explicitly. Consider embedded Postgres. Add CI testing against both backends.

### 7.5. Expand-Contract Migration Needs Practical Guardrails [Medium]
**Section:** 10.5

No dry-run, no CI backward-compat testing, no three-release cadence documentation.

**Recommendation:** Add CI step running previous version tests against new schema. Add `--dry-run` flag. Document release cadence.

### 7.6. Operational Runbooks Only Listed, Not Specified [Medium]
**Section:** 17.7

Seven runbooks listed as "must ship" with no structure, steps, or alert linkage.

**Recommendation:** Define per-runbook: triggering alert, diagnostic commands, remediation steps, escalation, verification.

### 7.7. No CRD/Postgres Drift Detection [Medium]
**Section:** 4.6.2

Manual kubectl edits or stale controller create silent drift. No GitOps annotation guidance.

**Recommendation:** Add `lenny_controller_crd_drift` metric. Document GitOps managed-by annotations. Add "reconcile now" endpoint.

### 7.8. PgBouncer Transaction-Mode RLS Interaction [Medium]
**Section:** 12.3

`SET` outside explicit transaction can leak to next borrower. Advisory locks are session-scoped, wrong for transaction-mode.

**Recommendation:** Specify `server_reset_query = DISCARD ALL`. Always use `SET LOCAL` inside transactions. Use `pg_advisory_xact_lock` for migrations.

### 7.9. Token Service SPOF for New Sessions [Medium]
**Section:** 4.3

No health endpoint, no Section 16.5 alert, no circuit breaker parameters specified.

**Recommendation:** Add `TokenServiceUnavailable` critical alert. Specify health check endpoints. Define circuit breaker parameters.

### 7.10. No Credential Pool Key Rotation Procedure [Medium]
**Section:** 4.9, 10.5

No procedure for adding new key, draining old, removing. No `draining`/`disabled` status per credential.

**Recommendation:** Add per-credential `status` field (active/draining/disabled). Expose via admin API. Add to runbooks.

### 7.11. Helm Values Surface Underspecified [Medium]
**Section:** 17.6

Five sample values for a system needing dozens of configuration points.

**Recommendation:** Define complete `values.yaml` skeleton. Provide "minimal production" example.

### 7.12. No Capacity Planning Guidance [Medium]
**Section:** various

No synthesis of resource requirements into deployer-facing sizing guidance.

**Recommendation:** Add T-shirt-sized reference architectures (Small/Medium/Large) with infrastructure estimates.

### 7.13. Smoke Test Doesn't Cover Critical Path [Low]
**Section:** 17.4

Only tests echo runtime happy path. Missing: upload, setup, checkpoint, credential, delegation.

**Recommendation:** Add `integration` test level covering upload, setup, checkpoint/resume, single-level delegation.

### 7.14. Observability Opt-In and Underdocumented in Dev Mode [Low]
**Section:** 17.4

Pre-built Grafana dashboard mentioned but not specified.

**Recommendation:** Ship dashboard JSON as checked-in artifact. Make observability default in Tier 2.

### 7.15. Controller Failover Sizing Assumes Steady-State [Low]
**Section:** 4.6.1

Formula doesn't account for burst patterns.

**Recommendation:** Present formula as lower bound. Add burst sizing guidance.

### 7.16. Duplicate Billing Event Stream [Medium]
**Section:** 11.2.1, 11.8

Same finding — consolidate into single section.

---

## 8. Multi-Tenancy & Tenant Isolation

### C6. Postgres RLS Race Condition Under PgBouncer Transaction Mode [Critical]
**Section:** 4.2, 12.3

`SET app.current_tenant` outside explicit transaction can execute on one connection while the next query uses a different connection where the tenant was never set (or set by prior tenant).

**Recommendation:** Mandate `BEGIN; SET LOCAL app.current_tenant = '<id>'; ... COMMIT;` — `SET LOCAL` is explicitly transaction-scoped. RLS policy must use `current_setting('app.current_tenant', false)` to error on unset. Add startup integration test verifying isolation. Set sentinel value on connection init.

### 8.1. Redis Tenant Isolation Absent [High]
**Section:** 12.2, 12.4

No key-naming convention with tenant prefix. Cache poisoning and routing cross-contamination risk.

**Recommendation:** Mandate `t:{tenant_id}:` key prefix convention. Document as implementation requirement. Add integration test.

### 8.2. MinIO Tenant Isolation Unspecified [High]
**Section:** 4.5, 12.5

No per-tenant bucket or path-based isolation strategy. `DeleteByTenant` implies scoping but mechanism undefined.

**Recommendation:** Specify path-based isolation with mandatory `tenant_id` prefix validation at `ArtifactStore` interface level.

### 8.3. Tenant Deletion Path Incomplete [High]
**Section:** 12.8

No active session termination, credential revocation, Redis cleanup, environment teardown, CRD cleanup, ordering, or legal hold interaction specified.

**Recommendation:** Define full tenant deletion lifecycle with phases (soft-disable -> terminate sessions -> revoke credentials -> delete data -> clean CRDs -> produce receipt). Add `TenantState` enum.

### 8.4. Task-Mode Pod Reuse Cross-Tenant Risk [High]
**Section:** 5.2

No explicit prohibition of cross-tenant task-mode pod reuse.

**Recommendation:** Add explicit statement: task-mode pods NEVER reuse across tenants. Enforce in gateway task assignment logic.

### 8.5. Three-Role RBAC Lacks Granularity [Medium]
**Section:** 10.2

No billing-admin, read-only user, or custom roles.

**Recommendation:** Add `tenant-viewer` role. Document extensibility path. Enumerate per-role permissions.

### 8.6. `noEnvironmentPolicy` Semantics Unclear in Multi-Tenant [Medium]
**Section:** 10.6

`allow-all` meaning unclear — all runtimes system-wide or tenant-scoped? Security implications undocumented.

**Recommendation:** Clarify as tenant-scoped. Warn against `allow-all` in multi-tenant. Rename to `unmatchedUserRuntimeAccess`.

### 8.7. Event/Checkpoint Store Missing RLS Confirmation [Medium]
**Section:** 4.4

RLS not explicitly confirmed for EventStore tables.

**Recommendation:** Confirm RLS coverage. Validate tenant ownership in checkpoint artifact references.

### 8.8. MemoryStore Tenant Isolation Application-Only [Medium]
**Section:** 9.4

No interface contract requiring tenant isolation. Custom plugins could leak memories.

**Recommendation:** Add tenant isolation as interface contract requirement. Mandate RLS on default implementation. Add integration tests.

### 8.9. Credential Pool Scoping Not Tied to Tenant [Medium]
**Section:** 4.9

Unclear if pools are global or tenant-scoped. Noisy-neighbor risk.

**Recommendation:** Specify scoping model. Add per-tenant limits if shared. Ensure lease records include tenant_id under RLS.

### 8.10. Semantic Cache Lacks Tenant Partitioning [Medium]
**Section:** 4.9

Cross-tenant cache hits possible. Same as 2.7.

### 8.11. Cross-Environment Delegation May Span Tenants [Medium]
**Section:** 10.6

No constraint preventing cross-tenant cross-environment delegation.

**Recommendation:** Add explicit constraint: cross-environment delegation only within same tenant.

### 8.12. Tenant Identity from OIDC Underspecified [Medium]
**Section:** 4.2

No configurable claim name, no fail-closed behavior, no auto-provisioning specification.

**Recommendation:** Specify configurable OIDC claim. Mandate fail-closed on missing claim. Document service-to-service tenant identity.

### 8.13. Billing Sequence Numbers Info Disclosure [Low]
**Section:** 11.2.1, 11.8

Gap-free sequences reveal activity volume if exposed cross-tenant.

**Recommendation:** Ensure strict tenant scoping on billing APIs.

### 8.14. Public Metadata May Leak Tenant Info [Low]
**Section:** 5.1

`public` visibility serves content without auth to anyone.

**Recommendation:** Require platform-admin approval for `public` metadata in multi-tenant mode.

---

## 9. Storage Architecture & Data Management

### 9.1. Redis Fail-Open Enables Per-Tenant Budget Bypass [High]
**Section:** 12.4

10 gateway replicas = 10x per-tenant budget during outage. Per-tenant budgets have no local enforcement.

**Recommendation:** Add per-tenant in-memory counters. Apply conservative ceiling per replica during fail-open.

### 9.2. Data-at-Rest Encryption Gaps [High]
**Section:** 12.3, 12.4

Postgres, Redis, WAL archives, and backup encryption never mentioned.

**Recommendation:** Require Postgres encryption at rest. Document Redis data as ephemeral with app-layer encryption. Require encrypted WAL archives/backups.

### 9.3. Orphaned MinIO Objects from Failed Checkpoints [Medium]
**Section:** 4.4, 12.5

Workspace uploaded to MinIO but Postgres metadata write fails = orphaned object invisible to GC.

**Recommendation:** Add periodic MinIO bucket scan cross-referencing Postgres. Clean objects >1hr old with no record.

### 9.4. Checkpoint Cadence Undefined [Medium]
**Section:** 5.2, 12.5

Listed as pool dimension but no default specified. GC throughput requirements unquantified.

**Recommendation:** Define default cadence (e.g., 15min). Document GC throughput requirements. Consider inline cleanup.

### 9.5. File Export Latency for Delegation Not Quantified [Medium]
**Section:** 8.8

200MB repo through 3 hops + 2 MinIO I/Os = 10-30s per delegation level.

**Recommendation:** Document expected latency. Consider workspace snapshot caching and content-addressed dedup.

### 9.6. Postgres SPOF for All Durable Writes [Medium]
**Section:** 12.3

30s RTO = 30s full platform blackout for all writes.

**Recommendation:** Consider write-ahead buffer for billing/audit events. Document expected behavior during failover. Serve hot-path reads from replica/cache.

### 9.7. Gap-Free Billing Sequences Infeasible Under Concurrent Writes [Medium]
**Section:** 11.2.1, 11.8

Postgres sequences have gaps on rollback. Gap-free requires serialized writes.

**Recommendation:** Clarify contract. Either use serialized writer pattern or relax to "monotonic with detectable gaps" + reconciliation endpoint.

### 9.8. PgBouncer Transaction Mode Breaks LISTEN/NOTIFY [Medium]
**Section:** 10.3, 12.3

LISTEN registrations lost when connection returns to pool.

**Recommendation:** Use dedicated direct Postgres connection for LISTEN/NOTIFY (bypassing PgBouncer), or rely on polling fallback.

### 9.9. pgvector Scaling Guidance Missing [Low]
**Section:** 9.4

Performance degrades at millions of rows. No partitioning or index guidance.

**Recommendation:** Document HNSW over IVFFlat, partition by tenant_id, add query latency metric.

### 9.10. No Autovacuum Tuning for High-Churn Tables [Low]
**Section:** 16.4

Sessions and quotas tables accumulate dead tuples.

**Recommendation:** Document aggressive autovacuum settings. Recommend partition-by-date for sessions. Monitor dead tuple count.

### 9.11. SQLite Dev Mode Schema Drift [Low]
**Section:** 17.4

Same as 7.4.

### 9.12. Legal Hold Degrades GC Performance [Low]
**Section:** 12.8

GC repeatedly fetches and skips held artifacts.

**Recommendation:** Add `WHERE legal_hold = false` to GC query. Index on `(expires_at, legal_hold)`.

---

## 10. Recursive Delegation & Task Trees

### 10.1. Permanent Lease Extension Rejection Too Aggressive [High]
**Section:** 8.6

Rejection permanently starves entire tree. No scoped rejection, no cool-off, no admin reset.

**Recommendation:** Allow per-subtree rejection. Add cool-off period. Provide admin API to clear flag.

### 10.2. Deep Tree Recovery Has No Timeout or Ordering [High]
**Section:** 8.11

Multi-level failure recovery has no ordering guarantee, no per-level timeout, no total tree recovery bound.

**Recommendation:** Specify bottom-up recovery. Add `maxTreeRecoverySeconds`. Document interaction with `maxResumeWindowSeconds`.

### 10.3. Orphan Cleanup Interval Underspecified [Medium]
**Section:** 8.11

No default `cascadeTimeoutSeconds`, no job interval, no metrics/alerts. Detached children could run indefinitely.

**Recommendation:** Specify defaults (3600s timeout, 60s interval). Add metrics. Count orphans against user quota.

### 10.4. Credential Propagation `inherit` Lacks Depth Semantics [Medium]
**Section:** 8.3

15 nodes inheriting from same pool can silently exhaust `maxConcurrentSessions`.

**Recommendation:** Pre-flight credential capacity check at delegation time. Document interaction with tree size limits.

### 10.5. Cross-Environment Delegation Policy Interaction [Medium]
**Section:** 10.6

Further delegation from cross-environment child unclear. `inherit` credential propagation likely wrong across environments.

**Recommendation:** Children use target environment's declarations. Prohibit `inherit` for cross-environment. Add worked example.

### 10.6. `maxTokenBudget` Overshoot in Deep Trees [Medium]
**Section:** 11.2

Concurrent children racing against shared Redis counter. Overshoot = N * max_tokens_per_request.

**Recommendation:** Use per-child budget reservation. Add `budgetWarningThreshold` for proactive notification.

### 10.7. `await_children` Mode `any` Leaves Orphaned Children [Medium]
**Section:** 8.9

Non-winning children run unchecked with no automatic cleanup.

**Recommendation:** Add `cancelRemaining: true` parameter. Add `maxUncollectedChildAge`. Emit warning when parent completes without collecting.

### 10.8. `cancel_all` Cascade No Ordering or Timeout [Medium]
**Section:** 8.11

Deep tree cancellation has no per-node timeout. Parent `cancel_all` vs child `await_completion` precedence undefined.

**Recommendation:** Enforce top-down with per-node 30s timeout. Parent policy overrides descendant policies. Add total cascade timeout.

### 10.9. No Fan-Out Rate Limiting [Medium]
**Section:** 8

10 children * 10 grandchildren = 100 pod claims in seconds.

**Recommendation:** Add `delegationRateLimit` (e.g., 5/sec/session). Queue excess.

### 10.10. `send_message` Allows Arbitrary Cross-Tree Communication [Medium]
**Section:** 7.2, 8.5

No restriction that target must be in same tree. Information leak in multi-tenant.

**Recommendation:** Default restrict to same-tree. Add explicit `crossTree` option requiring policy authorization. Enforce tenant isolation.

### 10.11. `children_reattached` Event Underspecified [Low]
**Section:** 8.2

No schema, no cursor/sequence, no completed-while-down result handling.

**Recommendation:** Define formal schema with child states, pending results, and last known sequence.

### 10.12. `LeaseSlice` Never Defined [Low]
**Section:** 8.2

Referenced in `delegate_task` signature but no schema anywhere.

**Recommendation:** Define schema (subset of lease fields parent can override). Specify reserved vs shared budget.

### 10.13. No Platform-Level Max Delegation Depth [Low]
**Section:** 8.3

Deployer could set `maxDepth: 100`. Elicitation, recovery, tracing all degrade.

**Recommendation:** Add platform-level `maxDelegationDepth` ceiling (e.g., 10) in Helm config.

### 10.14. Non-Cascading Interrupt Creates Inconsistent State [Low]
**Section:** 6.2

Parent suspended but children keep running and consuming budget.

**Recommendation:** Add `cascade: true` option to interrupt API. Pause tree-wide budget while root suspended.

### 10.15. Missing Section 8.7 [Info]
**Section:** 8

Numbering gap, likely from deleted content.

**Recommendation:** Renumber or note gap.

---

## 11. Session Lifecycle & State Management

### 11.1. Checkpoint Failure May Permanently Freeze Agent [Critical]
**Section:** 4.4

No guarantee `SIGCONT` sent on all failure paths. Adapter crash mid-checkpoint leaves agent SIGSTOPped with no recovery.

**Recommendation:** Add `defer SIGCONT` invariant. Add checkpoint-level timeout (60s) with unconditional SIGCONT. Document adapter crash behavior.

### 11.2. Pod State Machine Missing Failure Transitions [High]
**Section:** 6.2

No `running_setup -> failed` or `finalizing_workspace -> failed` transitions shown.

**Recommendation:** Add explicit failure transitions from all pre-`attached` states. Define retry policy for pre-attached failures.

### 11.3. Generation Counter Race During Postgres Fallback [High]
**Section:** 10.1

Brief lock release window allows coordinator thrashing. Stale replica handling undefined.

**Recommendation:** Specify coordinator handoff protocol. Stale replica must stop RPCs, clear state, back off.

### 11.4. `awaiting_client_action` Expiry Orphans Children [High]
**Section:** 7.3

Transition to `expired` doesn't trigger `cascadeOnFailure`. Running children left orphaned.

**Recommendation:** `awaiting_client_action -> expired` must trigger cascade policy. Add `expired` as terminal state.

### 11.5. Concurrent Checkpoint and Interrupt Operations [High]
**Section:** 4.7

Both target the running process. No mutual exclusion. Interrupt during SIGSTOP is undeliverable.

**Recommendation:** Define mutual exclusion via adapter per-session lock. Queue operations. Document ordering semantics.

### 11.6. Session Derive Credential State Undefined [Medium]
**Section:** 19 #12

No specification of how derived session handles credential lease.

**Recommendation:** Derived sessions are independent with own lease. Reuse stored token for same user without re-elicitation.

### 11.7. SSE Buffer Overflow No Client Feedback [Medium]
**Section:** 7.2

Disconnection indistinguishable from network failure. `checkpoint_boundary` behavior undefined.

**Recommendation:** Send `buffer_overflow` event before drop. Define `checkpoint_boundary` schema.

### 11.8. `suspended` Timer vs `maxSessionAge` in Delegation Trees [Medium]
**Section:** 6.2

Children continue consuming budget while parent timer paused. No max suspend duration.

**Recommendation:** Add `maxSuspendedTime`. Clarify child timers continue independently.

### 11.9. Task State Machine Missing `cancelled` State [Medium]
**Section:** 8.9

`cancelled` used in `await_children` but absent from state machine. Transitions undefined.

**Recommendation:** Add `cancelled` as terminal state. Define transitions from all active states.

### 11.10. SDK-Warm Race Between Workspace Check and Pod Selection [Medium]
**Section:** 6.1

Fallback if SDK-warm claim fails undefined. Late-upload of blocking files after SDK-warm claim undefined.

**Recommendation:** Define fallback to pod-warm. Reject blocking-path uploads after SDK-warm claim.

### 11.11. `resume_pending`/`resuming` Missing Timeouts [Medium]
**Section:** 6.2

Infinite wait possible on pool exhaustion or checkpoint restoration hang.

**Recommendation:** Timeout `resume_pending` at `podClaimQueueTimeout`. Timeout `resuming` at restoration timeout (300s).

### 11.12. Elicitation Chain Fragile Across Gateway Failover [Medium]
**Section:** 9.2

Multi-hop elicitation state in gateway memory. Not persisted for failover reconstruction.

**Recommendation:** Persist elicitation chain state in SessionStore. Reconstruct on failover.

### 11.13. `claimed` State No Timeout [Low]
**Section:** 6.2

Client claims pod but never uploads. Pod held indefinitely.

**Recommendation:** Add `claimTimeout` (120s). Revert to `idle` on expiry.

### 11.14. Seal-and-Export Retry Unbounded [Medium]
**Section:** 7.1

No max retries, no total timeout. MinIO outage accumulates draining pods.

**Recommendation:** 3 retries, 300s total. Transition to `terminated` with `seal_failed` flag. Alert.

### 11.15. Workspace Materialization Override Semantics [Low]
**Section:** 5.1

Silent overwrites with no warnings for client uploads overwriting derived defaults.

**Recommendation:** Emit warnings for all overwrites. Consider configurable `overwritePolicy`.

### 11.16. `attached -> resume_pending` Trigger Undefined [Medium]
**Section:** 6.2

Multiple detection mechanisms (gRPC break, health check, eviction) with different latencies. Not enumerated.

**Recommendation:** Enumerate detection mechanisms with expected latencies. Define final-checkpoint attempt.

### 11.17. Concurrent Mode No Session-Level State Machine [Medium]
**Section:** 5.2

State machines defined for session mode only. Per-slot aggregation, draining, checkpoint, suspend all undefined.

**Recommendation:** Define slot-level state machine and aggregation rules. Or explicitly state simplified semantics with limitations.

### 11.18. Duplicate Billing Event Sections [Info]
**Section:** 11.2.1, 11.8

Same finding — consolidate.

---

## 12. Observability & Operational Monitoring

### 12.1. No Elicitation Chain Metrics [High]
**Section:** 16.1

Zero metrics for elicitation round-trip latency, pending count, suppression, timeouts.

**Recommendation:** Add `lenny_elicitation_roundtrip_seconds`, `_pending`, `_suppressed_total`, `_timeout_total`. Add `ElicitationBacklogHigh` alert.

### 12.2. No Credential Lifecycle Tracing Spans [High]
**Section:** 16.3

No spans for credential assignment, rotation, or fallback chain. Critical latency path invisible.

**Recommendation:** Add `credential.assign`, `credential.rotate`, `credential.fallback_chain`, `credential.proxy_request` spans.

### 12.3. Delegation Budget Consumption Not Tracked [High]
**Section:** 16.1

No metrics for token budget utilization, extension rates, or tree-wide consumption trends.

**Recommendation:** Add `lenny_delegation_budget_utilization_ratio`, `_lease_extension_total`, `_tree_token_usage_total`. Add `DelegationBudgetNearExhaustion` alert.

### 12.4. Warm Pool Claim Queue Wait Missing [Medium]
**Section:** 16.1

Time-to-claim exists but no queue wait time, conflict count, or timeout metrics.

**Recommendation:** Add `lenny_pod_claim_queue_wait_seconds`, `_conflict_total`, `_timeout_total`.

### 12.5. Gateway Subsystem Metrics Not Enumerated [Medium]
**Section:** 16.1

Per-subsystem metrics mentioned but not named. No circuit breaker state metrics.

**Recommendation:** Enumerate all per-subsystem metrics with explicit names.

### 12.6. Token Service Metrics and Alert Missing [Medium]
**Section:** 16.1, 16.5

No latency, error, or circuit breaker metrics. No unavailability alert.

**Recommendation:** Add request duration, error counter, circuit breaker state. Add `TokenServiceUnavailable` alert.

### 12.7. No SLO for Delegation Spawn Latency [Medium]
**Section:** 16.5

Delegation is user-facing but has no SLO.

**Recommendation:** Add P95 <15s SLO for delegation spawn. Add `lenny_delegation_spawn_latency_seconds` metric.

### 12.8. Checkpoint Observability Insufficient [Medium]
**Section:** 16.1

No success/failure rates, failure reasons, resume success by checkpoint age, or data loss window.

**Recommendation:** Add `lenny_checkpoint_total` (by outcome), `_age_at_resume_seconds`, `_resume_total`, `_data_loss_window_seconds`.

### 12.9. No Setup Command Metrics [Medium]
**Section:** 16.1

Setup commands on hot path with no duration, timeout, or failure metrics.

**Recommendation:** Add `lenny_setup_command_duration_seconds`, `_rejected_total`, `_phase_duration_seconds`.

### 12.10. Workspace Materialization Tracing Too Coarse [Medium]
**Section:** 16.3

`session.upload` span doesn't distinguish network transfer, extraction, validation.

**Recommendation:** Add child spans: `workspace.stream_to_staging`, `workspace.validate`, `workspace.extract_archive`, `workspace.promote`.

### 12.11. No Concurrent Mode Slot Metrics [Medium]
**Section:** 16.1

No slot utilization, allocation latency, or saturation metrics.

**Recommendation:** Add `lenny_concurrent_slots_active`, `_total`, `_utilization_ratio`, `_wait_seconds`.

### 12.12. PgBouncer/Read Replica Monitoring Not in Alerting [Medium]
**Section:** 16.5

No alerts for PgBouncer saturation or read replica lag.

**Recommendation:** Add `PgBouncerPoolSaturated`, `PgBouncerClientWaitHigh`, `PostgresReadReplicaLag` alerts.

### 12.13. No LLM Reverse Proxy Metrics [Medium]
**Section:** 4.9

Critical path with no visibility. Per-pool comparison impossible.

**Recommendation:** Add `lenny_llm_proxy_request_duration_seconds`, `_request_total`, `_active_requests`, `_upstream_duration_seconds`.

### 12.14-12.19. Various Low/Info Findings

Including: FinalizerStuck not in Section 16.5, CredentialPoolExhausted alert missing, controller queue overflow no alert, no experiment variant labels on metrics, Redis fail-open observability gap, trace sampling may lose delegation context.

---

## 13. Compliance, Governance & Data Sovereignty

### C7. GDPR Erasure Flow Incomplete [Critical]
**Section:** 12.8

MemoryStore, Redis, semantic cache, shared workspace snapshots, and external SIEM not in scope. Billing event immutability directly conflicts with erasure obligation.

**Recommendation:** Enumerate every storage backend in erasure spec. Use pseudonymization for billing events. Define erasure propagation contract to external sinks. Address workspace deduplication.

### 13.1. Data Residency Delegation Insufficient [High]
**Section:** 12.8

Delegation trees can span regions. No region-awareness in routing. No per-tenant storage routing.

**Recommendation:** Add optional `dataResidencyRegion` on Tenant/Environment. Enforce region constraints on pod pools and storage. Provide multi-region reference architecture.

### 13.2. Audit Log Integrity Best-Effort [High]
**Section:** 11.7

Startup check warns only. No continuous monitoring. No cryptographic integrity. Superuser bypass unaddressed.

**Recommendation:** Hard-fail startup check in production. Add periodic background check. Add hash chaining. Validate SIEM connectivity.

### 13.3. Billing Event No Correction Mechanism [High]
**Section:** 11.2.1, 11.8

Absolute immutability makes error correction impossible.

**Recommendation:** Add `billing_correction` event type referencing original. Consumers reconstruct by applying corrections.

### 13.4. No Data Classification Tiers [High]
**Section:** various

All data treated uniformly despite PII, PHI, credentials, business data having different requirements.

**Recommendation:** Define classification tiers. Allow per-tenant workspace classification. Drive controls from classification.

### 13.5. Retention Doesn't Align With Regulations [Medium]
**Section:** 7.1, 16.4, 11.8

7-day artifact default below most regulatory minimums. 90-day audit below HIPAA (6yr) and SOX (7yr).

**Recommendation:** Provide regulation-aligned retention presets. Allow per-tenant overrides. Add `retentionPolicyPreset`.

### 13.6. No Physical Isolation Path [Medium]
**Section:** 4.2

Logical isolation only. Shared Postgres, Redis, MinIO across tenants.

**Recommendation:** Prioritize namespace-per-tenant. Document dedicated-tenant reference architecture. Add `tenantIsolationLevel` config.

### 13.7. No Consent Management [Medium]
**Section:** various

No consent records, processing purpose tracking, or withdrawal mechanism.

**Recommendation:** Add `consentRecord` to session creation. Add `processingPurpose` enum. Provide consent management API.

### 13.8. No FIPS Compliance Path [Medium]
**Section:** 10.3, 10.5, 12.4

No FIPS-validated crypto modules. No TLS version minimum. No key type requirements.

**Recommendation:** Add `fipsMode` flag. Specify TLS 1.2+ minimum. Document FIPS KMS backends.

### 13.9. No Cross-Border Transfer Controls [Medium]
**Section:** various

LLM API calls, webhooks, connector calls may transit data across borders with no visibility.

**Recommendation:** Add region metadata to credentials/connectors. Add `allowedTransferRegions` on tenants. Log transfers.

### 13.10. Legal Hold Implementation Gaps [Medium]
**Section:** 12.8

No cascading holds, no hold inventory, no expiry mechanism, no audit event hold coverage.

**Recommendation:** Implement cascading holds. Add `GET /v1/admin/legal-holds`. Ensure partition drops check holds.

### 13.11-13.14. Various Low Findings

Including: no PIA/DPIA integration, experiment compliance safeguards, SOC 2 Trust Service gaps.

---

## 14. API Design & External Interface Quality

### 14.1. REST/MCP Consistency Not Enforceable [High]
**Section:** 15.2.1

"Shared service layer" is implementation aspiration without testing strategy.

**Recommendation:** Generate MCP schemas from OpenAPI. Add contract tests calling both surfaces.

### 14.2. Admin API Error Response Schema Absent [High]
**Section:** 15.1

One inline example. No error code catalog, no per-endpoint docs, no validation error format.

**Recommendation:** Add canonical error envelope, error code table, validation error format, rate-limit headers.

### 14.3. `dryRun` Semantics Undefined [High]
**Section:** 15.1, 21.5

No definition of behavior, supported endpoints, response format, or interaction with etags.

**Recommendation:** Define full semantics: performs validation without persistence. Enumerate supported endpoints.

### 14.4. ETag Concurrency Completely Unspecified [High]
**Section:** 15.1

No generation mechanism, required headers, mismatch behavior, or interaction with DELETE.

**Recommendation:** Specify ETags on all GET responses. Require `If-Match` on PUT (428 on missing, 412 on mismatch). Use version counters.

### 14.5. No Pagination Specification [High]
**Section:** 15.1

Multiple list endpoints with no pagination contract.

**Recommendation:** Define cursor-based pagination envelope. Standard query parameters (`cursor`, `limit`, `sort`).

### 14.6. Admin API Resource Schemas Not Defined [Medium]
**Section:** 15.1

No mapping from internal YAML to REST JSON. No metadata fields (timestamps, versions).

**Recommendation:** Publish OpenAPI spec as normative reference or add representative request/response examples.

### 14.7. `POST /messages` Schema Undefined [Medium]
**Section:** 7.2, 15.1

Primary interaction endpoint with no request/response schema.

**Recommendation:** Add clear request/response examples for normal message and `inReplyTo` reply.

### 14.8. No API Discoverability [Medium]
**Section:** 15.1

No `GET /v1/` endpoint, no version header, no HATEOAS links.

**Recommendation:** Add `GET /v1/` returning API version and resource types. Add `X-Lenny-API-Version` header.

### 14.9. SSE Stream Contract Not Specified for REST [Medium]
**Section:** 7.2, 15.1

No endpoint definition, event format, or `Last-Event-ID` mapping for REST clients.

**Recommendation:** Define `GET /v1/sessions/{id}/stream` SSE endpoint with event format and cursor semantics.

### 14.10. Idempotency Key Mechanism Unspecified [Medium]
**Section:** 11.5

Header vs body? Dedup window? Concurrent duplicate behavior?

**Recommendation:** `Idempotency-Key` header, 24hr retention, per-tenant scope, cached response for duplicates.

### 14.11. Webhook Schema Inconsistency [Medium]
**Section:** 14, 11.8

Session callbacks and billing webhooks use different envelope schemas and verification mechanisms.

**Recommendation:** Unify delivery envelope. Same HMAC-SHA256 verification algorithm.

### 14.12. External Adapter Registration Security [Medium]
**Section:** 15

Runtime-registered adapters could intercept traffic. No validation/sandboxing.

**Recommendation:** Require `platform-admin`. Scope to registered path prefix. Audit trail. Two-phase register+enable.

### 14.13-14.20. Various Low Findings

Including: no bulk admin operations, derive endpoint unspecified, no rate-limit headers, usage API lacks grouping, session state matrix for API consumers, pool status endpoint lacks operational detail.

---

## 15. Competitive Positioning & Open Source Strategy

### C8. No "Why Lenny?" Narrative [Critical]
**Section:** 23

Competitors listed but differentiation never articulated. No value proposition statement.

**Recommendation:** Add "Why Lenny?" section with 3-5 concrete differentiators: runtime-agnostic adapter contract, recursive delegation primitive, self-hosted K8s-native, multi-protocol gateway, enterprise controls.

### 15.1. `agent-sandbox` Upstream Risk Unaddressed [High]
**Section:** 4.6.1

Same as C1 — no abstraction layer, engagement strategy, or fallback plan.

### 15.2. No Community Adoption Strategy [High]
**Section:** 18

No target persona, adoption funnel, "time to hello world" metric, or governance model.

**Recommendation:** Define personas. Set <5min TTHW target. Decide governance early (Phase 1-2). Plan comparison guides.

### 15.3. Missing Temporal/Modal/LangGraph Comparison [High]
**Section:** 23

Most direct competitive threats omitted.

**Recommendation:** Expand Section 23 with Temporal, Modal, LangGraph entries and specific differentiation.

### 15.4. "Hooks and Defaults" May Create Hollow Day-One Experience [Medium]
**Section:** 22.6

No memory, guardrails, eval, caching, or routing out of the box.

**Recommendation:** Ship 1-2 reference implementations per interface. Plan plugin registry. Consider "batteries-included" Helm variant.

### 15.5. Configuration Surface Overwhelming [Medium]
**Section:** various

Dozens of config points with no progressive disclosure.

**Recommendation:** Define "Simple mode" (<20 lines). Create progressive disclosure guide.

### 15.6. No Licensing Decision [Medium]
**Section:** 18

License choice undocumented. Affects enterprise adoption and contributor onboarding.

**Recommendation:** Decide before Phase 2. Apache 2.0 for maximum adoption.

### 15.7. 17-Phase Build Sequence "Never Ships" Risk [Medium]
**Section:** 18

No MVP definition, no timeline, no minimum viable product milestone.

**Recommendation:** Define "ready for" milestones. Front-load MVP track. Add timeline estimates.

### 15.8. gVisor Default Barrier to Entry [Medium]
**Section:** 5.3

Not available on most dev machines or managed K8s.

**Recommendation:** Make runc default in Helm with warning. gVisor default in `values-production.yaml`.

### 15.9. No Ecosystem Integration Beyond MCP [Medium]
**Section:** various

No CI/CD patterns, pre-built dashboards, provider marketplace, or dev tool integrations.

**Recommendation:** Ship Grafana dashboards. Document CI/CD patterns. Plan integrations directory.

### 15.10-15.14. Low/Info Findings

Including: multi-protocol may dilute focus, billing open-core boundary undefined, no devrel strategy.

---

## 16. Warm Pool & Pod Lifecycle Management

### 16.1. SDK-Warm Creates Dual-Pool Inventory Problem [High]
**Section:** 6.1

No mechanism to control SDK-warm vs pod-warm ratio. Degradation path for wrong pod type undefined.

**Recommendation:** Introduce `sdkWarmRatio` or make SDK-warm pods degradable to pod-warm with documented penalty.

### 16.2. Pool Sizing Formula Lacks Burst Term [High]
**Section:** 4.6.1, 4.6.2

Formula doesn't model refill rate or burst absorption.

**Recommendation:** Extend formula with burst absorption term: `burst_p99_claims * pod_warmup_seconds`.

### 16.3. Execution Mode / Warm Pool Interaction Underspecified [High]
**Section:** 5.2, 4.6

Task mode reuse and concurrent mode slot division not reflected in scaling formulas.

**Recommendation:** Add "Execution Mode Scaling Implications" subsection with per-mode formula variants.

### 16.4. `sdkWarmBlockingPaths` Fragile and Static [Medium]
**Section:** 6.1

Static per-runtime. Archives invisible at claim time. No discovery mechanism.

**Recommendation:** Default archives to pod-warm. Consider explicit client signal for SDK-warm eligibility.

### 16.5. Pod Eviction During SDK-Warm [Medium]
**Section:** 4.6.1, 6.2

No finalizer protection, no PDB coverage, preStop checkpoint pointless.

**Recommendation:** Clarify `sdk_connecting` pods carry `idle` label for PDB. Skip checkpoint in preStop.

### 16.6. Experiment Variant Pool Waste [Medium]
**Section:** 10.7

Per-variant pools with safety factor multiply warm pod count linearly with experiments.

**Recommendation:** Consider shared pool with claim-time variant assignment. Use lower safety factor for low-traffic variants.

### 16.7. Controller Failover Queue Cascading Timeouts [Medium]
**Section:** 4.6.1

30s `podClaimQueueTimeout` = 30s client request timeout. Post-failover burst from multiple replicas.

**Recommendation:** Set queue timeout < client timeout (15s vs 30s). Add jitter. Add queue depth limit.

### 16.8. No Warm Pool Priority/Reservation [Medium]
**Section:** 4.6.1

First-come-first-served. Low-priority batch can claim last pod over high-priority production.

**Recommendation:** Consider priority field on claims. Reserve subset of warm pool for high-priority.

### 16.9. Work Queue Overflow No Alert/Resync [Medium]
**Section:** 4.6.1

Same as 4.14 — add alert and periodic resync.

### 16.10. No Warm Pod Staleness/Health Degradation Model [Medium]
**Section:** 16.1

Only cert expiry recycles pods. Memory fragmentation, runtime drift unaddressed.

**Recommendation:** Introduce `maxIdleAge` per pool (e.g., 2hr). Track `stale_warm_pods` metric.

### 16.11. Certificate Expiry Creates Steady Churn [Low]
**Section:** 4.6.1, 10.3

~12-13 replacements/hr during idle periods for 50-pod pool.

**Recommendation:** Document expected churn rate. Make `certExpiryDrainThreshold` configurable.

### 16.12. Scale-to-Zero Resume Latency Not Quantified [Low]
**Section:** 4.6.1

Cold-start path never measured.

**Recommendation:** Add cold-start latency table. Add `lenny_pool_cold_start_seconds` metric.

### 16.13. `podClaimQueueTimeout` Invisible to Clients [Low]
**Section:** 4.6.1

Client blocks up to 30s with no feedback.

**Recommendation:** Consider returning session ID immediately in `queued` state.

---

## 17. Credential Management & Secret Handling

### 17.1. LLM Proxy as SPOF and Latency Bottleneck [High]
**Section:** 4.9

No subsystem isolation. Gateway outage kills all LLM calls in proxy mode.

**Recommendation:** Define as fourth gateway subsystem. Consider separate scalable deployment. Document availability tradeoff.

### 17.2. Credential Rotation Unreliable for Non-Full Runtimes [High]
**Section:** 4.7

Minimum/Standard tiers cannot receive rotated credentials. No restart/resume path defined.

**Recommendation:** Document per-tier rotation behavior. Auto-trigger checkpoint+restart for non-Full tiers.

### 17.3. Credential Pool Exhaustion Handling Gaps [High]
**Section:** 4.9

No pre-claim availability check. Pod claimed then fails at credential assignment.

**Recommendation:** Pre-claim credential availability check. Define `CREDENTIAL_POOL_EXHAUSTED` error code. Track mismatch metric.

### 17.4. Three Credential Modes Operator Confusion [Medium]
**Section:** 4.9, 14

Dual schema (Runtime vs WorkspacePlan). Precedence undefined. `userCredentialMode` workflows unclear.

**Recommendation:** Unify schema. Add operator decision tree. Specify precedence: Runtime defines envelope, WorkspacePlan selects within it.

### 17.5. Gateway Memory Credential Material Exposure [Medium]
**Section:** 4.3

Materialized credentials cached in gateway memory. Lifetime, encryption, zeroing unspecified.

**Recommendation:** Zero after transmission. If cached, store only lease IDs (not material). Document gateway as trust boundary.

### 17.6. Direct Mode Env Var Credential Exposure [Medium]
**Section:** 4.7

Same as 2.3 — credentials in env vars readable, persistent in crash dumps.

### 17.7. No Revocation Propagation for Direct Mode Active Calls [Medium]
**Section:** 4.9, 11.4

Race window between revocation and pod termination allows unauthorized calls.

**Recommendation:** Document timing guarantees per mode. Recommend proxy for providers without short-lived tokens.

### 17.8. Credential Health Scoring Unspecified [Medium]
**Section:** 4.9

No algorithm, thresholds, recovery mechanism, or update frequency.

**Recommendation:** Define states (healthy/degraded/cooldown/failed). Specify automatic recovery. Add health check for failed credentials.

### 17.9. Delegation `inherit` May Leak Pool Boundaries [Medium]
**Section:** 8.3

User-scoped credential inherited by all children. Tree-wide concurrent session count not tracked.

**Recommendation:** Add tree-wide credential tracking. Document `inherit` as identity propagation.

### 17.10. Credential Lifecycle Not in Audit Trail [Medium]
**Section:** 11.7

Only billing stream has credential events. Compliance requires full audit trail.

**Recommendation:** Add `credential.assigned`, `_rotated`, `_revoked`, `_released`, `_assignment_failed` to audit events.

### 17.11. No Emergency Credential Rotation Procedure [Medium]
**Section:** 10.5, 17.7

No documented response for credential compromise.

**Recommendation:** Add emergency revocation procedure and admin endpoint. Add to runbooks.

### 17.12. Semantic Cache Tenant Isolation [Low]
**Section:** 4.9

Same as 2.7/8.10.

### 17.13. Token Service Lacks Rate Limiting [Low]
**Section:** 4.3

No per-replica quotas or anomaly detection on high-value target.

**Recommendation:** Add per-gateway rate limits. Add anomaly detection alerts.

### 17.14. `callbackSecret` Storage Unspecified [Low]
**Section:** 14

Same as 2.12.

### 17.15. Build Sequence Places Credentials Late [Info]
**Section:** 18

Same as C9.

---

## 18. Content Model, Data Formats & Schema Design

### 18.1. `OutputPart` Lacks Schema Version [High]
**Section:** 15.4.1

No version identifier on the universal content model. Future schema evolution breaks durable data.

**Recommendation:** Add `schemaVersion` integer field (default 1). Define forward-compatibility contract.

### 18.2. `OutputPart` Translation Fidelity Undocumented [High]
**Section:** 15, 15.4.1

Round-trip through multiple adapters causes undocumented information loss.

**Recommendation:** Define translation fidelity matrix per adapter. Add `protocolHints` annotations.

### 18.3. Durable Data Schema Versioning Missing [High]
**Section:** 15.5

No version stamp on Postgres records. 13-month billing events will span schema changes.

**Recommendation:** Add `schemaVersion` to all durable records: TaskRecord, BillingEvent, AuditEvent, CheckpointMetadata, SessionRecord.

### 18.4. `MessageEnvelope` Missing Metadata Field [Medium]
**Section:** 15.4.1

No extension point for protocol-specific or deployer-specific data.

**Recommendation:** Add `metadata: map[string]any` with namespace convention (`lenny.` for platform, `x-` for deployer).

### 18.5. `RuntimeDefinition` Capability Inheritance Ambiguous [Medium]
**Section:** 5.1

Only `capabilities.interaction` explicitly locked. Other sub-fields ambiguous.

**Recommendation:** Make entire `capabilities` block non-overridable on derived runtimes.

### 18.6. `agentInterface` No Schema Validation [Medium]
**Section:** 5.1

No JSON Schema. A2A card auto-generation depends on specific fields.

**Recommendation:** Define JSON Schema. Validate at registration.

### 18.7. `WorkspacePlan` Missing Source Types [Medium]
**Section:** 14

No `artifactRef` (for derive) or `delegationExport` (for delegation) source types.

**Recommendation:** Add both source types for full audit/replay coverage.

### 18.8. `TaskRecord.messages` Role Enum Under-Specified [Medium]
**Section:** 8.9

`caller | agent` doesn't match `MessageEnvelope.from.kind` (client|agent|system|external).

**Recommendation:** Align with `MessageEnvelope.from.kind`. Include `from.id` for provenance.

### 18.9. `RuntimeDefinition` Missing `type: mcp` Constraints [Medium]
**Section:** 5.1

No spec of which fields are forbidden/ignored for `type: mcp`.

**Recommendation:** Add schema constraints table by type. Validate at registration.

### 18.10. `MessageEnvelope.delivery` Under-Defined for Multi-Thread [Medium]
**Section:** 15.4.1

Interaction with `threadId` and `slotId` undefined.

**Recommendation:** Document intended interaction. Note delivery scope extension needed for multi-thread.

### 18.11. No `OutputPart.inline` Size Limit [Medium]
**Section:** 15.4.1

Unbounded inline content could exhaust gateway memory and create blob-like Postgres records.

**Recommendation:** Define `maxInlineSize` (e.g., 10MB). Auto-promote oversized content to ArtifactStore.

### 18.12. Billing Event Schema Duplicated [Medium]
**Section:** 11.2.1, 11.8

Same finding — consolidate.

### 18.13-18.17. Various Low Findings

Including: `CredentialLease` missing extensibility, `WorkspacePlan.env` no allowlist mode, `SemanticCache` schema too thin, `TaskSpec.input` notation ambiguous, `publishedMetadata` no validation hook.

---

## 19. Build Sequence & Implementation Risk

### C9. Credential Leasing (Phase 11) Too Late [Critical]
**Section:** 18

Phases 4-10 (core session + delegation) can only test with echo runtime. First end-to-end test with real LLM after 10 phases of code.

**Recommendation:** Split credential leasing. Phase 5.5: basic pool-based direct leasing. Phase 11: rotation, fallback, proxy, user-scoped, health scoring.

### C10. Security Hardening (Phase 14) Dangerously Late [Critical]
**Section:** 18

NetworkPolicies, gVisor enforcement, and image signing come after real credentials (Phase 11) and OAuth (Phase 12) are in play. Credentials can be exfiltrated by misbehaving agents.

**Recommendation:** Introduce Phase 3.5: default-deny NetworkPolicy, gVisor validation, digest-pinned images. Full hardening stays at Phase 14.

### 19.1. Observability (Phase 13) After All Complex Subsystems [High]
**Section:** 18

Phases 6-12 (streaming, delegation, credentials) built without distributed tracing.

**Recommendation:** Phase 2.5: structured logging with correlation fields + OpenTelemetry trace propagation. Full observability stack at Phase 13.

### 19.2. Missing Load Testing Phase [High]
**Section:** 18

No phase for benchmarking, capacity planning, or load testing.

**Recommendation:** Add Phase 13.5/14.5 for load testing covering all documented scaling concerns.

### 19.3. Phase 12 Bundles Too Many Concerns [High]
**Section:** 18

MCP runtimes, concurrent execution, Token Service, and OAuth in one phase.

**Recommendation:** Split: 12a (Token Service + KMS), 12b (type:mcp support), 12c (concurrent modes).

### 19.4. Echo Runtime Insufficient for Delegation Testing [High]
**Section:** 18

Echo can't call MCP tools. Phases 9-10 delegation untestable.

**Recommendation:** Build "delegation-capable test runtime" (scripted tool call sequences) as Phase 2/9 deliverable.

### 19.5. Admin API Foundation Should Precede Phase 3 [Medium]
**Section:** 18

PoolScalingController needs Postgres config but admin API is Phase 4.5.

**Recommendation:** Move admin API foundation to Phase 2.5 or document file-based bootstrap for Phase 3.

### 19.6. No Database Schema Phase [Medium]
**Section:** 18

Phase 1 says "core types" but doesn't mention Postgres, migrations, PgBouncer, or RLS.

**Recommendation:** Phase 1 must include Postgres schema foundation, migration tooling, PgBouncer config, RLS.

### 19.7. No Parallelization Identified [Medium]
**Section:** 18

17 phases presented as linear. Many are independent after Phase 7.

**Recommendation:** Add dependency graph. Identify parallel tracks (core, infra, platform).

### 19.8. Community Phase 17 Bundles Features + Docs [Medium]
**Section:** 18

MemoryStore and semantic caching mixed with documentation and community guides.

**Recommendation:** Separate feature work from documentation. Make docs a continuous deliverable.

### 19.9. Missing Compliance Validation Phase [Medium]
**Section:** 18

Legal hold, GDPR erasure, billing immutability, audit integrity not validated end-to-end.

**Recommendation:** Add compliance validation to Phase 13 or as Phase 13.5.

### 19.10. Phase 1 Overloaded Without Testable Output [Medium]
**Section:** 18

Massive schema definition with no runnable deliverable.

**Recommendation:** Phase 1 produces Go module with types, CRDs applied to kind cluster, and validation tests.

### 19.11. `agent-sandbox` Spike Missing [Medium]
**Section:** 18

No Phase 0 validation of upstream dependency. Same as C1.

### 19.12. No Critical Path Analysis [Medium]
**Section:** 18

No effort estimates, no critical path, no timeline.

**Recommendation:** Add T-shirt sizing and identify critical path.

### 19.13-19.17. Low/Info Findings

Including: Helm chart not in any phase, runbooks not in any phase, Phase 6 before Phase 7 unprotected window, build sequence creates concurrent mode testing gap.

---

## 20. Failure Modes & Resilience Engineering

### 20.1. MinIO Failure Causes Silent Session Loss [Critical]
**Section:** 4.4, 12.5

Checkpoint fails during eviction. No fallback storage. Agent SIGSTOPped then killed. Session state permanently lost.

**Recommendation:** Define fallback (node-local emergency storage or extended retry). Add `CheckpointStorageUnavailable` critical alert. Resume agent if checkpoint fails and pod isn't evicting.

### 20.2. Redis Fail-Open Quota Bypass [High]
**Section:** 12.4

Same as 9.1 — per-tenant counters, cumulative timer, worst-case documentation.

### 20.3. Postgres Failover Coordination Gap [High]
**Section:** 12.3, 10.1

Dual-store (Redis + Postgres) simultaneous unavailability orphans sessions for 30s. Billing blocks on Postgres.

**Recommendation:** Define behavior during dual-store unavailability. Clarify billing write blocking. Consider gateway-local coordination fallback.

### 20.4. etcd Unavailability Not Addressed [High]
**Section:** 4.6.1

Pod claims, state transitions, pool reconciliation all freeze.

**Recommendation:** Document etcd as critical dependency. Add `EtcdUnavailable` alert. Define degraded-mode serving existing sessions from Postgres.

### 20.5. PgBouncer as SPOF [High]
**Section:** 12.3

Bad rolling update takes all replicas down. No readiness probe verifying backend connectivity.

**Recommendation:** Add PDB. Implement readiness probe with backend verification. Consider gateway direct-to-Postgres fallback.

### 20.6. Gateway preStop SIGKILL Cliff [Medium]
**Section:** 10.1

Long-running sessions never drain in 60s.

**Recommendation:** Send `session_relocating` event. Two-phase drain. Increase `terminationGracePeriodSeconds`.

### 20.7. Controller Crash Blast Radius [Medium]
**Section:** 4.6.1

Stuck finalizers, cert expiry, orphan accumulation during crash loop.

**Recommendation:** Define max acceptable outage window. Add crash-loop detection. Document blast radius timeline.

### 20.8. Seal-and-Export Holds Pods Indefinitely [Medium]
**Section:** 7.1

Same as 11.14 — bounded retries, terminal state, alert.

### 20.9. No Postgres Circuit Breaker [Medium]
**Section:** 11.6

Slow Postgres piles up goroutines without circuit breaker.

**Recommendation:** Add circuit breaker (P99 >2s for 30s trips to half-open). PgBouncer `query_timeout`.

### 20.10. Coordination Takeover Race [Medium]
**Section:** 10.1

Partitioned replica makes stale gateway-side decisions before generation mismatch detected.

**Recommendation:** Periodic lease validation (every 5s). Heartbeat mechanism at half-TTL.

### 20.11. No Backpressure for Tree Recovery [Medium]
**Section:** 8.11

10 simultaneous pod failures in one tree overwhelm warm pool, Postgres, gateway.

**Recommendation:** Per-tree concurrent recovery limit (e.g., 3). Tree circuit breaker for mass failure.

### 20.12. Credential Lease Renewal Protocol Undefined [Medium]
**Section:** 4.9

Who initiates, failure handling, grace period calculation all unspecified.

**Recommendation:** Define gateway-initiated renewal at `renewBefore`. Ensure materialized TTL >= 2x lease TTL. Add renewal failure alert.

### 20.13. Event Replay Unbounded Storage [Medium]
**Section:** 7.2

2-hour sessions with event replay create large storage dependency.

**Recommendation:** Define max replay window. Fall back to checkpoint-based state recovery beyond window.

### 20.14. Cosign Webhook Fail-Closed Deadlocks Warm Pool [Medium]
**Section:** 5.3

Webhook outage prevents all pod creation.

**Recommendation:** Deploy webhook HA. Add `timeoutSeconds`. Document break-glass procedure.

### 20.15. Orphan Cleanup Job No Defined Failure Behavior [Low]
**Section:** 8.11

Same as 10.3.

### 20.16. Dual-Controller Coordination Gap [Low]
**Section:** 4.6

Rapid config changes from PoolScalingController cause WarmPoolController oscillation.

**Recommendation:** Add debounce interval. Add `PoolConfigOscillation` alert.

### 20.17. Token Service Stale Cache on Revocation [Low]
**Section:** 4.3

Same as credential revocation propagation concerns.

---

## 21. Experimentation & A/B Testing Primitives

### 21.1. Eval Score Ingestion Pipeline Undefined [High]
**Section:** 10.7

Results API references scores by variant but no schema, storage, or aggregation logic defined.

**Recommendation:** Define `EvalResult` schema. Gateway auto-associates with variant. Define Results API response.

### 21.2. Health-Based Rollback Unspecified [High]
**Section:** 10.7

No metrics, conditions, or automation for experiment status transitions.

**Recommendation:** Either make explicit non-goal (admin-only transitions) or define pluggable `ExperimentHealthEvaluator`.

### 21.3. Experiment Propagation Through Delegation Underspecified [High]
**Section:** 10.7

`inherit`/`control`/`independent` meanings unclear. Cross-experiment interaction undefined.

**Recommendation:** Define each mode precisely. "Innermost wins" for cross-experiment. Specify eval attribution.

### 21.4. PoolScalingController Variant Pool Waste [Medium]
**Section:** 4.6.2, 10.7

Same as 16.6.

### 21.5. No Experiment-Aware Metrics [Medium]
**Section:** 16

No `experimentId`/`variantId` labels on metrics, traces, or billing events.

**Recommendation:** Add experiment labels to key metrics. Include in billing events and root trace spans.

### 21.6. Experiment Assignment Not Audited [Medium]
**Section:** 10.7

Hash function, input, and stickiness mechanism unspecified. No audit trail.

**Recommendation:** Specify hash inputs. Record `experiment.assigned` audit event. Add `experimentContext` to session record.

### 21.7. Results API Should Be Raw Data Export [Medium]
**Section:** 10.7

Pre-aggregation insufficient for real statistical analysis. External platforms need raw data.

**Recommendation:** Reframe as raw data export. Separate convenience aggregation endpoint.

### 21.8. Replay Endpoint Lacks Experiment Semantics [Medium]
**Section:** 15.1

Replay/experiment interaction undefined. Could pollute experiment results.

**Recommendation:** Tag replays with `replay: true`. Exclude from experiment results by default. Accept `targetVariant` parameter.

### 21.9-21.12. Various Low/Info Findings

Including: no experiment lifecycle hooks, cohort targeting undefined, experiment/environment interaction unclear, Phase 1 should include experiment data model slots.

---

## 22. Document Quality, Consistency & Completeness

### 22.1. Duplicate Billing Event Sections [High]
**Section:** 11.2.1, 11.8

Two definitions with divergent field names. Same finding as 4.17.

### 22.2. Duplicate Section 17.5 [High]
**Section:** 17.5

"Cloud Portability" and "Operational Defaults" both numbered 17.5.

**Recommendation:** Renumber second to 17.8.

### 22.3. Broken Cross-References to Section 14 [High]
**Section:** 14

"Webhook system" and "env blocklist" references point to WorkspacePlan schema. `session.awaiting_action` event referenced but not defined.

**Recommendation:** Add missing webhook event. Extract webhooks to subsection. Extract env blocklist.

### 22.4. Missing Section 8.7 [Medium]
**Section:** 8

Numbering gap.

### 22.5. Alerts in Component Sections Missing from 16.5 [Medium]
**Section:** 16.5

`WarmPoolIdleCostHigh`, `FinalizerStuck`, `CosignWebhookUnavailable` not in centralized table.

**Recommendation:** Add all inline alerts to Section 16.5.

### 22.6. Inconsistent Metric Names [Medium]
**Section:** 10.1

`lenny_gateway_active_sessions` vs `lenny_gateway_active_streams`.

**Recommendation:** Use one consistent name.

### 22.7. Document Length [Medium]
**Section:** all

3700 lines in single document. Several sections are standalone specs.

**Recommendation:** Extract largest sections (credentials, delegation, API surface) to companion documents.

### 22.8. `ReportUsage` RPC Missing from Table [Medium]
**Section:** 4.7, 11.2

Referenced but not in adapter RPC table.

**Recommendation:** Add to RPC table or clarify alternative mechanism.

### 22.9. Terms Used Without Definition [Low]
**Section:** various

"generation", "MCP Fabric", "Streamable HTTP", "OutputPart", "LeaseSlice", "seal" used before definition.

**Recommendation:** Add glossary or "Key Terms" section.

### 22.10. CRD Versioning Inconsistency [Low]
**Section:** 10.5, 15.5

`v1alpha1` vs `v1beta1` initial version.

**Recommendation:** Pick one and update the other.

### 22.11-22.18. Various Low/Info Findings

Including: empty Section 20, inconsistent event naming, empty code block, no table of contents, Section 4.6.1 too long, runtime tiers described twice, `session.awaiting_action` webhook undefined.

---

## 23. Messaging, Conversational Patterns & Multi-Turn Interactions

### 23.1. "Runtime Available" Under-Specified [High]
**Section:** 7.2

Delivery path 2 undefined for runtimes mid-tool-call or not reading stdin.

**Recommendation:** Define precisely. Add delivery timeout. Document unread message behavior.

### 23.2. No Dead-Letter for Inter-Session Messages [High]
**Section:** 7.2, 8.5

Message to terminated task silently lost. No TTL, no confirmation, no dead-letter.

**Recommendation:** Return error for terminal targets. Queue with TTL for recovering targets. Add delivery receipt.

### 23.3. Agent Teams / Sibling Coordination No First-Class Support [High]
**Section:** 8.5

Children can't discover siblings. No sibling messaging or broadcast mechanism.

**Recommendation:** Clarify `get_task_tree` child visibility. Consider `get_siblings()` or `broadcast(scope)` tools.

### 23.4. Potential Deadlock in Circular request_input/await_children [High]
**Section:** 8.9, 9.2

Multiple children in `input_required` with no re-await semantics. All-blocked subtree has no detection.

**Recommendation:** Specify re-await semantics. Document multi-child handling pattern. Add deadlock detection.

### 23.5. `input_required` No Independent Timeout [Medium]
**Section:** 8.9

Child waiting for parent can block indefinitely, bounded only by `maxSessionAge`.

**Recommendation:** Add `maxInputWaitSeconds` timeout distinct from `maxElicitationWait`.

### 23.6. SSE Buffer Overflow Reconnect Storm [Medium]
**Section:** 7.2

Slow clients repeatedly disconnect and reconnect.

**Recommendation:** Send `buffer_warning` at 80%. Consider agent backpressure. Implement reconnect backoff.

### 23.7. `delivery: immediate` During Suspended State [Medium]
**Section:** 6.2, 7.2

Three delivery paths don't cover `suspended` state.

**Recommendation:** Add suspended as explicit delivery path case.

### 23.8. No Turn Sequencing Guarantees [Medium]
**Section:** 15.4.1

Multiple rapid messages may reorder during failover. No sequence number.

**Recommendation:** Add monotonic `sequence` field to `MessageEnvelope`.

### 23.9. `request_input` vs Elicitation Boundary Unclear [Medium]
**Section:** 8.5, 9.2

Root-level `request_input` behavior undefined.

**Recommendation:** Define request_input as inter-agent, elicitation as human-in-loop. Specify root-level behavior.

### 23.10. Concurrent Mode `slotId` Messaging Under-Specified [Medium]
**Section:** 5.2, 15.4.1

Platform tools (`request_input`, `send_message`) interaction with `slotId` undefined.

**Recommendation:** All tools scoped per-task (mapped to slotId). Adapter routes by taskId->slotId.

### 23.11. Reconnect `checkpoint_boundary` Under-Specified [Medium]
**Section:** 7.2

Schema, client handling, and replay window undefined.

**Recommendation:** Define schema (session state, active tasks, pending requests). Specify replay window.

### 23.12. No Discovery of Pending `input_required` After Reconnect [Medium]
**Section:** 7.2

Client sees state change but may miss original request payload.

**Recommendation:** Include `pending_input_requests` in reconnection response.

### 23.13. Message Delivery to Terminated/Draining Pods [Medium]
**Section:** 7.2

Gateway behavior for unreachable target pods undefined.

**Recommendation:** Reject for terminal/draining sessions. Queue for recovering sessions with TTL.

### 23.14-23.18. Various Low Findings

Including: no elicitation ordering spec, `threadId` vestigial, one_shot + request_input inconsistency, send_to_child vs send_message redundancy, no output backpressure.

---

## 24. Policy Engine & Admission Control

### 24.1. Budget Propagation Lacks Atomic Reservation [Critical]
**Section:** 8.3, 11.2

No specification of whether delegation budget slices are reserved or just ceilings. Parent can spawn children exceeding total budget. Default slice when `lease_slice` omitted undefined.

**Recommendation:** Atomic budget reservation via Redis `INCR` at delegation time. Default slice = remaining parent budget or configurable fraction. Return unused budget on completion.

### 24.2. RequestInterceptor Chain Order/Short-Circuit Unspecified [High]
**Section:** 4.8

No priority, no short-circuit behavior, no mutability, no timeout failure mode.

**Recommendation:** Explicit numeric priority. Rejection short-circuits. Timeout defaults to fail-closed. Document built-in ordering.

### 24.3. Fail-Open Rate Limiting Per-Replica Only [High]
**Section:** 12.4

Same as 2.6 / 4.4 — 10x effective limit with 10 replicas.

### 24.4. Cross-Session Budget Enforcement Race [High]
**Section:** 8.3

`maxTreeSize` and `maxTokenBudget` under concurrent delegation lack atomic enforcement.

**Recommendation:** Redis `INCR` for tree-size. Budget reservation model (not just ceiling).

### 24.5. Quota Redis/Postgres Consistency Gap [High]
**Section:** 11.2

60s sync = 60s stale data on crash recovery. Drift unbounded during fail-open.

**Recommendation:** Configurable sync interval (min 10s). Recover from pod-side usage on crash. Document max overshoot formula.

### 24.6. Timeout Table Incompleteness [Medium]
**Section:** 11.3

10+ operations with timeouts mentioned elsewhere but absent from the table.

**Recommendation:** Expand to cover all RPCs, network calls, and bounded computations.

### 24.7. Evaluator Ordering Not Specified [Medium]
**Section:** 4.8

Auth before/after quota? Pessimistic vs optimistic consumption?

**Recommendation:** Document ordered sequence. Commit quota only after all evaluators pass.

### 24.8. DelegationPolicy Intersection Lacks Formal Definition [Medium]
**Section:** 8.3

Multi-rule OR, include/exclude override, session-level `maxDelegationPolicy` composition undefined.

**Recommendation:** Formal per-rule subset requirement. Derived can add excludes only. Worked examples.

### 24.9. Lease Extension Across Delegation Depth [Medium]
**Section:** 8.6

Grandchild extension may exceed child's allocation. No cascading reservation check.

**Recommendation:** Extensions require cascading reservation up the tree. Cap if ancestor lacks capacity.

### 24.10. GuardrailsInterceptor Default-Disabled Gap [Medium]
**Section:** 4.8

No startup warning. Deployers may not realize no content filtering exists.

**Recommendation:** Startup warning. Security model note. Optional `requireGuardrails` flag.

### 24.11. Elicitation Budget Not Tree-Wide [Medium]
**Section:** 9.2

Per-session limit * tree size = potentially hundreds of user elicitations.

**Recommendation:** Add `maxElicitationsPerTree` on delegation lease.

### 24.12. AdmissionController Semantics Undefined [Medium]
**Section:** 4.8

Queue depth, priority, timeout, circuit breaker interaction all unspecified.

**Recommendation:** Define per-pool admission queue with fair-queuing. Distinguish from pod claim queue.

### 24.13. No Lease Extension Rate Limit in `auto` Mode [Medium]
**Section:** 8.6

Tight loop can exhaust `maxExtendableBudget` invisibly.

**Recommendation:** Add per-session rate limit. Add `maxExtensionsPerSession` counter. Alert on bursts.

### 24.14. Conflicting Credential Policies in Delegation [Medium]
**Section:** 8.3, 4.9

`inherit` vs child's own `CredentialPolicy` precedence undefined.

**Recommendation:** `inherit` = parent's resolved source. `independent` = child's policy from scratch. Document failures.

### 24.15-24.18. Various Low Findings

Including: env blocklist pattern rules, cross-env policy concurrent update, billing sequence Postgres write interaction, idle timer pause exploitation.

---

## 25. Execution Modes & Concurrent Workloads

### 25.1. Task Mode Cleanup Lacks Concrete Failure Definitions [High]
**Section:** 5.2

"Lenny scrub" undefined. `onCleanupFailure` behavior unspecified. Deployer acknowledgment mechanism missing.

**Recommendation:** Define explicit scrub procedure. Specify both failure mode behaviors. Define acknowledgment mechanism. Consider `replace` option.

### 25.2. Concurrent-Workspace No Per-Slot Failure Semantics [High]
**Section:** 5.2, 15.4.1

Slot failure, reclamation, checkpoint, resource contention all undefined.

**Recommendation:** Add "Concurrent Mode Internals" section with per-slot state machine, failure isolation, cleanup, checkpoint granularity.

### 25.3. Execution Mode / Warm Pool Interaction Underspecified [High]
**Section:** 5.2, 4.6

Same as 16.3 — scaling formulas assume session mode.

### 25.4. Concurrent-Workspace Filesystem Layout Undefined [High]
**Section:** 5.2, 6.4

Single `/workspace/current` shown but per-slot workspace structure never specified.

**Recommendation:** Define per-slot layout, cwd strategy, and adapter vs runtime responsibility for slot directories.

### 25.5. Concurrent-Stateless vs Connector Distinction Fuzzy [Medium]
**Section:** 5.2

"Should" vs "must" unclear. Criteria for "expensive shared state" undefined. K8s Service routing implications unexplored.

**Recommendation:** Enumerate criteria. Clarify LLM credential access and task lifecycle as distinguishing factors.

### 25.6. Task Mode Lifecycle Race for Minimum-Tier [Medium]
**Section:** 5.2, 15.4.1

Minimum-tier runtimes have no lifecycle channel for between-task signaling.

**Recommendation:** Require lifecycle channel for task mode, or define fallback protocol. Reject task mode for incompatible runtimes.

### 25.7. SDK-Warm Pod Destruction on Failed Claim [Medium]
**Section:** 6.1

Pod destroyed after pre-connecting SDK due to transient claim failure.

**Recommendation:** Acknowledge explicitly. Recommend pod-warm for claim-failure-sensitive deployments.

### 25.8. No Execution Mode / Capability Validation Matrix [Medium]
**Section:** 5.2

Invalid combinations (one_shot + concurrent, etc.) accepted at registration.

**Recommendation:** Add compatibility matrix. Reject invalid combinations at registration.

### 25.9. Concurrent Mode No Deployer Acknowledgment Mechanism [Medium]
**Section:** 5.2

Required but undefined. Stateless variant requires no acknowledgment despite different routing model.

**Recommendation:** Define flags for both concurrent variants.

### 25.10. Task Mode Credential Lease Lifecycle [Medium]
**Section:** 5.2, 4.9

Per-pod or per-task? Cross-tenant task reuse with same credential?

**Recommendation:** Specify per-task leasing. Mandatory for cross-user scenarios.

### 25.11. Suspended State in Non-Session Modes [Low]
**Section:** 6.2

Suspension semantics undefined for task and concurrent modes.

**Recommendation:** Clarify per-mode support. Define behavior for interrupt on non-session modes.

### 25.12-25.14. Various Low Findings

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
