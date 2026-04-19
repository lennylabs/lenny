---
layout: default
title: "tenant-deletion-overdue"
parent: "Runbooks"
triggers:
  - alert: TenantDeletionOverdue
    severity: warning
components:
  - compliance
symptoms:
  - "tenant deletion approaching the deployment-size SLA"
  - "deletion Job stalled or partial"
  - "residual tenant data visible"
tags:
  - compliance
  - tenant-deletion
  - sla
  - data-lifecycle
requires:
  - admin-api
  - cluster-access
related:
  - erasure-job-failed
  - minio-failure
  - postgres-failover
---

# tenant-deletion-overdue

A tenant-deletion request has crossed the warning threshold against the deployment-size SLA without completion. Risk: SLA breach, and (if contractual) potential compliance violation. The warning threshold is deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Trigger

- `TenantDeletionOverdue` alert.
- Tenant in `deleting` state past the SLA window.

## Diagnosis

### Step 1 — Deletion status

<!-- access: api method=GET path=/v1/admin/tenants/{id}/deletion -->
```
GET /v1/admin/tenants/<id>/deletion
```

Returns: `status`, `startedAt`, `phases[]`, `failedPhase`, `residualResources[]`.

### Step 2 — Which phase stalled?

Phases typically include: `sessions_terminated`, `artifacts_deleted`, `tokens_revoked`, `audit_redacted`, `rls_row_purge`, `quota_released`.

The stalled phase tells you which subsystem is the blocker. Common mappings:

| Phase | Likely issue |
|:------|:-------------|
| `artifacts_deleted` | [minio-failure](minio-failure.html), [erasure-job-failed](erasure-job-failed.html) |
| `audit_redacted` | [postgres-failover](postgres-failover.html), constraint violations |
| `tokens_revoked` | [token-service-outage](token-service-outage.html) |

### Step 3 — Residual resources

<!-- access: api method=GET path=/v1/admin/tenants/{id}/deletion -->
```
GET /v1/admin/tenants/<id>/deletion
```

Inspect `residualResources[]` — specific buckets, tables, or leases still present.

## Remediation

### Step 1 — Resume the failed phase

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin tenants resume-deletion <id>
```

<!-- access: api method=POST path=/v1/admin/tenants/{id}/deletion/resume -->
```
POST /v1/admin/tenants/<id>/deletion/resume
```

The deletion controller retries from the last checkpoint.

### Step 2 — Follow the underlying runbook

If the resume surfaces the same error, follow the runbook linked in Step 2 of Diagnosis.

### Step 3 — Manual per-phase cleanup

If automated resumption cannot complete, run per-phase manually via `lenny-ctl admin tenants delete --phase <phase>`. Each phase is idempotent. Always run the phases in the documented order — skipping `sessions_terminated` before `artifacts_deleted` can leave orphan writes.

### Step 4 — Verify completion

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose tenant-deletion <id>
```

- `status: completed`.
- `residualResources: []`.
- Completion audit event recorded:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=tenant.deleted&tenant_id=<id>
```

## Escalation

Escalate to:

- **Compliance officer / DPO** if the SLA is about to be breached and you cannot complete deletion in time — they decide on tenant notification and regulator reporting.
- **Subsystem runbook escalation** for the underlying cause.
- **Platform engineering** if the deletion state machine itself has bugs (e.g., phase completion not being recorded).
