// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/escalation"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
	"github.com/lennylabs/lenny/pkg/ops/runbooks"
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

// TestMCPRunbooksListExposesNoOptionalFilters pins the §25.12 / §15.2.1
// rule-4 contract for the auto-generated lenny_runbooks_list tool: the tool's
// advertised inputSchema is the strict structural subset of the REST request
// the openapi-to-mcp step produces, so the §25.7 Path A optional runbook
// filters (alert, component, tag, requires, q) are NOT advertised as tool
// arguments — the schema carries only the operationId correlation field and
// requires nothing. The filters' canonical discovery surface is the OpenAPI
// document (§25.12 "or /v1/openapi.json for the full surface"), and they stay
// invokable through the tool (asserted by the companion test below), so their
// omission from tools/list does not narrow what an MCP-first agent can do.
//
// diagnosis: the openapi-to-mcp generator began advertising optional query
// parameters on a list tool's inputSchema (or dropped operationId). The
// §15.2.1 strict-subset generation drifted; reconcile the mcpschemagen
// transform and regenerate the §25.12 inventory rather than accepting the
// change here.
//
// spec: 15.2.1 rule 4 (MCP tool schemas generated from the OpenAPI
// request/response definitions, structural consistency by construction),
// 25.7 (Path A alert/component/tag/requires/q optional combinable runbook
// filters), 25.12 (auto-generated tool inventory; OpenAPI is the full-surface
// discovery document).
func TestMCPRunbooksListExposesNoOptionalFilters_spec_25_12(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	_, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	})
	result, _ := body["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	var schema map[string]any
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool["name"] == "lenny_runbooks_list" {
			schema, _ = tool["inputSchema"].(map[string]any)
			break
		}
	}
	if schema == nil {
		t.Fatal("lenny_runbooks_list absent from tools/list, or it carries no inputSchema")
	}
	props, _ := schema["properties"].(map[string]any)
	for _, filter := range []string{"alert", "component", "tag", "requires", "q"} {
		if _, present := props[filter]; present {
			t.Errorf("§25.7 optional filter %q must not be advertised on the lenny_runbooks_list inputSchema (strict structural subset, §15.2.1 rule 4); properties=%v", filter, props)
		}
	}
	if _, ok := props["operationId"]; !ok {
		t.Errorf("lenny_runbooks_list inputSchema must carry the operationId correlation field; properties=%v", props)
	}
	// §25.7: "When no filters are provided, the endpoint returns all
	// runbooks" — the tool therefore requires no arguments.
	if req, ok := schema["required"].([]any); ok && len(req) != 0 {
		t.Errorf("lenny_runbooks_list must require no arguments; required=%v", req)
	}
}

// TestMCPRunbooksListForwardsOptionalFilter confirms the §25.12 guarantee that
// "an MCP-first agent can do anything a REST caller can": although
// lenny_runbooks_list does not advertise the §25.7 Path A filters in its
// inputSchema, a tools/call that supplies one is forwarded to the runbook
// index as the matching query parameter, so the MCP surface can still narrow
// the index. This is why the strict-subset omission pinned above is not a loss
// of agent capability.
//
// diagnosis: the §25.12 MCP invoker dropped an unadvertised query argument
// instead of forwarding it, so an MCP agent that learned a §25.7 filter from
// the OpenAPI document can no longer apply it and the omission from tools/list
// becomes a real capability loss.
//
// spec: 25.12 (an MCP-first agent can do anything a REST caller can; MCP tool
// invocation translated into the mapped REST call), 25.7 (Path A component
// filter narrows the runbook index).
func TestMCPRunbooksListForwardsOptionalFilter_spec_25_12(t *testing.T) {
	src := fakeRunbookSource{books: []opsserver.Runbook{
		{Name: "warm-pool-exhaustion", FrontMatter: runbooks.FrontMatter{
			Title: "Warm Pool Exhaustion", Components: []string{"warmPools"},
		}},
		{Name: "postgres-failover", FrontMatter: runbooks.FrontMatter{
			Title: "Postgres Failover", Components: []string{"postgres"},
		}},
	}}
	srv := opsserver.New(opsserver.Options{Runbooks: src})
	_, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny_runbooks_list",
			"arguments": map[string]any{"component": "warmPools"},
		},
	})
	result, _ := body["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("isError = %v, want false; body=%v", result["isError"], body)
	}
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("tools/call returned no content")
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var list struct {
		Runbooks []struct {
			Name string `json:"name"`
		} `json:"runbooks"`
	}
	if err := json.Unmarshal([]byte(text), &list); err != nil {
		t.Fatalf("decode tool result %q: %v", text, err)
	}
	if len(list.Runbooks) != 1 || list.Runbooks[0].Name != "warm-pool-exhaustion" {
		t.Errorf("component=warmPools filter through MCP returned %+v, want only warm-pool-exhaustion", list.Runbooks)
	}
}

// TestMCPManagementGatewayOwnedToolWithoutGatewayReportsUnavailable pins
// the §25.12 transparent dual-routing branch on the public tools/call
// surface: a gateway-owned tool (absent from RouteSchemas(), for example
// admin.create_pool) is proxied to the gateway rather than replayed
// locally. With no gateway client wired (the local-only dev posture that
// opsserver.New builds by default), the proxy has no target, so the tool
// maps to the -32000 ENDPOINT_UNAVAILABLE error and stays listed.
//
// This is the corrected outcome. Before the routing branch existed the
// invoker replayed every tool locally, so admin.create_pool 404'd against
// the lenny-ops mux and came back as an isError tool result with a nil
// JSON-RPC error, never reaching this -32000. A pre-fix invoker fails this
// assertion.
//
// diagnosis: a gateway-owned tool that returns a normal result (or a
// 404-shaped isError) instead of -32000 means the ops-vs-gateway routing
// branch is not firing, so gateway-owned tools are being replayed against
// the lenny-ops mux that does not serve them.
//
// spec: §25.12 (transparent dual routing; a nil gateway makes a
// gateway-owned tool report ENDPOINT_UNAVAILABLE).
func TestMCPManagementGatewayOwnedToolReportsUnavailable_spec_25_12(t *testing.T) {
	srv := opsserver.New(opsserver.Options{})
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "admin.create_pool",
			"arguments": map[string]any{"name": "gvisor-a", "runtime": "gvisor"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JSON-RPC carries the error in the body)", rec.Code)
	}
	rpcErr, _ := body["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32000 {
		t.Fatalf("error = %v, want -32000 ENDPOINT_UNAVAILABLE for a gateway-owned tool with no gateway wired; a nil error means the tool was replayed locally instead of routed to the gateway", rpcErr)
	}
}

// TestMCPManagementGatewayOwnedToolThreadsGatewayAndBridge pins the
// §25.12 WIRE assembly the direct-construction invoker units do not reach:
// that opsserver.New threads Options.Gateway into the MCP invoker and
// mounts the MCP server behind the scope-bridge on the live /mcp/management
// route. It drives two invocations through the mounted route on a server
// built with a real gateway client and the §25.4 auth gate:
//
//  1. An authorized platform-admin invoking the gateway-owned admin.create_pool
//     reaches the stub gateway with the caller Authorization forwarded
//     verbatim. A pre-wire opsserver.New passed a nil gateway argument, so the
//     invoker reported ENDPOINT_UNAVAILABLE and the stub was never hit; the
//     forwarded bearer proves Options.Gateway threads through to the invoker.
//  2. A platform-admin whose JWT scope claim is narrowed to a domain that does
//     not grant the gateway-owned mutation is denied SCOPE_FORBIDDEN before
//     dispatch, and the stub gateway is never reached. This proves
//     newMCPBridge sits in the mounted path: without the bridge the JWT scope
//     claim never reaches the MCP-boundary gate and the call would fall
//     through to the gateway instead of being rejected pre-dispatch.
//
// diagnosis: a gateway-owned tool that returns ENDPOINT_UNAVAILABLE for an
// authorized caller means opsserver.New did not thread Options.Gateway into
// the invoker; a narrowed-scope caller that reaches the gateway instead of
// being denied SCOPE_FORBIDDEN means newMCPBridge is not mounted on the
// /mcp/management route.
//
// spec: §25.12 (ManagementMCPAdapter dual routing; Security Model layers 2
// and 3; the gateway-proxy client threaded through and the MCP server
// mounted behind the scope bridge).
func TestMCPManagementGatewayOwnedToolThreadsGatewayAndBridge_spec_25_12(t *testing.T) {
	var gotAuth, gotMethod, gotPath string
	var gotBody []byte
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"pool-1"}`))
	}))
	defer stub.Close()

	gw, err := gateway.NewClient(gateway.Config{BaseURL: stub.URL, PerRequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("build gateway client: %v", err)
	}
	signer := jwt.NewHMACSigner("ops-test", []byte("ops-test-secret"))
	srv := opsserver.New(opsserver.Options{
		Gateway: gw,
		Auth: &opsserver.AuthConfig{
			Options:     authmw.Options{Verifier: signer},
			RateLimiter: opsserver.NewRateLimiter(1000, 1000),
		},
	})

	// (1) An authorized platform-admin carrying no scope narrowing invokes
	// the gateway-owned tool; the call must reach the stub gateway with the
	// caller bearer forwarded.
	adminTok := mintOpsToken(t, signer, "alice@acme.com", auth.RolePlatformAdmin)
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management",
		map[string]string{"Authorization": "Bearer " + adminTok},
		map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{
				"name":      "admin.create_pool",
				"arguments": map[string]any{"name": "gvisor-a", "runtime": "gvisor"},
			},
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if rpcErr, ok := body["error"].(map[string]any); ok {
		t.Fatalf("tools/call returned a JSON-RPC error for an authorized gateway-owned call: %v", rpcErr)
	}
	if gotAuth != "Bearer "+adminTok {
		t.Errorf("stub gateway saw Authorization %q, want the forwarded caller bearer; Options.Gateway did not thread to the invoker", gotAuth)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/admin/pools" {
		t.Errorf("stub gateway saw %s %s, want POST /v1/admin/pools", gotMethod, gotPath)
	}
	if string(gotBody) != `{"name":"gvisor-a","runtime":"gvisor"}` {
		t.Errorf("stub gateway saw body %q, want the tool arguments", gotBody)
	}

	// (2) A platform-admin whose scope claim is narrowed to a domain that
	// does not grant tools:pool:write must be denied SCOPE_FORBIDDEN before
	// dispatch, and the stub gateway must not be reached.
	gotAuth, gotMethod, gotPath = "", "", ""
	narrowTok := mintOpsTokenWithScope(t, signer, "alice@acme.com", "tools:diagnostics:read", auth.RolePlatformAdmin)
	rec, body = doJSON(t, srv, http.MethodPost, "/mcp/management",
		map[string]string{"Authorization": "Bearer " + narrowTok},
		map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{
				"name":      "admin.create_pool",
				"arguments": map[string]any{"name": "gvisor-a", "runtime": "gvisor"},
			},
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (JSON-RPC carries the error in the body); body=%v", rec.Code, body)
	}
	rpcErr, _ := body["error"].(map[string]any)
	if rpcErr == nil || rpcErr["code"].(float64) != -32001 {
		t.Fatalf("error = %v, want -32001 SCOPE_FORBIDDEN for a narrowed-scope caller; the MCP-boundary scope bridge is not on the mounted route", rpcErr)
	}
	data, _ := rpcErr["data"].(map[string]any)
	if data == nil || data["code"] != "SCOPE_FORBIDDEN" {
		t.Fatalf("error data = %v, want SCOPE_FORBIDDEN", data)
	}
	if gotAuth != "" || gotPath != "" {
		t.Errorf("stub gateway was reached (auth=%q path=%q) for a scope-denied call; the rejection must be pre-dispatch", gotAuth, gotPath)
	}
}

// mintOpsTokenWithScope signs a bearer carrying sub, an RFC 9068 scope
// claim, and the given roles, so a test can exercise the §25.1 scope
// narrowing the MCP-boundary gate enforces.
func mintOpsTokenWithScope(t *testing.T, signer *jwt.HMACSigner, sub, scope string, roles ...auth.Role) string {
	t.Helper()
	tok, err := signer.Sign(jwt.Claims{Subject: sub, TenantID: "default", Roles: roles, Scope: scope})
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
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
