---
layout: default
title: "Adapter Contract"
parent: "Runtime Author Guide"
nav_order: 1
---

# Adapter Contract

This page is the complete reference for the protocol between the Lenny adapter sidecar and your runtime binary. It covers the sidecar architecture, the gRPC control protocol (gateway to adapter), the stdin/stdout JSON Lines protocol (adapter to your binary), and the lifecycle channel (Full level only).

---

## Sidecar Architecture

Every Lenny agent pod contains two containers:

1. **Adapter container** (Lenny-managed) --- handles all platform communication: gRPC to the gateway, mTLS certificate management, workspace staging, credential injection, health checks, and MCP server hosting.
2. **Agent container** (your runtime binary) --- your code. Communicates with the adapter exclusively via stdin/stdout (at every integration level) and optionally via local Unix sockets (Standard and Full levels).

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

The adapter writes configuration to `/run/lenny/adapter-manifest.json` before spawning your binary. This manifest tells your runtime where to find MCP servers, what session it is part of, and what capabilities are available. Basic-level runtimes do not need to read the manifest at all --- the four built-in adapter-local tools (`read_file`, `write_file`, `list_dir`, `delete_file`) are a fixed contract.

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

| Field | Type | Level | Description |
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

**Basic-level runtimes:** Read only `type`, `id`, and `input`. Ignore all other fields safely.

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

**Simplified shorthand** (Basic-level convenience --- adapter normalizes to the full form):

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

**Relationship with `lenny/output`:** At the Standard and Full levels, you may emit output parts incrementally via the `lenny/output` platform tool. The stdout `response` message is always required to signal task completion, regardless of whether `lenny/output` was used. Its `output` array contains only parts not already emitted via `lenny/output`. If you emitted all output via `lenny/output`, send an empty array: `{"type": "response", "output": []}`.

#### `tool_call` --- Request Tool Execution

Request the adapter to execute a tool. At the Basic level, only adapter-local tools are available (`read_file`, `write_file`, `list_dir`, `delete_file`). At the Standard and Full levels, platform tools are accessed via the MCP client connection, not via `tool_call`.

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

Informational. The adapter forwards status updates to the gateway for client visibility. Not required at any integration level.

#### `set_tracing_context` --- Propagate Tracing Identifiers

```json
{ "type": "set_tracing_context", "context": { "langsmith_run_id": "run_abc123" } }
```

Registers tracing identifiers that the adapter attaches to all subsequent `lenny/delegate_task` gRPC requests. Available at all levels. Validation rules are enforced by the gateway when the delegation request arrives.

---

## OutputPart Reference

`OutputPart` is Lenny's internal content model -- the unit of content in inbound `message.input[]`, outbound `response.output[]`, `tool_result.content[]`, and platform-tool `lenny/output` payloads. The gateway translates to and from external protocol shapes (MCP content blocks, OpenAI content, A2A parts) at the edge; your runtime always produces and consumes `OutputPart` directly.

The minimal valid part is `{"type": "text", "inline": "hello"}`. All other fields are optional.

### Envelope

| Field | Type | Default | Purpose |
|:------|:-----|:--------|:--------|
| `type` | string | required | A registered type name (see registry below) or a custom `x-<vendor>/<typeName>` |
| `inline` | string | -- | Literal payload (UTF-8 text or base64-encoded binary). Mutually exclusive with `ref`. |
| `ref` | string | -- | `lenny-blob://` URI pointing to gateway-staged bytes. Mutually exclusive with `inline`. |
| `mimeType` | string | type-specific | MIME type of the payload. Defaults to `text/plain` for `type: "text"`. |
| `id` | string | adapter-generated | Stable part identifier; enables per-part streaming correlation. |
| `schemaVersion` | integer | `1` | Envelope schema revision; bump only when emitting fields added in a later registry version. |
| `annotations` | object | -- | Open metadata map (`language`, `role`, `final`, `audience`, etc.). |
| `parts` | OutputPart[] | -- | Nested parts for compound outputs (e.g., `execution_result`). |
| `status` | string | `complete` | `streaming` / `complete` / `failed` -- primarily for incremental delivery via `lenny/output`. |

**`inline` vs `ref` -- size policy.** The gateway chooses a representation based on payload size; your runtime may emit either form and let the gateway promote it:

| Size | Representation | Consumer sees |
|:-----|:---------------|:--------------|
| ≤ 64 KB | `inline` (UTF-8 or base64) | `inline` populated, `ref` absent |
| > 64 KB and ≤ 50 MB | Staged to blob store; `ref` set to a `lenny-blob://` URI | `ref` populated, `inline` absent |
| > 50 MB | Rejected at ingress | `413 OUTPUTPART_TOO_LARGE` |

Setting both `inline` and `ref` on the same part is a validation error (`400 OUTPUTPART_INLINE_REF_CONFLICT`).

### Canonical type registry (v1)

| `type` | Required fields | Purpose |
|:-------|:----------------|:--------|
| `text` | `inline` | Plain or formatted text. `mimeType` defaults to `text/plain`. |
| `code` | `inline`, `annotations.language` | Source-code fragment with a language tag. |
| `reasoning_trace` | `inline` | Model chain-of-thought or internal reasoning. |
| `citation` | `inline`, `annotations.source` | Source citation or reference. |
| `screenshot` | `inline` or `ref`, `mimeType` (`image/*`) | Captured screen image. |
| `image` | `inline` or `ref`, `mimeType` (`image/*`) | General image content. |
| `diff` | `inline`, `annotations.language: "diff"` | Unified-format diff or patch. |
| `file` | `inline` or `ref`, `mimeType` | File produced by the agent. |
| `execution_result` | `parts[]` (each a full OutputPart) | Compound output from code execution (command + stdout + stderr + chart). |
| `error` | `inline` | Error or diagnostic message emitted mid-stream. `annotations.errorCode` optional. |

**Custom types.** Any `type` not listed above is treated as a custom type and collapsed to `text` at the adapter boundary, with the original type preserved in `annotations.originalType`. To avoid colliding with future registry entries, all vendor-defined types MUST use a reverse-DNS namespace: `x-<vendor>/<typeName>` (e.g., `x-acme/heatmap`, `x-myorg/audio-transcript`).

**`schemaVersion`.** Omit `schemaVersion` for parts that use only the v1 field set -- the adapter defaults it to `1`. Bump it to a higher value only if you are emitting fields introduced in a later registry version.

**`status` for streaming parts.** When streaming via `lenny/output`, set `status: "streaming"` on in-progress parts (reusing the same `id` across updates) and emit the final update with `status: "complete"`. For parts that failed mid-stream, emit `status: "failed"`.

**Cross-protocol fidelity.** Field-level round-trip fidelity through each external adapter (MCP, OpenAI Chat Completions, Open Responses, REST, A2A) is documented in [Spec §15.4.1 -- Translation Fidelity Matrix](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#1541-adapterbinary-protocol). Runtimes that need lossless round-trip should restrict clients to REST.

### Simplified text shorthand (Basic level)

Basic-level runtimes may emit a `response` with a top-level `text` field instead of a full `output` array:

```json
{ "type": "response", "text": "The answer is 42." }
```

The adapter normalizes this to the canonical form `{"type": "response", "output": [{"type": "text", "inline": "The answer is 42."}]}` before forwarding. Use the full form when you have more than one part or need a non-text type.

### Examples

```json
{ "type": "text", "inline": "Processing 3 files..." }

{ "type": "code", "inline": "fmt.Println(\"hi\")", "annotations": { "language": "go" } }

{ "type": "diff",
  "inline": "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,3 @@\n-old\n+new",
  "annotations": { "language": "diff", "path": "main.go" } }

{ "type": "image",
  "ref": "lenny-blob://tenant_acme/sess_abc/part_xyz?ttl=3600&enc=aes256gcm",
  "mimeType": "image/png",
  "annotations": { "caption": "UI heatmap after fix" } }

{ "type": "execution_result",
  "parts": [
    { "type": "code", "inline": "ls -la", "annotations": { "language": "bash" } },
    { "type": "text", "inline": "total 16\n-rw-r--r--  1 user  group   42 Apr 18 10:00 README.md" }
  ]
}

{ "type": "error",
  "inline": "Tool timed out after 30s",
  "annotations": { "errorCode": "TOOL_TIMEOUT" } }
```

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

At the Basic level, the adapter reports health based on process liveness (is your binary still running?) and heartbeat responsiveness (did it ack within 10 seconds?).

At the Standard and Full levels, you can optionally expose an HTTP health check endpoint (e.g., `/healthz` on a local port) that the adapter will incorporate into its health reporting. This is documented in the SDK examples.

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

**What each level needs to read:**

| Level | What to read |
|------|-------------|
| Basic | Not required for core operation. The four built-in tools are a fixed contract. Optionally read `adapterLocalTools` to discover custom adapter-local tools. |
| Standard | `platformMcpServer.socket`, `connectorServers`, `mcpNonce` --- to connect to and authenticate with local MCP servers. |
| Full | Standard fields plus `lifecycleChannel.socket`. |

**Manifest field reference:**

| Field | Description |
|-------|-------------|
| `version` | Manifest schema version. A version increment indicates a breaking change. |
| `platformMcpServer.socket` | Abstract Unix socket path for the platform MCP server. |
| `lifecycleChannel.socket` | Abstract Unix socket path for the lifecycle channel (Full level). |
| `connectorServers` | Array of connector MCP server entries with `id` and `socket`. |
| `runtimeMcpServers` | Array of runtime-provided MCP server entries. |
| `adapterLocalTools` | Array of adapter-local tool definitions with name, description, and inputSchema. |
| `sessionId` | The session identifier for this pod. |
| `taskId` | The current task identifier. Regenerated per task in task mode. |
| `mcpNonce` | Hex nonce for authenticating MCP connections. |
| `observability.otlpEndpoint` | OTLP collector endpoint for runtime-emitted OpenTelemetry spans. |

**Forward compatibility:** Your runtime must silently ignore unknown top-level fields. The adapter may add new fields in future versions without incrementing `version`. A `version` increment indicates a breaking change to existing field semantics.

---

## Lifecycle Channel (Full level only)

The lifecycle channel is a bidirectional JSON Lines stream over an abstract Unix socket (`@lenny-lifecycle`). The runtime connects as a client; the adapter listens. Each message is a single JSON object terminated by `\n`.

Opening the lifecycle channel is optional. Runtimes that do not open it operate in fallback-only mode (Basic or Standard level behavior).

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

### Complete Basic-level session trace

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

### Full-level checkpoint handshake

```
Adapter sends on lifecycle channel:
{"type":"checkpoint_request","checkpointId":"chk_42","deadlineMs":60000}

Runtime quiesces (flushes buffers, stops writing to workspace), then:
{"type":"checkpoint_ready","checkpointId":"chk_42"}

Adapter snapshots the workspace and sends:
{"type":"checkpoint_complete","checkpointId":"chk_42","status":"ok"}

Runtime resumes normal operation.
```

### Full-level interrupt handshake

```
Adapter sends on lifecycle channel:
{"type":"interrupt_request","interruptId":"int_7","deadlineMs":30000}

Runtime reaches a safe stop point (e.g., finishes current LLM call), then:
{"type":"interrupt_acknowledged","interruptId":"int_7"}

Session transitions to suspended state. Pod is held.
```

---

## Canonical artifacts

The adapter protocol is defined by three published schema artifacts. Runtime authors -- and the adapter compliance suite (`lenny-ctl runtime verify`) -- validate against these files rather than the narrative prose in this guide.

| Artifact | Purpose | Canonical URL |
|:---------|:--------|:--------------|
| `lenny-adapter.proto` | gRPC service definition for the gateway ↔ adapter control plane (`Attach`, `SendMessage`, `Checkpoint`, `DemoteSDK`, etc.) and all associated message types. | `https://schemas.lenny.dev/adapter/v1/lenny-adapter.proto` |
| `lenny-adapter-jsonl.schema.json` | JSON Schema for the stdin/stdout JSON Lines frames exchanged between the adapter and the agent binary (`message`, `tool_call`, `tool_result`, `response`, `heartbeat`, lifecycle frames). | `https://schemas.lenny.dev/adapter/v1/lenny-adapter-jsonl.schema.json` |
| `outputpart.schema.json` | JSON Schema for the structured `outputParts` field used in `agent_output` events and tool results (text, image, redaction, inline-file parts). | `https://schemas.lenny.dev/adapter/v1/outputpart.schema.json` |

Each artifact is versioned independently and distributed alongside every Lenny release under `/schemas/adapter/v1/` in the release bundle. Compliance is checked programmatically during `lenny-ctl runtime verify`, which returns structured diff output when a runtime's frames fail validation. Fix your runtime to produce valid frames rather than pinning an older schema version -- the schemas are stable within `v1`, and breaking changes bump the major version.
