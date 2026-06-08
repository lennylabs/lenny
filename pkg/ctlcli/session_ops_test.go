// SPDX-License-Identifier: MIT

package ctlcli

import (
	"bytes"
	"net/http"
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

// --- F-24.15.2: operations list / get ---------------------------------

func TestOperationsListTargetsGateway(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"operations":[]}`,
		"operations", "list", "--status", "in_progress", "--limit", "10")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/operations" {
		t.Errorf("request: %s %s, want GET /v1/admin/operations", got.method, got.path)
	}
	if !strings.Contains(got.query, "status=in_progress") || !strings.Contains(got.query, "limit=10") {
		t.Errorf("query = %q, want status + limit filters", got.query)
	}
}

func TestOperationsGet(t *testing.T) {
	code, got := runAgainstGateway(t, http.StatusOK, `{"id":"op_1"}`,
		"operations", "get", "op_1")
	if code != 0 {
		t.Fatalf("exit code: got %d, want 0", code)
	}
	if got.method != http.MethodGet || got.path != "/v1/admin/operations/op_1" {
		t.Errorf("request: %s %s, want GET /v1/admin/operations/op_1", got.method, got.path)
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

func TestMCPManagementCall(t *testing.T) {
	code, got := runAgainstOps(t, http.StatusOK, `{"jsonrpc":"2.0","id":1,"result":{}}`,
		"mcp-management", "call", "lenny_get_pool", "--params", `{"name":"pool-a"}`)
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

func TestMCPManagementCallRequiresTool(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "mcp-management", "call"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("mcp-management call without <tool>: exit %d, want 2", code)
	}
}

func TestMCPManagementCallRejectsBadParams(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--ops-server", "http://ops:8090", "mcp-management", "call", "t", "--params", "not-json"}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("mcp-management call with bad --params: exit %d, want 2", code)
	}
}
