# Lenny Technical Design

**Status:** Draft
**Date:** 2026-04-02
**Source:** Synthesized from design conversation (chatgpt-conversation.md), updated with design review decisions (design-updates-20260402.md)

---

## 1. Executive Summary

Lenny is a **Kubernetes-native, runtime-agnostic agent session platform** that provides on-demand, pre-warmed, isolated cloud agent instances to clients. It is not tied to any single agent runtime — it defines a standard contract that any compliant pod binary can implement.

The platform solves a specific problem: teams need cloud-hosted agent sessions (e.g., Claude Code, custom agents) that start fast, run in isolation, support long-lived interactive workflows, and can delegate work recursively — all behind a unified gateway that owns lifecycle, policy, security, and MCP-facing behavior.

### Core Design Principles

1. **Gateway-centric**: All external interaction goes through the gateway. Pods are internal workers, never directly exposed.
2. **Pre-warm everything possible**: Pods are warm before requests arrive. Workspace setup is the only hot-path work.
3. **Pod-local workspace, gateway-owned state**: Pod disk is a cache. Session truth lives in durable stores.
4. **MCP for interaction, custom protocol for infrastructure**: Use MCP where its semantics matter (tasks, elicitation, auth, delegation). Use a custom protocol for lifecycle plumbing.
5. **Recursive delegation as a platform primitive**: Any pod can delegate to other pods through gateway-mediated tools. The gateway enforces scope, budget, and lineage.
6. **Least privilege by default**: No broad credentials in pods. No shared mounts. Gateway-mediated file delivery only.

---

## 2. Goals and Non-Goals

### Goals

- Run agent runtimes on demand in Kubernetes with low startup latency
- Support full SDK-like interactive sessions (streaming, interrupts, follow-up prompts, tool use)
- Support multiple runtime binaries via a standard runtime adapter contract
- Enable recursive orchestration: any pod can delegate to other pods
- Make gateway failure survivable and pod failure recoverable (bounded resume)
- Enforce least-privilege security: no standing credentials in pods, gateway-mediated file delivery
- Scale the gateway horizontally with externalized session state
- Support deployer-selectable isolation profiles (runc, gVisor, Kata)
- Provide rate limiting, token budgets, concurrency controls, and audit logging

### Non-Goals

- Shared RWX storage mounts across agent pods
- Git-based or object-store-based workspace population by pods
- Mid-session file uploads as a default (supported as opt-in capability per runtime)
- Arbitrary late-bound volume mounts on warm pods
- Live migration of in-flight agent processes
- KubeVirt/full VM workloads
- Direct pod-to-pod communication (all delegation goes through gateway)
- Making every internal edge speak MCP

---

## 3. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Client / MCP Host                        │
└──────────────────────────────┬──────────────────────────────────┘
                               │ MCP / OpenAI / Open Responses
                               │ (via ExternalAdapterRegistry)
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Gateway Edge Replicas                        │
│  ┌──────────┐ ┌─────────────┐ ┌───────────┐ ┌───────────────┐  │
│  │  Auth /   │ │   Policy    │ │  Session   │ │  MCP Fabric   │  │
│  │  OIDC     │ │   Engine +  │ │  Router    │ │  (tasks,      │  │
│  │           │ │  Intercep-  │ │            │ │  elicitation,  │  │
│  │           │ │  tors       │ │            │ │  delegation)   │  │
│  └──────────┘ └─────────────┘ └───────────┘ └───────────────┘  │
└────────┬──────────┬──────────────┬──────────────┬───────────────┘
         │          │              │              │
    ┌────▼────┐ ┌───▼────┐ ┌──────▼─────┐ ┌─────▼──────┐
    │Session  │ │Token / │ │  Event /   │ │  Artifact  │
    │Manager  │ │Connec- │ │ Checkpoint │ │   Store    │
    │(Postgres│ │tor Svc │ │   Store    │ │            │
    │+ Redis) │ │        │ │            │ │            │
    └─────────┘ └────────┘ └────────────┘ └────────────┘

         Gateway ←──mTLS──→ Pods (gRPC control protocol)

┌─────────────────────────────────────────────────────────────────┐
│  Warm Pool Controller (pod lifecycle, agent-sandbox CRDs)       │
│  PoolScalingController (scaling intelligence, admin API → CRDs) │
└────────┬───────────────┬────────────────┬───────────────────────┘
         │               │                │
    ┌────▼────┐    ┌─────▼─────┐    ┌─────▼─────┐
    │  Pod A  │    │   Pod B   │    │   Pod C   │
    │┌───────┐│    │┌─────────┐│    │┌─────────┐│
    ││Runtime││    ││ Runtime  ││    ││ Runtime  ││
    ││Adapter││    ││ Adapter  ││    ││ Adapter  ││
    │├───────┤│    │├─────────┤│    │├─────────┤│
    ││Agent  ││    ││  Agent   ││    ││  Agent   ││
    ││Binary ││    ││  Binary  ││    ││  Binary  ││
    │└───────┘│    │└─────────┘│    │└─────────┘│
    └─────────┘    └───────────┘    └───────────┘
```

---

## 4. System Components

### 4.1 Edge Gateway Replicas

**Role:** The only externally-facing component. All client interaction enters through the gateway.

**Responsibilities:**

- Authenticate clients (OIDC/OAuth 2.1)
- Expose multiple external interfaces via `ExternalAdapterRegistry` (MCP, OpenAI Completions, Open Responses — see Section 15)
- Route sessions to the correct runtime pod
- Proxy long-lived interactive streams (WebSocket / gRPC bidi / Streamable HTTP)
- Run the Policy Engine and `RequestInterceptor` chain on every request
- Handle reconnect/reattach after disconnection
- Mediate all file uploads to pods
- Host virtual MCP child interfaces for delegation
- Serve dedicated MCP endpoints for `type: mcp` runtimes at `/mcp/runtimes/{name}`

**Deployment:**

- Stateless-ish replicas behind ingress/load balancer
- HPA on CPU, memory, active sessions, open streams (`active sessions` is sourced from the gateway's in-memory Prometheus gauge `lenny_gateway_active_sessions`, surfaced to the HPA via Prometheus Adapter as described in Section 10.1)
- Sticky routing is an optimization, not a correctness requirement
- PodDisruptionBudget to limit simultaneous disruptions

**Key invariant:** A client can land on any gateway replica. Session state is always in durable stores.

#### Gateway Internal Subsystems

The gateway binary is internally partitioned into three subsystem boundaries. These are **not** separate services — they are Go interfaces within a single binary that enforce isolation at the concurrency and failure-domain level, so that a problem in one area (e.g., a slow upload) cannot starve or crash another (e.g., MCP streaming).

**Subsystems:**

1. **Stream Proxy** — MCP streaming, session attachment, event relay, and client reconnection handling.
2. **Upload Handler** — File upload proxying, payload validation, staging to the Artifact Store, and archive extraction.
3. **MCP Fabric** — Delegation orchestration, virtual child MCP interfaces, and elicitation chain management.

**Per-subsystem isolation guarantees:**

Each subsystem is defined as a Go interface with its own:

- **Goroutine pool / concurrency limits:** A saturated Upload Handler cannot consume goroutines needed by the Stream Proxy. Each subsystem has independently configured `maxConcurrent` and queue-depth settings.
- **Per-subsystem metrics:** Latency histograms, error rates, and queue depth are emitted per subsystem, enabling targeted alerting (e.g., upload p99 latency spike does not hide a stream proxy degradation).
- **Circuit breaker:** Each subsystem has its own circuit breaker (see Section 11.6). The Upload Handler can trip to half-open or open state — returning 503 for uploads — while the Stream Proxy and MCP Fabric continue serving normally. This is the primary mechanism for partial gateway degradation.

**Extraction triggers:**

These internal boundaries are designed so that any subsystem can be extracted to a dedicated service if scaling requires it. The triggers for extraction are:

- Upload throughput requires dedicated HPA scaling independent of the Stream Proxy (e.g., burst upload patterns that would over-provision stream proxy replicas).
- MCP Fabric delegation orchestration becomes a bottleneck requiring its own scaling profile (e.g., deep recursive delegation trees consuming disproportionate resources).

Until those triggers are hit, a single binary with internal boundaries is preferred for operational simplicity.

### 4.2 Session Manager

**Role:** Source of truth for all session and task metadata.

**Backed by:** Postgres (primary), Redis (hot routing cache, short-lived locks)

**Manages:**

- Session records (id, **tenant_id**, user_id, state, pool, pod assignment, cwd, generation)
- Task records and parent/child lineage (task DAG)
- Retry counters and policy enforcement
- Resume eligibility and window
- Pod-to-session binding
- Delegation lease tracking

**Multi-tenancy:** `tenant_id` is carried on all session, task, quota, and token store records. The **primary** tenant isolation mechanism is PostgreSQL Row-Level Security (RLS) policies tied to the database session role. Every tenant-scoped table has an RLS policy that filters rows using `current_setting('app.current_tenant')`. At the start of each transaction, the gateway connection executes `SET app.current_tenant = '<tenant_id>'`, ensuring the database enforces tenant boundaries independently of application code. Application-layer `tenant_id` filtering in queries remains as defense-in-depth, but a missing WHERE clause in application code **cannot** break isolation — the database enforces it regardless. Namespace-level or cluster-level isolation is a future goal. All quotas, rate limits, and usage reports can be scoped by tenant.

**Single-tenant default:** For single-tenant deployments, `tenant_id` defaults to a built-in value (`default`). The field is always present internally — RLS policies and queries always filter by it — but single-tenant deployers never need to set or think about it; the gateway automatically applies the default tenant. Multi-tenant mode is enabled by configuring OIDC claims that carry tenant identity, or by setting `tenant_id` explicitly in the API. No additional configuration is required for single-tenant use.

### 4.3 Connector / Token Service

**Role:** Manages two categories of credentials:

1. **MCP tool tokens** — OAuth tokens for external tools (GitHub, Jira, etc.) used via the MCP fabric
2. **LLM provider credentials** — API keys, cloud IAM roles, and service accounts that runtimes need to access their backing LLM (see Section 4.9 for the full credential leasing design)

**Deployment:** Runs as a **separate process** (Deployment) with its own ServiceAccount and KMS access. This is the only component with KMS decrypt permissions for downstream OAuth tokens. Gateway replicas call the Token Service over mTLS — they cannot directly decrypt stored tokens.

**Key rules:**

- Pods never hold downstream OAuth tokens — the Token Service does
- Refresh tokens stored encrypted at rest (envelope encryption via KMS)
- Access tokens short-lived, cached in Redis (encrypted, not plaintext)
- Scoped by user + connector + tenant + environment
- Kubernetes Secrets used only for bootstrap/internal credentials, not per-user OAuth tokens
- Gateway replicas request tokens via the Token Service API; they receive short-lived access tokens, never refresh tokens or KMS keys
- Each gateway replica has a distinct mTLS identity so compromise of one is attributable and revocable independently

**High availability and failure handling:**

The Token/Connector Service is deployed as a **multi-replica Deployment (2+ replicas)** with a `PodDisruptionBudget` (`minAvailable: 1`) to survive voluntary disruptions. The service is fully stateless — all persistent state lives in Postgres (encrypted tokens) and KMS, so any replica can handle any request with no affinity requirements.

The gateway wraps all Token Service calls in a **circuit breaker** (see Section 11.6). If the Token Service becomes unavailable:

- Sessions that already hold credential leases **continue operating** — leases are self-contained and do not require the Token Service until renewal.
- New sessions that require LLM or OAuth credentials **cannot start** and fail with a retryable error, allowing clients to back off and retry.
- Sessions that do not need LLM credentials (e.g., file-processing-only runtimes) are **unaffected**.

Additionally, the gateway **caches active credential leases in memory**. Token Service unavailability does not affect already-leased credentials until the lease expires, providing a grace period proportional to the lease TTL.

### 4.4 Event / Checkpoint Store

**Role:** Enables session recovery and observability.

**Stores:**

- Event cursors / stream offsets
- Session logs and runtime stderr
- Workspace checkpoint references
- Agent session file snapshots
- Resume metadata

**Checkpoint Atomicity:** A checkpoint is an atomic unit comprising a workspace snapshot (tar of `/workspace/current`), a session file snapshot (copy of `/sessions/` contents), and a checkpoint metadata record (generation, timestamp, pod state). The adapter pauses the agent process (`SIGSTOP`) before starting the checkpoint to ensure a consistent point-in-time snapshot; the agent is resumed (`SIGCONT`) after both snapshots are captured. If either snapshot fails, the entire checkpoint is discarded — partial checkpoints are never stored. The metadata record in Postgres references both artifacts and is written only after both are successfully uploaded to MinIO.

### 4.5 Artifact Store

**Role:** Durable storage for workspace files and exports.

**Stores:**

- Original uploaded workspace files (the canonical "initial workspace")
- Sealed workspace bundles
- Exported file subsets for delegation
- Runtime checkpoints
- Large logs and artifacts

**Implementation:** MinIO (S3-compatible). Local disk for development mode. **Never** Postgres for blob storage — the TOAST overhead and vacuum pressure degrade transactional workload performance. See Section 12.5 for retention policy.

**Workspace lineage:** Each workspace snapshot is immutable and identified by a content-addressed hash (SHA-256 of the tar archive). The session record tracks lineage via a `parent_workspace_ref` field that links to the workspace snapshot that seeded the session (from uploads or a derived session per Section 7.1). This enables lineage queries such as "which sessions were derived from this workspace?" and "what was the workspace history for this session?" Full workspace versioning (tracking incremental changes within a session) is not supported — snapshots are full workspace captures at checkpoint or seal time.

### 4.6 Pod Lifecycle Controllers

Pod lifecycle management is split into two controllers with distinct responsibilities.

#### 4.6.1 Warm Pool Controller (Pod Lifecycle)

**Role:** Manages individual pod lifecycle, state transitions, and health.

**Responsibilities:**

- Maintain warm pods per pool (between `minWarm` and `maxWarm`)
- Manage pod state transitions via CRD status subresource
- Surface readiness status to gateway
- Claim/release/drain lifecycle
- Garbage-collect orphaned pods via owner references
- Handle node drain gracefully (checkpoint active sessions before eviction)
- Track certificate expiry on idle pods; proactively replace (drain and recreate) any idle pod whose certificate will expire within 30 minutes, preventing a claimed pod from having insufficient cert validity for a full session (see Section 10.3 for cert TTLs)

**Implementation:** Built on **`kubernetes-sigs/agent-sandbox`** (launched at KubeCon Atlanta, November 2025). The upstream project solves the same Kubernetes-native pod lifecycle, warm pool, and claim management problem. Building on it means Lenny stops maintaining a Kubernetes controller for pod lifecycle and gets upstream community maintenance, Pod Snapshots on GKE (which directly helps with checkpointing), and a claim model designed by the Kubernetes SIG Apps community. Lenny's gateway, runtime adapter protocol, credential leasing, delegation, and MCP fabric remain entirely Lenny's own — agent-sandbox has no opinion on any of those layers.

**CRD mapping from agent-sandbox:**

| Agent-Sandbox CRD | Replaces (old Lenny CRD) | Purpose |
|---|---|---|
| `SandboxTemplate` | `AgentPool` | Declares a pool: runtime, isolation profile, resource class, warm count range, scaling policy, `executionMode` |
| `SandboxWarmPool` | (part of WarmPoolController) | Upstream warm pool management with configurable `minWarm`/`maxWarm` |
| `Sandbox` | `AgentPod` | Represents a managed agent pod. Status subresource carries the authoritative state machine. Enables GC, structured claim semantics, and warm-pool PDB targeting. |
| `SandboxClaim` | `AgentSession` | Represents an active session binding. Created by the gateway only after it has successfully claimed a `Sandbox`. Links the claimed pod to session metadata (deliberately not an `ownerReference`, so the session survives pod deletion and can be reassigned). |

**Pre-commit requirement:** Verify `SandboxClaim` optimistic-locking guarantee matches Lenny's current status PATCH approach before committing.

**Pod claim mechanism:** Gateway replicas claim pods via `SandboxClaim` resources with optimistic locking — exactly one gateway wins; all others receive a conflict and retry with a different idle pod from the pool. This keeps the controller off the claim hot path entirely — pod-to-session binding is resolved at the API-server level with no single-writer bottleneck.

**Leader election:** The controller runs as a Deployment with 2+ replicas using Kubernetes Lease-based leader election (lease duration: 15s, renew deadline: 10s, retry period: 2s). During failover (~15s), existing sessions continue unaffected; only new pod creation and pool scaling pause.

**Scaling:** Pools support `minWarm`, `maxWarm`, and an optional `scalePolicy` with time-of-day schedules or demand-based rules. Low-traffic pools can scale to zero warm pods with documented cold-start latency as fallback.

**Idle cost visibility and scale-to-zero:** The metric `lenny_warmpool_idle_pod_minutes` (counter, labeled by pool and resource class) tracks cumulative idle pod-minutes, letting deployers estimate warm pool cost from their monitoring stack. Pools support `minWarm: 0` for off-hours via time-of-day rules in `scalePolicy`: `scaleToZero: { schedule: "0 22 * * *", resumeAt: "0 6 * * *" }` sets `minWarm: 0` during the window; sessions arriving in zero-warm periods incur cold-start latency. `scaleToZero` disabled by default for `type: mcp` pools (deployer opt-in required). A `WarmPoolIdleCostHigh` warning alert fires when idle pod-minutes exceed a deployer-configured threshold over a 24 h window (see Section 16.5).

**CRD validation:** All CRDs include OpenAPI schema validation with CEL rules to catch common misconfigurations at admission time. Key rules: `minWarm <= maxWarm`, `maxWarm > 0`, valid RuntimeClass reference format, resource class values within the allowed set, and `maxSessionAge > 0`. Malformed specs are rejected by the API server before reaching the controller, preventing reconciliation loops on invalid input. The controller also validates at reconciliation time as defense-in-depth.

**Controller failover and warm pool sizing:** During a leader-election failover (~15s), the controller cannot create new pods or reconcile pool scaling. If the warm pool is undersized, this pause can exhaust available pods. To prevent this, `minWarm` should account for the failover window: `minWarm >= peak_claims_per_second * (failover_seconds + pod_startup_seconds)`. For example, at 2 claims/sec with a 15s failover pause and 10s pod startup time: `minWarm >= 2 * 25 = 50`. During the failover window, the gateway queues incoming pod claim requests for up to `podClaimQueueTimeout` (default: 30s). If no pod becomes available before the timeout, the session creation fails with a retryable error so the client can back off and retry. As an early-warning mechanism, the `WarmPoolLow` alert (Section 16.5) fires when available pods drop below 25% of `minWarm`, giving operators time to investigate before exhaustion occurs.

**API server rate limiting:** The controller uses controller-runtime's default client-side rate limiter (token bucket: 10 QPS, burst 100) for all API server requests. During large pool-scale events (e.g., scaling from 0 to 50 warm pods), pod creation is processed sequentially through the work queue rather than in parallel bursts. The work queue max depth is configurable (default: 500); if the queue exceeds this depth, new reconciliation events are dropped and a `lenny_controller_queue_overflow_total` metric is incremented. These defaults prevent the controller from overwhelming the API server or etcd during scale-up events.

**etcd write pressure at scale:** At 500+ concurrent sessions, CRD status updates can generate significant etcd write pressure. Mitigations: (1) The state machine uses 3 coarse label values instead of 10+ fine-grained labels, reducing label mutation frequency (Section 6.2). (2) Status updates are batched by the controller's work queue — not every state transition is immediately written. (3) The controller's client-side rate limiter (10 QPS) bounds the update rate. (4) For deployments exceeding 1000 concurrent sessions, operators should monitor etcd write latency and consider increasing etcd resources or reducing status update frequency.

**Disruption protection for agent pods:** The primary protection against voluntary disruption (node drains, cluster upgrades) is a **preStop hook** on every agent pod that triggers a checkpoint via the runtime adapter's `Checkpoint` RPC before allowing termination. The pod's `terminationGracePeriodSeconds` is set high enough (default: 120s) to give the checkpoint time to complete and be persisted to object storage. For active pods whose sessions have been checkpointed (or that have no session at all), the session retry mechanism described in Section 7.2 handles resumption on a replacement pod.

The warm pool controller can optionally create a PDB **per `SandboxTemplate`** with `minAvailable` set to the pool's `minWarm` value. This prevents voluntary evictions from draining the warm pool below its configured minimum, protecting warm pod availability rather than individual sessions. The PDB targets only unclaimed (warm) pods via a label selector (`lenny.dev/pod-state: idle`), so it does not interfere with the preStop-based protection on active session pods.

**Sandbox finalizers:** Every `Sandbox` resource carries a finalizer (`lenny.dev/session-cleanup`) to prevent Kubernetes from deleting the pod — and its local workspace — while a session is still active. When a `Sandbox` enters the `Terminating` state, the warm pool controller checks whether any active `SandboxClaim` still references the pod. It removes the finalizer only after confirming one of two conditions: (a) no session references the pod, or (b) the session has been successfully checkpointed and the gateway has been notified so it can resume the session on a replacement pod. If the finalizer is not removed within 5 minutes (pod stuck in `Terminating`), the controller fires a `FinalizerStuck` alert. Operators can then investigate and manually remove the finalizer once they have confirmed the session state is safe. This ensures that node drains, scale-downs, and accidental deletions never silently orphan an active session.

#### 4.6.2 PoolScalingController (Pool Configuration)

**Role:** Manages desired pool configuration, scaling intelligence, and experiment variant pool sizing. Separate from the WarmPoolController which manages individual pod lifecycle.

**Responsibilities:**

- Reconcile pool configuration from Postgres (admin API source of truth) into `SandboxTemplate` and `SandboxWarmPool` CRDs
- Manage scaling decisions: time-of-day schedules, demand-based rules, experiment variant sizing
- Integrate with experiment definitions to automatically size variant pools

**Pluggable `PoolScalingStrategy` interface.** Fully replaceable by deployers.

**Default formula:**
```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor)
```

`safety_factor` defaults to 1.5 for agent-type pools, 2.0 for mcp-type pools.

**Pool phases:** pre-warm, ramp, steady state, wind-down — all automatic.

**CRDs become derived state** reconciled from Postgres by PoolScalingController. The admin API is the source of truth for pool configuration; the controller translates that into Kubernetes CRDs.

### 4.7 Runtime Adapter

**Role:** Standardized bridge between the Lenny platform and any pod binary. The adapter protocol uses a two-part model: multiple focused local MCP servers for tool access, and a separate lifecycle channel for operational signals.

**Contract (internal gRPC/HTTP+mTLS API — gateway ↔ adapter):**

| RPC                  | Description                                                          |
| -------------------- | -------------------------------------------------------------------- |
| `PrepareWorkspace`   | Accept streamed files into staging area                              |
| `FinalizeWorkspace`  | Validate, materialize to `/workspace/current`                        |
| `RunSetup`           | Execute bounded setup commands                                       |
| `StartSession`       | Start the agent runtime with final `cwd` (pod-warm mode)             |
| `ConfigureWorkspace` | Point a pre-connected session at the finalized `cwd` (SDK-warm mode) |
| `Attach`             | Connect client stream to running session                             |
| `Interrupt`          | Interrupt current agent work                                         |
| `Checkpoint`         | Export recoverable session state                                     |
| `ExportPaths`        | Package files for delegation, rebased per export spec (Section 8.8)  |
| `AssignCredentials`  | Push a credential lease to the runtime before session start          |
| `RotateCredentials`  | Push replacement credentials mid-session (fallback/rotation)         |
| `Resume`             | Restore from checkpoint on a replacement pod                         |
| `Terminate`          | Graceful shutdown                                                    |

**Runtime → Gateway events (sent over the gRPC control channel):**

| Event                  | Description                                                              |
| ---------------------- | ------------------------------------------------------------------------ |
| `RATE_LIMITED`         | Current credential is rate-limited; request fallback                     |
| `AUTH_EXPIRED`         | Credential lease expired or was rejected by provider                     |
| `PROVIDER_UNAVAILABLE` | Provider endpoint is unreachable                                         |
| `LEASE_REJECTED`       | Runtime cannot use the assigned credential (incompatible provider, etc.) |

#### Adapter ↔ Runtime Protocol (Intra-Pod)

The adapter communicates with the runtime binary via two mechanisms:

**Part A — Multiple focused local MCP servers** (intra-pod, stdio or Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`. Note: lease extension is handled via the adapter↔gateway gRPC lifecycle, not as an MCP tool (see Section 8.6).
- **One MCP server per authorized connector** — each connector in the session's delegation policy gets its own independent MCP server. No aggregated connector proxy — aggregation is not lossless per MCP spec (capability negotiation is per-server, sampling breaks, tool name collisions, resource URI collisions).

**No workspace MCP server.** Workspace is materialized to `/workspace/current` before the runtime starts. The runtime accesses it via the filesystem directly.

**Part B — Lifecycle channel** — separate stdin/stdout stream pair for operational signals:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    interrupt_request, credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged
```

Optional. Runtimes that don't open it operate in fallback-only mode. Versioned by capability negotiation at the top. Unknown messages silently ignored on both sides.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per task execution.

```json
{
  "platformMcpServer": { "socket": "/run/lenny/platform-mcp.sock" },
  "connectorServers": [
    { "id": "github", "socket": "/run/lenny/connector-github.sock" }
  ],
  "runtimeMcpServers": [],
  "agentInterface": { ... },
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

`runtimeMcpServers` slot reserved from v1 for future use by `type:mcp` runtimes accessible via adapter proxy.

#### Runtime Integration Tiers (agent-type only)

- **Minimum** — stdin/stdout binary protocol only. Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout. This is the floor. Zero Lenny knowledge required.
- **Standard** — minimum plus connects to adapter's platform MCP server and connector servers via the adapter manifest. Uses platform capabilities (delegation, discovery, output parts, elicitation). Standard MCP — no Lenny-specific code.
- **Full** — standard plus opens the lifecycle channel. True session continuity, clean interrupt points, mid-session credential rotation.

#### Startup Sequence for `type: agent` Runtimes

1. Pod created by Kubernetes
2. Adapter opens gRPC connection to gateway (mTLS)
3. Adapter writes placeholder manifest (connector servers unknown yet)
4. Adapter signals READY to gateway — pod enters warm pool
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials` → `StartSession`
6. Adapter writes **final manifest** (connector servers now known from lease)
7. Adapter spawns runtime binary
8. Runtime reads manifest, connects to MCP servers (Standard/Full), opens lifecycle channel (Full)
9. Adapter sends `lifecycle_capabilities` (Full); receives `lifecycle_support`
10. Adapter delivers first `{type: "message"}` on stdin

#### Deployment Model

- **Default: Sidecar container** communicating with the agent binary over a local Unix socket on a shared `emptyDir` volume. `shareProcessNamespace: false`. This minimizes what third-party binary authors need to implement — just a binary that reads/writes on a well-defined socket protocol.
- **Alternative: Embedded** — first-party binaries can embed the adapter directly and expose the same gRPC contract to the gateway.
- Same external contract either way.

**Sidecar vs embedded trade-offs:**

| Aspect                 | Sidecar (default)                           | Embedded                                   |
| ---------------------- | ------------------------------------------- | ------------------------------------------ |
| Complexity for authors | Low — implement stdin/stdout JSON protocol  | High — implement full gRPC contract        |
| Resource overhead      | ~50 MB memory for adapter sidecar           | None (shared process)                      |
| Language support       | Any language (stdin/stdout)                 | Go only (or language with gRPC support)    |
| Isolation              | Process isolation between adapter and agent | Single process, shared memory              |
| Recommended for        | Third-party runtimes, community adapters    | First-party runtimes where latency matters |

> **Note:** Third-party authors should always use the sidecar model. The embedded model is for first-party runtimes where the adapter and agent binary are developed together.

**Health check:** gRPC Health Checking Protocol. The warm pool controller marks a pod as `idle` only after the health check passes.

#### Adapter-Agent Security Boundary

The boundary between the adapter and the agent binary is **untrusted**. A compromised or misbehaving agent binary must not be able to escalate privileges, extract credentials, or manipulate the adapter. The following controls enforce this:

1. **Separate UIDs:** The adapter runs as a different UID than the agent binary (e.g., adapter as UID 1000, agent as UID 1001). Unix socket permissions: mode 0600, owned by the agent binary's UID. Filesystem-level isolation prevents the agent from reading adapter config or memory.

2. **Adapter-initiated protocol:** The adapter is the protocol initiator — it sends messages and tool-call instructions to the agent and receives responses. The agent cannot initiate arbitrary requests to the adapter.

3. **Untrusted agent responses:** The adapter treats all data received from the agent as untrusted input. It validates, sanitizes, and size-limits all responses before forwarding them to the gateway.

4. **No credential material over socket:** LLM credentials (whether direct lease or proxy URL) are injected into the agent process via environment variables at start time, not passed over the Unix socket or MCP servers. The adapter never sends credential material to the agent post-startup.

5. **Agent crash isolation:** If the agent process crashes, the adapter detects it (socket EOF), reports the failure to the gateway, and does not restart the agent. The gateway handles retry at the session level.

6. **Credential-sensitive RPC logging exclusion:** `AssignCredentials` and `RotateCredentials` RPCs carry credential material in their payloads. These RPCs must be excluded from gRPC access log payload capture, OpenTelemetry span attributes, and any request/response logging middleware. Only the RPC name, lease ID, provider type, and success/failure status should be logged — never the `materializedConfig` contents.

7. **MCP server security:** The local MCP servers never expose gateway credentials, mTLS certificates, other sessions, internal Lenny state, or anything about other tenants. The pod sandbox (gVisor/Kata) and network policy are the security boundary, not the protocol. The adapter never advertises the `sampling` MCP capability to the local server.

### 4.8 Gateway Policy Engine

**Role:** Centralized policy evaluation on the request path.

**Physically embedded** in edge gateway replicas (not a separate service). Can be split out later if policy evaluation becomes a scaling or organizational bottleneck.

**Evaluators:**

| Module                      | Scope                                                |
| --------------------------- | ---------------------------------------------------- |
| `AuthEvaluator`             | AuthN/AuthZ, user invalidation                       |
| `QuotaEvaluator`            | Rate limits, token budgets, concurrency limits       |
| `DelegationPolicyEvaluator` | Depth, fan-out, `DelegationPolicy` tag matching, budget inheritance |
| `RetryPolicyEvaluator`      | Retry eligibility, resume window                     |
| `AdmissionController`       | Queue/reject/prioritize, circuit breakers            |

**Backs onto:** SessionStore (sessions, tasks, delegation tree, lineage, retry state), QuotaStore, TokenStore, UserStateStore, RuntimeRegistry

#### `RequestInterceptor` Extension Point

Formalized interceptor interface at gateway phases: `PreAuth`, `PostAuth`, `PreRoute`, `PostRoute`, `PreToolResult`, `PostAgentOutput`.

Built-in interceptors:
- `ExperimentRouter` — active when experiments are defined (see Section 10.7)
- `GuardrailsInterceptor` — disabled by default, no built-in content classification or prompt injection detection logic. Deployers wire in AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, or their own classifier.

External interceptors via gRPC (like Kubernetes admission webhooks).

### 4.9 Credential Leasing Service

**Role:** Supplies runtime pods with the credentials they need to access their backing LLM provider (Anthropic API, AWS Bedrock, Vertex AI, etc.). This is distinct from the MCP tool token flow in Section 4.3 — this is about the runtime's own LLM access, not downstream tool OAuth.

**Design principle:** Runtimes receive **short-lived credential leases**, never long-lived API keys or root credentials. The Token Service owns all durable credential material.

#### Credential Provider

A pluggable interface per LLM provider type. Each provider knows how to mint usable runtime credentials from its source material.

| Provider           | Source Material        | Runtime Receives                                             |
| ------------------ | ---------------------- | ------------------------------------------------------------ |
| `anthropic_direct` | API key                | Short-lived API key or scoped token                          |
| `aws_bedrock`      | IAM role / access keys | Short-lived STS session credentials + region/endpoint config |
| `vertex_ai`        | GCP service account    | Short-lived access token + project/region config             |
| `azure_openai`     | Azure AD / API key     | Short-lived token + endpoint config                          |
| Custom             | Provider-specific      | Provider-specific config bundle                              |

New providers are added by implementing the `CredentialProvider` interface — no gateway changes required.

#### Credential Pool

An admin-managed set of credentials for a given provider. Deployers register pools via configuration:

```yaml
credentialPools:
  - name: claude-direct-prod
    provider: anthropic_direct
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key-1 # K8s Secret reference
      - id: key-2
        secretRef: lenny-system/anthropic-key-2
    assignmentStrategy: least-loaded # least-loaded | round-robin | sticky-until-failure
    maxConcurrentSessions: 10 # per credential
    cooldownOnRateLimit: 60s

  - name: bedrock-us-east-prod
    provider: aws_bedrock
    credentials:
      - id: role-1
        roleArn: arn:aws:iam::123456789:role/lenny-bedrock
        region: us-east-1
    assignmentStrategy: sticky-until-failure
    maxConcurrentSessions: 50
```

#### Credential Lease

A session-scoped assignment from the gateway to a runtime pod:

```json
{
  "leaseId": "cl_abc123",
  "sessionId": "s_xyz789",
  "provider": "anthropic_direct",
  "poolId": "claude-direct-prod",
  "credentialId": "key-1",
  "materializedConfig": {
    "apiKey": "sk-ant-...<short-lived or scoped>",
    "baseUrl": "https://api.anthropic.com"
  },
  "expiresAt": "2026-03-23T15:30:00Z",
  "renewBefore": "2026-03-23T15:25:00Z",
  "fallbackAllowed": true,
  "source": "pool"
}
```

The runtime receives the `materializedConfig` — a provider-specific bundle with everything needed to authenticate. It never receives the pool's root secret or long-lived key.

#### Credential Policy

Attached to a pool or Runtime, controls how credentials are selected and managed:

```yaml
credentialPolicy:
  preferredSource: pool # pool | user | prefer-user-then-pool | prefer-pool-then-user
  allowedProviders:
    - anthropic_direct
    - aws_bedrock
  defaultPool: claude-direct-prod
  fallback:
    enabled: true
    order: [claude-direct-prod, bedrock-us-east-prod]
    cooldownOnRateLimit: 60s
    maxRotationsPerSession: 3
    requiresRuntimeRestart: false # per-provider; overridden by runtime capability
  userCredentialMode: elicitation # elicitation | pre-authorized | disabled
```

#### Three Credential Modes

| Mode                     | How It Works                                                                                                                  | Use Case                                              |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| **Pool (admin-managed)** | Gateway picks a credential from a registered pool using the assignment strategy. Runtime gets a lease.                        | Shared team/org API keys, service accounts            |
| **User-scoped**          | User provides their own credential via MCP elicitation or pre-authorized flow. Token Service stores it. Runtime gets a lease. | "Bring your own API key", user-specific Bedrock roles |
| **Fallback chain**       | Policy determines precedence. E.g., prefer user credential, fall back to pool on failure.                                     | Flexible enterprise deployments                       |

> The `preferredSource` field determines which mode is used. When `fallback.enabled: true`, the gateway automatically tries the next source in the chain on failure. There are effectively three configurations: pool-only (`preferredSource: pool`, no fallback), user-only (`preferredSource: user`, no fallback), or fallback chain (`preferredSource: prefer-user-then-pool` or `prefer-pool-then-user`, with fallback enabled).

#### Fallback Flow

```
1. Runtime reports RATE_LIMITED (or AUTH_EXPIRED, PROVIDER_UNAVAILABLE) to gateway
2. Gateway marks current lease as degraded, records cooldown timestamp
3. Gateway evaluates CredentialPolicy fallback chain:
   a. Same provider, different credential from pool?
   b. Different provider allowed by policy?
   c. User credential available as fallback?
4. Gateway issues replacement CredentialLease
5. Gateway pushes new credentials to runtime via RotateCredentials RPC
6. Runtime performs provider rebind (if hotRotation supported) or session restart/resume
```

#### LLM Reverse Proxy

For API-key-based providers that do not support short-lived token exchange (e.g., providers where the "short-lived" key is really just the long-lived key with a TTL wrapper), the gateway can run a **credential-injecting reverse proxy** so the real API key never enters the pod:

1. Instead of receiving a materialized API key, the pod receives a lease token and a proxy URL (e.g., `http://gateway-internal:8443/llm-proxy/{lease_id}`).
2. The pod sends LLM API requests to the proxy URL using the lease token for authentication.
3. The gateway proxy validates the lease token, injects the real API key into the upstream request headers, and forwards the request to the LLM provider.
4. The real API key never enters the pod's memory or environment.

This is an **optional, per-provider** configuration — deployers choose for each credential pool whether to use direct credential leasing (current model, simpler, lower latency) or proxy mode (higher security, adds one network hop). The two modes can coexist across different pools.

The proxy enforces the same rate limits and budget constraints as direct leasing. When a lease expires or is revoked, the proxy immediately rejects requests — there is no window of exposure where a compromised pod could continue using a stale key.

```yaml
credentialPools:
  - name: claude-direct-prod
    provider: anthropic_direct
    deliveryMode: proxy # proxy | direct (default: direct)
    proxyEndpoint: http://gateway-internal:8443/llm-proxy
    # ... other pool config unchanged
```

**Credential health scoring:** For pooled credentials, the gateway tracks per-credential:

- Recent rate-limit events and cooldown expiry
- Auth failure count
- Concurrent session count
- Spend tracking (if provider reports it)

Assignment strategies use this health data to avoid assigning degraded credentials.

#### Semantic Caching

Optional `CachePolicy` on `CredentialPool` backed by pluggable `SemanticCache` interface:

```yaml
cachePolicy:
  strategy: semantic
  ttl: 300
  similarityThreshold: 0.92
  backend: redis
```

Default Redis-backed implementation. Fully replaceable by deployers. Disabled by default, opt-in per pool.

#### `CredentialRouter` Interface

Pluggable credential pool selection logic. Default: least-loaded/round-robin/sticky-until-failure. Deployers who want cost-aware, latency-based, or intent-based model routing implement the interface. Disabled by default, opt-in per pool.

#### Security Boundaries

- Long-lived credentials (API keys, IAM role ARNs, service account keys) live **only** in the Token Service and Kubernetes Secrets
- Pods receive **materialized short-lived credentials** (scoped tokens, STS sessions) via the `AssignCredentials` RPC
- Leases are **revocable** — on user invalidation or credential compromise, the gateway revokes the lease and the runtime loses access
- Credential material is **never logged** in audit events, transcripts, or agent output — only lease IDs and provider/pool names are logged
- The `env` blocklist (Section 14) rejects keys matching sensitive patterns (e.g., `ANTHROPIC_API_KEY`, `AWS_SECRET_ACCESS_KEY`, `*_SECRET_*`) regardless of whether credential leasing is configured. When leasing is enabled, credentials flow through the leasing system rather than environment variables; when leasing is not configured, the blocklist still prevents accidental credential exposure via env vars

---

## 5. Runtime Registry and Pool Model

### 5.1 Runtime

`Runtime` and `SessionTemplate` are unified into a single **`Runtime`** concept. A Runtime is either **standalone** (has an `image`) or **derived** (has a `baseRuntime` reference). All runtimes are registered via the admin API as static configuration.

Every Runtime has a **`type`** field and an optional **`capabilities`** field:

```yaml
type: agent          # agent (default) | mcp
capabilities:
  interaction: one_shot    # one_shot | multi_turn
  injection:
    supported: true        # default: false
    modes: [immediate, queued]
```

**`type: agent`** — participates in Lenny's task lifecycle. Receives tasks via stdin `{type: "message"}`, has sessions, workspace, delegation, elicitation, multi-turn dialog. Callable via `lenny/delegate_task`.

**`type: mcp`** — hosts an MCP server. Lenny manages pod lifecycle (isolation, credentials, workspace, pool, egress, audit). No task lifecycle. Runtime binary is oblivious to Lenny. No `capabilities` field.

**`capabilities.interaction: multi_turn`** — the runtime supports the `lenny/request_input` → response cycle and multiple `{type: "message"}` deliveries over the lifetime of a task. Multi-turn requires `capabilities.injection.supported: true` — the gateway enforces this at runtime registration. A multi-turn runtime that doesn't accept injections is incoherent.

**`capabilities.interaction: one_shot`** — the runtime consumes the initial `{type: "message"}`, produces exactly one `{type: "response"}` carrying the final result, and the task ends. May use `lenny/request_input` once (for a single clarification). Second call returns a gateway error.

**`capabilities.injection`** declares whether the runtime supports mid-session message delivery. Default: `supported: false`. Gateway rejects injection attempts against unsupported sessions at the API level before they reach the adapter.

**Capabilities are customizable per tenant**, with the platform defaults as described above.

**Labels are required from v1** — primary mechanism for environment `runtimeSelector` and `connectorSelector` matching (see Section 10.6).

#### Standalone Runtime

```yaml
name: langgraph-runtime
image: registry.example.com/langgraph:latest
type: agent
capabilities:
  interaction: one_shot     # one_shot | multi_turn
  injection:
    supported: true
    modes: [immediate, queued]
executionMode: task
isolationProfile: sandboxed
allowedResourceClasses: [small, medium, large]
delegationPolicyRef: orchestrator-policy
supportedProviders:
  - anthropic_direct
  - aws_bedrock
credentialCapabilities:
  hotRotation: true
  requiresRestartOnProviderSwitch: true
limits:
  maxSessionAge: 7200
  maxUploadSize: 500MB
setupCommandPolicy:
  mode: allowlist
  shell: false
  allowlist:
    - npm ci
    - pip install
    - make
    - chmod
  maxCommands: 10
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail          # fail | warn
runtimeOptionsSchema:
  type: object
  properties:
    model: { type: string }
    temperature: { type: number, minimum: 0, maximum: 2 }
  additionalProperties: false
defaultPoolConfig:
  warmCount: 5
  resourceClass: medium
  egressProfile: restricted
labels:
  team: platform
  approved: "true"
```

#### Derived Runtime

A derived runtime references a `baseRuntime` and customizes workspace, setup, agent interface, and policy — but cannot override security-critical fields from the base.

```yaml
name: research-pipeline
baseRuntime: langgraph-runtime
workspaceDefaults:
  files:
    - path: agent.py
      content: "..."
  setupCommands:
    - pip install -r requirements.txt
setupPolicy:
  timeoutSeconds: 300
  onTimeout: fail
agentInterface:
  description: "Analyzes codebases and produces refactoring plans"
  inputModes:
    - type: "text/plain"
    - type: "application/json"
  outputModes:
    - type: "text/plain"
      role: "primary"
  supportsWorkspaceFiles: true
  skills:
    - id: "review"
      name: "Code Review"
      description: "Reviews code for quality and correctness"
  examples:
    - description: "Review auth module"
      input: "Review the authentication module"
delegationPolicyRef: research-policy
publishedMetadata:
  - key: agent-card
    contentType: application/json
    visibility: public    # internal | tenant | public
    value: '...'
taskPolicy:
  cleanupCommands:
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn
labels:
  team: research
  approved: "true"
```

#### Inheritance Rules

**Never overridable on derived runtime:** `type`, `executionMode`, `isolationProfile`, `capabilities.interaction`, `allowedResourceClasses`, `allowStandardIsolation` acknowledgment.

**Independently configurable on derived runtime:** Pool settings, `workspaceDefaults`, `setupCommands`, `setupPolicy.timeoutSeconds` (gateway takes maximum of base and derived), `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, `taskPolicy`.

**Base runtime mutability:** `image` and `name` immutable via API. All other fields mutable with impact validation — changes that would invalidate existing derived runtimes are rejected with a list of affected runtimes.

#### Derived Runtime Instantiation

Registered via admin API as static configuration, not instantiated per-session. `workspaceDefaults` is the workspace plan the gateway materializes into every pod. Small files inline in `workspaceDefaults`, large files via MinIO reference. Session creation clients upload additional files on top of derived defaults. Workspace materialization order: base defaults → derived defaults → client uploads → file exports from parent delegation.

Derived runtimes have **fully independent pool settings**. Constraint: resource classes cannot exceed base runtime's configured classes. If no pool registered for a derived runtime, gateway falls back to base runtime's pool.

#### Setup Commands and Policy

Setup commands run after workspace materialization and before runtime starts. While executing, pod in INIT state, not READY. Pod failure during setup causes pod replacement before warm pool entry. Setup commands run once per pod, not per task. Per-task setup belongs in the runtime's initialization.

```yaml
setupPolicy:
  timeoutSeconds: 300      # optional — waits indefinitely if absent
  onTimeout: fail          # fail | warn
```

Gateway takes the **maximum** of base and derived `timeoutSeconds` if both set.

#### `agentInterface` Field

`type: agent` runtimes gain an optional `agentInterface` field serving three purposes: discovery, A2A card auto-generation, and adapter manifest summaries.

`supportsWorkspaceFiles: true` signals that workspace files in TaskSpec will be honored, distinguishing internal runtimes from external agents. `type: mcp` runtimes do not have `agentInterface`.

#### `publishedMetadata` Field

Generic metadata publication mechanism on `Runtime`, replacing any named protocol-specific fields (e.g., no dedicated `agentCard` field).

**Visibility levels:**
- **`internal`** — served at `GET /internal/runtimes/{name}/meta/{key}`, requires valid Lenny session JWT. Only reachable from inside the cluster.
- **`tenant`** — same as internal but additionally filtered by `tenant_id` claim in the JWT. An agent in tenant A cannot discover tenant B's agents.
- **`public`** — served at `GET /v1/runtimes/{name}/meta/{key}`, no auth required. A2A cards meant for cross-organization discovery live here.

Not-found and not-authorized produce identical responses — no enumeration. Gateway treats content as **opaque pass-through** — stores and serves without parsing or validating. Validation is the runtime author's responsibility.

**Rationale:** Does not encode a bet on A2A's longevity into the schema. Naturally accommodates agent cards, OpenAPI specs, cost manifests, or whatever the ecosystem invents.

#### Capability Inference from MCP `ToolAnnotations`

Gateway reads `tools/list` at connector or `type:mcp` runtime registration and infers capabilities from MCP `ToolAnnotations`. No manual re-annotation required.

| MCP annotation | Inferred capabilities |
|---|---|
| `readOnlyHint: true` | `read` |
| `readOnlyHint: false, destructiveHint: false` | `write` |
| `destructiveHint: true` | `write, delete` |
| `openWorldHint: true` | `network` |
| No annotations | `admin` (conservative default) |
| *(no MCP equivalent)* | `execute`, `admin` — set via `toolCapabilityOverrides` |

Tenant-overridable via `tenantRbacConfig.mcpAnnotationMapping` (see Section 10.6).

#### Minimal Configuration

Most fields above have sensible defaults. The absolute minimum to register a runtime and start handling sessions:

```yaml
# Minimal Lenny configuration — everything else uses sensible defaults
runtimes:
  - name: my-agent
    image: registry.example.com/my-agent:latest
    type: agent
    supportedProviders:
      - anthropic_direct
    labels:
      team: default
credentialPools:
  - name: default
    provider: anthropic_direct
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key
```

This is the minimum configuration for a single-runtime deployment. All other fields (isolation profile, resource class, warm count, delegation, egress profile, etc.) use deployer-safe defaults. See the full Runtime schema above for customization options.

### 5.2 Pool Configuration and Execution Modes

Each pool is a warmable deployment target for one runtime + operational profile.

**Pool dimensions:**

- Runtime name
- Isolation profile (runc / gvisor / kata)
- Resource class (small / medium / large)
- Execution mode
- Upload and setup policy
- Egress/network profile
- Warm count
- Max session age
- Checkpoint cadence

#### Execution Modes

All three execution modes are implemented in v1. Graph mode is removed as a separate concept — graph-aware runtimes are session-mode runtimes that optionally emit trace spans via the observability protocol.

```yaml
executionMode: session | task | concurrent
```

**`session`** — one session per pod. Pod is exclusive to the session for its lifetime. Default mode.

**`task`** — pod reuses across sequential tasks with workspace scrub between tasks. Requires explicit deployer acknowledgment (security: workspace scrub is best-effort, not a security boundary between tenants).

```yaml
taskPolicy:
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn    # warn | fail
```

Lifecycle: task completes → adapter sends `terminate(task_complete)` on lifecycle channel → runtime acknowledges → cleanup commands execute (have access to task state) → Lenny scrub runs → pod available. `setupCommands` run once per pod at start, not per task. Per-task setup belongs in the runtime's initialization.

**`concurrent`** — multiple tasks on a single pod simultaneously. Two sub-variants via `concurrencyStyle`:

```yaml
executionMode: concurrent
concurrencyStyle: stateless    # stateless | workspace
maxConcurrent: 8
```

**`concurrencyStyle: workspace`** — each slot gets its own workspace. Gateway tracks per-slot lifecycle. Task delivery via `slotId` multiplexing over stdin — the adapter assigns a `slotId` per slot; the runtime implements a dispatch loop keyed on `slotId`; all binary protocol messages (inbound and outbound) carry `slotId` in this mode. Cross-slot isolation is process-level and filesystem-level — explicitly weaker than session mode. Deployer acknowledgment required.

**`concurrencyStyle: stateless`** — no workspace materialization. Gateway routes through Kubernetes Service. Pod readiness probe reflects slot availability. PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

**Truly stateless runtimes** with no workspace and no expensive shared state should be registered as external connectors, not Lenny-managed pods.

`executionMode` is declared on the `Runtime` from v1 (and on the corresponding `SandboxTemplate`).

#### Pool Taxonomy

**Example pools:**

- `claude-worker-sandboxed-small`
- `claude-orchestrator-microvm-medium`

**Pool taxonomy strategy:** Not every runtime × isolation × resource combination needs a warm pool. Use a tiered approach:

- **Hot pools** (minWarm > 0): High-traffic combinations that need instant availability
- **Cold pools** (minWarm = 0, maxWarm > 0): Valid combinations that create pods on demand with documented cold-start latency
- **Disallowed combinations**: Invalid or insecure combinations rejected at pool definition time

This prevents the combinatorial explosion of 3 runtimes × 3 isolation × 3 resource = 27 pools each holding idle pods. In practice, most deployments need 3-5 hot pools.

**Topology spread constraints:** Agent pods use `topologySpreadConstraints` to distribute across availability zones and nodes. The default applied by the PoolScalingController:

- `maxSkew: 1`, `topologyKey: topology.kubernetes.io/zone`, `whenUnsatisfiable: ScheduleAnyway` (soft spread across zones)
- `maxSkew: 1`, `topologyKey: kubernetes.io/hostname`, `whenUnsatisfiable: ScheduleAnyway` (soft spread across nodes)

Deployers can override these defaults per pool via the `SandboxTemplate` CRD's `topologySpreadConstraints` field. For pools where zone balance is critical (e.g., high-availability orchestrator pools), deployers should set `whenUnsatisfiable: DoNotSchedule` to enforce strict spread.

### 5.3 Isolation Profiles

Lenny uses standard Kubernetes `RuntimeClass` for isolation:

| Profile     | RuntimeClass | Use Case                                                                                             | Default? |
| ----------- | ------------ | ---------------------------------------------------------------------------------------------------- | -------- |
| `standard`  | `runc`       | Development/testing only — requires explicit deployer opt-in with security acknowledgment            | No       |
| `sandboxed` | `gvisor`     | **Default for all workloads**. Kernel-level isolation prevents container escape via kernel exploits. | **Yes**  |
| `microvm`   | `kata`       | Higher-risk, semi-trusted, or multi-tenant workloads                                                 | No       |

**Security note:** `runc` provides no protection against kernel exploits. Even trusted developers can introduce malicious dependencies. `gvisor` is the minimum recommended isolation for any workload processing untrusted input (which includes all LLM-generated code execution). Deployers must explicitly opt in to `runc` via a pool configuration flag (`allowStandardIsolation: true`).

Each `RuntimeClass` should define `Pod Overhead` so scheduling accounts for the isolation cost. Reference overhead values:

| Profile              | CPU Overhead | Memory Overhead | Notes                            |
| -------------------- | ------------ | --------------- | -------------------------------- |
| `standard` (runc)    | None         | None            | Native container runtime         |
| `sandboxed` (gVisor) | ~200m        | ~200Mi          | gVisor userspace kernel overhead |
| `microvm` (Kata)     | ~500m        | ~500Mi          | VM boot + guest kernel overhead  |

> These are reference values; actual overhead depends on workload and should be tuned per deployment.

A `RuntimeProvider` abstraction keeps the door open for future backends (e.g., KubeVirt).

**Image supply chain controls:**

- Images **must** be pinned by digest (not tag) in Runtime definitions
- Image signature verification via cosign/Sigstore, enforced by a ValidatingAdmissionWebhook (or OPA/Gatekeeper policy). The cosign admission webhook must be configured as **fail-closed** (`failurePolicy: Fail`). If the webhook is unavailable, pod admission is blocked. This prevents unsigned images from being admitted during webhook outages. Alert on webhook unavailability (`CosignWebhookUnavailable`).
- Only images from deployer-configured trusted registries are admitted
- Vulnerability scanning integrated into CI for all runtime images

**Image provenance verification (signing, attestation) is a prerequisite for any production or staging deployment.** While full hardening is Phase 14 in the build sequence, deployers must not run untrusted agent images without provenance controls. At minimum, images should be pulled from a private registry with digest-based references (not mutable tags) starting from Phase 3 (when the warm pool controller begins creating pods).

**RuntimeClass validation and dev fallback:**

1. **Controller startup validation.** The warm pool controller validates that the required `RuntimeClass` objects exist in the cluster at startup. If a pool references a `RuntimeClass` that doesn't exist (e.g., `gvisor` on a cluster without gVisor installed), the controller logs an error and sets the pool's status to `Degraded` with a clear message: "RuntimeClass 'gvisor' not found — install gVisor or change the pool's isolation profile."
2. **Helm pre-install hook.** The Helm chart includes a pre-install validation Job that checks for required RuntimeClasses and warns if they're missing.
3. **Dev mode fallback.** When `global.devMode: true` in the Helm chart (or `LENNY_DEV_MODE=true`), the default isolation profile falls back to `standard` (runc) so developers can run locally without installing gVisor. A warning is logged: "Dev mode: using runc isolation. Do not use in production."
4. **gVisor installation guidance.** For production clusters, install gVisor via the GKE Sandbox (GKE), or the gVisor containerd-shim (`runsc`) on self-managed clusters. See gVisor documentation for installation instructions.

---

## 6. Warm Pod Model

### 6.1 What a Pre-Warmed Pod Looks Like

A warm pod is either **pod-warm** (default) or **SDK-warm** (optional, per runtime capability):

- Pod scheduled and running
- Selected `RuntimeClass` active (runc/gVisor/Kata)
- Runtime adapter listening and health-checked
- Agent binary dependencies installed and loaded
- `/workspace/current` exists but is empty
- `/workspace/staging` exists for upload staging
- `/sessions` directory present for session files
- Projected service account token mounted (audience: deployment-specific, see Section 10.3)
- No LLM provider credentials assigned (credential lease is assigned at claim time, not warm time)
- No user session bound
- No client files present
- Marked "idle and claimable" via readiness gate

**Pod-warm (default):** The agent process is NOT started. Because workspace contents (including `CLAUDE.md`, `.claude/*`) are unknown until request time, the session must start after workspace finalization. This is the safest and most general mode.

**SDK-warm (optional):** The agent process IS pre-connected and waiting for its first prompt. See below for constraints.

**Session mode security invariant: pods are one-session-only.** After a session completes or fails in `executionMode: session`, the pod is terminated and replaced — never recycled for a different session. This prevents cross-session data leakage through residual workspace files, session transcripts, cached DNS, or runtime memory. **Task mode** (`executionMode: task`) relaxes this invariant with explicit deployer acknowledgment — pods are reused across sequential tasks with workspace scrub between tasks (see Section 5.2). **Concurrent mode** allows multiple simultaneous tasks on a single pod (see Section 5.2).

**Optional: SDK-warm mode.** Runtimes that declare `capabilities.preConnect: true` can pre-connect their agent process during the warm phase (before workspace finalization) without sending a prompt. The warm pool controller starts the SDK process after the pod reaches `idle` state, leaving it waiting for its first prompt. This eliminates SDK cold-start latency from the hot path. **Constraint:** SDK-warm mode is only safe when the request does not inject project config files that must be present at session start. The SDK-warm eligibility decision is based on an explicit `sdkWarmEligible` predicate defined in the `Runtime`, not ad-hoc workspace plan parsing. Each `Runtime` declares a `sdkWarmBlockingPaths` list (default: `["CLAUDE.md", ".claude/*"]`) — if the workspace plan includes files matching any of these glob patterns, the gateway selects a pod-warm pod instead of SDK-warm. This makes the decision deterministic, configurable per runtime, and independent of workspace plan parsing logic.

### 6.2 Pod State Machine

```
Pod-warm path:
  warming ──→ idle ──→ claimed ──→ receiving_uploads ──→ finalizing_workspace
                                                                │
                                                                ▼
                           attached ←── starting_session ←── running_setup

SDK-warm path (preConnect: true):
  warming ──→ sdk_connecting ──→ idle ──→ claimed ──→ receiving_uploads
                                                           │
                                                           ▼
                           attached ←── finalizing_workspace ──→ running_setup

Session state transitions (from attached):
                            attached
                            │
                    ┌───────┼───────────────┬────────────────┐
                    ▼       ▼               ▼                ▼
               completed   failed    resume_pending     suspended
                                         │                   │
                                    ┌────┤              ┌────┤
                                    ▼    ▼              ▼    ▼
                               resuming  awaiting    running  completed
                                  │       _client     (resume)
                                  ▼       _action
                               attached     │
                                            ▼
                                      retry_exhausted / expired
                                            │
                                            ▼
                                          draining
```

**`suspended` state:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (interrupt_request + interrupt_acknowledged)
suspended → running   (resume_session — no new content)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed (terminate)
```

Pod held, workspace preserved, `maxSessionAge` timer paused while suspended. `interrupt_request` is a standalone lifecycle signal — pause-and-decide with decoupled timing. Distinct from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**`interrupt_request` does NOT cascade** to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children.

**State storage:** The authoritative state machine lives in the `Sandbox` CRD `.status.phase` and `.status.conditions` fields, backed by Postgres via the controller. **Pod labels are used only for coarse operational states** needed by selectors and monitoring:

| Label               | Values                       | Purpose                                                       |
| ------------------- | ---------------------------- | ------------------------------------------------------------- |
| `lenny.dev/state`   | `idle`, `active`, `draining` | Coarse state for kubectl, monitoring, NetworkPolicy selectors |
| `lenny.dev/pool`    | pool name                    | Pool membership                                               |
| `lenny.dev/runtime` | runtime name                 | Runtime type                                                  |

This avoids the 8-10 label mutations per session that would stress the API server at scale. Detailed state transitions (e.g., `receiving_uploads` → `finalizing_workspace`) are tracked in the CRD status subresource only.

### 6.3 Startup Latency Analysis

**Saved by pre-warming (removed from hot path):**

- Pod scheduling and container creation
- Image pull
- Runtime sandbox initialization
- Volume mounting
- Runtime adapter/binary boot
- Health/readiness checks

**Still on hot path (pod-warm):**

- Pod claim and routing (~ms)
- File upload and workspace materialization (depends on payload size)
- Setup commands (depends on commands)
- Agent session start (depends on runtime)
- First prompt / first token

**Still on hot path (SDK-warm):**

- Pod claim and routing (~ms)
- File upload and workspace materialization (depends on payload size)
- Setup commands (depends on commands)
- First prompt / first token (session start is already done)

**Estimated latency savings:**

- runc with cached image: ~1–3s
- gVisor: ~2–5s
- Kata: ~3–8s
- Cold image pulls: +5–30s avoided

### 6.4 Pod Filesystem Layout

```
/workspace/
  current/      # Agent's actual cwd — populated during workspace finalization
  staging/      # Upload staging area — files land here first
/sessions/      # Session files (e.g., conversation logs, runtime state)        [tmpfs]
/artifacts/     # Logs, outputs, checkpoints
/tmp/           # tmpfs writable area                        [tmpfs]
```

**Data-at-rest protection:**

- `/sessions/` and `/tmp/` use `emptyDir.medium: Memory` (tmpfs) — data is guaranteed gone when the pod terminates. tmpfs usage counts against pod memory limits and must be accounted for in resource requests. Resource class definitions (Section 5.2) must account for tmpfs usage in memory requests. For example, if a pod's memory limit is 2Gi and tmpfs usage can reach 500Mi, the effective memory available to the agent process is 1.5Gi. Deployers should set `emptyDir.sizeLimit` on tmpfs volumes to cap usage and provide predictable OOM boundaries rather than silent memory pressure. Recommended size limits: `/sessions/` sizeLimit of 256Mi (session transcripts), `/tmp/` sizeLimit of 256Mi (scratch space).
- `/workspace/` and `/artifacts/` use disk-backed emptyDir. Node-level disk encryption (LUKS/dm-crypt or cloud-provider encrypted volumes) is **required** for production deployments.
- `/dev/shm` is limited to 64MB. `procfs` and `sysfs` are masked/read-only. `shareProcessNamespace: false` when using sidecar containers. `shareProcessNamespace: false` is not enforceable via Pod Security Standards; a Kyverno or Gatekeeper policy must be deployed to reject pods in agent namespaces that set `shareProcessNamespace: true`, as part of the RuntimeClass-aware admission policies described in Section 17.2.
- Combined with the one-session-only invariant, sensitive data never persists on disk after pod termination.

---

## 7. Session Lifecycle

### 7.1 Normal Flow

```
1. Client → Gateway:     CreateSession(runtime, pool, retryPolicy, metadata)
2. Gateway:              Authenticate, authorize, evaluate policy
3. Gateway:              Select pool, claim idle warm pod
4. Gateway → Store:      Persist session metadata (session_id, pod, state)
5. Gateway:              Evaluate CredentialPolicy → assign CredentialLease from pool or user source
6. Gateway → Pod:        AssignCredentials(lease) — push materialized provider config to runtime
7. Gateway → Client:     Return session_id + upload token

8. Client → Gateway:     UploadWorkspaceContent(files, archives)
9. Gateway → Pod:        Stream files over mTLS into /workspace/staging

10. Client → Gateway:    FinalizeWorkspace()
11. Gateway → Pod:       Validate staging, materialize to /workspace/current
12. Pod:                 Run setup commands (bounded, logged)

13. Gateway → Pod:       StartSession(cwd=/workspace/current, options)
                         (SDK-warm pods: skip this step — session already connected,
                          send ConfigureWorkspace to point it at finalized cwd)
14. Pod:                 Start agent binary/runtime session (or resume pre-connected one)

15. Client → Gateway:    AttachSession(session_id)
16. Gateway ↔ Pod:       Bidirectional stream proxy
17. Client ↔ Gateway:    Full interactive session (prompts, responses, tool use,
                         interrupts, elicitation, credential rotation on RATE_LIMITED)

18. Session completes or client disconnects
19. Gateway → Pod:       Seal workspace — export final workspace snapshot to Artifact Store
20. Gateway → Pod:       Terminate
21. Gateway → Store:     Mark session completed, persist final state, record artifact refs
22. Gateway:             Release credential lease back to pool
23. Warm Pool:           Release pod to draining → eventual cleanup
```

**Artifact retention:** Session artifacts (workspace snapshots, logs, transcripts) are retained for a configurable TTL (default: 7 days, deployer-configurable). A background GC job deletes expired artifacts. Clients can extend retention on specific sessions via `extend_artifact_retention(session_id, ttl)`.

**Transcript as downloadable artifact:** The session transcript (conversation history) is available via `GET /v1/sessions/{id}/transcript` and is included as a downloadable session artifact. When deriving a new session (see `POST /v1/sessions/{id}/derive`), clients can optionally include the previous session's transcript as a file in the derived session's workspace, giving the new agent context from the prior conversation.

**Seal-and-export invariant:** The workspace is always exported to durable storage before the pod is released. If export fails, the pod is held in `draining` state with a retry. This ensures session output is never lost due to pod cleanup.

```

```

### 7.2 Interactive Session Model

Once a session is attached, the client interacts via an **MCP Task** with bidirectional streaming over Streamable HTTP (SSE for server→client, POST for client→server). All content delivery uses the `MessageEnvelope` format (see Section 15.4.1).

**Client → Gateway (external API):**

| Endpoint / Message                                 | Description                          |
| -------------------------------------------------- | ------------------------------------ |
| `POST /v1/sessions/{id}/messages`                  | Send a message (unified endpoint for all content delivery). Gateway rejects injection against sessions whose runtime has `injection.supported: false`. |
| `POST /v1/sessions/{id}/interrupt`                 | Interrupt current agent work (lifecycle signal, not content delivery) |
| `approve_tool_use(tool_call_id)`                   | Approve a pending tool call          |
| `deny_tool_use(tool_call_id, reason?)`             | Deny a pending tool call             |
| `respond_to_elicitation(elicitation_id, response)` | Answer an elicitation request        |

**Gateway → Client (streaming events):**

| Event                                          | Description                                       |
| ---------------------------------------------- | ------------------------------------------------- |
| `agent_output(parts: OutputPart[])`            | Streaming output from the agent (replaces `agent_text`) |
| `tool_use_requested(tool_call_id, tool, args)` | Agent wants to call a tool (if approval required) |
| `tool_result(tool_call_id, result)`            | Result of a tool call                             |
| `elicitation_request(elicitation_id, schema)`  | Agent/tool needs user input                       |
| `status_change(state)`                         | Session state transition (including `suspended`)  |
| `error(code, message, transient?)`             | Error with classification                         |
| `session_complete(result)`                     | Session finished, result available                |

**Session state machine:**

```
running → suspended   (interrupt_request + interrupt_acknowledged)
suspended → running   (resume_session — no new content)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed (terminate)
```

**Gateway-mediated inter-session messaging:** All inter-session communication flows through the gateway. Platform MCP tools available to runtimes:

- `lenny/send_message(to, message)` — send a message to any task by ID
- `lenny/request_input(parts)` → `MessageEnvelope` — blocks until answer arrives
- `lenny/get_task_tree()` → `TaskTreeNode` — returns task hierarchy with states
- `lenny/send_to_child(task_id, message)` — deliver a message to a child session (active in v1)

**Message delivery routing — three paths:**
1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves blocked tool call directly. No stdin delivery, no interrupt.
2. **No matching pending request, runtime available** → `{type: "message"}` to stdin at next read.
3. **No matching pending request, runtime blocked in `await_children`** → buffered in inbox; delivered before the next `await_children` event.

**Reconnect semantics:** The gateway persists an event cursor per session. On reconnect, the client provides its last-seen cursor and the gateway replays missed events from the EventStore. Events older than the checkpoint window may not be replayable; in that case the client receives a `checkpoint_boundary` marker and the current session state.

**SSE buffer policy:** The gateway maintains a per-client event buffer (default: 1000 events or 10MB, whichever is smaller). If a slow client falls behind and the buffer fills, the gateway drops the connection and the client must reconnect with its last-seen cursor. Events beyond the buffer are replayed from the EventStore on reconnect (if within the checkpoint window). This prevents a single slow client from causing unbounded memory growth in the gateway.

### 7.3 Retry and Resume

**Retry policy** is set per session by the client, bounded by deployer caps:

```json
{
  "retryPolicy": {
    "mode": "auto_then_client",
    "maxRetries": 2,
    "retryableFailures": ["pod_evicted", "node_lost", "runtime_crash"],
    "nonRetryableFailures": [
      "workspace_validation_failed",
      "setup_command_failed"
    ],
    "maxSessionAgeSeconds": 7200,
    "maxResumeWindowSeconds": 900
  }
}
```

**Session generations:** Each recovery creates a new generation of the same logical session. The client always sees one session_id.

**Resume flow after pod failure:**

1. Gateway detects session failure
2. Classify failure (retryable vs. non-retryable)
3. If retryable and `retryCount < maxRetries`:
   a. Allocate new warm pod
   b. Recreate same absolute `cwd` path
   c. Replay latest workspace checkpoint
   d. Restore session file to expected path
   e. Resume session (native SDK resume or fresh session with carried state)
4. If retries exhausted → state becomes `awaiting_client_action`

**Client actions after retry exhaustion:**

- Resume anyway (explicit override)
- Start fresh session from latest checkpoint
- Download artifacts / logs / transcript
- Terminate session
- Fork into a new session

**`awaiting_client_action` semantics:**

- **Expiry:** Sessions in `awaiting_client_action` expire after `maxResumeWindowSeconds` (default 900s). After expiry the session transitions to `expired` and artifacts are retained per the standard retention policy.
- **Children behavior:** Active children continue running when the parent enters `awaiting_client_action`. Their results are stored in the task tree. When the parent resumes, pending child results are delivered.
- **CI / automated discovery:** Automated clients can poll `GET /v1/sessions/{id}` and check for `state: awaiting_client_action`. The webhook system (Section 14) also fires a `session.awaiting_action` event so CI systems can react without polling.

### 7.4 Upload Safety

All uploads are gateway-mediated. **Pre-start uploads** are the default. **Mid-session uploads** are supported as an opt-in capability.

**Mid-session uploads:** If the runtime declares `capabilities.midSessionUpload: true` and the deployer policy allows it, clients can call `upload_to_session(session_id, files)` during an active session. Mid-session uploads use the same staging→validation→promotion pattern as pre-start uploads. Files are first written to `/workspace/staging`, validated (path traversal protection, size limits, hash verification), then atomically moved to `/workspace/current`. The runtime adapter receives a `FilesUpdated` notification only after promotion, so the agent never sees partially-written files.

**Enforcement rules:**

- All paths relative to workspace root
- Reject `..`, absolute paths, path traversal
- Reject symlinks, hard links, device files, FIFOs, sockets
- Per-file and total session size limits
- Hash verification:
  - **Optional** for client uploads (the client may not have pre-computed hashes)
  - **Mandatory** for delegation file exports (the gateway computes and verifies hashes during the export-to-child flow to ensure no tampering between parent export and child delivery)
- Write to staging first, promote only after validation
- Archive extraction is especially strict:
  - **Supported formats:** `tar.gz`, `tar.bz2`, `zip`. Other formats are rejected.
  - **Zip-slip protection:** Every extracted path is validated to resolve within the staging directory after canonicalization. Paths containing `..` components or absolute paths are rejected. Symlinks pointing outside the staging directory are rejected.
  - **Symlink handling:** Symlinks within archives are rejected by default. A `allowSymlinks: true` option can be set per Runtime for runtimes that require them, but even then symlinks must resolve within the workspace root.
  - **Atomic cleanup:** If extraction fails at any point (invalid path, size limit, format error), all already-extracted files are removed from staging before the error is returned. The staging directory is returned to its pre-extraction state.
  - **Size limits:** Total extracted size is checked against the per-session upload limit. Extraction aborts immediately if the limit is exceeded (no "extract then check").
- Upload channel closes after workspace finalization

> Clients can discover whether a runtime supports mid-session uploads by checking the `midSessionUpload` capability in the `GET /v1/runtimes` response before session creation.

### 7.5 Setup Commands

Run after workspace finalization, before session start.

**Constraints:**

- Time-bounded (configurable timeout per command and total)
- Resource-bounded
- Fully logged (stdout/stderr captured)
- Network **blocked by default** during setup (static NetworkPolicy; no dynamic toggling which would require NET_ADMIN)
- Max commands per session enforced (`setupCommandPolicy.maxCommands`)

**Security model:** The true security boundary for setup commands is the pod's isolation profile (gVisor/Kata), filesystem read-only root, non-root UID, network policy, and the ephemeral nature of the pod. Setup commands run inside the sandbox — even a malicious setup command is constrained by the pod's security context. The command policy modes below are defense-in-depth layers, not the primary security boundary.

**Command policy:** The gateway validates every setup command against the Runtime's `setupCommandPolicy` before forwarding to the pod:

| Mode        | Behavior                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `allowlist` | **Recommended default for multi-tenant deployments.** Only commands matching an explicitly listed prefix are permitted. Everything else is rejected. This is the strongest policy mode because it denies by default.                                                                                                                                                                                                             |
| `blocklist` | Commands matching any blocked prefix are rejected. Everything else is allowed. The blocklist prevents common mistakes (e.g., accidentally running `rm -rf /`). It is **not a security boundary** — a determined attacker with shell access can bypass any blocklist (e.g., `c\url`, backtick substitution, hex escapes). Suitable for single-tenant or trusted-deployer scenarios where the sandbox already limits blast radius. |

Matching is by **command prefix** — e.g., a blocklist entry `curl` blocks `curl`, `curl -s http://...`, etc. The gateway rejects invalid commands before they reach the pod, and the rejection reason is included in the session's setup output.

**Shell-free execution (`shell: false`):** When enabled in the `setupCommandPolicy`, setup commands are executed directly via `exec` (not via a shell interpreter). Commands are split by whitespace and passed as an argv array. This prevents shell metacharacter injection — backtick substitution, pipes, redirects, glob expansion, and variable interpolation are all inert. This is the most restrictive execution mode and is recommended alongside `allowlist` for multi-tenant deployments. When `shell: false` is set, commands that depend on shell features (pipes, redirects, `&&` chaining) will fail and must be refactored into scripts or individual commands.

---

## 8. Recursive Delegation

### 8.1 Design Philosophy

Recursive delegation is a **platform primitive**, not a hardcoded orchestration pattern. The gateway provides the foundational operations; the pod binary decides whether and how to use them.

Every pod runs the same orchestration-capable runtime. Whether it acts as a pure worker, a delegating orchestrator, or both is determined by the agent binary.

### 8.2 Delegation Mechanism

When a parent pod wants to delegate, it calls the single `lenny/delegate_task` tool on the platform MCP server:

```
lenny/delegate_task(
  target: string,
  task: TaskSpec,
  lease_slice?: LeaseSlice
) → TaskHandle
```

Target id is **opaque** — the runtime does not know whether the target is a standalone runtime, derived runtime, or external registered agent. No separate `external_delegate` tool.

`TaskSpec`:
```json
{
  "input": ["OutputPart[]"],
  "workspaceFiles": {
    "export": [{ "glob": "src/auth/**", "destPrefix": "/" }]
  }
}
```

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`.

**Delegation flow:**

1. Parent calls `lenny/delegate_task(target, task, lease_slice?)`
2. Gateway validates against parent's effective delegation policy and lease (depth, fan-out, budget)
3. Gateway asks parent runtime to export files matching the export spec (see Section 8.8)
4. Gateway stores exported files durably (rebased to child workspace root)
5. Gateway allocates child pod from specified pool
6. Gateway streams rebased files into child before it starts
7. Child starts with its own local workspace containing the exported files
8. Gateway creates a **virtual MCP child interface** and injects it into parent
9. Parent interacts with child through this virtual interface

**What the parent sees:** A gateway-hosted virtual MCP server with:

- Task status/result
- Elicitation forwarding
- Cancellation
- Message delivery via `lenny/send_to_child`

**What the parent never sees:** Pod addresses, internal endpoints, raw credentials.

**Virtual child interface lifecycle:**

- **Storage:** Virtual child interfaces live in gateway per-session memory. On parent pod failure, the gateway reconstructs them from the task tree in SessionStore (which records all child session IDs, states, and pending results).
- **Pending elicitations:** If a parent pod fails while an elicitation is pending from a child, the gateway holds that elicitation. When the parent resumes on a new pod, the gateway replays it via the re-injected virtual child interface (see the `children_reattached` event in Section 8.11).
- **Replay on resume:** The gateway re-injects all active virtual child interfaces on parent resume. Each interface carries the child's current state (running, completed, failed, `input_required`) and any pending results or elicitations. The parent agent receives a `children_reattached` event with this state.

### 8.3 Delegation Policy and Lease

#### `DelegationPolicy` as First-Class Resource

`allowedRuntimes`, `allowedConnectors`, and `allowedPools` fields are replaced by named `DelegationPolicy` resources with tag-based matching evaluated at delegation time:

```yaml
name: orchestrator-policy
rules:
  - target:
      matchLabels:
        team: platform
      types: [agent]
    allow: true
  - target:
      ids: [github, jira]
      types: [connector]
    allow: true
```

**Two policy levels:**
- **Runtime-level policy** (deployment time, tag rules) — set via `delegationPolicyRef` on the Runtime
- **Derived runtime policy** (post-deployment, can only restrict) — set via `delegationPolicyRef` on the derived runtime

**Effective policy = `base_policy ∩ derived_policy`** — derived runtime policy can only restrict.

**Dynamic tag evaluation at delegation time.** Tags can change without redeploying — policy re-evaluated on each delegation.

**Session-level override with `maxDelegationPolicy`** on the delegation lease.

**Discovery scoping:** `lenny/discover_agents` returns only targets authorized by the calling session's effective delegation policy. Returns `type: agent` runtimes and external agents only — `type: mcp` runtimes do not appear.

#### Delegation Lease

Every delegating session carries a **delegation lease** that defines its quantitative authority:

```json
{
  "maxDepth": 3,
  "maxChildrenTotal": 10,
  "maxParallelChildren": 3,
  "maxTreeSize": 50,
  "maxTokenBudget": 500000,
  "delegationPolicyRef": "orchestrator-policy",
  "maxDelegationPolicy": null,
  "minIsolationProfile": "sandboxed",
  "perChildRetryBudget": 1,
  "perChildMaxAge": 3600,
  "fileExportLimits": { "maxFiles": 100, "maxTotalSize": "100MB" },
  "approvalMode": "policy",
  "cascadeOnFailure": "cancel_all",
  "credentialPropagation": "independent",
  "allowedExternalEndpoints": []
}
```

Child leases are always **strictly narrower** than parent leases (depth decremented, budgets reduced).

**`allowedExternalEndpoints`** slot exists from v1 for future A2A support — controls which external agent endpoints can be delegated to.

**Isolation monotonicity:** Children must use an isolation profile **at least as restrictive** as their parent. The enforcement order is: `standard` (runc) < `sandboxed` (gVisor) < `microvm` (Kata). A `sandboxed` parent cannot delegate to a `standard` child. The `minIsolationProfile` field in the lease enforces this, and the gateway validates it before approving any delegation.

**Tree-wide limits:** `maxTreeSize` caps the total number of pods across the entire task tree (all depths), preventing exponential fan-out. `maxTokenBudget` caps total LLM token consumption across the tree.

**Credential propagation:** Controls how child sessions get LLM provider credentials:

| Mode          | Behavior                                                                                                      |
| ------------- | ------------------------------------------------------------------------------------------------------------- |
| `inherit`     | Child uses the same credential pool/source as parent (gateway assigns from same pool)                         |
| `independent` | Child gets its own credential lease based on its own Runtime's default policy                                  |
| `deny`        | Child receives no LLM credentials (for runtimes that don't need LLM access, e.g., pure file-processing tools) |

**Delegation Presets:** To reduce configuration burden, deployers can define named delegation presets:

```yaml
delegationPresets:
  simple: # Single-level delegation, no fan-out
    maxDepth: 1
    maxChildrenTotal: 3
    maxParallelChildren: 1
    maxTokenBudget: 100000
  standard: # Multi-level, moderate fan-out
    maxDepth: 3
    maxChildrenTotal: 10
    maxParallelChildren: 3
    maxTokenBudget: 500000
  orchestrator: # Deep trees, high fan-out
    maxDepth: 5
    maxChildrenTotal: 50
    maxParallelChildren: 10
    maxTokenBudget: 2000000
```

Clients reference presets by name in the WorkspacePlan: `"delegationLease": "standard"`. Presets can be partially overridden with inline fields: `"delegationLease": {"preset": "standard", "maxDepth": 2}`. If no delegation lease is specified, the Runtime's default applies.

### 8.4 Approval Modes

| Mode       | Behavior                                                                  |
| ---------- | ------------------------------------------------------------------------- |
| `policy`   | Gateway auto-approves if request matches lease constraints                |
| `approval` | Gateway pauses parent, surfaces delegation request to client for approval |
| `deny`     | Delegation not permitted                                                  |

### 8.5 Delegation Tools

Available on the platform MCP server for every delegation-capable pod:

| Tool                                              | Purpose                                                              |
| ------------------------------------------------- | -------------------------------------------------------------------- |
| `lenny/delegate_task(target, task, lease_slice?)`  | Spawn a child session (target is opaque — runtime, derived runtime, or external agent) |
| `lenny/await_children(child_ids, mode)`            | Wait for multiple children (`all`, `any`, or `settled`). Streaming response — unblocks on `input_required`. |
| `lenny/cancel_child(child_id)`                     | Cancel a child (cascades to its descendants per policy)              |
| `lenny/discover_agents(filter?)`                   | List available delegation targets, filtered by effective delegation policy |
| `lenny/send_to_child(task_id, message)`            | Deliver a message to a child session (active in v1) |
| `lenny/send_message(to, message)`                  | Send a message to any task by taskId |
| `lenny/request_input(parts)`                       | Block until answer arrives (replaces stdout `input_required`) |
| `lenny/get_task_tree()`                            | Return task hierarchy with states |

### 8.6 Lease Extension

Lease extension is part of the **adapter↔gateway gRPC lifecycle**, not the platform MCP server. The runtime never calls it and is never aware it happened.

**Trigger:** When the LLM proxy rejects a call for budget exhaustion, the adapter automatically requests a lease extension from the gateway via the gRPC control channel. This avoids the chicken-and-egg problem where a runtime would need LLM tokens to reason about requesting more tokens. On success, the adapter retries the failed LLM call transparently — the runtime sees a slightly slow LLM response, not a failure.

**Request:**

```json
{
  "extensions": {
    "additionalChildren": 5,
    "additionalTokenBudget": 200000,
    "additionalMaxAge": 1800
  }
}
```

**Extendable fields:** `maxChildrenTotal`, `maxTokenBudget`, `maxTreeSize`, `perChildMaxAge`, `fileExportLimits`. Not extendable: `maxDepth`, `minIsolationProfile`, `delegationPolicyRef` (these are security boundaries, not resource budgets).

**Hard ceilings — extensions can never exceed:**

1. **Effective max** — `min(deployment max, tenant max if set)`. See configuration layering below.
2. **The parent's own lease limits** — a child requesting an extension cannot exceed what the parent was granted

#### Configuration Layering

Lease extension settings are resolved by **specificity**: more specific levels override less specific, in either direction (more permissive or more restrictive).

**Resolution order for `extensionApproval` and `coolOffSeconds`:**

1. Start with **deployment default** (Helm)
2. **Tenant** overrides if set (via admin API)
3. **Runtime** overrides if set (via admin API)

**Resolution order for `maxExtendableBudget`:**

1. Start with **deployment default** (Helm)
2. **Tenant** overrides if set
3. **Runtime** overrides if set
4. Result is capped by **tenant max** if one exists
5. Result is capped by **deployment max** (absolute ceiling — can never be exceeded, even if tenant max is higher)

**Configuration at each level:**

```yaml
# Deployment level (Helm) — system-wide defaults and absolute ceiling
leaseExtension:
  defaults:
    extensionApproval: elicitation
    coolOffSeconds: 5
    maxExtendableBudget: 500000
  max:
    maxExtendableBudget: 2000000      # absolute ceiling, never exceeded
```

```yaml
# Tenant level (admin API) — per-tenant overrides and optional ceiling
leaseExtension:
  extensionApproval: auto             # overrides deployment default
  maxExtendableBudget: 1000000        # overrides deployment default
  max:
    maxExtendableBudget: 1500000      # tenant ceiling, capped by deployment max
```

```yaml
# Runtime level (admin API) — per-runtime overrides
leaseExtension:
  extensionApproval: elicitation      # overrides tenant setting
  coolOffSeconds: 10                  # overrides deployment default
  maxExtendableBudget: 800000         # overrides tenant setting
```

**Example resolutions with deployment default 500K, deployment max 2M, tenant max 1M:**

| Runtime sets | Tenant sets | Effective `maxExtendableBudget` | Why |
|---|---|---|---|
| 800K | 300K | 800K | Runtime overrides tenant; under both ceilings |
| 1.5M | — | 1M | Runtime overrides deployment default; capped by tenant max |
| — | 300K | 300K | Tenant overrides deployment default |
| — | — | 500K | Deployment default |
| 2.5M | — | 1M | Capped by tenant max (which is < deployment max) |

#### Approval Modes

**`auto` mode:** Each request is handled independently. The gateway grants the requested amount up to the effective max and returns success immediately. No elicitation, no queuing, no cool-off.

**`elicitation` mode (default):** Requests are serialized per task tree. The gateway presents at most one elicitation to the user at a time, with concurrent request batching and a cool-off window after approval.

**Elicitation mode flow:**

1. **First request** in a tree triggers a generic elicitation to the client: *"The agent needs more budget to continue. Approve?"* No specific token amounts are shown.
2. **Concurrent requests** arriving while the elicitation is pending are queued silently. No duplicate elicitation is sent.
3. **User approves:**
   - The gateway grants each queued request its individually requested amount, adding those tokens to the tree budget (capped at the effective max).
   - A **cool-off window** starts.
   - New requests arriving during the cool-off period are auto-granted their requested amounts with no elicitation.
   - If granting a request's full amount would exceed the effective max, the gateway caps the grant to whatever headroom remains.
   - **All requests tied to a single elicitation + cool-off period return success**, even if their grant was capped or reduced to zero because the ceiling was reached. Success means "your request was processed as part of an approved batch," not "you received exactly what you asked for."
   - After the cool-off window expires, the next request starts a new elicitation cycle (back to step 1).
4. **User rejects:**
   - All queued requests are rejected.
   - The tree is marked as **extension-denied** — all future extension requests from any session in that tree are auto-rejected for the remainder of the tree's lifetime.

**Scope:**

- Extensions apply to the requesting session only
- Existing children are **unaffected** — their leases remain as originally granted
- Only new children spawned after the extension benefit from the expanded parent budget

**Audit:** Every extension request is logged with: requesting session, requested amounts, approval mode, outcome (approved/denied/capped), approver (gateway-auto or client), granted amount, effective max at time of request, resulting new limits, batch id (groups requests tied to the same elicitation + cool-off period), gateway_replica_id, client_ip.

### 8.8 File Export Model

When a parent delegates to a child, it specifies which files to export and how they should appear in the child's workspace.

**Export spec (part of `delegate_task`):**

```json
{
  "fileExport": {
    "source": "./exports/export1/*",
    "destPrefix": "input/"
  }
}
```

**Rebasing rule:** The source glob's base path is stripped, and matched files are placed at the child's workspace root (or under `destPrefix` if specified). The child always sees a clean root-relative structure.

**Examples:**

| Parent workspace               | Source glob           | destPrefix     | Child sees              |
| ------------------------------ | --------------------- | -------------- | ----------------------- |
| `./exports/export1/foo.ts`     | `./exports/export1/*` | _(none)_       | `./foo.ts`              |
| `./exports/export1/lib/bar.ts` | `./exports/export1/*` | _(none)_       | `./lib/bar.ts`          |
| `./exports/export1/foo.ts`     | `./exports/export1/*` | `input/`       | `./input/foo.ts`        |
| `./src/auth.ts`                | `./src/*`             | `project/src/` | `./project/src/auth.ts` |
| `./results.json`               | `./results.json`      | _(none)_       | `./results.json`        |

This means the parent controls what slice of its workspace becomes the child's world. The child has no visibility into the parent's broader directory structure.

**Multiple exports:** A `delegate_task` can include multiple export entries. They are applied in order; if paths overlap, later entries overwrite earlier ones.

```json
{
  "fileExport": [
    { "source": "./src/*", "destPrefix": "src/" },
    { "source": "./config/child-config.json", "destPrefix": "" }
  ]
}
```

**Validation:**

- Source glob resolution must not follow symlinks outside `/workspace/current`. The gateway resolves each matched path to its real path (`realpath`) and rejects any file whose resolved path is outside the workspace root. This prevents an agent from creating a symlink (e.g., `./data → /etc/passwd`) that would be included in the export.
- Source globs are resolved inside the parent's `/workspace/current` only — no traversal outside the workspace
- `destPrefix` must be a relative path, no `..`, no absolute paths
- Total exported size is checked against `fileExportLimits` in the delegation lease
- File count is checked against `fileExportLimits.maxFiles`
- If multiple exports or `destPrefix` settings cause file overwrites in the child workspace, the gateway logs a warning with the overwritten paths and the export entry that caused it. This is audited in the session's delegation audit trail.

### 8.9 TaskRecord and TaskResult Schema

#### TaskRecord

Task records use a messages array forward-compatible with multi-turn dialog:

```json
{
  "taskId": "task_abc123",
  "sessionId": "sess_xyz",
  "state": "running",
  "messages": [
    { "role": "caller", "parts": ["OutputPart[]"] },
    { "role": "agent",  "parts": ["OutputPart[]"], "state": "completed" }
  ],
  "usage": { ... },
  "treeUsage": { ... }
}
```

**Task state machine — `input_required` is reachable in v1:**
```
submitted → running → completed
                    → failed
                    → input_required   (reachable via lenny/request_input)
```

#### TaskResult

Returned by `lenny/await_children`:

```json
{
  "taskId": "child_abc123",
  "status": "completed",
  "output": {
    "parts": ["OutputPart[]"],
    "artifactRefs": ["artifact://session_xyz/workspace.tar.gz"]
  },
  "usage": {
    "inputTokens": 15000,
    "outputTokens": 8000,
    "wallClockSeconds": 120,
    "podMinutes": 2.1,
    "credentialLeaseMinutes": 1.8
  },
  "treeUsage": {
    "inputTokens": 45000,
    "outputTokens": 22000,
    "wallClockSeconds": 450,
    "podMinutes": 12.5,
    "credentialLeaseMinutes": 10.2,
    "totalTasks": 4
  },
  "error": null
}
```

`treeUsage` is populated by the gateway from the task tree and is only available after all descendants have settled. It contains the sum of this task's usage plus all descendant tasks. For in-progress tasks or tasks with unsettled descendants, `treeUsage` will be `null`.

On failure:

```json
{
  "taskId": "child_abc123",
  "status": "failed",
  "output": null,
  "usage": {
    "inputTokens": 5000,
    "outputTokens": 1000,
    "wallClockSeconds": 30,
    "podMinutes": 0.5,
    "credentialLeaseMinutes": 0.0
  },
  "error": {
    "code": "RUNTIME_CRASH",
    "category": "TRANSIENT",
    "message": "Agent process exited with code 137",
    "retriesExhausted": true
  }
}
```

**`lenny/await_children` modes and behavior:**

- `all` — wait until all children complete or fail. Returns list of `TaskResult`.
- `any` — return as soon as any child completes. Returns the first `TaskResult`. **Remaining children continue running** — they are not auto-cancelled. The parent can explicitly cancel them via `lenny/cancel_child` if desired.
- `settled` — wait until all children reach a terminal state (completed, failed, or cancelled). Returns list of `TaskResult`.

**`lenny/await_children` unblocks on `input_required`:** When a child enters `input_required` state, the parent's `lenny/await_children` call yields a partial result carrying the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response — it yields partial events before the final settled result. The parent can respond via `lenny/send_message` with `inReplyTo: "req_001"`, which resolves the child's blocked `lenny/request_input` tool call directly. The parent then re-awaits.

### 8.10 Task Tree

The gateway maintains a complete task DAG:

```
root_task (client → pod A)
├── child_task_1 (pod A → pod B)
│   └── grandchild_task_1 (pod B → pod C)
└── child_task_2 (pod A → pod D)
```

Each node tracks: session_id, generation, pod, state, lease, budget consumed, failure history.

### 8.11 Delegation Tree Recovery

The gateway tracks the full task tree **independently of pods** in the SessionStore. This enables recovery when any node in the tree fails.

**Parent pod failure with active children:**

1. Gateway detects parent failure
2. Children continue running (they are independent pods with their own sessions)
3. If parent resumes on a new pod:
   a. Gateway re-injects virtual MCP child interfaces for all still-active children
   b. Parent session receives a `children_reattached` event listing current child states
   c. Parent can continue awaiting, canceling, or interacting with children
4. If parent exhausts retries (terminal failure):
   a. Gateway applies the parent's `cascadeOnFailure` policy (see below)

**Cascading behavior (configurable per delegation lease):**

| Policy             | Behavior                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------- |
| `cancel_all`       | Cancel all descendants immediately                                                                              |
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results                               |
| `detach`           | Children become orphaned; results are stored but no parent collects them. Client can query via `get_task_tree`. |

Default: `cancel_all`.

**Child failure notification:**

When a child fails, the gateway injects a `child_failed` event into the parent's session stream with:

- `child_task_id`
- failure classification (transient/permanent)
- error details
- whether retries were exhausted

The parent agent can then decide to: re-spawn a replacement, continue with partial results, or propagate the failure upward.

**Orphan cleanup:** A background job detects task tree nodes whose root session has been terminated and whose `cascadeTimeoutSeconds` has expired. Orphaned children are terminated and their artifacts follow standard retention policy.

---

## 9. MCP Integration

### 9.1 Where MCP Is Used

| Boundary                         | Protocol                | Why                                              |
| -------------------------------- | ----------------------- | ------------------------------------------------ |
| Client ↔ Gateway                 | MCP (Streamable HTTP) via `ExternalAdapterRegistry` | Tasks, elicitation, auth discovery, tool surface. Also OpenAI Completions, Open Responses, and other external adapters. |
| Adapter ↔ Runtime (intra-pod)    | MCP (local Unix socket servers) | Platform tools, per-connector tool servers. See Section 4.7. |
| Parent pod ↔ child (via gateway) | MCP (virtual interface) | Delegation, tasks, elicitation forwarding        |
| Gateway ↔ external MCP tools     | MCP                     | Tool invocation, OAuth flows                     |
| Gateway ↔ `type:mcp` runtimes   | MCP (dedicated endpoints at `/mcp/runtimes/{name}`) | Direct MCP server access. Implicit session for audit/billing. |
| Gateway ↔ pod runtime control    | Custom gRPC/HTTP+mTLS   | Lifecycle, uploads, checkpoints, lease extension — not MCP-like   |

#### Platform MCP Server Tools

The platform MCP server (available to `type: agent` runtimes via the adapter manifest) exposes:

| Tool                          | Purpose |
|-------------------------------|---------|
| `lenny/delegate_task`         | Spawn a child session (target is opaque) |
| `lenny/await_children`        | Wait for children (streaming, unblocks on `input_required`) |
| `lenny/cancel_child`          | Cancel a child and its descendants |
| `lenny/discover_agents`       | List available delegation targets (policy-scoped) |
| `lenny/output`                | Emit output parts to the parent/client |
| `lenny/request_elicitation`   | Request human input via the elicitation chain |
| `lenny/memory_write`          | Write to the memory store (see Section 9.4) |
| `lenny/memory_query`          | Query the memory store |
| `lenny/request_input`         | Block until answer arrives (replaces stdout `input_required`) |
| `lenny/send_message`          | Send a message to any task by taskId |
| `lenny/send_to_child`         | Deliver a message to a child session |
| `lenny/get_task_tree`         | Return task hierarchy with states |

#### Runtime Discovery

Every external interface exposes runtime discovery via `HandleDiscovery`. All results are identity-filtered and policy-scoped. Not-found and not-authorized produce identical responses — no enumeration.

- **MCP:** `list_runtimes` tool
- **REST:** `GET /v1/runtimes` with full `agentInterface` and `mcpEndpoint` fields
- **OpenAI Completions:** `GET /v1/models`
- **Open Responses:** `GET /v1/models`

### 9.2 Elicitation Chain

MCP requires hop-by-hop elicitation — servers elicit from their direct client, never skipping levels:

```
External Tool → (elicitation) → Gateway connector
Gateway connector → (elicitation) → Child pod (via virtual MCP)
Child pod → (elicitation) → Parent pod (via virtual MCP)
Parent pod → (elicitation) → Gateway edge
Gateway edge → (elicitation) → Client / Human
```

Response flows back down the same chain. The gateway mediates every hop but **does not erase the hop structure**.

**Elicitation provenance:** The gateway tags every elicitation with metadata before forwarding it up the chain:

| Field              | Description                                                             |
| ------------------ | ----------------------------------------------------------------------- |
| `origin_pod`       | Which pod initiated the elicitation                                     |
| `delegation_depth` | How deep in the task tree                                               |
| `origin_runtime`   | Runtime type of the originating pod                                     |
| `purpose`          | Stated purpose (e.g., "oauth_login", "user_confirmation")               |
| `connector_id`     | Registered connector ID (for OAuth flows)                               |
| `expected_domain`  | Expected OAuth endpoint domain (for URL-mode elicitations)              |
| `initiator_type`   | `connector` (gateway-registered connector) or `agent` (agent-initiated) |

Client UIs **must** display provenance prominently so users can distinguish platform OAuth flows from agent-initiated prompts.

**URL-mode elicitation security controls:**

1. **Agent-initiated URL-mode blocked by default.** URL-mode elicitations (those containing a URL for the user to visit, e.g., OAuth flows) can only be initiated by gateway-registered connectors. Agent binaries cannot emit URL-mode elicitations unless explicitly allowlisted per-pool. This prevents a compromised agent from phishing users via crafted URLs.
2. **URL domain validation is a hard enforcement boundary.** The gateway rejects any URL-mode elicitation whose URL domain does not match the registered connector's `expected_domain`. This is not a metadata annotation — the elicitation is dropped and an error is returned to the originator. Wildcards and subdomain matching follow the connector's registered domain policy.
3. **Initiator type in provenance.** The provenance metadata includes an `initiator_type` field: `connector` (gateway-initiated via a registered connector) or `agent` (agent-initiated, only if allowlisted). Client UIs should render these with distinct trust indicators — connector-initiated elicitations carry higher trust than agent-initiated ones.

**Depth-based restrictions:** Deployers can configure per-pool or global rules limiting which elicitation types are allowed at each delegation depth (e.g., children below depth 2 cannot trigger OAuth flows).

**Deep elicitation suppression:** At delegation depth >= 3, agent-initiated elicitations are **auto-suppressed by default** unless the elicitation type appears in the pool's allow list. Suppressed elicitations return a `SUPPRESSED` response to the originating pod, which should handle it equivalently to "user declined." Deployers configure this via `elicitationDepthPolicy` per pool:

- `allow_all` — no suppression at any depth
- `suppress_at_depth: N` — suppress agent-initiated elicitations at depth N+
- `block_all` — no elicitations from delegated sessions

OAuth flows initiated by gateway-registered connectors are exempt from suppression at any depth, provided the connector is authorized by the session's effective `DelegationPolicy` (these are gateway-initiated, not agent-initiated).

**Elicitation Timeout Semantics:**

1. **Timer pause:** When a session is waiting for an elicitation response, the session's `maxIdleTime` timer is paused. The session is in a "waiting_for_human" state, not idle.
2. **Elicitation timeout:** A separate `maxElicitationWait` timeout (default: 600s, configurable per pool) limits how long a session waits for a human response. If exceeded, the elicitation is dismissed and the pod receives a timeout error that the agent can handle.
3. **Per-hop forwarding timeout:** Each hop in the elicitation chain has a forwarding timeout (30s). If a hop doesn't forward the elicitation within 30s, the gateway treats it as a failure and returns a timeout to the originating pod.
4. **Dismiss elicitation:** Clients can explicitly dismiss a pending elicitation via a `dismiss_elicitation` action (sends a cancellation response down the chain).
5. **Elicitation budget:** Deployers can configure `maxElicitationsPerSession` (default: 50) to prevent agents from spamming the user with elicitation requests.

### 9.3 Connector Definition and OAuth/OIDC

#### `ConnectorDefinition` as First-Class Resource

Connector configuration is a first-class admin API resource, supporting both tool connectors and external agents:

```yaml
connectors:
  - id: github
    displayName: GitHub
    mcpServerUrl: https://mcp.github.com
    transport: streamable_http
    auth:
      type: oauth2
      authorizationEndpoint: https://github.com/login/oauth/authorize
      tokenEndpoint: https://github.com/login/oauth/access_token
      clientId: "..."
      clientSecretRef: lenny-system/github-client-secret
      scopes: [repo, read:org]
    visibility: tenant
    labels:
      team: platform
```

Includes `labels` map for environment selector matching. Tool capability metadata derived from MCP `ToolAnnotations` at registration time (see Section 5.1 — Capability Inference).

All connectors must be registered before they can be used — unregistered external MCP servers cannot be called from inside a pod (security: gateway must know about every external endpoint for OAuth flow, audit logging).

Each connector in a session's effective delegation policy gets its own independent MCP server in the adapter manifest (see Section 4.7). No aggregated connector proxy.

#### OAuth/OIDC Flow

When a nested agent calls an external MCP tool requiring user auth:

1. Pod calls the connector's local MCP server in the adapter manifest
2. Gateway (acting as MCP client to external tool) receives auth challenge
3. Gateway emits URL-mode elicitation through the chain (hop by hop up to client)
4. User completes OAuth flow
5. Gateway connector receives and stores resulting tokens (encrypted, never in pods)
6. Future calls from pods **authorized for that connector** use gateway-held connector state

**Key invariants:**

- Tokens never transit through pods. The gateway owns all downstream credential state.
- **Connector access is scoped per delegation level.** The `DelegationPolicy` (see Section 8.3) controls which connectors each session is authorized to use. The gateway validates the `connector_id` in every external tool call against the calling pod's effective delegation policy before proxying. A child cannot use connectors not permitted by its policy, even if tokens exist for them at the root level.

### 9.4 Memory Store

`MemoryStore` is a role-based storage interface alongside `SessionStore`, `ArtifactStore`, etc.

```go
type MemoryStore interface {
    Write(ctx, scope MemoryScope, memories []Memory) error
    Query(ctx, scope MemoryScope, query string, limit int) ([]Memory, error)
    Delete(ctx, scope MemoryScope, ids []string) error
    List(ctx, scope MemoryScope, filter MemoryFilter) ([]Memory, error)
}

type MemoryScope struct {
    TenantID  string
    UserID    string
    AgentType string   // optional: scope to a runtime type
    SessionID string   // optional: scope to a specific session
}
```

Default: Postgres + pgvector. Fully replaceable. Deployers who want Mem0 or Zep implement the interface backed by their choice. Technology choice explicitly deferred — the memory layer market is not settled as of Q1 2026.

Accessed via `lenny/memory_write` and `lenny/memory_query` tools on platform MCP server. Runtimes that don't need memory ignore these tools entirely.

---

## 10. Gateway Internals

### 10.1 Horizontal Scaling

Gateway replicas are stateless proxies over externalized state:

```
                     ┌──────────────────┐
                     │  Load Balancer   │
                     └────┬────┬────┬───┘
                          │    │    │
                     ┌────▼┐ ┌─▼──┐ ┌▼────┐
                     │ GW  │ │ GW │ │ GW  │
                     │ #1  │ │ #2 │ │ #3  │
                     └──┬──┘ └─┬──┘ └──┬──┘
                        │      │       │
                     ┌──▼──────▼───────▼──┐
                     │   Postgres + Redis  │
                     └────────────────────┘
```

**Correctness rule:** Sticky routing is an optimization. A client can reconnect to any replica and resume.

**Per-session coordination:**

- **Primary:** Redis-based distributed lease (`SET NX` with TTL) ensures only one replica actively coordinates a given session at a time. If that replica dies, another picks up after TTL expiry.
- **Fallback:** If Redis is unavailable, replicas acquire coordination rights using `SELECT ... FOR UPDATE SKIP LOCKED` on the session row in Postgres. This is transaction-scoped (not connection-scoped like advisory locks), so it survives PgBouncer connection recycling without risk of silent lock release.
- **Generation counters:** Each session row carries a `coordination_generation` counter. When a replica takes over coordination (via either mechanism), it increments the generation. Pods validate the generation on every gateway→pod RPC — if the generation is stale, the pod rejects the request and the stale replica discovers it is no longer the coordinator. This prevents split-brain even under lease/lock race conditions.

**Custom metrics pipeline:** Each gateway replica exposes `lenny_gateway_active_streams` (a per-replica gauge of in-flight streaming connections) on its `/metrics` endpoint. Prometheus scrapes these endpoints, and the **Prometheus Adapter** (`k8s-prometheus-adapter`) is configured to surface this metric to the Kubernetes custom metrics API (`custom.metrics.k8s.io/v1beta1`), making it available to the HPA. As an alternative, **KEDA** can be used with a Prometheus scaler trigger targeting the same metric, which simplifies HPA manifest authoring for teams already running KEDA.

**HPA scale-down protection:** Use `behavior.scaleDown.stabilizationWindowSeconds: 300` and `behavior.scaleDown.policies` with `type: Pods, value: 1, periodSeconds: 60` to scale down one pod at a time, preventing mass eviction of gateway replicas during traffic dips.

**preStop hook drain:** The gateway pod spec includes a `preStop` hook that blocks termination while `active_streams > 0`, polling at 1-second intervals up to `terminationGracePeriodSeconds` (recommended 60s). This gives in-flight streams time to complete naturally or allows clients to detect the closing connection and reconnect to another replica via the load balancer. If active streams have not drained by the grace period deadline, the process receives SIGKILL and remaining clients must reconnect. Combined with the one-pod-at-a-time scale-down policy, this ensures that at most one replica is draining at any moment.

### 10.2 Authentication

| Boundary          | Mechanism                                                                  |
| ----------------- | -------------------------------------------------------------------------- |
| Client → Gateway  | OIDC/OAuth 2.1 (MCP-standard protected resource server)                    |
| Automated clients | Service-to-service auth (client credentials grant)                         |
| Gateway ↔ Pod     | mTLS + projected service account token (audience-bound, short TTL)         |
| Pod → Gateway     | Projected service account token (audience: deployment-specific, short TTL) |

**Session capability context:** After authentication, the gateway mints a **signed JWT** via a pluggable `JWTSigner` interface. Two backends are supported:

- **Production:** KMS-backed signing (AWS KMS, GCP Cloud KMS, HashiCorp Vault Transit). The signing key never exists in gateway memory — the gateway sends the JWT claims to the KMS service and receives a signature. This eliminates the risk of key extraction from a compromised gateway process.
- **Dev mode:** Local HMAC-SHA256 key, enabled only when `LENNY_DEV_MODE=true` (see Section 17.4). This backend must never be used in production deployments.

**JWT claims:**

- `session_id`, `user_id`, `tenant_id`
- `delegation_depth`, `allowed_operations`
- `expiry` (short-lived, refreshed by gateway on each interaction)

**Key rotation:** KMS backends support automatic key rotation natively. The JWT `kid` (key ID) header identifies which key version signed the token. During verification, the gateway tries the current and previous key versions, with a configurable overlap window (default 24h). This allows seamless rotation with zero downtime — tokens signed with the old key remain valid until the overlap window closes.

Pods cannot forge or extend this token. The gateway validates the signature on every pod→gateway request.

#### Authorization and RBAC

Authentication alone is not sufficient for multi-tenant deployments. The platform defines a role-based access control model with three built-in roles:

- **`platform-admin`**: Full access to all endpoints across all tenants. Can manage runtimes, pools, global configuration, and platform-wide settings.
- **`tenant-admin`**: Full access scoped to their own tenant. Can manage users, quotas, view usage, set legal holds, and configure callback URLs. Cannot access other tenants' data or platform-wide settings.
- **`user`**: Can create and manage their own sessions. Cannot access other users' sessions (even within the same tenant) unless explicitly granted by a tenant-admin.

**Role assignment:** Roles are conveyed via OIDC claims (e.g., a `lenny_role` claim in the ID token) or via a platform-managed mapping (`user_id` → role stored in Postgres). When both sources are present, the platform-managed mapping takes precedence, allowing tenant-admins to override OIDC-derived roles within their tenant.

**Tenant-scoped admin API:** Admin endpoints (`GET /v1/usage`, `GET /v1/pools`, `GET /v1/metering/events`) are tenant-scoped for `tenant-admin` callers — they only return data belonging to the admin's tenant. `platform-admin` callers see data across all tenants, with an optional `?tenant_id=` filter.

**Future: self-service portal:** A web UI for tenant-admins to manage their configuration (users, quotas, callback URLs, legal holds) is a future goal, built on top of these tenant-scoped APIs.

### 10.3 mTLS PKI

**Certificate authority:** cert-manager with a cluster-internal CA (self-signed issuer or Vault-backed for production). This is the default; a service mesh (Istio/Linkerd) is an optional alternative.

**Certificate lifecycle:**

| Component        | Certificate TTL | SAN Format                                           | Rotation                                                |
| ---------------- | --------------- | ---------------------------------------------------- | ------------------------------------------------------- |
| Gateway replicas | 24h             | DNS: `lenny-gateway.lenny-system.svc`                | cert-manager auto-renewal at 2/3 lifetime               |
| Agent pods       | 4h              | SPIFFE URI: `spiffe://lenny/agent/{pool}/{pod-name}` | cert-manager auto-renewal; pod restart if renewal fails |
| Controller       | 24h             | DNS: `lenny-controller.lenny-system.svc`             | cert-manager auto-renewal                               |

**Pod identity:** Agent pods use SPIFFE-compatible URIs as SANs. The gateway validates the SPIFFE URI against the expected pool/pod on each connection. Each gateway replica gets a distinct certificate so compromise of one replica can be detected and revoked independently.

**Projected SA token:** Configured with `expirationSeconds: 900` (15 minutes). Kubelet auto-refreshes the token before expiry. The gateway validates the audience claim on every pod→gateway request. The audience value **must be deployment-specific** — formatted as `lenny-gateway-<cluster-name>` — to prevent token replay across Lenny deployments sharing the same Kubernetes cluster. This is configurable via Helm value `global.saTokenAudience` (default: `lenny-gateway`). The ServiceAccount bound to agent pods has **zero RBAC bindings** — no Kubernetes API access. The projected SA token is one layer in a defense-in-depth strategy. It must be used alongside mTLS (the gateway validates the pod's SPIFFE certificate) and NetworkPolicy (only gateway pods can reach agent pods). None of these controls is sufficient alone — the SA token prevents token replay across audiences, mTLS prevents impersonation, and NetworkPolicy prevents unauthorized network access.

**Admission-time RBAC enforcement:** A Kyverno or Gatekeeper policy must validate that ServiceAccounts used by agent pods in the `lenny-agents` and `lenny-agents-kata` namespaces have no RoleBindings or ClusterRoleBindings. This prevents accidental RBAC escalation — if a deployer or automation adds a binding to an agent SA, the policy blocks pod admission until the binding is removed. This complements the zero-RBAC-bindings design stated above by shifting enforcement left to admission time rather than relying solely on convention.

**For long-running sessions (up to 7200s):** SA token refresh is handled transparently by kubelet. Certificate TTL (4h) is longer than max session age (2h), so no mid-session certificate rotation is needed under normal conditions.

**cert-manager Failure Modes and CA Rotation:**

1. **Warm pool cert awareness:** The warm pool controller should verify that newly created pods have valid certificates before marking them as `idle`. If cert-manager fails to issue a certificate within 60s of pod creation, the pod is marked as unhealthy and replaced. Additionally, the controller continuously tracks certificate expiry on idle pods and proactively drains any idle pod whose certificate will expire within 30 minutes, replacing it with a fresh pod. This prevents the scenario where a pod idle for most of its 4h cert TTL is claimed with only minutes of validity remaining — insufficient for a session that may run up to 2h (see Section 4.6).
2. **Alerting:** `CertExpiryImminent` alert (referenced in Section 16.5) fires if any certificate is within 1h of expiry — since auto-renewal should happen at 2/3 lifetime, this indicates a cert-manager failure.
3. **cert-manager HA:** cert-manager should run with 2+ replicas and leader election in production. A single cert-manager failure should not prevent certificate renewal across the cluster.
4. **CA rotation procedure:** When rotating the cluster-internal CA (e.g., annually or on compromise):
   - Deploy a new CA certificate alongside the old one (both trusted during overlap period)
   - Issue new certificates signed by the new CA via cert-manager
   - Pods pick up new certificates on next rotation cycle (within 2/3 of their TTL)
   - After all certificates have rotated, remove the old CA from trust bundles
   - Gateway and controller trust bundles must include both CAs during the overlap window

**Certificate revocation:** Short-lived certificates (4h TTL) are the primary mitigation against compromised pods — stolen certs expire quickly. For immediate revocation, the gateway maintains an in-memory **certificate deny list** keyed by SPIFFE URI or certificate serial number. When a pod is terminated for security reasons (e.g., anomalous behavior detected by the controller), the gateway adds its certificate to the deny list and rejects any subsequent mTLS connection presenting that cert. The deny list is propagated across gateway replicas via Redis pub/sub (with Postgres `LISTEN/NOTIFY` as fallback). Entries are ephemeral — each entry expires when the certificate's natural TTL lapses (at most 4h), keeping the list small.

### 10.4 Gateway Reliability

**Design principle:** Gateway pod failure causes a broken stream and reconnect, never session loss.

**Mechanisms:**

- All session truth externalized (Postgres)
- Distributed session lease for active coordination
- App-level reconnect: client reconnects with session_id, gateway looks up state, reattaches
- Multiple replicas across zones/nodes
- PodDisruptionBudget limits voluntary disruptions
- Readiness probes remove unhealthy replicas from traffic
- Rolling updates preserve capacity

### 10.5 Upgrade and Rollback Strategy

**Gateway:** Rolling Deployment updates. Schema migrations use expand-contract pattern (add new columns/tables first, deploy code that writes both old and new, then migrate reads, then drop old). Mixed-version replicas must coexist during rollout.

**Schema Migration Tooling and Runbook:**

- **Tooling:** Use `golang-migrate` (or Atlas) for versioned, file-based migrations. Each migration is a pair of up/down SQL files stored in the repository under `migrations/`.
- **Execution:** Migrations run as a Kubernetes Job (init container or pre-deploy hook) before the gateway Deployment rolls out. The Job acquires a Postgres advisory lock to prevent concurrent migration runs.
- **Expand-contract discipline:**
  - Phase 1 (expand): Add new columns/tables, deploy code that writes to both old and new.
  - Phase 2 (migrate reads): Switch reads to new schema.
  - Phase 3 (contract): Drop old columns/tables in a subsequent release.
  - Each phase is a separate migration file and a separate deployment.
- **Rollback:** Down migrations are always provided but only used as a last resort. The expand-contract pattern means the previous code version is compatible with the current schema, since old columns are not removed until the code no longer reads them.
- **Locking:** `golang-migrate` uses Postgres advisory locks to prevent concurrent migrations. The lock is released on completion or failure.
- **Partial completion:** If a migration fails mid-way, the advisory lock is released and the migration can be re-run. Each migration step should be idempotent where possible.

**Warm Pool Controller:** Rolling update with leader election. During leader failover (~15s), existing sessions are unaffected; only new pod creation and scaling pause.

**CRD schema versioning during rolling deploys:** CRDs follow Kubernetes API versioning conventions (shipping at `v1beta1` initially; see Section 15.5). During a rolling deploy, the gateway and controller may briefly run different versions that expect different CRD schemas. Conversion webhooks translate between CRD versions so both components operate correctly during the transition. CRD specs use `x-kubernetes-preserve-unknown-fields` on extensible sub-objects so that a controller running an older version does not crash on fields introduced by a newer gateway (or vice versa). CRD schema changes follow the same expand-contract discipline as database migrations (above): new fields are added first, both versions write them, then old fields are removed in a subsequent release.

**Runtime adapters and agent binaries:** Versioned pool rotation:

1. Deploy new `SandboxTemplate` CRD with updated image (e.g., `claude-worker-v2-sandboxed-medium`)
2. New warm pods start with new version
3. Old pool's `minWarm` set to 0 — existing pods drain naturally as sessions complete
4. Once old pool is fully drained, remove old `SandboxTemplate` CRD

This avoids in-place image changes and ensures no session is disrupted by an upgrade.

**Rollback and safe rotation rules:**

- **Never delete the old `SandboxTemplate` CRD until the new pool is verified and the old pool is fully drained.** This is the key safety rule.
- **Safe rotation sequence:** (1) Deploy new pool, (2) verify new pool pods pass health checks, (3) route a canary percentage of new sessions to the new pool, (4) only after validation, set old pool's `minWarm` to 0, (5) only after old pool fully drains, delete old `SandboxTemplate` CRD.
- **Rollback procedure:** If the new pool version is broken, recreate the old `SandboxTemplate` CRD (same config, old image digest). Since pool rotation is additive (new pool created before old pool is drained), the old pool's config should be retained in version control or Helm values until the new pool is verified.

**Token/Connector Service:** Rolling Deployment update. Stateless — reads from Postgres/Redis, so no special migration needed for the service itself.

**KMS Key Rotation Procedure:**

1. **Rotation steps:**
   - Generate a new envelope key (DEK) in KMS; the old key remains active for decryption.
   - Deploy the Token Service with the new key ID as the default encryption key.
   - A background migration job re-encrypts all stored tokens: reads each token with the old DEK, re-encrypts with the new DEK, and updates the row atomically.
   - The `key_version` column on each encrypted token row tracks which DEK was used — the Token Service can decrypt with any known key version.
   - Once all rows are re-encrypted (verified by query: `SELECT COUNT(*) WHERE key_version < current_version`), the old key can be disabled in KMS (but retained for disaster recovery).
2. **Frequency:** Recommended every 90 days, or immediately on suspected compromise.
3. **Monitoring:** Alert if re-encryption job hasn't completed within 24h of key rotation.

**Rollback:** All components support rollback by deploying the previous version. Schema migrations are always backward-compatible (expand-contract). Pool rotation is reversed by creating a new pool with the old image.

### 10.6 Environment Resource and RBAC Model

`Environment` is a first-class admin API resource. A named, RBAC-governed project context grouping runtimes and connectors for a team.

**Two access paths:**
- **Transparent filtering** (default) — user connects to standard endpoint; gateway computes the union of authorized runtimes across all environments where the user's groups have a role and returns that filtered view.
- **Explicit environment endpoint** (opt-in) — dedicated paths across all external interfaces: `/mcp/environments/{name}`, `/v1/environments/{name}/sessions`, scoped model namespace on `/v1/responses` and `/v1/chat/completions`.

**Runtime and connector selection is tag-based** using Kubernetes-style label expression syntax with `include`/`exclude` overrides:

```yaml
environment:
  name: security-team
  tenantId: acme-corp
  description: "Security engineering workspace"

  members:
    - identity:
        type: oidc-group
        value: security-engineers
      role: creator
    - identity:
        type: oidc-group
        value: security-leads
      role: admin

  runtimeSelector:
    matchLabels:
      team: security
    matchExpressions:
      - key: approved
        operator: In
        values: ["true"]
    types: [agent, mcp]
    include: [legacy-code-auditor]
    exclude: [unstable-scanner]

  mcpRuntimeFilters:
    - runtimeSelector:
        matchLabels:
          category: execution
      allowedCapabilities: [read, execute]
      deniedCapabilities: [write, delete, admin]

  connectorSelector:
    matchLabels:
      team: security
    allowedCapabilities: [read, search, network]
    deniedCapabilities: [write, delete, execute, admin]

  defaultDelegationPolicy: security-policy
  crossEnvironmentDelegation: false
```

**Member roles:** `viewer`, `creator`, `operator`, `admin`.

**`mcpRuntimeFilters`** — capability-based tool filtering for `type: mcp` runtimes. Capabilities inferred from MCP `ToolAnnotations` (see Section 5.1). Name collisions resolved by `runtime:tool` qualified reference in `overrides`.

**`connectorSelector`** — internal only. Controls what connectors agents can use via the platform MCP server. External clients do not call connectors through environment policy.

**Cross-environment delegation** — structured bilateral declaration model:

```yaml
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
  inbound:
    - sourceEnvironment: "*"
      runtimes:
        matchLabels:
          shared: "true"
```

Effective cross-environment access requires both sides to permit it. Neither side can unilaterally grant the other access.

**Effective delegation scope:** `(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy`

**Gateway enforcement at delegation time:**
1. Resolve target runtime to its home environment
2. Verify calling environment has an outbound declaration permitting target → `target_not_in_scope` if absent
3. Verify target environment has an inbound declaration permitting calling environment → `target_not_in_scope` if absent
4. Apply DelegationPolicy as normal → `target_not_authorized` if policy doesn't permit

**Connectors are never cross-environment.** Child sessions use their own environment's connector configuration.

**`noEnvironmentPolicy`:** `deny-all` (platform default) or `allow-all`. Configurable per tenant, with platform-wide default at Helm time.

**Identity:** OIDC. Groups from LDAP/AD carried as JWT claims. `introspectionEnabled: true` adds real-time group checks at latency cost.

**Environment-level billing rollup (v1):** `environmentId` populated on all billing events for sessions created in an environment context.

**Tenant RBAC Config:** Managed via `PUT /v1/admin/tenants/{id}/rbac-config`. Includes `noEnvironmentPolicy`, `identityProvider` (OIDC), `tokenPolicy`, `capabilities` taxonomy (platform defaults + tenant custom), `mcpAnnotationMapping` overrides.

**V1 data model accommodations:**
- `Runtime` resources need `labels` map from v1
- `Connector` registrations need `labels` map from v1
- `type:mcp` runtime tool schemas cached by gateway from v1
- `GET /v1/runtimes` and `list_runtimes` accept optional `?environmentId=` stub
- Session creation accepts optional `environmentId` parameter as no-op stub
- `environmentId` as nullable field on billing event schema from Phase 1
- `crossEnvironmentDelegation` structured form schema from Phase 1

### 10.7 Experiment Primitives

`ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor` (see Section 4.8).

```yaml
experiments:
  - id: claude-v2-rollout
    status: active                  # active | paused | concluded
    baseRuntime: claude-worker
    variants:
      - id: treatment
        runtime: claude-worker-v2
        pool: claude-worker-v2-sandboxed-medium
        weight: 10                  # percentage
    targeting:
      mode: percentage              # percentage | cohort | combined
      sticky: user                  # user | session | none
    propagation:
      childSessions: inherit        # inherit | control | independent
```

**Targeting modes:** `percentage` (deterministic hash), `cohort` (explicit whitelist), `combined` (percentage within cohort).

**Experiment context propagates through delegation leases:**
```json
{
  "experimentContext": {
    "experimentId": "claude-v2-rollout",
    "variantId": "treatment",
    "inherited": true
  }
}
```

Results API aggregates eval scores by variant: `GET /v1/experiments/{id}/results`.

PoolScalingController manages variant pool lifecycle automatically — variant warm count derived from base pool demand signals × variant weight × safety factor.

**What Lenny explicitly will not build:** Statistical significance testing, experiment lifecycle management (winner declaration), multi-armed bandits, segment analysis. Those belong in dedicated experimentation platforms.

---

## 11. Policy and Controls

### 11.1 Admission and Fairness

| Control                              | Granularity                             |
| ------------------------------------ | --------------------------------------- |
| Rate limits (requests/min)           | Global, per-user, per-runtime, per-pool |
| Concurrency limits (active sessions) | Global, per-user, per-team, per-runtime |
| Active delegated children            | Per-session, per-user                   |
| Concurrent uploads                   | Per-session, global                     |
| Upload size                          | Per-file, per-session                   |

### 11.2 Budgets and Quotas

| Budget                      | Scope                                                                                      |
| --------------------------- | ------------------------------------------------------------------------------------------ |
| Token limits (LLM tokens)   | Per-request, per-session, per-user/window, per-tenant/window, global/window, per-task-tree |
| Runtime limits (wall clock) | Per-session, per-child                                                                     |
| Retry budget                | Per-session (client-set, deployer-capped)                                                  |
| Delegation budget           | Per-session (depth, fan-out, total children)                                               |
| Concurrent sessions         | Per-tenant, per-user                                                                       |
| Storage quota               | Per-tenant (total artifact storage)                                                        |

**Budget inheritance:** Children inherit strictly narrower budgets. A parent cannot bypass top-level limits by spawning many children.

**Hierarchical Quota Model:** Quotas are enforced hierarchically: global → tenant → user. A user quota cannot exceed its tenant's quota. Tenant quotas are configured via the admin API or Helm values. Soft warnings are emitted at 80% of quota utilization, surfaced as billing events per Section 11.2.1. Hard limits are enforced at 100% — new sessions or requests are rejected with a `QUOTA_EXCEEDED` error. Quota reset periods are configurable per quota type: hourly, daily, monthly, or rolling window.

**Quota Update Timing:** Token usage quotas are updated in real-time via Redis counters. The runtime adapter extracts token counts from LLM provider responses and reports them to the gateway via the `ReportUsage` RPC (Section 4.7). The gateway increments Redis counters on each usage report — it does not parse LLM provider responses directly. Postgres is updated periodically (every 60 s per session) as a durable checkpoint, and on session completion as final reconciliation. The gateway enforces budget limits against Redis counters (fast path); if a session exceeds its token budget, the gateway terminates it immediately rather than waiting for session completion. During Redis unavailability (fail-open window), the gateway tracks usage in-memory per session and enforces the per-session budget locally. Only cross-session and per-tenant quotas may drift during this window (bounded per DevOps-M2).

#### 11.2.1 Billing Event Stream

The platform emits a structured billing event stream for per-tenant, per-session cost attribution. This stream is the source of truth for invoicing and usage-based billing integrations.

**Event types:**

| Event Type           | Emitted When                                                           |
| -------------------- | ---------------------------------------------------------------------- |
| `session.created`    | A new session is created                                               |
| `session.completed`  | A session reaches a terminal state (completed, failed, cancelled)      |
| `delegation.spawned` | A child session is created via recursive delegation                    |
| `token.checkpoint`   | Periodically during a session (configurable interval, not only at end) |
| `credential.lease`   | A credential lease is acquired or renewed on behalf of a session       |

**Event schema (all events):**

| Field             | Type     | Description                                                 |
| ----------------- | -------- | ----------------------------------------------------------- |
| `sequence_number` | uint64   | Monotonic, gap-free per-tenant sequence number              |
| `tenant_id`       | string   | Tenant that owns the session                                |
| `user_id`         | string   | User who initiated the session                              |
| `session_id`      | string   | Session that generated the event                            |
| `event_type`      | string   | One of the event types above                                |
| `timestamp`       | RFC 3339 | When the event occurred                                     |
| `cost_dimensions` | object   | `{ tokens_in, tokens_out, pod_minutes, credential_leases }` |

**Delivery and sinks:**

Events are published to a deployer-configured sink. Supported sinks:

- **Webhook URL** — POST with HMAC signature, at-least-once delivery with retry and exponential backoff.
- **Message queue** — SQS, Google Pub/Sub, or Kafka topic. The gateway publishes via a pluggable sink interface.
- **Both** — webhook and queue can be active simultaneously for the same tenant.

**Immutability guarantees:**

- Billing events are **append-only** in the EventStore (Postgres). Once written, they are never updated or deleted.
- Each event carries a **monotonic sequence number** scoped to the tenant, enabling consumers to detect gaps.
- **Redis fail-open (Section 12.4) does not apply to billing events.** Billing events are always written to Postgres synchronously before being published to external sinks. If Postgres is unavailable, the operation that would generate the event blocks or fails — billing data is never silently dropped.

### 11.3 Timeouts and Cancellation

| Timeout                  | Default | Configurable       |
| ------------------------ | ------- | ------------------ |
| Request timeout          | 30s     | Yes                |
| Upload timeout           | 300s    | Yes                |
| Setup command timeout    | 300s    | Yes                |
| Max session age          | 7200s   | Yes (deployer cap) |
| Max idle time            | 600s    | Yes                |
| Max resume window        | 900s    | Yes                |
| Max elicitation wait     | 600s    | Yes (per pool)     |
| Max elicitations/session | 50      | Yes (per pool)     |

Cancellation is first-class: clients and parent agents can cancel sessions/tasks cleanly.

**Session expiry warning:** The gateway sends a `session_expiring_soon` event to the client and pod 5 minutes before `maxSessionAge` expires. This gives the agent time to checkpoint and the client time to extend or wrap up. The agent receives a `DEADLINE_APPROACHING` signal via the adapter.

> **Note:** The 2h default is a conservative starting point. Deployers should tune `maxSessionAge` per Runtime based on expected workload duration.

### 11.4 User Invalidation

Three levels:

| Level        | Effect                                                                           |
| ------------ | -------------------------------------------------------------------------------- |
| Soft disable | Deny new sessions                                                                |
| Hard disable | Also block new delegated tasks; reject pending delegation approvals for the user |
| Full revoke  | Terminate active sessions, invalidate cached auth, deny reconnects               |

**Full revoke propagation mechanism:**

1. Gateway looks up all active sessions for the user (via SessionStore).
2. For each session in the user's task tree: gateway sends a `Terminate` RPC to the pod with reason `USER_REVOKED`.
3. The pod's runtime adapter initiates graceful shutdown (SIGTERM to agent, wait up to 10s, then SIGKILL).
4. Gateway marks all sessions as terminated in SessionStore.
5. Cached auth tokens for the user are invalidated in Redis.
6. Credential leases held by the user's sessions are revoked (returned to pool).
7. Pending elicitations for the user are dismissed.

> **Note:** Invalidation is asynchronous — the API call returns immediately and propagation completes within seconds. The `GET /v1/sessions` endpoint reflects the updated state once propagation completes.

### 11.5 Idempotency

Critical operations support idempotency keys:

- CreateSession
- FinalizeWorkspace
- StartSession
- SpawnChild
- Approve/DenyDelegation
- Resume

Prevents duplicate sessions or children during gateway failover or client retries.

### 11.6 Circuit Breakers

Operators can declare degraded states:

- Runtime X degraded / offline
- Pool Y full
- External connector Z down
- Uploads temporarily disabled
- Delegation depth > N disabled during incident

### 11.7 Audit Logging

Every session/task/delegation produces durable records:

- Who requested it (user_id, tenant_id)
- What runtime, pool, isolation profile
- Parent/child lineage
- Which policies were applied and their results
- Token usage
- Retries, cancellations, failures
- External tool access and auth prompts
- Denial reasons

**Integrity:** Audit tables use **append-only semantics** — the gateway's database role (`lenny_app`) has INSERT-only grants on audit tables (no UPDATE, DELETE). These grants are defined in the schema migration files and verified by a startup health check — the gateway queries `information_schema.role_table_grants` at startup and logs a warning if UPDATE or DELETE grants are found on audit tables. Deployers should run periodic drift checks (e.g., via CI) to ensure grants haven't been modified. For production deployments, audit events **must** be streamed to an external immutable log (SIEM, cloud audit service, or append-only object storage) in addition to Postgres storage. This ensures audit integrity even if the database is compromised.

### 11.8 Billing Event Stream

The platform emits a structured billing event stream that provides per-tenant, per-session cost attribution suitable for invoice-grade billing integrations.

**Event types:**

| Event Type               | Trigger                                                                                    |
| ------------------------ | ------------------------------------------------------------------------------------------ |
| `session.created`        | A new session is created                                                                   |
| `session.completed`      | A session reaches a terminal state (completed, failed, terminated)                         |
| `delegation.spawned`     | A child session is created via recursive delegation                                        |
| `token_usage.checkpoint` | Periodic token usage snapshot (emitted at configurable intervals, not only at session end) |
| `credential.leased`      | A credential is leased from a credential pool to a session                                 |

**Event schema:**

Every billing event includes the following fields:

- `sequence_number` — Monotonically increasing, per-tenant sequence number (no gaps allowed)
- `tenant_id` — Tenant that owns the session
- `user_id` — User who initiated the session (or parent session owner for delegations)
- `session_id` — Session this event pertains to
- `event_type` — One of the event types above
- `timestamp` — Server-generated UTC timestamp at event creation
- **Cost dimensions:**
  - `tokens_input` / `tokens_output` — Token counts for the checkpoint window (where applicable)
  - `pod_minutes` — Wall-clock pod time consumed (where applicable)
  - `credential_pool_id` / `credential_id` — Credential pool and specific credential used (for `credential.leased` events)

**Delivery sinks:**

Billing events are published to a deployer-configurable sink. Supported sink types:

- **Webhook URL** — Events are POSTed as JSON with HMAC-SHA256 signature headers. Failed deliveries are retried with exponential backoff and dead-lettered after exhaustion.
- **Message queue** — SQS, Google Pub/Sub, or Kafka topic. The gateway publishes asynchronously but only after the synchronous Postgres write confirms.
- **Both** — Webhook and message queue simultaneously for redundancy.

**Immutability guarantees:**

- Billing events are **always written to Postgres synchronously** via the `EventStore`, regardless of Redis availability. The Redis fail-open behavior (Section 12.4) applies only to rate-limit counters — billing events are never deferred, batched, or dropped.
- The `EventStore` billing table uses append-only semantics (INSERT only, no UPDATE or DELETE grants), matching the audit log integrity model.
- Each event carries a monotonic `sequence_number` scoped to the tenant, enabling consumers to detect gaps and request replays via the metering API.
- Events are retained for a deployer-configurable retention period (default: 13 months) to support annual billing cycles and dispute resolution.

---

## 12. Storage Architecture

### 12.1 Design Principle

Abstract by **storage role**, not by raw database API. Each store exposes domain operations, not generic CRUD.

### 12.2 Storage Roles

| Role                  | Backend                                   | Purpose                                                                       |
| --------------------- | ----------------------------------------- | ----------------------------------------------------------------------------- |
| `SessionStore`        | Postgres                                  | Sessions, tasks, delegation tree, lineage, retry state                        |
| `LeaseStore`          | Redis (fallback: Postgres advisory locks) | Distributed session coordination                                              |
| `TokenStore`          | Postgres (encrypted)                      | Downstream OAuth tokens, refresh tokens                                       |
| `QuotaStore`          | Redis + Postgres                          | Rate limit counters, budget tracking                                          |
| `ArtifactStore`       | MinIO (dev: local disk)                   | Uploaded files, checkpoints, workspace snapshots                              |
| `EventStore`          | Postgres                                  | Audit events, session logs, stream cursors                                    |
| `CredentialPoolStore` | Postgres (encrypted)                      | Credential pool definitions, lease assignments, health scores, cooldown state |

### 12.3 Postgres HA Requirements

**Minimum topology:** Lenny requires a PostgreSQL instance (14+) with synchronous replication and automatic failover. This can be provided by a managed service (AWS RDS Multi-AZ, GCP Cloud SQL HA, Azure Database for PostgreSQL) or a self-managed operator (CloudNativePG, Patroni). Managed services are **recommended** as the default for most deployments.

| Deployment             | Recommendation                                             |
| ---------------------- | ---------------------------------------------------------- |
| Cloud (production)     | Managed PostgreSQL with multi-AZ HA (e.g., RDS, Cloud SQL) |
| On-prem / self-managed | CloudNativePG operator or Patroni on Kubernetes            |
| Local dev              | Single PostgreSQL container (via docker-compose)           |

**Connection pooling:** PgBouncer (or pgcat) is **required** in front of Postgres. Each gateway replica maintains a connection pool; without pooling, HPA-scaled replicas exhaust Postgres connection limits. PgBouncer must be configured in **transaction-mode** pooling (not session-mode) to ensure compatibility with RLS enforcement via `SET app.current_tenant` (see Section 4.2) — the `SET` is scoped to the transaction boundary, so transaction-mode guarantees the setting does not leak across tenants sharing the same pooled connection.

- **Deployment topology:** PgBouncer runs as a separate Deployment (minimum 2 replicas) fronted by a ClusterIP Service — not as a sidecar on each gateway pod. This centralizes pool management and avoids per-pod connection sprawl toward Postgres.
- **Pool mode:** Transaction mode (`pool_mode = transaction`). This is required for RLS enforcement because `SET app.current_tenant` is scoped to the transaction boundary; session mode would leak tenant context across unrelated requests sharing a backend connection.
- **Sizing guidance:** Set `default_pool_size` to approximately `max_connections / number_of_PgBouncer_replicas`, leaving headroom for superuser and replication connections. Configure `reserve_pool_size` (e.g., 5–10 per pool) for burst headroom, with `reserve_pool_timeout` set to a short duration (e.g., 3s) so reserved connections are only used under genuine load spikes.
- **HA:** PgBouncer is stateless — multiple replicas behind the Kubernetes ClusterIP Service provide transparent failover. If one replica is terminated or fails a health check, the Service routes traffic to surviving replicas with no client-side changes required.
- **Monitoring:** Deploy `pgbouncer_exporter` as a sidecar on each PgBouncer pod to expose Prometheus metrics. Key metrics to alert on: pool utilization (`cl_active` / `sv_active` vs. pool size), client wait time (`cl_waiting_time`), and average query duration (`avg_query_time`).

**Read replicas:** The gateway should use separate connection strings for read and write traffic. Read-heavy queries (session status, task tree, audit reads, usage reports) should be routed to read replicas. Most managed services provide a reader endpoint for this purpose. Write traffic goes to the primary only.

**RPO/RTO targets:**

- RPO: 0 (synchronous replication — no committed transaction lost)
- RTO: < 30s (automatic failover)

**Backups:** Daily base backups + continuous WAL archival. Restore tested monthly (see Section 17.3 for the `lenny-restore-test` CronJob procedure).

### 12.4 Redis HA and Failure Modes

**Minimum topology:** Redis Sentinel (3 sentinels, 1 primary + 1 replica). Redis Cluster if sharding is needed at scale.

**Security:** Redis AUTH (ACLs) and TLS are **required**. Cached access tokens are encrypted before storage in Redis (not stored as plaintext). Tokens are encrypted using AES-256-GCM with a key derived from the Token Service's envelope encryption key; each cached token is stored as `{nonce || ciphertext || tag}`, and the encryption key is rotated alongside the envelope key (Section 10.5).

**Failure behavior per use case:**

| Use Case                   | On Redis Unavailability                                                                                                     |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| Rate limit counters        | **Fail open with bounded window** — allow requests for up to `rateLimitFailOpenMaxSeconds` (default: 60s), then fail closed |
| Distributed session leases | **Fall back** to Postgres advisory locks (higher latency)                                                                   |
| Routing cache              | **Fall back** to Postgres lookup                                                                                            |
| Cached access tokens       | **Re-fetch** from TokenStore (Postgres)                                                                                     |
| Quota counters             | **Fail open** for short window, reconcile from Postgres when restored                                                       |

**Quota counter reconciliation after fail-open:** When Redis recovers, the gateway reconciles quota counters by querying Postgres for actual usage (session token counts, active sessions) and resetting Redis counters to match. Maximum drift during fail-open is bounded by the window duration (default 60s per Sec-H5) multiplied by request rate — at 100 req/s per tenant, worst-case drift is ~6,000 requests. Reconciliation runs automatically when Redis becomes available and completes within seconds. During reconciliation, the gateway falls back to Postgres-backed counters (slower but accurate).

**Bounded fail-open for rate limiting:** When Redis becomes unavailable, the gateway starts a fail-open timer per replica. During the fail-open window, requests are allowed, a `rate_limit_degraded` metric is incremented, and an alert fires. After the window expires, if Redis is still unavailable, rate limiting fails **closed** — new requests are rejected with 429 until Redis recovers. **Emergency hard limit:** Each gateway replica maintains an in-memory per-user request counter as a coarse emergency backstop. This counter is not shared across replicas (so the effective limit is `N * per_replica_limit`) but prevents a single user from sending unlimited requests through one replica during the fail-open window. The `rateLimitFailOpenMaxSeconds` is configurable per deployment (default 60s).

### 12.5 Artifact Store

**Backend:** MinIO (S3-compatible). For local development, use local disk with the same interface.

**HA topology requirements:**

- **Production topology:** MinIO with erasure coding (minimum 4 nodes, recommended 4+ nodes across availability zones) for data durability
- **Versioning:** Enable bucket versioning for checkpoint objects to prevent accidental overwrites
- **Replication:** For multi-zone deployments, configure MinIO site-to-site replication for near-zero RPO on artifact data

**Do not use Postgres for blob storage.** Workspace checkpoints (up to 500MB) cause TOAST overhead, vacuum pressure, and degrade transactional workload performance.

**Encryption at rest:** All stored objects (checkpoints, workspace snapshots, session transcripts) contain sensitive data including conversation history and workspace file contents. MinIO server-side encryption (SSE-S3 or SSE-KMS) must be enabled for production deployments. For cloud deployments using managed object storage (S3, GCS, Azure Blob), encryption at rest is typically enabled by default. For self-hosted MinIO, enable SSE with a KMS backend (HashiCorp Vault, AWS KMS) or at minimum SSE-S3 with MinIO's internal key management.

**Checkpoint retention policy:**

- Keep only the latest 2 checkpoints per active session
- Delete all checkpoints when session terminates and resume window expires
- Background GC job runs every 15 minutes to clean expired artifacts. The job is owned by the gateway — it runs as a leader-elected goroutine inside the gateway process (not a separate CronJob). Only one gateway instance runs GC at a time via the existing leader-election lease.
- The GC job is idempotent: it queries Postgres for artifacts past their TTL, deletes them from MinIO, then marks the rows as `deleted` in Postgres. Re-running on the same set is safe because MinIO delete-on-absent is a no-op and the Postgres update is conditional on current state.
- Individual artifact deletion failures are retried independently — one stuck artifact does not block cleanup of others. On cycle failure the job logs the error, increments the error counter, and retries on the next 15-minute cycle.
- **Monitoring:** `lenny_gc_runs_total` (counter), `lenny_gc_artifacts_deleted` (counter), `lenny_gc_errors_total` (counter), `lenny_gc_duration_seconds` (histogram). Alert rule `ArtifactGCBacklog` (Section 16.5) fires when expired artifacts pending cleanup exceeds 1 000.
- Session artifact retention: configurable TTL (default 7 days), extendable per session

### 12.6 Interface Design

Good:

```
SessionStore.claim_session(session_id, gateway_id)
SessionStore.mark_session_attached(session_id, pod_id)
TokenStore.save_refresh_token(user_id, connector_id, encrypted_token)
QuotaStore.increment_token_usage(user_id, window, tokens_used)
```

Bad:

```
Database.query("UPDATE sessions SET ...")
GenericStore.put(key, value)
```

### 12.7 Extensibility

- Define small role-based interfaces
- Start with Postgres + Redis
- Keep migrations/schema explicit and backend-specific
- Add new backends only under real pressure
- Token store module is separate even if initially backed by Postgres (allows future Vault/KMS migration)

### 12.8 Compliance Interfaces

**Legal hold.** Sessions and artifacts support a `legal_hold` boolean flag. When set, the artifact retention policy is suspended — artifacts are not deleted by the GC job regardless of TTL. Legal holds are set and cleared via the admin API (`POST /v1/admin/legal-hold`), which accepts a session ID or artifact ID and the desired hold state.

**Data erasure (GDPR).** Each store interface includes `DeleteByUser(user_id)` and `DeleteByTenant(tenant_id)` methods that remove all user- or tenant-scoped data. Erasure covers: sessions, task trees, audit events, artifacts, credential records, and billing events. Erasure is implemented as a background job that runs to completion and produces an erasure receipt (stored in the audit trail) confirming what was deleted and when.

**Data residency.** The platform does not enforce data residency at the application level — this is the deployer's responsibility via infrastructure choices (region-specific clusters, Postgres/MinIO region configuration). The design supports multi-region deployment but does not provide built-in cross-region replication or data routing.

**Audit trail.** All compliance operations (legal hold set/cleared, erasure requested/completed) are logged in the audit trail (Section 11.7) with the requesting admin's identity, timestamp, and affected resource IDs.

---

## 13. Security Model

### 13.1 Pod Security

| Control         | Setting                                                                                           |
| --------------- | ------------------------------------------------------------------------------------------------- |
| User            | Non-root (specific UID/GID)                                                                       |
| Capabilities    | All dropped                                                                                       |
| Root filesystem | Read-only                                                                                         |
| Writable paths  | tmpfs (`/tmp`), workspace, sessions, artifacts                                                    |
| Egress          | Default-deny NetworkPolicy; allow only gateway + required internal services                       |
| Credentials     | No standing credentials; projected SA token + short-lived credential lease only (see Section 4.9) |
| File delivery   | Gateway-mediated only                                                                             |

### 13.2 Network Isolation

**Minimum CNI requirement:** Calico or Cilium (must support NetworkPolicy enforcement including egress rules).

**Default-deny policy (applied to `lenny-agents` namespace):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: lenny-agents
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
```

**Allow gateway-to-pod (applied to all agent pods):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-gateway-ingress
  namespace: lenny-agents
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: lenny-system
          podSelector:
            matchLabels:
              lenny.dev/component: gateway
  policyTypes: [Ingress]
```

**Allow pod-to-gateway and DNS (applied to all agent pods):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-pod-egress-base
  namespace: lenny-agents
spec:
  podSelector:
    matchLabels:
      lenny.dev/managed: "true"
  egress:
    - to: # Gateway
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: lenny-system
          podSelector:
            matchLabels:
              lenny.dev/component: gateway
    - to: # DNS (lenny-system CoreDNS)
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: lenny-system
          podSelector:
            matchLabels:
              lenny.dev/component: coredns
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
  policyTypes: [Egress]
```

> **Note:** The namespace selectors above use `kubernetes.io/metadata.name` rather than custom labels like `lenny.dev/component`. This is an immutable label set by the Kubernetes API server on namespace creation -- it cannot be added to or spoofed on other namespaces, unlike custom labels. This prevents an attacker who can create namespaces from bypassing network isolation by applying a matching custom label.

**Per-pool egress relaxation:** Pools that need internet access (e.g., for LLM API calls) get additional NetworkPolicy resources allowing egress to specific CIDR ranges or services. These policies are **pre-created** by the Helm chart (or deployer) using label selectors that match pool labels (e.g., `lenny.dev/pool: <pool-name>`, `lenny.dev/egress-profile: restricted`). The warm pool controller does NOT create or modify NetworkPolicies — it only labels pods with the appropriate pool and egress-profile labels so that the pre-created policies take effect. This avoids granting the controller RBAC permissions for NetworkPolicy resources.

**`egressProfile` enum:**

| Profile                | Egress Allowed                                 | Use Case                                              |
| ---------------------- | ---------------------------------------------- | ----------------------------------------------------- |
| `restricted` (default) | Gateway + DNS proxy only                       | Agent pods that use the LLM proxy; no direct internet |
| `provider-direct`      | Gateway + DNS proxy + LLM provider CIDRs       | Direct LLM API access (Bedrock, Vertex endpoints)     |
| `internet`             | Gateway + DNS proxy + all internet (0.0.0.0/0) | Pods needing package install, web access              |

> **Note:** CIDR ranges for `provider-direct` are maintained in the Helm values (`egressCIDRs.providers`) and updated by deployers when provider endpoints change. NetworkPolicies reference these CIDRs via pre-created policies (per K8s-M3).

**DNS exfiltration mitigation:** A dedicated **CoreDNS instance** runs in `lenny-system` (labeled `lenny.dev/component: coredns`) and serves as the DNS resolver for all agent namespaces by default. The `allow-pod-egress-base` NetworkPolicy above routes DNS traffic exclusively to this instance — agent pods cannot reach `kube-system` DNS directly.

The dedicated CoreDNS instance provides:

- **Query logging** — all DNS queries from agent pods are recorded for audit.
- **Per-pod rate limiting** — throttles query volume per source pod to prevent high-throughput tunneling.
- **Response filtering** — blocks TXT records exceeding a size threshold and drops unusual record types commonly used for DNS tunneling (e.g., NULL, PRIVATE, KEY).

For `standard` (runc) isolation profiles, deployers may explicitly opt out of the dedicated CoreDNS instance via pool configuration (`dnsPolicy: cluster-default`), which falls back DNS to `kube-system` CoreDNS. This must be a conscious choice — the dedicated instance is the default for all profiles.

### 13.3 Credential Flow

**MCP tool credentials (OAuth):**

```
Client authenticates → Gateway validates → Gateway mints session context
                                         → Gateway holds all downstream OAuth tokens
                                         → Pod receives: session context + projected SA token
                                         → Pod never receives: client tokens, downstream OAuth tokens
```

**LLM provider credentials (credential leasing — direct mode):**

```
Gateway evaluates CredentialPolicy → Token Service selects from pool or user source
                                   → Token Service materializes short-lived credentials
                                   → Gateway pushes CredentialLease to pod via AssignCredentials
                                   → Pod receives: materialized short-lived provider config
                                   → Pod never receives: pool root API keys, IAM role ARNs, long-lived secrets
                                   → On RATE_LIMITED: gateway rotates → pushes new lease via RotateCredentials
                                   → On session end: lease released back to pool
```

**LLM provider credentials (credential leasing — proxy mode, optional):**

```
Gateway evaluates CredentialPolicy → Token Service selects from pool or user source
                                   → Gateway generates lease token + proxy URL
                                   → Gateway pushes lease token + proxy URL to pod (NOT the real API key)
                                   → Pod sends LLM requests to proxy URL with lease token
                                   → Gateway proxy validates lease, injects real API key, forwards upstream
                                   → Real API key never enters the pod
                                   → On lease expiry/revocation: proxy immediately rejects requests
                                   → On session end: lease invalidated, proxy stops forwarding
```

**Key distinction:** MCP tool tokens are used by the gateway on behalf of pods (pods never see them). LLM provider credentials are either delivered directly as short-lived leases (direct mode) or kept entirely out of the pod via the credential-injecting reverse proxy (proxy mode) — see Section 4.9 for details on both modes.

### 13.4 Upload Security

- Gateway validates and authorizes all uploads
- Pod trusts only the gateway (not arbitrary clients)
- Path traversal protection (reject `..`, absolute paths, symlinks escaping workspace)
- Size limits enforced at gateway and pod
- Staging → validation → promotion pattern
- Archive extraction with zip-slip protection

---

## 14. Workspace Plan Schema

The `WorkspacePlan` is the declarative specification for how a session's workspace should be prepared:

```json
{
  "pool": "claude-worker-sandboxed-medium",
  "isolationProfile": "gvisor",
  "workspacePlan": {
    "sources": [
      {
        "type": "inlineFile",
        "path": "CLAUDE.md",
        "content": "# Project Instructions\n..."
      },
      {
        "type": "inlineFile",
        "path": ".claude/settings.json",
        "content": "{...}"
      },
      {
        "type": "uploadFile",
        "path": "src/main.ts",
        "uploadRef": "upload_abc123"
      },
      {
        "type": "uploadArchive",
        "pathPrefix": ".",
        "uploadRef": "upload_def456",
        "format": "tar.gz"
      },
      {
        "type": "mkdir",
        "path": "output/"
      }
    ],
    "setupCommands": [
      {
        "cmd": "npm ci",
        "timeoutSeconds": 300
      }
    ]
  },
  "env": {
    "NODE_ENV": "production",
    "LOG_LEVEL": "info"
  },
  "labels": {
    "team": "platform",
    "project": "auth-refactor",
    "ticket": "JIRA-1234"
  },
  "runtimeOptions": {
    "settingSources": ["project"],
    "streamingMode": true
  },
  "timeouts": {
    "maxSessionAgeSeconds": 3600,
    "maxIdleSeconds": 300
  },
  "retryPolicy": {
    "mode": "auto_then_client",
    "maxRetries": 2,
    "maxResumeWindowSeconds": 900
  },
  "credentialPolicy": {
    "preferredSource": "pool",
    "provider": "anthropic_direct",
    "pool": "claude-direct-prod",
    "allowPoolFallback": true,
    "allowProviderSwitch": false
  },
  "callbackUrl": "https://ci.example.com/hooks/lenny-complete",
  "delegationLease": {
    "maxDepth": 2,
    "maxChildrenTotal": 5,
    "delegationPolicyRef": "default-policy"
  }
}
```

**Field notes:**

- `env`: Key-value environment variables injected into the agent session. Validated against a deployer-configured blocklist of denied environment variable names/patterns (e.g., `AWS_SECRET_ACCESS_KEY`, `ANTHROPIC_API_KEY`, `*_SECRET_*`). Any env var matching the blocklist is rejected; everything else is allowed.
- `labels`: User-defined metadata for querying and organizing sessions. Not used for internal routing. Labels are indexed in the session store and filterable in all query APIs: `GET /v1/sessions` (list), `GET /v1/usage` (usage reports), `GET /v1/metering/events` (billing events). This enables cost attribution by project, team, ticket, or any custom dimension.
- `timeouts`: Per-session overrides, capped by deployer policy. Cannot exceed the Runtime's `limits.maxSessionAge`.
- `callbackUrl`: Optional webhook. Gateway POSTs a `SessionComplete` payload when the session reaches a terminal state. Because this field accepts a URL from the client, it is a potential SSRF vector. The following mitigations apply:
  1. **URL validation.** The value must be an HTTPS URL (no HTTP, no non-HTTP schemes). It must parse as a valid URL with a public DNS hostname. IP literals, `localhost`, loopback addresses, and link-local addresses are rejected at submission time.
  2. **DNS pinning.** The gateway resolves the hostname at registration time and pins the resolved IP. If the resolved IP falls within a private or reserved range (RFC 1918, RFC 6598, loopback, link-local, etc.) the callback is rejected. The actual callback request is sent to the pinned IP with the original hostname in the `Host` header, preventing DNS rebinding attacks where the hostname re-resolves to an internal IP between validation and request time.
  3. **Isolated callback worker.** Callback HTTP requests are made from a dedicated goroutine pool with its own `http.Client` configured with: connect timeout of 5 s, response-read timeout of 10 s, `CheckRedirect` returning an error (no redirect following), and egress through a separate network path where possible. At minimum, a `NetworkPolicy` blocks the callback worker pods from reaching cluster-internal CIDRs (pod network, service network, node metadata endpoints).
  4. **Optional domain allowlist.** Deployers can set `callbackUrlAllowedDomains` in the platform configuration. When the list is non-empty, only callback URLs whose hostname matches an entry (exact or `*.suffix` wildcard) are accepted. When the list is empty, the public-DNS validation in (1) applies.

  **Webhook Delivery Model.** The callback URL receives structured webhook events with the following contract:

  **Payload schema:**

  ```json
  {
    "event": "session.completed",
    "session_id": "sess_abc123",
    "status": "completed",
    "timestamp": "2025-01-15T10:30:00Z",
    "idempotency_key": "evt_xyz789",
    "data": {
      "usage": { "inputTokens": 15000, "outputTokens": 8000 },
      "artifacts": ["workspace.tar.gz"]
    }
  }
  ```

  **Authentication:** Webhooks are signed with HMAC-SHA256. The signature is sent in the `X-Lenny-Signature` header. The signing secret is provided by the client at session creation (`callbackSecret` field, stored encrypted).

  **Event types:** `session.completed`, `session.failed`, `session.terminated`, `delegation.completed` (for child task completion notifications).

  **Retry behavior:** Failed deliveries (non-2xx response or timeout) are retried with exponential backoff: 10 s, 30 s, 60 s, 300 s, 900 s (5 attempts total). After exhaustion, the event is marked as undelivered and queryable via `GET /v1/sessions/{id}/webhook-events`.

  **Idempotency:** Each event has a unique `idempotency_key`. Receivers should deduplicate by this key.

- `credentialPolicy`: Controls how LLM provider credentials are assigned. `preferredSource` can be `pool`, `user`, `prefer-user-then-pool`, or `prefer-pool-then-user`. If omitted, the Runtime's default policy is used. See Section 4.9.
- `runtimeOptions`: Runtime-specific options passed through to the agent binary. If the target Runtime defines a `runtimeOptionsSchema` (a JSON Schema document), the gateway validates `runtimeOptions` against it at session creation time and rejects invalid options with a descriptive error. If no schema is registered, options are passed through as-is (backward compatible) but a warning is logged. Maximum size: 64 KB.

---

## 15. External API Surface

Lenny exposes multiple client-facing APIs through the **`ExternalAdapterRegistry`** — a pluggable adapter system where simultaneously active adapters route by path prefix. All adapters implement a common interface:

```go
type ExternalProtocolAdapter interface {
    HandleInbound(ctx, w, r, dispatcher) error
    HandleDiscovery(ctx, w, r, runtimes []AuthorizedRuntime) error
    Capabilities() AdapterCapabilities
}
```

**`HandleDiscovery` is required on all adapters.** Every adapter translates Lenny's policy-scoped runtime list into its protocol's native discovery format.

**Three tiers of pluggability:**
- **Built-in** (compiled in): MCP, OpenAI Completions, Open Responses. Always available, configurable via admin API.
- **Config-driven**: deployer points gateway at a Go plugin binary or gRPC service at startup.
- **Runtime registration via admin API**: `POST /v1/admin/external-adapters` — takes effect immediately, no restart.

**Built-in adapter inventory:**

| Adapter | Path prefix | Protocol | Status |
|---|---|---|---|
| `MCPAdapter` | `/mcp` | MCP Streamable HTTP | V1 |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions | V1 |
| `OpenResponsesAdapter` | `/v1/responses` | Open Responses Specification | V1 |
| `A2AAdapter` | `/a2a/{runtime}` | A2A | Post-V1 |
| `AgentProtocolAdapter` | `/ap/v1/agent` | Agent Protocol | Post-V1 |

`OpenResponsesAdapter` covers both Open Responses-compliant clients and OpenAI Responses API clients. OpenAI's Responses API is a proper superset of Open Responses; the difference is OpenAI's proprietary hosted tools, which Lenny doesn't implement.

**`type: mcp` runtime dedicated endpoints:** Each enabled `type: mcp` runtime gets a dedicated MCP endpoint at `/mcp/runtimes/{runtime-name}`. Standard MCP capability negotiation. Not aggregated. An implicit session record is created per connection for audit and billing. Discovery: `GET /v1/runtimes` and `list_runtimes` return `mcpEndpoint` and `mcpCapabilities.tools` preview for `type: mcp` runtimes.

### 15.1 REST API

The REST API covers all non-interactive operations. It is the primary integration point for CI/CD pipelines, admin dashboards, CLIs, and clients in any language.

**Session lifecycle:**

| Method   | Endpoint                      | Description                                                               |
| -------- | ----------------------------- | ------------------------------------------------------------------------- |
| `POST`   | `/v1/sessions`                | Create a new session                                                      |
| `POST`   | `/v1/sessions/start`          | Create, upload inline files, and start in one call (convenience)          |
| `GET`    | `/v1/sessions/{id}`           | Get session status and metadata                                           |
| `GET`    | `/v1/sessions`                | List sessions (filterable by status, runtime, tenant, labels)             |
| `POST`   | `/v1/sessions/{id}/upload`    | Upload workspace files (pre-start or mid-session if enabled)              |
| `POST`   | `/v1/sessions/{id}/finalize`  | Finalize workspace and run setup                                          |
| `POST`   | `/v1/sessions/{id}/start`     | Start the agent runtime                                                   |
| `POST`   | `/v1/sessions/{id}/interrupt` | Interrupt current agent work                                              |
| `POST`   | `/v1/sessions/{id}/terminate` | End a session                                                             |
| `POST`   | `/v1/sessions/{id}/resume`    | Explicitly resume after retry exhaustion                                  |
| `POST`   | `/v1/sessions/{id}/derive`    | Create a new session pre-populated with this session's workspace snapshot |
| `DELETE` | `/v1/sessions/{id}`           | Terminate and clean up                                                    |

**Artifacts and introspection:**

| Method | Endpoint                             | Description                                                                                                                          |
| ------ | ------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------ |
| `GET`  | `/v1/sessions/{id}/artifacts`        | List session artifacts                                                                                                               |
| `GET`  | `/v1/sessions/{id}/artifacts/{path}` | Download a specific artifact/file                                                                                                    |
| `GET`  | `/v1/sessions/{id}/workspace`        | Download workspace snapshot (tar.gz)                                                                                                 |
| `GET`  | `/v1/sessions/{id}/transcript`       | Get session transcript (paginated)                                                                                                   |
| `GET`  | `/v1/sessions/{id}/logs`             | Get session logs (paginated, streamable via SSE)                                                                                     |
| `GET`  | `/v1/sessions/{id}/setup-output`     | Get setup command stdout/stderr                                                                                                      |
| `GET`  | `/v1/sessions/{id}/tree`             | Get delegation task tree                                                                                                             |
| `GET`  | `/v1/sessions/{id}/usage`            | Get token and resource usage. Returns tree-aggregated usage (including all descendant tasks) when the session has a delegation tree. |

**Async job support:**

| Method | Endpoint                 | Description                                                  |
| ------ | ------------------------ | ------------------------------------------------------------ |
| `POST` | `/v1/sessions/start`     | Accepts optional `callbackUrl` for completion notification   |
| `POST` | `/v1/sessions/{id}/messages` | Send a message to a session (unified endpoint — replaces `send`). Gateway rejects injection against runtimes with `injection.supported: false`. |

**Discovery and introspection:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/v1/runtimes` | List registered runtimes with full `agentInterface`, `mcpEndpoint`, `mcpCapabilities`, capabilities, and labels. Identity-filtered and policy-scoped. |
| `GET` | `/v1/runtimes/{name}/meta/{key}` | Get published metadata for a runtime (visibility-controlled) |
| `GET` | `/v1/models` | OpenAI-compatible model list (identity-filtered) |
| `GET` | `/v1/pools` | List pools and warm pod counts |
| `GET` | `/v1/usage` | Usage report (filterable by tenant, user, window, labels) |
| `GET` | `/v1/metering/events` | Paginated billing event stream |

**Evaluation hooks:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/v1/sessions/{id}/eval` | Accept scored evaluator results (LLM-as-judge scores, custom heuristics, ground-truth comparisons). Stored as session metadata. |
| `POST` | `/v1/sessions/{id}/replay` | Re-run a session against a different runtime version using the same workspace and prompt history. |

**Comprehensive Admin API:**

All operational configuration is API-managed. Configuration is split into two planes:

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas, User Role Assignments, Egress Profiles, Experiments, Scaling Policies, Memory Store Config, Webhooks, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

CRDs become derived state reconciled from Postgres by PoolScalingController.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST/PUT/GET/DELETE` | `/v1/admin/runtimes` | Manage runtime definitions |
| `POST/PUT/GET/DELETE` | `/v1/admin/delegation-policies` | Manage delegation policies |
| `POST/PUT/GET/DELETE` | `/v1/admin/connectors` | Manage connector definitions |
| `POST/PUT/GET/DELETE` | `/v1/admin/pools` | Manage pool configurations |
| `POST/PUT/GET/DELETE` | `/v1/admin/credential-pools` | Manage credential pools |
| `POST/PUT/GET/DELETE` | `/v1/admin/tenants` | Manage tenants |
| `PUT/GET` | `/v1/admin/tenants/{id}/rbac-config` | Tenant RBAC configuration |
| `POST/PUT/GET/DELETE` | `/v1/admin/environments` | Manage environments |
| `GET` | `/v1/admin/environments/{name}/usage` | Environment billing rollup |
| `GET` | `/v1/admin/environments/{name}/access-report` | Resolved member list with group expansion |
| `GET` | `/v1/admin/environments/{name}/runtime-exposure` | Runtimes/connectors in scope |
| `GET` | `/v1/admin/tenants/{id}/access-report` | Cross-environment access matrix |
| `POST/PUT/GET/DELETE` | `/v1/admin/experiments` | Manage experiments |
| `GET` | `/v1/experiments/{id}/results` | Experiment results by variant |
| `POST/PUT/GET/DELETE` | `/v1/admin/external-adapters` | Manage external protocol adapters |
| `POST` | `/v1/admin/pools/{name}/drain` | Drain a pool |
| `PUT` | `/v1/admin/pools/{name}/warm-count` | Adjust minWarm/maxWarm at runtime |
| `POST` | `/v1/admin/sessions/{id}/force-terminate` | Force-terminate a session |

**Admin API design constraints:** Error taxonomy, OIDC auth, etag-based concurrency, `dryRun` support, OpenAPI spec, audit logging.

**`GET /v1/usage` response schema:**

```json
{
  "period": { "start": "2025-01-01T00:00:00Z", "end": "2025-01-31T23:59:59Z" },
  "totalSessions": 1523,
  "totalTokens": { "input": 45000000, "output": 22000000 },
  "totalPodMinutes": 12500.5,
  "byTenant": [
    {
      "tenantId": "t1",
      "sessions": 800,
      "tokens": { "input": 25000000, "output": 12000000 }
    }
  ],
  "byRuntime": [
    {
      "runtime": "claude-worker",
      "sessions": 1200,
      "tokens": { "input": 38000000, "output": 18000000 }
    }
  ]
}
```

### 15.2 MCP API

The MCP interface is for **interactive streaming sessions** and **recursive delegation**. It exposes the gateway as an MCP server over Streamable HTTP via the `MCPAdapter`.

**MCP tools (client-facing):**

| Tool                       | Description                                          |
| -------------------------- | ---------------------------------------------------- |
| `create_session`           | Create a new agent session                           |
| `create_and_start_session` | Create, upload inline files, and start in one call   |
| `upload_files`             | Upload workspace files                               |
| `finalize_workspace`       | Seal workspace, run setup                            |
| `start_session`            | Start the agent runtime                              |
| `attach_session`           | Attach to a running session (returns streaming task) |
| `send_message`             | Send a message to a session (unified — replaces `send_prompt`) |
| `interrupt_session`        | Interrupt current agent work                         |
| `get_session_status`       | Query session state (including `suspended`)          |
| `get_task_tree`            | Get delegation tree for a session                    |
| `get_session_logs`         | Get session logs (paginated)                         |
| `get_token_usage`          | Get token usage for a session                        |
| `list_artifacts`           | List artifacts for a session                         |
| `download_artifact`        | Download a specific artifact                         |
| `terminate_session`        | End a session                                        |
| `resume_session`           | Resume a suspended or paused session                 |
| `list_sessions`            | List active/recent sessions (filterable)             |
| `list_runtimes`            | List available runtimes (identity-filtered, policy-scoped) |

**MCP features used:**

- Tasks (for long-running session lifecycle and delegation)
- Elicitation (for user prompts, auth flows)
- Streamable HTTP transport

#### 15.2.1 REST/MCP Consistency Contract

The REST API (Section 15.1) and MCP tools (Section 15.2) intentionally overlap for operations like session creation, status queries, and artifact retrieval. Three rules govern this overlap:

1. **Semantic equivalence.** REST and MCP endpoints that perform the same operation (e.g., `POST /v1/sessions` and `create_session` MCP tool) must return semantically identical responses. Both API surfaces share a common service layer in the gateway so that business logic, validation, and response shaping are implemented exactly once.

2. **Tool versioning.** MCP tool schema evolution is governed by Section 15.5 (API Versioning and Stability), item 2.

3. **Shared error taxonomy.** All error responses — REST and MCP — use the error categories defined in Section 16.3 (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`). REST errors return a JSON body: `{"error": {"code": "QUOTA_EXCEEDED", "category": "POLICY", "message": "...", "retryable": false}}`. MCP tool errors use the same `code` and `category` fields inside the MCP error response format, so clients can apply a single error-handling strategy regardless of API surface.

### 15.3 Internal Control API (Custom Protocol)

Gateway ↔ Pod communication over gRPC + mTLS. See Section 4.7 (Runtime Adapter) for the full RPC surface. Protobuf service definitions will be published as a separate runtime adapter specification (see Section 15.4).

### 15.4 Runtime Adapter Specification

The runtime adapter contract will be published as a **standalone specification** with:

- Protobuf `.proto` service and message definitions
- Error code enum with categories (transient, permanent, policy)
- Streaming message type definitions for `Attach` bidirectional stream
- Version negotiation protocol (adapter advertises capabilities at startup; gateway selects compatible protocol version)
- Health check contract (gRPC Health Checking Protocol)
- Reference implementation in Go

This is the primary document for community runtime adapter authors.

#### 15.4.1 Adapter↔Binary Protocol

The runtime adapter communicates with the agent binary over **stdin/stdout** using newline-delimited JSON (JSON Lines). Each message is a single JSON object terminated by `\n`. The `prompt` message type is removed — the unified `message` type handles all inbound content delivery.

**Inbound messages (adapter → agent binary via stdin):**

| `type` field  | Description                                      |
| ------------- | ------------------------------------------------ |
| `message`     | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` for concurrent-workspace mode. |
| `tool_result` | The result of a tool call requested by the agent. Carries `slotId` in concurrent-workspace mode. |
| `heartbeat`   | Periodic liveness ping; agent must respond       |
| `shutdown`    | Graceful shutdown with no new task               |

The `message` type carries an `input` field containing an `OutputPart[]` array (see Internal `OutputPart` Format below), supporting text, images, structured data, and other content types. No `sessionState` field — the runtime knows it's receiving its first message by virtue of just having started. No `follow_up` or `prompt` type anywhere in the protocol.

**Outbound messages (agent binary → adapter via stdout):**

| `type` field | Description                              |
| ------------ | ---------------------------------------- |
| `response`   | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |
| `tool_call`  | Agent requests execution of a tool. Carries `slotId` in concurrent-workspace mode. |
| `status`     | Optional status/trace update             |

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call on the platform MCP server.

**`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This allows multiple independent concurrent task streams through a single stdin channel.

**Task mode between-task signaling:** Adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes. The next `{type: "message"}` after scrub is the start of the new task.

**stderr** is captured by the adapter for logging and diagnostics but is **not** parsed as protocol messages.

#### Internal `OutputPart` Format

`agent_text` streaming event is replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays. This is Lenny's internal content model — the adapter translates to/from external protocol formats (MCP, A2A) at the boundary.

```json
{
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "artifact://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

**Properties:**

- **`type` is an open string — not a closed enum.** `"text"`, `"code"`, `"reasoning_trace"`, `"citation"`, `"screenshot"`, `"diff"` — whatever the runtime needs. Unknown types passed through opaquely. The gateway never needs to update for new semantic types.
- **`mimeType` handles encoding separately.** The gateway validates, logs, and routes based on MIME type without understanding semantics.
- **`inline` vs `ref` as properties, not types.** A part either contains bytes inline or points to a reference. Both valid for any type.
- **`annotations` as an open metadata map.** `role`, `confidence`, `language`, `final`, `audience` — any metadata. The gateway can index and filter on annotations without understanding the part type.
- **`parts` for nesting.** Compound outputs (e.g., `execution_result` containing code, stdout, stderr, chart) are first-class.
- **`id` enables part-level streaming updates** — concurrent part delivery where text streams while an image renders.

**Rationale for internal format over MCP content blocks directly:** Runtimes are insulated from external protocol evolution. When MCP adds new block types or A2A parts change, only the gateway's `ExternalProtocolAdapter` translation layer updates — runtimes are untouched.

SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays for runtime authors who want to produce output using familiar MCP formats.

#### `MessageEnvelope` — Unified Message Format

All inbound content messages use a unified `MessageEnvelope` across the stdin binary protocol, platform MCP server tools, and all external APIs.

```json
{
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system | external",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "input": ["OutputPart[]"]
}
```

**Adapter-injected fields — runtime never supplies these:**
- `from.kind` and `from.id` — injected by the adapter from execution context
- `requestId` in `lenny/request_input` — generated by the gateway; runtime only supplies `parts`

**`delivery`** — optional mechanical hint. `immediate` means deliver at next stdin read (after interrupt acknowledgment if needed). Absent means queue for next natural pause.

**`id`** — every message has a stable ID enabling threading, reply tracking, and non-linear context retrieval.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Future-proof:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all future conversational patterns without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

#### 15.4.2 RPC Lifecycle State Machine

The adapter follows a well-defined state machine:

```
INIT ──→ READY ──→ ACTIVE ──→ DRAINING ──→ TERMINATED
                     │                          ▲
                     └──────────────────────────┘
                       (session ends normally)
```

| State        | Description                                                                                           |
| ------------ | ----------------------------------------------------------------------------------------------------- |
| `INIT`       | Adapter process starts, opens gRPC connection to gateway (mTLS), writes placeholder manifest.         |
| `READY`      | Adapter signals readiness. Pod enters warm pool. Gateway may now assign sessions.                     |
| `ACTIVE`     | A session is in progress. Adapter manages MCP servers, lifecycle channel, and stdin/stdout relay.     |
| `DRAINING`   | Graceful shutdown requested. The adapter finishes the current exchange and signals the agent to stop. |
| `TERMINATED` | The adapter has exited. The gateway marks the pod as no longer available.                             |

Transitions are initiated by either the gateway (e.g., session assignment, drain request) or the adapter itself (e.g., readiness signal, exit on completion).

#### 15.4.3 Runtime Integration Tiers

To lower the barrier for third-party runtime authors, the spec defines three integration tiers (for `type: agent` runtimes only):

**Minimum** — enough to get a custom runtime working:

- stdin/stdout binary protocol only
- Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout
- Zero Lenny knowledge required
- No checkpoint/restore support, no detailed health reporting

**Standard** — minimum plus MCP integration:

- Connects to adapter's platform MCP server and connector servers via the adapter manifest
- Uses platform capabilities (delegation, discovery, output parts, elicitation)
- Standard MCP — no Lenny-specific code

**Full** — standard plus lifecycle channel:

- Opens the lifecycle channel for operational signals
- True session continuity, clean interrupt points, mid-session credential rotation
- `DRAINING` state with graceful shutdown coordination
- Checkpoint/restore support

Third-party authors should start with a minimum adapter and incrementally adopt standard and full features as needed.

#### 15.4.4 Sample Echo Runtime

The project includes a reference **`echo-runtime`** — a trivial agent binary that echoes back messages with metadata (timestamp, session ID, message sequence number). It serves two purposes:

1. **Platform testing:** Validates the full session lifecycle (pod claim → workspace setup → message → response → teardown) without requiring a real agent runtime or LLM credentials.
2. **Template for custom runtimes:** Demonstrates the stdin/stdout JSON Lines protocol, heartbeat handling, and graceful shutdown — the minimal contract a custom agent binary must implement.

### 15.5 API Versioning and Stability

Community contributors and integrators need clear guarantees about which APIs are stable and how breaking changes are managed. Each external surface follows its own versioning scheme:

1. **REST API:** Versioned via URL path prefix (`/v1/`). Breaking changes require a new version (`/v2/`). Non-breaking additions (new fields, new endpoints) are added to the current version. The previous version is supported for at least 6 months after a new version ships.

2. **MCP tools:** Versioned via the MCP protocol's capability negotiation. Tool schemas can add optional fields without a version bump. Removing or renaming fields, or changing semantics, is a breaking change.

3. **Runtime adapter protocol:** Versioned independently (see Section 15.4). The adapter advertises a protocol version at INIT; the gateway selects a compatible version. Major version changes are breaking.

4. **CRDs:** Versioned via Kubernetes API versioning conventions (`v1alpha1` → `v1beta1` → `v1`). Conversion webhooks handle multi-version coexistence during upgrades.

5. **Definition of "breaking change":** Removing a field, changing a field's type, changing the default behavior of an existing feature, removing an endpoint/tool, or changing error codes for existing operations.

6. **Stability tiers:**
   - `stable`: Covered by versioning guarantees above.
   - `beta`: May change between minor releases with deprecation notice.
   - `alpha`: May change without notice.

### 15.6 Client SDKs

Lenny provides official client SDKs for **Go** and **TypeScript/JavaScript** as part of the v1 deliverables. SDKs encapsulate session lifecycle management, MCP streaming with automatic reconnect-with-cursor, file upload multipart handling, webhook signature verification, and error handling with retries — logic that is complex and error-prone to re-implement from the protocol specs alone.

SDKs are generated from the OpenAPI spec (REST) and MCP tool schemas, with hand-written streaming and reconnect logic layered on top. Community SDKs for other languages can build on the published OpenAPI spec and the MCP protocol specification.

Client SDKs follow the same versioning scheme as the API surfaces they wrap (Section 15.5): SDK major versions track REST API versions, and SDK releases note any MCP tool schema changes.

---

## 16. Observability

### 16.1 Metrics

| Metric                                                                           | Type            |
| -------------------------------------------------------------------------------- | --------------- |
| Active sessions (by runtime, pool, state, tenant)                                | Gauge           |
| Warm pods available (by pool)                                                    | Gauge           |
| Stale warm pods (idle beyond threshold, by pool)                                 | Gauge           |
| Session creation latency (phases)                                                | Histogram       |
| Time-to-claim (session request to pod claimed)                                   | Histogram       |
| Pod state transition durations (per state)                                       | Histogram       |
| Upload bytes/second and queue depth                                              | Counter + Gauge |
| Token usage (by user, runtime, tenant)                                           | Counter         |
| Retry count (by failure classification)                                          | Counter         |
| Resume success/failure rate                                                      | Counter         |
| Delegation depth distribution                                                    | Histogram       |
| Delegation tree size distribution                                                | Histogram       |
| Gateway replica count                                                            | Gauge           |
| Gateway active streams (per replica)                                             | Gauge           |
| Policy denials (by reason, tenant)                                               | Counter         |
| Checkpoint size and duration                                                     | Histogram       |
| Postgres connection pool utilization (per replica)                               | Gauge           |
| Redis memory usage and eviction rate                                             | Gauge + Counter |
| mTLS handshake latency (gateway-to-pod)                                          | Histogram       |
| Credential lease assignments (by provider, pool, source)                         | Counter         |
| Credential rotations (by reason: rate_limit, auth_expired, provider_unavailable) | Counter         |
| Credential pool utilization (active leases / total credentials, by pool)         | Gauge           |
| Credential pool health (credentials in cooldown, by pool)                        | Gauge           |
| Credential lease duration                                                        | Histogram       |

### 16.2 Key Latency Breakpoints

Instrument four timestamps per session:

1. Pod claimed
2. Workspace prep done
3. Session ready
4. First event/token emitted

This lets operators identify whether bottlenecks are in pod allocation, file upload, session start, or the agent runtime itself.

### 16.3 Distributed Tracing

**Mandatory:** OpenTelemetry trace ID propagation through the entire delegation tree.

**Trace context flows through:**

- Client → Gateway (HTTP headers)
- Gateway → Pod (gRPC metadata)
- Pod → Gateway (delegation tool calls carry parent trace context)
- Gateway → Child Pod (inherited trace context)
- Gateway → External MCP tools (HTTP headers)

**Span boundaries (instrumented):**

| Span                         | Component                             |
| ---------------------------- | ------------------------------------- |
| `session.create`             | Gateway                               |
| `session.claim_pod`          | Controller                            |
| `session.upload`             | Gateway + Pod                         |
| `session.finalize_workspace` | Pod                                   |
| `session.run_setup`          | Pod                                   |
| `session.start`              | Pod                                   |
| `session.prompt`             | Gateway + Pod (per prompt)            |
| `session.tool_call`          | Pod (per tool invocation)             |
| `delegation.spawn_child`     | Gateway                               |
| `delegation.await_child`     | Gateway + Parent Pod                  |
| `delegation.export_files`    | Gateway + Parent Pod                  |
| `mcp.external_tool_call`     | Gateway connector                     |
| `mcp.elicitation`            | Full chain (each hop is a child span) |
| `session.checkpoint`         | Gateway + Pod                         |
| `session.seal_and_export`    | Gateway + Pod                         |

**Sampling and backend:** Head-based sampling at a default rate of 10% for normal operations (configurable via `global.traceSamplingRate` Helm value). 100% sampling is applied for errors (any span with error status), slow requests (session creation exceeding P99 latency), and delegation trees (all spans in a tree are sampled if the root is sampled, preserving trace completeness). The trace pipeline uses OpenTelemetry Collector; the platform emits OTLP traces and the collector handles sampling, batching, and export. The backend is deployer-configured — Jaeger, Tempo, Zipkin, or cloud-native options (Cloud Trace, X-Ray) are all supported. In dev mode, 100% sampling is enabled with a local Jaeger instance (or stdout exporter for `make run`).

**Error codes:** Structured error taxonomy with categories:

- `TRANSIENT` — retryable (pod crash, network timeout)
- `PERMANENT` — not retryable (invalid workspace, policy denial)
- `POLICY` — denied by policy engine (quota exceeded, unauthorized runtime)
- `UPSTREAM` — external dependency failure (MCP tool error, auth failure)

### 16.4 Logging

- Structured JSON logs from gateway, token service, pool controller, and runtime adapter
- Correlation fields in every log line: `session_id`, `tenant_id`, `trace_id`, `span_id`
- Setup command stdout/stderr captured and stored in EventStore
- Audit events for all policy decisions
- Error events include structured error codes (TRANSIENT/PERMANENT/POLICY/UPSTREAM)
- **Credential-sensitive RPCs** (`AssignCredentials`, `RotateCredentials`) are excluded from payload-level logging, gRPC access logs, and OTel trace span attributes. Only RPC name, lease ID, provider type, and outcome are recorded.

**Log retention and EventStore management.** EventStore tables (audit events, session logs, stream cursors) are partitioned by time using native Postgres range partitioning. A background job drops partitions beyond the retention window: 90 days for audit events, 30 days for session logs, 7 days for stream cursors. Estimated volume is ~10 KB per session for audit events and ~50 KB for logs; at 10K sessions/day this is ~600 MB/day before retention cleanup. Deployers should configure an external log aggregation stack (ELK, Loki, CloudWatch, etc.) for long-term retention beyond the Postgres window.

### 16.5 Alerting Rules and SLOs

**Critical alerts (page):**

| Alert                      | Condition                                      | Severity |
| -------------------------- | ---------------------------------------------- | -------- |
| `WarmPoolExhausted`        | Available warm pods = 0 for any pool for > 60s | Critical |
| `PostgresReplicationLag`   | Sync replica lag > 1s for > 30s                | Critical |
| `GatewayNoHealthyReplicas` | Healthy gateway replicas < 2 for > 30s         | Critical |
| `SessionStoreUnavailable`  | Postgres primary unreachable for > 15s         | Critical |

**Warning alerts:**

| Alert                      | Condition                                                                         | Severity |
| -------------------------- | --------------------------------------------------------------------------------- | -------- |
| `WarmPoolLow`              | Available warm pods < 25% of `minWarm` for any pool                               | Warning  |
| `RedisMemoryHigh`          | Redis memory > 80% of maxmemory                                                   | Warning  |
| `CredentialPoolLow`        | Available credentials < 20% of pool size                                          | Warning  |
| `GatewayActiveStreamsHigh` | Active streams per replica > 80% of configured max                                | Warning  |
| `ArtifactGCBacklog`        | Expired artifacts pending cleanup > 1000                                          | Warning  |
| `RateLimitDegraded`        | Rate limiting in fail-open mode (per Sec-H5)                                      | Warning  |
| `CertExpiryImminent`       | mTLS cert expiry < 1h (should auto-renew, so this indicates cert-manager failure) | Warning  |

**SLO targets (operator-configurable baselines):**

| SLO                           | Target    | Measurement                                             |
| ----------------------------- | --------- | ------------------------------------------------------- |
| Session creation success rate | 99.5%     | Successful session starts / total attempts (30d window) |
| Time to first token           | P95 < 10s | From session start request to first streaming event     |
| Session availability          | 99.9%     | Uptime of sessions not in retry/recovery state          |
| Gateway availability          | 99.95%    | Healthy replicas serving requests                       |

---

## 17. Deployment Topology

### 17.1 Kubernetes Resources

| Component               | K8s Resource                              | Notes                                                                                                                                                      |
| ----------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Gateway                 | Deployment + Service + Ingress            | HPA, PDB, multi-zone, topology spread                                                                                                                      |
| Token/Connector Service | Deployment + Service + PDB                | 2+ replicas, stateless; separate SA with KMS access; PDB `minAvailable: 1`                                                                                 |
| Warm Pool Controller    | Deployment (2+ replicas, leader election) | Manages pod lifecycle via `kubernetes-sigs/agent-sandbox` CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`) |
| PoolScalingController   | Deployment (2+ replicas, leader election) | Reconciles pool config from Postgres into CRDs; manages scaling intelligence |
| Agent Pods              | Pods owned by `Sandbox` CRD              | RuntimeClass per pool; preStop checkpoint hook for active pods; optional PDB per pool on warm (idle) pods to enforce `minWarm` during voluntary disruption |
| Postgres                | StatefulSet or managed service            | HA: primary + sync replica, PgBouncer required                                                                                                             |
| Redis                   | StatefulSet or managed service            | HA: Sentinel (3 nodes), TLS + AUTH required                                                                                                                |
| MinIO                   | StatefulSet or managed service            | Artifact/checkpoint storage                                                                                                                                |

### 17.2 Namespace Layout

```
lenny-system/         # Gateway, token service, controller, stores
lenny-agents/         # Agent pods (gVisor/runc isolation boundary)
lenny-agents-kata/    # Kata pods (separate node pool with dedicated hardware)
```

**Pod Security Standards:** The `lenny-agents` and `lenny-agents-kata` namespaces apply Restricted PSS in **`warn` + `audit`** mode only — not `enforce`. Restricted PSS `enforce` mode is unsuitable because its `seccompType: RuntimeDefault` requirement is a no-op under gVisor (gVisor intercepts syscalls in userspace, making the host seccomp profile meaningless) and conflicts with some Kata device plugins that require relaxed `allowPrivilegeEscalation` constraints. In `enforce` mode, non-compliant pods are silently rejected by the API server, which would cause warm pool deadlock: the controller observes a missing pod, recreates it, and the replacement is rejected again in a tight loop.

Instead, fine-grained pod security enforcement uses **OPA/Gatekeeper or Kyverno** policies that are **RuntimeClass-aware** — applying appropriate constraints per isolation profile rather than blanket Restricted PSS. For example, gVisor pods skip the seccomp profile check while still requiring non-root, all-caps-dropped, and read-only rootfs (the controls listed in Section 13.1). Kata pods permit the specific privilege escalation paths needed by their device plugins but enforce all other Restricted constraints. This approach preserves the same security properties (non-root UID, all capabilities dropped, read-only root filesystem, gateway-mediated file delivery) via admission policy controllers rather than the built-in PSS enforce mode. Namespace labels are set as follows:

```
pod-security.kubernetes.io/warn: restricted
pod-security.kubernetes.io/audit: restricted
```

**Node isolation:** Kata (`microvm`) pods **must** run on dedicated node pools and **must** use hard scheduling constraints — not merely taints/tolerations — to guarantee they never share nodes with `standard` (runc) pods. A kernel compromise via an runc escape on a shared node would put co-located Kata pods at risk. The following controls are required:

1. **RuntimeClass `nodeSelector`:** The `kata-microvm` RuntimeClass definition **must** include `scheduling.nodeSelector` (e.g., `lenny.dev/node-pool: kata`). Any pod that references this RuntimeClass is automatically constrained to matching nodes at admission time, with no additional pod-level configuration needed.
2. **Hard node affinity:** As a defense-in-depth measure, the controller **must** inject a `requiredDuringSchedulingIgnoredDuringExecution` node affinity rule on every Kata pod, matching the same `lenny.dev/node-pool: kata` label. This ensures scheduling fails rather than falling back to an unsuitable node.
3. **Dedicated-node taint:** Kata node pools **must** carry the taint `lenny.dev/isolation=kata:NoSchedule`. Only pods with the corresponding toleration (added automatically by the RuntimeClass or controller) can schedule onto these nodes, preventing non-Kata workloads from landing on Kata-dedicated hardware.

### 17.3 Disaster Recovery

**RPO/RTO targets:**

| Component                        | RPO                                                                                  | RTO                                                                  |
| -------------------------------- | ------------------------------------------------------------------------------------ | -------------------------------------------------------------------- |
| Postgres (session state, tokens) | 0 (sync replication)                                                                 | < 30s (auto failover)                                                |
| Redis (cache, leases)            | Ephemeral — rebuild from Postgres                                                    | < 15s (Sentinel failover)                                            |
| MinIO (artifacts, checkpoints)   | Near-zero (erasure coding + site replication) or last backup (daily) for single-site | < 30s (surviving nodes serve reads); < 5 min (full node replacement) |

**Cross-zone requirements:**

- Postgres: primary and sync replica in different availability zones
- Redis: Sentinel nodes spread across zones
- Gateway: replicas spread via topology spread constraints
- Agent pods: spread via pool-level topology constraints (see Section 5.2)

**Backup schedule:**

- Postgres: continuous WAL archival + daily base backups to object storage
- MinIO: daily bucket replication or backup
- Restore testing recommended monthly (configurable by deployer via CronJob schedule) via `lenny-restore-test` CronJob that: (1) creates a temporary Postgres instance from the latest base backup + WAL, (2) verifies schema integrity and row counts against the primary, (3) runs a smoke query (e.g., list recent sessions), (4) records elapsed restore time and emits `lenny_restore_test_success` / `lenny_restore_test_duration_seconds` metrics, and (5) tears down the test instance. Alert if measured RTO exceeds targets (< 30s Postgres, < 5min MinIO). MinIO restore is validated in the same job via a test bucket restore and object checksum comparison.

**Zone failure blast radius:** Loss of one zone causes:

- Gateway: surviving replicas absorb traffic (PDB ensures minimum availability)
- Postgres: automatic failover to sync replica in another zone
- Agent pods: sessions on lost pods enter retry flow; warm pods in surviving zones serve new requests
- No data loss for committed transactions

### 17.4 Local Development Mode (`lenny-dev`)

For development, testing, and runtime adapter authoring, Lenny provides a **two-tier local development mode** that runs without Kubernetes:

#### Tier 1: `make run` — Zero-dependency local mode

```
make run   # Starts: gateway + controller-sim + single agent container (single binary)
```

A single binary entry point that embeds all required state:

- **Embedded SQLite** replaces Postgres for session and metadata storage
- **In-memory caches** replace Redis for pub/sub and ephemeral state
- **Local filesystem directory** (`./lenny-data/`) replaces MinIO for artifact storage
- Gateway, controller-sim, and a single agent container run as goroutines in one process

No Postgres, Redis, MinIO, or Docker required. Suitable for:

- Runtime adapter authors testing their adapter against the gateway contract
- First-time contributors getting oriented with the codebase
- Quick demos and evaluations
- Agent binary authors iterating on their binary locally

#### Tier 2: `docker compose up` — Full local stack

```
docker compose up   # Starts: gateway, controller-sim, single agent pod, Postgres, Redis, MinIO
```

Production-like local environment with real infrastructure dependencies:

- Gateway: single replica, no HPA, no mTLS (plain HTTP)
- Controller simulator: manages a single "pod" (Docker container) instead of CRDs
- Stores: real Postgres + Redis (lightweight containers)
- MinIO: single container for artifact storage
- Agent pod: single Docker container with runtime adapter + agent binary

Suitable for:

- Lenny core developers iterating on gateway/controller logic
- Integration testing against real storage backends
- CI integration tests
- Production-like local environment validation

#### Observability in dev mode (OSS-L2)

Tier 2 (`docker compose up`) includes optional observability containers: Prometheus (metrics scraping), Grafana (pre-built Lenny dashboard), and Jaeger (distributed tracing). Enable with `docker compose --profile observability up`. Tier 1 (`make run`) outputs traces to stdout and exposes Prometheus metrics on `:9090/metrics`.

#### Zero-credential mode

In both tiers, the gateway can operate without LLM provider credentials by using a **built-in echo/mock agent runtime** that does not require an LLM provider. The echo runtime replays deterministic responses, allowing contributors to test platform mechanics (session lifecycle, workspace materialization, delegation flows) without providing any API keys. This is the default runtime in Tier 1 and can be selected explicitly in Tier 2 via `LENNY_AGENT_RUNTIME=echo`.

#### Dev mode guard rails (Sec-C2)

Dev mode relaxes security defaults (TLS, JWT signing) for local convenience, but hard guard rails prevent accidental use outside development:

1. **Hard startup assertion:** The gateway **refuses to start** with TLS disabled unless the environment variable `LENNY_DEV_MODE=true` is explicitly set. Any other value, or absence of the variable, causes an immediate fatal error at startup. This ensures a misconfigured staging or production deployment cannot silently run without encryption.
2. **Prominent startup warning:** When `LENNY_DEV_MODE=true` is set, the gateway logs at `WARN` level on every startup: `"WARNING: TLS disabled — dev mode active. Do not use in production."` The warning is repeated every 60 seconds while the process is running.
3. **Unified security-relaxation gate:** The `LENNY_DEV_MODE` flag is the single gate for all security relaxations in dev mode, including TLS bypass, JWT signing bypass (see Sec-H1), and any future relaxations. No individual security feature can be disabled independently without this flag.

For adapter authors who need to test TLS behavior, setting `LENNY_DEV_TLS=true` (requires `LENNY_DEV_MODE=true`) enables self-signed mTLS certificates that are auto-generated on first run, allowing testing of certificate validation, rotation, and error handling without a full cert-manager setup.

#### Smoke test

Both dev mode tiers include a built-in smoke test: `make test-smoke` (Tier 1) or `docker compose run smoke-test` (Tier 2) creates a session with the echo runtime, sends a prompt, verifies a response, and exits. This validates the entire pipeline (gateway, controller-sim, runtime adapter, agent binary) in under 10 seconds.

### 17.5 Cloud Portability

The design avoids baking in cloud-specific assumptions:

- Storage backends are pluggable
- Network policies are standard Kubernetes
- RuntimeClass works with any conformant runtime
- No cloud-specific CRDs required

### 17.6 Packaging and Installation

**Helm chart** is the primary installation mechanism. The chart packages all Lenny components: gateway, token service, warm pool controller, CRD definitions, RBAC, NetworkPolicies, and cert-manager resources.

Key Helm values:

- `global.devMode` — enables `LENNY_DEV_MODE` for local development
- `gateway.replicas` — gateway replica count
- `pools` — array of warm pool configurations (runtime, size, resource limits)
- `postgres.connectionString` — Postgres DSN
- `redis.connectionString` — Redis DSN
- `minio.endpoint` — object storage endpoint

CRDs are installed via the chart but can be managed separately for GitOps workflows (`helm install --skip-crds` combined with external CRD management).

**Local dev:** A `docker-compose.yml` is provided as described in Section 17.4.

**GitOps:** The Helm chart supports `helm template` rendering for ArgoCD/Flux integration.

### 17.7 Operational Runbooks

Lenny must ship with operational runbooks for key failure scenarios as part of the documentation deliverables. The minimum required set:

- **Warm pool exhaustion** — diagnosis, emergency scaling, root cause analysis
- **Postgres failover** — verification, connection pool drain, health checks
- **Redis failure and recovery** — quota reconciliation, cache rebuild
- **Credential pool exhaustion** — diagnosis, emergency key addition
- **Gateway replica failure** — stream reconnection, session recovery
- **cert-manager outage** — warm pool impact, manual certificate issuance
- **MinIO failure** — artifact retrieval degradation, restore procedure

Each runbook references the relevant alerts defined in Section 16.5. Runbooks are version-controlled alongside the platform code in `docs/runbooks/`.

> **Note (OSS-L3):** The Lenny documentation targets three operator skill tiers: (1) **Developer** — uses `make run` or docker-compose, no K8s knowledge needed. (2) **Platform operator** — deploys via Helm, manages pools and scaling, follows runbooks for common issues. (3) **Cluster admin** — configures RuntimeClasses, node pools, network policies, and handles node drain/checkpoint integration. The Helm chart and documentation are structured to serve all three tiers, with progressive complexity.

### 17.5 Operational Defaults — Quick Reference

All tunable defaults collected in one place for operator convenience.

| Setting                     | Default              | Reference |
| --------------------------- | -------------------- | --------- |
| Artifact retention TTL      | 7 days               | §12.5     |
| Checkpoint retention        | Latest 2 per session | §12.5     |
| GC cycle interval           | 15 min               | §12.5     |
| Max session age             | 7200 s (2 h)         | §11.3     |
| Max idle time               | 600 s                | §11.3     |
| Max resume window           | 900 s                | §11.3     |
| Rate limit fail-open window | 60 s                 | §12.4     |
| Audit event retention       | 90 days              | §16.4     |
| Session log retention       | 30 days              | §16.4     |
| Pod cert TTL                | 4 h                  | §10.3     |

All values are overridable via Helm values or the corresponding CRD field. See each referenced section for detailed semantics.

---

## 18. Build Sequence

| Phase | Components | Milestone |
| ----- | ---------- | --------- |
| 1 | Core types: `Runtime` (unified, with `labels`), `type` field, `capabilities` (including `interaction: one_shot \| multi_turn`, `injection`), `executionMode`, `allowedExternalEndpoints` on delegation lease, `input_required` task state, messages array in TaskRecord, `suspended` session state. Agent-sandbox CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`). `Connector` resource with `labels`. `environmentId` nullable field on billing event schema. `crossEnvironmentDelegation` structured form schema slot. | Foundation |
| 2 | Replace adapter binary protocol: unified `{type:"message"}` (no separate `prompt`), `slotId` field for concurrent-workspace multiplexing, multi-server MCP adapter + lifecycle channel, adapter manifest written before binary spawns. Publish as runtime adapter specification. Includes `make run` local dev mode with embedded stores and echo runtime. | Can start an agent session; contributors can run locally |
| 3 | PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement. `taskPolicy.cleanupCommands`. | Pods stay warm; delegation policy works |

> **Note:** Digest-pinned images from a private registry are required from Phase 3 onward. Full image signing and attestation verification (Sigstore/cosign + admission controller) is Phase 14.

| Phase | Components | Milestone |
| ----- | ---------- | --------- |
| 4 | Session manager + session lifecycle + REST API | Full create → upload → attach → complete flow |
| 4.5 | **Admin API foundation**: runtimes, pools, connectors, delegation policies, tenant management, external adapters registry. Gateway loads config from Postgres. Capability inference from MCP annotations at connector registration. | All operational config API-managed |
| 5 | Gateway `ExternalAdapterRegistry`. MCP adapter, Completions adapter, Open Responses adapter all active. `list_runtimes`, `GET /v1/runtimes`, `GET /v1/models` with identity-aware filtering. `type: mcp` runtime endpoints at `/mcp/runtimes/{name}`. Tenant RBAC config API. `noEnvironmentPolicy` enforcement. | Clients can create and use sessions via REST, MCP, and Completions API |

> **Note:** Phase 5 sessions use the zero-credential echo runtime (Phase 2) for end-to-end testing. Real LLM credentials require Phase 11. The gateway supports sessions without credentials when the Runtime does not declare `supportedProviders`.

| Phase | Components | Milestone |
| ----- | ---------- | --------- |
| 6 | Interactive session model (streaming, messages, reconnect with event replay) | Full interactive sessions work |
| 7 | Policy engine (rate limits, auth, budgets, tenant_id) | Production-grade admission |
| 8 | Checkpoint/resume + artifact seal-and-export | Sessions survive pod failure; artifacts retrievable |
| 9 | `lenny/delegate_task` handles internal and external targets. `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree`, `lenny/send_to_child` (active). `lenny/discover_agents` with policy scoping. Multi-turn fully operational. | Parent → child task flow with multi-turn |
| 10 | `agentInterface` in discovery. Adapter manifest includes summaries. MCP fabric (virtual child interfaces, elicitation chain with provenance). | Recursive delegation with MCP semantics |
| 11 | Credential leasing (CredentialProvider interface, pool management, lease assignment, rotation) | Runtimes can access LLM providers |
| 12 | `type: mcp` runtime support. Concurrent execution modes including `slotId` multiplexing for workspace variant. Token/Connector service (separate deployment, KMS, OAuth flows, credential pools). | External MCP tool auth + concurrent mode |
| 13 | Audit logging, OpenTelemetry tracing, observability | Operational readiness |
| 14 | Hardening (gVisor/Kata, NetworkPolicy manifests, image signing, egress lockdown) | Security profiles |
| 15 | Environment resource: tag-based selectors, member RBAC, `mcpRuntimeFilters` with capability model, `connectorSelector`, cross-environment delegation enforcement, billing rollup endpoint, membership analytics endpoints, explicit environment endpoints across all adapters. | Full RBAC and environment support |
| 16 | Experiment primitives, PoolScalingController experiment integration. | A/B testing infrastructure |
| 17 | Memory (`MemoryStore` + platform tools), semantic caching, guardrail interceptor hooks, eval hooks. Production-grade docker-compose, documentation, community guides. | Full community onboarding |

> **Open-source readiness:** Lenny is designed as an open-source project. Contribution guidelines (`CONTRIBUTING.md`), governance model, and community communication channels will be established as part of Phase 2 (alongside the `make run` quick-start). The technical design prioritizes community extensibility through pluggable credential providers, a published runtime adapter contract, and a clear SDK boundary.

---

## 19. Resolved Decisions

These were open questions from the initial design, now resolved:

| #   | Question                         | Decision                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| --- | -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Checkpointing strategy           | Full snapshots with size cap. Keep latest 2 per session. Incrementals deferred.                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 2   | Agent binary packaging           | Sidecar container with local Unix socket. `shareProcessNamespace: false`. Lower barrier for third-party authors.                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| 3   | Multi-tenancy                    | `tenant_id` in all data models. Logical isolation via filtering. Namespace-level isolation deferred.                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| 4   | Controller framework             | `kubernetes-sigs/agent-sandbox` for pod lifecycle CRDs. kubebuilder (controller-runtime) for PoolScalingController and custom controllers.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 5   | Service mesh dependency          | cert-manager + manual mTLS. No Istio/Linkerd requirement (fewer deps for community adoption).                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| 6   | Default isolation                | gVisor (`sandboxed`) is the default. `runc` requires explicit deployer opt-in.                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 7   | Blob storage                     | MinIO. Never Postgres for blobs.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| 8   | Delegation file export structure | Source glob base path is stripped; files are rebased to child workspace root. Optional `destPrefix` prepends a path. Parent controls the slice; child sees clean root-relative structure. See Section 8.8.                                                                                                                                                                                                                                                                                                                                           |
| 9   | Inter-child data passing         | No first-class `pipe_artifacts` operation. Parents use the existing export→re-upload flow via `delegate_task` file exports. Simpler; avoids a new gateway primitive.                                                                                                                                                                                                                                                                                                                                                                                 |
| 10  | Setup command policy             | Allowlist is the recommended default for multi-tenant deployments; blocklist is a convenience guard (not a security boundary) for single-tenant scenarios. `shell: false` mode prevents metacharacter injection via direct exec. The real security boundary is the pod sandbox (gVisor/Kata, non-root UID, read-only root, network policy). See Section 7.5.                                                                                                                                                                                         |
| 11  | Billing/showback                 | Track per-session, per-token, and per-minute usage. Expose via REST API (`GET /v1/usage`). Filterable by tenant, user, runtime, and time window.                                                                                                                                                                                                                                                                                                                                                                                                     |
| 12  | Session forking                  | Not supported. The `fork_session` concept is dropped. Clients can derive a new session from a previous one by: (1) downloading the previous session's workspace snapshot via `GET /v1/sessions/{id}/workspace`, (2) creating a new session, (3) uploading the snapshot as an `uploadArchive` source in the new session's WorkspacePlan. The gateway also provides a convenience endpoint `POST /v1/sessions/{id}/derive` that performs steps 1-3 atomically — it creates a new session pre-populated with the previous session's workspace snapshot. |
| 13  | Lease extension                  | Supported via adapter↔gateway gRPC lifecycle (not an MCP tool — runtime is unaware). Triggered by the adapter when the LLM proxy rejects for budget exhaustion. `elicitation` mode (default): serialized per tree, one elicitation at a time, concurrent requests batched, cool-off window after approval, rejection is permanent for the tree. `auto` mode: each request handled independently, no elicitation. Extensions can never exceed deployer caps or the parent's own lease. See Section 8.6.                                                                                                                                                                                                                                                                                                           |

Each decision above is a summary; full Architecture Decision Records (ADRs) with context, alternatives considered, and consequences will be maintained in `docs/adr/` as separate documents following the MADR format, with this table serving as an index.

## 20. Open Questions

All open questions have been resolved. See Section 19 for decisions.

---

## 21. Planned / Post-V1

The following items are documented now so data models accommodate them. Implementation is deferred.

**21.1 A2A Full Support.** `ExternalProtocolAdapter` is the mechanism. A2A is implementation two. Inbound: gateway serves `POST /a2a/{runtimeName}/tasks` via `A2AAdapter`. Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`. A2A `input-required` state maps directly to Lenny's `input_required` task state. `agentInterface` auto-generates A2A agent cards. Per-agent A2A endpoints (not aggregated) at `/a2a/runtimes/{name}`. `allowedExternalEndpoints` slot on delegation lease schema exists from v1.

**21.2 A2A Intra-Pod Support.** Adapter additionally serves per-agent A2A endpoints intra-pod. Adapter manifest gains A2A base URL. All A2A traffic proxied through gateway. Runtime authors opt in explicitly.

**21.3 Agent Protocol (AP) Support.** AP defines `POST /ap/v1/agent/tasks` and step execution. Third `ExternalProtocolAdapter` implementation.

**21.4 Future Conversational Patterns.** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all of these without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

**21.5 Environment Management UI.** Full web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. The `?dryRun=true` parameter on `PUT /v1/admin/environments` is the preview mechanism and ships in v1.

**21.6 Environment Resource — Post-V1 Deferred Items.** Cross-environment delegation richer controls. Runtime-level cross-environment exceptions at granularity beyond the structured form. Integration with experiment-scoped delegation boundaries.

**21.7 Multi-Cluster Federation.** Session IDs must be globally unique. Storage interfaces must not assume single-cluster topology. Connectors registered in one cluster must be expressible by reference in multi-cluster scenarios.

**21.8 UI and CLI.** Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic.

---

## 22. Explicit Non-Decisions

**22.1 No Model B Runtime Deployment.** No mechanism for packaging a graph definition as a new registered runtime. Users register derived runtimes via admin API. (Context: LangGraph deployment discussion — Model A chosen: one generic LangGraph runtime, many deployed graphs via derived runtimes.)

**22.2 No Built-In Eval Logic.** Lenny provides hooks and storage. No LLM-as-judge or hallucination detection.

**22.3 No Built-In Guardrail Logic.** Lenny provides the `RequestInterceptor` hook (Section 4.8). No content classifiers or prompt injection detection.

**22.4 No Built-In Memory Extraction.** Lenny provides the `MemoryStore` interface and tools (Section 9.4). Runtimes decide what to write.

**22.5 No Direct External Connector Access.** Connectors are session-internal in v1. External clients do not call connectors directly. Whether to add this later is an independent product decision; the data model accommodates it without requiring a redesign.

**22.6 Hooks-and-Defaults Design Principle.** Every cross-cutting AI capability (memory, caching, guardrails, evaluation, routing) follows the same pattern: defined as an interface with a sensible default implementation, disabled unless explicitly enabled by the deployer, fully replaceable. Lenny never implements AI-specific logic (eval scoring, memory extraction, content classification) — that belongs to specialized tools in the ecosystem.

---

## 23. Competitive Landscape

| Project | Why It Matters for Lenny |
|---|---|
| `kubernetes-sigs/agent-sandbox` | Near-identical pod lifecycle primitive being standardized upstream — adopted as Lenny's infrastructure layer (Section 4.6) |
| E2B | Market-leading AI sandbox with Firecracker microVMs, ~150ms boot times, self-hosting options. Primary comparison point. |
| Fly.io Sprites | Jan 2026 direct competitor with Firecracker + checkpoint/restore in ~300ms. |
| Google A2A Protocol | Agent-to-agent protocol now under AAIF governance alongside MCP. Addressed via `ExternalAdapterRegistry`, `publishedMetadata`, and `allowedExternalEndpoints` (Sections 15, 5.1, 8.3) |
| Daytona | Sub-90ms cold starts, desktop environments for computer-use agents. |
| LangSmith Deployment | Now has A2A + MCP + RemoteGraph. Gap with Lenny's delegation model is narrower than originally acknowledged. |
| Amazon Bedrock AgentCore Memory | Short-term + long-term memory for agents. Motivated the `MemoryStore` interface (Section 9.4). |
