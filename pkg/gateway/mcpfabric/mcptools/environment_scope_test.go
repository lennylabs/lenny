// SPDX-License-Identifier: MIT

package mcptools_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcp"
)

// callEnvScoped posts a tools/call to the §10.6 explicit-environment MCP
// surface at /mcp/environments/{name} and returns the decoded response.
func callEnvScoped(t *testing.T, srv *mcp.Server, envName, tool, args string) map[string]any {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/environments/"+envName, strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.EnvironmentHandler().ServeHTTP(rr, req)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rr.Body.String())
	}
	return resp
}

// TestEnvironmentHandlerScopesCreateSession_spec_10_6_557 verifies that a
// create_session over /mcp/environments/{name} with no explicit
// environment argument scopes the session to the path environment.
func TestEnvironmentHandlerScopesCreateSession_spec_10_6_557(t *testing.T) {
	srv, store := newMCP(t)
	resp := callEnvScoped(t, srv, "security-team", "lenny/create_session", `{"runtimeRef":"echo","userId":"alice"}`)
	if text := resultText(t, resp); !strings.Contains(text, "sess_mcp") {
		t.Fatalf("create_session result: %q", text)
	}
	row, err := store.Get(context.Background(), "acme", "sess_mcp")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.Environment != "security-team" {
		t.Fatalf("session Environment = %q, want security-team (scoped by the path)", row.Environment)
	}
}

// TestEnvironmentHandlerExplicitArgWins_spec_10_6_557 verifies that an
// explicit environment argument overrides the path scope.
func TestEnvironmentHandlerExplicitArgWins_spec_10_6_557(t *testing.T) {
	srv, store := newMCP(t)
	resp := callEnvScoped(t, srv, "from-path", "lenny/create_session",
		`{"runtimeRef":"echo","userId":"alice","environment":"explicit-arg"}`)
	if text := resultText(t, resp); !strings.Contains(text, "sess_mcp") {
		t.Fatalf("create_session result: %q", text)
	}
	row, err := store.Get(context.Background(), "acme", "sess_mcp")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.Environment != "explicit-arg" {
		t.Fatalf("session Environment = %q, want explicit-arg (argument wins over path)", row.Environment)
	}
}

// TestPlainMcpHandlerNoEnvironmentScope_spec_10_6_557 confirms the plain
// /mcp surface does not inject an environment scope.
func TestPlainMcpHandlerNoEnvironmentScope_spec_10_6_557(t *testing.T) {
	srv, store := newMCP(t)
	resp := call(t, srv.Handler(), "lenny/create_session", `{"runtimeRef":"echo","userId":"alice"}`)
	if text := resultText(t, resp); !strings.Contains(text, "sess_mcp") {
		t.Fatalf("create_session result: %q", text)
	}
	row, err := store.Get(context.Background(), "acme", "sess_mcp")
	if err != nil {
		t.Fatalf("session not stored: %v", err)
	}
	if row.Environment != "" {
		t.Fatalf("session Environment = %q, want empty on the plain /mcp surface", row.Environment)
	}
}
