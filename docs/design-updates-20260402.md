# Lenny Technical Spec — Compiled Design Updates

**Date:** 2026-04-02
**Source:** Design review conversation (2026-03-27 through 2026-04-02) following the technical design critique (2026-03-26)
**Scope:** All agreed-upon changes to `docs/technical-design.md`

---

## How to Read This Document

- **V1 Must-Have (Breaking)** — must be in v1; impossible to fix after the first community runtime ships
- **V1 Must-Have (Feature)** — must ship with v1 to meet stated goals
- **V1 Should-Have** — significant value; should ship but won't break compatibility if deferred
- **Planned / Post-V1** — document intent now so data models accommodate it; implement later
- **Explicit Non-Decisions** — things Lenny deliberately will not do

---

## Part 1: V1 Must-Have — Breaking Changes

---

### 1.1 Replace Custom CRDs With `kubernetes-sigs/agent-sandbox`

**What changes:** Replace `AgentPool`, `AgentPod`, and `AgentSession` CRDs and the WarmPoolController with the upstream `SandboxTemplate`, `SandboxWarmPool`, `SandboxClaim`, and `Sandbox` CRDs from `kubernetes-sigs/agent-sandbox`.

**Mapping:**

| Lenny CRD | Agent-Sandbox CRD |
|---|---|
| `AgentPool` | `SandboxTemplate` |
| `AgentPod` | `Sandbox` + `SandboxWarmPool` |
| `AgentSession` | `SandboxClaim` |
| WarmPoolController | Upstream SIG Apps controller |

`executionMode` (`session`, `task`, `concurrent`) is declared on `SandboxTemplate` from v1 even if `task` and `concurrent` land slightly later in the build sequence.

**Rationale:** `kubernetes-sigs/agent-sandbox` (launched at KubeCon Atlanta, November 2025) solves the same Kubernetes-native pod lifecycle, warm pool, and claim management problem. Building on it means Lenny stops maintaining a Kubernetes controller and gets upstream community maintenance, Pod Snapshots on GKE (which directly helps with checkpointing), and a claim model designed by the Kubernetes SIG Apps community. Lenny's gateway, runtime adapter protocol, credential leasing, delegation, and MCP fabric remain entirely Lenny's own — agent-sandbox has no opinion on any of those layers.

**Pre-commit requirement:** Verify `SandboxClaim` optimistic-locking guarantee matches Lenny's current status PATCH approach before committing.

**Affects:** Sections 4.6, 6.1, 6.2, 6.3, Phase 1 and Phase 3.

---

### 1.2 Runtime Unification: `Runtime` Replaces `RuntimeType` and `SessionTemplate`

**What changes:** `RuntimeType` and `SessionTemplate` are unified into a single `Runtime` concept with an optional `baseRuntime` field distinguishing standalone from derived runtimes.

```yaml
# Standalone runtime
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
capabilities:
  interaction: one_shot     # one_shot | multi_turn
  injection:
    supported: true
    modes: [immediate, queued]
executionMode: task
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy
labels:
  team: platform
  approved: "true"

# Derived runtime
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail
agentInterface: ...
delegationPolicyRef: research-policy
publishedMetadata: ...
labels:
  team: research
  approved: "true"
```

**Labels are required from v1** — primary mechanism for environment `runtimeSelector` and `connectorSelector` matching.

**Inheritance rules — never overridable on derived runtime:** `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, `allowedResourceClasses`, `allowStandardIsolation` acknowledgment.

**Independently configurable on derived runtime:** Pool settings, `workspaceDefaults`, `setupCommands`, `setupPolicy.timeoutSeconds` (gateway takes maximum of base and derived), `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, `taskPolicy`.

**Base runtime mutability:** `image` and `name` immutable via API. All other fields mutable with impact validation — changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

**Derived runtime instantiation:** Registered via admin API as static configuration, not instantiated per-session. `workspaceDefaults` is the workspace plan the gateway materializes into every pod. Small files inline in `workspaceDefaults`, large files via MinIO reference. Session creation clients upload additional files on top of derived defaults. Workspace materialization order: base defaults → derived defaults → client uploads → file exports from parent delegation.

**Derived runtimes have fully independent pool settings.** Constraint: resource classes cannot exceed base runtime's configured classes. If no pool registered for a derived runtime, gateway falls back to base runtime's pool.

**Setup commands** run after workspace materialization and before runtime starts. While executing, pod in INIT state, not READY. Pod failure during setup causes pod replacement before warm pool entry. Setup commands run once per pod, not per task. Per-task setup belongs in the runtime's initialization.

**Affects:** Section 5.1, Section 15.1 and 15.2, Phase 1.

---

### 1.3 `type` and `capabilities` Fields on Runtime

**What changes:** Every `Runtime` has a `type` field and an optional `capabilities` field.

```yaml
type: agent          # agent (default) | mcp
capabilities:
  interaction: one_shot    # one_shot | multi_turn
  injection:
    supported: true        # default: false
    modes: [immediate, queued]
```

**`type: agent`** — participates in Lenny's task lifecycle. Receives tasks via stdin `{type: "message"}`, has sessions, workspace, delegation, elicitation, multi-turn dialog. Callable via `lenny/delegate_task`.

**`type: mcp`** — hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit). No task lifecycle. Runtime binary is oblivious to Lenny. No `capabilities` field.

**`capabilities.interaction: multi_turn`** is valid in v1. A runtime declaring `multi_turn` supports the `lenny/request_input` → response cycle and multiple `{type: "message"}` deliveries over the lifetime of a task. Multi-turn requires `capabilities.injection.supported: true` — the gateway enforces this at runtime registration. A multi-turn runtime that doesn't accept injections is incoherent.

**`capabilities.interaction: one_shot`** — the runtime consumes the initial `{type: "message"}`, produces exactly one `{type: "response"}` carrying the final result, and the task ends. May use `lenny/request_input` once (for a single clarification). Second call returns a gateway error.

**`capabilities.injection`** declares whether the runtime supports mid-session message delivery. Default: `supported: false`. Gateway rejects injection attempts against unsupported sessions at the API level before they reach the adapter.

**Capabilities are customizable per tenant**, with the platform defaults as described above.

**Affects:** Section 5.1, Phase 1.

---

### 1.4 Adapter Protocol: Multi-Server MCP + Lifecycle Channel

**What changes:** The stdin/stdout JSON Lines binary protocol (Section 15.4.1) is replaced by a two-part model.

**Part A — Multiple focused local MCP servers** (intra-pod, stdio or Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`
- **One MCP server per authorized connector** — each connector in the session's delegation policy gets its own independent MCP server. No aggregated connector proxy — aggregation is not lossless per MCP spec (capability negotiation is per-server, sampling breaks, tool name collisions, resource URI collisions).

**No workspace MCP server.** Workspace is materialized to `/workspace/current` before the runtime starts. The runtime accesses it via the filesystem directly.

**Part B — Lifecycle channel** — separate stdin/stdout stream pair for operational signals:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    interrupt_request, credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged
```

Optional. Runtimes that don't open it operate in fallback-only mode. Six message types inbound, five outbound. Versioned by capability negotiation at the top. Unknown messages silently ignored on both sides.

**Runtime integration tiers (agent-type only):**
- **Minimum** — stdin/stdout binary protocol only. Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout. This is the floor. Zero Lenny knowledge required.
- **Standard** — minimum plus connects to adapter's platform MCP server and connector servers via the adapter manifest. Uses platform capabilities (delegation, discovery, output parts, elicitation). Standard MCP — no Lenny-specific code.
- **Full** — standard plus opens the lifecycle channel. True session continuity, clean interrupt points, mid-session credential rotation.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per task execution.

```json
{
  "platformMcpServer": { "socket": "/run/lenny/platform-mcp.sock" },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [],
  "agentInterface": { ... },
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

`runtimeMcpServers` slot reserved from v1 for future use by `type:mcp` runtimes accessible via adapter proxy.

**Startup sequence for `type: agent` runtimes:**

1. Pod created by Kubernetes
2. Adapter opens gRPC connection to gateway (mTLS)
3. Adapter writes placeholder manifest (connector servers unknown yet)
4. Adapter signals READY to gateway — pod enters warm pool
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials` → `StartSession`
6. Adapter writes **final manifest** (connector servers now known from lease)
7. Adapter spawns runtime binary
8. Runtime reads manifest, connects to MCP servers (Standard/Full), opens lifecycle channel (Full)
9. Adapter sends `lifecycle_capabilities` (Full); receives `lifecycle_support`
10. Adapter delivers first `{type: "message"}` on stdin

**Security:** The local MCP servers never expose gateway credentials, mTLS certificates, other sessions, internal Lenny state, or anything about other tenants. The pod sandbox (gVisor/Kata) and network policy are the security boundary, not the protocol. The adapter never advertises the `sampling` MCP capability to the local server. Unix socket permissions: mode 0600, owned by the agent binary's UID.

**Affects:** Section 15.4 and 15.4.1 (full replacement), Section 4.7, Phase 2.

---

### 1.5 Binary Protocol: Unified `message` Type — `prompt` Removed

**What changes:** The `prompt` inbound message type is removed entirely. The unified `message` type handles all inbound content delivery — the initial task and all subsequent messages. No `sessionState` field — the runtime knows it's receiving its first message by virtue of just having started.

**Inbound messages (adapter → runtime via stdin):**

| `type` | Description |
|---|---|
| `message` | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` for concurrent-workspace mode. |
| `heartbeat` | Periodic liveness ping |
| `shutdown` | Graceful shutdown with no new task |

**Outbound messages (runtime → adapter via stdout):**

| `type` | Description |
|---|---|
| `response` | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |
| `tool_call` | Runtime requests a tool call. Carries `slotId` in concurrent-workspace mode. |
| `status` | Optional status/trace update |

**`follow_up` renamed to `message` everywhere.** No `follow_up` type anywhere in the protocol.

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call on the platform MCP server.

**`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This allows multiple independent concurrent task streams through a single stdin channel, eliminating the need for any separate delivery mechanism.

**Multimodal input support:** The `message` type carries an `input` field containing an `OutputPart[]` array, supporting text, images, structured data, and other content types. This makes the prompt mechanism generic rather than text-only.

**Task mode between-task signaling:** Adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes. The next `{type: "message"}` after scrub is the start of the new task.

**Affects:** Section 15.4.1 (binary protocol message types).

---

### 1.6 Internal `OutputPart` Format

**What changes:** `agent_text` streaming event replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays. This is Lenny's internal content model — the adapter translates to/from external protocol formats (MCP, A2A) at the boundary.

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

**Five properties that make this future-proof:**

- **`type` is an open string — not a closed enum.** `"text"`, `"code"`, `"reasoning_trace"`, `"citation"`, `"screenshot"`, `"diff"` — whatever the runtime needs. Unknown types passed through opaquely. The gateway never needs to update for new semantic types.
- **`mimeType` handles encoding separately.** The gateway validates, logs, and routes based on MIME type without understanding semantics.
- **`inline` vs `ref` as properties, not types.** A part either contains bytes inline or points to a reference. Both valid for any type.
- **`annotations` as an open metadata map.** `role`, `confidence`, `language`, `final`, `audience` — any metadata. The gateway can index and filter on annotations without understanding the part type.
- **`parts` for nesting.** Compound outputs (e.g., `execution_result` containing code, stdout, stderr, chart) are first-class.
- **`id` enables part-level streaming updates** — concurrent part delivery where text streams while an image renders.

**Rationale for internal format over MCP content blocks directly:** Runtimes are insulated from external protocol evolution. When MCP adds new block types or A2A parts change, only the gateway's `ExternalProtocolAdapter` translation layer updates — runtimes are untouched. The lossy cases are in the downward translations, not in the internal representation.

SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays for runtime authors who want to produce output using familiar MCP formats.

**Affects:** Section 15.4.1, Section 8.9, Section 7.2.

---

### 1.7 `MessageEnvelope` — Unified Message Format

**What changes:** All inbound content messages use a unified `MessageEnvelope` across the stdin binary protocol, platform MCP server tools, and all external APIs.

```json
{
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system | external",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "input": [OutputPart[]]
}
```

**Adapter-injected fields — runtime never supplies these:**
- `from.kind` and `from.id` — injected by the adapter from execution context
- `requestId` in `lenny/request_input` — generated by the gateway; runtime only supplies `parts`

**`delivery`** — optional mechanical hint. `immediate` means deliver at next stdin read (after interrupt acknowledgment if needed). Absent means queue for next natural pause.

**`id`** — every message has a stable ID enabling threading, reply tracking, and non-linear context retrieval.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Future-proof:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all future conversational patterns without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

**Affects:** Section 7.2, Section 15.4.1, Section 9.

---

### 1.8 TaskRecord Uses Messages Array + `input_required` State

**What changes:** Task record schema uses a messages array forward-compatible with multi-turn dialog:

```json
{
  "messages": [
    { "role": "caller", "parts": [...] },
    { "role": "agent",  "parts": [...], "state": "completed" }
  ]
}
```

**Task state machine — `input_required` is reachable in v1:**
```
submitted → running → completed
                    → failed
                    → input_required   (reachable via lenny/request_input)
```

`lenny/await_children` (and `lenny/await_child`) unblock when a child enters `input_required`, returning a partial result with the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response — it yields partial events before the final settled result.

**Affects:** Section 8.9.

---

### 1.9 Session State Machine Gains `suspended`

**What changes:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (interrupt_request + interrupt_acknowledged)
suspended → running   (resume_session — no new content)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed (terminate)
```

Pod held, workspace preserved, `maxSessionAge` timer paused while suspended. `interrupt_request` is a standalone lifecycle signal — pause-and-decide with decoupled timing. Distinct from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**`interrupt_request` does NOT cascade** to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children.

**Affects:** Section 7.2, Section 6.1.

---

### 1.10 Single `lenny/delegate_task` Tool

**What changes:** One delegation tool. Target id is opaque — runtime does not know whether target is a standalone runtime, derived runtime, or external registered agent. No separate `external_delegate` tool.

```
lenny/delegate_task(
  target: string,
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

`TaskSpec`:
```json
{
  "input": [OutputPart[]],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`.

**Affects:** Section 8.2, Section 8.9, Section 9.1.

---

### 1.11 Gateway-Mediated Session Messaging

**What changes:** All inter-session communication is gateway-mediated. Platform MCP tools:

```
lenny/send_message(
  to: string,             // taskId
  message: MessageEnvelope
) → void

lenny/request_input(
  parts: OutputPart[]     // question content only
) → MessageEnvelope       // blocks until answer arrives

lenny/get_task_tree() → TaskTreeNode

lenny/send_to_child(
  task_id: string,
  message: OutputPart[]
) → void
```

`lenny/get_task_tree` returns:
```json
{
  "self": { "taskId": "task_001", "sessionId": "sess_abc" },
  "parent": { "taskId": "task_root", "sessionId": "sess_parent" },
  "children": [...],
  "siblings": [
    { "taskId": "task_002", "sessionId": "sess_xyz",
      "state": "running", "runtime": "analyst-agent" }
  ]
}
```

**`taskId` is the messaging address** — stable across pod recovery generations.

**Message delivery routing — three paths:**
1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves blocked tool call directly. No stdin delivery, no interrupt.
2. **No matching pending request, runtime available** → `{type: "message"}` to stdin at next read.
3. **No matching pending request, runtime blocked in `await_children`** → buffered in inbox; delivered before the next `await_children` event.

**`lenny/send_to_child` is active in v1** — it is not a stub. Delivers a `{type: "message"}` to a child session via the same gateway-mediated path. This is the primary mechanism a parent agent uses to continue a multi-turn conversation with a child that has not entered `input_required`.

**`lenny/request_input` replaces the stdout `input_required` message type.**

**Affects:** Section 8.2, Section 9, Section 7.2.

---

### 1.12 `await_children` Unblocks on `input_required`

**What changes:** `lenny/await_children` unblocks when a child enters `input_required` state. The partial result carries the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response.

When the parent calls `lenny/send_message` with `inReplyTo: "req_001"`, the gateway resolves the child's blocked `lenny/request_input` tool call directly. The parent then re-awaits.

**Affects:** Section 8.9, Section 9.

---

### 1.13 `DelegationPolicy` as First-Class Resource

**What changes:** `allowedRuntimes`, `allowedConnectors`, and `allowedPools` fields on delegation lease replaced by named `DelegationPolicy` resources with tag-based matching evaluated at delegation time.

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
```

**Two policy levels:**
- **Runtime-level policy** (deployment time, tag rules) — set via `delegationPolicyRef` on the runtime
- **Derived runtime policy** (post-deployment, can only restrict) — set via `delegationPolicyRef` on the derived runtime

**Effective policy = `base_policy ∩ derived_policy`** — derived runtime policy can only restrict.

**Dynamic tag evaluation at delegation time.** Tags can change without redeploying — policy re-evaluated on each delegation.

**Session-level override with `maxDelegationPolicy`** on the delegation lease.

**Discovery scoping:** `lenny/discover_agents` returns only targets authorized by the calling session's effective delegation policy. Returns `type: agent` runtimes and external agents only — `type: mcp` runtimes do not appear.

**Affects:** Section 8.3, Section 5.1.

---

### 1.14 `executionMode` on Pool Configuration — All Three in V1

```yaml
executionMode: session | task | concurrent
```

All three modes implemented in v1. Graph mode removed as a separate concept — graph-aware runtimes are session-mode runtimes that optionally emit trace spans via the observability protocol.

**Affects:** Section 5.2, Phase 1.

---

### 1.15 `agentInterface` Field on Runtime

**What changes:** `type: agent` runtimes gain an optional `agentInterface` field serving three purposes: discovery, A2A card auto-generation, and adapter manifest summaries.

```yaml
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
    - type: "application/json"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
```

`supportsWorkspaceFiles: true` signals that workspace files in TaskSpec will be honored, distinguishing internal runtimes from external agents.

`type: mcp` runtimes do not have `agentInterface`.

**Affects:** Section 5.1.

---

### 1.16 `publishedMetadata` on Runtime

**What changes:** Generic metadata publication mechanism on `Runtime`, replacing any named protocol-specific fields (e.g., no dedicated `agentCard` field).

```yaml
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public    # internal | tenant | public
    value: '...'
```

**Visibility levels:**
- **`internal`** — served at `GET /internal/runtimes/{name}/meta/{key}`, requires valid Lenny session JWT. Only reachable from inside the cluster.
- **`tenant`** — same as internal but additionally filtered by `tenant_id` claim in the JWT. An agent in tenant A cannot discover tenant B's agents.
- **`public`** — served at `GET /v1/runtimes/{name}/meta/{key}`, no auth required. A2A cards meant for cross-organization discovery live here.

Not-found and not-authorized produce identical responses — no enumeration.

Gateway treats content as **opaque pass-through** — stores and serves without parsing or validating. Validation is the runtime author's responsibility.

**Rationale:** Does not encode a bet on A2A's longevity into the schema. Naturally accommodates agent cards, OpenAPI specs, cost manifests, or whatever the ecosystem invents.

**Affects:** Section 5.1.

---

### 1.17 `ExternalAdapterRegistry` — Replaces Single Protocol Adapter

**What changes:** Gateway uses `ExternalAdapterRegistry` with simultaneously active adapters routing by path prefix. Replaces the monolithic MCP-only external interface.

All adapters implement:
```go
type ExternalProtocolAdapter interface {
    HandleInbound(ctx, w, r, dispatcher) error
    HandleDiscovery(ctx, w, r, runtimes []AuthorizedRuntime) error
    Capabilities() AdapterCapabilities
}
```

**`HandleDiscovery` is required on all adapters.** Every adapter translates Lenny's policy-scoped runtime list into its protocol's native discovery format.

**Three tiers of pluggability:**
- **Built-in** (compiled in): MCP, OpenAI Completions, Open Responses. Always available, configurable via admin API.
- **Config-driven**: deployer points gateway at a Go plugin binary or gRPC service at startup.
- **Runtime registration via admin API**: `POST /v1/admin/external-adapters` — takes effect immediately, no restart.

**Built-in adapter inventory:**

| Adapter | Path prefix | Protocol |
|---|---|---|
| `MCPAdapter` | `/mcp` | MCP Streamable HTTP |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions |
| `OpenResponsesAdapter` | `/v1/responses` | Open Responses Specification |
| `A2AAdapter` | `/a2a/{runtime}` | A2A (post-v1) |
| `AgentProtocolAdapter` | `/ap/v1/agent` | AP (post-v1) |

`OpenResponsesAdapter` covers both Open Responses-compliant clients and OpenAI Responses API clients. OpenAI's Responses API is a proper superset of Open Responses; the difference is OpenAI's proprietary hosted tools, which Lenny doesn't implement.

**Affects:** Section 4.1, Section 15, Phase 1.

---

### 1.18 `type: mcp` Runtime Dedicated MCP Endpoints

**What changes:** Each enabled `type: mcp` runtime gets a dedicated MCP endpoint at `/mcp/runtimes/{runtime-name}`. Standard MCP capability negotiation. Not aggregated. An implicit session record is created per connection for audit and billing.

**Discovery:** `GET /v1/runtimes` and `list_runtimes` return `mcpEndpoint` and `mcpCapabilities.tools` preview for `type: mcp` runtimes.

**Affects:** Section 4.1, Section 9, Section 15.

---

### 1.19 Comprehensive Admin API

**What changes:** All operational configuration is API-managed. Configuration is split into two planes:

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas, User Role Assignments, Egress Profiles, Experiments, Scaling Policies, Memory Store Config, Webhooks, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

CRDs become derived state reconciled from Postgres by PoolScalingController.

**Admin API design constraints:** Error taxonomy, OIDC auth, etag-based concurrency, `dryRun` support, OpenAPI spec, audit logging.

**Affects:** Section 15.1, Section 10.2, Phase between 4 and 5.

---

## Part 2: V1 Must-Have — Feature Changes

---

### 2.1 Multi-Turn Agent Dialog

**What changes:** `interaction: multi_turn` is valid in v1. This is not a stub — it is a fully implemented capability for runtimes that declare it.

**What `multi_turn` means precisely:** A runtime that declares `multi_turn` may call `lenny/request_input` multiple times during a single task, receive multiple `{type: "message"}` deliveries, and produce partial outputs before reaching a final response. The task does not end after the first `response` message on stdout.

**Constraints:**
- `interaction: multi_turn` requires `capabilities.injection.supported: true` — the gateway enforces this at runtime registration. A multi-turn runtime that doesn't accept injections is incoherent.
- `interaction: one_shot` runtimes that call `lenny/request_input` more than once get a gateway error on the second call.
- Multi-turn is independent of external protocol. A2A natively supports multi-turn via its `input-required` state — this maps directly to Lenny's `input_required` task state. A2A is still post-v1; multi-turn within Lenny's internal task model is v1.

**`lenny/send_to_child` is active.** It delivers a `{type: "message"}` to a child session. Combined with `await_children` unblocking on `input_required`, this enables full synchronous parent-child multi-turn conversations within Lenny.

**Affects:** Section 5.1, Section 9, Phase 1 (capabilities schema), Phase 9 (tool activation).

---

### 2.2 Task Pod Execution Mode

**What changes:** `executionMode: task` reuses pods across sequential tasks with workspace scrub between tasks. Security requirement: explicit deployer acknowledgment.

```yaml
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn    # warn | fail
```

Lifecycle: task completes → adapter sends `terminate(task_complete)` on lifecycle channel → runtime acknowledges → cleanup commands execute (have access to task state) → Lenny scrub runs → pod available. `setupCommands` run once per pod at start, not per task.

**Affects:** Sections 6.1, 6.2, 7, 5.2.

---

### 2.3 Concurrent Execution Mode

**What changes:** Two sub-variants via `concurrencyStyle`:

```yaml
executionMode: concurrent
concurrencyStyle: stateless    # stateless | workspace
maxConcurrent: 8
```

**`concurrencyStyle: workspace`** — each slot gets its own workspace. Gateway tracks per-slot lifecycle. **Task delivery via `slotId` multiplexing over stdin** — the adapter assigns a `slotId` per slot; the runtime implements a dispatch loop keyed on `slotId`; all binary protocol messages (inbound and outbound) carry `slotId` in this mode. Cross-slot isolation is process-level and filesystem-level — explicitly weaker than session mode. Deployer acknowledgment required.

**`concurrencyStyle: stateless`** — no workspace materialization. Gateway routes through Kubernetes Service. Pod readiness probe reflects slot availability. PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

**Truly stateless runtimes** with no workspace and no expensive shared state should be registered as external connectors, not Lenny-managed pods.

**Affects:** Sections 5.2, 6.1, 6.2, 4.1.

---

### 2.4 `setupPolicy` on Runtime

```yaml
setupPolicy:
  timeoutSeconds: 300      # optional — waits indefinitely if absent
  onTimeout: fail          # fail | warn
```

Gateway takes the **maximum** of base and derived `timeoutSeconds` if both set.

**Affects:** Section 5.1, Section 6.2.

---

### 2.5 `ConnectorDefinition` as First-Class Resource

**What changes:** Connector configuration is a first-class admin API resource, supporting both tool connectors and external agents.

```yaml
connectors:
  - id: github
    displayName: GitHub
    mcpServerUrl: https://mcp.github.com
    transport: streamable_http
    auth:
      type: oauth2
      authorizationEndpoint: https://github.com/login/oauth/authorize
      tokenEndpoint: https://github.com/login/oauth/access_token
      clientId: "..."
      clientSecretRef: lenny-system/github-client-secret
      scopes: [repo, read:org]
    visibility: tenant
    labels:
      team: platform
```

Includes `labels` map for environment selector matching. Tool capability metadata derived from MCP `ToolAnnotations` at registration time.

All connectors must be registered before they can be used — unregistered external MCP servers cannot be called from inside a pod (security: gateway must know about every external endpoint for OAuth flow, audit logging).

**Affects:** Section 4.3, Section 9.3.

---

### 2.6 Runtime Discovery on All External Interfaces

**What changes:** Every external interface exposes runtime discovery via `HandleDiscovery`. All results are identity-filtered and policy-scoped. Not-found and not-authorized produce identical responses — no enumeration.

- **MCP:** `list_runtimes` tool
- **REST:** `GET /v1/runtimes` with full `agentInterface` and `mcpEndpoint` fields
- **OpenAI Completions:** `GET /v1/models`
- **Open Responses:** `GET /v1/models`

**Affects:** Section 9, Section 15.1, Section 15.2.

---

### 2.7 OpenAI Completions Adapter

**What changes:** Built-in `ExternalProtocolAdapter` implementing `POST /v1/chat/completions` and `GET /v1/models`. Stateless, ephemeral sessions. Tool handling — opaque mode only.

**Affects:** Section 15.

---

### 2.8 Open Responses Adapter

**What changes:** Built-in `ExternalProtocolAdapter` implementing the Open Responses Specification. Covers both Open Responses-compliant and OpenAI Responses API clients for all core features. Lenny connectors → Open Responses **internal tools**. Client-defined functions (external tools) not supported in v1.

**Affects:** Section 15.

---

### 2.9 Capability Inference from MCP `ToolAnnotations`

**What changes:** Gateway reads `tools/list` at connector or `type:mcp` runtime registration and infers capabilities from MCP `ToolAnnotations`. No manual re-annotation required.

| MCP annotation | Inferred capabilities |
|---|---|
| `readOnlyHint: true` | `read` |
| `readOnlyHint: false, destructiveHint: false` | `write` |
| `destructiveHint: true` | `write, delete` |
| `openWorldHint: true` | `network` |
| No annotations | `admin` (conservative default) |
| *(no MCP equivalent)* | `execute`, `admin` — set via `toolCapabilityOverrides` |

Tenant-overridable via `tenantRbacConfig.mcpAnnotationMapping`.

**Affects:** Section 4.3, Section 5.1.

---

### 2.10 PoolScalingController

**What changes:** Pool scaling intelligence extracted into dedicated `PoolScalingController` separate from `WarmPoolController`. WarmPoolController manages individual pod lifecycle. PoolScalingController manages desired pool configuration.

Backed by pluggable `PoolScalingStrategy` interface. Fully replaceable by deployers.

**Default formula:**
```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

`safety_factor` defaults to 1.5 for agent-type pools, 2.0 for mcp-type pools.

`scaleToZero` disabled by default for `type: mcp` pools. Deployer opt-in required.

**Pool phases:** pre-warm, ramp, steady state, wind-down — all automatic.

**Affects:** Section 4.6 (split into two controllers), Section 5.2.

---

### 2.11 Experiment Primitives

**What changes:** `ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor`.

```yaml
experiments:
  - id: claude-v2-rollout
    status: active                  # active | paused | concluded
    baseRuntime: claude-worker
    variants:
      - id: treatment
        runtime: claude-worker-v2
        pool: claude-worker-v2-sandboxed-medium
        weight: 10                  # percentage
    targeting:
      mode: percentage              # percentage | cohort | combined
      sticky: user                  # user | session | none
    propagation:
      childSessions: inherit        # inherit | control | independent
```

**Targeting modes:** `percentage` (deterministic hash), `cohort` (explicit whitelist), `combined` (percentage within cohort).

**Experiment context propagates through delegation leases:**
```json
{
  "experimentContext": {
    "experimentId": "claude-v2-rollout",
    "variantId": "treatment",
    "inherited": true
  }
}
```

Results API aggregates eval scores by variant: `GET /v1/experiments/{id}/results`.

PoolScalingController manages variant pool lifecycle automatically — variant warm count derived from base pool demand signals × variant weight × safety factor.

**Warm pool implication:** ExperimentController creates and continuously updates variant pool sizing based on experiment traffic weight. Deployer does not manually size variant pools.

**What Lenny explicitly will not build:** Statistical significance testing, experiment lifecycle management (winner declaration), multi-armed bandits, segment analysis. Those belong in dedicated experimentation platforms.

**Affects:** Section 4.8, Section 15.1.

---

### 2.12 Injection Capability Declaration and External Session Messaging

**What changes:** `POST /v1/sessions/{id}/messages` is the unified client-facing endpoint for all message types. MCP equivalent: `send_message` tool. Gateway rejects injection attempts against sessions whose runtime has `injection.supported: false`.

Interrupt remains separate: `POST /v1/sessions/{id}/interrupt` — lifecycle signal, not content delivery.

**Affects:** Section 7.2, Section 15.1.

---

## Part 3: V1 Should-Have

---

### 3.1 Environment Resource and RBAC Model

**What changes:** `Environment` as first-class admin API resource. Named, RBAC-governed project context grouping runtimes and connectors for a team.

**Two access paths:**
- **Transparent filtering** (default) — user connects to standard endpoint; gateway computes the union of authorized runtimes across all environments where the user's groups have a role and returns that filtered view.
- **Explicit environment endpoint** (opt-in) — dedicated paths across all external interfaces: `/mcp/environments/{name}`, `/v1/environments/{name}/sessions`, scoped model namespace on `/v1/responses` and `/v1/chat/completions`.

**Runtime and connector selection is tag-based** using Kubernetes-style label expression syntax with `include`/`exclude` overrides.

**`mcpRuntimeFilters`** — capability-based tool filtering for `type: mcp` runtimes. Capabilities inferred from MCP `ToolAnnotations` (see 2.9). Name collisions resolved by `runtime:tool` qualified reference in `overrides`.

**`connectorSelector`** — internal only. Controls what connectors agents can use via the platform MCP server.

**Cross-environment delegation** — structured bilateral declaration model:

```yaml
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
    - targetEnvironment: analytics-team
      runtimes:
        ids: [analytics-agent]
  inbound:
    - sourceEnvironment: "*"
      runtimes:
        matchLabels:
          shared: "true"
```

Effective cross-environment access requires both sides to permit it. Neither side can unilaterally grant the other access. The boolean shorthand (`false` | `true`) remains valid for simple cases. `true` bypasses environment scoping and falls through to DelegationPolicy only. Boolean and structured form are mutually exclusive on any given environment.

**Effective delegation scope:** `(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy`

**Gateway enforcement at delegation time:**
1. Resolve target runtime to its home environment
2. Verify calling environment has an outbound declaration permitting target → `target_not_in_scope` if absent
3. Verify target environment has an inbound declaration permitting calling environment → `target_not_in_scope` if absent
4. Apply DelegationPolicy as normal → `target_not_authorized` if policy doesn't permit

**Connectors are never cross-environment.** Child sessions use their own environment's connector configuration.

**Environment-level billing rollup (v1):** `environmentId` populated on all billing events for sessions created in an environment context. New endpoint: `GET /v1/admin/environments/{name}/usage`.

**Advanced membership analytics (v1):** Three read-only endpoints:
- `GET /v1/admin/tenants/{id}/access-report` — cross-environment access matrix
- `GET /v1/admin/environments/{name}/access-report` — resolved member list with group expansion
- `GET /v1/admin/environments/{name}/runtime-exposure` — runtimes/connectors in scope with capability filters

**Member roles:** `viewer`, `creator`, `operator`, `admin`.

**`noEnvironmentPolicy`:** `deny-all` (platform default) or `allow-all`. Configurable per tenant, with platform-wide default at Helm time.

**Identity:** OIDC. Groups from LDAP/AD carried as JWT claims. `introspectionEnabled: true` adds real-time group checks at latency cost.

**Tenant RBAC Config:**
```yaml
tenantRbacConfig:
  identityProvider:
    issuerUrl: https://idp.acme.com
    clientId: lenny-acme
    groupsClaim: groups
    usernameClaim: email
    groupFormat: short-name
  tokenPolicy:
    maxTtlSeconds: 3600
    introspectionEnabled: false
    introspectionEndpoint: https://idp.acme.com/introspect
  noEnvironmentPolicy: deny-all
  mcpAnnotationMapping: ...    # optional tenant-level overrides
```

**V1 data model accommodations:**
- `Runtime` resources need `labels` map from v1
- `Connector` registrations need `labels` map from v1
- `type:mcp` runtime tool schemas cached by gateway from v1
- `GET /v1/runtimes` and `list_runtimes` accept optional `?environmentId=` stub
- Session creation accepts optional `environmentId` parameter as no-op stub
- `environmentId` as nullable field on billing event schema from Phase 1
- `crossEnvironmentDelegation` structured form schema from Phase 1

**Affects:** Section 5.1 (labels), Section 4.1 (identity-aware routing), Section 15.1, New Section 10 (RBAC).

---

### 3.2 `MemoryStore` Interface

**What changes:** `MemoryStore` as a role-based storage interface alongside `SessionStore`, `ArtifactStore`, etc.

```go
type MemoryStore interface {
    Write(ctx, scope MemoryScope, memories []Memory) error
    Query(ctx, scope MemoryScope, query string, limit int) ([]Memory, error)
    Delete(ctx, scope MemoryScope, ids []string) error
    List(ctx, scope MemoryScope, filter MemoryFilter) ([]Memory, error)
}

type MemoryScope struct {
    TenantID  string
    UserID    string
    AgentType string   // optional: scope to a runtime type
    SessionID string   // optional: scope to a specific session
}
```

Default: Postgres + pgvector. Fully replaceable. Deployers who want Mem0 or Zep implement the interface backed by their choice. Technology choice explicitly deferred — the memory layer market is not settled as of Q1 2026.

Accessed via `lenny/memory_write` and `lenny/memory_query` tools on platform MCP server. Runtimes that don't need memory ignore these tools entirely.

**Affects:** Section 4 (storage interfaces), Section 9 (platform MCP server tools).

---

### 3.3 Semantic Caching at the LLM Proxy

**What changes:** Optional `CachePolicy` on `CredentialPool` backed by pluggable `SemanticCache` interface.

```yaml
cachePolicy:
  strategy: semantic
  ttl: 300
  similarityThreshold: 0.92
  backend: redis
```

Default Redis-backed implementation. Fully replaceable by deployers. Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

### 3.4 `RequestInterceptor` Extension Point

**What changes:** Formalized interceptor interface at gateway phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`.

Built-in interceptors:
- `ExperimentRouter` — active when experiments are defined
- `GuardrailsInterceptor` — disabled by default, no built-in content classification or prompt injection detection logic. Deployers wire in AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or their own classifier.

External interceptors via gRPC (like Kubernetes admission webhooks).

**Affects:** Section 4.8.

---

### 3.5 Agent Evaluation Hooks

**What changes:** `POST /v1/sessions/{id}/eval` accepts scored evaluator results (LLM-as-judge scores, custom heuristics, ground-truth comparisons). Stored as session metadata and surfaced in observability dashboard and experiment results.

`POST /v1/sessions/{id}/replay` re-runs a session against a different runtime version using the same workspace and prompt history — enables regression testing when upgrading runtimes.

No built-in eval logic. Evaluation work (running LLM-as-judge, building test datasets) done by deployers using their existing eval tooling (Braintrust, Langfuse, custom).

**Affects:** Section 15.1, Section 16.

---

### 3.6 `CredentialRouter` Interface

**What changes:** Pluggable credential pool selection logic. Default: least-loaded/round-robin/sticky-until-failure. Deployers who want cost-aware, latency-based, or intent-based model routing implement the interface.

Disabled by default, opt-in per pool.

**Affects:** Section 4.9.

---

## Part 4: Planned / Post-V1

---

### 4.1 A2A Full Support

**What to document now:**
- `ExternalProtocolAdapter` is the mechanism. A2A is implementation two.
- Inbound: gateway serves `POST /a2a/{runtimeName}/tasks` via `A2AAdapter`
- Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`
- A2A `input-required` state maps directly to Lenny's `input_required` task state — multi-turn over A2A is the primary external multi-turn use case
- A2A streaming output via SSE requires the adapter to bridge SSE events to `OutputPart` events through the parent's `await_children` streaming response — this is the primary implementation complexity for A2A multi-turn
- `agentInterface` auto-generates A2A agent cards
- Per-agent A2A endpoints (not aggregated) at `/a2a/runtimes/{name}`
- `allowedExternalEndpoints` slot on delegation lease schema must exist from v1
- `AuthEvaluator` must be extensible for non-OIDC auth schemes (A2A Agent Card validation)
- Runtime authors who want intra-pod A2A client behavior must opt in explicitly

**A2A adoption context (Q1 2026):** Both MCP and A2A are under the Linux Foundation's Agentic AI Foundation (AAIF). MCP has ~97M monthly SDK downloads; A2A is earlier but governance situation makes dismissal risky.

---

### 4.2 A2A Intra-Pod Support

**What to document now:** Adapter additionally serves per-agent A2A endpoints intra-pod. Adapter manifest gains A2A base URL. All A2A traffic proxied through gateway. Runtime authors opt in explicitly.

---

### 4.3 Agent Protocol (AP) Support

**What to document now:** AP defines `POST /ap/v1/agent/tasks` and step execution. Third `ExternalProtocolAdapter` implementation. No changes to intra-pod model.

---

### 4.4 Future Conversational Patterns

**What to document now:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all of these without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

---

### 4.5 Environment Management UI

**What to document now:** Full web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. The `?dryRun=true` parameter on `PUT /v1/admin/environments` is the preview mechanism and ships in v1.

---

### 4.6 Environment Resource — Post-V1 Deferred Items

**Full environment management UI:** Deferred. Admin API ships in v1. UI is thin-client work, can be built at any point without spec changes.

**Cross-environment delegation richer controls:** The structured bilateral declaration model (outbound/inbound) is v1. What is deferred is runtime-level cross-environment exceptions at granularity beyond the structured form, and integration with experiment-scoped delegation boundaries. These require a design pass once the base feature is in production.

---

### 4.7 Multi-Cluster Federation

**What to document now:** Session IDs must be globally unique. Storage interfaces must not assume single-cluster topology. Connectors registered in one cluster must be expressible by reference in multi-cluster scenarios.

---

### 4.8 UI and CLI

**What to document now:** Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic.

---

## Part 5: Explicit Non-Decisions

**5.1 No Model B Runtime Deployment.** No mechanism for packaging a graph definition as a new registered runtime. Users register derived runtimes via admin API. (Context: LangGraph deployment discussion — Model A chosen: one generic LangGraph runtime, many deployed graphs via derived runtimes.)

**5.2 No Built-In Eval Logic.** Lenny provides hooks and storage. No LLM-as-judge or hallucination detection.

**5.3 No Built-In Guardrail Logic.** Lenny provides the `RequestInterceptor` hook. No content classifiers or prompt injection detection.

**5.4 No Built-In Memory Extraction.** Lenny provides the `MemoryStore` interface and tools. Runtimes decide what to write.

**5.5 No Direct External Connector Access.** Connectors are session-internal in v1. External clients do not call connectors directly. Whether to add this later is an independent product decision; the data model accommodates it without requiring a redesign.

**5.6 Hooks-and-Defaults Design Principle.** Every cross-cutting AI capability (memory, caching, guardrails, evaluation, routing) follows the same pattern: defined as an interface with a sensible default implementation, disabled unless explicitly enabled by the deployer, fully replaceable. Lenny never implements AI-specific logic (eval scoring, memory extraction, content classification) — that belongs to specialized tools in the ecosystem.

---

## Part 6: Revised Build Sequence

**Phase 1** — Core types: `Runtime` (unified, with `labels`), `type` field, `capabilities` (including `interaction: one_shot | multi_turn`, `injection`), `executionMode`, `allowedExternalEndpoints` on delegation lease, `input_required` task state, messages array in TaskRecord, `suspended` session state. Agent-sandbox CRDs. `Connector` resource with `labels`. `environmentId` nullable field on billing event schema. `crossEnvironmentDelegation` structured form schema slot.

**Phase 2** — Replace adapter binary protocol: unified `{type:"message"}` (no separate `prompt`), `slotId` field for concurrent-workspace multiplexing, multi-server MCP adapter + lifecycle channel, adapter manifest written before binary spawns. Publish as runtime adapter specification.

**Phase 3** — PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement. `taskPolicy.cleanupCommands`.

**New Phase between 4 and 5** — Admin API foundation: runtimes, pools, connectors, delegation policies, tenant management, external adapters registry. Gateway loads config from Postgres. Capability inference from MCP annotations at connector registration.

**Phase 5** — Gateway `ExternalAdapterRegistry`. MCP adapter, Completions adapter, Open Responses adapter all active. `list_runtimes`, `GET /v1/runtimes`, `GET /v1/models` with identity-aware filtering. `type: mcp` runtime endpoints at `/mcp/runtimes/{name}`. Tenant RBAC config API. `noEnvironmentPolicy` enforcement.

**Phase 9** — `lenny/delegate_task` handles internal and external targets. `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree`, `lenny/send_to_child` (active). `lenny/discover_agents` with policy scoping. Multi-turn fully operational.

**Phase 10** — `agentInterface` in discovery. Adapter manifest includes summaries.

**Phase 12** — `type: mcp` runtime support. Concurrent execution modes including `slotId` multiplexing for workspace variant.

**New Phase 15** — Environment resource: tag-based selectors, member RBAC, `mcpRuntimeFilters` with capability model, `connectorSelector`, cross-environment delegation enforcement, billing rollup endpoint, membership analytics endpoints, explicit environment endpoints across all adapters.

**New Phase 16** — Experiment primitives, PoolScalingController experiment integration.

**New Phase 17** — Memory, caching, guardrail, eval hooks.

---

## Summary

| Tier | Count |
|---|---|
| V1 Must-Have Breaking | 19 |
| V1 Must-Have Feature | 12 |
| V1 Should-Have | 6 |
| Planned / Post-V1 | 8 |
| Explicit Non-Decisions | 6 |

---

## Appendix: Competitive Landscape Updates

The following projects were identified during the design review as relevant comparisons not covered in the original critique:

| Project | Why It Matters for Lenny |
|---|---|
| `kubernetes-sigs/agent-sandbox` | Near-identical pod lifecycle primitive being standardized upstream — **adopted as Lenny's infrastructure layer** (see 1.1) |
| E2B | Market-leading AI sandbox with Firecracker microVMs, ~150ms boot times, self-hosting options. Primary comparison point for Lenny's audience. |
| Fly.io Sprites | Jan 2026 direct competitor with Firecracker + checkpoint/restore in ~300ms. |
| Google A2A Protocol | Agent-to-agent protocol now under AAIF governance alongside MCP. Lenny's design must not close the door — **addressed via `ExternalAdapterRegistry`, `publishedMetadata`, and `allowedExternalEndpoints`** (see 1.17, 1.16, 4.1) |
| Daytona | Sub-90ms cold starts, desktop environments for computer-use agents. Relevant to Gap 3 in the critique. |
| LangSmith Deployment (formerly LangGraph Cloud) | Now has A2A + MCP + RemoteGraph. Gap with Lenny's delegation model is narrower than the critique acknowledged. |
| Amazon Bedrock AgentCore Memory | Short-term + long-term memory for agents. Motivated the `MemoryStore` interface (see 3.2). |
