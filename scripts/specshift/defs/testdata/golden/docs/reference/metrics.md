---
title: Metrics Reference
---

# Metrics Reference

Lists the metrics the platform emits.

## Gateway metrics

| Metric | Type | Labels | Description | Used by |
|:-------|:-----|:-------|:------------|:--------|
| `lenny_gateway_hand_written` | Gauge | -- | A hand-written row. | -- |

<!-- generated:reference-metrics -->

## Pools metrics

| Metric | Type | Labels | Unit | Emitted by | Description |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `lenny_warmpool_idle_pods` | Gauge | `pool`, `pod_set` |  | WarmPoolController, PoolScalingController | The idle pods of a pod set. |

<!-- /generated:reference-metrics -->

## Alert rules

### Critical alerts

| Alert | Expression / Condition | Severity |
|:------|:-----------------------|:---------|
| `HandWritten` | `x > 0` | Critical |

<!-- generated:reference-alerts -->

### Pools alerts

| Alert | Expression / Condition | For | Severity | Description |
| :-- | :-- | :-- | :-- | :-- |
| `WarmPoolLow` | lenny_warmpool_idle_pods < 0.25 * minWarm | 5m | Warning | A pod set's idle pods dropped below a quarter of minWarm. |

<!-- /generated:reference-alerts -->
