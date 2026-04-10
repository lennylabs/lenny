---
layout: default
title: Disaster Recovery
parent: "Operator Guide"
nav_order: 7
---

# Disaster Recovery

This page covers RPO/RTO targets, Postgres/Redis/MinIO high availability, backup schedules, automated restore testing, zone failure analysis, and recovery procedures.

---

## RPO/RTO Targets

| Component | RPO | RTO | Mechanism |
|---|---|---|---|
| Postgres (session state, tokens) | 0 (zero data loss) | < 30s | Synchronous replication + auto failover |
| Redis (cache, leases) | Ephemeral | < 15s | Sentinel failover; rebuild from Postgres |
| MinIO (artifacts, checkpoints) | Near-zero (erasure coding) | < 30s (reads); < 5 min (node replacement) | Erasure coding + site replication |

### Key Invariants

- **No committed transaction is lost** -- Postgres synchronous replication ensures RPO = 0
- **Redis is reconstructible** -- all durable state lives in Postgres; Redis is a fast-path cache
- **Session recovery is automatic** -- sessions on lost pods enter the retry/recovery flow

---

## Postgres High Availability

### Deployment Options

| Deployment | Recommendation | HA Mechanism |
|---|---|---|
| Cloud (production) | Managed PostgreSQL (RDS Multi-AZ, Cloud SQL HA, Azure DB) | Provider-managed failover |
| On-prem / self-managed | CloudNativePG operator or Patroni on Kubernetes | Operator-managed failover |
| Local dev | Single container via docker-compose | No HA |

### Requirements

- **Synchronous replication** -- primary and sync replica must be in different availability zones
- **Automatic failover** -- managed services handle this natively; self-managed deployments must configure Patroni or CloudNativePG
- **Connection pooling** -- PgBouncer (self-managed) or provider proxy (cloud-managed) in transaction mode

### Monitoring

| Metric | Alert | Threshold |
|---|---|---|
| Replication lag | `PostgresReplicationLag` (Critical) | > 1s for > 30s |
| Write IOPS | `PostgresWriteSaturation` (Warning) | > 80% of ceiling for > 5 min |
| PgBouncer wait time | `PgBouncerPoolSaturated` (Warning) | > 1s for > 60s |

---

## Redis High Availability

### Deployment Options

| Deployment | Recommendation | HA Mechanism |
|---|---|---|
| Cloud (production) | Managed Redis (ElastiCache, Memorystore, Azure Cache) | Provider-managed failover |
| On-prem / self-managed | Redis Sentinel (3 nodes across zones) | Sentinel-managed failover |
| Local dev | Single container | No HA |

### Requirements

- **TLS + AUTH** -- required in all environments
- **Sentinel nodes spread across zones** -- prevents single-zone failure from losing quorum
- **Memory monitoring** -- `RedisMemoryHigh` alert fires at 80% of maxmemory

### Recovery from Redis Loss

Redis loss is designed to be recoverable because all durable state lives in Postgres:

1. **Quota counters** -- rehydrated from Postgres checkpoints using the MAX rule
2. **Routing cache** -- rebuilt from SessionStore pod assignments
3. **Delegation budget counters** -- reconstructed from `delegation_tree_budget` table
4. **Slot counters** -- blocking rehydration from Postgres before accepting slot assignments
5. **Rate limit counters** -- reset (brief fail-open window, bounded by per-replica ceiling)

---

## MinIO / Object Storage High Availability

### Deployment Options

| Tier | Topology | Durability |
|---|---|---|
| Tier 1 | Single node | Daily backup |
| Tier 2 | 4-node erasure coded | Near-zero RPO; daily replication |
| Tier 3 | 8-node erasure coded | Near-zero RPO; site replication |

### Cloud Alternatives

| Cloud | Service | Configuration |
|---|---|---|
| AWS | S3 | Versioning enabled, lifecycle rules |
| GCP | Cloud Storage | Versioning enabled, lifecycle rules |
| Azure | Blob Storage | Versioning enabled, lifecycle management |

### What MinIO Stores

| Data Type | Impact of Loss |
|---|---|
| Workspace snapshots | Active sessions lose workspace state (recovered from last checkpoint) |
| Checkpoint archives | Session recovery from prior checkpoint or conversation-only resume |
| Uploaded artifacts | Original files must be re-uploaded by client |
| Eviction context objects | Extended context for eviction resume lost (2KB inline fallback) |

---

## Backup Schedule

### Postgres

| Backup Type | Frequency | Retention |
|---|---|---|
| Continuous WAL archival | Continuous | 7 days minimum |
| Base backups | Daily | 30 days minimum |
| Destination | Object storage (S3/GCS/Azure Blob) | Cross-region recommended |

### MinIO

| Backup Type | Frequency | Retention |
|---|---|---|
| Bucket replication | Daily (or site replication) | 30 days minimum |
| Destination | Secondary MinIO cluster or cloud storage | Cross-region recommended |

### Redis

Redis does not require backup -- it is reconstructed from Postgres on failure. RDB/AOF persistence is optional and used only to reduce recovery time.

---

## Automated Restore Testing

### `lenny-restore-test` CronJob

Lenny includes a CronJob that validates backup integrity and measures restore time:

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: lenny-restore-test
  namespace: lenny-system
spec:
  schedule: "0 3 1 * *"    # Monthly at 3 AM on the 1st
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: restore-test
              image: ghcr.io/lenny-dev/lenny-restore-test:latest
              # ...
```

### What It Tests

1. **Postgres restore** -- creates a temporary instance from the latest base backup + WAL, verifies schema integrity and row counts against the primary, runs a smoke query
2. **MinIO restore** -- restores a test bucket and performs object checksum comparison
3. **Timing** -- records elapsed restore time and emits metrics
4. **Cleanup** -- tears down the test instance

### Metrics Emitted

| Metric | Type | Description |
|---|---|---|
| `lenny_restore_test_success` | Gauge | 1 if passed, 0 if failed |
| `lenny_restore_test_duration_seconds` | Gauge | Elapsed restore time |

### Alert

An alert fires if the measured RTO exceeds targets (< 30s Postgres, < 5 min MinIO).

---

## Zone Failure Analysis

### Blast Radius

Loss of one availability zone causes:

| Component | Impact | Recovery |
|---|---|---|
| Gateway replicas | Surviving replicas absorb traffic (PDB ensures minimum) | Automatic via HPA |
| Postgres | Automatic failover to sync replica in another zone | < 30s |
| Redis | Sentinel promotes a replica in another zone | < 15s |
| Agent pods | Sessions on lost pods enter retry flow | Warm pods in surviving zones serve new requests |
| MinIO | Erasure coding provides read availability from surviving nodes | < 30s for reads; < 5 min for node replacement |

### Key Guarantees

- **No data loss** for committed transactions (Postgres synchronous replication)
- **Automatic gateway recovery** via topology spread constraints and PDB
- **Session continuity** via checkpoint/resume mechanism

### Cross-Zone Requirements

| Component | Requirement |
|---|---|
| Postgres | Primary and sync replica in different AZs |
| Redis | Sentinel nodes spread across AZs |
| Gateway | Topology spread constraints for multi-zone distribution |
| Agent pods | Pool-level topology constraints |

---

## Recovery Procedures

### Postgres Failover Recovery

**Automatic (managed services):**

1. Provider detects primary failure
2. Sync replica promoted to primary (< 30s)
3. Gateway reconnects automatically via PgBouncer/proxy
4. No operator action required

**Self-managed (Patroni/CloudNativePG):**

1. Patroni/operator detects primary failure
2. Sync replica promoted
3. PgBouncer health probe detects new primary
4. Monitor `PostgresReplicationLag` for new sync replica establishment

### Redis Recovery

1. Sentinel detects primary failure and promotes a replica (< 15s)
2. Gateway reconnects to new primary
3. Quota counters rehydrated from Postgres (MAX rule applied)
4. Delegation budget counters reconstructed
5. Rate limit counters reset (brief fail-open window)
6. Monitor `lenny_delegation_budget_reconstruction_total` for reconstruction events

### MinIO Recovery

**Single node failure (erasure coded):**

1. Surviving nodes continue serving reads
2. Replace failed node
3. Erasure repair reconstructs missing data
4. Monitor `lenny_checkpoint_storage_failure_total` during recovery

**Complete MinIO outage:**

1. Checkpoint uploads fail -- sessions continue without new checkpoints
2. `CheckpointStorageUnavailable` alert fires
3. Eviction checkpoints fall back to Postgres minimal state
4. On recovery, periodic checkpoints resume automatically

### Dual-Store Unavailability (Postgres + Redis)

When both stores are simultaneously unreachable:

1. `DualStoreUnavailable` critical alert fires immediately
2. New session creation rejected (503)
3. `PLATFORM_DEGRADED` events pushed to active client streams
4. Existing sessions continue with in-memory state
5. Recovery is automatic when either store becomes reachable

### Session Recovery After Pod Loss

1. Gateway detects pod failure (lease expiry or kubelet notification)
2. Session marked for recovery with `recovery_generation` incremented
3. New pod claimed from warm pool
4. Workspace restored from last successful checkpoint
5. Conversation history replayed from EventStore
6. Client receives `session.resumed` event with `resumeMode`

If no checkpoint exists, the session resumes with `resumeMode: "conversation_only"` and `workspaceLost: true`.

---

## Pre-Drain Health Check

Before draining a node, the WarmPoolController performs a MinIO health check webhook. If MinIO is unhealthy, the drain is blocked to prevent eviction checkpoints from failing and triggering the total-loss path.

The forced-drain override (`lenny.dev/drain-force: "true"`) bypasses this check -- use only when both MinIO and Postgres health have been verified manually.
