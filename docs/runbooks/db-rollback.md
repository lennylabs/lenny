---
layout: default
title: "db-rollback"
parent: "Runbooks"
triggers: []
components:
  - postgres
  - controlPlane
symptoms:
  - "a recent migration's down DDL is unsafe given data written since the up migration ran"
  - "a coordinated rollback across Postgres + gateway + lenny-ops is required"
  - "the desired pre-incident state must be restored from a backup snapshot"
tags:
  - postgres
  - rollback
  - migrations
  - backup
  - restore
requires:
  - admin-api
  - cluster-access
related:
  - schema-migration-failure
  - postgres-failover
  - crd-upgrade
  - drift-snapshot-refresh
---

# db-rollback

This runbook covers the broader database rollback procedure referenced by `schema-migration-failure.md` Step 3 (down migration) and §17.7. Use it when a single down migration is insufficient — typically when the up-migration backfilled rows, dropped a column with live data, or applied a destructive transformation, and the down DDL would silently lose information.

The procedure restores Postgres to a known-good snapshot, then re-applies forward state up to a verified version. It is coordinated with the gateway and lenny-ops upgrades so the platform restarts against a schema the deployed code understands.

## Trigger

- A failed or partially applied migration cannot be repaired with the per-migration down DDL.
- A rollback decision has been escalated to platform engineering and approved.
- A production incident has surfaced data corruption that can only be remediated by restoring from backup.

## Decision tree

The rollback path depends on whether the up migration was destructive and how recently it ran. Follow the matching branch.

### Branch A — Migration is reversible by down DDL

Use the per-migration down DDL path documented in [schema-migration-failure.md](./schema-migration-failure.md#step-3--down-migration-last-resort). Do not enter this runbook unless that path was rejected.

### Branch B — Migration is partially destructive (recoverable)

Restore the dropped columns or tables from a recent backup while preserving newer rows in surviving columns.

### Branch C — Migration corrupted live data (full restore)

Restore Postgres from the most recent verified backup and replay forward to the desired pre-incident version.

## Diagnosis

### Step 1 — Identify the migration window

<!-- access: lenny-ctl -->
```bash
lenny-ctl migrate status
```

Record the current version and the version you intend to roll back to.

### Step 2 — Identify the most recent verified backup

<!-- access: api method=GET path=/v1/admin/backups -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/backups?status=verified" \
  | jq '.backups[] | {id, completed_at, postgres_lsn, gateway_image_tag, controller_image_tag}'
```

The selected backup determines the rollback floor. Confirm with the release engineer that it predates the destructive change.

### Step 3 — Preview the restore

<!-- access: api method=POST path=/v1/admin/restore/preview -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/restore/preview" \
  -H 'content-type: application/json' \
  --data '{"backup_id":"<id>"}'
```

The preview reports `dataLossEstimate.mutationsSinceBackup`, the artifact replication lag, and the estimated downtime. Share the preview with on-call and confirm the data-loss window is acceptable.

## Remediation

### Step 1 — Quiesce writes

Stop new writes before the rollback so the restored state is consistent. Pause the gateway by scaling it to zero (gateway `preStop` drains in-flight sessions) and pause lenny-ops cron jobs.

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system scale deployment/lenny-gateway --replicas=0
kubectl -n lenny-system patch cronjob/lenny-backup -p '{"spec":{"suspend":true}}'
kubectl -n lenny-system patch cronjob/lenny-gc -p '{"spec":{"suspend":true}}'
```

### Step 2 — Run the safety check

<!-- access: api method=POST path=/v1/admin/restore/safety-check -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/restore/safety-check" \
  -H 'content-type: application/json' \
  --data '{"backup_id":"<id>"}'
```

A `safe: false` response names the blocking precondition (open delegations, in-flight credential rotations, billing flush in progress). Resolve each blocker before proceeding.

### Step 3 — Restore Postgres

<!-- access: api method=POST path=/v1/admin/restore -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/restore" \
  -H 'content-type: application/json' \
  --data '{"backup_id":"<id>","confirm":true,"acknowledge_data_loss":true}'
```

The endpoint launches the restore Job, replays Postgres + KMS-envelope state, and emits the `restore.requested`, `restore.started`, and `restore.completed` audit events. Monitor `GET /v1/admin/restore/status` until phase `completed`.

### Step 4 — Re-apply forward migrations to the desired version

If the rollback target is a forward version (not the backup's exact version), re-run `lenny-ctl migrate` to advance to the desired version. Skip when the backup already matches the desired version.

<!-- access: lenny-ctl -->
```bash
lenny-ctl migrate up --version <target>
lenny-ctl migrate status
```

### Step 5 — Resume the platform

Bring the gateway back online against the restored schema. Confirm the gateway image tag matches the schema version (mismatched code + schema is the failure mode this runbook exists to avoid).

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system patch cronjob/lenny-backup -p '{"spec":{"suspend":false}}'
kubectl -n lenny-system patch cronjob/lenny-gc -p '{"spec":{"suspend":false}}'
kubectl -n lenny-system scale deployment/lenny-gateway --replicas=<original>
kubectl -n lenny-system rollout status deployment/lenny-gateway --timeout=5m
```

### Step 6 — Verify

<!-- access: api method=GET path=/v1/admin/ops/health -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/ops/health" | jq '{status, components}'
```

Confirm every component is `healthy`. Run a smoke session to verify session creation and the credential-pool flow.

### Step 7 — Refresh the drift snapshot

The rollback changes the desired-state baseline. Refresh the snapshot before clearing the incident so subsequent drift runs do not warn against the restored state.

Follow [drift-snapshot-refresh.md](./drift-snapshot-refresh.md) Step 5 with the reconciled desired state.

## Post-rollback checklist

1. Notify on-call, the release engineer, and the data team that the rollback completed and which data window was lost.
2. Reconcile the GitOps Helm values file and the migration sources with the restored version.
3. Author a follow-up migration (forward-fix) that addresses the original failure, including the destructive-change guardrail that the incident exposed.
4. Schedule a post-incident review covering the migration that failed, the data-loss window, and the recovery time.

## Escalation

Escalate to:

- **DBA / database-operations** for any rollback that exceeds the preview's reported data-loss estimate.
- **Platform engineering** for rollbacks that span multiple migration phases or require coordinated CRD rollback (see [crd-upgrade.md](./crd-upgrade.md)).
- **Security / compliance** when the rolled-back window includes audit, billing, or credential events — restoring an audit log may require a `RedactionReceipt` for the lost window.

Cross-reference: Spec §10.5 (expand-contract discipline), §17.3 (Disaster recovery — backup/restore), §11.7 (Audit chain re-sealing after restore).
