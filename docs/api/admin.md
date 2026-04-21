---
layout: default
title: Admin API
parent: "API Reference"
nav_order: 5
---

# Admin API Reference

The Admin API provides operator-only management endpoints for configuring runtimes, pools, tenants, credentials, delegation policies, environments, experiments, external adapters, and operational controls. All admin endpoints are under the `/v1/admin/` path prefix.

These endpoints are also accessible via the `lenny-ctl` CLI tool. See the [Operator Guide](../operator-guide/index.html) for CLI usage.

---

## Authentication

Admin API endpoints authenticate via a static admin token configured at deployment time:

```
Authorization: Bearer <admin-token>
```

The admin token is set via the `LENNY_API_TOKEN` environment variable or a Kubernetes Secret. Admin API endpoints have **separate (higher) rate-limit windows** from client-facing endpoints.

### Required roles

Most admin endpoints require `platform-admin` or `tenant-admin` role. Each endpoint below specifies its required role. `tenant-admin` callers are scoped to their own tenant -- they only see and modify resources within their tenant's access grants.

---

## Common patterns

### ETag-based optimistic concurrency

Every admin resource in Postgres carries an integer `version` column (starts at 1, incremented on every successful write). The ETag value is the quoted decimal version: `"3"`.

**Rules:**

| Operation | ETag behavior |
|:----------|:-------------|
| **GET (single item)** | Response includes `ETag` header with current version |
| **GET (list)** | Each item in the response body includes `"etag": "3"` |
| **PUT** | `If-Match` header **required**. Missing: `428 ETAG_REQUIRED`. Mismatch: `412 ETAG_MISMATCH` with `details.currentEtag`. |
| **POST** | `If-Match` ignored (no prior version) |
| **DELETE** | `If-Match` optional. When provided, validated. When omitted, last-writer-wins. |

**Retry pattern after `412 ETAG_MISMATCH`:**
1. Use `details.currentEtag` from the error response (if present), or
2. Re-GET the specific resource to obtain the current ETag and body
3. Merge changes and retry the PUT

### Dry-run support

Most admin `POST` and `PUT` endpoints accept `?dryRun=true`:

- Full validation is performed (schema, field constraints, referential integrity, policy checks, quota evaluation)
- **No persistence** -- no side effects, no CRD reconciliation, no webhook dispatch
- **No outbound network calls** -- all checks use locally cached state
- Response body is identical to a real success, plus `X-Dry-Run: true` header
- Audit events are **not** emitted (exception: `POST /v1/admin/bootstrap` emits a dry-run audit event)
- When combined with `If-Match`, ETag validation is performed as normal

**Not supported on:** pool/session action endpoints (`drain`, `force-terminate`, `warm-count`), `DELETE` endpoints.

**Supported on circuit-breaker actions:** `POST /v1/admin/circuit-breakers/{name}/open` and `POST /v1/admin/circuit-breakers/{name}/close` accept `?dryRun=true`. The response body is a reduced simulation object (a subset of the real-call response fields — audit-like fields such as `opened_at`, `opened_by_sub`, `opened_by_tenant_id` are omitted since no state mutation occurs) plus a top-level `simulation` object with `currentState`, `predictedState`, and `wouldChangeState`. See the circuit-breakers section below for the exact field enumeration per endpoint.

### Deletion semantics

Deleting a resource that is referenced by active dependents is **blocked** (not cascaded). The gateway returns `409 RESOURCE_HAS_DEPENDENTS`:

```json
{
  "error": {
    "code": "RESOURCE_HAS_DEPENDENTS",
    "category": "PERMANENT",
    "message": "Cannot delete runtime: referenced by active pools and sessions.",
    "retryable": false,
    "details": {
      "dependents": [
        {
          "type": "pool",
          "name": "default-pool",
          "count": 1,
          "ids": ["default-pool"]
        },
        {
          "type": "session",
          "state": "running",
          "count": 3,
          "ids": ["sess-abc", "sess-def", "sess-ghi"]
        }
      ]
    }
  }
}
```

The `ids` array includes up to 20 individual resource IDs per dependent type. When the total count exceeds 20, `truncated: true` is set on that entry.

**Deletion rules by resource type:**

| Resource | Blocked when |
|:---------|:-------------|
| Runtime | Referenced by any active pool or non-terminal session |
| Pool | Any sessions are running or suspended. Drain first. |
| Delegation Policy | Referenced by any runtime definition or active delegation lease |
| Connector | Referenced by any environment or runtime |
| Credential Pool | Any active credential leases exist |
| Tenant | Any non-terminal sessions, pools, or credential pools under the tenant |
| Environment | Any active sessions within the environment |
| Experiment | Status is `active` or `paused` with non-terminal sessions. Conclude first. |
| External Adapter | Status is `active`. Set to `inactive` first. |

---

## Runtimes

Runtime definitions are **platform-global** (not tenant-scoped). Tenant visibility is controlled via access grants.

### POST /v1/admin/runtimes
{: .d-inline-block }
platform-admin
{: .label .label-red }

Create a new runtime definition.

**Request body:**

```json
{
  "name": "claude-worker",
  "type": "agent",
  "image": "ghcr.io/myorg/claude-worker:v1.2.0@sha256:abc...",
  "executionMode": "session",
  "capabilities": {
    "delegation": true,
    "elicitation": true,
    "checkpoint": true,
    "midSessionUpload": false,
    "preConnect": false
  },
  "agentInterface": {
    "injection": { "supported": true }
  },
  "labels": {
    "provider": "anthropic",
    "tier": "standard"
  },
  "resourceClass": "medium"
}
```

**Response:** `201 Created` with the created runtime object and `ETag` header.

**Error codes:** `VALIDATION_ERROR`, `RESOURCE_ALREADY_EXISTS`, `IMAGE_RESOLUTION_FAILED`.

**Dry-run:** Supported.

### GET /v1/admin/runtimes
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

List runtime definitions. `tenant-admin` sees only runtimes in their tenant's access grants. `platform-admin` sees all.

**Query parameters:** Standard pagination (`cursor`, `limit`, `sort`), plus `?labels=key:value` for label filtering.

**Response:** Paginated envelope with runtime objects.

### GET /v1/admin/runtimes/{name}
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Get a specific runtime definition. Returns `404` if not in caller's access grants.

### PUT /v1/admin/runtimes/{name}
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Update a runtime definition. Requires `If-Match` header. `tenant-admin` restricted to runtimes in their tenant's access grants.

**Dry-run:** Supported.

### DELETE /v1/admin/runtimes/{name}
{: .d-inline-block }
platform-admin
{: .label .label-red }

Delete a runtime definition. Blocked if referenced by active pools or non-terminal sessions.

### POST /v1/admin/runtimes/{name}/tenant-access
{: .d-inline-block }
platform-admin
{: .label .label-red }

Grant a tenant access to a runtime.

**Request body:**

```json
{
  "tenantId": "t_01J5K9..."
}
```

**Response:** `200 OK`. Idempotent -- returns `200` if the grant already exists.

### GET /v1/admin/runtimes/{name}/tenant-access
{: .d-inline-block }
platform-admin
{: .label .label-red }

List tenants with access to a runtime.

**Response:**

```json
[
  {
    "tenantId": "t_01J5K9...",
    "tenantName": "Acme Corp",
    "grantedAt": "2026-04-01T00:00:00Z",
    "grantedBy": "admin@acme.com"
  }
]
```

### DELETE /v1/admin/runtimes/{name}/tenant-access/{tenantId}
{: .d-inline-block }
platform-admin
{: .label .label-red }

Revoke a tenant's access to a runtime. Returns `404` if the grant does not exist.

---

## Pools

Pool configurations are **platform-global**. Tenant visibility is controlled via access grants.

### POST /v1/admin/pools
{: .d-inline-block }
platform-admin
{: .label .label-red }

Create a pool configuration.

**Request body:**

```json
{
  "name": "default-pool",
  "runtime": "claude-worker",
  "executionMode": "session",
  "minWarm": 2,
  "maxWarm": 10,
  "maxPods": 50,
  "resourceClass": "medium",
  "isolationProfile": "sandboxed",
  "scalePolicy": {
    "scaleUpThreshold": 0.8,
    "scaleDownThreshold": 0.3,
    "cooldownSeconds": 120
  }
}
```

**Response:** `201 Created` with pool object and `ETag` header.

**Dry-run:** Supported.

### GET /v1/admin/pools
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

List pool configurations. `tenant-admin` sees only pools in their tenant's access grants.

### GET /v1/admin/pools/{name}
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Get a specific pool configuration. Returns `404` if not in caller's access grants.

### PUT /v1/admin/pools/{name}
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Update a pool configuration. Requires `If-Match`.

**Dry-run:** Supported.

### DELETE /v1/admin/pools/{name}
{: .d-inline-block }
platform-admin
{: .label .label-red }

Delete a pool. Blocked if any sessions are running or suspended. Drain first.

### POST /v1/admin/pools/{name}/drain
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Drain a pool -- stop assigning new sessions and wait for in-flight sessions to complete.

**Response:**

```json
{
  "status": "draining",
  "activeSessions": 5,
  "estimatedDrainSeconds": 300
}
```

While draining, new session requests for this pool return `503 POOL_DRAINING` with `Retry-After` header.

### PUT /v1/admin/pools/{name}/warm-count
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Adjust `minWarm` and `maxWarm` at runtime.

**Request body:**

```json
{
  "minWarm": 5,
  "maxWarm": 20
}
```

### GET /v1/admin/pools/{name}/sync-status
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Report CRD reconciliation state.

**Response:**

```json
{
  "postgresGeneration": 42,
  "crdGeneration": 41,
  "lastReconciledAt": "2026-04-09T10:00:00Z",
  "lagSeconds": 3,
  "inSync": false
}
```

### PUT /v1/admin/pools/{name}/circuit-breaker
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Override the SDK-warm circuit-breaker state.

**Request body:**

```json
{
  "sdkWarm": {
    "circuitBreakerOverride": "enabled"
  }
}
```

Values: `enabled` (force SDK-warm on), `disabled` (force off), `auto` (restore automatic control).

### Pool upgrade lifecycle

| Endpoint | Method | Description |
|:---------|:-------|:------------|
| `/v1/admin/pools/{name}/upgrade/start` | POST | Begin rolling image upgrade. Body: `{"newImage": "<digest>"}` |
| `/v1/admin/pools/{name}/upgrade/proceed` | POST | Advance to next upgrade phase |
| `/v1/admin/pools/{name}/upgrade/pause` | POST | Pause the upgrade state machine |
| `/v1/admin/pools/{name}/upgrade/resume` | POST | Resume a paused upgrade |
| `/v1/admin/pools/{name}/upgrade/rollback` | POST | Rollback in-progress upgrade. Body: optional `{"restoreOldPool": true}` |
| `/v1/admin/pools/{name}/upgrade-status` | GET | Show upgrade state and progress |

### Pool tenant access

| Endpoint | Method | Description |
|:---------|:-------|:------------|
| `/v1/admin/pools/{name}/tenant-access` | POST | Grant tenant access. Body: `{"tenantId": "<uuid>"}`. Idempotent. |
| `/v1/admin/pools/{name}/tenant-access` | GET | List tenants with access |
| `/v1/admin/pools/{name}/tenant-access/{tenantId}` | DELETE | Revoke tenant access |

### DELETE /v1/admin/pools/{name}/bootstrap-override
{: .d-inline-block }
platform-admin
{: .label .label-red }

Remove the bootstrap `minWarm` override and switch to formula-driven scaling.

---

## Tenants

Tenants use `{id}` (opaque UUID) as the path identifier because tenant names are mutable display labels.

### POST /v1/admin/tenants
{: .d-inline-block }
platform-admin
{: .label .label-red }

Create a tenant. Also creates a per-tenant Postgres billing sequence.

**Request body:**

```json
{
  "name": "Acme Corp",
  "quota": {
    "maxConcurrentSessions": 50,
    "maxMonthlyTokens": 100000000,
    "maxStorageBytes": 10737418240
  },
  "complianceProfile": "standard"
}
```

### GET /v1/admin/tenants
{: .d-inline-block }
platform-admin
{: .label .label-red }

List all tenants.

### GET /v1/admin/tenants/{id}
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Get a specific tenant. `tenant-admin` can only view their own tenant.

### PUT /v1/admin/tenants/{id}
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Update a tenant. Requires `If-Match`. Quotas are embedded in the tenant record.

`complianceProfile` is subject to a one-way ratchet ordered `none < soc2 < fedramp < hipaa` -- a request that lowers the value is rejected with `422 COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`. For legitimate wind-down, use `POST /v1/admin/tenants/{id}/compliance-profile/decommission` (below). `workspaceTier` is similarly ratcheted (stricter only).

### DELETE /v1/admin/tenants/{id}
{: .d-inline-block }
platform-admin
{: .label .label-red }

Delete a tenant. Blocked if any non-terminal sessions, pools, or credential pools exist.

### Tenant RBAC configuration

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `PUT /v1/admin/tenants/{id}/rbac-config` | PUT | platform-admin, tenant-admin | Set tenant RBAC configuration |
| `GET /v1/admin/tenants/{id}/rbac-config` | GET | platform-admin, tenant-admin | Get tenant RBAC configuration |
| `GET /v1/admin/tenants/{id}/access-report` | GET | platform-admin, tenant-admin | Cross-environment access matrix |

### Tenant user management

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `GET /v1/admin/tenants/{id}/users` | GET | platform-admin, tenant-admin | List users with role assignments |
| `PUT /v1/admin/tenants/{id}/users/{user_id}/role` | PUT | platform-admin, tenant-admin | Assign or update user role. Body: `{"role": "<role-name>"}` |
| `DELETE /v1/admin/tenants/{id}/users/{user_id}/role` | DELETE | platform-admin, tenant-admin | Remove platform-managed role assignment |

### Custom roles

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/tenants/{id}/roles` | POST | platform-admin, tenant-admin | Create a custom role. Body: `{"name": "...", "permissions": [...]}` |
| `GET /v1/admin/tenants/{id}/roles` | GET | platform-admin, tenant-admin | List custom roles |
| `GET /v1/admin/tenants/{id}/roles/{name}` | GET | platform-admin, tenant-admin | Get a specific custom role |
| `PUT /v1/admin/tenants/{id}/roles/{name}` | PUT | platform-admin, tenant-admin | Update custom role. Requires `If-Match`. |
| `DELETE /v1/admin/tenants/{id}/roles/{name}` | DELETE | platform-admin, tenant-admin | Delete custom role. Blocked if any users are assigned it. |

### Other tenant operations

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/tenants/{id}/rotate-erasure-salt` | POST | platform-admin | Rotate billing pseudonymization salt |
| `POST /v1/admin/tenants/{id}/force-delete` | POST | platform-admin | Force-delete a tenant with active legal holds. Body: `{"justification": "..."}` |
| `POST /v1/admin/tenants/{id}/compliance-profile/decommission` | POST | platform-admin | Attested wind-down of a regulated `complianceProfile`. Sole legitimate path to lower the value (the generic `PUT` surface rejects downgrades with `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`). Body: `{"previousProfile": "...", "targetProfile": "...", "acknowledgeDataRemediation": true, "justification": "...", "remediationAttestations": ["..."]}`. Emits `compliance.profile_decommissioned` critical audit event and raises `CompliancePostureDecommissioned` warning alert. |

---

## Credential pools

Credential pools are **tenant-scoped**.

### CRUD endpoints

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/credential-pools` | POST | platform-admin, tenant-admin | Create a credential pool |
| `GET /v1/admin/credential-pools` | GET | platform-admin, tenant-admin | List credential pools (tenant-scoped) |
| `GET /v1/admin/credential-pools/{name}` | GET | platform-admin, tenant-admin | Get a specific credential pool |
| `PUT /v1/admin/credential-pools/{name}` | PUT | platform-admin, tenant-admin | Update. Requires `If-Match`. |
| `DELETE /v1/admin/credential-pools/{name}` | DELETE | platform-admin, tenant-admin | Delete. Blocked if active leases exist. |

### Credential management

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/credential-pools/{name}/credentials` | POST | platform-admin, tenant-admin | Add a credential to a pool |
| `POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke` | POST | platform-admin, tenant-admin | Emergency revocation of a single credential. Immediately invalidates all active leases. |
| `POST /v1/admin/credential-pools/{name}/credentials/{credId}/re-enable` | POST | platform-admin | Re-enable a previously revoked credential |
| `POST /v1/admin/credential-pools/{name}/revoke` | POST | platform-admin, tenant-admin | Emergency revocation of all credentials in pool |

---

## Delegation policies

### CRUD endpoints

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/delegation-policies` | POST | platform-admin, tenant-admin | Create a delegation policy |
| `GET /v1/admin/delegation-policies` | GET | platform-admin, tenant-admin | List all delegation policies |
| `GET /v1/admin/delegation-policies/{name}` | GET | platform-admin, tenant-admin | Get a specific policy |
| `PUT /v1/admin/delegation-policies/{name}` | PUT | platform-admin, tenant-admin | Update. Requires `If-Match`. |
| `DELETE /v1/admin/delegation-policies/{name}` | DELETE | platform-admin, tenant-admin | Delete. Blocked if referenced by runtimes or active leases. |

**Dry-run:** Supported on POST and PUT.

---

## Connectors

### CRUD endpoints

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/connectors` | POST | platform-admin, tenant-admin | Create a connector definition |
| `GET /v1/admin/connectors` | GET | platform-admin, tenant-admin | List all connectors |
| `GET /v1/admin/connectors/{name}` | GET | platform-admin, tenant-admin | Get a specific connector |
| `PUT /v1/admin/connectors/{name}` | PUT | platform-admin, tenant-admin | Update. Requires `If-Match`. |
| `DELETE /v1/admin/connectors/{name}` | DELETE | platform-admin, tenant-admin | Delete. Blocked if referenced by environments or runtimes. |

**Dry-run:** Supported on POST and PUT (validates URL format, scheme allowlist -- no outbound calls).

### POST /v1/admin/connectors/{name}/test
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Live connectivity test. Rate-limited to 10 requests per connector per minute.

**Response:**

```json
{
  "connector": "github-mcp",
  "stages": [
    { "name": "dns_resolution", "status": "passed", "latencyMs": 12 },
    { "name": "tls_handshake", "status": "passed", "latencyMs": 45 },
    { "name": "mcp_initialize", "status": "passed", "latencyMs": 230 },
    { "name": "auth_validation", "status": "passed", "latencyMs": 15 }
  ],
  "overall": "passed"
}
```

---

## Environments

### CRUD endpoints

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/environments` | POST | platform-admin, tenant-admin | Create an environment |
| `GET /v1/admin/environments` | GET | platform-admin, tenant-admin | List all environments |
| `GET /v1/admin/environments/{name}` | GET | platform-admin, tenant-admin | Get a specific environment |
| `PUT /v1/admin/environments/{name}` | PUT | platform-admin, tenant-admin | Update. Requires `If-Match`. |
| `DELETE /v1/admin/environments/{name}` | DELETE | platform-admin, tenant-admin | Delete. Blocked if active sessions exist. |

**Dry-run:** Supported. Response includes a `preview` object:

```json
{
  "resource": { "...computed environment..." },
  "preview": {
    "matchedRuntimes": ["claude-sonnet", "gpt-4-turbo"],
    "matchedConnectors": ["github-mcp", "jira-mcp"],
    "unmatchedSelectorTerms": []
  }
}
```

### Introspection endpoints

| Endpoint | Method | Description |
|:---------|:-------|:------------|
| `GET /v1/admin/environments/{name}/usage` | GET | Environment billing rollup |
| `GET /v1/admin/environments/{name}/access-report` | GET | Resolved member list with group expansion |
| `GET /v1/admin/environments/{name}/runtime-exposure` | GET | Runtimes and connectors in scope |

---

## Experiments

Experiments configure variant pools for runtime version rollouts. Lenny provides infrastructure primitives (pods organised into variant pools, deterministic sticky routing, variant context in the adapter manifest) and a basic built-in assigner for simple splits. For anything beyond simple rollouts, integrate an external experimentation platform (LaunchDarkly, Statsig, Unleash) via OpenFeature — assignment decisions then live in the external platform.

Regardless of who decides the assignment, the gateway delivers `experimentContext` (`experimentId`, `variantId`, `inherited`) in the adapter manifest so runtimes can tag traces with variant metadata for filtering and grouping in their chosen eval platform. When scores are also stored via the `/eval` endpoint, the gateway auto-populates variant attribution on those stored records.

### CRUD endpoints

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/experiments` | POST | platform-admin, tenant-admin | Create an experiment |
| `GET /v1/admin/experiments` | GET | platform-admin, tenant-admin | List all experiments |
| `GET /v1/admin/experiments/{name}` | GET | platform-admin, tenant-admin | Get a specific experiment |
| `PUT /v1/admin/experiments/{name}` | PUT | platform-admin, tenant-admin | Update. Requires `If-Match`. |
| `PATCH /v1/admin/experiments/{name}` | PATCH | platform-admin, tenant-admin | Partial update (JSON Merge Patch). For status transitions (`active`, `paused`, `concluded`). Requires `If-Match`. |
| `DELETE /v1/admin/experiments/{name}` | DELETE | platform-admin, tenant-admin | Delete. Blocked if `active`/`paused` with non-terminal sessions. |

**Dry-run:** Supported on POST and PUT (validates definition, variant weights, runtime references -- no capacity check).

### GET /v1/admin/experiments/{name}/results
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Get experiment results by variant. Returns aggregated scores from the **built-in `/eval` endpoint** only — scores submitted via runtime-native eval platforms (LangSmith, Braintrust, etc.) are not reflected here and should be queried from those platforms directly.

Returns a single aggregated object (not paginated).

**Response:**

```json
{
  "experiment": "model-comparison-v2",
  "status": "active",
  "variants": [
    {
      "id": "variant-a",
      "runtime": "claude-worker-v2",
      "sessions": 150,
      "evalScores": {
        "mean": 0.82,
        "median": 0.85,
        "p95": 0.95
      }
    },
    {
      "id": "variant-b",
      "runtime": "claude-worker-v1",
      "sessions": 148,
      "evalScores": {
        "mean": 0.78,
        "median": 0.80,
        "p95": 0.92
      }
    }
  ],
  "controlGroup": {
    "sessions": 702,
    "evalScores": {
      "mean": 0.79,
      "median": 0.81,
      "p95": 0.93
    }
  }
}
```

---

## External adapters

### CRUD endpoints

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/external-adapters` | POST | platform-admin | Register an external protocol adapter. Created in `status: pending_validation`. |
| `GET /v1/admin/external-adapters` | GET | platform-admin | List all external adapters |
| `GET /v1/admin/external-adapters/{name}` | GET | platform-admin | Get a specific adapter |
| `PUT /v1/admin/external-adapters/{name}` | PUT | platform-admin | Update. Requires `If-Match`. |
| `DELETE /v1/admin/external-adapters/{name}` | DELETE | platform-admin | Delete. Blocked if `status: active`. Set to `inactive` first. |

### POST /v1/admin/external-adapters/{name}/validate
{: .d-inline-block }
platform-admin
{: .label .label-red }

Run the `RegisterAdapterUnderTest` compliance suite against the adapter. Transitions status from `pending_validation` to `active` on success, or `validation_failed` (with per-test details) on failure.

Adapters must pass this validation before receiving any traffic.

---

## Circuit breakers

Two classes of circuit breaker are exposed via the admin API:

1. **Operator-managed (platform-wide) breakers** — Redis-backed, declared and toggled by platform admins for incident response. Endpoints in the table below; see [Section 11.6](../spec/11_policy-and-controls.html#116-circuit-breakers).
2. **SDK-warm pool breakers** — managed via `PUT /v1/admin/pools/{name}/circuit-breaker` above; see [Section 6.1](../spec/06_warm-pod-model.html#61-what-a-pre-warmed-pod-looks-like).

### Operator-managed circuit breakers

| Endpoint | Method | Role | Scope (`x-lenny-scope`) | Description |
|:---------|:-------|:-----|:-----|:------------|
| `GET /v1/admin/circuit-breakers` | GET | platform-admin | `tools:circuit_breaker:read` | List all circuit breakers and their current state |
| `GET /v1/admin/circuit-breakers/{name}` | GET | platform-admin | `tools:circuit_breaker:read` | Get state for a single circuit breaker |
| `POST /v1/admin/circuit-breakers/{name}/open` | POST | platform-admin | `tools:circuit_breaker:write` | Open (activate) a circuit breaker (see request body below) |
| `POST /v1/admin/circuit-breakers/{name}/close` | POST | platform-admin | `tools:circuit_breaker:write` | Close (deactivate) a circuit breaker; body is empty |

### POST /v1/admin/circuit-breakers/{name}/open
{: .d-inline-block }
platform-admin
{: .label .label-red }

Open (activate) an operator-managed circuit breaker. The admission path rejects matching requests with `CIRCUIT_BREAKER_OPEN` until the breaker is closed.

Request body:

```json
{
  "reason": "runtime degraded — upstream LLM API returning 5xx",
  "limit_tier": "runtime",
  "scope": { "runtime": "runtime_python_ml" }
}
```

- `reason` (string, required) — free-text justification recorded in audit and returned in the `CIRCUIT_BREAKER_OPEN` error body of every rejected admission.
- `limit_tier` (string, required) — one of `runtime` \| `pool` \| `connector` \| `operation_type`. Shares its closed vocabulary with the `lenny_circuit_breaker_rejections_total` metric label and the `admission.circuit_breaker_rejected` audit event.
- `scope` (object, required) — tier-specific matcher object. The key must match the selected `limit_tier`:

| `limit_tier`     | `scope` shape                                                                                      |
|------------------|-----------------------------------------------------------------------------------------------------|
| `runtime`        | `{ "runtime": "<runtime-name>" }`                                                                   |
| `pool`           | `{ "pool": "<pool-name>" }`                                                                         |
| `connector`      | `{ "connector": "<connector-identifier>" }`                                                         |
| `operation_type` | `{ "operation_type": "uploads" \| "delegation_depth" \| "session_creation" \| "message_injection" }` |

Invoking against a `{name}` that has no existing state **atomically registers and opens** the breaker with the supplied `limit_tier`/`scope`. Invoking against an existing `{name}` whose persisted `limit_tier` or `scope` differs from the request body is rejected with `INVALID_BREAKER_SCOPE` (HTTP 422) — scope is immutable across the breaker's lifecycle. To change scope, close the breaker and open a new one under a distinct `{name}`.

Responses:
- `200 OK` — breaker is open. Response body: `{ "name": "<name>", "state": "open", "reason": "...", "opened_at": "...", "opened_by_sub": "...", "opened_by_tenant_id": "...", "limit_tier": "...", "scope": {...} }` (the `opened_by_sub`/`opened_by_tenant_id` pair mirrors the Redis `cb:{name}` value shape in [Section 12.4](../spec/12_storage-architecture.html#124-redis-ha-and-failure-modes) and uses the same OIDC-subject vocabulary as the `caller_sub`/`caller_tenant_id` fields on the `admission.circuit_breaker_rejected` audit event).
- `422 INVALID_BREAKER_SCOPE` — `limit_tier` or `scope` is missing, outside its closed vocabulary, inconsistent with the selected tier, or mismatched against the persisted scope. See [error catalog]({{ site.baseurl }}/reference/error-catalog.html#invalid_breaker_scope).

Emits the `circuit_breaker.state_changed` audit event ([Section 16.7](../spec/16_observability.html#167-section-25-audit-events)).

**Dry-run:** Supported. With `?dryRun=true`, the gateway validates `reason`/`limit_tier`/`scope` and the scope-immutability rule against any persisted `cb:{name}` value (`422 INVALID_BREAKER_SCOPE` on mismatch) but does not write Redis. The response body is a reduced simulation object with exactly these five fields: `name`, `state` (predicted `"open"`), `reason`, `limit_tier`, `scope` — plus a top-level `simulation` object: `{"currentState": "open" | "closed" | "not_registered", "predictedState": "open", "wouldChangeState": <bool>}`. Audit-like fields of the real-call response (`opened_at`, `opened_by_sub`, `opened_by_tenant_id`) are **not** populated under `dryRun` because no state mutation occurs and no audit trail is recorded. `wouldChangeState` is `false` when the breaker is already open with the same `limit_tier`/`scope` (idempotent no-op). No `circuit_breaker.state_changed` audit event is emitted under `dryRun`.

### POST /v1/admin/circuit-breakers/{name}/close
{: .d-inline-block }
platform-admin
{: .label .label-red }

Close (deactivate) an operator-managed circuit breaker. Body is empty. The persisted `limit_tier` and `scope` are retained across open→closed→open transitions for the same `{name}`.

Responses:
- `200 OK` — breaker is closed.
- `404 RESOURCE_NOT_FOUND` — no breaker is registered under `{name}` (no `cb:{name}` key exists in Redis).

Emits the `circuit_breaker.state_changed` audit event.

**Dry-run:** Supported. With `?dryRun=true`, the gateway validates that `{name}` exists in Redis (`404 RESOURCE_NOT_FOUND` if not) and reads its persisted `limit_tier`/`scope` but does not write Redis. The response body is a reduced simulation object with exactly these four fields: `name`, `state` (predicted `"closed"`), `limit_tier`, `scope` (the latter two read from the persisted `cb:{name}` value) — plus a top-level `simulation` object: `{"currentState": "open" | "closed", "predictedState": "closed", "wouldChangeState": <bool>}`. No audit-like fields are populated since no state mutation occurs. `wouldChangeState` is `false` when the breaker is already closed (idempotent no-op). No `circuit_breaker.state_changed` audit event is emitted under `dryRun`.

---

## Quota

### POST /v1/admin/quota/reconcile
{: .d-inline-block }
platform-admin
{: .label .label-red }

Re-aggregate in-flight session usage from Postgres into Redis after Redis recovery. Use this after a Redis failover to ensure quota counters are accurate.

---

## Users

### POST /v1/oauth/token
{: .d-inline-block }
any authenticated subject
{: .label .label-green }

Canonical OAuth token endpoint (RFC 6749 + RFC 8693 token exchange). Used across roles:

| Caller | Purpose | Requires |
|:-------|:--------|:---------|
| `platform-admin` | Rotate admin tokens; mint cluster-scoped operator tokens | admin-level `subject_token` |
| `tenant-admin` | Rotate tenant-scoped tokens; mint scoped operator tokens for the tenant | tenant-admin `subject_token` |
| End users | Rotate their own session token | user `subject_token` |
| Gateway (internal) | Mint delegation child tokens with `actor_token` set to the parent session token | parent session token |
| `lenny-ops` (internal) | Mint short-lived scoped tokens for agent-operability calls | operator `subject_token` + requested scope |

For rotation, use `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` with `subject_token=<current_token>` and `requested_token_type` matching the subject. For delegation child-token minting, additionally supply `actor_token=<parent_session_token>` and a narrowed `scope` string. Scope narrowing is enforced server-side: the response token's scope is always a subset of the parent's.

See [Authentication](../client-guide/authentication.md#token-rotation-and-exchange-v1oauthtoken). The CLI command `lenny-ctl admin users rotate-token --user <name>` wraps this endpoint and additionally patches the `lenny-admin-token` Kubernetes Secret.

### POST /v1/admin/users/{user_id}/invalidate
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Terminate all active sessions for a user and revoke their tokens immediately. Used during incident response. `tenant-admin` is scoped to their own tenant.

### POST /v1/admin/users/{user_id}/erase
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Initiate a GDPR user-level erasure job. Returns a job ID.

---

## Erasure jobs

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `GET /v1/admin/erasure-jobs/{job_id}` | GET | platform-admin, tenant-admin | Query erasure job status: phase, completion %, time elapsed, errors |
| `POST /v1/admin/erasure-jobs/{job_id}/retry` | POST | platform-admin | Retry a failed erasure job |
| `POST /v1/admin/erasure-jobs/{job_id}/clear-processing-restriction` | POST | platform-admin | Clear the `processing_restricted` flag. Body: `{"justification": "..."}` |

---

## Bootstrap

### POST /v1/admin/bootstrap
{: .d-inline-block }
platform-admin
{: .label .label-red }

Apply a seed configuration file (idempotent upsert of runtimes, pools, tenants, etc.). Same schema as `bootstrap` Helm values.

Every invocation emits a `platform.bootstrap_applied` audit event recording: calling identity, seed file SHA-256 hash, resource changes summary, and `dryRun` flag. The audit record follows the OCSF v1.1.0 schema and crosses the EventBus as the `data` field of a CloudEvents v1.0.2 envelope with `datacontenttype=application/ocsf+json`; see the [CloudEvents catalog](../reference/cloudevents-catalog.md) and [OCSF audit guide](../operator-guide/audit-ocsf.md).

**Dry-run:** Supported (audit event is emitted with `dryRun: true`).

---

## Preflight

### POST /v1/admin/preflight
{: .d-inline-block }
platform-admin
{: .label .label-red }

Run preflight checks (Postgres, Redis, MinIO connectivity and schema version). POST because the endpoint performs active outbound connectivity probes.

**Response:**

```json
{
  "checks": [
    { "name": "postgres", "status": "passed", "latencyMs": 5 },
    { "name": "redis", "status": "passed", "latencyMs": 2 },
    { "name": "minio", "status": "passed", "latencyMs": 15 },
    { "name": "cert-manager", "status": "passed", "latencyMs": 8 },
    { "name": "schema-version", "status": "passed", "details": "v1.2.0" }
  ],
  "overall": "passed"
}
```

---

## Legal holds

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/legal-hold` | POST | platform-admin, tenant-admin | Set or clear a legal hold. Body: `{"resourceType": "session"\|"artifact", "resourceId": "...", "hold": true, "note": "..."}` |
| `GET /v1/admin/legal-holds` | GET | platform-admin, tenant-admin | List active legal holds. Query: `?tenant_id=`, `?resource_type=`, `?resource_id=` |

---

## Billing corrections

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `POST /v1/admin/billing-corrections` | POST | platform-admin | Issue a billing correction event |
| `POST /v1/admin/billing-corrections/{id}/approve` | POST | platform-admin | Approve a pending correction. Submitter cannot self-approve. |
| `POST /v1/admin/billing-corrections/{id}/reject` | POST | platform-admin | Reject a pending correction. Submitter cannot self-reject. |
| `POST /v1/admin/billing-correction-reasons` | POST | platform-admin | Add a deployer-defined correction reason code |
| `GET /v1/admin/billing-correction-reasons` | GET | platform-admin | List all correction reason codes |
| `DELETE /v1/admin/billing-correction-reasons/{code}` | DELETE | platform-admin | Remove a deployer-added code (built-in codes cannot be deleted) |

---

## Sessions (admin view)

| Endpoint | Method | Role | Description |
|:---------|:-------|:-----|:------------|
| `GET /v1/admin/sessions/{id}` | GET | platform-admin | Get session state with internal pod assignment and pool details. The session's materialized `workspacePlan` is included in the response body; it validates against the [WorkspacePlan JSON Schema](https://schemas.lenny.dev/workspaceplan/v1.json). |
| `POST /v1/admin/sessions/{id}/force-terminate` | POST | platform-admin | Force-terminate a session |

---

## Delegation tree operations

### DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial
{: .d-inline-block }
platform-admin tenant-admin
{: .label .label-red .label-blue }

Clear the extension-denied flag on a session subtree, bypassing the rejection cool-off window.

---

## Schema migrations

### GET /v1/admin/schema/migrations/status
{: .d-inline-block }
platform-admin
{: .label .label-red }

Return the current expand-contract migration phase for each active migration.

**Response:**

```json
{
  "migrations": [
    {
      "version": "1.3.0",
      "phase": "phase2_deployed",
      "appliedAt": "2026-04-08T12:00:00Z",
      "gateCheckResult": "not_run",
      "migrationJobName": "lenny-migrate-1-3-0"
    }
  ]
}
```
