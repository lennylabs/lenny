---
layout: default
title: "Platform MCP Tools"
parent: "Runtime Author Guide"
nav_order: 6
---

# Platform MCP Tools

The platform MCP server exposes 11 tools to agent runtimes at Standard and Full tier. These tools are available via the abstract Unix socket specified in the adapter manifest (`platformMcpServer.socket`). You connect to this server using an MCP client library and present the `mcpNonce` from the manifest during the `initialize` handshake.

---

## Connection Setup

Before calling any platform tool, connect to the platform MCP server:

1. Read `/run/lenny/adapter-manifest.json`.
2. Extract `platformMcpServer.socket` (e.g., `@lenny-platform-mcp`) and `mcpNonce`.
3. Connect via abstract Unix socket.
4. Send MCP `initialize` with the nonce:

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

5. Call `tools/list` to discover available tools.

---

## Tool Reference

### `lenny/delegate_task`

Spawn a child session on another runtime. The target is opaque --- your runtime does not know whether it is a standalone runtime, derived runtime, or external agent.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `target` | string | Yes | Opaque runtime identifier (discovered via `lenny/discover_agents`) |
| `task` | object | Yes | Task specification containing input and optional file exports |
| `task.input` | OutputPart[] | Yes | Input content for the child session |
| `task.workspaceFiles` | object | No | File export specification |
| `task.workspaceFiles.export` | array | No | Array of `{glob, destPrefix}` entries defining which parent workspace files to include in the child's workspace |
| `lease_slice` | object | No | Budget allocation from parent to child |
| `lease_slice.maxTokenBudget` | int | No | Token budget for the child tree |
| `lease_slice.maxChildrenTotal` | int | No | Max children the child may spawn |
| `lease_slice.maxTreeSize` | int | No | Contribution limit toward the tree-wide pod cap |
| `lease_slice.maxParallelChildren` | int | No | Max concurrent children for the child |
| `lease_slice.perChildMaxAge` | int | No | Max wall-clock seconds for the child |

**Returns:** `TaskHandle` with `taskId` and `sessionId`.

**Errors:**

| Code | Meaning |
|------|---------|
| `BUDGET_EXHAUSTED` | Token budget, tree size, children total, parallel children, or tree memory limit exceeded |
| `DELEGATION_CYCLE_DETECTED` | Target runtime appears in the caller's delegation lineage |
| `ISOLATION_MONOTONICITY_VIOLATED` | Target has weaker isolation than parent |
| `CREDENTIAL_POOL_EXHAUSTED` | No credential available for the child |
| `INPUT_TOO_LARGE` | `task.input` exceeds `contentPolicy.maxInputSize` |
| `target_not_an_agent` | Target is a `type: mcp` runtime (not delegatable) |

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/delegate_task",
    "arguments": {
      "target": "code-reviewer",
      "task": {
        "input": [
          { "type": "text", "inline": "Review this code for security issues." }
        ],
        "workspaceFiles": {
          "export": [
            { "glob": "src/**/*.go", "destPrefix": "src/" }
          ]
        }
      },
      "lease_slice": {
        "maxTokenBudget": 100000,
        "perChildMaxAge": 600
      }
    }
  }
}
```

---

### `lenny/await_children`

Wait for one or more child sessions to reach a terminal state. This is a streaming call --- it yields partial results when children enter `input_required` or complete.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `child_ids` | string[] | Yes | Session IDs of children to wait for |
| `mode` | string | Yes | `all` (wait for all), `any` (first to finish), or `settled` (same as `all`) |

**Returns:** Streaming `TaskResult` objects.

**Streaming Events:**

| Event | Description |
|-------|-------------|
| Terminal result | Child reached `completed`, `failed`, `cancelled`, or `expired` |
| `input_required` | Child is blocked in `lenny/request_input` --- respond via `lenny/send_message` with `inReplyTo` |
| `request_input_expired` | Child's `request_input` timed out |
| `deadlock_detected` | All tasks in a subtree are blocked --- respond or cancel to break the deadlock |

**Example (multi-child with input_required):**

```
Parent calls: lenny/await_children(["child_A", "child_B"], mode="all")

← stream: { childId: "child_A", state: "input_required",
             requestId: "req_001", parts: [...] }

Parent calls: lenny/send_message(target: "child_A",
               inReplyTo: "req_001", parts: [...])

← stream: { childId: "child_B", state: "completed", output: {...} }
← stream: { childId: "child_A", state: "completed", output: {...} }
← stream closes (all settled)
```

**Behavior notes:**

- `any` mode: returns as soon as **any** child reaches a terminal state. Remaining children continue running --- cancel them explicitly with `lenny/cancel_child` if desired.
- The stream remains open across multiple `input_required` events. You do not need to reopen it.
- `deadlock_detected` events carry a `willTimeoutAt` timestamp. Resolve the deadlock before that time or the deepest blocked tasks will fail with `DEADLOCK_TIMEOUT`.

---

### `lenny/cancel_child`

Cancel a child session and its descendants.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `child_id` | string | Yes | Session ID of the child to cancel |

**Returns:** Acknowledgement. The child receives a cancellation signal and transitions to `cancelled`.

**Cascade behavior:** Cancellation cascades to all descendants of the cancelled child, applying each node's `cascadeOnFailure` policy.

---

### `lenny/discover_agents`

List available delegation targets, filtered by the calling session's effective delegation policy.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `filter` | object | No | Optional filter criteria |
| `filter.labels` | object | No | Label selectors to match |
| `filter.type` | string | No | Filter by runtime type (only `agent` types are returned) |

**Returns:** Array of agent descriptors with `name`, `description`, `labels`, and `capabilities`.

**Notes:**

- Only returns `type: agent` runtimes --- `type: mcp` runtimes are excluded.
- Results are scoped by the calling session's delegation policy. You only see targets you are authorized to delegate to.
- Not-found and not-authorized produce identical (empty) responses --- no enumeration.

---

### `lenny/output`

Emit output parts to the parent session or client. Use this for incremental streaming output instead of (or in addition to) the stdout `response` message.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `output` | OutputPart[] | Yes | Array of output parts to emit |

**Returns:** Acknowledgement.

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/output",
    "arguments": {
      "output": [
        { "type": "text", "inline": "Processing file 1 of 10..." },
        { "type": "text", "inline": "Found 3 issues in auth.go" }
      ]
    }
  }
}
```

**Notes:**

- Output parts are delivered to the parent/client as `agent_output` streaming events.
- You can still use stdout `response` messages alongside `lenny/output`. The `response` message signals task completion, while `lenny/output` is for intermediate streaming output.

---

### `lenny/request_elicitation`

Request human input via the elicitation chain. The request is forwarded hop-by-hop up the delegation tree to the human client.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `schema` | object | Yes | JSON Schema describing the input to collect |
| `message` | string | Yes | Human-readable prompt displayed to the user |

**Returns:** The user's response (matching the provided schema), or an error if the elicitation was dismissed or timed out.

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/request_elicitation",
    "arguments": {
      "schema": {
        "type": "object",
        "properties": {
          "approved": { "type": "boolean" },
          "reason": { "type": "string" }
        },
        "required": ["approved"]
      },
      "message": "The analysis found 3 critical vulnerabilities. Proceed with auto-fix?"
    }
  }
}
```

**Timeout:** Elicitations time out after `maxElicitationWait` (default: 600 seconds). If the user does not respond, your runtime receives a timeout error.

**Budget:** Deployers can configure `maxElicitationsPerSession` (default: 50) to limit elicitation spam.

**Depth suppression:** At delegation depth >= 3, agent-initiated elicitations are auto-suppressed by default. Your runtime receives a `SUPPRESSED` response, which should be handled the same as "user declined."

---

### `lenny/request_input`

Block until the parent or client provides a response. This replaces the stdout `input_required` message type.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `parts` | OutputPart[] | Yes | Content describing what input is needed |

**Returns:** `MessageEnvelope` containing the response.

**Behavior:**

1. Your runtime calls `lenny/request_input` and blocks.
2. The session transitions to `input_required`.
3. The parent (or client) sees the request and responds via `lenny/send_message` with `inReplyTo`.
4. The tool call resolves with the response content.
5. The session transitions back to `running`.

**Timeout:** `maxRequestInputWaitSeconds` (configurable, Section 11.3) governs how long the tool call blocks. On timeout, the tool returns a `REQUEST_INPUT_TIMEOUT` error. Your runtime can handle this by producing a partial result or failing.

---

### `lenny/send_message`

Send a message to any task by ID, subject to messaging scope restrictions.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `to` | string | Yes | Target task/session ID |
| `message` | object | Yes | Message content |
| `message.parts` | OutputPart[] | Yes | Content parts |
| `message.inReplyTo` | string | No | If responding to a `request_input`, the request ID |
| `message.delivery` | string | No | `"immediate"` to interrupt a running session |

**Returns:** `deliveryReceipt` with status (`delivered`, `queued`, `dropped`, `expired`, `rate_limited`, `error`).

**Messaging scope:** Reachability is controlled by `messagingScope`:

| Scope | Allowed targets |
|-------|----------------|
| `direct` (default) | Direct parent and direct children |
| `siblings` | Direct parent, direct children, and sibling tasks |

**Cross-tenant validation:** Messages targeting a session belonging to a different tenant are rejected with `CROSS_TENANT_MESSAGE_DENIED`.

**Rate limits:** Subject to `maxPerMinute` (outbound), `maxPerSession` (lifetime), and `maxInboundPerMinute` (aggregate inbound on the target).

---

### `lenny/memory_write`

Write a memory record to the persistent memory store. Memories persist across sessions.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `content` | string | Yes | The memory content to store |
| `metadata` | object | No | Key-value metadata attached to the memory record |

**Returns:** Acknowledgement with the memory record ID.

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/memory_write",
    "arguments": {
      "content": "User prefers TypeScript over JavaScript for new projects.",
      "metadata": {
        "category": "preference",
        "source": "user_conversation"
      }
    }
  }
}
```

---

### `lenny/memory_query`

Query the memory store using natural-language semantic search.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | Yes | Natural-language query |
| `limit` | int | No | Maximum number of results (default: 10) |

**Returns:** Array of matching memory records with content, metadata, and relevance scores.

**Example:**

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/memory_query",
    "arguments": {
      "query": "What programming language does the user prefer?",
      "limit": 5
    }
  }
}
```

---

### `lenny/get_task_tree`

Return the task hierarchy with states. No input parameters required.

**Returns:** `TaskTreeNode` structure with `taskId`, `state`, `runtimeRef`, and children.

**Visibility control:** The `treeVisibility` field on the delegation lease controls what your runtime can see:

| Visibility | What You See |
|------------|-------------|
| `full` (default) | Entire subtree rooted at the tree root, including siblings |
| `parent-and-self` | Only your own node and your direct parent |
| `self-only` | Only your own node |

**Notes:**

- `siblings` messaging scope requires `treeVisibility: full`. The gateway rejects `messagingScope: siblings` when visibility is restricted.
- The task tree is a snapshot --- siblings spawned after you call `get_task_tree` will not appear until you call it again.

---

## Tool Availability by Tier

| Tool | Minimum | Standard | Full |
|------|---------|----------|------|
| `lenny/delegate_task` | -- | Yes | Yes |
| `lenny/await_children` | -- | Yes | Yes |
| `lenny/cancel_child` | -- | Yes | Yes |
| `lenny/discover_agents` | -- | Yes | Yes |
| `lenny/output` | -- | Yes | Yes |
| `lenny/request_elicitation` | -- | Yes | Yes |
| `lenny/request_input` | -- | Yes | Yes |
| `lenny/send_message` | -- | Yes | Yes |
| `lenny/memory_write` | -- | Yes | Yes |
| `lenny/memory_query` | -- | Yes | Yes |
| `lenny/get_task_tree` | -- | Yes | Yes |

All platform MCP tools require Standard tier or higher. Minimum-tier runtimes use only the stdin/stdout protocol and adapter-local tools (`read_file`, `write_file`, `list_dir`, `delete_file`).
