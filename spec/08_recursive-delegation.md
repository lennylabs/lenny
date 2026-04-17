## 8. Recursive Delegation

### 8.1 Design Philosophy

Recursive delegation is a **platform primitive**, not a hardcoded orchestration pattern. The gateway provides the foundational operations; the pod binary decides whether and how to use them.

Every pod runs the same orchestration-capable runtime. Whether it acts as a pure worker, a delegating orchestrator, or both is determined by the agent binary.

### 8.2 Delegation Mechanism

When a parent pod wants to delegate, it calls the single `lenny/delegate_task` tool on the platform MCP server:

```
lenny/delegate_task(
  target: string,
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

Target id is **opaque** — the runtime does not know whether the target is a standalone runtime, derived runtime, or external registered agent. No separate `external_delegate` tool.

`TaskSpec` (delegation subset — fields available to the calling runtime via `lenny/delegate_task`):

```json
{
  "input": ["OutputPart[]"],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

The gateway augments the delegation `TaskSpec` with routing metadata (resolved runtime, tenant context, credential assignment, and delegation parameters from the parent's lease) before processing. Interceptors ([Section 4.8](04_system-components.md#48-gateway-policy-engine)) receive this augmented form. The full session creation schema — including `env`, `runtimeOptions`, `retryPolicy`, `timeouts`, and `workspacePlan` — is defined in [Section 14](14_workspace-plan-schema.md).

**`LeaseSlice`** defines the budget allocated from parent to child:

| Field                 | Type | Description                           |
| --------------------- | ---- | ------------------------------------- |
| `maxTokenBudget`      | int  | Token budget for child tree           |
| `maxChildrenTotal`    | int  | Max children the child may spawn      |
| `maxTreeSize`         | int  | Contribution limit toward the tree-wide pod cap |
| `maxParallelChildren` | int  | Max concurrent children for the child |
| `perChildMaxAge`      | int  | Max wall-clock seconds for the child  |

All fields are optional. Defaults are derived from the deployer-configured `DelegationPolicy` ([Section 8.3](#83-delegation-policy-and-lease)). In practice, most runtimes omit `lease_slice` entirely and let the policy defaults apply — no existing agent framework (LangChain/LangGraph, CrewAI, AutoGen) implements LLM-driven budget allocation at delegation time. The `lease_slice` parameter exists for runtimes that want to request tighter constraints than the defaults (the gateway rejects any `lease_slice` that exceeds the parent's remaining budget).

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`.

**Automatic `tracingContext` injection:** When processing a `lenny/delegate_task` call, the gateway automatically attaches the parent runtime's registered `tracingContext` (set via `lenny/set_tracing_context` MCP tool or `set_tracing_context` JSONL message) to the child's delegation lease. The LLM never sees or touches tracing context — it is infrastructure plumbing managed by the runtime, not a delegation parameter. See [Section 8.3](#83-delegation-policy-and-lease) for `tracingContext` validation rules and [Section 16.3](16_observability.md#163-distributed-tracing) for the two-tier tracing model.

**Delegation flow:**

1. Parent calls `lenny/delegate_task(target, task, lease_slice?)`
2. Gateway validates against parent's effective delegation policy and lease (depth, fan-out, budget). **Redis independence of depth and cycle checks:** `maxDelegationDepth` and cycle detection use the delegation lineage stored in the session record (Postgres-backed), not Redis counters. These checks remain fully enforced during Redis outages. `maxTreeSize` and `maxParallelChildren` depend on Redis-backed budget counters and are subject to the fail-closed behavior described in [Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes).

   **Child-token minting (internal RFC 8693 exchange).** When validation passes, the gateway mints the child session's token through an internal call to the canonical `POST /v1/oauth/token` endpoint ([§13](../spec/13_security-model.md#133-credential-flow), [§4.3](04_system-components.md#43-token-service)) with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`, `subject_token` set to the authenticated user's JWT (so the child carries the same user identity), `actor_token` set to the parent session token (populating the child's `act` claim with the parent's `sub`, `session_id`, `tenant_id`, and `delegation_depth`), `scope` narrowed per the `LeaseSlice`, and `audience` set to `lenny-gateway`. The Token Service enforces semantic preservation: `delegation_depth` is exactly `parent.delegation_depth + 1`, `exp` is capped at `min(parent.exp, now + leaseTTL)` (Unix seconds integer), `scope` can only narrow, `audience` cannot broaden, `caller_type` cannot elevate. The result is that the child session has its own token with a complete `act` chain, identical semantics to the prior in-process minting path — only the wire-level naming (`actor_token`, `act`) is new. There is no external RFC 8693 endpoint traffic for internal delegation; the exchange is an in-process Token Service call.

   **Actor-token freshness under concurrent parent rotation/revocation.** The Token Service reads the `actor_token`'s `jti` against the cluster-wide revocation cache ([§13.3](13_security-model.md#133-credential-flow) "Token rotation and revocation") **inside** the same advisory-locked transaction that issues the child token. If the parent was rotated or revoked between the `lenny/delegate_task` call and the exchange — even by microseconds — the actor_token now resolves to a revoked `jti` and the exchange is rejected with `invalid_grant` reason `actor_token_revoked`; the `DELEGATE_TASK` call returns `DELEGATION_PARENT_REVOKED` and no child pod is allocated. This closes the race where a stale parent token could otherwise mint a child that outlives the parent's legitimate lifetime. If the parent's revocation was specifically a recursive revocation request (see [§13.3](13_security-model.md#133-credential-flow)), the parent session itself is also terminated and any concurrent `lenny/delegate_task` calls pending on that parent are cancelled with `DELEGATION_PARENT_REVOKED`.

   **Retry semantics on `AUDIT_CONCURRENCY_TIMEOUT` during child minting.** If the per-tenant audit advisory lock ([§11.7](../spec/11_policy-and-controls.md#117-audit-logging) item 3) times out during the child-token exchange (the exchange's audit write cannot acquire the lock within `audit.lock.acquireTimeoutMs`), the exchange fails closed: no child token is issued, no child pod is allocated, and the parent's `lenny/delegate_task` call returns `DELEGATION_AUDIT_CONTENTION` (retriable). The parent agent MUST retry the **entire** `lenny/delegate_task` call rather than retrying only the token exchange step — this guarantees that the full admission pipeline (policy evaluation, cycle detection, interceptors, and the actor-token freshness check) re-runs on the retry, preserving correctness if the parent was rotated during the retry interval. The gateway internally retries the audit-write transaction `audit.lock.maxRetries` times before surfacing `DELEGATION_AUDIT_CONTENTION` to the caller, so in practice contention-driven retries are invisible to the client except under sustained Postgres pressure.
2a. **Cycle detection:** The gateway checks the full runtime lineage recorded in the delegation lease (the chain of resolved `(runtime_name, pool_name)` tuples from root to current node). If the resolved target's `(runtime_name, pool_name)` tuple appears anywhere in the caller's lineage, the gateway rejects the delegation immediately with `DELEGATION_CYCLE_DETECTED` before any pod allocation. Cycle detection uses runtime identity — not `session_id` — because the child session does not exist yet at this point (pod allocation happens in step 5). This catches runtime-identity cycles (e.g., A→B→A, where A and B are `(runtime_name, pool_name)` tuples) that `maxDepth` alone cannot prevent. `maxDepth` prevents infinite forwarding depth; cycle detection prevents circular wait deadlocks where the same runtime identity reappears in the delegation lineage. The [§8.8](#88-taskrecord-and-taskresult-schema) subtree deadlock detector covers subtree-internal circular waits (e.g., sibling sessions blocking on each other); lineage cycle detection covers the complementary case where a delegation chain revisits a runtime identity. **Pool-differentiated cycles** (e.g., `A/pool1 → B → A/pool2`) are intentionally **not** detected as cycles because pool differentiation is a legitimate deployment pattern — the same runtime binary may be deployed in different pools with different resource classes, isolation profiles, or credential configurations, and delegating across pools is a valid use case. `maxDepth` is the safety net that bounds any pool-differentiated delegation chain.
2b. **Interceptor chain evaluation (including `ExperimentRouter`).** The gateway runs the `PreRoute` interceptor chain ([Section 4.8](04_system-components.md#48-gateway-policy-engine)) on the child's augmented `TaskSpec`, the same chain that fires during top-level session creation. The `ExperimentRouter` (priority 300) evaluates experiment assignment for the child based on the parent's propagation mode: under `inherit` the child receives the parent's `experimentContext` verbatim and the `ExperimentRouter` is skipped; under `control` the child is forced to variant `"control"` and the `ExperimentRouter` is skipped; under `independent` the `ExperimentRouter` evaluates the child for experiment eligibility independently (it may land in a different experiment or none). See [Section 10.7](10_gateway-internals.md#107-experiment-primitives) for propagation semantics. Other `PreRoute` interceptors (e.g., content filtering) also fire during this phase.
3. Gateway asks parent runtime to export files matching the export spec (see [Section 8.7](#87-file-export-model))
4. Gateway stores exported files durably (rebased to child workspace root)
5. Gateway allocates child pod from specified pool
6. Gateway streams rebased files into child before it starts
7. Child starts with its own local workspace containing the exported files
8. Gateway creates a **virtual MCP child interface** and injects it into parent
9. Parent interacts with child through this virtual interface

**What the parent sees:** A gateway-hosted virtual MCP server with:

- Task status/result
- Elicitation forwarding
- Cancellation
- Message delivery via `lenny/send_message`

**What the parent never sees:** Pod addresses, internal endpoints, raw credentials.

**Virtual child interface lifecycle:**

- **Storage:** Virtual child interfaces live in gateway per-session memory. On parent pod failure, the gateway reconstructs them from the task tree in SessionStore (which records all child session IDs, states, and pending results).
- **Pending elicitations:** If a parent pod fails while an elicitation is pending from a child, the gateway holds that elicitation. When the parent resumes on a new pod, the gateway replays it via the re-injected virtual child interface (see the `children_reattached` event in [Section 8.10](#810-delegation-tree-recovery)).
- **Replay on resume:** The gateway re-injects all active virtual child interfaces on parent resume. Each interface carries the child's current state (running, completed, failed, `input_required`) and any pending results or elicitations. The parent agent receives a `children_reattached` event with this state.

**Delegation tree memory management:**

Each node in a delegation tree carries in-memory state on the gateway replica that owns the root session. Estimated per-node memory footprint:

| Component                  | Estimate    | Notes                               |
| -------------------------- | ----------- | ----------------------------------- |
| Virtual child interface    | ~2 KB       | MCP server shim, routing metadata   |
| Event buffer (pending)     | ~8 KB       | Capped at 64 events × ~128 B avg    |
| Elicitation state          | ~1 KB       | At most one pending per node        |
| Task metadata + result ref | ~1 KB       | IDs, status, timestamps             |
| **Total per node**         | **~12 KB**  |                                     |
| **50-node tree**           | **~600 KB** | Maximum under default `maxTreeSize` |

The delegation lease includes a `maxTreeMemoryBytes` field (default: `2097152` / 2 MB) that caps the aggregate in-memory footprint of a single delegation tree on the gateway. The gateway tracks cumulative tree memory via an atomic Redis counter alongside the existing `maxTreeSize` counter. When a new delegation would push the tree over `maxTreeMemoryBytes`, it is rejected with `BUDGET_EXHAUSTED`. The memory counter is included in the periodic Postgres checkpoint and is reconstructed on Redis recovery alongside other delegation budget counters (see [Section 11.2](11_policy-and-controls.md#112-budgets-and-quotas)).

**Completed subtree offloading:** When a child session reaches a terminal state (completed, failed, cancelled, expired), the gateway offloads its virtual child interface state and buffered results to Postgres (`session_tree_archive` table, keyed by `(root_session_id, node_session_id)`). The in-memory node is replaced by a lightweight stub (~200 B) containing the child session ID, terminal status, and a `pg_archived: true` flag. If the parent later reads the child's result, the gateway fetches it from Postgres on demand (with a per-replica LRU cache, default 128 entries). This ensures that long-running trees with many completed branches do not accumulate unbounded memory. The `maxTreeMemoryBytes` counter is decremented when a node is offloaded.

### 8.3 Delegation Policy and Lease

#### `DelegationPolicy` as First-Class Resource

`allowedRuntimes`, `allowedConnectors`, and `allowedPools` fields are replaced by named `DelegationPolicy` resources with tag-based matching evaluated at delegation time:

```yaml
name: orchestrator-policy
rules:
  - target:
      matchLabels:
        team: platform
      types: [agent]
    allow: true
  - target:
      ids: [github, jira]
      types: [connector]
    allow: true
contentPolicy:
  maxInputSize: 131072 # max bytes for TaskSpec.input per delegation (default: 128KB)
  interceptorRef: null # optional ref to a RequestInterceptor for content scanning
```

**`contentPolicy` enforcement (prompt injection mitigation):** The gateway enforces `contentPolicy` on every `delegate_task` call. `maxInputSize` is a hard byte-size limit on `TaskSpec.input` — delegations exceeding it are rejected with `INPUT_TOO_LARGE` before pod allocation. When `interceptorRef` is set, the gateway invokes the referenced `RequestInterceptor` at the `PreDelegation` phase (see [Section 4.8](04_system-components.md#48-gateway-policy-engine)) with the full `TaskSpec.input` as payload. The interceptor can `ALLOW`, `REJECT`, or `MODIFY` the content. This is the primary hook for deployers to integrate external content classifiers (e.g., prompt injection detectors) into delegation chains. `contentPolicy` is inherited by child leases and can only be made stricter (smaller `maxInputSize`, same or more restrictive `interceptorRef`).

**`interceptorRef` restrictiveness enforcement rule.** The gateway cannot introspect the internal logic of two different named interceptors to compare their relative restrictiveness — named interceptors are opaque gRPC services. The concrete enforcement rule is therefore **identity-based**: a child lease's `interceptorRef` is considered "at least as restrictive" as the parent's if and only if it satisfies one of the following conditions (evaluated in order):

1. **Same reference:** The child `interceptorRef` names the same interceptor as the parent (identical name string). This is always permitted.
2. **Chained interceptor (trust-based):** Because `interceptorRef` is a scalar field (a single interceptor name), the platform cannot verify that a child's named interceptor internally chains calls to the parent's interceptor. This condition is therefore **trust-based, not enforced**: a child may name a different interceptor that the deployer has configured to internally invoke the parent's interceptor as a sub-call. The platform accepts this configuration without verification — deployers are responsible for ensuring the chaining is correctly implemented. If the deployer cannot guarantee chaining, condition 5 (different non-null reference → rejected) applies instead.
3. **Null-to-non-null:** The child sets an `interceptorRef` when the parent had `null`. This is always permitted (adding a check is strictly more restrictive).
4. **Non-null-to-null:** The child sets `interceptorRef: null` when the parent had a non-null reference. This is **rejected** with `CONTENT_POLICY_WEAKENING` — removing a content check always weakens policy.
5. **Different non-null reference:** The child names a different interceptor than the parent's (without retaining the parent's). The gateway **rejects** this with `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`. A child lease cannot substitute the parent's named interceptor with an unrelated one, because the platform cannot verify the substitute is equally or more restrictive.

**Runtime changes to `failPolicy` do not retroactively affect lease restrictiveness.** A child lease's `interceptorRef` restrictiveness is evaluated at lease issuance time against the current interceptor registration state. If the interceptor's `failPolicy` is subsequently changed from `fail-closed` to `fail-open` (making it effectively less restrictive), this does not invalidate existing leases — the lease is already issued and the `interceptorRef` is recorded by name. To prevent this from being a silent weakening, the gateway emits an `interceptor.fail_policy_weakened` audit event (see [Section 4.8](04_system-components.md#48-gateway-policy-engine)) whenever a `failPolicy` is changed from `fail-closed` to `fail-open`, including the list of active `DelegationPolicy` resources that reference the affected interceptor. Deployers should monitor these events and treat a `failPolicy` weakening with the same care as weakening a policy rule. The change is recommended only with explicit review of all active `DelegationPolicy` resources that reference that interceptor.

**Two policy levels:**

- **Runtime-level policy** (deployment time, tag rules) — set via `delegationPolicyRef` on the Runtime
- **Derived runtime policy** (post-deployment, can only restrict) — set via `delegationPolicyRef` on the derived runtime

**Effective policy = `base_policy ∩ derived_policy`** — derived runtime policy can only restrict.

**Dynamic tag evaluation at delegation time.** Tags can change without redeploying — policy is re-evaluated on each `delegate_task` call using the pool's live labels at the moment of that call.

**Tag evaluation semantics and security implications:**

- **Point-in-time evaluation.** Policy evaluation is point-in-time: the gateway reads pool labels from the registry at the instant `delegate_task` is processed. A delegation that was authorized at the time of the call is not retroactively revoked if pool labels change after the child session is running.
- **Mid-session label changes do not affect active child sessions.** Once a child session is running, its delegation was already approved. Label changes on the target pool between `delegate_task` and the child's termination have no effect on the active session — there is no continuous re-evaluation against running sessions.
- **Subsequent delegations are affected.** A new `delegate_task` call made after a label change will be evaluated against the updated labels. This means a child attempting to spawn its own children may be denied if labels changed between the parent's delegation and the grandchild's attempted delegation.
- **Security implication.** Because pool labels are live and mutable, a pool operator who modifies labels after a `DelegationPolicy` is registered can dynamically change which delegations are authorized. This is intentional — it allows runtime gating of pools during incidents (e.g., removing a label to block new delegations without redeploying). Deployers who require immutable policy snapshots per tree should use `snapshotPolicyAtLease: true` (see below).
- **`snapshotPolicyAtLease: true` option.** When set on the `DelegationPolicy`, the gateway snapshots the set of matching pool IDs for **all** `DelegationPolicy` resources in the effective policy computation — including `delegationPolicyRef`, `maxDelegationPolicy`, and any derived-runtime policy — at lease issuance time (when the root session starts) and records them in the delegation lease. All subsequent `delegate_task` calls in that tree evaluate policy against the snapshotted pool set rather than live labels. This provides stable, predictable delegation behavior for long-running trees at the cost of not picking up pool label changes mid-tree. The snapshot is stored as `snapshotted_pool_ids` in the lease record. Default: `false`. **Scope of snapshot:** `snapshotPolicyAtLease` snapshots the policy document and the set of matching pool IDs only — it does not snapshot the configuration of referenced interceptors (e.g., `contentPolicy.interceptorRef`). The interceptor named by `contentPolicy.interceptorRef` is always invoked with its live configuration at the time of each `delegate_task` call, regardless of whether the policy was snapshotted. This means that updating an interceptor's `failPolicy` (e.g., from `fail-closed` to `fail-open`) or its filtering rules affects all active delegation trees that reference it, even trees with snapshotted policies. Operators should be aware that the `interceptor.fail_policy_weakened` audit event ([Section 4.8](04_system-components.md#48-gateway-policy-engine)) surfaces such changes.

**`maxDelegationPolicy` — session-level policy cap.** The `maxDelegationPolicy` field on the delegation lease is a named reference to a `DelegationPolicy` resource (the same type used by `delegationPolicyRef`). It is typed as `string | null`.

- **`null` (default):** No additional restriction. The session's effective policy is solely the intersection of the runtime-level `delegationPolicyRef` and any derived-runtime policy.
- **Non-null (named `DelegationPolicy` reference):** The referenced policy is applied as an additional **intersection** on top of `delegationPolicyRef`. The effective policy becomes `delegationPolicyRef ∩ maxDelegationPolicy` — restriction only, never expansion. A `maxDelegationPolicy` cannot grant permissions that the base `delegationPolicyRef` does not already allow.
- **Precedence:** `maxDelegationPolicy` is evaluated after `delegationPolicyRef` and the derived runtime policy. It is the final restriction layer at session scope.
- **Inheritance:** Child leases inherit the intersection of the parent's effective policy; they cannot specify a `maxDelegationPolicy` that is less restrictive than the parent's effective `maxDelegationPolicy`. The same "restriction only" rule applies at every level.

**Example:** A runtime has `delegationPolicyRef: "broad-policy"` (allows all platform runtimes). A session lease specifies `maxDelegationPolicy: "read-only-policy"` (allows only read-only agent runtimes). The effective policy allows only runtimes that satisfy both `broad-policy` AND `read-only-policy`. Child leases spawned from this session cannot expand beyond `read-only-policy` even if they reference `broad-policy` directly.

**Discovery scoping:** `lenny/discover_agents` returns only targets authorized by the calling session's effective delegation policy. Returns `type: agent` runtimes and external agents only — `type: mcp` runtimes do not appear.

**`DelegationPolicy` deletion guard.** Deleting a `DelegationPolicy` via `DELETE /v1/admin/delegation-policies/{name}` is rejected with `RESOURCE_HAS_DEPENDENTS` (HTTP 409) if any of the following active references exist: (a) a Runtime or derived runtime whose `delegationPolicyRef` names this policy, or (b) an active (non-terminal) delegation lease whose `delegationPolicyRef` or `maxDelegationPolicy` names this policy. The `details.dependents` array in the error response lists blocking references by type (`runtime`, `derived_runtime`, or `delegation_lease`), name (runtime name or root session ID), and count — consistent with the `RESOURCE_HAS_DEPENDENTS` schema defined in the error catalog ([Section 15.1](15_external-api-surface.md#151-rest-api)). To delete a policy with active leases, the deployer must either wait for all referencing sessions to reach a terminal state or update the referencing runtimes to point to a different policy first. This prevents dangling `delegationPolicyRef` references that would cause undefined behavior at delegation time.

#### Delegation Lease

Every delegating session carries a **delegation lease** that defines its quantitative authority:

```json
{
  "maxDepth": 3,
  "maxChildrenTotal": 10,
  "maxParallelChildren": 3,
  "maxTreeSize": 50,
  "maxTokenBudget": 500000,
  "delegationPolicyRef": "orchestrator-policy",
  "maxDelegationPolicy": null,
  "minIsolationProfile": "sandboxed",
  "perChildRetryBudget": 1,
  "perChildMaxAge": 3600,
  "fileExportLimits": { "maxFiles": 100, "maxTotalSize": "100MB" },
  "approvalMode": "policy",
  "cascadeOnFailure": "cancel_all",
  "credentialPropagation": "independent",
  "allowedExternalEndpoints": [],
  "messagingRateLimit": {
    "maxPerMinute": 30,
    "maxPerSession": 200,
    "maxInboundPerMinute": 60
  },
  "maxTreeMemoryBytes": 2097152,
  "snapshotPolicyAtLease": false,
  "experimentContext": null,
  "tracingContext": null
}
```

Child leases are always **strictly narrower** than parent leases (depth decremented, budgets reduced).

**`experimentContext`** — nullable object carrying the session's experiment enrollment, populated by the `ExperimentRouter` at the `PreRoute` phase ([Section 10.7](10_gateway-internals.md#107-experiment-primitives)). When present, it contains `experimentId` (string), `variantId` (string), and `inherited` (boolean — `true` when propagated from a parent via `inherit` or `control` mode, `false` when independently assigned). The field is `null` when the session is not enrolled in any experiment. Experiment context propagation semantics (how this field is populated on child leases under `inherit`, `control`, and `independent` modes) are defined in [Section 10.7](10_gateway-internals.md#107-experiment-primitives). The `experimentContext` is delivered to the runtime in the adapter manifest ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification)) so that runtimes can tag traces with variant metadata for filtering and grouping in their eval platform.

**`tracingContext`** — nullable `map<string, string>` carrying opaque tracing identifiers registered by the runtime via `lenny/set_tracing_context` (MCP) or `set_tracing_context` (JSONL). The gateway automatically attaches the parent runtime's registered `tracingContext` to the child's delegation lease when processing a `lenny/delegate_task` call — the LLM never sees or touches tracing context. The child runtime receives it in the adapter manifest ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification)) and uses it to stitch its native traces into the parent's trace tree (e.g., creating a LangSmith child run under the parent's run ID). Child runtimes may extend the inherited `tracingContext` with additional entries via `lenny/set_tracing_context`, subject to the same validation rules. Child entries are merged with parent entries; child entries cannot overwrite or remove parent entries.

**`tracingContext` validation (gateway-enforced):**

| Constraint | Limit | Error code |
| --- | --- | --- |
| Max serialized size | 4 KB | `TRACING_CONTEXT_TOO_LARGE` |
| Max key length | 128 bytes | `TRACING_CONTEXT_TOO_LARGE` |
| Max value length | 256 bytes | `TRACING_CONTEXT_TOO_LARGE` |
| Max entries | 32 | `TRACING_CONTEXT_TOO_LARGE` |
| Key name blocklist (case-insensitive): patterns matching `*secret*`, `*token*`, `*password*`, `*key*`, `*credential*`, `*authorization*` | Rejected | `TRACING_CONTEXT_SENSITIVE_KEY` |
| Value URL blocklist: values starting with `http://` or `https://` | Rejected | `TRACING_CONTEXT_URL_NOT_ALLOWED` |

Tracing endpoint URLs (where to send traces) are deployer configuration — set via pool-level environment variables or the runtime's own config. Parent runtimes propagate only identifiers (trace IDs, run IDs, span IDs), never endpoint URLs or credentials. This separation ensures a malicious parent cannot redirect a child's tracing to an attacker-controlled endpoint.

**`tracingContext` audit and data lifecycle:** Delegation audit events (`delegation.created`, `delegation.completed`) log `tracingContext` keys only — values are redacted. `tracingContext` values are deleted alongside session data during GDPR erasure ([Section 12.8](12_storage-architecture.md#128-compliance-interfaces)).

**`fileExportLimits` sizing guidance:** The default `maxTotalSize: 100MB` is conservative and appropriate for most delegation workflows. For workflows that produce large build artifacts (e.g., compiled binaries, container images), deployers should increase `maxTotalSize` per delegation preset — up to the workspace size SLO ceiling (500 MB) if needed. Note that file exports transit through the gateway (parent pod → MinIO → child pod), so larger limits increase gateway I/O and MinIO bandwidth proportionally. Deployers should size MinIO I/O capacity accordingly when configuring higher limits.

**`perChildRetryBudget`** — maximum number of automatic retry attempts the gateway will make for each child session spawned under this lease, independent of the parent session's own `retryPolicy.maxRetries`. When a child session fails with a retryable failure (e.g., `pod_evicted`, `node_lost`), the gateway retries up to `perChildRetryBudget` times before marking the child as permanently failed. Each retry attempt consumes one unit from the child's retry budget but does **not** consume from the parent's `retryPolicy.maxRetries` (those are separate scopes: `retryPolicy` governs the session's own recovery; `perChildRetryBudget` governs recovery of delegated children). Each retry re-uses the child's already-reserved token budget slice — no additional `budget_reserve.lua` call is made for retries, so the `maxTokenBudget` allocation is unchanged. However, each retry does allocate a new pod, so it counts against the parent's `maxTreeSize` (the failed pod's tree-size slot is released by `budget_return.lua` before the retry pod is reserved). Default: `1` (one retry per child). Set to `0` to disable child retries entirely. This field is **not extendable** via lease extensions — it is a reliability boundary, not a resource budget.

**`allowedExternalEndpoints`** slot exists from v1 for future A2A support — controls which external agent endpoints can be delegated to.

**`messagingRateLimit`** — rate limits for `lenny/send_message`. `maxPerMinute` is a per-session outbound sliding-window burst limit; `maxPerSession` is a per-session lifetime cap. `maxInboundPerMinute` is a per-session **inbound** aggregate limit — the gateway enforces this on the **receiving** session, counting all messages arriving from any sender in the delegation tree. This prevents N compromised siblings from flooding a single target at N × `maxPerMinute`; regardless of the number of senders, the target accepts at most `maxInboundPerMinute` messages per sliding window. Messages exceeding the inbound limit receive a `RATE_LIMITED` delivery receipt. Exceeding any limit returns `RATE_LIMITED`. Child leases inherit the parent's limits (or stricter). Defaults are deployment-configurable via Helm.

**Isolation monotonicity:** Children must use an isolation profile **at least as restrictive** as their parent. The enforcement order is: `standard` (runc) < `sandboxed` (gVisor) < `microvm` (Kata). A `sandboxed` parent cannot delegate to a `standard` child. The `minIsolationProfile` field in the lease enforces this, and the gateway validates it before approving any delegation.

**Enforcement point and audit trail:** Isolation monotonicity is enforced **at delegation time** (runtime), not at `DelegationPolicy` registration time. This means a tag-based policy rule (e.g., `matchLabels: { team: platform }`) may match pools with varying isolation levels, and a monotonicity violation is only detected when a specific delegation is attempted. When the gateway rejects a delegation due to an isolation monotonicity violation, it emits a `delegation.isolation_violation` audit event (see [Section 11.7](11_policy-and-controls.md#117-audit-logging)) containing the parent session ID, requested target, parent isolation profile, target isolation profile, and the `DelegationPolicy` rule that matched. The delegation is rejected with error code `ISOLATION_MONOTONICITY_VIOLATED`.

**Proactive pool-registration enforcement:** In addition to delegation-time enforcement, the gateway performs a proactive isolation audit whenever a pool is created or updated via `POST /v1/admin/pools` or `PUT /v1/admin/pools/{name}`. After persisting the pool, the gateway evaluates all active `DelegationPolicy` resources against the new or updated pool's isolation profile. For every `DelegationPolicy` rule that could match the new pool as a delegation target and where a parent pool matchable by the same rule has a **more restrictive** isolation profile, the gateway emits a `pool.isolation_warning` audit event (see [Section 11.7](11_policy-and-controls.md#117-audit-logging)) containing the pool name, its isolation profile, the matching `DelegationPolicy` rule, the conflicting parent pool, and the parent's isolation profile. These events are emitted asynchronously and do **not** block pool registration — the pool is created regardless. This ensures that a newly registered pool with a weaker isolation profile immediately surfaces as a visible warning in the audit log rather than silently waiting to cause a `ISOLATION_MONOTONICITY_VIOLATED` rejection at runtime.

> **Deployer guidance:** The gateway automatically emits `pool.isolation_warning` audit events (see [Section 11.7](11_policy-and-controls.md#117-audit-logging)) whenever a newly registered or updated pool would introduce a potential isolation monotonicity violation in an existing `DelegationPolicy`. Monitor these events in the audit log or SIEM after any pool registration. The `lenny-ctl policy audit-isolation` CLI command (see [Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) can be used on demand to report all current `DelegationPolicy` rule × pool combinations where a delegation would be rejected at runtime due to a monotonicity violation.

**Tree-wide limits:** `maxTreeSize` caps the total number of pods across the entire task tree (all depths), preventing exponential fan-out. `maxTokenBudget` caps total LLM token consumption across the tree.

**Budget Reservation Model:**

Delegation budgets use an **atomic reservation** model, not a ceiling model. When the gateway processes a `delegate_task` call:

1. **Reservation:** The gateway reserves budget using a single Redis Lua script (`budget_reserve.lua`) that atomically reads the parent's token budget counter, the parent's actual token usage counter, the tree-size counter, the parent's `childrenTotal` counter (number of children already spawned), the parent's `parallelChildren` counter (number of currently in-flight children), and the tree-memory counter (cumulative `maxTreeMemoryBytes` usage), then applies the token `DECRBY`, tree-size `INCR`, `childrenTotal` `INCR`, `parallelChildren` `INCR`, and tree-memory `INCRBY` (adding the estimated per-node memory footprint) within one evaluation. The effective remaining budget is computed inside the script as `parentBudget - parentUsage`; the child slice is capped to `min(requested_slice, parentBudget - parentUsage)` before any check. The Lua script checks the effective remaining budget, the tree-size limit, `childrenTotal < maxChildrenTotal`, `parallelChildren < maxParallelChildren`, and `currentTreeMemory + nodeMemoryEstimate ≤ maxTreeMemoryBytes` before committing any change; if any check fails, no operation is applied and the script returns a structured result indicating which limit was exceeded (`TOKEN_BUDGET_EXHAUSTED`, `TREE_SIZE_EXCEEDED`, `CHILDREN_TOTAL_EXCEEDED`, `PARALLEL_CHILDREN_EXCEEDED`, or `TREE_MEMORY_EXCEEDED`). Reading the usage counter atomically inside the same script eliminates the race where a parent has nearly exhausted its own token usage while its delegation budget counter still reflects the full original allocation. This eliminates TOCTOU windows between all six counters and removes the need for compensating rollback operations. If the remaining budget is insufficient, the delegation is rejected with `BUDGET_EXHAUSTED` before pod allocation. The `parallelChildren` counter and tree-memory counter are decremented by `budget_return.lua` when a child session reaches a terminal state (step 3).

   **Lua script serialization and contention analysis.** Redis executes Lua scripts atomically — the entire `budget_reserve.lua` script runs without interruption. This means that concurrent `delegate_task` calls from the same session (fan-out) are serialized: each invocation blocks all other Redis operations on that primary for the script duration. The script's operation count is fixed (6 READs + 5 conditional WRITEs = 11 operations); at modern Redis speeds this executes in under 100 µs on unloaded hardware, providing acceptable throughput for modest fan-out. However, at high `maxParallelChildren` values, serialization contention can become the bottleneck:

   | `maxParallelChildren` | Estimated reservation burst rate | Approximate script serialization time per burst |
   | --------------------- | --------------------------------- | ----------------------------------------------- |
   | ≤ 10                  | ~100 reservations/s per session   | < 10 ms per burst (negligible)                  |
   | 11–50                 | ~500 reservations/s per session   | < 50 ms per burst (low)                         |
   | 51–100                | ~1,000 reservations/s per session | ~100 ms per burst (moderate; monitor P99)       |
   | > 100                 | > 1,000 reservations/s            | > 100 ms per burst (high; see ceiling guidance) |

   These estimates assume the Quota/Rate Limiting Redis instance is dedicated to that concern (separated from Coordination and Cache/Pub-Sub as recommended in [Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) and that the Lua script is the primary operation in that window. Mixed workloads (concurrent rate limit `INCR`s, quota checkpoints) reduce throughput proportionally.

   **`maxParallelChildren` ceiling guidance.** To prevent Lua script serialization from degrading the Redis P99 latency of other operations, deployers should observe the following limits per delegation lease:

   - **Soft ceiling: `maxParallelChildren ≤ 50`** — safe at Tier 2 and Tier 3 for the default Quota/Rate Limiting Redis topology (single primary with Sentinel). Monitor `lenny_redis_lua_script_duration_seconds{script="budget_reserve"}` P99; if it exceeds 5 ms sustained, reduce fan-out or split the Redis instance.
   - **Hard ceiling: `maxParallelChildren ≤ 100`** — above this value, Lua serialization bursts can spike Redis P99 latency above the 5 ms `LeaseStore` SLO ceiling ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) "When Sentinel becomes insufficient"), impacting lease renewal reliability. Leases with `maxParallelChildren > 100` should be considered only for isolated workloads running on a dedicated Quota Redis instance with no other tenants sharing the primary.
   - **The `orchestrator` preset (`maxParallelChildren: 10`) is safe for all tiers.** Deployments using only built-in presets do not need to review this guidance.

   The metric `lenny_delegation_parallel_children_high_watermark` (histogram, labeled by `pool`, `tenant_id`) records the maximum simultaneous in-flight children observed for each delegation tree at tree completion, enabling operators to detect fan-out that approaches the ceiling. The `root_session_id` is **not** a Prometheus label (it would create unbounded cardinality); per-tree diagnostics are available via structured logs and traces.

   **Cross-tenant aggregate Lua contention.** The per-session ceiling guidance above addresses intra-session fan-out. At Tier 3, a second independent concern arises: aggregate cross-tenant blocking from concurrent `budget_reserve.lua` invocations across all sessions simultaneously. Because Redis executes Lua scripts atomically on the primary, concurrent scripts from different tenants are equally serialized — one script blocks all others regardless of tenant ownership. At Tier 3 peak, ~500 concurrent `delegate_task` calls cluster-wide (across all tenants) may land within the same Redis scheduling slot. At 100 µs per script, 500 serialized invocations produce ~50 ms of aggregate blocking on the primary. This exceeds the 5 ms `LeaseStore` P99 SLO ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) "When Sentinel becomes insufficient"), because lease renewal `SET` operations issued during the blocking window are delayed — risking false lease expirations for unrelated tenants.

   **Aggregate contention formula.** The expected aggregate blocking window is:

   ```
   T_block = N_concurrent_scripts × T_script_duration
           = (delegation_rate × T_script_duration)
   ```

   where `T_script_duration` ≈ 100 µs on unloaded hardware and `delegation_rate` is the instantaneous cluster-wide `delegate_task` rate. The 5 ms `LeaseStore` SLO is breached when `T_block > 5 ms`, i.e., when `N_concurrent_scripts > 50`. At sustained Tier 3 delegation rates of 500–1,000/s, burst clustering routinely yields `N_concurrent_scripts > 50`.

   **Cross-tenant instance separation trigger.** The Quota/Rate Limiting Redis instance separation ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) "Logical separation of Redis concerns") mitigates cross-tenant `INCR` write contention but does **not** eliminate cross-tenant Lua serialization contention — all `budget_reserve.lua` calls still serialize on whichever Redis primary hosts the Quota instance. The additional trigger for separating the delegation budget Lua workload from general quota and rate-limit counters is:

   - **Delegation Lua P99 > 2 ms sustained** (per `lenny_redis_lua_script_duration_seconds{script="budget_reserve"}`) — this leaves less than 3 ms headroom before the 5 ms `LeaseStore` SLO is impacted by cross-operation queuing.
   - **Cluster-wide instantaneous `delegate_task` rate exceeds 50/s** — at this rate, aggregate blocking approaches the 5 ms threshold even at baseline script duration.

   When either trigger fires, the recommended remediation is to route delegation budget Lua scripts to a dedicated Redis instance (or a dedicated shard in the Cluster topology), isolated from lease renewals and general quota counters. Because each store role has its own connection interface ([Section 12.6](12_storage-architecture.md#126-interface-design)), this is a configuration-only change — route `DelegationBudgetStore` to a separate `redis.delegationBudgetBackend` connection string. At Tier 3 with Redis Cluster already deployed for Quota/Rate Limiting, the delegation budget keys (`{root_session_id}:dlg:*`) can be pinned to a dedicated shard group by using a hash tag that routes to the shard least shared with high-throughput `INCR` keys. Refer to [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults) for per-tier guidance on when this separation is warranted.

   **Delegation budget key hash tag convention (R-04).** `budget_reserve.lua` and `budget_return.lua` MUST use `{root_session_id}` as the Redis hash tag in all delegation budget key names. The `root_session_id` is the session ID of the tree's root node and is propagated through all levels of the delegation lease — every gateway replica processing a `delegate_task` at any depth uses the tree's `root_session_id` (not the calling session's own ID) to compute the Redis hash tag. Keys are divided into two categories:

   - **Tree-wide keys** (one per delegation tree): `{root_session_id}:dlg:tokens`, `{root_session_id}:dlg:tree_size`, `{root_session_id}:dlg:tree_memory`.
   - **Per-parent keys** (one per delegating parent session within the tree): `{root_session_id}:dlg:parallel_children:{parent_session_id}`, `{root_session_id}:dlg:children_total:{parent_session_id}`. These enable per-node enforcement of `maxParallelChildren` and `maxChildrenTotal` limits independently for each intermediate node. `childrenTotal` is a monotonic lifetime counter and is **never** decremented by `budget_return.lua` — only `parallelChildren` (a concurrency counter) is decremented when a child reaches a terminal state.

   Using `{root_session_id}` as the hash tag ensures that all keys for a given delegation tree (both tree-wide and per-parent) hash to the same Redis slot, preserving multi-key atomicity within the Lua script while distributing different delegation trees across distinct slots in a Redis Cluster deployment. This is intentionally different from the `t:{tenant_id}:` convention used for other Redis roles: if `{tenant_id}` were used instead, every delegation tree belonging to the same tenant would hash to a single slot, creating a hot slot under high fan-out workloads. The `{root_session_id}` hash tag is a documented exception to the [Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) tenant-prefix convention (see the [§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) key prefix table for the full entry). Tenant isolation is enforced at the gateway application layer: the gateway validates that `root_session_id` belongs to the calling tenant (via `SessionStore` under RLS) before invoking any delegation budget Lua script.

   **Defense-in-depth TTL.** `budget_reserve.lua` SHOULD set a TTL on all delegation budget keys equal to `delegation.budgetKeyTTLSeconds` (default: 259200s / 72h, configurable via Helm) at tree creation time. The gateway passes this value as an ARGV parameter on the first invocation for a given root session (i.e., when the root tree-wide keys are created). The TTL is deliberately generous — it must never fire during normal operation, including sessions that spend extended time in `suspended` state (where `maxSessionAge` is paused and the session may persist indefinitely after pod release; see [§6.2](06_warm-pod-model.md#62-pod-state-machine) `maxSuspendedPodHoldSeconds`). It exists solely as a safety net for cases where the [§12.8](12_storage-architecture.md#128-compliance-interfaces) explicit purge (see tenant deletion Phase 4 and `DeleteByUser` step 15) fails or is interrupted. The [§12.8](12_storage-architecture.md#128-compliance-interfaces) explicit purge remains the primary cleanup mechanism for compliance SLAs.

   **`BUDGET_KEYS_EXPIRED` detection and tree cleanup.** Both `budget_reserve.lua` and `budget_return.lua` MUST check for the existence of the tree-wide sentinel key (`{root_session_id}:dlg:tokens`) before operating on any budget keys. If the sentinel key does not exist (i.e., `EXISTS {root_session_id}:dlg:tokens` returns 0), the script MUST return a `BUDGET_KEYS_EXPIRED` error status without modifying any keys. This prevents silent misbehavior on TTL expiry or premature deletion: without this check, `budget_reserve.lua` would read counters as 0 and return `TOKEN_BUDGET_EXHAUSTED` (silent freeze), and `budget_return.lua` would recreate partial keys via `INCRBY` on nonexistent keys (silent data corruption — counters become inconsistent).

   When the gateway receives `BUDGET_KEYS_EXPIRED` from either Lua script, it initiates an immediate tree cleanup:

   1. **Cascade cancel** the entire delegation tree — same mechanism as root session cancellation ([§8.10](#810-delegation-tree-recovery) cascade cancellation). All active child sessions receive cancel signals.
   2. **Skip budget returns** for cancelled children. Since the budget keys are gone, `budget_return.lua` would also return `BUDGET_KEYS_EXPIRED`. The gateway marks these children as `cancelled` without attempting budget return.
   3. **Transition the root session to `failed`** with reason `BUDGET_KEYS_EXPIRED`. This is a non-retryable failure — the delegation tree's accounting state is irrecoverable.
   4. **Emit a `delegation.budget_keys_expired` critical structured event** recording `root_session_id`, `tenant_id`, and the operation that triggered detection (`reserve` or `return`). This event should trigger an operator alert (`DelegationBudgetKeysExpired`) — TTL expiry during normal operation indicates the TTL is configured too short or the [§12.8](12_storage-architecture.md#128-compliance-interfaces) explicit purge ran prematurely.

   The `BUDGET_KEYS_EXPIRED` detection can occur in any state where delegation operations execute: `running` (during `delegate_task` or child completion), `input_required` (a child completes while the parent is blocked in `lenny/request_input`, triggering `budget_return.lua`), and `suspended` (if a child completes while the parent is suspended, triggering `budget_return.lua`). All trigger `→ failed` transitions — see the session state machines in [§6.2](06_warm-pod-model.md#62-pod-state-machine) and [§7.2](07_session-lifecycle.md#72-interactive-session-model).

2. **Default slice:** When `lease_slice` is omitted, the child receives `min(remaining_parent_budget, deployer_configurable_default_fraction)`. The default fraction is 50% of remaining budget, configurable per environment via `defaultDelegationFraction` (range: 0.1 to 1.0). If the remaining budget is below a minimum usable threshold (configurable, default 10,000 tokens), the delegation is rejected.

3. **Return on completion:** When a child session reaches a terminal state (completed, failed, cancelled, expired), the gateway credits unused budget back to the parent's available pool via atomic Redis `INCRBY`. Unused budget = child's allocated budget minus child's actual consumption (including all descendants). The parent's `maxTokenBudget` ceiling is never exceeded — returns only restore up to the original allocation.

   **Usage quiescence before budget return.** The `ReportUsage` gRPC call from the pod may arrive at the gateway after the session's terminal event is written, creating a race where `budget_return.lua` executes before the final usage counter is updated. To close this window, the gateway applies the following quiescence step before executing `budget_return.lua`:

   1. On receiving a terminal lifecycle event from the pod (or on detecting pod termination), the gateway waits for a `FINAL_USAGE_REPORT` acknowledgement from the child's adapter, or for the gRPC stream to close — whichever comes first. The adapter sends `FINAL_USAGE_REPORT` as the last message on the lifecycle stream before closing, after all in-flight `ReportUsage` calls have been flushed.
   2. If neither occurs within the **usage quiescence timeout** (default: 5s, configurable via `delegation.usageQuiescenceTimeoutSeconds`), the gateway proceeds with the last known usage counter and emits a `delegation.budget_return_usage_lag` warning event (labeled by `session_id`, `delta_tokens_unknown: true`) to indicate that the return may slightly under-deduct usage.
   3. `budget_return.lua` executes only after step 1 or 2 completes.

   This ensures budget returns are accurate in the normal case and bounded in the degraded case. The `delegation.budget_return_usage_lag` warning is rate-limited to one per session and can be monitored via `lenny_delegation_budget_return_usage_lag_total` (counter).

4. **Concurrency safety:** All budget operations (reserve, consume, return) use Redis atomic operations. The reservation step (token budget + tree size + children counters + tree memory) uses a single Redis Lua script (`budget_reserve.lua`) that atomically: (a) reads the current token budget counter, the parent's actual token usage counter, the tree-size counter, the `childrenTotal` counter, the `parallelChildren` counter, and the tree-memory counter, (b) computes the effective remaining budget as `parentBudget - parentUsage` (the budget counter tracks reserved/granted capacity while the usage counter tracks tokens actually consumed by the parent session itself — both must be read together to avoid a race where a parent has nearly exhausted its own token usage but not yet reduced its delegation counter), (c) caps the requested child slice to `min(requested_slice, parentBudget - parentUsage)`, (d) checks the capped slice against the effective remaining budget, the tree-size result against `maxTreeSize`, `childrenTotal` against `maxChildrenTotal`, `parallelChildren` against `maxParallelChildren`, and `currentTreeMemory + nodeMemoryEstimate` against `maxTreeMemoryBytes`, (e) applies `DECRBY` (token budget, using the capped slice), `INCR` (tree size), `INCR` (`childrenTotal`), `INCR` (`parallelChildren`), and `INCRBY` (tree memory, using the per-node memory estimate) only if all checks pass, and (f) returns a structured result `{status, granted_slice, remaining_tokens, current_tree_size, current_children_total, current_parallel_children, current_tree_memory_bytes}`. If `parentBudget - parentUsage` is zero or below the minimum usable threshold, the script returns `TOKEN_BUDGET_EXHAUSTED` without modifying any counter. Because Redis executes Lua scripts atomically, there is no TOCTOU window between the six counters — concurrent delegation requests never observe a partially applied reservation and a child can never be granted a slice that exceeds the parent's actual remaining budget. The return-on-completion path (step 3) uses an analogous Lua script (`budget_return.lua`) that atomically credits unused tokens via `INCRBY`, decrements tree size via `DECR`, decrements `parallelChildren` via `DECR`, and decrements tree memory via `DECRBY` (subtracting the node's memory footprint), ensuring the reverse operation is equally atomic.

   **`parallelChildren` counter and recovering children.** Children in `resume_pending` or `resuming` states continue to count toward the parent's `parallelChildren` counter. The `parallelChildren` counter is decremented only when a child reaches a terminal state (via `budget_return.lua`), not when it enters a recovery state. During tree recovery ([Section 8.10](#810-delegation-tree-recovery)), this means the effective parallel capacity is temporarily reduced: recovering children occupy `parallelChildren` slots even though they may eventually fail and free those slots. If the parent also recovers and attempts to spawn replacement children before the recovering children settle, the `maxParallelChildren` limit may block new delegations. This is intentional: it prevents the parent from spawning unbounded replacement children while recovering children may still succeed and resume. Deployers running workloads where correlated failures (e.g., shared-node evictions) can affect multiple children simultaneously should set `maxParallelChildren` with headroom above the expected steady-state parallelism — a margin of 2-3 additional slots is recommended to absorb recovery transients without blocking replacement delegations.

5. **Over-run semantics.** The granted slice is a **soft cap enforced at settlement**, not a per-call hard ceiling at the LLM provider. In `deliveryMode: proxy`, the LLM reverse proxy tracks per-session token consumption in real time and rejects LLM calls once the session's `maxTokenBudget` (leaf budget) is exhausted — this bounds over-run in proxy mode. In `deliveryMode: direct`, the pod holds the API key directly and the gateway has no per-call visibility; over-run up to the next `ReportUsage` interval is possible. Regardless of delivery mode, at settlement time (step 3) `budget_return.lua` uses the **actual consumption** counter (not the granted slice) when computing unused budget returned to the parent — if actual consumption exceeds the granted slice, the return is zero (not negative). The parent's counter cannot go below zero. Deployers operating at high token volumes in direct delivery mode should set conservative `perChildMaxAge` and `maxTokenBudget` values on delegation leases to bound worst-case over-run exposure.

**Credential propagation:** Controls how child sessions get LLM provider credentials:

| Mode          | Behavior                                                                                                      |
| ------------- | ------------------------------------------------------------------------------------------------------------- |
| `inherit`     | Child uses the same credential pool/source as parent (gateway assigns from same pool)                         |
| `independent` | Child gets its own credential lease based on the tenant's credential policy and the child Runtime's `supportedProviders` |
| `deny`        | Child receives no LLM credentials (for runtimes that don't need LLM access, e.g., pure file-processing tools) |

**`credentialPropagation: inherit` multi-hop semantics:** The `inherit` mode applies **per-hop**. Each node in the delegation tree specifies its own `credentialPropagation` on its outgoing delegation lease; that value governs only the single hop from that node to its direct child. It does not recursively override the `credentialPropagation` settings on deeper hops. Concretely: if a root session (depth 0) delegates with `credentialPropagation: inherit`, the depth-1 child is assigned from the root's credential pool. Whether the depth-1 child's own children (depth 2) use `inherit`, `independent`, or `deny` is governed by the delegation lease that the depth-1 child specifies when it calls `lenny/delegate_task` — not by the root's setting.

**Worked example — 3-level tree with mixed modes:**

```
Root (depth 0)
  credentialPropagation: inherit     → depth-1 child uses root's credential pool
  delegates to →
    Child A (depth 1)
      credentialPropagation: independent  → depth-2 child gets its own credential
      delegates to →
        Child B (depth 2)
          credentialPropagation: deny     → depth-3 child (if any) would get no credential
```

In this tree:
- Root uses its own credential (assigned at session creation).
- Child A is assigned a credential from the **same pool** as Root (inherit from Root → Child A hop).
- Child B is assigned a **new credential** from the tenant's credential policy (independent at Child A → Child B hop).
- If Child B were to delegate further with `deny`, its children would receive no LLM credentials.

This per-hop model gives each orchestrating node full control over how its direct children are credentialed, without requiring a global tree-wide policy.

**Credential availability pre-check at delegation time:** When the gateway processes a `delegate_task` call with `credentialPropagation: inherit` or `independent`, it performs the same pre-claim credential availability check described in [Section 4.9](04_system-components.md#49-credential-leasing-service) before claiming a warm pod. For `inherit` mode, the gateway verifies that the parent's credential pool has at least one assignable slot (`active leases < maxConcurrentSessions` for at least one credential in the pool). For `independent` mode, the gateway checks the intersection of the child runtime's `supportedProviders` and the tenant's `credentialPolicy.providerPools`. If no credential is available, the delegation is rejected with `CREDENTIAL_POOL_EXHAUSTED` before pod allocation — no warm pod is wasted. This is a point-in-time check, not a reservation: concurrent `delegate_task` calls can each pass the check individually while collectively exhausting the pool. If the actual credential assignment fails after pod claim (due to this race), the gateway releases the pod back to the warm pool and returns `CREDENTIAL_POOL_EXHAUSTED`, consistent with the session-creation behavior in [Section 7.1](07_session-lifecycle.md#71-normal-flow).

**`credentialPropagation: inherit` — cross-environment compatibility check:** When a delegation crosses an environment boundary (cross-environment delegation, [Section 10.6](10_gateway-internals.md#106-environment-resource-and-rbac-model)), the child runtime in the target environment may declare a different `supportedProviders` list than the parent's runtime. Before approving a cross-environment `delegate_task` call with `credentialPropagation: inherit`, the gateway performs a **provider compatibility check**: it computes the intersection of (a) the providers represented in the parent's credential pool and (b) the child runtime's `supportedProviders`. If the intersection is non-empty, the delegation proceeds — the gateway assigns a credential from the parent's pool whose provider appears in that intersection. If the intersection is empty (the child runtime cannot use any provider in the parent's pool), the delegation is **rejected** with error code `CREDENTIAL_PROVIDER_MISMATCH` before pod allocation. The rejection message is: `"credentialPropagation: inherit is incompatible with cross-environment delegation: parent credential pool providers do not intersect with child runtime supportedProviders"`. There is no automatic fallback to `independent` mode — the delegation is rejected deterministically, requiring the delegating session to explicitly use `credentialPropagation: independent` if cross-environment credential assignment is intended. The compatibility check is performed as part of the pre-claim availability check step and does not claim any warm pod before rejecting.

> **Deployer guidance — `inherit` mode and fan-out:** The `inherit` credential propagation mode is not suitable for high fan-out delegation trees. Because all descendants sharing a pool via contiguous `inherit` hops draw from the same credential pool, a tree with `maxParallelChildren` exceeding the pool's `maxConcurrentSessions` (divided by expected tree depth) will experience credential exhaustion failures. Use `credentialPropagation: independent` when `maxParallelChildren > pool.maxConcurrentSessions / expected_tree_depth`. The `orchestrator` preset uses `independent` by default for this reason.

**Delegation Presets:** To reduce configuration burden, deployers can define named delegation presets:

```yaml
delegationPresets:
  simple: # Single-level delegation, no fan-out
    maxDepth: 1
    maxChildrenTotal: 3
    maxParallelChildren: 1
    maxTokenBudget: 100000
  standard: # Multi-level, moderate fan-out
    maxDepth: 3
    maxChildrenTotal: 10
    maxParallelChildren: 3
    maxTokenBudget: 500000
  orchestrator: # Deep trees, high fan-out
    maxDepth: 5
    maxChildrenTotal: 50
    maxParallelChildren: 10
    maxTokenBudget: 2000000
```

Clients reference presets by name in the WorkspacePlan: `"delegationLease": "standard"`. Presets can be partially overridden with inline fields: `"delegationLease": {"preset": "standard", "maxDepth": 2}`. If no delegation lease is specified, the Runtime's default applies. At Tier 3 with 10,000 sessions using the `orchestrator` preset, aggregate child pod demand can reach hundreds of thousands — deployers should size warm pools accordingly ([Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)).

### 8.4 Approval Modes

| Mode       | Behavior                                                                  |
| ---------- | ------------------------------------------------------------------------- |
| `policy`   | Gateway auto-approves if request matches lease constraints                |
| `approval` | **Reserved — not implemented in v1.** The `approval` value is accepted at policy registration time but the gateway treats it identically to `policy` mode in v1. Full implementation (dedicated approval API, approval timeout, concurrent-request handling, denial error code) is deferred to post-v1. |
| `deny`     | Delegation not permitted                                                  |

### 8.5 Delegation Tools

Available on the platform MCP server for every delegation-capable pod:

| Tool                                              | Purpose                                                                                                                                                                                                                                                                                                                                    |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `lenny/delegate_task(target, task, lease_slice?)` | Spawn a child session (target is opaque — runtime, derived runtime, or external agent)                                                                                                                                                                                                                                                     |
| `lenny/await_children(child_ids, mode)`           | Wait for multiple children (`all`, `any`, or `settled`). Streaming response — unblocks on `input_required`.                                                                                                                                                                                                                                |
| `lenny/cancel_child(child_id)`                    | Cancel a child (cascades to its descendants per policy)                                                                                                                                                                                                                                                                                    |
| `lenny/discover_agents(filter?)`                  | List available delegation targets, filtered by effective delegation policy                                                                                                                                                                                                                                                                 |
| `lenny/output`                                    | Emit output parts to the parent/client                                                                                                                                                                                                                                                                                                     |
| `lenny/request_elicitation`                       | Request human input via the elicitation chain (see [Section 9.2](09_mcp-integration.md#92-elicitation-chain))                                                                                                                                                                                                                                                                            |
| `lenny/memory_write`                              | Write to the memory store (see [Section 9.4](09_mcp-integration.md#94-memory-store))                                                                                                                                                                                                                                                                                                |
| `lenny/memory_query`                              | Query the memory store (see [Section 9.4](09_mcp-integration.md#94-memory-store))                                                                                                                                                                                                                                                                                                   |
| `lenny/send_message(to, message)`                 | Send a message to any task by taskId. Returns `deliveryReceipt` ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)). Returns error for terminal targets; queues with TTL for recovering targets.                                                                                                                                                                                 |
| `lenny/set_tracing_context(context)`               | Register tracing identifiers for propagation through delegation. `context` is a `map<string, string>` of non-sensitive identifiers (e.g., `{"langsmith_run_id": "run_abc123"}`). The adapter stores the context and attaches it to all subsequent delegation gRPC requests. Gateway validates on delegation ([Section 8.3](#83-delegation-policy-and-lease)). Also available via stdout JSONL `set_tracing_context` message for runtimes that prefer the simpler path. See [Section 16.3](16_observability.md#163-distributed-tracing). |
| `lenny/request_input(parts)`                      | Block until answer arrives (replaces stdout `input_required`)                                                                                                                                                                                                                                                                              |
| `lenny/get_task_tree()`                           | Return task hierarchy with states. Each node includes `taskId`, `state`, and `runtimeRef`. Visibility is controlled by the `treeVisibility` field on the delegation lease: `full` (default — child sees the entire subtree rooted at the tree root, including siblings and their descendants), `parent-and-self` (child sees only its own node and its direct parent's node), `self-only` (child sees only its own node). The parent controls sibling visibility when issuing the delegation lease. In multi-runtime deployments where child sessions are delegated to runtimes of varying trust levels, `parent-and-self` or `self-only` prevents a low-trust child from observing sibling runtime types, state transitions, or the overall orchestration strategy. When `treeVisibility` is `full`, a child session can discover its siblings by inspecting the tree; combined with `lenny/send_message` under `siblings` messaging scope ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)), this enables sibling coordination without additional tools. `siblings` messaging scope requires `treeVisibility: full`; the gateway rejects `messagingScope: siblings` when `treeVisibility` is `self-only` or `parent-and-self` at delegation time with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`. |

**Platform MCP tool input schemas** (tools not covered by the delegation tool schemas above):

`lenny/output`:
```json
{
  "type": "object",
  "properties": {
    "output": {
      "type": "array",
      "items": { "$ref": "#/definitions/OutputPart" },
      "description": "Output parts to emit to the parent/client. Same OutputPart schema as the stdout response.output array."
    }
  },
  "required": ["output"]
}
```

`lenny/request_elicitation`:
```json
{
  "type": "object",
  "properties": {
    "schema": {
      "type": "object",
      "description": "JSON Schema describing the input to collect from the user."
    },
    "message": {
      "type": "string",
      "description": "Human-readable prompt displayed to the user."
    }
  },
  "required": ["schema", "message"]
}
```

`lenny/memory_write`:
```json
{
  "type": "object",
  "properties": {
    "content": {
      "type": "string",
      "description": "The memory content to store."
    },
    "metadata": {
      "type": "object",
      "description": "Optional key-value metadata attached to the memory record.",
      "additionalProperties": { "type": "string" }
    }
  },
  "required": ["content"]
}
```

`lenny/memory_query`:
```json
{
  "type": "object",
  "properties": {
    "query": {
      "type": "string",
      "description": "Natural-language query for semantic search over the memory store."
    },
    "limit": {
      "type": "integer",
      "description": "Maximum number of results to return. Default: 10.",
      "default": 10
    }
  },
  "required": ["query"]
}
```

`lenny/get_task_tree`:
```json
{
  "type": "object",
  "properties": {},
  "required": []
}
```
No input parameters. Returns the task hierarchy visible to the calling session (scoped by `treeVisibility`).

### 8.6 Lease Extension

Lease extension is part of the **adapter↔gateway gRPC lifecycle**, not the platform MCP server. The runtime never calls it and is never aware it happened.

**Trigger:** When the LLM proxy rejects a call for budget exhaustion, the adapter automatically requests a lease extension from the gateway via the gRPC control channel. This avoids the chicken-and-egg problem where a runtime would need LLM tokens to reason about requesting more tokens. On `GRANTED` or `PARTIALLY_GRANTED`, the adapter retries the failed LLM call transparently — the runtime sees a slightly slow LLM response, not a failure. On `CEILING_REACHED` (zero grant), the adapter MUST NOT retry the extension; it propagates `BUDGET_EXHAUSTED` to the runtime as a terminal error. On `REJECTED` (user denied), the adapter propagates `BUDGET_EXHAUSTED` to the runtime.

**Request:**

```json
{
  "extensions": {
    "additionalChildren": 5,
    "additionalTokenBudget": 200000,
    "additionalMaxAge": 1800
  }
}
```

**Extendable fields:** `maxChildrenTotal`, `maxParallelChildren`, `maxTokenBudget`, `maxTreeSize`, `perChildMaxAge`, `fileExportLimits`. Not extendable: `maxDepth`, `minIsolationProfile`, `delegationPolicyRef`, `perChildRetryBudget` (these are security or reliability boundaries, not resource budgets).

**Hard ceilings — extensions can never exceed:**

1. **Effective max** — `min(deployment max, tenant max if set)`. See configuration layering below.
2. **The parent's own lease limits** — a child requesting an extension cannot exceed what the parent was granted

#### Configuration Layering

Lease extension settings are resolved by **specificity**: more specific levels override less specific, in either direction (more permissive or more restrictive).

**Resolution order for `extensionApproval` and `coolOffSeconds`:**

1. Start with **deployment default** (Helm)
2. **Tenant** overrides if set (via admin API)
3. **Runtime** overrides if set (via admin API)

**Resolution order for `maxExtendableBudget`:**

1. Start with **deployment default** (Helm)
2. **Tenant** overrides if set
3. **Runtime** overrides if set
4. Result is capped by **tenant max** if one exists
5. Result is capped by **deployment max** (absolute ceiling — can never be exceeded, even if tenant max is higher)

**Configuration at each level:**

```yaml
# Deployment level (Helm) — system-wide defaults and absolute ceiling
leaseExtension:
  defaults:
    extensionApproval: elicitation
    coolOffSeconds: 5
    maxExtendableBudget: 500000
  max:
    maxExtendableBudget: 2000000 # absolute ceiling, never exceeded
```

```yaml
# Tenant level (admin API) — per-tenant overrides and optional ceiling
leaseExtension:
  extensionApproval: auto # overrides deployment default
  maxExtendableBudget: 1000000 # overrides deployment default
  max:
    maxExtendableBudget: 1500000 # tenant ceiling, capped by deployment max
```

```yaml
# Runtime level (admin API) — per-runtime overrides
leaseExtension:
  extensionApproval: elicitation # overrides tenant setting
  coolOffSeconds: 10 # overrides deployment default
  maxExtendableBudget: 800000 # overrides tenant setting
```

**Example resolutions with deployment default 500K, deployment max 2M, tenant max 1M:**

| Runtime sets | Tenant sets | Effective `maxExtendableBudget` | Why                                                        |
| ------------ | ----------- | ------------------------------- | ---------------------------------------------------------- |
| 800K         | 300K        | 800K                            | Runtime overrides tenant; under both ceilings              |
| 1.5M         | —           | 1M                              | Runtime overrides deployment default; capped by tenant max |
| —            | 300K        | 300K                            | Tenant overrides deployment default                        |
| —            | —           | 500K                            | Deployment default                                         |
| 2.5M         | —           | 1M                              | Capped by tenant max (which is < deployment max)           |

> ⁴ The "Tenant sets" column shows the tenant's **base value**, not a ceiling. The runtime can override the tenant base value up to the tenant's ceiling (`leaseExtension.max.maxExtendableBudget`). To hard-cap the budget for all runtimes in a tenant, set `leaseExtension.max.maxExtendableBudget` on the tenant config — this is the absolute ceiling that no runtime can exceed regardless of its own `maxExtendableBudget` setting.

#### Lease Extension Approval Modes

**`auto` mode:** Each request is handled independently. The gateway grants the requested amount up to the effective max. No elicitation, no queuing, no cool-off. The response includes the granted amount and status: `GRANTED` (full amount), `PARTIALLY_GRANTED` (capped to remaining headroom), or `CEILING_REACHED` (zero grant — ceiling already reached). The adapter MUST NOT retry after `CEILING_REACHED`. **Auto-mode rate limit:** To prevent a runaway agent from exhausting the entire `maxExtendableBudget` in seconds without human visibility, `auto` mode enforces an optional `autoModeRateLimit` on the lease extension configuration (configurable per deployment/tenant/runtime using the same layering as other lease extension fields). The rate limit specifies `maxAutoExtensionsPerMinute` (default: no limit — backward compatible). When set, the gateway tracks the count of auto-approved extensions per task tree per sliding minute window. When the rate limit is exceeded, the gateway pauses auto-approval and falls back to `elicitation` mode for the remainder of the window — this provides a safety valve against rapid consumption without requiring the full elicitation UX overhead for normal operation. The fallback is logged as `lease_extension_auto_rate_limit_exceeded` in the audit log.

**`elicitation` mode (default):** Requests are serialized per task tree. The gateway presents at most one elicitation to the user at a time, with concurrent request batching and a cool-off window after approval.

**Elicitation mode flow:**

1. **First request** in a tree triggers a generic elicitation to the client: _"The agent needs more budget to continue. Approve?"_ No specific token amounts are shown.
2. **Concurrent requests** arriving while the elicitation is pending are queued silently. No duplicate elicitation is sent.
3. **User approves:**
   - The gateway grants each queued request its individually requested amount, adding those tokens to the tree budget (capped at the effective max).
   - A **cool-off window** starts.
   - New requests arriving during the cool-off period are auto-granted their requested amounts with no elicitation.
   - If granting a request's full amount would exceed the effective max, the gateway caps the grant to whatever headroom remains.
   - All requests tied to a single elicitation + cool-off period are processed as part of the approved batch. Each extension response includes the **granted amount** (`grantedTokenBudget`, `grantedChildren`, etc.) so the adapter knows exactly what was received. If a request's grant was capped, the response status is `PARTIALLY_GRANTED` with the actual amounts. If the grant is **zero** because the ceiling was already reached, the response status is `CEILING_REACHED` (not success) — the adapter MUST treat this as a terminal condition and propagate `BUDGET_EXHAUSTED` to the runtime rather than retrying the extension. The adapter MUST NOT retry extension requests after receiving `CEILING_REACHED`; doing so would create infinite retry loops (each request returns zero grant, each LLM call fails, adapter requests again). Extension response statuses: `GRANTED` (full amount), `PARTIALLY_GRANTED` (capped but non-zero), `CEILING_REACHED` (zero grant — ceiling hit), `REJECTED` (user denied).
   - After the cool-off window expires, the next request starts a new elicitation cycle (back to step 1).
4. **User rejects:**
   - All queued requests are rejected.
   - The **requesting subtree** (the session whose adapter issued the `ExtendLease` gRPC call that triggered the elicitation, and that session's descendants) is marked as **extension-denied**. Other subtrees in the same task tree are unaffected and may still request extensions independently.
   - **Durability:** The `extension-denied` flag and the rejection cool-off expiry timestamp are persisted to the `delegation_tree_budget` Postgres table (keyed by `root_session_id`) as part of the same periodic checkpoint transaction that records token and delegation budget counters (see [Section 11.1](11_policy-and-controls.md#111-admission-and-fairness)). On coordinator handoff, the new gateway replica reads these fields from Postgres before accepting any new extension requests for the tree. This ensures that a user rejection cannot be bypassed by a gateway restart or rolling update.
   - A **rejection cool-off period** begins (duration: `rejectionCoolOffSeconds`, configurable per deployment/tenant/runtime using the same layering as `coolOffSeconds`, default `300`). During the cool-off period, new extension requests from the denied subtree are auto-rejected without elicitation. After the cool-off expires, the subtree may request extensions again, which triggers a new elicitation cycle (back to step 1).
   - Operators can clear the extension-denied flag immediately via the **admin API** (`DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial`). This resets the subtree to normal extension behavior regardless of cool-off state. See [Section 15.1](15_external-api-surface.md#151-rest-api).

**Scope:**

- Extensions apply to the requesting session only
- Existing children are **unaffected** — their leases remain as originally granted
- Only new children spawned after the extension benefit from the expanded parent budget

**Audit:** Every extension request is logged with: requesting session, requested amounts, approval mode, outcome (approved/denied/capped), approver (gateway-auto or client), granted amount, effective max at time of request, resulting new limits, batch id (groups requests tied to the same elicitation + cool-off period), service_instance_id (the OTel attribute identifying the gateway replica — see [§16.1.1](16_observability.md#161-metrics)), client_ip.

### 8.7 File Export Model

When a parent delegates to a child, it specifies which files to export and how they should appear in the child's workspace.

**Export spec (part of `delegate_task`):**

```json
{
  "fileExport": {
    "source": "./exports/export1/*",
    "destPrefix": "input/"
  }
}
```

**Rebasing rule:** The source glob's base path is stripped, and matched files are placed at the child's workspace root (or under `destPrefix` if specified). The child always sees a clean root-relative structure.

**Examples:**

| Parent workspace               | Source glob           | destPrefix     | Child sees              |
| ------------------------------ | --------------------- | -------------- | ----------------------- |
| `./exports/export1/foo.ts`     | `./exports/export1/*` | _(none)_       | `./foo.ts`              |
| `./exports/export1/lib/bar.ts` | `./exports/export1/*` | _(none)_       | `./lib/bar.ts`          |
| `./exports/export1/foo.ts`     | `./exports/export1/*` | `input/`       | `./input/foo.ts`        |
| `./src/auth.ts`                | `./src/*`             | `project/src/` | `./project/src/auth.ts` |
| `./results.json`               | `./results.json`      | _(none)_       | `./results.json`        |

This means the parent controls what slice of its workspace becomes the child's world. The child has no visibility into the parent's broader directory structure.

**Multiple exports:** A `delegate_task` can include multiple export entries. They are applied in order; if paths overlap, later entries overwrite earlier ones.

```json
{
  "fileExport": [
    { "source": "./src/*", "destPrefix": "src/" },
    { "source": "./config/child-config.json", "destPrefix": "" }
  ]
}
```

**Validation:**

- Source glob resolution must not follow symlinks outside `/workspace/current`. The gateway resolves each matched path to its real path (`realpath`) and rejects any file whose resolved path is outside the workspace root. This prevents an agent from creating a symlink (e.g., `./data → /etc/passwd`) that would be included in the export.
- Source globs are resolved inside the parent's `/workspace/current` only — no traversal outside the workspace
- `destPrefix` must be a relative path, no `..`, no absolute paths
- Total exported size is checked against `fileExportLimits` in the delegation lease
- File count is checked against `fileExportLimits.maxFiles`
- If multiple exports or `destPrefix` settings cause file overwrites in the child workspace, the gateway logs a warning with the overwritten paths and the export entry that caused it. This is audited in the session's delegation audit trail.

**Security note — exported files are untrusted input:** The gateway validates export structure (path bounds, size, file count) but does not inspect file content. A compromised or manipulated parent agent can include files with adversarial content — including files that agent runtimes treat as instruction sources (e.g., `CLAUDE.md`, `.claude/settings.json`). The `contentPolicy.interceptorRef` on `DelegationPolicy` covers `TaskSpec.input` only; it does not apply to exported workspace files. Child agent runtimes and deployers must treat all workspace files received via delegation as untrusted input and apply appropriate content handling (e.g., configure the runtime to ignore or sandbox instruction files injected from external sources). Deployers requiring content inspection of exported files should front the child session creation with an interceptor that inspects the workspace plan's `inlineFile` entries before they are written to the child's workspace.

### 8.8 TaskRecord and TaskResult Schema

#### TaskRecord

Task records use a messages array forward-compatible with multi-turn dialog:

```json
{
  "schemaVersion": 1,
  "taskId": "task_abc123",
  "sessionId": "sess_xyz",
  "state": "running",
  "messages": [
    { "role": "caller", "parts": ["OutputPart[]"] },
    { "role": "agent",  "parts": ["OutputPart[]"], "state": "completed" }
  ],
  "usage": { ... },
  "treeUsage": { ... }
}
```

**Schema versioning in `TaskRecord`.** The top-level `schemaVersion` governs the outer record structure — the set and semantics of fields at the `TaskRecord` level (e.g., `taskId`, `sessionId`, `state`, `usage`, `treeUsage`, and the message-entry envelope fields `role`, `parts`, `state`). It does **not** govern the schema of the `OutputPart` objects nested inside each message's `parts` array: each `OutputPart` carries its own `schemaVersion` field (see [Section 15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol)), which independently tracks that type's evolution. This two-level versioning model means a `TaskRecord` written across a rolling gateway upgrade — where different gateway replicas may write messages at different `OutputPart` schema versions — is fully handled: the top-level `schemaVersion` is immutable once the record is created (set by the first writer, per [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7), and per-entry `OutputPart.schemaVersion` captures any intra-record variation in part schema. Consumers applying the durable-consumer forward-read rule ([Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7) MUST apply it independently at both levels: once for the record envelope and once per `OutputPart` entry.

**Envelope schema version upgrade constraint.** Because the top-level `schemaVersion` is immutable once created, a `TaskRecord` envelope schema version bump (e.g., from 1 to 2) cannot retroactively update in-flight records. During a rolling gateway upgrade where replica B knows about envelope schema 2 but the record was created at schema 1 by replica A, replica B MUST NOT write new envelope-level fields that are schema-2-only into a schema-1 record. The operational implication: `TaskRecord` envelope schema changes MUST be additive-only (new fields with defaults that schema-1 readers can safely ignore). If a breaking envelope schema change is ever required (a field removed or semantics changed), all active (non-terminal) task records at the old envelope version must drain to terminal state before the new schema version is deployed. In practice, envelope schema bumps should be exceedingly rare -- new envelope fields should be introduced as additive extensions within the current schema version whenever possible, reserving a version bump for structural changes that alter the interpretation of existing fields.

**Lenny canonical task state machine:**

Lenny defines its own task states independent of any external protocol. External protocol adapters map to/from these states at the boundary.

```
submitted → running → completed        (terminal)
                    → failed            (terminal)
                    → cancelled         (terminal — via lenny/cancel_child or cascade policy)
                    → expired           (terminal — lease/budget/deadline exhausted)
                    → input_required    (reachable via lenny/request_input)

input_required → running               (input provided via lenny/send_message with inReplyTo)
input_required → running               (request timeout — maxRequestInputWaitSeconds fires, gateway delivers REQUEST_INPUT_TIMEOUT tool-call error; see §11.3)
input_required → running               (request cancelled by parent via lenny/cancel_child or equivalent)
input_required → cancelled             (parent cancels while awaiting input)
input_required → expired               (deadline reached while awaiting input)
input_required → resume_pending        (pod crash / gRPC error while awaiting input, retryCount < maxRetries)
input_required → failed                (pod crash / gRPC error while awaiting input, retries exhausted)
input_required → failed                (BUDGET_KEYS_EXPIRED detected while awaiting input — see §8.3)
```

`input_required` is a sub-state of `running` where the pod is live; all failure transitions defined for `running` therefore also apply to `input_required`.

Terminal states: `completed`, `failed`, `cancelled`, `expired`.

**Protocol mapping:**

| Lenny state      | MCP Tasks             | A2A (future)              |
| ---------------- | --------------------- | ------------------------- |
| `submitted`      | `submitted`           | `submitted`               |
| `running`        | `working`             | `working`                 |
| `completed`      | `completed`           | `completed`               |
| `failed`         | `failed`              | `failed`                  |
| `cancelled`      | `canceled` (MCP)      | `canceled` (A2A)          |
| `expired`        | `failed` + error code | `failed` + error metadata |
| `input_required` | `input_required`      | `input-required`          |

Notes: A2A's `unknown` state maps to a gateway-level error (task ID not found or not visible), not to a Lenny task state. MCP uses American spelling `canceled`; Lenny uses `cancelled` internally — adapters handle the spelling difference. `expired` has no direct equivalent in MCP or A2A; adapters surface it as `failed` with a structured error code indicating the expiry reason (e.g., `expired:budget`, `expired:deadline`, `expired:lease`). The `canceled` alternative mentioned in earlier drafts is removed — `expired` always maps to `failed` for consistency; the error code's `expired:*` prefix unambiguously distinguishes expiry from other failure causes.

**`one_shot` input-round constraint.** When a `one_shot` runtime enters `input_required` (permitted once per [Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)), the adapter annotates the protocol-visible `input_required` event with `metadata.maxInputRounds: 1`. MCP clients receive this in the task status metadata; A2A clients receive it in the `input-required` state's metadata map. Clients that ignore the annotation and attempt a second input round receive a `400` error with code `ONE_SHOT_INPUT_EXHAUSTED`. This annotation is informational — the gateway enforces the constraint regardless of whether the client reads it.

**Session-level state mapping (supplementary).** The table above covers task-level states exposed via the `TaskRecord` schema. Lenny sessions have additional states ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) that are visible via the session API (`GET /v1/sessions/{id}`) but do not map 1:1 to MCP Tasks or A2A task states. The following table defines how session-level states are surfaced to external protocol clients:

| Lenny session state        | MCP Tasks surface              | A2A surface (future)           | Notes                                                                                          |
| -------------------------- | ------------------------------ | ------------------------------ | ---------------------------------------------------------------------------------------------- |
| `created`                  | `submitted`                    | `submitted`                    | Session exists but pod not yet assigned.                                                       |
| `ready`                    | `submitted`                    | `submitted`                    | Pod assigned, workspace not yet materialized.                                                  |
| `starting`                 | `submitted`                    | `submitted`                    | Workspace materializing or setup commands running.                                             |
| `finalizing`               | `submitted`                    | `submitted`                    | Workspace finalization in progress.                                                            |
| `suspended`                | `working` + `metadata.suspended: true` | `working` + `metadata.suspended: true` | Session paused via interrupt; pod still allocated. Adapter injects `suspended` in metadata.    |
| `resume_pending`           | `working` + `metadata.resuming: true`  | `working` + `metadata.resuming: true`  | Awaiting new pod for recovery. Adapter injects `resuming` in metadata.                         |
| `awaiting_client_action`   | `input_required`               | `input-required`               | Recovery requires client intervention (e.g., resubmit). Maps naturally to input-required.      |

Session-level states that are sub-states of `running` (`input_required`) or terminal (`completed`, `failed`, `cancelled`, `expired`) use the task-level mapping table above. Pre-running states (`created`, `ready`, `starting`, `finalizing`) are collapsed to `submitted` because external protocols have no equivalent granularity for pre-execution phases. `suspended` and `resume_pending` are surfaced as `working` with metadata annotations because the session is non-terminal and may resume autonomously — returning `input_required` would incorrectly imply that client input is needed for `resume_pending` (the gateway handles recovery automatically).

#### TaskResult

Returned by `lenny/await_children`:

```json
{
  "schemaVersion": 1,
  "taskId": "child_abc123",
  "state": "completed",
  "output": {
    "parts": ["OutputPart[]"],
    "artifactRefs": ["lenny-blob://tenant_acme/session_xyz/part_ws001?ttl=2592000&enc=aes256gcm"]
  },
  "usage": {
    "inputTokens": 15000,
    "outputTokens": 8000,
    "wallClockSeconds": 120,
    "podMinutes": 2.1,
    "credentialLeaseMinutes": 1.8
  },
  "treeUsage": {
    "inputTokens": 45000,
    "outputTokens": 22000,
    "wallClockSeconds": 450,
    "podMinutes": 12.5,
    "credentialLeaseMinutes": 10.2,
    "totalTasks": 4
  },
  "error": null
}
```

`treeUsage` is populated by the gateway from the task tree and is only available after all descendants have settled. It contains the sum of this task's usage plus all descendant tasks. For in-progress tasks or tasks with unsettled descendants, `treeUsage` will be `null`.

On failure:

```json
{
  "schemaVersion": 1,
  "taskId": "child_abc123",
  "state": "failed",
  "output": null,
  "usage": {
    "inputTokens": 5000,
    "outputTokens": 1000,
    "wallClockSeconds": 30,
    "podMinutes": 0.5,
    "credentialLeaseMinutes": 0.0
  },
  "error": {
    "code": "RUNTIME_CRASH",
    "category": "TRANSIENT",
    "message": "Agent process exited with code 137",
    "retriesExhausted": true
  }
}
```

`TaskResult.schemaVersion` follows the same producer/consumer obligations as `TaskRecord.schemaVersion` ([Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) item 7). The gateway sets `schemaVersion` when constructing the `TaskResult`; consumers must apply the durable-consumer forward-read rule independently for `TaskResult` and for any nested `OutputPart` entries.

**`lenny/await_children` modes and behavior:**

- `all` — wait until all children reach a terminal state (completed, failed, cancelled, or expired). Returns list of `TaskResult`.
- `any` — return as soon as any child reaches a terminal state. Returns the first `TaskResult`. **Remaining children continue running** — they are not auto-cancelled. The parent can explicitly cancel them via `lenny/cancel_child` if desired.
- `settled` — equivalent to `all`; wait until all children reach a terminal state (completed, failed, cancelled, or expired). Returns list of `TaskResult`.

**`lenny/await_children` unblocks on `input_required`:** When a child enters `input_required` state, the parent's `lenny/await_children` call yields a partial result carrying the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response — it yields partial events before the final settled result. The parent can respond via `lenny/send_message` with `inReplyTo: "req_001"`, which resolves the child's blocked `lenny/request_input` tool call directly. The parent then re-awaits.

**Re-await semantics (multiple `input_required` children):** A single `lenny/await_children` stream can yield multiple `input_required` partial results — one per child that blocks. The parent handles each partial result independently:

1. The stream yields an `input_required` event for child A. The parent responds via `lenny/send_message` with `inReplyTo` targeting child A's `requestId`.
2. While the parent is processing child A's question, child B may also enter `input_required`. The stream yields a second partial result for child B.
3. The parent responds to child B in the same manner. Both children resume independently once their respective `request_input` calls are resolved.
4. The parent does **not** need to close and re-open the `await_children` call between partial results — the stream remains open until the final settled/completed result.

**Multi-child `input_required` handling pattern:**

```
parent calls lenny/await_children(["child_A", "child_B"], mode="all")
  ← stream yields: { childId: "child_A", state: "input_required", requestId: "req_001", parts: [...] }
  parent calls lenny/send_message(target: "child_A", inReplyTo: "req_001", parts: [...])
  ← stream yields: { childId: "child_B", state: "input_required", requestId: "req_002", parts: [...] }
  parent calls lenny/send_message(target: "child_B", inReplyTo: "req_002", parts: [...])
  ← stream yields: { childId: "child_A", state: "completed", output: {...} }
  ← stream yields: { childId: "child_B", state: "completed", output: {...} }
  ← stream closes (all settled)
```

**`request_input_expired` event on `lenny/await_children`:** When a child's `lenny/request_input` call times out (governed by `maxRequestInputWaitSeconds`, [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)), the gateway delivers a `REQUEST_INPUT_TIMEOUT` tool-call error to the blocking child runtime and simultaneously emits a `request_input_expired` event on the parent's `lenny/await_children` stream:

```json
{ "type": "request_input_expired", "childId": "<child_session_id>", "requestId": "<req_id>", "expiredAt": "<ISO8601>" }
```

Parent authors MUST handle this event. It is distinct from `input_required` (child is still blocked, input is possible) and from a terminal `failed` result (the child transitions back to `running` after the timeout — it is not failed). If the child's runtime chooses to fail itself after receiving `REQUEST_INPUT_TIMEOUT`, a subsequent terminal result event will arrive on the stream.

**Subtree deadlock detection (heuristic).** The gateway uses a heuristic all-tasks-blocked detector to identify deadlocked subtrees: if every running task in a subtree (parent plus all descendants) is blocked — either in `input_required` or in `await_children` waiting only on `input_required` children — and no task in the chain can make progress, the gateway marks the subtree as `deadlocked`. **This is a per-subtree heuristic, not a true cycle-detection algorithm.** Known false-negative cases: (a) circular `lenny/send_message` dependencies across sibling subtrees (e.g., session A in `input_required` waiting for session B to respond, and session B in `input_required` waiting for A — possible when `messagingScope: siblings`) are not detected because the detector's scope is a single subtree and `send_message` creates cross-subtree dependencies invisible to it; (b) mixed blocking where some children are `running` but themselves blocked on external resources (e.g., timed-out LLM calls) makes the subtree appear non-deadlocked because not all tasks are in `input_required` or `await_children`. When `messagingScope: siblings` is active, deployers should be aware that cross-subtree circular waits are bounded by `maxRequestInputWaitSeconds` (the individual `request_input` timeout) rather than by the deadlock detector. The root task of the deadlocked subtree receives a `deadlock_detected` event on its `await_children` stream, carrying the list of blocked `requestId` values and their originating task IDs. The root task's agent can then break the deadlock by responding to one or more of the pending `request_input` calls, or by cancelling blocked children. If the deadlock is not resolved within `maxDeadlockWaitSeconds` (default: 120, configurable per pool), the gateway fails the deepest blocked tasks with error code `DEADLOCK_TIMEOUT`.

**`deadlock_detected` event schema:**

```json
{
  "type": "deadlock_detected",
  "deadlockedSubtreeRoot": "<session_id>",
  "blockedRequests": [
    { "requestId": "<id>", "taskId": "<session_id>", "blockedSince": "<ISO8601>" }
  ],
  "detectedAt": "<ISO8601>",
  "willTimeoutAt": "<ISO8601>"
}
```

`willTimeoutAt` is `detectedAt + maxDeadlockWaitSeconds`. Parent agents MUST handle this event and either resolve one of the pending `request_input` calls or cancel blocked children before `willTimeoutAt` to avoid `DEADLOCK_TIMEOUT` failures.

### 8.9 Task Tree

The gateway maintains a complete task tree:

```
root_task (client → pod A)
├── child_task_1 (pod A → pod B)
│   └── grandchild_task_1 (pod B → pod C)
└── child_task_2 (pod A → pod D)
```

Each node tracks: session_id, generation, pod, state, lease, budget consumed, failure history.

### 8.10 Delegation Tree Recovery

The gateway tracks the full task tree **independently of pods** in the SessionStore. This enables recovery when any node in the tree fails.

**Recovery ordering:** The gateway recovers delegation trees **bottom-up** (leaves first). For each level, the gateway attempts recovery of all nodes at that depth before moving to the next level up. This ensures that by the time a parent resumes, its children are already in a known state (recovered, failed, or still running).

**Per-level and total tree timeouts:**

| Parameter                 | Default | Scope          | Description                                                                                          |
| ------------------------- | ------- | -------------- | ---------------------------------------------------------------------------------------------------- |
| `maxLevelRecoverySeconds` | 120     | Per tree level | Maximum time the gateway waits for all nodes at a single depth to complete recovery before giving up |
| `maxTreeRecoverySeconds`  | 600     | Entire tree    | Total wall-clock bound for recovering the full delegation tree; overrides per-level budgets          |

If `maxLevelRecoverySeconds` is exceeded for a given depth, unrecovered nodes at that level are marked as terminally failed and the gateway continues upward. If `maxTreeRecoverySeconds` is exceeded, all remaining unrecovered nodes are marked as terminally failed and cascade policies apply from that point.

**Interaction with `maxResumeWindowSeconds`:** A node's individual resume window ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)) runs concurrently with tree recovery. If a node's `maxResumeWindowSeconds` expires before tree recovery reaches it, that node transitions to `expired` and its cascade policy is applied. Conversely, `maxTreeRecoverySeconds` can terminate a node's recovery attempt even if its `maxResumeWindowSeconds` has not yet elapsed. The effective recovery window for any node is therefore `min(maxResumeWindowSeconds, remaining maxTreeRecoverySeconds)`.

**Deep-tree deployer guidance:** The default `maxTreeRecoverySeconds` (600s) is sized for trees up to approximately 4 levels deep. For trees at depth 5 or greater, serial bottom-up recovery across all levels may consume the full 600s budget with no margin for retries or slow pod restarts. Deployers running deep trees should increase `maxTreeRecoverySeconds` using the full formula:

```
maxTreeRecoverySeconds ≥ maxResumeWindowSeconds + (maxDepth - 1) × maxLevelRecoverySeconds + buffer
```

The `maxResumeWindowSeconds` term accounts for the leaf level's individual resume window — leaf nodes may require up to `maxResumeWindowSeconds` to complete their own recovery before the gateway can progress upward. The `(maxDepth - 1) × maxLevelRecoverySeconds` term accounts for all non-leaf levels. The `buffer` accounts for gateway scheduling overhead and slow pod restarts (suggest: one `maxLevelRecoverySeconds` interval).

Example for a depth-6 tree with default values (`maxResumeWindowSeconds = 900s`, `maxLevelRecoverySeconds = 120s`):
`900 + (6 - 1) × 120 + 120 = 900 + 600 + 120 = 1620s minimum`.

> **Note:** The default `maxTreeRecoverySeconds` of 600s intentionally truncates leaf node resume windows (`maxResumeWindowSeconds = 900s`) to enforce a bounded worst-case tree recovery time. Leaf nodes whose individual resume window has not expired may still be force-terminated when `maxTreeRecoverySeconds` elapses. Deployers who need full leaf resume windows must set `maxTreeRecoverySeconds` per the formula above.

Configure via Helm `delegation.maxTreeRecoverySeconds`.

**Non-adjacent simultaneous failures:** When failures occur at multiple non-adjacent depths simultaneously (e.g., depth 1 and depth 4 both fail at the same time), the gateway continues bottom-up recovery in strict depth order. Recovery of the shallower failure (depth 1) is deferred until all deeper levels (depths 4, 3, 2) have been processed or timed out. Each failed node at a given depth is handled independently: its recovery timer starts when the gateway begins processing that depth level. Non-adjacent failures do not receive parallel recovery tracks — they share the same `maxTreeRecoverySeconds` budget. Deployers with workloads where simultaneous multi-level failures are likely (e.g., shared-node failures in a zone) should account for the additive recovery time in their `maxTreeRecoverySeconds` setting.

**Parent pod failure with active children:**

1. Gateway detects parent failure
2. Children continue running (they are independent pods with their own sessions)
3. Gateway initiates bottom-up tree recovery (see ordering above)
4. If parent resumes on a new pod:
   a. Gateway re-injects virtual MCP child interfaces for all still-active children
   b. Parent session receives a `children_reattached` event listing current child states
   c. Parent can continue awaiting, canceling, or interacting with children
   d. **Re-await protocol:** When the resumed parent re-issues `lenny/await_children`, the gateway first streams all already-settled child results from `session_tree_archive` in original-settlement order, then enters live-wait for any still-running children. The parent agent sees a consistent, ordered view of all child outcomes regardless of which settled before or after the parent failure.
5. If parent reaches any terminal state — including normal completion (`completed`), failure (retry exhaustion), expiry (`maxResumeWindowSeconds` or `maxTreeRecoverySeconds` elapsed → `expired`), or cancellation (`cancelled`):
   a. Gateway applies the parent's `cascadeOnFailure` policy (see below)

**Cascading behavior (configurable per delegation lease):**

The `cascadeOnFailure` policy applies whenever the parent reaches **any terminal state** — `completed`, `failed`, `cancelled`, or `expired`. The name `cascadeOnFailure` is historical; it governs the fate of children on all parent terminal transitions, not only failure. This means that a parent completing normally after `await_children(mode="any")` (which returns as soon as the first child completes, leaving remaining children running) will apply the cascade policy to any still-running siblings. To allow children to outlive a parent that completes normally, set `cascadeOnFailure: detach`.

| Policy             | Behavior                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------- |
| `cancel_all`       | Cancel all descendants immediately                                                                              |
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results                               |
| `detach`           | Children become orphaned; results are stored but no parent collects them. Client can query via `get_task_tree`. |

Default: `cancel_all`.

**`cascadeTimeoutSeconds` default:** 3600 (1 hour). Deployer-configurable via Helm (`delegation.cascadeTimeoutSeconds`). This bounds how long `await_completion` children may run after parent failure and how long `detach` orphans persist before cleanup. Deployers may set a platform-wide cap; per-lease values cannot exceed the cap.

**Child failure notification:**

When a child fails, the gateway injects a `child_failed` event into the parent's session stream with:

- `child_task_id`
- failure classification (transient/permanent)
- error details
- whether retries were exhausted

The parent agent can then decide to: re-spawn a replacement, continue with partial results, or propagate the failure upward.

**Orphan cleanup:** A background job runs every 60 seconds (configurable via Helm `delegation.orphanCleanupIntervalSeconds`) and detects task tree nodes whose root session has been terminated and whose `cascadeTimeoutSeconds` has expired. Orphaned children are terminated and their artifacts follow standard retention policy.

The cleanup job emits the following metrics:

| Metric                            | Type    | Description                                    |
| --------------------------------- | ------- | ---------------------------------------------- |
| `lenny_orphan_cleanup_runs_total` | Counter | Total cleanup job executions                   |
| `lenny_orphan_tasks_terminated`   | Counter | Orphan tasks terminated by cleanup             |
| `lenny_orphan_tasks_active`       | Gauge   | Currently active orphan tasks awaiting cleanup |

Deployers should alert when `lenny_orphan_tasks_active` exceeds a deployment-specific threshold (suggested: 50).

> **Note:** Detached orphan pods are **not** counted toward the originating user's concurrency quota during the detached window. Their lifetime is bounded by `cascadeTimeoutSeconds` (default 3600s), which limits exposure. However, a malicious or misbehaving orchestrator can accumulate large numbers of detached orphans across sessions. To bound this, the platform enforces a per-tenant orphan cap: `maxOrphanTasksPerTenant` (default: 100, configurable via Helm `delegation.maxOrphanTasksPerTenant` and per-tenant override via admin API). When a `detach` cascade policy would cause the tenant's active orphan count to exceed this cap, the gateway falls back to `cancel_all` for that delegation instead of detaching — the children are cancelled rather than orphaned. The gateway emits `lenny_orphan_tasks_active_per_tenant` (gauge, labeled by `tenant_id`) for per-tenant monitoring, and raises a `OrphanTasksPerTenantHigh` alert when the gauge exceeds 80% of `maxOrphanTasksPerTenant`. Deployers can raise the cap for tenants with legitimate high-fan-out detached orchestration workloads.

**Detached orphan cascade and budget semantics:**

- **Own cascade policy:** A detached orphan retains and executes its own `cascadeOnFailure` policy when it subsequently fails — the orphan's configured policy (`cancel_all`, `await_completion`, or `detach`) applies to its own descendants, independent of its former parent's now-terminal state.
- **Budget return on orphan completion:** When an orphaned child completes or fails, the standard `budget_return.lua` script is called. Its behavior depends on whether any part of the tree is still active. **Tree-wide Redis budget counter lifecycle:** tree-wide counters (`maxTreeSize`, `maxTokenBudget`, `maxTreeMemoryBytes`, keyed by `root_session_id`) are retained until all sessions in the tree have settled (including orphans under `detach` and winding-down children under `await_completion`). If the root session is terminal but other branches or orphans are still running, `budget_return.lua` decrements tree-wide counters normally — this is required so that remaining active branches see accurate budget availability. The counters are cleaned up only after the last session in the tree reaches a terminal state or `cascadeTimeoutSeconds` elapses. For per-session (parent-scoped) counters such as `parallelChildren` and `childrenTotal`, `budget_return.lua` effectively operates as a no-op when the parent is terminal — the `INCRBY` on the parent's budget key is discarded. No error is emitted. Permanently consumed budget in detached subtrees is expected and acceptable; the lifetime bound (`cascadeTimeoutSeconds`) limits the total exposure.
- **Ongoing usage charging after detachment:** A detached orphan continues to consume tokens against the tree-wide `maxTokenBudget` counter (keyed by `root_session_id`) for as long as the tree-wide counters remain alive. The orphan does not switch to the tenant's global quota — its usage remains within the original tree's budget allocation. If the tree-wide token budget is exhausted, the orphan's subsequent LLM requests are rejected with `BUDGET_EXHAUSTED` (in proxy mode) or over-run is bounded by the next `ReportUsage` interval (in direct mode). The parent's `budget_used` counter is not adjusted at detachment time — the orphan's previously reserved slice remains deducted from the parent's allocation.

