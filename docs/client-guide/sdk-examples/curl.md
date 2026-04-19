---
layout: default
title: "curl"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 4
---

# curl Command Reference

curl command reference for every REST endpoint, plus a session lifecycle walkthrough as a runnable bash script.

## Prerequisites

```bash
# Required: curl, jq
# macOS: brew install curl jq
# Ubuntu: apt-get install curl jq
```

---

## Shell Setup and Helper Functions

```bash
#!/bin/bash
# lenny.sh -- Lenny curl helpers
# Source this file: source lenny.sh

export LENNY_URL="${LENNY_URL:-https://lenny.example.com}"
export TOKEN="${LENNY_TOKEN:-your-access-token}"

# Base curl command with auth
lenny() {
  curl -s -H "Authorization: Bearer $TOKEN" "$@"
}

# POST with JSON body
lenny_post() {
  local path="$1"; shift
  lenny -X POST "${LENNY_URL}${path}" \
    -H "Content-Type: application/json" "$@"
}

# GET with optional query params
lenny_get() {
  lenny "${LENNY_URL}$1"
}

# Pretty-print JSON response
lenny_pp() {
  "$@" | jq .
}
```

---

## Authentication

```bash
# Get access token via client credentials grant
TOKEN=$(curl -s -X POST https://your-oidc-provider.com/oauth/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=client_credentials" \
  -d "client_id=$OIDC_CLIENT_ID" \
  -d "client_secret=$OIDC_CLIENT_SECRET" \
  -d "scope=openid profile" \
  | jq -r '.access_token')

echo "Token: ${TOKEN:0:20}..."
```

### Token rotation via `/v1/oauth/token`

Lenny exposes a canonical RFC 8693 token-exchange endpoint for rotation and delegation:

```bash
# Rotate the current token (e.g., shortly before expiry)
ROTATED=$(curl -s -X POST "$LENNY_URL/v1/oauth/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=urn:ietf:params:oauth:grant-type:token-exchange" \
  -d "subject_token=$TOKEN" \
  -d "subject_token_type=urn:ietf:params:oauth:token-type:access_token" \
  -d "requested_token_type=urn:ietf:params:oauth:token-type:access_token" \
  | jq -r '.access_token')

TOKEN=$ROTATED
```

For delegation child-token minting, additionally supply `actor_token=$PARENT_SESSION_TOKEN` and a narrowed `scope` string. See [Authentication](../authentication.md#token-rotation-and-exchange-v1oauthtoken).

---

## Session Lifecycle -- Every Endpoint

### Create Session

```bash
curl -s -X POST "$LENNY_URL/v1/sessions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "claude-worker",
    "pool": "default-pool",
    "labels": {"project": "my-app"},
    "retryPolicy": {
      "mode": "auto_then_client",
      "maxRetries": 2,
      "maxSessionAgeSeconds": 7200
    }
  }' | jq .

# Save session ID and upload token
SESSION_ID=$(curl -s -X POST "$LENNY_URL/v1/sessions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker"}' \
  | jq -r '.sessionId')

UPLOAD_TOKEN=$(curl -s -X POST "$LENNY_URL/v1/sessions" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker"}' \
  | jq -r '.uploadToken')
```

### Upload Files

```bash
# Upload individual files
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Upload-Token: $UPLOAD_TOKEN" \
  -F "files=@main.py" \
  -F "files=@config.yaml" \
  | jq .

# Upload a tar.gz archive (auto-extracted)
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/upload" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Upload-Token: $UPLOAD_TOKEN" \
  -F "files=@project.tar.gz" \
  | jq .
```

### Finalize Workspace

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/finalize" \
  -H "Authorization: Bearer $TOKEN" \
  -H "X-Upload-Token: $UPLOAD_TOKEN" \
  | jq .
```

### Start Session

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/start" \
  -H "Authorization: Bearer $TOKEN" \
  | jq .
```

### Send Message

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/messages" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "input": [
      {"type": "text", "inline": "Review this code and suggest improvements."}
    ]
  }' | jq .
```

### Interrupt (Suspend)

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/interrupt" \
  -H "Authorization: Bearer $TOKEN" \
  | jq .
```

### Resume (After Retry Exhaustion)

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/resume" \
  -H "Authorization: Bearer $TOKEN" \
  | jq .
```

### Terminate

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/terminate" \
  -H "Authorization: Bearer $TOKEN" \
  | jq .
```

### Delete (Force Terminate + Cleanup)

```bash
curl -s -X DELETE "$LENNY_URL/v1/sessions/$SESSION_ID" \
  -H "Authorization: Bearer $TOKEN" \
  | jq .
```

---

## Convenience: Create + Start in One Call

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/start" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "claude-worker",
    "inlineFiles": [
      {"path": "code.py", "content": "def add(a, b): return a + b"}
    ],
    "message": {
      "input": [{"type": "text", "inline": "Review this function."}]
    },
    "callbackUrl": "https://my-app.example.com/webhook"
  }' | jq .
```

---

## Introspection Endpoints

### Get Session Status

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### List Sessions

```bash
# All sessions
curl -s "$LENNY_URL/v1/sessions?limit=20" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Filter by state
curl -s "$LENNY_URL/v1/sessions?status=running&limit=10" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Filter by runtime
curl -s "$LENNY_URL/v1/sessions?runtime=claude-worker" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Pagination
CURSOR=$(curl -s "$LENNY_URL/v1/sessions?limit=10" \
  -H "Authorization: Bearer $TOKEN" | jq -r '.cursor')

curl -s "$LENNY_URL/v1/sessions?limit=10&cursor=$CURSOR" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### List Artifacts

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/artifacts" \
  -H "Authorization: Bearer $TOKEN" | jq '.items[].path'
```

### Download a Specific Artifact

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/artifacts/output/result.json" \
  -H "Authorization: Bearer $TOKEN" -o result.json
```

### Download Workspace Snapshot

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/workspace" \
  -H "Authorization: Bearer $TOKEN" -o workspace.tar.gz
```

### Get Transcript

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/transcript?limit=50" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Get Logs (JSON)

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/logs?limit=100" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Stream Logs (SSE)

```bash
# --no-buffer (-N) is essential for SSE streaming
curl -N "$LENNY_URL/v1/sessions/$SESSION_ID/logs" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream"

# With cursor for reconnection
curl -N "$LENNY_URL/v1/sessions/$SESSION_ID/logs?cursor=$CURSOR" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream"
```

### Get Setup Command Output

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/setup-output" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Get Delegation Tree

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/tree" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Get Usage

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/usage" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Extend Artifact Retention

```bash
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/extend-retention" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ttlSeconds": 2592000}' | jq .
```

### List Undelivered Webhook Events

```bash
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/webhook-events" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

---

## Discovery

### List Runtimes

```bash
curl -s "$LENNY_URL/v1/runtimes" \
  -H "Authorization: Bearer $TOKEN" | jq '.items[] | {name, type}'
```

### List Models (OpenAI-Compatible)

```bash
curl -s "$LENNY_URL/v1/models" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### List Pools

```bash
curl -s "$LENNY_URL/v1/pools" \
  -H "Authorization: Bearer $TOKEN" | jq .
```

### Get OpenAPI Spec

```bash
# No auth required
curl -s "$LENNY_URL/openapi.yaml" -o openapi.yaml
curl -s "$LENNY_URL/openapi.json" | jq . > openapi.json
```

---

## Credential Management

```bash
# Register a credential
curl -s -X POST "$LENNY_URL/v1/credentials" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "provider": "anthropic_direct",
    "secretMaterial": {"apiKey": "sk-ant-..."},
    "label": "My Key"
  }' | jq .

# List credentials
curl -s "$LENNY_URL/v1/credentials" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Rotate a credential
curl -s -X PUT "$LENNY_URL/v1/credentials/cred_abc123" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"secretMaterial": {"apiKey": "sk-ant-new..."}}' | jq .

# Revoke a credential
curl -s -X POST "$LENNY_URL/v1/credentials/cred_abc123/revoke" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Delete a credential
curl -s -X DELETE "$LENNY_URL/v1/credentials/cred_abc123" \
  -H "Authorization: Bearer $TOKEN"
```

---

## Session Derive and Replay

```bash
# Derive (fork) from a completed session
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/derive" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker-v2"}' | jq .

# Derive from a live session (requires allowStale)
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/derive" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker-v2", "allowStale": true}' | jq .

# Replay against a different runtime
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/replay" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "targetRuntime": "claude-worker-v2",
    "replayMode": "prompt_history",
    "evalRef": "eval-2026-q1"
  }' | jq .
```

---

## Elicitation and Tool Approval

```bash
# Respond to an elicitation
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/elicitations/$ELICITATION_ID/respond" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"response": {"answer": "yes"}}' | jq .

# Dismiss an elicitation
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/elicitations/$ELICITATION_ID/dismiss" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Approve a tool call
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/tool-use/$TOOL_CALL_ID/approve" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Deny a tool call
curl -s -X POST "$LENNY_URL/v1/sessions/$SESSION_ID/tool-use/$TOOL_CALL_ID/deny" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reason": "Not authorized to delete files"}' | jq .
```

---

## Complete Session Lifecycle Script

```bash
#!/bin/bash
# lifecycle.sh -- Complete Lenny session lifecycle
#
# Usage: LENNY_URL=https://lenny.example.com LENNY_TOKEN=... ./lifecycle.sh
set -euo pipefail

LENNY="${LENNY_URL:-https://lenny.example.com}"
TOKEN="${LENNY_TOKEN:?Set LENNY_TOKEN}"
AUTH=(-H "Authorization: Bearer $TOKEN")

echo "=== Lenny Session Lifecycle ==="

# 1. Create session
echo -e "\n1. Creating session..."
CREATE_RESP=$(curl -s -X POST "$LENNY/v1/sessions" \
  "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker", "labels": {"test": "lifecycle"}}')

SESSION_ID=$(echo "$CREATE_RESP" | jq -r '.sessionId')
UPLOAD_TOKEN=$(echo "$CREATE_RESP" | jq -r '.uploadToken')
echo "   Session: $SESSION_ID"

# 2. Upload files
echo -e "\n2. Uploading files..."
echo 'def greet(name): return f"Hello, {name}!"' > /tmp/lenny_example.py
curl -s -X POST "$LENNY/v1/sessions/$SESSION_ID/upload" \
  "${AUTH[@]}" \
  -H "X-Upload-Token: $UPLOAD_TOKEN" \
  -F "files=@/tmp/lenny_example.py;filename=code.py" \
  | jq '.uploaded[].path'
rm /tmp/lenny_example.py

# 3. Finalize workspace
echo -e "\n3. Finalizing..."
curl -s -X POST "$LENNY/v1/sessions/$SESSION_ID/finalize" \
  "${AUTH[@]}" \
  -H "X-Upload-Token: $UPLOAD_TOKEN" \
  | jq '.state'

# 4. Start session
echo -e "\n4. Starting..."
curl -s -X POST "$LENNY/v1/sessions/$SESSION_ID/start" \
  "${AUTH[@]}" \
  | jq '.state'

# 5. Send message
echo -e "\n5. Sending message..."
curl -s -X POST "$LENNY/v1/sessions/$SESSION_ID/messages" \
  "${AUTH[@]}" \
  -H "Content-Type: application/json" \
  -d '{"input": [{"type": "text", "inline": "Review code.py and suggest improvements."}]}' \
  | jq '.deliveryReceipt.status'

# 6. Stream output (timeout after 120s)
echo -e "\n6. Streaming output (Ctrl+C to stop)..."
timeout 120 curl -N "$LENNY/v1/sessions/$SESSION_ID/logs" \
  "${AUTH[@]}" \
  -H "Accept: text/event-stream" 2>/dev/null || true

# 7. Check state
echo -e "\n\n7. Final state..."
STATE=$(curl -s "$LENNY/v1/sessions/$SESSION_ID" \
  "${AUTH[@]}" | jq -r '.state')
echo "   State: $STATE"

# 8. Get usage
echo -e "\n8. Usage:"
curl -s "$LENNY/v1/sessions/$SESSION_ID/usage" \
  "${AUTH[@]}" | jq '{inputTokens, outputTokens, wallClockSeconds}'

# 9. List artifacts
echo -e "\n9. Artifacts:"
curl -s "$LENNY/v1/sessions/$SESSION_ID/artifacts" \
  "${AUTH[@]}" | jq '.items[].path'

# 10. Terminate if not already terminal
if [[ "$STATE" != "completed" && "$STATE" != "failed" && \
      "$STATE" != "cancelled" && "$STATE" != "expired" ]]; then
  echo -e "\n10. Terminating..."
  curl -s -X POST "$LENNY/v1/sessions/$SESSION_ID/terminate" \
    "${AUTH[@]}" | jq '.state'
fi

echo -e "\n=== Done ==="
```

---

## jq Recipes

```bash
# Extract session IDs of all running sessions
curl -s "$LENNY_URL/v1/sessions?status=running" \
  -H "Authorization: Bearer $TOKEN" \
  | jq -r '.items[].sessionId'

# Get total token usage across all completed sessions
curl -s "$LENNY_URL/v1/sessions?status=completed&limit=200" \
  -H "Authorization: Bearer $TOKEN" \
  | jq '[.items[].usage.inputTokens // 0] | add'

# Pretty-print delegation tree
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/tree" \
  -H "Authorization: Bearer $TOKEN" \
  | jq -r '
    def print_tree(indent):
      ("\("  " * indent)\(.state) \(.runtimeRef) (\(.sessionId))"),
      (.children[]? | print_tree(indent + 1));
    .root | print_tree(0)
  '

# Extract text from session transcript
curl -s "$LENNY_URL/v1/sessions/$SESSION_ID/transcript" \
  -H "Authorization: Bearer $TOKEN" \
  | jq -r '.items[] | "\(.role): \(.parts[0].text // "(non-text)")"'

# Count sessions by state
curl -s "$LENNY_URL/v1/sessions?limit=200" \
  -H "Authorization: Bearer $TOKEN" \
  | jq '.items | group_by(.state) | map({state: .[0].state, count: length})'
```
