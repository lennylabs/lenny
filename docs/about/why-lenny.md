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

---

## What Lenny is

Lenny is a platform for running interactive AI agent sessions in isolated sandboxes on your own Kubernetes cluster.

The platform is runtime-agnostic. Any agent harness that implements a basic JSON-over-stdin/stdout protocol can run under it. A reference runtime catalog defines runtimes for Claude Code, Gemini CLI, Codex, LangGraph, CrewAI, and other agents, and each of them can be forked or registered alongside custom runtimes.

Agents run in one of two execution modes, chosen to fit the isolation–throughput trade-off. The default, `session` mode, binds each session to a claimed pod and is parameterized by a `sessionPolicy` block. In the default policy each session gets its own locked-down pod with a fresh workspace, leased credentials, and a tight network perimeter. When throughput matters more than per-session isolation, `recycle.enabled: true` reuses a pod across sequential sessions with a whole-pod scrub between them, and `maxConcurrentSessions > 1` serves several sessions at once inside a single pod. `service` mode routes each message to a ready replica with no conversation continuity and provides minimal platform guarantees. It exists for runtimes already deployed as Lenny pods; new stateless deployments with no workspace requirements register as external connectors instead.

In Lenny, agents can delegate work to other agents, recursively. Lenny tracks the delegation tree and enforces budget, scope, and isolation at every hop, so multi-agent systems don't depend on any runtime to police itself.

Pods run non-root with dropped Linux capabilities, a read-only root filesystem, and default-deny network policies. No standing credentials are mounted: agents receive short-lived leases scoped to the session. Every session, task, and delegation produces durable records in a hash-chained audit log. Beyond those defaults, operators can route LLM calls through the gateway so provider API keys never reach the pod, configure the pool's sandbox runtime (gVisor, Kata Containers, or runc for development and testing), and plug guardrail and content-policy interceptors into the gateway's request path.

Clients can interact with Lenny agents over different protocols: REST, MCP, the OpenAI Chat Completions API, and the Open Responses API. REST and MCP clients can start sessions, stream messages, upload files, trigger delegation, and tear sessions down. The OpenAI Chat Completions and Open Responses adapters run a single-shot session per HTTP call and carry a subset of the message fields that REST and MCP carry.

Underneath, Lenny is built on standard Kubernetes building blocks: custom resources, controllers, network policies, autoscalers, and sandboxing runtimes. Lenny has no dependency on a vendor-hosted control plane, and all data stays in the operator's infrastructure.

---

## When Lenny is a good fit

Lenny fits teams that need one or more of:

- **Sessions that start fast even though they're isolated.** Idle pods are kept warm in a pool, so starting a new session doesn't wait for a container to boot. The remaining startup work is preparing the workspace and starting the agent.
- **Strict separation between sessions.** Under the default session policy, each session runs in its own pod. The isolation profile is set per pool: a gVisor sandbox (the default, and the minimum recommended for untrusted input, which includes all LLM-generated code), a Kata Containers microVM for higher-risk and multi-tenant workloads, or a plain runc container, which is recommended for development and testing. Pod reuse and concurrent sessions relax this separation and require explicit operator acknowledgment.
- **Interactive workflows.** Streaming model output to the user as it's generated. Asking the user for input mid-run and waiting for an answer. Interrupting a long-running agent cleanly. Resuming from the latest checkpoint after a retryable pod failure, for runtimes that checkpoint.
- **Multi-agent workflows with guardrails.** An agent can ask another agent to do part of the work, with an enforced token budget, a narrower set of permissions, and an isolation profile that stays the same or becomes stricter down the tree. The gateway prevents cycles and caps tree depth and size.
- **One integration point for multiple client styles.** Automation that wants REST, MCP hosts like Claude Desktop, code that already uses the OpenAI SDK, and clients built against the Open Responses spec all work against the same gateway.
- **Audit, multi-tenancy, and compliance controls.** Postgres row-level security on every query. A tamper-evident audit log. Compliance profiles (`soc2`, `hipaa`, and `fedramp`) that set retention floors. GDPR-style erasure that returns a cryptographic receipt. Legal holds. Data residency rules that pin sessions to specific regions.
- **A platform that an AI can operate.** Everything a human on-call might check (cluster state, recent errors, drift against intended configuration, and backup history) is exposed as a structured API. So is every remediation step. An AI DevOps agent can keep the platform healthy without screen-scraping `kubectl`.

## When Lenny is not a good fit

- **You want a hosted SaaS you sign up for.** Lenny is self-hosted only; there is no managed offering.
- **You want a specific agent framework baked in.** Lenny works with whatever agent you bring. The reference catalog covers common frameworks, and any runtime that implements the adapter contract runs alongside it.
- **You want evaluation scoring, LLM-as-judge, memory extraction, or prompt guardrails built in.** Lenny provides the hooks to plug those tools in, but it doesn't implement them. You bring your existing evaluation or safety stack.
- **You can't run Kubernetes.** The platform is built on Kubernetes features (custom resources, network policies, autoscalers, admission webhooks). The single-binary stack runs on a laptop for evaluation and demos, but it is not a supported production target.

---

## Design principles

The following principles govern the platform design. [Implementation Status](status) lists which parts run today.

1. **Gateway mediation.** Every external request goes through the gateway. Pods are never addressable from outside the cluster. The gateway is the single place where authentication, rate limits, quotas, delegation budgets, and audit are enforced.
2. **Durable session state.** Pod disks are scratch space. The durable state of a session (transcript, artifacts, and checkpoints) lives in Postgres, Redis, and object storage. After a retryable pod failure within the session's retry limit, the platform resumes the session on a new pod from its latest checkpoint. A runtime that does not checkpoint, such as a Basic-level runtime, resumes with a rebuilt workspace and no carried conversation state.
3. **Credential leasing.** Pods hold no standing credentials. Credentials are leased per session and scoped to what that session needs. Full-level runtimes rotate credentials in place; Basic and Standard runtimes rotate by checkpointing, restarting the pod with a new lease, and resuming. Workspaces have no shared mounts, and files arrive through the gateway.
4. **Warm pools.** The warm pool controller keeps idle pods ready so a new session does not wait for container boot. The hot path materializes the workspace, runs any setup commands, and starts the agent.
5. **Delegated AI functions.** Lenny does not implement AI behavior. It does not score evaluations, extract memories, or classify content. It exposes interfaces where those tools are plugged in; for example, a guardrail interceptor calls an external classifier.
6. **Operational API.** Every operator-visible state is available as an API. Human on-call and AI on-call use the same endpoints, and neither has to parse `kubectl` output.

---

## How Lenny is designed

The sections below describe the design choices that shape the platform.

### Gateway mediation

All client traffic enters through the gateway. Pods are never reachable from outside the cluster. The gateway is the one place that enforces authentication, authorization, rate limits, quotas, delegation budgets, and audit, so those concerns are not spread across every runtime. Each client protocol (REST, MCP, the OpenAI Chat Completions API, or the Open Responses API) is translated into the same session state machine. The OpenAI Chat Completions and Open Responses adapters map each HTTP call to a single-shot session.

### Integration levels

Lenny defines levels of integration for agent runtimes. A runtime starts at the level that matches what the agent needs and can move up later.

| Level        | What it is                                                                     | Effort                                    | What it adds                                                                                                                                                              |
| :----------- | :----------------------------------------------------------------------------- | :---------------------------------------- | :------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Basic**    | The program reads JSON lines from stdin and writes JSON lines to stdout        | No Lenny dependency                       | Session lifecycle, text in and out, reading and writing files in the workspace                                                                                            |
| **Standard** | Basic, plus the agent connects to Lenny's local tool server over a Unix socket | Basic plus an MCP client library; no Lenny-specific code beyond the manifest nonce | Delegation to other agents, asking the user for input mid-run, persistent memory, access to connectors like GitHub or Jira                                                |
| **Full**     | Standard, plus a second socket for lifecycle signals                           | Standard plus the lifecycle-signal handlers | Graceful checkpoints, clean interrupt handling, rotating credentials without restarting the agent, advance notice before a deadline |

Runtime author SDKs for Go, Python, and TypeScript implement the wire format. A runtime can also implement the protocol directly without an SDK.

### Runtime types and execution modes

There are two kinds of runtime:

- **Agent runtimes** take part in the full session lifecycle. They receive messages, use workspaces, can delegate, and can ask for user input.
- **MCP runtimes** wrap an existing MCP server and run it inside Lenny's sandboxing, credential leasing, and pool management, without the MCP server needing to know Lenny exists.

Agent runtimes run in one of two execution modes; session mode is parameterized by `sessionPolicy`:

| Mode / policy | Pod usage | Isolation guarantee | Typical use |
| :--- | :--- | :--- | :--- |
| `session`, `recycle.enabled: false`, `maxConcurrentSessions: 1` | One session owns the pod end-to-end | No reuse between sessions | Coding agents, long-running interactive work (default) |
| `session`, `recycle.enabled: true` | Pod is scrubbed at occupancy zero and reused for the next session | Best-effort scrub between sessions | High-throughput sequential work |
| `session`, `maxConcurrentSessions > 1` | Several sessions share one pod at the same time | Process-level only | Lightweight concurrent handlers |
| `service` | Each message is routed to a ready replica | Process-level, no scrub | Runtimes already deployed as Lenny pods with minimal state |

Pod recycling, concurrent sessions, and service mode relax isolation in exchange for throughput, so the platform requires explicit operator acknowledgments to enable them (`acknowledgeBestEffortScrub` for recycling, and `acknowledgeProcessLevelIsolation` for concurrent sessions and for a recycling pool with `maxSessionsPerPod` above 1, a `scrubProfile` other than `vm-restart`, and `recycle.runtimeProcess: keep`, because a later session on such a pod can run in the runtime process that served an earlier one) and refuses unsafe combinations. Recycling requires no lifecycle-signal socket and works at every integration level, and reuse requires a runtime that serves sequential sessions.

Service mode carries the fewest platform guarantees. It has no workspace delivery, no per-request lifecycle tracking or retry policy, no checkpoint support, and no per-request failure isolation, so a pod failure affects every request routed to that pod. The runtime owns retry, idempotency, and error handling. Stateless runtimes that need no workspace or session lifecycle are registered as external connectors, which integrate with Lenny's routing and observability without a warm pool or a runtime adapter. Use service mode only for a runtime that already runs as a Lenny pod and cannot yet move to the connector model.

### Isolation model

Every session pod runs non-root, with all Linux capabilities dropped, a read-only root filesystem, and a default-deny network policy. The default `restricted` egress profile allows the gateway and the dedicated DNS service; pods also reach object storage for checkpoint transfer through short-lived presigned URLs. The `provider-direct` and `internet` egress profiles widen this perimeter. No standing credentials are mounted. When a session needs an LLM provider credential, the gateway assigns a short-lived lease scoped to that session. In multi-tenant deployments, proxy-mode lease tokens are bound to the pod's SPIFFE identity. Connector OAuth tokens stay in the gateway and never reach the pod.

The isolation profile is set per pool:

- **`gVisor`** (`sandboxed`): a user-space kernel that intercepts system calls. This is the default and the minimum recommended isolation for any workload that processes untrusted input, which includes all LLM-generated code execution.
- **`Kata Containers`** (`microvm`): a full microVM. Use it for higher-risk, semi-trusted, or multi-tenant workloads.
- **`runc`** (`standard`): a normal Linux container, recommended for development and testing. A pool enables it with the opt-in `allowStandardIsolation: true` and provides no protection against kernel exploits.

For pools using `deliveryMode: proxy`, LLM API keys live only in the gateway's memory: when an agent calls a model, the gateway rewrites the request and forwards it to the provider; the agent sees only an opaque lease token. Proxy is the default delivery mode for multi-tenant deployments, and direct is the default for single-tenant deployments. The gateway has built-in support for Anthropic, AWS Bedrock, Google Vertex AI, and Azure OpenAI. For other providers, route through an external LLM proxy like LiteLLM or Portkey alongside the built-in one. Pools using `deliveryMode: direct` deliver the provider credential to the pod for the lease duration (for the `anthropic_direct` provider, the API key itself), so runtimes that must call the provider themselves can do so without the gateway on the request path. Direct mode is intended for single-tenant and development deployments where the agent pod is trusted, and a multi-tenant deployment rejects direct mode combined with the `standard` profile.

### Delegation controls

An agent asks to delegate work with a single tool call. The gateway tracks the whole tree and enforces, at every hop:

- A maximum depth and fan-out (how many parallel children are allowed)
- A token budget allocated from the parent and debited atomically
- A cap on the total tree's size and memory
- That a child's permissions are a subset of its parent's
- That a child's isolation profile is the same as its parent's or stricter
- That content policies can only be tightened down the tree
- That the tree can't form a cycle (an agent delegating to its ancestor)

The parent doesn't see the child's pod address, internal endpoints, or raw credentials. It sees a virtual interface for status, cancellation, forwarding user-input requests, and message delivery.

### Extension interfaces

Memory, caching, guardrails, evaluation scoring, and credential routing are defined as interfaces. Lenny ships a default implementation of each that is disabled unless enabled, and any of them can be replaced with another tool. Lenny does not implement evaluation scoring or safety classification; it provides the interfaces those tools are wired into.

### Experimentation

Lenny's focus is **infrastructure primitives** for rolling runtime versions: pools of pod variants, deterministic request routing to a variant, and delivery of the chosen variant in each session's [`session_start` frame](../reference/adapter-contract.md#inbound-messages-adapter-writes-to-your-stdin) so the runtime knows which configuration it's running under. Every experimentation flow needs these primitives.

A **basic built-in variant assigner** ships with the platform. It supports deterministic bucketing on a session-level key (for example, tenant or user ID) with configurable split ratios. It is intentionally limited — enough for simple runtime-version rollouts, not enough to replace a real experimentation platform.

Most teams will plug in an **external experimentation platform** (LaunchDarkly, Statsig, Unleash, or any OpenFeature-compatible provider) for assignment decisions. Lenny integrates through OpenFeature, so the assignment platform is swappable and experiment lifecycle management — targeting rules, rollout curves, auto-winner declaration, stats — lives where your team already runs it.

What Lenny deliberately does **not** provide: experiment lifecycle management, statistical significance testing, multi-armed bandits, or auto-winner declaration. All transitions between variants are operator-initiated or driven by the external platform you plug in.

### Evaluation

Lenny is **not an eval platform** and does not ship one. Runtime builders choose whichever eval framework fits their workflow — LangSmith, Braintrust, Arize, Langfuse, or a home-grown pipeline — and Lenny stays out of the way. The gateway propagates `tracingContext` across delegation chains so those external platforms can stitch traces end-to-end.

For teams that want to persist scores alongside session state without standing up another system, Lenny exposes a **basic score storage and retrieval mechanism** (`/eval` endpoint). It is a database table with an API in front of it — not an eval runner, not a judge, not a scoring model. Use it if it fits; replace it or ignore it otherwise.

### Operational API

Lenny ships with a dedicated management plane (`lenny-ops`) that exposes structured endpoints for diagnostics, runbooks, backups, drift detection, and cluster management, regardless of deployment size. `lenny-ctl doctor --fix` applies idempotent remediations for common misconfigurations.

Every state an operator might need is exposed as a structured API, so an AI DevOps agent can inspect and remediate the platform without screen-scraping `kubectl`.

---

## Trade-offs

- **Pre-warmed pods consume resources when idle.** Scale-to-zero schedules mitigate this for off-peak hours; during active periods the resource cost is real.
- **No shared storage mounts.** Workspace materialization adds latency to every session start. The trade is isolation and auditability.
- **Least privilege by default.** Credential management is more involved -- credential pools, lease rotation, per-session scoping -- than mounting API keys directly.
- **Deeper integration takes more code.** The Basic level needs no Lenny dependency. Standard adds an MCP client connection, and Full adds the lifecycle-signal socket. Teams that want in-place credential rotation, clean interrupts, and cooperative checkpointing have to do that work.
- **No eval scoring or memory extraction built in.** Deployers bring their own evaluation pipelines, scoring tools, and memory extraction logic. Lenny provides the hooks.
- **No automatic experiment lifecycle management.** All experiment transitions are operator-initiated. Lenny does not build auto-winner declaration, statistical significance testing, or multi-armed bandits.

---

## Related reading

- [Comparisons](comparisons) -- side-by-side with E2B, Daytona, Fly.io Sprites, Temporal, Modal, and LangGraph/LangSmith.
- [Architecture Overview](../getting-started/architecture) -- components, data flow, security boundaries.
- [Core Concepts](../getting-started/concepts) -- sessions, runtimes, pools, gateway, delegation, workspaces, MCP, tenants, credentials.
- [Agent Operability](../operator-guide/agent-operability) -- the `lenny-ops` control plane and the `doctor --fix` remediation loop.
