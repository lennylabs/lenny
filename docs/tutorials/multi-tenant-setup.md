---
layout: default
title: "Multi-Tenant Setup"
parent: Tutorials
nav_order: 7
---

# Multi-Tenant Setup

**Persona:** Operator | **Difficulty:** Advanced

Lenny separates tenants at the database level using Postgres row-level security (RLS), and enforces per-tenant quotas, rate limits, and access controls.

In this tutorial you will configure an OIDC provider for authentication, register tenants, set per-tenant quotas, configure pool access, verify isolation, and set up metering for billing.

## Prerequisites

- Lenny deployed to a Kubernetes cluster (see [Deploy to Kubernetes](deploy-to-cluster))
- An OIDC-compliant identity provider (Keycloak, Auth0, Okta, Azure AD, etc.)
- `lenny-ctl` CLI installed
- Admin access to the Lenny gateway

---

## Concepts: How Tenant Isolation Works

### Row-Level Security (RLS)

Every tenant-scoped table in Postgres has an RLS policy:

```sql
CREATE POLICY tenant_isolation ON sessions
    USING (tenant_id = current_setting('app.current_tenant', false));
```

Before every query, the gateway runs:

```sql
SET LOCAL app.current_tenant = '<tenant_id>';
```

A query from Tenant A cannot read Tenant B's rows: the database enforces isolation regardless of application-layer bugs.

### The Three Layers

1. **Database (RLS):** Rows are filtered by `tenant_id` at the Postgres level
2. **Gateway (policy):** Quotas, rate limits, and access controls are enforced per-tenant
3. **Runtime (pools):** Pool access is controlled via `pool_tenant_access` join tables

---

## Step 1: Configure the OIDC Provider

Lenny extracts tenant identity from OIDC tokens. Configure your identity provider to include a tenant claim in the ID token or access token.

### Keycloak Example

In Keycloak, create a client scope mapper that adds the tenant ID:

1. Create a client scope named `lenny-tenant`
2. Add a mapper:
   - **Name:** `tenant_id`
   - **Mapper type:** User Attribute
   - **User attribute:** `tenant_id`
   - **Token Claim Name:** `tenant_id`
   - **Claim JSON Type:** String
   - **Add to ID token:** Yes
   - **Add to access token:** Yes

### Lenny Gateway Configuration

In your Helm `values.yaml`, configure the OIDC settings:

```yaml
auth:
  oidc:
    issuer: "https://keycloak.example.com/realms/lenny"
    clientId: "lenny-gateway"
    # The JWT claim that contains the tenant ID
    tenantClaim: "tenant_id"
    # The JWT claim that contains the user's role (optional)
    roleClaim: "lenny_role"
    # JWKS endpoint for token validation (auto-discovered from issuer if omitted)
    jwksUri: ""
    # Audience validation
    audience: "lenny-gateway"
```

Apply the configuration:

```bash
helm upgrade lenny lenny/lenny \
  -n lenny-system \
  -f values.yaml \
  --wait
```

### Token Structure

A valid JWT token for Lenny must include:

```json
{
  "iss": "https://keycloak.example.com/realms/lenny",
  "sub": "user_12345",
  "aud": "lenny-gateway",
  "tenant_id": "tenant_acme",
  "lenny_role": "tenant-admin",
  "exp": 1717430400
}
```

---

## Step 2: Register Tenants

Tenants are a top-level resource. Each tenant has its own row-level-security partition, quota configuration, and pool access grants.

```bash
TOKEN="your-platform-admin-token"

# Register Tenant: Acme Corp
curl -s -X POST http://localhost:8080/v1/admin/tenants \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "acme",
    "displayName": "Acme Corporation",
    "metadata": {
      "plan": "enterprise",
      "contact": "admin@acme.example.com"
    }
  }' | jq .
```

Expected response:

```json
{
  "id": "tn_01J5ACME001",
  "name": "acme",
  "displayName": "Acme Corporation",
  "createdAt": "2026-04-09T10:00:00Z",
  "metadata": {
    "plan": "enterprise",
    "contact": "admin@acme.example.com"
  }
}
```

Register a second tenant for isolation testing:

```bash
curl -s -X POST http://localhost:8080/v1/admin/tenants \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "globex",
    "displayName": "Globex Industries",
    "metadata": {
      "plan": "starter",
      "contact": "admin@globex.example.com"
    }
  }' | jq .
```

The handler automatically creates a per-tenant Postgres billing sequence:

```sql
CREATE SEQUENCE IF NOT EXISTS billing_seq_tn_01J5ACME001
    START WITH 1 INCREMENT BY 1 NO CYCLE;
```

---

## Step 3: Set Per-Tenant Quotas

Quotas control token budgets, concurrency limits, and rate limits per tenant. They are embedded in the tenant record.

```bash
ACME_ID="tn_01J5ACME001"

# Get the current tenant record (need If-Match for update)
ETAG=$(curl -sI "http://localhost:8080/v1/admin/tenants/${ACME_ID}" \
  -H "Authorization: Bearer ${TOKEN}" | grep -i etag | awk '{print $2}' | tr -d '\r')

# Update with quotas
curl -s -X PUT "http://localhost:8080/v1/admin/tenants/${ACME_ID}" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "If-Match: ${ETAG}" \
  -d '{
    "name": "acme",
    "displayName": "Acme Corporation",
    "quotas": {
      "maxConcurrentSessions": 50,
      "maxSessionsPerDay": 500,
      "maxTokensPerDay": 5000000,
      "maxTokensPerMonth": 100000000,
      "maxStorageBytes": 10737418240,
      "rateLimits": {
        "requestsPerMinute": 60,
        "requestsPerHour": 1000
      }
    },
    "metadata": {
      "plan": "enterprise",
      "contact": "admin@acme.example.com"
    }
  }' | jq .
```

### Quota Fields

| Field | Description | Default |
|-------|-------------|---------|
| `maxConcurrentSessions` | Maximum sessions running simultaneously | Unlimited |
| `maxSessionsPerDay` | Daily session creation cap | Unlimited |
| `maxTokensPerDay` | Daily LLM token budget | Unlimited |
| `maxTokensPerMonth` | Monthly LLM token budget | Unlimited |
| `maxStorageBytes` | Total workspace + artifact storage | Unlimited |
| `rateLimits.requestsPerMinute` | API request rate limit | 120 |
| `rateLimits.requestsPerHour` | Hourly API request cap | 10000 |

Set different quotas for the starter-plan tenant:

```bash
GLOBEX_ID="tn_01J5GLOB001"
ETAG=$(curl -sI "http://localhost:8080/v1/admin/tenants/${GLOBEX_ID}" \
  -H "Authorization: Bearer ${TOKEN}" | grep -i etag | awk '{print $2}' | tr -d '\r')

curl -s -X PUT "http://localhost:8080/v1/admin/tenants/${GLOBEX_ID}" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -H "If-Match: ${ETAG}" \
  -d '{
    "name": "globex",
    "displayName": "Globex Industries",
    "quotas": {
      "maxConcurrentSessions": 5,
      "maxSessionsPerDay": 50,
      "maxTokensPerDay": 500000,
      "maxTokensPerMonth": 5000000,
      "maxStorageBytes": 1073741824,
      "rateLimits": {
        "requestsPerMinute": 20,
        "requestsPerHour": 200
      }
    },
    "metadata": {
      "plan": "starter",
      "contact": "admin@globex.example.com"
    }
  }' | jq .
```

---

## Step 4: Configure Per-Tenant Pool Access

Runtimes and pools are platform-global resources. Tenant access is granted via join tables. A tenant can only see and use runtimes and pools explicitly granted to them.

```bash
# Grant Acme access to the echo runtime
curl -s -X POST "http://localhost:8080/v1/admin/runtimes/echo/tenant-access" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"tenantId\": \"${ACME_ID}\"}" | jq .

# Grant Acme access to the default pool
curl -s -X POST "http://localhost:8080/v1/admin/pools/default-pool/tenant-access" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"tenantId\": \"${ACME_ID}\"}" | jq .

# Grant Globex access to the same runtime but a different pool
curl -s -X POST "http://localhost:8080/v1/admin/runtimes/echo/tenant-access" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"tenantId\": \"${GLOBEX_ID}\"}" | jq .

curl -s -X POST "http://localhost:8080/v1/admin/pools/starter-pool/tenant-access" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d "{\"tenantId\": \"${GLOBEX_ID}\"}" | jq .
```

### Verify Access Grants

```bash
# List which tenants have access to the echo runtime
curl -s "http://localhost:8080/v1/admin/runtimes/echo/tenant-access" \
  -H "Authorization: Bearer ${TOKEN}" | jq .
```

Expected output:

```json
[
  {
    "tenantId": "tn_01J5ACME001",
    "tenantName": "acme",
    "grantedAt": "2026-04-09T10:00:00Z",
    "grantedBy": "lenny-admin"
  },
  {
    "tenantId": "tn_01J5GLOB001",
    "tenantName": "globex",
    "grantedAt": "2026-04-09T10:01:00Z",
    "grantedBy": "lenny-admin"
  }
]
```

---

## Step 5: Verify Tenant Isolation

Verify that cross-tenant reads return zero rows.

### Test 1: Session Visibility

Create a session as Acme, then try to read it as Globex:

```bash
# Create a session as Acme
ACME_TOKEN="jwt-token-with-tenant_id=tn_01J5ACME001"

ACME_SESSION=$(curl -s -X POST http://localhost:8080/v1/sessions/start \
  -H "Authorization: Bearer ${ACME_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "echo",
    "input": [{"type": "text", "inline": "Acme secret data"}]
  }' | jq -r '.session_id')

echo "Acme session: ${ACME_SESSION}"

# Try to read it as Globex
GLOBEX_TOKEN="jwt-token-with-tenant_id=tn_01J5GLOB001"

curl -s "http://localhost:8080/v1/sessions/${ACME_SESSION}" \
  -H "Authorization: Bearer ${GLOBEX_TOKEN}" | jq .
```

Expected response (Globex cannot see Acme's session):

```json
{
  "error": {
    "code": "RESOURCE_NOT_FOUND",
    "message": "Session not found"
  }
}
```

The RLS policy filters the row before the application layer even sees it. The gateway returns 404 (not 403) to prevent leaking the existence of sessions belonging to other tenants.

### Test 2: Runtime Discovery Isolation

```bash
# Globex should only see runtimes they have access to
curl -s "http://localhost:8080/v1/runtimes" \
  -H "Authorization: Bearer ${GLOBEX_TOKEN}" | jq '.[].name'
```

If Globex has access only to `echo`, they see only `echo`, even if other runtimes exist on the platform.

### Test 3: Cross-Tenant Message Rejection

If an agent tries to send a message to a session belonging to a different tenant:

```
CROSS_TENANT_MESSAGE_DENIED: messages targeting a session belonging to
a different tenant are rejected regardless of messaging scope or tree structure.
```

This validation happens before scope evaluation and rate limiting.

---

## Step 6: Monitor Per-Tenant Usage

### Usage Report

```bash
# Get Acme's usage for the current day
curl -s "http://localhost:8080/v1/usage?tenantId=${ACME_ID}&window=day" \
  -H "Authorization: Bearer ${TOKEN}" | jq .
```

Expected response:

```json
{
  "tenantId": "tn_01J5ACME001",
  "window": {
    "start": "2026-04-09T00:00:00Z",
    "end": "2026-04-09T23:59:59Z"
  },
  "sessions": {
    "created": 15,
    "completed": 12,
    "failed": 1,
    "active": 2
  },
  "tokens": {
    "consumed": 125000,
    "budgetRemaining": 4875000,
    "monthlyConsumed": 2500000,
    "monthlyBudgetRemaining": 97500000
  },
  "storage": {
    "usedBytes": 52428800,
    "budgetBytes": 10737418240
  },
  "quotaUtilization": {
    "concurrentSessions": "4.0%",
    "dailySessions": "3.0%",
    "dailyTokens": "2.5%",
    "monthlyTokens": "2.5%",
    "storage": "0.5%"
  }
}
```

### Compare Across Tenants (Platform Admin)

```bash
# Get usage for all tenants
curl -s "http://localhost:8080/v1/usage?window=day" \
  -H "Authorization: Bearer ${TOKEN}" | jq '.[] | {name: .tenantName, sessions: .sessions.created, tokens: .tokens.consumed}'
```

---

## Step 7: Metering Events for Billing

Lenny emits fine-grained metering events for every billable action. These events power billing systems.

```bash
# Get metering events for Acme
curl -s "http://localhost:8080/v1/metering/events?tenantId=${ACME_ID}&limit=10" \
  -H "Authorization: Bearer ${TOKEN}" | jq .
```

Expected response:

```json
{
  "events": [
    {
      "eventId": "mtr_001",
      "tenantId": "tn_01J5ACME001",
      "type": "session_completed",
      "timestamp": "2026-04-09T10:35:00Z",
      "dimensions": {
        "runtime": "echo",
        "pool": "default-pool",
        "isolationProfile": "runc"
      },
      "measures": {
        "durationSeconds": 300,
        "tokensConsumed": 15000,
        "workspaceBytes": 1048576
      }
    },
    {
      "eventId": "mtr_002",
      "tenantId": "tn_01J5ACME001",
      "type": "token_usage",
      "timestamp": "2026-04-09T10:30:15Z",
      "dimensions": {
        "runtime": "echo",
        "provider": "anthropic"
      },
      "measures": {
        "inputTokens": 500,
        "outputTokens": 1200,
        "totalTokens": 1700
      }
    }
  ],
  "pagination": {
    "hasMore": true,
    "cursor": "mtr_002"
  }
}
```

### Metering Event Types

| Event Type | Triggers When | Key Measures |
|------------|---------------|--------------|
| `session_created` | Session enters `running` state | - |
| `session_completed` | Session reaches terminal state | `durationSeconds`, `tokensConsumed`, `workspaceBytes` |
| `token_usage` | LLM tokens consumed (periodic) | `inputTokens`, `outputTokens`, `totalTokens` |
| `storage_usage` | Workspace or artifact storage changes | `bytes`, `operation` |
| `delegation_created` | Child session spawned | `parentSessionId`, `depth` |

### Billing Integration Pattern

```python
import requests
import time

def sync_metering_events(gateway_url, token, tenant_id, billing_system):
    """
    Poll Lenny's metering events and forward to your billing system.
    In production, use webhooks (callbackUrl) instead of polling.
    """
    cursor = None

    while True:
        params = {"tenantId": tenant_id, "limit": 100}
        if cursor:
            params["cursor"] = cursor

        resp = requests.get(
            f"{gateway_url}/v1/metering/events",
            headers={"Authorization": f"Bearer {token}"},
            params=params,
        )
        resp.raise_for_status()
        data = resp.json()

        for event in data["events"]:
            billing_system.record_event(
                tenant_id=event["tenantId"],
                event_type=event["type"],
                timestamp=event["timestamp"],
                measures=event["measures"],
            )

        if not data["pagination"]["hasMore"]:
            break
        cursor = data["pagination"]["cursor"]
```

---

## Security Deep-Dive: How RLS Works

### The RLS Pipeline

Every database query in Lenny follows this sequence:

```
1. Gateway receives request with JWT
2. Extract tenant_id from JWT claim
3. Begin transaction
4. SET LOCAL app.current_tenant = 'tn_01J5ACME001'
5. Execute query (RLS policy filters rows)
6. Commit transaction
```

The `SET LOCAL` ensures the setting is scoped to the current transaction only. It does not leak across connections.

### PgBouncer Compatibility

Lenny requires PgBouncer in `transaction` mode (not `session` mode) because `SET LOCAL` is transaction-scoped. In session mode, a `SET` from one client could leak to another client that reuses the same backend connection.

Additionally, PgBouncer is configured with a `connect_query` sentinel:

```ini
connect_query = SET app.current_tenant = '__unset__'
```

Every new connection starts with the tenant set to `__unset__`. If the application code forgets to call `SET LOCAL`, the RLS policy evaluates against `__unset__`, which matches no rows. This is fail-closed behavior.

### Cloud-Managed Pooler Defense

Cloud-managed connection poolers (e.g., RDS Proxy, Cloud SQL Auth Proxy) do not support `connect_query`. For these environments, Lenny uses a per-transaction validation trigger:

```sql
CREATE OR REPLACE FUNCTION lenny_tenant_guard()
RETURNS TRIGGER AS $$
BEGIN
    IF current_setting('app.current_tenant', true) IS NULL
       OR current_setting('app.current_tenant', true) = '__unset__'
       OR current_setting('app.current_tenant', true) = '' THEN
        RAISE EXCEPTION 'tenant context not set';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
```

This trigger fires on every INSERT and UPDATE to tenant-scoped tables, rejecting operations where the tenant context was not properly set.

---

## Testing Isolation: Integration Test Approach

### Automated Test

An integration test that verifies tenant isolation:

```python
import requests
import pytest

GATEWAY = "http://localhost:8080"

@pytest.fixture
def acme_token():
    """JWT token for Acme tenant."""
    return get_test_token(tenant_id="tn_acme_test")

@pytest.fixture
def globex_token():
    """JWT token for Globex tenant."""
    return get_test_token(tenant_id="tn_globex_test")

def test_cross_tenant_session_invisible(acme_token, globex_token):
    """Sessions created by one tenant must be invisible to another."""

    # Acme creates a session
    resp = requests.post(f"{GATEWAY}/v1/sessions/start",
        headers={"Authorization": f"Bearer {acme_token}"},
        json={"runtime": "echo", "input": [{"type": "text", "inline": "secret"}]})
    assert resp.status_code == 200
    session_id = resp.json()["session_id"]

    # Globex tries to read it
    resp = requests.get(f"{GATEWAY}/v1/sessions/{session_id}",
        headers={"Authorization": f"Bearer {globex_token}"})
    assert resp.status_code == 404

    # Globex tries to send a message
    resp = requests.post(f"{GATEWAY}/v1/sessions/{session_id}/messages",
        headers={"Authorization": f"Bearer {globex_token}"},
        json={"input": [{"type": "text", "inline": "hack"}]})
    assert resp.status_code == 404

    # Globex lists sessions; Acme's session must not appear
    resp = requests.get(f"{GATEWAY}/v1/sessions",
        headers={"Authorization": f"Bearer {globex_token}"})
    assert resp.status_code == 200
    session_ids = [s["session_id"] for s in resp.json().get("sessions", [])]
    assert session_id not in session_ids

def test_cross_tenant_artifact_invisible(acme_token, globex_token):
    """Artifacts from one tenant must not be downloadable by another."""

    # Acme creates a session with a workspace file
    resp = requests.post(f"{GATEWAY}/v1/sessions",
        headers={"Authorization": f"Bearer {acme_token}"},
        json={"runtime": "echo"})
    session_id = resp.json()["session_id"]
    upload_token = resp.json()["uploadToken"]

    # Upload a file
    requests.post(f"{GATEWAY}/v1/sessions/{session_id}/upload",
        headers={"Authorization": f"UploadToken {upload_token}"},
        files={"files": ("secret.txt", b"Acme confidential data")})

    # Globex tries to download it
    resp = requests.get(f"{GATEWAY}/v1/sessions/{session_id}/artifacts/secret.txt",
        headers={"Authorization": f"Bearer {globex_token}"})
    assert resp.status_code == 404

def test_cross_tenant_transcript_invisible(acme_token, globex_token):
    """Transcripts from one tenant must not be readable by another."""

    # Create and interact with a session as Acme
    resp = requests.post(f"{GATEWAY}/v1/sessions/start",
        headers={"Authorization": f"Bearer {acme_token}"},
        json={"runtime": "echo", "input": [{"type": "text", "inline": "confidential"}]})
    session_id = resp.json()["session_id"]

    # Globex tries to read the transcript
    resp = requests.get(f"{GATEWAY}/v1/sessions/{session_id}/transcript",
        headers={"Authorization": f"Bearer {globex_token}"})
    assert resp.status_code == 404

def test_quota_enforcement_per_tenant(acme_token, globex_token):
    """Quotas must be enforced independently per tenant."""

    # Assume Globex has maxConcurrentSessions=5
    sessions = []
    for i in range(5):
        resp = requests.post(f"{GATEWAY}/v1/sessions/start",
            headers={"Authorization": f"Bearer {globex_token}"},
            json={"runtime": "echo", "input": [{"type": "text", "inline": f"session {i}"}]})
        assert resp.status_code == 200
        sessions.append(resp.json()["session_id"])

    # 6th session should be rejected
    resp = requests.post(f"{GATEWAY}/v1/sessions/start",
        headers={"Authorization": f"Bearer {globex_token}"},
        json={"runtime": "echo", "input": [{"type": "text", "inline": "overflow"}]})
    assert resp.status_code == 429  # or 503

    # Acme should still be able to create sessions (independent quota)
    resp = requests.post(f"{GATEWAY}/v1/sessions/start",
        headers={"Authorization": f"Bearer {acme_token}"},
        json={"runtime": "echo", "input": [{"type": "text", "inline": "acme still works"}]})
    assert resp.status_code == 200

    # Clean up
    for sid in sessions:
        requests.post(f"{GATEWAY}/v1/sessions/{sid}/terminate",
            headers={"Authorization": f"Bearer {globex_token}"}, json={})
```

Run the tests:

```bash
pytest tests/integration/test_tenant_isolation.py -v
```

---

## Summary

In this tutorial you:

1. Configured an OIDC provider to include tenant claims in JWT tokens
2. Registered two tenants with different plans and metadata
3. Set per-tenant quotas (concurrency, tokens, storage, rate limits)
4. Configured per-tenant pool access via join tables
5. Verified tenant isolation (cross-tenant reads return 404)
6. Monitored per-tenant usage via the usage API
7. Retrieved metering events for billing integration
8. Understood how RLS, PgBouncer `connect_query`, and cloud pooler defenses work together
9. Wrote integration tests that verify tenant isolation

### Tenant Isolation Checklist

- [ ] PgBouncer `pool_mode = transaction` confirmed by preflight
- [ ] PgBouncer `connect_query` contains tenant sentinel
- [ ] All tenant-scoped tables have RLS policies enabled
- [ ] OIDC provider includes `tenant_id` claim in tokens
- [ ] Runtime and pool access granted via join tables (not implicit)
- [ ] Cross-tenant session read returns 404
- [ ] Cross-tenant message delivery returns `CROSS_TENANT_MESSAGE_DENIED`
- [ ] Per-tenant quotas set and enforced
- [ ] Metering events flowing to billing system
- [ ] Integration tests passing in CI

---

## Next Steps

- [Deploy to Kubernetes](deploy-to-cluster): review production considerations
- [Recursive Delegation](recursive-delegation): how delegation works within tenant boundaries
