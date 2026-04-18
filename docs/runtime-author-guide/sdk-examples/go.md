---
layout: default
title: "Go Runtime SDK"
parent: "Runtime SDK Examples"
grand_parent: "Runtime Author Guide"
nav_order: 1
---

# Go Runtime SDK

This page presents a complete file summarizer runtime in Go. It implements the Basic integration level, reads workspace files via adapter-local tools, and produces summaries. The full source is ~200 lines including comments.

---

## Complete Source Code

```go
// file-summarizer: A Lenny runtime that reads workspace files and produces summaries.
//
// Integration level: Basic
//   - Reads JSON Lines from stdin
//   - Handles "message" by reading workspace files and summarizing them
//   - Uses adapter-local tools (read_file, list_dir) via tool_call/tool_result
//   - Handles "heartbeat" by responding with "heartbeat_ack"
//   - Handles "shutdown" by exiting cleanly
//   - Ignores unknown message types for forward compatibility
//
// Build:  go build -o file-summarizer .
// Run:    make run LENNY_AGENT_BINARY=./file-summarizer

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

// ---- Message types ----

// InboundMessage is the envelope for all messages received on stdin.
// Unknown fields are silently ignored by encoding/json (forward-compatibility rule).
type InboundMessage struct {
	Type       string       `json:"type"`
	ID         string       `json:"id,omitempty"`
	Input      []OutputPart `json:"input,omitempty"`
	TS         int64        `json:"ts,omitempty"`
	Reason     string       `json:"reason,omitempty"`
	DeadlineMs int          `json:"deadline_ms,omitempty"`

	// tool_result fields
	Content []OutputPart `json:"content,omitempty"`
	IsError bool         `json:"isError,omitempty"`
}

// OutputPart is Lenny's internal content model.
// At the Basic level, only "type" and "inline" are required.
type OutputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline,omitempty"`
}

// ToolCall requests the adapter to execute a tool.
type ToolCall struct {
	Type      string            `json:"type"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments"`
}

// Response signals task completion.
type Response struct {
	Type   string       `json:"type"`
	Output []OutputPart `json:"output"`
}

// HeartbeatAck acknowledges a heartbeat ping.
type HeartbeatAck struct {
	Type string `json:"type"`
}

// ---- State ----

// pendingToolCall tracks an outstanding tool call ID so we can correlate results.
var pendingToolCallID string

// toolCallCounter generates unique tool call IDs.
var toolCallCounter atomic.Int64

// fileContents accumulates file contents as tool_results arrive.
var fileContents []string

// fileList stores the list of files discovered via list_dir.
var fileList []string

// phase tracks the current processing phase:
//   0 = waiting for message
//   1 = listing directory
//   2 = reading files
//   3 = producing summary
var phase int

// currentFileIndex tracks which file we are reading next.
var currentFileIndex int

// ---- Main loop ----

func main() {
	scanner := bufio.NewScanner(os.Stdin)

	// Increase scanner buffer for large messages (default 64KB may be too small).
	const maxLineSize = 1024 * 1024 // 1 MB
	scanner.Buffer(make([]byte, 0, maxLineSize), maxLineSize)

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg InboundMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			fmt.Fprintf(os.Stderr, "file-summarizer: parse error: %v\n", err)
			continue
		}

		switch msg.Type {
		case "message":
			handleMessage(msg)

		case "tool_result":
			handleToolResult(msg)

		case "heartbeat":
			// Respond immediately. Failure to ack within 10 seconds causes SIGTERM.
			writeJSON(HeartbeatAck{Type: "heartbeat_ack"})

		case "shutdown":
			fmt.Fprintf(os.Stderr, "file-summarizer: shutdown (reason=%s)\n", msg.Reason)
			os.Exit(0)

		default:
			// Ignore unknown message types for forward compatibility.
			fmt.Fprintf(os.Stderr, "file-summarizer: ignoring unknown type: %s\n", msg.Type)
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "file-summarizer: stdin error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// handleMessage processes a new task message.
func handleMessage(msg InboundMessage) {
	// Reset state for this task.
	fileContents = nil
	fileList = nil
	currentFileIndex = 0
	phase = 1

	// Extract the user's request text.
	requestText := ""
	if len(msg.Input) > 0 {
		requestText = msg.Input[0].Inline
	}
	fmt.Fprintf(os.Stderr, "file-summarizer: received request: %s\n", requestText)

	// Step 1: List files in the workspace.
	listDir("/workspace/current")
}

// handleToolResult processes the result of a tool call.
func handleToolResult(msg InboundMessage) {
	if msg.ID != pendingToolCallID {
		fmt.Fprintf(os.Stderr, "file-summarizer: unexpected tool_result id=%s\n", msg.ID)
		return
	}

	if msg.IsError {
		// Tool call failed. Produce an error response.
		errorText := "unknown error"
		if len(msg.Content) > 0 {
			errorText = msg.Content[0].Inline
		}
		fmt.Fprintf(os.Stderr, "file-summarizer: tool error: %s\n", errorText)
		writeResponse(fmt.Sprintf("Error reading workspace: %s", errorText))
		return
	}

	switch phase {
	case 1:
		// Phase 1: We received the directory listing.
		if len(msg.Content) > 0 {
			// Parse the file list (one file per line).
			listing := msg.Content[0].Inline
			for _, line := range strings.Split(listing, "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, ".") {
					fileList = append(fileList, line)
				}
			}
		}

		if len(fileList) == 0 {
			writeResponse("No files found in the workspace.")
			return
		}

		// Step 2: Start reading files one by one.
		phase = 2
		currentFileIndex = 0
		readNextFile()

	case 2:
		// Phase 2: We received a file's contents.
		if len(msg.Content) > 0 {
			fileName := fileList[currentFileIndex]
			content := msg.Content[0].Inline
			fileContents = append(fileContents,
				fmt.Sprintf("=== %s ===\n%s", fileName, truncate(content, 500)))
		}

		currentFileIndex++
		if currentFileIndex < len(fileList) && currentFileIndex < 10 {
			// Read the next file (cap at 10 files to avoid excessive reads).
			readNextFile()
		} else {
			// All files read. Produce the summary.
			phase = 3
			produceSummary()
		}
	}
}

// listDir sends a list_dir tool call for the given path.
func listDir(path string) {
	id := nextToolCallID()
	pendingToolCallID = id
	writeJSON(ToolCall{
		Type:      "tool_call",
		ID:        id,
		Name:      "list_dir",
		Arguments: map[string]string{"path": path},
	})
}

// readNextFile sends a read_file tool call for the next file in the list.
func readNextFile() {
	if currentFileIndex >= len(fileList) {
		return
	}
	id := nextToolCallID()
	pendingToolCallID = id
	filePath := "/workspace/current/" + fileList[currentFileIndex]
	writeJSON(ToolCall{
		Type:      "tool_call",
		ID:        id,
		Name:      "read_file",
		Arguments: map[string]string{"path": filePath},
	})
}

// produceSummary generates the final summary response.
func produceSummary() {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Workspace Summary (%d files)\n\n", len(fileContents)))
	for _, fc := range fileContents {
		sb.WriteString(fc)
		sb.WriteString("\n\n")
	}
	sb.WriteString(fmt.Sprintf("Total files examined: %d", len(fileContents)))
	writeResponse(sb.String())
}

// ---- Helpers ----

// writeResponse sends a response message to stdout.
func writeResponse(text string) {
	resp := Response{
		Type: "response",
		Output: []OutputPart{
			{Type: "text", Inline: text},
		},
	}
	writeJSON(resp)
	phase = 0
}

// writeJSON marshals v to JSON and writes it as a single line to stdout.
// os.Stdout in Go is unbuffered by default, so no explicit flush is needed.
func writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "file-summarizer: marshal error: %v\n", err)
		return
	}
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}

// nextToolCallID generates a unique tool call ID.
func nextToolCallID() string {
	n := toolCallCounter.Add(1)
	return fmt.Sprintf("tc_%03d", n)
}

// truncate returns the first n characters of s, adding "..." if truncated.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
```

---

## How It Works

### Message Flow

```
1. Adapter sends:    {"type":"message","id":"msg_001","input":[{"type":"text","inline":"Summarize files"}]}
2. Runtime sends:    {"type":"tool_call","id":"tc_001","name":"list_dir","arguments":{"path":"/workspace/current"}}
3. Adapter sends:    {"type":"tool_result","id":"tc_001","content":[{"type":"text","inline":"main.go\nutil.go"}]}
4. Runtime sends:    {"type":"tool_call","id":"tc_002","name":"read_file","arguments":{"path":"/workspace/current/main.go"}}
5. Adapter sends:    {"type":"tool_result","id":"tc_002","content":[{"type":"text","inline":"package main..."}]}
6. Runtime sends:    {"type":"tool_call","id":"tc_003","name":"read_file","arguments":{"path":"/workspace/current/util.go"}}
7. Adapter sends:    {"type":"tool_result","id":"tc_003","content":[{"type":"text","inline":"package util..."}]}
8. Runtime sends:    {"type":"response","output":[{"type":"text","inline":"Workspace Summary (2 files)..."}]}
```

### Key Design Choices

- **Sequential file reading:** Files are read one at a time via `tool_call`/`tool_result` to keep the state machine simple. A production runtime could use multiple outstanding tool calls for parallel reads.
- **Atomic tool call IDs:** The `toolCallCounter` uses `atomic.Int64` for thread-safe ID generation, even though the main loop is single-threaded (future-proofing).
- **Truncation:** File contents are truncated to 500 characters per file. A real summarizer would pass the full content to an LLM.
- **Heartbeat interleaving:** Heartbeats may arrive between tool calls. The main loop handles them regardless of the current `phase`.

---

## go.mod

```
module github.com/lenny-dev/lenny/examples/runtimes/file-summarizer-go

go 1.22
```

No external dependencies. The file summarizer uses only the Go standard library.

---

## Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o file-summarizer .

FROM scratch
COPY --from=builder /build/file-summarizer /file-summarizer
ENTRYPOINT ["/file-summarizer"]
```

Multi-stage build produces a ~3MB static binary.

---

## Build and Run

```bash
# Build
cd examples/runtimes/file-summarizer-go
go build -o file-summarizer .

# Run locally with `make run` (in-process, no Docker)
make run LENNY_AGENT_BINARY=examples/runtimes/file-summarizer-go/file-summarizer

# Run locally with `docker compose up`
docker build -t file-summarizer:dev -f examples/runtimes/file-summarizer-go/Dockerfile .
docker compose up
```

---

## Register the Runtime

```bash
# Register via admin API
curl -X POST http://localhost:8080/v1/admin/runtimes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "file-summarizer",
    "type": "agent",
    "image": "file-summarizer:dev",
    "description": "Summarizes workspace files"
  }'

# Create a session
curl -X POST http://localhost:8080/v1/sessions \
  -H "Content-Type: application/json" \
  -d '{"runtimeName": "file-summarizer", "tenantId": "default"}'
```

---

## Upgrading to Standard level

To add MCP tools (delegation, output streaming, memory), upgrade to the Standard integration level:

### 1. Add MCP Client Dependency

```
// go.mod
require github.com/mark3labs/mcp-go v0.x.x
```

### 2. Read the Adapter Manifest

```go
import "encoding/json"
import "os"

type AdapterManifest struct {
    SessionID          string `json:"sessionId"`
    TaskID             string `json:"taskId"`
    PlatformMcpServer  struct {
        Socket string `json:"socket"`
    } `json:"platformMcpServer"`
    McpNonce string `json:"mcpNonce"`
}

func readManifest() (*AdapterManifest, error) {
    data, err := os.ReadFile("/run/lenny/adapter-manifest.json")
    if err != nil {
        return nil, err
    }
    var m AdapterManifest
    err = json.Unmarshal(data, &m)
    return &m, err
}
```

### 3. Connect to Lenny's local tool server

```go
import "github.com/mark3labs/mcp-go/client"

func connectMCP(manifest *AdapterManifest) (*client.Client, error) {
    c, err := client.NewUnixSocketClient(manifest.PlatformMcpServer.Socket)
    if err != nil {
        return nil, err
    }

    // Initialize with nonce
    err = c.Initialize(client.InitOptions{
        Nonce:           manifest.McpNonce,
        ClientName:      "file-summarizer",
        ClientVersion:   "1.0.0",
        ProtocolVersion: "2025-03-26",
    })
    return c, err
}
```

### 4. Use Platform Tools

```go
// Emit incremental output
func emitOutput(c *client.Client, text string) error {
    return c.CallTool("lenny/output", map[string]interface{}{
        "output": []map[string]string{
            {"type": "text", "inline": text},
        },
    })
}

// Delegate a subtask
func delegateReview(c *client.Client, code string) (string, error) {
    result, err := c.CallTool("lenny/delegate_task", map[string]interface{}{
        "target": "code-reviewer",
        "task": map[string]interface{}{
            "input": []map[string]string{
                {"type": "text", "inline": "Review this code:\n" + code},
            },
        },
    })
    return result.SessionId, err
}
```

### 5. macOS Note

The Standard level requires abstract Unix sockets, which are Linux-only. Use `docker compose up` on macOS.
