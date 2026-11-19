// SPDX-License-Identifier: MIT

package lenny

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPCreateAndStartSessionReturnsTheRunningSession checks that
// CreateAndStartSession invokes lenny/create_and_start_session and decodes
// the session identifier from the `id` field of the REST body the tool
// answers with, together with the running state.
//
// spec: §15.2 (MCP API)
func TestMCPCreateAndStartSessionReturnsTheRunningSession(t *testing.T) {
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	res, err := m.CreateAndStartSession(context.Background(), "echo")
	if err != nil {
		t.Fatalf("CreateAndStartSession: %v", err)
	}
	if res.SessionID != "sess_mcp_started" {
		t.Errorf("session id: got %q, want sess_mcp_started", res.SessionID)
	}
	if res.State != "running" {
		t.Errorf("state: got %q, want running", res.State)
	}
}

// TestMCPCreateAndStartSessionToolFailureIsError checks that a tool result
// with isError set, such as a refused create, surfaces as a Go error that
// carries the tool's text rather than as an empty session.
//
// spec: §15.2 (MCP API)
func TestMCPCreateAndStartSessionToolFailureIsError(t *testing.T) {
	ts := httptest.NewServer(mcpStub(t))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	res, err := m.CreateAndStartSession(context.Background(), "")
	if err == nil {
		t.Fatalf("CreateAndStartSession with no runtimeRef returned %+v and no error", res)
	}
	if !strings.Contains(err.Error(), "runtimeRef is required") {
		t.Errorf("error %q does not carry the tool's failure text", err)
	}
}

// TestMCPCreateAndStartSessionTransportErrorIsSentOnce checks that a
// JSON-RPC error, such as a gateway that does not register the tool, is
// returned as an *MCPError and that the tools/call request is not retried.
//
// spec: §15.2 (MCP API)
func TestMCPCreateAndStartSessionTransportErrorIsSentOnce(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpRPCEnvelope
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Method == "initialize" {
			writeRPC(w, req.ID, map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}, nil)
			return
		}
		calls++
		writeRPC(w, req.ID, nil, &MCPError{Code: -32601, Message: "unknown tool lenny/create_and_start_session"})
	}))
	defer ts.Close()
	m := mcpTestClient(t, ts.URL)

	_, err := m.CreateAndStartSession(context.Background(), "echo")
	if err == nil {
		t.Fatal("CreateAndStartSession against a gateway without the tool returned no error")
	}
	if _, ok := AsMCPError(err); !ok {
		t.Errorf("error is not an *MCPError: %v", err)
	}
	if calls != 1 {
		t.Errorf("tools/call sent %d times, want 1", calls)
	}
}
