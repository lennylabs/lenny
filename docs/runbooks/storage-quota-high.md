---
layout: default
title: "storage-quota-high"
parent: "Runbooks"
triggers:
  - alert: StorageQuotaHigh
    severity: warning
components:
  - objectStore
symptoms:
  - "artifact storage approaching tenant quota"
  - "upcoming quota-exceeded failures"
  - "tenant notifications queued"
tags:
  - storage
  - quota
  - artifacts
  - tenant
requires:
  - admin-api
related:
  - minio-failure
  - erasure-job-failed
---

# storage-quota-high

A tenant's artifact storage has crossed the warning threshold of their allocated quota. Without action, uploads will eventually fail with `QUOTA_EXCEEDED` at 100 %. The warning threshold is deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Trigger

- `StorageQuotaHigh` — tenant artifact storage approaching the configured quota ceiling.

## Diagnosis

### Step 1 — Identify the tenant and amount

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_tenant_artifact_bytes_total / lenny_tenant_artifact_quota_bytes&groupBy=tenant_id&window=15m
```

Returns utilization ratio per tenant.

### Step 2 — Growth rate

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_tenant_artifact_bytes_total[1h])&groupBy=tenant_id&window=24h
```

High growth rate × current utilization tells you how soon they'll hit quota.

### Step 3 — What's in the bucket?

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin artifacts list --tenant <id> --largest 50
```

Large old artifacts are usually the target for cleanup.

### Step 4 — Lifecycle rules

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin artifact-lifecycle show --tenant <id>
```

If lifecycle rules aren't firing (expiration, archive), the quota is inflated with deletable data.

## Remediation

### Step 1 — Notify the tenant

Tenants usually own their quota. A pre-exhaustion notification gives them time to clean up or request more.

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin tenants notify <id> --template storage-quota-approaching
```

### Step 2 — Apply or fix lifecycle rules

If lifecycle rules exist but aren't firing, check ILM configuration on the object store ([minio-failure](minio-failure.html) Step 4 for verification commands).

If no lifecycle rules exist:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin artifact-lifecycle set --tenant <id> \
  --expire-after 30d --archive-after 7d
```

### Step 3 — Clean up with tenant consent

Do NOT delete tenant data without explicit tenant consent. Propose a retention policy and apply it after sign-off.

### Step 4 — Increase quota

If the growth is legitimate:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin quotas set --tenant <id> --artifact-storage-bytes <new>
```

<!-- access: api method=PATCH path=/v1/admin/quotas/{tenant_id} -->
```
PATCH /v1/admin/quotas/<tenant_id>
{"artifactStorageBytes": <new>}
```

Coordinate with finance ops if the quota raise has a cost implication.

### Step 5 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose storage-quota --tenant <id>
```

- Utilization back within the alert's warning threshold.
- Lifecycle rules active.
- Alert clears.

## Escalation

Escalate to:

- **Tenant operator / account team** for quota-raise decisions.
- **Finance ops** for billing implications of raises.
- **Compliance officer** if the tenant has retention obligations (regulated workloads) that conflict with deletion.
