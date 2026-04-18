---
layout: default
title: "Session Derive and Replay"
parent: Tutorials
nav_order: 11
---

# Session Derive and Replay

**Persona:** Client Developer | **Difficulty:** Intermediate

Lenny supports two ways to build on completed sessions: derive (fork a session's workspace into a new session) and replay (re-run a session's prompt history against a different runtime). Derive supports iterative development; replay supports regression testing across runtime versions.

## Prerequisites

- Lenny running locally via `docker compose up`
- At least one completed session
- Two runtime versions registered (for the replay step)
- Familiarity with [Your First Session](first-session)
- curl and jq installed

---

## Step 1: Create and Complete a Session

Run a session through the standard lifecycle:

```bash
# Create
SESSION_ID=$(curl -s -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtime": "claude-worker"}' | jq -r '.session_id')

UPLOAD_TOKEN=$(curl -s http://localhost:8080/v1/sessions/${SESSION_ID} | jq -r '.uploadToken')

# Upload a file
echo "package main\nfunc main() { println(\"hello\") }" > /tmp/main.go
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/upload" \
  -H "Authorization: UploadToken ${UPLOAD_TOKEN}" \
  -F "files=@/tmp/main.go;filename=main.go"

# Finalize and start
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/finalize" \
  -H "Authorization: UploadToken ${UPLOAD_TOKEN}" \
  -H "Content-Type: application/json" -d '{}'

curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/start" \
  -H "Content-Type: application/json" -d '{}'

# Send a message
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{"input": [{"type": "text", "inline": "Add error handling to main.go"}]}'

# Terminate
curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/terminate" \
  -H "Content-Type: application/json" -d '{}'
```

The session is now in `completed` state with a sealed workspace snapshot.

---

## Step 2: Derive a New Session

Derive creates a new session pre-populated with the source session's workspace. The derived session has its own independent lifecycle.

```bash
DERIVED=$(curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/derive" \
  -H "Content-Type: application/json" \
  -d '{}')

echo $DERIVED | jq .
```

Expected response:

```json
{
  "session_id": "sess_02...",
  "uploadToken": "sess_02....a3f1c7e2d9b8",
  "workspaceSnapshotSource": "sealed",
  "workspaceSnapshotTimestamp": "2026-04-12T10:20:00Z"
}
```

The derived session starts in `created` state with the source's workspace already staged. You can upload additional files, finalize, and start it:

```bash
DERIVED_ID=$(echo $DERIVED | jq -r '.session_id')
DERIVED_TOKEN=$(echo $DERIVED | jq -r '.uploadToken')

curl -s -X POST "http://localhost:8080/v1/sessions/${DERIVED_ID}/finalize" \
  -H "Authorization: UploadToken ${DERIVED_TOKEN}" \
  -H "Content-Type: application/json" -d '{}'

curl -s -X POST "http://localhost:8080/v1/sessions/${DERIVED_ID}/start" \
  -H "Content-Type: application/json" -d '{}'

curl -s -X POST "http://localhost:8080/v1/sessions/${DERIVED_ID}/messages" \
  -H "Content-Type: application/json" \
  -d '{"input": [{"type": "text", "inline": "Now add unit tests for main.go"}]}'
```

The derived session has the modified `main.go` from the source session, so the agent can build on previous work.

Deriving from live sessions: by default, derive requires the source session to be in a terminal state. To derive from a running session, pass `"allowStale": true`; this uses the most recent checkpoint snapshot.

---

## Step 3: Replay Against a Different Runtime

Replay re-runs the source session's prompt history against a different runtime version. Use it for regression testing runtime upgrades.

```bash
REPLAY=$(curl -s -X POST "http://localhost:8080/v1/sessions/${SESSION_ID}/replay" \
  -H "Content-Type: application/json" \
  -d '{
    "targetRuntime": "claude-worker-v2",
    "replayMode": "prompt_history"
  }')

echo $REPLAY | jq .
```

Expected response:

```json
{
  "session_id": "sess_03...",
  "uploadToken": "sess_03....b4g2d8f3e0c9",
  "sessionIsolationLevel": {
    "executionMode": "session",
    "isolationProfile": "runc"
  }
}
```

The replayed session:
1. Gets the source session's sealed workspace
2. Receives the same prompt history as input
3. Runs against `claude-worker-v2` instead of the original runtime
4. Proceeds through the standard lifecycle independently

**Replay modes:**
- `prompt_history` (default): Replays the full prompt sequence from the source transcript.
- `workspace_derive`: Starts with the source workspace but no pre-loaded prompts (clean start with identical filesystem).

**Constraints:**
- The source session must be in a terminal state (otherwise returns `REPLAY_ON_LIVE_SESSION`).
- The target runtime must have the same `executionMode` as the source (otherwise returns `INCOMPATIBLE_RUNTIME`).

---

## Key Concepts

- Derive forks a workspace for iterative development; continue where a previous session left off.
- Replay runs the same prompts against a different runtime for regression testing.
- Both operations create independent sessions with their own credentials and lifecycle.
- Workspace snapshots come from the sealed workspace (terminal sessions) or the latest checkpoint (`allowStale: true`).

---

## Next Steps

- [Evaluation Scoring](evaluation-scoring): submit eval scores to compare replay results
- [REST API Reference](../api/rest): derive and replay endpoint details
- [Error Catalog](../reference/error-catalog): `DERIVE_ON_LIVE_SESSION`, `REPLAY_ON_LIVE_SESSION`, `INCOMPATIBLE_RUNTIME`
