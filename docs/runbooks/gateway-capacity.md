---
layout: default
title: "gateway-capacity"
parent: "Runbooks"
triggers:
  - alert: GatewayActiveStreamsHigh
    severity: warning
  - alert: GatewaySessionBudgetNearExhaustion
    severity: warning
components:
  - gateway
symptoms:
  - "active streams approaching configured max"
  - "sessions-per-replica approaching maxSessionsPerReplica"
  - "elevated p95 request latency"
tags:
  - gateway
  - capacity
  - hpa
  - scaling
requires:
  - admin-api
  - cluster-access
related:
  - gateway-replica-failure
  - gateway-subsystem-extraction
---

# gateway-capacity

Gateway replicas are approaching the per-replica capacity ceilings (`maxActiveStreams`, `maxSessionsPerReplica`). HPA will scale out, but the alert fires to give operators time to either (a) verify the HPA is doing its job, or (b) pre-empt a sustained spike.

## Trigger

- `GatewayActiveStreamsHigh` — active streams approaching the configured `maxActiveStreams` ceiling.
- `GatewaySessionBudgetNearExhaustion` — `sessions / maxSessionsPerReplica` sustained near its ceiling.

Exact threshold percentages and evaluation windows are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — HPA state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get hpa lenny-gateway -n lenny-system
kubectl describe hpa lenny-gateway -n lenny-system
```

Is the HPA actively scaling? `AbleToScale=True` with recent `SuccessfulRescale` events = working. `ScalingLimited=True` with `reason=TooFewReplicas` or `DesiredReplicasZero` = stuck.

### Step 2 — Per-replica load

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_active_streams&groupBy=service_instance_id&window=15m
GET /v1/admin/metrics?q=lenny_gateway_active_sessions&groupBy=service_instance_id&window=15m
```

Evenly-loaded replicas approaching the cap = legitimate scale-up needed. Hot-spotted single replica = load-balancing issue (see below).

### Step 3 — Hot-spot detection

If a single replica dominates, check the Service / Ingress affinity configuration. MCP Streamable HTTP often exhibits session-affinity; hot-spotting can indicate a long-lived stream holding a replica.

### Step 4 — Node-level

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_memory_bytes&groupBy=service_instance_id&window=30m
GET /v1/admin/metrics?q=container_cpu_usage_seconds_total&labels=pod=~lenny-gateway.*&window=30m
```

Memory near limit = OOM risk (see [gateway-replica-failure](gateway-replica-failure.html)).

## Remediation

### Step 1 — Let HPA scale

If HPA is scaling and alerts clear within minutes, no action needed.

### Step 2 — Raise maxReplicas

If HPA is limited by `maxReplicas`:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl patch hpa lenny-gateway -n lenny-system --type=json \
  -p '[{"op":"replace","path":"/spec/maxReplicas","value":<new-value>}]'
```

Then persist via Helm values (`gateway.autoscaling.maxReplicas`) and `helm upgrade` so the change survives a rollout.

### Step 3 — Scale out manually (pre-empt a spike)

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl scale deployment lenny-gateway -n lenny-system --replicas=<current+3>
```

HPA will drive replicas back down after the spike if its target metrics return to baseline.

### Step 4 — Per-replica tuning

If replicas are CPU/memory-bound well before the stream cap:

- Raise `gateway.resources.limits.memory` in Helm values and `helm upgrade`.
- Revisit `maxActiveStreams` — lower it to reduce per-replica pressure at the cost of needing more replicas.

### Step 5 — Subsystem extraction

If a single subsystem (e.g., MCP fabric) is the bottleneck, see [gateway-subsystem-extraction](gateway-subsystem-extraction.html) for the subsystem-split procedure.

### Step 6 — Verify

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_active_streams_ratio&window=15m
```

- Active streams ratio back within the alert's warning threshold.
- Session ratio back within the alert's warning threshold.
- HPA reports `ScalingLimited=False`.

## Escalation

Escalate to:

- **Capacity owner** for repeated capacity alerts within a week — may indicate a tier bump is needed.
- **Platform engineering** for suspected hot-spotting without a clear cause — may require Service-level config review.
