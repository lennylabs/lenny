---
layout: default
title: Quickstart
parent: Getting Started
nav_order: 1
---

# Quickstart

**Goal:** Clone the repository, start Lenny locally, and complete a full echo session -- all in under 5 minutes.

This guide uses Lenny's **Tier 1 local development mode** (`make run`), which runs the entire platform as a single Go binary with no external dependencies. No Kubernetes, Docker, Postgres, Redis, or MinIO required.

---

## Prerequisites

| Requirement | Version | Notes |
|------------|---------|-------|
| **Go** | 1.22+ | [Install Go](https://go.dev/dl/) |
| **Git** | Any recent | To clone the repository |
| **curl** | Any recent | To interact with the API (or use any HTTP client) |

That is all you need. Tier 1 mode embeds everything.

---

## Step 1: Clone the repository

```bash
git clone https://github.com/your-org/lenny.git
cd lenny
```

---

## Step 2: Start Lenny

```bash
make run
```

This single command starts a **single Go binary** that runs three components as goroutines within one process:

| Component | What it does |
|-----------|-------------|
| **Gateway** | The API entry point. Handles authentication, session routing, file uploads, and stream proxying. In dev mode, it listens on `http://localhost:8080`. |
| **Controller simulator** | Simulates the Warm Pool Controller. Instead of managing Kubernetes CRDs, it manages a single in-process "pod" that runs the echo runtime. |
| **Echo runtime** | A built-in agent runtime that echoes back whatever you send it. It implements the Minimum-tier adapter protocol (stdin/stdout JSON Lines). No LLM credentials required. |

**Storage in Tier 1 mode:**

| Production backend | Tier 1 replacement | Purpose |
|---|---|---|
| Postgres | **Embedded SQLite** | Session metadata, task records, tenant configuration |
| Redis | **In-memory caches** | Pub/sub, ephemeral state, routing cache, lease coordination |
| MinIO | **Local filesystem** (`./lenny-data/`) | Artifact storage (uploaded files, workspace snapshots, checkpoints) |

You should see output indicating the gateway is listening:

```
INFO  gateway listening on :8080
INFO  controller-sim started (echo runtime ready)
INFO  warm pool: 1 pod idle (echo)
WARN  WARNING: TLS disabled — dev mode active. Do not use in production.
```

The TLS warning is expected and repeats every 60 seconds. Tier 1 mode sets `LENNY_DEV_MODE=true` automatically.

---

## Step 3: Create a session

Open a new terminal and create a session using the echo runtime:

```bash
curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "echo"
  }' | jq .
```

**Response:**

```json
{
  "session_id": "ses_abc123",
  "uploadToken": "ses_abc123.1712700000.a1b2c3d4e5f6",
  "sessionIsolationLevel": {
    "executionMode": "session",
    "isolationProfile": "runc",
    "podReuse": false
  }
}
```

Save the `session_id` and `uploadToken` for subsequent steps:

```bash
export SESSION_ID="ses_abc123"
export UPLOAD_TOKEN="ses_abc123.1712700000.a1b2c3d4e5f6"
```

Replace the values above with the actual values from the response.

**What happened:** The gateway authenticated the request (dev mode uses a permissive default), selected the echo runtime's pool, claimed an idle warm pod from the controller simulator, persisted the session record to SQLite, and returned the session ID along with an upload token for file delivery.

---

## Step 4: Upload a file

Upload a workspace file to the session. This is optional for the echo runtime (it does not read files), but demonstrates the workspace delivery mechanism:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/upload" \
  -H "Authorization: Bearer ${UPLOAD_TOKEN}" \
  -F "file=@README.md;filename=README.md" | jq .
```

**Response:**

```json
{
  "uploaded": [
    {
      "path": "README.md",
      "size": 1234
    }
  ]
}
```

**What happened:** The gateway validated the upload token (session-scoped, HMAC-signed, short-lived), streamed the file into the pod's staging area (`/workspace/staging`), and recorded the upload. The file is staged but not yet visible to the runtime.

---

## Step 5: Finalize the workspace

Finalize the workspace to make uploaded files available to the runtime:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/finalize" \
  -H "Authorization: Bearer ${UPLOAD_TOKEN}" | jq .
```

**Response:**

```json
{
  "status": "finalized",
  "workspace": {
    "files": ["README.md"],
    "cwd": "/workspace/current"
  }
}
```

**What happened:** The gateway instructed the pod's adapter to validate the staging area and atomically materialize it to `/workspace/current`. Any setup commands defined on the runtime would execute at this point (the echo runtime defines none). The upload token is now invalidated -- it cannot be reused.

---

## Step 6: Start the session

Start the agent runtime within the pod:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/start" | jq .
```

**Response:**

```json
{
  "status": "running"
}
```

**What happened:** The gateway called the adapter's `StartSession` RPC, which spawned the echo runtime binary with its working directory set to `/workspace/current`. The runtime is now reading from stdin and ready to receive messages.

---

## Step 7: Send a message

Send a message and see the echo output:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{
    "input": [
      {
        "type": "text",
        "text": "Hello, Lenny!"
      }
    ]
  }' | jq .
```

**Response (streamed):**

```json
{
  "events": [
    {
      "type": "agent_output",
      "parts": [
        {
          "type": "text",
          "text": "Echo: Hello, Lenny!"
        }
      ]
    },
    {
      "type": "status_change",
      "state": "suspended"
    }
  ]
}
```

**What happened:** The gateway delivered the message to the echo runtime via stdin as a `{type: "message"}` JSON Line. The runtime read it, prepended "Echo: ", and wrote a `{type: "response"}` JSON Line to stdout. The gateway relayed the response back to you as an `agent_output` event. The echo runtime then suspended, waiting for the next message.

You can send additional messages -- the echo runtime supports multi-turn interaction.

---

## Step 8: Terminate the session

When you are done, terminate the session:

```bash
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/terminate" | jq .
```

**Response:**

```json
{
  "status": "completed",
  "session_id": "ses_abc123"
}
```

**What happened:** The gateway sent a `{type: "shutdown"}` message to the runtime via stdin, sealed the workspace (exported a final snapshot to `./lenny-data/`), terminated the pod, marked the session as `completed` in SQLite, and released the pod back to the controller simulator for cleanup.

---

## What just happened?

You walked through the complete session lifecycle that Lenny manages for every agent session:

```
CreateSession       →  Gateway claims a warm pod from the pool
UploadWorkspace     →  Files are staged on the pod via gateway-mediated delivery
FinalizeWorkspace   →  Staging area is materialized to /workspace/current
StartSession        →  Agent runtime binary is started
SendMessage         →  Messages flow through the gateway's stream proxy
Terminate           →  Workspace is sealed, pod is released, session is completed
```

In a production deployment, this same flow runs on Kubernetes with real isolation (gVisor/Kata), mTLS between gateway and pods, credential leasing from a KMS-backed Token Service, workspace checkpointing to MinIO, and horizontal scaling across gateway replicas.

The key design principles you saw in action:

- **Gateway-centric:** Every interaction went through `localhost:8080`. The pod was never directly exposed.
- **Pod-local workspace, gateway-owned state:** Files were delivered through the gateway, not mounted from shared storage.
- **Pre-warm everything possible:** The pod was already running and idle before your session started. Workspace setup was the only hot-path work.
- **Zero-credential mode:** The echo runtime required no LLM API keys, demonstrating that platform mechanics work independently of provider credentials.

---

## Next steps

### For runtime authors

You just saw the echo runtime in action. It implements the **Minimum tier** (stdin/stdout JSON Lines only). To build your own runtime:

1. Read [Core Concepts](concepts.html) -- focus on **Runtimes** and **Workspaces**.
2. Copy the echo runtime as your starting point.
3. Test locally with `make run LENNY_AGENT_BINARY=/path/to/your-binary`.
4. Graduate to Standard tier (add MCP tool access) or Full tier (add lifecycle channel for checkpointing and credential rotation).

### For platform operators

You ran Lenny in its simplest mode. To move toward production:

1. Read [Architecture Overview](architecture.html) to understand all components.
2. Try **Tier 2** (`docker compose up`) for a production-like local environment with real Postgres, Redis, and MinIO.
3. Proceed to the **Operator Guide** for Helm chart configuration, capacity planning, and security hardening.

### For client developers

You used raw curl commands. To build a real integration:

1. Read [Core Concepts](concepts.html) -- focus on **Sessions** and **MCP**.
2. Explore the full **API Reference** for all endpoints, streaming semantics, and error codes.
3. Learn about **delegation** to build multi-agent workflows.

### For contributors

You have a working local environment. To contribute:

1. Read [Core Concepts](concepts.html) and [Architecture Overview](architecture.html) for the full mental model.
2. Read the **Technical Design** document for the authoritative specification.
3. Run `make test-smoke` to verify the full pipeline in under 10 seconds.
