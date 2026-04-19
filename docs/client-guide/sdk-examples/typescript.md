---
layout: default
title: "TypeScript"
parent: "Client SDK Examples"
grand_parent: "Client Guide"
nav_order: 2
---

# TypeScript Client Examples

TypeScript/Node.js examples for interacting with the Lenny REST API using the built-in `fetch` API.

## Prerequisites

```json
{
  "name": "lenny-typescript-client",
  "version": "1.0.0",
  "type": "module",
  "scripts": {
    "start": "ts-node --esm lenny_client.ts"
  },
  "dependencies": {
    "typescript": "^5.4",
    "ts-node": "^10.9"
  }
}
```

```bash
npm install
```

---

## Type Definitions

```typescript
// types.ts: type definitions for Lenny API responses

export interface Session {
  sessionId: string;
  state: SessionState;
  runtime: string;
  pool?: string;
  createdAt: string;
  startedAt?: string;
  labels?: Record<string, string>;
  uploadToken?: string;
  sessionIsolationLevel?: SessionIsolationLevel;
  retryPolicy?: RetryPolicy;
}

export type SessionState =
  | "created"
  | "finalizing"
  | "ready"
  | "starting"
  | "running"
  | "suspended"
  | "resume_pending"
  | "awaiting_client_action"
  | "completed"
  | "failed"
  | "cancelled"
  | "expired";

export interface SessionIsolationLevel {
  executionMode: "session" | "task" | "concurrent";
  isolationProfile: "runc" | "gvisor" | "microvm";
  podReuse: boolean;
  scrubPolicy?: string;
  residualStateWarning?: boolean;
}

export interface RetryPolicy {
  mode: "auto_then_client" | "auto" | "manual";
  maxRetries: number;
  retryableFailures?: string[];
  maxSessionAgeSeconds?: number;
  maxResumeWindowSeconds?: number;
}

export interface CreateSessionResponse {
  sessionId: string;
  uploadToken: string;
  sessionIsolationLevel: SessionIsolationLevel;
  state: SessionState;
  createdAt: string;
}

export interface UploadResponse {
  uploaded: Array<{ path: string; size: number }>;
}

export interface MessageResponse {
  messageId: string;
  deliveryReceipt: {
    status: "delivered" | "queued" | "dropped";
    timestamp: string;
  };
}

export interface Usage {
  inputTokens: number;
  outputTokens: number;
  wallClockSeconds: number;
  podMinutes: number;
  credentialLeaseMinutes: number;
  treeUsage?: TreeUsage;
}

export interface TreeUsage {
  inputTokens: number;
  outputTokens: number;
  wallClockSeconds: number;
  podMinutes: number;
  credentialLeaseMinutes: number;
  totalTasks: number;
}

export interface Artifact {
  path: string;
  size: number;
  mimeType: string;
}

export interface PaginatedResponse<T> {
  items: T[];
  cursor: string | null;
  hasMore: boolean;
  total?: number;
}

export interface LennyError {
  code: string;
  category: "TRANSIENT" | "PERMANENT" | "POLICY" | "UPSTREAM";
  message: string;
  retryable: boolean;
  details: Record<string, any>;
}

export interface Runtime {
  name: string;
  type: string;
  capabilities?: Record<string, boolean>;
  labels?: Record<string, string>;
}

export interface TaskTreeNode {
  taskId: string;
  sessionId: string;
  state: string;
  runtimeRef: string;
  children: TaskTreeNode[];
}
```

---

## Full Session Lifecycle

```typescript
// lenny_client.ts: Lenny session lifecycle

import type {
  CreateSessionResponse,
  LennyError,
  PaginatedResponse,
  Runtime,
  Session,
  Artifact,
  Usage,
} from "./types.js";

// Configuration
const LENNY_URL = process.env.LENNY_URL ?? "https://lenny.example.com";
const OIDC_TOKEN_URL =
  process.env.OIDC_TOKEN_URL ?? "https://auth.example.com/oauth/token";
const OIDC_CLIENT_ID = process.env.OIDC_CLIENT_ID ?? "your-client-id";
const OIDC_CLIENT_SECRET =
  process.env.OIDC_CLIENT_SECRET ?? "your-client-secret";

// ---------------------------------------------------------------------------
// Authentication
// ---------------------------------------------------------------------------

async function getAccessToken(): Promise<string> {
  const response = await fetch(OIDC_TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "client_credentials",
      client_id: OIDC_CLIENT_ID,
      client_secret: OIDC_CLIENT_SECRET,
      scope: "openid profile",
    }),
  });

  if (!response.ok) {
    throw new Error(`Auth failed: ${response.status} ${response.statusText}`);
  }

  const data = await response.json();
  return data.access_token;
}

// Rotate the current Lenny access token via RFC 8693 token exchange.
// Call shortly before `exp` to avoid a gap in authorization. For delegation
// child-token minting, pass the parent session token via `actor_token` and a
// narrowed `scope` string.
async function rotateLennyToken(currentToken: string): Promise<string> {
  const response = await fetch(`${LENNY_URL}/v1/oauth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "urn:ietf:params:oauth:grant-type:token-exchange",
      subject_token: currentToken,
      subject_token_type: "urn:ietf:params:oauth:token-type:access_token",
      requested_token_type: "urn:ietf:params:oauth:token-type:access_token",
    }),
  });

  if (!response.ok) {
    throw new Error(`Token rotation failed: ${response.status}`);
  }

  const data = await response.json();
  return data.access_token;
}

// ---------------------------------------------------------------------------
// API Client with Retry
// ---------------------------------------------------------------------------

class LennyClient {
  private token: string;

  constructor(token: string) {
    this.token = token;
  }

  async request<T = any>(
    method: string,
    path: string,
    options: {
      body?: any;
      headers?: Record<string, string>;
      maxRetries?: number;
    } = {}
  ): Promise<T> {
    const maxRetries = options.maxRetries ?? 5;
    const baseDelay = 1000;
    const maxDelay = 60000;

    for (let attempt = 0; attempt <= maxRetries; attempt++) {
      const headers: Record<string, string> = {
        Authorization: `Bearer ${this.token}`,
        ...options.headers,
      };

      if (options.body && !options.headers?.["Content-Type"]) {
        headers["Content-Type"] = "application/json";
      }

      const fetchOptions: RequestInit = {
        method,
        headers,
      };

      if (options.body) {
        fetchOptions.body =
          typeof options.body === "string"
            ? options.body
            : JSON.stringify(options.body);
      }

      const response = await fetch(`${LENNY_URL}${path}`, fetchOptions);

      if (response.ok) {
        const text = await response.text();
        return text ? JSON.parse(text) : ({} as T);
      }

      let error: LennyError;
      try {
        const body = await response.json();
        error = body.error;
      } catch {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      if (!error.retryable || attempt === maxRetries) {
        throw error;
      }

      const retryAfter = response.headers.get("Retry-After");
      const wait = retryAfter
        ? parseFloat(retryAfter) * 1000
        : Math.min(
            baseDelay * Math.pow(2, attempt) + Math.random() * 1000,
            maxDelay
          );

      console.log(
        `  Retrying in ${(wait / 1000).toFixed(1)}s ` +
          `(${error.code}, attempt ${attempt + 1}/${maxRetries})`
      );
      await new Promise((r) => setTimeout(r, wait));
    }

    throw new Error("Unreachable");
  }

  // -------------------------------------------------------------------------
  // File Upload (multipart/form-data)
  // -------------------------------------------------------------------------

  async uploadFiles(
    sessionId: string,
    uploadToken: string,
    files: Array<{ name: string; content: string | Buffer }>
  ): Promise<any> {
    const formData = new FormData();
    for (const file of files) {
      const blob = new Blob([file.content], {
        type: "application/octet-stream",
      });
      formData.append("files", blob, file.name);
    }

    const response = await fetch(
      `${LENNY_URL}/v1/sessions/${sessionId}/upload`,
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${this.token}`,
          "X-Upload-Token": uploadToken,
        },
        body: formData,
      }
    );

    if (!response.ok) {
      const body = await response.json();
      throw body.error;
    }

    return response.json();
  }

  // -------------------------------------------------------------------------
  // SSE Streaming
  // -------------------------------------------------------------------------

  async streamSession(sessionId: string): Promise<void> {
    let lastCursor: string | null = null;

    while (true) {
      const url = new URL(`${LENNY_URL}/v1/sessions/${sessionId}/logs`);
      if (lastCursor) url.searchParams.set("cursor", lastCursor);

      let response: Response;
      try {
        response = await fetch(url.toString(), {
          headers: {
            Authorization: `Bearer ${this.token}`,
            Accept: "text/event-stream",
          },
        });
      } catch {
        console.log("\n[Connection lost, reconnecting...]");
        await new Promise((r) => setTimeout(r, 1000));
        continue;
      }

      if (!response.ok || !response.body) break;

      const reader = response.body.getReader();
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
          buffer = lines.pop()!;

          for (const line of lines) {
            if (line.startsWith("event: ")) {
              eventType = line.slice(7);
            } else if (line.startsWith("data: ")) {
              dataLines.push(line.slice(6));
            } else if (line.startsWith("id: ")) {
              lastCursor = line.slice(4);
            } else if (line === "") {
              if (eventType && dataLines.length > 0) {
                const data = JSON.parse(dataLines.join("\n"));

                switch (eventType) {
                  case "agent_output":
                    for (const part of data.parts ?? []) {
                      if (part.type === "text") {
                        process.stdout.write(part.inline);
                      }
                    }
                    break;
                  case "status_change":
                    console.log(`\n[Status: ${data.state}]`);
                    break;
                  case "tool_use_requested":
                    console.log(`\n[Tool: ${data.tool}]`);
                    break;
                  case "error":
                    console.log(`\n[Error: ${data.code} - ${data.message}]`);
                    break;
                  case "session_complete":
                    console.log("\n[Session complete]");
                    return;
                  case "checkpoint_boundary":
                    if (data.events_lost > 0) {
                      console.log(
                        `\n[WARNING: ${data.events_lost} events lost]`
                      );
                    }
                    break;
                }
              }
              eventType = null;
              dataLines = [];
            }
          }
        }
      } catch {
        console.log("\n[Stream interrupted, reconnecting...]");
        continue;
      }
    }
  }

  // -------------------------------------------------------------------------
  // Pagination Helper
  // -------------------------------------------------------------------------

  async *paginate<T>(
    path: string,
    limit = 50,
    params: Record<string, string> = {}
  ): AsyncGenerator<T> {
    let cursor: string | null = null;

    while (true) {
      const query = new URLSearchParams({ ...params, limit: String(limit) });
      if (cursor) query.set("cursor", cursor);

      const data = await this.request<PaginatedResponse<T>>(
        "GET",
        `${path}?${query}`
      );

      for (const item of data.items) {
        yield item;
      }

      cursor = data.cursor;
      if (!data.hasMore || !cursor) break;
    }
  }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  console.log("=== Lenny TypeScript Client Example ===\n");

  // 1. Authenticate
  console.log("1. Authenticating...");
  const token = await getAccessToken();
  console.log(`   Token: ${token.slice(0, 20)}...`);

  const client = new LennyClient(token);

  // 2. Discover runtimes
  console.log("\n2. Discovering runtimes...");
  const runtimes = await client.request<PaginatedResponse<Runtime>>(
    "GET",
    "/v1/runtimes"
  );
  for (const rt of runtimes.items) {
    console.log(`   - ${rt.name} (${rt.type})`);
  }

  const runtimeName = runtimes.items[0]?.name ?? "claude-worker";

  // 3. Create session
  console.log(`\n3. Creating session with '${runtimeName}'...`);
  const session = await client.request<CreateSessionResponse>(
    "POST",
    "/v1/sessions",
    {
      body: {
        runtime: runtimeName,
        labels: { example: "typescript-client" },
      },
    }
  );
  const { sessionId, uploadToken } = session;
  console.log(`   Session: ${sessionId}`);

  // 4. Upload files
  console.log("\n4. Uploading files...");
  const uploaded = await client.uploadFiles(sessionId, uploadToken, [
    {
      name: "example.ts",
      content: 'function greet(name: string): string {\n  return `Hello, ${name}!`;\n}\n',
    },
    {
      name: "README.md",
      content: "# Example\n\nA simple greeting function.\n",
    },
  ]);
  console.log(`   Uploaded: ${uploaded.uploaded.map((f: any) => f.path)}`);

  // 5. Finalize
  console.log("\n5. Finalizing workspace...");
  const finalized = await client.request("POST", `/v1/sessions/${sessionId}/finalize`, {
    headers: { "X-Upload-Token": uploadToken },
  });
  console.log(`   State: ${finalized.state}`);

  // 6. Start
  console.log("\n6. Starting session...");
  const started = await client.request("POST", `/v1/sessions/${sessionId}/start`);
  console.log(`   State: ${started.state}`);

  // 7. Send message
  console.log("\n7. Sending message...");
  const msg = await client.request("POST", `/v1/sessions/${sessionId}/messages`, {
    body: {
      input: [
        {
          type: "text",
          inline: "Review the TypeScript code in example.ts. Suggest type safety improvements.",
        },
      ],
    },
  });
  console.log(`   Delivery: ${msg.deliveryReceipt.status}`);

  // 8. Stream output
  console.log("\n8. Streaming output:");
  console.log("-".repeat(40));
  await client.streamSession(sessionId);
  console.log("-".repeat(40));

  // 9. Retrieve artifacts
  console.log("\n9. Artifacts:");
  for await (const artifact of client.paginate<Artifact>(
    `/v1/sessions/${sessionId}/artifacts`
  )) {
    console.log(`   - ${artifact.path} (${artifact.size} bytes)`);
  }

  // 10. Usage
  console.log("\n10. Usage:");
  const usage = await client.request<Usage>(
    "GET",
    `/v1/sessions/${sessionId}/usage`
  );
  console.log(`    Input tokens:  ${usage.inputTokens}`);
  console.log(`    Output tokens: ${usage.outputTokens}`);
  console.log(`    Wall clock:    ${usage.wallClockSeconds}s`);

  // 11. Check final state
  console.log("\n11. Final state:");
  const final = await client.request<Session>(
    "GET",
    `/v1/sessions/${sessionId}`
  );
  console.log(`    State: ${final.state}`);

  if (
    !["completed", "failed", "cancelled", "expired"].includes(final.state)
  ) {
    console.log("    Terminating...");
    await client.request("POST", `/v1/sessions/${sessionId}/terminate`);
  }

  console.log("\n=== Done ===");
}

main().catch(console.error);
```
