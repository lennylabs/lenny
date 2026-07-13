// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
)

// correlationRecorder is a test Invoker that captures the correlation
// fields present on the context the §25.12 management server passes to
// Invoke, so a test can assert what the adapter derived from the
// tools/call request before it reaches the underlying REST call.
type correlationRecorder struct {
	fields correlation.Fields
}

func (c *correlationRecorder) Invoke(ctx context.Context, _ mcp.Tool, _ json.RawMessage) (mcp.ToolResult, error) {
	c.fields = correlation.From(ctx)
	return mcp.ToolResult{Status: 200, Body: json.RawMessage(`{}`)}, nil
}

// callToolOK drives a §25.12 tools/call against srv and fails the test on
// a transport or JSON-RPC error, so each correlation assertion below reads
// only the recorded fields.
func callToolOK(t *testing.T, srv *mcp.Server, params map[string]any) {
	t.Helper()
	code, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params,
	})
	if code != 200 {
		t.Fatalf("tools/call HTTP status = %d, want 200; resp=%v", code, resp)
	}
	if e, ok := resp["error"]; ok {
		t.Fatalf("tools/call returned a JSON-RPC error: %v", e)
	}
}

// The §25.12 "Headers and Correlation" mapping requires the MCP adapter to
// carry the tool input's optional operationId field onto the underlying
// REST call as the X-Lenny-Operation-ID correlation value.
//
// spec: §25.12 (Headers and Correlation) — "X-Lenny-Operation-ID from the
// tool input's optional operationId field (if present) ... → same HTTP
// header on REST calls."
func TestToolInputOperationIDPropagates_spec_25_12(t *testing.T) {
	inv := &correlationRecorder{}
	srv := mcp.NewServer(inv)
	callToolOK(t, srv, map[string]any{
		"name": "lenny_lock_acquire",
		"arguments": map[string]any{
			"scope": "pool:default-gvisor", "operation": "scale", "operationId": "op-from-input",
		},
	})
	if inv.fields.OperationID != "op-from-input" {
		t.Errorf("OperationID = %q, want op-from-input (from the tool input operationId field)", inv.fields.OperationID)
	}
}

// The adapter also accepts the operation ID from MCP request metadata
// (_meta.operationId in the tools/call request) when the tool input omits
// the operationId field.
//
// spec: §25.12 (Headers and Correlation) — "X-Lenny-Operation-ID from ...
// OR from MCP tool call metadata (_meta.operationId in the tools/call
// request, per MCP convention for request metadata) → same HTTP header on
// REST calls."
func TestMetaOperationIDPropagates_spec_25_12(t *testing.T) {
	inv := &correlationRecorder{}
	srv := mcp.NewServer(inv)
	callToolOK(t, srv, map[string]any{
		"name":      "lenny_lock_acquire",
		"arguments": map[string]any{"scope": "pool:default-gvisor", "operation": "scale"},
		"_meta":     map[string]any{"operationId": "op-from-meta"},
	})
	if inv.fields.OperationID != "op-from-meta" {
		t.Errorf("OperationID = %q, want op-from-meta (from _meta.operationId)", inv.fields.OperationID)
	}
}

// When both the tool input operationId and _meta.operationId are present,
// the tool input field wins.
//
// spec: §25.12 (Headers and Correlation) — "When both are provided, the
// tool input field wins."
func TestToolInputOperationIDWinsOverMeta_spec_25_12(t *testing.T) {
	inv := &correlationRecorder{}
	srv := mcp.NewServer(inv)
	callToolOK(t, srv, map[string]any{
		"name": "lenny_lock_acquire",
		"arguments": map[string]any{
			"scope": "pool:default-gvisor", "operation": "scale", "operationId": "op-from-input",
		},
		"_meta": map[string]any{"operationId": "op-from-meta"},
	})
	if inv.fields.OperationID != "op-from-input" {
		t.Errorf("OperationID = %q, want op-from-input (tool input wins over _meta on conflict)", inv.fields.OperationID)
	}
}

// The adapter carries the MCP clientInfo.name onto the underlying REST
// call as the X-Lenny-Agent-Name correlation value.
//
// spec: §25.12 (Headers and Correlation) — "X-Lenny-Agent-Name from the
// MCP clientInfo.name → same HTTP header on REST calls."
func TestClientInfoNamePropagatesAsAgentName_spec_25_12(t *testing.T) {
	inv := &correlationRecorder{}
	srv := mcp.NewServer(inv)
	callToolOK(t, srv, map[string]any{
		"name":       "lenny_lock_acquire",
		"arguments":  map[string]any{"scope": "pool:default-gvisor", "operation": "scale"},
		"clientInfo": map[string]any{"name": "prod-watchdog-us-east-1"},
	})
	if inv.fields.AgentName != "prod-watchdog-us-east-1" {
		t.Errorf("AgentName = %q, want prod-watchdog-us-east-1 (from clientInfo.name)", inv.fields.AgentName)
	}
}
