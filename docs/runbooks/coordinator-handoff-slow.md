---
layout: default
title: "coordinator-handoff-slow"
parent: "Runbooks"
triggers:
  - alert: CoordinatorHandoffSlow
    severity: warning
components:
  - gateway
symptoms:
  - "p95 coordinator handoff elevated"
  - "delegation latency elevated"
  - "recursive sessions slow to start children"
tags:
  - coordinator
  - delegation
  - handoff
requires:
  - admin-api
  - cluster-access
related:
  - delegation-budget-recovery
  - gateway-capacity
---

# coordinator-handoff-slow

The coordinator handoff — the step where the parent session passes control of a delegated child session — is elevated in p95. User-visible impact: delegated workloads start slowly or time out.

## Trigger

- `CoordinatorHandoffSlow` — p95 handoff elevated beyond the configured threshold for the evaluation window (see [Metrics Reference](../reference/metrics.html#alert-rules)).
- Delegation-dependent workloads complain of slow starts.

## Diagnosis

### Step 1 — Handoff latency breakdown

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_coordinator_handoff_duration_seconds_bucket[5m]))&groupBy=phase&window=30m
```

Phases: `claim`, `materialize`, `warmup`, `attach`. The phase with the largest p95 is where to look.

### Step 2 — Warm-pool contention

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_warmpool_claim_wait_seconds&groupBy=pool&window=15m
```

If `claim` phase dominates, the delegated child is waiting on a warm pod — see [warm-pool-exhaustion](warm-pool-exhaustion.html).

### Step 3 — Credential materialization

If `materialize` phase dominates, the Token Service may be slow — see [token-service-outage](token-service-outage.html).

### Step 4 — Network / DNS

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_coordinator_attach_dns_lookup_seconds&window=15m
```

Attach-phase DNS lookup latency well above baseline is abnormal. See [dns-outage](dns-outage.html).

## Remediation

### Step 1 — Pool scale-out

If claim wait dominates, scale the target pool's `minWarm`:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools set-warm-count --pool <name> --min <N+10>
```

### Step 2 — Credential materialization

Follow [token-service-outage](token-service-outage.html).

### Step 3 — Gateway capacity

If handoff latency correlates with overall gateway load, see [gateway-capacity](gateway-capacity.html).

### Step 4 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose coordinator
```

- p95 handoff duration returns to baseline.
- `CoordinatorHandoffSlow` alert clears within its evaluation window.

## Escalation

Escalate to:

- **Capacity owner** if pool sizing is repeatedly insufficient for delegation peaks.
- **Platform engineering** if handoff latency is elevated without any identifiable phase bottleneck — may indicate coordinator internal contention.
