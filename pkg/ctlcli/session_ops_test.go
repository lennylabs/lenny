// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// spec: §24.11 lines 135-136 — admin sessions CLI group. §24.15 lines
// 181, 192, 193 — operations, logs, mcp-management CLI groups.
// F-24.11.1, F-24.15.2, F-24.15.8, F-24.15.9.

// --- F-24.11.1: admin sessions get / force-terminate ------------------

func TestAdminSessionsGet(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"id":"sess_1","state":"running"}`,
		"admin", "sessions", "get", "sess_1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/sessions/sess_1" {
		t.Errorf("request: %s %s, want GET /v1/admin/sessions/sess_1", got.method, got.path)
	}
}

func TestAdminSessionsGetRequiresID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "sessions", "get"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("sessions get without <id>: exit %d, want 2", code)
	}
}

func TestAdminSessionsForceTerminate(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"id":"sess_1","state":"failed"}`,
		"admin", "sessions", "force-terminate", "sess_1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/v1/admin/sessions/sess_1/force-terminate" {
		t.Errorf("request: %s %s, want POST /v1/admin/sessions/sess_1/force-terminate", got.method, got.path)
	}
}

func TestAdminSessionsForceTerminateWithReason(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"id":"sess_1","state":"failed"}`,
		"admin", "sessions", "force-terminate", "sess_1", "--reason", "stuck runtime")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.body["reason"] != "stuck runtime" {
		t.Errorf("body reason = %v, want stuck runtime", got.body["reason"])
	}
}

func TestAdminSessionsUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "admin", "sessions", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown sessions subcommand: exit %d, want 2", code)
	}
}

// --- F-24.15.2 / F-PE-2: operations list / get -------------------------

// TestOperationsListTargetsOps_spec_15_1_909 pins the operations
// inventory to lenny-ops: §15.1 line 909 assigns /v1/admin/operations to
// lenny-ops, so the CLI must resolve the ops endpoint (here via
// --ops-server, which runAgainstOps supplies) rather than the gateway.
// The gateway no longer serves the route, so a CLI that targeted it would
// hit a 404. spec: §24.15 line 181; §15.1 line 909.
func TestOperationsListTargetsOps_spec_15_1_909(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"operations":[]}`,
		"operations", "list", "--status", "in_progress", "--limit", "10")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	// runAgainstOps folds the query into got.path.
	if got.method != http.MethodGet || !strings.HasPrefix(got.path, "/v1/admin/operations") {
		t.Errorf("request: %s %s, want GET /v1/admin/operations", got.method, got.path)
	}
	if !strings.Contains(got.path, "status=in_progress") || !strings.Contains(got.path, "limit=10") {
		t.Errorf("path = %q, want status + limit filters", got.path)
	}
}

func TestOperationsGetTargetsOps_spec_15_1_909(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"id":"op_1"}`,
		"operations", "get", "op_1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/operations/op_1" {
		t.Errorf("request: %s %s, want GET /v1/admin/operations/op_1", got.method, got.path)
	}
}

// TestOperationsListReturnsOpsHostedOperation_spec_15_1_909 is the F-PE-2
// regression. The operations inventory is hosted by lenny-ops (§15.1
// line 909); the gateway no longer serves /v1/admin/operations and was
// previously wired with an empty inventory, so a list that targeted the
// gateway returned an empty page even while lenny-ops held an in-flight
// operation. This test drives `operations list` through the full §24.16
// auto-discovery path (no --ops-server): the gateway advertises the ops
// URL, lenny-ops returns one in-flight operation, and the CLI surfaces
// that operation rather than the empty page. Against the pre-fix code the
// list hit the gateway's empty inventory and would not contain op-1.
// spec: §24.15 line 181; §15.1 line 909; §24.16 line 201.
func TestOperationsListReturnsOpsHostedOperation_spec_15_1_909(t *testing.T) {
	ops := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/operations" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"operations":[{"operationId":"op-1","kind":"platform_upgrade","status":"in_progress"}]}`))
	}))
	defer ops.Close()

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/platform/version" {
			// The gateway no longer serves the operations inventory; any
			// non-version request would be a routing regression.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"gatewayVersion":"dev","opsServiceURL":"` + ops.URL + `"}`))
	}))
	defer gateway.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", gateway.URL, "operations", "list", "--status", "in_progress"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("operations list: exit code %d, want 0 (stderr %s)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"op-1"`) {
		t.Errorf("operations list did not return the ops-hosted operation; stdout=%q", stdout.String())
	}
}

func TestOperationsUnknownSubcommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", "http://127.0.0.1:0", "operations", "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("unknown operations subcommand: exit %d, want 2", code)
	}
}

// --- F-24.15.9: mcp-management tools / call ---------------------------

func TestMCPManagementTools(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`,
		"mcp-management", "tools")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodPost || got.path != "/mcp/management" {
		t.Errorf("request: %s %s, want POST /mcp/management", got.method, got.path)
	}
	if got.body["method"] != "tools/list" {
		t.Errorf("method = %v, want tools/list", got.body["method"])
	}
}

func TestMCPManagementCallRequiresTool(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "mcp-management", "call"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("mcp-management call without <tool>: exit %d, want 2", code)
	}
}

// TestMCPManagementAutoDiscoversOpsURL drives `mcp-management tools`
// through the full §24.16 auto-discovery path (no --ops-server): the
// gateway advertises the ops URL in its GET /v1/admin/platform/version
// response, and lenny-ctl routes the JSON-RPC call to /mcp/management on
// that ops server. §24.15 line 4866 lists mcp-management among the
// command groups that resolve to lenny-ops via this auto-discovery rule.
// spec: §24.15 line 4866 ("mcp-management | lenny-ops | --ops-server
// flag, or auto-discovered via GET /v1/admin/platform/version (gateway
// response includes opsServiceURL)"); §24.16 line 201 rule 2.
func TestMCPManagementAutoDiscoversOpsURL(t *testing.T) {
	opsGot := &capturedRequest{}
	ops := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opsGot.method = r.Method
		opsGot.path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer ops.Close()

	// The gateway advertises the ops URL in the version response.
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/platform/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"gatewayVersion":"dev","opsServiceURL":"` + ops.URL + `"}`))
	}))
	defer gateway.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", gateway.URL, "mcp-management", "tools"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0 (stderr %s)", code, stderr.String())
	}
	if opsGot.method != http.MethodPost || opsGot.path != "/mcp/management" {
		t.Errorf("auto-discovery did not route mcp-management to the ops server: ops saw %s %s", opsGot.method, opsGot.path)
	}
}

// TestMCPManagementFallsBackToGatewayWhenOpsURLAbsent covers the §24.16
// rule-3 fallback for the mcp-management group specifically: when the
// gateway's version response carries no opsServiceURL and --ops-server is
// not passed, lenny-ctl falls back to the gateway host rather than
// aborting, and surfaces a warning. spec: §24.16 line 201 rule 3 ("If
// auto-discovery fails (gateway unreachable, opsServiceURL absent because
// the cluster is mid-upgrade), lenny-ctl falls back to the gateway host
// under the assumption that gateway-hosted operability endpoints ...
// still work, and surfaces a warning for any ops-exclusive command.").
func TestMCPManagementFallsBackToGatewayWhenOpsURLAbsent(t *testing.T) {
	got := &capturedRequest{}
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/admin/platform/version" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"gatewayVersion":"dev"}`)) // no opsServiceURL.
			return
		}
		got.method = r.Method
		got.path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	}))
	defer gateway.Close()

	var stdout, stderr bytes.Buffer
	code := run([]string{"--api-url", gateway.URL, "mcp-management", "tools"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("fallback: exit code %d, want 0 (stderr %s)", code, stderr.String())
	}
	if got.method != http.MethodPost || got.path != "/mcp/management" {
		t.Errorf("fallback did not route mcp-management to the gateway host: gateway saw %s %s", got.method, got.path)
	}
	if !strings.Contains(stderr.String(), "WARN") || !strings.Contains(stderr.String(), "falling back to the gateway host") {
		t.Errorf("fallback should warn on stderr, got %q", stderr.String())
	}
}

// TestMCPManagementCallAcceptsArgsFlag covers the §25.14 command table's
// documented flag name for `mcp-management tools call`: the tool
// arguments are passed via --args, not --params.
// spec: §25.14 line 4984 ("lenny-ctl mcp-management tools call <name>
// --args <json>").
func TestMCPManagementCallAcceptsArgsFlag(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{}}`,
		"mcp-management", "call", "lenny_get_pool", "--args", `{"name":"pool-a"}`)
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.body["method"] != "tools/call" {
		t.Errorf("method = %v, want tools/call", got.body["method"])
	}
	params, _ := got.body["params"].(map[string]any)
	if params == nil || params["name"] != "lenny_get_pool" {
		t.Errorf("params.name = %v, want lenny_get_pool", params)
	}
	args, _ := params["arguments"].(map[string]any)
	if args == nil || args["name"] != "pool-a" {
		t.Errorf("params.arguments = %v, want {name: pool-a}", params["arguments"])
	}
}

func TestMCPManagementCallRejectsBadArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "mcp-management", "call", "t", "--args", "not-json"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("mcp-management call with bad --args: exit %d, want 2", code)
	}
}
