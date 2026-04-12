---
layout: default
title: "Using Connectors"
parent: Tutorials
nav_order: 9
---

# Using Connectors

**Persona:** Client Developer | **Difficulty:** Intermediate

Connectors let your agent sessions interact with external services (GitHub, Jira, Slack, etc.) through the gateway. The gateway manages OAuth tokens, encrypts stored credentials, and enforces content policy interceptors on all connector traffic.

In this tutorial you will register a GitHub connector, create a session with connector access, observe the OAuth elicitation flow, and watch the agent use GitHub MCP tools through the gateway.

## Prerequisites

- Lenny running locally via `docker compose up`
- A GitHub OAuth App (client ID and secret) -- see [GitHub docs](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app)
- Familiarity with [Your First Session](first-session)
- curl and jq installed

Throughout this tutorial the gateway is at `http://localhost:8080` and the admin API requires a `platform-admin` bearer token.

---

## Step 1: Register the GitHub Connector

Register a connector via the Admin API. The connector definition includes the MCP server URL and OAuth configuration.

```bash
curl -s -X POST http://localhost:8080/v1/admin/connectors \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "github",
    "displayName": "GitHub",
    "mcpServerUrl": "https://github-mcp-server.example.com/mcp",
    "oauth": {
      "provider": "github",
      "clientId": "Iv1.abc123def456",
      "clientSecret": "secret_xyz789",
      "authorizationUrl": "https://github.com/login/oauth/authorize",
      "tokenUrl": "https://github.com/login/oauth/access_token",
      "scopes": ["repo", "read:org"]
    },
    "labels": {
      "category": "source-control"
    }
  }' | jq .
```

Expected response:

```json
{
  "name": "github",
  "displayName": "GitHub",
  "status": "active",
  "createdAt": "2026-04-12T10:00:00Z"
}
```

You can verify connectivity with the test endpoint:

```bash
curl -s -X POST http://localhost:8080/v1/admin/connectors/github/test \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
```

---

## Step 2: Configure Runtime Connector Access

Ensure your runtime's delegation policy (or default configuration) permits access to the `github` connector. If you are using the default runtime, connectors are available by default. For locked-down environments, update the runtime or delegation policy:

```bash
curl -s -X PUT http://localhost:8080/v1/admin/runtimes/claude-worker \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -H "If-Match: \"etag-from-get\"" \
  -d '{
    "connectors": ["github"],
    "image": "ghcr.io/myorg/claude-worker:latest",
    "executionMode": "session"
  }' | jq .
```

---

## Step 3: Create a Session

Create a session using the runtime that has connector access:

```bash
curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Authorization: Bearer $USER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "claude-worker",
    "labels": {"task": "github-integration"}
  }' | jq .
```

Save the `session_id` and `uploadToken` from the response.

---

## Step 4: Finalize and Start

```bash
SESSION_ID="sess_01..."

curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/finalize" \
  -H "Authorization: UploadToken $UPLOAD_TOKEN" \
  -H "Content-Type: application/json" -d '{}' | jq .

curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/start" \
  -H "Content-Type: application/json" -d '{}' | jq .
```

---

## Step 5: Send a Message That Triggers Connector Use

Ask the agent to interact with GitHub:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{
    "input": [{"type": "text", "inline": "List the open pull requests in myorg/myrepo."}]
  }' | jq .
```

---

## Step 6: Handle the OAuth Elicitation

When the agent first calls a GitHub MCP tool, the gateway checks whether the user has an active OAuth token for the `github` connector. If not, it triggers an **elicitation** requesting the user to authorize.

Monitor the SSE stream for the elicitation event:

```bash
curl -s -N "http://localhost:8080/v1/sessions/${SESSION_ID}/events" \
  -H "Accept: text/event-stream"
```

You will see an event like:

```
event: elicitation
data: {"type":"elicitation","elicitationId":"elic_01...","kind":"oauth","provider":"github","authUrl":"https://github.com/login/oauth/authorize?client_id=Iv1.abc123def456&scope=repo+read:org&state=..."}
```

Open `authUrl` in a browser and complete the OAuth flow. The gateway captures the callback and stores the encrypted token. Then respond to the elicitation:

```bash
curl -s -X POST \
  "http://localhost:8080/v1/sessions/${SESSION_ID}/elicitations/elic_01.../respond" \
  -H "Content-Type: application/json" \
  -d '{"response": "authorized"}' | jq .
```

---

## Step 7: Observe Connector Tool Calls

After authorization, the agent's GitHub MCP tool calls flow through the gateway, which injects the OAuth token and proxies the request to the GitHub MCP server. The agent receives the tool results and continues its work.

In the SSE stream you will see events like:

```
event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Found 3 open PRs in myorg/myrepo:\n1. #42 - Fix auth module\n2. #43 - Add caching layer\n3. #44 - Update dependencies"}]}
```

The gateway logs each connector call for audit. Connector traffic is subject to `PreConnectorRequest` and `PostConnectorResponse` interceptors if configured.

---

## Key Concepts

- **Gateway-managed OAuth**: The gateway stores encrypted OAuth tokens and injects them into connector requests. Agent pods never see raw connector credentials.
- **Content policy interceptors**: `PreConnectorRequest` and `PostConnectorResponse` interceptors can inspect and filter connector traffic.
- **Elicitation flow**: When a user has not yet authorized a connector, the gateway triggers an elicitation. Once authorized, subsequent sessions reuse the stored token.
- **Connector test endpoint**: Use `POST /v1/admin/connectors/{name}/test` to verify DNS, TLS, MCP handshake, and auth before exposing a connector to users.

---

## Next Steps

- [Recursive Delegation](recursive-delegation) -- delegates can also use connectors
- [REST API Reference](../api/rest) -- full connector admin API
- [Error Catalog](../reference/error-catalog) -- connector-related error codes
