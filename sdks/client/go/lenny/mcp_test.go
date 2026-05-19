// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mcpRPCEnvelope is the JSON-RPC 2.0 request shape the stub gateway
// decodes. It mirrors the §15.2 wire envelope the gateway MCP server
// accepts.
type mcpRPCEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// mcpStub is an in-test JSON-RPC 2.0 handler that answers the §15.2
// MCP methods the MCPClient exercises. It is a faithful stand-in for
// the gateway /mcp endpoint: the contract test in
// tests/tier3_contract/sdks drives the real gateway MCP server.
func mcpStub(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		var req mcpRPCEnvelope
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeRPC(w, nil, nil, &MCPError{Code: -32700, Message: "parse error"})
			return
		}
		if req.JSONRPC != "2.0" {
			writeRPC(w, req.ID, nil, &MCPError{Code: -32600, Message: "jsonrpc must be 2.0"})
			return
		}
		switch req.Method {
		case "initialize":
			writeRPC(w, req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "lenny-gateway", "version": "0.1.0"},
			}, nil)
		case "tools/list":
			writeRPC(w, req.ID, map[string]any{"tools": []map[string]any{
				{
					"name":        "lenny/create_session",
					"description": "Create a new agent session against a runtime.",
					"inputSchema": map[string]any{"type": "object"},
				},
				{
					"name":        "lenny/send_message",
					"description": "Deliver a message to a running session.",
					"inputSchema": map[string]any{"type": "object"},
				},
			}}, nil)
		case "tools/call":
			handleStubToolCall(w, req)
		default:
			writeRPC(w, req.ID, nil, &MCPError{Code: -32601, Message: "unknown method " + req.Method})
		}
	})
	return mux
}

// handleStubToolCall answers a tools/call request. lenny/create_session
// and lenny/send_message return a session-driving result; an unknown
// tool name returns a JSON-RPC method-not-found error; a tool asked to
// fail returns a result with isError set.
func handleStubToolCall(w http.ResponseWriter, req mcpRPCEnvelope) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeRPC(w, req.ID, nil, &MCPError{Code: -32602, Message: "invalid params"})
		return
	}
	switch params.Name {
	case "lenny/create_session":
		var args struct {
			RuntimeRef string `json:"runtimeRef"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		if args.RuntimeRef == "" {
			writeRPC(w, req.ID, toolResultMap("runtimeRef is required", true), nil)
			return
		}
		writeRPC(w, req.ID, toolResultMap(`{"sessionId":"sess_mcp_1","state":"running"}`, false), nil)
	case "lenny/send_message":
		var args struct {
			Content string `json:"content"`
		}
		_ = json.Unmarshal(params.Arguments, &args)
		writeRPC(w, req.ID, toolResultMap("echo: "+args.Content, false), nil)
	default:
		writeRPC(w, req.ID, nil, &MCPError{Code: -32601, Message: "unknown tool " + params.Name})
	}
}

// toolResultMap builds an MCP tools/call result carrying a single text
// content block.
func toolResultMap(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

// writeRPC writes a JSON-RPC 2.0 response. A non-nil errObj produces an
// error response; otherwise result is the response body.
func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, errObj *MCPError) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id)}
	if errObj != nil {
		resp["error"] = errObj
	} else {
		resp["result"] = result
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// mcpTestClient builds an MCPClient against the stub gateway.
func mcpTestClient(t *testing.T, baseURL string) *MCPClient {
	t.Helper()
	c, err := New(baseURL, WithTenant("acme"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c.MCP()
}

func TestMCPInitialize(t *testing.T) {
	// spec: §15.2 MCP initialize handshake + version negotiation.
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	res, err := m.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if res.ProtocolVersion == "" {
		t.Error("Initialize returned an empty negotiated protocol version")
	}
	if res.ServerInfo.Name != "lenny-gateway" {
		t.Errorf("serverInfo.name: got %q, want lenny-gateway", res.ServerInfo.Name)
	}
}

func TestMCPListTools(t *testing.T) {
	// spec: §15.2 tools/list returns the platform tool catalog.
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	tools, err := m.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("ListTools returned an empty catalog")
	}
	byName := map[string]bool{}
	for _, tool := range tools {
		byName[tool.Name] = true
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no input schema", tool.Name)
		}
	}
	for _, want := range []string{"lenny/create_session", "lenny/send_message"} {
		if !byName[want] {
			t.Errorf("tools/list omitted %q; got %v", want, byName)
		}
	}
}

func TestMCPListToolsRunsHandshakeFirst(t *testing.T) {
	// spec: §15.2 ListTools runs the initialize handshake on first use.
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	// ListTools without an explicit Initialize must still succeed.
	if _, err := m.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools without explicit Initialize: %v", err)
	}
	m.mu.Lock()
	done := m.initialized
	m.mu.Unlock()
	if !done {
		t.Error("ListTools did not run the initialize handshake")
	}
}

func TestMCPCallToolDrivesSession(t *testing.T) {
	// spec: §15.2 tools/call drives a session over MCP.
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)
	ctx := context.Background()

	created, err := m.CreateSession(ctx, "claude-code", "alice@acme.com")
	if err != nil {
		t.Fatalf("CreateSession over MCP: %v", err)
	}
	if created.SessionID == "" {
		t.Fatal("CreateSession returned an empty session id")
	}
	if created.State != "running" {
		t.Errorf("create state: got %q, want running", created.State)
	}

	reply, err := m.SendMessage(ctx, created.SessionID, "hello")
	if err != nil {
		t.Fatalf("SendMessage over MCP: %v", err)
	}
	if reply != "echo: hello" {
		t.Errorf("send_message reply: got %q, want %q", reply, "echo: hello")
	}
}

func TestMCPCallToolUnknownToolIsTransportError(t *testing.T) {
	// spec: §15.2 an unknown tool is a JSON-RPC transport error.
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	_, err := m.CallTool(context.Background(), "lenny/no_such_tool", map[string]any{})
	if err == nil {
		t.Fatal("CallTool of an unknown tool returned no error")
	}
	mcpErr, ok := AsMCPError(err)
	if !ok {
		t.Fatalf("unknown tool error is not an *MCPError: %v", err)
	}
	if mcpErr.Code != -32601 {
		t.Errorf("error code: got %d, want -32601", mcpErr.Code)
	}
}

func TestMCPCallToolFailureIsResultNotError(t *testing.T) {
	// spec: §15.2 a tool failure is a result with isError set, not a
	// JSON-RPC transport error.
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	// lenny/create_session with no runtimeRef makes the tool report a
	// failure; that is a result, not a transport error.
	res, err := m.CallTool(context.Background(), "lenny/create_session", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool surfaced a tool failure as a transport error: %v", err)
	}
	if !res.IsError {
		t.Error("expected the tool result to carry isError=true")
	}
	if res.Text() == "" {
		t.Error("tool failure result carried no text")
	}
}

func TestMCPNonJSONRPCStatusIsAPIError(t *testing.T) {
	// spec: §15.2.1 a non-2xx status uses the shared §15.1 error
	// taxonomy so one error-handling strategy covers both surfaces.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"PERMISSION_DENIED","category":"POLICY","message":"no","retryable":false}}`))
	}))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	_, err := m.Initialize(context.Background())
	if err == nil {
		t.Fatal("Initialize against a 403 gateway returned no error")
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("a non-2xx MCP status is not an *APIError: %v", err)
	}
	if apiErr.Code != "PERMISSION_DENIED" {
		t.Errorf("error code: got %q, want PERMISSION_DENIED", apiErr.Code)
	}
}
