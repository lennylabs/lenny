---
layout: default
title: "Agent Memory"
parent: Tutorials
nav_order: 13
---

# Agent Memory

**Persona:** Runtime Author + Client Developer | **Difficulty:** Intermediate

Lenny provides a persistent memory store that agents can write to and query across sessions. Memories are scoped by tenant, user, agent, and session, so agents can reuse context from previous interactions. The default implementation uses Postgres with pgvector for semantic search.

In this tutorial you will write memories from one session, then query them from a later session for the same user.

## Prerequisites

- Lenny running locally via `docker compose up`
- A runtime that supports the platform MCP tools (`lenny/memory_write`, `lenny/memory_query`)
- Familiarity with [Your First Session](first-session)
- curl and jq installed

---

## Step 1: Write Memories from a Session

Memories are written by the agent runtime via the `lenny/memory_write` platform MCP tool. The tool is available to any agent pod through the Platform MCP Server.

Start a session and send a message that causes the agent to write memories:

```bash
SESSION_ID=$(curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker"}' | jq -r '.session_id')

# Finalize and start (abbreviated; see first-session tutorial)
UPLOAD_TOKEN=$(curl -s "http://localhost:8080/v1/sessions/${SESSION_ID}" | jq -r '.uploadToken')
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/finalize" \
  -H "X-Upload-Token: ${UPLOAD_TOKEN}" \
  -H "Content-Type: application/json" -d '{}'
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/start" \
  -H "Content-Type: application/json" -d '{}'

# Send a message; the agent writes memories
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{
    "input": [{"type": "text", "inline": "I prefer tabs over spaces, and my main language is Go. Store this for future sessions."}]
  }' | jq .
```

Behind the scenes, the runtime calls `lenny/memory_write`:

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/memory_write",
    "arguments": {
      "memories": [
        {
          "content": "User prefers tabs over spaces for indentation.",
          "tags": ["preferences", "formatting"]
        },
        {
          "content": "User's main programming language is Go.",
          "tags": ["preferences", "language"]
        }
      ]
    }
  }
}
```

Each memory is stored with the current tenant, user, agent, and session context. The gateway indexes the content for semantic search using pgvector embeddings.

Terminate the session:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/terminate" \
  -H "Content-Type: application/json" -d '{}'
```

---

## Step 2: Query Memories from a New Session

Start a new session for the same user. The agent can query memories from previous sessions:

```bash
NEW_SESSION_ID=$(curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker"}' | jq -r '.session_id')

# Finalize and start
NEW_TOKEN=$(curl -s "http://localhost:8080/v1/sessions/${NEW_SESSION_ID}" | jq -r '.uploadToken')
curl -s -X POST "http://localhost:8080/v1/sessions/${NEW_SESSION_ID}/finalize" \
  -H "X-Upload-Token: ${NEW_TOKEN}" \
  -H "Content-Type: application/json" -d '{}'
curl -s -X POST "http://localhost:8080/v1/sessions/${NEW_SESSION_ID}/start" \
  -H "Content-Type: application/json" -d '{}'
```

When the agent needs context about the user, it calls `lenny/memory_query`:

```json
{
  "method": "tools/call",
  "params": {
    "name": "lenny/memory_query",
    "arguments": {
      "query": "user preferences",
      "limit": 10,
      "tags": ["preferences"]
    }
  }
}
```

The gateway returns matching memories:

```json
{
  "memories": [
    {
      "memoryId": "mem_01...",
      "content": "User prefers tabs over spaces for indentation.",
      "tags": ["preferences", "formatting"],
      "sessionId": "sess_01...",
      "createdAt": "2026-04-12T10:05:00Z",
      "relevanceScore": 0.94
    },
    {
      "memoryId": "mem_02...",
      "content": "User's main programming language is Go.",
      "tags": ["preferences", "language"],
      "sessionId": "sess_01...",
      "createdAt": "2026-04-12T10:05:01Z",
      "relevanceScore": 0.91
    }
  ]
}
```

The agent uses these memories to personalize its responses; for example, it uses tabs and Go idioms without being told again.

---

## Step 3: Verify Persistence Across Sessions

Send a coding task to the new session:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${NEW_SESSION_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{
    "input": [{"type": "text", "inline": "Write a simple HTTP server."}]
  }' | jq .
```

The agent, having queried memories, writes the server in Go with tab indentation without being asked. This demonstrates cross-session memory persistence.

---

## Memory Scoping

Memories are scoped hierarchically:

| Scope | Description |
|:------|:------------|
| **Tenant** | Memories are isolated per tenant (enforced by Postgres RLS). |
| **User** | Each user has their own memory space. |
| **Agent** | Optionally scoped to a specific agent/runtime. |
| **Session** | Each memory records which session created it. |

The `lenny/memory_query` tool searches within the authenticated user's memory space by default. Agents cannot read other users' memories.

---

## Configuration

| Field | Default | Description |
|:------|:--------|:------------|
| `memory.maxMemoriesPerUser` | 10000 | Maximum memories per user. Oldest evicted on overflow. |
| `memory.retentionDays` | -- | Auto-delete memories older than this. Unset = indefinite. |

See [Configuration Reference](../reference/configuration) for details.

---

## Next Steps

- [Build a Runtime Adapter](build-a-runtime): implement memory support in your runtime
- [Configuration Reference](../reference/configuration): memory store settings
- [Glossary](../reference/glossary): Memory Store definition
