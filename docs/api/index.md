---
layout: default
title: API Reference
nav_order: 6
has_children: true
---

# API Reference

Lenny exposes multiple API surfaces through a pluggable **ExternalAdapterRegistry**. Each surface is optimized for a different class of client -- from CI/CD pipelines and admin dashboards to interactive MCP-native agents and OpenAI SDK consumers. All surfaces share a single authentication model, error taxonomy, and session lifecycle.

---

## API surfaces at a glance

| Surface | Path prefix | Best for | Section |
|:--------|:------------|:---------|:--------|
| [REST API](rest/index.html) | `/v1/` | Any HTTP client -- CI/CD, CLIs, dashboards, custom integrations. Primary integration point with full coverage of all operations. | [REST Reference](rest/index.html) |
| [MCP API](mcp.html) | `/mcp` | Interactive streaming sessions, recursive delegation, elicitation (human-in-the-loop), and MCP-native clients (Claude Desktop, Cursor, etc.). | [MCP Reference](mcp.html) |
| [OpenAI Completions](openai-completions.html) | `/v1/chat/completions` | Drop-in compatibility with the OpenAI SDK. Point your existing `openai` client at Lenny and use any registered runtime as a model. | [OpenAI Completions Reference](openai-completions.html) |
| [Open Responses](open-responses.html) | `/v1/responses` | Open Responses protocol support. Compatible with clients built against the OpenAI Responses API or the Open Responses specification. | [Open Responses Reference](open-responses.html) |
| [Admin API](admin.html) | `/v1/admin/` | Operator-only management -- runtimes, pools, tenants, credential pools, delegation policies, experiments, environments, and more. | [Admin Reference](admin.html) |
| [Internal gRPC](internal.html) | N/A (gRPC) | Gateway-to-pod communication over gRPC + mTLS. For runtime adapter authors only. | [Internal gRPC Reference](internal.html) |

---

## Base URL

All HTTP APIs are served from the gateway's base URL:

```
https://<gateway-host>/
```

In local development (`make run` or `docker compose up`), the default base URL is:

```
http://localhost:8080/
```

The REST API, MCP API, OpenAI Completions API, Open Responses API, and Admin API are all served from the same gateway host on the same port. The gateway routes requests to the correct adapter based on the URL path prefix.

---

## Authentication

All API surfaces require authentication. Lenny supports two authentication mechanisms:

### Bearer token (OIDC)

Client-facing APIs (`/v1/`, `/mcp`, `/v1/chat/completions`, `/v1/responses`) authenticate via **OIDC Bearer tokens**. Include the token in the `Authorization` header:

```
Authorization: Bearer <oidc-token>
```

The gateway validates the OIDC token signature, extracts `user_id` and `tenant_id` claims, and enforces role-based access control (RBAC). See the [RBAC roles](#rbac-roles) section below for the permission model.

### Admin token

Admin API endpoints (`/v1/admin/`) authenticate via a static admin token configured at deployment time (`LENNY_API_TOKEN` environment variable or Kubernetes Secret). Include it as a Bearer token:

```
Authorization: Bearer <admin-token>
```

Admin API endpoints have separate (higher) rate-limit windows from client-facing endpoints.

### RBAC roles

| Role | Scope | Description |
|:-----|:------|:------------|
| `platform-admin` | All tenants | Full access to all endpoints across all tenants |
| `tenant-admin` | Own tenant | Full access scoped to their own tenant |
| `tenant-viewer` | Own tenant | Read-only access scoped to their own tenant |
| `billing-viewer` | Own tenant | Usage and metering data only |
| `user` | Own sessions | Create and manage their own sessions |

Custom roles can be defined per tenant via `POST /v1/admin/tenants/{id}/roles`.

---

## Content types

| API surface | Request content type | Response content type |
|:------------|:--------------------|:---------------------|
| REST API | `application/json` | `application/json` |
| MCP API | Streamable HTTP (MCP transport) | Streamable HTTP (MCP transport) |
| OpenAI Completions | `application/json` | `application/json` (or `text/event-stream` for streaming) |
| Open Responses | `application/json` | `application/json` (or `text/event-stream` for streaming) |
| Admin API | `application/json` | `application/json` |

---

## API versioning and stability

### URL-versioned REST API

The REST API is versioned via URL path prefix (`/v1/`). Breaking changes require a new version (`/v2/`). Non-breaking additions (new fields, new endpoints) are added to the current version without a version bump.

### Backwards compatibility guarantees

- **REST API:** The previous version is supported for at least **6 months** after a new version ships.
- **MCP tools:** The gateway supports the two most recent MCP spec versions concurrently. When a new MCP spec version is adopted, the oldest supported version enters a 6-month deprecation window.
- **Runtime adapter protocol:** Versioned independently. Major version changes are breaking; minor/patch versions are backwards compatible.

### Deprecation policy

When an API version enters the deprecation window:

1. The gateway emits deprecation warnings in response headers (`X-Lenny-Deprecated-Version`).
2. The deprecated version continues to function for the full deprecation period.
3. After the deprecation period, new connections using the old version are rejected, but existing sessions that negotiated the old version are allowed to complete.

### MCP tool schema evolution

MCP tool schemas can add optional fields without a version bump. Removing or renaming fields, or changing semantics, is a breaking change that requires a new MCP protocol version.

### Definition of "breaking change"

Removing a field, changing a field's type, changing the default behavior of an existing feature, removing an endpoint or tool, or changing error codes for existing operations.

### Stability tiers

| Tier | Guarantee |
|:-----|:----------|
| `stable` | Covered by the versioning guarantees above |
| `beta` | May change between minor releases with deprecation notice |
| `alpha` | May change without notice |

---

## Pagination

All list endpoints return paginated results using a **cursor-based** envelope.

### Query parameters

| Parameter | Type | Default | Description |
|:----------|:-----|:--------|:------------|
| `cursor` | string | _(none)_ | Opaque cursor returned from a previous response. Omit for the first page. |
| `limit` | integer | `50` | Number of items per page. Range: 1--200. Values outside this range are clamped. |
| `sort` | string | `created_at:desc` | Sort field and direction (`field:asc` or `field:desc`). Supported fields vary by resource. |

### Response envelope

```json
{
  "items": [
    { "...resource objects..." }
  ],
  "cursor": "eyJpZCI6IjAxOTVmMzQ...",
  "hasMore": true,
  "total": 1247
}
```

| Field | Type | Description |
|:------|:-----|:------------|
| `items` | array | The page of results (required) |
| `cursor` | string or null | Opaque cursor for the next page. `null` when no more results exist. |
| `hasMore` | boolean | `true` if additional pages exist |
| `total` | integer or absent | Total count of matching items. Present only when cheaply computable; omitted when a full table scan would be required. |

**Cursor rules:**
- Cursors are opaque, URL-safe strings. Do not parse or construct them.
- Cursors encode the sort key and a unique tiebreaker for stable iteration.
- Cursors are valid for **24 hours**. Expired cursors return `VALIDATION_ERROR` with `details.fields[0].rule: "cursor_expired"`.

---

## Rate limiting

Rate limits are applied **per tenant** and **per user**. Admin API endpoints have separate (higher) rate-limit windows.

### Response headers

Every REST API response includes rate-limit headers:

| Header | Description |
|:-------|:------------|
| `X-RateLimit-Limit` | Maximum requests permitted in the current window |
| `X-RateLimit-Remaining` | Requests remaining in the current window |
| `X-RateLimit-Reset` | UTC epoch seconds when the current window resets |
| `Retry-After` | Seconds to wait before retrying (present on `429` and `503` responses) |

When a rate limit is exceeded, the gateway returns `429` with error code `RATE_LIMITED`.

---

## Error format

All API surfaces (REST, MCP, OpenAI Completions, Open Responses, Admin) return errors using a canonical JSON envelope:

```json
{
  "error": {
    "code": "QUOTA_EXCEEDED",
    "category": "POLICY",
    "message": "Tenant t1 has exceeded its monthly session quota (limit: 500).",
    "retryable": false,
    "details": {}
  }
}
```

| Field | Type | Description |
|:------|:-----|:------------|
| `code` | string | Machine-readable error code (see [Error catalog](#error-catalog)) |
| `category` | string | One of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM` |
| `message` | string | Human-readable description |
| `retryable` | boolean | Whether the client should retry the request |
| `details` | object | Additional context (structure varies by error code) |

### Error categories

| Category | Meaning | Client action |
|:---------|:--------|:-------------|
| `TRANSIENT` | Temporary infrastructure issue | Retry with exponential backoff |
| `PERMANENT` | Request is invalid and will never succeed as-is | Fix the request |
| `POLICY` | Blocked by a policy rule (quota, rate limit, permission) | Check limits or permissions |
| `UPSTREAM` | An external dependency returned an error | Investigate the upstream service |

### Validation errors

When `code` is `VALIDATION_ERROR`, the `details` field contains a `fields` array:

```json
{
  "error": {
    "code": "VALIDATION_ERROR",
    "category": "PERMANENT",
    "message": "Request validation failed.",
    "retryable": false,
    "details": {
      "fields": [
        {
          "field": "runtime",
          "message": "must not be empty",
          "rule": "required"
        },
        {
          "field": "workspace.maxSizeMB",
          "message": "must be between 1 and 10240",
          "rule": "range",
          "params": { "min": 1, "max": 10240 }
        }
      ]
    }
  }
}
```

### Error catalog
{: #error-catalog }

The complete error code catalog is shared across all API surfaces. MCP tool errors use the same `code` and `category` fields inside the MCP error response format, enabling a single error-handling strategy regardless of API surface.

| Code | Category | HTTP | Retryable | Description |
|:-----|:---------|:-----|:----------|:------------|
| `VALIDATION_ERROR` | `PERMANENT` | 400 | No | Request body or query parameters failed validation |
| `INVALID_STATE_TRANSITION` | `PERMANENT` | 409 | No | Operation not valid for the current resource state |
| `RESOURCE_NOT_FOUND` | `PERMANENT` | 404 | No | Resource does not exist or is not visible to the caller |
| `RESOURCE_ALREADY_EXISTS` | `PERMANENT` | 409 | No | A resource with the given identifier already exists |
| `RESOURCE_HAS_DEPENDENTS` | `PERMANENT` | 409 | No | Resource cannot be deleted; active dependents exist |
| `ETAG_MISMATCH` | `PERMANENT` | 412 | No | `If-Match` ETag does not match current version |
| `ETAG_REQUIRED` | `PERMANENT` | 428 | No | `If-Match` header required on PUT but missing |
| `UNAUTHORIZED` | `PERMANENT` | 401 | No | Missing or invalid authentication credentials |
| `FORBIDDEN` | `POLICY` | 403 | No | Authenticated but not authorized |
| `PERMISSION_DENIED` | `POLICY` | 403 | No | Lacks required permission for this specific resource |
| `QUOTA_EXCEEDED` | `POLICY` | 429 | No | Tenant or user quota exceeded |
| `RATE_LIMITED` | `POLICY` | 429 | No | Request rate limit exceeded |
| `BUDGET_EXHAUSTED` | `POLICY` | 429 | No | Token or tree-size budget insufficient |
| `CREDENTIAL_POOL_EXHAUSTED` | `POLICY` | 503 | No | No available credentials in pool |
| `CREDENTIAL_REVOKED` | `POLICY` | 403 | No | Credential has been explicitly revoked |
| `RUNTIME_UNAVAILABLE` | `TRANSIENT` | 503 | Yes | No healthy pods for the requested runtime |
| `WARM_POOL_EXHAUSTED` | `TRANSIENT` | 503 | Yes | No idle pods in warm pool |
| `POD_CRASH` | `TRANSIENT` | 502 | Yes | Session pod terminated unexpectedly |
| `TIMEOUT` | `TRANSIENT` | 504 | Yes | Operation timed out |
| `UPSTREAM_ERROR` | `UPSTREAM` | 502 | Yes | External dependency returned an error |
| `INTERNAL_ERROR` | `TRANSIENT` | 500 | Yes | Unexpected server error |
| `POOL_DRAINING` | `TRANSIENT` | 503 | Yes | Target pool is draining; not accepting new sessions |
| `CIRCUIT_BREAKER_OPEN` | `POLICY` | 503 | No | Operator-declared circuit breaker is active |
| `MCP_VERSION_UNSUPPORTED` | `PERMANENT` | 400 | No | Client MCP version is not supported |
| `INJECTION_REJECTED` | `POLICY` | 403 | No | Message injection rejected by runtime policy |
| `SCOPE_DENIED` | `POLICY` | 403 | No | Messaging scope denies target session |
| `ISOLATION_MONOTONICITY_VIOLATED` | `POLICY` | 403 | No | Delegation target isolation is less restrictive |
| `TARGET_TERMINAL` | `PERMANENT` | 409 | No | Target session is in a terminal state |
| `STORAGE_QUOTA_EXCEEDED` | `POLICY` | 429 | No | Artifact storage quota would be exceeded |
| `IMAGE_RESOLUTION_FAILED` | `PERMANENT` | 422 | No | Container image reference invalid or unresolvable |
| `ERASURE_IN_PROGRESS` | `POLICY` | 403 | No | User has a pending GDPR erasure job |

For the complete catalog of 50+ error codes with full descriptions and `details` schemas, see the [OpenAPI specification](#openapi-specification).

---

## OpenAPI specification
{: #openapi-specification }

The gateway serves its **OpenAPI 3.x** specification at two endpoints (no authentication required):

| Endpoint | Format |
|:---------|:-------|
| `GET /openapi.yaml` | YAML |
| `GET /openapi.json` | JSON |

The served spec reflects the API version of the running gateway instance. The `info.version` field matches the gateway's release version. Community SDK generators should target `/openapi.yaml` as the canonical source.

The OpenAPI spec is the **single source of truth** for all REST API operations. MCP tool schemas for overlapping operations are generated from the OpenAPI spec, ensuring structural consistency by construction.

---

## Interactive API explorer

The [REST API Reference](rest/index.html) page embeds **Swagger UI** loaded from the gateway's OpenAPI spec. Use it to:

- Browse all REST and Admin API endpoints
- View request and response schemas
- Try out API calls directly from the browser (requires a Bearer token)
- Deep-link to specific endpoints

---

## Next steps

| Page | Description |
|:-----|:------------|
| [REST API Reference (Swagger UI)](rest/index.html) | Interactive explorer for all REST endpoints |
| [MCP API Reference](mcp.html) | Connection setup, version negotiation, and complete tool catalog |
| [OpenAI Completions API](openai-completions.html) | Drop-in OpenAI SDK compatibility layer |
| [Open Responses API](open-responses.html) | Open Responses protocol support |
| [Admin API Reference](admin.html) | Operator-only management endpoints |
| [Internal gRPC API](internal.html) | Gateway-to-pod communication for runtime adapter authors |
