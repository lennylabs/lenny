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
lenny-ctl admin erasure-jobs retry <job-id>
```

Or recreate from the CronJob:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl create job --from=cronjob/lenny-erasure <new-job-name> -n lenny-system
```

### Step 2 — Storage-side error

Follow [minio-failure](minio-failure.html). Erasure jobs need working object-store writes to delete artifacts.

### Step 3 — Clear a legal-hold or restriction

If the job is blocked by a restriction (legal hold, active dispute), inspect the job state and, with a recorded justification, clear the restriction so the controller can retry:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin erasure-jobs get <job-id>
lenny-ctl admin erasure-jobs clear-restriction <job-id> --justification "<text>"
```

### Step 4 — Postgres constraint or sharding

Erasure is driven by the tenant-deletion controller; there is no operator CLI to invoke ad-hoc erasure runs. If the failure is a Postgres constraint or the scope is too large for a single batch, file a platform-engineering escalation to amend the controller's cascade order or shard configuration. Subsequent retries use the updated controller.

### Step 5 — Verify completion

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin erasure-jobs get <job-id>
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
