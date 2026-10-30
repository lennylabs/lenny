---
layout: default
title: "restore-failure-recovery"
parent: "Runbooks"
components:
  - backup
  - restore
symptoms:
  - "a restore Job failed and left a partial-restore state"
  - "the restore:platform lock is held after a failed restore"
tags:
  - restore
  - backup
  - disaster-recovery
requires:
  - admin-api
  - cluster-access
related:
  - restore-execute
  - pre-restore-backup-retention
  - backup-reconcile-blocked
  - db-rollback
---

# restore-failure-recovery

A restore Job failed and left the platform in a partial-restore state. Sessions targeting restored shards may succeed while sessions targeting unrestored shards may fail with stale data. The `restore:platform` remediation lock is held and is not auto-released, which blocks a competing restore against partially-restored state.

## Trigger

This runbook is operator-initiated, reached from [restore-execute.md](./restore-execute.md) when `GET /v1/admin/restore/{id}/status` reports `failed`.

## Diagnosis

### Step 1 — Read the per-shard failure

<!-- access: api method=GET path=/v1/admin/restore/{id}/status -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/restore/<restoreId>/status" | jq '{status, failedShard, error, shardStates}'
```

`failedShard` and `error` identify the shard whose `pg_restore` failed and the cause. The `shardStates` map distinguishes shards already `completed` from those still pending; resume restores only the incomplete shards.

### Step 2 — Confirm lock ownership

<!-- access: api method=GET path=/v1/admin/remediation-locks -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/remediation-locks" | jq '.locks[] | select(.scope=="restore:platform") | {id, acquiredBy, acquiredAt}'
```

Resume requires the caller to be the current `acquiredBy`. If another operator stole the lock, the new holder owns the resume; the original operator must steal it back to regain control.

### Step 3 — Fix the underlying cause

Resolve the cause the failed shard reported (free storage space, correct a schema mismatch, or restore Postgres or MinIO connectivity) before attempting recovery. A resume against an unresolved cause fails the same shard again.

## Remediation

Choose one recovery path.

1. **Resume the same restore** once the cause is fixed and the lock is held. Resume is idempotent: completed shards are skipped.

<!-- access: api method=POST path=/v1/admin/restore/resume -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/restore/resume?restoreId=<restoreId>" | jq '{restoreId, status, jobId}'
```

A released or expired lock returns `409 RESTORE_LOCK_REQUIRED`; re-acquire the `restore:platform` lock (`POST /v1/admin/remediation-locks` with scope `restore:platform`) before retrying.

2. **Restore to an older backup.** Release the held lock (`DELETE /v1/admin/remediation-locks/{id}` if still the `acquiredBy`, or steal it; both are audited), then start a fresh `restore/execute` against the earlier backup. The new call acquires its own lock and the `acknowledgeDataLoss: true` requirement still applies.
3. **Manual per-shard repair.** For deeply partial failures, access Postgres directly via the Total-Outage recovery path in [db-rollback.md](./db-rollback.md). Per-shard rollback is an operator-driven Postgres workflow rather than an API primitive.

## Verification

The restore status reaches `completed` with every shard `completed`, the `restore:platform` lock is released, and the gateway serves traffic after its post-restore restart. The pre-restore safety backup remains available for the full `preRestoreRetainDays` window.

## Escalation

Page platform engineering for a restore that fails to resume after the cause is resolved. Page security and compliance when the post-restore GDPR erasure reconciler blocks (see [backup-reconcile-blocked.md](./backup-reconcile-blocked.md)). Do not direct production traffic to the platform until recovery completes.

Cross-reference: [§17.3](../../spec/17_deployment-topology.md#173-disaster-recovery), [§25.11](../../spec/25_agent-operability.md#2511-backup-and-restore-api).
