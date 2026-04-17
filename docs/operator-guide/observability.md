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

### Attribute naming (OpenTelemetry semantic conventions)

Metric labels and trace attributes follow the [OpenTelemetry semantic conventions](https://opentelemetry.io/docs/specs/semconv/) where a convention exists. Dot-form OTel attribute names map to Prometheus labels by replacing dots with underscores; both forms refer to the same attribute.

| OTel attribute | Prometheus label | Meaning |
|---|---|---|
| `k8s.pod.name` | `k8s_pod_name` | Name of the Kubernetes pod |
| `service.instance.id` | `service_instance_id` | Stable process/replica identity (gateway replica, controller pod, webhook replica) |
| `service.name` | `service_name` | Service identifier (`lenny-gateway`, `lenny-ops`, etc.) |
| `service.version` | `service_version` | Build tag |
| `error.type` | `error_type` | Error/failure/rejection classification (e.g., `image_pull_error`, `rate_limit`) |

Lenny-specific labels that have no OTel convention retain their domain-specific names: `tenant_id`, `pool`, `runtime_class`, `provider`. A `reason` label is used only for lifecycle/operational classifications (e.g., pod retirement triggers); error classifications use `error_type`.

See the full attribute table in [SPEC §16.1.1](../../spec/16_observability.md#1611-attribute-naming).

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
| `lenny_warmpool_warmup_failure_total` | Counter | Pods failing to reach `idle` (by `error_type`: `image_pull_error`, `setup_command_failed`, `resource_quota_exceeded`, `node_pressure`) |
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
| `EtcdUnavailable` | API server etcd connectivity errors sustained > 15s | Treat as cluster-wide incident |
| `CosignWebhookUnavailable` | Cosign image verification webhook returning errors > 60s | Pod admission blocked; check webhook deployment |
| `DataResidencyWebhookUnavailable` | `lenny-data-residency-validator` webhook unreachable > 30s | Tenant-scoped CRD operations denied; check webhook |
| `DataResidencyViolationAttempt` | Storage write or delegation rejected for region mismatch | Investigate misconfiguration or code-path bypass |
| `PgBouncerAllReplicasDown` | All PgBouncer pods have zero ready replicas | Postgres unreachable; session creation failing |
| `BillingStreamEntryAgeHigh` | Oldest unacknowledged billing stream entry > 80% of TTL | Billing events at risk of TTL expiry; check Postgres |
| `TokenStoreUnavailable` | `/v1/oauth/token` 5xx rate with `error_type="token_store_unavailable"` sustained > 5 min | Token Service database backpressure; check Postgres, Token Service pods |
| `LiteLLMRouteAnomaly` | Non-allowlisted route hit observed on LiteLLM sidecar | Potential sidecar compromise; isolate replica, audit recent config pushes |
| `LiteLLMEgressAnomaly` | Outbound connection to non-allowlisted upstream detected | Investigate SPIFFE boundary, NetworkPolicy coverage |
| `AuditGrantDrift` | Unexpected UPDATE/DELETE grants detected on audit tables | Audit integrity at risk; see [OCSF audit guide](audit-ocsf.md) |

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
| `CredentialProactiveRenewalExhausted` | All proactive renewal retries exhausted before expiry | Session falls through to standard fallback flow |
| `GatewayActiveStreamsHigh` | Active streams per replica > 80% of configured max | Approaching stream proxy capacity; review scaling |
| `ErasureJobFailed` | User-level erasure job failed | User's `processing_restricted` flag remains set; retry job |
| `ErasureJobOverdue` | Erasure job exceeded tier-specific deadline (72h T3, 1h T4) | Requires operator investigation |
| `MemoryStoreGrowthHigh` | User memory count > 80% of `memory.maxMemoriesPerUser` | Review retention policy or increase limit |
| `EtcdQuotaNearLimit` | etcd database size > 80% of `--quota-backend-bytes` | Defragment or increase quota before alarm state |
| `EtcdWriteLatencyHigh` | P99 etcd WAL fsync latency > 25 ms for > 2 min | Leading indicator of etcd saturation; consider dedicated cluster |
| `ControllerWorkQueueDepthHigh` | Work queue depth > 50% of configured max for > 2 min | Controller reconciliation backlog; check CPU throttling |
| `RuntimeUpgradeStuck` | Upgrade state machine in non-terminal state > `phaseTimeoutSeconds` | Pool image upgrade not progressing; investigate |
| `EventBusPublishDropped` | `rate(lenny_event_bus_publish_dropped_total[5m]) > 0` for > 5 min | CloudEvents publishing backpressure; check EventBus transport and `eventBus.publishQueueDepth` |
| `LiteLLMUnexpectedRestart` | Sidecar restarts excluding `config_reload` over 15m > 0 | LiteLLM sidecar crash-looping; check memory limits, upstream provider connectivity |
| `PgAuditSinkDeliveryFailed` | pgaudit events failing to deliver to configured sink | pgaudit forwarding broken; see [OCSF audit guide](audit-ocsf.md) |

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

All components emit structured JSON logs with correlation fields. Field names follow the same OpenTelemetry semantic conventions as metric labels (`k8s.pod.name`, `service.instance.id`, `error.type`); Lenny-specific domain fields (`session_id`, `tenant_id`, `pool`, `runtime_class`) retain their native names.

```json
{
  "level": "INFO",
  "msg": "Session created",
  "session_id": "sess_abc123",
  "tenant_id": "default",
  "k8s.pod.name": "lenny-gateway-7d5f6b-9h2xz",
  "service.instance.id": "gateway-replica-3",
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

### Audit events and EventBus envelopes

Audit events leave the Postgres hot tier as **OCSF v1.1.0** records (see [OCSF audit guide](audit-ocsf.md) for the field mapping). When an audit record crosses the EventBus, it is carried as the `data` field of a **CloudEvents v1.0.2** envelope with `type=dev.lenny.audit.record`. SIEM forwarders consuming from the EventBus should unwrap the CloudEvents envelope first; consumers reading directly from the Postgres audit tables see the OCSF record without any additional wrapping. Full event type catalog: [CloudEvents catalog](../reference/cloudevents-catalog.md).

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
- Warmup failure rate by `error_type`
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
