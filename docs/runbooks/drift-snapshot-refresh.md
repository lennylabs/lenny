---
layout: default
title: "drift-snapshot-refresh"
parent: "Runbooks"
triggers: []
components:
  - drift
  - admin-api
symptoms:
  - "GET /v1/admin/drift returns snapshot_stale: true"
  - "snapshot_age_seconds exceeds ops.drift.snapshotStaleWarningDays"
  - "drift reports report changes from a recent hotfix or out-of-band admin-API mutation"
tags:
  - drift
  - hotfix
  - admin-api
  - gitops
requires:
  - admin-api
related:
  - credential-revocation
  - warm-pool-exhaustion
  - admission-plane-feature-flag-downgrade
  - legal-hold-quota-pressure
---

# drift-snapshot-refresh

The drift snapshot (`bootstrap_seed_snapshot`, id=`live`) is the desired-state baseline §25.10 compares the cluster against. When an operator makes an emergency hotfix via the admin API (credential rotation, pool resize, tenant suspension, runtime patch) without committing the corresponding change to the Helm values file, the snapshot ages out and `GET /v1/admin/drift` flags the divergence as stale. This runbook covers the canonical hotfix cleanup: reconcile the source of truth, refresh the snapshot, and verify. Every emergency-remediation runbook in §17.7 ends with a step that points back here.

## Trigger

- `GET /v1/admin/drift` returns `snapshot_stale: true`, with `snapshot_age_seconds` exceeding the per-deployment `ops.drift.snapshotStaleWarningDays` threshold (default 7 days).
- The response carries a `snapshot_stale_warning` string. A response without `snapshot_stale` set is fresh.
- The condition typically follows an emergency hotfix or out-of-band admin-API mutation made between Helm upgrades.

## Diagnosis

### Step 1 — Inspect the snapshot header

<!-- access: api method=GET path=/v1/admin/drift -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/drift" | jq '{snapshot_written_at, snapshot_age_seconds, snapshot_stale, snapshot_stale_warning, summary}'
```

Confirm `snapshot_written_at` and `snapshot_age_seconds` — the snapshot timestamp drives every downstream §25.10 comparison.

### Step 2 — Audit changes since the snapshot

<!-- access: api method=GET path=/v1/admin/audit-events -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/audit-events?since=<snapshot_written_at>&event_type=runtime.created,runtime.updated,pool.updated,tenant.updated,credential_pool.updated,delegation_policy.updated" \
  | jq '.events[] | {event_type, actor_id, resource_id, recorded_at}'
```

Each row identifies an intentional admin-API mutation. Decide for each one whether the change should persist in the new desired state or be rolled back.

### Step 3 — Validate the current Helm values against the snapshot

<!-- access: api method=POST path=/v1/admin/drift/validate -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/drift/validate" \
  -H 'content-type: application/json' \
  --data @- <<'JSON'
{
  "desired": { /* paste the relevant slice of the Helm values file */ }
}
JSON
```

Validation that returns a non-empty `divergences` array names the fields that diverge between the proposed source of truth (`desired`) and the stored snapshot. These are the fields the snapshot refresh in Step 5 will rewrite.

## Remediation

### Step 4 — Reconcile the source of truth

Reconcile the source of truth *before* refreshing the snapshot. The snapshot is downstream of the GitOps file; refreshing first leaves the GitOps repository out of sync with the cluster and re-introduces the same drift on the next reconciliation pass.

- **If the out-of-band changes should persist:** commit the changes to the Helm values file (or the equivalent GitOps source) so the next upgrade carries them forward.
- **If the changes were temporary:** revert them via the admin API before refreshing the snapshot.

### Step 5 — Refresh the snapshot

<!-- access: api method=POST path=/v1/admin/drift/snapshot/refresh -->
```bash
curl -sS -X POST "$LENNY_OPS_URL/v1/admin/drift/snapshot/refresh" \
  -H 'content-type: application/json' \
  --data @- <<'JSON'
{
  "desired": { /* the reconciled desired state from Step 4 */ },
  "confirm": true
}
JSON
```

The endpoint replaces `bootstrap_seed_snapshot` (id=`live`) atomically and records the refresh in the audit trail (`drift.snapshot_refreshed`). The response carries the new `snapshot_written_at`, the `byteSize` of the persisted JSONB row, and a `refreshed_at` timestamp.

### Step 6 — Verify

<!-- access: api method=GET path=/v1/admin/drift -->
```bash
curl -sS "$LENNY_OPS_URL/v1/admin/drift" | jq '{snapshot_stale, snapshot_age_seconds, summary}'
```

Confirm `snapshot_stale: false` and that every drift previously reported for an intentional change is now absent. A drift entry that survives the refresh indicates either a residual divergence in the Helm values file (re-run Step 3 with the corrected slice) or a §25.10 classifier rule that flags the field as structural (re-check the classification with `GET /v1/admin/drift/components`).

## Post-hotfix cleanup checklist

Every runbook in §17.7 that instructs the operator to make a direct admin-API mutation as an emergency remediation (credential rotation, pool resize outside a Helm upgrade, tenant suspension, runtime patch, delegation-policy adjustment) ends with a step pointing back to this runbook. Treat this as the permanent tail of every hotfix runbook:

> After the incident is resolved, call `POST /v1/admin/drift/snapshot/refresh` with the current desired state to prevent stale-snapshot warnings on subsequent drift runs.

The post-hotfix step is required by §17.7 and is enforced by the operator workflow — leaving the snapshot stale degrades the §25.10 monitoring surface until the next intentional Helm upgrade rolls a fresh snapshot.

## Cross-references

- [§25.10 Configuration drift detection](../../spec/25_agent-operability.md#2510-configuration-drift-detection)
- [§17.7 Operational runbooks](../../spec/17_deployment-topology.md#177-operational-runbooks)
- `admission-plane-feature-flag-downgrade.md` and `elicitation-content-tamper-detected.md` link to this runbook from their post-hotfix steps.
