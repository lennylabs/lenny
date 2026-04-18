---
layout: default
title: Glossary
parent: Reference
nav_order: 5
---

# Glossary
{: .no_toc }

Alphabetical reference for all Lenny-specific terms and concepts.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## A

**Adapter (Runtime Adapter)**
: A sidecar or process within an agent pod that implements the Lenny control protocol (gRPC or stdio JSON Lines). The adapter translates between Lenny's lifecycle management and the agent binary's native interface. It handles session start/stop, workspace notifications, credential delivery, checkpointing, and streaming I/O. See [Runtime Author Guide](../runtime-author-guide/).

**Agent Binary**
: The actual agent process that runs inside a pod -- Claude Code, a LangChain agent, a custom Python script, or any other process. The agent binary is distinct from the adapter; it contains the agent's logic while the adapter handles platform integration. The agent binary does not need to know about Lenny's existence.

**Agent Pod**
: A Kubernetes pod running in the `lenny-agents` (or `lenny-agents-kata`) namespace that hosts a runtime adapter and agent binary. Agent pods are pre-warmed by the Warm Pool Controller and claimed by the gateway when a session is created. Each pod runs with a deployer-selected isolation profile (runc, gVisor, or Kata). See [State Machines](state-machines).

**Artifact Store**
: Durable object storage (MinIO, S3, GCS, or Azure Blob) used to persist workspace snapshots, session transcripts, logs, and checkpoint data. Artifacts are organized under a tenant-scoped path (`/{tenant_id}/{object_type}/{session_id}/...`). Retention is configurable per session with a default of 7 days.

**Awaiting Client Action**
: A session state entered when automatic retry attempts are exhausted or `maxResumeWindowSeconds` has elapsed. The session requires explicit client intervention -- resume, restart, download artifacts, or terminate. Active children continue running. Expires after `maxAwaitingClientActionSeconds`. See [State Machines](state-machines).

## C

**Compliance Profile**
: A regulatory compliance configuration applied to a tenant (`soc2`, `fedramp`, `hipaa`) that affects audit retention periods, encryption requirements, grant-check intervals, and pgaudit/SIEM enforcement. See [Configuration Reference](configuration).

**Checkpoint**
: A periodic snapshot of a session's workspace state written to durable storage (MinIO). Checkpoints enable session recovery after pod failures by restoring the workspace to its last known-good state. Governed by `periodicCheckpointIntervalSeconds` (default: 600s) and bounded by `workspaceSizeLimitBytes` (default: 512 Mi). See [Configuration Reference](configuration).

**Checkpoint Freshness SLO**
: The operational target that no active session's last checkpoint age exceeds `periodicCheckpointIntervalSeconds`. Monitored via `lenny_checkpoint_stale_sessions` gauge and the `CheckpointStale` alert. See [Metrics Reference](metrics).

**Circuit Breaker**
: An automatic or operator-managed mechanism that stops traffic to a failing component. Lenny uses two kinds: (1) per-subsystem automatic circuit breakers within the gateway binary (per-replica, in-memory), and (2) operator-managed global circuit breakers backed by Redis that propagate across all replicas. See [Operator Guide](../operator-guide/).

**Claim (Pod Claim)**
: The process by which the gateway acquires an idle warm pod for a new session. Claims use a `SandboxClaim` CRD with optimistic locking. A `lenny-sandboxclaim-guard` admission webhook prevents double-claims. See [State Machines](state-machines).

**Connector**
: An external MCP server (e.g., GitHub, Jira) registered as a top-level admin API resource, with gateway-managed OAuth and encrypted token storage. Connectors are proxied to agent sessions and are subject to content policy interceptors. See [API Reference](../api/).

**Credential Lease**
: A time-bounded assignment of a credential from a credential pool to a specific session. Leases are managed by the Token Service and include the materialized provider configuration. Leases are per-session in session mode, per-task in task mode, and per-slot in concurrent mode. See [Configuration Reference](configuration).

**Credential Pool**
: A set of API keys or OAuth credentials for a specific LLM provider or external service. Credentials are assigned to sessions via the Token Service with round-robin, least-recently-used, or affinity-based selection. Pool utilization is monitored via Prometheus metrics. See [Operator Guide](../operator-guide/).

**CRD (Custom Resource Definition)**
: A Kubernetes extension mechanism used by Lenny to define platform-specific resources. Key CRDs include `Sandbox` (agent pod), `SandboxTemplate` (pool template), `SandboxClaim` (pod claim), and `RuntimeUpgrade` (rolling upgrade). See [Operator Guide](../operator-guide/).

## D

**Data Residency**
: A per-tenant regional constraint controlling where session data, artifacts, and checkpoints are stored. Specified via `dataResidencyRegion` at session creation. Enforced at storage and pool selection time; violations return `REGION_CONSTRAINT_VIOLATED` or `REGION_CONSTRAINT_UNRESOLVABLE`. See [Error Catalog](error-catalog).

**Delegation**
: An operation by which an agent pod spawns a child session through the gateway. The gateway enforces policy, budget, scope, depth limits, and isolation monotonicity at every hop. The parent interacts with the child through a virtual MCP child interface. See [State Machines](state-machines).

**Derived Session**
: A session created via `POST /v1/sessions/{id}/derive`, forking from a completed session's workspace snapshot. The derived session starts with the source session's workspace but its own independent lifecycle, credential lease, and event stream. See [REST API Reference](../api/rest).

**Delegation Policy**
: A named, top-level API resource defining which runtimes, connectors, and pools a session can delegate to. Uses tag-based matching with `include`/`exclude` rules. Effective policy is the intersection of runtime-level and derived-runtime policies. See [API Reference](../api/).

**Environment**
: A named, RBAC-governed project context that groups runtimes and connectors for a team. Environments provide scoping for session creation, runtime discovery, and delegation policy evaluation. Managed via the Admin API.

**Eval Result**
: A score record stored via `POST /v1/sessions/{id}/eval`, with multi-dimensional scores (e.g., accuracy, helpfulness) and automatic variant attribution when a variant pool is active. Lenny is not an eval platform — this is a basic score storage mechanism; runtime builders choose whichever eval framework they prefer (LangSmith, Braintrust, Arize, Langfuse, home-grown). Subject to `maxEvalsPerSession` quota. See [Configuration Reference](configuration).

**Experiment**
: A variant-pool configuration for runtime version rollouts, with variant pools and deterministic sticky routing. Lenny provides the infrastructure primitives (pools, routing, manifest delivery) and a basic built-in variant assigner; for anything beyond simple rollouts, integrate an external experimentation platform via OpenFeature (LaunchDarkly, Statsig, Unleash). Managed via the Admin API.

**Elicitation**
: A mechanism by which an agent or tool requests interactive input from the human user. Elicitations flow through the delegation chain -- child sessions can trigger elicitations that bubble up to the root session's client. Subject to `maxElicitationsPerSession`, `maxElicitationWait`, and `elicitationDepthPolicy` controls. See [Configuration Reference](configuration).

**Elicitation Chain**
: The path an elicitation request takes from its originating session up through the delegation tree to the root session's client, and the return path of the response back down. Each hop is a child span in the trace. Deep elicitation suppression applies at configurable delegation depth.

**Event Store**
: A Postgres-backed store for session events, audit records, and stream cursors. Events are partitioned by time with configurable retention. The Event Store supports replay for client reconnection within the replay window. See [Configuration Reference](configuration).

**Protocol Adapter Registry**
: The gateway component that maps external client protocols (MCP, OpenAI, Open Responses, A2A) to Lenny's internal session model. Each protocol is handled by a registered adapter that translates between the external wire format and Lenny's canonical session/task states. See [API Reference](../api/).

## G

**Gateway**
: The only externally-facing component of the Lenny platform. All client interaction enters through the gateway, which handles authentication, routing, policy enforcement, streaming proxy, file uploads, delegation orchestration, credential injection, and MCP protocol translation. Deployed as stateless replicas behind an ingress/load balancer with HPA. See [Getting Started](../getting-started/).

**Gateway Subsystem**
: One of four internal boundaries within the gateway binary: Stream Proxy, Upload Handler, MCP Fabric, and LLM Proxy. Each subsystem has its own goroutine pool, circuit breaker, and metrics. Subsystems can be extracted to dedicated services if scaling requires it. See [Metrics Reference](metrics).

**gVisor**
: A container runtime sandbox that intercepts syscalls in userspace, providing stronger isolation than runc without the overhead of a full virtual machine. Used as the `gvisor` isolation profile in Lenny. Pods using gVisor skip the seccomp profile check (seccomp is a no-op under gVisor). See [Configuration Reference](configuration).

## I

**Interceptor**
: A content policy evaluation hook in the gateway's 12-phase request processing chain. Interceptors can inspect and modify (or reject) traffic at specific phases. Built-in interceptors handle security-critical phases (priority <= 100); external interceptors are registered via gRPC and run at configurable phases with priority > 100. See [Error Catalog](error-catalog).

**Integration Level**
: The level of Lenny platform integration a runtime adapter implements. Three levels: Basic (stdin/stdout JSON Lines, ~50 lines of code), Standard (gRPC adapter with lifecycle), and Full (gRPC with checkpointing, credential rotation, delegation support). See [Runtime Author Guide](../runtime-author-guide/).

**Isolation Monotonicity**
: The security invariant that a child delegation's isolation profile must be at least as restrictive as the parent's. The ordering is: `runc` < `gvisor` < `microvm` (Kata). Delegations that would weaken isolation are rejected with `ISOLATION_MONOTONICITY_VIOLATED`. See [Error Catalog](error-catalog).

## K

**Kata Containers**
: A container runtime that uses lightweight virtual machines for strong isolation. Used as the `microvm` isolation profile. Kata pods must run on dedicated node pools with hard scheduling constraints. See [Configuration Reference](configuration).

## L

**Lease**
: See [Credential Lease](#credential-lease).

**Lifecycle Channel**
: The gRPC bidirectional stream between the gateway and a pod's runtime adapter. Used for control-plane signals: session start/stop, interrupt, checkpoint requests, credential rotation, and workspace notifications. Distinct from the data-plane stdin/stdout pipe.

**LLM Proxy**
: The gateway subsystem that talks to LLM providers on behalf of agent pods. It validates the pod's short-lived lease token, injects the real provider API key (which lives only in the gateway's memory), and forwards streaming requests to the upstream provider. Because the gateway handles this hop, pods never see real API keys and credential rotation is zero-downtime. See [Metrics Reference](metrics).

## M

**Memory Store**
: Pluggable persistence layer for agent memories, scoped by tenant/user/agent/session. Runtimes write memories via `lenny/memory_write` and query them via `lenny/memory_query`. Default implementation uses Postgres with pgvector for semantic search. Governed by `memory.maxMemoriesPerUser` and `memory.retentionDays`. See [Configuration Reference](configuration).

**MCP (Model Context Protocol)**
: The primary client-facing protocol for Lenny sessions. MCP defines Tasks, Elicitation, streaming, and tool-use semantics. Lenny implements MCP Tasks at the gateway's external interface; internal delegation uses a custom gRPC protocol. See [API Reference](../api/).

**MCP Fabric**
: A gateway subsystem responsible for delegation orchestration, virtual child MCP interfaces, and elicitation chain management. See [Metrics Reference](metrics).

**MinIO**
: The default S3-compatible object storage used for artifact, checkpoint, and workspace snapshot storage. Deployments can substitute any S3-compatible service, GCS, or Azure Blob. See [Configuration Reference](configuration).

## O

**OutputPart**
: The unit of content delivery in Lenny sessions. OutputParts support multiple types (text, code, image, tool-use, binary blob) and can be delivered inline or via external blob reference. Each part has a 50 MB size limit. Third-party types should use the `x-<vendor>/` namespace prefix. See [API Reference](../api/).

## P

**PDB (PodDisruptionBudget)**
: A Kubernetes resource that limits simultaneous voluntary disruptions. Lenny uses PDBs on gateway replicas and optionally on warm pool idle pods to enforce minimum availability during node drains and rolling updates.

**Platform MCP Server**
: The internal MCP server exposed by the gateway to each agent pod. Provides platform tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree`, and others. These tools are the pod's only interface for inter-session communication and delegation.

**Pod-warm**
: The default warm pod state where the agent process is NOT started. The pod is scheduled, the adapter is healthy, and the workspace is empty. The agent session starts only after workspace finalization. Contrasted with SDK-warm. See [State Machines](state-machines).

**Pool**
: A logical grouping of warm pods sharing the same runtime, resource class, and isolation profile. Pools are defined via `SandboxTemplate` CRDs and managed by the Warm Pool Controller. Each pool has its own `minWarm` target, scaling policy, and checkpointing configuration. See [Configuration Reference](configuration).

**PoolScalingController**
: A Kubernetes controller (deployed as 2+ replicas with leader election) that reconciles pool configuration from Postgres into CRDs and manages scaling intelligence. It computes `target_minWarm` based on workload profile assumptions and adjusts pool sizes. See [Metrics Reference](metrics).

**Pre-warming**
: The process of creating and preparing pods before any session request arrives. Pre-warmed pods are in `idle` state, ready for immediate claim. This eliminates cold-start latency from the session creation hot path.

**PSS (Pod Security Standards)**
: Kubernetes-defined security profiles (Privileged, Baseline, Restricted). Lenny uses a split enforcement model: full Restricted PSS for runc pods, relaxed RuntimeClass-specific constraints for gVisor and Kata pods. Enforcement is via RuntimeClass-aware admission policies (OPA/Gatekeeper or Kyverno).

## R

**RLS (Row-Level Security)**
: PostgreSQL row-level security policies that enforce tenant isolation at the database level. Every table in the session store is RLS-protected so that queries from one tenant cannot read or modify another tenant's data.

**runc**
: The standard OCI container runtime. Used as the `standard` isolation profile. Provides the fastest cold-start but the weakest isolation (shared kernel). Full Restricted PSS enforcement applies to runc pods.

**Runtime**
: A named, versioned agent binary configuration registered in the platform. Defines the container image, execution mode, capabilities, supported providers, SDK-warm settings, and delegation policy. Runtimes are the primary extension point for the platform. See [Runtime Author Guide](../runtime-author-guide/).

**RuntimeClass**
: A Kubernetes resource that specifies which container runtime handler (runc, gVisor, Kata) should be used for pods. Lenny uses RuntimeClasses to implement deployer-selectable isolation profiles.

## S

**Session Replay**
: Re-running a completed session's prompt history against a different runtime version for regression testing. Initiated via `POST /v1/sessions/{id}/replay`. The replayed session creates a new independent session with the source's workspace and prompt history but executes against the target runtime. See [REST API Reference](../api/rest).

**Sandbox CRD**
: The `kubernetes-sigs/agent-sandbox` Custom Resource Definition used by Lenny as its pod lifecycle primitive. `Sandbox` resources represent individual agent pods; `SandboxTemplate` resources define pool templates. Adopted as Lenny's infrastructure layer.

**SandboxTemplate**
: A CRD defining a warm pool's pod template -- container image, resource limits, RuntimeClass, environment variables, and other pod-level configuration. The Warm Pool Controller creates `Sandbox` pods from `SandboxTemplate` resources.

**Scope Narrowing**
: The security principle that delegation leases can only make policy stricter, never more permissive. Each child's effective budget, allowed runtimes, allowed connectors, messaging scope, and content policy must be equal to or more restrictive than its parent's.

**SDK-warm**
: An optional warm pod state where the agent process IS pre-connected and waiting for its first prompt. Eliminates SDK cold-start latency but requires the adapter to implement the `DemoteSDK` RPC for cases where workspace files must be present at start time. Enabled via `capabilities.preConnect: true`. See [State Machines](state-machines).

**Session**
: The primary user-facing resource in Lenny. A session represents a single interactive agent execution with its own workspace, credential lease, event stream, and lifecycle. Sessions are identified by a unique `session_id` and managed through the REST API. See [Getting Started](../getting-started/).

**Session Manager**
: The gateway component that manages session state in Postgres and Redis. Handles session creation, state transitions, coordination lease management, and orphan reconciliation.

**Setup Command**
: A command executed in the pod after workspace materialization but before the agent session starts. Used for dependency installation, compilation, or other workspace-specific preparation. Setup commands are bounded and logged.

**Streamable HTTP**
: The transport mechanism for Lenny's interactive sessions. Server-to-client events use SSE (Server-Sent Events); client-to-server messages use POST. Supports reconnection with cursor-based event replay.

**Stream Proxy**
: A gateway subsystem responsible for MCP streaming, session attachment, event relay, and client reconnection handling. See [Metrics Reference](metrics).

## T

**Task**
: The protocol-level abstraction for a unit of work in a delegation tree. Tasks have their own state machine (submitted, running, completed, failed, cancelled, expired, input_required) that maps to external protocol states (MCP Tasks, A2A). See [State Machines](state-machines).

**Task Tree**
: The hierarchical structure of delegation relationships. The root session spawns child tasks, which may spawn grandchildren, forming a tree. The tree is tracked via delegation leases and can be queried via `lenny/get_task_tree`. Tree size is bounded by `maxTreeSize` and `maxTreeMemoryBytes`.

**Tenant**
: An organizational unit for multi-tenancy isolation. Each tenant has its own quotas, rate limits, credential pools, RBAC configuration, and data isolation (enforced via Postgres RLS). Tenants are managed via the Admin API.

**Token Budget**
: A delegation-scoped limit on the total LLM tokens a delegation tree can consume. Tracked via Redis Lua scripts (`budget_reserve.lua`, `budget_return.lua`). When exhausted, returns `BUDGET_EXHAUSTED`. See [Error Catalog](error-catalog).

**Token Service**
: A stateless service (deployed as 2+ replicas with PDB) responsible for credential lifecycle management. Handles credential assignment, rotation, renewal, and revocation. Has its own ServiceAccount with KMS access. Communicates with the gateway via internal gRPC.

## U

**Upload Handler**
: A gateway subsystem responsible for file upload proxying, payload validation, staging to the Artifact Store, and archive extraction. Enforces path traversal protection, size limits, and zip-slip prevention. See [Metrics Reference](metrics).

**Upload Token**
: A short-lived, session-scoped HMAC-SHA256 signed token issued at session creation. Used to authorize pre-start file uploads. Format: `<session_id>.<expiry_unix_seconds>.<hmac_hex>`. Invalidated on `FinalizeWorkspace`. Must be treated as a secret credential.

## W

**Warm Pod**
: A pre-created pod sitting in `idle` state, ready for immediate claim. Warm pods eliminate cold-start latency. The Warm Pool Controller maintains `minWarm` idle pods per pool, replacing claimed or terminated pods. See [State Machines](state-machines).

**Warm Pool Controller**
: A Kubernetes controller (2+ replicas, leader election) that manages agent pod lifecycle via the `kubernetes-sigs/agent-sandbox` CRDs. Handles pod creation, state tracking, health monitoring, claim validation (via admission webhook), and garbage collection of orphaned claims. See [Operator Guide](../operator-guide/).

**Workspace**
: The pod-local filesystem (`/workspace/current`) containing the session's working files. Workspaces are materialized from client uploads during session creation, periodically checkpointed to durable storage, and sealed on session completion. Workspaces are never shared across sessions or pods.

**Workspace Plan**
: A structured schema defining how a session's workspace should be materialized. Includes file sources (client uploads, derived session snapshots, runtime defaults), directory structure, and environment variable configuration. Defined in Section 14 of the spec with a versioned `schemaVersion` field.
