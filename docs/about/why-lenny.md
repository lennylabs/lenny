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

## The problem

Teams building AI agents face a common infrastructure challenge: they need cloud-hosted agent sessions that:

- **Start fast** -- users and CI pipelines cannot wait 30 seconds for a container to boot.
- **Run in isolation** -- each session needs its own workspace, credentials, and security boundary.
- **Support interactive workflows** -- agents need to ask the user questions, request tool approvals, stream partial output, and handle interrupts mid-execution.
- **Delegate recursively** -- an orchestrator agent needs to spawn child agents with scoped budgets and permissions, forming deep delegation trees.
- **Work with any runtime** -- teams use different agent frameworks (LangChain, CrewAI, Autogen, custom code) and cannot be locked into a single SDK.
- **Support experimentation and evaluation** -- teams need to A/B test runtime versions, score agent output, and replay sessions for regression testing.
- **Comply with enterprise requirements** -- audit logging, data residency, GDPR erasure, credential rotation, and content policy enforcement.

Existing solutions each solve part of this problem but force significant trade-offs. Sandbox platforms (E2B, Daytona) provide fast isolated environments but lack orchestration contracts and delegation primitives. Workflow engines (Temporal) provide durability but require agent logic to use their SDK. Framework-coupled platforms (LangGraph/LangSmith) provide rich agent primitives but only within their ecosystem.

Lenny occupies a distinct point in this design space: a **Kubernetes-native, runtime-agnostic agent session platform** that provides all of these capabilities behind a unified gateway.

---

## Architectural differentiators

These are architectural commitments reflected throughout the specification, not roadmap aspirations.

### 1. Runtime-agnostic adapter contract

Lenny does not bundle or mandate a specific agent framework. Any process that implements the runtime adapter can run as a Lenny agent pod -- Claude Code, a custom LangChain agent, or a bare-metal script.

The adapter contract is tiered by integration depth:

| Tier         | Interface               | Effort                    | Capabilities                                                       |
| :----------- | :---------------------- | :------------------------ | :----------------------------------------------------------------- |
| **Minimum**  | stdin/stdout JSON Lines | ~50 lines of code, no SDK | Basic session lifecycle, text I/O                                  |
| **Standard** | gRPC adapter            | Moderate                  | Lifecycle management, credential flows, workspace notifications    |
| **Full**     | gRPC adapter            | Significant               | Checkpointing, credential rotation, delegation, interrupt handling |

The key distinction from competitors: the adapter contract separates **platform integration** (adapter) from **agent logic** (runtime). Agent code remains framework-independent even when the adapter layer carries Lenny-specific integration effort. E2B and Daytona provide sandbox environments but assume the operator brings their own orchestration. Temporal and LangGraph require agent logic itself to use their respective SDKs.

### 2. Recursive delegation as a platform primitive

Any agent pod can spawn child sessions through the gateway with enforced scope, token budget, and lineage tracking. This is not a library-level feature bolted on -- it is a first-class gateway operation with policy enforcement at every hop.

**What the gateway enforces per delegation:**

- Maximum delegation depth
- Fan-out limits (parallel children)
- Token budget (allocated from parent, tracked via Redis Lua scripts)
- Tree size and memory caps
- Isolation monotonicity (children must be at least as isolated as parents)
- Content policy inheritance (can only be made stricter, never relaxed)
- Cycle detection (prevents A -> B -> A runtime identity loops)

**What the parent sees:** A virtual MCP child interface with task status, elicitation forwarding, cancellation, and message delivery. Never pod addresses, internal endpoints, or raw credentials.

LangSmith's RemoteGraph offers graph-level delegation but without per-hop budget/scope controls enforced at the platform layer.

### 3. Self-hosted, Kubernetes-native

Lenny runs on the operator's own cluster using standard Kubernetes primitives -- CRDs, RuntimeClasses, namespaces, HPA, PDBs, topology spread constraints. There is no dependency on a vendor-hosted control plane.

**What this means in practice:**

- Deploy with `helm install` on any conformant Kubernetes cluster.
- All data stays in your infrastructure (Postgres, Redis, MinIO in your cluster or managed services).
- Local development mode (`make run`) works with zero cloud dependency.
- Deployer-selectable isolation profiles: runc (fast, standard isolation), gVisor (userspace syscall interception), Kata (microVM, strongest isolation).

### 4. Multi-protocol gateway

A single gateway edge serves MCP, OpenAI Completions, and Open Responses clients via the `ExternalAdapterRegistry`. Operators do not need separate infrastructure per client protocol.

**Supported external protocols:**

- **MCP (Model Context Protocol)** -- Tasks, Elicitation, streaming, tool-use
- **OpenAI Chat Completions** -- `/v1/chat/completions` compatible
- **Open Responses** -- `/v1/responses` compatible
- **A2A (Agent-to-Agent)** -- via ExternalAdapterRegistry (post-v1)
- **Agent Protocol** -- via ExternalAdapterRegistry (post-v1)

Internally, the gateway operates against a canonical session/task state machine; protocol adapters handle translation at the boundary. MCP Tasks are implemented at the gateway's external interface; internal delegation uses a custom gRPC protocol with equivalent semantics. Third-party adapters can be built and validated via the `RegisterAdapterUnderTest` compliance suite.

### 5. Enterprise controls at the platform layer

Rate limiting, token budgets, concurrency controls, isolation profiles, audit logging, and least-privilege pod security are built into the gateway and controller layers.

**Key enterprise features:**

- **Multi-tenancy** with Postgres row-level security, per-tenant quotas, and RBAC
- **Credential pools** with lease rotation, health scoring, and emergency revocation
- **Token budgets** enforced per delegation tree with Redis-backed atomic accounting
- **Audit logging** with hash-chained integrity, configurable retention presets (SOC2, HIPAA, FedRAMP), and SIEM forwarding
- **Content policy interceptors** for prompt injection detection and output filtering
- **Data residency** controls with region-validated storage routing
- **Legal holds** on sessions and artifacts to suspend retention policies for compliance investigations
- **GDPR data erasure** with phased `DeleteByUser` / `DeleteByTenant`, billing pseudonymization, erasure receipts, and processing restriction during erasure (Article 18)

### 6. Ecosystem-composable via hooks-and-defaults

Every cross-cutting AI capability -- memory, caching, guardrails, evaluation, routing -- is defined as an interface with a sensible default, disabled unless explicitly enabled, and fully replaceable.

Lenny never implements AI-specific logic (eval scoring, memory extraction, content classification). That belongs to specialized tools the deployer already uses. This is a deliberate architectural stance:

- **LangChain/LangSmith** bundles its own evaluators, tracing, and memory, requiring adopters to work within or around that stack.
- **Modal** provides no agent-specific hooks at all.
- **Lenny** provides the platform layer and stays out of the ecosystem layer, so deployers compose their preferred tools without fighting the platform.

---

## Complete feature inventory

Beyond the 6 architectural differentiators, Lenny includes a comprehensive set of platform features organized by category.

### Experimentation

Lenny includes first-class A/B experimentation primitives for runtime version rollouts and agent evaluation.

- **ExperimentDefinition** as a first-class admin API resource with `active`, `paused`, and `concluded` lifecycle states.
- **ExperimentRouter** as a built-in `RequestInterceptor` that routes sessions to variant pools based on deterministic bucketing (HMAC-SHA256 cumulative-weight partitioning).
- **Targeting modes**: `percentage` (deterministic hash-based, no external dependency) and `external` (webhook-based, delegates to LaunchDarkly, Statsig, Unleash, or any generic webhook).
- **Sticky assignment**: per-user, per-session, or no stickiness. Cached in Redis with automatic invalidation on experiment pause/conclude.
- **Variant pool sizing**: PoolScalingController automatically sizes variant pools proportional to traffic weight, and simultaneously adjusts the base pool's `minWarm` to avoid over-provisioning.
- **Delegation propagation**: configurable per-experiment (`inherit`, `control`, `independent`) -- controls whether child sessions inherit the parent's variant assignment.
- **Isolation monotonicity check**: variant pools must satisfy the session's minimum isolation profile, preventing experiments from silently weakening security.
- **Anonymous session handling**: null `user_id` sessions always route to control, preventing hash-collision concentration.
- **Multi-experiment first-match rule**: a session is enrolled in at most one experiment, keeping attribution unambiguous.
- **Manual rollback triggers**: recommended Prometheus alert thresholds for error rate, latency, eval score degradation, and safety score regression.

### Evaluation hooks

Lenny provides a pull-based evaluation framework that integrates with any external scoring pipeline without imposing opinions on eval methodology.

- **Eval submission endpoint**: `POST /v1/sessions/{id}/eval` accepts scores from any authenticated principal (agent runtime, session owner, or external scorer pipeline).
- **Multi-dimensional scoring**: `score` (aggregate float) and `scores` (per-dimension breakdown, e.g., `{"coherence": 0.9, "relevance": 0.7, "safety": 1.0}`).
- **Automatic experiment attribution**: the gateway auto-populates `experiment_id` and `variant_id` from the session's experiment context -- no scorer-side wiring needed.
- **Results API**: `GET /v1/admin/experiments/{name}/results` returns aggregated scores per variant with mean, p50, p95, and per-dimension breakdowns.
- **Session replay**: `POST /v1/sessions/{id}/replay` re-runs a completed session's prompt history against a different runtime version for regression testing and A/B evaluation.
- **Delegation-aware attribution**: `delegation_depth` and `inherited` fields on each `EvalResult` enable operators to distinguish direct vs. propagated child results and filter for sample contamination.
- **Idempotent submissions**: optional `idempotency_key` prevents duplicate scoring in pipeline retries.
- **Rate-limited**: 100 evals per session per minute, 10,000 per tenant per minute.
- **Pre-computed aggregation**: optional Postgres materialized view (`lenny_eval_aggregates`) for high-volume experiments.

What Lenny explicitly does **not** build: statistical significance testing, automatic winner declaration, multi-armed bandits, LLM-as-judge integration, or segment analysis. Those belong in dedicated experimentation platforms.

### Session replay and derivation

- **Session derive**: `POST /v1/sessions/{id}/derive` creates a new session pre-populated with a completed session's workspace snapshot -- useful for forking work from a known-good state.
- **Session replay**: `POST /v1/sessions/{id}/replay` replays a completed session's prompt history against a different runtime version. Two modes: `prompt_history` (replays the full transcript) and `workspace_derive` (clean start with identical filesystem).
- **Eval integration**: replayed sessions can be linked to an eval experiment via `evalRef`, enabling systematic regression testing across runtime versions.

### Memory store

- **`MemoryStore` interface** with `Write`, `Query`, `Delete`, and `List` operations, scoped by tenant, user, agent type, and session.
- **Default implementation**: Postgres + pgvector with full RLS tenant isolation.
- **Fully replaceable**: deployers can swap in Mem0, Zep, or any vector database by implementing the interface. A `ValidateMemoryStoreIsolation` contract test verifies tenant isolation for custom implementations.
- **Platform MCP tools**: `lenny/memory_write` and `lenny/memory_query` available to agent runtimes via the platform MCP server.
- **Retention controls**: configurable `maxMemoriesPerUser` (default: 10,000) with oldest-first eviction, and optional `retentionDays` TTL.
- **Instrumentation contract**: all implementations must emit standardized Prometheus metrics (`operation_duration_seconds`, `errors_total`, `record_count`).

### Semantic cache

- **`SemanticCache` interface** on `CredentialPool` for caching LLM responses.
- **Pluggable**: deployers implement the interface with their preferred caching backend (Redis, dedicated vector store, etc.).
- **Disabled by default**: opt-in per credential pool, no performance overhead when unused.

### Gateway request interceptors (hooks)

The gateway's `RequestInterceptor` chain provides hook points at every stage of request processing. External interceptors are invoked via gRPC (like Kubernetes admission webhooks).

**Built-in interceptors:**

| Interceptor                 | Purpose                                        | Default state          |
| :-------------------------- | :--------------------------------------------- | :--------------------- |
| `AuthEvaluator`             | AuthN/AuthZ, user invalidation                 | Always active          |
| `QuotaEvaluator`            | Rate limits, token budgets, concurrency limits | Always active          |
| `DelegationPolicyEvaluator` | Depth, fan-out, policy tag matching            | Always active          |
| `ExperimentRouter`          | A/B experiment variant assignment              | When experiments exist |
| `GuardrailsInterceptor`     | Content safety classification                  | Disabled by default    |
| `RetryPolicyEvaluator`      | Retry eligibility, resume window               | Always active          |

**Interceptor phases** (hook points for custom logic):

- `PreAuth` / `PostAuth` -- before/after authentication
- `PreRoute` -- before runtime/pool selection (where ExperimentRouter operates)
- `PreDelegation` -- before processing a `delegate_task` call (content policy enforcement)
- `PreMessageDelivery` -- before delivering a message to a running session
- `PostRoute` -- after routing decision
- `PreToolResult` / `PostAgentOutput` -- around tool execution and agent output
- `PreLLMRequest` / `PostLLMResponse` -- around LLM provider calls (via LLM Proxy)
- `PreConnectorRequest` / `PostConnectorResponse` -- around external tool calls

**GuardrailsInterceptor**: disabled by default; deployers wire in external classifiers such as AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or custom gRPC services. The interceptor can `ALLOW`, `DENY`, or `MODIFY` content at any phase.

### Credential management

- **Credential pools**: groups of API keys for a provider, with round-robin or weighted selection, health scoring, and automatic failover.
- **Credential leasing**: per-session leases with TTL, rotation mid-session, and emergency revocation.
- **LLM Proxy**: gateway subsystem that injects real API keys into LLM provider requests on behalf of pods. Pods never see raw credentials -- they receive opaque lease tokens.
- **Pre-authorized flow**: users register credentials once; every session auto-resolves based on authenticated identity.
- **Credential routing**: pluggable `CredentialRouter` interface for cost-aware, latency-based, or intent-based routing across providers. Deployers pass custom `hints` (model, cost_tier, region) without modifying the core interface.
- **SPIFFE-bound lease tokens**: in multi-tenant deployments, lease tokens are bound to the issuing pod's SPIFFE identity to prevent cross-pod replay.

### Storage architecture

- **Pluggable storage interfaces**: `SessionStore`, `ArtifactStore`, `EventStore`, `TokenStore`, `MemoryStore`, `CredentialPoolStore`, `QuotaStore`, `EvalResultStore` -- all defined as Go interfaces with default implementations backed by Postgres, Redis, and MinIO.
- **`StoreRouter`**: routes storage operations based on data residency region, directing writes to region-local backends.
- **Data classification**: T1 (Public), T2 (Internal), T3 (Confidential/PII), T4 (Restricted/regulated) with per-tier encryption and retention requirements.
- **Cloud-managed backends**: RDS/Cloud SQL for Postgres, ElastiCache/Memorystore for Redis, S3/GCS/Azure Blob for artifacts.

### Compliance and data governance

- **Audit logging**: append-only event store with hash-chained integrity, SIEM forwarding, and configurable retention presets for SOC2, HIPAA, and FedRAMP.
- **Legal holds**: sessions and artifacts can be placed on legal hold to suspend retention policies and checkpoint rotation. A background reconciler detects pre-existing checkpoint gaps.
- **GDPR data erasure**: phased `DeleteByUser` with 19-step dependency-ordered deletion across all stores, billing pseudonymization via per-tenant `erasure_salt` (with immediate salt deletion for Recital 26 compliance), processing restriction during erasure (Article 18), and cryptographic erasure receipts.
- **Tenant deletion lifecycle**: 6-phase process (soft-disable → terminate sessions → revoke credentials → delete data → clean CRDs → produce receipt) with SLA enforcement (T3: 72h, T4: 4h).
- **Data residency**: per-tenant/per-environment `dataResidencyRegion` with fail-closed storage routing. Pod pool routing restricts delegation targets to region-matching pools.

### Metering and billing

- **Billing event stream**: append-only, gap-detected billing events with monotonic sequence numbers for financial reconciliation.
- **Usage API**: `GET /v1/usage` with filtering by tenant, user, time window, and labels. Returns sessions, tokens (input/output), and pod-minutes.
- **Metering events**: `GET /v1/metering/events` provides a paginated billing event stream for integration with external billing systems.
- **Per-tenant and per-user quota enforcement**: token budgets, concurrent session limits, and rate limits with Redis-backed atomic accounting.

### Workspace management

- **Workspace Plan schema**: declarative workspace definition with files, setup commands, and configuration -- submitted at session creation.
- **Setup commands**: executed during workspace finalization with configurable timeouts, allowlists, and failure policies (`fail` or `continue`).
- **Concurrent workspace mode**: Full-tier runtimes can operate on multiple workspace slots simultaneously, with slot-scoped file delivery and checkpointing.
- **Workspace size limits**: hard `emptyDir.sizeLimit` enforced by kubelet, plus pre-checkpoint size probes to prevent unbounded agent quiescence.
- **`.lennyignore`**: deployers can exclude files from checkpoint snapshots to reduce checkpoint duration and storage.

### Observability

- **100+ Prometheus metrics** across gateway subsystems, pools, sessions, checkpoints, delegation, credentials, and experiments.
- **SLOs**: startup latency (P95 < 2s runc, < 5s gVisor), checkpoint duration (P95 < 2s for workspaces ≤ 100MB), checkpoint freshness (every 10 minutes).
- **Distributed tracing**: OpenTelemetry integration with trace propagation across gateway → pod → delegation chains.
- **Structured logging**: per-component, per-session log streams with configurable retention.
- **Alert rules**: 23+ critical alerts, 30+ warning alerts, and 8 SLO burn-rate alerts defined in the spec.
- **Grafana dashboards**: pre-built dashboard templates for gateway health, pool status, session lifecycle, and experiment results.

### Local development

- **`make run`** (Tier 1): zero-dependency, single binary with embedded SQLite, in-memory caches, and local filesystem for artifacts. Target: clone-to-echo-session in under 5 minutes.
- **`make dev`** (Tier 2): Docker Compose with real Postgres, Redis, and MinIO for integration testing.
- **Hot reload**: runtime adapter development with fast iteration.
- **`lenny-ctl`**: full CLI for operators with commands for bootstrap, pool management, credential management, circuit breakers, experiments, and more.

---

## Target personas

| Persona                       | Motivation                                                                                   | Entry point                                           |
| :---------------------------- | :------------------------------------------------------------------------------------------- | :---------------------------------------------------- |
| **Runtime authors**           | Integrate their agent framework (LangChain, CrewAI, custom) with a managed session platform. | Runtime adapter contract, `make run` local dev mode.  |
| **Platform operators**        | Run multi-tenant agent infrastructure on their own clusters.                                 | Helm chart, `lenny-ctl bootstrap`, Admin API.         |
| **Enterprise platform teams** | Evaluate Lenny against E2B/Daytona for internal agent hosting with policy controls.          | Comparison guides, enterprise controls documentation. |

---

## Time to Hello World

**Target: under 5 minutes.** A new contributor must be able to clone the repo, run `make run`, and complete a round-trip echo session within 5 minutes on a standard development machine.

The `make run` local dev mode runs with embedded stores (SQLite for Postgres, in-process Redis, local filesystem for MinIO) and the echo runtime sample. CI includes a TTHW smoke test that validates this path on every merge.

---

## Design principles

1. **Gateway-centric.** All external interaction goes through the gateway. Pods are internal workers, never directly exposed. This creates a single enforcement point for auth, policy, rate limiting, and audit.

2. **Pre-warm everything possible.** Pods are warm before requests arrive. Workspace setup is the only hot-path work. The pod-claim-and-routing step alone is in the millisecond range.

3. **Pod-local workspace, gateway-owned state.** Pod disk is a cache. Session truth lives in durable stores (Postgres, Redis, MinIO). This makes pods disposable and sessions recoverable.

4. **MCP for interaction, custom protocol for infrastructure.** Use MCP where its semantics matter (tasks, elicitation, auth, delegation). Use a custom gRPC protocol for lifecycle plumbing (pod control, credential delivery, checkpoint coordination).

5. **Recursive delegation as a platform primitive.** Any pod can delegate to other pods through gateway-mediated tools. The gateway enforces scope, budget, and lineage at every hop.

6. **Least privilege by default.** No broad credentials in pods. No shared mounts. Gateway-mediated file delivery only. Credentials are leased per-session with rotation and revocation support.

---

## Trade-offs

The differentiators above involve deliberate trade-offs that evaluators should weigh:

- **No shared storage mounts** means workspace materialization can add latency to every session start.
- **Least privilege by default** means more complex credential management (credential pools, lease rotation, per-session scoping) than competitors that simply mount API keys.
- **Integration with adapter effort** means runtime authors meaning to take full advantage of Lenny's capabilities need to integrate with Lenny's adapter.
