---
layout: default
title: "postgres-failover"
parent: "Runbooks"
triggers:
  - alert: PostgresReplicationLag
    severity: critical
  - alert: SessionStoreUnavailable
    severity: critical
components:
  - postgres
symptoms:
  - "gateway logs pq: connection refused"
  - "session creation returns INTERNAL_ERROR"
  - "/v1/oauth/token returns 503 token_store_unavailable"
  - "replication lag elevated for 30+ seconds"
tags:
  - postgres
  - failover
  - durability
requires:
  - admin-api
  - cluster-access
related:
  - pgbouncer-saturation
  - token-store-unavailable
  - schema-migration-failure
---

# postgres-failover

Postgres primary is unreachable, severely lagging, or failing over. Session state writes, credential leasing, and token issuance are all blocked fail-closed until the primary recovers.

## Trigger

- `PostgresReplicationLag` — sync replica lag elevated beyond its configured threshold.
- `SessionStoreUnavailable` — primary unreachable past the configured sustain window.
- Gateway logs: `pq: connection refused`, `pq: terminating connection`, `context deadline exceeded` on database calls.
- `/v1/admin/health` returns `postgres: unhealthy`.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Reachability from the gateway

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -n lenny-system deploy/lenny-gateway -- \
  psql "$POSTGRES_DSN" -c "SELECT now(), pg_is_in_recovery();"
```

Expect `pg_is_in_recovery = false` on the primary. If `true`, the "primary" endpoint is actually pointing at a replica.

### Step 2 — PgBouncer pool state

<!-- access: kubectl requires=cluster-access -->
```bash
psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "SHOW POOLS;"
psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "SHOW CONFIG;"
```

`cl_waiting` high with `sv_active` low means PgBouncer cannot reach Postgres. `cl_waiting` + `sv_active` near `default_pool_size` means connection starvation -- follow [pgbouncer-saturation](pgbouncer-saturation.html).

### Step 3 — Replication lag

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -n lenny-system deploy/lenny-gateway -- \
  psql "$POSTGRES_REPLICA_DSN" -c \
  "SELECT now() - pg_last_xact_replay_timestamp() AS replication_delay;"
```

Sustained lag beyond the configured threshold indicates a replication-tier problem. For managed Postgres (RDS, Cloud SQL), check the provider dashboard.

### Step 4 — Is this an outage or a failover in progress?

<!-- access: api method=GET path=/v1/admin/diagnostics/postgres -->
```
GET /v1/admin/diagnostics/postgres
```

The diagnostic returns: connection probe result, replica lag, last known primary hostname, and whether a failover signal has fired recently.

## Remediation

### Step 1 — Managed Postgres

If the database is managed (RDS, Cloud SQL, Azure DB), the provider handles the failover. Your job:

1. Watch the provider dashboard for a completed failover event.
2. Once the provider reports a new primary, **reload PgBouncer** so it drops stale pooled connections to the old primary:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   psql -h <pgbouncer-host> -p 6432 -U pgbouncer pgbouncer -c "RELOAD;"
   ```
3. Gateway circuit breakers close automatically on the next successful probe.

**Expected outcome:** Gateway stops logging `pq: connection refused` shortly after PgBouncer reload; `lenny_postgres_primary_reachable` returns to 1.

### Step 2 — Self-managed Postgres

1. Identify the healthy replica candidate:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl exec <replica-pod> -- psql -c "SELECT pg_is_in_recovery(), pg_last_xact_replay_timestamp();"
   ```
2. Promote the replica using your HA tooling (Patroni, repmgr, or manual `pg_ctl promote`). Lenny does not operate the promotion itself.
3. Update the DSN Secret if the endpoint changes:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl edit secret lenny-system/postgres-credentials
   ```
4. Reload PgBouncer (see Step 1.2).

### Step 3 — Verify the post-failover state

After the new primary is serving:

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose connectivity
```

Checks:

- `pg_is_in_recovery = false` on the endpoint.
- `SHOW CONFIG;` on PgBouncer returns `pool_mode = transaction`.
- RLS is active: run `SELECT COUNT(*) FROM sessions` as a non-admin tenant user; it must return only that tenant's rows.
- `lenny_postgres_replication_lag_seconds` returns to baseline on new replicas.

### Step 4 — Session reconciliation

Check for sessions that may have been lost during the failover window:

<!-- access: api method=GET path=/v1/admin/sessions -->
```
GET /v1/admin/sessions?state=running&lastCheckpointAgeSeconds=gt:300
```

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT session_id, state, last_checkpoint_at FROM sessions
   WHERE state NOT IN ('completed','failed','cancelled','expired')
     AND last_checkpoint_at < now() - interval '5 minutes';"
```

Notify affected tenants if any sessions straddled the outage window and could not be resumed.

### Step 5 — Post-incident cleanup

- Verify token-store health: `GET /v1/admin/diagnostics/token-service`.
- Audit for in-flight credential leases that may have stuck during the outage: `GET /v1/admin/credential-leases?state=active&ageSeconds=gt:<lease-ttl>`.
- File an incident record if client-visible impact exceeded the availability budget.

## Escalation

Escalate to:

- **Cloud provider support** for managed-Postgres failover that does not complete within the provider's documented RTO.
- **Database operations on-call** for self-managed clusters where replica promotion fails or replication lag does not recover after promotion.
- **Security on-call** if the outage window correlates with elevated `lenny_oauth_token_5xx_total{error_type="token_store_unavailable"}` and tokens may have been issued out-of-band.
