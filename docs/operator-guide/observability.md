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
| `lenny_credential_rotations_total` | Counter | By `error_type` (rate_limit, auth_expired, provider_unavailable) |
| `lenny_credential_pool_utilization` | Gauge | Active leases / total credentials |
| `lenny_credential_pool_health` | Gauge | Credentials in cooldown |

### Checkpoint Metrics

| Metric | Type | Description |
|---|---|---|
| `lenny_checkpoint_duration_seconds` | Histogram | By pool, level, trigger |
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
| `GatewayNoHealthyReplicas` | Healthy replicas below the deployment size's configured minimum for > 30s | Check the Horizontal Pod Autoscaler (HPA) and node capacity |
| `SessionStoreUnavailable` | Postgres primary unreachable > 15s | Check Postgres/PgBouncer connectivity |
| `CheckpointStorageUnavailable` | MinIO checkpoint upload failed after retries | Check MinIO health, network |
| `CredentialPoolExhausted` | 0 assignable credentials > 30s | Add credentials, check cooldowns |
| `CredentialCompromised` | Revoked credential (pool- or user-scoped) with active leases > 30s | Verify revocation propagation |
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
| `TokenStoreUnavailable` | `/v1/oauth/token` returning 503 with `error_type="token_store_unavailable"` for > 30s | Postgres primary unreachable for token issuance (fail-closed); session creation, delegation minting, and credential leasing all fail until Postgres primary recovers |
| `LLMUpstreamEgressAnomaly` | Outbound connection from gateway pod to non-allowlisted upstream detected | Investigate pod identity boundary, NetworkPolicy `allow-gateway-egress-llm-upstream` coverage |
| `AuditGrantDrift` | Unexpected UPDATE/DELETE grants detected on audit tables | Audit integrity at risk; see [OCSF audit guide](audit-ocsf.md) |
| `ArtifactReplicationResidencyViolation` | ArtifactStore replication residency preflight observed a jurisdiction-tag mismatch or cross-border transfer attempt | Replication for the affected region is suspended; fix jurisdiction mismatch and invoke `POST /v1/admin/artifact-replication/{region}/resume` |
| `LegalHoldEscrowResidencyViolation` | Phase 3.5 of a tenant force-delete aborted because the resolved escrow region has no `storage.regions.<region>.legalHoldEscrow` entry, or the region's escrow KMS key / bucket endpoint is unreachable (CMP-054) | Configure the missing region-scoped escrow target (Helm values) and re-invoke `POST /v1/admin/tenants/{id}/force-delete` |
| `PlatformAuditResidencyViolation` | A platform-tenant audit event referencing a non-platform `target_tenant_id` (impersonation, legal-hold escrow ledger, compliance decommission) failed to commit because the target tenant's regional platform-Postgres is misconfigured or unreachable (CMP-058) | Configure the missing `storage.regions.<region>.postgresEndpoint` entry (or restore platform-Postgres reachability); the originating operation will succeed once audit can commit |
| `AuditRedactionReceiptMissing` | Row classified `chainIntegrity=redacted_gdpr` has no matching signed `RedactionReceipt` | Investigate orphaned GDPR redaction vs. genuine tamper; escalate as compliance incident if no receipt |
| `T4KmsKeyUnusable` | Leader-elected T4 KMS probe has not succeeded within `2 * storage.t4KmsProbeInterval` (default interval 300s, min 60s); T4 envelope-encryption writes fail closed | Restore KMS reachability for the configured T4 key; T4 writes remain rejected until `lenny_t4_kms_probe_last_success_timestamp` is fresh again |

### Warning Alerts

| Alert | Condition | Action |
|---|---|---|
| `WarmPoolLow` | Warm pods < 25% of `minWarm` | Review pool sizing |
| `RedisMemoryHigh` | Memory > 80% of maxmemory | Scale Redis, review eviction |
| `CredentialPoolLow` | Available credentials < 20% | Add credentials |
| `GatewaySessionBudgetNearExhaustion` | Sessions > 90% of `maxSessionsPerReplica` > 60s | HPA lagging; check scale-out |
| `Tier3GCPressureHigh` | Fleet P99 GC > 50 ms > 5 min (Scale-size deployments only) | Extract the gateway's LLM routing subsystem to a dedicated service |
| `CheckpointStale` | Any pool has stale sessions > 60s | Check checkpoint scheduling |
| `PoolConfigDrift` | CRD config doesn't match Postgres > 60s | Check PoolScalingController |
| `WarmPoolReplenishmentFailing` | Warmup failures > 1/min for > 5 min | Check image pulls, setup commands |
| `CircuitBreakerActive` | Any breaker open > 5 min | Review affected subsystem |
| `LLMTranslationLatencyHigh` | P95 `lenny_gateway_llm_translation_duration_seconds` > 100 ms sustained 5 min | Investigate gateway translator regression or payload-size change |
| `LLMTranslationSchemaDrift` | `lenny_gateway_llm_translation_errors_total{error_type="schema_mismatch"}` rate > 0 sustained 5 min | Investigate runtime/SDK request drift or upstream provider response schema change |
| `ExperimentIsolationRejections` | `lenny_experiment_isolation_rejections_total` rate > 0 sustained 2 min | Re-validate every active experiment's variant pool `isolationProfile` against the strictest enrolled session `minIsolationProfile`; re-provision weaker pools or deactivate the experiment for the affected traffic class |
| `StorageQuotaHigh` | Tenant storage > 80% of quota | Cleanup or increase quota |
| `CredentialProactiveRenewalExhausted` | All proactive renewal retries exhausted before expiry | Session falls through to standard fallback flow |
| `GatewayActiveStreamsHigh` | Active streams per replica > 80% of configured max | Approaching stream proxy capacity; review scaling |
| `ErasureJobFailed` | User-level erasure job failed | User's `processing_restricted` flag remains set; retry job |
| `ErasureJobOverdue` | Erasure job exceeded the data classification's deadline (72h for T3, 1h for T4) | Requires operator investigation |
| `MemoryStoreGrowthHigh` | `rate(lenny_memory_store_user_count_over_threshold_total[5m]) > 0` sustained 5 min (any user in the tenant is at >= 80% of `memory.maxMemoriesPerUser`) | Review retention policy or increase limit |
| `MemoryStoreErasureDurationHigh` | P99 of `lenny_memory_store_operation_duration_seconds{operation="delete_by_user"}` > 60s sustained 10 min, OR P99 with `operation="delete_by_tenant"` > 300s. Whole-scope erasure calls invoked by the GDPR erasure job are breaching per-backend SLO. | Investigate backend health, vector-index contention, or shard connectivity before `ErasureJobOverdue` fires on the aggregate job clock. Custom `MemoryStore` backends must emit the two erasure labels; see operator-guide backend contract. |
| `EtcdQuotaNearLimit` | etcd database size > 80% of `--quota-backend-bytes` | Defragment or increase quota before alarm state |
| `EtcdWriteLatencyHigh` | P99 etcd WAL fsync latency > 25 ms for > 2 min | Leading indicator of etcd saturation; consider dedicated cluster |
| `ControllerWorkQueueDepthHigh` | Work queue depth > 50% of configured max for > 2 min | Controller reconciliation backlog; check CPU throttling |
| `RuntimeUpgradeStuck` | Upgrade state machine in non-terminal state > `phaseTimeoutSeconds` | Pool image upgrade not progressing; investigate |
| `EventBusPublishDropped` | `rate(lenny_event_bus_publish_dropped_total[5m]) > 0` for > 5 min | CloudEvents publishing backpressure; check EventBus transport and `eventBus.publishQueueDepth` |
| `PgAuditSinkDeliveryFailed` | pgaudit events failing to deliver to configured sink | pgaudit forwarding broken; see [OCSF audit guide](audit-ocsf.md) |
| `GatewayQueueDepthHigh` | Any gateway subsystem queue depth exceeds tier-scaled threshold (50/200/800) sustained > 5 min | Subsystem is admitting work faster than it drains; precedes `GatewaySubsystemCircuitOpen` |
| `GatewayLatencyHigh` | P95 gateway subsystem request duration exceeds tier-scaled threshold (2.0s / 1.0s / 0.5s) sustained > 10 min | Degraded end-to-end performance in the subsystem; may precede circuit-breaker trips |
| `PodStateMirrorStale` | `lenny_agent_pod_state_mirror_lag_seconds > 60` sustained > 60s | WarmPoolController is not writing state transitions; Postgres-backed pod claim fallback is disabled for any pool whose lag exceeds `podClaimFallbackMaxMirrorLagSeconds` |
| `LegalHoldOverrideUsed` | `gdpr.legal_hold_overridden` audit event emitted | `platform-admin` bypassed DeleteByUser legal-hold preflight with `acknowledgeHoldOverride: true`; compliance review required |
| `LegalHoldOverrideUsedTenant` | `gdpr.legal_hold_overridden_tenant` audit event emitted | `platform-admin` bypassed tenant-delete Phase 3.5 legal-hold gate; Phase 3.5 re-encrypted held evidence to escrow; compliance review required |
| `CompliancePostureDecommissioned` | `compliance.profile_decommissioned` audit event emitted | `platform-admin` lowered a regulated `complianceProfile` via `POST /v1/admin/tenants/{id}/compliance-profile/decommission`; generic `PUT` downgrades are blocked by the one-way ratchet; review `justification` and `remediation_attestations` |
| `OutstandingInflightAtRotationCeiling` | `lenny_credential_rotation_inflight_ceiling_hit_total` incremented | 300s in-flight gate ceiling hit during a non-proactive rotation; runtime may have failed to emit `llm_request_completed` |
| `PreStopCapFallbackRateHigh` | Per-replica combined share of 90s conservative-fallback selections exceeds 5% over 15 min | Indicates preStop Stage 2 regularly falling through to the conservative cap; correlate with Postgres outage or cold handoff cache |
| `DrainReadinessWebhookUnavailable` | `lenny-drain-readiness` webhook unreachable | Node drains will not check MinIO health before pod eviction |
| `EphemeralContainerCredGuardUnavailable` | `lenny-ephemeral-container-cred-guard` webhook unreachable > 5 min | `pods/ephemeralcontainers` updates denied in agent namespaces; `kubectl debug` attach attempts will be rejected until the webhook recovers — credential-boundary invariant remains protected by fail-closed policy |
| `AdmissionPlaneFeatureFlagDowngrade` | `lenny-deployment-phase-stamp` ConfigMap records `features.<flag>.enabled=true` but the corresponding `ValidatingWebhookConfiguration` is absent for > 2 minutes. The alert evaluates `kube_configmap_labels{configmap="lenny-deployment-phase-stamp", label_lenny_dev_flag_<flag-slug>_enabled="true"} unless on() kube_validatingwebhookconfiguration_info{name="<webhook>"}` with one PrometheusRule per `(flag, webhook)` pair — see SPEC §16.5 for the canonical expression body and the full four-pair rule decomposition. Flag-to-webhook mapping per SPEC §17.2 Feature-gated chart inventory: `features.llmProxy` → `lenny-direct-mode-isolation`; `features.drainReadiness` → `lenny-drain-readiness`; `features.compliance` → `lenny-data-residency-validator` AND `lenny-t4-node-isolation`. **Operator precondition:** `kube-state-metrics` MUST be started with `--metric-labels-allowlist=configmaps=[lenny.dev/flag-*]` (or the chart-equivalent setting) so the chart-rendered `lenny.dev/flag-<flag>-enabled: "true"` labels on the phase-stamp ConfigMap surface as metric labels on `kube_configmap_labels` — without the allowlist the expression evaluates empty and the alert is inoperative. See the "Admission-plane feature flags → Downgrade enforcement" section of `configuration.md` for the allowlist configuration. | A feature-flag-gated admission webhook has been removed without clearing its phase-stamp entry; this is the sole runtime signal for feature-flag downgrade drift (the paired per-webhook `*Unavailable` alerts do NOT fire because the PrometheusRule is gated on the same flag that removed the webhook). Follow [admission-plane-feature-flag-downgrade](../runbooks/admission-plane-feature-flag-downgrade.html): determine via audit log whether the flag-flip was an intentional, acknowledged downgrade (`deployment.feature_flag_downgrade_acknowledged` event). If unintentional, `helm upgrade --set features.<flag>=true` restores the webhook. If intentional, `helm upgrade --set features.<flag>=false --set acceptFeatureFlagDowngrade.<flag>=true` commits the acknowledgement audit event and retains the phase-stamp entry in the degraded state |
| `ElicitationContentTamperDetected` | `increase(lenny_elicitation_content_tamper_detected_total[5m]) > 0` — any non-zero count indicates an intermediate pod re-emitted an MCP `elicitation/create` wire frame for an existing elicitation whose `{message, schema}` pair diverges from the gateway-recorded original; per the gateway-origin-binding invariant in §9.2, the forward was dropped with `ELICITATION_CONTENT_TAMPERED` and the modified text never reached the client. | Investigate the `tampering_pod` label on the firing alert: suspend the associated session, review the runtime's prompt-injection posture, and if the tampering pod is an agent runtime, consider revoking its delegation lease. Correlate with the paired `elicitation.content_tamper_detected` audit event for `elicitation_id`, `origin_pod`, `divergent_fields`, and `original_sha256` / `attempted_sha256` context. |
| `CircuitBreakerStale` | `lenny_circuit_breaker_cache_stale_seconds > 60` on any replica | AdmissionController's circuit-breaker cache has not refreshed from Redis; admission decisions served against stale state; correlate with Redis unreachability |
| `QuotaFailOpenCumulativeThreshold` | `max by (service_instance_id) (lenny_quota_failopen_cumulative_seconds) > 0.8 * quotaFailOpenCumulativeMaxSeconds` sustained > 60s | Pre-breach warning before a replica exhausts its rolling 1-hour cumulative fail-open budget (default 300s) and transitions to fail-closed for quota enforcement; escalate the underlying Redis outage, tune `quotaFailOpenCumulativeMaxSeconds` per financial-risk exposure, or adjust `quotaPerReplicaHardCap` before fail-closed shutters new session creation |
| `QuotaFailOpenUserFractionInoperative` | `lenny_quota_user_failopen_fraction >= 0.5` — continuously-firing Prometheus alert. The gateway's configured per-user fail-open fraction (default `0.25`) is substantially weakened; at `>= 0.5` a single user can consume at least half the tenant's per-replica fail-open allocation during a Redis outage, so the monopolization-prevention intent of the control is largely inoperative even though the formula itself remains correct. The gateway additionally emits a matching structured log warning at startup, and `lenny-ops` emits the same warning during config validation — the alert fires continuously while the condition holds so it is visible to operators who joined after startup. | Lower `quotaUserFailOpenFraction` below 0.5 to keep the per-user fail-open cap meaningful; values at or above 0.5 let a single runaway user consume the tenant ceiling during a Redis outage |
| `LegalHoldCheckpointAccumulationProjectedBreach` | `(lenny_storage_quota_bytes_used + sum by (tenant_id) (lenny_legal_hold_checkpoint_projected_growth_bytes)) > 0.9 * lenny_tenant_storage_quota_bytes and on(tenant_id) lenny_tenant_legal_hold_active_count > 0` — predictive projection that 24-hour legal-hold checkpoint growth will push the tenant's shared `storageQuotaBytes` bucket past 90% utilization; gated on at least one active legal hold. The right-hand denominator uses the `lenny_tenant_storage_quota_bytes` gauge (per-tenant `storageQuotaBytes` config value) so the expression is a concrete PromQL comparison rather than a bare config identifier. Fires ahead of the reactive 80% `StorageQuotaHigh` / `CheckpointStorageHigh` thresholds when held-session checkpoint accumulation is the dominant driver (legal-hold sessions are exempt from the "latest 2 checkpoints" rotation). | Follow [legal-hold-quota-pressure](../runbooks/legal-hold-quota-pressure.html): coordinate with compliance to clear or narrow the hold, raise the tenant's `storageQuotaBytes`, or route the affected session to a pool with tighter `workspaceSizeLimitBytes` |

---

## Operational and audit events

Beyond alert-firing metrics, the gateway emits CloudEvents-wrapped operational events for admission-time advisory checks and security-salient state transitions. These flow through the same Redis-backed event stream as the session-lifecycle events and the `callbackUrl` webhook. See SPEC §16.6 (operational events) and §16.7 (audit events) for full envelope shape.

| Event | Severity | When | Key payload fields |
|---|---|---|---|
| `experiment.variant_weaker_than_tenant_floor` | Warning | Admission-time tenant-floor advisory check (experiment create/update, including `?dryRun=true`) detects a variant pool whose resolved `isolationProfile` is weaker than the tenant-level `minIsolationProfile` floor. The experiment is still creatable — but sessions whose resolved `minIsolationProfile` lands at the tenant floor will be rejected at routing time with `VARIANT_ISOLATION_UNAVAILABLE`. One event per offending variant per admission call. | `tenant_id`, `experiment_id`, `variant_id`, `variant_pool_isolation`, `tenant_floor`, `actor_sub`, `emitted_at` |
| `circuit_breaker.state_changed` | Audit | Every operator-managed circuit-breaker state transition via `POST /v1/admin/circuit-breakers/{name}/open` or `.../close`. Not sampled — one row per admin action. Written through the standard append-only audit path. | `circuit_name`, `old_state`, `new_state`, `reason`, `limit_tier`, `scope`, `operator_sub`, `operator_tenant_id`, `timestamp` |

---

## SLO Definitions

All SLO targets are **provisional first-principles estimates** that must be validated by Growth-sized load testing (the SLO-validation milestone) before use in customer-facing SLAs.

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
global:
  traceSamplingRate: 0.10   # 10% normal; 100% for errors/slow requests
observability:
  otlpEndpoint: "http://otel-collector:4317"
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
  "ts": "2026-04-09T10:30:00Z",
  "level": "INFO",
  "msg": "Session created",
  "component": "gateway",
  "session_id": "sess_abc123",
  "tenant_id": "default",
  "k8s.pod.name": "lenny-gateway-7d5f6b-9h2xz",
  "service.instance.id": "gateway-replica-3",
  "trace_id": "4bf92f3577b34da6",
  "span_id": "00f067aa0ba902b7"
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

| Size | Audit Events | Session Logs | Total |
|---|---|---|---|
| Starter | ~50 MB/day | ~250 MB/day | ~300 MB/day |
| Growth | ~100 MB/day | ~500 MB/day | ~600 MB/day |
| Scale | ~600 MB/day | ~3 GB/day | ~3.6 GB/day |

Configure an external log aggregation stack (ELK, Loki, CloudWatch) for long-term retention beyond the Postgres window.

### Audit events and EventBus envelopes

Audit events leave the Postgres hot tier as **OCSF v1.1.0** records (see [OCSF audit guide](audit-ocsf.md) for the field mapping). When an audit record crosses the EventBus, it is carried as the `data` field of a **CloudEvents v1.0.2** envelope with `datacontenttype=application/ocsf+json` and a `type` identifying the specific audit event (e.g. `dev.lenny.audit_session_terminated`). SIEM forwarders consuming from the EventBus should unwrap the CloudEvents envelope first; consumers reading directly from the Postgres audit tables see the OCSF record without any additional wrapping. Full event type catalog: [CloudEvents catalog](../reference/cloudevents-catalog.md).

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
- Rotation frequency by `error_type`
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
