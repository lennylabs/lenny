---
layout: default
title: REST API Reference
parent: "API Reference"
nav_order: 3
---

# REST API Reference
{: .no_toc }

The REST API covers all non-interactive operations. It is the primary integration point for CI/CD pipelines, admin dashboards, CLIs, and clients in any language. For interactive streaming sessions, use the [MCP API](mcp.html) instead.

The gateway also serves an interactive OpenAPI explorer at [`/openapi.yaml`](rest/index.html) (Swagger UI).

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## Authentication

Include an OIDC Bearer token in the `Authorization` header of every request:

```
Authorization: Bearer <oidc-token>
```

In `make run` dev mode, authentication is disabled.

---

## Session Lifecycle

| Method | Endpoint | Description |
|:-------|:---------|:------------|
| `POST` | `/v1/sessions` | Create a new session |
| `POST` | `/v1/sessions/{id}/upload` | Upload workspace files |
| `POST` | `/v1/sessions/{id}/finalize` | Finalize workspace and run setup |
| `POST` | `/v1/sessions/{id}/start` | Start the agent runtime |
| `POST` | `/v1/sessions/{id}/terminate` | Graceful session termination |
| `DELETE` | `/v1/sessions/{id}` | Force cancel and clean up |
| `GET` | `/v1/sessions/{id}` | Get session status and metadata |
| `POST` | `/v1/sessions/{id}/derive` | Fork from a completed session's workspace |
| `POST` | `/v1/sessions/{id}/replay` | Replay session against a different runtime |

### POST /v1/sessions

Create a new session. Claims a warm pod, assigns credentials, and returns a session ID and upload token.

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `runtime` | string | Yes | Name of a registered runtime |
| `pool` | string | No | Specific pool (defaults to runtime's default pool) |
| `workspacePlan` | object | No | Workspace configuration (sources, maxSizeMB, setup commands) |
| `labels` | object | No | Key-value labels for filtering |
| `environment` | string | No | Environment name to scope the session |
| `callbackUrl` | string | No | Webhook URL for completion notification |
| `idempotencyKey` | string | No | Client-supplied key for idempotent creation |
| `dataResidencyRegion` | string | No | Required data residency region constraint |

**Key error codes:** `VALIDATION_ERROR` (400), `RUNTIME_UNAVAILABLE` (503), `WARM_POOL_EXHAUSTED` (503), `QUOTA_EXCEEDED` (429), `CREDENTIAL_POOL_EXHAUSTED` (503), `ERASURE_IN_PROGRESS` (403).

### POST /v1/sessions/{id}/upload

Upload workspace files before finalization (or mid-session if `capabilities.midSessionUpload: true`). Uses multipart form data. Requires the `UploadToken` from session creation.

**Key error codes:** `UPLOAD_TOKEN_EXPIRED` (401), `UPLOAD_TOKEN_MISMATCH` (403), `UPLOAD_TOKEN_CONSUMED` (410), `STORAGE_QUOTA_EXCEEDED` (429), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/finalize

Seal the workspace and run setup commands. Moves uploaded files from staging to `/workspace/current`. After finalization, no further pre-start uploads are accepted.

**Valid precondition states:** `created`.
**Resulting transition:** `finalizing` then `ready`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/start

Start the agent runtime. The session must be in `ready` state (workspace finalized).

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `message` | string | No | Initial message to send to the agent upon start |

**Valid precondition states:** `ready`.
**Resulting transition:** `starting` then `running`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/terminate

End a session gracefully. Triggers shutdown, workspace seal, artifact export, and pod release.

**Valid precondition states:** any non-terminal state.
**Resulting transition:** `completed`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `TARGET_TERMINAL` (409).

### DELETE /v1/sessions/{id}

Force-terminate and clean up a session. Equivalent to terminate + cleanup in one call.

**Valid precondition states:** any non-terminal state.
**Resulting transition:** `cancelled`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `TARGET_TERMINAL` (409).

### GET /v1/sessions/{id}

Get session status, metadata, runtime, pool, labels, timestamps, and token usage.

**Response includes:** `sessionId`, `status`, `runtime`, `pool`, `labels`, `createdAt`, `startedAt`, `tokenUsage`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### POST /v1/sessions/{id}/derive

Create a new session pre-populated with this session's workspace snapshot.

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `targetRuntime` | string | No | Runtime for the derived session (defaults to source runtime) |
| `allowStale` | bool | No | Allow deriving from non-terminal sessions using the latest checkpoint |

**Valid precondition states:** terminal states by default; non-terminal with `allowStale: true`.

**Key error codes:** `DERIVE_ON_LIVE_SESSION` (409), `DERIVE_SNAPSHOT_UNAVAILABLE` (503), `DERIVE_LOCK_CONTENTION` (429).

### POST /v1/sessions/{id}/replay

Re-run a session against a different runtime version using the same workspace and prompt history. Primary mechanism for regression testing and A/B evaluation.

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `targetRuntime` | string | Yes | Runtime to replay against (must share `executionMode` with source) |
| `targetPool` | string | No | Specific pool for the replayed session |
| `replayMode` | string | No | `prompt_history` (default) or `workspace_derive` |
| `evalRef` | string | No | Link to an experiment or eval set |

**Valid precondition states:** terminal states only.

**Key error codes:** `REPLAY_ON_LIVE_SESSION` (409), `INCOMPATIBLE_RUNTIME` (400).

---

## Messages and Interaction

| Method | Endpoint | Description |
|:-------|:---------|:------------|
| `POST` | `/v1/sessions/{id}/messages` | Send a message to a session |
| `GET` | `/v1/sessions/{id}/messages` | List messages (paginated) |
| `POST` | `/v1/sessions/{id}/tool-use/{tool_call_id}/approve` | Approve a pending tool call |
| `POST` | `/v1/sessions/{id}/tool-use/{tool_call_id}/deny` | Deny a pending tool call |
| `POST` | `/v1/sessions/{id}/elicitations/{elicitation_id}/respond` | Respond to an elicitation |
| `POST` | `/v1/sessions/{id}/elicitations/{elicitation_id}/dismiss` | Dismiss a pending elicitation |

### POST /v1/sessions/{id}/messages

Send a message to a running or suspended session.

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `input` | array | Yes | Content parts to send (e.g., `[{"type": "text", "inline": "..."}]`) |
| `delivery` | string | No | `"immediate"` or `"queued"` (default) |
| `inReplyTo` | string | No | Message ID this is replying to |

**Valid precondition states:** any non-terminal state. Delivery semantics vary by state.

**Key error codes:** `INJECTION_REJECTED` (403), `SCOPE_DENIED` (403), `TARGET_TERMINAL` (409), `TARGET_NOT_READY` (409), `DUPLICATE_MESSAGE_ID` (400).

### GET /v1/sessions/{id}/messages

List messages sent to or from a session. Paginated with cursor-based navigation.

**Query parameters:** `cursor` (string), `limit` (int, default: 50, max: 200).

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### POST /v1/sessions/{id}/tool-use/{tool_call_id}/approve

Approve a pending tool call in a human-in-the-loop workflow.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/tool-use/{tool_call_id}/deny

Deny a pending tool call. Optional body: `{"reason": "<string>"}`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond

Respond to an elicitation request. Body: `{"response": <value>}`.

**Key error codes:** `ELICITATION_NOT_FOUND` (404), `RESOURCE_NOT_FOUND` (404).

### POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss

Dismiss a pending elicitation. The agent receives a timeout/dismissed signal.

**Key error codes:** `ELICITATION_NOT_FOUND` (404), `RESOURCE_NOT_FOUND` (404).

---

## Artifacts and Streaming

| Method | Endpoint | Description |
|:-------|:---------|:------------|
| `GET` | `/v1/sessions/{id}/artifacts` | List session artifacts |
| `GET` | `/v1/sessions/{id}/events` | SSE event stream |
| `GET` | `/v1/sessions/{id}/setup-output` | Setup command stdout/stderr |
| `GET` | `/v1/sessions/{id}/webhook-events` | Webhook event history |
| `GET` | `/v1/blobs/{ref}` | Resolve a `lenny-blob://` reference |

### GET /v1/sessions/{id}/artifacts

List artifacts produced by a session. Paginated.

**Query parameters:** `cursor` (string), `limit` (int, default: 50, max: 200).

**Response includes:** `items` (array of `{path, sizeBytes, mimeType, createdAt}`), `cursor`, `hasMore`, `total`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### GET /v1/sessions/{id}/events

Open an SSE (Server-Sent Events) stream for real-time session output. Supports reconnection via `Last-Event-ID` header with cursor-based replay within the replay window.

**Event types:** `agent_output`, `status_change`, `elicitation`, `tool_use`, `error`, `terminated`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### GET /v1/sessions/{id}/setup-output

Get stdout and stderr from workspace setup commands.

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### GET /v1/sessions/{id}/webhook-events

List undelivered webhook events after retry exhaustion. Useful for diagnosing `callbackUrl` delivery failures.

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### GET /v1/blobs/{ref}

Resolve and download a `lenny-blob://` reference. `{ref}` is the full `lenny-blob://` URI, URL-encoded. The gateway verifies read access before streaming the blob back with the appropriate `Content-Type`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `FORBIDDEN` (403).

---

## Discovery

| Method | Endpoint | Description |
|:-------|:---------|:------------|
| `GET` | `/v1/runtimes` | List available runtimes |
| `GET` | `/v1/pools` | List available pools |

### GET /v1/runtimes

List registered runtimes. Results are identity-filtered and policy-scoped -- users only see runtimes they have access to via their tenant and environment memberships.

**Query parameters:** `labels` (object), `environment` (string), `cursor` (string), `limit` (int).

**Response includes:** `items` (array with `name`, `type`, `executionMode`, `agentInterface`, `capabilities`, `labels`, `adapterCapabilities`), pagination fields. For `type: mcp` runtimes, response also includes `mcpEndpoint` and `mcpCapabilities.tools`.

**Key error codes:** `VALIDATION_ERROR` (400).

### GET /v1/pools

List pools and warm pod counts.

**Key error codes:** `VALIDATION_ERROR` (400).

---

## User Credentials

User-managed credentials for the "bring your own API key" workflow. See the [User Credentials tutorial](../tutorials/user-credentials.html) for a walkthrough.

| Method | Endpoint | Description |
|:-------|:---------|:------------|
| `POST` | `/v1/credentials` | Register a user credential |
| `GET` | `/v1/credentials` | List user credentials |
| `PUT` | `/v1/credentials/{ref}` | Update (rotate) a credential |
| `POST` | `/v1/credentials/{ref}/revoke` | Revoke a credential |
| `DELETE` | `/v1/credentials/{ref}` | Delete a credential |

### POST /v1/credentials

Register a credential for the authenticated user. One credential per provider; re-registering replaces the existing one.

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `provider` | string | Yes | Provider identifier (e.g., `anthropic`, `openai`) |
| `secret` | string | Yes | API key or OAuth token |
| `metadata` | object | No | Provider-specific metadata |

**Key error codes:** `VALIDATION_ERROR` (400), `UNAUTHORIZED` (401).

### GET /v1/credentials

List the authenticated user's registered credentials. No secret material is returned.

**Key error codes:** `UNAUTHORIZED` (401).

### PUT /v1/credentials/{ref}

Rotate (replace) the secret material for an existing credential. Active leases are immediately rotated.

**Key error codes:** `USER_CREDENTIAL_NOT_FOUND` (404), `UNAUTHORIZED` (401).

### POST /v1/credentials/{ref}/revoke

Revoke a credential and immediately invalidate all active leases backed by it.

**Key error codes:** `USER_CREDENTIAL_NOT_FOUND` (404), `UNAUTHORIZED` (401).

### DELETE /v1/credentials/{ref}

Remove a registered credential. Active session leases are unaffected (they continue using the credential until the session ends).

**Key error codes:** `USER_CREDENTIAL_NOT_FOUND` (404), `UNAUTHORIZED` (401).

---

## Evaluation

| Method | Endpoint | Description |
|:-------|:---------|:------------|
| `POST` | `/v1/sessions/{id}/eval` | Submit an evaluation score |
| `POST` | `/v1/sessions/{id}/extend-retention` | Extend artifact retention TTL |

### POST /v1/sessions/{id}/eval

Submit scored evaluation results for a session (LLM-as-judge scores, custom heuristics, ground-truth comparisons). Stored as session metadata.

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `scores` | object | Yes | Dimension-keyed scores (e.g., `{"accuracy": 0.95, "helpfulness": 0.8}`) |
| `evaluator` | string | No | Evaluator identifier |
| `metadata` | object | No | Additional eval context |

**Key error codes:** `SESSION_NOT_EVAL_ELIGIBLE` (422), `EVAL_QUOTA_EXCEEDED` (429), `RESOURCE_NOT_FOUND` (404).

### POST /v1/sessions/{id}/extend-retention

Extend the artifact retention TTL for a session.

**Request body:** `{"ttlSeconds": <n>}`

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

---

## Convenience Endpoints

### POST /v1/sessions/start

Create, upload inline files, and start a session in one call. Combines session creation, workspace finalization, and runtime start.

**Request body:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `runtime` | string | Yes | Runtime name |
| `input` | array | No | Initial message content parts |
| `files` | array | No | Inline files (`{path, content, encoding}`) |
| `labels` | object | No | Key-value labels |
| `callbackUrl` | string | No | Webhook URL |

Returns a running session with the first message already delivered.

---

## Error Handling

All endpoints return errors using the canonical error envelope. See the [Error Catalog](../reference/error-catalog.html) for the complete list.

```json
{
  "error": {
    "code": "INVALID_STATE_TRANSITION",
    "category": "PERMANENT",
    "message": "Cannot start a session that is already running.",
    "retryable": false,
    "details": {
      "currentState": "running",
      "allowedStates": ["created", "ready"]
    }
  }
}
```

All responses include [rate-limit headers](../reference/error-catalog.html#rate-limit-headers).

---

## REST/MCP Consistency

The REST API and MCP API share a common service layer. Operations available on both surfaces return semantically identical responses. See the [MCP API Reference](mcp.html#restmcp-consistency-contract) for the full consistency contract.

**REST-only operations** (no MCP tool equivalent): `derive`, `replay`, `extend-retention`, and `eval`. These are developer workflow operations typically driven by CI pipelines or human operators, not by agents mid-session.
