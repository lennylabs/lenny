---
layout: default
title: "postgres-unavailable"
parent: "Runbooks"
triggers:
  - alert: PostgresUnavailable
    severity: critical
components:
  - postgres
  - gateway
symptoms:
  - "every gateway request that touches a tenant-scoped store fails"
  - "the gateway's lenny_postgres_unhealthy gauge is 1"
  - "every health-API component that depends on Postgres reports unhealthy"
tags:
  - chaos
  - store-failure
  - postgres
requires:
  - cluster-access
  - admin-api
related:
  - postgres-failover
  - audit-pipeline-degraded
---

# postgres-unavailable

The gateway cannot reach Postgres. Tenant-scoped reads and writes fail closed; every component that depends on Postgres (sessions, audit, billing, credentials) reports `unhealthy` in the §16.5 health API.

## Trigger

The `PostgresUnavailable` alert fires when `lenny_postgres_unhealthy` is 1 for more than 30 seconds.

## Diagnosis

### Step 1 — Confirm the gateway sees Postgres as unhealthy

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -n lenny-system deployment/lenny-gateway -- \
  curl -s localhost:9090/metrics | grep lenny_postgres
```

### Step 2 — Probe Postgres directly

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -n lenny-system statefulset/lenny-postgres -- pg_isready
```

### Step 3 — Inspect the Postgres logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -n lenny-system statefulset/lenny-postgres --tail=200
```

Look for connection refusals, disk-full errors, or replication failures.

## Remediation

1. If the issue is a single-replica restart, wait for the StatefulSet to recreate the pod.
2. If the issue is disk pressure, free disk on the data PVC and restart Postgres.
3. If the issue is connection-pool exhaustion, follow [audit-pipeline-degraded.md](audit-pipeline-degraded.md) for the PgBouncer half.
4. For an HA topology with a hot standby, follow [postgres-failover.md](postgres-failover.md) and promote the standby.

## Verification

- `pg_isready` returns `accepting connections`.
- `lenny_postgres_unhealthy` returns to 0.
- A test session create succeeds: `lenny-ctl session start --runtime echo`.

## Escalation

Page the on-call DBA when Postgres remains unreachable after the above steps and the cluster is multi-tenant production.
