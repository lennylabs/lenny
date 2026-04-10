---
layout: default
title: "Adapter Contract"
parent: "Runtime Author Guide"
nav_order: 1
---

# Adapter Contract

This page is the complete reference for the protocol between the Lenny adapter sidecar and your runtime binary. It covers the sidecar architecture, the gRPC control protocol (gateway to adapter), the stdin/stdout JSON Lines protocol (adapter to your binary), and the lifecycle channel (Full tier only).

---

## Sidecar Architecture

Every Lenny agent pod contains two containers:

1. **Adapter container** (Lenny-managed) --- handles all platform communication: gRPC to the gateway, mTLS certificate management, workspace staging, credential injection, health checks, and MCP server hosting.
2. **Agent container** (your runtime binary) --- your code. Communicates with the adapter exclusively via stdin/stdout (all tiers) and optionally via local Unix sockets (Standard/Full tier).

```
┌─────────────────────────────────────────────────────────────────────┐
│  Lenny Pod                                                          │
│                                                                     │
│  ┌───────────────────────┐          ┌────────────────────────────┐  │
│  │  Adapter Container    │  stdin → │  Agent Container           │  │
│  │                       │  ← stdout│  (your runtime binary)     │  │
│  │  - gRPC to gateway    │          │                            │  │
│  │  - MCP servers (Unix) │  socket  │  - reads JSON from stdin   │  │
│  │  - lifecycle channel  │  ──────→ │  - writes JSON to stdout   │  │
│  │  - file staging       │          │  - optionally connects to  │  │
│  │  - health checks      │          │    MCP servers + lifecycle  │  │
│  │  - credential mgmt    │          │    channel via Unix sockets │  │
│  └───────────┬───────────┘          └────────────────────────────┘  │
│              │ gRPC (mTLS)                                          │
└──────────────┼──────────────────────────────────────────────────────┘
               │
         Lenny Gateway
```

The adapter writes configuration to `/run/lenny/adapter-manifest.json` before spawning your binary. This manifest tells your runtime where to find MCP servers, what session it is part of, and what capabilities are available. Minimum-tier runtimes do not need to read the manifest at all --- the four built-in adapter-local tools (`read_file`, `write_file`, `list_dir`, `delete_file`) are a fixed contract.

---

## gRPC Control Protocol (Gateway to Adapter)

These RPCs are between the gateway and the adapter. Your runtime binary never sees them directly, but understanding them helps you reason about the pod lifecycle.

| RPC | Description |
|-----|-------------|
| `PrepareWorkspace` | Accept streamed files into the staging area (`/workspace/staging`) |
| `FinalizeWorkspace` | Validate staging, materialize to `/workspace/current` |
| `RunSetup` | Execute bounded setup commands (deployer-defined) |
| `StartSession` | Start the agent runtime with `cwd=/workspace/current` (pod-warm mode) |
| `ConfigureWorkspace` | Point a pre-connected session at the finalized `cwd` (SDK-warm mode). Timeout: 10s. |
| `DemoteSDK` | Tear down the pre-connected SDK process and return the pod to pod-warm state |
| `Attach` | Connect client stream to running session |
| `Interrupt` | Interrupt current agent work |
| `Checkpoint` | Export recoverable session state |
| `ExportPaths` | Package files for delegation, rebased per export spec |
| `AssignCredentials` | Push per-provider credential map to the runtime before session start |
| `RotateCredentials` | Push replacement credentials for a specific provider mid-session |
| `Resume` | Restore from checkpoint on a replacement pod |
| `ReportUsage` | Report LLM token counts extracted from provider responses |
| `Terminate` | Graceful shutdown |

**Checkpoint and Interrupt are mutually exclusive.** The adapter maintains a per-session operation lock. Only one of these operations may execute at a time; the other is queued until the first completes.

**Runtime-to-Gateway events** (sent over the gRPC control channel):

| Event | Description |
|-------|-------------|
| `RATE_LIMITED` | Current credential is rate-limited; request fallback |
| `AUTH_EXPIRED` | Credential lease expired or was rejected by provider |
| `PROVIDER_UNAVAILABLE` | Provider endpoint is unreachable |
| `LEASE_REJECTED` | Runtime cannot use the assigned credential |

---

## stdin/stdout JSON Lines Protocol

This is the primary protocol your runtime implements. Every message is a single JSON object terminated by `\n`. Your binary reads from stdin and writes to stdout.

**Critical: stdout flushing.** Every JSON Lines message written to stdout MUST be followed by a flush before your binary blocks on the next `read_line(stdin)`. Many language runtimes buffer stdout by default. Without an explicit flush, the adapter never receives the message and the session hangs silently.

| Language | Required action |
|----------|-----------------|
| Go | Write directly to `os.Stdout` (unbuffered by default), or use `bufio.NewWriter` with explicit `Flush()` |
| Python | `sys.stdout.flush()` after each `print()`, or set `sys.stdout = io.TextIOWrapper(sys.stdout.buffer, line_buffering=True)` |
| Node.js | Use `process.stdout.write(line + "\n")` |
| Ruby | `$stdout.sync = true` at startup |
| Java | `new PrintStream(System.out, true)` for `autoFlush` |
| Rust | Call `stdout.flush()` from `std::io::Write` after each write |
| C/C++ | `fflush(stdout)` after each write, or `setbuf(stdout, NULL)` at startup |

**stderr** is captured by the adapter for logging and diagnostics but is **not** parsed as protocol messages. Use stderr freely for debug output.

---

### Inbound Messages (adapter writes to your stdin)

#### `message` --- All Content Delivery

The unified message type for all inbound content: initial task, mid-session injection, reply to `request_input`, and sibling notification.

```json
{
  "type": "message",
  "id": "msg_001",
  "input": [
    { "type": "text", "inline": "Summarize the files in /workspace/current" }
  ],
  "from": { "kind": "client", "id": "client_8f3a2b" },
  "inReplyTo": null,
  "threadId": "t_01",
  "delivery": "queued",
  "delegationDepth": 0,
  "slotId": null
}
```

**Field reference:**

| Field | Type | Tier | Description |
|-------|------|------|-------------|
| `type` | string | All | Always `"message"` |
| `id` | string | All | Unique message identifier (gateway-assigned ULID, `msg_` prefix) |
| `input` | OutputPart[] | All | Array of content parts. See OutputPart format below. |
| `from` | object | Standard+ | Sender identity. `kind`: `"client"`, `"agent"`, `"system"`, or `"external"`. Adapter-injected; never set by your runtime. |
| `inReplyTo` | string or null | Standard+ | If set, matches an outstanding `lenny/request_input` call on the target |
| `threadId` | string or null | Standard+ | Thread label. One implicit thread per session in v1. |
| `delivery` | string or null | Standard+ | `"immediate"` or `"queued"` (default). Controls interrupt behavior. |
| `delegationDepth` | integer | Standard+ | How many tree hops this message crossed. Informational. |
| `slotId` | string or null | Concurrent | Present only in concurrent-workspace mode. Identifies the slot. |

**Minimum-tier runtimes:** Read only `type`, `id`, and `input`. Ignore all other fields safely.

**The `input` array contains OutputPart objects.** The simplest OutputPart is:

```json
{ "type": "text", "inline": "Hello, world!" }
```

Only `type` and `inline` are required. All other OutputPart fields (`schemaVersion`, `id`, `mimeType`, `ref`, `annotations`, `parts`, `status`) are optional with sensible defaults.

#### `tool_result` --- Result of an Agent-Requested Tool Call

Delivered when a tool call you emitted has been executed by the adapter.

```json
{
  "type": "tool_result",
  "id": "tc_001",
  "content": [
    { "type": "text", "inline": "file contents here" }
  ],
  "isError": false,
  "slotId": null
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"tool_result"` |
| `id` | string | Matches the `id` of the `tool_call` this result responds to |
| `content` | OutputPart[] | Result content |
| `isError` | boolean | `true` if tool execution failed. Default `false`. |
| `slotId` | string or null | Present only in concurrent-workspace mode |

**Correlation:** Every `tool_result.id` matches a previously emitted `tool_call.id`. Results may arrive in any order when you have multiple outstanding tool calls. Other inbound messages (`heartbeat`, additional `message` content) may arrive before the `tool_result` --- your runtime must handle interleaved delivery.

#### `heartbeat` --- Liveness Ping

```json
{ "type": "heartbeat", "ts": 1717430400 }
```

Your runtime MUST respond with a `heartbeat_ack` within 10 seconds. If no ack is received, the adapter considers the process hung and sends SIGTERM.

#### `shutdown` --- Graceful Termination

```json
{ "type": "shutdown", "reason": "drain", "deadline_ms": 10000 }
```

Your runtime must finish current work and exit within `deadline_ms`. No acknowledgment message is required --- the adapter watches for process exit. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds.

| `reason` values | Description |
|------------------|-------------|
| `"drain"` | Pod is being drained (node maintenance, pool scaling) |
| `"session_complete"` | Session has completed normally |
| `"budget_exhausted"` | Token budget exhausted |
| `"eviction"` | Pod eviction |
| `"operator"` | Manual operator action |

---

### Outbound Messages (your runtime writes to stdout)

#### `response` --- Task Output

The primary output message. Signals task completion.

```json
{
  "type": "response",
  "output": [
    { "type": "text", "inline": "The answer is 42." }
  ],
  "slotId": null
}
```

**Simplified shorthand** (Minimum tier convenience --- adapter normalizes to the full form):

```json
{ "type": "response", "text": "The answer is 42." }
```

**Error reporting via `response`.** Include an optional `error` field for structured error reporting:

```json
{
  "type": "response",
  "output": [
    { "type": "text", "inline": "Partial results before failure..." }
  ],
  "error": {
    "code": "LLM_CONTEXT_OVERFLOW",
    "message": "Input exceeded model context window"
  }
}
```

When `error` is present, the adapter maps the task to `failed` state. When `error` is absent and the process exits with code 0, the task completes successfully. When the process exits non-zero without emitting a `response`, the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr.

**Relationship with `lenny/output`:** At Standard/Full tier, you may emit output parts incrementally via the `lenny/output` platform MCP tool. The stdout `response` message is always required to signal task completion, regardless of whether `lenny/output` was used. Its `output` array contains only parts not already emitted via `lenny/output`. If you emitted all output via `lenny/output`, send an empty array: `{"type": "response", "output": []}`.

#### `tool_call` --- Request Tool Execution

Request the adapter to execute a tool. At Minimum tier, only adapter-local tools are available (`read_file`, `write_file`, `list_dir`, `delete_file`). At Standard/Full tier, platform MCP tools are accessed via the MCP client connection, not via `tool_call`.

```json
{
  "type": "tool_call",
  "id": "tc_001",
  "name": "read_file",
  "arguments": { "path": "/workspace/current/README.md" },
  "slotId": null
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"tool_call"` |
| `id` | string | Unique call identifier. Used to correlate the inbound `tool_result`. Recommended format: `tc_` prefix with monotonic counter or random suffix. |
| `name` | string | Tool name (e.g., `read_file`, `write_file`) |
| `arguments` | object | Tool-specific parameters |
| `slotId` | string or null | Present only in concurrent-workspace mode |

**Built-in adapter-local tools:**

| Tool | Description | Arguments |
|------|-------------|-----------|
| `read_file` | Read file contents | `{"path": "..."}` |
| `write_file` | Create or overwrite a file | `{"path": "...", "content": "..."}` |
| `list_dir` | List directory entries | `{"path": "..."}` |
| `delete_file` | Delete a file or empty directory | `{"path": "..."}` |

All paths are confined to `/workspace`. The adapter rejects any path resolving outside `/workspace` with `isError: true` and `content[0].inline` set to `"path_outside_workspace"`.

#### `heartbeat_ack` --- Heartbeat Response

```json
{ "type": "heartbeat_ack" }
```

Must be sent in response to every inbound `heartbeat`. No other fields.

#### `status` --- Optional Status Update

```json
{ "type": "status", "state": "thinking", "message": "Analyzing code..." }
```

Informational. The adapter forwards status updates to the gateway for client visibility. Not required for any tier.

---

### Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Normal completion --- session ended cleanly or shutdown honored |
| 1 | Runtime error --- adapter logs stderr and reports failure to gateway |
| 2 | Protocol error --- agent could not parse inbound messages |
| 137 | SIGKILL (set by OS) --- adapter treats as crash, pod is not reused |

---

## Version Negotiation

The adapter advertises its protocol version to the gateway at startup. During the `INIT` state, the adapter sends an `AdapterInit` message on the gRPC control stream with `adapterProtocolVersion` (semver string, e.g., `"1.0.0"`). The gateway responds with `AdapterInitAck` carrying `selectedVersion` or closes the stream with `PROTOCOL_VERSION_INCOMPATIBLE` if no compatible version exists.

Major version changes are breaking; minor/patch are backwards compatible. Current protocol version: `"1.0.0"`.

For your runtime binary, version negotiation is transparent --- the adapter handles it. Your binary receives messages in the format documented on this page regardless of the adapter protocol version.

---

## Health Check Contract

The adapter implements the **gRPC Health Checking Protocol** on behalf of your runtime. The gateway uses this to determine pod liveness and readiness.

At Minimum tier, the adapter reports health based on process liveness (is your binary still running?) and heartbeat responsiveness (did it ack within 10 seconds?).

At Standard/Full tier, you can optionally expose an HTTP health check endpoint (e.g., `/healthz` on a local port) that the adapter will incorporate into its health reporting. This is documented in the SDK examples.

---

## Adapter Manifest

The adapter writes `/run/lenny/adapter-manifest.json` before spawning your binary. The manifest is read-only to the agent container, complete and authoritative when your binary starts, and regenerated per task execution in task mode.

```json
{
  "version": 1,
  "platformMcpServer": { "socket": "@lenny-platform-mcp" },
  "lifecycleChannel": { "socket": "@lenny-lifecycle" },
  "connectorServers": [
    { "id": "github", "socket": "@lenny-connector-github" }
  ],
  "runtimeMcpServers": [],
  "adapterLocalTools": [
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
    }
  ],
  "sessionId": "sess_abc",
  "taskId": "task_root",
  "mcpNonce": "a3f1...c7e2",
  "observability": {
    "otlpEndpoint": "http://otel-collector.lenny-system:4317"
  }
}
```

**Tier reading requirements:**

| Tier | What to read |
|------|-------------|
| Minimum | Not required for core operation. The four built-in tools are a fixed contract. Optionally read `adapterLocalTools` to discover custom adapter-local tools. |
| Standard | `platformMcpServer.socket`, `connectorServers`, `mcpNonce` --- to connect to and authenticate with local MCP servers. |
| Full | Standard fields plus `lifecycleChannel.socket`. |

**Forward compatibility:** Your runtime must silently ignore unknown top-level fields. The adapter may add new fields in future versions without incrementing `version`. A `version` increment indicates a breaking change to existing field semantics.

---

## Lifecycle Channel (Full Tier Only)

The lifecycle channel is a bidirectional JSON Lines stream over an abstract Unix socket (`@lenny-lifecycle`). The runtime connects as a client; the adapter listens. Each message is a single JSON object terminated by `\n`.

Opening the lifecycle channel is optional. Runtimes that do not open it operate in fallback-only mode (Minimum/Standard tier behavior).

### Capability Negotiation

On connection, the adapter sends `lifecycle_capabilities` first. The runtime replies with `lifecycle_support` declaring which capabilities it supports (a subset of what the adapter offered).

**Adapter sends:**
```json
{
  "type": "lifecycle_capabilities",
  "protocolVersion": "1.0",
  "capabilities": ["checkpoint", "interrupt", "credential_rotation", "deadline_signal", "task_lifecycle"]
}
```

**Runtime replies:**
```json
{
  "type": "lifecycle_support",
  "capabilities": ["checkpoint", "interrupt", "deadline_signal"]
}
```

### Lifecycle Messages Reference

#### Adapter to Runtime

| Message Type | Fields | Description |
|-------------|--------|-------------|
| `lifecycle_capabilities` | `protocolVersion`, `capabilities[]` | First message on channel open. |
| `checkpoint_request` | `checkpointId`, `deadlineMs` | Quiesce and signal readiness. Reply with `checkpoint_ready` within `deadlineMs`. |
| `checkpoint_complete` | `checkpointId`, `status` (`"ok"` or `"failed"`), `reason` | Snapshot upload result; runtime may resume. |
| `interrupt_request` | `interruptId`, `deadlineMs` | Reach a safe stop point within `deadlineMs`. If no `interrupt_acknowledged` within deadline, adapter forces suspended anyway. |
| `credentials_rotated` | `provider`, `credentialsPath`, `leaseId` | New credentials written; rebind and reply with `credentials_acknowledged`. |
| `terminate` | `deadlineMs`, `reason` | Graceful shutdown. Exit within `deadlineMs`; SIGTERM on timeout. Always means process exit. |
| `task_complete` | `taskId` | Between-task signal in task mode. Release task resources and reply with `task_complete_acknowledged`. |
| `task_ready` | `taskId` | Scrub complete, new workspace materialized. Re-read adapter manifest and prepare for next `message` on stdin. |
| `deadline_approaching` | `remainingMs`, `trigger` | Advance warning before forced termination. `trigger`: `"session_age"`, `"budget"`, or `"idle"`. |

#### Runtime to Adapter

| Message Type | Fields | Description |
|-------------|--------|-------------|
| `lifecycle_support` | `capabilities[]` | Capability handshake reply. |
| `checkpoint_ready` | `checkpointId` | Runtime has quiesced and is ready for snapshot. |
| `interrupt_acknowledged` | `interruptId` | Runtime has reached a safe stop point. |
| `credentials_acknowledged` | `leaseId`, `provider` | Runtime has rebound to the new credential. |
| `llm_request_started` | `requestId`, `provider` | Runtime is about to send an outbound LLM request (direct mode only). |
| `llm_request_completed` | `requestId`, `provider`, `status` | Runtime's outbound LLM request completed. |
| `task_complete_acknowledged` | `taskId` | Runtime has released task-specific resources. |

Unknown messages must be silently ignored on both sides for forward compatibility.

---

## Wire Format Examples

### Complete Minimum-Tier Session Trace

```
1. Adapter starts agent binary, stdin/stdout pipes open.

2. Adapter writes to stdin:
   {"type":"message","id":"msg_001","input":[{"type":"text","inline":"Hello"}],"from":{"kind":"client","id":"client_8f3a2b"},"threadId":"t_01"}

3. Agent reads line from stdin, parses JSON, reads type/id/input (ignores other fields).

4. Agent writes to stdout:
   {"type":"response","text":"Echo: Hello"}

5. Adapter reads line from stdout, delivers response to gateway.

6. [Heartbeat interval] Adapter writes:
   {"type":"heartbeat","ts":1717430410}

7. Agent writes:
   {"type":"heartbeat_ack"}

8. Gateway initiates shutdown. Adapter writes:
   {"type":"shutdown","reason":"drain","deadline_ms":10000}

9. Agent finishes, exits with code 0.

10. Adapter reports clean termination to gateway.
```

### Tool Call and Result

```
Agent writes to stdout:
{"type":"tool_call","id":"tc_001","name":"read_file","arguments":{"path":"/workspace/current/README.md"}}

Adapter reads file and writes to stdin:
{"type":"tool_result","id":"tc_001","content":[{"type":"text","inline":"# My Project\nThis is a sample project."}],"isError":false}
```

### Multiple Outstanding Tool Calls

```
Agent writes:
{"type":"tool_call","id":"tc_001","name":"read_file","arguments":{"path":"src/main.go"}}
{"type":"tool_call","id":"tc_002","name":"read_file","arguments":{"path":"go.mod"}}

Adapter may respond in any order:
{"type":"tool_result","id":"tc_002","content":[{"type":"text","inline":"module example.com/myapp\ngo 1.22"}],"isError":false}

A heartbeat may arrive between tool results:
{"type":"heartbeat","ts":1717430420}

Agent acks immediately:
{"type":"heartbeat_ack"}

Then the other result arrives:
{"type":"tool_result","id":"tc_001","content":[{"type":"text","inline":"package main\n..."}],"isError":false}
```

### Full-Tier Checkpoint Handshake

```
Adapter sends on lifecycle channel:
{"type":"checkpoint_request","checkpointId":"chk_42","deadlineMs":60000}

Runtime quiesces (flushes buffers, stops writing to workspace), then:
{"type":"checkpoint_ready","checkpointId":"chk_42"}

Adapter snapshots the workspace and sends:
{"type":"checkpoint_complete","checkpointId":"chk_42","status":"ok"}

Runtime resumes normal operation.
```

### Full-Tier Interrupt Handshake

```
Adapter sends on lifecycle channel:
{"type":"interrupt_request","interruptId":"int_7","deadlineMs":30000}

Runtime reaches a safe stop point (e.g., finishes current LLM call), then:
{"type":"interrupt_acknowledged","interruptId":"int_7"}

Session transitions to suspended state. Pod is held.
```
