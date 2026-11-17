---
code: CAT-METRICS
type: catalog
summary: >-
  Every metric with its type, labels, emitting component, and owner page. Generated from the metric
  definition blocks on owner pages; this slice's rows only.
---

<!-- GENERATED from `kind: metric` definition blocks. Do not edit by hand. In the spike this file was written by hand to show what the generator would produce, and holds the warm pools and pod lifecycle slice's rows only. -->

# Metrics catalog

| Metric | Type | Labels | Emitted by (owner) |
| :-- | :-- | :-- | :-- |
| `lenny_agent_pod_state_mirror_lag_seconds` | gauge | `pool` | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_agentpod_finalizer_terminating_seconds` | histogram | `agent_pod` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_controller_leader_lease_renewal_age_seconds` | gauge | `controller` | [WarmPoolController](../components/warm-pool-controller.md#metrics); also the PoolScalingController |
| `lenny_controller_pod_retirement_total` | counter | `reason`, `pool`, `runtime_class` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_controller_queue_overflow_total` | counter | `controller` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_controller_workqueue_depth` | gauge | `controller`, `queue` | [WarmPoolController](../components/warm-pool-controller.md#metrics); also the PoolScalingController |
| `lenny_crd_ssa_conflict_total` | counter | `crd`, `controller` | [WarmPoolController](../components/warm-pool-controller.md#metrics); also the PoolScalingController |
| `lenny_orphaned_claims_total` | counter | `pool` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_pod_claim_conflict_total` | counter | `pool` | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_pod_claim_duration_seconds` | histogram | `pool` | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_pod_claim_fallback_skipped_total` | counter | `reason` | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_pod_claim_fallback_total` | counter | | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_pod_claim_queue_depth` | gauge | `pool` | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_pod_claim_queue_wait_seconds` | histogram | `pool` | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_pod_claim_timeout_total` | counter | `pool` | [Gateway pod claimer](../components/gateway/pod-claimer.md#metrics) |
| `lenny_podclaim_guard_rejections_total` | counter | `service_instance_id` | [Admission webhooks](../components/admission-webhooks.md#metrics) |
| `lenny_pool_bootstrap_mode` | gauge | `pool` | [PoolScalingController](../components/pool-scaling-controller.md#metrics) |
| `lenny_pool_config_reconciliation_lag_seconds` | gauge | `pool` | [PoolScalingController](../components/pool-scaling-controller.md#metrics) |
| `lenny_pool_scaling_admission_denied_total` | counter | `pool`, `pod_set`, `reason` | [PoolScalingController](../components/pool-scaling-controller.md#metrics) |
| `lenny_pool_termination_budget_exceeded_total` | counter | `pool` | [Admission webhooks](../components/admission-webhooks.md#metrics) |
| `lenny_warmpool_claims_total` | counter | `pool`, `runtime_class` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_warmpool_fill_duration_seconds` | histogram | `pool` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_warmpool_idle_pod_minutes` | counter | `pool`, `resource_class` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_warmpool_idle_pods` | gauge | `pool`, `pod_set` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_warmpool_pod_startup_duration_seconds` | histogram | `pool`, `isolation_profile` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_warmpool_replenishment_rate` | gauge | `pool` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_warmpool_reserved_pods` | gauge | `pool`, `pod_set` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |
| `lenny_warmpool_warmup_failure_total` | counter | `pool`, `runtime_class`, `error_type` | [WarmPoolController](../components/warm-pool-controller.md#metrics) |

## Rows the generator would flag

| Metric | Problem |
| :-- | :-- |
| `lenny_gateway_pod_retirement_total` | Emitted by the gateway's release and recycle coordinator, whose page is outside the slice; its meaning is APOD-R8. |
| `lenny_warmpool_failover_claims_served_fraction` | No emitting component is stated. |
| `lenny_warmpool_stale_pods`, `lenny_warmpool_cold_start_total` | §16.1 lists them; no requirement in the slice produces them, and `maxIdleSeconds`, which the first reads, is defined nowhere. |
| `lenny_pool_security_degraded`, `lenny_pool_draining_sessions_total`, `lenny_quarantined_pods` | Emitted for capabilities outside the slice. |
