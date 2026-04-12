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

- Lenny running locally via `make run` or `docker compose up`
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

**One credential per provider.** If you register a second credential for the same provider, it replaces the first. The `secret` field is write-only -- it is never returned in API responses.

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

The session proceeds through the normal lifecycle. The gateway injects your API key into LLM proxy requests transparently -- the agent pod never sees the raw key.

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

When you get a new API key, rotate the secret without disrupting active sessions. Active leases are immediately updated:

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

Active sessions using the old key are seamlessly rotated to the new key via the Token Service's hot-rotation mechanism. No session interruption occurs.

---

## Step 5: Revoke When Done

When a credential is compromised or no longer needed, revoke it to immediately invalidate all active leases:

```bash
curl -s -X POST "http://localhost:8080/v1/credentials/cred_01.../revoke" \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" -d '{}' | jq .
```

Active sessions using this credential are terminated immediately. Future sessions fall back to the platform credential pool.

To remove the credential record entirely (without terminating active sessions):

```bash
curl -s -X DELETE "http://localhost:8080/v1/credentials/cred_01..." \
  -H "Authorization: Bearer $USER_TOKEN" | jq .
```

The difference: `revoke` invalidates active leases immediately; `delete` removes the record but lets active sessions finish with the credential they already hold.

---

## Key Concepts

- **User credentials take priority** over platform credential pools when both exist for the same provider.
- **Write-only secrets**: The `secret` field is never returned in API responses.
- **Hot rotation**: `PUT` updates propagate to active sessions without interruption.
- **Revoke vs. delete**: Revoke terminates active sessions; delete does not.
- **Fallback**: If no user credential exists, the platform pool is used.

---

## Next Steps

- [Your First Session](first-session) -- session lifecycle basics
- [REST API Reference](../api/rest) -- full credentials endpoint documentation
- [Error Catalog](../reference/error-catalog) -- `USER_CREDENTIAL_NOT_FOUND`, `CREDENTIAL_REVOKED`
