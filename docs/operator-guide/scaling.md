---
layout: default
title: Scaling
parent: "Operator Guide"
nav_order: 4
---

# Scaling

This page covers deployment sizes, HPA (Horizontal Pod Autoscaler) configuration, warm pool sizing, the PoolScalingController formula, capacity calibration methodology, subsystem extraction triggers, and KEDA integration.

---

## Deployment Sizes

Lenny defines four T-shirt deployment sizes that determine infrastructure sizing, SLO targets, and operational complexity:

| Parameter | Starter | Growth | Scale | Platform |
|---|---|---|---|---|
| Max concurrent sessions | 100 | 1,000 | 10,000 | 100,000 |
| Session creation rate (sustained) | 5/s | 30/s | 200/s | 2,000/s |
| Gateway RPS (all endpoints) | 500 | 5,000 | 50,000 | 500,000 |
| Delegation fan-out (concurrent) | 10 | 100 | 500 | 5,000 |
| Active tenants | 5 | 50 | 500 | 5,000 |
| LLM proxy concurrent streams | 50 | 500 | 5,000 | 50,000 |

**Size progression:**

- **Starter through Scale** are achievable with the standard architecture by scaling replica counts, instance sizes, and data store topology.
- **Platform** requires swapping one or more scaling extension interfaces to their high-scale implementations (e.g., `PostgresPodRegistry` replacing `CRDPodRegistry`).

---

## Infrastructure Sizing

### Gateway Replicas

| Size | Min Replicas | Max Replicas | `maxSessionsPerReplica` |
|---|---|---|---|
| Starter | 2 | 4 | 50 (provisional) |
| Growth | 3 | 10 | 200 (provisional) |
| Scale | 5 | 30 | 400 (provisional) |

### Postgres

| Size | Instance Class | Estimated Write Ceiling | 80% Alert Threshold |
|---|---|---|---|
| Starter | 2 vCPU / 4 GB | ~200 IOPS | ~160 IOPS |
| Growth | 4 vCPU / 16 GB | ~600 IOPS | ~480 IOPS |
| Scale | 8 vCPU / 32 GB | ~1,600 IOPS | ~1,280 IOPS |

### Redis

| Size | Memory | Notes |
|---|---|---|
| Starter | 2 GB | Single node |
| Growth | 8 GB | Sentinel (3 nodes) |
| Scale | 16 GB | Sentinel (3 nodes) or managed cluster |

### Object Storage (MinIO / S3 / GCS)

| Size | Topology |
|---|---|
| Starter | Single node |
| Growth | 4-node erasure coded |
| Scale | 8-node erasure coded |

---

## HPA Configuration

### Primary Scale-Out Trigger: Request Queue Depth

The primary HPA metric is `lenny_gateway_request_queue_depth`, which reflects instantaneous back-pressure:

```yaml
gateway:
  hpa:
    enabled: true
    minReplicas: 2
    maxReplicas: 10
    metrics:
      - type: Pods
        pods:
          metric:
            name: lenny_gateway_request_queue_depth
          target:
            type: AverageValue
            averageValue: 10
```

### Metric Role Table

| Metric | Role | Notes |
|---|---|---|
| `lenny_gateway_request_queue_depth` | Primary HPA trigger | Reacts faster than CPU; does not depend on Prometheus Adapter cache TTL when using KEDA |
| `lenny_gateway_active_streams` | Secondary HPA metric | Per-replica gauge of in-flight streaming connections |
| `active_sessions / maxSessionsPerReplica` | Alert only | Fires `GatewaySessionBudgetNearExhaustion` at > 90%; must NOT be an HPA trigger |

### KEDA Integration

> **KEDA is mandatory for Scale-size deployments.** At Scale size, the Prometheus Adapter path requires `minReplicas: 30` (which equals `maxReplicas`), providing no headroom for further scale-out. Starter and Growth sizes can use standard HPA with the Prometheus Adapter, though KEDA is recommended for faster reaction times.

For more responsive autoscaling that bypasses the Prometheus Adapter cache, use KEDA with a `ScaledObject`:

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: lenny-gateway
  namespace: lenny-system
spec:
  scaleTargetRef:
    name: lenny-gateway
  minReplicaCount: 2
  maxReplicaCount: 50
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus:9090
        metricName: lenny_gateway_request_queue_depth
        query: avg(lenny_gateway_request_queue_depth)
        threshold: "10"
    - type: prometheus
      metadata:
        serverAddress: http://prometheus:9090
        metricName: lenny_gateway_active_streams
        query: avg(lenny_gateway_active_streams)
        threshold: "200"
```

### HPA Queue Depth Targets by Deployment Size

The `lenny_gateway_request_queue_depth` HPA target `averageValue` varies by deployment size to provide tighter back-pressure control at higher scale:

| Size | `request_queue_depth` Target |
|---|---|
| Starter | 15 |
| Growth | 10 |
| Scale | 5 |

### Recommended Starting `minWarm` Values by Deployment Size

The following baseline values assume `safety_factor = 1.0` (no safety margin), a 25-second failover window, and 10-second pod startup. **For production deployments**, apply the size-specific safety factor to get the production `minWarm`.

| Size | Baseline `minWarm` (per hot pool) | Safety Factor (agent-type) | Safety Factor (mcp-type) |
|---|---|---|---|
| Starter | 20 | 1.5 | 2.0 |
| Growth | 175 | 1.5 | 2.0 |
| Scale | 1,050 | 1.2 | 1.5 |

With safety factors applied: Starter = `ceil(0.5 * 1.5 * 35) = 27`, Growth = `ceil(5 * 1.5 * 35) = 263`, Scale = `ceil(30 * 1.2 * 35) = 1,260`.

### Delegation Fan-Out Impact on `minWarm`

Delegation fan-out can significantly increase required `minWarm`. At Scale size, the baseline of 1,050 covers session-creation demand only. Deployments where a significant fraction of sessions use the `orchestrator` preset (or other high-fan-out leases) should increase `minWarm` using the delegation-adjusted formula -- a value of approximately **3,400+** (with burst term, zero historical burst data) is appropriate for Scale-size deployments when orchestrator-preset sessions represent a large share of load. If orchestrator-preset sessions are rare (< 10% of sessions), the baseline 1,050 remains adequate.

Reference SPEC Section 17.8.2 "Delegation fan-out sizing (SCL-041)" for the full formula and worked examples.

---

## Warm Pool Sizing

### PoolScalingController Formula

The PoolScalingController reconciles pool sizing from Postgres configuration into CRDs. The base formula for session-mode pools:

```
target_minWarm = ceil(base_demand_p95 × safety_factor × (failover_seconds + pod_startup_seconds)
                      + burst_p99_claims × pod_warmup_seconds)
```

### Mode Adjustment Factor

For task and concurrent execution modes, the formula includes a `mode_factor` divisor:

| Mode | `mode_factor` | `burst_mode_factor` |
|---|---|---|
| `session` | 1.0 | 1.0 |
| `task` | `avg_tasks_per_pod_lifetime` (converges toward `maxTasksPerPod`) | 1.0 |
| `concurrent` | `maxConcurrent` | `maxConcurrent` |

**Adjusted formula:**

```
target_minWarm = ceil(base_demand_p95 × safety_factor × (failover_seconds + pod_startup_seconds) / mode_factor
                      + burst_p99_claims × pod_warmup_seconds / burst_mode_factor)
```

### Cold-Start Bootstrap Procedure

When a pool is first created, the PoolScalingController has no historical traffic data. During the bootstrap period:

1. The controller uses a static `bootstrapMinWarm` override (configurable per pool)
2. As traffic data accumulates, the formula-driven target begins computing
3. After 48 hours (or when manually exited), the controller switches to formula-driven scaling
4. The `PoolBootstrapMode` alert fires if bootstrap persists beyond 72 hours

Use `lenny-ctl admin pools exit-bootstrap --pool <name>` to manually switch to formula-driven scaling when early traffic data is sufficient.

### Pool Warming Behavior

When a pool's `minWarm > 0` and there are zero idle pods, the pool enters `PoolWarmingUp` state:

- The WarmPoolController provisions pods to reach `minWarm`
- Session creation requests targeting the pool return `503 RUNTIME_UNAVAILABLE` with a `Retry-After` header
- The condition clears once `idlePodCount >= 1`
- The `WarmPoolBootstrapping` alert fires if warming exceeds `warmupDeadlineSeconds` (default: 300s)

---

## Capacity Calibration Methodology

### Ramp Test (first-working-slice deliverable)

The ramp test determines the empirical saturation point for `maxSessionsPerReplica`:

1. **Ramp test.** Drive a single gateway replica from 0 to `maxSessionsPerReplica x 1.5` concurrent sessions in 10% increments. At each step record:
   - `lenny_stream_proxy_p99_attach_latency_seconds`
   - `lenny_stream_proxy_queue_depth`
   - `lenny_gateway_gc_pause_p99_ms`
   - RSS (resident set size)

2. **Saturation point.** Identify the session count at which:
   - `lenny_stream_proxy_p99_attach_latency_seconds` first exceeds 0.8 s (800 ms), OR
   - `lenny_stream_proxy_queue_depth` first exceeds 500

3. **Budget setting.** Set `maxSessionsPerReplica` to the saturation point minus 20% headroom.

4. **HPA validation.** Confirm that HPA scale-out triggered by `request_queue_depth` fires at least one full HPA scale cycle (2-3 minutes) before saturation.

5. **Document and replace.** Replace provisional values with calibrated values annotated with the benchmark run ID and date.

---

## Subsystem Extraction Triggers

The gateway's four internal subsystems can be extracted to dedicated services when scaling demands it.

### Extraction Readiness Table

| Subsystem | Key Metrics | Indicative Threshold (provisional) |
|---|---|---|
| **Stream Proxy** | `queue_depth`, `goroutines`, `p99_attach_latency_seconds` | Queue depth > 500 or p99 > 0.8 s (800 ms) sustained > 5 min |
| **Upload Handler** | `active_uploads`, `queue_depth`, `p99_latency_seconds` | Active uploads > 200 concurrent sustained |
| **MCP Fabric** | `active_delegations`, `goroutines`, `p99_orchestration_latency_seconds` | Active delegations > 1,000 or p99 > 2.0 s (2,000 ms) |
| **LLM Proxy** | `active_connections`, `upstream_goroutines`, `p99_ttfb_seconds` | Active connections > 2,000 or exceeding 60% of maxConcurrent |

### LLM Proxy-to-Session Ratio Guidance

| Ratio (`llm_proxy_connections / active_sessions`) | Action |
|---|---|
| Below 0.3:1 | Combined binary sustainable at Scale size |
| 0.3:1 to 0.5:1 | Monitor `gc_pause_p99_ms` for early signs |
| Above 0.5:1 sustained > 15 min | Begin planning extraction |
| Approaching 1:1 | LLM Proxy is effectively as large as Stream Proxy |

### Shared-Process GC Pressure Signal

When any two subsystems simultaneously approach their thresholds, the combined goroutine count causes GC pauses. The metric `lenny_gateway_gc_pause_p99_ms` sustained > 50 ms signals that the process boundary is becoming a bottleneck.

The `Tier3GCPressureHigh` alert fires when the fleet-wide P99 GC pause exceeds 50 ms for > 5 minutes on Scale-size deployments.

### Extraction Threshold Helm Values

All thresholds are configurable via Helm:

```yaml
gateway:
  extractionThresholds:
    streamProxy:
      queueDepth: 500
      p99AttachLatencyMs: 800
    uploadHandler:
      activeConcurrent: 200
    mcpFabric:
      activeDelegations: 1000
    llmProxy:
      activeConnections: 2000
```

---

## Size Promotion Planning

### Starter to Growth

- Increase gateway replicas from 2-4 to 3-10
- Scale Postgres to 4 vCPU / 16 GB
- Scale Redis to 8 GB with Sentinel HA
- Move to erasure-coded MinIO (4 nodes)
- **Run the first-working-slice benchmark** to calibrate `maxSessionsPerReplica`
- Replace all provisional values with calibrated measurements

### Growth to Scale

- **Prerequisites:**
  1. Pre-hardening load tests confirm LLM Proxy extraction has occurred OR `lenny_gateway_llm_proxy_active_connections / lenny_gateway_active_sessions` is sustainably below 0.3:1
  2. `gc_pause_p99_ms` remains below 50 ms at Growth-size peak load
- Increase gateway replicas to 5-30
- Scale Postgres to 8 vCPU / 32 GB
- Consider separate billing/audit Postgres instance
- Scale Redis to 16 GB
- Move to 8-node erasure-coded MinIO

### Scale to Platform

Platform size requires infrastructure changes beyond horizontal scaling:

- Swap `CRDPodRegistry` to `PostgresPodRegistry` (eliminates etcd write pressure)
- Deploy multi-shard `StoreRouter`
- Deploy durable `EventBus` (NATS/Kafka replacing Redis pub/sub)
- Consider `ClusterRegistry` for multi-cluster delegation routing

---

## Scaling Checklist

Before any size promotion:

- [ ] Run the ramp test and calibrate `maxSessionsPerReplica`
- [ ] Update `capacityPlanning.*` Helm values with observed workload parameters
- [ ] Replace all provisional extraction thresholds with calibrated values
- [ ] Verify warm pool sizing formula outputs match expected pod counts
- [ ] Validate HPA scale-out fires before saturation point
- [ ] Confirm `GatewaySessionBudgetNearExhaustion` alert fires at 90%
- [ ] Update `postgres.writeCeilingIops` with measured values
- [ ] Run preflight checks at the new size: `lenny-ctl preflight --config values.yaml`
