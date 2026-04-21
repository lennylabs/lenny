---
layout: default
title: "legal-hold-quota-pressure"
parent: "Runbooks"
triggers:
  - alert: LegalHoldCheckpointAccumulationProjectedBreach
    severity: warning
components:
  - compliance
  - objectStore
symptoms:
  - "legal-hold-protected session checkpoint growth projected to exceed tenant storage headroom"
  - "checkpoint-accumulation pre-breach on a held session"
tags:
  - compliance
  - legal-hold
  - storage
  - quota
requires:
  - admin-api
related:
  - storage-quota-high
  - erasure-job-failed
  - tenant-deletion-overdue
---

# legal-hold-quota-pressure

A legal-hold-protected session's projected checkpoint growth will consume 90% of the tenant's remaining storage headroom before the hold is cleared. Left unaddressed, the tenant will hit `STORAGE_QUOTA_EXCEEDED` while deletion remains blocked by the hold.

## Trigger

- `LegalHoldCheckpointAccumulationProjectedBreach` — predictive projection from `lenny_legal_hold_checkpoint_projected_growth_bytes` against remaining `storageQuotaBytes` headroom.

## Diagnosis

### Step 1 — Identify the tenant, session, and hold

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_legal_hold_checkpoint_projected_growth_bytes&groupBy=tenant_id,root_session_id&window=15m
```

Cross-reference against `lenny_tenant_legal_hold_active_count` for the same tenant and list active holds:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin legal-holds list --tenant <id>
```

### Step 2 — Confirm headroom and growth rate

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_storage_quota_bytes_used&filter=tenant_id:<id>&window=24h
GET /v1/admin/metrics?q=deriv(lenny_checkpoint_storage_bytes_total[1h])&filter=tenant_id:<id>&window=24h
```

## Remediation

### Step 1 — Coordinate with compliance to clear or narrow the hold

Clearing or narrowing an active hold is a compliance decision, not a platform-operator one. The platform cannot delete held data. Contact the compliance officer with the tenant ID, affected session(s), and projected breach time.

### Step 2 — Raise the tenant storage quota

If the hold must remain and the growth is legitimate, increase `storageQuotaBytes` on the tenant so the hold does not force `STORAGE_QUOTA_EXCEEDED`:

<!-- access: api method=PUT path=/v1/admin/tenants/{tenant_id}/quota -->
```
PUT /v1/admin/tenants/<tenant_id>/quota
{"storageQuotaBytes": <new>}
```

Coordinate with finance ops per the [storage-quota-high](storage-quota-high.html) Step 4 flow.

### Step 3 — Route the session to a tighter-workspace pool (optional)

If the session is still live and the pool permits, route future turns to a pool with a smaller `workspaceSizeLimitBytes` to reduce per-checkpoint growth. This only helps prospectively.

### Step 4 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_legal_hold_checkpoint_projected_growth_bytes&filter=tenant_id:<id>&window=15m
```

- Projected growth ratio falls below the warning threshold.
- Alert clears.

## Escalation

Escalate to:

- **Compliance officer / DPO** for every decision on the hold itself.
- **Finance ops** for quota-raise cost implications.
- **Platform engineering** if the projection is implausible relative to observed growth (indicates a metric regression).
