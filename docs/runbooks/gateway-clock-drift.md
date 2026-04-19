---
layout: default
title: "gateway-clock-drift"
parent: "Runbooks"
triggers:
  - alert: GatewayClockDrift
    severity: warning
  - alert: GatewayClockDriftCritical
    severity: critical
components:
  - gateway
symptoms:
  - "token rejection for not-yet-expired tokens"
  - "replica self-removes from service endpoints"
  - "lenny_time_drift_seconds > threshold"
tags:
  - clock
  - ntp
  - tokens
  - fail-closed
requires:
  - admin-api
  - cluster-access
related:
  - gateway-replica-failure
  - token-store-unavailable
---

# gateway-clock-drift

A gateway replica's clock has drifted beyond tolerance. At the warning threshold, token validation becomes increasingly inaccurate. At the critical threshold, the replica's issuance and validation behavior is unreliable. At `abs(drift) ≥ 5.0s` the replica **self-removes** from Service endpoints — fail-closed, correct behavior. The 5s self-removal is a **hard design invariant** (Spec §13.3), not a deployer-tunable alert threshold.

## Trigger

- `GatewayClockDrift` (warning) — `abs(lenny_time_drift_seconds)` exceeds the configured warning threshold.
- `GatewayClockDriftCritical` — `abs(lenny_time_drift_seconds)` exceeds the configured critical threshold.
- Replica self-removal at `abs(lenny_time_drift_seconds) ≥ 5.0` (design invariant).
- Unexpected `subject_token_expired` / `actor_token_expired` rejections on a specific replica.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules). The 5s self-removal invariant is fixed by design.

## Diagnosis

### Step 1 — Identify the affected replica

The alert labels include `service_instance_id` — note the pod name.

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -l app=lenny-gateway -n lenny-system -o wide
```

Map `service_instance_id` → pod → node.

### Step 2 — Node NTP / chrony status

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl debug node/<node-name> -it --image=busybox -- \
  chronyc tracking
```

Or on systemd nodes:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl debug node/<node-name> -it --image=busybox -- \
  timedatectl status
```

Look for `System clock synchronized: yes` and stratum reasonable (< 10).

### Step 3 — Other replicas

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_time_drift_seconds&groupBy=service_instance_id&window=15m
```

If only one replica is affected, it is node-local. Cluster-wide drift indicates an upstream NTP source problem.

### Step 4 — Token-rejection signal

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_oauth_token_rejected_total&groupBy=reason&window=15m
```

Rising `subject_token_expired` or `actor_token_expired` on the drifted replica confirms real user-visible impact.

## Remediation

### Step 1 — Restart NTP/chrony on the node

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl debug node/<node-name> -it --image=busybox -- \
  chronyc makestep
```

Forces an immediate correction. Watch `lenny_time_drift_seconds` drop.

### Step 2 — Persistent drift

If drift recurs after restart:

1. Cordon the node:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl cordon <node-name>
   ```
2. Drain and reschedule the gateway pod:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl delete pod <gateway-pod> -n lenny-system
   ```
3. Investigate the node (hardware clock, VM host time source, NTP reachability from the node subnet) with your cluster admin team.

### Step 3 — Cluster-wide drift

If multiple replicas show correlated drift:

1. Check the upstream NTP source reachability from the cluster.
2. Add a redundant NTP source if your control plane permits (chrony `pool` directive).
3. Do NOT attempt to patch individual nodes while the shared NTP source is unhealthy — fix the source first.

### Step 4 — Do NOT override self-removal

At drift ≥ 5s, the replica removes itself from Service endpoints. This is the fail-closed escape valve preventing an out-of-sync replica from issuing or validating tokens. The 5s boundary is a design invariant (Spec §13.3), not a tunable. Overriding it would cause token replay / early-expiry anomalies.

### Step 5 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose gateway-clock
```

- All replicas report `lenny_time_drift_seconds` back within the configured warning threshold.
- Replicas back in Service endpoints: `kubectl get endpoints lenny-gateway -n lenny-system` shows all pods.
- Token-rejection rate returns to baseline.

## Escalation

Escalate to:

- **Cluster admin** if NTP configuration at the node level is outside your access.
- **Platform / infrastructure team** for VM host time-source issues in self-managed environments.
- **Security on-call** if the drift window correlates with elevated token-rejection rate AND tokens may have been accepted incorrectly (rare, but check `lenny_oauth_token_accepted_total` vs expected rate during the window).

Cross-reference: Spec §13.3 (clock synchronization tolerance).
