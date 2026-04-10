---
layout: default
title: "Your First Session"
parent: Tutorials
nav_order: 1
---

# Your First Session

**Persona:** Client Developer | **Difficulty:** Beginner

In this tutorial you will create a Lenny session from scratch, upload workspace files, start a runtime, send messages, stream output, retrieve artifacts, and terminate the session. Every step includes examples in curl, Python, and TypeScript so you can follow along in whichever language you prefer.

## Prerequisites

- Lenny running locally via `make run` (see [Local Development Mode](../getting-started/local-dev))
- The echo runtime is available (it ships by default with `make run`)
- curl, Python 3.10+, or Node.js 18+ installed

Throughout this tutorial the gateway is at `http://localhost:8080`. In `make run` dev mode, authentication is disabled, so no bearer token is needed. In a production deployment you would add `-H "Authorization: Bearer $TOKEN"` to every request.

---

## Step 1: Create a Session

A session is the fundamental unit of interaction in Lenny. Creating one claims a warm pod, assigns credentials (if any), and returns a `session_id` you will use for all subsequent calls.

### curl

```bash
curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "echo",
    "metadata": {
      "description": "My first Lenny session"
    }
  }' | jq .
```

### Python

```python
import requests

resp = requests.post("http://localhost:8080/v1/sessions", json={
    "runtime": "echo",
    "metadata": {
        "description": "My first Lenny session"
    }
})
resp.raise_for_status()
session = resp.json()
print(session)
```

### TypeScript

```typescript
const resp = await fetch("http://localhost:8080/v1/sessions", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    runtime: "echo",
    metadata: {
      description: "My first Lenny session",
    },
  }),
});
const session = await resp.json();
console.log(session);
```

### Expected Response

```json
{
  "session_id": "sess_01J5K9ABCDEF",
  "uploadToken": "sess_01J5K9ABCDEF.1717430700.a3f1c7e2d9b8",
  "sessionIsolationLevel": {
    "executionMode": "session",
    "isolationProfile": "runc",
    "podReuse": false
  }
}
```

**State transition:** The session is now in the `created` state. A warm pod has been claimed from the pool and credentials (if the runtime needs them) have been assigned. The session will remain in `created` for up to 300 seconds (configurable via `maxCreatedStateTimeoutSeconds`), after which it expires.

**Important:** The `uploadToken` is a secret credential. Do not log it, embed it in URLs, or include it in error reports. You need it for the upload and finalize steps below.

---

## Step 2: Upload Workspace Files

Before the runtime starts, you can populate its workspace with files. Uploads use multipart form data and require the `uploadToken` from Step 1.

### curl

```bash
SESSION_ID="sess_01J5K9ABCDEF"
UPLOAD_TOKEN="sess_01J5K9ABCDEF.1717430700.a3f1c7e2d9b8"

# Create a sample file
echo "Hello from Lenny!" > /tmp/greeting.txt

curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/upload" \
  -H "Authorization: UploadToken ${UPLOAD_TOKEN}" \
  -F "files=@/tmp/greeting.txt;filename=greeting.txt" | jq .
```

### Python

```python
session_id = session["session_id"]
upload_token = session["uploadToken"]

with open("/tmp/greeting.txt", "w") as f:
    f.write("Hello from Lenny!")

resp = requests.post(
    f"http://localhost:8080/v1/sessions/{session_id}/upload",
    headers={"Authorization": f"UploadToken {upload_token}"},
    files={"files": ("greeting.txt", open("/tmp/greeting.txt", "rb"))},
)
resp.raise_for_status()
print(resp.json())
```

### TypeScript

```typescript
const sessionId = session.session_id;
const uploadToken = session.uploadToken;

const formData = new FormData();
formData.append("files", new Blob(["Hello from Lenny!"]), "greeting.txt");

const uploadResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/upload`,
  {
    method: "POST",
    headers: { Authorization: `UploadToken ${uploadToken}` },
    body: formData,
  }
);
console.log(await uploadResp.json());
```

### Expected Response

```json
{
  "uploaded": [
    {
      "path": "greeting.txt",
      "size": 18,
      "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
    }
  ]
}
```

**What happens internally:** The file is written to `/workspace/staging` on the pod. It is not yet visible to the runtime. The gateway validates the file path (no `..`, no absolute paths, no symlinks) and checks size limits before accepting the upload.

You can upload multiple files and even tar.gz archives in a single call. The gateway extracts archives automatically.

---

## Step 3: Finalize the Workspace

Finalizing moves uploaded files from the staging area to `/workspace/current` (the runtime's working directory) and runs any setup commands defined in the runtime configuration.

### curl

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/finalize" \
  -H "Authorization: UploadToken ${UPLOAD_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{}' | jq .
```

### Python

```python
resp = requests.post(
    f"http://localhost:8080/v1/sessions/{session_id}/finalize",
    headers={"Authorization": f"UploadToken {upload_token}"},
    json={},
)
resp.raise_for_status()
print(resp.json())
```

### TypeScript

```typescript
const finalizeResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/finalize`,
  {
    method: "POST",
    headers: {
      Authorization: `UploadToken ${uploadToken}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({}),
  }
);
console.log(await finalizeResp.json());
```

### Expected Response

```json
{
  "state": "ready",
  "workspace": {
    "files": ["greeting.txt"],
    "totalSize": 18
  },
  "setupOutput": ""
}
```

**State transition:** The session transitions from `created` to `finalizing` (while setup commands run) and then to `ready` (setup complete, awaiting start). The `uploadToken` is now consumed and cannot be reused.

**Setup commands** are time-bounded and logged. If a setup command fails, the session transitions to `failed`. The echo runtime has no setup commands, so finalization is instant.

---

## Step 4: Start the Runtime

Starting the runtime launches the agent binary inside the pod. The session becomes interactive.

### curl

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/start" \
  -H "Content-Type: application/json" \
  -d '{}' | jq .
```

### Python

```python
resp = requests.post(
    f"http://localhost:8080/v1/sessions/{session_id}/start",
    json={},
)
resp.raise_for_status()
print(resp.json())
```

### TypeScript

```typescript
const startResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/start`,
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  }
);
console.log(await startResp.json());
```

### Expected Response

```json
{
  "state": "running",
  "startedAt": "2026-04-09T10:30:00Z"
}
```

**State transition:** The session moves from `ready` to `starting` (the adapter spawns the agent binary) and then to `running` (the binary is active and reading from stdin). The session is now fully interactive.

---

## Step 5: Send a Message

With the session running, you can send messages to the agent. The echo runtime echoes them back with a sequence number.

### curl

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{
    "input": [
      {
        "type": "text",
        "inline": "What is 2 + 2?"
      }
    ]
  }' | jq .
```

### Python

```python
resp = requests.post(
    f"http://localhost:8080/v1/sessions/{session_id}/messages",
    json={
        "input": [
            {"type": "text", "inline": "What is 2 + 2?"}
        ]
    },
)
resp.raise_for_status()
print(resp.json())
```

### TypeScript

```typescript
const msgResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/messages`,
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      input: [{ type: "text", inline: "What is 2 + 2?" }],
    }),
  }
);
console.log(await msgResp.json());
```

### Expected Response

```json
{
  "messageId": "msg_001",
  "deliveryReceipt": {
    "messageId": "msg_001",
    "status": "delivered",
    "deliveredAt": "2026-04-09T10:30:01Z"
  }
}
```

**How it works internally:** The gateway wraps your input in a `MessageEnvelope` and writes it as a JSON line to the runtime's stdin. The echo runtime reads the message, generates a response, and writes it to stdout. The adapter relays the response back through the gateway to you.

The `status: "delivered"` receipt means the runtime consumed the message from stdin. Other possible statuses include `queued` (buffered in the session inbox) and `rate_limited`.

---

## Step 6: Stream Output via SSE

To receive the runtime's output in real time, open an SSE (Server-Sent Events) stream on the session's logs endpoint.

### curl

```bash
curl -s -N "http://localhost:8080/v1/sessions/${SESSION_ID}/logs" \
  -H "Accept: text/event-stream"
```

### Python

```python
import sseclient  # pip install sseclient-py

resp = requests.get(
    f"http://localhost:8080/v1/sessions/{session_id}/logs",
    headers={"Accept": "text/event-stream"},
    stream=True,
)

client = sseclient.SSEClient(resp)
for event in client.events():
    print(f"Event: {event.event}")
    print(f"Data:  {event.data}")
    print("---")
    # Break after receiving a response for this demo
    if '"type":"agent_output"' in event.data:
        break
```

### TypeScript

```typescript
const eventSource = new EventSource(
  `http://localhost:8080/v1/sessions/${sessionId}/logs`
);

eventSource.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log("Event type:", data.type);
  console.log("Data:", JSON.stringify(data, null, 2));
};

eventSource.onerror = (err) => {
  console.error("SSE error:", err);
  eventSource.close();
};
```

### Expected SSE Events

```
event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"[1] Echo: What is 2 + 2?"}]}

event: status_change
data: {"type":"status_change","state":"running"}
```

**Reconnection:** If the connection drops, reconnect with the `Last-Event-ID` header set to your last seen cursor. The gateway replays missed events from the EventStore (within the replay window, typically 20 minutes). If your cursor falls outside the window, you receive a `checkpoint_boundary` marker instead.

---

## Step 7: Retrieve Artifacts

After the session produces output (files, logs, etc.), you can retrieve them as artifacts. The echo runtime does not produce file artifacts, but the API is the same for all runtimes.

### curl

```bash
# List all artifacts
curl -s "http://localhost:8080/v1/sessions/${SESSION_ID}/artifacts" | jq .

# Download the full workspace snapshot
curl -s "http://localhost:8080/v1/sessions/${SESSION_ID}/workspace" \
  -o workspace.tar.gz
```

### Python

```python
# List artifacts
resp = requests.get(
    f"http://localhost:8080/v1/sessions/{session_id}/artifacts"
)
resp.raise_for_status()
artifacts = resp.json()
print("Artifacts:", artifacts)

# Download workspace snapshot
resp = requests.get(
    f"http://localhost:8080/v1/sessions/{session_id}/workspace"
)
resp.raise_for_status()
with open("workspace.tar.gz", "wb") as f:
    f.write(resp.content)
print(f"Downloaded workspace: {len(resp.content)} bytes")
```

### TypeScript

```typescript
// List artifacts
const artifactsResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/artifacts`
);
const artifacts = await artifactsResp.json();
console.log("Artifacts:", artifacts);

// Download workspace snapshot
const wsResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/workspace`
);
const wsBlob = await wsResp.blob();
console.log(`Downloaded workspace: ${wsBlob.size} bytes`);
```

### Expected Response (artifact list)

```json
{
  "artifacts": [
    {
      "path": "greeting.txt",
      "size": 18,
      "type": "file",
      "createdAt": "2026-04-09T10:30:00Z"
    }
  ]
}
```

You can also download individual artifacts by path:

```bash
curl -s "http://localhost:8080/v1/sessions/${SESSION_ID}/artifacts/greeting.txt"
# Output: Hello from Lenny!
```

---

## Step 8: Get the Session Transcript

The transcript contains the full conversation history -- every message sent and received during the session.

### curl

```bash
curl -s "http://localhost:8080/v1/sessions/${SESSION_ID}/transcript" | jq .
```

### Python

```python
resp = requests.get(
    f"http://localhost:8080/v1/sessions/{session_id}/transcript"
)
resp.raise_for_status()
transcript = resp.json()
for entry in transcript.get("entries", []):
    print(f"[{entry['role']}] {entry['content']}")
```

### TypeScript

```typescript
const transcriptResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/transcript`
);
const transcript = await transcriptResp.json();
for (const entry of transcript.entries ?? []) {
  console.log(`[${entry.role}] ${entry.content}`);
}
```

### Expected Response

```json
{
  "entries": [
    {
      "role": "user",
      "content": "What is 2 + 2?",
      "timestamp": "2026-04-09T10:30:01Z",
      "messageId": "msg_001"
    },
    {
      "role": "assistant",
      "content": "[1] Echo: What is 2 + 2?",
      "timestamp": "2026-04-09T10:30:01Z",
      "messageId": "msg_002"
    }
  ],
  "pagination": {
    "hasMore": false,
    "cursor": "cur_002"
  }
}
```

The transcript is paginated. For long conversations, pass `?cursor=cur_002` to get subsequent pages.

---

## Step 9: Terminate the Session

When you are done, terminate the session gracefully. This seals the workspace, exports artifacts to durable storage, and releases the pod.

### curl

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/terminate" \
  -H "Content-Type: application/json" \
  -d '{}' | jq .
```

### Python

```python
resp = requests.post(
    f"http://localhost:8080/v1/sessions/{session_id}/terminate",
    json={},
)
resp.raise_for_status()
print(resp.json())
```

### TypeScript

```typescript
const termResp = await fetch(
  `http://localhost:8080/v1/sessions/${sessionId}/terminate`,
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({}),
  }
);
console.log(await termResp.json());
```

### Expected Response

```json
{
  "state": "completed",
  "completedAt": "2026-04-09T10:35:00Z"
}
```

**State transition:** The session moves from `running` to `completed`. The gateway:

1. Sends a `shutdown` message to the runtime on stdin
2. Waits for the runtime to exit (within the deadline)
3. Seals the workspace and exports it to the artifact store
4. Releases the credential lease
5. Releases the pod back to the warm pool for draining and cleanup

After termination, you can still access artifacts, the transcript, and session metadata -- they are retained for the configured TTL (default 7 days). You can extend retention via `POST /v1/sessions/{id}/extend-retention`.

---

## Complete Session Lifecycle Summary

Here is the full sequence of states your session went through:

```
created           (pod claimed, credentials assigned, awaiting uploads)
  |
  v
finalizing        (staging files moved to /workspace/current, setup commands run)
  |
  v
ready             (setup complete, awaiting runtime start)
  |
  v
starting          (adapter spawning agent binary)
  |
  v
running           (interactive session, accepting messages)
  |
  v
completed         (gracefully terminated, workspace sealed, pod released)
```

Other possible terminal states are `failed` (unrecoverable error), `cancelled` (explicitly cancelled), and `expired` (deadline or budget exhausted).

---

## Convenience: One-Shot Session

For simple use cases, Lenny provides a convenience endpoint that combines creation, inline file upload, and start in a single call:

```bash
curl -s -X POST http://localhost:8080/v1/sessions/start \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "echo",
    "input": [
      {"type": "text", "inline": "Quick hello!"}
    ],
    "metadata": {
      "description": "One-shot session"
    }
  }' | jq .
```

This returns a running session with the first message already delivered. It skips the separate upload and finalize steps, which is useful for sessions that do not need workspace files.

---

## Next Steps

- [Build a Runtime Adapter](build-a-runtime) -- create your own agent runtime
- [MCP Client Integration](mcp-client-integration) -- connect via the Model Context Protocol
- [OpenAI SDK Integration](openai-sdk-integration) -- use the OpenAI SDK with Lenny
