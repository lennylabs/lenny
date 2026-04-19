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
| Just need a sandbox | [Lenny vs E2B](#lenny-vs-e2b), then [Lenny vs Daytona](#lenny-vs-daytona) |
| Framework-coupled is fine | [Lenny vs LangGraph / LangSmith](#lenny-vs-langgraph--langsmith) |
| Need durable workflows | [Lenny vs Temporal](#lenny-vs-temporal) |
| Need GPU in v1 | [Lenny vs Modal](#lenny-vs-modal) |
| Don't care about delegation | [Lenny vs E2B](#lenny-vs-e2b) or [Lenny vs Daytona](#lenny-vs-daytona) |

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
