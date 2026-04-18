---
layout: default
title: Authentication
parent: "Client Guide"
nav_order: 1
---

# Authentication

Lenny uses **OIDC/OAuth 2.1** (OpenID Connect, layered on OAuth 2.1) for client authentication. Clients obtain a bearer token from your identity provider and include it on every API request. This page covers how to obtain tokens, register LLM provider credentials, and handle token lifecycle.

---

## Authentication Flow Overview

```
Client                     Identity Provider                    Lenny Gateway
  │                                 │                                │
  │──── Authorization Request ─────>│                                │
  │<─── Authorization Code ─────────│                                │
  │──── Token Request (code) ──────>│                                │
  │<─── ID Token + Access Token ────│                                │
  │                                 │                                │
  │──── API Request ────────────────────────────────────────────────>│
  │     Authorization: Bearer <access_token>                        │
  │                                 │                                │
  │<─── Response ───────────────────────────────────────────────────│
```

### Token Acquisition

Lenny supports two OAuth 2.1 grant types:

#### Authorization Code Flow (interactive users)

For browser-based applications and interactive CLIs:

1. Redirect the user to the identity provider's authorization endpoint
2. Receive the authorization code on your callback URL
3. Exchange the code for tokens at the token endpoint
4. Use the access token as a bearer token in API requests

#### Client Credentials Flow (automated clients)

For CI/CD pipelines, service accounts, and server-to-server integration:

1. Register a client with your identity provider
2. Exchange `client_id` and `client_secret` directly for an access token
3. Use the access token as a bearer token in API requests

---

## Bearer Token Usage

Include the access token in the `Authorization` header of every request:

```
Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9...
```

The gateway validates the token signature, checks expiry, and extracts:

- `user_id` -- the authenticated user
- `tenant_id` -- the tenant context (extracted from the identity-provider claim configured via `auth.tenantIdClaim`, default: `tenant_id`)
- Role claims (e.g., `lenny_role`) -- determines authorization level

### Tenant Context

In multi-tenant deployments, every request is scoped to a tenant. The tenant is extracted from the identity token:

| Condition | Behavior |
|---|---|
| Single-tenant deployment (`auth.multiTenant: false`) | Tenant claim is ignored; all requests use the built-in `default` tenant |
| Claim present and tenant registered | Request proceeds with the extracted `tenant_id` |
| Claim absent or empty | `401 Unauthorized` with code `TENANT_CLAIM_MISSING` |
| Claim present but tenant not registered | `403 Forbidden` with code `TENANT_NOT_FOUND` |

There is no silent fallback to a default tenant in multi-tenant mode.

### Token Refresh and Expiry

Access tokens have a limited lifetime (configured by your identity provider, typically 1 hour). When a token expires:

- The gateway returns `401 UNAUTHORIZED`
- Your application should use the refresh token to obtain a new access token
- Retry the failed request with the new token

Best practice: refresh proactively before expiry. Most identity providers include an `expires_in` field in the token response.

### Token Rotation and Exchange (`/v1/oauth/token`)

For token lifecycle operations inside Lenny — rotating an admin token, minting a narrowed scoped token for an agent service account, or any internal delegation child-token issuance — Lenny exposes the canonical OAuth token endpoint `POST /v1/oauth/token` compliant with [RFC 6749 §5](https://www.rfc-editor.org/rfc/rfc6749#section-5) and [RFC 8693 (Token Exchange)](https://www.rfc-editor.org/rfc/rfc8693).

**Example — rotate an admin token:**

```http
POST /v1/oauth/token
Content-Type: application/x-www-form-urlencoded
Authorization: Bearer <current_admin_token>

grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Atoken-exchange
&subject_token=<current_admin_token>
&subject_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Ajwt
&requested_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Ajwt
```

**Example — narrow scope for an automation agent:**

```http
POST /v1/oauth/token
Content-Type: application/x-www-form-urlencoded
Authorization: Bearer <agent_token>

grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Atoken-exchange
&subject_token=<agent_token>
&subject_token_type=urn%3Aietf%3Aparams%3Aoauth%3Atoken-type%3Ajwt
&scope=tools%3Adiagnostics%3Aread+tools%3Apools%3Aread
```

**Response:**

```json
{
  "access_token": "eyJhbGciOi...",
  "issued_token_type": "urn:ietf:params:oauth:token-type:jwt",
  "token_type": "Bearer",
  "expires_in": 3600
}
```

**Scope narrowing is monotonic.** An exchange may only request a `scope` that is a subset of the `subject_token`'s existing scope. Broadening is rejected with `invalid_scope`. See [Security -> Credential Flow](../operator-guide/security.md) for the full claim-mapping table and scope-narrowing rules.

The CLI command `lenny-ctl admin users rotate-token --user <name>` is a convenience wrapper that calls this endpoint internally.

---

## Credential Registration for LLM Providers

Lenny sessions need credentials to access LLM providers (Anthropic, AWS Bedrock, Vertex AI, Azure OpenAI, etc.). There are two flows for providing these credentials.

### Pre-Authorized Flow (Admin Manages Credential Pools)

The deployer or tenant admin registers credential pools via the admin API. Sessions are automatically assigned credentials from the pool at creation time. This is the recommended approach for production deployments.

Users do not need to register individual credentials -- the platform handles assignment, rotation, and revocation.

### On-Demand Flow (Per-User Credentials)

Users register their own credentials, which are used when no pool credential is available or when the credential policy specifies user-sourced credentials.

#### Register a Credential

```
POST /v1/credentials
Content-Type: application/json
Authorization: Bearer <access_token>

{
  "provider": "anthropic_direct",
  "secretMaterial": {
    "apiKey": "sk-ant-..."
  },
  "label": "My Anthropic Key"
}
```

**Response** (`201 Created`):

```json
{
  "credentialRef": "cred_abc123",
  "provider": "anthropic_direct",
  "label": "My Anthropic Key",
  "createdAt": "2026-01-15T10:30:00Z",
  "status": "active"
}
```

One credential per provider is allowed. Re-registering for the same provider replaces the existing credential.

#### List Credentials

```
GET /v1/credentials
Authorization: Bearer <access_token>
```

**Response** (`200 OK`):

```json
{
  "items": [
    {
      "credentialRef": "cred_abc123",
      "provider": "anthropic_direct",
      "label": "My Anthropic Key",
      "createdAt": "2026-01-15T10:30:00Z",
      "status": "active"
    }
  ],
  "cursor": null,
  "hasMore": false
}
```

No secret material is ever returned in GET responses.

#### Rotate a Credential

```
PUT /v1/credentials/cred_abc123
Content-Type: application/json
Authorization: Bearer <access_token>

{
  "secretMaterial": {
    "apiKey": "sk-ant-new-key..."
  },
  "label": "My Anthropic Key (rotated)"
}
```

**Response** (`200 OK`):

Active leases backed by this credential are immediately rotated -- running sessions receive the new credential without interruption.

#### Revoke a Credential

```
POST /v1/credentials/cred_abc123/revoke
Authorization: Bearer <access_token>
```

**Response** (`200 OK`):

Revocation immediately invalidates all active leases backed by this credential. Running sessions that depend on this credential will be terminated.

#### Remove a Credential

```
DELETE /v1/credentials/cred_abc123
Authorization: Bearer <access_token>
```

**Response** (`204 No Content`):

Removes the credential record. Active session leases are unaffected (they continue with the previously assigned credential until the session ends).

---

## Credential Policy and Delivery

### Credential Policy

Tenants configure a `CredentialPolicy` that controls how credentials are sourced for sessions. The key fields are:

- **`preferredSource`** -- determines the credential resolution order: `pool` (pool-only), `user` (user credentials only), `prefer-user-then-pool` (try user first, fall back to pool), or `prefer-pool-then-user` (try pool first, fall back to user).
- **`userCredentialsEnabled`** -- when `false`, user-scoped credentials registered via `POST /v1/credentials` are ignored regardless of the `preferredSource` setting. When `true`, the gateway resolves user-scoped credentials from the credential store according to the fallback configuration.

Per-session overrides can be passed at session creation time via the `credentialPolicy` field, but they can only restrict the tenant policy, never expand it.

### Delivery Modes

Credentials reach agent pods in one of two ways:

| Mode | How It Works | Security Implications |
|---|---|---|
| **Proxy** | The gateway injects credentials into upstream LLM requests on behalf of the pod. Pods receive a lease token and a proxy URL -- they never see the raw API key. | More secure. Recommended for most deployments. |
| **Direct** | The credential is written to a file on the pod filesystem. The runtime reads the file and calls LLM APIs directly. | Less secure (credential is present on the pod). Required for runtimes that call LLM APIs directly and cannot use the gateway proxy. |

The delivery mode is configured per credential pool by the deployer. In regulated environments, consider requiring explicit admin approval for pools configured with `deliveryMode: direct`.

### Pod-bound Lease Tokens

In multi-tenant deployments, proxy-mode lease tokens are cryptographically bound to the pod that requested them. Each pod has a unique cryptographic identity issued at pod startup (implemented as a SPIFFE identity), and on every LLM proxy request the gateway checks that the requesting pod's identity matches the one recorded when the credential was assigned. A mismatch is rejected with `LEASE_SPIFFE_MISMATCH`, so a lease token lifted from one pod cannot be replayed by another. This binding is enforced on the gateway side -- no protocol change is required on the pod side.

---

## Roles and Permissions (RBAC)

Lenny uses role-based access control (RBAC) to decide what an authenticated user can do. Five built-in roles are defined:

| Role | Access Level |
|---|---|
| `platform-admin` | Full access across all tenants |
| `tenant-admin` | Full access scoped to own tenant |
| `tenant-viewer` | Read-only access scoped to own tenant |
| `billing-viewer` | Usage and metering data for own tenant |
| `user` | Create and manage own sessions |

Roles are conveyed via identity-provider claims (e.g., a `lenny_role` claim) or via platform-managed user-to-role mappings. When both are present, the platform-managed mapping takes precedence.

---

## Examples

### curl -- Complete Auth Flow

```bash
# Step 1: Get an access token via client credentials grant
TOKEN=$(curl -s -X POST https://your-identity-provider.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=client_credentials" \
  -d "client_id=YOUR_CLIENT_ID" \
  -d "client_secret=YOUR_CLIENT_SECRET" \
  -d "scope=openid profile" \
  | jq -r '.access_token')

echo "Access token: ${TOKEN:0:20}..."

# Step 2: Verify the token works by listing runtimes
curl -s https://lenny.example.com/v1/runtimes \
  -H "Authorization: Bearer $TOKEN" \
  | jq .

# Step 3: Register a user credential (optional, for on-demand flow)
curl -s -X POST https://lenny.example.com/v1/credentials \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "anthropic_direct",
    "secretMaterial": {"apiKey": "sk-ant-..."},
    "label": "My API Key"
  }' | jq .
```

### Python -- Token Acquisition and API Call

```python
import httpx

# Client credentials flow
token_response = httpx.post(
    "https://your-identity-provider.com/oauth/token",
    data={
        "grant_type": "client_credentials",
        "client_id": "YOUR_CLIENT_ID",
        "client_secret": "YOUR_CLIENT_SECRET",
        "scope": "openid profile",
    },
)
token_response.raise_for_status()
access_token = token_response.json()["access_token"]

# Use the token to call Lenny
client = httpx.Client(
    base_url="https://lenny.example.com",
    headers={"Authorization": f"Bearer {access_token}"},
)

# List available runtimes
runtimes = client.get("/v1/runtimes").json()
for rt in runtimes["items"]:
    print(f"Runtime: {rt['name']} ({rt['type']})")

# Register a credential
client.post("/v1/credentials", json={
    "provider": "anthropic_direct",
    "secretMaterial": {"apiKey": "sk-ant-..."},
    "label": "My API Key",
})
```

### TypeScript -- Token Acquisition and API Call

```typescript
// Client credentials flow
const tokenResponse = await fetch(
  "https://your-identity-provider.com/oauth/token",
  {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "client_credentials",
      client_id: "YOUR_CLIENT_ID",
      client_secret: "YOUR_CLIENT_SECRET",
      scope: "openid profile",
    }),
  }
);
const { access_token } = await tokenResponse.json();

// Use the token to call Lenny
const headers = { Authorization: `Bearer ${access_token}` };

const runtimesResponse = await fetch(
  "https://lenny.example.com/v1/runtimes",
  { headers }
);
const runtimes = await runtimesResponse.json();
console.log("Available runtimes:", runtimes.items.map((r: any) => r.name));

// Register a credential
await fetch("https://lenny.example.com/v1/credentials", {
  method: "POST",
  headers: { ...headers, "Content-Type": "application/json" },
  body: JSON.stringify({
    provider: "anthropic_direct",
    secretMaterial: { apiKey: "sk-ant-..." },
    label: "My API Key",
  }),
});
```
