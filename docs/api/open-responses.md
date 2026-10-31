---
layout: default
title: Open Responses API
parent: "API Reference"
nav_order: 4
---

# Open Responses API

The `OpenResponsesAdapter` provides support for the **Open Responses** specification -- an open standard based on OpenAI's Responses API. Clients built against the OpenAI Responses API or the Open Responses specification can connect to Lenny without modification.

---

## Overview

| | |
|:--|:--|
| **Adapter** | `OpenResponsesAdapter` |
| **Path prefix** | `/v1/responses` |
| **Protocol** | Open Responses Specification |
| **Status** | Built-in, always available |

### What is Open Responses?

**Open Responses** is an open specification modeled on OpenAI's Responses API. The relationship:

- **OpenAI's Responses API** is a superset -- it includes proprietary hosted tools (web search, code interpreter, file search) that are OpenAI-specific.
- **Open Responses** is the open-standard subset -- it defines the wire format, streaming protocol, and core semantics without proprietary extensions.

Lenny implements the Open Responses specification. OpenAI Responses API clients work against Lenny for all standard operations, but Lenny does not implement OpenAI's proprietary hosted tools.

**Authentication.** Requests use `Authorization: Bearer <access-token>`. Rotate or exchange tokens via the canonical [`/v1/oauth/token`](./admin.md#post-v1oauthtoken) RFC 8693 endpoint.

**Upstream provider credentials.** When the selected runtime uses proxy-mode credential delivery, the gateway talks to the LLM provider on behalf of the agent pod. The pod sends its request to the gateway carrying only a short-lived lease token, and the gateway rewrites the request with the real provider credentials before forwarding it. Real provider API keys never reach the agent pod and are never written to disk -- they are kept only in the gateway process's in-memory cache, so credential rotation does not interrupt traffic. See [LLM Proxy security](../operator-guide/security.md#llm-proxy).

---

## Endpoint

### POST /v1/responses

Create a response. A request without `previous_response_id` creates a conversation session, runs the specified runtime, and returns the result in Open Responses format. A request with `previous_response_id` continues the conversation, either on the session that produced that response or on a new session, as [Multi-turn via `previous_response_id`](#multi-turn-via-previous_response_id) describes.

**Request format:**

```json
{
  "model": "claude-worker",
  "input": "Write a function to parse JSON in Go.",
  "instructions": "You are an expert Go developer. Write clean, idiomatic code.",
  "stream": true,
  "temperature": 0.7,
  "max_output_tokens": 4096
}
```

**Request parameters:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `model` | string | Yes | Runtime name registered in Lenny |
| `input` | string or array | Yes | Input text or array of input items. When a string, treated as a single user message. When an array, each item is a message with `role` and `content`. |
| `instructions` | string | No | System-level instructions for the agent |
| `stream` | boolean | No | Enable streaming (SSE). Default: `false`. |
| `temperature` | number | No | Sampling temperature. Passed through to the runtime. |
| `max_output_tokens` | integer | No | Maximum tokens in the response |
| `top_p` | number | No | Nucleus sampling parameter |
| `metadata` | object | No | Arbitrary key-value metadata attached to the response |
| `previous_response_id` | string | No | Chain to a previous response for multi-turn conversation |
| `store` | boolean | No | Whether to store the response for later retrieval. Default: `true`. |
| `truncation` | string | No | Truncation strategy for long conversations: `auto` or `disabled`. Default: `disabled`. |

### Input formats

**Simple string input:**

```json
{
  "model": "claude-worker",
  "input": "What is Kubernetes?"
}
```

**Structured array input:**

```json
{
  "model": "claude-worker",
  "input": [
    {
      "role": "user",
      "content": "Write a Python script to process CSV files."
    }
  ]
}
```

**Multi-turn with previous_response_id:**

```json
{
  "model": "claude-worker",
  "input": [
    {
      "role": "user",
      "content": "Now add error handling for malformed rows."
    }
  ],
  "previous_response_id": "resp_01J5K9..."
}
```

---

## Response format

### Non-streaming response

```json
{
  "id": "resp_01J5K9...",
  "object": "response",
  "created_at": 1712650800,
  "model": "claude-worker",
  "status": "completed",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "content": [
        {
          "type": "output_text",
          "text": "Here's a Go function to parse JSON:\n\n```go\npackage main\n\nimport (\n\t\"encoding/json\"\n\t\"fmt\"\n)\n\nfunc parseJSON(data []byte) (map[string]interface{}, error) {\n\tvar result map[string]interface{}\n\terr := json.Unmarshal(data, &result)\n\treturn result, err\n}\n```"
        }
      ]
    }
  ],
  "usage": {
    "input_tokens": 18,
    "output_tokens": 95,
    "total_tokens": 113
  },
  "metadata": {}
}
```

### Streaming response

When `stream: true`, the response uses **Server-Sent Events (SSE)**:

```
event: response.created
data: {"type":"response.created","response":{"id":"resp_01J5K9...","object":"response","status":"in_progress","model":"claude-worker","output":[]}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Here"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"'s a"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":" Go function"}

...

event: response.output_text.done
data: {"type":"response.output_text.done","output_index":0,"content_index":0,"text":"Here's a Go function to parse JSON:\n\n```go\n...```"}

event: response.content_part.done
data: {"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"..."}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"..."}]}}

event: response.completed
data: {"type":"response.completed","response":{"id":"resp_01J5K9...","status":"completed","output":[...],"usage":{"input_tokens":18,"output_tokens":95,"total_tokens":113}}}
```

Streaming events follow the Open Responses event type taxonomy:
- `response.created` -- initial response object
- `response.output_item.added` -- new output item started
- `response.content_part.added` -- new content part within an item
- `response.output_text.delta` -- incremental text content
- `response.output_text.done` -- text content complete
- `response.content_part.done` -- content part complete
- `response.output_item.done` -- output item complete
- `response.completed` -- final response with usage stats

---

## Session mapping

Each Open Responses conversation maps to a Lenny session. The first request creates the session, and the session keeps its pod between turns, so the runtime keeps its memory of the conversation.

```
POST /v1/responses (no previous_response_id)
  |
  v
create_session(runtime=model)
  |
  v
finalize_workspace()  (empty workspace)
  |
  v
start_session(message=input)
  |
  v
[stream output or wait for completion]
  |
  v
Return Open Responses format response
(the session stays running and keeps its pod)
```

The session follows the same idle, suspension, and maximum-age limits as any other Lenny session. A session that stays idle past the idle limit is suspended and releases its pod. A pool that runs in service mode keeps no conversation context between requests, and a runtime that is not `multi_turn` with injection enabled for the tenant accepts no second message, so in either case the session ends after every response.

### Response status mapping

A response's status comes from its own turn. Later changes to the session, such as suspension or expiry, do not change the status of a response that was already returned.

| Turn outcome | Response status |
|:-------------|:----------------|
| The turn is running | `in_progress` |
| The turn's output was recorded | `completed` |

`GET /v1/responses/{id}` returns completed responses. An id that names no completed response that the caller can read returns `404`.

### Multi-turn via `previous_response_id`

When `previous_response_id` names the newest response of a conversation session that is still running, the gateway sends the new input to the same session and pod, and the runtime continues from its own memory.

In every other case the gateway starts a new session and seeds it with the stored conversation up to the named response. This covers a continuation of an earlier response (a fork), a second continuation sent while another continuation of the same response is in flight, and a continuation sent after the session was suspended, restored onto a new pod, or ended. The client sees the same result in both cases.

Response ids are opaque strings. Clients store and send them back unchanged and do not parse them.

---

## What Lenny implements vs. what's OpenAI-proprietary

| Feature | Open Responses (Lenny) | OpenAI Responses API |
|:--------|:----------------------|:--------------------|
| Text generation | Supported | Supported |
| Streaming (SSE) | Supported | Supported |
| Multi-turn (`previous_response_id`) | Supported | Supported |
| System instructions | Supported | Supported |
| Temperature, max_output_tokens | Supported | Supported |
| Metadata | Supported | Supported |
| Response storage and retrieval | Supported | Supported |
| **Web search tool** | Not implemented (OpenAI-proprietary) | Supported |
| **Code interpreter tool** | Not implemented (OpenAI-proprietary) | Supported |
| **File search tool** | Not implemented (OpenAI-proprietary) | Supported |
| **Computer use tool** | Not implemented (OpenAI-proprietary) | Supported |
| **MCP connectors** | Not via this adapter (use MCP API) | Supported (recent addition) |
| **Reasoning** | Output includes reasoning if the runtime produces it, but `reasoning_trace` MessageParts are mapped to `output_text` with a role annotation; type is lossy. | Native `reasoning` output type |
| **Background mode** | Mapped to Lenny's async session with `callbackUrl` | Native background execution |

Requests that reference OpenAI-proprietary tools (`web_search`, `code_interpreter`, `file_search`, `computer_use`) return `400 VALIDATION_ERROR` with a message indicating the tool is not supported.

---

## Task lifecycle integration

The Open Responses adapter participates in Lenny's full session lifecycle:

- **Delegation:** If the runtime performs delegation (spawning sub-agents), the delegation tree is invisible to the Responses API client. The final output is the aggregated result.
- **Elicitation:** If the runtime requests human input, the adapter surfaces it as a response event. However, the standard Responses API does not have a native elicitation mechanism -- clients using the Responses API for workflows that require elicitation should consider the [MCP API](mcp.html) instead.
- **Checkpointing:** Sessions are checkpointed per normal Lenny policy. A turn that fails after its input reached the runtime returns a server error, and the gateway ends that session; a continuation of an earlier completed response then starts a new session.
- **Token budgets:** The `max_output_tokens` parameter is mapped to the session's token budget.

---

## Examples

### curl

```bash
# Non-streaming
curl -X POST https://lenny.example.com/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-oidc-token>" \
  -d '{
    "model": "claude-worker",
    "input": "Explain the CAP theorem in distributed systems.",
    "max_output_tokens": 2048
  }'

# Streaming
curl -X POST https://lenny.example.com/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-oidc-token>" \
  -N \
  -d '{
    "model": "claude-worker",
    "input": "Write a REST API in Go with proper error handling.",
    "stream": true,
    "temperature": 0.5
  }'
```

### Python

```python
import httpx
import json

BASE_URL = "https://lenny.example.com/v1"
TOKEN = "<your-oidc-token>"
HEADERS = {
    "Content-Type": "application/json",
    "Authorization": f"Bearer {TOKEN}",
}

# Non-streaming
response = httpx.post(
    f"{BASE_URL}/responses",
    headers=HEADERS,
    json={
        "model": "claude-worker",
        "input": "What are Go interfaces?",
        "instructions": "Explain with code examples.",
        "max_output_tokens": 2048,
    },
)
result = response.json()
for item in result["output"]:
    if item["type"] == "message":
        for part in item["content"]:
            if part["type"] == "output_text":
                print(part["text"])

# Streaming
with httpx.stream(
    "POST",
    f"{BASE_URL}/responses",
    headers=HEADERS,
    json={
        "model": "claude-worker",
        "input": "Write a Dockerfile for a Go application.",
        "stream": True,
    },
) as stream:
    for line in stream.iter_lines():
        if line.startswith("data: "):
            data = json.loads(line[6:])
            if data["type"] == "response.output_text.delta":
                print(data["delta"], end="", flush=True)

# Multi-turn conversation
first = httpx.post(
    f"{BASE_URL}/responses",
    headers=HEADERS,
    json={
        "model": "claude-worker",
        "input": "Write a sorting algorithm in Python.",
    },
).json()

followup = httpx.post(
    f"{BASE_URL}/responses",
    headers=HEADERS,
    json={
        "model": "claude-worker",
        "input": "Now optimize it for nearly-sorted arrays.",
        "previous_response_id": first["id"],
    },
).json()
```

### TypeScript

```typescript
const BASE_URL = "https://lenny.example.com/v1";
const TOKEN = "<your-oidc-token>";

// Non-streaming
const response = await fetch(`${BASE_URL}/responses`, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: `Bearer ${TOKEN}`,
  },
  body: JSON.stringify({
    model: "claude-worker",
    input: "Explain Kubernetes networking.",
    max_output_tokens: 2048,
  }),
});

const result = await response.json();
for (const item of result.output) {
  if (item.type === "message") {
    for (const part of item.content) {
      if (part.type === "output_text") {
        console.log(part.text);
      }
    }
  }
}

// Streaming
const stream = await fetch(`${BASE_URL}/responses`, {
  method: "POST",
  headers: {
    "Content-Type": "application/json",
    Authorization: `Bearer ${TOKEN}`,
  },
  body: JSON.stringify({
    model: "claude-worker",
    input: "Write a Helm chart for a web application.",
    stream: true,
  }),
});

const reader = stream.body!.getReader();
const decoder = new TextDecoder();

while (true) {
  const { done, value } = await reader.read();
  if (done) break;

  const text = decoder.decode(value);
  for (const line of text.split("\n")) {
    if (line.startsWith("data: ")) {
      const data = JSON.parse(line.slice(6));
      if (data.type === "response.output_text.delta") {
        process.stdout.write(data.delta);
      }
    }
  }
}
```

---

## Error handling

Errors follow Lenny's standard [error format](index.html#error-format). The adapter maps Lenny errors to appropriate HTTP status codes:

| Lenny error code | HTTP status | Description |
|:-----------------|:------------|:------------|
| `VALIDATION_ERROR` | 400 | Invalid request parameters |
| `RESOURCE_NOT_FOUND` | 404 | Unknown model (runtime) |
| `RATE_LIMITED` | 429 | Request rate limit exceeded |
| `QUOTA_EXCEEDED` | 429 | Token or session quota exceeded |
| `RUNTIME_UNAVAILABLE` | 503 | No pods available for the runtime |
| `WARM_POOL_EXHAUSTED` | 503 | No idle pods in pool |
| `INTERNAL_ERROR` | 500 | Unexpected server error |

When streaming, errors that occur mid-stream are delivered as an error event:

```
event: error
data: {"type":"error","code":"POD_CRASH","message":"Session pod terminated unexpectedly.","retryable":true}
```
