---
layout: default
title: MCP API
parent: "API Reference"
nav_order: 2
---

# MCP API Reference

The MCP interface is Lenny's surface for interactive streaming sessions and recursive delegation. It exposes the gateway as an MCP server over **Streamable HTTP** via the built-in `MCPAdapter`.

Use the MCP API when you need:
- Real-time streaming output from agent sessions
- Recursive delegation (agents spawning sub-agents)
- Elicitation (human-in-the-loop input collection)
- Integration with MCP-native clients (Claude Desktop, Cursor, Cline, etc.)

For non-interactive operations (CI/CD, admin dashboards, batch workflows), use the [REST API](rest/index.html) instead. Both surfaces share the same session lifecycle and error taxonomy.

---

## Connection setup

### Endpoint

```
POST https://<gateway-host>/mcp
```

The MCP API uses the **Streamable HTTP** transport as defined in the MCP specification. All communication flows through HTTP POST requests to the `/mcp` endpoint.

### Authentication

Include an OIDC Bearer token in the `Authorization` header of every request:

```
Authorization: Bearer <oidc-token>
```

### Connection lifecycle

1. **Client sends `initialize` request** -- includes the client's supported MCP protocol version and client capabilities.
2. **Gateway responds with `initialize` result** -- confirms the negotiated protocol version, server capabilities, and available tools.
3. **Client sends `initialized` notification** -- signals readiness to begin tool calls.
4. **Tool calls and notifications** -- the client invokes tools and receives streaming responses via Streamable HTTP.

---

## Version negotiation

The `MCPAdapter` performs MCP protocol version negotiation during the `initialize` handshake.

### Supported versions

| MCP spec version | Status | Notes |
|:-----------------|:-------|:------|
| `2025-03-26` | **Current** | Latest stable. All MCP features used by Lenny target this version or later. |
| `2024-11-05` | **Previous** | Fully supported. Will enter deprecation when the next MCP spec version is adopted. |

### Negotiation flow

1. The client sends its supported MCP version in the `initialize` request (`protocolVersion` field).
2. The gateway responds with the **highest mutually supported version**.
3. Once negotiated, the connection is **pinned** to that version for its lifetime. Tool schemas, error formats, and streaming behavior conform to the negotiated version.

### Rejection handling

If the client's version is older than the oldest supported version, the gateway rejects the connection:

```json
{
  "error": {
    "code": "MCP_VERSION_UNSUPPORTED",
    "category": "PERMANENT",
    "message": "Client MCP version 2024-01-01 is not supported.",
    "retryable": false,
    "details": {
      "supportedVersions": ["2025-03-26", "2024-11-05"]
    }
  }
}
```

---

## Compatibility policy

### Two-version support

Lenny supports the **two most recent stable MCP spec versions** simultaneously. When a new MCP spec version is adopted, the oldest supported version enters a **6-month deprecation window**.

### Deprecation signals

The gateway emits an `X-Lenny-Mcp-Version-Deprecated` warning header on connections using the deprecated version.

### Session-lifetime exception

When a deprecated version exits the deprecation window:
- Version support removal applies only to **new** connection negotiations.
- Any connection that completed the `initialize` handshake before the deprecation deadline is permitted to continue for the duration of its session (up to `maxSessionAgeSeconds`).
- The gateway maintains a per-connection `negotiatedVersion` field used at message dispatch time.

---

## MCP features used

| Feature | Usage |
|:--------|:------|
| **Tasks** | Long-running session lifecycle management and delegation. Each Lenny session maps to an MCP Task. |
| **Elicitation** | User prompts, auth flows, and human-in-the-loop input collection. |
| **Streamable HTTP** | Transport layer for all MCP communication. |

---

## Tool catalog

The MCP server exposes the following tools. Each tool is described with its complete input schema, output schema, error codes, and a usage example.

---

### `create_session`

Create a new agent session. Claims a warm pod, assigns credentials, and prepares for workspace uploads.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `runtime` | string | Yes | Name of a registered runtime to use |
| `pool` | string | No | Specific pool to use (defaults to the runtime's default pool) |
| `workspacePlan` | object | No | Workspace configuration (sources, maxSizeMB, setup commands). See [WorkspacePlan schema](../reference/workspace-plan.html). |
| `labels` | object | No | Key-value labels for filtering and organization |
| `environment` | string | No | Environment name to scope the session |
| `callbackUrl` | string | No | Webhook URL for completion notification |
| `idempotencyKey` | string | No | Client-supplied key for idempotent creation |
| `dataResidencyRegion` | string | No | Required data residency region constraint |

**Output schema:**

```json
{
  "sessionId": "sess_01J5K9...",
  "uploadToken": "ut_abc123...",
  "sessionIsolationLevel": "gvisor",
  "status": "created",
  "runtime": "claude-worker",
  "pool": "default-pool",
  "createdAt": "2026-04-09T10:00:00Z",
  "expiresAt": "2026-04-09T10:05:00Z"
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `VALIDATION_ERROR` | Invalid parameters (missing runtime, bad workspace plan) |
| `RUNTIME_UNAVAILABLE` | No healthy pods for the requested runtime |
| `WARM_POOL_EXHAUSTED` | No idle pods in the warm pool |
| `QUOTA_EXCEEDED` | Tenant session quota exceeded |
| `CREDENTIAL_POOL_EXHAUSTED` | No credentials available |
| `CIRCUIT_BREAKER_OPEN` | Operator circuit breaker active |
| `ERASURE_IN_PROGRESS` | User has pending GDPR erasure |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "create_session",
    "arguments": {
      "runtime": "claude-worker",
      "labels": {
        "project": "backend-refactor",
        "priority": "high"
      },
      "workspacePlan": {
        "sources": [
          {
            "type": "git",
            "url": "https://github.com/myorg/myrepo.git",
            "ref": "main"
          }
        ],
        "maxSizeMB": 2048
      }
    }
  }
}
```

---

### `create_and_start_session`

Convenience tool that combines session creation, inline file upload, workspace finalization, and runtime start into a single call. Ideal for simple workflows that do not need fine-grained control over the setup stages.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `runtime` | string | Yes | Name of a registered runtime |
| `message` | string | Yes | Initial message to send to the agent |
| `pool` | string | No | Specific pool to use |
| `workspacePlan` | object | No | Workspace configuration |
| `files` | array | No | Inline files to upload. Each entry: `{ "path": "string", "content": "string", "encoding": "utf-8 \| base64" }` |
| `labels` | object | No | Key-value labels |
| `environment` | string | No | Environment name |
| `callbackUrl` | string | No | Webhook URL for completion notification |

**Output schema:**

Returns an MCP Task representing the running session:

```json
{
  "taskId": "task_01J5K9...",
  "sessionId": "sess_01J5K9...",
  "status": "running",
  "runtime": "claude-worker"
}
```

The task streams output as MCP Task progress notifications.

**Error codes:**

Same as `create_session`, plus:

| Code | When |
|:-----|:-----|
| `IMAGE_RESOLUTION_FAILED` | Runtime container image unresolvable |
| `POOL_DRAINING` | Target pool is draining |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "create_and_start_session",
    "arguments": {
      "runtime": "claude-worker",
      "message": "Refactor the authentication module to use OIDC.",
      "files": [
        {
          "path": "context.md",
          "content": "## Requirements\n- Use OIDC for all auth flows\n- Support token refresh",
          "encoding": "utf-8"
        }
      ],
      "labels": { "task": "auth-refactor" }
    }
  }
}
```

---

### `upload_files`

Upload workspace files to a session. Valid before finalization (state: `created`) and mid-session if the runtime declares `capabilities.midSessionUpload: true`.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |
| `files` | array | Yes | Files to upload. Each entry: `{ "path": "string", "content": "string", "encoding": "utf-8 \| base64" }` |

**Output schema:**

```json
{
  "uploaded": 3,
  "totalBytes": 24576,
  "paths": ["src/main.go", "go.mod", "go.sum"]
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `INVALID_STATE_TRANSITION` | Session already finalized or started (and mid-session upload not enabled) |
| `UPLOAD_TOKEN_EXPIRED` | Upload token TTL elapsed |
| `STORAGE_QUOTA_EXCEEDED` | Workspace size limit exceeded |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "upload_files",
    "arguments": {
      "sessionId": "sess_01J5K9...",
      "files": [
        {
          "path": "src/main.go",
          "content": "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n",
          "encoding": "utf-8"
        }
      ]
    }
  }
}
```

---

### `finalize_workspace`

Seal the workspace and run setup commands. After finalization, no further file uploads are accepted (unless mid-session upload is enabled).

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |

**Output schema:**

```json
{
  "sessionId": "sess_01J5K9...",
  "status": "ready",
  "setupOutput": "npm install completed (12 packages)\n",
  "workspaceSizeMB": 156
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `INVALID_STATE_TRANSITION` | Session not in `created` state |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "finalize_workspace",
    "arguments": {
      "sessionId": "sess_01J5K9..."
    }
  }
}
```

---

### `start_session`

Start the agent runtime. The session must be in `ready` state (workspace finalized).

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |
| `message` | string | No | Initial message to send to the agent upon start |

**Output schema:**

Returns an MCP Task:

```json
{
  "taskId": "task_01J5K9...",
  "sessionId": "sess_01J5K9...",
  "status": "running"
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `INVALID_STATE_TRANSITION` | Session not in `ready` state |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "start_session",
    "arguments": {
      "sessionId": "sess_01J5K9...",
      "message": "Analyze the codebase and identify performance bottlenecks."
    }
  }
}
```

---

### `attach_session`

Attach to a running session and receive its streaming output. Returns an MCP Task that streams agent output in real time.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |

**Output schema:**

Returns an MCP Task with streaming progress notifications:

```json
{
  "taskId": "task_01J5K9...",
  "sessionId": "sess_01J5K9...",
  "status": "running"
}
```

Progress notifications stream as MCP Task updates containing `OutputPart` arrays translated to MCP content blocks.

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `INVALID_STATE_TRANSITION` | Session is in a terminal state |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "attach_session",
    "arguments": {
      "sessionId": "sess_01J5K9..."
    }
  }
}
```

---

### `send_message`

Send a message to a running or suspended session. This is the unified message delivery endpoint -- it replaces the earlier `send_prompt` tool.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |
| `message` | string | Yes | Message content to send |
| `delivery` | string | No | `"immediate"` (interrupt and deliver) or `"queued"` (buffer for next pause). Default: `"queued"`. |
| `inReplyTo` | string | No | Message ID this is replying to. If it matches an outstanding `request_input`, resolves that call directly. |
| `threadId` | string | No | Thread identifier (v1: one implicit thread per session) |

**Output schema:**

```json
{
  "messageId": "msg_xyz789",
  "status": "delivered",
  "deliveredAt": "2026-04-09T10:05:30Z"
}
```

Possible `status` values: `delivered`, `queued`, `dropped`, `expired`, `rate_limited`, `error`.

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `INJECTION_REJECTED` | Runtime has `injection.supported: false` |
| `SCOPE_DENIED` | Messaging scope denies target session |
| `TARGET_TERMINAL` | Session is in a terminal state |
| `DUPLICATE_MESSAGE_ID` | Sender-supplied message ID already exists |
| `INVALID_DELIVERY_VALUE` | Unknown `delivery` value |
| `TARGET_NOT_READY` | Session is in a pre-running state with no inbox |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "send_message",
    "arguments": {
      "sessionId": "sess_01J5K9...",
      "message": "Focus on the database query optimization first.",
      "delivery": "immediate"
    }
  }
}
```

---

### `interrupt_session`

Interrupt the agent's current work. Valid only when the session is in `running` state.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |

**Output schema:**

```json
{
  "sessionId": "sess_01J5K9...",
  "status": "suspended",
  "interruptedAt": "2026-04-09T10:10:00Z"
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `INVALID_STATE_TRANSITION` | Session not in `running` state |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "interrupt_session",
    "arguments": {
      "sessionId": "sess_01J5K9..."
    }
  }
}
```

---

### `get_session_status`

Query the current state of a session, including whether it is suspended, awaiting client action, or running.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |

**Output schema:**

```json
{
  "sessionId": "sess_01J5K9...",
  "status": "running",
  "runtime": "claude-worker",
  "pool": "default-pool",
  "labels": { "project": "backend-refactor" },
  "createdAt": "2026-04-09T10:00:00Z",
  "startedAt": "2026-04-09T10:01:00Z",
  "tokenUsage": {
    "input": 12500,
    "output": 8300
  }
}
```

Possible `status` values: `created`, `finalizing`, `ready`, `starting`, `running`, `suspended`, `resume_pending`, `awaiting_client_action`, `completed`, `failed`, `cancelled`, `expired`.

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "get_session_status",
    "arguments": {
      "sessionId": "sess_01J5K9..."
    }
  }
}
```

---

### `get_task_tree`

Get the delegation tree for a session. Shows parent-child relationships, delegation depth, and task status for all nodes in the tree.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Root session ID (or any node in the tree) |

**Output schema:**

```json
{
  "rootSessionId": "sess_01J5K9...",
  "nodes": [
    {
      "sessionId": "sess_01J5K9...",
      "parentSessionId": null,
      "runtime": "claude-worker",
      "status": "running",
      "delegationDepth": 0,
      "children": ["sess_02K6L0...", "sess_03M7N1..."]
    },
    {
      "sessionId": "sess_02K6L0...",
      "parentSessionId": "sess_01J5K9...",
      "runtime": "code-runner",
      "status": "completed",
      "delegationDepth": 1,
      "children": []
    }
  ],
  "totalNodes": 3,
  "maxDepth": 2
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "get_task_tree",
    "arguments": {
      "sessionId": "sess_01J5K9..."
    }
  }
}
```

---

### `get_session_logs`

Get session logs with pagination support.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |
| `cursor` | string | No | Pagination cursor from a previous response |
| `limit` | integer | No | Number of log entries per page (default: 50, max: 200) |
| `level` | string | No | Filter by log level: `debug`, `info`, `warn`, `error` |

**Output schema:**

```json
{
  "items": [
    {
      "timestamp": "2026-04-09T10:01:05Z",
      "level": "info",
      "message": "Agent started, reading initial prompt",
      "source": "runtime"
    },
    {
      "timestamp": "2026-04-09T10:01:10Z",
      "level": "info",
      "message": "Tool call: read_file /workspace/src/main.go",
      "source": "adapter"
    }
  ],
  "cursor": "eyJ0cyI6MTcx...",
  "hasMore": true
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `VALIDATION_ERROR` | Invalid cursor or limit |

---

### `get_token_usage`

Get token usage for a session. When the session has a delegation tree, returns tree-aggregated usage including all descendant tasks.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |

**Output schema:**

```json
{
  "sessionId": "sess_01J5K9...",
  "tokens": {
    "input": 45000,
    "output": 22000,
    "total": 67000
  },
  "treeAggregated": true,
  "descendants": 3,
  "podMinutes": 12.5
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |

---

### `list_artifacts`

List artifacts produced by a session.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |
| `cursor` | string | No | Pagination cursor |
| `limit` | integer | No | Items per page (default: 50, max: 200) |

**Output schema:**

```json
{
  "items": [
    {
      "path": "output/report.md",
      "sizeBytes": 4096,
      "mimeType": "text/markdown",
      "createdAt": "2026-04-09T10:15:00Z"
    },
    {
      "path": "output/chart.png",
      "sizeBytes": 102400,
      "mimeType": "image/png",
      "createdAt": "2026-04-09T10:15:30Z"
    }
  ],
  "cursor": null,
  "hasMore": false,
  "total": 2
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |

---

### `download_artifact`

Download a specific artifact from a session.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |
| `path` | string | Yes | Artifact path (from `list_artifacts`) |

**Output schema:**

Returns the artifact content as an MCP resource with the appropriate MIME type. For text files, inline content is returned. For binary files, base64-encoded content is returned.

```json
{
  "content": "# Performance Report\n\n## Summary\n...",
  "mimeType": "text/markdown",
  "sizeBytes": 4096
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session or artifact does not exist |

---

### `terminate_session`

End a session. Valid in any non-terminal state. Triggers graceful shutdown of the agent runtime.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |

**Output schema:**

```json
{
  "sessionId": "sess_01J5K9...",
  "status": "completed",
  "terminatedAt": "2026-04-09T10:20:00Z"
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `TARGET_TERMINAL` | Session already in a terminal state |

---

### `resume_session`

Resume a suspended or paused session. Behavior depends on the session state:
- `suspended`: Resumes the agent runtime.
- `awaiting_client_action`: Triggers pod re-acquisition and session restoration.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `sessionId` | string | Yes | Target session ID |

**Output schema:**

```json
{
  "sessionId": "sess_01J5K9...",
  "status": "running",
  "resumedAt": "2026-04-09T10:25:00Z"
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `RESOURCE_NOT_FOUND` | Session does not exist |
| `INVALID_STATE_TRANSITION` | Session not in a resumable state |

---

### `list_sessions`

List active and recent sessions with filtering support.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `status` | string | No | Filter by status: `running`, `suspended`, `completed`, etc. |
| `runtime` | string | No | Filter by runtime name |
| `labels` | object | No | Filter by label key-value pairs |
| `cursor` | string | No | Pagination cursor |
| `limit` | integer | No | Items per page (default: 50, max: 200) |
| `sort` | string | No | Sort field and direction (default: `created_at:desc`) |

**Output schema:**

```json
{
  "items": [
    {
      "sessionId": "sess_01J5K9...",
      "status": "running",
      "runtime": "claude-worker",
      "pool": "default-pool",
      "labels": { "project": "backend-refactor" },
      "createdAt": "2026-04-09T10:00:00Z"
    }
  ],
  "cursor": "eyJpZCI6IjAx...",
  "hasMore": true,
  "total": 47
}
```

**Error codes:**

| Code | When |
|:-----|:-----|
| `VALIDATION_ERROR` | Invalid filter parameters or cursor |

---

### `list_runtimes`

List available runtimes. Results are **identity-filtered** and **policy-scoped** -- users only see runtimes they have access to via their tenant and environment memberships.

**Input schema:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `labels` | object | No | Filter by label key-value pairs |
| `environment` | string | No | Filter by environment scope |
| `cursor` | string | No | Pagination cursor |
| `limit` | integer | No | Items per page (default: 50, max: 200) |

**Output schema:**

```json
{
  "items": [
    {
      "name": "claude-worker",
      "type": "agent",
      "executionMode": "session",
      "agentInterface": {
        "injection": { "supported": true },
        "capabilities": { "midSessionUpload": false }
      },
      "capabilities": ["delegation", "elicitation", "checkpoint"],
      "labels": { "provider": "anthropic" },
      "adapterCapabilities": {
        "supportsElicitation": true,
        "supportsDelegation": true,
        "supportsInterrupt": true,
        "supportsSessionContinuity": true
      }
    }
  ],
  "cursor": null,
  "hasMore": false,
  "total": 3
}
```

For `type: mcp` runtimes, the response also includes `mcpEndpoint` and `mcpCapabilities.tools`.

**Error codes:**

| Code | When |
|:-----|:-----|
| `VALIDATION_ERROR` | Invalid filter parameters |

---

## REST/MCP consistency contract

The REST API and MCP tools intentionally overlap for operations like session creation, status queries, and artifact retrieval. Five rules govern this overlap:

### 1. Semantic equivalence

REST and MCP endpoints that perform the same operation return **semantically identical** responses. Both surfaces share a common service layer in the gateway.

### 2. Shared error taxonomy

All error responses use the same error categories (`TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM`) and error codes. MCP tool errors use the same `code` and `category` fields inside the MCP error response format.

### 3. OpenAPI as source of truth

MCP tool schemas for overlapping operations are **generated from the OpenAPI spec**, not maintained independently. This keeps the schemas structurally consistent.

### 4. Contract testing

CI includes contract tests that call the REST endpoint and every built-in adapter (MCP, OpenAI Completions, Open Responses) for every overlapping operation and assert both structural and behavioral equivalence:

- Identical response payloads (modulo transport envelope)
- Same error codes for identical invalid inputs
- Same authorization behavior
- Identical `retryable` and `category` flags across surfaces
- Same session state transitions for identical operation sequences
- Same pagination behavior for list operations

### 5. REST-only operations

The following REST endpoints have **no MCP tool equivalents**: `derive`, `replay`, `extend-retention`, and `eval`. These are developer workflow operations typically driven by CI pipelines or human operators, not by agents mid-session. MCP clients needing these operations should use the REST API directly.

---

## Task lifecycle mapping

Lenny sessions map to MCP Tasks as follows:

| Lenny session state | MCP Task state |
|:-------------------|:---------------|
| `created` | Task created (not yet started) |
| `finalizing` | Task in progress |
| `ready` | Task in progress (setup complete) |
| `starting` | Task in progress |
| `running` | Task in progress (streaming output) |
| `suspended` | Task paused |
| `resume_pending` | Task in progress (resuming) |
| `awaiting_client_action` | Task requires input |
| `completed` | Task completed |
| `failed` | Task failed |
| `cancelled` | Task cancelled |
| `expired` | Task failed (lease/budget exhausted) |

When a session transitions to `running`, the attached MCP Task streams `OutputPart` arrays translated to MCP content blocks (TextContent, ImageContent, ResourceContent) using the [Translation Fidelity Matrix](internal.html#translation-fidelity-matrix) rules.

Elicitation requests from the agent are surfaced as MCP Elicitation prompts, enabling the client to collect human input and relay it back to the session.
