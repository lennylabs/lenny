---
layout: default
title: "gateway-replica-failure"
parent: "Runbooks"
triggers:
  - alert: GatewayNoHealthyReplicas
    severity: critical
components:
  - gateway
symptoms:
  - "stream clients receive connection errors"
  - "lenny_gateway_healthy_replicas drops below tier minimum"
  - "gateway pods in CrashLoopBackOff"
tags:
  - gateway
  - availability
  - oom
  - hpa
requires:
  - admin-api
  - cluster-access
related:
  - gateway-capacity
  - crd-upgrade
  - gateway-clock-drift
---

# gateway-replica-failure

One or more gateway pods are crashed or unready. Client-facing REST/MCP streams to affected replicas disconnect; active sessions persist (state is in Postgres/Redis) and reconnect to healthy replicas.

## Trigger

- `GatewayNoHealthyReplicas` — healthy gateway replicas below tier minimum for > 30s.
- `lenny_gateway_healthy_replicas` drops below tier `minReplicas`.
- Clients report MCP stream disconnects or REST 503s.

## Diagnosis

### Step 1 — Pod state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -l app=lenny-gateway -n lenny-system
kubectl describe pod <failing-pod> -n lenny-system | tail -50
```

Look for `CrashLoopBackOff`, `OOMKilled`, failing `readinessProbe`, or `ImagePullBackOff`.

### Step 2 — Recent logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs <failing-pod> -n lenny-system --previous --since=10m | tail -100
```

Key patterns:

- `runtime: out of memory` → OOM (see Remediation Step 1).
- `crd \"sandboxes.lenny.dev\" not found` or schema mismatch → CRD drift ([crd-upgrade](crd-upgrade.html)).
- `dial tcp ... postgres` → database reachability ([postgres-failover](postgres-failover.html)).
- `certificate signed by unknown authority` → mTLS chain ([cert-manager-outage](cert-manager-outage.html)).

### Step 3 — HPA / autoscaling state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get hpa lenny-gateway -n lenny-system
kubectl describe hpa lenny-gateway -n lenny-system
```

`ScalingLimited=true` with `reason=TooFewReplicas` can indicate the HPA cannot react fast enough to in-flight traffic.

### Step 4 — Memory / CPU pressure

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_memory_bytes&groupBy=pod&window=30m
GET /v1/admin/metrics?q=container_cpu_usage_seconds_total&labels=pod=~lenny-gateway.*&window=30m
```

Sustained memory near the Helm-configured limit is the common OOM signal.

## Remediation

### Step 1 — OOM

1. Confirm the OOM class from `kubectl describe pod` (`Reason: OOMKilled`).
2. Temporary relief: scale out:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl scale deployment lenny-gateway -n lenny-system --replicas=<current+2>
   ```
3. Persistent fix: raise `gateway.resources.limits.memory` in Helm values and `helm upgrade`.

### Step 2 — Startup crash (CRD or schema mismatch)

Follow [crd-upgrade](crd-upgrade.html). A partial Helm upgrade that left CRDs behind is the most common cause.

### Step 3 — Image pull

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl describe pod <failing-pod> -n lenny-system | grep -A3 "Events:"
```

`ImagePullBackOff` usually means a missing `imagePullSecret` or a digest pinned to an image that no longer exists. Correct the digest and `helm upgrade`.

### Step 4 — Clock drift

If logs show `token expired` anomalies, the replica may be rejecting valid tokens; see [gateway-clock-drift](gateway-clock-drift.html).

### Step 5 — Verify recovery

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -l app=lenny-gateway -n lenny-system
```

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_healthy_replicas&window=5m
```

- All gateway pods `Ready=True`.
- `lenny_gateway_healthy_replicas` ≥ tier `minReplicas`.
- `lenny_gateway_active_sessions` consistent with Postgres:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT COUNT(*) FROM sessions WHERE state = 'running';"
```

Material divergence points at in-flight state reconciliation issues; investigate before closing the incident.

## Escalation

Escalate if:

- Gateway cannot reach steady state after repeated scaling and rollout restarts.
- Crashes correlate with specific tenants or session types (a payload-triggered bug) — capture a crashdump for engineering.
- Clock drift exceeds the self-removal invariant and a replica has self-removed ([gateway-clock-drift](gateway-clock-drift.html)).
- A Helm rollback is under consideration — coordinate with the release engineer before reverting the gateway image.
