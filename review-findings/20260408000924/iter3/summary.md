# Technical Design Review Findings — 2026-04-08 (Iteration 3)

**Document reviewed:** `technical-design.md` (~10,039 lines)
**Review framework:** `review-povs.md` (25 perspectives)
**Iteration:** 3 of 8 — continuation from iteration 2
**Total findings:** 270 across 25 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 12    |
| Medium   | 166   |
| Low      | 92    |

### Carried Forward from Iterations 1–2 (still present / skipped)

| # | ID | Finding | Status |
|---|------|---------|--------|
| 1 | K8S-035 / NET-034 | `lenny-pool-config` ghost webhook — referenced but never formally defined | Skipped |
| 2 | WPL-030 | Failover formula 25s — intentionally conservative | Skipped |
| 3 | DEL-039 | `settled=all` redundant mode in `lenny/await_children` | Skipped |
| 4 | FLR-038 | Redis runbook references phantom metrics/alerts/config params | Skipped |
| 5 | CMP-041 | Salt rotation cannot re-pseudonymize billing records | Skipped |
| 6 | POL-041 | Cross-phase priority ordering error | Skipped |
| 7 | MSG-037 | `delivery_receipt` schema omits `error` from populated-status list | Skipped |
| 8 | CRD-031/032 | Secret shape table missing rows for `vault_transit` and `github` providers | Skipped |
| 9 | DOC-036 | Orphaned footnote number 4 | Skipped |

### High Findings

| # | ID | Perspective | Finding | Sections |
|---|------|-------------|---------|----------|
| 1 | SLC-056 | Session Lifecycle | Missing `input_required -> resume_pending` transition for pod crash | 6.2, 7.2 |
| 2 | OBS-050 | Observability | Coordinator handoff protocol has zero duration observability | 10.1 |
| 3 | OBS-051 | Observability | RuntimeUpgrade state machine has zero observability | 6.2 |
| 4 | CMP-056 | Compliance | Experiment variant sticky cache absent from user-level erasure scope | 11.3 |
| 5 | CMP-057 | Compliance | User-level erasure has no specified dependency ordering | 11.3 |
| 6 | CPS-041 | Competitive | Google A2A Protocol governance claim still factually incorrect | 19 |
| 7 | CPS-043 | Competitive | No sustainability or commercial model specification | 19 |
| 8 | CPS-048 | Competitive | Kubernetes requirement is major adoption barrier not addressed | 19 |
| 9 | BLD-050 | Build Sequence | Phase 2 checkpoint benchmark requires Full-tier lifecycle channel not yet available | 18 |
| 10 | FLR-051 | Failure Modes | Billing Redis stream TTL expiry creates silent data loss | 12.3, 11.2.1 |
| 11 | EXM-054 | Execution Modes | Concurrent-workspace slot assignment atomicity has Redis restart race | 5.2 |
| 12 | MSG-065 | Messaging | `children_reattached` event undefined in any message schema | 7.2, 8.10 |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes Infrastructure (K8S)

### K8S-050. Gateway ServiceAccount RBAC for pod label patching (`lenny.dev/tenant-id`) is never specified [Medium]

**Section:** 5.2 (Tenant pinning), 4.6.3 (RBAC), 13.2 (Network Security)

Section 5.2 states: "Warm-pool pods are labeled `lenny.dev/tenant-id: {tenant_id}` at first assignment time by the gateway agent." This means the **gateway** needs RBAC permission to `PATCH` pod labels in agent namespaces (`lenny-agents`, `lenny-agents-kata`). However, Section 4.6.3 exhaustively enumerates RBAC grants for the WarmPoolController and PoolScalingController ServiceAccounts but never mentions the gateway's ServiceAccount grants for pod operations. The gateway also sets `lenny.dev/state` from `idle` to `active` at claim time (Section 6.2, line 2429: "The pod label `lenny.dev/state` is `active` whenever `active_slots > 0`"), which is another pod label mutation.

The `lenny-tenant-label-immutability` webhook must also be configured to allow the gateway ServiceAccount (not just the WPC ServiceAccount) to perform the initial `lenny.dev/tenant-id` label write. Without explicit gateway RBAC grants for pod `PATCH` in agent namespaces, the tenant-pinning label write will fail with 403 Forbidden at runtime.

**Recommendation:** Add an explicit RBAC grants paragraph for the gateway ServiceAccount specifying at minimum: `get`/`patch` on `Pods` in agent namespaces (for tenant-id and state label mutations), `create`/`get`/`delete` on `SandboxClaim` resources (for the claim path), and whatever additional CRD access the gateway uses. Additionally, specify that the `lenny-tenant-label-immutability` webhook allows the gateway ServiceAccount to set `lenny.dev/tenant-id` on initial assignment.

---

### K8S-051. WarmPoolController RBAC missing `create`/`get`/`list` on `Pods` for cert-expiry tracking and health-checking [Medium]

**Section:** 4.6.1, 4.6.3

Section 4.6.1 specifies that the WarmPoolController "verif[ies] that newly created pods have valid certificates before marking them as idle" and "continuously tracks certificate expiry on idle pods and proactively drains any idle pod whose certificate will expire within 30 minutes" (Section 10.3, line 4257). Certificate information lives on the actual `Pod` resource (or its associated cert-manager `Certificate` resource), not on the `Sandbox` CRD. Similarly, health-checking requires reading Pod status (container statuses, readiness gates).

The RBAC grants in Section 4.6.3 list `create`/`update`/`delete` on `Sandbox` and `get`/`patch` on various status subresources, plus `get`/`list`/`watch` on CRD types. But there is no grant for `Pods` themselves. If agent-sandbox creates the actual Pod from the Sandbox CRD (as implied by "Pods owned by `Sandbox` CRD" in 17.1), the WPC may need `get`/`list`/`watch` on `Pods` in agent namespaces to read certificate annotations, check pod phase/conditions, and verify container status. Additionally, the WPC needs `get`/`list` on `certificates.cert-manager.io` to track cert-expiry (or it must read cert info from pod annotations set by cert-manager).

**Recommendation:** Specify whether the WPC reads certificate expiry from the `Sandbox` CRD status (populated by agent-sandbox from the underlying Pod), from cert-manager `Certificate` resources, or from Pod annotations. Add the corresponding RBAC grants to the WPC ServiceAccount enumeration in Section 4.6.3.

---

### K8S-052. `lenny-label-immutability` and `lenny-tenant-label-immutability` are separate webhooks with overlapping scope but inconsistent specification [Medium]

**Section:** 5.2, 13.2 (NET-003)

Two distinct `ValidatingAdmissionWebhooks` protect pod labels but are specified with different levels of rigor:

1. **`lenny-label-immutability`** (Section 13.2, NET-003): Protects `lenny.dev/managed`, `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`, and `lenny.dev/dns-policy`. Only the WPC ServiceAccount may set them at creation. Deployed with replicas: 2, PDB, and preflight check. Its manifest is located at `templates/admission-policies/label-immutability-webhook.yaml`.

2. **`lenny-tenant-label-immutability`** (Section 5.2): Protects `lenny.dev/tenant-id`. Allows `unset -> {tenant_id}` and `{tenant_id} -> unassigned` but rejects other mutations. Deployed "as part of the Helm chart under `templates/admission-policies/`".

However, `lenny-tenant-label-immutability` lacks the HA specification given to `lenny-label-immutability` (no replicas count, no PDB, no dedicated availability alert, no `failurePolicy` specified). It also does not specify which ServiceAccount is authorized to perform the initial label write -- the gateway sets it, but the webhook specification does not name the gateway SA as an allowed writer. If the tenant-label webhook is also fail-closed, it needs the same HA treatment.

**Recommendation:** Either consolidate both webhooks into a single `lenny-label-immutability` webhook that covers all protected labels (including `lenny.dev/tenant-id`) with unified HA/PDB/alert requirements and explicit ServiceAccount allowlists per label, or add explicit HA specification (replicas, PDB, failurePolicy, alert) to the `lenny-tenant-label-immutability` webhook and document that the gateway ServiceAccount is the authorized initial writer.

---

### K8S-053. PoolScalingController RBAC grants `create` on `SandboxTemplate` and `SandboxWarmPool` but these are Postgres-authoritative and should only be created through the admin API reconciliation [Low]

**Section:** 4.6.3, 4.6.2

Section 4.6.2 states: "CRDs become derived state reconciled from Postgres by PoolScalingController." This implies the PSC should create CRDs when a new pool is defined in Postgres (via the admin API) and no corresponding CRD exists yet. The `create` RBAC grant is therefore correct in practice.

However, the validating webhook (Section 4.6.3, line 631) "rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` fields unless the request carries the annotation `lenny.dev/managed-by: pool-scaling-controller`." This webhook only checks for the annotation on `UPDATE` operations -- it does not mention `CREATE` operations. A user with `kubectl create` access could create a `SandboxTemplate` or `SandboxWarmPool` that does not exist in Postgres. The WPC would then reconcile against a CRD that has no Postgres backing, potentially creating pods for a phantom pool. On the next PSC reconciliation cycle, the PSC would either overwrite the CRD (if a matching pool exists in Postgres) or leave it orphaned (if no matching pool exists).

**Recommendation:** Extend the validating webhook to also intercept `CREATE` operations on `SandboxTemplate` and `SandboxWarmPool`, rejecting creation requests that lack the `lenny.dev/managed-by: pool-scaling-controller` annotation. This prevents phantom CRD creation that could cause the WPC to spin up pods for non-existent pools.

---

### K8S-054. `concurrent-workspace` pod `lenny.dev/state` label oscillates between `idle` and `active` on every slot completion, causing high API server write churn [Medium]

**Section:** 6.2

Section 6.2 line 2429 states: "The pod label `lenny.dev/state` is `active` whenever `active_slots > 0` and `idle` when `active_slots == 0`." The concurrent-workspace state machine (line 2402) shows: `slot_active -> idle (last active slot completes/fails — active_slots reaches 0)`. For a pod at or near capacity with rapid slot turnover (e.g., `maxConcurrent: 8` with short-lived tasks), the pod will frequently oscillate between `active` and `idle` as the last slot completes and a new slot is assigned milliseconds later.

Each label change is a pod `PATCH` to the API server. At Tier 3 with hundreds of concurrent-workspace pods, this creates unnecessary label-mutation churn -- exactly the kind of high-frequency label mutation that Section 6.2 was designed to avoid (line 2520: "This avoids the 8-10 label mutations per session that would stress the API server at scale."). The `statusUpdateDeduplicationWindow` (line 511) only applies to `Sandbox` CRD status updates, not to pod label patches.

**Recommendation:** For concurrent-workspace pods, transition the `lenny.dev/state` label to `idle` only after a stabilization delay (e.g., 5 seconds with no slot assigned). Alternatively, keep concurrent-workspace pods labeled `active` for their entire lifetime while any slot assignment has ever occurred (transitioning to `idle` only when the pod returns to the pool after draining), and track slot-level availability through the Redis counter and `Sandbox` CRD status only.

---

### K8S-055. Validating webhook for Postgres-authoritative CRD state uses an annotation as the authorization mechanism -- annotations are not immutable and can be spoofed [Medium]

**Section:** 4.6.3

Line 631 describes the validating webhook: "rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` fields unless the request carries the annotation `lenny.dev/managed-by: pool-scaling-controller`." The PSC sets this annotation on every update.

The problem is that annotations are user-controlled metadata -- any principal with `patch` access to the CRD can set the annotation `lenny.dev/managed-by: pool-scaling-controller` on their own `kubectl apply` request and bypass the webhook. This makes the annotation-based check a convention-level control, not a security enforcement. Unlike the `lenny-label-immutability` webhook which checks `userInfo` to verify the caller's ServiceAccount identity, this webhook checks only an annotation the caller can freely set.

The SSA field manager mechanism provides real enforcement (the API server tracks which field manager owns each field), and RBAC provides coarse-grained enforcement. The annotation-based webhook adds no actual enforcement beyond what SSA already provides and gives a false sense of defense-in-depth.

**Recommendation:** Replace the annotation-based authorization check with a `userInfo`-based check, same as the `lenny-label-immutability` webhook -- only allow updates from the PSC ServiceAccount (`system:serviceaccount:lenny-system:lenny-pool-scaling-controller`). This provides genuine defense-in-depth that cannot be bypassed by a principal who can set annotations.

---

That concludes the K8S Iteration 3 findings. Summary:

| Severity | Count |
|----------|-------|
| Critical | 0     |
| High     | 0     |
| Medium   | 5     |
| Low      | 1     |

The specification is in strong shape after two prior iterations. The findings above address genuine gaps in RBAC specification completeness (K8S-050, K8S-051), webhook consistency and HA parity (K8S-052), admission control coverage (K8S-053), API server write efficiency for concurrent-workspace pods (K8S-054), and a spoofable authorization mechanism in the Postgres-authoritative CRD webhook (K8S-055).

---

## 2. Security & Threat Modeling (SEC)

### SEC-054. `callbackSecret` Storage and Lifecycle Lacks Specificity [Medium]

**Section:** 14 (Workspace Plan Schema), line ~6354

The `callbackSecret` is described as "stored encrypted" but the design does not specify: (a) which encryption mechanism is used (envelope encryption via KMS, application-layer AES, Postgres `pgcrypto`, etc.), (b) which storage layer holds the ciphertext (Postgres session table? A dedicated secrets table? A Kubernetes Secret?), (c) key rotation semantics for the encryption key, (d) access control beyond the gateway (does the `lenny_app` DB role have `SELECT` access to the ciphertext column? Can `tenant-admin` users read it back via any API?), or (e) when the secret is purged (on session termination? after GDPR erasure?). By contrast, credential pool secrets receive extensive KMS key hierarchy and storage topology treatment (Section 4.9). A `callbackSecret` is T3/T4 material (it is a client-provided HMAC signing key), yet its storage receives only the two-word phrase "stored encrypted."

**Recommendation:** Specify: (1) `callbackSecret` is stored in the `sessions` table as KMS-envelope-encrypted ciphertext using the same `JWTSigner` KMS backend (or a dedicated data-encryption key), (2) the plaintext is never returned by any API endpoint (write-only field), (3) the `lenny_app` role can `SELECT` the ciphertext column but only the gateway with KMS access can decrypt it, (4) the secret is purged (set to NULL) when the session reaches a terminal state and all webhook delivery attempts are exhausted or succeeded, and (5) GDPR erasure pseudonymizes or deletes the column. Add a cross-reference to Section 12.9 data classification confirming the classification tier.

---

### SEC-055. Semantic Cache Timing Side-Channel Across Users Within a Tenant [Medium]

**Section:** 4.9 (Semantic Cache), line ~1437

The semantic cache with `cacheScope: per-user` prevents cross-user content leakage -- User B cannot read User A's cached response. However, the design does not address timing side-channels: a cache hit returns faster than a cache miss, and response latency is observable to the caller. If User A's query populates the cache for a given embedding region and User B issues a semantically similar query, User B gets a cache miss (correct content isolation), but the embedding computation and vector similarity search still differ in observable timing depending on cache population density and index state. More critically, with `cacheScope: tenant` (the opt-in cross-user mode), a user can enumerate whether specific prompts have been issued by other users in the same tenant by measuring response latency differences between cache-hit and cache-miss paths.

**Recommendation:** For `cacheScope: tenant`, document the timing side-channel as an explicit trade-off in the deployer-facing documentation (similar to the residual state vectors documented for task mode scrub). For regulated tenants (`complianceProfile: hipaa`), consider rejecting `cacheScope: tenant` (already done -- the `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` rejection handles this), and add a note in the design that the rejection also mitigates the timing side-channel. No code change needed for `per-user` scope, but the risk should be documented.

---

### SEC-056. `lenny/get_task_tree` Exposes Sibling Session Metadata Without Scoping [Medium]

**Section:** 8.5 (Delegation Tools), line ~3378

`lenny/get_task_tree()` returns the task hierarchy with states, where each node includes `taskId`, `state`, and `runtimeRef`. The design explicitly states: "A child session can discover its siblings (other children of its parent) by inspecting the tree." This means a child session controlled by a potentially untrusted runtime can observe: (a) the existence and count of sibling sessions, (b) which `runtimeRef` (runtime type) each sibling is using, and (c) the real-time state of each sibling. This is an information disclosure concern in delegation trees where different child sessions are delegated to runtimes of varying trust levels. A low-trust child runtime can learn the identities of high-trust runtimes in the tree, the timing of their state transitions, and the overall orchestration strategy of the parent -- all of which may be sensitive in multi-runtime deployments.

**Recommendation:** Add a `treeVisibility` field to the delegation lease with values: `full` (current behavior, default for backward compatibility), `self-only` (child sees only its own node), `parent-and-self` (child sees its own node and its direct parent's node, but not siblings). The parent controls sibling visibility when issuing the delegation lease. This is especially important for orchestrator patterns where different child agents should not be aware of each other's existence or runtime type.

---

### SEC-057. Task-Mode Credential File Deletion Race During Scrub [Medium]

**Section:** 5.2 (Lenny Scrub Procedure), line ~2015

Step 3b of the scrub procedure removes `/run/lenny/credentials.json`. Step 6 verifies that the file no longer exists. However, between step 1 (kill all user processes) and step 3b (remove credential file), there is a window where the credential file is still present on the tmpfs while the runtime process has been killed. If the `cleanupCommands` (which execute between step 1 and step 3b per the lifecycle description at line ~2008) are deployer-defined and potentially untrusted or buggy, they could read the previous task's credential file during this window. The `cleanupCommands` execute with access to "task state" (per the lifecycle description), and the credential file is part of that accessible state until step 3b removes it.

**Recommendation:** Move credential file removal (step 3b) to immediately after step 1 (kill all user processes) and before `cleanupCommands` execute. The credential file is a platform-managed security artifact, not deployer task state, and should be purged before any deployer code runs. Update the scrub verification (step 6) accordingly. If backward compatibility requires `cleanupCommands` to access credential metadata (e.g., for custom audit logging), expose a sanitized metadata field (provider name, lease ID) in the cleanup environment rather than leaving the full credential file accessible.

---

### SEC-058. Interceptor `MODIFY` Action Can Alter Delegation Metadata Fields [Medium]

**Section:** 4.8 (RequestInterceptor), line ~922; Section 8.3 (Delegation Policy), line ~3142

The `PreDelegation` interceptor phase invokes the referenced `RequestInterceptor` with the full `TaskSpec.input` as payload, and the interceptor can return `MODIFY` to alter the content. However, the design does not specify which fields of the delegation request are modifiable by an interceptor versus which are immutable platform-enforced fields. If the `MODIFY` response can alter fields beyond `TaskSpec.input` (e.g., `target`, `lease_slice`, `fileExport`), a misconfigured or compromised external interceptor could redirect delegations to different targets, inflate budget slices, or alter file export paths -- all of which bypass the `DelegationPolicy` evaluation that runs before or after the interceptor.

**Recommendation:** Explicitly enumerate the mutable field set for `PreDelegation` interceptors. At minimum: only `TaskSpec.input` (the text content) should be modifiable. The `target`, `lease_slice`, and `fileExport` fields must be immutable after `DelegationPolicyEvaluator` validation. The gateway should reject any `MODIFY` response that attempts to alter immutable fields, returning a `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` error. This is analogous to the immutable field enforcement already specified for `MODIFY` on interceptor updates (Section 4.8, line ~1010) but needs to be specified for the `PreDelegation` phase payload.

---

### SEC-059. Dev Mode `LENNY_DEV_MODE` Single-Gate Bypass Scope Is Underspecified [Low]

**Section:** 17.4 (Dev Mode Guard Rails), line ~8745

The design states that `LENNY_DEV_MODE` is "the single gate for all security relaxations in dev mode, including TLS bypass, JWT signing bypass, and any future relaxations." However, the design does not enumerate all security properties that are relaxed. Based on the document, at minimum the following are affected: (a) mTLS is disabled (plain HTTP), (b) JWT signing uses local HMAC-SHA256 instead of KMS, (c) the gateway startup assertion for TLS is bypassed. But it is unclear whether dev mode also relaxes: (d) `SO_PEERCRED` enforcement, (e) NetworkPolicy enforcement (likely not, as that is Kubernetes-level), (f) audit table grant verification (the non-production path logs warnings instead of fatal errors), (g) SIEM requirements. The lack of a comprehensive enumeration makes it difficult for security reviewers to assess the full attack surface of a dev-mode deployment and creates risk that future contributors add new relaxations under this flag without understanding the cumulative effect.

**Recommendation:** Add a "Security Relaxations Under `LENNY_DEV_MODE`" table in Section 17.4 that exhaustively lists every security control that behaves differently when the flag is set, the production behavior, the dev-mode behavior, and a cross-reference to the relevant section. This table should be maintained as a living document -- any PR that adds a new `LENNY_DEV_MODE` check should update this table.

---

### SEC-060. Concurrent-Workspace Slots Share Network Stack Without Per-Slot Network Isolation [Medium]

**Section:** 5.2 (Concurrent-Workspace Slot Isolation), line ~2075

In `concurrencyStyle: workspace` mode, all slots share the pod's network namespace. The design acknowledges this ("cross-slot isolation is process-level and filesystem-level -- explicitly weaker than session mode") and requires deployer acknowledgment via `acknowledgeProcessLevelIsolation`. However, the security implications of shared networking across concurrent slots are not fully analyzed. Specifically: (a) one slot can observe all other slots' network traffic via raw sockets (if available) or timing, (b) one slot can bind to ports that other slots expect to use, (c) one slot can make DNS queries that populate the resolver cache with poisoned entries visible to other slots, and (d) if slots belong to different tasks (same tenant), one task's network activity patterns are observable by the other. In multi-task-per-pod scenarios where different tasks within the same tenant have different sensitivity levels, this is a meaningful information disclosure vector.

**Recommendation:** Add the network-level side-channels (traffic observation, port binding conflicts, DNS cache poisoning between slots) to the explicit list of shared resources in the `acknowledgeProcessLevelIsolation` rejection message. Consider adding a `CAP_NET_RAW` drop (already likely via PodSecurityPolicy/PSA but worth making explicit) for the agent container to prevent raw socket traffic sniffing between slots. Document that deployers requiring network isolation between concurrent tasks should use `executionMode: session` instead.

---

### SEC-061. Memory Store `Query` Interface Lacks Query Injection Protection Specification [Low]

**Section:** 9.4 (Memory Store), line ~3981

The `MemoryStore.Query` method accepts a `query string` parameter that is used for semantic similarity search (pgvector). The design specifies tenant isolation via RLS and the `TenantID` validation, but does not address SQL injection via the `query` parameter. In the default Postgres + pgvector implementation, the query string is converted to an embedding vector and used in a vector similarity search (`<=>` operator). If the `Query` implementation concatenates the `query` string into SQL (rather than using parameterized queries), it is vulnerable to SQL injection. While this is an implementation concern rather than a design concern, the interface contract should specify that implementations MUST use parameterized queries and MUST NOT interpolate the `query` string into SQL.

**Recommendation:** Add to the `MemoryStore` interface contract: "Implementations MUST use parameterized queries for all database operations. The `query` string parameter MUST be passed as a bind parameter, never interpolated into SQL text. The `ValidateMemoryStoreIsolation` contract test should include a SQL injection canary test (e.g., a query containing `'; DROP TABLE memories; --`) that verifies the query executes without error and returns zero results rather than executing injected SQL."

---

### SEC-062. Lease Extension `auto` Mode Has No Per-Tree Rate Limit [Medium]

**Section:** 8.6 (Lease Extension), line ~3467

In `auto` approval mode, "each request is handled independently" with "no elicitation, no queuing, no cool-off." Combined with the fact that lease extensions are triggered automatically by the adapter when the LLM proxy rejects a call for budget exhaustion, this creates a scenario where a misbehaving runtime can rapidly consume the entire `maxExtendableBudget` without any human visibility. A compromised agent that generates rapid, budget-consuming LLM calls will trigger automatic extension requests, each of which is auto-granted up to the effective max. The `elicitation` mode provides natural rate-limiting (the cool-off window), but `auto` mode has no equivalent throttle. This means a runaway agent in `auto` mode can exhaust the full `maxExtendableBudget` in seconds, with the only signals being metrics and audit logs that may not be monitored in real time.

**Recommendation:** Add an optional `autoModeRateLimit` field to the lease extension configuration (per deployment/tenant/runtime, using the same layering) that limits the number of auto-approved extensions per time window (e.g., `maxAutoExtensionsPerMinute: 5`). When the rate limit is exceeded, the gateway should pause auto-approval and either queue the request until the window resets or fall back to `elicitation` mode for the remainder of the window. This provides a safety valve against runaway consumption in `auto` mode without requiring the full elicitation UX overhead for normal operation.

---

### SEC-063. `snapshotPolicyAtLease` Snapshot Does Not Cover `contentPolicy.interceptorRef` Changes [Low]

**Section:** 8.3 (Delegation Policy), line ~3169

When `snapshotPolicyAtLease: true` is set, the gateway snapshots the set of matching pool IDs at lease issuance time. However, the snapshot only covers pool-matching results (`snapshotted_pool_ids`), not the `contentPolicy.interceptorRef` binding. If the `DelegationPolicy`'s `contentPolicy.interceptorRef` is changed after the lease is issued (e.g., the interceptor is updated to a less restrictive version, or the referenced interceptor's `failPolicy` is changed from `fail-closed` to `fail-open`), subsequent delegations within the snapshotted tree will use the updated interceptor -- not the interceptor configuration at snapshot time. This partially defeats the purpose of `snapshotPolicyAtLease` for deployments that want fully stable policy behavior within a tree.

**Recommendation:** Document this limitation explicitly in the `snapshotPolicyAtLease` description: "The snapshot covers pool-matching results only. `contentPolicy.interceptorRef` resolution is always live -- interceptor configuration changes after lease issuance affect all subsequent delegations in the tree." If full snapshot semantics are desired, recommend that deployers use versioned interceptor names (e.g., `content-scanner-v2`) and update `DelegationPolicy` references to new versions rather than modifying existing interceptors in-place.

---

### SEC-064. Abstract Unix Socket Namespace Collision Between Co-Located Lenny Instances [Low]

**Section:** 4.7 (Runtime Adapter), line ~833

The adapter uses abstract Unix sockets with fixed names (`@lenny-platform-mcp`, `@lenny-lifecycle`, `@lenny-connector-{id}`). Abstract Unix sockets exist in the kernel's network namespace, not the filesystem namespace. In the sidecar deployment model with `shareProcessNamespace: false`, each container has its own network namespace, so there is no collision risk between the adapter and agent containers. However, the design does not address the case where two Lenny pods are co-scheduled on the same node and share a network namespace (which can happen with `hostNetwork: true` or certain CNI configurations). In this scenario, abstract socket names would collide. While this is an unlikely deployment configuration, the design should explicitly state the assumption.

**Recommendation:** Add a note in the Deployment Model section: "Abstract Unix socket names are scoped to the pod's network namespace. Pods MUST NOT use `hostNetwork: true`. The `SandboxWarmPool` CRD validation webhook should reject pool configurations that set `hostNetwork: true` on the pod template, as this would cause abstract socket name collisions between co-located pods and violate the network isolation model (Section 13.2)."

---


| ID | Title | Severity |
|---|---|---|
| SEC-054 | `callbackSecret` Storage and Lifecycle Lacks Specificity | Medium |
| SEC-055 | Semantic Cache Timing Side-Channel Across Users | Medium |
| SEC-056 | `lenny/get_task_tree` Exposes Sibling Metadata Without Scoping | Medium |
| SEC-057 | Task-Mode Credential File Deletion Race During Scrub | Medium |
| SEC-058 | Interceptor `MODIFY` Can Alter Delegation Metadata Fields | Medium |
| SEC-059 | Dev Mode Single-Gate Bypass Scope Underspecified | Low |
| SEC-060 | Concurrent-Workspace Slots Share Network Without Per-Slot Isolation | Medium |
| SEC-061 | Memory Store Query Lacks Injection Protection Spec | Low |
| SEC-062 | Lease Extension `auto` Mode Has No Per-Tree Rate Limit | Medium |
| SEC-063 | `snapshotPolicyAtLease` Does Not Cover `interceptorRef` Changes | Low |
| SEC-064 | Abstract Unix Socket Namespace Collision | Low |

---

## 3. Network Security (NET)

### NET-044. `provider-direct` Egress Profile Missing IMDS `except` Clauses [Medium]

**Section:** 13.2 (egressProfile enum, NET-002 hardening note)

The `internet` profile's hardening note (NET-002) explicitly states that "the `0.0.0.0/0` CIDR rule in the `internet` profile (and any other supplemental policy containing broad CIDR rules) includes `except` entries for all cloud instance metadata service (IMDS) addresses." However, the `provider-direct` profile uses deployer-supplied CIDRs from `egressCIDRs.providers`, and there is no guidance or validation that these CIDRs must not overlap with IMDS link-local ranges or that IMDS `except` clauses should be applied to `provider-direct` NetworkPolicies as well.

While deployer-supplied provider CIDRs are typically narrow public ranges, there is no preflight or Helm-time validation to reject IMDS-overlapping CIDRs. On AWS in particular, some VPC endpoint addresses for services like Bedrock may be routed through the VPC's private CIDR, and a carelessly broad deployer CIDR (e.g., `169.254.0.0/16` for a link-local service endpoint) could inadvertently allow IMDS access.

**Recommendation:** Add a Helm-time validation rule (and preflight check) that rejects any entry in `egressCIDRs.providers` whose CIDR range overlaps with any address in `egressCIDRs.excludeIMDS`. Alternatively, unconditionally include IMDS `except` clauses on all supplemental egress NetworkPolicies (including `provider-direct`), not just those using `0.0.0.0/0`.

---

### NET-045. No Prometheus/Monitoring Ingress NetworkPolicy for `lenny-system` Components [Medium]

**Section:** 13.2 (lenny-system namespace NetworkPolicies, NET-017)

The `lenny-system` namespace enforces a default-deny NetworkPolicy. The component-specific allow-lists (the table at line 5965) enumerate egress and ingress rules for each component, but no component's ingress rules include Prometheus scrape traffic. The gateway exposes `/metrics`, the dedicated CoreDNS exposes `prometheus :9153` (line 6165), controllers presumably expose `/metrics`, yet there is no ingress rule allowing the monitoring namespace (e.g., `prometheus-system`, `monitoring`) to reach any `lenny-system` pod on their metrics ports.

Under the default-deny policy, Prometheus scrape requests from the monitoring namespace will be silently dropped. This means all `lenny-system` component metrics -- including the critical `lenny_gateway_active_sessions`, `lenny_network_policy_cidr_drift_total`, `DedicatedDNSUnavailable` signal metrics, and HPA-driving metrics like `lenny_gateway_request_queue_depth` -- will be unscrapeable, breaking the entire observability and autoscaling pipeline.

**Recommendation:** Add a `{{ .Values.monitoringNamespace }}` Helm value (default: `monitoring`) and render supplemental ingress NetworkPolicy rules in `lenny-system` allowing the monitoring namespace to reach each component's metrics port (gateway: `/metrics` port, CoreDNS: 9153, controllers: `/metrics` port). Include this in the component-specific allow-list table. Validate at preflight that the monitoring namespace exists and has running Prometheus pods.

---

### NET-046. Agent Pod OTLP Egress Not Covered by NetworkPolicy [Medium]

**Section:** 13.2 (allow-pod-egress-base), 5.2 (Execution Modes)

Section 5.2 states that "graph-aware runtimes may emit OpenTelemetry spans using their own OTel SDK configured against the OTLP collector endpoint injected in the adapter manifest as `observability.otlpEndpoint`." This means agent pods need network egress to the OTLP collector (typically port 4317 gRPC or 4318 HTTP in an observability namespace or `lenny-system`).

However, the `allow-pod-egress-base` NetworkPolicy permits only gateway gRPC (port 50051) and dedicated CoreDNS (port 53). There is no supplemental egress rule allowing agent pods to reach the OTLP collector. Under default-deny, all OTLP trace/span exports from agent pods will be silently dropped.

**Recommendation:** Either (a) add a conditional supplemental egress NetworkPolicy for agent namespaces allowing traffic to the OTLP collector endpoint (with a `{{ .Values.observability.otlpNamespace }}` and `{{ .Values.observability.otlpPort }}` Helm value), rendered only when `observability.otlpEndpoint` is configured, or (b) route agent pod trace export through the gateway's gRPC control channel (avoiding the need for a new network path, at the cost of additional gateway load). Document the chosen approach in Section 13.2.

---

### NET-047. Controller kube-apiserver Egress Missing CIDR Constraint [Low]

**Section:** 13.2 (lenny-system component-specific allow-lists)

The gateway's kube-apiserver egress rule explicitly references a CIDR: `kube-apiserver (TCP 443, CIDR {{ .Values.kubeApiServerCIDR }})`. The Warm Pool Controller / PoolScalingController entry lists `kube-apiserver (TCP 443)` with no CIDR reference. If the Helm-rendered NetworkPolicy for the controller uses `0.0.0.0/0` on port 443 (or no `ipBlock` at all), the controller gains unrestricted TCP 443 egress to any destination, which is significantly broader than intended.

The controller should be constrained to the same `kubeApiServerCIDR` as the gateway to prevent a compromised controller from making arbitrary HTTPS connections to the internet or internal services on port 443.

**Recommendation:** Add `CIDR {{ .Values.kubeApiServerCIDR }}` to the controller's kube-apiserver egress rule in the component-specific allow-list table, matching the gateway's configuration. Ensure the Helm chart renders the same CIDR constraint for both components.

---

### NET-048. Dedicated CoreDNS Ingress Rule in `lenny-system` Lacks Explicit Agent Namespace Scope [Low]

**Section:** 13.2 (lenny-system component-specific allow-lists, dedicated CoreDNS row)

The dedicated CoreDNS component's ingress column states: `Agent namespace pods (UDP/TCP 53, per allow-pod-egress-base in agent namespaces)`. This references the egress rule in agent namespaces but does not specify the corresponding **ingress** NetworkPolicy in `lenny-system` that permits agent namespace pods to reach the CoreDNS pods.

The agent namespace egress policy allows DNS traffic to pods in `lenny-system` labeled `lenny.dev/component: coredns`. For this to work, the `lenny-system` default-deny must have a matching ingress rule on the CoreDNS pods that allows ingress from agent namespaces. The component table implies this exists, but unlike the gateway (which has a fully rendered YAML example for `allow-gateway-ingress` and `allow-ingress-controller-to-gateway`), the CoreDNS ingress rule has no YAML specification. Without it, the default-deny in `lenny-system` blocks all inbound DNS queries from agent pods, silently breaking DNS resolution for every agent pod.

**Recommendation:** Add an explicit YAML example (or at minimum a structured specification) for the `allow-agent-dns-ingress` NetworkPolicy in `lenny-system` that allows ingress on UDP/TCP 53 to pods labeled `lenny.dev/component: coredns` from agent namespaces (using `namespaceSelector` matching `.Values.agentNamespaces`). This ensures implementers render the correct bidirectional policy.

---

### NET-049. Corefile `forward . /etc/resolv.conf` Relies on Implicit Pod DNS Configuration [Low]

**Section:** 13.2 (Reference Corefile)

The dedicated CoreDNS Corefile uses `forward . /etc/resolv.conf`, which forwards queries to whatever nameserver is in the CoreDNS pod's own `/etc/resolv.conf`. The design note on line 6065 states that `lenny-system` components use `kube-system` CoreDNS, implying the CoreDNS pod uses the default `ClusterFirst` dnsPolicy, which means `/etc/resolv.conf` points at the `kube-dns` Service ClusterIP in `kube-system`.

However, this forwarding target is implicit and fragile. If a deployer or automation accidentally sets `dnsPolicy: None` on the dedicated CoreDNS Deployment (or if a security-hardening tool applies blanket `dnsPolicy: None` to all pods in `lenny-system`), the forward target would be missing or wrong, silently breaking all agent pod DNS resolution with no clear error path.

**Recommendation:** Replace `forward . /etc/resolv.conf` with an explicit upstream directive: `forward . {{ .Values.coredns.upstreamDNS }}` (defaulting to the `kube-dns` Service ClusterIP, e.g., `10.96.0.10`). This makes the forwarding target explicit, self-documenting, and immune to accidental pod-spec changes. Add a Helm validation that `coredns.upstreamDNS` is non-empty.

---

---

## 4. Scalability & Performance (SCL)

### SCL-049. Periodic Checkpoint Thundering Herd at Tier 3 [Medium]

**Section:** 4.4, 17.8.2

**Description:** The periodic checkpoint interval (`periodicCheckpointIntervalSeconds`, default: 600s) is applied to all active sessions, but the spec does not describe any jitter or staggering mechanism. At Tier 3, 10,000 concurrent sessions all started within a similar time window will have their periodic checkpoints cluster around the same wall-clock second. With a 600s interval, the steady-state checkpoint rate is quoted as ~17/s (Section 17.8.2), but that assumes uniform distribution. In practice, sessions that start in the same burst (e.g., 200/s creation rate) will align their first checkpoint at T+600s, producing a burst of up to 200 x burst_duration checkpoints within a narrow window. This burst concentrates MinIO write load and gateway I/O far above the ~17/s steady-state estimate, potentially exceeding the MinIO throughput budget and triggering `CheckpointDurationHigh` alerts.

**Recommendation:** Add per-session jitter to the periodic checkpoint scheduler: each session's first checkpoint should be scheduled at `periodicCheckpointIntervalSeconds + random(0, jitter_range)` seconds after session start, where `jitter_range` defaults to `periodicCheckpointIntervalSeconds * 0.2` (120s). This spreads the checkpoint load uniformly across the interval window and keeps the steady-state rate close to the ~17/s estimate. Document the jitter range as a tunable (`periodicCheckpointJitterFraction`, default: 0.2).

---

### SCL-050. PgBouncer Backend Connection Budget Does Not Account for Separate Audit Write Pool [Medium]

**Section:** 12.3, 17.8.2

**Description:** Section 12.3 states that T3/T4 audit events use synchronous writes via "a separate goroutine with a dedicated connection pool (`audit.syncWritePoolSize`, default: 4 connections)." Each gateway replica therefore requires connections from at least two distinct pools: the main session/state pool and the audit sync write pool. At Tier 3 with up to 30 gateway replicas, the audit pool alone consumes 30 x 4 = 120 PgBouncer frontend connections (potentially mapped to 120 backend connections since each is a separate pool/user pair). The PgBouncer sizing in Section 17.8.2 specifies `default_pool_size: 60` per PgBouncer replica (4 replicas = 240 backend connections), plus `reserve_pool_size: 15` (4 x 15 = 60 reserve). However, the sizing guidance in Section 12.3 says to set `default_pool_size` to "approximately `max_connections / number_of_PgBouncer_replicas`" -- i.e., 500/4 = 125 -- yet the Tier 3 table says 60. This inconsistency (125 vs. 60) means the Tier 3 PgBouncer `default_pool_size` is undersized relative to its own sizing formula. Furthermore, the formula does not account for the audit pool's additional backend connections, which may push total backend connections above `max_connections`.

**Recommendation:** (1) Reconcile the Tier 3 `default_pool_size` (60) with the sizing formula (`max_connections / pgbouncer_replicas` = 125). (2) Add explicit per-tier guidance that accounts for the audit sync write pool's connection consumption. (3) Consider whether the audit sync write pool should be routed through PgBouncer or needs its own PgBouncer database entry with a dedicated `default_pool_size` allocation.

---

### SCL-051. Tier 3 Gateway Scale-Down Time Calculation Is Incorrect [Low]

**Section:** 17.8.2

**Description:** The "Gateway scale-down time" row in the gateway table states "8.3 min (30 -> 5, 3 pods/60s)" for Tier 3. The math: (30 - 5) / 3 = 8.33 periods, at 60s each = 500s = 8.33 min. However, Section 10.1 specifies `behavior.scaleDown.stabilizationWindowSeconds: 300` (5 minutes). The 300s stabilization window delays the first scale-down action, meaning actual max-to-min time is 300s + 500s = 800s = 13.3 min, not 8.3 min. The Tier 1 and Tier 2 calculations similarly omit the stabilization window (Tier 1: 2 min stated, actual 7 min; Tier 2: 7 min stated, actual 12 min).

**Recommendation:** Revise the "Gateway scale-down time" row to either (a) include the stabilization window in the total, or (b) add a footnote clarifying the time excludes the 300s stabilization window. Ensure the stated values are consistent with the stabilization policy so operators can accurately predict scale-down behavior.

---

### SCL-052. Tier 3 Warm Pool minWarm of 1,050 Does Not Survive Controller Failover [Medium]

**Section:** 17.8.2

**Description:** The warm pool sizing formula uses `failover_seconds = 25` (worst-case controller crash: `leaseDuration + renewDeadline = 15s + 10s`). During this 25s window, no new pods are created. The recommended Tier 3 baseline `minWarm` of 1,050 is intended to absorb session creation during failover: `claim_rate * (failover_seconds + pod_startup_seconds) = 30 * (25 + 10) = 1,050`. This means the pool is sized to absorb exactly 0% headroom above the expected demand during a controller failover. A single claim above the expected rate during the 35-second window exhausts the pool. Additionally, the note in Section 17.8.2 explicitly states that the recommended values use `safety_factor = 1.0` (no margin), while the table separately lists a Tier 3 safety factor of 1.2. This creates a confusing dual-track: the recommended value (1,050) has no safety margin, but the formula says to apply 1.2. A deployer reading only the table would be under-provisioned for any variance.

**Recommendation:** Either (a) apply the Tier 3 safety factor (1.2) to the recommended table value, yielding `ceil(30 * 1.2 * 35) = 1,260` as the baseline, or (b) add an explicit warning row in the minWarm table indicating "0% margin; apply safety factor for production." The current presentation risks operators using 1,050 as a production value when the formula's own safety factor recommends 1,260.

---

### SCL-053. MinIO Throughput Budget Assumes All Checkpoint Uploads Are Full Workspace Snapshots [Low]

**Section:** 17.8.2

**Description:** The MinIO throughput budget table estimates checkpoint bandwidth based on the assumption that every checkpoint uploads the full workspace tar. At Tier 3 with average 100 MB workspaces, this yields ~1.7 GB/s sustained. The spec mentions incremental checkpoints as a "deferred" future optimization (Section 4.4). However, the throughput budget does not account for the composition of checkpoint traffic: periodic checkpoints of idle sessions (no workspace changes since the last checkpoint) still upload the full tar under the current design. At Tier 3 with 10,000 sessions and a 600s interval, many of those ~17 checkpoints/s will be for sessions that have had zero workspace changes, contributing to wasted MinIO I/O. Meanwhile, the "Minimum MinIO aggregate throughput (sustained, avg workspace)" row lists 5 GB/s -- which is 3x the estimated 1.7 GB/s rate. The 3x multiplier is not explained.

**Recommendation:** (1) Document the 3x multiplier rationale (is it headroom? concurrent read + write? GC deletions?). (2) Consider whether periodic checkpoints should be skipped when the workspace is unchanged since the last successful checkpoint (a content-hash comparison, or at minimum an mtime check on `/workspace/current`), which could dramatically reduce MinIO load at Tier 3. This is cheaper than full incremental checkpoints and can be implemented in v1.

---

### SCL-054. Quota Checkpoint Flush Instantaneous IOPS Spike Not Quantified at Tier 3 [Medium]

**Section:** 12.3

**Description:** Section 12.3 acknowledges that "the Redis -> Postgres periodic quota checkpoint sync produces bursty writes" and states "At Tier 3 (10,000 sessions), the instantaneous IOPS spike at each flush boundary may significantly exceed the steady-state ~100/s estimate." However, no quantification is provided for this spike. With `quotaSyncIntervalSeconds: 10` at Tier 3 and 10,000 active sessions, the worst case (all sessions accumulating quota changes simultaneously) could produce up to 10,000 quota writes in a single flush pass. Even with batching, this is a spike of potentially thousands of IOPS landing within a 1-2 second window every 10 seconds. The margin between sustained load (~1,300/s) and instance ceiling (~1,600/s) is described as "approximately 18%", leaving only ~300 IOPS of headroom -- far below the potential quota flush spike.

**Recommendation:** (1) Quantify the expected peak flush spike at each tier (e.g., `min(active_sessions, batch_size) / flush_duration`). (2) Specify whether the quota flush uses batched multi-row inserts or individual per-session writes. (3) If individual writes, add explicit guidance that Tier 3 deployments should batch quota flushes (multi-row `INSERT ... ON CONFLICT UPDATE`) to cap the instantaneous spike. (4) Consider staggering quota flushes across the sync interval (similar to the checkpoint jitter recommendation) so not all gateway replicas flush simultaneously.

---

### SCL-055. HPA Target CPU Utilization Differs Between Section 4.1 and Section 17.8.2 [Low]

**Section:** 4.1, 17.8.2

**Description:** Section 4.1 defines `HPA target utilization` as 80% for all three tiers in the `maxSessionsPerReplica` table. Section 17.8.2 defines `HPA target CPU utilization` as 70% (Tier 1), 65% (Tier 2), and 60% (Tier 3). While these technically refer to different things (Section 4.1's "target utilization" is ambiguous and could refer to the session budget metric, while 17.8.2 is explicit about CPU), the shared "HPA target utilization" label and the fact that both tables are authoritative sizing references creates confusion. An operator reading Section 4.1 alone might configure CPU HPA at 80% when 17.8.2 recommends 60% at Tier 3.

**Recommendation:** Clarify in Section 4.1 that the "HPA target utilization" column refers to the session budget alert threshold (capacity ceiling), not the HPA CPU target. Add a cross-reference to Section 17.8.2 for the canonical HPA CPU utilization targets. Alternatively, rename the Section 4.1 column to "Session budget alert threshold" to avoid ambiguity.

---

### SCL-056. Concurrent-Workspace Pod Slot Counter Rehydration Is a Blocking Operation With Unspecified Latency [Medium]

**Section:** 10.1, 5.2

**Description:** Section 10.1 states: "The gateway atomically resets the Redis slot counter (`lenny:pod:{pod_id}:active_slots` -> 0) and rehydrates it from `SessionStore.GetActiveSlotsByPod(pod_id)` on the replacement pod's first slot allocation after recovery." Section 12.4 similarly states that after a Redis restart, slot counters are "rehydrated from `SessionStore.GetActiveSlotsByPod(pod_id)` on first slot allocation post-recovery before accepting new slot assignments." This rehydration requires a Postgres query for each pod being assigned a slot, occurring on the hot path (first slot allocation). At Tier 3 with concurrent-workspace pools, a Redis restart could trigger simultaneous rehydration queries for hundreds of pods, creating a Postgres query burst. The rehydration query's latency and the blocking behavior during rehydration are not specified.

**Recommendation:** (1) Specify whether rehydration blocks only the single pod's first slot allocation or all slot allocations globally during rehydration. (2) Add a latency budget for the rehydration query and consider pre-warming the slot counters proactively on Redis recovery (background sweep of all active concurrent-workspace pods) rather than lazily on first allocation.

---

### SCL-057. Orphan Session Reconciler at 60-Second Interval Creates Detection Lag at Tier 3 [Low]

**Section:** 10.1

**Description:** The orphan session reconciler runs every 60 seconds and cross-references the `agent_pod_state` mirror table. At Tier 3 with 10,000 active sessions, each reconciliation pass must join sessions in non-terminal states against the pod state mirror. With a 60-second interval and no incremental processing, each pass potentially scans thousands of rows. However, more importantly, the 60-second detection delay means that up to 60 seconds of quota (session budget, warm pool slots, credential leases) is held by orphaned sessions before being reclaimed. At Tier 3, with 200 sessions/s creation rate, a 60-second detection lag means up to 200 sessions could be queued behind exhausted quotas that are being held by orphans. The impact is multiplicative when combined with a node failure that terminates multiple pods simultaneously.

**Recommendation:** Consider reducing the reconciler interval to 15-30s at Tier 3, or switching to a watch-based approach on the `agent_pod_state` mirror (triggering reconciliation immediately when a pod transitions to `Terminated`). Add per-tier reconciler interval guidance to the controller tuning table in Section 17.8.2.

---

### SCL-058. Gateway preStop Stage 2 Tiered Checkpoint Cap Does Not Account for Concurrent Sessions [Medium]

**Section:** 10.1

**Description:** The preStop hook's Stage 2 ("Wait for in-flight checkpoints") uses a tiered cap per session (30s / 60s / 90s based on workspace size). The spec states the gateway "waits for those checkpoints to complete." At Tier 3, a single gateway replica can coordinate up to 400 sessions (`maxSessionsPerReplica`). If a rolling update triggers the preStop hook, the gateway must complete CheckpointBarrier flushes for all coordinated sessions. The tiered cap applies per-session, but it is unclear whether sessions are checkpointed sequentially or in parallel during drain. If sequential, 400 sessions at 30s each = 12,000s, far exceeding `terminationGracePeriodSeconds`. If parallel, the concurrent MinIO uploads would produce a significant I/O spike. The spec says `checkpointBarrierAckTimeoutSeconds` (default: 90s) governs the wait, but this appears to be a single timeout for all sessions, not per-session. The CRD validation formula (`max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds`) adds the two together, suggesting they are sequential phases, not per-session multiplied values. This needs clarification to prevent operators from miscalculating the grace period budget.

**Recommendation:** Clarify that the CheckpointBarrier protocol issues barriers to all coordinated pods in parallel (not sequentially) and that `checkpointBarrierAckTimeoutSeconds` is a single wall-clock deadline for all pods to acknowledge (not per-pod). If this is already the intent, add a note about the resulting MinIO I/O spike during gateway drain at Tier 3 (up to 400 concurrent checkpoint uploads) and ensure the MinIO throughput budget accounts for drain-triggered checkpoint bursts.

---

### SCL-059. Billing Redis Stream MAXLEN of 50,000 May Be Insufficient for Sustained Postgres Outage at Tier 3 [Medium]

**Section:** 12.3, 12.4

**Description:** Section 12.4 defines the billing Redis stream with `MAXLEN 50,000` and `TTL 3600s`. Section 12.3 states billing event rate at Tier 3 is ~600/s. At 600 events/s, the 50,000-entry stream is filled in ~83 seconds. If Postgres is unavailable for longer than 83 seconds (which the spec itself accommodates -- the Postgres failover RTO is "< 30s" for HA, but the Postgres fallback retry budget for eviction is 60s, and `dualStoreUnavailableMaxSeconds` is 60s), the stream will hit MAXLEN and the oldest entries will be evicted before they can be flushed to Postgres. Meanwhile, the `BillingStreamBackpressure` alert fires at 80% (40,000 entries, hit at ~67 seconds). The `billingStreamTTLSeconds` of 3600s is irrelevant because MAXLEN will trigger eviction far sooner than the TTL at Tier 3 rates.

**Recommendation:** (1) Scale `billingRedisStreamMaxLen` with tier: Tier 3 should default to at least `billing_rate * postgres_rto_seconds * safety_factor = 600 * 60 * 2 = 72,000` entries. (2) Add per-tier `billingRedisStreamMaxLen` defaults to the Section 17.8.2 operational defaults table. (3) Note that the 50,000 default is appropriate only for Tier 1/2 rates and will cause billing data loss at Tier 3 during any Postgres outage exceeding ~83 seconds.

---

That concludes the SCL iteration 3 findings: 11 new findings (SCL-049 through SCL-059), comprising 0 High, 5 Medium, and 6 Low severity issues.

**Summary of key themes:**
- **Thundering herd / synchronization effects** (SCL-049 periodic checkpoint storms, SCL-054 quota flush spikes) -- multiple systems use fixed intervals without jitter, creating correlated burst patterns at Tier 3 scale.
- **Tier 3 connection/capacity math inconsistencies** (SCL-050 PgBouncer sizing, SCL-051 scale-down time, SCL-052 warm pool margin, SCL-055 HPA utilization label mismatch, SCL-059 billing stream MAXLEN).
- **Hot-path blocking during recovery** (SCL-056 slot counter rehydration, SCL-057 orphan reconciler lag).
- **Underspecified concurrent behavior** (SCL-053 throughput multiplier, SCL-058 preStop parallel checkpoint semantics).

---

## 5. Protocol Design (PRT)

### PRT-047. A2A `SupportedEventKinds` Omits `tool_use` Without Justification [Low]

**Section:** 21.1 (A2A Full Support) / 15 (`OutboundCapabilitySet`)

The `OutboundCapabilitySet` interface definition (line 6525) lists six well-known event kinds: `"state_change"`, `"output"`, `"elicitation"`, `"tool_use"`, `"error"`, `"terminated"`. The A2A adapter's declared `SupportedEventKinds` (line 9804) includes only four: `["state_change", "output", "error", "terminated"]`. The omission of `"elicitation"` is justified by the elicitation suppression design in Section 21.1, but the omission of `"tool_use"` is not explained. A2A callers that subscribe to push notifications will not receive tool-use events, which may be relevant for observability-oriented A2A consumers (e.g., an orchestrating A2A agent monitoring a child's tool activity). If `tool_use` is intentionally omitted because A2A has no equivalent concept, this should be documented alongside the `elicitation` suppression rationale.

**Recommendation:** Either add `"tool_use"` to the A2A adapter's `SupportedEventKinds` (mapping to an A2A-compatible event format), or document the intentional omission with rationale in Section 21.1 alongside the elicitation suppression explanation.

---

### PRT-048. `one_shot` Interaction Mode Allows One `request_input` but Protocol Mapping Table Does Not Account for This [Medium]

**Section:** 5.1 (Runtime) / 8.8 (TaskRecord and TaskResult Schema)

Section 5.1 (line 1657) states that a `one_shot` runtime "May use `lenny/request_input` once (for a single clarification). Second call returns a gateway error." This means a `one_shot` task can legitimately enter the `input_required` state. However, the protocol mapping table in Section 8.8 (line 3591) maps `input_required` to MCP `input_required` and A2A `input-required` without distinguishing behavior for `one_shot` runtimes. An MCP or A2A client receiving `input_required` from a `one_shot` task has no signal that only a single round of input is permitted -- the client might attempt multiple rounds of input, which would fail opaquely on the second attempt. The `one_shot` constraint is a Lenny-internal enforcement that has no surface in the external protocol mapping.

**Recommendation:** Add a protocol-visible signal for the `one_shot` constraint. For example, include a `maxInputRounds: 1` field in the `input_required` event metadata surfaced to MCP/A2A clients, or document in the protocol mapping table that `one_shot` tasks emit `input_required` with a metadata annotation indicating it is the final permitted input request.

---

### PRT-049. `MessageEnvelope.from.kind` Closed Enum Missing `"parent"` for Delegation Messages [Low]

**Section:** 15.4.1 (`MessageEnvelope`) / 8.8 (Delegation)

The `from.kind` enum (line 6542) has exactly four values: `client`, `agent`, `system`, `external`. When a parent agent sends a message to a child via `lenny/send_message`, the child receives a `MessageEnvelope` with `from.kind: "agent"` and `from.id: "sess_{parent_session_id}"`. This is technically correct but semantically ambiguous: the child cannot distinguish a message from its direct parent (which has delegation authority over it) from a message from a sibling or unrelated agent that happens to have messaging scope. The `delegationDepth` field provides some context but does not identify the sender's relationship in the tree. For runtimes that need to apply different trust levels to parent-originated vs. peer-originated messages, the current enum is insufficient.

**Recommendation:** This is a minor design gap acceptable for v1. Document in the `from.kind` table that delegation-sourced messages use `kind: "agent"` and that runtimes should use `delegationDepth` combined with `from.id` cross-referenced against the task tree (via `lenny/get_task_tree`) to determine the sender's relationship.

---

### PRT-050. `/.well-known/agent.json` Array Extension Breaks A2A Discovery Client Expectations Silently [Medium]

**Section:** 21.1 (A2A Full Support) / 15.1 (REST API)

Section 21.1 (line 9790) acknowledges that returning a JSON array from `/.well-known/agent.json` is an "intentional Lenny extension" to the A2A spec, which requires a single `AgentCard` object. The spec correctly provides per-runtime endpoints (`/a2a/runtimes/{name}/.well-known/agent.json`) for standard clients. However, the failure mode for standard A2A clients hitting the aggregated endpoint is not specified. A standard A2A client that issues `GET /.well-known/agent.json` and receives a JSON array instead of a JSON object will likely fail with a deserialization error. The spec does not define a `Content-Type` hint, a `Link` header pointing to per-runtime endpoints, or any degradation path for non-Lenny-aware clients.

**Recommendation:** Add a `Link` header to the aggregated endpoint response pointing to the per-runtime discovery pattern (e.g., `Link: </a2a/runtimes/{name}/.well-known/agent.json>; rel="item"`), and consider returning `300 Multiple Choices` with a `Link` header instead of `200` with an array, so standard A2A clients get a meaningful HTTP-level signal that the response is not a single agent card. Alternatively, document that standard A2A clients MUST NOT use the aggregated endpoint and that DNS/reverse-proxy configurations should route `/.well-known/agent.json` to a single runtime when cross-organization discovery is needed.

---

### PRT-051. Intra-Pod MCP Nonce in `params._lennyNonce` Violates MCP Spec's `initialize` Schema [Medium]

**Section:** 15.4.3 (Standard-Tier MCP Integration)

The spec places a `_lennyNonce` field at `params._lennyNonce` in the MCP `initialize` request (line 7874-7888). The MCP specification defines the `initialize` request's `params` object with a specific schema (`clientInfo`, `protocolVersion`, `capabilities`). Injecting a custom `_lennyNonce` field into `params` may be rejected by strict MCP client libraries that validate outgoing request schemas or by the MCP spec's `additionalProperties: false` constraint if present in the schema definition. The spec acknowledges this is a "stopgap" (line 7891) and plans a v2 pre-initialize handshake, but the v1 approach may cause interoperability issues with off-the-shelf MCP client libraries that enforce strict schema compliance.

**Recommendation:** The spec already documents the v2 migration path (pre-initialize out-of-band handshake). For v1, add explicit guidance that runtime authors using strict MCP client libraries may need to bypass schema validation for the `initialize` call, or implement the nonce delivery as a separate pre-initialize JSON line (effectively adopting the v2 approach early). Document whether the `_lennyNonce` field is preserved or stripped before the adapter's MCP server processes the `initialize` request -- if the adapter's MCP server implementation validates against the MCP schema, the nonce must be stripped before dispatching.

---

### PRT-052. `expired` Task State Maps to `failed` in Both MCP and A2A, Losing Expiry Semantics [Low]

**Section:** 8.8 (TaskRecord and TaskResult Schema)

The protocol mapping table (line 3598) maps Lenny's `expired` state to MCP `failed` + error code and A2A `failed` + error metadata. Line 3601 confirms: "`expired` has no direct equivalent in MCP or A2A; adapters surface it as `failed`/`canceled` with a structured error code indicating the expiry reason." The table says `failed` but the prose says `failed`/`canceled`. This creates ambiguity: does `expired` map to `failed` (as the table says) or can it also map to `canceled` (as the prose says)? For lease-exhaustion expiry (budget exceeded), `failed` is appropriate. For deadline expiry (time-based), `canceled` might be more semantically correct since the task did not error -- it was preempted. The inconsistency between the table and the prose leaves adapter implementers without a deterministic mapping rule.

**Recommendation:** Resolve the table/prose inconsistency. If `expired` always maps to `failed` (with a structured error code carrying the expiry reason), remove the `canceled` option from the prose. If the mapping depends on the expiry reason (budget vs. deadline), document the per-reason sub-mapping explicitly in the table.

---

### PRT-053. Translation Fidelity Matrix Missing Inbound Translation Direction [Medium]

**Section:** 15.4.1 (Translation Fidelity Matrix)

The Translation Fidelity Matrix (lines 7477-7488) documents field-level fidelity for each adapter but does not consistently distinguish between outbound (OutputPart -> wire format) and inbound (wire format -> OutputPart) translation fidelity. For example, the `id` field for OpenAI Completions is marked `[dropped]` with the note "adapter generates new IDs on ingest" -- this conflates outbound behavior (ID not sent on wire) with inbound behavior (new ID generated). The matrix appears to primarily document the outbound direction, but several entries (e.g., `schemaVersion` "re-added with default on ingest") describe inbound behavior. The "Round-trip asymmetry summary" table (lines 7492-7498) partially addresses this but only for fields with asymmetric round-trips. An adapter author implementing a new `ExternalProtocolAdapter` would need to infer inbound translation rules from the round-trip descriptions rather than having them stated directly.

**Recommendation:** Add a companion inbound translation column to the matrix (or a separate inbound matrix) documenting how each adapter reconstructs `OutputPart` fields from its wire format. This is particularly important for the `RegisterAdapterUnderTest` compliance suite, where third-party adapter authors need deterministic specifications for both directions.

---

### PRT-054. `AdapterCapabilities` Struct Missing `SupportsStreaming` Field [Low]

**Section:** 15 (External API Surface) / `AdapterCapabilities` struct

The `AdapterCapabilities` struct (lines 6483-6512) declares `SupportsSessionContinuity`, `SupportsDelegation`, `SupportsElicitation`, and `SupportsInterrupt`. There is no `SupportsStreaming` field, despite streaming being a fundamental behavioral difference between adapters. The OpenAI Completions adapter uses SSE streaming for token-by-token delivery; the REST adapter does not stream; the MCP adapter uses Streamable HTTP. A client inspecting `adapterCapabilities` in a discovery response (as mandated by line 6604) cannot determine whether the adapter supports streaming responses without attempting a streaming request and observing the behavior. This is relevant for callers that need to decide between adapters based on streaming support.

**Recommendation:** Add a `SupportsStreaming bool` field to `AdapterCapabilities` so discovery consumers can programmatically select adapters that support streaming delivery.

---

### PRT-055. `schemaVersion` on `TaskRecord` Immutable Once Created Conflicts with Rolling Upgrade Scenario [Medium]

**Section:** 8.8 (TaskRecord and TaskResult Schema) / 15.5 (API Versioning)

Section 8.8 (line 3569) states: "the top-level `schemaVersion` is immutable once the record is created (set by the first writer, per Section 15.5 item 7)." This creates a subtle issue during rolling gateway upgrades. Consider: gateway replica A (running version N) creates a `TaskRecord` with `schemaVersion: 1`. During a rolling upgrade, gateway replica B (version N+1, which knows about `schemaVersion: 2`) writes a new message entry to the same `TaskRecord`. The message entry's `OutputPart` objects may use `schemaVersion: 2` fields. The `TaskRecord` envelope remains at `schemaVersion: 1` because it is immutable. This is documented as intentional (the two-level versioning model handles it). However, if schema version 2 of the `TaskRecord` envelope itself introduces new envelope-level fields (e.g., a new `priority` field on the record), gateway replica B cannot write those fields because the envelope schema version is locked to 1. The immutability constraint means `TaskRecord` envelope schema evolution can only happen when no cross-version records exist -- effectively requiring a complete drain before any envelope schema upgrade.

**Recommendation:** Document the operational constraint explicitly: `TaskRecord` envelope schema version upgrades require that no active (non-terminal) task records exist at the old envelope version, or that new envelope fields are additive-only and backward-compatible at the same `schemaVersion` (in which case the immutability constraint is moot since no version bump is needed). If the intent is that envelope `schemaVersion` only increments for breaking envelope changes (which should be rare), state this explicitly.

---

### PRT-056. `from_mcp_content` Helper Only Covers Inbound MCP-to-OutputPart; No Reverse Helper Documented [Low]

**Section:** 15.4.1 (Internal OutputPart Format)

The spec documents `from_mcp_content(blocks)` (line 7457) as an SDK helper that converts MCP content blocks to `OutputPart` arrays. However, there is no corresponding `to_mcp_content(parts)` helper documented for the reverse direction. While runtimes primarily produce `OutputPart` objects (outbound), runtime authors who interact with MCP connectors will receive MCP content blocks in tool results and may need to convert `OutputPart` arrays back to MCP content blocks when forwarding results to other MCP servers. The Translation Fidelity Matrix documents the gateway-level adapter translation, but runtime authors doing intra-pod MCP interop have no SDK helper for the reverse mapping.

**Recommendation:** Document whether `to_mcp_content(parts)` is planned for the SDK, or explicitly state that the reverse direction is not needed at the runtime level because the adapter handles all outbound protocol translation and runtimes never need to produce MCP content blocks directly.

---

## 6. Developer Experience (DXP)

### DXP-050. Credential file contract shows single-provider schema but describes multi-provider format only in prose [Medium]
**Section:** 4.7 (Runtime credential file contract)

The JSON example at line 885 shows a single-provider flat object with top-level `leaseId`, `provider`, `expiresAt`, `deliveryMode`, and `materializedConfig`. The prose at line 896 then says "When a session holds multiple leases (one per provider), the file contains a top-level `providers` array with one entry per active lease." This creates two problems for runtime authors:

1. There is no JSON example of the multi-provider format. A runtime author implementing credential file parsing cannot determine whether the multi-provider case uses `{"providers": [{...}, {...}]}` or some other envelope. The single-provider example implies the file is a flat object; the multi-provider case implies a different top-level structure.
2. It is ambiguous whether a single-provider session uses the flat format (as shown) or the array format with a single entry. Runtime authors must guess whether they need to handle two distinct schemas or one.

**Recommendation:** Add a second JSON example showing the multi-provider format (e.g., `{"providers": [{"leaseId": "...", ...}, {"leaseId": "...", ...}]}`). Explicitly state whether the single-provider case uses the flat format or the array format with one entry, or specify that the array format is always used (simplifying runtime parsing to a single code path).

---

### DXP-051. Standard-tier echo pseudocode uses `lenny/output` then emits empty stdout response without explaining the required interaction [Medium]
**Section:** 15.4.4 (Sample Echo Runtime — Standard-tier addition)

The Standard-tier pseudocode (lines 8018-8024) calls `platform_mcp.call("lenny/output", {...})` to emit output, then immediately writes `{"type": "response", "output": []}` (an empty response) to stdout. The Full-tier pseudocode follows the same pattern (lines 8116-8121). This raises several questions that the spec does not answer:

1. Is the empty stdout `response` mandatory to signal task completion even when output was delivered via `lenny/output`? If so, this is a critical protocol requirement that is only visible by reading pseudocode, not stated in the protocol reference.
2. What happens if a Standard-tier runtime uses `lenny/output` for some parts and also includes parts in the stdout `response`? Are they merged, duplicated, or does one override the other?
3. Can a Standard-tier runtime skip `lenny/output` entirely and use the stdout `response` with a populated `output` array (like Minimum-tier), or is `lenny/output` required once you are Standard-tier?

**Recommendation:** In Section 15.4.1 (Adapter-Binary Protocol), add an explicit paragraph defining the relationship between `lenny/output` MCP tool calls and stdout `{type: "response"}` messages. State whether the stdout response is always required to signal task completion, whether its `output` array is merged with prior `lenny/output` calls, and whether Standard-tier runtimes can choose either delivery path.

---

### DXP-052. Adapter-local tool discovery requires reading the manifest, contradicting "Minimum tier does not need to read the manifest" [Medium]
**Section:** 15.4.1 (Adapter-Local Tool Reference) and 4.7 (Adapter manifest — Tier reading requirements)

Section 4.7 line 782 states: "Minimum — The runtime does not need to read the adapter manifest at all. It operates purely on stdin/stdout." However, the adapter-local tool reference (line 7723) states: "agents discover adapter-local tools by inspecting the `adapterLocalTools` array in the adapter manifest." Since adapter-local tools (`read_file`, `write_file`, `list_dir`, `delete_file`) are explicitly available at all tiers including Minimum (line 7714: "They are available at all tiers"), a Minimum-tier runtime that wants to use file operations must read the manifest to discover tool names and schemas — contradicting the "no manifest required" claim.

A Minimum-tier runtime can hardcode the four built-in tool names, but the spec says custom adapters MAY extend the tool list, and the manifest is the discovery mechanism.

**Recommendation:** Acknowledge this tension in the Tier reading requirements table. Either: (a) state that Minimum-tier runtimes that use adapter-local tools may optionally read the `adapterLocalTools` field from the manifest; or (b) define the four built-in tools as a fixed contract that Minimum-tier runtimes can rely on without reading the manifest, and note that custom adapter-local tools are only discoverable via the manifest. Option (b) is cleaner.

---

### DXP-053. `one_shot` runtimes can use `lenny/request_input` once, but `lenny/request_input` is a platform MCP tool — unavailable at Minimum tier [Medium]
**Section:** 5.1 (Runtime — `capabilities.interaction`)

Section 5.1 line 1657 states: "`capabilities.interaction: one_shot` — the runtime consumes the initial `{type: "message"}`, produces exactly one `{type: "response"}` carrying the final result, and the task ends. May use `lenny/request_input` once (for a single clarification)."

`lenny/request_input` is a platform MCP server tool, which is only available at Standard and Full tiers. A Minimum-tier `one_shot` runtime has no access to `lenny/request_input`. Yet `interaction` and integration tier are orthogonal — a runtime can be Minimum-tier and `one_shot`. The spec does not address what happens when a Minimum-tier `one_shot` runtime needs to ask a clarifying question.

**Recommendation:** Add a note in Section 5.1 clarifying that the `lenny/request_input` capability for `one_shot` runtimes is only available at Standard tier and above. Minimum-tier `one_shot` runtimes cannot request clarification and must produce their response based solely on the initial input. Alternatively, if clarification is considered essential for `one_shot`, state that `one_shot` runtimes implicitly require Standard tier.

---

### DXP-054. `lenny/output` tool has no input schema documented anywhere in the spec [Medium]
**Section:** 8.5 (Delegation Tools) / 15.4

The `lenny/output` tool is listed in Section 8.5 and Section 9.1 as "Emit output parts to the parent/client" and is used in the Standard-tier and Full-tier echo runtime pseudocode as `platform_mcp.call("lenny/output", {"output": [...]})`. However, unlike the delegation tools (`lenny/delegate_task`, `lenny/await_children`, `lenny/request_input`, etc.) which have their input schemas documented in various sections, `lenny/output` has no formal input schema defined. Runtime authors must infer its schema from the pseudocode example alone.

Similarly, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, and `lenny/get_task_tree` lack formal tool input schemas. The delegation-focused tools (`lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/send_message`, `lenny/request_input`) are documented in Section 8, but the remaining platform MCP tools have only one-line descriptions.

**Recommendation:** Add a consolidated platform MCP tool schema reference (either in Section 8.5 or a new subsection of Section 15.4) documenting the MCP `inputSchema` for each platform tool, following the same JSON Schema format already used for adapter-local tools in Section 15.4.1. At minimum, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, and `lenny/get_task_tree` need schemas.

---

### DXP-055. `capabilities.interaction` and `capabilities.injection` have no documented defaults in the minimal runtime definition [Low]
**Section:** 5.1 (Minimal Configuration)

The minimal runtime definition (line 1932-1950) omits the `capabilities` field entirely. The spec says defaults exist: `interaction` defaults can be inferred from the `type: agent` description, and `injection.supported` defaults to `false` (line 1659). However, the default for `capabilities.interaction` (whether it is `one_shot` or `multi_turn`) is never explicitly stated. Line 1635 shows `interaction: one_shot` in an example, but that is an explicit setting, not a declaration of the default. A runtime author registering the minimal config cannot determine whether their runtime will be `one_shot` or `multi_turn` by default.

**Recommendation:** Add an explicit default statement: "When `capabilities.interaction` is omitted, it defaults to `one_shot`" (or whichever is correct). This should appear in the `capabilities` field documentation near line 1637.

---

### DXP-056. No guidance on what a Minimum-tier runtime should do when it receives a `tool_result` it did not request [Low]
**Section:** 15.4.1 (Protocol Reference — Inbound: `tool_result`)

Section 15.4.1 line 7659 states: "Every `tool_result.id` MUST match the `id` of a previously emitted `tool_call`. The adapter validates this — a `tool_result` with an unknown `id` is dropped and logged as a protocol error." This describes the adapter's behavior for mismatched results. However, line 7661 states: "Agents may have multiple outstanding `tool_call` requests; results may arrive in any order." Combined with interleaved `heartbeat` messages, a Minimum-tier runtime author must implement a correlation map to match incoming `tool_result` messages to outstanding `tool_call` IDs. This is not mentioned in the Minimum-tier echo pseudocode or the Minimum-tier capability description, which presents the protocol as trivially simple.

The echo runtime pseudocode (line 7947-7971) never emits a `tool_call`, so this complexity is invisible in the reference implementation. A runtime author who wants to use adapter-local tools (e.g., `read_file`) at Minimum tier must handle interleaved delivery but has no pseudocode guidance for it.

**Recommendation:** Add a brief "Minimum-tier with tool calls" pseudocode example or paragraph to Section 15.4.4, showing a Minimum-tier runtime that emits a `tool_call` for `read_file`, handles the interleaved `heartbeat`, and correlates the `tool_result` by `id`. This demonstrates the pattern without requiring Standard tier.

---

### DXP-057. `shutdown` on stdin has `deadline_ms` field; lifecycle `terminate` has `deadlineMs` — inconsistent casing [Low]
**Section:** 15.4.1 (Protocol Reference) vs. 4.7 (Lifecycle channel message schemas)

The stdin `shutdown` message (line 7631) uses `deadline_ms` (snake_case):
```json
{ "type": "shutdown", "reason": "drain", "deadline_ms": 10000 }
```

The lifecycle channel `terminate` message (line 708) uses `deadlineMs` (camelCase):
```json
terminate: { "type", "deadlineMs" (integer), "reason" (string) }
```

All other lifecycle channel messages use camelCase (`checkpointId`, `deadlineMs`, `interruptId`, `leaseId`, `remainingMs`). The stdin protocol uses snake_case (`deadline_ms`). This casing split is not documented as intentional and could confuse Full-tier runtime authors who handle both channels, since both deal with shutdown semantics.

**Recommendation:** Either (a) document the casing convention explicitly (stdin protocol = snake_case, lifecycle channel = camelCase) as an intentional choice, or (b) unify the casing across both channels. If this is intentional, add a one-line note in the Protocol Reference: "stdin protocol messages use snake_case field names; lifecycle channel messages use camelCase."

---

### DXP-058. Full-tier lifecycle channel checkpoint pseudocode assumes synchronous recv after sending `checkpoint_ready` [Low]
**Section:** 15.4.4 (Sample Echo Runtime — Full-tier addition)

The Full-tier pseudocode (lines 8069-8079) handles `checkpoint_request` by sending `checkpoint_ready`, then immediately blocking on `lc.recv_line()` expecting a `checkpoint_complete` response:

```
case "checkpoint_request":
    quiesce_state()
    lc.send_line(json({"type": "checkpoint_ready", ...}))
    cc = json_parse(lc.recv_line())   // ← blocks here
    assert cc.type == "checkpoint_complete"
    resume_state()
```

However, the lifecycle channel is bidirectional and may deliver other messages between `checkpoint_ready` and `checkpoint_complete` (e.g., `deadline_approaching`, `credentials_rotated`). A runtime that blocks synchronously on `recv_line()` expecting specifically `checkpoint_complete` will fail if any other message arrives first. The pseudocode uses a background goroutine with a `switch` statement, but within the `checkpoint_request` case, it synchronously waits for a specific message type — if `deadline_approaching` arrives between `checkpoint_ready` and `checkpoint_complete`, this code would assert-fail or mishandle it.

**Recommendation:** Add a comment in the pseudocode noting that production implementations should dispatch lifecycle messages from a single reader loop and use channels/callbacks to coordinate between handlers, rather than doing synchronous recv inside a case handler. Alternatively, restructure the pseudocode to show a state-machine approach.

---

---

## 7. Operator Experience (OPS)

### OPS-058. Missing `lenny-ctl` command for configuration drift audit [Medium]
**Section:** 24 (`lenny-ctl` Command Reference)

The `lenny-ctl` CLI provides comprehensive commands for pool management, migrations, and circuit breakers, but there is no command that allows an operator to compare the running Helm values against the actual state of the cluster. The `PoolConfigDrift` alert (Section 16.5) detects CRD-vs-Postgres drift for pools, but there is no general mechanism to detect drift between the Helm values file (the operator's declared desired state) and the live cluster state across all components (gateway configuration, PgBouncer settings, Redis topology, ResourceQuotas, LimitRanges, admission policies). This is especially important given the number of independent configuration surfaces: Helm values, CRDs, Postgres-stored pool configs, admin API-managed resources, and bootstrap seeds.

**Recommendation:** Add a `lenny-ctl config diff --values <values.yaml>` command that compares a Helm values file against the running cluster state and reports discrepancies. This provides operators a single-command way to answer "is my cluster running what I think it's running?" without requiring `helm diff` (which only compares rendered templates, not runtime state like Postgres-stored pool configs or admin API resources). Alternatively, document this as an explicit operational gap with guidance on which combination of existing tools to use.

---

### OPS-059. Bootstrap seed `pools` duplicated with top-level `pools` Helm value [Medium]
**Section:** 17.6 (Packaging and Installation), Day 0 walkthrough

The Day 0 walkthrough (Section 17.6) shows `pools` defined at two independent levels in the same `values.yaml` file: (1) a top-level `pools` array (line ~9005) that presumably configures the warm pool CRDs directly, and (2) a `bootstrap.pools` array (line ~9025) that seeds pool records via the admin API. The spec does not define the relationship between these two configuration surfaces or what happens when they conflict (e.g., `pools[0].minWarm: 2` at top level vs `bootstrap.pools[0].minWarm: 5` in bootstrap). Since the PoolScalingController reconciles pool config from Postgres into CRDs (Section 4.6.2), and the bootstrap Job writes to Postgres via the admin API, the top-level `pools` array and the bootstrap seed could produce conflicting desired states for the same pool.

**Recommendation:** Define the authoritative source. If the bootstrap seed (admin API / Postgres) is authoritative and the PoolScalingController reconciles CRDs from Postgres, then the top-level `pools` Helm value is either redundant or should only apply when there is no admin API record. Document this clearly and consider removing one of the two surfaces to prevent operator confusion. If both are needed (e.g., top-level `pools` for CRD-level fields, bootstrap for admin-API-level fields), document the exact split of responsibility.

---

### OPS-060. No rollback runbook or documented procedure for failed schema migrations [Medium]
**Section:** 10.5 (Upgrade and Rollback Strategy), 17.7 (Operational Runbooks)

Section 10.5 states "Down migrations are always provided but only used as a last resort" and the expand-contract pattern is well-documented. However, the runbook list in Section 17.7 does not include a schema migration failure runbook. The Phase 1.5 build sequence (Section 18) mentions `docs/runbooks/db-rollback.md` as a deliverable, but this runbook is not referenced in the Section 17.7 minimum required set. If a Phase 1 migration fails mid-way (e.g., a Phase 3 gate check passes but the subsequent `DROP COLUMN` fails due to a dependent view that was not accounted for), the operator needs a clear procedure. The advisory lock release and re-run behavior is documented, but the operational runbook (what to check, how to verify partial application, when to use down-migrations vs. forward-fix) is missing from the required runbook inventory.

**Recommendation:** Add `docs/runbooks/schema-migration-failure.md` to the minimum required runbook set in Section 17.7 with: trigger (`lenny-ctl migrate status` shows unexpected state or migration Job fails), diagnosis (check advisory lock status, verify partial DDL application, inspect `golang-migrate` schema_migrations table), and remediation (re-run vs. down-migration decision tree, forward-fix pattern for dependent objects). Cross-reference the Phase 1.5 deliverable `docs/runbooks/db-rollback.md`.

---

### OPS-061. `terminationGracePeriodSeconds` value is on gateway pods but tiered checkpoint cap applies to agent pod sessions [Low]
**Section:** 10.1 (preStop hook drain), 17.8.2 (Capacity Tier Reference)

The preStop hook drain in Section 10.1 describes a tiered checkpoint cap that applies during gateway pod termination. The tiered cap (30s / 60s / 90s) plus BarrierAck timeout (90s) plus stream drain (30s) must fit within `terminationGracePeriodSeconds`. Section 17.8.2 specifies `terminationGracePeriodSeconds` of 240s (Tier 1/2) and 300s (Tier 3) for the "gateway pod." However, agent pods also have their own `terminationGracePeriodSeconds` which governs the preStop checkpoint hook on eviction (Section 4.4). The agent pod `terminationGracePeriodSeconds` is stated as "240s at Tier 1/2, 300s at Tier 3" in Section 4.4, identical to the gateway value. This is potentially confusing because the gateway and agent pod preStop hooks have entirely different budgets and concerns. The Section 17.8.2 table only has one row for `terminationGracePeriodSeconds` without clarifying it applies to both gateway and agent pods (or whether the agent pod value is configured separately).

**Recommendation:** Add a separate row in the Section 17.8.2 "Gateway and API layer" table for agent pod `terminationGracePeriodSeconds`, or add an explicit note that the same values apply to both gateway and agent pods with a cross-reference to Section 4.4 explaining that the budget is consumed differently (gateway: tiered cap + barrier ack + stream drain; agent: eviction checkpoint upload + MinIO retries).

---

### OPS-062. No health check or smoke test for the bootstrap seed after `helm install` [Medium]
**Section:** 17.6 (Day 0 walkthrough)

The Day 0 walkthrough ends at step 7 ("Create a first echo session") which is a manual curl command. The `lenny-bootstrap` Job (step 4) seeds resources but does not verify that the seeded resources are functional (e.g., that the pool's warm pods actually reach `idle` state, that the runtime is correctly registered and reachable). The preflight Job validates infrastructure prerequisites but runs before bootstrap. If the bootstrap Job succeeds but the echo runtime image is wrong, or the pool can't warm pods due to an image pull error, the operator discovers this only when manually running step 6 (`lenny-ctl admin pools get echo-pool`). There is no automated post-install validation that the platform is end-to-end functional.

**Recommendation:** Add a `lenny-post-install-smoke` Helm post-install hook (weight after bootstrap) that: (1) waits for at least one warm pod to reach `idle` state in each bootstrapped pool (with a timeout), (2) creates a session with the echo runtime, sends a prompt, and verifies a response. This is essentially the `make test-smoke` from Section 17.4 but automated as a post-install hook. If the smoke test fails, the hook exits non-zero, making the failed install visible in `helm list` status. This catches configuration issues (wrong image, missing RuntimeClass, resource quota exhaustion) immediately rather than leaving them for the operator to discover manually.

---

### OPS-063. `lenny-ctl preflight` standalone mode requires Postgres/Redis DSNs but no documented credential handling [Medium]
**Section:** 24.2 (Preflight), 17.6 (Preflight validation)

`lenny-ctl preflight --config <values.yaml>` in standalone mode reads connection strings from the values file and probes infrastructure directly. The values file example (Section 17.6) shows `postgres.connectionString` containing credentials inline in the DSN (`postgres://lenny:password@pgbouncer:5432/lenny`). In a production CI pipeline running preflight before deployment, this means the operator must either (a) commit credentials to the values file (insecure), (b) use environment variable substitution in the values file (Helm does not natively support this), or (c) pass credentials through some other mechanism. The spec does not address how `lenny-ctl preflight` in standalone mode should obtain credentials securely when running outside the cluster (e.g., from a CI runner that has no access to Kubernetes Secrets).

**Recommendation:** Document the expected credential flow for standalone preflight in CI environments. Options include: (a) `lenny-ctl preflight` accepts `--postgres-dsn` and `--redis-dsn` flags that override values file DSNs (allowing CI to inject from environment variables), (b) the values file supports `postgres.dsnFromEnv: LENNY_PG_DSN` syntax, or (c) standalone mode supports reading DSNs from environment variables with a documented precedence order. Choose one and document it in Section 24.2.

---

### OPS-064. No operational guidance for partial-upgrade rollback when bootstrap and CRD versions diverge [Low]
**Section:** 10.5 (CRD upgrade procedure), 17.6 (Bootstrap seed mechanism)

The CRD upgrade procedure (Section 10.5) and the recovery procedure for stale CRDs (Section 17.6) are thorough. However, the bootstrap Job runs as `post-install,post-upgrade` (Section 17.6, line ~8846). If a `helm upgrade` applies new CRDs and then the bootstrap Job runs with new seed values that create resources referencing new CRD fields, a rollback to the previous Helm release (`helm rollback`) will restore the old bootstrap values and old controller code but the CRDs remain at the new version (Helm does not manage CRD downgrades). The admin API resources created by the bootstrap Job's `--force-update` path may also reference field values that the old code does not understand. The spec does not address this partial-rollback scenario.

**Recommendation:** Add a note in Section 10.5 or 17.6 documenting that `helm rollback` does not downgrade CRDs and that bootstrap-seeded resources created with `--force-update` are not automatically reverted. Recommend that operators who need a clean rollback after a failed upgrade should: (1) apply the previous version's CRDs explicitly, (2) re-run bootstrap with the old seed values, and (3) restart controllers. Consider whether the `lenny-upgrade.sh` script should capture a pre-upgrade snapshot of bootstrap resource state for rollback purposes.

---

### OPS-065. Self-managed MinIO endpoint defaults to `http://` in Helm config example [Low]
**Section:** 17.9 (Deployment Profiles, Self-Managed Profile)

The self-managed profile Helm configuration example (Section 17.9) shows `objectStorage.endpoint: "http://minio.lenny-system:9000"` (plain HTTP). However, Section 12.5 states "MinIO connections MUST use TLS (`https://` endpoint)" and "TLS may be disabled in local development mode via `minio.tls.enabled: false` in Helm values; this value defaults to `false` only when `global.deploymentProfile: dev`." The self-managed profile is explicitly not dev mode, yet its example uses an `http://` endpoint. While the preflight Job would catch this if MinIO encryption validation is enforced, the example is misleading and could be cargo-culted into production configurations.

**Recommendation:** Change the self-managed profile example endpoint to `https://minio.lenny-system:9000` to match the stated requirement. Add a comment noting that TLS is required for non-dev deployments.

---

### OPS-066. No `lenny-ctl` command for viewing or managing the `RuntimeUpgrade` state across all pools [Low]
**Section:** 24.4 (Pool Management), 10.5 (`RuntimeUpgrade` State Machine)

The `lenny-ctl admin pools upgrade status --pool <name>` command shows the upgrade state for a single pool. There is no command to list all active `RuntimeUpgrade` records across all pools (e.g., `lenny-ctl admin pools upgrade list` to show all in-progress upgrades). During a multi-pool upgrade window, an operator needs a single-command view of which pools are upgrading, in which state, and whether any are paused or stuck. Requiring per-pool status checks is operationally cumbersome.

**Recommendation:** Add `lenny-ctl admin pools upgrade list` that returns all active (non-`Complete`) `RuntimeUpgrade` records with their current state, pool name, and timestamps. This maps to a `GET /v1/admin/pools/upgrades` admin API endpoint.

---

## Summary

8 findings (0 Critical, 0 High, 5 Medium, 3 Low)

---

## 8. Multi-Tenancy (TNT)

### TNT-048. `agent_pod_state` mirror table missing from resource tenant-scoping classification [Medium]
**Section:** 4.2 (Resource tenant-scoping classification table), 4.6.1 (Fallback claim path)
The `agent_pod_state` Postgres table is used by the orphan session reconciler (Section 10.1) and the fallback claim path (Section 4.6.1). This table is a mirror of `Sandbox` CRD status, which is platform-global (no `tenant_id`). However, the orphan session reconciler cross-references `agent_pod_state` with session rows (which are tenant-scoped), and the fallback claim path uses `SELECT ... FOR UPDATE SKIP LOCKED` against it. Since `agent_pod_state` has no `tenant_id` and no RLS, a bug in the orphan session reconciler could inadvertently transition sessions belonging to a different tenant if it joins against the wrong pod record. The table is also absent from the resource tenant-scoping classification table in Section 4.2, so its scoping model is unspecified.
**Recommendation:** Add `agent_pod_state` to the resource tenant-scoping classification table with an explicit scoping model. Since pods are platform-global resources (like runtimes/pools), classify it as **Platform-global** with a note that all cross-references to tenant-scoped session rows are always mediated through RLS-protected `SessionStore` queries, and that direct queries against `agent_pod_state` must never return tenant-scoped data without a subsequent session-row lookup under RLS.

---

### TNT-049. `session_dlq_archive`, user role mappings, and custom role definitions missing from resource tenant-scoping classification table [Medium]
**Section:** 4.2 (Resource tenant-scoping classification table)
The resource tenant-scoping classification table in Section 4.2 enumerates tenant-scoped and platform-global resource types. Three resource types that are included in the tenant deletion lifecycle (Phase 4 in Section 12.8) are absent from this table: (1) `session_dlq_archive` (keyed by `(tenant_id, session_id, message_id)` per Section 7.2), (2) user role mappings (`user_id -> role` records scoped to a tenant), and (3) custom role definitions per tenant. These are all logically tenant-scoped, but their absence from the classification table means their isolation mechanism (RLS vs. application-layer) is unspecified. An implementer could omit RLS on these tables, leaving them protected only by application-layer filtering.
**Recommendation:** Add three rows to the resource tenant-scoping classification table: `session_dlq_archive` as **Tenant-scoped** (`tenant_id` column + RLS), user role mappings as **Tenant-scoped** (`tenant_id` column + RLS), and custom role definitions as **Tenant-scoped** (`tenant_id` column + RLS). These tables carry `tenant_id` and must have the same RLS policies as all other tenant-scoped tables.

---

### TNT-050. `__all__` sentinel and `lenny_tenant_guard` trigger interaction is underspecified for cloud-managed poolers [Medium]
**Section:** 4.2 (platform-admin cross-tenant access), 12.3 (Per-transaction tenant validation trigger)
Section 4.2 states that the `lenny_tenant_guard` trigger (Section 12.3) "treats `__all__` as a valid value (alongside concrete tenant IDs) and does not reject it." However, Section 12.3 describes the trigger as verifying that `current_setting('app.current_tenant', true)` is "set and is not `NULL`, empty, or `'__unset__'`" -- with no mention of `__all__` being explicitly allowlisted. Section 4.2 further states an integration test must verify that "`__all__` must be explicitly allowlisted in the trigger alongside concrete IDs." This creates a contradiction: the trigger definition in Section 12.3 does not mention `__all__` at all, while Section 4.2 requires it to be explicitly handled. Without explicit allowlisting in the trigger's implementation specification, the trigger would reject `__all__` as an unknown value when `LENNY_POOLER_MODE = external`, breaking `platform-admin` cross-tenant reads under cloud-managed poolers.
**Recommendation:** Add an explicit clause to the `lenny_tenant_guard` trigger specification in Section 12.3 stating that the trigger allows `__all__` in addition to concrete tenant IDs and the `__unset__` rejection. Specify that the trigger's logic is: reject if value is `NULL`, empty string, or `__unset__`; allow if value is `__all__` or matches the `^[a-zA-Z0-9_-]{1,128}$` tenant ID format. This makes the two sections consistent.

---

### TNT-051. No specification for how `lenny_tenant_guard` trigger handles tenant deletion tombstones [Low]
**Section:** 12.3 (Postgres tenant validation trigger), 12.8 (Tenant deletion lifecycle)
Section 12.8 specifies that after Phase 4 of tenant deletion, the tenant row is retained as a tombstone with `state = 'deleted'` and all mutable fields nulled. However, the `lenny_tenant_guard` trigger (Section 12.3) only validates that `app.current_tenant` is non-empty and not `__unset__` -- it does not check whether the tenant exists or is in `deleted` state. If a gateway bug or race condition sets `app.current_tenant` to a deleted tenant's ID, the trigger would allow the query to proceed, and since all tenant-scoped rows for that tenant have been purged, the query would return zero rows (harmless for reads). However, an INSERT with a deleted tenant's ID could create orphaned data that is unreachable by any active tenant and would not be cleaned up. The RLS policies filter by `app.current_tenant` but do not validate tenant existence.
**Recommendation:** This is a minor defense-in-depth gap. Consider adding a `BEFORE INSERT` trigger on tenant-scoped tables (or extending the `lenny_tenant_guard` trigger to cover INSERTs specifically) that validates the tenant exists and is in `active` state before allowing writes. Alternatively, document this as an accepted residual risk since the gateway's application layer validates tenant state before any write operation.

---

### TNT-052. Billing sequence object `billing_seq_{tenant_id}` is DDL-created with tenant ID in the name but not covered by RLS or the `lenny_tenant_guard` trigger [Low]
**Section:** 11.2.1 (Billing Event Stream), 12.3 (Postgres HA), 12.8 (Tenant deletion)
Section 11.2.1 specifies a per-tenant Postgres sequence object `billing_seq_{tenant_id}` for monotonic billing event numbering. Section 12.8 drops this sequence during tenant deletion (`DROP SEQUENCE IF EXISTS billing_seq_{tenant_id}`). Because this is a DDL object (not a table row), it is not covered by RLS policies or the `lenny_tenant_guard` trigger. The sequence name embeds the `tenant_id` directly. While the `tenant_id` format validation (Section 10.2) restricts the format to `^[a-zA-Z0-9_-]{1,128}$` which prevents SQL injection in the `CREATE SEQUENCE` DDL, the sequence is accessible to any database role with `USAGE` permission on the schema -- including the `lenny_app` role used by all gateway replicas regardless of which tenant they are currently serving. A gateway replica serving tenant A could theoretically call `nextval('billing_seq_<tenantB>')` and advance tenant B's sequence, creating gaps in tenant B's billing stream.
**Recommendation:** Ensure that `nextval('billing_seq_{tenant_id}')` is called only within the same transaction that performs `SET LOCAL app.current_tenant`, and add an application-layer assertion that the tenant ID in the sequence name matches the value of `app.current_tenant` for the current transaction. Document this as a defense-in-depth measure in Section 11.2.1. Alternatively, replace per-tenant sequences with a single sequence plus tenant-scoped gap detection, which eliminates the cross-tenant sequence advancement risk.

---

### TNT-053. `runtime_tenant_access` and `pool_tenant_access` join tables lack specified write-access restrictions for `lenny_app` database role [Medium]
**Section:** 4.2 (Multi-tenancy), 10.2 (Authorization and RBAC)
Section 4.2 specifies that `runtime_tenant_access` and `pool_tenant_access` are platform-global tables with no `tenant_id` column and no RLS, with visibility enforced by application-layer filtering. Section 10.2 states that only `platform-admin` can grant/revoke tenant access via these tables. However, there is no specification of database-level write restrictions on these tables. The `lenny_app` database role (used by all gateway replicas) presumably has full INSERT/UPDATE/DELETE on these tables. A bug in a `tenant-admin` code path could write to these tables, granting their tenant access to runtimes or pools not intended for them. Since there is no RLS and no database-level enforcement that only `platform-admin` operations can modify these rows, the entire access-control boundary depends on correct application-layer RBAC checks.
**Recommendation:** Specify that the `runtime_tenant_access` and `pool_tenant_access` tables should be writable only by a dedicated database role (e.g., `lenny_admin_app`) used exclusively by the `platform-admin` code paths, or add a `BEFORE INSERT/UPDATE/DELETE` trigger that validates the calling context (analogous to `lenny_tenant_guard`). At minimum, document this as a known application-layer-only enforcement boundary and add an integration test that verifies a `tenant-admin`-scoped request cannot modify these tables.

---

### TNT-054. Credential pool `cooldownOnRateLimit` state is not specified as tenant-scoped in Redis [Medium]
**Section:** 4.9 (Credential Pool), 12.4 (Redis HA and Failure Modes)
Section 4.9 describes per-credential health scoring including "Recent rate-limit events and cooldown expiry" and a `cooldownOnRateLimit` field on credential pools. The credential health scoring data (cooldown timers, rate-limit event history, auth failure counts) must be stored somewhere for the gateway to evaluate assignment strategies. Section 12.4 comprehensively lists all Redis key prefix patterns, but none cover credential health state. If cooldown state is stored in Redis, its key prefix pattern is unspecified, which means the tenant-key-isolation enforcement documented in Section 12.4 cannot be verified for this data. If stored in Postgres (in the `CredentialPoolStore`), it is covered by RLS. The storage location and tenant isolation mechanism for credential health data is not specified.
**Recommendation:** Specify where credential health scoring data (cooldown timers, rate-limit events, auth failure counts, concurrent session counts) is stored. If in Redis, add the key prefix pattern to the canonical table in Section 12.4 following the `t:{tenant_id}:` convention. If in Postgres, confirm it is covered by the `CredentialPoolStore` RLS. Add the key pattern to the `TestRedisTenantKeyIsolation` integration test coverage.

---

### TNT-055. `EvalResult` records lack explicit `tenant_id` column specification [Low]
**Section:** 10.7 (Experiment Primitives), 4.2 (Resource tenant-scoping classification)
Section 4.2 classifies "eval results" as tenant-scoped with `tenant_id` column + RLS. The `EvalResult` schema in Section 10.7 lists the fields `session_id`, `experiment_id`, `variant_id`, `scorer`, `score`, `scores`, `metadata`, `submitted_at`, `submitted_by`, and `idempotency_key` -- but does not include `tenant_id`. While tenant isolation could be inherited through the FK relationship to the `sessions` table (which has `tenant_id`), RLS policies based on `current_setting('app.current_tenant')` require the `tenant_id` column to exist directly on the table being queried. Without an explicit `tenant_id` column on the `eval_results` table, the RLS policy cannot be applied, and tenant isolation would depend on a JOIN to the sessions table in every query -- which is inconsistent with the direct-column RLS model used for all other tenant-scoped tables.
**Recommendation:** Add `tenant_id` as an explicit field in the `EvalResult` schema in Section 10.7, consistent with the pattern used for all other tenant-scoped tables. The gateway should populate `tenant_id` from the session's tenant at eval submission time, and the table should carry the same RLS policy as other tenant-scoped tables.

---

5 findings (0 Critical, 0 High, 4 Medium, 3 Low) [note: TNT-051 and TNT-052 are Low, TNT-055 is Low]

Let me recount: TNT-048 Medium, TNT-049 Medium, TNT-050 Medium, TNT-051 Low, TNT-052 Low, TNT-053 Medium, TNT-054 Medium, TNT-055 Low.

8 findings (0 Critical, 0 High, 5 Medium, 3 Low)

---

## 9. Storage Architecture (STR)

### STR-056. `session_tree_archive` missing from Storage Roles table and erasure scope [Medium]
**Section:** 12.2 (Storage Roles), 12.8 (Compliance Interfaces -- erasure scope table)

The `session_tree_archive` Postgres table is defined in Section 8.2 (completed subtree offloading) and referenced in Section 7.3 (re-await protocol) and Section 8.10, but it does not appear in the Section 12.2 Storage Roles table as a distinct store role. More critically, it is **absent from the erasure scope table** in Section 12.8. This table stores `TaskResult` payloads for completed delegation children, which can include agent-generated content (T3 -- Confidential). Without explicit inclusion in the erasure scope, `DeleteByUser` and `DeleteByTenant` may leave `session_tree_archive` rows intact after erasure, violating the GDPR completeness guarantee.

**Recommendation:** Add `session_tree_archive` to the erasure scope table in Section 12.8 with store `Postgres` and scope description "Completed child task results (TaskResult payloads) for delegation trees." Add a note that it must be deleted before `SessionStore` to satisfy FK dependencies (keyed by `root_session_id`). Optionally add a `DelegationArchiveStore` entry to the Section 12.2 Storage Roles table.

---

### STR-057. `MemoryStore` has no specified retention policy or GC mechanism [Medium]
**Section:** 9.4 (Memory Store), 12.5 (Artifact Store -- retention), 12.9 (Data Classification)

The `MemoryStore` interface (Section 9.4) stores user-scoped memories classified as T3 -- Confidential (Section 12.9). While `DeleteByUser` and `DeleteByTenant` are covered via the erasure scope table, the spec defines no retention policy, TTL, or GC mechanism for memories during normal operation. Every other persistent store has explicit retention guidance: artifacts (7 days), audit events (365 days), billing events (13 months), checkpoints (latest 2 per session), session logs (30 days). The `MemoryStore` is the only persistent T3 data store without a default retention floor or configurable TTL.

Since memories are user-scoped and persist across sessions, unbounded accumulation is expected by design. However, without a configurable `maxMemoriesPerUser` or `memoryRetentionDays` parameter, a single user or automated agent can accumulate unbounded rows in Postgres, and the only cleanup path is full user erasure.

**Recommendation:** Add a `maxMemoriesPerUser` (or `maxMemoriesPerScope`) configurable limit and a `memoryRetentionDays` optional TTL to the `MemoryStore` contract. Include memory row cleanup in the GC sweep (Section 12.5) for memories that exceed the TTL. If unbounded retention is intentional, state this explicitly and add a `MemoryStoreGrowthHigh` warning alert when per-user memory count exceeds a configurable threshold.

---

### STR-058. `EvalResultStore` missing from Storage Roles table (Section 12.2) [Low]
**Section:** 12.2 (Storage Roles)

`EvalResultStore` is referenced in the erasure scope table (Section 12.8) as a Postgres-backed store with FK dependency ordering requirements, and it can store up to 100,000 `EvalResult` records per session (Section 10.7). However, it does not appear in the canonical Section 12.2 Storage Roles table alongside `SessionStore`, `LeaseStore`, `TokenStore`, etc. This omission means the master store-role inventory is incomplete.

**Recommendation:** Add `EvalResultStore` to the Section 12.2 table with backend `Postgres`, purpose "Eval scores and metadata for session quality evaluation."

---

### STR-059. Experiment sticky assignment cache has no Redis recovery/rehydration path [Medium]
**Section:** 12.4 (Redis HA and Failure Modes), 10.7 (Experiment Primitives)

The Redis key `t:{tenant_id}:exp:{experiment_id}:sticky:{user_id}` stores experiment sticky variant assignments (Section 12.4 key table). Section 12.4 exhaustively documents failure behavior and rehydration paths for every other Redis-backed role (leases fall back to Postgres advisory locks, quota counters reconcile from Postgres checkpoints, routing cache falls back to Postgres lookup, token cache re-fetches from TokenStore). However, the experiment sticky cache has **no documented failure behavior entry** in the "Failure behavior per use case" table, and no documented Postgres-backed reconstruction path.

If Redis restarts, all sticky assignments are lost. Users who were previously assigned to a treatment variant could be reassigned to control (or vice versa) on their next session, corrupting experiment statistical validity. Section 10.7 does mention that variant assignments are flushed on experiment pause/conclude, but says nothing about Redis failure.

**Recommendation:** Add an entry to the Section 12.4 failure behavior table for "Experiment sticky assignments" specifying the behavior: either (a) persist sticky assignments to Postgres (the experiment `variant_assignments` table or equivalent) and rehydrate on Redis recovery, or (b) explicitly document that Redis loss causes re-randomization and that experiment analysis must account for this by using the Postgres `EvalResult.variant_id` as ground truth rather than the cache. If option (b), note the statistical implication.

---

### STR-060. Billing Redis stream `MAXLEN` is approximate (`~`) but spec implies hard cap [Medium]
**Section:** 11.2.1 (Billing Event Stream), 12.4 (Redis HA)

Section 11.2.1 specifies the billing Redis stream uses `MAXLEN ~billingRedisStreamMaxLen` (with `~`, the approximate trimming flag). The `BillingStreamBackpressure` alert fires at 80% of `billingRedisStreamMaxLen` (default 50,000). However, with approximate trimming, Redis can exceed the declared MAXLEN by an implementation-defined margin (typically up to one radix-tree node of entries, which can be several hundred entries). This means the actual stream length can exceed 50,000 before any trimming occurs.

This is not a correctness issue (billing events are not lost -- they're just delayed), but the 80% alert threshold (40,000) combined with approximate trimming means the alert could fire prematurely or the stream could grow beyond 50,000 without triggering the cap. More importantly, the spec says "events not flushed to Postgres within [billingStreamTTLSeconds] window are permanently lost" -- if the stream exceeds MAXLEN and the TTL fires, approximate trimming could cause slightly different data loss boundaries than expected.

**Recommendation:** Clarify that `MAXLEN` uses approximate trimming (`~`) and that the actual stream length may briefly exceed `billingRedisStreamMaxLen`. Adjust the alert threshold description to account for this (e.g., "fires when stream length exceeds 80% of the configured MAXLEN target"). If exact trimming is required for billing correctness, use `MAXLEN` without `~` and accept the slightly higher per-XADD overhead. This is a minor precision issue but matters for billing completeness guarantees.

---

### STR-061. `StorageRouter` multi-region design routes to per-region Postgres but lacks cross-region consistency model [Medium]
**Section:** 12.8 (Data Residency), 12.3 (Postgres HA)

Section 12.8 states that the `StorageRouter` directs writes to "region-local backends" for Postgres, MinIO, and Redis when `dataResidencyRegion` is set. The multi-region reference architecture (same section) says each region runs its own control plane with its own storage. However, several system-wide invariants assume a single Postgres instance:

1. The billing `sequence_number` is generated by a per-tenant Postgres sequence (`billing_seq_{tenant_id}`). If a tenant's sessions are split across regions (e.g., different environments in different regions), each regional Postgres instance would have its own sequence, breaking the monotonicity guarantee for consumers that aggregate across regions.
2. The `session_tree_archive` and delegation tree budget counters assume a single `root_session_id` lookup in one Postgres. Cross-region delegation is explicitly prohibited, so this is safe -- but the constraint is only stated as "delegation trees are region-local by design" without a hard enforcement mechanism.
3. Tenant-level quota counters (`storage_bytes_used`) are per-tenant in Redis. If a tenant has data in multiple regions, each region's Redis tracks its own counter independently. The per-tenant `storageQuotaBytes` could be exceeded globally even if each region is individually under quota.

**Recommendation:** Add explicit guidance in Section 12.8 that per-tenant quotas (storage, token budget) are enforced **per-region** and that global cross-region quota aggregation is the deployer's responsibility (e.g., via a centralized billing aggregation layer). Clarify that billing sequence monotonicity is per-region-per-tenant, not globally monotonic across regions. These are not bugs but need to be documented so multi-region deployers understand the consistency model.

---

### STR-062. Checkpoint GC "latest 2" rotation counting does not account for partial checkpoint manifests [Low]
**Section:** 12.5 (Artifact Store -- Checkpoint retention policy)

The checkpoint retention policy states "Keep only the latest 2 checkpoints per active session." The GC sweep for partial checkpoint manifests (same section) handles manifests where `partial = true` by deleting them when the session reaches terminal state or the resume window expires. However, the interaction between the "latest 2" rotation and partial manifests is unspecified.

If a checkpoint upload times out and produces a partial manifest (Section 10.1), does that manifest count toward the "latest 2" limit? If it does, a sequence of two failed checkpoints could cause the GC to rotate out the last two valid full checkpoints, leaving only partial manifests as the "latest 2." If it doesn't count, the spec should say so explicitly.

**Recommendation:** Clarify that the "latest 2 checkpoints" retention policy counts only records where `partial = false` (i.e., valid full checkpoints). Partial manifests (`partial = true`) are excluded from the rotation count and are governed exclusively by their own GC sweep (resume consumption or age-based expiry). This prevents a sequence of checkpoint failures from evicting valid checkpoints.

---

### STR-063. Redis durable inbox (`messaging.durableInbox`) not in failure behavior table [Low]
**Section:** 12.4 (Redis HA and Failure Modes)

The Redis key table in Section 12.4 includes `t:{tenant_id}:session:{session_id}:inbox` for the durable session inbox (created when `messaging.durableInbox: true`). However, the "Failure behavior per use case" table in the same section does not have an entry for "Durable session inbox." If Redis becomes unavailable, the behavior of enqueue/dequeue operations on the inbox is unspecified -- it is unclear whether messages are lost, whether the gateway falls back to in-memory buffering, or whether message delivery blocks.

**Recommendation:** Add a "Durable session inbox" entry to the failure behavior table. Expected behavior: on Redis unavailability, inbox enqueue operations should fail (messages cannot be durably queued) and the gateway should return a retryable error to the sender. Messages already in the inbox before the outage are preserved (Redis persistence). If the inbox is also used during `awaiting_client_action` to queue child results, clarify whether `session_tree_archive` (Postgres) serves as the durable fallback.

---

5 findings (0 Critical, 0 High, 4 Medium, 1 Low)

**Note on finding count:** I identified 8 candidate findings but downgraded 3 to Low, reflecting that this spec has already been through 16 review iterations and the remaining storage architecture is very thoroughly specified. The Medium findings (STR-056, STR-057, STR-059, STR-060, STR-061) represent genuine gaps in erasure completeness, retention specification, failure mode documentation, and multi-region consistency semantics. The Low findings (STR-058, STR-062, STR-063) are minor completeness issues in reference tables.

8 findings (0 Critical, 0 High, 5 Medium, 3 Low)

---

## 10. Recursive Delegation (DEL)

### DEL-055. `maxParallelChildren` missing from both extendable and not-extendable lists in Section 8.6 [Medium]
**Section:** 8.6 (Lease Extension)

The extendable fields list explicitly names: `maxChildrenTotal`, `maxTokenBudget`, `maxTreeSize`, `perChildMaxAge`, `fileExportLimits`. The not-extendable list names: `maxDepth`, `minIsolationProfile`, `delegationPolicyRef`, `perChildRetryBudget`. `maxParallelChildren` appears in neither list, leaving its extensibility undefined. Given that `maxParallelChildren` is a resource budget (concurrent fan-out capacity), it should logically be extendable. But the omission creates ambiguity for implementers.

**Recommendation:** Add `maxParallelChildren` to the extendable fields list. If the intent is to make it not-extendable (as a Redis contention safety boundary), add it to the not-extendable list with a justification.

---

### DEL-056. `LeaseSlice.maxTreeSize` described as per-subtree but enforcement is tree-wide [Medium]
**Section:** 8.2 (Delegation Mechanism), 8.3 (Delegation Policy and Lease)

The `LeaseSlice` field table describes `maxTreeSize` as "Max pods in child's subtree" -- implying a per-subtree scope. However, the `budget_reserve.lua` script (Section 8.3 step 1) operates on a single tree-size counter keyed by `root_session_id`, and Section 8.3 states: "`maxTreeSize` caps the total number of pods across the entire task tree (all depths)". The `delegation_tree_budget` Postgres table is also keyed by `root_session_id`.

This means the tree-size counter is tree-wide, not per-subtree. If a parent allocates `maxTreeSize: 20` to child A and `maxTreeSize: 20` to child B via `LeaseSlice`, the tree-wide counter does not enforce these per-subtree limits -- both children draw from the same global counter. There is no described mechanism for tracking or enforcing per-subtree size limits as the `LeaseSlice` description implies.

**Recommendation:** Either (a) change the `LeaseSlice.maxTreeSize` description to "Contribution limit toward the tree-wide pod cap" to match the actual tree-wide enforcement, or (b) specify a per-subtree counter mechanism that enforces the child's individual `maxTreeSize` allocation (analogous to how `maxTokenBudget` is sliced from parent to child).

---

### DEL-057. `budget_return.lua` does not decrement `childrenTotal` -- no specification of whether this is intentional [Low]
**Section:** 8.3 (Budget Reservation Model, step 4)

`budget_reserve.lua` increments `childrenTotal` (lifetime count of children spawned). `budget_return.lua` decrements `parallelChildren`, `treeSize`, and `treeMemory`, but does NOT decrement `childrenTotal`. This is likely intentional -- `childrenTotal` is a lifetime counter (total ever spawned) while `parallelChildren` is a concurrency counter (currently in-flight). However, the spec never explicitly states that `childrenTotal` is a monotonically increasing lifetime counter. The `LeaseSlice` description says "Max children the child may spawn" which is consistent with a lifetime interpretation, but `maxChildrenTotal` is listed as an extendable field. If extended, the ceiling rises, but the counter itself is never decremented, so the child can spawn more children total. This behavior should be explicit.

**Recommendation:** Add a sentence to step 4 or the `childrenTotal` field description: "`childrenTotal` is a lifetime (monotonically increasing) counter -- it is not decremented when children complete. It tracks the total number of children ever spawned under this session."

---

### DEL-058. `cascadeOnFailure` applies on normal `completed` but `detach` orphan pod cost is unbounded per-user [Medium]
**Section:** 8.10 (Delegation Tree Recovery)

The spec correctly documents that `cascadeOnFailure` applies on all terminal states including `completed`. When `cascadeOnFailure: detach` is set and a parent completes normally after `await_children(mode="any")`, still-running children become orphans bounded by `cascadeTimeoutSeconds` (default: 3600s) and `maxOrphanTasksPerTenant` (default: 100).

The issue is that detached orphan pods are explicitly "NOT counted toward the originating user's concurrency quota during the detached window" (Section 8.10). A user could systematically spawn delegation trees with `detach` policy, complete the parent immediately after launching children, and accumulate up to `maxOrphanTasksPerTenant` (100) resource-consuming pods that bypass their concurrency quota. The per-tenant cap exists, but there is no per-user orphan cap -- a single user in a shared tenant can exhaust the entire tenant's orphan budget.

**Recommendation:** Add a per-user orphan cap (`maxOrphanTasksPerUser`, default: e.g., 25) in addition to the per-tenant cap, or document that per-user orphan abuse is mitigated by the per-tenant cap being sufficiently low for the tenant's user count.

---

### DEL-059. Tree recovery timeout formula inconsistency with bottom-up recovery for deep trees [Low]
**Section:** 8.10 (Delegation Tree Recovery)

The deep-tree deployer guidance provides a formula: `maxTreeRecoverySeconds >= maxResumeWindowSeconds + (maxDepth - 1) * maxLevelRecoverySeconds + buffer`. The `maxResumeWindowSeconds` term accounts for "the leaf level's individual resume window." However, bottom-up recovery means ALL levels may need up to `maxLevelRecoverySeconds` each, not just the non-leaf levels. The formula gives the leaf level `maxResumeWindowSeconds` (900s default) instead of `maxLevelRecoverySeconds` (120s default), which is correct because a leaf's individual resume can take up to 900s. But the formula assumes only leaves have long resume windows -- a mid-tree node could also be recovering and need up to `maxResumeWindowSeconds` if its own individual resume window hasn't expired.

The spec states: "A node's individual resume window (`maxResumeWindowSeconds`) runs concurrently with tree recovery. If a node's `maxResumeWindowSeconds` expires before tree recovery reaches it, that node transitions to `expired`." This means non-leaf nodes can also consume up to `maxResumeWindowSeconds` at their depth level if they happen to be recovering. The formula only accounts for this at the leaf level.

**Recommendation:** Acknowledge this in the deployer guidance: for trees where failures can occur at multiple levels simultaneously (Section 8.10 "Non-adjacent simultaneous failures"), the worst case is `maxResumeWindowSeconds + (maxDepth - 1) * max(maxLevelRecoverySeconds, maxResumeWindowSeconds) + buffer`. The current formula is adequate when only leaf-level failures produce long recovery times.

---

### DEL-060. Cross-environment delegation does not specify credential pool tenant scoping when `credentialPropagation: inherit` crosses environments within the same tenant [Low]
**Section:** 8.3 (Credential Propagation), 10.6 (Cross-environment delegation)

Section 8.3 specifies the cross-environment `credentialPropagation: inherit` compatibility check (provider intersection between parent pool and child runtime's `supportedProviders`). This correctly handles the case where the child runtime uses a different LLM provider.

However, the spec does not address the case where two environments within the same tenant have different `credentialPolicy.providerPools` configurations (different pools for the same provider). When `inherit` is used across environments, the child is assigned from the parent's credential pool -- but if the target environment has a different pool configured for the same provider, the child ends up using a pool that its environment's policy may not have authorized. Since environments are within a single tenant, the credential pools are tenant-scoped and accessible, so there is no security violation. But there is a deployer-expectation gap: the deployer may have placed a rate-limited pool in one environment and a high-throughput pool in another, expecting environment-local assignment.

**Recommendation:** Add a note to the cross-environment `inherit` section clarifying that `inherit` mode bypasses the target environment's `credentialPolicy.providerPools` selection -- the parent's pool is used directly. Deployers who need environment-local pool assignment for cross-environment delegations should use `credentialPropagation: independent`.

---

### DEL-061. `extension-denied` subtree scoping is ambiguous for multi-hop extension chains [Medium]
**Section:** 8.6 (Lease Extension, elicitation mode)

When a user rejects a lease extension, the spec states: "The requesting subtree (the session that triggered the elicitation and its descendants) is marked as extension-denied." The `extension-denied` flag and rejection cool-off are persisted to `delegation_tree_budget` keyed by `root_session_id`.

The ambiguity arises in multi-level trees. Consider: Root -> A -> B -> C. Session C's adapter requests a lease extension. The elicitation propagates up to the user. The user rejects. The spec says "the requesting subtree" is marked extension-denied -- this is the subtree rooted at C (C and its descendants). But the actual extension request that triggered the elicitation could have been from B (requesting more budget to allocate to C), or from C itself (requesting more token budget for its own use). The spec does not clarify which session is "the session that triggered the elicitation" in the delegation context.

Additionally, when B's adapter requests an extension because C's delegation exceeded B's budget, B is the triggering session. If B is marked extension-denied, then B and all B's descendants (including C) are denied. But if only C triggered it, only C is denied. The triggering session identity determines the blast radius of a rejection.

**Recommendation:** Clarify that the "requesting session" is the specific session whose adapter issued the `ExtendLease` gRPC call. Since lease extension is adapter-to-gateway (Section 8.6), the adapter on the session that exhausted its budget triggers the request. Document the exact session ID that becomes the `extension-denied` subtree root.

---

### DEL-062. Cycle detection by `(runtime_name, pool_name)` allows circular delegation through different pools of the same runtime [Medium]
**Section:** 8.2 (Delegation Mechanism, step 2a)

Cycle detection checks whether the target's resolved `(runtime_name, pool_name)` tuple appears in the caller's lineage. This means if runtime `A` exists in two pools (`pool1` and `pool2`), the delegation chain `A/pool1 -> B -> A/pool2` is NOT detected as a cycle because `(A, pool1) != (A, pool2)`.

This creates a potential for circular delegation where the same runtime binary is involved at multiple levels through different pools, defeating the purpose of cycle detection (preventing "circular wait deadlocks where the same runtime identity reappears"). The runtime identity is the same -- the pod runs the same image and same logic -- only the pool differs. A runtime that delegates to targets based on its own identity could enter a livelock where `A/pool1` delegates to `B`, which delegates to `A/pool2`, which delegates to `B/pool2`, etc., until `maxDepth` is hit.

The spec acknowledges that `maxDepth` prevents infinite forwarding, and the subtree deadlock detector (Section 8.8) handles circular waits. However, the stated purpose of lineage cycle detection is specifically to catch "runtime-identity cycles (e.g., A -> B -> A)" -- the current `(runtime, pool)` tuple does not fully achieve this.

**Recommendation:** Either (a) use `runtime_name` alone (without pool) for cycle detection, since the runtime binary is the same regardless of pool, or (b) document that pool-differentiated cycles are intentionally allowed (some deployers may want runtime A on pool1 to delegate to runtime A on pool2 with different resource classes), and note that `maxDepth` is the safety net for this case.

---

### DEL-063. `treeUsage` unavailable for parent with `cascadeOnFailure: detach` after normal completion [Low]
**Section:** 8.8 (TaskResult Schema)

The spec states: "`treeUsage` is populated by the gateway from the task tree and is only available after all descendants have settled." When `cascadeOnFailure: detach` is set, a completed parent's children continue running as orphans. Since the parent has already completed (terminal state), and its children are not yet settled, `treeUsage` would be `null` at the time the parent's `TaskResult` is available.

The client can query `GET /v1/sessions/{id}/tree` to monitor orphan progress, but the `treeUsage` field on the parent's result will never be populated because the parent reached a terminal state before its descendants settled. There is no specified mechanism for the gateway to update a terminal session's `treeUsage` retroactively when orphaned descendants eventually complete.

**Recommendation:** Document that `treeUsage` is always `null` for sessions with `cascadeOnFailure: detach` that complete before their children. If post-completion tree usage aggregation is needed, the client should query `GET /v1/sessions/{id}/usage` (which the spec says "Returns tree-aggregated usage including all descendant tasks") after orphans complete.

---

### DEL-064. `budget_return.lua` for orphaned children is described as no-op, but token budget is tree-wide [Medium]
**Section:** 8.10 (Detached orphan cascade and budget semantics)

The spec states: "When an orphaned child completes or fails, the standard `budget_return.lua` script is called but operates as a no-op -- the parent session is terminal and its budget counters are no longer active." However, `maxTokenBudget` is described as a tree-wide counter keyed by `root_session_id`, not per-session. If the root session is terminal but other non-orphaned branches of the tree are still running (e.g., root has two children: child A completes, cascade detaches child B; but child A had its own descendants still running), the tree-wide token budget counter is shared.

The "no-op" description assumes the parent's budget counters are inactive. But for tree-wide counters (`maxTreeSize`, `maxTokenBudget`, `maxTreeMemoryBytes`), they are keyed by `root_session_id` and may still be relevant if any part of the tree is still active. If the root is terminal, all its budget counters are cleaned up. But the spec should clarify: when the root session reaches a terminal state, are all tree-wide Redis budget counters immediately deleted, or are they retained until all orphans complete?

If deleted immediately when the root terminates, then non-orphan branches that are still winding down (under `await_completion` cascade) lose their budget enforcement. If retained, the `budget_return.lua` is not truly a no-op -- it decrements tree counters that are still being used by other branches.

**Recommendation:** Clarify the lifecycle of tree-wide Redis budget counters relative to root session termination. Specifically: (a) if `cascadeOnFailure` is `cancel_all`, all descendants are terminated synchronously, so counters can be cleaned up after cascade completes; (b) if `await_completion`, counters must be retained until `cascadeTimeoutSeconds` elapses or all children settle; (c) if `detach`, counters must be retained until all orphans complete or `cascadeTimeoutSeconds` elapses. The current "no-op" description is only accurate for case (a).

---

---

## 11. Session Lifecycle (SLC)

### SLC-056. Missing `input_required -> resume_pending` Transition for Pod Crash [High]
**Section:** 6.2 (Pod State Machine), 7.2 (Session State Machine)

The pod state machine in Section 6.2 defines `input_required` sub-state transitions for `cancelled` and `expired`, but omits the transition `input_required -> resume_pending` for pod crash or gRPC error while the session is in `input_required` state. The session state machine in Section 7.2 similarly lists transitions out of `input_required` as `running`, `cancelled`, and `expired` -- but does not account for pod failure. Since `input_required` is explicitly a sub-state of `running` where the pod is live, a pod crash while in this state is entirely possible. The `running -> resume_pending` transition exists but the spec does not state that `input_required` inherits it. Without this, a pod crash during `input_required` leaves the session state machine without a defined transition path, potentially stranding the session.

**Recommendation:** Add explicit `input_required -> resume_pending (pod crash / gRPC error, retryCount < maxRetries)` and `input_required -> failed (retries exhausted)` transitions to both the pod state machine (Section 6.2) and the session state machine (Section 7.2). Alternatively, add a normative statement that all transitions defined for `running` also apply to `input_required` as a sub-state, including pod failure transitions.

---

### SLC-057. `starting` State Has No Dedicated Watchdog Timeout [Medium]
**Section:** 6.2 (maxSessionAge timer), 11.3 (Timeouts), 15.1 (State preconditions)

The `maxSessionAge` timer table (Section 6.2) states that `starting` is "Running" and elapsed time counts toward `maxSessionAge`. However, `maxSessionAge` defaults to 7200s (2 hours). The `starting` state represents the agent runtime launching -- a phase that should complete in seconds to low tens of seconds. Unlike `finalizing` (which has a dedicated `maxFinalizingTimeoutSeconds` at 600s) and `ready` (which has `maxReadyTimeoutSeconds` at 300s), `starting` has no dedicated watchdog. A session stuck in `starting` due to a hung runtime binary would consume a pod for up to 2 hours before `maxSessionAge` fires the `expired` transition. The timeout table in Section 11.3 lists no `maxStartingTimeout` entry.

**Recommendation:** Add a `maxStartingTimeoutSeconds` (default: 120s) watchdog analogous to `maxFinalizingTimeoutSeconds` and `maxReadyTimeoutSeconds`. Transition to `failed` with reason `STARTING_TIMEOUT` if the session does not reach `running` within this window. Add it to the Section 11.3 timeout table.

---

### SLC-058. Two Distinct `generation` Counters With Conflated Naming [Medium]
**Section:** 4.2 (Session Manager), 7.3 (Retry and Resume), 10.1 (Horizontal Scaling)

The spec uses the term "generation" in two distinct contexts with potentially overlapping Postgres column space: (1) **Session generation** (Section 7.3): "Each recovery creates a new generation of the same logical session" -- this tracks how many times a session has been recovered onto a new pod. (2) **Coordination generation** (`coordination_generation` in Section 10.1): tracks coordinator handoffs across gateway replicas for split-brain prevention. The session manager schema (Section 4.2) lists "generation" in the session record, and the eviction state table (Section 4.4) stores "generation - the session generation counter (for coordinator fencing)" which conflates the two concepts. These are semantically independent counters -- a coordinator handoff should not increment the session recovery generation, and a session recovery should not necessarily reset the coordination generation. The spec never clarifies whether these are the same column or distinct columns, creating implementation ambiguity.

**Recommendation:** Explicitly define two separate columns: `recovery_generation` (incremented on pod recovery, visible to clients) and `coordination_generation` (incremented on coordinator handoff, internal only). Clarify which one is stored in the eviction state record (likely `coordination_generation` for fencing).

---

### SLC-059. `awaiting_client_action` Expiry Timer Reuses `maxResumeWindowSeconds` Ambiguously [Medium]
**Section:** 7.3 (Retry and Resume), 6.2 (Pod State Machine)

Section 7.3 states: "Sessions in `awaiting_client_action` expire after `maxResumeWindowSeconds` (default 900s)." However, `maxResumeWindowSeconds` is also used as the wall-clock cap for `resume_pending` (Section 6.2). The problem is that a session can transit through `resume_pending` (consuming some or all of its `maxResumeWindowSeconds` budget) and then enter `awaiting_client_action`. If the `awaiting_client_action` expiry timer is a fresh `maxResumeWindowSeconds` starting from zero, the total wait from initial failure could be up to `2 * maxResumeWindowSeconds` (1800s). If instead the timer is shared (continuing from `resume_pending`), a session that spent 899s in `resume_pending` gets only 1s in `awaiting_client_action`. The spec does not clarify whether the timer is fresh or cumulative.

**Recommendation:** Clarify that `awaiting_client_action` starts a fresh, independent `maxResumeWindowSeconds` timer on entry (since the two states serve different purposes -- automatic recovery vs. human decision). Consider renaming the `awaiting_client_action` expiry to a separate configurable field like `maxAwaitingClientActionSeconds` to eliminate the ambiguity.

---

### SLC-060. Concurrent-Workspace Slot `leaked` Sub-State Has No Recovery Path [Medium]
**Section:** 6.2 (Per-slot sub-states)

The per-slot sub-state machine includes: `slot_cleanup -> leaked (cleanup timeout exceeded -- slot not reclaimed until pod termination)`. However, the pod-level state machine does not define any transition triggered by the `leaked` state. The spec says "slot not reclaimed until pod termination" but does not specify: (a) whether a leaked slot counts toward `active_slots` (if yes, it permanently reduces the pod's effective capacity; if no, the Redis counter is decremented and may allow a new slot assignment that conflicts with the leaked slot's unreleased resources), (b) whether there is a maximum number of leaked slots before the pod is marked unhealthy, and (c) how the leaked slot interacts with the `ceil(maxConcurrent/2)` failure threshold for pod replacement -- does a leaked slot count as a "failed" slot for purposes of the unhealthy threshold?

**Recommendation:** Specify that a leaked slot remains counted in `active_slots` (preventing over-assignment), counts as a failed slot for the `ceil(maxConcurrent/2)` unhealthy threshold, and that a pod with `leaked_slots >= ceil(maxConcurrent/2)` immediately transitions to `draining`. Also add the `leaked` count as a field in the pod's observability metadata.

---

### SLC-061. No Defined Transition From `created` on Pod Claim Failure [Medium]
**Section:** 7.1 (Normal Flow), 15.1 (REST API State Preconditions)

The normal flow (Section 7.1 step 4) shows "Select pool, claim idle warm pod" as part of session creation. The REST API (Section 15.1) defines `created` as "a warm pod has been claimed and credentials assigned." However, the `created` state's only documented exit paths are: `created -> finalizing` (via finalize), `created -> expired` (via `maxCreatedStateTimeoutSeconds`), and `created -> completed/cancelled` (via terminate/delete). If the warm pool is exhausted after session creation but before pod claim completes, or if credential assignment fails at step 6, the session has already been persisted with `session_id` returned to the client. The spec's pre-claim credential check (step 3) mitigates this for credentials but is a point-in-time check, not a reservation. If the pod claim at step 4 fails due to pool exhaustion (after the session row is already created), there is no `created -> failed` transition defined.

**Recommendation:** Either (a) define that session creation is atomic -- the entire sequence (steps 2-8) succeeds or the session is never persisted, returning a retryable 503 to the client, or (b) add a `created -> failed` transition with reason `POD_CLAIM_FAILED` or `CREDENTIAL_ASSIGNMENT_FAILED` for cases where the session row is persisted but the pod or credential claim subsequently fails. The current spec implies atomicity (step 8 returns session_id only on success) but never states it explicitly.

---

### SLC-062. `cascadeOnFailure` Naming Mismatches Its Actual Scope [Low]
**Section:** 8.10 (Delegation Tree Recovery)

Section 8.10 explicitly states: "The name `cascadeOnFailure` is historical; it governs the fate of children on all parent terminal transitions, not only failure." This means the field triggers on `completed`, not just `failed`/`cancelled`/`expired`. However, the field name `cascadeOnFailure` is used across multiple sections (8.3, 8.10, 7.3) and will be implemented in code, configs, and APIs. A field whose name says "on failure" but fires on successful completion is a semantic trap that will cause implementers and deployers to misconfigure cascading behavior -- particularly for `await_children(mode="any")` patterns where the parent completes while siblings are still running.

**Recommendation:** Rename the field to `cascadeOnTerminal` or `childTerminationPolicy` in the spec and API before v1 ships. The historical name can be accepted as an alias for one release cycle.

---

### SLC-063. `maxIdleTimeSeconds` Timer Interaction With `await_children` Not Specified [Medium]
**Section:** 6.2 (maxIdleTimeSeconds timer)

The `maxIdleTimeSeconds` timer table defines "idle" as no `agent_output` or `tool_use` event emitted. The timer is paused during `input_required`, `suspended`, `resume_pending`, `resuming`, and `awaiting_client_action`. However, the table does not address the scenario where the runtime is actively blocked in `lenny/await_children` -- a state where the agent is logically active (waiting for child task results) but produces no `agent_output` or `tool_use` events. A parent session that delegates to children and blocks in `await_children` for longer than `maxIdleTimeSeconds` (default 600s) would be expired despite being functionally active. The runtime is in `running` state during `await_children`, and the timer is documented as "Active" during `running`.

**Recommendation:** Define that `await_children` tool calls reset `last_agent_activity_at` upon each partial result received from the stream, and upon the initial `await_children` invocation. Alternatively, add `await_children` as a condition that pauses the idle timer (analogous to `input_required`). The chosen approach should be documented in the timer table.

---

### SLC-064. `suspended -> running` via `delivery: immediate` Message Has No Coordinator Validation [Medium]
**Section:** 7.2 (Message Delivery Path 5)

Path 5 states: "If the message carries `delivery: 'immediate'`, the gateway atomically resumes the session (`suspended -> running`) and delivers the message to the runtime's stdin pipe once the runtime reports `ready_for_input`." This atomic resume-and-deliver is triggered by any message -- including inter-session messages via `lenny/send_message`. However, the spec does not define what happens if the session's coordinating gateway replica is different from the replica handling the message delivery. The `suspended -> running` transition requires writing to Postgres (state change) and sending a resume RPC to the pod (which requires being the coordinator). If the message lands on a non-coordinator replica, the resume must be forwarded to the coordinator. The spec does not describe this forwarding mechanism or its failure modes.

**Recommendation:** Specify that `delivery: immediate` resume requests are forwarded to the session's coordinating gateway replica (identified via the coordination lease in Redis/Postgres). If the coordinator is unreachable, the message should fall back to inbox buffering with `queued` delivery receipt status, not silently fail. Define the coordination-forwarding mechanism or state that all `send_message` calls are routed through the target session's coordinator.

---

### SLC-065. Checkpoint During `SIGSTOP` With Concurrent Tool Calls Undefined [Medium]
**Section:** 4.4 (Checkpoint Atomicity), 10.1 (CheckpointBarrier)

The embedded adapter SIGSTOP checkpoint path (Section 4.4) sends SIGSTOP to freeze the agent process, takes a workspace snapshot, then sends SIGCONT. The CheckpointBarrier protocol (Section 10.1) addresses the rolling-update case where the adapter finishes the current tool call before checkpointing. However, for periodic scheduled checkpoints in embedded adapter mode, the spec does not define the interaction between SIGSTOP and in-flight tool calls. If the agent is mid-way through a tool call execution (e.g., a filesystem write or an LLM API call) when SIGSTOP fires, the tool call is frozen mid-execution. On SIGCONT, the tool call resumes with potentially stale or inconsistent state (e.g., a half-written file, a timed-out HTTP connection). The cooperative lifecycle channel path (Full-tier) handles this via `checkpoint_ready`, but the SIGSTOP path has no quiescence guarantee.

**Recommendation:** Document that SIGSTOP checkpoints in embedded adapter mode do not provide tool-call consistency guarantees -- the workspace snapshot may capture partially written files from in-flight tool calls. State that this is an inherent limitation of the SIGSTOP path (which is why the lifecycle channel path is preferred for Full-tier runtimes). Consider recommending that embedded adapter periodic checkpoints use the lifecycle channel when available, falling back to SIGSTOP only for eviction checkpoints.

---

### SLC-066. `task_cleanup -> sdk_connecting` Transition Skips Credential Cleanup Verification [Low]
**Section:** 6.2 (Task-mode state transitions)

The task-mode state machine includes `task_cleanup -> sdk_connecting` for preConnect-capable runtimes with scrub success. The Lenny scrub procedure (Section 5.2 step 3b) removes `/run/lenny/credentials.json`, and step 6 verifies its removal. However, the `sdk_connecting` transition initiates the SDK pre-connect sequence, which may attempt to read credential files or establish LLM provider connections. The spec (Section 6.1) says "between tasks: after scrub completes and the adapter sends `task_ready`, the adapter re-establishes SDK-warm state by calling the SDK connect sequence again." But `sdk_connecting` happens before the next task's `AssignCredentials` RPC (which occurs at claim time, not warm time). If the SDK connect sequence attempts credential-dependent operations (e.g., Claude Code validating its API key), the connect will fail because no credentials exist yet. The `sdk_connecting` watchdog (60s) would then mark the pod `failed`, wasting a functional pod.

**Recommendation:** Clarify in the SDK-warm / task-mode interaction section that the SDK connect sequence for between-task re-warm MUST NOT attempt credential validation or LLM provider connections. The SDK connect should only establish the process and input/output pipes. If the runtime's SDK connect inherently requires credentials (cannot be separated), the runtime should not declare `preConnect: true` for task-mode pools, or the adapter should skip SDK re-warm between tasks and warm to pod-warm state instead.

---

### SLC-067. Deadlock Detection Algorithm Not Specified for Complex Blocking Patterns [Medium]
**Section:** 8.8 (Subtree Deadlock Detection)

Section 8.8 describes deadlock detection: "if every running task in a subtree (parent plus all descendants) is blocked -- either in `input_required` or in `await_children` waiting only on `input_required` children -- and no task in the chain can make progress, the gateway marks the subtree as `deadlocked`." However, the spec does not define the algorithm's behavior for more complex patterns: (a) Circular `lenny/send_message` dependencies (A is in `input_required` waiting for B to respond, B is in `input_required` waiting for A -- possible when `messagingScope: siblings`), (b) Mixed blocking where some children are `running` (making the subtree not "all blocked") but those running children are themselves blocked on external resources (LLM calls that will timeout), and (c) The detection scope is "subtree" but `send_message` with sibling scope creates cross-subtree dependencies that a per-subtree detector cannot see. The spec says the detection fires when "every running task in a subtree is blocked" which is a necessary but insufficient condition for actual deadlock.

**Recommendation:** Clarify that the deadlock detector is a heuristic (all-tasks-blocked-in-subtree), not a true cycle-detection algorithm, and document its false-negative cases (cross-subtree circular dependencies via sibling messaging). Consider whether the detection scope should extend to the full tree when `messagingScope: siblings` is active.

---

### SLC-068. `session.resumed` Event Missing `generation` Field for Client Consistency [Low]
**Section:** 7.2 (Interactive Session Model)

The `session.resumed(resumeMode, workspaceLost)` event sent to clients on session recovery includes `resumeMode` (`full` or `conversation_only`) and `workspaceLost` (boolean). However, it does not include the session's new `recovery_generation` / `generation` counter. Clients maintaining local state (e.g., IDE integrations tracking workspace changes) need to know when a generation boundary has occurred to invalidate caches, reset workspace tracking, or prompt the user. Without the generation in the resume event, clients must make a separate `GET /v1/sessions/{id}` call to discover the generation, introducing a race window.

**Recommendation:** Add a `generation` field to the `session.resumed` event so clients receive the new generation number atomically with the resume notification.

---

### SLC-069. `resume_pending` to `awaiting_client_action` Transition Path Inconsistent Between Sections [Low]
**Section:** 6.2, 7.2, 7.3

The transitions from `resume_pending` are defined in three places with subtle inconsistencies: (1) Section 6.2 pod state machine: `resume_pending -> resuming` and `resume_pending -> awaiting_client_action`. (2) Section 7.2 session state machine: `resume_pending -> resuming (pod allocated within maxResumeWindowSeconds)` and `resume_pending -> awaiting_client_action (maxResumeWindowSeconds elapsed, no pod available)`. (3) Section 7.3: Entry paths for `awaiting_client_action` include "(a) auto-retry exhaustion" and "(b) resume_pending timeout." However, the Section 6.2 `resuming` failure transitions show `resuming -> awaiting_client_action (retries exhausted)` which is a third path into `awaiting_client_action` not listed in Section 7.3's entry paths. Section 7.3 mentions only two entry paths but there are at least three: retry exhaustion (from `running`), `resume_pending` timeout, and `resuming` timeout with retries exhausted.

**Recommendation:** Update Section 7.3's `awaiting_client_action` entry paths to include all three: (a) auto-retry exhaustion on initial failure, (b) `resume_pending` wall-clock timeout, and (c) `resuming` failure/timeout with retries exhausted.

---

### SLC-070. No Maximum Bound on `parallelChildren` Counter During Cascading Failure [Medium]
**Section:** 8.3 (Budget Reservation Model), 8.10 (Delegation Tree Recovery)

The `budget_return.lua` script atomically decrements the `parallelChildren` counter when a child reaches a terminal state. However, during bottom-up tree recovery (Section 8.10), child sessions that were `running` at the time of a correlated failure (e.g., shared-node failure) transition through `resume_pending -> resuming -> attached` or `resume_pending -> resuming -> failed`. The `parallelChildren` counter is not decremented until the child reaches a terminal state, but during recovery the child remains in non-terminal states (`resume_pending`, `resuming`). If the parent also recovers and attempts to spawn new children before the recovering children reach terminal state, the `parallelChildren` counter includes both the recovering children and any new children. Since the recovering children are consuming `parallelChildren` slots even though they may eventually fail and free those slots, the parent may be blocked from spawning replacements by the `maxParallelChildren` limit. The spec does not define whether children in `resume_pending`/`resuming` states should count toward `parallelChildren`.

**Recommendation:** Define explicitly whether `resume_pending`/`resuming` children count toward the `parallelChildren` counter. If they do (current implicit behavior), document that during tree recovery the effective parallel capacity is reduced and recommend that deployers set `maxParallelChildren` with headroom for the expected number of simultaneous recovering children. If they should not count, add a `budget_suspend.lua` script that decrements `parallelChildren` on entry to `resume_pending` and re-increments on successful recovery to `attached`.

---

## Summary

**12 findings (0 Critical, 1 High, 8 Medium, 3 Low)**

---

## 12. Observability (OBS)

### OBS-050. Coordinator Handoff Protocol Has Zero Duration Observability [High]

**Section:** 10.1, 16.1, 16.3

Section 10.1 defines a complex 3-step coordinator handoff protocol: (1) CAS generation increment on Postgres, (2) `CoordinatorFence` RPC to the pod with a 5-second deadline and up to 3 retries with 1-second backoff, and (3) begin coordination. The protocol also defines failure paths: if all fence retries are exhausted, the coordinator relinquishes the lease and backs off with jittered delay (initial 2s, max 16s) before reconsidering. A gap detection path on the pod adds further complexity.

Despite this being a latency-critical distributed operation that directly determines session unavailability during rolling updates and failovers, there is:

- No `lenny_coordinator_handoff_duration_seconds` histogram in Section 16.1. The only coordinator-related metric is `lenny_coordinator_handoff_stale_total` (counter for stale-generation rejections), which measures a different failure path entirely.
- No `coordinator.handoff` span or equivalent in the Section 16.3 span table. The handoff protocol is invisible to distributed traces.
- No counter for fence RPC retries or for lease relinquishment after retry exhaustion.
- No alert for sustained handoff latency, which would be a leading indicator of coordinator churn during rolling updates.

Operators have no way to measure how long coordinator handoffs take, how often they require retries, or how often they fail entirely. During a rolling update that affects all gateway replicas, every active session undergoes at least one handoff, and the aggregate handoff latency determines the platform's effective session unavailability window.

**Recommendation:** Add to Section 16.1: `lenny_coordinator_handoff_duration_seconds` (Histogram, labeled by `pool`, `outcome`: `success`, `fence_retry`, `relinquished` -- measures wall-clock time from lease acquisition to either successful fence acknowledgement or lease relinquishment), `lenny_coordinator_fence_retry_total` (Counter, labeled by `pool` -- counts fence RPC retries), `lenny_coordinator_fence_relinquished_total` (Counter, labeled by `pool` -- counts handoffs abandoned after retry exhaustion). Add a `coordinator.handoff` span to Section 16.3 with child spans for `coordinator.cas_increment` and `coordinator.fence`. Add a `CoordinatorHandoffSlow` warning alert to Section 16.5: P95 of `lenny_coordinator_handoff_duration_seconds` exceeds 10s for > 2 min.

---

### OBS-051. RuntimeUpgrade State Machine Has Zero Observability [High]

**Section:** 10.5, 16.1, 16.5

Section 10.5 defines a 5-state machine for runtime image upgrades (`Pending -> Expanding -> Draining -> Contracting -> Complete`, plus `Paused`). The state machine governs pod pool rotation, session routing, and old-pool drain, and is surfaced via the admin API (`GET /v1/admin/pools/{name}/upgrade-status`).

A search of the entire spec confirms there are no metrics related to the `RuntimeUpgrade` state machine. Specifically:

- No `lenny_runtime_upgrade_state` gauge (labeled by `pool`, `state`) to track current upgrade phase.
- No `lenny_runtime_upgrade_phase_duration_seconds` histogram to measure how long each phase takes.
- No `lenny_runtime_upgrade_stuck` alert for an upgrade stuck in any non-terminal state beyond a configurable threshold.
- No metric for the `Draining` phase's active pod countdown, which determines whether the drain is progressing or stalled.

The `Draining` phase in particular can last as long as `maxSessionAge` (default 7200s / 2 hours), and its `drainTimeoutSeconds` parameter triggers forced session termination with checkpoint when exceeded. Operators have no Prometheus-visible signal that an upgrade is stalled in `Draining` with N sessions remaining, or that `Expanding` health checks are failing and the upgrade will not auto-advance. The only visibility path is polling the admin API, which is not integrated with standard Prometheus/Grafana alerting workflows.

**Recommendation:** Add to Section 16.1: `lenny_runtime_upgrade_state` (Gauge, labeled by `pool`, `state` -- value 1 for current state, 0 otherwise), `lenny_runtime_upgrade_phase_duration_seconds` (Gauge, labeled by `pool`, `phase` -- time spent in current phase so far), `lenny_runtime_upgrade_draining_sessions` (Gauge, labeled by `pool` -- number of sessions remaining on the old pool during `Draining`). Add to Section 16.5: `RuntimeUpgradeStuck` (Warning -- any pool's upgrade has been in a non-terminal, non-`Paused` state for longer than the phase-specific expected duration: `Expanding` > 2x `stabilizationWindowSeconds`, `Draining` > 1.5x `drainTimeoutSeconds`, `Contracting` > 300s).

---

### OBS-052. `lenny_session_last_checkpoint_age_seconds` Has Unbounded `session_id` Label Cardinality [Medium]

**Section:** 16.1, 16.5

Section 16.1 (line 8250) defines:

> `lenny_session_last_checkpoint_age_seconds`, per session, labeled by `session_id`, `pool`, `tier`

The `session_id` label creates one time series per active session. At Tier 3 (10,000 concurrent sessions), this is 10,000 time series. Over a Prometheus retention window (default 15 days), with session churn (sessions completing and new ones starting), the total unique `session_id` label values can reach hundreds of thousands -- each creating a distinct time series that Prometheus must store, index, and query.

This is a well-documented Prometheus anti-pattern. The `session_id` label provides per-session debugging value but at a cardinality cost that degrades Prometheus ingestion performance, increases memory usage, and slows PromQL queries. The `CheckpointStale` alert (Section 16.5, line 8478) uses this metric with condition `lenny_session_last_checkpoint_age_seconds > periodicCheckpointIntervalSeconds`, but this expression requires evaluating all 10,000+ time series every evaluation interval.

The spec already acknowledges this pattern elsewhere: Section 8.3 explicitly states that `root_session_id` is "not a Prometheus label (it would create unbounded cardinality)" for `lenny_delegation_parallel_children_high_watermark`. The `session_id` label on `lenny_session_last_checkpoint_age_seconds` contradicts this design principle.

**Recommendation:** Remove the `session_id` label from `lenny_session_last_checkpoint_age_seconds`. Instead, restructure as an aggregate metric: `lenny_checkpoint_stale_sessions` (Gauge, labeled by `pool`, `tier` -- count of active sessions whose last checkpoint age exceeds `periodicCheckpointIntervalSeconds`). This directly serves the `CheckpointStale` alert without per-session cardinality. Per-session checkpoint age debugging should use structured logs (already emitted per checkpoint) or the admin API, not Prometheus labels.

---

### OBS-053. CheckpointBarrier Protocol Success Path Is Unobservable [Medium]

**Section:** 10.1, 16.1

Section 10.1 defines the `CheckpointBarrier` protocol for rolling updates (lines 4137-4143): the gateway sends a `CheckpointBarrier` to every coordinated pod, pods quiesce and flush checkpoints, then send `CheckpointBarrierAck` back. The gateway waits for acks up to `checkpointBarrierAckTimeoutSeconds` (default 90s).

The only metric related to this protocol is `lenny_checkpoint_barrier_ack_timeout_total` (Counter, referenced at line 4129 in body text and not in Section 16.1). This measures the failure path only -- pods that did not ack within the timeout.

Missing from the spec:

- No counter for successful `CheckpointBarrierAck` responses. Without this, operators cannot compute the barrier success rate (successful acks / total barriers sent).
- No histogram for barrier ack latency (time from `CheckpointBarrier` sent to `CheckpointBarrierAck` received). This latency directly determines how much of the `terminationGracePeriodSeconds` budget is consumed by checkpoint flushing during rolling updates, leaving the remainder for stream drain (stage 3).
- No metric for the number of sessions that fell back to a prior periodic checkpoint because the barrier timed out.

During a rolling update affecting all gateway replicas, every coordinated session executes this protocol. If barrier ack latency is systematically high (e.g., MinIO is slow), operators will only know after SIGKILL incidents -- there is no leading indicator.

**Recommendation:** Add to Section 16.1: `lenny_checkpoint_barrier_ack_total` (Counter, labeled by `pool`, `outcome`: `success`, `timeout`, `error`), `lenny_checkpoint_barrier_ack_duration_seconds` (Histogram, labeled by `pool` -- time from barrier signal to ack receipt), `lenny_checkpoint_barrier_fallback_total` (Counter, labeled by `pool` -- sessions that fell back to last periodic checkpoint due to barrier timeout). Move `lenny_checkpoint_barrier_ack_timeout_total` from body text into Section 16.1 as the canonical metric table entry.

---

### OBS-054. Delegation Tree Bottom-Up Recovery Has No Duration or Outcome Metrics [Medium]

**Section:** 8.10, 16.1

Section 8.10 defines bottom-up delegation tree recovery with two configurable timeouts (`maxLevelRecoverySeconds` default 120s, `maxTreeRecoverySeconds` default 600s), per-level processing, and specific failure semantics (unrecovered nodes marked as terminally failed, cascade policies applied). This is a complex multi-level operation that determines whether delegation workloads survive pod failures or degrade into cascading failures.

The "Delegation Tree Recovery" subsection of Section 16.1 (lines 8311-8315) lists only orphan cleanup metrics (`lenny_orphan_cleanup_runs_total`, `lenny_orphan_tasks_terminated`, `lenny_orphan_tasks_active`, `lenny_orphan_tasks_active_per_tenant`). These measure post-recovery orphan management, not the recovery operation itself.

OBS-005 (iteration 1) recommended adding `lenny_delegation_tree_recovery_duration_seconds` (histogram, by outcome: success/partial/failed). This was never implemented -- a search of the spec confirms no such metric exists. The finding is being re-raised because the gap is now more severe: the spec has been expanded with non-adjacent simultaneous failure handling (line 3772), interaction with `maxResumeWindowSeconds` (line 3755), and deep-tree deployer guidance (line 3757), all of which add recovery complexity without any observability instrumentation.

Operators have no Prometheus-visible signal for: how long tree recovery takes, how often it partially fails (some nodes recovered, others timed out), how often `maxTreeRecoverySeconds` is the binding constraint vs `maxLevelRecoverySeconds`, or how frequently non-adjacent simultaneous failures consume additional recovery budget. The deep-tree guidance formula at line 3760 recommends increasing `maxTreeRecoverySeconds` based on tree depth, but operators cannot validate whether their setting is adequate without measuring actual recovery durations.

**Recommendation:** Add to Section 16.1 under "Delegation Tree Recovery": `lenny_delegation_tree_recovery_duration_seconds` (Histogram, labeled by `pool`, `outcome`: `full_success`, `partial_failure`, `total_timeout` -- measures wall-clock recovery duration per tree), `lenny_delegation_tree_recovery_levels_completed` (Histogram, labeled by `pool` -- number of levels successfully recovered per tree recovery, compared against tree depth), `lenny_delegation_tree_recovery_timeout_total` (Counter, labeled by `pool`, `timeout_type`: `level`, `tree` -- distinguishes per-level from total-tree timeouts). Add a `DelegationRecoveryTimeoutRate` warning alert to Section 16.5: `lenny_delegation_tree_recovery_timeout_total` rate > 0 for > 5 min.

---

### OBS-055. Subtree Deadlock Detection Has No Metrics [Medium]

**Section:** 8.8, 16.1

Section 8.8 (lines 3709-3725) defines subtree deadlock detection: when every running task in a subtree is blocked (in `input_required` or awaiting only `input_required` children), the gateway marks the subtree as `deadlocked`, delivers a `deadlock_detected` event, and enforces `maxDeadlockWaitSeconds` (default 120s). If the deadlock is not resolved within the timeout, the deepest blocked tasks are failed with `DEADLOCK_TIMEOUT`.

The error code `DEADLOCK_TIMEOUT` is defined in the error table (line 6952). However, there are no metrics anywhere in the spec for:

- `lenny_delegation_deadlock_detected_total` (Counter) -- how often deadlocks are detected.
- `lenny_delegation_deadlock_resolved_total` (Counter, by resolution method: `client_response`, `child_cancel`, `timeout`) -- how deadlocks are resolved.
- `lenny_delegation_deadlock_wait_seconds` (Histogram) -- time from detection to resolution.

Without these metrics, operators cannot determine whether deadlocks are a systemic pattern in specific delegation topologies, whether `maxDeadlockWaitSeconds` is appropriately tuned, or whether clients are reliably handling `deadlock_detected` events. The `DEADLOCK_TIMEOUT` error code would appear in session failure logs but cannot be aggregated or alerted on through Prometheus.

**Recommendation:** Add to Section 16.1: `lenny_delegation_deadlock_detected_total` (Counter, labeled by `pool`, `tenant_id`), `lenny_delegation_deadlock_resolution_total` (Counter, labeled by `pool`, `resolution`: `client_input`, `child_cancel`, `timeout`), `lenny_delegation_deadlock_duration_seconds` (Histogram, labeled by `pool` -- time from deadlock detection to resolution). Add a `DelegationDeadlockRateHigh` warning alert to Section 16.5: deadlock detection rate > 5 per 5 minutes for any pool.

---

### OBS-056. `Tier3GCPressureHigh` Suppression Condition Masks Alert During Traffic Dips [Medium]

**Section:** 16.5

The `Tier3GCPressureHigh` alert (line 8476) fires when `lenny_gateway_gc_pause_fleet_p99_ms > 50ms for > 5 min` but is suppressed at "< 5,000 active sessions." The alert description says: "Suppress at Tier 1/2 scale (< 5,000 active sessions)."

The suppression threshold uses a runtime metric (active session count) rather than the deployment's capacity tier configuration. This means at a Tier 3 deployment (designed for 10,000 concurrent sessions), the alert is suppressed whenever active sessions temporarily dip below 5,000 -- during off-peak hours, after a traffic incident, or during a gradual ramp-up. These are precisely the periods when GC pressure signals are most diagnostic: if fleet-wide P99 GC pauses exceed 50ms at only 4,999 sessions, the deployment will certainly breach the threshold at peak load. The suppression hides the early warning.

A Tier 1 deployment (target: 100 sessions) will never reach 5,000 active sessions, so the alert is permanently suppressed for Tier 1, which is correct. But the same logic accidentally suppresses the alert for Tier 3 deployments during sub-peak traffic.

**Recommendation:** Replace the session-count suppression condition with a deployment-tier-based condition. The alert should fire only when the deployment's configured capacity tier is Tier 3 (a static configuration value, not a runtime session count). Suggested condition: `lenny_gateway_gc_pause_fleet_p99_ms > 50ms for > 5 min` AND `deployment_tier == "tier3"` (or equivalent config-derived label). If a session-count floor is desired for Tier 3 to avoid alerting during cold-start with 0 sessions, use a much lower threshold (e.g., 500 sessions -- enough to generate meaningful GC pressure data -- rather than 5,000).

---

### OBS-057. `PodClaimQueueSaturated` Alert Threshold Degenerates for Scale-to-Zero Pools [Low]

**Section:** 16.5

The `PodClaimQueueSaturated` alert (line 8489) condition is:

> `lenny_pod_claim_queue_depth > 0.25 x pool.minWarm` for > 30s AND `lenny_warmpool_idle_pods > 0`

For scale-to-zero pools (`minWarm: 0`, documented as a supported configuration at lines 457-465 and 2169), the threshold becomes `queue_depth > 0.25 x 0 = 0`. Any positive queue depth satisfies this condition. Combined with the second condition (`idle_pods > 0`), the alert fires whenever a scale-to-zero pool has even one idle pod and one queued claim simultaneously -- a normal transient state during cold-start ramp-up when on-demand pods are being created and reaching `idle` before being claimed.

For pools with `minWarm: 10`, the threshold is 2.5 (effectively 3), providing proportional headroom. For `minWarm: 0`, the threshold provides zero headroom. The alert's intent is to detect "claim queue backing up even though warm pods exist," but the degenerate threshold makes it fire on routine cold-start transients in scale-to-zero pools rather than genuine saturation.

**Recommendation:** Add a floor to the threshold: `lenny_pod_claim_queue_depth > max(0.25 x pool.minWarm, 3)` to ensure a minimum queue depth of 3 before the alert fires, regardless of `minWarm`. Alternatively, exclude pools with `minWarm: 0` from this alert entirely (those pools accept documented cold-start latency and queue depth > 0 is expected), and rely on `lenny_pod_claim_queue_wait_seconds` histogram SLO for scale-to-zero quality-of-service monitoring.

---

**8 findings (0 Critical, 2 High, 4 Medium, 1 Low)**

Wait -- I listed 8 findings in the summary table but counted "0 Critical, 2 High, 4 Medium, 1 Low" which is 7. Let me recount: OBS-050 High, OBS-051 High, OBS-052 Medium, OBS-053 Medium, OBS-054 Medium, OBS-055 Medium, OBS-056 Medium, OBS-057 Low. That's 0 Critical, 2 High, 5 Medium, 1 Low = 8 total.

**Correction: 8 findings (0 Critical, 2 High, 5 Medium, 1 Low)**

---

## 13. Compliance & Governance (CMP)

### CMP-056. Experiment variant sticky cache and assignment data absent from user-level erasure scope [High]
**Section:** 10.7, 12.8

Section 10.7 describes per-user sticky variant assignment caches stored in Redis (keyed `t:{tenant_id}:exp:{experiment_id}:sticky:*`) that contain `user_id` as part of the key and value. Section 12.8's erasure scope table (the "Storage backends in erasure scope" table) enumerates every store the `DeleteByUser` job must cover, but experiment sticky caches are not listed. The `SemanticCache` entry covers query/response pairs, and the generic "Redis caches" entry covers "cached access tokens, routing entries" -- neither explicitly covers experiment assignment caches. Since sticky assignments are keyed by `user_id` and constitute personal data (they reveal which experiments a user was enrolled in), failing to purge them during GDPR erasure leaves identifiable experiment participation records in Redis after the user's data is supposedly erased.

Additionally, Section 4.2 lists "Experiments" as tenant-scoped with "variant assignments and sticky caches" stored per-tenant, yet the `DeleteByTenant` Phase 4 dependency-ordered deletion sequence in Section 12.8 does not include experiment definitions, variant assignments, or sticky caches as a deletion step.

**Recommendation:** Add "Experiment sticky assignment cache" as an explicit entry in the erasure scope table (backend: Redis, data erased: `sticky:user` variant assignment records for the user's sessions). Add it to the `DeleteByUser` implementation with a `DEL` on keys matching `t:{tenant_id}:exp:*:sticky:{user_id}`. Add experiment definitions and sticky caches to the `DeleteByTenant` Phase 4 sequence.

---

### CMP-057. User-level erasure has no specified dependency ordering [High]
**Section:** 12.8

Tenant-level deletion (Section 12.8, Phase 4) meticulously specifies the dependency order for store deletion: `LeaseStore -> SemanticCache -> Redis caches -> billing Redis stream -> QuotaStore -> ArtifactStore -> ... -> SessionStore -> TokenStore -> CredentialPoolStore`. The user-level erasure job has no equivalent ordering specification. The `EvalResultStore` entry notes it must be "deleted before `SessionStore` to satisfy the FK dependency," but this is the only FK ordering mentioned. The remaining stores are listed in a flat table with no execution sequence.

If the erasure job deletes from `SessionStore` before `ArtifactStore`, the artifact GC cannot resolve the session ID to locate MinIO objects. If `EventStore` (billing) is pseudonymized before `MemoryStore` is cleared, a crash during pseudonymization could leave the salt deleted while memories still contain the user's PII. Since the erasure job has an explicit crash-recovery mechanism based on persisted `phase` (Section 12.8), the absence of a defined store ordering means the crash-recovery resume point logic has no canonical sequence to resume from.

**Recommendation:** Define an explicit dependency-ordered deletion sequence for `DeleteByUser` analogous to the `DeleteByTenant` Phase 4 list. Specify FK-respecting ordering (at minimum: `LeaseStore` and `SemanticCache` first, then `QuotaStore`, `ArtifactStore`, `MemoryStore`, `EvictionStateStore`, `session_dlq_archive`, `EvalResultStore`, then `SessionStore`, then `EventStore` pseudonymization, then `TokenStore` and `CredentialPoolStore`, then Redis caches). Map each step to the `phase` field's sub-states for crash recovery.

---

### CMP-058. Billing event retention period has no compliance-profile-aware floor [Medium]
**Section:** 11.2.1, 16.4

Billing events are retained for a deployer-configurable period (default: 13 months). Section 16.4 defines compliance-aware audit retention presets with explicit floors per compliance profile (e.g., `hipaa`: 6 years, `fedramp-high`: 3 years), and the GDPR erasure receipt has a floor of 2190 days for regulated profiles. However, billing event retention has no equivalent compliance-profile floor. A deployer with `complianceProfile: hipaa` could set billing retention to 1 month, which would violate HIPAA's 6-year record-keeping requirement for financial transactions involving PHI (45 C.F.R. section 164.530(j)). The platform enforces floors on audit retention and GDPR receipt retention but not on billing retention.

**Recommendation:** Add a `billing.retentionDays` Helm value with compliance-profile-aware minimum floors (e.g., `hipaa`: 2190 days, `soc2`: 365 days, `fedramp`: 365 days). Reject configurations below the floor at startup when a regulated `complianceProfile` is active, consistent with the `audit.gdprRetentionDays` floor enforcement pattern.

---

### CMP-059. `billingErasurePolicy: exempt` has no retention ceiling or periodic review mechanism [Medium]
**Section:** 12.8

When a tenant sets `billingErasurePolicy: exempt`, billing events with the original `user_id` are retained indefinitely. GDPR Article 5(1)(e) (storage limitation principle) requires that personal data be kept "for no longer than is necessary for the purposes for which the personal data are processed." An indefinite retention with no ceiling and no periodic review mechanism fails to demonstrate storage limitation compliance, even when the retention is justified under Article 17(3)(b).

The spec documents that the deployer "accepts compliance responsibility" and the erasure receipt records the policy, but there is no platform primitive to enforce or remind deployers to review exempt billing data periodically. For HIPAA-covered entities, 45 C.F.R. section 164.530(j)(2) requires a 6-year retention period -- not indefinite retention.

**Recommendation:** Add a `billingExemptRetentionMaxDays` configuration with a deployer-specified ceiling (no default -- deployer must set it explicitly when choosing `exempt`). After this period, exempt billing events should be pseudonymized using the standard salt mechanism. Add a `BillingExemptRetentionReviewDue` warning alert that fires annually (or at a deployer-configured interval) to remind operators to review the continued necessity of exempt billing data retention.

---

### CMP-060. No data-protection impact assessment (DPIA) guidance for high-risk processing [Medium]
**Section:** 12.8, 12.9

GDPR Article 35 requires a Data Protection Impact Assessment for processing that is "likely to result in a high risk to the rights and freedoms of natural persons." The platform handles T3/T4 personal data including session transcripts (which may contain personal data in agent conversations), PHI-tagged workspace data, and user behavioral data (experiment variant assignments, semantic cache queries). The spec addresses deployer responsibility for breach notification (Section 11.8) and DSAR (Section 12.8 "Data Subject Access, Rectification, and Portability"), but provides no guidance on when a DPIA is required or what platform-provided data would support one.

Given that the platform is explicitly designed for multi-tenant personal data processing with AI agents (a category highlighted by EDPB guidelines as high-risk), the absence of DPIA guidance is a gap for deployers in EU jurisdictions.

**Recommendation:** Add a subsection to Section 12.8 documenting: (1) which processing activities on the platform likely trigger DPIA requirements under Article 35 (e.g., T4 PHI workspace processing, large-scale session transcript storage, experiment-based profiling); (2) which platform primitives provide data for a DPIA (data classification tiers, data flow inventory via audit trail, storage backend mapping); (3) a note that deployers must conduct their own DPIA using these primitives. This is guidance, not enforcement -- consistent with the "deployer as data controller" responsibility model.

---

### CMP-061. Audit hash chain genesis per-tenant allows post-creation manipulation of early entries [Medium]
**Section:** 11.7

Section 11.7 item 3 states that "the first entry in each tenant partition uses a well-known genesis hash" for the hash chain. This means the genesis hash is a fixed, predictable value (not derived from tenant-specific or time-specific entropy). A database superuser who tampers with a tenant's earliest audit entries and reconstructs the hash chain from the genesis hash forward can produce a valid chain that appears unbroken, because the genesis point is deterministic and publicly knowable.

While the SIEM provides an independent copy for regulated tenants, for tenants with `complianceProfile: none` operating without SIEM, the well-known genesis hash makes early-entry tampering undetectable by chain verification alone. The periodic background sampling (item 2) only detects broken chains, not reconstructed ones.

**Recommendation:** Derive the genesis hash from a tenant-creation-time random nonce that is stored alongside the first audit entry and also written to the SIEM (when configured) or to a separate, operator-accessible location outside the audit table. This makes chain reconstruction without the genesis nonce infeasible, strengthening the tamper-evidence guarantee even without SIEM. Alternatively, include a signed timestamp from the KMS in the genesis hash derivation.

---

### CMP-062. Semantic cache erasure does not cover pluggable backend implementations [Medium]
**Section:** 12.8, 4.9

The erasure scope table lists `SemanticCache` with backend "Redis (or pluggable)" and data "Cached query/response pairs scoped to the user." The `DeleteByUser` method is defined on each store interface. However, the `SemanticCache` is described in Section 4.9 as "fully replaceable by deployers" using pluggable backends (Mem0, Zep, vector databases). The contract validation helper (`ValidateMemoryStoreIsolation`) verifies tenant isolation and instrumentation, but there is no equivalent `ValidateSemanticCacheErasure` contract test that verifies pluggable `SemanticCache` implementations correctly implement `DeleteByUser`.

A deployer using a third-party vector database as their semantic cache backend may not have implemented user-level deletion, causing the erasure job to silently skip or fail on that store. The erasure receipt would record the store as erased even if the pluggable implementation's `DeleteByUser` is a no-op.

**Recommendation:** Add a `ValidateSemanticCacheErasure(t *testing.T, cache SemanticCache)` contract test that verifies: (a) `DeleteByUser` removes all entries for the specified user; (b) a subsequent `Query` for the deleted user returns zero results; (c) entries for other users are unaffected. Require pluggable implementations to pass this test. The erasure job should verify the return value of `DeleteByUser` and fail the erasure (not silently proceed) if the call returns an error or an unexpected zero-delete count when entries are known to exist.

---

### CMP-063. SIEM delivery lag can silently exceed audit retention, creating a compliance gap [Medium]
**Section:** 12.3, 16.4, 11.7

The SIEM outbox forwarder uses a high-water-mark-based delivery model (Section 12.3). The `AuditSIEMDeliveryLag` alert fires when lag exceeds `audit.siem.maxDeliveryLagSeconds` (default: 30s). However, the Postgres audit partition GC drops partitions beyond `audit.retentionDays`. If the SIEM forwarder is stalled or backlogged for longer than the Postgres retention period (e.g., 365 days for SOC2), the forwarder's high-water mark points to a partition that has already been dropped. Those audit events are permanently lost from both Postgres and SIEM.

The `AuditSIEMDeliveryLag` alert has a 30-second default threshold, but this only detects short-term lag. There is no protection against a scenario where SIEM delivery is silently disabled (e.g., forwarder misconfiguration after a deploy) and the lag gradually exceeds the retention window. The partition GC does not check whether the SIEM forwarder has consumed all events in a partition before dropping it.

**Recommendation:** The partition GC MUST NOT drop a partition whose most recent event has a `sequence_number` greater than the SIEM forwarder's last acknowledged high-water mark. Add a pre-drop check that queries the `siem_delivery_state` table and holds the partition if the forwarder has not caught up. Add a `AuditPartitionDropBlocked` warning alert when a partition is held beyond its normal TTL due to SIEM lag, signaling that either the forwarder must catch up or the operator must make an explicit decision to accept data loss.

---

### CMP-064. Data residency enforcement does not cover Redis-stored personal data [Medium]
**Section:** 12.8

Section 12.8 defines data residency enforcement at three levels: pod pool routing, storage routing (Postgres, MinIO, Redis), and session-creation validation. However, the `StorageRouter` interface is described as directing "writes (Postgres, MinIO, Redis) to the region-local backend," yet the Redis key prefix convention (Section 12.4) and the tenant key isolation scheme use a flat `t:{tenant_id}:` prefix with no region awareness. Multiple Redis-stored data types contain personal data classified as T3-Confidential: semantic cache entries, session coordination leases, DLQ messages containing inter-session user content, experiment sticky assignments, and billing write-ahead buffer entries.

If a deployer configures `dataResidencyRegion: eu-west-1` on a tenant but Redis is deployed as a single global instance (the default topology), all Redis-written personal data for that tenant resides outside the declared region. The `StorageRouter` mentions Redis as a routed backend, but there is no per-region Redis endpoint configuration equivalent to the per-region Postgres and MinIO endpoints described in the multi-region reference architecture (Section 12.8).

**Recommendation:** Add `storage.regions.<region>.redisEndpoint` to the Helm configuration alongside `postgresEndpoint` and `minioEndpoint`. The `StorageRouter` must route Redis writes for tenants with `dataResidencyRegion` to the region-local Redis instance. When `dataResidencyRegion` is set but `redisEndpoint` is not configured for that region, the `StorageRouter` must fail closed with `REGION_CONSTRAINT_UNRESOLVABLE` (consistent with the Postgres/MinIO behavior). Document that single-region Redis deployments are incompatible with multi-region data residency.

---

### CMP-065. `processing_restricted` flag cleared atomically with erasure receipt, but no constraint prevents early clearing via direct DB access [Medium]
**Section:** 12.8

Section 12.8 states that the `processing_restricted` flag "is cleared atomically when the erasure job writes its completion receipt." A manual override endpoint (`POST /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction`) exists for failed jobs. However, the `processing_restricted` flag is a simple boolean on the `UserStore` record in Postgres. The `lenny_app` database role has UPDATE access on the `UserStore` table (unlike audit and billing tables which are INSERT-only). A gateway bug, migration error, or direct database manipulation could clear this flag before erasure completes, allowing new sessions to be created for a user whose data is mid-erasure.

GDPR Article 18 (right to restriction of processing) requires that restricted processing be enforced until the underlying purpose (erasure) is complete. The current design relies on application-layer enforcement with no database-level protection.

**Recommendation:** Add a database-level `CHECK` constraint or trigger on the `processing_restricted` column that prevents clearing the flag unless the corresponding erasure job is in `completed` or `failed` state (joined via `user_id`). Alternatively, make the `processing_restricted` enforcement rely on the erasure job state directly (querying `erasure_jobs WHERE user_id = X AND status NOT IN ('completed', 'failed')` at session creation time) rather than a separate mutable boolean, eliminating the flag-clearing bypass vector entirely.

---

### CMP-066. Tenant deletion tombstone retention is unbounded [Low]
**Section:** 12.8

Section 12.8 states that tenant record tombstones (rows with `state = 'deleted'` and all mutable fields nulled) are retained after Phase 4 to "prevent tenant ID reuse" and "allow `GET /v1/admin/tenants/{id}` to return a `410 Gone` response." These tombstones are retained indefinitely with no specified retention period or cleanup mechanism. While individual tombstones are small, over years of operation in a multi-tenant platform, they accumulate and the table becomes an unbounded append-only store.

More importantly for GDPR, although mutable fields are nulled, the `tenant_id` itself is retained and the row references the erasure receipt. Under strict interpretation, a former tenant's `tenant_id` -- which may encode organization identity (e.g., `acme-corp`) -- is personal data if it can identify a legal entity.

**Recommendation:** Define a tombstone retention period (e.g., `tenant.tombstoneRetentionDays`, default: 2555 days / 7 years, matching GDPR receipt retention). After this period, the GC job replaces the `tenant_id` with a one-way hash and removes the `410 Gone` endpoint for that tenant. The audit trail references the hashed ID for historical traceability. Add `tenant_id` format guidance recommending opaque UUIDs rather than organization-identifying strings.

---

### CMP-067. Billing correction `correction_detail` free-text field is not covered by erasure pseudonymization [Low]
**Section:** 11.2.1, 12.8

Section 11.2.1 defines a `correction_detail` field on `billing_correction` events as "optional free-text detail supplementing the structured reason code." Section 12.8's billing pseudonymization states that "any free-text fields that could contain PII are cleared" during erasure. However, `correction_detail` is a free-text field on billing events that may contain user-identifying information (e.g., "Correcting overbilling for user john.doe@acme.com session s_xyz"). The pseudonymization spec explicitly names `user_id` and "free-text PII fields" as targets, but the `lenny_erasure` role's UPDATE grants are scoped to "the `user_id` and free-text PII columns only." `correction_detail` is not listed in either the grant scope or the pseudonymization target columns.

**Recommendation:** Explicitly list `correction_detail` as a column included in the `lenny_erasure` role's UPDATE scope and in the pseudonymization target set. During erasure, `correction_detail` should be set to NULL or a generic placeholder (e.g., "redacted-erasure") for any billing event belonging to the erased user.

---

### CMP-068. No audit event for `complianceProfile` downgrades [Low]
**Section:** 11.7, 12.8

Section 12.8 documents that creating or updating a tenant with a regulated `complianceProfile` triggers validation (SIEM required, pgaudit required). Section 11.7 documents compliance-profile enforcement gates. However, the spec does not address what happens when a `complianceProfile` is downgraded (e.g., from `hipaa` to `none`). A downgrade immediately disables: the 60-second grant check interval (reverting to 5 minutes), the SIEM hard requirement, and the pgaudit requirement. There is no documented audit event for this transition, no confirmation step, and no cooling-off period.

A `platform-admin` who wants to remove compliance controls from a tenant (whether legitimately or as an insider threat) can do so with a single `PUT /v1/admin/tenants/{id}` call, and the only record would be the standard admin operation audit log -- easily buried among routine tenant updates.

**Recommendation:** Emit a dedicated `compliance.profile_downgraded` critical audit event (distinct from generic tenant-update events) when `complianceProfile` is changed from any regulated value to `none` or to a less-restrictive regulated value. The event should include: `tenant_id`, `previous_profile`, `new_profile`, `changed_by`, and `justification` (require a justification field in the request body for downgrades). Forward this event to the SIEM with critical priority. Optionally, require dual-control approval for profile downgrades (similar to billing corrections), configurable via `compliance.dualControlOnDowngrade`.

---

### CMP-069. MinIO backup encryption validation is weaker than Postgres backup validation [Low]
**Section:** 12.5, 17.3

Section 12.3 explicitly states that "all WAL archives and base backups must also be encrypted" for Postgres, with specific guidance for managed and self-managed deployments. Section 17.3 defines the `lenny-restore-test` CronJob that validates Postgres backup integrity monthly. However, for MinIO, Section 12.5 states SSE-S3 or SSE-KMS "must be enabled for production deployments" but does not address MinIO backup encryption. Section 17.3 mentions "daily bucket replication or backup" for MinIO with "object checksum comparison" in the restore test, but does not validate that MinIO backups (or replicated buckets) are encrypted.

MinIO stores T3/T4 data including workspace files, session transcripts, and PHI-tagged workspace data. If MinIO site-to-site replication targets an unencrypted bucket, or if manual MinIO backups are stored without encryption, T3/T4 data is exposed at rest in the backup tier.

**Recommendation:** Add an explicit requirement that MinIO backup/replication targets must have SSE enabled. The `lenny-restore-test` CronJob should verify that the restored MinIO test bucket has encryption enabled (via `mc stat` or equivalent). Add a preflight check that validates the replication target bucket's encryption configuration when MinIO site-to-site replication is configured.

---

---

## 14. API Design (API)

### API-067. `INVALID_STATE` Error Code Not in Error Catalog [Medium]
**Section:** 7.1 (Derive session semantics), 15.1 (Error code catalog)
The derive semantics (line 2712) specify that when no workspace snapshot exists, derive returns `400 INVALID_STATE`. However, `INVALID_STATE` does not appear in the error code catalog (Section 15.1). The catalog has `INVALID_STATE_TRANSITION` (409) for state machine violations, but `INVALID_STATE` with HTTP 400 is a different code entirely. This means clients cannot rely on the catalog as the exhaustive error reference.
**Recommendation:** Either add `INVALID_STATE` to the error catalog with its HTTP status and category, or replace the derive error with an existing catalog code (e.g., `VALIDATION_ERROR` with an appropriate `details.field` and message, since the issue is about missing precondition data rather than a state machine violation).

---

### API-068. `UPLOAD_TOKEN_EXPIRED`, `UPLOAD_TOKEN_MISMATCH`, and `UPLOAD_TOKEN_CONSUMED` Not in Error Catalog [Medium]
**Section:** 7.1 (uploadToken format), 15.1 (Error code catalog)
Section 7.1 defines three upload token error codes with specific HTTP statuses: `UPLOAD_TOKEN_EXPIRED` (401), `UPLOAD_TOKEN_MISMATCH` (403), and `UPLOAD_TOKEN_CONSUMED` (410). None of these appear in the error code catalog in Section 15.1. Clients building error-handling logic from the catalog will not know about these codes. Additionally, `UPLOAD_TOKEN_CONSUMED` uses HTTP 410 Gone, which is not used by any other error in the system -- its `category` and `retryable` values are unspecified.
**Recommendation:** Add all three upload token error codes to the error catalog with their respective HTTP statuses, categories (`PERMANENT` for all three), and `retryable: false`.

---

### API-069. `TARGET_NOT_READY` and `CROSS_TENANT_MESSAGE_DENIED` Not in Error Catalog [Medium]
**Section:** 7.2 (Dead-letter handling, Cross-tenant validation), 15.1 (Error code catalog)
Section 7.2 defines `TARGET_NOT_READY` (returned when messaging a pre-running session) and `CROSS_TENANT_MESSAGE_DENIED` (returned when a message crosses tenant boundaries). Neither appears in the error code catalog. `TARGET_NOT_READY` has no specified HTTP status code. `CROSS_TENANT_MESSAGE_DENIED` has no specified HTTP status, category, or retryability. The catalog claims to be the comprehensive error reference but these inter-session messaging errors are missing.
**Recommendation:** Add both error codes to the catalog. `TARGET_NOT_READY` should be `TRANSIENT` (the session will eventually reach `running`). `CROSS_TENANT_MESSAGE_DENIED` should be `POLICY` / 403 / `retryable: false`.

---

### API-070. `RUNTIME_OPTIONS_INVALID` Error Code Not in Error Catalog [Low]
**Section:** 14 (Workspace Plan Schema), 15.1 (Error code catalog)
Section 14 (line 6372) specifies that invalid runtime options are rejected with `400 RUNTIME_OPTIONS_INVALID`, including a JSON Schema validation report. This error code is absent from the error catalog. While the behavior is well-described locally, clients cannot find it in the canonical error reference.
**Recommendation:** Add `RUNTIME_OPTIONS_INVALID` to the error catalog (`PERMANENT`, 400, `retryable: false`) with a note about the `details` field containing the JSON Schema validation report.

---

### API-071. `approve_tool_use`, `deny_tool_use`, and `dismiss_elicitation` Lack REST Endpoint Definitions [Medium]
**Section:** 7.2 (Interactive Session Model), 15.1 (REST API)
Section 7.2 lists `approve_tool_use(tool_call_id)`, `deny_tool_use(tool_call_id, reason?)`, and `dismiss_elicitation` as client-to-gateway operations in the interactive session model (alongside `respond_to_elicitation`). However, the REST API table in Section 15.1 defines no endpoints for these three operations. There is no `POST /v1/sessions/{id}/tool-approvals` or similar endpoint. Similarly, the MCP tools table in Section 15.2 does not include these as MCP tools. The only surface where they appear is the informal list in Section 7.2. This means REST clients and third-party adapters have no defined mechanism to approve/deny tool calls or dismiss elicitations.
**Recommendation:** Either add REST endpoints for these operations (e.g., `POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve`, `POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny`, `POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss`) and corresponding MCP tools, or document that these are streaming-only operations available only through the SSE channel and specify the wire format for each.

---

### API-072. Idempotency Key Mechanism Unspecified [Medium]
**Section:** 11.5 (Idempotency)
Section 11.5 states that critical operations (CreateSession, FinalizeWorkspace, StartSession, SpawnChild, Approve/DenyDelegation, Resume) "support idempotency keys" but provides no specification of the mechanism. There is no definition of how clients provide the idempotency key (header name, query parameter, or request body field), what format the key must have, what the retention window is for idempotency deduplication, what response is returned on a duplicate request (the original response? a specific error code?), or how idempotency keys interact with the error catalog. For a platform targeting third-party tooling, this is a significant specification gap.
**Recommendation:** Specify the idempotency mechanism: header name (e.g., `Idempotency-Key`), key format constraints, deduplication window (e.g., 24 hours), response behavior on duplicate (return cached 201 response with same body), and storage requirements (Postgres vs. Redis). Add a corresponding error code for idempotency conflicts if applicable.

---

### API-073. `session.terminated` Webhook Event Uses Non-Existent Terminal State [Medium]
**Section:** 14 (callbackUrl field, Per-event data schemas), 15.1 (External session states)
The webhook event type list (Section 14, line 6365) includes `session.terminated` and `session.cancelled` as separate event types. The per-event data schema distinguishes them: `session.terminated` is for "cancelled externally" with `terminatedBy: admin|system`, while `session.cancelled` is for "user/runtime cancelled". However, the external session state model (Section 15.1, line 6675-6678) defines only four terminal states: `completed`, `failed`, `cancelled`, and `expired`. There is no `terminated` terminal state. The `POST /v1/sessions/{id}/terminate` endpoint transitions to `completed` (line 6657). The `delegation.completed` webhook data schema also uses `status: "terminated"` as a possible value (line 6363), but `terminated` is not a valid canonical task state either. This creates ambiguity: what session state corresponds to a `session.terminated` webhook?
**Recommendation:** Clarify the mapping. If `session.terminated` corresponds to `completed` (via the terminate endpoint), document this explicitly. If it should be a distinct state, add it to the session state model. Also fix the `delegation.completed` status enum to use only valid terminal states (`completed`, `failed`, `cancelled`, `expired`).

---

### API-074. Custom Role CRUD Endpoint Missing from Admin API Table [Medium]
**Section:** 10.2 (Authorization and RBAC), 15.1 (Admin API table)
Section 10.2 (line 4222) states that deployers can define custom roles per tenant via `POST /v1/admin/tenants/{id}/roles`. However, this endpoint does not appear in the comprehensive admin API table in Section 15.1. There are no corresponding GET, PUT, or DELETE endpoints for role management either. The `lenny-ctl` command reference (Section 24) also has no commands for custom role management. This is a complete CRUD surface referenced in the RBAC model but never defined in the API spec.
**Recommendation:** Add the full custom role CRUD surface to the admin API table: `POST /v1/admin/tenants/{id}/roles` (create), `GET /v1/admin/tenants/{id}/roles` (list), `GET /v1/admin/tenants/{id}/roles/{name}` (get), `PUT /v1/admin/tenants/{id}/roles/{name}` (update, with `If-Match`), `DELETE /v1/admin/tenants/{id}/roles/{name}` (delete). Define the request body schema (role name, permissions list). Add corresponding `lenny-ctl` commands.

---

### API-075. Egress Profile Management Endpoints Missing [Low]
**Section:** 10.2 (RBAC Permission Matrix), 15.1 (Admin API)
The RBAC permission matrix (line 4215) lists "Manage egress profiles" as a permissioned operation across all roles. Section 15.1 (line 6778) clarifies that egress profiles are "an enum field on pool and runtime definitions, managed through pool/runtime endpoints -- they are not a separate CRUD resource." However, the permission matrix's phrasing "Manage egress profiles" implies a distinct administrative operation. The actual capabilities and constraints for egress profile values (what profiles exist, how new ones are added, what network restrictions each profile imposes) are never specified in the API surface. Third-party tooling cannot programmatically discover available egress profiles.
**Recommendation:** Either add a read-only `GET /v1/admin/egress-profiles` endpoint that returns available egress profile values and their network policy descriptions, or document that available profiles are a fixed set discoverable through the OpenAPI spec's enum definition on the pool/runtime schema. Update the permission matrix to say "Configure egress profiles (via pool/runtime endpoints)" to avoid implying a separate management surface.

---

### API-076. `POST /v1/admin/runtimes/regenerate-cards` Missing from Admin API Table [Low]
**Section:** 5.1 (publishedMetadata Field), 15.1 (Admin API table)
Section 5.1 (line 1895) defines a `POST /v1/admin/runtimes/regenerate-cards` endpoint for bulk A2A agent card regeneration, complete with request body schema and response format. This endpoint does not appear in the comprehensive admin API table in Section 15.1. It is also absent from the `lenny-ctl` command reference and the `dryRun` support table.
**Recommendation:** Add `POST /v1/admin/runtimes/regenerate-cards` to the admin API table. Note whether it supports `dryRun` (the endpoint has its own `dryRun` request body field, which should be documented alongside the query-parameter-based `dryRun` mechanism). Add a corresponding `lenny-ctl` command.

---

### API-077. REST/MCP Parity Gap: `derive`, `replay`, `extend-retention`, and `eval` Have No MCP Tool Equivalents [Medium]
**Section:** 15.1 (REST API), 15.2 (MCP API), 15.2.1 (REST/MCP Consistency Contract)
The REST API defines several session operations that have no corresponding MCP tools: `POST /v1/sessions/{id}/derive`, `POST /v1/sessions/{id}/replay`, `POST /v1/sessions/{id}/extend-retention`, and `POST /v1/sessions/{id}/eval`. The MCP tools table (Section 15.2) does not list `derive_session`, `replay_session`, `extend_retention`, or `submit_eval` tools. While the consistency contract (Section 15.2.1) acknowledges that "any manual MCP-only tool that has no REST counterpart is authored independently," it does not address the reverse gap where REST endpoints have no MCP equivalents. MCP-first clients (e.g., Claude Code or other AI agents using Lenny as an MCP server) cannot perform A/B evaluation workflows, session derivation, or retention management through the MCP interface.
**Recommendation:** Either add MCP tool equivalents for these endpoints or explicitly document them as "REST-only operations" with rationale. Given that MCP is the primary interface for interactive agent workflows, and `derive` and `replay` are key agent development operations, MCP tools are recommended.

---

### API-078. `GET /v1/sessions/{id}/messages` Pagination and Filtering Not Aligned with Standard Cursor Envelope [Low]
**Section:** 7.2 (MessageEnvelope), 15.1 (Cursor-based pagination)
The `GET /v1/sessions/{id}/messages` endpoint (mentioned at line 7591 and listed at line 6709) supports `?threadId=` and `?since=` filters. However, this endpoint is not listed in the explicit enumeration of paginated endpoints at line 7120 (which lists `GET /v1/sessions/{id}/transcript`, `GET /v1/sessions/{id}/logs`, `GET /v1/sessions/{id}/artifacts` but not `GET /v1/sessions/{id}/messages`). It is unclear whether message listing uses the standard cursor-based pagination envelope or the `?since=` filter replaces cursor-based iteration entirely. The `?since=` filter also has no documented format (timestamp? message ID? cursor?).
**Recommendation:** Add `GET /v1/sessions/{id}/messages` to the explicit pagination endpoint list. Document the `?since=` parameter format (likely a message ID or timestamp) and its interaction with cursor-based pagination. Specify whether `?threadId=` and `?since=` are composable with `?cursor=` and `?limit=`.

---

### API-079. `DELETE /v1/sessions/{id}` vs `POST /v1/sessions/{id}/terminate` Semantic Overlap Unclear [Low]
**Section:** 15.1 (Session lifecycle endpoints, State-mutating endpoint preconditions)
The API defines both `POST /v1/sessions/{id}/terminate` (transitions to `completed`) and `DELETE /v1/sessions/{id}` (transitions to `cancelled`). The precondition table (line 6657) says terminate transitions to `completed`, while DELETE (line 6661) transitions to `cancelled`. The description says DELETE is "equivalent to terminate + cleanup in one call." This is contradictory: if DELETE is equivalent to terminate + cleanup, it should transition to `completed` like terminate does, but instead it transitions to `cancelled`. The distinction matters for billing, webhooks (separate event types `session.completed` vs `session.cancelled`), and eval eligibility (`SESSION_NOT_EVAL_ELIGIBLE` mentions `cancelled` as not eval-eligible).
**Recommendation:** Clarify the semantic distinction: `terminate` is a graceful shutdown that allows the agent to finish and produce a result (hence `completed`), while `DELETE` is a forceful abort that produces no result (hence `cancelled`). Document this distinction explicitly in both endpoint descriptions.

---

### API-080. `POST /v1/sessions/{id}/messages` Precondition States Inconsistent with `POST /v1/sessions/{id}/resume` [Low]
**Section:** 15.1 (State-mutating endpoint preconditions)
The `POST /v1/sessions/{id}/messages` endpoint (line 6659) is valid in `running` and `suspended` states. With `delivery: immediate`, it atomically resumes a `suspended` session and delivers. However, `POST /v1/sessions/{id}/resume` (line 6658) is valid only in `awaiting_client_action`, not in `suspended`. The description says "Not valid in `suspended` (use message delivery or `resume_session` for that)." This is internally inconsistent: it says to use `resume_session` for `suspended` sessions but the `resume` endpoint doesn't accept `suspended` as a valid precondition state. The actual mechanism for resuming a `suspended` session is either `POST /v1/sessions/{id}/messages` with `delivery: immediate` or the `resume_session` MCP tool -- but the table's own note creates confusion.
**Recommendation:** Fix the note on `POST /v1/sessions/{id}/resume` to say "Not valid in `suspended` (use `POST /v1/sessions/{id}/messages` with `delivery: immediate`, or `send_message` MCP tool with `delivery: immediate`, for that)" -- removing the misleading reference to `resume_session` for `suspended` state.

---

### API-081. `GET /v1/usage` Response Schema Missing Pagination Envelope [Medium]
**Section:** 15.1 (Usage report, Cursor-based pagination)
`GET /v1/usage` is listed as a paginated endpoint at line 7120 ("This applies to: ... `GET /v1/usage` ..."). However, the response schema shown at lines 7149-7170 is a flat JSON object with `period`, `totalSessions`, `totalTokens`, `totalPodMinutes`, `byTenant`, and `byRuntime` fields. This does not use the cursor-based pagination envelope (`items`, `cursor`, `hasMore`, `total`). Either the endpoint is aggregated (like `GET /v1/admin/experiments/{name}/results`, which is explicitly called out as not paginated) or its response schema is wrong. Clients following the pagination contract will expect the standard envelope and fail to parse this response.
**Recommendation:** Decide whether `GET /v1/usage` is paginated or aggregated. If aggregated, remove it from the paginated endpoint list and note the exception (like is done for experiment results). If paginated (e.g., paginating over the `byTenant` or `byRuntime` arrays), restructure the response schema to use the standard cursor envelope.

---

### API-082. `PATCH` Method Used Only for Experiments, Creating Inconsistency Across Admin Resources [Low]
**Section:** 15.1 (Admin API table)
The experiments resource is the only admin resource that defines a `PATCH` endpoint (line 6852: `PATCH /v1/admin/experiments/{name}` for partial updates / status transitions using JSON Merge Patch). All other admin resources use `PUT` for updates. The spec does not explain why experiments need `PATCH` when all other resources manage status transitions through dedicated action endpoints (e.g., pools use `POST /v1/admin/pools/{name}/drain` for status change, external adapters use `POST /v1/admin/external-adapters/{name}/validate`). This creates an inconsistency: some resources change state via `PUT` (full replacement), others via `POST` (action), and experiments via `PATCH` (partial update). Third-party tooling must handle all three patterns.
**Recommendation:** Either adopt `PATCH` consistently for all resources that need partial updates (and document the JSON Merge Patch contract uniformly), or replace `PATCH /v1/admin/experiments/{name}` with dedicated action endpoints like `POST /v1/admin/experiments/{name}/activate`, `POST /v1/admin/experiments/{name}/pause`, `POST /v1/admin/experiments/{name}/conclude` -- consistent with the action-endpoint pattern used elsewhere.

---

---

## 15. Competitive Positioning (CPS)

### CPS-041. Google A2A Protocol Governance Claim Still Factually Incorrect [High]
**Section:** 23 (Competitive Landscape table, line ~9851)
The table entry for Google A2A Protocol states it is "now under AAIF governance alongside MCP." This was reported as CPS-026 in iteration 9 but the fix appears incomplete. As of the current document, Google's A2A protocol is governed by the Linux Foundation (specifically the A2A Project under the Joint Development Foundation), not AAIF. MCP and A2A are not "alongside" each other under AAIF governance -- they are under separate governance bodies (MCP under Anthropic's open specification, A2A under the Linux Foundation). Stating they share governance misleads evaluators about the protocol relationship and Lenny's alignment with standards bodies.
**Recommendation:** Replace "Agent-to-agent protocol now under AAIF governance alongside MCP" with "Agent-to-agent protocol governed by the Linux Foundation (Joint Development Foundation). Separate from MCP's governance." Update the description to accurately reflect the distinct governance structures.

---

### CPS-042. "Phase 17 deliverables" Reference Still Incorrect After CPS-039 [Low]
**Section:** 23.2 (line ~9901)
CPS-039 (iteration 1 of the current review cycle) identified that "Phase 17 deliverables" should read "Phase 17a deliverables" because there is no "Phase 17" in the build sequence -- only "Phase 17a" and "Phase 17b". The finding was reported but never fixed. The comparison guides paragraph still reads: "Phase 17 deliverables include published comparison guides..."
**Recommendation:** Change "Phase 17 deliverables" to "Phase 17a deliverables" in the comparison guides paragraph of Section 23.2.

---

### CPS-043. No Sustainability or Commercial Model Specification for an Open-Source Project [High]
**Section:** 23.2 (Community Adoption Strategy)
The spec positions Lenny as open-source with an enterprise-targeting community strategy, but contains zero specification of how the project will be sustained. The license evaluation (ADR-008) lists BSL as a candidate -- BSL is a source-available license incompatible with the OSI open-source definition and typically signals a commercial model (Temporal, Elastic, HashiCorp all used BSL with a commercial offering). If BSL is selected, calling Lenny "open-source" throughout the document is misleading. If a permissive license (MIT, Apache 2.0) is selected, there is no specification of what sustains the project beyond a single BDfN maintainer. Competitors have clear sustainability models: E2B has a hosted commercial offering; Temporal has Temporal Cloud; LangChain has LangSmith. The spec's silence here creates ambiguity for enterprise evaluators assessing long-term viability and for community contributors assessing whether their contributions will be captured by a future re-licensing.
**Recommendation:** Add a "Sustainability Model" subsection to Section 23.2 that specifies: (1) if BSL is selected, remove all "open-source" language and replace with "source-available" throughout, documenting the conversion timeline and additional-use grants; (2) regardless of license, document the intended sustainability path (commercial hosting, enterprise support contracts, foundation sponsorship, or community-sustained), even if high-level. ADR-008 evaluation criteria should explicitly include sustainability model compatibility.

---

### CPS-044. Hooks-and-Defaults Interfaces Are Fully Specified but Ship Post-V1, Creating a Hollow Differentiator at Launch [Medium]
**Section:** 22.6, 23.1 (differentiator 6), 18 (Phase 17b)
Differentiator 6 ("Ecosystem-composable via hooks-and-defaults") is a cornerstone of the competitive narrative, explicitly contrasting Lenny with LangChain's bundled approach and Modal's absence of hooks. However, the build sequence places the actual implementation of memory (`MemoryStore` + platform tools), semantic caching, guardrail interceptor hooks, and eval hooks in Phase 17b -- the final phase, after community launch (Phase 17a). At v1 community launch, the only hooks-and-defaults interface that is actually operational is the `RequestInterceptor` chain (Phase 7). `MemoryStore`, `SemanticCache`, and eval hooks exist as defined interfaces in the spec but have no implementation. This means the primary differentiator over LangChain's bundled approach does not exist in practice at the point Lenny is competing for adopters at community launch.
**Recommendation:** Either (1) move `MemoryStore` and `GuardrailsInterceptor` hook implementations to Phase 15 or earlier so they ship before community launch, or (2) revise differentiator 6 in Section 23.1 to explicitly scope which hooks are available at v1 launch vs. post-launch, so the competitive claims are honest. Currently the text reads as if all hooks are v1 commitments.

---

### CPS-045. Client SDK Coverage (Go + TypeScript Only) Is a Competitive Weakness Not Acknowledged [Medium]
**Section:** 15.6, 23.1
Lenny ships official client SDKs for Go and TypeScript/JavaScript only (Phase 6). The runtime adapter is Go-only (gRPC). Community SDKs for other languages "can build on the published OpenAPI spec." For comparison: E2B has official SDKs in Python, JavaScript, and Java; LangChain supports Python and JavaScript natively; Modal's primary SDK is Python. Python is the dominant language in the AI/ML ecosystem -- the absence of an official Python client SDK or any Python runtime adapter SDK is a significant adoption barrier for the "runtime authors" persona (Section 23.2), the majority of whom work in Python (LangChain, CrewAI, AutoGen are all Python-first). The spec mentions `from_mcp_content()` helper availability for Go only (Phase 2) and explicitly states "Other languages: not yet published as a library." This gap is not acknowledged anywhere in the competitive analysis.
**Recommendation:** Either (1) add a Python client SDK and Python runtime adapter helper as v1 deliverables alongside Go and TypeScript, or (2) add an explicit acknowledgment in Section 23.1 and 23.2 that Python SDK coverage is a known gap, with a timeline for community or official Python SDK delivery. At minimum, add Python to the Phase 17a comparison guides as a known limitation relative to competitors.

---

### CPS-046. Standard/Full-Tier Runtime Development Requires Linux, Excluding macOS Contributors Without Acknowledgment in Competitive Narrative [Medium]
**Section:** 15.4.3, 17.4, 23.2
The TTHW target ("< 5 minutes, clone + `make run` + echo session") is a key community adoption metric. However, Section 15.4.3 reveals that Standard- and Full-tier runtime development requires abstract Unix sockets, which are Linux-only. macOS developers -- a substantial portion of the target contributor base -- can only use `make run` for Minimum-tier runtimes. Standard/Full development on macOS requires `docker compose up` (Tier 2), which is a materially heavier setup (Docker Desktop, multi-container stack, real Postgres/Redis). The TTHW claim in Section 23.2 does not caveat that the < 5-minute target only covers Minimum-tier on macOS, or that the realistic path for macOS developers targeting Standard/Full tier is significantly longer. The persona table lists "Runtime authors" as the primary adoption target with entry point "`make run` local dev mode" -- but this entry point is limited on the most common developer platform.
**Recommendation:** (1) Add a macOS caveat to the TTHW paragraph: "The < 5 minute TTHW target covers Minimum-tier runtimes on all platforms. Standard- and Full-tier development on macOS requires `docker compose up` (Tier 2); TTHW for this path is targeted at < 10 minutes." (2) Consider providing a `make run-docker` convenience target that wraps `docker compose up` for macOS developers, keeping the single-command UX while transparently using Linux containers.

---

### CPS-047. Competitive Table Missing OpenAI Codex / Anthropic Claude Code Agent Platforms as Comparison Points [Medium]
**Section:** 23 (Competitive Landscape table)
The competitive landscape table covers E2B, Fly.io Sprites, Daytona, LangSmith, Temporal, Modal, and LangGraph. It omits two major competitors that directly overlap with Lenny's target use case of "cloud-hosted agent sessions": OpenAI's Codex (announced 2025, cloud-hosted coding agent with sandboxed environments) and Anthropic's own Claude Code infrastructure. Both provide hosted agent sessions with isolation, tool use, and workspace management. The "Claude Code" name appears in Section 1 as an example runtime but is never analyzed as a competing platform. Given that Lenny's differentiator is self-hosted + runtime-agnostic, the absence of these first-party agent platforms from the competitive analysis leaves a gap: an enterprise evaluator would immediately ask "why not just use the vendor's hosted agent platform?"
**Recommendation:** Add entries to the Section 23 competitive table for OpenAI Codex and Anthropic's hosted Claude Code (or a generalized "First-party hosted agent platforms" row). For each, document the trade-offs: vendor lock-in, data residency constraints, inability to run custom runtimes, and absence of deployer-controlled policy. This strengthens Lenny's self-hosted narrative by making the comparison explicit rather than leaving evaluators to infer it.

---

### CPS-048. Kubernetes Requirement Is a Major Adoption Barrier Not Addressed in Competitive Positioning [High]
**Section:** 23.1, 23.2
Every competitor cited in the competitive analysis has a lighter deployment path than Lenny's production deployment: E2B is a hosted API (or self-hosted VMs); Fly.io Sprites is hosted; Modal is hosted; LangSmith has a hosted tier; even Temporal Cloud exists as a managed service. Lenny's v1 production deployment requires: Kubernetes >= 1.27, cert-manager, gVisor or Kata RuntimeClass, Postgres (HA), Redis (Sentinel/Cluster), MinIO or S3-compatible storage, OPA/Gatekeeper or Kyverno, and optionally KEDA. The `make run` local mode exists but is explicitly for development only -- it uses SQLite and in-memory stores and cannot serve real workloads. There is no intermediate deployment path between "single-process dev mode" and "full Kubernetes cluster with 6+ infrastructure dependencies." This makes the "< 5 minute TTHW" claim misleading for the "Platform operators" persona, who need to evaluate Lenny in a production-like setting. The competitive positioning claims Lenny is simpler to self-host than Temporal ("self-hosted Temporal adds significant operational burden") but Lenny's own operational dependency surface is at least as large. This gap is not acknowledged.
**Recommendation:** (1) Add an honest acknowledgment to Section 23.1 differentiator 3 that Lenny's Kubernetes requirement is a trade-off, not purely an advantage: "Lenny trades deployment simplicity for Kubernetes-native operations -- teams without existing Kubernetes infrastructure face a higher adoption barrier than E2B's VM-based or vendor-hosted alternatives." (2) Consider specifying a "Lenny Lite" or single-binary production-adjacent mode (e.g., single-node k3s auto-provisioning, or a docker-compose production mode with gVisor) as a post-v1 item in Section 21 to address the adoption gap for teams without existing K8s clusters.

---

### CPS-049. Adapter Protocol Is Custom gRPC, Not a Standard -- "Runtime-Agnostic" Claim Requires Qualification [Medium]
**Section:** 23.1 (differentiator 1), 15.4
Differentiator 1 states: "Any process that implements the gRPC runtime adapter (Section 15.4) can run as a Lenny agent pod." This is technically true but undersells the coupling: the adapter must implement a Lenny-specific gRPC control protocol with Lenny-specific state machine transitions, Lenny-specific credential rotation flows, Lenny-specific lifecycle channel messages, and a Lenny-specific stdin/stdout JSON Lines binary protocol. The adapter specification (Section 15.4) is 900+ lines of Lenny-proprietary protocol. Calling this "runtime-agnostic" implies a standard or lightweight contract, but in practice it is a Lenny-specific integration that requires non-trivial engineering effort at Standard/Full tier. By comparison, E2B's sandbox API is a simple HTTP REST API; Temporal requires workflow SDK adoption but provides SDKs in 7 languages. The competitive claim that "Temporal and LangGraph require agent logic to use their respective SDKs" while Lenny does not is misleading -- Lenny requires adapter-level SDK integration (the runtime adapter specification), which is architecturally different but not lower-effort than Temporal's workflow SDK.
**Recommendation:** Qualify differentiator 1: after "without coupling to a specific framework" add "Minimum-tier integration requires only stdin/stdout JSON Lines (no SDK, ~50 lines of code); Standard and Full tiers require gRPC adapter integration." This honestly represents the tiered effort while preserving the architectural distinction. Separately, in the competitive table entries for Temporal and LangGraph, distinguish "agent code must use SDK" from "adapter code must use SDK" -- Lenny's architecture separates agent logic from platform integration, which is a real advantage, but the current phrasing obscures the adapter effort.

---

### CPS-050. Comparison Guides Deferred to Phase 17a -- No Competitive Content Available During Pre-Launch Evaluation Window [Medium]
**Section:** 23.2, 18 (Phase 17a)
The competitive positioning relies on "published comparison guides covering Lenny vs E2B, Daytona, Fly.io Sprites, Temporal, Modal, and LangGraph" as Phase 17a deliverables. However, the repository becomes publicly visible at Phase 0 and `CONTRIBUTING.md` ships at Phase 2. Between Phase 0 and Phase 17a (a span of 17 build phases covering the entire development cycle), anyone evaluating the project has access to the code and documentation but no competitive positioning content. Enterprise evaluators discovering the project during this window will form their own comparisons without Lenny's framing. Given the spec's acknowledgment that competitors are actively closing gaps (LangSmith adding A2A + MCP, E2B adding self-hosting), deferring all competitive content to the final phase risks losing the narrative window.
**Recommendation:** Move a "brief competitive overview" document (a condensed version of Section 23 + 23.1) to Phase 2 alongside `CONTRIBUTING.md`. The full comparison guides can remain Phase 17a deliverables, but a high-level positioning page should be available from the first public-facing milestone.

---

### CPS-051. No Explicit Trade-Off Disclosure for the Kubernetes-Only Design Decision [Medium]
**Section:** 23.1
Section 23.1 lists six differentiators but contains no trade-off disclosure. Every architectural decision involves trade-offs, and honest competitive positioning requires acknowledging them. The current narrative is purely advantage-framed. Key undisclosed trade-offs include: (1) Kubernetes-native means no non-Kubernetes deployment path; (2) gateway-centric means all traffic bottlenecks through a single component class; (3) "no shared storage mounts" means workspace materialization adds latency to every session start; (4) "least privilege by default" means more complex credential management than competitors that simply mount API keys; (5) Go-only platform means contributor pool is narrower than Python/TypeScript-first projects. An evaluator reading only Section 23.1 would see no downsides, which undermines credibility.
**Recommendation:** Add a "Known Trade-offs" subsection after Section 23.1's six differentiators. For each major architectural choice, state the trade-off honestly in one sentence. This strengthens credibility with enterprise evaluators who are experienced enough to know that no platform has zero downsides.

---

---

## 16. Warm Pool & Pod Lifecycle (WPL)

### WPL-041. Variant pool formula omits `mode_factor` adjustment for non-session execution modes [Medium]
**Section:** 4.6.2, 5.2
The variant pool formula in Section 4.6.2 is:
```
target_minWarm = ceil(base_demand_p95 * variant_weight * safety_factor * (failover_seconds + pod_startup_seconds)
                      + burst_p99_claims * variant_weight * pod_warmup_seconds)
```
This formula has no `mode_factor` or `burst_mode_factor` divisor. Section 5.2 defines the adjusted formula for non-experiment pools that divides by `mode_factor` and `burst_mode_factor`, and Section 5.2 line 2143 says "For A/B experiment variant pools, apply `variant_weight` as defined in the variant pool formula in Section 4.6.2." This creates an inconsistency: if a variant pool uses a task-mode runtime with `maxTasksPerPod: 50`, the variant formula produces 50x the needed warm pods because it lacks the mode_factor divisor. The base pool adjusted formula (Section 4.6.2, line 585) has the same omission. Both formulas in Section 4.6.2 are written assuming session mode without acknowledgment, making it unclear whether deployers of variant pools with task or concurrent modes should compose the Section 5.2 mode_factor adjustment with the variant weight manually.
**Recommendation:** Add `/ mode_factor` and `/ burst_mode_factor` to both the variant pool formula and the adjusted base pool formula in Section 4.6.2, consistent with the mode-adjusted formula in Section 5.2. Alternatively, state explicitly that the Section 4.6.2 variant formulas assume session mode and that for task/concurrent variant pools, deployers must apply the mode_factor adjustment from Section 5.2.

---

### WPL-042. Delegation-adjusted `minWarm` formula omits `mode_factor` for task/concurrent pools [Medium]
**Section:** 17.8.2
The delegation-adjusted formula (Section 17.8.2, line 9269) is:
```
minWarm >= adjusted_claim_rate * safety_factor * (failover_seconds + pod_startup_seconds)
            + adjusted_burst_claims * pod_warmup_seconds
```
Like the base formula in Section 4.6.2, this assumes session mode (`mode_factor = 1.0`). However, delegation child sessions can target task-mode or concurrent-mode pools. If a delegation child pool uses `executionMode: task` with `maxTasksPerPod: 50`, the formula over-provisions by 50x because the delegation-adjusted claim rate is not divided by `mode_factor`. The worked example uses `pod_warmup_seconds = 35` and arrives at `minWarm: 3,346` for Tier 3 -- this would be ~67 pods for a task-mode pool with `mode_factor = 50`, a massive over-provision.
**Recommendation:** Add `/ mode_factor` and `/ burst_mode_factor` to the delegation-adjusted formula, matching the mode-adjusted formula in Section 5.2.

---

### WPL-043. SDK-warm pods in `sdk_connecting` state not counted in `PoolWarmingUp` condition logic [Low]
**Section:** 5.2, 6.1
The `PoolWarmingUp` condition (Section 5.2 line 2178-2184) is set to True when: `minWarm > 0` AND `idlePodCount == 0` AND "at least one pod is in the `warming` state." For SDK-warm pools, a pod transitions `warming -> sdk_connecting -> idle`. A pool where all pods are in `sdk_connecting` (past `warming` but not yet `idle`) has `idlePodCount == 0` and zero pods in `warming` state. By the condition's definition, this pool would have `reason: Drained` ("fully empty with no controller activity -- an error condition"), which is incorrect -- the pool is actively warming, just in the SDK connect phase. This would incorrectly signal an error condition to operators.
**Recommendation:** Extend the `PoolWarmingUp` condition to also check for pods in `sdk_connecting` state. The condition should be True with `reason: Provisioning` when `idlePodCount == 0` and at least one pod is in `warming` OR `sdk_connecting` state.

---

### WPL-044. `sdk_connecting` watchdog pod replacement not counted as pool replenishment demand [Medium]
**Section:** 6.1, 4.6.1
When the `sdk_connecting` watchdog fires (default: 60s timeout), the pod transitions to `failed` and must be replaced. If SDK warm startup is systematically slow but not completely broken (e.g., consistently taking 55-70s with a 60s timeout), the watchdog fires intermittently, creating a replacement churn cycle: new pod created, enters `sdk_connecting`, times out at 60s, fails, replaced, repeat. This churn consumes pods from the warm pool without increasing the available idle count. The `SDKConnectTimeout` alert fires when the rate exceeds 0.1/min for 5 minutes, but there is no mechanism in the pool sizing formula to account for this replacement demand. The steady-state formula sizes for session claim demand, not for internal watchdog-driven replacement. A pool experiencing intermittent SDK connect timeouts could exhaust its warm capacity without any session claims.
**Recommendation:** Add guidance that deployers of SDK-warm pools should monitor `lenny_warmpool_sdk_connect_timeout_total` rate and, if it contributes materially to pod turnover, either (a) increase `sdkConnectTimeoutSeconds` to accommodate the SDK's actual startup time, or (b) add the observed timeout rate to `base_demand_p95` when computing `minWarm`. Alternatively, have the PoolScalingController automatically factor SDK connect timeout rate into the demand signal.

---

### WPL-045. Task-mode `sdk_connecting` re-warm blocks pod availability for next task assignment [Medium]
**Section:** 6.2, 6.1
The state machine shows `task_cleanup -> sdk_connecting` for preConnect-enabled task-mode pods (line 2393). During `sdk_connecting`, the pod is not in `idle` and is therefore not claimable. If SDK initialization takes 30-60s (the `sdkConnectTimeoutSeconds` default is 60s), the pod is unavailable for next-task dispatch during this entire window -- despite being scrubbed and otherwise ready. For task-mode pods where demotion rates are high (>60%), every task incurs both the scrub delay AND the SDK re-warm delay, then a demotion delay on claim. The net effect is that the pod's inter-task gap includes: scrub time + SDK warm time + demotion time, which can easily exceed 90s. This significantly reduces the effective `mode_factor` below the formula's assumed `avg_tasks_per_pod_lifetime`, because each task's wall-clock overhead is dominated by the re-warm cycle. Section 5.2 acknowledges that `mode_factor` converges toward `maxTasksPerPod` for "predictable workloads" but does not identify SDK re-warm as a factor that degrades it.
**Recommendation:** Add a note in Section 5.2 (Execution Mode Scaling Implications) that for task-mode pools with `preConnect: true`, the inter-task SDK re-warm window (up to `sdkConnectTimeoutSeconds`) reduces effective throughput per pod and lowers the observed `mode_factor`. The PoolScalingController should use observed `lenny_task_reuse_count` p50 (which naturally reflects this overhead) rather than the theoretical `maxTasksPerPod` for such pools. Consider offering a pool-level option to skip SDK re-warm between tasks when the circuit-breaker `sdkWarmDisabled` flag is set or when the demotion rate exceeds the threshold.

---

### WPL-046. Tier 3 recommended `minWarm` baseline (1,050) arithmetic does not match the stated formula inputs [Low]
**Section:** 17.8.2
The warm pool sizing table states Tier 3: expected claim rate 30/s, recommended minWarm 1,050. The Note below the table says values use `safety_factor = 1.0`. The formula below is `minWarm >= claim_rate * safety_factor * (failover_seconds + pod_startup_seconds) + burst_p99_claims * pod_warmup_seconds`. With the stated inputs (claim_rate=30, safety_factor=1.0, failover_seconds=25, pod_startup_seconds=10): `30 * 1.0 * 35 = 1,050`. This matches. However, the burst term is `burst_p99_claims * pod_warmup_seconds`. At safety_factor=1.0 and burst_p99_claims=0, the burst term is 0. But the Note says these are "conservative starting points suitable for a first deployment." A first-deployment baseline with zero burst headroom is not conservative -- it exactly matches the steady-state demand with no margin for any burst. The Tier 1 value (20) similarly equals `0.5 * 1.0 * 35 = 17.5 -> ceil = 18`, not 20 -- this does not match the formula at all (20 requires `safety_factor ~= 1.14` or a non-zero burst term).
**Recommendation:** Clarify how the Tier 1 baseline of 20 was derived (it does not match the formula with safety_factor=1.0 and burst=0, since `ceil(0.5 * 1.0 * 35) = 18`). Consider adding a small non-zero burst term to the baseline values to justify the "conservative" characterization, or remove the word "conservative."

---

### WPL-047. Circuit-breaker `sdkWarmDisabled` flag is set by PoolScalingController but read by WarmPoolController -- no propagation latency bound specified [Low]
**Section:** 6.1, 4.6.3
The SDK-warm circuit-breaker fires when the rolling 5-minute demotion rate exceeds 90%. The PoolScalingController sets `spec.sdkWarmDisabled: true` on the `SandboxWarmPool` CRD (line 2313, line 614). The WarmPoolController reads this flag and stops initiating `sdk_connecting` transitions. But the WarmPoolController watches CRD changes via the Kubernetes API server informer cache, which has a propagation delay (typically seconds, but can be tens of seconds under API server pressure at Tier 3). During this propagation window, the WarmPoolController continues creating new pods in `sdk_connecting` state -- each of which will also be demoted, exacerbating the problem the circuit breaker was meant to stop. No upper bound on this propagation delay is documented.
**Recommendation:** Document the expected CRD watch propagation latency and acknowledge that during this window, a small number of additional SDK-warm pods may be created. Alternatively, have the WarmPoolController read `sdkWarmDisabled` from the API server directly (not cache) when creating a new pod's warm phase, at the cost of one additional API server read per pod creation.

---

### WPL-048. No `maxWarm` default or guidance for experiment variant pools [Low]
**Section:** 4.6.2, 10.7
The `SandboxWarmPool` CRD carries both `spec.minWarm` and `spec.maxWarm`. The variant pool formula computes `target_minWarm` but never mentions `maxWarm`. When the PoolScalingController creates a variant pool's `SandboxWarmPool` CRD, what `maxWarm` value is used? If `maxWarm` is not set or defaults to a very high value, a demand spike on a variant pool (e.g., due to sticky assignment concentrating load) could cause unbounded scale-up. If `maxWarm` defaults to `minWarm`, the pool cannot absorb any burst. The CRD validation requires `minWarm <= maxWarm` and `maxWarm > 0`, but no formula or guidance is provided for variant pool `maxWarm`.
**Recommendation:** Specify how the PoolScalingController determines `maxWarm` for variant pools. A reasonable default would be `max(target_minWarm * 2, base_pool_maxWarm * variant_weight)`, capped by the base pool's original `maxWarm` to prevent a small variant from over-provisioning.

---

5 findings (0 Critical, 0 High, 5 Medium, 3 Low)

---

## 17. Credential Management (CRD)

### CRD-044. `requiresRestartOnProviderSwitch` field is declared in YAML but never defined or referenced [Medium]
**Section:** 5.1

The standalone runtime YAML example (line 1685) includes:

```yaml
credentialCapabilities:
  hotRotation: true
  requiresRestartOnProviderSwitch: true
```

`requiresRestartOnProviderSwitch` appears exactly once in the entire spec -- in this YAML block. It has no prose definition, no description of its semantics, no gateway enforcement logic, and no reference in the credential rotation protocol (Section 4.9), the fallback flow, or the rotation mode resolution paragraph (lines 1373-1378). The rotation mode resolution explicitly states that `credentialCapabilities.hotRotation` is "the authoritative signal for whether credentials can be rotated in-place" and makes no mention of provider switching as a distinct concept.

The field's intended meaning is ambiguous: does it govern rotation within the same provider type (e.g., switching from `key-1` to `key-2` in `anthropic_direct`), or switching between different provider types (e.g., from `anthropic_direct` to `aws_bedrock`) during fallback? The multi-provider lease model (one lease per provider, independent fallback per provider) makes the latter interpretation unclear -- a "provider switch" does not occur in the documented fallback flow, which rotates credentials within a single provider's fallback chain.

**Recommendation:** Either (a) define `requiresRestartOnProviderSwitch` with prose semantics, gateway enforcement, and cross-references to the fallback flow and rotation mode resolution, or (b) remove it from the YAML example if it is a vestigial field from an earlier design iteration. If retained, clarify how it interacts with `hotRotation` -- the current spec offers no precedence rule when `hotRotation: true` and `requiresRestartOnProviderSwitch: true` are both set.

---

### CRD-045. Concurrent-workspace mode credential lifecycle unspecified -- per-slot or per-pod leasing undefined [Medium]
**Section:** 5.2, 4.9

Task-mode credential lifecycle is explicitly specified (line 2291): "Credentials are leased per-task, not per-pod. A fresh credential assignment (`AssignCredentials` RPC) is performed at each task dispatch." Session-mode is one lease per session, one session per pod.

Concurrent-workspace mode (`executionMode: concurrent`, `concurrencyStyle: workspace`) has no equivalent specification. The spec does not define whether:

1. **Credential leases are per-slot or per-pod.** If per-pod, all concurrent slots share a single credential lease -- a credential rotation would affect all active slots simultaneously, and the `maxConcurrentSessions` counter on the pool credential would count one lease for N simultaneous slots (under-counting actual LLM usage). If per-slot, each slot holds an independent lease, and `maxConcurrentSessions` accurately reflects slot-level concurrency -- but the adapter must manage N simultaneous leases and the `/run/lenny/credentials.json` file format (which contains a single `providers` array) cannot represent per-slot credential differentiation.
2. **Credential rotation in concurrent mode.** The Full-tier rotation protocol (Section 4.7, lines 806-817) describes an in-flight gate and `credentials_rotated` message. With multiple concurrent slots, the in-flight gate must wait for all slots' LLM requests to complete, and all slots must acknowledge the rotation. This interaction is not specified.

The Phase 12c integration test gate (line 9740) requires "credential isolation between concurrent slots -- verify no slot receives another slot's credentials," which implies per-slot leasing is the intended design, but Section 4.9 and Section 5.2 never state this.

**Recommendation:** Add a "Concurrent-mode credential lease lifecycle" paragraph to Section 5.2 (adjacent to the task-mode paragraph at line 2291) specifying whether leases are per-slot or per-pod, how the credential file is structured for concurrent slots, and how rotation interacts with multiple active slots.

---

### CRD-046. Re-enable endpoint restores credential to `healthy` with no pre-validation of underlying secret [Medium]
**Section:** 15.1 (admin API table, line 6825)

The `POST /v1/admin/credential-pools/{name}/credentials/{credId}/re-enable` endpoint "restores credential to `healthy` status with a fresh health score." The emergency revocation runbook (Section 17.7, line 1551) specifies: "rotate the underlying secret (Kubernetes Secret or external secrets manager) before re-enabling the credential ID."

However, the re-enable endpoint performs no validation that the underlying Kubernetes Secret has actually been updated. An operator who calls re-enable without first rotating the secret at the provider and updating the Kubernetes Secret will restore a compromised credential to the active pool. The Token Service informer detects Secret changes within 30 seconds (line 1573), but re-enable does not wait for or verify a Secret change since the revocation timestamp.

This creates a dangerous operator workflow: revoke (credential is blocked) then re-enable (credential is immediately available) with no enforced sequencing of the secret rotation step in between.

**Recommendation:** The re-enable endpoint should verify that the Kubernetes Secret referenced by `secretRef` has been modified since the `revokedAt` timestamp (compare the Secret's `resourceVersion` or `metadata.creationTimestamp` against `revokedAt`). If the Secret has not changed, the endpoint should return a warning (not a blocking error, to preserve operator override capability) in the response body: `"warning": "secretRef has not been modified since revocation; ensure the underlying credential material has been rotated at the provider before re-enabling"`. Alternatively, add an `acknowledgeUnrotatedSecret: true` required field when the Secret is unchanged.

---

### CRD-047. Proactive renewal `expiresAt` guard races with Vault-controlled early expiry for `vault_transit` provider [Medium]
**Section:** 4.9 (Proactive Lease Renewal, line 1371; `leaseTTLSeconds` table, line 1096)

The `vault_transit` provider row in the `leaseTTLSeconds` table states: "The Vault token TTL must be at least `leaseTTLSeconds`; a shorter Vault TTL takes precedence and causes the lease to expire early." This means a `vault_transit` lease can expire before its Lenny-computed `expiresAt` if the Vault policy sets a shorter TTL than `leaseTTLSeconds`.

The proactive renewal worker (line 1371) uses `renewBefore = expiresAt - renewBeforeBuffer` to schedule renewal. If the Vault token's actual TTL is shorter than `leaseTTLSeconds`, the Lenny-computed `renewBefore` is too late -- the Vault token may already be expired when the renewal fires. For example: `leaseTTLSeconds: 3600`, Vault policy TTL: 900s, `renewBeforeBuffer: 300s`. Lenny computes `renewBefore` at T+3300 (55 minutes after issuance), but the Vault token expired at T+900 (15 minutes). The session experiences a 40-minute window of silent credential failure before proactive renewal even attempts to fire.

The `expiresAt` guard (line 1371) catches already-expired leases at retry time, but this is a reactive check, not a proactive one -- the damage (failed LLM requests for 40 minutes) has already occurred by the time the retry fires.

**Recommendation:** When the `vault_transit` provider materializes a lease, it should set `expiresAt = min(issuedAt + leaseTTLSeconds, vaultTokenExpiryTime)` so that the Lenny `renewBefore` is computed from the actual Vault token expiry, not from the pool's `leaseTTLSeconds`. The same approach should apply to any custom provider whose underlying token may expire before the Lenny-computed `expiresAt`. Add a note in the provider table that `vault_transit` leases automatically adjust `expiresAt` downward when the Vault token TTL is shorter than `leaseTTLSeconds`.

---

### CRD-048. `lenny-ctl admin credential-pools` command reference covers only `add-credential` -- omits revoke, re-enable, list, and pool-level revoke [Medium]
**Section:** 24.5

Section 24.5 (`lenny-ctl` Credential Management, line 9956-9961) contains a single command:

```
lenny-ctl admin credential-pools add-credential --pool <name> --provider <p>
```

The admin API (Section 15.1, lines 6820-6826) defines seven credential pool endpoints including `POST .../revoke` (single credential), `POST .../revoke` (pool-level), `POST .../re-enable`, `GET` (list), `GET` (single), `PUT` (update), and `DELETE`. The emergency revocation runbook (Section 17.7) instructs operators to "call the revocation endpoint" -- but the CLI command for doing so is absent from the `lenny-ctl` reference.

For an incident response scenario, an operator following the runbook needs to execute the revocation via `lenny-ctl`, not by crafting raw HTTP requests. The missing CLI commands are operationally critical:

- `lenny-ctl admin credential-pools revoke-credential --pool <name> --credential <id>` (emergency single-credential revocation)
- `lenny-ctl admin credential-pools revoke-pool --pool <name>` (emergency full-pool revocation)
- `lenny-ctl admin credential-pools re-enable --pool <name> --credential <id>` (post-rotation re-enablement)
- `lenny-ctl admin credential-pools list` (diagnostic)

**Recommendation:** Add the missing credential pool management commands to Section 24.5 with their API mappings, required roles, and cross-references to the emergency revocation runbook.

---

### CRD-049. User credential `DELETE` vs `POST .../revoke` semantic gap -- active sessions behave differently but no guidance exists [Low]
**Section:** 4.9 (lines 1292-1293)

The spec defines two credential removal operations:

- `DELETE /v1/credentials/{credential_ref}` (line 1293): "Remove a registered credential. Active sessions using this credential are not affected (they hold a lease); new sessions will no longer resolve it."
- `POST /v1/credentials/{credential_ref}/revoke` (line 1292): "Revoke a user-scoped credential and immediately invalidate all active leases backed by it."

These have dramatically different impacts on active sessions, but there is no operator guidance on when to use which. A user who suspects their API key is compromised needs `revoke` (immediate lease termination), but may instinctively reach for `DELETE` (which silently allows the compromised key to remain active in all current sessions until lease expiry). The `DELETE` endpoint description says "Active sessions using this credential are not affected" -- this is correct but dangerous when the user's intent is emergency revocation.

**Recommendation:** Add a callout note after the `DELETE` description: "If the credential is suspected compromised, use `POST .../revoke` instead of `DELETE`. `DELETE` does not terminate active leases -- sessions continue using the credential until their leases expire naturally."

---

### CRD-050. Credential pool sizing formula does not account for task-mode per-task leasing [Low]
**Section:** 17.8.2 (Credential pool sizing, line 9405)

The credential pool sizing formula uses `peak_concurrent_sessions_for_provider` as the numerator. For task-mode pools, credentials are leased per-task (line 2291), not per-session in the traditional sense. A task-mode pod with `maxTasksPerPod: 50` releases and re-acquires a credential lease 50 times over its lifetime, but only holds one at a time. The sizing formula is correct if "sessions" is interpreted as "concurrent active leases" -- but the text defines it as "the number of concurrently active sessions that will hold a credential lease from this pool at peak load" (line 9416), which does not distinguish between session-mode sessions and task-mode tasks.

At Tier 3 with task-mode pools processing short tasks (e.g., 30-second tasks), the instantaneous credential lease count may be significantly lower than the "concurrent sessions" count because leases are released between tasks. Using session-count as the numerator would over-provision the pool. Conversely, if tasks are long-running, the formula is accurate.

**Recommendation:** Add a note to the sizing formula: "For task-mode pools, use the peak number of concurrently executing tasks (not the total number of sessions or total task throughput) as `peak_concurrent_sessions_for_provider`, since credential leases are held per-task and released between tasks (Section 6.1)."

---

## Summary

6 findings (0 Critical, 0 High, 4 Medium, 2 Low)

---

## 18. Content Model & Schema (SCH)

### SCH-059. `allowStandardIsolation` missing from Normative Merge Algorithm table [Medium]
**Section:** 5.1, Normative Merge Algorithm
The "Never overridable on derived runtime" prose (line 1773) lists `allowStandardIsolation` as a prohibited field on derived runtimes. However, the normative merge algorithm table (lines 1783-1807) -- which is the authoritative per-field merge reference -- has no row for `allowStandardIsolation`. This omission means the gateway has no defined merge behavior for the field: should it be "Prohibited" (derived may not set it), "Inherited" (always from base), or something else? Given the prose says "never overridable," it should be "Prohibited," but the authoritative table is silent. Additionally, `allowStandardIsolation` does not appear in the standalone Runtime YAML example (line 1668-1715), making its schema placement ambiguous -- it is only described as a pool configuration flag in Section 5.3 (line 2232).
**Recommendation:** Add `allowStandardIsolation` to the normative merge algorithm table with merge behavior **Prohibited**, consistent with the prose. Clarify whether this field belongs on the Runtime definition or exclusively on the pool configuration, and document it in the standalone Runtime YAML example if applicable.

---

### SCH-060. `observability.otlpEndpoint` referenced in adapter manifest but absent from field reference table [Medium]
**Section:** 5.2 (Execution Modes) / 4.7 (Adapter Manifest)
Section 5.2 (line 1972) states that graph-aware runtimes emit OTel spans "configured against the OTLP collector endpoint injected in the adapter manifest as `observability.otlpEndpoint`." However, the adapter manifest field reference table (lines 760-778) does not include an `observability` or `observability.otlpEndpoint` field. The manifest JSON example (lines 722-757) also omits it. A runtime author following the field reference table would not know this field exists or what its schema is.
**Recommendation:** Add `observability` (object) and `observability.otlpEndpoint` (string, URL of the OTLP collector) to the adapter manifest field reference table and the JSON example. Specify tier relevance (likely "All tiers -- informational") and whether it is optional.

---

### SCH-061. `setupCommands` listed as independently configurable but absent from Normative Merge Algorithm table [Medium]
**Section:** 5.1, Inheritance Rules / Normative Merge Algorithm
The inheritance rules prose (line 1775) lists `setupCommands` as "Independently configurable on derived runtime." The derived runtime YAML example (line 1728) shows `setupCommands` nested inside `workspaceDefaults`. However, the normative merge algorithm table only has a row for `workspaceDefaults` (Append behavior), and the `workspaceDefaults` Append rule describes file-level merging ("derived files appended to base; conflicting paths replaced by derived"). It is ambiguous whether `setupCommands` nested within `workspaceDefaults` follows the same Append semantics (commands appended), the Override semantics, or something else entirely. The WorkspacePlan schema in Section 14 shows `setupCommands` as a top-level field within `workspacePlan` (line 6282), while the derived runtime example shows it nested under `workspaceDefaults`, creating a schema location inconsistency.
**Recommendation:** Add an explicit merge algorithm row for `setupCommands` (either as a standalone row or a documented sub-field of `workspaceDefaults`) specifying whether derived commands are appended after base commands, replace them, or are prohibited. Harmonize the schema location between the derived runtime example and the WorkspacePlan schema.

---

### SCH-062. `TaskResult` schema missing `schemaVersion` field [Medium]
**Section:** 8.8, TaskResult
The `TaskRecord` schema (line 3555) prominently includes a `schemaVersion` field and extensive versioning discussion. However, the `TaskResult` schema (lines 3621-3645) -- returned by `lenny/await_children` and consumed by parent agents -- has no `schemaVersion` field. `TaskResult` is a distinct persisted/transmitted data structure that may evolve independently from `TaskRecord`. Without a version field, parent agents that receive `TaskResult` objects written by a newer gateway version during a rolling upgrade have no way to detect schema mismatch, violating the bifurcated consumer model established in Section 15.5 item 7.
**Recommendation:** Add a `schemaVersion` integer field to `TaskResult` with the same producer/consumer obligations as `TaskRecord.schemaVersion`. Update the JSON example accordingly.

---

### SCH-063. `response` outbound message lacks a schema for structured error reporting [Medium]
**Section:** 15.4.1, Protocol Reference -- Outbound: `response`
The `response` message schema (lines 7671-7684) contains only `type` and `output` (or the `text` shorthand). When a runtime encounters an unrecoverable error during processing, it has no structured way to communicate failure details in the `response` message -- there is no `error` field, no `status` field, and no `isError` flag. The only option is to exit with a non-zero exit code, which loses all error context (code, category, message). By contrast, the `tool_result` inbound message (line 7640) has an `isError` field. The `TaskResult` schema (line 3665) has a structured `error` object. This gap means protocol-level error context from the runtime is lost in the translation layer.
**Recommendation:** Add an optional `error` field to the `response` outbound message schema (e.g., `{"code": string, "message": string}`) or an `isError` boolean, so the adapter can distinguish a successful empty response from a runtime-reported failure without relying solely on exit codes.

---

### SCH-064. `write_file` adapter-local tool only supports UTF-8 text, no binary file support [Low]
**Section:** 15.4.1, Adapter-Local Tool Reference
The `write_file` tool schema (lines 7741-7751) accepts only `content` of type `string` with description "UTF-8 text content to write." There is no mechanism for Minimum-tier runtimes to write binary files (compiled artifacts, images, serialized data) to the workspace through the adapter-local tool interface. The `OutputPart` content model supports binary via base64 in the `inline` field for output, but the input tool for file creation does not. This means Minimum-tier runtimes that need to produce binary workspace artifacts must use a workaround (base64-encode then shell decode) or upgrade to Standard tier for MCP tool access.
**Recommendation:** Add an optional `encoding` field (`"utf8" | "base64"`, default `"utf8"`) to the `write_file` input schema, allowing Minimum-tier runtimes to write binary content by providing base64-encoded `content` with `encoding: "base64"`.

---

### SCH-065. `TaskSpec` schema is minimal and missing several fields referenced elsewhere [Medium]
**Section:** 8.2, Delegation Mechanism
The `TaskSpec` schema (lines 3051-3057) is defined with only two fields: `input` (OutputPart[]) and `workspaceFiles` (with `export` array). However, multiple other sections reference additional `TaskSpec` fields that are not shown in this definition: Section 4.8 (line 1004) describes `PreRoute` interceptors receiving "the task specification after authentication... Contains `input`, requested runtime, workspace files, and delegation parameters." The `agentInterface.supportsWorkspaceFiles` field (line 1869) implies `TaskSpec` has a mechanism for workspace files beyond just export globs. The session creation flow (Section 7.1) passes `runtime`, `pool`, `retryPolicy`, `metadata`, `env`, `runtimeOptions`, and other fields that must be part of the task specification. The minimal two-field schema does not represent the actual shape of the object as used throughout the system.
**Recommendation:** Expand the `TaskSpec` schema definition to include all fields that are part of the delegation task specification, or explicitly document that the schema shown is the delegation-specific subset and cross-reference the full session creation schema (Section 14, WorkspacePlan) for the complete field set.

---

### SCH-066. Billing event `sequence_number` type inconsistency with `corrects_sequence` [Low]
**Section:** 11.2.1, Billing Event Stream
The billing event schema (line 4996) defines `sequence_number` as `uint64` and `corrects_sequence` as `uint64` (line 5007). However, the null/absent field contract (line 5027) states that `corrects_sequence` uses type `uint64 | null` and that `0` is never valid since "sequences start at 1." The schema table declares `corrects_sequence` as `uint64` without the nullable union type, creating a type inconsistency between the table definition and the prose contract. For languages with strict type systems (Go, Rust), this distinction matters for code generation.
**Recommendation:** Update the `corrects_sequence` type in the schema table from `uint64` to `uint64 | null` (or `*uint64` / `optional uint64`) to match the prose contract. Consider applying the same treatment to other conditionally-present uint64 fields like `affected_policy_count` and `leases_terminated`.

---

### SCH-067. `sdkWarmBlockingPaths` and `minPlatformVersion` absent from Normative Merge Algorithm table [Low]
**Section:** 5.1, Normative Merge Algorithm
Two fields present on the Runtime definition -- `sdkWarmBlockingPaths` (line 1642) and `minPlatformVersion` (line 1768) -- have no entry in the normative merge algorithm table. For `sdkWarmBlockingPaths`, it is unclear whether a derived runtime can customize its blocking paths (which would make sense operationally -- a derived runtime might have different SDK initialization requirements). For `minPlatformVersion`, a derived runtime might want to declare a higher minimum version than its base. The table's completeness is important because it is the authoritative reference for gateway validation logic.
**Recommendation:** Add rows for `sdkWarmBlockingPaths` (likely Override -- derived can customize) and `minPlatformVersion` (likely Maximum -- gateway uses max(base, derived) to ensure the stricter version requirement applies) to the normative merge algorithm table.

---

### SCH-068. `sharedAssets` field referenced in pod filesystem layout but never defined on RuntimeDefinition [Medium]
**Section:** 6.4 (Pod Filesystem Layout) / 5.1 (Runtime)
Section 6.4 (line 2615) states: "`/workspace/shared/` is populated by the gateway during pod initialization from the Runtime's `sharedAssets` configuration -- a list of artifact references or inline file specs." However, the `sharedAssets` field does not appear anywhere in the RuntimeDefinition schema (Section 5.1), the standalone or derived runtime YAML examples, or the normative merge algorithm table. A runtime author or deployer cannot configure shared assets because the field's schema, location, and format are unspecified.
**Recommendation:** Define the `sharedAssets` field on the RuntimeDefinition schema in Section 5.1 with its schema (array of artifact references or inline file specs, mirroring the WorkspacePlan sources format), add it to the standalone runtime YAML example, and add it to the normative merge algorithm table.

---

### SCH-069. `capabilities.preConnect` listed as "Prohibited" on derived runtimes but `capabilities.injection` is the only `capabilities` sub-field in the merge table [Low]
**Section:** 5.1, Normative Merge Algorithm
The merge algorithm table has rows for `capabilities.interaction` (Prohibited) and `capabilities.injection` (Prohibited), but no row for `capabilities.preConnect`. The inheritance rules prose (line 1773) does not list `preConnect` either -- it lists only `capabilities.interaction` under the "Never overridable" category. This leaves `capabilities.preConnect` with undefined merge behavior. Since `preConnect` has deep security implications (it requires `DemoteSDK` support), leaving its inheritability undefined is a gap.
**Recommendation:** Add `capabilities.preConnect` to the normative merge algorithm table. Given that it controls SDK lifecycle behavior tied to the base runtime's adapter implementation, it should likely be **Prohibited** (derived runtimes cannot change whether the base runtime pre-connects).

---

### SCH-070. Webhook event payload uses `snake_case` while REST API and internal schemas use `camelCase` [Low]
**Section:** 14, WorkspacePlan / callbackUrl webhook
The webhook delivery payload schema (lines 6340-6352) uses `snake_case` field names (`session_id`, `idempotency_key`), while the REST API session responses, `OutputPart`, `MessageEnvelope`, `TaskRecord`, `TaskResult`, `CredentialLease`, and virtually all other data schemas in the spec use `camelCase` (`sessionId`, `taskId`, `schemaVersion`). The billing event schema (Section 11.2.1) also uses `snake_case` (`sequence_number`, `tenant_id`). This creates two distinct naming conventions across the platform's data formats. Client SDK implementations must handle both conventions, increasing integration complexity and error surface.
**Recommendation:** Pick one convention and apply it uniformly. Since the majority of client-facing schemas (OutputPart, MessageEnvelope, TaskResult, CredentialLease, REST API) use `camelCase`, the webhook payload and billing event schemas should be aligned to `camelCase` for consistency. If `snake_case` is intentional for billing/webhook payloads (e.g., to match common webhook conventions), document this as an explicit convention and ensure client SDKs handle both.

---

---

## 19. Build Sequence (BLD)

### BLD-050. Phase 2 checkpoint duration benchmark requires Full-tier lifecycle channel not yet available [High]
**Section:** 18 (Phase 2, Phase 2.8)

Phase 2 includes a "Checkpoint duration benchmark: measures end-to-end checkpoint time across workspace sizes (10MB, 100MB, 500MB) and validates the < 2s SLO for <= 100MB workspaces (see Section 4.4)." However, Section 4.4 specifies that consistent checkpoints require the Full-tier lifecycle channel (`checkpoint_request`/`checkpoint_ready` handshake). The echo runtime shipped in Phase 2 is Minimum-tier and does not implement the lifecycle channel. The `streaming-echo` runtime that implements Full-tier lifecycle channel support is not introduced until Phase 2.8. This means the Phase 2 checkpoint benchmark can only measure best-effort checkpoints (tagged `consistency: best-effort`) -- not the cooperative quiescence path that production deployments will actually use. The < 2s SLO validated against best-effort snapshots (no quiescence overhead) is not representative of the Full-tier path that adds a round-trip handshake delay.

**Recommendation:** Either move the checkpoint duration benchmark to Phase 2.8 (where `streaming-echo` with configurable `checkpoint_ready` delay is available), or explicitly scope the Phase 2 benchmark as "best-effort checkpoint baseline only" and add a re-validation requirement in Phase 2.8 using `streaming-echo` with its configurable checkpoint delay to measure realistic Full-tier checkpoint duration including the quiescence handshake overhead.

---

### BLD-051. `SandboxClaim` admission webhook (`lenny-sandboxclaim-guard`) has no build phase assignment [Medium]
**Section:** 18; 4.6.1

Section 4.6.1 specifies the `lenny-sandboxclaim-guard` ValidatingAdmissionWebhook as a critical defense-in-depth component for double-claim prevention. It is deployed with `failurePolicy: Fail` (fail-closed), meaning if absent, all `SandboxClaim` operations are blocked. The webhook has its own deployment (`replicas: 2`), PodDisruptionBudget, a dedicated metric (`lenny_sandboxclaim_guard_rejections_total`), and a Critical-severity alert (`SandboxClaimGuardUnavailable`).

No build phase mentions this webhook. Phase 3.5 deploys "admission policy deployment (RuntimeClass-aware PSS enforcement, `shareProcessNamespace` validation)" but does not mention the `SandboxClaim` guard webhook. Phase 1 defines the `SandboxClaim` CRD. Pod claiming begins in Phase 2 ("Can start an agent session"). This means sessions are being claimed from Phase 2 onward without the double-claim prevention webhook that Section 4.6.1 describes as essential for correctness under the failover window.

**Recommendation:** Add `lenny-sandboxclaim-guard` webhook deployment to Phase 3 (alongside PoolScalingController and pod lifecycle) or Phase 3.5 (alongside admission policy deployment). The webhook must be operational before any multi-replica gateway deployment where concurrent claims are possible.

---

### BLD-052. Phase 13.5 incremental baseline comparison references Phase 6.5/9.5/11.5 but cannot detect regressions introduced by Phases 12a-12c [Medium]
**Section:** 18 (Phase 13.5)

Phase 13.5 states: "Compare against Phase 6.5/9.5/11.5 incremental baselines to validate no regression was introduced during Phases 10-13." However, Phases 12a, 12b, and 12c are parallel tracks that may complete at different times. The incremental load tests at Phases 6.5, 9.5, and 11.5 were all run before any Phase 12 work began. Phase 13.5 compares against those pre-Phase-12 baselines to detect regressions in Phases 10-13, but the Phase 12 tracks introduce significant new code paths:

- Phase 12a: KMS envelope encryption on Token Service (affects credential assignment latency)
- Phase 12b: `type: mcp` runtime lifecycle (new session creation path)
- Phase 12c: Concurrent execution modes with `slotId` multiplexing (new workspace management logic)

None of these have their own incremental load test. If Phase 12c introduces a latency regression in session creation (due to slot allocation overhead), Phase 13.5 would detect it only in the aggregate comparison, with no way to attribute it to the specific Phase 12 track that caused it.

**Recommendation:** Add a brief integration performance gate to each Phase 12 track's definition of done: after merging, re-run the Phase 11.5 credential-path scenarios and the Phase 6.5 session-creation scenarios to verify no regression. This need not be a full load test -- a focused comparison of the specific path exercised by each Phase 12 track against the Phase 11.5 baseline suffices.

---

### BLD-053. `RuntimeUpgrade` state machine has no build phase assignment [Medium]
**Section:** 18; 10.5

Section 10.5 defines the `RuntimeUpgrade` state machine -- a multi-step, pauseable, rollback-capable mechanism for production runtime image upgrades. It includes `Pending`, `Expanding`, `Draining`, `Contracting`, `Paused`, `Complete`, and `Failed` states, integration with schema migrations (expand-contract coordination), admin API endpoints (`POST /v1/admin/pools/{name}/upgrade/start`, `/proceed`, `/pause`, `/resume`, `/rollback`), and `lenny-ctl` CLI commands. The state machine interacts with the PoolScalingController, the WarmPoolController (which blocks `SandboxTemplate` deletion during active upgrades), and the gateway (which blocks Phase 3 migration attempts while `upgradeState != Complete`).

No build phase mentions the `RuntimeUpgrade` state machine or its admin API endpoints. The pool management CLI commands (`lenny-ctl admin pools upgrade *`) reference it extensively, but Section 24.4 is a CLI reference, not a build deliverable. The state machine is operationally essential for any production deployment that needs to update runtime images.

**Recommendation:** Add `RuntimeUpgrade` state machine implementation to Phase 3 (alongside PoolScalingController, which owns the reconciliation) or as a dedicated sub-phase. Include the admin API upgrade endpoints and the `lenny-ctl` upgrade CLI as part of the deliverable. The state machine should be testable against the echo runtime (upgrade from echo v1 to echo v2 image).

---

### BLD-054. Phase 2 `make run` uses SQLite but Phase 1.5 mandates Postgres migration framework -- no SQLite migration path specified [Medium]
**Section:** 18 (Phase 1.5, Phase 2); 17.4

Phase 1.5 establishes the database migration framework: "all data models introduced in Phase 1 (sessions, tasks, tenants, billing events) must be expressed as numbered migration files (`migrations/0001_initial.sql`, etc.)" and "CI must run all pending migrations against a clean Postgres instance." Phase 2 introduces `make run` local dev mode with "Embedded SQLite replaces Postgres for session and metadata storage."

The migration files are SQL migrations targeting Postgres (with Postgres-specific features: RLS policies, `SET LOCAL`, `connect_query`, PL/pgSQL gate checks). SQLite does not support RLS, `SET LOCAL`, or PL/pgSQL. This creates a fork: either the `make run` mode ignores the migration framework entirely (running a separate SQLite schema), or the migrations must be written in a dialect-portable subset. Neither approach is specified.

If `make run` uses a separate SQLite schema, any schema drift between the Postgres migrations and the SQLite schema would cause the "< 5-minute Time to Hello World" path to exercise different data model behavior than production. If migrations must be portable, the Phase 1.5 migration framework needs dialect-aware tooling.

**Recommendation:** Add an explicit note to Phase 1.5 or Phase 2 specifying how the SQLite dev-mode schema relates to the Postgres migration files. Recommended approach: generate the SQLite schema from the Postgres migrations at build time (stripping Postgres-specific features like RLS, triggers), and include a CI test that verifies the SQLite schema is structurally equivalent to the Postgres schema for the core tables (columns, types, constraints minus RLS).

---

### BLD-055. Phase 5.8 LLM Proxy depends on SPIFFE identity but no phase establishes SPIFFE infrastructure [Medium]
**Section:** 18 (Phase 5.8); 4.9

Phase 5.8 lists "SPIFFE-binding for lease tokens (Section 4.9 -- v1 requirement for multi-tenant deployments)" as a deliverable. SPIFFE identity binding requires a SPIFFE-compliant identity provider (typically SPIRE or cert-manager with SPIFFE support) to issue SPIFFE SVIDs to pods. The mTLS PKI established in Phase 3 uses cert-manager with `ClusterIssuer` for certificate issuance, but cert-manager's standard `Certificate` resources do not produce SPIFFE SVIDs unless specifically configured with a SPIFFE-compatible issuer (e.g., `cert-manager-csi-driver-spiffe` or an external SPIRE server).

No build phase mentions SPIRE installation, SPIFFE trust domain configuration, SVID issuance to pods, or the specific cert-manager SPIFFE integration required. Phase 3's mTLS PKI setup covers "certificate issuance for gateway replicas and controller" and "trust bundle distribution to agent pods" -- standard X.509 certificates, not necessarily SPIFFE SVIDs.

**Recommendation:** Either add SPIFFE infrastructure setup (trust domain, SVID issuance) as a deliverable in Phase 3 (alongside mTLS PKI), or add it as a Phase 5.8 prerequisite with explicit tooling requirements (SPIRE server or cert-manager SPIFFE CSI driver). If SPIFFE is delivered via cert-manager, specify which cert-manager add-on is required and add it to the Phase 3 mTLS PKI deliverables.

---

### BLD-056. Phase 17b `MemoryStore` is listed in the erasure scope table but erasure infrastructure is validated at Phase 14 -- no re-validation gate [Low]
**Section:** 18 (Phase 14, Phase 17b); 12.8

Section 12.8's erasure scope table includes `MemoryStore` as a store that must be covered by `DeleteByUser` and `DeleteByTenant`. Phase 14 performs "comprehensive security audit and penetration testing" and Phase 13 delivers the full audit logging infrastructure including erasure receipts. However, `MemoryStore` is not implemented until Phase 17b ("Advanced platform features").

When `MemoryStore` arrives in Phase 17b, the erasure job must be extended to include `MemoryStore.DeleteByUser` and `MemoryStore.DeleteByTenant` in its deletion sequence. This new erasure path is not covered by any load test or security audit since Phase 14 (security audit) and Phase 14.5 (SLO re-validation) have already completed. A `MemoryStore` implementation that fails to properly scope `DeleteByUser` by `tenant_id` (despite the interface contract) would be a GDPR compliance gap that no existing gate catches.

**Recommendation:** Add a note to Phase 17b requiring that the `MemoryStore` erasure integration pass the existing `TestMemoryStoreTenantIsolation` contract test and a dedicated erasure round-trip test (write memory, run erasure, confirm deletion) before merge. This is a lightweight gate, not a full re-audit.

---

### BLD-057. Phase 15 (Environment resource) has no stated dependency on Phase 14 but follows it in the sequence [Low]
**Section:** 18 (Phase 14, Phase 14.5, Phase 15)

The build sequence places Phase 15 (Environment resource with tag-based selectors, member RBAC, `mcpRuntimeFilters`) after Phase 14.5 (post-hardening SLO re-validation). Phase 15 is a feature phase that adds RBAC and environment-based access control. It has no dependency on security hardening (Phase 14) or load testing (Phase 14.5) -- it depends on the Admin API (Phase 4.5), the runtime registry (Phase 5), and the delegation policy infrastructure (Phase 3/9).

The implied sequential ordering means Phase 15 cannot begin until Phase 14.5 completes. Since Phase 14 includes a full security audit and penetration test (potentially a weeks-long external engagement), Phase 15 sits idle during this period. Phase 15 is on the critical path to Phase 16 (experiments, which BLD-042/BLD-022 identified as requiring Phase 15's Environment resource), and Phase 16 is on the critical path to Phase 17a (community launch).

**Recommendation:** Add a note that Phase 15 can begin in parallel with Phase 14/14.5, since it has no dependency on security hardening results. Its deliverables should be included in Phase 14.5's SLO re-validation scope (or Phase 16.5's experiment re-validation) to catch any performance impact.

---

## Summary

8 findings (0 Critical, 1 High, 5 Medium, 2 Low)

---

## 20. Failure Modes & Resilience (FLR)

### FLR-051. Billing Redis Stream TTL Expiry Creates Silent Data Loss Without Operator Escalation Path [High]
**Section:** 12.3 (Write classification), 11.2.1 (Billing durability)
The billing Redis stream has a TTL of 3600s (1 hour). The spec states events not flushed to Postgres within this window "are permanently lost." The `BillingStreamBackpressure` alert fires at 80% of MAXLEN but there is no alert that fires when stream entries are approaching their TTL expiry. A Postgres outage lasting longer than 1 hour (e.g., a failed migration, a manual maintenance overrun, or a cross-region failover) would silently expire billing events from the stream with no notification beyond the existing backpressure alert on depth. The spec does not define an alert on stream entry age or a mechanism to extend the TTL under sustained Postgres unavailability.
**Recommendation:** Add a `BillingStreamEntryAgeHigh` alert that fires when the oldest unacknowledged entry in any tenant's billing stream exceeds 80% of `billingStreamTTLSeconds` (default: 2880s). Additionally, specify that the flusher goroutine must log a CRITICAL-level message when it detects entries within 5 minutes of TTL expiry, and define an operator escalation procedure (e.g., extend TTL via `EXPIRE` or resolve Postgres before the deadline).

---

### FLR-052. Orphan Session Reconciler Depends on `agent_pod_state` Mirror Table Freshness but No Staleness Detection Exists [Medium]
**Section:** 10.1 (Coordinator-loss detection, orphan session reconciliation)
The orphan session reconciler cross-references the `agent_pod_state` Postgres mirror table to detect sessions whose pods have terminated. This table is updated "by the WarmPoolController on every state transition." During a WarmPoolController crash (up to 25s failover), the mirror table becomes stale. If a pod terminates during this window (e.g., node failure), the mirror table will not reflect `Terminated` phase, and the orphan reconciler will not detect the orphan until the WarmPoolController catches up. This is a 60s reconcile interval + 25s controller failover = potential 85s window where a session holds resources with a dead pod. However, the deeper issue is that there is no staleness detection on the mirror table itself -- if the WarmPoolController has a bug or sustained degradation in mirror writes, the orphan reconciler silently trusts stale data indefinitely.
**Recommendation:** Add a `lenny_agent_pod_state_mirror_lag_seconds` gauge measuring the time since the last successful mirror update per pool. Fire a `PodStateMirrorStale` warning alert when lag exceeds 60s. The orphan reconciler should fall back to direct Kubernetes API queries for sessions in non-terminal states when mirror staleness exceeds a threshold.

---

### FLR-053. Gateway Subsystem Circuit Breakers Lack Coordination Across Replicas [Medium]
**Section:** 4.1 (Gateway Internal Subsystems), 11.6 (Circuit Breakers)
The per-subsystem automatic circuit breakers (Stream Proxy, Upload Handler, MCP Fabric, LLM Proxy) are described as "in-memory, managed by each gateway replica." Unlike the operator-managed circuit breakers (which are Redis-backed with pub/sub propagation), automatic subsystem breakers are purely local. This means one replica can have its Upload Handler in open state (returning 503) while another replica's Upload Handler is healthy. Clients behind a load balancer will experience non-deterministic upload failures -- some requests succeed, some fail with 503, depending on which replica handles the request. The spec does not define how clients should interpret this or whether sticky routing mitigates it. More critically, if a downstream dependency (e.g., MinIO) is genuinely degraded, each replica independently discovers the problem with its own failure count, meaning the first N requests per replica fail before the breaker opens -- multiplied across all replicas.
**Recommendation:** Specify whether per-subsystem circuit breaker state should be shared across replicas (e.g., via Redis pub/sub, similar to operator-managed breakers) for cases where the underlying failure is infrastructure-wide (MinIO down, upstream LLM provider degraded). If local-only is the intended design, document the expected client behavior (retries will land on healthy replicas) and confirm that sticky routing is explicitly not recommended when subsystem degradation is infrastructure-wide.

---

### FLR-054. Concurrent-Workspace Pod Failure During Eviction Creates Multiplied MinIO Checkpoint Load [Medium]
**Section:** 4.4 (Checkpoint storage failure), 5.2 (Concurrent-workspace mode), 6.2 (Pod State Machine)
A concurrent-workspace pod with `maxConcurrent: 8` active slots that is evicted will trigger 8 simultaneous eviction checkpoint uploads to MinIO (one per slot, since checkpoints are per-slot per Section 5.2 and 12.5). The eviction checkpoint retry budget (30s total, Section 4.4) applies per checkpoint, not per pod. Eight concurrent checkpoint uploads from the same pod will create 8x the MinIO write load, and if MinIO is already degraded, all 8 will sequentially exhaust their retry budgets and fall back to 8 Postgres minimal state records. The total Postgres retry budget for 8 sessions (8 x 60s = 480s) may exceed `terminationGracePeriodSeconds` (240s at Tier 1/2), meaning some sessions' fallback writes may be killed by SIGKILL before completing.
**Recommendation:** For concurrent-workspace pods, specify that eviction checkpoints are serialized or batched (not fully parallel) to avoid MinIO write amplification. Alternatively, specify that `terminationGracePeriodSeconds` for concurrent-workspace pools must be at least `maxConcurrent * max(checkpoint_retry_budget, postgres_fallback_retry_budget) + stream_drain_budget`, and add a CRD validation rule enforcing this constraint.

---

### FLR-055. `CoordinatorFence` Retry Exhaustion Leaves Session in Limbo During Dual-Store Unavailability [Medium]
**Section:** 10.1 (Coordinator handoff protocol, Dual-store unavailability)
The coordinator handoff protocol specifies that if `CoordinatorFence` fails after 3 retries, the new coordinator "must relinquish the lease and back off." During dual-store unavailability, item 3 states "Coordination handoffs are frozen." However, there is a gap: if a coordinating replica crashes during dual-store unavailability, no new coordinator can acquire the session (Postgres is down for generation increment). The pod enters hold state with a 120s timeout (`coordinatorHoldTimeoutSeconds`). If dual-store unavailability lasts longer than 120s -- which is possible since `dualStoreUnavailableMaxSeconds` defaults to 60s but does not control the actual duration of the outage -- the adapter self-terminates the session. But the spec says sessions whose coordinator crashes are "governed by `coordinatorHoldTimeoutSeconds`... not by a new coordinator's dual-store timer." This means `dualStoreUnavailableMaxSeconds` (60s) and `coordinatorHoldTimeoutSeconds` (120s) create an inconsistency: the platform claims to bound degraded mode at 60s, but sessions losing their coordinator during the outage are actually bounded at 120s of inactivity before forced termination.
**Recommendation:** Explicitly document that the effective degraded window for any session during dual-store unavailability is `max(dualStoreUnavailableMaxSeconds, coordinatorHoldTimeoutSeconds)` = 120s (this is stated in the spec but buried in item 4). Add this formula to the operational defaults table (Section 17.8.1) so operators can reason about the combined worst case. Consider whether `coordinatorHoldTimeoutSeconds` should default to match or be less than `dualStoreUnavailableMaxSeconds` to avoid this asymmetry.

---

### FLR-056. Pre-Drain MinIO Health Check Webhook Does Not Cover Spontaneous Evictions or Cluster Autoscaler Pod Deletions [Medium]
**Section:** 12.5 (Pre-drain MinIO health check)
The pre-drain webhook intercepts `CREATE` operations on `pods/eviction` resources. However, Kubernetes node failures, OOM kills, and preemptions do not go through the eviction API -- they result in direct pod deletion or node-level kubelet termination. The spec acknowledges that "the total-loss path is most likely to occur during spontaneous node failures" (Section 4.4), but the pre-drain webhook provides no protection for this class of failures. Additionally, the cluster autoscaler uses the eviction API but can be configured with `--skip-nodes-with-local-storage=false` which may bypass PDB protections. The spec does not address whether the webhook covers cluster-autoscaler-initiated scale-downs that use `DELETE` instead of eviction.
**Recommendation:** Clarify that the pre-drain webhook is a mitigation for planned drains only and cannot protect against spontaneous failures. Document that the `CheckpointStale` alert and the periodic checkpoint freshness SLO (Section 4.4) are the primary defenses against spontaneous eviction data loss. Consider adding a note that the cluster autoscaler's eviction behavior should be validated -- some autoscaler implementations use pod deletion rather than the eviction API under certain conditions.

---

### FLR-057. Billing Stream Consumer Group `XAUTOCLAIM` Reclaim Interval Creates 60s Blackout on Replica Crash [Medium]
**Section:** 11.2.1 (Billing durability, Redis stream)
The `XAUTOCLAIM` reclaim interval is 30s with `min-idle: 60s`. When a gateway replica crashes while processing billing events from the Redis stream, any entries that were `XREADGROUP`-delivered to that replica but not yet `XACK`ed become pending. These entries will not be reclaimed by another replica until `min-idle` (60s) elapses, plus up to one reclaim interval (30s) -- a worst-case 90s blackout before those specific billing events resume processing. During a Postgres recovery scenario where billing events are actively being flushed, this creates a 90s gap where specific tenant billing events are stuck in the pending list. If the stream TTL (3600s) is already partially consumed, the 90s delay further narrows the recovery window.
**Recommendation:** Reduce `min-idle` to 30s and the reclaim interval to 15s, or make both configurable. Alternatively, specify that on gateway startup, each replica should immediately attempt `XAUTOCLAIM` with `min-idle: 0` for its own consumer group to fast-recover any entries assigned to a crashed predecessor with the same consumer name (pod ID).

---

### FLR-058. Token Service Unavailability During Proactive Lease Renewal Triggers Unnecessary Pod Restarts for Standard/Minimum-Tier Runtimes [Medium]
**Section:** 4.3 (Token Service HA), 4.9 (Proactive Lease Renewal, Credential rotation by tier)
When the Token Service is unavailable, the gateway's circuit breaker opens for new credential operations. However, the proactive lease renewal worker (Section 4.9) runs inside the gateway and calls "the same path as `AssignCredentials`" which requires the Token Service. If all 3 proactive renewal retries fail because the Token Service is down, the session falls through to the standard Fallback Flow, which for Standard/Minimum-tier runtimes triggers "Checkpoint -> terminates pod -> schedules replacement pod -> AssignCredentials (new lease) -> Resume." But the replacement pod also needs `AssignCredentials`, which also requires the Token Service -- creating a restart loop. The spec notes that the Token Service circuit breaker prevents new sessions but does not address this circular dependency for renewal-triggered restarts of existing sessions.
**Recommendation:** Specify that when the Token Service circuit breaker is open, the proactive renewal worker should extend the existing lease's `expiresAt` locally (the adapter-side timer) by one additional `renewBeforeBuffer` interval rather than triggering the fallback flow. This keeps the session alive on its current (still-valid, if not yet expired) credential until the Token Service recovers. The fallback flow should only be triggered when the credential has actually expired or been rejected by the provider, not when renewal infrastructure is unavailable.

---

### FLR-059. `maxTreeRecoverySeconds` Default Truncates Leaf Recovery for Trees of Depth 2 or Greater [Low]
**Section:** 8.10 (Delegation Tree Recovery)
The spec documents that the default `maxTreeRecoverySeconds` (600s) "intentionally truncates leaf node resume windows" and provides a formula for deployers to calculate the correct value for deeper trees. However, even a depth-2 tree with defaults requires `900 + (2-1) * 120 + 120 = 1140s` -- nearly double the default. This means any deployment using delegation (depth >= 2) with default settings will have leaf nodes force-terminated during tree recovery. The default is documented as intentional but creates a surprising failure mode where leaf sessions that would otherwise be recoverable are prematurely killed for deployments that have not tuned this parameter.
**Recommendation:** Either increase the default `maxTreeRecoverySeconds` to at least `maxResumeWindowSeconds + maxLevelRecoverySeconds + buffer` (= 1140s) to cover the common depth-2 case, or add a startup validation warning when any `DelegationPolicy` permits depth > 1 and `maxTreeRecoverySeconds` has not been explicitly set above the default.

---

### FLR-060. Gateway Rolling Update With KEDA Can Cause Scaling Oscillation During preStop Drain [Low]
**Section:** 10.1 (preStop hook drain, KEDA)
During a rolling update, the preStop hook sets readiness to false (stage 1) and begins draining streams. KEDA monitors `lenny_gateway_request_queue_depth` with a 10s polling interval. As streams drain from the terminating replica to surviving replicas, the surviving replicas' queue depth spikes, potentially triggering KEDA to scale up new replicas. Simultaneously, the terminating replica's streams eventually complete and the process exits, reducing total load -- at which point KEDA may scale down the newly-added replicas. This oscillation is bounded by the scale-down stabilization window (300s), but it means every rolling update may temporarily add unnecessary replicas. The spec does not address this interaction between preStop drain behavior and KEDA's reactive scaling.
**Recommendation:** Document that KEDA's `behavior.scaleDown.stabilizationWindowSeconds: 300` is intended to absorb this temporary spike. Consider recommending `behavior.scaleUp.stabilizationWindowSeconds` of 30-60s (instead of 0) specifically during rolling updates, or using a KEDA `ScaledObject` pause annotation during planned rollouts.

---

### FLR-061. Semantic Cache Redis Entries Have No Defined Eviction Policy Under Memory Pressure [Low]
**Section:** 4.9 (Semantic Caching), 12.4 (Redis HA and Failure Modes)
The semantic cache stores entries in Redis under `t:{tenant_id}:scache:{scope}:{hash}`. The Redis failure behavior table (Section 12.4) does not list semantic cache as a use case. If Redis approaches its `maxmemory` limit, the eviction policy (presumably `allkeys-lru` or `volatile-lru`) will evict cache entries alongside potentially more critical data like lease renewals or quota counters. The spec does not define a TTL for semantic cache entries, does not specify which Redis instance they should reside on (coordination? cache/pub-sub?), and does not specify their eviction priority relative to other Redis-backed roles.
**Recommendation:** Add semantic cache to the Redis failure behavior table with behavior "Cache miss -> re-compute from LLM (higher latency, higher cost)." Specify a TTL for semantic cache entries. When the Redis logical separation (Section 12.4) is deployed, assign semantic cache to the Cache/Pub-Sub instance. Configure semantic cache keys with `volatile-lru` eviction policy (set a TTL on all cache entries) so they are evicted before non-TTL coordination keys.

---

### FLR-062. Checkpoint Quiescence for Full-Tier During preStop Creates Unbounded Agent Freeze When Multiple Sessions Share a Gateway Replica [Low]
**Section:** 10.1 (preStop hook drain, stage 2), 4.4 (Checkpoint quiescence)
During preStop stage 2, the gateway waits for in-flight checkpoints on all sessions it coordinates. Full-tier runtimes are quiesced during checkpoint (Section 4.4 -- the runtime is paused via the lifecycle channel). If a gateway replica coordinates many Full-tier sessions and a preStop hook triggers checkpoints for all of them, each session's runtime is quiesced for the duration of its checkpoint upload. While the tiered cap per session is 30-90s, the sessions are checkpointed concurrently (not serially), so the total stage-2 duration is bounded by the longest individual checkpoint. However, if MinIO is slow (not down, just degraded), all sessions may sit at their tier cap simultaneously, with their runtimes frozen. The spec does not address whether the CheckpointBarrier protocol (stage 1.5) or the stage-2 checkpoint wait should have a separate aggregate budget across all sessions, or whether they inherit the individual tiered caps.
**Recommendation:** Clarify that stage 2 waits for the maximum of individual checkpoint caps (not the sum), confirming checkpoints run in parallel. Add a note that MinIO degradation during preStop affects all coordinated Full-tier sessions simultaneously and operators should monitor `lenny_checkpoint_duration_seconds` during rolling updates.

---

---

## 21. Experimentation (EXP)

### EXP-044. `EvalResult` schema missing `tenant_id` column [Medium]
**Section:** 10.7 (Experiment Primitives), lines 4771-4783
The `EvalResult` schema table lists fields `id`, `session_id`, `experiment_id`, `variant_id`, `scorer`, `score`, `scores`, `metadata`, and `created_at`. However, Section 4.2 (line 237) classifies "eval results" as tenant-scoped with `tenant_id` column + RLS. The schema table does not include `tenant_id` as a field, even though the resource-tenant-scoping classification table requires it for RLS to function. Without `tenant_id`, RLS policies cannot filter `EvalResult` rows, and the `lenny_tenant_guard` trigger would fail or be bypassed.
**Recommendation:** Add `tenant_id` (string, non-null, indexed) to the `EvalResult` schema table, consistent with all other tenant-scoped tables and the classification in Section 4.2.

### EXP-045. Delegation lease JSON structure omits `experimentContext` field [Medium]
**Section:** 8.3 (Delegation Policy and Lease), lines 3188-3212; 10.7 (Experiment Primitives), lines 4757-4767
Section 10.7 states "Experiment context propagates through delegation leases" and shows an `experimentContext` object with `experimentId`, `variantId`, and `inherited` fields. However, the canonical delegation lease JSON structure in Section 8.3 (lines 3188-3212) does not include `experimentContext` among its fields. This omission means the authoritative lease definition and the experimentation section are inconsistent -- an implementer reading Section 8.3 alone would not know to include experiment context in the lease.
**Recommendation:** Add `experimentContext` (object, nullable) to the delegation lease JSON example and field documentation in Section 8.3, with a cross-reference to Section 10.7 for propagation semantics.

### EXP-046. Session creation flow does not specify when experiment assignment occurs [Medium]
**Section:** 7.1 (Normal Flow), lines 2636-2665; 10.7 (Experiment Primitives); 4.8 (`ExperimentRouter` at `PreRoute`)
The session creation flow (Section 7.1, steps 1-8) describes authentication, policy evaluation, credential check, pool selection, credential assignment, etc. but never mentions experiment assignment. The `ExperimentRouter` interceptor fires at the `PreRoute` phase (priority 300, Section 4.8 line 1024), which implies it runs between step 2 (policy evaluation) and step 4 (pool selection). However, this is never made explicit in the session creation sequence. Since experiment assignment determines which pool is used (variant pool vs. base pool), the timing is critical to correctness and should be documented in the authoritative flow.
**Recommendation:** Add an explicit experiment assignment step between steps 2 and 3 (or between 3 and 4) in the session creation flow in Section 7.1, referencing the `ExperimentRouter` interceptor at the `PreRoute` phase.

### EXP-047. Eval results for concluded experiment sessions stored with `experiment_id: null` loses attribution [Medium]
**Section:** 10.7 (Experiment Primitives), line 4812
The Eval Submission Contract states: "Submissions against `concluded` experiments' sessions are accepted (the session state, not the experiment state, governs eligibility), but the eval is stored with `experiment_id: null` if the experiment has since been concluded." This design decision means that eval results submitted post-conclusion for sessions that were enrolled in the experiment cannot be attributed to the experiment in the Results API. For external scorer pipelines that process eval results in batches (potentially hours after session completion), this creates a data loss scenario: if the experiment is concluded before the batch pipeline runs, all pending eval attributions are silently dropped. The session record still has `experimentContext`, so the gateway has the information to attribute correctly.
**Recommendation:** Store the eval result with the session's actual `experiment_id` and `variant_id` regardless of experiment status. The experiment's `concluded` status already prevents new sessions from being assigned; there is no reason to discard attribution on already-enrolled sessions. If the concern is preventing stale data from polluting results, add a `submitted_after_conclusion: true` flag or filter in the Results API instead.

### EXP-048. Sticky assignment cache for `sticky: session` has no defined behavior [Low]
**Section:** 10.7 (Experiment Primitives), lines 4619, 4651, 4883
The `sticky` field supports three values: `user`, `session`, and `none`. The bucketing algorithm (line 4619) uses `session_id` as the assignment key when `sticky: session`. However, the sticky assignment cache (Redis key `t:{tenant_id}:exp:{experiment_id}:sticky:{user_id}`, line 5466) is keyed only by `user_id`. The cache invalidation logic (line 4883) mentions only `sticky: user` caching. There is no specification of whether `sticky: session` uses a Redis cache at all, what its key pattern would be, or whether cache invalidation applies. Since the deterministic hash already guarantees the same `session_id` always yields the same variant, the cache may be unnecessary for `sticky: session` -- but this is never stated explicitly.
**Recommendation:** Clarify that `sticky: session` does not require a Redis cache (the deterministic hash already provides the guarantee), or define the cache key pattern and invalidation behavior for `sticky: session`.

### EXP-049. `control` propagation mode creates misleading eval attribution [Medium]
**Section:** 10.7 (Experiment Primitives), lines 4750, 4755
Under the `control` propagation mode, the child session "is forced into the base runtime (control group) regardless of the parent's variant" but "Eval results still attribute to the parent's experiment." The eval attribution paragraph (line 4755) then states: "Under `inherit` and `control` modes this is the root experiment." For `control` mode, the child's effective `experimentContext` would be `(experiment_id: "X", variant_id: "control")` with `inherited: true`. However, the Results API aggregates scores per variant -- so eval results from a child running the base runtime under `control` propagation would be counted in the "control" variant's aggregates. This creates a sample contamination risk: the control group's eval scores would include results from children whose parent was in the treatment group, introducing selection bias (children spawned by treatment-variant parents may receive systematically different tasks than children spawned by control-group parents).
**Recommendation:** Document this sample contamination risk explicitly. Consider adding a `delegation_depth > 0` filter or a `propagation_source` field on `EvalResult` so that operators querying the Results API can distinguish direct eval results from propagated child results.

### EXP-050. Experiment deletion guard allows deletion of `paused` experiments with active variant sessions [Medium]
**Section:** 15.1 / Admin API (line 7114); 10.7 (Experiment Primitives), line 4889
The experiment deletion guard (line 7114) states: "blocked if `status: active` and sessions are assigned to variants. Deactivate the experiment first." This means a `paused` experiment can be deleted. However, when an experiment transitions to `paused`, the PoolScalingController sets `minWarm` to 0 but `maxWarm` is "intentionally left unchanged" and "existing warm pods... remain available for in-flight sessions already assigned the variant" (line 4889). This means sessions assigned to the variant can still be running while the experiment is paused. Deleting the paused experiment while variant sessions are in-flight would orphan those sessions' `experimentContext` (the experiment record is gone but sessions reference it) and prevent eval results from being attributed or queried via the Results API.
**Recommendation:** Extend the deletion guard to also block deletion when `status: paused` and any active sessions have an `experimentContext` referencing this experiment. Alternatively, require `concluded` status before deletion is permitted.

### EXP-051. Billing event schema missing `experiment_id` and `variant_id` fields [Medium]
**Section:** 11.2.1 (Billing Event Stream), lines 4991-5026; 10.7 (Experiment Primitives)
The billing event schema (lines 4991-5026) does not include `experiment_id` or `variant_id` fields. Sessions enrolled in experiments incur costs that deployers need to attribute per-experiment and per-variant for cost analysis (e.g., "did the treatment variant cost more per session than control?"). Without these fields on billing events, deployers must join billing events with session records to determine experiment enrollment, which is error-prone and impossible after session data is cleaned up. Since experiments are a first-class platform primitive with dedicated admin API, metrics, and pool management, cost attribution should be equally first-class.
**Recommendation:** Add optional `experiment_id` (string, nullable) and `variant_id` (string, nullable) fields to the billing event schema, auto-populated from the session's `experimentContext` when present.

### EXP-052. No specification for maximum number of variants per experiment [Low]
**Section:** 10.7 (Experiment Primitives), lines 4567-4592
The `ExperimentDefinition` schema shows a `variants` list but does not define a maximum number of variants per experiment. The Results API response description (line 4818) notes "the number of variants per experiment is bounded by operator configuration (typically 2-5)" but no actual configuration parameter or validation rule is specified. Each variant creates a separate warm pool via the PoolScalingController, and the base pool adjustment formula (line 585) clamps `sum(variant_weights)` to `[0, 1)`. Without a hard limit, an operator could theoretically create dozens of micro-weighted variants, each requiring its own `SandboxWarmPool` CRD, `SandboxTemplate` CRD, and pool fill cycle -- consuming etcd resources and controller reconciliation time.
**Recommendation:** Define a `maxVariantsPerExperiment` configuration parameter (with a sensible default, e.g., 10) enforced at experiment creation and update time.

### EXP-053. `ExperimentRouter` `PreRoute` phase interaction with pool selection is underspecified for delegation [Medium]
**Section:** 4.8 (Policy Engine), line 1024; 10.7 (Experiment Primitives), lines 4745-4753; 8.2 (Delegation Flow), line 3076
The `ExperimentRouter` fires at the `PreRoute` interceptor phase during session creation. For delegated child sessions, the propagation mode (`inherit`, `control`, `independent`) determines experiment context (Section 10.7). However, Section 8.2's delegation flow (line 3076-3086) does not mention the `ExperimentRouter` or any interceptor chain evaluation. It is unclear whether the `PreRoute` interceptor chain -- including the `ExperimentRouter` -- fires during `lenny/delegate_task` processing, and if so, how it interacts with the propagation mode. Under `independent` mode the child should be independently evaluated by the `ExperimentRouter`, but the delegation flow makes no reference to this step.
**Recommendation:** Specify in the delegation flow (Section 8.2) that the `PreRoute` interceptor chain fires during `delegate_task` processing, and document how the `ExperimentRouter` interacts with the three propagation modes (skip for `inherit`/`control`, evaluate for `independent`).

### EXP-054. Sticky cache invalidation on `paused -> active` re-activation may cause assignment shifts [Low]
**Section:** 10.7 (Experiment Primitives), line 4883
The spec states: "On `paused -> active` re-activation, no flush is required -- the existing cached assignment remains valid." This is correct if the experiment definition was not modified while paused. However, the admin API allows `PUT /v1/admin/experiments/{name}` to update the experiment (line 6851), and the only explicit restriction is that "Concluded experiments are immutable" (line 4881). If an operator pauses an experiment, modifies variant weights, then re-activates, the stale cached assignments in Redis would route users to the old weight distribution, while new users (uncached) would be assigned according to the new weights. The deterministic hash means uncached users' assignments would also shift if weights changed. This creates an inconsistent cohort during re-activation.
**Recommendation:** Specify whether weight modifications are permitted while an experiment is paused. If they are, require a sticky cache flush on re-activation whenever the experiment definition has been modified (detectable by comparing the experiment's `version`/etag at pause time vs. re-activation time).

---

## 22. Document Quality (DOC)

**DOC-047 | Broken Markdown table: extra column separator in preflight checks header row | Section 17.6 | Line 8914 | Severity: Medium**

The table header separator row (line 8914) has four column separators, while the header row (line 8913) has three columns (Check, Validation, Failure Message). The extra `| --------------------------- |` at the end creates a phantom fourth column. Most Markdown renderers will render this table incorrectly.

*Recommendation:* Remove the trailing `| --------------------------- |` from line 8914 so the separator row matches the three-column header.

---

**DOC-048 | Unescaped pipe character in table cell breaks Markdown table | Section 17.6 | Line 8936 | Severity: Medium**

The "etcd Secret encryption (warning)" row's Failure Message cell contains the shell command `'etcdctl get /registry/secrets/lenny-system/<name> | hexdump'`. The pipe character `|` in the shell command is interpreted as a Markdown table column separator, causing the remainder of the cell (`hexdump'. See Section 4.9.`) to spill into a nonexistent fourth column. This corrupts rendering for this row and potentially subsequent rows.

*Recommendation:* Escape the pipe character as `\|` within the table cell: `... <name> \| hexdump`.

---

**DOC-049 | Incorrect cross-reference: "Section 4" should be "Section 10.2" | Section 15.1 | Line 6837 | Severity: Medium**

The `PUT /v1/admin/tenants/{id}/users/{user_id}/role` endpoint description says "see Authorization and RBAC in Section 4". The "Authorization and RBAC" subheading is at line 4186, which is within Section 10.2 (Authentication) under Section 10 (Gateway Internals), not Section 4 (System Components). Section 4 spans lines 106-1625 and does not contain this content.

*Recommendation:* Change "see Authorization and RBAC in Section 4" to "see Authorization and RBAC in Section 10.2".

---

**DOC-050 | Imprecise cross-reference: "(Section 10)" for DEADLINE_APPROACHING | Section 15.4.3 | Line 7915 | Severity: Low**

The Runtime Integration Tiers comparison table (Full tier column) states: `DEADLINE_APPROACHING signal delivered on lifecycle channel before session expiry (Section 10)`. Section 10 is "Gateway Internals" (a large section spanning lines 4013-4922). The `deadline_approaching` lifecycle message is actually defined in Section 4.7 (Runtime Adapter, line 711) and the session expiry warning behavior is described in Section 11.3 (Timeouts and Cancellation, line 5128). A bare "(Section 10)" provides no navigational value in a document of this size.

*Recommendation:* Replace "(Section 10)" with a more precise reference such as "(Section 4.7, Section 11.3)".

---

**DOC-051 | Misleading cross-references in "Why Lenny?" differentiators | Section 23.1 | Line 9873 | Severity: Medium**

Differentiator 5 states: "Rate limiting, token budgets, concurrency controls, deployer-selectable isolation profiles (runc/gVisor/Kata), audit logging, and least-privilege pod security are built into the gateway and controller layers (Sections 2, 8, 16)." The actual sections covering these features are:
- Rate limiting: Section 11.1 (Admission and Fairness)
- Token budgets: Section 11.2 (Budgets and Quotas)
- Concurrency controls: Section 11.1
- Isolation profiles: Section 5.3 (Isolation Profiles)
- Audit logging: Section 11.7 (Audit Logging)
- Pod security: Section 13.1 (Pod Security)
- Section 2 is Goals/Non-Goals (mentions these as goals but does not define them)
- Section 16 is Observability (monitoring, not controls)

*Recommendation:* Replace "(Sections 2, 8, 16)" with "(Sections 5.3, 8, 11, 13)" or list specific subsections.

---

**DOC-052 | Security-related content split across non-adjacent sections (10 and 13) | Document-wide | Severity: Low**

Section 10 (Gateway Internals) contains security-critical subsections: 10.2 (Authentication), 10.3 (mTLS PKI). Section 13 (Security Model) contains: 13.1 (Pod Security), 13.2 (Network Isolation), 13.3 (Credential Flow), 13.4 (Upload Security), 13.5 (Delegation Chain Content Security). Sections 11.7 (Audit Logging) and 11.8 (Security Incident Response) also contain security content. A reader seeking the security posture must visit three non-adjacent sections (10, 11, 13) with no cross-references between the section headings explaining this split.

*Recommendation:* Add a brief note at the top of Section 13 acknowledging that authentication and mTLS are in Section 10.2-10.3, and audit/incident response are in Section 11.7-11.8, so readers can find the complete security surface. Alternatively, add a consolidated "Security Index" subsection.

---

**DOC-053 | Inconsistent cross-reference notation: "Section X" vs "§X" | Document-wide | Severity: Low**

The document uses two notation styles for section cross-references: `Section X` (~786 occurrences) and `§X` (~121 occurrences). While both are understood, the inconsistency creates ambiguity about whether different notation implies different semantics (e.g., whether `§` references are less formal or carry different authority). Some paragraphs even mix both styles in the same sentence (e.g., line 9871: "Section 15, Section 3 ... see §4.7 and §9").

*Recommendation:* Choose one notation style and apply it consistently. If both are retained, document the convention (e.g., "Section for first reference, § for subsequent references in the same paragraph").

---

**DOC-054 | Inconsistent hyphenation of "SDK-warm" vs "SDK warm" | Sections 5.2, 6.1, 16.5 | Lines 565, 567, 2319, 8495 | Severity: Low**

The compound adjective "SDK-warm" is used predominantly throughout the document (approximately 37 times with hyphen). However, at least four occurrences use the unhyphenated form "SDK warm" (lines 565, 567, 2319, 8495). Examples:
- Line 565: "excluding SDK warm" 
- Line 8495: "SDK warm startup is systematically failing"

*Recommendation:* Standardize on "SDK-warm" (hyphenated) when used as a compound modifier, consistent with the majority usage.

---

**DOC-055 | Section 20 (Open Questions) is vestigial | Section 20 | Lines 9778-9782 | Severity: Low**

Section 20 contains only: "All open questions have been resolved. See Section 19 for decisions." This is a three-line section with no substantive content. It occupies a top-level section number in a 24-section document and contributes no information beyond what the Section 19 heading already communicates.

*Recommendation:* Either (a) remove Section 20 entirely and add a note at the top of Section 19 stating that all open questions are now resolved, or (b) retain it as-is if the section numbering is stable and external documents reference "Section 20" by number.

---

**DOC-056 | No Table of Contents for a 10,039-line document | Document-wide | Severity: Medium**

The document has 24 top-level sections, 261 total headings, and spans 10,039 lines. There is no table of contents or section index. Navigation requires searching or scrolling. For a specification of this size, a ToC is essential for both human readers and automated tooling that processes section references.

*Recommendation:* Add a Table of Contents after the document header (line 6) listing all `##` and `###` level headings with line anchors.

---

**DOC-057 | Document length exceeds practical review and maintenance threshold | Document-wide | Severity: Medium**

At 10,039 lines, the document is a single-file specification covering architecture, APIs, security, deployment, CLI reference, competitive analysis, governance, and build phasing. Specific concerns:
- The Admin API endpoint table (Section 15.1, lines ~6626-6891) alone spans ~265 lines of dense table content duplicated by the `lenny-ctl` command reference (Section 24, lines 9905-10039).
- The error code catalog (lines 6913-6993) spans 80 lines and functions as a standalone reference table.
- The Build Sequence (Section 18) spans ~73 lines of phase tables that read like project management rather than technical design.

This is not a style preference: review processes, diff-based collaboration, and agent-based tooling all degrade at this file size.

*Recommendation:* Consider extracting self-contained reference sections (error code catalog, CLI reference, admin API table, build sequence, capacity planning tables) into separate documents linked from the main spec. This does not require restructuring the design narrative.

---

**DOC-058 | Admin API table and lenny-ctl reference have parallel maintenance burden | Sections 15.1, 24 | Lines 6626-6891, 9905-10039 | Severity: Medium**

The Admin API endpoint table (Section 15.1) and the `lenny-ctl` Command Reference (Section 24) describe the same operations from two perspectives (REST endpoints vs CLI commands). Section 24 explicitly includes an "API Mapping" column that maps each CLI command to its REST endpoint. When endpoints are added or modified, both sections must be updated in sync. Section 15.1 line 6891 states: "The table above includes all endpoints; Section 24 provides CLI wrappers and usage examples." However, several Section 24 commands reference endpoints not described in the Section 15.1 table (e.g., `DELETE /v1/admin/pools/{name}/bootstrap-override` appears in the table at line 6879 but its description there is minimal compared to the Section 24 entry at line 9939).

*Recommendation:* Add a maintenance note near the Section 15.1 table and the Section 24 header stating that these two sections must be kept in sync, and consider a CI check or convention that verifies 1:1 coverage.

---

**DOC-036 | Carried-forward skip: Orphaned footnote markers | N/A | Severity: N/A**

No footnote markers (`[^N]` or similar) were found anywhere in the document. This carried-forward item is moot.

---

Review iteration 2: 12 findings (0 Critical, 0 High, 6 Medium, 6 Low)

---

## 23. Messaging & Conversations (MSG)

**MSG-054** | **Section 7.2, line 2867** | **Medium**

**Path precedence note contradicts path numbering order.** The spec states that the actual precedence is `path 1 > path 2 > path 4 > path 3 > path 5 > path 6`, while the paths are listed in the order 1, 2, 3, 4, 5, 6. The note says "path 4 appears after path 3 only for readability" but then defines path 4 as higher priority than path 3. This creates a hazardous ambiguity: an implementer reading the paths in listed order and applying "first matching path wins" (as the opening sentence states) would evaluate path 3 before path 4, getting the wrong behavior when both `await_children` and `request_input` are in flight simultaneously. The spec tries to fix this with the precedence note, but a normative "first matching path wins in listed order" rule directly contradicts the separate precedence statement.

**Recommendation:** Reorder the paths to match the actual precedence (swap paths 3 and 4), or replace the "first matching path wins" language with an explicit numbered precedence table that is unambiguous.

---

**MSG-055** | **Section 7.2, line 2870 vs. Section 15.4.1, line 7563** | **Medium**

**Inconsistent behavior of `delivery: "immediate"` for `running` (non-`input_required`) sessions.** Line 7563 states: "If session is `running`, the gateway sends an interrupt signal on the lifecycle channel and writes the message to stdin as soon as the runtime emits `interrupt_acknowledged`." However, the six delivery paths in Section 7.2 (lines 2869-2876) never describe what happens when `delivery: "immediate"` is set and the session is in the `running` state with a tool call in flight (i.e., not `ready_for_input` and not `input_required`). Path 2 requires `ready_for_input`. Path 3 applies when blocked in `await_children`. Path 4 applies when `input_required`. The interrupt-and-deliver behavior described in the `delivery` field definition (line 7563) is not mapped to any of the six paths. A message with `delivery: "immediate"` targeting a `running` session with a tool call in flight falls through all six paths with no match.

**Recommendation:** Add an explicit path (or modify path 2) to cover the `delivery: "immediate"` + `running` (tool-call-in-flight) case, describing the interrupt-then-deliver behavior specified in line 7563. This should be path 2a or a conditional branch within path 2.

---

**MSG-056** | **Section 7.2, line 2870** | **Low**

**Delivery receipt for path 2 fallback is ambiguous on timing.** Path 2 states that if the runtime does not consume the message within the delivery timeout (30s), the receipt status is `queued`, not `delivered`, and the message falls through to "path 3 behavior" (inbox buffering). However, the delivery receipt is described as synchronous (returned from `lenny/send_message`). If the gateway must wait up to 30 seconds to determine whether the receipt should be `delivered` or `queued`, the synchronous return is delayed by up to 30 seconds. This latency is not mentioned anywhere in the delivery receipt documentation or the `lenny/send_message` tool description.

**Recommendation:** Clarify the expected latency behavior of `lenny/send_message` delivery receipts for path 2, and consider whether a shorter timeout or an optimistic `queued` receipt with an async upgrade to `delivered` would be more appropriate.

---

**MSG-057** | **Section 8.8, line 3582 vs. Section 7.2, line 2764** | **Medium**

**`input_required` exit transition inconsistency between task and session state machines.** The canonical task state machine (line 3582) specifies: `input_required -> running (input provided via lenny/send_message with inReplyTo)`. The session state machine (line 2764) specifies: `input_required -> running (input provided via inReplyTo or request expires/cancelled)`. These are semantically different: the task state machine mentions only `lenny/send_message with inReplyTo`, while the session state machine additionally includes request expiry and cancellation as transitions back to `running`. More critically, the task state machine does not list `input_required -> running` via timeout (`maxRequestInputWaitSeconds`), even though Section 11.3 (line 5126) explicitly states the gateway resolves the blocked tool call with a `REQUEST_INPUT_TIMEOUT` error, after which the child transitions back to `running`.

**Recommendation:** Update the canonical task state machine (Section 8.8, line 3582) to include all three exit conditions from `input_required -> running`: (1) inReplyTo response, (2) request timeout (maxRequestInputWaitSeconds), (3) request cancellation. This matches the session state machine and the Section 11.3 description.

---

**MSG-058** | **Section 7.2, line 2884** | **Medium**

**Pre-running states reject messages but lack retry guidance for inter-session senders.** The dead-letter table states that messages to sessions in pre-running states (`created`, `ready`, `starting`, `finalizing`) are rejected with `TARGET_NOT_READY` and "Client should retry after the session transitions to `running`." However, for inter-session messages (agent-to-agent via `lenny/send_message`), the sending agent has no mechanism to subscribe to the target session's state transitions. The only option is polling `lenny/get_task_tree()`, but this only returns the sender's own tree, not arbitrary sessions. A parent sending a message to a child that hasn't yet reached `running` has no efficient way to know when to retry.

**Recommendation:** Either (1) specify that `lenny/send_message` to pre-running child sessions should be buffered rather than rejected (since the gateway already knows the session exists and will eventually start), or (2) define a mechanism for the sender to wait/subscribe for the target's state transition, or (3) document the expected retry pattern (polling interval, backoff, max attempts) for `TARGET_NOT_READY` in inter-session contexts.

---

**MSG-059** | **Section 7.2, line 2886 vs. Section 7.2, line 2857** | **Medium**

**Inbox-to-DLQ drain race window for `durableInbox: true` on transition to terminal state.** The spec defines inbox-to-DLQ migration for `resume_pending` transitions (line 2857), and states that for `durableInbox: true`, the drain-to-DLQ step is unnecessary because the Redis inbox survives coordinator crashes. However, the spec does not describe what happens to messages in the durable inbox when a session transitions directly from `running` (or `input_required`) to a terminal state (`completed`, `failed`, `cancelled`, `expired`). If the inbox contains undelivered messages at the moment the session completes, those messages are orphaned in Redis. The dead-letter table (line 2885) only covers messages sent *to* a session already in a terminal state; it does not cover messages already *in* the inbox when the session terminates.

**Recommendation:** Specify the behavior for messages in the inbox (both in-memory and Redis-backed) when the session transitions to a terminal state. Options include: (1) drain to DLQ with a short TTL for post-mortem retrieval, (2) discard with `message_dropped` receipts to original senders, or (3) mark as `expired` and notify senders.

---

**MSG-060** | **Section 7.2, lines 2908-2912** | **Medium**

**`get_task_tree` snapshot staleness creates a race in sibling coordination.** The spec correctly notes (line 2912) that "the task tree is a snapshot: siblings spawned between the `get_task_tree` call and the final `send_message` call will be missed." However, no mechanism exists for a sibling to discover *newly spawned* siblings after the initial tree snapshot. For agent team patterns where the coordinator spawns workers incrementally, existing workers cannot discover later-spawned peers without re-calling `get_task_tree` (polling). There is no event-based notification when the tree changes.

**Recommendation:** For v1, document the polling requirement and recommend a poll interval. For post-v1, consider adding a `tree_changed` event on the `lenny/await_children` stream or a `lenny/watch_task_tree()` streaming tool that notifies when siblings are added or removed.

---

**MSG-061** | **Section 7.2, line 2906 vs. Section 15, lines 6579-6585** | **Low**

**SSE back-pressure disconnection does not guarantee event replay completeness.** The SSE bounded-error policy (line 6579-6585) states that when the subscriber is slow, the gateway closes the connection within 100ms, and the client must reconnect with its last-seen cursor. The reconnect semantics (line 2892) define a replay window of `max(periodicCheckpointIntervalSeconds x 2, 1200s)`. However, during the brief period between the gateway closing the connection and the client reconnecting, new events may be generated that push older events past the replay window boundary (especially under high-throughput workloads). The spec does not quantify the event generation rate at which the replay window becomes insufficient to cover the reconnect gap.

**Recommendation:** Add deployer guidance on sizing `periodicCheckpointIntervalSeconds` relative to expected event throughput, so the replay window comfortably covers the reconnect gap even under peak load. Consider adding a metric for "events generated per second per session" to help deployers tune this.

---

**MSG-062** | **Section 15.4.1, line 7557 vs. Section 7.2 paths** | **Medium**

**`slotId` routing for concurrent-workspace mode not described in delivery paths.** The `MessageEnvelope` includes an optional `slotId` field (line 7557) for concurrent-workspace mode, and the spec states it "identifies the concurrent slot this message is addressed to." However, none of the six delivery paths in Section 7.2 (lines 2869-2876) mention `slotId` or describe how message routing works when multiple slots are active on the same pod. Key unanswered questions: (1) Does each slot have its own independent inbox? (2) Is `ready_for_input` evaluated per-slot or per-pod? (3) Can a message be delivered to one slot while another is in `input_required`? (4) How does `delivery: "immediate"` interact with slot multiplexing?

**Recommendation:** Add a subsection or annotation to the six delivery paths describing how each path behaves in concurrent-workspace mode, including per-slot inbox semantics, per-slot `ready_for_input` evaluation, and per-slot `input_required` tracking.

---

**MSG-063** | **Section 7.2, line 2820 vs. Section 15.1, line 6934** | **Medium**

**`CROSS_TENANT_MESSAGE_DENIED` error code not in the error catalog.** Section 7.2 (line 2820) specifies that cross-tenant messages are rejected with `CROSS_TENANT_MESSAGE_DENIED`. However, this error code does not appear in the error code catalog (Section 15.1, lines 6913-6993). The catalog includes `SCOPE_DENIED` for messaging scope violations but omits the cross-tenant error code. This means the error code is normatively referenced in the messaging spec but has no defined HTTP status, category, or retryability.

**Recommendation:** Add `CROSS_TENANT_MESSAGE_DENIED` to the error code catalog in Section 15.1, with appropriate HTTP status (likely 403), category (`POLICY`), and `retryable: false`.

---

**MSG-064** | **Section 7.2, line 2888 vs. Section 15.4.1, line 7574** | **Low**

**Delivery receipt `status` enum has asymmetric definitions.** Section 7.2 (line 2888) lists six status values: `delivered`, `queued`, `dropped`, `expired`, `rate_limited`, `error`. Section 15.4.1 (line 7574) lists the same six but adds context in the description that `error` covers both `inbox_unavailable` (Redis failure for durable inbox) and `scope_denied` (messaging scope denial). However, `scope_denied` is also a standalone error code (`SCOPE_DENIED`, line 6934) returned as an HTTP 403. It is unclear whether a messaging scope violation returns a delivery receipt with `status: "error", reason: "scope_denied"` OR a synchronous HTTP 403 `SCOPE_DENIED` error (rejecting the `lenny/send_message` call entirely). These are different behaviors with different client handling requirements.

**Recommendation:** Clarify whether messaging scope violations result in a delivery receipt (caller gets back a receipt object with error status) or an RPC/HTTP error (caller gets an exception/error response). Define which policy violations use delivery receipts vs. synchronous errors.

---

**MSG-065** | **Section 7.2, line 2836 vs. Section 8.10, line 3783** | **High**

**`children_reattached` event undefined in any message schema.** When a parent pod resumes after failure with active children, Section 8.10 (line 3781) states the parent receives a `children_reattached` event "listing current child states." This event is not defined in any schema: not in the Gateway-to-Client streaming events (lines 2740-2751), not in the lifecycle channel message schemas (lines 698-717), not in the MessageEnvelope format (lines 7516-7597), and not in the Protocol Reference. The event schema (fields, delivery mechanism, format) is entirely unspecified. An implementer cannot build the parent re-await pattern described in lines 3781-3783 without knowing how `children_reattached` is delivered.

**Recommendation:** Define the `children_reattached` event schema, including: (1) delivery mechanism (lifecycle channel, stdin message, or gateway streaming event), (2) field schema (list of child session IDs, their current states, pending request IDs), and (3) add it to the appropriate message schema table.

---

**MSG-066** | **Section 9.2, line 3927 vs. Section 8.8, line 3709** | **Low**

**Elicitation chains excluded from deadlock detection but interaction is underspecified.** Line 3927 states that a task blocked on elicitation "is NOT considered deadlocked -- it is waiting on an external actor." However, if a parent is blocked on elicitation (waiting for a human) and its child calls `lenny/request_input` targeting the parent, the child blocks in `input_required` waiting for the parent, while the parent is blocked waiting for a human. This is not technically a deadlock (the human can unblock it), but the child's `maxRequestInputWaitSeconds` timer will fire, potentially causing the child to fail while the parent is still waiting for a human with no awareness that its child timed out. The `request_input_expired` event (line 3701) would go to the parent's `await_children` stream, but if the parent is blocked on elicitation, it cannot process the event.

**Recommendation:** Document the interaction between elicitation blocking and `request_input` timeout explicitly. Consider whether `maxRequestInputWaitSeconds` should be paused when the target parent is blocked on elicitation (analogous to how `maxIdleTime` is paused during elicitation per line 3919).

---

**MSG-067** | **Section 7.2, line 2848 vs. line 2870** | **Medium**

**At-least-once delivery in durable inbox creates duplicate delivery risk with no deduplication at the recipient.** The durable inbox specification (line 2848) states: "Until ACK, the message remains at the list head and is re-delivered on coordinator restart. This provides at-least-once delivery." However, the message deduplication mechanism (line 7583) is defined only for *inbound* message IDs (sender-side deduplication to prevent the same message from being submitted twice). There is no recipient-side deduplication to prevent the same message from being delivered to the runtime's stdin twice after a coordinator restart. If the coordinator crashes after delivering a message to stdin but before executing the `LREM` ACK, the message will be delivered again. The runtime has no platform-provided mechanism to detect this duplicate.

**Recommendation:** Either (1) add a recipient-side deduplication mechanism (e.g., the adapter tracks delivered message IDs and suppresses duplicates on re-delivery), or (2) document this as a known property of at-least-once delivery and require runtimes to implement idempotent message handling, or (3) provide a `delivered_message_ids` set on the adapter that is persisted to the checkpoint so it survives coordinator restarts.

---

**MSG-068** | **Section 15.4.3, line 7920 vs. Section 7.2** | **Medium**

**Minimum-tier runtimes cannot participate in the messaging system but this limitation is not listed.** The tier comparison matrix (line 7920) states that Minimum-tier runtimes only need `type`, `id`, `input` from the `MessageEnvelope`, with all other fields "safely ignored." However, Minimum-tier runtimes have no access to the platform MCP server (line 7909), which means they cannot call `lenny/send_message`, `lenny/request_input`, or `lenny/get_task_tree`. The limitation list for Minimum tier (lines 7922-7934) mentions delegation and platform MCP tools but does not explicitly list inter-session messaging or `request_input` as unavailable. Since `lenny/request_input` is critical for multi-turn patterns and `lenny/send_message` is critical for agent coordination, this omission could mislead Minimum-tier runtime authors into expecting messaging support.

**Recommendation:** Add `lenny/send_message` (inter-session messaging) and `lenny/request_input` (input-required blocking) to the explicit Minimum-tier limitation list at lines 7922-7934, alongside the existing delegation and platform MCP tools entries.

---

**MSG-069** | **Section 5.1, line 1657 vs. Section 7.2** | **Medium**

**`one_shot` runtime `lenny/request_input` limitation creates orphaned `input_required` state.** Line 1657 states that a `one_shot` runtime "may use `lenny/request_input` once (for a single clarification). Second call returns a gateway error." However, the session state machine (Section 7.2) and the task state machine (Section 8.8) do not distinguish between `one_shot` and `multi_turn` runtimes in their state transition rules. If a `one_shot` runtime enters `input_required` and the client responds, the runtime produces its single response and the task ends. But if the client does *not* respond before `maxRequestInputWaitSeconds`, the runtime receives `REQUEST_INPUT_TIMEOUT` and transitions back to `running`. At this point, the `one_shot` runtime has already consumed its single clarification opportunity and cannot call `request_input` again. The spec does not describe what the runtime should do: it must produce a response (it's `one_shot`), but it has no clarification and may not have enough context. The interaction between `one_shot` + `request_input` + timeout is underspecified.

**Recommendation:** Document the expected behavior of a `one_shot` runtime after its single `request_input` call times out. Options: (1) the runtime must produce a best-effort response without the clarification, (2) the runtime may fail with a structured error indicating insufficient input, or (3) the gateway should auto-fail the task after a `one_shot` runtime's `request_input` times out.

---

**MSG-070** | **Section 7.2, lines 2859-2863** | **Low**

**Inbox-to-DLQ drain atomicity claim is overstated.** The spec states (line 2862): "This drain is performed as a single Redis pipeline call within the same goroutine that executes the `resume_pending` state transition, so no messages are lost between the inbox read and the DLQ write." However, the inbox is in-memory (for `durableInbox: false`). Between reading the in-memory inbox and completing the Redis pipeline write, a new message could arrive via `lenny/send_message` and be enqueued to the in-memory inbox. Since the goroutine reads the inbox first and then writes to Redis, this new message would not be included in the DLQ write. The "no messages are lost" claim holds only if no new messages arrive during the drain window, which is not guaranteed.

**Recommendation:** Specify that the inbox must be locked (no new enqueues accepted) during the drain operation, or acknowledge that messages arriving during the drain window may be lost and quantify the expected window size.

---

**MSG-071** | **Section 15.1, line 6659 vs. Section 7.2, line 2874** | **Medium**

**`POST /v1/sessions/{id}/messages` precondition states are incomplete.** The REST API table (line 6659) lists valid precondition states for the messages endpoint as `running` and `suspended`. However, the delivery paths in Section 7.2 also handle messages to sessions in `input_required` (path 4, line 2874), `resume_pending` and `awaiting_client_action` (path 6, line 2886 -- dead-letter), and pre-running states like `created` (line 2884 -- `TARGET_NOT_READY`). Since `input_required` is a sub-state of `running`, it may be implicitly covered by listing `running`, but `resume_pending` and `awaiting_client_action` are not sub-states of `running` or `suspended` -- they are distinct states that accept messages (via DLQ). The messages endpoint should either reject or DLQ these, but the precondition table implies rejection (only `running` and `suspended` are valid).

**Recommendation:** Expand the precondition states for `POST /v1/sessions/{id}/messages` to include `resume_pending` and `awaiting_client_action` (with a note that messages are DLQ'd, not directly delivered), or add a note that the endpoint accepts messages in any non-terminal state with delivery semantics varying by state per Section 7.2.

---

**MSG-072** | **Section 7.2, line 2890** | **Low**

**`message_expired` event delivered to sender's event stream has no schema definition.** Line 2890 defines a `message_expired` event: `{ "type": "message_expired", "messageId": "msg_abc123", "reason": "target_ttl_exceeded" }`. However, this event is not listed in the Gateway-to-Client streaming events table (lines 2740-2751), nor is it defined in the Protocol Reference (Section 15.4.1). Clients and runtime authors have no way to discover that this event type exists from the streaming event documentation alone.

**Recommendation:** Add `message_expired` to the Gateway-to-Client streaming events table (lines 2740-2751) with a description like "Previously queued message expired before delivery; sent to the original sender's stream."

---

**MSG-073** | **Section 7.2, line 2836 vs. Section 7.2, line 2863** | **Medium**

**`inbox_cleared` event on coordinator failover conflicts with `awaiting_client_action` DLQ Postgres flush.** For `durableInbox: false`, the coordinator failover emits an `inbox_cleared` event (line 2836) notifying the target session's client that buffered messages were lost. Separately, line 2863 specifies that when a session enters `awaiting_client_action`, the gateway flushes the Redis DLQ to the `session_dlq_archive` Postgres table. However, the interaction is unclear: if the coordinator crashes while the session is in `resume_pending` (inbox already drained to Redis DLQ per line 2857-2862), and then the new coordinator emits `inbox_cleared` -- this is misleading because the messages were actually drained to DLQ and not lost. The `inbox_cleared` event does not distinguish between "messages were truly lost" vs. "messages were preserved in DLQ."

**Recommendation:** The `inbox_cleared` event should include a `messagesPreservedInDLQ` boolean or count, so clients can distinguish between a true data loss event and a benign coordinator handoff where messages were already drained to DLQ.

---

---

## 24. Policy Engine (POL)

### POL-057. Missing timeout for DelegationPolicy tag-matching evaluation [Low]

**Section:** 8.3 (DelegationPolicy), 4.8 (RequestInterceptor chain)  
**Lines:** ~3247-3350  

**Description:** The DelegationPolicy tag-matching algorithm is described in Section 8.3 but there is no explicit timeout or computational bound on the tag-matching evaluation itself. While the interceptor chain has per-phase timeouts (Section 4.8, lines 1108-1110), the delegation admission path -- which evaluates DelegationPolicy rules including tag matching, isolation monotonicity, and contentPolicy -- does not specify a maximum evaluation duration. A policy with a large number of rules or complex tag combinations could introduce unbounded latency on the `delegate_task` hot path.

**Recommendation:** Add a delegation admission evaluation timeout (e.g., 500ms) to the timeout table in Section 11.3, covering the complete DelegationPolicy evaluation (tag matching + isolation check + contentPolicy + budget reservation). This would be distinct from the individual interceptor timeouts and would bound the total admission latency for delegation requests.

---

### POL-058. Budget reservation Lua script does not account for maxTreeMemoryBytes during Redis fail-open [Medium]

**Section:** 8.3 (Budget reservation model), 12.4 (Redis HA and Failure Modes)  
**Lines:** ~3300-3400, ~5480-5498  

**Description:** Section 12.4 describes the fail-open behavior for quota enforcement during Redis unavailability, including per-replica budget ceilings and cumulative fail-open timers. Section 8.3 describes the atomic `budget_reserve.lua` Lua script that enforces six counters atomically (token budget, usage, tree size, childrenTotal, parallelChildren, tree memory). During a Redis outage, the fail-open behavior described in Section 12.4 covers rate-limit counters and per-tenant token quotas, but does not explicitly address how delegation budget counters (tree size, parallel children, tree memory) are enforced in the fail-open window. Section 12.4's "Delegation budget counter reconciliation on Redis recovery" paragraph (line ~5492) describes reconstruction after recovery but not enforcement during the outage. If a parent agent issues `delegate_task` calls during the fail-open window, there is no specification for how `maxTreeSize`, `maxParallelChildren`, or `maxTreeMemoryBytes` are enforced.

**Recommendation:** Specify the fail-open behavior for delegation budget counters explicitly. Options include: (a) fail-closed for delegation during Redis outage (reject all `delegate_task` with a retryable error), which is the safest approach since delegation trees are structurally complex to enforce in-memory across replicas; or (b) per-replica in-memory tracking with conservative limits similar to the per-tenant fail-open model. Document the chosen behavior in Section 12.4 alongside the existing fail-open table.

---

### POL-059. Interceptor chain short-circuit on REJECT does not specify whether subsequent MODIFY results are discarded [Medium]

**Section:** 4.8 (RequestInterceptor chain)  
**Lines:** ~999-1033  

**Description:** Section 4.8 states that the interceptor chain short-circuits on REJECT: when any interceptor returns REJECT, the request is denied. It also states that MODIFY propagates payload changes to subsequent interceptors. However, the specification does not clarify the following edge case: if an interceptor at priority N returns MODIFY (changing the payload), and a subsequent interceptor at priority N+1 returns REJECT, the specification does not state whether the modifications from the priority-N interceptor are visible in the rejection audit record. This matters for audit trail accuracy -- if a content filter modifies a prompt and then a rate limiter rejects it, the audit should record the original or modified prompt depending on the design intent.

**Recommendation:** Specify that when a REJECT occurs, the audit record captures the payload as it existed at the point of rejection (including all preceding MODIFY transformations). This is the natural behavior of a sequential pipeline but should be explicitly stated for implementor clarity.

---

### POL-060. No admission control for `maxConcurrent` slot exhaustion in concurrent-workspace mode [Medium]

**Section:** 5.2 (Execution modes), 11.3 (Timeouts)  
**Lines:** ~1998-2100  

**Description:** Section 5.2 describes the concurrent-workspace execution mode where a single pod handles multiple concurrent tasks via slot assignment. Slot assignment atomicity is specified, and the `lenny:pod:{pod_id}:active_slots` Redis counter tracks active slots. However, there is no admission control specification for what happens when all slots across all pods in a pool are exhausted. The warm pool claim path (Section 4.6.1) handles pod exhaustion with `WARM_POOL_EXHAUSTED`, but the slot-level exhaustion within already-claimed concurrent-mode pods has no equivalent admission error code or backpressure mechanism.

**Recommendation:** Add a `CONCURRENT_SLOTS_EXHAUSTED` error code (or reuse `WARM_POOL_EXHAUSTED` with additional context) to handle the case where all concurrent-mode pods have reached their `maxConcurrent` limit. Specify whether the gateway should queue the task, reject immediately, or attempt to claim a new pod when all existing pods' slots are full.

---

### POL-061. Circuit breaker `retryable: false` conflicts with transient nature of operator-managed breakers [Low]

**Section:** 11.6 (Circuit Breakers), 15.1 (Error code catalog)  
**Lines:** ~5170-5194, ~6962  

**Description:** The error code catalog (line 6962) states that `CIRCUIT_BREAKER_OPEN` is `retryable: false` with the note "the client should wait for the circuit breaker to be closed by an operator before retrying." However, operator-managed circuit breakers are inherently transient -- they are opened during incidents and closed when the incident is resolved. Setting `retryable: false` tells automated clients to give up permanently, which is inconsistent with the transient nature of these breakers. Clients cannot know when the breaker will close and have no mechanism to be notified.

**Recommendation:** Either (a) change `retryable` to `true` with a long `Retry-After` header (e.g., 60s), allowing automated clients to periodically recheck; or (b) keep `retryable: false` but add a `details.retryAfterSeconds` hint field that clients can optionally use. The current design forces all automated orchestrators to treat circuit breaker events as permanent failures.

---

### POL-062. Fail-open cumulative timer resets on replica restart, creating a bypass window [Medium]

**Section:** 12.4 (Redis HA and Failure Modes)  
**Lines:** ~5498  

**Description:** Section 12.4 specifies that the cumulative fail-open timer (which transitions quota enforcement to fail-closed after `quotaFailOpenCumulativeMaxSeconds`, default 300s) "resets to zero on replica restart." The stated rationale is "conservative choice -- the outage that caused the restart is treated as resolved." However, this creates a bypass: if Redis is experiencing intermittent failures that trigger replica restarts (e.g., CrashLoopBackOff due to Redis timeout), each restart resets the cumulative timer. A gateway replica that restarts every 280 seconds during a sustained Redis outage would never reach the 300s cumulative threshold, allowing unbounded fail-open operation. The `quota_failopen_started` audit event fires per replica per outage, but the cumulative security control is defeated.

**Recommendation:** Persist the cumulative fail-open state to a local file or shared state (e.g., a Kubernetes ConfigMap or Postgres row) so that replica restarts do not reset the security control timer. Alternatively, add a startup check that queries other gateway replicas' fail-open state before resetting the timer.

---

### POL-063. Per-user fail-open ceiling `userFailOpenFraction` default of 0.25 may be too generous for single-user tenants [Low]

**Section:** 12.4 (Redis HA and Failure Modes)  
**Lines:** ~5496  

**Description:** The per-user fail-open ceiling is `min(tenant_limit * userFailOpenFraction, per_replica_hard_cap)` with `userFailOpenFraction` defaulting to 0.25. For single-user tenants (common in development or small deployments), this means the single user can consume 25% of the tenant's fail-open allocation per replica, which is effectively 25% * N replicas of the tenant limit during a Redis outage. For a single-user tenant this provides no meaningful per-user throttling since there is only one user. The documentation does not note this edge case.

**Recommendation:** Document that `userFailOpenFraction` provides no meaningful per-user throttling for single-user tenants and that the per-tenant ceiling is the operative control in that case. Consider defaulting `userFailOpenFraction` to 1.0 for single-tenant/single-user deployments to avoid confusing operators.

---

### POL-064. Timeout table (Section 11.3) missing entries for several policy-relevant operations [Medium]

**Section:** 11.3 (Timeouts and Cancellation)  
**Lines:** ~5088-5122  

**Description:** The comprehensive timeout table in Section 11.3 covers many operations but is missing explicit entries for several policy-relevant timeouts that are defined elsewhere in the document:

1. **Credential lease TTL** -- mentioned throughout Section 4.9 as governing how long a session can operate before credential renewal, but no entry in the timeout table with a default value.
2. **SandboxClaim orphan timeout** -- defined in Section 4.6.1 (line ~530) as `claimOrphanTimeout` (default 5 minutes) but absent from the timeout table.
3. **Billing stream TTL** -- defined in Section 11.2.1 as `billingStreamTTLSeconds` (default 3600s) but absent from the timeout table.
4. **Legal hold checkpoint reconciler interval** -- 15 minutes (Section 12.8 line ~5622) but absent.
5. **`task_complete_acknowledged` timeout** -- 30 seconds (Section 4.7 line 716) but absent.

**Recommendation:** Add these five timeouts to the Section 11.3 table for completeness, since they all have policy or security implications (credential expiry, orphan cleanup, billing data loss window, evidence preservation, and task lifecycle).

---

### POL-065. `CIRCUIT_BREAKER_OPEN` error does not include `Retry-After` header [Low]

**Section:** 15.1 (Error code catalog), 11.6 (Circuit Breakers)  
**Lines:** ~6962, ~5190  

**Description:** When a pool drain returns `POOL_DRAINING` (line 6963), the response includes a `Retry-After` header with an estimated drain completion time. However, `CIRCUIT_BREAKER_OPEN` (line 6962) includes no `Retry-After` header despite being a similar "temporarily unavailable" condition. The error details include `circuit_name`, `reason`, and `opened_at` but no estimated recovery time. Without `Retry-After`, clients have no guidance on when to recheck.

**Recommendation:** Add an optional `Retry-After` header to `CIRCUIT_BREAKER_OPEN` responses, defaulted to a conservative value (e.g., 60s or configurable per circuit breaker). This aligns with the `POOL_DRAINING` pattern and gives automated clients actionable retry guidance.

---

### POL-066. Interceptor `failPolicy: fail-open` has no per-interceptor cumulative failure tracking [Medium]

**Section:** 4.8 (RequestInterceptor chain)  
**Lines:** ~999-1033  

**Description:** Section 4.8 defines `failPolicy: fail-open` for external interceptors -- when the interceptor times out or errors, the request proceeds as if the interceptor was not present. The section describes per-invocation timeout behavior but does not specify any cumulative failure tracking analogous to the quota fail-open cumulative timer (Section 12.4). An interceptor with `failPolicy: fail-open` could fail continuously for hours without any automatic escalation to fail-closed. This is a significant policy gap: a content-filtering interceptor that silently fails open for an extended period bypasses all content policy enforcement.

**Recommendation:** Add a cumulative failure threshold for fail-open interceptors (e.g., if an interceptor with `failPolicy: fail-open` has failed more than N times in a rolling window, automatically escalate to fail-closed or emit a critical alert). The `interceptor.fail_policy_weakened` audit event (Section 11.2.1) covers configuration changes but not runtime failures.

---

### POL-067. Budget return semantics for detached orphan tasks not fully specified [Medium]

**Section:** 8.10 (Delegation tree recovery), 8.3 (Budget reservation model)  
**Lines:** ~3747-3900, ~3300-3400  

**Description:** Section 8.10 describes orphan handling when a parent session terminates while children are still running. It specifies three strategies: `cancel_all`, `await_completion`, and `detach`. For `detach`, orphaned children continue running as independent sessions. Section 8.3's `budget_reserve.lua` reserves budget atomically from the parent's allocation. However, the specification does not fully address what happens to the budget reserved by detached orphans:

1. When a parent detaches an orphan, does the parent's `budget_used` counter decrease by the orphan's reserved allocation?  
2. If the orphan continues consuming tokens after detachment, where is that usage charged? The parent's tree budget or the tenant's global quota?
3. When the orphan eventually completes, does `budget_return.lua` attempt to return unused budget to the (now-terminal) parent's counters?

The `maxOrphanTasksPerTenant` cap (Section 8.10) limits the count of detached orphans but does not address budget accounting.

**Recommendation:** Specify that upon detachment: (a) the orphan's remaining reserved budget is transferred from the parent's tree budget to the tenant's global quota, (b) subsequent usage by the orphan is charged against the tenant's global quota (not the parent's tree), and (c) `budget_return.lua` for the orphan returns unused budget to the tenant quota, not the terminated parent. Document the Lua script modification required.

---

### POL-068. ExperimentRouter interceptor at priority 300 can be overridden by external interceptors [Low]

**Section:** 10.7 (Experiment Primitives), 4.8 (RequestInterceptor chain)  
**Lines:** ~4747-4850, ~999-1033  

**Description:** Section 10.7 specifies that the ExperimentRouter runs as a RequestInterceptor at priority 300. Section 4.8 reserves priorities <= 100 for built-in security-critical interceptors and requires external interceptors to use priority > 100. This means an external interceptor registered at priority 101-299 would execute before the ExperimentRouter. An external interceptor that returns REJECT at priority 200 would short-circuit the chain before experiment routing occurs, which may be the intended behavior (security before routing). However, an external interceptor at priority 200 that returns MODIFY could alter the request payload (e.g., changing the user ID or session metadata) before experiment bucketing occurs, potentially causing inconsistent experiment assignments.

**Recommendation:** Document that the ExperimentRouter at priority 300 sees the payload after all interceptors at priorities 101-299 have had the opportunity to MODIFY it. If experiment bucketing should be based on the original (unmodified) payload, the ExperimentRouter should capture its bucketing inputs at an earlier phase or the priority should be moved below 100 (making it a built-in).

---

### POL-069. `snapshotPolicyAtLease` option does not specify behavior for policy updates to contentPolicy.interceptorRef [Medium]

**Section:** 8.3 (DelegationPolicy)  
**Lines:** ~3247-3350  

**Description:** Section 8.3 describes `snapshotPolicyAtLease` which snapshots the DelegationPolicy at lease creation time so that mid-session policy changes do not affect active delegation trees. The section also describes `contentPolicy.interceptorRef` which references an external interceptor for content filtering, with enforcement that children cannot weaken the parent's interceptor reference. However, the specification does not address the interaction between `snapshotPolicyAtLease: true` and dynamic changes to the referenced interceptor itself. If a DelegationPolicy is snapshotted at lease time with `contentPolicy.interceptorRef: "my-filter"`, and the operator subsequently updates `my-filter`'s configuration (e.g., changing its filtering rules or its `failPolicy` from `fail-closed` to `fail-open`), the snapshotted policy still references the same interceptor name but the interceptor's behavior has changed.

**Recommendation:** Clarify that `snapshotPolicyAtLease` snapshots the policy document only, not the interceptor configuration. The live interceptor configuration is always used at invocation time. Document this explicitly so operators understand that updating an interceptor's `failPolicy` affects all active delegation trees that reference it, regardless of whether the DelegationPolicy was snapshotted.

---

### POL-070. No admission control for recursive delegation depth during fail-open [Medium]

**Section:** 8.2 (Delegation flow), 12.4 (Redis HA and Failure Modes)  
**Lines:** ~2997-3100, ~5480-5498  

**Description:** Section 8.2 describes cycle detection and delegation depth tracking during recursive delegation. The delegation tree structure is tracked in Redis (delegation budget counters) and Postgres (session store). During a Redis outage (fail-open window), the gateway cannot reliably read the current delegation tree depth or tree size from Redis. If the gateway's fail-open behavior allows `delegate_task` calls to proceed without Redis-backed depth/size validation, a runaway recursive delegation could create arbitrarily deep trees during the outage. The `maxDelegationDepth` constraint (if specified on the DelegationPolicy) is enforced via the delegation lineage in the session's metadata (which is in Postgres), but the `maxTreeSize` and `maxParallelChildren` constraints rely on the Redis-backed budget counters.

**Recommendation:** This is related to POL-058 but specifically concerns delegation depth. Clarify that `maxDelegationDepth` enforcement does not depend on Redis (it uses the Postgres-backed session lineage) and remains enforced during Redis outages. For `maxTreeSize` and `maxParallelChildren`, cross-reference the fail-open behavior specified in POL-058's recommendation.

---

**Carried-forward skip:** POL-041 (Cross-phase priority ordering) -- skipped per instructions.

---

## 25. Execution Modes (EXM)

**EXM-049 | Medium | Section 5.2, line ~2090 | Concurrent-stateless lacks tenant pinning enforcement detail for concurrent requests**

Section 5.2, line ~2092, states that concurrent-stateless pods use the same two-layer tenant pinning mechanism as task-mode and concurrent-workspace pods: gateway records `tenantId` on first request and the admission webhook prevents label mutation. However, the concurrent-stateless section (line ~2090) also states "Gateway routes through Kubernetes Service" and "Pod readiness probe reflects slot availability." The Kubernetes Service routing model means the gateway does not directly select a specific pod for each request -- the Service load balancer does. This creates a gap: the gateway cannot enforce `tenantId` matching before routing if the Kubernetes Service selects the pod. The mechanism for ensuring the Kubernetes Service routes a given tenant's requests only to pods already pinned to that tenant (or unpinned pods) is unspecified. Session-mode and task-mode pods use the warm pool claim model where the gateway explicitly selects a pod; concurrent-stateless bypasses this model. The spec should define how tenant affinity is achieved through the Service routing layer (e.g., pod label-based service selectors per tenant, session affinity, or gateway-side pre-routing).

**EXM-050 | Medium | Section 5.2, lines ~2094-2100 | Concurrent-stateless "preferred alternative is connectors" creates an unclear v1 commitment**

Section 5.2, lines ~2094-2100, extensively describes concurrent-stateless limitations and recommends connectors as the preferred alternative for new deployments. The text reads: "`concurrencyStyle: stateless` exists for runtimes that are already deployed as Lenny pods and have minimal statefulness, but where migrating to the connector model is not yet feasible." This framing positions `stateless` as a migration bridge, not a first-class v1 feature. Yet Section 1972 declares "All three execution modes are implemented in v1" and the `executionMode` enum is `session | task | concurrent` with `concurrencyStyle: stateless | workspace`. The phasing table (line ~9740, Phase 12c) lists concurrent execution modes including `slotId` multiplexing for workspace variant but does not separately call out stateless. It is unclear whether Phase 12c covers concurrent-stateless implementation, or if stateless is deferred. The spec should explicitly state whether concurrent-stateless ships in v1 and in which phase, or explicitly defer it.

**EXM-051 | Medium | Section 5.2, lines ~2094, 2157 | Concurrent-stateless scaling controller behavior is underspecified**

Line ~2157 states: "For concurrent mode with `concurrencyStyle: stateless`, routing goes through a Kubernetes Service and pod readiness reflects slot availability, so the scaling controller monitors slot saturation directly rather than using the warm pool claim model." This is the only description of the scaling controller's interaction with concurrent-stateless. The PoolScalingController formula (Section 4.6.2) uses `mode_factor = maxConcurrent` for concurrent mode generically (line ~2134), but the `mode_factor` adjustment for workspace bottlenecks (line ~2156) applies only to `concurrencyStyle: workspace`. There is no equivalent formula adjustment for stateless. Additionally, the warm pool claim model is the primary mechanism the scaling controller uses to measure demand -- if concurrent-stateless bypasses the claim model entirely, how does the scaling controller obtain demand signals (e.g., `base_demand_p95`, `burst_p99_claims`)? The spec should define the demand signal source for concurrent-stateless scaling.

**EXM-052 | Low | Section 5.2, line ~1972 | Graph mode elimination rationale is terse but correct**

Line ~1972 states: "Graph mode is removed as a separate concept -- graph-aware runtimes are session-mode runtimes." The rationale is limited to one sentence. However, the subsequent text explains that graph-aware runtimes emit OTel spans using standard OTLP libraries, and that a dedicated `lenny/emit_span` MCP tool is deferred to post-v1. This is architecturally sound: graph-aware runtimes do not require different pod lifecycle, workspace, or isolation semantics from session-mode runtimes -- the graph awareness is purely within the runtime binary's internal execution model. No specification gap exists; the finding is informational.

**EXM-053 | Medium | Section 5.2, lines ~2102-2106 | Concurrent-workspace slot cleanup "leaked" state lacks recovery specification**

Line ~2105 defines slot cleanup behavior: "If cleanup fails, the slot is leaked -- the pod continues but the slot is not reclaimed until pod termination." The per-slot state machine (line ~2414) confirms: `slot_cleanup --> leaked (cleanup timeout exceeded -- slot not reclaimed until pod termination)`. However, the spec does not define:
1. Whether a leaked slot's `active_slots` counter is decremented. If not, the pod permanently loses capacity (the atomic Redis counter still counts the leaked slot as active, preventing new assignments to that slot position). If yes, a new assignment could overlap with the leaked slot's uncleaned workspace.
2. Whether leaked slots count toward the `ceil(maxConcurrent / 2)` unhealthy threshold that triggers whole-pod replacement (line ~2119). If leaked slots do not count as failures, a pod could accumulate `maxConcurrent - 1` leaked slots and remain in service with only one usable slot.
3. What metrics or alerts surface leaked slots. `lenny_slot_failure_total{reason}` is emitted on failure (line ~2104), but there is no `lenny_slot_leaked_total` counter.

**EXM-054 | High | Section 5.2, line ~2109 | Concurrent-workspace slot assignment atomicity has a Redis restart race condition**

Line ~2109 describes the atomic `INCR` Lua script for slot assignment. Section 12.4 (line ~5469) defines the Redis key `lenny:pod:{pod_id}:active_slots` and notes: "On Redis restart, counters reset to zero; the gateway rehydrates them from `SessionStore.GetActiveSlotsByPod(pod_id)` on first slot allocation post-recovery before accepting new slot assignments." However, the rehydration is triggered "on first slot allocation post-recovery." Between Redis restart and the first slot allocation attempt, the counter is zero. If two concurrent session assignments arrive simultaneously as the first post-recovery requests, both hit a counter of zero, both attempt the Lua `INCR` script, and both could succeed before either triggers rehydration. The spec should define: (a) whether rehydration is blocking (all allocation attempts wait until rehydration completes) or opportunistic (first allocation triggers it, subsequent allocations proceed against the stale zero), and (b) the atomicity guarantee of the rehydration-then-allocate sequence.

**EXM-055 | Medium | Section 5.2, lines ~2075, 2083 | Concurrent-workspace cleanup timeout formula inconsistency with CRD validation**

Line ~2083 states: `cleanupTimeoutSeconds: 60 # per-slot cleanup timeout is max(cleanupTimeoutSeconds / maxConcurrent, 5); must be >= maxConcurrent x 5`. Line ~2105 repeats: "Cleanup timeout is `max(cleanupTimeoutSeconds / maxConcurrent, 5)` seconds (minimum 5s enforced at runtime by the adapter)." The CRD validation rule (line ~2105) rejects configurations where `cleanupTimeoutSeconds / maxConcurrent < 5`, i.e., `cleanupTimeoutSeconds < maxConcurrent x 5`. However, the formula `max(cleanupTimeoutSeconds / maxConcurrent, 5)` already enforces a floor of 5 seconds at runtime regardless of the configured values. The CRD validation is therefore redundant with the runtime floor -- but more importantly, the CRD validation imposes a stricter constraint than the runtime formula needs. If `maxConcurrent = 8` and `cleanupTimeoutSeconds = 30`, the CRD rejects it (30 < 40), but the runtime formula would produce `max(30/8, 5) = 5`, which is valid. This means the CRD validation is more restrictive than the actual runtime behavior. While this is a conservative design choice, the spec should clarify whether the CRD validation is intentionally stricter or should match the runtime formula.

**EXM-056 | Medium | Section 5.2, line ~2010-2013 | Task mode Standard/Minimum-tier behavior has an unstated cost implication**

Lines ~2010-2013 state that task mode with Standard/Minimum-tier runtimes "effectively `maxTasksPerPod = 1` (pod terminated per task)" because these tiers lack the lifecycle channel needed for between-task signaling. This means deployers who use Standard/Minimum-tier runtimes in task mode get no pod reuse benefit -- each task requires a new pod from the warm pool, same as session mode. However, the scaling formula (Section 5.2, line ~2133) uses `mode_factor = avg_tasks_per_pod_lifetime` for task mode without distinguishing by integration tier. During cold start, the controller defaults to `mode_factor = 1.0`, which is correct for Standard/Minimum-tier. But once the controller has enough samples from Full-tier pods (default: 100 completed tasks), it will compute a `mode_factor > 1.0` based on those pods' reuse metrics. If a pool serves both Full-tier and Standard/Minimum-tier runtimes (or if a derived runtime changes tier), the elevated `mode_factor` would under-provision pods for the Standard/Minimum-tier workloads. The spec should clarify whether the scaling formula accounts for integration tier heterogeneity within a pool.

**EXM-057 | Low | Section 6.2, lines ~2382-2396 | Task-mode state diagram missing `attached --> attached` transition for between-task signal**

The task-mode state transitions at line ~2382 show `attached --> task_cleanup` as the successful completion path. But the lifecycle described at line ~2008 is: task completes, adapter sends `task_complete`, runtime replies `task_complete_acknowledged`, cleanup runs, adapter sends `task_ready`. The state diagram shows `task_cleanup --> idle` (or `draining`/`sdk_connecting`), but does not show the pod state during the `task_ready --> next task assigned --> attached` transition. The session-mode diagram has `idle --> claimed --> ... --> attached`. For task mode, the equivalent would be `idle --> claimed --> attached` (pod selected for next task). This path exists implicitly but is not drawn in the task-mode state diagram. This is a minor documentation completeness issue, not a functional gap.

**EXM-058 | Medium | Section 6.1, lines ~2321-2328 | SDK-warm (preConnect) compatibility with concurrent modes is stated but not enforced**

Lines ~2321-2328 state:
- `session` mode: `preConnect` supported.
- `task` mode: `preConnect` supported (with re-warm after scrub via `task_cleanup --> sdk_connecting` at line ~2393).
- `concurrent (workspace)`: `preConnect` NOT supported -- "multiple concurrent tasks share one process; SDK-warm pre-connects a single session."
- `concurrent (stateless)`: `preConnect` NOT supported.

The spec does not define a validation gate that rejects pool configurations combining `preConnect: true` with `executionMode: concurrent`. The pool controller validation at line ~2063 only covers `acknowledgeBestEffortScrub` for task mode and `acknowledgeProcessLevelIsolation` for concurrent-workspace. A deployer could configure `preConnect: true` on a concurrent pool and the spec does not state that this combination is rejected at validation time. The warm pool controller or CRD admission webhook should reject this combination.

**EXM-059 | Medium | Section 5.2, line ~2088 | Concurrent-workspace cross-tenant prohibition lacks admission webhook enforcement**

Line ~2088 states: "Cross-tenant slot sharing is never permitted in concurrent-workspace mode -- there is no `allowCrossTenantReuse` equivalent." The tenant pinning enforcement is described as using the same two-layer mechanism as task mode. However, for task mode, the pool controller validation explicitly rejects `allowCrossTenantReuse: true` unless `isolationProfile: microvm` (line ~1987). For concurrent-workspace, the spec states the prohibition but does not explicitly state that the pool controller rejects a `concurrentWorkspacePolicy` that includes `allowCrossTenantReuse: true`. While the field may not exist in the schema, a deployer could set `allowCrossTenantReuse: true` at the pool level (outside `concurrentWorkspacePolicy`) and it is unclear whether this is caught. The spec should explicitly state that the pool controller rejects `allowCrossTenantReuse` at any level for concurrent-workspace pools.

**EXM-060 | Low | Section 14, line ~6247 | WorkspacePlan concurrent-workspace scope note is informational, not enforced**

Line ~6247 states: "In `concurrencyStyle: workspace` pools, the `WorkspacePlan` serves as a shared template... Per-slot workspace differentiation -- different files or environment per slot -- is intentionally out of scope." This is a correct design constraint. However, the spec does not define what happens if a client submits a `WorkspacePlan` with per-slot overrides (e.g., via some future extension). The validation rule is implicit (there are no per-slot fields in the `WorkspacePlan` schema), but the spec could be more explicit that per-slot differentiation requests are rejected (not silently ignored).

**EXM-061 | Medium | Section 5.2, lines ~2106-2107 | Concurrent-workspace terminationGracePeriodSeconds interaction with node drain is a warning, not a rejection**

Line ~2106 states that when the computed `terminationGracePeriodSeconds` floor exceeds 600s, the CRD validation webhook emits a "warning (not a rejection)." This means a deployer can create a pool where the kubelet will SIGKILL pods before checkpoints complete. The spec acknowledges this: "If `terminationGracePeriodSeconds` exceeds the node drain timeout... the kubelet will SIGKILL the pod before checkpoints complete, causing data loss for in-flight slots." A warning-only approach for a data loss scenario is inconsistent with the fail-closed philosophy applied elsewhere (e.g., fail-closed admission webhooks for label immutability, data residency, etc.). While the spec justifies this by noting that cluster drain timeouts vary and are not always introspectable, the inconsistency should be noted. Consider making this a rejection when the floor exceeds a configurable threshold.

**EXM-062 | Low | Section 5.2, line ~2092 | Concurrent-stateless tenant pinning rejects allowCrossTenantReuse but does not mention T4 prohibition**

Line ~2092 states that the pool controller rejects `concurrencyStyle: stateless` pools that set `allowCrossTenantReuse: true`. However, for task mode, there is an additional T4 prohibition (line ~1989): `allowCrossTenantReuse: true` is rejected for T4-tier pools even with microvm isolation. For concurrent-stateless, since `allowCrossTenantReuse` is already universally rejected, the T4 check is redundant. But for concurrent-workspace, the T4 prohibition is not mentioned (cross-tenant reuse is already fully prohibited for concurrent-workspace at line ~2088). This is consistent -- all three findings arrive at the same outcome (no cross-tenant reuse for concurrent modes) -- but through different reasoning paths. The spec would benefit from a summary table mapping `(executionMode, concurrencyStyle)` to cross-tenant reuse rules.

---
