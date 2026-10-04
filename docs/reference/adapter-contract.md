---
layout: default
title: "Adapter Contract"
parent: Reference
nav_order: 9
---

# Adapter Contract

This page is the complete reference for the protocol between the Lenny adapter sidecar and your runtime binary. It covers the sidecar architecture, the gRPC control protocol (gateway to adapter), the stdin/stdout JSON Lines protocol (adapter to your binary), and the CH-RUNTIMEOPS (Full level only).

---

## Sidecar Architecture

Every Lenny agent pod contains two containers:

1. **Adapter container** (Lenny-managed) --- handles all platform communication: gRPC to the gateway, mTLS certificate management, workspace staging, credential injection, health checks, and MCP server hosting.
2. **Agent container** (your runtime binary) --- your code. Communicates with the adapter exclusively via stdin/stdout (at every integration level) and optionally via local Unix sockets (Standard and Full levels).

![Lenny pod with two containers. The adapter container runs gRPC to the gateway, MCP servers over Unix sockets, the CH-RUNTIMEOPS, file staging, health checks, and credential management. The agent container reads JSON from stdin, writes JSON to stdout, and optionally connects to MCP servers and the CH-RUNTIMEOPS via Unix sockets. The adapter connects to the Lenny gateway over gRPC with mTLS.](../assets/diagrams/lenny-pod-containers.svg)

<!--
ASCII fallback for the diagram above (lenny-pod-containers):

  +---------------------------------------------------------------------+
  |  LENNY POD                                                          |
  |                                                                     |
  |  +-----------------------+          +----------------------------+  |
  |  |  Adapter container    |  stdin ==>  Agent container          |  |
  |  |  (Lenny-managed)      |  <== stdout (your runtime binary)    |  |
  |  |                       |                                       |  |
  |  |  - gRPC to gateway    |  socket                              |  |
  |  |  - MCP servers (Unix) |  ====>   - reads JSON from stdin     |  |
  |  |  - CH-RUNTIMEOPS  |          - writes JSON to stdout     |  |
  |  |  - file staging       |          - optionally connects to    |  |
  |  |  - health checks      |            MCP servers and lifecycle |  |
  |  |  - credential mgmt    |            channel via Unix sockets  |  |
  |  +-----------+-----------+          +----------------------------+  |
  |              | gRPC (mTLS)                                          |
  +--------------|------------------------------------------------------+
                 v
           Lenny Gateway
-->


The adapter writes pod-scoped configuration to `/run/lenny/adapter-manifest.json` before spawning your binary. The manifest tells your runtime where to find the MCP servers and the CH-RUNTIMEOPS, which nonce authenticates its connections, and which adapter-local tools exist. Each session's own context, including the path of its credential file, reaches your runtime in that session's `session_start` frame, described under [Inbound Messages](#inbound-messages-adapter-writes-to-your-stdin). For core operation a Basic-level runtime reads only `mcpNonce`, and only when it dials the message channel as a socket (see [Connection Handshake](#connection-handshake)). The four built-in adapter-local tools (`read_file`, `write_file`, `list_dir`, `delete_file`) are a fixed contract, and a Basic-level runtime that reads a credential file reads `credentialsPath` from the session's `session_start` frame to find it.

---

## gRPC Control Protocol (Gateway to Adapter)

These RPCs are between the gateway and the adapter. Your runtime binary never sees them directly, but understanding them helps you reason about the pod lifecycle.

**Gateway-to-Adapter RPCs:**

| RPC | Description |
|-----|-------------|
| `PrepareWorkspace` | Accept streamed files into the session's staging area (`/workspace/slots/{sessionId}/staging`) |
| `FinalizeWorkspace` | Validate staging, materialize to `/workspace/slots/{sessionId}/current` |
| `RunSetup` | Execute bounded setup commands (deployer-defined) |
| `StartSession` | Start the agent runtime with `cwd=/workspace/slots/{sessionId}/current` (pod-warm mode) |
| `ConfigureWorkspace` | Point a pre-connected session at the finalized `cwd` (SDK-warm mode). Timeout: 10s. |
| `DemoteSDK` | Write the session's `session_end` frame when the pod holds a session in `running`, tear down the pre-connected SDK process, drop the adapter's slot registry entry the pod holds if it holds one, whichever session holds it, running that slot's cleanup inside the call before it answers, and return the pod to pod-warm state. Where that cleanup runs and completes, the next bind sequence on the pod creates a fresh entry and stamps it with that attempt's own token. |
| `Attach` | Connect client stream to running session |
| `Interrupt` | Interrupt current agent work |
| `Checkpoint` | Export recoverable session state |
| `CheckpointBarrier` | Barrier signal during graceful drain; adapter quiesces tool-call dispatch, flushes a best-effort checkpoint, and replies via `CheckpointBarrierAck` |
| `CoordinatorFence` | Announce new coordination generation on gateway handoff; precondition for any subsequent operational RPC |
| `ExportPaths` | Package files for delegation, rebased per export spec |
| `AssignCredentials` | Push per-provider credential map to the runtime before session start |
| `RotateCredentials` | Push replacement credentials for a specific provider mid-session |
| `Resume` | Restore from checkpoint on a replacement pod |
| `ReportUsage` | Report LLM token counts extracted from provider responses |
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. Every request states which teardown it is asking for, by carrying either the bind attempt whose registry entry it is reclaiming or the unconditional-teardown flag, and a request carrying neither or both is rejected as invalid and performs nothing. The response reports what became of the entry the request was addressed to: `reclaimed` when the adapter held that entry and released the slot, `superseded` when the adapter holds an entry the request is not addressed to, so nothing was released, and `absent` when the adapter holds no entry for the session. Every outcome is answered on a successful call, and the two outcomes that remove nothing run neither teardown. The slot release removes the session's slot tree and runs whenever the request removes an entry, whether or not `AssignCredentials` has bound that entry. The runtime teardown runs only for a session whose start the adapter has admitted: it flushes the session's final usage report, writes the session's `session_end` frame on stdin when the session reached `running`, and then ends the session's use of the runtime process, which stays alive for the pod's life. It sends the runtime no CH-RUNTIMEOPS frame. The adapter reports the per-slot cleanup outcome through `ReportSessionScrub` for a slot that reached `running`, and reports no outcome for a cleanup on a slot that did not. The request carries the recycle disposition beside that teardown: on the recycle disposition the adapter keeps the pod process alive, runs the whole-pod scrub the carried `RecycleScrub` parameterizes, and reports its outcome for `podId` through `ReportPodScrub`. |

**Bind attempt token.** The gateway may attempt to bind one session onto a pod more than once, and each attempt mints its own opaque token, carried on the bind-sequence requests the linked contract's carriage table lists. The adapter stamps the token onto the entry it creates and afterwards compares it for equality, which is what lets a teardown that compensates an abandoned attempt name the entry it is entitled to destroy. A start that the adapter refuses at its start confirmation is taken back off the runtime process the pod's sessions share; when a later attempt at the same session has claimed the slot in the meantime, that take-back closes the later attempt's runtime session. The rules the adapter applies to the token are numbered and named in [Role and Gateway RPC Contract](https://github.com/lennylabs/lenny/blob/main/spec/04_system-components.md#471-role-and-gateway-rpc-contract), which states for each rule whatever answer it fixes; [Runtime Adapter Specification](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#154-runtime-adapter-specification) states what conformance against those rules means. An adapter author reads both. A runtime binary issues none of the requests those rules govern, which is why this page states the teardown behaviour and leaves the rules where they are stated.

**Adapter-to-Gateway RPCs:**

| RPC | Description |
|-----|-------------|
| `ReportSessionScrub` | Report a per-slot cleanup's outcome (`released` or `leaked`) for the cleanups the `Shutdown` row states the adapter reports, and for no other release. The request is session-scoped: it is addressed by the identifier of the released session and names no slot. The gateway increments the pod's served-session count and feeds the leak ledger. |
| `ReportPodScrub` | Report the binary outcome of the whole-pod scrub the adapter runs when occupancy reaches zero on a recycling pod, and whether the runtime process can serve the next session. The gateway computes the recycle disposition from the outcome and `sessionPolicy`, and retires the pod with `runtime_not_live` when the report states that the runtime cannot serve the next session or omits that fact. |

**Scrub responsibilities.** The per-slot cleanup and the whole-pod scrub are adapter-executed and gateway-coordinated, with no CH-RUNTIMEOPS handshake between sessions. Your runtime process lives as long as the pod. On a recycling pool with `recycle.maxSessionsPerPod` above 1 and a `recycle.scrubProfile` other than `vm-restart`, which requires `sessionPolicy.acknowledgeProcessLevelIsolation: true`, it serves the pod's later sessions on the same connection, keyed by `sessionId`, up to `maxSessionsPerPod`. Each session opens with its `session_start` frame and ends with its `session_end` frame (see [Inbound Messages](#inbound-messages-adapter-writes-to-your-stdin)), and a runtime that exits after its session makes the pod retire at the recycle boundary. The adapter runs the credential purge, deployer `cleanupCommands`, and the scrub, which clears the shared paths and does not reach the runtime process, then reports through these RPCs.

**Checkpoint and Interrupt are mutually exclusive.** The adapter maintains a per-session operation lock. Only one of these operations may execute at a time; the other is queued until the first completes.

**Adapter-to-Gateway events** (sent over the gRPC CH-ADAPTEREVENTS):

| Event | Description |
|-------|-------------|
| `RATE_LIMITED` | Current credential is rate-limited; request fallback |
| `AUTH_EXPIRED` | Credential lease expired or was rejected by provider |
| `PROVIDER_UNAVAILABLE` | Provider endpoint is unreachable |
| `LEASE_REJECTED` | Runtime cannot use the assigned credential |
| `CheckpointBarrierAck` | Acknowledges a `CheckpointBarrier` after quiescence and checkpoint flush (fields: `barrier_id`, `checkpoint_ref`) |
| `AdapterTerminating` | Self-initiated terminal notification (e.g., coordinator-loss hold timeout); lets the gateway transition the session without waiting for the orphan-session reconciler |
| `FINAL_USAGE_REPORT` | Final lifecycle-stream message sent before the stream closes, after all in-flight `ReportUsage` calls have been flushed |

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

### Connection Handshake

When your binary dials the message channel (`CH-MSGSOCK`) as a socket, and whenever a Full-level runtime dials the [CH-RUNTIMEOPS](#ch-runtimeops-full-level-only) socket, the connection opens with a handshake that precedes the channel's framed protocol and sits outside its frame schema:

1. Before each dial, read `mcpNonce` from `/run/lenny/adapter-manifest.json`. When the manifest is not yet published, wait for it before dialing; the adapter refuses a connection it accepts while no manifest is published.
2. Write the nonce line as the first line on the connection, within 500 ms of the connection being accepted:

   ```json
   {"_lennyNonce":"<nonce_hex>"}
   ```

   The adapter compares the value in constant time with the `mcpNonce` the manifest carries when it accepts the connection.
3. In nonce-only mode (a runtime registered with `requireSoPeercred: false`, where the adapter cannot verify the peer UID), the adapter then writes a challenge line, and the runtime answers it within 500 ms with `HMAC-SHA256(key = mcpNonce, data = challenge)`, hex-encoded:

   ```json
   {"_lennyChallenge":"<hex>"}
   {"_lennyChallengeResponse":"<hex HMAC>"}
   ```

   Answer a `_lennyChallenge` whenever one arrives before the first protocol frame on the connection.
4. When the adapter closes the connection before the first protocol frame, read the manifest again and dial again. The adapter closes a refused connection, or one that fails either check, with no protocol response, and it keeps accepting new connections. A later session's manifest write can replace `mcpNonce` while your runtime is dialing, and the redial picks up the current value.

---

### Inbound Messages (adapter writes to your stdin)

#### `session_start` --- Open a Session

The adapter writes `session_start` once for each start and each resume of a session. On the connection that carries the session, it precedes every other frame addressed to the session. The frame carries the session's own context, which the pod-scoped manifest does not carry.

```json
{
  "type": "session_start",
  "sessionId": "sess_abc",
  "startId": "st_1",
  "credentialsPath": "/run/lenny/slots/sess_abc/credentials.json",
  "experimentContext": { "experimentId": "claude-v2-rollout", "variantId": "treatment", "inherited": false },
  "tracingContext": null,
  "llm": { "deliveryMode": "proxy", "dialect": "anthropic", "apiKeyEnv": "ANTHROPIC_API_KEY" }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Always `"session_start"`. |
| `sessionId` | string | Yes | The session the frame opens. The adapter populates it on every pod. |
| `startId` | string | Yes | Identifies this write of the frame. Each `session_start` the adapter writes on the pod carries a value distinct from every other it has written there, so the acknowledgement of an earlier start of the same session is not read as this start's. |
| `credentialsPath` | string | No | Absolute path to this session's credential file, `/run/lenny/slots/{sessionId}/credentials.json`. The adapter writes the file before it writes this frame and rewrites it in place when this session's credentials rotate. A runtime that reads credential material reads its path from this field rather than assuming a fixed location. Present whenever the adapter provisioned a credential file for the session and absent otherwise; a runtime given no path loads no credential bundle for the session. |
| `experimentContext` | object or null | No | The session's experiment enrollment: `experimentId` (string), `variantId` (string), and `inherited` (boolean, `true` when propagated from a parent through delegation). Absent or `null` when the session is not enrolled in an experiment. Use it to tag traces with variant metadata in your eval platform. |
| `tracingContext` | object or null | No | Tracing identifiers propagated from the parent runtime through delegation, as an opaque map of strings such as `{"langsmith_run_id": "run_abc123"}`. Absent or `null` for a top-level session. A child runtime uses it to stitch its native traces into the parent's trace tree. It carries only non-sensitive identifiers. |
| `llm` | object or null | No | The session's LLM provider configuration. Absent or `null` when the session has no active LLM credential lease. |
| `llm.deliveryMode` | string | Yes | `direct` or `proxy`: the credential delivery mode of the session's LLM provider pool. |
| `llm.dialect` | string | Yes in proxy mode | `openai` or `anthropic`. In proxy mode the runtime's LLM SDK speaks this dialect to the `materializedConfig.proxyUrl` in the session's credential file. Omitted in direct mode, where the runtime uses the provider's native SDK. |
| `llm.apiKeyEnv` | string | No | The environment variable name the runtime's LLM SDK reads for its API key, by convention `ANTHROPIC_API_KEY` for `dialect: anthropic` and `OPENAI_API_KEY` for `dialect: openai`. The runtime supplies the lease token from this session's credential file as this session's API key: it passes the token to the LLM client it builds for the session, or sets the variable only in the environment of a subprocess it starts for this session. It does not set the variable in its own process environment, which every session the process serves shares. |
| `llm.headers` | object | No | Header names and string values the runtime's LLM SDK sends to the LLM proxy in proxy mode. Omitted when no header is advertised. |

A runtime holds a session from its read of the session's `session_start`, including while it creates the session's context, until its read of the session's `session_end`. The following rules govern the frame:

- A runtime that keeps per-session context creates the session's context from this frame and then answers it with [`session_started`](#outbound-messages-your-runtime-writes-to-stdout). A runtime that has opened the CH-RUNTIMEOPS answers every `session_start` with `session_started`, whether or not it keeps per-session context.
- A runtime that keeps no per-session context and opens no CH-RUNTIMEOPS may ignore the frame under the unknown-type rule.
- A runtime ignores a `session_start` for a session it already holds, except that it answers the frame with `session_started` again.
- A runtime that keeps per-session context and fails to create a session's context answers the session's `session_start` with a `session_started` carrying `error`, and keeps running. It answers a `message` for a session whose `session_start` it has not received, or whose context it failed to create, with a `response` carrying `error` for that `sessionId`, and keeps running.

#### `message` --- All Content Delivery

The unified message type for all inbound content: initial task, mid-session injection, reply to `request_input`, and sibling notification.

```json
{
  "type": "message",
  "id": "msg_001",
  "input": [
    { "type": "text", "inline": "Summarize the files in /workspace/slots/sess_abc/current" }
  ],
  "from": { "kind": "client", "id": "client_8f3a2b" },
  "inReplyTo": null,
  "threadId": "t_01",
  "delivery": "queued",
  "delegationDepth": 0,
  "sessionId": "sess_abc"
}
```

**Field reference:**

| Field | Type | Level | Description |
|-------|------|------|-------------|
| `type` | string | All | Always `"message"` |
| `id` | string | All | Unique message identifier (gateway-assigned ULID, `msg_` prefix) |
| `input` | MessagePart[] | All | Array of content parts. See MessagePart format below. |
| `from` | object | Standard+ | Sender identity. `kind`: `"client"`, `"agent"`, `"system"`, or `"external"`. Adapter-injected; never set by your runtime. |
| `inReplyTo` | string or null | Standard+ | If set, matches an outstanding `lenny/request_input` call on the target |
| `threadId` | string or null | Standard+ | Thread label. One implicit thread per session in v1. |
| `delivery` | string or null | Standard+ | `"immediate"` or `"queued"` (default). Controls interrupt behavior. |
| `delegationDepth` | integer | Standard+ | How many tree hops this message crossed. Informational. |
| `sessionId` | string | All | Names the session this message is addressed to. The adapter populates it on every pod, whatever the pool's `sessionPolicy.maxConcurrentSessions`. |

**Basic-level runtimes:** Read `type`, `id`, `input`, and `sessionId`. The remaining envelope fields may be ignored safely. `sessionId` is excepted from that permission: it names the session the message is addressed to, the adapter populates it on every pod, and a Basic-level runtime echoes it on the session-scoped frames it emits in response.

**The `input` array contains MessagePart objects.** The simplest MessagePart is:

```json
{ "type": "text", "inline": "Hello, world!" }
```

Only `type` and `inline` are required. All other MessagePart fields (`schemaVersion`, `id`, `mimeType`, `ref`, `annotations`, `parts`, `status`) are optional with sensible defaults.

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
  "sessionId": "sess_abc"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"tool_result"` |
| `id` | string | Matches the `id` of the `tool_call` this result responds to |
| `content` | MessagePart[] | Result content |
| `isError` | boolean | `true` if tool execution failed. Default `false`. |
| `sessionId` | string | Names the session this result is addressed to. The adapter populates it on every pod, whatever the pool's `sessionPolicy.maxConcurrentSessions`. |

**Correlation:** Every `tool_result.id` matches a previously emitted `tool_call.id`. Results may arrive in any order when you have multiple outstanding tool calls. Other inbound messages (`heartbeat`, additional `message` content) may arrive before the `tool_result` --- your runtime must handle interleaved delivery.

#### `heartbeat` --- Liveness Ping

```json
{ "type": "heartbeat", "ts": 1717430400 }
```

Your runtime MUST respond with a `heartbeat_ack` within 10 seconds. If no ack is received, the adapter treats the process as hung and ends the session, and the runtime process receives no signal.

#### `session_end` --- Release a Session

```json
{ "type": "session_end", "sessionId": "sess_abc" }
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"session_end"`. |
| `sessionId` | string | The session the frame releases. |

The adapter writes `session_end` when a session it started ends on the runtime: at the session's teardown, and when a start it opened fails before the session reaches `running`. An interrupt, which leaves the session resumable, a heartbeat escalation, and pod exit write no `session_end` themselves; the teardown that follows a heartbeat escalation writes the session's `session_end`. The frame is not acknowledged and carries no deadline. The following rules govern it:

- A `session_end` ends the start of the latest `session_start` the runtime read for the session before it, other than one it ignored as a repeat. The runtime releases the context that start created, identified by that `session_start`'s `startId`, and when it is still creating that context it releases the context once the creation ends. Any other creation of a context for the same session that is still in flight, which belongs to an earlier start that an earlier `session_end` ended, is released when that creation ends, and that context serves no frame of the session.
- After `session_end` the runtime writes no further frame addressed to the session, except the `session_started` it owes for a `session_start` read before the `session_end`, and the `llm_request_completed` of a request whose `llm_request_started` preceded the `session_end`. It keeps serving the pod's other sessions.
- A runtime ignores a `session_end` for a session it does not hold.
- When the connection that carries a session ends, every session it carries ends, and no `session_end` follows.

#### `shutdown` --- Graceful Termination

```json
{ "type": "shutdown", "reason": "drain", "deadline_ms": 10000 }
```

`shutdown` is process-scoped: the adapter never writes it at a session boundary. Your runtime must finish current work and exit within `deadline_ms`. No acknowledgment message is required --- the adapter watches for process exit. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds.

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
  "sessionId": "sess_abc"
}
```

**Simplified shorthand** (Basic-level convenience --- adapter normalizes to the full form). A Basic-level runtime echoes the identifier the adapter handed it on the shorthand form as well:

```json
{ "type": "response", "sessionId": "sess_abc", "text": "The answer is 42." }
```

**Error reporting via `response`.** Include an optional `error` field for structured error reporting:

```json
{
  "type": "response",
  "sessionId": "sess_abc",
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

**Relationship with `lenny/output`:** At the Standard and Full levels, you may emit output parts incrementally via the `lenny/output` platform tool. The stdout `response` message is always required to signal task completion, regardless of whether `lenny/output` was used. Its `output` array contains only parts not already emitted via `lenny/output`. If you emitted all output via `lenny/output`, send an empty array: `{"type": "response", "sessionId": "sess_abc", "output": []}`.

#### `tool_call` --- Request Tool Execution

Request the adapter to execute a tool. At the Basic level, only adapter-local tools are available (`read_file`, `write_file`, `list_dir`, `delete_file`). At the Standard and Full levels, platform tools are accessed via the MCP client connection, not via `tool_call`.

```json
{
  "type": "tool_call",
  "id": "tc_001",
  "name": "read_file",
  "arguments": { "path": "/workspace/slots/sess_abc/current/README.md" },
  "sessionId": "sess_abc"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"tool_call"` |
| `id` | string | Unique call identifier. Used to correlate the inbound `tool_result`. Recommended format: `tc_` prefix with monotonic counter or random suffix. |
| `name` | string | Tool name (e.g., `read_file`, `write_file`) |
| `arguments` | object | Tool-specific parameters |
| `sessionId` | string, optional | Names the session this call belongs to. Echo the identifier the adapter handed you on the frame you are responding to. An identifier the frame omits resolves to the binding of the stream that delivered it on a pod holding at most one slot, and is rejected on a pod holding more. |

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

#### `session_started` --- Acknowledge a Session Start

```json
{ "type": "session_started", "sessionId": "sess_abc", "startId": "st_1" }
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | Yes | Always `"session_started"`. |
| `sessionId` | string | Yes | The session whose `session_start` the frame answers, echoed from that frame. |
| `startId` | string | Yes | The `startId` of the `session_start` the frame answers, including a repeated one. |
| `error` | object | No | `{"code": string, "message": string}`, the object a `response` error carries. Present when the runtime failed to create the session's context, and absent otherwise. |

Two kinds of runtime write `session_started`:

- A runtime that keeps per-session context writes it for a session after it reads that session's `session_start` and has created the session's context, or with `error` when the creation failed.
- A runtime that has opened the CH-RUNTIMEOPS writes it for every `session_start` it reads, whether or not it keeps per-session context.

Either runtime writes it again for a `session_start` that names a session it already holds. A runtime that keeps no per-session context and opens no CH-RUNTIMEOPS writes no `session_started`.

The adapter waits for `session_started` when your runtime's CH-RUNTIMEOPS connection completed its capability handshake before the start began. The wait is bounded, and when the request that starts the session carries a deadline, the wait ends no later than that deadline. A start whose wait ends without the frame, or whose `session_started` carries `error`, fails: the adapter writes the session's `session_end` and takes the session back off the runtime. The adapter does not wait for `session_started` before it writes the session's other frames on stdin, because `session_start` already precedes them on the same connection. It consumes the frame and relays it to no client, and it drops a `session_started` whose `startId` is not that of the start waiting for it. How the frame orders the session's CH-RUNTIMEOPS frames is stated under [CH-RUNTIMEOPS](#ch-runtimeops-full-level-only).

#### `status` --- Optional Status Update

```json
{ "type": "status", "state": "thinking", "message": "Analyzing code...", "sessionId": "sess_abc" }
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"status"` |
| `state` | string | Status label (for example `thinking`, `analyzing`, or `calling_tool`) |
| `message` | string, optional | Human-readable detail |
| `sessionId` | string, optional | Names the session this status belongs to. Echo the identifier the adapter handed you. An identifier the frame omits resolves to the binding of the stream that delivered it on a pod holding at most one slot, and is rejected on a pod holding more. |

Informational. The adapter forwards status updates to the gateway for client visibility. Not required at any integration level.

#### `set_tracing_context` --- Propagate Tracing Identifiers

```json
{
  "type": "set_tracing_context",
  "context": { "langsmith_run_id": "run_abc123" },
  "sessionId": "sess_abc"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"set_tracing_context"` |
| `context` | object | Map of string keys to string values carrying opaque, non-sensitive tracing identifiers |
| `sessionId` | string, optional | Names the session registering the identifiers. Echo the identifier the adapter handed you. An identifier the frame omits resolves to the binding of the stream that delivered it on a pod holding at most one slot, and is rejected on a pod holding more. The published JSONL schema accepts only a JSON string here; `null` and any other type fail schema validation. |

Registers tracing identifiers for the session that emitted the frame. The gateway merges the submitted context into that session's recorded context, validates the merged result against the tracing-context rules at registration time, and attaches the registered context to each child's delegation lease when the session delegates. The adapter itself stores no context and attaches none to later requests. The frame is available at all integration levels.

**Addressing.** The adapter hands the runtime a per-session identifier on every pod, and the runtime echoes that identifier in the frame's `sessionId`. The adapter resolves the frame against the stream that delivered it. That stream is bound to one session and to that session's slot, on every pod. The adapter applies the frame only when the frame's `sessionId` matches the stream's session and the adapter's registry still holds that address with a bound session. The comparison is exact string equality.

Two dispositions reject a frame. A frame that carries no `sessionId` resolves to the receiving stream's own binding on a pod holding at most one slot, and on a pod holding more than one slot it is rejected and relayed to no stream by that stream's demultiplexer, counted in `lenny_adapter_unaddressed_frame_rejected_total`, and logged. A frame whose `sessionId` names no live binding on the receiving stream is dropped, counted in `lenny_adapter_set_tracing_context_dropped_total` (see [Metrics](metrics.md)), and logged as a protocol error. Nothing is relayed onward and nothing is returned to the runtime; the runtime receives no error for a dropped frame. A frame's identifier names no live binding either when it is not the receiving stream's own session, or when the adapter's registry no longer holds that identifier with a bound session, which is the case while an ending session's stream drains after its slot is released.

---

## MessagePart Reference

`MessagePart` is Lenny's internal content model -- the unit of content in inbound `message.input[]`, outbound `response.output[]`, `tool_result.content[]`, and platform-tool `lenny/output` payloads. The gateway translates to and from external protocol shapes (MCP content blocks, OpenAI content, A2A parts) at the edge; your runtime always produces and consumes `MessagePart` directly.

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
| `parts` | MessagePart[] | -- | Nested parts for compound outputs (e.g., `execution_result`). |
| `status` | string | `complete` | `streaming` / `complete` / `failed` -- primarily for incremental delivery via `lenny/output`. |

**`inline` vs `ref` -- size policy.** The gateway chooses a representation based on payload size; your runtime may emit either form and let the gateway promote it:

| Size | Representation | Consumer sees |
|:-----|:---------------|:--------------|
| ≤ 64 KB | `inline` (UTF-8 or base64) | `inline` populated, `ref` absent |
| > 64 KB and ≤ 50 MB | Staged to blob store; `ref` set to a `lenny-blob://` URI | `ref` populated, `inline` absent |
| > 50 MB | Rejected at ingress | `413 MESSAGEPART_TOO_LARGE` |

Setting both `inline` and `ref` on the same part is a validation error (`400 MESSAGEPART_INLINE_REF_CONFLICT`).

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
| `execution_result` | `parts[]` (each a full MessagePart) | Compound output from code execution (command + stdout + stderr + chart). |
| `error` | `inline` | Error or diagnostic message emitted mid-stream. `annotations.errorCode` optional. |

**Custom types.** Any `type` not listed above is treated as a custom type and collapsed to `text` at the adapter boundary, with the original type preserved in `annotations.originalType`. To avoid colliding with future registry entries, all vendor-defined types MUST use a reverse-DNS namespace: `x-<vendor>/<typeName>` (e.g., `x-acme/heatmap`, `x-myorg/audio-transcript`).

**`schemaVersion`.** Omit `schemaVersion` for parts that use only the v1 field set -- the adapter defaults it to `1`. Bump it to a higher value only if you are emitting fields introduced in a later registry version.

**`status` for streaming parts.** When streaming via `lenny/output`, set `status: "streaming"` on in-progress parts (reusing the same `id` across updates) and emit the final update with `status: "complete"`. For parts that failed mid-stream, emit `status: "failed"`.

**Cross-protocol fidelity.** Field-level round-trip fidelity through each external adapter (MCP, OpenAI Chat Completions, Open Responses, REST, A2A) is documented in [Spec §15.4 -- Translation Fidelity Matrix](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#translation-fidelity-matrix). Runtimes that need lossless round-trip should restrict clients to REST.

### Simplified text shorthand (Basic level)

Basic-level runtimes may emit a `response` with a top-level `text` field instead of a full `output` array, echoing on it the per-session identifier the adapter handed them:

```json
{ "type": "response", "sessionId": "sess_abc", "text": "The answer is 42." }
```

The adapter normalizes this to the canonical form `{"type": "response", "sessionId": "sess_abc", "output": [{"type": "text", "inline": "The answer is 42."}]}` before forwarding. Use the full form when you have more than one part or need a non-text type.

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

The adapter writes `/run/lenny/adapter-manifest.json` before spawning your binary. The manifest is one pod-global file, read-only to the agent container, and it carries only pod-scoped fields. The adapter rewrites it before each session's runtime start, including each session on a recycling pod. A session's own identifier, credential path, experiment and tracing context, and LLM configuration reach your runtime in that session's `session_start` frame, so a later start's rewrite changes none of them for an earlier session. On a pod holding more than one bound session, a later session's start replaces the `mcpNonce` member while an earlier session's runtime is still processing. The intra-pod MCP servers are pod-wide and started at most once per pod: a server validates a presented nonce against the value the manifest carried at the start that bound that server, and a later session's manifest write does not re-arm a running server. A message channel or CH-RUNTIMEOPS socket connection is checked once, when the adapter accepts it, against the `mcpNonce` the manifest carries at that moment (see [Connection Handshake](#connection-handshake)).

```json
{
  "version": 1,
  "platformMcpServer": { "socket": "@lenny-platform-mcp" },
  "runtimeOps": { "socket": "@lenny-runtime-ops" },
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
  "mcpNonce": "a3f1...c7e2",
  "observability": {
    "otlpEndpoint": "http://otel-collector.lenny-system:4317"
  }
}
```

**What each level needs to read:**

| Level | What to read |
|------|-------------|
| Basic | `mcpNonce`, and only when the runtime dials the message channel as a socket. The four built-in tools are a fixed contract. A Basic-level runtime that reads a credential file reads its location from the `credentialsPath` member of the session's `session_start` frame. Optionally read `adapterLocalTools` to discover custom adapter-local tools. |
| Standard | `platformMcpServer.socket`, `connectorServers`, `mcpNonce` --- to connect to and authenticate with local MCP servers. |
| Full | Standard fields plus `runtimeOps.socket`. |

**Manifest field reference:**

| Field | Description |
|-------|-------------|
| `version` | Manifest schema version. A version increment indicates a breaking change. |
| `platformMcpServer.socket` | Abstract Unix socket path for the platform MCP server. |
| `runtimeOps.socket` | Abstract Unix socket path for the CH-RUNTIMEOPS (Full level). |
| `connectorServers` | Array of connector MCP server entries with `id` and `socket`. |
| `runtimeMcpServers` | Array of runtime-provided MCP server entries. |
| `adapterLocalTools` | Array of adapter-local tool definitions with name, description, and inputSchema. |
| `mcpNonce` | Hex nonce that authenticates connections to the intra-pod MCP servers, and the message channel and CH-RUNTIMEOPS socket connections, each checked once when the adapter accepts it (see [Connection Handshake](#connection-handshake)). |
| `observability.otlpEndpoint` | OTLP collector endpoint for runtime-emitted OpenTelemetry spans. |

**Forward compatibility:** Your runtime must silently ignore unknown top-level fields. The adapter may add new fields in future versions without incrementing `version`. A `version` increment indicates a breaking change to existing field semantics.

---

## CH-RUNTIMEOPS (Full level only)

The CH-RUNTIMEOPS is a bidirectional JSON Lines stream over an abstract Unix socket (`@lenny-runtime-ops`). The runtime connects as a client; the adapter listens. Each message is a single JSON object terminated by `\n`.

Opening the CH-RUNTIMEOPS is optional. Runtimes that do not open it operate in fallback-only mode (Basic or Standard level behavior). The connection opens with the [connection handshake](#connection-handshake): the runtime's first line is the `_lennyNonce` line, and in nonce-only mode it answers the adapter's challenge.

The session-scoped frames on this channel carry the `sessionId` of the session they concern: `checkpoint_request`, `checkpoint_complete`, `interrupt_request`, `credentials_rotated`, `deadline_approaching`, and `files_updated` from the adapter, and `llm_request_completed` from the runtime. The runtime's replies stay correlated by `checkpointId`, `interruptId`, and `leaseId`. `lifecycle_capabilities`, `lifecycle_support`, and `llm_request_started` are process-scoped and carry no `sessionId`. A runtime that keeps per-session context drops, without a reply, a session-scoped frame that names a session it does not hold.

**Ordering against the session's start.** This channel is a separate connection from the message channel, so the order in which the adapter writes `session_start` on stdin does not reach it. The adapter writes a session-scoped frame for a session on this channel only after it has read the [`session_started`](#outbound-messages-your-runtime-writes-to-stdout) that answers the session's `session_start`. The exception is a session whose start did not wait for `session_started`, because the runtime's CH-RUNTIMEOPS connection had not completed its capability handshake when the start began: the adapter writes that session's frames without this ordering, and a runtime that opens the CH-RUNTIMEOPS during a session is ordered only from its next `session_start`. A path that has a session-scoped frame to write before that read defers the frame until the read, and drops it when the session's start fails, the session ends, or a bound the adapter places on the deferral elapses first; the path then ends as it ends for a frame the runtime does not answer.

### Capability Negotiation

After the connection handshake, the adapter sends `lifecycle_capabilities` first. The runtime replies with `lifecycle_support` declaring which capabilities it supports (a subset of what the adapter offered).

**Adapter sends:**
```json
{
  "type": "lifecycle_capabilities",
  "protocolVersion": "1.0",
  "capabilities": ["checkpoint", "interrupt", "credential_rotation", "deadline_signal"]
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
| `lifecycle_capabilities` | `protocolVersion`, `capabilities[]` | First frame after the connection handshake. |
| `checkpoint_request` | `sessionId`, `checkpointId`, `deadlineMs` | Quiesce the named session's work and signal readiness. Reply with `checkpoint_ready` within `deadlineMs`. |
| `checkpoint_complete` | `sessionId`, `checkpointId`, `status` (`"ok"` or `"failed"`), `reason` | Snapshot upload result; the named session's work may resume. |
| `interrupt_request` | `sessionId`, `interruptId`, `deadlineMs` | Bring the named session's work to a safe stop point within `deadlineMs`. If no `interrupt_acknowledged` arrives within the deadline, the adapter suspends the session anyway. |
| `credentials_rotated` | `sessionId`, `provider`, `credentialsPath`, `leaseId` | New credentials written for the named session at `credentialsPath`; rebind that session and reply with `credentials_acknowledged`. |
| `deadline_approaching` | `sessionId`, `remainingMs`, `trigger` | Advance warning before the named session's forced termination. `trigger`: `"session_age"`, `"budget"`, or `"idle"`. Wrap up that session's work; write no `response` for the session in reply to this frame when no `message` for the session is in flight. |
| `files_updated` | `sessionId` | A mid-session upload has promoted new files into the named session's workspace. One-way, with no acknowledgement. |

#### Runtime to Adapter

| Message Type | Fields | Description |
|-------------|--------|-------------|
| `lifecycle_support` | `capabilities[]` | Capability handshake reply. |
| `checkpoint_ready` | `checkpointId` | Runtime has quiesced and is ready for snapshot. |
| `interrupt_acknowledged` | `interruptId` | Runtime has reached a safe stop point. |
| `credentials_acknowledged` | `leaseId`, `provider` | Runtime has rebound to the new credential. |
| `llm_request_started` | `requestId`, `provider` | Runtime is about to send an outbound LLM request (direct mode only). |
| `llm_request_completed` | `sessionId`, `requestId`, `provider`, `status`, `inputTokens` (optional), `outputTokens` (optional) | Runtime's outbound LLM request completed. In direct mode the adapter adds the token counts to the total of the session `sessionId` names. |

Unknown messages must be silently ignored on both sides for forward compatibility.

---

## Wire Format Examples

### Complete Basic-level session trace

```
1. The runtime's connection to the adapter is open; the runtime dialled it on the pod's first session.

2. Adapter writes to stdin (a runtime that keeps no per-session context ignores it):
   {"type":"session_start","sessionId":"sess_abc","startId":"st_1"}

3. Adapter writes to stdin:
   {"type":"message","id":"msg_001","sessionId":"sess_abc","input":[{"type":"text","inline":"Hello"}],"from":{"kind":"client","id":"client_8f3a2b"},"threadId":"t_01"}

4. Agent reads line from stdin, parses JSON, reads type/id/input/sessionId (ignores the other fields, and echoes sessionId on what it emits).

5. Agent writes to stdout:
   {"type":"response","sessionId":"sess_abc","text":"Echo: Hello"}

6. Adapter reads line from stdout, delivers response to gateway.

7. [Heartbeat interval] Adapter writes:
   {"type":"heartbeat","ts":1717430410}

8. Agent writes:
   {"type":"heartbeat_ack"}

9. The session ends. Adapter writes:
   {"type":"session_end","sessionId":"sess_abc"}

10. The pod drains. Adapter writes:
   {"type":"shutdown","reason":"drain","deadline_ms":10000}

11. Agent finishes, exits with code 0.

12. Adapter reports clean termination to gateway.
```

### Tool Call and Result

```
Agent writes to stdout:
{"type":"tool_call","id":"tc_001","sessionId":"sess_abc","name":"read_file","arguments":{"path":"/workspace/slots/sess_abc/current/README.md"}}

Adapter reads file and writes to stdin:
{"type":"tool_result","id":"tc_001","sessionId":"sess_abc","content":[{"type":"text","inline":"# My Project\nThis is a sample project."}],"isError":false}
```

### Multiple Outstanding Tool Calls

```
Agent writes:
{"type":"tool_call","id":"tc_001","sessionId":"sess_abc","name":"read_file","arguments":{"path":"src/main.go"}}
{"type":"tool_call","id":"tc_002","sessionId":"sess_abc","name":"read_file","arguments":{"path":"go.mod"}}

Adapter may respond in any order:
{"type":"tool_result","id":"tc_002","sessionId":"sess_abc","content":[{"type":"text","inline":"module example.com/myapp\ngo 1.22"}],"isError":false}

A heartbeat may arrive between tool results:
{"type":"heartbeat","ts":1717430420}

Agent acks immediately:
{"type":"heartbeat_ack"}

Then the other result arrives:
{"type":"tool_result","id":"tc_001","sessionId":"sess_abc","content":[{"type":"text","inline":"package main\n..."}],"isError":false}
```

### Full-level checkpoint handshake

```
Adapter sends on CH-RUNTIMEOPS:
{"type":"checkpoint_request","sessionId":"sess_abc","checkpointId":"chk_42","deadlineMs":60000}

Runtime quiesces (flushes buffers, stops writing to workspace), then:
{"type":"checkpoint_ready","checkpointId":"chk_42"}

Adapter snapshots the workspace and sends:
{"type":"checkpoint_complete","sessionId":"sess_abc","checkpointId":"chk_42","status":"ok"}

Runtime resumes normal operation.
```

### Full-level interrupt handshake

```
Adapter sends on CH-RUNTIMEOPS:
{"type":"interrupt_request","sessionId":"sess_abc","interruptId":"int_7","deadlineMs":30000}

Runtime reaches a safe stop point (e.g., finishes current LLM call), then:
{"type":"interrupt_acknowledged","interruptId":"int_7"}

Session transitions to suspended state. Pod is held.
```

---

## Canonical artifacts

The adapter protocol is defined by the published schema artifacts the table below names. Runtime authors, and the adapter compliance suite (`lenny-ctl runtime verify`), validate against these files rather than the narrative prose in this guide.

| Artifact | Purpose | Canonical URL |
|:---------|:--------|:--------------|
| `lenny-adapter.proto` | gRPC service definition for the gateway ↔ adapter control plane (`Attach`, `SendMessage`, `Checkpoint`, `DemoteSDK`, etc.) and all associated message types. | `https://schemas.lenny.dev/adapter/v1/lenny-adapter.proto` |
| `lenny-adapter-jsonl.schema.json` | JSON Schema for the stdin/stdout JSON Lines frames exchanged between the adapter and the agent binary (`session_start`, `session_started`, `session_end`, `message`, `heartbeat`, `heartbeat_ack`, `shutdown`, `tool_call`, `tool_result`, `response`, `status`, and `set_tracing_context`). | `https://schemas.lenny.dev/adapter/v1/lenny-adapter-jsonl.schema.json` |
| `messagepart.schema.json` | JSON Schema for the structured `messageParts` field used in `agent_output` events and tool results (text, image, redaction, inline-file parts). | `https://schemas.lenny.dev/adapter/v1/messagepart.schema.json` |
| `runtime-ops-events.schema.json` | JSON Schema for the [CH-RUNTIMEOPS](#ch-runtimeops-full-level-only) frames a Full-level runtime and the adapter exchange over the runtime-operations Unix socket (capability handshake, checkpoint, interrupt, `credentials_rotated`, `deadline_approaching`, `files_updated`, and the LLM-request frames). | `https://schemas.lenny.dev/adapter/v1/runtime-ops-events.schema.json` |

Each artifact is versioned independently and distributed alongside every Lenny release under `/schemas/adapter/v1/` in the release bundle. Compliance is checked programmatically during `lenny-ctl runtime verify`, which returns structured diff output when a runtime's frames fail validation. Fix your runtime to produce valid frames rather than pinning an older schema version -- the schemas are stable within `v1`, and breaking changes bump the major version.
