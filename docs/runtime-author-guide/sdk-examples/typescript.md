---
layout: default
title: "TypeScript Runtime SDK"
parent: "Runtime SDK Examples"
grand_parent: "Runtime Author Guide"
nav_order: 3
---

# TypeScript Runtime SDK

This page presents a complete file summarizer runtime in TypeScript. It implements the Basic integration level, reads workspace files via adapter-local tools, and produces summaries. The full source is ~200 lines including comments.

---

## Complete Source Code

```typescript
/**
 * file-summarizer: A Lenny runtime that reads workspace files and produces summaries.
 *
 * Integration level: Basic
 *   - Reads JSON Lines from stdin
 *   - Handles "message" by reading workspace files and summarizing them
 *   - Uses adapter-local tools (read_file, list_dir) via tool_call/tool_result
 *   - Handles "heartbeat" by responding with "heartbeat_ack"
 *   - Handles "shutdown" by exiting cleanly
 *   - Ignores unknown message types for forward compatibility
 *
 * Build:  npm run build
 * Run:    make run LENNY_AGENT_BINARY="node dist/main.js"
 */

import * as readline from "readline";

// ---- Types ----

interface OutputPart {
  type: string;
  inline?: string;
}

interface InboundMessage {
  type: string;
  id?: string;
  input?: OutputPart[];
  ts?: number;
  reason?: string;
  deadline_ms?: number;
  // tool_result fields
  content?: OutputPart[];
  isError?: boolean;
}

interface ToolCall {
  type: "tool_call";
  id: string;
  name: string;
  arguments: Record<string, string>;
}

interface Response {
  type: "response";
  output: OutputPart[];
}

// ---- State ----

let toolCallCounter = 0;
let pendingToolCallId = "";
let phase = 0; // 0=idle, 1=listing, 2=reading, 3=summarizing
let fileList: string[] = [];
let fileContents: string[] = [];
let currentFileIndex = 0;

// ---- Output helpers ----

/**
 * Write a JSON object as a single line to stdout.
 *
 * Node.js stdout is line-buffered when connected to a pipe. Using
 * process.stdout.write() with a synchronous write ensures the adapter
 * receives the message before we block on the next stdin read.
 */
function writeJSON(obj: unknown): void {
  const line = JSON.stringify(obj);
  process.stdout.write(line + "\n");
}

/**
 * Send a response message signaling task completion.
 */
function writeResponse(text: string): void {
  const resp: Response = {
    type: "response",
    output: [{ type: "text", inline: text }],
  };
  writeJSON(resp);
  phase = 0;
}

/**
 * Generate a unique tool call ID.
 */
function nextToolCallId(): string {
  toolCallCounter++;
  return `tc_${String(toolCallCounter).padStart(3, "0")}`;
}

/**
 * Truncate a string to n characters, adding "..." if truncated.
 */
function truncate(s: string, n = 500): string {
  if (s.length <= n) return s;
  return s.slice(0, n) + "...";
}

// ---- Tool call helpers ----

/**
 * Send a list_dir tool call.
 */
function listDir(path: string): void {
  const id = nextToolCallId();
  pendingToolCallId = id;
  const call: ToolCall = {
    type: "tool_call",
    id,
    name: "list_dir",
    arguments: { path },
  };
  writeJSON(call);
}

/**
 * Send a read_file tool call for the next file in the list.
 */
function readNextFile(): void {
  if (currentFileIndex >= fileList.length) return;
  const id = nextToolCallId();
  pendingToolCallId = id;
  const filePath = `/workspace/current/${fileList[currentFileIndex]}`;
  const call: ToolCall = {
    type: "tool_call",
    id,
    name: "read_file",
    arguments: { path: filePath },
  };
  writeJSON(call);
}

// ---- Message handlers ----

/**
 * Process a new task message.
 */
function handleMessage(msg: InboundMessage): void {
  // Reset state for this task.
  fileList = [];
  fileContents = [];
  currentFileIndex = 0;
  phase = 1;

  // Extract the user's request text.
  let requestText = "";
  if (msg.input && msg.input.length > 0) {
    requestText = msg.input[0].inline || "";
  }
  process.stderr.write(`file-summarizer: received request: ${requestText}\n`);

  // Step 1: List files in the workspace.
  listDir("/workspace/current");
}

/**
 * Process the result of a tool call.
 */
function handleToolResult(msg: InboundMessage): void {
  // Verify this result matches our pending tool call.
  if (msg.id !== pendingToolCallId) {
    process.stderr.write(
      `file-summarizer: unexpected tool_result id=${msg.id}\n`
    );
    return;
  }

  // Check for errors.
  if (msg.isError) {
    let errorText = "unknown error";
    if (msg.content && msg.content.length > 0) {
      errorText = msg.content[0].inline || errorText;
    }
    process.stderr.write(`file-summarizer: tool error: ${errorText}\n`);
    writeResponse(`Error reading workspace: ${errorText}`);
    return;
  }

  if (phase === 1) {
    // Phase 1: We received the directory listing.
    if (msg.content && msg.content.length > 0) {
      const listing = msg.content[0].inline || "";
      for (const line of listing.split("\n")) {
        const trimmed = line.trim();
        if (trimmed && !trimmed.startsWith(".")) {
          fileList.push(trimmed);
        }
      }
    }

    if (fileList.length === 0) {
      writeResponse("No files found in the workspace.");
      return;
    }

    // Step 2: Start reading files one by one.
    phase = 2;
    currentFileIndex = 0;
    readNextFile();
  } else if (phase === 2) {
    // Phase 2: We received a file's contents.
    if (msg.content && msg.content.length > 0) {
      const fileName = fileList[currentFileIndex];
      const content = msg.content[0].inline || "";
      fileContents.push(`=== ${fileName} ===\n${truncate(content)}`);
    }

    currentFileIndex++;
    if (currentFileIndex < fileList.length && currentFileIndex < 10) {
      // Read the next file (cap at 10 files).
      readNextFile();
    } else {
      // All files read. Produce the summary.
      phase = 3;
      produceSummary();
    }
  }
}

/**
 * Generate the final summary response.
 */
function produceSummary(): void {
  const lines = [`Workspace Summary (${fileContents.length} files)\n`];
  for (const fc of fileContents) {
    lines.push(fc);
    lines.push("");
  }
  lines.push(`Total files examined: ${fileContents.length}`);
  writeResponse(lines.join("\n"));
}

// ---- Main loop ----

/**
 * Read JSON Lines from stdin and dispatch by message type.
 */
function main(): void {
  const rl = readline.createInterface({
    input: process.stdin,
    terminal: false,
  });

  rl.on("line", (line: string) => {
    if (!line.trim()) return;

    let msg: InboundMessage;
    try {
      msg = JSON.parse(line);
    } catch (e) {
      process.stderr.write(`file-summarizer: parse error: ${e}\n`);
      return;
    }

    switch (msg.type) {
      case "message":
        handleMessage(msg);
        break;

      case "tool_result":
        handleToolResult(msg);
        break;

      case "heartbeat":
        // Respond immediately. Failure to ack within 10 seconds causes SIGTERM.
        writeJSON({ type: "heartbeat_ack" });
        break;

      case "shutdown":
        process.stderr.write(
          `file-summarizer: shutdown (reason=${msg.reason || "unknown"})\n`
        );
        process.exit(0);
        break;

      default:
        // Ignore unknown message types for forward compatibility.
        process.stderr.write(
          `file-summarizer: ignoring unknown type: ${msg.type}\n`
        );
        break;
    }
  });

  // stdin closed (adapter terminated the pipe). Exit cleanly.
  rl.on("close", () => {
    process.exit(0);
  });
}

main();
```

---

## Stdout Flushing in Node.js

Node.js stdout is line-buffered when connected to a pipe. `process.stdout.write(line + "\n")` writes synchronously, which is sufficient for the adapter protocol. However, be aware of these edge cases:

- **Async writes:** If you use streams or async I/O, ensure each message is fully written before reading the next stdin line.
- **Buffered writes returning false:** If `process.stdout.write()` returns `false`, the write buffer is full. In practice this is rare with JSON Lines (each message is typically < 1KB), but handle it if your output is large.
- **High-water mark:** The default high-water mark for stdout is 16KB. Messages larger than this may be split across multiple kernel writes.

---

## package.json

```json
{
  "name": "file-summarizer-ts",
  "version": "1.0.0",
  "description": "A Lenny runtime that summarizes workspace files",
  "main": "dist/main.js",
  "scripts": {
    "build": "tsc",
    "start": "node dist/main.js"
  },
  "devDependencies": {
    "typescript": "^5.4.0",
    "@types/node": "^20.0.0"
  }
}
```

No runtime dependencies for the Basic level.

---

## tsconfig.json

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "commonjs",
    "lib": ["ES2022"],
    "outDir": "./dist",
    "rootDir": "./src",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "resolveJsonModule": true,
    "declaration": true,
    "declarationMap": true,
    "sourceMap": true
  },
  "include": ["src/**/*"],
  "exclude": ["node_modules", "dist"]
}
```

---

## Dockerfile

```dockerfile
FROM node:20-alpine AS builder
WORKDIR /app
COPY package.json package-lock.json tsconfig.json ./
RUN npm ci
COPY src/ ./src/
RUN npm run build

FROM node:20-alpine
WORKDIR /app
COPY --from=builder /app/dist ./dist
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/package.json .
ENTRYPOINT ["node", "dist/main.js"]
```

---

## Build and Run

```bash
# Build
cd examples/runtimes/file-summarizer-ts
npm install
npm run build

# Run locally with `make run` (in-process, no Docker)
make run LENNY_AGENT_BINARY="node examples/runtimes/file-summarizer-ts/dist/main.js"

# Run locally with `docker compose up`
docker build -t file-summarizer-ts:dev \
  -f examples/runtimes/file-summarizer-ts/Dockerfile .
docker compose up
```

---

## Register the Runtime

```bash
# Register via admin API
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "file-summarizer-ts",
    "type": "agent",
    "image": "file-summarizer-ts:dev",
    "description": "TypeScript file summarizer"
  }'

# Create a session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtimeName": "file-summarizer-ts", "tenantId": "default"}'
```

---

## Upgrading to Standard level

### 1. Add MCP Client Dependency

```json
{
  "dependencies": {
    "@modelcontextprotocol/sdk": "^1.0.0"
  }
}
```

### 2. Read the Adapter Manifest

```typescript
import * as fs from "fs";

interface AdapterManifest {
  sessionId: string;
  taskId: string;
  platformMcpServer: {
    socket: string;
  };
  connectorServers: Array<{
    id: string;
    socket: string;
  }>;
  mcpNonce: string;
}

function readManifest(): AdapterManifest {
  const data = fs.readFileSync("/run/lenny/adapter-manifest.json", "utf-8");
  return JSON.parse(data);
}
```

### 3. Connect to Lenny's local tool server

```typescript
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { UnixSocketTransport } from "@modelcontextprotocol/sdk/transport/unix.js";

async function connectMCP(manifest: AdapterManifest): Promise<Client> {
  const transport = new UnixSocketTransport(
    manifest.platformMcpServer.socket
  );
  const client = new Client(
    { name: "file-summarizer-ts", version: "1.0.0" },
    { capabilities: {} }
  );

  await client.connect(transport);

  // The nonce is presented during the initialize handshake
  // (handled by the transport layer in the SDK).

  return client;
}
```

### 4. Use Platform Tools

```typescript
async function emitOutput(client: Client, text: string): Promise<void> {
  await client.callTool("lenny/output", {
    output: [{ type: "text", inline: text }],
  });
}

async function delegateReview(
  client: Client,
  code: string
): Promise<string> {
  const result = await client.callTool("lenny/delegate_task", {
    target: "code-reviewer",
    task: {
      input: [{ type: "text", inline: `Review this code:\n${code}` }],
    },
  });
  return (result as any).sessionId;
}
```

### 5. Async Main Loop

The Standard level with MCP requires an async event loop. The readline interface already works with Node.js event loop:

```typescript
async function asyncMain(): Promise<void> {
  const manifest = readManifest();
  const mcpClient = await connectMCP(manifest);

  // Discover available tools
  const tools = await mcpClient.listTools();
  process.stderr.write(
    `Available tools: ${tools.tools.map((t) => t.name).join(", ")}\n`
  );

  // The readline-based main loop already works with async/await.
  // Handle MCP tool calls inside your message handler using await.
}
```

### 6. macOS Note

The Standard level requires abstract Unix sockets, which are Linux-only. Use `docker compose up` on macOS.
