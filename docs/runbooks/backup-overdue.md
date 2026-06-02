---
layout: default
title: "backup-overdue"
parent: "Runbooks"
triggers:
  - alert: BackupOverdue
    severity: warning
components:
  - backup
symptoms:
  - "no successful full backup within the 48h window"
  - "lenny_backup_last_successful_timestamp{type=full} stale"
tags:
  - backup
  - disaster-recovery
requires:
  - admin-api
related:
  - backup-failed
  - backup-storage-high
  - restore-execute
---

# backup-overdue

A full backup has not completed within the expected 48h window. The recovery-point objective for the Postgres pipeline is at risk until a fresh full backup completes.

## Trigger

`BackupOverdue` — `time() - lenny_backup_last_successful_timestamp{type="full"}` exceeds 48h.

## Diagnosis

### Step 1 — Read the most recent backups

<!-- access: api method=GET path=/v1/admin/backups -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups?type=full&limit=5" | jq '.backups[] | {id, status, startedAt, completedAt, error}'
```

A run in `failed` or stuck `pending` explains the stale timestamp. A run in `running` for longer than `spec.activeDeadlineSeconds` (2h) has exceeded its deadline and will be killed.

### Step 2 — Confirm the schedule is enabled

<!-- access: api method=GET path=/v1/admin/backups/schedule -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups/schedule" | jq '{full, postgres, enabled}'
```

`enabled: false` means scheduled backups are paused and the cron evaluator skips every fire. A disabled schedule is the most common cause of a silent overdue state.

### Step 3 — Check the leader and the K8s API

When no `ops_backups` row exists for the missed window, the leader-elected cron evaluator did not fire. Confirm a leader holds the `lenny-ops-leader` Lease and that `lenny-ops` can reach the Kubernetes API. A failure to create the Job surfaces as `503 BACKUP_JOB_CREATION_FAILED` on a manual trigger.

## Remediation

1. **If the schedule is disabled and should not be:** re-enable it with `PUT /v1/admin/backups/schedule` and `{"enabled": true}`.
2. **If a Job failed:** follow [backup-failed.md](./backup-failed.md) to resolve the underlying cause, then trigger a fresh full backup.
3. **Trigger an immediate full backup** once the cause is resolved:

<!-- access: api method=POST path=/v1/admin/backups -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/backups" \
  -H 'Content-Type: application/json' \
  -d '{"type": "full", "confirm": true}' | jq '{id, status}'
```

`confirm: true` is required for a full backup in production.

## Verification

`GET /v1/admin/backups?type=full&limit=1` reports a `completed` run with a recent `completedAt`, and `lenny_backup_last_successful_timestamp{type="full"}` advances. The alert clears on the next evaluation.

## Escalation

Page platform engineering when a manually triggered full backup fails to start (`BACKUP_JOB_CREATION_FAILED`) or when repeated runs fail for a cause that is not storage capacity. A backup outage longer than the tier's RPO target is a recovery-point compliance issue; notify the data-protection owner.

Cross-reference: [§17.3](../../spec/17_deployment-topology.md#173-disaster-recovery), [§25.11](../../spec/25_agent-operability.md#2511-backup-and-restore-api).
