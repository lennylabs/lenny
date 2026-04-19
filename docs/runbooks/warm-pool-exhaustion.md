---
layout: default
title: "warm-pool-exhaustion"
parent: "Runbooks"
triggers:
  - alert: WarmPoolLow
    severity: warning
  - alert: WarmPoolExhausted
    severity: critical
  - alert: PodClaimQueueSaturated
    severity: warning
  - alert: WarmPoolReplenishmentSlow
    severity: warning
  - alert: WarmPoolReplenishmentFailing
    severity: warning
components:
  - warmPools
symptoms:
  - "session creation returns RUNTIME_UNAVAILABLE"
  - "idle pod count drops to zero"
  - "warm pool replenishment stalls"
  - "pod claim queue saturated with idle pods available"
tags:
  - scaling
  - pods
  - warm-pool
  - capacity
requires:
  - admin-api
  - cluster-access
related:
  - cert-manager-outage
  - admission-webhook-outage
  - gateway-replica-failure
---

# warm-pool-exhaustion

A warm pool has run out of idle pods (or is trending that way) and session creation is rejecting requests.

## Trigger

Any of:

- `WarmPoolExhausted` alert: `lenny_warmpool_idle_pods` sustained at zero for a pool.
- `WarmPoolLow` alert: warm pods materially below `minWarm`.
- `PodClaimQueueSaturated` alert: claim queue depth elevated while idle pods remain available.
- Session requests return `RUNTIME_UNAVAILABLE`.
- `/v1/admin/health` returns `warmPools: degraded` or `unhealthy`.

Exact thresholds and evaluation windows for each alert are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Pool status at a glance

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose pool <pool-name>
```

<!-- access: api method=GET path=/v1/admin/diagnostics/pools/{name} -->
```
GET /v1/admin/diagnostics/pools/<pool-name>
```

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandboxes -n <agent-ns> -l pool=<pool> -o wide
kubectl get sandboxes -n <agent-ns> -l pool=<pool> \
  -o jsonpath='{range .items[*]}{.status.phase}{"\n"}{end}' | sort | uniq -c
```

Expect to see a breakdown of `warming` / `idle` / `claimed` counts. `idle == 0` confirms exhaustion; a large `warming` count with no progress points at replenishment failure.

### Step 2 — Replenishment health

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_warmpool_pod_startup_duration_seconds&window=10m
GET /v1/admin/metrics?q=lenny_warmpool_warmup_failure_total&window=10m
```

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-warm-pool-controller --since=10m
```

Elevated P95 startup duration (> 2× baseline) or any entries in `lenny_warmpool_warmup_failure_total` point to the failure mode.

### Step 3 — Admission and capacity

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl describe nodes | grep -A5 "Conditions:"
kubectl describe replicaset -n <agent-ns> -l pool=<pool> | tail -20
kubectl get resourcequota -n <agent-ns>
```

Look for `MemoryPressure` / `DiskPressure` / `PIDPressure` on nodes, `FailedCreate` events on the ReplicaSet, or `ResourceQuota` saturation. Admission-webhook errors indicate a webhook issue -- jump to [admission-webhook-outage](admission-webhook-outage.html).

## Remediation

### Step 3a — Image pull is failing

Verify image digest and registry credentials:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl describe pod <pod-name> -n <agent-ns> | grep -A3 "Events:"
kubectl get secret -n <agent-ns> <image-pull-secret> -o yaml
```

Rotate the pull secret or fix the image tag, then start a rolling upgrade with the corrected image digest:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools upgrade start --pool <pool> --new-image <digest>
```

### Step 3b — Demand exceeds supply

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools set-warm-count --pool <pool> --min <current + 10>
```

<!-- access: api method=PUT path=/v1/admin/pools/{pool}/warm-count -->
```
PUT /v1/admin/pools/<pool>/warm-count
{"minWarm": <current + 10>}
```

**Expected outcome:** Within 2 minutes `lenny-ctl diagnose pool <pool>` shows `idle > 0`; the alert resolves.

### Step 3c — Admission-webhook or quota blocks creation

If the diagnosis showed `failed calling webhook` events, follow [admission-webhook-outage](admission-webhook-outage.html). If `ResourceQuota` is saturated, escalate to cluster admin -- a quota increase is required.

### Step 3d — Node resource pressure

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl cordon <saturated-node>
kubectl drain <saturated-node> --ignore-daemonsets --delete-emptydir-data
```

Escalate to cluster admin for node scale-out. Do not uncordon until the root cause is understood.

### Step 4 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose pool <pool>
```

- `idle >= minWarm` within 2 minutes of scaling.
- `lenny_warmpool_warmup_failure_total` flat (no new entries).
- `WarmPoolExhausted` / `WarmPoolLow` alert resolves in the Prometheus rule window.

## Escalation

Escalate if:

- The pool does not recover within 5 minutes of scaling.
- Node resource pressure or `ResourceQuota` is the root cause (cluster admin).
- The same pool has exhausted 3+ times in 24 hours (indicates structural undersizing -- revisit [capacity planning](../operator-guide/scaling.html)).
- `lenny_warmpool_warmup_failure_total` continues rising after applying the fix.
