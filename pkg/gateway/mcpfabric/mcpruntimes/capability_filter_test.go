// SPDX-License-Identifier: MIT

package mcpruntimes_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/capabilityinference"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcpruntimes"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// executionRuntime is a type:mcp runtime tagged category:execution with
// explicit per-tool capability overrides.
func executionRuntime() runtimestore.Runtime {
	return runtimestore.Runtime{
		Name:   "code-runner",
		Type:   runtimestore.TypeMCP,
		Labels: map[string]string{"category": "execution"},
		ToolCapabilityOverrides: map[string][]capabilityinference.Capability{
			"write_file": {capabilityinference.CapWrite},
			"read_file":  {capabilityinference.CapRead},
		},
	}
}

// executionEnv denies write/delete/admin on category:execution runtimes,
// mirroring the §10.6 mcpRuntimeFilters example.
func executionEnv(t *testing.T) *environmentstore.Memory {
	t.Helper()
	envs := environmentstore.NewMemory()
	if err := envs.Create(context.Background(), environmentstore.Environment{
		Name:     "security-team",
		TenantID: "acme",
		MCPRuntimeFilters: []environmentstore.MCPRuntimeFilter{
			{
				RuntimeSelector:     environment.Selector{MatchLabels: map[string]string{"category": "execution"}},
				AllowedCapabilities: []string{"read", "execute"},
				DeniedCapabilities:  []string{"write", "delete", "admin"},
			},
		},
	}); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return envs
}

func callToolRequest(tool, query string) *http.Request {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":{}}}`
	r := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/code-runner"+query, strings.NewReader(body))
	r.SetPathValue("name", "code-runner")
	r.Header.Set("X-Lenny-Tenant-ID", "acme")
	return r
}

// TestRuntimeFilterDeniesWriteTool_spec_10_6_607 verifies an
// environment-scoped tools/call whose tool resolves to a denied
// capability is rejected with TOOL_CAPABILITY_DENIED before dispatch.
func TestRuntimeFilterDeniesWriteTool_spec_10_6_607(t *testing.T) {
	disp := &stubDispatcher{response: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	h := mcpruntimes.New(mustStore(t, executionRuntime()), disp).WithEnvironments(executionEnv(t))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, callToolRequest("write_file", "?environment=security-team"))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "TOOL_CAPABILITY_DENIED") {
		t.Fatalf("body = %q, want TOOL_CAPABILITY_DENIED", rr.Body.String())
	}
	if len(disp.calls) != 0 {
		t.Fatalf("dispatcher called %d times for a denied tool, want 0", len(disp.calls))
	}
}

// TestRuntimeFilterPermitsReadTool_spec_10_6_607 verifies an allowed tool
// dispatches normally.
func TestRuntimeFilterPermitsReadTool_spec_10_6_607(t *testing.T) {
	disp := &stubDispatcher{response: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	h := mcpruntimes.New(mustStore(t, executionRuntime()), disp).WithEnvironments(executionEnv(t))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, callToolRequest("read_file", "?environment=security-team"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(disp.calls) != 1 {
		t.Fatalf("dispatcher called %d times, want 1", len(disp.calls))
	}
}

// TestRuntimeFilterNoEnvironmentScopeUngated_spec_10_6_607 confirms a
// call that names no environment is not gated.
func TestRuntimeFilterNoEnvironmentScopeUngated_spec_10_6_607(t *testing.T) {
	disp := &stubDispatcher{response: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	h := mcpruntimes.New(mustStore(t, executionRuntime()), disp).WithEnvironments(executionEnv(t))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, callToolRequest("write_file", "")) // no ?environment=

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ungated); body=%s", rr.Code, rr.Body.String())
	}
	if len(disp.calls) != 1 {
		t.Fatalf("dispatcher called %d times, want 1", len(disp.calls))
	}
}

// TestRuntimeFilterUnmatchedRuntimeUngated_spec_10_6_607 confirms a
// runtime that no filter admits is not gated even under an environment
// scope.
func TestRuntimeFilterUnmatchedRuntimeUngated_spec_10_6_607(t *testing.T) {
	rt := executionRuntime()
	rt.Labels = map[string]string{"category": "analysis"} // not admitted by the filter
	disp := &stubDispatcher{response: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	h := mcpruntimes.New(mustStore(t, rt), disp).WithEnvironments(executionEnv(t))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, callToolRequest("write_file", "?environment=security-team"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no matching filter); body=%s", rr.Code, rr.Body.String())
	}
	if len(disp.calls) != 1 {
		t.Fatalf("dispatcher called %d times, want 1", len(disp.calls))
	}
}

// TestRuntimeFilterUnannotatedToolFailsClosed_spec_5_1 confirms a tool
// with no override and no annotations resolves to the conservative admin
// default under strict mode and is denied by a restrictive filter.
func TestRuntimeFilterUnannotatedToolFailsClosed_spec_5_1(t *testing.T) {
	disp := &stubDispatcher{response: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	h := mcpruntimes.New(mustStore(t, executionRuntime()), disp).WithEnvironments(executionEnv(t))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, callToolRequest("mystery_tool", "?environment=security-team"))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (admin default denied); body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "admin") {
		t.Fatalf("body = %q, want the blocked admin capability in the detail", rr.Body.String())
	}
	if len(disp.calls) != 0 {
		t.Fatalf("dispatcher called %d times for a denied tool, want 0", len(disp.calls))
	}
}

// TestRuntimeFilterIgnoresNonToolsCall_spec_10_6_607 confirms the gate
// applies only to tools/call; a tools/list passes through.
func TestRuntimeFilterIgnoresNonToolsCall_spec_10_6_607(t *testing.T) {
	disp := &stubDispatcher{response: []byte(`{"jsonrpc":"2.0","id":1,"result":{}}`)}
	h := mcpruntimes.New(mustStore(t, executionRuntime()), disp).WithEnvironments(executionEnv(t))

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	r := httptest.NewRequest(http.MethodPost, "/mcp/runtimes/code-runner?environment=security-team", strings.NewReader(body))
	r.SetPathValue("name", "code-runner")
	r.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if len(disp.calls) != 1 {
		t.Fatalf("dispatcher called %d times, want 1", len(disp.calls))
	}
}
