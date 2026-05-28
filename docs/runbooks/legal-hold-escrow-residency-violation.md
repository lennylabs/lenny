---
layout: default
title: "legal-hold-escrow-residency-violation"
parent: "Runbooks"
triggers:
  - alert: LegalHoldEscrowResidencyViolation
    severity: critical
components:
  - compliance
  - storage
symptoms:
  - "Phase 3.5 tenant force-delete aborted with a residency mismatch"
  - "escrow KMS key or bucket endpoint unreachable in the target region"
tags:
  - legal-hold
  - residency
  - compliance
  - tenant-deletion
requires:
  - admin-api
related:
  - artifact-replication-residency-violation
  - data-residency-violation
  - legal-hold-override
  - tenant-deletion-overdue
---

# legal-hold-escrow-residency-violation

A Phase 3.5 step of a tenant force-delete aborted because the resolved escrow region either has no `storage.regions.<region>.legalHoldEscrow` entry or that region's escrow KMS key or bucket endpoint is unreachable.

## Trigger

`LegalHoldEscrowResidencyViolation` — `rate(lenny_legal_hold_escrow_region_unresolvable_total[5m]) > 0`.

## Diagnosis

### Step 1 — Identify the affected tenant and region

<!-- access: api method=GET path=/v1/admin/audit-events -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/audit-events?event_type=tenant.delete.phase_3_5_aborted&since=1h" \
  | jq '.events[] | {tenant_id, target_escrow_region, reason}'
```

The audit row names the tenant, the resolved escrow region, and the specific abort reason (`region_unconfigured`, `escrow_kms_unreachable`, or `escrow_bucket_unreachable`).

### Step 2 — Inspect the escrow configuration

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-storage -o jsonpath='{.data.regions}' \
  | jq '.<region>.legalHoldEscrow'
```

A missing object or empty `kmsKeyRef` / `bucketEndpoint` confirms the configuration gap.

### Step 3 — Probe the escrow endpoint

<!-- access: api method=POST path=/v1/admin/legal-holds/escrow/probe -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/legal-holds/escrow/probe" \
  -H 'content-type: application/json' \
  --data '{"region":"<region>"}'
```

The probe returns the KMS reachability, bucket reachability, and the residency tag observed at the destination.

## Remediation

1. **If the escrow region is unconfigured:** apply a `helm upgrade` to add `storage.regions.<region>.legalHoldEscrow.{kmsKeyRef, bucketEndpoint, jurisdictionTag}`. Re-run the probe to confirm reachability.
2. **If the KMS key is unreachable:** verify cloud IAM grants for the `lenny-ops-legal-hold` role and unblock per [kms-unavailable.md](./kms-unavailable.md).
3. **If the bucket endpoint is unreachable:** verify managed-bucket allowlists and NetworkPolicy egress; on private endpoints check the cluster's outbound routing.
4. Resume the suspended tenant delete via `POST /v1/admin/tenants/<id>/delete/resume` once the probe returns `ok`.

## Verification

The probe returns `ok` for every required dimension and `GET /v1/admin/tenants/<id>` reports `state: deleting` continuing through the remaining phases.

## Escalation

Page security and compliance for any sustained escrow-region misconfiguration. Page platform engineering when the probe fails repeatedly despite valid configuration.

Cross-reference: [§12.8](../../spec/12_storage-architecture.md#128-compliance-interfaces), [§17.5](../../spec/17_deployment-topology.md#175-cloud-deployment-shapes).
