---
layout: default
title: "pre-restore-backup-retention"
parent: "Runbooks"
components:
  - backup
  - restore
symptoms:
  - "pre-restore safety backups accumulating from repeated restores"
  - "uncertainty about the retention window for pre-restore backups"
tags:
  - backup
  - restore
  - retention
  - disaster-recovery
requires:
  - admin-api
related:
  - restore-execute
  - restore-failure-recovery
  - backup-storage-high
---

# pre-restore-backup-retention

Each `restore/execute` creates a pre-restore safety backup before the restore Job runs, so a botched restore can be reverted. These backups follow `backups.retention.preRestoreRetainDays` (default 7) independently of the regular retention policy so that repeated failed restores do not accumulate without bound.

## Trigger

This runbook is operator-initiated. Use it to understand the pre-restore retention window, to locate a pre-restore backup for a revert, or when repeated restores have produced a run of pre-restore backups.

## Diagnosis

### Step 1 — List the pre-restore backups

<!-- access: api method=GET path=/v1/admin/backups -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups?type=pre-restore&limit=50" | jq '.backups[] | {id, status, startedAt, expiresAt, sizeBytes}'
```

A pre-restore backup is a full backup tagged `pre-restore`. The `expiresAt` field reports when the pre-restore retention window prunes it.

### Step 2 — Read the pre-restore window

<!-- access: api method=GET path=/v1/admin/backups/policy -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups/policy" | jq '{preRestoreRetainDays}'
```

`preRestoreRetainDays` governs only the pre-restore backups. The regular `retainDays` and `retainCount` do not apply to them, so a wide regular policy does not extend the pre-restore window.

## Remediation

1. **Revert a botched restore** by running a fresh `restore/execute` against the pre-restore backup that the failed restore created. The pre-restore backup id is on the restore record:

<!-- access: api method=GET path=/v1/admin/restore/{id}/status -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/restore/<restoreId>/status" | jq '{preRestoreBackupId}'
```

Follow [restore-execute.md](./restore-execute.md) with the `preRestoreBackupId` as the target. A pre-restore backup retained for the failed restore is available for the full `preRestoreRetainDays` window.

2. **Adjust the window** when the default 7 days does not match the recovery requirement:

<!-- access: api method=PUT path=/v1/admin/backups/policy -->
```bash
curl -sS -X PUT "$LENNY_OPS_URL/v1/admin/backups/policy" \
  -H 'Content-Type: application/json' \
  -d '{"retainDays": 30, "retainCount": 10, "retainMinFull": 3, "preRestoreRetainDays": 7}' | jq
```

A longer window keeps more revert points and consumes more backup storage. When pre-restore churn is the dominant storage consumer, follow [backup-storage-high.md](./backup-storage-high.md).

## Verification

`GET /v1/admin/backups?type=pre-restore` shows pre-restore backups expiring at `startedAt + preRestoreRetainDays`, and storage attributable to pre-restore backups stays within the bucket sizing.

## Escalation

Page platform engineering when pre-restore backups fail to expire after the window passes, or when pre-restore churn keeps backup storage above quota despite the window matching the recovery requirement.

Cross-reference: [§17.3](../../spec/17_deployment-topology.md#173-disaster-recovery), [§25.11](../../spec/25_agent-operability.md#2511-backup-and-restore-api).
