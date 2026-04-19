---
layout: default
title: "workspace-seal-stuck"
parent: "Runbooks"
triggers:
  - alert: WorkspaceSealStuck
    severity: warning
components:
  - gateway
symptoms:
  - "seal operation retrying beyond maxWorkspaceSealDurationSeconds"
  - "session completion blocked on finalize"
  - "artifact upload not progressing"
tags:
  - workspace
  - seal
  - finalize
  - artifacts
requires:
  - admin-api
  - cluster-access
related:
  - minio-failure
  - checkpoint-stale
---

# workspace-seal-stuck

A workspace seal operation (the finalization step that snapshots and uploads the pod's workspace to object storage) is retrying past `maxWorkspaceSealDurationSeconds`. The session cannot reach `completed` state until the seal succeeds or fails definitively.

## Trigger

- `WorkspaceSealStuck` alert.
- Session in state `sealing` past the configured max duration.

## Diagnosis

### Step 1 — Affected sessions

<!-- access: api method=GET path=/v1/admin/sessions -->
```
GET /v1/admin/sessions?state=sealing&ageSeconds=gt:<maxSealSeconds>
```

Each returns: `sessionId`, `podName`, `sealStartedAt`, `lastRetryAt`, `retryCount`, `lastError`.

### Step 2 — Last error class

Typical `lastError` values:

- `minio_dial_tcp` / `s3_request_timeout` — object store [minio-failure](minio-failure.html).
- `workspace_too_large` — workspace exceeded configured max.
- `archive_fs_error` — pod-side tar / stream error; pod may be unhealthy.
- `credential_denied` — uploader's credential is revoked or mis-scoped.

### Step 3 — Pod health

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pod <pod-name> -n <namespace>
kubectl describe pod <pod-name> -n <namespace> | tail -30
```

A crashed or Terminating pod cannot complete a seal; the retry path lives in the gateway.

### Step 4 — Size

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, lenny_workspace_seal_size_bytes)&window=1h
```

Oversized workspaces correlate with slow seals; Spec §6.4 defines size limits.

## Remediation

### Step 1 — Storage-side error

Follow [minio-failure](minio-failure.html). Once the object store is healthy, the gateway retries in-flight seals automatically.

### Step 2 — Workspace too large

Workspaces > `workspace.maxSealBytes` cannot be sealed. Options:

1. Work with the tenant to reduce workspace size (exclude build caches, logs, etc.).
2. If the size is legitimate, raise `workspace.maxSealBytes` in Helm values after confirming the object-store impact.

### Step 3 — Pod-side error

If the pod itself is unhealthy and cannot stream the archive:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin sessions fail-sealed <session-id> \
  --reason "workspace_seal_failed" --retain-partial-upload
```

Marks the session as failed; any partial upload is retained for forensic review.

### Step 4 — Credential

If uploader credentials were revoked, rotate and confirm the uploader ServiceAccount has `WriteObject` on the artifacts bucket.

### Step 5 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_workspace_seal_duration_seconds&window=15m
```

- No sessions in `sealing` past the max duration.
- Seal p95 returns to baseline.
- Alert clears.

## Escalation

Escalate to:

- **Object-store operators** for sustained upload failures that aren't covered by [minio-failure](minio-failure.html).
- **Tenant account team** for oversized workspace decisions — they own the conversation about size limits.
- **Platform engineering** for pod-side archive errors that recur — may indicate a runtime-image defect.
