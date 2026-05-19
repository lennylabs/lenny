// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// TestMCPManagementToolsList confirms the §25.12 MCP management server
// is served at /mcp/management and lists the operability tools.
func TestMCPManagementToolsList(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	result, _ := body["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list returned an empty inventory")
	}
}

// TestMCPManagementInvokesEscalationCreate confirms a §25.12 tools/call
// is routed end-to-end through the MCP adapter into the ops escalation
// handler.
func TestMCPManagementInvokesEscalationCreate(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Escalations: escalation.NewService(nil)})
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "lenny_escalation_create",
			"arguments": map[string]any{
				"severity": "critical", "summary": "warm pool exhausted",
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	result, _ := body["result"].(map[string]any)
	if result["isError"] != false {
		t.Errorf("isError = %v, want false on a successful escalation create", result["isError"])
	}
	// The escalation now exists on the ops escalation service.
	_, list := doJSON(t, srv, http.MethodGet, "/v1/admin/escalations", nil, nil)
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Errorf("escalation service has %d escalations after the MCP call, want 1", len(items))
	}
}

// TestMCPManagementInvokesLockAcquire confirms a §25.12 tools/call with
// arguments is routed into the ops remediation-lock handler.
func TestMCPManagementInvokesLockAcquire(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore()})
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": "lenny_lock_acquire",
			"arguments": map[string]any{
				"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": float64(300),
			},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	// The lock now exists on the ops lock store.
	_, list := doJSON(t, srv, http.MethodGet, "/v1/admin/remediation-locks", nil, nil)
	locks, _ := list["locks"].([]any)
	if len(locks) != 1 {
		t.Errorf("lock store has %d locks after the MCP call, want 1", len(locks))
	}
}

// TestMCPManagementUnavailableEndpointMapsTo32000 confirms a §25.12
// tools/call against an unconfigured ops endpoint maps the 503 to the
// -32000 ENDPOINT_UNAVAILABLE error.
func TestMCPManagementUnavailableEndpointMapsTo32000(t *testing.T) {
	// No escalation service configured — the endpoint is 503.
	srv := opsserver.New(opsserver.Options{})
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny_escalation_create",
			"arguments": map[string]any{"severity": "info", "summary": "x"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JSON-RPC carries the error in the body)", rec.Code)
	}
	rpcErr, _ := body["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32000 {
		t.Fatalf("error = %v, want -32000 ENDPOINT_UNAVAILABLE", rpcErr)
	}
}
