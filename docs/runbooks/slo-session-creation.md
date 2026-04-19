---
layout: default
title: "slo-session-creation"
parent: "Runbooks"
triggers:
  - alert: SessionCreationSuccessRateBurnRate
    severity: critical
  - alert: SessionCreationLatencyBurnRate
    severity: critical
components:
  - gateway
  - warmPools
symptoms:
  - "session-creation success rate burn-rate alert firing"
  - "session-creation p99 latency burn-rate alert firing"
  - "SLO error budget depleting"
tags:
  - slo
  - session-creation
  - burn-rate
  - multi-window
requires:
  - admin-api
related:
  - warm-pool-exhaustion
  - credential-pool-exhaustion
  - token-service-outage
---

# slo-session-creation

Session-creation SLOs are burning. Two SLOs here: success-rate and p99 latency. Targets, burn-rate factors, and evaluation windows are deployer-configurable — the multi-window multi-burn-rate pattern (Google SRE) is the canonical shape; your cluster's exact values are in [Metrics Reference](../reference/metrics.html#alert-rules).

## Trigger

- `SessionCreationSuccessRateBurnRate` — success rate burn-rate exceeded.
- `SessionCreationLatencyBurnRate` — latency burn-rate exceeded.

## Diagnosis

### Step 1 — Which SLO is burning?

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_session_creation_success_total,lenny_session_creation_total&window=1h
GET /v1/admin/metrics?q=histogram_quantile(0.99, rate(lenny_session_creation_duration_seconds_bucket[5m]))&window=1h
```

### Step 2 — Failure reasons

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_session_creation_failed_total&groupBy=reason&window=1h
```

Common reason classes and their owning runbooks:

| Reason | Runbook |
|:-------|:--------|
| `RUNTIME_UNAVAILABLE` | [warm-pool-exhaustion](warm-pool-exhaustion.html) |
| `CREDENTIAL_POOL_EXHAUSTED` | [credential-pool-exhaustion](credential-pool-exhaustion.html) |
| `CREDENTIAL_MATERIALIZATION_ERROR` | [token-service-outage](token-service-outage.html) |
| `QUOTA_EXCEEDED` | (expected if quota is working; investigate only if unexpected) |
| `INTERNAL_ERROR` | [postgres-failover](postgres-failover.html), [minio-failure](minio-failure.html), [gateway-replica-failure](gateway-replica-failure.html) |

### Step 3 — Latency p99 breakdown

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.99, rate(lenny_session_creation_duration_seconds_bucket[5m]))&groupBy=phase&window=1h
```

Phases: `claim`, `materialize`, `warmup`, `attach`. The dominant phase points at the underlying bottleneck.

### Step 4 — Tenant / pool scope

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_session_creation_failed_total[5m])&groupBy=tenant_id,pool&window=1h
```

If one tenant or pool dominates, the issue is scoped.

## Remediation

### Step 1 — Follow the underlying runbook

The SLO alert is a symptom. Use the Diagnosis step mapping to reach the subsystem runbook. Fixing the underlying cause restores the SLO.

### Step 2 — Widen rate limits if quota-exhausted

If `QUOTA_EXCEEDED` is dominant and the denial is against a tenant's expected usage:

Review the tenant's current limits with `lenny-ctl admin tenants get <id>` and update the quota via the admin tenant-quota API (or update the tenant's Helm values and run `helm upgrade`):

```
PUT /v1/admin/tenants/<id>/quota
```

Validate with the tenant before raising permanently.

### Step 3 — Capacity scale-out

If latency burn is driven by contention at the gateway layer, see [gateway-capacity](gateway-capacity.html).

### Step 4 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_slo_session_creation_success_ratio&window=1h
```

- Success rate back within the configured SLO target over the trailing hour.
- p99 latency back within the configured SLO target.
- Burn-rate alerts clear within the fast window.

### Step 5 — Error-budget accounting

<!-- access: api method=GET path=/v1/admin/slo/error-budget -->
```
GET /v1/admin/slo/error-budget?slo=session-creation-success
```

If the budget is materially depleted, consider a release freeze until the budget recovers — operations only, no new risky changes.

## Escalation

Escalate to:

- **Subsystem runbook escalation** for the underlying cause (see Step 1 mapping).
- **SLO owner / SRE** if the SLO is repeatedly burning despite remediation — the SLO target may need recalibration or the capacity baseline may have shifted.
