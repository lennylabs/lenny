---
layout: default
title: "token-store-unavailable"
parent: "Runbooks"
triggers:
  - alert: TokenStoreUnavailable
    severity: critical
components:
  - postgres
  - tokenService
symptoms:
  - "POST /v1/oauth/token returns 503 token_store_unavailable"
  - "session creation and credential leasing fail platform-wide"
  - "delegation minting fails"
tags:
  - oauth
  - tokens
  - postgres
  - fail-closed
requires:
  - admin-api
  - cluster-access
related:
  - postgres-failover
  - token-service-outage
---

# token-store-unavailable

The Postgres-backed token store (the `issued_tokens` table and associated write-path) is unavailable. `/v1/oauth/token` returns `503 token_store_unavailable`; session creation, delegation minting, and credential leasing are blocked platform-wide. This is **fail-closed by design** (Spec §13.3) — do not add a fallback path.

## Trigger

- `TokenStoreUnavailable` alert — `/v1/oauth/token` returning 503 past the configured sustain window.
- Session creation and delegation fail with `token_store_unavailable`.
- `lenny_oauth_token_5xx_total{error_type="token_store_unavailable"}` rising.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Primary reachability

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec -n lenny-system deploy/lenny-gateway -- \
  psql "$TOKEN_STORE_DSN" -c '\dt issued_tokens'
```

Timeouts or connection errors → follow [postgres-failover](postgres-failover.html).

### Step 2 — Blocking queries

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$TOKEN_STORE_DSN" -c \
  "SELECT pid, now() - query_start AS runtime, state, query
   FROM pg_stat_activity
   WHERE query LIKE '%issued_tokens%' AND state != 'idle'
   ORDER BY runtime DESC LIMIT 20;"
```

```bash
kubectl exec deploy/lenny-gateway -- psql "$TOKEN_STORE_DSN" -c \
  "SELECT * FROM pg_locks WHERE locktype = 'advisory';"
```

Long-running queries against `issued_tokens` or held advisory locks indicate a stuck writer.

### Step 3 — Replica lag

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_postgres_replication_lag_seconds&window=15m
```

Excessive lag degrades the token-validation Postgres-fallback path (reads) even when writes succeed.

### Step 4 — Audit-lock pressure

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=histogram_quantile(0.99, rate(lenny_audit_lock_acquire_seconds_bucket[5m]))
```

p99 elevated above the audit-lock alert threshold means write-before-issue is being starved on the audit side; see [audit-pipeline-degraded](audit-pipeline-degraded.html).

## Remediation

### Step 1 — Postgres primary down

Follow [postgres-failover](postgres-failover.html). Token issuance remains blocked fail-closed until the primary is restored. **This is correct behavior** — do not bypass.

### Step 2 — Blocking query

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$TOKEN_STORE_DSN" -c \
  "SELECT pg_cancel_backend(<pid>);"
```

Alert the on-call for root-cause investigation. If the blocking query is a data migration, coordinate with the release engineer before cancelling.

### Step 3 — Replica lag

Follow [postgres-failover](postgres-failover.html) Replica promotion section. The validation fallback path will recover automatically once lag returns to baseline.

### Step 4 — Audit-side back-pressure

Follow [audit-pipeline-degraded](audit-pipeline-degraded.html) Step 2 to drain the hot tenant(s).

### Step 5 — Verify recovery

<!-- access: lenny-ctl -->
```bash
lenny-ctl diagnose connectivity
```

- `/v1/oauth/token` returns 200 for a test tenant.
- `lenny_oauth_token_5xx_total{error_type="token_store_unavailable"}` rate flat.
- `lenny_postgres_replication_lag_seconds` back to baseline.

## Escalation

Escalate to:

- **Database operations** if Postgres is up but writes cannot complete — may be a stuck replica promotion or replication slot issue.
- **Security on-call** if 5xx correlated with elevated token-issuance error rate and tokens may have been issued out-of-band during recovery; audit reconciliation required.
- **Platform engineering** if the fallback design itself seems to be the blocker — do NOT introduce a fail-open bypass; platform engineering can provide tactical guidance without breaking the fail-closed invariant.

Cross-reference: Spec §13.3 (authoritative durability for revocation, write-before-issue ordering).
