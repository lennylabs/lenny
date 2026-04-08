# Lenny Technical Design Critique

**Date:** 2026-03-26
**Scope:** Evaluation against two success criteria for OSS adoption
**Reviewed:** `docs/technical-design.md` (Draft, 2026-03-23)

---

## Evaluation Criteria

1. **Interfaces & Modularity** -- Well-thought interfaces and modular design that allow growth without major refactors or breaking backward compatibility.
2. **Workflow Universality** -- Support for any agentic workflow setup, including cloud coding agents (Cursor Background Agents, Claude Code cloud, OpenAI Codex, Devin, GitHub Copilot Coding Agent) and generic agentic workflows (LangGraph Cloud, CrewAI, Temporal-based agents, Modal, Fly Machines).

---

## Executive Summary

Lenny's design occupies a genuine gap in the market. No existing platform combines **runtime-agnostic agent hosting**, **strong compute isolation**, **gateway-mediated orchestration with policy enforcement**, and **recursive delegation as a platform primitive**. The closest analogues are either orchestration-rich but isolation-poor (LangGraph, Temporal) or isolation-strong but orchestration-absent (Modal, Fly Machines). Lenny could be both.

The design is architecturally mature for a v1 -- role-based storage interfaces, explicit extraction triggers for the gateway monolith, one-session-only pod invariant, and delegation lease narrowing are all strong decisions. However, **10 structural issues** threaten long-term extensibility and backward compatibility, and **5 capability gaps** limit the platform's ability to serve the full spectrum of agentic workloads.

**Bottom line:** The foundation is strong. The issues identified below are fixable without a redesign -- most require adding abstraction layers or extension points that should be in the design before v1 ships, because retrofitting them later will break existing adapters and clients.

---

## Part 1: Interfaces & Modularity

### 1.1 The Adapter-Binary Protocol Needs Capability Negotiation (Critical)

**Problem.** The stdin/stdout JSON Lines protocol (Section 15.4.1) is the *only* interface third-party runtime authors implement. It defines message types (`prompt`, `tool_result`, `heartbeat`, `shutdown` inbound; `response`, `tool_call`, `status` outbound) but lacks:

- **Capability handshake.** The binary cannot declare what it supports at runtime. If a binary does not support checkpointing, the adapter discovers this only when `Checkpoint` fails. There is no negotiation where the binary says "I support prompt and tool_call; I do not support heartbeat or checkpoint."
- **Streaming semantics for `response`.** The `final` field on `agent_text` (gateway-facing) implies streaming, but the binary protocol does not define multi-chunk framing. Every third-party author will invent their own convention.
- **Error propagation contract.** What happens on malformed JSON to stdout? Does one bad line kill the session or get skipped?
- **Forward compatibility.** No rule for unknown `type` values. Adding new message types will break existing binaries.

**Why it matters.** Kubernetes's CRI, Terraform's provider protocol, and Envoy's xDS all have versioned capability negotiation. These projects survived a decade of evolution because the plugin boundary was designed for it. Lenny's binary protocol is the community-facing contract -- getting it wrong here means breaking every adapter on the first protocol change.

**Suggestion.** Add a mandatory `init` handshake:

```jsonl
← {"type":"init","protocolVersion":1,"capabilities":["prompt","tool_call","checkpoint","heartbeat"]}
→ {"type":"init_ack","protocolVersion":1,"supportedCapabilities":["prompt","tool_call"]}
```

The adapter then adapts behavior: no `heartbeat` if unsupported, no `checkpoint` if undeclared. Unknown `type` values are silently ignored by both sides. Protocol version mismatches produce a clean startup error. This is a one-time design cost that prevents years of compatibility pain.

---

### 1.2 Cross-Store Transaction Boundaries Are Undefined (High)

**Problem.** The six role-based storage interfaces (SessionStore, LeaseStore, TokenStore, QuotaStore, ArtifactStore, EventStore) are the strongest modularity decision in the design. But several operations span multiple stores atomically:

- Session creation touches SessionStore + QuotaStore + CredentialPoolStore
- Session completion touches SessionStore + EventStore (billing) + ArtifactStore (seal) + QuotaStore (release) + CredentialPoolStore (release)
- User erasure (Section 12.8) cascades across all stores

The design never specifies how cross-store atomicity works. If SessionStore and EventStore are both Postgres, they can share a transaction. But ArtifactStore is MinIO (non-transactional), so the "seal-and-export invariant" (Section 7.1) is eventually consistent by definition -- yet the design treats it as atomic.

**Suggestion.** Introduce a `UnitOfWork` coordinator for multi-store operations. For Postgres-backed stores, this wraps a DB transaction. For cross-backend operations (Postgres + MinIO), it implements a saga with compensating actions. Make the transactional boundary explicit in the interface rather than leaving it as an implementation detail that diverges across deployments.

---

### 1.3 No Extension Point Without Recompiling the Gateway (High)

**Problem.** Every successful long-lived OSS project has an out-of-process extension mechanism:

| Project | Extension Mechanism |
|---------|-------------------|
| Kubernetes | Admission webhooks, CRDs, API aggregation |
| Terraform | Provider binaries (separate process) |
| Envoy | WASM filters, ext_proc gRPC |
| Lenny | Nothing -- all extension requires forking |

The Policy Engine (Section 4.8) has the right shape -- modular evaluators -- but they are hardcoded. A deployer who wants a custom authorization check, a proprietary rate limiter, or custom metadata injection must fork the gateway.

**Suggestion.** Define a `RequestInterceptor` interface (Go interface internally, gRPC service externally) that the gateway calls at configurable phases (PreAuth, PostAuth, PreRoute, PostRoute). The existing Policy Engine evaluators become built-in interceptors. External interceptors are called via gRPC, like Kubernetes admission webhooks. This can ship as internal-only in v1 with the external gRPC option documented as "planned" -- but the interface must exist in the code from the start.

---

### 1.4 CRD Used as Coordination Protocol Creates Hidden Coupling (Medium)

**Problem.** The `AgentPod` CRD `.status.claimedBy` field is used as a distributed lock via optimistic locking on `resourceVersion` (Section 4.6). This is unusual -- most Kubernetes controllers reconcile declarative state; they do not use CRD status fields as coordination primitives. The consequence: any CRD schema change to the claim-related fields requires coordinating the gateway's hot-path claim logic, the controller's reconciliation logic, and the conversion webhook simultaneously.

**Suggestion.** Separate the coordination protocol from the CRD. Use a Kubernetes Lease object (or a purpose-built `AgentPodClaim` CRD) for the optimistic-locking claim mechanism. Let the `AgentPod` CRD describe desired/observed state without serving double duty as a distributed lock. Kubernetes itself uses Lease objects for leader election rather than embedding election state in controller CRDs -- for this exact reason.

---

### 1.5 MCP Tool Versioning Will Cause Combinatorial Pain (Medium)

**Problem.** Section 15.5 says breaking MCP tool changes create a new tool (e.g., `create_session_v2`). With 17 tools, clients will mix versions arbitrarily (`create_session` v1 + `send_prompt` v2 + `list_sessions` v1). The gateway must support every combination forever.

gRPC services version at the package level (`v1.SessionService` vs `v2.SessionService`). Kubernetes versions APIs by group (`/apis/apps/v1`). Both ensure all operations within a version are consistent.

**Suggestion.** Version MCP tools as a group. When any tool needs a breaking change, release a new MCP capability version (`lenny/v2`) with the full updated tool set. The gateway advertises supported versions; the client selects one at connection time. Old versions are maintained for the documented deprecation window. This mirrors how the Language Server Protocol (LSP) handles capability evolution.

---

### 1.6 Credential System Conflates Provider and Delivery (Medium)

**Problem.** The `CredentialProvider` interface cleanly abstracts adding new LLM providers. But the two delivery modes (`direct` and `proxy`) are configured per pool, not factored into their own interface. Adding a third delivery mode (e.g., Vault sidecar injection, workload identity federation, mutating webhook) requires modifying the gateway's credential assignment logic, the adapter's credential handling, and the pool configuration schema simultaneously.

**Suggestion.** Factor into two orthogonal interfaces:

```
CredentialProvider -- how to mint credentials for a provider (Anthropic, Bedrock, Vertex...)
CredentialDelivery -- how to deliver credentials to a pod (direct RPC, proxy URL, Vault sidecar...)
```

The gateway composes provider + delivery at assignment time. New delivery modes implement `CredentialDelivery` without touching `CredentialProvider` or pool configuration.

---

### 1.7 Event/Notification Topology Is Fragmented (Medium)

**Problem.** The design uses at least six independent notification mechanisms:

- SSE for client streaming
- Redis pub/sub for cert deny list propagation
- Postgres LISTEN/NOTIFY as Redis fallback
- Webhook POST for billing/callbacks
- gRPC streaming for pod-to-gateway events
- MCP elicitation chain for human-in-the-loop

There is no unified event taxonomy or bus. When someone wants "notify me when any session in this tenant exceeds 80% token budget," they must discover which mechanism carries that information.

**Suggestion.** Define an internal `EventBus` interface that all subsystems publish to. Concrete implementation: Redis Streams (persistent, replayable) with Postgres fallback. All current notification mechanisms become consumers. New consumers (Slack integration, custom alerting, third-party billing) become simple subscribers without new plumbing.

---

### 1.8 Missing First-Class Abstractions That Will Be Needed Early

**Session Templates.** The WorkspacePlan (Section 14) has 15+ fields. Every CI/CD pipeline will define the same plan repeatedly. There is no reusable template concept. Adding it later requires a new API endpoint plus migration logic. Define a `SessionTemplate` (named, stored WorkspacePlan) as a v1 resource.

**Headless Session Mode.** The design is optimized for interactive sessions with an attached client. CI/CD wants fire-and-forget: create session, send prompt, disconnect, get callback. When no client is attached, who handles elicitations? Define a `headless` mode where elicitations are auto-denied (or answered by policy) and the session runs autonomously.

**Multi-Cluster Constraints.** The design is single-cluster. At minimum, document that session IDs must be globally unique (they appear to be) and that storage interfaces do not assume single-cluster topology. This prevents deployers from making assumptions that will break when they need to federate later.

---

### 1.9 What the Design Gets Right (Do Not Change These)

- **Role-based storage interfaces** -- the strongest modularity decision. Do not retreat to generic CRUD.
- **Gateway-centric topology** with pods as internal workers -- eliminates a class of security/coordination problems.
- **One-session-only pod invariant** -- expensive but eliminates cross-session leakage by construction.
- **Explicit extraction triggers** for gateway subsystems -- the right alternative to premature microservices.
- **Delegation lease narrowing** -- children always have strictly fewer permissions than parents.
- **Dual-mode local dev** (`make run` + `docker compose up`) -- will drive contributor adoption.
- **REST/MCP consistency contract** with shared service layer (Section 15.2.1) -- prevents API surface drift.
- **Expand-contract schema migrations** -- the right discipline for zero-downtime evolution.

---

## Part 2: Workflow Universality

### 2.1 Competitive Landscape

Lenny was evaluated against two categories of platforms:

**Cloud Coding Agents** (all interactive, code-focused):

| Platform | Execution Model | Key Capabilities | Multi-Agent | Self-Hostable |
|----------|----------------|-----------------|-------------|---------------|
| Cursor Background Agents | Cloud VM per task | File edit, terminal, git, PR creation | No | No |
| Claude Code (SDK) | Local/embedded/cloud | File edit, terminal, git, MCP tools, sub-agents | Yes (SDK) | Yes |
| GitHub Copilot Coding Agent | Codespaces-based | File edit, terminal, git, CI iteration | No | No |
| Devin | Persistent cloud VM | File edit, terminal, browser, git, long-running | No | No |
| OpenAI Codex | Sandboxed container | File edit, terminal, git, parallel tasks | No | CLI only |

**Generic Agentic Platforms** (broader workflow support):

| Platform | Core Strength | State Model | Isolation | Multi-Agent |
|----------|--------------|-------------|-----------|-------------|
| LangGraph Cloud | Stateful graph + checkpointing | Checkpointed per thread | Process-level | Subgraphs, supervisor |
| CrewAI | Role-based multi-agent | In-memory per run | None | Core feature |
| Temporal | Durable execution, fault tolerance | Event-sourced replay | Namespace | Child workflows |
| Modal | Serverless GPU compute | Volumes + queues | gVisor containers | Fan-out via .map() |
| Fly Machines | Fast microVMs, stop/start | Volumes (region-pinned) | Firecracker | Build-your-own |
| Dagger | Cached containerized pipelines | Stateless (cache) | OCI containers | DAG composition |

### 2.2 Where Lenny Exceeds the Market

**Runtime agnosticism.** No other platform supports pluggable agent runtimes. Cursor runs Cursor's agent, Devin runs Devin. Lenny's RuntimeType + adapter contract means it can host Claude Code, a custom LangGraph agent, or any future runtime. This is the strongest differentiator and the single most important thing to protect via the adapter protocol (see 1.1).

**Isolation + orchestration combined.** Existing platforms force a choice: LangGraph/Temporal give rich orchestration but weak isolation (shared process/namespace). Modal/Fly give strong isolation but no orchestration. Lenny's gateway provides orchestration, policy, and state while pods provide isolated execution. This combination does not exist elsewhere.

**Recursive delegation with policy enforcement.** LangGraph has subgraphs, Temporal has child workflows, but neither enforces "child has strictly fewer permissions and budget than parent" at the platform level. Lenny's delegation lease narrowing is unique.

**Multi-tenancy.** None of the surveyed platforms offer tenant isolation, per-tenant quotas, or RLS-backed data separation. Lenny is designed for platform builders serving multiple customers.

**Credential brokering.** Every other platform mounts an API key. Lenny's credential leasing, pool rotation, fallback chains, and LLM reverse proxy are far more sophisticated.

### 2.3 Capability Gaps That Limit Universality

#### Gap 1: No Graph/Pipeline Execution Model (High Impact)

**The problem.** Lenny models agents as opaque processes that receive prompts and produce responses. This covers coding agents perfectly but cannot natively express the patterns that LangGraph, CrewAI, and Temporal users depend on:

- **Stateful graphs** where the LLM decides which node to execute next
- **Multi-agent crews** where agents with different roles collaborate on a task
- **Durable workflows** where each step is independently retryable and the workflow survives crashes

Lenny's recursive delegation is the closest analogue, but it operates at a much coarser granularity -- each "step" is a full pod with workspace setup, not a lightweight function call within a graph.

**Why it matters.** A research agent that does "search -> analyze -> synthesize -> review" should not require four pods with four workspace materializations. LangGraph runs this as a single process with four graph nodes. Temporal runs it as one workflow with four activities. Lenny has no equivalent.

**Suggestion.** This is not a redesign -- it is an additional execution mode. Define a **lightweight task mode** alongside the current session model:

- A "task pod" runs a long-lived agent process that accepts multiple tasks sequentially (or concurrently)
- The gateway routes individual tasks to running pods rather than claiming a new pod per task
- The pod lifecycle is decoupled from the task lifecycle
- This enables "function-as-a-service" patterns where a single pod serves many short tasks

This would allow deployers to build LangGraph-like graph execution on top of Lenny, using task pods as nodes and the gateway's delegation system for the graph edges. The one-session-only invariant can remain the default, with the lightweight task mode as an opt-in that requires explicit security acknowledgment (since it reuses pods across tasks).

#### Gap 2: No Streaming Artifact / Inter-Agent Data Pipeline (High Impact)

**The problem.** The file export model (Section 8.8) copies files from parent workspace to child workspace at delegation time. This is a batch transfer. There is no mechanism for:

- Streaming data between agents during execution (e.g., agent A produces partial results that agent B consumes incrementally)
- Shared artifact references without copying (e.g., agent B reads from agent A's sealed workspace without a full copy)
- Large dataset handoff (the `fileExportLimits` of 100 files / 100MB is limiting for data processing workflows)

**Why it matters.** Data processing pipelines (the bread and butter of LangGraph and Temporal) need streaming data flow. A "summarize 1000 documents" workflow should not require copying all documents into each child pod's workspace.

**Suggestion.** Add a **shared artifact reference** mechanism. Instead of copying files, the parent can grant the child a scoped, read-only reference to an artifact in the ArtifactStore. The child's runtime adapter fetches files on demand via the gateway. This preserves the security model (gateway-mediated, no direct pod-to-pod access) while avoiding O(N) workspace copies for fan-out patterns.

```yaml
fileExport:
  - type: artifactRef          # new: reference, not copy
    artifactId: "art_abc123"   # parent's sealed workspace or specific artifact
    mountPath: "/input"        # read-only mount point in child
    scope: "read"              # read-only; no mutation
```

#### Gap 3: No Browser or GUI Tool Capability at the Platform Level (Medium Impact)

**The problem.** Devin's key differentiator is a full browser for web research, testing web apps, and interacting with web UIs. Lenny's design has no browser concept. The egress profiles (Section 13.2) allow internet access, but browser automation (Playwright, Puppeteer) requires specific runtime support and the platform does not assist with it.

**Why it matters.** Web research agents, QA/testing agents, and data scraping agents all need browser access. These are high-value use cases that Lenny's current design pushes entirely to the runtime author.

**Suggestion.** This is correctly a runtime concern, not a platform concern. But the platform should explicitly support it:

- Document a `browser` capability in the RuntimeType schema so deployers can declare which runtimes include browser tooling
- Ensure the egress profiles support the network access patterns browsers need (WebSocket, varied HTTPS endpoints)
- Consider a reference "browser sidecar" container image that runtimes can include -- a headless Chromium with a well-known API that any adapter can use
- Add a `display` capability for runtimes that expose a VNC/noVNC endpoint (useful for debugging and for Devin-style "watch the agent work" UIs)

#### Gap 4: No Native Human-in-the-Loop Beyond Elicitation (Medium Impact)

**The problem.** The MCP elicitation chain (Section 9.2) handles structured human-in-the-loop interactions: the agent asks a question, the human answers. But more complex HITL patterns are common in agentic workflows:

- **Approval gates:** "The agent has generated a plan. Approve before execution." (LangGraph's `interrupt_before` pattern)
- **Collaborative editing:** The human and agent co-edit a document in real-time
- **Supervised tool use:** The human reviews and approves each tool call (Claude Code's permission model)

Lenny has `approve_tool_use`/`deny_tool_use` for tool approval, but no general-purpose "pause workflow, present state to human, wait for decision" primitive beyond elicitation.

**Suggestion.** The existing elicitation mechanism is close. Extend it with:

- **Structured approval elicitation type** that presents a plan/diff and accepts approve/reject/modify
- **Checkpoint-and-wait** pattern: the session checkpoints, enters a `waiting_for_human` state (timer paused, pod can be released to save resources), and resumes when the human responds. This is how LangGraph's interrupt works -- the graph checkpoints and the thread waits for the next human message.

The pod-release optimization is important: an agent waiting 2 hours for human review should not hold a pod.

#### Gap 5: No First-Class Observability Contract for Runtimes (Low Impact, High Long-Term Value)

**The problem.** The design has thorough observability for the platform (Section 16) but does not define what runtimes should report. Token usage is tracked via `ReportUsage` RPC, but there is no standard for:

- Agent "thinking" / reasoning trace visibility
- Tool call latency and success rates from the agent's perspective
- Agent-level metrics (context window utilization, memory pressure, task complexity estimates)
- Structured agent logs (distinct from runtime adapter logs)

**Why it matters.** LangSmith's entire value proposition is agent observability. If Lenny runtimes report different things in different formats, the platform cannot provide unified dashboards or debugging tools.

**Suggestion.** Define optional observability message types in the adapter-binary protocol:

```jsonl
→ {"type":"trace","span":"tool_call","tool":"file_read","duration_ms":45,"status":"ok"}
→ {"type":"metric","name":"context_tokens","value":85000,"max":100000}
→ {"type":"log","level":"info","message":"Planning approach for auth refactor","structured":{"step":"planning"}}
```

These are optional -- minimum viable adapters ignore them. But runtimes that emit them get platform-level dashboards for free. This becomes a powerful incentive for runtime authors to adopt the observability contract, giving Lenny a LangSmith-like story without building a separate product.

---

## Part 3: Prioritized Recommendations

### Must-Have Before v1 (Prevent Breaking Changes Later)

| # | Recommendation | Section | Rationale |
|---|---------------|---------|-----------|
| 1 | Add capability negotiation to adapter-binary protocol | 1.1 | The community contract. Changing it after adoption is nearly impossible. |
| 2 | Define cross-store transaction boundaries | 1.2 | The seal-and-export invariant depends on it; undefined behavior will become relied-upon behavior. |
| 3 | Version MCP tools as a group, not individually | 1.5 | Combinatorial version explosion is unrecoverable once clients depend on mixed versions. |
| 4 | Add `SessionTemplate` as a first-class resource | 1.8 | Every CI/CD user will need this; adding it later changes the API surface. |
| 5 | Add `headless` session mode for batch/async workloads | 1.8 | Without it, CI/CD integration requires an always-connected client, which is architecturally wrong. |

### Should-Have for v1 (Significant Extensibility Value)

| # | Recommendation | Section | Rationale |
|---|---------------|---------|-----------|
| 6 | Plan the `RequestInterceptor` extension point | 1.3 | Define the interface now, ship external gRPC support later. The interface must exist in code from day one. |
| 7 | Separate CRD state from CRD coordination (Lease for claims) | 1.4 | Prevents CRD schema evolution from being coupled to the claim hot path. |
| 8 | Factor credential provider vs. delivery interfaces | 1.6 | Prevents credential delivery mode additions from requiring gateway changes. |
| 9 | Add shared artifact references for delegation | 2.3/Gap 2 | Without it, fan-out delegation hits O(N) copy overhead that makes data-processing workflows impractical. |
| 10 | Define optional observability message types in binary protocol | 2.3/Gap 5 | The protocol is hard to change post-v1; including optional observability types now costs nothing and enables a LangSmith-like story later. |

### Nice-to-Have / Post-v1 (Plan for but Don't Block On)

| # | Recommendation | Section | Rationale |
|---|---------------|---------|-----------|
| 11 | Internal EventBus interface | 1.7 | Unifies fragmented notification mechanisms. Can be introduced incrementally. |
| 12 | Lightweight task mode (multi-task pods) | 2.3/Gap 1 | Enables LangGraph-like graph execution. Requires careful security design for pod reuse. |
| 13 | Browser sidecar reference image | 2.3/Gap 3 | High-value for web-capable runtimes but can ship as a community contribution. |
| 14 | Checkpoint-and-wait with pod release | 2.3/Gap 4 | Optimizes resource usage for long HITL waits. Not blocking for v1. |
| 15 | Multi-cluster federation constraints | 1.8 | Document now, implement later. |

---

## Conclusion

Lenny's technical design is remarkably thorough and architecturally sound. The gateway-centric model, runtime agnosticism, delegation primitives, and credential brokering place it in a category of one. The main risks are not in what the design does, but in what it does not yet account for:

1. **The adapter-binary protocol** is the community contract. It must be designed for decade-long evolution before the first community adapter ships.
2. **The platform assumes interactive, session-per-pod workloads.** Adding lightweight task execution and headless modes would unlock the entire generic-agentic-workflow market without compromising the core model.
3. **Extension without recompilation** is the pattern that separates projects that last (Kubernetes, Terraform, Envoy) from projects that fork (everything else).

The 5 must-have recommendations above are feasible before v1 and would dramatically improve the platform's ability to grow without breaking backward compatibility. The design is already 90% of the way there -- these are the last 10% that will determine whether Lenny becomes infrastructure or a curiosity.
