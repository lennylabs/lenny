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

Tenant deletion follows a 6-phase lifecycle managed by a background controller:

```
active → disabling → deleting → deleted
```

| Phase | State | Actions |
|---|---|---|
| 1. Soft-disable | `disabling` | New session creation blocked; existing sessions continue |
| 2. Drain active sessions | `disabling` | Send graceful-shutdown signals; wait for in-flight sessions to complete or hit configurable timeout (default: 5 min), then force-terminate remaining pods |
| 3. Credential revocation | `deleting` | Revoke all active credential leases via `CredentialPoolStore.RevokeByTenant`; delete stored OAuth/refresh tokens via `TokenStore.DeleteByTenant`; flush tenant-scoped Redis cache entries |
| 4. Data deletion | `deleting` | Execute `DeleteByTenant` on every store in dependency order: sessions, artifacts, checkpoints, memories, audit logs, billing events (respecting `billingErasurePolicy` and legal holds) |
| 4a. KMS key scheduling | `deleting` | **T4 tenants only.** Schedule tenant-specific KMS keys for deletion after provider-minimum retention period (AWS: 7 days, GCP: 24h, Vault: immediate). Key is disabled first to block new encrypt/decrypt calls. |
| 5. CRD cleanup | `deleting` | Remove tenant-specific Kubernetes resources: `SandboxClaim` instances, pool annotations, NetworkPolicy labels |
| 6. Produce deletion receipt | `deleted` | Write cryptographic erasure receipt to the audit trail recording each phase's completion timestamp, any errors, which sinks were notified, and the final `deleted` state |

> **Legal holds block Phase 4.** Before entering Phase 4, the controller checks for active legal holds on any session or artifact belonging to the tenant. If holds exist, the controller pauses at Phase 3 and emits an `admin.tenant.deletion_blocked` audit event. An operator must explicitly clear the holds or exempt the deletion via `force-delete` before the controller proceeds.

The tenant record itself is retained after Phase 6 with `state = 'deleted'` and all mutable fields nulled. This tombstone prevents tenant ID reuse and allows `GET /v1/admin/tenants/{id}` to return a `410 Gone` response.

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
| `TenantDeletionOverdue` | Tenant in `disabling`/`deleting` longer than 80% of tier SLA (T3: 72h, T4: 4h) |
| `KmsKeyDeletionFailed` | Phase 4a KMS key deletion failed |

---

## Data Residency

Tenants and environments support an optional `dataResidencyRegion` field (e.g., `eu-west-1`, `us-east-1`) set via the admin API. When set, the platform enforces region constraints at three levels:

1. **Pod pool routing.** The gateway restricts pod allocation (including delegation targets) to pools whose `region` label matches. Delegations to non-matching regions are rejected with `REGION_CONSTRAINT_VIOLATED`. Every node in a delegation tree inherits the root session's region constraint.

2. **Storage routing.** The `StorageRouter` directs writes (Postgres, MinIO, Redis) to the region-local backend. Configure per-region endpoints in Helm values:

   ```yaml
   storage:
     regions:
       eu-west-1:
         postgresEndpoint: "postgres://pg-eu:5432/lenny"
         minioEndpoint: "https://minio-eu.example.com"
         redisEndpoint: "rediss://:pw@redis-eu:6380"
       us-east-1:
         postgresEndpoint: "postgres://pg-us:5432/lenny"
         minioEndpoint: "https://minio-us.example.com"
         redisEndpoint: "rediss://:pw@redis-us:6380"
   ```

   When `dataResidencyRegion` is set but the region is not present in `storage.regions`, the `StorageRouter` **fails closed** with `REGION_CONSTRAINT_UNRESOLVABLE`.

3. **Validation at session creation.** The gateway validates that at least one pool and one storage backend are available for the requested region before accepting a session.

### Data Residency Admission Webhook

The `lenny-data-residency-validator` `ValidatingAdmissionWebhook` (configured `failurePolicy: Fail`) enforces region integrity at the Kubernetes resource layer. It intercepts `CREATE` and `UPDATE` operations on tenant-scoped CRD resources with a `dataResidencyRegion` field and rejects resources specifying a region not declared in `storage.regions`. If the webhook is unavailable, admission is denied (fail-closed). The `DataResidencyWebhookUnavailable` alert fires when the webhook has been unreachable for more than 30 seconds.

### Per-Region KMS

Each region entry can declare its own KMS endpoint for envelope encryption, ensuring encryption keys remain within the data residency boundary.

### Inheritance

Sessions inherit `dataResidencyRegion` from their environment, which inherits from its tenant unless explicitly overridden. An environment must use the same `dataResidencyRegion` as its tenant or inherit the tenant's value; specifying a different region is rejected with `REGION_CONSTRAINT_VIOLATED`.

---

## Legal Holds

Sessions and artifacts support a `legal_hold` boolean flag. When set:

- Artifact retention policy is suspended -- artifacts are not deleted by the GC job regardless of TTL
- Checkpoint rotation is suspended for held sessions
- The legal hold reconciler prevents deletion of held resources during tenant deletion (Phase 4 is blocked)

### Managing Legal Holds

```bash
# Set a legal hold on a session
lenny-ctl admin legal-holds set --resource-type session --resource-id <session-id> --note "Investigation ref #1234"

# Clear a legal hold
lenny-ctl admin legal-holds clear --resource-type session --resource-id <session-id>

# List active legal holds
lenny-ctl admin legal-holds list --tenant-id <tenant-id>
```

Both `platform-admin` and `tenant-admin` roles can manage legal holds. `tenant-admin` callers are automatically scoped to their own tenant. All compliance operations (hold set/cleared, erasure requested/completed) are logged in the audit trail with the requesting admin's identity.

---

## Environments

Environments are optional project contexts within a tenant that provide RBAC scoping and resource selection. Configure via the admin API with:

- **Runtime selectors:** Label-based selectors that restrict which runtimes are available within the environment
- **Connector selectors:** Label-based selectors that restrict which connectors are available
- **Member roles:** Per-environment RBAC controlling which users can create sessions within the environment
- **Data residency inheritance:** Environments inherit `dataResidencyRegion` from their tenant unless explicitly overridden to the same or stricter region

Environments are not required for basic operation -- single-tenant and simple multi-tenant deployments can operate without them.
