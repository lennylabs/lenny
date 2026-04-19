---
layout: default
title: "pgbouncer-saturation"
parent: "Runbooks"
triggers:
  - alert: PgBouncerPoolSaturated
    severity: warning
components:
  - postgres
  - pgbouncer
symptoms:
  - "cl_waiting_time elevated sustained"
  - "elevated session creation latency"
  - "gateway logs context deadline exceeded on DB"
tags:
  - postgres
  - pgbouncer
  - connection-pool
requires:
  - admin-api
  - cluster-access
related:
  - postgres-failover
  - gateway-capacity
---

# pgbouncer-saturation

PgBouncer's client pool is saturated: clients are queued (`cl_waiting` non-zero, `cl_waiting_time` elevated). Applies to self-managed Postgres topologies where Lenny fronts Postgres with PgBouncer.

## Trigger

- `PgBouncerPoolSaturated` alert — `cl_waiting_time` elevated beyond the configured threshold for the configured window (see [Metrics Reference](../reference/metrics.html#alert-rules)).
- Gateway logs: `context deadline exceeded` on DB operations.
- Session creation p95 latency climbs sharply.

## Diagnosis

### Step 1 — Pool state

<!-- access: kubectl requires=cluster-access -->
```bash
psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "SHOW POOLS;"
```

Compare `cl_active + cl_waiting` against `default_pool_size`. If `cl_waiting` persistently > 0, the pool is saturated.

### Step 2 — Max client connections

<!-- access: kubectl requires=cluster-access -->
```bash
psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "SHOW CONFIG;"
psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "SHOW STATS;"
```

If total client connections approach `max_client_conn`, new connection attempts are rejected at TCP accept.

### Step 3 — Wait-time vs query-time

<!-- access: kubectl requires=cluster-access -->
```bash
# pgbouncer_exporter exposes these as Prometheus metrics
```

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=pgbouncer_avg_wait_time_seconds&window=15m
GET /v1/admin/metrics?q=pgbouncer_avg_query_seconds&window=15m
```

Sustained `avg_wait_time` above the alert threshold with normal `avg_query_time` confirms pool starvation (not slow queries).

### Step 4 — Postgres-side check

<!-- access: kubectl requires=cluster-access -->
```bash
psql "$POSTGRES_DSN" -c "
  SELECT count(*), wait_event_type, wait_event
  FROM pg_stat_activity
  GROUP BY 2,3 ORDER BY 1 DESC LIMIT 20;"
```

High lock waits or I/O waits on Postgres require separate diagnosis — fix the slow queries first; growing the pool will not help.

## Remediation

### Step 1 — Immediate relief: raise pool size at runtime

<!-- access: kubectl requires=cluster-access -->
```bash
psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c \
  "SET default_pool_size=<new-value>;"
```

Calculate `new-value ≤ (postgres.max_connections - reserved) / pgbouncer_replicas`. Leave ≥ 10 % headroom for superuser + replication connections.

### Step 2 — Persistent fix: update Helm

Set `pgbouncer.defaultPoolSize` and `pgbouncer.maxClientConn` in your `values.yaml`:

```yaml
pgbouncer:
  defaultPoolSize: 200
  maxClientConn: 2000
```

Then:

<!-- access: kubectl requires=cluster-access -->
```bash
helm upgrade lenny lennylabs/lenny -f values.yaml
```

### Step 3 — Scale PgBouncer horizontally

If a single PgBouncer replica is CPU-bound or connection-bound:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl scale deployment lenny-pgbouncer -n lenny-system --replicas=<N>
```

Each added replica multiplies backend connections toward Postgres by `default_pool_size`. Verify `max_connections` on Postgres is not about to be exceeded.

### Step 4 — If Postgres max_connections is the hard limit

Either:

- Increase `max_connections` on Postgres and restart (self-managed) or request a tier bump (managed).
- Temporarily reduce gateway replicas to shed load:
  <!-- access: kubectl requires=cluster-access -->
  ```bash
  kubectl scale deployment lenny-gateway -n lenny-system --replicas=<N-1>
  ```

### Step 5 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose pgbouncer
```

- `cl_waiting` returns to 0 in `SHOW POOLS;`.
- `cl_waiting_time` drops back within baseline.
- `PgBouncerPoolSaturated` alert clears within the evaluation window.

### Step 6 — Post-incident

Review `lenny_gateway_db_query_duration_seconds` p99 for the affected window to quantify user impact. If saturation has recurred within 7 days, re-evaluate sizing against current Tier targets ([Spec §17.8](https://github.com/lennylabs/lenny/blob/main/spec/17_deployment-topology.md#178-capacity-planning-and-defaults)).

## Escalation

Escalate if:

- `max_connections` cannot be raised (managed-Postgres tier limit).
- Saturation recurs daily — structural capacity problem; loop in capacity-planning owner.
- `cl_waiting_time` does not recover after pool expansion — look for Postgres-side bottleneck (lock contention, runaway query).
