---
layout: default
title: "controller-leader-election"
parent: "Runbooks"
triggers:
  - alert: ControllerLeaderElectionFailed
    severity: critical
components:
  - controllers
symptoms:
  - "controller Lease not renewed within leaseDuration"
  - "reconciliation gaps in warm pool or runtime controllers"
  - "stuck finalizers from cleanup path"
tags:
  - controllers
  - leader-election
  - leases
requires:
  - cluster-access
related:
  - stuck-finalizer
  - etcd-operations
---

# controller-leader-election

A Lenny controller could not renew its leader-election Lease within `leaseDuration`. Controllers reconcile only while they hold the lease; a renewal failure means reconciliation stalled.

## Trigger

- `ControllerLeaderElectionFailed` alert.
- Controller logs: `failed to renew lease` / `leader election lost`.
- Gaps in reconcile activity (`lenny_controller_reconciles_total` flat past the configured sustain window).

Exact alert thresholds and `leaseDuration` are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules) and chart values.

## Diagnosis

### Step 1 — Which controller?

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get leases -n lenny-system
kubectl describe lease <lease-name> -n lenny-system
```

Labels identify the controller: `lenny-warm-pool-controller`, `lenny-runtime-controller`, etc.

### Step 2 — Controller pod health

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n lenny-system -l app=<controller-name>
kubectl describe pod <controller-pod> -n lenny-system | tail -30
```

Look for OOM, probe failures, or recent restarts.

### Step 3 — API-server responsiveness

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs <controller-pod> -n lenny-system --since=10m | \
  grep -Ei "throttle|429|timeout|context deadline"
```

API-server throttling (429s) or slow responses are the most common cause of lease-renewal failures.

### Step 4 — etcd pressure

If API server is throttling, check etcd (see [etcd-operations](etcd-operations.html)).

## Remediation

### Step 1 — Restart the controller

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment <controller-name> -n lenny-system
kubectl rollout status deployment <controller-name> -n lenny-system --timeout=2m
```

Resolves transient panics or deadlocks. The leader role transfers to a healthy replica on restart.

### Step 2 — Scale controller replicas

If a single replica is insufficient:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl scale deployment <controller-name> -n lenny-system --replicas=2
```

Only one replica is active at a time (leader-elected), but the standby takes over immediately on failure.

### Step 3 — API-server throttling

If diagnosis points at API-server throttling:

1. Check `lenny_controller_api_throttle_total` for the controller.
2. Increase the controller's API-request budget in Helm values (`controller.apiQPS`, `controller.apiBurst`).
3. If cluster-wide, engage cluster admin — may indicate an API-server capacity issue.

### Step 4 — etcd pressure

If etcd is the bottleneck, see [etcd-operations](etcd-operations.html). Lease renewals go to etcd; a saturated etcd causes cluster-wide renewal failures.

### Step 5 — Verify

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get lease <lease-name> -n lenny-system -o yaml \
  | grep -E "holderIdentity|renewTime"
```

- `holderIdentity` points at a Ready pod.
- `renewTime` advances every few seconds.

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose controllers
```

- All controller Leases fresh (renewed within `leaseDuration`).
- Reconcile activity resumes.

## Escalation

Escalate to:

- **Cluster admin** for persistent API-server throttling or etcd issues outside Lenny's surface.
- **Platform engineering** if a controller cannot regain leadership even after restart — may indicate a Lease corruption or a bug in the controller's election logic.
- **SRE / on-call** if the leadership gap coincided with stuck finalizers across multiple pods — see [stuck-finalizer](stuck-finalizer.html) for cleanup.
