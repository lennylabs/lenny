---
layout: default
title: "Python"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 1
---

# Python Client Examples

Python examples for interacting with the Lenny REST API. Uses `httpx` (async) for the main examples and includes a `requests` (sync) variant.

## Prerequisites

```bash
pip install httpx requests
```

---

## Full Session Lifecycle (Async)

```python
"""
Lenny session lifecycle using httpx (async).

pip install httpx

Usage:
    python lenny_client.py
"""

import asyncio
import json
import os
import time
import random
from pathlib import Path
from typing import Optional

import httpx


# Configuration
LENNY_URL = os.environ.get("LENNY_URL", "https://lenny.example.com")
OIDC_TOKEN_URL = os.environ.get("OIDC_TOKEN_URL", "https://auth.example.com/oauth/token")
OIDC_CLIENT_ID = os.environ.get("OIDC_CLIENT_ID", "your-client-id")
OIDC_CLIENT_SECRET = os.environ.get("OIDC_CLIENT_SECRET", "your-client-secret")


# ---------------------------------------------------------------------------
# Authentication Helper
# ---------------------------------------------------------------------------

async def get_access_token() -> str:
    """Obtain an access token via OAuth 2.1 client credentials grant."""
    async with httpx.AsyncClient() as client:
        response = await client.post(
            OIDC_TOKEN_URL,
            data={
                "grant_type": "client_credentials",
                "client_id": OIDC_CLIENT_ID,
                "client_secret": OIDC_CLIENT_SECRET,
                "scope": "openid profile",
            },
        )
        response.raise_for_status()
        data = response.json()
        return data["access_token"]


async def rotate_lenny_token(current_token: str) -> str:
    """Rotate the current Lenny access token via RFC 8693 token exchange.

    Call this shortly before `exp` to avoid a gap in authorization.
    For delegation child-token minting, pass the parent session token via
    `actor_token` and a narrowed `scope` string.
    """
    async with httpx.AsyncClient() as client:
        response = await client.post(
            f"{LENNY_URL}/v1/oauth/token",
            data={
                "grant_type": "urn:ietf:params:oauth:grant-type:token-exchange",
                "subject_token": current_token,
                "subject_token_type": "urn:ietf:params:oauth:token-type:access_token",
                "requested_token_type": "urn:ietf:params:oauth:token-type:access_token",
            },
        )
        response.raise_for_status()
        return response.json()["access_token"]


# ---------------------------------------------------------------------------
# Error Handling with Retry
# ---------------------------------------------------------------------------

class LennyAPIError(Exception):
    """Raised when a Lenny API call returns an error."""

    def __init__(self, code: str, category: str, message: str,
                 retryable: bool, details: dict, status_code: int):
        super().__init__(f"[{code}] {message}")
        self.code = code
        self.category = category
        self.retryable = retryable
        self.details = details
        self.status_code = status_code


async def lenny_request(
    client: httpx.AsyncClient,
    method: str,
    path: str,
    max_retries: int = 5,
    **kwargs,
) -> httpx.Response:
    """
    Make a Lenny API request with automatic retry for TRANSIENT errors.

    Uses exponential backoff with jitter. Respects the Retry-After header.
    """
    base_delay = 1.0
    max_delay = 60.0

    for attempt in range(max_retries + 1):
        response = await client.request(method, path, **kwargs)

        if response.is_success:
            return response

        # Parse error body
        try:
            error_body = response.json().get("error", {})
        except Exception:
            error_body = {}

        code = error_body.get("code", "UNKNOWN")
        category = error_body.get("category", "UNKNOWN")
        message = error_body.get("message", response.text)
        retryable = error_body.get("retryable", False)
        details = error_body.get("details", {})

        if not retryable or attempt == max_retries:
            raise LennyAPIError(
                code, category, message, retryable, details, response.status_code
            )

        # Calculate wait time
        retry_after = response.headers.get("Retry-After")
        if retry_after:
            wait = float(retry_after)
        else:
            wait = min(
                base_delay * (2 ** attempt) + random.uniform(0, 1),
                max_delay,
            )

        print(f"  Retrying in {wait:.1f}s ({code}, attempt {attempt + 1}/{max_retries})")
        await asyncio.sleep(wait)

    # Should not reach here
    raise LennyAPIError(code, category, message, retryable, details, response.status_code)


# ---------------------------------------------------------------------------
# ETag-Based Updates
# ---------------------------------------------------------------------------

async def update_with_etag(
    client: httpx.AsyncClient,
    path: str,
    updates: dict,
    max_retries: int = 3,
) -> dict:
    """
    Update an admin resource using ETag-based optimistic concurrency.

    Handles 412 ETAG_MISMATCH by re-fetching and retrying.
    """
    for attempt in range(max_retries):
        # Get current state and ETag
        response = await lenny_request(client, "GET", path)
        current = response.json()
        etag = response.headers["etag"]

        # Merge updates
        current.update(updates)

        # Attempt update with If-Match
        try:
            response = await lenny_request(
                client,
                "PUT",
                path,
                json=current,
                headers={"If-Match": etag},
            )
            return response.json()
        except LennyAPIError as e:
            if e.code == "ETAG_MISMATCH" and attempt < max_retries - 1:
                print(f"  ETag conflict, retrying (attempt {attempt + 1})")
                continue
            raise

    raise Exception("Max ETag retries exceeded")


# ---------------------------------------------------------------------------
# File Upload
# ---------------------------------------------------------------------------

async def upload_files(
    client: httpx.AsyncClient,
    session_id: str,
    upload_token: str,
    file_paths: list[str],
) -> dict:
    """Upload local files to a session workspace."""
    files = []
    for file_path in file_paths:
        path = Path(file_path)
        files.append(
            ("files", (path.name, path.read_bytes(), "application/octet-stream"))
        )

    response = await lenny_request(
        client,
        "POST",
        f"/v1/sessions/{session_id}/upload",
        files=files,
        headers={"X-Upload-Token": upload_token},
    )
    return response.json()


# ---------------------------------------------------------------------------
# SSE Streaming
# ---------------------------------------------------------------------------

async def stream_session(
    client: httpx.AsyncClient,
    session_id: str,
) -> None:
    """Stream real-time output from a session using SSE."""
    last_cursor: Optional[str] = None

    while True:
        params = {}
        if last_cursor:
            params["cursor"] = last_cursor

        try:
            async with client.stream(
                "GET",
                f"/v1/sessions/{session_id}/logs",
                headers={"Accept": "text/event-stream"},
                params=params,
                timeout=None,
            ) as response:
                response.raise_for_status()

                event_type = None
                data_lines: list[str] = []

                async for line in response.aiter_lines():
                    if line.startswith("event: "):
                        event_type = line[7:]
                    elif line.startswith("data: "):
                        data_lines.append(line[6:])
                    elif line.startswith("id: "):
                        last_cursor = line[4:]
                    elif line == "":
                        if event_type and data_lines:
                            data = json.loads("\n".join(data_lines))

                            if event_type == "agent_output":
                                for part in data.get("output", []):
                                    if part["type"] == "text":
                                        print(part.get("inline", ""), end="", flush=True)
                            elif event_type == "status_change":
                                print(f"\n[Status: {data['state']}]")
                            elif event_type == "error":
                                print(f"\n[Error: {data['code']} - {data['message']}]")
                            elif event_type == "session_complete":
                                print("\n[Session complete]")
                                return
                            elif event_type == "checkpoint_boundary":
                                lost = data.get("events_lost", 0)
                                if lost > 0:
                                    print(f"\n[WARNING: {lost} events lost]")

                        event_type = None
                        data_lines = []

        except (httpx.ReadTimeout, httpx.RemoteProtocolError):
            print("\n[Connection lost, reconnecting...]")
            continue


# ---------------------------------------------------------------------------
# Pagination Helper
# ---------------------------------------------------------------------------

async def paginate(
    client: httpx.AsyncClient,
    path: str,
    limit: int = 50,
    params: Optional[dict] = None,
):
    """
    Iterate through all pages of a paginated endpoint.

    Yields individual items from each page.
    """
    query = dict(params or {})
    query["limit"] = limit
    cursor: Optional[str] = None

    while True:
        if cursor:
            query["cursor"] = cursor
        elif "cursor" in query:
            del query["cursor"]

        response = await lenny_request(client, "GET", path, params=query)
        data = response.json()

        for item in data.get("items", []):
            yield item

        cursor = data.get("cursor")
        if not data.get("hasMore", False) or cursor is None:
            break


# ---------------------------------------------------------------------------
# Main: Full Lifecycle
# ---------------------------------------------------------------------------

async def main():
    print("=== Lenny Python Client Example ===\n")

    # 1. Authenticate
    print("1. Authenticating...")
    token = await get_access_token()
    print(f"   Token obtained: {token[:20]}...")

    async with httpx.AsyncClient(
        base_url=LENNY_URL,
        headers={"Authorization": f"Bearer {token}"},
        timeout=30.0,
    ) as client:

        # 2. Discover runtimes
        print("\n2. Discovering runtimes...")
        response = await lenny_request(client, "GET", "/v1/runtimes")
        runtimes = response.json()
        for rt in runtimes["items"]:
            print(f"   - {rt['name']} (type: {rt.get('type', 'unknown')})")

        runtime_name = runtimes["items"][0]["name"] if runtimes["items"] else "claude-worker"

        # 3. Create session
        print(f"\n3. Creating session with runtime '{runtime_name}'...")
        response = await lenny_request(
            client,
            "POST",
            "/v1/sessions",
            json={
                "runtime": runtime_name,
                "labels": {"example": "python-client"},
                "retryPolicy": {
                    "mode": "auto_then_client",
                    "maxRetries": 2,
                },
            },
        )
        session = response.json()
        session_id = session["sessionId"]
        upload_token = session["uploadToken"]
        print(f"   Session: {session_id}")
        print(f"   Isolation: {session['sessionIsolationLevel']}")

        # 4. Upload files
        print("\n4. Uploading files...")
        response = await lenny_request(
            client,
            "POST",
            f"/v1/sessions/{session_id}/upload",
            files=[
                ("files", ("example.py", b'def greet(name):\n    return f"Hello, {name}!"\n', "application/octet-stream")),
                ("files", ("README.md", b"# Example Project\n\nA simple greeting function.\n", "application/octet-stream")),
            ],
            headers={"X-Upload-Token": upload_token},
        )
        uploaded = response.json()
        print(f"   Uploaded: {[f['path'] for f in uploaded['uploaded']]}")

        # 5. Finalize workspace
        print("\n5. Finalizing workspace...")
        response = await lenny_request(
            client,
            "POST",
            f"/v1/sessions/{session_id}/finalize",
            headers={"X-Upload-Token": upload_token},
        )
        print(f"   State: {response.json()['state']}")

        # 6. Start session
        print("\n6. Starting session...")
        response = await lenny_request(
            client,
            "POST",
            f"/v1/sessions/{session_id}/start",
        )
        print(f"   State: {response.json()['state']}")

        # 7. Send a message
        print("\n7. Sending message...")
        response = await lenny_request(
            client,
            "POST",
            f"/v1/sessions/{session_id}/messages",
            json={
                "input": [
                    {
                        "type": "text",
                        "inline": "Review the code in example.py. Suggest improvements for error handling and documentation.",
                    }
                ]
            },
        )
        print(f"   Delivery: {response.json()['deliveryReceipt']['status']}")

        # 8. Stream output
        print("\n8. Streaming output:")
        print("-" * 40)
        await stream_session(client, session_id)
        print("-" * 40)

        # 9. Retrieve artifacts
        print("\n9. Retrieving artifacts...")
        async for artifact in paginate(client, f"/v1/sessions/{session_id}/artifacts"):
            print(f"   - {artifact['path']} ({artifact['size']} bytes)")

        # 10. Get usage
        print("\n10. Getting usage...")
        response = await lenny_request(
            client,
            "GET",
            f"/v1/sessions/{session_id}/usage",
        )
        usage = response.json()
        print(f"    Input tokens:  {usage['inputTokens']}")
        print(f"    Output tokens: {usage['outputTokens']}")
        print(f"    Wall clock:    {usage['wallClockSeconds']}s")

        # 11. Get transcript
        print("\n11. Getting transcript...")
        count = 0
        async for entry in paginate(client, f"/v1/sessions/{session_id}/transcript"):
            count += 1
        print(f"    {count} transcript entries")

        # 12. Terminate (already completed if session_complete fired)
        print("\n12. Checking final state...")
        response = await lenny_request(
            client,
            "GET",
            f"/v1/sessions/{session_id}",
        )
        final_state = response.json()["state"]
        print(f"    Final state: {final_state}")

        if final_state not in ("completed", "failed", "cancelled", "expired"):
            print("    Terminating session...")
            await lenny_request(
                client,
                "POST",
                f"/v1/sessions/{session_id}/terminate",
            )
            print("    Terminated.")

    print("\n=== Done ===")


if __name__ == "__main__":
    asyncio.run(main())
```

---

## Sync Variant (requests)

For simpler use cases that do not need async:

```python
"""
Simple synchronous Lenny client using requests.

pip install requests
"""

import requests
import time
import json

LENNY_URL = "https://lenny.example.com"
TOKEN = "your-access-token"

session = requests.Session()
session.headers.update({
    "Authorization": f"Bearer {TOKEN}",
    "Content-Type": "application/json",
})


def create_and_run_session(runtime: str, code: str, prompt: str) -> dict:
    """Create a session, send a prompt, wait for completion, return results."""

    # Create + start in one call
    response = session.post(f"{LENNY_URL}/v1/sessions/start", json={
        "runtime": runtime,
        "inlineFiles": [{"path": "code.py", "content": code}],
        "message": {"input": [{"type": "text", "inline": prompt}]},
    })
    response.raise_for_status()
    data = response.json()
    session_id = data["sessionId"]
    print(f"Session: {session_id}")

    # Poll for completion
    while True:
        response = session.get(f"{LENNY_URL}/v1/sessions/{session_id}")
        response.raise_for_status()
        state = response.json()["state"]

        if state in ("completed", "failed", "cancelled", "expired"):
            break

        time.sleep(2)

    # Get results
    results = {
        "state": state,
        "usage": session.get(f"{LENNY_URL}/v1/sessions/{session_id}/usage").json(),
        "transcript": session.get(
            f"{LENNY_URL}/v1/sessions/{session_id}/transcript"
        ).json(),
    }

    return results


# Usage
results = create_and_run_session(
    runtime="claude-worker",
    code='def add(a, b):\n    return a + b\n',
    prompt="Review this function and suggest improvements.",
)
print(f"State: {results['state']}")
print(f"Tokens: {results['usage']['inputTokens']} in, {results['usage']['outputTokens']} out")
```

---

## Webhook Receiver (FastAPI)

See the [Webhooks](../webhooks.html) page for a complete FastAPI webhook receiver example.
