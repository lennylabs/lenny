---
layout: default
title: "checkpoint-stale"
parent: "Runbooks"
triggers:
  - alert: CheckpointStale
    severity: warning
  - alert: CheckpointDurationHigh
    severity: warning
  - alert: CheckpointDurationBurnRate
    severity: warning
components:
  - warmPools
symptoms:
  - "lenny_checkpoint_stale_sessions non-zero sustained"
  - "p95 checkpoint duration elevated against the SLO target"
  - "checkpoint SLO burn-rate elevated"
tags:
  - checkpoint
  - durability
  - sessions
requires:
  - admin-api
  - cluster-access
related:
  - minio-failure
  - postgres-failover
  - session-eviction-loss
---

# checkpoint-stale

Sessions whose state is older than the freshness SLO (`checkpoint.stalenessThreshold`) are present, or p95 checkpoint duration is elevated. Risk: on eviction, state loss exceeds the advertised RPO. Alert thresholds and SLO targets are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Trigger

- `CheckpointStale` — `lenny_checkpoint_stale_sessions` is non-zero for sustained duration.
- `CheckpointDurationHigh` — p95 checkpoint duration above the warning threshold over the configured window.
- `CheckpointDurationBurnRate` — checkpoint-duration SLO burning at the configured fast rate.

## Diagnosis

### Step 1 — Which sessions are stale?

<!-- access: api method=GET path=/v1/admin/sessions -->
```
GET /v1/admin/sessions?state=running&lastCheckpointAgeSeconds=gt:<checkpoint.stalenessThreshold>
```

### Step 2 — Checkpoint duration distribution

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_checkpoint_duration_seconds_bucket[5m]))&groupBy=pool&window=30m
```

Is elevation pool-wide or scoped to specific pools / runtimes?

### Step 3 — Storage-side latency

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, rate(lenny_minio_put_duration_seconds_bucket[5m]))&window=30m
```

If MinIO write latency is elevated, the bottleneck is the object store.

### Step 4 — Checkpoint size distribution

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.95, lenny_checkpoint_size_bytes)&groupBy=pool&window=30m
```

Unusually large checkpoints slow everything down; trace back to the session or pool.

## Remediation

### Step 1 — Storage bottleneck

If MinIO latency is the cause, follow [minio-failure](minio-failure.html).

### Step 2 — Inspect and recover individual stale sessions

For each session surfaced in Diagnosis Step 1, inspect its state and last checkpoint:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin sessions get <id>
```

If the session is unrecoverable (runtime pod gone, workspace materialization stuck), force-terminate so the tenant can create a fresh session from the last durable checkpoint:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin sessions force-terminate <id>
```

Watch `lenny_checkpoint_stale_sessions` drop to 0 as the backlog clears.

### Step 3 — Large-checkpoint tenants

If a tenant consistently produces checkpoints larger than the pool's configured `checkpoint.maxSizeBytes`:

1. Confirm via metrics by tenant.
2. Work with the tenant to reduce workspace footprint or pre-serialize state deterministically.
3. Raise `checkpoint.postgresFallbackMaxBytes` only if you can absorb the Postgres cost; prefer workspace shaping.

### Step 4 — Concurrency

<!-- access: kubectl requires=cluster-access -->
```bash
# Raise checkpoint worker concurrency (Helm value)
```

`checkpointWorker.concurrency` in Helm values controls the per-gateway checkpoint-worker count.

### Step 5 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin sessions get <id>
```

- `lenny_checkpoint_stale_sessions` = 0.
- p95 checkpoint duration back under SLO.
- `CheckpointDurationBurnRate` decelerating.

## Escalation

Escalate to:

- **Object-store operators** if MinIO / S3 latency cannot be reduced at the storage tier.
- **Capacity owner** if checkpoint concurrency is saturated at tier limits — may indicate a structural sizing mismatch.
- **Tenant account team** for tenants whose workspace footprint consistently violates checkpoint SLOs.
