// SPDX-License-Identifier: MIT

//go:build contract

package ops_endpoints_test

import (
	"net/http"
	"testing"
)

// diagnosticReadPaths are the four §25.6 diagnostic REST endpoints, each
// of which the OpenAPI document maps to the tools:diagnostics:read scope
// (the same x-lenny-scope the MCP tools carry).
var diagnosticReadPaths = []struct {
	name string
	path string
}{
	{"session", "/v1/admin/diagnostics/sessions/sess-known"},
	{"pool", "/v1/admin/diagnostics/pools/default-gvisor"},
	{"credential-pool", "/v1/admin/diagnostics/credential-pools/anthropic"},
	{"connectivity", "/v1/admin/diagnostics/connectivity"},
}

// TestDiagnosticsRESTScopeForbidden confirms the §25.1 enforcement-point-1
// Admin API middleware gates each §25.6 diagnostic REST endpoint on its
// x-lenny-scope: a caller whose authenticated JWT scope claim omits
// tools:diagnostics:read is rejected with 403 SCOPE_FORBIDDEN before the
// handler runs, and the response body lists the caller's active scopes.
//
// spec: §25.1 lines 89, 92-94 — "A request for a tool not permitted by
// any scope returns 403 SCOPE_FORBIDDEN with a response body listing the
// caller's active scopes"; "Scopes are enforced in three places: 1. Admin
// API middleware ... The middleware checks scopes before routing to the
// handler." §25.6 (diagnostic endpoints).
// diagnosis: A scope-narrowed token reached a diagnostic REST handler at
// its full role ceiling. Scope enforcement is a security layer — the
// Admin API middleware must reject a token lacking the endpoint's
// x-lenny-scope with 403 SCOPE_FORBIDDEN before dispatch, mirroring the
// MCP tools/call enforcement layer. A missing REST scope gate lets a
// token scoped to observation-only reads invoke diagnostics it was
// deliberately narrowed away from.
func TestDiagnosticsRESTScopeForbidden(t *testing.T) {
	srv, signer := opsServerWithAuth(t)
	// The caller's JWT scope claim covers health reads only; it omits
	// tools:diagnostics:read, so every diagnostic endpoint is out of scope.
	const narrowedScope = "tools:health:read"
	bearer := "Bearer " + mintScopedToken(t, signer, narrowedScope)

	for _, tc := range diagnosticReadPaths {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := request(t, srv, http.MethodGet, tc.path,
				map[string]string{"Authorization": bearer}, nil)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 SCOPE_FORBIDDEN; body=%v", rec.Code, body)
			}
			env := errorEnvelope(t, body)
			if env["code"] != "SCOPE_FORBIDDEN" {
				t.Fatalf("error code = %v, want SCOPE_FORBIDDEN; body=%v", env["code"], body)
			}
			details, ok := env["details"].(map[string]any)
			if !ok {
				t.Fatalf("SCOPE_FORBIDDEN envelope carries no details object: %v", env)
			}
			if details["requiredScope"] != "tools:diagnostics:read" {
				t.Errorf("details.requiredScope = %v, want tools:diagnostics:read", details["requiredScope"])
			}
			// §25.1 line 89: the body lists the caller's active scopes.
			if details["activeScope"] != narrowedScope {
				t.Errorf("details.activeScope = %v, want the caller's claim %q", details["activeScope"], narrowedScope)
			}
		})
	}
}

// TestDiagnosticsRESTScopePermitted is the positive control: a caller
// whose JWT scope claim includes tools:diagnostics:read passes the §25.1
// Admin API scope gate and reaches the §25.6 diagnostic handler, so the
// gate narrows the surface without blocking an in-scope caller.
//
// spec: §25.1 lines 90, 92-94 — an in-scope claim defers to the role
// ceiling; §25.6 (diagnostic endpoints).
// diagnosis: The scope gate wrongly rejected a token that carries the
// endpoint's x-lenny-scope. A gate that forbids an in-scope caller breaks
// every scoped watchdog agent, so the negative test alone is insufficient
// evidence that enforcement is correct.
func TestDiagnosticsRESTScopePermitted(t *testing.T) {
	srv, signer := opsServerWithAuth(t)
	bearer := "Bearer " + mintScopedToken(t, signer, "tools:health:read tools:diagnostics:read")

	// opsServerWithAuth seeds the default-gvisor pool, so an in-scope
	// caller reaches the handler and receives a diagnosis rather than a
	// scope rejection.
	rec, body := request(t, srv, http.MethodGet, "/v1/admin/diagnostics/pools/default-gvisor",
		map[string]string{"Authorization": bearer}, nil)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("in-scope caller was rejected with 403; body=%v", body)
	}
}
