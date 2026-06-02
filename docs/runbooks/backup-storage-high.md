---
layout: default
title: "backup-storage-high"
parent: "Runbooks"
triggers:
  - alert: BackupStorageHigh
    severity: warning
components:
  - backup
symptoms:
  - "backup object storage above 80 percent of quota"
  - "retention may need tightening or the bucket needs resizing"
tags:
  - backup
  - storage
  - disaster-recovery
requires:
  - admin-api
related:
  - backup-failed
  - storage-quota-high
  - minio-unavailable
---

# backup-storage-high

Backup object storage in MinIO has exceeded 80 percent of its provisioned quota. New backups will begin to fail once the bucket fills, which puts the recovery-point objective at risk.

## Trigger

`BackupStorageHigh` — `lenny_backup_storage_used_bytes / lenny_backup_storage_quota_bytes > 0.80`.

## Diagnosis

### Step 1 — Read the current retention policy

<!-- access: api method=GET path=/v1/admin/backups/policy -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups/policy" | jq '{retainDays, retainCount, retainMinFull, preRestoreRetainDays}'
```

A high `retainDays` or `retainCount` keeps more archives than the bucket can hold. The pre-restore retention window (`preRestoreRetainDays`, default 7) governs the automatic safety backups created by restore execution; repeated failed restores accumulate pre-restore backups that the regular policy does not prune.

### Step 2 — Identify the largest consumers

<!-- access: api method=GET path=/v1/admin/backups -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups?limit=50" | jq -r '.backups[] | "\(.sizeBytes)\t\(.type)\t\(.id)\t\(.status)"' | sort -rn | head
```

A run of pre-restore backups indicates restore churn. A run of large full backups indicates the retention window is wider than the bucket sizing assumed.

## Remediation

1. **Tighten retention** when the policy is wider than the bucket sizing. A value can be raised again later; it cannot be lowered below `retainMinFull: 1` and `retainCount: 1` (the chart refuses zero-retention configs).

<!-- access: api method=PUT path=/v1/admin/backups/policy -->
```bash
curl -sS -X PUT "$LENNY_OPS_URL/v1/admin/backups/policy" \
  -H 'Content-Type: application/json' \
  -d '{"retainDays": 30, "retainCount": 10, "retainMinFull": 3}' | jq
```

The retention sweep runs after each successful backup and on the daily 03:30 UTC cron; the tightened policy takes effect on the next sweep.

2. **Resize the bucket** when retention already matches the recovery requirement and the storage needs more headroom. Raise the backup bucket quota on the MinIO deployment (or the cloud object-store equivalent).
3. **Clear pre-restore churn** by resolving the failing restore (see [restore-failure-recovery.md](./restore-failure-recovery.md)); the pre-restore backups age out under `preRestoreRetainDays`.

## Verification

`lenny_backup_storage_used_bytes / lenny_backup_storage_quota_bytes` drops below 0.80 after the next retention sweep or the quota increase, and the alert clears.

## Escalation

Page platform engineering when storage stays above quota after retention has been tightened to the recovery minimum and the bucket cannot be resized. A backup bucket that fills causes `BackupFailed`; treat a sustained full bucket as a recovery-point risk.

Cross-reference: [§17.3](../../spec/17_deployment-topology.md#173-disaster-recovery), [§25.11](../../spec/25_agent-operability.md#2511-backup-and-restore-api).
