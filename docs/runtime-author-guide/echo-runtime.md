---
layout: default
title: "Echo Runtime Sample"
parent: "Runtime Author Guide"
nav_order: 3
---

# Echo Runtime Sample

This page presents a complete, runnable echo runtime in Go and walks through it line by line. It is for a runtime author who wants to see the Basic level of the Lenny adapter contract in working code before reaching for the SDK.

At the Basic level, the runtime binary speaks newline-delimited JSON over stdin and stdout and uses no platform MCP tools. The echo runtime reads each message from stdin, echoes its text back with a sequence number, acknowledges heartbeats, and exits cleanly on shutdown. Copy it as the starting point for your own runtime, then replace the `message` handler with your logic.

The sections below follow the source in order: a line-by-line walkthrough of each handler, how the adapter wraps the binary on stdin and stdout, the commands to build and run it locally, and the changes that turn the sample into a working runtime. For the message contract in full, see the [Adapter Contract](../reference/adapter-contract.md).

---

## Complete Source Code

```go
// echo-runtime: A minimal Lenny agent runtime that echoes messages.
//
// This implements the Basic level of the Lenny adapter contract:
//   - Reads JSON Lines from stdin
//   - Handles "message" by echoing input with a sequence number
//   - Handles "heartbeat" by responding with "heartbeat_ack"
//   - Handles "shutdown" by exiting cleanly
//   - Ignores unknown message types for forward compatibility
//
// Build:  go build -o echo-runtime .
// Run:    make run LENNY_AGENT_BINARY=./echo-runtime

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// ---- Inbound message types (adapter -> runtime via stdin) ----

// InboundMessage is the envelope for all messages received on stdin.
// We only parse the fields we need; unknown fields are silently ignored
// by encoding/json (this is the forward-compatibility rule).
type InboundMessage struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Input []OutputPart    `json:"input,omitempty"`
	TS    int64           `json:"ts,omitempty"`         // heartbeat timestamp
	Reason string         `json:"reason,omitempty"`     // shutdown reason
	DeadlineMs int        `json:"deadline_ms,omitempty"` // shutdown deadline

	// tool_result fields
	Content []OutputPart  `json:"content,omitempty"`
	IsError bool          `json:"isError,omitempty"`
}

// OutputPart is the Lenny internal content model.
// At the Basic level, only "type" and "inline" are required.
type OutputPart struct {
	Type   string `json:"type"`
	Inline string `json:"inline,omitempty"`
}

// ---- Outbound message types (runtime -> adapter via stdout) ----

// Response is the primary output message, signaling task completion.
type Response struct {
	Type   string       `json:"type"`
	Output []OutputPart `json:"output"`
}

// HeartbeatAck acknowledges a heartbeat ping.
type HeartbeatAck struct {
	Type string `json:"type"`
}

// ---- Main loop ----

func main() {
	scanner := bufio.NewScanner(os.Stdin)

	// Increase the scanner buffer for large messages (default 64KB may be too small).
	const maxLineSize = 1024 * 1024 // 1 MB
	scanner.Buffer(make([]byte, 0, maxLineSize), maxLineSize)

	seq := 0

	for scanner.Scan() {
		line := scanner.Bytes()

		// Parse the inbound message.
		var msg InboundMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			// Log parse errors to stderr (captured by adapter for diagnostics).
			fmt.Fprintf(os.Stderr, "echo-runtime: failed to parse message: %v\n", err)
			continue
		}

		switch msg.Type {
		case "message":
			// Extract the first text part from input (if any).
			inputText := ""
			if len(msg.Input) > 0 {
				inputText = msg.Input[0].Inline
			}

			// Increment the sequence counter and echo the input.
			seq++
			resp := Response{
				Type: "response",
				Output: []OutputPart{
					{
						Type:   "text",
						Inline: fmt.Sprintf("echo [seq=%d]: %s", seq, inputText),
					},
				},
			}
			writeJSON(resp)

		case "tool_result":
			// At the Basic level, tool_result arrives after a tool_call we emitted.
			// The echo runtime does not emit tool_calls, so this is a no-op.
			// A real runtime would correlate by msg.ID and process the result.
			fmt.Fprintf(os.Stderr, "echo-runtime: received tool_result id=%s (ignored)\n", msg.ID)

		case "heartbeat":
			// Respond immediately. Failure to ack within 10 seconds causes SIGTERM.
			writeJSON(HeartbeatAck{Type: "heartbeat_ack"})

		case "shutdown":
			// Exit cleanly. No acknowledgment message is needed.
			fmt.Fprintf(os.Stderr, "echo-runtime: shutdown received (reason=%s), exiting\n", msg.Reason)
			os.Exit(0)

		default:
			// Ignore unknown message types for forward compatibility.
			// New message types may be added in future adapter versions.
			fmt.Fprintf(os.Stderr, "echo-runtime: ignoring unknown type: %s\n", msg.Type)
		}
	}

	// stdin closed (adapter terminated the pipe). Exit cleanly.
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "echo-runtime: stdin read error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// writeJSON marshals a value to JSON, writes it as a single line to stdout,
// and flushes. os.Stdout in Go is unbuffered by default, so no explicit
// flush call is needed. If you use bufio.NewWriter, you MUST call Flush().
func writeJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "echo-runtime: failed to marshal response: %v\n", err)
		return
	}
	// Write the JSON line followed by newline.
	// os.Stdout.Write is unbuffered in Go --- no flush needed.
	os.Stdout.Write(data)
	os.Stdout.Write([]byte("\n"))
}
```

---

## Line-by-Line Walkthrough

### Message Parsing

```go
type InboundMessage struct {
    Type  string          `json:"type"`
    ID    string          `json:"id,omitempty"`
    Input []OutputPart    `json:"input,omitempty"`
    // ...
}
```

We define a single struct that can hold fields from any inbound message type. Go's `encoding/json` silently ignores unknown fields, which satisfies the forward-compatibility rule: your runtime MUST ignore fields it does not recognize. We dispatch on `msg.Type` to determine which fields are relevant.

### The Main Loop

```go
scanner := bufio.NewScanner(os.Stdin)
for scanner.Scan() {
    line := scanner.Bytes()
    var msg InboundMessage
    json.Unmarshal(line, &msg)
    switch msg.Type {
    // ...
    }
}
```

The loop reads one line at a time from stdin. Each line is a complete JSON object (JSON Lines format). The scanner blocks until a line is available. When the adapter closes stdin (pod termination), `scanner.Scan()` returns `false` and the loop exits.

### Handling `message`

```go
case "message":
    inputText := msg.Input[0].Inline
    seq++
    resp := Response{
        Type: "response",
        Output: []OutputPart{{Type: "text", Inline: fmt.Sprintf("echo [seq=%d]: %s", seq, inputText)}},
    }
    writeJSON(resp)
```

We extract the text from the first `OutputPart` in the `input` array. We write a `response` message to stdout. The `response` signals task completion --- the adapter forwards the output to the gateway and the gateway delivers it to the client.

### Handling `heartbeat`

```go
case "heartbeat":
    writeJSON(HeartbeatAck{Type: "heartbeat_ack"})
```

The adapter sends periodic heartbeats to check liveness. You MUST respond within 10 seconds or the adapter sends SIGTERM. The heartbeat handler should be immediate --- do not do any heavy work here.

### Handling `shutdown`

```go
case "shutdown":
    os.Exit(0)
```

The adapter sends `shutdown` when the pod is being drained, the session has completed, or the budget is exhausted. Exit cleanly within `deadline_ms`. For the echo runtime, there is no cleanup needed, so we exit immediately.

### Writing to stdout

```go
func writeJSON(v interface{}) {
    data, _ := json.Marshal(v)
    os.Stdout.Write(data)
    os.Stdout.Write([]byte("\n"))
}
```

In Go, `os.Stdout.Write` is unbuffered --- each call goes directly to the file descriptor. No explicit flush is needed. If you use `bufio.NewWriter(os.Stdout)`, you MUST call `Flush()` after every message.

---

## How the Adapter Wraps It

When the pod starts, the adapter:

1. Opens a gRPC connection to the gateway (mTLS).
2. Writes the adapter manifest to `/run/lenny/adapter-manifest.json`.
3. Signals readiness to the gateway (pod enters the warm pool).
4. Waits for session assignment.
5. Receives workspace files from the gateway and materializes them to `/workspace/current/`.
6. Spawns your binary with stdin/stdout pipes connected.
7. Delivers the first `message` on stdin.
8. Relays your `response` from stdout to the gateway.
9. Sends periodic `heartbeat` messages.
10. On session end, sends `shutdown` and waits for your binary to exit.

Your binary does not handle any of these steps. It reads from stdin and writes to stdout, and the adapter does the rest.

---

## How to Build and Run

Lenny runs locally in several modes. The recommended path is `lenny up`, which runs the echo runtime in a real Kubernetes pod against the production code path. The `make run` and `docker compose up` modes give a tighter loop in specific cases. The [Local Development](local-development.md) page covers each mode in full.

### Build

```bash
# From the echo runtime directory
cd examples/runtimes/echo
go build -o echo-runtime .
```

### Run with `lenny up` (recommended, end-to-end in a pod)

```bash
# Build the container image and register the runtime against a running gateway
docker build -t echo-runtime:dev -f examples/runtimes/echo/Dockerfile .
lenny up
lenny runtime publish echo --image echo-runtime:dev
lenny session new --runtime echo --message "Hello"
```

`lenny up` starts the whole platform from one binary, including an embedded Kubernetes, and runs your runtime in an actual pod. Use it to exercise the same code path production uses and to run end-to-end demos.

### Run with `make run` (host process, no dependencies)

```bash
# From the project root
make run LENNY_AGENT_BINARY=examples/runtimes/echo/echo-runtime
```

This starts the gateway, an in-process controller, and your binary as goroutines in a single process, with no Postgres, Redis, MinIO, or Docker needed. The binary runs as a host process rather than in a pod, which rebuilds fast. A Basic-level runtime such as the echo runtime works here on any host. Use `make run` for a fast host-process loop while iterating on the binary.

### Run with `docker compose up` (production-like stack)

```bash
# Build a container image
docker build -t echo-runtime:dev -f examples/runtimes/echo/Dockerfile .

# Start the full stack
LENNY_AGENT_RUNTIME=echo docker compose up
```

This runs the gateway, a controller, and your agent container alongside Postgres, Redis, and MinIO as containers. Use it to test against real storage backends or to run integration tests in CI.

### Run the smoke test

The smoke test creates a session, sends a prompt, checks the response, and exits.

```bash
# With `make run`
make test-smoke

# With `docker compose up`
docker compose run smoke-test
```

---

## How to Modify It for Your Own Runtime

1. **Replace the `message` handler** with your actual logic. Read workspace files, call your LLM, produce output.
2. **Add tool calls** if you need to read/write files during processing:

```go
case "message":
    // Read a file from the workspace
    readCall := ToolCall{
        Type:      "tool_call",
        ID:        "tc_001",
        Name:      "read_file",
        Arguments: map[string]string{"path": "/workspace/current/input.txt"},
    }
    writeJSON(readCall)

    // Continue reading stdin for the tool_result
    // (heartbeats may arrive before the result)
```

3. **Handle `tool_result`** to process the results of your tool calls:

```go
case "tool_result":
    if msg.ID == "tc_001" {
        fileContent := msg.Content[0].Inline
        // Process the file content...
    }
```

4. **Add error handling** for malformed input, missing files, and LLM failures. Use the `error` field on the `response` message to report structured errors.

5. **Upgrade to the Standard level** when you need delegation, connectors, or platform tools. See the [Integration Levels](integration-levels.md) page for the migration path.

---

## go.mod

```
module github.com/lennylabs/lenny/examples/runtimes/echo

go 1.22
```

No external dependencies. The echo runtime uses only the Go standard library.

---

## Dockerfile

```dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o echo-runtime .

FROM scratch
COPY --from=builder /build/echo-runtime /echo-runtime
ENTRYPOINT ["/echo-runtime"]
```

Multi-stage build produces a ~2MB static binary with no OS dependencies. The `scratch` base image contains no OS files.
