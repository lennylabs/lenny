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

No existing open-source framework provides these as a cohesive, extensible platform.

### 1.3 Design Principles

- **Scopes govern visibility, not authority.** The registry is a recursive tree of scopes. Each scope is a bounded context with its own skills, commands, agents, and child scopes. Navigation through the tree is progressive disclosure. Authority remains centralized in the control plane.
- **The registry is authored as a scope tree, compiled into an index, and delegated through bounded agent sessions.** Scope definitions live in a Git repo as a recursive folder hierarchy. At startup they're compiled into a runtime index. At execution time each scope runs as an isolated Agent SDK session that sees only its own assets, inherited shared assets, and descriptions of its immediate children.
- **Default deny, everywhere.** If a capability, scope, or policy rule is missing, execution fails closed.
- **Pluggable at every boundary.** Storage, search, policy, workflow, audit, and approval are all interfaces. The framework ships defaults (PostgreSQL, in-memory, basic RBAC) but never hard-codes them.

### 1.4 Constraints and Decisions

| Decision | Rationale |
|----------|-----------|
| TypeScript | Agent SDK has first-class TS support. Enterprise ecosystem. |
| Each scope is its own Agent SDK session | Within a scope, the session can leverage subagents, skills, commands, and agents. The boundary is the scope: entering a child scope requires spawning a new Agent SDK session via `scope.enter`. The control plane manages each session's lifecycle, prompt, tools, and lease. |
| REST + SSE for external API | Compatible with LibreChat, Slack bots, custom UIs. No WebSocket complexity initially. |
| Capability registry in a separate Git repo | Decouples domain knowledge from framework code. Different owners, different review cadence. |
| A2A support in MVP | External agent collaboration is a first-class concern, not an afterthought. |
| PostgreSQL as default backing store | With pgvector for semantic search. Pluggable interface for alternatives. |
| Custom minimal workflow engine | Behind a pluggable interface. Users can swap in Temporal, BullMQ, etc. |

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
│  │              AGENT RUNTIME MANAGER                      │  │
│  │   Start / stop / resume / fork Agent SDK sessions       │  │
│  └──────┬─────────────────────┬───────────────┬───────────┘  │
│         │                     │               │              │
│    Root Scope            Child Scope      Child Scope        │
│   (Agent SDK)          (Agent SDK)      (Agent SDK)          │
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
  (external tools)  (external agents)  (ephemeral containers)
```

### 2.2 Component Responsibilities

**Session API.** External entry point. Authenticates callers, attaches tenant/policy context, opens or resumes root tasks, resolves approvals, streams events to clients via SSE.

**Orchestrator.** The core control-plane loop. Manages task lifecycle, decides when to expand registry scope or spawn child workers, routes meta-tool calls to the appropriate subsystem.

**Scope Resolver.** Holds the compiled scope tree. Navigates the tree to build scope views: a scope's own assets, inherited shared assets from ancestors, and descriptions of immediate child scopes. Never returns information outside the lease's scope path.

**Policy / Lease Engine.** Evaluates whether a given actor may perform actions within a given scope in the current environment. Issues leases. Validates lease constraints on every tool invocation. A child lease must always be anchored to a descendant scope path of its parent.

**Agent Runtime Manager.** Starts and supervises Agent SDK sessions. Each scope runs as its own Agent SDK session with a system prompt, tools (MCP servers from manifest + meta-tools), and a lease. The manager maps meta-tool calls (from the worker) back to control-plane operations (scope navigation, child spawning).

**Tool Gateway.** The single enforcement point. Every sensitive action passes through it. It re-checks the lease, re-evaluates policy, requests approval if needed, injects scoped credentials, validates that invoked MCP tools belong to the scope's manifest, and logs an audit event.

**Approval Service.** Creates approval requests bound to an exact scope + action + arguments + expiry. Returns one-time, action-bound grant tokens. Children cannot approve themselves.

**Credential Broker.** Holds or exchanges real credentials. Issues ephemeral, least-privilege access per invocation. Workers never see standing secrets.

**Audit / Telemetry.** Logs every significant event: task creation, lease issuance, tool requests, policy decisions, approval lifecycle, tool execution results. Every event carries a trace ID linking it to the originating user request.

**A2A Adapter.** Discovers external A2A-compliant agents via their Agent Card, dispatches tasks, streams results. External agents are configured in scope manifests and accessible within those scopes.

**Sandbox Runner.** Runs scope agent sessions in ephemeral containers with no standing credentials, allowlisted network egress, and filesystem isolation. Sandbox constraints are defined per scope in the manifest. Important for code execution, repo mutations, and shell tasks.

---

## 3. Progressive Disclosure

### 3.1 The Problem

Capability saturation: if a model sees 500 tools, planning quality degrades. The solution is to reveal capabilities progressively, matching the task's evolving needs.

### 3.2 Scope-Based Discovery

The registry is a recursive tree of scopes. Each scope is a self-contained unit with its own context, skills, commands, agents, and child scopes — mirroring the structure of a Claude Code project.

A worker operating within a scope sees:
1. **Its own assets** — context.md, skills/, commands/, agents/ from its scope directory.
2. **Inherited shared assets** — context, skills, commands, agents that ancestor scopes have explicitly placed in their `shared/` directories, accumulated down the tree.
3. **Child scope descriptions** — a short description (from each child's manifest) of what each immediate child scope handles.

The worker does NOT see the internals of child scopes, sibling scopes, or any scope outside its branch of the tree.

**Example: a scope's view of its children**

```
## Child Scopes
- finance: Payments, invoices, vendor ops, reconciliation
- engineering: Platform services, CI/CD, code review
- hr: Benefits, payroll, employee records
```

Each child description is a single line from the child's `manifest.yaml`. Enough to route, not enough to overwhelm.

### 3.3 Disclosure Flow

1. Root scope worker starts with its own assets and descriptions of immediate child scopes.
2. Worker calls `scope.describe` to inspect a child scope in more detail — sees the child's description, its own child scopes, and a summary of its available assets.
3. Worker calls `scope.enter` to delegate work to a child scope, spawning a new agent session. The child scope worker starts with its own full view (own assets + inherited shared + its own child descriptions).
4. If the child scope has further children, the process repeats recursively.

The capability surface grows with the task. It never starts large. Each session only loads the tools (MCP servers) defined in its scope's manifest.

---

## 4. Worker Model

### 4.1 Scope Workers

Every scope in the registry maps to a potential worker session. There is no distinction between "planner" and "executor" — every scope worker has the same structure:

- Its own context, skills, commands, and agents
- MCP tools from its manifest
- Descriptions of its child scopes
- Inherited shared assets from ancestors
- Meta-tools for scope navigation and task reporting

Each scope runs as its own Agent SDK session. Within a session, the worker can spawn subagents — these operate under the same lease and see the same assets. The scope boundary is the key constraint: only `scope.enter` can cross into a child scope, which starts a new isolated session with its own context, tools, and lease.

A scope can be both a container (has child scopes) and directly executable (has its own skills, commands, agents, and MCP tools). Whether a worker delegates to children or does work itself depends on the task and the scope's structure.

### 4.2 Worker Lifecycle

Every scope worker is an Agent SDK session. The control plane creates the session with:

- A system prompt containing the task objective, inherited context, scope-local context, available assets, and child scope descriptions.
- MCP tools defined in the scope's manifest, plus meta-tools (§6) that route back to the control plane.
- A lease (§4.3) that bounds what the worker can see and do.

Within the session, the worker can spawn subagents, use skills, run commands, and invoke agents. These all operate within the scope's boundary and lease. The Agent SDK's `canUseTool` callback is where the control plane intercepts every tool call and enforces the lease.

### 4.3 Leases

A lease is the structural boundary around a worker. Every tool call is checked against the lease before execution.

```
Lease:
  id:              unique identifier
  task_id:         the task this lease belongs to
  parent_task_id:  null for root, parent's task ID for children
  scope:
    scope_path:    the scope this worker operates in (e.g., "main.finance.ap")
    max_depth:     how many levels below scope_path the worker may navigate (optional)
  allowed_tools:   [] = all meta-tools, or explicit list
  disallowed_tools: explicit deny list (takes precedence)
  permission_mode: default | acceptEdits | bypassPermissions | dontAsk
  approval_mode:   self | parent | root
  max_turns:       cap on agent loop iterations
  max_depth:       how many levels of children this worker may spawn
  current_depth:   how deep this worker is in the task tree
  budget:
    max_tokens:    token budget
    max_duration_ms: wall-clock timeout
  expires_at:      absolute expiry (≤ parent's expiry)
```

**Key invariant:** A child lease's scope path must be a descendant of its parent's scope path. The wrapper enforces this at spawn time.

### 4.4 Entering Child Scopes

A worker calls `scope.enter` to delegate work to a child scope. The control plane (not the worker) decides whether to grant the request:

1. Validates the target scope path is a descendant of the parent's scope path.
2. Checks depth and budget limits.
3. If valid, issues a child lease with `current_depth = parent.current_depth + 1` and `scope_path` set to the child scope.
4. Starts a new Agent SDK session with a system prompt built from the child scope's view (own assets + inherited shared + its child descriptions).
5. The child runs to completion and returns structured output.

**Note:** Subagents spawned *within* a scope are distinct from `scope.enter`. In-scope subagents share the scope's context, tools, and lease — they are internal to the session. `scope.enter` crosses into a *child scope*, which creates a new isolated Agent SDK session with different context, tools, and lease. The scope boundary is what `scope.enter` enforces.

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
- Required additional scopes (outside current scope)
- Risks / policy flags
- Confidence
- Artifacts / evidence references

---

## 5. Scope Registry

### 5.1 Physical Structure

The registry lives in a separate Git repo. Authored as a recursive scope tree, compiled into an index at startup.

Each scope is a directory that mirrors a Claude Code project structure: `context.md` for instructions, `manifest.yaml` for configuration, `skills/`, `commands/`, `agents/` for assets, `shared/` for assets inherited by children, and `scopes/` for child scopes.

```
capability-registry/
├── registry.yaml                    # version, name, defaults
└── main/
    ├── context.md                   # Instructions for the root scope
    ├── manifest.yaml                # Root scope configuration
    ├── skills/                      # Skills available at root level
    ├── commands/                    # Commands available at root level
    ├── agents/                      # Agents available at root level
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

### 5.2 Scope Manifest Schema

Each scope has a `manifest.yaml` that configures the session:

```yaml
description: "Accounts payable — invoice lookup, payment drafting, payment submission"

# MCP tool servers available in this scope's session
mcpServers:
  - name: finance-ap-tools
    transport: stdio
    command: npx
    args: ["-y", "@company/finance-ap-mcp"]
    env:
      DATABASE_URL: "${AP_DATABASE_URL}"

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

# Inheritance filters — control which shared assets from ancestors this scope receives
# By default, all ancestor shared assets are accumulated and passed down.
# Use whitelist or blacklist to filter.
inherit:
  context:
    blacklist: ["legacy-guidelines"]
  skills:
    whitelist: ["code-review", "testing"]
  commands: {}                       # accept all inherited commands
  agents:
    blacklist: ["deprecated-agent"]
```

### 5.3 Shared Assets and Inheritance

Each scope can place assets in its `shared/` directory to make them available to all descendant scopes:

- `shared/context.md` — context instructions inherited by children
- `shared/skills/` — skills inherited by children
- `shared/commands/` — commands inherited by children
- `shared/agents/` — agents inherited by children

**Inheritance rules:**

1. **Accumulated by default.** A scope sees shared assets from ALL ancestors, accumulated top-down. If `main` shares a skill and `main.finance` shares another skill, then `main.finance.ap` sees both.
2. **Filterable per scope.** A scope's manifest can declare `inherit` filters to whitelist or blacklist specific inherited assets. A whitelist takes precedence over a blacklist if both are set.
3. **Non-transitive filtering.** If scope B blacklists an inherited skill, scope B's children also won't see it (since B's `shared/` won't re-export it). But B's filtering does not affect its siblings.

### 5.4 Scripts in Skills

Skills can include executable scripts (shell scripts, TypeScript files, etc.) as part of the registry. These scripts are executed within the sandbox constraints defined in the scope's manifest. The `sandbox` field in the manifest controls:

- Filesystem access (allowed/denied paths)
- Network access
- Duration limits
- Permission mode

### 5.5 Runtime Compilation

At startup, the registry loader recursively walks the scope tree. The compiler produces:

- A `ScopeTree` with the root `ScopeNode` and all descendants
- A `byPath` map for O(1) lookup of any scope by its dot-delimited path (e.g., `"main.finance.ap"`)

The resolver navigates this tree, filtered by lease scope path.

---

## 6. Meta-Tools

Workers interact with the control plane through a small, fixed set of meta-tools. These are registered alongside the scope's MCP tools (from its manifest) into each Agent SDK session.

| Tool | Description | Available to |
|------|-------------|-------------|
| `scope.describe` | Inspect a child or descendant scope without entering it. Returns description, child scopes, and asset summary. | All workers |
| `scope.enter` | Delegate work to a child scope by spawning a new agent session with an objective. | All workers |
| `scope.search` | Search visible scopes by natural language query. | All workers |
| `approval.request` | Pre-request HITL approval for a planned action within the current scope. | All workers |
| `task.update` | Report structured findings/artifacts to the parent. | All workers |

Each scope's session also has direct access to the MCP tools defined in its manifest. Unlike the previous design, there is no `gateway.invoke` indirection for tool calls — MCP tools are registered directly into the session. The gateway still mediates sensitive actions (approval gating, credential injection, audit logging) but this happens transparently.

This design avoids tool explosion. Each session only loads the MCP tools relevant to its scope. A root scope with 3 child scopes sees 5 meta-tools + its own MCP tools — not every tool in the registry.

---

## 7. Request Lifecycle

End-to-end flow for a user request:

**Step 1 — Session API receives request.**
Authenticates the caller. Attaches actor identity, roles, scopes, tenant. Creates a root Task record with a trace ID.

**Step 2 — Orchestrator hydrates root scope.**
Builds a scope view for the root scope: own context + inherited shared assets + child scope descriptions. Does NOT load child scopes' internals.

**Step 3 — Root lease issued.**
Anchored to the root scope path. Default budget and depth limits.

**Step 4 — Root scope worker starts.**
Agent SDK session created with a system prompt containing the objective, root scope context, and child scope descriptions. MCP tools from root manifest + meta-tools registered.

**Step 5 — Root worker reasons.**
Sees child scope descriptions. Calls `scope.search` or `scope.describe` to learn more about relevant scopes. Determines which scope should handle the work.

**Step 6 — Direct work or delegation.**
If the root scope has the necessary MCP tools, the worker uses them directly. If the work belongs in a child scope, the worker calls `scope.enter` to delegate.

**Step 7 — Side-effect actions.**
When a worker invokes an MCP tool in a scope where `policy.requiresApproval: true`, the gateway intercepts and returns `approval_required`.

**Step 8 — Approval surfaced to UI.**
The Session API streams an `approval.requested` event to the consuming client. The client renders an approval prompt showing the exact action, arguments, risk level, and any rollback information.

**Step 9 — User approves.**
The client calls the Session API's resolve-approval endpoint. The approval service generates a one-time grant token.

**Step 10 — Execution with grant.**
Worker retries the action with the grant token. The gateway validates the grant (one-time, action-bound, not expired), injects scoped credentials, executes, and returns the result.

**Step 11 — Recursive delegation (if needed).**
If the task grows beyond the current scope, the worker calls `scope.enter` to delegate to a child scope. The wrapper validates the scope path, issues a child lease, and starts a new agent session. The child runs, returns structured output, and the parent synthesizes.

**Step 12 — Completion.**
Root returns the final result. The Session API streams a `done` event. The audit trail is complete.

---

## 8. Security Model

### 8.1 Threat Model

The primary threat: **the model attempts actions outside its authorized scope**, whether due to prompt injection, hallucination, or adversarial input. The secondary threat: **credential exposure** from the runtime to external systems.

### 8.2 Mandatory Design Rules

**1. Forced mediation.** Workers never talk directly to protected systems. All capability invocations go through the Tool Gateway. Even if a worker attempts a direct network call, sandbox network policies block it.

**2. No standing credentials.** Workers and sandboxes never hold durable secrets. The credential broker issues ephemeral, scoped tokens per invocation. The gateway injects them at dispatch time and never returns them to the worker.

**3. Approval is cryptographic, not advisory.** Approval is not "Claude says the user agreed." It is a signed, action-bound grant token with an expiry, tied to an exact capability + arguments + requester identity + root task ID. The gateway validates the token before execution.

**4. Least privilege per action.** Each invocation gets the smallest scope, shortest lifetime, and narrowest resource access possible.

**5. Default deny.** If a capability is not in the lease scope, if a policy rule doesn't explicitly allow it, if the environment doesn't permit it — execution fails closed.

**6. Verification before trust.** For code/data changes: run tests, validate schemas, verify diffs, compare against acceptance criteria. The gateway can run validators defined in the scope manifest before returning success.

**7. Lease-scoped access.** Every `canUseTool` callback in the Agent SDK checks the worker's lease. A child cannot see or access scopes outside its lease's scope path, regardless of what it "knows."

### 8.3 Sandbox Isolation

For scope workers where the manifest defines sandbox constraints:

- Ephemeral container per worker or per job
- No default credentials
- Network egress allowlist: only the MCP/tool gateway + required package registries
- Filesystem: mount only the scope's allowed paths from the manifest, read-only where possible
- Runtime limits: max CPU, memory, wall-clock timeout
- For code changes: prefer the PR workflow (generate diff → human review → merge)

---

## 9. Policy Engine

### 9.1 Evaluation Model

The policy engine is called by the Tool Gateway on sensitive actions. It receives a `PolicyContext`:

```
PolicyContext:
  actor:        { id, roles, scopes, tenant_id }
  scopeNode:    the scope's manifest and metadata
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
2. **Approval request created**: bound to exact scope path + action + arguments + risk level + expiry.
3. **Event streamed to client**: `approval.requested` with summary, risk, rollback info.
4. **User resolves**: approved or denied, with their identity.
5. **Grant token generated**: one-time, action-bound. Stored in the approval record.
6. **Worker retries invocation** with the grant token.
7. **Gateway validates**: token matches the approval record, not expired, not already used.
8. **Audit logged**: who approved, what was approved, when, with what arguments.

### 10.2 Key Properties

- **Action-bound.** A grant for a specific action in a specific scope cannot be reused for a different action or scope.
- **One-time.** Each grant token is consumed on use.
- **Expiring.** Grants have a TTL (default: 10 minutes).
- **Non-inheritable.** A child worker cannot use a grant issued to its parent (unless the wrapper explicitly binds it).

---

## 11. External Integration

### 11.1 MCP Tools

External systems are exposed to Lenny as MCP servers configured in scope manifests. Each scope's session connects to the MCP servers listed in its `mcpServers` field. The gateway mediates credential injection and audit logging.

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

---

## 13. Audit and Observability

### 13.1 What Gets Logged

Every significant event, each carrying a trace ID:

| Event | When |
|-------|------|
| `task.created` | Root or child task created |
| `task.updated` | Task status or output changed |
| `lease.issued` | New lease created |
| `lease.expired` | Lease expired or revoked |
| `worker.started` | Agent SDK session started |
| `worker.stopped` | Session completed, failed, or cancelled |
| `tool.requested` | Worker attempted a tool call |
| `tool.allowed` | Policy allowed the call |
| `tool.denied` | Policy denied the call |
| `tool.executed` | Tool call succeeded |
| `tool.failed` | Tool call errored |
| `approval.requested` | Approval prompt created |
| `approval.resolved` | User approved or denied |
| `scope.entered` | Worker spawned agent session in a child scope |
| `scope.described` | Worker inspected a child scope's description |
| `policy.evaluated` | Policy engine made a decision |

### 13.2 PII Redaction

Scope manifests or policy rules can specify fields that should be redacted in audit logs (e.g., `bank_account`, `ssn`).

### 13.3 Pluggable Sink

The audit sink is an interface. The default writes to the storage adapter. Users can plug in external SIEM, log aggregation, or compliance systems.

---

## 14. Pluggable Interfaces

Every major subsystem boundary is a pluggable interface:

| Interface | Default | Purpose |
|-----------|---------|---------|
| `StorageAdapter` | PostgreSQL (in-memory for dev) | Tasks, leases, approvals, audit records |
| `PolicyEngine` | Rule-based RBAC/ABAC | Policy evaluation |
| `RegistrySearchProvider` | In-memory text match (pgvector for prod) | Semantic + metadata search over scopes |
| `WorkflowEngine` | In-memory state machine | Async job lifecycle |
| `AuditSink` | Storage adapter | Event logging |
| `CredentialBroker` | No-op | Ephemeral credential issuance |
| `A2AAdapter` | HTTP-based | External agent communication |
| `ToolExecutor` | Pass-through | Dispatches to MCP/A2A/workflow |

---

## 15. End-to-End Examples

### 15.1 Read-Only Knowledge Query

> User: "What's the parental leave policy for employees in Canada?"

1. Root sees child scopes including `hr`.
2. Calls `scope.describe("hr")` → sees `benefits` child scope.
3. Calls `scope.enter("hr.benefits", objective: "Find parental leave policy for Canada")`.
4. Child session starts with `hr.benefits` scope. Has MCP tools for policy search.
5. Uses policy search tool directly → returns policy document.
6. Child returns findings to root.
7. Root summarizes with citations.

Simple delegation. No approval needed (low risk scope).

### 15.2 Financial Write with Approval

> User: "Pay invoice INV-9831 from ACME for $50,000."

1. Root sees `finance` child scope. Calls `scope.enter("finance.ap", objective: "Pay invoice INV-9831")`.
2. AP session starts. Has invoice and payment MCP tools.
3. Uses invoice lookup tool → returns invoice details.
4. Uses payment draft tool → returns `draft_id`.
5. Calls payment submit tool. Scope manifest has `policy.requiresApproval: true`.
6. Gateway intercepts → returns `approval_required` with summary: "Submit payment $50,000 to ACME".
7. User approves in UI.
8. Worker retries with grant token → gateway validates → payment submitted.
9. Full audit trail: who requested, who approved, what arguments, which scope.

### 15.3 Task That Grows Mid-Execution

> User: "Pay invoice INV-9831."

Same as above, but at step 3, invoice lookup reveals the vendor has an incomplete compliance record.

4. AP worker sees it needs vendor verification, which lives in a sibling scope (`finance.procurement`). It can't access it directly.
5. AP worker returns findings to root: "Vendor compliance incomplete. Need procurement check before proceeding."
6. Root calls `scope.enter("finance.procurement", objective: "Check vendor compliance status for ACME")`.
7. Procurement session investigates → returns: "Vendor W-9 unverified. Banking details incomplete. Recommend: block payment, notify procurement."
8. Root synthesizes findings from both scopes and asks the user what to do.

The task adapted by routing through sibling scopes. Neither child could see the other's internals.

### 15.4 Engineering Workflow

> User: "Add rate limiting to /v1/export, update tests, open a PR."

1. Root sees `engineering` child scope. Calls `scope.enter("engineering.platform", objective: "Add rate limiting to /v1/export, update tests, open a PR")`.
2. Platform session starts in an ephemeral container (sandbox constraints from manifest). Clones the repo (gateway injects short-lived Git token). Makes changes. Runs tests.
3. If tests fail, iterates (Reason → Act → Verify loop within the scope's turn budget).
4. PR creation requires approval per scope policy. Gateway returns `approval_required`.
5. User approves. PR created.
6. Root returns the PR link, change summary, and test results.

---

## 16. MVP Build Sequence

| Phase | What | Why first |
|-------|------|-----------|
| 1 | Core types and pluggable interfaces | Everything depends on these contracts |
| 2 | Scope tree loader and compiler | Progressive disclosure is the foundation |
| 3 | Policy engine + lease management | Security must be structural from day one |
| 4 | Tool Gateway | The single enforcement point |
| 5 | Agent Runtime Manager + scope meta-tools | The Agent SDK integration |
| 6 | Orchestrator | The control-plane loop connecting everything |
| 7 | Approval flow | HITL for side effects |
| 8 | A2A adapter | External agent support (MVP requirement) |
| 9 | Audit logger | Observability |
| 10 | Session API (REST + SSE) | External client connectivity |
| 11 | Example scope registry | Prove end-to-end |

---

## 17. Verification

- **Unit tests** for each module: scope tree loader, policy engine, lease validation, gateway, approval service.
- **Integration test**: end-to-end flow with the example scope registry — user request → scope navigation → child spawn → tool invocation → approval → result.
- **Security tests**: child cannot escape lease scope path; sibling scopes invisible; approval bypass attempts are blocked; missing policy → denied; expired lease → denied; grant token reuse → denied.
- **Example scope registry**: a demo registry with nested scopes, read/write MCP tools, approval on writes, shared assets with inheritance, to validate the full lifecycle without any external dependencies.

---

## 18. Key Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Agent SDK API changes | Pin SDK version. Wrap SDK calls behind internal interfaces. |
| Scope tree too deep or wide | Monitor scope tree depth/breadth. Set hard caps on child scope count and nesting depth per deployment. |
| Runaway session spawning | Depth limits, turn budgets, cost caps enforced in leases. Session-per-scope cost mitigated by scope design (keep trees shallow). |
| Approval fatigue (too many prompts) | Tunable thresholds. Batch approvals. Auto-approve for low-risk + non-prod. |
| Prompt injection via scope manifests or context | Manifests and context files are authored by trusted scope authors in a reviewed Git repo. Registry is read-only to workers. |
| Latency from multi-hop routing | Policy evaluation must be < 50ms. Cache compiled registry index. Minimize child spawning for simple tasks. |
