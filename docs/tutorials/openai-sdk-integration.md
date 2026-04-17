---
layout: default
title: "OpenAI SDK Integration"
parent: Tutorials
nav_order: 6
---

# OpenAI SDK Integration

**Persona:** Client Developer | **Difficulty:** Intermediate

Lenny exposes an OpenAI Chat Completions-compatible API. This means you can point the standard OpenAI Python or TypeScript SDK at Lenny's gateway and use familiar `openai.chat.completions.create()` calls. Behind the scenes, Lenny creates a session, starts a runtime, delivers your messages, and streams output back through the Completions API format.

This tutorial shows you how to configure the OpenAI SDK, list models (mapped to runtimes), create chat completions, and handle streaming responses.

## Prerequisites

- Lenny running locally via `make run` or `docker compose up`
- Python: `pip install openai` (v1.0+)
- TypeScript/Node.js: `npm install openai` (v4.0+)

---

## Step 1: Point the OpenAI SDK at Lenny

The OpenAI SDK accepts a `base_url` parameter that overrides the default `https://api.openai.com/v1`. Point it at Lenny's gateway:

### Python

```python
from openai import OpenAI

# In dev mode, authentication is disabled -- use any string as the API key.
# In production, use your Lenny bearer token. See
# [Authentication](../client-guide/authentication.md) for how to obtain the
# initial token and rotate it via /v1/oauth/token (RFC 8693).
client = OpenAI(
    base_url="http://localhost:8080/v1",
    api_key="dev-mode-no-auth-needed",
)
```

### TypeScript

```typescript
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "http://localhost:8080/v1",
  apiKey: "dev-mode-no-auth-needed",
});
```

That is all the configuration needed. Every subsequent `client` call goes to Lenny instead of OpenAI.

---

## Step 2: List Models (Runtimes)

In Lenny, "models" map to registered runtimes. The `GET /v1/models` endpoint returns all runtimes you are authorized to use, formatted as OpenAI model objects.

### Python

```python
models = client.models.list()

print("Available models (runtimes):")
for model in models.data:
    print(f"  - {model.id}: {model.owned_by}")
```

### TypeScript

```typescript
const models = await client.models.list();

console.log("Available models (runtimes):");
for (const model of models.data) {
  console.log(`  - ${model.id}: ${model.owned_by}`);
}
```

### Expected Output

```
Available models (runtimes):
  - echo: lenny
```

Each runtime is surfaced as a "model" with its name as the model ID. The `owned_by` field is always `"lenny"`.

---

## Step 3: Create a Chat Completion

The `POST /v1/chat/completions` endpoint maps to a full Lenny session lifecycle: session creation, runtime start, message delivery, response collection, and session termination -- all in a single API call.

### Python

```python
response = client.chat.completions.create(
    model="echo",
    messages=[
        {"role": "user", "content": "What is the meaning of life?"}
    ],
)

print(f"Model: {response.model}")
print(f"Response: {response.choices[0].message.content}")
print(f"Finish reason: {response.choices[0].finish_reason}")
print(f"Usage: {response.usage}")
```

### TypeScript

```typescript
const response = await client.chat.completions.create({
  model: "echo",
  messages: [{ role: "user", content: "What is the meaning of life?" }],
});

console.log(`Model: ${response.model}`);
console.log(`Response: ${response.choices[0].message.content}`);
console.log(`Finish reason: ${response.choices[0].finish_reason}`);
console.log(`Usage:`, response.usage);
```

### Expected Output

```
Model: echo
Response: [1] Echo: What is the meaning of life?
Finish reason: stop
Usage: CompletionUsage(completion_tokens=12, prompt_tokens=8, total_tokens=20)
```

**What happens behind the scenes:**

1. The gateway creates a session with the `echo` runtime
2. Starts the runtime
3. Sends your message
4. Waits for the response
5. Terminates the session
6. Returns the response in OpenAI Chat Completions format

---

## Step 4: Handle Streaming Responses

For real-time output, use streaming mode. The gateway streams SSE events formatted as OpenAI Chat Completion chunks.

### Python

```python
stream = client.chat.completions.create(
    model="echo",
    messages=[
        {"role": "user", "content": "Tell me a story about Lenny."}
    ],
    stream=True,
)

print("Streaming response:")
full_response = ""
for chunk in stream:
    if chunk.choices[0].delta.content:
        content = chunk.choices[0].delta.content
        full_response += content
        print(content, end="", flush=True)

print(f"\n\nFull response: {full_response}")
```

### TypeScript

```typescript
const stream = await client.chat.completions.create({
  model: "echo",
  messages: [{ role: "user", content: "Tell me a story about Lenny." }],
  stream: true,
});

console.log("Streaming response:");
let fullResponse = "";

for await (const chunk of stream) {
  const content = chunk.choices[0]?.delta?.content;
  if (content) {
    fullResponse += content;
    process.stdout.write(content);
  }
}

console.log(`\n\nFull response: ${fullResponse}`);
```

### Expected SSE Stream

The raw SSE stream looks like standard OpenAI chunks:

```
data: {"id":"chatcmpl-sess_01","object":"chat.completion.chunk","created":1717430400,"model":"echo","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-sess_01","object":"chat.completion.chunk","created":1717430400,"model":"echo","choices":[{"index":0,"delta":{"content":"[1] Echo: Tell me a story about Lenny."},"finish_reason":null}]}

data: {"id":"chatcmpl-sess_01","object":"chat.completion.chunk","created":1717430400,"model":"echo","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

---

## Step 5: Multi-Turn Conversations

You can send multiple messages in the `messages` array to simulate a conversation history. The gateway delivers all messages to the runtime in sequence.

### Python

```python
response = client.chat.completions.create(
    model="echo",
    messages=[
        {"role": "system", "content": "You are a helpful calculator."},
        {"role": "user", "content": "What is 2+2?"},
        {"role": "assistant", "content": "4"},
        {"role": "user", "content": "And what is that times 3?"},
    ],
)

print(f"Response: {response.choices[0].message.content}")
```

### TypeScript

```typescript
const response = await client.chat.completions.create({
  model: "echo",
  messages: [
    { role: "system", content: "You are a helpful calculator." },
    { role: "user", content: "What is 2+2?" },
    { role: "assistant", content: "4" },
    { role: "user", content: "And what is that times 3?" },
  ],
});

console.log(`Response: ${response.choices[0].message.content}`);
```

**How Lenny handles conversation history:** The gateway creates a fresh session for each completions call (unless you use the session continuation extension -- see below). The full message history is delivered to the runtime as the initial context, and the runtime responds to the last user message. The session is terminated after the response.

---

## Step 6: Session Continuation (Lenny Extension)

Standard OpenAI Chat Completions are stateless -- each call creates a new session. Lenny provides an extension header to continue an existing session across multiple completions calls:

### Python

```python
# First call -- create a new session
response1 = client.chat.completions.create(
    model="echo",
    messages=[
        {"role": "user", "content": "Remember the number 42."}
    ],
    extra_headers={
        # Leave empty to create a new session that persists
        "X-Lenny-Session-Mode": "persistent",
    },
)

# The response includes the session ID in a custom header
session_id = response1._response.headers.get("X-Lenny-Session-Id")
print(f"Session ID: {session_id}")

# Second call -- continue the same session
response2 = client.chat.completions.create(
    model="echo",
    messages=[
        {"role": "user", "content": "What number did I ask you to remember?"}
    ],
    extra_headers={
        "X-Lenny-Session-Id": session_id,
    },
)

print(f"Response: {response2.choices[0].message.content}")

# Clean up -- terminate the persistent session
import requests
requests.post(
    f"http://localhost:8080/v1/sessions/{session_id}/terminate",
    json={},
)
```

### TypeScript

```typescript
// First call -- create a persistent session
const response1 = await client.chat.completions.create({
  model: "echo",
  messages: [{ role: "user", content: "Remember the number 42." }],
  // @ts-ignore -- custom header
  headers: {
    "X-Lenny-Session-Mode": "persistent",
  },
});

// Extract session ID from response headers
const sessionId = response1._response?.headers.get("x-lenny-session-id");
console.log(`Session ID: ${sessionId}`);

// Second call -- continue the session
const response2 = await client.chat.completions.create({
  model: "echo",
  messages: [
    { role: "user", content: "What number did I ask you to remember?" },
  ],
  // @ts-ignore
  headers: {
    "X-Lenny-Session-Id": sessionId,
  },
});

console.log(`Response: ${response2.choices[0].message.content}`);
```

---

## Step 7: Error Handling

The OpenAI SDK raises standard exceptions for API errors. Lenny maps its errors to OpenAI-compatible error codes:

### Python

```python
from openai import OpenAIError, BadRequestError, NotFoundError

try:
    response = client.chat.completions.create(
        model="nonexistent-runtime",
        messages=[{"role": "user", "content": "Hello"}],
    )
except NotFoundError as e:
    print(f"Model not found: {e.message}")
    # Output: Model not found: Runtime 'nonexistent-runtime' not found
except BadRequestError as e:
    print(f"Bad request: {e.message}")
except OpenAIError as e:
    print(f"API error: {e}")
```

### TypeScript

```typescript
try {
  const response = await client.chat.completions.create({
    model: "nonexistent-runtime",
    messages: [{ role: "user", content: "Hello" }],
  });
} catch (error) {
  if (error instanceof OpenAI.NotFoundError) {
    console.log(`Model not found: ${error.message}`);
  } else if (error instanceof OpenAI.BadRequestError) {
    console.log(`Bad request: ${error.message}`);
  } else if (error instanceof OpenAI.APIError) {
    console.log(`API error: ${error.message}`);
  }
}
```

### Error Code Mapping

| Lenny Error | OpenAI HTTP Status | OpenAI Error Type |
|-------------|-------------------|-------------------|
| Runtime not found | 404 | `NotFoundError` |
| Pool exhausted | 503 | `APIError` |
| Rate limited | 429 | `RateLimitError` |
| Invalid input | 400 | `BadRequestError` |
| Auth failure | 401 | `AuthenticationError` |
| Budget exhausted | 429 | `RateLimitError` |

---

## Limitations: What is Different from Real OpenAI

The OpenAI Completions adapter provides compatibility, not identity. Here are the key differences:

| Feature | OpenAI | Lenny via OpenAI SDK |
|---------|--------|---------------------|
| **Models** | GPT-4, GPT-3.5, etc. | Registered Lenny runtimes |
| **Function calling** | Native function_call support | Not passed through -- tools are handled inside the runtime |
| **Logprobs** | Supported | Not supported |
| **Token counting** | Precise | Approximate (depends on runtime reporting) |
| **Statefulness** | Stateless | Optional persistence via `X-Lenny-Session-Id` |
| **Max tokens** | `max_tokens` parameter | Mapped to session token budget |
| **Temperature** | Controls randomness | Passed to runtime but behavior depends on implementation |
| **System messages** | First-class | Delivered as the first message to the runtime |
| **Tool calls in response** | `tool_calls` in response | Not in the completions response -- tools execute inside the runtime |
| **Structured output** | JSON mode | Depends on runtime implementation |

### When to Use MCP Instead

Use the [MCP integration](mcp-client-integration) when you need:

- **Session persistence** -- MCP sessions are naturally long-lived
- **Elicitation** -- human-in-the-loop prompts
- **Delegation visibility** -- observing task trees
- **Tool approval** -- approving/denying tool calls
- **Fine-grained state control** -- interrupt, suspend, resume

The OpenAI SDK integration is best for:

- **Quick integration** -- drop-in replacement for OpenAI calls
- **Existing codebases** -- applications already using the OpenAI SDK
- **Simple request/response** -- stateless question-answer patterns
- **Streaming output** -- real-time text streaming with familiar API

---

## Complete Example

### Python

```python
from openai import OpenAI

def main():
    client = OpenAI(
        base_url="http://localhost:8080/v1",
        api_key="dev-mode",
    )

    # List available runtimes as "models"
    print("=== Available Models ===")
    for model in client.models.list().data:
        print(f"  {model.id}")

    # Non-streaming completion
    print("\n=== Non-Streaming ===")
    response = client.chat.completions.create(
        model="echo",
        messages=[
            {"role": "user", "content": "Hello, Lenny!"}
        ],
    )
    print(f"  {response.choices[0].message.content}")

    # Streaming completion
    print("\n=== Streaming ===")
    stream = client.chat.completions.create(
        model="echo",
        messages=[
            {"role": "user", "content": "Stream this response."}
        ],
        stream=True,
    )
    print("  ", end="")
    for chunk in stream:
        content = chunk.choices[0].delta.content
        if content:
            print(content, end="", flush=True)
    print()

    print("\nDone.")

if __name__ == "__main__":
    main()
```

### TypeScript

```typescript
import OpenAI from "openai";

async function main() {
  const client = new OpenAI({
    baseURL: "http://localhost:8080/v1",
    apiKey: "dev-mode",
  });

  // List available runtimes as "models"
  console.log("=== Available Models ===");
  const models = await client.models.list();
  for (const model of models.data) {
    console.log(`  ${model.id}`);
  }

  // Non-streaming completion
  console.log("\n=== Non-Streaming ===");
  const response = await client.chat.completions.create({
    model: "echo",
    messages: [{ role: "user", content: "Hello, Lenny!" }],
  });
  console.log(`  ${response.choices[0].message.content}`);

  // Streaming completion
  console.log("\n=== Streaming ===");
  const stream = await client.chat.completions.create({
    model: "echo",
    messages: [{ role: "user", content: "Stream this response." }],
    stream: true,
  });
  process.stdout.write("  ");
  for await (const chunk of stream) {
    const content = chunk.choices[0]?.delta?.content;
    if (content) {
      process.stdout.write(content);
    }
  }
  console.log();

  console.log("\nDone.");
}

main();
```

---

## Next Steps

- [MCP Client Integration](mcp-client-integration) -- full MCP protocol integration with elicitation and delegation
- [Your First Session](first-session) -- learn the REST API for fine-grained session control
