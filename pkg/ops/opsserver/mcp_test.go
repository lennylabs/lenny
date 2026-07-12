// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
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

// TestMCPManagementCallCarriesCallerPrincipalThroughAuthGate confirms
// that when the §25.4 OIDC auth gate is configured, a tools/call the
// caller is authorized to make reaches its underlying endpoint. §25.12
// routes every scope-passing tool invocation into a REST call that
// "passes through the standard OIDC/JWT middleware and role-based
// authorization check"; the internal replay must therefore carry the
// caller's verified principal so an authorized platform-admin call
// returns the endpoint's result rather than being rejected 401/403 by
// the role gate on the replay. Without principal propagation every
// management tool invocation fails under a production auth config.
//
// diagnosis: the §25.12 MCP invoker replays the mapped REST request
// without the caller principal, so the §25.4 requireAdminRole gate
// rejects the replay and the tool result comes back isError:true even
// for an authorized caller.
//
// spec: §25.12 ("Every MCP tool invocation that passes the scope check
// is translated into a REST call ... That REST call passes through the
// standard OIDC/JWT middleware and role-based authorization check.").
func TestMCPManagementCallCarriesCallerPrincipal_spec_25_12(t *testing.T) {
	srv, signer := authedServer()
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management",
		map[string]string{"Authorization": "Bearer " + mintOpsToken(t, signer, "alice@acme.com", auth.RolePlatformAdmin)},
		map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name":      "lenny_ops_health_get",
				"arguments": map[string]any{},
			},
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if rpcErr, ok := body["error"].(map[string]any); ok {
		t.Fatalf("tools/call returned a JSON-RPC error for an authorized platform-admin: %v", rpcErr)
	}
	result, _ := body["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; the internal replay must carry the caller principal so the platform-admin call is admitted by the role gate; result=%v", result["isError"], result)
	}
}

// TestMCPManagementCallRejectsNonAdminRole confirms the §25.12 security
// model's REST-layer RBAC: a caller whose bearer carries no admin role
// cannot invoke a management tool. The role gate rejects the request at
// the /mcp/management boundary, so the caller never reaches the tool
// dispatch.
//
// diagnosis: the §25.4 role gate must reject a non-admin caller from the
// MCP management surface; a 200 here means the role layer of the §25.12
// security model is not enforced on the MCP path.
//
// spec: §25.12 ("An agent with a tenant-admin role receives 403 from the
// underlying REST endpoint when calling a tool that requires
// platform-admin ... scopes narrow; they don't elevate") and §25.4 line
// 1562 ("Requires platform-admin or tenant-admin role on all
// endpoints.").
func TestMCPManagementCallRejectsNonAdminRole_spec_25_12(t *testing.T) {
	srv, signer := authedServer()
	rec, _ := doJSON(t, srv, http.MethodPost, "/mcp/management",
		map[string]string{"Authorization": "Bearer " + mintOpsToken(t, signer, "bob@acme.com", auth.RoleUser)},
		map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name":      "lenny_ops_health_get",
				"arguments": map[string]any{},
			},
		})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-admin caller; body=%s", rec.Code, rec.Body.String())
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
