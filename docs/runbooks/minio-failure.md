---
layout: default
title: "minio-failure"
parent: "Runbooks"
triggers:
  - alert: MinIOUnavailable
    severity: critical
  - alert: CheckpointStorageUnavailable
    severity: critical
components:
  - objectStore
symptoms:
  - "workspace upload/download failures"
  - "lenny_artifact_upload_error_total spikes"
  - "checkpoint upload retries exhausted"
tags:
  - storage
  - object-store
  - artifacts
  - checkpoints
requires:
  - admin-api
  - cluster-access
related:
  - postgres-failover
  - session-eviction-loss
---

# minio-failure

The object store (MinIO on-premises, or a cloud-managed S3/GCS/Azure Blob equivalent) is unavailable or degraded. Workspace uploads fail, artifact retrieval returns 5xx, and checkpoint storage falls back to Postgres -- which is a bounded emergency mode.

## Trigger

- `MinIOUnavailable` alert.
- `CheckpointStorageUnavailable` alert (upload failed after all retries).
- `lenny_artifact_upload_error_total` climbing.
- Gateway logs: `minio: dial tcp`, `s3: RequestTimeout`, `NoSuchBucket`.
- `/v1/admin/health` returns `objectStore: degraded` or `unhealthy`.

## Diagnosis

### Step 1 — Reachability

<!-- access: kubectl requires=cluster-access -->
```bash
mc admin info <alias>
mc ls <alias>/lenny-artifacts/
```

The cluster should report all nodes online and the bucket should list. `mc admin heal` progress or an offline drive indicates hardware or erasure-set issues.

### Step 2 — Cluster / erasure-set health

<!-- access: kubectl requires=cluster-access -->
```bash
mc admin heal -r <alias>/lenny-artifacts
mc admin prometheus metrics <alias> | grep -E "minio_cluster|minio_inter_node"
```

If the cluster is below quorum, writes are impossible; read traffic may still succeed against degraded nodes.

### Step 3 — Client-side errors

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-gateway --since=5m | grep -E "minio|s3"
```

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_artifact_upload_error_total&window=15m&groupBy=error_type
```

`dial_tcp`: network or endpoint issue. `access_denied`: credential rotation needed. `no_such_bucket`: configuration drift.

### Step 4 — Bucket-level config

<!-- access: kubectl requires=cluster-access -->
```bash
mc encrypt info <alias>/lenny-artifacts
mc version info <alias>/lenny-artifacts
mc ilm ls <alias>/lenny-artifacts
```

Encryption should be enabled; versioning and ILM rules must match the [lifecycle requirements](https://github.com/lennylabs/lenny/blob/main/spec/17_deployment-topology.md#1794-cloud-object-storage-lifecycle-requirements).

## Remediation

### Step 1 — Partial outage: some nodes unhealthy

1. Identify and restart the failed node(s):
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl describe pod -n <minio-ns> <failed-pod>
   kubectl delete pod -n <minio-ns> <failed-pod>
   ```
2. Watch `mc admin info <alias>` until all drives are online.
3. Artifact uploads and downloads recover automatically once quorum is restored.

### Step 2 — Full outage

1. Inform affected tenants: new session creation is degraded -- workspace finalize will return `INTERNAL_ERROR`.
2. While MinIO is down:
   - **In-flight sessions continue running.** The pod-local workspace is intact. Only upload/download to the artifact store is blocked.
   - **Checkpoints fall back to Postgres.** Checkpoint size is bounded by `checkpoint.postgresFallbackMaxBytes` (default 100 MiB). Larger checkpoints fail fast with `CHECKPOINT_TOO_LARGE_FOR_FALLBACK`.
3. Restore MinIO following your object-store operational procedures (re-seeding erasure sets, restoring from snapshot, or provider-side failover).

### Step 3 — Cloud-managed object store

If you're running against S3, GCS, or Azure Blob:

1. Check the provider status page.
2. Verify IAM / service-account credentials still valid:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl exec deploy/lenny-gateway -- \
     aws s3 ls s3://lenny-artifacts/ --region <region>
   ```
3. No bucket-side action from you is typically needed -- the provider fails over internally.

### Step 4 — After recovery

Verify the post-recovery invariants:

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose object-store
```

- Bucket access works: `mc ls <alias>/lenny-artifacts/workspaces/`.
- Encryption still enabled: `mc encrypt info` -- must be `SSE-KMS` (or cloud equivalent).
- ILM rules present and enabled (versioning + expiration per lifecycle spec).
- `lenny_checkpoint_storage_bytes_total` gauge returning to normal (MinIO writes resume).

### Step 5 — Reconcile sessions whose artifacts may be missing

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT session_id, state FROM sessions
   WHERE state = 'completed' AND completed_at > now() - interval '2 hours';"
```

For each completed session during the outage window, verify artifact presence:

<!-- access: kubectl requires=cluster-access -->
```bash
mc ls <alias>/lenny-artifacts/workspaces/<session-id>/
```

Missing artifacts: the session result was persisted (Postgres state is authoritative) but the workspace tarball was never uploaded. Inform the affected tenant; you cannot reconstruct the workspace.

## Escalation

Escalate to:

- **Cluster admin / storage team** if quorum cannot be restored without node rebuild.
- **Cloud provider support** for managed object stores that do not recover within the provider's RTO.
- **Security on-call** if bucket encryption was disabled during recovery, even briefly -- all affected artifacts must be re-encrypted before returning to steady state.
- **Finance ops** if ILM rules were misconfigured during the outage and unbounded storage was written (billing impact).
