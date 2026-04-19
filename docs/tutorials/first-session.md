---
layout: default
title: "Your First Session"
parent: Tutorials
nav_order: 1
---

# Your First Session

**For:** Client Developer | **Difficulty:** Beginner

In this tutorial you'll create a session, send a couple of messages, stream the live output, and shut the session down. Path A uses the `lenny session` CLI against a local `lenny up`, with no client code. Paths B through E show the same flow via REST, Python, and TypeScript as a template for your own application.

The examples use the `chat` runtime, which ships with every installation.

## Before you start

- Install the CLI: `brew install lennylabs/tap/lenny` (or grab a binary from the releases page).
- Run `lenny up`. It starts the whole platform on your machine and prints a gateway URL (`https://localhost:8443` by default) and a development token.
- For the SDK paths below, you'll also need Python 3.10+ or Node.js 18+.

`lenny up` handles authentication for you; the CLI and the web playground already know how to talk to it. To point the same commands at a deployed cluster, export `LENNY_GATEWAY=https://...` and `LENNY_TOKEN=...`.

---

## Path A: the `lenny session` CLI

The CLI drives a session by hand. Each command is a thin wrapper over the same API an SDK would use.

### Step 1: Start the session

```shell
lenny session new --runtime chat --message "hello, what is 2 + 2?"
```

You'll see something like:

```
session_id: sess_01J5K9ABCDEF
runtime:    chat
state:      running
message_id: msg_001
response:   4
```

**What just happened:** Lenny picked an idle pod for the `chat` runtime, set up an isolated workspace, started the agent, delivered your message, and streamed the reply back. The session stays open; you can keep talking to it.

### Step 2: Continue the conversation

```shell
lenny session send $SESSION_ID "and 10 times that?"
```

```
message_id: msg_002
response:   40
```

### Step 3: Watch the live stream

Open a second terminal:

```shell
lenny session logs $SESSION_ID --follow
```

Every agent message, state change, and question-to-the-user comes through this feed. Press `Ctrl+C` to stop watching; the session keeps running.

### Step 4: Upload a workspace (optional)

Some runtimes (for example `claude-code`) need files to work with. Upload any directory or archive:

```shell
mkdir -p /tmp/demo && echo "hello from a file" > /tmp/demo/greeting.txt
lenny session upload $SESSION_ID --path /tmp/demo
```

The CLI handles the multi-step upload and returns the files it accepted.

### Step 5: Inspect the session

```shell
lenny session get $SESSION_ID                 # metadata, current state, runtime
lenny session transcript $SESSION_ID          # the full conversation
lenny session artifacts $SESSION_ID           # files the session produced
```

### Step 6: Shut it down

```shell
lenny session cancel $SESSION_ID
```

```
state:       completed
completed_at: 2026-04-17T10:35:00Z
```

The transcript and any artifacts stay around for the retention period your operator configured (7 days by default). Extend it with `lenny session extend-retention $SESSION_ID --until 2026-05-01`.

---

## Path B: the web playground

Open `https://localhost:8443/playground` while `lenny up` is running. Pick the `chat` runtime, send a message, and watch the reply stream in. The playground uses the same public API as the CLI and SDKs. Operators can disable it with `playground.enabled=false` in their Helm values.

---

## Path C: Raw REST (curl)

Every CLI command is a thin wrapper around an HTTP endpoint. Call them directly when you're scripting automation, or working in a language without a Lenny SDK. `lenny up` serves the gateway at `https://localhost:8443`; swap that for your gateway URL and add `-H "Authorization: Bearer $TOKEN"` when you're hitting a deployed cluster.

### Start a session and send the first message in one call

```bash
curl -sk -X POST https://localhost:8443/v1/sessions/start \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "chat",
    "input": [{"type": "text", "inline": "hello, what is 2 + 2?"}]
  }' | jq .
```

Expected response:

```json
{
  "session_id": "sess_01J5K9ABCDEF",
  "state": "running",
  "startedAt": "2026-04-17T10:30:00Z"
}
```

### Follow-up message

```bash
SESSION_ID=sess_01J5K9ABCDEF

curl -sk -X POST "https://localhost:8443/v1/sessions/${SESSION_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{
    "input": [{"type": "text", "inline": "and 10 times that?"}]
  }' | jq .
```

### Stream the log feed

```bash
curl -skN "https://localhost:8443/v1/sessions/${SESSION_ID}/logs" \
  -H "Accept: text/event-stream"
```

The stream sends Server-Sent Events: `agent_output`, `status_change`, and (for runtimes that ask the human questions) `elicitation_requested`. If you get disconnected, include `Last-Event-ID` on the reconnect to resume.

### Upload and finalize a workspace

If you created the session with `POST /v1/sessions` (instead of the one-shot `start` endpoint), use the `uploadToken` from that response:

```bash
curl -sk -X POST "https://localhost:8443/v1/sessions/${SESSION_ID}/upload" \
  -H "X-Upload-Token: ${UPLOAD_TOKEN}" \
  -F "files=@/tmp/demo/greeting.txt;filename=greeting.txt"

curl -sk -X POST "https://localhost:8443/v1/sessions/${SESSION_ID}/finalize" \
  -H "X-Upload-Token: ${UPLOAD_TOKEN}" \
  -H "Content-Type: application/json" -d '{}'
```

### Terminate

```bash
curl -sk -X POST "https://localhost:8443/v1/sessions/${SESSION_ID}/terminate" \
  -H "Content-Type: application/json" -d '{}' | jq .
```

---

## Path D: Python SDK

Install: `pip install lenny-client`

```python
from lenny import Client

client = Client(base_url="https://localhost:8443", verify=False)  # dev only

session = client.sessions.start(
    runtime="chat",
    input=[{"type": "text", "inline": "hello, what is 2 + 2?"}],
)
print(session.id, session.state)

reply = client.sessions.message(
    session.id,
    input=[{"type": "text", "inline": "and 10 times that?"}],
)
print(reply.response)

for event in client.sessions.stream_logs(session.id):
    print(event.type, event.data)
    if event.type == "status_change" and event.data["state"] == "completed":
        break

client.sessions.terminate(session.id)
```

For a remote cluster, pass `token=...` and drop `verify=False`.

---

## Path E: TypeScript SDK

Install: `npm install @lennylabs/client`

```typescript
import { Client } from "@lennylabs/client";

const client = new Client({
  baseUrl: "https://localhost:8443",
  rejectUnauthorized: false, // dev only
});

const session = await client.sessions.start({
  runtime: "chat",
  input: [{ type: "text", inline: "hello, what is 2 + 2?" }],
});
console.log(session.id, session.state);

const reply = await client.sessions.message(session.id, {
  input: [{ type: "text", inline: "and 10 times that?" }],
});
console.log(reply.response);

for await (const event of client.sessions.streamLogs(session.id)) {
  console.log(event.type, event.data);
  if (event.type === "status_change" && event.data.state === "completed") break;
}

await client.sessions.terminate(session.id);
```

---

## The session lifecycle

Every path above walks through the same states:

```
created -> finalizing -> ready -> starting -> running -> completed
```

Sessions can also end in `failed` (unrecoverable error), `cancelled` (you asked it to stop), or `expired` (ran out of time or budget). The [Session Lifecycle](../client-guide/session-lifecycle.html) page has the full state diagram and every transition.

---

## Where to go next

- [MCP Client Integration](mcp-client-integration): plug Claude Desktop, Cursor, or your own MCP host into Lenny.
- [OpenAI SDK Integration](openai-sdk-integration): point existing OpenAI SDK code at Lenny by changing one URL.
- [Recursive Delegation](recursive-delegation): build workflows where one agent hands work to another, with budgets and scopes enforced.
- [Build a Runtime Adapter](build-a-runtime): write your own agent runtime.
