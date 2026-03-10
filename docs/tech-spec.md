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

- **Scopes govern visibility, not authority.** The registry is a recursive tree of scopes. Each scope is a bounded context with its own skills, commands, agents, and child scopes. Navigation through the tree is progressive disclosure. Authority remains centralized in the control plane.
- **The registry is authored as a scope tree, compiled into execution boundaries, and delegated through bounded agent sessions.** Scope definitions live in a Git repo as a recursive folder hierarchy. At startup they're compiled — first into a merged scope tree, then into execution boundaries that determine which scopes share a runtime session. At execution time each active boundary runs as an isolated Agent SDK session that sees only its merged assets, inherited shared assets, and descriptions of its immediate child boundaries.
- **Scopes are authored; execution boundaries are compiled.** Scopes are the unit of authoring, ownership, and review. Execution boundaries are the unit of runtime allocation. The boundary compiler maps one or more scopes into each boundary based on policy compatibility, resource constraints, and semantic affinity. Authors think in scopes; the runtime thinks in boundaries.
- **Default deny, everywhere.** If a capability, scope, or policy rule is missing, execution fails closed.
- **Pluggable at every boundary.** Storage, search, policy, workflow, audit, and approval are all interfaces. The framework ships defaults (PostgreSQL, in-memory, basic RBAC) but never hard-codes them.

### 1.4 Constraints and Decisions

| Decision                                            | Rationale                                                                                                                                                                                                                                                                                                         |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| TypeScript                                          | Agent SDK has first-class TS support. Enterprise ecosystem.                                                                                                                                                                                                                                                       |
| Each active execution boundary is its own Agent SDK session | Within a boundary, the session can leverage subagents, skills, commands, and agents. The boundary is the isolation unit: entering a child boundary requires spawning a new Agent SDK session via `boundary.enter`. The control plane manages each session's lifecycle, prompt, tools, and lease.                   |
| Execution boundary compilation                      | Scopes are authored independently but compiled into execution boundaries at startup. One or more compatible scopes may share a boundary, reducing session overhead (~1 GiB per SDK session) while preserving policy isolation where required.                                                                      |
| Hybrid Lenny sessions                               | A durable Lenny session wraps one or more Claude SDK sessions. Lenny persists conversation state, active boundary sessions, and structured findings across SDK session lifetimes, enabling hydrate/work/spin-down/resume patterns.                                                                                 |
| REST + SSE for external API                         | Compatible with LibreChat, Slack bots, custom UIs. No WebSocket complexity initially.                                                                                                                                                                                                                             |
| Capability registry in a separate Git repo          | Decouples domain knowledge from framework code. Different owners, different review cadence.                                                                                                                                                                                                                       |
| A2A support in MVP                                  | External agent collaboration is a first-class concern, not an afterthought.                                                                                                                                                                                                                                       |
| PostgreSQL as default backing store                 | With pgvector for semantic search. Pluggable interface for alternatives.                                                                                                                                                                                                                                          |
| Custom minimal workflow engine                      | Behind a pluggable interface. Users can swap in Temporal, BullMQ, etc.                                                                                                                                                                                                                                            |
| Multiple registries via overlays                    | Individuals and teams can layer their own skills, agents, and context onto the base registry. Overlays are applied in order; same-scope assets are combined. Name collisions within a scope are errors.                                                                                                            |

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

**Orchestrator.** The core control-plane loop. Manages task lifecycle, decides when to expand registry scope or spawn child workers, routes meta-tool calls to the appropriate subsystem.

**Scope Resolver.** Holds the compiled scope tree and the boundary map. Navigates the compiled boundary tree to build boundary views: a boundary's merged assets, inherited shared assets from ancestors, and descriptions of immediate child boundaries. Never returns information outside the lease's boundary path.

**Policy / Lease Engine.** Evaluates whether a given actor may perform actions within a given boundary in the current environment. Issues leases. Validates lease constraints on every tool invocation. A child lease must always be anchored to a descendant boundary path of its parent.

**Boundary Compiler.** Takes the merged scope tree (output of overlay compilation) and produces execution boundaries. Applies policy compatibility checks, resource constraints, manifest hints, and semantic heuristics to determine which scopes share a runtime session. See §5.7 for full design.

**Agent Runtime Manager.** Starts and supervises Agent SDK sessions. Each active execution boundary runs as its own Agent SDK session with a system prompt, tools (gateway-backed MCP façades from manifest + meta-tools), and a lease. The manager maps meta-tool calls (from the worker) back to control-plane operations (boundary navigation, child spawning).

**Tool Gateway.** The single enforcement point. Every sensitive action passes through it. It re-checks the lease, re-evaluates policy, requests approval if needed, injects scoped credentials, validates that invoked MCP tools belong to the boundary's manifest, and logs an audit event. MCP tools are exposed to sessions as gateway-backed façades — the session invokes a tool that routes through the gateway, which mediates the actual MCP server connection. Raw MCP endpoints are never directly exposed to worker sessions.

**Approval Service.** Creates approval requests bound to an exact boundary + action + arguments + expiry. Returns one-time, action-bound grant tokens. Children cannot approve themselves.

**Credential Broker.** Holds or exchanges real credentials. Issues ephemeral, least-privilege capabilities per invocation. Workers never see standing secrets. MCP server processes that require credentials (e.g., database URLs, API keys) receive them through the credential broker/proxy — not through direct environment variable injection in the manifest.

**Audit / Telemetry.** Logs every significant event: task creation, lease issuance, tool requests, policy decisions, approval lifecycle, tool execution results. Every event carries a trace ID linking it to the originating user request.

**A2A Adapter.** Discovers external A2A-compliant agents via their Agent Card, dispatches tasks, streams results. External agents are configured in scope manifests and accessible within those scopes.

**Sandbox Runner.** Runs boundary agent sessions in ephemeral containers with no standing credentials, allowlisted network egress, and filesystem isolation. Sandbox constraints are defined per scope in the manifest and merged at boundary compilation. Important for code execution, repo mutations, and shell tasks.

---

## 3. Progressive Disclosure

### 3.1 The Problem

Capability saturation: if a model sees 500 tools, planning quality degrades. The solution is to reveal capabilities progressively, matching the task's evolving needs.

Claude Code already has native progressive disclosure mechanisms — Skills for on-demand knowledge, MCP Tool Search for deferred tool loading. Lenny complements these by adding enterprise-defined capability partitioning: the scope tree and boundary compiler determine which capabilities are available in which runtime context, enforced by policy rather than left to model discretion.

### 3.2 Scope-Based Discovery

The registry is a recursive tree of scopes. Each scope is a self-contained authoring unit with its own context, skills, commands, agents, and child scopes — mirroring the structure of a Claude Code project.

After overlay merging, the scope tree is compiled into execution boundaries (§5.7). At runtime, a worker operates within an execution boundary, which may contain one or more compiled scopes.

A worker operating within a boundary sees:

1. **Its own assets** — context.md, skills/, commands/, agents/ from its boundary's merged scopes.
2. **Inherited shared assets** — context, skills, commands, agents that ancestor scopes have explicitly placed in their `shared/` directories, accumulated down the tree.
3. **Child boundary descriptions** — a short description (from each child boundary's manifest) of what each immediate child boundary handles.

The worker does NOT see the internals of child boundaries, sibling boundaries, or any boundary outside its branch of the tree.

**Example: a boundary's view of its children**

```
## Child Boundaries
- finance: Payments, invoices, vendor ops, reconciliation
- engineering: Platform services, CI/CD, code review
- hr: Benefits, payroll, employee records
```

Each child description is derived from the child boundary's merged manifests. Enough to route, not enough to overwhelm.

### 3.3 Disclosure Flow

1. Root boundary worker starts with its own assets and descriptions of immediate child boundaries.
2. Worker calls `boundary.describe` to inspect a child boundary in more detail — sees the child's description, its own child boundaries, and a summary of its available assets.
3. Worker calls `boundary.enter` to delegate work to a child boundary, spawning a new agent session. The child boundary worker starts with its own full view (own assets + inherited shared + its own child boundary descriptions).
4. If the child boundary has further children, the process repeats recursively.

The capability surface grows with the task. It never starts large. Each session only loads the tools (gateway-backed MCP façades) defined in its boundary's merged manifest.

---

## 4. Worker Model

### 4.1 Boundary Workers

Every execution boundary maps to a potential worker session. A boundary may contain one or more compiled scopes. There is no distinction between "planner" and "executor" — every boundary worker has the same structure:

**Model-autonomous capabilities:**
- Skills (on-demand knowledge and workflows) from its merged scopes
- Agents (delegated worker contexts) from its merged scopes
- MCP tools (external actions) from its merged manifest, exposed as gateway-backed façades

**User-invoked workflows:**
- Commands (slash commands — invoked by the user, not by the model autonomously)

**Structural context:**
- Context documents from its merged scopes
- Descriptions of its child boundaries
- Inherited shared assets from ancestors
- Meta-tools for boundary navigation and task reporting

Each boundary runs as its own Agent SDK session. Within a session, the worker can spawn subagents — these operate under the same lease and see the same assets. The boundary is the key constraint: only `boundary.enter` can cross into a child boundary, which starts a new isolated session with its own context, tools, and lease.

A boundary can be both a container (has child boundaries) and directly executable (has its own skills, commands, agents, and MCP tools). Whether a worker delegates to children or does work itself depends on the task and the boundary's structure.

### 4.2 Worker Lifecycle

Every boundary worker is an Agent SDK session. The control plane creates the session with:

- A system prompt containing the task objective, inherited context, boundary-local context, available assets, and child boundary descriptions.
- MCP tools defined in the boundary's merged manifest (as gateway-backed façades), plus meta-tools (§6) that route back to the control plane.
- A lease (§4.3) that bounds what the worker can see and do.

Within the session, the worker can spawn subagents, use skills, run commands, and invoke agents. These all operate within the boundary and lease. The Agent SDK's `canUseTool` callback is where the control plane intercepts every tool call and enforces the lease.

**Note on subagents vs meta-tools:** Subagents spawned within a boundary are intra-boundary — they share the boundary's context, tools, and lease. Only the main boundary session receives orchestration meta-tools (`boundary.enter`, `boundary.describe`, `boundary.search`). Subagents do not get meta-tools; they are worker threads, not orchestration participants.

### 4.3 Leases

A lease is the structural boundary around a worker. Every tool call is checked against the lease before execution.

```
Lease:
  id:              unique identifier
  task_id:         the task this lease belongs to
  parent_task_id:  null for root, parent's task ID for children
  scope:
    scope_path:    the boundary this worker operates in (e.g., "main.finance.ap")
    max_scope_hops: how many boundary levels below scope_path the worker may navigate (optional)
  allowed_tools:   [] = all meta-tools, or explicit list
  disallowed_tools: explicit deny list (takes precedence)
  permission_mode: default | acceptEdits | bypassPermissions | dontAsk
  approval_mode:   self | parent | root
  max_turns:       cap on agent loop iterations
  max_task_depth:  how many levels of children this worker may spawn
  current_depth:   how deep this worker is in the task tree
  budget:
    max_tokens:    token budget
    max_duration_ms: wall-clock timeout
  expires_at:      absolute expiry (≤ parent's expiry)
```

**Key invariant:** A child lease's scope path must be a descendant of its parent's scope path. The wrapper enforces this at spawn time.

### 4.4 Entering Child Boundaries

A worker calls `boundary.enter` to delegate work to a child boundary. The control plane (not the worker) decides whether to grant the request:

1. Validates the target boundary path is a descendant of the parent's boundary path.
2. Checks depth and budget limits.
3. If valid, issues a child lease with `current_depth = parent.current_depth + 1` and `scope_path` set to the child boundary.
4. Starts a new Agent SDK session with a system prompt built from the child boundary's view (own assets + inherited shared + its child boundary descriptions).
5. The child runs to completion and returns structured output.

**Note:** Subagents spawned _within_ a boundary are distinct from `boundary.enter`. In-boundary subagents share the boundary's context, tools, and lease — they are internal to the session. `boundary.enter` crosses into a _child boundary_, which creates a new isolated Agent SDK session with different context, tools, and lease. The boundary is what `boundary.enter` enforces.

**Default limits (adjustable per deployment):**

- Max delegation depth: configurable (default: 5)
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

1. **Same-session continuation.** The worker handles the task directly within its current boundary session using its available tools and context.
2. **Subagent delegation.** The worker spawns an intra-boundary subagent for a focused subtask. The subagent shares the boundary's tools, context, and lease.
3. **New boundary session.** The worker calls `boundary.enter` to delegate to a child boundary, spawning a new Agent SDK session with its own context, tools, and lease.
4. **Lightweight one-shot.** For simple routing decisions (e.g., "which child boundary handles this?"), the control plane can use a minimal prompt without full boundary hydration.

The control plane and worker collaborate to choose the right level. Heavier strategies cost more (~1 GiB per SDK session) but provide stronger isolation and scoping.

---

## 5. Scope Registry

### 5.1 Physical Structure

The registry is authored as a recursive scope tree and compiled into an index at startup. The control plane accepts an ordered list of registry roots (overlays). The first registry is the base; subsequent registries layer additional assets on top. Each overlay follows the same directory structure. At compile time, overlays are merged scope-by-scope: if the same scope path exists in multiple overlays, their assets (skills, commands, agents, context, shared assets, MCP servers, external agents) are combined. A skill, command, or agent with the same name in the same scope across overlays is a compile-time error. The `description` field uses last-overlay-wins. Security-sensitive manifest fields (`sandbox`, `policy`, `permissions`) use most-restrictive-wins: the compiler takes the most restrictive value from any overlay. For example, if the base sets `requiresApproval: false` but an overlay sets `requiresApproval: true`, the result is `true`. If one overlay allows network access but another denies it, the result is denied. Allowlists (like `allowedTools`, `allowedPaths`) are intersected; denylists (like `disallowedTools`, `deniedPaths`) are unioned. Overlays can add new child scopes but cannot remove scopes defined by earlier overlays.

After overlay merging, the merged scope tree is passed to the **Boundary Compiler** (§5.7), which produces execution boundaries — the runtime units that map to Agent SDK sessions.

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

Each scope has a `manifest.yaml` that configures the session:

```yaml
description: "Accounts payable — invoice lookup, payment drafting, payment submission"

# MCP tool servers available in this scope's session
# Note: credentials are resolved at runtime by the credential broker,
# not injected as static env vars. The names below are credential references.
mcpServers:
  - name: finance-ap-tools
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-ap-mcp"]
    credentials:
      DATABASE_URL: "credential://ap-database-url"

# External A2A-compliant agents accessible from this scope
externalAgents:
  - name: vendor-verification
    description: "Verifies vendor compliance and banking details"
    a2aCardUrl: "https://agents.example.com/vendor-verify/.well-known/agent.json"

# Sandboxing constraints for this scope's session
sandbox:
  allowedPaths:
    - "/workspace/finance/**"
  deniedPaths:
    - "/workspace/finance/.env"
  networkAccess: true
  maxDurationMs: 300000
  permissionMode: acceptEdits

# Policy annotations
policy:
  risk: medium
  dataClass: internal
  requiresApproval: false

# Permissions for the session
permissions:
  allowedTools: []
  disallowedTools: []
  permissionMode: default

# Boundary compilation hints (optional)
boundary:
  force_merge: []        # scope paths to merge into this boundary
  force_split: false     # keep this scope in its own boundary
  never_co_reside: []    # scope paths that must never share a boundary with this scope

# Inheritance filters — control which shared assets from ancestors this scope receives
# By default, all ancestor shared assets are accumulated and passed down.
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
3. **Non-transitive filtering.** If scope B blacklists an inherited skill, scope B's children also won't see it (since B's `shared/` won't re-export it). But B's filtering does not affect its siblings.
4. **Overlays accumulate.** Shared assets from overlays are combined with the base registry's shared assets at each scope level. The same name-collision rule applies: a shared skill, command, or agent with the same name in the same scope across overlays is a compile-time error.

### 5.4 Scripts in Skills

Skills can include executable scripts (shell scripts, TypeScript files, etc.) as part of the registry. These scripts are executed within the sandbox constraints defined in the scope's manifest. The `sandbox` field in the manifest controls:

- Filesystem access (allowed/denied paths)
- Network access
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
4. **Boundary formation.** Pass the merged scope tree to the Boundary Compiler (§5.7), which produces the `ExecutionBoundary[]` tree and `boundaryByScope` map.

Context files (`context.md`) from overlays are appended to the base scope's context, separated by a delimiter. This allows overlays to add instructions without replacing existing ones.

The resolver navigates the compiled boundary tree, filtered by lease scope path. Workers see the combined view — they have no awareness of which registry contributed which assets or which scopes were merged into their boundary.

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

The boundary compiler transforms the merged scope tree into execution boundaries — the runtime units that map to Agent SDK sessions.

**Inputs:**

- Merged scope tree (output of overlay compilation, §5.5 steps 1–3)
- Policy/security metadata per scope (data classification, approval mode, sandbox requirements)
- Boundary hints from manifests and overlays (`force_merge`, `force_split`, `never_co_reside`)
- Hard configuration limits (max scopes per boundary, max tools per boundary, etc.)

**Merge topology rules (hard constraints):**

- Scopes cannot be split across boundaries — a scope belongs to exactly one boundary.
- A boundary is either: one scope, a merge of sibling scopes, or a merge of a parent scope with ALL its immediate children.
- Incompatible scopes must remain in separate boundaries:
  - Different data classifications (e.g., `public` vs `confidential`)
  - Incompatible approval modes (e.g., `self` vs `root`)
  - Incompatible credential domains
  - Incompatible sandbox/network requirements

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

- `force_merge`: explicit directive to merge scopes into one boundary
- `force_split`: keep scopes in separate boundaries even if merge is permitted
- `never_co_reside`: specific scopes that must never share a boundary

**Overlay precedence for boundary formation:**

- Security/policy: most-restrictive-wins (consistent with §5.1)
- Boundary hints: later overlays can add constraints but cannot loosen earlier ones
- `force_split` overrides `force_merge` if they conflict

**Evaluation order:**

1. Apply security/policy hard constraints → identify forced separations
2. Apply operational hard limits → cap boundary size
3. Apply human/manifest overrides → honor `force_merge`, `force_split`, `never_co_reside`
4. Apply semantic optimization → merge remaining candidates where beneficial
5. Validate: every scope belongs to exactly one boundary; boundary tree is valid

**Compiler output:**

- `ExecutionBoundary[]` — the compiled boundary tree
- `boundaryByScope: Map<scope_path, boundary_id>` — O(1) scope→boundary lookup
- Per-boundary: merged manifest (combined tools, skills, context, policy envelope)
- `explanations: Map<boundary_id, string[]>` — why scopes were combined or separated (auditability)

**Properties:**

- **Deterministic:** same inputs always produce the same boundary tree
- **Explainable:** every merge/split decision has a logged reason
- **Testable:** boundary formation can be unit-tested with fixture registries

---

## 6. Meta-Tools

Workers interact with the control plane through a small, fixed set of meta-tools. These are registered alongside the boundary's MCP tools (gateway-backed façades from its merged manifest) into each Agent SDK session.

| Tool                 | Description                                                                                                        | Available to               |
| -------------------- | ------------------------------------------------------------------------------------------------------------------ | -------------------------- |
| `boundary.describe`  | Inspect a child or descendant boundary without entering it. Returns description, child boundaries, and asset summary. | Main boundary session only |
| `boundary.enter`     | Delegate work to a child boundary by spawning a new agent session with an objective.                               | Main boundary session only |
| `boundary.search`    | Search visible boundaries by natural language query.                                                               | Main boundary session only |
| `approval.request`   | Pre-request HITL approval for a planned action within the current boundary.                                        | All workers                |
| `task.update`        | Report structured findings/artifacts to the parent.                                                                | All workers                |

**Note:** Only the main boundary session receives orchestration meta-tools (`boundary.describe`, `boundary.enter`, `boundary.search`). In-boundary subagents do not — they are worker threads scoped to focused subtasks, not orchestration participants.

Each boundary's session also has direct access to the MCP tools defined in its merged manifest, exposed as gateway-backed façades. The gateway mediates all sensitive actions (approval gating, credential injection, audit logging) transparently.

This design avoids tool explosion. Each session only loads the MCP tools relevant to its boundary. A root boundary with 3 child boundaries sees 5 meta-tools + its own MCP tools — not every tool in the registry.

---

## 7. Request Lifecycle

End-to-end flow for a user request:

**Step 1 — Session API receives request.**
Authenticates the caller. Attaches actor identity, roles, scopes, tenant. Creates a root Task record with a trace ID.

**Step 2 — Orchestrator hydrates root execution boundary.**
Builds a boundary view for the root boundary: own context + inherited shared assets + child boundary descriptions. Does NOT load child boundaries' internals.

**Step 3 — Root lease issued.**
Anchored to the root boundary path. Default budget and depth limits.

**Step 4 — Root boundary worker starts.**
Agent SDK session created with a system prompt containing the objective, root boundary context, and child boundary descriptions. Gateway-backed MCP façades from root manifest + meta-tools registered.

**Step 5 — Root worker reasons.**
Sees child boundary descriptions. Calls `boundary.search` or `boundary.describe` to learn more about relevant boundaries. Determines which boundary should handle the work.

**Step 6 — Direct work or delegation.**
If the root boundary has the necessary MCP tools, the worker uses them directly. If the work belongs in a child boundary, the worker calls `boundary.enter` to delegate.

**Step 7 — Side-effect actions.**
When a worker invokes an MCP tool in a boundary where `policy.requiresApproval: true`, the gateway intercepts and returns `approval_required`.

**Step 8 — Approval surfaced to UI.**
The Session API streams an `approval.requested` event to the consuming client. The client renders an approval prompt showing the exact action, arguments, risk level, and any rollback information.

**Step 9 — User approves.**
The client calls the Session API's resolve-approval endpoint. The approval service generates a one-time grant token.

**Step 10 — Execution with grant.**
Worker retries the action with the grant token. The gateway validates the grant (one-time, action-bound, not expired), injects scoped credentials, executes, and returns the result.

**Step 11 — Recursive delegation (if needed).**
If the task grows beyond the current boundary, the worker calls `boundary.enter` to delegate to a child boundary. The wrapper validates the boundary path, issues a child lease, and starts a new agent session. The child runs, returns structured output, and the parent synthesizes.

**Step 12 — Completion.**
Root returns the final result. The Session API streams a `done` event. The audit trail is complete.

---

## 8. Security Model

### 8.1 Threat Model

The primary threat: **the model attempts actions outside its authorized scope**, whether due to prompt injection, hallucination, or adversarial input. The secondary threat: **credential exposure** from the runtime to external systems.

### 8.2 Mandatory Design Rules

**1. Forced mediation.** Workers never talk directly to protected systems. All capability invocations go through the Tool Gateway. MCP tools are exposed to worker sessions as gateway-backed façades — the session invokes a tool definition that routes through the gateway, which manages the actual MCP server connection, credential injection, and audit logging. Even if a worker attempts a direct network call, sandbox network policies block it. Raw MCP endpoints are never directly exposed to worker sessions.

**2. No standing credentials.** Workers and sandboxes never hold durable secrets. The credential broker issues ephemeral, scoped capabilities per invocation. The gateway injects them at dispatch time and never returns them to the worker. MCP server processes that require credentials (e.g., `DATABASE_URL`) receive them through the credential broker/proxy at invocation time — not through static environment variable injection in the manifest. Manifest `credentials` fields are references (e.g., `credential://ap-database-url`), not values.

**3. Approval is cryptographic, not advisory.** Approval is not "Claude says the user agreed." It is a signed, action-bound grant token with an expiry, tied to an exact capability + arguments + requester identity + root task ID. The gateway validates the token before execution.

**4. Least privilege per action.** Each invocation gets the smallest scope, shortest lifetime, and narrowest resource access possible.

**5. Default deny.** If a capability is not in the lease scope, if a policy rule doesn't explicitly allow it, if the environment doesn't permit it — execution fails closed.

**6. Verification before trust.** For code/data changes: run tests, validate schemas, verify diffs, compare against acceptance criteria. The gateway can run validators defined in the scope manifest before returning success.

**7. Lease-scoped access.** Every `canUseTool` callback in the Agent SDK checks the worker's lease. A child cannot see or access boundaries outside its lease's scope path, regardless of what it "knows."

### 8.3 Sandbox Isolation

For boundary workers where the manifest defines sandbox constraints:

- Ephemeral container per worker or per job
- No default credentials
- Network egress allowlist: only the MCP/tool gateway + required package registries
- Filesystem: mount only the boundary's allowed paths from the merged manifest, read-only where possible
- Runtime limits: max CPU, memory, wall-clock timeout
- For code changes: prefer the PR workflow (generate diff → human review → merge)

---

## 9. Policy Engine

### 9.1 Evaluation Model

The policy engine is called by the Tool Gateway on sensitive actions. It receives a `PolicyContext`:

```
PolicyContext:
  actor:        { id, roles, scopes, tenant_id }
  scopeNode:    the boundary's merged manifest and metadata
  toolName:     the specific MCP tool being invoked (optional)
  environment:  dev | staging | prod
  lease:        the worker's lease
  args:         the invocation arguments (optional, for fine-grained rules)
```

It returns one of:

- `{ allowed: true }`
- `{ allowed: false, reason: "..." }`
- `{ requires_approval: true, reason: "..." }`

### 9.2 Evaluation Order

1. **Lease scope check.** Is the action within the lease's scope path? If not → denied.
2. **Lease expiry check.** Has the lease expired? If so → denied.
3. **Policy rules.** Evaluate authored rules in priority order. Each rule has conditions (roles, scope paths, environments, risk levels, data classes) and an effect (allow, deny, require_approval).
4. **Scope-level approval.** If the manifest says `policy.requiresApproval: true` → approval required.
5. **Actor scope check.** Does the actor have the necessary permissions for this scope?
6. **Default deny.**

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
2. **Approval request created**: bound to exact boundary path + action + arguments + risk level + expiry.
3. **Event streamed to client**: `approval.requested` with summary, risk, rollback info.
4. **User resolves**: approved or denied, with their identity.
5. **Grant token generated**: one-time, action-bound. Stored in the approval record.
6. **Worker retries invocation** with the grant token.
7. **Gateway validates**: token matches the approval record, not expired, not already used.
8. **Audit logged**: who approved, what was approved, when, with what arguments.

### 10.2 Key Properties

- **Action-bound.** A grant for a specific action in a specific boundary cannot be reused for a different action or boundary.
- **One-time.** Each grant token is consumed on use.
- **Expiring.** Grants have a TTL (default: 10 minutes).
- **Non-inheritable.** A child worker cannot use a grant issued to its parent (unless the wrapper explicitly binds it).

---

## 11. External Integration

### 11.1 MCP Tools

External systems are exposed to Lenny as MCP servers configured in scope manifests. At runtime, each boundary's session accesses MCP tools through gateway-backed façades — the worker invokes a tool definition that the gateway proxies to the actual MCP server, mediating credential injection, policy enforcement, and audit logging. Raw MCP endpoints are never directly exposed to worker sessions.

### 11.2 A2A Agents

External A2A-compliant agents are configured as `externalAgents` in scope manifests with an `a2aCardUrl`. The A2A adapter:

1. Fetches and caches the remote agent's Agent Card.
2. Maps the scope's external agent config to A2A task format.
3. Dispatches tasks via HTTP.
4. Streams results back through the standard event pipeline.

The adapter supports both synchronous (`/tasks/send`) and streaming (`/tasks/sendSubscribe` via SSE) A2A interactions.

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
- Compacted conversation state (structured summaries, not raw transcripts)
- Structured findings from completed child boundary sessions
- Pending approvals
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

| Event                  | When                                              |
| ---------------------- | ------------------------------------------------- |
| `task.created`         | Root or child task created                        |
| `task.updated`         | Task status or output changed                     |
| `lease.issued`         | New lease created                                 |
| `lease.expired`        | Lease expired or revoked                          |
| `worker.started`       | Agent SDK session started                         |
| `worker.stopped`       | Session completed, failed, or cancelled           |
| `tool.requested`       | Worker attempted a tool call                      |
| `tool.allowed`         | Policy allowed the call                           |
| `tool.denied`          | Policy denied the call                            |
| `tool.executed`        | Tool call succeeded                               |
| `tool.failed`          | Tool call errored                                 |
| `approval.requested`   | Approval prompt created                           |
| `approval.resolved`    | User approved or denied                           |
| `boundary.entered`     | Worker spawned agent session in a child boundary  |
| `boundary.described`   | Worker inspected a child boundary's description   |
| `policy.evaluated`     | Policy engine made a decision                     |

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
| `WorkerAllocator`        | One-session-per-boundary                 | Boundary worker allocation strategy          |
| `IdleTimeoutPolicy`      | Fixed timeout (15 min)                   | Configurable session deallocation            |
| `CostGovernor`           | No-op                                    | Per-tenant token/concurrency budgets         |

---

## 15. End-to-End Examples

### 15.1 Read-Only Knowledge Query

> User: "What's the parental leave policy for employees in Canada?"

1. Root sees child boundaries including `hr`.
2. Calls `boundary.describe("hr")` → sees `benefits` child boundary.
3. Calls `boundary.enter("hr.benefits", objective: "Find parental leave policy for Canada")`.
4. Child session starts with `hr.benefits` boundary. Has MCP tools for policy search.
5. Uses policy search tool directly → returns policy document.
6. Child returns findings to root.
7. Root summarizes with citations.

Simple delegation. No approval needed (low risk boundary).

### 15.2 Financial Write with Approval

> User: "Pay invoice INV-9831 from ACME for $50,000."

1. Root sees `finance` child boundary. Calls `boundary.enter("finance.ap", objective: "Pay invoice INV-9831")`.
2. AP session starts. Has invoice and payment MCP tools.
3. Uses invoice lookup tool → returns invoice details.
4. Uses payment draft tool → returns `draft_id`.
5. Calls payment submit tool. Boundary manifest has `policy.requiresApproval: true`.
6. Gateway intercepts → returns `approval_required` with summary: "Submit payment $50,000 to ACME".
7. User approves in UI.
8. Worker retries with grant token → gateway validates → payment submitted.
9. Full audit trail: who requested, who approved, what arguments, which boundary.

### 15.3 Task That Grows Mid-Execution

> User: "Pay invoice INV-9831."

Same as above, but at step 3, invoice lookup reveals the vendor has an incomplete compliance record.

4. AP worker sees it needs vendor verification, which lives in a sibling boundary (`finance.procurement`). It can't access it directly.
5. AP worker returns findings to root: "Vendor compliance incomplete. Need procurement check before proceeding."
6. Root calls `boundary.enter("finance.procurement", objective: "Check vendor compliance status for ACME")`.
7. Procurement session investigates → returns: "Vendor W-9 unverified. Banking details incomplete. Recommend: block payment, notify procurement."
8. Root synthesizes findings from both boundaries and asks the user what to do.

The task adapted by routing through sibling boundaries. Neither child could see the other's internals.

### 15.4 Engineering Workflow

> User: "Add rate limiting to /v1/export, update tests, open a PR."

1. Root sees `engineering` child boundary. Calls `boundary.enter("engineering.platform", objective: "Add rate limiting to /v1/export, update tests, open a PR")`.
2. Platform session starts in an ephemeral container (sandbox constraints from manifest). Clones the repo (gateway injects short-lived Git token). Makes changes. Runs tests.
3. If tests fail, iterates (Reason → Act → Verify loop within the boundary's turn budget).
4. PR creation requires approval per boundary policy. Gateway returns `approval_required`.
5. User approves. PR created.
6. Root returns the PR link, change summary, and test results.

---

## 16. MVP Build Sequence

| Phase | What                                     | Why first                                      |
| ----- | ---------------------------------------- | ---------------------------------------------- |
| 1     | Core types and pluggable interfaces      | Everything depends on these contracts           |
| 2     | Scope tree loader and compiler           | Progressive disclosure is the foundation        |
| 2.5   | Boundary compiler                        | Execution boundaries must exist before runtime  |
| 3     | Policy engine + lease management         | Security must be structural from day one        |
| 4     | Tool Gateway                             | The single enforcement point                    |
| 5     | Agent Runtime Manager + boundary meta-tools | The Agent SDK integration                    |
| 6     | Orchestrator                             | The control-plane loop connecting everything    |
| 7     | Approval flow                            | HITL for side effects                           |
| 8     | A2A adapter                              | External agent support (MVP requirement)        |
| 9     | Audit logger                             | Observability                                   |
| 10    | Session API (REST + SSE)                 | External client connectivity                    |
| 11    | Example scope registry                   | Prove end-to-end                                |

---

## 17. Verification

- **Unit tests** for each module: scope tree loader, boundary compiler, policy engine, lease validation, gateway, approval service.
- **Integration test**: end-to-end flow with the example scope registry — user request → boundary navigation → child spawn → tool invocation → approval → result.
- **Security tests**: child cannot escape lease scope path; sibling boundaries invisible; approval bypass attempts are blocked; missing policy → denied; expired lease → denied; grant token reuse → denied.
- **Example scope registry**: a demo registry with nested scopes, read/write MCP tools, approval on writes, shared assets with inheritance, to validate the full lifecycle without any external dependencies.

---

## 18. Key Risks and Mitigations

| Risk                                            | Mitigation                                                                                                                       |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| Agent SDK API changes                           | Pin SDK version. Wrap SDK calls behind internal interfaces.                                                                      |
| Scope tree too deep or wide                     | Monitor scope tree depth/breadth. Set hard caps on child scope count and nesting depth per deployment.                           |
| Runaway session spawning                        | Depth limits, turn budgets, cost caps enforced in leases. Session cost mitigated by boundary compilation (merge compatible scopes). |
| Approval fatigue (too many prompts)             | Tunable thresholds. Batch approvals. Auto-approve for low-risk + non-prod.                                                       |
| Prompt injection via scope manifests or context | Manifests and context files are authored by trusted scope authors in a reviewed Git repo. Registry is read-only to workers.      |
| Latency from multi-hop routing                  | Policy evaluation must be < 50ms. Cache compiled registry index. Minimize child spawning for simple tasks.                       |
| Boundary compiler heuristics TBD                | Semantic optimization rules need empirical tuning. Start with conservative defaults (fewer merges). Add telemetry to measure boundary efficiency. |
| Overlay precedence for boundary formation       | Boundary hint conflicts need explicit resolution rules. `force_split` overrides `force_merge`; later overlays can tighten but not loosen. Validate at compile time. |
