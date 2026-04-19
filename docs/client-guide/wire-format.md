---
layout: default
title: "Wire Format"
parent: "Client Guide"
nav_order: 0
description: Single-page reference for the JSON shapes, SSE events, headers, and content types Lenny's gateway speaks over the wire. The source of truth SDK examples and tutorials cite.
---

# Wire Format

{: .no_toc }

A single reference for the JSON shapes, SSE events, headers, and content types the Lenny gateway speaks over the wire. SDK examples, tutorials, and third-party clients should all match this page. When this page and the [spec](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md) disagree, the spec wins.

<details open markdown="block">
  <summary>Table of contents</summary>
  {: .text-delta }
- TOC
{:toc}
</details>

---

## 1. Base URL and content types

Everything the client talks to lives at the gateway's base URL:

```
https://<gateway-host>/
```

| Surface | Request content type | Response content type |
|:--------|:---------------------|:----------------------|
| REST | `application/json` | `application/json` |
| REST streaming (`/logs` with `Accept: text/event-stream`) | — | `text/event-stream` |
| MCP | Streamable HTTP (MCP framing) | Streamable HTTP (MCP framing) |
| OpenAI Chat Completions | `application/json` | `application/json` or `text/event-stream` |
| Open Responses | `application/json` | `application/json` or `text/event-stream` |
| Admin | `application/json` | `application/json` |
| Upload (`POST /v1/sessions/{id}/upload`) | `multipart/form-data` | `application/json` |

---

## 2. Authentication headers

```
Authorization: Bearer <oidc-or-lenny-access-token>
```

For the admin surface, the token is a shared admin token set at install time. For client surfaces, the token is either an IdP-issued OIDC token the gateway verifies directly, or a Lenny access token obtained via `POST /v1/oauth/token` (RFC 8693 token exchange).

**Upload token** (separate credential, not a Bearer token). Every `POST /v1/sessions` response includes an `uploadToken`. Include it on every upload request as:

```
X-Upload-Token: <uploadToken>
```

Format: `<session_id>.<expiry_unix_seconds>.<hmac_hex>`. Treat it as a secret. The token expires at `session_creation_time + maxCreatedStateTimeoutSeconds` (default 300s).

**Optimistic concurrency** on Admin resources (`PUT /v1/admin/runtimes/{name}` and friends): `If-Match: "<etag>"` is required; omitting it returns `428 ETAG_REQUIRED`.

---

## 3. Session lifecycle endpoints

| Method | Endpoint | Purpose |
|:-------|:---------|:--------|
| `POST` | `/v1/sessions` | Multi-step session creation (returns `sessionId`, `uploadToken`) |
| `POST` | `/v1/sessions/{id}/upload` | Upload workspace files (multipart; requires `X-Upload-Token`) |
| `POST` | `/v1/sessions/{id}/finalize` | Seal workspace; run setup commands |
| `POST` | `/v1/sessions/{id}/start` | Start the agent runtime |
| `POST` | `/v1/sessions/start` | One-shot create + finalize + start |
| `POST` | `/v1/sessions/{id}/messages` | Send a message to a running session |
| `GET`  | `/v1/sessions/{id}/logs` | Session log stream (paginated JSON, or SSE with `Accept: text/event-stream`) |
| `POST` | `/v1/sessions/{id}/terminate` | Graceful termination |
| `DELETE` | `/v1/sessions/{id}` | Force cancel and clean up |
| `GET`  | `/v1/sessions/{id}` | Session status and metadata |
| `POST` | `/v1/sessions/{id}/derive` | Fork from a completed session's workspace |
| `POST` | `/v1/sessions/{id}/replay` | Replay prompt history against a different runtime |

For the complete list including admin endpoints, see the [REST API Reference](../api/rest).

---

## 4. Message shapes

### 4.1 Inbound message (`POST /v1/sessions/{id}/messages`)

```json
{
  "type": "message",
  "input": [
    { "type": "text", "inline": "List the open pull requests in myorg/myrepo." }
  ]
}
```

| Field | Type | Required | Notes |
|:------|:-----|:---------|:------|
| `type` | enum | yes | Always `"message"` on the inbound side. |
| `input` | array | yes | Ordered array of content parts. Each part has a `type` and type-specific fields. |
| `input[].type` | enum | yes | See the OutputPart type registry below. The inbound side supports the same registry. |
| `input[].inline` | string | — | Literal payload (e.g., text). |
| `input[].uploadRef` | string | — | Reference to an already-uploaded file, for large payloads. |

### 4.2 Outbound response (terminal result of a session)

```json
{
  "type": "response",
  "output": [
    { "type": "text", "inline": "Found 3 open PRs in myorg/myrepo." },
    { "type": "citation", "inline": "Fix auth module", "annotations": { "source": "https://github.com/myorg/myrepo/pull/42" } }
  ]
}
```

| Field | Type | Required | Notes |
|:------|:-----|:---------|:------|
| `type` | enum | yes | Always `"response"`. |
| `output` | array | yes | Ordered array of `OutputPart`. |

Note: the outbound response puts `output` directly on the top level. **TaskResult** (delegation child output) wraps it one level deeper:

```json
{
  "output": {
    "parts": [ { "type": "text", "inline": "Task complete." } ],
    "artifactRefs": ["artifact_01..."]
  }
}
```

---

## 5. OutputPart type registry

Canonical discriminated-union types. See Spec §15 for field-level detail.

| `type` | Key fields | Purpose |
|:-------|:-----------|:--------|
| `text` | `inline` (string), `mimeType` (`text/plain`) | Plain-text output. |
| `code` | `inline` (string), `mimeType`, `annotations.language` | Source-code fragment with language tag. |
| `reasoning_trace` | `inline` (string) | Chain-of-thought or internal reasoning. |
| `citation` | `inline` (string), `annotations.source` | Source attribution. |
| `screenshot` | `inline` (base64) or `ref`, `mimeType` (`image/*`) | Captured screen image. |
| `image` | `inline` (base64) or `ref`, `mimeType` (`image/*`) | Generic image. |
| `diff` | `inline` (string), `annotations.language` (`diff`) | Unified-format diff. |
| `file` | `inline` or `ref`, `mimeType` | File produced by the agent. |
| `execution_result` | `parts[]` (each part is a full `OutputPart`) | Tool-call result. |
| `error` | `inline` (human-readable), `annotations.errorCode` (optional) | Error emitted mid-stream. |

Parts with a size above 64 KB are staged to blob storage and delivered with `ref` populated instead of `inline`; parts above 50 MB are rejected at ingress with `413 OUTPUTPART_TOO_LARGE`. Unknown types MUST be preserved and forwarded verbatim by middleware; unprefixed custom types collapse to `text` with `annotations.originalType` set.

---

## 6. SSE event stream (`GET /v1/sessions/{id}/logs`)

Set `Accept: text/event-stream` to receive SSE; omit it to receive paginated JSON.

### 6.1 Event envelope

Each event is an SSE block:

```
id: 1718203320000000123
event: <event_type>
data: { …event-specific JSON… }

```

The `id` is a cursor you can pass back via `Last-Event-ID` on reconnect.

### 6.2 Event types

| `event:` | `data:` payload | Meaning |
|:---------|:----------------|:--------|
| `agent_output` | `{ "output": OutputPart[] }` | One or more parts of agent output. |
| `tool_use_requested` | `{ "tool_call_id", "tool", "args" }` | Agent wants to call a tool (if approval required). |
| `tool_result` | `{ "tool_call_id", "result": { "content": OutputPart[] } }` | Tool call returned. |
| `elicitation_request` | `{ "elicitation_id", "schema" }` | Agent/tool needs user input. |
| `status_change` | `{ "state" }` | Session state transition (including `suspended` and `input_required`). |
| `session.resumed` | `{ "resumeMode", "workspaceLost" }` | Session resumed from checkpoint or minimal state. |
| `children_reattached` | `{ "children": ReattachedChild[] }` | Parent session resumed with active children. |
| `session_complete` | `{ "result": { "output": OutputPart[] } }` | Session reached terminal state. |
| `error` | `{ "code", "message", "transient" }` | Fatal or recoverable error. |
| `checkpoint_boundary` | `{ "cursor", "events_lost", "reason", "checkpoint_timestamp" }` | Client's last-seen cursor fell outside the replay window. |
| `session_expiring_soon` | `{ "maxSessionAge", "remainingSeconds" }` | Sent 5 minutes before `maxSessionAge` expires. |

### 6.3 Example: `agent_output`

```
event: agent_output
data: {"output":[{"type":"text","inline":"Analyzing the codebase..."}]}
```

### 6.4 Example: `tool_result`

```
event: tool_result
data: {"tool_call_id":"tc_001","result":{"content":[{"type":"text","inline":"Found 12 matches."}]}}
```

---

## 7. WorkspacePlan shape

The session-creation body may include a `workspacePlan`. Top-level fields:

```json
{
  "runtime": "claude-code",
  "pool": "claude-worker-sandboxed-medium",
  "isolationProfile": "sandboxed",
  "workspacePlan": {
    "$schema": "https://schemas.lenny.dev/workspaceplan/v1.json",
    "schemaVersion": 1,
    "sources": [
      { "type": "inlineFile", "path": "CLAUDE.md", "content": "..." },
      { "type": "uploadFile", "path": "src/main.ts", "uploadRef": "upload_abc123" }
    ],
    "setupCommands": [
      { "cmd": "npm ci", "timeoutSeconds": 300 }
    ]
  },
  "env": { "NODE_ENV": "production" },
  "labels": { "team": "platform" },
  "timeouts": { "maxSessionAgeSeconds": 3600 },
  "callbackUrl": "https://ci.example.com/hooks/lenny-complete",
  "delegationLease": { "maxDepth": 2, "maxChildrenTotal": 5 }
}
```

The full schema is in the [WorkspacePlan reference](../reference/workspace-plan) and [Spec §14](https://github.com/lennylabs/lenny/blob/main/spec/14_workspace-plan-schema.md).

---

## 8. Webhook payload (CloudEvents v1.0.2)

Terminal session events are POSTed to `callbackUrl` as CloudEvents in JSON mode:

```json
{
  "specversion": "1.0",
  "id": "t_acme:gw-7f4c2:1718203320000000000:9f3a",
  "source": "//lenny.dev/gateway/gw-7f4c2",
  "type": "dev.lenny.session_completed",
  "time": "2026-04-17T10:30:00Z",
  "datacontenttype": "application/json",
  "subject": "session/sess_abc123",
  "lennytenantid": "t_acme",
  "data": {
    "session_id": "sess_abc123",
    "status": "completed",
    "usage": { "inputTokens": 15000, "outputTokens": 8000 },
    "artifacts": ["workspace.tar.gz"]
  }
}
```

**Signature** (webhooks): `X-Lenny-Signature: t=<unix_seconds>,v1=<hex_signature>` with HMAC-SHA256 over `"<unix_seconds>.<raw_body_bytes>"`. Enforce a 5-minute replay window on `t`. See the [CloudEvents Catalog](../reference/cloudevents-catalog) for per-type `data` schemas.

---

## 9. Error envelope

Every API returns errors in the same shape:

```json
{
  "error": {
    "code": "QUOTA_EXCEEDED",
    "category": "POLICY",
    "message": "Tenant t1 has exceeded its monthly session quota (limit: 500).",
    "retryable": false,
    "details": {}
  }
}
```

Categories: `TRANSIENT` (retry with backoff), `PERMANENT` (fix the request), `POLICY` (check limits/permissions), `UPSTREAM` (check dependency).

See the [Error Catalog](../reference/error-catalog) for every code.

---

## 10. Pagination

List endpoints return:

```json
{
  "items": [ /* resource objects */ ],
  "cursor": "eyJpZCI6IjAxOTVmMzQ...",
  "hasMore": true,
  "total": 1247
}
```

Request with `?cursor=<prev>&limit=<n>&sort=<field>:<asc|desc>`. `limit` defaults to 50 (max 200). Cursors expire after 24 hours; expired cursors return `VALIDATION_ERROR` with `details.fields[0].rule: "cursor_expired"`.

---

## 11. Rate-limit headers

Every response:

```
X-RateLimit-Limit: 120
X-RateLimit-Remaining: 42
X-RateLimit-Reset: 1718203380
```

`429` responses also include `Retry-After: <seconds>`.

---

## 12. Things that surprise new integrators

- **SSE buffer size.** Some `agent_output` events carry large payloads (base64 screenshots, long code blocks). If your SSE parser caps lines at 64 KB (e.g., Go's default `bufio.Scanner`), raise the ceiling or switch to a reader.
- **Upload token is not a Bearer token.** It goes in `X-Upload-Token`, not `Authorization`. Using `Authorization: Bearer <uploadToken>` returns `401 UNAUTHORIZED`.
- **TaskResult nests `output.parts`.** Inbound messages use `input[]`, outbound responses use `output[]` directly, but delegation task results use `output.parts[]` with an extra wrapper.
- **Inline vs. uploadRef.** Small text fits in `inline`; anything binary or over a few KB should be pre-uploaded and referenced by `uploadRef`.
- **Isolation profile vs. RuntimeClass.** Use `isolationProfile` (`standard`/`sandboxed`/`microvm`) in config; the K8s `RuntimeClass` (`runc`/`gvisor`/`kata`) is an implementation detail set by the installer.

---

## Related

- [REST API Reference](../api/rest) — endpoint-by-endpoint reference
- [WorkspacePlan Schema](../reference/workspace-plan)
- [CloudEvents Catalog](../reference/cloudevents-catalog)
- [Error Catalog](../reference/error-catalog)
- [Spec §15 — External API Surface](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md) (source of truth)
