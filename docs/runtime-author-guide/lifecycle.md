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

Before your binary starts, the adapter writes `/run/lenny/adapter-manifest.json`. At Minimum tier, you can ignore this file. At Standard tier, you read it to discover MCP server sockets:

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

Checkpointing creates a snapshot of the workspace so the session can recover after pod failure. The behavior depends on your integration tier.

### Minimum Tier: No Checkpoint

The adapter performs **no checkpoint**. If the pod fails, all in-flight context is lost. The gateway restarts the session from the last gateway-persisted state, which may be significantly behind your runtime's actual progress.

This is acceptable for idempotent or stateless workloads.

### Standard Tier: Best-Effort Snapshot

The adapter takes **best-effort snapshots** without pausing your runtime. The workspace is snapshotted while your binary continues running, so files written during the snapshot window may be inconsistent. On resume, minor workspace inconsistencies are possible.

For most workloads this is sufficient.

### Full Tier: Cooperative Checkpoint

Full-tier runtimes participate in a handshake that guarantees **consistent snapshots**:

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

## Interrupt and Suspend (Full Tier)

Full-tier runtimes can handle clean interrupts via the lifecycle channel:

```
1. Adapter sends interrupt_request:
   {"type":"interrupt_request","deadlineMs":30000}

2. Your runtime reaches a safe stop point (finishes current output, flushes).

3. Your runtime replies:
   {"type":"interrupt_acknowledged"}

4. Session transitions to "suspended".
```

While suspended:
- Pod is held (initially) and workspace is preserved.
- `maxSessionAge` timer is paused.
- After `maxSuspendedPodHoldSeconds` (default: 900s / 15 minutes), the gateway checkpoints and releases the pod.
- The session can be resumed by the client sending a new message or calling `resume_session`.

At Minimum and Standard tiers, interrupt is SIGTERM-based --- there is no clean handshake.

---

## Credential Rotation

When an LLM provider rate-limits or revokes a credential, the platform rotates it. The behavior depends on your tier:

| Tier | Method | Session Impact |
|------|--------|----------------|
| **Full** | `credentials_rotated` on lifecycle channel; runtime rebinds in-place | No session interruption |
| **Standard** | Gateway triggers checkpoint, terminates pod, resumes on new pod with new credentials | Brief pause; client sees a reconnect |
| **Minimum** | Same as Standard. If no checkpoint support, in-flight context is lost | Pause; potential context loss |

### Full-Tier Credential Rotation

```
1. Adapter sends on lifecycle channel:
   {"type":"credentials_rotated","credentialPath":"/run/lenny/credentials.json"}

2. Your runtime re-reads the credentials file and rebinds the LLM client.

3. Your runtime replies:
   {"type":"credentials_acknowledged"}
```

---

## Deadline Signals (Full Tier)

Full-tier runtimes receive advance warning before session expiry:

```json
{"type":"deadline_approaching","remainingMs":60000}
```

This gives your runtime time to wrap up long-running work, flush outputs, and produce a partial result before the hard deadline arrives.

At Minimum and Standard tiers, you receive only a `shutdown` message when the deadline is reached.

---

## Task-Mode Pod Reuse (Full Tier)

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
| 137 | SIGKILL (OOM or timeout) |
| 143 | SIGTERM (graceful but forced) |

---

## Seal and Export

Before a pod is released, the gateway always exports the final workspace snapshot to durable storage (MinIO):

1. Workspace files are sealed and uploaded.
2. If export fails, the pod is held and retried with exponential backoff.
3. After `maxWorkspaceSealDurationSeconds` (default: 300s), the gateway gives up and terminates the pod anyway.

This ensures session output is preserved for the client to download after the session ends.
