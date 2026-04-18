---
layout: default
title: "User Credentials"
parent: Tutorials
nav_order: 12
---

# User Credentials

**Persona:** Client Developer | **Difficulty:** Beginner

Lenny supports a "bring your own API key" workflow where users register their own LLM provider credentials. When a session is created, the gateway checks for a user credential before falling back to the platform credential pool. This tutorial walks through the full lifecycle: register, use, rotate, and revoke.

## Prerequisites

- Lenny running locally via `lenny up` (recommended; see [Quickstart](../getting-started/quickstart)) or via `make run` / `docker compose up` for contributor dev loops
- An API key for an LLM provider (e.g., Anthropic, OpenAI)
- Familiarity with [Your First Session](first-session)
- curl and jq installed

---

## Step 1: Register a User Credential

Register your API key with the gateway. The credential is scoped to your authenticated identity and a specific provider.

```bash
curl -s -X POST http://localhost:8080/v1/credentials \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "anthropic",
    "secret": "sk-ant-api03-...",
    "metadata": {
      "label": "personal-key",
      "tier": "pro"
    }
  }' | jq .
```

Expected response:

```json
{
  "credentialRef": "cred_01...",
  "provider": "anthropic",
  "metadata": {
    "label": "personal-key",
    "tier": "pro"
  },
  "createdAt": "2026-04-12T10:00:00Z"
}
```

One credential per provider: if you register a second credential for the same provider, it replaces the first. The `secret` field is write-only; it is never returned in API responses.

---

## Step 2: Create a Session Using Your Credential

When you create a session with a runtime that uses the `anthropic` provider, the gateway automatically selects your registered credential instead of the platform pool:

```bash
curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "claude-worker",
    "labels": {"credential-source": "user"}
  }' | jq .
```

The session proceeds through the normal lifecycle. The gateway injects your API key into LLM proxy requests; the agent pod never sees the raw key. The gateway talks to LLM providers on behalf of agent pods: it reads your API key from its in-memory Token Service cache, translates the request to the upstream provider's wire format, and forwards it. Your API key only lives in the gateway's memory, so agent pods do not hold real credentials and rotation happens without restarting pods. See [LLM Proxy security](../operator-guide/security.md) for the full credential flow.

You can verify which credential source was used by checking the session metadata:

```bash
curl -s "http://localhost:8080/v1/sessions/${SESSION_ID}" \
  -H "Authorization: Bearer $USER_TOKEN" | jq '.credentialSource'
```

Returns `"user"` when your registered credential is used, or `"pool"` when the platform pool is used.

---

## Step 3: List Your Credentials

View all your registered credentials (secret material is never returned):

```bash
curl -s http://localhost:8080/v1/credentials \
  -H "Authorization: Bearer $USER_TOKEN" | jq .
```

```json
{
  "credentials": [
    {
      "credentialRef": "cred_01...",
      "provider": "anthropic",
      "metadata": {"label": "personal-key", "tier": "pro"},
      "createdAt": "2026-04-12T10:00:00Z"
    }
  ]
}
```

---

## Step 4: Rotate the Credential

When you get a new API key, rotate the secret without disrupting active sessions. Active leases are updated:

```bash
curl -s -X PUT "http://localhost:8080/v1/credentials/cred_01..." \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "secret": "sk-ant-api03-new-key-...",
    "metadata": {
      "label": "personal-key-rotated",
      "tier": "pro"
    }
  }' | jq .
```

Active sessions switch to the new key without interruption. The token service refreshes its credential cache atomically, and the next outbound call uses the new key.

---

## Step 5: Revoke When Done

When a credential is compromised or no longer needed, revoke it to invalidate all active leases:

```bash
curl -s -X POST "http://localhost:8080/v1/credentials/cred_01.../revoke" \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" -d '{}' | jq .
```

Active sessions using this credential are terminated. Future sessions fall back to the platform credential pool.

To remove the credential record entirely (without terminating active sessions):

```bash
curl -s -X DELETE "http://localhost:8080/v1/credentials/cred_01..." \
  -H "Authorization: Bearer $USER_TOKEN" | jq .
```

The difference: `revoke` invalidates active leases; `delete` removes the record but lets active sessions finish with the credential they already hold.

---

## Key Concepts

- User credentials take precedence over platform credential pools when both exist for the same provider.
- Write-only secrets: the `secret` field is never returned in API responses.
- Rotation: `PUT` updates propagate to active sessions without interruption.
- Revoke vs. delete: revoke terminates active sessions; delete does not.
- Fallback: if no user credential exists, the platform pool is used.

## Rotating your Lenny access token

The `$USER_TOKEN` above is your Lenny access token (separate from the LLM API key you registered). Rotate it via the canonical `/v1/oauth/token` endpoint:

```bash
ROTATED=$(curl -s -X POST "http://localhost:8080/v1/oauth/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=urn:ietf:params:oauth:grant-type:token-exchange" \
  -d "subject_token=$USER_TOKEN" \
  -d "subject_token_type=urn:ietf:params:oauth:token-type:access_token" \
  -d "requested_token_type=urn:ietf:params:oauth:token-type:access_token" \
  | jq -r '.access_token')

export USER_TOKEN=$ROTATED
```

See [Authentication](../client-guide/authentication.md#token-rotation-and-exchange-v1oauthtoken) for the full RFC 8693 parameter set (including `actor_token` for delegation minting).

---

## Next Steps

- [Your First Session](first-session): session lifecycle basics
- [REST API Reference](../api/rest): credentials endpoint documentation
- [Error Catalog](../reference/error-catalog): `USER_CREDENTIAL_NOT_FOUND`, `CREDENTIAL_REVOKED`
