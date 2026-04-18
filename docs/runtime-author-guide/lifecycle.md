---
layout: default
title: "Pod Lifecycle"
parent: "Runtime Author Guide"
nav_order: 5
---

# Pod Lifecycle

This page covers the complete lifecycle of a Lenny agent pod --- from pre-warming through session execution to termination. Understanding these states helps you write runtimes that handle startup, checkpointing, interrupts, and shutdown correctly.

---

## Pod States

Every Lenny agent pod follows a state machine. The exact path depends on whether the pool uses **pod-warm** (default) or **SDK-warm** (`preConnect: true`) mode.

### Pod-Warm Path (Default)

```
warming ──→ idle ──→ claimed ──→ receiving_uploads ──→ finalizing_workspace
                                                              │
                                                              ▼
                         attached ←── starting_session ←── running_setup
```

| State | What Happens |
|-------|-------------|
| `warming` | Pod is scheduled, container starts, adapter boots, health checks pass. No session is bound. |
| `idle` | Pod is healthy and claimable. Listed in the warm pool. `/workspace/current` exists but is empty. |
| `claimed` | Gateway has selected this pod for a session. No other session can claim it. |
| `receiving_uploads` | Client files are streaming into `/workspace/staging`. |
| `finalizing_workspace` | Files are validated and promoted from `/workspace/staging` to `/workspace/current`. |
| `running_setup` | Setup commands (if any) execute in the workspace. Bounded by `setupTimeoutSeconds` (default: 300s). |
| `starting_session` | Agent binary is spawned with stdin/stdout pipes connected. |
| `attached` | Session is live. Bidirectional message flow begins. |

### SDK-Warm Path (preConnect: true)

```
warming ──→ sdk_connecting ──→ idle ──→ claimed ──→ receiving_uploads
                                                         │
                                                         ▼
                         attached ←── finalizing_workspace ──→ running_setup
```

In SDK-warm mode, the agent process starts during the warm phase (before any session) to eliminate cold-start latency. The adapter pre-connects the SDK, then waits for session assignment.

**Demotion:** If a claimed session's workspace includes files matching `sdkWarmBlockingPaths` (default: `["CLAUDE.md", ".claude/*"]`), the adapter tears down the pre-connected process, returns to pod-warm state, and proceeds via the normal path. This adds 1--3 seconds but ensures the agent starts with workspace files present.

---

## Session Binding

A pod is bound to exactly one session for its entire lifetime (in `session` execution mode). After the session completes or fails, the pod is terminated and replaced --- never recycled for a different session. This prevents cross-session data leakage through residual files, cached DNS, or runtime memory.

**Task mode** relaxes this constraint: pods are reused across sequential tasks with workspace scrubbing between tasks. See the task-mode state transitions below.

---

## Workspace Materialization

When a pod is claimed for a session, the gateway materializes the client's files into the workspace:

1. **Upload phase:** Files stream from the client through the gateway into `/workspace/staging` on the pod. Each file is validated (no path traversal, no symlinks outside workspace, size limits enforced).

2. **Finalization:** The gateway promotes files from `/workspace/staging` to `/workspace/current`. Archive extraction (tar.gz, tar.bz2, zip) happens here with zip-slip protection.

3. **Setup commands:** If the runtime defines setup commands (e.g., `npm install`), they run in `/workspace/current` with a bounded timeout. Setup command output is captured for diagnostics.

4. **Agent start:** Your binary is spawned with its working directory set to `/workspace/current`.

### Filesystem Layout

```
/workspace/
  current/      # Your working directory --- populated during finalization
  staging/      # Upload staging area --- files land here first
/sessions/      # Session files (conversation logs, runtime state)    [tmpfs]
/artifacts/     # Logs, outputs, checkpoints
/tmp/           # Writable scratch area                               [tmpfs]
```

- `/sessions/` and `/tmp/` use tmpfs (data is guaranteed gone when the pod terminates).
- `/workspace/` and `/artifacts/` use disk-backed emptyDir. Node-level disk encryption is required for production.

### Adapter Manifest

Before your binary starts, the adapter writes `/run/lenny/adapter-manifest.json`. At the Basic level, you can ignore this file. At the Standard level, you read it to discover MCP server sockets:

```json
{
  "sessionId": "sess_abc123",
  "taskId": "task_xyz",
  "platformMcpServer": {
    "socket": "@lenny-platform-mcp"
  },
  "connectorServers": [
    { "id": "github", "socket": "@lenny-connector-github" }
  ],
  "mcpNonce": "a1b2c3d4e5f6..."
}
```

---

## Session States (From Attached)

Once a session reaches `attached`, it enters the interactive session state machine:

```
                        attached
                        │
                ┌───────┼───────────────┬────────────────┬──────────┐
                ▼       ▼               ▼                ▼          ▼
           completed   failed    resume_pending     suspended   cancelled
                                     │                   │
                                ┌────┤              ┌────┼────────┐
                                ▼    ▼              ▼    ▼        ▼
                           resuming  awaiting    running completed resume_pending
                              │       _client     (resume)
                              ▼       _action
                           attached     │
                                        ▼
                                      expired
```

### Key States

| State | Meaning |
|-------|---------|
| `running` | Session is active. Your binary is processing messages. |
| `input_required` | Sub-state of `running`. Your runtime called `lenny/request_input` and is blocked waiting for a response. |
| `suspended` | Session is paused via `interrupt_request`. Pod is held (initially). `maxSessionAge` timer is paused. |
| `resume_pending` | Pod failed. Gateway is acquiring a new pod for recovery. |
| `resuming` | Workspace is being restored onto a new pod. |
| `awaiting_client_action` | Auto-retries exhausted. Client must decide: resume, terminate, or download artifacts. |
| `completed` | Session finished normally. Terminal state. |
| `failed` | Unrecoverable error or retries exhausted. Terminal state. |
| `cancelled` | Client or parent cancelled the session. Terminal state. |
| `expired` | Budget, lease, or deadline exhausted. Terminal state. |

---

## Checkpointing

Checkpointing creates a snapshot of the workspace so the session can recover after pod failure. The behavior depends on your integration level.

### Basic level: no checkpoint

The adapter performs **no checkpoint**. If the pod fails, all in-flight context is lost. The gateway restarts the session from the last gateway-persisted state, which may be significantly behind your runtime's actual progress.

This is acceptable for idempotent or stateless workloads.

### Standard level: best-effort snapshot

The adapter takes **best-effort snapshots** without pausing your runtime. The workspace is snapshotted while your binary continues running, so files written during the snapshot window may be inconsistent. On resume, minor workspace inconsistencies are possible.

For most workloads this is sufficient.

### Full level: cooperative checkpoint

Full-level runtimes participate in a handshake that guarantees **consistent snapshots**:

```
1. Adapter sends checkpoint_request on the lifecycle channel:
   {"type":"checkpoint_request","checkpointId":"chk_42","deadlineMs":60000}

2. Your runtime:
   - Finishes current output write
   - Flushes all buffers
   - Ensures workspace files are in a consistent state
   - Does NOT exit or stop processing permanently

3. Your runtime replies:
   {"type":"checkpoint_ready","checkpointId":"chk_42"}

4. Adapter snapshots the workspace filesystem.

5. Adapter sends checkpoint completion:
   {"type":"checkpoint_complete","checkpointId":"chk_42","status":"ok"}

6. Your runtime resumes normal operation.
```

If `checkpoint_ready` is not received within `deadlineMs` (default 60 seconds), the adapter falls back to best-effort snapshot and sets a `checkpointStuck` health flag. Your process is not killed --- it continues running, but the checkpoint may be inconsistent.

---

## Resume After Pod Failure

When a pod fails (eviction, OOM, node failure), the gateway attempts automatic recovery:

1. Gateway detects the failure and classifies it (retryable vs. non-retryable).
2. If retryable and `retryCount < maxRetries`:
   - Session transitions to `resume_pending`.
   - Gateway allocates a new warm pod.
   - Recreates the same workspace directory structure.
   - Replays the latest checkpoint.
   - Restores session state.
   - Resumes the session (your binary restarts on the new pod).
3. If retries exhausted, session becomes `awaiting_client_action`.

Your runtime does not need to implement any resume logic --- the adapter handles it. From your binary's perspective, you start fresh on the new pod and receive the first `message` on stdin as if it were a new session.

### The `session.resumed` Event

After a successful resume, the client receives a `session.resumed` event:

```json
{
  "type": "session.resumed",
  "resumeMode": "full",
  "workspaceLost": false
}
```

- `resumeMode`: `full` (workspace restored from checkpoint) or `conversation_only` (workspace could not be restored).
- `workspaceLost`: `true` if the workspace snapshot was unavailable or corrupt.

---

## Interrupt and Suspend (Full level)

Full-level runtimes can handle clean interrupts via the lifecycle channel:

```
1. Adapter sends interrupt_request:
   {"type":"interrupt_request","interruptId":"int_001","deadlineMs":30000}

2. Your runtime reaches a safe stop point (finishes current output, flushes).

3. Your runtime replies:
   {"type":"interrupt_acknowledged","interruptId":"int_001"}

4. Session transitions to "suspended".
```

While suspended:
- Pod is held (initially) and workspace is preserved.
- `maxSessionAge` timer is paused.
- After `maxSuspendedPodHoldSeconds` (default: 900s / 15 minutes), the gateway checkpoints and releases the pod.
- The session can be resumed by the client sending a new message or calling `resume_session`.

At the Basic and Standard levels, interrupt is SIGTERM-based --- there is no clean handshake.

---

## Credential Rotation

When an LLM provider rate-limits or revokes a credential, the platform rotates it. The behavior depends on your integration level:

| Level | Method | Session Impact |
|------|--------|----------------|
| **Full** | `credentials_rotated` on lifecycle channel; runtime rebinds in-place | No session interruption |
| **Standard** | Gateway triggers checkpoint, terminates pod, resumes on new pod with new credentials | Brief pause; client sees a reconnect |
| **Basic** | Same as Standard. If no checkpoint support, in-flight context is lost | Pause; potential context loss |

### Full-level credential rotation

```
1. Adapter sends on lifecycle channel:
   {"type":"credentials_rotated","provider":"anthropic","credentialsPath":"/run/lenny/credentials.json","leaseId":"lease_xyz"}

2. Your runtime re-reads the credentials file and rebinds the LLM client.

3. Your runtime replies:
   {"type":"credentials_acknowledged","leaseId":"lease_xyz","provider":"anthropic"}
```

---

## Deadline Signals (Full level)

Full-level runtimes receive advance warning before session expiry:

```json
{"type":"deadline_approaching","remainingMs":60000}
```

This gives your runtime time to wrap up long-running work, flush outputs, and produce a partial result before the hard deadline arrives.

At the Basic and Standard levels, you receive only a `shutdown` message when the deadline is reached.

---

## Terminate Signal (Full level)

Full-level runtimes receive a `terminate` message on the lifecycle channel as the preferred shutdown path, instead of the stdin `shutdown` message. This is distinct from stdin `shutdown` --- `terminate` arrives on the lifecycle channel and is the primary graceful shutdown mechanism for Full-level runtimes.

```json
{"type":"terminate","deadlineMs":10000,"reason":"session_complete"}
```

| Field | Type | Description |
|-------|------|-------------|
| `deadlineMs` | integer | Time in milliseconds before the adapter sends SIGTERM. |
| `reason` | string | One of `"session_complete"`, `"budget_exhausted"`, `"eviction"`, or `"operator"`. |

Your runtime must exit within `deadlineMs`. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds. `terminate` always means process exit --- it is never used for between-task signaling (see `task_complete` below).

---

## LLM Request Tracking (Full level, Direct Mode)

Runtimes that call LLM provider APIs directly (not through the adapter proxy) should emit `llm_request_started` and `llm_request_completed` messages on the lifecycle channel. These signals allow the adapter to track in-flight LLM requests for credential rotation coordination --- the adapter will not send `credentials_rotated` while LLM requests are in flight.

### `llm_request_started` (runtime to adapter)

Emitted just before the runtime sends an outbound LLM request directly to the provider.

```json
{"type":"llm_request_started","requestId":"req_001","provider":"anthropic"}
```

| Field | Type | Description |
|-------|------|-------------|
| `requestId` | string | Opaque, runtime-generated identifier for this request. |
| `provider` | string | The LLM provider being called (e.g., `"anthropic"`, `"openai"`). |

### `llm_request_completed` (runtime to adapter)

Emitted when the outbound LLM request completes or errors.

```json
{"type":"llm_request_completed","requestId":"req_001","provider":"anthropic","status":"ok"}
```

| Field | Type | Description |
|-------|------|-------------|
| `requestId` | string | Matches the corresponding `llm_request_started`. |
| `provider` | string | The LLM provider that was called. |
| `status` | string | `"ok"` or `"error"`. |

When the in-flight counter for a provider reaches zero and a credential rotation is pending, the adapter proceeds to send `credentials_rotated`.

---

## Task-Mode Pod Reuse (Full level)

Task-mode pods execute sequential tasks without pod replacement:

```
attached ──→ task_cleanup ──→ idle ──→ (next task) ──→ attached
```

The lifecycle channel drives the handshake:

1. Task completes. Adapter sends `task_complete` on the lifecycle channel.
2. Your runtime releases task-specific resources and replies `task_complete_acknowledged`.
3. Workspace is scrubbed (files removed, processes killed).
4. Adapter sends `task_ready` with the new task ID.
5. Your runtime re-reads the adapter manifest (regenerated per task).
6. The next `message` on stdin starts the new task.

After `maxTasksPerPod` tasks or when `maxPodUptimeSeconds` is exceeded, the pod drains and is replaced.

---

## Execution Modes

Pools are configured with an execution mode that determines how tasks are mapped to pods. The mode affects your runtime's lifecycle, workspace layout, and required integration level.

### Session Mode (Default)

One session per pod. Your runtime receives one task, handles it, and the pod terminates after the session ends. No special runtime code is needed beyond the base adapter contract for your integration level. The pod is never recycled for a different session --- this prevents cross-session data leakage.

This is the simplest mode and works at every integration level.

### Task Mode

The pod is reused across sequential tasks with workspace scrubbing between tasks. This avoids the overhead of pod provisioning for each task.

Task mode requires **Full-level integration** (lifecycle channel) for actual pod reuse. Standard and Basic level runtimes effectively get one task per pod because they cannot participate in the `task_complete` / `task_complete_acknowledged` / `task_ready` lifecycle handshake.

The between-task sequence on the lifecycle channel:

1. Adapter sends `task_complete` with the finished `taskId`.
2. Your runtime releases task-specific resources (open files, temp state) and replies `task_complete_acknowledged`.
3. The adapter scrubs the workspace (files removed, processes killed).
4. Adapter sends `task_ready` with the new `taskId`.
5. Your runtime re-reads the adapter manifest (regenerated per task) and prepares for the next `message` on stdin.

After `maxTasksPerPod` tasks or when `maxPodUptimeSeconds` is exceeded, the pod drains and is replaced.

### Concurrent-Workspace Mode

Multiple tasks run simultaneously on a single pod. Your runtime must implement a **dispatch loop keyed on `slotId`** --- all binary protocol messages (inbound and outbound) carry a `slotId` field in this mode. Each slot gets its own workspace under `/workspace/slots/{slotId}/current/`. Your runtime must NOT assume a global `/workspace/current` path.

Cross-slot isolation is process-level and filesystem-level only --- explicitly weaker than session mode. CPU and memory are shared across slots (no per-slot cgroup subdivision in v1).

`preConnect` is incompatible with concurrent-workspace mode. The pool controller rejects pool definitions that combine `executionMode: concurrent`, `concurrencyStyle: workspace`, and `capabilities.preConnect: true`.

### Concurrent-Stateless Mode

No workspace materialization. Requests are routed via Kubernetes Service load balancing. There is no per-slot lifecycle tracking, no checkpoint support, and no per-slot failure isolation. This mode is typically better served by the external connector model.

Your runtime just handles requests as they arrive. The deployer is responsible for retry, idempotency, and error-handling logic. `preConnect` is incompatible with concurrent-stateless mode.

---

## Health Checks

The adapter implements the gRPC Health Checking Protocol. Your binary does not need to implement health checks directly --- the adapter handles it. The heartbeat mechanism serves as a liveness check:

1. Adapter sends `{"type":"heartbeat"}` on stdin.
2. Your binary MUST respond with `{"type":"heartbeat_ack"}` within **10 seconds**.
3. Failure to respond triggers SIGTERM.

The heartbeat handler should be immediate --- do not do heavy work before responding.

---

## Pod Termination

Pods are terminated in the following cases:

- Session completes or fails (session mode).
- `maxTasksPerPod` reached (task mode).
- `maxPodUptimeSeconds` exceeded.
- Pool scaling down (surplus pods).
- Node drain or eviction.

The termination sequence:

1. Adapter sends `shutdown` on stdin with `deadline_ms`.
2. Your binary should finish current work and exit within the deadline.
3. If your binary does not exit, the adapter sends SIGTERM.
4. If the process still does not exit, Kubernetes sends SIGKILL after `terminationGracePeriodSeconds`.

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Clean exit |
| 1 | Runtime error (captured in diagnostics) |
| 2 | Protocol error (runtime could not parse adapter messages) |
| 137 | SIGKILL (OOM or timeout) |

---

## Seal and Export

Before a pod is released, the gateway always exports the final workspace snapshot to durable storage (MinIO):

1. Workspace files are sealed and uploaded.
2. If export fails, the pod is held and retried with exponential backoff.
3. After `maxWorkspaceSealDurationSeconds` (default: 300s), the gateway gives up and terminates the pod anyway.

This ensures session output is preserved for the client to download after the session ends.
