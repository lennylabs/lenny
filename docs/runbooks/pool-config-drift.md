---
layout: default
title: "pool-config-drift"
parent: "Runbooks"
triggers:
  - alert: PoolConfigDrift
    severity: warning
components:
  - warmPools
symptoms:
  - "Postgres / CRD generation mismatch"
  - "pool config updates not reflected in warm pool behavior"
  - "config changes visible in one surface but not the other"
tags:
  - warm-pool
  - config
  - drift
  - reconciliation
requires:
  - admin-api
  - cluster-access
related:
  - pool-bootstrap-mode
  - crd-upgrade
  - controller-leader-election
---

# pool-config-drift

The pool configuration stored in Postgres disagrees with what's live on the CRD (`Runtime` / `PoolPolicy`). The controller is either not reconciling, or a write to one surface didn't propagate to the other.

## Trigger

- `PoolConfigDrift` — Postgres / CRD generation mismatch sustained past the configured evaluation window.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — What's in Postgres

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools get <pool-name> -o yaml
```

The response includes `generation` and `lastAppliedCRDGeneration`.

### Step 2 — What's on the CRD

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get poolpolicy <pool-name> -o yaml | head -40
```

The `metadata.generation` is the current CRD generation; `status.lastObservedGeneration` is what the controller last reconciled.

### Step 3 — Controller state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-warm-pool-controller --since=10m \
  | grep -E "pool-config|<pool-name>|reconcile"
```

### Step 4 — Leader election

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get lease lenny-warm-pool-controller -n lenny-system
```

If the lease holder is missing or the `renewTime` is stale, see [controller-leader-election](controller-leader-election.html).

## Remediation

### Step 1 — Restart the controller

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-warm-pool-controller -n lenny-system
kubectl rollout status deployment lenny-warm-pool-controller -n lenny-system --timeout=2m
```

A fresh controller re-lists CRDs and forces reconciliation.

### Step 2 — Inspect sync status, then force reconcile

Inspect the current drift before reconciling:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools sync-status --pool <pool-name>
```

Once you have decided to remediate:

<!-- access: lenny-ctl -->
```bash
lenny-ctl drift reconcile --scope pool:<pool-name> --confirm
```

<!-- access: api method=POST path=/v1/admin/drift/reconcile -->
```
POST /v1/admin/drift/reconcile
{"scope": "pool:<pool-name>"}
```

### Step 3 — Decide which side is authoritative

If Postgres and CRD disagree on the same field and you cannot tell which is correct:

- **Postgres is authoritative for runtime-settable fields** (via admin API). The drift reconciler in Step 2 propagates these to the CRD.
- **Helm values are authoritative for chart-owned fields** (imageDigest, baseTemplate). Re-run `helm upgrade`, then re-run the drift reconciler:

<!-- access: lenny-ctl -->
```bash
lenny-ctl drift reconcile --scope pool:<pool-name> --confirm
```

### Step 4 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose pool <pool-name>
```

- Generations match.
- Controller reconciles cleanly.
- `PoolConfigDrift` clears within its alert evaluation window.

## Escalation

Escalate to:

- **Platform engineering** if drift recurs after reconcile — may indicate a controller bug or a write-path race.
- **Release engineer** if drift appears after a `helm upgrade` and doesn't resolve — may indicate a chart regression.
