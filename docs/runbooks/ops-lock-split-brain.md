---
layout: default
title: "ops-lock-split-brain"
parent: "Runbooks"
triggers:
  - alert: OpsLockSplitBrainDetected
    severity: critical
components:
  - ops
  - leader-election
symptoms:
  - "two lenny-ops replicas briefly held the same remediation lock"
  - "outage-epoch reconciliation resolved the conflict"
tags:
  - lenny-ops
  - leader-election
  - locks
  - reconciliation
requires:
  - admin-api
  - cluster-access
related:
  - controller-leader-election
---

# ops-lock-split-brain

Two `lenny-ops` replicas briefly believed they held the same remediation lock. Outage-epoch reconciliation resolved the conflict, but the event requires auditing because a split-brain window means two operators may have observed conflicting confirmations.

## Trigger

`OpsLockSplitBrainDetected` — `lenny_ops_lock_split_brain_detected_total > 0`.

## Diagnosis

### Step 1 — Identify the affected lock

<!-- access: api method=GET path=/v1/admin/locks -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/locks" | jq '.locks[] | {name, holder, acquired_at, outage_epoch}'
```

Cross-reference the alert labels — the affected lock is the one whose `outage_epoch` advanced in the alert window.

### Step 2 — Replay the audit trail

<!-- access: api method=GET path=/v1/admin/audit-events -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/audit-events?event_type=ops.lock.acquired,ops.lock.released,ops.lock.outage_epoch_advanced&since=15m" \
  | jq '.events[] | {recorded_at, event_type, actor_id, resource_id}'
```

A pair of `lock.acquired` events from different `actor_id`s with overlapping timestamps confirms the split-brain.

### Step 3 — Confirm reconciliation

<!-- access: api method=GET path=/v1/admin/audit-events -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/audit-events?event_type=ops.lock.split_brain_reconciled&since=15m" | jq
```

A `split_brain_reconciled` event confirms the platform converged. Absence means the reconciler did not run — escalate.

## Remediation

1. **If reconciliation completed:** review the remediation actions taken under the conflicting locks (`GET /v1/admin/audit-events?event_type=remediation.*&since=15m`). Document any duplicated or conflicting confirmations in the incident record.
2. **If reconciliation did not complete:** restart the `lenny-ops` Deployment to force a fresh leader election: `kubectl -n lenny-system rollout restart deployment/lenny-ops`. Confirm the locks list resolves to a single holder per name.
3. Re-issue any remediation that was rolled back as part of the reconciliation.

## Verification

`GET /v1/admin/locks` returns one holder per lock and the alert clears.

## Escalation

Page platform engineering for a split-brain that does not reconcile within one Deployment restart. Notify on-call lead when remediation actions were duplicated under the conflicting locks.

Cross-reference: [§25.4](../../spec/25_agent-operability.md#254-remediation-locks-and-escalations), [§10.4](../../spec/10_gateway-internals.md#104-runtime-extensibility).
