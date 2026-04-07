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
- Scale the gateway horizontally with externalized session state, designed to reach Tier 3 (10,000 concurrent sessions) with horizontal scaling only (Section 16.5)
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
- HPA on CPU, memory, active sessions, open streams, active LLM proxy connections (`active sessions` is sourced from the gateway's in-memory Prometheus gauge `lenny_gateway_active_sessions`, surfaced to the HPA via Prometheus Adapter as described in Section 10.1)
- Per-tier HPA configuration (min/max replicas, target utilization, scale-down policy) is in Section 17.8.
- Sticky routing is an optimization, not a correctness requirement
- PodDisruptionBudget to limit simultaneous disruptions

**Key invariant:** A client can land on any gateway replica. Session state is always in durable stores.

#### Gateway Internal Subsystems

The gateway binary is internally partitioned into four subsystem boundaries. These are **not** separate services — they are Go interfaces within a single binary that enforce isolation at the concurrency and failure-domain level, so that a problem in one area (e.g., a slow upload) cannot starve or crash another (e.g., MCP streaming).

**Subsystems:**

1. **Stream Proxy** — MCP streaming, session attachment, event relay, and client reconnection handling.
2. **Upload Handler** — File upload proxying, payload validation, staging to the Artifact Store, and archive extraction.
3. **MCP Fabric** — Delegation orchestration, virtual child MCP interfaces, and elicitation chain management.
4. **LLM Proxy** — Credential-injecting reverse proxy for LLM provider traffic (Section 4.9). Validates lease tokens, injects real API keys, and forwards streaming requests to upstream LLM providers.

**Per-subsystem isolation guarantees:**

Each subsystem is defined as a Go interface with its own:

- **Goroutine pool / concurrency limits:** A saturated Upload Handler cannot consume goroutines needed by the Stream Proxy. Each subsystem has independently configured `maxConcurrent` and queue-depth settings. See Section 17.8 for per-tier recommended values.
- **Per-subsystem metrics:** Latency histograms, error rates, and queue depth are emitted per subsystem, enabling targeted alerting (e.g., upload p99 latency spike does not hide a stream proxy degradation).
- **Circuit breaker:** Each subsystem has its own circuit breaker (see Section 11.6). The Upload Handler can trip to half-open or open state — returning 503 for uploads — while the Stream Proxy and MCP Fabric continue serving normally. This is the primary mechanism for partial gateway degradation.

**Extraction triggers:**

These internal boundaries are designed so that any subsystem can be extracted to a dedicated service if scaling requires it. The following table defines the observable metrics and indicative threshold ranges for each subsystem. Threshold values marked as TBD are estimates; Phase 13.5 load testing is required to validate or revise them before Tier 3 rollout.

| Subsystem          | Key Metrics                                                                                                           | Indicative Extraction Threshold                                                                                                                                                                                                                                                                    | Failure Mode if Not Extracted                                                                               |
| ------------------ | --------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| **Stream Proxy**   | `lenny_stream_proxy_queue_depth`, `lenny_stream_proxy_goroutines`, `lenny_stream_proxy_p99_attach_latency_ms`         | Queue depth sustained >500 **or** p99 attach latency >800 ms under normal (non-burst) load for ≥5 min (TBD: validate in Phase 13.5)                                                                                                                                                                | Head-of-line blocking on session attach; reconnection storms amplify the problem                            |
| **Upload Handler** | `lenny_upload_handler_active_uploads`, `lenny_upload_handler_queue_depth`, `lenny_upload_handler_p99_latency_ms`      | Active uploads sustained >200 concurrent **or** HPA signals consistently driven by upload load rather than session load (TBD: validate in Phase 13.5)                                                                                                                                              | Upload bursts cause gateway over-provisioning that wastes resources on stream proxy replicas                |
| **MCP Fabric**     | `lenny_mcp_fabric_active_delegations`, `lenny_mcp_fabric_goroutines`, `lenny_mcp_fabric_p99_orchestration_latency_ms` | Active delegations sustained >1,000 concurrent **or** p99 orchestration latency >2,000 ms (TBD: validate in Phase 13.5)                                                                                                                                                                            | Deep delegation trees starve goroutine pool; GC pressure from delegation state affects Stream Proxy latency |
| **LLM Proxy**      | `lenny_llm_proxy_active_connections`, `lenny_llm_proxy_upstream_goroutines`, `lenny_llm_proxy_p99_ttfb_ms`            | Active upstream connections sustained >2,000 **or** `lenny_llm_proxy_active_connections` exceeds 60% of `maxConcurrent` across all replicas for ≥10 min and is growing faster than gateway HPA can respond **or** HPA signals dominated by LLM proxy connection load (TBD: validate in Phase 13.5) | Long-lived upstream streaming goroutines inflate heap and GC pause time for all subsystems                  |

**LLM Proxy-to-session ratio guidance:** When the ratio of `lenny_llm_proxy_active_connections` to `lenny_gateway_active_sessions` approaches 1:1 (i.e., nearly every active session has an open LLM proxy stream), the LLM Proxy subsystem is effectively as large as the Stream Proxy subsystem. At ratios above 0.5:1 sustained for more than 15 minutes, operators should begin planning extraction. Below 0.3:1, the combined binary is expected to remain sustainable at Tier 3 scale. Between 0.3:1 and 0.5:1, operators should monitor `lenny_gateway_gc_pause_p99_ms` for early signs of shared-process GC pressure.

**Shared-process GC pressure signal:** When any two subsystems simultaneously approach their indicative thresholds, the combined goroutine count and heap allocation rate can cause GC pauses that affect all subsystems. The metric `lenny_gateway_gc_pause_p99_ms` is emitted at the process level; a sustained value >50 ms is an additional signal that the process boundary is becoming a bottleneck regardless of per-subsystem queue depth.

All threshold values in this table are estimated from first-principles capacity reasoning. Phase 13.5 load testing must confirm or revise them. Operators should instrument these metrics from initial deployment so that baseline data is available when Tier 3 scaling is approached.

Until those triggers are hit, a single binary with internal boundaries is preferred for operational simplicity.

### 4.2 Session Manager

**Role:** Source of truth for all session and task metadata.

**Backed by:** Postgres (primary), Redis (hot routing cache, short-lived locks)

**Manages:**

- Session records (id, **tenant_id**, user_id, state, pool, pod assignment, cwd, generation, **schema_version**)
- Task records and parent/child lineage (task DAG)
- Retry counters and policy enforcement
- Resume eligibility and window
- Pod-to-session binding
- Delegation lease tracking

**Multi-tenancy:** `tenant_id` is carried on all session, task, quota, token, memory store, and credential pool store records. The **primary** tenant isolation mechanism is PostgreSQL Row-Level Security (RLS) policies tied to the database session role. Every tenant-scoped table has an RLS policy that filters rows using `current_setting('app.current_tenant', false)` — the `false` parameter causes a hard error if the setting is unset, preventing silent fallback to an empty string. Every database call is wrapped in an explicit transaction that begins with `SET LOCAL app.current_tenant = '<tenant_id>'`. `SET LOCAL` is transaction-scoped: the value is automatically cleared on `COMMIT` or `ROLLBACK`, which is essential under PgBouncer transaction-mode pooling where bare `SET` could leak tenant context to subsequent requests sharing the same pooled connection. As defense-in-depth, PgBouncer's `connect_query` is configured to set a sentinel value (`SET app.current_tenant = '__unset__'`) on every fresh connection checkout; any query that reaches RLS evaluation with `__unset__` will be rejected by the policy. Application-layer `tenant_id` filtering in queries remains as additional defense-in-depth, but a missing WHERE clause in application code **cannot** break isolation — the database enforces it regardless. An integration test must verify tenant isolation at startup by confirming that a query without `SET LOCAL` is rejected and that cross-tenant reads return zero rows. Namespace-level or cluster-level isolation is a future goal. All quotas, rate limits, and usage reports can be scoped by tenant.

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

**Checkpoint Atomicity:** A checkpoint is an atomic unit comprising a workspace snapshot (tar of `/workspace/current`), a session file snapshot (copy of `/sessions/` contents), and a checkpoint metadata record (generation, timestamp, pod state). If either snapshot fails, the entire checkpoint is discarded — partial checkpoints are never stored. The metadata record in Postgres references both artifacts and is written only after both are successfully uploaded to MinIO.

**Checkpoint quiescence strategy by tier:**

- **Full-tier runtimes (lifecycle channel):** Cooperative `checkpoint_request`/`checkpoint_ready` handshake via the lifecycle channel (see Section 4.7) is the only mechanism that produces consistent checkpoints under all isolation profiles (runc, gVisor, Kata). The adapter sends `checkpoint_request`, the runtime quiesces and replies `checkpoint_ready`, snapshots are captured, and the adapter resumes the runtime with `checkpoint_complete`.
- **Minimum / Standard-tier runtimes (no lifecycle channel):** Best-effort snapshot without pausing. Checkpoint tagged `consistency: best-effort`. Deployers should expect minor workspace inconsistencies on resume. Consistent checkpoints require Full-tier.
- **Embedded adapter mode only:** `SIGSTOP`/`SIGCONT` may be used when the adapter is embedded in the same process (not the default sidecar model). This is **not available** in the default sidecar deployment because `shareProcessNamespace: false` prevents cross-container signaling. This is **not supported** under gVisor or Kata — signal-based checkpointing under sandboxed runtimes is unsupported; use the lifecycle channel instead.

**Checkpoint timeout and recovery:** All checkpoint paths enforce a 60-second checkpoint timeout measured from the initial quiescence request to completion. For the **Full-tier lifecycle channel path**: if the runtime sends `checkpoint_ready` but does not receive `checkpoint_complete` within 60 seconds, it MUST autonomously resume normal operation and log a `checkpoint_timeout` warning. This protects against adapter crashes or network partitions during the snapshot phase. For the **embedded adapter path**: the adapter MUST issue `SIGCONT` in a deferred cleanup handler (Go `defer` or equivalent) so that `SIGCONT` is sent on all exit paths — including panics and crashes. Additionally, a 60-second watchdog timer starts when `SIGSTOP` is sent; if the checkpoint does not complete within that window, `SIGCONT` is sent unconditionally, the checkpoint is marked failed, and the adapter sets a `checkpoint_stuck` flag on the liveness endpoint (see below).

**Checkpoint abort cleanup:** When a checkpoint is aborted — whether by watchdog timeout, adapter crash, or any other failure — the adapter MUST delete any partially uploaded MinIO objects for that checkpoint attempt. For multipart uploads, the adapter calls the S3 `AbortMultipartUpload` API; for single-part uploads that may have completed server-side, the adapter issues a `DeleteObject` call for the checkpoint's object key. The checkpoint metadata record in Postgres is never written for an aborted checkpoint (per the atomicity guarantee above), so no Postgres cleanup is needed. This cleanup is best-effort: if the delete call itself fails (e.g., MinIO is unreachable), the orphaned partial object is harmless (it cannot be mistaken for a valid checkpoint because no metadata references it) but consumes storage. The `lenny_checkpoint_orphaned_objects_total` metric (counter, labeled by `pool` and `trigger`) is incremented when cleanup fails, enabling operators to run periodic garbage collection sweeps against MinIO for unreferenced checkpoint objects. Any Go `recover()` call wrapping the checkpoint goroutine MUST either re-panic or explicitly set the liveness endpoint to unhealthy if `SIGCONT` cannot be confirmed sent — silently recovering a checkpoint goroutine panic without signaling unhealthiness is forbidden, as it leaves the agent process permanently frozen while the liveness probe continues to pass.

**Embedded adapter liveness integration:** In embedded adapter mode, the `/healthz` liveness endpoint MUST incorporate checkpoint state. The adapter maintains a shared atomic flag (`checkpointStuck`) that is set by the watchdog timer when a `SIGSTOP` has been held for more than `watchdogTimeoutSeconds` (default 60s) without checkpoint completion. When `checkpointStuck` is set, the `/healthz` endpoint returns HTTP 503 with `{"status": "unhealthy", "reason": "checkpoint_stuck"}`, causing the liveness probe to fail and Kubernetes to restart the pod. This closes the deadlock window where the adapter's HTTP server remains healthy while the agent process is permanently frozen — the liveness probe now actively detects stuck checkpoints rather than relying solely on adapter process crashes. The `checkpointStuck` flag is cleared on successful checkpoint completion or after `SIGCONT` is confirmed sent. If the embedded adapter process itself crashes entirely while the agent is SIGSTOPped (not a recovered goroutine panic but a full process exit), the agent process remains stopped; the pod's liveness probe will fail (since the adapter process is gone), Kubernetes will restart the pod, and the session resumes from the last successful checkpoint per Section 7.2.

**Checkpoint duration SLO and workspace size impact:** Checkpoint duration is dominated by workspace tar creation and MinIO upload. Target SLO: P95 checkpoint duration < 2 seconds for workspaces ≤ 100MB. Expected scaling: ~1 second per 100MB on typical node-local SSD with gigabit-class MinIO connectivity; 500MB workspaces may reach 5-10 seconds. For **Minimum/Standard-tier** (best-effort, no pause): duration affects only checkpoint freshness, not agent responsiveness. For **Full-tier** (cooperative handshake): the runtime is quiesced during the snapshot phase, so checkpoint duration directly impacts agent pause time — deployers with large workspaces should monitor the `lenny_checkpoint_duration_seconds` histogram and consider workspace hygiene (`.lennyignore` excludes, smaller working sets). For **embedded adapter mode** (SIGSTOP): the agent is fully frozen for the entire checkpoint duration, making this the most latency-sensitive path. The Phase 2 startup benchmark harness (Section 18) must include a **checkpoint duration benchmark** that measures end-to-end checkpoint time across workspace sizes (10MB, 100MB, 500MB) and storage backends, and validates the < 2s SLO for ≤ 100MB workspaces. Incremental checkpoints (diffing against the previous snapshot) are deferred but noted as the primary mitigation if the SLO cannot be met at larger workspace sizes.

**Checkpoint freshness SLO:** Every active session MUST have a successful checkpoint recorded within the last `periodicCheckpointIntervalSeconds` (default: 600s / 10 minutes). The gateway enforces this by scheduling periodic checkpoints for active sessions. The `lenny_session_last_checkpoint_age_seconds` gauge (per session, reported by the gateway) tracks the elapsed time since each session's last successful checkpoint. A `CheckpointStale` warning alert (Section 16.5) fires when any active session exceeds this window without a successful checkpoint. This SLO bounds the maximum workspace state that can be lost if an eviction checkpoint fails — any session that has met the SLO loses at most 10 minutes of workspace changes, not unbounded progress.

**Periodic checkpoint scheduling:** The gateway schedules periodic checkpoints for all active sessions using a configurable interval (`periodicCheckpointIntervalSeconds`, default: 600s). Periodic checkpoints are best-effort (see non-eviction path below) — they do not block agent progress. Failed periodic checkpoints are logged and retried on the next interval; they do not affect session state. The gateway tracks `last_successful_checkpoint_at` on the session record in Postgres, updated on every successful checkpoint regardless of trigger (periodic, eviction, pre-drain).

**Checkpoint storage failure:** MinIO uploads during checkpoint are retried with exponential backoff. The retry budget differs by checkpoint trigger:

- **Non-eviction checkpoints** (periodic scheduled checkpoints, pre-scale-down checkpoints): Retried with exponential backoff (initial 200ms, factor 2x, up to ~5 seconds total). If all upload retries fail, the adapter resumes the agent immediately — via `checkpoint_complete` on the lifecycle channel (Full-tier), `SIGCONT` (embedded adapter), or no-op (Minimum/Standard-tier best-effort). The failed checkpoint is discarded. The adapter logs the failure and increments the `lenny_checkpoint_storage_failure_total` metric (counter, labeled by `pool`, `tier`, and `trigger`). The next scheduled checkpoint retries normally.
- **Eviction checkpoints** (preStop hook): Retried with exponential backoff (initial 500ms, factor 2x, capped at 5s per attempt, up to **30 seconds total**). The eviction retry budget is deliberately longer than non-eviction checkpoints because (a) the pod is terminating regardless — there is no agent to resume, so retry cost is just wall-clock time, (b) `terminationGracePeriodSeconds` (120s) provides ample headroom, and (c) transient MinIO unavailability (network blip, brief leader election) typically resolves within 10-20 seconds. If all retries fail, the gateway falls back to writing a **minimal session state record** directly to Postgres (see below). A `CheckpointStorageUnavailable` critical alert fires (see Section 16.5). The adapter also increments `lenny_checkpoint_eviction_fallback_total` (counter, labeled by `pool` and `had_prior_checkpoint: true|false`) to distinguish evictions that fell back to Postgres with a prior full checkpoint from those without one — the latter represent sessions where workspace state is permanently lost. If a previous successful full checkpoint exists, the session is resumed from that checkpoint per Section 7.2. If only the minimal state record exists, the session is resumed on a fresh pod with conversation context but without workspace files — the client receives a `session.resumed` event with `resumeMode: "conversation_only"` and `workspaceLost: true` so it can take appropriate action (re-upload files, restart workspace setup, etc.).

**Eviction fallback: Postgres minimal state record.** When an eviction checkpoint cannot be persisted to MinIO, the gateway writes a minimal state record to the `session_eviction_state` table in Postgres. This record is intentionally small — it contains only what is needed to resume a conversation on a fresh pod without the workspace:

- `session_id` — the session identifier
- `generation` — the session generation counter (for coordinator fencing)
- `conversation_cursor` — the last event cursor from the EventStore (allows replay of conversation history from the event log)
- `last_message_context` — the last gateway-persisted prompt/response pair (plain text, max 64KB, truncated if longer), sufficient for the runtime to orient itself without full replay
- `evicted_at` — timestamp of the eviction event
- `workspace_lost` — boolean, always `true` for this record

This record is written in a single Postgres transaction. It does not store workspace file contents (no blobs, no TOAST). The workspace tar is not stored in Postgres — this fallback provides conversation continuity, not workspace recovery. On resume from minimal state, workspace materialization is skipped and the pod starts with an empty workspace; the client is notified to re-upload files if needed. The `session_eviction_state` table is cleaned up when the session reaches a terminal state.

### 4.5 Artifact Store

**Role:** Durable storage for workspace files and exports.

**Stores:**

- Original uploaded workspace files (the canonical "initial workspace")
- Sealed workspace bundles
- Exported file subsets for delegation
- Runtime checkpoints
- Large logs and artifacts

**Implementation:** MinIO (S3-compatible). Local disk for development mode. **Never** Postgres for blob storage — the TOAST overhead and vacuum pressure degrade transactional workload performance. See Section 12.5 for retention policy.

**Tenant isolation:** All object paths in MinIO are prefixed with `/{tenant_id}/`. The `ArtifactStore` interface enforces this: every method that reads, writes, lists, or deletes objects validates that the supplied `tenant_id` matches the path prefix before issuing the S3 call. Paths that fail prefix validation are rejected without reaching MinIO. This guarantees tenant-scoped access at the interface level, prevents cross-tenant reads or writes regardless of caller bugs, and ensures that `DeleteByTenant(tenant_id)` (Section 12.8) maps to a deterministic prefix-scoped bulk delete (`/{tenant_id}/*`).

**Workspace lineage:** Each workspace snapshot is immutable and identified by a content-addressed hash (SHA-256 of the tar archive). The session record tracks lineage via a `parent_workspace_ref` field that records the session ID and artifact path of the workspace snapshot that seeded this session (from uploads or a derived session per Section 7.1). This field is a **metadata lineage pointer only** — it does not create a shared MinIO object reference. When a session is derived (Section 7.1), the gateway copies the parent's workspace snapshot bytes into a new MinIO object scoped to the derived session's own path (`/{tenant_id}/{object_type}/{new_session_id}/...`). The derived session owns its artifact independently; GC on the parent's artifacts has no effect on the derived session's workspace. This enables lineage queries such as "which sessions were derived from this workspace?" and "what was the workspace history for this session?" Full workspace versioning (tracking incremental changes within a session) is not supported — snapshots are full workspace captures at checkpoint or seal time.

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

**Internal abstraction layer:** All Lenny components interact with pod lifecycle through two interfaces, never directly with agent-sandbox CRD types. Both embed a shared read-only `PoolReader`.

**`PoolReader`** (shared, embedded by both interfaces):

- `ListPools() → ([]PoolStatus, error)` — pool health and capacity
- `GetPoolStatus(ctx, poolName) → (PoolStatus, error)` — single pool status

**`PodLifecycleManager`** (gateway-facing, embeds `PoolReader`):

- `ClaimPod(ctx, poolName, sessionID, opts ClaimOpts) → (PodHandle, error)` — acquire idle pod. `ClaimOpts` includes `requiresDemotion` (true when workspace plan includes `sdkWarmBlockingPaths` — gateway signals that the claimed SDK-warm pod must be demoted before use), optional `priority`, optional `clusterID` (nullable, for future multi-cluster). `PodHandle` carries metadata: `warmMode`, `certExpiresAt`, `adapterEndpoint`.
- `ReleasePod(ctx, podHandle) → error` — release pod after session ends
- `DrainPod(ctx, podHandle, checkpointFirst bool) → (DrainResult, error)` — gracefully terminate. `DrainResult` includes retry state for seal-and-export hold semantics.
- `GetPodStatus(ctx, podHandle) → (PodStatus, error)` — read pod state, health, cert expiry

**`PoolManager`** (controller-facing, embeds `PoolReader`):

- `ReconcilePool(ctx, poolConfig) → error` — ensure pool matches desired state (minWarm/maxWarm)
- `ApplyPoolDefinition(ctx, poolDef) → error` — CRUD for pool definitions (creates/updates/deletes `SandboxTemplate` and `SandboxWarmPool` CRDs)
- `ReplacePod(ctx, podHandle, reason string) → error` — proactive replacement (cert expiry, health failure)
- `TransitionPodState(ctx, podHandle, from PodState, to PodState) → error` — transition pod through state machine; validates legal transitions
- `GarbageCollect(ctx) → ([]OrphanResult, error)` — orphan detection and cleanup. Covers orphaned `Sandbox` pods (no matching pool) and orphaned `SandboxClaim` resources (see orphan claim detection below).
- `ManageFinalizer(ctx, podHandle, action FinalizerAction) → error` — add/remove finalizer; `FinalizerAction` is an enum (`Add`, `Remove`)
- `ManagePDB(ctx, poolName, config PDBConfig) → error` — create/update/delete PodDisruptionBudget per pool
- `DrainPool(ctx, poolName, checkpointFirst bool) → error` — drain all pods in a pool (admin API)
- `SetPoolCondition(ctx, poolName, condition PoolCondition, reason string) → error` — set pool health status (e.g., Degraded when RuntimeClass missing)

Default implementations: `AgentSandboxPodLifecycleManager` and `AgentSandboxPoolManager`, which delegate to agent-sandbox CRDs.

Future extension points (reserved, not implemented now): `PodDebugger` interface for troubleshooting (exec, logs, describe); `SnapshotPod`/`RestoreFromSnapshot` for GKE Pod Snapshots integration; `clusterID` on `ClaimOpts` and `PoolStatus` for multi-cluster readiness.

This indirection means a breaking upstream change or a decision to replace agent-sandbox requires changing only the implementations behind these interfaces, not the gateway, scaling controller, or any other consumer.

**CRD mapping from agent-sandbox:**

| Agent-Sandbox CRD | Replaces (old Lenny CRD)     | Purpose                                                                                                                                                                                                                                                        |
| ----------------- | ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SandboxTemplate` | `AgentPool`                  | Declares a pool: runtime, isolation profile, resource class, warm count range, scaling policy, `executionMode`                                                                                                                                                 |
| `SandboxWarmPool` | (part of WarmPoolController) | Upstream warm pool management with configurable `minWarm`/`maxWarm`                                                                                                                                                                                            |
| `Sandbox`         | `AgentPod`                   | Represents a managed agent pod. Status subresource carries the authoritative state machine. Enables GC, structured claim semantics, and warm-pool PDB targeting.                                                                                               |
| `SandboxClaim`    | `AgentSession`               | Represents an active session binding. Created by the gateway only after it has successfully claimed a `Sandbox`. Links the claimed pod to session metadata (deliberately not an `ownerReference`, so the session survives pod deletion and can be reassigned). |

**ADR required (ADR-TBD): `SandboxClaim` optimistic-locking verification — Phase 1 blocking prerequisite.** Before Phase 1 implementation begins, verify that `SandboxClaim`'s optimistic-locking semantics match Lenny's claim approach. Document the result as an ADR. If the semantics diverge, the `PodLifecycleManager.ClaimPod` implementation must compensate (e.g., by wrapping claims in a compare-and-swap loop at the gateway level). **Verification approach:** Write a minimal integration test against a test Kubernetes cluster that (1) creates a `SandboxClaim` resource with a `resourceVersion`-guarded update, (2) races two concurrent claim attempts against the same `Sandbox`, and (3) confirms exactly one succeeds with HTTP 409 Conflict for the loser. If the 409 path works as expected, the ADR records "optimistic locking confirmed, no compensation needed." If it does not, the ADR must specify the compensating CAS loop design before Phase 1 proceeds.

**Pod claim mechanism:** Gateway replicas claim pods via `SandboxClaim` resources with optimistic locking — exactly one gateway wins; all others receive a conflict and retry with a different idle pod from the pool. This keeps the controller off the claim hot path entirely — pod-to-session binding is resolved at the API-server level with no single-writer bottleneck.

**Leader election:** The WarmPoolController runs as a Deployment with 2+ replicas using Kubernetes Lease-based leader election with lease name `lenny-warm-pool-controller` (lease duration: 15s, renew deadline: 10s, retry period: 2s). The PoolScalingController uses a separate lease (`lenny-pool-scaling-controller`); see Section 4.6.2. During failover, existing sessions continue unaffected; only new pod creation and pool scaling pause. **Failover duration depends on the trigger:** on clean shutdown (rolling update, voluntary termination), the outgoing leader releases its lease via `LeaderCallbacks.OnStoppedLeading`, reducing the gap to near-zero. On crash (process kill, node failure), the worst-case failover window is `leaseDuration + renewDeadline = 25s` — the new leader cannot acquire the lease until the old lease expires after its full duration plus the renew deadline elapses without renewal. All `minWarm` formulas and capacity planning use the 25s crash-case as the sizing basis (see below and Section 17.8).

**Scaling:** Pools support `minWarm`, `maxWarm`, and an optional `scalePolicy` with time-of-day schedules or demand-based rules. Pools referencing a `preConnect`-capable runtime warm all pods to SDK-warm state; demotion to pod-warm happens on demand at claim time (see Section 6.1). Low-traffic pools can scale to zero warm pods with documented cold-start latency as fallback.

**Idle cost visibility and scale-to-zero:** The metric `lenny_warmpool_idle_pod_minutes` (counter, labeled by pool and resource class) tracks cumulative idle pod-minutes, letting deployers estimate warm pool cost from their monitoring stack. Pools support `minWarm: 0` for off-hours via time-of-day rules in `scalePolicy`: `scaleToZero: { schedule: "0 22 * * *", resumeAt: "0 6 * * *" }` sets `minWarm: 0` during the window; sessions arriving in zero-warm periods incur cold-start latency. `scaleToZero` disabled by default for `type: mcp` pools (deployer opt-in required). A `WarmPoolIdleCostHigh` warning alert fires when idle pod-minutes exceed a deployer-configured threshold over a 24 h window (see Section 16.5).

**CRD validation:** All CRDs include OpenAPI schema validation with CEL rules to catch common misconfigurations at admission time. Key rules: `minWarm <= maxWarm`, `maxWarm > 0`, valid RuntimeClass reference format, resource class values within the allowed set, `maxSessionAge > 0`, and `taskPolicy.maxTasksPerPod > 0` when `executionMode: task`. Malformed specs are rejected by the API server before reaching the controller, preventing reconciliation loops on invalid input. The controller also validates at reconciliation time as defense-in-depth.

**Controller failover and warm pool sizing:** During a leader-election failover (near-zero on clean shutdown; up to 25s on crash — see leader election paragraph above), the controller cannot create new pods or reconcile pool scaling. If the warm pool is undersized, this pause can exhaust available pods. To prevent this, `minWarm` should account for both the failover window and burst absorption: `minWarm >= peak_claims_per_second * (failover_seconds + pod_startup_seconds) + burst_p99_claims * pod_warmup_seconds`. **Use `failover_seconds = 25` (the crash-case worst case) for all sizing calculations.** The burst term ensures the pool is not exhausted by demand spikes that arrive faster than replacement pods can become ready (see Section 4.6.2 for full formula and variable definitions). For example, at 2 claims/sec with a 25s failover pause, 10s pod startup, and a p99 burst rate of 4 claims/sec with 10s warmup: `minWarm >= 2 * 35 + 4 * 10 = 110`. Per-tier claim rate estimates and recommended minWarm values are in Section 17.8. During the failover window, the gateway queues incoming pod claim requests for up to `podClaimQueueTimeout` (default: 30s). If no pod becomes available before the timeout, the session creation fails with a retryable error so the client can back off and retry. Note: `podClaimQueueTimeout` (30s) is longer than the 25s worst-case failover, so queued claims will survive a crash-case failover provided the warm pool has not been exhausted. As an early-warning mechanism, the `WarmPoolLow` alert (Section 16.5) fires when available pods drop below 25% of `minWarm`, giving operators time to investigate before exhaustion occurs.

**Simultaneous controller failover:** The WarmPoolController and PoolScalingController use separate leases and may failover independently. If both crash simultaneously (e.g., both leaders co-located on a failed node): (1) pool replenishment pauses (WPC down) — no new pods are created, (2) scaling intelligence pauses (PSC down) — `minWarm`/`maxWarm` values are not updated from Postgres. The blast radius is additive but not multiplicatively worse: since the PSC only adjusts scaling parameters (which the WPC reads from CRDs), losing both controllers for 25s is equivalent to losing the WPC alone — the stale CRD values from before the PSC crash remain valid and the WPC will reconcile against them once it re-elects. The `podClaimQueueTimeout` (30s) covers this scenario. To prevent co-located leader failures, operators should use pod anti-affinity on controller deployments (see Section 17.8) so WPC and PSC leaders run on different nodes.

**Controller leader election monitoring:** A `ControllerLeaderElectionFailed` critical alert fires when any controller's Lease has not been renewed within `leaseDuration` (15s) — detectable via the `lenny_controller_leader_lease_renewal_age_seconds` gauge (one per controller). This gives operators early warning that failover is imminent or in progress before the lease fully expires. The alert auto-resolves when a new leader acquires the lease and begins renewing.

**API server rate limiting:** The controller uses two separate client-side rate limiter buckets to prevent pod creation starvation during scale-up:

- **Pod creation bucket:** token bucket at 20 QPS, burst 50. Dedicated to `Create` calls for new `Sandbox` pods, ensuring scale-up is never starved by status update traffic.
- **Status update bucket:** token bucket at 30 QPS, burst 100. Dedicated to `UpdateStatus` calls on `Sandbox` and `SandboxWarmPool` resources.

Both buckets are configurable via controller flags (`--create-qps`, `--create-burst`, `--status-qps`, `--status-burst`). All other API server requests (reads, deletions, finalizer updates) share the controller-runtime default rate limiter (10 QPS, burst 100).

During steady-state pool replenishment, pod creation is processed through the work queue with controlled concurrency. The work queue max depth is configurable (default: 500); if the queue exceeds this depth, new reconciliation events are dropped and a `lenny_controller_queue_overflow_total` metric is incremented. The number of concurrent reconciliation workers is configurable via `--max-concurrent-reconciles` (default: 1). For cold-start scenarios (initial pool fill from zero, cluster restart recovery), operators should increase concurrent workers to match the tier's parallelism needs — see Section 17.8 for per-tier recommendations. The rate limiter remains the throughput ceiling regardless of worker count, preventing API server or etcd overload even with multiple concurrent workers.

**Cold-start pool fill:** When a pool is created or recovers from zero (e.g., after a cluster restart or scale-from-zero resume), filling to `minWarm` takes time proportional to `minWarm / effective_create_throughput`. The `WarmPoolExhausted` alert (Section 16.5) is suppressed during an initial fill grace period (`--initial-fill-grace-period`, default: 120s from pool creation or controller startup) to avoid false-positive alerts during expected cold-start fill time. The metric `lenny_warmpool_fill_duration_seconds` (histogram, labeled by pool) tracks the time from pool creation to reaching `minWarm` ready pods, enabling operators to set SLO targets for cold-start recovery. See Section 17.8 for controller tuning recommendations.

**etcd write pressure at scale:** At high concurrent session counts (e.g., 1,000 sessions with ~2-minute lifetimes), CRD status updates can generate 80+ writes/second. Mitigations: (1) The state machine uses 3 coarse label values instead of 10+ fine-grained labels, reducing label mutation frequency (Section 6.2). (2) Status updates are batched by the controller's work queue — not every state transition is immediately written. (3) The dedicated status update rate limiter (30 QPS, burst 100) bounds the update rate while the separate pod creation bucket prevents creation starvation. (4) Operators should monitor etcd write latency (`etcd_disk_wal_fsync_duration_seconds`, `etcd_server_proposals_committed_total`) and scale etcd resources if p99 write latency exceeds 25ms.

**etcd operational tuning:** Deployers must configure the following to maintain etcd health under sustained CRD churn:

- **Compaction:** Enable automatic compaction with `--auto-compaction-mode=periodic` and `--auto-compaction-retention=5m`. This prevents unbounded revision history growth from frequent status updates. Note: compaction mode and retention are etcd startup flags, not runtime-adjustable. Changing them requires an etcd member restart. On managed Kubernetes (EKS, GKE, AKS), the cloud provider controls etcd flags — operators cannot change compaction settings directly. Self-managed clusters must perform rolling etcd member restarts (one member at a time, waiting for cluster health between restarts) to apply new values without downtime. See the etcd operations runbook (Section 17.7) for the procedure.
- **Defragmentation:** Schedule periodic defragmentation during low-traffic windows (e.g., via a CronJob running `etcdctl defrag` on each member). Defrag reclaims free pages after compaction and should run at least once daily for clusters with high CRD write rates. **Operational caveat:** `etcdctl defrag` on a given member blocks all reads and writes to that member while defrag runs (typically 1–30 seconds depending on database size). To avoid disrupting Kubernetes API availability, defrag one member at a time, starting with followers and finishing with the leader. Never run `etcdctl defrag --cluster` in production — it defrags all members sequentially but provides no control over timing. On managed Kubernetes, the cloud provider handles defragmentation automatically; operators should not schedule their own defrag CronJobs. For Tier 3 deployments where "off-peak" is difficult to define (e.g., 10,000 concurrent sessions with global traffic), defrag should target the window with the lowest observed `etcd_server_proposals_committed_total` rate, or operators should use a dedicated etcd cluster (as recommended in Section 17.8) where defrag does not affect the main API server.
- **Quota monitoring:** Set `--quota-backend-bytes` (recommended: 8 GB) and alert when usage exceeds 80% (`etcd_server_quota_backend_bytes` minus `etcd_debugging_mvcc_db_total_size_in_bytes`). An `EtcdQuotaNearLimit` alert should fire at 80% to give operators time to defragment or increase quota before etcd enters alarm state. **Quota exhaustion recovery:** If etcd reaches its quota limit, it enters alarm state and refuses all write operations — the Kubernetes API server becomes read-only for CRD operations, blocking pod claims, state transitions, and pool reconciliation (existing sessions continue as described in "etcd unavailability" below). Recovery requires: (1) defragment all members to reclaim space (`etcdctl defrag` on each member individually), (2) verify database size dropped below quota (`etcdctl endpoint status`), (3) disarm the alarm (`etcdctl alarm disarm`), (4) verify writes resume by checking API server health. On managed Kubernetes, quota exhaustion is a cloud-provider support escalation — the operator cannot access etcd directly. See the etcd operations runbook (Section 17.7) for the full procedure.
- **Snapshot frequency:** Ensure `--snapshot-count` is tuned for the write rate (default 100,000 is reasonable for most deployments; reduce to 10,000 for very high write rates to limit recovery time).

See Section 17.8 for controller tuning recommendations.

**etcd unavailability (degraded mode):** etcd is a critical dependency for the Kubernetes API server. When etcd is unavailable, the API server cannot process CRD writes, which means pod claims (`SandboxClaim` creation), pod state transitions (`Sandbox` status updates), and pool reconciliation all freeze. The platform enters a degraded mode with the following behavior:

- **Existing sessions continue unaffected.** Active session pods are already running and communicate with the gateway via gRPC — they do not depend on etcd for ongoing operation. Session state is persisted to Postgres, not etcd.
- **New session creation is rejected.** The gateway detects API server unavailability when `ClaimPod` fails and returns a retryable error to the client (same mechanism as the `podClaimQueueTimeout` path described above). Clients back off and retry.
- **Pool replenishment is frozen.** The warm pool controller cannot create new pods or update CRD status. Once etcd recovers, the controller's work queue replays pending reconciliations and the pool self-heals.
- **Alerting:** An `EtcdUnavailable` critical alert (Section 16.5) fires when the API server reports etcd connection failures (`etcd_request_duration_seconds` errors or `apiserver_storage_errors_total` sustained > 0) for more than 15 seconds. Operators should treat this as a cluster-wide incident.

**Disruption protection for agent pods:** The primary protection against voluntary disruption (node drains, cluster upgrades) is a **preStop hook** on every agent pod that triggers a checkpoint via the runtime adapter's `Checkpoint` RPC before allowing termination. The pod's `terminationGracePeriodSeconds` is set high enough (default: 120s) to give the checkpoint time to complete and be persisted to object storage. For active pods whose sessions have been checkpointed (or that have no session at all), the session retry mechanism described in Section 7.2 handles resumption on a replacement pod.

The warm pool controller can optionally create a PDB **per `SandboxTemplate`** with `minAvailable` set to the pool's `minWarm` value. This prevents voluntary evictions from draining the warm pool below its configured minimum, protecting warm pod availability rather than individual sessions. The PDB targets only unclaimed (warm) pods via a label selector (`lenny.dev/state: idle`), so it does not interfere with the preStop-based protection on active session pods.

**Sandbox finalizers:** Every `Sandbox` resource carries a finalizer (`lenny.dev/session-cleanup`) to prevent Kubernetes from deleting the pod — and its local workspace — while a session is still active. When a `Sandbox` enters the `Terminating` state, the warm pool controller checks whether any active `SandboxClaim` still references the pod. It removes the finalizer only after confirming one of two conditions: (a) no session references the pod, or (b) the session has been successfully checkpointed and the gateway has been notified so it can resume the session on a replacement pod. If the finalizer is not removed within 5 minutes (pod stuck in `Terminating`), the controller fires a `FinalizerStuck` alert. Operators should follow the stuck finalizer runbook (`docs/runbooks/stuck-finalizer.md`; see Section 17.7) to safely verify session state, confirm checkpoint and artifact integrity, and remove the finalizer without orphaning resources. This ensures that node drains, scale-downs, and accidental deletions never silently orphan an active session.

**Orphaned `SandboxClaim` detection:** Because `SandboxClaim` resources deliberately omit an `ownerReference` (to survive pod deletion and allow reassignment), Kubernetes will not garbage-collect them automatically. The WarmPoolController's `GarbageCollect` reconciliation loop detects orphaned claims as follows: every 60 seconds, the controller lists all `SandboxClaim` resources whose `metadata.creationTimestamp` is older than `claimOrphanTimeout` (default: 5 minutes, configurable via `--claim-orphan-timeout`). For each such claim, the controller queries Postgres to check whether an active session references it. If no active session exists, the claim is deleted and the underlying `Sandbox` pod is transitioned back to `idle`, returning it to the warm pool. This handles the crash scenario where a gateway creates a `SandboxClaim` but crashes before persisting the session to Postgres — on recovery, the gateway has no record of the claim, so the controller's orphan loop reclaims it. The metric `lenny_orphaned_claims_total` (counter, labeled by pool) is incremented for each orphaned claim deleted. A `SandboxClaimOrphanRateHigh` warning alert fires when `lenny_orphaned_claims_total` exceeds 10 in a 15-minute window, indicating potential gateway instability (see Section 16.5).

**Dependency pinning and upgrade policy:** Lenny pins `kubernetes-sigs/agent-sandbox` to a specific tagged release (initially the latest stable tag at implementation start). Upgrades follow a one-release-delay cadence: when upstream publishes release N, Lenny evaluates it against integration tests; only after release N+1 is published (confirming N's API surface is stable) does Lenny upgrade to N. All CRD schema changes are gated by the integration test suite before merge.

**Fallback plan if agent-sandbox is abandoned or diverges:** Because all consumers use `PodLifecycleManager` and `PoolManager` interfaces, Lenny can replace the agent-sandbox backend with custom kubebuilder-based controllers implementing the same interfaces. Estimated effort: 2-3 engineering-weeks for a minimal replacement covering pod claiming, release, drain, and basic warm pool reconciliation (the interface definitions serve as functional requirements). Advanced features (Pod Snapshots on GKE, upstream community PDB patterns) would require additional effort. This fallback is viable but undesirable — the preferred path is continued upstream contribution.

#### 4.6.2 PoolScalingController (Pool Configuration)

**Role:** Manages desired pool configuration, scaling intelligence, and experiment variant pool sizing. Separate from the WarmPoolController which manages individual pod lifecycle.

**Responsibilities:**

- Reconcile pool configuration from Postgres (admin API source of truth) into `SandboxTemplate` and `SandboxWarmPool` CRDs
- Manage scaling decisions: time-of-day schedules, demand-based rules, experiment variant sizing
- Integrate with experiment definitions to automatically size variant pools

**Leader election:** The PoolScalingController runs its own Lease-based leader election using a separate lease name (`lenny-pool-scaling-controller`) from the WarmPoolController (`lenny-warm-pool-controller`). Lease parameters: duration 15s, renew deadline 10s, retry period 2s (matching WarmPoolController). The two controllers elect leaders independently — they may run on different replicas. Failover timing follows the same model as the WarmPoolController: near-zero on clean shutdown (voluntary lease release), up to 25s on crash (see Section 4.6.1).

**Pluggable `PoolScalingStrategy` interface.** Fully replaceable by deployers.

**Default formula:**

```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor + burst_p99_claims × pod_warmup_seconds)
```

The first term covers steady-state demand; the second term (`burst_p99_claims × pod_warmup_seconds`) absorbs request bursts that arrive faster than the pool can refill. `burst_p99_claims` is the p99 claim arrival rate (claims/second) observed over a short window (default: 60s). `pod_warmup_seconds` is the time from pod creation to ready state (pod pull + startup + optional SDK warm). During a burst, each second of warmup latency means one fewer pod available to serve the next claim — the burst term reserves enough headroom so the pool is not exhausted before replacement pods become ready.

`safety_factor` defaults to 1.5 for agent-type pools, 2.0 for mcp-type pools. This formula assumes session mode (one session per pod). For task and concurrent modes, a `mode_factor` adjustment reduces pod demand based on reuse and slot multiplexing — see Section 5.2 "Execution Mode Scaling Implications" for per-mode formula variants.

**Cold-start limitation:** The formula inputs (`base_demand_p95`, `burst_p99_claims`) are derived from historical traffic metrics and are not available at first deployment. For new pools, the operator must set `minWarm` manually — the PoolScalingController cannot auto-configure this value without observed demand. See Section 17.8.2 "First deployment sizing" for conservative starting recommendations and a first-week monitoring workflow.

**Pool phases:** pre-warm, ramp, steady state, wind-down — all automatic. For experiment-lifecycle-driven pool teardown (what happens to variant pools when an experiment is paused or concluded), see the "PoolScalingController behavior on experiment status transitions" paragraph in Section 10.7.

**CRDs become derived state** reconciled from Postgres by PoolScalingController. The admin API is the source of truth for pool configuration; the controller translates that into Kubernetes CRDs.

**Reconciliation drift detection.** Because CRDs are derived state, any period where the PoolScalingController is down (crash, leader election, network partition to the API server) causes CRD specs to diverge from Postgres desired state. The WarmPoolController continues operating against stale CRD values indefinitely — it has no mechanism to detect that the values it reads are outdated. To close this observability gap:

1. **Reconciliation lag metric.** The PoolScalingController exposes a `lenny_pool_config_reconciliation_lag_seconds` gauge (labeled by pool name) that records the time since the last successful CRD reconciliation for each pool. On every successful reconciliation cycle, the controller resets the gauge to zero. If the controller is down, the gauge value grows monotonically (scraped from the last-active replica's `/metrics` endpoint until the process exits; after exit, the metric disappears and the `PoolConfigDrift` alert fires on the absence condition below).
2. **`PoolConfigDrift` warning alert.** Fires when any pool's Postgres `pool_config_generation` (incremented on every admin API write) differs from its CRD's `metadata.annotations["lenny.dev/config-generation"]` for more than 60 seconds. Detection runs in the gateway (which reads both Postgres and CRD state) rather than in the PoolScalingController itself, so the alert fires even when the controller is completely down. The alert auto-resolves when the controller catches up and reconciles the CRD. Added to the alert inventory in Section 16.5.
3. **Admin API sync status.** The `PUT /v1/admin/pools/{name}` response includes a `syncStatus` field: `"pending"` immediately after a write (Postgres updated, CRD reconciliation not yet confirmed), or `"synced"` once the CRD generation matches. This prevents operators from believing changes are instantly effective. The `GET /v1/admin/pools/{name}` response also includes `syncStatus` for consistency.
4. **Sync-status endpoint.** `GET /v1/admin/pools/{name}/sync-status` returns detailed reconciliation state: `postgresGeneration`, `crdGeneration`, `lastReconciledAt`, `lagSeconds`, and `inSync` (boolean). This gives operators a single endpoint to diagnose whether the PoolScalingController is keeping up.

#### 4.6.3 CRD Field Ownership and Write Boundaries

Both the WarmPoolController and PoolScalingController interact with the same CRD types (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`). To prevent conflicting writes, each controller owns a disjoint set of fields:

| CRD               | Field / Subresource                                | Owner                      | Notes                                           |
| ----------------- | -------------------------------------------------- | -------------------------- | ----------------------------------------------- |
| `SandboxTemplate` | `spec.*`                                           | PoolScalingController      | Reconciled from Postgres pool definitions       |
| `SandboxTemplate` | `status.*`                                         | WarmPoolController         | Pool health conditions, observed generation     |
| `SandboxWarmPool` | `spec.minWarm`, `spec.maxWarm`, `spec.scalePolicy` | PoolScalingController      | Scaling parameters from Postgres                |
| `SandboxWarmPool` | `status.*`                                         | WarmPoolController         | Current warm count, ready count, conditions     |
| `Sandbox`         | `spec.*`                                           | WarmPoolController         | Pod creation and configuration                  |
| `Sandbox`         | `status.*`                                         | WarmPoolController         | State machine transitions, health               |
| `SandboxClaim`    | `spec.*`, `status.*`                               | Gateway (not a controller) | Created/deleted by gateway during claim/release |

**RBAC enforcement:** Each controller's ServiceAccount has RBAC rules scoped to its owned fields. The WarmPoolController ServiceAccount has `update` on `Sandbox` and `status` subresources of `SandboxTemplate` and `SandboxWarmPool`, but only `get`/`list`/`watch` on `SandboxTemplate.spec` and `SandboxWarmPool.spec`. The PoolScalingController ServiceAccount has `create`/`update`/`delete` on `SandboxTemplate` and `SandboxWarmPool` specs, but only `get`/`list`/`watch` on status subresources and no access to `Sandbox` resources. This prevents accidental cross-controller writes even if code bugs exist.

**Validating webhook for Postgres-authoritative state:** A validating admission webhook rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` fields unless the request carries the annotation `lenny.dev/managed-by: pool-scaling-controller`. The PoolScalingController sets this annotation on every update it performs. This prevents operators from manually editing CRD specs that would be silently overwritten on the next reconciliation cycle. The webhook returns an informative denial message directing operators to use the admin API instead. The webhook runs in `Fail` mode with a 5s timeout; if the webhook is unavailable, updates are denied (fail-closed) to protect Postgres-authoritative state.

### 4.7 Runtime Adapter

**Role:** Standardized bridge between the Lenny platform and any pod binary. The adapter protocol uses a two-part model: multiple focused local MCP servers for tool access, and a separate lifecycle channel for operational signals.

**Contract (internal gRPC/HTTP+mTLS API — gateway ↔ adapter):**

| RPC                  | Description                                                                                                                                            |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `PrepareWorkspace`   | Accept streamed files into staging area                                                                                                                |
| `FinalizeWorkspace`  | Validate, materialize to `/workspace/current`                                                                                                          |
| `RunSetup`           | Execute bounded setup commands                                                                                                                         |
| `StartSession`       | Start the agent runtime with final `cwd` (pod-warm mode)                                                                                               |
| `ConfigureWorkspace` | Point a pre-connected session at the finalized `cwd` (SDK-warm mode)                                                                                   |
| `DemoteSDK`          | Tear down the pre-connected SDK process and return the pod to pod-warm state (see Section 6.1). Required for runtimes that declare `preConnect: true`. |
| `Attach`             | Connect client stream to running session                                                                                                               |
| `Interrupt`          | Interrupt current agent work                                                                                                                           |
| `Checkpoint`         | Export recoverable session state                                                                                                                       |
| `ExportPaths`        | Package files for delegation, rebased per export spec (Section 8.7)                                                                                    |
| `AssignCredentials`  | Push a per-provider credential map (one lease per authorized provider) to the runtime before session start                                             |
| `RotateCredentials`  | Push replacement credentials for a specific provider's lease mid-session (fallback/rotation)                                                           |
| `Resume`             | Restore from checkpoint on a replacement pod                                                                                                           |
| `Terminate`          | Graceful shutdown                                                                                                                                      |

**Checkpoint / Interrupt mutual exclusion:** The adapter maintains a per-session operation lock that serializes `Checkpoint` and `Interrupt` RPCs. Only one of these operations may execute at a time; if a second arrives while the first is in progress, it is queued and executed after the first completes. Ordering semantics:

- **Interrupt during checkpoint:** The interrupt is queued until the checkpoint completes (or times out per Section 4.4). After the checkpoint finishes, the queued interrupt is delivered normally. Rationale: a checkpoint in progress has already paused or quiesced the runtime (via SIGSTOP or lifecycle channel `checkpoint_request`); delivering an interrupt in that state is undeliverable (signals cannot reach a SIGSTOPped process) or would violate the quiescence guarantee.
- **Checkpoint during interrupt:** The checkpoint is queued until the runtime acknowledges the interrupt (via `interrupt_acknowledged` on the lifecycle channel for Full-tier, or until the adapter observes the runtime resume output for lower tiers). This prevents snapshotting mid-interrupt state.
- **Queue depth:** At most one operation may be queued. If a second operation of the same type arrives while one is already queued, the second is coalesced (checkpoint) or dropped with a `BUSY` status (interrupt). The gateway retries dropped interrupts with backoff.

**Runtime → Gateway events (sent over the gRPC control channel):**

| Event                  | Description                                                              |
| ---------------------- | ------------------------------------------------------------------------ |
| `RATE_LIMITED`         | Current credential is rate-limited; request fallback                     |
| `AUTH_EXPIRED`         | Credential lease expired or was rejected by provider                     |
| `PROVIDER_UNAVAILABLE` | Provider endpoint is unreachable                                         |
| `LEASE_REJECTED`       | Runtime cannot use the assigned credential (incompatible provider, etc.) |

#### Adapter ↔ Runtime Protocol (Intra-Pod)

The adapter communicates with the runtime binary via two mechanisms:

**Part A — Multiple focused local MCP servers** (intra-pod, abstract Unix socket):

- **Platform MCP server** — Lenny-specific tools: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`. Note: lease extension is handled via the adapter↔gateway gRPC lifecycle, not as an MCP tool (see Section 8.6).
- **One MCP server per authorized connector** — each connector in the session's delegation policy gets its own independent MCP server. No aggregated connector proxy — aggregation is not lossless per MCP spec (capability negotiation is per-server, sampling breaks, tool name collisions, resource URI collisions).

**No workspace MCP server.** Workspace is materialized to `/workspace/current` before the runtime starts. The runtime accesses it via the filesystem directly.

**Part B — Lifecycle channel** — bidirectional JSON Lines stream over an abstract Unix socket (`@lenny-lifecycle`) for operational signals. The socket path is advertised in the adapter manifest (`lifecycleChannel.socket`). The runtime connects as a client; the adapter listens. Each message is a single JSON object terminated by `\n`:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    checkpoint_complete, interrupt_request,
                    credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged
```

Optional. Runtimes that don't open it operate in fallback-only mode. Versioned by capability negotiation at the top. Unknown messages silently ignored on both sides.

**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` on the manifest volume (read-only to the agent container) **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per task execution (in task mode, re-written before each task; the runtime must re-read the manifest at the start of each task). The manifest is stable for the duration of a single task or session — it does not change while the runtime is processing.

```json
{
  "version": 1,
  "platformMcpServer": { "socket": "@lenny-platform-mcp" },
  "lifecycleChannel": { "socket": "@lenny-lifecycle" },
  "connectorServers": [{ "id": "github", "socket": "@lenny-connector-github" }],
  "runtimeMcpServers": [],
  "agentInterface": {
    "description": "Analyzes codebases and produces refactoring plans",
    "inputModes": [{ "type": "text/plain" }],
    "outputModes": [{ "type": "text/plain", "role": "primary" }],
    "supportsWorkspaceFiles": true,
    "skills": [
      {
        "id": "review",
        "name": "Code Review",
        "description": "Reviews code for quality and correctness"
      }
    ]
  },
  "sessionId": "sess_abc",
  "taskId": "task_root"
}
```

**Adapter manifest field reference:**

| Field                       | Type               | Required | Description                                                                                                                                                                                                                                                                                 | Tier relevance            |
| --------------------------- | ------------------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| `version`                   | `integer`          | Yes      | Schema version. Currently `1`. Runtimes should reject unknown major versions.                                                                                                                                                                                                               | All (if reading manifest) |
| `platformMcpServer`         | `object`           | Yes      | Platform MCP server endpoint.                                                                                                                                                                                                                                                               | Standard, Full            |
| `platformMcpServer.socket`  | `string`           | Yes      | Abstract Unix socket name (e.g., `@lenny-platform-mcp`).                                                                                                                                                                                                                                    | Standard, Full            |
| `lifecycleChannel`          | `object`           | Yes      | Lifecycle channel endpoint.                                                                                                                                                                                                                                                                 | Full                      |
| `lifecycleChannel.socket`   | `string`           | Yes      | Abstract Unix socket name (e.g., `@lenny-lifecycle`).                                                                                                                                                                                                                                       | Full                      |
| `connectorServers`          | `array`            | Yes      | Per-connector MCP servers. Empty array `[]` if no connectors are authorized. Never absent.                                                                                                                                                                                                  | Standard, Full            |
| `connectorServers[].id`     | `string`           | Yes      | Connector identifier.                                                                                                                                                                                                                                                                       | Standard, Full            |
| `connectorServers[].socket` | `string`           | Yes      | Abstract Unix socket name for this connector.                                                                                                                                                                                                                                               | Standard, Full            |
| `runtimeMcpServers`         | `array`            | Yes      | Reserved for `type:mcp` runtimes. Empty array `[]` in v1.                                                                                                                                                                                                                                   | — (future)                |
| `agentInterface`            | `object` or `null` | Yes      | Summary of the runtime's declared `agentInterface` from the Runtime definition (Section 5.1). `null` if not declared. Contains `description`, `inputModes`, `outputModes`, `supportsWorkspaceFiles`, `skills`, `examples` — same schema as the Runtime definition's `agentInterface` field. | Informational (all tiers) |
| `sessionId`                 | `string`           | Yes      | Current session identifier.                                                                                                                                                                                                                                                                 | All (if reading manifest) |
| `taskId`                    | `string`           | Yes      | Current task identifier.                                                                                                                                                                                                                                                                    | All (if reading manifest) |

**Tier reading requirements:**

- **Minimum** — The runtime does not need to read the adapter manifest at all. It operates purely on stdin/stdout.
- **Standard** — The runtime reads `platformMcpServer.socket` and `connectorServers` to connect to local MCP servers.
- **Full** — Standard fields plus `lifecycleChannel.socket`.

**Forward compatibility:** Runtimes must silently ignore unknown top-level fields. The adapter may add new fields in future versions without incrementing `version`. A `version` increment indicates a breaking change to existing field semantics.

`runtimeMcpServers` slot reserved from v1 for future use by `type:mcp` runtimes accessible via adapter proxy.

#### Runtime Integration Tiers (agent-type only)

- **Minimum** — stdin/stdout binary protocol only. Reads `{type: "message"}` from stdin, writes `{type: "response"}` and `{type: "tool_call"}` to stdout. This is the floor. Zero Lenny knowledge required.
- **Standard** — minimum plus connects to adapter's platform MCP server and connector servers via the adapter manifest. Uses platform capabilities (delegation, discovery, output parts, elicitation). Standard MCP — no Lenny-specific code.
- **Full** — standard plus opens the lifecycle channel. True session continuity, clean interrupt points, mid-session credential rotation via `credentials_rotated` lifecycle message.

**Credential rotation behavior by tier:**

| Tier     | Rotation method                                                                                                                                                                              |
| -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Full     | Gateway calls `RotateCredentials` RPC; adapter sends `credentials_rotated` on lifecycle channel; runtime rebinds provider in-place. No session interruption.                                 |
| Standard | Gateway triggers `Checkpoint` → terminates pod → schedules replacement pod → `AssignCredentials` (new lease) → `Resume`. Session experiences a brief pause; the client sees a reconnect.     |
| Minimum  | Same as Standard: checkpoint + restart. Minimum-tier runtimes that do not support checkpoint lose in-flight context; the gateway restarts the session from the last gateway-persisted state. |

The gateway selects the rotation strategy automatically based on the tier reported in the adapter's `lifecycle_support` handshake (Full) or the absence of a lifecycle channel (Standard/Minimum).

**Full-tier credential rotation protocol:**

The Full-tier rotation via the lifecycle channel follows a strict protocol with timeout and in-flight request handling:

1. **In-flight request completion gate.** Before sending `credentials_rotated` on the lifecycle channel, the adapter MUST wait for any in-flight LLM request to complete (or receive an error from the provider). The adapter tracks active outbound LLM requests via the lease's proxy stream count (proxy mode) or a `request_active` flag set by the runtime on the lifecycle channel (direct mode). New outgoing LLM requests from the runtime are queued by the adapter during the rotation window — they are held until the runtime sends `credentials_acknowledged`, at which point they are released using the new credential. This prevents the runtime from starting a new request with the old credential after the rotation signal has been decided but before it has been delivered. The adapter records the gate wait duration in the `lenny_credential_rotation_inflight_wait_seconds` histogram (labeled by `pool`, `provider`). If the wait exceeds 60 seconds, the adapter emits a `credential_rotation_inflight_wait_long` warning event (fields: `session_id`, `lease_id`, `provider`, `elapsed_seconds`) for operator visibility. The wait is intentionally unbounded: the old credential remains valid, the in-flight request will complete naturally, and imposing a timeout would risk an auth failure on an otherwise successful request. The queued-request impact is bounded because the rotation trigger (`RATE_LIMITED`) means the runtime was already unable to start new LLM calls.

2. **`credentials_acknowledged` timeout.** The adapter enforces a **60-second timeout** on the `credentials_acknowledged` response, matching the checkpoint timeout (Section 4.4). The timeout starts when `credentials_rotated` is sent on the lifecycle channel. If the runtime does not reply within 60 seconds, the adapter:
   - Emits a `credential_rotation_timeout` warning event (fields: `session_id`, `lease_id`, `provider`, `elapsed_seconds`).
   - Falls back to the Standard-tier rotation path: triggers `Checkpoint` → terminates pod → schedules replacement pod → `AssignCredentials` (new lease) → `Resume`.
   - Increments the `lenny_credential_rotation_timeout_total` counter (labeled by `pool`, `provider`, `runtime`).

3. **Old credential grace period.** The old credential is NOT invalidated at the provider (removed from the pool's active set or returned to the pool) until one of: (a) `credentials_acknowledged` is received from the runtime, confirming the rebind is complete; or (b) the 60-second timeout elapses and the fallback path takes over. This ensures that if an in-flight LLM request is still completing with the old credential at the provider, it is not rejected mid-stream due to premature credential invalidation. After the grace period, the old lease is marked `released` and the credential is returned to the pool (or to cooldown, depending on the rotation reason). The metric `lenny_credential_rotation_grace_period_seconds` (histogram, labeled by `pool`, `provider`) records the actual duration between `credentials_rotated` and old credential release.

#### Startup Sequence for `type: agent` Runtimes

1. Pod created by Kubernetes
2. Adapter opens gRPC connection to gateway (mTLS)
3. Adapter writes placeholder manifest (connector servers unknown yet)
4. Adapter signals READY to gateway — pod enters warm pool
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials(leases)` → `StartSession`
6. Adapter writes **final manifest** (connector servers now known from leases; all provider credentials available)
7. Adapter spawns runtime binary
8. Runtime reads manifest, connects to MCP servers (Standard/Full), opens lifecycle channel (Full)
9. Adapter sends `lifecycle_capabilities` (Full); receives `lifecycle_support`
10. Adapter delivers first `{type: "message"}` on stdin

#### Deployment Model

- **Default: Sidecar container** communicating with the agent binary over **abstract Unix sockets** (Linux `\0` namespace — no filesystem path). `shareProcessNamespace: false`. The adapter writes the manifest to a dedicated `emptyDir` volume (`/run/lenny/`) mounted **read-only into the agent container** and read-write into the adapter container. Workspace is a separate `emptyDir` volume (`/workspace/`). No single shared volume carries both communication and data — sockets are abstract (kernel-only), the manifest is read-only to the agent, and workspace is isolated. This minimizes what third-party binary authors need to implement — just a binary that reads/writes on a well-defined socket protocol.
- **Alternative: Embedded** — first-party binaries can embed the adapter directly and expose the same gRPC contract to the gateway.
- Same external contract either way.

**Sidecar vs embedded trade-offs:**

| Aspect                 | Sidecar (default)                                        | Embedded                                   |
| ---------------------- | -------------------------------------------------------- | ------------------------------------------ |
| Complexity for authors | Low — implement stdin/stdout JSON protocol               | High — implement full gRPC contract        |
| Resource overhead      | ~50 MB memory for adapter sidecar                        | None (shared process)                      |
| Language support       | Any language (stdin/stdout)                              | Go only (or language with gRPC support)    |
| Isolation              | Process isolation; abstract sockets + read-only manifest | Single process, shared memory              |
| Recommended for        | Third-party runtimes, community adapters                 | First-party runtimes where latency matters |

> **Note:** Third-party authors should always use the sidecar model. The embedded model is for first-party runtimes where the adapter and agent binary are developed together.

**Health check:** gRPC Health Checking Protocol. The warm pool controller marks a pod as `idle` only after the health check passes.

#### Adapter-Agent Security Boundary

The boundary between the adapter and the agent binary is **untrusted**. A compromised or misbehaving agent binary must not be able to escalate privileges, extract credentials, or manipulate the adapter. The following controls enforce this:

1. **Separate UIDs and connection authentication:** The adapter runs as a different UID than the agent binary (e.g., adapter as UID 1000, agent as UID 1001). Abstract Unix sockets use `SO_PEERCRED` for peer UID verification — the adapter accepts connections only from the expected agent UID. The expected UID is the UID within the pod's user namespace (matching the `runAsUser` in the pod spec), not a host UID — both sidecar containers share the same user namespace, so `SO_PEERCRED` reports consistent UIDs. As a defense-in-depth measure, the adapter also performs a manifest-nonce handshake on connection: the adapter writes a random nonce to the manifest file (read-only to the agent container) before spawning the agent, and the agent must present this nonce as the first message on the socket. This ensures the connecting process has legitimate access to the manifest, independent of UID verification. gVisor `SO_PEERCRED` semantics must be validated in Phase 3.5 security integration tests; if behavior diverges from the host kernel, the nonce handshake serves as the primary authentication control. The manifest volume is mounted read-only into the agent container, preventing tampering. Filesystem-level isolation prevents the agent from reading adapter config or memory.

2. **Adapter-initiated protocol:** The adapter is the protocol initiator — it sends messages and tool-call instructions to the agent and receives responses. The agent cannot initiate arbitrary requests to the adapter.

3. **Untrusted agent responses:** The adapter treats all data received from the agent as untrusted input. It validates, sanitizes, and size-limits all responses before forwarding them to the gateway.

4. **No credential material over socket:** LLM credentials (whether direct lease or proxy URL) are delivered to the agent process via a tmpfs-backed file (`/run/lenny/credentials.json`, mode `0400`, owned by the agent UID) written by the adapter before spawning the runtime binary. Credentials are never passed via environment variables (readable via `/proc`, persist in crash dumps, often captured by framework logging), Unix sockets, or MCP servers. The adapter never sends credential material to the agent post-startup. For proxy mode, only the lease token and proxy URL are written to the file — no real API keys enter the pod.

5. **Agent crash isolation:** If the agent process crashes, the adapter detects it (socket EOF), reports the failure to the gateway, and does not restart the agent. The gateway handles retry at the session level.

6. **Credential-sensitive RPC logging exclusion:** `AssignCredentials` and `RotateCredentials` RPCs carry credential material in their payloads. These RPCs must be excluded from gRPC access log payload capture, OpenTelemetry span attributes, and any request/response logging middleware. Only the RPC name, lease ID, provider type, and success/failure status should be logged — never the `materializedConfig` contents.

7. **MCP server security:** The local MCP servers never expose gateway credentials, mTLS certificates, other sessions, internal Lenny state, or anything about other tenants. The pod sandbox (gVisor/Kata) and network policy are the security boundary, not the protocol. The adapter never advertises the `sampling` MCP capability to the local server.

### 4.8 Gateway Policy Engine

**Role:** Centralized policy evaluation on the request path.

**Physically embedded** in edge gateway replicas (not a separate service). Can be split out later if policy evaluation becomes a scaling or organizational bottleneck.

**Evaluators:**

| Module                      | Scope                                                               |
| --------------------------- | ------------------------------------------------------------------- |
| `AuthEvaluator`             | AuthN/AuthZ, user invalidation                                      |
| `QuotaEvaluator`            | Rate limits, token budgets, concurrency limits                      |
| `DelegationPolicyEvaluator` | Depth, fan-out, `DelegationPolicy` tag matching, budget inheritance |
| `RetryPolicyEvaluator`      | Retry eligibility, resume window                                    |
| `AdmissionController`       | Queue/reject/prioritize, circuit breakers                           |

**Backs onto:** SessionStore (sessions, tasks, delegation tree, lineage, retry state), QuotaStore, TokenStore, UserStateStore, RuntimeRegistry

#### `RequestInterceptor` Extension Point

Formalized interceptor interface at gateway phases: `PreAuth`, `PostAuth`, `PreRoute`, `PreDelegation`, `PreMessageDelivery`, `PostRoute`, `PreToolResult`, `PostAgentOutput`, `PreLLMRequest`, `PostLLMResponse`, `PreConnectorRequest`, `PostConnectorResponse`.

**Interceptor chain execution order:** Interceptors execute in ascending numeric `priority` order (lowest number runs first). When two interceptors share the same priority, built-in interceptors run before external ones; among external interceptors with equal priority, registration order is preserved.

**Built-in interceptors** (with default priorities):

| Interceptor             | Default Priority | Notes                                                                                                                          |
| ----------------------- | ---------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `AuthEvaluator`         | 100              | Always active                                                                                                                  |
| `QuotaEvaluator`        | 200              | Always active                                                                                                                  |
| `ExperimentRouter`      | 300              | Active when experiments are defined (see Section 10.7)                                                                         |
| `GuardrailsInterceptor` | 400              | Disabled by default; deployers wire in external classifiers (AWS Bedrock Guardrails, Azure Content Safety, Lakera Guard, etc.) |

**Short-circuit behavior:** If any interceptor returns `REJECT`, the chain short-circuits immediately — no subsequent interceptors are invoked. The rejection reason is logged and returned to the caller. `MODIFY` results are applied to the payload before passing it to the next interceptor in the chain; `ALLOW` passes the payload through unchanged.

External interceptors are invoked via gRPC (like Kubernetes admission webhooks):

```protobuf
service RequestInterceptor {
  rpc Intercept(InterceptRequest) returns (InterceptResponse);
}

message InterceptRequest {
  string phase = 1;               // e.g. "PreDelegation", "PostAgentOutput"
  string session_id = 2;
  string tenant_id = 3;
  bytes content = 4;              // phase-dependent payload (e.g. TaskSpec.input for PreDelegation)
  map<string, string> metadata = 5;
}

message InterceptResponse {
  enum Action {
    ALLOW = 0;
    REJECT = 1;
    MODIFY = 2;                   // return modified content in `modified_content`
  }
  Action action = 1;
  string reason = 2;              // human-readable; logged and returned to caller on REJECT
  bytes modified_content = 3;     // only used when action = MODIFY
}
```

**Interceptor registration** includes a numeric `priority` field and a `failPolicy`:

| Field        | Type     | Default       | Description                                                                                                                                                                                                                                                             |
| ------------ | -------- | ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `priority`   | int32    | 500           | Execution order; lower runs first. External interceptors must use priority > 100 (priorities 1–100 are reserved for built-in security-critical interceptors). The gateway rejects registration of external interceptors with priority ≤ 100 at configuration load time. |
| `failPolicy` | string   | `fail-closed` | Behavior on timeout or error: `fail-closed` (treat as REJECT) or `fail-open` (treat as ALLOW)                                                                                                                                                                           |
| `timeout`    | duration | 500ms         | Per-interceptor deadline                                                                                                                                                                                                                                                |

**Priority reservation.** Priorities 1–100 are reserved exclusively for built-in security-critical interceptors (`AuthEvaluator` at 100). External interceptors must register at priority > 100. The gateway validates this constraint at interceptor registration time and rejects any external interceptor configuration with `priority ≤ 100`, returning a `INVALID_INTERCEPTOR_PRIORITY` error. This prevents external interceptors from running before authentication completes, which would allow `MODIFY` actions to alter requests before auth evaluation or `fail-open` interceptors to produce audit entries from unauthenticated contexts.

**Authentication context availability.** The `InterceptRequest.metadata` map contains different context depending on the interceptor's position relative to `AuthEvaluator`:

- **Priority ≤ 100 (built-in only):** `metadata` contains transport-level info only (source IP, TLS client cert DN). No authenticated identity is available.
- **Priority > 100 (all external interceptors):** `metadata` contains authenticated identity (`user_id`, `tenant_id`, roles/claims) populated by `AuthEvaluator`. External interceptors can always rely on authentication context being present.

**Timeout and failure mode:** External interceptors have a configurable timeout (default: 500ms). On timeout or error, the behavior is controlled by `failPolicy` on the interceptor registration: `fail-closed` (default) or `fail-open`. The default is fail-closed so that a misbehaving interceptor cannot silently bypass policy. Deployers who prefer availability over strict enforcement may override to `fail-open` on a per-interceptor basis.

The `PreDelegation` phase fires before the gateway processes a `delegate_task` call, providing the full `TaskSpec.input` as the content payload. This is the hook point for the `contentPolicy.interceptorRef` field in `DelegationPolicy` (Section 8.3).

The `PreMessageDelivery` phase fires before the gateway delivers an inter-session message (`lenny/send_message`) to the target session. The content payload is the message body. This allows deployers to apply the same content scanning (prompt injection detection, content safety) to agent-to-agent messages as they apply to delegation inputs. The `contentPolicy.maxInputSize` limit from the target session's effective `DelegationPolicy` is enforced on inter-session messages in addition to delegation inputs. The `contentPolicy.interceptorRef`, if configured, is invoked at this phase with the message content.

**Interceptor phase payload specification.** The `content` field of `InterceptRequest` carries a phase-specific payload. The following table defines the payload type, contents, and `MODIFY` semantics for each phase:

| Phase                | `content` payload type                                                                         | Description                                                                                                                                                                 | `MODIFY` permitted changes                                                                                                                                                                             |
| -------------------- | ---------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `PreAuth`            | Serialized inbound MCP request envelope (JSON)                                                 | The raw client request before authentication. `metadata` contains transport-level info (IP, TLS client cert DN) but no authenticated identity.                              | May modify request headers or metadata fields. May **not** alter the request method/type. `MODIFY` at this phase is unusual — most interceptors should `ALLOW` or `REJECT` only.                       |
| `PostAuth`           | Serialized inbound MCP request envelope (JSON)                                                 | Same request envelope as `PreAuth`, now with `metadata` populated with authenticated identity (`user_id`, `tenant_id`, roles/claims).                                       | May modify `metadata` fields (e.g., inject additional claims, override tenant routing hints). May **not** alter authenticated identity fields (`user_id`, `tenant_id`).                                |
| `PreRoute`           | Serialized `TaskSpec` (JSON)                                                                   | The task specification after authentication and before runtime selection. Contains `input` (`OutputPart[]`), requested runtime, workspace files, and delegation parameters. | May modify `TaskSpec.input` content (e.g., prepend system instructions, redact sensitive data). May modify runtime hints. May **not** alter `tenant_id` or `user_id`.                                  |
| `PreDelegation`      | Serialized `TaskSpec.input` (`OutputPart[]`, JSON)                                             | The delegation input payload before the gateway processes a `delegate_task` call. This is the hook point for `contentPolicy.interceptorRef`.                                | May modify, redact, or truncate input content. May add safety preambles. May **not** alter delegation metadata (parent session ID, budget parameters).                                                 |
| `PreMessageDelivery` | Serialized message body (`OutputPart[]`, JSON)                                                 | The inter-session message content before delivery to the target session (`lenny/send_message`).                                                    | May modify, redact, or truncate message content. Same permitted changes as `PreDelegation`.                                                                                                            |
| `PostRoute`          | Serialized `TaskSpec` with resolved runtime metadata (JSON)                                    | The task specification after runtime selection, including the resolved runtime name, runtime tags, and credential pool assignment.                                          | May modify runtime-specific parameters. May **not** change the resolved runtime or credential assignment (use `PreRoute` to influence runtime selection instead).                                      |
| `PreToolResult`      | Serialized tool result object (JSON): `{ id: string, content: OutputPart[], is_error?: bool }` | The result of a tool call before it is delivered back to the agent. `id` correlates with the originating `tool_call.id`.                                                    | May modify `content` (redact sensitive data from tool output, transform structured results). May set `is_error` to true to force-fail a tool call. May **not** alter `id`.                             |
| `PostAgentOutput`    | Serialized `OutputPart[]` (JSON)                                                               | The agent's response output before delivery to the client (or to the parent session in a delegation). Includes text, structured data, images, and annotations.              | May modify, redact, or truncate output content. May remove or replace individual `OutputPart` entries. May add compliance annotations. May **not** suppress delivery entirely (use `REJECT` for that). |
| `PreLLMRequest`      | Serialized upstream LLM API request body (JSON)                                                | The full LLM provider request before the gateway forwards it to the upstream endpoint. Proxy mode only — does not fire in direct delivery mode. Includes `model`, `messages`/`prompt`, parameters. Credential headers are injected by the proxy after this phase. | May modify any request body field (model, messages, parameters). May **not** alter credential/authorization headers (those are injected by the proxy after the interceptor runs). |
| `PostLLMResponse`    | Serialized upstream LLM API response metadata (JSON)                                           | The LLM provider response before forwarding to the pod. For streaming responses, fires once on the initial response metadata — individual stream chunks are not intercepted. Proxy mode only. | May modify response metadata fields. For streaming, modifications apply to initial metadata only — individual stream chunks pass through unmodified. |
| `PreConnectorRequest` | Serialized MCP tool call (JSON): `{ tool_name, arguments, connector_id }` | The outgoing tool call before the gateway proxies it to the external connector MCP server. Fires on all connector calls (all are gateway-proxied). | May modify `arguments` (redact, transform). May **not** alter `tool_name` or `connector_id` (would bypass DelegationPolicy connector access control). |
| `PostConnectorResponse` | Serialized MCP tool result (JSON): `{ tool_name, connector_id, content: OutputPart[], is_error? }` | The connector's response before forwarding to the pod. | May modify `content` (redact, transform). May set `is_error` to true. May **not** alter `tool_name` or `connector_id`. |

**LLM interceptor phases.** The `PreLLMRequest` and `PostLLMResponse` phases fire exclusively in the LLM reverse proxy path (Section 4.9, proxy mode). They have no effect when credentials are delivered directly to the pod (`deliveryMode: direct`), because in direct mode the gateway has no visibility into LLM API traffic. The default timeout for LLM interceptor phases is **100ms** (shorter than the 500ms default for other phases) because they are on the LLM call hot path — every millisecond of interceptor latency adds directly to LLM request latency. Deployers may override this via the `timeout` field on the interceptor registration, but should be aware of the latency impact. For `PostLLMResponse` with streaming responses: the interceptor receives the initial response headers/metadata and the first data chunk. It does **not** buffer the full stream — buffering would destroy streaming latency and is architecturally unacceptable. Deployers who need full-response content scanning should implement it as an async post-processing step (e.g., a `PostAgentOutput` interceptor or an external audit pipeline), not in the synchronous `PostLLMResponse` interceptor. On `REJECT`, `PreLLMRequest` returns `LLM_REQUEST_REJECTED` to the pod; `PostLLMResponse` returns `LLM_RESPONSE_REJECTED` (see error catalog, Section 15.1). Typical use cases: model governance (allowlist/denylist models via `PreLLMRequest`), prompt safety scanning, PII redaction from prompts, cost controls (reject expensive models), compliance logging, and response content filtering.

**Connector interceptor phases.** The `PreConnectorRequest` and `PostConnectorResponse` phases fire on all gateway-proxied connector calls. Unlike LLM interceptors which only apply in proxy mode, connector interceptors always apply — pods cannot reach external MCP connector servers directly (NetworkPolicy enforced), so all connector traffic flows through the gateway. The default timeout for connector interceptor phases is **200ms** (connectors are on the tool-call path which is latency-sensitive but less extreme than streaming LLM calls). The `DelegationPolicy`'s `connectorSelector` controls which connectors a session may access; these interceptors govern the **content** of calls to already-authorized connectors. On `REJECT`, `PreConnectorRequest` returns `CONNECTOR_REQUEST_REJECTED` to the pod; `PostConnectorResponse` returns `CONNECTOR_RESPONSE_REJECTED` (see error catalog, Section 15.1). Typical use cases: PII redaction before data reaches external systems, audit logging of sensitive tool calls, blocking destructive operations (e.g., reject `delete` operations on a GitHub connector), compliance controls on data leaving the platform, and redacting secrets or sensitive data from connector responses.

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

An admin-managed, **tenant-scoped** set of credentials for a given provider. Each `CredentialPool` belongs to exactly one tenant; the `CredentialPoolStore` tables carry a `tenant_id` column and are covered by the same RLS policies as all other tenant-scoped tables (see Section 4.2). A `tenant-admin` can create, update, and delete credential pools for their own tenant via the admin API. A `platform-admin` can manage credential pools across all tenants. Cross-tenant credential pool access is impossible by construction — RLS enforces this at the database level, and the gateway validates `tenant_id` on every pool operation.

Deployers register pools via configuration:

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

Pool sizing scales with tier — at Tier 3, deployers may need hundreds of credentials per pool. See Section 17.8.

#### Pre-Claim Credential Availability Check

Before claiming a warm pod (Section 7.1, step 4), the gateway computes the intersection of the Runtime's `supportedProviders` and the tenant's `credentialPolicy.providerPools` keys. For each provider in the intersection, it verifies that the resolved credential source (pool, user, or fallback chain) has at least one assignable credential, evaluating pool utilization (`active leases < maxConcurrentSessions` for at least one credential), cooldown status, and health scores. If **no provider** in the intersection has an available credential, the gateway rejects the request immediately with `CREDENTIAL_POOL_EXHAUSTED` (category: `POLICY`) — no pod is claimed, preventing a pod from being wasted on a session that would fail at credential assignment. The session can proceed if at least one provider has an available credential.

Because the availability check and the actual lease assignment are not atomic, a race condition exists where a credential becomes unavailable between the check and the assignment. If this occurs, the gateway releases the claimed pod back to the warm pool and returns `CREDENTIAL_POOL_EXHAUSTED` to the client. The metric `lenny_gateway_credential_preclaim_mismatch_total` (counter, labeled by pool and provider) tracks how often the pre-claim check passes but the subsequent assignment fails, letting operators detect pool contention and tune pool sizing.

#### Credential Lease

A session-scoped assignment from the gateway to a runtime pod. A session may hold **multiple leases** — one per provider in the intersection of Runtime `supportedProviders` and tenant `credentialPolicy.providerPools`:

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

The `source` field indicates how the credential was obtained: `pool` (admin-managed pool), `user` (user-scoped pre-registered credential). The runtime receives the `materializedConfig` — a provider-specific bundle with everything needed to authenticate. It never receives the pool's root secret or long-lived key. Each provider's lease rotates independently via its own fallback chain; `maxRotationsPerSession` applies to the total rotation count across all providers.

#### Credential Policy

Attached to the tenant configuration, controls how credentials are selected and managed across all sessions for that tenant. The Runtime definition declares what providers it *can* use (`supportedProviders`, `credentialCapabilities` — see Section 5.1); the tenant-level `credentialPolicy` declares what credentials *are* available and how they are sourced. At session creation, the gateway intersects Runtime capabilities with tenant policy (see "Intersection at session creation" below).

```yaml
# Tenant-level credential policy
credentialPolicy:
  preferredSource: pool  # pool | user | prefer-user-then-pool | prefer-pool-then-user
  providerPools:
    anthropic_direct:
      defaultPool: claude-direct-prod
      fallback:
        order: [claude-direct-prod, claude-direct-backup]
    aws_bedrock:
      defaultPool: bedrock-us-east-prod
      fallback:
        order: [bedrock-us-east-prod, bedrock-us-west-prod]
  fallback:
    cooldownOnRateLimit: 60s
    maxRotationsPerSession: 3  # total across all providers in a session
  userCredentialsEnabled: true  # whether user-scoped credentials (pre-registered via POST /v1/credentials) are used
```

**Intersection at session creation.** When a session is created, the gateway computes the set of authorized providers as the intersection of the Runtime's `supportedProviders` (Section 5.1) and the tenant's `credentialPolicy.providerPools` keys. Only providers present in both sets are eligible for credential assignment. If the intersection is empty, session creation fails with `CREDENTIAL_POOL_EXHAUSTED`. The gateway then assigns a `CredentialLease` for each provider in the intersection that has an available credential (see "Multi-provider lease assignment" below). A session can start if at least one provider in the intersection has a successfully assigned credential — providers without available credentials are skipped, and the session receives credentials only for the providers that were successfully assigned.

#### Three Credential Modes

| Mode                     | How It Works                                                                                                                                                                                                                                                                                                                                             | Use Case                                              |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| **Pool (admin-managed)** | Gateway picks a credential from the per-provider pool mapping (`providerPools`) using the assignment strategy. Runtime gets a lease per provider.                                                                                                                                                                                                        | Shared team/org API keys, service accounts            |
| **User-scoped**          | User pre-registers their own credential via `POST /v1/credentials`. Token Service stores it with the same envelope encryption via KMS used for OAuth refresh tokens (Section 4.3) — user-supplied API keys are T4 Restricted data and receive identical encryption-at-rest, key rotation, and access-control treatment. Runtime gets a lease. | "Bring your own API key", user-specific Bedrock roles |
| **Fallback chain**       | Policy determines precedence. E.g., prefer user credential, fall back to pool on failure.                                                                                                                                                                                                                                                                | Flexible enterprise deployments                       |

> The `preferredSource` field determines which mode is used. There are effectively three configurations: pool-only (`preferredSource: pool`), user-only (`preferredSource: user`), or fallback chain (`preferredSource: prefer-user-then-pool` or `prefer-pool-then-user`). Per-provider fallback across pools is configured via each provider's `fallback.order` list within `providerPools`.

> **User-scoped credential storage:** User-supplied credentials (registered via `POST /v1/credentials`) are stored in `TokenStore` (Postgres) using the same envelope encryption via KMS as OAuth refresh tokens (Section 4.3). Each credential is encrypted with a per-tenant data encryption key (DEK), itself wrapped by the KMS key encryption key (KEK). User-scoped credentials are subject to the same KMS key rotation procedure (Section 10.5), the same access-control boundaries (tenant-scoped RLS), and the same data classification (T4 Restricted, Section 12.6). The Token Service decrypts user-scoped credentials on demand when materializing a credential lease — the plaintext credential is never persisted outside the Token Service's in-memory processing context.

#### Pre-Authorized Credential Flow

The pre-authorized flow allows users to register their own credentials once per provider, after which every session automatically resolves the credential based on the authenticated user's identity. This works identically across all client interfaces (MCP, REST, OpenAI-compatible, A2A) — no interactive channel is required.

**User credential management endpoints:**

| Method   | Endpoint                           | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| -------- | ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/v1/credentials`                  | Register a credential. Body: `provider` (string, must match a known `CredentialProvider` type), `credential` (the secret material — API key, access token, etc.), optional `label` (human-readable name). The Token Service encrypts the credential using the same envelope encryption as OAuth tokens (per-tenant DEK, KMS KEK). Returns: `credential_ref` (opaque ID), `provider`, `label`, `created_at`. The secret material is never returned after registration. |
| `GET`    | `/v1/credentials`                  | List the authenticated user's registered credentials. Returns: array of `{credential_ref, provider, label, created_at, last_used_at}`. No secret material.                                                                                                                                                                                                                                                                                                            |
| `DELETE` | `/v1/credentials/{credential_ref}` | Remove a registered credential. Active sessions using this credential are not affected (they hold a lease); new sessions will no longer resolve it.                                                                                                                                                                                                                                                                                                                   |

**Scoping rules:**

- Credentials are scoped to `(tenant_id, user_id, provider)`. A user can register at most one credential per provider (re-registering for the same provider replaces the previous one).
- `tenant_id` and `user_id` are derived from the authenticated JWT — never supplied by the client.
- The Token Service validates that `provider` matches a `CredentialProvider` type known to the platform. Unknown providers are rejected with `VALIDATION_ERROR`.

**Resolution at session creation:**

- When a session is created and the `credentialPolicy.preferredSource` includes user credentials (`prefer-user-then-pool`, `prefer-pool-then-user`, or `user` only), the gateway calls the Token Service to look up a credential for `(tenant_id, user_id, provider)` for each provider in the intersection of the Runtime's `supportedProviders` and the tenant's `credentialPolicy.providerPools`.
- If found: the Token Service decrypts it and materializes a `CredentialLease` — same as pool-managed credentials.
- If not found: depends on fallback configuration. With fallback enabled, falls through to pool. Without fallback, session creation fails with `USER_CREDENTIAL_NOT_FOUND` (see error catalog, Section 15.1).
- `last_used_at` is updated on each successful resolution.

**`userCredentialsEnabled` semantics:**

- `userCredentialsEnabled: true` — the gateway resolves user-scoped credentials from the credential store (registered via `POST /v1/credentials`). If no credential is registered for the requested provider, the behavior depends on the fallback configuration: with fallback enabled (`preferredSource: prefer-user-then-pool`), falls through to pool; without fallback (`preferredSource: user`), session creation fails with `USER_CREDENTIAL_NOT_FOUND`.
- `userCredentialsEnabled: false` — user-scoped credentials are not used regardless of what is registered.

> **Why credential elicitation via MCP is not supported.** Credential elicitation via the MCP `lenny/request_input` channel is intentionally not supported. The MCP message path is subject to logging, tracing, interceptor inspection, and transcript persistence — credential material flowing through this path would be exposed to all of these systems. User-scoped credentials must be registered via the dedicated `POST /v1/credentials` endpoint, which has explicit logging exclusions and delivers credentials directly to the Token Service without traversing the MCP message chain.

**Storage:** User credentials are stored in the `TokenStore` (Postgres) with the same envelope encryption, RLS, and access controls described in the "User-scoped credential storage" callout above. See that callout for the full encryption lifecycle.

**Security considerations:**

- The `POST /v1/credentials` endpoint carries secret material. It MUST be excluded from HTTP access log payload capture, OpenTelemetry span attributes, and request/response logging middleware — the same treatment as `AssignCredentials` and `RotateCredentials` RPCs (Section 4.7 item 6). Only the `credential_ref`, `provider`, and success/failure status should be logged.
- A `credential.registered` audit event is emitted on registration (fields: `tenant_id`, `user_id`, `provider`, `credential_ref`). A `credential.deleted` audit event is emitted on deletion.

#### Fallback Flow

Fallback operates **per-provider**. When the runtime reports a credential failure for a specific provider, only that provider's fallback chain is walked. Other providers' leases are unaffected.

```
1. Runtime reports RATE_LIMITED (or AUTH_EXPIRED, PROVIDER_UNAVAILABLE) to gateway,
   identifying the affected provider
2. Gateway marks the affected provider's lease as degraded, records cooldown timestamp
3. Gateway increments the session's rotation counter (shared across all providers).
   If the counter exceeds maxRotationsPerSession (default: 3), the fallback chain
   is exhausted:
   - The session is terminated with CREDENTIAL_FALLBACK_EXHAUSTED (category: POLICY)
   - A credential.fallback_exhausted audit event is emitted (fields: session_id,
     rotation_count, last_failure_reason, fallback_chain_attempted)
   - The metric lenny_gateway_credential_fallback_exhausted_total (counter,
     labeled by pool, provider, failure_reason) is incremented
   - The client receives a terminal error; no further rotation attempts are made
4. Gateway evaluates the affected provider's fallback chain (from
   credentialPolicy.providerPools.{provider}.fallback.order):
   a. Same provider, different credential from pool?
   b. User credential available as fallback?
5. Gateway issues replacement CredentialLease for the affected provider only
6. Gateway pushes the replacement credential to runtime via RotateCredentials RPC,
   targeting the specific provider's lease
7. Delivery depends on runtime tier and restart requirements:
   - Full: adapter delivers via lifecycle channel; runtime rebinds the specific
     provider in-place. Other providers' credentials are untouched.
   - Standard/Minimum: gateway triggers Checkpoint → pod restart → Resume with
     new lease map (see Section 4.7 tier table)
```

**Rotation mode resolution.** The runtime's `credentialCapabilities.hotRotation` declaration (Section 5.1) is the authoritative signal for whether credentials can be rotated in-place, because only the runtime knows whether it can rebind credentials without a restart. The gateway resolves the effective restart requirement at session start:

1. If the runtime declares `credentialCapabilities.hotRotation: true`, the gateway uses **hot rotation** (lifecycle channel delivery, no restart). The runtime has affirmed it can handle in-place rebinding.
2. If the runtime declares `credentialCapabilities.hotRotation: false` (or omits `credentialCapabilities` entirely), the gateway uses **checkpoint-and-restart rotation**. The runtime cannot handle hot rotation.

The resolved rotation mode is recorded in the session's metadata at creation time and is visible in the `credential.leased` audit event (`rotationMode: "hot" | "restart"`).

#### LLM Reverse Proxy

For API-key-based providers that do not support short-lived token exchange (e.g., providers where the "short-lived" key is really just the long-lived key with a TTL wrapper), the gateway can run a **credential-injecting reverse proxy** so the real API key never enters the pod:

1. Instead of receiving a materialized API key, the pod receives a lease token and a proxy URL (e.g., `https://gateway-internal:8443/llm-proxy/{lease_id}`).
2. The pod sends LLM API requests to the proxy URL using the lease token for authentication.
3. The gateway proxy validates the lease token, runs the `PreLLMRequest` interceptor chain (Section 4.8) on the request body, injects the real API key into the upstream request headers, and forwards the request to the LLM provider. If a `PreLLMRequest` interceptor returns `REJECT`, the proxy returns `LLM_REQUEST_REJECTED` to the pod without contacting the upstream provider.
4. On receiving the upstream response, the proxy runs the `PostLLMResponse` interceptor chain (Section 4.8) before forwarding the response to the pod. For streaming responses, the interceptor fires once on the initial response metadata — stream chunks are forwarded without per-chunk interception. If a `PostLLMResponse` interceptor returns `REJECT`, the proxy returns `LLM_RESPONSE_REJECTED` to the pod.
5. The real API key never enters the pod's memory or environment.

This is a **per-provider** configuration — deployers choose for each credential pool whether to use direct credential leasing (simpler, lower latency) or proxy mode (higher security, adds one network hop). The two modes can coexist across different pools. **Proxy mode is the recommended default for multi-tenant deployments**, since the real API key never enters the pod and a compromised agent cannot exfiltrate it. Direct mode is appropriate for single-tenant and development deployments where simplicity and lower latency are preferred.

The proxy enforces the same rate limits and budget constraints as direct leasing. When a lease expires or is revoked, the proxy immediately rejects requests — there is no window of exposure where a compromised pod could continue using a stale key.

> **Warning — `direct` + `standard` (runc) isolation:** Combining `deliveryMode: direct` with `standard` isolation is a dangerous configuration. In `standard` isolation, a container escape gives the attacker access to materialized credential material on the host. In multi-tenant deployments, deployers **must** use `proxy` delivery mode or require `sandboxed`/`microvm` isolation when using `direct` mode. The controller emits a warning event (`DirectModeWeakIsolation`) when this combination is detected in a pool's target RuntimeClass.

> **Monitoring direct-mode usage:** Deployers should monitor the metric `lenny_gateway_credential_leases_active{delivery_mode="direct"}` and set alerts when direct-mode lease counts exceed expected thresholds. The audit event `credential.leased` (Section 12.4) includes a `deliveryMode` field, enabling compliance teams to track and review all direct-mode credential deliveries. In regulated environments, consider requiring explicit admin approval for pools configured with `deliveryMode: direct`.

**Subsystem isolation:** The LLM Proxy runs within the gateway's fourth internal subsystem boundary (see Section 4.1), with its own goroutine pool, concurrency limits, circuit breaker, and per-subsystem metrics (`lenny_gateway_llm_proxy_active_connections`, `lenny_gateway_llm_proxy_request_duration_seconds`, `lenny_gateway_llm_proxy_circuit_state`). This ensures that a surge in proxy traffic or an upstream provider outage cannot starve the Stream Proxy, Upload Handler, or MCP Fabric subsystems.

**LLM Proxy circuit breaker open-state behavior:** When the LLM Proxy circuit breaker transitions to **open** state (tracked by `lenny_gateway_llm_proxy_circuit_state = 2`), the following behavior applies:

1. **All new LLM proxy requests are immediately rejected** with a `PROVIDER_UNAVAILABLE` error. The gateway does NOT silently hang or wait for timeout.
2. **The adapter receives a `PROVIDER_UNAVAILABLE` event** on the control channel. The adapter relays this to the runtime as a tool-result error with code `PROVIDER_UNAVAILABLE`, enabling the agent to handle the failure gracefully (e.g., inform the user, retry later, or fall back to a different provider if available via the fallback chain).
3. **In-flight streaming responses that were established before the circuit opened continue until completion or upstream failure** — the circuit breaker applies only to new outbound connections, not to already-established streams.
4. **Half-open retry:** After a configurable cooldown period (default: 30s, configurable via `llmProxy.circuitBreaker.halfOpenInterval`), the circuit transitions to half-open and allows a single probe request. If the probe succeeds, the circuit closes; if it fails, the circuit returns to open and the cooldown resets.
5. **Alert coverage:** The `GatewaySubsystemCircuitOpen` Warning alert (Section 16.5) fires when the LLM Proxy circuit breaker (or any other subsystem circuit breaker) remains in open state for > 60s. No separate `LLMProxyCircuitOpen` alert is needed — the subsystem label on `lenny_gateway_{subsystem}_circuit_state` provides per-subsystem granularity.

```yaml
credentialPools:
  - name: claude-direct-prod
    provider: anthropic_direct
    deliveryMode: proxy # proxy | direct (default: proxy for multi-tenant, direct for single-tenant)
    proxyEndpoint: https://gateway-internal:8443/llm-proxy
    # ... other pool config unchanged
```

> **Proxy endpoint transport security:** The proxy endpoint **must** use TLS (`https://`). It shares the same mTLS certificate infrastructure as the internal gRPC control plane (Section 4.3), so lease tokens are always encrypted in transit. The controller **rejects** pool registrations where `proxyEndpoint` uses an `http://` scheme and emits a validation error (`InvalidProxyEndpointScheme`). This ensures that proxy mode's security guarantee — real API keys never leave the gateway — is not undermined by plaintext lease-token transmission on the cluster network.
>
> **Future hardening (post-v1):** Lease tokens are not currently bound to the pod's SPIFFE identity. A captured lease token could theoretically be replayed from a different pod that also has a valid mTLS certificate. In practice, the token is only visible inside the pod (tmpfs, mode 0400) and the pod is sandboxed, making extraction unlikely. A future hardening pass could bind each lease token to the issuing pod's SPIFFE URI at `AssignCredentials` time and validate the binding on every proxy request, eliminating cross-pod replay entirely.

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

**Tenant and user isolation:** Semantic cache implementations MUST scope all cache lookups and writes to the `tenant_id` of the current session. Cross-tenant cache hits are forbidden regardless of semantic similarity. The effective cache key space is `(tenant_id, query_embedding, model, provider)` at minimum. When the credential source is `user`-scoped (i.e., the user provides their own API key), the cache key MUST additionally include `user_id`, since responses may reflect user-specific context or permissions. The default Redis-backed implementation inherits tenant key isolation from the Redis wrapper layer (Section 12.4), but pluggable implementations must enforce equivalent scoping independently. An integration test (`TestSemanticCacheTenantIsolation`) must verify that a cached response written for tenant A is not returned for a semantically identical query from tenant B.

#### `CredentialRouter` Interface

The CredentialRouter is a pluggable interface invoked at two points: (1) session creation (Section 7.1, step 6) when the gateway assigns initial credential leases, and (2) rotation time (Fallback Flow, step 4) when the gateway selects a replacement credential for a failed provider.

**Input:**

| Field | Type | Description |
| --- | --- | --- |
| `tenantId` | string | Tenant making the request |
| `userId` | string | Authenticated user |
| `provider` | string | The provider being assigned (e.g., `anthropic_direct`) |
| `allowedPools` | list of pool descriptors | Pools eligible for this provider, each with current utilization stats (active leases, max capacity, health score) |
| `userCredentialAvailable` | boolean | Whether a user-scoped credential exists for this provider |
| `preferredSource` | string | From the tenant's `credentialPolicy.preferredSource` |
| `rotationContext` | object (nullable) | Present only during rotation: includes `previousPoolId`, `previousCredentialId`, `failureReason`, `rotationCount` |
| `hints` | map of string to string | Deployer-extensible key-value map. Populated from `runtimeOptions` at session creation or from static hints configured on pools. Examples: `model`, `cost_tier`, `region` |

**Output:**

| Field | Type | Description |
| --- | --- | --- |
| `source` | string | `pool` or `user` |
| `poolId` | string (nullable) | Selected pool ID (required when `source` is `pool`) |
| `strategyOverride` | string (nullable) | If set, overrides the pool's `assignmentStrategy` for this assignment |

**Built-in assignment strategies.** The default CredentialRouter uses the pool's `assignmentStrategy` field to select a credential within the chosen pool:

| Strategy | Behavior |
| --- | --- |
| `least-loaded` | Selects the credential with the fewest active leases. Default. |
| `round-robin` | Even distribution across credentials in the pool |
| `sticky-until-failure` | Same credential for all sessions until a failure event triggers rotation |

Deployers who need cost-aware, latency-based, or intent-based routing implement a custom CredentialRouter. The `hints` map is the primary extension point — deployers pass deployment-specific context (e.g., `model`, `cost_tier`, `region`) without modifying the core interface.

**External LLM routing proxy integration.** For deployers who want external model routing, cost optimization, or provider switching, the recommended pattern is to deploy an LLM routing proxy (e.g., LiteLLM, Portkey, or a custom gateway) as a sidecar or cluster service. Configure a `CredentialPool` with a custom `CredentialProvider` that mints `materializedConfig` entries with `baseUrl` pointing to the routing proxy. The runtime (or Lenny's LLM proxy) sends LLM requests to the external proxy, which handles model routing, cost optimization, and provider switching internally. For model governance specifically, deployers can use `PreLLMRequest` interceptors (Section 4.8) as a lighter-weight alternative to an external routing proxy — e.g., rejecting disallowed models or rewriting model fields before the request reaches the provider. The two approaches can be combined: `PreLLMRequest` interceptors for policy enforcement (allowlist/denylist) and an external proxy for routing intelligence and cost optimization. This pattern keeps Lenny's credential governance intact while providing flexible model governance options.

#### Credential Governance Boundaries

This section clarifies what Lenny governs versus what is delegated to the runtime, and how the two delivery modes affect governance scope.

**What Lenny governs:**

- **Which providers a runtime can access** — the intersection of the Runtime's `supportedProviders` and the tenant's `credentialPolicy.providerPools` keys determines available providers.
- **Which credentials are used** — pool assignment via the CredentialRouter, user credentials via the pre-authorized flow, and fallback chain ordering are all gateway-controlled.
- **Connector call content** — all connector traffic is gateway-proxied, so connector call arguments and responses are interceptable via `PreConnectorRequest` and `PostConnectorResponse` interceptors (Section 4.8). There is no direct-mode bypass for connectors.

**What Lenny delegates to the runtime:**

- **Which model to call for a given task** — the runtime selects the model based on its own logic (e.g., agent configuration, task complexity). Lenny does not govern model selection by default. However, in proxy mode, deployers can enforce model policies via `PreLLMRequest` interceptors (Section 4.8) — e.g., allowlisting/denylisting specific models, or redirecting requests to approved models. Alternatively, deployers can enforce model policies via an external LLM routing proxy (e.g., LiteLLM, Portkey).
- **Cost optimization across providers/models** — the runtime (or an external routing proxy) handles cost-aware routing. Lenny supports this via CredentialRouter hints and multi-provider credential maps.

**Proxy mode vs. direct mode:**

| Aspect | Proxy mode | Direct mode |
| --- | --- | --- |
| Credential injection | Gateway injects; key never enters pod | Pod receives materialized key |
| Request visibility | Gateway can inspect/audit LLM requests | Gateway has no visibility |
| LLM request/response interception | `PreLLMRequest` and `PostLLMResponse` interceptors fire on every proxied LLM call (Section 4.8) | No interception — gateway has no visibility into LLM traffic |
| Recommended for | Multi-tenant, regulated environments | Single-tenant, development, low-latency |

#### Emergency Credential Revocation

When a single credential within a pool is suspected compromised (e.g., `key-2` in `claude-direct-prod` has been exfiltrated), operators must be able to pull that specific credential immediately — without deleting the entire pool or waiting for natural lease TTL expiry. During the TTL window, the compromised key would continue to be injected into upstream LLM requests for every session currently holding a lease against it.

**Revocation endpoint:**

```
POST /v1/admin/credential-pools/{poolId}/credentials/{credId}/revoke
```

Request body (optional):

```json
{
  "reason": "suspected_exfiltration",
  "note": "key-2 appeared in logs — rotating immediately"
}
```

The operation is synchronous and MUST:

1. Mark the credential as `revoked` in `CredentialPoolStore` (status: `revoked`, `revokedAt`, `revokedBy`, `revocationReason`).
2. Look up all active leases backed by `credId` (via `CredentialPoolStore` lease index).
3. For each active lease: terminate it immediately via in-process signal or Redis pub/sub (matching the certificate deny-list pattern from Section 10.3). The credential ID is added to an **in-memory credential deny list** on every gateway replica, propagated via Redis pub/sub with Postgres `LISTEN/NOTIFY` as fallback. Entries in the deny list expire when the credential's natural lease TTL lapses — keeping the list small.
4. For proxy mode: the LLM proxy checks the deny list on every upstream request. Any request presenting a lease backed by a denied credential is immediately rejected with `CREDENTIAL_REVOKED` (category: `SECURITY`) — there is no window where the compromised key continues to reach the provider.
5. For direct-delivery mode: the gateway sends a `RotateCredentials` RPC to all pods holding leases against `credId`, triggering the standard rotation fallback flow (Section 4.9 Fallback Flow). The pod's lease is invalidated; it must acquire a new lease from a different credential in the pool (or the fallback chain) to continue.
6. Emit a `credential.revoked` audit event (fields: `pool_id`, `credential_id`, `revoked_by`, `reason`, `active_leases_terminated`).
7. Return `200 OK` with a summary: `{ "revokedCredential": "key-2", "leasesTerminated": 3, "propagatedAt": "2026-04-05T..." }`.

**Force-rotate all credentials in a pool:**

```
POST /v1/admin/credential-pools/{poolId}/revoke
```

Revokes all credentials in the pool simultaneously. All active sessions using this pool are rotated to their fallback chain, or terminated with `CREDENTIAL_POOL_EXHAUSTED` if no fallback is available. Use when the entire pool is suspected compromised.

**Credential deny list:** Mirrors the certificate deny-list mechanism from Section 10.3. The deny list is propagated across all gateway replicas via Redis pub/sub. Each entry is keyed by `(poolId, credentialId)` and expires automatically when the last active lease against that credential reaches its natural TTL expiry. The `CredentialPoolStore` persists the `revoked` status durably so newly started gateway replicas rebuild their deny list on startup by querying for credentials in `revoked` state with active-or-recent leases.

**Emergency revocation runbook:** See Section 17.7 (operational runbooks). Steps: (1) identify the compromised credential ID from audit logs (`credential.leased` events showing `credentialId`); (2) call the revocation endpoint; (3) verify `leasesTerminated` count matches expected active sessions; (4) confirm the `CredentialCompromised` alert clears within 60s (indicating revocation propagation succeeded); (5) add a replacement credential to the pool via `PUT /v1/admin/credential-pools/{poolId}`; (6) rotate the underlying secret (Kubernetes Secret or external secrets manager) before re-enabling the credential ID.

#### Security Boundaries

- Long-lived credentials (API keys, IAM role ARNs, service account keys) live **only** in the Token Service and Kubernetes Secrets
- Pods receive **materialized short-lived credentials** (scoped tokens, STS sessions) via the `AssignCredentials` RPC, delivered to the agent as a tmpfs-backed file (mode `0400`) — never via environment variables
- Leases are **revocable** — on user invalidation, or on emergency credential revocation via `POST /v1/admin/credential-pools/{poolId}/credentials/{credId}/revoke`, the gateway immediately terminates affected leases and the runtime loses access
- Credential material is **never logged** in audit events, transcripts, or agent output — only lease IDs and provider/pool names are logged
- The `env` blocklist (Section 14, `env` field) rejects keys matching sensitive patterns (e.g., `ANTHROPIC_API_KEY`, `AWS_SECRET_ACCESS_KEY`, `*_SECRET_*`) regardless of whether credential leasing is configured. When leasing is enabled, credentials flow through the leasing system rather than environment variables; when leasing is not configured, the blocklist still prevents accidental credential exposure via env vars

#### Credential Lifecycle and Secret Management

Credential pools span two planes: the **pool definition** (admin-API-managed, stored in Postgres) and the **credential material** (Kubernetes Secrets, referenced by `secretRef`). This is by design — secret material belongs in Kubernetes Secrets (where it benefits from encryption-at-rest, RBAC, and external-secrets-operator integration), while pool configuration (assignment strategy, concurrency limits, policies) belongs in the admin API. Operators must understand the lifecycle for both halves.

> **Production requirement — etcd encryption at rest for Secrets.** Kubernetes does **not** encrypt Secret data in etcd by default. Anyone with etcd access (backup tapes, compromised etcd node, etcd snapshot) sees all Secret values — including credential pool API keys — in plaintext. Production deployments **must** enable the Kubernetes `EncryptionConfiguration` resource with an `aescbc`, `aesgcm`, or `kms` provider for the `secrets` resource type. Cloud-managed Kubernetes services (EKS, GKE, AKS) offer envelope encryption via their respective KMS integrations (AWS KMS, GCP Cloud KMS, Azure Key Vault) and should be configured to use them. Self-managed clusters must configure `EncryptionConfiguration` in the API server manifest and verify encryption by running `etcdctl get /registry/secrets/lenny-system/<secret-name> | hexdump` — the output should be ciphertext, not readable JSON. The `lenny-preflight` Job emits a non-blocking warning for this check (see Section 17.6), since programmatic verification requires etcd access that the preflight Job may not have.

**Initial setup workflow:**

1. **Create Kubernetes Secrets** containing credential material (API keys, service account keys, etc.) in the `lenny-system` namespace.
2. **Register the credential pool** via the admin API (`POST /v1/admin/credential-pools`) or via the bootstrap seed (`bootstrap.credentialPools` in Helm values). The pool definition references the Secrets by `secretRef`.
3. The `lenny-bootstrap` Job (Section 17.6) seeds pool definitions but does **not** create Kubernetes Secrets — Secrets must exist before the bootstrap Job runs, or the Token Service will log errors for unresolvable `secretRef` entries and mark those credentials as `unavailable` in the pool.

**Token Service secret resolution.** The Token Service uses a Kubernetes **informer** (watch) on Secrets in the `lenny-system` namespace. When a Secret referenced by a credential pool is created, updated, or deleted, the Token Service detects the change within its informer resync interval (default: 30 seconds, configurable via `tokenService.secretResyncInterval` Helm value). No explicit reload or admin API call is required after updating a Secret — the Token Service picks up the new value automatically. The metric `lenny_token_service_secret_reloads_total` (counter, labeled by secret name and outcome: `success`, `not_found`, `parse_error`) tracks secret resolution events.

**Credential rotation workflow:**

1. **Update the Kubernetes Secret** with the new credential material (e.g., `kubectl create secret generic anthropic-key-1 --from-literal=api-key=sk-ant-new... --dry-run=client -o yaml | kubectl apply -f -`).
2. The Token Service detects the Secret change via its informer within 30 seconds.
3. **No pool definition update is required** — the `secretRef` has not changed, only the Secret's content.
4. New credential leases issued after the Secret update use the new material. Existing active leases continue using the previously materialized credentials until they expire naturally (governed by lease TTL). If immediate rotation of active sessions is required, use the emergency revocation endpoint (`POST /v1/admin/credential-pools/{poolId}/credentials/{credId}/revoke`) to force all active leases to re-acquire credentials with the updated material.

**GitOps pattern.** For teams using GitOps (ArgoCD/Flux) with external secret management:

- **Kubernetes Secrets** are managed by `external-secrets-operator` (ESO), `sealed-secrets`, or a similar controller that syncs secret material from an external vault (AWS Secrets Manager, HashiCorp Vault, GCP Secret Manager, etc.). These are standard Kubernetes resources managed through the GitOps pipeline.
- **Credential pool definitions** are managed via the `bootstrap.credentialPools` section in Helm values, committed to Git alongside other Helm values. The `lenny-bootstrap` Job applies them idempotently on every `helm upgrade` / ArgoCD sync.
- **Ordering:** ESO / sealed-secrets controllers must populate the Secrets **before** the Token Service starts resolving them. In ArgoCD, use sync-wave ordering (e.g., `ExternalSecret` at sync-wave `-3`, Lenny chart at sync-wave `0`). The Token Service tolerates temporarily missing Secrets (credentials are marked `unavailable` and re-resolved on the next informer event), so brief ordering races during initial deployment are self-healing.

---

## 5. Runtime Registry and Pool Model

### 5.1 Runtime

`Runtime` and `SessionTemplate` are unified into a single **`Runtime`** concept. A Runtime is either **standalone** (has an `image`) or **derived** (has a `baseRuntime` reference). All runtimes are registered via the admin API as static configuration.

Every Runtime has a **`type`** field and an optional **`capabilities`** field:

```yaml
type: agent # agent (default) | mcp
capabilities:
  interaction: one_shot # one_shot | multi_turn
  injection:
    supported: true # default: false
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
  interaction: one_shot # one_shot | multi_turn
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
  onTimeout: fail # fail | warn
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
    visibility: public # internal | tenant | public
    value: "..."
taskPolicy:
  acknowledgeBestEffortScrub: true
  allowCrossTenantReuse: false # only valid when isolationProfile: microvm
  cleanupCommands:
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn
  maxScrubFailures: 3
  maxTasksPerPod: 50 # required — pod retired after this many tasks
  maxPodUptimeSeconds: 86400 # optional — pod retired after this duration
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
  timeoutSeconds: 300 # optional — waits indefinitely if absent
  onTimeout: fail # fail | warn
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

| MCP annotation                                | Inferred capabilities                                  |
| --------------------------------------------- | ------------------------------------------------------ |
| `readOnlyHint: true`                          | `read`                                                 |
| `readOnlyHint: false, destructiveHint: false` | `write`                                                |
| `destructiveHint: true`                       | `write, delete`                                        |
| `openWorldHint: true`                         | `network`                                              |
| No annotations                                | `admin` (conservative default)                         |
| _(no MCP equivalent)_                         | `execute`, `admin` — set via `toolCapabilityOverrides` |

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

**Tenant pinning:** Task-mode pods are pinned to a single tenant for their entire lifetime. The gateway MUST NOT assign a task-mode pod to a different tenant than its first assignment. This is enforced in the gateway's task assignment logic: each task-mode pod records its `tenantId` on first use, and subsequent assignment requests verify `tenantId` match before routing. Cross-tenant pod reuse is only permitted with `microvm` isolation and an explicit `allowCrossTenantReuse: true` field in `taskPolicy`, where the VM boundary provides a VM-level isolation boundary that is significantly stronger than runc or gVisor but shares host virtio devices. Kata provides isolation appropriate for cross-tenant task reuse where tenants have been independently vetted, but it is not equivalent to dedicated hardware isolation. The pool controller rejects `allowCrossTenantReuse: true` on any pool whose `isolationProfile` is not `microvm` at validation time with a descriptive error.

```yaml
taskPolicy:
  acknowledgeBestEffortScrub: true # required — see below
  allowCrossTenantReuse: false # only valid when isolationProfile: microvm — see tenant pinning above
  cleanupCommands:
    - pkill -f jupyter_kernel
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn # warn | fail
  maxScrubFailures: 3 # pod retired after this many cumulative scrub failures
  maxTasksPerPod: 50 # required — pod retired after this many completed tasks
  maxPodUptimeSeconds: 86400 # optional — pod retired after this uptime (seconds)
```

Lifecycle: task completes → adapter sends `terminate(task_complete)` on lifecycle channel → runtime acknowledges → deployer-defined `cleanupCommands` execute (have access to task state) → Lenny scrub runs → pod available. `setupCommands` run once per pod at start, not per task. Per-task setup belongs in the runtime's initialization.

**Lenny scrub procedure.** After deployer-defined `cleanupCommands` finish, the gateway agent executes a deterministic, non-configurable scrub sequence on the pod:

1. Kill all remaining user processes (`kill -9 -1` as the sandbox user).
2. Remove the workspace directory (`rm -rf $WORKSPACE_DIR`).
3. Purge environment variables injected for the previous task (tracked by the adapter; restored to the pod baseline set recorded at first boot).
4. Clear `/tmp`, `/dev/shm`, and any adapter-managed scratch directories.
5. Truncate adapter-local log buffers.
6. Verify scrub by stat-checking the workspace path, `/tmp`, and `/dev/shm` — if any path is non-empty after scrub, the scrub is marked failed.

The scrub is **best-effort, not a security boundary** — it reduces cross-task data leakage within a single tenant but does not replace isolation. This is why task-mode pods are tenant-pinned (see above). Specifically, the scrub **cannot** address the following residual state vectors: POSIX shared memory segments not under managed paths (`/dev/shm` is cleared but `shmget`-allocated segments keyed by IPC ID may persist if the creating process is not killed before step 1 races), kernel TCP socket `TIME_WAIT` state and connection tracking entries, DNS resolver cache in long-lived `nscd` or `systemd-resolved` processes (killed in step 1 but cache may be observable via timing during the kill window), kernel buffer/page cache priming (files read by a previous task remain in page cache, observable via timing), `inotify` and `fanotify` watch registrations (cleared only when the owning process is killed), and named pipes or UNIX domain sockets outside managed paths. Deployers should evaluate whether these residual vectors are acceptable for their workload's sensitivity level.

**Kata/microvm scrub variant.** When `isolationProfile: microvm` and `allowCrossTenantReuse: true`, the standard Lenny scrub (steps 1-6 above) is insufficient because the guest VM itself persists across tasks — the guest kernel's DNS resolver cache, TCP connection tracking state, kernel buffer/page cache, and in-memory filesystem metadata survive the scrub. For cross-tenant task reuse on microvm pods, the scrub procedure includes an additional step after step 6:

7. **Guest VM restart:** The adapter requests a full guest VM restart via the Kata runtime's VM lifecycle API. The guest kernel is shut down and a fresh guest boots from the original VM image. This eliminates all guest-kernel-level residual state (DNS cache, TCP `TIME_WAIT`, page cache, inotify registrations, kernel module state). The restart adds latency (typically 3-8 seconds, consistent with Kata cold-start times documented in Section 6.2). The scrub verification (step 6) is re-executed after the guest restarts to confirm a clean state.

If a deployer requires cross-tenant reuse without the guest restart latency cost, they may set `taskPolicy.microvmScrubMode: in-place` (default: `restart`). In `in-place` mode, only the standard steps 1-6 execute inside the continuing VM guest, and the following residual state vectors are documented as persisting across tenant boundaries: guest kernel DNS resolver cache, guest kernel TCP `TIME_WAIT` state, guest kernel buffer/page cache priming, and guest kernel inotify/fanotify registrations. The `in-place` mode requires an additional acknowledgment: `acknowledgeMicrovmResidualState: true` in `taskPolicy`. The pool controller rejects `microvmScrubMode: in-place` without this acknowledgment.

**`onCleanupFailure` behaviors:**

- **`warn`** (default) — the pod is returned to the available pool with a `scrub_warning` annotation. The gateway logs the failure, increments `lenny_task_scrub_failure_total` (aggregate counter) and `lenny_task_pod_scrub_failure_count` (per-pod gauge, labeled by `pod_name`, `pool`, `runtime_class`), and accepts the next task. The deployer accepts residual state risk. When the pod's cumulative scrub failure count reaches `maxScrubFailures` (default: `3`), the pod is retired and terminated regardless of the `onCleanupFailure` setting — the gateway provisions a replacement from the warm pool and logs the retirement reason as `scrub_failure_limit_reached`.
- **`fail`** — the pod is removed from the pool and terminated. The gateway provisions a replacement pod from the warm pool. The failed pod's metadata is retained in the audit log for inspection.

**Task-mode pod retirement policy.** Task-mode pods are retired (transitioned to `draining` and then terminated) when any of the following conditions is met:

- **Task count limit:** The pod's completed task count reaches `maxTasksPerPod`. After the current task completes and scrub finishes, the pod transitions to `draining` instead of returning to `idle`. A replacement pod is provisioned from the warm pool. `maxTasksPerPod` is required with no default — the deployer must make an explicit choice based on their workload's sensitivity and the residual state vectors enumerated above.
- **Uptime limit:** The pod's wall-clock uptime since first boot exceeds `maxPodUptimeSeconds`. The gateway checks uptime before assigning the next task; if the pod has exceeded the limit, it transitions to `draining` after the current task completes. `maxPodUptimeSeconds` is optional; if omitted, only `maxTasksPerPod` and `maxScrubFailures` govern retirement.
- **Scrub failure limit:** The pod's cumulative scrub failure count reaches `maxScrubFailures` (see `onCleanupFailure: warn` above).

When a pod is retired, the gateway increments `lenny_task_pod_retirement_total` (labeled by `reason`: `task_count_limit`, `uptime_limit`, `scrub_failure_limit`) and logs the retirement with the pod's lifetime task count and uptime.

**Deployer acknowledgment.** Because workspace scrub is best-effort, deployers must set an explicit acknowledgment flag to enable task mode:

```yaml
taskPolicy:
  acknowledgeBestEffortScrub: true # required — task mode rejected without this
  allowCrossTenantReuse: false # only valid when isolationProfile: microvm
  cleanupCommands: [...]
  cleanupTimeoutSeconds: 30
  onCleanupFailure: warn # warn | fail
  maxScrubFailures: 3 # pod retired after this many cumulative failures (default: 3)
  maxTasksPerPod: 50 # required — no default, forces deployer choice
  maxPodUptimeSeconds: 86400 # optional — retirement after uptime threshold
```

If `acknowledgeBestEffortScrub` is absent or `false`, the pool controller rejects the pool definition at validation time with a descriptive error referencing this section. Similarly, `maxTasksPerPod` is required with no default — the pool controller rejects task-mode pool definitions that omit it, forcing the deployer to make an explicit reuse-limit choice appropriate to their workload.

**`concurrent`** — multiple tasks on a single pod simultaneously. Two sub-variants via `concurrencyStyle`:

```yaml
executionMode: concurrent
concurrencyStyle: stateless # stateless | workspace
maxConcurrent: 8
```

**`concurrencyStyle: workspace`** — each slot gets its own workspace under `/workspace/slots/{slotId}/` (see Section 6.4 for full per-slot filesystem layout). Gateway tracks per-slot lifecycle. Task delivery via `slotId` multiplexing over stdin — the adapter assigns a `slotId` per slot, creates the per-slot directory tree, and sets the slot's `cwd` to `/workspace/slots/{slotId}/current/`; the runtime implements a dispatch loop keyed on `slotId`; all binary protocol messages (inbound and outbound) carry `slotId` in this mode. Cross-slot isolation is process-level and filesystem-level — explicitly weaker than session mode.

**Deployer acknowledgment.** Because concurrent-workspace mode shares a single pod's process namespace, `/tmp`, cgroup memory, and network stack across all simultaneous slots, deployers must set an explicit acknowledgment flag to enable this mode:

```yaml
concurrentWorkspacePolicy:
  acknowledgeProcessLevelIsolation: true # required — concurrent-workspace mode rejected without this
  maxConcurrent: 8
```

If `acknowledgeProcessLevelIsolation` is absent or `false`, the pool controller rejects the pool definition at validation time with a descriptive error referencing this section and listing the specific isolation properties the deployer is accepting: shared process namespace, shared `/tmp`, shared cgroup memory, and shared network stack between concurrent slots.

**`concurrencyStyle: stateless`** — no workspace materialization. Gateway routes through Kubernetes Service. Pod readiness probe reflects slot availability. PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

**Concurrent-workspace slot failure and cleanup.** Slots fail independently — a single slot failure does not terminate the pod or affect other active slots. Per-slot behavior:

- **Failure isolation:** When a slot's task fails (runtime error, OOM within the slot's cgroup, or unhandled exception), the adapter marks that `slotId` as `failed` and emits `lenny_slot_failure_total{reason}`. Other slots continue unaffected. The gateway is notified via the lifecycle channel and applies the slot retry policy below.
- **Slot cleanup:** On slot completion or failure, the adapter removes the slot's workspace directory, kills any processes owned by the slot's process group, and releases the `slotId`. Cleanup timeout is `cleanupTimeoutSeconds / maxConcurrent` (minimum 5s). If cleanup fails, the slot is leaked — the pod continues but the slot is not reclaimed until pod termination.
- **Checkpoint granularity:** Checkpoints are per-slot. Each slot's checkpoint includes only that slot's workspace state and conversation history. Whole-pod checkpoints are not supported in concurrent-workspace mode because slot lifecycles are independent.
- **Resource contention:** CPU and memory are shared across slots (no per-slot cgroup subdivision in v1). If a single slot monopolizes resources, the adapter's health probe degrades and the PoolScalingController reduces `mode_factor` for the pool. Deployers should set `maxConcurrent` conservatively relative to the resource class. Future versions may introduce per-slot resource quotas via cgroup nesting.

**Concurrent-workspace slot retry policy.** When a slot fails, the gateway applies the following retry policy (analogous to the pre-attached failure retry policy in Section 6.2):

- **Max retries:** 1 (2 total attempts including the original). The retry is always assigned to a **new slot** on the same pod (if a slot is available) or on a different pod from the pool (if the original pod is fully saturated or unhealthy).
- **Fresh workspace guarantee:** A retried slot always receives a fresh workspace — workspace staging is materialized from scratch. The retried slot never inherits any state from the failed slot's workspace, even if the failed slot's cleanup has not yet completed.
- **Non-retryable failure categories:** The following failure reasons are returned to the client immediately without retry:
  - **OOM** (`reason: oom`) — the same input is likely to OOM again on an identically-sized slot.
  - **Workspace validation error** (`reason: workspace_validation`) — the workspace plan is structurally invalid and will fail on any slot.
  - **Policy rejection** (`reason: policy_rejection`) — the task was rejected by admission policy.
- **Whole-pod replacement trigger:** When `ceil(maxConcurrent / 2)` or more slots on the same pod fail within a rolling 5-minute window, the gateway marks the pod as unhealthy, drains remaining slots gracefully, and requests a replacement pod from the warm pool. The `lenny_slot_pod_replacement_total` counter is incremented. This prevents a degraded pod from consuming retries across many independent slot failures.
- **Client error on exhaustion:** When a slot fails and either no retry is attempted (non-retryable category) or the single retry is also exhausted, the gateway returns a structured error to the client with `error.category` set to the failure reason, `error.retryable: false` from the platform's perspective (the client may choose to resubmit as a new request), and `error.slotId` identifying the failed slot.

**Truly stateless runtimes** with no workspace and no expensive shared state should be registered as external connectors, not Lenny-managed pods.

`executionMode` is declared on the `Runtime` from v1 (and on the corresponding `SandboxTemplate`).

#### Execution Mode Scaling Implications

The default PoolScalingController formula (Section 4.6.2) assumes session mode — one session per pod, no reuse. Task and concurrent modes change the relationship between pod count and effective capacity, so the formula must include a per-mode adjustment factor.

**Mode adjustment factor (`mode_factor`):**

- **`session`**: `mode_factor = 1.0` — each pod serves exactly one session. No adjustment.
- **`task`**: `mode_factor = avg_tasks_per_pod_lifetime` — a task-mode pod serves multiple sequential tasks before replacement. If a pod typically handles 10 tasks before being recycled, the pool needs ~1/10th the pods to serve the same request volume. Measured via `lenny_task_reuse_count` histogram (p50). **Formula assumption:** the `mode_factor` estimate converges toward the configured `maxTasksPerPod` for predictable workloads where pods are not retired early by `maxPodUptimeSeconds` or `maxScrubFailures`. For variable workloads where early retirement is common, use observed `lenny_task_reuse_count` p50 rather than `maxTasksPerPod` as the estimate.
- **`concurrent`**: `mode_factor = maxConcurrent` — each pod serves `maxConcurrent` simultaneous tasks. A pod with `maxConcurrent: 8` provides 8x the effective capacity of a session-mode pod.

**Adjusted formula:**

```
target_minWarm = ceil(base_demand_p95 × variant_weight × safety_factor / mode_factor
                      + burst_p99_claims × pod_warmup_seconds / mode_factor)
```

Both steady-state and burst terms are divided by `mode_factor` because each pod absorbs more demand in task and concurrent modes.

**Caveats:**

- For task mode, `mode_factor` is derived from observed reuse metrics and converges toward `maxTasksPerPod` over time. During cold start (no historical data), the controller falls back to `mode_factor = 1.0` (session-mode sizing) until sufficient samples are collected (default: 100 completed tasks). Once converged, `mode_factor` is bounded above by `maxTasksPerPod` (pods cannot serve more tasks than the configured limit).
- For concurrent mode with `concurrencyStyle: workspace`, the effective `mode_factor` may be lower than `maxConcurrent` if workspace materialization per slot is a bottleneck. The PoolScalingController uses `active_slots / (pod_count × maxConcurrent)` saturation to detect this and adjusts `mode_factor` downward when slot saturation consistently exceeds 0.85.
- For concurrent mode with `concurrencyStyle: stateless`, routing goes through a Kubernetes Service and pod readiness reflects slot availability, so the scaling controller monitors slot saturation directly rather than using the warm pool claim model.

#### Pool Taxonomy

**Example pools:**

- `claude-worker-sandboxed-small`
- `claude-orchestrator-microvm-medium`

**Pool taxonomy strategy:** Not every runtime × isolation × resource combination needs a warm pool. Use a tiered approach:

- **Hot pools** (minWarm > 0): High-traffic combinations that need instant availability
- **Cold pools** (minWarm = 0, maxWarm > 0): Valid combinations that create pods on demand with documented cold-start latency
- **Disallowed combinations**: Invalid or insecure combinations rejected at pool definition time

This prevents the combinatorial explosion of 3 runtimes × 3 isolation × 3 resource = 27 pools each holding idle pods. In practice, Tier 1 deployments typically need 1-2 hot pools; Tier 2/3 deployments need 3-10 depending on runtime variety and isolation requirements (Section 17.8).

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
2. **Helm pre-install hook.** The Helm chart includes a `lenny-preflight` validation Job (see Section 17.6) that checks for required RuntimeClasses and all other infrastructure dependencies before installation proceeds.
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

**Optional: SDK-warm mode.** Runtimes that declare `capabilities.preConnect: true` can pre-connect their agent process during the warm phase (before workspace finalization) without sending a prompt. The warm pool controller starts the SDK process after the pod reaches `idle` state, leaving it waiting for its first prompt. This eliminates SDK cold-start latency from the hot path.

**All pods are SDK-warm when the runtime supports it.** Pools referencing a `preConnect`-capable runtime warm **all** pods to SDK-warm state. There is no pod-warm/SDK-warm split or ratio to configure — simplicity over micro-optimization.

**Demotion on demand.** SDK-warm mode is only safe when the request does not inject project config files that must be present at session start. Each `Runtime` declares a `sdkWarmBlockingPaths` list (default: `["CLAUDE.md", ".claude/*"]`) — if the workspace plan includes files matching any of these glob patterns, the gateway sets `requiresDemotion: true` on the `ClaimOpts` and the adapter calls the `DemoteSDK` RPC (Section 4.7) to tear down the pre-connected SDK process, transitions the pod back to `idle` (pod-warm), and the normal pod-warm setup path proceeds. This incurs an SDK teardown penalty (typically 1–3s depending on runtime) but avoids the complexity of maintaining a dual-pool inventory. The metric `lenny_warmpool_sdk_demotions_total` (counter, labeled by pool) tracks demotion frequency for observability.

**Demotion support is mandatory for `preConnect` runtimes.** Declaring `capabilities.preConnect: true` implies that the runtime's adapter supports the `DemoteSDK` RPC — the ability to cleanly tear down and restart the agent process without restarting the pod. This is not optional: since all pods in a `preConnect` pool are SDK-warm, any request that includes `sdkWarmBlockingPaths` files requires demotion. A runtime that cannot safely tear down its SDK process must not declare `preConnect: true`. The gateway validates this at runtime registration: if `preConnect: true` is set and `sdkWarmBlockingPaths` is non-empty (which it is by default), the registration response includes a warning reminding the runtime author that their adapter must implement `DemoteSDK`. If the adapter does not implement `DemoteSDK`, the RPC returns `UNIMPLEMENTED` and the gateway fails the session with a clear error (`SDK_DEMOTION_NOT_SUPPORTED`) rather than silently proceeding with stale SDK state.

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

Pre-attached failure transitions:
  warming ──→ failed
  sdk_connecting ──→ failed
  receiving_uploads ──→ failed
  running_setup ──→ failed
  finalizing_workspace ──→ failed
  starting_session ──→ failed

Session state transitions (from attached):
                            attached
                            │
                    ┌───────┼───────────────┬────────────────┬──────────┐
                    ▼       ▼               ▼                ▼          ▼
               completed   failed    resume_pending     suspended   cancelled
                                         │                   │
                                    ┌────┤              ┌────┼────────┐
                                    ▼    ▼              ▼    ▼        ▼
                               resuming  awaiting    running completed resume_pending
                                  │       _client     (resume)        (pod failure
                                  ▼       _action                      while suspended)
                               attached     │
                                            ▼
                                          expired

Resuming failure transitions:
  resuming ──→ resume_pending        (pod crash / gRPC error during resume, retryCount < maxRetries)
  resuming ──→ awaiting_client_action (pod crash / gRPC error during resume, retries exhausted)
  resuming ──→ awaiting_client_action (resuming timeout: 300s, retries exhausted)
  resuming ──→ resume_pending        (resuming timeout: 300s, retryCount < maxRetries)

Task-mode state transitions (from attached):
  attached ──→ task_cleanup          (task completes — adapter sends terminate(task_complete))
  task_cleanup ──→ idle              (scrub succeeds, maxTasksPerPod not reached, uptime limit not reached)
  task_cleanup ──→ idle [scrub_warning] (scrub fails, onCleanupFailure: warn, maxScrubFailures not reached)
  task_cleanup ──→ draining          (scrub fails, maxScrubFailures reached)
  task_cleanup ──→ draining          (scrub succeeds, maxTasksPerPod reached)
  task_cleanup ──→ draining          (scrub succeeds, maxPodUptimeSeconds exceeded)
  task_cleanup ──→ failed            (onCleanupFailure: fail — pod terminated)
  idle ──→ draining                  (maxPodUptimeSeconds exceeded while idle, checked before next assignment)
  draining ──→ terminated            (pod replacement provisioned from warm pool)
```

**`suspended` state:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (interrupt_request + interrupt_acknowledged)
suspended → running   (resume_session — no new content)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed (terminate)
suspended → resume_pending (pod failure/eviction while suspended)
```

Pod held, workspace preserved, `maxSessionAge` timer paused while suspended. `interrupt_request` is a standalone lifecycle signal — pause-and-decide with decoupled timing. Distinct from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**`interrupt_request` does NOT cascade** to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children.

**Pod failure while `suspended`:** A pod eviction, node drain, or runtime crash can occur while a session is in the `suspended` state (the pod is held but the agent is paused). When the gateway detects pod failure for a `suspended` session, the session transitions to `resume_pending` and follows the standard retry-and-resume path (Section 7.3). On successful recovery (`resuming → attached`), the session transitions to `running` — not back to `suspended` — because the interrupt context that caused the original suspension cannot be recovered from a checkpoint. The loss of interrupt context is an expected limitation: the session and workspace are recoverable, but the suspended-state semantics (paused agent, pending client decision) are not preserved across pod failure. The `maxSessionAge` timer, which was paused during `suspended`, resumes when the recovered session enters `running`.

**`resuming` failure transitions:** A pod crash, gRPC error, or workspace-restoration hang while the gateway is restoring a session onto a new pod must not leave the session permanently stuck in `resuming`. The gateway applies the following transitions:

- **Pod crash or unrecoverable gRPC error during `resuming`:** If `retryCount < maxRetries`, transition to `resume_pending` and begin another retry attempt. If retries are exhausted, transition directly to `awaiting_client_action`.
- **`resuming` timeout (300s):** The gateway sets a 300-second watchdog when entering `resuming` (matching the setup-command total timeout). If the watchdog fires, the same retry-count branching applies: `resume_pending` if retries remain, `awaiting_client_action` if exhausted.
- **Non-retryable errors during `resuming`** (e.g., workspace checkpoint corrupt, policy rejection): transition directly to `awaiting_client_action` regardless of retry count.

This ensures `resuming` has the same failure coverage as every pre-attached pod state and cannot become a deadlock sink.

**Pre-attached failure retry policy:** Failures in any state before `attached` trigger automatic retry by the gateway. The pod is marked `failed` and released back to the pool (or terminated if unhealthy). The gateway re-claims a new pod and replays the setup sequence from the beginning. Policy:

- **Max retries:** 2 (3 total attempts including the original)
- **Backoff:** Exponential — 500ms, 1s between retries
- **Scope:** Retries apply per client request, not per pod. Each retry claims a fresh pod.
- **Non-retryable failures:** Upload validation errors and policy rejections (e.g., disallowed setup commands) are returned to the client immediately without retry.
- **Exhaustion:** After retry exhaustion, the gateway returns an error to the client with a correlation ID for debugging.

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

**Estimated latency savings (targets, not benchmarks — to be validated by startup benchmark harness, see Phase 2):**

- runc with cached image: ~1–3s
- gVisor: ~2–5s
- Kata: ~3–8s
- Cold image pulls: +5–30s avoided

**Startup latency SLO target:** P95 pod-warm session start (pod claim through agent session ready) < 2s for runc, < 5s for gVisor, excluding file upload time. See Section 16.5.

**Per-phase latency budget (indicative targets, to be validated by Phase 2 benchmark harness):**

The TTFT SLO is P95 < 10s (from session start request to first streaming event, Section 16.5). The following table allocates that budget across hot-path phases. Setup commands are deployer-controlled and are excluded from the platform-managed budget; deployers should target ≤ 3s to preserve the overall TTFT SLO.

| Phase                                          | Who controls     | Indicative P95 target                               | Notes                                                                                                                                                                                                                                                  |
| ---------------------------------------------- | ---------------- | --------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Pod claim and routing                          | Platform         | ≤ 100ms                                             | Pool lookup and pod assignment                                                                                                                                                                                                                         |
| Credential assignment                          | Platform         | ≤ 100ms                                             | Secret injection via gateway-mediated delivery                                                                                                                                                                                                         |
| Workspace materialization                      | Platform         | ≤ 1s (small, ≤ 1MB) / ≤ 3s (large, ≤ 50MB)          | File delivery over internal network; excluded from pod-warm SLO                                                                                                                                                                                        |
| Setup commands                                 | Deployer         | ≤ 3s (runc recommended) / ≤ 1s (gVisor recommended) | Excluded from platform budget; deployer must size accordingly                                                                                                                                                                                          |
| Agent session start                            | Platform         | ≤ 1.5s (runc) / ≤ 4.5s (gVisor)                     | Covered by pod-warm SLO (2s / 5s); includes any remaining setup in adapter                                                                                                                                                                             |
| First prompt dispatch + first token            | Platform + model | ≤ 1s platform overhead                              | Model TTFP is runtime-dependent and excluded from platform budget                                                                                                                                                                                      |
| **Total (platform-controlled, no setup cmds)** |                  | **≤ 6s (runc) / ≤ 9s (gVisor)**                     | Leaves ≤ 4s (runc) / ≤ 1s (gVisor) for deployer setup commands within the 10s TTFT SLO. gVisor deployments with >1s setup commands will exceed the 10s TTFT SLO; deployers should either minimize setup time or accept a relaxed SLO for gVisor pools. |

> These are indicative planning targets, not hard requirements. Actual values depend on cluster conditions, file payload size, and runtime behaviour. All phases must be validated by the Phase 2 startup benchmark harness before targets are promoted to SLOs.

**Per-phase measurement requirement:** Each hot-path phase (pod claim, credential assignment, workspace materialization, setup commands, agent session start, first prompt dispatch) must be independently instrumented with histogram metrics (`lenny_session_startup_phase_duration_seconds{phase, runtime_class}`). The startup benchmark harness (Phase 2) must measure pod-warm vs SDK-warm latency per runtime class to validate the complexity tradeoff of the SDK-warm model.

### 6.4 Pod Filesystem Layout

```
/workspace/
  current/      # Agent's actual cwd — populated during workspace finalization
  staging/      # Upload staging area — files land here first
/sessions/      # Session files (e.g., conversation logs, runtime state)        [tmpfs]
/artifacts/     # Logs, outputs, checkpoints
/tmp/           # tmpfs writable area                        [tmpfs]
```

**Concurrent-workspace per-slot layout.** In `concurrent` mode with `concurrencyStyle: workspace`, the single `/workspace/current` layout above does not apply. Instead, the adapter creates a per-slot directory tree under `/workspace/slots/`:

```
/workspace/
  slots/
    {slotId}/
      current/    # This slot's cwd — populated during per-slot workspace finalization
      staging/    # Per-slot upload staging area
  shared/         # Optional read-only shared assets (populated once at pod start, read-only mount)
/sessions/
  {slotId}/       # Per-slot session files (conversation logs, runtime state)    [tmpfs]
/artifacts/
  {slotId}/       # Per-slot logs, outputs, checkpoints
/tmp/             # tmpfs writable area (shared across slots)                    [tmpfs]
```

**Responsibility split:**

- **Adapter** — creates and removes per-slot directory trees (`/workspace/slots/{slotId}/`, `/sessions/{slotId}/`, `/artifacts/{slotId}/`). The adapter creates the slot directory on `slotId` assignment and removes it during slot cleanup (Section 5.2). The adapter sets each slot's `cwd` to `/workspace/slots/{slotId}/current/` when dispatching a task to the runtime.
- **Runtime** — receives `cwd` per slot and operates within it. The runtime MUST NOT assume a global `/workspace/current` path in concurrent-workspace mode. All file operations use the `cwd` provided with each `slotId`-tagged message.
- **Gateway** — addresses per-slot workspace finalization and checkpoint export using the `slotId`-qualified paths. `FinalizeWorkspace` materializes files from `/workspace/slots/{slotId}/staging/` to `/workspace/slots/{slotId}/current/`. Checkpoint export (Section 4.4) targets `/workspace/slots/{slotId}/current/` for the specific slot.

Session mode and task mode continue to use the base layout (`/workspace/current`).

**`/workspace/shared/` population and enforcement.** The `/workspace/shared/` directory in concurrent-workspace mode is populated by the gateway during pod initialization (before any slot is assigned) from the Runtime's `sharedAssets` configuration — a list of artifact references or inline file specs. Once populated, `/workspace/shared/` is mounted read-only at the container level: the pod spec uses a separate `emptyDir` volume for `/workspace/shared/` with a `readOnly: true` volumeMount on the runtime container. This enforces immutability at the kernel level — any write attempt by the runtime process returns `EROFS` (read-only filesystem). The adapter does not create or modify files under `/workspace/shared/` after initial population. If no `sharedAssets` are configured on the Runtime, the `/workspace/shared/` directory is still mounted (empty, read-only) to prevent runtimes from using it as writable scratch space.

**Data-at-rest protection:**

- `/sessions/` and `/tmp/` use `emptyDir.medium: Memory` (tmpfs) — data is guaranteed gone when the pod terminates. tmpfs usage counts against pod memory limits and must be accounted for in resource requests. Resource class definitions (Section 5.2) must account for tmpfs usage in memory requests. For example, if a pod's memory limit is 2Gi and tmpfs usage can reach 500Mi, the effective memory available to the agent process is 1.5Gi. Deployers should set `emptyDir.sizeLimit` on tmpfs volumes to cap usage and provide predictable OOM boundaries rather than silent memory pressure. Recommended size limits: `/sessions/` sizeLimit of 256Mi (session transcripts), `/tmp/` sizeLimit of 256Mi (scratch space).
- `/workspace/` and `/artifacts/` use disk-backed emptyDir. Node-level disk encryption (LUKS/dm-crypt or cloud-provider encrypted volumes) is **required** for production deployments. **T4 workspace limitation:** For tenants with `workspaceTier: T4`, the data classification table (Section 12.9) requires envelope encryption via KMS. Node-level disk encryption satisfies this for data at rest on the node's physical volume, but does not provide per-tenant key isolation for in-flight workspace files on emptyDir. Application-layer encryption of individual file writes on emptyDir is impractical without a FUSE-based encrypted filesystem, which conflicts with simplicity and performance goals. The T4 envelope encryption requirement is fully enforced for durable workspace data (MinIO snapshots use SSE-KMS with tenant-scoped keys per Section 12.5). For ephemeral in-flight workspace data, the combined controls of node-level disk encryption + pod isolation (gVisor/Kata RuntimeClass) + one-session-only pod lifecycle + emptyDir cleanup on pod termination provide defense-in-depth. Deployers handling T4 workloads should use Kata or gVisor isolation profiles, which provide per-pod encrypted scratch volumes on supported platforms. Lenny cannot programmatically verify node-level disk encryption — see Section 17.6 preflight warning.
- `/dev/shm` is limited to 64MB. `procfs` and `sysfs` are masked/read-only. `shareProcessNamespace: false` when using sidecar containers. `shareProcessNamespace: false` is not enforceable via Pod Security Standards; a Kyverno or Gatekeeper policy must be deployed to reject pods in agent namespaces that set `shareProcessNamespace: true`, as part of the RuntimeClass-aware admission policies described in Section 17.2.
- Combined with the one-session-only invariant, sensitive data never persists on disk after pod termination.

---

## 7. Session Lifecycle

### 7.1 Normal Flow

```
1. Client → Gateway:     CreateSession(runtime, pool, retryPolicy, metadata)
2. Gateway:              Authenticate, authorize, evaluate policy
3. Gateway:              Pre-claim credential availability check — compute the intersection of
                         Runtime.supportedProviders and tenant credentialPolicy.providerPools.
                         Verify at least one provider in the intersection has an assignable
                         credential. If no provider has availability, reject immediately with
                         CREDENTIAL_POOL_EXHAUSTED (POLICY) — no pod is claimed or wasted.
4. Gateway:              Select pool, claim idle warm pod
5. Gateway → Store:      Persist session metadata (session_id, pod, state)
6. Gateway:              Evaluate CredentialPolicy per provider in the intersection →
                         assign CredentialLease map (one lease per provider with available
                         credentials; providers without availability are skipped)
7. Gateway → Pod:        AssignCredentials(leases) — push map of provider → materializedConfig
                         to runtime for all successfully assigned providers
8. Gateway → Client:     Return session_id + upload token

9. Client → Gateway:     UploadWorkspaceContent(files, archives)
10. Gateway → Pod:       Stream files over mTLS into /workspace/staging

11. Client → Gateway:    FinalizeWorkspace()
12. Gateway → Pod:       Validate staging, materialize to /workspace/current
13. Pod:                 Run setup commands (bounded, logged)

14. Gateway → Pod:       StartSession(cwd=/workspace/current, options)
                         (SDK-warm pods: skip this step — session already connected,
                          send ConfigureWorkspace to point it at finalized cwd)
15. Pod:                 Start agent binary/runtime session (or resume pre-connected one)

16. Client → Gateway:    AttachSession(session_id)
17. Gateway ↔ Pod:       Bidirectional stream proxy
18. Client ↔ Gateway:    Full interactive session (prompts, responses, tool use,
                         interrupts, elicitation, credential rotation on RATE_LIMITED)

19. Session completes or client disconnects
20. Gateway → Pod:       Seal workspace — export final workspace snapshot to Artifact Store
21. Gateway → Pod:       Terminate
22. Gateway → Store:     Mark session completed, persist final state, record artifact refs
23. Gateway:             Release credential lease back to pool
24. Warm Pool:           Release pod to draining → eventual cleanup
```

**Artifact retention:** Session artifacts (workspace snapshots, logs, transcripts) are retained for a configurable TTL (default: 7 days, deployer-configurable). A background GC job deletes expired artifacts. Clients can extend retention on specific sessions via `extend_artifact_retention(session_id, ttl)`.

**Transcript as downloadable artifact:** The session transcript (conversation history) is available via `GET /v1/sessions/{id}/transcript` and is included as a downloadable session artifact. When deriving a new session (see `POST /v1/sessions/{id}/derive`), clients can optionally include the previous session's transcript as a file in the derived session's workspace, giving the new agent context from the prior conversation.

**Derive copy semantics:** `POST /v1/sessions/{id}/derive` always performs a full copy of the parent session's workspace snapshot — it reads the parent's MinIO object and writes a new independent object under the derived session's own path (`/{tenant_id}/{object_type}/{new_session_id}/...`). No MinIO object is shared between parent and derived sessions. The `parent_workspace_ref` field on the derived session record is a metadata lineage pointer only (Section 4.5); it does not create a reference dependency for GC purposes. This means the GC job can safely delete the parent's artifacts by TTL without affecting the derived session's workspace. Reference counting is not required in v1. If a future optimization introduces content-addressed deduplication (shared blobs across sessions), reference counting must be added to the GC job at that time (Section 12.8).

**Derive session semantics:** The `POST /v1/sessions/{id}/derive` endpoint creates a new independent session pre-populated with the source session's workspace snapshot. The following rules govern derive behavior:

1. **Allowed source session states.** Derive is permitted from any session state that has a resolvable workspace snapshot:
   - `completed` — uses the sealed final workspace. This is the recommended and most predictable source state.
   - `failed` — uses the last successful checkpoint snapshot. The response includes `workspaceSnapshotSource: "checkpoint"` and `workspaceSnapshotTimestamp` so the client knows the snapshot predates the failure.
   - `running`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action` — uses the most recent successful checkpoint snapshot. The response includes `workspaceSnapshotSource: "checkpoint"` and `workspaceSnapshotTimestamp`. Clients should be aware that the snapshot may lag behind the live workspace by the checkpoint interval. No on-demand checkpoint is triggered (to avoid unexpected pauses on Full-tier runtimes); clients who need a fresh snapshot should wait for session completion.
   - `cancelled`, `expired` — uses the sealed workspace if seal completed before termination, otherwise the last successful checkpoint. `workspaceSnapshotSource` indicates which was used.
   - If no workspace snapshot exists (e.g., session failed before any checkpoint or seal), derive returns `400 INVALID_STATE` with message `"source session has no resolvable workspace snapshot"`.

2. **Credential lease handling.** Derive creates a fully independent session. The derived session goes through the standard `CredentialPolicy` evaluation (Section 7.1, step 6) — the gateway evaluates the credential policy for the new session's runtime and assigns a fresh `CredentialLease` from the resolved pool or user source. Credential leases are never inherited from the source session. If no credential is available, derive fails with `503 CREDENTIAL_POOL_EXHAUSTED` — the same error as a normal `CreateSession` when the pool is exhausted. The source session's credential lease is unaffected by the derive operation.

3. **Connector state.** Connector OAuth tokens and authorization state are not inherited. The derived session starts with no active connector tokens. If the derived session's runtime requires connector access, the client must complete the connector authorization flow independently (Section 9.3) for the new session. Gateway-held connector tokens are scoped to the source session and are not copied to the derived session.

**Seal-and-export invariant:** The workspace is always exported to durable storage before the pod is released. If export fails, the pod is held in `draining` state with a retry. This ensures session output is never lost due to pod cleanup.

```

```

### 7.2 Interactive Session Model

Once a session is attached, the client interacts via an **MCP Task** with bidirectional streaming over Streamable HTTP (SSE for server→client, POST for client→server). All content delivery uses the `MessageEnvelope` format (see Section 15.4.1).

**Client → Gateway (external API):**

| Endpoint / Message                                 | Description                                                                                                                                            |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `POST /v1/sessions/{id}/messages`                  | Send a message (unified endpoint for all content delivery). Gateway rejects injection against sessions whose runtime has `injection.supported: false`. |
| `POST /v1/sessions/{id}/interrupt`                 | Interrupt current agent work (lifecycle signal, not content delivery)                                                                                  |
| `approve_tool_use(tool_call_id)`                   | Approve a pending tool call                                                                                                                            |
| `deny_tool_use(tool_call_id, reason?)`             | Deny a pending tool call                                                                                                                               |
| `respond_to_elicitation(elicitation_id, response)` | Answer an elicitation request                                                                                                                          |

**Gateway → Client (streaming events):**

| Event                                          | Description                                                                                                                 |
| ---------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `agent_output(parts: OutputPart[])`            | Streaming output from the agent (replaces `agent_text`)                                                                     |
| `tool_use_requested(tool_call_id, tool, args)` | Agent wants to call a tool (if approval required)                                                                           |
| `tool_result(tool_call_id, result)`            | Result of a tool call                                                                                                       |
| `elicitation_request(elicitation_id, schema)`  | Agent/tool needs user input                                                                                                 |
| `status_change(state)`                         | Session state transition (including `suspended` and `input_required`)                                                       |
| `session.resumed(resumeMode, workspaceLost)`   | Session resumed from checkpoint or minimal state; `resumeMode` is `full` or `conversation_only`; `workspaceLost` is boolean |
| `error(code, message, transient?)`             | Error with classification                                                                                                   |
| `session_complete(result)`                     | Session finished, result available                                                                                          |

**Session state machine:**

```
running → suspended        (interrupt_request + interrupt_acknowledged)
running → input_required   (runtime calls lenny/request_input — sub-state of running)
running → completed        (agent finishes)
running → failed           (runtime crash, unrecoverable error)
running → cancelled        (client/parent cancels)
running → expired          (lease/budget/deadline exhausted)
input_required → running   (input provided via inReplyTo or request expires/cancelled)
input_required → cancelled (parent cancels while awaiting input)
input_required → expired   (deadline reached while awaiting input)
suspended → running        (resume_session — no new content)
suspended → running        (POST /v1/sessions/{id}/messages delivery:immediate)
suspended → completed      (terminate)
suspended → cancelled      (client/parent cancels while suspended)
suspended → expired        (deadline reached while suspended)
suspended → resume_pending (pod failure/eviction while suspended)
```

`input_required` is a **sub-state of `running`**: the pod is live and the runtime process is active, but the agent is blocked inside a `lenny/request_input` tool call awaiting a response. This sub-state is significant for message routing (see delivery paths below) and for external observability (the gateway emits `status_change(state: "input_required")` to the client when the session enters this state, and `status_change(state: "running")` when it exits). The transitions mirror those in the canonical task state machine (Section 8.8).

Terminal states: `completed`, `failed`, `cancelled`, `expired`. These match the canonical task states defined in Section 8.8.

**Gateway-mediated inter-session messaging:** All inter-session communication flows through the gateway. Platform MCP tools available to runtimes:

- `lenny/send_message(to, message)` — send a message to a task by ID, subject to `messagingScope` (see below)
- `lenny/request_input(parts)` → `MessageEnvelope` — blocks until answer arrives
- `lenny/get_task_tree()` → `TaskTreeNode` — returns task hierarchy with states


**Messaging scope:** `lenny/send_message` target reachability is controlled by a `messagingScope` setting:

| Scope      | Allowed targets                                                                 |
| ---------- | ------------------------------------------------------------------------------- |
| `direct`   | Direct parent and direct children of the calling session (default)              |
| `siblings` | Direct parent, direct children, and sibling tasks (children of the same parent) |

Additional scopes (e.g. full-tree or cross-tree) may be added in future versions; the enum is intentionally extensible.

**Configuration hierarchy (most-restrictive wins, can only narrow):**

1. **Deployment level** (Helm) — sets the ceiling and the default
2. **Tenant level** (admin API) — can restrict within deployment limits
3. **Runtime level** (top-most parent runtime config applies to the tree it roots) — can restrict further within tenant limits

```yaml
# Deployment level (Helm)
messaging:
  defaultScope: direct             # default for sessions without overrides
  maxScope: siblings               # absolute ceiling — no tenant or runtime can widen beyond this

# Tenant level (admin API)
messaging:
  scope: direct                    # overrides deployment default; capped by deployment maxScope

# Runtime level (admin API, on the runtime resource)
messaging:
  scope: direct                    # overrides tenant setting; capped by tenant effective scope
```

**Effective scope** = narrowest of (deployment maxScope, tenant scope if set, top-most parent runtime scope if set). The restrictiveness order is: `direct` < `siblings`. A tenant with `scope: siblings` under a deployment with `maxScope: direct` gets `direct`.

**Rate limiting:** `lenny/send_message` is subject to per-session outbound rate limits (`maxPerMinute`, `maxPerSession`) and per-session inbound aggregate rate limits (`maxInboundPerMinute`) defined in the delegation lease (see `messagingRateLimit` in Section 8.3).

**Content policy enforcement:** Inter-session messages are subject to the target session's `contentPolicy.maxInputSize` and `contentPolicy.interceptorRef` via the `PreMessageDelivery` interceptor phase (Section 4.8). This ensures that agent-to-agent messages receive the same content scanning as delegation inputs.

**Session inbox definition:** Each active session on its coordinating gateway replica has a **session inbox** — an in-memory FIFO queue of undelivered messages. The inbox has the following contract:

| Property          | Value                                                                                                                                                                                                                                                                                                          |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Backing store     | In-memory on the coordinating gateway replica (not persisted to Redis or Postgres)                                                                                                                                                                                                                             |
| Size bound        | `maxInboxSize` messages per session (default: 500; configurable per deployment). This cap prevents unbounded memory growth during long `await_children` blocks.                                                                                                                                                |
| Overflow behavior | When the inbox reaches `maxInboxSize`, the **oldest** buffered message is dropped and the sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`.                                                                                                                                |
| Durability        | **No durability guarantee.** If the coordinating gateway replica crashes, the inbox contents are lost. This is a documented loss window.                                                                                                                                                                       |
| Crash recovery    | When a new gateway replica takes over coordination (via lease reacquisition), the session's inbox is empty. Senders that received a `queued` receipt and require reliable delivery must detect the gap via the `delivery_receipt` mechanism and re-send, or use the DLQ path (path 6 for recovering sessions). |

The inbox guarantee is therefore: **the gateway does not drop undelivered messages while the coordinating replica remains alive**. This is not an end-to-end durability guarantee. Deployers requiring stronger guarantees should use the DLQ path (path 6) by treating the target session as temporarily unavailable, or route through a durable queue outside the platform.

**Message delivery routing — six paths:**

1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves blocked tool call directly. No stdin delivery, no interrupt. Delivery receipt status: `delivered`.
2. **No matching pending request, runtime available** → `{type: "message"}` written to the runtime's stdin pipe. A runtime is considered _available_ when it is actively reading from stdin — that is, its adapter reports `ready_for_input` (between tool calls, after emitting output, or during any explicit input-wait). If the runtime does not consume the message within a configurable delivery timeout (default: 30 seconds), the gateway treats it as undeliverable for this path and falls through to inbox buffering (path 3 behavior). Messages buffered this way are delivered in FIFO order when the runtime next enters `ready_for_input`. The inbox will not drop messages while the coordinating replica is alive; see the session inbox definition above for overflow and crash semantics.
3. **No matching pending request, runtime blocked in `await_children`** → buffered in the session inbox (see inbox definition above); delivered in FIFO order before the next `await_children` event.
4. **Target session is `input_required`** (blocked in `lenny/request_input`, no matching `inReplyTo`) → message is buffered in the session inbox. The runtime is not reading from stdin while blocked in a `request_input` call, so direct delivery is not possible. Buffered messages are delivered in FIFO order once the `request_input` resolves (via a matching `inReplyTo` message, cancellation, or expiry) and the runtime returns to `ready_for_input`. Delivery receipt status: `queued`.
5. **Target session is `suspended`** → message is buffered in the session inbox. If the message carries `delivery: "immediate"`, the gateway atomically resumes the session (`suspended → running`) and delivers the message to the runtime's stdin pipe once the runtime reports `ready_for_input`. This applies uniformly to all message sources: external client (`POST /v1/sessions/{id}/messages`) and inter-session via `lenny/send_message`. The delivery receipt is `delivered` if `delivery: "immediate"` triggers a successful resume-and-deliver; otherwise `queued`. Messages without `delivery: "immediate"` remain buffered until an explicit `resume_session` or a subsequent `delivery: "immediate"` message triggers the resume, at which point all buffered inbox messages are delivered in FIFO order.
6. **Target session in terminal or recovering state** → see dead-letter handling below.

**Dead-letter handling for inter-session messages:**

The gateway checks target session state before routing. Behavior depends on the target's state:

| Target state                                             | Behavior                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Terminal (`completed`, `failed`, `cancelled`, `expired`) | Gateway returns an error to the sender immediately: `{ "code": "TARGET_TERMINAL", "message": "Target task {id} is in terminal state {state}", "targetState": "{state}" }`. The message is not enqueued.                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| Recovering (`resume_pending`, `awaiting_client_action`)  | Message is enqueued in a **dead-letter queue** (DLQ) stored in Redis (sorted set keyed by `session_id:dlq`, scored by expiry timestamp) with a configurable TTL (default: `maxResumeWindowSeconds` of the target session, or 900s if unset). The DLQ has a `maxDLQSize` cap (default: 500 messages); on overflow, the oldest DLQ entry is dropped and the sender receives a `message_dropped` delivery receipt. If the target resumes before TTL expiry, queued messages are delivered in FIFO order. On TTL expiry, undelivered messages are discarded and the sender receives a `message_expired` notification via the `delivery_receipt` mechanism (see below). |

**Delivery receipts:** All `lenny/send_message` calls return a `deliveryReceipt`:

```json
{
  "messageId": "msg_abc123",
  "status": "delivered | queued | dropped | error",
  "targetState": "running",
  "queueTTL": null
}
```

Receipt `status` values:

- `delivered` — message was written to the target's stdin pipe or resolved a pending `request_input`.
- `queued` — message was buffered (inbox or DLQ). Includes `queueTTL` in seconds if the target is in a recovering state.
- `dropped` — message was dropped due to inbox or DLQ overflow. The `reason` field contains `"inbox_overflow"` or `"dlq_overflow"`.
- `error` — message was rejected. The `error` field contains the reason (e.g., `TARGET_TERMINAL`, `SCOPE_DENIED`, `RATE_LIMITED`).

For queued messages that later expire, the gateway emits a `message_expired` event to the sender's event stream: `{ "type": "message_expired", "messageId": "msg_abc123", "reason": "target_ttl_exceeded" }`.

**Reconnect semantics:** The gateway persists an event cursor per session. On reconnect, the client provides its last-seen cursor and the gateway replays missed events from the EventStore. Events older than the checkpoint window may not be replayable; in that case the client receives a `checkpoint_boundary` marker and the current session state.

**SSE buffer policy:** The gateway maintains a per-client event buffer (default: 1000 events or 10MB, whichever is smaller). If a slow client falls behind and the buffer fills, the gateway drops the connection and the client must reconnect with its last-seen cursor. Events beyond the buffer are replayed from the EventStore on reconnect (if within the checkpoint window). This prevents a single slow client from causing unbounded memory growth in the gateway. At Tier 3, aggregate buffer memory across all sessions can be significant; deployers should monitor total gateway memory.

**Sibling coordination patterns:** When `messagingScope` is set to `siblings`, sessions that share the same parent can discover each other via `lenny/get_task_tree` and exchange messages via `lenny/send_message`. The following constraints and design decisions apply:

1. **Message ordering.** When multiple siblings send messages concurrently to the same target, delivery order is determined by the timestamp assigned when the coordinating gateway replica receives the `lenny/send_message` call. Under multi-replica gateway operation, two messages arriving at different replicas for the same target session are serialised by the target's coordinator (only one replica coordinates a given session at any time via the coordination lease). The coordinator appends messages to the inbox in the order it processes them. This provides **coordinator-local FIFO** ordering, not global wall-clock ordering. Siblings must not rely on cross-sender ordering guarantees; if causal ordering matters, the messages themselves should carry application-level sequence numbers or vector clocks.

2. **No broadcast primitive.** The platform provides only point-to-point messaging (`lenny/send_message`). A sibling that needs to notify all peers must enumerate them from `lenny/get_task_tree` and send individual messages. This is a deliberate design choice — broadcast introduces delivery atomicity questions (all-or-nothing vs. best-effort) that are better handled at the application layer. Note that the task tree is a snapshot: siblings spawned between the `get_task_tree` call and the final `send_message` call will be missed. Agents that require reliable broadcast should adopt a coordinator pattern (one designated sibling collects and redistributes) rather than all-to-all messaging.

3. **O(N²) messaging storm risk.** In a wide fan-out tree (`maxParallelChildren: 10`, `siblings` scope), unrestricted sibling-to-sibling coordination where every sibling messages every other generates O(N²) messages. Each sender is independently subject to `messagingRateLimit.maxPerMinute` (default: 30). To prevent N compromised siblings from flooding a single target at N × `maxPerMinute`, the gateway enforces `messagingRateLimit.maxInboundPerMinute` (default: 60) on the **receiving** session — this is a tree-wide aggregate cap on inbound messages regardless of how many senders contribute. Messages exceeding the inbound limit are rejected with a `RATE_LIMITED` delivery receipt. Deployers enabling `siblings` scope on high-fan-out trees should additionally: (a) reduce `maxPerMinute` proportionally to the expected fan-out, (b) use a coordinator or hub-and-spoke pattern instead of all-to-all, and (c) monitor per-session inbox utilisation to detect storms early.

4. **Parent communication asymmetry.** A parent can message any direct child via `lenny/send_message` with `direct` scope (the parent created the child and holds the lease). There is no dedicated `lenny/send_to_parent` tool. A child that needs to communicate with its parent has two options: (a) use `lenny/request_input` to send a structured request and block for a response (appropriate for synchronous hand-offs), or (b) use `lenny/send_message` targeting the parent's `taskId` (discovered via `lenny/get_task_tree`), which requires `messagingScope: direct` or wider. The absence of a dedicated `send_to_parent` tool is intentional: child→parent communication is governed by `messagingScope` to preserve the top-down control model. A child with `messagingScope: direct` can already reach its parent via `lenny/send_message`; a dedicated tool would add no capability.

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

- **Expiry:** Sessions in `awaiting_client_action` expire after `maxResumeWindowSeconds` (default 900s). After expiry the session transitions to `expired` — a terminal state. The gateway applies the session's `cascadeOnFailure` policy to all active children (same behavior as terminal failure after retry exhaustion). Artifacts are retained per the standard retention policy.
- **Children behavior:** Active children continue running when the parent enters `awaiting_client_action`. Their results are stored in the task tree. When the parent resumes, pending child results are delivered.
- **CI / automated discovery:** Automated clients can poll `GET /v1/sessions/{id}` and check for `state: awaiting_client_action`. The webhook system (Section 14, `callbackUrl`) also fires a `session.awaiting_action` event so CI systems can react without polling.

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
  - **Zip bomb protection:** The extractor enforces a configurable decompression ratio limit (default: 100:1 compressed-to-uncompressed). During streaming extraction, cumulative compressed bytes read and cumulative uncompressed bytes written are tracked; extraction aborts immediately if the ratio exceeds the configured maximum. Additionally, all decompressor `Read()` calls are wrapped with a per-call size cap (e.g., `io.LimitedReader` with a 1 MB bound) to prevent a single read from allocating unbounded memory. Abort reasons are labeled and emitted via `lenny_upload_extraction_aborted_total{reason}` (see Section 16.1).
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

**`LeaseSlice`** defines the budget allocated from parent to child:

| Field                 | Type | Description                           |
| --------------------- | ---- | ------------------------------------- |
| `maxTokenBudget`      | int  | Token budget for child tree           |
| `maxChildrenTotal`    | int  | Max children the child may spawn      |
| `maxTreeSize`         | int  | Max pods in child's subtree           |
| `maxParallelChildren` | int  | Max concurrent children for the child |
| `perChildMaxAge`      | int  | Max wall-clock seconds for the child  |

All fields are optional. Defaults are described in Section 8.3.

**`lenny/delegate_task` rejects `type: mcp` targets** with `target_not_an_agent`.

**Delegation flow:**

1. Parent calls `lenny/delegate_task(target, task, lease_slice?)`
2. Gateway validates against parent's effective delegation policy and lease (depth, fan-out, budget)
3. Gateway asks parent runtime to export files matching the export spec (see Section 8.7)
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
- Message delivery via `lenny/send_message`

**What the parent never sees:** Pod addresses, internal endpoints, raw credentials.

**Virtual child interface lifecycle:**

- **Storage:** Virtual child interfaces live in gateway per-session memory. On parent pod failure, the gateway reconstructs them from the task tree in SessionStore (which records all child session IDs, states, and pending results).
- **Pending elicitations:** If a parent pod fails while an elicitation is pending from a child, the gateway holds that elicitation. When the parent resumes on a new pod, the gateway replays it via the re-injected virtual child interface (see the `children_reattached` event in Section 8.10).
- **Replay on resume:** The gateway re-injects all active virtual child interfaces on parent resume. Each interface carries the child's current state (running, completed, failed, `input_required`) and any pending results or elicitations. The parent agent receives a `children_reattached` event with this state.

**Delegation tree memory management:**

Each node in a delegation tree carries in-memory state on the gateway replica that owns the root session. Estimated per-node memory footprint:

| Component                  | Estimate    | Notes                               |
| -------------------------- | ----------- | ----------------------------------- |
| Virtual child interface    | ~2 KB       | MCP server shim, routing metadata   |
| Event buffer (pending)     | ~8 KB       | Capped at 64 events × ~128 B avg    |
| Elicitation state          | ~1 KB       | At most one pending per node        |
| Task metadata + result ref | ~1 KB       | IDs, status, timestamps             |
| **Total per node**         | **~12 KB**  |                                     |
| **50-node tree**           | **~600 KB** | Maximum under default `maxTreeSize` |

The delegation lease includes a `maxTreeMemoryBytes` field (default: `2097152` / 2 MB) that caps the aggregate in-memory footprint of a single delegation tree on the gateway. The gateway tracks cumulative tree memory via an atomic Redis counter alongside the existing `maxTreeSize` counter. When a new delegation would push the tree over `maxTreeMemoryBytes`, it is rejected with `BUDGET_EXHAUSTED`.

**Completed subtree offloading:** When a child session reaches a terminal state (completed, failed, cancelled, expired), the gateway offloads its virtual child interface state and buffered results to Postgres (`session_tree_archive` table, keyed by `(root_session_id, node_session_id)`). The in-memory node is replaced by a lightweight stub (~200 B) containing the child session ID, terminal status, and a `pg_archived: true` flag. If the parent later reads the child's result, the gateway fetches it from Postgres on demand (with a per-replica LRU cache, default 128 entries). This ensures that long-running trees with many completed branches do not accumulate unbounded memory. The `maxTreeMemoryBytes` counter is decremented when a node is offloaded.

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
contentPolicy:
  maxInputSize: 131072 # max bytes for TaskSpec.input per delegation (default: 128KB)
  interceptorRef: null # optional ref to a RequestInterceptor for content scanning
```

**`contentPolicy` enforcement (prompt injection mitigation):** The gateway enforces `contentPolicy` on every `delegate_task` call. `maxInputSize` is a hard byte-size limit on `TaskSpec.input` — delegations exceeding it are rejected with `INPUT_TOO_LARGE` before pod allocation. When `interceptorRef` is set, the gateway invokes the referenced `RequestInterceptor` at the `PreDelegation` phase (see Section 4.8) with the full `TaskSpec.input` as payload. The interceptor can `ALLOW`, `REJECT`, or `MODIFY` the content. This is the primary hook for deployers to integrate external content classifiers (e.g., prompt injection detectors) into delegation chains. `contentPolicy` is inherited by child leases and can only be made stricter (smaller `maxInputSize`, same or more restrictive `interceptorRef`).

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
  "allowedExternalEndpoints": [],
  "messagingRateLimit": {
    "maxPerMinute": 30,
    "maxPerSession": 200,
    "maxInboundPerMinute": 60
  },
  "maxTreeMemoryBytes": 2097152
}
```

Child leases are always **strictly narrower** than parent leases (depth decremented, budgets reduced).

**`fileExportLimits` sizing guidance:** The default `maxTotalSize: 100MB` is conservative and appropriate for most delegation workflows. For workflows that produce large build artifacts (e.g., compiled binaries, container images), deployers should increase `maxTotalSize` per delegation preset — up to the workspace size SLO ceiling (500 MB) if needed. Note that file exports transit through the gateway (parent pod → MinIO → child pod), so larger limits increase gateway I/O and MinIO bandwidth proportionally. Deployers should size MinIO I/O capacity accordingly when configuring higher limits.

**`allowedExternalEndpoints`** slot exists from v1 for future A2A support — controls which external agent endpoints can be delegated to.

**`messagingRateLimit`** — rate limits for `lenny/send_message`. `maxPerMinute` is a per-session outbound sliding-window burst limit; `maxPerSession` is a per-session lifetime cap. `maxInboundPerMinute` is a per-session **inbound** aggregate limit — the gateway enforces this on the **receiving** session, counting all messages arriving from any sender in the delegation tree. This prevents N compromised siblings from flooding a single target at N × `maxPerMinute`; regardless of the number of senders, the target accepts at most `maxInboundPerMinute` messages per sliding window. Messages exceeding the inbound limit receive a `RATE_LIMITED` delivery receipt. Exceeding any limit returns `RATE_LIMITED`. Child leases inherit the parent's limits (or stricter). Defaults are deployment-configurable via Helm.

**Isolation monotonicity:** Children must use an isolation profile **at least as restrictive** as their parent. The enforcement order is: `standard` (runc) < `sandboxed` (gVisor) < `microvm` (Kata). A `sandboxed` parent cannot delegate to a `standard` child. The `minIsolationProfile` field in the lease enforces this, and the gateway validates it before approving any delegation.

**Enforcement point and audit trail:** Isolation monotonicity is enforced **at delegation time** (runtime), not at `DelegationPolicy` registration time. This means a tag-based policy rule (e.g., `matchLabels: { team: platform }`) may match pools with varying isolation levels, and a monotonicity violation is only detected when a specific delegation is attempted. When the gateway rejects a delegation due to an isolation monotonicity violation, it emits a `delegation.isolation_violation` audit event (see Section 11.7) containing the parent session ID, requested target, parent isolation profile, target isolation profile, and the `DelegationPolicy` rule that matched. The delegation is rejected with error code `ISOLATION_MONOTONICITY_VIOLATED`.

> **Deployer guidance:** Because tag-based `DelegationPolicy` rules are evaluated dynamically and may match pools added after the policy was created, deployers **should** review delegation policies for isolation-level consistency whenever new pools or runtimes are registered. Specifically, verify that all pools matchable by a given policy rule have an isolation profile >= the isolation profile of any pool that could be the delegating parent. The `lenny-ctl policy audit-isolation` CLI command (see Section 17.6) can be used to report all policy-pool combinations where a monotonicity violation would occur at runtime.

**Tree-wide limits:** `maxTreeSize` caps the total number of pods across the entire task tree (all depths), preventing exponential fan-out. `maxTokenBudget` caps total LLM token consumption across the tree.

**Budget Reservation Model:**

Delegation budgets use an **atomic reservation** model, not a ceiling model. When the gateway processes a `delegate_task` call:

1. **Reservation:** The gateway reserves budget using a single Redis Lua script that atomically performs both the token `DECRBY` and tree-size `INCR` within one evaluation. The Lua script checks both the remaining token budget and the tree-size limit before committing either change; if either check fails, neither operation is applied and the script returns a structured result indicating which limit was exceeded (`TOKEN_BUDGET_EXHAUSTED` or `TREE_SIZE_EXCEEDED`). This eliminates TOCTOU windows between the two counters and removes the need for compensating rollback operations. If the remaining budget is insufficient, the delegation is rejected with `BUDGET_EXHAUSTED` before pod allocation.

2. **Default slice:** When `lease_slice` is omitted, the child receives `min(remaining_parent_budget, deployer_configurable_default_fraction)`. The default fraction is 50% of remaining budget, configurable per environment via `defaultDelegationFraction` (range: 0.1 to 1.0). If the remaining budget is below a minimum usable threshold (configurable, default 10,000 tokens), the delegation is rejected.

3. **Return on completion:** When a child session reaches a terminal state (completed, failed, cancelled, expired), the gateway credits unused budget back to the parent's available pool via atomic Redis `INCRBY`. Unused budget = child's allocated budget minus child's actual consumption (including all descendants). The parent's `maxTokenBudget` ceiling is never exceeded — returns only restore up to the original allocation.

4. **Concurrency safety:** All budget operations (reserve, consume, return) use Redis atomic operations. The reservation step (token budget + tree size) uses a single Redis Lua script (`budget_reserve.lua`) that atomically: (a) reads the current token budget and tree-size counters, (b) checks the token budget result against the requested slice and the tree-size result against `maxTreeSize`, (c) applies both `DECRBY` (token budget) and `INCR` (tree size) only if both checks pass, and (d) returns a structured result `{status, remaining_tokens, current_tree_size}`. Because Redis executes Lua scripts atomically, there is no TOCTOU window between the two counters — concurrent delegation requests never observe a partially applied reservation. The return-on-completion path (step 3) uses an analogous Lua script (`budget_return.lua`) that atomically credits unused tokens via `INCRBY` and decrements tree size via `DECR`, ensuring the reverse operation is equally atomic.

**Credential propagation:** Controls how child sessions get LLM provider credentials:

| Mode          | Behavior                                                                                                      |
| ------------- | ------------------------------------------------------------------------------------------------------------- |
| `inherit`     | Child uses the same credential pool/source as parent (gateway assigns from same pool)                         |
| `independent` | Child gets its own credential lease based on the tenant's credential policy and the child Runtime's `supportedProviders` |
| `deny`        | Child receives no LLM credentials (for runtimes that don't need LLM access, e.g., pure file-processing tools) |

**Credential availability pre-check at delegation time:** When the gateway processes a `delegate_task` call with `credentialPropagation: inherit` or `independent`, it performs the same pre-claim credential availability check described in Section 4.9 before claiming a warm pod. For `inherit` mode, the gateway verifies that the parent's credential pool has at least one assignable slot (`active leases < maxConcurrentSessions` for at least one credential in the pool). For `independent` mode, the gateway checks the intersection of the child runtime's `supportedProviders` and the tenant's `credentialPolicy.providerPools`. If no credential is available, the delegation is rejected with `CREDENTIAL_POOL_EXHAUSTED` before pod allocation — no warm pod is wasted. This is a point-in-time check, not a reservation: concurrent `delegate_task` calls can each pass the check individually while collectively exhausting the pool. If the actual credential assignment fails after pod claim (due to this race), the gateway releases the pod back to the warm pool and returns `CREDENTIAL_POOL_EXHAUSTED`, consistent with the session-creation behavior in Section 7.1.

> **Deployer guidance — `inherit` mode and fan-out:** The `inherit` credential propagation mode is not suitable for high fan-out delegation trees. Because all descendants share the parent's credential pool, a tree with `maxParallelChildren` exceeding the pool's `maxConcurrentSessions` (divided by expected tree depth) will experience credential exhaustion failures. Use `credentialPropagation: independent` when `maxParallelChildren > pool.maxConcurrentSessions / expected_tree_depth`. The `orchestrator` preset uses `independent` by default for this reason.

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

Clients reference presets by name in the WorkspacePlan: `"delegationLease": "standard"`. Presets can be partially overridden with inline fields: `"delegationLease": {"preset": "standard", "maxDepth": 2}`. If no delegation lease is specified, the Runtime's default applies. At Tier 3 with 10,000 sessions using the `orchestrator` preset, aggregate child pod demand can reach hundreds of thousands — deployers should size warm pools accordingly (Section 17.8).

### 8.4 Approval Modes

| Mode       | Behavior                                                                  |
| ---------- | ------------------------------------------------------------------------- |
| `policy`   | Gateway auto-approves if request matches lease constraints                |
| `approval` | Gateway pauses parent, surfaces delegation request to client for approval |
| `deny`     | Delegation not permitted                                                  |

### 8.5 Delegation Tools

Available on the platform MCP server for every delegation-capable pod:

| Tool                                              | Purpose                                                                                                                                                                                                                                                                                                                                    |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `lenny/delegate_task(target, task, lease_slice?)` | Spawn a child session (target is opaque — runtime, derived runtime, or external agent)                                                                                                                                                                                                                                                     |
| `lenny/await_children(child_ids, mode)`           | Wait for multiple children (`all`, `any`, or `settled`). Streaming response — unblocks on `input_required`.                                                                                                                                                                                                                                |
| `lenny/cancel_child(child_id)`                    | Cancel a child (cascades to its descendants per policy)                                                                                                                                                                                                                                                                                    |
| `lenny/discover_agents(filter?)`                  | List available delegation targets, filtered by effective delegation policy                                                                                                                                                                                                                                                                 |
| `lenny/send_message(to, message)`                 | Send a message to any task by taskId. Returns `deliveryReceipt` (Section 7.2). Returns error for terminal targets; queues with TTL for recovering targets.                                                                                                                                                                                 |
| `lenny/request_input(parts)`                      | Block until answer arrives (replaces stdout `input_required`)                                                                                                                                                                                                                                                                              |
| `lenny/get_task_tree()`                           | Return task hierarchy with states. Each node includes `taskId`, `state`, and `runtimeRef`. A child session can discover its siblings (other children of its parent) by inspecting the tree. Combined with `lenny/send_message` under `siblings` messaging scope (Section 7.2), this enables sibling coordination without additional tools. |

### 8.6 Lease Extension

Lease extension is part of the **adapter↔gateway gRPC lifecycle**, not the platform MCP server. The runtime never calls it and is never aware it happened.

**Trigger:** When the LLM proxy rejects a call for budget exhaustion, the adapter automatically requests a lease extension from the gateway via the gRPC control channel. This avoids the chicken-and-egg problem where a runtime would need LLM tokens to reason about requesting more tokens. On `GRANTED` or `PARTIALLY_GRANTED`, the adapter retries the failed LLM call transparently — the runtime sees a slightly slow LLM response, not a failure. On `CEILING_REACHED` (zero grant), the adapter MUST NOT retry the extension; it propagates `BUDGET_EXHAUSTED` to the runtime as a terminal error. On `REJECTED` (user denied), the adapter propagates `BUDGET_EXHAUSTED` to the runtime.

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
    maxExtendableBudget: 2000000 # absolute ceiling, never exceeded
```

```yaml
# Tenant level (admin API) — per-tenant overrides and optional ceiling
leaseExtension:
  extensionApproval: auto # overrides deployment default
  maxExtendableBudget: 1000000 # overrides deployment default
  max:
    maxExtendableBudget: 1500000 # tenant ceiling, capped by deployment max
```

```yaml
# Runtime level (admin API) — per-runtime overrides
leaseExtension:
  extensionApproval: elicitation # overrides tenant setting
  coolOffSeconds: 10 # overrides deployment default
  maxExtendableBudget: 800000 # overrides tenant setting
```

**Example resolutions with deployment default 500K, deployment max 2M, tenant max 1M:**

| Runtime sets | Tenant sets | Effective `maxExtendableBudget` | Why                                                        |
| ------------ | ----------- | ------------------------------- | ---------------------------------------------------------- |
| 800K         | 300K        | 800K                            | Runtime overrides tenant; under both ceilings              |
| 1.5M         | —           | 1M                              | Runtime overrides deployment default; capped by tenant max |
| —            | 300K        | 300K                            | Tenant overrides deployment default                        |
| —            | —           | 500K                            | Deployment default                                         |
| 2.5M         | —           | 1M                              | Capped by tenant max (which is < deployment max)           |

#### Approval Modes

**`auto` mode:** Each request is handled independently. The gateway grants the requested amount up to the effective max. No elicitation, no queuing, no cool-off. The response includes the granted amount and status: `GRANTED` (full amount), `PARTIALLY_GRANTED` (capped to remaining headroom), or `CEILING_REACHED` (zero grant — ceiling already reached). The adapter MUST NOT retry after `CEILING_REACHED`.

**`elicitation` mode (default):** Requests are serialized per task tree. The gateway presents at most one elicitation to the user at a time, with concurrent request batching and a cool-off window after approval.

**Elicitation mode flow:**

1. **First request** in a tree triggers a generic elicitation to the client: _"The agent needs more budget to continue. Approve?"_ No specific token amounts are shown.
2. **Concurrent requests** arriving while the elicitation is pending are queued silently. No duplicate elicitation is sent.
3. **User approves:**
   - The gateway grants each queued request its individually requested amount, adding those tokens to the tree budget (capped at the effective max).
   - A **cool-off window** starts.
   - New requests arriving during the cool-off period are auto-granted their requested amounts with no elicitation.
   - If granting a request's full amount would exceed the effective max, the gateway caps the grant to whatever headroom remains.
   - All requests tied to a single elicitation + cool-off period are processed as part of the approved batch. Each extension response includes the **granted amount** (`grantedTokenBudget`, `grantedChildren`, etc.) so the adapter knows exactly what was received. If a request's grant was capped, the response status is `PARTIALLY_GRANTED` with the actual amounts. If the grant is **zero** because the ceiling was already reached, the response status is `CEILING_REACHED` (not success) — the adapter MUST treat this as a terminal condition and propagate `BUDGET_EXHAUSTED` to the runtime rather than retrying the extension. The adapter MUST NOT retry extension requests after receiving `CEILING_REACHED`; doing so would create infinite retry loops (each request returns zero grant, each LLM call fails, adapter requests again). Extension response statuses: `GRANTED` (full amount), `PARTIALLY_GRANTED` (capped but non-zero), `CEILING_REACHED` (zero grant — ceiling hit), `REJECTED` (user denied).
   - After the cool-off window expires, the next request starts a new elicitation cycle (back to step 1).
4. **User rejects:**
   - All queued requests are rejected.
   - The **requesting subtree** (the session that triggered the elicitation and its descendants) is marked as **extension-denied**. Other subtrees in the same task tree are unaffected and may still request extensions independently.
   - A **rejection cool-off period** begins (duration: `rejectionCoolOffSeconds`, configurable per deployment/tenant/runtime using the same layering as `coolOffSeconds`, default `300`). During the cool-off period, new extension requests from the denied subtree are auto-rejected without elicitation. After the cool-off expires, the subtree may request extensions again, which triggers a new elicitation cycle (back to step 1).
   - Operators can clear the extension-denied flag immediately via the **admin API** (`DELETE /admin/v1/trees/{treeId}/subtrees/{sessionId}/extension-denial`). This resets the subtree to normal extension behavior regardless of cool-off state.

**Scope:**

- Extensions apply to the requesting session only
- Existing children are **unaffected** — their leases remain as originally granted
- Only new children spawned after the extension benefit from the expanded parent budget

**Audit:** Every extension request is logged with: requesting session, requested amounts, approval mode, outcome (approved/denied/capped), approver (gateway-auto or client), granted amount, effective max at time of request, resulting new limits, batch id (groups requests tied to the same elicitation + cool-off period), gateway_replica_id, client_ip.

### 8.7 File Export Model

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

### 8.8 TaskRecord and TaskResult Schema

#### TaskRecord

Task records use a messages array forward-compatible with multi-turn dialog:

```json
{
  "schemaVersion": 1,
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

**Lenny canonical task state machine:**

Lenny defines its own task states independent of any external protocol. External protocol adapters map to/from these states at the boundary.

```
submitted → running → completed        (terminal)
                    → failed            (terminal)
                    → cancelled         (terminal — via lenny/cancel_child or cascade policy)
                    → expired           (terminal — lease/budget/deadline exhausted)
                    → input_required    (reachable via lenny/request_input)

input_required → running               (input provided via lenny/send_message with inReplyTo)
input_required → cancelled             (parent cancels while awaiting input)
input_required → expired               (deadline reached while awaiting input)
```

Terminal states: `completed`, `failed`, `cancelled`, `expired`.

**Protocol mapping:**

| Lenny state      | MCP Tasks             | A2A (future)              |
| ---------------- | --------------------- | ------------------------- |
| `submitted`      | `submitted`           | `submitted`               |
| `running`        | `working`             | `working`                 |
| `completed`      | `completed`           | `completed`               |
| `failed`         | `failed`              | `failed`                  |
| `cancelled`      | `canceled` (MCP)      | `canceled` (A2A)          |
| `expired`        | `failed` + error code | `failed` + error metadata |
| `input_required` | `input_required`      | `input-required`          |

Notes: A2A's `unknown` state maps to a gateway-level error (task ID not found or not visible), not to a Lenny task state. MCP uses American spelling `canceled`; Lenny uses `cancelled` internally — adapters handle the spelling difference. `expired` has no direct equivalent in MCP or A2A; adapters surface it as `failed`/`canceled` with a structured error code indicating the expiry reason.

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
- `settled` — wait until all children reach a terminal state (completed, failed, cancelled, or expired). Returns list of `TaskResult`.

**`lenny/await_children` unblocks on `input_required`:** When a child enters `input_required` state, the parent's `lenny/await_children` call yields a partial result carrying the child's question and `requestId`. The gRPC `AwaitChildren` call is a streaming response — it yields partial events before the final settled result. The parent can respond via `lenny/send_message` with `inReplyTo: "req_001"`, which resolves the child's blocked `lenny/request_input` tool call directly. The parent then re-awaits.

**Re-await semantics (multiple `input_required` children):** A single `lenny/await_children` stream can yield multiple `input_required` partial results — one per child that blocks. The parent handles each partial result independently:

1. The stream yields an `input_required` event for child A. The parent responds via `lenny/send_message` with `inReplyTo` targeting child A's `requestId`.
2. While the parent is processing child A's question, child B may also enter `input_required`. The stream yields a second partial result for child B.
3. The parent responds to child B in the same manner. Both children resume independently once their respective `request_input` calls are resolved.
4. The parent does **not** need to close and re-open the `await_children` call between partial results — the stream remains open until the final settled/completed result.

**Multi-child `input_required` handling pattern:**

```
parent calls lenny/await_children(["child_A", "child_B"], mode="all")
  ← stream yields: { childId: "child_A", state: "input_required", requestId: "req_001", parts: [...] }
  parent calls lenny/send_message(target: "child_A", inReplyTo: "req_001", parts: [...])
  ← stream yields: { childId: "child_B", state: "input_required", requestId: "req_002", parts: [...] }
  parent calls lenny/send_message(target: "child_B", inReplyTo: "req_002", parts: [...])
  ← stream yields: { childId: "child_A", state: "completed", output: {...} }
  ← stream yields: { childId: "child_B", state: "completed", output: {...} }
  ← stream closes (all settled)
```

**Subtree deadlock detection:** The gateway detects deadlocked subtrees: if every running task in a subtree (parent plus all descendants) is blocked — either in `input_required` or in `await_children` waiting only on `input_required` children — and no task in the chain can make progress, the gateway marks the subtree as `deadlocked`. The root task of the deadlocked subtree receives a `deadlock_detected` event on its `await_children` stream, carrying the list of blocked `requestId` values and their originating task IDs. The root task's agent can then break the deadlock by responding to one or more of the pending `request_input` calls, or by cancelling blocked children. If the deadlock is not resolved within `maxDeadlockWaitSeconds` (default: 120, configurable per pool), the gateway fails the deepest blocked tasks with error code `DEADLOCK_TIMEOUT`.

### 8.9 Task Tree

The gateway maintains a complete task DAG:

```
root_task (client → pod A)
├── child_task_1 (pod A → pod B)
│   └── grandchild_task_1 (pod B → pod C)
└── child_task_2 (pod A → pod D)
```

Each node tracks: session_id, generation, pod, state, lease, budget consumed, failure history.

### 8.10 Delegation Tree Recovery

The gateway tracks the full task tree **independently of pods** in the SessionStore. This enables recovery when any node in the tree fails.

**Recovery ordering:** The gateway recovers delegation trees **bottom-up** (leaves first). For each level, the gateway attempts recovery of all nodes at that depth before moving to the next level up. This ensures that by the time a parent resumes, its children are already in a known state (recovered, failed, or still running).

**Per-level and total tree timeouts:**

| Parameter                 | Default | Scope          | Description                                                                                          |
| ------------------------- | ------- | -------------- | ---------------------------------------------------------------------------------------------------- |
| `maxLevelRecoverySeconds` | 120     | Per tree level | Maximum time the gateway waits for all nodes at a single depth to complete recovery before giving up |
| `maxTreeRecoverySeconds`  | 600     | Entire tree    | Total wall-clock bound for recovering the full delegation tree; overrides per-level budgets          |

If `maxLevelRecoverySeconds` is exceeded for a given depth, unrecovered nodes at that level are marked as terminally failed and the gateway continues upward. If `maxTreeRecoverySeconds` is exceeded, all remaining unrecovered nodes are marked as terminally failed and cascade policies apply from that point.

**Interaction with `maxResumeWindowSeconds`:** A node's individual resume window (Section 7.3) runs concurrently with tree recovery. If a node's `maxResumeWindowSeconds` expires before tree recovery reaches it, that node transitions to `expired` and its cascade policy is applied. Conversely, `maxTreeRecoverySeconds` can terminate a node's recovery attempt even if its `maxResumeWindowSeconds` has not yet elapsed. The effective recovery window for any node is therefore `min(maxResumeWindowSeconds, remaining maxTreeRecoverySeconds)`.

**Parent pod failure with active children:**

1. Gateway detects parent failure
2. Children continue running (they are independent pods with their own sessions)
3. Gateway initiates bottom-up tree recovery (see ordering above)
4. If parent resumes on a new pod:
   a. Gateway re-injects virtual MCP child interfaces for all still-active children
   b. Parent session receives a `children_reattached` event listing current child states
   c. Parent can continue awaiting, canceling, or interacting with children
5. If parent reaches a terminal failure state (retry exhaustion, `maxResumeWindowSeconds` expiry, or `maxTreeRecoverySeconds` expiry → `expired`):
   a. Gateway applies the parent's `cascadeOnFailure` policy (see below)

**Cascading behavior (configurable per delegation lease):**

| Policy             | Behavior                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------- |
| `cancel_all`       | Cancel all descendants immediately                                                                              |
| `await_completion` | Let running children finish (up to `cascadeTimeoutSeconds`), then collect results                               |
| `detach`           | Children become orphaned; results are stored but no parent collects them. Client can query via `get_task_tree`. |

Default: `cancel_all`.

**`cascadeTimeoutSeconds` default:** 3600 (1 hour). Deployer-configurable via Helm (`delegation.cascadeTimeoutSeconds`). This bounds how long `await_completion` children may run after parent failure and how long `detach` orphans persist before cleanup. Deployers may set a platform-wide cap; per-lease values cannot exceed the cap.

**Child failure notification:**

When a child fails, the gateway injects a `child_failed` event into the parent's session stream with:

- `child_task_id`
- failure classification (transient/permanent)
- error details
- whether retries were exhausted

The parent agent can then decide to: re-spawn a replacement, continue with partial results, or propagate the failure upward.

**Orphan cleanup:** A background job runs every 60 seconds (configurable via Helm `delegation.orphanCleanupIntervalSeconds`) and detects task tree nodes whose root session has been terminated and whose `cascadeTimeoutSeconds` has expired. Orphaned children are terminated and their artifacts follow standard retention policy.

The cleanup job emits the following metrics:

| Metric                            | Type    | Description                                    |
| --------------------------------- | ------- | ---------------------------------------------- |
| `lenny_orphan_cleanup_runs_total` | Counter | Total cleanup job executions                   |
| `lenny_orphan_tasks_terminated`   | Counter | Orphan tasks terminated by cleanup             |
| `lenny_orphan_tasks_active`       | Gauge   | Currently active orphan tasks awaiting cleanup |

Deployers should alert when `lenny_orphan_tasks_active` exceeds a deployment-specific threshold (suggested: 50).

> **Note:** Detached orphan pods are **not** counted toward the originating user's concurrency quota during the detached window. Their lifetime is bounded by `cascadeTimeoutSeconds` (default 3600s), which limits exposure. If quota-aware orphan accounting is needed, it can be added in a future version.

---

## 9. MCP Integration

### 9.1 Where MCP Is Used

| Boundary                         | Protocol                                            | Why                                                                                                                     |
| -------------------------------- | --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Client ↔ Gateway                 | MCP (Streamable HTTP) via `ExternalAdapterRegistry` | Tasks, elicitation, auth discovery, tool surface. Also OpenAI Completions, Open Responses, and other external adapters. |
| Adapter ↔ Runtime (intra-pod)    | MCP (local Unix socket servers)                     | Platform tools, per-connector tool servers. See Section 4.7.                                                            |
| Parent pod ↔ child (via gateway) | MCP (virtual interface)                             | Delegation, tasks, elicitation forwarding                                                                               |
| Gateway ↔ external MCP tools     | MCP                                                 | Tool invocation, OAuth flows                                                                                            |
| Gateway ↔ `type:mcp` runtimes    | MCP (dedicated endpoints at `/mcp/runtimes/{name}`) | Direct MCP server access. Implicit session for audit/billing.                                                           |
| Gateway ↔ pod runtime control    | Custom gRPC/HTTP+mTLS                               | Lifecycle, uploads, checkpoints, lease extension — not MCP-like                                                         |

#### Platform MCP Server Tools

The platform MCP server (available to `type: agent` runtimes via the adapter manifest) exposes:

| Tool                        | Purpose                                                       |
| --------------------------- | ------------------------------------------------------------- |
| `lenny/delegate_task`       | Spawn a child session (target is opaque)                      |
| `lenny/await_children`      | Wait for children (streaming, unblocks on `input_required`)   |
| `lenny/cancel_child`        | Cancel a child and its descendants                            |
| `lenny/discover_agents`     | List available delegation targets (policy-scoped)             |
| `lenny/output`              | Emit output parts to the parent/client                        |
| `lenny/request_elicitation` | Request human input via the elicitation chain                 |
| `lenny/memory_write`        | Write to the memory store (see Section 9.4)                   |
| `lenny/memory_query`        | Query the memory store                                        |
| `lenny/request_input`       | Block until answer arrives (replaces stdout `input_required`) |
| `lenny/send_message`        | Send a message to any task by taskId                          |
| `lenny/get_task_tree`       | Return task hierarchy with states                             |

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

**Interaction with `input_required` deadlock detection:** Elicitation chains and `input_required` chains are independent blocking mechanisms, but both participate in the gateway's subtree deadlock detection (Section 8.7). A task blocked on an elicitation waiting for a human response is **not** considered deadlocked — it is waiting on an external actor. However, a task blocked on `lenny/request_input` is waiting on its parent, which is an internal actor. If the parent is itself blocked (on its own `request_input` or on `await_children` with all children in `input_required`), the gateway's deadlock detector treats this as a circular wait.

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

**Tenant isolation contract.** All `MemoryStore` implementations MUST guarantee that `Write`, `Query`, `Delete`, and `List` operations are strictly scoped to the `TenantID` in the supplied `MemoryScope`. Cross-tenant reads and writes MUST be impossible regardless of application-layer correctness. The interface boundary MUST validate that `MemoryScope.TenantID` is non-empty before dispatching to the underlying implementation — calls with an empty `TenantID` are rejected with an error, never silently defaulted.

**Default implementation:** Postgres + pgvector. The `memories` table carries a `tenant_id` column with the same RLS policy as all other tenant-scoped tables (see Section 4.2): rows are filtered by `current_setting('app.current_tenant', false)`, and every query runs inside a transaction preceded by `SET LOCAL app.current_tenant`. The `connect_query` sentinel and cloud-managed pooler trigger (Section 12.3) cover the `memories` table identically to other tenant-scoped tables.

**Custom implementations:** The store is fully replaceable. Deployers who want Mem0, Zep, or another vector database implement the interface backed by their choice. Custom implementations MUST enforce tenant isolation equivalent to the default — a `ValidateMemoryStoreIsolation(t *testing.T, store MemoryStore)` contract validation helper is provided so deployers can verify their implementation. Technology choice explicitly deferred — the memory layer market is not settled as of Q1 2026.

**Integration test:** `TestMemoryStoreTenantIsolation` verifies that a `Write` with `TenantID=A` followed by a `Query` with `TenantID=B` returns zero results, and that calls with an empty `TenantID` are rejected. This test runs against the default Postgres implementation at startup and is available for custom implementations.

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

**Coordinator handoff protocol:** When a replica acquires the coordination lease (Redis or Postgres), it must execute the following sequence before sending any RPCs to the pod:

1. **Increment generation:** Atomically increment `coordination_generation` on the session row in Postgres (`UPDATE sessions SET coordination_generation = coordination_generation + 1 ... RETURNING coordination_generation`). The new value becomes this replica's **local generation stamp**.
2. **Fence announcement (precondition):** Send a `CoordinatorFence(session_id, new_generation)` RPC to the pod with a 5-second deadline. The pod records the new generation and from this point rejects any RPC carrying an older generation. **This step is a hard precondition for step 3: the new coordinator MUST NOT send any operational RPC to the pod until `CoordinatorFence` returns a successful acknowledgement.** Until the pod acknowledges the fence, the pod still accepts RPCs carrying the previous generation, which means the prior coordinator can continue sending RPCs — the generation increment in Postgres alone does not close this window. The fence acknowledgement is what closes it.
   - **If `CoordinatorFence` fails or times out:** The new coordinator must retry the fence RPC with the same generation value (up to 3 attempts with 1-second backoff). If all retries are exhausted, the new coordinator must relinquish the lease — it stops extending the Redis TTL and releases the Postgres `FOR UPDATE` lock — and backs off with jittered delay (initial 2s, max 16s) before reconsidering coordination. The generation increment remains in Postgres; the next coordinator to acquire the lease will increment it again, issuing a higher generation value that supersedes both.
3. **Begin coordination:** All subsequent gateway→pod RPCs include the local generation stamp. The pod accepts only RPCs whose generation matches the fenced value. Because fence confirmation is required before this step is reached, there is no window in which both the old and new coordinator can simultaneously issue accepted RPCs to the pod.

**Dual-store unavailability (Redis + Postgres both down):** If both Redis and Postgres are simultaneously unreachable, replicas cannot acquire or verify coordination leases through either mechanism. In this state:

1. **Existing sessions continue:** Replicas that already hold an active coordination lease (cached locally with a known generation) continue serving their existing sessions using in-memory state. Gateway→pod RPCs proceed normally — the pod validates the generation stamp, which remains valid because no new coordinator can increment it while Postgres is down.
2. **New sessions are rejected:** Session creation requires a Postgres INSERT, so new `session.create` requests are rejected with `503 Service Unavailable` and a `Retry-After` header (recommended: 10s). Clients are expected to retry with backoff.
3. **Coordination handoffs are frozen:** No replica can increment `coordination_generation` while Postgres is unreachable, so no handoffs occur. If a coordinating replica crashes during this window, its sessions become uncoordinated until at least one store recovers. The pod detects coordinator loss via gRPC transport failure (see **Coordinator-loss detection** below) and enters a hold state awaiting a new coordinator.
4. **Duration bound:** This degraded mode is bounded by the Postgres RTO (< 30s for managed HA deployments). If dual unavailability exceeds `dualStoreUnavailableMaxSeconds` (default: 60s), replicas begin gracefully terminating sessions that have had no successful store interaction, emitting `session.terminated` with reason `store_unavailable` when Postgres recovers.
5. **Observability:** Replicas emit a `dual_store_unavailable` metric (gauge, 1 while both stores are unreachable) and fire alert `DualStoreUnavailable` immediately on detection.

**Coordinator-loss detection:** The gateway→pod gRPC channel uses standard gRPC keepalive probes (`GRPC_ARG_KEEPALIVE_TIME_MS: 10000`, `GRPC_ARG_KEEPALIVE_TIMEOUT_MS: 5000`). If the coordinating gateway replica crashes or becomes network-partitioned, the pod's gRPC transport layer detects the broken connection within 15 seconds (one keepalive interval plus one timeout). Upon detecting connection loss with no active coordinator, the adapter enters **hold state**:

- **Hold state semantics:** The adapter pauses all runtime activity (no new tool results are delivered, no new lifecycle signals are sent). The runtime process continues running but receives no new instructions. The adapter accepts inbound gRPC connections — specifically, it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state. All other inbound RPCs are rejected with `UNAVAILABLE` status and a `coordinator_hold` error detail until a new coordinator successfully fences.
- **Hold state timeout:** If no new coordinator issues a successful `CoordinatorFence` within `coordinatorHoldTimeoutSeconds` (default: 120s), the adapter initiates graceful session termination — it sends `terminate` on the lifecycle channel and emits a `session.terminated` event with reason `coordinator_lost` once a coordinator reconnects (or writes it to local disk for post-mortem if no coordinator ever returns).
- **Observability:** The adapter emits a `lenny_adapter_coordinator_hold` gauge (1 while in hold state) and logs a structured `coordinator_connection_lost` event with the last known generation.

**Stale replica behavior:** When a replica receives a generation-stale rejection (from a pod or from a failed Postgres CAS on the session row), it must:

1. **Stop RPCs immediately:** Cancel all in-flight RPCs for that session. Do not retry — the session now belongs to a different coordinator.
2. **Clear local state:** Discard all cached session state (in-memory streams, pending tool calls, buffered events) for that session.
3. **Exponential backoff:** If the replica believes it should re-acquire coordination (e.g., it still holds a Redis lease that has not yet expired), it must back off with jittered exponential delay (initial 500ms, max 8s) before re-checking the generation in Postgres. If the generation has advanced beyond its own, it must release the lease and stop contending.
4. **Log and metric:** Emit a `coordinator_preempted` structured log and increment the `lenny_coordinator_handoff_stale_total` counter for observability.

**Custom metrics pipeline:** Each gateway replica exposes `lenny_gateway_active_streams` (a per-replica gauge of in-flight streaming connections) on its `/metrics` endpoint. Prometheus scrapes these endpoints, and the **Prometheus Adapter** (`k8s-prometheus-adapter`) is configured to surface this metric to the Kubernetes custom metrics API (`custom.metrics.k8s.io/v1beta1`), making it available to the HPA. As an alternative, **KEDA** can be used with a Prometheus scaler trigger targeting the same metric, which simplifies HPA manifest authoring for teams already running KEDA.

**HPA scale-up policy:** Gateway workloads are inherently bursty — a spike in session creation or streaming connections can exhaust existing replicas within seconds, while default HPA behavior introduces 30–60s of lag before new replicas appear. To absorb bursts before scale-up completes, set `minReplicas` high enough that idle replicas can handle expected burst peaks (per-tier values in Section 17.8). Configure aggressive scale-up behavior: `behavior.scaleUp.stabilizationWindowSeconds: 0` (react immediately) with `behavior.scaleUp.policies` using `type: Percent, value: 100, periodSeconds: 15` (double replica count every 15s) and a parallel `type: Pods, value: 4, periodSeconds: 15` (add at least 4 pods per period), combined via `selectPolicy: Max`. In addition to CPU utilization, configure leading-indicator metrics that detect load before CPU saturates: `lenny_gateway_request_queue_depth` (pending requests awaiting a handler goroutine) and `lenny_gateway_rejection_rate` (requests rejected with 429/503 per second). Surface both through the Prometheus Adapter or KEDA alongside `lenny_gateway_active_streams`. An HPA target on queue depth (e.g., `averageValue: 10`) triggers scale-up before CPU rises, closing the lag window. Per-tier scale-up thresholds and `minReplicas` burst-absorption guidance are in Section 17.8.

**HPA scale-down protection:** Use `behavior.scaleDown.stabilizationWindowSeconds: 300` and `behavior.scaleDown.policies` with `type: Pods, value: 1, periodSeconds: 60` to scale down one pod at a time, preventing mass eviction of gateway replicas during traffic dips. Per-tier scale-down policy adjustments are in Section 17.8.

**preStop hook drain:** The gateway pod spec includes a `preStop` hook that executes a staged graceful drain sequence within `terminationGracePeriodSeconds` (per-tier values in Section 17.8). The stages are:

1. **Stop accepting new work (readiness=false).** The preStop hook immediately sets the pod's readiness probe to `false`. This causes the Kubernetes Endpoints controller to remove the pod from the Service's endpoints list, which in turn causes the load balancer to stop routing new requests to this pod. The readiness flip must happen _before_ any drain logic begins, to close the window where the load balancer continues routing new requests during health check propagation lag (typically 1–5 seconds depending on load balancer and kube-proxy configuration). No new sessions or streams are accepted after this point.
2. **Wait for in-flight checkpoints (capped at 30s).** If any sessions coordinated by this replica have checkpoints in progress (triggered by periodic scheduling or pre-drain), the hook waits for those checkpoints to complete before proceeding. This prevents SIGKILL from interrupting a checkpoint upload, which would leave checkpoint state inconsistent. The wait is capped at 30 seconds — if a checkpoint has not completed within this window, it is treated as failed and the session falls back to its last successful checkpoint.
3. **Drain active streams (remaining grace period).** The hook polls `active_streams > 0` at 1-second intervals for the remainder of `terminationGracePeriodSeconds` after stages 1 and 2. This gives in-flight streams time to complete naturally or allows clients to detect the closing connection (via gRPC `GOAWAY` or SSE stream close) and reconnect to another replica via the load balancer.
4. **SIGKILL deadline.** If active streams have not drained by the grace period deadline, the process receives SIGKILL and remaining clients must reconnect. Combined with the one-pod-at-a-time scale-down policy, this ensures that at most one replica is draining at any moment.

**Long-running sessions and rolling updates:** Sessions with lifetimes approaching `maxSessionAge` (up to 7200s) will always exceed the gateway's `terminationGracePeriodSeconds` (60–120s) and will therefore be interrupted by any gateway rolling update. Sticky routing (see above) mitigates this by preferring the same replica for a session's lifetime, but sticky routing is an optimization — it is not guaranteed across all load balancer implementations. Deployments where long-running sessions are common (e.g., interactive coding sessions with `maxSessionAge` > 1800s) should: (a) configure sticky routing at the load balancer layer (e.g., Envoy session affinity, AWS ALB stickiness), (b) schedule rolling updates during low-traffic windows, and (c) monitor the `lenny_gateway_sigkill_streams_total` metric (streams terminated by SIGKILL at grace period deadline) to quantify the impact of forced disconnections.

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

Authentication alone is not sufficient for multi-tenant deployments. The platform defines a role-based access control model with five built-in roles:

- **`platform-admin`**: Full access to all endpoints across all tenants. Can manage runtimes, pools, global configuration, and platform-wide settings.
- **`tenant-admin`**: Full access scoped to their own tenant. Can manage users, quotas, credential pools, view usage, set legal holds, and configure callback URLs. Cannot access other tenants' data or platform-wide settings.
- **`tenant-viewer`**: Read-only access scoped to their own tenant. Can view sessions, runtimes, pools, usage, environments, and configuration. Cannot create, modify, or delete any resources.
- **`billing-viewer`**: Can view usage and metering data for their own tenant. Cannot view session content or manage any resources.
- **`user`**: Can create and manage their own sessions. Cannot access other users' sessions (even within the same tenant) unless explicitly granted by a tenant-admin.

**Permission matrix:**

| Operation                                      | `platform-admin`  |  `tenant-admin`  |     `tenant-viewer`      |  `billing-viewer`  |          `user`          |
| ---------------------------------------------- | :---------------: | :--------------: | :----------------------: | :----------------: | :----------------------: |
| Create / cancel own sessions                   |        Yes        |       Yes        |            No            |         No         |           Yes            |
| Read own session history                       |        Yes        |       Yes        |    Yes (read-only)       |         No         |           Yes            |
| Manage own credentials (`/v1/credentials`)     |        Yes        |       Yes        |            No            |         No         |           Yes            |
| Read other users' sessions (same tenant)       |        Yes        |       Yes        |    Yes (read-only)       |         No         | Only with explicit grant |
| Manage runtimes                                | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage pools / scaling policies                | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage credential pools                        | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| View usage / metering                          | Yes (all tenants) | Yes (own tenant) |    Yes (own tenant)      | Yes (own tenant)   |            No            |
| Manage quotas                                  | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage users / role assignments                | Yes (all tenants) | Yes (own tenant) |            No            |         No         |            No            |
| Set / release legal holds                      | Yes (all tenants) | Yes (own tenant) |            No            |         No         |            No            |
| Configure callback URLs / webhooks             | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Configure tenant RBAC config                   | Yes (all tenants) | Yes (own tenant) |            No            |         No         |            No            |
| Manage environments                            | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage delegation policies                     | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage egress profiles                         | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Issue billing corrections (operator-initiated) |        Yes        |        No        |            No            |         No         |            No            |
| Manage platform-wide settings                  |        Yes        |        No        |            No            |         No         |            No            |
| Access other tenants' data                     |        Yes        |        No        |            No            |         No         |            No            |

**Platform roles vs. environment-level roles:** Platform RBAC roles (`platform-admin`, `tenant-admin`, `tenant-viewer`, `billing-viewer`, `user`) govern access to platform API operations. Environment-level member roles (`viewer`, `creator`, `operator`, `admin` — see Section 10.6) are orthogonal and govern which runtimes and connectors a user can access within an environment context. A `user` with environment `admin` role gains runtime/connector management within that environment but does not acquire `tenant-admin` privileges (quota management, legal holds, user management, etc.). The two role systems compose: platform RBAC gates the API operation, environment RBAC gates the resource scope within that operation.

**Custom roles (tenant-scoped).** Deployers can define custom roles per tenant via the admin API (`POST /v1/admin/tenants/{id}/roles`). A custom role is a named set of permissions drawn from the same operation categories in the permission matrix above. Custom roles cannot exceed the permissions of `tenant-admin` — they can only restrict, not expand. This allows tenant-admins to create purpose-specific roles (e.g., `session-manager` that can create and view sessions but not manage quotas, or `connector-admin` that can manage connectors only). Custom roles are stored in the tenant RBAC configuration and conveyed via the same OIDC claim / platform-managed mapping as built-in roles.

**Role assignment:** Roles — both built-in and custom — are conveyed via OIDC claims (e.g., a `lenny_role` claim in the ID token, which can carry any built-in or custom role name) or via a platform-managed mapping (`user_id` → role stored in Postgres). When both sources are present, the platform-managed mapping takes precedence, allowing tenant-admins to override OIDC-derived roles within their tenant. Custom roles are assignable through the same mechanisms as built-in roles.

**Tenant-scoped admin API:** Admin endpoints (`GET /v1/usage`, `GET /v1/pools`, `GET /v1/metering/events`) are tenant-scoped for `tenant-admin`, `tenant-viewer`, and `billing-viewer` callers — they only return data belonging to the caller's tenant (with `billing-viewer` restricted to usage/metering endpoints only). `platform-admin` callers see data across all tenants, with an optional `?tenant_id=` filter.

**Future: self-service portal:** A web UI for tenant-admins to manage their configuration (users, quotas, callback URLs, legal holds) is a future goal, built on top of these tenant-scoped APIs.

### 10.3 mTLS PKI

**Certificate authority:** cert-manager with a cluster-internal CA (self-signed issuer or Vault-backed for production). This is the default; a service mesh (Istio/Linkerd) is an optional alternative.

**Certificate lifecycle:**

| Component               | Certificate TTL | SAN Format                                           | Rotation                                                |
| ----------------------- | --------------- | ---------------------------------------------------- | ------------------------------------------------------- |
| Gateway replicas        | 24h             | DNS: `lenny-gateway.lenny-system.svc`                | cert-manager auto-renewal at 2/3 lifetime               |
| Agent pods              | 4h              | SPIFFE URI: `spiffe://lenny/agent/{pool}/{pod-name}` | cert-manager auto-renewal; pod restart if renewal fails |
| Controller              | 24h             | DNS: `lenny-controller.lenny-system.svc`             | cert-manager auto-renewal                               |
| Token/Connector Service | 24h             | DNS: `lenny-token-service.lenny-system.svc`          | cert-manager auto-renewal                               |

**Pod identity:** Agent pods use SPIFFE-compatible URIs as SANs. The gateway validates the SPIFFE URI against the expected pool/pod on each connection. Each gateway replica gets a distinct certificate so compromise of one replica can be detected and revoked independently.

**Token Service identity:** The gateway validates the Token Service's DNS SAN (`lenny-token-service.lenny-system.svc`) on every mTLS connection — it must not accept any certificate from the cluster CA. The Token Service validates that incoming connections present a gateway replica certificate (matching the gateway's DNS SAN `lenny-gateway.lenny-system.svc`), rejecting connections from any other component. This mutual SAN validation ensures that compromise of an unrelated component's certificate cannot be used to impersonate either the gateway or the Token Service.

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
   - Gateway, controller, and Token Service trust bundles must include both CAs during the overlap window

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
- **Expand-contract operational rules:**
  - **Nullable columns during coexistence:** All new columns added in Phase 1 **must** be `NULL`-able (or have a server-side `DEFAULT`) until Phase 3 removes the old columns. During a rolling deploy, old-version replicas that do not know about the new column will issue `INSERT` statements that omit it — a `NOT NULL` constraint without a default causes those inserts to fail. The `NOT NULL` constraint may only be added in Phase 3, after all replicas run the new code.
  - **Minimum inter-phase wait:** Phase 2 must not be deployed until all replicas are running Phase 1 code (i.e., the Phase 1 rolling deploy is fully complete). Phase 3 must not be deployed until every record that could have been written under the old schema has either been migrated or expired. The minimum wait before Phase 3 is `max(maxSessionAge, longest_record_TTL)` for the affected table — for the session store this is `maxSessionAge` (default 7200s / 2h); for audit records it is the audit retention window. Operators must verify via query (e.g., `SELECT COUNT(*) FROM <table> WHERE <new_column> IS NULL AND created_at > now() - interval '<wait>'`) that no un-migrated records remain before deploying Phase 3.
  - **Idempotency requirements:** Phase 1 migrations must use idempotent DDL (`ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, `CREATE TABLE IF NOT EXISTS`). Phase 2 migrations (switching read paths) are application-code deploys and are inherently re-deployable. Phase 3 migrations (`ALTER TABLE ... DROP COLUMN`) are **not** idempotent — `DROP COLUMN` fails if the column does not exist. Phase 3 migrations must use `DROP COLUMN IF EXISTS` or guard with a pre-check. If any migration step fails mid-transaction, the advisory lock is released and the migration can be re-run safely because each step is wrapped in a transaction.
- **Rollback:** Down migrations are always provided but only used as a last resort. The expand-contract pattern means the previous code version is compatible with the current schema, since old columns are not removed until the code no longer reads them.
- **Locking:** `golang-migrate` uses Postgres advisory locks to prevent concurrent migrations. The lock is released on completion or failure.
- **Partial completion:** If a migration fails mid-way, the advisory lock is released and the migration can be re-run. Each migration step should be idempotent where possible (see idempotency requirements above for per-phase guidance).

**Warm Pool Controller:** Rolling update with leader election. During leader failover (near-zero on clean shutdown via voluntary lease release; up to 25s on crash), existing sessions are unaffected; only new pod creation and scaling pause.

**CRD schema versioning during rolling deploys:** CRDs follow Kubernetes API versioning conventions (shipping at `v1beta1` initially; see Section 15.5). During a rolling deploy, the gateway and controller may briefly run different versions that expect different CRD schemas. Conversion webhooks translate between CRD versions so both components operate correctly during the transition. CRD specs use `x-kubernetes-preserve-unknown-fields` on extensible sub-objects so that a controller running an older version does not crash on fields introduced by a newer gateway (or vice versa). CRD schema changes follow the same expand-contract discipline as database migrations (above): new fields are added first, both versions write them, then old fields are removed in a subsequent release.

**Helm CRD upgrade limitation:** Helm does not update CRDs on `helm upgrade` — this is a known Helm limitation. Stale CRDs after an upgrade can cause silent failures (e.g., new fields are stripped by the API server, controllers observe unexpected defaults). CRDs must be applied separately before running `helm upgrade`. See Section 17.6 for the required upgrade procedure. To detect stale CRDs at runtime, each controller validates on startup that the installed CRD schema version (read from the `lenny.dev/schema-version` annotation on the CRD object) matches the version the controller binary expects. If there is a mismatch, the controller logs a `FATAL` error — `"CRD schema version mismatch: installed=<installed>, expected=<expected>. Apply updated CRDs before upgrading. See docs/runbooks/crd-upgrade.md"` — and exits with a non-zero code, preventing the Deployment rollout from completing. See Section 17.6 for the full recovery procedure when this occurs.

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

**`noEnvironmentPolicy` semantics:**

- **`deny-all`** (default): An authenticated user who is not a member of any environment cannot access any runtime. All runtime access requires explicit environment membership.
- **`allow-all`**: An authenticated user who is not a member of any environment can access any runtime **owned by their own tenant** with no capability restrictions. Specifically:
  - "All" means all runtimes tagged to the user's tenant (`tenantId` matches the authenticated user's tenant). Runtimes owned by other tenants are never exposed regardless of this setting. Cross-tenant runtime access is architecturally impossible — the gateway resolves tenant identity before evaluating `noEnvironmentPolicy`.
  - `allow-all` does **not** bypass environment-level RBAC for users who **are** members of an environment. It only governs the fallback behavior for users with no environment membership.
  - `allow-all` applies at the tenant level, not at individual environments. There is no inheritance model — environments define their own membership and selectors independently.
  - **Security warning:** Do not set `noEnvironmentPolicy: allow-all` as the platform-wide Helm default in multi-tenant deployments. In that configuration, every authenticated user in every tenant would have unrestricted access to all runtimes within their tenant, bypassing the environment-based access control model. Restrict `allow-all` to individual tenants that have reviewed the access implications and confirmed this matches their intended security posture.

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
    status: active # active | paused | concluded
    baseRuntime: claude-worker
    variants:
      - id: treatment
        runtime: claude-worker-v2
        pool: claude-worker-v2-sandboxed-medium
        weight: 10 # percentage
    targeting:
      mode: percentage # percentage | external
      sticky: user # user | session | none
    propagation:
      childSessions: inherit # inherit | control | independent
```

**Control group identifier.** Sessions not assigned to any named variant run the `baseRuntime` and are assigned `variant_id: "control"` automatically by the gateway. `"control"` is a reserved variant identifier — deployers cannot define a variant with `id: "control"` in the `variants` list. This constraint is enforced at experiment creation and update time via `POST /v1/admin/experiments` and `PUT /v1/admin/experiments/{name}` validation; attempts to use a reserved identifier return `422` with error code `RESERVED_IDENTIFIER`. The control group's warm pool capacity is not managed by experiment machinery — it falls through to the base runtime's existing pool.

**Targeting modes:** `percentage` (deterministic hash) and `external` (webhook-based, delegates to deployer's targeting service).

**Targeting schema.** The `targeting` block in the `ExperimentDefinition` varies by mode. Each mode's additional fields are defined below:

```yaml
# Mode: percentage (default)
targeting:
  mode: percentage
  sticky: user               # user | session | none
  # No additional fields. Assignment uses deterministic hash:
  # hash(user_id + experiment_id) mod 100 < variant weight → treatment.

# Mode: external (webhook-based)
targeting:
  mode: external
  sticky: user               # user | session | none
  # Assignment is delegated to the tenant's experimentTargeting webhook.
  # No additional per-experiment fields — the webhook receives user context
  # and returns assignments for all external experiments in one call.
```

**Tenant-level experiment targeting configuration.** The `experimentTargeting` block on the tenant configuration defines how external experiment assignment is resolved. It is called once per session creation for tenants that have any active experiments with `mode: external`.

```yaml
# Tenant-level configuration
experimentTargeting:
  provider: generic              # launchdarkly | statsig | unleash | generic
  timeoutMs: 200                 # webhook call timeout; on the session creation hot path
  generic:
    webhookUrl: https://targeting.internal/evaluate
    headers:                     # optional static headers
      Authorization: "Bearer ${TARGETING_SERVICE_TOKEN}"
```

Named provider examples:

```yaml
# LaunchDarkly
experimentTargeting:
  provider: launchdarkly
  timeoutMs: 200
  launchdarkly:
    sdkKey: "${LD_SDK_KEY}"
    baseUrl: https://app.launchdarkly.com   # optional, for private instances

# Statsig
experimentTargeting:
  provider: statsig
  timeoutMs: 200
  statsig:
    serverSecret: "${STATSIG_SERVER_SECRET}"

# Unleash
experimentTargeting:
  provider: unleash
  timeoutMs: 200
  unleash:
    apiUrl: https://unleash.internal/api
    apiToken: "${UNLEASH_API_TOKEN}"
```

**Webhook protocol (generic provider).** The gateway POSTs to the configured `webhookUrl`:

Request:
```json
{
  "user_id": "user-abc-123",
  "tenant_id": "t_acme",
  "session_metadata": {
    "runtime": "claude-worker",
    "labels": { "team": "platform" }
  }
}
```

Response:
```json
{
  "assignments": [
    { "experiment_id": "sonnet-vs-opus", "variant_id": "treatment" }
  ]
}
```

Or `{ "assignments": [] }` if the user is not targeted for any experiment.

The gateway matches returned `experiment_id` values against registered experiments with `mode: external`. Unknown experiment IDs in the response are silently ignored — the external system may manage experiments not registered in Lenny. The `variant_id` must match a variant defined in the corresponding `ExperimentDefinition`, or be `"control"`. Unknown variant IDs are treated as `"control"` with a warning event. The `sticky` field on the experiment definition governs caching: `sticky: user` means the assignment is cached for the user across sessions (same as percentage mode). The webhook is not called again for subsequent sessions if a cached assignment exists. Named providers (launchdarkly, statsig, unleash) are built-in adapters compiled into the gateway binary that translate between the provider's native evaluation API and Lenny's assignment format. They handle authentication, request formatting, and response mapping internally. New providers can be supported via the generic webhook — the deployer implements a thin proxy service.

**Webhook failure behavior.** On webhook timeout or error: no experiment assignment is made for any `mode: external` experiment. The session proceeds with the base runtime, no experiment tracking, no eval attribution for external experiments. `mode: percentage` experiments on the same tenant are unaffected — they are evaluated independently by the gateway's built-in hash. The `lenny_experiment_targeting_webhook_error_total` metric is incremented, and a `experiment.targeting_webhook_failed` warning event is emitted (fields: `tenant_id`, `user_id`, `provider`, `error`). No `fallbackOnError` configuration — on failure, exclusion is the only valid behavior since the gateway has no assignment information.

**Propagation modes for `childSessions`:** Controls how experiment assignment flows through delegation (see Section 8 for delegation mechanics):

| Mode          | Behavior                                                                                                                                                            |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `inherit`     | Child session receives the parent's experiment context verbatim. The child runs the same variant and its eval results attribute to the same experiment.             |
| `control`     | Child session is forced into the base runtime (control group) regardless of the parent's variant. Eval results still attribute to the parent's experiment.          |
| `independent` | Child session is evaluated for experiment eligibility independently — it may land in a different experiment or no experiment. No context is copied from the parent. |

**Cross-experiment conflict resolution (innermost wins).** When a child session is eligible for multiple experiments (e.g., parent propagates experiment A via `inherit` while the child independently qualifies for experiment B), the **innermost assignment wins**: the child's own independent assignment takes precedence over any inherited context. This can only occur when `propagation.childSessions` is `independent`; under `inherit` or `control` the parent's experiment context is authoritative and no independent evaluation occurs.

**Eval result attribution.** Eval results submitted against a child session are attributed to the child's effective experiment context (the `experiment_id` and `variant_id` on the child's session record). Under `inherit` and `control` modes this is the root experiment. Under `independent` mode it is whatever experiment the child was independently assigned to (or `null` if none). The Results API (below) aggregates scores per experiment; delegation depth is not a grouping dimension.

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

The `inherited` field is `true` when the context was propagated from a parent (`inherit` or `control` mode) and `false` when the child was assigned independently.

**Eval result schema.** Scores submitted via `POST /v1/sessions/{id}/eval` are stored as `EvalResult` records in Postgres:

| Field           | Type     | Description                                                                                                                                                                                                                                    |
| --------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`            | uuid     | Auto-generated primary key                                                                                                                                                                                                                     |
| `session_id`    | uuid     | Session the score pertains to (foreign key)                                                                                                                                                                                                    |
| `experiment_id` | uuid     | Auto-populated by gateway from session's experiment context                                                                                                                                                                                    |
| `variant_id`    | string   | Auto-populated by gateway from session's experiment context                                                                                                                                                                                    |
| `scorer`        | string   | Identifier for the scoring method (e.g., `llm-judge`, `exact-match`)                                                                                                                                                                           |
| `score`         | float64  | Normalized score value (0.0–1.0)                                                                                                                                                                                                               |
| `scores`        | jsonb    | Optional. Multi-dimensional scores as key-value pairs (e.g., `{"coherence": 0.9, "relevance": 0.7, "safety": 1.0}`). When present, `score` should be the aggregate/summary score. Keys are scorer-defined dimension names; values are float64. |
| `metadata`      | jsonb    | Arbitrary key-value pairs (model version, prompt hash, etc.)                                                                                                                                                                                   |
| `created_at`    | RFC 3339 | Server-generated UTC timestamp                                                                                                                                                                                                                 |

**Gateway auto-association.** When the gateway receives an eval submission, it looks up the session's `experimentContext` (populated at assignment time per the experiment context block above). If the session is enrolled in an active experiment, the gateway sets `experiment_id` and `variant_id` on the `EvalResult` automatically. If the session has no experiment context, those fields are `null` and the result is still stored for non-experiment use cases.

**Eval submission request body (`POST /v1/sessions/{id}/eval`):**

```json
{
  "scorer": "llm-judge",
  "score": 0.82,
  "scores": {
    "coherence": 0.9,
    "relevance": 0.78,
    "safety": 1.0
  },
  "metadata": {
    "judge_model": "gpt-4o",
    "rubric_version": "v3"
  }
}
```

Both `score` and `scores` are optional individually, but at least one must be provided. When both are present, `score` is the aggregate and `scores` contains the per-dimension breakdown. When only `scores` is provided, the gateway does not auto-compute `score` — the caller is responsible for providing the aggregate if needed.

**Results API response (`GET /v1/experiments/{id}/results`).** Returns aggregated eval scores grouped by variant. Uses cursor-based pagination per Section 15.

```json
{
  "experiment_id": "claude-v2-rollout",
  "status": "active",
  "variants": [
    {
      "variant_id": "control",
      "sample_count": 412,
      "scorers": {
        "llm-judge": {
          "mean": 0.74,
          "p50": 0.76,
          "p95": 0.91,
          "count": 412,
          "dimensions": {
            "coherence": {
              "mean": 0.8,
              "p50": 0.82,
              "p95": 0.95,
              "count": 412
            },
            "relevance": {
              "mean": 0.71,
              "p50": 0.73,
              "p95": 0.89,
              "count": 412
            },
            "safety": { "mean": 0.99, "p50": 1.0, "p95": 1.0, "count": 412 }
          }
        },
        "exact-match": { "mean": 0.68, "p50": 0.7, "p95": 0.88, "count": 390 }
      }
    },
    {
      "variant_id": "treatment",
      "sample_count": 45,
      "scorers": {
        "llm-judge": {
          "mean": 0.81,
          "p50": 0.83,
          "p95": 0.94,
          "count": 45,
          "dimensions": {
            "coherence": { "mean": 0.88, "p50": 0.9, "p95": 0.97, "count": 45 },
            "relevance": { "mean": 0.78, "p50": 0.8, "p95": 0.92, "count": 45 },
            "safety": { "mean": 0.99, "p50": 1.0, "p95": 1.0, "count": 45 }
          }
        },
        "exact-match": { "mean": 0.72, "p50": 0.74, "p95": 0.9, "count": 42 }
      }
    }
  ],
  "cursor": "eyJsYXN0X2lkIjoiYWJjMTIzIn0="
}
```

Aggregation is computed on read (no pre-materialized rollups). The `dimensions` object is present only when at least one `EvalResult` for that scorer has a non-null `scores` field; dimension keys are the union of all keys found across results for that scorer/variant. For experiments with high eval volume, deployers can configure a Postgres materialized view refresh interval via Helm (`evalAggregationRefreshSeconds`, default: 60).

PoolScalingController manages variant pool lifecycle automatically — variant warm count derived from base pool demand signals × variant weight × safety factor.

**Experiment status transitions.** Experiment status (`active`, `paused`, `concluded`) is managed exclusively via the admin API. There is no automatic health-based rollback or promotion — an administrator must explicitly transition an experiment between states using `PATCH /v1/experiments/{id}` with `{ "status": "<new_status>" }`. Valid transitions: `active → paused`, `paused → active`, `active → concluded`, `paused → concluded`. Concluded experiments are immutable. The gateway emits an audit event (`experiment.status_changed`) on each transition, including the acting admin identity and the previous/new status.

**PoolScalingController behavior on experiment status transitions.** When an experiment transitions between states, the PoolScalingController adjusts the variant pool accordingly:

| Transition                                  | PoolScalingController Action                                                                                                                                                                                                                                                                                                                                                                 |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `active → paused`                           | Sets variant pool `minWarm` to 0. Existing warm pods are **not** pre-terminated — they remain available for in-flight sessions already assigned the variant, but no new warm pods are created. The `SandboxWarmPool` CRD is retained so the pool can be restored on re-activation.                                                                                                           |
| `paused → active`                           | Restores variant pool `minWarm` from the experiment definition (recomputed using the standard formula: `base_demand_p95 × variant_weight × safety_factor + burst_p99_claims × pod_warmup_seconds`). Normal demand-based scaling resumes.                                                                                                                                                     |
| `active → concluded` / `paused → concluded` | Sets variant pool `minWarm` to 0 and `maxWarm` to 0, triggering full drain. Once `status.readyCount == 0`, the PoolScalingController deletes the variant's `SandboxWarmPool` CRD. The `SandboxTemplate` is **not** deleted — it may be referenced by other experiments or retained for audit purposes. Deletion follows the same controller-owned field boundaries defined in Section 4.6.3. |

The PoolScalingController detects experiment status changes by watching the experiment records in Postgres during its normal reconciliation loop (the same loop that reconciles pool configuration into CRDs). No additional watch mechanism is required. If the PoolScalingController is down during an experiment transition, the variant pool retains its pre-transition scaling parameters until the controller recovers and reconciles — the `PoolConfigDrift` alert (Section 4.6.2) fires in this scenario.

**Future extension — `ExperimentHealthEvaluator`.** The admin-only transition model is the v1 design. A future version may introduce a pluggable `ExperimentHealthEvaluator` interface that watches eval scores, error rates, or custom metrics and recommends (or auto-executes) status transitions. The interface is reserved but not defined:

```go
// ExperimentHealthEvaluator is a future extension point (not implemented in v1).
// Implementations would evaluate experiment health and recommend status transitions.
type ExperimentHealthEvaluator interface {
    Evaluate(ctx context.Context, experimentID string) (Recommendation, error)
}
```

Until this interface is implemented, all experiment lifecycle decisions are manual.

**What Lenny explicitly will not build:** Statistical significance testing, automatic experiment lifecycle management (winner declaration, auto-rollback), multi-armed bandits, segment analysis. Those belong in dedicated experimentation platforms.

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

Delegation budget enforcement uses atomic reservation (see Section 8.3). Budget slices are reserved at delegation time via Redis atomic counters, preventing concurrent delegations from over-committing the parent's budget. Unused budget is returned to the parent when children complete. The Redis counters for delegation budgets follow the same durability model as token usage counters: Postgres checkpoint every `quotaSyncIntervalSeconds` (default: 30s, minimum: 10s), final reconciliation on session completion. **Delegation budget counters included in Postgres checkpoint:** The periodic Postgres checkpoint explicitly persists all delegation budget counters for each active delegation tree: `maxTreeSize` current count (active pods in the tree), `maxTokenBudget` consumed (tokens reserved and spent across the tree), and per-node budget allocations. These are written to the `delegation_tree_budget` table (keyed by `root_session_id`) alongside token usage counters in the same checkpoint transaction, ensuring atomicity between token and delegation budget state.

**Hierarchical Quota Model:** Quotas are enforced hierarchically: global → tenant → user. A user quota cannot exceed its tenant's quota. Tenant quotas are configured via the admin API or Helm values. Soft warnings are emitted at 80% of quota utilization, surfaced as billing events per Section 11.2.1. Hard limits are enforced at 100% — new sessions or requests are rejected with a `QUOTA_EXCEEDED` error. Quota reset periods are configurable per quota type: hourly, daily, monthly, or rolling window.

**Quota Update Timing:** Token usage quotas are updated in real-time via Redis counters. The runtime adapter extracts token counts from LLM provider responses and reports them to the gateway via the `ReportUsage` RPC (Section 4.7). The gateway increments Redis counters on each usage report — it does not parse LLM provider responses directly. Postgres is updated periodically at a configurable sync interval (`quotaSyncIntervalSeconds`, default: 30s, minimum: 10s) as a durable checkpoint, and on session completion as final reconciliation. The gateway enforces budget limits against Redis counters (fast path); if a session exceeds its token budget, the gateway terminates it immediately rather than waiting for session completion. During Redis unavailability (fail-open window), the gateway tracks usage in-memory per session and enforces the per-session budget locally using the per-replica ceiling formula from Section 12.4 (`tenant_limit / replica_count`). Only cross-session and per-tenant quotas may drift during this window (bounded by the per-replica ceiling formula).

**Crash Recovery for Quota Counters:** If a gateway replica crashes between Postgres sync intervals, in-flight usage data held only in Redis (or in-memory during fail-open) may be lost. On recovery, the gateway reconstructs quota counters from two sources: (1) the last Postgres checkpoint for each active session, and (2) pod-side `ReportUsage` records — each pod's runtime adapter retains a cumulative usage total that is re-reported on reconnection to a new gateway replica (Section 7.3). The gateway takes the **maximum** of the Postgres checkpoint and the pod-reported cumulative total for each session to avoid under-counting. This bounds the maximum unrecovered usage to at most one sync interval's worth of tokens for sessions whose pods were also lost simultaneously.

**Crash Recovery for Delegation Budget Counters:** Delegation tree budget counters (`maxTreeSize` active count, `maxTokenBudget` consumed) are reconstructed using the same two-source approach: (1) the last Postgres checkpoint from `delegation_tree_budget`, and (2) the gateway's authoritative knowledge of which pods are currently alive (from the SessionStore's `session → pod` mapping). On Redis recovery (restart or reconnection after outage), the gateway **must** re-validate every active delegation tree's budget state before accepting new `delegate_task` requests for that tree. The reconstruction procedure is: (a) load the last Postgres checkpoint for each active tree's `root_session_id`, (b) count currently-alive descendant pods from the SessionStore to derive the actual `treeSize`, (c) sum allocated token budgets across all active descendants for `tokenBudgetConsumed`, (d) set Redis counters to `max(postgres_checkpoint, live_count)` for tree size and `max(postgres_checkpoint, sum_of_allocations)` for token budget. **Irrecoverable trees:** If a tree's budget state cannot be reconstructed with confidence — specifically, if the Postgres checkpoint is older than `2 × quotaSyncIntervalSeconds` AND the coordinating gateway replica for the tree's root session was also lost (preventing live pod enumeration) — the tree's root session is moved to `awaiting_client_action` with reason `BUDGET_STATE_UNRECOVERABLE`, and no new delegations are accepted until the client resumes. This prevents potentially over-budget delegations. The gateway emits `lenny_delegation_budget_reconstruction_total` (counter, labeled by `outcome`: `success`, `irrecoverable`) on every reconstruction event.

**Maximum Overshoot Formula:** The worst-case quota overshoot during normal operation is: `max_overshoot = quotaSyncIntervalSeconds × max_tokens_per_second × active_sessions_per_tenant`. During a Redis fail-open window, additional drift is bounded by the per-replica ceiling (Section 12.4): `fail_open_overshoot = (tenant_limit / replica_count) × replica_count = tenant_limit` (i.e., at most 1x the tenant's configured budget before fail-closed triggers). The cumulative fail-open timer (Section 12.4, `quotaFailOpenCumulativeMaxSeconds`) further limits repeated drift accumulation. Deployers should set `quotaSyncIntervalSeconds` lower (minimum 10s) for tenants with high token throughput to reduce crash-recovery exposure.

#### 11.2.1 Billing Event Stream

The platform emits a structured billing event stream that provides per-tenant, per-session cost attribution suitable for invoice-grade billing integrations.

**Event types:**

| Event Type                       | Emitted When                                                                                                                       |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `session.created`                | A new session is created                                                                                                           |
| `session.completed`              | A session reaches a terminal state (completed, failed, cancelled, expired)                                                         |
| `delegation.spawned`             | A child session is created via recursive delegation                                                                                |
| `delegation.isolation_violation` | A delegation was rejected because the target pool's isolation profile is weaker than the parent's `minIsolationProfile`            |
| `token_usage.checkpoint`         | Periodic token usage snapshot (emitted at configurable intervals, not only at session end)                                         |
| `credential.leased`              | A credential is leased from a credential pool to a session                                                                         |
| `credential.revoked`             | A credential was emergency-revoked; all active leases against it were terminated (see Section 4.9 Emergency Credential Revocation) |
| `billing_correction`             | Corrects a previously emitted billing event (references original by sequence number)                                               |

**Event schema (all events):**

| Field                    | Type     | Description                                                                                                                                                                                                                                 |
| ------------------------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `schema_version`         | uint32   | Schema revision used to write this record (see Section 15.5 item 7)                                                                                                                                                                         |
| `sequence_number`        | uint64   | Monotonically increasing, per-tenant sequence number (no gaps allowed)                                                                                                                                                                      |
| `tenant_id`              | string   | Tenant that owns the session                                                                                                                                                                                                                |
| `user_id`                | string   | User who initiated the session (or parent session owner for delegations)                                                                                                                                                                    |
| `session_id`             | string   | Session this event pertains to                                                                                                                                                                                                              |
| `event_type`             | string   | One of the event types above                                                                                                                                                                                                                |
| `timestamp`              | RFC 3339 | Server-generated UTC timestamp at event creation                                                                                                                                                                                            |
| `tokens_input`           | uint64   | Token input count for the checkpoint window (where applicable)                                                                                                                                                                              |
| `tokens_output`          | uint64   | Token output count for the checkpoint window (where applicable)                                                                                                                                                                             |
| `pod_minutes`            | float64  | Wall-clock pod time consumed (where applicable)                                                                                                                                                                                             |
| `credential_pool_id`     | string   | Credential pool used (for `credential.leased` events)                                                                                                                                                                                       |
| `credential_id`          | string   | Specific credential used (for `credential.leased` events)                                                                                                                                                                                   |
| `corrects_sequence`      | uint64   | Sequence number of the original event being corrected (for `billing_correction` events only)                                                                                                                                                |
| `correction_reason_code` | string   | Structured reason code from a closed enum: `METERING_BUG`, `RETRY_OVERCOUNTING`, `TEST_SESSION_CLEANUP`, `GATEWAY_CRASH_RECONSTRUCTION`, `OPERATOR_MANUAL_ADJUSTMENT` (extensible via admin API). Required for `billing_correction` events. |
| `correction_detail`      | string   | Optional free-text detail supplementing the structured reason code (for `billing_correction` events only)                                                                                                                                   |
| `parent_isolation`       | string   | Parent session's isolation profile (for `delegation.isolation_violation` events)                                                                                                                                                            |
| `target_isolation`       | string   | Target pool's isolation profile (for `delegation.isolation_violation` events)                                                                                                                                                               |
| `matched_policy_rule`    | string   | The `DelegationPolicy` rule name/index that matched the target (for `delegation.isolation_violation` events)                                                                                                                                |
| `revoked_by`             | string   | Identity of the operator who triggered revocation (for `credential.revoked` events)                                                                                                                                                         |
| `revocation_reason`      | string   | Reason for revocation (for `credential.revoked` events)                                                                                                                                                                                     |
| `leases_terminated`      | uint32   | Number of active leases terminated by the revocation (for `credential.revoked` events)                                                                                                                                                      |

**Delivery sinks:**

Events are published to a deployer-configurable sink. Supported sink types:

- **Webhook URL** — Events are POSTed as JSON with HMAC-SHA256 signature headers. Failed deliveries are retried with exponential backoff and dead-lettered after exhaustion.
- **Message queue** — SQS, Google Pub/Sub, or Kafka topic. The gateway publishes asynchronously but only after the synchronous Postgres write confirms.
- **Both** — Webhook and message queue simultaneously for redundancy.

**Immutability guarantees:**

- Billing events are **always written to Postgres synchronously** via the `EventStore`, regardless of Redis availability. The Redis fail-open behavior (Section 12.4) applies only to rate-limit counters — billing events are never deferred, batched, or dropped. **During Postgres failover:** If the synchronous write fails due to Postgres unavailability, the billing event is queued in a **bounded in-memory write-ahead buffer** on the gateway replica (max `billingWriteAheadBufferSize`, default: 10,000 events per replica). Queued events are flushed to Postgres in sequence-number order once connectivity is restored, preserving the monotonic ordering guarantee. If the buffer fills before Postgres recovers, the gateway rejects new session-progressing requests (returning `503`) to maintain the invariant that no billable work occurs without a corresponding billing record. The buffer is not persisted to disk — if a gateway replica crashes with buffered events, those events are reconstructed from pod-reported token usage during session recovery (Section 7.3).
- The `EventStore` billing table uses append-only semantics (INSERT only, no UPDATE or DELETE grants), matching the audit log integrity model.
- Each event carries a monotonic `sequence_number` scoped to the tenant, enabling consumers to detect gaps and request replays via the metering API.
- Events are retained for a deployer-configurable retention period (default: 13 months) to support annual billing cycles and dispute resolution.

**Correction semantics:**

A `billing_correction` event adjusts a previously emitted event without violating append-only immutability. The correction carries its own `sequence_number` and references the original event via `corrects_sequence`. The `tokens_input`, `tokens_output`, and `pod_minutes` fields on a correction represent the **replacement values** for the original event's corresponding fields. Consumers reconstruct the accurate billing ledger by processing events in `sequence_number` order and applying each correction to the referenced original: when a `billing_correction` is encountered, its values supersede the original event's values for that field set. Multiple corrections to the same original are applied in sequence-number order, with the latest correction taking precedence. The original event remains in the stream unchanged, preserving the full audit trail.

**Billing correction authorization:**

Billing corrections alter the authoritative revenue record. Corrections fall into two categories with different authorization models:

**Category 1 — Gateway-emitted automated reconciliation.** The gateway emits `billing_correction` events through the normal `EventStore` write path during routine operational reconciliation. These corrections are not operator-initiated — they are an automated consequence of crash recovery, retry deduplication, and self-healing metering. The following reason codes belong to this category:

- `RETRY_OVERCOUNTING` — the gateway detects duplicate metering from retried requests and emits a correction to remove the double-count.
- `GATEWAY_CRASH_RECONSTRUCTION` — after crash recovery (Section 7.3), the gateway reconciles billing from pod-reported token totals and emits corrections for any discrepancies.
- `METERING_BUG` — the gateway detects and self-corrects a metering inconsistency (e.g., a token count that disagrees with the pod's reported usage).

These corrections are written to the `EventStore` by the gateway like any other billing event. Each carries a `correction_reason_code` and emits a `billing.correction_issued` audit event recording the gateway replica identity, reason code, original `sequence_number`, and replacement values.

**Category 2 — Operator-initiated manual corrections.** These are issued by human operators through a dedicated admin API endpoint (`POST /v1/admin/billing-corrections`) and require the following controls:

1. **RBAC gating:** The endpoint requires the `platform-admin` role. `tenant-admin` and `user` roles receive `403 FORBIDDEN`.
2. **Structured reason code:** Each correction must include a `correction_reason_code` from this category's codes: `TEST_SESSION_CLEANUP`, `OPERATOR_MANUAL_ADJUSTMENT`, or any deployer-added custom reason code. Free-text `correction_detail` remains available for additional context but is not a substitute for the structured code.
3. **Audit trail:** Every correction emits a `billing.correction_issued` audit event recording the issuing admin's identity, the correction reason code, the original event's `sequence_number`, and the replacement values. This audit event is distinct from the billing event itself and cannot be suppressed.

**Controls that apply to both categories:**

- **Structured reason code (all corrections):** Every `billing_correction` event — whether gateway-emitted or operator-initiated — must carry a `correction_reason_code` from the appropriate category. The reason code unambiguously identifies the correction's origin.
- **Anomaly alerting:** A `BillingCorrectionRateHigh` warning alert fires when correction events (both categories combined) exceed a deployer-configurable percentage (default: 5%) of total billing events in a rolling 24h window (see Section 16.5).

The `correction_reason_code` enum is extensible via the admin API (`POST /v1/admin/billing-correction-reasons`) to allow deployers to add domain-specific reason codes without modifying the platform binary. Deployer-added reason codes are classified as operator-initiated and require the admin endpoint. New reason codes require `platform-admin` and are themselves audit-logged.

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

**Integrity:** Audit tables use **append-only semantics** — the gateway's database role (`lenny_app`) has INSERT-only grants on audit tables (no UPDATE, DELETE). These grants are defined in the schema migration files and enforced through the following layered integrity controls:

1. **Startup grant verification (hard-fail in production):** At startup, the gateway queries `information_schema.role_table_grants` and checks that no UPDATE or DELETE grants exist on audit tables for the `lenny_app` role. In production mode (`LENNY_ENV=production`), any unexpected grant causes the gateway to **refuse to start** (fatal error). In non-production environments, a warning is logged instead. This prevents a compromised or misconfigured database from silently undermining audit integrity.

2. **Periodic background integrity check:** A background goroutine re-verifies audit table grants every 5 minutes (configurable via `audit.grantCheckInterval`). If drift is detected at runtime (e.g., a superuser added UPDATE grants after startup), the gateway emits a critical alert, increments the `audit_grant_drift_total` Prometheus counter, and — if `audit.hardFailOnDrift` is enabled — initiates a graceful shutdown. This closes the window between startup and the next deploy where grant drift would go undetected.

3. **Hash chaining:** Each audit log entry includes a `prev_hash` column containing the SHA-256 hash of the previous entry's `(id, prev_hash, tenant_id, event_type, payload, created_at)` tuple. The first entry in each tenant partition uses a well-known genesis hash. This creates a tamper-evident chain — any retroactive modification or deletion of an entry breaks the chain for all subsequent entries. The periodic background check (item 2) also samples random chain segments and verifies hash continuity; a broken chain triggers the same critical alert path.

4. **SIEM connectivity validation (optional):** For deployments that require compliance-grade audit integrity, audit events **should** be streamed to an external immutable log (SIEM, cloud audit service, or append-only object storage) in addition to Postgres storage. SIEM is not required — Lenny operates fully without it, with the integrity trade-offs documented below. When `audit.siem.endpoint` is configured, the gateway validates SIEM connectivity at startup: a test event is sent and the gateway **refuses to start** until acknowledgement is received. At runtime, a health check monitors SIEM delivery success rate; if the failure rate exceeds the configured threshold (`audit.siem.failureThresholdPercent`, default 5%), the gateway emits a critical alert and the `/healthz` endpoint reports degraded status. This ensures audit integrity even if the database is compromised, and prevents silent SIEM delivery failures from creating gaps in the external audit trail.

**Audit integrity gap — Postgres-only deployments:** When `audit.siem.endpoint` is not configured, audit logs are stored exclusively in Postgres. In this configuration, the hash-chain mechanism (item 3) provides tamper-detection after the fact, but a database superuser can modify or delete rows and reconstruct a valid hash chain — the external SIEM is the only copy a superuser cannot alter. This means:

- **Compliance-grade deployments** (SOC2, FedRAMP, HIPAA) **should** configure `audit.siem.endpoint`. The SIEM provides the independent, immutable audit copy that satisfies regulatory requirements for separation of control from the audited system.
- **Development and single-tenant low-risk deployments** may operate without a SIEM, accepting this integrity limitation. This is appropriate for non-regulated workloads where a database superuser compromise is within the deployer's accepted risk profile.
- **Multi-tenant deployments** without a SIEM are explicitly operating below compliance-grade integrity, regardless of tenant count. This must be a deliberate deployer decision, not an accidental configuration omission.

**Startup behavior when SIEM is unconfigured:**

- In **production mode** (`LENNY_ENV=production`) with **multi-tenant configuration** (`tenancy.mode: multi`): the gateway logs a `WARN`-level message at startup and every 60 seconds while running: `"WARNING: audit.siem.endpoint is not configured — audit logs are stored in Postgres only. A database superuser can bypass INSERT-only grants. This deployment does not meet compliance-grade audit integrity requirements (SOC2 CC7.2, FedRAMP AU-9, HIPAA §164.312(b)). Configure audit.siem.endpoint to resolve."` The `AuditSIEMNotConfigured` alert (Section 16.5) fires immediately. The gateway **does not refuse to start** — SIEM is not mandatory for all deployments — but the integrity gap is made visible and persistent.
- In **production mode** with **single-tenant configuration** (`tenancy.mode: single`): the same `WARN` message is logged once at startup, without the repeating 60-second reminder. The `AuditSIEMNotConfigured` alert fires at `Warning` severity.
- In **non-production environments**: no warning is emitted. SIEM configuration is not expected outside production.

**Compliance-grade requirement summary:**

| Configuration              | Integrity Level                                                   | Compliance-Grade? |
| -------------------------- | ----------------------------------------------------------------- | ----------------- |
| SIEM configured + Postgres | Independent external copy; superuser bypass detectable            | Yes               |
| Postgres only, no SIEM     | Hash-chain tamper-detection only; superuser can reconstruct chain | No                |

**Superuser bypass mitigation:** Database superusers can bypass INSERT-only grants. The hash chaining mechanism (item 3) provides a detection layer — any superuser modification that alters or deletes rows is detectable by chain verification, provided an independent copy exists to compare against. The external SIEM stream (item 4) is that independent copy — a database superuser cannot modify the SIEM. When no SIEM is configured, hash-chain verification remains useful for detecting accidental corruption but provides weaker guarantees against a determined insider threat. Deployers operating under strict compliance requirements **should** configure `audit.siem.endpoint` and **should** additionally restrict superuser access via connection-level controls (e.g., separate superuser credentials stored in a hardware security module, accessed only through a privileged access management workflow).

---

## 12. Storage Architecture

### 12.1 Design Principle

Abstract by **storage role**, not by raw database API. Each store exposes domain operations, not generic CRUD.

### 12.2 Storage Roles

| Role                  | Backend                                   | Purpose                                                                                                                                                                                                                                       |
| --------------------- | ----------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SessionStore`        | Postgres                                  | Sessions, tasks, delegation tree, lineage, retry state                                                                                                                                                                                        |
| `LeaseStore`          | Redis (fallback: Postgres advisory locks) | Distributed session coordination                                                                                                                                                                                                              |
| `TokenStore`          | Postgres (encrypted)                      | Downstream OAuth tokens, refresh tokens                                                                                                                                                                                                       |
| `QuotaStore`          | Redis + Postgres                          | Rate limit counters, budget tracking                                                                                                                                                                                                          |
| `ArtifactStore`       | MinIO (dev: local disk)                   | Uploaded files, checkpoints, workspace snapshots                                                                                                                                                                                              |
| `EventStore`          | Postgres                                  | Audit events, session logs, stream cursors                                                                                                                                                                                                    |
| `CredentialPoolStore` | Postgres (encrypted)                      | Credential pool definitions, lease assignments, health scores, cooldown state, revocation state and deny-list references. Tenant-scoped: all tables carry `tenant_id` with RLS policies identical to other tenant-scoped stores (Section 4.2) |
| `EvictionStateStore`  | Postgres                                  | Minimal session state records written during eviction checkpoint fallback (conversation cursor, last message context)                                                                                                                         |

All Redis-backed roles (`LeaseStore`, `QuotaStore`, routing cache, token cache) **must** use the `t:{tenant_id}:` key prefix convention defined in Section 12.4 to enforce tenant isolation at the key-naming level.

The `ArtifactStore` (MinIO) **must** use `/{tenant_id}/` path prefixes for all object keys, with mandatory prefix validation at the interface level (see Sections 4.5 and 12.5).

### 12.3 Postgres HA Requirements

**Minimum topology:** Lenny requires a PostgreSQL instance (14+) with synchronous replication and automatic failover. This can be provided by a managed service (AWS RDS Multi-AZ, GCP Cloud SQL HA, Azure Database for PostgreSQL) or a self-managed operator (CloudNativePG, Patroni). Managed services are **recommended** as the default for most deployments. Per-tier Postgres instance sizing is in Section 17.8.

| Deployment             | Recommendation                                             |
| ---------------------- | ---------------------------------------------------------- |
| Cloud (production)     | Managed PostgreSQL with multi-AZ HA (e.g., RDS, Cloud SQL) |
| On-prem / self-managed | CloudNativePG operator or Patroni on Kubernetes            |
| Local dev              | Single PostgreSQL container (via docker-compose)           |

**Connection pooling:** A connection pooler is **required** in front of Postgres. Each gateway replica maintains a connection pool; without pooling, HPA-scaled replicas exhaust Postgres connection limits. **Cloud-managed deployments** should use the provider's built-in connection proxy (AWS RDS Proxy, GCP Cloud SQL Auth Proxy, Azure PgBouncer integration) instead of deploying a self-managed PgBouncer — see Section 17.9 for deployment profile guidance. **Self-managed deployments** deploy PgBouncer (or pgcat) as described below. Regardless of implementation, the pooler must operate in **transaction-mode** pooling (not session-mode) to ensure compatibility with RLS enforcement via `SET LOCAL app.current_tenant` (see Section 4.2) — `SET LOCAL` is transaction-scoped (cleared on `COMMIT`/`ROLLBACK`), so transaction-mode guarantees the setting does not leak across tenants sharing the same pooled connection. The pooler's `connect_query` (or equivalent initialization hook) must set a sentinel value (`SET app.current_tenant = '__unset__'`) on every fresh connection checkout so that any query reaching RLS without a prior `SET LOCAL` is rejected.

- **Deployment topology:** PgBouncer runs as a separate Deployment (minimum 2 replicas) fronted by a ClusterIP Service — not as a sidecar on each gateway pod. This centralizes pool management and avoids per-pod connection sprawl toward Postgres.
- **Pool mode:** Transaction mode (`pool_mode = transaction`). This is required for RLS enforcement because `SET LOCAL app.current_tenant` is transaction-scoped (cleared on `COMMIT`/`ROLLBACK`); session mode would leak tenant context across unrelated requests sharing a backend connection.
- **Sizing guidance:** Set `default_pool_size` to approximately `max_connections / number_of_PgBouncer_replicas`, leaving headroom for superuser and replication connections. Configure `reserve_pool_size` (e.g., 5–10 per pool) for burst headroom, with `reserve_pool_timeout` set to a short duration (e.g., 3s) so reserved connections are only used under genuine load spikes.
- **HA:** PgBouncer is stateless — multiple replicas behind the Kubernetes ClusterIP Service provide transparent failover. If one replica is terminated or fails a health check, the Service routes traffic to surviving replicas with no client-side changes required.
- **PodDisruptionBudget:** A PDB **must** be configured for the PgBouncer Deployment with `minAvailable: 1` (or `maxUnavailable: 1` when running 3+ replicas). This prevents a rolling update or node drain from terminating all PgBouncer replicas simultaneously. Without the PDB, a bad rollout can take every replica through `CrashLoopBackOff` at once, making Postgres unreachable for all gateway traffic.
- **Readiness probe:** Each PgBouncer pod **must** define a readiness probe that verifies backend Postgres connectivity — not just PgBouncer process liveness. The probe should execute a lightweight query (e.g., `SELECT 1`) through PgBouncer's admin or application port to confirm that at least one backend connection can be established. A pod that starts successfully but cannot reach Postgres must be removed from the Service endpoints so traffic is routed only to replicas with verified backend connectivity. Recommended settings: `periodSeconds: 5`, `failureThreshold: 2`, `timeoutSeconds: 3`.
- **Full PgBouncer unavailability:** If all PgBouncer replicas are simultaneously unreachable (despite the PDB), gateway replicas lose access to Postgres. In this state the system behaves identically to a Postgres outage: Redis remains the primary coordination mechanism and all Redis-backed roles continue normally; new sessions and Postgres-dependent writes (including billing events — Section 11.2.1) are rejected with `503 Service Unavailable`. If Redis is also unavailable during this window, dual-store unavailability rules apply (Section 10.1). Recovery is automatic once at least one PgBouncer replica passes its readiness probe and rejoins the Service.
- **Monitoring:** Deploy `pgbouncer_exporter` as a sidecar on each PgBouncer pod to expose Prometheus metrics. Key metrics to alert on: pool utilization (`cl_active` / `sv_active` vs. pool size), client wait time (`cl_waiting_time`), and average query duration (`avg_query_time`).

**Cloud-managed pooler limitation — `__unset__` sentinel:** Most cloud-managed connection proxies (AWS RDS Proxy, GCP Cloud SQL Auth Proxy, Azure PgBouncer integration) do **not** support a `connect_query` or equivalent initialization hook. This means the `__unset__` sentinel — which is the second layer of RLS defense ensuring that any query reaching Postgres without a prior `SET LOCAL app.current_tenant` is rejected — cannot be set on connection checkout. Without the sentinel, a bug in the application-layer tenant context logic could silently allow cross-tenant reads, since only the application-layer `WHERE` clauses would remain as the sole defense.

**Alternative RLS defense for cloud-managed poolers:** Deployments using a cloud-managed connection proxy that lacks `connect_query` support **must** implement the following alternative defense:

1. **Per-transaction tenant validation trigger:** Create a Postgres `BEFORE` trigger (or RLS policy with a validation function) on every tenant-scoped table that verifies `current_setting('app.current_tenant', true)` is set and is not `NULL`, empty, or `'__unset__'` before allowing any `SELECT`, `INSERT`, `UPDATE`, or `DELETE`. If the setting is missing or invalid, the trigger raises an exception (`ERRCODE 'P0001'`), blocking the query. This provides equivalent protection to the `connect_query` sentinel — any connection that reaches a tenant-scoped table without a prior `SET LOCAL app.current_tenant` in the current transaction will be rejected at the database level.
2. **Migration-enforced:** The trigger/function **must** be created by the Lenny schema migration (Section 12.3), not left to manual operator action. The migration detects whether `connect_query` sentinel support is available (via the `postgres.connectionPooler` Helm value) and creates the trigger when `connectionPooler = external`.
3. **Performance note:** The per-transaction trigger adds negligible overhead (a single `current_setting()` call per statement), but operators should monitor `pg_stat_user_functions` to verify trigger execution counts align with query volume.

See Section 17.8 for per-tier Postgres and PgBouncer sizing recommendations.

**Write IOPS estimation:** Multiple gateway components generate Postgres writes concurrently: session state updates, quota checkpoint flushes, billing event inserts, and audit log inserts. The following table estimates sustained write IOPS at each capacity tier (see Section 16.5 for tier definitions, Section 17.8 for instance sizing):

| Write Source                   | Tier 1    | Tier 2     | Tier 3       | Notes                                                  |
| ------------------------------ | --------- | ---------- | ------------ | ------------------------------------------------------ |
| Session state updates          | ~5/s      | ~50/s      | ~300/s       | Per-session lifecycle transitions, heartbeats          |
| Quota checkpoint flushes       | ~2/s      | ~20/s      | ~100/s       | Redis→Postgres periodic sync (Section 10.1)            |
| Billing event inserts          | ~10/s     | ~100/s     | ~600/s       | Per-token and per-session events (Section 11.2.1)      |
| Audit log inserts              | ~5/s      | ~50/s      | ~300/s       | Per-request and lifecycle audit entries (Section 11.7) |
| **Total estimated write IOPS** | **~22/s** | **~220/s** | **~1,300/s** | Sustained; bursts may reach 2–3× during session storms |

At Tier 3, total sustained write IOPS (~1,300/s) with burst headroom (~3,900/s) approaches the practical write ceiling of a single 8-vCPU Postgres primary with synchronous replication. Operators should monitor `pg_stat_bgwriter` (buffers written), `pg_stat_wal` (WAL write rate), and replication lag to detect write pressure before it impacts latency.

**Batching guidance for write-heavy paths:** Billing event inserts and audit log inserts are the two highest-volume write sources and are append-only (no UPDATE or DELETE). Both **should** be batched to reduce per-statement overhead and WAL amplification:

- **Billing events:** The gateway **should** buffer billing events in memory and flush them as multi-row `INSERT` statements at a configurable interval (`billingFlushIntervalMs`, default: 500ms) or when the buffer reaches a size threshold (`billingFlushBatchSize`, default: 50 events). On graceful shutdown, the gateway flushes any remaining buffered events before exiting. If the buffer exceeds `billingFlushMaxPending` (default: 500), the gateway flushes immediately and emits the `billing_flush_pressure` metric.
- **Audit logs:** Audit log inserts are batched using `auditFlushIntervalMs` (default: **250ms**; `auditFlushBatchSize`, default: 100 entries). The default flush interval is intentionally shorter than the billing interval because audit events represent a compliance obligation.

  **Durability trade-off and bounded loss window:** The audit flush buffer is in-memory and has no write-ahead log. A gateway replica crash during a flush interval can lose buffered audit events that were not yet written to Postgres. The bounded loss estimate at each tier is:

  | Tier   | Audit write rate | Max events lost per crash (250ms window) |
  | ------ | ---------------- | ---------------------------------------- |
  | Tier 1 | ~5/s             | ~2 events                                |
  | Tier 2 | ~50/s            | ~13 events                               |
  | Tier 3 | ~300/s           | ~75 events                               |

  **When SIEM is configured:** This Postgres-side batching is acceptable because the SIEM receives the same events in real-time (synchronous delivery path, not buffered). The SIEM is the durable primary copy; the Postgres batch is the secondary. A crash only affects the Postgres copy — the SIEM copy is complete. On gateway restart, the startup chain-continuity check (described below) will detect and log the gap so operators can replay from the SIEM if needed.

  **When no SIEM is configured (Postgres-only deployments):** In `LENNY_ENV=production` mode, audit inserts are made **synchronous** (no batching; `auditFlushIntervalMs` is ignored) to eliminate the crash data-loss window. The synchronous write path adds latency to the audit-event path but does not block request processing — audit writes use a separate goroutine with a dedicated connection pool (`audit.syncWritePoolSize`, default: 4 connections). In non-production environments, batching applies regardless of SIEM configuration. This rule is enforced at startup: if `LENNY_ENV=production` and `audit.siem.endpoint` is not set, the gateway logs `"Audit writes will be synchronous in production mode without SIEM (auditFlushIntervalMs ignored)"` and operates in synchronous audit mode.

  **Startup chain-continuity check:** On gateway startup, after Postgres becomes reachable, the gateway verifies that the audit hash chain is unbroken for each active tenant by querying the most recent N entries (configurable via `audit.startupChainCheckEntries`, default: 1000) and re-verifying the `prev_hash` chain. If a gap is detected (broken chain), the gateway emits a `WARN`-level log message identifying the tenant, the sequence numbers of the gap boundaries, and the approximate timestamp range of the missing entries: `"Audit chain gap detected for tenant <id>: gap between sequence <n> and <m> (~<timestamp range>). If SIEM is configured, replay missing events from SIEM to restore chain continuity."` The `AuditChainGap` alert (Section 16.5) fires. The gateway does **not** refuse to start — a gap is a compliance signal, not a fatal error. Operators are responsible for replaying from the SIEM or documenting the gap in their compliance records.

- **Separate Postgres for write-heavy paths (Tier 3):** At Tier 3, operators **may** deploy a dedicated Postgres instance for billing and audit writes to isolate write amplification from the primary instance handling session state and quota checkpoints. The gateway supports separate connection strings for the billing/audit write path (`LENNY_PG_BILLING_AUDIT_DSN`). When configured, billing and audit inserts are routed to this instance while all other writes continue to the primary. This is optional — a single well-provisioned primary handles Tier 3 sustained load, but the separate instance provides headroom for burst absorption and reduces replication lag on the primary.

**Read replicas:** The gateway should use separate connection strings for read and write traffic. Read-heavy queries (session status, task tree, audit reads, usage reports) should be routed to read replicas. Most managed services provide a reader endpoint for this purpose. Write traffic goes to the primary only.

**RPO/RTO targets:**

- RPO: 0 (synchronous replication — no committed transaction lost)
- RTO: < 30s (automatic failover)

**Behavior during Postgres failover:** During the Postgres failover window (up to 30s), Redis remains the primary coordination mechanism and all Redis-backed roles continue normally. If Redis is also unavailable during this window, dual-store unavailability rules apply (see Section 10.1, "Dual-store unavailability"). Existing sessions with cached coordination state continue; new sessions and writes requiring Postgres (including billing events — see Section 11.2.1) are rejected or queued as specified below.

**Write durability categories during failover:**

| Write category                               | Durability during failover                                                                                  | Recovery mechanism                                                                                                                                      |
| -------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Committed session state transitions          | **Durable** — synchronous replication guarantees no committed transaction is lost (RPO = 0)                 | Automatic; new primary has all committed writes                                                                                                         |
| In-flight (uncommitted) session state writes | **At-risk** — transactions not yet committed at failover time are lost by definition                        | Gateway retries the state transition after reconnecting to the new primary; `coordination_generation` (Section 10.1) prevents stale or duplicate writes |
| Billing events in write-ahead buffer         | **At-risk** — in-memory buffer on gateway replica is lost if the replica crashes during the failover window | Reconstructed from pod-reported cumulative token usage during session recovery (Section 7.3); see maximum billing gap below                             |
| Quota counters (Redis-primary)               | **Unaffected** — Redis continues independently of Postgres                                                  | Postgres checkpoint (every `quotaSyncIntervalSeconds`, default 30s) provides durable fallback                                                           |
| Coordination leases (Redis-primary)          | **Unaffected** — Redis continues independently of Postgres                                                  | Postgres advisory lock fallback if Redis also fails                                                                                                     |
| Audit events in flush buffer                 | **At-risk** — in-memory buffer lost on gateway crash                                                        | SIEM copy is complete if configured; Postgres-only deployments use synchronous writes in production mode (Section 12.3)                                 |

**Maximum billing gap:** The worst-case billing gap occurs when a gateway replica crashes simultaneously with a Postgres failover while its billing write-ahead buffer is non-empty and the pods it coordinated also fail (preventing reconstruction from pod-reported usage). The bounded gap per replica is: `billingFlushIntervalMs` (default 500ms) worth of events, capped at `billingFlushBatchSize` (default 50 events). At Tier 3 with ~600 billing inserts/s across all replicas and 10 gateway replicas, a single replica handles ~60 events/s; a 500ms crash window loses at most ~30 events. If the coordinated pods survive (the common case), pod-reported cumulative totals allow full reconstruction with zero billing loss. The simultaneous gateway-crash + pod-loss + Postgres-failover scenario is the only path to permanent billing gaps, and the per-replica loss is bounded at ~30 events (~50 at `billingFlushBatchSize` cap).

**Encryption at rest:** Postgres storage must be encrypted at rest. Managed services (RDS, Cloud SQL, Azure Database) provide this by default via volume-level encryption — verify it is enabled. Self-managed deployments must use LUKS-encrypted volumes or dm-crypt for the Postgres data directory. All WAL archives and base backups must also be encrypted: managed services encrypt backups automatically when storage encryption is enabled; self-managed deployments must use server-side encryption on the backup target (e.g., SSE-S3/SSE-KMS for MinIO/S3 backup destinations) or client-side encryption (e.g., `gpg` or age) before upload.

**Backups:** Daily base backups + continuous WAL archival. Restore tested monthly (see Section 17.3 for the `lenny-restore-test` CronJob procedure). All backup and WAL archive storage must be encrypted at rest as specified above.

### 12.4 Redis HA and Failure Modes

**Minimum topology:** For self-managed deployments: Redis Sentinel (3 sentinels, 1 primary + 1 replica). Redis Cluster if sharding is needed at Tier 3. For cloud-managed deployments: use the provider's managed cache service (AWS ElastiCache, GCP Memorystore, Azure Cache for Redis) which provides equivalent HA, replication, and scaling — see Section 17.9 for deployment profile guidance. See Section 17.8 for per-tier Redis topology recommendations.

**Tenant key isolation:** All Redis keys **must** use the prefix `t:{tenant_id}:` followed by the storage role and logical key — e.g., `t:acme-corp:lease:session:abc123`, `t:acme-corp:quota:tokens:user42:2026-04`. This convention is enforced in the Redis wrapper layer; no raw Redis command may be issued without the tenant prefix. An integration test (`TestRedisTenantKeyIsolation`) must verify that operations scoped to one tenant cannot read or mutate keys belonging to another tenant.

**Security:** Redis AUTH (ACLs) and TLS are **required**. Cached access tokens are encrypted before storage in Redis (not stored as plaintext). Tokens are encrypted using AES-256-GCM with a key derived from the Token Service's envelope encryption key; each cached token is stored as `{nonce || ciphertext || tag}`, and the encryption key is rotated alongside the envelope key (Section 10.5).

**Data-at-rest posture:** Redis data is treated as **ephemeral** — every Redis-backed role (leases, quota counters, routing cache, token cache) has a durable fallback or reconstruction path (see failure behavior table below). Redis is not a system of record, and total data loss is recoverable. Nonetheless, sensitive cached values (access tokens, credential lease references) must be encrypted at the application layer before writing to Redis, as specified above. Volume-level encryption for Redis persistence files (RDB/AOF) is recommended for defense-in-depth but is not a substitute for app-layer encryption of sensitive fields.

**Failure behavior per use case:**

| Use Case                   | On Redis Unavailability                                                                                                      |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Rate limit counters        | **Fail open with bounded window** — allow requests for up to `rateLimitFailOpenMaxSeconds` (default: 60s), then fail closed  |
| Distributed session leases | **Fall back** to Postgres advisory locks (higher latency)                                                                    |
| Routing cache              | **Fall back** to Postgres lookup                                                                                             |
| Cached access tokens       | **Re-fetch** from TokenStore (Postgres)                                                                                      |
| Quota counters             | **Fail open with per-replica budget ceiling** — enforce conservative per-tenant limits locally, then fail closed (see below) |

**Quota counter reconciliation after fail-open:** When Redis recovers, the gateway reconciles quota counters by querying Postgres for actual usage (session token counts, active sessions) and resetting Redis counters to match. Maximum drift during fail-open is bounded by the window duration (default 60s) multiplied by request rate — at 100 req/s per tenant, worst-case drift is ~6,000 requests. Worst-case drift scales with tier — see Section 17.8 for per-tier estimates. Reconciliation runs automatically when Redis becomes available and completes within seconds. During reconciliation, the gateway falls back to Postgres-backed counters (slower but accurate).

**Delegation budget counter reconciliation on Redis recovery:** On Redis recovery (restart or reconnection), the gateway additionally re-validates all active delegation tree budget counters before resuming delegation operations. The reconciliation procedure follows the two-source reconstruction described in Section 11.2 (Crash Recovery for Delegation Budget Counters): Postgres checkpoint + live pod enumeration, with irrecoverable trees moved to `awaiting_client_action`. New `delegate_task` requests for any tree are rejected with a retryable error during the reconciliation window (typically < 5 seconds). The `lenny_delegation_budget_reconstruction_total` metric (labeled by `outcome`) is emitted for each tree processed, enabling operators to monitor reconstruction volume and detect irrecoverable trees. Operators should add this to the Redis recovery runbook (Section 17.7).

**Bounded fail-open for rate limiting:** When Redis becomes unavailable, the gateway starts a fail-open timer per replica. During the fail-open window, requests are allowed, a `rate_limit_degraded` metric is incremented, and an alert fires. After the window expires, if Redis is still unavailable, rate limiting fails **closed** — new requests are rejected with 429 until Redis recovers. **Emergency hard limit:** Each gateway replica maintains an in-memory per-user request counter as a coarse emergency backstop. This counter is not shared across replicas (so the effective limit is `N * per_replica_limit`) but prevents a single user from sending unlimited requests through one replica during the fail-open window. The `rateLimitFailOpenMaxSeconds` is configurable per deployment (default 60s). Higher tiers should use shorter fail-open windows to limit burst damage (Section 17.8).

**Per-tenant fail-open budget enforcement:** In addition to per-user rate limiting, each gateway replica maintains in-memory per-tenant token and request counters that are active during Redis unavailability. The per-replica ceiling for each tenant is `per_replica_limit = tenant_limit / replica_count`, where `replica_count` is sourced from the Kubernetes Endpoints object for the gateway Service (polled via the API server, not from Redis), with a maximum staleness of 30 seconds. If the Endpoints object is also unavailable, `replica_count` falls back to `1` — making the per-replica ceiling equal to the full tenant limit. This conservative fallback prevents unbounded overshoot when both Redis and peer discovery are simultaneously unavailable. Once a tenant's local counter reaches `per_replica_limit`, the replica rejects further requests for that tenant with 429 until Redis recovers. This ensures that even in the worst case (all replicas saturated with a current replica count), total cluster-wide usage cannot exceed the tenant's configured budget. **Cumulative fail-open timer (security control):** Each replica tracks total cumulative time spent in fail-open mode via a true sliding window counter stored in-memory (not in Redis). The counter accumulates time across all Redis outages within the window — five separate 59-second outages each add 59 seconds to the running total, not just the most recent. If cumulative fail-open time exceeds `quotaFailOpenCumulativeMaxSeconds` (default: 300s) within any rolling 1-hour window (not calendar-aligned), the replica transitions to **fail-closed for quota enforcement** — all new sessions and token-consuming requests are blocked until Redis recovers and counters are reconciled. `quotaFailOpenCumulativeMaxSeconds` is a **financial security control**: its default (300s) should be reviewed per deployment's financial risk exposure and set to the maximum acceptable quota overshoot window. On replica restart, the cumulative timer resets to zero (conservative choice — the outage that caused the restart is treated as resolved). When a replica enters fail-open mode, it emits a `quota_failopen_started` audit event containing `tenant_id`, `replica_id`, and `timestamp`, enabling billing consumers to detect and attribute overshoot windows. The gateway also emits `quota_failopen_cumulative_seconds` (gauge) and fires alert `QuotaFailOpenCumulativeThreshold` when the cumulative timer exceeds 80% of the configured maximum.

**Scalability ceiling of single-instance Redis:** At Tiers 1 and 2, a single Redis Sentinel topology handles all Redis-backed concerns: session leases, quota counters, routing cache, token cache, and event pub/sub. This is simple to operate but creates a shared scalability ceiling — all concerns compete for the same CPU, memory, and network bandwidth on one primary node. Sentinel provides HA (automatic failover) but does not provide horizontal throughput scaling.

**When Sentinel becomes insufficient:** Monitor the following signals to determine when the single Sentinel topology is approaching its ceiling:

- **CPU saturation:** Redis primary sustained above 70% CPU (single-threaded; 70% of one core leaves limited headroom for latency-sensitive operations like lease renewals).
- **Memory pressure:** Used memory exceeds 75% of `maxmemory`, causing eviction risk for cached tokens and routing entries.
- **Operation latency:** P99 latency for `LeaseStore` or `QuotaStore` operations exceeds 5ms (baseline should be sub-1ms). Lease renewals are latency-critical — elevated latency risks false lease expirations.
- **Pub/sub fan-out cost:** At high session counts, pub/sub message delivery competes with key-value operations. If `redis_pubsub_channels` exceeds 5,000 and `redis_connected_subscribers` exceeds 10,000, pub/sub overhead becomes measurable.
- **Operations rate:** Approaching 80% of the budget operations estimate for the current tier (Section 17.8).

**Logical separation of Redis concerns:** When the signals above indicate the single topology is insufficient (typically at upper Tier 2 or Tier 3 scale), separate Redis into independent instances by concern:

| Redis Instance            | Backed Concerns                                     | Scaling Rationale                                                                                      |
| ------------------------- | --------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| **Coordination**          | `LeaseStore` (session leases), routing cache        | Latency-critical, low throughput, small key space. Sentinel topology is sufficient even at Tier 3.     |
| **Quota / Rate Limiting** | `QuotaStore` (rate limit counters, budget tracking) | High write throughput (per-request increments). Benefits most from Redis Cluster sharding at scale.    |
| **Cache / Pub-Sub**       | Token cache, event pub/sub                          | Tolerates higher latency. Pub/sub fan-out is CPU-intensive and should not compete with lease renewals. |

At Tiers 1 and 2, all concerns run on a single Sentinel instance. The separation is a deployment-time configuration change (separate connection strings per store role) — no code changes are required because each store role already has its own interface (Section 12.6). See Section 17.8 for per-tier Redis topology recommendations including when to split.

**In-memory quota budgets with Postgres reconciliation:** For high-value limits (e.g., per-tenant token budgets where overshoot has direct cost impact), each gateway replica can maintain an in-memory budget allocation drawn from Postgres rather than relying solely on Redis counters. The replica requests a budget slice from Postgres on startup (e.g., 1/N of the tenant's remaining budget, where N is the replica count), decrements locally per request, and reconciles with Postgres periodically (default: every 30s) or when the local slice is 80% consumed. This approach tolerates full Redis unavailability for quota enforcement with bounded overshoot (at most one slice per replica). It is **not the default** — deployers enable it via `quotaEnforcementMode: in_memory_reconciled` when Redis-based quota drift during outages is unacceptable.

### 12.5 Artifact Store

**Backend:** Any S3-compatible object store. Cloud-managed deployments should use the provider's native object storage (AWS S3, GCP Cloud Storage, Azure Blob Storage) — see Section 17.9 for deployment profile guidance. Self-managed deployments use MinIO. For local development, use local disk with the same interface.

**HA topology requirements:**

- **Production topology:** MinIO with erasure coding (minimum 4 nodes, recommended 4+ nodes across availability zones) for data durability. See Section 17.8 for per-tier MinIO sizing.
- **Versioning:** Enable bucket versioning for checkpoint objects to prevent accidental overwrites
- **Replication:** For multi-zone deployments, configure MinIO site-to-site replication for near-zero RPO on artifact data

**MinIO unavailability during checkpoint:** If MinIO is unreachable during a checkpoint upload, the adapter retries with exponential backoff as described in Section 4.4. Deployers should monitor the `CheckpointStorageUnavailable` critical alert and the `lenny_checkpoint_storage_failure_total` metric. Prolonged MinIO unavailability degrades session durability — any pod eviction during an outage that exhausts all MinIO retries falls back to the Postgres minimal state record (Section 4.4); sessions resume with conversation context but workspace state is lost.

**Risk: simultaneous MinIO outage and node eviction.** A MinIO rolling upgrade or network partition coinciding with a node drain causes simultaneous preStop hooks across all pods on the drained node, all competing for an unavailable MinIO, all falling back to minimal state. This is a known cascading failure scenario. Mitigations: (1) the `CheckpointStale` alert (Section 16.5) fires before eviction if the freshness SLO is already missed, giving operators an early signal; (2) deployers should stagger node drains — drain one node at a time (use `kubectl drain --max-unavailable 1` or cluster-autoscaler drain rate limits) to limit the number of simultaneous eviction checkpoints; (3) schedule MinIO maintenance windows outside of cluster upgrade windows to avoid the combination. The Postgres minimal state fallback is the last-resort safety net, not a substitute for operational discipline around MinIO availability.

**Do not use Postgres for blob storage.** Workspace checkpoints (up to 500MB) cause TOAST overhead, vacuum pressure, and degrade transactional workload performance.

**Tenant isolation:** All object keys use the path format `/{tenant_id}/{object_type}/{session_id}/{filename}`. The `ArtifactStore` implementation validates the `tenant_id` prefix on every operation — no S3 call is issued unless the caller's authenticated `tenant_id` matches the path prefix. This validation lives at the interface boundary, not in individual callers. `DeleteByTenant(tenant_id)` performs a prefix-scoped bulk delete on `/{tenant_id}/*`. The GC job (below) inherits the same prefix scoping: it only deletes artifacts under the tenant prefix associated with each expired record.

**Encryption at rest:** All stored objects (checkpoints, workspace snapshots, session transcripts) contain sensitive data including conversation history and workspace file contents. MinIO server-side encryption (SSE-S3 or SSE-KMS) must be enabled for production deployments. For cloud deployments using managed object storage (S3, GCS, Azure Blob), encryption at rest is typically enabled by default. For self-hosted MinIO, enable SSE with a KMS backend (HashiCorp Vault, AWS KMS) or at minimum SSE-S3 with MinIO's internal key management. **T4 tenant encryption:** For tenants with `workspaceTier: T4` (Section 12.9), workspace snapshots and session transcripts in object storage **must** use SSE-KMS with a tenant-specific KMS key — not the default shared encryption key. This enables per-tenant key rotation and cryptographic erasure (revoking the tenant's KMS key renders all their stored artifacts unreadable). The `ArtifactStore` implementation selects the encryption key based on the tenant's `workspaceTier`: T3 uses the deployment-wide SSE key; T4 uses a KMS key scoped to `tenant:{tenant_id}`. Key provisioning is the deployer's responsibility (e.g., AWS KMS key per tenant, HashiCorp Vault transit key per tenant). The preflight check (Section 17.6) validates that MinIO SSE is enabled but does not validate per-tenant KMS key existence — this is validated at runtime when the first T4 artifact is written, returning `CLASSIFICATION_CONTROL_VIOLATION` if the tenant-scoped KMS key is unavailable.

**Checkpoint retention policy:**

- Keep only the latest 2 checkpoints per active session. In concurrent-workspace mode (Section 5.2), checkpoints are per-slot — the "latest 2" limit applies independently to each slot, meaning a pod with `maxConcurrent: 8` may retain up to 16 checkpoint objects (8 slots × 2). The GC job and retention policy operate on `(session_id, slot_id)` pairs, not on sessions alone.
- Delete all checkpoints when session terminates and resume window expires
- Background GC job runs every 15 minutes to clean expired artifacts. The job is owned by the gateway — it runs as a leader-elected goroutine inside the gateway process (not a separate CronJob). Only one gateway instance runs GC at a time via the existing leader-election lease. Higher tiers may require more frequent cycles (Section 17.8).
- The GC job is idempotent: it queries Postgres for artifacts past their TTL, deletes them from MinIO, then marks the rows as `deleted` in Postgres. Re-running on the same set is safe because MinIO delete-on-absent is a no-op and the Postgres update is conditional on current state.
- **No reference counting required in v1:** Each MinIO artifact object is owned by exactly one session. Session derivation (Section 7.1) always copies the parent's workspace snapshot into a new session-scoped MinIO object — no object is shared across sessions. TTL-based deletion is therefore safe: deleting a parent's expired artifact cannot affect any derived session's workspace. Reference counting will be required only if a future content-addressed deduplication optimization is introduced (see Section 12.8).
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

**Legal hold and checkpoint rotation.** Legal holds prevent TTL-based deletion but do **not** prevent checkpoint retention rotation. For sessions under legal hold, the "latest 2 checkpoints" retention policy (Section 12.5) still applies — newer checkpoints replace older ones. This bounds storage growth for long-held sessions. The rationale: a legal hold preserves the session's _current recoverable state_, not its full checkpoint history. If full checkpoint history is required for forensic purposes, the deployer should configure a separate archival pipeline (e.g., MinIO bucket replication to a cold-storage bucket with lifecycle rules). Metric: `lenny_checkpoint_storage_bytes_total` (gauge, labeled by `tenant_id` and `pool`) tracks total checkpoint storage per tenant. Alert rule `CheckpointStorageHigh` (Section 16.5) fires when per-tenant checkpoint storage exceeds a deployer-configurable threshold (default: 80% of the tenant's storage quota).

**Data erasure (GDPR).** Each store interface includes `DeleteByUser(user_id)` and `DeleteByTenant(tenant_id)` methods. Erasure is implemented as a background job that runs to completion and produces an erasure receipt stored in the audit trail.

**User-level erasure invocation.** User-level erasure is triggered via the Admin API: `POST /v1/admin/users/{user_id}/erase` initiates the background erasure job and returns a job ID. The job status is queryable via `GET /v1/admin/erasure-jobs/{job_id}`, which returns the current phase, completion percentage, time elapsed, and any errors. These endpoints are listed in Section 15.1.

**Erasure SLA enforcement.** Erasure jobs must complete within the tier-specific deadlines defined in Section 12.9: T3 data within 72 hours, T4 data within 1 hour. The erasure job records its start time and the applicable deadline. If the job has not completed by the deadline, the `ErasureJobOverdue` alert (Section 16.5) fires. Metrics: `lenny_erasure_job_duration_seconds` (histogram) tracks job completion time; `lenny_erasure_jobs_active` (gauge) tracks in-progress jobs. These metrics enable operators to monitor erasure throughput and detect stalled jobs before they breach the SLA.

**Storage backends in erasure scope:**

| Store                  | Backend                 | Data Erased                                                                              |
| ---------------------- | ----------------------- | ---------------------------------------------------------------------------------------- |
| `SessionStore`         | Postgres                | Sessions, task trees, delegation lineage, retry state                                    |
| `EventStore` (audit)   | Postgres                | Audit events for the user's sessions                                                     |
| `EventStore` (billing) | Postgres                | See tenant-controlled billing erasure below                                              |
| `ArtifactStore`        | MinIO                   | Workspace snapshots, checkpoints, uploaded files, session transcripts                    |
| `TokenStore`           | Postgres                | OAuth tokens, refresh tokens                                                             |
| `CredentialPoolStore`  | Postgres                | Credential lease assignments referencing the user                                        |
| `EvictionStateStore`   | Postgres                | Minimal eviction state records containing `last_message_context` for the user's sessions |
| `MemoryStore`          | Postgres (or pluggable) | All memories written by or scoped to the user                                            |
| `QuotaStore`           | Redis + Postgres        | Per-user rate-limit counters and budget tracking                                         |
| `LeaseStore`           | Redis                   | Active session coordination leases for the user's sessions                               |
| `SemanticCache`        | Redis (or pluggable)    | Cached query/response pairs scoped to the user                                           |
| Redis caches           | Redis                   | Cached access tokens, routing entries for the user's sessions                            |

**Tenant-controlled billing erasure.** Billing events use append-only semantics with monotonic sequence numbers (Section 11.2.1). Deleting billing rows would break gap detection and audit integrity. By default, the erasure job **pseudonymizes** billing events: `user_id` is replaced with a one-way hash (`SHA-256(user_id || erasure_salt)`) and any free-text fields that could contain PII are cleared. The pseudonymized events retain their `sequence_number`, `tenant_id`, and cost dimensions so that financial reconciliation remains intact.

**`erasure_salt` key management.** The `erasure_salt` is a **per-tenant** cryptographic secret used exclusively for billing pseudonymization. Storage and lifecycle:

- **Storage:** The salt is stored in the tenant configuration record in Postgres, encrypted at rest using envelope encryption via KMS — the same pattern used for OAuth refresh tokens (Section 4.3). The salt is never stored in plaintext or alongside the pseudonymized billing values.
- **Scope:** Per-tenant. Each tenant has its own independently generated salt (256-bit, generated via `crypto/rand`). A per-tenant salt ensures that pseudonymized `user_id` values from one tenant cannot be correlated with values from another tenant, even by an adversary with access to both tenants' billing data.
- **Access control:** Only the erasure job reads the salt, and only during pseudonymization. The KMS decrypt permission required to unwrap the salt is scoped to the erasure job's service account — gateway replicas and runtime pods cannot decrypt it.
- **Rotation:** Salt rotation is supported but optional. On rotation, the old salt is retained in a `previous_erasure_salts` array (also KMS-encrypted) so that previously pseudonymized records remain internally consistent. New pseudonymization operations use the current salt. Re-hashing historical records with the new salt is not required — the old salt's retention preserves referential integrity across the billing event timeline. If a tenant requires old-salt destruction (to strengthen pseudonymization of historical records), a one-time re-hash migration job re-pseudonymizes all billing events under the new salt and then deletes the old salt. The erasure receipt records which salt version was used.
- **Compromise response:** If a salt is compromised, the tenant admin rotates the salt via the admin API (`POST /v1/admin/tenants/{id}/rotate-erasure-salt`). The old salt is moved to `previous_erasure_salts` and a security audit event is emitted.

**GDPR compliance note on pseudonymization.** Under GDPR Recital 26, pseudonymized data remains personal data — it is a risk-reduction technique, not anonymization. The `erasure_salt` enables re-identification of pseudonymized billing records if the salt is accessible. The KMS-backed storage and access-control scoping described above reduce this risk but do not eliminate it. Deployers must treat pseudonymized billing events as T3 — Confidential personal data subject to the same access controls, retention policies, and data residency constraints as non-pseudonymized billing records. Tenants with `billingErasurePolicy: exempt` retain original `user_id` values under GDPR Article 17(3)(b) and must document this retention in their Records of Processing Activities.

Tenants that require user-level billing attribution (chargebacks, department allocation, per-user invoicing) may configure billing events as **exempt from user-level erasure** via the tenant configuration (`billingErasurePolicy: exempt`). When exempt, billing events are retained with the original `user_id` intact. This is legally permissible under GDPR Article 17(3)(b) — processing necessary for compliance with a legal obligation or for financial record-keeping. Tenants that enable this exemption accept compliance responsibility for the retained billing data. The erasure receipt records which policy was applied and whether billing events were pseudonymized or exempted.

**Tenant deletion lifecycle.** Tenant deletion is a multi-phase process managed by a background controller. Each tenant carries a `TenantState` enum (`active`, `disabling`, `deleting`, `deleted`) persisted in Postgres and exposed via the admin API.

| Phase                 | `TenantState` | Actions                                                                                                                                                                                                                                                                                                                                                          |
| --------------------- | ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1. Soft-disable       | `disabling`   | Reject new session creation, API key issuance, and user sign-ups for the tenant. Existing sessions continue until Phase 2.                                                                                                                                                                                                                                       |
| 2. Terminate sessions | `disabling`   | Send graceful-shutdown signals to all active sessions (Section 7). Wait for in-flight tasks to reach a terminal state or hit a configurable timeout (default: 5 minutes), then force-terminate remaining pods.                                                                                                                                                   |
| 3. Revoke credentials | `deleting`    | Revoke all OAuth tokens and refresh tokens via `TokenStore.DeleteByTenant`. Invalidate all credential pool leases via `CredentialPoolStore.RevokeByTenant`. Flush tenant-scoped entries from the Redis access-token cache.                                                                                                                                       |
| 4. Delete data        | `deleting`    | Execute `DeleteByTenant` on every store in the erasure scope table above, in dependency order: `LeaseStore` → `SemanticCache` → Redis caches → `QuotaStore` → `ArtifactStore` → `MemoryStore` → `EvictionStateStore` → `EventStore` (audit) → `EventStore` (billing, respecting `billingErasurePolicy`) → `SessionStore` → `TokenStore` → `CredentialPoolStore`. |
| 5. Clean CRDs         | `deleting`    | Remove all tenant-scoped Kubernetes CRD instances (`AgentSession`, pool annotations, NetworkPolicy labels).                                                                                                                                                                                                                                                      |
| 6. Produce receipt    | `deleted`     | Write an erasure receipt to the audit trail recording each phase's completion timestamp, any errors, which sinks were notified, and the final `deleted` state.                                                                                                                                                                                                   |

**Legal hold interaction during deletion.** Before entering Phase 4, the controller checks for active legal holds on any session or artifact belonging to the tenant. If legal holds exist, the controller pauses at Phase 3 and emits an `admin.tenant.deletion_blocked` audit event listing the held resource IDs. An operator must explicitly clear the holds (or exempt the deletion via `POST /v1/admin/tenants/{id}/force-delete`) before the controller proceeds. The force-delete endpoint records operator identity and justification in the audit trail.

**Idempotency and resumption.** The controller persists the current phase in the tenant record. If the process is interrupted (controller restart, transient failure), it resumes from the last incomplete phase. Each phase is individually idempotent.

**Erasure propagation to external sinks.** Data exported to external systems (SIEM via Section 11.7, billing webhooks/message queues via Section 11.2.1) is outside direct platform control. The erasure job publishes an `erasure.requested` event to all configured billing and audit sinks, carrying the `user_id` (or `tenant_id`) and a deadline. Deployers are responsible for ensuring their downstream consumers honor this event. The erasure receipt records which sinks were notified and their acknowledgment status. The platform guarantees notification and auditability, not external erasure.

**Workspace deduplication.** In v1, each MinIO artifact object is owned by exactly one session — session derivation (Section 7.1) always copies the parent's workspace snapshot into a new independent MinIO object, never shares it. There are no cross-session MinIO references in v1. If a future optimization introduces content-addressed deduplication for workspace snapshots in the `ArtifactStore`, the GC job and erasure job must use reference counting: a blob is only deleted when no other session's artifact references it (i.e., `reference_count = 0`). Until deduplication is implemented, each artifact is session-scoped and deleted directly by TTL without reference counting.

**Data residency.** Tenants and environments support an optional `dataResidencyRegion` field (e.g., `eu-west-1`, `us-east-1`) set via the admin API (`PUT /v1/admin/tenants/{id}` and environment configuration). When set, the platform enforces region constraints at three levels:

1. **Pod pool routing.** Each pool carries a `region` label. When a tenant or environment specifies `dataResidencyRegion`, the gateway restricts pod allocation — including delegation targets — to pools whose `region` label matches. A delegation that would route to a pool in a non-matching region is rejected with `REGION_CONSTRAINT_VIOLATED`. This applies transitively: every node in a delegation tree inherits the root session's region constraint.
2. **Storage routing.** The `StorageRouter` interface accepts `dataResidencyRegion` as a parameter and directs writes (Postgres, MinIO, Redis) to the region-local backend. Deployers configure per-region storage endpoints in the Helm values (`storage.regions` map). When `dataResidencyRegion` is unset, the platform uses the default (single-region) storage backend — no behavioral change for existing deployments.
3. **Validation at session creation.** The gateway validates that at least one pool and one storage backend are available for the requested region before accepting a session. If not, session creation fails with `REGION_UNAVAILABLE`.

**Data residency audit events.** Every data residency enforcement action emits an audit event to make violations and violation attempts independently observable — not just silently rejected:

- When the `StorageRouter` receives a write request where the resolved storage region does not match the session's `dataResidencyRegion`, it rejects the write and emits a `DataResidencyViolationAttempt` audit event (critical severity). The event records: `tenant_id`, `session_id`, `requested_region`, `resolved_region`, `storage_backend`, `operation`, and `timestamp`. This turns silent bypasses (e.g., a misconfigured fallback-to-default route) into observable, alertable signals.
- When pod pool routing rejects a delegation due to `REGION_CONSTRAINT_VIOLATED`, a `DataResidencyViolationAttempt` audit event is emitted with the same fields plus the delegation `parent_session_id` and `target_pool`.
- When session creation fails with `REGION_UNAVAILABLE`, a `DataResidencyRegionUnavailable` audit event (warning severity) is emitted.

These events are forwarded to the SIEM (when configured) alongside all other audit events.

**Inheritance:** Sessions inherit `dataResidencyRegion` from their environment, which inherits from its tenant unless explicitly overridden. An environment may specify a stricter region than its tenant but cannot widen it.

**Multi-region reference architecture.** A multi-region deployment runs one Lenny control plane per region (gateway, controllers, storage), each serving tenants pinned to that region. Cross-region delegation is not supported — delegation trees are region-local by design. A global load balancer (e.g., DNS-based) routes clients to the correct regional gateway based on tenant configuration. Tenant metadata (including `dataResidencyRegion`) is replicated to a lightweight global catalog so the load balancer can resolve region affinity before the first request reaches a gateway. This architecture avoids cross-region data transfer while allowing a single organization to operate tenants in multiple jurisdictions.

**Audit trail.** All compliance operations (legal hold set/cleared, erasure requested/completed) are logged in the audit trail (Section 11.7) with the requesting admin's identity, timestamp, and affected resource IDs.

### 12.9 Data Classification

The platform defines four classification tiers. Every data element maps to exactly one tier, and the tier drives encryption, retention, access, and audit controls.

| Tier | Label            | Description                                                                                                                                                                |
| ---- | ---------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| T1   | **Public**       | Non-sensitive data safe for external exposure (e.g., public runtime catalog metadata, documentation links)                                                                 |
| T2   | **Internal**     | Operational data not intended for external exposure but carrying no regulatory risk (e.g., session metadata, pool metrics, routing tables, non-PII audit events)           |
| T3   | **Confidential** | Business-sensitive data and PII (e.g., workspace files, session transcripts, user identifiers, billing events, memory store contents)                                      |
| T4   | **Restricted**   | Credentials, PHI, and data subject to the strictest regulatory controls (e.g., OAuth tokens, refresh tokens, credential pool secrets, API keys, PHI-tagged workspace data) |

**Default data-type mappings:**

| Data Type                                       | Default Tier      | Store                            |
| ----------------------------------------------- | ----------------- | -------------------------------- |
| Runtime catalog metadata                        | T1 — Public       | Postgres                         |
| Session metadata (IDs, state, timestamps)       | T2 — Internal     | Postgres                         |
| Pool metrics, routing tables                    | T2 — Internal     | Redis                            |
| Audit events (non-PII)                          | T2 — Internal     | Postgres                         |
| Workspace files and snapshots                   | T3 — Confidential | MinIO                            |
| Session transcripts                             | T3 — Confidential | MinIO                            |
| User identifiers, user-scoped audit events      | T3 — Confidential | Postgres                         |
| Billing events                                  | T3 — Confidential | Postgres                         |
| Memory store contents                           | T3 — Confidential | Postgres (or pluggable)          |
| Semantic cache entries                          | T3 — Confidential | Redis (or pluggable)             |
| Eviction state records (`last_message_context`) | T3 — Confidential | Postgres                         |
| OAuth/refresh tokens                            | T4 — Restricted   | Postgres (encrypted)             |
| Credential pool secrets                         | T4 — Restricted   | Postgres (encrypted, KMS-backed) |
| Credential leases                               | T4 — Restricted   | Redis (encrypted)                |

**Per-tenant workspace classification.** By default, workspace data is classified as T3 — Confidential. Tenants that handle PHI or similarly regulated data may elevate workspace classification to T4 — Restricted via tenant configuration:

```yaml
# Tenant level (admin API)
dataClassification:
  workspaceTier: "T4" # default: "T3"
```

When a tenant sets `workspaceTier: T4`, the platform applies Restricted-tier controls (below) to all workspace files, snapshots, and session transcripts for that tenant's sessions. This setting is inherited by all environments under the tenant unless explicitly overridden at the environment level to a **stricter** (never looser) tier.

**Controls driven by classification tier:**

| Control               | T1 — Public             | T2 — Internal            | T3 — Confidential                                  | T4 — Restricted                                                    |
| --------------------- | ----------------------- | ------------------------ | -------------------------------------------------- | ------------------------------------------------------------------ |
| Encryption at rest    | Optional                | Required (storage-layer) | Required (storage-layer, SSE-KMS)                  | Required (envelope encryption via KMS, Section 4.3)                |
| Encryption in transit | TLS                     | mTLS                     | mTLS                                               | mTLS + field-level encryption for token values                     |
| Access control        | Public or authenticated | RBAC role ≥ `viewer`     | RBAC role ≥ `user` + tenant scope                  | RBAC role ≥ `admin` + explicit grant                               |
| Audit logging         | None                    | Write operations         | All read/write operations                          | All operations including access attempts                           |
| Retention default     | Indefinite              | 90 days                  | Deployer-configured (default 7 days, Section 12.5) | Deployer-configured (default 24 hours for leases, Section 4.9)     |
| Retention override    | Tenant-configurable     | Tenant-configurable      | Tenant-configurable, subject to regulatory floor   | Tenant-configurable, subject to regulatory floor, legal-hold aware |
| Erasure on deletion   | Best-effort             | Required within 30 days  | Required within 72 hours (GDPR-aligned)            | Immediate + cryptographic erasure where supported                  |
| Data residency        | No constraint           | No constraint            | Respects `dataResidencyRegion` (Section 12.8)      | Respects `dataResidencyRegion`; cross-region transfer prohibited   |

**Enforcement.** Classification is enforced at the storage interface boundary (Section 12.6). Each store method receives the applicable tier as context and applies the corresponding controls. Tier mismatches (e.g., writing T4 data to a store not configured for envelope encryption) are rejected at write time with a `CLASSIFICATION_CONTROL_VIOLATION` error. The gateway policy engine (Section 4.8) validates tenant classification configuration at session creation.

**Interaction with existing controls.** Data classification integrates with — and does not replace — existing mechanisms:

- **Legal holds** (Section 12.8) override retention at any tier.
- **Data residency** (Section 12.8) applies to T3 and T4 data; classification adds the T4 prohibition on cross-region transfer.
- **Credential encryption** (Section 4.3, 4.9) already satisfies T4 encryption requirements; classification formalizes and makes these requirements auditable.
- **Erasure** (Section 12.8) honors tier-specific erasure timelines during `DeleteByUser` and `DeleteByTenant` operations.

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

**Default-deny policy (applied to every agent namespace — `lenny-agents`, `lenny-agents-kata`, and any future additions):**

> **Helm templatization:** The Helm chart iterates over `.Values.agentNamespaces` (default: `[lenny-agents, lenny-agents-kata]`) and renders all three NetworkPolicy manifests below into each namespace. The YAML examples show `lenny-agents` as a representative instance.

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: lenny-agents # repeated per agent namespace via Helm range
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
```

**Allow gateway-to-pod (applied to all agent pods in every agent namespace):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-gateway-ingress
  namespace: lenny-agents # repeated per agent namespace via Helm range
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
      ports:
        - port: 50051 # {{ .Values.adapter.grpcPort }} — adapter gRPC listen port
          protocol: TCP
  policyTypes: [Ingress]
```

**Allow pod-to-gateway and DNS (applied to all agent pods in every agent namespace):**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-pod-egress-base
  namespace: lenny-agents # repeated per agent namespace via Helm range
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

**`lenny-system` namespace NetworkPolicies (NET-017):**

The `lenny-system` namespace houses the gateway, Token/Connector Service, warm pool controller, scaling controller, and the dedicated CoreDNS instance. These components hold high-value credentials and control-plane authority. The Helm chart applies a default-deny policy and component-specific allow-lists to enforce least-privilege networking within `lenny-system`, mirroring the agent namespace approach.

**Default-deny policy for `lenny-system`:**

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny-all
  namespace: lenny-system
spec:
  podSelector: {}
  policyTypes: [Ingress, Egress]
```

**Component-specific allow-lists** are rendered by the Helm chart as individual NetworkPolicy resources in `lenny-system`. Each policy uses a `podSelector` matching the component's `lenny.dev/component` label. The permitted traffic per component:

| Component                                                                            | Egress Allowed                                                                                                                                                                                                                                                    | Ingress Allowed                                                                                                                     |
| ------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Gateway** (`lenny.dev/component: gateway`)                                         | Agent namespaces (TCP 50051, adapter gRPC); Token Service (mTLS port); PgBouncer (TCP 5432); Redis (TCP 6379); MinIO (TCP 9000); kube-apiserver (TCP 443); `kube-system` CoreDNS (UDP/TCP 53). Explicit deny for cloud metadata endpoints (`169.254.169.254/32`). | External ingress (TCP 443 from Ingress controller namespace); agent namespaces (TCP, gateway gRPC port for pod-to-gateway traffic). |
| **Token/Connector Service** (`lenny.dev/component: token-service`)                   | PgBouncer (TCP 5432); Redis (TCP 6379); KMS endpoint (HTTPS 443, CIDR from `{{ .Values.kms.endpointCIDR }}`); `kube-system` CoreDNS (UDP/TCP 53).                                                                                                                 | Gateway pods only (mTLS port).                                                                                                      |
| **Warm Pool Controller / PoolScalingController** (`lenny.dev/component: controller`) | kube-apiserver (TCP 443); PgBouncer (TCP 5432); `kube-system` CoreDNS (UDP/TCP 53).                                                                                                                                                                               | None (controllers initiate all connections).                                                                                        |
| **Dedicated CoreDNS** (`lenny.dev/component: coredns`)                               | `kube-system` CoreDNS (UDP/TCP 53, for upstream forwarding); external DNS resolvers if configured.                                                                                                                                                                | Agent namespace pods (UDP/TCP 53, per `allow-pod-egress-base` in agent namespaces).                                                 |

> **Note:** `lenny-system` components use `kube-system` CoreDNS for their own DNS resolution (not the dedicated agent CoreDNS instance). The dedicated CoreDNS in `lenny-system` serves agent namespaces only. Cloud metadata endpoint blocking (`169.254.169.254/32`) is applied as an explicit `except` clause on any egress rule that could reach external IPs, preventing credential theft via cloud instance metadata services (IMDS).

**Per-pool egress relaxation:** Pools that need internet access (e.g., for LLM API calls) get additional NetworkPolicy resources allowing egress to specific CIDR ranges or services. These policies are **pre-created** by the Helm chart (or deployer) using label selectors that match pool labels (e.g., `lenny.dev/pool: <pool-name>`, `lenny.dev/egress-profile: restricted`). The warm pool controller does NOT create or modify NetworkPolicies — it only labels pods with the appropriate pool and egress-profile labels so that the pre-created policies take effect. This avoids granting the controller RBAC permissions for NetworkPolicy resources.

**`egressProfile` enum:**

| Profile                | Egress Allowed                                                                      | Use Case                                              |
| ---------------------- | ----------------------------------------------------------------------------------- | ----------------------------------------------------- |
| `restricted` (default) | Gateway + DNS proxy only                                                            | Agent pods that use the LLM proxy; no direct internet |
| `provider-direct`      | Gateway + DNS proxy + LLM provider CIDRs                                            | Direct LLM API access (Bedrock, Vertex endpoints)     |
| `internet`             | Gateway + DNS proxy + all internet (0.0.0.0/0, excluding cluster pod/service CIDRs) | Pods needing package install, web access              |

> **Note:** CIDR ranges for `provider-direct` are maintained in the Helm values (`egressCIDRs.providers`) and updated by deployers when provider endpoints change. NetworkPolicies reference these CIDRs via pre-created policies (per K8s-M3).

> **`internet` profile hardening (NET-002):** The `internet` egress NetworkPolicy explicitly **excludes** cluster-internal CIDRs (`egressCIDRs.excludeClusterPodCIDR` and `egressCIDRs.excludeClusterServiceCIDR` in the Helm values) via `except` clauses on the `0.0.0.0/0` CIDR rule. This prevents lateral movement between agent pods even when internet egress is permitted. Additionally, the `internet` profile **requires** a sandboxed isolation profile (`sandboxed` or `microvm`) — pools with `isolationProfile: standard` (runc) cannot use the `internet` egress profile. The warm pool controller rejects pool configurations that combine `standard` isolation with `internet` egress at validation time.

**DNS exfiltration mitigation:** A dedicated **CoreDNS instance** runs in `lenny-system` (labeled `lenny.dev/component: coredns`) and serves as the DNS resolver for all agent namespaces by default. The `allow-pod-egress-base` NetworkPolicy above routes DNS traffic exclusively to this instance — agent pods cannot reach `kube-system` DNS directly.

The dedicated CoreDNS instance provides:

- **Query logging** — all DNS queries from agent pods are recorded for audit.
- **Per-pod rate limiting** — throttles query volume per source pod to prevent high-throughput tunneling.
- **Response filtering** — blocks TXT records exceeding a size threshold and drops unusual record types commonly used for DNS tunneling (e.g., NULL, PRIVATE, KEY).

**Dedicated CoreDNS high availability (NET-018):** Because the dedicated CoreDNS instance is the sole authorized DNS resolver for all agent pods across all agent namespaces, it must run as a highly available Deployment:

- **Replica count:** Minimum 2 replicas (`{{ .Values.coredns.replicas }}`, default `2`). The Helm chart validates that this value is >= 2.
- **PodDisruptionBudget:** `minAvailable: 1`, ensuring at least one replica survives voluntary disruptions (node drains, rolling upgrades).
- **Failure mode:** If all dedicated CoreDNS replicas become unavailable, agent pods lose DNS resolution entirely — the `allow-pod-egress-base` NetworkPolicy does **not** permit fallback to `kube-system` CoreDNS. This is intentional: a silent fallback would bypass query logging, rate limiting, and response filtering without any indication to the operator. The monitoring stack must fire a `DedicatedDNSUnavailable` critical alert (Section 16) when the ready replica count drops to zero, and a `DedicatedDNSDegraded` warning alert when it drops below the configured minimum.

For `standard` (runc) isolation profiles, deployers may explicitly opt out of the dedicated CoreDNS instance via pool configuration (`dnsPolicy: cluster-default`), which falls back DNS to `kube-system` CoreDNS. This must be a conscious choice — the dedicated instance is the default for all profiles. Note that opting out removes the security properties (query logging, rate limiting, response filtering) for pods in that pool.

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

### 13.5 Delegation Chain Content Security

Delegation chains introduce a prompt injection attack surface: a compromised or manipulated parent agent can craft adversarial `TaskSpec.input` payloads targeting child agents. Lenny provides layered mitigations:

1. **Input size limits** — `contentPolicy.maxInputSize` on `DelegationPolicy` (Section 8.3) enforces a hard byte-size cap on delegation input. Default: 128KB.
2. **Content scanning hook** — `contentPolicy.interceptorRef` invokes a `RequestInterceptor` at the `PreDelegation` phase (Section 4.8) before any delegation is processed. Deployers wire in external classifiers (prompt injection detectors, content safety APIs) here.
3. **Inter-session message scanning** — `contentPolicy.maxInputSize` and `contentPolicy.interceptorRef` apply to `lenny/send_message` payloads via the `PreMessageDelivery` interceptor phase (Section 4.8), providing the same content policy enforcement as delegation inputs.
4. **Messaging rate limits** — `messagingRateLimit` on the delegation lease (Section 8.3) caps `lenny/send_message` volume per session (`maxPerMinute` outbound, `maxPerSession` lifetime). The `maxInboundPerMinute` aggregate limit caps total inbound messages to any single session regardless of the number of senders, preventing N compromised siblings from flooding a target at N × the per-sender rate.
5. **Messaging scope** — `messagingScope` (Section 7.2) restricts which sessions can message each other. Default `direct` limits to parent/children only.
6. **Budget and depth limits** — delegation leases enforce `maxDepth`, `maxTreeSize`, and `maxTokenBudget`, bounding the blast radius of any compromised delegation chain.

**Residual risk without content scanning:** Without `contentPolicy.interceptorRef`, the gateway validates delegation structure (depth, budget, policy tags) but does not inspect content semantics. See Section 22.3 for the explicit non-decision on built-in guardrail logic.

---

## 14. Workspace Plan Schema

The `WorkspacePlan` is the declarative specification for how a session's workspace should be prepared:

```json
{
  "pool": "claude-worker-sandboxed-medium",
  "isolationProfile": "gvisor",
  "workspacePlan": {
    "schemaVersion": 1,
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
    "preferredSource": "pool"
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
  1. **URL validation.** The value must be an HTTPS URL (no HTTP, no non-HTTP schemes). It must parse as a valid URL with a public DNS hostname. IP literals, `localhost`, loopback addresses, and link-local addresses are rejected at submission time. Additionally, well-known cloud metadata hostnames (`metadata.google.internal`, `metadata.google.internal.`, `instance-data`) are rejected regardless of their resolved IP, as a defense-in-depth measure against non-standard metadata endpoint configurations.
  2. **DNS pinning.** The gateway resolves the hostname at registration time and pins the resolved IP. If the resolved IP falls within a private or reserved range (RFC 1918, RFC 6598, loopback, link-local, etc.) the callback is rejected. The callback `http.Client` uses a custom `DialContext` that connects directly to the pinned IP at the TCP level, with the original hostname set only in the `Host` header and TLS SNI. This prevents DNS rebinding attacks where the hostname re-resolves to an internal IP between validation and request time, and ensures the pinned IP — not a re-resolved address — is always the actual connection target.
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

  **Event types:** `session.completed`, `session.failed`, `session.terminated`, `session.awaiting_action` (fired when a session enters `awaiting_client_action` state, enabling CI systems to react without polling), `delegation.completed` (for child task completion notifications).

  **Retry behavior:** Failed deliveries (non-2xx response or timeout) are retried with exponential backoff: 10 s, 30 s, 60 s, 300 s, 900 s (5 attempts total). After exhaustion, the event is marked as undelivered and queryable via `GET /v1/sessions/{id}/webhook-events`.

  **Idempotency:** Each event has a unique `idempotency_key`. Receivers should deduplicate by this key.

- `credentialPolicy`: Per-session override hints for credential assignment. `preferredSource` can be `pool`, `user`, `prefer-user-then-pool`, or `prefer-pool-then-user`. If omitted, the tenant-level credential policy is used. Per-session overrides can only restrict, not expand, the tenant policy. See Section 4.9.
- `runtimeOptions`: Runtime-specific options passed through to the agent binary. If the target Runtime defines a `runtimeOptionsSchema` (a JSON Schema document), the gateway validates `runtimeOptions` against it at session creation time and rejects invalid options with a descriptive error. If no schema is registered, options are passed through as-is (backward compatible) but a warning is logged. Maximum size: 64 KB.

---

## 15. External API Surface

Lenny exposes multiple client-facing APIs through the **`ExternalAdapterRegistry`** — a pluggable adapter system where simultaneously active adapters route by path prefix. All adapters implement a common interface:

```go
type ExternalProtocolAdapter interface {
    // Required — all adapters must implement these three.
    HandleInbound(ctx, w, r, dispatcher) error
    HandleDiscovery(ctx, w, r, runtimes []AuthorizedRuntime) error
    Capabilities() AdapterCapabilities

    // Optional lifecycle hooks — adapters that manage stateful protocols
    // (A2A task lifecycle, push notifications) implement these.
    // Default no-op implementations are provided by BaseAdapter; adapters
    // that embed BaseAdapter only override hooks they need.
    OnSessionCreated(ctx, sessionID, metadata SessionMetadata) error
    OnSessionEvent(ctx, sessionID, event SessionEvent) error
    OnSessionTerminated(ctx, sessionID, reason TerminationReason) error

    // OutboundCapabilities declares what the adapter can push to clients
    // (e.g., streaming updates, push notifications, task state transitions).
    // Adapters with no outbound behavior return an empty declaration.
    OutboundCapabilities() OutboundCapabilitySet
}
```

The gateway provides a **`BaseAdapter`** struct with no-op implementations of all optional methods. Adapters that embed `BaseAdapter` satisfy the full interface and only override lifecycle hooks they need — existing adapters (MCP, OpenAI Completions, Open Responses) require no changes.

**`HandleDiscovery` is required on all adapters.** Every adapter translates Lenny's policy-scoped runtime list into its protocol's native discovery format.

**Three tiers of pluggability:**

- **Built-in** (compiled in): MCP, OpenAI Completions, Open Responses. Always available, configurable via admin API.
- **Config-driven**: deployer points gateway at a Go plugin binary or gRPC service at startup.
- **Runtime registration via admin API**: `POST /v1/admin/external-adapters` — takes effect immediately, no restart.

**Built-in adapter inventory:**

| Adapter                    | Path prefix            | Protocol                     | Status  |
| -------------------------- | ---------------------- | ---------------------------- | ------- |
| `MCPAdapter`               | `/mcp`                 | MCP Streamable HTTP          | V1      |
| `OpenAICompletionsAdapter` | `/v1/chat/completions` | OpenAI Chat Completions      | V1      |
| `OpenResponsesAdapter`     | `/v1/responses`        | Open Responses Specification | V1      |
| `A2AAdapter`               | `/a2a/{runtime}`       | A2A                          | Post-V1 |
| `AgentProtocolAdapter`     | `/ap/v1/agent`         | Agent Protocol               | Post-V1 |

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

**State-mutating endpoint preconditions.** The following table maps each state-mutating session endpoint to its valid precondition states and resulting state transitions. Calling an endpoint in an invalid state returns `409 INVALID_STATE_TRANSITION` with `details.currentState` and `details.allowedStates`.

| Endpoint                           | Valid precondition states                                          | Resulting transition                                                                   | Notes                                                                                                                           |
| ---------------------------------- | ------------------------------------------------------------------ | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `POST /v1/sessions/{id}/upload`    | `created`                                                          | remains `created`                                                                      | Gateway rejects if session already finalized or started                                                                         |
| `POST /v1/sessions/{id}/finalize`  | `created`                                                          | `finalizing` → `ready`                                                                 | Triggers workspace materialization and setup commands                                                                           |
| `POST /v1/sessions/{id}/start`     | `ready`                                                            | `starting` → `running`                                                                 | Starts the agent runtime                                                                                                        |
| `POST /v1/sessions/{id}/interrupt` | `running`                                                          | `suspended`                                                                            | Only valid while the agent is actively executing. Not valid in `suspended`, `starting`, `finalizing`, or any terminal state.    |
| `POST /v1/sessions/{id}/terminate` | `running`, `suspended`, `resume_pending`, `awaiting_client_action` | `completed`                                                                            | Valid in any non-terminal, non-setup state. Graceful shutdown.                                                                  |
| `POST /v1/sessions/{id}/resume`    | `awaiting_client_action`                                           | `resume_pending` → `resuming` → `running`                                              | Only valid after automatic retries are exhausted. Not valid in `suspended` (use message delivery or `resume_session` for that). |
| `POST /v1/sessions/{id}/messages`  | `running`, `suspended`                                             | `running` (if `suspended` with `delivery: immediate`, atomically resumes and delivers) | See Section 7.2 for delivery semantics.                                                                                         |
| `POST /v1/sessions/{id}/derive`    | `completed`, `failed`, `cancelled`, `expired`                      | Creates a new session (original unchanged)                                             | Only valid on terminal sessions with a workspace snapshot.                                                                      |
| `DELETE /v1/sessions/{id}`         | Any non-terminal state                                             | `cancelled`                                                                            | Force-terminates and cleans up. Equivalent to terminate + cleanup in one call.                                                  |

**Externally visible vs. internal-only states.** The REST API (`GET /v1/sessions/{id}`) returns session states from the **session/task state model** (Section 7.2, 8.8), not the pod state model (Section 6.2). Pod states are internal implementation details not exposed to API callers.

| External session state (returned in API) | Description                                                   | Terminal? |
| ---------------------------------------- | ------------------------------------------------------------- | --------- |
| `created`                                | Session created, awaiting file uploads or finalization        | No        |
| `finalizing`                             | Workspace materialization and setup commands in progress      | No        |
| `ready`                                  | Setup complete, awaiting `start`                              | No        |
| `starting`                               | Agent runtime is launching                                    | No        |
| `running`                                | Agent is actively executing                                   | No        |
| `suspended`                              | Agent paused via `interrupt`; pod held, workspace preserved   | No        |
| `resume_pending`                         | Pod failed; gateway is retrying on a new pod                  | No        |
| `awaiting_client_action`                 | Retries exhausted; client must explicitly resume or terminate | No        |
| `completed`                              | Agent finished successfully                                   | Yes       |
| `failed`                                 | Unrecoverable error                                           | Yes       |
| `cancelled`                              | Cancelled by client or parent                                 | Yes       |
| `expired`                                | Lease, budget, or deadline exhausted                          | Yes       |

Internal-only states (from the pod state machine in Section 6.2) such as `warming`, `idle`, `claimed`, `receiving_uploads`, `running_setup`, `sdk_connecting`, and `resuming` are **never** returned in external API responses. These are tracked in the `Sandbox` CRD `.status.phase` for controller reconciliation and operational monitoring only.

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

| Method | Endpoint                     | Description                                                                                                                                     |
| ------ | ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/sessions/start`         | Accepts optional `callbackUrl` for completion notification                                                                                      |
| `POST` | `/v1/sessions/{id}/messages` | Send a message to a session (unified endpoint — replaces `send`). Gateway rejects injection against runtimes with `injection.supported: false`. |

**Discovery and introspection:**

| Method | Endpoint                         | Description                                                                                                                                           |
| ------ | -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/runtimes`                   | List registered runtimes with full `agentInterface`, `mcpEndpoint`, `mcpCapabilities`, capabilities, and labels. Identity-filtered and policy-scoped. |
| `GET`  | `/v1/runtimes/{name}/meta/{key}` | Get published metadata for a runtime (visibility-controlled)                                                                                          |
| `GET`  | `/v1/models`                     | OpenAI-compatible model list (identity-filtered)                                                                                                      |
| `GET`  | `/v1/pools`                      | List pools and warm pod counts                                                                                                                        |
| `GET`  | `/v1/usage`                      | Usage report (filterable by tenant, user, window, labels)                                                                                             |
| `GET`  | `/v1/metering/events`            | Paginated billing event stream                                                                                                                        |

**User credential management:**

| Method   | Endpoint                           | Description                                                                                                                                  |
| -------- | ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/v1/credentials`                  | Register a credential for the authenticated user. One credential per provider; re-registering replaces. See Section 4.9 pre-authorized flow. |
| `GET`    | `/v1/credentials`                  | List the authenticated user's registered credentials (no secret material returned).                                                          |
| `DELETE` | `/v1/credentials/{credential_ref}` | Remove a registered credential. Active session leases are unaffected.                                                                        |

**Evaluation hooks:**

| Method | Endpoint                   | Description                                                                                                                     |
| ------ | -------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/sessions/{id}/eval`   | Accept scored evaluator results (LLM-as-judge scores, custom heuristics, ground-truth comparisons). Stored as session metadata. |
| `POST` | `/v1/sessions/{id}/replay` | Re-run a session against a different runtime version using the same workspace and prompt history.                               |

**Comprehensive Admin API:**

All operational configuration is API-managed. Configuration is split into two planes:

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas, User Role Assignments, Egress Profiles, Experiments, Scaling Policies, Memory Store Config, Webhooks, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions.

CRDs become derived state reconciled from Postgres by PoolScalingController.

All admin CRUD resources use `{name}` as the path identifier (human-readable, unique within scope). Tenants use `{id}` (opaque UUID) because tenant names are mutable display labels. This convention applies uniformly to every admin resource type below.

| Method   | Endpoint                                                        | Description                                                                                                                                                                                                                                                                                                                |
| -------- | --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/v1/admin/runtimes`                                            | Create a runtime definition                                                                                                                                                                                                                                                                                                |
| `GET`    | `/v1/admin/runtimes`                                            | List all runtime definitions                                                                                                                                                                                                                                                                                               |
| `GET`    | `/v1/admin/runtimes/{name}`                                     | Get a specific runtime definition                                                                                                                                                                                                                                                                                          |
| `PUT`    | `/v1/admin/runtimes/{name}`                                     | Update a runtime definition (requires `If-Match`)                                                                                                                                                                                                                                                                          |
| `DELETE` | `/v1/admin/runtimes/{name}`                                     | Delete a runtime definition                                                                                                                                                                                                                                                                                                |
| `POST`   | `/v1/admin/delegation-policies`                                 | Create a delegation policy                                                                                                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/delegation-policies`                                 | List all delegation policies                                                                                                                                                                                                                                                                                               |
| `GET`    | `/v1/admin/delegation-policies/{name}`                          | Get a specific delegation policy                                                                                                                                                                                                                                                                                           |
| `PUT`    | `/v1/admin/delegation-policies/{name}`                          | Update a delegation policy (requires `If-Match`)                                                                                                                                                                                                                                                                           |
| `DELETE` | `/v1/admin/delegation-policies/{name}`                          | Delete a delegation policy                                                                                                                                                                                                                                                                                                 |
| `POST`   | `/v1/admin/connectors`                                          | Create a connector definition                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/connectors`                                          | List all connector definitions                                                                                                                                                                                                                                                                                             |
| `GET`    | `/v1/admin/connectors/{name}`                                   | Get a specific connector definition                                                                                                                                                                                                                                                                                        |
| `PUT`    | `/v1/admin/connectors/{name}`                                   | Update a connector definition (requires `If-Match`)                                                                                                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/connectors/{name}`                                   | Delete a connector definition                                                                                                                                                                                                                                                                                              |
| `POST`   | `/v1/admin/connectors/{name}/test`                              | Live connectivity test: DNS, TLS, MCP handshake, auth validation (rate-limited: 10/min per connector)                                                                                                                                                                                                                      |
| `POST`   | `/v1/admin/pools`                                               | Create a pool configuration                                                                                                                                                                                                                                                                                                |
| `GET`    | `/v1/admin/pools`                                               | List all pool configurations                                                                                                                                                                                                                                                                                               |
| `GET`    | `/v1/admin/pools/{name}`                                        | Get a specific pool configuration                                                                                                                                                                                                                                                                                          |
| `PUT`    | `/v1/admin/pools/{name}`                                        | Update a pool configuration (requires `If-Match`)                                                                                                                                                                                                                                                                          |
| `DELETE` | `/v1/admin/pools/{name}`                                        | Delete a pool configuration                                                                                                                                                                                                                                                                                                |
| `POST`   | `/v1/admin/pools/{name}/drain`                                  | Drain a pool                                                                                                                                                                                                                                                                                                               |
| `GET`    | `/v1/admin/pools/{name}/sync-status`                            | Report CRD reconciliation state: `postgresGeneration`, `crdGeneration`, `lastReconciledAt`, `lagSeconds`, `inSync`                                                                                                                                                                                                         |
| `PUT`    | `/v1/admin/pools/{name}/warm-count`                             | Adjust minWarm/maxWarm at runtime                                                                                                                                                                                                                                                                                          |
| `POST`   | `/v1/admin/credential-pools`                                    | Create a credential pool (tenant-scoped; `tenant-admin` sees own tenant's pools, `platform-admin` sees all with optional `?tenant_id=` filter)                                                                                                                                                                             |
| `GET`    | `/v1/admin/credential-pools`                                    | List credential pools (tenant-scoped)                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/credential-pools/{name}`                             | Get a specific credential pool                                                                                                                                                                                                                                                                                             |
| `PUT`    | `/v1/admin/credential-pools/{name}`                             | Update a credential pool (requires `If-Match`)                                                                                                                                                                                                                                                                             |
| `DELETE` | `/v1/admin/credential-pools/{name}`                             | Delete a credential pool                                                                                                                                                                                                                                                                                                   |
| `POST`   | `/v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | Emergency revocation of a single compromised credential; immediately invalidates all active leases backed by that credential and adds it to the credential deny list (see Section 4.9 Emergency Credential Revocation)                                                                                                     |
| `POST`   | `/v1/admin/credential-pools/{name}/revoke`                      | Emergency revocation of all credentials in a pool                                                                                                                                                                                                                                                                          |
| `POST`   | `/v1/admin/tenants`                                             | Create a tenant                                                                                                                                                                                                                                                                                                            |
| `GET`    | `/v1/admin/tenants`                                             | List all tenants                                                                                                                                                                                                                                                                                                           |
| `GET`    | `/v1/admin/tenants/{id}`                                        | Get a specific tenant                                                                                                                                                                                                                                                                                                      |
| `PUT`    | `/v1/admin/tenants/{id}`                                        | Update a tenant (requires `If-Match`)                                                                                                                                                                                                                                                                                      |
| `DELETE` | `/v1/admin/tenants/{id}`                                        | Delete a tenant                                                                                                                                                                                                                                                                                                            |
| `PUT`    | `/v1/admin/tenants/{id}/rbac-config`                            | Set tenant RBAC configuration                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/tenants/{id}/rbac-config`                            | Get tenant RBAC configuration                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/tenants/{id}/access-report`                          | Cross-environment access matrix                                                                                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/tenants/{id}/rotate-erasure-salt`                    | Rotate the tenant's billing pseudonymization salt. Old salt retained in `previous_erasure_salts` for referential integrity. See Section 12.8.                                                                                                                                                                              |
| `POST`   | `/v1/admin/environments`                                        | Create an environment                                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/environments`                                        | List all environments                                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/environments/{name}`                                 | Get a specific environment                                                                                                                                                                                                                                                                                                 |
| `PUT`    | `/v1/admin/environments/{name}`                                 | Update an environment (requires `If-Match`)                                                                                                                                                                                                                                                                                |
| `DELETE` | `/v1/admin/environments/{name}`                                 | Delete an environment                                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/environments/{name}/usage`                           | Environment billing rollup                                                                                                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/environments/{name}/access-report`                   | Resolved member list with group expansion                                                                                                                                                                                                                                                                                  |
| `GET`    | `/v1/admin/environments/{name}/runtime-exposure`                | Runtimes/connectors in scope                                                                                                                                                                                                                                                                                               |
| `POST`   | `/v1/admin/experiments`                                         | Create an experiment                                                                                                                                                                                                                                                                                                       |
| `GET`    | `/v1/admin/experiments`                                         | List all experiments                                                                                                                                                                                                                                                                                                       |
| `GET`    | `/v1/admin/experiments/{name}`                                  | Get a specific experiment                                                                                                                                                                                                                                                                                                  |
| `PUT`    | `/v1/admin/experiments/{name}`                                  | Update an experiment (requires `If-Match`)                                                                                                                                                                                                                                                                                 |
| `DELETE` | `/v1/admin/experiments/{name}`                                  | Delete an experiment                                                                                                                                                                                                                                                                                                       |
| `GET`    | `/v1/experiments/{name}/results`                                | Experiment results by variant                                                                                                                                                                                                                                                                                              |
| `POST`   | `/v1/admin/external-adapters`                                   | Register an external protocol adapter                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/external-adapters`                                   | List all external protocol adapters                                                                                                                                                                                                                                                                                        |
| `GET`    | `/v1/admin/external-adapters/{name}`                            | Get a specific external adapter                                                                                                                                                                                                                                                                                            |
| `PUT`    | `/v1/admin/external-adapters/{name}`                            | Update an external adapter (requires `If-Match`)                                                                                                                                                                                                                                                                           |
| `DELETE` | `/v1/admin/external-adapters/{name}`                            | Delete an external adapter                                                                                                                                                                                                                                                                                                 |
| `POST`   | `/v1/admin/sessions/{id}/force-terminate`                       | Force-terminate a session                                                                                                                                                                                                                                                                                                  |
| `POST`   | `/v1/admin/users/{user_id}/erase`                               | Initiate a GDPR user-level erasure job. Returns a job ID. Requires `platform-admin` or `tenant-admin` role. See Section 12.8.                                                                                                                                                                                              |
| `GET`    | `/v1/admin/erasure-jobs/{job_id}`                               | Query erasure job status: phase, completion percentage, time elapsed, errors. See Section 12.8.                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/billing-corrections`                                 | Issue a billing correction event. Requires `platform-admin`. Body: `tenant_id`, `corrects_sequence`, `correction_reason_code` (enum), optional `correction_detail`, replacement values (`tokens_input`, `tokens_output`, `pod_minutes`). Returns the correction event with assigned `sequence_number`. See Section 11.2.1. |
| `POST`   | `/v1/admin/bootstrap`                                           | Apply a seed file (idempotent upsert of runtimes, pools, tenants, etc.). Same schema as `bootstrap` Helm values. See Section 17.6.                                                                                                                                                                                         |

**Admin API design constraints:** Error taxonomy, OIDC auth, etag-based concurrency, `dryRun` support, OpenAPI spec, audit logging.

**Error response envelope.** All REST API endpoints (both client-facing and admin) return errors using a canonical JSON envelope:

```json
{
  "error": {
    "code": "QUOTA_EXCEEDED",
    "category": "POLICY",
    "message": "Tenant t1 has exceeded its monthly session quota (limit: 500).",
    "retryable": false,
    "details": {}
  }
}
```

Fields: `code` (string, required) — machine-readable error code from the table below. `category` (string, required) — one of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM` as defined in Section 16.3. `message` (string, required) — human-readable description. `retryable` (boolean, required) — whether the client should retry. `details` (object, optional) — additional context; structure varies by error code.

**Error code catalog:**

| Code                        | Category    | HTTP Status | Description                                                                                                                                                                                                                                              |
| --------------------------- | ----------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `VALIDATION_ERROR`          | `PERMANENT` | 400         | Request body or query parameters failed validation                                                                                                                                                                                                       |
| `INVALID_STATE_TRANSITION`  | `PERMANENT` | 409         | Requested operation is not valid for the current resource state                                                                                                                                                                                          |
| `RESOURCE_NOT_FOUND`        | `PERMANENT` | 404         | The requested resource does not exist or is not visible to the caller                                                                                                                                                                                    |
| `RESOURCE_ALREADY_EXISTS`   | `PERMANENT` | 409         | A resource with the given identifier already exists                                                                                                                                                                                                      |
| `RESOURCE_HAS_DEPENDENTS`   | `PERMANENT` | 409         | Resource cannot be deleted because it is referenced by active dependents. `details.dependents` lists blocking references by type, name, and count.                                                                                                       |
| `ETAG_MISMATCH`             | `PERMANENT` | 412         | The `If-Match` etag does not match the current resource version. `details.currentEtag` contains the current ETag value.                                                                                                                                  |
| `ETAG_REQUIRED`             | `PERMANENT` | 428         | `If-Match` header is required on PUT but was not provided                                                                                                                                                                                                |
| `UNAUTHORIZED`              | `PERMANENT` | 401         | Missing or invalid authentication credentials                                                                                                                                                                                                            |
| `FORBIDDEN`                 | `POLICY`    | 403         | Authenticated but not authorized for this operation                                                                                                                                                                                                      |
| `QUOTA_EXCEEDED`            | `POLICY`    | 429         | Tenant or user quota exceeded                                                                                                                                                                                                                            |
| `RATE_LIMITED`              | `POLICY`    | 429         | Request rate limit exceeded                                                                                                                                                                                                                              |
| `CREDENTIAL_POOL_EXHAUSTED` | `POLICY`    | 503         | No available credentials in the assigned pool                                                                                                                                                                                                            |
| `USER_CREDENTIAL_NOT_FOUND` | `PERMANENT` | 404         | No pre-registered credential found for user and provider. Register a credential via `POST /v1/credentials` or configure pool fallback.                                                                                                                   |
| `RUNTIME_UNAVAILABLE`       | `TRANSIENT` | 503         | No healthy pods available for the requested runtime                                                                                                                                                                                                      |
| `POD_CRASH`                 | `TRANSIENT` | 502         | The session pod terminated unexpectedly                                                                                                                                                                                                                  |
| `TIMEOUT`                   | `TRANSIENT` | 504         | Operation timed out                                                                                                                                                                                                                                      |
| `UPSTREAM_ERROR`            | `UPSTREAM`  | 502         | An external dependency (MCP tool, auth provider) returned an error                                                                                                                                                                                       |
| `TARGET_TERMINAL`           | `PERMANENT` | 409         | Target task or session is in a terminal state                                                                                                                                                                                                            |
| `INJECTION_REJECTED`        | `POLICY`    | 403         | Message injection rejected (runtime has `injection.supported: false`)                                                                                                                                                                                    |
| `MCP_VERSION_UNSUPPORTED`   | `PERMANENT` | 400         | Client MCP version is not supported                                                                                                                                                                                                                      |
| `IMAGE_RESOLUTION_FAILED`   | `PERMANENT` | 422         | Container image reference is invalid or could not be resolved. `details.image` contains the unresolvable reference; `details.reason` describes the failure (e.g., `invalid_digest`, `tag_not_found`, `registry_unreachable`).                            |
| `RESERVED_IDENTIFIER`       | `PERMANENT` | 422         | A field value uses a platform-reserved identifier (e.g., variant `id: "control"`). `details.field` identifies the offending field; `details.value` is the reserved value that was rejected.                                                              |
| `CONFIGURATION_CONFLICT`    | `PERMANENT` | 422         | The requested configuration contains mutually incompatible field values. `details.conflicts` is an array of `{"fields": ["fieldA", "fieldB"], "message": "..."}` entries describing each incompatibility.                                                |
| `SEED_CONFLICT`             | `PERMANENT` | 409         | A bootstrap/seed upsert conflicts with an existing resource in a non-idempotent way and `--force-update` was not set. `details.resource` identifies the conflicting resource by type and name; `details.conflictingFields` lists the fields that differ. |
| `LLM_REQUEST_REJECTED`      | `PERMANENT` | 403         | LLM request rejected by `PreLLMRequest` interceptor. `details.reason` contains the interceptor's rejection reason. Proxy mode only (Section 4.8).                                                                                                      |
| `LLM_RESPONSE_REJECTED`     | `PERMANENT` | 502         | LLM response rejected by `PostLLMResponse` interceptor. `details.reason` contains the interceptor's rejection reason. Proxy mode only (Section 4.8).                                                                                                    |
| `CONNECTOR_REQUEST_REJECTED` | `PERMANENT` | 403         | Connector tool call rejected by `PreConnectorRequest` interceptor. `details.reason` contains the interceptor's rejection reason (Section 4.8).                                                                                                           |
| `CONNECTOR_RESPONSE_REJECTED` | `PERMANENT` | 502         | Connector response rejected by `PostConnectorResponse` interceptor. `details.reason` contains the interceptor's rejection reason (Section 4.8).                                                                                                          |
| `INTERNAL_ERROR`            | `TRANSIENT` | 500         | Unexpected server error                                                                                                                                                                                                                                  |

**Validation error format.** When `code` is `VALIDATION_ERROR`, the `details` field contains a `fields` array describing each validation failure:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "category": "PERMANENT",
    "message": "Request validation failed.",
    "retryable": false,
    "details": {
      "fields": [
        {
          "field": "runtime",
          "message": "must not be empty",
          "rule": "required"
        },
        {
          "field": "workspace.maxSizeMB",
          "message": "must be between 1 and 10240",
          "rule": "range",
          "params": { "min": 1, "max": 10240 }
        }
      ]
    }
  }
}
```

Each entry: `field` (string) — JSON path to the invalid field. `message` (string) — human-readable description. `rule` (string) — validation rule that failed (e.g., `required`, `range`, `pattern`, `enum`). `params` (object, optional) — rule-specific parameters.

**Rate-limit headers.** All REST API responses include rate-limit headers:

| Header                  | Description                                                       |
| ----------------------- | ----------------------------------------------------------------- |
| `X-RateLimit-Limit`     | Maximum requests permitted in the current window                  |
| `X-RateLimit-Remaining` | Requests remaining in the current window                          |
| `X-RateLimit-Reset`     | UTC epoch seconds when the current window resets                  |
| `Retry-After`           | Seconds to wait before retrying (present only on `429` responses) |

**`dryRun` query parameter.** All admin `POST` and `PUT` endpoints accept `?dryRun=true`. Behavior: the gateway performs full request validation — schema, field constraints, referential integrity, policy checks, and quota evaluation — but **does not persist** the result, emit audit events, or trigger any side effects (no CRD reconciliation, no pool scaling, no webhook dispatch). **`dryRun` never makes outbound network calls.** All referential integrity checks performed under `dryRun` are syntactic and against locally cached state only — for example, connector `mcpServerUrl` validation checks URL format and scheme allowlist but does not attempt a network connection. Live connectivity verification requires the dedicated `POST /v1/admin/connectors/{name}/test` endpoint (see below). The response body is identical to a non-dry-run success response (including the computed resource representation), with one addition: the response includes the header `X-Dry-Run: true`.

**Endpoint-specific `dryRun` semantics:**

- **Connectors (`POST /v1/admin/connectors`, `PUT /v1/admin/connectors/{name}`):** Validates URL format, scheme allowlist (`https` only in production), authentication field structure, and referential integrity against known environments. Does **not** perform DNS resolution, TLS handshake, or any outbound call to the connector endpoint. For live reachability testing, use `POST /v1/admin/connectors/{name}/test` (described below).
- **Experiments (`POST /v1/admin/experiments`, `PUT /v1/admin/experiments/{name}`):** Validates experiment definition, variant weight normalization (sum to 1.0), and runtime/pool references. Capacity validation is **not** included — `dryRun` does not query current pool utilization or node availability. Capacity feasibility is evaluated asynchronously by the PoolScalingController when the experiment is activated. To pre-check capacity, use `GET /v1/admin/pools/{name}` to inspect current `availableCount` and `warmCount` before activating an experiment.
- **Environments (`POST /v1/admin/environments`, `PUT /v1/admin/environments/{name}`):** Validates membership selectors and runtime scoping. When `dryRun=true`, the response body includes an additional `preview` object alongside the computed resource representation:

  ```json
  {
    "resource": {
      /* computed environment representation */
    },
    "preview": {
      "matchedRuntimes": ["claude-sonnet", "gpt-4-turbo"],
      "matchedConnectors": ["github-mcp", "jira-mcp"],
      "unmatchedSelectorTerms": []
    }
  }
  ```

  `matchedRuntimes` and `matchedConnectors` list all resources whose labels satisfy the environment's selectors at the time of the dry run. `unmatchedSelectorTerms` lists any selector terms that matched zero resources (useful for detecting typos in label keys or values). This preview is the primary mechanism for the Environment Management UI (Section 21.5).

**Connector live test endpoint.** `POST /v1/admin/connectors/{name}/test` performs a live connectivity check against an already-created connector: DNS resolution, TLS handshake, MCP `initialize` handshake (if the connector type supports it), and authentication credential validation. The response reports pass/fail for each stage:

```json
{
  "connector": "github-mcp",
  "stages": [
    { "name": "dns_resolution", "status": "passed", "latencyMs": 12 },
    { "name": "tls_handshake", "status": "passed", "latencyMs": 45 },
    { "name": "mcp_initialize", "status": "passed", "latencyMs": 230 },
    { "name": "auth_validation", "status": "passed", "latencyMs": 15 }
  ],
  "overall": "passed"
}
```

The endpoint requires `platform-admin` or `tenant-admin` role. It is rate-limited to 10 requests per connector per minute to prevent abuse as a network scanning tool. The test uses the connector's stored credentials and does not accept inline credential overrides.

Supported endpoints:

| Method | Endpoint                               | Notes                                                                               |
| ------ | -------------------------------------- | ----------------------------------------------------------------------------------- |
| `POST` | `/v1/admin/runtimes`                   | Validates runtime definition, checks image reference format                         |
| `PUT`  | `/v1/admin/runtimes/{name}`            | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/delegation-policies`        | Validates policy rules and selector syntax                                          |
| `PUT`  | `/v1/admin/delegation-policies/{name}` | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/connectors`                 | Validates connector config, checks URL format (no outbound calls)                   |
| `PUT`  | `/v1/admin/connectors/{name}`          | Validates update, checks etag (no outbound calls)                                   |
| `POST` | `/v1/admin/pools`                      | Validates pool spec, checks runtime reference                                       |
| `PUT`  | `/v1/admin/pools/{name}`               | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/credential-pools`           | Validates credential pool structure                                                 |
| `PUT`  | `/v1/admin/credential-pools/{name}`    | Validates update, checks etag                                                       |
| `POST` | `/v1/admin/environments`               | Validates membership selectors and runtime scoping; returns `preview` object        |
| `PUT`  | `/v1/admin/environments/{name}`        | Validates update, returns `preview` with matched runtimes/connectors (Section 21.5) |
| `POST` | `/v1/admin/experiments`                | Validates definition and variant weights; no capacity check                         |
| `PUT`  | `/v1/admin/experiments/{name}`         | Validates update, checks etag; no capacity check                                    |
| `POST` | `/v1/admin/external-adapters`          | Validates adapter configuration                                                     |
| `PUT`  | `/v1/admin/external-adapters/{name}`   | Validates update, checks etag                                                       |

ETag interaction: when `dryRun=true` is combined with `If-Match`, the gateway validates the etag against the current resource version and returns `412 ETAG_MISMATCH` if it does not match — the same behavior as a real request. This allows clients to pre-validate an update without committing it. When `dryRun=true` is used on a `POST` (create), `If-Match` is ignored since no prior version exists.

`DELETE` endpoints do not support `dryRun` — deletion validation is trivial (existence + authorization) and does not benefit from a preview. Action endpoints (`drain`, `force-terminate`, `warm-count`) do not support `dryRun` because their value is in the side effect, not validation.

**ETag-based optimistic concurrency.** Every admin resource in Postgres carries an integer `version` column (starts at 1, incremented on every successful write). The ETag value is the quoted decimal version: `"3"`. The gateway enforces ETags as follows:

- **GET responses.** All `GET` endpoints that return an admin resource (single-item or list) include an `ETag` header set to the resource's current version. List responses include per-item ETags in the response body (`"etag": "3"` on each object).
- **PUT requests — `If-Match` required.** Every admin `PUT` request **must** include an `If-Match` header containing the ETag obtained from a prior `GET`. If the header is missing, the gateway returns `428 Precondition Required` with error code `ETAG_REQUIRED`. If the header is present but does not match the current version, the gateway returns `412 Precondition Failed` with error code `ETAG_MISMATCH` (already in the error catalog above); the `412` response includes `details.currentEtag` containing the resource's current ETag so clients can refresh without a round-trip `GET`. On success, the response includes the new `ETag` reflecting the incremented version.
- **Retry pattern after `412 ETAG_MISMATCH`.** When a `PUT` returns `412`, the recommended client pattern is: (1) use `details.currentEtag` from the error response if present, or (2) re-`GET` the **specific resource** (not re-list the collection) to obtain the current ETag and resource body, then merge changes and retry. Clients performing bulk updates from a list response should re-`GET` only the individual resource that conflicted, not re-fetch the entire list.
- **POST requests.** `If-Match` is not required on `POST` (resource creation) and is ignored if present, since no prior version exists.
- **DELETE requests.** `If-Match` is **optional** on `DELETE`. When provided, the gateway validates it and returns `412 ETAG_MISMATCH` on mismatch. When omitted, the delete proceeds unconditionally (last-writer-wins). This avoids forcing clients to fetch before deleting, while still allowing concurrency-safe deletion when desired.
- **Deletion semantics for resources with dependents.** Deleting a resource that is referenced by active dependents is **blocked** (not cascaded). The gateway returns `409` with error code `RESOURCE_HAS_DEPENDENTS` and a `details.dependents` array listing the blocking references. Specific rules per resource type:
  - **Runtime:** blocked if referenced by any active pool (`status != draining/drained`) or any non-terminal session. `details.dependents` example: `[{"type": "pool", "name": "default-pool", "count": 1}, {"type": "session", "state": "running", "count": 3}]`.
  - **Pool:** blocked if any sessions are running or suspended in the pool. Drain the pool first (`POST /v1/admin/pools/{name}/drain`), then delete once all sessions complete.
  - **Delegation Policy:** blocked if referenced by any runtime definition. Remove the reference from the runtime first.
  - **Connector:** blocked if referenced by any environment or runtime. Remove the reference first.
  - **Credential Pool:** blocked if any active credential leases exist. Revoke leases first.
  - **Tenant:** blocked if any non-terminal sessions, pools, or credential pools exist under the tenant. All child resources must be removed first.
  - **Environment:** blocked if any sessions are active within the environment.
  - **Experiment:** blocked if `status: active` and sessions are assigned to variants. Deactivate the experiment first.
  - **External Adapter:** blocked if `status: active`. Set to `inactive` first.
- **Implementation.** The Postgres `UPDATE ... WHERE id = $1 AND version = $2` pattern ensures atomicity without application-level locking. If zero rows are affected, the gateway re-reads the current version and returns `412`.

The `ETAG_REQUIRED` error code (HTTP 428) is added to the error catalog:

| Code            | Category    | HTTP Status | Description                                               |
| --------------- | ----------- | ----------- | --------------------------------------------------------- |
| `ETAG_REQUIRED` | `PERMANENT` | 428         | `If-Match` header is required on PUT but was not provided |

Rate limits are applied per tenant and per user. Admin API endpoints have separate (higher) rate-limit windows from client-facing endpoints.

**Cursor-based pagination.** All list endpoints return paginated results using a cursor-based envelope. This applies to: `GET /v1/sessions`, `GET /v1/runtimes`, `GET /v1/pools`, `GET /v1/usage`, `GET /v1/metering/events`, `GET /v1/sessions/{id}/artifacts`, `GET /v1/sessions/{id}/transcript`, `GET /v1/sessions/{id}/logs`, `GET /v1/experiments/{id}/results`, and all admin `GET` collection endpoints (e.g., `/v1/admin/runtimes`, `/v1/admin/pools`).

Query parameters:

| Parameter | Type    | Default           | Description                                                                                                                                                                                     |
| --------- | ------- | ----------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `cursor`  | string  | (none)            | Opaque cursor returned from a previous response. Omit for the first page.                                                                                                                       |
| `limit`   | integer | 50                | Number of items per page. Minimum: 1, maximum: 200. Values outside this range are clamped.                                                                                                      |
| `sort`    | string  | `created_at:desc` | Sort field and direction, formatted as `field:asc` or `field:desc`. Supported fields vary by resource (typically `created_at`, `updated_at`, `name`). Invalid fields return `VALIDATION_ERROR`. |

Response envelope:

```json
{
  "items": [
    /* array of resource objects */
  ],
  "cursor": "eyJpZCI6IjAxOTVmMzQ...",
  "hasMore": true
}
```

Fields: `items` (array, required) — the page of results. `cursor` (string, nullable) — opaque cursor to pass as the `cursor` query parameter to fetch the next page; `null` when there are no more results. `hasMore` (boolean, required) — `true` if additional pages exist beyond this one.

Cursors are opaque, URL-safe strings. They encode the sort key and unique tiebreaker (typically `id`) to guarantee stable iteration even when new items are inserted. Cursors are valid for 24 hours; expired cursors return `VALIDATION_ERROR` with `details.fields[0].rule: "cursor_expired"`. Clients must not parse or construct cursors — they are an internal implementation detail.

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

| Tool                       | Description                                                    |
| -------------------------- | -------------------------------------------------------------- |
| `create_session`           | Create a new agent session                                     |
| `create_and_start_session` | Create, upload inline files, and start in one call             |
| `upload_files`             | Upload workspace files                                         |
| `finalize_workspace`       | Seal workspace, run setup                                      |
| `start_session`            | Start the agent runtime                                        |
| `attach_session`           | Attach to a running session (returns streaming task)           |
| `send_message`             | Send a message to a session (unified — replaces `send_prompt`) |
| `interrupt_session`        | Interrupt current agent work                                   |
| `get_session_status`       | Query session state (including `suspended`)                    |
| `get_task_tree`            | Get delegation tree for a session                              |
| `get_session_logs`         | Get session logs (paginated)                                   |
| `get_token_usage`          | Get token usage for a session                                  |
| `list_artifacts`           | List artifacts for a session                                   |
| `download_artifact`        | Download a specific artifact                                   |
| `terminate_session`        | End a session                                                  |
| `resume_session`           | Resume a suspended or paused session                           |
| `list_sessions`            | List active/recent sessions (filterable)                       |
| `list_runtimes`            | List available runtimes (identity-filtered, policy-scoped)     |

**Target MCP spec version:** MCP 2025-03-26 (latest stable at time of writing). All MCP features used by Lenny are gated on this version or later.

**Version negotiation.** The `MCPAdapter` performs MCP protocol version negotiation during connection initialization:

1. The client sends its supported MCP version in the `initialize` request (`protocolVersion` field per MCP spec).
2. The gateway responds with the highest mutually supported version. Lenny supports the **current** (`2025-03-26`) and **previous** (`2024-11-05`) MCP spec versions concurrently.
3. If the client's version is older than the oldest supported version, the gateway rejects the connection with a structured error (`MCP_VERSION_UNSUPPORTED`) including the list of supported versions.
4. Once negotiated, the connection is pinned to that version for its lifetime. The `MCPAdapter` dispatches to version-specific serialization logic internally — tool schemas, error formats, and streaming behavior conform to the negotiated version.

**Compatibility policy:** Lenny supports the two most recent stable MCP spec versions simultaneously. When a new MCP spec version is adopted, the oldest supported version enters a 6-month deprecation window. The gateway emits a `mcp_version_deprecated` warning header on connections using the deprecated version.

**MCP features used:**

- Tasks (for long-running session lifecycle and delegation)
- Elicitation (for user prompts, auth flows)
- Streamable HTTP transport

#### 15.2.1 REST/MCP Consistency Contract

The REST API (Section 15.1) and MCP tools (Section 15.2) intentionally overlap for operations like session creation, status queries, and artifact retrieval. Five rules govern this overlap:

1. **Semantic equivalence.** REST and MCP endpoints that perform the same operation (e.g., `POST /v1/sessions` and `create_session` MCP tool) must return semantically identical responses. Both API surfaces share a common service layer in the gateway so that business logic, validation, and response shaping are implemented exactly once.

2. **Tool versioning.** MCP tool schema evolution is governed by Section 15.5 (API Versioning and Stability), item 2.

3. **Shared error taxonomy.** All error responses — REST and MCP — use the error categories defined in Section 16.3 (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`). REST errors return a JSON body: `{"error": {"code": "QUOTA_EXCEEDED", "category": "POLICY", "message": "...", "retryable": false}}`. MCP tool errors use the same `code` and `category` fields inside the MCP error response format, so clients can apply a single error-handling strategy regardless of API surface.

4. **OpenAPI as source of truth.** The REST API's OpenAPI spec is the single authoritative schema for all overlapping operations. MCP tool schemas for overlapping operations (e.g., `create_session`, `get_session_status`, `list_artifacts`) are generated from the OpenAPI spec's request/response definitions, not maintained independently. A code generation step in the build pipeline produces MCP tool JSON schemas from OpenAPI operation definitions, ensuring structural consistency by construction. Any manual MCP-only tool (e.g., `lenny/delegate_task`) that has no REST counterpart is authored independently but must use the shared error taxonomy (item 3).

5. **Contract testing.** CI includes contract tests that call the REST endpoint and **every built-in external adapter** (MCP, OpenAI Completions, Open Responses) for every overlapping operation and assert both structural and behavioral equivalence of responses. These tests cover:

   (a) **Success paths** — identical response payloads modulo transport envelope.

   (b) **Validation errors** — same error `code` and `category` for identical invalid inputs.

   (c) **Authz rejections** — same denial behavior.

   (d) **Behavioral equivalence — `retryable` and `category` flags.** For every error condition exercised in (b) and (c), the `retryable` flag and error `category` must be identical across REST and all adapter surfaces. A transient error that is `retryable: true` on REST must be `retryable: true` on MCP and every other adapter. This prevents silent breakage of client retry logic when switching API surfaces.

   (e) **Behavioral equivalence — session state transitions.** After performing an identical sequence of operations (e.g., create session, interrupt session), `GET /v1/sessions/{id}` and the `get_session_status` MCP tool must return the same session state. The contract tests include a set of fixed operation sequences that exercise all externally visible state transitions (see Section 7.2) and assert state identity across surfaces.

   (f) **Behavioral equivalence — pagination.** For overlapping list operations (e.g., listing artifacts), default page size, cursor semantics, and empty-result shapes must be identical across REST and adapter surfaces. Adapters must not silently return a different subset of results for the same query.

   Contract tests run on every PR; a failure blocks merge. The test harness is introduced in Phase 5 (Section 18) alongside the first phase where both REST and MCP surfaces are active.

   **`RegisterAdapterUnderTest` test matrix.** The contract test harness exposes a `RegisterAdapterUnderTest(adapter ExternalProtocolAdapter)` entry point so that third-party adapter authors can run the suite against their implementation. The test matrix covers the full operation set of overlapping endpoints:
   - All session lifecycle operations: create, get status, interrupt, resume, terminate, list artifacts, retrieve artifact.
   - All error classes: `VALIDATION_ERROR`, `QUOTA_EXCEEDED`, `RATE_LIMITED`, `RESOURCE_NOT_FOUND`, `INVALID_STATE_TRANSITION`, `PERMISSION_DENIED`, `CREDENTIAL_REVOKED`, `CREDENTIAL_POOL_EXHAUSTED`, `ISOLATION_MONOTONICITY_VIOLATED` — each exercised with a canonical triggering input. For each, the test asserts identical `code`, `category`, and `retryable` values.
   - All state transition sequences: at minimum the sequences `create→running→completed`, `create→running→interrupted→resumed→completed`, and `create→running→terminated`.
   - Pagination: multi-page artifact list traversal asserting cursor behavior and total result set identity.

   Third-party adapters that do not pass this full matrix **must not be enabled in production**. The `POST /v1/admin/external-adapters` registration endpoint enforces this gate: a new adapter is created in `status: pending_validation` and will not receive traffic until `PUT /v1/admin/external-adapters/{name}/validate` is called and returns a passing result. `PUT /v1/admin/external-adapters/{name}/validate` runs the `RegisterAdapterUnderTest` suite in a sandboxed environment against the registered adapter and transitions the adapter to `status: active` on success or `status: validation_failed` (with per-test failure details) on failure. Adapters in `pending_validation` or `validation_failed` status are excluded from all traffic routing. This makes compliance testable without out-of-band coordination, and makes the production gate machine-enforceable rather than a documentation requirement.

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

**SDK-warm demotion contract:** Adapters for runtimes that declare `capabilities.preConnect: true` **must** implement the `DemoteSDK` RPC. This RPC cleanly terminates the pre-connected agent process and returns the pod to a pod-warm state so that workspace files (including those matching `sdkWarmBlockingPaths`) can be materialized before the agent starts. The specification must document: expected teardown behavior, timeout (default: 10s — if the SDK process does not exit within this window, the adapter sends SIGKILL), post-demotion pod state (equivalent to a freshly warmed pod-warm pod), and the `UNIMPLEMENTED` error code for adapters that do not support demotion. Runtime authors who set `preConnect: true` without implementing `DemoteSDK` will see session failures whenever a client uploads files matching `sdkWarmBlockingPaths`.

#### 15.4.1 Adapter↔Binary Protocol

The runtime adapter communicates with the agent binary over **stdin/stdout** using newline-delimited JSON (JSON Lines). Each message is a single JSON object terminated by `\n`. The `prompt` message type is removed — the unified `message` type handles all inbound content delivery.

**Inbound messages (adapter → agent binary via stdin):**

| `type` field  | Description                                                                                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `message`     | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` for concurrent-workspace mode. |
| `tool_result` | The result of a tool call requested by the agent. Carries `slotId` in concurrent-workspace mode.                                                                    |
| `heartbeat`   | Periodic liveness ping; agent must respond                                                                                                                          |
| `shutdown`    | Graceful shutdown with no new task                                                                                                                                  |

The `message` type carries an `input` field containing an `OutputPart[]` array (see Internal `OutputPart` Format below), supporting text, images, structured data, and other content types. No `sessionState` field — the runtime knows it's receiving its first message by virtue of just having started. No `follow_up` or `prompt` type anywhere in the protocol.

**Outbound messages (agent binary → adapter via stdout):**

| `type` field    | Description                                                                                           |
| --------------- | ----------------------------------------------------------------------------------------------------- |
| `response`      | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |
| `tool_call`     | Agent requests execution of a tool. Carries `slotId` in concurrent-workspace mode.                    |
| `heartbeat_ack` | Acknowledges an inbound `heartbeat`. Protocol-level; no content payload.                              |
| `status`        | Optional status/trace update                                                                          |

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call on the platform MCP server.

**`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This allows multiple independent concurrent task streams through a single stdin channel.

**Task mode between-task signaling:** Adapter sends `{type: "terminate", reason: "task_complete"}` on the lifecycle channel after a task completes. The next `{type: "message"}` after scrub is the start of the new task.

**stderr** is captured by the adapter for logging and diagnostics but is **not** parsed as protocol messages.

#### Internal `OutputPart` Format

`agent_text` streaming event is replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays. This is Lenny's internal content model — the adapter translates to/from external protocol formats (MCP, A2A) at the boundary.

```json
{
  "schemaVersion": 1,
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

- **`schemaVersion` is an integer identifying the OutputPart schema revision (default `1`).** Present on every persisted `OutputPart`. The forward-compatibility contract has obligations on both sides:
  - **Producer obligation:** Producers MUST set `schemaVersion` to the highest version required by the fields they emit. When a schema version introduces semantically important fields (e.g., `citations` in v2), the producer MUST set `schemaVersion` to that version so consumers can detect the presence of fields they may not understand.
  - **Consumer obligation — streaming/live delivery:** Consumers MUST NOT reject an `OutputPart` solely because its `schemaVersion` is higher than the consumer understands. When a consumer encounters a `schemaVersion` it does not recognize, it processes the fields it does understand and MUST surface a **degradation signal**: a `schema_version_ahead` annotation on the parent `MessageEnvelope` (with `"knownVersion"` and `"encounteredVersion"` fields) so the end user or upstream caller is informed that the response may be incomplete. Consumers MUST NOT silently discard unknown fields without this signal. This ensures data loss from schema mismatch is always visible rather than hidden.
  - **Consumer obligation — durable storage (TaskRecord):** When `OutputPart` arrays are persisted as part of a `TaskRecord` (Section 8.7), the rejection-at-read rule from Section 15.5 item 7 applies: if a reader encounters an `OutputPart` with a `schemaVersion` it does not recognize, it MUST reject the read with a structured error rather than silently dropping fields. Billing and audit records retained for 13 months will span multiple schema revisions; silent field loss in these records is unacceptable. This is a stricter rule than the live-delivery contract because durable records are authoritative and cannot be re-fetched from the producer.
- **`type` is an open string — not a closed enum — with a versioned canonical type registry.** The registry defines platform-defined types and their guaranteed translation behavior per adapter. Any type not in the current registry version is treated as a custom type and falls back to `text` with the original type preserved in `annotations.originalType`. Types may be added to the registry in minor releases; removing a type or changing its translation behavior is a breaking change requiring a major version bump. This retains open-string extensibility while making translation deterministic across adapter implementations.

  **Canonical Type Registry (v1):**

  | Type               | Description                                   | MCP Translation                                              | OpenAI Translation                           | A2A Translation                                      |
  | ------------------ | --------------------------------------------- | ------------------------------------------------------------ | -------------------------------------------- | ---------------------------------------------------- |
  | `text`             | Plain or formatted text                       | `TextContent` block                                          | `text` content                               | A2A `TextPart`                                       |
  | `code`             | Source code with optional language annotation | `TextContent` with `language` annotation                     | `text` content                               | A2A `TextPart` with `mimeType`                       |
  | `reasoning_trace`  | Model reasoning/chain-of-thought              | `TextContent` with `thinking` annotation                     | `text` content (reasoning not representable) | A2A `TextPart` with `metadata.semantic: "reasoning"` |
  | `citation`         | Source citation or reference                  | `TextContent` with citation annotation                       | `text` content                               | A2A `TextPart` with `metadata.semantic: "citation"`  |
  | `screenshot`       | Screen capture image                          | `ImageContent` block                                         | `image_url` content                          | A2A `FilePart` with image MIME type                  |
  | `image`            | General image content                         | `ImageContent` block                                         | `image_url` content                          | A2A `FilePart` with image MIME type                  |
  | `diff`             | Code diff / patch                             | `TextContent` with `language: "diff"`                        | `text` content                               | A2A `TextPart` with `mimeType: "text/x-diff"`        |
  | `file`             | File content (binary or text)                 | `ResourceContent` block                                      | Resolved to inline `text` or dropped         | A2A `FilePart`                                       |
  | `execution_result` | Compound output from code execution           | Flattened to sequential `TextContent` blocks with `parentId` | Flattened to sequential `text` entries       | A2A composite part                                   |
  | `error`            | Error or diagnostic message                   | `TextContent` with `isError: true`                           | `text` content                               | A2A `TextPart` with `metadata.semantic: "error"`     |

  **Custom types** (any `type` value not listed above): collapsed to `text` with `annotations.originalType` set to the original type string. Runtimes may emit any custom type; the gateway passes them through internally but adapters apply the fallback rule at the protocol boundary. The registry is published as part of the runtime adapter specification and versioned alongside the adapter protocol.

- **`mimeType` handles encoding separately.** The gateway validates, logs, and routes based on MIME type without understanding semantics.
- **`inline` vs `ref` as properties, not types.** A part either contains bytes inline or points to a reference. Both valid for any type.
- **`annotations` as an open metadata map.** `role`, `confidence`, `language`, `final`, `audience` — any metadata. The gateway can index and filter on annotations without understanding the part type.
- **`parts` for nesting.** Compound outputs (e.g., `execution_result` containing code, stdout, stderr, chart) are first-class.
- **`id` enables part-level streaming updates** — concurrent part delivery where text streams while an image renders.

**Rationale for internal format over MCP content blocks directly:** Runtimes are insulated from external protocol evolution. When MCP adds new block types or A2A parts change, only the gateway's `ExternalProtocolAdapter` translation layer updates — runtimes are untouched.

**Minimum required fields for Minimum-tier runtimes:** Only `type` and `inline` are required. All other fields (`schemaVersion`, `id`, `mimeType`, `ref`, `annotations`, `parts`, `status`) are optional and have sensible defaults — `schemaVersion` defaults to `1` if absent, `id` is generated by the adapter if absent, `mimeType` defaults to `text/plain` for `type: "text"`, `status` defaults to `complete` for non-streaming responses. A minimal valid `OutputPart` is `{"type": "text", "inline": "hello"}`.

**Simplified text-only response shorthand:** Minimum-tier runtimes may emit a simplified response form with a top-level `text` field instead of an `output` array:

```json
{ "type": "response", "text": "The answer is 4." }
```

The adapter normalizes this to the canonical form `{"type": "response", "output": [{"type": "text", "inline": "The answer is 4."}]}` before forwarding to the gateway. This shorthand is strictly equivalent — runtimes that need structured output (multiple parts, non-text types, annotations) use the full `output` array form.

**Optional** SDK helper `from_mcp_content(blocks)` converts MCP content blocks to `OutputPart` arrays for runtime authors who want to produce output using familiar MCP formats. This helper is a convenience provided by the Lenny SDK — it is not required. Runtimes can construct `OutputPart` objects directly without any SDK dependency.

#### Translation Fidelity Matrix

Each `ExternalProtocolAdapter` translates between `OutputPart` and its wire format. The following matrix documents field-level fidelity for each built-in adapter. Round-trip through adapters that mark a field as **lossy** or **dropped** is not reversible — callers that require full fidelity should use the REST adapter or persist `OutputPart` directly.

| `OutputPart` field | MCP                                                                                                                                                                                                                              | OpenAI Completions                                                                        | REST     | A2A                                                                                                                  |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- | -------- | -------------------------------------------------------------------------------------------------------------------- |
| `schemaVersion`    | Dropped — MCP content blocks have no version field; re-added with default on ingest                                                                                                                                              | Dropped — not representable; re-added with default on ingest                              | Lossless | Lossy — mapped to A2A `metadata`; survives round-trip but as string                                                  |
| `id`               | Lossless — mapped to MCP `partId` annotation                                                                                                                                                                                     | Dropped — no per-content-block ID in Chat Completions                                     | Lossless | Lossless — mapped to A2A `partId`                                                                                    |
| `type`             | Lossy — platform-defined types (see Canonical Type Registry) mapped to nearest MCP block type (`text`, `image`, `resource`); custom types (not in registry) collapsed to `text` with original type in `annotations.originalType` | Lossy — everything becomes `text` or `image_url`; custom types collapsed to `text`        | Lossless | Lossy — platform-defined types mapped to A2A part kinds per registry; custom types placed in `metadata.originalType` |
| `mimeType`         | Lossless — carried in `resource` or `image` block metadata                                                                                                                                                                       | Lossy — only `image/*` preserved via `image_url`; other MIME types dropped                | Lossless | Lossless — A2A parts carry `mimeType` natively                                                                       |
| `inline`           | Lossless                                                                                                                                                                                                                         | Lossless (as `content` string or base64 for images)                                       | Lossless | Lossless                                                                                                             |
| `ref`              | Lossless — mapped to MCP `resource.uri`                                                                                                                                                                                          | Dropped — no URI reference in Chat Completions; adapter resolves to inline before sending | Lossless | Lossy — mapped to A2A `artifact.uri`; scheme may be rewritten                                                        |
| `annotations`      | Lossy — well-known keys (`role`, `final`, `audience`) mapped to MCP annotation fields; unknown keys placed in `metadata` extension if the MCP client negotiated metadata support, otherwise dropped                              | Dropped — no annotation mechanism                                                         | Lossless | Lossy — mapped to A2A `metadata` map; nested objects flattened to JSON strings                                       |
| `parts` (nesting)  | Lossy — flattened to sequential MCP content blocks with `parentId` annotation; one level of nesting reconstructible                                                                                                              | Dropped — flattened to sequential content entries; nesting not recoverable                | Lossless | Lossy — A2A supports one nesting level via composite parts; deeper nesting flattened                                 |
| `status`           | Lossy — mapped to MCP streaming progress events; `failed` mapped to `isError`                                                                                                                                                    | Dropped — Chat Completions has `finish_reason` at message level only                      | Lossless | Lossy — mapped to A2A task state; per-part granularity lost                                                          |
| `protocolHints`    | Dropped — consumed by adapter before serialization; not sent on wire                                                                                                                                                             | Dropped — consumed by adapter before serialization                                        | Lossless | Dropped — consumed by adapter before serialization                                                                   |

**`protocolHints` annotation field.** `OutputPart.annotations` may include a `protocolHints` key containing adapter-specific directives that influence translation behavior. The gateway adapter reads and removes `protocolHints` before serializing the outbound message — hints never appear on the wire. Structure:

```json
{
  "annotations": {
    "protocolHints": {
      "mcp": { "preferResourceBlock": true },
      "openai": { "collapseToText": false },
      "a2a": { "artifactType": "file" }
    }
  }
}
```

Adapters ignore hint keys they do not recognize. Runtimes that do not set `protocolHints` get default translation behavior as described in the matrix above. Hints are optional and only needed when the default translation is inadequate for a specific use case (e.g., forcing a binary blob to be sent as an MCP resource rather than inline base64).

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

**`from` object schema — adapter-injected, runtime never supplies these fields:**

`from.kind` is a closed enum with exactly four values. `from.id` format depends on `kind`:

| `kind`     | `id` format           | Description                                                                                                                                                   | Example               |
| ---------- | --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------- |
| `client`   | `client_{opaque}`     | Client-scoped identifier assigned by the gateway at session creation.                                                                                         | `client_8f3a2b`       |
| `agent`    | `sess_{session_id}`   | The session ID of the sending agent. Enables reply routing via `inReplyTo`.                                                                                   | `sess_01J5K9...`      |
| `system`   | `lenny-gateway`       | Always the literal string `lenny-gateway`. Used for platform-injected messages (heartbeats, shutdown, credential rotation notices).                           | `lenny-gateway`       |
| `external` | `conn_{connector_id}` | The registered connector ID from the `ConnectorDefinition`. Used for messages originating from external A2A agents or MCP servers routed through a connector. | `conn_slack_bot_prod` |

The adapter populates `from.kind` and `from.id` from execution context before delivering the message to the runtime. Runtimes MUST NOT set these fields; any runtime-supplied `from` is silently overwritten by the adapter.

**Additional adapter-injected fields:**

- `requestId` in `lenny/request_input` — generated by the gateway; runtime only supplies `parts`

**`delivery`** — optional mechanical hint. `immediate` means deliver at next stdin read (after interrupt acknowledgment if needed). Absent means queue for next natural pause.

**`id`** — every message has a stable ID enabling threading, reply tracking, and non-linear context retrieval.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Future-proof:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all future conversational patterns without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

#### Protocol Reference — Message Schemas

All messages on stdin use the full `MessageEnvelope` format (Section 15.4.1). Runtimes MUST ignore unrecognized fields. Minimum-tier runtimes need only read `type`, `id`, and `input` — all other envelope fields (`from`, `inReplyTo`, `threadId`, `delivery`) can be safely ignored.

##### Inbound: `message`

```json
{
  "type": "message",
  "id": "msg_001",
  "input": [{ "type": "text", "inline": "What is 2+2?" }],
  "from": "client",
  "threadId": "t_01",
  "delivery": "at-least-once"
}
```

Minimum-tier: read `type`, `id`, `input`. Ignore all other fields.

##### Inbound: `heartbeat`

```json
{ "type": "heartbeat", "ts": 1717430400 }
```

Agent must respond with `heartbeat_ack` (see below). If no ack within 10 seconds, the adapter considers the process hung and sends SIGTERM.

##### Inbound: `shutdown`

```json
{ "type": "shutdown", "reason": "drain", "deadline_ms": 10000 }
```

Agent must finish current work and exit within `deadline_ms`. No acknowledgment required — the adapter watches for process exit. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds.

##### Inbound: `tool_result`

Schema:

```json
{
  "type": "tool_result",
  "id": "<string, required — matches the tool_call.id this result responds to>",
  "content": ["<OutputPart[], required — result content>"],
  "isError": "<boolean, optional — true if tool execution failed; defaults to false>"
}
```

Example:

```json
{
  "type": "tool_result",
  "id": "tc_001",
  "content": [{ "type": "text", "inline": "file contents here" }],
  "isError": false
}
```

**Correlation:** Every `tool_result.id` MUST match the `id` of a previously emitted `tool_call`. The adapter validates this — a `tool_result` with an unknown `id` is dropped and logged as a protocol error. Agents may have multiple outstanding `tool_call` requests; results may arrive in any order.

**Delivery semantics:** Tool calls use synchronous request/response semantics within the stdin/stdout channel. The agent emits a `tool_call`, then continues reading stdin until it receives the matching `tool_result` (identified by `id`). Other inbound messages (`heartbeat`, additional `message` content) may arrive before the `tool_result` — the agent must handle interleaved delivery. There is no async callback or webhook mechanism; all tool results are delivered inline on stdin.

**Tool access by tier:**

| Tier         | Tool access                                                                                                        | `tool_call` / `tool_result` behavior                                                                                                                                                                                                                               |
| ------------ | ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Minimum**  | No MCP tools available. The agent binary has no platform MCP server or connector MCP servers.                      | Agents MAY still emit `tool_call` for adapter-local tools (e.g., `read_file`, `write_file` provided by the adapter's local sandbox tooling). The adapter resolves these locally and returns `tool_result` on stdin. No platform or connector tools are accessible. |
| **Standard** | Platform MCP server tools (`lenny/delegate_task`, `lenny/request_input`, etc.) and per-connector MCP server tools. | The agent calls MCP tools via the MCP client connection to the adapter's local servers (not via `tool_call` on stdin). The stdin `tool_call`/`tool_result` channel is used for adapter-local tools only.                                                           |
| **Full**     | Same as Standard plus lifecycle channel capabilities.                                                              | Same as Standard.                                                                                                                                                                                                                                                  |

##### Outbound: `response`

```json
{
  "type": "response",
  "output": [{ "type": "text", "inline": "The answer is 4." }]
}
```

Minimum-tier shorthand (adapter normalizes to canonical form above):

```json
{ "type": "response", "text": "The answer is 4." }
```

##### Outbound: `tool_call`

Schema:

```json
{
  "type": "tool_call",
  "id": "<string, required — unique call identifier; used to correlate the inbound tool_result>",
  "name": "<string, required — tool name>",
  "arguments": "<object, required — tool-specific parameters; validated by the adapter against the tool's input schema>"
}
```

Example:

```json
{
  "type": "tool_call",
  "id": "tc_001",
  "name": "read_file",
  "arguments": { "path": "/workspace/foo.txt" }
}
```

The `id` field is generated by the agent and must be unique within the session. Recommended format: `tc_` prefix with a monotonic counter or random suffix (e.g., `tc_001`, `tc_a7f3b`). The adapter uses this `id` to route the corresponding `tool_result` back on stdin.

##### Outbound: `heartbeat_ack`

```json
{ "type": "heartbeat_ack" }
```

##### Outbound: `status` (optional)

```json
{ "type": "status", "state": "thinking", "message": "Analyzing code..." }
```

**Exit Codes**

| Code | Meaning                                                            |
| ---- | ------------------------------------------------------------------ |
| 0    | Normal completion — session ended cleanly or shutdown honored      |
| 1    | Runtime error — adapter logs stderr and reports failure to gateway |
| 2    | Protocol error — agent could not parse inbound messages            |
| 137  | SIGKILL (set by OS) — adapter treats as crash, pod is not reused   |

Any non-zero exit during an active session causes the gateway to report a session error to the client. During draining, exit code 0 confirms graceful shutdown; non-zero triggers an alert but the session result (if any) is still delivered.

**Annotated Protocol Trace — Minimum-Tier Session**

```
1. Adapter starts agent binary, stdin/stdout pipes open.
2. Adapter writes to stdin:
   {"type": "message", "id": "msg_001", "input": [{"type": "text", "inline": "Hello"}], "from": "client", "threadId": "t_01"}
3. Agent reads line from stdin, parses JSON, reads type/id/input (ignores other fields).
4. Agent writes to stdout (either form is valid):
   {"type": "response", "text": "Echo: Hello"}
   — or equivalently —
   {"type": "response", "output": [{"type": "text", "inline": "Echo: Hello"}]}
5. Adapter reads line from stdout, delivers response to gateway.
6. [Heartbeat interval] Adapter writes:
   {"type": "heartbeat", "ts": 1717430410}
7. Agent writes:
   {"type": "heartbeat_ack"}
8. Gateway initiates shutdown. Adapter writes:
   {"type": "shutdown", "reason": "drain", "deadline_ms": 10000}
9. Agent finishes, exits with code 0.
10. Adapter reports clean termination to gateway.
```

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
- Must handle `{type: "heartbeat"}` by responding with `{type: "heartbeat_ack"}` — failure to ack within 10 seconds causes SIGTERM
- Must handle `{type: "shutdown"}` by exiting within the specified `deadline_ms`
- Zero Lenny knowledge required beyond the above message types
- No checkpoint/restore support, no detailed health reporting

**Standard** — minimum plus MCP integration:

- Connects to adapter's platform MCP server and connector servers via the adapter manifest
- Uses platform capabilities (delegation, discovery, output parts, elicitation)
- Standard MCP — no Lenny-specific code

**Standard-Tier MCP Integration**

Standard-tier runtimes connect to the adapter's local MCP servers as a standard MCP client. The following details apply:

- **Transport.** All intra-pod MCP servers use **abstract Unix sockets** exclusively (names listed in the adapter manifest, e.g., `@lenny-platform-mcp`, `@lenny-connector-github`). There is no stdio transport for intra-pod MCP — stdio is reserved for the binary protocol between the adapter and the runtime.
- **Protocol version.** The adapter's local MCP servers speak **MCP 2025-03-26** (the platform's target MCP spec version; see Section 15.2 for version negotiation details). The local servers also accept **MCP 2024-11-05** for backward compatibility.
- **Client libraries.** Runtime authors should use an existing MCP client library for their language (e.g., `mcp-go` for Go, `@modelcontextprotocol/sdk` for TypeScript/Node.js, `mcp` for Python). These libraries work out of the box against the adapter's local servers — no Lenny-specific client code, initialization, or authentication is required. The runtime simply connects to the abstract Unix socket and performs standard MCP `initialize` → `tools/list` → `tools/call` interactions.
- **Tool discovery.** The runtime calls `tools/list` on each MCP server (platform and connectors) to discover available tools. The platform MCP server exposes the tools listed in Part A of this section (e.g., `lenny/delegate_task`, `lenny/output`). Each connector server exposes that connector's tools.
- **No authentication.** Intra-pod MCP connections are unauthenticated. The adapter trusts the runtime within the pod boundary; network policy prevents external access to the abstract sockets.

**Full** — standard plus lifecycle channel:

- Opens the lifecycle channel for operational signals
- True session continuity, clean interrupt points, mid-session credential rotation
- `DRAINING` state with graceful shutdown coordination
- Checkpoint/restore support

**Tier Comparison Matrix**

The following matrix enumerates every tier-sensitive capability with its behavior at each integration level. Capabilities marked "N/A" are not available and have no fallback.

| Capability                                                                 | Minimum                                                                                                                                       | Standard                                                                                                                                   | Full                                                                                                                                            |
| -------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| **stdin/stdout binary protocol**                                           | Yes                                                                                                                                           | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Heartbeat / shutdown handling**                                          | Yes                                                                                                                                           | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Platform MCP server** (delegation, discovery, elicitation, output parts) | N/A — runtime operates without platform tools                                                                                                 | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Connector MCP servers**                                                  | N/A — no connector access                                                                                                                     | Yes                                                                                                                                        | Yes                                                                                                                                             |
| **Lifecycle channel**                                                      | N/A — operates in fallback-only mode                                                                                                          | N/A — operates in fallback-only mode                                                                                                       | Yes                                                                                                                                             |
| **Checkpoint / restore**                                                   | No checkpoint support; pod failure loses in-flight context. Gateway restarts session from last gateway-persisted state.                       | Best-effort snapshot without runtime pause (`consistency: best-effort`). Minor workspace inconsistencies possible on resume (Section 4.4). | Consistent checkpoint with runtime pause via lifecycle channel `checkpoint_request` / `checkpoint_ready`.                                       |
| **Interrupt**                                                              | No clean interrupt. Gateway sends SIGTERM; runtime has no opportunity to reach a safe stop point.                                             | No clean interrupt. Same SIGTERM-based termination as Minimum.                                                                             | Clean interrupt via `interrupt_request` on lifecycle channel; runtime acknowledges with `interrupt_acknowledged` and reaches a safe stop point. |
| **Credential rotation**                                                    | Checkpoint → pod restart → `AssignCredentials` with new lease → `Resume`. If checkpoint unsupported, in-flight context is lost (Section 4.7). | Checkpoint → pod restart → `AssignCredentials` with new lease → `Resume`. Brief session pause; client sees reconnect.                      | In-place rotation via `RotateCredentials` RPC and `credentials_rotated` lifecycle message. No session interruption.                             |
| **Deadline / expiry warning**                                              | No advance warning. `DEADLINE_APPROACHING` signal requires lifecycle channel; Minimum-tier receives only `shutdown` at expiry.                | No advance warning. Same as Minimum — no lifecycle channel to deliver `DEADLINE_APPROACHING`.                                              | `DEADLINE_APPROACHING` signal delivered on lifecycle channel before session expiry (Section 10).                                                |
| **Graceful drain (`DRAINING` state)**                                      | No drain coordination. Adapter sends `shutdown` with `deadline_ms`; SIGTERM on timeout.                                                       | No drain coordination. Same as Minimum.                                                                                                    | `DRAINING` state via lifecycle channel enables graceful shutdown coordination before `shutdown`.                                                |
| **Simplified response shorthand** (`{type: "response", text: "..."}`)      | Yes — adapter normalizes to canonical `OutputPart` form (Section 15.4.1).                                                                     | Yes — available but typically unused since Standard runtimes produce structured output.                                                    | Yes — available but typically unused.                                                                                                           |
| **OutputPart minimal fields**                                              | Only `type` and `inline` required; all other fields optional with defaults (Section 15.4.1).                                                  | Full `OutputPart` schema available.                                                                                                        | Full `OutputPart` schema available.                                                                                                             |
| **MessageEnvelope fields**                                                 | Only `type`, `id`, `input` needed; all other envelope fields safely ignored (Section 15.4.1).                                                 | Full envelope including `from`, `inReplyTo`, `threadId`, `delivery`.                                                                       | Full envelope including `from`, `inReplyTo`, `threadId`, `delivery`.                                                                            |

Third-party authors should start with a minimum adapter and incrementally adopt standard and full features as needed.

#### 15.4.4 Sample Echo Runtime

The project includes a reference **`echo-runtime`** — a trivial agent binary that echoes back messages with metadata (timestamp, session ID, message sequence number). It serves two purposes:

1. **Platform testing:** Validates the full session lifecycle (pod claim → workspace setup → message → response → teardown) without requiring a real agent runtime or LLM credentials.
2. **Template for custom runtimes:** Demonstrates the stdin/stdout JSON Lines protocol, heartbeat handling, and graceful shutdown — the minimal contract a custom agent binary must implement.

```
Pseudocode (Minimum-tier):

    seq = 0
    while line = read_line(stdin):
        msg = json_parse(line)
        switch msg.type:
            case "message":
                seq += 1
                write_line(stdout, json({
                    "type": "response",
                    "output": [{
                        "type": "text",
                        "inline": "echo [seq={seq}]: {msg.input[0].inline}"
                    }]
                }))
            case "heartbeat":
                write_line(stdout, json({"type": "heartbeat_ack"}))
            case "shutdown":
                exit(0)
            default:
                // ignore unknown types for forward compatibility
    exit(0)
```

#### 15.4.5 Runtime Author Roadmap

Runtime-author information is distributed across this specification. The following reading order provides a guided path from first build to production-ready adapter, organized by integration tier.

**Minimum-tier (get a runtime working):**

1. **Section 15.4.4** — Sample Echo Runtime. Copy this pseudocode as your starting point.
2. **Section 15.4.1** — Adapter↔Binary Protocol. The stdin/stdout JSON Lines contract, message types, `OutputPart` format, and simplified response shorthand.
3. **Section 15.4.2** — RPC Lifecycle State Machine. Understand `INIT → READY → ACTIVE → DRAINING → TERMINATED` transitions.
4. **Section 15.4.3** — Runtime Integration Tiers. Tier definitions and the capability comparison matrix — confirms what Minimum-tier runtimes can skip.
5. **Section 6.4** — Pod Filesystem Layout. Where your binary's working directory, workspace, and scratch space live (`/workspace/current/`, `/tmp/`, `/artifacts/`).
6. **Section 17.4** — Local Development Mode (`lenny-dev`). Use `make run` for zero-dependency local testing against the gateway contract.

**Standard-tier (add MCP integration):**

7. **Section 4.7** — Runtime Adapter. Component overview, adapter manifest, RPC contract between gateway and adapter.
8. **Section 9.1** — MCP Integration. How the platform MCP server and connector MCP servers are exposed to your runtime.
9. **Section 8.2** — Delegation Mechanism. How `lenny/delegate_task` works if your runtime delegates sub-tasks.
10. **Section 5.1** — Runtime. Runtime definition schema (`type`, `capabilities`, `baseRuntime`), registration via admin API.

**Full-tier (lifecycle channel and production hardening):**

11. **Section 5.2** — Pool Configuration and Execution Modes. Execution modes (session, task, concurrent-workspace), resource classes, and pool sizing.
12. **Section 7.1** — Session Lifecycle Normal Flow. End-to-end session flow from pod claim through teardown.
13. **Section 13.1–13.2** — Pod Security and Network Isolation. Security constraints your runtime operates under (seccomp, gVisor, egress rules).
14. **Section 14** — Workspace Plan Schema. How workspace sources are declared and materialized before your binary starts.
15. **Section 15.5** — API Versioning and Stability. Versioning guarantees for the adapter protocol.

### 15.5 API Versioning and Stability

Community contributors and integrators need clear guarantees about which APIs are stable and how breaking changes are managed. Each external surface follows its own versioning scheme:

1. **REST API:** Versioned via URL path prefix (`/v1/`). Breaking changes require a new version (`/v2/`). Non-breaking additions (new fields, new endpoints) are added to the current version. The previous version is supported for at least 6 months after a new version ships.

2. **MCP tools:** Versioned via the MCP protocol's capability negotiation (see Section 15.2 for target version and negotiation details). The gateway supports two concurrent MCP spec versions (current + previous) with a 6-month deprecation window for the oldest. Tool schemas can add optional fields without a version bump. Removing or renaming fields, or changing semantics, is a breaking change.

3. **Runtime adapter protocol:** Versioned independently (see Section 15.4). The adapter advertises a protocol version at INIT; the gateway selects a compatible version. Major version changes are breaking.

4. **CRDs:** Versioned via Kubernetes API versioning conventions (`v1alpha1` → `v1beta1` → `v1`). Conversion webhooks handle multi-version coexistence during upgrades.

5. **Definition of "breaking change":** Removing a field, changing a field's type, changing the default behavior of an existing feature, removing an endpoint/tool, or changing error codes for existing operations.

6. **Stability tiers:**
   - `stable`: Covered by versioning guarantees above.
   - `beta`: May change between minor releases with deprecation notice.
   - `alpha`: May change without notice.

7. **Durable data schema versioning:** All Postgres-persisted record types carry a `schemaVersion` integer field (starting at `1`) that identifies the schema revision used to write the record. This applies to: `TaskRecord` (Section 8.7), billing events (Section 11.2.1), audit events (`EventStore`), checkpoint metadata (Section 7.1), session records (Section 5), and `WorkspacePlan` (Section 14). The field is set at write time by the gateway and is immutable once written. Reader code uses `schemaVersion` to select the correct deserialization path, enabling rolling schema migrations without downtime. Records with an unrecognized `schemaVersion` are rejected at read time with a structured error, preventing silent data misinterpretation. This is critical for billing events, which are retained for 13 months (Section 11.2.1) and will span multiple schema revisions. **This rejection-at-read rule extends to `OutputPart` arrays nested within `TaskRecord`:** if any `OutputPart` in a persisted `TaskRecord` carries a `schemaVersion` that the reader does not recognize, the reader MUST reject the read with a structured error rather than silently dropping unknown fields (see Section 15.4.1, `schemaVersion` consumer obligation for durable storage).

### 15.6 Client SDKs

Lenny provides official client SDKs for **Go** and **TypeScript/JavaScript** as part of the v1 deliverables. SDKs encapsulate session lifecycle management, MCP streaming with automatic reconnect-with-cursor, file upload multipart handling, webhook signature verification, and error handling with retries — logic that is complex and error-prone to re-implement from the protocol specs alone.

SDKs are generated from the OpenAPI spec (REST) and MCP tool schemas, with hand-written streaming and reconnect logic layered on top. Community SDKs for other languages can build on the published OpenAPI spec and the MCP protocol specification.

Client SDKs follow the same versioning scheme as the API surfaces they wrap (Section 15.5): SDK major versions track REST API versions, and SDK releases note any MCP tool schema changes.

---

## 16. Observability

### 16.1 Metrics

| Metric                                                                                                                                                                                                                 | Type            |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------- |
| Active sessions (by runtime, pool, state, tenant)                                                                                                                                                                      | Gauge           |
| Warm pods available (by pool)                                                                                                                                                                                          | Gauge           |
| Stale warm pods (idle beyond threshold, by pool)                                                                                                                                                                       | Gauge           |
| Task-mode per-pod scrub failure count (`lenny_task_pod_scrub_failure_count`, per pod, labeled by `pod_name`, `pool`, `runtime_class`)                                                                                  | Gauge           |
| Task-mode pod retirement (`lenny_task_pod_retirement_total`, labeled by `reason`: `task_count_limit`, `uptime_limit`, `scrub_failure_limit`, `pool`, `runtime_class`)                                                  | Counter         |
| Concurrent-workspace slot failure count (`lenny_slot_failure_total`, labeled by `reason`, `pool`, `pod_name`)                                                                                                          | Counter         |
| Concurrent-workspace slot pod replacement (`lenny_slot_pod_replacement_total`, labeled by `pool`, `pod_name`)                                                                                                          | Counter         |
| Session creation latency (phases)                                                                                                                                                                                      | Histogram       |
| Time-to-claim (session request to pod claimed)                                                                                                                                                                         | Histogram       |
| Pod state transition durations (per state)                                                                                                                                                                             | Histogram       |
| Upload bytes/second and queue depth                                                                                                                                                                                    | Counter + Gauge |
| Upload extraction aborted (`lenny_upload_extraction_aborted_total`, labeled by `reason`: `zip_bomb`, `size_limit`, `path_traversal`, `symlink`, `format_error`)                                                        | Counter         |
| Token usage (by user, runtime, tenant)                                                                                                                                                                                 | Counter         |
| Retry count (by failure classification)                                                                                                                                                                                | Counter         |
| Resume success/failure rate                                                                                                                                                                                            | Counter         |
| Delegation depth distribution                                                                                                                                                                                          | Histogram       |
| Delegation tree size distribution                                                                                                                                                                                      | Histogram       |
| Gateway replica count                                                                                                                                                                                                  | Gauge           |
| Gateway active streams (per replica)                                                                                                                                                                                   | Gauge           |
| Policy denials (by reason, tenant)                                                                                                                                                                                     | Counter         |
| Checkpoint size and duration                                                                                                                                                                                           | Histogram       |
| Session last checkpoint age (`lenny_session_last_checkpoint_age_seconds`, per session, labeled by `session_id`, `pool`, `tier`)                                                                                        | Gauge           |
| Checkpoint storage fallback to Postgres minimal state (`lenny_checkpoint_eviction_fallback_total`, labeled by `pool`, `had_prior_checkpoint`)                                                                          | Counter         |
| Postgres connection pool utilization (per replica)                                                                                                                                                                     | Gauge           |
| Redis memory usage and eviction rate                                                                                                                                                                                   | Gauge + Counter |
| mTLS handshake latency (gateway-to-pod)                                                                                                                                                                                | Histogram       |
| Credential lease assignments (by provider, pool, source)                                                                                                                                                               | Counter         |
| Credential rotations (by reason: rate_limit, auth_expired, provider_unavailable)                                                                                                                                       | Counter         |
| Credential pool utilization (active leases / total credentials, by pool)                                                                                                                                               | Gauge           |
| Credential pool health (credentials in cooldown, by pool)                                                                                                                                                              | Gauge           |
| Credential lease duration                                                                                                                                                                                              | Histogram       |
| Credential pre-claim mismatch (check passed, assignment failed; by pool, provider)                                                                                                                                     | Counter         |
| Elicitation round-trip latency (`lenny_elicitation_roundtrip_seconds`)                                                                                                                                                 | Histogram       |
| Elicitation requests pending (`lenny_elicitation_pending`)                                                                                                                                                             | Gauge           |
| Elicitation requests suppressed (`lenny_elicitation_suppressed_total`)                                                                                                                                                 | Counter         |
| Elicitation requests timed out (`lenny_elicitation_timeout_total`)                                                                                                                                                     | Counter         |
| Delegation budget utilization ratio (`lenny_delegation_budget_utilization_ratio`)                                                                                                                                      | Gauge           |
| Delegation lease extensions (`lenny_delegation_lease_extension_total`)                                                                                                                                                 | Counter         |
| Delegation tree token usage (`lenny_delegation_tree_token_usage_total`)                                                                                                                                                | Counter         |
| Delegation budget reconstruction events (`lenny_delegation_budget_reconstruction_total`, by `outcome`: `success`, `irrecoverable`)                                                                                     | Counter         |
| Checkpoint storage bytes (`lenny_checkpoint_storage_bytes_total`, labeled by `tenant_id`, `pool`)                                                                                                                      | Gauge           |
| Pod claim queue depth (`lenny_pod_claim_queue_depth`, by pool)                                                                                                                                                         | Gauge           |
| Pod claim queue wait time (`lenny_pod_claim_queue_wait_seconds`, by pool)                                                                                                                                              | Histogram       |
| Pod claim optimistic-lock conflicts (`lenny_pod_claim_conflict_total`, by pool)                                                                                                                                        | Counter         |
| Pod claim timeouts (`lenny_pod_claim_timeout_total`, by pool)                                                                                                                                                          | Counter         |
| **Token Service**                                                                                                                                                                                                      |                 |
| Token Service request duration (`lenny_token_service_request_duration_seconds`, by operation: `assign`, `rotate`, `refresh`)                                                                                           | Histogram       |
| Token Service errors (`lenny_token_service_errors_total`, by error type)                                                                                                                                               | Counter         |
| Token Service circuit breaker state (`lenny_token_service_circuit_state`, 0=closed, 1=half-open, 2=open)                                                                                                               | Gauge           |
| Token Service secret reloads (`lenny_token_service_secret_reloads_total`, by secret name, outcome: `success`, `not_found`, `parse_error`)                                                                              | Counter         |
| **Gateway Subsystems**                                                                                                                                                                                                 |                 |
| Per-subsystem request duration (`lenny_gateway_{subsystem}_request_duration_seconds`, where `{subsystem}` ∈ {`stream_proxy`, `upload_handler`, `mcp_fabric`, `llm_proxy`})                                             | Histogram       |
| Per-subsystem errors (`lenny_gateway_{subsystem}_errors_total`, by error type)                                                                                                                                         | Counter         |
| Per-subsystem queue depth (`lenny_gateway_{subsystem}_queue_depth`)                                                                                                                                                    | Gauge           |
| Per-subsystem circuit breaker state (`lenny_gateway_{subsystem}_circuit_state`, 0=closed, 1=half-open, 2=open)                                                                                                         | Gauge           |
| LLM Proxy active connections (`lenny_gateway_llm_proxy_active_connections`)                                                                                                                                            | Gauge           |
| **Warm Pool Replenishment**                                                                                                                                                                                            |                 |
| Pod startup duration (`lenny_warmpool_pod_startup_duration_seconds`, by pool, by isolation profile — time from pod creation to `idle` state)                                                                           | Histogram       |
| Replenishment rate (`lenny_warmpool_replenishment_rate`, pods/min entering `idle` state, by pool)                                                                                                                      | Gauge           |
| Cold-start sessions served (`lenny_warmpool_cold_start_total`, by pool — increments when a session is served from a cold pod in a `minWarm: 0` pool or when the warm pool is exhausted and a pod is created on demand) | Counter         |
| Cold-start pool fill duration (`lenny_warmpool_fill_duration_seconds`, by pool — time from pool creation to reaching `minWarm` ready pods; see Section 4.6.1)                                                          | Histogram       |
| **Pool Config Reconciliation**                                                                                                                                                                                         |                 |
| Pool config reconciliation lag (`lenny_pool_config_reconciliation_lag_seconds`, by pool — time since last successful CRD reconciliation from Postgres; see Section 4.6.2)                                              | Gauge           |
| **Experiment Targeting**                                                                                                                                                                                               |                 |
| Experiment targeting webhook latency (`lenny_experiment_targeting_webhook_duration_seconds`, by `provider`)                                                                                                             | Histogram       |
| Experiment targeting webhook failures (`lenny_experiment_targeting_webhook_error_total`, by `provider`, `error_type`)                                                                                                   | Counter         |

These four templated metrics fulfill the "per-subsystem metrics" contract from Section 4.1. The subsystem-specific extraction-threshold metrics defined in Section 4.1's extraction readiness table (`lenny_stream_proxy_queue_depth`, `lenny_upload_handler_active_uploads`, `lenny_mcp_fabric_active_delegations`, `lenny_llm_proxy_active_connections`, etc.) are also canonical and must be instrumented; they provide finer-grained signals for the extraction decision described in Section 4.1.

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
| `credential.assign`          | Gateway (credential service)          |
| `credential.rotate`          | Gateway (credential service)          |
| `credential.fallback_chain`  | Gateway (credential service)          |
| `credential.proxy_request`   | Gateway (LLM proxy)                   |
| `session.checkpoint`         | Gateway + Pod                         |
| `session.seal_and_export`    | Gateway + Pod                         |

**Sampling and backend:** Head-based sampling at a default rate of 10% for normal operations (configurable via `global.traceSamplingRate` Helm value). 100% sampling is applied for errors (any span with error status), slow requests (session creation exceeding P99 latency), and delegation trees (all spans in a tree are sampled if the root is sampled, preserving trace completeness). The trace pipeline uses OpenTelemetry Collector; the platform emits OTLP traces and the collector handles sampling, batching, and export. The backend is deployer-configured — Jaeger, Tempo, Zipkin, or cloud-native options (Cloud Trace, X-Ray) are all supported. In dev mode, 100% sampling is enabled with a local Jaeger instance (or stdout exporter for `make run`).

**Error codes:** Structured error taxonomy with categories:

- `TRANSIENT` — retryable (pod crash, network timeout)
- `PERMANENT` — not retryable (invalid workspace, policy denial)
- `POLICY` — denied by policy engine (quota exceeded, unauthorized runtime, `CREDENTIAL_POOL_EXHAUSTED`)
- `UPSTREAM` — external dependency failure (MCP tool error, auth failure)

### 16.4 Logging

- Structured JSON logs from gateway, token service, pool controller, and runtime adapter
- Correlation fields in every log line: `session_id`, `tenant_id`, `trace_id`, `span_id`
- Setup command stdout/stderr captured and stored in EventStore
- Audit events for all policy decisions
- Error events include structured error codes (TRANSIENT/PERMANENT/POLICY/UPSTREAM)
- **Credential-sensitive RPCs** (`AssignCredentials`, `RotateCredentials`) are excluded from payload-level logging, gRPC access logs, and OTel trace span attributes. Only RPC name, lease ID, provider type, and outcome are recorded.

**Log retention and EventStore management.** EventStore tables (audit events, session logs, stream cursors) are partitioned by time using native Postgres range partitioning. A background job drops partitions beyond the retention window: 365 days for audit events (default), 30 days for session logs, 7 days for stream cursors. Estimated volume is ~10 KB per session for audit events and ~50 KB for logs; at 10K sessions/day this is ~600 MB/day before retention cleanup. This estimate corresponds to Tier 3 daily throughput; see Section 17.8 for per-tier volume estimates. Deployers should configure an external log aggregation stack (ELK, Loki, CloudWatch, etc.) for long-term retention beyond the Postgres window.

**Compliance-aware audit retention.** The default 365-day Postgres retention satisfies SOC2 (12-month audit evidence window) and FedRAMP Moderate (1-year AU-11). Deployers subject to stricter frameworks should set `audit.retentionDays` using named presets or a custom value:

| Preset           | Retention           | Frameworks                       |
| ---------------- | ------------------- | -------------------------------- |
| `soc2` (default) | 365 days            | SOC2, FedRAMP Low/Moderate       |
| `fedramp-high`   | 1095 days (3 years) | FedRAMP High                     |
| `hipaa`          | 2190 days (6 years) | HIPAA §164.312(b)                |
| `nis2-dora`      | 1825 days (5 years) | EU NIS2, DORA (financial sector) |
| `custom`         | Deployer-specified  | Any                              |

Helm configuration: `audit.retentionPreset: soc2` or `audit.retentionDays: 2190`. The preset drives the Postgres partition retention policy automatically.

**SIEM responsibility split.** When an external SIEM is configured (see Section 11.7), the SIEM is the system of record for long-term regulatory retention. The Postgres audit partition serves as a queryable hot tier for operational use. In this configuration, the Postgres retention period may be shorter than the regulatory requirement — the SIEM satisfies the long-term obligation. If `audit.retentionDays` is set below 365 and `LENNY_ENV=production`, the gateway emits an `AuditRetentionLow` warning at startup. This warning is suppressed if `audit.siem.endpoint` is configured, since the SIEM provides the durable long-term copy.

### 16.5 Alerting Rules and SLOs

**Critical alerts (page):**

| Alert                            | Condition                                                                                                                                                                                                                                                                                                   | Severity |
| -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `WarmPoolExhausted`              | Available warm pods = 0 for any pool for > 60s                                                                                                                                                                                                                                                              | Critical |
| `PostgresReplicationLag`         | Sync replica lag > 1s for > 30s                                                                                                                                                                                                                                                                             | Critical |
| `GatewayNoHealthyReplicas`       | Healthy gateway replicas below tier minimum (see Section 17.8) for > 30s                                                                                                                                                                                                                                    | Critical |
| `SessionStoreUnavailable`        | Postgres primary unreachable for > 15s                                                                                                                                                                                                                                                                      | Critical |
| `CheckpointStorageUnavailable`   | Checkpoint upload to MinIO failed after all retries during eviction; Postgres minimal state fallback was attempted                                                                                                                                                                                          | Critical |
| `EtcdUnavailable`                | API server etcd connectivity errors sustained > 15s                                                                                                                                                                                                                                                         | Critical |
| `CredentialPoolExhausted`        | Any credential pool has 0 assignable credentials (all credentials exhausted, in cooldown, or revoked) for > 30s; new session creation will return `CREDENTIAL_POOL_EXHAUSTED` for this pool                                                                                                                 | Critical |
| `CredentialCompromised`          | A credential in `revoked` state has active leases still alive against it for > 30s, indicating revocation propagation failure — the compromised key may still be in use                                                                                                                                     | Critical |
| `TokenServiceUnavailable`        | Token Service circuit breaker in open state for > 30s; new sessions requiring credentials will fail (existing sessions unaffected until lease expiry)                                                                                                                                                       | Critical |
| `ControllerLeaderElectionFailed` | Any controller's Lease (`lenny-warm-pool-controller` or `lenny-pool-scaling-controller`) has not been renewed within `leaseDuration` (15s); failover is imminent or in progress. Auto-resolves when a new leader acquires the lease. Tracked via `lenny_controller_leader_lease_renewal_age_seconds` gauge. | Critical |

**Warning alerts:**

| Alert                            | Condition                                                                                                                                                                                                                                    | Severity |
| -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `WarmPoolLow`                    | Available warm pods < 25% of `minWarm` for any pool                                                                                                                                                                                          | Warning  |
| `RedisMemoryHigh`                | Redis memory > 80% of maxmemory                                                                                                                                                                                                              | Warning  |
| `CredentialPoolLow`              | Available credentials < 20% of pool size                                                                                                                                                                                                     | Warning  |
| `GatewayActiveStreamsHigh`       | Active streams per replica > 80% of configured max                                                                                                                                                                                           | Warning  |
| `ArtifactGCBacklog`              | Expired artifacts pending cleanup exceeds tier-dependent threshold (Section 17.8)                                                                                                                                                            | Warning  |
| `CheckpointStale`                | Any active session has `lenny_session_last_checkpoint_age_seconds` > `periodicCheckpointIntervalSeconds` (default: 600s); session is at elevated risk of workspace loss on eviction                                                          | Warning  |
| `RateLimitDegraded`              | Rate limiting in fail-open mode                                                                                                                                                                                                              | Warning  |
| `CertExpiryImminent`             | mTLS cert expiry < 1h (should auto-renew, so this indicates cert-manager failure)                                                                                                                                                            | Warning  |
| `ElicitationBacklogHigh`         | Pending elicitation requests > 50 for > 30s                                                                                                                                                                                                  | Warning  |
| `DelegationBudgetNearExhaustion` | Delegation budget utilization ratio > 90% for any active delegation tree                                                                                                                                                                     | Warning  |
| `DelegationBudgetIrrecoverable`  | Any delegation tree moved to `awaiting_client_action` due to `BUDGET_STATE_UNRECOVERABLE` during Redis recovery                                                                                                                              | Warning  |
| `AuditSIEMNotConfigured`         | `LENNY_ENV=production` and `audit.siem.endpoint` is not set; fires at startup and persists until endpoint is configured. Informational — SIEM is optional.                                                                                   | Warning  |
| `AuditChainGap`                  | Startup chain-continuity check detected a broken hash chain (gap in audit sequence) for one or more tenants. Indicates buffered events lost during a prior crash. Operators should replay from SIEM or document the gap.                     | Warning  |
| `CheckpointStorageHigh`          | Per-tenant checkpoint storage (`lenny_checkpoint_storage_bytes_total`) exceeds deployer-configurable threshold (default: 80% of tenant's storage quota)                                                                                      | Warning  |
| `PodClaimQueueSaturated`         | `lenny_pod_claim_queue_depth` exceeds 50% of the pool's `maxConcurrent` session rate for > 30s; indicates claim queue is backing up even though warm pods may exist                                                                          | Warning  |
| `GatewaySubsystemCircuitOpen`    | Any gateway subsystem circuit breaker (`lenny_gateway_{subsystem}_circuit_state`) in open state (value = 2) for > 60s; affected subsystem is rejecting requests while other subsystems continue serving                                      | Warning  |
| `PoolConfigDrift`                | Any pool's Postgres `pool_config_generation` differs from its CRD `lenny.dev/config-generation` annotation for > 60s; PoolScalingController reconciliation is stalled or the controller is down. Detected by the gateway. See Section 4.6.2. | Warning  |
| `WarmPoolReplenishmentSlow`      | P95 of `lenny_warmpool_pod_startup_duration_seconds` exceeds 2× the pool's configured `pod_warmup_seconds` baseline for > 5 min; pool refill is slower than expected and `WarmPoolLow` may not self-correct                                  | Warning  |
| `ErasureJobOverdue`              | A user-level erasure job has exceeded its tier-specific deadline (72h for T3, 1h for T4) without completing. Requires operator investigation. See Section 12.8.                                                                              | Warning  |
| `DataResidencyViolationAttempt`  | A `StorageRouter` write or pod pool delegation was rejected because the resolved region did not match the session's `dataResidencyRegion`. Indicates a misconfiguration or code-path bypass. See Section 12.8.                               | Warning  |
| `BillingCorrectionRateHigh`      | Billing correction events exceed deployer-configurable percentage (default: 5%) of total billing events in a rolling 24h window. May indicate compromised credentials, operational error, or systematic metering bug. See Section 11.2.1.    | Warning  |
| `AuditRetentionLow`              | `LENNY_ENV=production` and `audit.retentionDays` is below 365 days without a configured SIEM (`audit.siem.endpoint`). Fires at startup. Suppressed when SIEM is configured (SIEM provides long-term regulatory retention). See Section 16.4. | Warning  |

**Capacity tiers:**

The following tiers define the scale points at which SLOs and alerting rules must hold. Infrastructure sizing for each tier is detailed in Section 17.8.

| Parameter                         | Tier 1 (Starter) | Tier 2 (Growth) | Tier 3 (Scale) |
| --------------------------------- | ---------------- | --------------- | -------------- |
| Max concurrent sessions           | 100              | 1,000           | 10,000         |
| Session creation rate (sustained) | 5/s              | 30/s            | 200/s          |
| Gateway RPS (all endpoints)       | 500              | 5,000           | 50,000         |
| Delegation fan-out (concurrent)   | 10               | 100             | 500            |
| Active tenants                    | 5                | 50              | 500            |
| LLM proxy concurrent streams      | 50               | 500             | 5,000          |

**Design-time scale target:** All architectural decisions (queue depths, connection pool sizes, controller reconciliation intervals, warm pool sizing) must be validated against **Tier 2** as the minimum. Tier 3 must be achievable with horizontal scaling only (no architectural changes). Tier 1 exists for single-node development and CI environments.

**Benchmark requirement:** Before GA, load tests must demonstrate that all SLOs below hold at sustained Tier 2 load, and that Tier 3 is reachable with linear horizontal scaling of gateway, controllers, and data stores. Phase 13.5 establishes pre-hardening baselines; Phase 14.5 re-validates all SLOs with full security hardening active — Phase 14.5 is the final GA gate for SLO compliance.

**SLO targets (operator-configurable baselines, validated at Tier 2 scale):**

| SLO                                     | Target    | Measurement                                                  |
| --------------------------------------- | --------- | ------------------------------------------------------------ |
| Session creation success rate           | 99.5%     | Successful session starts / total attempts (30d window)      |
| Time to first token                     | P95 < 10s | From session start request to first streaming event          |
| Session availability                    | 99.9%     | Uptime of sessions not in retry/recovery state               |
| Gateway availability                    | 99.95%    | Healthy replicas serving requests                            |
| Startup latency (pod-warm, runc)        | P95 < 2s  | Pod claim through agent session ready, excluding file upload |
| Startup latency (pod-warm, gVisor)      | P95 < 5s  | Pod claim through agent session ready, excluding file upload |
| Checkpoint duration (≤ 100MB workspace) | P95 < 2s  | Quiescence request through snapshot upload complete          |

---

## 17. Deployment Topology

### 17.1 Kubernetes Resources

| Component               | K8s Resource                              | Notes                                                                                                                                                      |
| ----------------------- | ----------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Gateway                 | Deployment + Service + Ingress            | HPA, PDB, multi-zone, topology spread                                                                                                                      |
| Token/Connector Service | Deployment + Service + PDB                | 2+ replicas, stateless; separate SA with KMS access; PDB `minAvailable: 1`                                                                                 |
| Warm Pool Controller    | Deployment (2+ replicas, leader election) | Manages pod lifecycle via `PoolManager` interface (default implementation: `kubernetes-sigs/agent-sandbox` CRDs)                                           |
| PoolScalingController   | Deployment (2+ replicas, leader election) | Reconciles pool config from Postgres into CRDs; manages scaling intelligence                                                                               |
| Agent Pods              | Pods owned by `Sandbox` CRD               | RuntimeClass per pool; preStop checkpoint hook for active pods; optional PDB per pool on warm (idle) pods to enforce `minWarm` during voluntary disruption |
| Postgres                | StatefulSet or managed service            | HA: primary + sync replica; connection pooling required (PgBouncer for self-managed, provider proxy for cloud-managed — see Section 17.9)                  |
| Redis                   | StatefulSet or managed service            | HA: Sentinel (3 nodes) for self-managed, managed cache service for cloud — see Section 17.9; TLS + AUTH required                                           |
| MinIO                   | StatefulSet or managed service            | Artifact/checkpoint storage; S3/GCS/Azure Blob for cloud-managed — see Section 17.9                                                                        |

### 17.2 Namespace Layout

```
lenny-system/         # Gateway, token service, controller, stores
lenny-agents/         # Agent pods (gVisor/runc isolation boundary)
lenny-agents-kata/    # Kata pods (separate node pool with dedicated hardware)
```

**Pod Security Standards:** The `lenny-agents` and `lenny-agents-kata` namespaces use a **split enforcement model** based on RuntimeClass:

- **runc (`standard`) pods:** Full Restricted PSS compliance is **enforced** via RuntimeClass-aware admission policies (OPA/Gatekeeper or Kyverno). The `seccompType: RuntimeDefault` requirement is meaningful for runc (the host kernel seccomp filter is active), and all controller-generated runc pods already satisfy Restricted PSS constraints (non-root, all caps dropped, read-only rootfs). The admission policy rejects any runc pod that does not meet Restricted PSS, ensuring non-compliant pods fail at admission rather than silently running with weaker security.
- **gVisor and Kata pods:** The admission policies apply **relaxed, RuntimeClass-specific** constraints. Restricted PSS `enforce` is unsuitable because its `seccompType: RuntimeDefault` requirement is a no-op under gVisor (gVisor intercepts syscalls in userspace, making the host seccomp profile meaningless) and conflicts with some Kata device plugins that require relaxed `allowPrivilegeEscalation` constraints. With namespace-level PSS `enforce`, non-compliant pods are silently rejected by the API server, which would cause warm pool deadlock: the controller observes a missing pod, recreates it, and the replacement is rejected again in a tight loop. Instead, gVisor pods skip the seccomp profile check while still requiring non-root, all-caps-dropped, and read-only rootfs (the controls listed in Section 13.1). Kata pods permit the specific privilege escalation paths needed by their device plugins but enforce all other Restricted constraints.

This approach preserves the same security properties (non-root UID, all capabilities dropped, read-only root filesystem, gateway-mediated file delivery) via admission policy controllers rather than the built-in PSS enforce mode, while applying the strictest possible constraints per RuntimeClass.

**Admission policy manifests** (OPA/Gatekeeper ConstraintTemplates or Kyverno ClusterPolicies) are included in the Helm chart under `templates/admission-policies/` and deployed as part of the chart install. These policies include: (1) full Restricted PSS enforcement for runc pods, (2) RuntimeClass-specific relaxed enforcement for gVisor and Kata pods, (3) a `shareProcessNamespace: false` validation policy that rejects pods in agent namespaces with `shareProcessNamespace: true` (see Section 13.1), and (4) label-based namespace targeting via `.Values.agentNamespaces`. An **integration test suite** (`tests/integration/admission_policy_test.go`) verifies that controller-generated pod specs for each RuntimeClass pass the deployed admission policies, preventing policy/spec drift from causing warm pool deadlock.

Namespace-level PSS labels remain at `warn` + `audit` (not `enforce`) because PSS enforcement is namespace-scoped and cannot distinguish RuntimeClasses — enforcement is handled by the RuntimeClass-aware admission policies above:

```
pod-security.kubernetes.io/warn: restricted
pod-security.kubernetes.io/audit: restricted
```

**Node isolation:** Kata (`microvm`) pods **must** run on dedicated node pools and **must** use hard scheduling constraints — not merely taints/tolerations — to guarantee they never share nodes with `standard` (runc) pods. A kernel compromise via an runc escape on a shared node would put co-located Kata pods at risk. The following controls are required:

1. **RuntimeClass `nodeSelector`:** The `kata-microvm` RuntimeClass definition **must** include `scheduling.nodeSelector` (e.g., `lenny.dev/node-pool: kata`). Any pod that references this RuntimeClass is automatically constrained to matching nodes at admission time, with no additional pod-level configuration needed.
2. **Hard node affinity:** As a defense-in-depth measure, the controller **must** inject a `requiredDuringSchedulingIgnoredDuringExecution` node affinity rule on every Kata pod, matching the same `lenny.dev/node-pool: kata` label. This ensures scheduling fails rather than falling back to an unsuitable node. Note: `IgnoredDuringExecution` is the strongest node affinity semantic Kubernetes offers — there is no `RequiredDuringExecution` variant. If a node's label is removed after scheduling, the pod is not evicted. However, the dedicated-node taint (control 3 below) independently prevents non-Kata workloads from scheduling onto Kata nodes regardless of label state, so label drift alone cannot cause Kata and runc pods to share a node.
3. **Dedicated-node taint:** Kata node pools **must** carry the taint `lenny.dev/isolation=kata:NoSchedule`. Only pods with the corresponding toleration (added automatically by the RuntimeClass or controller) can schedule onto these nodes, preventing non-Kata workloads from landing on Kata-dedicated hardware.

**Resource governance:** Each agent namespace (`lenny-agents`, `lenny-agents-kata`) includes a `ResourceQuota` and a `LimitRange` deployed by the Helm chart. These prevent runaway pod creation (e.g., from a controller bug or misconfigured `minWarm`) from exhausting cluster resources.

- **ResourceQuota** (configurable via `.Values.agentNamespaces[].resourceQuota`): caps total pods, aggregate CPU requests, and aggregate memory requests per namespace. Default values are derived from the expected warm pool size for the namespace's tier (see Section 17.8) with a safety margin (2x the maximum pool size). Example defaults for the `lenny-agents` namespace: `pods: 200`, `requests.cpu: "400"`, `requests.memory: "800Gi"`. Operators **must** tune these values when configuring large `minWarm` pools — if the quota is lower than the pool's target size, the warm pool controller will be unable to create pods and will emit `pool_quota_exhausted` warning events.
- **LimitRange** (configurable via `.Values.agentNamespaces[].limitRange`): sets default resource requests and limits for containers in agent pods, ensuring no pod is scheduled as BestEffort QoS class. Default container values: `defaultRequest.cpu: "250m"`, `defaultRequest.memory: "256Mi"`, `default.cpu: "2"`, `default.memory: "2Gi"`. These defaults apply only to containers that do not specify their own resource requirements — controller-generated pods already include explicit resource requests (Section 17.8), so the LimitRange acts as a safety net for any manually created or misconfigured pods.

The `lenny-preflight` Job validates that both `ResourceQuota` and `LimitRange` exist in each agent namespace and that the quota's pod limit is at least as large as the sum of `minWarm` across all pools targeting that namespace. If the check fails, the preflight Job reports: `"ResourceQuota in namespace '<ns>' allows <n> pods but configured pools require at least <m>; increase agentNamespaces[].resourceQuota.pods"`.

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

- Gateway: single replica, no HPA, no mTLS (plain HTTP by default)
- Controller simulator: manages a single "pod" (Docker container) instead of CRDs
- Stores: real Postgres + Redis (lightweight containers)
- MinIO: single container for artifact storage
- Agent pod: single Docker container with runtime adapter + agent binary

> **Warning — plain HTTP and real credentials.** The default Tier 2 profile transmits all traffic — including LLM provider API keys injected via credential pools — over plain HTTP between the gateway and agent containers. **Do not configure real LLM credentials in docker-compose unless TLS is enabled** (see credential-testing profile below). This applies to both interactive development and CI environments that run integration tests with real API keys.

Suitable for:

- Lenny core developers iterating on gateway/controller logic
- Integration testing against real storage backends
- CI integration tests
- Production-like local environment validation

#### Credential-testing profile

When testing with real LLM provider credentials locally, use the `credentials` docker-compose profile, which enables TLS by default:

```
docker compose --profile credentials up   # Enables LENNY_DEV_TLS=true automatically
```

This profile sets `LENNY_DEV_TLS=true` (which requires `LENNY_DEV_MODE=true`, already set in all dev profiles) and generates self-signed mTLS certificates on first run. All gateway-to-agent traffic is encrypted. Use this profile whenever real API keys are configured in credential pool definitions.

**Self-signed certificate trust setup.** When `LENNY_DEV_TLS=true` is active, the gateway generates a self-signed CA and leaf certificates in `./lenny-data/certs/`. To allow API clients (CLI tools, test harnesses, CI scripts) to verify the gateway's TLS certificate:

1. **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ./lenny-data/certs/ca.crt`
2. **Linux:** `sudo cp ./lenny-data/certs/ca.crt /usr/local/share/ca-certificates/lenny-dev-ca.crt && sudo update-ca-certificates`
3. **Per-process (any OS):** Set `SSL_CERT_FILE=./lenny-data/certs/ca.crt` or use `--cacert ./lenny-data/certs/ca.crt` with curl.
4. **CI environments:** Option 3 (per-process) is recommended to avoid modifying system trust stores in CI runners.

The certificates are regenerated if deleted; no manual key management is required.

#### Observability in dev mode

Tier 2 (`docker compose up`) includes optional observability containers: Prometheus (metrics scraping), Grafana (pre-built Lenny dashboard), and Jaeger (distributed tracing). Enable with `docker compose --profile observability up`. Tier 1 (`make run`) outputs traces to stdout and exposes Prometheus metrics on `:9090/metrics`.

#### Zero-credential mode

In both tiers, the gateway can operate without LLM provider credentials by using a **built-in echo/mock agent runtime** that does not require an LLM provider. The echo runtime replays deterministic responses, allowing contributors to test platform mechanics (session lifecycle, workspace materialization) without providing any API keys. This is the default runtime in Tier 1 and can be selected explicitly in Tier 2 via `LENNY_AGENT_RUNTIME=echo`. Note: the echo runtime cannot invoke MCP tools; delegation flow testing requires the `delegation-echo` test runtime introduced in Phase 9 (Section 18), which executes scripted tool call sequences including `lenny/delegate_task`.

#### Dev mode guard rails

Dev mode relaxes security defaults (TLS, JWT signing) for local convenience, but hard guard rails prevent accidental use outside development:

1. **Hard startup assertion:** The gateway **refuses to start** with TLS disabled unless the environment variable `LENNY_DEV_MODE=true` is explicitly set. Any other value, or absence of the variable, causes an immediate fatal error at startup. This ensures a misconfigured staging or production deployment cannot silently run without encryption.
2. **Prominent startup warning:** When `LENNY_DEV_MODE=true` is set, the gateway logs at `WARN` level on every startup: `"WARNING: TLS disabled — dev mode active. Do not use in production."` The warning is repeated every 60 seconds while the process is running.
3. **Unified security-relaxation gate:** The `LENNY_DEV_MODE` flag is the single gate for all security relaxations in dev mode, including TLS bypass, JWT signing bypass, and any future relaxations. No individual security feature can be disabled independently without this flag.

Setting `LENNY_DEV_TLS=true` (requires `LENNY_DEV_MODE=true`) enables self-signed mTLS certificates that are auto-generated on first run. This is **required when testing with real LLM credentials** in Tier 2 (use the `credentials` docker-compose profile, which sets this automatically) and is also useful for adapter authors testing certificate validation, rotation, and error handling without a full cert-manager setup. See "Credential-testing profile" and "Self-signed certificate trust setup" above for details.

#### Smoke test

Both dev mode tiers include a built-in smoke test: `make test-smoke` (Tier 1) or `docker compose run smoke-test` (Tier 2) creates a session with the echo runtime, sends a prompt, verifies a response, and exits. This validates the entire pipeline (gateway, controller-sim, runtime adapter, agent binary) in under 10 seconds.

### 17.5 Cloud Portability

The design avoids baking in cloud-specific assumptions:

- Storage backends are pluggable
- Network policies are standard Kubernetes
- RuntimeClass works with any conformant runtime
- No cloud-specific CRDs required

### 17.6 Packaging and Installation

**Helm chart** is the primary installation mechanism. The chart packages all Lenny components: gateway, token service, warm pool controller, CRD definitions, RBAC, NetworkPolicies, admission policies (OPA/Gatekeeper or Kyverno manifests per Section 17.2), and cert-manager resources.

Key Helm values:

- `global.devMode` — enables `LENNY_DEV_MODE` for local development
- `gateway.replicas` — gateway replica count
- `pools` — array of warm pool configurations (runtime, size, resource limits)
- `agentNamespaces[].resourceQuota` — per-namespace ResourceQuota overrides (pods, CPU, memory caps)
- `agentNamespaces[].limitRange` — per-namespace LimitRange overrides (default container requests/limits)
- `postgres.connectionString` — Postgres DSN
- `redis.connectionString` — Redis DSN
- `minio.endpoint` — object storage endpoint

CRDs are installed via the chart on initial `helm install` but can be managed separately for GitOps workflows (`helm install --skip-crds` combined with external CRD management).

**CRD upgrade procedure (required — read this before every upgrade).** Helm does not update CRDs on `helm upgrade`. This is a known Helm limitation that causes silent production incidents if CRDs become stale (new fields are stripped by the API server, controllers observe unexpected defaults). **Every Lenny upgrade requires CRD application as a separate step.** The required upgrade sequence is:

1. **Apply CRDs first:** `kubectl apply -f charts/lenny/crds/` (or the equivalent from the release tarball). This updates the CRD schemas in the cluster before any controller code changes.
2. **Run `helm upgrade`:** Proceed with the normal Helm upgrade. Controllers validate the installed CRD schema version on startup (see Section 10.5) and will refuse to start if CRDs are stale.
3. **GitOps workflows:** When using ArgoCD or Flux, configure CRD manifests as a separate sync wave (e.g., ArgoCD `sync-wave: "-5"`) that applies before the main chart resources. **This is not optional** — without a separate sync wave, ArgoCD/Flux will apply controllers and CRDs in arbitrary order, which can produce the same stale-CRD failure.

The `lenny-preflight` Job (above) includes a CRD version check: it compares the `lenny.dev/schema-version` annotation on each installed CRD against the expected version for the chart release. If any CRD is stale, the preflight Job fails with: `"CRD '<name>' schema version is '<installed>'; expected '<expected>'. Apply updated CRDs before running helm upgrade."`

**Helm post-upgrade CRD validation hook.** As a defense-in-depth measure, the Helm chart includes a `lenny-crd-validate` Job (`helm.sh/hook: post-upgrade`, `helm.sh/hook-weight: "-5"`) that runs after `helm upgrade` completes. The Job verifies that every Lenny CRD in the cluster carries the expected `lenny.dev/schema-version` annotation for the chart release. If any CRD is stale, the Job fails with a clear message: `"Post-upgrade CRD validation failed: CRD '<name>' has schema version '<installed>', expected '<expected>'. Run: kubectl apply -f charts/lenny/crds/ && kubectl rollout restart deployment -l app.kubernetes.io/part-of=lenny -n <namespace>. See docs/runbooks/crd-upgrade.md."` This catch-net ensures that even if preflight was skipped (`preflight.enabled: false`) or the operator applied CRDs from an incorrect release, the failure is surfaced immediately rather than manifesting as silent data loss.

**Recovery procedure for stale CRDs after a failed upgrade.** If `helm upgrade` was run without applying CRDs first, or with CRDs from the wrong release version, the following recovery procedure applies:

1. **Symptom identification.** One or more of: (a) controller pods are in `CrashLoopBackOff` with `FATAL: CRD schema version mismatch` in logs, (b) the `lenny-preflight` Job failed with a CRD version mismatch message, (c) the `lenny-crd-validate` post-upgrade hook failed.
2. **Apply the correct CRDs.** Identify the exact chart version that was deployed (`helm list -n <namespace>` shows the chart version). Apply CRDs from that release: `kubectl apply -f charts/lenny/crds/` (from the matching release tarball or git tag). Verify with: `kubectl get crd agentsessions.lenny.dev -o jsonpath='{.metadata.annotations.lenny\.dev/schema-version}'`.
3. **Restart affected controllers.** After CRDs are applied, restart the controllers so they pick up the corrected CRD schemas: `kubectl rollout restart deployment -l app.kubernetes.io/part-of=lenny -n <namespace>`. Wait for rollout to complete: `kubectl rollout status deployment -l app.kubernetes.io/part-of=lenny -n <namespace> --timeout=120s`.
4. **Verify recovery.** Confirm controllers are running: `kubectl get pods -l app.kubernetes.io/part-of=lenny -n <namespace>`. Confirm no CRD mismatch errors in logs: `kubectl logs -l app.kubernetes.io/part-of=lenny -n <namespace> --tail=50 | grep -i "schema version"`. Run the preflight check manually: `lenny-ctl preflight --config <values.yaml>`.
5. **If recovery fails.** If controllers still fail after applying CRDs and restarting, roll back the entire Helm release: `helm rollback <release> <previous-revision> -n <namespace>`, then apply CRDs for the previous version and coordinate a fresh upgrade attempt following the correct procedure above.

**Bootstrap seed mechanism.** After `helm install`, Postgres is empty — no runtimes, pools, tenants, or credentials exist. Lenny provides an idempotent bootstrap mechanism to seed Day-1 configuration:

1. **Helm values: `bootstrap` section.** The chart includes a `bootstrap` values block defining seed resources:

```yaml
bootstrap:
  enabled: true # default: true
  # Seed resources — all optional, all idempotent (upsert by name).
  tenant:
    name: "default"
    displayName: "Default Tenant"
  runtimes: [] # array of Runtime definitions (same schema as POST /v1/admin/runtimes)
  pools: [] # array of Pool definitions (same schema as POST /v1/admin/pools)
  credentialPools: [] # array of CredentialPool definitions (tenant-scoped; assigned to the bootstrap tenant above)
  delegationPolicies: [] # array of DelegationPolicy definitions
  environments: [] # array of Environment definitions
```

2. **Init Job: `lenny-bootstrap`.** The Helm chart includes a Kubernetes `Job` (with `helm.sh/hook: post-install,post-upgrade` and `helm.sh/hook-weight: "10"`) that runs after the gateway and database migrations are ready. The Job executes `lenny-ctl bootstrap --from-values /etc/lenny/bootstrap-values.yaml` against the admin API. The bootstrap values ConfigMap is rendered from the `bootstrap` Helm section.

3. **`lenny-ctl bootstrap` CLI command.** The CLI command reads a seed file (YAML, same schema as the Helm `bootstrap` section) and applies each resource via the admin API using upsert semantics (create if absent, skip or update if present with matching name). Behavior:
   - **Idempotent**: safe to run multiple times. Existing resources with matching names are left unchanged unless `--force-update` is passed, in which case they are updated to match the seed file.
   - **Dry-run**: `lenny-ctl bootstrap --dry-run` validates the seed file and reports what would be created/updated without making changes.
   - **Exit codes**: 0 = success (all resources seeded), 1 = validation error, 2 = partial failure (some resources failed, others succeeded — log details which).
   - **Waits for readiness**: the command polls `GET /healthz` on the gateway before applying seeds, with a configurable timeout (`--wait-timeout`, default 120s).

4. **What gets seeded.** The minimum Day-1 seed for a functional deployment:

   | Resource             | Purpose                                                      | Required?                                                 |
   | -------------------- | ------------------------------------------------------------ | --------------------------------------------------------- |
   | Default tenant       | Tenant for initial users                                     | Yes (one tenant required for any API call)                |
   | At least one Runtime | Defines an agent runtime (e.g., echo runtime for smoke test) | Yes (sessions require a runtime)                          |
   | At least one Pool    | Pre-warms pods for the registered runtime                    | Yes (sessions require warm pods)                          |
   | Credential pool      | LLM provider credentials                                     | No (only for real LLM providers; echo runtime needs none) |
   | Delegation policy    | Controls delegation behavior                                 | No (default-deny is safe)                                 |
   | Environment          | Groups runtimes for teams                                    | No (optional organizational construct)                    |

   The chart ships with a commented-out example seed configuration for a complete deployment with the echo runtime, one pool of 2 warm pods, and the default tenant.

5. **`lenny-ctl policy audit-isolation` CLI command.** Reports all `DelegationPolicy` rule × pool combinations where a delegation would be rejected at runtime due to an isolation monotonicity violation (Section 8.3). For each combination, the output lists the policy rule, the matching source pool (delegating parent), the matching target pool, and their respective isolation profiles. This is a read-only audit command — it does not modify any resources. Use it after registering new pools or runtimes to verify that delegation policies remain consistent with isolation requirements.

6. **Build sequence integration.** The bootstrap Job is part of Phase 4.5 (Admin API foundation) — it depends on the admin API endpoints being available.

**Preflight validation: `lenny-preflight` Job.** Missing or misconfigured infrastructure dependencies (wrong PgBouncer pool mode, absent CNI plugin, missing RuntimeClasses) cause cryptic failures that are difficult to diagnose after installation. The Helm chart includes a `lenny-preflight` Job (`helm.sh/hook: pre-install,pre-upgrade`, `helm.sh/hook-weight: "-10"`) that validates all infrastructure prerequisites before any Lenny component is deployed. The Job runs to completion and blocks the install/upgrade if any check fails.

**Checks performed:**

| Check                                 | Validation                                                                                                                                                                                                                                                                                   | Failure Message                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------- |
| Postgres connectivity                 | Connect to `postgres.connectionString`, execute `SELECT 1`                                                                                                                                                                                                                                   | `Postgres unreachable at <DSN>`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| Postgres version                      | Verify server version ≥ 14                                                                                                                                                                                                                                                                   | `Postgres version <ver> unsupported; minimum 14 required`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| PgBouncer pool mode                   | Query PgBouncer `SHOW CONFIG` and verify `pool_mode = transaction`                                                                                                                                                                                                                           | `PgBouncer pool_mode is '<mode>'; must be 'transaction' for RLS enforcement (Section 12.3)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| PgBouncer connect_query               | Verify `connect_query` contains `SET app.current_tenant` sentinel                                                                                                                                                                                                                            | `PgBouncer connect_query missing tenant sentinel; see Section 12.3`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| Cloud-managed pooler sentinel defense | When `postgres.connectionPooler = external`: connect to Postgres, verify the per-transaction tenant validation trigger exists on tenant-scoped tables (query `pg_trigger` for the `lenny_tenant_guard` trigger). If absent, fail.                                                            | `Cloud-managed pooler detected but per-transaction tenant validation trigger 'lenny_tenant_guard' not found; required because cloud-managed proxies cannot enforce the __unset__ sentinel via connect_query — see Section 12.3`                                                                                                                                                                                                                                                                                                                                          |
| Redis connectivity                    | Connect to `redis.connectionString`, execute `PING`                                                                                                                                                                                                                                          | `Redis unreachable at <DSN>`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Redis AUTH / TLS                      | Verify AUTH succeeds and TLS handshake completes                                                                                                                                                                                                                                             | `Redis AUTH or TLS failed; both are required (Section 12.4)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| MinIO connectivity                    | Connect to `minio.endpoint`, verify bucket access                                                                                                                                                                                                                                            | `MinIO unreachable at <endpoint>`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| MinIO encryption                      | Verify server-side encryption is enabled on the target bucket                                                                                                                                                                                                                                | `MinIO SSE not enabled; required for production (Section 12.5)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| RuntimeClasses                        | For each pool in `.Values.pools`, verify the referenced `RuntimeClass` exists in the cluster                                                                                                                                                                                                 | `RuntimeClass '<name>' not found; required by pool '<pool>'`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| cert-manager                          | Verify cert-manager CRDs (`certificates.cert-manager.io`) are installed and the configured `ClusterIssuer` is Ready                                                                                                                                                                          | `cert-manager not found or ClusterIssuer '<name>' not Ready`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| CNI NetworkPolicy support             | Create and delete a test `NetworkPolicy` in the target namespace to verify the CNI plugin supports NetworkPolicy enforcement                                                                                                                                                                 | `CNI plugin does not support NetworkPolicy; required for agent pod isolation (Section 13.4)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Namespace ResourceQuota               | Verify each agent namespace has a `ResourceQuota` and that the quota's pod limit ≥ sum of `minWarm` for pools targeting that namespace                                                                                                                                                       | `ResourceQuota in namespace '<ns>' allows <n> pods but configured pools require at least <m>; increase agentNamespaces[].resourceQuota.pods`                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Namespace LimitRange                  | Verify each agent namespace has a `LimitRange` with default container resource requests                                                                                                                                                                                                      | `LimitRange missing in namespace '<ns>'; required to prevent BestEffort pods (Section 17.2)`                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
| Kubernetes version                    | Verify server version ≥ 1.27 (minimum for required API features)                                                                                                                                                                                                                             | `Kubernetes version <ver> unsupported; minimum 1.27 required`                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| StorageRouter region coverage         | For each region declared in `storage.regions`, verify that Postgres, MinIO, and Redis endpoints are reachable and that at least one pool carries a matching `region` label. When `storage.regions` is non-empty, verify every tenant with a `dataResidencyRegion` maps to a declared region. | `StorageRouter region '<region>' has no reachable <backend> endpoint` / `Tenant '<name>' references dataResidencyRegion '<region>' which is not declared in storage.regions`                                                                                                                                                                                                                                                                                                                                                                                             |
| etcd Secret encryption (warning)      | When `LENNY_ENV=production`: emit a non-blocking preflight warning that etcd encryption at rest for Kubernetes Secrets cannot be verified programmatically by the preflight Job (it lacks etcd access).                                                                                      | `WARNING: Kubernetes does not encrypt Secrets in etcd by default. Credential pool API keys stored in Kubernetes Secrets (secretRef) are readable in plaintext from etcd snapshots or backups unless EncryptionConfiguration is enabled. Production deployments MUST configure EncryptionConfiguration with aescbc/aesgcm/kms for the 'secrets' resource type. Cloud-managed clusters: enable envelope encryption via AWS KMS (EKS), GCP Cloud KMS (GKE), or Azure Key Vault (AKS). Self-managed clusters: verify with 'etcdctl get /registry/secrets/lenny-system/<name> | hexdump'. See Section 4.9.` |
| Node disk encryption (warning)        | When `LENNY_ENV=production`: emit a non-blocking preflight warning that node-level disk encryption cannot be verified programmatically.                                                                                                                                                      | `WARNING: Node-level disk encryption (LUKS/dm-crypt or cloud-provider encrypted volumes) is required for production deployments (Section 6.4) but cannot be verified by Lenny — it depends on the underlying node pool configuration. Verify manually: AWS (EBS encryption on launch template), GCP (CMEK or default encryption on boot/scratch disks), Azure (SSE on managed disks). For T4 workloads, use Kata or gVisor isolation profiles with encrypted scratch volumes.`                                                                                           |
| SIEM endpoint (warning)               | When `LENNY_ENV=production` and `audit.siem.endpoint` is not set: emit a non-blocking preflight warning.                                                                                                                                                                                     | `WARNING: audit.siem.endpoint is not configured. Audit logs will be stored in Postgres only. A database superuser can bypass INSERT-only grants. This deployment does not meet compliance-grade audit integrity requirements (SOC2 CC7.2, FedRAMP AU-9, HIPAA §164.312(b)). Configure audit.siem.endpoint before using for regulated workloads (Section 11.7).`                                                                                                                                                                                                          |

**Behavior:**

- **Exit code 0:** All checks passed — Helm proceeds with installation.
- **Exit code 1:** One or more checks failed — Helm aborts. The Job logs each failed check with the failure message and a reference to the relevant spec section.
- **Warnings (non-blocking):** Checks that detect suboptimal but functional configurations (e.g., MinIO without erasure coding, Redis Sentinel with fewer than 3 sentinels, absent SIEM endpoint) log warnings but do not block installation.
- **`--skip-preflight`:** Deployers can disable preflight validation by setting `preflight.enabled: false` in Helm values. This is intended for air-gapped or constrained environments where the Job cannot reach all backends at install time. A warning is logged: `"Preflight validation skipped — infrastructure misconfigurations may cause runtime failures."`
- **Dev mode:** When `global.devMode: true`, the preflight Job skips checks for MinIO encryption, cert-manager, CNI NetworkPolicy support, and PgBouncer (since dev mode uses embedded stores). Only Postgres and Redis connectivity are validated in Tier 2; Tier 1 (`make run`) skips preflight entirely.
- **Timeout:** The Job has a `activeDeadlineSeconds: 120`. If infrastructure is slow to respond, the deployer can increase this via `preflight.timeoutSeconds` in Helm values.
- **Idempotent:** Safe to re-run on `helm upgrade` — all checks are read-only (except the ephemeral NetworkPolicy create/delete test, which cleans up after itself).

**CLI equivalent:** `lenny-ctl preflight --config <values.yaml>` runs the same checks outside of Helm for pre-deployment validation in CI pipelines or manual verification.

**Local dev:** A `docker-compose.yml` is provided as described in Section 17.4. The `make run` target automatically applies the bootstrap seed with the echo runtime and default tenant.

**GitOps:** The Helm chart supports `helm template` rendering for ArgoCD/Flux integration. For GitOps workflows, the bootstrap seed values are committed alongside other Helm values and applied on every sync (idempotent by design).

### 17.7 Operational Runbooks

Lenny must ship with operational runbooks for key failure scenarios as part of the documentation deliverables. The minimum required set:

- **CRD upgrade failure and recovery** (`docs/runbooks/crd-upgrade.md`) — stale CRD detection, recovery procedure, GitOps sync-wave configuration. See Section 17.6 for the detailed recovery steps.
- **Warm pool exhaustion** — diagnosis, emergency scaling, root cause analysis
- **Postgres failover** — verification, connection pool drain, health checks
- **Redis failure and recovery** — quota reconciliation, cache rebuild
- **Credential pool exhaustion** — diagnosis, emergency key addition
- **Gateway replica failure** — stream reconnection, session recovery
- **cert-manager outage** — warm pool impact, manual certificate issuance
- **MinIO failure** — artifact retrieval degradation, restore procedure
- **Emergency credential revocation** (`docs/runbooks/credential-revocation.md`) — compromised credential identification, revocation endpoint usage, propagation verification, replacement credential addition. See Section 4.9 for the detailed steps.
- **etcd operations** (`docs/runbooks/etcd-operations.md`) — operational procedures for etcd maintenance under sustained CRD write pressure. Covers: (1) defragmentation on a live cluster — single-member defrag procedure (follower-first, then leader), expected write pause duration, how to verify member health between defrags, and guidance for Tier 3 deployments where off-peak windows are narrow. (2) Compaction setting changes — rolling member restart procedure for self-managed clusters; escalation path for managed Kubernetes where etcd flags are provider-controlled. (3) Quota exhaustion recovery — full procedure for recovering from etcd alarm state: defrag all members, verify space reclaimed, `etcdctl alarm disarm`, verify API server write capability restored. (4) Escalation path — when etcd issues cannot be resolved at the Lenny operator level (managed K8s etcd unavailability, persistent quota growth despite defrag), escalation to the cloud provider or cluster admin team with the diagnostic information to collect (`etcd_disk_wal_fsync_duration_seconds`, `etcd_server_proposals_committed_total`, `etcd_debugging_mvcc_db_total_size_in_bytes`). Referenced by `EtcdQuotaNearLimit` and `EtcdUnavailable` alerts (Section 16.5).
- **Stuck finalizer remediation** (`docs/runbooks/stuck-finalizer.md`) — safe procedure for removing a stuck `lenny.dev/session-cleanup` finalizer after a `FinalizerStuck` alert fires. Steps:
  1. **Identify the stuck pod:** `kubectl get sandbox -A --field-selector=metadata.deletionTimestamp!='' | grep Terminating` — locate the `Sandbox` resource that has been in `Terminating` state beyond the 5-minute threshold.
  2. **Check for an active SandboxClaim:** `kubectl get sandboxclaim -A -o json | jq '.items[] | select(.spec.sandboxRef == "<pod-name>")'` — if a claim exists, the session is still logically active. Do **not** remove the finalizer until the claim is resolved.
  3. **Verify session state in Postgres:** Query `SELECT session_id, state, last_checkpoint_at FROM sessions WHERE pod_name = '<pod-name>'` — confirm the session is in a terminal state (`completed`, `failed`, `expired`) or has a recent checkpoint. If the session is still `active` with no checkpoint, coordinate with the gateway to trigger a checkpoint first (`DrainPod` with `checkpointFirst: true`).
  4. **Verify no in-flight artifacts:** Check that the pod's workspace artifacts have been uploaded to object storage: `mc ls lenny-artifacts/workspaces/<session-id>/` — if artifacts are missing and the session is active, do not proceed.
  5. **Remove the finalizer:** `kubectl patch sandbox <pod-name> -n <namespace> --type=json -p '[{"op":"remove","path":"/metadata/finalizers","value":["lenny.dev/session-cleanup"]}]'` — this allows Kubernetes to complete pod deletion.
  6. **Post-removal verification:** Confirm the pod is fully deleted: `kubectl get sandbox <pod-name> -n <namespace>` should return `NotFound`. Verify the warm pool has replenished: check `lenny_warmpool_idle_pods` metric returns to the expected `minWarm` level within 2 minutes. If replenishment does not occur, check WarmPoolController logs for reconciliation errors.
  7. **Root cause investigation:** Examine controller logs for the 5-minute window before the alert: `kubectl logs -l app=lenny-warm-pool-controller --since=10m | grep <pod-name>`. Common causes: controller leader election gap during pod termination, API server throttling on status updates (check `lenny_controller_api_throttle_total`), or a bug in the session cleanup path. File an incident if the stuck finalizer recurs on more than 2 pods within 24 hours.

Each runbook references the relevant alerts defined in Section 16.5. Runbooks are version-controlled alongside the platform code in `docs/runbooks/`.

> **Note:** The Lenny documentation targets three operator skill tiers: (1) **Developer** — uses `make run` or docker-compose, no K8s knowledge needed. (2) **Platform operator** — deploys via Helm, manages pools and scaling, follows runbooks for common issues. (3) **Cluster admin** — configures RuntimeClasses, node pools, network policies, and handles node drain/checkpoint integration. The Helm chart and documentation are structured to serve all three tiers, with progressive complexity.

### 17.8.1 Operational Defaults — Quick Reference

All tunable defaults collected in one place for operator convenience.

| Setting                           | Default                                                                  | Reference |
| --------------------------------- | ------------------------------------------------------------------------ | --------- |
| Artifact retention TTL            | 7 days                                                                   | §12.5     |
| Checkpoint retention              | Latest 2 per session                                                     | §12.5     |
| Periodic checkpoint interval      | 600 s (10 min)                                                           | §4.4      |
| GC cycle interval                 | 15 min                                                                   | §12.5     |
| Max session age                   | 7200 s (2 h)                                                             | §11.3     |
| Max idle time                     | 600 s                                                                    | §11.3     |
| Max resume window                 | 900 s                                                                    | §11.3     |
| Rate limit fail-open window       | 60 s                                                                     | §12.4     |
| Quota sync interval               | 30 s (min 10 s)                                                          | §11.2     |
| Audit event retention             | 365 days (override via `audit.retentionPreset` or `audit.retentionDays`) | §16.4     |
| Session log retention             | 30 days                                                                  | §16.4     |
| Pod cert TTL                      | 4 h                                                                      | §10.3     |
| Audit sync write pool size        | 4 connections                                                            | §12.3     |
| Audit startup chain check entries | 1000                                                                     | §12.3     |
| Session inbox max size            | 500 messages                                                             | §7.2      |
| DLQ max size                      | 500 messages                                                             | §7.2      |

All values are overridable via Helm values or the corresponding CRD field. See each referenced section for detailed semantics. For per-tier recommended values, see Section 17.8.

### 17.8.2 Capacity Tier Reference

This section provides per-tier sizing recommendations for all infrastructure components. These are starting points — production deployments should benchmark and adjust. See Section 16.5 for tier definitions.

**Gateway and API layer:**

| Parameter                                  | Tier 1                     | Tier 2                     | Tier 3                     |
| ------------------------------------------ | -------------------------- | -------------------------- | -------------------------- |
| Gateway replicas (min / max)               | 2 / 4                      | 3 / 10                     | 5 / 30                     |
| HPA target CPU utilization                 | 70%                        | 65%                        | 60%                        |
| HPA queue depth target (averageValue)      | 15                         | 10                         | 5                          |
| HPA scale-up stabilization window          | 0s                         | 0s                         | 0s                         |
| HPA scale-up max policy                    | 100% / 15s or 4 pods / 15s | 100% / 15s or 4 pods / 15s | 100% / 15s or 8 pods / 15s |
| HPA scale-down pods per period             | 1 / 60s                    | 1 / 60s                    | 3 / 60s                    |
| Stream Proxy maxConcurrent                 | 200                        | 2,000                      | 20,000                     |
| Upload Handler maxConcurrent               | 50                         | 500                        | 2,000                      |
| MCP Fabric maxConcurrent                   | 100                        | 1,000                      | 5,000                      |
| LLM Proxy maxConcurrent                    | 100                        | 1,000                      | 10,000                     |
| Gateway preStop drain timeout              | 60s                        | 60s                        | 120s                       |
| Gateway scale-down time (max→min replicas) | 2 min (4→2, 1 pod/60s)     | 7 min (10→3, 1 pod/60s)    | 8.3 min (30→5, 3 pods/60s) |
| Minimum healthy gateway replicas (alert)   | 2                          | 3                          | 5                          |

**Warm pool sizing:**

| Parameter                          | Tier 1 | Tier 2 | Tier 3 |
| ---------------------------------- | ------ | ------ | ------ |
| Expected claim rate                | 0.5/s  | 5/s    | 30/s   |
| Recommended minWarm (per hot pool) | 20     | 175    | 1050   |
| Hot pools                          | 1–2    | 3–5    | 5–10   |
| Pool safety factor                 | 1.5    | 1.5    | 1.2    |

Formula: `minWarm >= claim_rate * (failover_seconds + pod_startup_seconds) + burst_p99_claims * pod_warmup_seconds`. The first term covers sustained demand during failover; the burst term (see Section 4.6.2) reserves headroom for demand spikes that outpace pool refill. **Use `failover_seconds = 25` (worst-case crash scenario: `leaseDuration + renewDeadline = 15s + 10s`).** With 25s failover + 10s startup = 35s window; burst term adds headroom proportional to warmup latency and observed burst intensity. Note: clean shutdown (rolling update) reduces failover to near-zero via voluntary lease release, but sizing must cover the crash case.

**First deployment sizing.** The formulas in Section 4.6.2 and above require historical traffic metrics (`base_demand_p95`, `burst_p99_claims`, `pod_warmup_seconds`) that are not available at initial deployment. The PoolScalingController **cannot auto-configure `minWarm` for a new pool** because it has no demand signal — the initial value must always be set manually via the admin API or Helm values.

For first deployments, use the per-tier `minWarm` values in the table above as conservative starting points. These values assume: (a) `failover_seconds` of 25s (worst-case crash scenario), (b) `pod_startup_seconds` of 10s (container pull cached, runtime startup only), (c) the formula `claim_rate * (failover_seconds + pod_startup_seconds)` as the primary term, and (d) a single hot pool per runtime. If your deployment has multiple hot pools, divide the per-tier `minWarm` across pools proportionally to expected traffic share. If pod warmup in your environment is significantly longer (e.g., large container images, slow registry), increase `minWarm` proportionally.

**First-week monitoring workflow.** During the first week of operation, monitor the following metrics to determine whether the initial `minWarm` is appropriate:

- **`lenny_warmpool_idle_pods`** — if consistently near zero, `minWarm` is too low; increase by 50%.
- **`WarmPoolLow` alert** — if this fires repeatedly (more than twice per hour), `minWarm` is undersized for current demand.
- **`lenny_warmpool_idle_pod_minutes`** — if idle pod-minutes per hour exceeds `minWarm × 30` (i.e., most pods sit idle for half the hour), `minWarm` is oversized; reduce by 25%.
- **`lenny_warmpool_claim_wait_seconds_p99`** — if claim latency exceeds 2s, the pool is not keeping up with demand; increase `minWarm` or investigate pod startup times.

After 48–72 hours of production traffic, the PoolScalingController will have sufficient `base_demand_p95` and `burst_p99_claims` data to auto-scale. At that point, switch from the manual `minWarm` override to controller-managed scaling by removing the `minWarm` override from Helm values and confirming the controller's computed target is reasonable. Re-evaluate weekly for the first month.

**Controller tuning:**

| Parameter                                                 | Tier 1         | Tier 2                                  | Tier 3                                   |
| --------------------------------------------------------- | -------------- | --------------------------------------- | ---------------------------------------- |
| Pod creation rate limiter (QPS / burst)                   | 20 / 50        | 40 / 100                                | 80 / 200                                 |
| Status update rate limiter (QPS / burst)                  | 30 / 100       | 60 / 200                                | 120 / 400                                |
| Max concurrent reconciles (`--max-concurrent-reconciles`) | 1              | 5                                       | 15                                       |
| Work queue max depth                                      | 500            | 2,000                                   | 10,000                                   |
| Initial fill grace period                                 | 60s            | 90s                                     | 120s                                     |
| Controller replicas                                       | 2              | 2                                       | 3                                        |
| etcd compaction mode / retention                          | periodic / 5m  | periodic / 5m                           | periodic / 2m                            |
| etcd defrag schedule                                      | Daily off-peak | Daily off-peak                          | Every 12h off-peak                       |
| etcd quota-backend-bytes                                  | 4 GB           | 8 GB                                    | 8 GB (dedicated cluster recommended)     |
| etcd monitoring                                           | Standard       | Enhanced (write latency + quota alerts) | Dedicated etcd cluster with full metrics |

**Postgres and connection pooling** (self-managed profile — for cloud-managed equivalents see Section 17.9):

| Parameter                              | Tier 1        | Tier 2         | Tier 3                                            |
| -------------------------------------- | ------------- | -------------- | ------------------------------------------------- |
| Postgres instance class                | 2 vCPU / 4 GB | 4 vCPU / 16 GB | 8+ vCPU / 32+ GB                                  |
| Postgres max_connections               | 100           | 200            | 500                                               |
| PgBouncer replicas                     | 2             | 2              | 4                                                 |
| PgBouncer default_pool_size            | 25            | 50             | 60                                                |
| PgBouncer reserve_pool_size            | 5             | 10             | 15                                                |
| Read replicas                          | 0             | 0–1            | 1–2                                               |
| Estimated sustained write IOPS         | ~22/s         | ~220/s         | ~1,300/s                                          |
| Estimated burst write IOPS (3×)        | ~66/s         | ~660/s         | ~3,900/s                                          |
| Billing/audit batch flush interval     | 500ms / 250ms | 500ms / 250ms  | 250ms / 250ms                                     |
| Audit flush mode (production, no SIEM) | synchronous   | synchronous    | synchronous                                       |
| Separate billing/audit Postgres        | No            | No             | Optional (recommended if replication lag > 100ms) |

**Redis** (self-managed profile — for cloud-managed equivalents see Section 17.9):

| Parameter                   | Tier 1                                                  | Tier 2                                                           | Tier 3                                                                                            |
| --------------------------- | ------------------------------------------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| Topology                    | Sentinel (3 sentinels, 1+1)                             | Sentinel (3 sentinels, 1+1)                                      | Redis Cluster (6+ nodes)                                                                          |
| Memory per node             | 1 GB                                                    | 4 GB                                                             | 8 GB                                                                                              |
| Budget operations estimate  | ~1,000 ops/s                                            | ~10,000 ops/s                                                    | ~100,000 ops/s                                                                                    |
| Concern separation          | Single instance (all concerns)                          | Single instance; split if ceiling signals trigger (Section 12.4) | Separate instances: coordination (Sentinel), quota (Cluster), cache/pub-sub (Sentinel or Cluster) |
| Capacity ceiling monitoring | Basic (`redis_memory_used`, `redis_commands_processed`) | Enhanced (add P99 latency per store role, pub/sub channel count) | Per-instance dashboards with alerting on all ceiling signals (Section 12.4)                       |

**Object storage** (self-managed profile — for cloud-managed equivalents see Section 17.9):

| Parameter                         | Tier 1                            | Tier 2                      | Tier 3                       |
| --------------------------------- | --------------------------------- | --------------------------- | ---------------------------- |
| Topology                          | Single-node (dev) or 4-node MinIO | 4-node MinIO erasure coding | 8+ node MinIO erasure coding |
| GC cycle interval                 | 15 min                            | 15 min                      | 5 min                        |
| ArtifactGCBacklog alert threshold | 100                               | 1,000                       | 10,000                       |

**Operational defaults by tier:**

| Parameter                      | Tier 1   | Tier 2     | Tier 3      |
| ------------------------------ | -------- | ---------- | ----------- |
| Token Service replicas         | 2        | 2          | 4           |
| Rate limit fail-open window    | 60s      | 60s        | 30s         |
| Quota sync interval            | 30s      | 30s        | 10s         |
| Quota drift bound (worst case) | ~600 req | ~6,000 req | ~30,000 req |
| Log volume estimate (per day)  | ~30 MB   | ~300 MB    | ~3 GB       |
| Billing event storage (13 mo)  | ~1M rows | ~10M rows  | ~100M rows  |

### 17.9 Deployment Profiles

> **Note:** Prior to this section, Sections 17.8.1 (Operational Defaults) and 17.8.2 (Capacity Tier Reference) provide per-tier sizing and tunable defaults.

Lenny's data store layer (Postgres, Redis, object storage) is accessed exclusively through pluggable interfaces (Section 12.6). The implementation behind each interface varies by deployment environment. This section defines two deployment profiles — **cloud-managed** and **self-managed** — so that Lenny takes advantage of cloud-provider redundancy and scalability when available, and falls back to self-managed components when deployed outside major cloud environments.

**Design principle:** Lenny must not depend on any single cloud provider. Cloud-managed profiles use provider-native services for operational simplicity, but the self-managed profile is always a fully supported first-class path. Helm values select the active profile; the gateway and controllers are unaware of which profile is active.

#### Cloud-Managed Profile

Use this profile when deploying on AWS, GCP, or Azure. The provider's managed services handle HA, replication, scaling, patching, and backups. Fewer Kubernetes Deployments to operate; the Helm chart omits PgBouncer, Redis Sentinel, and MinIO resources.

| Component                         | Cloud-Managed Equivalent                     | Provider Examples                                                           | Notes                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| --------------------------------- | -------------------------------------------- | --------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Postgres**                      | Managed relational database with multi-AZ HA | AWS RDS for PostgreSQL, GCP Cloud SQL, Azure Database for PostgreSQL        | Same schema, same RLS enforcement. Managed service handles failover, backups, encryption at rest.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| **Connection pooler** (PgBouncer) | Provider connection proxy                    | AWS RDS Proxy, GCP Cloud SQL Auth Proxy, Azure PgBouncer (built-in)         | Must support transaction-mode pooling for RLS compatibility (Section 12.3). RDS Proxy supports `SET LOCAL` in transaction mode. Cloud SQL Auth Proxy terminates IAM auth but does not pool — if using Cloud SQL, deploy PgBouncer or pgcat alongside it, or use AlloyDB with built-in pooling. **`connect_query` limitation:** Most cloud-managed proxies do **not** support `connect_query` or an equivalent initialization hook, so the `__unset__` sentinel cannot be set on connection checkout. Deployments using such proxies **must** rely on the per-transaction tenant validation trigger (`lenny_tenant_guard`) as the second layer of RLS defense — see Section 12.3 for details. The preflight Job verifies this trigger exists when `connectionPooler = external`. |
| **Redis**                         | Managed cache/data store with HA             | AWS ElastiCache for Redis, GCP Memorystore for Redis, Azure Cache for Redis | Same AUTH + TLS requirements. ElastiCache Cluster Mode and Memorystore provide the horizontal sharding that self-managed Redis Cluster provides at Tier 3. Logical concern separation (Section 12.4) maps to separate ElastiCache replication groups or Memorystore instances.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| **Object storage** (MinIO)        | Provider-native object storage               | AWS S3, GCP Cloud Storage, Azure Blob Storage                               | `ArtifactStore` interface uses S3-compatible API; S3 and GCS are natively compatible. Azure Blob requires the S3-compatible gateway or a thin `ArtifactStore` implementation using Azure SDK. Encryption at rest, versioning, and lifecycle policies are provider-managed.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |

**Helm configuration (cloud-managed):**

```yaml
deploymentProfile: cloud-managed

postgres:
  # No PgBouncer Deployment created; gateway connects through provider proxy
  connectionPooler: external # "external" = provider-managed, "pgbouncer" = self-managed
  dsn: "postgres://..." # Provider-issued endpoint (e.g., RDS Proxy endpoint)
  readDsn: "postgres://..." # Read replica endpoint (provider reader endpoint)

redis:
  provider: external # "external" = provider-managed, "sentinel" / "cluster" = self-managed
  endpoints:
    - "rediss://elasticache-primary.example.com:6379"
  # For Tier 3 concern separation, configure per-role endpoints:
  # coordinationEndpoints: [...]
  # quotaEndpoints: [...]
  # cacheEndpoints: [...]

objectStorage:
  provider: s3 # "s3" | "gcs" | "azure" | "minio"
  bucket: "lenny-artifacts"
  region: "us-east-1"
  # Encryption, versioning, lifecycle managed by provider
```

#### Self-Managed Profile

Use this profile when deploying on bare-metal, on-premises Kubernetes, or any environment without managed database/cache services. The Helm chart deploys PgBouncer, Redis Sentinel (or Cluster), and MinIO as Kubernetes workloads alongside Lenny's own components.

| Component             | Self-Managed Implementation                          | Notes                                                                                        |
| --------------------- | ---------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| **Postgres**          | CloudNativePG operator or Patroni on Kubernetes      | See Section 12.3 for HA, encryption, backup requirements.                                    |
| **Connection pooler** | PgBouncer Deployment (2+ replicas) with PDB          | See Section 12.3 for sizing, pool mode, readiness probe, and monitoring.                     |
| **Redis**             | Redis Sentinel (Tiers 1–2) or Redis Cluster (Tier 3) | See Section 12.4 for topology, TLS, AUTH, failure behavior, and concern separation triggers. |
| **Object storage**    | MinIO with erasure coding                            | See Section 12.5 for HA topology, encryption, tenant isolation, and GC.                      |

**Helm configuration (self-managed):**

```yaml
deploymentProfile: self-managed

postgres:
  connectionPooler: pgbouncer
  pgbouncer:
    replicas: 2
    poolMode: transaction
    defaultPoolSize: 25
    # See Section 17.8 for per-tier sizing

redis:
  provider: sentinel # "sentinel" | "cluster"
  sentinels:
    - "redis-sentinel-0.redis:26379"
    - "redis-sentinel-1.redis:26379"
    - "redis-sentinel-2.redis:26379"

objectStorage:
  provider: minio
  endpoint: "http://minio.lenny-system:9000"
  bucket: "lenny-artifacts"
```

#### Local Development Profile

A third implicit profile exists for local development (Section 17.4): single Postgres container, no pooler, single Redis container, local disk for artifacts. This profile is activated by `make run` / docker-compose and requires zero cloud dependencies.

#### Profile-Invariant Requirements

Regardless of deployment profile, the following requirements apply uniformly:

- **Transaction-mode pooling** for Postgres connections (RLS compatibility)
- **Two-layer RLS defense:** Either `connect_query` sentinel (self-managed PgBouncer) **or** per-transaction tenant validation trigger (cloud-managed poolers without `connect_query` support) — at least one must be active; see Section 12.3
- **Redis AUTH + TLS** (no plaintext connections, no unauthenticated access)
- **Tenant key prefix** (`t:{tenant_id}:`) enforced at the Redis wrapper layer
- **S3-compatible API** for object storage (all providers above satisfy this)
- **Encryption at rest** for all persistent stores
- **Interface contracts** (Section 12.6) are identical across profiles — the gateway does not branch on deployment profile

---

## 18. Build Sequence

> **Note:** This section described a tentative phasing of the development of v1. Its goal is to inform the sequence in which AI agents should build and test v1, in order to reduce risk. However, this entire technical spec describes v1 and everything needs to be implemented.

| Phase | Components                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              | Milestone                                                                                                                                                                              |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1     | Core types: `Runtime` (unified, with `labels`), `type` field, `capabilities` (including `interaction: one_shot \| multi_turn`, `injection`), `executionMode`, `allowedExternalEndpoints` on delegation lease, `input_required` task state, messages array in TaskRecord, `suspended` session state. Agent-sandbox CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`). `Connector` resource with `labels`. `environmentId` nullable field on billing event schema. `crossEnvironmentDelegation` structured form schema slot. **Prerequisite:** ADR-TBD (`SandboxClaim` optimistic-locking verification, Section 4.6.1) must be resolved before Phase 1 begins — the `SandboxClaim` CRD design depends on its outcome.                                                                                                                | Foundation                                                                                                                                                                             |
| 2     | Replace adapter binary protocol: unified `{type:"message"}` (no separate `prompt`), `slotId` field for concurrent-workspace multiplexing, multi-server MCP adapter + lifecycle channel, adapter manifest written before binary spawns. Publish as runtime adapter specification. Includes `make run` local dev mode with embedded stores and echo runtime. **Startup benchmark harness**: automated test measuring per-phase startup latency (pod claim, credential assignment, workspace materialization, setup commands, agent session start, first prompt dispatch) for each runtime class; produces pod-warm vs SDK-warm comparison; runs in CI and ad-hoc. **Checkpoint duration benchmark**: measures end-to-end checkpoint time across workspace sizes (10MB, 100MB, 500MB) and validates the < 2s SLO for ≤ 100MB workspaces (see Section 4.4). | Can start an agent session; contributors can run locally; startup latency measurable; checkpoint duration baselined                                                                    |
| 2.5   | **Basic observability foundation**: structured logging with correlation fields (`tenantId`, `sessionId`, `taskId`, `sandboxId`) across all components, OpenTelemetry trace propagation wired into gateway and controller gRPC calls, request-scoped correlation ID generation and header propagation.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   | All subsequent phases debuggable with correlated traces and structured logs                                                                                                            |
| 3     | PoolScalingController. `DelegationPolicy` resource. `setupPolicy` enforcement. `taskPolicy.cleanupCommands`. **mTLS PKI setup**: cert-manager installation, `ClusterIssuer` configuration (self-signed or Vault-backed), certificate issuance for gateway replicas and controller, trust bundle distribution to agent pods, mTLS enforcement on gateway↔pod gRPC channel (see Section 10.3). This is required before PoolScalingController creates pods that communicate with the gateway.                                                                                                                                                                                                                                                                                                                                                              | Pods stay warm; delegation policy works; gateway↔pod mTLS operational                                                                                                                  |
| 3.5   | **Basic security hardening**: default-deny NetworkPolicy for agent namespaces, gVisor RuntimeClass validation (verify gVisor is functional before proceeding), digest-pinned base images for all controller-created pods, **admission policy deployment** (RuntimeClass-aware PSS enforcement, `shareProcessNamespace` validation — see Section 17.2), integration tests verifying controller-generated pod specs pass admission policies. **mTLS end-to-end verification**: integration tests confirming gateway↔pod and gateway↔controller mTLS is functional (not just admitted by policy), certificate auto-renewal works, and plain-text connections are rejected.                                                                                                                                                                                 | Agent pods run with network isolation, validated sandboxing, admission policy enforcement, and mTLS verified end-to-end before any external connectivity or credentials are introduced |

> **Note:** Digest-pinned images from a private registry are required from Phase 3 onward. Full image signing and attestation verification (Sigstore/cosign + admission controller) is Phase 14.

| Phase | Components                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               | Milestone                                                                                                                                 |
| ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------- |
| 4     | Session manager + session lifecycle + REST API                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Full create → upload → attach → complete flow                                                                                             |
| 4.5   | **Admin API foundation**: runtimes, pools, connectors, delegation policies, tenant management, external adapters registry. Gateway loads config from Postgres. Capability inference from MCP annotations at connector registration. **Bootstrap seed mechanism**: `lenny-ctl bootstrap` CLI command + Helm init Job (Section 17.6). **Authentication infrastructure**: OIDC/OAuth 2.1 JWT validation wired into the gateway (Section 10.2), `tenant_id` claim extraction and propagation, `UserStateStore` integration for role resolution, multi-tenant JWT test fixtures in CI (covering `platform-admin`, `tenant-admin`, and `user` roles across multiple tenants). This deliverable is the named auth-complete milestone that Phase 7's `AuthEvaluator` depends on. | All operational config API-managed; fresh install seeds Day-1 resources automatically; authentication verified for multi-tenant scenarios |
| 5     | Gateway `ExternalAdapterRegistry`. MCP adapter, Completions adapter, Open Responses adapter all active. `list_runtimes`, `GET /v1/runtimes`, `GET /v1/models` with identity-aware filtering. `type: mcp` runtime endpoints at `/mcp/runtimes/{name}`. Tenant RBAC config API. `noEnvironmentPolicy` enforcement. **REST/MCP contract tests** (Section 15.2.1): OpenAPI→MCP schema generation step in build pipeline; CI contract tests asserting semantic equivalence across both API surfaces for all overlapping operations.                                                                                                                                                                                                                                           | Clients can create and use sessions via REST, MCP, and Completions API; API surface consistency enforced by CI                            |

> **Note:** Phase 5 sessions use the zero-credential echo runtime (Phase 2). Phase 5.5 introduces basic credential leasing and the Basic Token Service (single-replica, K8s Secrets backend, no KMS), enabling real LLM provider testing from Phase 6 onward. The Token Service is the authoritative owner of `TokenStore` from Phase 5.5 onward; the Phase 12a hardening phase adds KMS, OAuth flows, and multi-replica HA.

| Phase | Components                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  | Milestone                                                                                                                                                                                            |
| ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 5.5   | **Basic credential leasing** + **Basic Token Service**: `CredentialProvider` interface, `anthropic_direct` provider implementation, single-pool credential assignment (`least-loaded` strategy), `AssignCredentials` RPC to push credentials to the adapter, lease creation and expiry. Admin API endpoint for credential pool registration. **Basic Token Service (single-replica, no KMS)**: a minimal Token/Connector Service deployment that owns `TokenStore` writes/reads and serves `AssignCredentials` / `RevokeCredentials` RPCs to the gateway over mTLS. Credentials are read from Kubernetes Secrets at this stage — no KMS envelope encryption, no OAuth flows, no credential rotation. This component is the authoritative owner of `TokenStore` from Phase 5.5 onward; Phase 7's `AuthEvaluator` (backed by `TokenStore` and `UserStateStore`) and Phase 11's advanced credential leasing both depend on it being present. **Limitations at this stage:** single replica, K8s Secrets backend only, no OAuth token storage, no KMS, no credential rotation — these are all addressed in Phase 12a hardening. | Sessions can use real LLM providers; end-to-end testing with actual model calls begins; Token Service present as the authoritative credential owner before policy engine and advanced leasing phases |

> **Note — Phase 6–8 CI test coverage:** Phase 5.5 enables real LLM provider testing from Phase 6 onward, which is sufficient for integration testing of streaming reconnect (Phase 6), quota enforcement (Phase 7), and checkpoint/resume (Phase 8). However, CI pipelines should not depend exclusively on real LLM credentials for milestone validation. The echo runtime (Phase 2) is Minimum-tier and cannot exercise these behaviors: it does not produce streaming output, report token usage via `ReportUsage`, or implement the Full-tier lifecycle channel (`checkpoint_request`/`checkpoint_ready`). Phase 6–8 milestone validation requires either extending the echo runtime or introducing an additional test runtime that supports simulated streaming, token usage reporting, and Full-tier lifecycle — following the same pattern as `delegation-echo` (Phase 9). The test runtime design is an implementation detail; the requirement is that each phase's milestone can be validated in CI without external LLM credentials.

| 6 | Interactive session model (streaming, messages, reconnect with event replay) | Full interactive sessions work |
| 7 | Policy engine (rate limits, auth, budgets, tenant_id) | Production-grade admission |
| 8 | Checkpoint/resume + artifact seal-and-export | Sessions survive pod failure; artifacts retrievable |
| 9 | `lenny/delegate_task` handles internal and external targets. `lenny/send_message`, `lenny/request_input`, `lenny/get_task_tree`. `lenny/discover_agents` with policy scoping. Multi-turn fully operational. **Delegation-capable test runtime** (`delegation-echo`): a scripted test runtime that executes pre-defined tool call sequences (e.g., `lenny/delegate_task`, `lenny/send_message`), delegates to child sessions, and handles results — enabling end-to-end delegation and recursive delegation testing without a real LLM provider. Ships alongside the echo runtime as a built-in test fixture. | Parent → child task flow with multi-turn; delegation testable without LLM credentials |
| 10 | `agentInterface` in discovery. Adapter manifest includes summaries. MCP fabric (virtual child interfaces, elicitation chain with provenance). | Recursive delegation with MCP semantics |
| 11 | **Advanced credential leasing**: multi-provider implementations (Bedrock STS, Vertex AI, Azure), credential rotation (`RotateCredentials` RPC), fallback chains with cooldown, user-scoped credentials (elicitation flow, `/v1/credentials` endpoints for pre-authorized registration and resolution), credential health scoring, LLM proxy credential injection, `credentialPolicy` with source preference logic. **Emergency credential revocation**: revocation admin endpoint, credential deny list propagation via Redis pub/sub, LLM proxy deny-list enforcement (Section 4.9). Note: the pre-authorized credential flow mechanics (endpoints, scoping, resolution, security treatment) are fully specified in Section 4.9; this phase implements them. | Full credential lifecycle with rotation, fallback, user-scoped keys, and emergency revocation |
| 12a | **Token/Connector Service hardening** (builds on Phase 5.5 Basic Token Service): KMS envelope encryption for stored OAuth tokens, multi-replica deployment with PodDisruptionBudget, OAuth flows for external tool connectors (GitHub, Jira, etc.), full credential pool management, `RotateCredentials` RPC, per-user OAuth token storage. Phase 5.5's single-replica K8s-Secrets-backed service is promoted to the production-grade deployment described in Section 4.3. | Token Service production-hardened: KMS-backed token storage, OAuth connector auth, multi-replica HA, credential rotation operational |
| 12b | `type: mcp` runtime support. MCP runtime endpoints, lifecycle management, and discovery integration. | External MCP runtimes operational |
| 12c | Concurrent execution modes including `slotId` multiplexing for workspace variant. | Concurrent mode available for all runtime types |
| 13 | **Full observability stack**: audit logging, OpenTelemetry metrics and dashboards, distributed tracing visualization, alerting rules, SLO monitoring. Builds on structured logging and trace propagation established in Phase 2.5. | Operational readiness |
| 13.5 | **Pre-hardening load baseline**: benchmark all documented scaling concerns and SLOs to establish baseline measurements before security hardening. Key scenarios: (1) pod claim + workspace materialization latency under concurrent session creation (target: P99 from Section 6.3), (2) checkpoint duration across workspace sizes vs < 2s SLO for ≤ 100MB (Section 4.4), (3) gateway horizontal scaling to 10,000 concurrent sessions (Section 16.5), (4) pool scaling controller responsiveness under burst demand, (5) delegation chain depth and fan-out throughput (Section 5), (6) streaming reconnect under load (Section 6), (7) credential rotation latency during active sessions (Section 11). Produces capacity planning baselines and identifies bottlenecks. Results are pre-hardening measurements, not final SLO validation — Phase 14.5 re-validates after security hardening. | Pre-hardening load baseline established; bottlenecks identified; capacity plan documented |
| 14 | **Comprehensive security hardening**: image signing and admission enforcement, advanced NetworkPolicy refinement (per-runtime egress rules), seccomp profile tuning, pod-level security context hardening, security audit and penetration testing. Basic network isolation and gVisor enforcement are already in place since Phase 3.5. | Production-grade security posture validated by audit |
| 14.5 | **Post-hardening SLO re-validation**: re-run Phase 13.5 load test scenarios with all security hardening in place (image signing admission, advanced NetworkPolicy, seccomp profiles, hardened security contexts). Compare results against Phase 13.5 baselines to quantify security overhead. All SLOs from Section 16.5 must hold at sustained Tier 2 load with full security controls active. Any SLO regression triggers investigation and resolution before GA. This is the final SLO validation gate referenced by Section 16.5's benchmark requirement. | All scaling SLOs validated under load with full security hardening; delta from Phase 13.5 baseline documented |
| 15 | Environment resource: tag-based selectors, member RBAC, `mcpRuntimeFilters` with capability model, `connectorSelector`, cross-environment delegation enforcement, billing rollup endpoint, membership analytics endpoints, explicit environment endpoints across all adapters. | Full RBAC and environment support |
| 16 | Experiment primitives, PoolScalingController experiment integration. | A/B testing infrastructure |
| 17 | Memory (`MemoryStore` + platform tools), semantic caching, guardrail interceptor hooks, eval hooks. Production-grade docker-compose, documentation, community guides. | Full community onboarding |

> **Open-source readiness:** Lenny is designed as an open-source project. Contribution guidelines (`CONTRIBUTING.md`), governance model (BDfN transitioning to steering committee), and community communication channels will be established as part of Phase 2 (alongside the `make run` quick-start). The Phase 2 milestone includes a < 5-minute Time to Hello World (TTHW) target validated by CI. See Section 23.2 for the full community adoption strategy including target personas and governance details. The technical design prioritizes community extensibility through pluggable credential providers, a published runtime adapter contract, and a clear SDK boundary.

---

## 19. Resolved Decisions

These were open questions from the initial design, now resolved:

| #   | Question                         | Decision                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| --- | -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Checkpointing strategy           | Full snapshots with size cap. Keep latest 2 per session. Incrementals deferred.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| 2   | Agent binary packaging           | Sidecar container with local Unix socket. `shareProcessNamespace: false`. Lower barrier for third-party authors.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 3   | Multi-tenancy                    | `tenant_id` in all data models. Logical isolation via filtering. Namespace-level isolation deferred.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 4   | Controller framework             | `kubernetes-sigs/agent-sandbox` for pod lifecycle CRDs. kubebuilder (controller-runtime) for PoolScalingController and custom controllers.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 5   | Service mesh dependency          | cert-manager + manual mTLS. No Istio/Linkerd requirement (fewer deps for community adoption).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| 6   | Default isolation                | gVisor (`sandboxed`) is the default. `runc` requires explicit deployer opt-in.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| 7   | Blob storage                     | MinIO. Never Postgres for blobs.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 8   | Delegation file export structure | Source glob base path is stripped; files are rebased to child workspace root. Optional `destPrefix` prepends a path. Parent controls the slice; child sees clean root-relative structure. See Section 8.7.                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 9   | Inter-child data passing         | No first-class `pipe_artifacts` operation. Parents use the existing export→re-upload flow via `delegate_task` file exports. Simpler; avoids a new gateway primitive.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 10  | Setup command policy             | Allowlist is the recommended default for multi-tenant deployments; blocklist is a convenience guard (not a security boundary) for single-tenant scenarios. `shell: false` mode prevents metacharacter injection via direct exec. The real security boundary is the pod sandbox (gVisor/Kata, non-root UID, read-only root, network policy). See Section 7.5.                                                                                                                                                                                                                                                                                                                       |
| 11  | Billing/showback                 | Track per-session, per-token, and per-minute usage. Expose via REST API (`GET /v1/usage`). Filterable by tenant, user, runtime, and time window.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 12  | Session forking                  | Not supported. The `fork_session` concept is dropped. Clients can derive a new session from a previous one by: (1) downloading the previous session's workspace snapshot via `GET /v1/sessions/{id}/workspace`, (2) creating a new session, (3) uploading the snapshot as an `uploadArchive` source in the new session's WorkspacePlan. The gateway also provides a convenience endpoint `POST /v1/sessions/{id}/derive` that performs steps 1-3 atomically — it creates a new session pre-populated with the previous session's workspace snapshot.                                                                                                                               |
| 13  | Lease extension                  | Supported via adapter↔gateway gRPC lifecycle (not an MCP tool — runtime is unaware). Triggered by the adapter when the LLM proxy rejects for budget exhaustion. `elicitation` mode (default): serialized per tree, one elicitation at a time, concurrent requests batched, cool-off window after approval, rejection is permanent for the subtree. `auto` mode: each request handled independently, no elicitation. Extensions can never exceed deployer caps or the parent's own lease. Extension responses include granted amounts and explicit status: `GRANTED`, `PARTIALLY_GRANTED`, `CEILING_REACHED` (zero grant — adapter must not retry), or `REJECTED`. See Section 8.6. |

Each decision above is a summary; full Architecture Decision Records (ADRs) with context, alternatives considered, and consequences will be maintained in `docs/adr/` as separate documents following the MADR format, with this table serving as an index.

## 20. Open Questions

All open questions have been resolved. See Section 19 for decisions.

---

## 21. Planned / Post-V1

The following items are documented now so data models accommodate them. Implementation is deferred.

**21.1 A2A Full Support.** `ExternalProtocolAdapter` is the mechanism. A2A is implementation two. Inbound: gateway serves `POST /a2a/{runtimeName}/tasks` via `A2AAdapter`. The adapter creates a Lenny session, maps A2A task fields to `TaskSpec`, and translates A2A state queries to Lenny's canonical task state machine (see Section 8.8 protocol mapping table). Outbound: external A2A agents registered as connectors, callable via `lenny/delegate_task`. A2A `input-required` state maps directly to Lenny's `input_required` task state. A2A `canceled` maps to Lenny's `cancelled`. A2A `unknown` is not a Lenny state — it surfaces as a 404 from the adapter. A2A artifacts map to Lenny artifact refs (`artifact://`). `agentInterface` auto-generates A2A agent cards. Per-agent A2A endpoints (not aggregated) at `/a2a/runtimes/{name}`. `allowedExternalEndpoints` slot on delegation lease schema exists from v1.

**21.2 A2A Intra-Pod Support.** Adapter additionally serves per-agent A2A endpoints intra-pod. Adapter manifest gains A2A base URL. All A2A traffic proxied through gateway. Runtime authors opt in explicitly.

**21.3 Agent Protocol (AP) Support.** AP defines `POST /ap/v1/agent/tasks` and step execution. Third `ExternalProtocolAdapter` implementation.

**21.4 Future Conversational Patterns.** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId` accommodates all of these without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

**21.5 Environment Management UI.** Full web UI for browsing environments, editing membership, and previewing selector matches is thin-client work over the admin API. The `?dryRun=true` parameter on `PUT /v1/admin/environments` (semantics defined in Section 15.1) is the preview mechanism and ships in v1. The dryRun response includes a `preview` object with `matchedRuntimes`, `matchedConnectors`, and `unmatchedSelectorTerms` arrays, giving the UI immediate feedback on which resources a selector change would include and flagging selector terms that match nothing.

**21.6 Environment Resource — Post-V1 Deferred Items.** Cross-environment delegation richer controls. Runtime-level cross-environment exceptions at granularity beyond the structured form. Integration with experiment-scoped delegation boundaries.

**21.7 Multi-Cluster Federation.** Session IDs must be globally unique. Storage interfaces must not assume single-cluster topology. Connectors registered in one cluster must be expressible by reference in multi-cluster scenarios.

**21.8 UI and CLI.** Admin API is the complete operational surface. Official CLI (`lenny-ctl`) and web portal are separate projects consuming the admin API as thin clients with zero business logic.

---

## 22. Explicit Non-Decisions

**22.1 No Model B Runtime Deployment.** No mechanism for packaging a graph definition as a new registered runtime. Users register derived runtimes via admin API. (Context: LangGraph deployment discussion — Model A chosen: one generic LangGraph runtime, many deployed graphs via derived runtimes.)

**22.2 No Built-In Eval Logic.** Lenny provides hooks and storage. No LLM-as-judge or hallucination detection.

**22.3 No Built-In Guardrail Logic.** Lenny provides the `RequestInterceptor` hook (Section 4.8) and the `contentPolicy` on `DelegationPolicy` (Section 8.3). No content classifiers or prompt injection detection are built in. **Deployers enabling delegation chains without configuring `contentPolicy.interceptorRef` accept the risk that a compromised or manipulated parent agent can craft adversarial `TaskSpec.input` payloads targeting child agents.** The `maxInputSize` limit provides a baseline size constraint, but content-level scanning requires an external interceptor. **Elicitation responses** (Section 9.2) flowing downward from clients or gateway-registered connectors to pods are not subject to `contentPolicy` interceptors — they originate from humans or trusted connectors and are delivered as replies to pending `lenny/request_input` calls. A malicious human user or compromised connector could provide adversarial content in an elicitation response; this is an inherent property of human-in-the-loop systems and is mitigated by connector registration requirements and provenance metadata, not by content scanning.

**22.4 No Built-In Memory Extraction.** Lenny provides the `MemoryStore` interface and tools (Section 9.4). Runtimes decide what to write.

**22.5 No Direct External Connector Access.** Connectors are session-internal in v1. External clients do not call connectors directly. Whether to add this later is an independent product decision; the data model accommodates it without requiring a redesign.

**22.6 Hooks-and-Defaults Design Principle.** Every cross-cutting AI capability (memory, caching, guardrails, evaluation, routing) follows the same pattern: defined as an interface with a sensible default implementation, disabled unless explicitly enabled by the deployer, fully replaceable. Lenny never implements AI-specific logic (eval scoring, memory extraction, content classification) — that belongs to specialized tools in the ecosystem. This is a governing architectural principle, not a limitation — it is the reason Lenny can serve as a platform layer without competing with the ecosystem tools deployers already use. See Section 23.1, differentiator 6 for the competitive positioning of this principle.

---

## 23. Competitive Landscape

| Project                         | Why It Matters for Lenny                                                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| ------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kubernetes-sigs/agent-sandbox` | Near-identical pod lifecycle primitive being standardized upstream — adopted as Lenny's infrastructure layer (Section 4.6)                                                                                                                                                                                                                                                                                                                                                                   |
| E2B                             | Market-leading AI sandbox with Firecracker microVMs, ~150ms boot times, self-hosting options. Primary comparison point.                                                                                                                                                                                                                                                                                                                                                                      |
| Fly.io Sprites                  | Jan 2026 direct competitor with Firecracker + checkpoint/restore in ~300ms.                                                                                                                                                                                                                                                                                                                                                                                                                  |
| Google A2A Protocol             | Agent-to-agent protocol now under AAIF governance alongside MCP. Addressed via `ExternalAdapterRegistry`, `publishedMetadata`, and `allowedExternalEndpoints` (Sections 15, 5.1, 8.3)                                                                                                                                                                                                                                                                                                        |
| Daytona                         | Sub-90ms cold starts, desktop environments for computer-use agents.                                                                                                                                                                                                                                                                                                                                                                                                                          |
| LangSmith Deployment            | Now has A2A + MCP + RemoteGraph — closing the protocol gap. However, RemoteGraph provides graph-level delegation without per-hop token budget or scope controls enforced at the platform layer (Section 5). LangSmith's A2A and MCP support operates within the LangChain ecosystem; Lenny's adapter contract (Section 15.4) is runtime-agnostic and does not require agent code to use LangChain. LangSmith is also a hosted service with no self-hosted Kubernetes-native deployment path. |
| Amazon Bedrock AgentCore Memory | Short-term + long-term memory for agents. Motivated the `MemoryStore` interface (Section 9.4).                                                                                                                                                                                                                                                                                                                                                                                               |
| Temporal                        | Durable workflow engine with replay-based fault tolerance. Strong at long-running orchestration but requires workflow logic in Temporal SDKs — not runtime-agnostic. Lenny's gateway-mediated delegation (Section 5) provides durable session lineage without coupling agent code to a workflow SDK.                                                                                                                                                                                         |
| Modal                           | Serverless GPU/CPU container platform with sub-second cold starts. Excellent for batch inference but lacks agent-specific primitives (delegation, token budgets, MCP). Lenny's pre-warmed pod pools (Section 6.3) target similar latency while adding session lifecycle and policy enforcement (Section 8).                                                                                                                                                                                  |
| LangGraph (LangChain)           | Graph-based agent orchestration with built-in persistence and human-in-the-loop. Tightly coupled to LangChain ecosystem. Lenny treats LangGraph agents as one possible runtime behind the adapter contract (Section 15.4) rather than mandating a specific orchestration framework.                                                                                                                                                                                                          |

> **Latency comparison note — what each number measures.** The startup figures above are not directly comparable to Lenny's SLO targets in Section 6.3 and 16.5. Competitors' published numbers (E2B ~150ms, Daytona sub-90ms, Fly.io ~300ms) measure **cold-start time**: the wall-clock duration to boot a fresh VM or container from a stopped state — essentially container scheduling + runtime initialization only. Lenny's SLO target (P95 < 2s runc, < 5s gVisor) measures **full session-ready time**: pod claim + workspace file delivery + setup command execution + agent session start. These are different operations. The pod-claim-and-routing step alone — the operation most directly analogous to a competitor's cold-start number — is in the ~ms range (Section 6.3); the remainder of Lenny's hot-path latency is workspace setup work that competitors' numbers do not include at all. A fair latency comparison requires aligning on the same end-point definition. Lenny's numbers are also explicitly **unvalidated targets** to be measured by the Phase 2 startup benchmark harness (Section 6.3) — not published benchmarks. Until Phase 2 benchmarks are available, latency parity with competitor cold-start figures is to be demonstrated, not assumed.

### 23.1 Why Lenny?

Lenny occupies a distinct point in the agent infrastructure design space. The differentiators below are architectural commitments, not roadmap aspirations — each is reflected in the spec sections cited.

1. **Runtime-agnostic adapter contract.** Lenny does not bundle or mandate a specific agent framework. Any process that implements the gRPC runtime adapter (Section 15.4) can run as a Lenny agent pod — Claude Code, a custom LangChain agent, or a bare-metal script. Competitors like E2B and Daytona provide sandbox environments but assume the operator brings their own orchestration; Temporal and LangGraph require agent logic to use their respective SDKs; Lenny provides both the sandbox and the orchestration contract without coupling to a specific framework.

2. **Recursive delegation as a platform primitive.** Any agent pod can spawn child sessions through the gateway with enforced scope, token budget, and lineage tracking (Section 5, Principle 5). This is not a library-level feature bolted on — it is a first-class gateway operation with policy enforcement at every hop. LangSmith's RemoteGraph offers graph-level delegation but without per-hop budget/scope controls.

3. **Self-hosted, Kubernetes-native.** Lenny runs on the operator's own cluster using standard Kubernetes primitives — CRDs, RuntimeClasses, namespaces (Section 17). There is no dependency on a vendor-hosted control plane. E2B, Fly.io Sprites, and Modal require their hosted infrastructure; Temporal Cloud is available but self-hosted Temporal adds significant operational burden. Lenny's local development mode (Section 17.4) runs with zero cloud dependency.

4. **Multi-protocol gateway.** A single gateway edge serves MCP, OpenAI, and Open Responses clients via the `ExternalAdapterRegistry` (Section 15, Section 3). Operators do not need separate infrastructure per client protocol.

5. **Enterprise controls at the platform layer.** Rate limiting, token budgets, concurrency controls, deployer-selectable isolation profiles (runc/gVisor/Kata), audit logging, and least-privilege pod security are built into the gateway and controller layers (Sections 2, 8, 16). These are not add-ons — they are enforced by default.

6. **Ecosystem-composable via hooks-and-defaults.** Every cross-cutting AI capability — memory, caching, guardrails, evaluation, routing — is defined as an interface with a sensible default, disabled unless explicitly enabled, and fully replaceable (Section 22.6). Lenny never implements AI-specific logic (eval scoring, memory extraction, content classification); that belongs to specialized tools the deployer already uses. This is a deliberate architectural stance: LangChain/LangSmith bundles its own evaluators, tracing, and memory tightly, requiring adopters to work within or around that stack; Modal provides no agent-specific hooks at all. Lenny provides the platform layer and stays out of the ecosystem layer, so deployers compose their preferred tools without fighting the platform.

### 23.2 Community Adoption Strategy

**Target personas:**

| Persona                       | Motivation                                                                                  | Entry point                                                        |
| ----------------------------- | ------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| **Runtime authors**           | Integrate their agent framework (LangChain, CrewAI, custom) with a managed session platform | Runtime adapter contract (Section 15.4), `make run` local dev mode |
| **Platform operators**        | Run multi-tenant agent infrastructure on their own clusters                                 | Helm chart, `lenny-ctl bootstrap`, Admin API (Section 17)          |
| **Enterprise platform teams** | Evaluate Lenny against E2B/Daytona for internal agent hosting with policy controls          | Comparison guides, enterprise controls documentation (Section 16)  |

**Time to Hello World (TTHW) target: < 5 minutes.** A new contributor must be able to clone the repo, run `make run`, and complete a round-trip echo session within 5 minutes on a standard development machine. The Phase 2 `make run` local dev mode (Section 18) with embedded stores and echo runtime is the primary vehicle for this target. CI includes a TTHW smoke test that validates this path on every merge.

**Governance model.** Lenny adopts a lightweight governance structure established during Phase 2:

- **Benevolent Dictator for Now (BDfN):** Single maintainer makes final decisions during early development (Phases 1-4). Transition to a multi-maintainer steering committee when the project reaches 3+ regular contributors.
- **Decision records:** All architectural decisions tracked via ADRs in `docs/adr/` (see Section 19). Community-proposed changes above a defined scope threshold require an ADR with alternatives analysis.
- **Contribution path:** `CONTRIBUTING.md` published in Phase 2 covering: local dev setup, test expectations, PR review process, and the runtime adapter contract as the primary extension point.
- **Communication:** Public issue tracker for bugs and feature requests. Discussions forum for design proposals and RFC-style conversations.

**Open-source license.** The project license has not yet been selected and is an open question requiring resolution before Phase 2 (before `CONTRIBUTING.md` publication and any external contributor engagement). The license choice will be evaluated against the competitive landscape — notably E2B (AGPL + commercial), Temporal (MIT), LangChain (MIT) — and must satisfy enterprise legal review gates, runtime author copyleft clarity, and compatibility with `kubernetes-sigs/agent-sandbox` upstream. This decision will be recorded as an ADR in `docs/adr/`.

**Comparison guides.** Phase 17 deliverables include published comparison guides covering Lenny vs E2B, Daytona, Fly.io Sprites, Temporal, Modal, and LangGraph — focusing on self-hosting, recursive delegation, and runtime-agnostic design as differentiators (see Section 23.1).
