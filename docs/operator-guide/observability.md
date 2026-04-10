---
layout: default
title: Observability
parent: "Operator Guide"
nav_order: 6
---

# Observability

This page covers Prometheus metrics by component, alert rules with thresholds, SLO definitions and burn-rate alerts, Grafana dashboards, distributed tracing, and log aggregation.

---

## Metrics Overview

Lenny exports Prometheus metrics from every component. All metrics use the `lenny_` prefix.

### Gateway Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_gateway_active_sessions` | Gauge | Active sessions by runtime, pool, state, tenant |
| `lenny_gateway_active_streams` | Gauge | Active streams per replica |
| `lenny_gateway_request_queue_depth` | Gauge | Requests queued awaiting a handler goroutine |
| `lenny_gateway_rejection_rate` | Gauge | Requests rejected with 429/503 per second |
| `lenny_gateway_gc_pause_p99_ms` | Gauge | Process-level GC pause per replica |
| `lenny_gateway_gc_pause_fleet_p99_ms` | Gauge | Fleet-wide P99 GC pause across all replicas |
| `lenny_session_startup_duration_seconds` | Histogram | End-to-end startup (pod claim to session ready) |
| `lenny_session_time_to_first_token_seconds` | Histogram | Session start to first streaming event |

### Gateway Subsystem Metrics

Each subsystem (`stream_proxy`, `upload_handler`, `mcp_fabric`, `llm_proxy`) emits:

| Metric | Type |
|---|---|
| `lenny_gateway_{subsystem}_request_duration_seconds` | Histogram |
| `lenny_gateway_{subsystem}_errors_total` | Counter |
| `lenny_gateway_{subsystem}_queue_depth` | Gauge |
| `lenny_gateway_{subsystem}_circuit_state` | Gauge (0=closed, 1=half-open, 2=open) |
| `lenny_gateway_llm_proxy_active_connections` | Gauge |

### Warm Pool Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_warmpool_idle_pods` | Gauge | Pods in `idle` state ready to be claimed |
| `lenny_warmpool_pod_startup_duration_seconds` | Histogram | Pod creation to `idle` state |
| `lenny_warmpool_replenishment_rate` | Gauge | Pods/min entering `idle` state |
| `lenny_warmpool_warmup_failure_total` | Counter | Pods failing to reach `idle` (by reason) |
| `lenny_warmpool_cold_start_total` | Counter | Sessions served from cold pods |
| `lenny_warmpool_claims_total` | Counter | Warm pod claims |
| `lenny_warmpool_sdk_demotions_total` | Counter | SDK-warm pods demoted before assignment |
| `lenny_warmpool_idle_pod_minutes` | Counter | Cumulative idle pod-minutes (cost tracking) |

### Token Service Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_token_service_request_duration_seconds` | Histogram | By operation (assign, rotate, refresh) |
| `lenny_token_service_errors_total` | Counter | By error type |
| `lenny_token_service_circuit_state` | Gauge | Circuit breaker state |

### Credential Pool Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_credential_lease_assignments_total` | Counter | By provider, pool, source |
| `lenny_credential_rotations_total` | Counter | By reason |
| `lenny_credential_pool_utilization` | Gauge | Active leases / total credentials |
| `lenny_credential_pool_health` | Gauge | Credentials in cooldown |

### Checkpoint Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_checkpoint_duration_seconds` | Histogram | By pool, tier, trigger |
| `lenny_checkpoint_stale_sessions` | Gauge | Sessions exceeding checkpoint interval |
| `lenny_checkpoint_storage_failure_total` | Counter | Non-eviction checkpoint upload failures |
| `lenny_checkpoint_eviction_fallback_total` | Counter | Evictions falling back to Postgres |

### Delegation Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_delegation_budget_utilization_ratio` | Gauge | Budget utilization per tree |
| `lenny_delegation_tree_memory_bytes` | Gauge | In-memory footprint per tree |
| `lenny_delegation_deadlock_detected_total` | Counter | Deadlock detections |
| `lenny_redis_lua_script_duration_seconds` | Histogram | Lua execution latency (budget scripts) |

### Disaster Recovery Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_restore_test_success` | Gauge | 1 if latest restore test passed, 0 if failed |
| `lenny_restore_test_duration_seconds` | Gauge | Duration of latest restore test |

---

## Alert Rules

### Critical Alerts (Page)

| Alert | Condition | Action |
|---|---|---|
| `WarmPoolExhausted` | Available warm pods = 0 for any pool > 60s | Scale pool, check warmup failures |
| `PostgresReplicationLag` | Sync replica lag > 1s for > 30s | Check Postgres instance health |
| `GatewayNoHealthyReplicas` | Healthy replicas below tier minimum > 30s | Check HPA, node capacity |
| `SessionStoreUnavailable` | Postgres primary unreachable > 15s | Check Postgres/PgBouncer connectivity |
| `CheckpointStorageUnavailable` | MinIO checkpoint upload failed after retries | Check MinIO health, network |
| `CredentialPoolExhausted` | 0 assignable credentials > 30s | Add credentials, check cooldowns |
| `CredentialCompromised` | Revoked credential with active leases > 30s | Verify revocation propagation |
| `TokenServiceUnavailable` | Circuit breaker open > 30s | Check Token Service deployment |
| `ControllerLeaderElectionFailed` | Lease not renewed within 15s | Check controller pods, RBAC |
| `DualStoreUnavailable` | Both Postgres and Redis unreachable | Follow dual-store recovery procedure |
| `SessionEvictionTotalLoss` | Both MinIO and Postgres unavailable during eviction | Immediate investigation required |
| `SandboxClaimGuardUnavailable` | Admission webhook unreachable > 30s | Check webhook deployment |

### Warning Alerts

| Alert | Condition | Action |
|---|---|---|
| `WarmPoolLow` | Warm pods < 25% of `minWarm` | Review pool sizing |
| `RedisMemoryHigh` | Memory > 80% of maxmemory | Scale Redis, review eviction |
| `CredentialPoolLow` | Available credentials < 20% | Add credentials |
| `GatewaySessionBudgetNearExhaustion` | Sessions > 90% of `maxSessionsPerReplica` > 60s | HPA lagging; check scale-out |
| `Tier3GCPressureHigh` | Fleet P99 GC > 50 ms > 5 min (Tier 3 only) | Extract LLM Proxy subsystem |
| `CheckpointStale` | Any pool has stale sessions > 60s | Check checkpoint scheduling |
| `PoolConfigDrift` | CRD config doesn't match Postgres > 60s | Check PoolScalingController |
| `WarmPoolReplenishmentFailing` | Warmup failures > 1/min for > 5 min | Check image pulls, setup commands |
| `CircuitBreakerActive` | Any breaker open > 5 min | Review affected subsystem |
| `StorageQuotaHigh` | Tenant storage > 80% of quota | Cleanup or increase quota |

---

## SLO Definitions

All SLO targets are **provisional first-principles estimates** that must be validated by Phase 14.5 load testing before use in customer-facing SLAs.

| SLO | Target (Provisional) | Measurement |
|---|---|---|
| Session creation success rate | 99.5% | Successful starts / total attempts (30d) |
| Session creation latency | P99 < 500ms | Auth through session_id response (excludes upload) |
| Time to first token | P95 < 10s | Session start to first streaming event |
| Session availability | 99.9% | Uptime not in retry/recovery state |
| Gateway availability | 99.95% | Healthy replicas serving requests |
| Startup latency (runc) | P95 < 2s | Pod claim to session ready |
| Startup latency (gVisor) | P95 < 5s | Pod claim to session ready |
| Checkpoint duration (100MB) | P95 < 2s | Quiescence through snapshot upload |

### Burn-Rate Alerts

Multi-window burn-rate alerting ensures both acute outages and slow-burn degradation are caught:

| Alert | SLO | Fast Window | Slow Window |
|---|---|---|---|
| `SessionCreationSuccessRateBurnRate` | 99.5% | 1h, 14x | 6h, 3x |
| `SessionCreationLatencyBurnRate` | P99 < 500ms | 1h, 14x | 6h, 3x |
| `SessionAvailabilityBurnRate` | 99.9% | 1h, 14x | 6h, 3x |
| `GatewayAvailabilityBurnRate` | 99.95% | 1h, 14x | 6h, 3x |
| `StartupLatencyBurnRate` | P95 < 2s (runc) | 1h, 14x | 6h, 3x |
| `StartupLatencyGVisorBurnRate` | P95 < 5s (gVisor) | 1h, 14x | 6h, 3x |
| `TTFTBurnRate` | P95 < 10s | 1h, 14x | 6h, 3x |
| `CheckpointDurationBurnRate` | P95 < 2s | 1h, 14x | 6h, 3x |

**Burn-rate calculation:** At 14x for 1 hour, the alert fires if more than 1.94% of the monthly error budget is consumed in one hour. At 3x for 6 hours, 2.5% of the budget is consumed. Both conditions must fire simultaneously for a page.

Burn-rate thresholds are configurable via:
```yaml
slo:
  burnRate:
    fastMultiplier: 14
    slowMultiplier: 3
```

---

## Distributed Tracing

### Configuration

Lenny uses OpenTelemetry with tail-based sampling:

```yaml
observability:
  otlpEndpoint: "http://otel-collector:4317"
  traceSamplingRate: 0.10   # 10% normal; 100% for errors/slow requests
```

### Sampling Rules

| Condition | Sampling Rate |
|---|---|
| Normal operations | 10% (configurable) |
| Errors (any span with error status) | 100% |
| Slow requests (session creation > 500ms) | 100% |
| Delegation trees (if root is sampled) | 100% (trace completeness) |

### Key Span Boundaries

| Span | Component |
|---|---|
| `session.create` | Gateway |
| `session.claim_pod` | Controller |
| `session.upload` | Gateway + Pod |
| `session.start` | Pod |
| `delegation.spawn_child` | Gateway |
| `credential.assign` | Gateway (credential service) |
| `credential.proxy_request` | Gateway (LLM proxy) |
| `coordinator.handoff` | Gateway |

### Trace Context Flow

```
Client → Gateway (HTTP headers)
  → Pod (gRPC metadata)
    → Gateway (delegation tool calls carry parent context)
      → Child Pod (inherited trace context)
  → External MCP tools (HTTP headers)
```

---

## Key Latency Breakpoints

Instrument four timestamps per session to identify bottlenecks:

1. **Pod claimed** -- time to acquire a warm pod
2. **Workspace prep done** -- file upload and materialization
3. **Session ready** -- agent runtime started
4. **First event/token emitted** -- initial output

---

## Log Aggregation

### Structured Logging

All components emit structured JSON logs with correlation fields:

```json
{
  "level": "INFO",
  "msg": "Session created",
  "session_id": "sess_abc123",
  "tenant_id": "default",
  "trace_id": "4bf92f3577b34da6",
  "span_id": "00f067aa0ba902b7",
  "timestamp": "2026-04-09T10:30:00Z"
}
```

### Log Retention

| Log Type | Postgres Retention | Purpose |
|---|---|---|
| Audit events | 365 days (default, configurable) | Compliance, security analysis |
| Session logs | 30 days | Debugging, support |
| Stream cursors | 7 days | Operational |

### External Aggregation

Estimated log volume:

| Tier | Audit Events | Session Logs | Total |
|---|---|---|---|
| Tier 1 | ~50 MB/day | ~250 MB/day | ~300 MB/day |
| Tier 2 | ~100 MB/day | ~500 MB/day | ~600 MB/day |
| Tier 3 | ~600 MB/day | ~3 GB/day | ~3.6 GB/day |

Configure an external log aggregation stack (ELK, Loki, CloudWatch) for long-term retention beyond the Postgres window.

---

## Grafana Dashboards

The Helm chart includes pre-built Grafana dashboards (enable with `docker compose --profile observability up` in dev mode):

### Platform Overview Dashboard

- Active sessions by runtime and tenant
- Gateway replica health
- Session creation rate and latency
- Error rates by category

### Warm Pool Dashboard

- Idle pods per pool
- Pod startup duration distribution
- Warmup failure rate by reason
- Cold-start session count

### Credential Dashboard

- Pool utilization (active leases / total)
- Credential health scores
- Rotation frequency by reason
- Emergency revocation events

### Delegation Dashboard

- Active delegation trees
- Budget utilization ratio
- Deadlock detection rate
- Lua script latency (budget operations)

### Infrastructure Dashboard

- Postgres write IOPS vs. ceiling
- Redis memory utilization
- MinIO object count and storage
- PgBouncer pool saturation
