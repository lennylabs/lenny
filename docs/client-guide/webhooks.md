---
layout: default
title: Webhooks
parent: "Client Guide"
nav_order: 6
---

# Webhooks

Lenny supports webhook callbacks for asynchronous session monitoring. Instead of polling for session state changes, you can register a `callbackUrl` when creating a session and receive HTTP POST notifications when significant events occur.

---

## Setting Up Webhooks

Register a callback URL when creating a session using the convenience endpoint:

```
POST /v1/sessions/start
Content-Type: application/json
Authorization: Bearer <token>

{
  "runtime": "claude-worker",
  "pool": "default-pool",
  "inlineFiles": [
    {"path": "code.py", "content": "def hello(): return 'world'"}
  ],
  "message": {
    "parts": [{"type": "text", "text": "Review this code."}]
  },
  "callbackUrl": "https://my-app.example.com/webhooks/lenny"
}
```

**Response** (`201 Created`):

```json
{
  "sessionId": "sess_abc123",
  "state": "running",
  "uploadToken": "sess_abc123.1712345678.a1b2c3d4e5f6..."
}
```

---

## Webhook Event Types

The gateway delivers webhook events for session state changes:

| Event Type | Trigger |
|---|---|
| `session.started` | Session transitions to `running` |
| `session.completed` | Session reaches `completed` state |
| `session.failed` | Session reaches `failed` state |
| `session.cancelled` | Session reaches `cancelled` state |
| `session.expired` | Session reaches `expired` state |
| `session.suspended` | Session transitions to `suspended` |
| `session.awaiting_action` | Session transitions to `awaiting_client_action` (retries exhausted) |
| `session.expiring_soon` | 5 minutes before `maxSessionAge` expires |

---

## Webhook Delivery

Webhook events are delivered as HTTP POST requests to the registered `callbackUrl`:

```
POST https://my-app.example.com/webhooks/lenny
Content-Type: application/json
X-Lenny-Webhook-Signature: sha256=a1b2c3d4e5f6...

{
  "eventType": "session.completed",
  "sessionId": "sess_abc123",
  "state": "completed",
  "timestamp": "2026-01-15T10:45:00Z",
  "runtime": "claude-worker",
  "result": {
    "parts": [
      {"type": "text", "text": "Code review complete. 3 issues found."}
    ]
  }
}
```

Your webhook endpoint should:

1. Verify the `X-Lenny-Webhook-Signature` header (HMAC-SHA256)
2. Return a `2xx` status code to acknowledge receipt
3. Process the event asynchronously if needed

---

## Retry Policy for Failed Deliveries

If your webhook endpoint is unreachable or returns a non-2xx status code, the gateway retries delivery with exponential backoff:

| Attempt | Delay |
|---|---|
| 1st retry | 10 seconds |
| 2nd retry | 30 seconds |
| 3rd retry | 1 minute |
| 4th retry | 5 minutes |
| 5th retry | 15 minutes |

After all retry attempts are exhausted, the event is stored as an undelivered event.

---

## Retrieving Undelivered Events

If webhook delivery fails after all retries, retrieve undelivered events via the API:

```
GET /v1/sessions/{id}/webhook-events
Authorization: Bearer <token>
```

**Response** (`200 OK`):

```json
{
  "items": [
    {
      "eventType": "session.completed",
      "sessionId": "sess_abc123",
      "state": "completed",
      "timestamp": "2026-01-15T10:45:00Z",
      "deliveryAttempts": 6,
      "lastAttemptAt": "2026-01-15T11:00:00Z",
      "lastError": "Connection refused"
    }
  ],
  "cursor": null,
  "hasMore": false
}
```

---

## Async Job Pattern

Webhooks enable a fire-and-forget pattern for CI/CD pipelines and batch processing:

```
1. Create session with callbackUrl  ──>  Lenny creates session and starts agent
2. Return immediately               <──  Response: {sessionId, state: "running"}
3. Do other work...
4. Receive webhook notification      <──  POST to callbackUrl: {session.completed}
5. Retrieve results                  ──>  GET /v1/sessions/{id}/artifacts
```

This avoids the need to poll `GET /v1/sessions/{id}` in a loop.

---

## Extending Artifact Retention

By default, session artifacts are retained for 7 days. For async workflows where results may be retrieved much later, extend the retention period:

```
POST /v1/sessions/sess_abc123/extend-retention
Content-Type: application/json
Authorization: Bearer <token>

{
  "ttlSeconds": 2592000
}
```

**Response** (`200 OK`):

```json
{
  "retentionExpiresAt": "2026-02-14T10:30:00Z"
}
```

Call this endpoint in your webhook handler to ensure artifacts remain available for as long as you need them.

---

## Example: Async Session with Webhook Callback

### Python -- FastAPI Webhook Receiver

```python
"""
Async session with webhook callback.

pip install fastapi uvicorn httpx
"""

import hashlib
import hmac
import httpx
from fastapi import FastAPI, Request, HTTPException

app = FastAPI()

LENNY_URL = "https://lenny.example.com"
TOKEN = "your-access-token"
WEBHOOK_SECRET = "your-webhook-secret"  # Shared secret for signature verification


# Step 1: Create a session with a callback URL
async def create_async_session(code: str, prompt: str) -> str:
    """Create a Lenny session that will notify us via webhook when done."""
    async with httpx.AsyncClient() as client:
        response = await client.post(
            f"{LENNY_URL}/v1/sessions/start",
            headers={
                "Authorization": f"Bearer {TOKEN}",
                "Content-Type": "application/json",
            },
            json={
                "runtime": "claude-worker",
                "pool": "default-pool",
                "inlineFiles": [
                    {"path": "code.py", "content": code}
                ],
                "message": {
                    "parts": [{"type": "text", "text": prompt}]
                },
                "callbackUrl": "https://my-app.example.com/webhooks/lenny",
            },
        )
        response.raise_for_status()
        data = response.json()
        print(f"Session created: {data['sessionId']}")
        return data["sessionId"]


# Step 2: Receive webhook notifications
@app.post("/webhooks/lenny")
async def handle_webhook(request: Request):
    """Handle Lenny webhook notifications."""

    # Verify signature
    body = await request.body()
    signature = request.headers.get("X-Lenny-Webhook-Signature", "")
    expected = "sha256=" + hmac.new(
        WEBHOOK_SECRET.encode(),
        body,
        hashlib.sha256,
    ).hexdigest()

    if not hmac.compare_digest(signature, expected):
        raise HTTPException(status_code=401, detail="Invalid signature")

    # Parse event
    event = await request.json()
    event_type = event["eventType"]
    session_id = event["sessionId"]

    print(f"Received webhook: {event_type} for {session_id}")

    if event_type == "session.completed":
        # Session finished -- retrieve artifacts
        await retrieve_results(session_id)

    elif event_type == "session.failed":
        print(f"Session {session_id} failed!")
        # Handle failure (alert, retry, etc.)

    elif event_type == "session.awaiting_action":
        print(f"Session {session_id} needs attention (retries exhausted)")
        # Decide whether to resume or terminate

    return {"status": "ok"}


# Step 3: Retrieve results when notified
async def retrieve_results(session_id: str):
    """Download session artifacts after completion."""
    async with httpx.AsyncClient() as client:
        headers = {"Authorization": f"Bearer {TOKEN}"}

        # Extend retention so we don't lose artifacts
        await client.post(
            f"{LENNY_URL}/v1/sessions/{session_id}/extend-retention",
            headers=headers,
            json={"ttlSeconds": 2592000},  # 30 days
        )

        # List artifacts
        response = await client.get(
            f"{LENNY_URL}/v1/sessions/{session_id}/artifacts",
            headers=headers,
        )
        artifacts = response.json()

        print(f"Session {session_id} completed with {len(artifacts['items'])} artifacts:")
        for artifact in artifacts["items"]:
            print(f"  {artifact['path']} ({artifact['size']} bytes)")

        # Download transcript
        response = await client.get(
            f"{LENNY_URL}/v1/sessions/{session_id}/transcript",
            headers=headers,
        )
        transcript = response.json()
        print(f"Transcript: {len(transcript['items'])} messages")

        # Get usage
        response = await client.get(
            f"{LENNY_URL}/v1/sessions/{session_id}/usage",
            headers=headers,
        )
        usage = response.json()
        print(
            f"Usage: {usage['inputTokens']} input tokens, "
            f"{usage['outputTokens']} output tokens"
        )


# Run with: uvicorn webhook_server:app --host 0.0.0.0 --port 8000
```

### curl -- Async Session Workflow

```bash
#!/bin/bash
# Create an async session with webhook callback

TOKEN="your-access-token"
LENNY="https://lenny.example.com"
CALLBACK="https://my-app.example.com/webhooks/lenny"

# Create session with callback
SESSION_ID=$(curl -s -X POST "$LENNY/v1/sessions/start" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "{
    \"runtime\": \"claude-worker\",
    \"inlineFiles\": [{\"path\": \"code.py\", \"content\": \"print('hello')\"}],
    \"message\": {\"parts\": [{\"type\": \"text\", \"text\": \"Review this code.\"}]},
    \"callbackUrl\": \"$CALLBACK\"
  }" | jq -r '.sessionId')

echo "Created session: $SESSION_ID"
echo "Waiting for webhook notification at $CALLBACK..."

# If you need to poll instead of using webhooks:
while true; do
  STATE=$(curl -s "$LENNY/v1/sessions/$SESSION_ID" \
    -H "Authorization: Bearer $TOKEN" \
    | jq -r '.state')

  echo "State: $STATE"

  case $STATE in
    completed|failed|cancelled|expired)
      echo "Session reached terminal state: $STATE"
      break
      ;;
  esac

  sleep 5
done

# Retrieve results
echo "Artifacts:"
curl -s "$LENNY/v1/sessions/$SESSION_ID/artifacts" \
  -H "Authorization: Bearer $TOKEN" | jq '.items[].path'

echo "Usage:"
curl -s "$LENNY/v1/sessions/$SESSION_ID/usage" \
  -H "Authorization: Bearer $TOKEN" | jq '{inputTokens, outputTokens}'
```
