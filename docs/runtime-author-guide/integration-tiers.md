---
layout: default
title: "Integration Tiers"
parent: "Runtime Author Guide"
nav_order: 2
---

# Integration Tiers

Lenny defines three integration tiers for `type: agent` runtimes. Each tier builds on the previous one, adding capabilities while increasing implementation complexity. You choose the tier that matches your needs and can upgrade incrementally at any time.

---

## Feature Matrix

| Capability | Minimum | Standard | Full |
|-----------|---------|----------|------|
| **stdin/stdout binary protocol** | Yes | Yes | Yes |
| **Heartbeat / shutdown handling** | Yes | Yes | Yes |
| **Adapter-local tools** (`read_file`, `write_file`, `list_dir`, `delete_file`) | Yes | Yes | Yes |
| **Simplified response shorthand** (`{"type":"response","text":"..."}`) | Yes | Yes | Yes |
| **OutputPart minimal fields** (only `type` + `inline` required) | Yes | Yes | Yes |
| **MessageEnvelope minimal fields** (only `type`, `id`, `input` needed) | Yes | Yes | Yes |
| **Platform MCP server** (delegation, discovery, elicitation, output parts, memory, messaging) | -- | Yes | Yes |
| **Connector MCP servers** (GitHub, Jira, Slack, etc.) | -- | Yes | Yes |
| **Lifecycle channel** | -- | -- | Yes |
| **Checkpoint / restore** | None (pod failure loses context) | Best-effort (minor inconsistencies possible) | Cooperative handshake (consistent snapshots) |
| **Interrupt** | SIGTERM only (no safe stop point) | SIGTERM only | Clean `interrupt_request` / `interrupt_acknowledged` |
| **Credential rotation** | Pod restart (context lost if no checkpoint) | Pod restart (brief session pause) | In-place via lifecycle channel (zero interruption) |
| **Deadline / expiry warning** | `shutdown` only (no advance notice) | `shutdown` only | `deadline_approaching` signal before expiry |
| **Graceful drain (`DRAINING` state)** | `shutdown` + SIGTERM | `shutdown` + SIGTERM | Lifecycle-coordinated drain |
| **Task mode pod reuse** | No (effectively `maxTasksPerPod: 1`) | No | Yes (`task_complete` / `task_ready` handshake) |

---

## Minimum Tier

### What You Implement

1. **Read JSON Lines from stdin.** Parse each line as a JSON object. Dispatch on the `type` field.
2. **Handle `message`** by producing a `response` on stdout.
3. **Handle `heartbeat`** by immediately writing `{"type":"heartbeat_ack"}` to stdout.
4. **Handle `shutdown`** by finishing current work and exiting within `deadline_ms`.
5. **Ignore unknown types** for forward compatibility.
6. **Flush stdout** after every write.

### What the Platform Provides

- **Workspace files** at `/workspace/current/` --- your working directory.
- **Adapter-local tools** (`read_file`, `write_file`, `list_dir`, `delete_file`) via the `tool_call`/`tool_result` stdin/stdout exchange.
- **Process lifecycle management** --- the adapter starts your binary, delivers messages, and handles shutdown.

### What You Cannot Do

- No delegation (cannot spawn child tasks)
- No platform MCP tools (`lenny/output`, `lenny/request_input`, `lenny/discover_agents`, etc.)
- No connector access (GitHub, Jira, etc.)
- No inter-session messaging (`lenny/send_message`)
- No human input requests (`lenny/request_input`)
- No clean interrupt handling (only SIGTERM)
- No cooperative checkpointing (pod failure loses all in-flight context)
- No advance deadline warnings (only `shutdown` at expiry)
- No task-mode pod reuse

### Checkpoint Consistency at Minimum Tier

The adapter performs **no checkpoint** at Minimum tier. If the pod fails (eviction, OOM, node failure), all in-flight context is lost. The gateway restarts the session from the last gateway-persisted state, which may be significantly behind the runtime's actual progress. For idempotent or stateless workloads this is acceptable. For long-running tasks with intermediate state, consider Standard or Full tier.

### Implementation Effort

~50 lines of code in any language. See the [Echo Runtime Sample](echo-runtime.md) for a complete example.

---

## Standard Tier

### What You Add (on top of Minimum)

1. **Read the adapter manifest** at `/run/lenny/adapter-manifest.json`. Extract `platformMcpServer.socket`, `connectorServers`, and `mcpNonce`.
2. **Connect to the platform MCP server** via abstract Unix socket. Present the `mcpNonce` in the MCP `initialize` handshake.
3. **Optionally connect to connector MCP servers** (one per authorized connector).
4. **Use platform MCP tools** for delegation, output streaming, elicitation, memory, and messaging.

### MCP Integration Details

- **Transport:** Abstract Unix sockets exclusively (`@lenny-platform-mcp`, `@lenny-connector-github`, etc.). No stdio transport for intra-pod MCP.
- **Protocol version:** MCP 2025-03-26 (also accepts 2024-11-05 for backward compatibility).
- **Authentication:** Present the manifest nonce as `_lennyNonce` in the MCP `initialize` request's `params` object:

```json
{
  "method": "initialize",
  "params": {
    "_lennyNonce": "<nonce_hex_from_manifest>",
    "clientInfo": { "name": "my-runtime", "version": "1.0.0" },
    "protocolVersion": "2025-03-26"
  }
}
```

- **Tool discovery:** Call `tools/list` on each connected MCP server to discover available tools.
- **Client libraries:** Use an existing MCP client library for your language (`mcp-go` for Go, `@modelcontextprotocol/sdk` for TypeScript, `mcp` for Python).

### Platform MCP Tools Available

| Tool | Purpose |
|------|---------|
| `lenny/delegate_task` | Spawn a child session |
| `lenny/await_children` | Wait for children (streaming, unblocks on `input_required`) |
| `lenny/cancel_child` | Cancel a child and descendants |
| `lenny/discover_agents` | List available delegation targets |
| `lenny/output` | Emit output parts to parent/client |
| `lenny/request_elicitation` | Request human input |
| `lenny/memory_write` | Write to memory store |
| `lenny/memory_query` | Query memory store |
| `lenny/request_input` | Block until answer arrives |
| `lenny/send_message` | Send message to any task by taskId |
| `lenny/get_task_tree` | Return task hierarchy with states |

### Checkpoint Consistency at Standard Tier

The adapter performs **best-effort snapshots** without pausing the runtime. The workspace is snapshotted while your binary continues running, which means files written during the snapshot window may be in an inconsistent state. On resume, minor workspace inconsistencies are possible. For most workloads this is sufficient.

### macOS Note

Standard tier requires abstract Unix sockets (`@` prefix), which are a **Linux-only** feature. macOS developers must use `docker compose up` (Tier 2 local dev) to run Standard-tier runtimes. `make run` (Tier 1 local dev) supports macOS for Minimum-tier runtimes only.

### Implementation Effort

~150-200 lines of code plus an MCP client library dependency.

---

## Full Tier

### What You Add (on top of Standard)

1. **Open the lifecycle channel** by connecting to `manifest.lifecycleChannel.socket` (`@lenny-lifecycle`).
2. **Complete the capability handshake:** receive `lifecycle_capabilities`, reply with `lifecycle_support` declaring which capabilities you implement.
3. **Handle lifecycle signals** in a background goroutine/thread, concurrently with the main stdin loop.

### Lifecycle Capabilities

| Capability | What it enables |
|-----------|----------------|
| `checkpoint` | Cooperative `checkpoint_request` / `checkpoint_ready` / `checkpoint_complete` handshake |
| `interrupt` | Clean `interrupt_request` / `interrupt_acknowledged` for safe pause points |
| `credential_rotation` | In-place `credentials_rotated` / `credentials_acknowledged` with zero session interruption |
| `deadline_signal` | `deadline_approaching` advance warning before session expiry |
| `task_lifecycle` | `task_complete` / `task_complete_acknowledged` / `task_ready` for pod reuse in task mode |

You declare only the capabilities you implement. Undeclared capabilities fall back to the Standard-tier behavior (e.g., checkpoint falls back to best-effort, interrupt falls back to SIGTERM).

### Cooperative Checkpoint Flow

```
1. Adapter sends: {"type":"checkpoint_request","checkpointId":"chk_42","deadlineMs":60000}

2. Runtime:
   - Finishes current output write
   - Flushes all buffers
   - Ensures workspace files are in a consistent state
   - Does NOT exit or stop processing permanently

3. Runtime sends: {"type":"checkpoint_ready","checkpointId":"chk_42"}

4. Adapter snapshots the workspace filesystem.

5. Adapter sends: {"type":"checkpoint_complete","checkpointId":"chk_42","status":"ok"}

6. Runtime resumes normal operation.
```

If `checkpoint_ready` is not received within `deadlineMs` (default 60 seconds), the adapter falls back to best-effort snapshot and sets a `checkpointStuck` health flag. The runtime process is not killed --- it continues running, but the checkpoint may be inconsistent.

### Checkpoint Consistency at Full Tier

Cooperative checkpointing guarantees **consistent snapshots**. Because the runtime explicitly quiesces before signaling `checkpoint_ready`, the workspace filesystem is in a known-good state when the snapshot is taken. On resume after pod failure, the workspace is restored exactly as it was at the checkpoint, and the session state is replayed from that point.

### Implementation Effort

~300-400 lines of code including the lifecycle signal handler.

---

## Migration Path

### Minimum to Standard

1. Add a manifest reader. Parse `/run/lenny/adapter-manifest.json` at startup.
2. Add an MCP client library dependency to your project.
3. Connect to the platform MCP server using the socket path and nonce from the manifest.
4. Optionally connect to connector MCP servers.
5. Replace stdout-only output with `lenny/output` calls for incremental streaming (optional --- stdout `response` still works).
6. Add delegation logic if needed (`lenny/delegate_task`, `lenny/await_children`).

**No changes to the stdin/stdout protocol are needed.** The `message`/`response`/`heartbeat`/`shutdown` exchange is identical. Standard tier adds MCP on top.

### Standard to Full

1. Add a lifecycle channel connection. Connect to `@lenny-lifecycle` at startup.
2. Implement the capability handshake (`lifecycle_capabilities` / `lifecycle_support`).
3. Add a background goroutine/thread to read lifecycle messages concurrently with the stdin loop.
4. Implement handlers for the capabilities you need:
   - `checkpoint`: quiesce state, reply `checkpoint_ready`, wait for `checkpoint_complete`, resume.
   - `interrupt`: reach a safe stop point, reply `interrupt_acknowledged`.
   - `credential_rotation`: reload credentials from the new path, reply `credentials_acknowledged`.
   - `deadline_signal`: begin wrapping up long-running work.
   - `task_lifecycle`: release task resources on `task_complete`, prepare for new task on `task_ready`.

**No changes to the MCP integration are needed.** The platform MCP server tools work identically at Standard and Full tier.

---

## Credential Rotation by Tier

Credential rotation (when an LLM provider rate-limits or revokes a credential) behaves differently at each tier:

| Tier | Rotation method | Session impact |
|------|----------------|----------------|
| Full | Gateway calls `RotateCredentials` RPC; adapter sends `credentials_rotated` on lifecycle channel; runtime rebinds in-place. | No session interruption. |
| Standard | Gateway triggers Checkpoint, terminates pod, schedules replacement pod, assigns new credentials, resumes. | Brief pause; client sees a reconnect. |
| Minimum | Same as Standard. If checkpoint is not supported, in-flight context is lost. | Pause; potential context loss. |

The gateway selects the rotation strategy automatically based on the tier reported in the adapter's `lifecycle_support` handshake (Full) or the absence of a lifecycle channel (Standard/Minimum).
