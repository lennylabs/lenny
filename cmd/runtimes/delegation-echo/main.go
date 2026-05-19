// SPDX-License-Identifier: MIT

// Command delegation-echo is the Standard-level reference adapter
// binary. It inherits the Basic-level stdin/stdout protocol from
// cmd/runtimes/echo (message → response, heartbeat → heartbeat_ack,
// shutdown → clean exit) and adds the Standard-level intra-pod MCP
// integration from spec §15.4.3:
//
//   - Reads the adapter manifest (`/run/lenny/adapter-manifest.json` by
//     default, or the path in $LENNY_ADAPTER_MANIFEST) to discover the
//     platform MCP server socket, the connector MCP server sockets, and
//     the §15.4.3 intra-pod MCP nonce (`mcpNonce`).
//   - Connects to the platform MCP server as a standard MCP client and
//     presents the nonce as the top-level `params._lennyNonce` field of
//     the MCP `initialize` request.
//   - Connects to every connector MCP server listed in the manifest with
//     the same nonce.
//
// delegation-echo exercises the §8.5 delegation tool surface end to end.
// On each inbound `message` it:
//
//  1. Calls `lenny/delegate_task` on the platform MCP server to spawn a
//     child sub-task whose input is the inbound message's input parts.
//  2. Calls `lenny/await_children` to wait for the child to settle and
//     collect its `TaskResult` (§8.8).
//  3. Calls `lenny/request_input` to confirm the echoed result before
//     finalizing (exercising the platform input-blocking tool).
//  4. Calls `lenny/output` to emit the child's result parts to the
//     parent/client.
//
// It then writes the §15.4.1 stdout `response` echoing the child's
// `TaskResult` output parts so the Basic-level `message`/`response`
// round-trip contract is also satisfied.
//
// The intra-pod MCP transport is an abstract Unix socket on Linux
// (names beginning with `@`, per §15.4.3) and a file-based Unix socket
// on macOS, where abstract sockets are unsupported; the socket path is
// taken verbatim from the manifest so the conformance harness can drive
// the runtime cross-platform.
//
// delegation-echo serves three purposes:
//   - Reference implementation for runtime authors writing their own
//     Standard-level adapters.
//   - Target the Phase 9 contract and conformance tests exec.
//   - Binary the lenny-compliance --level standard battery exercises.
//
// A missing manifest, or a manifest without a platform MCP server, is
// not fatal: delegation-echo degrades to the Basic-level echo behaviour
// (it echoes the inbound input parts directly) so it can still be
// exercised in Basic-only test paths.
//
// Exit codes (spec §15.4): identical to echo — 0 success, 1 runtime
// error, 2 protocol error.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/runtimekit"
)

const (
	exitOK            = 0
	exitRuntimeError  = 1
	exitProtocolError = 2

	defaultManifestPath = "/run/lenny/adapter-manifest.json"

	// mcpProtocolVersion is the §15.4.3 intra-pod MCP spec version the
	// adapter's local MCP servers speak.
	mcpProtocolVersion = "2025-03-26"

	// nonceParamKey is the §15.4.3 canonical injection key for the
	// intra-pod MCP nonce: the top-level field of the MCP initialize
	// request's params object.
	nonceParamKey = "_lennyNonce"
)

func main() {
	// §4.7: resolve the §15.4.1 transport. LENNY_ADAPTER_SOCKET selects
	// the sidecar-pod abstract socket; its absence selects stdin/stdout.
	// The intra-pod MCP connections are separate Unix sockets resolved
	// from the adapter manifest, unaffected by this choice.
	transport, err := runtimekit.Open(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitRuntimeError)
	}
	defer transport.Close()

	if err := run(transport.Reader, transport.Writer, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var pe protocolError
		if errors.As(err, &pe) {
			os.Exit(exitProtocolError)
		}
		os.Exit(exitRuntimeError)
	}
	os.Exit(exitOK)
}

func run(stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	stdoutWriter := newWriter(stdout)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Standard-level startup: read the adapter manifest and connect to
	// the platform MCP server (and any connector MCP servers) as an MCP
	// client. A missing manifest, or one without a platform MCP server,
	// degrades to Basic-level echo behaviour — delegation-echo is still
	// exercised in the Basic-only test paths without a manifest.
	manifest, manifestErr := loadManifest(os.Getenv("LENNY_ADAPTER_MANIFEST"))
	var platform *mcpClient
	if manifestErr != nil {
		fmt.Fprintf(stderr, "delegation-echo: no adapter manifest (%v); MCP integration disabled\n", manifestErr)
	} else if manifest.PlatformMcpServer == nil || manifest.PlatformMcpServer.Socket == "" {
		fmt.Fprintln(stderr, "delegation-echo: manifest has no platform MCP server; MCP integration disabled")
	} else {
		c, err := connectMCP(ctx, manifest.PlatformMcpServer.Socket, manifest.MCPNonce, "delegation-echo")
		if err != nil {
			return fmt.Errorf("connect platform MCP server: %w", err)
		}
		defer c.close()
		platform = c
		// §15.4.3 / §15.4.6 connector reachability: connect to every
		// connector MCP server with the same nonce and complete the
		// initialize handshake. Connector tools are discovered but not
		// invoked by delegation-echo.
		for _, conn := range manifest.ConnectorServers {
			cc, err := connectMCP(ctx, conn.Socket, manifest.MCPNonce, "delegation-echo")
			if err != nil {
				return fmt.Errorf("connect connector MCP server %q: %w", conn.ID, err)
			}
			defer cc.close()
		}
	}

	scanner := bufio.NewScanner(stdin)
	const maxBytes = 50 * 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxBytes)

	var seq atomic.Uint64
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			return protocolError{msg: fmt.Sprintf("malformed JSONL on stdin: %v", err)}
		}
		switch env.Type {
		case "message":
			if err := handleMessage(stdoutWriter, line, &seq, platform, stderr); err != nil {
				return err
			}
		case "heartbeat":
			if err := stdoutWriter.write(heartbeatAck{Type: "heartbeat_ack"}); err != nil {
				return fmt.Errorf("write heartbeat_ack: %w", err)
			}
		case "shutdown":
			return handleShutdown(stdoutWriter, line, cancel)
		default:
			fmt.Fprintf(stderr, "delegation-echo: ignoring unknown message type %q\n", env.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return protocolError{msg: fmt.Sprintf("stdin read error: %v", err)}
	}
	return nil
}

// adapterManifest is the subset of /run/lenny/adapter-manifest.json this
// runtime reads. Unknown fields on the file are ignored (§4.7 forward
// compatibility).
type adapterManifest struct {
	Version           int    `json:"version"`
	MCPNonce          string `json:"mcpNonce"`
	PlatformMcpServer *struct {
		Socket string `json:"socket"`
	} `json:"platformMcpServer"`
	ConnectorServers []struct {
		ID     string `json:"id"`
		Socket string `json:"socket"`
	} `json:"connectorServers"`
}

func loadManifest(path string) (adapterManifest, error) {
	if path == "" {
		path = defaultManifestPath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return adapterManifest{}, err
	}
	var m adapterManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return adapterManifest{}, fmt.Errorf("manifest %s: %w", path, err)
	}
	// §4.7 forward-compatibility rule: reject a manifest version higher
	// than the highest this runtime understands. Every increment is
	// breaking.
	if m.Version > 1 {
		return adapterManifest{}, fmt.Errorf("manifest %s: version %d is newer than supported (1)", path, m.Version)
	}
	return m, nil
}

// handleMessage processes one inbound `message`. At Standard level, with
// a platform MCP client connected, it delegates a sub-task via
// `lenny/delegate_task`, awaits the child via `lenny/await_children`,
// confirms via `lenny/request_input`, emits the child's parts via
// `lenny/output`, and writes the stdout `response` echoing the child's
// result. Without a platform MCP client it degrades to the Basic-level
// echo: it echoes the inbound input parts directly.
func handleMessage(w *writer, line []byte, seq *atomic.Uint64, platform *mcpClient, stderr io.Writer) error {
	var inbound struct {
		Type  string       `json:"type"`
		ID    string       `json:"id"`
		Input []outputPart `json:"input"`
	}
	if err := json.Unmarshal(line, &inbound); err != nil {
		return protocolError{msg: fmt.Sprintf("malformed message envelope: %v", err)}
	}
	n := seq.Add(1)

	if platform == nil {
		// Basic-level fallback: no platform MCP server, so echo the
		// inbound input parts directly (the §15.4.1 message/response
		// round-trip contract).
		return w.write(response{Type: "response", Output: echoParts(inbound.Input, n)})
	}

	out, err := delegateAndEcho(platform, inbound.ID, inbound.Input, n)
	if err != nil {
		fmt.Fprintf(stderr, "delegation-echo: delegation flow failed: %v\n", err)
		// §15.4.1 structured error reporting: emit a `response` carrying
		// the failure so the adapter maps the task to `failed` without
		// losing the error context.
		return w.write(response{
			Type:   "response",
			Output: []outputPart{},
			Error:  &responseError{Code: "DELEGATION_FAILED", Message: err.Error()},
		})
	}
	return w.write(response{Type: "response", Output: out})
}

// delegateAndEcho runs the §8.5 delegation flow against the platform MCP
// server: delegate a sub-task, await the child, confirm via
// request_input, emit via lenny/output, and return the child's output
// parts for the stdout `response`.
func delegateAndEcho(platform *mcpClient, messageID string, input []outputPart, seq uint64) ([]outputPart, error) {
	// 1. lenny/delegate_task — spawn a child whose input is this
	//    message's input parts. The target id is opaque (§8.2).
	handleRaw, err := platform.callTool("lenny/delegate_task", map[string]any{
		"target": "delegation-echo-child",
		"task": map[string]any{
			"input": echoParts(input, seq),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("lenny/delegate_task: %w", err)
	}
	var handle taskHandle
	if err := json.Unmarshal(handleRaw, &handle); err != nil {
		return nil, fmt.Errorf("decode TaskHandle: %w", err)
	}
	if handle.TaskID == "" {
		return nil, errors.New("lenny/delegate_task returned an empty taskId")
	}

	// 2. lenny/await_children — wait for the child to settle. mode=all
	//    waits until every named child reaches a terminal state (§8.5).
	awaitRaw, err := platform.callTool("lenny/await_children", map[string]any{
		"child_ids": []string{handle.TaskID},
		"mode":      "all",
	})
	if err != nil {
		return nil, fmt.Errorf("lenny/await_children: %w", err)
	}
	results, err := decodeTaskResults(awaitRaw)
	if err != nil {
		return nil, fmt.Errorf("decode await_children result: %w", err)
	}
	if len(results) == 0 {
		return nil, errors.New("lenny/await_children returned no TaskResult")
	}
	child := results[0]
	if child.State != "completed" {
		code := "CHILD_FAILED"
		if child.Error != nil && child.Error.Code != "" {
			code = child.Error.Code
		}
		return nil, fmt.Errorf("child %s settled %s (%s)", child.TaskID, child.State, code)
	}
	childParts := child.Output.Parts

	// 3. lenny/request_input — confirm the echoed result before
	//    finalizing. The platform tool blocks until an answer arrives
	//    (§8.5, replaces the stdout `input_required` message).
	if _, err := platform.callTool("lenny/request_input", map[string]any{
		"parts": []outputPart{{
			SchemaVersion: 1,
			Type:          "text",
			Inline:        fmt.Sprintf("delegation-echo: confirm echo of child %s?", child.TaskID),
		}},
	}); err != nil {
		return nil, fmt.Errorf("lenny/request_input: %w", err)
	}

	// 4. lenny/output — emit the child's result parts to the
	//    parent/client (§8.5). The stdout `response` below still signals
	//    task completion; per §15.4.1 its `output` array carries the
	//    same parts so a consumer reading either path sees the echo.
	if _, err := platform.callTool("lenny/output", map[string]any{
		"output": childParts,
	}); err != nil {
		return nil, fmt.Errorf("lenny/output: %w", err)
	}
	return childParts, nil
}

// taskHandle is the §8.2 `lenny/delegate_task` return value. The spec
// names the return type `TaskHandle`; delegation-echo reads only the
// child task identifier from it.
type taskHandle struct {
	TaskID string `json:"taskId"`
}

// taskResult mirrors the §8.8 TaskResult schema returned by
// `lenny/await_children`, restricted to the fields delegation-echo
// reads.
type taskResult struct {
	SchemaVersion int    `json:"schemaVersion"`
	TaskID        string `json:"taskId"`
	State         string `json:"state"`
	Output        struct {
		Parts []outputPart `json:"parts"`
	} `json:"output"`
	Error *responseError `json:"error"`
}

// decodeTaskResults decodes the `lenny/await_children` result. The tool
// returns a list of TaskResult for mode `all`/`settled` and a single
// TaskResult for mode `any` (§8.8); delegation-echo accepts either
// encoding and normalizes to a slice.
func decodeTaskResults(raw json.RawMessage) ([]taskResult, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var list []taskResult
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		return list, nil
	}
	var single taskResult
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	return []taskResult{single}, nil
}

// echoParts stamps schemaVersion on every part and prefixes text parts
// with the per-session sequence number so multi-message tests can
// correlate. §15.4.1 producer obligation: schemaVersion MUST be set on
// every emitted OutputPart.
func echoParts(in []outputPart, seq uint64) []outputPart {
	out := make([]outputPart, 0, len(in))
	for _, p := range in {
		if p.SchemaVersion == 0 {
			p.SchemaVersion = 1
		}
		if p.Type == "text" && p.Inline != "" {
			out = append(out, outputPart{
				SchemaVersion: 1,
				Type:          "text",
				Inline:        fmt.Sprintf("[delegation-echo seq=%d] %s", seq, p.Inline),
				ID:            p.ID,
				MimeTyp:       p.MimeTyp,
			})
		} else {
			out = append(out, p)
		}
	}
	return out
}

func handleShutdown(_ *writer, line []byte, cancel context.CancelFunc) error {
	var sd struct {
		Type       string `json:"type"`
		Reason     string `json:"reason"`
		DeadlineMS int    `json:"deadline_ms"`
	}
	if err := json.Unmarshal(line, &sd); err != nil {
		return protocolError{msg: fmt.Sprintf("malformed shutdown envelope: %v", err)}
	}
	cancel()
	d := time.Duration(sd.DeadlineMS) * time.Millisecond
	if d > 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	if d > 0 {
		time.Sleep(d)
	}
	return nil
}

// --- intra-pod MCP client ----------------------------------------------

// mcpClient is a minimal §15.4.3 intra-pod MCP client over a single Unix
// socket connection. It speaks JSON-RPC 2.0: an `initialize` request
// carrying the manifest nonce, then `tools/list` and `tools/call`.
type mcpClient struct {
	conn net.Conn
	dec  *json.Decoder
	enc  *json.Encoder
	mu   sync.Mutex
	id   atomic.Int64
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// connectMCP dials the intra-pod MCP socket, completes the
// nonce-authenticated `initialize` handshake (§15.4.3), and discovers
// the tool set via `tools/list`. The nonce is presented as the
// top-level `params._lennyNonce` field of the `initialize` request.
func connectMCP(ctx context.Context, socket, nonce, clientName string) (*mcpClient, error) {
	conn, err := dialMCPSocket(ctx, socket)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", socket, err)
	}
	c := &mcpClient{
		conn: conn,
		dec:  json.NewDecoder(conn),
		enc:  json.NewEncoder(conn),
	}
	c.enc.SetEscapeHTML(false)

	// §15.4.3 nonce handshake. The adapter validates _lennyNonce against
	// the manifest's mcpNonce before dispatching any tool call, and
	// strips the field before forwarding initialize to its MCP server.
	if _, err := c.call("initialize", map[string]any{
		nonceParamKey:     nonce,
		"protocolVersion": mcpProtocolVersion,
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": "1.0.0",
		},
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// §15.4.3 tool discovery: list the tools the server exposes. The
	// result is not inspected — discovery confirms the post-handshake
	// channel is live before any tools/call.
	if _, err := c.call("tools/list", map[string]any{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return c, nil
}

// dialMCPSocket dials an intra-pod MCP Unix socket. A name beginning
// with `@` is a Linux abstract socket; net.Dial expects a leading NUL
// for abstract addresses off Linux, so the `@` is translated. On Linux
// the @-prefixed name is passed through and the kernel resolves it.
func dialMCPSocket(ctx context.Context, socket string) (net.Conn, error) {
	addr := socket
	if strings.HasPrefix(socket, "@") {
		addr = "\x00" + socket[1:]
	}
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "unix", addr)
	if err == nil {
		return conn, nil
	}
	// One bounded retry — the conformance harness may not have its
	// listener up at the instant the runtime starts.
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return d.DialContext(ctx, "unix", addr)
}

// callTool invokes one MCP tool via `tools/call` and returns the raw
// `result` JSON. The fake adapter MCP server (and the production
// adapter) return the tool's value directly as the JSON-RPC result.
func (c *mcpClient) callTool(name string, arguments any) (json.RawMessage, error) {
	return c.call("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
}

// call sends one JSON-RPC request and reads the matching response. The
// connection is serialized by mu: delegation-echo issues calls
// sequentially, so a single in-flight request at a time is sufficient.
func (c *mcpClient) call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.id.Add(1)
	if err := c.enc.Encode(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}); err != nil {
		return nil, fmt.Errorf("write %s request: %w", method, err)
	}
	var resp rpcResponse
	if err := c.dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("read %s response: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: rpc error %d: %s", method, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

func (c *mcpClient) close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// --- stdin/stdout JSONL helpers -----------------------------------------

// writer / outbound types below mirror cmd/runtimes/echo. They are
// duplicated rather than shared because the runtime binaries are
// intentionally simple and self-contained; runtime authors copying one
// of these into a new project want a single file, not a shared util.

type writer struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func newWriter(w io.Writer) *writer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &writer{enc: enc}
}

func (w *writer) write(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(v)
}

// outputPart mirrors the §15.4.1 OutputPart wire JSON, restricted to the
// fields delegation-echo reads or writes. SchemaVersion is mandatory per
// §15.4.1; delegation-echo stamps v1.
type outputPart struct {
	SchemaVersion int            `json:"schemaVersion"`
	Type          string         `json:"type"`
	ID            string         `json:"id,omitempty"`
	Inline        string         `json:"inline,omitempty"`
	Ref           string         `json:"ref,omitempty"`
	MimeTyp       string         `json:"mimeType,omitempty"`
	Annotations   map[string]any `json:"annotations,omitempty"`
	Status        string         `json:"status,omitempty"`
}

// responseError is the §15.4.1 optional `response.error` object and the
// §8.8 TaskResult.error object: both carry a code and a message.
type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type response struct {
	Type   string         `json:"type"`
	Output []outputPart   `json:"output"`
	Error  *responseError `json:"error,omitempty"`
}

type heartbeatAck struct {
	Type string `json:"type"`
}

// protocolError signals a non-recoverable inbound-format violation.
// main maps this to exit code 2.
type protocolError struct{ msg string }

func (e protocolError) Error() string { return "protocol error: " + e.msg }
