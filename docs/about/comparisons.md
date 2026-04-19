---
layout: default
title: Comparisons
parent: About
nav_order: 2
---

# Comparisons

{: .no_toc }

Side-by-side analysis of Lenny against other platforms in the agent infrastructure space. Each comparison focuses on architectural differences rather than feature counts.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

{: .note }
> **Status: design phase.** This page compares Lenny's v1 target surface against shipping competitors. See [Implementation Status](status) for what's wired up in Lenny today.

---

## Decide in 30 seconds

Pick Lenny when **all** of the following apply; otherwise jump to the detailed comparisons below.

- [ ] You can run Kubernetes (and want the data and keys to stay inside the cluster you operate).
- [ ] You need **more than a sandbox** — session lifecycle, streaming, delegation with per-hop budgets, credential leasing, audit.
- [ ] You want a **runtime-agnostic** contract so different teams can bring their own agents/frameworks behind one gateway.
- [ ] You need enterprise controls out of the box: multi-tenancy, RBAC, audit log, GDPR-style erasure, data residency.
- [ ] You do **not** need GPU workloads in v1.

If two or more boxes are unchecked, read the specific alternative below — you likely want something more specialized (E2B/Daytona for pure sandbox, Temporal for durable workflows, Modal for GPU, LangGraph for framework-coupled orchestration).

**Triage by what's unchecked.** If you only skim one section:

| Unchecked box | Read first |
|:--|:--|
| Can't run Kubernetes | [Lenny vs Fly.io Sprites](#lenny-vs-flyio-sprites) (hosted) or [Lenny vs Modal](#lenny-vs-modal) |
| Just need a sandbox | [Lenny vs E2B](#lenny-vs-e2b), [Lenny vs Daytona](#lenny-vs-daytona), or [Lenny vs Alibaba OpenSandbox](#lenny-vs-alibaba-opensandbox) |
| Framework-coupled is fine | [Lenny vs LangGraph / LangSmith](#lenny-vs-langgraph--langsmith) |
| Need durable workflows | [Lenny vs Temporal](#lenny-vs-temporal) |
| Need GPU in v1 | [Lenny vs Modal](#lenny-vs-modal) |
| Don't care about delegation | [Lenny vs E2B](#lenny-vs-e2b) or [Lenny vs Daytona](#lenny-vs-daytona) |
| Computer-use / GUI agents | [Lenny vs Alibaba OpenSandbox](#lenny-vs-alibaba-opensandbox) — Lenny has no GUI path in v1 |
| Multi-agent research prototyping, not production | [Lenny vs Google Scion](#lenny-vs-google-scion) |
| Single-developer policy sandbox | [Lenny vs NVIDIA OpenShell](#lenny-vs-nvidia-openshell) |

---

## Comparison table

| Dimension                  | Lenny                                      | E2B                     | Daytona                   | Fly.io Sprites              | Temporal                 | Modal                       | LangGraph/LangSmith             |
| :------------------------- | :----------------------------------------- | :---------------------- | :------------------------ | :-------------------------- | :----------------------- | :-------------------------- | :------------------------------ |
| **Self-hosted**            | Yes (K8s)                                  | Yes (Firecracker infra) | Yes                       | No (hosted)                 | Yes (complex)            | No (hosted)                 | Yes (K8s)                       |
| **Runtime-agnostic**       | Yes (adapter contract)                     | Sandbox only            | Sandbox only              | Container-based             | SDK-coupled              | Container-based             | LangChain-coupled               |
| **Runtime types**          | `agent` (task lifecycle) + `mcp` (MCP server hosting) | Single type   | Single type               | Single type                 | Worker type              | Single type                 | Graph-based                     |
| **Execution modes**        | session / task / concurrent (workspace + stateless) | N/A            | N/A                       | N/A                         | N/A (workflow-based)     | N/A                         | N/A (graph-based)               |
| **Recursive delegation**   | Yes (gateway-enforced)                     | No                      | No                        | No                          | Via workflows            | No                          | RemoteGraph (no per-hop budget) |
| **Multi-protocol gateway** | REST + MCP + OpenAI + Open Responses + A2A (post-v1 adapter) | API-only                | API-only                  | API-only                    | gRPC/HTTP                | API-only                    | LangServe/API                   |
| **Enterprise controls**    | Built-in (RBAC, budgets, audit, isolation) | Basic                   | Basic                     | Basic                       | Via add-ons              | Basic                       | LangSmith platform              |
| **Experimentation**        | Variant pool + routing primitives; basic built-in assigner; integrates with LaunchDarkly/Statsig/Unleash via OpenFeature | No                      | No                        | No                          | No                       | No                          | LangSmith datasets/evals        |
| **Eval**                   | Not an eval platform; basic score storage only. Compatible with any eval framework (LangSmith, Braintrust, Arize, Langfuse, home-grown). | No           | No                        | No                          | No                       | No                          | Built-in eval framework          |
| **Session replay**         | Built-in (prompt_history + workspace_derive) | No                    | No                        | No                          | Deterministic replay     | No                          | Dataset replay                   |
| **Memory store**           | Pluggable interface (Postgres+pgvector default) | No                 | No                        | No                          | No                       | No                          | Built-in (LangChain-coupled)     |
| **Credential management**  | Pools, leasing, gateway-mediated LLM proxy, pod-bound lease tokens | No                      | No                        | No                          | No                       | No                          | Basic                            |
| **Compliance (GDPR/legal)**| Erasure, legal holds, data residency       | No                      | No                        | No                          | No                       | No                          | Basic                            |
| **Guardrails/interceptors**| Pluggable gRPC interceptor chain (12 phases) | No                   | No                        | No                          | No                       | No                          | LangSmith guardrails             |
| **Cold-start**             | P95 <2s runc, <5s gVisor (session-ready)   | ~150ms (container boot) | Sub-90ms (container boot) | ~300ms (checkpoint/restore) | N/A (persistent workers) | Sub-second (container boot) | N/A (persistent)                |
| **GPU support**            | Not in v1                                  | Limited                 | Limited                   | No                          | No                       | Yes (primary use case)      | No                              |
| **Isolation profiles**     | runc / gVisor / Kata                       | Firecracker microVM     | Container                 | Firecracker microVM         | Process-level            | Container                   | Process-level                   |
| **Semantic cache**         | Pluggable interface on credential pools (Section 4.9) | No                      | No                        | No                          | No                       | No                          | No                              |

---

## 2026 open-source entrants

Three Apache 2.0 projects launched in Q1 2026 cover adjacent surface area. All three ship pre-wired for AI coding workflows: Scion and OpenShell only launch coding-agent CLIs (Claude Code, Codex, Gemini CLI, OpenCode, Copilot); OpenSandbox lists coding agents as its first use case alongside GUI/computer-use, eval, and RL. Lenny is workload-agnostic — any JSON-over-stdio program is a runtime, and its reference catalog spans coding CLIs, chat, and framework adapters (LangGraph, Mastra, CrewAI, OpenAI Assistants). None of the three is a drop-in replacement for Lenny's full scope, but each is the right choice for a specific need. Full narrative comparisons are below; this table is a quick orientation.

| Dimension | Lenny | Google Scion | NVIDIA OpenShell | Alibaba OpenSandbox |
|:---|:---|:---|:---|:---|
| **Released** | Design phase (MIT) | April 2026 (Apache 2.0) | GTC 2026 (Apache 2.0) | March 2026 (Apache 2.0) |
| **Framing** | K8s-native agent session platform | Multi-agent orchestration testbed | Policy-driven sandbox runtime | General sandbox with unified API |
| **Workload focus** | Workload-agnostic (coding, chat, MCP hosting, framework adapters) | Coding-CLI harnesses + experimental multi-agent research | AI coding (Claude / OpenCode / Codex / Copilot) | Coding, GUI/computer-use, eval, RL (GUI features are the standout) |
| **Implementation** | Go, gateway + CRDs + controllers | Go CLI over containers | Rust CLI + K3s-in-Docker | Python FastAPI + multi-language SDKs |
| **Laptop-only start** | Yes (`lenny up` single binary) | Yes (`go install`) | Yes (K3s-in-Docker) | Yes (Docker) |
| **Isolation** | runc / gVisor / Kata via `RuntimeClass` | Containers + per-agent git worktree | Docker container + YAML policy engine | Docker / K8s / gVisor / Kata / Firecracker |
| **Multi-agent** | Recursive delegation, gateway-enforced across sessions | Emergent coordination via a shared CLI | No cross-sandbox orchestration (alpha: "single-player mode"); in-session subagent spawning mentioned | Not a platform feature |
| **Token budgets** | Per-subtree, atomic | Not available | Not available | Not available |
| **Credentials** | Leased, pod-bound, rotatable | Per-agent separated credentials | "Providers" — env-var injection | `OPEN_SANDBOX_API_KEY` for management-API auth |
| **Client protocols** | REST + MCP + OpenAI + Open Responses | CLI | CLI | REST + MCP |
| **Multi-tenancy** | Postgres RLS, RBAC, audit | Not provided | Explicit future work | Design goal for K8s runtime; isolation model undocumented |
| **Compliance controls** | GDPR erasure, legal holds, audit chain | Not provided | Not provided | Not provided |
| **Standout feature** | Platform-enforced recursive delegation | Emergent multi-agent coordination | Hot-reloadable YAML policy incl. inference routing | VNC desktops + Chromium + code-server in-sandbox |

---

## Latency comparison note

The cold-start figures in this table are **not directly comparable**. Each platform measures a different operation:

- **E2B (~150ms), Daytona (sub-90ms), Fly.io Sprites (~300ms):** These measure **container/VM boot time** -- the wall-clock duration to go from a stopped state to a running process. No workspace setup, no file delivery, no credential assignment.
- **Lenny (P95 <2s runc, <5s gVisor):** This measures **full session-ready time** -- pod claim + workspace file delivery + setup command execution + agent session start. The pod-claim-and-routing step alone (the operation most analogous to competitor cold-start numbers) is in the millisecond range because pods are pre-warmed.

A fair comparison requires aligning on the same end-point definition. Lenny's numbers are also explicitly **unvalidated targets** (first-principles estimates) that must be measured by the first-working-slice benchmark harness before any comparison claim is made.

---

## Lenny vs E2B

**E2B** is the market-leading AI sandbox platform. It provides Firecracker microVMs with ~150ms boot times, SDK support for Python and TypeScript, and both hosted and self-hosted deployment options.

### Where Lenny differs

| Aspect                  | Lenny                                                                                                                                           | E2B                                                                                                                            |
| :---------------------- | :---------------------------------------------------------------------------------------------------------------------------------------------- | :----------------------------------------------------------------------------------------------------------------------------- |
| **Architecture**        | Kubernetes-native platform with gateway, controllers, and CRDs.                                                                                 | Firecracker microVM orchestrator with API server.                                                                              |
| **Runtime contract**    | Formal adapter contract with three integration levels (Basic, Standard, Full) that separates platform integration from agent logic. Any process can be a Lenny runtime. | Sandbox environment where the operator's code runs. No orchestration contract -- the sandbox is a blank execution environment. |
| **Runtime types**       | Two types: `agent` (full task lifecycle) and `mcp` (managed MCP server hosting with zero code changes).                                         | Single sandbox type.                                                                                                           |
| **Execution modes**     | Three modes: `session` (1:1 pod), `task` (sequential reuse with scrub), `concurrent` (slot multiplexing). Mode-aware pool scaling.              | Single sandbox per request. No pod reuse or multiplexing.                                                                      |
| **Delegation**          | Gateway-enforced delegation with per-hop budget, scope, and policy.                                                                             | Not available. Multi-agent coordination must be built at the application layer.                                                |
| **Self-hosting**        | Standard Kubernetes deployment. Runs wherever K8s runs.                                                                                         | Self-hosting requires managing Firecracker/microVM infrastructure separately from Kubernetes clusters.                         |
| **Isolation**           | Deployer-selectable: runc (fast), gVisor (medium), Kata (strong).                                                                               | Firecracker microVM only (strong isolation).                                                                                   |
| **Protocol support**    | REST, MCP, OpenAI, Open Responses.                                                                                                              | REST API.                                                                                                                      |
| **Enterprise controls** | Built-in multi-tenancy, RBAC, token budgets, audit logging, content policy interceptors.                                                        | Basic API key authentication. Enterprise features via commercial offering.                                                     |

### Where E2B has advantages

- **Faster cold-start.** Firecracker microVMs boot in ~150ms. Lenny's pre-warmed pods avoid cold-start entirely for session creation, but workspace materialization adds latency.
- **Broader language sandbox support.** E2B provides pre-built sandboxes for Python, JavaScript, and other languages with dependency management.
- **SaaS option.** E2B offers a fully managed hosted service. Lenny requires Kubernetes infrastructure.
- **Simpler operational model.** E2B is a single service; Lenny has multiple controllers, stores, and CRDs.
- **Mature ecosystem.** E2B has an established SDK ecosystem and community.

---

## Lenny vs Daytona

**Daytona** provides sub-90ms cold starts and desktop environments for computer-use agents. It focuses on development environment provisioning with fast startup.

### Where Lenny differs

| Aspect               | Lenny                                                       | Daytona                                                     |
| :------------------- | :---------------------------------------------------------- | :---------------------------------------------------------- |
| **Runtime contract** | Formal adapter contract with layered integration levels.    | Environment provisioning without an orchestration contract. |
| **Delegation**       | Gateway-enforced, with budget and scope controls.           | Not available.                                              |
| **Protocol support** | Multi-protocol gateway (REST, MCP, OpenAI, Open Responses). | REST API.                                                   |
| **Focus**            | Agent session lifecycle management, delegation, and policy. | Fast development environment provisioning.                  |

### Where Daytona has advantages

- **Faster boot time.** Sub-90ms cold starts vs. Lenny's pre-warm + workspace setup latency.
- **Computer-use agent support.** Desktop environments for agents that need GUI interaction.
- **Simpler for single-agent use cases.** If you only need a fast sandbox without delegation or policy enforcement, Daytona has less operational overhead.

---

## Lenny vs Fly.io Sprites

**Fly.io Sprites** is a January 2026 direct competitor using Firecracker with checkpoint/restore in ~300ms. It provides hosted infrastructure for running agent workloads.

### Where Lenny differs

| Aspect                 | Lenny                                                                                           | Fly.io Sprites                                         |
| :--------------------- | :---------------------------------------------------------------------------------------------- | :----------------------------------------------------- |
| **Deployment model**   | Self-hosted on your Kubernetes cluster. All data in your infrastructure.                        | Hosted on Fly.io infrastructure.                       |
| **Delegation**         | Gateway-mediated, with policy enforced at every hop.                                            | Not available.                                         |
| **Policy engine**      | Built-in rate limiting, token budgets, concurrency controls, isolation profiles, audit logging. | Basic platform-level controls.                         |
| **Checkpoint/restore** | Workspace-level checkpointing via MinIO. Session state in Postgres.                             | Process-level checkpoint/restore via Firecracker CRIU. |

### Where Fly.io Sprites has advantages

- **Hosted infrastructure.** No Kubernetes cluster to manage. Simpler operational model.
- **Process-level checkpoint/restore.** Can snapshot and restore entire process state, not just workspace files.
- **Edge deployment.** Fly.io's global edge network provides low-latency execution worldwide.

---

## Lenny vs Temporal

**Temporal** is a durable workflow engine with replay-based fault tolerance. It excels at long-running orchestration with strong durability guarantees.

### Where Lenny differs

| Aspect                   | Lenny                                                                                                                                       | Temporal                                                                                                             |
| :----------------------- | :------------------------------------------------------------------------------------------------------------------------------------------ | :------------------------------------------------------------------------------------------------------------------- |
| **Runtime coupling**     | Runtime-agnostic adapter contract. Agent code does not import Lenny libraries.                                                              | Agent logic must use Temporal SDK (Go, Java, TypeScript, Python). Workflow definitions are Temporal-native.          |
| **Delegation**           | Gateway-mediated delegation without workflow coupling. Parent and child sessions are independent runtimes connected by budget/scope policy. | Child workflows are tightly coupled to parent via Temporal SDK primitives.                                           |
| **Durability model**     | Workspace checkpointing + session state in Postgres + event replay.                                                                         | Deterministic replay from event history. Strongest durability guarantees in the space.                               |
| **Interactive sessions** | Streaming, elicitation, interrupts, and tool approvals are part of the session contract.                                                    | Designed for batch/async workflows. Interactive patterns require additional infrastructure.                          |
| **Self-hosting**         | Standard Kubernetes deployment.                                                                                                             | Self-hosted Temporal adds significant operational burden (Cassandra/MySQL, Elasticsearch, multi-service deployment). |

### Where Temporal has advantages

- **Battle-tested durability.** Temporal's replay-based fault tolerance is proven at massive scale (Uber, Netflix, Snap).
- **Rich workflow primitives.** Timers, signals, queries, child workflows, saga patterns, and versioning are mature and well-documented.
- **Deterministic replay.** Temporal can replay the exact execution history, providing stronger recovery guarantees than Lenny's checkpoint-based approach.
- **Language breadth.** Official SDKs for Go, Java, TypeScript, Python, .NET.
- **Hosted option.** Temporal Cloud provides a fully managed service.

---

## Lenny vs Modal

**Modal** is a serverless GPU/CPU container platform with sub-second cold starts. It excels at batch inference and function-level compute.

### Where Lenny differs

| Aspect                        | Lenny                                                                       | Modal                                           |
| :---------------------------- | :-------------------------------------------------------------------------- | :---------------------------------------------- |
| **Focus**                     | Agent session lifecycle with interactive streaming, delegation, and policy. | Serverless function execution with GPU support. |
| **Agent-specific primitives** | Delegation, token budgets, elicitation, credential pools, task trees.       | None. Modal is a generic compute platform.      |
| **Deployment model**          | Self-hosted on your Kubernetes cluster.                                     | Hosted on Modal infrastructure only.            |
| **Interactive sessions**      | Bidirectional streaming with interrupts, tool approvals, and elicitation.   | Primarily request/response and batch patterns.  |

### Where Modal has advantages

- **GPU support.** Modal provides GPU instances with fast cold starts -- essential for inference workloads. Lenny v1 has no GPU support.
- **Simpler for batch inference.** If your use case is running inference functions without interactive sessions, Modal has less overhead.
- **Sub-second cold starts.** Modal's container snapshots provide fast startup without pre-warming.
- **Python-native.** Modal's SDK is Python-first with decorators for function definition.

---

## Lenny vs LangGraph / LangSmith

**LangGraph** provides graph-based agent orchestration with built-in persistence and human-in-the-loop support. **LangSmith** provides observability, evaluation, and deployment. Together they form the LangChain platform.

### Where Lenny differs

| Aspect                  | Lenny                                                                                         | LangGraph / LangSmith                                                                                                |
| :---------------------- | :-------------------------------------------------------------------------------------------- | :------------------------------------------------------------------------------------------------------------------- |
| **Runtime coupling**    | Runtime-agnostic. LangGraph agents are one possible runtime behind the adapter contract.      | Tightly coupled to LangChain ecosystem. Agent logic uses LangGraph primitives.                                       |
| **Delegation controls** | Per-hop token budget, scope narrowing, isolation monotonicity, content policy inheritance.    | RemoteGraph provides graph-level delegation without per-hop budget or scope controls enforced at the platform layer. |
| **Protocol support**    | REST, MCP, OpenAI, Open Responses.                                                            | LangServe, LangGraph Cloud API.                                                                                      |
| **Self-hosting**        | Standard Kubernetes deployment.                                                               | LangSmith offers self-hosted Kubernetes deployment (available since 2024). Requires LangChain ecosystem coupling.    |
| **Ecosystem stance**    | Platform layer only. All AI capabilities (memory, eval, guardrails) are pluggable interfaces. | Bundles evaluators, tracing, memory, and observability. Rich but coupled.                                            |
| **A2A + MCP support**   | MCP Tasks at the gateway; A2A via the protocol adapter registry (a plug-in point for additional client protocols). | LangSmith now has A2A + MCP + RemoteGraph support, closing the protocol gap.                                         |

### Where LangGraph/LangSmith has advantages

- **Rich agent primitives.** Graph-based state machines, built-in persistence, human-in-the-loop, and streaming are mature and well-integrated within the LangChain ecosystem.
- **Observability and evaluation.** LangSmith provides integrated tracing, evaluation, and dataset management. Lenny relies on external observability tools (OpenTelemetry, Prometheus, Jaeger).
- **Ecosystem breadth.** LangChain's ecosystem includes hundreds of integrations for LLMs, tools, vector stores, and retrievers.
- **Python-first.** LangChain's primary SDK is Python, the dominant language for AI/ML development. Lenny's platform is Go with TypeScript and Go client SDKs.
- **Lower barrier to entry.** For teams already using LangChain, LangGraph is a natural extension with minimal new concepts.

---

## Lenny vs Google Scion

**Scion** is Google's multi-agent orchestration testbed, open-sourced in April 2026 under Apache 2.0. Google describes it as "an experimental multi-agent orchestration testbed designed to manage 'deep agents' running in containers." Pre-built harnesses ship for the major coding CLIs — Claude Code, Gemini CLI, Codex, OpenCode — and each agent gets its own container and git worktree. Agents coordinate by calling a shared CLI (`scion message`), so the model itself decides who to talk to. Ships with a demo project, *Relics of the Athenaeum*, where agents collaborate to solve puzzles. The README flags the project as "early and experimental."

### Where Lenny differs

| Aspect | Lenny | Scion |
|:---|:---|:---|
| **Nature of multi-agent** | Recursive delegation is a platform primitive — a parent calls `lenny/delegate_task` and the gateway enforces budget, scope, and isolation at every hop. | Coordination is emergent — agents learn a shared CLI and the model decides how to message peers. No manager/worker roles, no per-hop enforcement. |
| **Formal runtime contract** | Adapter contract with Basic / Standard / Full integration levels. | No formal adapter contract; "harness-agnostic" means any supported CLI agent plugs in as-is. |
| **Enforcement surface** | Gateway mediates every client interaction and delegation. | Agents coordinate directly; no central enforcement layer. |
| **Isolation options** | runc / gVisor / Kata via Kubernetes `RuntimeClass`. | Containers (Docker, Podman, Apple Container) plus a per-agent git worktree. |
| **Credential management** | Leased, pod-bound, rotatable; gateway mediates LLM provider calls. | Per-agent "separated credentials"; no leasing or gateway mediation. |
| **Client surface** | REST + MCP + OpenAI + Open Responses. | CLI (`scion message`, tmux sessions). No REST, gRPC, or MCP interface documented. |
| **Production posture** | Designed for multi-tenant production. | Explicitly experimental — no production integration planned by Google. |

### Where Scion has advantages

- **Emergent-coordination research.** Lets you study how agents self-organize through a shared CLI — Lenny's structured delegation does not attempt this.
- **Smaller surface to learn.** A single `scion message` primitive plus tmux-based inspection. Lenny's delegation machinery (token budgets, scope narrowing, isolation invariants, gateway interceptors) is more to pick up if the goal is just watching agents talk.
- **Demo artifact.** *Relics of the Athenaeum* is a ready-made, inspectable multi-agent workload.

Lenny matches Scion on the things that look like advantages at first glance: the `lenny up` embedded single-binary stack provides a comparable laptop-scale start with no outside dependencies, and the reference runtime catalog covers the same coding CLIs (plus `cursor-cli`, `chat`, and four framework adapters).

---

## Lenny vs NVIDIA OpenShell

**OpenShell** is NVIDIA's open-source policy-driven sandbox runtime, released at GTC 2026 under Apache 2.0. It's a Rust CLI that provisions a Docker container running K3s and launches an agent CLI (Claude, OpenCode, Codex, Copilot) inside. Behaviour is governed by a declarative YAML policy covering filesystem, process, network, and inference layers — filesystem and process are locked at sandbox creation, network and inference are hot-reloadable. Credentials are managed as "providers" and injected as environment variables. The project is explicitly alpha: "one developer, one environment, one gateway."

### Where Lenny differs

| Aspect | Lenny | OpenShell |
|:---|:---|:---|
| **Scope** | Multi-tenant platform — many sessions, many tenants, recursive delegation. | Single-agent "single-player mode" (alpha). Multi-tenant is explicitly future work. |
| **Policy model** | 12-phase request interceptor chain on the gateway; content classifiers, budgets, audit events plug in. | 4-layer YAML policy (filesystem/process static, network/inference hot-reloadable). |
| **Multi-agent** | Recursive delegation is a platform primitive, enforced across sessions at the gateway. | No cross-sandbox orchestration (alpha: "single-player mode"); NVIDIA's blog mentions agents spawning "scoped subagents" inside a single sandbox. |
| **Deployment** | Kubernetes deployment with CRDs and controllers. | A single Docker container running K3s on the developer's machine, or `--remote user@host` for a remote daemon. |
| **Credentials** | Leased with per-pod identity; gateway mediates LLM API keys. | "Providers" — credential bundles injected as environment variables at sandbox creation. |
| **Audit / compliance** | OCSF hash-chained audit log, SIEM forwarding, SOC 2 / HIPAA / FedRAMP retention presets. | `openshell logs` for operational introspection. |
| **Protocol surface** | REST + MCP + OpenAI + Open Responses. | CLI-driven. |

### Where OpenShell has advantages

- **Policy-first ergonomics.** The declarative YAML with HTTP-method-level network and inference routing is concise and easy to audit. Lenny's equivalent is spread across gRPC interceptors, `ContentPolicy` CRs, and gateway config.
- **Hot-reloadable network and inference policy.** Dynamic sections can change without recreating the sandbox. Lenny's content policy is revised through a gateway reload.
- **Low-overhead single-developer setup.** If you need one agent hardened against exfiltration on a laptop, OpenShell is simpler to stand up than a Kubernetes install.
- **Explicit Privacy Router.** Controls *where inference requests travel* as a first-class policy concept. Lenny's credential-routing interface provides equivalent mechanics, but as a pluggable interface rather than declarative policy.

---

## Lenny vs Alibaba OpenSandbox

**OpenSandbox** is Alibaba's open-source sandbox platform, released March 2026 under Apache 2.0. Its primary surface is a multi-language SDK fleet (Python, Java/Kotlin, JavaScript/TypeScript, C#/.NET, Go) wrapping a Python FastAPI server that manages Docker- or Kubernetes-backed sandboxes. The README lists Coding Agents first among the target use cases, alongside GUI Agents, Agent Evaluation, AI Code Execution, and RL Training. Its standout feature set is first-class support for GUI and computer-use agents: Chromium with VNC and DevTools, code-server for in-sandbox IDEs, and full desktop environments — use cases Lenny does not target in v1.

### Where Lenny differs

| Aspect | Lenny | OpenSandbox |
|:---|:---|:---|
| **Primary interface** | Gateway with four client protocols (REST, MCP, OpenAI, Open Responses). | SDK-first; REST API defined via OpenAPI; MCP wrapper for IDE clients. |
| **Multi-agent** | Recursive delegation with per-hop enforcement. | Not a platform feature; the SDK manages individual sandboxes. |
| **Multi-tenancy** | Postgres RLS, per-tenant RBAC, audit log. | The K8s runtime is described as "designed for production-grade, multi-tenant environments," but no per-tenant isolation model or RBAC is documented. |
| **Credentials** | Leased, pool-managed, rotatable; gateway mediates LLM proxy. | `OPEN_SANDBOX_API_KEY` for management-API auth; no per-session leasing or LLM-proxy model documented. |
| **Token budgets & quotas** | Per-subtree, per-session, per-tenant; Redis-backed atomic accounting. | Not documented. |
| **Compliance** | GDPR erasure, legal holds, residency, audit chain. | Not documented. |
| **Runtime contract** | Formal adapter contract with three integration levels. | SDK-as-contract — programs call the `opensandbox` library directly. |

### Where OpenSandbox has advantages

- **GUI / computer-use agents.** First-class VNC desktops, Chromium with DevTools, and code-server (VS Code Web) inside the sandbox make it the better choice today for browser-using and computer-use agents. Lenny v1 does not target this.
- **SDK breadth.** Official SDKs for Python, Java/Kotlin, JavaScript/TypeScript, C#/.NET, and Go — broader than Lenny's Go + TypeScript client surface.
- **Simpler data model.** One sandbox = one object with lifecycle + exec + file APIs. Lower cognitive load if you don't need sessions, delegation, or multi-tenancy.
- **No framework coupling.** Shares Lenny's goal of being framework-independent; easier to drop into an existing Python codebase than a Kubernetes platform.

---

## When Lenny fits

Lenny's design matches these requirements:

- **Self-hosted on your own Kubernetes cluster** -- data sovereignty, compliance, or existing K8s infrastructure.
- **Multiple agent frameworks** -- your teams use different agent runtimes and you need a single platform.
- **Mixed workload patterns** -- you need both interactive agent sessions (`session` mode) and high-throughput batch processing (`task` or `concurrent` mode) on the same platform, with mode-aware scaling.
- **MCP server hosting** -- you want to deploy existing MCP servers behind managed infrastructure (isolation, credentials, scaling, audit) without modifying the server code (`type: mcp` runtimes).
- **Recursive delegation** -- orchestrator agents that spawn child agents with enforced budgets and scope.
- **Enterprise controls** -- multi-tenancy, RBAC, audit logging, token budgets, content policy enforcement.
- **Multi-protocol clients** -- REST, MCP, OpenAI, and Open Responses clients connecting to the same infrastructure.
- **Interactive sessions** -- streaming, elicitation, interrupts, and tool approvals are part of the session contract.
- **Runtime version rollouts** -- variant pool and deterministic routing primitives, a basic built-in variant assigner, and integration with external experimentation platforms (LaunchDarkly, Statsig, Unleash) via OpenFeature for assignment decisions.
- **Compatibility with any eval framework** -- bring LangSmith, Braintrust, Arize, Langfuse, or a home-grown pipeline. Lenny is not an eval platform; it only provides a basic mechanism to store and retrieve scores alongside session state, plus session replay for regression testing.
- **Credential management** -- centralized credential pools with leasing and rotation. The gateway talks to LLM providers on behalf of agent pods, so pods never hold real provider API keys. Each lease token is cryptographically bound to the pod that requested it, so a token lifted from one pod cannot be used by another.
- **Compliance requirements** -- GDPR data erasure, legal holds, data residency, audit logging with hash-chained integrity, and configurable retention presets.
- **Pluggable guardrails** -- content safety interceptors at 12 gateway phases, compatible with AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or custom gRPC classifiers.
- **Agent memory** -- pluggable memory store (default: Postgres + pgvector, replaceable with Mem0, Zep, or any vector database).

Lenny is not a fit when:

- You need GPU support (not in v1).
- You do not have Kubernetes infrastructure and prefer a hosted solution.
- You are building within the LangChain ecosystem and want tight framework integration.
- You need a minimal sandbox without orchestration features.
- Your primary use case is batch inference rather than interactive agent sessions.
