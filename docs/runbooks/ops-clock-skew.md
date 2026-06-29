---
layout: default
title: "ops-clock-skew"
parent: "Runbooks"
triggers:
  - alert: OpsClockSkewExceeded
    severity: warning
components:
  - ops
  - postgres
  - redis
symptoms:
  - "lenny_ops_clock_skew_seconds exceeds the 10s tolerance"
  - "premature or delayed remediation-lock expiry"
  - "outage-epoch reconciliation observes inconsistent timestamps"
tags:
  - lenny-ops
  - clock
  - ntp
  - locks
requires:
  - admin-api
  - cluster-access
related:
  - gateway-clock-drift
  - ops-lock-split-brain
  - postgres-failover
  - redis-failure
---

# ops-clock-skew

The measured clock skew between the Postgres dependency clock (Tier 1, `now()`) and the Redis dependency clock (Tier 2, the `TIME` command) exceeds the 10s tolerance NTP is expected to hold. `lenny-ops` authors every remediation lock's `expiresAt` from a single per-tier server clock, and outage-epoch reconciliation compares timestamps across the two stores when a tier recovers. A sustained skew breach risks premature or delayed lease expiry and timestamp inconsistencies during reconciliation. The 10s threshold is the operator-managed NTP tolerance the spec bounds Postgres-Redis skew by; it is the alert threshold, not a fixed design invariant.

## Trigger

`OpsClockSkewExceeded` (warning) — `lenny_ops_clock_skew_seconds{pair="postgres-redis"} > 10`.

The gauge is the absolute difference between the Postgres and Redis server clocks sampled by `lenny-ops`. The sample includes a sub-second read round-trip floor, so a value approaching 10s is a real divergence between the two dependency clocks rather than measurement noise.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Confirm the current skew

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_ops_clock_skew_seconds&window=15m
```

A value sustained above the threshold confirms the breach. A single transient spike that has already cleared does not require remediation; the gauge reflects the latest sample.

### Step 2 — Identify the nodes running Postgres and Redis

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -n lenny-system -l 'app in (postgres, redis)' -o wide
```

Map each dependency pod to its node. The skew is between the clocks on those two nodes (or the managed-service hosts when Postgres or Redis is managed externally).

### Step 3 — Node NTP / chrony status

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl debug node/<postgres-node> -it --image=busybox -- \
  chronyc tracking
```

Repeat for the Redis node. Look for `System clock synchronized: yes` and a reasonable stratum (< 10). A node whose clock is unsynchronized or drifting against the reference is the source of the skew.

### Step 4 — Managed-service clocks

When Postgres or Redis is a managed service, the dependency clock is the provider's, not a node you can inspect with `kubectl`. Check the provider's status page and the instance's monitoring for a clock-source or maintenance event in the alert window. A provider-side time-source problem requires escalation to the cloud provider.

## Remediation

### Step 1 — Correct NTP on the drifted node

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl debug node/<drifted-node> -it --image=busybox -- \
  chronyc makestep
```

Forces an immediate correction. Watch `lenny_ops_clock_skew_seconds` drop back below the threshold.

### Step 2 — Persistent skew

If the skew recurs after the correction:

1. Cordon the node:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl cordon <drifted-node>
   ```
2. Investigate the node time source (hardware clock, VM host time source, NTP reachability from the node subnet) with your cluster admin team.
3. Reschedule the dependency pod onto a node with a healthy clock if the drifted node cannot be corrected promptly.

### Step 3 — Cluster-wide or shared-source skew

If both dependency nodes drift in a correlated way, the shared upstream NTP source is the likely cause:

1. Check the upstream NTP source reachability from the cluster.
2. Add a redundant NTP source if your control plane permits (chrony `pool` directive).
3. Do not patch individual nodes while the shared NTP source is unhealthy — fix the source first.

### Step 4 — Verify recovery

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_ops_clock_skew_seconds&window=15m
```

- `lenny_ops_clock_skew_seconds` is back below the configured tolerance.
- No new `OpsClockSkewExceeded` firings.
- Remediation locks acquired during the window expire on schedule (cross-check `GET /v1/admin/locks` against expected TTLs).

## Escalation

Escalate to:

- **Cluster admin** when node-level NTP configuration is outside your access.
- **Cloud provider support** when Postgres or Redis is a managed service whose clock you cannot inspect or correct directly.
- **Platform / infrastructure team** for VM host time-source issues in self-managed environments.

Escalate if the skew persists above the tolerance after correcting NTP on both dependency nodes, or if the skew correlates with remediation-lock split-brain events (`LenniOpsLockSplitBrainDetected`); see [ops-lock-split-brain](ops-lock-split-brain.html).

Cross-reference: Spec §25.4 (clock source, Postgres-Redis skew monitoring).
