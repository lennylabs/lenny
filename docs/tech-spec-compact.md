# Lenny - Compact Technical Specification

## 1. Overview

### 1.1 What Lenny Is

Lenny is an open-source orchestrator for enterprise-scale agentic workflows. It enables large teams to easily build and deploy agents, skills, and tools and easily make them available inside a general purpose conversational agent. Behind the scenes, Lenny manages a fleet of Claude Code agents and provides routing, policy enforcement, approvals, improved progressive disclosure for better semantic matching at scale, multi-worker executions, and auditing.

Key features:

- Opinionated registry for agents, skills, and tools:
  - Agents, skills, tools, policies, and configs are packaged into scopes. Scopes can be nested, forming a tree structure that Lenny and Claude Code use to progressively disclose capabilities and to create new workers (Claude Code instances) when needed.
  - Agents and skills are defined in Markdown using the same conventions as in Claude Code, Cursor, etc.
  - A registry is a just a filesystem directory with child folders and files, allowing for easy version control with Git if desired.
- Composable registries (e.g. enterprise registry, team registry, and individual registry).
- Support for skill and agent hook scripts.
- Support for external MCP tools
- MCP interface for connecting tools and workflows to the main orchestrator agent

### 1.2 Problems It Solves

Lenny is designed to solve these problems:

1. Too many visible tools hurt planning quality.
2. As the number of tools, agents, and skills grow in an organization, several challenges arise:

- Limited solutions for managing skills and agents at the enterprise level
- Proliferation of very similar user interfaces
- Proliferation of Git repos or shared files with skill and agent definitions
- Policy enforcement

3. A single top-level skills/ or agents/ folder doesn't scale when dealing with hundreds or thousands of agents and skills.
4. In many cases, coding agents plus agents and skills defined in Markdown are a better choice than building and deploying custom agents. That's especially true in non-engineering organizations.

### 1.3 Design Principles

- Scopes control visibility, not authority.
- The registry is authored as a scope tree, compiled into execution boundaries, then loaded progressively at runtime.
- Scopes are authoring units. Boundaries are runtime units.
- Boundary topology is compiled first. Runtime adaptation happens only inside that compiled topology.
- Default deny everywhere.
- Storage, search, policy, workflow, audit, approvals, and related services are pluggable.

### 1.4 Key Decisions

- Implementation language: TypeScript.
- Each active execution boundary gets its own Agent SDK session.
- Workers can load more scopes inside a boundary without starting a new session.
- Crossing into a child boundary uses `boundary.enter` and creates a new session.
- A durable Lenny session can outlive individual SDK sessions.
- External API is REST plus SSE.
- The capability registry lives in a separate Git repo.
- A2A support is included in MVP.
- PostgreSQL with pgvector is the default store, but not required.
- The workflow engine is minimal by default and replaceable.
- Multiple registries can be layered with overlays.

---

## 2. Architecture

### 2.1 Main Components

- `Session API`: authenticates callers, starts or resumes work, handles approvals, streams events.
- `Orchestrator`: manages task state and routes worker requests.
- `Scope Resolver`: returns the worker's boundary-local view and loads more scopes inside the same boundary.
- `Policy / Lease Engine`: issues leases and checks them on every sensitive action.
- `Boundary Compiler`: turns the merged scope tree into execution boundaries.
- `Agent Runtime Manager`: starts, stops, resumes, and supervises Agent SDK sessions.
- `Tool Gateway`: single enforcement point for sensitive actions.
- `Approval Service`: issues one-time approval grants.
- `Credential Broker`: provides short-lived scoped credentials.
- `Audit / Telemetry`: logs structured events with trace IDs and redaction.
- `A2A Adapter`: talks to external A2A agents.
- `Sandbox Runner`: runs workers in isolated containers when needed.

### 2.2 Responsibility Rules

- The worker decides when to inspect or load scopes and when to enter child boundaries.
- The control plane validates and enforces every such move.
- The scope resolver never returns data outside the lease's boundary path.
- Raw MCP servers are never exposed directly to workers.
- MCP tools are presented as gateway-backed facades.
- The gateway re-checks lease, policy, visibility, approval, credentials, idempotency, and audit on every sensitive call.
- Workers never receive standing secrets.

---

## 3. Progressive Disclosure

### 3.1 Why It Exists

The problem is not only "too many tools." It is also "too many partly relevant capabilities visible at once." Lenny keeps the visible surface small and grows it only when the task needs more.

### 3.2 What a Worker Can See

Inside one execution boundary, a worker can see:

1. Boundary-envelope assets and metadata.
2. Assets from the entry scope or top-level scopes active at session start.
3. Shared assets inherited from ancestor scopes.
4. Descriptions of immediate child scopes that can be inspected or loaded next.
5. Descriptions of immediate child execution boundaries.

The worker cannot see sibling boundaries or anything outside its branch. Unloaded scopes inside the current boundary appear only as descriptions until loaded.

### 3.3 Disclosure Flow

1. Start with the entry scope plus inherited and boundary-level assets.
2. Use `scope.inspect` to see what a child scope contains and whether it can be loaded.
3. Use `scope.load` to add a same-boundary child scope into the current session.
4. Use `boundary.enter` only when the task must cross into a child execution boundary.
5. Repeat as the task grows.

Important rule: `scope.load` expands the current session. It does not create a new one. Only `boundary.enter` creates a new Agent SDK session.

---

## 4. Worker Model

### 4.1 Boundary Workers

Each execution boundary can run as one worker session. A worker may use:

- Skills visible through the current loaded-scope set
- Agents visible through the current loaded-scope set
- MCP tools visible through the current loaded-scope set, exposed as gateway-backed facades

Commands are different: they are user-invoked entrypoints, not model-autonomous capabilities.

The worker also gets:

- Boundary prompt layers
- Initially active context plus later loaded context
- Child scope summaries
- Child boundary descriptions
- Inherited shared assets
- Meta-tools for orchestration

### 4.2 Worker Lifecycle

The control plane starts each worker session with:

- A system prompt built from the task objective, inherited context, boundary context, entry scope context, and visible child summaries
- Visible MCP tool facades
- Meta-tools
- A lease

The Agent SDK `canUseTool` hook is where the control plane enforces the lease.

Subagents inside a boundary are not the same as child-boundary workers:

- In-boundary subagents share the boundary and lease.
- In-boundary subagents do not get orchestration meta-tools.
- `boundary.enter` creates a separate child boundary session with its own context, tools, and lease.

### 4.3 Leases

A lease defines what a worker can see and do. It includes:

- Task identity and parent task identity
- Boundary path
- Entry scope paths
- Loadable scope paths
- Loaded scope paths
- Optional caps on loaded scopes and boundary hops
- Tool allow and deny lists
- Permission mode
- Turn, depth, token, and time limits
- Expiry

Lease invariants:

- Loaded scopes must stay inside the lease boundary.
- Loading must happen through the control plane.
- Child leases must stay on descendant boundary paths.
- Approval authority is not part of the lease.

Tool permission detail:

- Omitting `allowed_tools` means use boundary defaults.
- `allowed_tools: []` means no tools.
- Deny lists take precedence.

### 4.4 Expansion Paths

Workers have two structurally different ways to expand:

1. Same-boundary expansion
   - Use `scope.inspect` or `scope.load`
   - Validate load frontier, scope allowlist, caps, and policy
   - Continue in the same session
2. Cross-boundary expansion
   - Use `boundary.enter`
   - Validate immediate-child relationship, depth, and budget
   - Issue a child lease
   - Start a new session

Default deployment limits are configurable. The spec expects controls such as:

- Max loaded scopes per boundary
- Max child-boundary depth, example default `5`
- Inherited budgets and turn caps with decrements

### 4.5 Parent-Child Communication

Child requests are structured and include:

- Objective
- Current facts
- Constraints

Child responses are structured and include:

- Findings
- Recommended next actions
- Additional required scopes
- Risks or policy flags
- Confidence
- Artifacts or evidence references

### 4.6 Execution Ladder

Workers should prefer the lightest valid option:

1. Continue in the initial scope.
2. Inspect or load more scopes in the same boundary.
3. Spawn an in-boundary subagent.
4. Enter a child boundary and start a new session.
5. Use a lightweight metadata-only or one-shot routing path when full hydration is unnecessary.

---

## 5. Scope Registry

### 5.1 Physical Structure

The registry is a recursive scope tree stored in a Git repo. Each scope can contain:

- `context.md`
- `manifest.yaml`
- `skills/`
- `commands/`
- `agents/`
- `shared/`
- `scopes/` for child scopes

A scope can have its own executable assets and child scopes at the same time. There is no strict container-versus-leaf split.

### 5.2 Overlays

Lenny accepts an ordered list of registries:

- First entry is the base registry.
- Later entries are overlays applied left to right.

Overlay rules:

- Same scope path across overlays merges assets.
- New scopes can be added by overlays.
- Existing scopes cannot be removed by overlays.
- `description` uses last-overlay-wins.
- Security-sensitive fields use most-restrictive-wins.
- Allowlists are intersected.
- Denylists are unioned.
- Same-name skills, commands, or agents in the same scope are compile-time errors.
- Overlays cannot silently shadow assets. Intentional replacement requires removal or renaming in the base layer.

Typical layering is base, then team, then personal.

### 5.3 Scope Manifest Schema

Each scope's `manifest.yaml` contributes to the runtime envelope and boundary compiler. It includes these major parts:

- `description`
- `mcpServers`
- `externalAgents`
- `sandbox`
- `policy`
- `permissions`
- `boundary`
- `inherit`

Important manifest rules:

- MCP credentials are references such as `credential://...`, not raw secrets.
- Credential values are resolved at runtime by the credential broker.
- MCP servers can declare mutating tools.
- External agents can define domain allowlists, maximum data class, timeouts, and retry limits.
- `sandbox` defines workspace roots, read-only paths, writable paths, denied paths, egress allowlist, symlink behavior, max duration, and permission mode.
- `policy` includes risk, data class, and whether approval is required.
- `permissions` controls visible tools and permission mode.
- In permissions, omitting `allowedTools` means inherit visible defaults, while `allowedTools: []` means allow none.
- `boundary` can include `mergeWithSiblings`, `mergeWithChildren`, `forceSplit`, and `neverCoReside`.
- `inherit` can whitelist or blacklist inherited shared context, skills, commands, and agents.

### 5.4 Shared Assets and Inheritance

Scopes can place assets in `shared/` so descendants inherit them.

Inheritance rules:

1. Shared assets accumulate from all ancestors by default.
2. A scope can whitelist or blacklist inherited assets.
3. Whitelists override blacklists.
4. Inheritance is computed across the full ancestor chain, not re-exported one hop at a time.
5. Overlay shared assets also accumulate.
6. Overlay name collisions still fail compilation.

### 5.5 Scripts in Skills

Skills may include executable scripts such as shell or TypeScript files. Those scripts still run under the scope's sandbox constraints.

### 5.6 Runtime Compilation

At startup, Lenny:

1. Walks the base registry.
2. Applies overlays in order.
3. Builds a merged `ScopeTree` plus fast lookup maps.
4. Compiles prompt layers, inherited shared-asset views, and effective policy envelopes.
5. Passes the merged tree to the boundary compiler.

Context handling rule:

- Overlay `context.md` files are compiled into ordered prompt layers with stable delimiters and contradiction linting.
- They are not blindly concatenated into one opaque blob.

Workers see the compiled combined view. They do not need to know which overlay supplied which asset.

### 5.7 Boundary Compiler

The boundary compiler turns the merged scope tree into execution boundaries. A boundary is the runtime envelope that maps to one Agent SDK session while preserving internal scope hierarchy.

Inputs:

- Merged scope tree
- Policy and security metadata
- Boundary hints from manifests and overlays
- Hard limits such as max scopes or tools per boundary

Hard topology rules:

- A scope belongs to exactly one boundary.
- Legal boundary shapes are:
  - one scope
  - merged siblings
  - parent plus all immediate children
- Scopes must stay separate when they have incompatible:
  - data classifications
  - credential domains
  - sandbox or egress requirements
  - permission modes that cannot safely co-host

Operational limits can also force separation, such as:

- Max scopes per boundary
- Max estimated tools per boundary
- Max skill metadata per boundary
- Max expected working-set size

Semantic heuristics may consider:

- Shared artifacts
- Shared instructions
- Shared tools or skills
- Common task patterns
- Ownership similarity

Supported author hints:

- `mergeWithSiblings`
- `mergeWithChildren: all`
- `forceSplit`
- `neverCoReside`

Compiler rules for hints:

- Invalid merge requests are compile-time errors.
- Later overlays can tighten boundary constraints but not loosen earlier ones.
- `forceSplit` overrides merge hints when they conflict.

Evaluation order:

1. Apply hard security and policy constraints.
2. Apply hard operational limits.
3. Apply human and manifest overrides.
4. Apply semantic optimization where still legal.
5. Validate the result.

Compiler output includes:

- The boundary tree
- Scope-to-boundary lookup
- Per-boundary merged manifest and internal scope hierarchy
- Progressive loading metadata
- Explanations for merge and split decisions

Required properties:

- Deterministic
- Explainable
- Auditable
- Runtime-adaptive only inside compiled boundaries
- Testable with fixture registries

---

## 6. Meta-Tools

Workers use a small fixed set of meta-tools:

- `scope.inspect`: inspect a loadable same-boundary scope
- `scope.load`: load a same-boundary scope into the current session
- `boundary.enter`: delegate to an immediate child boundary
- `boundary.search`: search visible scopes and child boundaries without flooding the prompt
- `approval.request`: ask for human approval for a planned action
- `task.update`: send structured findings or artifacts to the parent

Availability rules:

- Only the main boundary session gets orchestration tools such as `scope.inspect`, `scope.load`, `boundary.enter`, and `boundary.search`.
- All workers can use `approval.request` and `task.update`.
- In-boundary subagents do not get orchestration tools.

Load contract:

- `scope.inspect` is cheap metadata discovery.
- `scope.load` is the auditable expansion step.
- Successful loads make new context layers, skills, agents, and MCP tool facades visible for the rest of the session unless the worker moves into a different boundary.

---

## 7. Request Lifecycle

End-to-end flow:

1. `Session API` receives the request, authenticates the caller, and creates the root task and trace ID.
2. The orchestrator hydrates the root execution boundary and its initial view.
3. A root lease is issued.
4. The root worker session starts with prompt, tools, and meta-tools.
5. The worker reasons, searches, inspects, and decides whether to stay in the boundary or delegate.
6. Same-boundary work uses `scope.load`. Cross-boundary work uses `boundary.enter`.
7. Before a mutating tool call, the worker supplies an `operation_id`.
8. The gateway checks lease, permissions, policy, and approval requirements.
9. If approval is required, the Session API streams an approval event that identifies the exact action, normalized arguments, risk, rollback context, and operation ID.
10. The user resolves the approval. The approval service issues a one-time grant token.
11. The worker retries the exact same operation with that grant.
12. The gateway validates the grant, enforces idempotency, injects credentials, executes, records the result, and returns it.
13. The task continues recursively as needed.
14. On completion, the result is returned and the audit trail includes inspections, loads, boundary entries, approvals, and replay handling.

Critical rule: same-boundary loads do not create new sessions. Boundary hops do.

---

## 8. Security Model

### 8.1 Threat Model

Main threat: the model tries to act outside its authorized scope. Secondary threat: credentials leak from the runtime to external systems.

### 8.2 Mandatory Rules

1. Forced mediation
   - Workers never talk directly to protected systems.
   - All protected actions go through the Tool Gateway.
   - Raw MCP endpoints are never directly exposed.
   - If a worker tries to bypass the gateway with direct network access, sandbox egress policy should block it.
2. No standing credentials
   - Workers and sandboxes do not hold durable secrets.
   - The credential broker issues short-lived scoped credentials per call.
   - Manifest credential entries are references, not values.
3. Approval is cryptographic, not advisory
   - Approval uses signed, expiring, action-bound grant tokens.
4. Least privilege per action
   - Every invocation gets the smallest practical scope and lifetime.
5. Mutations must be idempotent or rejected
   - If safe retry semantics are not possible, autonomous retry is not allowed.
6. Default deny
   - Missing visibility, lease scope, policy allowance, or environment permission means deny.
7. Verification before trust
   - For code or data changes, run tests and validators before reporting success.
8. Lease-scoped access
   - `canUseTool` checks enforce the lease on every tool call.

### 8.3 Sandbox Isolation

When a boundary uses a sandbox, the design expects:

- Ephemeral container or job
- No default credentials
- Egress allowlist
- Deterministic filesystem merge rules
- CPU, memory, and wall-clock limits
- PR-based workflows preferred for code changes

Filesystem merge rules:

- `workspaceRoots`: union only when scopes may co-reside
- `readOnlyPaths`: union
- `writablePaths`: intersection
- `deniedPaths`: union
- `followSymlinks: false`: sticky

---

## 9. Policy Engine

### 9.1 Evaluation Model

The Tool Gateway calls policy for sensitive actions. Policy receives a context that includes:

- Actor identity, roles, scopes, and tenant
- Boundary manifest and loaded-scope state
- Tool name
- Environment
- Lease
- Arguments
- Optional mutation `operationId`

Policy can return:

- allow
- deny
- requires approval

### 9.2 Evaluation Order

1. Lease boundary check
2. Lease expiry check
3. Lease tool allow or deny check
4. Manifest visibility check
5. Authored policy rules
6. Manifest approval requirement check
7. Actor scope check
8. Default deny

### 9.3 Policy Rules

Policy rules are registry-authored and may be overlaid per environment. They can use conditions such as:

- Roles
- Scope paths
- Environments
- Risk levels
- Data classes

Effects are:

- allow
- deny
- require approval

Example patterns the spec expects:

- deny production writes for some operator roles
- require approval for high-risk irreversible actions

---

## 10. Approval System

### 10.1 Lifecycle

1. The gateway determines approval is required.
2. It creates an approval request bound to exact boundary, scope, action, arguments, operation ID, risk, and expiry.
3. The client receives an `approval.requested` event.
4. A human approves or denies.
5. The system creates a one-time grant token.
6. The worker retries the same operation with that token.
7. The gateway validates token, expiry, usage status, arguments, and operation ID.
8. The action is audited.

### 10.2 Key Properties

- Action-bound
- One-time
- Expiring, example default `10 minutes`
- Bound to the requester identity and root task context
- Non-inheritable unless the wrapper explicitly binds it that way
- Human-resolved outside the worker
- Workers and children cannot approve their own actions

Approval authority does not live in leases.

### 10.3 Mutating Action Contract

Every mutating action must follow the same contract:

1. Worker supplies a stable `operation_id`.
2. Gateway computes or validates the idempotency key.
3. Gateway records intent before dispatch.
4. Downstream systems receive the idempotency key when supported.
5. Retries and resumes check prior state before dispatching again.
6. Approval-bound retries must reuse the same `operation_id`.

---

## 11. External Integration

### 11.1 MCP Tools

External systems are exposed through MCP servers configured in manifests, but workers only see gateway-backed tool facades.

Runtime contract:

1. MCP server processes are owned by the control plane or sandbox, not by the worker prompt.
2. Server lifecycle may be per-call, pooled, or boundary-isolated.
3. The gateway proxies worker-facing calls to the real server transport.
4. Credentials are handed off per call and scrubbed after use.
5. A tool is callable only if it is present in the merged manifest, visible through loaded scopes, lease-allowed, and policy-allowed.
6. Mutating tools must follow the mutating action contract.

### 11.2 A2A Agents

External A2A agents are configured in `externalAgents` with an `a2aCardUrl`. The adapter:

1. Fetches and caches the agent card.
2. Validates the target domain.
3. Applies redaction and data minimization.
4. Authenticates using `authRef`.
5. Dispatches with bounded timeout and retry.
6. Streams results through the normal event pipeline.

Supported patterns:

- synchronous `/tasks/send`
- streaming `/tasks/sendSubscribe` over SSE

Outbound trust rules:

- Domain must be allowlisted.
- Data class sent out must not exceed the agent's `maxDataClass`.
- Tenant identity and trace IDs must be preserved.
- Timeout, retry, and circuit-breaker policy must be explicit.
- All traffic must be audited.

### 11.3 Client Integration

The public API is REST plus SSE. Core endpoints include:

- `POST /sessions`
- `GET /sessions/:id/events`
- `POST /sessions/:id/approvals/:approvalId/resolve`
- `GET /sessions/:id/tasks`
- `GET /sessions/:id/approvals`

This is enough for LibreChat, Slack-style clients, and custom UIs.

---

## 12. Execution Modes

### 12.1 Synchronous

Use this for short chat-turn work:

- bounded loop
- deterministic stop conditions
- immediate streaming back to the client

### 12.2 Asynchronous

Use this for waits, retries, approvals, CI, or long-running work:

- orchestrator hands off to the workflow engine
- Claude acts as a decision function inside workflow steps, not as a free-running daemon
- workflow engine manages pause, resume, retry, and timeout
- results stream back as they complete

The workflow engine is pluggable. The default is minimal and in-memory. Production systems should use something like Temporal or BullMQ.

### 12.3 Lenny Sessions

A Lenny session is durable state above short-lived SDK sessions.

Persisted state includes:

- current or last active Claude session ID
- active boundary sessions and their states
- loaded scopes per boundary session
- compacted conversation state as structured summaries, not raw transcripts
- structured findings from child sessions
- pending approvals
- mutation intent and completion records
- expiry metadata

Lifecycle pattern:

1. Hydrate
2. Work
3. Spin down idle SDK sessions
4. Resume later

Example defaults:

- idle timeout per boundary session: `15 minutes`
- overall session expiry: `24 hours`

Shared storage mounts may help resume, but structured app-level state is the portable fallback.

---

## 13. Audit and Observability

### 13.1 Logged Events

Every significant event carries a trace ID. Important examples include:

- `task.created`
- `task.updated`
- `lease.issued`
- `lease.expired`
- `worker.started`
- `worker.stopped`
- `tool.requested`
- `tool.allowed`
- `tool.denied`
- `tool.executed`
- `tool.failed`
- `scope.loaded`
- `scope.inspected`
- `boundary.entered`
- `mutation.recorded`
- `mutation.replayed`
- `approval.requested`
- `approval.resolved`
- `policy.evaluated`

### 13.2 PII Redaction

Manifests or policy rules can mark fields for redaction in logs, for example bank account numbers or SSNs.

### 13.3 Audit Sink

The audit sink is pluggable. The default writes through the storage adapter, but external SIEM or compliance systems are supported.

---

## 14. Pluggable Interfaces

Major pluggable interfaces include:

- `StorageAdapter`
- `PolicyEngine`
- `RegistrySearchProvider`
- `WorkflowEngine`
- `AuditSink`
- `CredentialBroker`
- `A2AAdapter`
- `ToolExecutor`
- `BoundaryCompiler`
- `SessionStore`
- `WorkerAllocator`
- `IdleTimeoutPolicy`
- `CostGovernor`

Typical defaults are simple for development and replaceable for production. Examples:

- PostgreSQL or in-memory for storage
- rule-based RBAC/ABAC for policy
- in-memory search or pgvector-backed search
- in-memory workflow engine for MVP
- no-op or basic credential broker in development

---

## 15. End-to-End Examples

### 15.1 Read-Only Knowledge Query

User asks for a parental leave policy in Canada.

1. Root enters the `hr` boundary.
2. The `hr` worker inspects `benefits`.
3. It loads `benefits` inside the same session.
4. The policy-search tool becomes visible.
5. The worker returns the answer.

Point of the example: crossing into `hr` needed a new session, but loading `benefits` did not.

### 15.2 Financial Write with Approval

User asks to pay an invoice.

1. Root enters `finance`.
2. The finance worker loads `ap`.
3. It looks up the invoice and drafts the payment.
4. It tries payment submission with a stable `operation_id`.
5. Policy or manifest requires approval.
6. User approves.
7. The worker retries the exact same action with the grant token.
8. Gateway validates grant and idempotency, then executes.

Point of the example: approvals are exact-action retries, not loose confirmations.

### 15.3 Task That Grows Mid-Execution

User asks to pay an invoice, but vendor compliance turns out to be incomplete.

1. Finance work starts as above.
2. Finance discovers it needs procurement information.
3. It checks whether `procurement` is in the same boundary.
4. In this deployment, it is a separate boundary due to credential and egress differences.
5. Finance enters `procurement`.
6. Procurement returns structured findings.
7. Finance synthesizes the result and asks the user how to proceed.

Point of the example: load within a boundary first, then create a new session only when the compiled boundary requires it.

### 15.4 Engineering Workflow

User asks for a code change, tests, and a PR.

1. Root enters `engineering`.
2. The worker runs in an ephemeral container.
3. It loads the needed engineering scope, edits code, and runs tests.
4. The gateway injects short-lived Git credentials and records mutation intents.
5. PR creation requires approval.
6. User approves.
7. The worker retries the same PR-creation operation ID.
8. The PR is created and returned with summary and test results.

---

## 16. MVP Build Sequence

Recommended build order:

1. Core types and pluggable interfaces
2. Scope tree loader and compiler
3. Boundary compiler
4. Policy engine and lease management
5. Tool Gateway
6. Agent Runtime Manager and boundary meta-tools
7. Orchestrator
8. Approval flow
9. A2A adapter
10. Audit logging
11. Session API
12. Example scope registry

Why this order:

- Scope and boundary compilation are the base of progressive disclosure.
- Policy and the gateway must exist before any real tool execution.
- The example registry is part of proving the design end to end.

---

## 17. Verification

The design should be verified with:

- Unit tests for scope loading, boundary compilation, policy, leases, gateway behavior, and approvals
- An integration test that covers request to result across scope loading, child sessions, approvals, and tool execution
- Security tests for boundary escape, invisible siblings, invisible out-of-scope tools, approval bypass, missing policy, expired leases, and grant-token reuse
- Reliability tests for duplicate retries, crash recovery from intent records, and approval-bound retry safety
- An example scope registry that exercises nested scopes, shared inheritance, read and write tools, and approval flows without external dependencies

---

## 18. Key Risks and Mitigations

- Agent SDK changes
  - Pin versions and wrap SDK calls behind internal interfaces.
- Scope trees that get too deep or wide
  - Enforce caps on depth, breadth, and child count.
- Runaway session spawning
  - Use depth limits, turn budgets, and cost caps. Prefer same-boundary loading before spawning.
- Approval fatigue
  - Tune thresholds, batch where possible, and allow low-risk non-prod auto-approval where appropriate.
- Prompt injection through manifests or context
  - Keep the registry in reviewed Git-controlled storage and read-only to workers.
- Latency from multi-hop routing
  - Keep policy fast, cache compiled registry data, and prefer same-boundary loading.
- Immature boundary heuristics
  - Start with conservative merges and add telemetry.
- Overlay conflicts and boundary-hint conflicts
  - Resolve with explicit precedence rules and compile-time validation.
