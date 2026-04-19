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
3a. Gateway:             Experiment assignment — the ExperimentRouter interceptor fires at the
                         PreRoute phase (priority 300, see Section 4.8 and Section 10.7).
                         Evaluates active experiments for this tenant, assigns the session to
                         a variant (or control) using the bucketing algorithm, and populates
                         the session's experimentContext. If a variant is assigned, the variant's
                         pool overrides the default pool selection in step 4.
4. Gateway:              Select pool, claim idle warm pod
5. Gateway → Store:      Persist session metadata (session_id, pod, state)
6. Gateway:              Evaluate CredentialPolicy per provider in the intersection →
                         assign CredentialLease map (one lease per provider with available
                         credentials; providers without availability are skipped)
7. Gateway → Pod:        AssignCredentials(leases) — push map of provider → materializedConfig
                         to runtime for all successfully assigned providers
8. Gateway → Client:     Return session_id + upload token + sessionIsolationLevel

**Atomicity of session creation (steps 2–8).** Steps 2 through 8 are executed as an atomic unit from the client's perspective. If any step fails — including pool exhaustion at step 4 (pod claim), credential assignment failure at step 6, or pod communication failure at step 7 — the gateway rolls back any partially allocated resources (releases the pod claim, revokes the credential lease), does NOT persist the session row, and returns a retryable error to the client: `503 SERVICE_UNAVAILABLE` with error code `SESSION_CREATION_FAILED` and a `Retry-After` header. The client never receives a `session_id` for a session that failed to fully initialize. There is no `created -> failed` transition because the session is never persisted in the `created` state until all preconditions are satisfied.
                         (executionMode, isolationProfile, scrubPolicy summary)

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

**`uploadToken` format and security properties.** The `uploadToken` returned at step 8 is a short-lived, session-scoped HMAC-SHA256 signed token. It is structured as `<session_id>.<expiry_unix_seconds>.<hmac_hex>`, where the HMAC covers `session_id || "." || expiry_unix_seconds` under a gateway-held signing key. Properties:

- **TTL:** The token expires at `session_creation_time + maxCreatedStateTimeoutSeconds` (default 300 s) — the same deadline that governs the `created` state. The gateway rejects any upload or finalize request bearing an expired token with `401 UPLOAD_TOKEN_EXPIRED`.
- **Session scope binding:** The embedded `session_id` is validated on every upload and finalize request. Tokens from a different session are rejected with `403 UPLOAD_TOKEN_MISMATCH`.
- **Single-use invalidation:** The token is invalidated immediately upon successful `FinalizeWorkspace`. Any subsequent attempt to use the same token returns `410 UPLOAD_TOKEN_CONSUMED`. This prevents replay of a captured token after workspace finalization.
- **Cryptographic protection:** The HMAC prevents forgery of arbitrary `(session_id, expiry)` pairs. Signing keys are rotated on a deployer-configurable schedule (default: 24 hours); the gateway keeps the previous key valid during a short overlap window (default: 5 minutes) to avoid rejecting tokens issued just before rotation.

Clients MUST treat `uploadToken` as a secret credential: it MUST NOT be logged, embedded in URLs, or included in client-side error reports.

**Session isolation response (`sessionIsolationLevel`).** The `POST /v1/sessions` response includes a `sessionIsolationLevel` object alongside `session_id` and `uploadToken`. This gives clients visibility into the actual isolation properties of the assigned pod before they proceed with file uploads or session start. Fields:

| Field | Type | Description |
| ----- | ---- | ----------- |
| `executionMode` | `string` | `session`, `task`, or `concurrent` — the execution mode of the assigned pool |
| `isolationProfile` | `string` | `runc`, `gvisor`, or `microvm` — the container/VM isolation level |
| `podReuse` | `boolean` | `true` when `executionMode` is `task` or `concurrent`; `false` for `session` mode |
| `scrubPolicy` | `string` | Present only when `podReuse: true`. **Task mode:** `"best-effort"` for standard scrub; `"vm-restart"` for microvm task mode with `allowCrossTenantReuse: true` and `microvmScrubMode: restart`; `"best-effort-in-place"` for microvm task mode with `microvmScrubMode: in-place`. **Concurrent-workspace mode:** `"best-effort-per-slot"` — the same scrub operations (workspace removal, process-group kill, scratch directory cleanup) are applied per-slot on slot completion or failure (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) concurrent-workspace slot cleanup). **Concurrent-stateless mode:** `"none"` — no workspace exists and no scrub is performed; pod reuse is implicit via Kubernetes Service routing. For concurrent-stateless mode, `"none"` indicates more than "no cleanup": the gateway does not track per-request state or lifecycle for this mode, and no per-request scrub, checkpoint, or slot-level lifecycle management is performed by Lenny (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) concurrent-stateless limitations). |
| `residualStateWarning` | `boolean` | `true` when `executionMode` is `task` (any scrub variant) or `concurrent` with `concurrencyStyle: workspace` — signals that the pod may carry residual state from prior tasks or slots. For task mode, residual state vectors include DNS cache, TCP TIME_WAIT, page cache, etc. from prior tasks. For concurrent-workspace mode, residual state includes shared process namespace, `/tmp`, cgroup memory, and network stack across simultaneous slots (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) deployer acknowledgment). Clients can use this to display warnings or enforce additional input sanitization. |

`GET /v1/sessions/{id}` also returns `sessionIsolationLevel` in the session metadata so clients can inspect it after creation. This field is populated from the assigned pool's configuration at session creation time and does not change for the lifetime of the session.

**Artifact retention:** Session artifacts (workspace snapshots, logs, transcripts) are retained for a configurable TTL (default: 7 days, deployer-configurable). A background GC job deletes expired artifacts. Clients can extend retention on specific sessions via `POST /v1/sessions/{id}/extend-retention` (body: `{"ttlSeconds": <n>}`). See [Section 15.1](15_external-api-surface.md#151-rest-api).

**Transcript as downloadable artifact:** The session transcript (conversation history) is available via `GET /v1/sessions/{id}/transcript` and is included as a downloadable session artifact. When deriving a new session (see `POST /v1/sessions/{id}/derive`), clients can optionally include the previous session's transcript as a file in the derived session's workspace, giving the new agent context from the prior conversation.

**Derive copy semantics:** `POST /v1/sessions/{id}/derive` always performs a full copy of the parent session's workspace snapshot — it reads the parent's MinIO object and writes a new independent object under the derived session's own path (`/{tenant_id}/{object_type}/{new_session_id}/...`). No MinIO object is shared between parent and derived sessions. The `parent_workspace_ref` field on the derived session record is a metadata lineage pointer only ([Section 4.5](04_system-components.md#45-artifact-store)); it does not create a reference dependency for GC purposes. This means the GC job can safely delete the parent's artifacts by TTL without affecting the derived session's workspace. Reference counting is not required in v1. If a future optimization introduces content-addressed deduplication (shared blobs across sessions), reference counting must be added to the GC job at that time ([Section 12.8](12_storage-architecture.md#128-compliance-interfaces)).

**Derive session semantics:** The `POST /v1/sessions/{id}/derive` endpoint creates a new independent session pre-populated with the source session's workspace snapshot. The following rules govern derive behavior:

1. **Allowed source session states.** Derive is permitted from any session state that has a resolvable workspace snapshot:
   - `completed` — uses the sealed final workspace. This is the recommended and most predictable source state.
   - `failed` — uses the last successful checkpoint snapshot. The response includes `workspaceSnapshotSource: "checkpoint"` and `workspaceSnapshotTimestamp` so the client knows the snapshot predates the failure.
   - `running`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action` — **rejected by default** with `409 DERIVE_ON_LIVE_SESSION`. The client must pass `allowStale: true` in the request body to derive from a non-terminal session. When `allowStale: true` is set, the derive uses the most recent successful checkpoint snapshot. The response includes `workspaceSnapshotSource: "checkpoint"` and `workspaceSnapshotTimestamp`. **Staleness warning:** the checkpoint snapshot may lag behind the live workspace by up to the full checkpoint interval (default 10 minutes). No on-demand checkpoint is triggered (to avoid unexpected pauses); clients who need a fresh snapshot should wait for session completion instead.
   - `cancelled`, `expired` — uses the sealed workspace if seal completed before termination, otherwise the last successful checkpoint. `workspaceSnapshotSource` indicates which was used.
   - If no workspace snapshot exists (e.g., session failed before any checkpoint or seal), derive returns `400 VALIDATION_ERROR` with `details.fields[0].field: "sourceSessionId"` and message `"source session has no resolvable workspace snapshot"`.

2. **Concurrent derive serialization.** The gateway acquires a per-source-session advisory lock (Redis `SETNX` on key `derive_lock:{source_session_id}`, TTL 30 seconds) before reading the workspace snapshot reference. This ensures that concurrent `POST /v1/sessions/{id}/derive` calls on the same source session are serialized: only one caller reads and copies the snapshot reference at a time, preventing torn reads of a snapshot reference that is being updated by a concurrent checkpoint. A caller that fails to acquire the lock within 5 seconds receives `429 DERIVE_LOCK_CONTENTION` and may retry. The lock is released immediately after the snapshot reference is read (not held for the full copy duration, which may be long for large workspaces). **Why releasing the lock before the copy is safe:** each checkpoint write creates a new MinIO object at a new path — it never overwrites the previously stored object in place. Once the lock has been released after reading the snapshot reference, that reference resolves to a stable, immutable MinIO object key that cannot be mutated by any concurrent checkpoint. The only scenario where the copy could fail is if the referenced object has been deleted (e.g., by a GC bug or premature TTL expiry); in that case the gateway returns `503 DERIVE_SNAPSHOT_UNAVAILABLE` and the caller may retry or wait for the source session to reach a terminal state before retrying. **Partial-copy and retry semantics:** the gateway does not resume partial derive copies — if the MinIO copy fails after the lock has been released (including `503 DERIVE_SNAPSHOT_UNAVAILABLE` and any other transient I/O failure), any partially written destination object for the derived session is deleted by the gateway and the derived session record is marked `failed`. Clients MUST retry the entire `POST /v1/sessions/{id}/derive` call; there is no mid-copy resume endpoint. `503 DERIVE_SNAPSHOT_UNAVAILABLE` is classified `TRANSIENT` and is retriable, though immediate retry is unlikely to succeed if the underlying cause is a deleted snapshot object (see [Section 15.1](15_external-api-surface.md#151-rest-api) error catalog).

3. **Credential lease handling.** Derive creates a fully independent session. The derived session goes through the standard `CredentialPolicy` evaluation ([Section 7.1](#71-normal-flow), step 6) — the gateway evaluates the credential policy for the new session's runtime and assigns a fresh `CredentialLease` from the resolved pool or user source. Credential leases are never inherited from the source session. If no credential is available, derive fails with `503 CREDENTIAL_POOL_EXHAUSTED` — the same error as a normal `CreateSession` when the pool is exhausted. The source session's credential lease is unaffected by the derive operation.

4. **Connector state.** Connector OAuth tokens and authorization state are not inherited. The derived session starts with no active connector tokens. If the derived session's runtime requires connector access, the client must complete the connector authorization flow independently ([Section 9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)) for the new session. Gateway-held connector tokens are scoped to the source session and are not copied to the derived session.

**Seal-and-export invariant:** The workspace is always exported to durable storage before the pod is released. If export fails, the pod is held in `draining` state and retried with exponential backoff (initial: 5s, factor: 2×, cap: 60s per attempt). The total retry window is bounded by `maxWorkspaceSealDurationSeconds` (pool-level configuration, default: 300s). If the seal does not succeed within this window, the gateway stops retrying, transitions the session to `failed` with reason `workspace_seal_timeout`, emits a `workspaceSealFailed` audit event (recording the last MinIO error), terminates the pod anyway, and fires the `WorkspaceSealStuck` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)). The `lenny_workspace_seal_duration_seconds` histogram (labeled by `pool` and `outcome`: `success`, `timeout`) tracks seal completion time across all sessions. This ensures session output is never lost due to transient pod cleanup races while preventing a permanent MinIO outage from holding pods in `draining` indefinitely.

### 7.2 Interactive Session Model

Once a session is attached, the client interacts via a **Lenny session** with bidirectional streaming over Streamable HTTP (SSE for server→client, POST for client→server). All content delivery uses the `MessageEnvelope` format (see [Section 15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol)). Externally, the session is surfaced as a protocol-native task object by the active `ExternalProtocolAdapter` — an MCP Task for MCP clients, an A2A Task for A2A clients, and so on. Internally the gateway operates against the **Lenny canonical task state machine** ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)), which is defined independently of any external protocol.

**Client → Gateway (external API):**

| Endpoint / Message                                 | Description                                                                                                                                            |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `POST /v1/sessions/{id}/messages`                  | Send a message (unified endpoint for all content delivery). Gateway rejects injection against sessions whose runtime has `injection.supported: false`. |
| `POST /v1/sessions/{id}/interrupt`                 | Interrupt current agent work (lifecycle signal, not content delivery)                                                                                  |
| `POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve` | Approve a pending tool call. Body: `{}`. Returns `200` on success, `404 RESOURCE_NOT_FOUND` if the tool call is not pending. |
| `POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny`   | Deny a pending tool call. Body: `{"reason": "<string>"}` (optional). Returns `200` on success.                               |
| `POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond` | Answer an elicitation request. Body: `{"response": <value>}`. Returns `200` on success. See [Section 9.2](09_mcp-integration.md#92-elicitation-chain).               |
| `POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss` | Dismiss a pending elicitation (cancellation). Returns `200` on success. See [Section 9.2](09_mcp-integration.md#92-elicitation-chain).                                |

**Gateway → Client (streaming events):**

| Event                                          | Description                                                                                                                 |
| ---------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `agent_output(parts: OutputPart[])`            | Streaming output from the agent (replaces `agent_text`)                                                                     |
| `tool_use_requested(tool_call_id, tool, args)` | Agent wants to call a tool (if approval required)                                                                           |
| `tool_result(tool_call_id, result)`            | Result of a tool call                                                                                                       |
| `elicitation_request(elicitation_id, schema)`  | Agent/tool needs user input                                                                                                 |
| `status_change(state)`                         | Session state transition (including `suspended` and `input_required`)                                                       |
| `session.resumed(resumeMode, workspaceLost)`   | Session resumed from checkpoint or minimal state; `resumeMode` is `full` or `conversation_only`; `workspaceLost` is boolean |
| `children_reattached(children)`                | Parent session resumed after failure with active children; gateway re-injected virtual child interfaces (see [Section 8.10](08_recursive-delegation.md#810-delegation-tree-recovery)). `children` is an array of `ReattachedChild` objects (schema below) |
| `error(code, message, transient?)`             | Error with classification                                                                                                   |
| `session_complete(result)`                     | Session finished, result available                                                                                          |

**`ReattachedChild` schema** (used in `children_reattached` event):

| Field                  | Type     | Description                                                                                                                                                                       |
| ---------------------- | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `session_id`           | string   | Session ID of the reattached child                                                                                                                                                |
| `state`                | string   | Current state of the child session (`running`, `input_required`, `suspended`, `completed`, `failed`, `cancelled`, `expired`)                                                      |
| `pending_request_id`   | string?  | If the child is in `input_required` state with a pending elicitation or tool approval directed at the parent, the request ID that the parent must respond to. `null` if none.      |
| `result`               | object?  | If the child has reached a terminal state (`completed`, `failed`, `cancelled`, `expired`), the child's result object. `null` if the child is still running.                       |
| `delegation_lease_id`  | string   | The delegation lease ID that governs this child, allowing the parent to correlate with its original `lenny/delegate_task` call                                                    |

The gateway emits `children_reattached` as a single SSE event immediately after `session.resumed` when the resuming session has one or more active children in the delegation tree. If the parent session has no children (or all children reached terminal states before the parent resumed), the event is not emitted. The event is delivered exactly once per parent resume; subsequent child state changes are delivered through the normal streaming event flow.

**Session state machine:**

> **Note:** This is a summary of session-level state transitions as visible to external clients. For the complete pod and session state machine — including pre-attached states, task-mode cycling, and concurrent-workspace slot multiplexing — see [Section 6.2](06_warm-pod-model.md#62-pod-state-machine).

```
running → suspended        (interrupt_request + interrupt_acknowledged)
running → suspended        (interrupt_request timeout — deadlineMs elapsed without interrupt_acknowledged; adapter forces suspended, RPC returns INTERRUPT_TIMEOUT)
running → input_required   (runtime calls lenny/request_input — sub-state of running)
running → completed        (agent finishes)
running → resume_pending   (pod crash / gRPC error, retryCount < maxRetries)
running → failed           (runtime crash, unrecoverable error, retries exhausted, or BUDGET_KEYS_EXPIRED — see §8.3)
running → cancelled        (client/parent cancels)
running → expired          (lease/budget/deadline exhausted)
input_required → running   (input provided via inReplyTo or request expires/cancelled)
input_required → cancelled (parent cancels while awaiting input)
input_required → expired   (deadline reached while awaiting input)
input_required → resume_pending (pod crash / gRPC error while awaiting input, retryCount < maxRetries)
input_required → failed    (pod crash / gRPC error while awaiting input, retries exhausted)
input_required → failed    (BUDGET_KEYS_EXPIRED detected while awaiting input — see §8.3)
suspended → running        (resume_session — no new content; pod still held)
suspended → running        (POST /v1/sessions/{id}/messages delivery:immediate; pod still held)
suspended → resume_pending (resume_session; pod was released by maxSuspendedPodHoldSeconds)
suspended → resume_pending (POST /v1/sessions/{id}/messages delivery:immediate; pod was released — message held in session inbox)
suspended → completed      (terminate)
suspended → cancelled      (client/parent cancels while suspended)
suspended → expired        (delegation lease perChildMaxAge wall-clock expiry while suspended)
suspended → failed         (BUDGET_KEYS_EXPIRED detected — see §8.3)
suspended → resume_pending (involuntary pod failure/eviction while suspended; pod still held)
resume_pending → resuming              (pod allocated within maxResumeWindowSeconds)
resume_pending → awaiting_client_action (maxResumeWindowSeconds elapsed, no pod available)
resuming → running                     (re-attach succeeds on replacement pod; internal-only transient — the API reports the overall transition as resume_pending → running, see [§15.1](15_external-api-surface.md#151-rest-api))
resuming → failed                      (re-attach fails after retries exhausted)
awaiting_client_action → resume_pending (client issues POST /v1/sessions/{id}/resume — see [§15.1](15_external-api-surface.md#151-rest-api))
awaiting_client_action → completed     (client issues POST /v1/sessions/{id}/terminate)
awaiting_client_action → cancelled     (client issues DELETE /v1/sessions/{id}, or parent cancels)
awaiting_client_action → expired       (lease/budget/deadline exhausted while awaiting client action)
```

`input_required` is a **sub-state of `running`**: the pod is live and the runtime process is active, but the agent is blocked inside a `lenny/request_input` tool call awaiting a response. This sub-state is significant for message routing (see delivery paths below) and for external observability (the gateway emits `status_change(state: "input_required")` to the client when the session enters this state, and `status_change(state: "running")` when it exits). Because `input_required` is a sub-state of `running` where the pod is live, all failure transitions defined for `running` also apply to `input_required` — including `resume_pending` on pod crash when retries remain and `failed` when retries are exhausted (these transitions are listed explicitly in the state machine above). The remaining transitions mirror those in the canonical task state machine ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)).

Terminal states: `completed`, `failed`, `cancelled`, `expired`. These match the canonical task states defined in [Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema).

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

**Cross-tenant validation:** Before routing any `lenny/send_message` call, the gateway MUST validate that the sender session's `tenant_id` matches the target session's `tenant_id`. Messages targeting a session belonging to a different tenant are rejected with `CROSS_TENANT_MESSAGE_DENIED` regardless of messaging scope or tree structure. This validation is performed before scope evaluation and rate limiting, and applies uniformly to all message paths (inter-session, `inReplyTo`, and `delivery: "immediate"` resume triggers).

**Rate limiting:** `lenny/send_message` is subject to per-session outbound rate limits (`maxPerMinute`, `maxPerSession`) and per-session inbound aggregate rate limits (`maxInboundPerMinute`) defined in the delegation lease (see `messagingRateLimit` in [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)).

**Content policy enforcement:** Inter-session messages are subject to the target session's `contentPolicy.maxInputSize` and `contentPolicy.interceptorRef` via the `PreMessageDelivery` interceptor phase ([Section 4.8](04_system-components.md#48-gateway-policy-engine)). This ensures that agent-to-agent messages receive the same content scanning as delegation inputs.

**Session inbox definition:** Each active session on its coordinating gateway replica has a **session inbox** — a buffered queue of undelivered messages. The inbox operates in one of two modes controlled by the deployment-level `messaging.durableInbox` flag:

**Default mode (`durableInbox: false`) — in-memory inbox:**

| Property          | Value                                                                                                                                                                                                                                                                                                          |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Backing store     | In-memory on the coordinating gateway replica (not persisted to Redis or Postgres)                                                                                                                                                                                                                             |
| Size bound        | `maxInboxSize` messages per session (default: 500; configurable per deployment). This cap prevents unbounded memory growth during long `await_children` blocks.                                                                                                                                                |
| Overflow behavior | When the inbox reaches `maxInboxSize`, the **oldest** buffered message is dropped and the sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`.                                                                                                                                |
| Durability        | **No durability guarantee.** If the coordinating gateway replica crashes, the inbox contents are lost. This is a documented loss window.                                                                                                                                                                       |
| Crash recovery    | When a new gateway replica takes over coordination (via lease reacquisition), the session's inbox is empty. The gateway emits an **`inbox_cleared`** event on the **target session's own event stream** immediately after the new coordinator acquires the lease: `{ "type": "inbox_cleared", "reason": "coordinator_failover", "clearedAt": "<ISO8601>", "sessionId": "<target_session_id>", "messagesPreservedInDLQ": 0 }`. The `messagesPreservedInDLQ` field contains the count of inbox messages that were successfully drained to the DLQ before the coordinator crash (e.g., during a prior `resume_pending` transition). When this value is `> 0`, the messages were not truly lost — they are preserved in the DLQ and will be delivered on session resumption. When `0`, the in-memory inbox contents were lost. This distinction allows clients to differentiate between a true data-loss event and a benign coordinator handoff. This event notifies the target session's client about the inbox state change. Because the in-memory inbox contents (including sender identities) are lost on coordinator crash, the gateway **cannot** notify individual senders directly. Senders that previously received a `queued` delivery receipt for this target have no platform-level signal that their message was dropped. To achieve reliable delivery, senders MUST use one of: (1) `durableInbox: true` mode, which survives coordinator failover (see durable mode below); (2) the DLQ path (path 7), which persists messages to Postgres; or (3) application-level acknowledgement — the sender waits for an application-defined ACK message from the target and re-sends after a timeout. |

**Durable mode (`durableInbox: true`) — Redis-list-backed inbox:**

When `messaging.durableInbox: true` is set, the session inbox is backed by a Redis list keyed `t:{tenant_id}:session:{session_id}:inbox` instead of in-memory state. All inbox operations (enqueue, peek, dequeue) execute against Redis:

| Property          | Value                                                                                                                                                                                       |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Backing store     | Redis list (`RPUSH` / `LRANGE` / `LPOP`) on the coordinating gateway's Redis connection. Each list entry is a serialised `InboxMessage` including `message_id`, `payload`, `enqueued_at`, and `per_message_ttl` (default: `maxResumeWindowSeconds`, or 900s).  |
| Size bound        | Same `maxInboxSize` cap enforced atomically via a Lua script that checks `LLEN` before `RPUSH`. Overflow drops the oldest entry (`LPOP` + drop) with a `message_dropped` receipt.          |
| Per-message TTL   | Each message carries an `enqueued_at` timestamp. A background goroutine on the coordinating replica trims expired messages from the list head using `LRANGE` + `LTRIM` every 30 seconds.   |
| Durability        | **Durable across coordinator crashes.** The Redis list survives coordinator failover; the new coordinator acquires the coordination lease, reads the existing inbox from Redis, and continues delivery. |
| Explicit ACK      | After delivering an inbox message to the runtime's stdin pipe (path 2 or after `await_children` unblock), the gateway removes the message from the Redis list with `LREM key 1 <serialised_message>`. Until ACK, the message remains at the list head and is re-delivered on coordinator restart. This provides at-least-once delivery within the coordinating replica lifetime. **Duplicate delivery note:** If the coordinator crashes after delivering a message to stdin but before executing the `LREM` ACK, the message will be re-delivered after coordinator recovery. The adapter MUST maintain a `delivered_message_ids` set (bounded to the last 1000 message IDs) that is included in checkpoint state. On re-delivery, the adapter checks this set and suppresses duplicates, emitting a `lenny_inbox_duplicate_suppressed_total` counter. Runtimes are therefore shielded from duplicate delivery under normal operation.  |
| Crash recovery    | On coordinator lease acquisition, the new replica calls `LRANGE t:{tenant_id}:session:{session_id}:inbox 0 -1` to recover all undelivered messages in FIFO order and re-attempts delivery. |

**Durable inbox prerequisites:** `durableInbox: true` requires Redis availability. If Redis becomes unreachable while durable mode is active, inbox enqueue operations fail and senders receive an `error` receipt with `reason: "inbox_unavailable"`. The gateway emits the `lenny_inbox_redis_unavailable_total` counter. For sessions where inbox unavailability is unacceptable, deployers should also enable the DLQ path as a fallback.

**Mode selection guidance:** Use `durableInbox: false` (default) for low-latency deployments where coordinator-crash message loss is acceptable and throughput is the priority. Use `durableInbox: true` for delegation-heavy deployments where inter-session message loss on coordinator crash would break agent coordination chains. The durable mode adds one Redis round-trip per enqueue and one per ACK; at Tier 1/2 throughputs this overhead is negligible.

The inbox guarantee in default mode is: **the gateway does not drop undelivered messages while the coordinating replica remains alive**. In durable mode: **the gateway does not drop undelivered messages as long as Redis remains available**. Neither mode is an end-to-end durability guarantee against simultaneous Redis + coordinator crashes. Deployers requiring stronger guarantees for the most critical messages should use the DLQ path (path 7) by treating the target session as temporarily unavailable.

**Inbox-to-DLQ migration on `resume_pending` transition (`durableInbox: false` only):** Messages that are in the session inbox at the moment the session transitions to `resume_pending` (due to pod failure or eviction) are at risk of loss, because the inbox is in-memory and the coordinating replica may crash before recovery completes. To close this gap, the gateway performs an **atomic inbox drain** when it writes the `resume_pending` state transition. **When `durableInbox: true`:** The inbox is already persisted in Redis and survives coordinator crashes, so the drain-to-DLQ step is unnecessary. Instead, the gateway leaves messages in the Redis-backed inbox; they are recovered by the new coordinator during lease acquisition (see durable inbox crash recovery above). The DLQ TTL (`maxResumeWindowSeconds`) is applied to the inbox key itself via `EXPIRE` to ensure stale messages are cleaned up if the session never resumes.

1. The gateway reads all messages currently in the session inbox (in FIFO order).
2. It writes each message to the session's DLQ in Redis (the same `t:{tenant_id}:session:{session_id}:dlq` sorted set used for externally queued messages), using the current time plus the session's `maxResumeWindowSeconds` TTL as the score.
3. It clears the in-memory inbox atomically after the Redis write completes.

This drain is performed as a single Redis pipeline call within the same goroutine that executes the `resume_pending` state transition, so no messages are lost between the inbox read and the DLQ write. If the Redis write fails (e.g., Redis is temporarily unavailable), the transition to `resume_pending` is still committed (pod failure is non-negotiable) but the inbox messages are lost; the `lenny_inbox_drain_failure_total` counter (labeled by `pool` and `session_state`) is incremented and a `WARN` log entry is emitted. For `awaiting_client_action` sessions, the DLQ is additionally backed by Postgres: when a session enters `awaiting_client_action`, the gateway flushes the Redis DLQ to the `session_dlq_archive` Postgres table (keyed by `(tenant_id, session_id, message_id)`) to ensure DLQ durability beyond the Redis TTL window. Messages are replayed from `session_dlq_archive` on parent resumption if the Redis DLQ has expired.

**Message delivery routing — seven paths:**

**Path precedence:** The paths below are evaluated in listed order; the **first matching path wins**. The path 3 vs. path 5 precedence rule applies **only when a single runtime has `lenny/request_input` in flight concurrently with one or more other blocking tool calls (such as `lenny/await_children`)** via parallel tool execution. In that specific overlap — and only in that overlap — the session-level `input_required` state (path 3) takes precedence over the runtime-level `await_children` condition (path 5), because `input_required` is a gateway-tracked session state. Being blocked in `lenny/await_children` alone (with no concurrent `lenny/request_input`) does **not** trigger path 3 precedence; such a runtime is governed by path 5. In practice, when both `await_children` and `request_input` are in flight, messages are buffered in the inbox and delivered only when the runtime returns to `ready_for_input` (which requires **all** in-flight tool calls to settle, per the `ready_for_input` definition below).

1. **`inReplyTo` matches outstanding `lenny/request_input`** → gateway resolves blocked tool call directly. No stdin delivery, no interrupt. Delivery receipt status: `delivered`.
2. **No matching pending request, runtime available** → `{type: "message"}` written to the runtime's stdin pipe. A runtime is considered _available_ when it is actively reading from stdin — that is, its adapter reports `ready_for_input` (between tool calls, after emitting output, or during any explicit input-wait). Delivery receipt status is **`delivered` only after confirmed stdin consumption** — that is, the adapter acknowledges the write within the configurable delivery timeout (default: 30 seconds). If the runtime does not consume the message within this timeout, the gateway treats it as undeliverable for this path and **falls through to inbox buffering** (path 5 behavior); in this fallback case the delivery receipt status is `queued`, not `delivered`. Messages buffered this way are delivered in FIFO order when the runtime next enters `ready_for_input`. The inbox will not drop messages while the coordinating replica is alive; see the session inbox definition above for overflow and crash semantics.

   **`ready_for_input` — normative definition for concurrent tool execution:** The adapter MUST emit `ready_for_input` only when **all** of the following conditions hold simultaneously: (a) the runtime has **no in-flight tool calls** — every tool call dispatched to the adapter has received its result or error and the call is fully settled; (b) the runtime is actively reading from stdin (i.e., blocked waiting for the next message). The adapter MUST NOT emit `ready_for_input` while any tool call is still in progress, even if the runtime would logically accept a new message at that point. When a runtime executes multiple tool calls concurrently (e.g., via parallel MCP tool invocations), `ready_for_input` MUST be deferred until all concurrent calls have settled. This rule ensures deterministic path 2 vs. path 5 routing: a message arriving while any tool call is in flight is routed to the inbox (path 5) rather than delivered directly, preventing non-deterministic adapter behavior across different runtime implementations.
3. **Target session is `input_required`** (blocked in `lenny/request_input`, no matching `inReplyTo`) → message is buffered in the session inbox. The runtime is not reading from stdin while blocked in a `request_input` call, so direct delivery is not possible — **this applies even when `delivery: "immediate"` is set** (see `delivery` field definition in [Section 15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol)). Buffered messages are delivered in FIFO order once the `request_input` resolves (via a matching `inReplyTo` message, cancellation, or expiry) and the runtime returns to `ready_for_input`. Delivery receipt status: `queued`. **Overlap with path 5:** This overlap rule applies **only** when the runtime has `lenny/request_input` in flight **concurrently** with `await_children` (or other blocking tool calls). When that specific concurrency exists, this path (path 3) governs over path 5. The `input_required` session state is the authoritative routing signal because it is a gateway-tracked session state, whereas `await_children` is a runtime-level blocking condition. In this overlap scenario, buffered messages are delivered when the runtime reaches `ready_for_input` (all tool calls settled), not at the next `await_children` partial event. A runtime blocked in `await_children` **without** a concurrent `request_input` is not in `input_required` state and is governed by path 5, not path 3.
4. **`delivery: "immediate"`, session is `running` with tool call in flight (not `input_required`)** → the gateway sends an interrupt signal on the lifecycle channel. Full-level runtimes: the gateway waits for `interrupt_acknowledged` before delivering. Basic/Standard-level runtimes: the gateway proceeds immediately after the in-flight stdin write completes. The message is then written to the runtime's stdin pipe. If the runtime does not consume the message within the delivery timeout (default: 30 seconds), the message falls through to inbox buffering (path 5 behavior) with receipt status `queued`. Otherwise receipt status: `delivered`. This path applies only when `delivery: "immediate"` is explicitly set; messages without this flag that arrive while a tool call is in flight are routed to the inbox via path 5.
5. **No matching pending request, runtime busy (blocked in `await_children`, tool call in flight, or otherwise not `ready_for_input`) and session is NOT in `input_required` state** → buffered in the session inbox (see inbox definition above); delivered in FIFO order when the runtime next enters `ready_for_input`.
6. **Target session is `suspended`** → message is buffered in the session inbox. If the message carries `delivery: "immediate"`, behavior depends on whether the session still holds a pod (see [§6.2](06_warm-pod-model.md#62-pod-state-machine) `maxSuspendedPodHoldSeconds`):
   - **Pod still held:** The gateway atomically resumes the session (`suspended → running`) and delivers the message to the runtime's stdin pipe once the runtime reports `ready_for_input`. The delivery receipt is `delivered` on successful resume-and-deliver.
   - **Pod released (podless suspension):** The gateway transitions the session to `resume_pending` (`suspended → resume_pending`). The message is held in the session inbox; the standard `resume_pending` inbox handling applies (inbox-to-DLQ drain for `durableInbox: false`, or Redis inbox retention for `durableInbox: true` — see [§7.2](#72-interactive-session-model)). A new pod must be acquired and the workspace restored from checkpoint before delivery. The delivery receipt is `queued`.

   This applies uniformly to all message sources: external client (`POST /v1/sessions/{id}/messages`) and inter-session via `lenny/send_message`. Messages without `delivery: "immediate"` remain buffered until an explicit `resume_session` or a subsequent `delivery: "immediate"` message triggers the resume, at which point all buffered inbox messages are delivered in FIFO order. **Coordinator routing for `delivery: immediate` resume:** The `suspended → running` (or `suspended → resume_pending`) transition requires Postgres state writes and (when the pod is held) a resume RPC to the pod, both of which must be performed by the session's coordinating gateway replica. When a `delivery: immediate` message lands on a non-coordinator replica, that replica forwards the message to the session's coordinator (identified via the coordination lease in Redis/Postgres). The coordinator executes the atomic resume-and-deliver sequence (or the `resume_pending` transition for podless sessions). If the coordinator is unreachable (e.g., crashed, network partition), the forwarding replica falls back to inbox buffering with a `queued` delivery receipt status — the message is not silently dropped. The coordinator forwarding mechanism reuses the same internal gRPC `ForwardMessage` RPC used for all cross-replica message routing (see [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) per-session coordination).
7. **Target session in terminal or recovering state** → see dead-letter handling below.

**Concurrent-workspace mode (`slotId`) routing:** In concurrent-workspace mode, each active slot maintains its own independent inbox on the coordinating gateway replica. The `slotId` field in the `MessageEnvelope` determines which slot's inbox receives the message. Path evaluation (paths 1-7 above) is performed **per-slot**: `ready_for_input`, `input_required`, and `await_children` are tracked per-slot, not per-pod. A message with `slotId: "slot_01"` can be delivered (path 2) to slot 01 while slot 02 is in `input_required` (path 3). Messages without a `slotId` in concurrent-workspace mode are rejected with `SLOT_ID_REQUIRED`. The `delivery: "immediate"` interrupt (path 4) targets the specific slot's tool-call context, not the entire pod. See [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) for full concurrent-workspace semantics.

**Dead-letter handling for inter-session messages:**

The gateway checks target session state before routing. Behavior depends on the target's state:

| Target state                                             | Behavior                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Pre-running (`created`, `ready`, `starting`, `finalizing`) | **Inter-session messages (parent→child via `lenny/send_message`):** The gateway buffers the message in the target session's DLQ (same mechanism as recovering states) with a TTL of `maxCreatedStateTimeoutSeconds` (default: 300s). The message is delivered when the session reaches `running`. Delivery receipt status: `queued`. **External client messages (`POST /v1/sessions/{id}/messages`):** Message is rejected with `TARGET_NOT_READY` — session has not yet entered `running` state. Client should retry after starting the session. This distinction exists because a parent that just delegated a child knows the child will eventually start, whereas an external client targeting a pre-running session likely has a sequencing error.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| Terminal (`completed`, `failed`, `cancelled`, `expired`) | Gateway returns an error to the sender immediately: `{ "code": "TARGET_TERMINAL", "message": "Target task {id} is in terminal state {state}", "targetState": "{state}" }`. The message is not enqueued.                                                                                                                                                                                                                                                                                                                                                                                                                                                            |
| Recovering (`resume_pending`, `awaiting_client_action`)  | Message is enqueued in a **dead-letter queue** (DLQ) stored in Redis (sorted set keyed by `t:{tenant_id}:session:{session_id}:dlq`, scored by expiry timestamp) with a configurable TTL (default: `maxResumeWindowSeconds` of the target session, or 900s if unset). The canonical DLQ key format is `t:{tenant_id}:session:{session_id}:dlq` — this follows the platform-wide tenant key prefix convention ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) and ensures a DLQ processor iterating across keys cannot read messages belonging to a different tenant. The DLQ has a `maxDLQSize` cap (default: 500 messages); on overflow, the oldest DLQ entry is dropped and the sender receives a `message_dropped` delivery receipt with `reason: "dlq_overflow"`. If the target resumes before TTL expiry, queued messages are delivered in FIFO order. On TTL expiry, undelivered messages are discarded and the sender receives a `message_expired` notification via the `delivery_receipt` mechanism (see below). |

**Inbox drain on terminal transition:** When a session transitions to a terminal state (`completed`, `failed`, `cancelled`, `expired`) while its inbox (in-memory or Redis-backed) contains undelivered messages, the gateway drains those messages to the DLQ with a short TTL (default: 60 seconds) to allow post-mortem retrieval by monitoring tools. For each drained message, the gateway emits a `message_expired` event to the original sender's event stream with `reason: "target_terminated"`. This ensures senders that previously received a `queued` delivery receipt are notified that their message was not consumed.

**Delivery receipts:** All `lenny/send_message` calls return a `deliveryReceipt`. The canonical schema is defined in [Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification) (`delivery_receipt` acknowledgement schema). The `status` values are: `delivered` (runtime consumed), `queued` (buffered in inbox or DLQ), `dropped` (inbox/DLQ overflow), `expired` (DLQ TTL elapsed), `rate_limited` (inbound rate cap exceeded), `error` (infrastructure or policy failure).

For queued messages that later expire, the gateway emits a `message_expired` event to the sender's event stream: `{ "type": "message_expired", "messageId": "msg_abc123", "reason": "target_ttl_exceeded" }`.

**Reconnect semantics:** The gateway persists an event cursor per session. On reconnect, the client provides its last-seen cursor and the gateway replays missed events from the EventStore. The **replay window** is defined as `max(periodicCheckpointIntervalSeconds × 2, 1200s)` — by default 1200 seconds (20 minutes). Events older than the replay window are not guaranteed to be present in the EventStore. When the client's last-seen cursor falls outside the replay window, the gateway cannot replay the intervening events and instead emits a `checkpoint_boundary` marker followed by the current session state. The `checkpoint_boundary` marker has the following schema:

```json
{
  "type": "checkpoint_boundary",
  "cursor": "<current_cursor>",
  "events_lost": 42,
  "reason": "replay_window_exceeded",
  "checkpoint_timestamp": "<ISO8601>"
}
```

The `events_lost` field contains the count of events that occurred between the client's last-seen cursor and the start of the replay window and are therefore undeliverable. A value of `0` means all events since the cursor are available; a positive value is a client data-loss event that MUST be surfaced to the user or logged by the client. The `reason` field is `"replay_window_exceeded"` when the cursor is outside the window and `"event_store_unavailable"` when the EventStore itself cannot be queried. Clients MUST treat any `checkpoint_boundary` with `events_lost > 0` as a gap in their event history.

**SSE back-pressure policy:** Connection-coupled adapters (SSE, long-poll) use the **bounded-error** `OutboundChannel` policy defined in [Section 15](15_external-api-surface.md): a non-blocking write is attempted, and if the subscriber's read loop is behind such that the write would block, `Send` returns a non-nil error within `MaxOutboundSendTimeoutMs` (default: 100 ms). The gateway then closes the channel and drops the connection. The client must reconnect with its last-seen cursor; missed events are replayed from the EventStore (if within the replay window defined above). This ensures a single slow client cannot cause unbounded memory growth in the gateway. At Tier 3, deployers should monitor total gateway memory and the `lenny_outbound_channel_buffer_drop_total` counter.

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

**Session generations:** Each recovery creates a new `recovery_generation` of the same logical session (see [Section 4.2](04_system-components.md#42-session-manager) for the distinction between `recovery_generation` and `coordination_generation`). The client always sees one session_id.

**Resume flow after pod failure:**

1. Gateway detects session failure
2. Classify failure (retryable vs. non-retryable)
3. If retryable and `retryCount < maxRetries`:
   a. Transition to `resume_pending`; start `maxResumeWindowSeconds` wall-clock timer
   b. Allocate new warm pod (may wait if pool is temporarily exhausted)
   c. If `maxResumeWindowSeconds` fires before pod is allocated → transition to `awaiting_client_action` (same as step 4)
   d. Recreate same absolute `cwd` path
   e. Replay latest workspace checkpoint
   f. Restore session file to expected path
   g. Resume session (native SDK resume or fresh session with carried state)
4. If retries exhausted → state becomes `awaiting_client_action`

**Client actions after retry exhaustion:**

- Resume anyway (explicit override)
- Start fresh session from latest checkpoint
- Download artifacts / logs / transcript
- Terminate session
- Fork into a new session

**`awaiting_client_action` semantics:**

- **Entry paths:** Sessions enter `awaiting_client_action` in two ways: (a) auto-retry exhaustion (`retryCount >= maxRetries`) — the platform has given up automatic recovery; or (b) `resume_pending` timeout — `maxResumeWindowSeconds` elapsed while waiting for a pod to become available (pool exhaustion or scheduling delay). In both cases, client intervention is required.
- **Expiry:** Sessions in `awaiting_client_action` expire after `maxAwaitingClientActionSeconds` (default 900s, configurable via `runtime.maxAwaitingClientActionSeconds`). This timer starts fresh on entry to `awaiting_client_action` — it is independent of the `maxResumeWindowSeconds` timer that governs `resume_pending` (a session that spent time in `resume_pending` before entering `awaiting_client_action` gets a full, fresh `maxAwaitingClientActionSeconds` window). After expiry the session transitions to `expired` — a terminal state. The gateway applies the session's `cascadeOnFailure` policy to all active children (same behavior as terminal failure after retry exhaustion). Artifacts are retained per the standard retention policy.
- **DLQ drain on terminal transition:** On any terminal state transition (`completed`, `failed`, `cancelled`, `expired`) of a session that has an active DLQ (messages enqueued while in `resume_pending` or `awaiting_client_action`), the gateway drains the DLQ by sending `message_expired` delivery receipts to all registered senders for each queued entry, with `reason: "session_terminal"`. The DLQ Redis key is then deleted. This ensures senders are not held waiting for a session that will never resume.
- **Children behavior:** Active children continue running when the parent enters `awaiting_client_action`. As each child reaches a terminal state (completed, failed, cancelled, expired), the gateway persists the child's completion event — including the full `TaskResult` payload — to the `session_tree_archive` Postgres table (keyed by `(root_session_id, node_session_id)`) rather than holding it only in the in-memory virtual child interface. This durability guarantee ensures that child completion events are not lost if the coordinating gateway replica crashes while the parent is in `awaiting_client_action`. On parent resumption, the gateway replays any archived child results from `session_tree_archive` before entering live-wait for any still-running children, so the parent receives a complete and consistent view of all child outcomes regardless of how long the parent remained in `awaiting_client_action`.
- **CI / automated discovery:** Automated clients can poll `GET /v1/sessions/{id}` and check for `state: awaiting_client_action`. The webhook system ([Section 14](14_workspace-plan-schema.md), `callbackUrl`) also fires a `session.awaiting_action` event so CI systems can react without polling.

### 7.4 Upload Safety

All uploads are gateway-mediated. **Pre-start uploads** are the default. **Mid-session uploads** are supported as an opt-in capability.

**Mid-session uploads:** If the runtime declares `capabilities.midSessionUpload: true` and the deployer policy allows it, clients can call `upload_to_session(session_id, files)` during an active session. Mid-session uploads use the same staging→validation→promotion pattern as pre-start uploads. Files are first written to `/workspace/staging`, validated (path traversal protection, size limits, hash verification), then atomically moved to `/workspace/current`. The runtime adapter receives a `FilesUpdated` notification only after promotion, so the agent never sees partially-written files.

**Upload authorization:** Every upload and finalize request must carry the `uploadToken` issued at session creation (see [Section 7.1](#71-normal-flow) `uploadToken` format and security properties). The gateway validates the token's HMAC signature, checks that it has not expired, confirms the embedded `session_id` matches the target session, and rejects any token that has already been consumed by a prior `FinalizeWorkspace` call. Mid-session uploads (after `FinalizeWorkspace`) use the caller's normal session-scoped bearer credential instead; the `uploadToken` is not reissued.

**Enforcement rules:**

- All paths relative to workspace root
- Reject `..`, absolute paths, path traversal
- Reject symlinks, hard links, device files, FIFOs, sockets
- Per-file and total session size limits
- **Inbound body hard cap:** The gateway wraps every upload request body in an `io.LimitedReader` bounded by `remaining_quota_bytes` (see [Section 11.2](11_policy-and-controls.md#112-budgets-and-quotas) storage quota enforcement). This cap is enforced at the I/O layer independently of the client-supplied `Content-Length` header, so a client cannot bypass the pre-upload quota check by declaring a small `Content-Length` and streaming a larger body. If the cap is reached the upload is aborted with `STORAGE_QUOTA_EXCEEDED` and any bytes already written to staging are removed.
- Hash verification:
  - **Optional** for client uploads (the client may not have pre-computed hashes)
  - **Mandatory** for delegation file exports (the gateway computes and verifies hashes during the export-to-child flow to ensure no tampering between parent export and child delivery)
- Write to staging first, promote only after validation
- Archive extraction is especially strict:
  - **Supported formats:** `tar.gz`, `tar.bz2`, `zip`. Other formats are rejected.
  - **Zip-slip protection:** Every extracted path is validated to resolve within the staging directory after canonicalization. Paths containing `..` components or absolute paths are rejected. Symlinks pointing outside the staging directory are rejected.
  - **Symlink handling:** Symlinks within archives are rejected by default. A `allowSymlinks: true` option can be set per Runtime for runtimes that require them, but even then symlinks must resolve within the workspace root. Because symlink targets are resolved relative to the symlink's own location, validation performed in `/workspace/staging` may yield different absolute paths than the same symlink at its promoted location in `/workspace/current`. Therefore, after the atomic staging→current promotion, the gateway **re-validates every symlink** in the promoted tree: each symlink target is re-resolved against its new location under `/workspace/current`, and any symlink whose resolved target falls outside `/workspace/current` causes the entire promotion to be rolled back and the extracted content to be removed.
  - **Atomic cleanup:** If extraction fails at any point (invalid path, size limit, format error), all already-extracted files are removed from staging before the error is returned. The staging directory is returned to its pre-extraction state.
  - **Size limits:** Total extracted size is checked against the per-session upload limit. Extraction aborts immediately if the limit is exceeded (no "extract then check").
  - **Zip bomb protection:** The extractor enforces a configurable decompression ratio limit (default: 100:1 compressed-to-uncompressed). During streaming extraction, cumulative compressed bytes read and cumulative uncompressed bytes written are tracked; extraction aborts immediately if the ratio exceeds the configured maximum. Additionally, all decompressor `Read()` calls are wrapped with a per-call size cap (e.g., `io.LimitedReader` with a 1 MB bound) to prevent a single read from allocating unbounded memory. Abort causes are labeled and emitted via `lenny_upload_extraction_aborted_total{error_type}` (see [Section 16.1](16_observability.md#161-metrics)).
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

