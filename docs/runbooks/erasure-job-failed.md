---
layout: default
title: "erasure-job-failed"
parent: "Runbooks"
triggers:
  - alert: ErasureJobFailed
    severity: warning
components:
  - compliance
symptoms:
  - "data-erasure Job in Failed state"
  - "tenant deletion overdue"
  - "compliance.erasure_job_failed event"
tags:
  - compliance
  - erasure
  - gdpr
  - data-deletion
requires:
  - admin-api
  - cluster-access
related:
  - tenant-deletion-overdue
  - minio-failure
---

# erasure-job-failed

A scheduled erasure Job (GDPR/CCPA data-deletion job that purges tenant artifacts, audit-log PII, and cached state) failed. Regulatory windows for data deletion are typically tight; failure must be resolved before the deadline.

## Trigger

- `ErasureJobFailed` alert.
- Audit event `compliance.erasure_job_failed`.
- `kubectl get job -n lenny-system -l app=lenny-erasure` shows `Failed` status.

## Diagnosis

### Step 1 — Identify the job and scope

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get jobs -n lenny-system -l app=lenny-erasure --sort-by=.metadata.creationTimestamp
kubectl describe job <job-name> -n lenny-system | head -40
```

Each job annotates the target: `tenant-id`, `scope` (artifacts / audit-pii / cache), `regulatoryDeadline`.

### Step 2 — Failure reason

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs job/<job-name> -n lenny-system | tail -100
```

Common causes:

- **Object store error** — see [minio-failure](minio-failure.html).
- **Postgres constraint** — referential integrity blocking a delete; erasure needs a specific cascade order.
- **Resource limit** — OOM or timeout on a large scope.
- **Permission denied** — ServiceAccount RBAC drift.

### Step 3 — Regulatory deadline

<!-- access: api method=GET path=/v1/admin/erasure-jobs -->
```
GET /v1/admin/erasure-jobs?tenantId=<id>
```

Inspect `regulatoryDeadline` and `status`. Prioritize jobs with nearest deadlines.

## Remediation

### Step 1 — Re-run the job

For transient failures:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin erasure retry <job-name>
```

Or recreate from the CronJob:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl create job --from=cronjob/lenny-erasure <new-job-name> -n lenny-system
```

### Step 2 — Storage-side error

Follow [minio-failure](minio-failure.html). Erasure jobs need working object-store writes to delete artifacts.

### Step 3 — Postgres constraint

If the error is a foreign-key or referential-integrity violation, inspect the involved tables and run the manual erasure procedure for the specific scope:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin erasure run --tenant <id> --scope <scope> --dry-run
```

Dry-run prints the DDL/DML statements that would execute. Review, then apply.

### Step 4 — Scope sharding

If the job is OOM or timing out on a large tenant:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin erasure run --tenant <id> --shard-by <field> --shard <N>
```

Splits the erasure into smaller batches.

### Step 5 — Verify completion

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose erasure <job-name>
```

- Target objects absent from object store (spot-check).
- Audit table PII columns NULLed (where applicable).
- Completion event recorded:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=compliance.erasure_completed&tenant_id=<id>&since=1h
```

## Escalation

Escalate to:

- **Compliance officer / DPO** immediately if the regulatory deadline is within 24 hours and remediation is not certain to complete in time.
- **Platform engineering** for unexpected constraint violations or schema issues blocking erasure — may indicate a migration missed a cascade rule.
- **Object-store operators** for sustained storage-side issues blocking artifact deletion.
