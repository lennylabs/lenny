---
layout: default
title: "Platform MCP Tools"
parent: "Runtime Author Guide"
nav_order: 6
---

# Platform MCP Tools

Lenny's platform MCP server exposes a set of tools to `type: agent` runtimes at the Standard and Full integration levels. The tools cover delegation, child-session control, streaming output, human elicitation, blocking on input, inter-session messaging, the memory store, the task tree, and tracing-context propagation. The server is reachable over the abstract Unix socket named in the adapter manifest (`platformMcpServer.socket`). Connect to it with an MCP client library and present the `mcpNonce` from the manifest during the `initialize` handshake.

These tools are unavailable at the Basic level and to `type: mcp` runtimes. A Basic-level runtime uses only the stdin/stdout protocol and the adapter-local file tools. A `type: mcp` runtime hosts its own MCP server and does not participate in the task lifecycle, so the platform tools do not apply to it. See [Integration Levels](integration-levels.md) for the level definitions.

---

## Connection Setup

Before calling any platform tool, connect to the platform MCP server:

1. Read `/run/lenny/adapter-manifest.json`.
2. Extract `platformMcpServer.socket` (e.g., `@lenny-platform-mcp`) and `mcpNonce`. The nonce is a 256-bit hex string regenerated for each task execution.
3. Connect over the abstract Unix socket. Abstract sockets are a Linux kernel feature, so they are available wherever the adapter runs inside an in-cluster Linux pod. Under Embedded Mode (`lenny up`) the adapter runs in an in-cluster pod, which on macOS and Windows runs under Docker Desktop's Linux VM, so Standard- and Full-level runtime authors can develop against Embedded Mode on any host. Only Source Mode (`make run`), which runs the adapter on the host, requires a Linux host for these levels.
4. Send MCP `initialize` with the nonce as the top-level `_lennyNonce` field in `params`. The server speaks MCP `2025-03-26` and also accepts `2024-11-05` for a transition window. The adapter validates `_lennyNonce` against `mcpNonce`, strips it from `params`, and rejects any connection that omits a valid nonce before dispatching tools.

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
| `task.input` | MessagePart[] | Yes | Input content for the child session |
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
| `DELEGATION_CYCLE_DETECTED` | Target runtime's `(runtime_name, pool_name)` appears in the caller's lineage and the three-layer self-recursion gate did not opt in. `details.blockedBy` names the first `false` layer (`platform` \| `runtime` \| `policy`). See the [delegation guide](delegation.md#opting-into-self-recursion). |
| `DELEGATION_POLICY_WEAKENING` | Child lease widens an inherited `DelegationPolicy` axis. `details.field` is `maxDelegationPolicy` or `allowSelfRecursion`. |
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
- A target that does not exist and a target you are not authorized to reach both produce the same empty response, so the result cannot be used to enumerate runtimes.

---

### `lenny/output`

Emit output parts to the parent session or client. Use this for incremental streaming output instead of (or in addition to) the stdout `response` message.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `output` | MessagePart[] | Yes | Array of output parts to emit |

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

**Depth suppression:** At delegation depth >= 3, agent-initiated elicitations are auto-suppressed by default unless the elicitation type appears in the pool's allow list (deployers configure this through `elicitationDepthPolicy` per pool). A suppressed call returns a `SUPPRESSED` response, which your runtime should handle the same as "user declined."

**Content integrity (gateway-origin binding).** The gateway is the authoritative source for elicitation display text. Intermediate pods in a delegation chain forward elicitations by `elicitation_id` only — they may observe the original `{message, schema}` pair (for policy, logging, or suppression decisions) but must not modify the rendered text. The forward-hop wire mechanism is the native MCP `elicitation/create` frame: an intermediate pod re-emits the upstream `elicitation/create` frame carrying the original `{message, schema}` payload; the gateway matches the forwarded payload against its recorded original. If your runtime needs to present transformed text (translation, rephrasing, audience-targeted summarization) for a different viewer, emit a new `lenny/request_elicitation` establishing a fresh `elicitation_id` and your runtime's own `origin_pod` — do not rewrite an existing one. Under the tenant's effective elicitation content integrity enforcement mode `enforce` (the default), attempting to forward an existing `elicitation_id` with a diverging `{message, schema}` is rejected with `ELICITATION_CONTENT_TAMPERED` (HTTP 409) and raises the `ElicitationContentTamperDetected` critical alert. Operators may configure a tenant's mode to `detect-only` (the divergence is forwarded but audited) or `off` (no check) via the admin API. Regardless of the enforcement mode, runtime authors should always forward unchanged and emit a fresh elicitation for any transformed text. The `detect-only` and `off` postures exist for operator visibility and legacy-adapter accommodation; they do not license a runtime to rewrite forwarded content.

---

### `lenny/request_input`

Block until the parent or client provides a response. This is the mechanism for blocking until client input arrives (Standard level or higher).

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `parts` | MessagePart[] | Yes | Content describing what input is needed |

**Returns:** `MessageEnvelope` containing the response.

**Behavior:**

1. Your runtime calls `lenny/request_input` and blocks.
2. The session transitions to `input_required`.
3. The parent (or client) sees the request and responds via `lenny/send_message` with `inReplyTo`.
4. The tool call resolves with the response content.
5. The session transitions back to `running`.

**Timeout:** `maxRequestInputWaitSeconds` (configurable, default 600 seconds) governs how long the tool call blocks. On timeout, the tool returns a `REQUEST_INPUT_TIMEOUT` error. Your runtime can handle this by producing a partial result or failing.

**`one_shot` constraint:** Runtimes with `capabilities.interaction: one_shot` may call this tool at most once per session. A second call returns a gateway error. The runtime must then produce a best-effort response without the requested clarification or fail with a structured error (`{ "code": "INSUFFICIENT_INPUT" }`).

---

### `lenny/send_message`

Send a message to any task by ID, subject to messaging scope restrictions.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `to` | string | Yes | Target task/session ID |
| `message` | object | Yes | Message content |
| `message.parts` | MessagePart[] | Yes | Content parts |
| `message.inReplyTo` | string | No | If responding to a `request_input`, the request ID |
| `message.delivery` | string | No | `"immediate"` to interrupt a running session |

**Returns:** `deliveryReceipt` with status (`delivered`, `queued`, `dropped`, `expired`, `rate_limited`, `error`).

**Messaging scope:** Reachability is controlled by `messagingScope`:

| Scope | Allowed targets |
|-------|----------------|
| `direct` (default) | Direct parent and direct children |
| `siblings` | Direct parent, direct children, and sibling tasks |

**`treeVisibility` constraint:** `messagingScope: siblings` requires `treeVisibility: full` on the delegation lease. The gateway rejects `siblings` scope when visibility is restricted (`self-only` or `parent-and-self`) at delegation time with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`.

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
- The task tree is a snapshot. Siblings spawned after you call `get_task_tree` will not appear until you call it again.

---

### `lenny/set_tracing_context`

Register tracing identifiers that the gateway propagates to child sessions. Use this to stitch your runtime's native traces into the parent's trace tree across delegation.

**Parameters:**

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `context` | object | Yes | Map of string keys to string values carrying non-sensitive tracing identifiers (e.g., `{ "langsmith_run_id": "run_abc123" }`) |

**Returns:** Acknowledgement.

**Notes:**

- The adapter stores the context and attaches it to every subsequent `lenny/delegate_task` call. The LLM never sees or sets the tracing context; the runtime manages it as infrastructure plumbing.
- A child runtime can extend the inherited context with additional entries. Child entries merge with parent entries and cannot overwrite or remove them.
- The same registration is available to all integration levels through the stdout JSONL `set_tracing_context` message. The MCP tool exists for Standard- and Full-level runtimes that already hold an MCP connection.

---

## Tool Availability by Integration Level

| Tool | Basic | Standard | Full |
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
| `lenny/set_tracing_context` | -- | Yes | Yes |

All platform tools require the Standard level or higher. Basic-level runtimes use only the stdin/stdout protocol and the adapter-local file tools (`read_file`, `write_file`, `list_dir`, and `delete_file`). A Basic-level runtime that needs to propagate a tracing context can still do so through the stdout JSONL `set_tracing_context` message, which is available at every level.
