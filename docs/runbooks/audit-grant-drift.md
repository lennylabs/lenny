---
layout: default
title: "audit-grant-drift"
parent: "Runbooks"
triggers:
  - alert: AuditGrantDrift
    severity: critical
components:
  - audit
symptoms:
  - "unexpected UPDATE or DELETE grants on audit tables"
  - "audit table permissions diverged from baseline"
tags:
  - audit
  - security
  - postgres
  - grants
requires:
  - cluster-access
related:
  - audit-pipeline-degraded
  - postgres-failover
---

# audit-grant-drift

A scheduled grant-baseline check detected that one or more audit tables have acquired UPDATE or DELETE privileges for a role that should only have INSERT/SELECT. The audit tables are append-only by design; any grant drift is a potential integrity issue.

## Trigger

- `AuditGrantDrift` alert fires.
- Output of the grant-audit Job (`lenny-audit-grant-check`) reports unexpected privileges.

## Diagnosis

### Step 1 — Inspect current grants

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT grantee, privilege_type
   FROM information_schema.role_table_grants
   WHERE table_name IN ('audit_log','audit_log_ocsf','compliance_events')
   ORDER BY grantee, privilege_type;"
```

Compare output against the expected baseline in `charts/lenny/templates/postgres-grants.sql`.

### Step 2 — Audit the change

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT usename, application_name, query, query_start
   FROM pg_stat_activity
   WHERE query ILIKE '%grant%' ORDER BY query_start DESC LIMIT 20;"
```

If your Postgres has logical audit logging enabled (pgaudit), query its event stream as well.

### Step 3 — Who holds the extra grant?

Identify the role with the unexpected privilege. If it's a human account, investigate why the grant was applied (chart upgrade bug, manual ops, compromise).

## Remediation

### Step 1 — Revoke

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "REVOKE UPDATE, DELETE ON audit_log, audit_log_ocsf, compliance_events FROM <role>;"
```

Apply to every audit table listed in the baseline.

### Step 2 — Re-apply baseline

<!-- access: kubectl requires=cluster-access -->
```bash
helm template lenny lennylabs/lenny -f values.yaml --show-only templates/postgres-grants.sql \
  | kubectl exec -i deploy/lenny-gateway -- psql "$POSTGRES_DSN"
```

### Step 3 — Verify

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c "
  SELECT grantee, table_name, privilege_type
  FROM information_schema.role_table_grants
  WHERE table_name LIKE 'audit_%' AND privilege_type IN ('UPDATE','DELETE');"
```

- No UPDATE or DELETE privileges on audit tables for any non-`postgres` role.
- `AuditGrantDrift` alert clears within its evaluation window.

### Step 4 — Evidence preservation

Export the drift window and the grant-change audit trail for the security-incident record before closing.

## Escalation

Escalate to **security on-call immediately** — audit-grant drift is a potential tampering vector. The grant may have been used to delete or edit audit entries; a read of recent audit-table content for anomalies is mandatory.
