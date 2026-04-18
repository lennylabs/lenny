---
layout: default
title: API Reference
nav_order: 6
has_children: true
---

# API Reference

The Lenny gateway speaks several different APIs on the same port. Each one is aimed at a different kind of client, and all of them share one authentication model, one error format, and one session lifecycle. Pick whichever matches what your client code already knows how to do.

---

## The APIs at a glance

| Surface | Path | Best for |
|:--------|:-----|:---------|
| [REST](rest/index.html) | `/v1/` | HTTP clients -- CI/CD, scripts, dashboards, custom integrations. Covers every operation. |
| [MCP](mcp.html) | `/mcp` | Interactive streaming, multi-agent delegation, mid-session user prompts, and MCP hosts like Claude Desktop or Cursor. |
| [OpenAI Chat Completions](openai-completions.html) | `/v1/chat/completions` | Code that already uses the OpenAI SDK. Each Lenny runtime shows up as a model. |
| [Open Responses](open-responses.html) | `/v1/responses` | The Open Responses specification and the OpenAI Responses API. |
| [Admin](admin.html) | `/v1/admin/` | Operator-only management: runtimes, pools, tenants, credential pools, delegation policies, and more. |
| [Internal gRPC](internal.html) | internal only | Communication between the gateway and session pods. Runtime authors may need this; clients do not. |

---

## Base URL

Everything is served from the gateway's base URL:

```
https://<gateway-host>/
```

When you start the stack locally with `lenny up`, that's:

```
https://localhost:8443/
```

All five HTTP surfaces -- REST, MCP, OpenAI Chat Completions, Open Responses, and Admin -- share one host and one port. The gateway dispatches to the right handler based on the URL path.

---

## Authentication

Every request needs an `Authorization: Bearer <token>` header. There are two ways to get a token.

### From your identity provider

Client-facing APIs (`/v1/`, `/mcp`, `/v1/chat/completions`, `/v1/responses`) accept tokens from the identity provider your operator wired Lenny up to -- usually Google, Okta, Azure AD, or another OIDC provider. The gateway verifies the token's signature, pulls the user and tenant out of its claims, and applies the permission model described below.

```
Authorization: Bearer <your-oidc-token>
```

If you already have a working OIDC login in your application, you can pass that same token straight through.

### Admin token

The Admin API (`/v1/admin/`) uses a shared admin token that your operator sets at install time. Pass it the same way:

```
Authorization: Bearer <admin-token>
```

Admin endpoints have their own rate-limit windows, separate from the client-facing ones.

### Exchanging tokens and refreshing them

`POST /v1/oauth/token` implements the standard OAuth 2.0 token-exchange flow (RFC 8693) and refresh (RFC 6749). Use it to:

- Swap an identity-provider token for a Lenny access token
- Refresh an access token that's about to expire
- Rotate the admin token without restarting the gateway

See the [OAuth Token Exchange walkthrough](../tutorials/oauth-token-exchange) for a step-by-step example.

### Roles and permissions

Lenny applies role-based permissions to every request. The built-in roles are:

| Role | Scope | What they can do |
|:-----|:------|:-----------------|
| `platform-admin` | All tenants | Everything, across every tenant |
| `tenant-admin` | Their own tenant | Everything inside their tenant |
| `tenant-viewer` | Their own tenant | Read-only |
| `billing-viewer` | Their own tenant | Usage and metering data only |
| `user` | Their own sessions | Create and manage sessions they own |

Tenants can define custom roles via `POST /v1/admin/tenants/{id}/roles`.

---

## Content types

| API | Request | Response |
|:----|:--------|:---------|
| REST | `application/json` | `application/json` |
| MCP | MCP over Streamable HTTP | MCP over Streamable HTTP |
| OpenAI Chat Completions | `application/json` | `application/json` -- or `text/event-stream` when you ask for streaming |
| Open Responses | `application/json` | `application/json` -- or `text/event-stream` when you ask for streaming |
| Admin | `application/json` | `application/json` |

---

## API versioning and stability

### The REST API is versioned in the URL

The REST API is versioned by URL prefix (`/v1/`). Breaking changes require a new prefix (`/v2/`). Additions -- new fields on responses, new endpoints -- ship inside the current version without a bump.

### What's guaranteed

- **REST:** a previous version keeps working for at least **6 months** after a new version ships.
- **MCP:** the gateway supports the two most recent MCP spec versions side-by-side. When a newer version is adopted, the oldest enters the same 6-month deprecation window.
- **Runtime adapter protocol:** versioned on its own track. Major versions can break; minor and patch versions don't.

### Deprecation

When a version enters deprecation:

1. The gateway adds an `X-Lenny-Deprecated-Version` header to every response.
2. The deprecated version keeps working through the full window.
3. After the window closes, new connections on the old version are rejected -- but any session that's still running on it is allowed to finish.

### How MCP tool schemas change

Tool schemas can add optional fields without a version bump. Removing a field, renaming it, or changing what a field means is a breaking change and needs a new MCP protocol version.

### What counts as "breaking"

Removing a field, changing a field's type, changing the default behavior of a feature, removing an endpoint or tool, or changing an existing operation's error code.

### Stability labels

Every endpoint, tool, and response field carries one of three labels:

| Label | What it means |
|:------|:-------------|
| `stable` | Covered by everything above |
| `beta` | Can change between minor releases, with deprecation notice |
| `alpha` | Can change without notice -- don't build production code against it |

---

## Pagination

List endpoints page through results with a cursor, not page numbers.

### Query parameters

| Parameter | Type | Default | What it does |
|:----------|:-----|:--------|:-------------|
| `cursor` | string | _(none)_ | The cursor returned by the previous response. Leave it off for the first page. |
| `limit` | integer | `50` | Items per page, between 1 and 200. Values outside that range get clamped. |
| `sort` | string | `created_at:desc` | Sort field and direction (`field:asc` or `field:desc`). Which fields are supported depends on the resource. |

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

| Field | Type | What it is |
|:------|:-----|:-----------|
| `items` | array | The page of results |
| `cursor` | string or null | Pass this back in the next request's `cursor` query parameter. `null` when there are no more results. |
| `hasMore` | boolean | `true` when there are more pages after this one |
| `total` | integer or absent | The total count of matching items. Only present when it can be computed cheaply -- omitted when it would require a full table scan. |

**About cursors:**
- They're opaque strings. Don't try to parse or construct them yourself.
- They encode the sort key plus a tiebreaker, so iteration is stable even when data changes between pages.
- They're valid for 24 hours. Expired cursors come back as a `VALIDATION_ERROR` with `details.fields[0].rule: "cursor_expired"`.

---

## Rate limiting

Rate limits apply per tenant and per user. The Admin API has its own, higher limits.

### Response headers

Every response tells you where you are inside the current window:

| Header | What it tells you |
|:-------|:------------------|
| `X-RateLimit-Limit` | The maximum number of requests allowed in the current window |
| `X-RateLimit-Remaining` | How many you have left |
| `X-RateLimit-Reset` | When the window resets, as a UTC epoch-seconds timestamp |
| `Retry-After` | How many seconds to wait before retrying -- included on `429` and `503` responses |

Blow through the limit and the gateway returns `429` with error code `RATE_LIMITED`.

---

## Error format

Every API -- REST, MCP, OpenAI Chat Completions, Open Responses, Admin -- returns errors in the same JSON envelope:

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

| Field | Type | What it is |
|:------|:-----|:-----------|
| `code` | string | Machine-readable identifier (see [Error catalog](#error-catalog)) |
| `category` | string | One of `TRANSIENT`, `PERMANENT`, `POLICY`, or `UPSTREAM` |
| `message` | string | Human-readable description |
| `retryable` | boolean | Whether retrying will help |
| `details` | object | Extra context -- the shape depends on the error code |

### The four categories

| Category | What it means | What to do |
|:---------|:--------------|:-----------|
| `TRANSIENT` | A temporary infrastructure hiccup | Retry with exponential backoff |
| `PERMANENT` | The request is wrong and won't succeed as-is | Fix the request |
| `POLICY` | A policy rule rejected the request (quota, rate limit, permission) | Check limits or permissions |
| `UPSTREAM` | An external dependency returned an error | Check the upstream service |

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

The same codes apply across every API. MCP tool errors use the same `code` and `category` fields inside MCP's own error wrapping, so you can handle errors with one strategy no matter which API you're calling.

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

The full catalog (50+ codes, with the shape of each error's `details`) lives in the [OpenAPI specification](#openapi-specification).

---

## OpenAPI specification
{: #openapi-specification }

The gateway publishes its OpenAPI 3.x specification at two endpoints, no authentication required:

| Endpoint | Format |
|:---------|:-------|
| `GET /openapi.yaml` | YAML |
| `GET /openapi.json` | JSON |

The spec is always the one for the running gateway -- `info.version` matches the gateway's release. If you're generating an SDK, point it at `/openapi.yaml`.

The OpenAPI spec is the source of truth for the REST API. MCP tool schemas for any operation that also appears over REST are generated from the same spec, which is why the two surfaces stay structurally consistent.

---

## Try the API in your browser

The [REST API Reference](rest/index.html) page embeds Swagger UI loaded from the gateway's live OpenAPI spec. Use it to:

- Browse every REST and Admin endpoint
- See request and response shapes
- Make real API calls right from the browser (you'll need a Bearer token)
- Deep-link to a specific endpoint when you're sharing

---

## Next steps

| Page | What you'll find |
|:-----|:-----------------|
| [REST API Reference (Swagger UI)](rest/index.html) | Interactive explorer for every REST endpoint |
| [MCP API Reference](mcp.html) | Connection setup, version negotiation, and the full tool catalog |
| [OpenAI Chat Completions API](openai-completions.html) | Compatibility layer for the OpenAI SDK |
| [Open Responses API](open-responses.html) | Open Responses protocol details |
| [Admin API Reference](admin.html) | Operator-only management endpoints |
| [Internal gRPC API](internal.html) | Gateway-to-pod protocol, for runtime adapter authors |
