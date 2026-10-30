---
layout: default
title: REST API Reference
parent: "API Reference"
nav_order: 3
---

# REST API Reference

{: .no_toc }

The REST API covers all non-interactive operations. It is the primary integration point for CI/CD pipelines, admin dashboards, CLIs, and clients in any language. For interactive streaming sessions, use the [MCP API](mcp.html) instead.

For a try-it-in-browser view, the gateway's OpenAPI spec is also rendered in the [API Explorer](rest/index.html) (Swagger UI).

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

| Method   | Endpoint                      | Description                                |
| :------- | :---------------------------- | :----------------------------------------- |
| `POST`   | `/v1/sessions`                | Create a new session                       |
| `GET`    | `/v1/sessions`                | List sessions (filterable)                 |
| `POST`   | `/v1/sessions/{id}/upload`    | Upload a workspace file                    |
| `POST`   | `/v1/sessions/{id}/upload-archive` | Upload a workspace archive            |
| `POST`   | `/v1/sessions/{id}/finalize`  | Bind the workspace plan if none is bound, materialize the workspace, and run setup |
| `POST`   | `/v1/sessions/{id}/start`     | Start the agent runtime                    |
| `POST`   | `/v1/sessions/{id}/interrupt` | Interrupt current agent work               |
| `POST`   | `/v1/sessions/{id}/resume`    | Resume a session in `awaiting_client_action` or `suspended` |
| `POST`   | `/v1/sessions/{id}/terminate` | Graceful session termination               |
| `DELETE` | `/v1/sessions/{id}`           | Force cancel and clean up                  |
| `GET`    | `/v1/sessions/{id}`           | Get session status and metadata            |
| `POST`   | `/v1/sessions/{id}/derive`    | Fork from a completed session's workspace  |
| `POST`   | `/v1/sessions/{id}/replay`    | Replay session against a different runtime |

### POST /v1/sessions

Create a new session. Claims a warm pod, assigns credentials, and returns a session ID and upload token.

**Request body:**

| Parameter             | Type   | Required | Description                                                  |
| :-------------------- | :----- | :------- | :----------------------------------------------------------- |
| `runtime`             | string | Yes      | Name of a registered runtime                                 |
| `pool`                | string | No       | Specific pool (defaults to runtime's default pool)           |
| `workspacePlan`       | object | No       | Workspace plan (sources and setup commands). A plan here cannot carry `uploadFile` or `uploadArchive` sources, because uploads happen after creation; a plan that names uploads binds in the [finalize](#post-v1sessionsidfinalize) body instead. |
| `labels`              | object | No       | Key-value labels for filtering                               |
| `environment`         | string | No       | Environment name to scope the session                        |
| `callbackUrl`         | string | No       | Webhook URL for completion notification                      |
| `dataResidencyRegion` | string | No       | Required data residency region constraint                    |

For idempotent creation, set the `Idempotency-Key` request header (see [Idempotency](#idempotency) below). The body has no `idempotencyKey` field.

**Response** carries `session_id`, `uploadToken`, and a `sessionIsolationLevel` object describing the assigned pool's isolation posture:

| Field | Type | Description |
| :--- | :--- | :--- |
| `executionMode` | string | `session` or `service` |
| `isolationProfile` | string | `runc`, `gvisor`, or `microvm` |
| `podReuse` | boolean | `true` when the pool recycles pods (`recycle.enabled`), runs `maxConcurrentSessions > 1`, or is service mode |
| `scrubPolicy` | string | Present only when `podReuse: true`; `best-effort`, `vm-restart`, `best-effort-in-place`, `best-effort-per-slot`, or `none` (service mode) |
| `residualStateWarning` | boolean | `true` when the pod may carry residual state from prior sessions, sibling slots, or same-tenant concurrent requests. On a recycling pool whose pod keeps its runtime process across sessions, a session may run in a runtime process that served an earlier session of the same tenant. |
| `conversationContinuity` | string | `platform` for session mode; `none` for service mode |

In service mode (`conversationContinuity: none`) every message is self-contained and the gateway may route successive messages to different replicas. A client of a `multi_turn` runtime in service mode must re-inject any needed context into each message's `input`. `GET /v1/sessions/{id}` returns the same `sessionIsolationLevel` object.

**Key error codes:** `VALIDATION_ERROR` (400), `RUNTIME_UNAVAILABLE` (503), `WARM_POOL_EXHAUSTED` (503), `QUOTA_EXCEEDED` (429), `CREDENTIAL_POOL_EXHAUSTED` (503), `ERASURE_IN_PROGRESS` (403), `USER_INVALIDATED` (403), `TENANT_SUSPENDED` (403), and `TENANT_NOT_ACTIVE` (403).

### POST /v1/sessions/{id}/upload

Upload one workspace file before finalization (or mid-session if `capabilities.midSessionUpload: true`). The request body is the raw file content; it is not multipart form data. The request carries the upload token from session creation in the `X-Lenny-Upload-Token` header.

```bash
curl -X POST "https://lenny.example.com/v1/sessions/$SESSION_ID/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Lenny-Upload-Token: $UPLOAD_TOKEN" \
  --data-binary @src/main.py
```

**Response** (`201 Created`) is an `UploadResponse`. Its `uploadRef` field is an opaque reference to the stored upload; the other fields are listed in the OpenAPI document. Pass `uploadRef` verbatim as the `uploadRef` of an `uploadFile` source in the workspace plan that the session binds at finalize. An `uploadRef` is valid only in a plan bound to the session it was uploaded to. Uploading a file does not place it in the workspace: the gateway materializes only the uploads that a source of the bound plan names (see [Workspace sources](../getting-started/concepts.md#workspace-sources)).

```json
{
  "uploadRef": "<uploadRef>",
  "size": 2048
}
```

**Valid precondition states:** `created`; also `running` if the runtime declares `capabilities.midSessionUpload: true`.

**Key error codes:** `UPLOAD_TOKEN_INVALID` (401), `UPLOAD_TOKEN_EXPIRED` (401), `UPLOAD_TOKEN_MISMATCH` (403), `UPLOAD_TOKEN_CONSUMED` (410), `PAYLOAD_TOO_LARGE` (413), `STORAGE_QUOTA_EXCEEDED` (429), and `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/upload-archive

Upload one workspace archive in `tar`, `tar.gz`, or `zip` format. The request body is the raw archive, and the request carries the `X-Lenny-Upload-Token` header. The gateway extracts the archive itself at materialization, with zip-slip protection and size ceilings.

**Response** (`201 Created`) is an `UploadResponse` whose `uploadRef` names the archive in an `uploadArchive` source of the plan the session binds at finalize. The precondition states and error codes are the same as for `POST /v1/sessions/{id}/upload`.

### POST /v1/sessions/{id}/finalize

Bind the session's workspace plan when creation carried none, materialize the workspace into `/workspace/slots/{sessionId}/current`, and run setup commands. After finalization, no further pre-start uploads are accepted. Finalize requests carry the `X-Lenny-Upload-Token` header.

**Request body** (optional):

| Parameter       | Type   | Required | Description |
| :-------------- | :----- | :------- | :---------- |
| `workspacePlan` | object | No       | The [workspace plan](../reference/workspace-plan.md) to bind. Its `uploadFile` and `uploadArchive` sources name uploads to this session by their `uploadRef`. Allowed only when the session was created without a plan. |

```bash
curl -X POST "https://lenny.example.com/v1/sessions/$SESSION_ID/finalize" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Lenny-Upload-Token: $UPLOAD_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "workspacePlan": {
      "schemaVersion": 1,
      "sources": [
        { "type": "uploadFile", "path": "src/main.py", "uploadRef": "<uploadRef>" }
      ]
    }
  }'
```

`<uploadRef>` is the value from the upload response. A request without a body, or whose `workspacePlan` is absent or `null`, carries no plan: finalize materializes the plan bound at creation, or the empty plan when the session has none. The gateway validates a plan in the body, and resolves each `gitClone.ref` to a commit SHA, before it changes the session. A request rejected for its plan binds nothing and leaves the session in `created`, so the client can correct the plan and finalize again. The gateway materializes only the uploads that a source of the bound plan names (see [Workspace sources](../getting-started/concepts.md#workspace-sources)).

**Valid precondition states:** `created`.
**Resulting transition:** `finalizing` then `ready`.

**Key error codes:**

| Code | Status | Meaning |
| :--- | :----- | :------ |
| `VALIDATION_ERROR` | 400 | `details.reason` is `plan_already_set` when the body carries a plan for a session created with one, `invalid_upload_ref` when an `uploadRef` is not an upload reference, or `upload_ref_foreign_session` when an `uploadRef` names another session's upload. `details.field` names the source, such as `sources[0].uploadRef`. |
| `WORKSPACE_PLAN_INVALID` | 400 | The plan in the body fails schema validation. |
| `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS`, `GIT_CLONE_REF_UNRESOLVABLE` | 422 | A `gitClone` source cannot be authenticated for its host, or its `ref` cannot be resolved to a commit. The [Error Catalog](../reference/error-catalog.html) lists the `details` of each code. |
| `GIT_CLONE_REF_RESOLVE_TRANSIENT`, `SERVICE_UNAVAILABLE` | 503 | A `gitClone.ref` or the runtime's workspace defaults could not be read because of a transient failure. Retry after the `Retry-After` interval. |
| `SETUP_COMMAND_FAILED` | 422 | A setup command exited non-zero. |
| `RESOURCE_NOT_FOUND` | 404 | The session does not exist. |
| `INVALID_STATE_TRANSITION` | 409 | The session is not in `created`. |

A finalize call that is still running when the session ends, for example through `POST /v1/sessions/{id}/terminate`, `DELETE /v1/sessions/{id}`, an administrator force-terminate, or the gateway's finalizing timeout (`maxFinalizingTimeoutSeconds`), returns `INVALID_STATE_TRANSITION` (409), even when its own finalization also failed. The session stays in the state it ended in, and the error's `details.currentState` names that state.

### POST /v1/sessions/{id}/start

Start the agent runtime. The session must be in `ready` state (workspace finalized).

**Request body:**

| Parameter | Type   | Required | Description                                     |
| :-------- | :----- | :------- | :---------------------------------------------- |
| `message` | string | No       | Initial message to send to the agent upon start |

**Valid precondition states:** `ready`.
**Resulting transition:** `starting` then `running`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/interrupt

Pause a session mid-execution. The pod is held (up to `maxSuspendedPodHoldSeconds`) so the session can be resumed without reallocation.

**Valid precondition states:** `running`.
**Resulting transition:** `suspended`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/resume

The gateway recovers a session automatically. After a pod or stream failure, the session enters `resume_pending`, and the gateway's resume driver restores it onto a replacement pod. A session whose automatic retries are exhausted, or whose `maxResumeWindowSeconds` resume window elapses, moves to `awaiting_client_action` and waits for the client.

This endpoint is the client override. It resumes a session in `awaiting_client_action` or a `suspended` session. A message to a `suspended` session also resumes it, whatever its `delivery` value (see [POST /v1/sessions/{id}/messages](#post-v1sessionsidmessages)).

The call returns `200` with the session in its new state. From `awaiting_client_action`, and from a `suspended` session whose pod was released, the session is returned in `resume_pending`: the resume driver performs the restore, and the restore's outcome arrives on the event stream as a `status_change` event. From a `suspended` session whose pod is still held, the session is returned in `running`. The internal `resuming` state between `resume_pending` and `running` is not reported.

A `suspended` session does not count toward the concurrent-session limits, and resuming it counts it again. The gateway therefore runs the admission check that session creation runs before it resumes a `suspended` session. When a limit the session counts toward is full, the call returns `429 QUOTA_EXCEEDED` with the details a refused creation carries, and the session stays `suspended`. A client that sent an `Idempotency-Key` with a refused call retries with a new key, because the gateway returns the cached refusal for the original key until that key expires.

**Valid precondition states:** `awaiting_client_action`, `suspended`.
**Resulting transition:** `awaiting_client_action` to `resume_pending`; `suspended` to `resume_pending` when the pod was released; `suspended` to `running` when the pod is still held.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409), `QUOTA_EXCEEDED` (429).

### POST /v1/sessions/{id}/terminate

End a session gracefully. Triggers shutdown, workspace seal, artifact export, and pod release.

**Valid precondition states:** any non-terminal state.
**Resulting transition:** `completed`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `TARGET_TERMINAL` (409).

### DELETE /v1/sessions/{id}

Force-cancel a session and release all resources. Unlike `POST /v1/sessions/{id}/terminate` (which records a graceful, successful `completed` state), `DELETE` always records `cancelled` as the terminal state for audit and billing purposes. Use `/terminate` to signal normal completion; use `DELETE` to abandon a session.

**Valid precondition states:** any non-terminal state.
**Resulting transition:** `cancelled`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `TARGET_TERMINAL` (409).

### GET /v1/sessions/{id}

Get session status, metadata, runtime, pool, labels, timestamps, and token usage.

**Response includes:** `sessionId`, `status`, `runtime`, `pool`, `labels`, `createdAt`, `startedAt`, `tokenUsage`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### GET /v1/sessions

List sessions. Results are scoped to the caller's tenant and role.

**Query parameters:** `status` (string), `runtime` (string), `tenant` (string, admin only), `labels` (object), `cursor` (string), `limit` (int).

**Key error codes:** `VALIDATION_ERROR` (400).

### POST /v1/sessions/{id}/derive

Create a new session pre-populated with this session's workspace snapshot.

**Request body:**

| Parameter       | Type   | Required | Description                                                           |
| :-------------- | :----- | :------- | :-------------------------------------------------------------------- |
| `runtimeRef`    | string | No       | Runtime for the derived session (defaults to source runtime)          |
| `targetPool`    | string | No       | Pool for the derived session (defaults to the source session's pool)  |
| `allowStale`    | bool   | No       | Allow deriving from non-terminal sessions using the latest checkpoint |

**Valid precondition states:** terminal states by default; non-terminal with `allowStale: true`.

**Response:** the new session in `created`, with the `uploadToken` that authorizes uploads into it until it is finalized, as on a `POST /v1/sessions` response, and the `workspaceSnapshotSource` and `workspaceSnapshotTimestamp` of the snapshot it starts from.

**Key error codes:** `DERIVE_ON_LIVE_SESSION` (409), `DERIVE_SNAPSHOT_UNAVAILABLE` (503), and `DERIVE_LOCK_CONTENTION` (429). A `targetPool` passes the same check as the `pool` field of `POST /v1/sessions`: a pool that does not exist, is not backed by the runtime, or does not satisfy the target isolation profile answers `VALIDATION_ERROR` (400), and a pool the tenant is not granted answers `FORBIDDEN` (403). The derived session claims a warm pod at creation as `POST /v1/sessions` does, so a claim failure answers the same codes session creation does: `SESSION_CREATION_FAILED` (503), `CREDENTIAL_POOL_EXHAUSTED` (503), `USER_CREDENTIAL_NOT_FOUND` (404), and `RUNTIME_UNAVAILABLE` (503) while the pool is warming up.

### POST /v1/sessions/{id}/replay

Re-run a session against a different runtime version using the same workspace and prompt history. Used for regression testing and controlled A/B comparison. Replayed sessions receive experiment context in their [`session_start` frame](../reference/adapter-contract.md#inbound-messages-adapter-writes-to-your-stdin) so runtime-native eval platforms can attribute the replay to the correct experiment.

**Request body:**

| Parameter       | Type   | Required | Description                                                        |
| :-------------- | :----- | :------- | :----------------------------------------------------------------- |
| `targetRuntime` | string | Yes      | Runtime to replay against (must share `executionMode` with source) |
| `targetPool`    | string | No       | Specific pool for the replayed session                             |
| `replayMode`    | string | No       | `prompt_history` (default) or `workspace_derive`                   |
| `evalRef`       | string | No       | Link to an experiment or eval set                                  |

**Valid precondition states:** terminal states only.

**Response:** the new session in `created` with its `uploadToken`, identical to a `POST /v1/sessions` response. The session then goes through the standard upload, finalize, and start lifecycle.

**Key error codes:** `REPLAY_ON_LIVE_SESSION` (409), `INCOMPATIBLE_RUNTIME` (400), and `DERIVE_SNAPSHOT_UNAVAILABLE` (503). A `targetRuntime` whose `executionMode` differs from the source session's answers `INCOMPATIBLE_RUNTIME` with `details.sourceExecutionMode` and `details.targetExecutionMode`, and a `targetRuntime` that is not registered answers `VALIDATION_ERROR` (400). A `targetPool` is checked as on derive: `VALIDATION_ERROR` (400) for a pool that does not exist, is not backed by `targetRuntime`, or does not satisfy the target isolation profile, and `FORBIDDEN` (403) for a pool the tenant is not granted. The replayed session claims a warm pod at creation, so a claim failure answers the session-creation codes listed for `POST /v1/sessions/{id}/derive`.

---

## Messages and Interaction

| Method | Endpoint                                                  | Description                   |
| :----- | :-------------------------------------------------------- | :---------------------------- |
| `POST` | `/v1/sessions/{id}/messages`                              | Send a message to a session   |
| `GET`  | `/v1/sessions/{id}/messages`                              | List messages (paginated)     |
| `POST` | `/v1/sessions/{id}/tool-use/{toolCallId}/approve`       | Approve a pending tool call   |
| `POST` | `/v1/sessions/{id}/tool-use/{toolCallId}/deny`          | Deny a pending tool call      |
| `POST` | `/v1/sessions/{id}/elicitations/{elicitationId}/respond` | Respond to an elicitation     |
| `POST` | `/v1/sessions/{id}/elicitations/{elicitationId}/dismiss` | Dismiss a pending elicitation |

### POST /v1/sessions/{id}/messages

Send a message to a running or suspended session. A message to a `suspended` session resumes the session, whatever its `delivery` value. Before it holds or delivers the message, the gateway runs the concurrent-session admission check that a resume runs (see [POST /v1/sessions/{id}/resume](#post-v1sessionsidresume)). When a limit the session counts toward is full, the message is neither held nor delivered, the call returns `429 QUOTA_EXCEEDED`, and the session stays `suspended`.

**Request body:**

| Parameter   | Type   | Required | Description                                                         |
| :---------- | :----- | :------- | :------------------------------------------------------------------ |
| `input`     | array  | Yes      | Content parts to send (e.g., `[{"type": "text", "inline": "..."}]`) |
| `delivery`  | string | No       | `"immediate"` or `"queued"` (default)                               |
| `inReplyTo` | string | No       | Message ID this is replying to                                      |

**Valid precondition states:** any non-terminal state. Delivery semantics vary by state.

**Key error codes:** `INJECTION_REJECTED` (403), `SCOPE_DENIED` (403), `TARGET_TERMINAL` (409), `TARGET_NOT_READY` (409), `DUPLICATE_MESSAGE_ID` (400), `QUOTA_EXCEEDED` (429).

### GET /v1/sessions/{id}/messages

List messages sent to or from a session. Paginated with cursor-based navigation.

**Query parameters:** `cursor` (string), `limit` (int, default: 50, max: 200).

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### POST /v1/sessions/{id}/tool-use/{toolCallId}/approve

Approve a pending tool call in a human-in-the-loop workflow.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/tool-use/{toolCallId}/deny

Deny a pending tool call. Optional body: `{"reason": "<string>"}`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404), `INVALID_STATE_TRANSITION` (409).

### POST /v1/sessions/{id}/elicitations/{elicitationId}/respond

Respond to an elicitation request. Body: `{"response": <value>}`.

**Key error codes:** `ELICITATION_NOT_FOUND` (404), `RESOURCE_NOT_FOUND` (404).

### POST /v1/sessions/{id}/elicitations/{elicitationId}/dismiss

Dismiss a pending elicitation. The agent receives a timeout/dismissed signal.

**Key error codes:** `ELICITATION_NOT_FOUND` (404), `RESOURCE_NOT_FOUND` (404).

---

## Artifacts and Streaming

| Method | Endpoint                           | Description                         |
| :----- | :--------------------------------- | :---------------------------------- |
| `GET`  | `/v1/sessions/{id}/artifacts`      | List session artifacts              |
| `GET`  | `/v1/sessions/{id}/logs`           | Session log stream (paginated, SSE) |
| `GET`  | `/v1/sessions/{id}/setup-output`   | Setup command stdout/stderr         |
| `GET`  | `/v1/sessions/{id}/webhook-events` | Webhook event history               |
| `GET`  | `/v1/blobs/{ref}`                  | Resolve a `lenny-blob://` reference |

### GET /v1/sessions/{id}/artifacts

List artifacts produced by a session. Paginated.

**Query parameters:** `cursor` (string), `limit` (int, default: 50, max: 200).

**Response includes:** `items` (array of `{path, sizeBytes, mimeType, createdAt}`), `cursor`, `hasMore`, `total`.

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

### GET /v1/sessions/{id}/logs

Session log stream. Returns paginated JSON by default; switches to Server-Sent Events when the client sends `Accept: text/event-stream`. SSE streams support reconnection via the `Last-Event-ID` header with cursor-based replay within the replay window.

**Event types (SSE mode):** `agent_output`, `tool_use_requested`, `tool_result`, `elicitation_request`, `status_change`, `session.resumed`, `children_reattached`, `session_complete`, `error`, `checkpoint_boundary`, `session_expiring_soon`. See [Session Wire Format](../client-guide/wire-format.html) for full event schemas.

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

| Method | Endpoint       | Description             |
| :----- | :------------- | :---------------------- |
| `GET`  | `/v1/runtimes` | List available runtimes |
| `GET`  | `/v1/pools`    | List available pools    |

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

| Method   | Endpoint                       | Description                  |
| :------- | :----------------------------- | :--------------------------- |
| `POST`   | `/v1/credentials`              | Register a user credential   |
| `GET`    | `/v1/credentials`              | List user credentials        |
| `PUT`    | `/v1/credentials/{credentialRef}`        | Update (rotate) a credential |
| `POST`   | `/v1/credentials/{credentialRef}/revoke` | Revoke a credential          |
| `DELETE` | `/v1/credentials/{credentialRef}`        | Delete a credential          |

### POST /v1/credentials

Register a credential for the authenticated user. One credential per provider; re-registering replaces the existing one.

**Request body:**

| Parameter  | Type   | Required | Description                                       |
| :--------- | :----- | :------- | :------------------------------------------------ |
| `provider` | string | Yes      | Provider identifier (e.g., `anthropic`, `openai`) |
| `secret`   | string | Yes      | API key or OAuth token                            |
| `metadata` | object | No       | Provider-specific metadata                        |

**Key error codes:** `VALIDATION_ERROR` (400), `UNAUTHORIZED` (401).

### GET /v1/credentials

List the authenticated user's registered credentials. No secret material is returned.

**Key error codes:** `UNAUTHORIZED` (401).

### PUT /v1/credentials/{credentialRef}

Rotate (replace) the secret material for an existing credential. Active leases are immediately rotated.

**Key error codes:** `USER_CREDENTIAL_NOT_FOUND` (404), `UNAUTHORIZED` (401).

### POST /v1/credentials/{credentialRef}/revoke

Revoke a credential and immediately invalidate all active leases backed by it.

**Key error codes:** `USER_CREDENTIAL_NOT_FOUND` (404), `UNAUTHORIZED` (401).

### DELETE /v1/credentials/{credentialRef}

Remove a registered credential. Active session leases are unaffected (they continue using the credential until the session ends).

**Key error codes:** `USER_CREDENTIAL_NOT_FOUND` (404), `UNAUTHORIZED` (401).

---

## Score Storage (Basic `/eval` Endpoint)

Lenny is **not an eval platform** and does not ship one. Runtime builders choose whichever eval framework they prefer (LangSmith, Braintrust, Arize, Langfuse, home-grown) and use that framework directly for scoring and observability. The endpoints below are a **basic mechanism to store and retrieve scores** alongside session state — useful when you want to persist results without standing up another system. They are not an eval runner, a judge, or a scoring model. When a session is routed to a variant, the gateway auto-populates variant attribution on stored records.

| Method | Endpoint                             | Description                   |
| :----- | :----------------------------------- | :---------------------------- |
| `POST` | `/v1/sessions/{id}/eval`             | Submit an evaluation score    |
| `POST` | `/v1/sessions/{id}/extend-retention` | Extend artifact retention TTL |

### POST /v1/sessions/{id}/eval

Submit scored evaluation results for a session. Accepts scores from any authenticated principal (runtime, session owner, or external scorer pipeline). When the session is enrolled in an experiment, the gateway automatically populates `experiment_id` and `variant_id` from the session's experiment context.

**Request body:**

| Parameter         | Type   | Required | Description                                                             |
| :---------------- | :----- | :------- | :---------------------------------------------------------------------- |
| `scores`          | object | Yes      | Dimension-keyed scores (e.g., `{"accuracy": 0.95, "helpfulness": 0.8}`) |
| `evaluator`       | string | No       | Evaluator identifier                                                    |
| `metadata`        | object | No       | Additional eval context                                                 |
| `idempotency_key` | string | No       | Prevents duplicate scoring in pipeline retries                          |

**Rate limits:** Configurable via `evalRateLimit.perSessionPerMinute` (default: 100) and `evalRateLimit.perTenantPerMinute` (default: 10,000) in tenant configuration.

**Key error codes:** `SESSION_NOT_EVAL_ELIGIBLE` (422), `EVAL_QUOTA_EXCEEDED` (429), `RATE_LIMITED` (429), `RESOURCE_NOT_FOUND` (404).

### POST /v1/sessions/{id}/extend-retention

Extend the artifact retention TTL for a session.

**Request body:** `{"ttlSeconds": <n>}`

**Key error codes:** `RESOURCE_NOT_FOUND` (404).

---

## Convenience Endpoints

### POST /v1/sessions/start

Create, upload inline files, and start a session in one call. Combines session creation, workspace finalization, and runtime start.

**Request body:**

| Parameter     | Type   | Required | Description                                |
| :------------ | :----- | :------- | :----------------------------------------- |
| `runtime`     | string | Yes      | Runtime name                               |
| `input`       | array  | No       | Initial message content parts              |
| `files`       | array  | No       | Inline files (`{path, content, encoding}`) |
| `labels`      | object | No       | Key-value labels                           |
| `callbackUrl` | string | No       | Webhook URL                                |

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

## Idempotency

Critical mutating operations support idempotency keys. Set the `Idempotency-Key` request header (not a request body field) to an opaque string of at most 128 characters; a UUID is recommended.

```
Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000
```

Within a 24-hour window, a duplicate request with the same key returns the cached response (same HTTP status and body) without re-executing the operation. A 5xx response is not cached, so a retry against a recovered backend re-executes. Reusing a key with a different request body returns `422 IDEMPOTENCY_KEY_REUSED`.

Idempotency keys are scoped per tenant. The header applies to:

- `POST /v1/sessions` (CreateSession)
- `POST /v1/sessions/start` (create-and-start)
- `POST /v1/sessions/{id}/finalize` (FinalizeWorkspace)
- `POST /v1/sessions/{id}/start` (StartSession)
- `POST /v1/sessions/{id}/resume` (Resume)
- `POST /v1/sessions/{id}/derive`, `POST /v1/sessions/{id}/tool-use/{toolCallId}/approve`, `POST /v1/sessions/{id}/tool-use/{toolCallId}/deny`

The header is ignored on read endpoints (GET / HEAD) and on non-listed POST endpoints.

---

## REST/MCP Consistency

The REST API and MCP API share a common service layer. Operations available on both surfaces return semantically identical responses. See the [MCP API Reference](mcp.html#restmcp-consistency-contract) for the full consistency contract.

**REST-only operations** (no MCP tool equivalent): `derive`, `replay`, `extend-retention`, `eval`, `tool-use/{toolCallId}/approve`, `tool-use/{toolCallId}/deny`, `elicitations/{elicitationId}/respond`, and `elicitations/{elicitationId}/dismiss`. The first four are developer workflow or administrative operations typically driven by CI pipelines or human operators, not by agents mid-session. The tool-use approval and elicitation response endpoints have no MCP tool equivalents because MCP clients receive and resolve these prompts through the native MCP Elicitation feature — they are surfaced as elicitation exchanges on the session's streaming transport, and responses flow back over that same channel.
