---
layout: default
title: "Build a Runtime Adapter"
parent: Tutorials
nav_order: 2
---

# Build a Runtime Adapter

**Persona:** Runtime Author | **Difficulty:** Intermediate

In this tutorial you will build a custom Lenny runtime from scratch. You will start from the echo runtime sample, understand the adapter-to-binary protocol, and build a "calculator" runtime that declares a tool and handles tool calls. By the end you will have a working runtime registered in your local dev environment.

## Prerequisites

- Go 1.22+ installed
- Lenny running locally via `make run`
- Familiarity with the [Your First Session](first-session) tutorial

---

## Part 1: Understand the Protocol

Lenny runtimes communicate with the adapter via a **stdin/stdout JSON Lines protocol**. Each line is a complete JSON object terminated by `\n`. The adapter writes messages to your binary's stdin; your binary writes responses to stdout.

### Messages You Receive (stdin)

| Type | Purpose |
|------|---------|
| `message` | A user or agent message with input content |
| `heartbeat` | Liveness check -- you must reply with `heartbeat_ack` |
| `shutdown` | Graceful shutdown signal -- exit within `deadline_ms` |
| `tool_result` | Result of a tool call you previously requested |

### Messages You Send (stdout)

| Type | Purpose |
|------|---------|
| `response` | Your output -- text, structured data, etc. |
| `tool_call` | Request the adapter to execute a tool (e.g., `read_file`) |
| `heartbeat_ack` | Reply to a heartbeat |
| `status` | Optional progress update |

### Protocol Trace

Here is a minimal session exchange:

```
STDIN  -> {"type":"message","id":"msg_001","input":[{"type":"text","inline":"Hello"}]}
STDOUT <- {"type":"response","output":[{"type":"text","inline":"Echo: Hello"}]}
STDIN  -> {"type":"heartbeat","ts":1717430410}
STDOUT <- {"type":"heartbeat_ack"}
STDIN  -> {"type":"shutdown","reason":"drain","deadline_ms":10000}
         (process exits with code 0)
```

---

## Part 2: The Echo Runtime -- Starting Point

Before building the calculator, let us examine a complete echo runtime in Go. This is the minimal contract every Lenny runtime must implement.

```go
// file: cmd/echo-runtime/main.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Message represents any inbound JSON line from the adapter.
type Message struct {
	Type       string      `json:"type"`
	ID         string      `json:"id,omitempty"`
	Input      []InputPart `json:"input,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	DeadlineMs int         `json:"deadline_ms,omitempty"`
	Ts         int64       `json:"ts,omitempty"`
}

// InputPart is a single content part within a message.
type InputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

// Response is the outbound message format.
type Response struct {
	Type   string       `json:"type"`
	Output []OutputPart `json:"output,omitempty"`
}

// OutputPart is a single content part within a response.
type OutputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for large messages (1 MB)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	seq := 0

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg Message
		if err := json.Unmarshal(line, &msg); err != nil {
			// Protocol error -- log and continue
			fmt.Fprintf(os.Stderr, "parse error: %v\n", err)
			continue
		}

		switch msg.Type {
		case "message":
			// Echo the input back with a sequence number
			seq++
			text := ""
			for _, part := range msg.Input {
				text += part.Inline
			}

			resp := Response{
				Type: "response",
				Output: []OutputPart{
					{Type: "text", Inline: fmt.Sprintf("[%d] Echo: %s", seq, text)},
				},
			}
			writeJSON(resp)

		case "heartbeat":
			// Must respond to heartbeats within 10 seconds or get SIGTERM
			writeJSON(map[string]string{"type": "heartbeat_ack"})

		case "shutdown":
			// Graceful shutdown -- exit cleanly
			os.Exit(0)

		default:
			// Ignore unknown message types (forward compatibility)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stdin read error: %v\n", err)
		os.Exit(1)
	}
}

// writeJSON marshals a value and writes it as a single line to stdout.
func writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal error: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stdout, "%s\n", data)
}
```

---

## Part 3: Build the Calculator Runtime

Now let us build something more interesting. The calculator runtime will:

1. Accept messages containing math expressions
2. Declare a `calculate` tool that the agent can call
3. Handle `tool_call` requests from the adapter for workspace file operations
4. Parse and evaluate simple arithmetic expressions

### Full Source Code

```go
// file: cmd/calc-runtime/main.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// --- Protocol Types ---

// InboundMessage represents any JSON line received on stdin.
// We use a single type with optional fields and dispatch on "type".
type InboundMessage struct {
	Type       string      `json:"type"`
	ID         string      `json:"id,omitempty"`
	Input      []InputPart `json:"input,omitempty"`
	Ts         int64       `json:"ts,omitempty"`
	Reason     string      `json:"reason,omitempty"`
	DeadlineMs int         `json:"deadline_ms,omitempty"`

	// tool_result fields
	Content []InputPart `json:"content,omitempty"`
	IsError bool        `json:"isError,omitempty"`
}

type InputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

type OutputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline"`
}

// --- Outbound Message Types ---

type ResponseMsg struct {
	Type   string       `json:"type"`
	Output []OutputPart `json:"output"`
}

type ToolCallMsg struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// --- Calculator Logic ---

// evaluate handles simple arithmetic: "2 + 3", "10 * 5", etc.
// It supports +, -, *, / with two operands.
func evaluate(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)

	// Try to find an operator
	operators := []string{"+", "-", "*", "/"}
	for _, op := range operators {
		// Split on the operator, but handle negative numbers
		parts := strings.SplitN(expr, op, 2)
		if len(parts) != 2 {
			continue
		}

		left := strings.TrimSpace(parts[0])
		right := strings.TrimSpace(parts[1])

		if left == "" || right == "" {
			continue
		}

		a, errA := strconv.ParseFloat(left, 64)
		b, errB := strconv.ParseFloat(right, 64)

		if errA != nil || errB != nil {
			continue
		}

		switch op {
		case "+":
			return a + b, nil
		case "-":
			return a - b, nil
		case "*":
			return a * b, nil
		case "/":
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return a / b, nil
		}
	}

	return 0, fmt.Errorf("unsupported expression: %s", expr)
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	seq := 0
	toolCallCounter := 0

	// Track pending tool calls so we can process results
	pendingToolCalls := make(map[string]string) // tool_call_id -> original expression

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg InboundMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			fmt.Fprintf(os.Stderr, "calc-runtime: parse error: %v\n", err)
			continue
		}

		switch msg.Type {

		case "message":
			seq++
			// Extract the text input
			text := ""
			for _, part := range msg.Input {
				if part.Type == "text" {
					text += part.Inline
				}
			}
			text = strings.TrimSpace(text)

			// Check if this is a "save" command -- demonstrates tool_call usage
			if strings.HasPrefix(strings.ToLower(text), "save ") {
				// The user wants to save a result to a file.
				// We will call the adapter-local "write_file" tool.
				content := strings.TrimPrefix(text, "save ")
				content = strings.TrimPrefix(content, "Save ")
				toolCallCounter++
				callID := fmt.Sprintf("tc_%03d", toolCallCounter)

				pendingToolCalls[callID] = content

				call := ToolCallMsg{
					Type: "tool_call",
					ID:   callID,
					Name: "write_file",
					Arguments: map[string]interface{}{
						"path":    "result.txt",
						"content": content,
					},
				}
				writeJSON(call)
				// Do not send a response yet -- wait for tool_result
				continue
			}

			// Try to evaluate as a math expression
			result, err := evaluate(text)
			if err != nil {
				// Not a math expression -- provide a help message
				resp := ResponseMsg{
					Type: "response",
					Output: []OutputPart{
						{
							Type:   "text",
							Inline: fmt.Sprintf("[%d] Calculator ready. Send an expression like '2 + 3' or '10 * 5'. Use 'save <text>' to write to a file.", seq),
						},
					},
				}
				writeJSON(resp)
			} else {
				// Return the calculation result
				resp := ResponseMsg{
					Type: "response",
					Output: []OutputPart{
						{
							Type:   "text",
							Inline: fmt.Sprintf("[%d] %s = %s", seq, text, formatNumber(result)),
						},
					},
				}
				writeJSON(resp)
			}

		case "tool_result":
			// A tool call we made has completed.
			// msg.ID matches the tool_call.id we sent.
			originalContent, ok := pendingToolCalls[msg.ID]
			if !ok {
				fmt.Fprintf(os.Stderr, "calc-runtime: unexpected tool_result id: %s\n", msg.ID)
				continue
			}
			delete(pendingToolCalls, msg.ID)

			seq++
			if msg.IsError {
				errorText := "unknown error"
				if len(msg.Content) > 0 {
					errorText = msg.Content[0].Inline
				}
				resp := ResponseMsg{
					Type: "response",
					Output: []OutputPart{
						{
							Type:   "text",
							Inline: fmt.Sprintf("[%d] Failed to save: %s", seq, errorText),
						},
					},
				}
				writeJSON(resp)
			} else {
				resp := ResponseMsg{
					Type: "response",
					Output: []OutputPart{
						{
							Type:   "text",
							Inline: fmt.Sprintf("[%d] Saved '%s' to result.txt", seq, originalContent),
						},
					},
				}
				writeJSON(resp)
			}

		case "heartbeat":
			writeJSON(map[string]string{"type": "heartbeat_ack"})

		case "shutdown":
			// Clean up any resources if needed, then exit
			fmt.Fprintf(os.Stderr, "calc-runtime: shutting down (reason: %s)\n", msg.Reason)
			os.Exit(0)

		default:
			// Forward compatibility: ignore unknown message types
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "calc-runtime: stdin error: %v\n", err)
		os.Exit(1)
	}
}

// formatNumber removes trailing zeros from float representation.
func formatNumber(f float64) string {
	s := fmt.Sprintf("%g", f)
	return s
}

// writeJSON marshals a value and writes it as a single line to stdout.
func writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "calc-runtime: marshal error: %v\n", err)
		return
	}
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}
```

### Key Design Decisions

1. **Single stdin loop:** All messages arrive on stdin. We dispatch on `msg.Type` to handle different message kinds.

2. **Tool calls are asynchronous within the stdin channel:** When we emit a `tool_call`, we do not get the result immediately on the next line. Other messages (heartbeats, additional user messages) may arrive first. We track pending tool calls by ID.

3. **Heartbeats are critical:** If you do not respond to a heartbeat within 10 seconds, the adapter sends SIGTERM. Always handle them in your main loop.

4. **Unknown messages are ignored:** The protocol is forward-compatible. New message types may be added in future versions. Your runtime must not crash on unrecognized types.

---

## Part 4: Build and Test Locally

### Build the Binary

```bash
cd /path/to/your/project
go mod init calc-runtime
go build -o calc-runtime ./cmd/calc-runtime/
```

### Test Manually (without Lenny)

You can test the protocol manually by piping JSON lines:

```bash
echo '{"type":"message","id":"msg_001","input":[{"type":"text","inline":"3 + 7"}]}' | ./calc-runtime
```

Expected output:

```
{"type":"response","output":[{"type":"text","inline":"[1] 3 + 7 = 10"}]}
```

Test heartbeat handling:

```bash
echo '{"type":"heartbeat","ts":1717430410}' | ./calc-runtime
```

Expected output:

```
{"type":"heartbeat_ack"}
```

### Run with Lenny

Start Lenny with your custom binary:

```bash
make run LENNY_AGENT_BINARY=/path/to/calc-runtime
```

Then in another terminal, test it:

```bash
# Create and start a session
SESSION=$(curl -s -X POST http://localhost:8080/v1/sessions/start \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "echo",
    "input": [{"type": "text", "inline": "5 * 3"}]
  }')

echo $SESSION | jq .
```

---

## Part 5: Create a Dockerfile

For production or Tier 2 (`docker compose`) testing, package the runtime as a container image.

```dockerfile
# file: Dockerfile
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /calc-runtime ./cmd/calc-runtime/

FROM alpine:3.19

# The runtime runs as a non-root user (UID 1001).
# The adapter runs as UID 1000 in the sidecar container.
RUN adduser -D -u 1001 agent
USER agent

COPY --from=builder /calc-runtime /usr/local/bin/calc-runtime

# The adapter spawns this binary -- it is NOT the container entrypoint.
# The adapter is the entrypoint; it starts the runtime via the sidecar model.
# For embedded model testing, you can use this entrypoint directly:
ENTRYPOINT ["/usr/local/bin/calc-runtime"]
```

Build it:

```bash
docker build -t calc-runtime:dev .
```

---

## Part 6: Register in Local Dev Config

### Tier 2 (docker compose)

After building the image, register it via the admin API:

```bash
# Register the runtime
curl -s -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "calculator",
    "type": "agent",
    "image": "calc-runtime:dev",
    "description": "A simple arithmetic calculator runtime",
    "agentInterface": {
      "description": "Evaluates arithmetic expressions",
      "inputModes": [{"type": "text/plain"}],
      "outputModes": [{"type": "text/plain", "role": "primary"}],
      "skills": [
        {
          "id": "calculate",
          "name": "Arithmetic",
          "description": "Evaluates expressions like 2+3, 10*5, 100/4"
        }
      ]
    }
  }' | jq .
```

Alternatively, add it to your `lenny-data/seed.yaml`:

```yaml
runtimes:
  - name: calculator
    type: agent
    image: calc-runtime:dev
    description: A simple arithmetic calculator runtime
```

### Test with Your Runtime

```bash
curl -s -X POST http://localhost:8080/v1/sessions/start \
  -H "Content-Type: application/json" \
  -d '{
    "runtime": "calculator",
    "input": [{"type": "text", "inline": "42 / 6"}]
  }' | jq .
```

Expected response includes:

```json
{
  "session_id": "sess_...",
  "state": "running"
}
```

---

## Part 7: Upgrade to Standard Tier

The calculator runtime so far is Minimum tier -- it uses only stdin/stdout. To unlock platform capabilities like delegation, tool discovery, and elicitation, upgrade to Standard tier by connecting to the adapter's MCP servers.

### What Standard Tier Adds

- Access to platform MCP tools (`lenny/delegate_task`, `lenny/discover_agents`, `lenny/output`, etc.)
- Access to connector MCP servers (GitHub, Jira, etc.)
- Structured output via `lenny/output` for incremental delivery
- Health check integration

### Reading the Adapter Manifest

Standard-tier runtimes read `/run/lenny/adapter-manifest.json` on startup to discover MCP server socket paths:

```go
// file: internal/manifest/manifest.go
package manifest

import (
	"encoding/json"
	"os"
)

type AdapterManifest struct {
	Version          int               `json:"version"`
	PlatformMcpServer MCPServerConfig  `json:"platformMcpServer"`
	LifecycleChannel  MCPServerConfig  `json:"lifecycleChannel"`
	ConnectorServers  []ConnectorServer `json:"connectorServers"`
	AdapterLocalTools []ToolDef         `json:"adapterLocalTools"`
	SessionID        string            `json:"sessionId"`
	TaskID           string            `json:"taskId"`
	McpNonce         string            `json:"mcpNonce"`
}

type MCPServerConfig struct {
	Socket string `json:"socket"`
}

type ConnectorServer struct {
	ID     string `json:"id"`
	Socket string `json:"socket"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func Load(path string) (*AdapterManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m AdapterManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
```

### Connecting to the Platform MCP Server

Standard-tier runtimes connect to the platform MCP server as a standard MCP client. The connection uses abstract Unix sockets (Linux only -- use `docker compose` on macOS).

```go
// Pseudocode for MCP connection setup:
//
// 1. Read manifest from /run/lenny/adapter-manifest.json
// 2. Connect to platformMcpServer.socket (e.g., "@lenny-platform-mcp")
// 3. Send MCP initialize with _lennyNonce from manifest
// 4. Call tools/list to discover available tools
// 5. Use lenny/output to emit incremental output

manifest, err := manifest.Load("/run/lenny/adapter-manifest.json")
if err != nil {
    log.Fatal(err)
}

// Connect to platform MCP server using the mcp-go library
// The nonce must be sent in the initialize params:
// {"method": "initialize", "params": {"_lennyNonce": "<nonce>", ...}}
```

### Adding a Health Check

Standard-tier runtimes should implement the gRPC Health Checking Protocol. The warm pool controller marks a pod as `idle` (ready for session assignment) only after the health check passes:

```go
// The health check is handled by the adapter sidecar in the default
// deployment model. For embedded adapters, implement:
// grpc.health.v1.Health/Check
```

### Declaring Capabilities

When registering your runtime, declare capabilities that inform the platform about what your runtime supports:

```json
{
  "name": "calculator",
  "type": "agent",
  "image": "calc-runtime:dev",
  "capabilities": {
    "midSessionUpload": false,
    "delegation": false,
    "injection": {
      "supported": true
    }
  }
}
```

---

## Summary

In this tutorial you:

1. Learned the stdin/stdout JSON Lines protocol between the adapter and runtime
2. Built a Minimum-tier calculator runtime in Go
3. Handled messages, heartbeats, shutdown, and tool calls
4. Created a Dockerfile for containerized deployment
5. Registered the runtime in Lenny's local dev environment
6. Understood the path to Standard tier (MCP integration)

### Integration Tier Quick Reference

| Tier | What you implement | What you get |
|------|--------------------|--------------|
| **Minimum** | stdin/stdout JSON Lines | Basic message/response, adapter-local tools (read_file, write_file, etc.) |
| **Standard** | + MCP client connection to adapter's local servers | Platform tools (delegation, discovery, elicitation), connector access |
| **Full** | + Lifecycle channel | Clean interrupts, checkpoint/restore, in-place credential rotation |

---

## Next Steps

- [Recursive Delegation](recursive-delegation) -- build a coordinator that delegates to child runtimes
- [Deploy to Kubernetes](deploy-to-cluster) -- deploy your runtime to a real cluster
