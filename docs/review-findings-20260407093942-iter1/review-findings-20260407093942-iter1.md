# Technical Design Review Findings — 2026-04-07 (Iteration 1)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 1 of 5
**Total findings:** 353 across 25 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 30    |
| High     | 105   |
| Medium   | 138   |
| Low      | 71    |
| Info     | 16    |

### Critical Findings

| # | Perspective | Finding | Section |
|---|-------------|---------|---------|
| 1 | K8s Infrastructure | K8S-001 SandboxClaim Double-Claim Race Condition on Controller Failover | 4.6, 4.6.1 |
| 2 | Network Security | NET-001 Unrestricted Port Range on Pod-to-Gateway Egress | 13.2 |
| 3 | Network Security | NET-002 Cloud IMDS Endpoint Not Blocked for Agent Pods with Internet Egress | 13.2 |
| 4 | Scalability | SCL-001 All Gateway Subsystem Extraction Thresholds Are TBD Estimates | 4.1 |
| 5 | Scalability | SCL-002 No Per-Gateway-Replica Session Capacity Budget | 4.1, 16.5 |
| 6 | Developer Experience | DXP-001 Standalone Adapter Specification Promised but Not Published | 15.4 |
| 7 | Operator Experience | OPS-001 CRD Upgrade Requires Manual Out-of-Band Step | 10.5 |
| 8 | Operator Experience | OPS-002 Tier 2 Local Dev Default Is Plain HTTP | 17.4 |
| 9 | Operator Experience | OPS-003 Expand-Contract Phase 3 Has No Enforcement Gate | 10.5 |
| 10 | Multi-Tenancy | TNT-001 RLS Silently Disabled Under Cloud-Managed Poolers Without connect_query | 12.3 |
| 11 | Storage | STR-001 Redis Quota Fail-Open Allows Full-Tenant Budget Consumption on Single Replica | 11.2, 12.4 |
| 12 | Storage | STR-002 Audit Event Batching Silently Loses Events on Gateway Crash Without SIEM | 12.2 |
| 13 | Session Lifecycle | SLC-001 Derive From Live Session — No Isolation of In-Progress Workspace State | 7.1 |
| 14 | Session Lifecycle | SLC-002 Generation Counter Fencing Window — New Coordinator Sends RPCs Before Pod Acknowledges Fence | 10.1 |
| 15 | Compliance | CMP-001 SIEM Optional in Multi-Tenant Production Breaks Compliance-Grade Audit Integrity | 11.7, 16.4 |
| 16 | Compliance | CMP-002 Data Residency Enforcement Has No Runtime Validation Gate | 12.8, 4.2 |
| 17 | Compliance | CMP-003 Audit Batching Applies Even When SIEM Is Configured | 11.7, 12.3 |
| 18 | Competitive | CPS-001 No Differentiation Narrative | 1, 2 |
| 19 | Warm Pool | WPL-001 SDK-Warm Pod Eviction During sdk_connecting State Not Handled | 6.1, 6.2, 4.6.1 |
| 20 | Schema Design | SCH-001 OutputPart Type Registry Has No Formal Schema or Versioning Contract | 15.4.1 |
| 21 | Schema Design | SCH-002 RuntimeDefinition Inheritance Rules Exist Only in Prose | 5.1 |
| 22 | Schema Design | SCH-003 CredentialLease materializedConfig Is Deliberately Unschematized | 4.9 |
| 23 | Build Sequence | BLD-001 Critical-Path Dependency: Authentication Comes After Real LLM Testing | 18 |
| 24 | Build Sequence | BLD-002 Security Audit Scheduled After Full Observability — Too Late | 18 |
| 25 | Build Sequence | BLD-003 Echo Runtime Insufficient for Phase 6–8 Milestone Validation | 18 |
| 26 | Failure Modes | FLR-001 Dual-Store Concurrent Outage Leaves Sessions in Terminal Limbo | 10.1, 12.3, 12.4 |
| 27 | Failure Modes | FLR-002 MinIO Outage During Node Eviction Causes Irrecoverable Workspace Loss | 12.5, 4.4 |
| 28 | Document Quality | DOC-101 Section 17.8 heading does not exist — 34 cross-references are broken | 17.8 |
| 29 | Policy Engine | POL-001 Budget Propagation Race: Child Can Transiently Exceed Parent's Remaining Budget | 8.3 |
| 30 | Policy Engine | POL-002 Fail-Open Window for Quota Enforcement Is Unbounded Within the Per-Replica Ceiling | 12.4, 11.2 |

---

## Cross-Cutting Themes

### 1. Redis Fail-Open Window Is Under-Bounded Across Multiple Subsystems
Multiple perspectives (STR-001, POL-002, SEC-015, FLR-003, FLR-011) identify that the Redis fail-open behavior creates unbounded exposure windows for quota enforcement, rate limiting, delegation budgets, and session coordination. The per-replica fallback with `replica_count = 1` default allows N× overshoot. This theme appears in Storage, Policy, Security, and Failure Modes perspectives.

### 2. Session Inbox In-Memory Durability Is a Systemic Weakness
The in-memory session inbox with no durability guarantee surfaces as a finding across Security (SEC-011), Session Lifecycle (SLC-005, SLC-006), Messaging (MSG-002, MSG-006), Scalability (SCL-010), and Failure Modes (FLR-005). Loss of inter-session messages on coordinator crash creates data loss, message suppression attacks, and delegation result loss.

### 3. Schema and Contract Under-Specification Creates Implementation Ambiguity
Multiple schema-related findings (SCH-001 through SCH-008, DXP-001, DXP-004, CRD-001, CRD-003) identify that key data structures — OutputPart, RuntimeDefinition inheritance, CredentialLease materializedConfig, MessageEnvelope delivery semantics — lack formal schemas, validation contracts, or versioning strategies. This creates implementation divergence risk across adapter authors and third-party integrations.

### 4. Build Sequence Has Late-Bound Security and Performance Validation
The build sequence places security audit (Phase 14) and load testing (Phase 13.5) after the full system is built, rather than incrementally validating at key milestones. BLD-001, BLD-002, BLD-003, and BLD-010 all identify that credential injection, delegation chains, and streaming paths are built and merged without security review or performance validation until very late in the sequence.

### 5. Multi-Tenant Isolation Has Gaps Across Storage Backends
Tenant isolation findings span Postgres RLS (TNT-001), Redis key prefixing (TNT-002), MinIO path-only isolation (TNT-009), semantic cache scoping (TNT-005, STR-011), and task-mode pod reuse (TNT-003). The isolation model varies in strength across backends — from strong (Postgres RLS) to application-layer-only (MinIO, Redis) — creating inconsistent trust boundaries.

### 6. Observability Metrics and Alerts Are Scattered and Incomplete
OBS-001 through OBS-016 identify systematic gaps: alerts defined in prose but missing from the canonical Section 16.5 table, missing SLO burn-rate alerting, absent metrics for delegation tree memory, memory store operations, and session terminal errors. The observability specification lacks a single source of truth.

### 7. Compliance Controls Are Described but Not Enforced
CMP-001 through CMP-014 identify that compliance requirements (SIEM, data residency, audit integrity, legal hold, GDPR erasure) are described as deployer responsibilities but lack platform-level enforcement gates. A misconfigured deployment silently violates compliance posture with no runtime detection.

---

## Detailed Findings by Perspective

_Each perspective's findings are listed below in full, ordered by severity within each perspective._

---

## 1. Kubernetes Infrastructure & Controller Design

### K8S-001 SandboxClaim Double-Claim Race Condition on Controller Failover [Critical]
**Section:** 4.6, 4.6.1

The spec identifies optimistic-locking via `resourceVersion`-guarded updates as the mechanism preventing two WarmPoolController replicas from claiming the same pod simultaneously, marked as "ADR-TBD — Phase 1 blocking prerequisite." The 25-second crash-case failover window compounds this: Kubernetes Lease-based leader election does not provide fencing tokens, so there is no hard guarantee the old leader's in-flight API writes are rejected.

**Recommendation:** Elevate the ADR-TBD to a named, tracked decision with an explicit acceptance test: run a chaos test that kills the leader mid-claim at high concurrency and verify zero double-claims. Add a fencing layer (e.g., a `SandboxClaim` generation field incremented by the claiming controller, rejected by a validating webhook if it doesn't match).

### K8S-002 RBAC Does Not Enforce Field-Level Ownership of CRD Fields [High]
**Section:** 4.6.3

The spec presents a "CRD field ownership" table backed by RBAC + validating admission webhooks, but Kubernetes RBAC operates at resource and subresource granularity, not at individual field granularity. The actual Kubernetes-native mechanism for field-level ownership is Server-Side Apply (SSA) with named field managers.

**Recommendation:** Adopt Server-Side Apply as the primary enforcement mechanism for CRD field ownership. Each controller should apply its owned fields using a named field manager; the validating webhook becomes a defense-in-depth backstop.

### K8S-003 PSS Admission Policy Webhook Failure Mode Not Specified [High]
**Section:** 17.2, 5.3

The spec disables PSS `enforce` mode in favor of RuntimeClass-aware admission policies (OPA/Gatekeeper or Kyverno) but does not specify whether these webhooks run in `Fail` or `Ignore` mode. If unavailable, pods can be scheduled without security constraints.

**Recommendation:** Explicitly specify `failurePolicy: Fail` for all RuntimeClass-aware admission policy webhooks. Document minimum-availability SLO for the admission policy webhook deployment. Consider keeping PSS `enforce` mode active as defense-in-depth for the baseline `restricted` profile.

### K8S-004 agent-sandbox Upstream Maturity Risk Understated [High]
**Section:** 4.6

The one-release-delay upgrade cadence is insufficient for a v0.x project where breaking API changes between minor versions are expected. The fallback plan mentions "internal minimal implementation" but does not specify what triggers this decision.

**Recommendation:** Define explicit go/no-go criteria for the agent-sandbox dependency: API stability targets, community support SLOs, and a decision gate at end of Phase 1.

### K8S-005 GC Loop Inside Gateway Competes with Request Serving at Scale [High]
**Section:** 4.6

The 60-second orphan detection GC loop runs as a goroutine inside the gateway. At Tier 3 scale, this runs API server list operations across the agent namespace, and all gateway replicas run GC simultaneously.

**Recommendation:** Move the GC loop to a leader-elected goroutine inside WarmPoolController, or gate it behind a leader election lease shared among gateway replicas.

### K8S-006 etcd Compaction Flags Unavailable on Managed Kubernetes [Medium]
**Section:** 4.6.1

Recommended etcd tuning flags cannot be changed on managed Kubernetes offerings (GKE, EKS, AKS).

**Recommendation:** For managed deployments, document that etcd compaction is provider-controlled. Cap CRD write frequency per session and emit a metric tracking etcd write rate.

### K8S-007 Controller Anti-Affinity Is Advisory [Medium]
**Section:** 4.6.1, 4.6.2

Using `preferredDuringSchedulingIgnoredDuringExecution` means both controllers can land on the same node under resource pressure.

**Recommendation:** Change to `requiredDuringSchedulingIgnoredDuringExecution` for production deployments.

### K8S-008 agent-sandbox CRD Presence Not Validated at Controller Startup [Medium]
**Section:** 4.6

No startup check verifies agent-sandbox CRDs are installed and at the expected version.

**Recommendation:** Add a `validateAgentSandboxCRDs()` startup check alongside `validateRuntimeClasses()`.

### K8S-009 CRD Schema Version Graduation Path Not Defined [Medium]
**Section:** 4.6

No CRD API version graduation path is defined. Conversion webhooks are required for serving multiple versions simultaneously.

**Recommendation:** Define the initial API version for all Lenny-owned CRDs and specify conversion webhook deployment for schema-breaking changes.

### K8S-010 Kata Node Pool Taint Not Verified Against Agent Scheduling Path [Medium]
**Section:** 5.3, 17.2

No post-scheduling validation ensures pods created with the `kata` RuntimeClass actually land on Kata-tainted nodes.

**Recommendation:** Add a post-scheduling validation step and alert (`KataIsolationViolation`) if a pod's node lacks the expected taint.

### K8S-011 Dedicated CoreDNS Failure Has No Graceful Degradation Path [Low]
**Section:** 13.2

No graceful degradation path if the dedicated CoreDNS instance is unavailable.

**Recommendation:** Deploy with minimum 2 replicas and a PodDisruptionBudget. Add a `DNSIsolationDegraded` alert.

### K8S-012 ResourceQuota Default Values Not Specified [Low]
**Section:** 17.2

No default ResourceQuota and LimitRange values are specified per agent namespace.

**Recommendation:** Provide reference values for each tier in the Helm chart. Include quota exhaustion as an alerting condition.

---

## 2. Security & Threat Modeling

### SEC-001 Intra-Pod MCP Connections Are Completely Unauthenticated [High]
**Section:** 4.7, 15.4.3

The local MCP servers (platform MCP, connector MCP servers) are reachable by any process inside the pod via abstract Unix socket (`@lenny-platform-mcp`) with no authentication. A compromised runtime child process could call `lenny/delegate_task` to spawn arbitrary child sessions, use `lenny/memory_write` to poison the memory store, or trigger `lenny/request_elicitation` to phish the user.

**Recommendation:** Apply the same nonce-based connection handshake already defined for the lifecycle channel to the platform MCP server and connector MCP servers. Require the connecting process to present the manifest nonce on MCP `initialize` handshake.

### SEC-002 Lease Token Not SPIFFE-Bound in Proxy Mode — Cross-Pod Replay Risk [High]
**Section:** 4.9

Lease tokens for the LLM proxy are not bound to the pod's SPIFFE identity. A leaked token can be replayed from any pod with a valid mTLS certificate. Deferred to "post-v1" but critical for multi-tenant deployments.

**Recommendation:** Implement SPIFFE-binding for proxy mode lease tokens in v1. Bind each lease token to the issuing pod's SPIFFE URI and validate on every LLM proxy request.

### SEC-003 Prompt Injection via Unchecked Delegation File Exports [High]
**Section:** 8.7, 13.5

No content inspection of exported files during delegation. A compromised parent agent can craft files containing adversarial prompt injection content (e.g., `CLAUDE.md`) that bypass `contentPolicy.interceptorRef` which only covers `TaskSpec.input`.

**Recommendation:** Add a `fileExportPolicy` field to `DelegationPolicy` with a `PreFileExport` interceptor phase. Document that workspace files received via delegation should be treated as untrusted input.

### SEC-004 Isolation Monotonicity Enforced at Delegation Time Only [High]
**Section:** 8.3

A tag-based `DelegationPolicy` rule may match pools with varying isolation levels. A new `standard` pool registered with matching labels silently becomes a monotonicity-violation enabler. The `lenny-ctl policy audit-isolation` CLI is not a continuous check.

**Recommendation:** Add server-side continuous enforcement: when a new pool is registered, proactively evaluate all active `DelegationPolicy` resources against the new pool's isolation profile and emit warnings.

### SEC-005 Task-Mode Scrub Residual State Vectors Not Surfaced to Clients [High]
**Section:** 5.2

Clients creating sessions have no visibility into whether their session runs on a task-mode pod with residual state from prior tasks (DNS cache, TCP TIME_WAIT, page cache). The `acknowledgeBestEffortScrub` is deployer-level only.

**Recommendation:** Expose execution mode and isolation profile in the session creation response. Add a `sessionIsolationLevel` field to session metadata.

### SEC-006 Agent-Initiated URL-Mode Elicitation Allowlist Has No Domain Constraint [Medium]
**Section:** 9.2

The per-pool allowlist for agent-initiated URL-mode elicitations has no required `domainAllowlist`, making it an unrestricted phishing enabler for any pool that opts in.

**Recommendation:** Define the pool-level allowlist structure to require a `domainAllowlist` that the gateway validates for all agent-initiated URL-mode elicitations.

### SEC-007 Session Inbox Message Origin Authentication Not Explicitly Documented [Medium]
**Section:** 7.2

The mechanism by which the gateway enforces that `from` fields on `lenny/send_message` cannot be forged is not specified as a security invariant.

**Recommendation:** Explicitly document that `from` is always set by the gateway from the calling session's authenticated identity and that `messagingScope` is enforced using authenticated identity.

### SEC-008 Webhook callbackUrl DNS Pinning Has No Re-Validation Policy [Medium]
**Section:** 14

DNS pinning at registration time has no specified TTL or refresh policy. The `dryRun` interaction with DNS resolution is also ambiguous.

**Recommendation:** Specify that the pinned IP is validated both at session creation and at callback delivery time. Clarify `dryRun` behavior for `callbackUrl`.

### SEC-009 gVisor SO_PEERCRED Validation Deferred with No Startup Check [Medium]
**Section:** 4.7

`SO_PEERCRED` on abstract Unix sockets under gVisor may not behave identically to the Linux kernel. If silently unavailable, UID verification degrades to nonce-only with no observable failure.

**Recommendation:** Add a mandatory `SO_PEERCRED` loopback test at adapter startup. If it fails, the adapter must log FATAL and refuse to start.

### SEC-010 No Content-Type Validation on Uploaded Files [Medium]
**Section:** 7.4

No MIME type validation or content-type verification for uploaded files. Polyglot files could trigger unexpected behavior in runtimes that parse based on extension.

**Recommendation:** Add server-side content-type sniffing for all uploads. Compare detected MIME type against expected type for the file extension.

### SEC-011 Session Inbox Has No Durability — Silently Lost on Gateway Restart [Medium]
**Section:** 7.2

Inter-session messages are permanently lost if the coordinating gateway crashes. This enables message suppression attacks on security-critical signals like `cancel_child`.

**Recommendation:** For security-critical messages (cancel signals, cascade signals), make the inbox backed by durable Redis rather than in-memory-only.

### SEC-012 Admin API Bootstrap Endpoint Audit Logging Not Explicitly Specified [Medium]
**Section:** 15.1

No explicit specification that `POST /v1/admin/bootstrap` calls are fully audit-logged with the acting service account identity. Bootstrap can silently downgrade isolation profiles.

**Recommendation:** Explicitly specify that every bootstrap call is audit-logged, the bootstrap Job uses a minimal-RBAC ServiceAccount, and optionally support a `signatureRef` field for payload integrity.

### SEC-013 Semantic Cache Allows Cross-Session Information Disclosure [Medium]
**Section:** 4.9

The cache key space doesn't always include `user_id`, only when user-scoped credentials are used. Two users within the same tenant making semantically similar queries could receive each other's cached responses.

**Recommendation:** Always include `user_id` in the cache key. Add a `cacheScope` field (global-tenant, per-user, per-session). Default to per-user.

### SEC-014 `publishedMetadata` Stored and Served Without Validation — Stored XSS Risk [Low]
**Section:** 5.1

Public metadata served at `GET /v1/runtimes/{name}/meta/{key}` without authentication is opaque pass-through, creating a stored XSS vector.

**Recommendation:** Add `Content-Security-Policy: default-src 'none'` and `Content-Disposition: attachment` headers on all metadata responses.

### SEC-015 Rate-Limit Fail-Open Window Not Tenant-Aware for New Session Quota [Low]
**Section:** 12.4

If `replica_count` falls back to `1`, each replica enforces the full tenant limit independently, allowing N × tenant_limit during dual failure.

**Recommendation:** Add a deployment-level hard cap on max sessions per replica during fail-open, independent of per-tenant limit.

### SEC-016 Concurrent-Workspace `/tmp` Shared Across All Slots [Low]
**Section:** 5.2

Any file written to `/tmp/` by one slot's task is visible to all concurrent slots, with no slot-private temp directory.

**Recommendation:** Create per-slot temp directories (e.g., `/workspace/slots/{slotId}/.tmp/`) and set `TMPDIR` environment variable per slot.

### SEC-017 Orphan Detach Sessions Not Counted Against Quota [Low]
**Section:** 8.10

Detached orphan pods are not counted toward concurrency quota during the detach window (default 1 hour), enabling unbounded pod consumption.

**Recommendation:** Count orphaned detached pods against the originating user's pod concurrency quota immediately upon root session entering terminal state.

### SEC-018 Adapter Manifest Nonce Write Timing Unspecified [Info]
**Section:** 4.7

The spec does not specify whether the nonce is first written in the placeholder manifest (step 3) or only in the final manifest (step 6).

**Recommendation:** Specify that the nonce is written only in the final manifest write (step 6). The placeholder manifest should contain a sentinel value that the adapter rejects.

---

## 3. Network Security & Isolation

### NET-001 Unrestricted Port Range on Pod-to-Gateway Egress [Critical]
**Section:** 13.2

The `allow-pod-egress-base` NetworkPolicy permits agent pods to reach the gateway on any port and any protocol. No `ports` stanza is present, allowing probing of admin, debug, and metrics endpoints.

**Recommendation:** Add an explicit `ports` list limiting to TCP 50051 (gRPC) and TCP 8443 (LLM proxy for proxy-mode pools only).

### NET-002 Cloud IMDS Endpoint Not Blocked for Agent Pods with Internet Egress [Critical]
**Section:** 13.2

The `internet` egress profile adds `0.0.0.0/0` but `169.254.169.254` (IMDS) is reachable directly from node-local processes. A compromised agent pod could retrieve node IAM credentials.

**Recommendation:** Add explicit `ipBlock` deny for `169.254.169.254/32` to every egress profile policy. Also block `fd00:ec2::254` (IPv6 IMDS) and `100.100.100.200` (Alibaba IMDS).

### NET-003 Mutable Pod Label Used as NetworkPolicy Selector [High]
**Section:** 13.2

`lenny.dev/managed: "true"` can be mutated at runtime by any principal with `patch` on pods. Adding this label to a rogue pod grants gateway connectivity.

**Recommendation:** Enforce label immutability via admission webhook that prevents adding the label to pods not created by the warm pool controller.

### NET-004 mTLS Not Enforced on Gateway-to-Redis and Gateway-to-PgBouncer Paths [High]
**Section:** 10.3, 13.2

NetworkPolicy is L3/L4 only and cannot enforce TLS negotiation. Without a service mesh, a misconfigured gateway could connect to Redis in plaintext.

**Recommendation:** Configure Redis with `tls-auth-clients yes`, run a startup mTLS probe, and add integration tests asserting plaintext connections are rejected.

### NET-005 Lease Tokens Not Bound to Pod SPIFFE Identity [High]
**Section:** 10.3, 4.9

A leaked lease token can be replayed from any pod with a valid mTLS certificate. The SPIFFE-based identity model is undermined.

**Recommendation:** Promote SPIFFE-binding from "future hardening" to pre-GA requirement. Bind lease tokens to the pod's SPIFFE URI at issuance time.

### NET-006 Provider-Direct Egress Profile May Allow LLM Traffic to Bypass Gateway Proxy [High]
**Section:** 13.2, 4.9

A pod with `provider-direct` egress and `proxyMode: true` has a direct network path to LLM provider CIDRs, bypassing the gateway proxy.

**Recommendation:** Enforce mutual exclusivity between `proxyMode: true` and `egressProfile: provider-direct` via admission webhook.

### NET-007 Dedicated CoreDNS Security Hardening Underspecified [Medium]
**Section:** 13.2

No specification of CoreDNS plugins, rate-limit thresholds, response filtering semantics, or DNSSEC configuration.

**Recommendation:** Specify the CoreDNS Corefile configuration. At minimum: forward to internal resolver, ratelimit per-client, log all queries, ACL to deny DNS-tunnel record types.

### NET-008 PgBouncer-to-Postgres NetworkPolicy Missing [Medium]
**Section:** 13.2

No policy governing PgBouncer egress to Postgres. The spec is ambiguous about whether lenny-system enforces default-deny-all.

**Recommendation:** Add `allow-pgbouncer-egress` and `allow-postgres-egress` policies. Enumerate all intra-lenny-system traffic flows in a table.

### NET-009 Internet Egress CIDR Exclusions Depend on Manually Maintained Helm Values [Medium]
**Section:** 13.2

Cluster-specific CIDRs may change with upgrades. Misconfigured values allow agent pods to reach internal cluster IPs directly.

**Recommendation:** Automate CIDR extraction. Add a validating webhook that checks `except` blocks cover RFC-1918 ranges.

### NET-010 LLM Proxy Port 8443 Accessible to All Managed Pods [Medium]
**Section:** 4.9, 13.2

Port 8443 is universally accessible even to pools where `proxyMode: false`.

**Recommendation:** Define two egress policy variants: base (50051 only) and llm-proxy (adds 8443), applied based on pool `proxyMode`.

### NET-011 Single Dedicated CoreDNS Instance Creates DNS SPOF [Low]
**Section:** 13.2

CoreDNS crash affects all agent pods simultaneously. No replica count or HPA configuration specified.

**Recommendation:** Run with `replicas: 2` minimum, PodDisruptionBudget, and topology spread constraints.

### NET-012 Gateway Pod Label Is Mutable [Low]
**Section:** 13.2

`lenny.dev/component: gateway` can be applied to a rogue pod in lenny-system to intercept agent connections.

**Recommendation:** Protect lenny-system with restrictive RBAC. Only the gateway's own service account should create pods.

### NET-013 No Alerting Defined for NetworkPolicy Modifications [Info]
**Section:** 13.2

No runtime monitoring or alerting for modifications to NetworkPolicy objects.

**Recommendation:** Define an audit policy rule for CREATE/UPDATE/PATCH/DELETE on NetworkPolicy resources in all Lenny-managed namespaces.

### NET-014 MCP-Facing External Listener Network Path Not Documented [Info]
**Section:** 13.2

The north-south path (client → gateway) is not specified in the NetworkPolicy section.

**Recommendation:** Document the Service type, exposed ports, and source IP allowlist mechanism for external client access.

---

## 4. Scalability & Performance Engineering

### SCL-001 All Gateway Subsystem Extraction Thresholds Are TBD Estimates [Critical]
**Section:** 4.1

Every extraction threshold trigger is labeled "TBD: validate in Phase 13.5" with no empirical basis or sensitivity analysis. The gateway is the single synchronous path for all client interactions.

**Recommendation:** Run Phase 2 benchmark harness against a monolithic gateway. Record actual p99 latency at 25/50/75/100% of Tier 2 load. Set thresholds with 20% headroom.

### SCL-002 No Per-Gateway-Replica Session Capacity Budget [Critical]
**Section:** 4.1, 16.5

Nowhere is there a stated maximum number of concurrent sessions a single gateway replica can handle. The HPA cannot be correctly dimensioned.

**Recommendation:** Define a per-replica session capacity budget derived from load testing. Use this as the primary HPA custom metric.

### SCL-003 Startup Latency SLOs Are Unvalidated Estimates [High]
**Section:** 6.3

SLOs are derived from per-phase latency budget tables built from estimates, not measurements. Phase 2 benchmark harness is planned but not yet delivered.

**Recommendation:** Block Tier 2 promotion on completion of the Phase 2 startup benchmark harness with actual P50/P95/P99 measurements.

### SCL-004 Redis Sentinel Scalability Ceiling [High]
**Section:** 12.4

A single Redis primary cannot be horizontally scaled for writes. Tier 3 delegation budget serialization and per-session locks could saturate it.

**Recommendation:** Quantify Redis write throughput requirements at Tier 3 load. Pre-plan Redis Cluster migration as a Tier 2→3 transition step.

### SCL-005 HPA Custom Metric Pipeline Lag Not Formally Bounded [High]
**Section:** 10.1

End-to-end latency of the custom metric pipeline (Prometheus scrape + adapter + HPA) can add 30-90 seconds of lag. At 200 sessions/second, 6,000-18,000 attempts arrive under-provisioned.

**Recommendation:** Document the full pipeline latency. Consider KEDA with shorter polling intervals. Implement leading-indicator metrics.

### SCL-006 Postgres Write Path Has No Horizontal Scaling Route [High]
**Section:** 12.3

Tier 3 write IOPS (~1,300/s) approaches the practical write ceiling of a single Postgres primary with no stated strategy for scaling beyond.

**Recommendation:** Define a write load shedding strategy. Partition high-volume append-only tables to a separate instance. Classify writes as SLO-critical vs. best-effort.

### SCL-007 PoolScalingController Cold-Start Formula Cannot Auto-Configure [High]
**Section:** 4.6.2

Historical traffic metrics are unavailable at first deployment. No cold-start default, convergence criteria, or bootstrap override is specified.

**Recommendation:** Specify explicit cold-start bootstrap: fallback `minWarm` value, convergence criteria, operator-facing override API, and a bootstrap-mode metric/alert.

### SCL-008 Delegation Budget Redis Lua Script Serialization [Medium]
**Section:** 8.3

Lua scripts block all other Redis commands during execution. At high delegation fan-out, O(depth × breadth) invocations are serialized.

**Recommendation:** Benchmark Lua script throughput under simulated delegation fan-out. Consider sharding budget keys by session root ID at Tier 3.

### SCL-009 Experiment Targeting Webhook on Session Creation Hot Path [Medium]
**Section:** 10.7

Synchronous webhook with 200ms timeout on 200/s creation path. No result caching specified.

**Recommendation:** Add a client-level experiment assignment cache with short TTL. Define circuit breaker behavior. Consider pre-fetching assignments.

### SCL-010 Session Inbox In-Memory With No Durability and 500-Message Cap [Medium]
**Section:** 7.2

In-memory inbox with 500-message cap. Lost on gateway crash. Overflow behavior unspecified.

**Recommendation:** Persist inbox to Redis. Define explicit overflow behavior (reject-with-error preferred). Emit a metric when cap is approached.

### SCL-011 etcd Write Pressure Mitigations Speculative at Tier 3 [Medium]
**Section:** 4.6.1

CRD status update volume at Tier 3 creates substantial etcd write pressure without quantified validation.

**Recommendation:** Audit all CRD controllers for status update frequency. Implement debouncing if estimate exceeds 50% of etcd write capacity.

### SCL-012 Delegation Tree Per-Node Memory Estimate Is First-Principles Only [Low]
**Section:** 8.2

12KB per-node estimate derived from capacity reasoning, not profiling. Underestimate could break legitimate deep-recursion workflows.

**Recommendation:** Profile actual memory footprint with `pprof` heap profiler. Add a runtime metric tracking actual tree memory.

### SCL-013 Checkpoint Duration SLO Unvalidated for Full-Tier Runtime Pause [Low]
**Section:** 4.4

2s P95 target unvalidated for gVisor vs runc. During checkpoint, session pod is paused and streaming is interrupted.

**Recommendation:** Benchmark checkpoint duration for both runc and gVisor at multiple workspace sizes. Establish separate SLOs per runtime class.

### SCL-014 SSE Event Buffer Aggregate Memory Unquantified [Low]
**Section:** 7.2

Worst-case 10,000 × 10MB = 100GB SSE buffer memory at Tier 3. No gateway-level memory cap.

**Recommendation:** Add a gateway-level hard cap on total SSE buffer memory per replica (e.g., 2GB). Apply back-pressure to slow consumers.

---

## 5. Protocol Design & Future-Proofing

### PRT-001 Elicitation Chain is Structurally MCP-Only — Would Break Under A2A [High]
**Section:** 9.2, 8.2, 4.7

The entire elicitation chain is built on MCP's hop-by-hop model. A2A has no equivalent. Any delegation tree generating elicitations at depth >= 2 will degrade when surfaced via A2A.

**Recommendation:** In Section 21.1, explicitly define how elicitation chains are surfaced via A2AAdapter before implementation begins.

### PRT-002 ExternalProtocolAdapter Interface Missing Outbound Push Contract [High]
**Section:** 15, 21.1, 21.3

The interface has no mechanism for adapters to push subsequent state changes to registered webhook URLs after the initial response. `OutboundCapabilitySet` schema is not defined.

**Recommendation:** Define `OutboundCapabilitySet` concretely. Add an `OutboundChannel` mechanism for asynchronous event delivery.

### PRT-003 MCP Tasks Dependency — Core Session Lifecycle Uses MCP-Specific Concept [High]
**Section:** 7.2, 4.1, 9.1

The gateway's session lifecycle is modeled as an MCP Task at the architectural layer, not just the adapter layer. If MCP Tasks evolves, gateway internals are affected.

**Recommendation:** Clarify that "Lenny canonical task state machine" is the internal model. Replace "the client interacts via an MCP Task" with protocol-neutral language.

### PRT-004 publishedMetadata Has No Schema Validation or Versioning [Medium]
**Section:** 5.1, 21.1

"A2A card auto-generation" claim conflicts with "opaque pass-through" design. No content negotiation or versioning mechanism exists.

**Recommendation:** Clarify whether auto-generation is write-time or read-time. Add a `version` query parameter for protocol-specific retrieval.

### PRT-005 MCP Version Compatibility Creates Hard Deprecation Cliff [Medium]
**Section:** 15.2, 15.5

MCP Tasks and Elicitation are core dependencies. If a future MCP version substantially changes these features, gateway internals are affected, not just the adapter.

**Recommendation:** Distinguish between MCP spec version negotiation (adapter-layer) and MCP feature dependency (core-layer) in Section 15.2.

### PRT-006 Intra-Pod MCP Hardcodes MCP as Platform Tool Protocol [Medium]
**Section:** 4.7, 5.1

Any runtime wanting platform primitives must speak MCP intra-pod. No intra-pod A2A equivalent exists.

**Recommendation:** In Section 21.2, define the mapping from platform MCP tools to A2A equivalents before implementation.

### PRT-007 OpenAI Completions Adapter Has No Session Lifecycle [Medium]
**Section:** 15, 4.1, 7.2

OpenAI Chat Completions is stateless-per-request. Delegation trees, interruption, and multi-turn sessions are invisible to OpenAI callers.

**Recommendation:** Add a capability declaration to `AdapterCapabilities` specifying which lifecycle operations each adapter supports. Document unsupported operations.

### PRT-008 Agent Protocol Adapter Has No Design [Low]
**Section:** 21.3, 15

A single-sentence mention with no schema mappings, state machine translations, or capability notes.

**Recommendation:** Either provide a minimal design comparable to A2A's Section 21.1, or downgrade from "Post-V1 planned" to "future consideration."

### PRT-009 OutputPart Translation Fidelity Documents Lossy Paths But No Error on Loss [Low]
**Section:** 15.4.1

No mechanism for callers to detect that a round-trip through A2A degraded an OutputPart.

**Recommendation:** Add a `translationLoss` annotation to MessageEnvelope when outbound adapter drops or flattens fields.

### PRT-010 No Protocol-Level Session Affinity Hint for Adapter Switching [Low]
**Section:** 7.1, 15

No hint tells clients which adapter surface provides full fidelity for a given session.

**Recommendation:** Add a `supportedAdapters` list to the session record returned in `GET /v1/sessions/{id}`.

---

## 6. Developer Experience (Runtime Authors)

### DXP-001 Standalone Adapter Specification Promised but Not Published [Critical]
**Section:** 15.4

The spec states this will be "the primary document for community runtime adapter authors" but it does not exist yet. Runtime authors hit a hard blocker.

**Recommendation:** Either publish the standalone spec before community readiness, or promote Section 15.4 tables to be self-sufficient with wire encoding details and error codes.

### DXP-002 Echo Runtime Is Pseudocode Only, Not Runnable [High]
**Section:** 15.4.4

Cannot be compiled or run. Developers cannot distinguish "my runtime is wrong" from "my Lenny setup is wrong."

**Recommendation:** Provide at minimum one runnable Echo runtime in Go under `examples/runtimes/echo/`.

### DXP-003 Minimum-Tier Degraded Experience Not Consolidated [High]
**Section:** 15.4.3, 15.4.5

Capabilities lost at Minimum tier (no checkpoint, no interrupt, no delegation, no MCP tools) are scattered across multiple sections.

**Recommendation:** Add a dedicated "Minimum-tier limitations" callout in Section 15.4.3 enumerating every unavailable capability.

### DXP-004 OutputPart Rationale Does Not Show MCP Mapping [High]
**Section:** 15.4.2

No side-by-side mapping between MCP content blocks and OutputPart fields. `from_mcp_content()` helper has no concrete home.

**Recommendation:** Add a mapping table. Confirm whether `from_mcp_content()` ships as part of a Go SDK or is a copy-paste pattern.

### DXP-005 Runtime Author Roadmap Buried Deep [Medium]
**Section:** 15.4.5

The reading-order guide is at Section 15.4.5 with no forward reference from the introduction.

**Recommendation:** Add a "For Runtime Authors: Start Here" callout in Section 1 linking to Section 15.4.5.

### DXP-006 Local Dev Mode Does Not Document Custom Runtime Plugging [Medium]
**Section:** 17.4

No guidance on how to substitute a custom runtime binary into `make run` or `docker compose up`.

**Recommendation:** Add a "Plugging in a custom runtime" subsection showing the exact config change or `make` variable.

### DXP-007 Admin API Runtime Registration Missing from Getting-Started [Medium]
**Section:** 15.4.3, 15.4.5

How to register a runtime with the platform (prerequisite for any test) is not referenced in the Minimum-tier summary or Echo example.

**Recommendation:** Add a "Step 0: Register your runtime" paragraph with a concrete `kubectl apply` or Admin API call example.

### DXP-008 Abstract Unix Socket Transport Is Linux-Only [Medium]
**Section:** 15.4

Abstract Unix sockets are not supported on macOS. No macOS development path documented.

**Recommendation:** Add a platform compatibility note and document a concrete macOS developer workflow using Docker.

### DXP-009 Credential File Schema Undocumented for Runtime Authors [Low]
**Section:** 15.4.3

The exact schema of `/run/lenny/credentials.json` is not documented in runtime author sections.

**Recommendation:** Add a `credentials.json` schema block with a minimal example.

### DXP-010 Client SDK vs. Runtime SDK Distinction Not Explicit [Low]
**Section:** 15.4

Two distinct SDK concepts appear without clear separation. Runtime authors may be unsure if they need a Lenny SDK dependency.

**Recommendation:** Add an explicit statement that no SDK dependency is required for any tier.

### DXP-011 `from_mcp_content()` Helper Location Undocumented [Low]
**Section:** 15.4.2

No package, module, or availability information for the conversion utility.

**Recommendation:** Provide the import path or mark as "not yet published" with a copy-paste snippet.

### DXP-012 Heartbeat Failure Consequence Not Prominent [Info]
**Section:** 15.4.3

The 10-second window for heartbeat ACK is easy to miss. A blocking message handler can inadvertently cause SIGTERM.

**Recommendation:** Add a note that heartbeat handling must be non-blocking — read from stdin continuously regardless of processing time.

---

## 7. Operator & Deployer Experience

### OPS-001 CRD Upgrade Requires Manual Out-of-Band Step [Critical]
**Section:** 10.5

Helm does not update CRDs on `helm upgrade`. No tooling, pre-upgrade hook, or runbook makes this step reliable. Stale CRDs cause "silent failures."

**Recommendation:** Provide a `lenny-upgrade` script that diffs CRDs, applies them, waits for establishment, then triggers `helm upgrade`. Extend lenny-preflight to assert CRD version currency.

### OPS-002 Tier 2 Local Dev Default Is Plain HTTP [Critical]
**Section:** 17.4

The Docker Compose tier defaults to "no mTLS" — the mTLS code path is never exercised in development.

**Recommendation:** Add a `make compose-tls` variant. Mark plain-HTTP as unsupported for TLS-related development.

### OPS-003 Expand-Contract Phase 3 Has No Enforcement Gate [Critical]
**Section:** 10.5

No mechanism enforces the Phase 3 verification condition. An operator who deploys Phase 3 prematurely silently drops columns with live data.

**Recommendation:** Encode verification as a required migration prerequisite. The migration runner should query a count expression and abort if nonzero.

### OPS-004 No Operational Runbooks Exist [High]
**Section:** 17.7

Referenced runbook sections are either empty or not present. No step-by-step recovery procedure for any failure condition.

**Recommendation:** Produce minimum viable runbooks for: pool drain, stuck session eviction, failed migration rollback, PgBouncer saturation, Redis split-brain, cert-manager failure.

### OPS-005 minWarm Pool Gap at First Deployment [High]
**Section:** 5.1, 5.2, 4.6

No defined bootstrap behavior for pools with zero pods. First session requests experience cold-start latency with no user-visible signal.

**Recommendation:** Define explicit bootstrap behavior: `PoolWarmingUp` condition on Pool CRD, `503 Pool Not Ready` with `Retry-After` when zero warm pods.

### OPS-006 etcd Tuning Is Outside Lenny's Scope on Managed K8s [High]
**Section:** 12.3, 17

No matrix distinguishing managed Kubernetes (tuning impossible) from self-managed (operator responsibility).

**Recommendation:** Add a per-topology matrix listing what tuning Lenny requires vs. what the provider handles.

### OPS-007 Bootstrap Seed Job Has No Idempotency Documentation [High]
**Section:** 15.1, 17

Not specified whether running the bootstrap Job twice produces duplicates, no-ops, or errors.

**Recommendation:** Specify upsert semantics. Define that initial admin credential is written to a Kubernetes Secret with a documented rotation procedure.

### OPS-008 Credential Pool Kubernetes Secret Topology Underspecified [High]
**Section:** 13, 15.1

Whether each credential is a separate Secret, shares one Secret, or is database-backed with a Secret encryption key is unspecified.

**Recommendation:** Add a section defining credential storage topology and the KMS key hierarchy.

### OPS-009 Rolling Pool Rotation Procedure Is Incomplete [High]
**Section:** 10.5, 5.1

No rotation state machine, drain handling, pause capability, or schema migration interaction documented.

**Recommendation:** Define a `RuntimeUpgrade` state machine (Pending → Expanding → Draining → Contracting → Complete) with a pause command.

### OPS-010 lenny-ctl CLI Is Underspecified [Medium]
**Section:** Throughout

Referenced in multiple places but never defines its command surface.

**Recommendation:** Add a `lenny-ctl` command reference appendix mapping commands to admin API endpoints.

### OPS-011 Tier 1 SQLite Semantic Gaps Not Documented [Medium]
**Section:** 17.4

SQLite has well-known semantic differences from Postgres (no `FOR UPDATE SKIP LOCKED`, different JSONB) that can mask bugs.

**Recommendation:** Add a "Tier 1 Limitations" subsection listing stubbed features and untestable behaviors.

### OPS-012 Helm Values Schema Lacks Defaults Rationale [Medium]
**Section:** 17, 5.1, 5.2

Many configurable parameters without documented defaults or sizing guidance for first deployment.

**Recommendation:** Add a values reference appendix with defaults, rationale, and "start here" recommendations per scale tier.

### OPS-013 Scale-to-Zero Cron Timezone Handling Unspecified [Medium]
**Section:** 5.2

Not specified whether cron expressions are UTC, configurable, or cluster-local. Minimum Kubernetes version for timezone not documented.

**Recommendation:** Specify UTC default with optional `timezone` field. Document minimum K8s version 1.27 for timezone support.

### OPS-014 cert-manager Dependency Version Not Pinned [Medium]
**Section:** 10.3, 17

No minimum cert-manager version, required CRDs, or installation ordering specified.

**Recommendation:** Add cert-manager as an optional Helm dependency. Specify minimum version 1.12.0. Add a preflight check.

### OPS-015 No Procedure for Promoting Tier 2 Config to Production [Low]
**Section:** 17.4, 17.1

No export/import format for operational-plane configuration between dev and production.

**Recommendation:** Define an operational config export format applicable via `lenny-ctl apply -f`.

### OPS-016 agent-sandbox Upstream Upgrade Path Not Addressed [Low]
**Section:** 4.6

No compatibility matrix, version tracking, or emergency patch procedure for the upstream dependency.

**Recommendation:** Add dependency upgrade policy specifying version range, Helm dependency, and emergency patch procedure.

---

## 8. Multi-Tenancy & Tenant Isolation

### TNT-001 RLS Silently Disabled Under Cloud-Managed Poolers [Critical]
**Section:** 12.3

Cloud-managed poolers (AWS RDS Proxy, GCP Cloud SQL Auth Proxy) don't support `connect_query`. Without the per-transaction trigger (only created when `postgres.connectionPooler = external` Helm flag is set), tenant isolation rests entirely on application correctly issuing `SET LOCAL`.

**Recommendation:** Make `external` pooler mode the default when any cloud-managed pooler is detected. Add a startup check that refuses to proceed without the flag. Add an integration test that deliberately skips `SET LOCAL` and asserts no cross-tenant row is readable.

### TNT-002 Redis DLQ Keys Lack Explicit Tenant Namespace [High]
**Section:** 7.2, 12.4

DLQ keys use `session_id:dlq` format without the required `t:{tenant_id}:` prefix. A DLQ processor operating across all keys could read messages belonging to a different tenant.

**Recommendation:** Document canonical DLQ key format as `t:{tenant_id}:session:{session_id}:dlq`. Extend `TestRedisTenantKeyIsolation` to cover DLQ keys.

### TNT-003 Task-Mode Tenant Pinning Enforced Only at Application Layer [High]
**Section:** 5.2

No Kubernetes-layer mechanism prevents a task-mode pod labeled for tenant A from being assigned to tenant B if gateway logic has a bug.

**Recommendation:** Label warm-pool pods with `lenny.io/tenant-id`. Add an admission webhook that rejects tenant label changes from non-`unassigned` values.

### TNT-004 `noEnvironmentPolicy` Platform-Default Not Explicitly Specified [High]
**Section:** 10.6

A reader cannot determine whether omitting `noEnvironmentPolicy` results in `deny-all` (safe) or `allow-all` (unsafe).

**Recommendation:** Add explicit normative statement: "The platform default is `deny-all`." Add a validation webhook that emits a warning when set to `allow-all`.

### TNT-005 Semantic Cache Tenant Isolation Has No Runtime Enforcement [Medium]
**Section:** 4.9, 9.4

Contract tests verify tenant scoping at development time but no runtime mechanism enforces the `tenant_id` invariant at call time.

**Recommendation:** Introduce a `TenantScopedMemoryStore` wrapper that automatically prepends `tenant_id` to every cache key at the interface boundary.

### TNT-006 Tenant Deletion Has No SLA or Overdue Alert [Medium]
**Section:** 12.8

Full tenant deletion has no documented completion SLA and no `TenantDeletionOverdue` alert.

**Recommendation:** Define a tenant deletion SLA. Add a `TenantDeletionOverdue` alert when a tenant has been in `Deleting` state beyond the SLA.

### TNT-007 Detached Orphan Sessions Create Quota Accounting Bypass [Medium]
**Section:** 8.10, 11.2

Detached child sessions continue consuming resources but fall outside quota enforcement.

**Recommendation:** Count detached sessions toward tenant-level resource quota. Add `maxDetachedSessions` limit per tenant.

### TNT-008 EvictionStateStore RLS Coverage Not Confirmed [Medium]
**Section:** 4.6, 12.3

`session_eviction_state` table is not explicitly enumerated in the RLS-protected tables list.

**Recommendation:** Add `session_eviction_state` to the explicit list of RLS-protected tables. Include in RLS integration test suite.

### TNT-009 MinIO Tenant Isolation Is Prefix-Only [Medium]
**Section:** 4.5

No MinIO bucket policy or IAM policy independently restricts credentials to a single tenant's prefix. A path traversal bug exposes cross-tenant access.

**Recommendation:** Create per-tenant MinIO service accounts with bucket policies scoped to `/{tenant_id}/*`.

### TNT-010 RBAC Model Documents 5 Roles But Reviews Reference 3 [Low]
**Section:** 10.2

The role model was expanded from 3 to 5 roles but external documentation was not updated. `billing-viewer` scope boundaries are undocumented.

**Recommendation:** Update all documentation to reference the 5-role model. Document `billing-viewer` scope boundaries.

### TNT-011 Concurrent-Workspace Cross-Tenant Reuse Not Addressed [Low]
**Section:** 5.2, 5.3

No explicit prohibition of cross-tenant reuse for workspace-mode pods.

**Recommendation:** Add explicit normative statement: "Workspace-mode pods are never reused across tenants."

### TNT-012 RLS and Redis Isolation Tests Are Requirements, Not Implementations [Info]
**Section:** 9.4, 12.3, 12.4

Named test suites are documented as requirements with no indication they exist or are tracked.

**Recommendation:** Promote to explicit acceptance criteria on v1 milestone. Add CI step that fails if test suites are absent.

---

## 9. Storage Architecture & Data Management

### STR-001 Redis Quota Fail-Open Allows Full-Tenant Budget on Single Replica [Critical]
**Section:** 11.2, 12.4

During dual outage (Redis + Endpoints), `replica_count` falls back to 1. With N replicas, total exposed spend is `N × tenant_limit` before fail-closed triggers.

**Recommendation:** Store last-known replica count in a local variable. Add a hard cap: `effective_ceiling = min(tenant_limit / max(cached_count, 1), per_replica_hard_cap)`.

### STR-002 Audit Event Batching Silently Loses Events on Gateway Crash [Critical]
**Section:** 12.2

250ms batch window can silently drop up to ~75 events on gateway crash. No dead-letter or WAL mechanism exists for the audit buffer.

**Recommendation:** Default to synchronous writes unconditionally for T3/T4 audit events. Make batching explicit opt-in with documented data-loss warning.

### STR-003 T4 Workspace Data on emptyDir Lacks Per-Tenant Key Isolation [High]
**Section:** 6.4, 12.8

Node-level disk encryption doesn't provide per-tenant key isolation for T4 sessions. Node compromise exposes all T4 tenants on that node.

**Recommendation:** Require T4 workloads to run on dedicated nodes via `nodeSelector` with a dedicated KMS key per node.

### STR-004 Storage Quota Enforcement Mechanism Undefined [High]
**Section:** 11.2, 12.5

No mechanism described for checking or enforcing storage quota at upload or checkpoint time. MinIO OSS has no built-in per-prefix quota.

**Recommendation:** Define explicit enforcement: pre-upload size check against Redis counter, post-upload increment, GC-triggered decrement.

### STR-005 Checkpoint Scaling Bottleneck Under Large Workspaces [High]
**Section:** 4.4, 12.5

500MB workspaces can take 5-10s for checkpoint during which the agent is entirely unresponsive. Incremental checkpoints deferred.

**Recommendation:** Enforce a hard emptyDir size limit on `/workspace/`. Add `lenny_checkpoint_duration_seconds` histogram. Document quiescence-to-workspace-size relationship.

### STR-006 Node Drain + MinIO Unavailability Creates Cascading Checkpoint Failure [High]
**Section:** 4.4, 12.5

Concurrent node drain and MinIO degradation causes all checkpoint attempts to fail. Standard/Minimum tier fallback is silent failure.

**Recommendation:** Add a pre-drain webhook that checks MinIO health before allowing pod eviction. Store a compressed workspace index in Postgres as fallback.

### STR-007 Legal Hold Does Not Protect Checkpoint History [Medium]
**Section:** 12.5, 12.9

Legal hold doesn't suspend the "latest 2 checkpoints" rotation policy. Intermediate checkpoints are destroyed.

**Recommendation:** Extend legal hold to include `preserve_checkpoints: true`. When set, GC skips checkpoint rotation for that session.

### STR-008 64KB last_message_context Contradicts TOAST Prohibition [Medium]
**Section:** 4.4, 12.1

The 64KB TEXT field triggers TOAST, contradicting the "never Postgres for blobs" principle.

**Recommendation:** Replace with MinIO-backed fallback. Store only the object key in Postgres. Truncate to 2KB on MinIO unavailability.

### STR-009 No Shared RWX Storage Non-Goal Not Validated [Medium]
**Section:** 3.3, 4.5

No evidence that gateway-mediated file delivery is sufficient for actual agent workflows (code execution, data analysis, multi-step pipelines).

**Recommendation:** Conduct and document a workflow validation exercise against three reference architectures.

### STR-010 GC Job Cadence Is Fixed and Not Tier-Configurable [Medium]
**Section:** 12.6

Fixed 15-minute cycle. T4 tenants with strict deletion SLAs may need sub-5-minute deletion.

**Recommendation:** Make GC interval configurable (default 15min, minimum 1min). Add per-tenant `gc_priority` flag.

### STR-011 Semantic Cache Custom Implementations Lack Enforcement [Medium]
**Section:** 12.4

No runtime isolation layer or proxy enforces tenant-scoped queries on third-party backends.

**Recommendation:** Implement a thin semantic cache proxy inside the gateway that enforces `tenant_id` scoping.

### STR-012 MinIO Delete Markers May Accumulate [Low]
**Section:** 12.5, 12.6

With bucket versioning, deleting old checkpoints creates delete markers that degrade list-objects performance.

**Recommendation:** Add a MinIO lifecycle policy expiring delete markers older than 24 hours. Document versioning strategy per prefix.

### STR-013 DLQ Is Redis-Backed and Unavailable During Redis Failure [Low]
**Section:** 12.4

Redis failure means the DLQ is also unavailable — exactly when it's most needed.

**Recommendation:** Document as known limitation. Consider a two-tier DLQ: Redis fast path + Postgres overflow.

---

## 10. Recursive Delegation & Task Trees

### DEL-001 Stale Summary Contradicts Section 8.6 on Rejection Permanence [High]
**Section:** 8.6, 19

Section 19 says "rejection is permanent for the subtree" but Section 8.6 defines a recoverable cool-off period (`rejectionCoolOffSeconds`, default 300s).

**Recommendation:** Correct Section 19 to read: "rejection triggers a cool-off period for the denied subtree."

### DEL-002 Deep Tree Recovery (Depth 5+) with Multiple Simultaneous Failures Unspecified [High]
**Section:** 8.10

Serial recovery across 5 levels consumes exactly `maxTreeRecoverySeconds` (600s), leaving zero margin. Multi-node failure across non-adjacent depths is unaddressed.

**Recommendation:** Add guidance that deployers using deep trees should increase `maxTreeRecoverySeconds`. Specify behavior for non-adjacent failures.

### DEL-003 `credentialPropagation: inherit` Multi-Hop Semantics Undefined [High]
**Section:** 8.3

The spec never specifies whether `credentialPropagation` applies per-hop or is tree-wide. `LeaseSlice` includes no credential-scoping field.

**Recommendation:** Explicitly state whether it governs the immediate hop or applies recursively. Add a worked example across a 3-level tree with mixed modes.

### DEL-004 `detach` Cascade Policy — Detached Children's Own Cascade Behavior [Medium]
**Section:** 8.10

When a detached child subsequently fails, behavior of its own `cascadeOnFailure` policy is unspecified. Budget return on orphan completion is undefined.

**Recommendation:** Specify that detached orphans retain their own cascade policies. Budget return on orphan completion is a no-op.

### DEL-005 Cross-Environment Delegation Doesn't Specify Monotonicity Check [Medium]
**Section:** 8.3, 10.6

The cross-environment enforcement path has no mention of isolation monotonicity checking.

**Recommendation:** Explicitly state that cross-environment delegation is subject to the same isolation monotonicity enforcement. Extend `lenny-ctl policy audit-isolation` for cross-environment combinations.

### DEL-006 `maxTreeRecoverySeconds` Default Shorter Than `maxResumeWindowSeconds` [Medium]
**Section:** 8.10, 7.3

Tree recovery timeout (600s) is shorter than individual resume window (900s). Leaf-node failures that would be individually recoverable are force-terminated.

**Recommendation:** Add deployer guidance with a formula for `maxTreeRecoverySeconds >= maxResumeWindowSeconds + (maxDepth - 1) × maxLevelRecoverySeconds`.

### DEL-007 Rejection Cool-Off and Budget Return Interaction [Medium]
**Section:** 8.6, 8.3

Whether returned budget from completing children can be used for re-delegation during the cool-off period is unspecified.

**Recommendation:** Clarify whether the cool-off applies only to extension requests or also to normal delegation within already-granted budget.

### DEL-008 `await_children(mode="any")` Leaves Children Running Unbounded [Medium]
**Section:** 8.5, 8.8

Remaining children continue consuming tokens and pod slots without auto-cancellation. `perChildMaxAge` applicability not stated in this context.

**Recommendation:** Add a note that `perChildMaxAge` continues to bound non-cancelled children. Add guidance to cancel remaining children promptly.

### DEL-009 No Cycle Detection in Delegation Target Resolution [Medium]
**Section:** 8.2, 8.3

`maxDepth` prevents infinite depth but not graph-level cycles (A→B→A→B). `lenny/discover_agents` doesn't filter out ancestors.

**Recommendation:** Specify whether the gateway performs lineage-based cycle prevention. Consider adding `DELEGATION_CYCLE_DETECTED` error code.

### DEL-010 Detach Orphans Not Counted Toward Quota [Low]
**Section:** 8.10

No deployment-level cap on total active orphaned pod-hours per tenant.

**Recommendation:** Add `maxOrphanTasksPerTenant` limit enforced at parent failure time.

### DEL-011 `treeUsage` Returns `null` for In-Progress Trees [Low]
**Section:** 8.8

No visibility into cumulative tree-wide token consumption until the entire tree settles.

**Recommendation:** Add a `cumulativeUsage` field to `lenny/get_task_tree()` response showing live approximate token usage per node.

### DEL-012 No Observability Metrics for Tree Recovery Events [Low]
**Section:** 8.10, 16.1

No metrics for tree recovery operations, force-failed nodes, or recovery success rate.

**Recommendation:** Add `lenny_delegation_tree_recovery_total` counter and `DelegationTreeRecoveryFailure` alert.

---

## 11. Session Lifecycle & State Management

### SLC-001 Derive From Live Session — No Isolation of In-Progress Workspace State [Critical]
**Section:** 7.1

No on-demand checkpoint at derive time. Source checkpoint may be arbitrarily stale (up to 10 minutes). Concurrent derives have no locking or ordering guarantee.

**Recommendation:** Add a session-level lock for concurrent derives. Consider rejecting derive on `running` sessions unless `allowStale: true` is passed.

### SLC-002 Generation Counter Fencing Window [Critical]
**Section:** 10.1

If `CoordinatorFence` fails all retries, the generation counter has already been incremented. The next coordinator arrives with a skipped generation. Old coordinator's in-flight changes may still land.

**Recommendation:** Specify generation increment as a CAS operation. Define cleanup actions on receiving a fenced generation value higher than expected+1.

### SLC-003 SSE Buffer Overflow — Drop Connection Without Guaranteed Replay Window [High]
**Section:** 7.2

Events beyond the buffer are replayed from EventStore "if within the checkpoint window" — but the replay window is not defined. Events outside the window are client data loss.

**Recommendation:** Define replay window explicitly. Include an `events_lost` field in the `checkpoint_boundary` marker. Document this as a client data-loss event.

### SLC-004 Checkpoint Failure During SIGSTOP — Watchdog Restarts Without SIGCONT Confirmation [High]
**Section:** 4.4

No OS primitive confirms the process actually received SIGCONT and resumed. In pathological cases, the agent can be frozen beyond the 60-second window.

**Recommendation:** After SIGCONT, poll `/proc/{pid}/stat` for transition out of stopped state within 5 retries. Set `checkpointStuck = true` immediately on failure.

### SLC-005 `awaiting_client_action` Children — Pending Results Not Guaranteed After Parent Resumption [High]
**Section:** 7.3

Child completion events buffered in the in-memory virtual child interface are lost if the coordinating replica crashes.

**Recommendation:** Persist child completion events to `session_tree_archive` rather than holding only in-memory. On parent resumption, replay from archive.

### SLC-006 Session Inbox vs. DLQ Inconsistency — Different Durability for Same Flow [High]
**Section:** 7.2

Messages buffered in the inbox at the moment of pod failure are lost, while messages arriving moments later (after `resume_pending` transition) are durably stored in the DLQ.

**Recommendation:** Define inbox-to-DLQ migration path: when session transitions to `resume_pending`, drain inbox into DLQ atomically. Back DLQ with Postgres for `awaiting_client_action` sessions.

### SLC-007 `maxSessionAge` Timer Behavior During Recovery States Unspecified [High]
**Section:** 6.2

Timer behavior during `resuming`, `resume_pending`, and `awaiting_client_action` states is not specified.

**Recommendation:** Explicitly specify timer behavior in each state. Paused during all recovery states, resumed only when `running` or `attached`.

### SLC-008 Task-Mode Cleanup Timeout Formula Produces Sub-Minimum Values [Medium]
**Section:** 5.2

With `cleanupTimeoutSeconds: 30` and `maxConcurrent: 8`, per-slot timeout is 3.75s below the documented 5s minimum. The 5s minimum precedence is not stated.

**Recommendation:** Clarify 5s minimum takes precedence. Track leaked slot count per pod. Add CRD validation rejecting `cleanupTimeoutSeconds < 5`.

### SLC-009 `task_cleanup → idle` With `scrub_warning` — Next Task May See Prior Files [Medium]
**Section:** 6.2, 4.4

Workspace materialization does not specify whether it first clears the workspace on a pod with `scrub_warning` annotation.

**Recommendation:** Specify that workspace materialization always begins with a workspace reset before applying the workspace plan.

### SLC-010 `input_required` in Delegation — `await_children` Re-Subscribe After Parent Resume [Medium]
**Section:** 8.8

After parent pod recovery, the parent's `await_children` call is lost. The gateway must replay archived child results on re-issued call.

**Recommendation:** Specify the "re-await protocol": parent re-issues `await_children`, gateway streams archived settled results before entering live-wait.

### SLC-011 Derive Endpoint — OAuth Tokens Not Inherited but No Indication of Re-Auth Need [Medium]
**Section:** 7.1

Derived sessions start with no connector tokens. No proactive notification that re-authorization will be required.

**Recommendation:** Add to the derive response a list of connectors that were active on the source session.

### SLC-012 `resuming` Timeout Interaction With `coordinatorHoldTimeoutSeconds` [Medium]
**Section:** 6.2, 10.1

Pod terminates after 120s hold, but session stays in `resuming` for up to 135s before retry kicks in.

**Recommendation:** The adapter should update `Sandbox` CRD status to `failed` on `coordinator_lost`. Gateway health monitoring should detect the broken connection independently.

### SLC-013 SDK-Warm Demotion Race During `sdk_connecting` [Medium]
**Section:** 6.1

Timing window between `sdk_connecting` and `idle` is not fully addressed for `DemoteSDK` calls.

**Recommendation:** Specify that a pod only transitions to `idle` after SDK handshake is complete. Define `DemoteSDK` error response if SDK is not yet connected.

### SLC-014 Concurrent Checkpoint and Interrupt — Coalesce Semantics Unspecified [Medium]
**Section:** 4.7

Coalescing an eviction checkpoint with a periodic checkpoint could silently downgrade the eviction's retry budget.

**Recommendation:** Coalesced checkpoint should preserve the most urgent trigger's retry budget and fallback behavior.

### SLC-015 `cancelled` Terminal State Missing From Some State Machines [Low]
**Section:** 6.2, 7.2, 8.8

Canonical task state machine doesn't show `suspended → cancelled` or `awaiting_client_action → cancelled`.

**Recommendation:** Add all non-terminal → cancelled transitions to the canonical task state machine. Cross-reference all three state machines.

### SLC-016 Session `created` State — No Maximum TTL [Low]
**Section:** 15.1

A session in `created` state holds a claimed pod and credential lease indefinitely. No `maxCreatedStateTimeoutSeconds` defined.

**Recommendation:** Define `maxCreatedStateTimeoutSeconds` (e.g., 300s). Release pod and credentials on timeout.

### SLC-017 Generation Counter Maximum Value Unspecified [Info]
**Section:** 10.1

Data type and maximum value for `coordination_generation` not specified.

**Recommendation:** Specify `int64` in the Postgres schema and document overflow is not a realistic concern.

### SLC-018 Derive Endpoint State Preconditions Contradict Between Sections [Info]
**Section:** 7.1, 15.1

Section 7.1 permits derive from `running`/`suspended`/`resuming`. Section 15.1 restricts to terminal states only.

**Recommendation:** Reconcile the two sections. If live sessions are permitted, update 15.1. If restricted, update 7.1.

---

## 12. Observability & Operational Monitoring

### OBS-001 Multiple Alerts Referenced but Missing from Section 16.5 [High]
**Section:** 4.6.1, 5.3, 13.2, 16.5

At least 8 alerts defined in body sections are absent from the canonical alert table: `WarmPoolIdleCostHigh`, `SandboxClaimOrphanRateHigh`, `EtcdQuotaNearLimit`, `FinalizerStuck`, `CosignWebhookUnavailable`, `DedicatedDNSUnavailable`, `AuditGrantDrift`, `SIEMDeliveryDegraded`.

**Recommendation:** Add all referenced alerts to Section 16.5. Mark it as the single source of truth.

### OBS-002 No SLO Error-Budget Burn-Rate Alerting [High]
**Section:** 16.5

Seven SLOs defined but only threshold-based alerts — no multi-window burn-rate rules. Operators learn about violations only after the fact.

**Recommendation:** Add burn-rate alerting: fast-window (1h, 14× rate) and slow-window (6h, 3× rate) for key SLOs.

### OBS-003 Delegation Tree Memory Metrics Missing [High]
**Section:** 8.2, 16.1

No Prometheus metric for tree memory utilization, rejection count, or distribution of footprints. Trees rejected for memory exhaustion are invisible.

**Recommendation:** Add `lenny_delegation_tree_memory_bytes`, `lenny_delegation_memory_budget_utilization_ratio`, and `lenny_delegation_tree_memory_rejection_total` metrics.

### OBS-004 No Tracing Span for Budget Operations [Medium]
**Section:** 8.2, 16.3

Budget reservation and return Lua scripts have no OTel spans. Contention and rejection causes are invisible in traces.

**Recommendation:** Add `delegation.budget_reserve` and `delegation.budget_return` spans with outcome attributes.

### OBS-005 Warm Pool Claim Latency Metric Name Inconsistency [Medium]
**Section:** 16.1, 17.8.2

`lenny_pod_claim_queue_wait_seconds` vs `lenny_warmpool_claim_wait_seconds_p99` — two names for the same concept.

**Recommendation:** Designate one canonical name. Add a separate total claim latency metric covering queue wait + lock acquisition.

### OBS-006 No Metrics for Memory Store Operations [Medium]
**Section:** 9.4, 16.1

No entries for memory store operation latency, error rates, or record counts.

**Recommendation:** Add `lenny_memory_store_operation_duration_seconds`, `lenny_memory_store_errors_total`, and `lenny_memory_store_record_count` metrics.

### OBS-007 Task-Mode and Concurrent-Mode Metrics Gaps [Medium]
**Section:** 5.2, 16.1

No histogram for task execution duration, `lenny_task_reuse_count` referenced but not in metrics table, no per-slot workspace materialization latency.

**Recommendation:** Add missing metrics to Section 16.1. `lenny_task_reuse_count` is used in the scaling formula and must appear in the canonical table.

### OBS-008 PgBouncer Alerting Not in Section 16.5 [Medium]
**Section:** 12.3, 16.5

Section 12.3 describes PgBouncer metrics but no corresponding alerts exist in the canonical alert inventory.

**Recommendation:** Add `PgBouncerPoolSaturated` (Warning) and `PgBouncerAllReplicasDown` (Critical) to Section 16.5.

### OBS-009 Tracing Does Not Cover Elicitation Hop Latency Individually [Medium]
**Section:** 9.2, 16.3

Per-hop spans lack provenance attributes. Per-hop timeouts indistinguishable from overall timeouts.

**Recommendation:** Add `elicitation.delegation_depth`, `elicitation.hop_number` to span attributes. Add `lenny_elicitation_hop_timeout_total` metric.

### OBS-010 Warm Pool Waste Metric Incomplete [Medium]
**Section:** 4.6.1, 16.1

No idle-to-active ratio or claim-to-idle-recycle ratio metric. `lenny_warmpool_sdk_demotions_total` defined in body but absent from metrics table.

**Recommendation:** Add `lenny_warmpool_pod_claim_rate`, `lenny_warmpool_pod_idle_recycle_total`, and `lenny_warmpool_sdk_demotions_total` to Section 16.1.

### OBS-011 Session Error Rate by Failure Classification Not Exposed [Medium]
**Section:** 16.1, 7.3

No metric for terminal failures by classification (TRANSIENT/PERMANENT/POLICY/UPSTREAM).

**Recommendation:** Add `lenny_session_terminal_errors_total` with `error_classification`, `pool`, `runtime`, `tenant_id` labels.

### OBS-012 No Latency SLO Alert for TTFT [Medium]
**Section:** 16.5

SLO targets `TimeToFirstToken P95 < 10s` and `StartupLatency P95 < 2s` have no corresponding alerts.

**Recommendation:** Add `StartupLatencySLOBreach` and `TTFTSLOBreach` warning alerts.

### OBS-013 Delegation Tree Parent-Child Not in Log Correlation Fields [Low]
**Section:** 8.9, 16.3, 16.4

Log correlation fields lack `parent_session_id` and `root_session_id`. Delegation tree log filtering requires separate tree reconstruction.

**Recommendation:** Add `parent_session_id` and `root_session_id` as standard log correlation fields and OTel span attributes.

### OBS-014 Restore Test Metrics Not in Metrics Table [Low]
**Section:** 17.3, 16.1

`lenny_restore_test_success` and `lenny_restore_test_duration_seconds` referenced in Section 17.3 but absent from Section 16.1.

**Recommendation:** Add both metrics to Section 16.1. Add `RestoreTestFailed` alert to Section 16.5.

### OBS-015 No Observability for Deadlock Detection [Low]
**Section:** 9.2, 8.3

No metric, span, or alert for delegation deadlock detection events.

**Recommendation:** Add `lenny_delegation_deadlock_detected_total` counter and `DelegationDeadlockDetected` warning alert.

### OBS-016 Grafana Dashboard Specification Is Absent [Low]
**Section:** 16, 17.4

No specification of required dashboards, panels, or queries for production Grafana.

**Recommendation:** Add a Section 16.6 listing minimum required dashboards shipped with the Helm chart.

---

## 13. Compliance, Governance & Data Sovereignty

### CMP-001 SIEM Optional Breaks Compliance-Grade Audit Integrity [Critical]
**Section:** 11.7, 16.4

SIEM connectivity is optional with no enforcement gate. A deployer can run multi-tenant production with no tamper-proof audit trail and no warning. INSERT-only grants are trivially bypassed by superuser or `pg_dump`+restore.

**Recommendation:** Introduce compliance profile enforcement gate. Reject environment creation when `complianceProfile` is FedRAMP/HIPAA/SOC2 and SIEM is not configured.

### CMP-002 Data Residency Has No Runtime Validation Gate [Critical]
**Section:** 12.8, 4.2

When `dataResidencyRegion` is unset, the platform silently falls back to default single-region. No admission webhook or runtime gate prevents cross-region writes.

**Recommendation:** Define fail-closed behavior for unresolvable regions. Add ValidatingAdmissionWebhook rejecting resources where region is not in `storage.regions`.

### CMP-003 Audit Batching Applies Even When SIEM Is Configured [Critical]
**Section:** 11.7, 12.3

250ms batch window means gateway crash can lose events even with SIEM configured. HIPAA AU-9, FedRAMP AU-10, and SOC2 CC7.2 require completeness.

**Recommendation:** Write audit events synchronously to Postgres first, then forward to SIEM asynchronously via change-data-capture or outbox pattern.

### CMP-004 Legal Hold Does Not Prevent Checkpoint Rotation [High]
**Section:** 12.5, 12.8

Intermediate checkpoint state between the two most recent is permanently deleted even under legal hold. Could constitute spoliation.

**Recommendation:** Suspend all retention rotation policies when `legal_hold` is set. Add a reconciler detecting held sessions with rotated checkpoints.

### CMP-005 Billing Corrections Require No Dual-Control Approval [High]
**Section:** 11.2.1

A single `platform-admin` can unilaterally mutate billing records with only self-generated audit trail.

**Recommendation:** Require dual-control approval for billing corrections above configurable threshold. Implement four-eyes principle.

### CMP-006 GDPR Billing Pseudonymization Does Not Constitute Erasure [High]
**Section:** 12.8, 11.2.1

If the `erasure_salt` is retained in the same database, erasure is not achieved — data remains personal under GDPR Recital 26.

**Recommendation:** Delete the `erasure_salt` immediately after pseudonymization completes. Add verification step confirming derivation fails.

### CMP-007 Startup-Only Audit Grant Verification Insufficient [High]
**Section:** 11.7

5-minute background check is sufficient window for a superuser to grant, tamper, and revoke without detection.

**Recommendation:** Supplement with pgaudit logging to external append-only sink. Reduce check interval to 60s for regulated profiles.

### CMP-008 SOC2/HIPAA/FedRAMP Controls Not Systematically Mapped [Medium]
**Section:** 12.8, 12.9, 16.4

No controls mapping table exists. Deployers cannot perform gap analysis.

**Recommendation:** Add a compliance controls appendix mapping each framework to platform mechanisms. Flag unaddressed controls.

### CMP-009 KMS Key Residency Not Required to Match dataResidencyRegion [Medium]
**Section:** 12.8, 12.9, 4.3

Envelope encryption with a KMS endpoint in a different jurisdiction may still constitute a transfer of personal data.

**Recommendation:** Add `kmsRegion` field. Enforce KMS endpoint must be in same jurisdiction as storage region.

### CMP-010 Erasure SLA Has No Hard Stop on New Data Processing [Medium]
**Section:** 12.8

No mechanism halts new data processing for a subject with a pending erasure request. GDPR Article 18 requires restriction of processing.

**Recommendation:** Set `processing_restricted` flag on erasure request. Prevent new session creation for that subject.

### CMP-011 Task-Mode Scrub Residual Vectors Can Retain PHI/PII [Medium]
**Section:** 5.2

No enforcement mechanism routes PHI-processing tasks to dedicated pools. A HIPAA deployer could inadvertently route PHI to shared pools.

**Recommendation:** Add `dataClassification` field to AgentTask. When T3/T4, enforce routing to dedicated pools via admission control.

### CMP-012 No Records of Processing Activities (RoPA) Mechanism [Low]
**Section:** 12.8, 4.2

GDPR Article 30 requires RoPA but no aggregation, export, or reporting capability exists.

**Recommendation:** Add a RoPA export endpoint to the compliance API generating a structured report per tenant.

### CMP-013 Erasure Propagation to External Sinks Is Notification-Only [Low]
**Section:** 12.8

No verification that external sinks completed erasure. No retry mechanism for failed propagation.

**Recommendation:** Add erasure propagation tracking table with per-sink confirmation status and webhook callback mechanism.

### CMP-014 Audit Retention Presets Require Manual Selection [Info]
**Section:** 16.4

No automatic selection based on `complianceProfile`. Operator can set mismatched retention.

**Recommendation:** Auto-select retention preset based on `complianceProfile`. Validate at tenant creation time.

---

## 14. API Design & External Interface Quality

### API-001 Undocumented Endpoints Scattered Across Prose [High]
**Section:** 7.1, 7.2, 8.6, 12.8, 14

Several operations (artifact retention extension, webhook events, extension-denial deletion, legal-hold query) are in prose but absent from the REST API table.

**Recommendation:** Audit the spec for every imperative operation. Add every operation to the formal REST API table.

### API-002 Experiment Endpoint Method and Path Inconsistency [High]
**Section:** 10.7, 15.1

Section 10.7 describes `PATCH /v1/experiments/{id}` while Section 15.1 shows `PUT /v1/admin/experiments/{name}`.

**Recommendation:** Resolve to a single canonical endpoint. Use `PATCH /v1/admin/experiments/{id}`.

### API-003 Error Code `SCOPE_DENIED` Not in Catalog [High]
**Section:** 7.2, 15.1

Used in webhook delivery receipt but absent from the error code catalog.

**Recommendation:** Add `SCOPE_DENIED` to the error code catalog with category `POLICY`.

### API-004 Pagination Missing `total` Count [High]
**Section:** 15.1

Cursor-based pagination has no `total` field. UIs cannot render progress or "X results found."

**Recommendation:** Add optional `total` field to pagination envelope, present when cheaply computable.

### API-005 No PATCH Support for Admin Resources [Medium]
**Section:** 15.1

All updates are full-body PUT. No partial update mechanism for large resources like environments.

**Recommendation:** Add PATCH endpoints for complex resources using JSON Merge Patch (RFC 7396).

### API-006 ETag Delivery Inconsistency [Medium]
**Section:** 15.1

Single-item GET returns ETag in HTTP header; list GET embeds per-item `etag` in body. Two extraction strategies needed.

**Recommendation:** Always include ETag as a body field (`_etag`) in addition to the response header.

### API-007 `dryRun` Preview Only Specified for Environments [Medium]
**Section:** 15.1

Other admin endpoints that accept `dryRun` don't specify what the response body contains.

**Recommendation:** For each endpoint accepting `dryRun`, specify the dry-run response body contents.

### API-008 `RESOURCE_HAS_DEPENDENTS` Has No Dependent IDs [Medium]
**Section:** 15.1

Only count and type name included, not IDs. UIs cannot link to blocking resources.

**Recommendation:** Include a `dependents` array with `{id, name, type}` per blocking resource (capped at 20).

### API-009 Sortable Fields Not Enumerated [Medium]
**Section:** 15.1

`sort` parameter accepted but valid fields not listed per resource type.

**Recommendation:** Add `x-sortable-fields` to OpenAPI spec per list endpoint.

### API-010 OpenAPI Spec Publication Location Not Specified [Medium]
**Section:** 15.1, 15.5, 15.6

No well-known URL, format, or versioning policy for the OpenAPI document.

**Recommendation:** Specify `GET /openapi.yaml` as the well-known URL served from the gateway.

### API-011 `GET /v1/pools` vs `GET /v1/admin/pools` Ambiguity [Low]
**Section:** 15.1

Two pool list endpoints with no documented distinction.

**Recommendation:** Clarify: `/v1/pools` returns tenant-visible pools for routing; `/v1/admin/pools` returns all with admin detail.

### API-012 REST/MCP Contract Does Not Cover Admin API [Low]
**Section:** 15.2.1

Admin API is REST-only but this is not explicitly stated. Adapter authors may assume admin operations should be MCP tools.

**Recommendation:** Add explicit statement that admin API is REST-only and not part of the MCP surface.

### API-013 No Standardized Field Naming Convention [Low]
**Section:** 15.1

camelCase used but not declared. Inconsistencies exist (`hasMore` vs `dry_run`).

**Recommendation:** Add API conventions subsection. Audit all examples for consistency.

### API-014 Webhook Signature Not Versioned or Replay-Protected [Low]
**Section:** 15.1

No mechanism for algorithm rotation. No timestamp in signed payload for replay prevention.

**Recommendation:** Add `X-Lenny-Signature-Version` header and `X-Lenny-Timestamp` header with documented replay window.

### API-015 No API Changelog or Deprecation Notice Mechanism [Info]
**Section:** 15.5

No `Deprecation` header, `Sunset` header, or changelog publication policy.

**Recommendation:** Add deprecation signaling using RFC 8594 headers and OpenAPI extension fields.

---

## 15. Competitive Positioning & Open Source Strategy

### CPS-001 No Differentiation Narrative [Critical]
**Section:** 1, 2

No section answers "why Lenny instead of X?" Potential adopters cannot determine fit without deep landscape knowledge.

**Recommendation:** Add a "Why Lenny" section covering the gap Lenny fills, target personas, and explicit trade-offs.

### CPS-002 No Open Source Community Strategy [High]
**Section:** 1, 2, 15

Excellent extensibility primitives but no governance model, contributor ladder, release cadence, or community channels documented.

**Recommendation:** Produce GOVERNANCE.md and CONTRIBUTING.md as v1 launch artifacts.

### CPS-003 Upstream Dependency Risk Incompletely Assessed [High]
**Section:** 4.6.1

No governance health criteria, upstream contribution strategy, or SIG sponsorship assessment for `kubernetes-sigs/agent-sandbox`.

**Recommendation:** Add dependency risk section with health criteria, contribution commitment, and SIG identification.

### CPS-004 No Comparison to Adjacent Orchestration Systems [High]
**Section:** 1, 2

No mention of Temporal, Modal, LangGraph, Ray, or Dagger. No comparative analysis or positioning.

**Recommendation:** Add "Relationship to Adjacent Systems" subsection positioning Lenny relative to at minimum Temporal, Modal, and LangGraph.

### CPS-005 Extensibility Requires Go and gRPC Expertise [Medium]
**Section:** 4.7, 4.8, 15.4

Custom adapters, interceptors, and store implementations all require Go/gRPC. Python/TypeScript contributors cannot extend the platform.

**Recommendation:** Evaluate HTTP webhook variants for a subset of extension points. Document which are Go-only vs. polyglot.

### CPS-006 No Runtime Registry or Discovery Mechanism [Medium]
**Section:** 5.1, 15.4

No runtime marketplace or community catalog for runtime authors to publish and operators to discover.

**Recommendation:** Define a community runtime registry concept for v1 or post-v1.

### CPS-007 MCP Protocol Dependency Risk Not Assessed [Medium]
**Section:** 3.2, 4.7

Deep coupling to MCP with no assessment of governance relationship, version compatibility, or migration path if MCP stalls.

**Recommendation:** Add a protocol dependency section documenting MCP version targets, governance relationship, and fallback intent.

### CPS-008 No Licensing or OSS Governance Model [Medium]
**Section:** Throughout

No license, CLA policy, or open-core vs. fully-open statement. Pluggable interfaces suggest open-core potential.

**Recommendation:** Document license choice, CLA/DCO requirement, and commercial offering intent in GOVERNANCE.md.

### CPS-009 Local Development Experience Underspecified [Low]
**Section:** 5, 17

No minimum resource requirements, one-command bootstrap, or feature-disabling guidance for local clusters.

**Recommendation:** Add a "Local Development" section with minimum cluster requirements and quickstart YAML.

### CPS-010 No Flagship Demo Use Case [Low]
**Section:** 1, 2

No end-to-end example demonstrating Lenny's value in a compelling scenario.

**Recommendation:** Add 2-3 annotated example scenarios showcasing recursive delegation, multi-tenant isolation, and warm pool latency.

### CPS-011 Protocol-Agnostic publishedMetadata Is a Differentiator [Info]
**Section:** 5.1

Multi-protocol runtime publication is a technically elegant design worth highlighting.

**Recommendation:** Surface this capability in the executive summary and positioning materials.

### CPS-012 Three-Tier Runtime Ladder Is Positive [Info]
**Section:** 15.4, 5.1

Well-designed for progressive adoption. The tier model is a strong community on-ramp.

**Recommendation:** Ensure materials are front-loaded in documentation, not buried in Section 15.

---

## 16. Warm Pool & Pod Lifecycle Management

### WPL-001 SDK-Warm Pod Eviction During `sdk_connecting` Not Handled [Critical]
**Section:** 6.1, 6.2, 4.6.1

A pod in `sdk_connecting` state that is evicted has no specified cleanup behavior for the running SDK process.

**Recommendation:** Specify adapter behavior on SIGTERM during `sdk_connecting`: call `DemoteSDK` with bounded timeout, then terminate. Add state transition to diagram.

### WPL-002 Burst Formula Dimensionality Mismatch [High]
**Section:** 4.6.2, 5.2, 17.8.2

Section 4.6.2's formula produces `claims/second` (not pod count) for the first term. Section 17.8.2's formula is correct but inconsistent.

**Recommendation:** Reconcile formulas. Add `× (failover_seconds + pod_startup_seconds)` to the first term in Section 4.6.2.

### WPL-003 `sdkWarmBlockingPaths` Glob Semantics Unspecified [High]
**Section:** 6.1, 5.1

No specification of: path vs filename matching, case sensitivity, glob dialect, symlink resolution, or whether `workspaceDefaults` files count.

**Recommendation:** Specify matching contract precisely: relative path, case-sensitive, Go `path.Match` with `**`, no symlink resolution.

### WPL-004 `sdkWarmBlockingPaths` Demotion Negates SDK-Warm Benefit for Common Workloads [High]
**Section:** 6.1, 6.3

Default blocking paths (`CLAUDE.md`, `.claude/*`) will match virtually every real Claude Code project, triggering demotion on the majority of sessions.

**Recommendation:** Add `demotionRateThreshold` guidance. Consider a circuit-breaker that disables SDK-warm when demotion rate exceeds threshold.

### WPL-005 Variant Pool Formula Doesn't Reduce Base Pool [High]
**Section:** 4.6.2, 10.7

When a variant pool is created, the base pool's `minWarm` is not reduced to reflect diverted traffic. Total warm pods = `(1 + variant_weight) × original`.

**Recommendation:** Specify that PoolScalingController recomputes base pool's `minWarm` as `base_demand_p95 × (1 - Σ variant_weights) × safety_factor × time_window`.

### WPL-006 Scale-to-Zero With SDK-Warm Pools Not Defined [Medium]
**Section:** 4.6.1, 6.1

Behavior for `minWarm: 0` on SDK-warm pools: cleanup, scale-up path, and cold-start SLO all unspecified.

**Recommendation:** Specify graceful `DemoteSDK` before termination. Document cold-start latency for SDK-warm pools.

### WPL-007 `WarmPoolIdleCostHigh` Alert Missing from Section 16.5 [Medium]
**Section:** 4.6.1, 16.5

Alert referenced in Section 4.6.1 but absent from the canonical alert inventory.

**Recommendation:** Add to Section 16.5 with condition and threshold.

### WPL-008 Pod Finalizer Hold + PDB Can Deadlock Node Drains [Medium]
**Section:** 4.6.1, 6.2

PDB `minAvailable = minWarm` blocks eviction of pods needed for drain when pool has exactly `minWarm` pods.

**Recommendation:** Use `maxUnavailable: 1` formulation. WarmPoolController should proactively create replacements before approving evictions.

### WPL-009 `ConfigureWorkspace` RPC Semantics Underspecified [Medium]
**Section:** 6.1, 4.7, 7.1

No timeout, failure handling, or idempotency specification for the SDK-warm workspace reconfiguration RPC.

**Recommendation:** Add specification: 10s timeout, fallback to `DemoteSDK` + pod-warm path on failure, idempotent.

### WPL-010 Concurrent Warm Pool Formula Not Adjusted for Slot Detection Latency [Low]
**Section:** 5.2, 4.6.2

Controller adjustment responsiveness for `mode_factor` changes is not specified.

**Recommendation:** Define the averaging window and maximum downward adjustment per cycle. Add info event on material changes.

### WPL-011 Pool Fill Grace Period Not Applied to `WarmPoolLow` Alert [Low]
**Section:** 4.6.1, 16.5

`WarmPoolLow` fires during expected cold-start fill, creating noise at startup.

**Recommendation:** Apply same grace period suppression as `WarmPoolExhausted`.

### WPL-012 `WarmPoolReplenishmentSlow` References Unconfigurable Baseline [Low]
**Section:** 16.5

Alert threshold uses `pod_warmup_seconds` which has no canonical source or configurable field.

**Recommendation:** Add `pod_warmup_seconds` as an explicit field on pool definitions.

---

## 17. Credential Management & Secret Handling

### CRD-001 Lease TTL Undefined for `anthropic_direct` Direct Mode [High]
**Section:** 4.9

No explicit default lease TTL for provider types. `anthropic_direct` in direct mode has no provider-side expiry.

**Recommendation:** Define explicit default lease TTLs per `CredentialProvider` type. Add configurable `leaseTTLSeconds` on `CredentialPool`.

### CRD-002 `renewBefore` Has No Corresponding Renewal Mechanism [High]
**Section:** 4.9

No background process monitors active leases against `renewBefore` for proactive renewal. Expiry-driven rotation consumes `maxRotationsPerSession` budget.

**Recommendation:** Define a proactive renewal loop. Renewals triggered by `renewBefore` should NOT consume the rotation counter.

### CRD-003 Shared `maxRotationsPerSession` Conflates Fault and Proactive Rotations [Medium]
**Section:** 4.9

A 2-hour STS session uses 1 of 3 rotation slots for routine renewal, leaving only 2 for faults.

**Recommendation:** Split into `faultRotationCount` and `proactiveRenewalCount`. Apply `maxRotationsPerSession` only to faults.

### CRD-004 LLM Reverse Proxy Lease Token Not Bound to Pod Identity [High]
**Section:** 4.9

A compromised agent reading its `credentials.json` can replay the lease token through any gateway replica.

**Recommendation:** Bind lease tokens to pod's SPIFFE URI at `AssignCredentials` time. Validate on every proxy request.

### CRD-005 Credential Pool Exhaustion Has No Queuing [Medium]
**Section:** 4.9

Immediate `CREDENTIAL_POOL_EXHAUSTED` with no wait mechanism. Pre-claim check and assignment are not atomic, creating amplified retry cycles.

**Recommendation:** Add brief credential availability queue (2-5s configurable) when pool has sessions approaching completion.

### CRD-006 Direct-Mode + `standard` (runc) Only Warns, Not Blocked [High]
**Section:** 4.9, 5.3

Container escape gives attacker access to `credentials.json` on the host node. Only a warning event, not a hard rejection.

**Recommendation:** Make this a hard admission rejection in multi-tenant mode. Require explicit opt-in field.

### CRD-007 User-Scoped Credential Rotation and Revocation Not Described [High]
**Section:** 4.9

No endpoint for rotating a user-scoped credential or revoking active leases backed by it.

**Recommendation:** Add `POST /v1/credentials/{ref}/revoke` (user-facing) and `PUT /v1/credentials/{ref}` for rotation.

### CRD-008 KMS Leaves Pool Secrets Outside KMS Scope [Medium]
**Section:** 4.9, 10.5

Pool credentials in Kubernetes Secrets get weaker protection than user-scoped credentials in TokenStore. Both are T4 Restricted.

**Recommendation:** Provide `secretStoreMode: kms` option that stores pool credentials in TokenStore via KMS. Change preflight etcd check to blocking for `secretRef` mode.

### CRD-009 LLM Proxy Extraction Thresholds TBD [Medium]
**Section:** 4.1, 4.9

No concrete SLO for LLM proxy latency overhead. Extraction plan deferred to Phase 13.5.

**Recommendation:** Add SLO for proxy overhead (< 5ms p99). Provide capacity model mapping session count to GC pressure.

### CRD-010 Credential Rotation Mid-Session Gate Wait vs Timeout Race [Medium]
**Section:** 4.7

The `credentials_acknowledged` timeout may run concurrently with the unbounded in-flight gate wait, causing premature fallback.

**Recommendation:** Clarify that the 60s timeout starts only after `credentials_rotated` is sent. Add separate max gate wait duration.

### CRD-011 Three Credential Modes Not Clearly Differentiated for Operators [Medium]
**Section:** 4.9

Relationship between `preferredSource` fallback and per-provider pool fallback not illustrated.

**Recommendation:** Add decision tree, unified resolution order table, and worked examples for each mode.

### CRD-012 Credential Deny List Expiry Tied to Lease TTL, Not Revocation [Medium]
**Section:** 4.9

After deny list expires, a compromised credential could be reassigned if the underlying Secret hasn't been rotated.

**Recommendation:** Clarify that `revoked` status in `CredentialPoolStore` is permanent until explicitly re-enabled.

### CRD-013 KMS Key Rotation Doesn't Address In-Memory Cache [Low]
**Section:** 10.5, 4.3

In-memory decrypted material and old DEK during rolling upgrades are unaddressed.

**Recommendation:** Specify Token Service must not cache decrypted material beyond request scope. Add `lenny_token_service_leases_by_key_version` metric.

### CRD-014 `inherit` Mode Lacks Sibling Isolation [Low]
**Section:** 8.3, 4.9

N children with `inherit` mode collectively consume up to N × `maxRotationsPerSession` against the same pool.

**Recommendation:** Document and add warning. Consider tree-level rotation budget for `inherit` mode.

### CRD-015 Credential Audit Events Missing Structured Fields [Low]
**Section:** 11.7, 4.9

`leaseId`, `expiresAt`, `rotationMode`, `sessionId` referenced in prose but not formalized in audit event schema.

**Recommendation:** Extend audit event field table with complete specification for each credential event type.

---

## 18. Content Model, Data Formats & Schema Design

### SCH-001 OutputPart Type Registry Has No Formal Schema [Critical]
**Section:** 15.4.1

No formal registry document, no namespace convention for third-party types, no mapping from `(type, schemaVersion)` to concrete schema. Evolution is indistinguishable from envelope version bumps.

**Recommendation:** Define a formal type registry as a versioned document. Introduce `x-vendor/typeName` namespace convention for custom types.

### SCH-002 RuntimeDefinition Inheritance Rules Exist Only in Prose [Critical]
**Section:** 5.1

Merge semantics for `derived` runtimes exist entirely in prose with no formal algorithm. Conflicting fields (resources, network policy, capabilities intersection) have no worked examples.

**Recommendation:** Provide a normative merge algorithm table. Add at least two worked examples covering conflicts.

### SCH-003 CredentialLease `materializedConfig` Is Unschematized [Critical]
**Section:** 4.9

Provider-specific field with no schema registry, no validation contract, no documented encoding for sensitive values.

**Recommendation:** Define a schema per built-in credential provider using a discriminated union pattern keyed on `provider` type.

### SCH-004 MessageEnvelope `delivery` Field Underspecified for Multi-Turn [High]
**Section:** 15.4.1

Acknowledgement schema, `threadId`/`inReplyTo` DAG model, ordering guarantees, and delegation forwarding semantics all undefined.

**Recommendation:** Define acknowledgement schema, DAG model, and ordering guarantees. Add `delegationDepth` field.

### SCH-005 OutputPart Inline/Ref Duality Has No Resolution Protocol [High]
**Section:** 15.4.1

No size threshold, ref URI scheme, TTL policy, or consumer fallback behavior defined.

**Recommendation:** Define `LennyBlobURI` scheme. Document thresholds and TTL. Require adapters to dereference refs before producing external messages.

### SCH-006 WorkspacePlan `runtimeOptions` Has No Schema [High]
**Section:** 14

Free-form `map[string]any` with no per-runtime documentation. Env blocklist has no wildcard support.

**Recommendation:** Define `runtimeOptions` as per-runtime discriminated union. Add glob pattern support to env blocklist.

### SCH-007 Schema Versioning Conflates Live and Durable Consumers [High]
**Section:** 15.5

Rejection-at-read rule stated uniformly. Durable consumers rejecting unknown versions creates compliance gaps.

**Recommendation:** Bifurcate: live consumers MAY reject; durable consumers MUST forward-read. Define migration window SLA.

### SCH-008 Translation Fidelity Matrix Has No Lossiness Documentation [High]
**Section:** 15.4.1

No documentation of which source fields have no target equivalent, which round-trips are asymmetric, or how `thinking` parts translate to OpenAI/A2A.

**Recommendation:** Annotate each cell with `exact`, `lossy`, `unsupported`, or `extended` tags. Make lossiness explicit.

### SCH-009 DelegationLease Budget Fields Have No Overflow Semantics [Medium]
**Section:** 8.3

Whether budget is subtracted at creation or consumption, and cascade failure recovery semantics, are undefined.

**Recommendation:** Define as reservation model (subtracted at grant, refunded at completion). Document currency unit and cascade recovery.

### SCH-010 Capability Inference from MCP ToolAnnotations Has Counterintuitive Defaults [Medium]
**Section:** 5.1

Absent `openWorldHint` (default `false`) implies network not needed, but many tools simply omit the annotation. Silent capability downgrade.

**Recommendation:** Default to permissive inference. Add `capabilityInferenceMode` field (strict/permissive). Log warning on inferred defaults.

### SCH-011 BillingEvent `sequence_number` Gap-Detection Undefined [Medium]
**Section:** 11.2.1

Scope (per-session vs per-tenant), gap remediation protocol, and failover numbering behavior all undefined.

**Recommendation:** Define scope as per-tenant monotonic. Assign sequencing authority to a single service. Document gap detection policy.

### SCH-012 EvalResult `scores` JSONB Defeats Schema Enforcement [Medium]
**Section:** 10.7

Unschematized JSONB. Different evaluators use different keys and scales.

**Recommendation:** Define minimal required schema: `dimension`, `value`, `scale`, `weight`. Publish standard dimension vocabulary.

### SCH-013 Adapter Manifest `version` Has No Compatibility Contract [Medium]
**Section:** 4.7

No semver, compatibility validation, or minimum version requirement.

**Recommendation:** Require semver. Define `minPlatformVersion` field. Add manifest compatibility check at registration.

### SCH-014 OutputPart `annotations` Has No Namespace Convention [Low]
**Section:** 15.4.1

Free-form string-keyed map with no collision avoidance for third-party annotations.

**Recommendation:** Reserve `lenny.` prefix. Define `x-{vendor}.` convention.

### SCH-015 WorkspacePlan Has No Incremental Update Operations [Low]
**Section:** 14

No `deleteFile`, `patchFile`, or mid-session update operations defined.

**Recommendation:** Either document as initialization-only or add operation types with a `phase` field.

### SCH-016 TaskRecord.messages Has No Maximum Size [Low]
**Section:** 8.8

Array can grow unboundedly for long-running tasks with many turns.

**Recommendation:** Define `maxMessagesInline` limit. Add `messagesRef` for externalized archives.

### SCH-017 treeUsage Has No Schema for Non-Token Resources [Low]
**Section:** 8.8

Only token-count fields defined. No extensibility for wall-clock time, API calls, or storage bytes.

**Recommendation:** Define as extensible map `map[string]ResourceUsage` with standard keys.

### SCH-018 No Null vs Absent Field Semantics Defined [Info]
**Section:** Multiple

For inheritance-bearing fields, the difference between "explicitly null" (revoke inherited) and "absent" (inherit) is semantically significant but undefined.

**Recommendation:** Define spec-wide three-state model: set, null, absent. Enforce via CRD validation.

---

## 19. Build Sequence & Implementation Risk

### BLD-001 Authentication Comes After Real LLM Testing [Critical]
**Section:** 18

Phase 5.5 introduces real LLM credentials before Phase 7's policy engine. Real credentials injected into pods without production-grade admission, budget, and auth enforcement.

**Recommendation:** Gate real-LLM testing on auth-complete milestone. Move minimum viable policy enforcement to Phase 5.75.

### BLD-002 Security Audit Scheduled Too Late [Critical]
**Section:** 18

Phase 14 places security audit after the full platform is built. Cost of architectural findings is maximally high.

**Recommendation:** Insert targeted security design reviews after Phase 5.5 (credential injection) and Phase 9 (delegation attack surface).

### BLD-003 Echo Runtime Insufficient for Phase 6-8 Validation [Critical]
**Section:** 18

Echo runtime cannot produce streaming output, report token usage, or implement Full-tier lifecycle. Phases 6-8 cannot be milestone-validated in CI.

**Recommendation:** Promote "extended test runtime" to explicit Phase 2.8 deliverable implementing streaming, `ReportUsage`, and lifecycle channel.

### BLD-004 License Unresolved Before Community Engagement [High]
**Section:** 18, 23.2

Phase 2 promises `CONTRIBUTING.md` but license is unresolved. No assigned phase or ADR for license selection.

**Recommendation:** Assign license selection as Phase 1 gating item with ADR.

### BLD-005 SandboxClaim ADR Is Phase 1 Blocker With No Owner [High]
**Section:** 18, 4.6.1

ADR-TBD is marked as Phase 1 blocking prerequisite but has no timeline or deliverable.

**Recommendation:** Make it an explicit Phase 0 deliverable with a running integration test.

### BLD-006 KMS Deferred — Credentials Unencrypted for 7+ Phases [High]
**Section:** 18

Real credentials stored without envelope encryption from Phase 5.5 through Phase 12a. Preflight etcd check is non-blocking.

**Recommendation:** Make etcd encryption mandatory before Phase 5.5. Move Token Service multi-replica to Phase 5.5.

### BLD-007 Phases 12b and 12c Have No Stated Dependencies [High]
**Section:** 18

Concurrent execution modes and MCP runtimes introduced without integration-test gates against credential assignment and session initialization.

**Recommendation:** Require each to run Phase 13.5 performance baseline before merging.

### BLD-008 Phase 17 Bundles Too Much [High]
**Section:** 18, 23.2

MemoryStore, semantic caching, guardrails, eval hooks, documentation, and community guides in one phase.

**Recommendation:** Split into Phase 17a (documentation, governance, community) and Phase 17b (feature work).

### BLD-009 No Database Migration Phase [High]
**Section:** 18

17 phases introduce new data models but no phase establishes the migration framework, conventions, or CI gate.

**Recommendation:** Add Phase 1.5 establishing migration tool, initial schema, CI gate, and rollback documentation.

### BLD-010 Load Testing Baseline Comes After Full Build [High]
**Section:** 18

Phase 13.5 baseline measured on nearly production-ready system. Late findings require expensive rework.

**Recommendation:** Introduce incremental load testing after Phase 6, 9, and 11.

### BLD-011 Phases 6-8 Have No Sub-Phase Deliverables [Medium]
**Section:** 18

Among the most complex phases, described as single table rows with no intermediate checkpoints.

**Recommendation:** Break into sub-phases similar to earlier phases (2.5, 3.5, 4.5).

### BLD-012 `type: mcp` Runtime Warm Pool Interaction Unspecified [Medium]
**Section:** 18, 5, 6

Phase 12b introduces MCP runtimes but claim semantics, eviction, and scaling behavior are not described.

**Recommendation:** Add spec appendix describing MCP runtime lifecycle before Phase 12b implementation.

### BLD-013 Phase 15 (Environments) After Phases 11-12 Which Need Scoping [Medium]
**Section:** 18

User-scoped credentials and connectors are environment-scoped, but Environments come later.

**Recommendation:** Move Phase 15 before Phase 11 or document explicit rework items.

### BLD-014 Compliance Validation Has No Phase [Medium]
**Section:** 18, 11.7

No dedicated phase for audit integrity validation, SOC2 control mapping, or HIPAA chain verification.

**Recommendation:** Add Phase 14.1 (Compliance Validation) between security hardening and SLO re-validation.

### BLD-015 Parallelization Opportunities Not Identified [Medium]
**Section:** 18

Build sequence is strictly sequential but several phases could run in parallel (12a‖12b, 15‖14, 17a‖14.5).

**Recommendation:** Annotate with "parallelizable with" column. Define merge gates.

### BLD-016 Phase 16 Has No Load Testing Requirement [Medium]
**Section:** 18

Experiment routing modifies session routing paths but is never load-tested.

**Recommendation:** Add Phase 13.5 re-run with experiment routing enabled as Phase 16 deliverable.

### BLD-017 No Runbook Validation Phase [Low]
**Section:** 18, 17.7

Runbooks never tested against the real system.

**Recommendation:** Add runbook validation exercise to Phase 14: fire each alert condition and execute the runbook.

### BLD-018 Phase 2 Benchmark Has No SLO Failure-Response Plan [Low]
**Section:** 18, 6.3

No defined action if benchmarks reveal SLOs cannot be met.

**Recommendation:** Add Phase 2 decision gate: revise SLO or document optimization plan.

### BLD-019 Helm Chart Not Assigned to Any Phase [Low]
**Section:** 18, 17.6

No explicit owner or milestone for the Helm chart across 17 phases.

**Recommendation:** Add "Helm chart updated and tested" as deliverable for each phase adding K8s components.

---

## 20. Failure Modes & Resilience Engineering

### FLR-001 Dual-Store Concurrent Outage Leaves Sessions in Limbo [Critical]
**Section:** 10.1, 12.3, 12.4

When both Redis and Postgres are unavailable simultaneously, no writable store exists. No defined degraded mode.

**Recommendation:** Define explicit dual-store-down mode: reject new sessions, emit `PLATFORM_DEGRADED` to clients, surface alert.

### FLR-002 MinIO Outage During Node Eviction Causes Irrecoverable Loss [Critical]
**Section:** 12.5, 4.4

Checkpoint cannot be written to MinIO during eviction. Workspace irrecoverably lost with no fallback.

**Recommendation:** Add two-phase checkpoint fallback: attempt MinIO, on failure write minimal manifest to Postgres. Pre-eviction checkpoint at node taint time.

### FLR-003 Redis Fail-Open Creates Unbounded Financial Exposure [High]
**Section:** 12.4

N replicas × tenant_limit overshoot during Redis outage. Post-recovery reconciliation undefined.

**Recommendation:** Cap per-replica ceiling at `quota / min_replicas`. Define post-recovery reconciliation using per-replica usage logs.

### FLR-004 Rolling Updates Always Interrupt Long-Running Sessions [High]
**Section:** 10.1, 4.4

In-flight tool calls at checkpoint time have no defined behavior: abandoned, re-executed, or deduplicated?

**Recommendation:** Introduce `CheckpointBarrier` protocol. Add `tool_call_idempotency_key` for resume deduplication.

### FLR-005 Session Inbox Is In-Memory with No Durability [High]
**Section:** 7.2

Messages silently dropped on coordinator crash. No retry, no acknowledgment, no dead-letter path.

**Recommendation:** Move inbox to Redis list/stream with per-message TTL and explicit ACK step.

### FLR-006 Controller Crash Failover Margin Is Only 5s [High]
**Section:** 4.6.1

25s failover window vs 30s queue timeout. Under API server slowness, queue exhausts before recovery.

**Recommendation:** Increase `podClaimQueueTimeout` to 60s. Add Postgres-based fallback claim path.

### FLR-007 Postgres Failover Creates Billing Consistency Gap [High]
**Section:** 12.3

Billing buffer lost if gateway pod crashes during Postgres failover window. No buffer size limit or secondary sink.

**Recommendation:** Route billing events through Redis stream as intermediate buffer. Define overflow policy.

### FLR-008 Gateway preStop 30s Cap Abandons Large Workspaces [High]
**Section:** 10.1, 4.4

30s insufficient for sessions with hundreds of MB workspace. Partial checkpoint behavior undefined.

**Recommendation:** Add tiered checkpoint cap. Write partial manifest even when full upload can't complete.

### FLR-009 PoolScalingController CRD Drift Unbounded During Outage [Medium]
**Section:** 4.6.1

Controller crash leaves stale scaling targets. No staleness detection or alert.

**Recommendation:** Add `lastScalingUpdateTimestamp` to `AgentPool` CRD status. Alert on staleness.

### FLR-010 Postgres Failover + Batch Boundary Creates Audit Gap [Medium]
**Section:** 12.3

Batch flush and failover coincidence can lose a full batch with no detection.

**Recommendation:** Add sequence numbers to audit batches with gap-detection query on recovery.

### FLR-011 Redis Recovery Blocks All Delegation Budget Operations [Medium]
**Section:** 12.4

Behavior during reconciliation undefined: fail-open, fail-closed, or rate-limited?

**Recommendation:** Use last-known counters as conservative lower bound. Process active sessions first.

### FLR-012 Standard/Minimum Tier Best-Effort Checkpoint Undefined [Medium]
**Section:** 4.4

"Best-effort" has no numeric bounds. Clients get no indication of checkpoint staleness.

**Recommendation:** Define maximum checkpoint interval, max skipped checkpoints, and client-visible `checkpointAge` field.

### FLR-013 CoordinatorFence RPC Has No Timeout [Medium]
**Section:** 10.1

If old coordinator is unreachable, the fence call hangs indefinitely.

**Recommendation:** Define `coordinatorFenceTimeoutSeconds` (10s). Proceed on timeout relying on generation counter.

### FLR-014 Orphaned SandboxClaim Recovery Has 5-Minute Lag [Low]
**Section:** 4.6.1

Default resync periods of 5-10 minutes. Gateway rolling update could drain the warm pool.

**Recommendation:** Add `claimExpiryTimestamp` to SandboxClaim CRD. Check on every reconciliation tick.

### FLR-015 Coordinator Hold State Has No Client-Visible Signal [Low]
**Section:** 7.2

120s hold with no progress signal. Client cannot distinguish healthy hold from hung session.

**Recommendation:** Send synthetic MCP `progress` notification every 15s during hold. Allow per-session `delegationTimeout` override.

---

## 21. Experimentation & A/B Testing Primitives

### EXP-001 Health-Based Rollback Triggers Undefined [High]
**Section:** 10.7

No specification of what signals should prompt manual experiment pause. The `ExperimentHealthEvaluator` interface stub is empty.

**Recommendation:** Add "Manual Rollback Triggers" subsection with concrete example thresholds using platform-native metrics.

### EXP-002 Eval Score Ingestion Path Is a Black Box [High]
**Section:** 10.7

Who calls `POST /v1/sessions/{id}/eval`, accepted session states, rate-limiting, idempotency, and storage bounds all undefined.

**Recommendation:** Add "Eval Submission Contract" subsection specifying caller, states, rate limit, idempotency, and trigger model.

### EXP-003 Variant Pool Cold-Start Has No Guidance [High]
**Section:** 4.6.2, 10.7

No `initialMinWarm` field for variant pools. PoolScalingController produces `minWarm ≈ 0` for new experiments.

**Recommendation:** Add optional `initialMinWarm` field to `ExperimentDefinition.variants[]`.

### EXP-004 Experiment Context Not in DelegationLease Schema [Medium]
**Section:** 8.3, 10.7

`experimentContext` referenced in delegation but absent from the `DelegationLease` JSON schema in Section 8.3.

**Recommendation:** Add `experimentContext` to the DelegationLease schema. Clarify edge cases for mismatched runtimes.

### EXP-005 Variant Pool Formula Omits mode_factor [Medium]
**Section:** 4.6.2, 5.2, 10.7

Task-mode or concurrent-mode variant pools over-provisioned by `mode_factor` (10-50×).

**Recommendation:** Update variant pool formula to include `/ mode_factor`.

### EXP-006 Experiment Tenant Scoping Not Specified [Medium]
**Section:** 10.7

No `tenantId` field on `ExperimentDefinition`. Tenant isolation for experiments undefined.

**Recommendation:** Add `tenant_id` to schema. Apply same RLS policy as all tenant-scoped tables.

### EXP-007 Eval Submission on Concluded Experiments Not Guarded [Medium]
**Section:** 10.7

Late evals after conclusion corrupt historical records already used for decisions.

**Recommendation:** Define eval acceptance window. Prefer rejecting with `EXPERIMENT_CONCLUDED` after conclusion.

### EXP-008 ExperimentRouter Phase Interaction With Quota Not Defined [Medium]
**Section:** 4.8, 10.7

Quota-rejected sessions excluded from all variants, creating selection bias. `ExperimentRouter` behavior within interceptor chain unspecified.

**Recommendation:** Acknowledge selection bias. Specify ExperimentRouter's phase, return type, and metadata setting.

### EXP-009 Variant Pool Waste During Ramp-Up [Low]
**Section:** 4.6.2, 10.7

Low-weight variants have warm pods sitting idle during slow ramp.

**Recommendation:** Add guidance for conservative `initialMinWarm` at low weights. Reference `lenny_warmpool_idle_pod_minutes`.

### EXP-010 Results API Freshness Semantics Underspecified [Low]
**Section:** 10.7, 15

Paginated reads may be inconsistent across pages. Materialized view refresh lag not documented in response.

**Recommendation:** Add `computedAt` timestamp to Results API response.

### EXP-011 Platform vs Experimentation Boundary Is Implicit [Low]
**Section:** 10.7

Deployers may mistake the platform primitives for a complete experimentation solution.

**Recommendation:** Add "Integration with External Experimentation Platforms" subsection naming the expected pattern.

---

## 22. Document Quality, Consistency & Completeness

### DOC-101 Section 17.8 Heading Does Not Exist — 34 Cross-References Broken [Critical]
**Section:** 17.8

17.8.1 and 17.8.2 exist but the parent `### 17.8` heading was never added. Most-referenced section in the document.

**Recommendation:** Add `### 17.8 Capacity Planning and Defaults` as parent heading before 17.8.1.

### DOC-102 Renumbering Introduced Broken Cross-Reference in Section 9.2 [High]
**Section:** 9.2 (line 2882)

"Section 8.7" now points to File Export Model instead of Task Tree (Section 8.9).

**Recommendation:** Update line 2882 from "Section 8.7" to "Section 8.9".

### DOC-103 KMS Key Rotation Procedure Buried in Upgrade Strategy Section [High]
**Section:** 10.5

Security-operations content in an upgrade strategy section. Two cross-references cite 10.5 for KMS rotation.

**Recommendation:** Extract KMS rotation into Section 4.9.x or 13.x. Update cross-references.

### DOC-104 `ReportUsage` RPC Missing from Adapter RPC Table [High]
**Section:** 4.7 (lines 462-479)

Referenced 3 times in the body but absent from the authoritative RPC contract table.

**Recommendation:** Add `ReportUsage` to the Section 4.7 RPC table.

### DOC-105 Section 13.4 Cited for CNI — Actual Content in 13.2 [Medium]
**Section:** 17.6

Preflight check references Section 13.4 (Upload Security) for CNI NetworkPolicy. Should be 13.2 (Network Isolation).

**Recommendation:** Change cross-reference to Section 13.2.

### DOC-106 Section 5 Cited for Delegation — Should Be Section 8 [Medium]
**Section:** 18, 23

Four cross-references cite "Section 5" (Runtime Registry) where Section 8 (Recursive Delegation) is meant.

**Recommendation:** Replace all four "Section 5" references with "Section 8."

### DOC-107 `deliveryMode` Field Missing from Billing Event Schema [Medium]
**Section:** 4.9, 11.2.1

Referenced in Section 4.9 but absent from the schema table. Cross-reference also points to wrong section.

**Recommendation:** Add `deliveryMode` to schema table. Correct cross-reference from 12.4 to 11.2.1.

### DOC-108 Section 17.8.1 References "Section 17.8" Circularly [Medium]
**Section:** 17.8.1 (line 6170)

Self-reference to non-existent parent. Intended target is 17.8.2.

**Recommendation:** Change to "see Section 17.8.2 (Capacity Tier Reference)."

### DOC-109 Build Sequence Table Split Across Three Markdown Tables [Medium]
**Section:** 18

Three separate tables with interstitial notes. Readers see fragmented phase sequence.

**Recommendation:** Consolidate into a single table. Move notes to "Build Sequence Notes" section below.

### DOC-110 Section 21.5 Cross-References in Admin API Table [Medium]
**Section:** 15.1

dryRun is defined in 15.1 but references imply it's defined in 21.5 (Post-V1).

**Recommendation:** Remove Section 21.5 reference from API table. Clarify that 15.1 defines the behavior.

### DOC-111 Three Inconsistent Platform MCP Tool Lists [Medium]
**Section:** 4.7, 8.5, 9.1

Three locations with different membership and detail levels. No statement of which is authoritative.

**Recommendation:** Designate Section 9.1 as authoritative. Replace other lists with cross-references.

### DOC-112 ADR-TBD Has No Number and No docs/adr/ Directory [Medium]
**Section:** 4.6.1, 18

Phase 1 blocker with no tracking mechanism. `docs/adr/` directory does not exist.

**Recommendation:** Create `docs/adr/`. Assign ADR-001 to SandboxClaim verification, ADR-002 to license decision.

### DOC-113 Document Status Still "Draft" with Stale Date [Low]
**Section:** Lines 3-4

Status "Draft" and date "2026-04-02" despite substantial post-draft edits. Content exceeds typical draft level.

**Recommendation:** Update status to "In Review" and date to last edit.

### DOC-114 Section 20 Placeholder Doesn't Mention Open License Question [Low]
**Section:** 20

Says "All open questions resolved" but license selection remains unresolved.

**Recommendation:** Note the outstanding license decision or add it to Section 19 when resolved.

### DOC-115 "BDfN" Used Before Defined [Low]
**Section:** 18 (line 6424)

Abbreviation used before its definition in Section 23.2.

**Recommendation:** Expand on first use: "Benevolent Dictator for Now (BDfN)."

### DOC-116 Document Length 6,548 Lines vs Stated ~3,700 [Low]
**Section:** All

77% longer than stated estimate.

**Recommendation:** Update external references. Consider extraction recommendations.

### DOC-117 Inconsistent Callout Block Styling [Info]
**Section:** 7.1

Mixed callout patterns (`> **Note:**` vs inline bold).

**Recommendation:** Standardize to `> **Note/Warning/Caution:**` blockquotes.

### DOC-118 Non-Integer Phase Numbering Unexplained [Info]
**Section:** 18

Fractional (3.5, 4.5) and lettered (12a, 12b, 12c) phases with no convention note.

**Recommendation:** Add brief convention note to Section 18 preamble.

---

## 23. Messaging, Conversational Patterns & Multi-Turn Interactions

### MSG-001 Path 2 Timeout Fallback to Inbox Overflow Ambiguous [High]
**Section:** 7.2

When path 2 falls through to inbox and inbox is at `maxInboxSize`, delivery receipt already returned as `delivered` becomes incorrect.

**Recommendation:** Path 2 should return `delivered` only after confirmed stdin consumption. Return `queued` if routing to inbox.

### MSG-002 Session Inbox Loss on Crash Is Undiscoverable [High]
**Section:** 7.2

No sequence number, cursor, or `inbox_reset` event for senders to detect lost messages after coordinator handoff.

**Recommendation:** Add durable Redis inbox or explicit `inbox_cleared` event. Define gap-detection API.

### MSG-003 `input_required` State Not Integrated Across State Machine Diagrams [High]
**Section:** 6.2, 7.2, 8.8

Defined as sub-state of `running` in 7.2, peer state in 8.8, absent from 6.2. Transitions to `cancelled`/`expired` inconsistent.

**Recommendation:** Unify across all three state machines. Include `input_required` in pod state machine as sub-state.

### MSG-004 SSE Replay Window Boundary Underspecified [High]
**Section:** 7.2

"Checkpoint window" for event replay is undefined. No behavior for gaps outside the replay window.

**Recommendation:** Define replay window explicitly. Include `events_lost` count in `checkpoint_boundary` marker.

### MSG-005 Sibling Coordination Lacks Membership Stability [Medium]
**Section:** 7.2

No mechanism for siblings to be notified when new siblings are added. Dynamic teams rely on pre-planned static topologies.

**Recommendation:** Define `sibling_joined` event or `lenny/subscribe_task_tree` streaming call. Document limitation if deferred.

### MSG-006 DLQ Transition From Inbox Boundary Unclear [Medium]
**Section:** 7.2

When session transitions to `resume_pending`, inbox messages on the crashing replica are lost (not in DLQ because session was still active).

**Recommendation:** Define inbox-to-DLQ migration path: drain inbox into DLQ atomically when session enters `resume_pending`.

### MSG-007 `await_children(mode: any)` Cancel Semantics After Parent Completion [Medium]
**Section:** 8.8

Whether `cascadeOnFailure` applies on parent completion (not just failure) to uncollected siblings is unspecified.

**Recommendation:** Clarify cascade behavior for all terminal states (completed, failed, cancelled). Define sibling lifecycle explicitly.

### MSG-008 Path 2/3 Boundary Undefined Under Concurrent Tool Execution [Medium]
**Section:** 7.2

`ready_for_input` signal undefined for runtimes executing multiple tool calls simultaneously.

**Recommendation:** Define `ready_for_input` as adapter-maintained signal emitted only after runtime's explicit `ready` output.

### MSG-009 `lenny/request_input` Expiry During Parent `await_children` [Medium]
**Section:** 8.8, 9.2

If child's request times out before parent processes `input_required`, no follow-up event notifies the parent.

**Recommendation:** Add `request_input_expired` event to `await_children` stream.

### MSG-010 `one_shot` Runtime Second `request_input` Error Unspecified [Low]
**Section:** 5.1

Error code and error type for the second call are not defined.

**Recommendation:** Add `REQUEST_INPUT_LIMIT_EXCEEDED` to error catalog with category POLICY.

### MSG-011 Concurrent `delivery: immediate` to Suspended Session Race [Low]
**Section:** 7.2

Two simultaneous `delivery: immediate` messages compete for `suspended → running` transition. Second message path undefined.

**Recommendation:** Specify that coordination lease serializes delivery. Second message follows path 2 after session is running.

### MSG-012 Self-Send via `lenny/send_message` Behavior Undefined [Low]
**Section:** 7.2

Session sending a message to itself could create a loop.

**Recommendation:** Reject with `INVALID_TARGET`. Document in error catalog.

### MSG-013 No SSE Backpressure Before Drop [Info]
**Section:** 7.2

No `buffer_pressure` event to warn clients approaching the limit before connection drop.

**Recommendation:** Consider adding buffer pressure event at 80% threshold. Document intentional absence if deferred.

---

## 24. Policy Engine & Admission Control

### POL-001 Budget Propagation Race: Child Can Exceed Parent's Remaining Budget [Critical]
**Section:** 8.3

Token usage counter and delegation budget reservation counter are separate Redis keys. Parent can have consumed 190K of 200K tokens while still having full 200K in delegation counter.

**Recommendation:** `budget_reserve.lua` must also read parent's actual usage counter atomically. Cap child slice to `min(requested, parentBudget - parentUsage)`.

### POL-002 Fail-Open Window Is Unbounded Within Per-Replica Ceiling [Critical]
**Section:** 12.4, 11.2

With both Redis and Endpoints unavailable, every replica allows full `tenant_limit`. Aggregate overshoot = `N × tenant_limit`.

**Recommendation:** Add `quotaFailOpenReplicaFloor` config. Effective ceiling = `tenant_limit / max(actual_count, floor)`.

### POL-003 Quota Update Timing Creates Dual-Source Inconsistency [High]
**Section:** 11.2, 12.4

On Redis recovery, Postgres checkpoint (30s stale) could reset counters to lower value, effectively un-enforcing a budget violation.

**Recommendation:** Take `MAX(redis_counter_before_failure, postgres_checkpoint)` on recovery. Write authoritative value on session completion.

### POL-004 Interceptor Short-Circuit and MODIFY Interaction Undefined [High]
**Section:** 4.8

External interceptors can modify task input before `QuotaEvaluator` reads it. Built-in evaluators may operate on modified data.

**Recommendation:** Document which fields each built-in reads. Specify that modification of quota-relevant fields triggers re-evaluation.

### POL-005 `contentPolicy` Inheritance Through Delegation Not Fully Specified [High]
**Section:** 8.3

"Same or more restrictive `interceptorRef`" has no mechanism for the gateway to evaluate restrictiveness of named references.

**Recommendation:** Define concrete enforcement rule. Add note that runtime changes to `failPolicy` don't retroactively affect lease restrictiveness.

### POL-006 Fail-Open Rate Limiting Lacks Per-User Bounding [High]
**Section:** 12.4

Per-tenant fail-open ceiling but no per-user ceiling. One user can monopolize the entire allocation.

**Recommendation:** Add per-user fail-open ceiling alongside per-tenant ceiling.

### POL-007 PreAuth Phase MODIFY Semantics Are Dangerous [High]
**Section:** 4.8

`PreAuth` phase should only be accessible to built-ins (priority ≤ 100) but MODIFY semantics are described as if external interceptors could fire there.

**Recommendation:** Explicitly state PreAuth is built-in only. Remove MODIFY semantics for external interceptors at this phase.

### POL-008 Circuit Breaker Specification Incomplete [Medium]
**Section:** 11.6

Storage, propagation mechanism, API, and interaction with AdmissionController are all undefined.

**Recommendation:** Specify Redis pub/sub storage, admin API endpoint, and AdmissionController evaluation rules.

### POL-009 Timeout Table Missing Critical Operation Timeouts [Medium]
**Section:** 11.3

9 operation-specific timeouts defined elsewhere are absent from the canonical timeout table.

**Recommendation:** Extend timeout table to be comprehensive. Add columns for configurability and Helm value names.

### POL-010 `maxDelegationPolicy` on Lease Under-Defined [Medium]
**Section:** 8.3

Type (named reference vs inline), interaction with `delegationPolicyRef`, and precedence rules unspecified.

**Recommendation:** Add definition specifying type, precedence, restriction-only rule, and concrete example.

### POL-011 Interceptor Timeout Inconsistency Between LLM and Other Phases [Medium]
**Section:** 4.8

100ms `PreLLMRequest` timeout with fail-closed can significantly disrupt legitimate LLM calls. Error code indistinguishable from explicit rejection.

**Recommendation:** Add `INTERCEPTOR_TIMEOUT` error code distinct from `LLM_REQUEST_REJECTED`.

### POL-012 Budget Return Doesn't Account for In-Flight Usage [Medium]
**Section:** 8.3

Child's final `ReportUsage` may not have been processed when `budget_return.lua` fires. Parent receives slightly more budget than entitled.

**Recommendation:** Specify that return script reads usage counter at execution time. Ensure quiescence step before terminal transition.

### POL-013 DelegationPolicy Tag Evaluation Creates Policy-Window Vulnerability [Medium]
**Section:** 8.3

Dynamic tag evaluation means runtime labels changed mid-session can grant or revoke delegation permissions.

**Recommendation:** Clarify whether evaluation uses point-in-time snapshot or live labels. Consider `snapshotPolicyAtLease` option.

### POL-014 `approvalMode: deny` Not Validated at Session Creation [Low]
**Section:** 8.3, 8.4

Runtime with delegation capabilities gets silent failure when `approvalMode: deny` is set. No warning at creation.

**Recommendation:** Emit `delegation_disabled` annotation on session creation audit event.

### POL-015 No Sub-Range Reserved for Platform Extensions [Low]
**Section:** 4.8

External interceptors can register at priority 101, interleaving with platform evaluators.

**Recommendation:** Reserve 1-499 for platform use. Document used slots.

### POL-016 Missing Interceptor Reference Behavior Undefined [Low]
**Section:** 8.3, 4.8

`contentPolicy.interceptorRef` referencing a deleted interceptor has no specified behavior.

**Recommendation:** Fail-closed with `INTERCEPTOR_NOT_FOUND` error. Log audit event.

---

## 25. Execution Modes & Concurrent Workloads

### EXM-001 Task-Mode Pod Crash During Active Task Not in State Machine [High]
**Section:** 6.2

No `attached → failed` or `attached → resume_pending` transition in the task-mode state diagram.

**Recommendation:** Add explicit transitions. Specify whether crash fails the task outright or retries on a new pod.

### EXM-002 Concurrent-Stateless Mode Is Underspecified [High]
**Section:** 5.2

Single paragraph of specification. No lifecycle, failure semantics, deployer guidance, or decision criteria vs connectors.

**Recommendation:** Either provide full specification or deprecate from v1 scope with connector recommendation.

### EXM-003 SlotId Multiplexing Failure on Pod-Gateway Connection Loss [Medium]
**Section:** 5.2, 15

Pod-level gRPC connection loss with multiple active slots has undefined behavior. Gateway reconnect logic designed for single-session pods.

**Recommendation:** Specify whether all slots fail simultaneously, reconnect window, and interaction with whole-pod replacement trigger.

### EXM-004 Concurrent-Workspace Cross-Slot Security Caveat Absent [Medium]
**Section:** 5.2

Task mode has "not a security boundary" warning with residual state enumeration. Concurrent mode (with worse isolation) lacks equivalent.

**Recommendation:** Add cross-slot residual state enumeration: shared procfs, network namespace, cgroup memory, `/tmp`, kill(2) reachability.

### EXM-005 Graph Mode — Observability Protocol Undefined [Medium]
**Section:** 5.2

"Optionally emit trace spans via the observability protocol" — but this protocol is not specified anywhere.

**Recommendation:** Define the trace span emission contract or mark as future capability and remove the reference.

### EXM-006 Credential Lease Between Sequential Tasks Unspecified [Medium]
**Section:** 5.2, 4.3, 4.9

Whether credential lease persists across task boundaries or is re-acquired per task is undefined. Both have significant implications.

**Recommendation:** Add "task-mode credential lease lifecycle" paragraph specifying per-task vs per-pod semantics.

### EXM-007 Slot Cleanup Timeout Formula Produces Small Values at High Concurrency [Low]
**Section:** 5.2

With `maxConcurrent: 16-32`, aggregate cleanup time (16-32 × 5s min) exceeds `cleanupTimeoutSeconds`. No warning to deployers.

**Recommendation:** Document interaction with `terminationGracePeriodSeconds`. Add validation rule when aggregate exceeds it.

### EXM-008 Task Mode Warm Pod Counting Ambiguous [Low]
**Section:** 5.2, 4.6.1

Whether `attached` or `task_cleanup` pods count toward `minWarm` is unspecified.

**Recommendation:** Explicitly state that only `idle` pods count toward `minWarm`.

### EXM-009 "Stateless Should Be Connectors" Advisory Orphaned [Low]
**Section:** 5.2

Standalone sentence with no decision criteria, connector registration reference, or explanation of `concurrent-stateless` existence.

**Recommendation:** Replace with structured decision guide: when to use concurrent-stateless vs external connector.

### EXM-010 Graph Mode Removal Not Cross-Referenced to ADR [Info]
**Section:** 5.2

No ADR reference, migration note, or acknowledgment of affected use cases.

**Recommendation:** Add cross-reference to design-updates document and one-sentence migration note.

---
