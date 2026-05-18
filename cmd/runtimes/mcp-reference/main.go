// SPDX-License-Identifier: MIT

// Command mcp-reference is the reference type: mcp runtime (spec §18.29
// Phase 12b). A type: mcp runtime's agent IS an MCP server: Lenny
// manages the pod lifecycle (isolation, credentials, workspace, pool,
// egress, audit) and the §4.7 adapter drives the agent over MCP rather
// than the §15.4.1 JSONL stdin/stdout protocol used for a type: agent
// runtime (spec §5.1, §9.1).
//
// mcp-reference is the MCP analogue of cmd/runtimes/echo: a minimal,
// self-contained agent the type: mcp adapter path drives end to end. It
// is a plain MCP server with no Lenny-specific code — a type: mcp
// runtime is "oblivious to Lenny" (spec §5.1), so this binary speaks
// only standard MCP and never reads an adapter manifest, presents a
// nonce, or opens a lifecycle channel.
//
// Transport: MCP over stdio. The server reads newline-delimited
// JSON-RPC 2.0 from stdin and writes newline-delimited JSON-RPC to
// stdout, the conventional way an MCP server runs as a hosted
// subprocess. The adapter's MCPRuntime spawns this binary and connects
// to these pipes as an MCP client.
//
// MCP surface:
//
//   - initialize: negotiate the protocol version, advertise the tools
//     capability.
//   - notifications/initialized: accepted, no reply (it is a
//     notification).
//   - tools/list: advertise the `echo` tool.
//   - tools/call (echo): return the `text` argument as a single text
//     content block, prefixed with a per-process call sequence number
//     so multi-call tests can correlate.
//
// stdin EOF: exit cleanly with code 0. Closing the adapter→agent stdin
// pipe is the conventional clean-shutdown signal for a stdio MCP
// server, and is how the adapter's MCPRuntime.Close stops this binary.
//
// Exit codes (matching the other reference runtimes):
//
//	0   success / clean stdin EOF
//	1   runtime error (unexpected internal failure)
//	2   protocol error (malformed inbound JSON-RPC)
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2
)

// protocolVersion is the MCP spec version mcp-reference speaks. It
// matches the adapter's target MCP version (spec §15.4.3).
const protocolVersion = "2025-03-26"

// serverName identifies this MCP server in the initialize handshake.
const serverName = "lenny-mcp-reference"

// JSON-RPC 2.0 error codes.
const (
	errMethodNotFound = -32601
	errInvalidParams  = -32602
)

func main() {
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var pe protocolError
		if errors.As(err, &pe) {
			os.Exit(exitProtocolError)
		}
		os.Exit(exitRuntimeError)
	}
	os.Exit(exitOK)
}

// run drives the MCP server's stdio loop. It is split from main so
// tests can drive it over in-memory pipes without spawning a process.
func run(stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	reader := bufio.NewReader(stdin)
	w := newWriter(stdout)

	// callSeq numbers tools/call invocations so multi-call tests can
	// correlate responses, mirroring the echo runtime's sequence prefix.
	var callSeq atomic.Uint64

	for {
		line, err := readLine(reader)
		if err != nil {
			if err == io.EOF {
				// stdin EOF: the adapter closed the pipe. Clean exit.
				return nil
			}
			return protocolError{msg: fmt.Sprintf("stdin read error: %v", err)}
		}
		if len(line) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return protocolError{msg: fmt.Sprintf("malformed JSON-RPC on stdin: %v", err)}
		}

		resp, respond := dispatch(req, &callSeq, stderr)
		if !respond {
			continue
		}
		if err := w.write(resp); err != nil {
			return fmt.Errorf("write JSON-RPC response: %w", err)
		}
	}
}

// dispatch handles one JSON-RPC request. respond is false for a
// notification (no id), which the MCP spec says draws no reply.
func dispatch(req rpcRequest, callSeq *atomic.Uint64, stderr io.Writer) (rpcResponse, bool) {
	if req.isNotification() {
		// notifications/initialized and any other notification: accepted
		// silently. An MCP server must not reply to a notification.
		return rpcResponse{}, false
	}
	switch req.Method {
	case "initialize":
		return ok(req.ID, initializeResult()), true
	case "tools/list":
		return ok(req.ID, toolsListResult()), true
	case "tools/call":
		return handleToolsCall(req, callSeq), true
	default:
		fmt.Fprintf(stderr, "mcp-reference: unknown method %q\n", req.Method)
		return fail(req.ID, errMethodNotFound, "unknown method "+req.Method), true
	}
}

func initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": serverName, "version": "1"},
	}
}

func toolsListResult() map[string]any {
	return map[string]any{
		"tools": []map[string]any{
			{
				"name":        "echo",
				"description": "Echo the text argument back as a text content block.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "The text to echo back.",
						},
					},
					"required": []string{"text"},
				},
			},
		},
	}
}

// handleToolsCall implements the `echo` tool: it returns the `text`
// argument as a single text content block, prefixed with the per-
// process call sequence number. An MCP tool result is
// `{content: [...], isError}`.
func handleToolsCall(req rpcRequest, callSeq *atomic.Uint64) rpcResponse {
	var call struct {
		Name      string `json:"name"`
		Arguments struct {
			Text string `json:"text"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		return fail(req.ID, errInvalidParams, "malformed tools/call params")
	}
	if call.Name != "echo" {
		return fail(req.ID, errInvalidParams, "unknown tool "+call.Name)
	}

	n := callSeq.Add(1)
	result := map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": fmt.Sprintf("[mcp-reference seq=%d] %s", n, call.Arguments.Text)},
		},
		"isError": false,
	}
	return ok(req.ID, result)
}

// writer serialises newline-delimited JSON writes to stdout. The MCP
// server is single-goroutine, but the writer is structured so a future
// move to a background writer goroutine is safe.
type writer struct {
	w *bufio.Writer
}

func newWriter(w io.Writer) *writer {
	return &writer{w: bufio.NewWriter(w)}
}

// write encodes v as one newline-delimited JSON object and flushes.
// Flushing after every message is required (spec §15.4.1 stdout
// flushing requirement): without it the adapter never reads the
// response and the call hangs.
func (w *writer) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.w.Write(b); err != nil {
		return err
	}
	if err := w.w.WriteByte('\n'); err != nil {
		return err
	}
	return w.w.Flush()
}

// readLine reads one newline-delimited message, returning it without
// the trailing newline. A final line with no newline is returned with
// io.EOF.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if len(line) > 0 && line[len(line)-1] == '\n' {
		return line[:len(line)-1], nil
	}
	return line, err
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// isNotification reports whether a request is a JSON-RPC notification
// (no id). The MCP server must not reply to one.
func (r rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func ok(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func fail(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

// protocolError signals a non-recoverable inbound-format violation.
// main maps it to exit code 2.
type protocolError struct{ msg string }

func (e protocolError) Error() string { return "protocol error: " + e.msg }
