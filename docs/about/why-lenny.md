---
layout: default
title: "Why Lenny?"
parent: About
nav_order: 1
---

# Why Lenny?

{: .no_toc }

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

{: .note }

> **Status: design phase.** These docs describe the platform as specified. See [Implementation Status](status) for what's wired up today.
>
> **Lenny is unusually ambitious for its stage.** The surface area is broader than a traditional early-stage OSS project would attempt. That's deliberate: with AI assistance, it's feasible to write a detailed spec and documentation up front and drive the implementation from them — an approach that wasn't realistic before. The project is still early stage; expect changes as feedback arrives.

---

## What Lenny is

Lenny is a platform for running interactive AI agent sessions in isolated sandboxes on your own Kubernetes cluster.

The platform is runtime-agnostic. Any agent harness that implements a basic JSON-over-stdin/stdout protocol can run under it, and a catalog of ready-to-use runtimes ships out of the box — Claude Code, Gemini CLI, Codex, LangGraph, CrewAI, and others. You can fork any of them or register your own alongside.

Agents run in one of three modes, chosen to fit the isolation–throughput trade-off. The default, `session` mode, gives each session its own locked-down pod with a fresh workspace, leased credentials, and a tight network perimeter. When throughput matters more than per-session isolation, `task` mode reuses a pod across sequential tasks with a workspace scrub between them, and `concurrent` mode handles several tasks at once inside a single pod.

In Lenny, agents can delegate work to other agents, recursively. Lenny tracks the delegation tree and enforces budget, scope, and isolation at every hop, so multi-agent systems don't depend on any runtime to police itself.

Pods run non-root with dropped Linux capabilities, a read-only root filesystem, and default-deny network policies; no standing credentials are mounted — agents receive short-lived leases scoped to the session — and every state change is written to a hash-chained audit log. Beyond those defaults, operators can route LLM calls through the gateway so provider API keys never reach the pod, configure the pool's sandbox runtime (runc, gVisor, or Kata Containers), and plug guardrail and content-policy interceptors into the gateway's request path.

Clients can interact with Lenny agents over different protocols: REST, MCP, the OpenAI Chat Completions API, and Open Responses API. Any of them can start
sessions, stream messages, upload files, trigger delegation, and tear sessions down.

Underneath, Lenny is built on standard Kubernetes building blocks: custom resources, controllers, network policies, autoscalers, and sandboxing  
runtimes. There's no custom scheduler, no external control plane, and no outbound telemetry to a vendor — data stays inside the cluster you operate.

---

## When Lenny is a good fit

Lenny fits teams that need one or more of:

- **Sessions that start fast even though they're isolated.** Idle pods are kept warm in a pool, so starting a new session doesn't wait for a container to boot. What a user waits on is their workspace being prepared and their agent starting -- usually a few seconds.
- **Strict separation between sessions.** Each session runs in its own pod. You choose how strong that isolation is per pool: a normal container for trusted code, a gVisor sandbox for untrusted code (the default), or a microVM with Kata Containers for high-risk workloads. No shared memory, no shared filesystem.
- **Interactive workflows.** Streaming model output to the user as it's generated. Asking the user for input mid-run and waiting for an answer. Interrupting a long-running agent cleanly. Picking up from a checkpoint if the pod dies.
- **Multi-agent workflows with guardrails.** An agent can ask another agent to do part of the work, with an enforced token budget, a narrower set of permissions, and isolation that can only get stricter -- never looser -- down the tree. The gateway prevents cycles and caps tree depth and size.
- **One integration point for multiple client styles.** Automation that wants REST, MCP hosts like Claude Desktop, code that already uses the OpenAI SDK, and clients built against the Open Responses spec all work against the same gateway.
- **Audit, multi-tenancy, and compliance controls.** Postgres row-level security on every query. A tamper-evident audit log. Retention windows compatible with SOC 2, HIPAA, and FedRAMP. GDPR-style erasure that returns a cryptographic receipt. Legal holds. Data residency rules that pin sessions to specific regions.
- **A platform that an AI can operate.** Everything a human on-call might check -- cluster state, recent errors, drift against intended configuration, backup history -- is exposed as a structured API. So is every remediation step. An AI DevOps agent can keep the platform healthy without screen-scraping `kubectl`.

## When Lenny is not a good fit

- **You want a hosted SaaS you sign up for.** Lenny is self-hosted only; there is no managed offering.
- **You want a specific agent framework baked in.** Lenny works with whatever agent you bring. The reference catalog covers common frameworks, but it's a starting point, not a constraint.
- **You want evaluation scoring, LLM-as-judge, memory extraction, or prompt guardrails built in.** Lenny provides the hooks to plug those tools in, but it doesn't implement them. You bring your existing evaluation or safety stack.
- **You can't run Kubernetes.** The platform is built on Kubernetes features (custom resources, network policies, autoscalers, admission webhooks). The single-binary stack runs on a laptop for evaluation and demos, but it is not a supported production target.

---

## How Lenny is designed

The sections below describe the design choices that shape the platform. They are implemented today, not roadmap items.

### The gateway is the only external surface

All client traffic enters through the gateway. Pods are never reachable from outside the cluster. This gives you one place to enforce authentication, authorization, rate limits, quotas, delegation budgets, and audit -- instead of spreading those concerns across every runtime. Clients see a consistent session model whether they're using REST, MCP, the OpenAI Chat Completions API, or the Open Responses API. Behind the scenes, each protocol is translated into the same session state machine.

### You can integrate an agent as deeply or as shallowly as you want

There are three levels of integration. You pick the level that matches what your agent needs; you can move up later.

| Level        | What it is                                                                     | Effort                                    | What it adds                                                                                                                                                              |
| :----------- | :----------------------------------------------------------------------------- | :---------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Basic**    | Your program reads JSON lines from stdin, writes JSON lines to stdout          | ~50 lines, no Lenny dependency            | Session lifecycle, text in and out, reading and writing files in the workspace                                                                                            |
| **Standard** | Basic, plus the agent connects to Lenny's local tool server over a Unix socket | ~150-200 lines plus an MCP client library | Delegation to other agents, asking the user for input mid-run, persistent memory, access to connectors like GitHub or Jira                                                |
| **Full**     | Standard, plus a second socket for lifecycle signals                           | ~300-400 lines                            | Graceful checkpoints, clean interrupt handling, rotating credentials without restarting the agent, advance notice before a deadline, reusing the pod for sequential tasks |

Official SDKs for Go, Python, and TypeScript handle the wire format so you don't have to. If you prefer, you can implement the protocol directly -- the SDKs are thin conveniences, not lock-in.

### Runtime types and execution modes

There are two kinds of runtime:

- **Agent runtimes** take part in the full session lifecycle -- they receive messages, use workspaces, can delegate, and can ask for user input.
- **MCP runtimes** wrap an existing MCP server and run it inside Lenny's sandboxing, credential leasing, and pool management -- without the MCP server needing to know Lenny exists.

Agent runtimes can be scheduled three ways:

| Mode         | Pod usage                                                          | Isolation guarantee                    | Typical use                                                      |
| :----------- | :----------------------------------------------------------------- | :------------------------------------- | :--------------------------------------------------------------- |
| `session`    | One session owns the pod end-to-end                                | Strongest -- no reuse between sessions | Coding agents, long-running interactive work (default)           |
| `task`       | Pod runs one task, workspace is scrubbed, next task reuses the pod | Best-effort scrub between tasks        | High-throughput batch when the runtime supports Full integration |
| `concurrent` | One pod handles multiple tasks at once                             | Process-level only                     | Lightweight, stateless handlers                                  |

`task` and `concurrent` modes relax isolation in exchange for throughput, so the platform requires explicit operator acknowledgment to enable them and refuses unsafe combinations (for example, `task` mode with a Basic-integration runtime).

### Pods are untrusted by default

Every session pod runs non-root, with all Linux capabilities dropped, a read-only root filesystem, and a default-deny network policy that allows only the gateway. No standing credentials are mounted. When a session needs an LLM API key or a connector token, the gateway mints a short-lived lease bound to that specific pod's identity.

You choose how hard the pod boundary is, per pool:

- **`runc`** -- a normal Linux container. Appropriate for first-party code you trust.
- **`gVisor`** -- a user-space kernel that intercepts system calls. This is the default, and is appropriate for arbitrary untrusted agents.
- **`Kata Containers`** -- a full microVM. Use it for high-risk workloads or strict multi-tenant isolation.

For pools using `deliveryMode: proxy` (the default), LLM API keys live only in the gateway's memory: when an agent calls a model, the gateway rewrites the request and forwards it to the provider; the agent sees only an opaque lease token. The gateway has built-in support for Anthropic, AWS Bedrock, Google Vertex AI, and Azure OpenAI. For other providers, route through an external LLM proxy like LiteLLM or Portkey alongside the built-in one. Pools using `deliveryMode: direct` deliver a short-lived, lease-scoped credential to the pod so runtimes that must call the provider themselves can do so without the gateway on the request path.

### Delegation is enforced at the gateway

An agent asks to delegate work with a single tool call. The gateway tracks the whole tree and enforces, at every hop:

- A maximum depth and fan-out (how many parallel children are allowed)
- A token budget allocated from the parent and debited atomically
- A cap on the total tree's size and memory
- That a child's permissions are a subset of its parent's -- never a superset
- That a child's isolation is at least as strict as its parent's -- never looser
- That content policies can only be tightened down the tree, never loosened
- That the tree can't form a cycle (an agent delegating to its ancestor)

The parent doesn't see the child's pod address, internal endpoints, or raw credentials. It sees a virtual interface for status, cancellation, forwarding user-input requests, and message delivery.

### Cross-cutting capabilities are interfaces you fill in

Memory, caching, guardrails, evaluation scoring, and credential routing are defined as interfaces. Lenny ships a default implementation of each that is disabled unless you opt in, and any of them can be replaced with your own tool. Lenny does not implement evaluation scoring or safety classification; it provides the interfaces you wire those into.

### Experimentation

Lenny's focus is **infrastructure primitives** for rolling runtime versions: pools of pod variants, deterministic request routing to a variant, and propagation of the chosen variant into the adapter manifest so the runtime knows which configuration it's running under. These are the parts you can't get anywhere else and that every experimentation flow needs.

A **basic built-in variant assigner** ships with the platform. It supports deterministic bucketing on a session-level key (for example, tenant or user ID) with configurable split ratios. It is intentionally limited — enough for simple runtime-version rollouts, not enough to replace a real experimentation platform.

Most teams will plug in an **external experimentation platform** (LaunchDarkly, Statsig, Unleash, or any OpenFeature-compatible provider) for assignment decisions. Lenny integrates through OpenFeature, so the assignment platform is swappable and experiment lifecycle management — targeting rules, rollout curves, auto-winner declaration, stats — lives where your team already runs it.

What Lenny deliberately does **not** provide: experiment lifecycle management, statistical significance testing, multi-armed bandits, or auto-winner declaration. All transitions between variants are operator-initiated or driven by the external platform you plug in.

### Evaluation

Lenny is **not an eval platform** and does not ship one. Runtime builders choose whichever eval framework fits their workflow — LangSmith, Braintrust, Arize, Langfuse, or a home-grown pipeline — and Lenny stays out of the way. The gateway propagates `tracingContext` across delegation chains so those external platforms can stitch traces end-to-end.

For teams that want to persist scores alongside session state without standing up another system, Lenny exposes a **basic score storage and retrieval mechanism** (`/eval` endpoint). It is a database table with an API in front of it — not an eval runner, not a judge, not a scoring model. Use it if it fits; replace it or ignore it otherwise.

### Every installation can be operated by a machine

Lenny ships with a dedicated management plane (`lenny-ops`) that exposes structured endpoints for diagnostics, runbooks, backups, drift detection, and cluster management -- regardless of how big your deployment is. `lenny-ctl doctor --fix` applies idempotent remediations for common misconfigurations.

Every state an operator might need is exposed as a structured API, so an AI DevOps agent can inspect and remediate the platform without screen-scraping `kubectl`.

---

## Trade-offs

- **Pre-warmed pods consume resources when idle.** Scale-to-zero schedules mitigate this for off-peak hours; during active periods the resource cost is real.
- **No shared storage mounts.** Workspace materialization adds latency to every session start. The trade is isolation and auditability.
- **Least privilege by default.** Credential management is more involved -- credential pools, lease rotation, per-session scoping -- than mounting API keys directly.
- **Deeper integration takes more code.** The Basic level is ~50 lines; Full is ~300-400. Teams that want in-place credential rotation, clean interrupts, and cooperative checkpointing have to do that work.
- **No eval scoring or memory extraction built in.** Deployers bring their own evaluation pipelines, scoring tools, and memory extraction logic. Lenny provides the hooks.
- **No automatic experiment lifecycle management.** All experiment transitions are operator-initiated. Lenny does not build auto-winner declaration, statistical significance testing, or multi-armed bandits.

---

## Related reading

- [Comparisons](comparisons) -- side-by-side with E2B, Daytona, Fly.io Sprites, Temporal, Modal, and LangGraph/LangSmith.
- [Architecture Overview](../getting-started/architecture) -- components, data flow, security boundaries.
- [Core Concepts](../getting-started/concepts) -- sessions, runtimes, pools, gateway, delegation, workspaces, MCP, tenants, credentials.
- [Agent Operability](../operator-guide/agent-operability) -- the `lenny-ops` control plane and the `doctor --fix` remediation loop.
