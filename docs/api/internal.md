---
layout: default
title: Internal gRPC API
parent: "API Reference"
nav_order: 6
---

# Internal gRPC API Reference

{: .warning }
> **Audience: Runtime adapter authors only.** These are internal APIs for gateway-to-pod communication. They are not for external clients. Unlike the stable external APIs, internal APIs may change between versions.

The gateway communicates with agent pods over **gRPC + mTLS**. This page documents the protobuf service definitions, lifecycle channel messages, connection semantics, and error handling that runtime adapter authors need to implement.

---

## Architecture overview

```
┌────────────────────┐         gRPC + mTLS         ┌─────────────────────┐
│                    │ ◄──────────────────────────► │                     │
│   Gateway Replica  │                              │   Agent Pod         │
│                    │   RuntimeAdapter service     │  ┌───────────────┐  │
│   Session Router   │ ──── StartSession ────────►  │  │Runtime Adapter│  │
│                    │ ──── StopSession ─────────►  │  ├───────────────┤  │
│                    │ ──── Attach (bidi) ───────►  │  │ Agent Binary  │  │
│                    │ ──── Checkpoint ──────────►  │  └───────────────┘  │
│                    │ ──── UploadFiles ─────────►  │                     │
│                    │ ──── DemoteSDK ───────────►  │                     │
│                    │                              │                     │
│                    │   Health service             │                     │
│                    │ ──── Check ───────────────►  │                     │
│                    │ ──── Watch ───────────────►  │                     │
└────────────────────┘                              └─────────────────────┘
```

---

## mTLS requirements

All gateway-to-pod communication is protected by mutual TLS (mTLS).

### Certificate details

| Component | Certificate TTL | SAN format | Rotation |
|:----------|:---------------|:-----------|:---------|
| Gateway replicas | 24h | DNS: `lenny-gateway.lenny-system.svc` | cert-manager auto-renewal at 2/3 lifetime |
| Agent pods | 4h | SPIFFE URI: `spiffe://<trust-domain>/agent/{pool}/{pod-name}` | cert-manager auto-renewal; pod restart if renewal fails |

### Identity verification

- The gateway validates the pod's **SPIFFE URI** against the expected pool and pod name on each connection.
- Each gateway replica gets a **distinct certificate** so compromise of one replica can be detected and revoked independently.
- Pods cannot forge or extend session JWTs. The gateway validates JWT signatures on every pod-to-gateway request.

### Trust domain

The SPIFFE trust domain is configurable via `global.spiffeTrustDomain` Helm value (default: `lenny`). Deployers **must** override this in any environment where multiple Lenny instances share the same Kubernetes cluster and CA.

---

## Protobuf service definitions

### RuntimeAdapter service

The `RuntimeAdapter` service is the primary interface between the gateway and agent pods.

```protobuf
service RuntimeAdapter {
  // StartSession assigns a session to the pod and begins the agent runtime.
  // Called after workspace materialization is complete.
  rpc StartSession(StartSessionRequest) returns (StartSessionResponse);

  // StopSession gracefully terminates the current session.
  // The adapter sends shutdown to the agent binary and waits for exit.
  rpc StopSession(StopSessionRequest) returns (StopSessionResponse);

  // Attach opens a bidirectional stream for real-time communication
  // between the gateway and the running agent session.
  rpc Attach(stream AttachMessage) returns (stream AttachMessage);

  // Checkpoint triggers a workspace snapshot for session recovery.
  rpc Checkpoint(CheckpointRequest) returns (CheckpointResponse);

  // UploadFiles delivers workspace files to the pod.
  // Called during workspace materialization or mid-session upload.
  rpc UploadFiles(stream UploadChunk) returns (UploadResponse);

  // DemoteSDK terminates the pre-connected SDK process and returns
  // the pod to pod-warm state. Required for SDK-warm pools when
  // workspace files match sdkWarmBlockingPaths.
  rpc DemoteSDK(DemoteSDKRequest) returns (DemoteSDKResponse);
}
```

#### StartSession

Assigns a session to the pod. Called after workspace files have been uploaded and finalized.

**Request:**

```protobuf
message StartSessionRequest {
  string session_id = 1;
  string runtime_name = 2;
  SessionConfig config = 3;  // runtime-specific configuration
  CredentialSet credentials = 4;  // assigned credential leases
  map<string, string> labels = 5;
  string execution_mode = 6;  // "session", "task", "concurrent-workspace"
}
```

**Response:**

```protobuf
message StartSessionResponse {
  bool success = 1;
  string error_code = 2;  // set when success is false
  string error_message = 3;
}
```

**Error codes:**

| Code | Description |
|:-----|:------------|
| `ALREADY_ACTIVE` | Pod already has an active session |
| `WORKSPACE_NOT_READY` | Workspace materialization not complete |
| `RUNTIME_START_FAILED` | Agent binary failed to start |
| `CREDENTIAL_INVALID` | Credential material is invalid or expired |

#### StopSession

Gracefully terminates the current session.

**Request:**

```protobuf
message StopSessionRequest {
  string session_id = 1;
  string reason = 2;  // "client_terminate", "drain", "lease_expired", "budget_exhausted"
  int32 deadline_ms = 3;  // time the agent has to finish (default: 10000)
}
```

**Response:**

```protobuf
message StopSessionResponse {
  bool clean_exit = 1;  // true if agent exited within deadline
  int32 exit_code = 2;
  string final_output = 3;  // last response from agent, if any
}
```

#### Attach (bidirectional stream)
{: #attach-rpc }

Opens a bidirectional stream for real-time communication. This is the primary data channel during an active session.

**Stream message format:**

```protobuf
message AttachMessage {
  oneof payload {
    // Gateway → Pod
    MessageEnvelope message = 1;      // deliver content to agent
    InterruptSignal interrupt = 2;     // interrupt current work
    CredentialRotation credential = 3; // mid-session credential rotation

    // Pod → Gateway
    AgentOutput output = 4;           // streaming agent output (OutputPart[])
    ToolCallRequest tool_call = 5;    // agent requests tool execution
    StatusUpdate status = 6;          // agent status change
    HeartbeatAck heartbeat_ack = 7;   // heartbeat acknowledgment
  }
}
```

**Streaming semantics:**

- The gateway opens the Attach stream after `StartSession` succeeds.
- Messages flow in both directions concurrently.
- The gateway sends `MessageEnvelope` objects containing `OutputPart` arrays as input.
- The pod sends `AgentOutput` objects containing `OutputPart` arrays as streaming output.
- The stream remains open for the duration of the session.
- If the stream is interrupted (network partition, pod restart), the gateway attempts reconnection. The pod must accept a new Attach stream for an in-progress session.

**Heartbeat protocol:**

- The gateway sends periodic heartbeat pings on the Attach stream.
- The adapter must respond with `HeartbeatAck` within **10 seconds**.
- Failure to ack triggers SIGTERM to the agent process.
- Heartbeat interval is configurable (default: 30 seconds).

#### Checkpoint

Triggers a workspace snapshot for session recovery.

**Request:**

```protobuf
message CheckpointRequest {
  string session_id = 1;
  string checkpoint_id = 2;  // gateway-assigned unique ID
  string consistency = 3;    // "best-effort" (Standard) or "consistent" (Full)
}
```

**Response:**

```protobuf
message CheckpointResponse {
  bool success = 1;
  string checkpoint_id = 2;
  int64 size_bytes = 3;  // checkpoint size
  string error_code = 4;
  string error_message = 5;
}
```

For `consistency: "consistent"` (Full tier), the adapter first sends a `checkpoint_request` on the lifecycle channel, waits for `checkpoint_ready` from the runtime, performs the snapshot, then sends `checkpoint_complete`. See [Lifecycle channel messages](#lifecycle-channel-messages) below.

For `consistency: "best-effort"` (Standard tier), the adapter takes the snapshot immediately without pausing the runtime. Minor workspace inconsistencies are possible.

#### UploadFiles

Delivers workspace files to the pod as a stream of chunks.

**Request stream:**

```protobuf
message UploadChunk {
  string path = 1;          // workspace-relative path
  bytes data = 2;           // file content chunk
  bool is_last_chunk = 3;   // true for the final chunk of a file
  int32 permissions = 4;    // Unix file permissions (e.g., 0644)
  string session_id = 5;    // target session
}
```

**Response:**

```protobuf
message UploadResponse {
  int32 files_received = 1;
  int64 total_bytes = 2;
  repeated string paths = 3;  // successfully written paths
  repeated UploadError errors = 4;
}

message UploadError {
  string path = 1;
  string error = 2;
}
```

#### DemoteSDK

Terminates the pre-connected SDK process and returns the pod to pod-warm state. Required when the pool uses SDK-warm mode (`preConnect: true`) and workspace files match `sdkWarmBlockingPaths`.

**Request:**

```protobuf
message DemoteSDKRequest {
  string session_id = 1;
}
```

**Response:**

```protobuf
message DemoteSDKResponse {
  bool success = 1;
  string error_code = 2;  // "UNIMPLEMENTED" if adapter doesn't support demotion
  int32 demotion_time_ms = 3;  // time taken to demote
}
```

**Timeout:** Default 10 seconds. If the SDK process does not exit within this window, the adapter sends SIGKILL.

---

### Health service

The adapter implements the **gRPC Health Checking Protocol** (standard `grpc.health.v1.Health` service).

```protobuf
service Health {
  // Check returns the current serving status.
  rpc Check(HealthCheckRequest) returns (HealthCheckResponse);

  // Watch streams status changes.
  rpc Watch(HealthCheckRequest) returns (stream HealthCheckResponse);
}
```

**Serving status values:**

| Status | Description |
|:-------|:------------|
| `SERVING` | Adapter is ready to accept sessions |
| `NOT_SERVING` | Adapter is not ready (starting up or shutting down) |
| `SERVICE_UNKNOWN` | Service name not recognized |

The gateway uses `Watch` to monitor pod health continuously and `Check` for point-in-time health probes during pod warming.

---

## Lifecycle channel messages (Full tier only)
{: #lifecycle-channel-messages }

Full-tier runtimes open a **lifecycle channel** -- an abstract Unix socket (`@lenny-lifecycle`) -- for operational signals that require runtime cooperation. The lifecycle channel runs alongside the stdin/stdout binary protocol and the MCP connections.

### Channel setup

1. The adapter opens the lifecycle channel socket.
2. The adapter sends `lifecycle_capabilities` listing available signals.
3. The runtime responds with `lifecycle_support` listing capabilities it supports.
4. The channel remains open for the session duration.

### Messages

#### checkpoint_request (gateway to runtime)

Requests the runtime to quiesce for a consistent checkpoint.

```json
{
  "type": "checkpoint_request",
  "checkpointId": "ckpt_01J5K9..."
}
```

#### checkpoint_ready (runtime to gateway)

Acknowledges quiescence -- the runtime has flushed buffers and reached a safe state.

```json
{
  "type": "checkpoint_ready",
  "checkpointId": "ckpt_01J5K9..."
}
```

#### checkpoint_complete (gateway to runtime)

Signals that the snapshot is complete and the runtime may resume.

```json
{
  "type": "checkpoint_complete",
  "checkpointId": "ckpt_01J5K9..."
}
```

#### interrupt_request (gateway to runtime)

Requests the runtime to reach a safe stop point.

```json
{
  "type": "interrupt_request",
  "interruptId": "int_01J5K9..."
}
```

The runtime should finish its current operation, save any necessary state, and acknowledge:

```json
{
  "type": "interrupt_acknowledged",
  "interruptId": "int_01J5K9..."
}
```

#### credentials_rotated (gateway to runtime)

Notifies the runtime that credentials have been rotated in place.

```json
{
  "type": "credentials_rotated",
  "leaseId": "lease_01J5K9...",
  "provider": "anthropic",
  "credentialsPath": "/run/lenny/credentials/anthropic.json"
}
```

The runtime should reload credentials and acknowledge:

```json
{
  "type": "credentials_acknowledged",
  "leaseId": "lease_01J5K9...",
  "provider": "anthropic"
}
```

#### deadline_approaching (gateway to runtime)

Warning signal before forced session termination.

```json
{
  "type": "deadline_approaching",
  "remainingMs": 60000
}
```

The runtime should begin wrapping up long-running work.

#### terminate (gateway to runtime)

Ordered shutdown signal.

```json
{
  "type": "terminate",
  "reason": "drain",
  "deadlineMs": 10000
}
```

The runtime must exit within `deadlineMs`.

---

## Connection lifecycle

### How the gateway connects to pods

1. **Pod warming:** The controller creates a pod. The adapter starts, opens a gRPC connection to the gateway (mTLS), and writes a placeholder manifest.
2. **Version negotiation:** The adapter sends `AdapterInit` with `adapterProtocolVersion` (semver, e.g., `"1.0.0"`). The gateway responds with `AdapterInitAck` carrying `selectedVersion` or closes with `PROTOCOL_VERSION_INCOMPATIBLE`.
3. **Readiness signal:** The adapter signals `READY` via the Health service. The pod enters the warm pool.
4. **Session assignment:** The gateway calls `StartSession`. The adapter transitions to `ACTIVE`.
5. **Active session:** The gateway opens an `Attach` bidirectional stream. All content flows through this stream.
6. **Session end:** The gateway calls `StopSession` or the agent exits naturally. The adapter transitions to `TERMINATED`.

### Reconnection

If the `Attach` stream is interrupted during an active session:

1. The gateway detects the stream break via heartbeat timeout.
2. For Full-tier sessions with checkpoint support, the gateway may attempt to resume on the same pod (if the pod is still healthy) by opening a new `Attach` stream.
3. If the pod is unhealthy, the gateway claims a new pod, restores from the last checkpoint, and starts a new session.
4. For Minimum/Standard-tier sessions without checkpoint support, pod failure results in session failure.

---

## RPC lifecycle state machine

```
INIT ──► READY ──► ACTIVE ──► DRAINING ──► TERMINATED
                     │                          ▲
                     └──────────────────────────┘
                       (session ends normally)
```

| State | Description |
|:------|:------------|
| `INIT` | Adapter starts, opens gRPC connection, sends `AdapterInit` with protocol version |
| `READY` | Adapter signals readiness. Pod enters warm pool. Gateway may assign sessions. |
| `ACTIVE` | Session in progress. Adapter manages MCP servers, lifecycle channel, stdin/stdout. |
| `DRAINING` | Graceful shutdown requested. Finishes current exchange, signals agent to stop. |
| `TERMINATED` | Adapter has exited. Gateway marks pod as unavailable. |

Transitions are initiated by the gateway (session assignment, drain request) or the adapter (readiness signal, exit on completion).

---

## Error codes and handling

### gRPC status codes

| gRPC code | When used |
|:----------|:----------|
| `OK` | RPC completed successfully |
| `UNAVAILABLE` | Pod is not ready or is shutting down. Gateway retries. |
| `NOT_FOUND` | Session ID not found on this pod |
| `ALREADY_EXISTS` | Session already active on this pod |
| `DEADLINE_EXCEEDED` | RPC timed out |
| `UNIMPLEMENTED` | RPC not supported (e.g., `DemoteSDK` on an adapter that does not support it) |
| `INTERNAL` | Unexpected adapter error |
| `FAILED_PRECONDITION` | Operation not valid in current state |
| `RESOURCE_EXHAUSTED` | Pod resources exhausted (disk, memory) |

### Adapter protocol version

Current protocol version: `"1.0.0"`.

- **Major version changes** are breaking -- the gateway will not connect to an adapter with an incompatible major version.
- **Minor/patch changes** are backwards compatible.

The gateway logs a warning if the adapter's protocol version is older than the gateway's preferred version but still within the compatible range.

---

## Agent binary protocol summary

The runtime adapter communicates with the agent binary over **stdin/stdout** using newline-delimited JSON (JSON Lines). This is separate from the gRPC interface -- gRPC is gateway-to-adapter; stdin/stdout is adapter-to-binary.

### Inbound messages (adapter to binary via stdin)

| Type | Description |
|:-----|:------------|
| `message` | All content delivery (initial task, injection, replies) |
| `tool_result` | Result of a tool call requested by the agent |
| `heartbeat` | Liveness ping; agent must respond with `heartbeat_ack` |
| `shutdown` | Graceful shutdown signal |

### Outbound messages (binary to adapter via stdout)

| Type | Description |
|:-----|:------------|
| `response` | Complete or streamed response with `OutputPart[]` |
| `tool_call` | Agent requests tool execution |
| `heartbeat_ack` | Acknowledges heartbeat |
| `status` | Optional status/trace update |

### Exit codes

| Code | Meaning |
|:-----|:--------|
| `0` | Normal completion |
| `1` | Runtime error |
| `2` | Protocol error (could not parse messages) |
| `137` | SIGKILL (pod not reused) |

For the complete binary protocol specification, including `OutputPart` format, `MessageEnvelope` schema, and tier-specific behavior, see the technical design document Section 15.4.

---

## Translation fidelity matrix
{: #translation-fidelity-matrix }

Each external protocol adapter translates between `OutputPart` (Lenny's internal content model) and its wire format. The fidelity of this translation varies by adapter:

| Tag | Meaning |
|:----|:--------|
| `[exact]` | Field round-trips with no information loss |
| `[lossy]` | Field is representable but some information is lost |
| `[dropped]` | Field has no representation in the target protocol |

**Key asymmetries:**

- `schemaVersion` is **dropped** by MCP, OpenAI Completions, and Open Responses adapters (reconstructed as `1` on ingest).
- `ref` (`lenny-blob://` URIs) are **dropped** by all adapters except REST -- adapters dereference blobs and inline the content before sending to external clients.
- `annotations` are **dropped** by OpenAI Completions and Open Responses adapters.
- `parts` nesting is **dropped** by OpenAI Completions and Open Responses (flattened to sequential entries).

The REST adapter provides `[exact]` round-trip fidelity for all fields. Callers that require full fidelity should use the REST API.
