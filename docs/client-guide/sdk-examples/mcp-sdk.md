---
layout: default
title: "MCP SDK"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 5
---

# MCP SDK Examples

Examples for interacting with Lenny through the Model Context Protocol (MCP) using the TypeScript and Python SDKs. MCP provides bidirectional streaming, delegation tree management, and elicitation (human-in-the-loop) support over Streamable HTTP.

Use MCP when you need:
- Streaming of agent output as it is produced
- Delegation tree monitoring across recursive agent sessions
- Elicitation handling for interactive human-in-the-loop prompts
- MCP-native clients that already speak the protocol

For non-interactive automation (CI/CD, batch jobs, backend services), the [REST API examples](index.html) are simpler.

---

## Protocol Details

| Property | Value |
|---|---|
| Endpoint | `/mcp` |
| Transport | Streamable HTTP |
| Target MCP version | `2025-03-26` |
| Previous supported version | `2024-11-05` |
| Authentication | Bearer token (same OIDC token as REST API) |

**Version negotiation:** During `initialize`, the client sends its `protocolVersion`. Lenny responds with the highest mutually supported version. If the client version is older than `2024-11-05`, the connection is rejected with `MCP_VERSION_UNSUPPORTED`.

---

## MCP Tools Reference

These are the client-facing tools exposed by the Lenny gateway as an MCP server:

| Tool | Description |
|---|---|
| `create_session` | Create a new agent session |
| `create_and_start_session` | Create, upload inline files, and start in one call |
| `upload_files` | Upload workspace files |
| `finalize_workspace` | Seal workspace, run setup |
| `start_session` | Start the agent runtime |
| `attach_session` | Attach to a running session (returns streaming task) |
| `send_message` | Send a message to a session |
| `interrupt_session` | Interrupt current agent work |
| `get_session_status` | Query session state (including `suspended`) |
| `get_task_tree` | Get delegation tree for a session |
| `get_session_logs` | Get session logs (paginated) |
| `get_token_usage` | Get token usage for a session |
| `list_artifacts` | List artifacts for a session |
| `download_artifact` | Download a specific artifact |
| `terminate_session` | End a session |
| `resume_session` | Resume a suspended or paused session |
| `list_sessions` | List active/recent sessions (filterable) |
| `list_runtimes` | List available runtimes (identity-filtered, policy-scoped) |

---

## TypeScript MCP SDK

### Prerequisites

```json
{
  "name": "lenny-mcp-client",
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "start": "ts-node --esm lenny_mcp.ts"
  },
  "dependencies": {
    "@modelcontextprotocol/sdk": "^1.12",
    "typescript": "^5.4",
    "ts-node": "^10.9"
  }
}
```

```bash
npm install
```

### Complete Session Lifecycle

```typescript
// lenny_mcp.ts: Lenny MCP client lifecycle
//
// Uses the MCP TypeScript SDK with Streamable HTTP transport to:
//   1. Connect and negotiate protocol version
//   2. Discover runtimes
//   3. Create and start a session
//   4. Attach to the session for streaming output
//   5. Send messages and handle streaming task updates
//   6. Handle elicitation requests
//   7. Terminate the session

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const LENNY_URL = process.env.LENNY_URL ?? "https://lenny.example.com";
const ACCESS_TOKEN = process.env.ACCESS_TOKEN ?? "your-access-token";

// ---------------------------------------------------------------------------
// Connection Setup
// ---------------------------------------------------------------------------

async function createClient(): Promise<Client> {
  const transport = new StreamableHTTPClientTransport(
    new URL(`${LENNY_URL}/mcp`),
    {
      requestInit: {
        headers: {
          Authorization: `Bearer ${ACCESS_TOKEN}`,
        },
      },
    }
  );

  const client = new Client(
    {
      name: "lenny-typescript-example",
      version: "1.0.0",
    },
    {
      capabilities: {
        // Enable elicitation support so the server can request user input
        elicitation: {},
      },
    }
  );

  // Connect and negotiate protocol version.
  // The SDK sends protocolVersion in the initialize request.
  // Lenny responds with the highest mutually supported version
  // (2025-03-26 or 2024-11-05).
  await client.connect(transport);

  console.log("Connected to Lenny MCP server");
  return client;
}

// ---------------------------------------------------------------------------
// 1. Discover Runtimes
// ---------------------------------------------------------------------------

async function discoverRuntimes(client: Client): Promise<string> {
  console.log("\n--- Discovering Runtimes ---");

  const result = await client.callTool({
    name: "list_runtimes",
    arguments: {},
  });

  // Tool result content is an array of content parts
  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Found ${data.items.length} runtime(s):`);
  for (const rt of data.items) {
    console.log(`  - ${rt.name} (type: ${rt.type})`);
    if (rt.mcpEndpoint) {
      console.log(`    MCP endpoint: ${rt.mcpEndpoint}`);
    }
  }

  // adapterCapabilities tells us what this connection supports
  if (data.adapterCapabilities) {
    const caps = data.adapterCapabilities;
    console.log("\nAdapter capabilities:");
    console.log(`  Elicitation: ${caps.supportsElicitation}`);
    console.log(`  Delegation:  ${caps.supportsDelegation}`);
    console.log(`  Interrupts:  ${caps.supportsInterrupts}`);
  }

  return data.items[0]?.name ?? "claude-worker";
}

// ---------------------------------------------------------------------------
// 2. Create and Start Session (Convenience Tool)
// ---------------------------------------------------------------------------

async function createAndStartSession(
  client: Client,
  runtime: string
): Promise<string> {
  console.log("\n--- Creating and Starting Session ---");

  const result = await client.callTool({
    name: "create_and_start_session",
    arguments: {
      runtime,
      labels: { example: "mcp-sdk" },
      inlineFiles: [
        {
          path: "example.ts",
          content:
            'function greet(name: string): string {\n  return `Hello, ${name}!`;\n}\n\nexport { greet };\n',
        },
        {
          path: "README.md",
          content: "# Example\n\nA simple greeting function.\n",
        },
      ],
      message: {
        parts: [
          {
            type: "text",
            text: "Review the TypeScript code in example.ts. Suggest improvements for type safety and error handling.",
          },
        ],
      },
    },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Session ID:  ${data.sessionId}`);
  console.log(`State:       ${data.state}`);
  console.log(`Isolation:   ${JSON.stringify(data.sessionIsolationLevel)}`);

  return data.sessionId;
}

// ---------------------------------------------------------------------------
// 3. Create Session (Step-by-Step)
// ---------------------------------------------------------------------------

async function createSessionStepByStep(
  client: Client,
  runtime: string
): Promise<string> {
  console.log("\n--- Creating Session (Step-by-Step) ---");

  // Step 1: Create session
  let result = await client.callTool({
    name: "create_session",
    arguments: {
      runtime,
      labels: { example: "mcp-sdk-stepwise" },
      retryPolicy: {
        mode: "auto_then_client",
        maxRetries: 2,
      },
    },
  });

  let data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );
  const sessionId = data.sessionId;
  const uploadToken = data.uploadToken;
  console.log(`Created session: ${sessionId}`);

  // Step 2: Upload files
  result = await client.callTool({
    name: "upload_files",
    arguments: {
      sessionId,
      uploadToken,
      files: [
        {
          path: "main.ts",
          content:
            'import { greet } from "./example.js";\n\nconsole.log(greet("world"));\n',
        },
      ],
    },
  });
  console.log("Files uploaded");

  // Step 3: Finalize workspace
  result = await client.callTool({
    name: "finalize_workspace",
    arguments: {
      sessionId,
      uploadToken,
    },
  });
  data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );
  console.log(`Finalized: state = ${data.state}`);

  // Step 4: Start session
  result = await client.callTool({
    name: "start_session",
    arguments: { sessionId },
  });
  data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );
  console.log(`Started: state = ${data.state}`);

  return sessionId;
}

// ---------------------------------------------------------------------------
// 4. Attach and Stream Output
// ---------------------------------------------------------------------------

async function attachAndStream(
  client: Client,
  sessionId: string
): Promise<void> {
  console.log("\n--- Attaching to Session ---");

  // attach_session returns a streaming MCP Task.
  // The SDK handles the Streamable HTTP SSE connection internally.
  const result = await client.callTool({
    name: "attach_session",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Attached. Task state: ${data.state}`);
  console.log("Streaming output:");
  console.log("-".repeat(50));

  // If the tool returned final content directly (session already complete),
  // print it and return
  if (data.output) {
    for (const part of data.output) {
      if (part.type === "text") {
        process.stdout.write(part.text);
      }
    }
    console.log("\n" + "-".repeat(50));
    return;
  }
}

// ---------------------------------------------------------------------------
// 5. Send Message
// ---------------------------------------------------------------------------

async function sendMessage(
  client: Client,
  sessionId: string,
  text: string
): Promise<void> {
  console.log(`\n--- Sending Message ---`);
  console.log(`Message: "${text}"`);

  const result = await client.callTool({
    name: "send_message",
    arguments: {
      sessionId,
      parts: [
        {
          type: "text",
          text,
        },
      ],
    },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Delivery status: ${data.deliveryReceipt.status}`);
  // deliveryReceipt.status is one of:
  //   "delivered" - runtime consumed the message
  //   "queued"    - buffered in inbox or DLQ
  //   "dropped"   - inbox/DLQ overflow
}

// ---------------------------------------------------------------------------
// 6. Handle Elicitation Requests
// ---------------------------------------------------------------------------

// When the server declares elicitation support and an agent (or connector)
// needs user input, the MCP SDK invokes the elicitation callback.
//
// Elicitation requests include provenance metadata:
//   origin_pod       - which pod initiated the request
//   delegation_depth - how deep in the task tree
//   origin_runtime   - runtime type of the originating pod
//   purpose          - e.g., "oauth_login", "user_confirmation"
//   connector_id     - registered connector ID (for OAuth flows)
//   expected_domain  - expected OAuth endpoint domain
//   initiator_type   - "connector" or "agent"
//
// Client UIs MUST display provenance so users can distinguish platform
// OAuth flows from agent-initiated prompts.

function setupElicitationHandler(client: Client): void {
  client.setRequestHandler(
    // The MCP SDK routes elicitation requests through the request handler
    { method: "elicitation/create" } as any,
    async (request: any) => {
      const { message, requestedSchema } = request.params;

      console.log("\n========================================");
      console.log("ELICITATION REQUEST");
      console.log(`Message: ${message}`);
      console.log(`Schema:  ${JSON.stringify(requestedSchema, null, 2)}`);

      // Display provenance metadata if present
      if (request.params.provenance) {
        const p = request.params.provenance;
        console.log(`Provenance:`);
        console.log(`  Initiator: ${p.initiator_type}`);
        console.log(`  Runtime:   ${p.origin_runtime}`);
        console.log(`  Depth:     ${p.delegation_depth}`);
        console.log(`  Purpose:   ${p.purpose}`);
        if (p.connector_id) {
          console.log(`  Connector: ${p.connector_id}`);
        }
      }
      console.log("========================================\n");

      // In a real application, you would present this to the user via a UI.
      // For this example, we auto-respond based on the schema type.

      if (requestedSchema?.properties) {
        // Build a response matching the schema
        const response: Record<string, any> = {};
        for (const [key, prop] of Object.entries(
          requestedSchema.properties as Record<string, any>
        )) {
          if (prop.type === "boolean") {
            // Auto-approve boolean confirmations
            response[key] = true;
          } else if (prop.type === "string") {
            response[key] = `example-${key}`;
          } else if (prop.type === "number" || prop.type === "integer") {
            response[key] = 42;
          }
        }

        console.log(`Auto-responding: ${JSON.stringify(response)}`);
        return {
          action: "accept",
          content: response,
        };
      }

      // If no schema, accept with empty content
      return {
        action: "accept",
        content: {},
      };

      // Other possible actions:
      // return { action: "decline" };  // User declined the elicitation
      // return { action: "cancel" };   // Dismiss the elicitation entirely
    }
  );
}

// ---------------------------------------------------------------------------
// 7. Monitor Task Tree (Delegation)
// ---------------------------------------------------------------------------

async function monitorTaskTree(
  client: Client,
  sessionId: string
): Promise<void> {
  console.log("\n--- Task Tree ---");

  const result = await client.callTool({
    name: "get_task_tree",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  function printTree(node: any, indent = 0): void {
    const prefix = "  ".repeat(indent);
    console.log(
      `${prefix}- Task ${node.taskId} [${node.state}] (runtime: ${node.runtimeRef})`
    );
    for (const child of node.children ?? []) {
      printTree(child, indent + 1);
    }
  }

  printTree(data);
}

// ---------------------------------------------------------------------------
// 8. Query Session Status and Usage
// ---------------------------------------------------------------------------

async function getSessionStatus(
  client: Client,
  sessionId: string
): Promise<string> {
  const result = await client.callTool({
    name: "get_session_status",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  return data.state;
}

async function getTokenUsage(
  client: Client,
  sessionId: string
): Promise<void> {
  console.log("\n--- Token Usage ---");

  const result = await client.callTool({
    name: "get_token_usage",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Input tokens:  ${data.inputTokens}`);
  console.log(`Output tokens: ${data.outputTokens}`);
  console.log(`Wall clock:    ${data.wallClockSeconds}s`);
  console.log(`Pod minutes:   ${data.podMinutes}`);

  // Tree usage includes all delegated child sessions
  if (data.treeUsage) {
    console.log("\nTree usage (including delegated children):");
    console.log(`  Total tasks:   ${data.treeUsage.totalTasks}`);
    console.log(`  Input tokens:  ${data.treeUsage.inputTokens}`);
    console.log(`  Output tokens: ${data.treeUsage.outputTokens}`);
    console.log(`  Pod minutes:   ${data.treeUsage.podMinutes}`);
  }
}

// ---------------------------------------------------------------------------
// 9. List and Download Artifacts
// ---------------------------------------------------------------------------

async function listArtifacts(
  client: Client,
  sessionId: string
): Promise<void> {
  console.log("\n--- Artifacts ---");

  const result = await client.callTool({
    name: "list_artifacts",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  if (data.items.length === 0) {
    console.log("No artifacts produced.");
    return;
  }

  for (const artifact of data.items) {
    console.log(
      `  - ${artifact.path} (${artifact.size} bytes, ${artifact.mimeType})`
    );
  }

  // Download the first artifact as an example
  if (data.items.length > 0) {
    const first = data.items[0];
    console.log(`\nDownloading: ${first.path}`);

    const downloadResult = await client.callTool({
      name: "download_artifact",
      arguments: {
        sessionId,
        path: first.path,
      },
    });

    const content = downloadResult.content as Array<{
      type: string;
      text?: string;
      data?: string;
    }>;
    if (content[0].type === "text") {
      console.log(
        `Content (first 200 chars): ${content[0].text?.slice(0, 200)}`
      );
    } else {
      console.log(`Binary artifact, ${content[0].data?.length ?? 0} bytes`);
    }
  }
}

// ---------------------------------------------------------------------------
// 10. Interrupt and Resume
// ---------------------------------------------------------------------------

async function interruptSession(
  client: Client,
  sessionId: string
): Promise<void> {
  console.log("\n--- Interrupting Session ---");

  const result = await client.callTool({
    name: "interrupt_session",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Session state after interrupt: ${data.state}`);
  // State transitions to "suspended" after interrupt is acknowledged
}

async function resumeSession(
  client: Client,
  sessionId: string
): Promise<void> {
  console.log("\n--- Resuming Session ---");

  const result = await client.callTool({
    name: "resume_session",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Session state after resume: ${data.state}`);
}

// ---------------------------------------------------------------------------
// 11. Terminate Session
// ---------------------------------------------------------------------------

async function terminateSession(
  client: Client,
  sessionId: string
): Promise<void> {
  console.log("\n--- Terminating Session ---");

  const result = await client.callTool({
    name: "terminate_session",
    arguments: { sessionId },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Final state: ${data.state}`);
}

// ---------------------------------------------------------------------------
// 12. List Sessions
// ---------------------------------------------------------------------------

async function listSessions(client: Client): Promise<void> {
  console.log("\n--- Active Sessions ---");

  const result = await client.callTool({
    name: "list_sessions",
    arguments: {
      state: "running",
      limit: 10,
    },
  });

  const data = JSON.parse(
    (result.content as Array<{ type: string; text: string }>)[0].text
  );

  console.log(`Found ${data.items.length} running session(s):`);
  for (const session of data.items) {
    console.log(
      `  - ${session.sessionId} (runtime: ${session.runtime}, state: ${session.state})`
    );
  }
}

// ---------------------------------------------------------------------------
// Main: Full Lifecycle
// ---------------------------------------------------------------------------

async function main(): Promise<void> {
  console.log("=== Lenny MCP SDK Example (TypeScript) ===\n");

  // 1. Connect to Lenny MCP server
  const client = await createClient();

  // 2. Set up elicitation handler (must be done before session start)
  setupElicitationHandler(client);

  try {
    // 3. Discover runtimes
    const runtime = await discoverRuntimes(client);

    // 4. Create and start a session (using convenience tool)
    const sessionId = await createAndStartSession(client, runtime);

    // 5. Attach to session and stream output
    await attachAndStream(client, sessionId);

    // 6. Send a follow-up message
    await sendMessage(
      client,
      sessionId,
      "Now add comprehensive JSDoc comments to the greet function."
    );

    // 7. Check task tree (shows delegation hierarchy if agents delegated)
    await monitorTaskTree(client, sessionId);

    // 8. Wait for session to reach a terminal state
    console.log("\n--- Waiting for completion ---");
    let state = await getSessionStatus(client, sessionId);
    while (
      !["completed", "failed", "cancelled", "expired"].includes(state)
    ) {
      await new Promise((r) => setTimeout(r, 2000));
      state = await getSessionStatus(client, sessionId);
      console.log(`  State: ${state}`);
    }
    console.log(`Session reached terminal state: ${state}`);

    // 9. Get usage
    await getTokenUsage(client, sessionId);

    // 10. List artifacts
    await listArtifacts(client, sessionId);

    // 11. Terminate if not already terminal
    if (!["completed", "failed", "cancelled", "expired"].includes(state)) {
      await terminateSession(client, sessionId);
    }

    // 12. List all running sessions
    await listSessions(client);
  } finally {
    // Clean up connection
    await client.close();
    console.log("\n=== Done ===");
  }
}

main().catch(console.error);
```

---

### Step-by-Step Walkthrough with Interrupt/Resume

This example demonstrates suspending and resuming a session mid-work:

```typescript
// interrupt_resume.ts: interrupt and resume a session

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const LENNY_URL = process.env.LENNY_URL ?? "https://lenny.example.com";
const ACCESS_TOKEN = process.env.ACCESS_TOKEN ?? "your-access-token";

async function main(): Promise<void> {
  const transport = new StreamableHTTPClientTransport(
    new URL(`${LENNY_URL}/mcp`),
    {
      requestInit: {
        headers: { Authorization: `Bearer ${ACCESS_TOKEN}` },
      },
    }
  );

  const client = new Client(
    { name: "interrupt-example", version: "1.0.0" },
    { capabilities: {} }
  );
  await client.connect(transport);

  try {
    // Create and start a session with a long-running task
    const createResult = await client.callTool({
      name: "create_and_start_session",
      arguments: {
        runtime: "claude-worker",
        inlineFiles: [
          {
            path: "data.json",
            content: JSON.stringify({ items: Array.from({ length: 100 }, (_, i) => ({ id: i })) }),
          },
        ],
        message: {
          parts: [
            {
              type: "text",
              text: "Process each item in data.json and generate a summary report.",
            },
          ],
        },
      },
    });

    const session = JSON.parse(
      (createResult.content as Array<{ type: string; text: string }>)[0].text
    );
    const sessionId = session.sessionId;
    console.log(`Session: ${sessionId}`);

    // Let the session work for 10 seconds, then interrupt
    console.log("Waiting 10 seconds before interrupting...");
    await new Promise((r) => setTimeout(r, 10000));

    // Interrupt the session; transitions to "suspended"
    console.log("Interrupting...");
    await client.callTool({
      name: "interrupt_session",
      arguments: { sessionId },
    });

    // Verify suspended state
    const statusResult = await client.callTool({
      name: "get_session_status",
      arguments: { sessionId },
    });
    const status = JSON.parse(
      (statusResult.content as Array<{ type: string; text: string }>)[0].text
    );
    console.log(`State after interrupt: ${status.state}`);
    // Expected: "suspended"

    // Send a new message and resume
    console.log("Sending new instructions and resuming...");
    await client.callTool({
      name: "send_message",
      arguments: {
        sessionId,
        parts: [
          {
            type: "text",
            text: "Focus only on items with id < 10 and skip the rest.",
          },
        ],
      },
    });

    // Resume the session
    await client.callTool({
      name: "resume_session",
      arguments: { sessionId },
    });

    // Poll until complete
    let state = "running";
    while (!["completed", "failed", "cancelled", "expired"].includes(state)) {
      await new Promise((r) => setTimeout(r, 2000));
      const pollResult = await client.callTool({
        name: "get_session_status",
        arguments: { sessionId },
      });
      state = JSON.parse(
        (pollResult.content as Array<{ type: string; text: string }>)[0].text
      ).state;
      console.log(`State: ${state}`);
    }

    console.log(`Final state: ${state}`);
  } finally {
    await client.close();
  }
}

main().catch(console.error);
```

---

## Python MCP SDK

### Prerequisites

```
# requirements.txt
mcp>=1.9
```

```bash
pip install -r requirements.txt
```

### Complete Session Lifecycle

```python
"""
Lenny MCP client lifecycle using the Python MCP SDK.

pip install mcp

Usage:
    python lenny_mcp.py
"""

import asyncio
import json
import os
from typing import Any

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client
from mcp.types import (
    CallToolResult,
    TextContent,
    CreateMessageRequest,
)


# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

LENNY_URL = os.environ.get("LENNY_URL", "https://lenny.example.com")
ACCESS_TOKEN = os.environ.get("ACCESS_TOKEN", "your-access-token")


# ---------------------------------------------------------------------------
# Helper: Parse Tool Results
# ---------------------------------------------------------------------------

def parse_tool_result(result: CallToolResult) -> dict[str, Any]:
    """Extract JSON data from an MCP tool result."""
    for content in result.content:
        if isinstance(content, TextContent):
            return json.loads(content.text)
    return {}


# ---------------------------------------------------------------------------
# Elicitation Handler
# ---------------------------------------------------------------------------
#
# Elicitation requests include provenance metadata:
#   origin_pod       - which pod initiated the request
#   delegation_depth - how deep in the task tree
#   origin_runtime   - runtime type of the originating pod
#   purpose          - e.g., "oauth_login", "user_confirmation"
#   connector_id     - registered connector ID (for OAuth flows)
#   expected_domain  - expected OAuth endpoint domain
#   initiator_type   - "connector" or "agent"
#
# Client UIs MUST display provenance so users can distinguish platform
# OAuth flows from agent-initiated prompts.

async def handle_elicitation(request: Any) -> dict[str, Any]:
    """
    Handle an elicitation request from the agent or connector.

    In a real application, present the request to the user through a UI.
    This example auto-responds for demonstration purposes.
    """
    message = request.get("message", "")
    schema = request.get("requestedSchema", {})

    print("\n" + "=" * 50)
    print("ELICITATION REQUEST")
    print(f"Message: {message}")
    print(f"Schema:  {json.dumps(schema, indent=2)}")

    # Display provenance metadata if present
    provenance = request.get("provenance", {})
    if provenance:
        print("Provenance:")
        print(f"  Initiator: {provenance.get('initiator_type', 'unknown')}")
        print(f"  Runtime:   {provenance.get('origin_runtime', 'unknown')}")
        print(f"  Depth:     {provenance.get('delegation_depth', 0)}")
        print(f"  Purpose:   {provenance.get('purpose', 'unknown')}")
        if provenance.get("connector_id"):
            print(f"  Connector: {provenance['connector_id']}")
    print("=" * 50 + "\n")

    # Build a response matching the schema
    response: dict[str, Any] = {}
    if "properties" in schema:
        for key, prop in schema["properties"].items():
            prop_type = prop.get("type", "string")
            if prop_type == "boolean":
                response[key] = True
            elif prop_type == "string":
                response[key] = f"example-{key}"
            elif prop_type in ("number", "integer"):
                response[key] = 42

    print(f"Auto-responding: {json.dumps(response)}")
    return {"action": "accept", "content": response}


# ---------------------------------------------------------------------------
# 1. Discover Runtimes
# ---------------------------------------------------------------------------

async def discover_runtimes(session: ClientSession) -> str:
    """Discover available runtimes and return the first one."""
    print("\n--- Discovering Runtimes ---")

    result = await session.call_tool("list_runtimes", arguments={})
    data = parse_tool_result(result)

    print(f"Found {len(data['items'])} runtime(s):")
    for rt in data["items"]:
        print(f"  - {rt['name']} (type: {rt.get('type', 'unknown')})")
        if rt.get("mcpEndpoint"):
            print(f"    MCP endpoint: {rt['mcpEndpoint']}")

    # Display adapter capabilities
    if data.get("adapterCapabilities"):
        caps = data["adapterCapabilities"]
        print("\nAdapter capabilities:")
        print(f"  Elicitation: {caps.get('supportsElicitation', False)}")
        print(f"  Delegation:  {caps.get('supportsDelegation', False)}")
        print(f"  Interrupts:  {caps.get('supportsInterrupts', False)}")

    return data["items"][0]["name"] if data["items"] else "claude-worker"


# ---------------------------------------------------------------------------
# 2. Create and Start Session
# ---------------------------------------------------------------------------

async def create_and_start_session(
    session: ClientSession,
    runtime: str,
) -> str:
    """Create, upload files, and start a session in one call."""
    print("\n--- Creating and Starting Session ---")

    result = await session.call_tool(
        "create_and_start_session",
        arguments={
            "runtime": runtime,
            "labels": {"example": "mcp-python-sdk"},
            "inlineFiles": [
                {
                    "path": "example.py",
                    "content": (
                        "def greet(name: str) -> str:\n"
                        '    """Greet someone by name."""\n'
                        '    return f"Hello, {name}!"\n'
                    ),
                },
                {
                    "path": "README.md",
                    "content": "# Example\n\nA simple greeting function.\n",
                },
            ],
            "message": {
                "parts": [
                    {
                        "type": "text",
                        "text": (
                            "Review the Python code in example.py. "
                            "Suggest improvements for error handling "
                            "and documentation."
                        ),
                    }
                ]
            },
        },
    )

    data = parse_tool_result(result)
    print(f"Session ID:  {data['sessionId']}")
    print(f"State:       {data['state']}")
    print(f"Isolation:   {json.dumps(data.get('sessionIsolationLevel', {}))}")

    return data["sessionId"]


# ---------------------------------------------------------------------------
# 3. Create Session (Step-by-Step)
# ---------------------------------------------------------------------------

async def create_session_stepwise(
    session: ClientSession,
    runtime: str,
) -> str:
    """Create a session using individual steps."""
    print("\n--- Creating Session (Step-by-Step) ---")

    # Step 1: Create
    result = await session.call_tool(
        "create_session",
        arguments={
            "runtime": runtime,
            "labels": {"example": "mcp-python-stepwise"},
            "retryPolicy": {
                "mode": "auto_then_client",
                "maxRetries": 2,
            },
        },
    )
    data = parse_tool_result(result)
    session_id = data["sessionId"]
    upload_token = data["uploadToken"]
    print(f"Created: {session_id}")

    # Step 2: Upload files
    await session.call_tool(
        "upload_files",
        arguments={
            "sessionId": session_id,
            "uploadToken": upload_token,
            "files": [
                {
                    "path": "main.py",
                    "content": (
                        "from example import greet\n\n"
                        'if __name__ == "__main__":\n'
                        '    print(greet("world"))\n'
                    ),
                },
            ],
        },
    )
    print("Files uploaded")

    # Step 3: Finalize
    result = await session.call_tool(
        "finalize_workspace",
        arguments={
            "sessionId": session_id,
            "uploadToken": upload_token,
        },
    )
    data = parse_tool_result(result)
    print(f"Finalized: state = {data['state']}")

    # Step 4: Start
    result = await session.call_tool(
        "start_session",
        arguments={"sessionId": session_id},
    )
    data = parse_tool_result(result)
    print(f"Started: state = {data['state']}")

    return session_id


# ---------------------------------------------------------------------------
# 4. Attach and Stream
# ---------------------------------------------------------------------------

async def attach_and_stream(
    session: ClientSession,
    session_id: str,
) -> None:
    """Attach to a running session and print streaming output."""
    print("\n--- Attaching to Session ---")

    result = await session.call_tool(
        "attach_session",
        arguments={"sessionId": session_id},
    )

    data = parse_tool_result(result)
    print(f"Attached. Task state: {data.get('state', 'unknown')}")
    print("-" * 50)

    # If the session already completed, output is returned directly
    if data.get("output"):
        for part in data["output"]:
            if part["type"] == "text":
                print(part["text"], end="")
        print("\n" + "-" * 50)


# ---------------------------------------------------------------------------
# 5. Send Message
# ---------------------------------------------------------------------------

async def send_message(
    session: ClientSession,
    session_id: str,
    text: str,
) -> None:
    """Send a message to a running or suspended session."""
    print(f'\n--- Sending Message: "{text[:50]}..." ---')

    result = await session.call_tool(
        "send_message",
        arguments={
            "sessionId": session_id,
            "parts": [{"type": "text", "text": text}],
        },
    )

    data = parse_tool_result(result)
    status = data.get("deliveryReceipt", {}).get("status", "unknown")
    print(f"Delivery status: {status}")


# ---------------------------------------------------------------------------
# 6. Monitor Task Tree
# ---------------------------------------------------------------------------

async def monitor_task_tree(
    session: ClientSession,
    session_id: str,
) -> None:
    """Print the delegation task tree."""
    print("\n--- Task Tree ---")

    result = await session.call_tool(
        "get_task_tree",
        arguments={"sessionId": session_id},
    )

    data = parse_tool_result(result)

    def print_tree(node: dict, indent: int = 0) -> None:
        prefix = "  " * indent
        print(
            f"{prefix}- Task {node['taskId']} [{node['state']}] "
            f"(runtime: {node.get('runtimeRef', 'unknown')})"
        )
        for child in node.get("children", []):
            print_tree(child, indent + 1)

    print_tree(data)


# ---------------------------------------------------------------------------
# 7. Query Status and Usage
# ---------------------------------------------------------------------------

async def get_session_status(
    session: ClientSession,
    session_id: str,
) -> str:
    """Get the current session state."""
    result = await session.call_tool(
        "get_session_status",
        arguments={"sessionId": session_id},
    )
    data = parse_tool_result(result)
    return data["state"]


async def get_token_usage(
    session: ClientSession,
    session_id: str,
) -> None:
    """Print token usage for a session."""
    print("\n--- Token Usage ---")

    result = await session.call_tool(
        "get_token_usage",
        arguments={"sessionId": session_id},
    )

    data = parse_tool_result(result)
    print(f"Input tokens:  {data['inputTokens']}")
    print(f"Output tokens: {data['outputTokens']}")
    print(f"Wall clock:    {data['wallClockSeconds']}s")
    print(f"Pod minutes:   {data['podMinutes']}")

    if data.get("treeUsage"):
        tree = data["treeUsage"]
        print("\nTree usage (including delegated children):")
        print(f"  Total tasks:   {tree['totalTasks']}")
        print(f"  Input tokens:  {tree['inputTokens']}")
        print(f"  Output tokens: {tree['outputTokens']}")
        print(f"  Pod minutes:   {tree['podMinutes']}")


# ---------------------------------------------------------------------------
# 8. List and Download Artifacts
# ---------------------------------------------------------------------------

async def list_artifacts(
    session: ClientSession,
    session_id: str,
) -> None:
    """List session artifacts and download the first one."""
    print("\n--- Artifacts ---")

    result = await session.call_tool(
        "list_artifacts",
        arguments={"sessionId": session_id},
    )

    data = parse_tool_result(result)
    items = data.get("items", [])

    if not items:
        print("No artifacts produced.")
        return

    for artifact in items:
        print(
            f"  - {artifact['path']} "
            f"({artifact['size']} bytes, {artifact['mimeType']})"
        )

    # Download the first artifact
    first = items[0]
    print(f"\nDownloading: {first['path']}")

    download_result = await session.call_tool(
        "download_artifact",
        arguments={
            "sessionId": session_id,
            "path": first["path"],
        },
    )

    download_data = parse_tool_result(download_result)
    if download_data:
        content_preview = json.dumps(download_data)[:200]
        print(f"Content (first 200 chars): {content_preview}")


# ---------------------------------------------------------------------------
# 9. Interrupt and Resume
# ---------------------------------------------------------------------------

async def interrupt_session(
    session: ClientSession,
    session_id: str,
) -> None:
    """Interrupt a running session (transitions to suspended)."""
    print("\n--- Interrupting Session ---")

    result = await session.call_tool(
        "interrupt_session",
        arguments={"sessionId": session_id},
    )

    data = parse_tool_result(result)
    print(f"State after interrupt: {data.get('state', 'unknown')}")


async def resume_session(
    session: ClientSession,
    session_id: str,
) -> None:
    """Resume a suspended session."""
    print("\n--- Resuming Session ---")

    result = await session.call_tool(
        "resume_session",
        arguments={"sessionId": session_id},
    )

    data = parse_tool_result(result)
    print(f"State after resume: {data.get('state', 'unknown')}")


# ---------------------------------------------------------------------------
# 10. Terminate Session
# ---------------------------------------------------------------------------

async def terminate_session(
    session: ClientSession,
    session_id: str,
) -> None:
    """Terminate a session."""
    print("\n--- Terminating Session ---")

    result = await session.call_tool(
        "terminate_session",
        arguments={"sessionId": session_id},
    )

    data = parse_tool_result(result)
    print(f"Final state: {data.get('state', 'unknown')}")


# ---------------------------------------------------------------------------
# 11. List Sessions
# ---------------------------------------------------------------------------

async def list_sessions(session: ClientSession) -> None:
    """List active sessions."""
    print("\n--- Active Sessions ---")

    result = await session.call_tool(
        "list_sessions",
        arguments={"state": "running", "limit": 10},
    )

    data = parse_tool_result(result)
    print(f"Found {len(data.get('items', []))} running session(s):")
    for s in data.get("items", []):
        print(
            f"  - {s['sessionId']} "
            f"(runtime: {s['runtime']}, state: {s['state']})"
        )


# ---------------------------------------------------------------------------
# Main: Full Lifecycle
# ---------------------------------------------------------------------------

async def main() -> None:
    print("=== Lenny MCP SDK Example (Python) ===\n")

    # Connect to Lenny's MCP Streamable HTTP endpoint.
    # The SDK handles protocol version negotiation automatically.
    # Lenny supports MCP versions 2025-03-26 and 2024-11-05.
    async with streamablehttp_client(
        url=f"{LENNY_URL}/mcp",
        headers={"Authorization": f"Bearer {ACCESS_TOKEN}"},
    ) as (read_stream, write_stream, _):
        async with ClientSession(
            read_stream,
            write_stream,
        ) as session:
            # Initialize the connection (triggers version negotiation)
            await session.initialize()
            print("Connected to Lenny MCP server")

            # 1. Discover runtimes
            runtime = await discover_runtimes(session)

            # 2. Create and start a session
            session_id = await create_and_start_session(session, runtime)

            # 3. Attach to session for streaming output
            await attach_and_stream(session, session_id)

            # 4. Send a follow-up message
            await send_message(
                session,
                session_id,
                "Now add comprehensive docstrings to all functions.",
            )

            # 5. Monitor task tree (shows delegation if agents delegated)
            await monitor_task_tree(session, session_id)

            # 6. Wait for completion
            print("\n--- Waiting for completion ---")
            state = await get_session_status(session, session_id)
            terminal_states = {
                "completed", "failed", "cancelled", "expired"
            }
            while state not in terminal_states:
                await asyncio.sleep(2)
                state = await get_session_status(session, session_id)
                print(f"  State: {state}")
            print(f"Session reached terminal state: {state}")

            # 7. Get usage
            await get_token_usage(session, session_id)

            # 8. List artifacts
            await list_artifacts(session, session_id)

            # 9. Terminate if not already terminal
            if state not in terminal_states:
                await terminate_session(session, session_id)

            # 10. List running sessions
            await list_sessions(session)

    print("\n=== Done ===")


if __name__ == "__main__":
    asyncio.run(main())
```

---

### Interrupt/Resume Example (Python)

```python
"""
Interrupt and resume a Lenny session using the Python MCP SDK.

pip install mcp
"""

import asyncio
import json
import os

from mcp import ClientSession
from mcp.client.streamable_http import streamablehttp_client
from mcp.types import CallToolResult, TextContent


LENNY_URL = os.environ.get("LENNY_URL", "https://lenny.example.com")
ACCESS_TOKEN = os.environ.get("ACCESS_TOKEN", "your-access-token")


def parse_result(result: CallToolResult) -> dict:
    for content in result.content:
        if isinstance(content, TextContent):
            return json.loads(content.text)
    return {}


async def main() -> None:
    async with streamablehttp_client(
        url=f"{LENNY_URL}/mcp",
        headers={"Authorization": f"Bearer {ACCESS_TOKEN}"},
    ) as (read_stream, write_stream, _):
        async with ClientSession(read_stream, write_stream) as session:
            await session.initialize()
            print("Connected")

            # Create and start a long-running session
            result = await session.call_tool(
                "create_and_start_session",
                arguments={
                    "runtime": "claude-worker",
                    "inlineFiles": [
                        {
                            "path": "data.json",
                            "content": json.dumps(
                                {"items": [{"id": i} for i in range(100)]}
                            ),
                        },
                    ],
                    "message": {
                        "parts": [
                            {
                                "type": "text",
                                "text": (
                                    "Process each item in data.json and "
                                    "generate a summary report."
                                ),
                            }
                        ]
                    },
                },
            )

            data = parse_result(result)
            session_id = data["sessionId"]
            print(f"Session: {session_id}")

            # Let it work for 10 seconds
            print("Working for 10 seconds...")
            await asyncio.sleep(10)

            # Interrupt
            print("Interrupting...")
            result = await session.call_tool(
                "interrupt_session",
                arguments={"sessionId": session_id},
            )
            data = parse_result(result)
            print(f"State: {data['state']}")  # Expected: "suspended"

            # Send new instructions
            await session.call_tool(
                "send_message",
                arguments={
                    "sessionId": session_id,
                    "parts": [
                        {
                            "type": "text",
                            "text": "Only process items with id < 10.",
                        }
                    ],
                },
            )

            # Resume
            print("Resuming...")
            await session.call_tool(
                "resume_session",
                arguments={"sessionId": session_id},
            )

            # Poll until done
            terminal = {"completed", "failed", "cancelled", "expired"}
            state = "running"
            while state not in terminal:
                await asyncio.sleep(2)
                result = await session.call_tool(
                    "get_session_status",
                    arguments={"sessionId": session_id},
                )
                state = parse_result(result)["state"]
                print(f"  State: {state}")

            print(f"Final state: {state}")


if __name__ == "__main__":
    asyncio.run(main())
```

---

## Error Handling

Both SDKs surface Lenny's standard error taxonomy within MCP tool error responses. Errors include the same `code`, `category`, `message`, and `retryable` fields as the REST API.

### TypeScript Error Handling

```typescript
import { Client } from "@modelcontextprotocol/sdk/client/index.js";

async function safeToolCall(
  client: Client,
  tool: string,
  args: Record<string, any>,
  maxRetries = 3
): Promise<any> {
  for (let attempt = 0; attempt <= maxRetries; attempt++) {
    const result = await client.callTool({ name: tool, arguments: args });

    // Check if the tool returned an error
    if (result.isError) {
      const errorData = JSON.parse(
        (result.content as Array<{ type: string; text: string }>)[0].text
      );

      const { code, category, message, retryable } = errorData;

      console.error(`Tool error: [${code}] ${message} (${category})`);

      // Retry transient errors
      if (retryable && attempt < maxRetries) {
        const wait = Math.min(1000 * Math.pow(2, attempt), 30000);
        console.log(`  Retrying in ${wait / 1000}s...`);
        await new Promise((r) => setTimeout(r, wait));
        continue;
      }

      throw new Error(`${code}: ${message}`);
    }

    return JSON.parse(
      (result.content as Array<{ type: string; text: string }>)[0].text
    );
  }

  throw new Error("Max retries exceeded");
}

// Usage:
// const status = await safeToolCall(client, "get_session_status", { sessionId });
// console.log(status.state);
```

### Python Error Handling

```python
import asyncio
import json
from mcp import ClientSession
from mcp.types import CallToolResult, TextContent


async def safe_tool_call(
    session: ClientSession,
    tool: str,
    arguments: dict,
    max_retries: int = 3,
) -> dict:
    """Call an MCP tool with automatic retry for transient errors."""
    for attempt in range(max_retries + 1):
        result = await session.call_tool(tool, arguments=arguments)

        # Check for tool error
        if result.isError:
            error_text = ""
            for content in result.content:
                if isinstance(content, TextContent):
                    error_text = content.text
                    break

            try:
                error_data = json.loads(error_text)
            except json.JSONDecodeError:
                raise RuntimeError(f"Tool error: {error_text}")

            code = error_data.get("code", "UNKNOWN")
            category = error_data.get("category", "UNKNOWN")
            message = error_data.get("message", error_text)
            retryable = error_data.get("retryable", False)

            print(f"Tool error: [{code}] {message} ({category})")

            if retryable and attempt < max_retries:
                wait = min(1.0 * (2 ** attempt), 30.0)
                print(f"  Retrying in {wait:.1f}s...")
                await asyncio.sleep(wait)
                continue

            raise RuntimeError(f"{code}: {message}")

        # Parse successful result
        for content in result.content:
            if isinstance(content, TextContent):
                return json.loads(content.text)

        return {}

    raise RuntimeError("Max retries exceeded")
```

### Common Error Codes

| Code | Category | Retryable | Meaning |
|---|---|---|---|
| `MCP_VERSION_UNSUPPORTED` | `PERMANENT` | No | Client MCP version too old |
| `SESSION_NOT_FOUND` | `PERMANENT` | No | Invalid session ID |
| `INVALID_STATE_TRANSITION` | `PERMANENT` | No | Operation not valid in current state |
| `QUOTA_EXCEEDED` | `POLICY` | No | Tenant quota reached |
| `RATE_LIMITED` | `TRANSIENT` | Yes | Too many requests |
| `POD_SCHEDULING_TIMEOUT` | `TRANSIENT` | Yes | Pod pool temporarily unavailable |
| `UPSTREAM_ERROR` | `UPSTREAM` | Yes | External dependency failure |
| `LEASE_EXPIRED` | `POLICY` | No | Session lease expired |

See [Error Handling](../error-handling.html) for the complete error code catalog and retry strategies.

---

## Choosing Between MCP and REST

| Criterion | MCP SDK | REST API |
|---|---|---|
| Streaming output | Native via Streamable HTTP | SSE via `/v1/sessions/{id}/logs` |
| Elicitation (human-in-the-loop) | Built-in callback mechanism | Poll + respond via REST endpoints |
| Delegation tree monitoring | Real-time via task updates | Poll `GET /v1/sessions/{id}/tree` |
| Language support | TypeScript, Python | Any HTTP client |
| Complexity | Higher (protocol negotiation, callbacks) | Lower (standard HTTP) |
| Best for | Interactive clients, MCP-native agents | CI/CD, batch jobs, simple scripts |

Both surfaces share the same authentication, error taxonomy, and session semantics. See the [REST/MCP Consistency Contract](../session-lifecycle.html) for details.
