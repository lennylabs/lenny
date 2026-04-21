---
layout: default
title: WorkspacePlan Schema
parent: Reference
nav_order: 8
description: Declarative specification for how a session's workspace is prepared — sources, setup commands, env, timeouts, retry policy, delegation lease.
---

# WorkspacePlan Schema

The `WorkspacePlan` is the declarative specification for how a session's workspace is prepared. It is supplied at session creation (`POST /v1/sessions`) and controls workspace source materialization, setup commands, environment, timeouts, retry behavior, callbacks, credentials, and delegation budgets.

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
      { "type": "uploadFile", "path": "src/main.ts", "uploadRef": "upload_abc123" },
      { "type": "uploadArchive", "pathPrefix": ".", "uploadRef": "upload_def456", "format": "tar.gz" },
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
| `uploadFile` | `path`, `uploadRef` | Materialize a single file previously uploaded via `POST /v1/sessions/{id}/upload`. |
| `uploadArchive` | `pathPrefix`, `uploadRef`, `format` | Extract an uploaded archive (`tar.gz`, `zip`) under `pathPrefix`. |
| `mkdir` | `path` | Create an empty directory (useful for output collection). |
| `gitClone` | `url`, `ref`, `path` | Clone a Git repository at the specified ref into `path`. Optional `depth`, `submodules`, and `auth` (credential reference). The gateway resolves `ref` to a commit SHA at session-creation time and writes it back as `resolvedCommitSha` on the stored source (readable via `GET /v1/sessions/{id}`); unresolvable refs fail with `422 GIT_CLONE_REF_UNRESOLVABLE`. |

**File mode (`inlineFile`, `mkdir`).** The optional `mode` field on these sources is a POSIX-style octal string matching the regex `^0[0-7]{3,4}$` (e.g., `"0644"`, `"0755"`). The setuid (`04000`) and setgid (`02000`) bits are prohibited for all source types; the sticky bit (`01000`) is accepted only on `mkdir`. Invalid values are rejected at session creation with `400 VALIDATION_ERROR`.

---

## Webhook payload schema

Terminal session events are delivered as CloudEvents v1.0.2 envelopes signed with HMAC-SHA256. For the full payload schema, retry policy, and signature verification rules, see [§14 Workspace Plan Schema — Webhook Delivery Model](https://github.com/lennylabs/lenny/blob/main/spec/14_workspace-plan-schema.md) and the [CloudEvents Catalog](cloudevents-catalog).

---

## Validation and versioning

- Plans are validated against the registered JSON Schema (`schemaVersion`) at session creation. Invalid plans are rejected with `400 VALIDATION_ERROR`.
- Schema migrations follow the 90-day durable-consumer SLA in Spec §15.5.
- `runtimeOptions` is validated against the target runtime's schema when registered; otherwise a `RuntimeOptionsUnschematized` warning event is emitted.

---

## Related

- [REST API: `POST /v1/sessions`](../api/rest) — where the plan is submitted
- [CloudEvents Catalog](cloudevents-catalog) — webhook envelope shapes
- [Configuration Reference](configuration) — deployer-configurable defaults and blocklists
- [Spec §14 — Workspace Plan Schema](https://github.com/lennylabs/lenny/blob/main/spec/14_workspace-plan-schema.md) (source of truth)
