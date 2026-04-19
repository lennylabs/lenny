---
layout: default
title: "gateway-subsystem-extraction"
parent: "Runbooks"
triggers:
  - alert: Tier3GCPressureHigh
    severity: warning
components:
  - gateway
symptoms:
  - "fleet p99 GC pause elevated at Tier 3"
  - "gateway subsystem circuit opens correlated with GC pauses"
  - "gateway latency tail elevated without per-subsystem symptoms"
tags:
  - gateway
  - scaling
  - subsystem
  - tier-3
  - gc-pressure
requires:
  - admin-api
  - cluster-access
related:
  - gateway-capacity
  - gateway-replica-failure
  - circuit-breaker-open
---

# gateway-subsystem-extraction

At Tier 3 (Scale-size) deployments, the gateway runs the stream proxy, upload handler, MCP fabric, LLM proxy, and LLM translator as one process. When a single subsystem becomes the bottleneck — commonly visible as GC-pause pressure — the Lenny design supports **extracting** it to a dedicated Deployment.

This runbook describes the extraction decision and procedure. It is a **structural change**, not a fire-fighting step.

## Trigger

- `Tier3GCPressureHigh` — Fleet p99 GC pause elevated at Tier 3 (threshold deployer-configurable).
- Subsystem extraction-threshold metrics (e.g., `stream_proxy_cpu_share`, `upload_handler_memory_share`) cross the configured extraction thresholds.
- Subsystem-specific circuit breakers open correlated with overall gateway latency spikes.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Per-subsystem cost

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_subsystem_cpu_share&groupBy=subsystem&window=30m
GET /v1/admin/metrics?q=lenny_gateway_subsystem_memory_share&groupBy=subsystem&window=30m
```

Report each subsystem's share of total gateway resource use. The subsystem crossing the configured extraction threshold is the extraction candidate.

### Step 2 — GC pressure signal

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.99, rate(go_gc_pause_seconds_bucket[5m]))&groupBy=service_instance_id&window=30m
```

p99 GC pause consistently elevated above its configured threshold suggests the gateway process has outgrown the shared-process model.

### Step 3 — Circuit correlation

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_subsystem_circuit_open_total&groupBy=subsystem&window=30m
```

If a single subsystem's circuit opens repeatedly while other subsystems are healthy, extraction isolates the blast radius.

### Step 4 — Current tier

Confirm you are on Tier 3 (Scale-size). Extraction is not meaningful on smaller tiers — scale vertically or to Tier 3 first.

## Remediation

### Step 1 — Decision check

Extraction is appropriate when:

- Tier 3 vertical scaling is exhausted (max node size reached).
- One subsystem consistently dominates resource consumption.
- GC pauses or subsystem circuit breakers are impacting SLOs.

Do **not** extract pre-emptively when a subsystem is not yet dominating gateway resource use — extraction adds operational overhead (another Deployment to scale, monitor, and secure).

### Step 2 — Enable subsystem deployment

Update Helm values to extract the subsystem (example for LLM proxy):

```yaml
gateway:
  subsystems:
    llmProxy:
      enabled: true
      replicas: 2
      resources:
        requests: { cpu: 2, memory: 4Gi }
        limits:   { cpu: 4, memory: 8Gi }
```

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml
```

The chart creates `lenny-llm-proxy` Deployment, Service, NetworkPolicy, and updates the main gateway to route to it.

### Step 3 — Wait for rollout

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout status deployment lenny-llm-proxy -n lenny-system --timeout=5m
kubectl rollout status deployment lenny-gateway -n lenny-system --timeout=5m
```

### Step 4 — Verify extraction

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get pods -l app=lenny-llm-proxy -n lenny-system
```

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_gateway_gc_pause_seconds&window=15m
```

- Extracted subsystem reports healthy status.
- Main gateway p99 GC pause drops.
- Subsystem-specific metrics are served by the new Deployment (`service_instance_id` in metrics now includes the subsystem Deployment).

### Step 5 — HPA for the extracted subsystem

Confirm the HPA is attached to the new Deployment:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get hpa lenny-llm-proxy -n lenny-system
```

## Escalation

Escalate to:

- **Capacity / architecture owner** for the extraction decision itself — it is a structural change and should be a reviewed choice, not an emergency reaction.
- **Platform engineering** if extraction is enabled but the symptoms persist — may indicate a bug in the subsystem itself.

Cross-reference: Spec §17.8 (capacity planning — per-subsystem extraction thresholds), §10 (gateway internals).
