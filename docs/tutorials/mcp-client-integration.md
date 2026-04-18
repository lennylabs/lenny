---
layout: default
title: "MCP Client Integration"
parent: Tutorials
nav_order: 5
---

# MCP Client Integration

**Persona:** Client Developer | **Difficulty:** Intermediate

The Model Context Protocol (MCP) is Lenny's client-facing protocol. Lenny's gateway exposes an MCP Streamable HTTP endpoint that any MCP-compatible host can connect to. In this tutorial you will connect an MCP client to Lenny, discover runtimes, create sessions, send messages, handle elicitation (human-in-the-loop), and manage task state transitions.

## Prerequisites

- Lenny running locally via `lenny up` (recommended; see [Quickstart](../getting-started/quickstart)) or via `make run` / `docker compose up` for contributor dev loops
- Python 3.10+ with `mcp` package, or Node.js 18+ with `@modelcontextprotocol/sdk`
- Familiarity with [Your First Session](first-session)

---

## What is MCP

The [Model Context Protocol](https://spec.modelcontextprotocol.io/) is an open specification for communication between AI applications and their tools/resources. Lenny uses MCP as its client-facing protocol:

1. **Tasks:** MCP defines a task lifecycle (submitted, working, input_required, completed, failed, canceled) that maps to Lenny's session states
2. **Elicitation:** MCP supports structured human-in-the-loop prompts, used by Lenny for tool approval and user confirmation
3. **Streaming:** MCP Streamable HTTP provides server-to-client streaming for real-time output
4. **Tool discovery:** MCP's `tools/list` mechanism is used for runtime discovery
5. **Composability:** MCP servers can be nested, which maps to Lenny's recursive delegation model

---

## Step 1: Configure the MCP Server URL

Lenny's MCP endpoint is at `/mcp` on the gateway. In local dev mode:

```
http://localhost:8080/mcp
```

This is a **Streamable HTTP** endpoint (not stdio, not WebSocket). Your MCP client must support the Streamable HTTP transport.

---

## Step 2: Version Negotiation

Lenny's gateway supports **MCP 2025-03-26** (default) and **MCP 2024-11-05**. The version is negotiated during the MCP `initialize` handshake.

### Python

```python
from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client

async def connect_to_lenny():
    # Connect to Lenny's MCP endpoint via Streamable HTTP
    async with streamablehttp_client(
        url="http://localhost:8080/mcp",
        # In production, add headers={"Authorization": "Bearer <token>"}
    ) as (read_stream, write_stream, _):
        async with ClientSession(read_stream, write_stream) as session:
            # Initialize the MCP connection
            result = await session.initialize()

            print(f"Server: {result.serverInfo.name}")
            print(f"Protocol version: {result.protocolVersion}")
            print(f"Capabilities: {result.capabilities}")

            return session
```

### TypeScript

```typescript
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

async function connectToLenny(): Promise<Client> {
  const transport = new StreamableHTTPClientTransport(
    new URL("http://localhost:8080/mcp")
    // In production, pass headers: { Authorization: "Bearer <token>" }
  );

  const client = new Client({
    name: "my-app",
    version: "1.0.0",
  });

  await client.connect(transport);

  const serverInfo = client.getServerVersion();
  console.log("Server:", serverInfo?.name);
  console.log("Protocol version:", serverInfo?.protocolVersion);

  return client;
}
```

### Expected Output

```
Server: lenny-gateway
Protocol version: 2025-03-26
Capabilities: {tools: {listChanged: true}, ...}
```

---

## Step 3: Discover Runtimes

Lenny exposes runtime discovery through the `list_runtimes` MCP tool. The results are identity-filtered and policy-scoped: you only see runtimes you are authorized to use.

### Python

```python
async def discover_runtimes(session):
    # Call the list_runtimes tool
    result = await session.call_tool("list_runtimes", {})

    import json
    runtimes = json.loads(result.content[0].text)

    print("Available runtimes:")
    for rt in runtimes:
        print(f"  - {rt['name']}: {rt.get('description', 'No description')}")
        print(f"    Type: {rt['type']}")
        if rt.get('agentInterface'):
            print(f"    Skills: {[s['name'] for s in rt['agentInterface'].get('skills', [])]}")

    return runtimes
```

### TypeScript

```typescript
async function discoverRuntimes(client: Client) {
  const result = await client.callTool({
    name: "list_runtimes",
    arguments: {},
  });

  const runtimes = JSON.parse(result.content[0].text);

  console.log("Available runtimes:");
  for (const rt of runtimes) {
    console.log(`  - ${rt.name}: ${rt.description ?? "No description"}`);
    console.log(`    Type: ${rt.type}`);
    if (rt.agentInterface?.skills) {
      console.log(`    Skills: ${rt.agentInterface.skills.map((s: any) => s.name)}`);
    }
  }

  return runtimes;
}
```

### Expected Output

```
Available runtimes:
  - echo: Echo runtime for testing
    Type: agent
    Skills: []
```

The response also includes an `adapterCapabilities` block that tells you what protocol-level capabilities the active adapter supports:

```json
{
  "adapterCapabilities": {
    "supportsSessionContinuity": true,
    "supportsDelegation": true,
    "supportsElicitation": true,
    "supportsInterrupt": true
  }
}
```

Always check `supportsElicitation` before starting elicitation-dependent workflows.

---

## Step 4: Create and Start a Session

Use the `create_and_start_session` tool to create a session and start it in one call:

### Python

```python
async def create_session(session, runtime_name="echo"):
    result = await session.call_tool("create_and_start_session", {
        "runtime": runtime_name,
        "input": [
            {"type": "text", "inline": "Hello from MCP!"}
        ],
        "metadata": {
            "description": "MCP client tutorial session"
        }
    })

    import json
    session_info = json.loads(result.content[0].text)

    print(f"Session ID: {session_info['session_id']}")
    print(f"State: {session_info['state']}")

    return session_info
```

### TypeScript

```typescript
async function createSession(
  client: Client,
  runtimeName: string = "echo"
) {
  const result = await client.callTool({
    name: "create_and_start_session",
    arguments: {
      runtime: runtimeName,
      input: [{ type: "text", inline: "Hello from MCP!" }],
      metadata: {
        description: "MCP client tutorial session",
      },
    },
  });

  const sessionInfo = JSON.parse(result.content[0].text);
  console.log(`Session ID: ${sessionInfo.session_id}`);
  console.log(`State: ${sessionInfo.state}`);

  return sessionInfo;
}
```

### Expected Output

```
Session ID: sess_01J5K9MCP001
State: running
```

---

## Step 5: Attach to the Session

Attach to a running session to receive streaming events. The MCP adapter translates Lenny session events into MCP task state transitions.

### Python

```python
async def attach_session(session, session_id):
    # attach_session returns a streaming response via MCP
    result = await session.call_tool("attach_session", {
        "session_id": session_id,
    })

    import json
    attachment = json.loads(result.content[0].text)
    print(f"Attached to session: {attachment['session_id']}")
    print(f"Task state: {attachment['task_state']}")

    return attachment
```

### TypeScript

```typescript
async function attachSession(client: Client, sessionId: string) {
  const result = await client.callTool({
    name: "attach_session",
    arguments: { session_id: sessionId },
  });

  const attachment = JSON.parse(result.content[0].text);
  console.log(`Attached to session: ${attachment.session_id}`);
  console.log(`Task state: ${attachment.task_state}`);

  return attachment;
}
```

---

## Step 6: Send Messages and Receive Output

Send messages to the running session and process the streaming output:

### Python

```python
async def send_message(session, session_id, text):
    result = await session.call_tool("send_message", {
        "session_id": session_id,
        "input": [
            {"type": "text", "inline": text}
        ]
    })

    import json
    response = json.loads(result.content[0].text)

    print(f"Delivery status: {response['deliveryReceipt']['status']}")

    # Process output parts
    if "output" in response:
        for part in response["output"]:
            if part["type"] == "text":
                print(f"Agent: {part['inline']}")

    return response
```

### TypeScript

```typescript
async function sendMessage(
  client: Client,
  sessionId: string,
  text: string
) {
  const result = await client.callTool({
    name: "send_message",
    arguments: {
      session_id: sessionId,
      input: [{ type: "text", inline: text }],
    },
  });

  const response = JSON.parse(result.content[0].text);
  console.log(`Delivery status: ${response.deliveryReceipt.status}`);

  if (response.output) {
    for (const part of response.output) {
      if (part.type === "text") {
        console.log(`Agent: ${part.inline}`);
      }
    }
  }

  return response;
}
```

### Expected Output

```
Delivery status: delivered
Agent: [1] Echo: Hello from MCP!
```

---

## Step 7: Handle Elicitation (Human-in-the-Loop)

When a runtime needs user input (e.g., to approve a tool call or confirm an action), it sends an elicitation request through the MCP chain. Your client must handle these by prompting the user and responding.

### Python

```python
import asyncio

async def handle_elicitation(session, session_id):
    """
    Poll for elicitation requests and handle them.
    In a real application, you would register an event handler
    via the MCP streaming connection.
    """
    while True:
        # Check for pending elicitations
        result = await session.call_tool("get_session_state", {
            "session_id": session_id,
        })

        import json
        state = json.loads(result.content[0].text)

        if state.get("pendingElicitations"):
            for elicitation in state["pendingElicitations"]:
                elic_id = elicitation["elicitation_id"]
                schema = elicitation["schema"]

                print(f"\n--- Elicitation Request ---")
                print(f"ID: {elic_id}")
                print(f"Purpose: {elicitation.get('purpose', 'unknown')}")
                print(f"Origin: {elicitation.get('provenance', {}).get('origin_runtime', 'unknown')}")

                # Display the schema to the user
                if schema.get("type") == "object":
                    for field, field_schema in schema.get("properties", {}).items():
                        print(f"  {field}: {field_schema.get('description', '')}")

                # Get user input (in a real app, this would be a UI prompt)
                user_response = input("Your response (or 'dismiss' to cancel): ")

                if user_response.lower() == "dismiss":
                    # Dismiss the elicitation
                    await session.call_tool("dismiss_elicitation", {
                        "session_id": session_id,
                        "elicitation_id": elic_id,
                    })
                    print("Elicitation dismissed.")
                else:
                    # Respond to the elicitation
                    await session.call_tool("respond_to_elicitation", {
                        "session_id": session_id,
                        "elicitation_id": elic_id,
                        "response": user_response,
                    })
                    print("Response sent.")

        if state.get("state") in ["completed", "failed", "cancelled", "expired"]:
            break

        await asyncio.sleep(1)
```

### TypeScript

```typescript
async function handleElicitation(client: Client, sessionId: string) {
  const result = await client.callTool({
    name: "get_session_state",
    arguments: { session_id: sessionId },
  });

  const state = JSON.parse(result.content[0].text);

  if (state.pendingElicitations?.length > 0) {
    for (const elicitation of state.pendingElicitations) {
      console.log("\n--- Elicitation Request ---");
      console.log(`ID: ${elicitation.elicitation_id}`);
      console.log(`Purpose: ${elicitation.purpose ?? "unknown"}`);

      // In a real app, display the schema and collect user input
      const userResponse = "approved"; // placeholder

      await client.callTool({
        name: "respond_to_elicitation",
        arguments: {
          session_id: sessionId,
          elicitation_id: elicitation.elicitation_id,
          response: userResponse,
        },
      });

      console.log("Response sent.");
    }
  }
}
```

### Elicitation Provenance

Every elicitation includes provenance metadata so your UI can display where the request originated:

```json
{
  "elicitation_id": "elic_001",
  "schema": {"type": "string", "description": "Confirm operation"},
  "provenance": {
    "origin_pod": "sandbox-abc",
    "delegation_depth": 0,
    "origin_runtime": "my-agent",
    "purpose": "user_confirmation",
    "initiator_type": "agent"
  }
}
```

Display provenance in your UI. Connector-initiated elicitations (`initiator_type: "connector"`) carry higher trust than agent-initiated ones.

---

## Step 8: Handle Task State Transitions

MCP tasks go through defined state transitions. Your client should handle each state:

| Lenny Session State | MCP Task State | Client Action |
|---------------------|----------------|---------------|
| `running` | `working` | Display streaming output |
| `input_required` | `input-required` | Prompt user for input |
| `suspended` | (custom) | Show paused indicator |
| `completed` | `completed` | Display final result |
| `failed` | `failed` | Display error |
| `cancelled` | `canceled` | Display cancellation notice |
| `expired` | `failed` | Display expiration notice |

### Python: Session Handler

```python
async def run_complete_session(session):
    """Complete session lifecycle with state handling."""

    # 1. Discover runtimes
    runtimes = await discover_runtimes(session)

    # 2. Create and start a session
    session_info = await create_session(session, "echo")
    session_id = session_info["session_id"]

    # 3. Send a message
    response = await send_message(session, session_id, "Hello from MCP!")
    print(f"Response: {response}")

    # 4. Check for elicitations
    await handle_elicitation(session, session_id)

    # 5. Get the transcript
    result = await session.call_tool("get_transcript", {
        "session_id": session_id,
    })
    import json
    transcript = json.loads(result.content[0].text)
    print(f"\nTranscript ({len(transcript.get('entries', []))} entries):")
    for entry in transcript.get("entries", []):
        print(f"  [{entry['role']}] {entry['content']}")

    # 6. Terminate
    await session.call_tool("terminate_session", {
        "session_id": session_id,
    })
    print(f"\nSession terminated.")
```

### TypeScript: Session Handler

```typescript
async function runCompleteSession(client: Client) {
  // 1. Discover runtimes
  const runtimes = await discoverRuntimes(client);

  // 2. Create and start a session
  const sessionInfo = await createSession(client, "echo");
  const sessionId = sessionInfo.session_id;

  // 3. Send a message
  const response = await sendMessage(client, sessionId, "Hello from MCP!");
  console.log("Response:", response);

  // 4. Check for elicitations
  await handleElicitation(client, sessionId);

  // 5. Get the transcript
  const transcriptResult = await client.callTool({
    name: "get_transcript",
    arguments: { session_id: sessionId },
  });
  const transcript = JSON.parse(transcriptResult.content[0].text);
  console.log(`\nTranscript (${transcript.entries?.length ?? 0} entries):`);
  for (const entry of transcript.entries ?? []) {
    console.log(`  [${entry.role}] ${entry.content}`);
  }

  // 6. Terminate
  await client.callTool({
    name: "terminate_session",
    arguments: { session_id: sessionId },
  });
  console.log("\nSession terminated.");
}
```

---

## MCP Tool Reference

The following MCP tools are available on Lenny's gateway MCP endpoint:

| Tool | Description |
|------|-------------|
| `list_runtimes` | Discover available runtimes (identity-filtered, policy-scoped) |
| `create_and_start_session` | Create a session and start it in one call |
| `attach_session` | Attach to a running session for streaming events |
| `send_message` | Send a message to a running session |
| `get_session_state` | Get current session state and pending elicitations |
| `respond_to_elicitation` | Answer a pending elicitation request |
| `dismiss_elicitation` | Cancel a pending elicitation |
| `get_transcript` | Get the session conversation history |
| `terminate_session` | Gracefully terminate a session |

---

## Next Steps

- [OpenAI SDK Integration](openai-sdk-integration): use the OpenAI SDK interface
- [Recursive Delegation](recursive-delegation): observe delegation trees via MCP
