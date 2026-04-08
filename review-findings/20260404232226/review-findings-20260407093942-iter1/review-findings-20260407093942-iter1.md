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
| 18 | Competitive | ~~CPS-001 No Differentiation Narrative~~ **FIXED** | 1, 2 |
| 19 | Warm Pool | ~~WPL-001 SDK-Warm Pod Eviction During sdk_connecting State Not Handled~~ **FIXED** | 6.1, 6.2, 4.6.1 |
| 20 | Schema Design | SCH-001 OutputPart Type Registry Has No Formal Schema or Versioning Contract | 15.4.1 |
| 21 | Schema Design | ~~SCH-002 RuntimeDefinition Inheritance Rules Exist Only in Prose~~ **FIXED** | 5.1 |
| 22 | Schema Design | ~~SCH-003 CredentialLease materializedConfig Is Deliberately Unschematized~~ **FIXED** | 4.9 |
| 23 | Build Sequence | BLD-001 Critical-Path Dependency: Authentication Comes After Real LLM Testing | 18 |
| 24 | Build Sequence | ~~BLD-002 Security Audit Scheduled After Full Observability — Too Late~~ **FIXED** | 18 |
| 25 | Build Sequence | ~~BLD-003 Echo Runtime Insufficient for Phase 6–8 Milestone Validation~~ **FIXED** | 18 |
| 26 | Failure Modes | ~~FLR-001 Dual-Store Concurrent Outage Leaves Sessions in Terminal Limbo~~ **FIXED** | 10.1, 12.3, 12.4 |
| 27 | Failure Modes | ~~FLR-002 MinIO Outage During Node Eviction Causes Irrecoverable Workspace Loss~~ **FIXED** | 12.5, 4.4 |
| 28 | Document Quality | DOC-101 Section 17.8 heading does not exist — 34 cross-references are broken | 17.8 |
| 29 | Policy Engine | POL-001 Budget Propagation Race: Child Can Transiently Exceed Parent's Remaining Budget | 8.3 |
| 30 | Policy Engine | ~~POL-002 Fail-Open Window for Quota Enforcement Is Unbounded Within the Per-Replica Ceiling~~ **ALREADY FIXED (by STR-001)** | 12.4, 11.2 |

---

## Cross-Cutting Themes

### 1. Redis Fail-Open Window Is Under-Bounded Across Multiple Subsystems
Multiple perspectives (STR-001, POL-002, SEC-015, ~~FLR-003~~, FLR-011) identify that the Redis fail-open behavior creates unbounded exposure windows for quota enforcement, rate limiting, delegation budgets, and session coordination. The per-replica fallback with `replica_count = 1` default allows N× overshoot. This theme appears in Storage, Policy, Security, and Failure Modes perspectives. **FLR-003 is now resolved** — the quota fail-open exposure was fully addressed by STR-001 (see finding detail). SEC-015, FLR-011, and POL-002 remain open for their respective scopes (rate limiting, delegation budget reconciliation, and quota update timing).

### 2. Session Inbox In-Memory Durability Is a Systemic Weakness
The in-memory session inbox with no durability guarantee surfaces as a finding across Security (SEC-011), Session Lifecycle (SLC-005, SLC-006), Messaging (MSG-002, MSG-006), Scalability (SCL-010), and Failure Modes (~~FLR-005~~). Loss of inter-session messages on coordinator crash creates data loss, message suppression attacks, and delegation result loss. **FLR-005 is now resolved** — Section 7.2 introduces the `durableInbox: true` mode backed by a Redis list, providing crash-durable inbox storage with explicit ACK and per-message TTL. SLC-006 (inbox-to-DLQ drain at `resume_pending`) was previously fixed. The broader theme persists across SEC-011, MSG-002, MSG-006, and SCL-010, which address additional durability and security aspects not covered by the inbox mode change.

### 3. Schema and Contract Under-Specification Creates Implementation Ambiguity
Multiple schema-related findings (SCH-001 through SCH-008, DXP-001, DXP-004, CRD-001, CRD-003) identify that key data structures — OutputPart, RuntimeDefinition inheritance, CredentialLease materializedConfig, MessageEnvelope delivery semantics — lack formal schemas, validation contracts, or versioning strategies. This creates implementation divergence risk across adapter authors and third-party integrations.

### 4. Build Sequence Has Late-Bound Security and Performance Validation
The build sequence places security audit (Phase 14) and load testing (Phase 13.5) after the full system is built, rather than incrementally validating at key milestones. BLD-001, BLD-002, BLD-003, and BLD-010 all identify that credential injection, delegation chains, and streaming paths are built and merged without security review or performance validation until very late in the sequence. **BLD-002, BLD-003, and BLD-010 are now fixed** — targeted security reviews at Phases 5.6/9.1, the `streaming-echo` test runtime at Phase 2.8, and incremental load tests at Phases 6.5/9.5/11.5 address these gaps. BLD-001 remains open.

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
**Status:** Fixed

The spec presents a "CRD field ownership" table backed by RBAC + validating admission webhooks, but Kubernetes RBAC operates at resource and subresource granularity, not at individual field granularity. The actual Kubernetes-native mechanism for field-level ownership is Server-Side Apply (SSA) with named field managers.

**Recommendation:** Adopt Server-Side Apply as the primary enforcement mechanism for CRD field ownership. Each controller should apply its owned fields using a named field manager; the validating webhook becomes a defense-in-depth backstop.

**Resolution:** Section 4.6.3 was updated to adopt Server-Side Apply (SSA) with named field managers (`lenny-warm-pool-controller` and `lenny-pool-scaling-controller`) as the primary field-ownership enforcement mechanism. The API server enforces SSA field manager conflicts at update time (HTTP 409 on conflict). The RBAC paragraph was reworded to clearly state that RBAC operates at resource/subresource granularity only and cannot enforce field-level boundaries. The validating webhook was explicitly demoted to a defense-in-depth backstop role, with its description updated accordingly.

### K8S-003 PSS Admission Policy Webhook Failure Mode Not Specified [High]
**Section:** 17.2, 5.3
**Status:** Fixed

The spec disables PSS `enforce` mode in favor of RuntimeClass-aware admission policies (OPA/Gatekeeper or Kyverno) but does not specify whether these webhooks run in `Fail` or `Ignore` mode. If unavailable, pods can be scheduled without security constraints.

**Recommendation:** Explicitly specify `failurePolicy: Fail` for all RuntimeClass-aware admission policy webhooks. Document minimum-availability SLO for the admission policy webhook deployment. Consider keeping PSS `enforce` mode active as defense-in-depth for the baseline `restricted` profile.

**Resolution:** Section 17.2 was updated with a new **"Admission webhook failure mode"** paragraph specifying: (1) all RuntimeClass-aware admission policy webhooks (`ValidatingWebhookConfiguration` objects) must be configured with `failurePolicy: Fail` so pod admission is denied (fail-closed) when the webhook is unavailable; (2) the admission controller must maintain a minimum availability SLO of 99.9% (rolling 30-day window), enforced via `replicas: 2` and `podDisruptionBudget.minAvailable: 1` in the Helm chart; and (3) an `AdmissionWebhookUnavailable` alert fires when the webhook has been unreachable for more than 30 seconds. The paragraph also explains why namespace-level PSS `enforce` cannot serve as a defense-in-depth fallback (it cannot distinguish RuntimeClasses, causing gVisor and Kata pods to be incorrectly rejected). Section 5.3 was updated with a new **"RuntimeClass-aware admission policies"** paragraph that introduces the `failurePolicy: Fail` requirement at the point where isolation profiles are first described and cross-references Section 17.2 for the full specification.

### K8S-004 agent-sandbox Upstream Maturity Risk Understated [High]
**Section:** 4.6
**Status:** Fixed

The one-release-delay upgrade cadence is insufficient for a v0.x project where breaking API changes between minor versions are expected. The fallback plan mentions "internal minimal implementation" but does not specify what triggers this decision.

**Recommendation:** Define explicit go/no-go criteria for the agent-sandbox dependency: API stability targets, community support SLOs, and a decision gate at end of Phase 1.

**Resolution:** Section 4.6 was updated with two new blocks. (1) The **"Dependency pinning and upgrade policy"** paragraph was expanded to explain why the one-release-delay cadence is insufficient for a v0.x dependency and to add three augmenting rules: a CI gate requiring the full integration suite to pass before any upgrade, a breaking-change hold that prevents upgrading until two successive releases confirm a stable API surface, and active API stability monitoring via upstream release notes and issue tracker. (2) A new **"Go/no-go criteria for the agent-sandbox dependency"** block defines three explicit, measurable criteria evaluated at Phase 1 exit: (a) API stability — no structural breaking change in the two most recent upstream releases; (b) community support SLO — at least one release in 6 months, critical issues acknowledged within 30 days, no unaddressed blocker issues older than 60 days; (c) integration test pass rate — 100% pass on the agent-sandbox test suite. Failure of any criterion triggers fallback activation before Phase 2 begins, with the decision recorded as an ADR. (3) The **"Fallback plan"** paragraph was updated to add an explicit trigger condition: fallback activation begins when the go/no-go assessment finds one or more criteria unmet and upstream cannot commit to resolution within 30 days.

### K8S-005 GC Loop Inside Gateway Competes with Request Serving at Scale [High]
**Section:** 4.6
**Status:** Fixed

The 60-second orphan detection GC loop runs as a goroutine inside the gateway. At Tier 3 scale, this runs API server list operations across the agent namespace, and all gateway replicas run GC simultaneously.

**Recommendation:** Move the GC loop to a leader-elected goroutine inside WarmPoolController, or gate it behind a leader election lease shared among gateway replicas.

**Resolution:** Section 4.6's **"Orphaned `SandboxClaim` detection"** paragraph was updated to make the leader-election constraint explicit and to document the design rationale. The paragraph now states that only the **leader replica** of the WarmPoolController executes the `GarbageCollect` loop every 60 seconds — non-leader replicas skip it entirely. An explanatory note was added making clear this is the deliberate reason orphan detection is owned by the WarmPoolController rather than the gateway: running the GC loop in the gateway would cause all gateway replicas to issue API server list operations simultaneously at each tick, multiplying API load linearly with replica count. By placing the loop exclusively in the single elected leader, API list load remains constant regardless of gateway or controller replica count.

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

### ~~SEC-001 Intra-Pod MCP Connections Are Completely Unauthenticated~~ **FIXED** [High]
**Section:** 4.7, 15.4.3

~~The local MCP servers (platform MCP, connector MCP servers) are reachable by any process inside the pod via abstract Unix socket (`@lenny-platform-mcp`) with no authentication. A compromised runtime child process could call `lenny/delegate_task` to spawn arbitrary child sessions, use `lenny/memory_write` to poison the memory store, or trigger `lenny/request_elicitation` to phish the user.~~

**Fix applied:** The manifest-nonce handshake already defined for the lifecycle channel has been extended to cover all intra-pod MCP connections. Section 4.7 item 7 now requires the connecting process to present the manifest nonce as the first message of the MCP `initialize` handshake on every MCP server (platform MCP and all connector MCP servers); the adapter rejects connections that do not present a valid nonce. Section 15.4.3 replaces the "No authentication" bullet with a description of this nonce handshake requirement. The adapter manifest JSON example and field reference table now include the `mcpNonce` field (random 256-bit hex, regenerated per task execution). Tier reading requirements updated: Standard-tier runtimes must read `mcpNonce` alongside socket addresses.

~~**Recommendation:** Apply the same nonce-based connection handshake already defined for the lifecycle channel to the platform MCP server and connector MCP servers. Require the connecting process to present the manifest nonce on MCP `initialize` handshake.~~

### ~~SEC-002 Lease Token Not SPIFFE-Bound in Proxy Mode — Cross-Pod Replay Risk~~ **FIXED** [High]
**Section:** 4.9

~~Lease tokens for the LLM proxy are not bound to the pod's SPIFFE identity. A leaked token can be replayed from any pod with a valid mTLS certificate. Deferred to "post-v1" but critical for multi-tenant deployments.~~

**Fix applied:** The "Future hardening (post-v1)" callout in Section 4.9 has been promoted to a v1 requirement for multi-tenant deployments. The spec now specifies: (1) at `AssignCredentials` time the gateway records the issuing pod's SPIFFE URI alongside the lease record in `TokenStore`; (2) on every LLM proxy request the gateway extracts the peer SPIFFE URI from the authenticated mTLS connection and verifies it matches the stored lease record, rejecting mismatches with `LEASE_SPIFFE_MISMATCH` (category: `SECURITY`) and emitting audit event `credential.lease_spiffe_mismatch`; (3) no protocol changes are required for pods — binding is enforced server-side. SPIFFE-binding is enabled by default for proxy-mode pools and can be disabled only for single-tenant or development deployments via `credentialPool.spiffeBinding: disabled`, which emits a `ProxyModeSpiffeBindingDisabled` warning event at pool registration.

~~**Recommendation:** Implement SPIFFE-binding for proxy mode lease tokens in v1. Bind each lease token to the issuing pod's SPIFFE URI and validate on every LLM proxy request.~~

### ~~SEC-003 Prompt Injection via Unchecked Delegation File Exports~~ **FIXED** [High]
**Section:** 8.7, 13.5

~~No content inspection of exported files during delegation. A compromised parent agent can craft files containing adversarial prompt injection content (e.g., `CLAUDE.md`) that bypass `contentPolicy.interceptorRef` which only covers `TaskSpec.input`.~~

~~**Recommendation:** Add a `fileExportPolicy` field to `DelegationPolicy` with a `PreFileExport` interceptor phase. Document that workspace files received via delegation should be treated as untrusted input.~~

**Resolution:** Addressed via explicit documentation of the gap and residual risk rather than adding a new interceptor phase (which would be a significant schema and architecture change). Two targeted additions were made:

1. **Section 8.7 (File Export Model)** — Added a "Security note — exported files are untrusted input" paragraph under the Validation section. It explicitly states that `contentPolicy.interceptorRef` covers `TaskSpec.input` only (not exported workspace files), that a compromised parent can embed adversarial content in any exported file including instruction sources like `CLAUDE.md`, and that child runtimes and deployers must treat all delegation-received workspace files as untrusted. It also provides deployer-side mitigation guidance (inspect `inlineFile` entries in the workspace plan via an interceptor before they are written).

2. **Section 13.5 (Delegation Chain Content Security)** — Added a "Residual risk — file export content" paragraph explicitly calling out the file-export gap alongside the existing `contentPolicy.interceptorRef` residual risk paragraph. This ensures the gap is visible in the security threat model section alongside the other delegation security controls.

The recommended `fileExportPolicy` / `PreFileExport` interceptor phase was not added: the gap is a known platform limitation (structural validation only, no semantic inspection), the existing interceptor model already provides a deployer-side escape hatch via workspace plan inspection, and adding a new interceptor phase would require schema changes to `DelegationPolicy` and `InterceptorPhase` with broader spec impact than the security benefit warrants for v1.

### ~~SEC-004 Isolation Monotonicity Enforced at Delegation Time Only~~ **FIXED** [High]
**Section:** 8.3

~~A tag-based `DelegationPolicy` rule may match pools with varying isolation levels. A new `standard` pool registered with matching labels silently becomes a monotonicity-violation enabler. The `lenny-ctl policy audit-isolation` CLI is not a continuous check.~~

~~**Recommendation:** Add server-side continuous enforcement: when a new pool is registered, proactively evaluate all active `DelegationPolicy` resources against the new pool's isolation profile and emit warnings.~~

**Resolution:** Addressed via two targeted additions to Section 8.3 and the Section 11.7 audit event catalog:

1. **Section 8.3 — Proactive pool-registration enforcement paragraph.** A new "Proactive pool-registration enforcement" paragraph was added immediately after the existing "Enforcement point and audit trail" paragraph. It specifies that whenever a pool is created or updated (`POST /v1/admin/pools` or `PUT /v1/admin/pools/{name}`), the gateway asynchronously evaluates all active `DelegationPolicy` resources against the new pool's isolation profile. For every policy rule where the new pool would become a weaker-isolation target reachable by a parent with stricter isolation, the gateway emits a `pool.isolation_warning` audit event. Pool registration is not blocked — the event is purely a visibility mechanism. This closes the silent-failure window identified in the finding.

2. **Section 8.3 — Deployer guidance blockquote updated.** The guidance was rewritten to direct operators to monitor `pool.isolation_warning` events in the audit log or SIEM after any pool change, and to use `lenny-ctl policy audit-isolation` on demand for a full current-state report.

3. **Section 11.7 — Audit event catalog extended.** The `pool.isolation_warning` event type was added to the event types table, and four new fields (`pool_name`, `pool_isolation`, `conflicting_pool_name`, `conflicting_isolation`) were added to the event schema table. The `matched_policy_rule` field description was updated to cover both `delegation.isolation_violation` and `pool.isolation_warning` events.

### SEC-005 Task-Mode Scrub Residual State Vectors Not Surfaced to Clients [High]
**Section:** 5.2
**Status:** Fixed

Clients creating sessions have no visibility into whether their session runs on a task-mode pod with residual state from prior tasks (DNS cache, TCP TIME_WAIT, page cache). The `acknowledgeBestEffortScrub` is deployer-level only.

**Recommendation:** Expose execution mode and isolation profile in the session creation response. Add a `sessionIsolationLevel` field to session metadata.

**Resolution:** Two changes were made to the spec. (1) Section 7.1 (Normal Flow): step 8 updated to include `sessionIsolationLevel` in the `POST /v1/sessions` response, and a new `sessionIsolationLevel` field table added documenting `executionMode`, `isolationProfile`, `podReuse`, `scrubPolicy`, and `residualStateWarning` fields. The `residualStateWarning: true` field explicitly signals to clients when they are on a task-mode pod with best-effort scrub. `GET /v1/sessions/{id}` also returns this object. (2) Section 5.2 (Deployer Acknowledgment): a new "Client visibility of task-mode isolation" paragraph added cross-referencing Section 7.1 and explaining that clients should check `residualStateWarning` and reject sessions if their use case requires strict isolation.

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

### NET-001 Unrestricted Port Range on Pod-to-Gateway Egress [Critical] ✅ Fixed
**Section:** 13.2

The `allow-pod-egress-base` NetworkPolicy permits agent pods to reach the gateway on any port and any protocol. No `ports` stanza is present, allowing probing of admin, debug, and metrics endpoints.

**Recommendation:** Add an explicit `ports` list limiting to TCP 50051 (gRPC) and TCP 8443 (LLM proxy for proxy-mode pools only).

**Resolution:** Added a `ports` stanza to the gateway egress rule in `allow-pod-egress-base` (Section 13.2), limiting pod-to-gateway egress to TCP 50051 (`gateway.grpcPort`, pod-to-gateway gRPC control channel) and TCP 8443 (`gateway.llmProxyPort`, LLM proxy for proxy-mode pools only).

### NET-002 Cloud IMDS Endpoint Not Blocked for Agent Pods with Internet Egress [Critical]
**Section:** 13.2

The `internet` egress profile adds `0.0.0.0/0` but `169.254.169.254` (IMDS) is reachable directly from node-local processes. A compromised agent pod could retrieve node IAM credentials.

**Recommendation:** Add explicit `ipBlock` deny for `169.254.169.254/32` to every egress profile policy. Also block `fd00:ec2::254` (IPv6 IMDS) and `100.100.100.200` (Alibaba IMDS).

**Status: Fixed** — Three changes were made to Section 13.2: (1) The `egressProfile` table's `internet` row was updated to note that `0.0.0.0/0` excludes both cluster CIDRs and IMDS addresses. (2) The `internet` profile hardening note (NET-002 callout) was expanded to explicitly enumerate all three IMDS addresses blocked via `except` clauses — `169.254.169.254/32` (AWS/GCP/Azure IPv4), `fd00:ec2::254/128` (AWS IPv6), and `100.100.100.200/32` (Alibaba Cloud) — and to clarify that these exclusions apply to **every** egress profile policy (not only `internet`), including the base `allow-pod-egress-base` policy. A new Helm value `egressCIDRs.excludeIMDS` (default list of the three addresses) was introduced so deployers can extend coverage for additional cloud providers. (3) The `lenny-system` NetworkPolicy note was updated to list all three IMDS addresses and cross-reference the NET-002 hardening note.

### NET-003 Mutable Pod Label Used as NetworkPolicy Selector [High]
**Section:** 13.2

`lenny.dev/managed: "true"` can be mutated at runtime by any principal with `patch` on pods. Adding this label to a rogue pod grants gateway connectivity.

**Recommendation:** Enforce label immutability via admission webhook that prevents adding the label to pods not created by the warm pool controller.

**Status: Fixed** — Two changes were made. (1) A `lenny.dev/managed` label immutability note (NET-003) was added in Section 13.2 immediately after the existing namespace-selector immutability note. The note specifies a new `lenny-label-immutability` ValidatingAdmissionWebhook deployed fail-closed (`failurePolicy: Fail`) that enforces two rules: a creation guard allowing `lenny.dev/managed: "true"` to be set only by the warm pool controller ServiceAccount (`system:serviceaccount:lenny-system:lenny-controller`), and a mutation guard that unconditionally denies any UPDATE that adds or modifies this label post-creation. The webhook is scoped to agent namespaces, deployed with `replicas: 2` and a PDB, and its manifest is included in the Helm chart at `templates/admission-policies/label-immutability-webhook.yaml`. (2) The admission policy manifest list in Section 17.2 was updated to enumerate this webhook as item (5), cross-referencing the NET-003 note in Section 13.2.

### ~~NET-004 mTLS Not Enforced on Gateway-to-Redis and Gateway-to-PgBouncer Paths~~ **FIXED** [High]
**Section:** 10.3, 13.2

~~NetworkPolicy is L3/L4 only and cannot enforce TLS negotiation. Without a service mesh, a misconfigured gateway could connect to Redis in plaintext.~~

**Fix applied:** Two sets of changes were made.

**Section 10.3** — Added a "Redis and PgBouncer TLS enforcement (NET-004)" block immediately before Section 10.4 specifying:
1. **Redis server-side enforcement:** Redis must be configured with `tls-auth-clients yes` and plaintext port disabled (`port 0`), making plaintext connections structurally impossible at the server.
2. **PgBouncer server-side enforcement:** PgBouncer must be configured with `client_tls_sslmode = require` (or `verify-full`) so plaintext client connections are rejected at the listener.
3. **Gateway startup TLS probe:** Each gateway replica must run a startup probe verifying TLS connectivity to both Redis and PgBouncer before the replica is marked ready. A plaintext acceptance or handshake failure fails the deployment.
4. **Integration tests:** The test suite must include `TestRedisTLSEnforcement` and `TestPgBouncerTLSEnforcement` asserting that plaintext connection attempts are rejected.

**Section 13.2 (Profile-Invariant Requirements)** — The `Redis AUTH + TLS` bullet was expanded to reference the server-side enforcement configuration (`tls-auth-clients yes`, `port 0`, `client_tls_sslmode = require`) and cross-reference the full Section 10.3 requirements.

~~**Recommendation:** Configure Redis with `tls-auth-clients yes`, run a startup mTLS probe, and add integration tests asserting plaintext connections are rejected.~~

### ~~NET-005 Lease Tokens Not Bound to Pod SPIFFE Identity~~ **ALREADY FIXED (by SEC-002)** [High]
**Section:** 10.3, 4.9

~~A leaked lease token can be replayed from any pod with a valid mTLS certificate. The SPIFFE-based identity model is undermined.~~

~~**Recommendation:** Promote SPIFFE-binding from "future hardening" to pre-GA requirement. Bind lease tokens to the pod's SPIFFE URI at issuance time.~~

**Resolution (Already Fixed — same fix as SEC-002):** Section 4.9 already contains a full SPIFFE-binding specification under the callout "SPIFFE-binding for proxy mode lease tokens (v1 requirement for multi-tenant deployments)". The fix was introduced as part of the SEC-002 resolution and covers all three required elements: (1) recording the pod's SPIFFE URI (`spiffe://lenny/agent/{pool}/{pod-name}`) in the `TokenStore` at `AssignCredentials` time; (2) verifying the peer SPIFFE URI on every LLM proxy request and rejecting mismatches with `LEASE_SPIFFE_MISMATCH` + audit event `credential.lease_spiffe_mismatch`; (3) making SPIFFE-binding the default for all proxy-mode pools, with explicit opt-out (`credentialPool.spiffeBinding: disabled`) restricted to single-tenant and development deployments (emitting a `ProxyModeSpiffeBindingDisabled` warning event). No additional spec change is required for NET-005.

### ~~NET-006 Provider-Direct Egress Profile May Allow LLM Traffic to Bypass Gateway Proxy~~ **FIXED** [High]
**Section:** 13.2, 4.9

~~A pod with `provider-direct` egress and `proxyMode: true` has a direct network path to LLM provider CIDRs, bypassing the gateway proxy.~~

~~**Recommendation:** Enforce mutual exclusivity between `proxyMode: true` and `egressProfile: provider-direct` via admission webhook.~~

**Resolution:** Section 13.2 now contains a "`provider-direct` + `deliveryMode: proxy` mutual exclusivity (NET-006)" callout that enforces hard mutual exclusivity between these two settings at three layers: (1) pool registration validation in the warm pool controller rejects the illegal combination with a `InvalidPoolEgressDeliveryCombo` error; (2) the `lenny-pool-config` ValidatingAdmissionWebhook blocks pod creation for pools with this combination (fail-closed); (3) a Helm pre-install/upgrade hook validates all `credentialPools[*]` entries and fails the deployment if any pool violates the constraint. The callout also makes the correct pairings explicit: `deliveryMode: proxy` pairs with `egressProfile: restricted`, and `deliveryMode: direct` pairs with `egressProfile: provider-direct`.

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

**Status: Fixed** — Section 4.1 was updated as follows: (1) The bare `(TBD: validate in Phase 13.5)` labels in all four subsystem table rows were replaced with explicit notes stating the values are provisional and must be set per the calibration methodology. (2) The introductory paragraph above the extraction-triggers table was reworded to clearly state that all threshold values are provisional first-principles estimates requiring empirical replacement. (3) A new **"Threshold calibration methodology (Phase 2 deliverable)"** block was added after the table, specifying a five-step procedure: baseline sweep at 25/50/75/100% of Tier 2 load recording p50/p95/p99 per subsystem metric, identification of the empirical saturation point, threshold setting at saturation-point value minus 20% headroom, a sensitivity check that the threshold fires at least one full HPA scale cycle before saturation, and replacement of provisional values with calibrated values annotated with benchmark run ID and date. The block also designates this calibration as a Phase 2 exit criterion, with Phase 13.5 performing a second validation at Tier 3 scale. The LLM Proxy-to-session ratio guidance paragraph was also annotated as provisional pending Phase 2 results. No threshold numbers were changed — those remain placeholders to be replaced after the benchmark harness runs.

### SCL-002 No Per-Gateway-Replica Session Capacity Budget [Critical] ✅ Fixed
**Section:** 4.1, 16.5

Nowhere is there a stated maximum number of concurrent sessions a single gateway replica can handle. The HPA cannot be correctly dimensioned.

**Recommendation:** Define a per-replica session capacity budget derived from load testing. Use this as the primary HPA custom metric.

**Status: Fixed** — Two sections were updated. (1) Section 4.1 now defines `gateway.maxSessionsPerReplica` as the primary HPA custom metric. A new **"Per-replica session capacity budget"** block was added to the Gateway Deployment description, including: a table of provisional per-tier values (Tier 1: 50, Tier 2: 200, Tier 3: 200 — all annotated as provisional); a five-step **"Capacity budget calibration methodology (Phase 2 deliverable)"** specifying how to run a ramp test on a single replica, identify the empirical saturation point (when `lenny_stream_proxy_p99_attach_latency_ms` > 800 ms or `lenny_stream_proxy_queue_depth` > 500), set `maxSessionsPerReplica` at saturation minus 20% headroom, validate HPA fires one full scale cycle before saturation, and replace provisional values with calibrated values annotated with benchmark run ID. This calibration is a Phase 2 exit criterion aligned with the SCL-001 subsystem extraction threshold calibration. (2) Section 16.5 was updated in two places: a `GatewaySessionBudgetNearExhaustion` Warning alert was added (fires when `lenny_gateway_active_sessions / gateway.maxSessionsPerReplica` > 90% on any replica for > 60s), and a `Per-replica session capacity budget (maxSessionsPerReplica)` row was added to the capacity tiers table with the same provisional values. No threshold numbers were changed — those remain provisional placeholders pending Phase 2 benchmark harness results.

### SCL-003 Startup Latency SLOs Are Unvalidated Estimates [High]
**Section:** 6.3

SLOs are derived from per-phase latency budget tables built from estimates, not measurements. Phase 2 benchmark harness is planned but not yet delivered.

**Recommendation:** Block Tier 2 promotion on completion of the Phase 2 startup benchmark harness with actual P50/P95/P99 measurements.

**Status: Fixed** — Section 6.3 now contains an explicit **Tier 2 promotion gate** immediately after the latency budget table. The gate specifies three conditions that must all be satisfied before Tier 2 promotion is permitted: (a) actual P95 pod-warm latency measured and within target for runc and gVisor, (b) all per-phase histogram metrics producing data in the benchmark environment, and (c) benchmark results recorded with run ID/configuration and attached to the Phase 2 exit gate ADR. The note also explicitly prohibits use of the latency budget table as an SLO in any capacity agreement or customer-facing documentation until the gate is cleared. No existing target numbers were changed.

### SCL-004 Redis Sentinel Scalability Ceiling [High]
**Section:** 12.4

A single Redis primary cannot be horizontally scaled for writes. Tier 3 delegation budget serialization and per-session locks could saturate it.

**Recommendation:** Quantify Redis write throughput requirements at Tier 3 load. Pre-plan Redis Cluster migration as a Tier 2→3 transition step.

**Status: Fixed** — Section 12.4 now contains two new blocks. (1) **Tier 3 Redis write throughput quantification** — a table enumerating all write sources (quota increments, rate limit increments, delegation Lua scripts, lease renewals, token cache writes) with per-source ops/s estimates at Tier 3 scale, totalling ~600–650/s sustained (~2,000/s burst), with narrative explaining why raw throughput is not the binding constraint (CPU contention and pub/sub fan-out are) and cross-referencing the ceiling signals already defined in the section. (2) **Redis Cluster migration pre-plan (Tier 2→3 transition)** — a five-step procedure specifying the trigger condition (two simultaneous ceiling signals for 30+ minutes), target topology (6-node Cluster, Quota/Rate Limiting instance only), interface compatibility requirements (cluster-aware client library verification), migration steps (parallel deploy + per-tenant feature flag + 24h validation), and rollback path. The Coordination instance is explicitly called out as remaining on Sentinel throughout.

### SCL-005 HPA Custom Metric Pipeline Lag Not Formally Bounded [High]
**Section:** 10.1

End-to-end latency of the custom metric pipeline (Prometheus scrape + adapter + HPA) can add 30-90 seconds of lag. At 200 sessions/second, 6,000-18,000 attempts arrive under-provisioned.

**Recommendation:** Document the full pipeline latency. Consider KEDA with shorter polling intervals. Implement leading-indicator metrics.

**Status: Fixed** — Section 10.1 now contains two new blocks immediately after the custom metrics pipeline paragraph. (1) **Custom metric pipeline end-to-end latency** — a stage-by-stage table breaking down the Prometheus Adapter path (scrape 15s + adapter cache TTL 30s + HPA eval 15s = 60s worst case, 30–45s typical), with a calculation of the session-attempt exposure window at Tier 2 (up to 12,000 attempts at 200/s) and cross-references to the three existing mitigation mechanisms (minReplicas burst absorption, leading-indicator metrics, GatewaySessionBudgetNearExhaustion alert). (2) **Reducing pipeline lag — KEDA option** — documents that KEDA with `pollingInterval: 10s` reduces worst-case lag to ~20s, designates KEDA as the recommended option for Tier 3 deployments, and provides specific configuration recommendations for both paths (scrape interval 10s, adapter metricsRelistInterval 15s, KEDA pollingInterval 10s, queue-depth as primary trigger). No architectural changes — purely documentation of existing behavior with formal bounds.

### SCL-006 Postgres Write Path Has No Horizontal Scaling Route [High]
**Section:** 12.3

Tier 3 write IOPS (~1,300/s) approaches the practical write ceiling of a single Postgres primary with no stated strategy for scaling beyond.

**Recommendation:** Define a write load shedding strategy. Partition high-volume append-only tables to a separate instance. Classify writes as SLO-critical vs. best-effort.

**Status: Fixed** — Section 12.3 now contains a **Write classification and load shedding strategy (Tier 3)** block immediately after the "Separate Postgres for write-heavy paths" paragraph. The block: (1) classifies all four write sources (session state transitions = SLO-critical/never deferred; quota checkpoint flushes = SLO-critical/defer-interval only; billing events = best-effort durable/write-ahead buffer with back-pressure rejection; T3/T4 audit = SLO-critical; T2 audit = best-effort/highest tolerable drop); (2) defines a four-step horizontal write scaling route (vertical scale first → instance separation at 80% ceiling → append-only table partitioning by time range → write-ahead buffer as back-pressure); and (3) specifies monitoring requirements (`pg_stat_bgwriter`, `pg_stat_wal`, replication lag) with a new `PostgresWriteSaturation` warning alert added to Section 16.5 (fires when sustained write IOPS exceed 80% of estimated ceiling for > 5 minutes). The existing "optional" separate billing/audit instance recommendation is now promoted to a formal step in the scaling route with a specific trigger condition.

### SCL-007 PoolScalingController Cold-Start Formula Cannot Auto-Configure [High]
**Section:** 4.6.2

Historical traffic metrics are unavailable at first deployment. No cold-start default, convergence criteria, or bootstrap override is specified.

**Recommendation:** Specify explicit cold-start bootstrap: fallback `minWarm` value, convergence criteria, operator-facing override API, and a bootstrap-mode metric/alert.

**Status: Fixed** — Two sections were updated. (1) Section 4.6.2 "Cold-start limitation" paragraph was expanded to introduce the bootstrap mode concept (`status.scalingMode: bootstrap`) and cross-reference the new detailed procedure in Section 17.8.2, including the operator API endpoints and observability signals. (2) Section 17.8.2 now contains a full **Cold-start bootstrap procedure** block (5 numbered steps): step 1 sets the `bootstrapMinWarm` static override via admin API or Helm; step 2 documents the `status.scalingMode: bootstrap` CRD field and `lenny_pool_bootstrap_mode` gauge; step 3 specifies the operator-facing override API (`PUT` to set, `DELETE` to release, `GET` for bootstrap status including `estimatedConvergenceAt`); step 4 defines the five convergence criteria (48h of traffic data, formula stable for 2h at < 20% variance, no WarmPoolLow in 6h, formula target ≤ 3× bootstrap override); step 5 documents the `PoolBootstrapMode` Warning alert. Two new alerts were also added to Section 16.5: `PoolBootstrapMode` (fires after 72h in bootstrap mode) and `PoolBootstrapUnderprovisioned` (fires when formula target exceeds 3× bootstrap override). The existing first-week monitoring workflow was preserved and cross-linked to the new `DELETE` override API.

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

### ~~PRT-001 Elicitation Chain is Structurally MCP-Only — Would Break Under A2A~~ **FIXED** [High]
**Section:** 9.2, 8.2, 4.7

~~The entire elicitation chain is built on MCP's hop-by-hop model. A2A has no equivalent. Any delegation tree generating elicitations at depth >= 2 will degrade when surfaced via A2A.~~

~~**Recommendation:** In Section 21.1, explicitly define how elicitation chains are surfaced via A2AAdapter before implementation begins.~~

**Resolution:** Section 21.1 was extended with a dedicated **"Elicitation chains under A2A (design constraint)"** block that explicitly documents the design decision for v1 A2A support. The block defines three behaviors: (1) `A2AAdapter`-initiated sessions set `elicitationDepthPolicy: block_all` for agent-initiated elicitations — A2A has no hop-by-hop elicitation primitive, so suppression is the correct v1 posture rather than a silent breakage; (2) `lenny/request_input` calls that block a session transition to Lenny `input_required`, which maps to A2A `input-required` — the A2A caller responds by sending a new task message that the adapter routes as an `inReplyTo` reply, making `input-required` the canonical A2A substitute for the MCP elicitation round-trip; (3) the `A2AAdapter`-generated agent card advertises `elicitation: false` in its `capabilities` field so callers know in advance that elicitation-dependent flows are not available through this interface. A richer A2A elicitation model is explicitly scoped as post-v1, with the note that it requires no changes to the internal elicitation chain — only the `A2AAdapter` surface. The internal MCP elicitation chain (Section 9.2) is unchanged.

### ~~PRT-002 ExternalProtocolAdapter Interface Missing Outbound Push Contract~~ **FIXED** [High]
**Section:** 15, 21.1, 21.3

~~The interface has no mechanism for adapters to push subsequent state changes to registered webhook URLs after the initial response. `OutboundCapabilitySet` schema is not defined.~~

~~**Recommendation:** Define `OutboundCapabilitySet` concretely. Add an `OutboundChannel` mechanism for asynchronous event delivery.~~

**Resolution:** Section 15's `ExternalProtocolAdapter` interface was updated with two concrete additions. (1) `OutboundCapabilitySet` is now a defined struct with three fields: `PushNotifications bool` (adapter can deliver events after the initial response), `SupportedEventKinds []string` (the event kinds the adapter pushes), and `MaxConcurrentSubscriptions int` (per-session subscription cap). (2) `OpenOutboundChannel(ctx, sessionID, OutboundSubscription) (OutboundChannel, error)` was added as an optional method — adapters with no outbound push embed `BaseAdapter` and get a no-op implementation. The `OutboundSubscription` type carries the delivery target (webhook `CallbackURL` or held `ResponseWriter`) and adapter-specific metadata. The `OutboundChannel` interface defines `Send(ctx, SessionEvent) error` (non-blocking; non-nil error causes the gateway to close the channel) and `Close() error`. A **"Gateway outbound dispatch"** paragraph describes how the gateway iterates all active channels per session and calls `Send`. Section 21.1 was extended with a concrete **"A2A outbound push (OutboundChannel contract)"** block specifying the `A2AAdapter`'s implementation: `PushNotifications: true`, HMAC-signed webhook POSTs with 3-attempt retry back-off, SSE frame delivery for long-poll clients, and terminal frame delivery on `Close`. Section 21.3 was updated to reference `OutboundChannel` for AP support.

### ~~PRT-003 MCP Tasks Dependency — Core Session Lifecycle Uses MCP-Specific Concept~~ **FIXED** [High]
**Section:** 7.2, 4.1, 9.1

~~The gateway's session lifecycle is modeled as an MCP Task at the architectural layer, not just the adapter layer. If MCP Tasks evolves, gateway internals are affected.~~

~~**Recommendation:** Clarify that "Lenny canonical task state machine" is the internal model. Replace "the client interacts via an MCP Task" with protocol-neutral language.~~

**Resolution:** Section 7.2's opening sentence was updated from "the client interacts via an **MCP Task**" to protocol-neutral language: "the client interacts via a **Lenny session**." The sentence now explicitly states that the external protocol representation is determined by the active `ExternalProtocolAdapter` — an MCP Task for MCP clients, an A2A Task for A2A clients — and that the gateway operates internally against the **Lenny canonical task state machine** (Section 8.8), which is defined independently of any external protocol. The state machine diagram and terminal state list that follow are unchanged, as they already reference "canonical task states." This edit makes clear that MCP is one adapter-layer expression of the internal model, not a structural dependency of the gateway lifecycle.

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

### DXP-001 Standalone Adapter Specification Promised but Not Published [Critical] — FIXED

**Section:** 15.4
**Status:** Fixed

The spec states this will be "the primary document for community runtime adapter authors" but it does not exist yet. Runtime authors hit a hard blocker.

**Recommendation:** Either publish the standalone spec before community readiness, or promote Section 15.4 tables to be self-sufficient with wire encoding details and error codes.

**Resolution:** Section 15.4 now opens with an explicit status notice acknowledging that the standalone spec has not yet been published and targeting Phase 2 as the release milestone. The inline subsections (15.4.1–15.4.5) are declared the normative interim reference for runtime adapter authors, with a guarantee of no breaking changes when the standalone spec is released. The forward reference in Section 15.3 was also updated to remove the implication that the standalone spec is already available.

### DXP-002 Echo Runtime Is Pseudocode Only, Not Runnable [High] — FIXED
**Section:** 15.4.4
**Status:** Fixed

Cannot be compiled or run. Developers cannot distinguish "my runtime is wrong" from "my Lenny setup is wrong."

**Recommendation:** Provide at minimum one runnable Echo runtime in Go under `examples/runtimes/echo/`.

**Resolution:** A callout block was added immediately before the pseudocode in Section 15.4.4. It states that a fully runnable Go implementation will be published at `examples/runtimes/echo/` as a Phase 2 deliverable, compiles to a single static binary, and can be used with `lenny-dev` via `make run`. The pseudocode is retained as a readable summary; the Go source is declared the authoritative runnable reference. This allows runtime authors to distinguish platform setup problems from their own adapter bugs.

### DXP-003 Minimum-Tier Degraded Experience Not Consolidated [High] — FIXED
**Section:** 15.4.3, 15.4.5
**Status:** Fixed

Capabilities lost at Minimum tier (no checkpoint, no interrupt, no delegation, no MCP tools) are scattered across multiple sections.

**Recommendation:** Add a dedicated "Minimum-tier limitations" callout in Section 15.4.3 enumerating every unavailable capability.

**Resolution:** A "Minimum-tier limitations — complete list" callout block was added at the end of Section 15.4.3, immediately after the tier comparison matrix. The callout enumerates all eight unavailable capabilities with their practical consequence: checkpoint/restore, clean interrupt, credential rotation without disruption, delegation (`lenny/delegate_task`), platform MCP tools, connector MCP servers, `DEADLINE_APPROACHING` warning, and graceful drain. Each entry explains the fallback behavior (or lack thereof) so runtime authors understand the operational impact without having to cross-reference the matrix or other sections.

### DXP-004 OutputPart Rationale Does Not Show MCP Mapping [High] — FIXED
**Section:** 15.4.1 (rationale block within the OutputPart definition)
**Status:** Fixed

No side-by-side mapping between MCP content blocks and OutputPart fields. `from_mcp_content()` helper has no concrete home.

**Recommendation:** Add a mapping table. Confirm whether `from_mcp_content()` ships as part of a Go SDK or is a copy-paste pattern.

**Resolution:** Two changes were made in Section 15.4.1 immediately after the "Rationale for internal format" paragraph. (1) A "MCP content block → OutputPart mapping" table was added, covering all six inbound MCP content block variants (`TextContent`, `ImageContent` url/base64, `EmbeddedResource` text/blob/uri, and `isError` annotation) with the resulting `OutputPart.type`, `inline`, `mimeType`, `ref`, and any notes. (2) The `from_mcp_content()` description was expanded to specify: Go availability under `github.com/lenny-platform/lenny-sdk-go/outputpart` (Phase 2), a note that other languages are not yet published with a pointer to the mapping table as a copy-paste reference, and an explicit statement that no SDK dependency is required.

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
**Status:** Fixed

Helm does not update CRDs on `helm upgrade`. No tooling, pre-upgrade hook, or runbook makes this step reliable. Stale CRDs cause "silent failures."

**Recommendation:** Provide a `lenny-upgrade` script that diffs CRDs, applies them, waits for establishment, then triggers `helm upgrade`. Extend lenny-preflight to assert CRD version currency.

**Resolution:** Added a dedicated **CRD upgrade procedure** subsection to Section 10.5 (immediately following the existing `**Helm CRD upgrade limitation:**` paragraph). The new subsection specifies a `lenny-upgrade` script (`scripts/lenny-upgrade.sh`) and `make upgrade` Makefile target that automates the full five-step sequence: (1) preflight CRD version assertion via `lenny-ctl preflight`, (2) `kubectl diff` of CRDs with operator confirmation, (3) `kubectl apply -f charts/lenny/crds/`, (4) `kubectl wait --for=condition=Established` on each updated CRD, (5) `helm upgrade`. Script usage (interactive and non-interactive/CI modes) is documented inline. A GitOps sync-wave note is included. The existing Section 17.6 detail (post-upgrade validation hook, recovery procedure) is preserved and cross-referenced.

### OPS-002 Tier 2 Local Dev Default Is Plain HTTP [Critical]
**Section:** 17.4
**Status:** Fixed

The Docker Compose tier defaults to "no mTLS" — the mTLS code path is never exercised in development.

**Recommendation:** Add a `make compose-tls` variant. Mark plain-HTTP as unsupported for TLS-related development.

**Resolution:** Section 17.4 updated with two targeted changes: (1) The Tier 2 bullet for the gateway now explicitly marks plain-HTTP as "unsupported for TLS-related development" and references `make compose-tls`. (2) The credential-testing profile section now introduces `make compose-tls` as a Makefile alias for `docker compose --profile credentials up`, and adds an explicit statement that the plain-HTTP default does not exercise the mTLS code path and must not be used for TLS-related development.

### OPS-003 Expand-Contract Phase 3 Has No Enforcement Gate [Critical] — Fixed
**Section:** 10.5
**Status:** Fixed

No mechanism enforces the Phase 3 verification condition. An operator who deploys Phase 3 prematurely silently drops columns with live data.

**Recommendation:** Encode verification as a required migration prerequisite. The migration runner should query a count expression and abort if nonzero.

**Resolution:** Section 10.5 now mandates a **Phase 3 enforcement gate** as a required migration prerequisite. Every Phase 3 migration file must open with a PL/pgSQL `DO` block that counts un-migrated rows for the affected column. If the count is nonzero, the block raises an exception, which causes the migration runner to abort with a non-zero exit code and emit a structured error message (`"Phase 3 gate failed: <N> un-migrated rows remain in <table>.<old_column>. Resolve data migration before retrying."`). Because the `DO` block runs inside the same transaction as the subsequent `DROP COLUMN`, the gate is not skippable by the operator and is held under the advisory lock that prevents concurrent migration runs. A concrete example (using `sessions.legacy_token`) is included in the spec to serve as the implementation template.

### OPS-004 No Operational Runbooks Exist [High]
**Section:** 17.7
**Status:** Fixed

Referenced runbook sections are either empty or not present. No step-by-step recovery procedure for any failure condition.

**Recommendation:** Produce minimum viable runbooks for: pool drain, stuck session eviction, failed migration rollback, PgBouncer saturation, Redis split-brain, cert-manager failure.

**Resolution:** Section 17.7 now includes minimum viable runbook stubs for all key failure scenarios using a consistent Trigger / Diagnosis / Remediation structure. New stubs added: **Warm pool exhaustion** (trigger: `WarmPoolBelowMinimum` alert; diagnosis: pod state inspection, warmup latency metrics; remediation: emergency scale, node pressure checks, image pull verification); **Postgres failover** (trigger: `PostgresDown` alert; diagnosis: PgBouncer pool state, replication lag; remediation: reload PgBouncer, verify DSN, audit lost sessions); **Redis failure and recovery** (trigger: `RedisUnavailable` alert; diagnosis: Redis connectivity, Sentinel/Cluster status; remediation: fail-open window management, quota reconciliation via `lenny-ctl admin quota reconcile`); **Credential pool exhaustion** (trigger: `CredentialPoolExhausted` alert; diagnosis: `availableCount`/`coolingDownCount` via admin API; remediation: add credentials, adjust `maxConcurrentSessions`, rotate rate-limited credentials); **Gateway replica failure** (trigger: `GatewayReplicasLow` alert; diagnosis: pod crash reason, HPA state; remediation: memory tuning, CRD mismatch check, session continuity verification); **cert-manager outage** (trigger: `AdmissionWebhookUnavailable` or certificate failures; diagnosis: cert-manager pod status, certificate describe; remediation: rollout restart, emergency manual cert issuance, auto-renewal verification). All stubs reference relevant alerts (Section 16.5) and are cross-referenced from the alert definitions. The overall structure introduction (Trigger/Diagnosis/Remediation) is added at the top of Section 17.7 for clarity.

### OPS-005 minWarm Pool Gap at First Deployment [High]
**Section:** 5.1, 5.2, 4.6
**Status:** Fixed

No defined bootstrap behavior for pools with zero pods. First session requests experience cold-start latency with no user-visible signal.

**Recommendation:** Define explicit bootstrap behavior: `PoolWarmingUp` condition on Pool CRD, `503 Pool Not Ready` with `Retry-After` when zero warm pods.

**Resolution:** Section 5.2 now includes a **"Bootstrap Behavior — Pool Warming at First Deployment"** subsection specifying: (1) **`PoolWarmingUp` condition** on `SandboxTemplate` CRD — set to `True` when `minWarm > 0` and `idlePodCount == 0` with pods in `warming` state; cleared once `idlePodCount >= 1`; `reason` field carries `Provisioning` or `Drained`. (2) **`503 Pool Not Ready` client response** — when a session creation request hits a `PoolWarmingUp` pool, the gateway returns `HTTP 503` with `Retry-After` set to `max(30, estimatedWarmupSeconds)` (derived from `lenny_warmpool_warmup_latency_seconds` p50, fallback 120s) and a structured JSON body with `RUNTIME_UNAVAILABLE` code, `retryable: true`, and `details.poolCondition: "PoolWarmingUp"`, `details.podsWarming: <count>`. (3) **`WarmPoolBootstrapping` alert** — fires when `PoolWarmingUp = True` for more than `warmupDeadlineSeconds` (default 300s), surfacing bootstrap failures (image pull, node pressure, quota) before sustained unavailability. (4) **Operator visibility** — `GET /v1/admin/pools/<name>` returns `poolCondition: PoolWarmingUp` and `idlePodCount: 0` during bootstrap.

### OPS-006 etcd Tuning Is Outside Lenny's Scope on Managed K8s [High]
**Section:** 12.3, 17
**Status:** Fixed

No matrix distinguishing managed Kubernetes (tuning impossible) from self-managed (operator responsibility).

**Recommendation:** Add a per-topology matrix listing what tuning Lenny requires vs. what the provider handles.

**Resolution:** Section 4.6 (etcd operational tuning block) now includes an **"etcd tuning topology matrix"** table covering all six tuning areas (compaction mode/retention, quota-backend-bytes, snapshot-count, defragmentation, quota exhaustion recovery, snapshot/backup) across two columns: Managed K8s (EKS, GKE, AKS) and Self-managed (kubeadm, Talos, etc.). For each area, the table specifies whether the setting is provider-controlled (operator cannot change) or operator responsibility, with concrete examples (e.g., EKS KMS ARN, GKE CMEK command, defrag CronJob warning for managed clusters). A "write rate reduction" row applies to both topologies and lists Lenny-side controls (status update deduplication window, pool count, dedicated etcd cluster). A summary sentence closes the block: on managed K8s, the only available levers are Lenny-side write rate controls; on self-managed, operators own the full set. The etcd operations runbook (Section 17.7) is cross-referenced for self-managed procedures.

### OPS-007 Bootstrap Seed Job Has No Idempotency Documentation [High]
**Section:** 15.1, 17
**Status:** Fixed

Not specified whether running the bootstrap Job twice produces duplicates, no-ops, or errors.

**Recommendation:** Specify upsert semantics. Define that initial admin credential is written to a Kubernetes Secret with a documented rotation procedure.

**Resolution:** Section 17.6 bootstrap seed section now includes two new items. (1) **Upsert semantics — full specification**: a table covering all four conditions (resource absent → create; exists with identical fields → no-op; exists with differing fields → skip with WARN log by default, update with `--force-update`; security-critical field conflict → error regardless of flag). Running the bootstrap Job twice on a clean cluster is a complete no-op. Operators must pass `--force-update` to apply changed seed values to already-existing resources on `helm upgrade`. (2) **Initial admin credential — Kubernetes Secret handling**: specifies that the bootstrap Job creates a `platform-admin` user and writes the generated API token to Secret `lenny-system/lenny-admin-token` using an idempotent `kubectl apply` that does not regenerate the token on re-run. Secret shape (keys: `token`, `created_at`) and naming convention are defined. Rotation procedure: `lenny-ctl admin users rotate-token` updates Postgres and patches the Secret atomically with immediate old-token invalidation. First-use prompt vs. re-run message behavior is documented. RBAC constraint (bootstrap Job ServiceAccount has `create`/`get`/`patch` on Secrets in `lenny-system` only) is specified.

### OPS-008 Credential Pool Kubernetes Secret Topology Underspecified [High]
**Section:** 13, 15.1
**Status:** Fixed

Whether each credential is a separate Secret, shares one Secret, or is database-backed with a Secret encryption key is unspecified.

**Recommendation:** Add a section defining credential storage topology and the KMS key hierarchy.

**Resolution:** Section 4.9 (Credential Pool) now includes a **"Credential Storage Topology"** subsection specifying: (1) **Secret-per-credential topology is required** (not optional) — rationale: revocation granularity, RBAC granularity, rotation isolation. Each credential in a pool uses a separate Kubernetes Secret named `lenny-{pool-name}-{credential-id}`. (2) **Secret shape per provider** — table mapping each provider (`anthropic_direct`, `aws_bedrock` role/access-keys, `vertex_ai`, `azure_openai`) to the Secret key names and value format. (3) **KMS key hierarchy table** — covers EKS (envelope encryption via AWS KMS CMK ARN), GKE (CMEK or Google-managed default), AKS (Azure Disk Encryption or Key Vault), and self-managed (EncryptionConfiguration with aescbc/aesgcm/kms). Each entry specifies the enablement command and verification step. The preflight warning (Section 17.6) cross-references this section. (4) **RBAC for the Token Service** — minimum Role definition with `resourceNames` populated from bootstrap `secretRef` list; guidance for operationally-added credentials (RBAC patch required). (5) **Tier 3 large-pool management guidance** — External Secrets Operator for hundreds of credentials, preserving the Secret-per-credential topology while eliminating manual Helm updates.

### OPS-009 Rolling Pool Rotation Procedure Is Incomplete [High]
**Section:** 10.5, 5.1
**Status:** Fixed

No rotation state machine, drain handling, pause capability, or schema migration interaction documented.

**Recommendation:** Define a `RuntimeUpgrade` state machine (Pending → Expanding → Draining → Contracting → Complete) with a pause command.

**Resolution:** Section 10.5 now replaces the informal four-step pool rotation description with a formal **`RuntimeUpgrade` state machine**. States: `Pending` (registered, not started) → `Expanding` (new pool ramping up, canary routing active) → `Draining` (old pool accepts no new sessions, existing sessions drain) → `Contracting` (old CRD being deleted) → `Complete` (terminal). A `Paused` state is reachable from any non-terminal state via `lenny-ctl admin pools upgrade pause` and exits via `resume`. State table specifies entry condition, exit condition, and transition description for each state. **Drain handling**: `Draining` exits when old pool `activePodCount == 0` or `drainTimeoutSeconds` (default `maxSessionAge`) expires; on timeout, remaining sessions are force-terminated with checkpoint. **Pause/resume**: operator commands halt all state machine activity while preserving current pool states. **Schema migration interaction**: if the new image requires a schema migration, `drainFirst: true` forces the state machine to complete `Draining` before `Contracting`; the gateway blocks Phase 3 DDL while `upgradeState != Complete` for the referenced pool. **Rollback**: from `Expanding` — sets new pool `minWarm` to 0, restores old pool routing; from `Draining`/`Contracting` — recreates old `SandboxTemplate` from `RuntimeUpgrade.previousPoolSpec`; key safety invariant: WarmPoolController blocks `SandboxTemplate` deletion while an active `RuntimeUpgrade` references it. An operational example with CLI commands is included. The old four-step manual procedure is retained as "manual rotation sequence (reference only)" for environments where the CLI is unavailable.

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
**Status:** Fixed

Cloud-managed poolers (AWS RDS Proxy, GCP Cloud SQL Auth Proxy) don't support `connect_query`. Without the per-transaction trigger (only created when `postgres.connectionPooler = external` Helm flag is set), tenant isolation rests entirely on application correctly issuing `SET LOCAL`.

**Recommendation:** Make `external` pooler mode the default when any cloud-managed pooler is detected. Add a startup check that refuses to proceed without the flag. Add an integration test that deliberately skips `SET LOCAL` and asserts no cross-tenant row is readable.

**Fix applied (2026-04-07):** Three additions made to Section 12.3 "Alternative RLS defense for cloud-managed poolers":

1. **Auto-default** (new item 3): When `deploymentProfile = cloud-managed`, `postgres.connectionPooler` now defaults to `external` if not explicitly set, eliminating the silent misconfiguration path. Overriding to `pgbouncer` requires an explicit opt-out.

2. **Gateway startup refusal** (new item 4): The gateway now inspects `LENNY_POOLER_MODE` (injected from the Helm value) on startup and refuses to start (fatal exit) if `LENNY_POOLER_MODE = external` but the `lenny_tenant_guard` trigger is absent from tenant-scoped tables. Error message: `"FATAL: cloud-managed pooler mode (LENNY_POOLER_MODE=external) detected but lenny_tenant_guard trigger is absent from tenant-scoped tables..."`. This check runs on every gateway start, not just at install time, catching trigger removal after initial deployment.

3. **Integration test requirement** (new item 5): `TestRLSTenantGuardMissingSetLocal` is now required in CI. The test covers two cases: (a) transaction without `SET LOCAL` must be blocked by the trigger; (b) transaction for tenant A attempting to read tenant B's rows must return zero rows.

### TNT-002 Redis DLQ Keys Lack Explicit Tenant Namespace [High]
**Section:** 7.2, 12.4
**Status:** Fixed

DLQ keys use `session_id:dlq` format without the required `t:{tenant_id}:` prefix. A DLQ processor operating across all keys could read messages belonging to a different tenant.

**Recommendation:** Document canonical DLQ key format as `t:{tenant_id}:session:{session_id}:dlq`. Extend `TestRedisTenantKeyIsolation` to cover DLQ keys.

**Fix applied (2026-04-07):** Two changes made to the technical design:

1. **§7.2 dead-letter handling table:** The DLQ Redis sorted-set key was updated from the ambiguous `session_id:dlq` to the canonical tenant-namespaced form `t:{tenant_id}:session:{session_id}:dlq`. The table cell now includes an explicit rationale sentence: "The canonical DLQ key format is `t:{tenant_id}:session:{session_id}:dlq` — this follows the platform-wide tenant key prefix convention (§12.4) and ensures a DLQ processor iterating across keys cannot read messages belonging to a different tenant."

2. **§12.4 tenant key isolation paragraph:** A cross-reference to the DLQ key convention was added (`DLQ keys follow the same convention: t:{tenant_id}:session:{session_id}:dlq — see §7.2`). The `TestRedisTenantKeyIsolation` requirement was extended to mandate DLQ key coverage: (a) a DLQ write for tenant A must not be readable by a DLQ processor scoped to tenant B, and (b) a DLQ processor performing a cross-tenant `ZRANGEBYSCORE` scan must return zero results when the key prefix belongs to a different tenant.

### TNT-003 Task-Mode Tenant Pinning Enforced Only at Application Layer [High]
**Section:** 5.2
**Status:** Fixed

No Kubernetes-layer mechanism prevents a task-mode pod labeled for tenant A from being assigned to tenant B if gateway logic has a bug.

**Recommendation:** Label warm-pool pods with `lenny.io/tenant-id`. Add an admission webhook that rejects tenant label changes from non-`unassigned` values.

**Fix applied (2026-04-07):** The tenant pinning paragraph in §5.2 was restructured to document two independent enforcement layers:

1. **Application layer (existing, clarified):** The existing gateway-side `tenantId` check is retained and described as the primary routing enforcement.

2. **Kubernetes layer (new):** Warm-pool pods are now labeled `lenny.io/tenant-id: {tenant_id}` at first assignment time by the gateway agent. A new `ValidatingAdmissionWebhook` (`lenny-tenant-label-immutability`) rejects any API request that mutates the `lenny.io/tenant-id` label on an existing pod to a different non-empty value. Permitted transitions are: unset → `{tenant_id}` (initial assignment) and `{tenant_id}` → `unassigned` (pod return to pool). Any other mutation is rejected with HTTP 403 and error `tenant_label_immutable`. The webhook is deployed via the Helm chart under `templates/admission-policies/` and is covered by the existing admission policy integration test suite (`tests/integration/admission_policy_test.go`). This provides defense-in-depth: a gateway application-layer bug cannot silently re-label a pod to a different tenant via the Kubernetes API.

### TNT-004 `noEnvironmentPolicy` Platform-Default Not Explicitly Specified [High]
**Section:** 10.6
**Status:** Fixed

A reader cannot determine whether omitting `noEnvironmentPolicy` results in `deny-all` (safe) or `allow-all` (unsafe).

**Recommendation:** Add explicit normative statement: "The platform default is `deny-all`." Add a validation webhook that emits a warning when set to `allow-all`.

**Fix applied (2026-04-07):** Three additions made to §10.6:

1. **Normative default statement:** The `noEnvironmentPolicy` field definition was updated with the explicit normative statement: "The platform default is `deny-all`." The sentence is immediately followed by: "This is a normative requirement: an omitted `noEnvironmentPolicy` field — whether at the platform level (Helm) or at the tenant level (admin API) — MUST be treated as `deny-all` by the gateway." This removes all ambiguity about the safe default.

2. **Validation webhook (`lenny-noenvironmentpolicy-audit`):** A new `ValidatingAdmissionWebhook` is specified that inspects `PUT /v1/admin/tenants/{id}/rbac-config` and emits a non-blocking audit warning (HTTP 200 with `Warning:` response header per RFC 9110 §11.5) whenever `noEnvironmentPolicy` is explicitly set to `allow-all`. The warning text and the `lenny_noenvironmentpolicy_allowall_total` counter (labeled by `tenant_id`) are documented. The webhook does not block the request — it is an advisory audit control, not an enforcement gate, preserving operator autonomy while surfacing the security-sensitive configuration change.

3. **Invalid value handling:** A sentence was added clarifying that no other values are valid and the gateway rejects unrecognised values at tenant RBAC config validation time.

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
**Status:** Fixed

During dual outage (Redis + Endpoints), `replica_count` falls back to 1. With N replicas, total exposed spend is `N × tenant_limit` before fail-closed triggers.

**Recommendation:** Store last-known replica count in a local variable. Add a hard cap: `effective_ceiling = min(tenant_limit / max(cached_count, 1), per_replica_hard_cap)`.

**Resolution:** Applied in Sections 12.4 and 11.2. Each gateway replica now maintains a `cached_replica_count` in-memory variable that is updated on every successful Endpoints poll and persists across poll failures (last-known good value). During a dual outage, replicas use the cached count rather than defaulting to `1`, eliminating the `N × tenant_limit` exposure. A hard per-replica cap is also enforced: `effective_ceiling = min(tenant_limit / max(cached_replica_count, 1), per_replica_hard_cap)`, where `per_replica_hard_cap` defaults to `tenant_limit / 2` and is configurable via `quotaPerReplicaHardCap`. The Maximum Overshoot Formula in Section 11.2 was updated to reflect the corrected dual-outage bound.

### STR-002 Audit Event Batching Silently Loses Events on Gateway Crash [Critical]
**Section:** 12.2
**Status: Fixed**

250ms batch window can silently drop up to ~75 events on gateway crash. No dead-letter or WAL mechanism exists for the audit buffer.

**Recommendation:** Default to synchronous writes unconditionally for T3/T4 audit events. Make batching explicit opt-in with documented data-loss warning.

**Resolution:** Applied in Section 12.3 (batching guidance) and the Section 12.3 failover durability table. The fix has three parts:

1. **T3/T4 audit events are now unconditionally synchronous.** The spec now explicitly states that user-scoped audit events (T3 — Confidential, per Section 12.7 classification table) and T4 — Restricted audit events are written synchronously with no batching buffer, regardless of `LENNY_ENV` or SIEM configuration. `auditFlushIntervalMs` is ignored for these events. This eliminates the crash data-loss window for all compliance-significant audit records.

2. **Batching is now explicit opt-in for T2 (non-PII) events only.** The spec now requires `audit.batchingEnabled: true` (disabled by default) to activate batching, and mandates that the deployment record its accepted data-loss risk. The loss-window table (~75 events at Tier 3) is preserved as an explicit WARNING block so the trade-off is visible at the point of configuration.

3. **Failover durability table split.** The single "Audit events in flush buffer — At-risk" row is replaced by two rows: T3/T4 events are now "Durable" (synchronous), and T2 events carry a conditional "At-risk if batching enabled" note with the opt-in caveat.

The Section 17.8 Postgres sizing table is also updated: the combined `Billing/audit batch flush interval` row is split to separately represent billing (unchanged), unconditional T3/T4 synchronous mode, and the opt-in T2 batching parameters.

### STR-003 T4 Workspace Data on emptyDir Lacks Per-Tenant Key Isolation [High] ✅ FIXED
**Section:** 6.4, 12.8
**Status:** Fixed in iteration 1.

Node-level disk encryption doesn't provide per-tenant key isolation for T4 sessions. Node compromise exposes all T4 tenants on that node.

**Recommendation:** Require T4 workloads to run on dedicated nodes via `nodeSelector` with a dedicated KMS key per node.

**Fix applied:** Section 6.4 (Data-at-rest protection, `/workspace/` bullet) extended with a new **T4 dedicated-node requirement** sub-section. It specifies: (1) every pool referencing a T4 Runtime must include a `nodeSelector` matching `lenny.dev/workspace-tier: t4` in its `SandboxTemplate` pod spec, and the pool controller rejects T4 pool creation without this selector; T4 nodes must carry the `lenny.dev/workspace-tier=t4:NoSchedule` taint. (2) A fail-closed `ValidatingAdmissionWebhook` (`lenny-t4-node-isolation`, `failurePolicy: Fail`) is deployed by the Helm chart; it rejects T4 pods missing the required `nodeSelector`/toleration and rejects non-T4 pods that carry T4 node selectors (preventing accidental co-location). Section 12.8 (Compliance Interfaces) now opens with a **T4 ephemeral workspace isolation** paragraph that cross-references these controls and clarifies the relationship between the node-isolation requirement (ephemeral data) and SSE-KMS (durable MinIO data).

### STR-004 Storage Quota Enforcement Mechanism Undefined [High] ✅ FIXED
**Section:** 11.2, 12.5
**Status:** Fixed in iteration 1.

No mechanism described for checking or enforcing storage quota at upload or checkpoint time. MinIO OSS has no built-in per-prefix quota.

**Recommendation:** Define explicit enforcement: pre-upload size check against Redis counter, post-upload increment, GC-triggered decrement.

**Fix applied:** A new **Storage quota enforcement mechanism** sub-section added to Section 11.2, defining a three-step lifecycle: (1) pre-upload size check — gateway reads tenant `storage_bytes_used` Redis counter, computes projected usage, and rejects with `STORAGE_QUOTA_EXCEEDED` if it would exceed `storageQuotaBytes`; client supplies `Content-Length` for uploads, adapter supplies tar size for checkpoints. (2) Post-upload atomic Redis increment using confirmed object size. (3) GC-triggered atomic Redis decrement when artifacts are deleted, using `artifact_size_bytes` stored in the `artifact_store` Postgres table (source of truth). Redis counters are rehydrated from Postgres on restart. Exports `lenny_storage_quota_bytes_used` gauge; `StorageQuotaHigh` alert fires at 80% utilization. Section 12.5 now includes a **Storage quota enforcement** paragraph that cross-references Section 11.2 and documents the `artifact_size_bytes` column and GC integration.

### STR-005 Checkpoint Scaling Bottleneck Under Large Workspaces [High] ✅ FIXED
**Section:** 4.4, 12.5
**Status:** Fixed in iteration 1.

500MB workspaces can take 5-10s for checkpoint during which the agent is entirely unresponsive. Incremental checkpoints deferred.

**Recommendation:** Enforce a hard emptyDir size limit on `/workspace/`. Add `lenny_checkpoint_duration_seconds` histogram. Document quiescence-to-workspace-size relationship.

**Fix applied:** A new **Hard workspace size limit** paragraph added to Section 4.4 immediately after the existing Checkpoint duration SLO paragraph. It specifies: (1) `/workspace/current` (and per-slot paths) must carry `emptyDir.sizeLimit` set to the pool-level `workspaceSizeLimitBytes` (default 512Mi), enforced by kubelet eviction. (2) The adapter performs a pre-checkpoint workspace size probe; if size exceeds the limit, the checkpoint is aborted without quiescing the runtime — the `lenny_checkpoint_size_exceeded_total` counter is incremented and a `checkpoint.skipped` event is sent to the client. (3) The `lenny_checkpoint_duration_seconds` histogram (already referenced in the existing SLO paragraph) is mandated for all checkpoint completions. (4) A new `CheckpointDurationHigh` alert fires when P95 of `lenny_checkpoint_duration_seconds` for Full-tier or embedded-adapter pools exceeds 10 seconds. (5) The quiescence-to-workspace-size relationship is documented as a linear formula: `expected_quiescence_seconds ≈ workspace_bytes / (100 × 1024 × 1024)`.

### STR-006 Node Drain + MinIO Unavailability Creates Cascading Checkpoint Failure [High] ✅ FIXED
**Section:** 4.4, 12.5
**Status:** Fixed in iteration 1.

Concurrent node drain and MinIO degradation causes all checkpoint attempts to fail. Standard/Minimum tier fallback is silent failure.

**Recommendation:** Add a pre-drain webhook that checks MinIO health before allowing pod eviction. Store a compressed workspace index in Postgres as fallback.

**Fix applied:** A new **Pre-drain MinIO health check** paragraph added to Section 12.5, immediately after the existing risk paragraph. It specifies: (1) The gateway exposes a `GET /internal/drain-readiness` endpoint that performs a MinIO `HeadBucket` liveness probe (2-second timeout) and returns `200` (healthy) or `503` (unhealthy). (2) A fail-closed `ValidatingAdmissionWebhook` (`lenny-drain-readiness`, `failurePolicy: Fail`) intercepts `CREATE` on `nodes/eviction` objects and calls the gateway drain-readiness endpoint; it rejects the eviction with a clear message if MinIO is unhealthy or unreachable, blocking both manual `kubectl drain` and cluster-autoscaler evictions. (3) An emergency bypass via `lenny.dev/drain-force: "true"` node annotation permits forced drain with a `node.drain.forced` critical audit event. (4) `lenny_drain_readiness_checks_total` counter tracks outcomes. The workspace-index-in-Postgres fallback from the recommendation was not added — the existing Postgres minimal state record (Section 4.4) already serves as the last-resort fallback; the pre-drain check is the primary mitigation that prevents entering that fallback unnecessarily.

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

### ~~DEL-001 Stale Summary Contradicts Section 8.6 on Rejection Permanence~~ **FIXED** [High]
**Section:** 8.6, 19

Section 19 says "rejection is permanent for the subtree" but Section 8.6 defines a recoverable cool-off period (`rejectionCoolOffSeconds`, default 300s).

**Recommendation:** Correct Section 19 to read: "rejection triggers a cool-off period for the denied subtree."

**Fix applied:** Section 19, decision table row 13 updated. The phrase "rejection is permanent for the subtree" was replaced with "rejection triggers a cool-off period (`rejectionCoolOffSeconds`, default 300s) for the denied subtree — after the cool-off expires the subtree may request extensions again." This now accurately reflects the recoverable cool-off model specified in Section 8.6 (lines 2826–2830), where `rejectionCoolOffSeconds` bounds the denial window and the subtree can re-enter the elicitation cycle after expiry. The admin API escape hatch (`DELETE /admin/v1/trees/{treeId}/subtrees/{sessionId}/extension-denial`) is already documented in Section 8.6 and is unaffected.

### ~~DEL-002 Deep Tree Recovery (Depth 5+) with Multiple Simultaneous Failures Unspecified~~ **FIXED** [High]
**Section:** 8.10

Serial recovery across 5 levels consumes exactly `maxTreeRecoverySeconds` (600s), leaving zero margin. Multi-node failure across non-adjacent depths is unaddressed.

**Recommendation:** Add guidance that deployers using deep trees should increase `maxTreeRecoverySeconds`. Specify behavior for non-adjacent failures.

**Fix applied:** Two new paragraphs added to Section 8.10 immediately after the `maxResumeWindowSeconds` interaction note. (1) **Deep-tree deployer guidance** — states that the 600s default is sized for ≤4 levels; provides a sizing formula (`maxTreeRecoverySeconds ≥ maxDepth × maxLevelRecoverySeconds + buffer`) with a concrete example for depth 6 (840s minimum); references the Helm key `delegation.maxTreeRecoverySeconds`. (2) **Non-adjacent simultaneous failures** — specifies that recovery proceeds in strict bottom-up depth order regardless of which depths failed; shallower failures are deferred until deeper levels are resolved; all failures share a single `maxTreeRecoverySeconds` budget; deployers with zone-level failure exposure should size the budget additively.

### ~~DEL-003 `credentialPropagation: inherit` Multi-Hop Semantics Undefined~~ **FIXED** [High]
**Section:** 8.3

The spec never specifies whether `credentialPropagation` applies per-hop or is tree-wide. `LeaseSlice` includes no credential-scoping field.

**Recommendation:** Explicitly state whether it governs the immediate hop or applies recursively. Add a worked example across a 3-level tree with mixed modes.

**Fix applied:** Two new blocks added to Section 8.3 immediately after the credential propagation mode table. (1) **`credentialPropagation: inherit` multi-hop semantics** — explicitly states that the mode is per-hop: each node's `credentialPropagation` value in its outgoing delegation lease governs only the single hop to its direct child; deeper hops are governed by each intermediate node's own lease setting, not by the root's. (2) **Worked example — 3-level tree with mixed modes** — shows Root (`inherit`) → Child A (`independent`) → Child B (`deny`) with explanations: Root's credential pool is shared to Child A; Child B gets a fresh independent credential; Child B's hypothetical children would receive no credential. The deployer guidance note on fan-out was also updated to clarify that the pool exhaustion risk applies specifically to contiguous `inherit` hops rather than the entire tree.

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
**Status:** Fixed

No on-demand checkpoint at derive time. Source checkpoint may be arbitrarily stale (up to 10 minutes). Concurrent derives have no locking or ordering guarantee.

**Recommendation:** Add a session-level lock for concurrent derives. Consider rejecting derive on `running` sessions unless `allowStale: true` is passed.

**Resolution:** Section 7.1 "Derive session semantics" was updated with two targeted additions:
1. **Staleness gate (`allowStale`):** Derive on non-terminal sessions (`running`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`) is now rejected by default with `409 DERIVE_ON_LIVE_SESSION`. Clients must pass `allowStale: true` to opt in. The response documents the snapshot age via `workspaceSnapshotTimestamp` and a staleness warning (up to the full 10-minute checkpoint interval).
2. **Concurrent derive serialization:** A new rule 2 specifies that the gateway acquires a per-source-session Redis advisory lock (`SETNX derive_lock:{source_session_id}`, TTL 30 s) before reading the snapshot reference. Concurrent callers are serialized; lock contention beyond 5 seconds returns `429 DERIVE_LOCK_CONTENTION`. The lock is released immediately after the snapshot reference is read.

### SLC-002 Generation Counter Fencing Window [Critical]
**Section:** 10.1
**Status:** Fixed

If `CoordinatorFence` fails all retries, the generation counter has already been incremented. The next coordinator arrives with a skipped generation. Old coordinator's in-flight changes may still land.

**Recommendation:** Specify generation increment as a CAS operation. Define cleanup actions on receiving a fenced generation value higher than expected+1.

**Resolution:** Two targeted changes made to Section 10.1:

1. **Step 1 (Increment generation) converted to CAS:** The increment SQL was changed from a blind `coordination_generation + 1` to a compare-and-swap: `UPDATE sessions SET coordination_generation = $expected_generation + 1 WHERE id = $session_id AND coordination_generation = $expected_generation RETURNING coordination_generation`. The coordinator must supply the generation value it observed at lease acquisition as the precondition. If 0 rows are updated (another coordinator already incremented), the replica must re-read the counter, discard its lease claim, and restart from lease acquisition. This closes the double-increment window that arose when a coordinator crashed after writing to Postgres but before completing the fence.

2. **Gap detection added to pod fence handling (Step 2):** A new "Gap detection on the pod" sub-rule specifies that when the adapter receives `CoordinatorFence(new_generation)` and `new_generation > last_fenced_generation + 1`, it must: (a) cancel and discard all in-flight RPCs received after `last_fenced_generation` (from unfenced coordinators), (b) reset any transient tool-call or lifecycle state accumulated since the last fenced coordinator, (c) log a `coordinator_generation_gap` structured event, and (d) acknowledge the fence normally. A gap of exactly 1 is the normal case requiring no special handling.

### SLC-003 SSE Buffer Overflow — Drop Connection Without Guaranteed Replay Window [High]
**Section:** 7.2
**Status:** Fixed

Events beyond the buffer are replayed from EventStore "if within the checkpoint window" — but the replay window is not defined. Events outside the window are client data loss.

**Recommendation:** Define replay window explicitly. Include an `events_lost` field in the `checkpoint_boundary` marker. Document this as a client data-loss event.

**Fix applied (§7.2):** The replay window is now explicitly defined as `max(periodicCheckpointIntervalSeconds × 2, 1200s)` (default 1200 s / 20 min). The `checkpoint_boundary` marker schema is now fully specified and includes an `events_lost` integer field (count of unreplayable events between the client cursor and the start of the replay window), a `reason` field (`"replay_window_exceeded"` or `"event_store_unavailable"`), and a `checkpoint_timestamp`. The spec mandates that clients MUST treat `events_lost > 0` as a data-loss event. The SSE buffer policy prose was updated to reference the newly defined replay window rather than the vague "checkpoint window".

### SLC-004 Checkpoint Failure During SIGSTOP — Watchdog Restarts Without SIGCONT Confirmation [High]
**Section:** 4.4
**Status:** Fixed

No OS primitive confirms the process actually received SIGCONT and resumed. In pathological cases, the agent can be frozen beyond the 60-second window.

**Recommendation:** After SIGCONT, poll `/proc/{pid}/stat` for transition out of stopped state within 5 retries. Set `checkpointStuck = true` immediately on failure.

**Fix applied (§4.4):** A new "SIGCONT confirmation" paragraph specifies that after sending SIGCONT, the adapter polls `/proc/{pid}/stat` (field 3, process state) up to 5 times at 100 ms intervals, waiting for the state to leave `T` (stopped) or `t` (tracing stop). If all 5 retries are exhausted without the process resuming, `checkpointStuck` is set immediately — without waiting for the 60-second watchdog — and `/healthz` returns HTTP 503 to trigger pod restart. The `checkpointStuck` liveness paragraph was updated to document both trigger paths (watchdog timeout and SIGCONT confirmation failure). The note that `shareProcessNamespace: true` is required on the pod spec for `/proc/{pid}/stat` access is included, and a graceful degradation path (skip polling, log warning) is defined for environments where the proc path is unavailable.

### SLC-005 `awaiting_client_action` Children — Pending Results Not Guaranteed After Parent Resumption [High]
**Section:** 7.3
**Status:** Fixed

Child completion events buffered in the in-memory virtual child interface are lost if the coordinating replica crashes.

**Recommendation:** Persist child completion events to `session_tree_archive` rather than holding only in-memory. On parent resumption, replay from archive.

**Fix applied (§7.3):** The "Children behavior" bullet under `awaiting_client_action` semantics now specifies that as each child reaches a terminal state while the parent is in `awaiting_client_action`, the gateway persists the child's full `TaskResult` completion event to `session_tree_archive` (keyed by `root_session_id, node_session_id`) rather than keeping it only in memory. On parent resumption, the gateway replays archived child results from `session_tree_archive` before entering live-wait for still-running children. This eliminates the data-loss window caused by coordinating replica crashes during the `awaiting_client_action` window.

### SLC-006 Session Inbox vs. DLQ Inconsistency — Different Durability for Same Flow [High]
**Section:** 7.2
**Status:** Fixed

Messages buffered in the inbox at the moment of pod failure are lost, while messages arriving moments later (after `resume_pending` transition) are durably stored in the DLQ.

**Recommendation:** Define inbox-to-DLQ migration path: when session transitions to `resume_pending`, drain inbox into DLQ atomically. Back DLQ with Postgres for `awaiting_client_action` sessions.

**Fix applied (§7.2):** A new "Inbox-to-DLQ migration on `resume_pending` transition" section was added immediately after the inbox definition. It specifies a three-step atomic drain: (1) read all inbox messages in FIFO order, (2) write them to the session's Redis DLQ in a single pipeline call with the session's `maxResumeWindowSeconds` TTL, (3) clear the in-memory inbox. Failure to write to Redis is tolerated (pod failure is non-negotiable) but tracked via `lenny_inbox_drain_failure_total`. For `awaiting_client_action` sessions, the Redis DLQ is additionally flushed to a `session_dlq_archive` Postgres table to ensure durability beyond the Redis TTL, with replay from Postgres on parent resumption if the Redis DLQ has expired.

### SLC-007 `maxSessionAge` Timer Behavior During Recovery States Unspecified [High]
**Section:** 6.2
**Status:** Fixed

Timer behavior during `resuming`, `resume_pending`, and `awaiting_client_action` states is not specified.

**Recommendation:** Explicitly specify timer behavior in each state. Paused during all recovery states, resumed only when `running` or `attached`.

**Fix applied (§6.2):** A new "`maxSessionAge` timer behavior across states" section was added to §6.2. It provides a per-state table covering `running`, `attached`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`, and terminal states, with explicit timer semantics (running / paused / stopped) and rationale for each. The spec now states that the timer is paused in all recovery states (`resume_pending`, `resuming`, `awaiting_client_action`) and resumes only when the session enters `running`. It also specifies the persistence mechanism: `accumulated_session_age_seconds` is written to Postgres on every pause/resume transition, and the gateway evaluates `accumulated + elapsed_since_last_resume` against `maxSessionAge` on each transition into `running`.

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

### ~~OBS-001 Multiple Alerts Referenced but Missing from Section 16.5 [High]~~ **FIXED**
**Section:** 4.6.1, 5.3, 13.2, 16.5

**Status:** Fixed in `docs/technical-design.md` Section 16.5.

All 8 missing alerts have been added to the canonical Section 16.5 alert tables. Three alerts were added to the Critical alerts table: `DedicatedDNSUnavailable` (all dedicated CoreDNS replicas unavailable, fires at ready count = 0 for > 30s), `CosignWebhookUnavailable` (cosign ValidatingAdmissionWebhook unreachable, blocking pod admission), and `AuditGrantDrift` (runtime detection of unexpected UPDATE/DELETE grants on audit tables, tracks `audit_grant_drift_total`). Five alerts were added to the Warning alerts table: `WarmPoolIdleCostHigh` (cumulative `lenny_warmpool_idle_pod_minutes` exceeds deployer threshold over 24 h), `SandboxClaimOrphanRateHigh` (> 10 orphaned claims per 15-minute window), `EtcdQuotaNearLimit` (etcd DB size > 80% of `--quota-backend-bytes`), `FinalizerStuck` (`Sandbox` in `Terminating` state > 5 minutes with `lenny.dev/session-cleanup` finalizer), and `DedicatedDNSDegraded` (dedicated CoreDNS below minimum but not zero). The finding referenced `SIEMDeliveryDegraded`; the alert added is `AuditSIEMDeliveryLag` to match the name used in the spec body at Section 13.2 (`siem_delivery_lag_seconds` exceeds `audit.siem.maxDeliveryLagSeconds`). Section 16.5 is now the single source of truth for all alerts.

**Original finding:** At least 8 alerts defined in body sections are absent from the canonical alert table: `WarmPoolIdleCostHigh`, `SandboxClaimOrphanRateHigh`, `EtcdQuotaNearLimit`, `FinalizerStuck`, `CosignWebhookUnavailable`, `DedicatedDNSUnavailable`, `AuditGrantDrift`, `SIEMDeliveryDegraded`.

**Recommendation:** Add all referenced alerts to Section 16.5. Mark it as the single source of truth.

### ~~OBS-002 No SLO Error-Budget Burn-Rate Alerting [High]~~ **FIXED**
**Section:** 16.5

**Status:** Fixed in `docs/technical-design.md` Section 16.5.

A new "SLO error-budget burn-rate alerts" subsection has been added immediately after the SLO targets table in Section 16.5. It defines five burn-rate alert rules covering all key SLOs: `SessionCreationSuccessRateBurnRate`, `SessionAvailabilityBurnRate`, `GatewayAvailabilityBurnRate`, `StartupLatencyBurnRate`, and `TTFTBurnRate`. Each rule applies a dual-window strategy: fast window (1 h at 14× burn rate, fires Critical) for catching acute outages, and slow window (6 h at 3× burn rate, fires Warning) for catching slow-burn degradation. The section includes the burn-rate calculation formula, explains what a 1× burn rate means relative to the 30-day budget window, and documents that both windows must fire simultaneously for a Critical page to reduce false positives. Burn-rate multipliers are configurable via Helm values (`slo.burnRate.fastMultiplier`, `slo.burnRate.slowMultiplier`).

**Original finding:** Seven SLOs defined but only threshold-based alerts — no multi-window burn-rate rules. Operators learn about violations only after the fact.

**Recommendation:** Add burn-rate alerting: fast-window (1h, 14× rate) and slow-window (6h, 3× rate) for key SLOs.

### ~~OBS-003 Delegation Tree Memory Metrics Missing [High]~~ **FIXED**
**Section:** 8.2, 16.1

**Status:** Fixed in `docs/technical-design.md` Section 16.1.

Three delegation tree memory metrics have been added to the canonical metrics table in Section 16.1, immediately after the existing delegation budget metrics: `lenny_delegation_tree_memory_bytes` (Gauge, per active root session, labeled by `root_session_id`, `pool`, `tenant_id` — tracks cumulative in-memory footprint of all live nodes against `maxTreeMemoryBytes`), `lenny_delegation_memory_budget_utilization_ratio` (Gauge, per root session — ratio of current tree memory to `maxTreeMemoryBytes`, approaches 1.0 at rejection threshold), and `lenny_delegation_tree_memory_rejection_total` (Counter, labeled by `pool`, `tenant_id`, `reason: memory_budget_exhausted` — increments on each `delegate_task` rejection due to `maxTreeMemoryBytes` overflow). These metrics make tree memory pressure and memory-exhaustion rejections visible in dashboards and enable alerting before trees are silently dropped.

**Original finding:** No Prometheus metric for tree memory utilization, rejection count, or distribution of footprints. Trees rejected for memory exhaustion are invisible.

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

### CMP-001 SIEM Optional Breaks Compliance-Grade Audit Integrity [Critical] — FIXED
**Section:** 11.7, 16.4
**Status:** Fixed

SIEM connectivity is optional with no enforcement gate. A deployer can run multi-tenant production with no tamper-proof audit trail and no warning. INSERT-only grants are trivially bypassed by superuser or `pg_dump`+restore.

**Recommendation:** Introduce compliance profile enforcement gate. Reject environment creation when `complianceProfile` is FedRAMP/HIPAA/SOC2 and SIEM is not configured.

**Fix applied (2026-04-07):** Section 11.7 now defines a `complianceProfile` field on tenants (`none` | `soc2` | `fedramp` | `hipaa`). A hard enforcement gate blocks tenant creation or update with a regulated profile when `audit.siem.endpoint` is absent (HTTP 422 `COMPLIANCE_SIEM_REQUIRED`). Environment creation within a regulated tenant is also rejected if SIEM is not available. Gateway startup in production mode is a fatal error when any active tenant has a regulated profile and SIEM is unconfigured. Unregulated tenants (`complianceProfile: none`) retain the existing warn-only behaviour. Section 16.4's `AuditSIEMNotConfigured` alert severity is updated to Warning/Critical depending on whether any regulated tenants exist.

### CMP-002 Data Residency Has No Runtime Validation Gate [Critical] — FIXED
**Section:** 12.8, 4.2
**Status:** Fixed

When `dataResidencyRegion` is unset, the platform silently falls back to default single-region. No admission webhook or runtime gate prevents cross-region writes.

**Recommendation:** Define fail-closed behavior for unresolvable regions. Add ValidatingAdmissionWebhook rejecting resources where region is not in `storage.regions`.

**Resolution (Section 12.8):** Two surgical additions were made to the Data residency subsection of Section 12.8:

1. **Fail-closed storage routing:** The storage routing paragraph (item 2 of the enforcement list) now explicitly states that when `dataResidencyRegion` is set but the value is not present in `storage.regions`, the `StorageRouter` fails closed — the write is rejected with `REGION_CONSTRAINT_UNRESOLVABLE` and a `DataResidencyViolationAttempt` audit event is emitted. There is no silent fallback to the default backend.

2. **ValidatingAdmissionWebhook:** A new "Data residency admission control" paragraph was added (between the enforcement list and the audit events paragraph) specifying a `ValidatingAdmissionWebhook` (`lenny-data-residency-validator`) that intercepts `CREATE` and `UPDATE` operations on all CRD resources carrying a `dataResidencyRegion` field. The webhook rejects resources where the region is non-empty and not declared in `storage.regions`. It runs with `failurePolicy: Fail` (fail-closed on webhook outage) and a 5-second timeout. Alert `DataResidencyWebhookUnavailable` fires if the webhook is unreachable for more than 30 seconds.

### CMP-003 Audit Batching Applies Even When SIEM Is Configured [Critical]
**Section:** 11.7, 12.3
**Status: Fixed**

250ms batch window means gateway crash can lose events even with SIEM configured. HIPAA AU-9, FedRAMP AU-10, and SOC2 CC7.2 require completeness.

**Recommendation:** Write audit events synchronously to Postgres first, then forward to SIEM asynchronously via change-data-capture or outbox pattern.

**Resolution:** Applied in Section 12.3 (batching guidance, "When SIEM is configured" paragraph). The previous spec inverted the durability model — it positioned SIEM as the primary durable copy and Postgres as a secondary batch, and asserted that a synchronous SIEM delivery path existed. This was incorrect and created the data-loss window identified in CMP-003. The fix has three parts:

1. **Postgres-first, outbox forwarding to SIEM.** The spec now explicitly states that audit events are always written to Postgres synchronously first, regardless of SIEM configuration. SIEM forwarding happens asynchronously via an outbox/CDC pattern — a dedicated forwarder process tails the committed audit table and delivers events to the SIEM after they are durably in Postgres. A gateway crash before SIEM delivery cannot lose events; the records are already committed in Postgres and will be forwarded on recovery.

2. **SIEM configuration no longer relaxes T2 batching.** The prior text claimed SIEM presence made T2 batch losses acceptable because "SIEM has the complete copy." Under the corrected outbox model, the forwarder reads from Postgres — any T2 event lost from the in-memory buffer before Postgres commit is lost from both Postgres and SIEM. T2 batching therefore remains an explicit opt-in that accepts data loss regardless of SIEM configuration, consistent with HIPAA AU-9, FedRAMP AU-10, and SOC2 CC7.2 completeness requirements.

3. **Outbox forwarder requirements added.** The spec now documents that the forwarder must use logical replication (or sequence-based polling), checkpoint delivery position durably in a `siem_delivery_state` table, and only advance the high-water mark after SIEM acknowledgement. The `AuditSIEMDeliveryLag` alert fires when delivery lag exceeds `audit.siem.maxDeliveryLagSeconds` (default: 30s).

The startup chain-continuity check paragraph was also updated: the prior text suggested replaying missing events from the SIEM, which is impossible under the outbox model (events never committed to Postgres are absent from both stores). The updated text clarifies that chain gaps indicate T2 batch-buffer losses that cannot be replayed.

### CMP-004 Legal Hold Does Not Prevent Checkpoint Rotation [High] — FIXED
**Section:** 12.5, 12.8
**Status:** Fixed in technical-design.md

Intermediate checkpoint state between the two most recent is permanently deleted even under legal hold. Could constitute spoliation.

**Recommendation:** Suspend all retention rotation policies when `legal_hold` is set. Add a reconciler detecting held sessions with rotated checkpoints.

**Fix applied (2026-04-07):**
- §12.8 "Legal hold and checkpoint rotation" paragraph rewritten: when `legal_hold = true`, the GC job suspends both TTL-based deletion and the "latest 2 checkpoints" rotation for that session. Checkpoints accumulate until the hold is lifted, after which normal rotation resumes. Spoliation rationale is explicitly documented.
- §12.8 new "Legal hold reconciler" paragraph added: a background reconciler (co-located with GC, running every 15 minutes) scans for held sessions with checkpoint sequence gaps, emits `legal_hold.checkpoint_gap_detected` critical audit events, and increments `lenny_legal_hold_checkpoint_gaps_total`. Detection-only — no recovery of already-deleted checkpoints.
- §12.5 checkpoint retention bullet updated with an explicit "Exception" callout cross-referencing the legal hold suspension in §12.8.
- Regression check: the GC job's TTL path and the "latest 2" rotation path both now gate on `legal_hold = false` before acting. Existing `legal_hold` flag API (`POST /v1/admin/legal-hold`) and metric (`lenny_checkpoint_storage_bytes_total`) are unchanged.

### CMP-005 Billing Corrections Require No Dual-Control Approval [High] — FIXED
**Section:** 11.2.1
**Status:** Fixed in technical-design.md

A single `platform-admin` can unilaterally mutate billing records with only self-generated audit trail.

**Recommendation:** Require dual-control approval for billing corrections above configurable threshold. Implement four-eyes principle.

**Fix applied (2026-04-07):**
- §11.2.1 Category 2 operator-initiated corrections now require a dual-control (four-eyes) approval workflow. New item 3 added to the controls list describes the full lifecycle: pending → approve/reject/expire, with second-admin identity enforcement (self-approval rejected by gateway).
- `billing.dualControlThreshold` config field introduced (default: `0` = all operator corrections require approval). Positive threshold enables single-control path for corrections at or below the threshold.
- `billing.approvalTimeoutSeconds` (default: 86400) controls pending approval expiry.
- New approval/rejection/expiry audit events (`billing.correction_approval_*`) added; all are non-suppressible.
- `billing.approverNotificationWebhook` optional field for notifying eligible approvers.
- New metric `lenny_billing_correction_pending_total` and `BillingCorrectionApprovalBacklog` alert added.
- Category 1 (gateway-automated corrections) is unaffected — they are not operator-initiated and do not require human approval.
- Regression check: existing `BillingCorrectionRateHigh` alert (§16.5), `correction_reason_code` enum, and append-only billing event semantics are all preserved.

### CMP-006 GDPR Billing Pseudonymization Does Not Constitute Erasure [High] — FIXED
**Section:** 12.8, 11.2.1
**Status:** Fixed in technical-design.md

If the `erasure_salt` is retained in the same database, erasure is not achieved — data remains personal under GDPR Recital 26.

**Recommendation:** Delete the `erasure_salt` immediately after pseudonymization completes. Add verification step confirming derivation fails.

**Fix applied (2026-04-07):**
- §12.8 `erasure_salt` key management section restructured. New "Immediate deletion after pseudonymization" bullet defines the mandatory 5-step sequence:
  1. Pseudonymize all billing events for the target `user_id` in a single DB transaction.
  2. In the same transaction, set `erasure_salt` to `NULL` and delete the KMS-wrapped ciphertext.
  3. Verification step: re-derive a known hash in-memory, query billing table to confirm original `user_id` is gone, attempt DB-side re-derivation (must fail with NULL/decryption error). Outcome recorded in erasure receipt.
  4. On verification failure: mark job `verification_failed`, emit `gdpr.erasure_verification_failed` critical audit event, halt — job is not marked complete until resolved.
  5. In-memory salt copy is zeroed immediately after verification.
- Cross-tenant impact documented: salt is per-tenant, so deletion affects all subsequent erasures in that tenant. Next erasure generates a fresh salt — previously pseudonymized records remain pseudonymized under the destroyed key (effectively anonymous).
- Salt rotation policy updated: old salt is no longer retained in `previous_erasure_salts`; it is deleted immediately on rotation. Re-hash migration required if historical consistency is needed.
- §12.8 GDPR compliance note rewritten to reflect that immediate salt deletion makes the pseudonymized records effectively anonymous under Recital 26 once the verification step passes.
- Regression check: `billingErasurePolicy: exempt` path is unaffected (no pseudonymization, no salt involvement). Append-only billing event semantics unchanged. Erasure receipt already existed — extended to record verification outcome and salt deletion confirmation.

### CMP-007 Startup-Only Audit Grant Verification Insufficient [High] — FIXED
**Section:** 11.7
**Status:** Fixed in technical-design.md

5-minute background check is sufficient window for a superuser to grant, tamper, and revoke without detection.

**Recommendation:** Supplement with pgaudit logging to external append-only sink. Reduce check interval to 60s for regulated profiles.

**Fix applied (2026-04-07):**
- §11.7 integrity control item 1 (startup grant verification) updated with an explicit caveat noting its insufficiency in isolation; cross-reference to items 2 and 5 added.
- §11.7 integrity control item 2 (periodic background check) updated: `audit.grantCheckInterval` is now profile-dependent. Regulated profiles (`soc2`, `fedramp`, `hipaa`) default to **60 seconds** with a hard cap of 120 seconds (gateway clamps and fatal-errors if exceeded). Unregulated profiles default to 5 minutes, configurable up to 15 minutes.
- §11.7 new integrity control item 5 added: **pgaudit logging to external append-only sink**. Regulated deployments must enable `pgaudit` (DDL + ROLE classes) with log records shipped to an external append-only sink. Gateway startup preflight (§17.6) validates pgaudit extension presence and `log_destination` config. Regulated tenant creation/update returns HTTP 422 `COMPLIANCE_PGAUDIT_REQUIRED` if not configured. pgaudit captures every GRANT/REVOKE/DDL by any role (including superusers) to an indelible external record — closing the tamper-and-revert window even if no periodic check cycle fires in the interim.
- New metric `lenny_pgaudit_grant_events_total` and `PgAuditSinkDeliveryFailed` alert added.
- Items 3 (hash chaining) and 4 (SIEM connectivity) are unchanged; item 5 supplements them without replacing them.
- Regression check: existing `audit.hardFailOnDrift`, `audit_grant_drift_total`, compliance profile enforcement gate, and SIEM startup hard-fail behavior all preserved. The new pgaudit requirement is additive — existing non-regulated deployments are unaffected (`audit.pgaudit.enabled` defaults to `false`).

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

### ~~API-001 Undocumented Endpoints Scattered Across Prose~~ **FIXED** [High]
**Section:** 7.1, 7.2, 8.6, 12.8, 14

Several operations (artifact retention extension, webhook events, extension-denial deletion, legal-hold query) are in prose but absent from the REST API table.

**Recommendation:** Audit the spec for every imperative operation. Add every operation to the formal REST API table.

**Fix applied:** Audited all referenced sections and added the following previously undocumented endpoints to the §15.1 REST API table:
- `POST /v1/sessions/{id}/extend-retention` — artifact retention TTL extension (§7.1). Prose at §7.1 updated to reference this endpoint by path instead of describing it as `extend_artifact_retention(session_id, ttl)`.
- `GET /v1/sessions/{id}/webhook-events` — list undelivered webhook events after retry exhaustion (§14 WorkspacePlan `callbackUrl` field).
- `POST /v1/admin/legal-hold` — set or clear a legal hold on a session or artifact (§12.8).
- `POST /v1/admin/tenants/{id}/force-delete` — force-delete a tenant with active legal holds (§12.8).
- `DELETE /v1/admin/trees/{treeId}/subtrees/{sessionId}/extension-denial` — clear extension-denied flag (§8.6). Prose at §8.6 also corrected: the path prefix was wrong (`/admin/v1/` → `/v1/admin/`).

### ~~API-002 Experiment Endpoint Method and Path Inconsistency~~ **FIXED** [High]
**Section:** 10.7, 15.1

Section 10.7 describes `PATCH /v1/experiments/{id}` while Section 15.1 shows `PUT /v1/admin/experiments/{name}`.

**Recommendation:** Resolve to a single canonical endpoint. Use `PATCH /v1/admin/experiments/{id}`.

**Fix applied:** Two changes made:
1. §10.7 prose updated: `PATCH /v1/experiments/{id}` → `PATCH /v1/admin/experiments/{id}` (corrected both the HTTP method verb and the path to include the `/admin/` segment and use `{id}` as the identifier).
2. §15.1 REST API table: added `PATCH /v1/admin/experiments/{id}` as a new row (canonical endpoint for status transitions using JSON Merge Patch, requires `If-Match`). The existing `PUT /v1/admin/experiments/{name}` row is retained for full-body updates with ETag concurrency control.

### ~~API-003 Error Code `SCOPE_DENIED` Not in Catalog~~ **FIXED** [High]
**Section:** 7.2, 15.1

Used in webhook delivery receipt but absent from the error code catalog.

**Recommendation:** Add `SCOPE_DENIED` to the error code catalog with category `POLICY`.

**Fix applied:** Added `SCOPE_DENIED` to the error code catalog in §15.1. Entry: category `POLICY`, HTTP 403, description: "Inter-session message rejected because the sender's delegation scope does not permit messaging the target session. Returned as the `error` reason in a `delivery_receipt` event. See Section 7.2." Placed after `INJECTION_REJECTED` in the catalog table, consistent with grouping of POLICY-category codes.

### ~~API-004 Pagination Missing `total` Count~~ **FIXED** [High]
**Section:** 15.1

Cursor-based pagination has no `total` field. UIs cannot render progress or "X results found."

**Recommendation:** Add optional `total` field to pagination envelope, present when cheaply computable.

**Fix applied:** Added `total` (integer, optional) to the pagination response envelope in §15.1. The JSON example now includes `"total": 1247`. The prose description clarifies: present only when cheaply computable (cached count or inexpensive `COUNT(*)`); omitted when a full table scan would be required. UIs may use it for "X results found" display or pagination progress but must not rely on its presence.

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

### CPS-001 No Differentiation Narrative [Critical] — FIXED
**Section:** 1, 2
**Status:** Fixed

Section 23.1 ("Why Lenny?") already contained a comprehensive differentiation narrative covering Temporal, Modal, LangGraph, E2B, Fly.io Sprites, and Daytona — including target personas (Section 23.2) and explicit architectural trade-offs. The gap was that Sections 1 and 2 had no forward reference to it, leaving casual readers without a pointer to the competitive context. Fixed by adding a forward reference in Section 1 (Executive Summary), immediately after the problem-statement paragraph, directing readers to Section 23 / Section 23.1 for the differentiation narrative.

### CPS-002 No Open Source Community Strategy [High] — FIXED
**Section:** 1, 2, 15
**Status:** Fixed

Excellent extensibility primitives but no governance model, contributor ladder, release cadence, or community channels documented.

**Recommendation:** Produce GOVERNANCE.md and CONTRIBUTING.md as v1 launch artifacts.

Section 23.2 already contained a governance model (BDfN → steering committee, CONTRIBUTING.md, communication channels, TTHW target), but Sections 1 and 2 had no forward reference to it, and GOVERNANCE.md was not explicitly listed as a v1 launch deliverable. Fixed by two changes: (1) A forward-reference paragraph was added at the end of Section 2 (Non-Goals), directing readers to Section 23.2 and explicitly stating that GOVERNANCE.md and CONTRIBUTING.md are v1 launch deliverables published in Phase 2. (2) A new "Governance artifact" bullet was added in Section 23.2's governance model block, explicitly naming GOVERNANCE.md as a Phase 2 v1 launch deliverable alongside CONTRIBUTING.md and describing its content (BDfN → steering committee transition criteria, decision-making process, ADR requirement thresholds, license/CLA policy).

### CPS-003 Upstream Dependency Risk Incompletely Assessed [High] — ALREADY FIXED
**Section:** 4.6.1
**Status:** Already Fixed (by K8S-004)

No governance health criteria, upstream contribution strategy, or SIG sponsorship assessment for `kubernetes-sigs/agent-sandbox`.

**Recommendation:** Add dependency risk section with health criteria, contribution commitment, and SIG identification.

K8S-004 (see resolution above) already addressed this finding in full. Section 4.6.1 now contains: (1) a "Dependency pinning and upgrade policy" block with augmented upgrade rules including a CI gate, breaking-change hold, and API stability monitoring subscription; (2) a "Go/no-go criteria for the agent-sandbox dependency" block with three explicit, measurable health criteria — API stability (no structural breaking change in two most recent releases), community support SLO (one release per 6 months, critical issues acknowledged within 30 days, no unaddressed blocker issues older than 60 days), and integration test pass rate (100%) — evaluated at Phase 1 exit; and (3) an updated fallback plan with an explicit trigger condition. No additional changes to the spec are required for CPS-003.

### CPS-004 No Comparison to Adjacent Orchestration Systems [High] — ALREADY FIXED
**Section:** 1, 2
**Status:** Already Fixed (by CPS-001 + pre-existing Section 23)

No mention of Temporal, Modal, LangGraph, Ray, or Dagger. No comparative analysis or positioning.

**Recommendation:** Add "Relationship to Adjacent Systems" subsection positioning Lenny relative to at minimum Temporal, Modal, and LangGraph.

Section 23 (Competitive Landscape) already contained a comparison table covering Temporal, Modal, LangGraph, E2B, Fly.io Sprites, Daytona, and others, with explicit architectural positioning for each. Section 23.1 ("Why Lenny?") provides a full differentiator narrative. CPS-001 (Fixed) added a forward reference in Section 1 (Executive Summary) directing readers to Section 23 and Section 23.1. The CPS-002 fix additionally added a forward reference in Section 2. With both forward references in place and Section 23/23.1 containing the full comparative analysis, no additional spec changes are required for CPS-004.

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

### ~~WPL-001 SDK-Warm Pod Eviction During `sdk_connecting` Not Handled~~ **FIXED** [Critical]
**Section:** 6.1, 6.2, 4.6.1
**Status:** Fixed

A pod in `sdk_connecting` state that is evicted has no specified cleanup behavior for the running SDK process.

**Recommendation:** Specify adapter behavior on SIGTERM during `sdk_connecting`: call `DemoteSDK` with bounded timeout, then terminate. Add state transition to diagram.

**Fix applied:** (1) Added `sdk_connecting → terminated` state transition to the Section 6.2 pod state machine diagram, annotated with the SIGTERM-triggered sequence. (2) Added a dedicated "Adapter SIGTERM behavior during `sdk_connecting`" paragraph to Section 6.1 specifying: call `DemoteSDK` with a bounded timeout (default 5s, configurable via `LENNY_DEMOTE_TIMEOUT_SECONDS`), force-terminate the SDK process on timeout, then exit; the pod transitions to `terminated` (not `failed`); `terminationGracePeriodSeconds` must exceed `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s`. Prevents credential leaks and abandoned LLM provider connections.

### WPL-002 Burst Formula Dimensionality Mismatch [High]
**Section:** 4.6.2, 5.2, 17.8.2

Section 4.6.2's formula produces `claims/second` (not pod count) for the first term. Section 17.8.2's formula is correct but inconsistent.

**Recommendation:** Reconcile formulas. Add `× (failover_seconds + pod_startup_seconds)` to the first term in Section 4.6.2.

**Fix applied:** Added `× (failover_seconds + pod_startup_seconds)` to the first term in the Section 4.6.2 default formula, correcting the dimensionality from `claims/second` to `pods` (pod count). Updated the accompanying explanation to define `failover_seconds` (default: 25s = `leaseDuration + renewDeadline`) and `pod_startup_seconds`, and clarify that the first term represents claims arriving during the window when the pool cannot create new ready pods. Applied the same correction to (1) the mode-factor variant formula in Section 5.2, with a note explaining the `(failover_seconds + pod_startup_seconds)` factor and its consistency with the base formula; and (2) the `paused → active` row in the Section 10.7 experiment transitions table, which reproduced the old (incorrect) formula inline. Section 17.8.2 already had the correct dimensionality and required no changes.

### WPL-003 `sdkWarmBlockingPaths` Glob Semantics Unspecified [High]
**Section:** 6.1, 5.1

No specification of: path vs filename matching, case sensitivity, glob dialect, symlink resolution, or whether `workspaceDefaults` files count.

**Recommendation:** Specify matching contract precisely: relative path, case-sensitive, Go `path.Match` with `**`, no symlink resolution.

**Fix applied:** Added a dedicated "**`sdkWarmBlockingPaths` matching contract**" paragraph to Section 6.1 immediately after the "Demotion on demand" paragraph. The contract specifies: (1) patterns are matched against the **relative path** of each file within the workspace root; (2) matching is **case-sensitive** on all platforms; (3) the glob dialect is Go's `path.Match` extended with `**` support (`**` matches zero or more path segments; `*` matches within a single segment only; `?` matches one non-separator character; `[...]` matches character classes); (4) patterns are not implicitly root-anchored — `CLAUDE.md` matches only the top-level file, not `subdir/CLAUDE.md`; (5) symlinks are **not resolved** — only the literal path is checked; (6) `workspaceDefaults` files from the Runtime definition are included in the check alongside client-uploaded files. Also added `preConnect` and `sdkWarmBlockingPaths` fields to the Section 5.1 Runtime capabilities YAML example, with prose definitions for both fields and a cross-reference to the matching contract in Section 6.1.

### WPL-004 `sdkWarmBlockingPaths` Demotion Negates SDK-Warm Benefit for Common Workloads [High]
**Section:** 6.1, 6.3

Default blocking paths (`CLAUDE.md`, `.claude/*`) will match virtually every real Claude Code project, triggering demotion on the majority of sessions.

**Recommendation:** Add `demotionRateThreshold` guidance. Consider a circuit-breaker that disables SDK-warm when demotion rate exceeds threshold.

**Fix applied:** Added a "**Demotion rate threshold and circuit-breaker**" paragraph to Section 6.1 after the "Demotion support is mandatory" paragraph. The addition covers: (1) `demotionRateThreshold` operator guidance with three demotion-rate bands (< 20%, 20–60%, > 60%) and recommended actions for each; (2) a `SDKWarmDemotionRateHigh` warning event emitted by PoolScalingController when the rolling 1-hour rate exceeds 60%, with the option to suppress via `sdkWarm.acknowledgeHighDemotionRate: true`; (3) an automatic circuit-breaker that triggers at 90% rolling 5-minute demotion rate — the controller sets `status.sdkWarmDisabled: true` on the `SandboxWarmPool` CRD, causing the WarmPoolController to stop SDK-warm transitions and emit a `pool.sdk_warm_circuit_breaker_open` audit event; (4) explicit re-enable path via `PUT /v1/admin/pools/{name}` with `circuitBreakerOverride`. Also added a "SDK-warm savings depend on demotion rate" callout to Section 6.3 pointing operators to the Section 6.1 guidance and the relevant metrics.

### WPL-005 Variant Pool Formula Doesn't Reduce Base Pool [High]
**Section:** 4.6.2, 10.7

When a variant pool is created, the base pool's `minWarm` is not reduced to reflect diverted traffic. Total warm pods = `(1 + variant_weight) × original`.

**Recommendation:** Specify that PoolScalingController recomputes base pool's `minWarm` as `base_demand_p95 × (1 - Σ variant_weights) × safety_factor × time_window`.

**Fix applied:** Added a "**Variant pool sizing and base pool adjustment**" subsection to Section 4.6.2 specifying: (1) the base pool adjusted formula: `ceil(base_demand_p95 × (1 - Σ variant_weights) × safety_factor × (failover_seconds + pod_startup_seconds) + burst_p99_claims × (1 - Σ variant_weights) × pod_warmup_seconds)`, with `Σ variant_weights` being the sum of all active variant weights; (2) the adjustment is recalculated on every experiment activation, weight change, pause, or conclusion; (3) `Σ variant_weights` is clamped to `[0, 1)` — if it reaches or exceeds 1, the experiment configuration is rejected at admission with `INVALID_VARIANT_WEIGHTS`; (4) the base and variant pool CRD updates are applied in the same reconciliation cycle to avoid transient over-provisioning. Updated Section 10.7 summary sentence to reference the Section 4.6.2 recomputation formula. Updated all three rows of the `PoolScalingController behavior on experiment status transitions` table with "**Base pool adjustment:**" notes specifying that base pool `minWarm` is recomputed (with the corresponding `Σ variant_weights` change) atomically with each variant pool transition.

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

### ~~CRD-001 Lease TTL Undefined for `anthropic_direct` Direct Mode~~ **FIXED** [High]
**Section:** 4.9
**Status:** Fixed

~~No explicit default lease TTL for provider types. `anthropic_direct` in direct mode has no provider-side expiry.~~

**Fix applied:** The `CredentialPool` schema now includes an optional `leaseTTLSeconds` field (pool-level override). A per-provider default TTL table was added to Section 4.9 immediately after the pool configuration YAML example. `anthropic_direct` defaults to **3600 seconds** (1 hour); the synthetic enforcement mechanism is explained (the API key itself does not expire; Lenny enforces the TTL synthetically by refusing to honor leases past `expiresAt`). The table covers all six built-in providers (`anthropic_direct`, `aws_bedrock`, `vertex_ai`, `azure_openai`, `github`, `vault_transit`) and custom providers. A `renewBeforeBufferSeconds` field (default: 300s) is also introduced on `CredentialPool` to configure how far before `expiresAt` the `renewBefore` timestamp is set. Provider ceilings (`providerMaxTTL`) are documented for each provider type; `leaseTTLSeconds` is silently capped at the provider ceiling.

~~**Recommendation:** Define explicit default lease TTLs per `CredentialProvider` type. Add configurable `leaseTTLSeconds` on `CredentialPool`.~~

### ~~CRD-002 `renewBefore` Has No Corresponding Renewal Mechanism~~ **FIXED** [High]
**Section:** 4.9
**Status:** Fixed

~~No background process monitors active leases against `renewBefore` for proactive renewal. Expiry-driven rotation consumes `maxRotationsPerSession` budget.~~

**Fix applied:** A new "Proactive Lease Renewal" subsection was added to Section 4.9 immediately before "Rotation mode resolution". It defines the `CredentialRenewalWorker` — a gateway background goroutine that maintains a min-heap of active leases ordered by `renewBefore`. The worker wakes at each lease's `renewBefore` time, issues a replacement lease (same pool/credential), and pushes it to the runtime via the standard `RotateCredentials` RPC. Critically, proactive renewals are identified by `rotationTrigger: proactive_renewal` and are **explicitly excluded** from the `maxRotationsPerSession` counter — only fault-driven rotations (triggered by `RATE_LIMITED`, `AUTH_EXPIRED`, or `PROVIDER_UNAVAILABLE`) increment the counter. Renewal failure handling is specified: retry at half the remaining TTL interval (up to 3 times); if all retries fail, fall through to the standard Fallback Flow at `expiresAt` (consuming a fault rotation slot only at that last-resort attempt). Metric `lenny_gateway_credential_proactive_renewals_total` added. Audit event `credential.renewed` with `rotationTrigger: proactive_renewal` specified.

~~**Recommendation:** Define a proactive renewal loop. Renewals triggered by `renewBefore` should NOT consume the rotation counter.~~

### CRD-003 Shared `maxRotationsPerSession` Conflates Fault and Proactive Rotations [Medium]
**Section:** 4.9

A 2-hour STS session uses 1 of 3 rotation slots for routine renewal, leaving only 2 for faults.

**Recommendation:** Split into `faultRotationCount` and `proactiveRenewalCount`. Apply `maxRotationsPerSession` only to faults.

### ~~CRD-004 LLM Reverse Proxy Lease Token Not Bound to Pod Identity~~ **ALREADY FIXED (by SEC-002)** [High]
**Section:** 4.9
**Status:** Already Fixed

~~A compromised agent reading its `credentials.json` can replay the lease token through any gateway replica.~~

**Resolution (Already Fixed — same fix as SEC-002):** Section 4.9 already contains a full SPIFFE-binding specification under the callout "SPIFFE-binding for proxy mode lease tokens (v1 requirement for multi-tenant deployments)". The fix was introduced as part of the SEC-002 resolution and covers all three required elements: (1) recording the pod's SPIFFE URI (`spiffe://lenny/agent/{pool}/{pod-name}`) in the `TokenStore` at `AssignCredentials` time; (2) verifying the peer SPIFFE URI on every LLM proxy request and rejecting mismatches with `LEASE_SPIFFE_MISMATCH` + audit event `credential.lease_spiffe_mismatch`; (3) making SPIFFE-binding the default for all proxy-mode pools, with explicit opt-out (`credentialPool.spiffeBinding: disabled`) restricted to single-tenant and development deployments. No additional spec change is required for CRD-004.

~~**Recommendation:** Bind lease tokens to pod's SPIFFE URI at `AssignCredentials` time. Validate on every proxy request.~~

### CRD-005 Credential Pool Exhaustion Has No Queuing [Medium]
**Section:** 4.9

Immediate `CREDENTIAL_POOL_EXHAUSTED` with no wait mechanism. Pre-claim check and assignment are not atomic, creating amplified retry cycles.

**Recommendation:** Add brief credential availability queue (2-5s configurable) when pool has sessions approaching completion.

### ~~CRD-006 Direct-Mode + `standard` (runc) Only Warns, Not Blocked~~ **FIXED** [High]
**Section:** 4.9, 5.3
**Status:** Fixed

~~Container escape gives attacker access to `credentials.json` on the host node. Only a warning event, not a hard rejection.~~

**Fix applied:** The "Warning" callout in Section 4.9 (LLM Reverse Proxy subsection) was replaced with a "Hard rejection" callout that specifies two-layer admission enforcement when `tenancy.mode: multi`: (1) the warm pool controller rejects any pool creation/update combining `deliveryMode: direct` + `isolationProfile: standard` with error `DirectModeStandardIsolationMultiTenantRejected`; (2) a new `ValidatingAdmissionWebhook` (`lenny-direct-mode-isolation`) deployed with `failurePolicy: Fail` rejects `SandboxTemplate` and `CredentialPool` resources carrying this combination in agent namespaces. In single-tenant or dev mode the combination is permitted only with an explicit opt-in field (`allowDirectModeStandardIsolation: true`) on the pool, which emits the `DirectModeWeakIsolation` warning event. The opt-in field is rejected by the admission webhook in multi-tenant mode regardless of its value, eliminating any bypass path.

~~**Recommendation:** Make this a hard admission rejection in multi-tenant mode. Require explicit opt-in field.~~

### ~~CRD-007 User-Scoped Credential Rotation and Revocation Not Described~~ **FIXED** [High]
**Section:** 4.9
**Status:** Fixed

~~No endpoint for rotating a user-scoped credential or revoking active leases backed by it.~~

**Fix applied:** Two new endpoints were added to the "User credential management endpoints" table in Section 4.9 (Pre-Authorized Credential Flow subsection):

1. **`PUT /v1/credentials/{credential_ref}`** — Rotates (replaces) the secret material for an existing user-scoped credential. The Token Service atomically replaces the encrypted material. Active leases backed by this credential are immediately rotated via `RotateCredentials` RPC (same mechanism as pool emergency revocation). Returns `credential_ref`, `provider`, `label`, `updated_at`. Emits `credential.rotated` audit event. Subject to the same logging exclusion requirements as `POST /v1/credentials`.

2. **`POST /v1/credentials/{credential_ref}/revoke`** — Revokes a user-scoped credential and immediately invalidates all active leases backed by it. Uses the same propagation path as pool credential revocation: deny-list for proxy-mode leases, `RotateCredentials` RPC + fallback flow for direct-mode leases. Marks the credential `revoked` in `TokenStore` (preserving the audit record, unlike `DELETE`). Returns a summary of terminated leases. Emits `credential.user_revoked` audit event. The distinction between `revoke` (keeps record, triggers immediate lease invalidation) and `DELETE` (removes record, existing leases run to TTL) is documented.

The "Security considerations" paragraph was extended to cover `PUT /v1/credentials/{credential_ref}` in the logging exclusion requirement. The "Resolution at session creation" paragraph was updated to specify that `revoked` user credentials are treated as not-found (falling through to pool per fallback configuration).

~~**Recommendation:** Add `POST /v1/credentials/{ref}/revoke` (user-facing) and `PUT /v1/credentials/{ref}` for rotation.~~

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

### SCH-001 OutputPart Type Registry Has No Formal Schema [Critical] ✅ Fixed
**Section:** 15.4.1
**Status:** Fixed

No formal registry document, no namespace convention for third-party types, no mapping from `(type, schemaVersion)` to concrete schema. Evolution is indistinguishable from envelope version bumps.

**Recommendation:** Define a formal type registry as a versioned document. Introduce `x-vendor/typeName` namespace convention for custom types.

**Fix applied (2026-04-07):** Section 15.4.1 (Internal `OutputPart` Format, Canonical Type Registry v1) updated with:
1. **Namespace convention** — third-party types MUST use `x-<vendor>/<typeName>` prefix (e.g., `x-acme/heatmap`). Unprefixed names are reserved for platform types. Gateway enforces this at ingress with `UNREGISTERED_PART_TYPE` error.
2. **Per-type `schemaVersion` contract** — new table maps each built-in type to its v1 guaranteed field set, with notes on what future versions may add. Producers MUST set `schemaVersion` to the version required by any fields they emit beyond the v1 stable set.

### SCH-002 RuntimeDefinition Inheritance Rules Exist Only in Prose [Critical]
**Section:** 5.1
**Status:** Fixed

Merge semantics for `derived` runtimes exist entirely in prose with no formal algorithm. Conflicting fields (resources, network policy, capabilities intersection) have no worked examples.

**Recommendation:** Provide a normative merge algorithm table. Add at least two worked examples covering conflicts.

**Fix applied (2026-04-07):** Section 5.1 (Inheritance Rules) updated with:
1. **Normative merge algorithm table** — lists every `RuntimeDefinition` field with its merge behavior: `Prohibited`, `Inherited`, `Override`, `Maximum`, `Append`, or `Merge`. Each behavior is formally defined below the table.
2. **Worked Example A** — `setupPolicy.timeoutSeconds` Maximum rule: shows that `max(300, 120) = 300`, preventing derived runtimes from underming base-imposed safety margins.
3. **Worked Example B** — `labels` Merge rule and `capabilities` Prohibited rule: shows label key collision resolution and the gateway's `INVALID_DERIVED_RUNTIME` rejection for any `capabilities` field on a derived runtime.

### SCH-003 CredentialLease `materializedConfig` Is Unschematized [Critical]
**Section:** 4.9

Provider-specific field with no schema registry, no validation contract, no documented encoding for sensitive values.

**Recommendation:** Define a schema per built-in credential provider using a discriminated union pattern keyed on `provider` type.

**Fix applied (2026-04-07):** Section 4.9 (Credential Lease) updated with a new `materializedConfig` Schema by Provider subsection immediately following the `CredentialLease` example. The fix includes:
1. **Encoding conventions** — plaintext delivery over mTLS to tmpfs, ISO 8601 UTC for expiry timestamps, optional fields omitted rather than nulled.
2. **Proxy vs. direct mode differentiation** — explicit statement that secret fields (`apiKey`, `accessKeyId`, etc.) are omitted in proxy mode and replaced by `proxyUrl` + `leaseToken`.
3. **Per-provider field tables** covering all five built-in providers: `anthropic_direct`, `aws_bedrock`, `vertex_ai`, `github`, and `vault_transit`. Each table entry specifies field name, type, required/optional (distinguished by direct vs. proxy mode), and encoding notes.
4. **Validation contract** — Token Service validates required fields before lease issuance; failures surface as `CREDENTIAL_MATERIALIZATION_ERROR` → `CREDENTIAL_POOL_EXHAUSTED`. Custom providers bypass built-in validation.
5. **Runtime responsibility** — runtimes must treat fields with sensitive name patterns as secret and must not include them in logs, traces, or agent output.

### SCH-004 MessageEnvelope `delivery` Field Underspecified for Multi-Turn [High] — FIXED
**Section:** 15.4.1
**Status:** Fixed

Acknowledgement schema, `threadId`/`inReplyTo` DAG model, ordering guarantees, and delegation forwarding semantics all undefined.

**Recommendation:** Define acknowledgement schema, DAG model, and ordering guarantees. Add `delegationDepth` field.

**Fix applied (2026-04-07):** Replaced the single-line `delivery` field description in the `MessageEnvelope` section (§15.4.1) with a complete specification:
- `delivery` is now a closed enum (`"immediate"` / `"queued"` / absent) with a table documenting gateway behaviour for each value and the rejection error for unknown values.
- A `delivery_receipt` acknowledgement schema is defined (JSON object with `messageId`, `status`, `reason`, `deliveredAt`, `queueDepth`) with all five `status` values documented.
- `id` field now specifies gateway-assigned ULID format and the `DUPLICATE_MESSAGE_ID` rejection rule.
- `inReplyTo` and `threadId` are now covered by a DAG conversation model subsection: explains the `session_messages` Postgres table, coordinator-local FIFO ordering guarantee, cross-sender ordering limitations, and the `delegationDepth` gateway-injected field with its informational semantics.

### SCH-005 OutputPart Inline/Ref Duality Has No Resolution Protocol [High] — FIXED
**Section:** 15.4.1
**Status:** Fixed

No size threshold, ref URI scheme, TTL policy, or consumer fallback behavior defined.

**Recommendation:** Define `LennyBlobURI` scheme. Document thresholds and TTL. Require adapters to dereference refs before producing external messages.

**Fix applied (2026-04-07):** Added a comprehensive resolution protocol to the `inline` vs `ref` property note in §15.4.1 (`OutputPart` format):
- Size thresholds defined: ≤ 64 KB → inline; > 64 KB and ≤ 50 MB → staged to blob store with `lenny-blob://` ref; > 50 MB → rejected with `413 OUTPUTPART_TOO_LARGE`.
- `LennyBlobURI` scheme fully specified: `lenny-blob://{tenant_id}/{session_id}/{part_id}?ttl={seconds}&enc=aes256gcm` with component descriptions.
- TTL policy table covering four contexts: live streaming (1 h default), TaskRecord (30 d default), audit/billing (13 months), delegation export (child session duration + 1 h).
- Consumer fallback obligation: surface `blob_ref_unresolvable` degradation annotation, substitute error part, never silently drop.
- Adapter dereference obligation: MCP/OpenAI/A2A adapters must dereference before serializing; REST passes `ref` through for direct client dereference.

### SCH-006 WorkspacePlan `runtimeOptions` Has No Schema [High] — FIXED
**Section:** 14
**Status:** Fixed

Free-form `map[string]any` with no per-runtime documentation. Env blocklist has no wildcard support.

**Recommendation:** Define `runtimeOptions` as per-runtime discriminated union. Add glob pattern support to env blocklist.

**Fix applied (2026-04-07):** Two changes in §14:
1. **`env` blocklist:** Replaced the vague parenthetical with an explicit glob pattern specification. The blocklist now supports exact names and `*`-wildcard glob patterns. The gateway rejects matching vars with `400 ENV_VAR_BLOCKLISTED` identifying the offending key and matching pattern. Multi-tenant mode prevents reducing the default blocklist.
2. **`runtimeOptions`:** The field is now described as a per-runtime discriminated union. Three built-in runtime schemas are fully documented: `claude-code` (model, settingSources, streamingMode, maxTokens, temperature, thinkingBudget), `langgraph` (graphModule, checkpointBackend, recursionLimit, configSchema), and `openai-agents` (model, temperature, parallelToolCalls, responseFormat) — all as JSON Schema objects with additionalProperties: false. Derived runtime schema inheritance and narrowing rules documented.

### SCH-007 Schema Versioning Conflates Live and Durable Consumers [High] — FIXED
**Section:** 15.5
**Status:** Fixed

Rejection-at-read rule stated uniformly. Durable consumers rejecting unknown versions creates compliance gaps.

**Recommendation:** Bifurcate: live consumers MAY reject; durable consumers MUST forward-read. Define migration window SLA.

**Fix applied (2026-04-07):** Item 7 of §15.5 was rewritten to bifurcate the consumer rules:
- **Live consumers** (streaming, in-memory): MAY reject an unrecognized `schemaVersion`; SHOULD forward-read with a `schema_version_ahead` degradation annotation.
- **Durable consumers** (billing, audit, analytics): MUST forward-read; MUST preserve unknown fields verbatim; MUST NOT silently discard records based on unrecognized version; if they cannot pass through unknown fields, they MUST emit a `durable_schema_version_ahead` alert and queue for manual review.
- **Migration window SLA:** Durable consumers must be upgraded within 90 days of a new `schemaVersion` release. After 90 days the old write path may be retired, but persisted records remain readable for their full retention period.
- The `OutputPart` nested-in-`TaskRecord` rule updated to reference durable consumer forward-read obligation rather than the previous uniform rejection rule.

### SCH-008 Translation Fidelity Matrix Has No Lossiness Documentation [High] — FIXED
**Section:** 15.4.1
**Status:** Fixed

No documentation of which source fields have no target equivalent, which round-trips are asymmetric, or how `thinking` parts translate to OpenAI/A2A.

**Recommendation:** Annotate each cell with `exact`, `lossy`, `unsupported`, or `extended` tags. Make lossiness explicit.

**Fix applied (2026-04-07):** The Translation Fidelity Matrix in §15.4.1 was restructured:
- A fidelity tag legend table defines four tags: `[exact]`, `[lossy]`, `[dropped]`, `[extended]` (note: `[unsupported]` added as a future-use tag; no current fields require it, so `[dropped]` is used for intentionally non-transmitted fields).
- Every matrix cell now leads with its fidelity tag, followed by the existing descriptive text.
- `reasoning_trace` / `thinking` translation lossiness explicitly annotated for MCP (`[lossy]` — collapsed to `TextContent` with `thinking` annotation) and OpenAI (`[lossy]` — collapsed to `text`, semantic lost, indistinguishable from regular text on round-trip).
- `ref` (`lenny-blob://` URI) lossiness for OpenAI adapter (`[dropped]` — inlined; scheme permanently lost) and A2A (`[lossy]` — scheme rewritten to HTTPS URL) now explicit.
- A new **Round-trip asymmetry summary** table identifies the five fields with asymmetric round-trips (schemaVersion, id, type/reasoning_trace, ref, annotations) with adapter scope, asymmetry description, and caller impact.

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

### BLD-001 Authentication Comes After Real LLM Testing [Critical] — FIXED
**Section:** 18
**Status:** Fixed

Phase 5.5 introduces real LLM credentials before Phase 7's policy engine. Real credentials injected into pods without production-grade admission, budget, and auth enforcement.

**Recommendation:** Gate real-LLM testing on auth-complete milestone. Move minimum viable policy enforcement to Phase 5.75.

**Fix applied (2026-04-07):** Section 18 updated with three changes:
1. The note following Phase 5 now explicitly states that real LLM provider testing is **gated on Phase 5.75** — minimum viable policy enforcement (JWT auth, per-tenant quota enforcement, and basic budget checks) must be operational before any real-credential session proceeds to Phase 6.
2. A new **Phase 5.75** row has been inserted between Phase 5.5 and Phase 6. It requires wiring `AuthEvaluator` (JWT validation + `tenant_id` extraction, backed by Phase 4.5 auth infrastructure) and `QuotaEvaluator` (per-tenant concurrency limits and basic token budget checks) into the gateway interceptor chain as a hard prerequisite for real-credential integration testing. A separate "Phase 5.75 policy gate" note marks this as a hard prerequisite for Phase 6.
3. The Phase 6–8 CI test coverage note now references Phase 5.75 (not Phase 5.5) as the enabler of real LLM provider testing.

These changes ensure that real credentials are always covered by authenticated session creation and basic quota enforcement before any interactive (Phase 6+) testing begins, closing the window where credentials were present but policy enforcement was absent.

### BLD-002 Security Audit Scheduled Too Late [Critical] — FIXED
**Section:** 18
**Status:** Fixed

Phase 14 places security audit after the full platform is built. Cost of architectural findings is maximally high.

**Recommendation:** Insert targeted security design reviews after Phase 5.5 (credential injection) and Phase 9 (delegation attack surface).

**Fix applied:** Two targeted security design review checkpoints inserted into Section 18:
- **Phase 5.6** (after Phase 5.5): focused design review of the credential injection surface — credential scope leakage, Token Service RPC authentication, K8s Secrets access controls, and lease expiry enforcement. Blocking gate before Phase 6.
- **Phase 9.1** (after Phase 9): focused design review of the delegation attack surface — cross-tenant delegation, parent-forging, delegation loop/depth exhaustion, and `lenny/discover_agents` scope leakage. Blocking gate before Phase 10.
- **Phase 14** updated to clarify it is the comprehensive audit and pentest covering the full platform, with a note that targeted design reviews at Phases 5.6 and 9.1 have already addressed the highest-risk surfaces.

### BLD-003 Echo Runtime Insufficient for Phase 6-8 Validation [Critical] — FIXED
**Section:** 18
**Status:** Fixed

Echo runtime cannot produce streaming output, report token usage, or implement Full-tier lifecycle. Phases 6-8 cannot be milestone-validated in CI.

**Recommendation:** Promote "extended test runtime" to explicit Phase 2.8 deliverable implementing streaming, `ReportUsage`, and lifecycle channel.

**Fix applied:** Phase 2.8 added as an explicit deliverable in Section 18 with a new `streaming-echo` built-in test runtime:
- **Phase 2.8** introduces the `streaming-echo` runtime that extends the echo runtime with: (1) simulated streaming `OutputPart` chunk sequences, (2) `ReportUsage` with deterministic token counts for quota enforcement testing, and (3) Full-tier lifecycle channel support (`checkpoint_request`/`checkpoint_ready` with configurable delay). Ships as a built-in fixture alongside the echo runtime.
- **Milestone gate**: Phase 6, 7, and 8 CI pipelines are explicitly required to validate milestones using `streaming-echo`-based test cases.
- **Phase 6–8 CI note** updated to reference Phase 2.8 as the specified solution rather than describing it as an open implementation detail.

### BLD-004 License Unresolved Before Community Engagement [High]
**Section:** 18, 23.2
**Status: Fixed**

Phase 2 promises `CONTRIBUTING.md` but license is unresolved. No assigned phase or ADR for license selection.

**Recommendation:** Assign license selection as Phase 1 gating item with ADR.

**Resolution:** Three changes were made. (1) A new **Phase 0** row was added to the build sequence table as a pre-implementation gating phase. It includes two mandatory deliverables before Phase 1 begins: ADR-007 (`SandboxClaim` optimistic-locking verification) and ADR-008 (open-source license selection). The Phase 0 description specifies that the license must be committed to the repository root before any contributor engagement, `CONTRIBUTING.md` publication, or external PR acceptance. (2) Phase 1 was updated to list both ADR-007 and ADR-008 as explicit prerequisites. (3) Section 23.2's license paragraph was rewritten to eliminate the "open question" framing: it now states that license selection is a Phase 0 gating item (ADR-008), lists evaluation criteria and candidate licenses (MIT, Apache 2.0, AGPL + commercial exception, BSL), and cross-references ADR-008 in `docs/adr/`. The §18 open-source readiness note was also updated to reference Phase 0 and ADR-008.

### BLD-005 SandboxClaim ADR Is Phase 1 Blocker With No Owner [High]
**Section:** 18, 4.6.1
**Status: Already Fixed**

ADR-TBD is marked as Phase 1 blocking prerequisite but has no timeline or deliverable.

**Recommendation:** Make it an explicit Phase 0 deliverable with a running integration test.

**Resolution:** A prior iteration (K8S-001 fix) already resolved this finding. The spec text at Section 4.6.1 (lines 383–389) and the Phase 1 row in Section 18 already reference **ADR-007** by name (not ADR-TBD). ADR-007 includes two specified verification tests (concurrent-claim integration test and chaos test), both described in detail with pass/fail criteria. The ADR-007 requirement was further reinforced by BLD-004's fix, which added Phase 0 explicitly listing ADR-007 as a deliverable alongside ADR-008. No additional spec changes are needed for this finding.

### BLD-006 KMS Deferred — Credentials Unencrypted for 7+ Phases [High]
**Section:** 18
**Status: Fixed**

Real credentials stored without envelope encryption from Phase 5.5 through Phase 12a. Preflight etcd check is non-blocking.

**Recommendation:** Make etcd encryption mandatory before Phase 5.5. Move Token Service multi-replica to Phase 5.5.

**Resolution:** Three changes were made. (1) A new **Phase 5.4** row was added immediately before Phase 5.5. Phase 5.4 mandates etcd encryption-at-rest (`EncryptionConfiguration`) for all Kubernetes Secret resources before any real credentials are written to the cluster. It specifies required providers (`aescbc` minimum, `kms` recommended), a Helm value `etcdEncryption.enabled` defaulting to `true` that fails deployment if disabled in non-development profiles, a CI gate verifying encrypted storage via `etcdctl get`, and a key rotation runbook. Phase 5.4 is marked as a hard prerequisite for Phase 5.5. (2) Phase 5.5 was updated to: (a) acknowledge that Kubernetes Secrets are now protected by Phase 5.4 etcd encryption; (b) introduce **Token Service multi-replica HA** (`replicas: 2`, `PodDisruptionBudget minAvailable: 1`) from Phase 5.5 onward — single-replica is not acceptable once real credentials are present; (c) clarify that the remaining limitation is application-layer KMS (addressed in Phase 12a), not infrastructure-layer encryption. (3) Phase 12a was updated to remove "multi-replica HA deployment and PodDisruptionBudget" from its scope (now in Phase 5.5) and to focus on application-layer KMS envelope encryption and OAuth flows. The §18 introductory note was also updated to reference Phase 5.4 and the distinction between etcd encryption and application-layer KMS.

### BLD-007 Phases 12b and 12c Have No Stated Dependencies [High]
**Section:** 18
**Status: Fixed**

Concurrent execution modes and MCP runtimes introduced without integration-test gates against credential assignment and session initialization.

**Recommendation:** Require each to run Phase 13.5 performance baseline before merging.

**Resolution:** Both Phase 12b and Phase 12c were updated with explicit **integration test gates** as prerequisites for merging. Phase 12b now requires an end-to-end integration test suite covering: (1) MCP runtime session creation and credential assignment from the Phase 5.5 Token Service; (2) `type: mcp` runtime lifecycle (start, idle, claim, release) exercised against the credential-injection path; (3) session initialization against an MCP runtime with valid credentials, confirming no cross-tenant credential leakage. Phase 12c now requires tests covering: (1) concurrent workspace multiplexing via `slotId` exercised against both the credential-assignment path and session initialization path; (2) credential isolation between concurrent slots; (3) concurrent-mode session initialization for all runtime types. Both sets of tests must pass in CI before the respective branch is merged. Note: the recommendation to run Phase 13.5 was adjusted to integration test gates rather than performance baseline gates, since the incremental load tests at Phases 6.5, 9.5, and 11.5 (from BLD-010 fix) cover the performance validation concern earlier and more specifically.

### BLD-008 Phase 17 Bundles Too Much [High]
**Section:** 18, 23.2
**Status: Fixed**

MemoryStore, semantic caching, guardrails, eval hooks, documentation, and community guides in one phase.

**Recommendation:** Split into Phase 17a (documentation, governance, community) and Phase 17b (feature work).

**Resolution:** Phase 17 was split into two distinct phases. **Phase 17a** covers documentation, governance, and community launch: production-grade docker-compose, `CONTRIBUTING.md` and `GOVERNANCE.md` review and finalization, operator runbook review, comparison guides (Lenny vs E2B, Daytona, Fly.io Sprites, Temporal, Modal, LangGraph), community communication channels activation, and open-source license confirmation in all repository artifacts (LICENSE, NOTICE, package manifests). Phase 17a explicitly gates community launch — no external contributor PR solicitation before it completes. **Phase 17b** covers the advanced platform features: Memory (`MemoryStore` + platform tools), semantic caching, guardrail interceptor hooks, and eval hooks. Phase 17b is noted as independent of documentation work and can proceed in parallel with or after Phase 17a.

### BLD-009 No Database Migration Phase [High]
**Section:** 18
**Status: Fixed**

17 phases introduce new data models but no phase establishes the migration framework, conventions, or CI gate.

**Recommendation:** Add Phase 1.5 establishing migration tool, initial schema, CI gate, and rollback documentation.

**Resolution:** A new **Phase 1.5** row was added to the build sequence table immediately after Phase 1. Phase 1.5 establishes the database migration framework with five deliverables: (1) migration tool selection and setup (`golang-migrate`, `atlas`, or `goose` committed to the repository); (2) initial schema migration — all Phase 1 data models (sessions, tasks, tenants, billing events) expressed as numbered migration files (`migrations/0001_initial.sql`, etc.) rather than ad-hoc `CREATE TABLE` scripts; (3) CI migration gate — CI runs all pending migrations against a clean Postgres instance and verifies schema state before any test suite executes; (4) rollback documentation — each migration file has a corresponding rollback script and `docs/runbooks/db-rollback.md` documents the production rollback procedure; (5) convention document — `docs/contributing/migrations.md` specifies naming conventions, backward-compatibility requirements (additive-only for in-flight sessions), and PR review process. Every subsequent phase that introduces new data models must include migration files as part of its definition of done.

### BLD-010 Load Testing Baseline Comes After Full Build [High]
**Section:** 18
**Status: Fixed**

Phase 13.5 baseline measured on nearly production-ready system. Late findings require expensive rework.

**Recommendation:** Introduce incremental load testing after Phase 6, 9, and 11.

**Resolution:** Three new incremental load testing phases were added. **Phase 6.5** (after Phase 6 — streaming path): measures concurrent session creation throughput, streaming reconnect latency (P95/P99) under 500 concurrent sessions, gateway resource use at sustained streaming load, and event replay correctness. Any P99 regression against Section 6.3 budgets must be resolved before Phase 7. **Phase 9.5** (after Phase 9.1 — delegation path): measures delegation fan-out throughput (10, 50 parallel delegates), delegation depth throughput (chains of depth 3/5/10), `lenny/discover_agents` latency under 200 concurrent sessions, and delegation budget propagation latency. Any regression must be resolved before Phase 10. **Phase 11.5** (after Phase 11 — credential lifecycle path): measures credential rotation latency under 200 concurrent sessions, fallback chain activation latency, emergency revocation propagation time, and user-scoped credential elicitation throughput. Any regression must be resolved before Phase 12a. Phase 13.5 was updated to reference these incremental baselines — it now performs a full-system cross-check comparing against Phases 6.5/9.5/11.5 results to detect regressions introduced during Phases 10–13.

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

### ~~FLR-001 Dual-Store Concurrent Outage Leaves Sessions in Limbo~~ [Critical] — **FIXED**
**Section:** 10.1, 12.3, 12.4
**Status:** Fixed

When both Redis and Postgres are unavailable simultaneously, no writable store exists. No defined degraded mode.

**Recommendation:** Define explicit dual-store-down mode: reject new sessions, emit `PLATFORM_DEGRADED` to clients, surface alert.

**Resolution:** Section 10.1 now contains a dedicated "Dual-store unavailability (Redis + Postgres both down)" block (items 1–5) defining the full degraded-mode protocol: (1) existing sessions continue on cached coordination state with valid generation stamps; (2) new `session.create` requests are rejected with `503 Service Unavailable` and `Retry-After: 10`; (3) coordination handoffs are frozen — coordinator crashes during the window leave pods in hold state until recovery; (4) the mode is bounded by `dualStoreUnavailableMaxSeconds` (default: 60s), after which sessions with no store interaction are gracefully terminated with reason `store_unavailable`; (5) replicas emit `dual_store_unavailable` metric, fire `DualStoreUnavailable` alert, and deliver a `PLATFORM_DEGRADED` SSE event to all active client streams within 1 second of detection. Section 12.3 already cross-references Section 10.1 for dual-store behavior during Postgres failover. Section 12.4's failure-behavior table now includes an explicit "Both stores down" row referencing Section 10.1.

### ~~FLR-002 MinIO Outage During Node Eviction Causes Irrecoverable Loss~~ **FIXED** [Critical]
**Section:** 12.5, 4.4

**Status: FIXED**

**Original finding:** Checkpoint cannot be written to MinIO during eviction. Workspace irrecoverably lost with no fallback.

**Fix applied (Sections 4.4, 12.5):** Two-phase eviction checkpoint fallback is now fully specified.

*Eviction path (Section 4.4):*
- The preStop hook retries MinIO upload with exponential backoff (initial 500ms, factor 2×, capped at 5s/attempt, up to **30 seconds total**) before declaring failure.
- On exhaustion of MinIO retries, the adapter writes a **minimal session state record** to Postgres (`session_eviction_state` table) in a single transaction. The record contains: `session_id`, `generation`, `conversation_cursor` (last event cursor for conversation replay), `last_message_context` (last prompt/response pair, max 64 KB), `evicted_at`, and `workspace_lost: true`. It does not store workspace file contents or blobs.
- If a prior full checkpoint exists in MinIO, the session resumes from that checkpoint. If only the Postgres minimal record exists, the session resumes with conversation context but an empty workspace; the client receives `session.resumed` with `resumeMode: "conversation_only"` and `workspaceLost: true`.
- A `CheckpointStorageUnavailable` critical alert fires on any eviction MinIO failure. The `lenny_checkpoint_eviction_fallback_total` counter distinguishes fallbacks with vs. without a prior full checkpoint.

*Pre-eviction checkpoint freshness (Section 4.4):*
- A periodic checkpoint freshness SLO (default 10 minutes) is enforced by the gateway via `lenny_session_last_checkpoint_age_seconds`. The `CheckpointStale` alert fires before any eviction if the SLO is already missed, bounding maximum workspace state loss to at most one periodic interval.

*Section 12.5 update:*
- Documents the Postgres minimal state fallback and cross-references Section 4.4.
- Documents the cascading failure scenario (simultaneous MinIO outage + node drain) and mitigations: stagger node drains, separate MinIO and cluster maintenance windows.

**Recommendation:** Add two-phase checkpoint fallback: attempt MinIO, on failure write minimal manifest to Postgres. Pre-eviction checkpoint at node taint time.

### ~~FLR-003 Redis Fail-Open Creates Unbounded Financial Exposure~~ **ALREADY FIXED (by STR-001)** [High]
**Section:** 12.4
**Status:** Already Fixed (by STR-001)

N replicas × tenant_limit overshoot during Redis outage. Post-recovery reconciliation undefined.

**Recommendation:** Cap per-replica ceiling at `quota / min_replicas`. Define post-recovery reconciliation using per-replica usage logs.

**Resolution:** STR-001 (Critical, Storage perspective) fully addresses this finding. Section 12.4 now specifies: (1) each replica maintains a `cached_replica_count` variable (in-memory, updated from Kubernetes Endpoints, never falls back to `1` on dual-outage); (2) `effective_ceiling = min(tenant_limit / max(cached_replica_count, 1), per_replica_hard_cap)`, where `per_replica_hard_cap` defaults to `tenant_limit / 2`; (3) a cumulative fail-open timer (`quotaFailOpenCumulativeMaxSeconds`, default 300s in a rolling 1-hour window) transitions the replica to fail-closed if the total fail-open window is exceeded; (4) post-recovery reconciliation is defined in Section 12.4 ("Quota counter reconciliation after fail-open") — Redis is reset from Postgres authoritative values, with the MAX of the Redis counter and Postgres checkpoint taken to avoid under-counting. Section 11.2 ("Crash Recovery for Quota Counters") also defines recovery using pod-reported cumulative totals. The N × tenant_limit overshoot is provably bounded by `min(N × (tenant_limit / max(cached_replica_count, 1)), N × per_replica_hard_cap)` — always less than N × tenant_limit.

### ~~FLR-004 Rolling Updates Always Interrupt Long-Running Sessions~~ **FIXED** [High]
**Section:** 10.1, 4.4
**Status:** Fixed

In-flight tool calls at checkpoint time have no defined behavior: abandoned, re-executed, or deduplicated?

**Recommendation:** Introduce `CheckpointBarrier` protocol. Add `tool_call_idempotency_key` for resume deduplication.

**Resolution:** Section 10.1 "Long-running sessions and rolling updates" now includes a new **"CheckpointBarrier protocol for rolling updates"** block with five numbered steps:
1. **Barrier signal** — when preStop flips readiness to `false`, a `CheckpointBarrier` control message carrying `coordination_generation` and `barrier_id` is sent to every coordinated pod.
2. **Pod quiescence** — adapter finishes the current in-flight tool call, then stops accepting new dispatches. `barrier_id` and `last_tool_call_id` are recorded in checkpoint metadata.
3. **Checkpoint flush** — adapter triggers best-effort checkpoint and sends `CheckpointBarrierAck(barrier_id, last_tool_call_id, checkpoint_ref)`. Gateway waits up to `checkpointBarrierAckTimeoutSeconds` (default: 45s).
4. **Tool call idempotency key** — each tool call dispatch carries a `tool_call_idempotency_key = (session_id, coordination_generation, tool_call_sequence_number)`, stored in `session_checkpoint_meta`. On resume, the new coordinator skips any tool call with `sequence_number <= last_tool_call_id`.
5. **Resume deduplication** — new coordinator reads `last_tool_call_id` from Postgres before dispatching, tracked by `coordinator_resume_deduplicated_total` counter.
The protocol bounds interruption to at most one in-flight tool call per session and provides at-most-once tool call semantics across rolling updates.

### ~~FLR-005 Session Inbox Is In-Memory with No Durability~~ **FIXED** [High]
**Section:** 7.2
**Status:** Fixed

Messages silently dropped on coordinator crash. No retry, no acknowledgment, no dead-letter path.

**Recommendation:** Move inbox to Redis list/stream with per-message TTL and explicit ACK step.

**Resolution:** Section 7.2 "Session inbox definition" has been restructured into two modes controlled by the deployment-level `messaging.durableInbox` flag:

- **Default mode (`durableInbox: false`)** — preserves the existing in-memory inbox with documented crash-loss semantics; unchanged from prior behavior.
- **Durable mode (`durableInbox: true`)** — backs the inbox with a Redis list (`t:{tenant_id}:session:{session_id}:inbox`) using `RPUSH`/`LRANGE`/`LPOP`. Key properties: (a) per-message TTL enforced by `enqueued_at` timestamp and a background trimmer; (b) explicit ACK via `LREM` after runtime delivery — message stays at list head until acknowledged, providing at-least-once delivery within the coordinator lifetime; (c) crash recovery via `LRANGE 0 -1` on coordinator lease acquisition — the new coordinator recovers all undelivered messages in FIFO order; (d) `maxInboxSize` enforced atomically via Lua script.

The durable mode satisfies FLR-005's requirement: inbox messages survive coordinator crashes as long as Redis is available. Redis unavailability during durable mode emits `lenny_inbox_redis_unavailable_total` and returns `inbox_unavailable` error receipts.

Note: SLC-006 (previously fixed) addressed the inbox-to-DLQ migration at `resume_pending` transition time, which covers the `running → resume_pending` gap. FLR-005's fix adds the broader Redis-backed option that covers coordinator crashes during all running-state inbox buffering.

### ~~FLR-006 Controller Crash Failover Margin Is Only 5s~~ **FIXED** [High]
**Section:** 4.6.1
**Status:** Fixed

25s failover window vs 30s queue timeout. Under API server slowness, queue exhausts before recovery.

**Recommendation:** Increase `podClaimQueueTimeout` to 60s. Add Postgres-based fallback claim path.

**Resolution:** Section 4.6.1 "Controller failover and warm pool sizing" has been updated with two changes:
1. **`podClaimQueueTimeout` increased from 30s to 60s.** The paragraph now explains the rationale: the previous 30s provided only a 5s margin over the 25s worst-case failover, insufficient when API server latency is elevated (5–15s overhead under load) or pod readiness probes add delay. The 60s default provides a 35s margin.
2. **Postgres-backed fallback claim path added.** After `podClaimQueueTimeout` expires without a successful API-server claim, the gateway attempts a `SELECT ... FOR UPDATE SKIP LOCKED` on `agent_pod_state` (a Postgres-side mirror of `Sandbox` CRD status updated by the WarmPoolController) before returning `WARM_POOL_EXHAUSTED`. The fallback creates the corresponding `SandboxClaim` CRD directly. The `lenny_pod_claim_fallback_total` counter tracks fallback activations. The "Simultaneous controller failover" paragraph was also updated to reference the new 60s value.

### ~~FLR-007 Postgres Failover Creates Billing Consistency Gap~~ **FIXED** [High]
**Section:** 12.3
**Status:** Fixed

Billing buffer lost if gateway pod crashes during Postgres failover window. No buffer size limit or secondary sink.

**Recommendation:** Route billing events through Redis stream as intermediate buffer. Define overflow policy.

**Resolution:** Section 11.2.1 "Immutability guarantees" has been updated with a **two-tier failover path** for billing events during Postgres unavailability:
1. **Tier 1 (Redis stream):** On Postgres write failure, the gateway publishes billing events to a per-tenant Redis stream (`t:{tenant_id}:billing:stream`) via `XADD` with `MAXLEN ~50,000` and TTL 3600s. A background flusher goroutine re-attempts Postgres INSERTs in `stream_seq` order; on success, the entry is `XDEL`'d. The stream survives individual gateway crashes, closing the key gap identified by FLR-007. `BillingStreamBackpressure` alert fires at 80% of `billingRedisStreamMaxLen`.
2. **Tier 2 (in-memory write-ahead buffer):** Only activated when Redis is also unavailable. Unchanged from prior behavior (`billingWriteAheadBufferSize`, default 10,000 events; 503 back-pressure on overflow).

The write-classification load-shedding table in Section 12.3 and the failover durability table were both updated to reflect the new durable status of billing events (durable when Redis is available, at-risk only in the simultaneous Redis + gateway crash scenario). The "Maximum billing gap" paragraph was rewritten: the permanent gap scenario now requires Redis + Postgres + gateway crash + pod loss simultaneously; the common failure (Postgres failover with Redis available) now results in zero billing loss.

### ~~FLR-008 Gateway preStop 30s Cap Abandons Large Workspaces~~ **FIXED** [High]
**Section:** 10.1, 4.4
**Status:** Fixed

30s insufficient for sessions with hundreds of MB workspace. Partial checkpoint behavior undefined.

**Recommendation:** Add tiered checkpoint cap. Write partial manifest even when full upload can't complete.

**Resolution:** Section 10.1 preStop hook stage 2 has been replaced with a **tiered checkpoint cap** and a **partial manifest fallback**:

*Tiered cap (replaces fixed 30s cap):*
| Last measured workspace size | Checkpoint cap |
|------------------------------|----------------|
| ≤ 100 MB | 30s (matches P95 SLO §4.4) |
| 101 MB – 300 MB | 60s |
| 301 MB – 512 MB (hard limit) | 90s |

The gateway reads `last_checkpoint_workspace_bytes` from the session Postgres record to select the tier. The cap is always clamped to `terminationGracePeriodSeconds - 30s` to preserve stage 3 stream drain time.

*Partial manifest on checkpoint timeout:* When a checkpoint upload does not complete within the tiered cap, the gateway writes a **partial checkpoint manifest** to Postgres recording: `workspace_bytes_uploaded`, `partial_object_keys` (committed multipart parts), `partial: true`, and `checkpoint_timeout_at`. On resume: if `workspace_bytes_uploaded >= partial_recovery_threshold_bytes` (default: 50% of last full checkpoint), the new coordinator attempts to reassemble the workspace from committed multipart parts. On success: `session.resumed` event with `resumeMode: "partial_workspace"` and `workspaceRecoveryFraction`. On failure: fallback to last successful full checkpoint. The `lenny_checkpoint_partial_total` counter (labeled by `pool` and `recovered: true|false`) tracks partial checkpoint events.

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
**Status:** Fixed (2026-04-07)

No specification of what signals should prompt manual experiment pause. The `ExperimentHealthEvaluator` interface stub is empty.

**Recommendation:** Add "Manual Rollback Triggers" subsection with concrete example thresholds using platform-native metrics.

**Resolution:** Added a "Manual Rollback Triggers" subsection in §10.7 immediately after the `ExperimentHealthEvaluator` stub. The subsection provides a five-row table of platform-native signals with concrete example thresholds: (1) variant error rate > 5% over two consecutive 5-minute windows → pause; (2) variant p95 latency > 2× control for 10 minutes → pause; (3) treatment mean eval score drops > 0.10 below control with ≥ 50 samples per group → pause; (4) variant warm pool reaches 0 ready pods with a non-empty claim queue for > 60s → pause and investigate; (5) safety scorer mean drops below 0.95 → pause immediately. Each row names the specific Prometheus metric or API endpoint used to detect the condition. The subsection explicitly states these are example thresholds, not platform defaults, and instructs deployers to encode them as Prometheus alerting rules or runbook-automation scripts calling `PATCH /v1/admin/experiments/{id}`.

### EXP-002 Eval Score Ingestion Path Is a Black Box [High]
**Section:** 10.7
**Status:** Fixed (2026-04-07)

Who calls `POST /v1/sessions/{id}/eval`, accepted session states, rate-limiting, idempotency, and storage bounds all undefined.

**Recommendation:** Add "Eval Submission Contract" subsection specifying caller, states, rate limit, idempotency, and trigger model.

**Resolution:** Added an "Eval Submission Contract" table in §10.7 immediately after the eval submission request body paragraph (before the Results API section). The table specifies six dimensions: (1) Caller — any principal with `session:eval:write` permission, including agent runtime, session owner, or external scorer pipeline; (2) Accepted session states — `active`, `completed`, `failed`; `cancelled`/`expired` sessions return `422 SESSION_NOT_EVAL_ELIGIBLE`; (3) Rate limit — 100 submissions/session/minute (Redis sliding window, keyed by `session_id`) and 10,000/minute global per-tenant cap, both returning `429` with `Retry-After`; (4) Idempotency — optional `idempotency_key` field (max 128 bytes), 24-hour dedup window via Redis TTL keyed by `session_id + idempotency_key`, returns `200` with original record on match; (5) Storage bounds — max 10,000 `EvalResult` records per session (configurable via `maxEvalsPerSession`, max 100,000), returns `429 EVAL_QUOTA_EXCEEDED` on breach; (6) Trigger model — pull-only, no eval scheduling or LLM-judge integration by the platform.

### EXP-003 Variant Pool Cold-Start Has No Guidance [High]
**Section:** 4.6.2, 10.7
**Status:** Fixed (2026-04-07)

No `initialMinWarm` field for variant pools. PoolScalingController produces `minWarm ≈ 0` for new experiments.

**Recommendation:** Add optional `initialMinWarm` field to `ExperimentDefinition.variants[]`.

**Resolution:** Added `initialMinWarm` as an optional field on each entry in `ExperimentDefinition.variants[]` in two places. (1) The `ExperimentDefinition` YAML example in §10.7 now includes `initialMinWarm: 5` with an inline comment. (2) A new "Variant `initialMinWarm` — cold-start guidance" prose block was inserted in §10.7 immediately after the "Control group identifier" paragraph. The block explains: the formula yields `minWarm = 0` at creation time because no demand history exists; `initialMinWarm` sets a static floor used only during bootstrap mode (exits per §4.6.2 and §17.8.2 convergence criteria); after bootstrap, the field is discarded and the formula-derived value takes over; the field has no effect on re-activations. Sizing guidance: `ceil(expected_peak_rps × weight_fraction × (failover_seconds + pod_startup_seconds))`; low-weight ramp experiments (≤ 5%) typically need only 3–5 warm pods; omitting the field defaults to `0`.

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

### DOC-101 Section 17.8 Heading Does Not Exist — 34 Cross-References Broken [Critical] ✓ FIXED
**Section:** 17.8
**Status:** Fixed

Added `### 17.8 Capacity Planning and Defaults` as a parent heading immediately before `### 17.8.1 Operational Defaults — Quick Reference`. The new heading includes a one-paragraph summary describing the purpose of the section and directing readers to the two subsections. All 34 cross-references throughout the document that cite "Section 17.8" now resolve correctly to the new parent heading.

**Recommendation:** Add `### 17.8 Capacity Planning and Defaults` as parent heading before 17.8.1.

### DOC-102 Renumbering Introduced Broken Cross-Reference in Section 9.2 [High] ✓ FIXED
**Section:** 9.2 (line 2882)
**Status:** Fixed

"Section 8.7" now points to File Export Model instead of Task Tree (Section 8.9).

In Section 9.2 ("Elicitation Chain"), the sentence describing deadlock detection contained a stale cross-reference "Section 8.7" (File Export Model) where it should have read "Section 8.9" (Task Tree). Updated the reference from `Section 8.7` to `Section 8.9` so it correctly points to the Task Tree section that describes the deadlock detection mechanism.

**Recommendation:** Update line 2882 from "Section 8.7" to "Section 8.9".

### DOC-103 KMS Key Rotation Procedure Buried in Upgrade Strategy Section [High] ✓ FIXED
**Section:** 10.5
**Status:** Fixed

Security-operations content in an upgrade strategy section. Two cross-references cite 10.5 for KMS rotation.

The KMS key rotation procedure has been extracted from Section 10.5 ("Upgrade and Rollback Strategy") into a new dedicated subsection **Section 4.9.1 ("KMS Key Rotation Procedure")** within the Credential Leasing Service component — the logical home for all credential and key management operational procedures. The new section covers all rotation steps (DEK generation, background re-encryption job, `key_version` tracking, old-key disablement), Redis cache invalidation (tokens derived from the envelope key are invalidated and re-derived on next access), rotation frequency (every 90 days or on suspected compromise), and the monitoring alert (re-encryption job must complete within 24h). Section 10.5 now contains a concise forward-reference: "See Section 4.9.1 for the full KMS envelope key rotation procedure." The cross-reference in Section 12.4 (Redis security) has been updated from "Section 10.5" to "Section 4.9.1".

**Recommendation:** Extract KMS rotation into Section 4.9.x or 13.x. Update cross-references.

### DOC-104 `ReportUsage` RPC Missing from Adapter RPC Table [High] ✓ FIXED
**Section:** 4.7 (lines 462-479)
**Status:** Fixed

Referenced 3 times in the body but absent from the authoritative RPC contract table.

Added `ReportUsage` to the gateway ↔ adapter RPC contract table in Section 4.7. The new row is placed between `Resume` and `Terminate` and reads: "Report LLM token counts extracted from provider responses; gateway increments quota counters and persists to Postgres on the next sync interval (see Section 11.2)." This matches the runtime behaviour described in Sections 11.2 and 12.3, which reference `ReportUsage` as the mechanism by which the adapter forwards token usage to the gateway.

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

### ~~MSG-001 Path 2 Timeout Fallback to Inbox Overflow Ambiguous~~ **FIXED** [High]
**Section:** 7.2

When path 2 falls through to inbox and inbox is at `maxInboxSize`, delivery receipt already returned as `delivered` becomes incorrect.

**Recommendation:** Path 2 should return `delivered` only after confirmed stdin consumption. Return `queued` if routing to inbox.

**Fix applied (§7.2):** Path 2 delivery path now explicitly states that the `delivered` receipt is issued **only after confirmed stdin consumption** — i.e., the adapter acknowledges the write within the configurable delivery timeout (default: 30 seconds). When the runtime does not consume the message within the timeout and the gateway falls through to inbox buffering, the delivery receipt status is now explicitly `queued`, not `delivered`. The prose distinguishes the two outcomes with a bold note: "Delivery receipt status is `delivered` only after confirmed stdin consumption … in this fallback case the delivery receipt status is `queued`, not `delivered`."

### ~~MSG-002 Session Inbox Loss on Crash Is Undiscoverable~~ **FIXED** [High]
**Section:** 7.2

No sequence number, cursor, or `inbox_reset` event for senders to detect lost messages after coordinator handoff.

**Recommendation:** Add durable Redis inbox or explicit `inbox_cleared` event. Define gap-detection API.

**Fix applied (§7.2):** FLR-005 (previously fixed) added the durable Redis-backed inbox mode (`durableInbox: true`), which eliminates crash-loss in that mode. For the default in-memory mode, the crash-recovery row in the session inbox table now specifies that the gateway emits an **`inbox_cleared`** event on the session's event stream immediately after the new coordinator acquires the lease: `{ "type": "inbox_cleared", "reason": "coordinator_failover", "clearedAt": "<ISO8601>" }`. This event enables senders that received a `queued` receipt to discover the gap and re-send. The prose mandates that senders requiring reliable delivery MUST listen for `inbox_cleared` and re-send, or use the DLQ path. Note: FLR-005's durable inbox addresses the broader durability concern; `inbox_cleared` addresses the gap-discoverability requirement for the default mode, which FLR-005 did not cover.

### ~~MSG-003 `input_required` State Not Integrated Across State Machine Diagrams~~ **FIXED** [High]
**Section:** 6.2, 7.2, 8.8

Defined as sub-state of `running` in 7.2, peer state in 8.8, absent from 6.2. Transitions to `cancelled`/`expired` inconsistent.

**Recommendation:** Unify across all three state machines. Include `input_required` in pod state machine as sub-state.

**Fix applied (§6.2):** Section 6.2 now includes `input_required` explicitly in two places: (1) a new **`input_required` sub-state** block with the four transitions (`running → input_required`, `input_required → running`, `input_required → cancelled`, `input_required → expired`) and an explicit note that the pod is live and runtime is active but blocked; (2) a new row in the `maxSessionAge` timer behavior table defining `input_required` as **Running** (timer continues, session is logically active). A cross-reference note reads "This aligns with the canonical task state machine (Section 8.8) and the interactive session model (Section 7.2)." The pod-level state machine diagram was also extended with the `input_required` sub-state block immediately following the session transitions. Sections 7.2 and 8.8 were already consistent with each other; §6.2 was the only gap.

### ~~MSG-004 SSE Replay Window Boundary Underspecified~~ **ALREADY FIXED** [High]
**Section:** 7.2

"Checkpoint window" for event replay is undefined. No behavior for gaps outside the replay window.

**Recommendation:** Define replay window explicitly. Include `events_lost` count in `checkpoint_boundary` marker.

**Already fixed by SLC-003 (§7.2):** The replay window is explicitly defined as `max(periodicCheckpointIntervalSeconds × 2, 1200s)` (default 1200 s / 20 min). The `checkpoint_boundary` marker schema is fully specified with `events_lost` (integer count of unreplayable events), `reason` (`"replay_window_exceeded"` or `"event_store_unavailable"`), and `checkpoint_timestamp`. The spec mandates that clients MUST treat `events_lost > 0` as a data-loss event. MSG-004 is a duplicate of SLC-003 and is resolved by the SLC-003 fix already applied.

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
**Status:** Fixed

Token usage counter and delegation budget reservation counter are separate Redis keys. Parent can have consumed 190K of 200K tokens while still having full 200K in delegation counter.

**Recommendation:** `budget_reserve.lua` must also read parent's actual usage counter atomically. Cap child slice to `min(requested, parentBudget - parentUsage)`.

**Fix applied (Section 8.3 — Budget Reservation Model, steps 1 and 4):** The `budget_reserve.lua` specification now requires the script to atomically read three counters: the parent's token budget counter, the parent's actual token usage counter, and the tree-size counter. The effective remaining budget is computed inside the script as `parentBudget - parentUsage`; the child slice is capped to `min(requested_slice, parentBudget - parentUsage)` before any reservation check is applied. Both the step 1 (Reservation) narrative and the step 4 (Concurrency safety) detailed description were updated to reflect this three-counter atomic read. The race condition — where a parent with nearly exhausted actual usage could still grant a child a slice based on the stale delegation counter — is closed because the usage check occurs atomically within the same Lua script evaluation as the reservation.

### POL-002 Fail-Open Window Is Unbounded Within Per-Replica Ceiling [Critical]
**Section:** 12.4, 11.2
**Status:** Already Fixed (by STR-001)

With both Redis and Endpoints unavailable, every replica allows full `tenant_limit`. Aggregate overshoot = `N × tenant_limit`.

**Recommendation:** Add `quotaFailOpenReplicaFloor` config. Effective ceiling = `tenant_limit / max(actual_count, floor)`.

**Resolution:** Addressed by the STR-001 fix already applied to Sections 12.4 and 11.2. The `cached_replica_count` in-memory variable (persisted across poll failures, never falls back to `1`) prevents the core `N × tenant_limit` dual-outage overshoot. The additional `per_replica_hard_cap` (default: `tenant_limit / 2`, configurable via `quotaPerReplicaHardCap`) provides an equivalent or stronger bound than the recommended `quotaFailOpenReplicaFloor`: with the default, per-replica ceiling is at most `tenant_limit / 2`, which is equivalent to a `replicaFloor` of `2` — the minimum meaningful value. The effective ceiling formula `min(tenant_limit / max(cached_replica_count, 1), per_replica_hard_cap)` ensures total cluster-wide overshoot is bounded well below `N × tenant_limit` in all dual-outage scenarios. The `quotaFailOpenReplicaFloor` approach as originally recommended is not added as a separate parameter because `quotaPerReplicaHardCap` is a strictly more flexible mechanism (an absolute cap rather than a divisor floor, independently configurable regardless of tenant_limit scale).

### POL-003 Quota Update Timing Creates Dual-Source Inconsistency [High]
**Section:** 11.2, 12.4

On Redis recovery, Postgres checkpoint (30s stale) could reset counters to lower value, effectively un-enforcing a budget violation.

**Recommendation:** Take `MAX(redis_counter_before_failure, postgres_checkpoint)` on recovery. Write authoritative value on session completion.

**Status: Fixed.** Two changes applied:

1. **§11.2 "Quota Update Timing"** — Added explicit paragraph stating that on Redis recovery the gateway applies `restored_counter = MAX(in_memory_counter, postgres_checkpoint)` for each active session and tenant scope, with explanation of why the Postgres-only value is insufficient (up to `quotaSyncIntervalSeconds` stale). Added statement that on session completion the final cumulative usage is written to Postgres as an authoritative reconciliation point.

2. **§12.4 "Quota counter reconciliation after fail-open"** — Replaced "resetting Redis counters to match [Postgres]" with a full description of the two-source MAX rule: the gateway reads both the last Postgres checkpoint value and the in-memory counter accumulated during the fail-open window, then writes `MAX(postgres_checkpoint, in_memory_counter)` as the authoritative Redis value. Added explanation that using the Postgres value alone would reset counters to a stale value, potentially un-enforcing a violation, and that the MAX rule prevents loss of usage accumulated during the fail-open window.

### POL-004 Interceptor Short-Circuit and MODIFY Interaction Undefined [High]
**Section:** 4.8

External interceptors can modify task input before `QuotaEvaluator` reads it. Built-in evaluators may operate on modified data.

**Recommendation:** Document which fields each built-in reads. Specify that modification of quota-relevant fields triggers re-evaluation.

**Status: Fixed.** Added a new subsection **"Built-in interceptor field dependencies and MODIFY interaction"** (§4.8, before the LLM interceptor phases paragraph). The subsection contains:

- A table mapping each built-in interceptor (`AuthEvaluator`, `QuotaEvaluator`, `ExperimentRouter`, `GuardrailsInterceptor`) to: the phases where it is active, the specific fields it reads from the intercepted payload, and the precise effect of an upstream MODIFY on its behavior.
- A paragraph "Short-circuit interaction with MODIFY" clarifying that the modified payload propagates forward to all subsequent interceptors including built-ins, there is no re-evaluation against the original, and deployers who register external interceptors at priorities 101–199 (between `AuthEvaluator` and `QuotaEvaluator`) must be aware that MODIFY operations on quota-relevant fields (e.g., input byte length) are seen by `QuotaEvaluator`. The finding's request for "re-evaluation on modification of quota-relevant fields" was resolved as documented behavior rather than a new mechanism: `QuotaEvaluator` already operates on the post-modification payload, which is the correct and intended behavior.

### POL-005 `contentPolicy` Inheritance Through Delegation Not Fully Specified [High]
**Section:** 8.3

"Same or more restrictive `interceptorRef`" has no mechanism for the gateway to evaluate restrictiveness of named references.

**Recommendation:** Define concrete enforcement rule. Add note that runtime changes to `failPolicy` don't retroactively affect lease restrictiveness.

**Status: Fixed.** Added subsection **"`interceptorRef` restrictiveness enforcement rule"** immediately after the `contentPolicy` enforcement paragraph in §8.3. The rule is identity-based (named interceptors are opaque gRPC services; the platform cannot compare their logic). The five conditions are:

1. Same reference → always permitted.
2. Additional interceptor (parent's is retained) → permitted.
3. Null-to-non-null → permitted (adding a check).
4. Non-null-to-null → rejected with `CONTENT_POLICY_WEAKENING`.
5. Different non-null reference (substitution, without retaining parent's) → rejected with `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`.

Added a separate paragraph on runtime `failPolicy` changes: these do not retroactively invalidate existing leases; active leases referencing the interceptor by name silently inherit the changed behavior, and deployers should treat `failPolicy` changes on referenced interceptors with the same care as policy weakening.

### POL-006 Fail-Open Rate Limiting Lacks Per-User Bounding [High]
**Section:** 12.4

Per-tenant fail-open ceiling but no per-user ceiling. One user can monopolize the entire allocation.

**Recommendation:** Add per-user fail-open ceiling alongside per-tenant ceiling.

**Status: Fixed.** Added paragraph **"Per-user fail-open ceiling"** in §12.4, immediately after "Bounded fail-open for rate limiting". The mechanism: each gateway replica maintains an in-memory per-user token and request counter during the fail-open window. Per-user ceiling formula: `per_user_failopen_ceiling = min(tenant_limit * userFailOpenFraction, per_replica_hard_cap)`, where `userFailOpenFraction` defaults to 0.25 (25% of tenant fail-open allocation per user per replica), configurable via `quotaUserFailOpenFraction` in Helm values. When a user reaches the ceiling, their requests are rejected with 429 for the remainder of the fail-open window even if per-tenant headroom remains. Per-user counters are reset on Redis recovery using the standard MAX rule.

### POL-007 PreAuth Phase MODIFY Semantics Are Dangerous [High]
**Section:** 4.8

`PreAuth` phase should only be accessible to built-ins (priority ≤ 100) but MODIFY semantics are described as if external interceptors could fire there.

**Recommendation:** Explicitly state PreAuth is built-in only. Remove MODIFY semantics for external interceptors at this phase.

**Status: Fixed.** Two changes applied:

1. **Phase payload table — `PreAuth` row** — Replaced the generic payload/MODIFY description with explicit statements: "Built-in interceptors only (priority ≤ 100). External interceptors are **never** invoked at this phase — the gateway rejects any external interceptor registration that targets `PreAuth` with `INVALID_INTERCEPTOR_PHASE`." The MODIFY column now states: "MODIFY semantics at this phase are **internal only** — `AuthEvaluator` may normalize request metadata before passing control to downstream phases. No external interceptor may issue MODIFY at `PreAuth`."

2. **Priority reservation section (§4.8)** — Added a new paragraph **"`PreAuth` phase is built-in only"** explicitly stating: the `PreAuth` phase runs exclusively within the priority 1–100 range; external interceptors are never invoked there regardless of their configured phase list; the gateway rejects external registrations that include `PreAuth` in their phase set with `INVALID_INTERCEPTOR_PHASE`; and all external policy controls (IP blocklisting, etc.) that deployers might want at `PreAuth` must instead be registered at `PostAuth` or later phases.

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
**Status:** FIXED

No `attached → failed` or `attached → resume_pending` transition in the task-mode state diagram.

**Recommendation:** Add explicit transitions. Specify whether crash fails the task outright or retries on a new pod.

**Resolution:** Two transitions added to the task-mode state diagram in §6.2:
- `attached ──→ failed` (pod crash / node failure / unrecoverable gRPC error, retries exhausted or non-retryable)
- `attached ──→ resume_pending` (pod crash / gRPC error, `retryCount < maxTaskRetries`)

A new prose paragraph "Pod crash during active task-mode task" was inserted immediately after the state diagram, specifying:
- **Retry-on-new-pod semantics**: `resume_pending` here means waiting for a fresh pod to re-run the task from scratch — not session checkpoint recovery.
- **Retry policy**: default `maxTaskRetries: 1` (2 total attempts); each retry dispatches the original task on a fresh pod with a clean workspace.
- **Retry exhaustion / non-retryable failures**: task transitions to `failed`; pod is terminated.
- **Non-retryable categories**: OOM, workspace validation errors, policy rejections.
- **`maxTaskRetries: 0`** disables retries (crash always fails the task outright).

### EXM-002 Concurrent-Stateless Mode Is Underspecified [High]
**Section:** 5.2
**Status:** FIXED

Single paragraph of specification. No lifecycle, failure semantics, deployer guidance, or decision criteria vs connectors.

**Recommendation:** Either provide full specification or deprecate from v1 scope with connector recommendation.

**Resolution:** A "Concurrent-stateless limitations (v1)" callout block was added immediately after the `concurrencyStyle: stateless` paragraph in §5.2. Rather than writing a full parallel specification, the fix takes the minimal approach: clearly document what the mode does NOT provide (no workspace delivery, no per-slot lifecycle tracking, no slot-level retry, no checkpoints, no per-slot failure isolation) and explicitly name connectors as the preferred alternative for stateless workloads. A structured decision guide (when to use `stateless` vs connectors) is included so deployers can make an informed choice without needing to read additional sections. The mode is retained in v1 scope — not deprecated — because existing deployments may rely on it, but new deployments are steered toward connectors.

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
