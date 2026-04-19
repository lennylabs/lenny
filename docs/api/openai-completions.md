---
layout: default
title: OpenAI Completions API
parent: "API Reference"
nav_order: 3
---

# OpenAI Completions API

The `OpenAICompletionsAdapter` accepts the OpenAI Chat Completions wire format. Point an existing OpenAI SDK client at Lenny and use any registered runtime as a model; the only code change required is updating the base URL.

---

## Overview

| | |
|:--|:--|
| **Adapter** | `OpenAICompletionsAdapter` |
| **Path prefix** | `/v1/chat/completions` (completions endpoint), `/v1/models` (model list) |
| **Protocol** | OpenAI Chat Completions (streaming and non-streaming) |
| **Status** | V1 (built-in, always available) |

The adapter translates between OpenAI's Chat Completions wire format and Lenny's internal session lifecycle. Each completions request creates a Lenny session, runs it to completion (or streams output), and returns the result in standard OpenAI format.

**Authentication.** All requests must carry `Authorization: Bearer <access-token>`. Obtain the initial token from your identity provider (OIDC); rotate it via the canonical [`/v1/oauth/token`](./admin.md#post-v1oauthtoken) RFC 8693 endpoint.

**Upstream provider credentials.** When the chosen runtime is configured for proxy-mode credential delivery, the gateway talks to LLM providers (Anthropic, Bedrock, Vertex, Azure OpenAI) on behalf of the agent pod. The pod calls the gateway with only a short-lived lease token; the gateway rewrites the request with the real provider credentials before forwarding. Your provider API key never reaches the agent pod and is never written to disk -- it is kept only in the gateway process's in-memory cache, so credential rotation does not interrupt traffic. See [LLM Proxy security](../operator-guide/security.md#llm-proxy).

---

## Endpoints

### POST /v1/chat/completions

Send a chat completion request. The gateway creates a session, runs the specified runtime, and returns the agent's output in OpenAI Chat Completions format.

**Request format:**

```json
{
  "model": "claude-worker",
  "messages": [
    {
      "role": "system",
      "content": "You are a helpful coding assistant."
    },
    {
      "role": "user",
      "content": "Write a Python function to compute the Fibonacci sequence."
    }
  ],
  "stream": true,
  "temperature": 0.7,
  "max_tokens": 4096
}
```

**Supported parameters:**

| Parameter | Type | Required | Description |
|:----------|:-----|:---------|:------------|
| `model` | string | Yes | Runtime name registered in Lenny. Maps to a Lenny runtime, not an LLM model directly. |
| `messages` | array | Yes | Array of message objects with `role` and `content`. Supported roles: `system`, `user`, `assistant`. |
| `stream` | boolean | No | Enable streaming (SSE). Default: `false`. |
| `temperature` | number | No | Sampling temperature. Passed through to the runtime as session metadata. |
| `max_tokens` | integer | No | Maximum tokens in the response. Mapped to the session's token budget. |
| `stop` | string or array | No | Stop sequences. Passed to the runtime as session metadata. |
| `top_p` | number | No | Nucleus sampling parameter. Passed through as metadata. |
| `n` | integer | No | Number of completions. Only `n=1` is supported; values > 1 return `400`. |
| `presence_penalty` | number | No | Presence penalty. Passed through as metadata. |
| `frequency_penalty` | number | No | Frequency penalty. Passed through as metadata. |
| `user` | string | No | End-user identifier for tracking. |

### Non-streaming response

```json
{
  "id": "chatcmpl-sess_01J5K9...",
  "object": "chat.completion",
  "created": 1712650800,
  "model": "claude-worker",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "Here's a Python function for the Fibonacci sequence:\n\n```python\ndef fibonacci(n):\n    if n <= 1:\n        return n\n    a, b = 0, 1\n    for _ in range(2, n + 1):\n        a, b = b, a + b\n    return b\n```"
      },
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 28,
    "completion_tokens": 85,
    "total_tokens": 113
  }
}
```

### Streaming response

When `stream: true`, the response uses **Server-Sent Events (SSE)**:

```
data: {"id":"chatcmpl-sess_01J5K9...","object":"chat.completion.chunk","created":1712650800,"model":"claude-worker","choices":[{"index":0,"delta":{"role":"assistant","content":"Here"},"finish_reason":null}]}

data: {"id":"chatcmpl-sess_01J5K9...","object":"chat.completion.chunk","created":1712650800,"model":"claude-worker","choices":[{"index":0,"delta":{"content":"'s a"},"finish_reason":null}]}

data: {"id":"chatcmpl-sess_01J5K9...","object":"chat.completion.chunk","created":1712650800,"model":"claude-worker","choices":[{"index":0,"delta":{"content":" Python"},"finish_reason":null}]}

...

data: {"id":"chatcmpl-sess_01J5K9...","object":"chat.completion.chunk","created":1712650800,"model":"claude-worker","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

Each chunk follows the standard OpenAI delta format. The stream ends with the `[DONE]` sentinel.

---

### GET /v1/models

List available runtimes as OpenAI-compatible models. Results are **identity-filtered** -- users only see runtimes they have access to.

**Response format:**

```json
{
  "object": "list",
  "data": [
    {
      "id": "claude-worker",
      "object": "model",
      "created": 1712600000,
      "owned_by": "lenny"
    },
    {
      "id": "gpt-4-turbo",
      "object": "model",
      "created": 1712600000,
      "owned_by": "lenny"
    },
    {
      "id": "code-runner",
      "object": "model",
      "created": 1712600000,
      "owned_by": "lenny"
    }
  ]
}
```

Each runtime registered in Lenny appears as a "model" in this list. The `id` field is the runtime name used in the `model` parameter of completions requests.

---

## How runtimes map to models

When the adapter receives a completions request:

1. The `model` field is resolved to a **Lenny runtime name**. If no runtime matches, the gateway returns `404`.
2. A **Lenny session** is created using that runtime.
3. The `messages` array is translated to a `MessageEnvelope` with the conversation history as input `OutputPart` arrays.
4. The session runs to completion. Agent output is translated back to OpenAI chat completion format.
5. Token usage reported by the runtime is mapped to the `usage` object.

The runtime does the actual LLM interaction internally -- the adapter does not call any LLM API directly. The `temperature`, `max_tokens`, and other parameters are passed through to the runtime as session metadata; it is up to the runtime to honor them.

---

## Under the hood: session lifecycle mapping

Each OpenAI Completions request maps to a full Lenny session lifecycle:

```
POST /v1/chat/completions
  |
  v
create_session(runtime=model)
  |
  v
finalize_workspace()  (empty workspace)
  |
  v
start_session(message=messages)
  |
  v
[stream output or wait for completion]
  |
  v
terminate_session()
  |
  v
Return OpenAI-format response
```

For streaming requests, the adapter streams `OutputPart` chunks as they arrive from the runtime, translating each to an OpenAI delta chunk.

---

## Limitations vs. real OpenAI

| Feature | OpenAI | Lenny |
|:--------|:-------|:------|
| Function calling / tools | Supported (`tools` parameter) | Not supported. Tools are runtime-internal; the adapter does not pass through OpenAI's `tools` parameter. The runtime may use tools internally, but they do not appear in the completions response. |
| Multiple completions (`n > 1`) | Supported | Not supported. Only `n=1` is allowed. |
| Session semantics | Each request is stateless | Each request creates a new Lenny session with full lifecycle (pod allocation, workspace setup, teardown). |
| Fine-tuned models | Supported (`ft:model-name`) | Not supported. The `model` parameter maps to Lenny runtime names, not fine-tuned model IDs. |
| Embeddings | `POST /v1/embeddings` | Not supported. No embeddings endpoint. |
| Logprobs | `logprobs` parameter | Not supported. |
| Vision (image inputs) | Supported via `image_url` content | Supported. Image content in messages is translated to `OutputPart` with `type: image`. |
| Response format (`json_mode`) | Supported | Passed through as runtime metadata. Runtime support varies. |
| Seed (deterministic) | Supported | Passed through as runtime metadata. Determinism depends on the runtime. |

---

## Examples

### Python with the `openai` package

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://lenny.example.com/v1",
    api_key="<your-oidc-token>",
)

# Non-streaming
response = client.chat.completions.create(
    model="claude-worker",
    messages=[
        {"role": "system", "content": "You are a helpful coding assistant."},
        {"role": "user", "content": "Write a Python function to sort a list."},
    ],
    temperature=0.7,
    max_tokens=2048,
)

print(response.choices[0].message.content)

# Streaming
stream = client.chat.completions.create(
    model="claude-worker",
    messages=[
        {"role": "user", "content": "Explain Kubernetes pods in simple terms."},
    ],
    stream=True,
)

for chunk in stream:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

### TypeScript with the `openai` package

```typescript
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://lenny.example.com/v1",
  apiKey: "<your-oidc-token>",
});

// Non-streaming
const response = await client.chat.completions.create({
  model: "claude-worker",
  messages: [
    { role: "system", content: "You are a helpful coding assistant." },
    { role: "user", content: "Write a Go HTTP server." },
  ],
  max_tokens: 4096,
});

console.log(response.choices[0].message.content);

// Streaming
const stream = await client.chat.completions.create({
  model: "claude-worker",
  messages: [
    { role: "user", content: "Explain container orchestration." },
  ],
  stream: true,
});

for await (const chunk of stream) {
  const content = chunk.choices[0]?.delta?.content;
  if (content) process.stdout.write(content);
}
```

### curl

```bash
# Non-streaming
curl -X POST https://lenny.example.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-oidc-token>" \
  -d '{
    "model": "claude-worker",
    "messages": [
      {"role": "user", "content": "What is Kubernetes?"}
    ],
    "max_tokens": 1024
  }'

# Streaming
curl -X POST https://lenny.example.com/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <your-oidc-token>" \
  -N \
  -d '{
    "model": "claude-worker",
    "messages": [
      {"role": "user", "content": "Write a haiku about containers."}
    ],
    "stream": true
  }'
```

---

## Migration guide: OpenAI to Lenny

Moving an application from the OpenAI API to Lenny requires minimal changes:

### Step 1: Update the base URL

```python
# Before (OpenAI)
client = OpenAI()

# After (Lenny)
client = OpenAI(
    base_url="https://lenny.example.com/v1",
    api_key="<your-oidc-token>",
)
```

### Step 2: Update the model name

Replace OpenAI model names with Lenny runtime names:

```python
# Before
model="gpt-4-turbo"

# After
model="claude-worker"  # or whatever runtime you registered
```

Use `GET /v1/models` to list available runtimes.

### Step 3: Remove unsupported parameters

Remove any parameters Lenny does not support:

```python
# Remove these:
# tools=[...]          -- not supported
# tool_choice=...      -- not supported
# logprobs=True        -- not supported
# n=3                  -- only n=1 supported
```

### Step 4: Handle session semantics

Be aware that each request creates a Lenny session:
- Requests are **not** stateless. Each request allocates a pod, sets up a workspace, and tears it down.
- Latency is higher than a direct LLM call because of session lifecycle overhead.
- For conversational use cases with multiple turns, consider using the [MCP API](mcp.html) or [REST API](rest) instead, which support multi-turn sessions natively.

### Step 5: Update error handling

Lenny returns the same HTTP status codes as OpenAI for common errors, but error bodies use Lenny's [error format](index.html#error-format). Update your error handling if you parse error response bodies:

```python
try:
    response = client.chat.completions.create(...)
except openai.RateLimitError as e:
    # Status 429 -- same as OpenAI, but body uses Lenny error format
    # e.response.json() returns {"error": {"code": "RATE_LIMITED", ...}}
    pass
except openai.APIStatusError as e:
    # Handle Lenny-specific errors
    error = e.response.json().get("error", {})
    if error.get("code") == "WARM_POOL_EXHAUSTED":
        # Retry with backoff
        pass
```
