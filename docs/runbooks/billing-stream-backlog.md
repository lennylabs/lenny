---
layout: default
title: "billing-stream-backlog"
parent: "Runbooks"
triggers:
  - alert: BillingStreamEntryAgeHigh
    severity: critical
  - alert: BillingStreamBackpressure
    severity: warning
components:
  - billing
symptoms:
  - "oldest billing stream entry nearing TTL"
  - "Redis stream depth climbing"
  - "downstream billing system slow or stalled"
tags:
  - billing
  - redis-streams
  - metering
requires:
  - admin-api
  - cluster-access
related:
  - redis-failure
  - audit-pipeline-degraded
---

# billing-stream-backlog

The Redis-streams buffer between the gateway and the downstream billing/metering pipeline is accumulating. If the oldest entry exceeds the stream TTL, entries are lost — which is a billing-accuracy event.

## Trigger

- `BillingStreamEntryAgeHigh` — oldest entry age approaching the stream TTL.
- `BillingStreamBackpressure` — stream depth approaching the configured max.
- Downstream billing dashboards show stalled metering.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Stream depth and oldest-entry age

<!-- access: kubectl requires=cluster-access -->
```bash
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls \
  XINFO STREAM lenny:billing:events
```

Inspect `length`, `first-entry`, and `last-entry` timestamps.

### Step 2 — Consumer groups

<!-- access: kubectl requires=cluster-access -->
```bash
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls \
  XINFO CONSUMERS lenny:billing:events <consumer-group>
```

Look for consumers with high `pending` counts or stale `idle` times — indicates a stuck consumer.

### Step 3 — Downstream health

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_billing_consumer_lag_seconds&window=15m
```

If only one consumer is lagging, it's consumer-local. Cluster-wide lag means the downstream sink is slow or down.

### Step 4 — Publish rate

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=rate(lenny_billing_events_published_total[5m])&window=30m
```

A rate spike with normal consumption rate signals a legitimate load surge; flat publish with growing depth signals consumer failure.

## Remediation

### Step 1 — Stuck consumer

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-billing-consumer -n lenny-system
kubectl rollout status deployment lenny-billing-consumer -n lenny-system --timeout=2m
```

If a single consumer instance hung, the restart reclaims its pending entries via `XAUTOCLAIM`.

### Step 2 — Scale consumers

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl scale deployment lenny-billing-consumer -n lenny-system --replicas=<N+1>
```

Each consumer reads a disjoint slice of the stream via the consumer group.

### Step 3 — Downstream sink down

If the billing sink (Stripe, custom ledger, etc.) is unavailable:

1. Confirm sink health with the provider or owner team.
2. Temporarily raise stream TTL (`billing.stream.maxLenSeconds` in Helm) to extend retention until the sink recovers — this **trades Redis memory for accuracy**.
3. After the sink recovers, let consumers drain naturally.

### Step 4 — Entries nearing TTL

If `BillingStreamEntryAgeHigh` is firing and draining won't complete before TTL, coordinate with the billing platform owner to capture the stream tail before loss. Reconciliation is performed post-incident from `audit_log`; there is no operator CLI to snapshot the stream.

### Step 5 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose connectivity
```

- Stream depth trending toward baseline.
- `lenny_billing_consumer_lag_seconds` within its configured threshold.
- No entries at risk of TTL expiry.

## Escalation

Escalate to:

- **Finance ops** if any billing entries were lost (TTL-expired without consumption) — reconciliation from `audit_log` may be required.
- **Billing platform owner** (downstream sink) for sustained sink outages.
- **Redis operators** if the stream's physical size is the limit — may need larger `maxmemory`.
