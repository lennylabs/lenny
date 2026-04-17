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

Every webhook delivery is a [CloudEvents v1.0.2](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) JSON record. The CloudEvents `type` attribute identifies the event:

| CloudEvents `type` | Trigger |
|---|---|
| `dev.lenny.session_completed` | Session reaches `completed` state |
| `dev.lenny.session_failed` | Session reaches `failed` state |
| `dev.lenny.session_terminated` | Admin or system terminated the session |
| `dev.lenny.session_cancelled` | User/runtime cancelled the session |
| `dev.lenny.session_expired` | Session reached `maxSessionAge` or `maxIdleTimeSeconds` |
| `dev.lenny.session_awaiting_action` | Session transitioned to `awaiting_client_action` (retries exhausted) |
| `dev.lenny.delegation_completed` | Child session reached a terminal state |

See the [CloudEvents catalog](../reference/cloudevents-catalog.md) for the complete event inventory.

---

## Webhook Delivery

Webhook events are delivered as HTTP POST requests to the registered `callbackUrl` with a CloudEvents envelope body:

```
POST https://my-app.example.com/webhooks/lenny
Content-Type: application/json
X-Lenny-Signature: t=1718203320,v1=a1b2c3d4e5f6...

{
  "specversion": "1.0",
  "id": "t_acme:gw-7f4c2:1718203320000000000:9f3a",
  "source": "//lenny.dev/gateway/gw-7f4c2",
  "type": "dev.lenny.session_completed",
  "time": "2026-01-15T10:45:00Z",
  "datacontenttype": "application/json",
  "subject": "session/sess_abc123",
  "lennytenantid": "t_acme",
  "data": {
    "session_id": "sess_abc123",
    "status": "completed",
    "usage": { "inputTokens": 15000, "outputTokens": 8000 },
    "artifacts": ["workspace.tar.gz"]
  }
}
```

Your webhook endpoint should:

1. Verify the `X-Lenny-Signature` header (HMAC-SHA256 over `<unix_seconds>.<raw_body_bytes>`; reject events where the timestamp is more than 5 minutes old).
2. Deduplicate by CloudEvents `id` (an `id` seen previously is a retry; respond 2xx but do not re-process).
3. Read the event kind from CloudEvents `type` and the payload from the `data` field.
4. Return a `2xx` status code to acknowledge receipt.
5. Process the event asynchronously if needed.

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
      "cloudevent": {
        "specversion": "1.0",
        "id": "t_acme:gw-7f4c2:1718203320000000000:9f3a",
        "source": "//lenny.dev/gateway/gw-7f4c2",
        "type": "dev.lenny.session_completed",
        "time": "2026-01-15T10:45:00Z",
        "subject": "session/sess_abc123",
        "data": { "session_id": "sess_abc123", "status": "completed" }
      },
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

## Elicitation Limitation

Webhook-only clients cannot respond to elicitations. Elicitations require an active SSE or MCP streaming connection because the gateway delivers `elicitation_request` events on the session's event stream and expects a synchronous response. If your workflow uses webhook-only delivery, choose runtimes that do not require elicitation (human-in-the-loop prompts), or pair webhooks with an SSE streaming connection on the same session to handle elicitations when they arise.

---

## Webhook Secret Configuration

The webhook signature (`X-Lenny-Signature`) uses the format `t=<unix_seconds>,v1=<hex_signature>` where the signature is HMAC-SHA256 over the ASCII bytes of the string `<unix_seconds>.<raw_body_bytes>` — that is, the decimal-string form of `t`, followed by a literal `.` (`0x2E`), followed by the raw request body exactly as received on the wire (no trimming, no re-serialization). There are two ways to configure the shared secret:

- **Per-session** -- pass a `callbackSecret` field alongside `callbackUrl` when creating the session. This secret is used exclusively for that session's webhook deliveries.
- **Tenant-level** -- configure a default webhook secret via the admin API at the tenant level. Sessions that specify a `callbackUrl` without a `callbackSecret` use the tenant-level secret.

A 5-minute replay window is enforced: receivers MUST reject events where `abs(current_time - t) > 300`. Always verify the `X-Lenny-Signature` header before processing any webhook event.

> **`t` vs CloudEvents `time`, and retry compatibility:** `t` is the **delivery attempt** timestamp — the gateway regenerates it (and therefore the HMAC signature) on every retry attempt, so a retry delivered 15 minutes after the original event carries a fresh `t` equal to the retry dispatch time, not the original event time. The 5-minute replay window therefore applies to each delivery attempt independently and is NOT in conflict with the 15-minute maximum retry interval below — the gateway re-signs with the current `t` on every retry, and the signed timestamp is always close to the moment the HTTPS request was initiated. The CloudEvents `time` field inside the body (`{"time": "2026-01-15T10:45:00Z"}`) is the **event occurrence** timestamp and is stable across retries. Use CloudEvents `id` (not `time`) for deduplication; use `t` only for replay-window enforcement.

### Verification pseudocode

```python
import hmac, hashlib, time

def verify_lenny_webhook(headers, raw_body_bytes, secret_bytes):
    # 1. Parse the header.
    sig_header = headers.get("X-Lenny-Signature", "")
    parts = dict(kv.split("=", 1) for kv in sig_header.split(",") if "=" in kv)
    t_str = parts.get("t", "")
    v1_hex = parts.get("v1", "")
    if not t_str or not v1_hex:
        raise ValueError("malformed X-Lenny-Signature header")

    # 2. Replay-window check (5 minutes).
    t = int(t_str)
    if abs(int(time.time()) - t) > 300:
        raise ValueError("timestamp outside 5-minute replay window")

    # 3. Recompute HMAC over "<t>.<body>" (ASCII bytes).
    signed_payload = t_str.encode("ascii") + b"." + raw_body_bytes
    expected = hmac.new(secret_bytes, signed_payload, hashlib.sha256).hexdigest()

    # 4. Constant-time compare.
    if not hmac.compare_digest(expected, v1_hex):
        raise ValueError("invalid signature")
```

**Deduplication guidance.** Retain each delivered CloudEvents `id` for at least **20 minutes** — long enough to cover the entire retry schedule above (final retry fires at 15 minutes after the previous attempt, plus a safety margin for clock skew and network delay). A CloudEvents `id` seen previously within this window is a retry of an already-processed event; respond 2xx but do not re-process. Beyond 20 minutes, retention is optional; longer retention lets consumers tolerate operational incidents (e.g., a retry that the gateway queued during its own outage and redelivers hours later). Dedup scope is per webhook subscription (the `callbackUrl` + `callbackSecret` pair); consumers need not share dedup state across subscriptions. Note that the 5-minute replay window on `t` is independent of dedup retention: `t` is regenerated per retry (see above), so a 15-minute-old retry still arrives with a fresh `t` and passes replay-window validation — only the CloudEvents `id` reveals that it is a duplicate.

---

## Async Job Pattern

Webhooks enable a fire-and-forget pattern for CI/CD pipelines and batch processing:

```
1. Create session with callbackUrl  ──>  Lenny creates session and starts agent
2. Return immediately               <──  Response: {sessionId, state: "running"}
3. Do other work...
4. Receive webhook notification      <──  POST to callbackUrl: CloudEvents {type: "dev.lenny.session_completed"}
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
    """Handle Lenny webhook notifications (CloudEvents envelope)."""

    # Verify signature: X-Lenny-Signature = t=<unix_seconds>,v1=<hex>
    body = await request.body()
    sig_header = request.headers.get("X-Lenny-Signature", "")
    parts = dict(p.split("=", 1) for p in sig_header.split(","))
    ts, provided_sig = parts.get("t"), parts.get("v1")
    if not ts or not provided_sig:
        raise HTTPException(status_code=401, detail="Malformed signature")

    # Replay protection: 5-minute window
    import time
    if abs(int(time.time()) - int(ts)) > 300:
        raise HTTPException(status_code=401, detail="Signature expired")

    signing_input = f"{ts}.".encode() + body
    expected = hmac.new(WEBHOOK_SECRET.encode(), signing_input, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(provided_sig, expected):
        raise HTTPException(status_code=401, detail="Invalid signature")

    # Parse CloudEvents envelope
    event = await request.json()
    event_type = event["type"]
    event_id = event["id"]
    data = event["data"]
    session_id = data.get("session_id")

    # Deduplicate by CloudEvents id
    if await already_processed(event_id):
        return {"status": "duplicate"}

    print(f"Received webhook: {event_type} for {session_id}")

    if event_type == "dev.lenny.session_completed":
        # Session finished -- retrieve artifacts
        await retrieve_results(session_id)

    elif event_type == "dev.lenny.session_failed":
        print(f"Session {session_id} failed: {data.get('error', {}).get('message')}")
        # Handle failure (alert, retry, etc.)

    elif event_type == "dev.lenny.session_awaiting_action":
        print(f"Session {session_id} needs attention (retries exhausted)")
        # Decide whether to resume or terminate

    await mark_processed(event_id)
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
