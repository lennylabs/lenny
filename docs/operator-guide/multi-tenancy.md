---
layout: default
title: Multi-Tenancy
parent: "Operator Guide"
nav_order: 9
---

# Multi-Tenancy

This page covers the tenant model, PostgreSQL Row-Level Security, PgBouncer sentinel defense, single-tenant vs. multi-tenant modes, per-tenant quotas, resource scoping, and integration testing.

---

## Tenant Model

### Overview

Lenny uses a single-database, multi-tenant architecture where tenant isolation is enforced at the database level via PostgreSQL Row-Level Security (RLS).

Key properties:

- **`tenant_id` is carried on all tenant-scoped records** -- sessions, tasks, tokens, quota counters, credential pools, audit events, billing events, and more
- **RLS policies filter every query** -- no application-level bug can bypass isolation
- **Single-tenant mode is the default** -- uses the built-in `default` tenant with no additional configuration
- **Multi-tenant mode** is enabled by configuring OIDC claims that carry tenant identity

### Single-Tenant Mode

For single-tenant deployments, `tenant_id` defaults to `default`:

- The field is always present internally
- RLS policies and queries always filter by it
- Single-tenant deployers never need to set or think about it
- The gateway automatically applies the default tenant

No additional configuration is required.

### Multi-Tenant Mode

Multi-tenant mode is activated by:

1. Configuring the OIDC claim that carries tenant identity:
   ```yaml
   auth:
     tenantIdClaim: "tenant_id"    # OIDC claim name (default)
   ```
2. Creating tenant records via the admin API or bootstrap seed
3. Setting `tenant_id` explicitly in API calls (if not using OIDC)

---

## PostgreSQL Row-Level Security

### How RLS Works

Every tenant-scoped table has an RLS policy that filters rows using:

```sql
current_setting('app.current_tenant', false)
```

The `false` parameter causes a **hard error** if the setting is unset, preventing silent fallback to an empty string.

### Transaction Wrapping

Every database call is wrapped in an explicit transaction that begins with:

```sql
SET LOCAL app.current_tenant = '<tenant_id>';
```

`SET LOCAL` is transaction-scoped: the value is automatically cleared on `COMMIT` or `ROLLBACK`. This is essential under PgBouncer transaction-mode pooling, where bare `SET` could leak tenant context to subsequent requests sharing the same pooled connection.

### Defense-in-Depth Layers

| Layer | Mechanism | Protects Against |
|---|---|---|
| RLS policy | `current_setting('app.current_tenant', false)` | Missing WHERE clauses in application code |
| `SET LOCAL` | Transaction-scoped tenant context | Tenant context leaking across pooled connections |
| PgBouncer sentinel | `connect_query` sets `__unset__` on checkout | Queries reaching RLS without prior `SET LOCAL` |
| Application filtering | `WHERE tenant_id = ?` in queries | Additional defense-in-depth |
| Integration tests | Verify cross-tenant isolation at startup | Regression during development |

---

## PgBouncer Sentinel Defense

### Self-Managed PgBouncer

PgBouncer's `connect_query` is configured to set a sentinel value on every fresh connection checkout:

```
SET app.current_tenant = '__unset__'
```

Any query that reaches RLS evaluation with `__unset__` is rejected by the policy. This prevents bugs where a code path accidentally omits the `SET LOCAL` call.

### Cloud-Managed Pooler Defense

Cloud-managed connection proxies (RDS Proxy, Cloud SQL Auth Proxy, Azure PgBouncer) typically do not support `connect_query`. For these deployments:

1. Set `postgres.connectionPooler: external` in Helm values
2. The Lenny schema migration creates the `lenny_tenant_guard` trigger
3. The trigger validates `current_setting('app.current_tenant', true)` on every tenant-scoped statement
4. The gateway **refuses to start** if `connectionPooler: external` is set but the trigger is absent

```yaml
postgres:
  connectionPooler: external    # Triggers lenny_tenant_guard creation
```

### Trigger Validation Logic

The `lenny_tenant_guard` trigger:

- **Rejects:** NULL, empty string, `'__unset__'`
- **Allows:** `'__all__'` (platform-admin sentinel) and concrete tenant IDs matching `^[a-zA-Z0-9_-]{1,128}$`
- **Raises exception:** `ERRCODE 'P0001'` for invalid values

---

## Platform-Admin Cross-Tenant Access

### The `__all__` Sentinel

Platform-admin code paths that require cross-tenant reads use:

```sql
SET LOCAL app.current_tenant = '__all__';
```

Every RLS policy includes an additional clause:

```sql
OR current_setting('app.current_tenant', false) = '__all__'
```

This disables row filtering for the transaction. The `__all__` sentinel is restricted to the `platform-admin` code path: the gateway sets it only after RBAC verification confirms the caller holds the `platform-admin` role.

### Audit Trail

Every code path that sets `app.current_tenant = '__all__'` emits a `cross_tenant_read` audit event recording the caller identity, endpoint, and query category.

---

## Resource Tenant-Scoping Classification

### Tenant-Scoped Resources

These resources carry `tenant_id` and are protected by RLS:

| Resource | Isolation Mechanism |
|---|---|
| Sessions, tasks, tokens, memories | `tenant_id` column + RLS |
| Quota counters, credential pools | `tenant_id` column + RLS |
| Audit/billing events, eval results | `tenant_id` column + RLS |
| Delegation policies | `tenant_id` column + RLS |
| Connectors | `tenant_id` column + RLS |
| Environments | `tenant_id` column + RLS |
| Experiments | `tenant_id` column + RLS |
| User role mappings | `tenant_id` column + RLS |
| Custom role definitions | `tenant_id` column + RLS |
| `session_eviction_state` | `tenant_id` column + RLS |
| `session_dlq_archive` | `tenant_id` column + RLS |

### Platform-Global Resources

These resources have no `tenant_id` column and are not subject to RLS:

| Resource | Access Control |
|---|---|
| Runtimes | `runtime_tenant_access` join table (application-layer) |
| Pools | `pool_tenant_access` join table (application-layer) |
| External adapters | `platform-admin` only |
| `agent_pod_state` | Platform-global; cross-references mediated through RLS-protected queries |

### Runtime and Pool Access Control

Runtimes and pools are platform-global but visibility is controlled per-tenant:

```bash
# Grant a tenant access to a runtime
lenny-ctl admin runtimes grant-access --runtime claude-worker --tenant acme

# List tenants with access
lenny-ctl admin runtimes list-access --runtime claude-worker

# Revoke access
lenny-ctl admin runtimes revoke-access --runtime claude-worker --tenant acme
```

A `tenant-admin`'s calls to the admin API are filtered to the rows in the access tables for their tenant. `platform-admin` calls are unfiltered.

---

## Per-Tenant Quotas

### Hierarchical Quota Model

Quotas are enforced hierarchically: **global to tenant to user**. A user quota cannot exceed its tenant's quota.

| Quota Type | Scope |
|---|---|
| Token limits (LLM tokens) | Per-request, per-session, per-user/window, per-tenant/window |
| Runtime limits (wall clock) | Per-session |
| Concurrent sessions | Per-tenant, per-user |
| Storage quota | Per-tenant (total artifact storage) |
| Delegation budget | Per-session (depth, fan-out, total children) |

### Quota Configuration

```yaml
tenants:
  - name: acme
    quotas:
      maxConcurrentSessions: 100
      maxTokensPerHour: 1000000
      storageQuotaBytes: 10737418240    # 10 GiB
```

### Quota Enforcement

- **Real-time enforcement** via Redis counters (fast path)
- **Durable checkpoint** to Postgres at configurable intervals (`quotaSyncIntervalSeconds`)
- **Soft warnings** at 80% utilization (emitted as billing events)
- **Hard limits** at 100% -- new sessions or requests rejected with `QUOTA_EXCEEDED`

### Storage Quota Enforcement

MinIO has no built-in per-prefix quota. The gateway enforces storage quotas via:

1. **Pre-upload atomic reservation** -- Redis Lua script checks and reserves bytes atomically
2. **Post-upload reconciliation** -- adjusts counter to match confirmed object size
3. **GC-triggered decrement** -- decrements counter when artifacts are deleted

The `StorageQuotaHigh` alert fires when a tenant exceeds 80% of their storage quota.

---

## Redis Key Isolation

All Redis-backed roles use the `t:{tenant_id}:` key prefix convention:

| Key Pattern | Purpose |
|---|---|
| `t:{tenant_id}:quota:*` | Quota counters |
| `t:{tenant_id}:session:*` | Session routing cache |
| `t:{tenant_id}:billing:stream` | Billing event Redis stream |
| `t:{tenant_id}:rate_limit:*` | Rate limit counters |

This ensures tenant isolation at the key-naming level.

---

## MinIO Path Isolation

All MinIO object paths are prefixed with `/{tenant_id}/`:

```
/{tenant_id}/workspace/{session_id}/snapshot.tar
/{tenant_id}/checkpoint/{session_id}/gen-{n}.tar
/{tenant_id}/eviction/{session_id}/context
```

The `ArtifactStore` interface validates that the supplied `tenant_id` matches the path prefix before issuing any S3 call. Paths that fail prefix validation are rejected without reaching MinIO.

---

## Integration Testing

### Required Tests

The following integration tests must verify tenant isolation:

**`TestRLSTenantGuardMissingSetLocal`:**
1. Connect to a test Postgres instance with `lenny_tenant_guard` trigger deployed
2. Open a transaction without issuing `SET LOCAL app.current_tenant`
3. Execute a `SELECT` against a tenant-scoped table
4. Assert that the query raises an exception

**Cross-tenant read test:**
1. Set `SET LOCAL app.current_tenant = 'tenant-a'`
2. Query rows seeded under `'tenant-b'`
3. Assert zero rows returned

**`TestRLSPlatformAdminAllSentinel`:**
1. Verify `SET LOCAL app.current_tenant = '__all__'` returns rows from multiple tenants
2. Verify a non-`platform-admin` caller cannot reach the `__all__` code path
3. Verify the `__all__` sentinel is properly handled by `lenny_tenant_guard`

### Running at Startup

An integration test verifies tenant isolation at startup by confirming:
- A query without `SET LOCAL` is rejected
- Cross-tenant reads return zero rows

---

## Tenant Lifecycle

### Tenant Deletion

Tenant deletion follows a multi-phase lifecycle:

```
active → disabling → deleting → deleted
```

1. **Disabling:** All new sessions rejected; existing sessions allowed to drain
2. **Deleting:** All data erasure (Postgres rows, MinIO objects, Redis keys, KMS keys)
3. **Deleted:** Terminal state; tenant record retained for audit

```bash
# Initiate tenant deletion
lenny-ctl admin tenants delete acme

# Monitor progress
lenny-ctl admin tenants get acme

# Force-delete with legal hold override
lenny-ctl admin tenants force-delete acme --justification "Legal hold expired"
```

### Alerts

| Alert | Condition |
|---|---|
| `TenantDeletionOverdue` | Tenant in `disabling`/`deleting` longer than 80% of tier SLA |
| `KmsKeyDeletionFailed` | Phase 4a KMS key deletion failed |
