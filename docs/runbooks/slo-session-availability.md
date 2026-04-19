---
layout: default
title: "slo-session-availability"
parent: "Runbooks"
triggers:
  - alert: SessionAvailabilityBurnRate
    severity: critical
components:
  - gateway
  - warmPools
symptoms:
  - "session-availability burn-rate alert firing"
  - "active sessions experiencing disconnections"
  - "session-availability SLO error budget depleting"
tags:
  - slo
  - session-availability
  - burn-rate
requires:
  - admin-api
related:
  - gateway-replica-failure
  - postgres-failover
  - checkpoint-stale
---

# slo-session-availability

The session-availability SLO is burning. The SLO measures the fraction of time an already-running session remains reachable and operational; disconnections, eviction loss, or state inaccessibility count against it. Target, burn-rate factors, and evaluation windows are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules) for your cluster's defaults.

## Trigger

- `SessionAvailabilityBurnRate` — error budget burning at the configured fast or slow rate.

## Diagnosis

### Step 1 — Unavailability breakdown

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_session_unavailable_seconds_total&groupBy=reason&window=1h
```

Common reasons and owning runbooks:

| Reason | Runbook |
|:-------|:--------|
| `gateway_disconnect` | [gateway-replica-failure](gateway-replica-failure.html) |
| `postgres_degraded` | [postgres-failover](postgres-failover.html) |
| `pod_evicted` | [warm-pool-exhaustion](warm-pool-exhaustion.html), [session-eviction-loss](session-eviction-loss.html) |
| `checkpoint_stale_loss` | [checkpoint-stale](checkpoint-stale.html) |
| `network_partition` | [dns-outage](dns-outage.html), [network-policy-drift](network-policy-drift.html) |

### Step 2 — Duration distribution

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_session_unavailability_window_seconds_bucket[5m]))&window=1h
```

Short windows — typical of gateway replica churn; tolerable if rare.
Long windows — structural issue; investigate aggressively.

### Step 3 — Scope

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_session_unavailable_seconds_total[5m])&groupBy=tenant_id,pool&window=1h
```

## Remediation

### Step 1 — Follow the underlying runbook

Unavailability is a symptom. Use the Step 1 mapping above to reach the right runbook.

### Step 2 — Gateway reconnect

If `gateway_disconnect` dominates, check whether clients are reconnecting. The SDK should auto-reconnect; persistent disconnect without reconnection means either (a) SDK misbehavior or (b) gateway unreachable at the Service level. Both are tracked in [gateway-replica-failure](gateway-replica-failure.html).

### Step 3 — Checkpoint freshness

If `checkpoint_stale_loss` is present, sessions lost state on eviction because the last checkpoint was too old. Fix via [checkpoint-stale](checkpoint-stale.html) and consider raising checkpoint frequency for the affected pool(s).

### Step 4 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_session_availability_ratio&window=1h
```

- Availability back within the configured SLO target over trailing hour.
- Burn-rate alerts clear within the fast window.

### Step 5 — Error-budget accounting

<!-- access: api method=GET path=/v1/admin/slo/error-budget -->
```
GET /v1/admin/slo/error-budget?slo=session-availability
```

If the budget is materially depleted, consider a release freeze and pause non-essential maintenance.

## Escalation

Escalate to:

- **Subsystem runbook escalation** per the Step 1 mapping.
- **SRE / SLO owner** for repeated burn-rate incidents — may indicate the SLO target is set too tight for the current architecture.
- **Security on-call** if `session-availability` loss correlates with a data-residency or compliance event.
