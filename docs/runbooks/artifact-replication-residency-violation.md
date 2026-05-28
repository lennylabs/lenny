---
layout: default
title: "artifact-replication-residency-violation"
parent: "Runbooks"
triggers:
  - alert: ArtifactReplicationResidencyViolation
    severity: critical
components:
  - storage
  - compliance
symptoms:
  - "ArtifactStore replication preflight observed a jurisdiction-tag mismatch or missing tag"
  - "DNS rebinding outside the allowlisted CIDRs"
  - "destination tag-probe failed"
  - "replication suspended for the affected region"
tags:
  - artifacts
  - replication
  - residency
  - compliance
requires:
  - admin-api
  - cluster-access
related:
  - data-residency-violation
  - legal-hold-escrow-residency-violation
  - minio-replication-lag
---

# artifact-replication-residency-violation

The ArtifactStore runtime residency preflight observed a jurisdiction-tag mismatch, a missing tag, DNS rebinding outside the allowlisted CIDRs, or a failed destination tag-probe. Replication for the affected region is suspended pending operator review.

## Trigger

`ArtifactReplicationResidencyViolation` — `rate(lenny_minio_replication_residency_violation_total[5m]) > 0`.

## Diagnosis

### Step 1 — Identify the affected region

<!-- access: api method=GET path=/v1/admin/storage/replication/status -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/storage/replication/status" | jq '.regions[] | select(.suspended) | {region, reason, last_probe_at}'
```

The response names the region, the residency tag that failed, and the most recent probe outcome.

### Step 2 — Inspect the destination configuration

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system get configmap lenny-storage -o jsonpath='{.data.regions}' | jq '.<region>'
```

Cross-reference `legalHoldEscrow`, `replicationDestinationCidr`, and the expected jurisdiction tag against the deployed `values.yaml`.

### Step 3 — DNS verification

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl -n lenny-system exec deploy/lenny-gateway -- nslookup <destination-host>
```

The resolved IPs must be inside the `replicationDestinationCidr` allowlist; an out-of-range address is the DNS-rebinding case the preflight flags.

## Remediation

1. **If the destination configuration drifted:** apply a `helm upgrade` to restore the correct residency tag, allowlist CIDR, and destination endpoint. Replication resumes after the next probe succeeds.
2. **If the destination IP rotated out of the allowlist legitimately:** update `storage.regions.<region>.replicationDestinationCidr` to cover the new range, then refresh the drift snapshot ([drift-snapshot-refresh.md](./drift-snapshot-refresh.md)).
3. **If the residency tag is genuinely incorrect at the destination:** treat the violation as a compliance incident. Suspend the upstream tenant if the destination cannot be brought into compliance, and notify security and compliance.
4. After the destination is repaired, call `POST /v1/admin/storage/replication/<region>/resume` with `{"confirm": true}` to clear the suspension flag.

## Verification

`GET /v1/admin/storage/replication/status` reports the affected region as `active` with `last_probe_result: ok`, and the alert clears.

## Escalation

Page security and compliance for any sustained residency mismatch. Page platform engineering when the probe continues to fail after destination repair.

Cross-reference: [§12.5](../../spec/12_storage-architecture.md#125-artifact-store), [§17.5](../../spec/17_deployment-topology.md#175-cloud-deployment-shapes), [data-residency-violation.md](./data-residency-violation.md).
