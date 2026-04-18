---
layout: default
title: "Error Handling"
parent: "Client Guide"
nav_order: 5
---

# Error Handling

Lenny uses a structured error response format with machine-readable error codes, categories that guide retry decisions, and ETag-based optimistic concurrency. This page covers the error format, complete error code catalog, retry strategies, ETags, and rate limiting.

---

## Error Response Format

All REST API endpoints return errors using a canonical JSON envelope:

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
|---|---|---|
| `code` | string | Machine-readable error code (see catalog below) |
| `category` | string | One of `TRANSIENT`, `PERMANENT`, `POLICY`, `UPSTREAM` |
| `message` | string | Human-readable description |
| `retryable` | boolean | Whether the client should retry this request |
| `details` | object | Additional context; structure varies by error code |

---

## Error Categories

| Category | Meaning | Retry? | Client Action |
|---|---|---|---|
| `TRANSIENT` | Temporary failure, may resolve on its own | **Yes** with exponential backoff | Retry with backoff; respect `Retry-After` header |
| `PERMANENT` | Request is invalid or resource state prevents the operation | **No** | Fix the request or wait for state change |
| `POLICY` | Request denied by policy, quota, or configuration | **No** (usually) | Check configuration, quotas, or permissions |
| `UPSTREAM` | External dependency (LLM provider, MCP tool, auth provider) failed | **Sometimes** | Check upstream service status; may retry |

---

## Error Code Catalog

### Session and Resource Errors

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `VALIDATION_ERROR` | `PERMANENT` | 400 | No | Request body or query parameters failed validation | Fix the request body. Check `details.fields` for specific failures. |
| `INVALID_STATE_TRANSITION` | `PERMANENT` | 409 | No | Operation not valid for current resource state | Check `details.currentState` and `details.allowedStates`. Wait for correct state. |
| `RESOURCE_NOT_FOUND` | `PERMANENT` | 404 | No | Resource does not exist or is not visible | Verify the resource ID and your access permissions. |
| `RESOURCE_ALREADY_EXISTS` | `PERMANENT` | 409 | No | Resource with this identifier already exists | Use a different identifier or update the existing resource. |
| `RESOURCE_HAS_DEPENDENTS` | `PERMANENT` | 409 | No | Resource cannot be deleted (active dependents) | Check `details.dependents` for blocking references. Remove dependents first. |
| `TARGET_TERMINAL` | `PERMANENT` | 409 | No | Target session is in a terminal state | The session has ended. Create a new session or derive from this one. |
| `TARGET_NOT_READY` | `TRANSIENT` | 409 | Yes | Target session is in a pre-running state | Retry after the session transitions to `running`. |

### Authentication and Authorization Errors

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `UNAUTHORIZED` | `PERMANENT` | 401 | No | Missing or invalid credentials | Refresh your access token and retry. |
| `FORBIDDEN` | `POLICY` | 403 | No | Authenticated but not authorized | Check your role and permissions for this operation. |
| `PERMISSION_DENIED` | `POLICY` | 403 | No | Lacks required permission for this resource | Check delegation scope or policy rules. |
| `INJECTION_REJECTED` | `POLICY` | 403 | No | Message injection rejected by runtime | The runtime does not support message injection. |
| `SCOPE_DENIED` | `POLICY` | 403 | No | Inter-session message blocked by messaging scope | Verify the sender's `messagingScope` permits this target. |
| `CROSS_TENANT_MESSAGE_DENIED` | `POLICY` | 403 | No | Cross-tenant messaging is prohibited | Ensure sender and target are in the same tenant. |
| `CREDENTIAL_REVOKED` | `POLICY` | 403 | No | Credential has been revoked | The session's credential was explicitly revoked. Contact admin. |

### Quota and Rate Limiting

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `QUOTA_EXCEEDED` | `POLICY` | 429 | No | Tenant or user quota exceeded | Wait for quota reset or contact admin for quota increase. |
| `RATE_LIMITED` | `POLICY` | 429 | Yes | Request rate limit exceeded | Wait for `Retry-After` seconds, then retry. |
| `BUDGET_EXHAUSTED` | `POLICY` | 429 | No | Token budget or tree-size budget exhausted | Request a lease extension or terminate. Check `details.limitType`. |
| `STORAGE_QUOTA_EXCEEDED` | `POLICY` | 429 | No | Tenant artifact storage quota exceeded | Delete old artifacts or request a storage quota increase. |
| `EVAL_QUOTA_EXCEEDED` | `POLICY` | 429 | No | Per-session eval result storage cap reached | The session has reached `maxEvalsPerSession` (default 10,000). |

### Infrastructure and Availability

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `RUNTIME_UNAVAILABLE` | `TRANSIENT` | 503 | Yes | No healthy pods available | Retry with exponential backoff. |
| `WARM_POOL_EXHAUSTED` | `TRANSIENT` | 503 | Yes | No idle pods in warm pool | Retry with exponential backoff. |
| `CREDENTIAL_POOL_EXHAUSTED` | `POLICY` | 503 | Yes | No available credentials in pool | Retry or wait for credentials to become available. |
| `POD_CRASH` | `TRANSIENT` | 502 | Yes | Session pod terminated unexpectedly | The gateway may auto-retry based on retry policy. |
| `TIMEOUT` | `TRANSIENT` | 504 | Yes | Operation timed out | Retry with backoff. |
| `INTERNAL_ERROR` | `TRANSIENT` | 500 | Yes | Unexpected server error | Retry with backoff. Report if persistent. |
| `UPSTREAM_ERROR` | `UPSTREAM` | 502 | Maybe | External dependency returned an error | Check upstream service status. |
| `POOL_DRAINING` | `TRANSIENT` | 503 | Yes | Target pool is draining | Respect `Retry-After` header. |
| `CIRCUIT_BREAKER_OPEN` | `POLICY` | 503 | No | Operator-declared circuit breaker is active | Wait for operator to close the circuit breaker. |

### Concurrency and ETags

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `ETAG_MISMATCH` | `PERMANENT` | 412 | Yes (after re-GET) | `If-Match` ETag does not match current version | Use `details.currentEtag` or re-GET, then retry. |
| `ETAG_REQUIRED` | `PERMANENT` | 428 | No | `If-Match` header required but missing | Add `If-Match` header from a prior GET response. |
| `IDEMPOTENCY_KEY_REUSED` | `PERMANENT` | 422 | No | Idempotency key reused with different request body | Use a unique idempotency key per distinct request. |

### Delegation Errors

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `DELEGATION_CYCLE_DETECTED` | `PERMANENT` | 400 | No | Delegation would create a circular runtime chain | Choose a different delegation target. |
| `ISOLATION_MONOTONICITY_VIOLATED` | `POLICY` | 403 | No | Target pool's isolation is weaker than parent's | Use a target pool with equal or higher isolation. |
| `INPUT_TOO_LARGE` | `PERMANENT` | 413 | No | Delegation input exceeds `contentPolicy.maxInputSize` | Reduce input size. |
| `CONTENT_POLICY_WEAKENING` | `POLICY` | 403 | No | Child lease removes parent's content policy interceptor | Retain the parent's `interceptorRef`. |
| `DEADLOCK_TIMEOUT` | `TRANSIENT` | 504 | Maybe | Subtree deadlock not resolved in time | Break the deadlock by responding to pending requests or cancelling. |

### Upload and Workspace Errors

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `UPLOAD_TOKEN_EXPIRED` | `PERMANENT` | 401 | No | Upload token TTL has elapsed | Create a new session. |
| `UPLOAD_TOKEN_MISMATCH` | `PERMANENT` | 403 | No | Upload token does not match target session | Use the token from the correct session. |
| `UPLOAD_TOKEN_CONSUMED` | `PERMANENT` | 410 | No | Upload token already used by `FinalizeWorkspace` | Cannot upload after finalization. |
| `DERIVE_ON_LIVE_SESSION` | `PERMANENT` | 409 | No | Derive from non-terminal session without `allowStale: true` | Set `allowStale: true` or wait for session to complete. |
| `DERIVE_SNAPSHOT_UNAVAILABLE` | `TRANSIENT` | 503 | Maybe | Workspace snapshot not found in storage | Retry later or derive from a different source. |

### MCP and Protocol Errors

| Code | Category | HTTP | Retryable | Description | Recommended Action |
|---|---|---|---|---|---|
| `MCP_VERSION_UNSUPPORTED` | `PERMANENT` | 400 | No | Client MCP version is not supported | Upgrade your MCP client. |
| `ELICITATION_NOT_FOUND` | `PERMANENT` | 404 | No | Elicitation ID not found or not owned by this session | Verify the `elicitation_id` belongs to this session and user. |

---

## Retry Strategies

### TRANSIENT Errors: Exponential Backoff

For `TRANSIENT` errors, retry with exponential backoff and jitter:

```
wait = min(base_delay * 2^attempt + random_jitter, max_delay)
```

Recommended parameters:
- Base delay: 1 second
- Max delay: 60 seconds
- Max attempts: 5
- Jitter: 0 to 1 second (random)

Always check and respect the `Retry-After` header when present.

### PERMANENT Errors: Do Not Retry

`PERMANENT` errors indicate the request itself is invalid. Fix the request before retrying:

- `VALIDATION_ERROR`: check `details.fields` for what to fix
- `INVALID_STATE_TRANSITION`: wait for the correct state, then call the endpoint
- `RESOURCE_NOT_FOUND`: verify the resource ID

### POLICY Errors: Check Configuration

`POLICY` errors mean the request is valid but denied by policy. Common resolutions:

- `QUOTA_EXCEEDED`: wait for quota reset or request increase
- `RATE_LIMITED`: respect `Retry-After`, then retry
- `FORBIDDEN`: check role assignments

### UPSTREAM Errors: Check External Services

`UPSTREAM` errors come from external dependencies. Check the upstream service status and retry cautiously.

---

## ETag-Based Optimistic Concurrency

Admin API resources use ETags for safe concurrent updates. The ETag is a quoted decimal version number (e.g., `"3"`).

### How ETags Work

1. **GET** a resource: the response includes an `ETag` header
2. **PUT** the resource: include `If-Match: "3"` with the ETag from step 1
3. If the resource was modified since your GET, you get `412 ETAG_MISMATCH`
4. If successful, you get the updated resource with a new ETag

### If-Match on PUT Requests (Required)

Every admin `PUT` request **must** include an `If-Match` header:

```
PUT /v1/admin/runtimes/my-runtime
Content-Type: application/json
Authorization: Bearer <token>
If-Match: "3"

{
  "image": "my-registry.com/my-runtime:v2.0"
}
```

If `If-Match` is missing, the gateway returns `428 Precondition Required` with code `ETAG_REQUIRED`.

### Handling 412 ETAG_MISMATCH

When you get a `412`, the resource was modified by another client:

```json
{
  "error": {
    "code": "ETAG_MISMATCH",
    "category": "PERMANENT",
    "message": "Resource version mismatch.",
    "retryable": false,
    "details": {
      "currentEtag": "5"
    }
  }
}
```

**Retry pattern:**

1. Use `details.currentEtag` from the error response (avoids a round-trip GET)
2. Or re-GET the specific resource to see the current state
3. Merge your changes with the current state
4. Retry the PUT with the new ETag

```python
# Example: ETag-based update with retry
def update_runtime(client, name, updates, max_retries=3):
    for attempt in range(max_retries):
        # Get current state
        response = client.get(f"/v1/admin/runtimes/{name}")
        response.raise_for_status()
        current = response.json()
        etag = response.headers["etag"]

        # Apply updates
        current.update(updates)

        # Try to update
        response = client.put(
            f"/v1/admin/runtimes/{name}",
            json=current,
            headers={"If-Match": etag},
        )

        if response.status_code == 412:
            # ETag mismatch: resource was modified, retry
            continue
        response.raise_for_status()
        return response.json()

    raise Exception("Max retries exceeded for ETag conflict")
```

---

## Rate Limiting

Lenny applies rate limits per tenant and per user. All responses include rate-limit headers:

| Header | Description |
|---|---|
| `X-RateLimit-Limit` | Maximum requests permitted in the current window |
| `X-RateLimit-Remaining` | Requests remaining in the current window |
| `X-RateLimit-Reset` | UTC epoch seconds when the current window resets |
| `Retry-After` | Seconds to wait before retrying (on `429` and `503` responses) |

Admin API endpoints have separate (higher) rate-limit windows from client-facing endpoints.

When you receive a `429 RATE_LIMITED` response, wait for the number of seconds specified in `Retry-After` before retrying.

---

## State Transition Errors

When you call an endpoint in an invalid state, you get `409 INVALID_STATE_TRANSITION`:

```json
{
  "error": {
    "code": "INVALID_STATE_TRANSITION",
    "category": "PERMANENT",
    "message": "Cannot interrupt a session in state 'suspended'.",
    "retryable": false,
    "details": {
      "currentState": "suspended",
      "allowedStates": ["running"]
    }
  }
}
```

Use `details.currentState` and `details.allowedStates` to determine what to do next.

---

## Validation Error Details

When `code` is `VALIDATION_ERROR`, the `details.fields` array describes each validation failure:

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
          "params": {"min": 1, "max": 10240}
        }
      ]
    }
  }
}
```

Each field entry contains:
- `field`: JSON path to the invalid field
- `message`: human-readable description
- `rule`: validation rule that failed (e.g., `required`, `range`, `pattern`, `enum`)
- `params`: rule-specific parameters (optional)

---

## Examples

### Python -- Error Handling with Retry

```python
import httpx
import time
import random

LENNY_URL = "https://lenny.example.com"
TOKEN = "your-access-token"


class LennyError(Exception):
    def __init__(self, code, category, message, retryable, details=None):
        super().__init__(message)
        self.code = code
        self.category = category
        self.retryable = retryable
        self.details = details or {}


def lenny_request(
    client: httpx.Client,
    method: str,
    path: str,
    max_retries: int = 5,
    **kwargs,
) -> httpx.Response:
    """Make a Lenny API request with automatic retry for TRANSIENT errors."""

    base_delay = 1.0
    max_delay = 60.0

    for attempt in range(max_retries + 1):
        response = client.request(method, f"{LENNY_URL}{path}", **kwargs)

        # Success
        if response.is_success:
            return response

        # Parse error
        try:
            error_body = response.json().get("error", {})
        except Exception:
            error_body = {}

        code = error_body.get("code", "UNKNOWN")
        category = error_body.get("category", "UNKNOWN")
        message = error_body.get("message", response.text)
        retryable = error_body.get("retryable", False)
        details = error_body.get("details", {})

        # Non-retryable: raise immediately
        if not retryable or attempt == max_retries:
            raise LennyError(code, category, message, retryable, details)

        # Calculate wait time
        retry_after = response.headers.get("Retry-After")
        if retry_after:
            wait = float(retry_after)
        else:
            wait = min(base_delay * (2 ** attempt) + random.uniform(0, 1), max_delay)

        print(f"Retrying in {wait:.1f}s (attempt {attempt + 1}/{max_retries}): {code}")
        time.sleep(wait)

    raise LennyError(code, category, message, retryable, details)


# Usage
client = httpx.Client(headers={"Authorization": f"Bearer {TOKEN}"})

try:
    response = lenny_request(client, "POST", "/v1/sessions", json={
        "runtime": "claude-worker",
    })
    session = response.json()
    print(f"Created session: {session['sessionId']}")

except LennyError as e:
    if e.code == "QUOTA_EXCEEDED":
        print(f"Quota exceeded: {e.message}")
    elif e.code == "VALIDATION_ERROR":
        for field in e.details.get("fields", []):
            print(f"  {field['field']}: {field['message']}")
    else:
        print(f"Error [{e.code}]: {e.message}")
```

### TypeScript -- Error Handling with Retry

```typescript
const LENNY_URL = "https://lenny.example.com";
const TOKEN = "your-access-token";

interface LennyError {
  code: string;
  category: "TRANSIENT" | "PERMANENT" | "POLICY" | "UPSTREAM";
  message: string;
  retryable: boolean;
  details: Record<string, any>;
}

async function lennyRequest(
  method: string,
  path: string,
  options: RequestInit = {},
  maxRetries = 5
): Promise<Response> {
  const baseDelay = 1000; // ms
  const maxDelay = 60000; // ms

  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    const response = await fetch(`${LENNY_URL}${path}`, {
      method,
      headers: {
        Authorization: `Bearer ${TOKEN}`,
        "Content-Type": "application/json",
        ...((options.headers as Record<string, string>) ?? {}),
      },
      ...options,
    });

    if (response.ok) return response;

    let error: LennyError;
    try {
      const body = await response.json();
      error = body.error;
    } catch {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    if (!error.retryable || attempt === maxRetries) {
      throw error;
    }

    const retryAfter = response.headers.get("Retry-After");
    const wait = retryAfter
      ? parseFloat(retryAfter) * 1000
      : Math.min(baseDelay * Math.pow(2, attempt) + Math.random() * 1000, maxDelay);

    console.log(
      `Retrying in ${(wait / 1000).toFixed(1)}s ` +
        `(attempt ${attempt + 1}/${maxRetries}): ${error.code}`
    );
    await new Promise((r) => setTimeout(r, wait));
  }

  throw new Error("Unreachable");
}

// Usage
try {
  const response = await lennyRequest("POST", "/v1/sessions", {
    body: JSON.stringify({ runtime: "claude-worker" }),
  });
  const session = await response.json();
  console.log(`Created session: ${session.sessionId}`);
} catch (error: any) {
  if (error.code === "QUOTA_EXCEEDED") {
    console.log(`Quota exceeded: ${error.message}`);
  } else if (error.code === "VALIDATION_ERROR") {
    for (const field of error.details?.fields ?? []) {
      console.log(`  ${field.field}: ${field.message}`);
    }
  } else {
    console.log(`Error [${error.code}]: ${error.message}`);
  }
}
```
