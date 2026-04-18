---
layout: default
title: "Integration Levels"
parent: "Runtime Author Guide"
nav_order: 2
---

# Integration Levels

Lenny gives `type: agent` runtimes three levels of integration. Each level adds capabilities on top of the previous one. Start where the feature set matches what you need -- you can always move up later without rewriting what you have.

---

## At a glance

| Capability | Basic | Standard | Full |
|-----------|---------|----------|------|
| **stdin/stdout JSON-lines protocol** | Yes | Yes | Yes |
| **Heartbeat / shutdown handling** | Yes | Yes | Yes |
| **Built-in file tools** (`read_file`, `write_file`, `list_dir`, `delete_file`) | Yes | Yes | Yes |
| **Simple response shorthand** (`{"type":"response","text":"..."}`) | Yes | Yes | Yes |
| **Minimal output part fields** (only `type` + `inline` required) | Yes | Yes | Yes |
| **Minimal message fields** (only `type`, `id`, `input` needed) | Yes | Yes | Yes |
| **Platform tool server** (delegation, discovery, user input, streaming output, memory, messaging) | -- | Yes | Yes |
| **Connector tool servers** (GitHub, Jira, Slack, etc.) | -- | Yes | Yes |
| **Lifecycle channel** | -- | -- | Yes |
| **Checkpoint and restore** | None -- pod failure loses context | Best-effort -- minor inconsistencies possible | Cooperative handshake -- consistent snapshots |
| **Interrupt** | SIGTERM only, no safe stop point | SIGTERM only | Clean `interrupt_request` / `interrupt_acknowledged` |
| **Credential rotation** | Pod restart, context lost unless checkpointed | Pod restart, brief pause | Rotated in place, no interruption |
| **Advance deadline warning** | `shutdown` only, no advance notice | `shutdown` only | `deadline_approaching` signal before expiry |
| **Graceful drain** | `shutdown` + SIGTERM | `shutdown` + SIGTERM | Coordinated via the lifecycle channel |
| **Pod reuse in task mode** | No (effectively `maxTasksPerPod: 1`) | No | Yes, via `task_complete` / `task_ready` handshake |

---

## Basic

### What you implement

1. **Read one JSON object per line from stdin.** Dispatch on the `type` field.
2. **Handle `message`** by writing a `response` back on stdout.
3. **Handle `heartbeat`** by writing `{"type":"heartbeat_ack"}` right away.
4. **Handle `shutdown`** by wrapping up and exiting within `deadline_ms`.
5. **Ignore unknown types** so the platform can add things later without breaking you.
6. **Flush stdout** after every write.

### What the platform gives you

- **Workspace files** at `/workspace/current/` -- that's your working directory.
- **Built-in file tools** (`read_file`, `write_file`, `list_dir`, `delete_file`) through the `tool_call` / `tool_result` stdin/stdout exchange.
- **Process lifecycle management** -- the sidecar starts your binary, delivers messages, and coordinates shutdown.

### What's off the table at this level

- No delegation to other runtimes.
- No platform tools like asking the user a question or streaming incremental output.
- No connector access (GitHub, Jira, and so on).
- No inter-session messaging.
- No clean interrupt handling -- you only get SIGTERM.
- No cooperative checkpointing -- a pod failure loses everything in flight.
- No advance deadline warnings -- you just get `shutdown` when time's up.
- No pod reuse across tasks.

### What happens if the pod dies

At the Basic level, there's no checkpoint. If the pod is evicted, OOM-killed, or the node fails, everything in flight is lost. Lenny restarts the session from the last state it had persisted, which may be well behind where the runtime actually was. That's fine for stateless workers; for anything with meaningful intermediate state, move to Standard or Full.

### How much code

About 50 lines in any language. See the [Echo Runtime Sample](echo-runtime.md) for a complete, runnable example.

---

## Standard

### What you add on top of Basic

1. **Read the manifest** the sidecar writes to `/run/lenny/adapter-manifest.json` at startup. Extract `platformMcpServer.socket`, `connectorServers`, and `mcpNonce`.
2. **Connect to the platform tool server** over the Unix socket named in the manifest. Present the `mcpNonce` in the MCP `initialize` handshake.
3. **Optionally connect to connector tool servers** -- one per connector the operator has authorized for your tenant.
4. **Use the platform tools** for delegation, streaming output, asking the user questions, memory, and messaging.

### About the connection

- **Transport:** Linux abstract Unix sockets (`@lenny-platform-mcp`, `@lenny-connector-github`, etc.). Inside the pod, this is how you reach every local tool server.
- **Protocol:** MCP 2025-03-26 (the gateway also accepts 2024-11-05 for a transition window).
- **Authentication:** send the manifest nonce as `_lennyNonce` in the MCP `initialize` request:

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

- **Tool discovery:** once connected, call `tools/list` on each server to see what's available.
- **Client libraries:** use whichever MCP client library your language already has -- `mcp-go` for Go, `@modelcontextprotocol/sdk` for TypeScript, `mcp` for Python.

### The platform tools available at this level

| Tool | What it does |
|------|--------------|
| `lenny/delegate_task` | Spawn a child session on another runtime |
| `lenny/await_children` | Wait for child sessions to finish (streaming, unblocks when a child needs input) |
| `lenny/cancel_child` | Cancel a child and everything below it |
| `lenny/discover_agents` | List the runtimes you're allowed to delegate to |
| `lenny/output` | Stream output parts back to the parent or the client |
| `lenny/request_elicitation` | Ask the human a question |
| `lenny/memory_write` | Write to the persistent memory store |
| `lenny/memory_query` | Query the memory store |
| `lenny/request_input` | Block until the user answers |
| `lenny/send_message` | Send a message to any task by task ID |
| `lenny/get_task_tree` | Get the task hierarchy with current states |

### What happens if the pod dies

At Standard, Lenny takes **best-effort snapshots** without pausing your runtime. The workspace is tarred up while your binary is still running, so files written during the snapshot can end up in an inconsistent state. On resume, you may see minor workspace drift. For most workloads that's fine.

### A note on macOS

Standard and Full use Linux abstract Unix sockets (names that start with `@`), which only exist on Linux. If you're developing on macOS, run your runtime inside `docker compose up` -- that gives you a Linux environment. `make run` works on macOS for Basic-level runtimes, since those only use stdin/stdout.

### How much code

Somewhere around 150-200 lines, plus an MCP client library.

---

## Full

### What you add on top of Standard

1. **Open the lifecycle channel** by connecting to the socket named in `manifest.lifecycleChannel.socket` (usually `@lenny-lifecycle`).
2. **Do the capability handshake:** you'll receive `lifecycle_capabilities` from the sidecar; reply with `lifecycle_support` naming which of them you actually implement.
3. **Handle lifecycle signals** in a background goroutine or thread, running alongside the main stdin loop.

### The lifecycle capabilities

| Capability | What it enables |
|-----------|----------------|
| `checkpoint` | Cooperative `checkpoint_request` / `checkpoint_ready` / `checkpoint_complete` handshake |
| `interrupt` | Clean `interrupt_request` / `interrupt_acknowledged` so the runtime stops at a safe point |
| `credential_rotation` | In-place `credentials_rotated` / `credentials_acknowledged` -- the session keeps going |
| `deadline_signal` | `deadline_approaching` so the runtime can wrap up before it's terminated |
| `task_lifecycle` | `task_complete` / `task_complete_acknowledged` / `task_ready` for pod reuse in task-mode pools |

Declare only the capabilities you implement. Anything you don't declare falls back to Standard-level behavior -- an unimplemented checkpoint becomes best-effort, an unimplemented interrupt becomes SIGTERM.

### What a cooperative checkpoint looks like

```
1. Sidecar sends:
   {"type":"checkpoint_request","checkpointId":"chk_42","deadlineMs":60000}

2. Your runtime:
   - Finishes the current output write
   - Flushes its buffers
   - Makes sure the workspace files are in a consistent state
   - Does NOT exit or stop processing permanently

3. Your runtime sends:
   {"type":"checkpoint_ready","checkpointId":"chk_42"}

4. Sidecar snapshots the workspace filesystem.

5. Sidecar sends:
   {"type":"checkpoint_complete","checkpointId":"chk_42","status":"ok"}

6. Your runtime resumes normal operation.
```

If `checkpoint_ready` doesn't arrive within `deadlineMs` (default 60 seconds), the sidecar falls back to a best-effort snapshot and flags `checkpointStuck` for the operator to see. Your runtime keeps running either way -- it's never killed for a stuck checkpoint -- but the snapshot may not be consistent.

### What happens if the pod dies

Cooperative checkpointing guarantees consistent snapshots. Because your runtime has explicitly quiesced before it says `checkpoint_ready`, the workspace is in a known-good state when the snapshot is taken. When the session resumes on a new pod, the workspace comes back exactly as it was and the session continues from that point.

### How much code

About 300-400 lines, including the background handler for lifecycle signals.

---

## Moving up

### From Basic to Standard

1. Read `/run/lenny/adapter-manifest.json` at startup.
2. Add an MCP client library to your project.
3. Connect to the platform tool server using the socket and nonce from the manifest.
4. Connect to any connector tool servers you want to use.
5. Optionally switch stdout `response` messages to `lenny/output` for incremental streaming -- plain stdout still works.
6. Add delegation if you need it (`lenny/delegate_task`, `lenny/await_children`).

The stdin/stdout contract doesn't change. `message` / `response` / `heartbeat` / `shutdown` keep working the same way. Standard just layers MCP on top.

### From Standard to Full

1. Connect to the lifecycle channel (`@lenny-lifecycle`) at startup.
2. Do the capability handshake (`lifecycle_capabilities` / `lifecycle_support`), declaring only what you implement.
3. Read lifecycle messages in a background goroutine or thread, alongside the stdin loop.
4. Implement the handlers you need:
   - `checkpoint`: quiesce, reply `checkpoint_ready`, wait for `checkpoint_complete`, resume.
   - `interrupt`: reach a safe stop point, reply `interrupt_acknowledged`.
   - `credential_rotation`: reload credentials from the new path, reply `credentials_acknowledged`.
   - `deadline_signal`: start wrapping up long-running work.
   - `task_lifecycle`: release task resources on `task_complete`, prepare for the next one on `task_ready`.

MCP doesn't change between Standard and Full -- the platform tool server behaves the same way.

---

## How credential rotation differs

When an LLM provider rate-limits or revokes a credential, Lenny rotates it. What that looks like depends on your integration level:

| Level | How rotation happens | What the session sees |
|-------|---------------------|----------------------|
| Full | The platform sends `credentials_rotated` on the lifecycle channel; your runtime rebinds in place. | Nothing -- the session keeps running. |
| Standard | The platform checkpoints, terminates the pod, allocates a new one, assigns new credentials, and resumes. | A brief pause; the client sees a reconnect. |
| Basic | Same as Standard, but if there's no checkpoint, the in-flight context is lost. | A pause, and potentially some lost context. |

The platform picks the right strategy automatically -- based on the capabilities you declared in `lifecycle_support`, or the absence of a lifecycle channel.

---

## `type: mcp` runtimes

Integration levels only apply to `type: agent` runtimes. Lenny also supports `type: mcp` runtimes, and they work differently.

A `type: mcp` runtime hosts an MCP server behind Lenny's infrastructure. You get the same operational benefits -- pod isolation, credential management, pool scaling, egress control, audit -- but your server itself doesn't need to know anything about Lenny. There's no task lifecycle, no stdin/stdout contract, and no integration levels.

Your server just needs to speak MCP. It doesn't receive `message`, `heartbeat`, or `shutdown` -- Lenny manages the pod lifecycle from the outside.

Each `type: mcp` runtime gets its own endpoint on the gateway at `/mcp/runtimes/{runtime-name}`. Clients connect directly over standard MCP; Lenny creates a session record per connection for audit and billing.

How it differs from `type: agent`:

- **No task lifecycle.** Your server doesn't participate in sessions, delegation, checkpointing, or interrupt handling.
- **No integration levels.** The Basic / Standard / Full model doesn't apply.
- **Not delegatable.** `type: mcp` runtimes don't show up in `lenny/discover_agents` and can't be the target of `lenny/delegate_task`.
- **No `capabilities` field.** The runtime registration omits the `capabilities` block entirely.
