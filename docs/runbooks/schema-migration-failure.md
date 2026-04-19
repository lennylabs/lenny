---
layout: default
title: "schema-migration-failure"
parent: "Runbooks"
triggers:
  - alert: SchemaMigrationFailed
    severity: critical
  - alert: SchemaMigrationDirty
    severity: critical
components:
  - postgres
  - controlPlane
symptoms:
  - "lenny-ctl migrate status shows dirty or unexpected phase"
  - "migration Job in Failed state"
  - "gateway startup fails with schema mismatch"
tags:
  - postgres
  - migrations
  - upgrade
  - ddl
requires:
  - admin-api
  - cluster-access
related:
  - postgres-failover
  - crd-upgrade
---

# schema-migration-failure

A `golang-migrate`-managed schema migration failed mid-way or is stuck. The `schema_migrations` table contains a `dirty` flag, or the observed phase does not match the expected phase of an expand-contract migration.

## Trigger

- `lenny-ctl migrate status` shows an unexpected phase (e.g., `phase1_applied` when `phase3_applied` was expected).
- A migration Kubernetes Job completed with `Failed`.
- The `schema_migrations` table has `dirty = true`.

## Diagnosis

### Step 1 — Migration state

<!-- access: lenny-ctl -->
```bash
lenny-ctl migrate status
```

Look for dirty versions or unexpected phase values.

### Step 2 — Advisory lock

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT * FROM pg_locks WHERE locktype = 'advisory';"
```

A held advisory lock indicates a migration is either still running or crashed mid-way without releasing the lock.

### Step 3 — schema_migrations state

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 5;"
```

`dirty = true` means the migration started but did not complete.

### Step 4 — Migration Job logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs job/lenny-migrate-<version> -n lenny-system
```

Look for the specific DDL failure — common examples:

- `cannot drop column ... because other objects depend on it` (a view or index you need to drop first).
- `deadlock detected` (concurrent DDL or long-running transaction holding locks).
- `could not serialize access due to concurrent update` (transient).

### Step 5 — Schema vs expected

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c '\d <table>'
```

Compare the observed schema to the expected schema in the migration file. Partial DDL application dictates remediation path.

## Remediation

### Step 1 — Forward-fix (preferred)

If the failure is due to a missing prerequisite (e.g., a dependent view blocking `DROP COLUMN`):

1. Author a forward-fix migration that resolves the dependency (e.g., drops or recreates the view).
2. Commit it in the codebase ahead of the blocked migration.
3. Clear the dirty flag and re-run:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "UPDATE schema_migrations SET dirty = false WHERE version = <N>;"
```

<!-- access: lenny-ctl -->
```bash
lenny-ctl migrate up
```

### Step 2 — Re-run on transient failure

If the cause is transient (connection timeout, advisory-lock contention with a crashed prior run):

1. Release stale advisory locks if any were held by a dead session:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
     "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle in transaction' AND query LIKE '%schema_migrations%';"
   ```
2. Clear the dirty flag as above.
3. Re-run `lenny-ctl migrate up`.

### Step 3 — Down migration (LAST RESORT)

Only if forward-fix is not feasible and the partial state is actively harmful:

<!-- access: lenny-ctl -->
```bash
lenny-ctl migrate down --version <N> --confirm
```

This launches the down-migration Job using `down.sql` to reverse the partial DDL. Confirm with `lenny-ctl migrate status` that the version is rolled back and `dirty = false`.

### Step 4 — Preflight

After any recovery:

<!-- access: lenny-ctl -->
```bash
lenny-ctl preflight --config values.yaml
```

Verifies the cluster is ready for the gateway version you have deployed.

### Step 5 — Verify gateway accepts the schema

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-gateway -n lenny-system
kubectl rollout status deployment lenny-gateway -n lenny-system --timeout=2m
```

If gateway pods start cleanly, the migration is consistent with the deployed code version.

## Escalation

Escalate to:

- **DBA / database-operations** for DDL errors that are specific to data shape (orphaned rows, incompatible type casts).
- **Platform engineering** for down-migration path when it is unclear whether the down DDL is safe given data written since the up-migration ran.
- **Release engineer** if the failed migration was tied to a gateway image upgrade; do not roll forward the gateway until the schema is consistent.

Cross-reference: Spec §10.5 (expand-contract discipline), Phase 1.5 deliverable `docs/runbooks/db-rollback.md` (broader database rollback procedure).
