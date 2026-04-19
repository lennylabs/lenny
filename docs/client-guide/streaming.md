---
layout: default
title: Streaming
parent: "Client Guide"
nav_order: 3
---

# Streaming

Lenny supports multiple streaming mechanisms for receiving real-time output from agent sessions. This page covers SSE log streaming, Streamable HTTP (MCP transport), WebSocket attachment, event types, reconnection handling, and backpressure.

> **Session output vs. operational events.** This page covers **session output streaming**: real-time agent tokens, tool calls, and results consumed by the end-client. **Operational event streaming**, the platform-level events consumed by operators and agent-operability tools, uses a separate endpoint (`/v1/admin/events/stream`) and a different wire format (CloudEvents v1.0.2). See the [CloudEvents catalog](../reference/cloudevents-catalog.md) for operational-event consumption. The two streams are distinct.

---

## Streaming Options

| Mechanism | Endpoint / Transport | Use Case |
|---|---|---|
| SSE (Server-Sent Events) | `GET /v1/sessions/{id}/logs` with `Accept: text/event-stream` | Real-time log/event streaming via REST |
| Streamable HTTP (MCP) | `/mcp` endpoint | Interactive MCP sessions with bidirectional streaming |
| WebSocket | Session attachment | Full bidirectional session interaction |

---

## SSE Log Streaming

To receive real-time output from a session, send a GET request with `Accept: text/event-stream`:

```
GET /v1/sessions/{id}/logs
Accept: text/event-stream
Authorization: Bearer <token>
```

The server responds with an SSE stream. Each event is a newline-delimited frame:

```
event: agent_output
data: {"output": [{"type": "text", "inline": "Analyzing the codebase..."}]}

event: tool_use_requested
data: {"tool_call_id": "tc_001", "tool": "read_file", "args": {"path": "main.py"}}

event: tool_result
data: {"tool_call_id": "tc_001", "result": {"content": [{"type": "text", "inline": "..."}]}}

event: status_change
data: {"state": "running"}

event: session_complete
data: {"result": {"output": [{"type": "text", "inline": "Review complete."}]}}
```

### Event Types

| Event | Description |
|---|---|
| `agent_output` | Streaming output from the agent. Contains `output: OutputPart[]`. |
| `tool_use_requested` | Agent wants to call a tool. Contains `tool_call_id`, `tool`, `args`. |
| `tool_result` | Result of a tool call. Contains `tool_call_id`, `result`. |
| `elicitation_request` | Agent or tool needs user input. Contains `elicitation_id`, `schema`, `message`. |
| `status_change` | Session state transition. Contains `state` (e.g., `suspended`, `input_required`). |
| `session.resumed` | Session resumed from checkpoint. Contains `resumeMode` (`full` or `conversation_only`) and `workspaceLost` (boolean). |
| `children_reattached` | Parent session resumed with active children. Contains array of `ReattachedChild` objects. |
| `error` | Error with classification. Contains `code`, `message`, `transient` (boolean). |
| `session_complete` | Session finished. Contains `result`. |
| `checkpoint_boundary` | Client's cursor fell outside the replay window. Contains `cursor`, `events_lost`, `reason`. |
| `session_expiring_soon` | Sent 5 minutes before `maxSessionAge` expires. |

### OutputPart Types

The `agent_output` event's `output` field contains an array of `OutputPart` objects:

| Type | Description |
|---|---|
| `text` | Plain or formatted text. Fields: `type`, `inline` (string), `mimeType` (`text/plain`). |
| `code` | Source code. Fields: `type`, `inline`, `mimeType`, `annotations.language`. |
| `reasoning_trace` | Model reasoning/chain-of-thought. Fields: `type`, `inline`. |
| `citation` | Source citation or reference. Fields: `type`, `inline`, `annotations.source`. |
| `screenshot` / `image` | Image content. Fields: `type`, `inline` (base64) or `ref` (`lenny-blob://`), `mimeType` (`image/*`). |
| `diff` | Code diff or patch. Fields: `type`, `inline`, `annotations.language: "diff"`. |
| `file` | File content (binary or text). Fields: `type`, `inline` or `ref`, `mimeType`. |
| `execution_result` | Compound output from code execution. Fields: `type`, `parts[]` (each entry is a nested `OutputPart`). |
| `error` | Error or diagnostic. Fields: `type`, `inline`, `annotations.errorCode` (optional). |

See the canonical type registry in [spec §15.4.1](../reference/glossary.html) for per-type field contracts, `schemaVersion`, and the `x-<vendor>/<typeName>` namespace convention for third-party types. Parts either embed bytes via `inline` or reference a blob via `ref` — never both (`OUTPUTPART_INLINE_REF_CONFLICT`). For REST clients, resolve `ref` via `GET /v1/blobs/{ref}`; MCP, OpenAI, and A2A adapters dereference refs internally and never expose `lenny-blob://` URIs to external callers.

---

## Streamable HTTP (MCP Transport)

MCP sessions use Streamable HTTP: SSE for server-to-client events, POST for client-to-server messages. The gateway acts as an MCP server at the `/mcp` endpoint.

Connection setup:

1. Client sends `POST /mcp` with an `initialize` request
2. Server responds with capabilities and negotiated protocol version
3. Client opens an SSE connection for server-to-client events
4. Client sends subsequent requests via `POST /mcp`

See the [MCP SDK Examples](sdk-examples/mcp-sdk.html) for complete code.

---

## Reconnection Handling

### SSE Reconnection

If the SSE connection drops (network issue, server restart, etc.):

1. The gateway persists an **event cursor** per session
2. On reconnect, provide your last-seen cursor via the `Last-Event-ID` header or `?cursor=` query parameter
3. The gateway replays missed events from the EventStore

**Replay window**: Events are available for replay for `max(periodicCheckpointIntervalSeconds * 2, 1200s)` (typically 20 minutes). If your cursor falls outside this window, the gateway sends a `checkpoint_boundary` event:

```
event: checkpoint_boundary
data: {"cursor": "cur_xyz", "events_lost": 42, "reason": "replay_window_exceeded", "checkpoint_timestamp": "2026-01-15T10:35:00Z"}
```

When `events_lost > 0`, your client has missed events. Treat this as a gap in event history; you may need to re-fetch session state via `GET /v1/sessions/{id}`.

### Reconnection Example

```bash
# First connection
curl -N "https://lenny.example.com/v1/sessions/sess_abc123/logs" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream"

# After disconnect, reconnect with last cursor
curl -N "https://lenny.example.com/v1/sessions/sess_abc123/logs?cursor=cur_abc" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream"
```

---

## Backpressure and Flow Control

The gateway uses a **bounded-error** policy for SSE connections:

- Each SSE connection has a send buffer
- If the client's read loop falls behind and the buffer fills, the gateway closes the connection within 100ms
- The client must reconnect with its last-seen cursor
- Missed events are replayed from the EventStore (if within the replay window)

A single slow client cannot cause unbounded memory growth in the gateway.

---

## Examples

### Python -- SSE Streaming with httpx

```python
import httpx
import json

LENNY_URL = "https://lenny.example.com"
TOKEN = "your-access-token"


async def stream_session_output(session_id: str):
    """Stream real-time output from a Lenny session using SSE."""
    last_cursor = None

    async with httpx.AsyncClient() as client:
        while True:
            url = f"{LENNY_URL}/v1/sessions/{session_id}/logs"
            params = {}
            if last_cursor:
                params["cursor"] = last_cursor

            try:
                async with client.stream(
                    "GET",
                    url,
                    headers={
                        "Authorization": f"Bearer {TOKEN}",
                        "Accept": "text/event-stream",
                    },
                    params=params,
                    timeout=None,  # SSE connections are long-lived
                ) as response:
                    response.raise_for_status()

                    event_type = None
                    data_lines = []

                    async for line in response.aiter_lines():
                        if line.startswith("event: "):
                            event_type = line[7:]
                        elif line.startswith("data: "):
                            data_lines.append(line[6:])
                        elif line.startswith("id: "):
                            last_cursor = line[4:]
                        elif line == "":
                            # End of event
                            if event_type and data_lines:
                                data = json.loads("\n".join(data_lines))
                                handle_event(event_type, data)

                                if event_type == "session_complete":
                                    return  # Session done

                            event_type = None
                            data_lines = []

            except httpx.ReadTimeout:
                print("Connection timed out, reconnecting...")
                continue
            except httpx.HTTPStatusError as e:
                if e.response.status_code == 401:
                    print("Token expired, refresh and retry")
                    return
                raise


def handle_event(event_type: str, data: dict):
    """Process a streaming event."""
    if event_type == "agent_output":
        for part in data.get("output", []):
            if part["type"] == "text":
                print(part.get("inline", ""), end="", flush=True)
    elif event_type == "status_change":
        print(f"\n[Status: {data['state']}]")
    elif event_type == "tool_use_requested":
        print(f"\n[Tool call: {data['tool']}({data['args']})]")
    elif event_type == "tool_result":
        print(f"\n[Tool result for {data['tool_call_id']}]")
    elif event_type == "elicitation_request":
        print(f"\n[Elicitation: {data['message']}]")
    elif event_type == "error":
        print(f"\n[Error: {data['code']} - {data['message']}]")
    elif event_type == "session_complete":
        print("\n[Session complete]")
    elif event_type == "checkpoint_boundary":
        if data.get("events_lost", 0) > 0:
            print(f"\n[WARNING: {data['events_lost']} events lost]")


# Usage
import asyncio
asyncio.run(stream_session_output("sess_abc123"))
```

### TypeScript -- SSE Streaming with fetch + ReadableStream

```typescript
const LENNY_URL = "https://lenny.example.com";
const TOKEN = "your-access-token";

async function streamSessionOutput(sessionId: string): Promise<void> {
  let lastCursor: string | null = null;

  while (true) {
    const url = new URL(`${LENNY_URL}/v1/sessions/${sessionId}/logs`);
    if (lastCursor) {
      url.searchParams.set("cursor", lastCursor);
    }

    const response = await fetch(url.toString(), {
      headers: {
        Authorization: `Bearer ${TOKEN}`,
        Accept: "text/event-stream",
      },
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status}: ${response.statusText}`);
    }

    const reader = response.body!.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    let eventType: string | null = null;
    let dataLines: string[] = [];

    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split("\n");
        buffer = lines.pop()!; // Keep incomplete line in buffer

        for (const line of lines) {
          if (line.startsWith("event: ")) {
            eventType = line.slice(7);
          } else if (line.startsWith("data: ")) {
            dataLines.push(line.slice(6));
          } else if (line.startsWith("id: ")) {
            lastCursor = line.slice(4);
          } else if (line === "") {
            // End of event
            if (eventType && dataLines.length > 0) {
              const data = JSON.parse(dataLines.join("\n"));
              handleEvent(eventType, data);

              if (eventType === "session_complete") {
                return; // Session done
              }
            }
            eventType = null;
            dataLines = [];
          }
        }
      }
    } catch (error) {
      console.log("Connection lost, reconnecting...");
      continue;
    }
  }
}

function handleEvent(eventType: string, data: any): void {
  switch (eventType) {
    case "agent_output":
      for (const part of data.parts ?? []) {
        if (part.type === "text") {
          process.stdout.write(part.text);
        }
      }
      break;
    case "status_change":
      console.log(`\n[Status: ${data.state}]`);
      break;
    case "tool_use_requested":
      console.log(`\n[Tool call: ${data.tool}]`);
      break;
    case "error":
      console.log(`\n[Error: ${data.code} - ${data.message}]`);
      break;
    case "session_complete":
      console.log("\n[Session complete]");
      break;
    case "checkpoint_boundary":
      if (data.events_lost > 0) {
        console.log(`\n[WARNING: ${data.events_lost} events lost]`);
      }
      break;
  }
}

// Usage
streamSessionOutput("sess_abc123");
```

### curl -- SSE Streaming

```bash
# Stream session output in real-time
# --no-buffer ensures curl doesn't buffer the SSE stream
curl -N "https://lenny.example.com/v1/sessions/sess_abc123/logs" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream"

# With cursor for reconnection
curl -N "https://lenny.example.com/v1/sessions/sess_abc123/logs?cursor=cur_abc" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream"
```
