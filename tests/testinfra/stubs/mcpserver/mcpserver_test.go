// SPDX-License-Identifier: MIT

package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/tests/testinfra/stubs/mcpserver"
)

// dial drives the real gateway outbound MCP client against the stub, so
// the self-test exercises the same wire path production uses.
func dial(t *testing.T, srv *mcpserver.Stub) *connectorinvoke.Session {
	t.Helper()
	client := connectorinvoke.New(srv.Client())
	sess, _, err := client.Initialize(context.Background(), srv.URL(), "bearer-token-xyz")
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	return sess
}

// spec: 9.1 (Gateway ↔ external MCP tools), 9.3 (connector MCP endpoint)
func TestStubServesToolsListAndCall(t *testing.T) {
	srv := mcpserver.New(
		t,
		mcpserver.WithTool(mcpserver.Tool{Name: "echo", Description: "echo tool"}),
		mcpserver.WithToolResult("echo", json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)),
	)
	sess := dial(t, srv)

	tools, err := sess.ListTools(context.Background())
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools/list = %+v, want the echo tool", tools)
	}

	raw, err := sess.CallTool(context.Background(), "echo", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if !strings.Contains(string(raw), "pong") {
		t.Errorf("tools/call result = %s, want the configured result", raw)
	}

	// The stub recorded the bearer the gateway carried and the session id
	// it echoed after initialize.
	var sawBearer, sawSession bool
	for _, r := range srv.Requests() {
		if r.Authorization == "Bearer bearer-token-xyz" {
			sawBearer = true
		}
		if r.Method == "tools/call" && r.SessionID == "mcp-stub-session" {
			sawSession = true
		}
	}
	if !sawBearer {
		t.Error("stub never recorded the gateway bearer credential")
	}
	if !sawSession {
		t.Error("stub never saw the assigned Mcp-Session-Id carried on tools/call")
	}
}

// spec: 9.1 (Gateway ↔ external MCP tools — error propagation)
func TestStubInjectsToolError(t *testing.T) {
	srv := mcpserver.New(t, mcpserver.WithTool(mcpserver.Tool{Name: "boom"}))
	srv.SetToolError("boom", -32000, "external tool exploded")
	sess := dial(t, srv)

	if _, err := sess.CallTool(context.Background(), "boom", nil); err == nil {
		t.Fatal("tools/call returned nil error, want the injected JSON-RPC error")
	}
}

// spec: 9.1 (Gateway ↔ external MCP tools — transport failure)
func TestStubForceStatusDrivesTransportFailure(t *testing.T) {
	srv := mcpserver.New(t)
	srv.SetForceStatus(503)
	client := connectorinvoke.New(srv.Client())
	if _, _, err := client.Initialize(context.Background(), srv.URL(), ""); err == nil {
		t.Fatal("initialize succeeded against a 503 stub, want a transport error")
	}
}

// spec: 9.2 (per-hop forwarding timeout — external server latency)
func TestStubLatencyDrivesDeadline(t *testing.T) {
	srv := mcpserver.New(t)
	srv.SetLatency(200 * time.Millisecond)
	client := connectorinvoke.New(srv.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := client.Initialize(ctx, srv.URL(), ""); err == nil {
		t.Fatal("initialize succeeded despite the deadline, want a context-deadline error")
	}
}
