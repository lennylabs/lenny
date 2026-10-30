---
layout: default
title: "backup-failed"
parent: "Runbooks"
triggers:
  - alert: BackupFailed
    severity: warning
components:
  - backup
symptoms:
  - "a backup Job terminated with failure"
  - "lenny_backup_total{status=failed} incremented"
tags:
  - backup
  - disaster-recovery
requires:
  - admin-api
  - cluster-access
related:
  - backup-overdue
  - backup-storage-high
  - minio-unavailable
  - postgres-unavailable
---

# backup-failed

A backup Job terminated with failure. The `ops_backups` row carries the row-level reason and the failed Job is retained for post-mortem inspection.

## Trigger

`BackupFailed` — `increase(lenny_backup_total{status="failed"}[1h]) > 0`.

## Diagnosis

### Step 1 — Read the failed backup's reason

<!-- access: api method=GET path=/v1/admin/backups -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups?status=failed&limit=5" | jq '.backups[] | {id, type, startedAt, error}'
```

The `error` field carries the row-level cause. `JOB_CREATE_FAILED` means the Kubernetes Job was never created (the reconciler failed a pending row after 2 minutes). A `pg_dump` or upload failure carries the in-pod cause.

### Step 2 — Inspect the failed Job

<!-- access: cluster -->
```bash
kubectl -n lenny-system describe job -l lenny.dev/backup-id=<id>
kubectl -n lenny-system logs job/<job-name>
```

The Job is retained for one hour after it finishes (`ttlSecondsAfterFinished: 3600`). The pod logs distinguish a Postgres connectivity failure, a MinIO upload failure, and a `BACKUP_REGION_UNRESOLVABLE` residency abort.

### Step 3 — Classify the cause

- **Postgres unreachable:** the `lenny-backup` role cannot connect. See [postgres-unavailable.md](./postgres-unavailable.md).
- **MinIO upload failed:** the destination bucket is unreachable or full. See [minio-unavailable.md](./minio-unavailable.md).
- **`BACKUP_REGION_UNRESOLVABLE`:** a shard's resolved region has no `backups.regions.<region>` entry, or the region endpoint is unreachable. See [data-residency-violation.md](./data-residency-violation.md).
- **Quota:** the backup bucket is above its quota. See [backup-storage-high.md](./backup-storage-high.md).

## Remediation

1. Resolve the classified cause (restore Postgres or MinIO connectivity, correct the region configuration, or free backup storage).
2. Trigger a fresh backup of the failed type:

<!-- access: api method=POST path=/v1/admin/backups -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/backups" \
  -H 'Content-Type: application/json' \
  -d '{"type": "full", "confirm": true}' | jq '{id, status}'
```

3. The Job retries at the Job level up to `backoffLimit: 3` before it is marked failed, so a transient cause may clear on its own. A persistent failure needs the cause resolved first.

## Verification

The replacement backup reaches `completed`, `GET /v1/admin/backups/{id}` shows a size and checksum, and `lenny_backup_total{status="failed"}` stops incrementing.

## Escalation

Page platform engineering when backups fail repeatedly for a cause that is not transient connectivity or storage capacity. A residency-violation abort is a compliance fault; follow [data-residency-violation.md](./data-residency-violation.md) and notify the data-protection owner.

Cross-reference: [§17.3](../../spec/17_deployment-topology.md#173-disaster-recovery), [§25.11](../../spec/25_agent-operability.md#2511-backup-and-restore-api).
