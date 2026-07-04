// SPDX-License-Identifier: MIT

// Command elicitation-echo is the §9.2 reference Standard-level
// adapter binary that exercises the platform elicitation tool. It
// inherits the Basic-level stdin/stdout protocol from cmd/runtimes/echo
// (message → response, heartbeat → heartbeat_ack, shutdown → clean
// exit) and adds the §15.4.3 intra-pod MCP integration so it can
// call `lenny/request_elicitation` on every inbound `message`.
//
// On each inbound message it:
//
//  1. Connects to the platform MCP server using the §15.4.3 nonce
//     handshake from the adapter manifest (the same shape as
//     delegation-echo).
//  2. Calls `lenny/request_elicitation` with the inbound input as
//     the prompt; blocks until the §9.1 timeout or a human
//     response arrives via §15.1 respond/dismiss.
//  3. Emits the response payload back through the §15.4.1 stdout
//     `response` frame.
//
// elicitation-echo is the §9.2 Standard-level exemplar runtime: it
// calls `lenny/request_elicitation` on each inbound message and returns
// the human response. The elicitation integration suite and the tier-9
// admin probes for the enforce, detect-only, and floor wire contract use
// it as the raising pod. In v1 the gateway resolves the elicitation
// chain server-internally and no intermediate pod re-emits, so there is
// no live content-tamper path for this runtime to drive; the §9.2
// content-integrity detector is the forward-compatible enforcement point
// exercised by injected providers in unit tests.
//
// The runtime degrades to Basic-level echo when no adapter manifest
// is present (the same fallback delegation-echo uses) so the Basic
// contract suite can exercise it without a platform MCP server.
//
// Exit codes (spec §15.4): identical to echo — 0 success, 1
// runtime error, 2 protocol error.
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
	mcpProtocolVersion  = "2025-03-26"
	nonceParamKey       = "_lennyNonce"
)

func main() {
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

	manifest, manifestErr := loadManifest(os.Getenv("LENNY_ADAPTER_MANIFEST"))
	var platform *mcpClient
	if manifestErr != nil {
		fmt.Fprintf(stderr, "elicitation-echo: no adapter manifest (%v); MCP integration disabled\n", manifestErr)
	} else if manifest.PlatformMcpServer == nil || manifest.PlatformMcpServer.Socket == "" {
		fmt.Fprintln(stderr, "elicitation-echo: manifest has no platform MCP server; MCP integration disabled")
	} else {
		c, err := connectMCP(ctx, manifest.PlatformMcpServer.Socket, manifest.MCPNonce, "elicitation-echo")
		if err != nil {
			return fmt.Errorf("connect platform MCP server: %w", err)
		}
		defer c.close()
		platform = c
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
			cancel()
			return nil
		default:
			fmt.Fprintf(stderr, "elicitation-echo: ignoring unknown message type %q\n", env.Type)
		}
	}
	if err := scanner.Err(); err != nil {
		return protocolError{msg: fmt.Sprintf("stdin read error: %v", err)}
	}
	return nil
}

type adapterManifest struct {
	Version           int    `json:"version"`
	MCPNonce          string `json:"mcpNonce"`
	PlatformMcpServer *struct {
		Socket string `json:"socket"`
	} `json:"platformMcpServer"`
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
	if m.Version > 1 {
		return adapterManifest{}, fmt.Errorf("manifest %s: version %d is newer than supported (1)", path, m.Version)
	}
	return m, nil
}

func handleMessage(w *writer, line []byte, seq *atomic.Uint64, platform *mcpClient, stderr io.Writer) error {
	var inbound struct {
		Type  string        `json:"type"`
		ID    string        `json:"id"`
		Input []messagePart `json:"input"`
	}
	if err := json.Unmarshal(line, &inbound); err != nil {
		return protocolError{msg: fmt.Sprintf("malformed message envelope: %v", err)}
	}
	n := seq.Add(1)

	if platform == nil {
		// Basic-level fallback when no manifest / no platform MCP.
		return w.write(response{Type: "response", Output: echoParts(inbound.Input, n)})
	}

	out, err := elicitAndEcho(platform, inbound.Input, n)
	if err != nil {
		fmt.Fprintf(stderr, "elicitation-echo: elicitation flow failed: %v\n", err)
		return w.write(response{
			Type:   "response",
			Output: []messagePart{},
			Error:  &responseError{Code: "ELICITATION_FAILED", Message: err.Error()},
		})
	}
	return w.write(response{Type: "response", Output: out})
}

// elicitAndEcho calls `lenny/request_elicitation` with the message
// input as the prompt and returns the elicitation result parts.
func elicitAndEcho(platform *mcpClient, input []messagePart, seq uint64) ([]messagePart, error) {
	prompt := fmt.Sprintf("[elicitation-echo seq=%d] please confirm:", seq)
	if len(input) > 0 && input[0].Inline != "" {
		prompt = input[0].Inline
	}

	// §9.2 request_elicitation: the call blocks until a human
	// responds via the §15.1 respond endpoint or the §9.1 timeout
	// fires. The result is the response payload.
	resRaw, err := platform.callTool("lenny/request_elicitation", map[string]any{
		"message": prompt,
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"answer": map[string]any{"type": "string"},
			},
			"required": []string{"answer"},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("lenny/request_elicitation: %w", err)
	}
	// The result is opaque to us; emit it back as a text part so the
	// task contract sees a non-empty response.
	resText := strings.TrimSpace(string(resRaw))
	return []messagePart{{
		SchemaVersion: 1,
		Type:          "text",
		Inline:        fmt.Sprintf("[elicitation-echo seq=%d resolved] %s", seq, resText),
	}}, nil
}

func echoParts(in []messagePart, seq uint64) []messagePart {
	out := make([]messagePart, 0, len(in))
	for _, p := range in {
		if p.SchemaVersion == 0 {
			p.SchemaVersion = 1
		}
		if p.Type == "text" && p.Inline != "" {
			out = append(out, messagePart{
				SchemaVersion: 1,
				Type:          "text",
				Inline:        fmt.Sprintf("[elicitation-echo seq=%d] %s", seq, p.Inline),
				ID:            p.ID,
				MimeTyp:       p.MimeTyp,
			})
		} else {
			out = append(out, p)
		}
	}
	return out
}

// --- intra-pod MCP client (copied from delegation-echo) ----------

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
	if _, err := c.call("tools/list", map[string]any{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return c, nil
}

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
	select {
	case <-time.After(200 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return d.DialContext(ctx, "unix", addr)
}

func (c *mcpClient) callTool(name string, arguments any) (json.RawMessage, error) {
	return c.call("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	})
}

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

// --- stdin/stdout JSONL helpers (copied from delegation-echo) -----

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

type messagePart struct {
	SchemaVersion int            `json:"schemaVersion"`
	Type          string         `json:"type"`
	ID            string         `json:"id,omitempty"`
	Inline        string         `json:"inline,omitempty"`
	Ref           string         `json:"ref,omitempty"`
	MimeTyp       string         `json:"mimeType,omitempty"`
	Annotations   map[string]any `json:"annotations,omitempty"`
	Status        string         `json:"status,omitempty"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type response struct {
	Type   string         `json:"type"`
	Output []messagePart  `json:"output"`
	Error  *responseError `json:"error,omitempty"`
}

type heartbeatAck struct {
	Type string `json:"type"`
}

type protocolError struct{ msg string }

func (e protocolError) Error() string { return "protocol error: " + e.msg }
