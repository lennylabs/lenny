## 15. External API Surface

Lenny exposes multiple client-facing APIs through the **`ExternalAdapterRegistry`** — a pluggable adapter system where simultaneously active adapters route by path prefix. All adapters implement a common interface:

```go
type ExternalProtocolAdapter interface {
    // Required — all adapters must implement these three.
    HandleInbound(ctx, w, r, dispatcher) error
    HandleDiscovery(ctx, w, r, runtimes []AuthorizedRuntime, caps AdapterCapabilities) error
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

    // OpenOutboundChannel is called by the gateway when an adapter with
    // outbound push capability (OutboundCapabilitySet.PushNotifications == true)
    // needs to deliver events to a registered callback or subscriber.
    // The returned OutboundChannel is owned by the adapter; the gateway calls
    // Send() for each qualifying SessionEvent and Close() when the adapter is
    // unregistered. Adapters with no outbound push return a no-op channel.
    OpenOutboundChannel(ctx context.Context, sessionID string, sub OutboundSubscription) (OutboundChannel, error)
}

// AdapterCapabilities declares the routing and protocol capabilities of an adapter.
// BaseAdapter.Capabilities() returns a zero value with PathPrefix and Protocol
// populated from the adapter's registration; all bool fields default to false.
type AdapterCapabilities struct {
    // PathPrefix is the URL path prefix this adapter owns (e.g., "/mcp", "/a2a", "/v1").
    // The gateway routes inbound requests to this adapter when the request path
    // has this prefix. Must be unique across all registered adapters.
    PathPrefix string

    // Protocol is the protocol identifier for this adapter (e.g., "mcp", "a2a",
    // "openai-completions", "openai-responses"). Used in audit events and metrics.
    Protocol string

    // SupportsSessionContinuity indicates the adapter can resume interrupted sessions
    // (i.e., it persists sufficient state to reconstruct the protocol session after
    // a gateway restart or failover).
    SupportsSessionContinuity bool

    // SupportsDelegation indicates the adapter handles delegated task routing —
    // it can receive and forward delegate_task calls from parent sessions.
    SupportsDelegation bool

    // SupportsElicitation indicates the adapter can surface lenny/request_elicitation
    // calls to the client (human-in-the-loop input collection).
    SupportsElicitation bool

    // SupportsInterrupt indicates the adapter handles interrupt_request signals
    // from the lifecycle channel and can surface them to the client.
    SupportsInterrupt bool
}

// OutboundCapabilitySet declares the asynchronous push capabilities of an adapter.
// All fields are false in the zero value (BaseAdapter default).
type OutboundCapabilitySet struct {
    // PushNotifications indicates the adapter can deliver state-change events
    // to a caller-registered callback URL or persistent connection after the
    // initial inbound response has been sent. Required for A2A streaming updates
    // and webhook-based integrations.
    PushNotifications bool

    // SupportedEventKinds lists the SessionEvent kinds the adapter is prepared
    // to push. An empty slice means no events are pushed even if PushNotifications
    // is true. Well-known kinds: "state_change", "output", "elicitation",
    // "tool_use", "error", "terminated".
    SupportedEventKinds []string

    // MaxConcurrentSubscriptions is the maximum number of simultaneous
    // OutboundChannel instances the adapter supports per session. 0 = unlimited.
    MaxConcurrentSubscriptions int
}

// OutboundSubscription carries the caller-supplied delivery target registered
// when the external protocol request was accepted (e.g., an A2A webhook URL,
// a long-poll response writer, or a persistent SSE stream handle).
type OutboundSubscription struct {
    // CallbackURL is the webhook URL to POST events to, if applicable.
    // Empty for connection-coupled delivery (SSE, long-poll).
    CallbackURL string

    // ResponseWriter is set for connection-coupled adapters; nil for webhook.
    ResponseWriter http.ResponseWriter

    // Metadata carries adapter-specific fields (e.g., A2A task ID, correlation IDs).
    Metadata map[string]string
}

// OutboundChannel is a handle to an active push channel for a single session.
// The gateway calls Send for each qualifying event and Close when the session
// terminates or the subscription is cancelled.
type OutboundChannel interface {
    // Send delivers a SessionEvent to the subscriber. Implementations must be
    // non-blocking; if the subscriber is slow, events may be buffered or dropped
    // according to the normative back-pressure policy below. Send returns an
    // error if the channel is permanently unavailable (e.g., webhook URL
    // consistently unreachable); the gateway will close the channel on non-nil error.
    Send(ctx context.Context, event SessionEvent) error

    // Close releases resources. Called exactly once by the gateway.
    Close() error
}

// Normative back-pressure policy for OutboundChannel implementations.
//
// Each OutboundChannel MUST implement one of the following two policies:
//
//   1. Buffered-drop policy (REQUIRED for webhook-based adapters):
//      The channel maintains an in-memory event buffer with a maximum depth of
//      MaxOutboundBufferDepth (default: 256 events; configurable per adapter via
//      the `adapter.outboundBufferDepth` Helm value, range: 16–4096). When Send
//      is called and the buffer is full, the oldest event in the buffer is evicted
//      (head-drop) and the new event is enqueued. The eviction increments the
//      `lenny_outbound_channel_buffer_drop_total` counter (labeled by `adapter`,
//      `session_id`). Send MUST return nil even on eviction — buffer overflow is
//      a degradation signal, not a fatal error, so the gateway does not close the
//      channel on drop.
//
//   2. Bounded-error policy (REQUIRED for connection-coupled adapters — SSE, long-poll):
//      The channel attempts a non-blocking write to the underlying transport.
//      If the write would block (subscriber's read loop is behind), the channel
//      MUST return a non-nil error from Send within 100 ms. The gateway closes
//      the channel on non-nil error and removes it from the session's dispatch
//      map. The subscriber must reconnect. This ensures a single slow subscriber
//      cannot block the gateway's event dispatch loop.
//
// Both policies share these invariants:
//   - Send MUST NOT block the caller for more than MaxOutboundSendTimeoutMs
//     (default: 100 ms; configured globally via `adapter.outboundSendTimeoutMs`).
//   - Send MUST be safe to call concurrently from multiple goroutines.
//   - The buffer depth limit applies per OutboundChannel instance (per session),
//     not globally across all channels — a slow subscriber for one session cannot
//     starve delivery to other sessions.
//
// Adapters that embed BaseAdapter inherit the buffered-drop policy with
// MaxOutboundBufferDepth = 256. Adapters that override Send must document
// which policy they implement.
```

The gateway provides a **`BaseAdapter`** struct with no-op implementations of all optional methods. Adapters that embed `BaseAdapter` satisfy the full interface and only override lifecycle hooks they need — existing adapters (MCP, OpenAI Completions, Open Responses) require no changes. `BaseAdapter.OutboundCapabilities()` returns a zero-value `OutboundCapabilitySet` (all false). `BaseAdapter.OpenOutboundChannel()` returns a no-op channel that discards all events.

**Gateway outbound dispatch.** When a session event fires, the gateway iterates all adapters that have an active `OutboundChannel` for the session (tracked in an adapter-keyed map per session). For each channel, it calls `Send` with the event. Channels that return a non-nil error are closed and removed from the map. Adapters choose their own delivery semantics inside `Send` — buffered HTTP POST, SSE frame write, or silent drop with a metric increment. The gateway does not impose a delivery order guarantee across adapters.

**`HandleDiscovery` is required on all adapters.** Every adapter translates Lenny's policy-scoped runtime list into its protocol's native discovery format. Each adapter **must** include its own `AdapterCapabilities` as an `adapterCapabilities` annotation in its discovery output so that consumers know which protocol-level capabilities (elicitation, delegation, interrupts, session continuity) the active adapter provides. The gateway calls `Capabilities()` on the serving adapter and passes the result to `HandleDiscovery` as an additional parameter alongside the runtime list; adapters embed the capability fields in their native discovery format (e.g., a top-level `adapterCapabilities` object in REST and `list_runtimes` responses, or a `capabilities` node in A2A agent cards). At minimum, `supportsElicitation` must be surfaced — callers must not start elicitation-dependent workflows against an adapter that returns `supportsElicitation: false`.

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

**OpenAPI spec endpoint.** The gateway serves its OpenAPI 3.x specification at `GET /openapi.yaml` (no authentication required). The same document is available at `GET /openapi.json` for clients that prefer JSON. The served spec reflects the API version of the running gateway instance; the `info.version` field in the spec matches the gateway's release version. Community SDK generators should target `/openapi.yaml` as the canonical source. The spec is generated from the same source-of-truth that drives REST/MCP contract tests ([Section 15.2.1](#1521-restmcp-consistency-contract)).

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
| `POST`   | `/v1/sessions/{id}/tool-use/{tool_call_id}/approve` | Approve a pending tool call                                |
| `POST`   | `/v1/sessions/{id}/tool-use/{tool_call_id}/deny`    | Deny a pending tool call. Optional body: `{"reason": "<string>"}` |
| `POST`   | `/v1/sessions/{id}/elicitations/{elicitation_id}/respond` | Answer an elicitation request. Body: `{"response": <value>}` |
| `POST`   | `/v1/sessions/{id}/elicitations/{elicitation_id}/dismiss` | Dismiss a pending elicitation                         |
| `DELETE` | `/v1/sessions/{id}`           | Terminate and clean up                                                    |

**State-mutating endpoint preconditions.** The following table maps each state-mutating session endpoint to its valid precondition states and resulting state transitions. Calling an endpoint in an invalid state returns `409 INVALID_STATE_TRANSITION` with `details.currentState` and `details.allowedStates`.

| Endpoint                           | Valid precondition states                                          | Resulting transition                                                                   | Notes                                                                                                                           |
| ---------------------------------- | ------------------------------------------------------------------ | -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `POST /v1/sessions/{id}/upload`    | `created`; also `running` when runtime declares `capabilities.midSessionUpload: true` and deployer policy allows it | remains in current state                                                               | Pre-start: gateway rejects if session already finalized or started. Mid-session: see [Section 7.4](07_session-lifecycle.md#74-upload-safety)                                |
| `POST /v1/sessions/{id}/finalize`  | `created`                                                          | `finalizing` → `ready`                                                                 | Triggers workspace materialization and setup commands                                                                           |
| `POST /v1/sessions/{id}/start`     | `ready`                                                            | `starting` → `running`                                                                 | Starts the agent runtime                                                                                                        |
| `POST /v1/sessions/{id}/interrupt` | `running`                                                          | `suspended`                                                                            | Only valid while the agent is actively executing. Not valid in `suspended`, `starting`, `finalizing`, or any terminal state.    |
| `POST /v1/sessions/{id}/terminate` | `created`, `finalizing`, `ready`, `starting`, `running`, `suspended`, `resume_pending`, `awaiting_client_action` | `completed`                                                                            | Valid in any non-terminal state. For `created`, the gateway cancels any pending finalization, releases resources if any were allocated, and marks the session `completed`. For `finalizing` and `ready`, the gateway aborts the in-progress setup or dequeues the waiting session, releases the pod, and marks the session `completed`. Graceful shutdown.                                                                  |
| `POST /v1/sessions/{id}/resume`    | `awaiting_client_action`                                           | `resume_pending` → `running`                                                           | Only valid after automatic retries are exhausted. Not valid in `suspended` (use message delivery or `resume_session` for that). `resuming` is an internal-only transient state between `resume_pending` and `running`; the API reports the transition as `resume_pending` → `running`. |
| `POST /v1/sessions/{id}/messages`  | Any non-terminal state. Delivery semantics vary by state: `running` and `suspended` deliver or buffer per [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) paths 1-7; `resume_pending` and `awaiting_client_action` enqueue to DLQ; pre-running states buffer (inter-session) or reject with `TARGET_NOT_READY` (external client). | `running` (if `suspended` with `delivery: immediate`, atomically resumes and delivers); no state change for other states | See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) for full delivery semantics per state.                                                                                         |
| `POST /v1/sessions/{id}/derive`    | `completed`, `failed`, `cancelled`, `expired` (default); `running`, `suspended`, `resume_pending`, `awaiting_client_action` (requires `allowStale: true` in request body) | Creates a new session (original unchanged) | Terminal sessions use the sealed or last-checkpoint snapshot. Non-terminal sessions require `allowStale: true`; derive uses the most recent successful checkpoint snapshot. Response includes `workspaceSnapshotSource` and `workspaceSnapshotTimestamp`. See [Section 7.1](07_session-lifecycle.md#71-normal-flow) derive semantics. |
| `DELETE /v1/sessions/{id}`         | Any non-terminal state                                             | `cancelled`                                                                            | Force-terminates and cleans up. Equivalent to terminate + cleanup in one call.                                                  |

**Externally visible vs. internal-only states.** The REST API (`GET /v1/sessions/{id}`) returns session states from the **session/task state model** ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model), 8.8), not the pod state model ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). Pod states are internal implementation details not exposed to API callers.

| External session state (returned in API) | Description                                                   | Terminal? |
| ---------------------------------------- | ------------------------------------------------------------- | --------- |
| `created`                                | Session created; a warm pod has been claimed and credentials assigned (see [§7.1](07_session-lifecycle.md#71-normal-flow) steps 4–6), awaiting workspace file uploads or finalization. **TTL:** `maxCreatedStateTimeoutSeconds` (default 300s). On expiry the gateway transitions the session to `expired`, releases the pod claim back to the pool, and revokes the credential lease. `maxCreatedStateTimeoutSeconds` prevents stale sessions from accumulating indefinitely. Configurable via `gateway.maxCreatedStateTimeoutSeconds`. | No        |
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

Internal-only states (from the pod state machine in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) such as `warming`, `idle`, `claimed`, `receiving_uploads`, `running_setup`, `sdk_connecting`, and `resuming` are **never** returned in external API responses. These are tracked in the `Sandbox` CRD `.status.phase` for controller reconciliation and operational monitoring only.

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
| `POST` | `/v1/sessions/{id}/extend-retention` | Extend artifact retention TTL. Body: `{"ttlSeconds": <n>}`. See [Section 7.1](07_session-lifecycle.md#71-normal-flow).                                                        |
| `GET`  | `/v1/sessions/{id}/webhook-events`   | List undelivered webhook events after retry exhaustion. See [Section 14](14_workspace-plan-schema.md) (`callbackUrl` field).                                        |

**Blob resolution:**

| Method | Endpoint              | Description                                                                                                                                                                                                                                                                                                                                                                        |
| ------ | --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/blobs/{ref}`     | Resolve and download a `lenny-blob://` reference (see [Section 15.4.1](#1541-adapterbinary-protocol), `LennyBlobURI` scheme). `{ref}` is the full `lenny-blob://` URI, URL-encoded. The gateway verifies that the caller's identity has read access to the tenant and session embedded in the URI (`tenant_id`, `session_id` components) before retrieving the blob from the artifact store and streaming it back. Returns the blob bytes with the `Content-Type` header set to the blob's `mimeType`. Returns `404` if the blob has expired (`ttl` elapsed) or was never written; returns `403` if the caller lacks access to the owning session. REST adapter clients use this endpoint to dereference `ref` fields in `OutputPart` responses — external protocol adapters (MCP, OpenAI, A2A) MUST dereference `ref` fields internally and MUST NOT pass `lenny-blob://` URIs to external callers. |

**Async job support:**

| Method | Endpoint                     | Description                                                                                                                                     |
| ------ | ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/sessions/start`         | Accepts optional `callbackUrl` for completion notification                                                                                      |
| `POST` | `/v1/sessions/{id}/messages` | Send a message to a session (unified endpoint — replaces `send`). Gateway rejects injection against runtimes with `injection.supported: false`. |
| `GET`  | `/v1/sessions/{id}/messages` | List messages sent to or from a session (paginated). Returns message history including delivery receipts and state. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model).             |

**Discovery and introspection:**

| Method | Endpoint                         | Description                                                                                                                                           |
| ------ | -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/v1/runtimes`                   | List registered runtimes with full `agentInterface`, `mcpEndpoint`, `mcpCapabilities`, `adapterCapabilities`, capabilities, and labels. Identity-filtered and policy-scoped. |
| `GET`  | `/v1/runtimes/{name}/meta/{key}` | Get published metadata for a runtime (visibility-controlled)                                                                                          |
| `GET`  | `/.well-known/agent.json`        | **Post-V1 (A2A).** Aggregated A2A agent card discovery endpoint. Returns JSON array of all public `agent-card` entries (**intentional Lenny extension** — the A2A spec requires a single `AgentCard` object; see [Section 21.1](21_planned-post-v1.md) for rationale and per-runtime standard-compliant endpoints). No auth. |
| `GET`  | `/a2a/runtimes/{name}/.well-known/agent.json` | **Post-V1 (A2A).** Per-runtime A2A agent card endpoint. Returns a single `AgentCard` object conforming to the A2A spec (§3). Standard A2A clients that expect a single object SHOULD use this endpoint. No auth. See [Section 21.1](21_planned-post-v1.md). |
| `GET`  | `/v1/models`                     | OpenAI-compatible model list (identity-filtered)                                                                                                      |
| `GET`  | `/v1/pools`                      | List pools and warm pod counts                                                                                                                        |
| `GET`  | `/v1/usage`                      | Usage report (filterable by tenant, user, window, labels)                                                                                             |
| `GET`  | `/v1/metering/events`            | Paginated billing event stream                                                                                                                        |

**User credential management:**

| Method   | Endpoint                           | Description                                                                                                                                  |
| -------- | ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/v1/credentials`                                  | Register a credential for the authenticated user. One credential per provider; re-registering replaces. See [Section 4.9](04_system-components.md#49-credential-leasing-service) pre-authorized flow. |
| `GET`    | `/v1/credentials`                                  | List the authenticated user's registered credentials (no secret material returned).                                                          |
| `PUT`    | `/v1/credentials/{credential_ref}`                 | Rotate (replace) secret material for an existing credential. Active leases are immediately rotated. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                         |
| `POST`   | `/v1/credentials/{credential_ref}/revoke`          | Revoke a credential and immediately invalidate all active leases backed by it. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                                              |
| `DELETE` | `/v1/credentials/{credential_ref}`                 | Remove a registered credential. Active session leases are unaffected. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                                                      |

**Evaluation hooks:**

| Method | Endpoint                   | Description                                                                                                                     |
| ------ | -------------------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `POST` | `/v1/sessions/{id}/eval`   | Accept scored evaluator results (LLM-as-judge scores, custom heuristics, ground-truth comparisons). Stored as session metadata. |
| `POST` | `/v1/sessions/{id}/replay` | Re-run a session against a different runtime version using the same workspace and prompt history. See **Session Replay Semantics** below. |

#### Session Replay Semantics

`POST /v1/sessions/{id}/replay` creates a new independent session that replays the source session's prompt history against a different runtime version. This is the primary mechanism for regression testing and A/B evaluation of runtime upgrades.

**Request body:**

```json
{
  "targetRuntime": "<runtime-name>",
  "targetPool": "<pool-name (optional)>",
  "replayMode": "prompt_history | workspace_derive",
  "evalRef": "<eval-id (optional)>"
}
```

**Semantics:**

- **`replayMode: prompt_history`** (default): The source session's prompt history (from `GET /v1/sessions/{id}/transcript`) is replayed verbatim as the initial message sequence to the new session. The workspace is populated with the source session's sealed final workspace snapshot (or last checkpoint if the source session did not seal cleanly). The replayed session starts fresh — it has no knowledge of prior tool call results and will re-execute tool calls using the new runtime.
- **`replayMode: workspace_derive`**: Equivalent to `POST /v1/sessions/{id}/derive` with `targetRuntime` substituted. The new session starts with the source workspace but receives no pre-loaded prompt history. Use this mode when testing the new runtime's behavior from a clean start with an identical filesystem state.
- **`targetRuntime`** (required): The runtime name to replay against. Must be a registered runtime with the same `executionMode` as the source session. A different `executionMode` returns `400 INCOMPATIBLE_RUNTIME`.
- **`targetPool`** (optional): If omitted, the gateway selects the default pool for `targetRuntime`. Must be a pool backed by `targetRuntime`.
- **`evalRef`** (optional): If provided, links the replayed session to an experiment or eval set. The `evalRef` is recorded in the new session's metadata and can be used to filter `GET /v1/sessions` by eval context.

**Preconditions:** Source session must be in a terminal state (`completed`, `failed`, `cancelled`, `expired`) with a resolvable workspace snapshot. Non-terminal source sessions return `409 REPLAY_ON_LIVE_SESSION`.

**Response:** Returns the new session's `session_id`, `uploadToken`, and `sessionIsolationLevel` — identical to `POST /v1/sessions`. The new session proceeds through the standard lifecycle (upload → finalize → start → run).

**Credential handling:** The replayed session goes through standard `CredentialPolicy` evaluation ([Section 7.1](07_session-lifecycle.md#71-normal-flow), step 6) independently — credentials are never inherited from the source session.

**Comprehensive Admin API:**

All operational configuration is API-managed. Configuration is split into two planes:

**Operational plane — API-managed:** Runtimes, Delegation Policies, Connectors, Pools, Credential Pools, Tenants, Quotas (embedded in tenant records — managed via `PUT /v1/admin/tenants/{id}`), User Role Assignments, Experiments, External Adapters, Environments, Tenant RBAC Config.

**Bootstrap plane — Helm only:** DB URLs, Redis, MinIO, KMS, cluster name, namespace assignments, certificate paths, `LENNY_DEV_MODE`, system-wide defaults, Kubernetes object definitions, Memory Store implementation choice and backend connection config.

> **Note on items not listed above:** Egress Profiles are an enum field on pool and runtime definitions, managed through pool/runtime endpoints — they are not a separate CRUD resource. Scaling Policies are a sub-field within pool definitions (`scalePolicy`), managed through `PUT /v1/admin/pools/{name}` and `PUT /v1/admin/pools/{name}/warm-count`. Webhook delivery (`callbackUrl`) is a per-session field, not a platform-admin-managed subscription resource.

CRDs become derived state reconciled from Postgres by PoolScalingController.

All admin CRUD resources use `{name}` as the path identifier (human-readable, unique within scope). Tenants use `{id}` (opaque UUID) because tenant names are mutable display labels. This convention applies uniformly to every admin resource type below.

**Runtime and pool records are platform-global** (no `tenant_id` column, no RLS). Tenant-scoped visibility is enforced at the application layer via `runtime_tenant_access` and `pool_tenant_access` join tables. `platform-admin` callers receive unfiltered results; `tenant-admin` and `tenant-viewer` callers receive results filtered to the access-table entries for their tenant. Only `platform-admin` can create new runtime/pool definitions or grant access to a tenant; `tenant-admin` can update configuration for already-granted records only.

| Method   | Endpoint                                                        | Description                                                                                                                                                                                                                                                                                                                |
| -------- | --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/v1/admin/runtimes`                                            | Create a runtime definition (`platform-admin` only; creates a global record not yet visible to any tenant)                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/runtimes`                                            | List runtime definitions (application-layer filtered: `tenant-admin` sees own tenant's access-table entries; `platform-admin` sees all)                                                                                                                                                                                    |
| `GET`    | `/v1/admin/runtimes/{name}`                                     | Get a specific runtime definition (returns `404` if not in caller's access-table entries)                                                                                                                                                                                                                                  |
| `PUT`    | `/v1/admin/runtimes/{name}`                                     | Update a runtime definition (requires `If-Match`; `tenant-admin` restricted to access-table entries for own tenant)                                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/runtimes/{name}`                                     | Delete a runtime definition (`platform-admin` only)                                                                                                                                                                                                                                                                        |
| `POST`   | `/v1/admin/runtimes/{name}/tenant-access`                       | Grant a tenant access to a runtime. Body: `{"tenantId": "<uuid>"}`. Creates a `runtime_tenant_access` join-table entry. Idempotent — returns `200` if the grant already exists. Requires `platform-admin`.                                                                                                                 |
| `GET`    | `/v1/admin/runtimes/{name}/tenant-access`                       | List tenants with access to a runtime. Returns `[{"tenantId", "tenantName", "grantedAt", "grantedBy"}]`. Requires `platform-admin`.                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/runtimes/{name}/tenant-access/{tenantId}`            | Revoke a tenant's access to a runtime. Deletes the `runtime_tenant_access` join-table entry. Returns `404` if the grant does not exist. Requires `platform-admin`.                                                                                                                                                          |
| `POST`   | `/v1/admin/delegation-policies`                                 | Create a delegation policy                                                                                                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/delegation-policies`                                 | List all delegation policies                                                                                                                                                                                                                                                                                               |
| `GET`    | `/v1/admin/delegation-policies/{name}`                          | Get a specific delegation policy                                                                                                                                                                                                                                                                                           |
| `PUT`    | `/v1/admin/delegation-policies/{name}`                          | Update a delegation policy (requires `If-Match`)                                                                                                                                                                                                                                                                           |
| `DELETE` | `/v1/admin/delegation-policies/{name}`                          | Delete a delegation policy. Rejected with `RESOURCE_HAS_DEPENDENTS` if any runtime or active delegation lease references this policy (see [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease), deletion guard).                                                                                                                                                    |
| `POST`   | `/v1/admin/connectors`                                          | Create a connector definition                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/connectors`                                          | List all connector definitions                                                                                                                                                                                                                                                                                             |
| `GET`    | `/v1/admin/connectors/{name}`                                   | Get a specific connector definition                                                                                                                                                                                                                                                                                        |
| `PUT`    | `/v1/admin/connectors/{name}`                                   | Update a connector definition (requires `If-Match`)                                                                                                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/connectors/{name}`                                   | Delete a connector definition                                                                                                                                                                                                                                                                                              |
| `POST`   | `/v1/admin/connectors/{name}/test`                              | Live connectivity test: DNS, TLS, MCP handshake, auth validation (rate-limited: 10/min per connector)                                                                                                                                                                                                                      |
| `POST`   | `/v1/admin/pools`                                               | Create a pool configuration (`platform-admin` only; pool is platform-global until tenant access is granted)                                                                                                                                                                                                                |
| `GET`    | `/v1/admin/pools`                                               | List pool configurations (application-layer filtered: `tenant-admin` sees own tenant's access-table entries; `platform-admin` sees all)                                                                                                                                                                                    |
| `GET`    | `/v1/admin/pools/{name}`                                        | Get a specific pool configuration (returns `404` if not in caller's access-table entries)                                                                                                                                                                                                                                  |
| `PUT`    | `/v1/admin/pools/{name}`                                        | Update a pool configuration (requires `If-Match`; `tenant-admin` restricted to access-table entries for own tenant)                                                                                                                                                                                                        |
| `DELETE` | `/v1/admin/pools/{name}`                                        | Delete a pool configuration (`platform-admin` only)                                                                                                                                                                                                                                                                        |
| `POST`   | `/v1/admin/pools/{name}/drain`                                  | Drain a pool — transitions the pool to `draining` state, stops assigning warm pods to new sessions, and waits for in-flight sessions to complete or timeout before pod cleanup. **Backpressure for in-flight sessions:** while the pool is in `draining` state, any new `POST /v1/sessions` (or `create_session` MCP call) that would have selected this pool returns `503 POOL_DRAINING` with a `Retry-After: <seconds>` response header. The `Retry-After` value is computed as `ceil(estimated_drain_completion_seconds)` based on the longest active session age in the pool (capped at `maxSessionAgeSeconds`). Clients MUST respect `Retry-After` before retrying; the gateway rate-limits retry-after violations per client IP. The drain API response body includes `{"status": "draining", "activeSessions": <n>, "estimatedDrainSeconds": <n>}`. A `GET /v1/admin/pools/{name}` query returns `"phase": "draining"` and `"activeSessions": <n>` while drain is in progress. Metric: `lenny_pool_draining_sessions_total` (gauge, labeled by `pool`) tracks in-flight sessions during drain. |
| `GET`    | `/v1/admin/pools/{name}/sync-status`                            | Report CRD reconciliation state: `postgresGeneration`, `crdGeneration`, `lastReconciledAt`, `lagSeconds`, `inSync`                                                                                                                                                                                                         |
| `PUT`    | `/v1/admin/pools/{name}/warm-count`                             | Adjust minWarm/maxWarm at runtime                                                                                                                                                                                                                                                                                          |
| `PUT`    | `/v1/admin/pools/{name}/circuit-breaker`                        | Override the SDK-warm circuit-breaker state for a pool. Body: `{"sdkWarm": {"circuitBreakerOverride": "enabled" \| "disabled" \| "auto"}}`. Values: `enabled` — forces SDK-warm on regardless of demotion rate (use only after narrowing `sdkWarmBlockingPaths`); `disabled` — forces SDK-warm off regardless of demotion rate; `auto` — clears any override and restores automatic circuit-breaker control. Returns `409 INVALID_STATE_TRANSITION` if the pool's `sdkWarm.enabled` is `false` (circuit-breaker override has no effect on non-SDK-warm pools). Emits `pool.sdk_warm_circuit_breaker_override` audit event recording operator identity, previous state, and new value. Requires `platform-admin` or `tenant-admin` role. See [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) (SDK-warm circuit-breaker).                                             |
| `POST`   | `/v1/admin/pools/{name}/tenant-access`                          | Grant a tenant access to a pool. Body: `{"tenantId": "<uuid>"}`. Creates a `pool_tenant_access` join-table entry. Idempotent — returns `200` if the grant already exists. Requires `platform-admin`.                                                                                                                       |
| `GET`    | `/v1/admin/pools/{name}/tenant-access`                          | List tenants with access to a pool. Returns `[{"tenantId", "tenantName", "grantedAt", "grantedBy"}]`. Requires `platform-admin`.                                                                                                                                                                                           |
| `DELETE` | `/v1/admin/pools/{name}/tenant-access/{tenantId}`               | Revoke a tenant's access to a pool. Deletes the `pool_tenant_access` join-table entry. Returns `404` if the grant does not exist. Requires `platform-admin`.                                                                                                                                                               |
| `POST`   | `/v1/admin/credential-pools`                                    | Create a credential pool (tenant-scoped; `tenant-admin` sees own tenant's pools, `platform-admin` sees all with optional `?tenant_id=` filter)                                                                                                                                                                             |
| `GET`    | `/v1/admin/credential-pools`                                    | List credential pools (tenant-scoped)                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/credential-pools/{name}`                             | Get a specific credential pool                                                                                                                                                                                                                                                                                             |
| `PUT`    | `/v1/admin/credential-pools/{name}`                             | Update a credential pool (requires `If-Match`)                                                                                                                                                                                                                                                                             |
| `DELETE` | `/v1/admin/credential-pools/{name}`                             | Delete a credential pool                                                                                                                                                                                                                                                                                                   |
| `POST`   | `/v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | Emergency revocation of a single compromised credential; immediately invalidates all active leases backed by that credential and adds it to the credential deny list (see [Section 4.9](04_system-components.md#49-credential-leasing-service) Emergency Credential Revocation)                                                                                                     |
| `POST`   | `/v1/admin/credential-pools/{name}/credentials/{credId}/re-enable` | Re-enable a previously revoked pool credential. Restores credential to `healthy` status with a fresh health score. Requires `platform-admin`. Body: optional `reason` (string). Emits `credential.re_enabled` audit event (fields: `pool_id`, `credential_id`, `reason`, `re_enabled_by`). Use after emergency rotation to restore the original credential if the revocation was temporary. |
| `POST`   | `/v1/admin/credential-pools/{name}/revoke`                      | Emergency revocation of all credentials in a pool                                                                                                                                                                                                                                                                          |
| `POST`   | `/v1/admin/tenants`                                             | Create a tenant. Handler creates a per-tenant Postgres billing sequence: `CREATE SEQUENCE IF NOT EXISTS billing_seq_{tenant_id} START WITH 1 INCREMENT BY 1 NO CYCLE`. This sequence must exist before any billing event is written for the tenant. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                     |
| `GET`    | `/v1/admin/tenants`                                             | List all tenants                                                                                                                                                                                                                                                                                                           |
| `GET`    | `/v1/admin/tenants/{id}`                                        | Get a specific tenant                                                                                                                                                                                                                                                                                                      |
| `PUT`    | `/v1/admin/tenants/{id}`                                        | Update a tenant (requires `If-Match`)                                                                                                                                                                                                                                                                                      |
| `DELETE` | `/v1/admin/tenants/{id}`                                        | Delete a tenant                                                                                                                                                                                                                                                                                                            |
| `PUT`    | `/v1/admin/tenants/{id}/rbac-config`                            | Set tenant RBAC configuration                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/tenants/{id}/rbac-config`                            | Get tenant RBAC configuration                                                                                                                                                                                                                                                                                              |
| `GET`    | `/v1/admin/tenants/{id}/access-report`                          | Cross-environment access matrix                                                                                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/tenants/{id}/rotate-erasure-salt`                    | Rotate the tenant's billing pseudonymization salt. On rotation, the old salt is retained during a one-time re-hash migration job that re-pseudonymizes historical billing records under the new salt; the old salt is deleted only after migration completes. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                    |
| `GET`    | `/v1/admin/tenants/{id}/users`                                  | List users in a tenant with their platform-managed role assignments. Returns `user_id`, `role`, `assignedAt`, `assignedBy`. `tenant-admin` callers are scoped to their own tenant. Requires `platform-admin` or `tenant-admin`. |
| `PUT`    | `/v1/admin/tenants/{id}/users/{user_id}/role`                   | Assign or update the platform-managed role for a user within a tenant. Body: `{"role": "<role-name>"}`. Valid roles: `tenant-admin`, `tenant-viewer`, `billing-viewer`, `user`, or any custom role defined in the tenant RBAC config. The platform-managed assignment takes precedence over OIDC-derived roles (see Authorization and RBAC in [Section 10.2](10_gateway-internals.md#102-authentication)). Requires `platform-admin` or `tenant-admin`. Emits `user.role_assigned` audit event. |
| `DELETE` | `/v1/admin/tenants/{id}/users/{user_id}/role`                   | Remove the platform-managed role assignment for a user within a tenant. After removal, the user's effective role reverts to their OIDC-derived role (if any). Requires `platform-admin` or `tenant-admin`. Emits `user.role_removed` audit event. |
| `POST`   | `/v1/admin/tenants/{id}/roles`                                | Create a custom role. Body: `{"name": "<string>", "permissions": ["<operation>", ...]}`. Permissions must be a subset of `tenant-admin` permissions. Requires `platform-admin` or `tenant-admin`. |
| `GET`    | `/v1/admin/tenants/{id}/roles`                                | List custom roles for a tenant. Returns role name, permissions, `createdAt`, `updatedAt`. Requires `platform-admin` or `tenant-admin`. |
| `GET`    | `/v1/admin/tenants/{id}/roles/{name}`                         | Get a specific custom role. Requires `platform-admin` or `tenant-admin`. |
| `PUT`    | `/v1/admin/tenants/{id}/roles/{name}`                         | Update a custom role (requires `If-Match`). Body: `{"permissions": ["<operation>", ...]}`. Requires `platform-admin` or `tenant-admin`. |
| `DELETE` | `/v1/admin/tenants/{id}/roles/{name}`                         | Delete a custom role. Blocked if any users are assigned this role (`RESOURCE_HAS_DEPENDENTS`). Requires `platform-admin` or `tenant-admin`. |
| `POST`   | `/v1/admin/users/{user_id}/invalidate`                          | Terminate all active sessions for a user and revoke their tokens immediately. Used during incident response to stop an active attacker's sessions. Requires `platform-admin` or `tenant-admin` (scoped to own tenant). Emits `user.invalidated` audit event. See [Section 11.4](11_policy-and-controls.md#114-user-invalidation).                                             |
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
| `PATCH`  | `/v1/admin/experiments/{name}`                                  | Partial update of an experiment — canonical endpoint for status transitions (`active`, `paused`, `concluded`). Uses JSON Merge Patch. Requires `If-Match`. See [Section 10.7](10_gateway-internals.md#107-experiment-primitives).                                                                                                                                               |
| `DELETE` | `/v1/admin/experiments/{name}`                                  | Delete an experiment                                                                                                                                                                                                                                                                                                       |
| `GET`    | `/v1/admin/experiments/{name}/results`                          | Experiment results by variant. Returns per-variant session counts and eval score aggregates collected via `POST /v1/sessions/{id}/eval`. Requires `platform-admin` or `tenant-admin` role. See [Section 10.7](10_gateway-internals.md#107-experiment-primitives).                                                                                           |
| `POST`   | `/v1/admin/external-adapters`                                   | Register an external protocol adapter                                                                                                                                                                                                                                                                                      |
| `GET`    | `/v1/admin/external-adapters`                                   | List all external protocol adapters                                                                                                                                                                                                                                                                                        |
| `GET`    | `/v1/admin/external-adapters/{name}`                            | Get a specific external adapter                                                                                                                                                                                                                                                                                            |
| `PUT`    | `/v1/admin/external-adapters/{name}`                            | Update an external adapter (requires `If-Match`)                                                                                                                                                                                                                                                                           |
| `POST`   | `/v1/admin/external-adapters/{name}/validate`                   | Run the `RegisterAdapterUnderTest` compliance suite against the adapter in a sandboxed environment. Transitions `status` from `pending_validation` to `active` on success, or `validation_failed` (with per-test details) on failure. Required before an adapter receives traffic. See [Section 15.2.1](#1521-restmcp-consistency-contract).                                                                                                                                                                             |
| `DELETE` | `/v1/admin/external-adapters/{name}`                            | Delete an external adapter                                                                                                                                                                                                                                                                                                 |
| `GET`    | `/v1/admin/sessions/{id}`                                       | Get session state, metadata, and assigned pod for operator investigation. Returns the same session state model as `GET /v1/sessions/{id}` plus internal pod assignment and pool details. Requires `platform-admin`. See [Section 24.11](24_lenny-ctl-command-reference.md#2411-session-investigation).                                                                                      |
| `POST`   | `/v1/admin/sessions/{id}/force-terminate`                       | Force-terminate a session                                                                                                                                                                                                                                                                                                  |
| `POST`   | `/v1/admin/users/{user_id}/erase`                               | Initiate a GDPR user-level erasure job. Returns a job ID. Requires `platform-admin` or `tenant-admin` role. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                                                                                                              |
| `GET`    | `/v1/admin/erasure-jobs/{job_id}`                               | Query erasure job status: phase, completion percentage, time elapsed, errors. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/erasure-jobs/{job_id}/retry`                         | Retry a failed erasure job. The job must be in `failed` state. Requires `platform-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                                                                                                                                |
| `POST`   | `/v1/admin/erasure-jobs/{job_id}/clear-processing-restriction`  | Manually clear the `processing_restricted` flag for a user after a failed erasure job. Body: `{"justification": "<text>"}`. Operator identity and justification are recorded in the audit trail. Requires `platform-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                              |
| `POST`   | `/v1/admin/billing-corrections`                                 | Issue a billing correction event. Requires `platform-admin`. Body: `tenant_id`, `corrects_sequence`, `correction_reason_code` (enum), optional `correction_detail`, replacement values (`tokens_input`, `tokens_output`, `pod_minutes`). Returns the correction event with assigned `sequence_number`. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream). |
| `POST`   | `/v1/admin/bootstrap`                                           | Apply a seed file (idempotent upsert of runtimes, pools, tenants, etc.). Same schema as `bootstrap` Helm values. See [Section 17.6](17_deployment-topology.md#176-packaging-and-installation). Every invocation emits a `platform.bootstrap_applied` audit event (T3) recording: calling service account identity, seed file SHA-256 hash, resource changes summary (resource type, name, action: `created`/`updated`/`skipped`/`error`), and `dryRun: true/false`. The audit event is emitted even when `?dryRun=true` (with the dry-run flag set) so operators have a record of what a bootstrap run would have changed. The bootstrap Job's ServiceAccount is documented in [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) — it uses a minimal-RBAC ServiceAccount scoped to `lenny-system` with no cluster-wide permissions. |
| `POST`   | `/v1/admin/legal-hold`                                          | Set or clear a legal hold on a session or artifact. Body: `{"resourceType": "session"\|"artifact", "resourceId": "<id>", "hold": true\|false, "note": "<string> (required when hold is true)"}`. Requires `platform-admin` or `tenant-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                             |
| `GET`    | `/v1/admin/legal-holds`                                         | List active legal holds. Query params: `?tenant_id=`, `?resource_type=session\|artifact`, `?resource_id=`. Returns paginated list with fields: `resourceType`, `resourceId`, `setBy`, `setAt`, `note`. `tenant-admin` callers are automatically scoped to their own tenant. Requires `platform-admin` or `tenant-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                  |
| `POST`   | `/v1/admin/tenants/{id}/force-delete`                           | Force-delete a tenant that has active legal holds. Body: `{"justification": "<text>"}`. Operator identity and justification are recorded in the audit trail. Requires `platform-admin`. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces).                                                                                                                   |
| `DELETE` | `/v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial` | Clear the extension-denied flag on a session subtree immediately, bypassing the rejection cool-off window. `rootSessionId` is the `root_session_id` of the delegation tree (the `session_id` of the root session that originated the tree). `sessionId` is the `session_id` of the denied subtree to clear. Requires `platform-admin` or `tenant-admin`. See [Section 8.6](08_recursive-delegation.md#86-lease-extension).                                                                                                                                                   |
| `POST`   | `/v1/admin/pools/{name}/upgrade/start`                          | Begin rolling image upgrade for a pool. Body: `{"newImage": "<digest>"}`. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy) (`RuntimeUpgrade` state machine).                                                                                                                                                                                               |
| `POST`   | `/v1/admin/pools/{name}/upgrade/proceed`                        | Advance to next upgrade phase. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                            |
| `POST`   | `/v1/admin/pools/{name}/upgrade/pause`                          | Pause upgrade state machine. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                              |
| `POST`   | `/v1/admin/pools/{name}/upgrade/resume`                         | Resume paused upgrade. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                                    |
| `POST`   | `/v1/admin/pools/{name}/upgrade/rollback`                       | Rollback in-progress upgrade. Body: optional `{"restoreOldPool": true}` for late-stage rollback. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                          |
| `GET`    | `/v1/admin/pools/{name}/upgrade-status`                         | Show upgrade state and progress. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy).                                                                                                                                                                                                                                                                          |
| `DELETE` | `/v1/admin/pools/{name}/bootstrap-override`                     | Remove the bootstrap `minWarm` override and switch to formula-driven scaling. See [Section 17.8.2](17_deployment-topology.md#1782-capacity-tier-reference).                                                                                                                                                                                                                           |
| `POST`   | `/v1/admin/credential-pools/{name}/credentials`                 | Add a credential to a pool. See [Section 24.5](24_lenny-ctl-command-reference.md#245-credential-management).                                                                                                                                                                                                                                                                               |
| `POST`   | `/v1/admin/quota/reconcile`                                     | Re-aggregate in-flight session usage from Postgres into Redis after Redis recovery. See [Section 24.6](24_lenny-ctl-command-reference.md#246-quota-operations).                                                                                                                                                                                                                       |
| `POST`   | `/v1/admin/users/{user_id}/rotate-token`                        | Rotate admin token and patch Kubernetes Secret. See [Section 24.9](24_lenny-ctl-command-reference.md#249-user-and-token-management).                                                                                                                                                                                                                                                           |
| `POST`   | `/v1/admin/billing-corrections/{id}/approve`                    | Approve a pending billing correction. Requires `platform-admin`; submitter cannot approve their own request (self-approval rejected). See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                                                   |
| `POST`   | `/v1/admin/billing-corrections/{id}/reject`                     | Reject a pending billing correction. Requires `platform-admin`; submitter cannot reject their own request (self-rejection rejected). The correction remains in `billing_correction_pending` state with `rejected` outcome for audit purposes and is never promoted to the billing stream. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                |
| `POST`   | `/v1/admin/billing-correction-reasons`                          | Add a deployer-defined `correction_reason_code` to the closed enum. Body: `{"code": "<string>", "description": "<string>"}`. Requires `platform-admin`. Audit-logged. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                  |
| `GET`    | `/v1/admin/billing-correction-reasons`                          | List all `correction_reason_code` values (built-in and deployer-added). Requires `platform-admin`. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                                                                                     |
| `DELETE` | `/v1/admin/billing-correction-reasons/{code}`                   | Remove a deployer-added `correction_reason_code`. Built-in codes cannot be deleted. Requires `platform-admin`. Audit-logged. See [Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream).                                                                                                                                                                            |
| `POST`   | `/v1/admin/preflight`                                           | Run preflight checks (Postgres, Redis, MinIO connectivity and schema version). POST because the endpoint performs active outbound connectivity probes — it is not idempotent or side-effect-free. See [Section 17.6](17_deployment-topology.md#176-packaging-and-installation).                                                                                                         |
| `GET`    | `/v1/admin/schema/migrations/status`                            | Return the current expand-contract migration phase for each active migration: `version`, `phase` (`phase1_applied` \| `phase2_deployed` \| `phase3_applied` \| `complete`), `appliedAt`, `gateCheckResult` (for Phase 3: `pass`, `fail:<N>_rows`, or `not_run`), and `migrationJobName` (the Kubernetes Job that applied it). See [Section 24.13](24_lenny-ctl-command-reference.md#2413-migration-management). Requires `platform-admin`. |

**Additional operational endpoints** are defined in [Section 24](24_lenny-ctl-command-reference.md) (`lenny-ctl` command reference), each with its REST API mapping. The table above includes all endpoints; [Section 24](24_lenny-ctl-command-reference.md) provides CLI wrappers and usage examples.

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

Fields: `code` (string, required) — machine-readable error code from the table below. `category` (string, required) — one of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM` as defined in [Section 16.3](16_observability.md#163-distributed-tracing). `message` (string, required) — human-readable description. `retryable` (boolean, required) — whether the client should retry. `details` (object, optional) — additional context; structure varies by error code.

**Error code catalog:**

| Code                        | Category    | HTTP Status | Description                                                                                                                                                                                                                                              |
| --------------------------- | ----------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `VALIDATION_ERROR`          | `PERMANENT` | 400         | Request body or query parameters failed validation                                                                                                                                                                                                       |
| `INVALID_STATE_TRANSITION`  | `PERMANENT` | 409         | Requested operation is not valid for the current resource state                                                                                                                                                                                          |
| `RESOURCE_NOT_FOUND`        | `PERMANENT` | 404         | The requested resource does not exist or is not visible to the caller                                                                                                                                                                                    |
| `RESOURCE_ALREADY_EXISTS`   | `PERMANENT` | 409         | A resource with the given identifier already exists                                                                                                                                                                                                      |
| `RESOURCE_HAS_DEPENDENTS`   | `PERMANENT` | 409         | Resource cannot be deleted because it is referenced by active dependents. `details.dependents` lists blocking references by type, name, count, and (where a stable identifier exists) an `ids` array of up to 20 individual resource IDs. When more than 20 dependents of a given type exist, the array is truncated and `truncated: true` is set on that entry. |
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
| `SCOPE_DENIED`              | `POLICY`    | 403         | Inter-session message rejected because the sender's effective `messagingScope` does not permit messaging the target session. Returned as the `error` reason in a `delivery_receipt` event. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model).                                                                                                                         |
| `MCP_VERSION_UNSUPPORTED`   | `PERMANENT` | 400         | Client MCP version is not supported                                                                                                                                                                                                                      |
| `IMAGE_RESOLUTION_FAILED`   | `PERMANENT` | 422         | Container image reference is invalid or could not be resolved. `details.image` contains the unresolvable reference; `details.reason` describes the failure (e.g., `invalid_digest`, `tag_not_found`, `registry_unreachable`).                            |
| `RESERVED_IDENTIFIER`       | `PERMANENT` | 422         | A field value uses a platform-reserved identifier (e.g., variant `id: "control"`). `details.field` identifies the offending field; `details.value` is the reserved value that was rejected.                                                              |
| `CONFIGURATION_CONFLICT`    | `PERMANENT` | 422         | The requested configuration contains mutually incompatible field values. `details.conflicts` is an array of `{"fields": ["fieldA", "fieldB"], "message": "..."}` entries describing each incompatibility.                                                |
| `SEED_CONFLICT`             | `PERMANENT` | 409         | A bootstrap/seed upsert conflicts with an existing resource in a non-idempotent way and `--force-update` was not set. `details.resource` identifies the conflicting resource by type and name; `details.conflictingFields` lists the fields that differ. |
| `INTERCEPTOR_TIMEOUT`       | `TRANSIENT` | 503         | An external interceptor did not respond within its configured timeout. `details.interceptor_ref`, `details.phase`, and `details.timeout_ms` are included. Returned when `failPolicy: fail-closed`; suppressed (request proceeds) when `failPolicy: fail-open`. Distinct from `LLM_REQUEST_REJECTED` (which indicates a deliberate REJECT decision, not a timeout). See [Section 4.8](04_system-components.md#48-gateway-policy-engine). |
| `INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION` | `POLICY` | 400 | An external interceptor returned `MODIFY` with changes to immutable fields (e.g., `user_id`, `tenant_id`). `details.interceptor_ref`, `details.phase`, and `details.violated_fields` are included. The modification is rejected and the original payload is preserved. See [Section 4.8](04_system-components.md#48-gateway-policy-engine). |
| `LLM_REQUEST_REJECTED`      | `PERMANENT` | 403         | LLM request rejected by `PreLLMRequest` interceptor. `details.reason` contains the interceptor's rejection reason. Proxy mode only ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                      |
| `LLM_RESPONSE_REJECTED`     | `PERMANENT` | 502         | LLM response rejected by `PostLLMResponse` interceptor. `details.reason` contains the interceptor's rejection reason. Proxy mode only ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                    |
| `CONNECTOR_REQUEST_REJECTED` | `PERMANENT` | 403         | Connector tool call rejected by `PreConnectorRequest` interceptor. `details.reason` contains the interceptor's rejection reason ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                           |
| `CONNECTOR_RESPONSE_REJECTED` | `PERMANENT` | 502         | Connector response rejected by `PostConnectorResponse` interceptor. `details.reason` contains the interceptor's rejection reason ([Section 4.8](04_system-components.md#48-gateway-policy-engine)).                                                                                                          |
| `INTERNAL_ERROR`            | `TRANSIENT` | 500         | Unexpected server error                                                                                                                                                                                                                                  |
| `WARM_POOL_EXHAUSTED`       | `TRANSIENT` | 503         | No idle pods are available in the warm pool after exhausting both the API-server claim path and the Postgres fallback. Client should retry with exponential backoff. See [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle).                                                                  |
| `INVALID_INTERCEPTOR_PRIORITY` | `PERMANENT` | 422      | External interceptor registration specifies `priority ≤ 100`, which is reserved for built-in security-critical interceptors. Set `priority > 100`. See [Section 4.8](04_system-components.md#48-gateway-policy-engine).                                                                                     |
| `INVALID_INTERCEPTOR_PHASE` | `PERMANENT` | 422         | External interceptor registration includes the `PreAuth` phase, which is exclusively reserved for built-in interceptors. Remove `PreAuth` from the phase set. See [Section 4.8](04_system-components.md#48-gateway-policy-engine).                                                                          |
| `ISOLATION_MONOTONICITY_VIOLATED` | `POLICY` | 403       | Delegation rejected because the target pool's isolation profile is less restrictive than the calling session's `minIsolationProfile`. `details.parentIsolation` and `details.targetIsolation` identify the conflicting profiles. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease).       |
| `CREDENTIAL_PROVIDER_MISMATCH`    | `POLICY`  | 422       | Cross-environment delegation with `credentialPropagation: inherit` rejected because the parent's credential pool providers and the child runtime's `supportedProviders` have no intersection. Use `credentialPropagation: independent` for cross-environment delegations where the runtimes use different providers. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `DEADLOCK_TIMEOUT`          | `TRANSIENT` | 504         | A delegated subtree deadlock was not resolved within `maxDeadlockWaitSeconds`. The deepest blocked tasks have been failed. The root task may retry after breaking the deadlock. See [Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema).                                                         |
| `SESSION_NOT_EVAL_ELIGIBLE` | `PERMANENT` | 422         | Eval submission rejected because the target session is in a terminal state (`cancelled` or `expired`) that is not eligible for eval storage. See [Section 10.7](10_gateway-internals.md#107-experiment-primitives).                                                                                 |
| `EVAL_QUOTA_EXCEEDED`       | `POLICY`    | 429         | The per-session `EvalResult` storage cap has been reached (`maxEvalsPerSession`, default 10,000). `details.sessionId` and `details.limit` are included. See [Section 10.7](10_gateway-internals.md#107-experiment-primitives).                                                                      |
| `STORAGE_QUOTA_EXCEEDED`    | `POLICY`    | 429         | Tenant artifact storage quota would be exceeded by the upload or checkpoint write. `details.currentBytes` and `details.limitBytes` are included. See [Section 11.2](11_policy-and-controls.md#112-budgets-and-quotas).                                                                                      |
| `CREDENTIAL_RENEWAL_FAILED` | `TRANSIENT` | 503         | All credential renewal retries were exhausted before the active lease expired. The session is entering the credential fallback flow. `details.provider` identifies the affected credential provider. See [Section 4.9](04_system-components.md#49-credential-leasing-service).                                    |
| `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` | `PERMANENT` | 422  | A stored `WorkspacePlan` uses a `schemaVersion` higher than this gateway version understands. Workspace materialization is blocked. `details.knownVersion` and `details.encounteredVersion` are included. See [Section 14](14_workspace-plan-schema.md).                               |
| `BUDGET_STATE_UNRECOVERABLE` | `TRANSIENT` | 503        | A delegation tree's budget state could not be reconstructed after Redis recovery (Postgres checkpoint too stale and coordinating gateway replica was also lost). The root session is moved to `awaiting_client_action`. See [Section 11.2](11_policy-and-controls.md#112-budgets-and-quotas).                |
| `PERMISSION_DENIED`         | `POLICY`    | 403         | The authenticated identity lacks the required permission for this specific resource or operation. Distinguished from `FORBIDDEN` (role-level rejection) in that `PERMISSION_DENIED` is policy-evaluated at the resource level (e.g., delegation scope, policy rule). |
| `CREDENTIAL_REVOKED`        | `POLICY`    | 403         | The credential backing the active session lease has been explicitly revoked (placed on the deny list). Active sessions using this credential are terminated immediately; no further requests can be made with the revoked credential. See [Section 4.9](04_system-components.md#49-credential-leasing-service).   |
| `INVALID_POOL_CONFIGURATION` | `PERMANENT` | 422        | Pool creation or update rejected due to an invalid configuration constraint (e.g., `cleanupTimeoutSeconds / maxConcurrent < 5`, or `terminationGracePeriodSeconds` too small for the tiered checkpoint cap). `details.message` describes the violated constraint. See [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle). |
| `CIRCUIT_BREAKER_OPEN`      | `POLICY`    | 503         | Session creation or delegation rejected because an operator-declared circuit breaker is active. `details.circuit_name`, `details.reason`, and `details.opened_at` are included. Not `retryable` — the client should wait for the circuit breaker to be closed by an operator before retrying. See [Section 11.6](11_policy-and-controls.md#116-circuit-breakers). |
| `POOL_DRAINING`             | `TRANSIENT` | 503         | Session creation rejected because the target pool is in `draining` state and is no longer accepting new sessions. `Retry-After` header indicates estimated drain completion. `details.pool` and `details.estimatedDrainSeconds` are included. See [Section 15.1](#151-rest-api) (pool drain). |
| `DELEGATION_CYCLE_DETECTED` | `PERMANENT` | 400         | Delegation rejected because the target's resolved `(runtime_name, pool_name)` identity tuple appears in the caller's delegation lineage, which would create a circular wait. `details.cycleRuntimeName` and `details.cyclePoolName` identify the offending identity. Not retryable — the caller must choose a different target. See [Section 8.2](08_recursive-delegation.md#82-delegation-mechanism). |
| `OUTPUTPART_TOO_LARGE`      | `PERMANENT` | 413         | An `OutputPart` payload exceeds the per-part size limit (50 MB). The part was rejected at ingress. `details.partIndex`, `details.sizeBytes`, and `details.limitBytes` are included. See [Section 15.4.1](#1541-adapterbinary-protocol). |
| `REQUEST_INPUT_TIMEOUT`     | `TRANSIENT` | 504         | A `lenny/request_input` call blocked longer than `maxRequestInputWaitSeconds` without receiving a response. Delivered as a tool-call error to the blocking runtime. `details.requestId` and `details.timeoutSeconds` are included. See [Section 11.3](11_policy-and-controls.md#113-timeouts-and-cancellation). |
| `ERASURE_IN_PROGRESS`       | `POLICY`    | 403         | Session creation rejected because the target `user_id` has a pending GDPR erasure job and `processing_restricted: true` is set. `details.userId` and `details.jobId` are included. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `URL_MODE_ELICITATION_DOMAIN_REQUIRED` | `PERMANENT` | 400 | Pool registration or update rejected because `urlModeElicitation.enabled: true` was set without a non-empty `domainAllowlist`. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) (elicitation). |
| `DUPLICATE_MESSAGE_ID`      | `PERMANENT` | 400         | A sender-supplied message `id` is not globally unique within the tenant — a message with the same ID was received within the deduplication window. `details.duplicateId` is included. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `UNREGISTERED_PART_TYPE`    | `WARNING`   | —           | An `OutputPart` carries an unprefixed `type` not present in the current platform-defined registry. The part is passed through with a custom-type-to-`text` fallback and an `unregistered_platform_type` warning annotation. Third-party types should use the `x-<vendor>/` namespace prefix. `details.type` is included. See [Section 15.4.1](#1541-adapterbinary-protocol). |
| `REPLAY_ON_LIVE_SESSION`    | `PERMANENT` | 409         | `POST /v1/sessions/{id}/replay` rejected because the source session is not in a terminal state. The source session must be `completed`, `failed`, `cancelled`, or `expired`. See [Section 15.1](#151-rest-api) (session replay). |
| `INCOMPATIBLE_RUNTIME`      | `PERMANENT` | 400         | `POST /v1/sessions/{id}/replay` rejected because `targetRuntime` has a different `executionMode` than the source session. Replay requires matching execution mode. `details.sourceExecutionMode` and `details.targetExecutionMode` are included. See [Section 15.1](#151-rest-api) (session replay). |
| `DOMAIN_NOT_ALLOWLISTED`    | `POLICY`    | 403         | An agent-initiated URL-mode elicitation was dropped because the URL's effective host does not match any entry in the pool's `urlModeElicitation.domainAllowlist`. `details.host` and `details.allowlist` are included. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) (elicitation). |
| `COMPLIANCE_PGAUDIT_REQUIRED` | `PERMANENT` | 422       | Tenant creation or update rejected because the tenant's `complianceProfile` requires `audit.pgaudit.enabled: true` with a configured `sinkEndpoint`, but these are not set. Configure pgaudit before creating a regulated tenant. See [Section 11.7](11_policy-and-controls.md#117-audit-logging). |
| `DERIVE_ON_LIVE_SESSION`      | `PERMANENT` | 409         | `POST /v1/sessions/{id}/derive` rejected because the source session is not in a terminal state and `allowStale: true` was not set in the request body. See [Section 15.1](#151-rest-api) (derive semantics). |
| `DERIVE_LOCK_CONTENTION`      | `POLICY`    | 429         | `POST /v1/sessions/{id}/derive` rejected because too many concurrent derive operations are in progress for this session. Retry with exponential backoff. See [Section 15.1](#151-rest-api) (derive semantics). |
| `DERIVE_SNAPSHOT_UNAVAILABLE` | `TRANSIENT` | 503         | `POST /v1/sessions/{id}/derive` failed because the referenced workspace snapshot object was not found in object storage (e.g., deleted by a GC bug or premature TTL expiry). Retrying immediately is unlikely to help; the caller should wait and retry or derive from a different source state. `details.snapshotRef` includes the missing object path. See [Section 15.1](#151-rest-api) (derive semantics). |
| `REGION_CONSTRAINT_VIOLATED`  | `POLICY`    | 403         | Request rejected because the resolved storage region does not satisfy the session's `dataResidencyRegion` constraint. `details.requiredRegion` and `details.resolvedRegion` are included. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `REGION_CONSTRAINT_UNRESOLVABLE` | `PERMANENT` | 422      | Session creation rejected because no storage or pool configuration can satisfy the requested `dataResidencyRegion`. `details.region` identifies the unresolvable constraint. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `REGION_UNAVAILABLE`          | `TRANSIENT` | 503         | The storage region required by the session's data residency constraint is temporarily unavailable. Retry when the region recovers. `details.region` identifies the affected region. See [Section 12.8](12_storage-architecture.md#128-compliance-interfaces). |
| `KMS_REGION_UNRESOLVABLE`     | `PERMANENT` | 422         | Session or credential operation rejected because no KMS key is configured for the required region. `details.region` and `details.provider` are included. See [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `LEASE_SPIFFE_MISMATCH`       | `POLICY`    | 403         | A pod presented a SPIFFE identity that does not match the credential lease's expected identity (`details.expectedSpiffeId`, `details.actualSpiffeId`). The lease is invalidated. See [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `ENV_VAR_BLOCKLISTED`         | `PERMANENT` | 400         | Session creation or runtime registration rejected because one or more requested environment variables are on the platform blocklist. `details.blocklisted` lists the offending variable names. See [Section 14](14_workspace-plan-schema.md). |
| `INPUT_TOO_LARGE`             | `PERMANENT` | 413         | Delegation rejected because `TaskSpec.input` exceeds `contentPolicy.maxInputSize`. `details.sizeBytes` and `details.limitBytes` are included. Not retryable — the caller must reduce input size. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `CONTENT_POLICY_WEAKENING`    | `POLICY`    | 403         | Delegation rejected because the child lease sets `contentPolicy.interceptorRef: null` when the parent had a non-null reference. Removing a content check is always a weakening and is not permitted. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` | `POLICY` | 403  | Delegation rejected because the child lease names a different `contentPolicy.interceptorRef` than the parent without retaining the parent's reference. A child lease may not substitute the parent's named interceptor with an unrelated one. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease). |
| `COMPLIANCE_SIEM_REQUIRED`    | `POLICY`    | 422         | Tenant creation or update rejected because the tenant's `complianceProfile` requires a SIEM endpoint (`audit.siem.endpoint`) to be configured, but it is not set. See [Section 11.7](11_policy-and-controls.md#117-audit-logging). |
| `BUDGET_EXHAUSTED`            | `POLICY`    | 429         | Delegation or lease extension rejected because the remaining token budget or tree-size budget is insufficient. `details.limitType` is `token_budget`, `tree_size`, or `tree_memory` (distinguishing the exhausted resource). `TOKEN_BUDGET_EXHAUSTED` and `TREE_SIZE_EXCEEDED` are internal Lua script result codes; the wire error code is always `BUDGET_EXHAUSTED`. Not retryable without a budget extension. See Sections 8.3, 8.6. |
| `OUTPUTPART_INLINE_REF_CONFLICT` | `PERMANENT` | 400      | An `OutputPart` has both `inline` and `ref` fields set, which are mutually exclusive. Set exactly one field: `inline` for direct byte embedding or `ref` for external blob storage reference. See [Section 15.4.1](#1541-adapterbinary-protocol). |
| `INVALID_DELIVERY_VALUE`      | `PERMANENT` | 400         | A message delivery envelope contains an unrecognized `delivery` field value. Valid values are `queued` and `immediate`. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `SDK_DEMOTION_NOT_SUPPORTED`  | `PERMANENT` | 422         | Session creation failed because the pool uses SDK-warm mode (`preConnect: true`) and the adapter does not implement the `DemoteSDK` RPC. The workspace includes files from `sdkWarmBlockingPaths` that require demotion, but demotion is unavailable. Runtime authors must implement `DemoteSDK` before declaring `preConnect: true`. See [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like). |
| `ELICITATION_NOT_FOUND`       | `PERMANENT` | 404         | `respond_to_elicitation` or `dismiss_elicitation` rejected because the `(session_id, user_id, elicitation_id)` triple does not match any pending elicitation. The ID is unknown, belongs to a different session, or belongs to a different user. 404 is returned in all mismatch cases to avoid leaking the existence of elicitations in other sessions. See [Section 9.2](09_mcp-integration.md#92-elicitation-chain). |
| `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` | `POLICY` | 400  | Pool registration or update rejected because `cacheScope: tenant` was set on a pool whose `complianceProfile` is a regulated value (`hipaa`, `fedramp`). Cross-user cache sharing is prohibited under these compliance profiles. Use `cacheScope: per-user` (default). See [Section 4.9](04_system-components.md#49-credential-leasing-service). |
| `IDEMPOTENCY_KEY_REUSED`    | `PERMANENT` | 422         | An idempotency key was reused with a different request body. Each idempotency key must correspond to a single unique request. See [Section 11.5](11_policy-and-controls.md#115-idempotency). |
| `UPLOAD_TOKEN_EXPIRED`        | `PERMANENT` | 401         | The upload token's TTL has elapsed (`session_creation_time + maxCreatedStateTimeoutSeconds`). The client must create a new session. See [Section 7.1](07_session-lifecycle.md#71-normal-flow). |
| `UPLOAD_TOKEN_MISMATCH`       | `PERMANENT` | 403         | The upload token's embedded `session_id` does not match the target session. Tokens are session-scoped and cannot be reused across sessions. See [Section 7.1](07_session-lifecycle.md#71-normal-flow). |
| `UPLOAD_TOKEN_CONSUMED`       | `PERMANENT` | 410         | The upload token has already been invalidated by a successful `FinalizeWorkspace` call. Replay of a consumed token is not permitted. See [Section 7.1](07_session-lifecycle.md#71-normal-flow). |
| `TARGET_NOT_READY`            | `TRANSIENT` | 409         | Inter-session message rejected because the target session is in a pre-running state (`created`, `ready`, `starting`, `finalizing`) and has no inbox. Retry after the session transitions to `running`. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |
| `CROSS_TENANT_MESSAGE_DENIED` | `POLICY`    | 403         | Inter-session message rejected because the sender and target sessions belong to different tenants. Cross-tenant messaging is unconditionally prohibited. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model). |

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
| `Retry-After`           | Seconds to wait before retrying (present on `429` and `503` responses) |

**`dryRun` query parameter.** Most admin `POST` and `PUT` endpoints accept `?dryRun=true`. Exceptions: action endpoints (`drain`, `force-terminate`, `warm-count`) and `DELETE` endpoints do not support `dryRun` — see below. Behavior: the gateway performs full request validation — schema, field constraints, referential integrity, policy checks, and quota evaluation — but **does not persist** the result or trigger any side effects (no CRD reconciliation, no pool scaling, no webhook dispatch). Audit events are **not** emitted for dry-run requests, with one exception: `POST /v1/admin/bootstrap?dryRun=true` emits a `platform.bootstrap_applied` audit event with `dryRun: true` so operators have a record of what a bootstrap run would have changed (see [Section 15.1](#151-rest-api) bootstrap endpoint). **`dryRun` never makes outbound network calls.** All referential integrity checks performed under `dryRun` are syntactic and against locally cached state only — for example, connector `mcpServerUrl` validation checks URL format and scheme allowlist but does not attempt a network connection. Live connectivity verification requires the dedicated `POST /v1/admin/connectors/{name}/test` endpoint (see below). The response body is identical to a non-dry-run success response (including the computed resource representation), with one addition: the response includes the header `X-Dry-Run: true`.

**Endpoint-specific `dryRun` semantics:**

- **Connectors (`POST /v1/admin/connectors`, `PUT /v1/admin/connectors/{name}`):** Validates URL format, scheme allowlist (`https` only in production), authentication field structure, and referential integrity against known environments. Does **not** perform DNS resolution, TLS handshake, or any outbound call to the connector endpoint. For live reachability testing, use `POST /v1/admin/connectors/{name}/test` (described below).
- **Experiments (`POST /v1/admin/experiments`, `PUT /v1/admin/experiments/{name}`):** Validates experiment definition, variant weight constraint (Σ variant_weights must be in [0, 1) — remainder is reserved for the control group), and runtime/pool references. Capacity validation is **not** included — `dryRun` does not query current pool utilization or node availability. Capacity feasibility is evaluated asynchronously by the PoolScalingController when the experiment is activated. To pre-check capacity, use `GET /v1/admin/pools/{name}` to inspect current `availableCount` and `warmCount` before activating an experiment.
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

  `matchedRuntimes` and `matchedConnectors` list all resources whose labels satisfy the environment's selectors at the time of the dry run. `unmatchedSelectorTerms` lists any selector terms that matched zero resources (useful for detecting typos in label keys or values). This preview is the primary mechanism for the Environment Management UI ([Section 21.5](21_planned-post-v1.md)).

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
| `PUT`  | `/v1/admin/environments/{name}`        | Validates update, returns `preview` with matched runtimes/connectors ([Section 21.5](21_planned-post-v1.md)) |
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
- **Deletion semantics for resources with dependents.** Deleting a resource that is referenced by active dependents is **blocked** (not cascaded). The gateway returns `409` with error code `RESOURCE_HAS_DEPENDENTS` and a `details.dependents` array listing the blocking references. Each entry in `details.dependents` includes `type`, `name` or `count`, and (where applicable) an `ids` array of up to 20 individual resource IDs — set `truncated: true` on the entry when the total count exceeds 20. Specific rules per resource type:
  - **Runtime:** blocked if referenced by any active pool (`status != draining/drained`) or any non-terminal session. `details.dependents` example: `[{"type": "pool", "name": "default-pool", "count": 1, "ids": ["default-pool"]}, {"type": "session", "state": "running", "count": 3, "ids": ["sess-abc", "sess-def", "sess-ghi"]}]`.
  - **Pool:** blocked if any sessions are running or suspended in the pool. Drain the pool first (`POST /v1/admin/pools/{name}/drain`), then delete once all sessions complete.
  - **Delegation Policy:** blocked if referenced by any runtime or derived runtime definition (`delegationPolicyRef`), or by any active (non-terminal) delegation lease (`delegationPolicyRef` or `maxDelegationPolicy`). Remove the reference from runtimes and wait for active leases to reach a terminal state before deleting. See [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) (deletion guard).
  - **Connector:** blocked if referenced by any environment or runtime. Remove the reference first.
  - **Credential Pool:** blocked if any active credential leases exist. Revoke leases first.
  - **Tenant:** blocked if any non-terminal sessions, pools, or credential pools exist under the tenant. All child resources must be removed first.
  - **Environment:** blocked if any sessions are active within the environment.
  - **Experiment:** blocked if `status: active` or `status: paused` and any non-terminal sessions have an `experimentContext` referencing this experiment. When `paused`, variant pools may still have in-flight sessions (see PoolScalingController behavior in [Section 10.7](10_gateway-internals.md#107-experiment-primitives)). Transition the experiment to `concluded` and wait for all enrolled sessions to reach a terminal state before deleting.
  - **External Adapter:** blocked if `status: active`. Set to `inactive` first.
- **Implementation.** The Postgres `UPDATE ... WHERE id = $1 AND version = $2` pattern ensures atomicity without application-level locking. If zero rows are affected, the gateway re-reads the current version and returns `412`.

Rate limits are applied per tenant and per user. Admin API endpoints have separate (higher) rate-limit windows from client-facing endpoints.

**Cursor-based pagination.** All list endpoints return paginated results using a cursor-based envelope. This applies to: `GET /v1/sessions`, `GET /v1/runtimes`, `GET /v1/pools`, `GET /v1/metering/events`, `GET /v1/sessions/{id}/artifacts`, `GET /v1/sessions/{id}/transcript`, `GET /v1/sessions/{id}/logs`, and all admin `GET` collection endpoints (e.g., `/v1/admin/runtimes`, `/v1/admin/pools`). Note: `GET /v1/admin/experiments/{name}/results` is **not** a paginated list endpoint — it returns a single aggregated object per experiment (see [Section 10.7](10_gateway-internals.md#107-experiment-primitives)).

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
  "hasMore": true,
  "total": 1247
}
```

Fields: `items` (array, required) — the page of results. `cursor` (string, nullable) — opaque cursor to pass as the `cursor` query parameter to fetch the next page; `null` when there are no more results. `hasMore` (boolean, required) — `true` if additional pages exist beyond this one. `total` (integer, optional) — total count of matching items across all pages, present only when cheaply computable (i.e., available from a cached count or inexpensive `COUNT(*)` query). Omitted when the count would require a full table scan or is otherwise expensive to compute. UIs may use `total` to render "X results found" or pagination progress indicators; they must not rely on its presence.

Cursors are opaque, URL-safe strings. They encode the sort key and unique tiebreaker (typically `id`) to guarantee stable iteration even when new items are inserted. Cursors are valid for 24 hours; expired cursors return `VALIDATION_ERROR` with `details.fields[0].rule: "cursor_expired"`. Clients must not parse or construct cursors — they are an internal implementation detail.

**`GET /v1/usage` response schema.** Note: `GET /v1/usage` is an aggregated endpoint (like `GET /v1/admin/experiments/{name}/results`), not a paginated list endpoint. It returns a single aggregated object and does not use the cursor-based pagination envelope.

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

**Compatibility policy:** Lenny supports the two most recent stable MCP spec versions simultaneously. When a new MCP spec version is adopted, the oldest supported version enters a 6-month deprecation window. The gateway emits a `X-Lenny-Mcp-Version-Deprecated` warning header on connections using the deprecated version. (Header uses hyphens per RFC 7230; underscore-named headers are dropped by some proxies.)

**Session-lifetime exception for deprecated versions.** When a version exits the deprecation window (i.e., the gateway drops support for it on the 6-month boundary), connections that are already established and mid-session at that instant MUST NOT be forcibly terminated. The gateway enforces the following rule: version support removal applies only to **new** connection negotiations — any `MCPAdapter` connection that completed `initialize` handshake before the deprecation deadline is permitted to continue for the duration of its session (up to `maxSessionAgeSeconds`, [Section 11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)). Concretely:
- The gateway maintains a per-connection `negotiatedVersion` field set at `initialize` time.
- Version enforcement checks at message dispatch time use `negotiatedVersion` — not the current supported-version set — to route to the correct serialization logic.
- When the deprecated version's handler is scheduled for removal (deployment of the gateway binary that drops the old version), the `lenny-preflight` Job emits a warning if any sessions older than 1 hour are active on the deprecated version (`lenny_mcp_deprecated_version_active_sessions` gauge). Operators must drain these sessions (via graceful terminate + resume on the new version) before the deployment to avoid a mid-session protocol mismatch.
- If a session on the deprecated version is still active after the deployment (i.e., the operator did not drain), the gateway falls back to the nonce-handshake-only serialization path with a `schema_version_ahead` degradation annotation rather than terminating the session abruptly.

**MCP features used:**

- Tasks (for long-running session lifecycle and delegation)
- Elicitation (for user prompts, auth flows)
- Streamable HTTP transport

#### 15.2.1 REST/MCP Consistency Contract

The REST API ([Section 15.1](#151-rest-api)) and MCP tools ([Section 15.2](#152-mcp-api)) intentionally overlap for operations like session creation, status queries, and artifact retrieval. Five rules govern this overlap:

1. **Semantic equivalence.** REST and MCP endpoints that perform the same operation (e.g., `POST /v1/sessions` and `create_session` MCP tool) must return semantically identical responses. Both API surfaces share a common service layer in the gateway so that business logic, validation, and response shaping are implemented exactly once.

2. **Tool versioning.** MCP tool schema evolution is governed by [Section 15.5](#155-api-versioning-and-stability) (API Versioning and Stability), item 2.

3. **Shared error taxonomy.** All error responses — REST and MCP — use the error categories defined in [Section 16.3](16_observability.md#163-distributed-tracing) (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`). REST errors return a JSON body: `{"error": {"code": "QUOTA_EXCEEDED", "category": "POLICY", "message": "...", "retryable": false}}`. MCP tool errors use the same `code` and `category` fields inside the MCP error response format, so clients can apply a single error-handling strategy regardless of API surface.

4. **OpenAPI as source of truth.** The REST API's OpenAPI spec is the single authoritative schema for all overlapping operations. MCP tool schemas for overlapping operations (e.g., `create_session`, `get_session_status`, `list_artifacts`) are generated from the OpenAPI spec's request/response definitions, not maintained independently. A code generation step in the build pipeline produces MCP tool JSON schemas from OpenAPI operation definitions, ensuring structural consistency by construction. Any manual MCP-only tool (e.g., `lenny/delegate_task`) that has no REST counterpart is authored independently but must use the shared error taxonomy (item 3).

5. **Contract testing.** CI includes contract tests that call the REST endpoint and **every built-in external adapter** (MCP, OpenAI Completions, Open Responses) for every overlapping operation and assert both structural and behavioral equivalence of responses. These tests cover:

   (a) **Success paths** — identical response payloads modulo transport envelope.

   (b) **Validation errors** — same error `code` and `category` for identical invalid inputs.

   (c) **Authz rejections** — same denial behavior.

   (d) **Behavioral equivalence — `retryable` and `category` flags.** For every error condition exercised in (b) and (c), the `retryable` flag and error `category` must be identical across REST and all adapter surfaces. A transient error that is `retryable: true` on REST must be `retryable: true` on MCP and every other adapter. This prevents silent breakage of client retry logic when switching API surfaces.

   (e) **Behavioral equivalence — session state transitions.** After performing an identical sequence of operations (e.g., create session, interrupt session), `GET /v1/sessions/{id}` and the `get_session_status` MCP tool must return the same session state. The contract tests include a set of fixed operation sequences that exercise all externally visible state transitions (see [Section 7.2](07_session-lifecycle.md#72-interactive-session-model)) and assert state identity across surfaces.

   (f) **Behavioral equivalence — pagination.** For overlapping list operations (e.g., listing artifacts), default page size, cursor semantics, and empty-result shapes must be identical across REST and adapter surfaces. Adapters must not silently return a different subset of results for the same query.

   Contract tests run on every PR; a failure blocks merge. The test harness is introduced in Phase 5 ([Section 18](18_build-sequence.md)) alongside the first phase where both REST and MCP surfaces are active.

   **REST-only operations.** The following REST endpoints intentionally have no MCP tool equivalents: `POST /v1/sessions/{id}/derive`, `POST /v1/sessions/{id}/replay`, `POST /v1/sessions/{id}/extend-retention`, and `POST /v1/sessions/{id}/eval`. Rationale: `derive` and `replay` are developer workflow operations typically driven by CI pipelines or human operators, not by agents mid-session. `extend-retention` is an administrative lifecycle action. `eval` is a post-hoc scoring endpoint called by external pipelines ([Section 10.7](10_gateway-internals.md#107-experiment-primitives)). MCP-first clients needing these operations should use the REST API directly — the gateway accepts both surfaces on the same authentication credentials.

   **`RegisterAdapterUnderTest` test matrix.** The contract test harness exposes a `RegisterAdapterUnderTest(adapter ExternalProtocolAdapter)` entry point so that third-party adapter authors can run the suite against their implementation. The test matrix covers the full operation set of overlapping endpoints:
   - All session lifecycle operations: create, get status, interrupt, resume, terminate, list artifacts, retrieve artifact.
   - All error classes: `VALIDATION_ERROR`, `QUOTA_EXCEEDED`, `RATE_LIMITED`, `RESOURCE_NOT_FOUND`, `INVALID_STATE_TRANSITION`, `PERMISSION_DENIED`, `CREDENTIAL_REVOKED`, `CREDENTIAL_POOL_EXHAUSTED`, `ISOLATION_MONOTONICITY_VIOLATED` — each exercised with a canonical triggering input. For each, the test asserts identical `code`, `category`, and `retryable` values.
   - All state transition sequences: at minimum the sequences `create→running→completed`, `create→running→interrupted→resumed→completed`, and `create→running→terminated`.
   - Pagination: multi-page artifact list traversal asserting cursor behavior and total result set identity.

   Third-party adapters that do not pass this full matrix **must not be enabled in production**. The `POST /v1/admin/external-adapters` registration endpoint enforces this gate: a new adapter is created in `status: pending_validation` and will not receive traffic until `POST /v1/admin/external-adapters/{name}/validate` is called and returns a passing result. `POST /v1/admin/external-adapters/{name}/validate` runs the `RegisterAdapterUnderTest` suite in a sandboxed environment against the registered adapter and transitions the adapter to `status: active` on success or `status: validation_failed` (with per-test failure details) on failure. Adapters in `pending_validation` or `validation_failed` status are excluded from all traffic routing. This makes compliance testable without out-of-band coordination, and makes the production gate machine-enforceable rather than a documentation requirement.

### 15.3 Internal Control API (Custom Protocol)

Gateway ↔ Pod communication over gRPC + mTLS. See [Section 4.7](04_system-components.md#47-runtime-adapter) (Runtime Adapter) for the full RPC surface. Protobuf service definitions are documented in [Section 15.4](#154-runtime-adapter-specification), which serves as the interim authoritative reference until the standalone runtime adapter specification is published in Phase 2.

### 15.4 Runtime Adapter Specification

> **Status:** The standalone adapter specification has not yet been published. It is targeted for **Phase 2** (see [Section 18](18_build-sequence.md)). Until it is released, **this section (15.4 and its subsections) is the authoritative interim reference for community runtime adapter authors.** All wire encoding details, error codes, message types, and integration guidance provided here are normative and will be carried forward into the standalone spec without breaking changes.

The runtime adapter contract will be published as a **standalone specification** with:

- Protobuf `.proto` service and message definitions
- Error code enum with categories (transient, permanent, policy)
- Streaming message type definitions for `Attach` bidirectional stream
- Version negotiation protocol (adapter advertises capabilities at startup; gateway selects compatible protocol version)
- Health check contract (gRPC Health Checking Protocol)
- Reference implementation in Go

When published, the standalone specification will supersede this section as the primary document for community runtime adapter authors. Until then, the subsections below (15.4.1 through 15.4.5) provide complete, self-sufficient guidance.

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

| `type` field             | Description                                                                                           |
| ------------------------ | ----------------------------------------------------------------------------------------------------- |
| `response`               | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |
| `tool_call`              | Agent requests execution of a tool. Carries `slotId` in concurrent-workspace mode.                    |
| `heartbeat_ack`          | Acknowledges an inbound `heartbeat`. Protocol-level; no content payload.                              |
| `status`                 | Optional status/trace update                                                                          |
| `set_tracing_context`    | Registers tracing identifiers for propagation through delegation. Payload: `{"type": "set_tracing_context", "context": {"langsmith_run_id": "run_abc123"}}`. The adapter stores the context and automatically attaches it to all subsequent `lenny/delegate_task` gRPC requests. Validation rules ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) are enforced by the gateway when the delegation request arrives. Available at all tiers. See [Section 16.3](16_observability.md#163-distributed-tracing) for the two-tier tracing model. |

**`input_required` outbound message type removed.** Replaced by `lenny/request_input` blocking MCP tool call on the platform MCP server.

**`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This allows multiple independent concurrent task streams through a single stdin channel.

**Task mode between-task signaling:** Adapter sends `{type: "task_complete", taskId: "..."}` on the lifecycle channel after a task completes. The runtime releases task-specific resources and replies with `{type: "task_complete_acknowledged", taskId: "..."}`. After deployer-defined `cleanupCommands` and Lenny scrub complete, the adapter sends `{type: "task_ready", taskId: "..."}` with the new task's ID. The runtime re-reads the adapter manifest (regenerated per task) and the next `{type: "message"}` on stdin is the start of the new task. This is distinct from `terminate`, which always means process exit.

**stderr** is captured by the adapter for logging and diagnostics but is **not** parsed as protocol messages.

**stdout flushing requirement:** Every JSON Lines message written to stdout MUST be followed by a flush before the binary blocks on the next `read_line(stdin)`. Many language runtimes buffer stdout by default; without an explicit flush the adapter never receives the message and the session hangs silently. Language-specific guidance:

| Language | Required action |
| -------- | --------------- |
| Python   | `sys.stdout.flush()` after each `print()`, or open stdout with `sys.stdout = io.TextIOWrapper(sys.stdout.buffer, line_buffering=True)` |
| Node.js  | Use `process.stdout.write(line + "\n")` — Node's stdout is line-buffered when connected to a pipe, but only unbuffered when writing synchronously; always call the callback or await the write before blocking on stdin |
| Ruby     | `$stdout.sync = true` at startup |
| Java     | Use `PrintStream` with `autoFlush=true`: `new PrintStream(System.out, true)` |
| Go       | Use `bufio.NewWriter(os.Stdout)` with an explicit `Flush()` call after each `WriteString`, or write directly to `os.Stdout` (unbuffered by default) |
| Rust     | Call `stdout.flush()` from `std::io::Write` after each write, or use `BufWriter` with explicit flush |
| C / C++  | `fflush(stdout)` after each `fputs`/`printf`, or set `setbuf(stdout, NULL)` at startup for unbuffered mode |

Runtimes that use a line-buffered or fully-buffered stdout MUST flush after every outbound message. The reference Go implementation (`examples/runtimes/echo/`) writes directly to `os.Stdout` (unbuffered) and requires no explicit flush call.

#### Internal `OutputPart` Format

`agent_text` streaming event is replaced by `agent_output` carrying `OutputPart` array. `TaskResult` and `TaskSpec` use `OutputPart` arrays. This is Lenny's internal content model — the adapter translates to/from external protocol formats (MCP, A2A) at the boundary.

```json
{
  "schemaVersion": 1,
  "id": "part_abc123",
  "type": "text",
  "mimeType": "text/plain",
  "inline": "content here",
  "ref": "lenny-blob://...",
  "annotations": { "role": "primary", "final": true },
  "parts": [],
  "status": "streaming | complete | failed"
}
```

**Properties:**

- **`schemaVersion` is an integer identifying the OutputPart schema revision (default `1`).** Present on every persisted `OutputPart`. The forward-compatibility contract has obligations on both sides:
  - **Producer obligation:** Producers MUST set `schemaVersion` to the highest version required by the fields they emit. When a schema version introduces semantically important fields (e.g., `citations` in v2), the producer MUST set `schemaVersion` to that version so consumers can detect the presence of fields they may not understand.
  - **Consumer obligation — streaming/live delivery:** Consumers MUST NOT reject an `OutputPart` solely because its `schemaVersion` is higher than the consumer understands. When a consumer encounters a `schemaVersion` it does not recognize, it processes the fields it does understand and MUST surface a **degradation signal**: a `schema_version_ahead` annotation on the parent `MessageEnvelope` (with `"knownVersion"` and `"encounteredVersion"` fields) so the end user or upstream caller is informed that the response may be incomplete. Consumers MUST NOT silently discard unknown fields without this signal. This ensures data loss from schema mismatch is always visible rather than hidden.
  - **Consumer obligation — durable storage (TaskRecord):** When `OutputPart` arrays are persisted as part of a `TaskRecord` ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)), the forward-read rule from [Section 15.5](#155-api-versioning-and-stability) item 7 applies: if a reader encounters an `OutputPart` with a `schemaVersion` it does not recognize, it MUST **forward-read** — process all fields it understands and preserve all unknown fields verbatim (pass-through) — rather than rejecting the record. Billing and audit records retained for 13 months will span multiple schema revisions; silent data loss or outright rejection in these records is unacceptable. If a durable consumer cannot safely pass through unknown fields (e.g., it writes to a schema-strict sink), it MUST emit a `durable_schema_version_ahead` structured error to an operator alert channel and queue the record for manual review rather than dropping it. This rule is consistent with the general durable-consumer rule in [Section 15.5](#155-api-versioning-and-stability) item 7 and extends it explicitly to `OutputPart` arrays embedded within persisted `TaskRecord` objects.
- **`type` is an open string — not a closed enum — with a versioned canonical type registry.** The registry defines platform-defined types and their guaranteed translation behavior per adapter. Unprefixed names are reserved for the platform registry; third-party extensibility uses the `x-<vendor>/` namespace (see namespace convention below). Any type not in the current registry version is treated as a custom type and falls back to `text` with the original type preserved in `annotations.originalType`. Types may be added to the registry in minor releases; removing a type or changing its translation behavior is a breaking change requiring a major version bump. To preserve forward-compatibility across minor releases, unknown unprefixed types are **not** rejected at ingress — they are passed through with the same custom-type fallback, plus an `unregistered_platform_type` warning annotation, so that a newly registered type can be emitted by an updated runtime before all gateways have been upgraded. This retains open-string extensibility while making translation deterministic across adapter implementations.

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

  **Namespace convention for third-party types.** To avoid collisions with future platform-defined types, all vendor- or community-defined custom types MUST use a reverse-DNS namespace prefix in the form `x-<vendor>/<typeName>` (e.g., `x-acme/heatmap`, `x-myorg/audio-transcript`). Unprefixed names are reserved for platform-defined registry types. The gateway logs and annotates unknown unprefixed types at ingress (adding an `unregistered_platform_type` warning annotation with the unrecognized type string) but does **not** reject them — they fall through to the standard custom-type-to-`text` collapse so that newly registered types introduced in a minor release are forward-compatible across gateway versions that have not yet been upgraded.

  **`schemaVersion` per-type contract.** The `schemaVersion` field on an `OutputPart` is scoped to the envelope schema (field set, semantics of existing fields). The stable field set guaranteed at each registry version is:

  | Type               | `schemaVersion` 1 — guaranteed fields                                               | Notes on future versions                                              |
  | ------------------ | ----------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
  | `text`             | `type`, `inline`, `mimeType` (`text/plain`)                                         | v2 may add `citations[]`                                              |
  | `code`             | `type`, `inline`, `mimeType`, `annotations.language`                                | —                                                                     |
  | `reasoning_trace`  | `type`, `inline`                                                                    | v2 may add structured `steps[]`                                       |
  | `citation`         | `type`, `inline`, `annotations.source`                                              | —                                                                     |
  | `screenshot`       | `type`, `inline` (base64) or `ref`, `mimeType` (image/*)                            | —                                                                     |
  | `image`            | `type`, `inline` (base64) or `ref`, `mimeType` (image/*)                            | —                                                                     |
  | `diff`             | `type`, `inline`, `annotations.language` (`diff`)                                   | —                                                                     |
  | `file`             | `type`, `inline` or `ref`, `mimeType`                                               | —                                                                     |
  | `execution_result` | `type`, `parts[]` (each part is a full `OutputPart`)                                | v2 may add `exitCode`, `duration`                                     |
  | `error`            | `type`, `inline` (human-readable message), `annotations.errorCode` (optional)       | —                                                                     |

  A producer emitting fields that were introduced in a later schema version MUST set `schemaVersion` to that version. Consumers that encounter a `(type, schemaVersion)` combination they do not recognize apply the forward-compatibility rules defined above: degradation signal for live delivery; forward-read with unknown-field preservation for durable storage (see [Section 15.5](#155-api-versioning-and-stability) item 7).

- **`mimeType` handles encoding separately.** The gateway validates, logs, and routes based on MIME type without understanding semantics.
- **`inline` vs `ref` — resolution protocol.** A part either contains bytes inline (`inline` field set, base64 for binary content) or references external blob storage (`ref` field set). The two fields are mutually exclusive on any given part; setting both is a validation error (`400 OUTPUTPART_INLINE_REF_CONFLICT`). The gateway selects the representation automatically based on part size:

  | Part size | Gateway action | Consumer sees |
  | --- | --- | --- |
  | ≤ 64 KB | Store inline (base64 for binary, UTF-8 for text) | `inline` field populated; `ref` absent |
  | > 64 KB and ≤ 50 MB | Stage to blob store; set `ref` to `LennyBlobURI` | `ref` populated; `inline` absent |
  | > 50 MB | Rejected at ingress | `413 OUTPUTPART_TOO_LARGE` |

  **`LennyBlobURI` scheme.** Blob references use the URI scheme `lenny-blob://`:

  ```
  lenny-blob://{tenant_id}/{session_id}/{part_id}?ttl={seconds}&enc=aes256gcm
  ```

  | Component | Description |
  | --- | --- |
  | `tenant_id` | Tenant namespace — prevents cross-tenant dereference |
  | `session_id` | Originating session — scopes the blob to one session |
  | `part_id` | Stable part identifier (matches `OutputPart.id`) |
  | `ttl` | Seconds until the blob expires in storage (see TTL table below) |
  | `enc` | Encryption indicator; always `aes256gcm` for stored blobs |

  **Immutability guarantee.** Blob storage is write-once per `(tenant_id, session_id, part_id)` triple. The gateway writes a blob exactly once when staging an `OutputPart`; subsequent reads always return the same bytes. No `generation` component is needed in the URI because part IDs are globally unique within a session — the internal `coordination_generation` counter ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)) is used only for coordinator fencing and never causes part IDs to be reused or existing blobs to be overwritten. A `lenny-blob://` URI is safe to cache and share for the duration of its `ttl`.

  **TTL policy by context:**

  | Context | Default TTL | Configurable? |
  | --- | --- | --- |
  | Live streaming delivery (session active) | 3 600 s (1 h) | Yes — `blobStore.liveDeliveryTtlSeconds` |
  | Persisted in `TaskRecord` | 2 592 000 s (30 d) | Yes — `blobStore.taskRecordTtlSeconds` |
  | Audit / billing event payload | 34 128 000 s (13 months) | Yes — `blobStore.auditTtlSeconds` |
  | Delegation export (parent → child) | Duration of child session + 1 h | Fixed |

  **Consumer fallback obligation.** When a consumer encounters a `ref` it cannot dereference (blob expired, storage unavailable, network partition), it MUST:
  1. Surface a `blob_ref_unresolvable` degradation annotation on the `MessageEnvelope` (fields: `partId`, `ref`, `reason`).
  2. Substitute a placeholder `OutputPart` of type `error` with `inline: "Blob reference unresolvable: {reason}"`.
  3. Never silently drop the part.

  **Adapter dereference obligation.** External protocol adapters (MCP, OpenAI, A2A) MUST dereference `ref` fields before serializing outbound messages to external clients — external protocols do not speak `lenny-blob://`. The REST adapter passes `ref` values through as-is (REST clients may dereference directly via `GET /v1/blobs/{ref}`).
- **`annotations` as an open metadata map.** `role`, `confidence`, `language`, `final`, `audience` — any metadata. The gateway can index and filter on annotations without understanding the part type.
- **`parts` for nesting.** Compound outputs (e.g., `execution_result` containing code, stdout, stderr, chart) are first-class.
- **`id` enables part-level streaming updates** — concurrent part delivery where text streams while an image renders.

**Rationale for internal format over MCP content blocks directly:** Runtimes are insulated from external protocol evolution. When MCP adds new block types or A2A parts change, only the gateway's `ExternalProtocolAdapter` translation layer updates — runtimes are untouched.

**MCP content block → OutputPart mapping (inbound translation):** When the gateway receives MCP content blocks from a client and delivers them to a runtime, the adapter translates each MCP block to an `OutputPart` as follows:

| MCP content block type | → `OutputPart.type` | `OutputPart.inline` source                  | `OutputPart.mimeType`          | `OutputPart.ref` source   | Notes                                                             |
| ---------------------- | ------------------- | ------------------------------------------- | ------------------------------ | ------------------------- | ----------------------------------------------------------------- |
| `TextContent`          | `text`              | `text` field                                | `text/plain`                   | —                         | `language` annotation → `annotations.language` if present        |
| `ImageContent` (url)   | `image`             | —                                           | from `mimeType` if present     | `url.url`                 | URL set as `ref`; inline not populated                            |
| `ImageContent` (base64)| `image`             | base64 data string                          | `mimeType`                     | —                         | Stored inline                                                     |
| `EmbeddedResource` (text blob) | `file`    | resource text content                       | `text/plain` or resource MIME  | —                         | Stored inline when small; large blobs staged to artifact store    |
| `EmbeddedResource` (blob)      | `file`    | —                                           | resource MIME type             | artifact URI              | Staged to artifact store; `ref` set to `lenny-blob://` URI        |
| `EmbeddedResource` (uri)       | `file`    | —                                           | resource MIME type             | resource URI              | `ref` set directly from resource URI                              |
| MCP `isError: true` annotation | `error`   | inherited from enclosing block              | —                              | —                         | `type` overridden to `error`; `annotations.errorCode` populated if present |

Runtime authors who produce output using MCP-familiar content block objects can use the `from_mcp_content()` helper (see below) to perform this translation without manual field mapping.

**Minimum required fields for Minimum-tier runtimes:** Only `type` and `inline` are required. All other fields (`schemaVersion`, `id`, `mimeType`, `ref`, `annotations`, `parts`, `status`) are optional and have sensible defaults — `schemaVersion` defaults to `1` if absent, `id` is generated by the adapter if absent, `mimeType` defaults to `text/plain` for `type: "text"`, `status` defaults to `complete` for non-streaming responses. A minimal valid `OutputPart` is `{"type": "text", "inline": "hello"}`.

**Simplified text-only response shorthand:** Minimum-tier runtimes may emit a simplified response form with a top-level `text` field instead of an `output` array:

```json
{ "type": "response", "text": "The answer is 4." }
```

The adapter normalizes this to the canonical form `{"type": "response", "output": [{"type": "text", "inline": "The answer is 4."}]}` before forwarding to the gateway. This shorthand is strictly equivalent — runtimes that need structured output (multiple parts, non-text types, annotations) use the full `output` array form.

**Optional SDK helper `from_mcp_content(blocks)`** converts MCP content blocks to `OutputPart` arrays for runtime authors who want to produce output using familiar MCP formats. Availability:

- **Go:** Ships in the `github.com/lenny-platform/lenny-sdk-go/outputpart` package (Phase 2 deliverable). Import the package and call `outputpart.FromMCPContent(blocks)`.
- **Other languages:** Not yet published as a library. Use the mapping table above to implement the conversion inline — the logic is a straightforward switch on `content.type`. A copy-paste reference implementation will be included in the runtime adapter specification (Phase 2).
- **No SDK required:** Runtimes can construct `OutputPart` objects directly without any Lenny SDK dependency. The SDK helper is a convenience only.

#### Translation Fidelity Matrix

Each `ExternalProtocolAdapter` translates between `OutputPart` and its wire format. The following matrix documents field-level fidelity for each built-in adapter. Round-trip through adapters that mark a field as **`[lossy]`** or **`[dropped]`** is not reversible — callers that require full fidelity should use the REST adapter or persist `OutputPart` directly.

**Fidelity tag legend:**

| Tag | Meaning |
| --- | --- |
| `[exact]` | Field round-trips with no information loss. |
| `[lossy]` | Field is representable in the target protocol but some information is lost or transformed; the original value cannot be fully reconstructed from the wire form alone. |
| `[dropped]` | Field has no representation in the target protocol and is not present on the wire. A round-trip ingests the field back with a default value. |
| `[unsupported]` | Field semantics are fundamentally incompatible with the target protocol. No mapping attempt is made; the field is silently omitted. Use `protocolHints` to influence fallback behavior. |
| `[extended]` | Field carries richer semantics in the Lenny internal model than the target protocol can represent; extra information is preserved in a protocol extension (annotation, metadata, sidecar) that conformant clients may ignore. |

| `OutputPart` field | MCP | OpenAI Completions | Open Responses | REST | A2A |
| --- | --- | --- | --- | --- | --- |
| `schemaVersion` | **`[dropped]`** — MCP content blocks have no version field; re-added with default on ingest. Round-trip: inbound always reconstructed as version 1. | **`[dropped]`** — not representable; re-added with default on ingest. Round-trip asymmetric: version information permanently lost. | **`[dropped]`** — Responses API output items carry no schema version field; re-added with default on ingest. Round-trip asymmetric: version information permanently lost. | **`[exact]`** | **`[lossy]`** — mapped to A2A `metadata.schemaVersion` string; survives round-trip but as string, not integer. |
| `id` | **`[extended]`** — mapped to MCP `partId` annotation; preserved in extension, ignored by non-Lenny MCP clients. | **`[dropped]`** — no per-content-block ID in Chat Completions. Round-trip: adapter generates new IDs on ingest; original IDs permanently lost. | **`[extended]`** — mapped to Responses API `output[].id`; preserved on outbound and recoverable on inbound for top-level output items. Nested part IDs within composite outputs are not preserved. | **`[exact]`** | **`[exact]`** — mapped to A2A `partId`. |
| `type` | **`[lossy]`** — platform-defined types (see Canonical Type Registry) mapped to nearest MCP block type (`text`, `image`, `resource`); custom types (not in registry) collapsed to `text` with original type preserved in `annotations.originalType`. `reasoning_trace` type has no native MCP representation — collapsed to `TextContent` with `thinking` annotation; round-trip loses semantic typing. | **`[lossy]`** — everything becomes `text` or `image_url`; custom types and `reasoning_trace` collapsed to `text` with no type recovery on round-trip. `thinking` content (from `reasoning_trace` parts) becomes indistinguishable from regular text. | **`[lossy]`** — text, image, and file output types map natively; `reasoning_trace` mapped to `output_text` with a `reasoning` role annotation. Custom types not in the Canonical Type Registry collapse to `output_text` with no type recovery on inbound. | **`[exact]`** | **`[lossy]`** — platform-defined types mapped to A2A part kinds per registry; custom types placed in `metadata.originalType`. `reasoning_trace` → A2A `TextPart` with `metadata.semantic: "reasoning"`; recoverable on ingest if consumer reads `metadata.semantic`. |
| `mimeType` | **`[exact]`** — carried in `resource` or `image` block metadata. | **`[lossy]`** — only `image/*` MIME types preserved via `image_url`; all other MIME types dropped. Non-image blobs become opaque `text` entries with no MIME recovery. | **`[lossy]`** — `image/*` and well-known file MIME types preserved via `output_image` and file output items; uncommon MIME types collapsed to generic file output with no MIME recovery on inbound. | **`[exact]`** | **`[exact]`** — A2A parts carry `mimeType` natively. |
| `inline` | **`[exact]`** | **`[exact]`** (as `content` string or base64 for images) | **`[exact]`** (as `text` or base64-encoded `image` content) | **`[exact]`** | **`[exact]`** |
| `ref` (`lenny-blob://` URI) | **`[dropped]`** — adapters dereference `lenny-blob://` URIs and inline the resolved content before sending to external MCP clients (see SCH-005 resolution protocol). Round-trip: ref scheme permanently lost; content inlined. If blob is expired at send time, the part is replaced with an error part. | **`[dropped]`** — no URI reference in Chat Completions; adapter resolves `ref` to inline before sending. Round-trip: ref scheme permanently lost; content inlined. If blob is expired at send time, the part is replaced with an error part. | **`[dropped]`** — no `lenny-blob://` URI reference in Responses API; adapter resolves `ref` to inline before sending. Round-trip: ref scheme permanently lost; content inlined. If blob is expired at send time, the part is replaced with an error part. | **`[exact]`** — REST clients may dereference via `GET /v1/blobs/{ref}`. | **`[lossy]`** — mapped to A2A `artifact.uri`; `lenny-blob://` scheme rewritten to a gateway-issued HTTPS URL. Scheme is not recoverable from the wire form. |
| `annotations` | **`[lossy]`** — well-known keys (`role`, `final`, `audience`) mapped to MCP annotation fields; unknown keys placed in `metadata` extension if the MCP client negotiated metadata support, otherwise `[dropped]`. Round-trip: unknown annotation keys are lost for clients that do not support MCP metadata extensions. | **`[dropped]`** — no annotation mechanism in Chat Completions. All annotation keys permanently lost on outbound; not recovered on inbound. | **`[dropped]`** — no per-output annotation mechanism in the Open Responses Specification. All annotation keys permanently lost on outbound; not recovered on inbound. | **`[exact]`** | **`[lossy]`** — mapped to A2A `metadata` map; nested objects flattened to JSON strings. Nested structure not recoverable on round-trip. |
| `parts` (nesting) | **`[lossy]`** — flattened to sequential MCP content blocks with `parentId` annotation; one level of nesting reconstructible on ingest if `parentId` is present. Deeper nesting permanently flattened. | **`[dropped]`** — flattened to sequential content entries; nesting structure not recoverable on round-trip. | **`[dropped]`** — Responses API output items are flat; nesting structure not representable and not recoverable on round-trip. | **`[exact]`** | **`[lossy]`** — A2A supports one nesting level via composite parts; deeper nesting flattened. Round-trip: nesting beyond one level permanently lost. |
| `status` | **`[lossy]`** — mapped to MCP streaming progress events; `failed` mapped to `isError`; `streaming` and `complete` distinctions partially recoverable via SSE stream termination signals. Per-part granularity partially preserved. | **`[dropped]`** — Chat Completions has `finish_reason` only at message level; per-part status not representable. | **`[lossy]`** — `failed` status mapped to an output item with `status: "failed"`; `streaming` and `complete` distinctions partially recoverable via SSE streaming events. Per-part status granularity partially preserved; better than Chat Completions but not exact. | **`[exact]`** | **`[lossy]`** — mapped to A2A task state; per-part status granularity lost (only terminal state survives). |
| `protocolHints` | **`[dropped]`** — consumed by adapter before serialization; intentionally not sent on wire. Never recovered on ingest. | **`[dropped]`** — consumed by adapter before serialization. | **`[dropped]`** — consumed by adapter before serialization. | **`[exact]`** | **`[dropped]`** — consumed by adapter before serialization. |

**Round-trip asymmetry summary.** The following fields have asymmetric round-trips (outbound → external protocol → inbound produces a different value than the original):

| Field | Adapter | Asymmetry | Impact |
| --- | --- | --- | --- |
| `schemaVersion` | MCP, OpenAI Completions, Open Responses | Always reconstructed as `1` on inbound regardless of original value | Consumers must not rely on `schemaVersion` surviving an MCP, OpenAI Completions, or Open Responses round-trip |
| `id` | OpenAI Completions | New IDs generated on inbound | Part correlation across an OpenAI Completions round-trip requires application-level tracking |
| `type` (`reasoning_trace`) | MCP, OpenAI Completions, Open Responses | Collapsed to `text`/`TextContent` with annotation; `reasoning_trace` semantic lost in OpenAI Completions; role annotation present but not semantically typed in Open Responses | Agents receiving their own reasoning output via OpenAI Completions cannot distinguish reasoning from regular text; Open Responses consumers must read the role annotation |
| `ref` | MCP, OpenAI Completions, Open Responses | Inlined; scheme lost | Callers that stored a `lenny-blob://` ref cannot recover it after an MCP, OpenAI Completions, or Open Responses round-trip |
| `annotations` (unknown keys) | MCP (no metadata ext.), OpenAI Completions, Open Responses | Unknown keys dropped | Vendor-defined annotations are lost for non-Lenny MCP clients and all OpenAI Completions and Open Responses consumers |

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

All inbound **content** messages (type `message`) use a unified `MessageEnvelope` across the stdin binary protocol, platform MCP server tools, and all external APIs. Non-content lifecycle messages (`heartbeat`, `shutdown`, `heartbeat_ack`) use their own minimal schemas and are not `MessageEnvelope` instances — see Protocol Reference below.

```json
{
  "schemaVersion": 1,
  "type": "message",
  "id": "msg_xyz789",
  "from": {
    "kind": "client | agent | system | external",
    "id": "..."
  },
  "inReplyTo": "req_abc123",
  "threadId": "thread_001",
  "delivery": "immediate",
  "delegationDepth": 0,
  "slotId": "slot_01",
  "input": ["OutputPart[]"]
}
```

**`schemaVersion`** — gateway-injected integer (default `1`). Every `MessageEnvelope` persisted to the `session_messages` table carries this field. Runtimes MUST NOT set it; the gateway writes it at inbox-enqueue time and it is immutable once written. Forward-compatibility rules follow the bifurcated consumer model in [Section 15.5](#155-api-versioning-and-stability) item 7: live consumers (streaming adapters, in-flight delivery) MAY reject an unrecognized version but SHOULD forward-read; durable consumers (message DAG readers, audit pipelines) MUST forward-read and preserve unknown fields verbatim.

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

**`slotId`** — optional string; present only in concurrent-workspace mode. Identifies the concurrent slot this message is addressed to. Session-mode and task-mode messages never carry `slotId`. See [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) and the `slotId` multiplexing note in the Protocol Reference.

**`delivery`** — optional closed enum controlling interrupt behaviour. Defined values:

| Value | Meaning | Gateway behaviour |
| --- | --- | --- |
| `"immediate"` | Interrupt the running agent and deliver now. | If session is `running`, the gateway sends an interrupt signal on the lifecycle channel and writes the message to stdin as soon as the runtime emits `interrupt_acknowledged` (Full-tier) or immediately after the in-flight stdin write completes (Minimum/Standard-tier). If session is `suspended`, the gateway atomically resumes (`suspended → running`) then delivers. **Exception — `input_required`:** When the session is in the `input_required` sub-state (blocked in `lenny/request_input`), `delivery: "immediate"` does **not** override path 3 buffering ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)). The runtime is not reading from stdin while blocked in a `request_input` call, so direct delivery is impossible regardless of the `delivery` flag. The message is buffered in the session inbox and delivered in FIFO order once the `request_input` resolves. Receipt: `queued`. For all other `running` sub-states, receipt: `delivered`. |
| `"queued"` | Buffer for next natural pause. | Message appended to the session inbox. Delivered in FIFO order when the runtime next enters `ready_for_input`. Receipt: `queued`. |
| absent | Same as `"queued"`. | Default behaviour. |

No other values are valid. The gateway rejects unknown `delivery` values with `400 INVALID_DELIVERY_VALUE`.

**`delivery_receipt` acknowledgement schema.** Every `lenny/send_message` call returns a synchronous `delivery_receipt` object. Senders that require reliable delivery MUST track receipts and re-send on gap detection (see [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) inbox crash-recovery note):

```json
{
  "messageId":   "msg_xyz789",
  "status":      "delivered | queued | dropped | expired | rate_limited | error",
  "reason":      "<string — populated when status is dropped, expired, or rate_limited>",
  "deliveredAt": "<RFC 3339 timestamp — populated when status is delivered>",
  "queueDepth":  "<integer — inbox depth after enqueue; populated when status is queued>"
}
```

`status` values: `delivered` (runtime consumed); `queued` (buffered in inbox); `dropped` (inbox overflow — oldest entry evicted); `expired` (DLQ TTL elapsed before delivery); `rate_limited` (inbound rate cap exceeded, [Section 7.2](07_session-lifecycle.md#72-interactive-session-model)); `error` (delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` when Redis is unreachable for durable inbox, or `reason: "scope_denied"` when messaging scope denies the target).

**`id`** — every message has a stable ID enabling threading, reply tracking, and non-linear context retrieval. IDs are gateway-assigned ULIDs (`msg_` prefix) when the sender omits them; sender-supplied IDs MUST be globally unique within the tenant or are rejected with `400 DUPLICATE_MESSAGE_ID`. **Deduplication window:** seen IDs are stored in a Redis sorted set (`t:{tenant_id}:session:{session_id}:msg_dedup`, scored by receipt timestamp) and retained for `deduplicationWindowSeconds` (default: 3600s, configurable per deployment via `messaging.deduplicationWindowSeconds` in Helm values). The set is trimmed on each write to remove entries older than the window.

**`inReplyTo`** — optional. If it matches an outstanding `lenny/request_input` call on the target, the gateway resolves that tool call directly instead of delivering to stdin.

**`threadId`** and `inReplyTo` — DAG conversation model. Messages within a session form a directed acyclic graph (DAG), not a flat list:

- Each message node has: `id` (self), `inReplyTo` (parent edge, optional), `threadId` (thread label, optional).
- In v1 there is one implicit thread per session (`threadId` absent or the same value for all messages). Multi-thread sessions are additive post-v1.
- The gateway records every delivered message in the session's `MessageDAG` store (Postgres `session_messages` table, indexed on `(session_id, id, thread_id)`). Clients may retrieve the DAG via `GET /v1/sessions/{id}/messages` with optional `?threadId=` and `?since=` filters.
- **Ordering guarantee:** Within a single thread, messages are ordered by the coordinator-local sequence number assigned at inbox-enqueue time (a monotonic integer per session, persisted to Postgres). This provides **coordinator-local FIFO** — not global wall-clock order. Cross-sender causal ordering requires application-level sequence numbers or vector clocks embedded in message content.
- **Delegation forwarding:** When a `lenny/send_message` call targets a session in a different delegation tree node, the `delegationDepth` field (integer, 0-based, gateway-injected) records how many tree hops the message crossed. Runtimes MAY inspect `delegationDepth` to detect unexpected cross-tree routing. The field is informational; the gateway does not alter delivery semantics based on it.

**`threadId`** — optional. In v1 one implicit thread per session. Multi-thread sessions are additive post-v1.

**Future-proof:** `MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId`, `delivery`, and `delegationDepth` accommodates all future conversational patterns without schema changes: threaded messages, multiple participants, non-linear context retrieval, broadcast, external agent participation.

#### Protocol Reference — Message Schemas

All **content** messages on stdin (type `message`) use the full `MessageEnvelope` format ([Section 15.4.1](#1541-adapterbinary-protocol)). Lifecycle messages (`heartbeat`, `shutdown`) use their own minimal schemas defined below and are not `MessageEnvelope` instances. Runtimes MUST ignore unrecognized fields. Minimum-tier runtimes need only read `type`, `id`, and `input` — all other envelope fields (`from`, `inReplyTo`, `threadId`, `delivery`, `delegationDepth`, `slotId`) can be safely ignored.

##### Inbound: `message`

```json
{
  "type": "message",
  "id": "msg_001",
  "input": [{ "type": "text", "inline": "What is 2+2?" }],
  "from": { "kind": "client", "id": "client_8f3a2b" },
  "threadId": "t_01",
  "delivery": "queued",
  "slotId": "slot_01"
}
```

Minimum-tier: read `type`, `id`, `input`. Ignore all other fields. `slotId` is optional — present only in concurrent-workspace mode.

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
  "isError": "<boolean, optional — true if tool execution failed; defaults to false>",
  "slotId": "<string, optional — present only in concurrent-workspace mode>"
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
  "output": [{ "type": "text", "inline": "The answer is 4." }],
  "slotId": "<string, optional — present only in concurrent-workspace mode>"
}
```

Minimum-tier shorthand (adapter normalizes to canonical form above):

```json
{ "type": "response", "text": "The answer is 4." }
```

**Error reporting via `response`.** The `response` message supports an optional `error` field for structured error reporting: `{"code": string, "message": string}`, matching the `TaskResult.error` shape. When `error` is present, the adapter maps the task to `failed` state and populates `TaskResult.error` from the response error. This allows runtimes to report failure details while still delivering partial output in the `output` array, without relying solely on non-zero exit codes (which lose error context). When `error` is absent and the process exits zero, the task completes successfully. When the process exits non-zero without emitting a `response`, the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr.

**Relationship between `lenny/output` and stdout `response`:** At Standard and Full tiers, runtimes may emit output parts incrementally via the `lenny/output` platform MCP tool. The stdout `{type: "response"}` message is always required to signal task completion, regardless of whether `lenny/output` was used. Its `output` array contains only parts not already emitted via `lenny/output`; runtimes that have already emitted all output parts via `lenny/output` send an empty `output` array (`[])`. The adapter concatenates `lenny/output` parts (in call order) with the final `response.output` parts to form the complete task output. Minimum-tier runtimes, which have no access to `lenny/output`, must include all output in the stdout `response.output` array. Standard-tier runtimes may use either delivery path or both.

##### Outbound: `tool_call`

Schema:

```json
{
  "type": "tool_call",
  "id": "<string, required — unique call identifier; used to correlate the inbound tool_result>",
  "name": "<string, required — tool name>",
  "arguments": "<object, required — tool-specific parameters; validated by the adapter against the tool's input schema>",
  "slotId": "<string, optional — present only in concurrent-workspace mode>"
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

**Adapter-Local Tool Reference**

Adapter-local tools are resolved entirely within the adapter process — no MCP server, no platform access, and no network call is required. They are available at all tiers (Minimum, Standard, Full). The following tools are built into every adapter:

| Tool name     | Description                                                       |
| ------------- | ----------------------------------------------------------------- |
| `read_file`   | Read the contents of a file in the workspace                      |
| `write_file`  | Create or overwrite a file in the workspace                       |
| `list_dir`    | List the entries of a directory in the workspace                  |
| `delete_file` | Delete a file or empty directory from the workspace               |

Discovery: agents discover adapter-local tools by inspecting the `adapterLocalTools` array in the adapter manifest (`/run/lenny/adapter-manifest.json`). Each entry contains the tool `name`, a human-readable `description`, and a JSON Schema `inputSchema` for its `arguments` object. Adapters MUST populate this array before spawning the runtime; the set is fixed for the lifetime of the pod.

Schemas for the four built-in tools:

```json
[
  {
    "name": "read_file",
    "description": "Read the contents of a file in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the file." }
      },
      "required": ["path"]
    }
  },
  {
    "name": "write_file",
    "description": "Create or overwrite a file in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path":    { "type": "string", "description": "Workspace-relative or absolute path to the file." },
        "content": { "type": "string", "description": "UTF-8 text content to write." }
      },
      "required": ["path", "content"]
    }
  },
  {
    "name": "list_dir",
    "description": "List the entries of a directory in the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the directory." }
      },
      "required": ["path"]
    }
  },
  {
    "name": "delete_file",
    "description": "Delete a file or empty directory from the workspace.",
    "inputSchema": {
      "type": "object",
      "properties": {
        "path": { "type": "string", "description": "Workspace-relative or absolute path to the target." }
      },
      "required": ["path"]
    }
  }
]
```

All `read_file` / `write_file` / `list_dir` / `delete_file` calls are confined to the pod's workspace volume (`/workspace`). The adapter rejects any path that resolves outside `/workspace` with a `tool_result` carrying `isError: true` and `content[0].inline` set to the string `"path_outside_workspace"`. Custom adapters MAY extend this list with additional adapter-local tools; they MUST declare all custom tools in `adapterLocalTools` before spawning the runtime.

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
   {"type": "message", "id": "msg_001", "input": [{"type": "text", "inline": "Hello"}], "from": {"kind": "client", "id": "client_8f3a2b"}, "threadId": "t_01"}
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
| `INIT`       | Adapter process starts, opens gRPC connection to gateway (mTLS), writes placeholder manifest. The adapter sends an `AdapterInit` message on the control stream with `adapterProtocolVersion` (semver string, e.g., `"1.0.0"`). The gateway responds with `AdapterInitAck` carrying `selectedVersion` (the highest compatible version the gateway supports) or closes the stream with `PROTOCOL_VERSION_INCOMPATIBLE` if no compatible version exists. Major version changes are breaking; minor/patch are backwards compatible. Current protocol version: `"1.0.0"`. |
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

- **Transport.** All intra-pod MCP servers use **abstract Unix sockets** exclusively (names listed in the adapter manifest, e.g., `@lenny-platform-mcp`, `@lenny-connector-github`). There is no stdio transport for intra-pod MCP — stdio is reserved for the binary protocol between the adapter and the runtime. **Platform compatibility note:** Abstract Unix sockets (names beginning with `@`) are a Linux kernel feature and are **not supported on macOS**. Standard- and Full-tier runtime development therefore requires a Linux environment. The recommended approach for macOS developers is to use `docker compose up` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Tier 2), which runs the adapter inside a Linux container. `make run` ([Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev) Tier 1) supports macOS for Minimum-tier runtimes only, since Minimum tier uses the stdin/stdout binary protocol exclusively and does not open any Unix sockets.
- **Protocol version.** The adapter's local MCP servers speak **MCP 2025-03-26** (the platform's target MCP spec version; see [Section 15.2](#152-mcp-api) for version negotiation details). The local servers also accept **MCP 2024-11-05** for backward compatibility. Intra-pod MCP version support follows the same rolling two-version policy as the gateway ([Section 15.5](#155-api-versioning-and-stability) item 2): the oldest accepted version enters a 6-month deprecation window when a new MCP spec version is adopted, and removal applies only to new connection negotiations (active sessions on the deprecated version are not forcibly terminated).
- **Client libraries.** Runtime authors should use an existing MCP client library for their language (e.g., `mcp-go` for Go, `@modelcontextprotocol/sdk` for TypeScript/Node.js, `mcp` for Python). These libraries work against the adapter's local servers with one Lenny-specific addition: the runtime must send the manifest nonce as the first message of the MCP `initialize` handshake (see Authentication below).
- **Tool discovery.** The runtime calls `tools/list` on each MCP server (platform and connectors) to discover available tools. The platform MCP server exposes the tools listed in Part A of this section (e.g., `lenny/delegate_task`, `lenny/output`). Each connector server exposes that connector's tools.
- **Authentication.** Intra-pod MCP connections require a manifest-nonce handshake, identical in mechanism to the lifecycle channel handshake ([Section 4.7](04_system-components.md#47-runtime-adapter), item 1). The adapter writes a random nonce into the adapter manifest (`/run/lenny/adapter-manifest.json`, read-only to the agent container) before spawning the runtime. The runtime must present this nonce as the first message of the MCP `initialize` handshake on each MCP connection (platform MCP server and every connector MCP server). The adapter rejects — with an immediate close — any MCP connection that does not present a valid nonce before dispatching tools. This prevents any process that has not read the manifest from connecting to a privileged MCP server, regardless of its UID. The nonce is stored in the manifest under the top-level key `mcpNonce` (a random 256-bit hex string, regenerated per task execution alongside the rest of the manifest).

  **Nonce wire format (v1 — intra-pod only).** The nonce is a Lenny-private convention for intra-pod MCP connections only; it does not appear on any external-facing MCP endpoint and is not part of the MCP specification. The canonical injection location is the top-level `_lennyNonce` field in the MCP `initialize` request's `params` object:
  ```json
  {
    "method": "initialize",
    "params": {
      "_lennyNonce": "<nonce_hex>",
      "clientInfo": {
        "name": "my-agent",
        "version": "1.0.0"
      },
      "protocolVersion": "2025-03-26"
    }
  }
  ```
  The adapter validates the `_lennyNonce` value against the manifest's `mcpNonce` field before processing any tool dispatch. The nonce must be the hex-encoded 256-bit value exactly as written in the manifest; no normalization or encoding is applied. After successful validation, the adapter **strips** the `_lennyNonce` field from `params` before dispatching the `initialize` request to its internal MCP server implementation, ensuring the MCP server never sees the non-standard field. This stripping is required because the adapter's MCP server validates `initialize` params against the MCP schema, which does not include `_lennyNonce`.

  > **Strict MCP client libraries.** Some MCP client libraries enforce schema validation on outgoing requests and may reject the `_lennyNonce` field in `params`. Runtime authors using such libraries should either (a) add `_lennyNonce` to the `initialize` params after the library constructs the request but before it is serialized to the socket, or (b) disable outbound schema validation for the `initialize` call only. The adapter accepts the field regardless of its position relative to other `params` keys.

  > **Deprecated location.** Earlier adapter versions also accepted `_lennyNonce` inside `params.clientInfo.extensions`. That location is no longer canonical and will not be checked in adapter manifest `version: 2`. Runtime authors MUST use `params._lennyNonce` (top-level in `params`).

  > **Migration path — v2 out-of-band handshake.** Injecting authentication material into MCP `initialize` parameters is a stopgap. Adapter manifest `version: 2` will replace this with a pre-`initialize` out-of-band handshake: the runtime sends a single JSON line `{"type":"lenny_nonce","nonce":"<nonce_hex>"}` on the socket before the MCP `initialize` exchange, keeping the nonce entirely outside the MCP message stream. The `params._lennyNonce` field will be supported (but ignored) in `version: 2` for a two-release backward-compat window; runtimes should migrate to the pre-initialize message at `version: 2` adoption.

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
| **Checkpoint / restore**                                                   | No checkpoint support; pod failure loses in-flight context. Gateway restarts session from last gateway-persisted state.                       | Best-effort snapshot without runtime pause (`consistency: best-effort`). Minor workspace inconsistencies possible on resume ([Section 4.4](04_system-components.md#44-event--checkpoint-store)). | Consistent checkpoint with runtime pause via lifecycle channel `checkpoint_request` / `checkpoint_ready`.                                       |
| **Interrupt**                                                              | No clean interrupt. Gateway sends SIGTERM; runtime has no opportunity to reach a safe stop point.                                             | No clean interrupt. Same SIGTERM-based termination as Minimum.                                                                             | Clean interrupt via `interrupt_request` on lifecycle channel; runtime acknowledges with `interrupt_acknowledged` and reaches a safe stop point. |
| **Credential rotation**                                                    | Checkpoint → pod restart → `AssignCredentials` with new lease → `Resume`. If checkpoint unsupported, in-flight context is lost ([Section 4.7](04_system-components.md#47-runtime-adapter)). | Checkpoint → pod restart → `AssignCredentials` with new lease → `Resume`. Brief session pause; client sees reconnect.                      | In-place rotation via `RotateCredentials` RPC and `credentials_rotated` lifecycle message. No session interruption.                             |
| **Deadline / expiry warning**                                              | No advance warning. `DEADLINE_APPROACHING` signal requires lifecycle channel; Minimum-tier receives only `shutdown` at expiry.                | No advance warning. Same as Minimum — no lifecycle channel to deliver `DEADLINE_APPROACHING`.                                              | `DEADLINE_APPROACHING` signal delivered on lifecycle channel before session expiry ([Section 10](10_gateway-internals.md)).                                                |
| **Graceful drain (`DRAINING` state)**                                      | No drain coordination. Adapter sends `shutdown` with `deadline_ms`; SIGTERM on timeout.                                                       | No drain coordination. Same as Minimum.                                                                                                    | `DRAINING` state via lifecycle channel enables graceful shutdown coordination before `shutdown`.                                                |
| **Task mode pod reuse**                                                    | No pod reuse. Adapter sends `shutdown` on stdin after task; pod replaced from warm pool. Effectively `maxTasksPerPod: 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).       | No pod reuse. Same as Minimum — no lifecycle channel for between-task signaling.                                                           | Full pod reuse via `task_complete` / `task_complete_acknowledged` / `task_ready` on lifecycle channel. Scrub + reuse cycle as described in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). |
| **Simplified response shorthand** (`{type: "response", text: "..."}`)      | Yes — adapter normalizes to canonical `OutputPart` form ([Section 15.4.1](#1541-adapterbinary-protocol)).                                                                     | Yes — available but typically unused since Standard runtimes produce structured output.                                                    | Yes — available but typically unused.                                                                                                           |
| **OutputPart minimal fields**                                              | Only `type` and `inline` required; all other fields optional with defaults ([Section 15.4.1](#1541-adapterbinary-protocol)).                                                  | Full `OutputPart` schema available.                                                                                                        | Full `OutputPart` schema available.                                                                                                             |
| **MessageEnvelope fields**                                                 | Only `type`, `id`, `input` needed; all other envelope fields safely ignored ([Section 15.4.1](#1541-adapterbinary-protocol)).                                                 | Full envelope including `from`, `inReplyTo`, `threadId`, `delivery`.                                                                       | Full envelope including `from`, `inReplyTo`, `threadId`, `delivery`.                                                                            |

> **Minimum-tier limitations — complete list:**
> Minimum-tier runtimes operate without the lifecycle channel and without platform MCP server access. The following capabilities are **unavailable** at Minimum tier and have no fallback:
>
> - **Checkpoint / restore:** Pod failure loses all in-flight context. The gateway restarts the session from the last gateway-persisted state; any unsaved intermediate work is gone.
> - **Clean interrupt:** No opportunity for the runtime to reach a safe stop point. The gateway issues `shutdown` on stdin and follows with SIGTERM after `deadline_ms`; the runtime cannot acknowledge an interrupt cleanly.
> - **Credential rotation without disruption:** Rotation requires a full pod restart (checkpoint → restart → `AssignCredentials` → `Resume`). If the runtime does not support checkpoint, the in-flight context is lost during rotation.
> - **Delegation (`lenny/delegate_task`):** Requires the platform MCP server, which is unavailable at Minimum tier. Minimum-tier runtimes cannot spawn sub-tasks.
> - **Platform MCP tools** (including `lenny/output`, `lenny/request_input`, `lenny/discover_agents`): All platform-side tools are inaccessible. Runtimes must produce all output via the stdout binary protocol.
> - **Connector MCP servers:** No connector (GitHub, filesystem, etc.) tool access.
> - **`DEADLINE_APPROACHING` warning:** Requires the lifecycle channel. Minimum-tier runtimes receive only the `shutdown` message at expiry with no advance notice.
> - **Graceful drain (`DRAINING` state):** No drain coordination signal. Shutdown is `shutdown`-on-stdin followed by SIGTERM.
> - **Inter-session messaging (`lenny/send_message`):** Requires the platform MCP server. Minimum-tier runtimes cannot send messages to other sessions or participate in sibling coordination patterns.
> - **Input-required blocking (`lenny/request_input`):** Requires the platform MCP server. Minimum-tier runtimes cannot request clarification from a parent or client mid-task. `one_shot` Minimum-tier runtimes must produce their response based solely on the initial input.
>
> These limitations are intentional — Minimum tier prioritizes simplicity and zero Lenny knowledge. Runtime authors who need any of the above must adopt Standard or Full tier.

Third-party authors should start with a minimum adapter and incrementally adopt standard and full features as needed.

#### 15.4.4 Sample Echo Runtime

The project includes a reference **`echo-runtime`** — a trivial agent binary that echoes back messages with a sequence number. It serves two purposes:

1. **Platform testing:** Validates the full session lifecycle (pod claim → workspace setup → message → response → teardown) without requiring a real agent runtime or LLM credentials.
2. **Template for custom runtimes:** Demonstrates the stdin/stdout JSON Lines protocol, heartbeat handling, and graceful shutdown — the minimal contract a custom agent binary must implement.

> **Runnable implementation (Phase 2 deliverable):** A fully runnable Go implementation of this echo runtime will be published at `examples/runtimes/echo/` in the repository. It compiles to a single static binary, requires no external dependencies, and can be registered with a local `lenny-dev` instance using `make run`. Runtime authors are encouraged to use it as a baseline when debugging their own adapter setups — if the echo runtime responds correctly, the platform is configured correctly. The pseudocode below serves as a readable summary of the logic; the Go source in `examples/runtimes/echo/` is the authoritative runnable reference.

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
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "heartbeat":
                write_line(stdout, json({"type": "heartbeat_ack"}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "shutdown":
                exit(0)
            default:
                // ignore unknown types for forward compatibility
    exit(0)
```

The samples below show the incremental additions required when advancing to Standard or Full tier. They assume the Minimum-tier loop above as their base.

```
Pseudocode (Standard-tier addition — nonce + MCP):

    // --- Startup: read adapter manifest and authenticate to local MCP servers ---
    manifest = json_parse(read_file("/run/lenny/adapter-manifest.json"))
    nonce    = manifest.mcpNonce     // 256-bit hex string, regenerated each task

    // Connect to platform MCP server (Unix socket, abstract namespace)
    platform_mcp = mcp_client_connect(manifest.platformMcpServer.socket)

    // Present nonce in MCP initialize params (top-level _lennyNonce field).
    // The adapter validates this before dispatching any tool call.
    platform_mcp.send({
        "method": "initialize",
        "params": {
            "_lennyNonce": nonce,
            "clientInfo": {"name": "my-runtime", "version": "1.0.0"},
            "protocolVersion": "2025-03-26"
        }
    })
    platform_mcp.recv()   // wait for initialize response

    // Optionally connect to each connector MCP server with the same nonce
    for server in manifest.connectorServers:
        conn = mcp_client_connect(server.socket)
        conn.send({"method": "initialize", "params": {
            "_lennyNonce": nonce,
            "clientInfo": {"name": "my-runtime", "version": "1.0.0"},
            "protocolVersion": "2025-03-26"
        }})
        conn.recv()   // wait for initialize response

    // Discover available tools (call tools/list on each connected server)
    tools = platform_mcp.call("tools/list", {})

    // --- Main loop (same as Minimum-tier, plus MCP tool invocation) ---
    seq = 0
    while line = read_line(stdin):
        msg = json_parse(line)
        switch msg.type:
            case "message":
                seq += 1
                // Invoke a platform tool via MCP instead of a bare echo
                result = platform_mcp.call("lenny/output", {
                    "output": [{"type": "text",
                                "inline": "echo [seq={seq}]: {msg.input[0].inline}"}]
                })
                write_line(stdout, json({"type": "response", "output": []}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "heartbeat":
                write_line(stdout, json({"type": "heartbeat_ack"}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "shutdown":
                platform_mcp.close()
                exit(0)
            default:
                // ignore unknown types for forward compatibility
    exit(0)
```

```
Pseudocode (Full-tier addition — lifecycle channel):

    // --- Startup: same manifest read and MCP setup as Standard-tier ---
    manifest     = json_parse(read_file("/run/lenny/adapter-manifest.json"))
    nonce        = manifest.mcpNonce
    platform_mcp = mcp_client_connect(manifest.platformMcpServer.socket)
    platform_mcp.send({"method": "initialize", "params": {
        "_lennyNonce": nonce,
        "clientInfo": {"name": "my-runtime", "version": "1.0.0"},
        "protocolVersion": "2025-03-26"
    }})
    platform_mcp.recv()

    // --- Lifecycle channel setup ---
    // Connect to the lifecycle channel (Full-tier runtimes only).
    // The socket path is advertised in the manifest; opening it is optional
    // but required for checkpoint, clean interrupt, and credential rotation.
    lc = unix_connect(manifest.lifecycleChannel.socket)  // @lenny-lifecycle

    // Capability negotiation: adapter sends lifecycle_capabilities first.
    cap_msg = json_parse(lc.recv_line())   // type: "lifecycle_capabilities"
    assert cap_msg.type == "lifecycle_capabilities"

    // Declare which capabilities this runtime supports (subset of offered).
    supported = ["checkpoint", "interrupt", "deadline_signal"]   // omit credential_rotation if unused
    lc.send_line(json({"type": "lifecycle_support", "capabilities": supported}))

    // --- Background goroutine: handle lifecycle signals concurrently ---
    spawn background:
        while lc_line = lc.recv_line():
            lc_msg = json_parse(lc_line)
            switch lc_msg.type:
                case "checkpoint_request":
                    // Quiesce: finish current output, flush buffers
                    quiesce_state()
                    lc.send_line(json({
                        "type": "checkpoint_ready",
                        "checkpointId": lc_msg.checkpointId
                    }))
                    // Wait for checkpoint_complete before resuming
                    cc = json_parse(lc.recv_line())
                    assert cc.type == "checkpoint_complete"
                    resume_state()

                case "interrupt_request":
                    // Reach a safe stop point, then acknowledge
                    reach_safe_stop_point()
                    lc.send_line(json({
                        "type": "interrupt_acknowledged",
                        "interruptId": lc_msg.interruptId
                    }))

                case "credentials_rotated":
                    // Reload credentials from the new path and rebind
                    reload_credentials(lc_msg.credentialsPath)
                    lc.send_line(json({
                        "type": "credentials_acknowledged",
                        "leaseId": lc_msg.leaseId,
                        "provider": lc_msg.provider
                    }))

                case "deadline_approaching":
                    // Wrap up long-running work before forced termination
                    begin_graceful_wrap_up(lc_msg.remainingMs)

                case "terminate":
                    // Ordered shutdown — exit within deadlineMs
                    cleanup_and_exit(0)

                default:
                    // ignore unknown lifecycle messages for forward compatibility

    // --- Main loop (same as Standard-tier) ---
    seq = 0
    while line = read_line(stdin):
        msg = json_parse(line)
        switch msg.type:
            case "message":
                seq += 1
                result = platform_mcp.call("lenny/output", {
                    "output": [{"type": "text",
                                "inline": "echo [seq={seq}]: {msg.input[0].inline}"}]
                })
                write_line(stdout, json({"type": "response", "output": []}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "heartbeat":
                write_line(stdout, json({"type": "heartbeat_ack"}))
                flush(stdout)   // REQUIRED: flush after every write (see Section 15.4.1)
            case "shutdown":
                // shutdown arrives on stdin even for Full-tier; lifecycle terminate
                // may arrive first — handle whichever comes first
                platform_mcp.close()
                lc.close()
                exit(0)
            default:
                // ignore unknown types for forward compatibility
    exit(0)
```

#### 15.4.5 Runtime Author Roadmap

Runtime-author information is distributed across this specification. The following reading order provides a guided path from first build to production-ready adapter, organized by integration tier.

**Minimum-tier (get a runtime working):**

1. **[Section 15.4.4](#1544-sample-echo-runtime)** — Sample Echo Runtime. Copy this pseudocode as your starting point.
2. **[Section 15.4.1](#1541-adapterbinary-protocol)** — Adapter↔Binary Protocol. The stdin/stdout JSON Lines contract, message types, `OutputPart` format, and simplified response shorthand.
3. **[Section 15.4.2](#1542-rpc-lifecycle-state-machine)** — RPC Lifecycle State Machine. Read for context: the adapter (not your binary) owns this state machine. Knowing it helps you understand when your binary will start receiving messages (`ACTIVE`), and that `shutdown` arrives only during `DRAINING` — your binary never drives these transitions.
4. **[Section 15.4.3](#1543-runtime-integration-tiers)** — Runtime Integration Tiers. Tier definitions and the capability comparison matrix — confirms what Minimum-tier runtimes can skip.
5. **[Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout)** — Pod Filesystem Layout. Where your binary's working directory, workspace, and scratch space live (`/workspace/current/`, `/tmp/`, `/artifacts/`).
6. **[Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)** — Local Development Mode (`lenny-dev`). Use `make run` for zero-dependency local testing against the gateway contract.

**Standard-tier (add MCP integration):**

7. **[Section 4.7](04_system-components.md#47-runtime-adapter)** — Runtime Adapter. Read for the **adapter manifest field reference** (`platformMcpServer.socket`, `connectorServers`, `mcpNonce`). The lifecycle channel message schemas (Part B) are Full-tier only — skip for Standard tier. The gRPC RPC table at the top of 4.7 is the gateway↔adapter contract and is not relevant to binary authors.
8. **[Section 9.1](09_mcp-integration.md#91-where-mcp-is-used)** — MCP Integration. How the platform MCP server and connector MCP servers are exposed to your runtime.
9. **[Section 8.2](08_recursive-delegation.md#82-delegation-mechanism)** — Delegation Mechanism. How `lenny/delegate_task` works if your runtime delegates sub-tasks.
10. **[Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)** — Runtime. Runtime definition schema (`type`, `capabilities`, `baseRuntime`), registration via admin API.

**Full-tier (lifecycle channel and production hardening):**

11. **[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)** — Pool Configuration and Execution Modes. Execution modes (session, task, concurrent-workspace), resource classes, and pool sizing.
12. **[Section 7.1](07_session-lifecycle.md#71-normal-flow)** — Session Lifecycle Normal Flow. End-to-end session flow from pod claim through teardown.
13. **[Section 13.1](13_security-model.md#131-pod-security)–13.2** — Pod Security and Network Isolation. Security constraints your runtime operates under (seccomp, gVisor, egress rules).
14. **[Section 14](14_workspace-plan-schema.md)** — Workspace Plan Schema. How workspace sources are declared and materialized before your binary starts.
15. **[Section 15.5](#155-api-versioning-and-stability)** — API Versioning and Stability. Versioning guarantees for the adapter protocol.

### 15.5 API Versioning and Stability

Community contributors and integrators need clear guarantees about which APIs are stable and how breaking changes are managed. Each external surface follows its own versioning scheme:

1. **REST API:** Versioned via URL path prefix (`/v1/`). Breaking changes require a new version (`/v2/`). Non-breaking additions (new fields, new endpoints) are added to the current version. The previous version is supported for at least 6 months after a new version ships.

2. **MCP tools:** Versioned via the MCP protocol's capability negotiation (see [Section 15.2](#152-mcp-api) for target version and negotiation details). The gateway supports two concurrent MCP spec versions (current + previous) with a 6-month deprecation window for the oldest. Tool schemas can add optional fields without a version bump. Removing or renaming fields, or changing semantics, is a breaking change.

3. **Runtime adapter protocol:** Versioned independently (see [Section 15.4](#154-runtime-adapter-specification)). The adapter advertises a protocol version at INIT; the gateway selects a compatible version. Major version changes are breaking.

4. **CRDs:** All Lenny CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`) ship initially at **`v1alpha1`** and follow the graduation path `v1alpha1` → `v1beta1` → `v1`. Graduation criteria: `v1alpha1` → `v1beta1` requires Phase 2 benchmark completion and no breaking field changes for 60 days; `v1beta1` → `v1` requires GA load-test sign-off (Phase 14.5) and no breaking changes for 6 months.

   **Conversion webhook deployment.** Multi-version coexistence during upgrades depends on a running conversion webhook. The conversion webhook (`lenny-crd-conversion`) must be deployed **before** adding a new served version to any CRD — the API server begins routing conversion requests to the webhook as soon as `spec.conversion.strategy: Webhook` is set, and a missing webhook causes all CRD operations to fail. The deployment procedure for each version graduation is:
   1. Deploy the `lenny-crd-conversion` Deployment (from `charts/lenny/templates/conversion-webhook.yaml`) and wait for its pods to reach `Ready` state.
   2. Verify the webhook Service and `spec.conversion.webhook.clientConfig` in each CRD are correctly referencing the `lenny-crd-conversion` Service before applying the updated CRD. Run `kubectl get svc -n lenny-system lenny-crd-conversion` to confirm the Service exists.
   3. Apply the updated CRD manifests (`kubectl apply -f charts/lenny/crds/`). The `lenny-preflight` Job validates conversion webhook availability as a preflight check and will fail the upgrade if the webhook Service is absent or not ready.
   4. Confirm `kubectl get crd <name> -o jsonpath='{.spec.versions[*].name}'` lists both the old and new version as served.
   5. Migrate stored objects to the new storage version using `kubectl get <resource> -A --output=yaml | kubectl apply -f -` (re-apply triggers conversion and storage migration). Monitor `apiserver_crd_webhook_conversion_duration_seconds` for conversion latency.
   6. Once all stored objects are migrated, remove the old version from the `served: true` list and update `storage: true` to the new version in the CRD spec.

   Conversion webhooks are deployed with `replicas: 2` and `PodDisruptionBudget minAvailable: 1`. The `lenny-preflight` Job checks webhook availability as part of every upgrade. See [Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy) for the full CRD upgrade procedure.

5. **Definition of "breaking change":** Removing a field, changing a field's type, changing the default behavior of an existing feature, removing an endpoint/tool, or changing error codes for existing operations.

6. **Stability tiers:**
   - `stable`: Covered by versioning guarantees above.
   - `beta`: May change between minor releases with deprecation notice.
   - `alpha`: May change without notice.

7. **Schema versioning — bifurcated consumer rules.** All Postgres-persisted record types carry a `schemaVersion` integer field (starting at `1`) that identifies the schema revision used to write the record. This applies to: `TaskRecord` ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)), billing events ([Section 11.2.1](11_policy-and-controls.md#1121-billing-event-stream)), audit events (`EventStore`), checkpoint metadata ([Section 7.1](07_session-lifecycle.md#71-normal-flow)), session records ([Section 7](07_session-lifecycle.md)), `WorkspacePlan` ([Section 14](14_workspace-plan-schema.md)), and `MessageEnvelope` ([Section 15.4.1](#1541-adapterbinary-protocol), persisted in the `session_messages` table). The field is set at write time by the gateway and is immutable once written.

   The forward-compatibility rules differ between **live (streaming) consumers** and **durable (persisted) consumers**:

   **Live consumers** (streaming sessions, real-time adapters, in-memory event handlers):
   - **MAY reject** an unrecognized `schemaVersion` — but SHOULD forward-read (process known fields, surface a `schema_version_ahead` degradation signal) unless the unrecognized version introduces semantically incompatible fields that make silent partial processing dangerous.
   - When a live consumer chooses to forward-read, it MUST surface a `schema_version_ahead` annotation on the enclosing `MessageEnvelope` (fields: `knownVersion`, `encounteredVersion`) so the caller is informed of potential incompleteness.
   - Rationale: live consumers are transient — a rejection causes only a session failure that can be retried with an updated consumer. Silently dropping unknown fields in a live context is acceptable when degradation is signalled.

   **Durable consumers** (billing processors, audit log readers, analytics pipelines, compliance exporters):
   - **MUST forward-read** records with unrecognized `schemaVersion`. Rejection at read time creates compliance gaps: billing records retained for 13 months and audit events retained for regulatory periods MUST remain readable even when the consumer binary has not yet been upgraded.
   - Durable consumers MUST process all fields they understand and preserve all unknown fields verbatim (pass-through). If a durable consumer cannot safely pass through unknown fields (e.g., it writes to a schema-strict sink), it MUST emit a `durable_schema_version_ahead` structured error to an operator alert channel and queue the record for manual review rather than dropping it.
   - Durable consumers MUST NOT silently discard records based solely on an unrecognized `schemaVersion`.
   - **Migration window SLA:** When a new `schemaVersion` is introduced, all durable consumers MUST be upgraded to understand the new version within **90 days** of the version's release. After 90 days, the previous schema version may be retired from active write paths, but persisted records at the old version remain readable for the full retention period of each record type.

   **Reader code** uses `schemaVersion` to select the correct deserialization path, enabling rolling schema migrations without downtime. **This durable-consumer forward-read rule extends to `OutputPart` arrays nested within `TaskRecord`:** if any `OutputPart` in a persisted `TaskRecord` carries a `schemaVersion` a durable consumer does not recognize, the consumer MUST forward-read (preserving unknown fields verbatim) rather than rejecting the record or silently dropping unrecognized fields — silent data loss in billing or audit records is unacceptable (see [Section 15.4.1](#1541-adapterbinary-protocol), "Consumer obligation — durable storage (TaskRecord)").

### 15.6 Client SDKs

Lenny provides official client SDKs for **Go** and **TypeScript/JavaScript** as part of the v1 deliverables. SDKs encapsulate session lifecycle management, MCP streaming with automatic reconnect-with-cursor, file upload multipart handling, webhook signature verification, and error handling with retries — logic that is complex and error-prone to re-implement from the protocol specs alone.

SDKs are generated from the OpenAPI spec (REST) and MCP tool schemas, with hand-written streaming and reconnect logic layered on top. Community SDKs for other languages can build on the published OpenAPI spec and the MCP protocol specification.

Client SDKs follow the same versioning scheme as the API surfaces they wrap ([Section 15.5](#155-api-versioning-and-stability)): SDK major versions track REST API versions, and SDK releases note any MCP tool schema changes.

