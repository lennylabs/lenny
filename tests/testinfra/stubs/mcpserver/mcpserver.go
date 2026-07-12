// SPDX-License-Identifier: MIT

// Package mcpserver is an external third-party MCP server stub. It stands
// in for a registered §9.3 connector endpoint (`Gateway ↔ external MCP
// tools`) so a test can exercise the gateway's outbound Streamable-HTTP
// MCP client end to end against a real HTTP server instead of a fake
// in-process invoker.
//
// The stub serves the JSON-RPC 2.0 methods the gateway's connector client
// drives over the Streamable-HTTP transport: `initialize`,
// `notifications/initialized`, `tools/list`, and `tools/call`. It records
// every inbound request so a test can assert the bearer credential the
// gateway carried, and it supports configurable latency and error
// injection so failure and timeout paths can be exercised.
//
// The server is TLS because §9.3 requires a connector `mcpServerUrl` to be
// HTTPS; the connector registry rejects a plaintext endpoint. Client()
// returns an *http.Client that trusts the stub's self-signed certificate,
// suitable as the connectorinvoke dialer.
//
// Usage:
//
//	srv := mcpserver.New(t,
//	    mcpserver.WithTool(mcpserver.Tool{Name: "echo", Description: "echo"}),
//	    mcpserver.WithToolResult("echo", json.RawMessage(`{"content":[{"type":"text","text":"hi"}]}`)),
//	)
//	inv := connectorinvoke.New(srv.Client())
//	// register a connector with MCPServerURL == srv.URL(), then drive it.
//
// spec: §9.1 line 10 (Gateway ↔ external MCP tools); §9.3 lines 142-164.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Tool is one entry the stub advertises from `tools/list`.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	Annotations json.RawMessage `json:"annotations,omitempty"`
}

// Request captures the relevant bits of an inbound JSON-RPC request the
// stub recorded, so a test can assert what the gateway sent.
type Request struct {
	Method        string
	Authorization string
	SessionID     string
	Params        json.RawMessage
}

// Stub is a recorded external MCP server.
type Stub struct {
	server    *httptest.Server
	sessionID string

	mu          sync.Mutex
	tools       []Tool
	results     map[string]json.RawMessage
	toolErrors  map[string]*rpcError
	latency     time.Duration
	forceStatus int
	requests    []Request
}

// rpcError is a JSON-RPC 2.0 error object the stub can inject on a
// tools/call.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Option configures a Stub at construction time.
type Option func(*Stub)

// WithTool advertises t from tools/list. Repeat to advertise several.
func WithTool(t Tool) Option {
	return func(s *Stub) { s.tools = append(s.tools, t) }
}

// WithToolResult sets the raw MCP tools/call result the stub returns for
// the named tool. The value is returned verbatim as the JSON-RPC result,
// so it should be a well-formed MCP tool result object (for example
// `{"content":[{"type":"text","text":"ok"}]}` or one carrying
// `"isError":true`).
func WithToolResult(name string, result json.RawMessage) Option {
	return func(s *Stub) { s.results[name] = result }
}

// New starts the stub over TLS and registers a t.Cleanup that closes it.
// The stub assigns a fixed Mcp-Session-Id on initialize so a test can
// assert session continuity across the handshake.
func New(t testing.TB, opts ...Option) *Stub {
	t.Helper()
	s := &Stub{
		sessionID:  "mcp-stub-session",
		results:    map[string]json.RawMessage{},
		toolErrors: map[string]*rpcError{},
	}
	for _, opt := range opts {
		opt(s)
	}
	s.server = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

// URL is the stub's HTTPS base URL, suitable as a connector mcpServerUrl.
func (s *Stub) URL() string { return s.server.URL }

// Client returns an *http.Client that trusts the stub's self-signed TLS
// certificate. Use it as the connectorinvoke dialer so the gateway can
// reach the stub without a real CA.
func (s *Stub) Client() *http.Client { return s.server.Client() }

// SetLatency injects a per-request delay before the stub responds. A
// value larger than the caller's context deadline drives the timeout
// path. Safe to call while the server is running.
func (s *Stub) SetLatency(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latency = d
}

// SetToolError injects a JSON-RPC error the stub returns for the named
// tool's tools/call, exercising the gateway's error-propagation path.
func (s *Stub) SetToolError(name string, code int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolErrors[name] = &rpcError{Code: code, Message: message}
}

// SetForceStatus makes every request fail with the given HTTP status,
// exercising the transport-level failure path. A zero value clears it.
func (s *Stub) SetForceStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forceStatus = status
}

// Requests returns a snapshot of every recorded request, in order.
func (s *Stub) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Request, len(s.requests))
	copy(out, s.requests)
	return out
}

// jsonrpcRequest is the subset of an inbound JSON-RPC 2.0 request the
// stub inspects. A notification carries no id.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (s *Stub) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	var req jsonrpcRequest
	_ = json.Unmarshal(body, &req)

	s.mu.Lock()
	s.requests = append(s.requests, Request{
		Method:        req.Method,
		Authorization: r.Header.Get("Authorization"),
		SessionID:     r.Header.Get("Mcp-Session-Id"),
		Params:        req.Params,
	})
	latency := s.latency
	forceStatus := s.forceStatus
	s.mu.Unlock()

	if latency > 0 {
		select {
		case <-time.After(latency):
		case <-r.Context().Done():
			// The caller's deadline fired first; drop the connection so the
			// gateway client observes a transport failure rather than a
			// late response.
			return
		}
	}
	if forceStatus != 0 {
		http.Error(w, "injected failure", forceStatus)
		return
	}

	switch req.Method {
	case "initialize":
		s.writeResult(w, req.ID, s.initializeResult(), s.sessionID)
	case "notifications/initialized":
		// A notification carries no id and expects no body; the gateway
		// treats any 2xx as success.
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		s.writeResult(w, req.ID, s.toolsListResult(), "")
	case "tools/call":
		s.handleToolsCall(w, req)
	default:
		s.writeError(w, req.ID, -32601, "method not found: "+req.Method)
	}
}

func (s *Stub) initializeResult() json.RawMessage {
	raw, _ := json.Marshal(map[string]any{
		"protocolVersion": "2025-06-18",
		"serverInfo":      map[string]string{"name": "mcpserver-stub", "version": "1"},
		"capabilities":    map[string]any{"tools": map[string]any{}},
	})
	return raw
}

func (s *Stub) toolsListResult() json.RawMessage {
	s.mu.Lock()
	tools := make([]Tool, len(s.tools))
	copy(tools, s.tools)
	s.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"tools": tools})
	return raw
}

func (s *Stub) handleToolsCall(w http.ResponseWriter, req jsonrpcRequest) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(req.Params, &params)

	s.mu.Lock()
	injected := s.toolErrors[params.Name]
	result, haveResult := s.results[params.Name]
	s.mu.Unlock()

	if injected != nil {
		s.writeError(w, req.ID, injected.Code, injected.Message)
		return
	}
	if !haveResult {
		// A tool the stub was not configured to answer echoes its
		// arguments so a test that does not pin a result still gets a
		// well-formed MCP tool result.
		result = json.RawMessage(fmt.Sprintf(`{"content":[{"type":"text","text":%q}]}`, string(params.Arguments)))
	}
	s.writeResult(w, req.ID, result, "")
}

func (s *Stub) writeResult(w http.ResponseWriter, id, result json.RawMessage, sessionID string) {
	if sessionID != "" {
		w.Header().Set("Mcp-Session-Id", sessionID)
	}
	w.Header().Set("Content-Type", "application/json")
	env := map[string]any{"jsonrpc": "2.0", "id": rawOrNull(id), "result": json.RawMessage(result)}
	_ = json.NewEncoder(w).Encode(env)
}

func (s *Stub) writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	env := map[string]any{
		"jsonrpc": "2.0",
		"id":      rawOrNull(id),
		"error":   map[string]any{"code": code, "message": message},
	}
	_ = json.NewEncoder(w).Encode(env)
}

// rawOrNull returns id verbatim, or a JSON null when the request carried
// no id (so the response envelope stays valid JSON-RPC).
func rawOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}
