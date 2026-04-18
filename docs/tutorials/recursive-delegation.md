---
layout: default
title: "Recursive Delegation"
parent: Tutorials
nav_order: 4
---

# Recursive Delegation

**Persona:** Runtime Author + Client Developer | **Difficulty:** Advanced

Recursive delegation is a platform primitive in Lenny. A parent agent can spawn child agents through the gateway, pass them a subset of its workspace, allocate a token budget, and wait for results. The gateway enforces scope, budget, and lineage at every hop.

In this tutorial you will:
1. Build a "coordinator" runtime that receives a task and delegates sub-tasks to workers
2. Build a "worker" runtime that processes individual sub-tasks
3. Configure a DelegationPolicy to control what the coordinator can delegate to
4. Run the delegation chain locally
5. Inspect the task tree and observe budget enforcement

## Prerequisites

- Go 1.22+ installed
- Lenny running locally via `docker compose up` (required for Standard-level MCP integration)
- Familiarity with [Build a Runtime Adapter](build-a-runtime)
- Familiarity with [Your First Session](first-session)

---

## Concepts

### How Delegation Works

```
                        Gateway
                          |
                    +-----------+
                    | Coordinator|  (parent session)
                    +-----------+
                     /          \
              +--------+    +--------+
              | Worker |    | Worker |   (child sessions)
              +--------+    +--------+
```

1. The coordinator calls `lenny/delegate_task(target, task, lease_slice)` on Lenny's local tool server
2. The gateway validates the delegation against the parent's lease (depth, fan-out, budget)
3. The gateway creates a child session: claims a pod, streams workspace files, starts the child
4. The gateway creates a **virtual MCP child interface** and injects it into the parent
5. The parent calls `lenny/await_children(child_ids, mode)` to wait for results
6. When children complete, unused budget is returned to the parent

### Key Constraints

- Children cannot exceed the parent's budget (budgets are strictly narrowing)
- Children cannot use a less restrictive isolation profile than the parent
- Delegation depth, total children, and parallel children are all bounded by the lease
- All delegation goes through the gateway -- pods never communicate directly

---

## Part 1: Build the Worker Runtime

The worker is a Standard-level runtime that receives a sub-task, processes it, and returns a result. For this tutorial, it performs string transformations.

```go
// file: cmd/worker-runtime/main.go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	mcpclient "github.com/example/mcp-go/client"
)

// --- Protocol Types ---

type InboundMessage struct {
	Type  string      `json:"type"`
	ID    string      `json:"id,omitempty"`
	Input []InputPart `json:"input,omitempty"`
	Ts    int64       `json:"ts,omitempty"`
}

type InputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

type OutputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

type ResponseMsg struct {
	Type   string       `json:"type"`
	Output []OutputPart `json:"output"`
}

// --- Manifest ---

type AdapterManifest struct {
	PlatformMcpServer struct {
		Socket string `json:"socket"`
	} `json:"platformMcpServer"`
	McpNonce  string `json:"mcpNonce"`
	SessionID string `json:"sessionId"`
}

func main() {
	// 1. Read the adapter manifest to get MCP server connection info
	manifestData, err := os.ReadFile("/run/lenny/adapter-manifest.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: cannot read manifest: %v\n", err)
		// Fall back to Basic-level mode (no MCP)
		runMinimumTier()
		return
	}

	var manifest AdapterManifest
	json.Unmarshal(manifestData, &manifest)

	// 2. Connect to Lenny's local tool server
	ctx := context.Background()
	mcp, err := mcpclient.Connect(ctx, mcpclient.ConnectOptions{
		Socket:   manifest.PlatformMcpServer.Socket,
		Nonce:    manifest.McpNonce,
		ClientInfo: mcpclient.ClientInfo{
			Name:    "worker-runtime",
			Version: "1.0.0",
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker: MCP connect failed: %v\n", err)
		runMinimumTier()
		return
	}
	defer mcp.Close()

	// 3. Process messages from stdin
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var msg InboundMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "message":
			text := extractText(msg.Input)

			// Perform the transformation
			result := processTask(text)

			// Emit incremental output via lenny/output (Standard level)
			mcp.CallTool(ctx, "lenny/output", map[string]interface{}{
				"output": []OutputPart{
					{Type: "text", Inline: fmt.Sprintf("Processing: %s", text)},
				},
			})

			// Send final response
			resp := ResponseMsg{
				Type: "response",
				Output: []OutputPart{
					{Type: "text", Inline: result},
				},
			}
			writeJSON(resp)

		case "heartbeat":
			writeJSON(map[string]string{"type": "heartbeat_ack"})

		case "shutdown":
			os.Exit(0)
		}
	}
}

// processTask performs string transformations based on commands.
func processTask(text string) string {
	parts := strings.SplitN(text, ":", 2)
	if len(parts) != 2 {
		return "ERROR: expected format 'command:data'"
	}

	command := strings.TrimSpace(parts[0])
	data := strings.TrimSpace(parts[1])

	switch strings.ToLower(command) {
	case "uppercase":
		return strings.ToUpper(data)
	case "lowercase":
		return strings.ToLower(data)
	case "reverse":
		runes := []rune(data)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes)
	case "wordcount":
		return fmt.Sprintf("%d words", len(strings.Fields(data)))
	default:
		return fmt.Sprintf("unknown command: %s", command)
	}
}

func extractText(parts []InputPart) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Inline)
		}
	}
	return strings.Join(texts, " ")
}

func runMinimumTier() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		var msg InboundMessage
		json.Unmarshal(scanner.Bytes(), &msg)
		switch msg.Type {
		case "message":
			text := extractText(msg.Input)
			result := processTask(text)
			writeJSON(ResponseMsg{
				Type:   "response",
				Output: []OutputPart{{Type: "text", Inline: result}},
			})
		case "heartbeat":
			writeJSON(map[string]string{"type": "heartbeat_ack"})
		case "shutdown":
			os.Exit(0)
		}
	}
}

func writeJSON(v interface{}) {
	data, _ := json.Marshal(v)
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}
```

---

## Part 2: Build the Coordinator Runtime

The coordinator is a Standard-level runtime that:
1. Receives a task with multiple sub-tasks
2. Discovers available worker agents via `lenny/discover_agents`
3. Delegates each sub-task to a worker via `lenny/delegate_task`
4. Waits for all children via `lenny/await_children`
5. Aggregates and returns results

```go
// file: cmd/coordinator-runtime/main.go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	mcpclient "github.com/example/mcp-go/client"
)

type InboundMessage struct {
	Type  string      `json:"type"`
	ID    string      `json:"id,omitempty"`
	Input []InputPart `json:"input,omitempty"`
	Ts    int64       `json:"ts,omitempty"`
}

type InputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

type OutputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

type ResponseMsg struct {
	Type   string       `json:"type"`
	Output []OutputPart `json:"output"`
}

type AdapterManifest struct {
	PlatformMcpServer struct {
		Socket string `json:"socket"`
	} `json:"platformMcpServer"`
	McpNonce  string `json:"mcpNonce"`
	SessionID string `json:"sessionId"`
}

// --- Delegation Types ---

// TaskHandle is returned by lenny/delegate_task
type TaskHandle struct {
	TaskID    string `json:"taskId"`
	SessionID string `json:"sessionId"`
}

// ChildResult is returned by lenny/await_children
type ChildResult struct {
	TaskID string `json:"taskId"`
	State  string `json:"state"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

func main() {
	// 1. Read manifest
	manifestData, err := os.ReadFile("/run/lenny/adapter-manifest.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "coordinator: cannot read manifest: %v\n", err)
		os.Exit(1)
	}

	var manifest AdapterManifest
	json.Unmarshal(manifestData, &manifest)

	// 2. Connect to Lenny's local tool server
	ctx := context.Background()
	mcp, err := mcpclient.Connect(ctx, mcpclient.ConnectOptions{
		Socket:   manifest.PlatformMcpServer.Socket,
		Nonce:    manifest.McpNonce,
		ClientInfo: mcpclient.ClientInfo{
			Name:    "coordinator-runtime",
			Version: "1.0.0",
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "coordinator: MCP connect failed: %v\n", err)
		os.Exit(1)
	}
	defer mcp.Close()

	// 3. Process messages
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var msg InboundMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "message":
			handleTask(ctx, mcp, msg)

		case "heartbeat":
			writeJSON(map[string]string{"type": "heartbeat_ack"})

		case "shutdown":
			os.Exit(0)
		}
	}
}

func handleTask(ctx context.Context, mcp *mcpclient.Client, msg InboundMessage) {
	text := extractText(msg.Input)

	// Parse the task: expect "command1:data1 | command2:data2 | ..."
	subTasks := strings.Split(text, "|")
	if len(subTasks) == 0 {
		writeJSON(ResponseMsg{
			Type:   "response",
			Output: []OutputPart{{Type: "text", Inline: "No sub-tasks found. Use 'cmd1:data1 | cmd2:data2' format."}},
		})
		return
	}

	// --- Step A: Discover available workers ---
	emitStatus(mcp, ctx, "Discovering available worker agents...")

	discoverResult, err := mcp.CallTool(ctx, "lenny/discover_agents", map[string]interface{}{
		"filter": map[string]interface{}{
			"labels": map[string]string{"role": "worker"},
		},
	})
	if err != nil {
		writeJSON(ResponseMsg{
			Type:   "response",
			Output: []OutputPart{{Type: "text", Inline: fmt.Sprintf("Discovery failed: %v", err)}},
		})
		return
	}

	// Parse discovered agents
	var agents []map[string]interface{}
	json.Unmarshal(discoverResult, &agents)
	emitStatus(mcp, ctx, fmt.Sprintf("Found %d worker agent(s)", len(agents)))

	if len(agents) == 0 {
		writeJSON(ResponseMsg{
			Type:   "response",
			Output: []OutputPart{{Type: "text", Inline: "No worker agents available for delegation."}},
		})
		return
	}

	// Use the first discovered worker
	workerName := agents[0]["name"].(string)

	// --- Step B: Delegate each sub-task ---
	var childIDs []string

	for i, subTask := range subTasks {
		subTask = strings.TrimSpace(subTask)
		if subTask == "" {
			continue
		}

		emitStatus(mcp, ctx, fmt.Sprintf("Delegating sub-task %d/%d: %s", i+1, len(subTasks), subTask))

		// Call lenny/delegate_task with a budget slice
		result, err := mcp.CallTool(ctx, "lenny/delegate_task", map[string]interface{}{
			// Target is opaque -- the coordinator does not know if this is
			// a local runtime, derived runtime, or external agent.
			"target": workerName,

			// The task specification with input for the child
			"task": map[string]interface{}{
				"input": []OutputPart{
					{Type: "text", Inline: subTask},
				},
			},

			// Budget slice allocated from parent to child
			"lease_slice": map[string]interface{}{
				"maxTokenBudget": 10000,     // 10k tokens per child
				"perChildMaxAge": 300,        // 5 minutes max per child
			},
		})

		if err != nil {
			emitStatus(mcp, ctx, fmt.Sprintf("Delegation failed for sub-task %d: %v", i+1, err))
			continue
		}

		var handle TaskHandle
		json.Unmarshal(result, &handle)
		childIDs = append(childIDs, handle.TaskID)
		emitStatus(mcp, ctx, fmt.Sprintf("Delegated sub-task %d -> task %s", i+1, handle.TaskID))
	}

	if len(childIDs) == 0 {
		writeJSON(ResponseMsg{
			Type:   "response",
			Output: []OutputPart{{Type: "text", Inline: "All delegations failed."}},
		})
		return
	}

	// --- Step C: Wait for all children to complete ---
	emitStatus(mcp, ctx, fmt.Sprintf("Waiting for %d child task(s)...", len(childIDs)))

	// lenny/await_children blocks until all children reach a terminal state.
	// mode: "all" -- wait for every child
	// mode: "any" -- return as soon as one child completes
	// mode: "settled" -- return when all are terminal (including failures)
	awaitResult, err := mcp.CallTool(ctx, "lenny/await_children", map[string]interface{}{
		"child_ids": childIDs,
		"mode":      "all",
	})

	if err != nil {
		writeJSON(ResponseMsg{
			Type:   "response",
			Output: []OutputPart{{Type: "text", Inline: fmt.Sprintf("await_children failed: %v", err)}},
		})
		return
	}

	// --- Step D: Aggregate results ---
	var results []ChildResult
	json.Unmarshal(awaitResult, &results)

	var outputLines []string
	outputLines = append(outputLines, fmt.Sprintf("Completed %d sub-task(s):", len(results)))
	outputLines = append(outputLines, "")

	for i, r := range results {
		if r.State == "completed" {
			outputLines = append(outputLines, fmt.Sprintf("  [%d] %s -> %s", i+1, subTasks[i], r.Output))
		} else {
			outputLines = append(outputLines, fmt.Sprintf("  [%d] %s -> FAILED: %s", i+1, subTasks[i], r.Error))
		}
	}

	writeJSON(ResponseMsg{
		Type: "response",
		Output: []OutputPart{
			{Type: "text", Inline: strings.Join(outputLines, "\n")},
		},
	})
}

func emitStatus(mcp *mcpclient.Client, ctx context.Context, msg string) {
	mcp.CallTool(ctx, "lenny/output", map[string]interface{}{
		"output": []OutputPart{
			{Type: "text", Inline: msg},
		},
	})
}

func extractText(parts []InputPart) string {
	var texts []string
	for _, p := range parts {
		if p.Type == "text" {
			texts = append(texts, p.Inline)
		}
	}
	return strings.Join(texts, " ")
}

func writeJSON(v interface{}) {
	data, _ := json.Marshal(v)
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}
```

---

## Part 3: Configure the DelegationPolicy

The DelegationPolicy controls what the coordinator is allowed to delegate to. Register it via the admin API:

```bash
TOKEN="your-admin-token"

# Create the delegation policy
curl -s -X POST http://localhost:8080/v1/admin/delegation-policies \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "coordinator-policy",
    "rules": [
      {
        "target": {
          "matchLabels": {"role": "worker"},
          "types": ["agent"]
        },
        "allow": true
      }
    ],
    "contentPolicy": {
      "maxInputSize": 131072
    }
  }' | jq .
```

Expected response:

```json
{
  "name": "coordinator-policy",
  "rules": [
    {
      "target": {
        "matchLabels": {"role": "worker"},
        "types": ["agent"]
      },
      "allow": true
    }
  ],
  "contentPolicy": {
    "maxInputSize": 131072
  }
}
```

---

## Part 4: Register Both Runtimes

```bash
# Register the worker runtime
curl -s -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "text-worker",
    "type": "agent",
    "image": "worker-runtime:dev",
    "description": "Text transformation worker",
    "labels": {"role": "worker"},
    "capabilities": {
      "delegation": false
    }
  }' | jq .

# Register the coordinator runtime with delegation enabled
curl -s -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "text-coordinator",
    "type": "agent",
    "image": "coordinator-runtime:dev",
    "description": "Coordinator that delegates text processing to workers",
    "labels": {"role": "coordinator"},
    "delegationPolicyRef": "coordinator-policy",
    "capabilities": {
      "delegation": true
    }
  }' | jq .
```

Create pools for both runtimes:

```bash
# Worker pool -- needs enough warm pods for fan-out
curl -s -X POST http://localhost:8080/v1/admin/pools \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "worker-pool",
    "runtime": "text-worker",
    "namespace": "lenny-agents",
    "isolationProfile": "runc",
    "warmCount": {"min": 3, "max": 10}
  }' | jq .

# Coordinator pool
curl -s -X POST http://localhost:8080/v1/admin/pools \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "coordinator-pool",
    "runtime": "text-coordinator",
    "namespace": "lenny-agents",
    "isolationProfile": "runc",
    "warmCount": {"min": 1, "max": 3}
  }' | jq .
```

---

## Part 5: Run the Delegation Chain

Create a coordinator session and send it multiple sub-tasks separated by `|`:

```bash
# Start a coordinator session with a delegation lease
curl -s -X POST http://localhost:8080/v1/sessions/start \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "text-coordinator",
    "delegationLease": {
      "preset": "standard",
      "maxDepth": 2,
      "maxChildrenTotal": 5,
      "maxParallelChildren": 3,
      "maxTokenBudget": 100000
    },
    "input": [
      {
        "type": "text",
        "inline": "uppercase:hello world | lowercase:SHOUTING | reverse:backwards | wordcount:count these five words"
      }
    ]
  }' | jq .
```

Expected response:

```json
{
  "session_id": "sess_coordinator_01",
  "state": "running",
  "sessionIsolationLevel": {
    "executionMode": "session",
    "isolationProfile": "runc",
    "podReuse": false
  }
}
```

### Stream the Output

Watch the coordinator's output to see delegation in action:

```bash
curl -s -N "http://localhost:8080/v1/sessions/sess_coordinator_01/logs" \
  -H "Accept: text/event-stream" \
  -H "Authorization: Bearer ${TOKEN}"
```

Expected SSE events (in order):

```
event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Discovering available worker agents..."}]}

event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Found 1 worker agent(s)"}]}

event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Delegating sub-task 1/4: uppercase:hello world"}]}

event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Delegated sub-task 1 -> task task_child_01"}]}

event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Delegating sub-task 2/4: lowercase:SHOUTING"}]}

...

event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Waiting for 4 child task(s)..."}]}

event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Completed 4 sub-task(s):\n\n  [1] uppercase:hello world -> HELLO WORLD\n  [2] lowercase:SHOUTING -> shouting\n  [3] reverse:backwards -> sdrawkcab\n  [4] wordcount:count these five words -> 4 words"}]}

event: session_complete
data: {"type":"session_complete","result":{...}}
```

---

## Part 6: Inspect the Task Tree

The task tree shows the parent-child hierarchy with states:

```bash
curl -s "http://localhost:8080/v1/sessions/sess_coordinator_01/tree" \
  -H "Authorization: Bearer ${TOKEN}" | jq .
```

Expected response:

```json
{
  "root": {
    "taskId": "task_root",
    "sessionId": "sess_coordinator_01",
    "runtime": "text-coordinator",
    "state": "completed",
    "children": [
      {
        "taskId": "task_child_01",
        "sessionId": "sess_child_01",
        "runtime": "text-worker",
        "state": "completed"
      },
      {
        "taskId": "task_child_02",
        "sessionId": "sess_child_02",
        "runtime": "text-worker",
        "state": "completed"
      },
      {
        "taskId": "task_child_03",
        "sessionId": "sess_child_03",
        "runtime": "text-worker",
        "state": "completed"
      },
      {
        "taskId": "task_child_04",
        "sessionId": "sess_child_04",
        "runtime": "text-worker",
        "state": "completed"
      }
    ]
  }
}
```

---

## Part 7: Token Budget Enforcement

The delegation lease enforces a total token budget across the entire tree. Let us demonstrate what happens when the budget is exhausted.

Create a session with a very small budget:

```bash
curl -s -X POST http://localhost:8080/v1/sessions/start \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "text-coordinator",
    "delegationLease": {
      "maxDepth": 1,
      "maxChildrenTotal": 10,
      "maxParallelChildren": 3,
      "maxTokenBudget": 100
    },
    "input": [
      {
        "type": "text",
        "inline": "uppercase:a | uppercase:b | uppercase:c | uppercase:d | uppercase:e"
      }
    ]
  }' | jq .
```

After the first few children consume the budget, subsequent delegations will fail:

```
event: agent_output
data: {"type":"agent_output","parts":[{"type":"text","inline":"Delegation failed for sub-task 4: BUDGET_EXHAUSTED"}]}
```

Check the session's usage to see budget consumption:

```bash
curl -s "http://localhost:8080/v1/sessions/${SESSION_ID}/usage" \
  -H "Authorization: Bearer ${TOKEN}" | jq .
```

```json
{
  "tokenUsage": {
    "budget": 100,
    "consumed": 90,
    "remaining": 10
  },
  "treeStats": {
    "totalNodes": 4,
    "maxDepth": 1,
    "completedChildren": 3,
    "failedChildren": 0
  }
}
```

---

## Part 8: Scope Narrowing

Children always receive a **strictly narrower** scope than their parent. This is enforced automatically:

| Parent Lease | Child Lease (enforced) |
|-------------|----------------------|
| `maxDepth: 3` | `maxDepth: 2` (decremented) |
| `maxTokenBudget: 100000` | `maxTokenBudget: 10000` (sliced) |
| `maxChildrenTotal: 10` | `maxChildrenTotal: 5` (reduced) |
| `isolationProfile: gvisor` | Must be `gvisor` or `microvm` (never weaker) |

If a coordinator tries to delegate with a less restrictive isolation profile, the gateway rejects it:

```
ISOLATION_MONOTONICITY_VIOLATED: child isolation profile 'runc' is less
restrictive than parent profile 'gvisor'
```

Similarly, the delegation policy is always intersected -- children can never have broader permissions than their parent.

---

## Summary

In this tutorial you:

1. Built a worker runtime that processes text transformation sub-tasks
2. Built a coordinator runtime that discovers agents, delegates tasks, waits for results, and aggregates output
3. Configured a DelegationPolicy with tag-based matching rules
4. Ran a delegation chain with 4 parallel children
5. Inspected the task tree to see the parent-child hierarchy
6. Observed token budget enforcement when the budget was exhausted
7. Learned how scope narrowing ensures children never exceed their parent's authority

### Key Delegation Tools

| Tool | Purpose |
|------|---------|
| `lenny/discover_agents` | Find delegation targets authorized by your policy |
| `lenny/delegate_task` | Spawn a child session with a budget slice |
| `lenny/await_children` | Wait for children (`all`, `any`, or `settled`) |
| `lenny/cancel_child` | Cancel a child and its descendants |
| `lenny/get_task_tree` | View the full task hierarchy with states |

---

## Next Steps

- [MCP Client Integration](mcp-client-integration) -- interact with delegation trees from an MCP client
- [Deploy to Kubernetes](deploy-to-cluster) -- run your delegation setup in a real cluster
