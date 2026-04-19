---
layout: default
title: "session-eviction-loss"
parent: "Runbooks"
triggers:
  - alert: SessionEvictionTotalLoss
    severity: critical
components:
  - postgres
  - objectStore
symptoms:
  - "eviction occurred while MinIO and Postgres both unavailable"
  - "session data cannot be reconstructed"
  - "affected tenants require notification"
tags:
  - session
  - eviction
  - data-loss
  - durability
requires:
  - admin-api
  - cluster-access
related:
  - dual-store-unavailable
  - minio-failure
  - postgres-failover
---

# session-eviction-loss

A warm-pod eviction occurred during a window where both MinIO and Postgres were unavailable. The affected sessions cannot be restored from either the object-store workspace snapshot (MinIO) or the Postgres checkpoint fallback — the session data is lost.

This is a data-loss event and requires incident declaration and tenant notification.

## Trigger

- `SessionEvictionTotalLoss` alert.
- Audit events `session.evicted_lost`.

## Diagnosis

### Step 1 — Scope of loss

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=session.evicted_lost&since=<outage_window_start>
```

Each event records: `session_id`, `tenant_id`, `pod_name`, `eviction_reason`, `lastCheckpointAt`, `artifactsUploaded`.

### Step 2 — Correlate with outage

Confirm both stores were unavailable at the eviction time:

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_postgres_primary_reachable&window=1h
GET /v1/admin/metrics?q=lenny_minio_cluster_reachable&window=1h
```

### Step 3 — Per-tenant roll-up

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=session.evicted_lost&since=<outage_window_start>&groupBy=tenant_id
```

Use to prepare tenant notifications.

## Remediation

### Step 1 — Stop the bleeding

Ensure the dual-store outage is resolved. See [dual-store-unavailable](dual-store-unavailable.html). No session data can be lost once either store is healthy.

### Step 2 — Do NOT attempt to reconstruct

Session data written to a pod during the dual-outage window was never persisted. There is no safe reconstruction path — tenant workspaces contain arbitrary state, and guessing is worse than reporting loss.

### Step 3 — Preserve evidence

Export the incident window from audit tables before anything ages out:

<!-- access: lenny-ctl -->
```bash
lenny-ctl audit query --since <outage_window_start> \
  --filter 'event_type IN ("session.evicted_lost","session.eviction_start")' \
  --output json > eviction-loss-<date>.json
```

### Step 4 — Prepare tenant notification

1. Group affected sessions by tenant.
2. For each tenant: prepare a summary with session IDs, creation time, and last-known state.
3. Provide the notification through your normal channel (incident page, account team, email).
4. Record the notification in the incident timeline.

### Step 5 — Post-incident

1. Root-cause the dual-store outage itself.
2. If both stores shared a dependency (node pool, AZ, network), file remediation to separate them.
3. Review eviction-policy aggressiveness — should evictions be paused during degraded-storage windows? Spec §12.5 defines the current trade-off.

## Escalation

Escalate **immediately**:

- **Platform on-call / incident commander** — data loss is a declared incident.
- **Security on-call** — data-loss events sometimes intersect compliance obligations (notification windows).
- **Compliance officer / DPO** — notification requirements vary by jurisdiction and contract; typical window is 72 hours.
- **Tenant account teams** — they carry the conversation with affected customers.
