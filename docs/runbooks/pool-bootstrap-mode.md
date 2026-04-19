---
layout: default
title: "pool-bootstrap-mode"
parent: "Runbooks"
triggers:
  - alert: PoolBootstrapMode
    severity: warning
components:
  - warmPools
symptoms:
  - "pool in bootstrap mode > 72 hours"
  - "minWarm / maxWarm sized to bootstrap defaults, not tuned"
  - "warm pool lacks capacity tuning"
tags:
  - warm-pool
  - bootstrap
  - capacity
requires:
  - admin-api
related:
  - warm-pool-exhaustion
  - pool-config-drift
---

# pool-bootstrap-mode

A warm pool is still running in bootstrap mode (initial defaults) more than 72 hours after first use. Bootstrap defaults are conservative and designed for first-run safety; running in bootstrap mode indefinitely masks real capacity signals.

## Trigger

- `PoolBootstrapMode` alert — pool in bootstrap mode > 72 hours.
- Pool metrics show traffic but `minWarm` / `maxWarm` at bootstrap defaults.

## Diagnosis

### Step 1 — Confirm bootstrap state

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools get <pool-name>
```

The output includes `bootstrapMode: true` and the bootstrap-default values.

### Step 2 — Observed traffic

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_warmpool_pod_claims_total{pool="<name>"}&window=7d
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_warmpool_pod_startup_duration_seconds_bucket[5m]))&groupBy=pool&window=7d
```

Use actual traffic over the past week to size the pool.

### Step 3 — Peak concurrency

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=max_over_time(lenny_warmpool_active_pods[7d])&groupBy=pool
```

## Remediation

### Step 1 — Pick sensible sizing

Rule of thumb (Spec §17.8):

- `minWarm` = 90th-percentile concurrent demand over the past week, rounded up.
- `maxWarm` = 2× `minWarm` with a floor from the tier default.
- `hpaTargetUtilization` = 0.7 as a starting point.

### Step 2 — Apply

Set the warm-count floor (HPA target utilization comes from Helm values — see Step 4):

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools set-warm-count --pool <name> --min <N>
```

<!-- access: api method=PATCH path=/v1/admin/pools/{name} -->
```
PATCH /v1/admin/pools/<name>
{"minWarm": <N>, "maxWarm": <M>}
```

### Step 3 — Exit bootstrap mode

Once warm-count is tuned, exit bootstrap mode explicitly:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools exit-bootstrap --pool <name>
```

Confirm:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools get <pool-name>
```

`bootstrapMode: false`.

### Step 4 — Persist to Helm values

Bootstrap-mode exits are runtime changes. Persist them to your `values.yaml` so they survive a redeploy:

```yaml
pools:
  - name: <pool-name>
    minWarm: <N>
    maxWarm: <M>
    hpaTargetUtilization: 0.7
```

### Step 5 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose pool <pool-name>
```

- `bootstrapMode: false`.
- Pool size reflects the new values.
- `PoolBootstrapMode` alert clears.

## Escalation

Escalate to:

- **Capacity owner** for pools whose observed traffic exceeds the tier's recommended defaults — may need a tier bump.
- **Platform operator peers** for sizing review — it's easier to size a pool with a colleague's second opinion than alone.
