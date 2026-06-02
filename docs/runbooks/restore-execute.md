---
layout: default
title: "restore-execute"
parent: "Runbooks"
components:
  - backup
  - restore
symptoms:
  - "a restore from backup is required to recover platform state"
  - "operator needs the safety-check and confirm workflow for restore/execute"
tags:
  - restore
  - backup
  - disaster-recovery
requires:
  - admin-api
  - cluster-access
related:
  - restore-failure-recovery
  - pre-restore-backup-retention
  - backup-reconcile-blocked
  - db-rollback
---

# restore-execute

The day-2 procedure for restoring platform state from a backup. Restore is destructive: it requires `confirm: true` and, for any backup that is not a safe restore point, `acknowledgeDataLoss: true`. The procedure takes the `restore:platform` remediation lock and creates a pre-restore safety backup before the restore Job runs.

## Trigger

This runbook is operator-initiated rather than alert-driven. Use it after diagnosing an incident that requires reverting to a backup rather than a narrower remediation.

## Diagnosis

### Step 1 — Identify the target backup

<!-- access: api method=GET path=/v1/admin/backups -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups?type=full&status=completed&limit=10" | jq '.backups[] | {id, completedAt, platformVersion, schemaVersion}'
```

A restore is forward-only: an older backup restores onto the current platform, and a backup newer than the running version is incompatible.

### Step 2 — Verify the archive

<!-- access: api method=POST path=/v1/admin/backups/{id}/verify -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/backups/<id>/verify" | jq '{backupId, status, jobId}'
```

Verification validates the SHA-256 checksum and runs `pg_restore --list` against the archive. It is required before a production restore.

### Step 3 — Run the safety check

<!-- access: api method=GET path=/v1/admin/restore/safety-check -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/restore/safety-check?backupId=<id>" | jq '{safe, dataLossEstimate, compatibility, recommendedAction}'
```

`safe: true` is returned only when the backup is recent (younger than 5 minutes) or the platform has been idle. In most cases `safe: false` and the `dataLossEstimate` reports the mutations, sessions, and audit events written since the backup.

### Step 4 — Preview the restore

<!-- access: api method=POST path=/v1/admin/restore/preview -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/restore/preview" \
  -H 'Content-Type: application/json' \
  -d '{"backupId": "<id>"}' | jq '{estimatedDowntime, requiresFullStop, artifactReplicationLagSeconds, estimatedOrphanArtifactRows, warnings}'
```

`artifactReplicationLagSeconds` reports the ArtifactStore off-cluster replication lag. To minimize orphaned artifact rows, choose a backup whose `completedAt` is at or before `now() - artifactReplicationLagSeconds`. The `estimatedDowntime` drives stakeholder coordination.

## Remediation

1. **Notify stakeholders.** Restore causes downtime; coordinate with affected tenants using the preview's `estimatedDowntime`.
2. **Execute the restore** with `confirm: true`, adding `acknowledgeDataLoss: true` when the safety check is not safe:

<!-- access: api method=POST path=/v1/admin/restore/execute -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/restore/execute" \
  -H 'Content-Type: application/json' \
  -d '{"backupId": "<id>", "confirm": true, "acknowledgeDataLoss": true}' | jq '{restoreId, status, jobId, preRestoreBackupId}'
```

A request without `confirm` returns a dry-run preview and mutates nothing. A confirmed but unacknowledged request against an unsafe backup returns `400 RESTORE_ACKNOWLEDGE_REQUIRED`. A competing restore returns `REMEDIATION_LOCK_CONFLICT`.

3. **Monitor progress** on the restore status:

<!-- access: api method=GET path=/v1/admin/restore/{id}/status -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/restore/<restoreId>/status" | jq '{status, shardStates, failedShard, error}'
```

A failed restore keeps the `restore:platform` lock held and does not auto-release it; follow [restore-failure-recovery.md](./restore-failure-recovery.md).

## Verification

The restore status reaches `completed`, every entry in `shardStates` is `completed`, and the gateway serves traffic after its post-restore restart. The pre-restore safety backup is retained under `preRestoreRetainDays` (see [pre-restore-backup-retention.md](./pre-restore-backup-retention.md)).

## Escalation

Page platform engineering for any restore that fails a shard, and page security and compliance when the post-restore GDPR erasure reconciler blocks (see [backup-reconcile-blocked.md](./backup-reconcile-blocked.md)). For deeply partial failures, use the Total-Outage recovery path in [db-rollback.md](./db-rollback.md).

Cross-reference: [§17.3](../../spec/17_deployment-topology.md#173-disaster-recovery), [§25.11](../../spec/25_agent-operability.md#2511-backup-and-restore-api).
