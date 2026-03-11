# Lenny — Technical Specification

## 1. Overview

### 1.1 What Lenny Is

Lenny is an open-source, enterprise-grade agentic orchestration framework written in TypeScript. It uses Claude as the reasoning engine via the Anthropic Agent SDK, wrapped by a control plane that the framework operator owns. The control plane handles routing, policy enforcement, approval gating, progressive capability disclosure, and execution lifecycle. Claude runs inside bounded sessions that Lenny starts, scopes, and supervises.

The governing sentence: **"The wrapper is the control plane; Agent SDK sessions are leased worker runtimes."**

### 1.2 Problem Statement

Building reliable, enterprise-scale AI agent systems requires solving several interlocking problems:

1. **Capability saturation.** When hundreds or thousands of tools are available, exposing them all to a model degrades planning quality. The model needs to see only what's relevant.
2. **Policy enforcement.** The model must never be the authority over what it can do. Policy must live outside the model and be enforced by infrastructure.
3. **Safe execution.** Side effects (money movement, production changes, data writes) must flow through gated, auditable pathways. The model proposes; infrastructure disposes.
4. **Adaptability.** Tasks grow mid-execution. A request that starts in one domain may need capabilities from another. The system must reveal new capabilities on demand without pre-loading everything.
5. **Coordination.** When a task becomes large enough to justify multiple reasoning contexts, those contexts must be scoped, bounded, and unable to escape their lease.
6. **Governance.** As organizations accumulate hundreds of skills, agents, prompts, and context files, they need structured ownership, review, and layering. Without a registry model that supports multiple contributors — teams, individuals, platform — asset sprawl becomes ungovernable.
7. **Runtime ceilings.** Claude Code and similar agentic tools are remarkably capable for individual tasks, and continue to gain native capabilities (subagents, session resume/fork, Skills, MCP Tool Search). But enterprise workloads require additional governance: deterministic policy enforcement, bounded multi-session orchestration, credential mediation, and structured approval flows that no single-session agent provides. Lenny adds the control plane that manages agent sessions as governed, auditable workers.

No existing open-source framework provides these as a cohesive, extensible platform.

### 1.3 Design Principles

- **Scopes govern visibility, not authority.** The registry is a recursive tree of scopes. Each scope is a bounded context with its own skills, commands, agents, and child scopes. Navigation through the tree is progressive disclosure. Policy and enforcement authority remain centralized in the control plane.
- **The registry is authored as a scope tree, compiled into execution boundaries, and hydrated progressively at runtime.** Scope definitions live in a Git repo as a recursive folder hierarchy. At startup they're compiled — first into a merged scope tree, then into execution boundaries that determine which scopes may share a runtime session. At execution time each active boundary retains its internal scope hierarchy, the orchestrator defines the worker's initial visible scope/tool surface, and the worker progressively inspects or loads additional scopes via meta-tools. The control plane validates and materializes those requests without spawning a new session.
- **Scopes are authored; execution boundaries are compiled envelopes.** Scopes are the unit of authoring, ownership, and review. Execution boundaries are the unit of runtime allocation. The boundary compiler maps one or more compatible scopes into each boundary based on policy compatibility, resource constraints, and semantic affinity. Authors think in scopes; the runtime thinks in boundaries plus progressively loaded scope assets inside them.
- **Compile first, adapt second.** Boundary topology is compiled ahead of time for determinism, auditability, and policy review. Runtime adaptation is limited to progressive hydration within those prevalidated envelopes.
- **Default deny, everywhere.** If a capability, scope, or policy rule is missing, execution fails closed.
- **Pluggable at every boundary.** Storage, search, policy, workflow, audit, and approval are all interfaces. The framework ships defaults (PostgreSQL, in-memory, basic RBAC) but never hard-codes them.

### 1.4 Constraints and Decisions

| Decision                                                    | Rationale                                                                                                                                                                                                                                                                                                                          |
| ----------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| TypeScript                                                  | Agent SDK has first-class TS support. Enterprise ecosystem.                                                                                                                                                                                                                                                                        |
| Each active execution boundary is its own Agent SDK session | Within a boundary, the session starts narrow and can cumulatively load additional scope-local context, tools, skills, and agents on demand. Crossing into a child execution boundary requires spawning a new Agent SDK session via `boundary.enter`. The control plane manages each session's lifecycle, prompt, tools, and lease. |
| Execution boundary compilation                              | Scopes are authored independently but compiled into execution boundaries at startup. One or more compatible scopes may share a boundary envelope, reducing session overhead and improving performance while preserving policy isolation where required.                                                                            |
| Hybrid Lenny sessions                                       | A durable Lenny session wraps one or more Claude SDK sessions. Lenny persists conversation state, active boundary sessions, and structured findings across SDK session lifetimes, enabling hydrate/work/spin-down/resume patterns.                                                                                                 |
| REST + SSE for external API                                 | Compatible with LibreChat, Slack bots, custom UIs. No WebSocket complexity initially.                                                                                                                                                                                                                                              |
| Capability registry in a separate Git repo                  | Decouples domain knowledge from framework code. Different owners, different review cadence.                                                                                                                                                                                                                                        |
| A2A support in MVP                                          | External agent collaboration is a first-class concern, not an afterthought.                                                                                                                                                                                                                                                        |
| PostgreSQL as default backing store                         | With pgvector for semantic search. Pluggable interface for alternatives.                                                                                                                                                                                                                                                           |
| Custom minimal workflow engine                              | Behind a pluggable interface. Users can swap in Temporal, BullMQ, etc.                                                                                                                                                                                                                                                             |
| Multiple registries via overlays                            | Individuals and teams can layer their own skills, agents, and context onto the base registry. Overlays are applied in order; same-scope assets are combined. Name collisions within a scope are errors.                                                                                                                            |

---

## 2. Architecture

### 2.1 High-Level Component Map

```
Consuming Clients (LibreChat, Slack, CLI, custom)
        │
        │  REST + SSE
        ▼
┌──────────────────────────────────────────────────────────────┐
│                       SESSION API                             │
│  Authenticates callers. Opens/resumes tasks. Streams events.  │
└───────────────────────────┬──────────────────────────────────┘
                            │
┌───────────────────────────▼──────────────────────────────────┐
│                    CONTROL PLANE                              │
│                                                               │
│  ┌─────────────┐  ┌──────────────┐  ┌────────────────────┐   │
│  │ Orchestrator │  │   Scope      │  │  Policy / Lease    │   │
│  │             │  │   Resolver   │  │    Engine          │   │
│  │ Task state  │  │  Tree nav   │  │  RBAC/ABAC         │   │
│  │ Routing     │  │  Search     │  │  Lease validation  │   │
│  │ Spawn       │  │             │  │  Default deny      │   │
│  └──────┬──────┘  └──────┬──────┘  └─────────┬──────────┘   │
│         │                │                    │              │
│  ┌──────▼────────────────▼────────────────────▼───────────┐  │
│  │              BOUNDARY COMPILER                          │  │
│  │   Scope tree → execution boundaries (at startup)        │  │
│  └──────┬─────────────────────────────────────────────────┘  │
│         │                                                    │
│  ┌──────▼────────────────────────────────────────────────┐  │
│  │              AGENT RUNTIME MANAGER                      │  │
│  │   Start / stop / resume / fork Agent SDK sessions       │  │
│  └──────┬─────────────────────┬───────────────┬───────────┘  │
│         │                     │               │              │
│    Root Boundary         Child Boundary   Child Boundary     │
│   (Agent SDK)           (Agent SDK)     (Agent SDK)          │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐  │
│  │                  TOOL GATEWAY (PEP)                     │  │
│  │  Policy re-check · Lease re-check · Credential inject  │  │
│  │  Approval gating · Schema validation · Audit logging    │  │
│  └──────┬──────────────────────────────────┬──────────────┘  │
│         │                                  │                 │
│  ┌──────▼──────────┐           ┌───────────▼─────────────┐   │
│  │ APPROVAL SERVICE│           │   CREDENTIAL BROKER     │   │
│  │ Create/resolve  │           │   Ephemeral scoped      │   │
│  │ One-time grants │           │   credentials only      │   │
│  └─────────────────┘           └─────────────────────────┘   │
│                                                               │
│  ┌────────────────────────────────────────────────────────┐  │
│  │                AUDIT / TELEMETRY                        │  │
│  │  Structured events · Trace IDs · PII redaction          │  │
│  └────────────────────────────────────────────────────────┘  │
└───────────────────────────┬──────────────────────────────────┘
                            │
         ┌──────────────────┼──────────────────┐
         ▼                  ▼                  ▼
    MCP Servers        A2A Adapters      Sandbox Runners
 (gateway façades)  (external agents)  (ephemeral containers)
```

### 2.2 Component Responsibilities

**Session API.** External entry point. Authenticates callers, attaches tenant/policy context, opens or resumes root tasks, resolves approvals, streams events to clients via SSE.

**Orchestrator.** The core control-plane loop. Manages task lifecycle, defines the worker's initial visible scope/tool surface, routes worker-initiated meta-tool calls to the appropriate subsystem, and preserves durable task state across SDK session lifetimes. The worker decides when to inspect/load scopes or enter a child boundary; the orchestrator validates, routes, and supervises those moves.

**Scope Resolver.** Holds the compiled scope tree, boundary map, and per-boundary internal scope hierarchy. Builds boundary views from three layers: boundary-envelope assets, inherited shared assets from ancestors, and the set of scope assets already loaded inside the current session. It can return descriptions for eligible child scopes and materialize additional scope-local assets inside the current boundary without spawning a new session, but never returns information outside the lease's boundary path.

**Policy / Lease Engine.** Evaluates whether a given actor may perform actions within a given boundary in the current environment. Issues leases. Validates lease constraints on every tool invocation. A child lease must always be anchored to a descendant boundary path of its parent.

**Boundary Compiler.** Takes the merged scope tree (output of overlay compilation) and produces execution boundaries plus each boundary's retained internal scope hierarchy. Applies policy compatibility checks, resource constraints, manifest hints, and semantic heuristics to determine which scopes share a runtime envelope. See §5.7 for full design.

**Agent Runtime Manager.** Starts and supervises Agent SDK sessions. Each active execution boundary runs as its own Agent SDK session with a system prompt, tools (gateway-backed MCP façades from manifest + meta-tools), and a lease. The manager maps meta-tool calls back to control-plane operations for same-boundary scope inspection, scope loading, tool hydration, and child-boundary spawning.

**Tool Gateway.** The single enforcement point. Every sensitive action passes through it. It re-checks the lease, re-evaluates policy, requests approval if needed, injects scoped credentials, validates that invoked MCP tools belong to the boundary's manifest and are visible through the session's currently loaded scopes, enforces idempotency for mutations, and logs an audit event. MCP tools are exposed to sessions as gateway-backed façades — the session invokes a tool that routes through the gateway, which mediates the actual MCP server connection. Raw MCP endpoints are never directly exposed to worker sessions.

**Approval Service.** Creates approval requests bound to an exact boundary + source scope + action + normalized arguments + operation ID + expiry. Returns one-time, action-bound grant tokens. Children cannot approve themselves.

**Credential Broker.** Holds or exchanges real credentials. Issues ephemeral, least-privilege capabilities per invocation. Workers never see standing secrets. MCP server processes that require credentials (e.g., database URLs, API keys) receive them through the credential broker/proxy — not through direct environment variable injection in the manifest.

**Audit / Telemetry.** Logs every significant event: task creation, lease issuance, tool requests, policy decisions, approval lifecycle, tool execution results. Every event carries a trace ID linking it to the originating user request.

**A2A Adapter.** Discovers external A2A-compliant agents via their Agent Card, dispatches tasks, streams results. External agents are configured in scope manifests and accessible within those scopes.

**Sandbox Runner.** Runs boundary agent sessions in ephemeral containers with no standing credentials, allowlisted network egress, and filesystem isolation. Sandbox constraints are defined per scope in the manifest and merged at boundary compilation. Important for code execution, repo mutations, and shell tasks.

---

## 3. Progressive Disclosure

### 3.1 The Problem

Capability saturation is not only "too many tools exist"; it is also "too many partially relevant capabilities are visible at once." A good runtime should reveal capabilities progressively, matching the task's evolving needs without paying the cost of a new session every time the task descends one more scope level.

Claude Code already has native progressive disclosure mechanisms — Skills for on-demand knowledge, MCP Tool Search for deferred tool loading, and subagents for narrower isolated reasoning. Lenny does not replace those generic mechanisms; it constrains and governs discovery through an enterprise-authored scope tree and compiled execution boundaries. The boundary compiler determines which agents, tools, and skills are even discoverable from a given runtime context, the orchestrator exposes only a bounded initial subset plus child-scope descriptions, and the worker uses explicit inspect/load tools to expand that surface only when needed. The result is not just lower prompt pressure, but a smaller and more coherent decision surface that makes it easier for the worker to choose the right capability.

### 3.2 Scope- and Boundary-Based Discovery

The registry is a recursive tree of scopes. Each scope is a self-contained authoring unit with its own context, skills, commands, agents, and child scopes — mirroring the structure of a Claude Code project.

After overlay merging, the scope tree is compiled into execution boundaries (§5.7). Each execution boundary keeps the scope hierarchy it contains. At runtime, a worker operates within one execution boundary and progressively accumulates a set of **loaded scopes** inside that boundary.

A worker operating within a boundary sees:

1. **Boundary-envelope assets** — the merged policy envelope, boundary metadata, and top-level scope descriptions for scopes compiled into that boundary.
2. **Initially active assets** — context, skills, agents, and MCP façades from the boundary's entry scope or top-level scopes.
3. **Inherited shared assets** — context, skills, commands, and agents that ancestor scopes have explicitly placed in their `shared/` directories, accumulated down the tree subject to inheritance filters.
4. **Child-scope descriptions** — short descriptions of the immediate child scopes that are eligible to be inspected or loaded next.
5. **Child boundary descriptions** — a short description (from each child execution boundary's merged manifests) of what each immediate child boundary handles.

The worker does NOT see the internals of sibling boundaries or any boundary outside its branch of the tree. Inside the current boundary, it sees only descriptions for not-yet-loaded scopes until it explicitly loads them.

**Example: a boundary's view**

```
## Initially Active Scope
- finance

## Loadable Child Scopes
- ap: Invoice lookup, payment drafting, payment submission
- procurement: Vendor verification, compliance checks

## Child Boundaries
- treasury: Bank ops and liquidity controls
```

Scope summaries are enough to route and progressively hydrate. They are not a dump of every tool or prompt in the subtree.

### 3.3 Disclosure Flow

1. Root boundary worker starts with boundary-envelope assets, inherited shared assets, and the entry scope's local assets plus descriptions of its immediate child scopes.
2. Worker calls `scope.inspect` to inspect an immediate child scope in more detail — sees that scope's description, its child scopes, and a summary of the assets that would become visible if loaded.
3. Worker calls `scope.load` to cumulatively load a child scope that is already inside the current execution boundary. The control plane keeps the same lease and session identity, appends the newly loaded context into the session state, and makes that scope's skills, agents, and MCP façades visible for subsequent use.
4. Only if the needed work lives in a different child execution boundary does the worker call `boundary.enter`, which spawns a new Agent SDK session.
5. The process repeats recursively: inspect and load more within the same boundary where useful; spawn a new session only when crossing a compiled boundary edge.

The capability surface grows with the task, but session creation is reserved for genuine isolation boundaries. Each session starts narrow and expands through explicit, auditable load calls that mirror on-demand skill and context loading patterns.

---

## 4. Worker Model

### 4.1 Boundary Workers

Every execution boundary maps to a potential worker session. A boundary may contain one or more compiled scopes and retains the subtree relationships among them. There is no distinction between "planner" and "executor" — every boundary worker has the same structure:

**Model-autonomous capabilities:**

- Skills (on-demand knowledge and workflows) from the current boundary envelope and any scopes already loaded into the session
- Agents (delegated worker contexts) from the current boundary envelope and any scopes already loaded into the session
- MCP tools (external actions) exposed as gateway-backed façades when they are visible through the session's currently loaded scopes

**User-invoked workflows:**

- Commands (slash commands) are entrypoints invoked by the user or client, not by the model autonomously

**Structural context:**

- Boundary-envelope prompt layers
- Initially active scope context plus any additional context loaded later in the session
- Summaries of immediate child scopes eligible for inspection or loading
- Descriptions of immediate child execution boundaries
- Inherited shared assets from ancestors
- Meta-tools for scope inspection, scope loading, boundary navigation, and task reporting

Each active execution boundary runs as its own Agent SDK session. Within a session, the worker can progressively load more of the current boundary and can spawn subagents for narrower work. Only `boundary.enter` can cross into a child execution boundary and start a new isolated session with its own context, tools, and lease.

A boundary can be both a container (has child scopes and child boundaries) and directly executable (has its own skills, agents, and MCP tools). Whether a worker loads more assets within the same boundary or delegates across a boundary depends on the task and the compiled runtime topology.

### 4.2 Worker Lifecycle

Every boundary worker is an Agent SDK session. The control plane creates the session with:

- A system prompt containing the task objective, inherited context, boundary-envelope context, the entry scope's local context, and descriptions of immediate child scopes and child boundaries
- MCP tools visible from the initially active scope set (as gateway-backed façades), plus meta-tools (§6) that route back to the control plane
- A lease (§4.3) that bounds what the worker can see and do

Within the session, the worker can spawn subagents, use skills, and invoke agents. These all operate within the same boundary and lease. Commands remain user-invoked workflows, not model-callable capabilities. The Agent SDK's `canUseTool` callback is where the control plane intercepts every tool call and enforces the lease.

**Note on subagents vs meta-tools:** Subagents spawned within a boundary are intra-boundary — they share the boundary envelope, the session's currently loaded scopes unless narrowed further, and the same lease constraints. Only the main boundary session receives orchestration meta-tools (`scope.inspect`, `scope.load`, `boundary.enter`, `boundary.search`). Subagents do not get orchestration meta-tools; they are worker threads, not orchestration participants.

### 4.3 Leases

A lease is the structural boundary around a worker. Every tool call is checked against the lease before execution.

```
Lease:
  id:                unique identifier
  task_id:           the task this lease belongs to
  parent_task_id:    null for root, parent's task ID for children
  scope:
    boundary_path:        the execution boundary this worker operates in (e.g., "main.finance")
    entry_scope_paths:    the top-level scope path(s) that are active when the session starts
    loadable_scope_paths: descendant scope paths this worker may load inside the boundary (optional; default = all scopes in boundary)
    loaded_scope_paths:   scope paths already loaded into the current session
    max_loaded_scopes:    optional cap on how many additional scopes may be loaded in one session
    max_boundary_hops:    how many child boundaries below boundary_path the worker may enter (optional)
  allowed_tools:     explicit allowlist of tool IDs granted to the lease; omitted means "use boundary defaults", `[]` means "no tools"
  disallowed_tools:  explicit deny list (takes precedence)
  permission_mode:   default | acceptEdits | bypassPermissions | dontAsk
  max_turns:         cap on agent loop iterations
  max_task_depth:    how many levels of child boundary tasks this worker may spawn
  current_depth:     how deep this worker is in the task tree
  budget:
    max_tokens:      token budget
    max_duration_ms: wall-clock timeout
  expires_at:        absolute expiry (≤ parent's expiry)
```

**Key invariants:**

- Every `loaded_scope_path` must stay within the lease's `boundary_path`.
- A worker may load only scopes allowed by `loadable_scope_paths` and only through the control plane's load mechanism.
- A child lease's `boundary_path` must be a descendant boundary of its parent's `boundary_path`.
- Approval authority is not encoded in the lease. Approval is resolved externally by the Approval Service and enforced by action-bound grant tokens at execution time.

### 4.4 Scope Loading and Child Boundary Entry

Workers have two structurally different expansion moves:

**1. Same-boundary scope inspection and loading**

A worker calls `scope.inspect` or `scope.load` to inspect or load a scope that is already inside the current execution boundary. The control plane:

1. Validates the target scope is eligible to be loaded from the session's current loaded-scope frontier.
2. Checks `loadable_scope_paths`, `max_loaded_scopes`, and any scope-level policy constraints.
3. Returns the target scope's description and asset summary for `scope.inspect`, or for `scope.load` appends that scope to `loaded_scope_paths`, hydrates newly visible prompt layers, and registers the scope's skills, agents, and MCP façades for subsequent use.
4. Continues execution in the same Agent SDK session.

**2. Cross-boundary entry**

A worker calls `boundary.enter` to delegate work to an immediate child execution boundary. The control plane:

1. Validates the target boundary is an immediate child of the current boundary path.
2. Checks depth and budget limits.
3. Issues a child lease with `current_depth = parent.current_depth + 1` and `boundary_path` set to the child boundary.
4. Starts a new Agent SDK session with a system prompt built from the child boundary's initial view.
5. The child runs to completion and returns structured output.

**Note:** Subagents spawned _within_ a boundary are distinct from `boundary.enter`. In-boundary subagents share the boundary envelope and lease; `boundary.enter` crosses into a _different execution boundary_, which creates a new isolated Agent SDK session with different context, tools, and lease.

**Default limits (adjustable per deployment):**

- Max loaded scopes within a boundary: configurable per boundary
- Max delegation depth across boundaries: configurable (default: 5)
- Budget and turn limits inherited from parent, with decrements

### 4.5 Parent–Child Communication

Structured, not freeform. A child request contains:

- Objective
- Current facts
- Constraints

A child response returns:

- Findings
- Recommended next actions
- Required additional scopes (outside current boundary)
- Risks / policy flags
- Confidence
- Artifacts / evidence references

### 4.6 Execution Ladder

Workers have a spectrum of execution strategies, from lightest to heaviest:

1. **Initial-scope continuation.** The worker handles the task directly within the assets visible at session start.
2. **Same-boundary scope loading.** The worker calls `scope.inspect` or `scope.load` to inspect or cumulatively load a child scope inside the current boundary without creating a new session.
3. **Subagent delegation.** The worker spawns an intra-boundary subagent for a focused subtask. The subagent shares the boundary envelope and lease, optionally with a narrower prompt/tool subset.
4. **New boundary session.** The worker calls `boundary.enter` to delegate to a child execution boundary, spawning a new Agent SDK session with its own context, tools, and lease.
5. **Lightweight one-shot.** For simple routing decisions, the control plane can use a minimal prompt or metadata-only search without full boundary hydration.

The worker chooses the next execution strategy. The control plane exposes the allowed mechanisms and enforces lease, policy, and approval constraints on the worker's choice.

---

## 5. Scope Registry

### 5.1 Physical Structure

The registry is authored as a recursive scope tree and compiled into an index at startup. The control plane accepts an ordered list of registry roots (overlays). The first registry is the base; subsequent registries layer additional assets on top. Each overlay follows the same directory structure. At compile time, overlays are merged scope-by-scope: if the same scope path exists in multiple overlays, their assets (skills, commands, agents, context, shared assets, MCP servers, external agents) are combined. A skill, command, or agent with the same name in the same scope across overlays is a compile-time error. The `description` field uses last-overlay-wins. Security-sensitive manifest fields (`sandbox`, `policy`, `permissions`) use most-restrictive-wins: the compiler takes the most restrictive value from any overlay. If one overlay sets `requiresApproval: true`, the result is `true`. If one overlay denies a writable path or egress destination, the result stays denied. Allowlists (like `allowedTools`, `writablePaths`, `egressAllowlist`) are intersected; denylists (like `disallowedTools`, `deniedPaths`) are unioned. Overlays can add new child scopes but cannot remove scopes defined by earlier overlays.

After overlay merging, the merged scope tree is passed to the **Boundary Compiler** (§5.7), which produces execution boundaries — the runtime units that map to Agent SDK sessions — and preserves the internal scope hierarchy inside each boundary envelope.

Each scope is a directory that mirrors a Claude Code project structure: `context.md` for instructions, `manifest.yaml` for configuration, `skills/`, `commands/`, `agents/` for assets, `shared/` for assets inherited by children, and `scopes/` for child scopes.

```
capability-registry/
├── registry.yaml                    # version, name, defaults
└── main/
    ├── context.md                   # Instructions for the root scope
    ├── manifest.yaml                # Root scope configuration
    ├── skills/                      # Model-autonomous knowledge/workflows
    ├── commands/                    # User-invoked workflows (slash commands)
    ├── agents/                      # Delegated worker contexts
    ├── shared/                      # Assets inherited by ALL descendant scopes
    │   ├── context.md               # Global guidelines
    │   ├── skills/
    │   ├── commands/
    │   └── agents/
    └── scopes/
        ├── finance/
        │   ├── context.md
        │   ├── manifest.yaml
        │   ├── skills/
        │   ├── shared/              # Inherited by finance's children
        │   │   └── skills/
        │   └── scopes/
        │       ├── ap/
        │       │   ├── context.md
        │       │   ├── manifest.yaml  # mcpServers, sandbox, policy
        │       │   ├── skills/
        │       │   └── scopes/
        │       │       ├── invoices/
        │       │       │   ├── manifest.yaml
        │       │       │   └── ...
        │       │       └── payments/
        │       │           ├── manifest.yaml
        │       │           └── ...
        │       └── treasury/
        │           └── ...
        └── engineering/
            └── ...
```

The tree can nest to arbitrary depth. A scope at any level can have its own executable assets (skills, commands, agents, MCP tools) AND child scopes — there is no distinction between "container" and "leaf" scopes.

## Overlay example

Base registry (company-wide):

```
company-registry/
├── registry.yaml
└── main/
    └── scopes/
        └── engineering/
            ├── manifest.yaml
            ├── skills/
            │   └── deploy.md
            └── scopes/
                └── platform/
                    └── manifest.yaml
```

Team overlay:

```
team-platform-overlay/
├── registry.yaml
└── main/
    └── scopes/
        └── engineering/
            └── scopes/
                └── platform/
                    ├── skills/
                    │   └── canary-deploy.md    # new skill added to platform scope
                    ├── agents/
                    │   └── oncall-helper.md    # new agent added to platform scope
                    └── context.md              # additional context appended
```

After merging, `main.engineering.platform` has both `deploy` (from base) and `canary-deploy` + `oncall-helper` (from overlay). If the overlay also defined a skill named `deploy`, compilation would fail.

### 5.2 Scope Manifest Schema

Each scope has a `manifest.yaml` that configures the runtime envelope for that scope and contributes to the compiled boundary:

```yaml
description: "Accounts payable — invoice lookup, payment drafting, payment submission"

# MCP tool servers available to this scope when it is loaded.
# Note: credentials are resolved at runtime by the credential broker,
# not injected as static env vars. The names below are credential references.
mcpServers:
  - name: finance-ap-tools
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-ap-mcp"]
    credentials:
      DATABASE_URL: "credential://ap-database-url"
    lifecycle: pooled
    mutatingTools:
      - payment_submit

# External A2A-compliant agents accessible from this scope
externalAgents:
  - name: vendor-verification
    description: "Verifies vendor compliance and banking details"
    a2aCardUrl: "https://agents.example.com/vendor-verify/.well-known/agent.json"
    authRef: "credential://vendor-verify-oauth"
    allowedDomains: ["agents.example.com"]
    maxDataClass: internal
    timeoutMs: 10000
    maxRetries: 1

# Sandboxing constraints for this scope's session
sandbox:
  workspaceRoots:
    - "/workspace/finance"
  readOnlyPaths:
    - "/workspace/shared/**"
  writablePaths:
    - "/workspace/finance/**"
  deniedPaths:
    - "/workspace/finance/.env"
  egressAllowlist:
    - "tool-gateway.internal"
    - "registry.npmjs.org"
  followSymlinks: false
  maxDurationMs: 300000
  permissionMode: acceptEdits

# Policy annotations
policy:
  risk: medium
  dataClass: internal
  requiresApproval: false

# Permissions for the session
permissions:
  # Omit allowedTools to inherit all tools visible through loaded scopes.
  # [] means allow none.
  allowedTools:
    - "mcp.finance-ap-tools.*"
  disallowedTools: []
  permissionMode: default

# Boundary compilation hints (optional)
boundary:
  mergeWithSiblings: [] # immediate sibling scope paths under the same parent only
  mergeWithChildren: none # none | all
  forceSplit: false # keep this scope in its own boundary
  neverCoReside: [] # scope paths that must never share a boundary with this scope

# Inheritance filters — control which shared assets from ancestors this scope receives
# By default, all ancestor shared assets are accumulated and evaluated at each hop.
# Use whitelist or blacklist to filter.
inherit:
  context:
    blacklist: ["legacy-guidelines"]
  skills:
    whitelist: ["code-review", "testing"]
  commands: {} # accept all inherited commands
  agents:
    blacklist: ["deprecated-agent"]
```

### 5.3 Shared Assets and Inheritance

Each scope can place assets in its `shared/` directory to make them available to all descendant scopes:

- `shared/context.md` — context instructions inherited by children
- `shared/skills/` — model-autonomous knowledge/workflows inherited by children
- `shared/commands/` — user-invoked workflows inherited by children
- `shared/agents/` — delegated worker contexts inherited by children

**Inheritance rules:**

1. **Accumulated by default.** A scope sees shared assets from ALL ancestors, accumulated top-down. If `main` shares a skill and `main.finance` shares another skill, then `main.finance.ap` sees both.
2. **Filterable per scope.** A scope's manifest can declare `inherit` filters to whitelist or blacklist specific inherited assets. A whitelist takes precedence over a blacklist if both are set.
3. **Computed from the ancestor chain, not re-exported hop-by-hop.** The runtime computes a descendant's inherited view by walking the ancestor chain in order, adding each ancestor's shared assets and applying each intermediate scope's filters to the cumulative set. If scope B filters out an inherited asset, B and B's descendants stop seeing it unless a lower scope introduces a new asset with a different identity.
4. **Overlays accumulate.** Shared assets from overlays are combined with the base registry's shared assets at each scope level. The same name-collision rule applies: a shared skill, command, or agent with the same name in the same scope across overlays is a compile-time error.

### 5.4 Scripts in Skills

Skills can include executable scripts (shell scripts, TypeScript files, etc.) as part of the registry. These scripts are executed within the sandbox constraints defined in the scope's manifest. The `sandbox` field in the manifest controls:

- Workspace roots plus read-only and writable filesystem areas
- Explicit egress allowlist
- Symlink behavior and path-safety rules
- Duration limits
- Permission mode

### 5.5 Runtime Compilation

At startup, the registry loader processes each registry root in overlay order. For each registry, it recursively walks the scope tree. The compiler then merges the trees:

1. Walk the base registry to produce the initial `ScopeTree`.
2. For each subsequent overlay, walk its tree and merge into the base:
   - If a scope path exists in both, combine their assets (skills, commands, agents, context, shared assets). Error on name collisions.
   - If a scope path exists only in the overlay, graft it into the tree at the appropriate parent.
   - For `description`, last-overlay-wins. For `sandbox`, `policy`, and `permissions`, most-restrictive-wins (see §5.1 text above).
3. Produce the final merged `ScopeTree` with root `ScopeNode` and all descendants, plus a `byPath` map for O(1) lookup.
4. Compile each scope's prompt layers, inherited shared-asset view, and effective policy envelope.
5. **Boundary formation.** Pass the merged scope tree to the Boundary Compiler (§5.7), which produces the `ExecutionBoundary[]` tree, each boundary's internal scope hierarchy, and a `boundaryByScope` map.

Context files (`context.md`) from overlays are not blindly concatenated into one opaque blob. They are compiled into an ordered prompt layer set with stable delimiters and contradiction linting so the resulting session prompt is explainable and reviewable.

The resolver navigates the compiled boundary tree, filtered by lease boundary path and the session's loaded scope state. Workers see the combined view — they have no awareness of which registry contributed which assets or which scopes were merged into their boundary.

### 5.6 Overlay Configuration

The control plane accepts an ordered list of registry roots:

```yaml
registries:
  - path: ./company-registry # base
  - path: ./team-platform-overlay # overlay 1
  - path: ./personal-overlay # overlay 2
```

Each entry points to a directory following the standard registry structure (with a `registry.yaml` and a root scope folder). Overlays are applied left-to-right. Typical layering:

- **Base:** Organization-wide scopes, policies, shared context.
- **Team overlay:** Team-specific skills, agents, and scope extensions.
- **Personal overlay:** Individual developer customizations.

The compile-time name-collision check ensures overlays cannot silently shadow each other's assets. To intentionally replace a skill, the base must remove it first (or the skill must be renamed).

### 5.7 Boundary Compiler

The boundary compiler transforms the merged scope tree into execution boundaries — the runtime envelopes that map to Agent SDK sessions while retaining internal scope hierarchy.

**Inputs:**

- Merged scope tree (output of overlay compilation, §5.5 steps 1–4)
- Policy/security metadata per scope (data classification, credential domains, permission modes, sandbox requirements)
- Boundary hints from manifests and overlays (`mergeWithSiblings`, `mergeWithChildren`, `forceSplit`, `neverCoReside`)
- Hard configuration limits (max scopes per boundary, max tools per boundary, etc.)

**Merge topology rules (hard constraints):**

- Scopes cannot be split across boundaries — a scope belongs to exactly one boundary.
- A boundary is either: one scope, a merge of sibling scopes, or a merge of a parent scope with ALL its immediate children.
- Incompatible scopes must remain in separate boundaries:
  - Different data classifications (e.g., `public` vs `confidential`)
  - Incompatible credential domains
  - Incompatible sandbox/egress requirements
  - Incompatible permission modes that the SDK cannot safely co-host

**Operational hard limits:**

- Max scopes per boundary (configurable, e.g., 5)
- Max estimated tools per boundary
- Max estimated skill metadata per boundary
- Max expected working-set size

**Semantic optimization (heuristics):**

- Overlap in artifacts, instructions, tools, and skills
- Common task co-occurrence patterns
- Overlapping tool names/descriptions
- Ownership similarity

**Human/manifest overrides:**

- `mergeWithSiblings`: requests a legal sibling merge shape only
- `mergeWithChildren: all`: requests the legal parent-plus-all-immediate-children shape
- `forceSplit`: keep scopes in separate boundaries even if merge is permitted
- `neverCoReside`: specific scopes that must never share a boundary

Invalid merge hints are compile-time errors. Authors cannot request arbitrary merge graphs that the compiler is not allowed to build.

**Overlay precedence for boundary formation:**

- Security/policy: most-restrictive-wins (consistent with §5.1)
- Boundary hints: later overlays can add constraints but cannot loosen earlier ones
- `forceSplit` overrides `mergeWithSiblings` and `mergeWithChildren` if they conflict

**Evaluation order:**

1. Apply security/policy hard constraints → identify forced separations.
2. Apply operational hard limits → cap boundary size.
3. Apply human/manifest overrides → honor `mergeWithSiblings`, `mergeWithChildren`, `forceSplit`, `neverCoReside`.
4. Apply semantic optimization → merge remaining candidates where beneficial.
5. Validate: every scope belongs to exactly one boundary; every requested merge shape is legal; boundary tree is valid.

**Compiler output:**

- `ExecutionBoundary[]` — the compiled boundary tree
- `boundaryByScope: Map<scope_path, boundary_id>` — O(1) scope→boundary lookup
- Per-boundary: merged manifest, internal scope hierarchy, entry scope set, and progressive loading metadata
- `explanations: Map<boundary_id, string[]>` — why scopes were combined or separated (auditability)

**Properties:**

- **Deterministic:** same inputs always produce the same boundary tree
- **Explainable:** every merge/split decision has a logged reason
- **Compiled but runtime-adaptive:** topology is fixed at startup, while scope-loading and tool-hydration decisions happen at runtime inside each boundary
- **Testable:** boundary formation can be unit-tested with fixture registries

---

## 6. Meta-Tools

Workers interact with the control plane through a small, fixed set of meta-tools. These are registered alongside the boundary's MCP tools (gateway-backed façades from its merged manifest) into each Agent SDK session.

| Tool               | Description                                                                                                                        | Available to               |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------- | -------------------------- |
| `scope.inspect`    | Inspect a loadable scope inside the current execution boundary. Returns description, child scopes, asset summary, and loadability. | Main boundary session only |
| `scope.load`       | Cumulatively load a scope inside the current execution boundary and make its local assets visible in the same session.             | Main boundary session only |
| `boundary.enter`   | Delegate work to an immediate child execution boundary by spawning a new agent session with an objective.                          | Main boundary session only |
| `boundary.search`  | Search visible scopes and child boundaries by natural language query without dumping all metadata into the prompt.                 | Main boundary session only |
| `approval.request` | Pre-request HITL approval for a planned action within the current boundary.                                                        | All workers                |
| `task.update`      | Report structured findings/artifacts to the parent.                                                                                | All workers                |

**Note:** Only the main boundary session receives orchestration meta-tools (`scope.inspect`, `scope.load`, `boundary.enter`, `boundary.search`). In-boundary subagents do not — they are worker threads scoped to focused subtasks, not orchestration participants.

Each boundary session also has access to the MCP tools visible through its currently loaded scopes, exposed as gateway-backed façades. The gateway mediates all sensitive actions (approval gating, credential injection, idempotency, audit logging) transparently.

This design avoids tool explosion. Each session sees a compiled boundary envelope, but only the tools relevant to the session's currently loaded scopes need to be hydrated into the working set at any moment.

**Load contract:** `scope.inspect` is cheap metadata discovery. `scope.load` is the auditable expansion step. A successful load returns the scope description, child-scope descriptions, asset summaries, and a list of newly visible context layers, skills, agents, and MCP tool façades. Those assets remain visible for the rest of the session unless the worker crosses into a new execution boundary.

---

## 7. Request Lifecycle

End-to-end flow for a user request:

**Step 1 — Session API receives request.**
Authenticates the caller. Attaches actor identity, roles, scopes, tenant. Creates a root Task record with a trace ID.

**Step 2 — Orchestrator hydrates root execution boundary.**
Selects the root execution boundary and entry scope set. Builds a boundary view containing boundary-envelope context, inherited shared assets, initially active scope assets, and summaries of immediate child scopes and child boundaries.

**Step 3 — Root lease issued.**
Anchored to the root boundary path and initial loaded scope set. Default budget and depth limits apply.

**Step 4 — Root boundary worker starts.**
Agent SDK session created with a system prompt containing the objective, boundary-envelope context, initially active scope context, and visible scope/boundary summaries. Gateway-backed MCP façades visible through the initial loaded scopes + meta-tools are registered.

**Step 5 — Root worker reasons.**
Uses `boundary.search`, `scope.inspect`, native skill loading, and tool search to decide whether the task can stay inside the current boundary or needs a different boundary.

**Step 6 — Same-boundary load or cross-boundary delegation.**
If the needed work is inside the current boundary, the worker calls `scope.load` and continues in the same session with a larger visible asset set. If the work belongs in a child execution boundary, the worker calls `boundary.enter` to delegate.

**Step 7 — Side-effect attempt prepared.**
Before a mutating MCP call, the worker provides an `operation_id` / idempotency key. The gateway checks lease, permissions, policy, and whether approval is required for this exact action.

**Step 8 — Approval surfaced to UI.**
If approval is required, the Session API streams an `approval.requested` event to the consuming client. The client renders an approval prompt showing the exact action, arguments, risk level, rollback information, and the operation identifier.

**Step 9 — User approves.**
The client calls the Session API's resolve-approval endpoint. The approval service generates a one-time grant token.

**Step 10 — Execution with grant.**
Worker retries the exact same operation with the grant token. The gateway validates the grant (one-time, action-bound, not expired), checks idempotency state, injects scoped credentials, executes, durably records the outcome, and returns the result.

**Step 11 — Recursive continuation (if needed).**
As the task evolves, the worker may load more scopes within the same boundary, or delegate to a child boundary. Each boundary hop creates a new lease and session; same-boundary load calls do not.

**Step 12 — Completion.**
Root returns the final result. The Session API streams a `done` event. The audit trail includes scope inspections, scope loads, boundary entries, approvals, and any replayed or deduplicated mutations.

---

## 8. Security Model

### 8.1 Threat Model

The primary threat: **the model attempts actions outside its authorized scope**, whether due to prompt injection, hallucination, or adversarial input. The secondary threat: **credential exposure** from the runtime to external systems.

### 8.2 Mandatory Design Rules

**1. Forced mediation.** Workers never talk directly to protected systems. All capability invocations go through the Tool Gateway. MCP tools are exposed to worker sessions as gateway-backed façades — the session invokes a tool definition that routes through the gateway, which manages the actual MCP server connection, credential injection, and audit logging. Even if a worker attempts a direct network call, sandbox network policies block it. Raw MCP endpoints are never directly exposed to worker sessions.

**2. No standing credentials.** Workers and sandboxes never hold durable secrets. The credential broker issues ephemeral, scoped capabilities per invocation. The gateway injects them at dispatch time and never returns them to the worker. MCP server processes that require credentials (e.g., `DATABASE_URL`) receive them through the credential broker/proxy at invocation time — not through static environment variable injection in the manifest. Manifest `credentials` fields are references (e.g., `credential://ap-database-url`), not values.

**3. Approval is cryptographic, not advisory.** Approval is not "Claude says the user agreed." It is a signed, action-bound grant token with an expiry, tied to an exact capability + arguments + requester identity + root task ID. The gateway validates the token before execution.

**4. Least privilege per action.** Each invocation gets the smallest scope, shortest lifetime, and narrowest resource access possible.

**5. Mutations are idempotent or rejected.** Any external write must include an operation identifier that the gateway can use for deduplication, replay detection, and crash recovery. If a downstream integration cannot support safe retry semantics, the mutation is not eligible for autonomous retry.

**6. Default deny.** If a capability is not in the lease scope, if a tool is not visible through the session's currently loaded scopes, if a policy rule doesn't explicitly allow it, or if the environment doesn't permit it — execution fails closed.

**7. Verification before trust.** For code/data changes: run tests, validate schemas, verify diffs, compare against acceptance criteria. The gateway can run validators defined in the scope manifest before returning success.

**8. Lease-scoped access.** Every `canUseTool` callback in the Agent SDK checks the worker's lease. A child cannot see or access boundaries outside its lease's boundary path, regardless of what it "knows."

### 8.3 Sandbox Isolation

For boundary workers where the manifest defines sandbox constraints:

- Ephemeral container per worker or per job
- No default credentials
- Network egress allowlist enforced at the sandbox boundary; no ambient outbound internet
- Filesystem mounts computed deterministically from merged manifests:
  - `workspaceRoots` are unioned only when all candidate scopes are allowed to co-reside
  - `readOnlyPaths` are unioned
  - `writablePaths` are intersected
  - `deniedPaths` are unioned
  - `followSymlinks: false` is sticky
- Runtime limits: max CPU, memory, wall-clock timeout
- For code changes: prefer the PR workflow (generate diff → human review → merge)

---

## 9. Policy Engine

### 9.1 Evaluation Model

The policy engine is called by the Tool Gateway on sensitive actions. It receives a `PolicyContext`:

```
PolicyContext:
  actor:        { id, roles, scopes, tenant_id }
  scopeNode:    the boundary's merged manifest, loaded scope set, and metadata
  toolName:     the specific MCP tool being invoked (optional)
  environment:  dev | staging | prod
  lease:        the worker's lease
  args:         the invocation arguments (optional, for fine-grained rules)
  operationId:  mutation identifier for replay/deduplication (optional)
```

It returns one of:

- `{ allowed: true }`
- `{ allowed: false, reason: "..." }`
- `{ requires_approval: true, reason: "..." }`

### 9.2 Evaluation Order

1. **Lease boundary check.** Is the action within the lease's boundary path and either already visible through the loaded scope set or explicitly eligible for a `scope.inspect` / `scope.load` call? If not → denied.
2. **Lease expiry check.** Has the lease expired? If so → denied.
3. **Lease/tool allowlist check.** Is the tool in the lease allowlist and not in the deny list? If not → denied.
4. **Manifest visibility check.** Is the tool visible through the currently loaded scopes and permitted by the merged manifest permissions? If not → denied.
5. **Policy rules.** Evaluate authored rules in priority order. Each rule has conditions (roles, scope paths, environments, risk levels, data classes) and an effect (allow, deny, require_approval).
6. **Manifest approval check.** If the manifest says `policy.requiresApproval: true` → approval required.
7. **Actor scope check.** Does the actor have the necessary permissions for this scope?
8. **Default deny.**

### 9.3 Policy Rules

Policy rules are authored in the registry (e.g., in the root scope's `shared/` or a dedicated policies scope) and can be overlaid per environment. Example:

```yaml
- id: deny-prod-writes-for-operators
  description: Operators cannot perform writes in production
  effect: deny
  conditions:
    roles: [operator]
    environments: [prod]
    scopePaths: ["main.finance.*", "main.engineering.*"]
    risk_levels: [medium, high]
  priority: 100

- id: require-approval-for-irreversible
  description: All irreversible writes require human approval
  effect: require_approval
  conditions:
    risk_levels: [high]
  priority: 90
```

---

## 10. Approval System

### 10.1 Lifecycle

1. **Gateway determines approval is required** (from policy or manifest).
2. **Approval request created**: bound to exact boundary path + source scope path + action + arguments + operation ID + risk level + expiry.
3. **Event streamed to client**: `approval.requested` with summary, risk, rollback info.
4. **User resolves**: approved or denied, with their identity.
5. **Grant token generated**: one-time, action-bound. Stored in the approval record.
6. **Worker retries invocation** with the grant token.
7. **Gateway validates**: token matches the approval record, not expired, not already used, and still matches the same operation ID and arguments.
8. **Audit logged**: who approved, what was approved, when, with what arguments and operation ID.

### 10.2 Key Properties

- **Action-bound.** A grant for a specific action in a specific boundary cannot be reused for a different action or boundary.
- **One-time.** Each grant token is consumed on use.
- **Expiring.** Grants have a TTL (default: 10 minutes).
- **Non-inheritable.** A child worker cannot use a grant issued to its parent (unless the wrapper explicitly binds it).
- **Human-resolved.** Approval authority lives outside the worker. There is no structural "self/parent/root" approval mode inside leases; workers request, humans resolve, gateways enforce.

### 10.3 Mutating Action Contract

Every mutating tool invocation must satisfy the same runtime contract:

1. The worker supplies a stable `operation_id`.
2. The gateway computes or validates an idempotency key derived from boundary path, tool, normalized arguments, and `operation_id`.
3. The gateway writes an intent record before dispatching the external mutation.
4. The downstream executor forwards the idempotency key when the integration supports it; otherwise the gateway records that the operation is not safely retryable.
5. On retry or resume, the gateway first checks whether the operation already succeeded, failed definitively, or is still in-flight before dispatching again.
6. Approval-bound retries must reuse the same `operation_id`; approving a new mutation requires a new approval record.

---

## 11. External Integration

### 11.1 MCP Tools

External systems are exposed to Lenny as MCP servers configured in scope manifests. At runtime, each boundary session accesses MCP tools through gateway-backed façades — the worker invokes a tool definition that the gateway proxies to the actual MCP server, mediating credential injection, policy enforcement, idempotency, and audit logging. Raw MCP endpoints are never directly exposed to worker sessions.

**MCP runtime contract:**

1. **Process ownership.** MCP server processes are owned by the control plane or sandbox runner, never by the worker prompt.
2. **Lifecycle.** Each server is either short-lived per invocation, pooled per tenant/environment, or isolated per boundary, as declared by infrastructure policy. The session only sees the façade, not the server handle.
3. **Transport proxying.** The gateway terminates the worker-facing tool call and bridges it to the server's stdio/HTTP/SSE transport. This is the enforcement point for policy, schema validation, and audit.
4. **Credential handoff.** The credential broker issues per-call scoped credentials. They are attached to the downstream request or temporary process environment only for the lifetime of the call and are scrubbed immediately afterward.
5. **Tool visibility.** A tool is callable only if it is present in the merged manifest, visible through the session's currently loaded scopes, allowed by the lease, and admitted by policy.
6. **Mutation handling.** Mutating MCP tools must participate in the mutating action contract (§10.3).

### 11.2 A2A Agents

External A2A-compliant agents are configured as `externalAgents` in scope manifests with an `a2aCardUrl`. The A2A adapter:

1. Fetches and caches the remote agent's Agent Card.
2. Validates the target domain against the manifest allowlist and deployment trust policy.
3. Maps the scope's external agent config to A2A task format, applying data minimization and redaction rules before dispatch.
4. Authenticates using the configured `authRef`.
5. Dispatches tasks via HTTP with bounded timeout/retry policy.
6. Streams results back through the standard event pipeline.

The adapter supports both synchronous (`/tasks/send`) and streaming (`/tasks/sendSubscribe` via SSE) A2A interactions.

**Outbound trust requirements:**

- External calls must be domain-allowlisted.
- The outbound payload's highest data classification must not exceed the agent's declared `maxDataClass`.
- Tenant identity and trace IDs must be preserved end-to-end.
- Timeout, retry, and circuit-breaker policy must be explicit.
- Every outbound request and inbound result is audited.

### 11.3 Client Integration

Lenny exposes a REST + SSE API. Consuming clients connect to:

- `POST /sessions` — start a task
- `GET /sessions/:id/events` — SSE stream of session events
- `POST /sessions/:id/approvals/:approvalId/resolve` — approve or deny
- `GET /sessions/:id/tasks` — get the task tree
- `GET /sessions/:id/approvals` — list pending approvals

This is sufficient for LibreChat (which supports SSE), Slack bots (which poll or use webhooks), and custom UIs.

---

## 12. Execution Modes

### 12.1 Synchronous (Chat Turn)

For short tasks that complete within a single user interaction:

- Bounded agent loop (configurable max turns, default 50)
- Deterministic stop conditions
- Immediate response streamed to the client

### 12.2 Asynchronous (Workflow / Job)

For tasks that involve waiting (approvals, CI pipelines, external processes), retries, or long runtimes:

- The orchestrator hands off to the workflow engine.
- Claude acts as a decision function within workflow steps, not as a free-running daemon.
- The workflow engine manages pause/resume/retry/timeout.
- Results are streamed to the client as they complete.

The workflow engine interface is pluggable. The default is a minimal in-memory state machine. Production deployments should use Temporal, BullMQ, or equivalent.

### 12.3 Lenny Sessions

A Lenny session is a durable conversation layer above Claude SDK sessions. While individual Claude sessions are bounded by context limits and may be spun down to conserve resources, the Lenny session persists across SDK session lifetimes.

**Persisted state:**

- Main Claude session ID (current or last active)
- Active boundary session IDs and their states
- Loaded scope paths per boundary session
- Compacted conversation state (structured summaries, not raw transcripts)
- Structured findings from completed child boundary sessions
- Pending approvals
- Mutation intent / completion records needed for safe resume
- Expiry metadata

**Hybrid lifecycle pattern:**

1. **Hydrate** — restore session state, reconnect or create Claude SDK sessions as needed.
2. **Work** — execute within active boundary sessions.
3. **Spin down** — release idle Claude SDK sessions to conserve resources. Lenny session state is preserved.
4. **Resume** — re-hydrate when new input arrives or an async event (approval, CI result) triggers continuation.

**Configuration:**

- Configurable idle timeout per boundary session (default: 15 minutes)
- Session expiration (default: 24 hours, configurable per deployment)
- Shared storage mount as optional resume strategy; app-level structured state as portable fallback

---

## 13. Audit and Observability

### 13.1 What Gets Logged

Every significant event, each carrying a trace ID:

| Event                | When                                             |
| -------------------- | ------------------------------------------------ |
| `task.created`       | Root or child task created                       |
| `task.updated`       | Task status or output changed                    |
| `lease.issued`       | New lease created                                |
| `lease.expired`      | Lease expired or revoked                         |
| `worker.started`     | Agent SDK session started                        |
| `worker.stopped`     | Session completed, failed, or cancelled          |
| `tool.requested`     | Worker attempted a tool call                     |
| `tool.allowed`       | Policy allowed the call                          |
| `tool.denied`        | Policy denied the call                           |
| `tool.executed`      | Tool call succeeded                              |
| `tool.failed`        | Tool call errored                                |
| `scope.loaded`       | Worker loaded a child scope within a boundary    |
| `mutation.recorded`  | Mutating operation intent or completion recorded |
| `mutation.replayed`  | Retry reused prior mutation state                |
| `approval.requested` | Approval prompt created                          |
| `approval.resolved`  | User approved or denied                          |
| `boundary.entered`   | Worker spawned agent session in a child boundary |
| `scope.inspected`    | Worker inspected a child scope's description     |
| `policy.evaluated`   | Policy engine made a decision                    |

### 13.2 PII Redaction

Scope manifests or policy rules can specify fields that should be redacted in audit logs (e.g., `bank_account`, `ssn`).

### 13.3 Pluggable Sink

The audit sink is an interface. The default writes to the storage adapter. Users can plug in external SIEM, log aggregation, or compliance systems.

---

## 14. Pluggable Interfaces

Every major subsystem boundary is a pluggable interface:

| Interface                | Default                                  | Purpose                                      |
| ------------------------ | ---------------------------------------- | -------------------------------------------- |
| `StorageAdapter`         | PostgreSQL (in-memory for dev)           | Tasks, leases, approvals, audit records      |
| `PolicyEngine`           | Rule-based RBAC/ABAC                     | Policy evaluation                            |
| `RegistrySearchProvider` | In-memory text match (pgvector for prod) | Semantic + metadata search over scopes       |
| `WorkflowEngine`         | In-memory state machine                  | Async job lifecycle                          |
| `AuditSink`              | Storage adapter                          | Event logging                                |
| `CredentialBroker`       | No-op                                    | Ephemeral credential issuance                |
| `A2AAdapter`             | HTTP-based                               | External agent communication                 |
| `ToolExecutor`           | Pass-through                             | Dispatches to MCP/A2A/workflow               |
| `BoundaryCompiler`       | Default rule-based compiler              | Compile scope tree into execution boundaries |
| `SessionStore`           | In-memory (PostgreSQL for prod)          | Durable Lenny session state                  |
| `WorkerAllocator`        | One-active-session-per-boundary          | Boundary worker allocation strategy          |
| `IdleTimeoutPolicy`      | Fixed timeout (15 min)                   | Configurable session deallocation            |
| `CostGovernor`           | No-op                                    | Per-tenant token/concurrency budgets         |

---

## 15. End-to-End Examples

### 15.1 Read-Only Knowledge Query

> User: "What's the parental leave policy for employees in Canada?"

1. Root sees the child execution boundary `hr` and calls `boundary.enter("hr", objective: "Find parental leave policy for Canada")`.
2. The `hr` session starts with `hr` assets active and sees child scope summaries including `benefits`.
3. It calls `scope.inspect("benefits")`, sees that the scope contains policy-search assets, then calls `scope.load("benefits")`.
4. The gateway appends the `hr.benefits` context layer and makes the policy-search MCP facade visible inside the same session.
5. The worker uses the policy-search tool directly → returns the policy document.
6. The `hr` session returns findings to root.
7. Root summarizes with citations.

The task crossed one execution boundary but did not need a second session for `benefits`; the worker simply loaded that scope's assets into the existing `hr` session.

### 15.2 Financial Write with Approval

> User: "Pay invoice INV-9831 from ACME for $50,000."

1. Root sees `finance` child boundary. Calls `boundary.enter("finance", objective: "Pay invoice INV-9831")`.
2. The `finance` session starts with `finance` assets active, inspects `ap`, and then loads `ap`.
3. The gateway makes invoice and payment MCP tools visible through the newly loaded `finance.ap` scope.
4. The worker uses invoice lookup → returns invoice details.
5. The worker uses payment draft → returns `draft_id`.
6. The worker calls payment submit with `operation_id=pay-invoice-inv-9831-v1`. The manifest/policy requires approval.
7. Gateway intercepts → returns `approval_required` with summary: "Submit payment $50,000 to ACME".
8. User approves in UI.
9. Worker retries the same operation with the grant token → gateway validates token + idempotency key → payment submitted.
10. Full audit trail: who requested, who approved, what arguments, which boundary, which operation ID.

### 15.3 Task That Grows Mid-Execution

> User: "Pay invoice INV-9831."

Same as above, but at step 3, invoice lookup reveals the vendor has an incomplete compliance record.

4. The finance worker sees it needs vendor verification. It first checks whether `procurement` is another scope in the same boundary.
5. In this deployment, `procurement` is a separate child execution boundary because it has a different credential domain and stricter egress policy.
6. The finance worker calls `boundary.enter("procurement", objective: "Check vendor compliance status for ACME")`.
7. Procurement session investigates → returns: "Vendor W-9 unverified. Banking details incomplete. Recommend: block payment, notify procurement."
8. Parent finance session synthesizes findings and asks the user what to do.

The task adapted in two stages: inspect and load within the current boundary first, then spawn a new session only when a real compiled boundary edge was crossed.

### 15.4 Engineering Workflow

> User: "Add rate limiting to /v1/export, update tests, open a PR."

1. Root sees `engineering` child boundary. Calls `boundary.enter("engineering", objective: "Add rate limiting to /v1/export, update tests, open a PR")`.
2. The engineering session starts in an ephemeral container, inspects `platform`, then loads it and hydrates repo-editing tools visible to that scope.
3. Gateway injects a short-lived Git token, enforces sandbox filesystem and egress policy, and records mutation intents for repo writes and PR creation.
4. The worker makes changes, runs tests, and iterates within the boundary's turn budget.
5. PR creation requires approval per policy. Gateway returns `approval_required`.
6. User approves. Worker retries the same PR-creation operation ID. PR created.
7. Root returns the PR link, change summary, and test results.

---

## 16. MVP Build Sequence

| Phase | What                                        | Why first                                      |
| ----- | ------------------------------------------- | ---------------------------------------------- |
| 1     | Core types and pluggable interfaces         | Everything depends on these contracts          |
| 2     | Scope tree loader and compiler              | Progressive disclosure is the foundation       |
| 2.5   | Boundary compiler                           | Execution boundaries must exist before runtime |
| 3     | Policy engine + lease management            | Security must be structural from day one       |
| 4     | Tool Gateway                                | The single enforcement point                   |
| 5     | Agent Runtime Manager + boundary meta-tools | The Agent SDK integration                      |
| 6     | Orchestrator                                | The control-plane loop connecting everything   |
| 7     | Approval flow                               | HITL for side effects                          |
| 8     | A2A adapter                                 | External agent support (MVP requirement)       |
| 9     | Audit logger                                | Observability                                  |
| 10    | Session API (REST + SSE)                    | External client connectivity                   |
| 11    | Example scope registry                      | Prove end-to-end                               |

---

## 17. Verification

- **Unit tests** for each module: scope tree loader, boundary compiler, policy engine, lease validation, gateway, approval service.
- **Integration test**: end-to-end flow with the example scope registry — user request → scope reveal → child spawn → tool invocation → approval → result.
- **Security tests**: child cannot escape lease boundary path; out-of-scope tools are invisible; sibling boundaries invisible; approval bypass attempts are blocked; missing policy → denied; expired lease → denied; grant token reuse → denied.
- **Reliability tests**: duplicate mutation retries reuse prior state; crashed writes reconcile from intent records; approval-bound retries cannot mutate different arguments under the same grant.
- **Example scope registry**: a demo registry with nested scopes, read/write MCP tools, approval on writes, shared assets with inheritance, to validate the full lifecycle without any external dependencies.

---

## 18. Key Risks and Mitigations

| Risk                                            | Mitigation                                                                                                                                                                                     |
| ----------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Agent SDK API changes                           | Pin SDK version. Wrap SDK calls behind internal interfaces.                                                                                                                                    |
| Scope tree too deep or wide                     | Monitor scope tree depth/breadth. Set hard caps on child scope count and nesting depth per deployment.                                                                                         |
| Runaway session spawning                        | Depth limits, turn budgets, cost caps enforced in leases. Session cost is further reduced by same-boundary scope reveal before spawning a new session.                                         |
| Approval fatigue (too many prompts)             | Tunable thresholds. Batch approvals. Auto-approve for low-risk + non-prod.                                                                                                                     |
| Prompt injection via scope manifests or context | Manifests and context files are authored by trusted scope authors in a reviewed Git repo. Registry is read-only to workers.                                                                    |
| Latency from multi-hop routing                  | Policy evaluation must be < 50ms. Cache compiled registry index. Prefer same-boundary scope reveal before child spawning.                                                                      |
| Boundary compiler heuristics TBD                | Semantic optimization rules need empirical tuning. Start with conservative defaults (fewer merges). Add telemetry to measure boundary efficiency.                                              |
| Overlay precedence for boundary formation       | Boundary hint conflicts need explicit resolution rules. `forceSplit` overrides `mergeWithSiblings` / `mergeWithChildren`; later overlays can tighten but not loosen. Validate at compile time. |
