---
layout: default
title: WorkspacePlan Schema
parent: Reference
nav_order: 8
description: Declarative specification for how a session's workspace is prepared — sources, setup commands, env, timeouts, retry policy, delegation lease.
---

# WorkspacePlan Schema

The `WorkspacePlan` is the declarative specification for how a session's workspace is prepared. Its inner `workspacePlan` object binds once per session: in the session creation request (`POST /v1/sessions` or `POST /v1/sessions/start`), or, when creation carried none, in the body of `POST /v1/sessions/{id}/finalize`. A plan that names uploads binds at finalize, because uploads happen after the session is created. The request envelope controls workspace source materialization, setup commands, environment, timeouts, retry behavior, callbacks, credentials, and delegation budgets.

The canonical, versioned schema is [`spec/14_workspace-plan-schema.md`](https://github.com/lennylabs/lenny/blob/main/spec/14_workspace-plan-schema.md). This page is a reference distillation; for every field's full semantics, defaults, and validation rules, consult the spec.

---

## Example

```json
{
  "pool": "claude-worker-sandboxed-medium",
  "isolationProfile": "sandboxed",
  "workspacePlan": {
    "$schema": "https://schemas.lenny.dev/workspaceplan/v1.json",
    "schemaVersion": 1,
    "sources": [
      { "type": "inlineFile", "path": "CLAUDE.md", "content": "# Project Instructions\n..." },
      { "type": "mkdir", "path": "output/" }
    ],
    "setupCommands": [
      { "cmd": "npm ci", "timeoutSeconds": 300 }
    ]
  },
  "env": { "NODE_ENV": "production" },
  "labels": { "team": "platform", "project": "auth-refactor" },
  "timeouts": { "maxSessionAgeSeconds": 3600, "maxIdleSeconds": 300 },
  "retryPolicy": { "mode": "auto_then_client", "maxRetries": 2 },
  "credentialPolicy": { "preferredSource": "pool" },
  "callbackUrl": "https://ci.example.com/hooks/lenny-complete",
  "delegationLease": { "maxDepth": 2, "maxChildrenTotal": 5 }
}
```

The plan in this creation example carries no upload sources. A plan that names uploads binds in the finalize body, after the uploads return their `uploadRef` values:

```json
{
  "workspacePlan": {
    "schemaVersion": 1,
    "sources": [
      { "type": "uploadFile", "path": "src/main.ts", "uploadRef": "<uploadRef>" },
      { "type": "uploadArchive", "pathPrefix": ".", "uploadRef": "<uploadRef>", "format": "tar.gz" },
      { "type": "mkdir", "path": "output/" }
    ],
    "setupCommands": [
      { "cmd": "npm ci", "timeoutSeconds": 300 }
    ]
  }
}
```

Each `<uploadRef>` is the value from the response of `POST /v1/sessions/{id}/upload` or `POST /v1/sessions/{id}/upload-archive` for the same session.

---

## Top-level fields

| Field | Type | Description |
|:------|:-----|:------------|
| `pool` | string | Target pool. Defaults to the runtime's default pool. |
| `isolationProfile` | enum | `standard` (runc, dev only) \| `sandboxed` (gVisor, default) \| `microvm` (Kata). See [§5.3 isolation profiles](https://github.com/lennylabs/lenny/blob/main/spec/05_runtime-registry-and-pool-model.md#53-isolation-profiles). |
| `workspacePlan.schemaVersion` | integer | Schema version; the gateway validates incoming plans against the registered schema. |
| `workspacePlan.sources` | array | Materialization sources: `inlineFile`, `uploadFile`, `uploadArchive`, `mkdir`, `gitClone`. |
| `workspacePlan.setupCommands` | array | Post-materialization setup commands with optional per-command `timeoutSeconds`. |
| `env` | object | Environment variables injected into the session pod. Subject to the deployer-configured blocklist. |
| `labels` | object | User-defined metadata for querying and cost attribution; indexed in the session store. |
| `timeouts` | object | Per-session overrides for `maxSessionAgeSeconds` and `maxIdleSeconds`; capped by runtime policy. |
| `retryPolicy` | object | `mode` (`auto`, `client`, `auto_then_client`), `maxRetries`, `maxResumeWindowSeconds`. |
| `credentialPolicy` | object | Per-session override for credential source (`pool`, `user`, `prefer-*`). Can restrict but not expand tenant policy. |
| `callbackUrl` | HTTPS URL | Webhook URL for session-terminal CloudEvents delivery. Subject to SSRF mitigations (DNS pinning, private-IP rejection, optional allowlist). |
| `callbackSecret` | string | Write-only HMAC-SHA256 signing secret for webhook deliveries. Stored KMS-encrypted. |
| `delegationLease` | object | Budget ceilings for recursive delegation: `maxDepth`, `maxChildrenTotal`, `delegationPolicyRef`. |
| `runtimeOptions` | object | Per-runtime discriminated-union options validated against the target runtime's `runtimeOptionsSchema`. |

---

## Source types

| Type | Required fields | Purpose |
|:-----|:----------------|:--------|
| `inlineFile` | `path`, `content` | Write literal content (small files, config, instructions) into the workspace. |
| `uploadFile` | `path`, `uploadRef` | Materialize a single file uploaded via `POST /v1/sessions/{id}/upload`. `uploadRef` is the opaque value from the upload response for the same session; pass it verbatim. A plan bound at session creation cannot carry this source. |
| `uploadArchive` | `pathPrefix`, `uploadRef`, `format` | Extract an archive uploaded via `POST /v1/sessions/{id}/upload-archive` (`tar`, `tar.gz`, or `zip`) under `pathPrefix`. `uploadRef` is the opaque value from the upload response for the same session; pass it verbatim. A plan bound at session creation cannot carry this source. |
| `mkdir` | `path` | Create an empty directory (useful for output collection). |
| `gitClone` | `url`, `ref`, `path` | Clone a Git repository at the specified ref into `path`. Optional `depth`, `submodules`, and `auth` (credential reference). The gateway resolves `ref` to a commit SHA when the plan binds, at session creation or at finalize, and writes it back as `resolvedCommitSha` on the stored source (readable via `GET /v1/sessions/{id}`); ref-resolution failures return `422 GIT_CLONE_REF_UNRESOLVABLE` (auth failure or unknown ref, not retryable) or `503 GIT_CLONE_REF_RESOLVE_TRANSIENT` (transient network failure, retryable). |

**File mode (`inlineFile`, `mkdir`).** The optional `mode` field on these sources is a POSIX-style octal string matching the regex `^0[0-7]{3,4}$` (e.g., `"0644"`, `"0755"`). The setuid (`04000`) and setgid (`02000`) bits are prohibited for all source types; the sticky bit (`01000`) is accepted only on `mkdir`. Invalid values are rejected by the request that binds the plan with `400 VALIDATION_ERROR`.

---

## Webhook payload schema

Terminal session events are delivered as CloudEvents v1.0.2 envelopes signed with HMAC-SHA256. For the full payload schema, retry policy, and signature verification rules, see [§14 Workspace Plan Schema — Webhook Delivery Model](https://github.com/lennylabs/lenny/blob/main/spec/14_workspace-plan-schema.md) and the [CloudEvents Catalog](cloudevents-catalog).

---

## Validation and versioning

- Plans are validated against the registered JSON Schema (`schemaVersion`) by the request that binds them: `POST /v1/sessions`, `POST /v1/sessions/start`, or the `POST /v1/sessions/{id}/finalize` body. Invalid plans are rejected with `400 WORKSPACE_PLAN_INVALID`.
- An `uploadRef` that is not an upload reference, or that names another session's upload, is rejected with `400 VALIDATION_ERROR` and `details.reason` set to `invalid_upload_ref` or `upload_ref_foreign_session`.
- Schema migrations follow the 90-day durable-consumer SLA in Spec §15.5.
- `runtimeOptions` is validated against the target runtime's schema when registered; otherwise a `RuntimeOptionsUnschematized` warning event is emitted.

---

## Related

- [REST API: `POST /v1/sessions`](../api/rest) — where the plan is submitted
- [CloudEvents Catalog](cloudevents-catalog) — webhook envelope shapes
- [Configuration Reference](configuration) — deployer-configurable defaults and blocklists
- [Spec §14 — Workspace Plan Schema](https://github.com/lennylabs/lenny/blob/main/spec/14_workspace-plan-schema.md) (source of truth)
