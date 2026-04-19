---
layout: default
title: "Delegation & Tasks"
parent: "Client Guide"
nav_order: 4
---

# Delegation & Tasks

Lenny supports recursive delegation. An agent session can spawn child sessions, which can spawn their own children, forming a delegation tree. This page covers how clients observe and interact with delegation trees.

---

## Task Trees from the Client Perspective

When a root session delegates work to child sessions, the gateway maintains a complete task tree. As a client, you can:

- **View the tree**: see which sessions are running, completed, or failed
- **Monitor progress**: stream events from the root session to observe child state changes
- **Cancel subtrees**: terminate a child and all its descendants
- **Respond to elicitations**: answer human-in-the-loop prompts that surface from any depth in the tree
- **Retrieve aggregated usage**: see total token consumption across the entire tree

---

## Viewing the Delegation Tree

```
GET /v1/sessions/{id}/tree
Authorization: Bearer <token>
```

**Response** (`200 OK`):

```json
{
  "root": {
    "taskId": "sess_root",
    "sessionId": "sess_root",
    "state": "running",
    "runtimeRef": "orchestrator-agent",
    "children": [
      {
        "taskId": "sess_child1",
        "sessionId": "sess_child1",
        "state": "running",
        "runtimeRef": "code-reviewer",
        "children": [
          {
            "taskId": "sess_grandchild1",
            "sessionId": "sess_grandchild1",
            "state": "completed",
            "runtimeRef": "linter-agent",
            "children": []
          }
        ]
      },
      {
        "taskId": "sess_child2",
        "sessionId": "sess_child2",
        "state": "completed",
        "runtimeRef": "test-runner",
        "children": []
      }
    ]
  }
}
```

The tree structure shows:

- `taskId` / `sessionId`: identifiers for the session at each node
- `state`: current state of each session
- `runtimeRef`: which runtime is running at each node
- `children`: nested child sessions

---

## Aggregated Usage

```
GET /v1/sessions/{id}/usage
Authorization: Bearer <token>
```

**Response** (`200 OK`):

```json
{
  "inputTokens": 15000,
  "outputTokens": 8000,
  "wallClockSeconds": 120,
  "podMinutes": 2.1,
  "credentialLeaseMinutes": 1.8,
  "treeUsage": {
    "inputTokens": 45000,
    "outputTokens": 22000,
    "wallClockSeconds": 450,
    "podMinutes": 12.5,
    "credentialLeaseMinutes": 10.2,
    "totalTasks": 4
  }
}
```

The `treeUsage` field aggregates usage across all descendant tasks. It is populated only after all descendants have settled (reached a terminal state). While children are still running, `treeUsage` is `null`.

---

## Task States and Transitions

Tasks in the delegation tree follow the Lenny canonical task state machine:

```
submitted --> running --> completed (terminal)
                      --> failed (terminal)
                      --> cancelled (terminal)
                      --> expired (terminal)
                      --> input_required (sub-state of running)

input_required --> running (input provided)
input_required --> cancelled (parent cancels)
input_required --> expired (deadline reached)
```

Terminal states: `completed`, `failed`, `cancelled`, `expired`.

The `input_required` sub-state indicates the child agent has called `lenny/request_input` and is blocked waiting for a response from its parent (or, if the elicitation chain reaches the client, from the human user).

---

## Watching Delegation Progress via Streaming

When you stream events from a root session (via SSE or MCP), you see events from the entire delegation tree:

```
event: status_change
data: {"state": "running"}

event: agent_output
data: {"output": [{"type": "text", "inline": "Delegating code review to child agent..."}]}

event: status_change
data: {"state": "running", "childId": "sess_child1", "childState": "running"}

event: agent_output
data: {"output": [{"type": "text", "inline": "Child agent reviewing code..."}]}

event: status_change
data: {"state": "running", "childId": "sess_child1", "childState": "completed"}

event: agent_output
data: {"output": [{"type": "text", "inline": "Code review complete. 3 issues found."}]}
```

Key events to watch for:

| Event | Meaning |
|---|---|
| `status_change` with `childState` | A child session changed state |
| `elicitation_request` | A child (or grandchild) needs human input |
| `session_complete` | The root session finished |

---

## Cancelling Subtrees

Cancel a specific child session and all its descendants:

```
DELETE /v1/sessions/{child_session_id}
Authorization: Bearer <token>
```

This cascades cancellation through the child's entire subtree. The parent session continues running and receives a `failed` or `cancelled` result for that child.

---

## Elicitation: Human-in-the-Loop Prompts

When an agent (at any depth in the delegation tree) needs human input, it calls `lenny/request_elicitation`. The request bubbles up through the delegation chain, hop by hop, until it reaches the client:

```
Agent (depth 3) --> Parent (depth 2) --> Parent (depth 1) --> Gateway --> Client
```

The client sees an `elicitation_request` event on the root session's event stream:

```
event: elicitation_request
data: {
  "elicitation_id": "elic_abc123",
  "message": "The code requires database credentials. Please provide the connection string.",
  "schema": {
    "type": "object",
    "properties": {
      "connectionString": {"type": "string", "description": "PostgreSQL connection string"}
    },
    "required": ["connectionString"]
  },
  "provenance": {
    "origin_pod": "pod-xyz",
    "delegation_depth": 3,
    "origin_runtime": "db-migration-agent",
    "purpose": "user_confirmation",
    "initiator_type": "agent"
  }
}
```

### Elicitation Provenance Metadata

Every elicitation includes provenance metadata so clients can make informed trust decisions:

| Field | Description |
|---|---|
| `origin_pod` | Which pod initiated the elicitation |
| `delegation_depth` | How deep in the task tree (0 = root, 1 = direct child, etc.) |
| `origin_runtime` | Runtime type of the originating pod |
| `purpose` | Stated purpose (e.g., `oauth_login`, `user_confirmation`) |
| `connector_id` | Registered connector ID (for OAuth flows) |
| `expected_domain` | Expected OAuth endpoint domain (for URL-mode elicitations) |
| `initiator_type` | `connector` (gateway-registered, higher trust) or `agent` (agent-initiated) |

Client UIs **must** display provenance so users can distinguish platform OAuth flows from agent-initiated prompts. Connector-initiated elicitations (`initiator_type: "connector"`) carry higher trust than agent-initiated ones.

### Responding to an Elicitation

```
POST /v1/sessions/{id}/elicitations/{elicitation_id}/respond
Content-Type: application/json
Authorization: Bearer <token>

{
  "response": {
    "connectionString": "postgresql://user:pass@host:5432/db"
  }
}
```

**Response** (`200 OK`):

```json
{
  "status": "delivered"
}
```

The response flows back down the elicitation chain to the originating agent.

### Dismissing an Elicitation

If the user declines to answer:

```
POST /v1/sessions/{id}/elicitations/{elicitation_id}/dismiss
Authorization: Bearer <token>
```

The originating agent receives a dismissal response and must handle it gracefully (equivalent to "user declined").

### Elicitation Constraints

- **Timeout**: `maxElicitationWait` (default 600s) limits how long a session waits for a response
- **Budget**: `maxElicitationsPerSession` (default 50) prevents agents from spamming the user
- **Depth suppression**: At delegation depth >= 3, agent-initiated elicitations are auto-suppressed by default
- **URL-mode restrictions**: URL-mode elicitations (e.g., OAuth flows) from agents are blocked by default unless the pool allowlists specific domains

---

## Delegation Enforcement

The gateway enforces several constraints at every delegation hop. Understanding these helps you design delegation trees that succeed on the first attempt.

### Token Budgets

When a parent session delegates work, it allocates a `maxTokenBudget` to the child. This budget is reserved atomically using Redis Lua scripts: the parent's available budget is decremented at delegation time, not when tokens are actually consumed. If a child finishes under budget, unused tokens are credited back to the parent. Children cannot exceed their allocated budget; the gateway rejects LLM requests once the budget is exhausted with `BUDGET_EXHAUSTED`.

### Scope Narrowing

Child leases are always strictly equal to or narrower than their parent's lease. A child session cannot have connectors, runtimes, or capabilities that the parent lacks. Depth is decremented, budgets are reduced, and any restriction the parent carries is inherited by the child. The gateway validates this before approving any delegation.

### Content Policy Inheritance

Content policies (`contentPolicy.interceptorRef`) can only be made stricter at each delegation hop, never relaxed. A child may retain the parent's interceptor or add one where the parent had none, but it cannot remove a content check (`CONTENT_POLICY_WEAKENING`) or substitute a different interceptor (`CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`). Content scanning cannot be bypassed by delegation depth.

### Isolation Monotonicity

Children must use an isolation profile at least as strong as their parent. The enforcement order is: `standard` (runc) < `sandboxed` (gVisor) < `microvm` (Kata). A `sandboxed` parent cannot delegate to a `standard` child; the gateway rejects such delegations with `ISOLATION_MONOTONICITY_VIOLATED`.

### Cycle Detection

The gateway prevents runtime delegation loops (e.g., A delegates to B, B delegates back to A). It checks each delegation request against the caller's delegation lineage. If the target's resolved `(runtime_name, pool_name)` identity already appears in the lineage, the request is rejected with `DELEGATION_CYCLE_DETECTED`. This check uses Postgres-backed session records and remains enforced even during Redis outages.

### Credential Propagation

The `credentialPropagation` field in the delegation lease controls how child sessions get LLM credentials. Three modes are available:

| Mode | Behavior |
|---|---|
| `inherit` | Child uses the same credential pool/source as its parent. The gateway assigns from the same pool. |
| `independent` | Child gets its own credential lease based on the tenant's credential policy and the child runtime's `supportedProviders`. |
| `deny` | Child receives no LLM credentials. Use this for runtimes that do not need LLM access (e.g., pure file-processing tools). |

Each parent controls its direct children's credential mode. In a multi-level tree, different hops can use different modes.

---

## Approving or Denying Tool Calls

Some runtimes require client approval for specific tool calls. When the agent requests a tool that needs approval:

```
event: tool_use_requested
data: {"tool_call_id": "tc_001", "tool": "delete_file", "args": {"path": "important.db"}}
```

Approve the tool call:

```
POST /v1/sessions/{id}/tool-use/tc_001/approve
Authorization: Bearer <token>
```

Or deny it:

```
POST /v1/sessions/{id}/tool-use/tc_001/deny
Content-Type: application/json
Authorization: Bearer <token>

{
  "reason": "This file should not be deleted"
}
```

---

## Example: Monitoring a Multi-Agent Delegation

```python
import httpx
import json

LENNY_URL = "https://lenny.example.com"
TOKEN = "your-access-token"


async def monitor_delegation(session_id: str):
    """Monitor a multi-agent delegation tree in real-time."""

    async with httpx.AsyncClient() as client:
        # Start streaming events
        async with client.stream(
            "GET",
            f"{LENNY_URL}/v1/sessions/{session_id}/logs",
            headers={
                "Authorization": f"Bearer {TOKEN}",
                "Accept": "text/event-stream",
            },
            timeout=None,
        ) as response:
            event_type = None
            data_lines = []

            async for line in response.aiter_lines():
                if line.startswith("event: "):
                    event_type = line[7:]
                elif line.startswith("data: "):
                    data_lines.append(line[6:])
                elif line == "":
                    if event_type and data_lines:
                        data = json.loads("\n".join(data_lines))
                        await handle_delegation_event(
                            client, session_id, event_type, data
                        )

                        if event_type == "session_complete":
                            # Print final tree
                            tree = await get_tree(client, session_id)
                            print("\nFinal delegation tree:")
                            print_tree(tree["root"], indent=0)
                            return

                    event_type = None
                    data_lines = []


async def handle_delegation_event(
    client: httpx.AsyncClient,
    session_id: str,
    event_type: str,
    data: dict,
):
    if event_type == "agent_output":
        for part in data.get("output", []):
            if part["type"] == "text":
                print(part.get("inline", ""), end="", flush=True)

    elif event_type == "elicitation_request":
        print(f"\n{'='*60}")
        print(f"ELICITATION from depth {data['provenance']['delegation_depth']}")
        print(f"  Runtime: {data['provenance']['origin_runtime']}")
        print(f"  Type: {data['provenance']['initiator_type']}")
        print(f"  Message: {data['message']}")
        print(f"{'='*60}")

        # Auto-respond for demonstration (in production, prompt the user)
        user_response = input("Your response (or 'dismiss'): ")
        if user_response.lower() == "dismiss":
            await client.post(
                f"{LENNY_URL}/v1/sessions/{session_id}/elicitations/{data['elicitation_id']}/dismiss",
                headers={"Authorization": f"Bearer {TOKEN}"},
            )
        else:
            await client.post(
                f"{LENNY_URL}/v1/sessions/{session_id}/elicitations/{data['elicitation_id']}/respond",
                headers={
                    "Authorization": f"Bearer {TOKEN}",
                    "Content-Type": "application/json",
                },
                json={"response": user_response},
            )

    elif event_type == "status_change":
        state = data.get("state", "")
        child_id = data.get("childId")
        child_state = data.get("childState")
        if child_id:
            print(f"\n[Child {child_id}: {child_state}]")
        else:
            print(f"\n[Root session: {state}]")

    elif event_type == "error":
        print(f"\n[ERROR: {data['code']} - {data['message']}]")

    elif event_type == "session_complete":
        print("\n[Session complete]")


async def get_tree(client: httpx.AsyncClient, session_id: str) -> dict:
    response = await client.get(
        f"{LENNY_URL}/v1/sessions/{session_id}/tree",
        headers={"Authorization": f"Bearer {TOKEN}"},
    )
    response.raise_for_status()
    return response.json()


def print_tree(node: dict, indent: int):
    prefix = "  " * indent
    state_icon = {
        "completed": "[done]",
        "failed": "[FAIL]",
        "cancelled": "[cancel]",
        "running": "[...]",
    }.get(node["state"], f"[{node['state']}]")

    print(f"{prefix}{state_icon} {node['runtimeRef']} ({node['sessionId']})")
    for child in node.get("children", []):
        print_tree(child, indent + 1)


# Usage
import asyncio
asyncio.run(monitor_delegation("sess_root"))
```
