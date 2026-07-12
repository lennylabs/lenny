// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §25.12 MCP Management Server
// authorization sweeps.
//
// §25.12 ("Security Model") mandates two CI sweeps over the entire
// management-tool inventory:
//
//	The CI suite includes two tests:
//	- Iterates every MCP tool, attempts to call it with a token carrying
//	  no role, asserts 403.
//	- Iterates every MCP tool, attempts to call it with a scope-restricted
//	  token that excludes the tool, asserts 403 SCOPE_FORBIDDEN.
//	These catch endpoints that accidentally skip authorization at
//	implementation time.
//
// The existing single-tool coverage (pkg/ops/mcp asserts SCOPE_FORBIDDEN
// for one tool; pkg/gateway/externalapi/admin asserts the per-route scope
// gate for a handful of routes) does not range over the registry, so a
// newly generated tool that skips its scope or role gate passes today. The
// two sweeps below close that gap by iterating mcp.NewRegistry().All().
//
//   - The scope sweep drives the in-process §25.12 MCP server (the scope
//     layer, §25.12 layer 2): a present scope claim that excludes the tool
//     is rejected -32001 SCOPE_FORBIDDEN before any REST call is issued.
//   - The no-role sweep drives the production lenny-ops management server
//     with its §25.4 role gate wired: a call carrying an authenticated
//     token with no role is rejected 403 by the role layer (§25.12 layer 3
//     as realized by §25.4's "requires platform-admin or tenant-admin role
//     on all endpoints"). §25.4's "any authenticated caller may call
//     /v1/admin/me" governs the direct REST endpoint, not tool invocation
//     through /mcp/management, which requires the management role uniformly.
//
// spec: §25.12 (the two CI authorization sweeps over every MCP tool),
// §25.4 (lenny-ops requires platform-admin or tenant-admin on every
// management route; no anonymous access except the probes).

package tier9_security_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// sweepRecordingInvoker records every tool the §25.12 dispatch loop asks
// it to run. The scope sweep asserts it stays empty: the scope layer must
// reject a scope-excluded call before the invoker is reached.
type sweepRecordingInvoker struct{ calls []string }

func (r *sweepRecordingInvoker) Invoke(tool mcp.Tool, _ json.RawMessage) (mcp.ToolResult, error) {
	r.calls = append(r.calls, tool.Name)
	return mcp.ToolResult{Status: http.StatusOK, Body: json.RawMessage(`{}`)}, nil
}

// excludingScope returns a valid, present §25.1 scope claim that does not
// grant toolScope, so the §25.12 scope layer must reject a call to a tool
// carrying toolScope. tools:operations:read and tools:me:read are both
// canonical scopes; a specific (non-wildcard) granted scope matches only
// its exact self per §25.1, so whichever of the two differs from the
// tool's own scope excludes it. No registry tool carries a wildcard-action
// scope, so a single specific granted scope cannot accidentally match.
func excludingScope(toolScope string) string {
	const a = "tools:operations:read"
	const b = "tools:me:read"
	if toolScope == a {
		return b
	}
	return a
}

// postManagementRPC posts a JSON-RPC request to the given handler at
// /mcp/management with the supplied headers and returns the HTTP status
// and the decoded response body.
func postManagementRPC(t *testing.T, h http.Handler, headers map[string]string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp/management", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec.Code, out
}

// toolsCallBody builds a §25.12 tools/call JSON-RPC request for the named
// tool with empty arguments (the authorization layers run before any
// argument-schema validation).
func toolsCallBody(name string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": map[string]any{}},
	}
}

// spec: 25.12
// diagnosis: the §25.12 scope sweep failed for the named tool. Every MCP
// management tool, called with a present scope claim that excludes it,
// must be rejected -32001 SCOPE_FORBIDDEN by the MCP adapter's scope layer
// before any REST call is issued (§25.12 layer 2). A tool that slips
// through — because its generated x-lenny-scope is empty, malformed, or
// the central scope gate regressed — reaches the invoker and would issue
// an unauthorized REST call. A failure here is exactly the "endpoint that
// accidentally skips authorization" the §25.12 CI sweep exists to catch.
func TestMCPManagementScopeForbiddenSweep_spec_25_12(t *testing.T) {
	inv := &sweepRecordingInvoker{}
	srv := mcp.NewServer(inv)

	tools := mcp.NewRegistry().All()
	if len(tools) == 0 {
		t.Fatal("the §25.12 management-tool registry is empty; the sweep exercised nothing")
	}

	for _, tool := range tools {
		grant := excludingScope(tool.Scope)
		if grant == tool.Scope {
			t.Fatalf("%s: excluding scope %q equals the tool scope; the sweep would not exercise a rejection", tool.Name, grant)
		}
		_, resp := postManagementRPC(t, srv, map[string]string{mcp.ScopeHeader: grant}, toolsCallBody(tool.Name))

		rpcErr, ok := resp["error"].(map[string]any)
		if !ok || rpcErr == nil {
			t.Errorf("%s: scope-excluded call returned no JSON-RPC error (scope layer did not reject); resp=%v", tool.Name, resp)
			continue
		}
		if code, _ := rpcErr["code"].(float64); code != -32001 {
			t.Errorf("%s: error code = %v, want -32001 SCOPE_FORBIDDEN", tool.Name, rpcErr["code"])
			continue
		}
		data, _ := rpcErr["data"].(map[string]any)
		if data["code"] != "SCOPE_FORBIDDEN" {
			t.Errorf("%s: data.code = %v, want SCOPE_FORBIDDEN", tool.Name, data["code"])
		}
		// §25.2: a scope rejection carries the AUTH category and the
		// required scope so the caller can self-correct.
		if data["category"] != "AUTH" {
			t.Errorf("%s: data.category = %v, want AUTH", tool.Name, data["category"])
		}
		if data["requiredScope"] != tool.Scope {
			t.Errorf("%s: data.requiredScope = %v, want the tool scope %q", tool.Name, data["requiredScope"], tool.Scope)
		}
	}

	// The scope layer runs before dispatch, so a scope-excluded call must
	// never reach the invoker.
	if len(inv.calls) != 0 {
		t.Errorf("the scope layer let %d call(s) reach the invoker despite an excluding scope claim: %v", len(inv.calls), inv.calls)
	}
}

// spec: 25.12, 25.4
// diagnosis: the §25.12 no-role sweep failed for the named tool. Every MCP
// management tool, invoked through the production lenny-ops management
// server (/mcp/management) by an authenticated token that carries no role,
// must be rejected 403 by the §25.4 role gate (§25.12 layer 3: "requires
// platform-admin or tenant-admin role on all endpoints. No anonymous
// access except /healthz."). A non-403 (200 dispatch, or a 401
// authentication failure) means either the role gate was skipped in front
// of the management server or the caller was not authenticated; both break
// the §25.12 guarantee that a role-less token cannot invoke a management
// tool. The positive control confirms a platform-admin caller passes the
// gate, so a blanket rejection is not masking a broken gate.
func TestMCPManagementNoRoleForbiddenSweep_spec_25_12_25_4(t *testing.T) {
	// Wire the §25.4 role gate in front of the management server via the
	// dev-header auth path: an authenticated request carrying no
	// X-Lenny-Roles yields a principal with no role. Single-tenant mode
	// admits the dev-header request without a tenant header.
	srv := opsserver.New(opsserver.Options{
		Auth: &opsserver.AuthConfig{Options: authmw.Options{
			AllowDevHeaders: true,
			AllowDevRoles:   true,
			RequireAuth:     true,
			MultiTenant:     false,
		}},
	})

	noRole := map[string]string{"X-Lenny-User-ID": "alice@acme.com"}

	tools := mcp.NewRegistry().All()
	if len(tools) == 0 {
		t.Fatal("the §25.12 management-tool registry is empty; the sweep exercised nothing")
	}

	for _, tool := range tools {
		code, resp := postManagementRPC(t, srv, noRole, toolsCallBody(tool.Name))
		if code != http.StatusForbidden {
			t.Errorf("%s: no-role tools/call HTTP status = %d, want 403; resp=%v", tool.Name, code, resp)
			continue
		}
		// The §25.4 role gate writes the canonical FORBIDDEN envelope; a
		// 403 UNAUTHORIZED/other code would signal a different rejection.
		errObj, _ := resp["error"].(map[string]any)
		if errObj == nil || errObj["code"] != "FORBIDDEN" {
			t.Errorf("%s: no-role 403 error code = %v, want FORBIDDEN (the §25.4 role gate)", tool.Name, resp["error"])
		}
	}

	// Positive control: a platform-admin caller clears the §25.4 role gate,
	// so the transport gate does not reject and the request reaches the
	// management server (the JSON-RPC envelope returns HTTP 200 regardless
	// of the tool's own outcome). This proves the sweep's 403s come from the
	// role gate, not a blanket rejection of the /mcp/management path.
	admin := map[string]string{"X-Lenny-User-ID": "alice@acme.com", "X-Lenny-Roles": "platform-admin"}
	code, resp := postManagementRPC(t, srv, admin, toolsCallBody("admin.health"))
	if code == http.StatusForbidden {
		t.Errorf("platform-admin caller was rejected by the §25.4 role gate: resp=%v", resp)
	}
}
