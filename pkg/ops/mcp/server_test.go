// SPDX-License-Identifier: MIT

package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/mcp"
)

// fakeInvoker is a test Invoker returning a fixed ToolResult and
// recording the tool it was asked to run.
type fakeInvoker struct {
	result mcp.ToolResult
	err    error
	called string
}

func (f *fakeInvoker) Invoke(_ context.Context, tool mcp.Tool, _ json.RawMessage) (mcp.ToolResult, error) {
	f.called = tool.Name
	return f.result, f.err
}

// rpc posts a JSON-RPC request to the management server and decodes the
// response.
func rpc(t *testing.T, srv *mcp.Server, headers map[string]string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/mcp/management", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

func TestInitializeReportsProtocolAndServerInfo(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	})
	result, _ := resp["result"].(map[string]any)
	if result["protocolVersion"] != mcp.ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %s", result["protocolVersion"], mcp.ProtocolVersion)
	}
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != mcp.ServerName {
		t.Errorf("serverInfo.name = %v, want %s", info["name"], mcp.ServerName)
	}
}

func TestToolsListReturnsTheInventory(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list returned an empty inventory")
	}
	// The §25.12 operability tools are present, including the §4.0 /
	// §25.4 unified Operations Inventory tools. The tool names are the
	// x-lenny-mcp-tool values the openapi-to-mcp generator reads from the
	// served document: the gateway-hosted health tool is admin.health, and
	// the lenny-ops-hosted tools keep their lenny_* names.
	names := toolNames(tools)
	for _, want := range []string{
		"admin.health", "lenny_diagnostics_session", "lenny_drift_report",
		"lenny_lock_acquire", "lenny_escalation_create",
		"lenny_operations_list", "lenny_operation_get",
		// spec: §25.7 Runbook Discovery / §25.12 Tool Inventory — the three
		// runbook-index tools are part of the operability surface every
		// MCP-capable agent discovers through tools/list.
		"lenny_runbooks_list", "lenny_runbooks_get", "lenny_runbook_steps",
	} {
		if !names[want] {
			t.Errorf("tools/list is missing %s", want)
		}
	}
	// The three runbook tools share the §25.7 read scope tools:runbooks:read
	// so a caller with that scope can list, fetch, and step through runbooks.
	byName := map[string]map[string]any{}
	for _, tv := range tools {
		tm, _ := tv.(map[string]any)
		if n, ok := tm["name"].(string); ok {
			byName[n] = tm
		}
	}
	for _, want := range []string{"lenny_runbooks_list", "lenny_runbooks_get", "lenny_runbook_steps"} {
		tm := byName[want]
		if tm == nil {
			continue // already reported missing above
		}
		if got := tm["x-lenny-scope"]; got != "tools:runbooks:read" {
			t.Errorf("%s x-lenny-scope = %v, want tools:runbooks:read", want, got)
		}
	}
	// Each tool carries the §25.12 x-lenny-* extension metadata.
	first, _ := tools[0].(map[string]any)
	if first["x-lenny-category"] == nil || first["x-lenny-scope"] == nil {
		t.Error("a tool descriptor is missing the x-lenny-* extensions")
	}
}

func TestToolsListReadOnlyFilterExcludesMutatingTools(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		"params": map[string]any{"capabilities": map[string]any{"readOnly": true}},
	})
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := toolNames(tools)
	// §25.12 readOnly capability: observation tools stay, mutating ones go.
	if !names["admin.health"] {
		t.Error("readOnly filter dropped the read-only admin.health")
	}
	if names["lenny_lock_acquire"] {
		t.Error("readOnly filter kept the mutating lenny_lock_acquire")
	}
}

func TestToolsListIntersectsCapabilityFilterWithScopeClaim(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	// spec: §25.12 (Capability Negotiation) — "Filtering is ALWAYS
	// intersected with the caller's scope claim (tools not permitted by
	// scope are filtered out regardless of capability declaration)." A
	// caller whose scope claim is tools:health:read declares no capability
	// filters, so on the capability axis every tool would list; the scope
	// intersection must still drop tools the claim does not permit.
	_, resp := rpc(t, srv, map[string]string{"X-Lenny-Scope": "tools:health:read"},
		map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/list",
		})
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list returned an empty inventory for a scope-restricted caller")
	}
	names := toolNames(tools)
	// admin.health carries x-lenny-scope tools:health:read, which the
	// caller's claim permits, so it survives the intersection.
	if !names["admin.health"] {
		t.Error("scope intersection dropped admin.health, which tools:health:read permits")
	}
	// lenny_lock_acquire carries x-lenny-scope tools:locks:write, which the
	// caller's claim does not permit; §25.12 requires it be filtered out
	// even though the caller declared no capability filter that would.
	if names["lenny_lock_acquire"] {
		t.Error("scope intersection kept lenny_lock_acquire, outside the caller's tools:health:read claim")
	}
}

func TestToolsCallDispatchesThroughTheInvoker(t *testing.T) {
	inv := &fakeInvoker{result: mcp.ToolResult{
		Status: 200, Body: json.RawMessage(`{"status":"healthy"}`),
	}}
	srv := mcp.NewServer(inv)
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "admin.health", "arguments": map[string]any{}},
	})
	if inv.called != "admin.health" {
		t.Errorf("invoker ran %q, want admin.health", inv.called)
	}
	result, _ := resp["result"].(map[string]any)
	if result["isError"] != false {
		t.Errorf("isError = %v, want false on a 200 result", result["isError"])
	}
}

func TestToolsCallUnknownToolIsInvalidParams(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "lenny_nonexistent_tool"},
	})
	rpcErr, _ := resp["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32602 {
		t.Errorf("error = %v, want -32602 invalid params for an unknown tool", rpcErr)
	}
}

func TestToolsCallScopeForbidden(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	// §25.12 scope enforcement: a caller whose X-Lenny-Scope does not
	// include the tool's x-lenny-scope receives -32001 SCOPE_FORBIDDEN
	// before any REST call is issued.
	_, resp := rpc(t, srv, map[string]string{"X-Lenny-Scope": "tools:health:read"},
		map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name":      "lenny_lock_acquire",
				"arguments": map[string]any{"scope": "pool:p"},
			},
		})
	rpcErr, _ := resp["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32001 {
		t.Fatalf("error = %v, want -32001 SCOPE_FORBIDDEN", rpcErr)
	}
	data, _ := rpcErr["data"].(map[string]any)
	if data["code"] != "SCOPE_FORBIDDEN" {
		t.Errorf("data.code = %v, want SCOPE_FORBIDDEN", data["code"])
	}
	// spec: §25.2 lines 302-329 — a scope rejection carries the AUTH
	// error category alongside code/retryable. F-25.2.6.
	if data["category"] != "AUTH" {
		t.Errorf("data.category = %v, want AUTH", data["category"])
	}
	if data["requiredScope"] != "tools:locks:write" {
		t.Errorf("data.requiredScope = %v, want tools:locks:write", data["requiredScope"])
	}
}

func TestToolsCallAllowedWhenScopeMatches(t *testing.T) {
	inv := &fakeInvoker{result: mcp.ToolResult{Status: 201, Body: json.RawMessage(`{"id":"lock-1"}`)}}
	srv := mcp.NewServer(inv)
	// The caller's scope claim includes the tool's scope — the call
	// passes the §25.12 scope layer.
	_, resp := rpc(t, srv, map[string]string{"X-Lenny-Scope": "tools:locks:write tools:health:read"},
		map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name":      "lenny_lock_acquire",
				"arguments": map[string]any{"scope": "pool:p"},
			},
		})
	if _, hasErr := resp["error"]; hasErr {
		t.Errorf("scoped call returned an error: %v", resp["error"])
	}
	if inv.called != "lenny_lock_acquire" {
		t.Errorf("invoker ran %q, want lenny_lock_acquire", inv.called)
	}
}

func TestToolsCallDryRunMapsToMetaFlag(t *testing.T) {
	inv := &fakeInvoker{result: mcp.ToolResult{
		Status:  200,
		Body:    json.RawMessage(`{"dryRun":true,"preview":{"estimatedDowntime":"0s"}}`),
		DryRun:  true,
		Preview: json.RawMessage(`{"estimatedDowntime":"0s"}`),
	}}
	srv := mcp.NewServer(inv)
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny_drift_snapshot_refresh",
			"arguments": map[string]any{"desired": map[string]any{}},
		},
	})
	result, _ := resp["result"].(map[string]any)
	// §25.12 dry-run mapping: a preview is isError:false with the
	// canonical _meta.lenny.dryRun flag set.
	if result["isError"] != false {
		t.Errorf("isError = %v, want false for a dry-run preview", result["isError"])
	}
	meta, _ := result["_meta"].(map[string]any)
	if meta == nil || meta["lenny.dryRun"] != true {
		t.Errorf("_meta = %v, want lenny.dryRun:true", meta)
	}
}

func TestToolsCallEndpointUnavailable(t *testing.T) {
	inv := &fakeInvoker{result: mcp.ToolResult{Unavailable: true}}
	srv := mcp.NewServer(inv)
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "admin.health"},
	})
	rpcErr, _ := resp["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32000 {
		t.Fatalf("error = %v, want -32000 for an unavailable endpoint", rpcErr)
	}
	data, _ := rpcErr["data"].(map[string]any)
	if data["code"] != "ENDPOINT_UNAVAILABLE" {
		t.Errorf("data.code = %v, want ENDPOINT_UNAVAILABLE", data["code"])
	}
	// spec: §25.2 lines 302-329 — an unavailable endpoint is a TRANSIENT
	// failure; the category rides alongside code/retryable. F-25.2.6.
	if data["category"] != "TRANSIENT" {
		t.Errorf("data.category = %v, want TRANSIENT", data["category"])
	}
	// §25.12: the tool stays in tools/list during the outage.
	_, list := rpc(t, srv, nil, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	listResult, _ := list["result"].(map[string]any)
	if !toolNames(listResult["tools"].([]any))["admin.health"] {
		t.Error("admin.health was removed from tools/list during an endpoint outage")
	}
}

func TestUnknownMethodIsMethodNotFound(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	_, resp := rpc(t, srv, nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/teleport",
	})
	rpcErr, _ := resp["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32601 {
		t.Errorf("error = %v, want -32601 method not found", rpcErr)
	}
}

func TestServeHTTPRejectsNonPOST(t *testing.T) {
	srv := mcp.NewServer(&fakeInvoker{})
	req := httptest.NewRequest(http.MethodGet, "/mcp/management", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for a GET", rec.Code)
	}
}

func toolNames(tools []any) map[string]bool {
	names := make(map[string]bool)
	for _, t := range tools {
		m, _ := t.(map[string]any)
		if n, ok := m["name"].(string); ok {
			names[n] = true
		}
	}
	return names
}
