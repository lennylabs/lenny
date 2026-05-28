---
layout: default
title: "backup-reconcile-blocked"
parent: "Runbooks"
triggers:
  - alert: BackupReconcileBlocked
    severity: critical
components:
  - backup
  - compliance
symptoms:
  - "gateway restart held pending operator confirmation of legal-hold ledger currency"
  - "post-restore GDPR erasure reconciler refuses to replay"
tags:
  - backup
  - restore
  - gdpr
  - compliance
requires:
  - admin-api
  - cluster-access
related:
  - db-rollback
  - drift-snapshot-refresh
  - erasure-job-failed
---

# backup-reconcile-blocked

The post-restore GDPR erasure reconciler refused to replay because the legal-hold ledger is stale relative to the restore point. The gateway remains held until an operator confirms ledger currency or accepts a documented divergence.

## Trigger

`BackupReconcileBlocked` — `lenny_backup_reconcile_blocked_total > 0`.

## Diagnosis

### Step 1 — Identify the blocked restore

<!-- access: api method=GET path=/v1/admin/restore/status -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/restore/status" | jq '{phase, blockedReason, ledgerWindowStart, ledgerWindowEnd}'
```

`blockedReason: legal_hold_ledger_stale` confirms the reconciler is the cause.

### Step 2 — Compare ledger and restore windows

<!-- access: api method=GET path=/v1/admin/legal-holds -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/legal-holds?since=<ledgerWindowStart>" | jq '.holds[] | {id, scope, set_at, cleared_at}'
```

Each gap or overlap is a candidate divergence between the restored state and the legal-hold ledger.

## Remediation

1. **If the ledger is current and the divergence is benign:** call `POST /v1/admin/restore/confirm-ledger` with the reviewed window and `{"confirm": true}`. The reconciler resumes replay and the gateway clears the hold.
2. **If the ledger is genuinely stale:** restore the ledger from the most recent verified backup ([db-rollback.md](./db-rollback.md) Branch C) before confirming.
3. **If the restored window contains data that should remain held:** abort the restore (`POST /v1/admin/restore/abort`) and follow the broader restore procedure in [db-rollback.md](./db-rollback.md). Do not bypass the reconciler.

## Verification

`GET /v1/admin/restore/status` reports phase `completed` and the alert clears.

## Escalation

Page security and compliance for any restore that requires confirming a non-trivial ledger divergence. Page platform engineering when the reconciler refuses to clear after ledger restoration.

Cross-reference: [§17.3](../../spec/17_deployment-topology.md#173-disaster-recovery), [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces).
