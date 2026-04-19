---
layout: default
title: "slo-startup-latency"
parent: "Runbooks"
triggers:
  - alert: StartupLatencyBurnRate
    severity: critical
  - alert: StartupLatencyGVisorBurnRate
    severity: critical
components:
  - warmPools
symptoms:
  - "pod startup p95 exceeding the configured SLO target (separate targets per runtime class)"
  - "warm pool replenishment slow"
  - "session creation latency elevated at claim phase"
tags:
  - slo
  - startup-latency
  - warm-pool
  - runtime-class
requires:
  - admin-api
  - cluster-access
related:
  - warm-pool-exhaustion
  - checkpoint-stale
  - sdk-connect-timeout
---

# slo-startup-latency

Pod-startup latency SLO burn. Separate SLO targets are defined per `RuntimeClass` (runc is lighter than gVisor or Kata). The exact SLO targets and burn-rate parameters are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules) for your cluster's defaults.

## Trigger

- `StartupLatencyBurnRate` — runc startup p95 burning.
- `StartupLatencyGVisorBurnRate` — gVisor startup p95 burning.

## Diagnosis

### Step 1 — Per-pool distribution

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_warmpool_pod_startup_duration_seconds_bucket[5m]))&groupBy=pool,runtime_class&window=1h
```

Which pool and runtime class? Startup cost differs materially between runc (light) and gVisor/Kata (heavier).

### Step 2 — Phase breakdown

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_warmpool_pod_startup_phase_duration_seconds_bucket[5m]))&groupBy=phase&window=1h
```

Phases: `schedule`, `image_pull`, `sandbox_init`, `setup_command`, `ready_check`.

### Step 3 — Node pressure

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl top nodes
kubectl get events -A --sort-by='.lastTimestamp' | grep -iE "evicted|preempted|pressure"
```

### Step 4 — Image-pull latency

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_warmpool_image_pull_duration_seconds_bucket[5m]))&groupBy=image_digest&window=1h
```

Elevated pull time = image uncached or registry slow.

## Remediation

### Step 1 — Schedule phase high

Node-pool pressure. Scale the node pool or adjust pod resource requests so the scheduler can place pods faster.

### Step 2 — Image-pull high

1. Verify image is pre-pulled on target nodes:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl get nodes -o json | \
     jq '.items[] | {name: .metadata.name, images: [.status.images[].names[]] | map(select(contains("<image-prefix>")))}'
   ```
2. If not pre-pulled, use a DaemonSet to warm the cache, or enable `imagePullPolicy: IfNotPresent` and pre-seed.
3. Check registry health — see [dns-outage](dns-outage.html) for network.

### Step 3 — Setup-command high

Long setup commands are a warm-pod design anti-pattern; the warm pod exists precisely to pre-pay setup cost. Move the work into the base image. See Spec §6 (warm-pod model).

### Step 4 — gVisor-specific

gVisor startup is heavier by construction. If the SLO target is not tier-appropriate for gVisor:

- Increase warm-pool `minWarm` so claims rarely wait for cold starts.
- Revisit whether the tier's SLO target is realistic; Spec §17.8 defines tier-specific SLOs.

### Step 5 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose slo startup-latency
```

- p95 startup within the tier target.
- Burn-rate alerts clear within the fast window.

## Escalation

Escalate to:

- **Cluster admin** for node-pool pressure that requires provider-side scaling.
- **Capacity owner** for structural warm-pool sizing gaps.
- **Image / release engineer** for images whose build output is too large to warm quickly — may need a squash/slim pass.
