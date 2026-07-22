// SPDX-License-Identifier: MIT

package opsserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/common/scopes"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/mcp"
)

// bridgeRPCError extracts the JSON-RPC error code and the nested data
// code from an MCP response body, or reports that no error was present.
type bridgeRPCError struct {
	Error *struct {
		Code int `json:"code"`
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	} `json:"error"`
}

// mustScopes parses a §25.1 scope claim for a test or fails.
func mustScopes(t *testing.T, claim string) scopes.Set {
	t.Helper()
	set, err := scopes.Parse(claim)
	if err != nil {
		t.Fatalf("scopes.Parse(%q): %v", claim, err)
	}
	return set
}

// callToolThroughBridge drives a tools/call for name against a bridge
// wrapping a nil-invoker MCP server and returns the decoded JSON-RPC
// error (if any). A nil invoker returns ENDPOINT_UNAVAILABLE (-32000)
// once a call passes the scope gate, so the presence of a SCOPE_FORBIDDEN
// (-32001) error distinguishes a pre-dispatch scope rejection from a call
// that reached the (unavailable) dispatch path.
func callToolThroughBridge(t *testing.T, ctx context.Context, allowDevHeaders bool, headers http.Header, name string) bridgeRPCError {
	t.Helper()
	bridge := newMCPBridge(mcp.NewServer(nil), allowDevHeaders)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name},
	})
	r := httptest.NewRequest(http.MethodPost, "/mcp/management", bytes.NewReader(body)).WithContext(ctx)
	for k, vs := range headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	bridge.ServeHTTP(rec, r)
	var out bridgeRPCError
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	return out
}

// TestMCPBridgeForwardsJWTScopeClaimToBoundaryGate pins T-STD.8: the
// bridge attaches the authenticated Principal.Scopes onto the MCP request
// context, so a Bearer-JWT caller whose scope claim excludes the tool is
// denied SCOPE_FORBIDDEN at the pre-dispatch MCP-boundary gate. Against
// the pre-bridge code the JWT claim never reached the gate (which read
// only the X-Lenny-Scope header), so the nil-invoker path would return
// ENDPOINT_UNAVAILABLE rather than SCOPE_FORBIDDEN, and this assertion
// would fail.
//
// spec: §25.12 (Security Model layer 2; RFC 9068 space-separated scope
// claim; 403 SCOPE_FORBIDDEN before any REST call is issued).
func TestMCPBridgeForwardsJWTScopeClaimToBoundaryGate(t *testing.T) {
	// Out-of-scope JWT: scoped to diagnostics read, invoking a locks write.
	outOfScope := authmw.WithPrincipal(context.Background(), authmw.Principal{
		Scopes: mustScopes(t, "tools:diagnostics:read"),
	})
	got := callToolThroughBridge(t, outOfScope, false, nil, "lenny_lock_acquire")
	if got.Error == nil || got.Error.Code != -32001 || got.Error.Data.Code != "SCOPE_FORBIDDEN" {
		t.Fatalf("out-of-scope JWT: got %+v, want -32001 SCOPE_FORBIDDEN", got.Error)
	}

	// In-scope JWT: the same claim now includes the tool's scope, so the
	// gate passes and the nil invoker reports ENDPOINT_UNAVAILABLE. This
	// confirms the bridged claim narrows only the out-of-scope call.
	inScope := authmw.WithPrincipal(context.Background(), authmw.Principal{
		Scopes: mustScopes(t, "tools:locks:write"),
	})
	got = callToolThroughBridge(t, inScope, false, nil, "lenny_lock_acquire")
	if got.Error == nil || got.Error.Code != -32000 {
		t.Fatalf("in-scope JWT: got %+v, want -32000 ENDPOINT_UNAVAILABLE", got.Error)
	}
}

// TestMCPBridgeDevPostureFallsBackToScopeHeader confirms the bridge
// attaches a context scope set only when the Principal's scope claim is
// present. The AllowDevHeaders posture attaches a Principal with a zero
// Scopes set; the bridge must not stamp that empty set onto the context,
// so requestScopes still reads the X-Lenny-Scope dev header. A restricting
// header therefore still yields SCOPE_FORBIDDEN for an out-of-scope tool.
// Had the bridge stamped the empty set, requestScopes would short-circuit
// on the present-but-empty context value and apply no narrowing, letting
// the call fall through to ENDPOINT_UNAVAILABLE.
//
// spec: §25.12 (Security Model layer 2), §25.1 (absent-claim semantics).
func TestMCPBridgeDevPostureFallsBackToScopeHeader(t *testing.T) {
	devPrincipal := authmw.WithPrincipal(context.Background(), authmw.Principal{
		TenantID: "acme",
		// Scopes deliberately zero: the dev-header auth path never sets it.
	})
	headers := http.Header{mcp.ScopeHeader: []string{"tools:diagnostics:read"}}
	got := callToolThroughBridge(t, devPrincipal, true, headers, "lenny_lock_acquire")
	if got.Error == nil || got.Error.Code != -32001 || got.Error.Data.Code != "SCOPE_FORBIDDEN" {
		t.Fatalf("dev-header fallback: got %+v, want -32001 SCOPE_FORBIDDEN", got.Error)
	}
}

// TestMCPBridgeAbsentClaimAppliesNoNarrowing confirms that an absent scope
// claim (no Principal scopes and no X-Lenny-Scope header) applies no
// narrowing, so the call reaches the dispatch path (nil invoker →
// ENDPOINT_UNAVAILABLE) rather than being rejected.
//
// spec: §25.1 (absent-claim semantics: the token's role ceiling applies
// unmodified).
func TestMCPBridgeAbsentClaimAppliesNoNarrowing(t *testing.T) {
	got := callToolThroughBridge(t, context.Background(), false, nil, "lenny_lock_acquire")
	if got.Error == nil || got.Error.Code != -32000 {
		t.Fatalf("absent claim: got %+v, want -32000 ENDPOINT_UNAVAILABLE", got.Error)
	}
}

// TestMCPBridgeCapturesCallerIdentity pins the caller-identity carrier the
// gateway proxy reads with MCPCallerIdentityHeaders. A present
// Authorization bearer takes precedence and is forwarded alone; absent a
// bearer, the dev-mode identity headers are forwarded only under
// AllowDevHeaders (fail closed otherwise), and the singular X-Lenny-Role
// and X-Lenny-Scope headers the gateway auth layer never reads are
// excluded.
//
// spec: §25.12 (Headers and Correlation; the authenticated identity maps
// to the Authorization header on gateway calls).
func TestMCPBridgeCapturesCallerIdentity(t *testing.T) {
	tests := []struct {
		name            string
		allowDevHeaders bool
		reqHeaders      http.Header
		want            http.Header
	}{
		{
			name:            "bearer takes precedence over dev headers",
			allowDevHeaders: true,
			reqHeaders: http.Header{
				"Authorization":     []string{"Bearer tok"},
				"X-Lenny-Tenant-ID": []string{"acme"},
			},
			want: http.Header{"Authorization": []string{"Bearer tok"}},
		},
		{
			name:            "dev headers forwarded under AllowDevHeaders",
			allowDevHeaders: true,
			reqHeaders: http.Header{
				"X-Lenny-Tenant-ID": []string{"acme"},
				"X-Lenny-Roles":     []string{"tenant-admin"},
				"X-Lenny-Groups":    []string{"eng"},
			},
			want: http.Header{
				"X-Lenny-Tenant-ID": []string{"acme"},
				"X-Lenny-Roles":     []string{"tenant-admin"},
				"X-Lenny-Groups":    []string{"eng"},
			},
		},
		{
			name:            "dev headers dropped when AllowDevHeaders is false",
			allowDevHeaders: false,
			reqHeaders: http.Header{
				"X-Lenny-Tenant-ID": []string{"acme"},
				"X-Lenny-Roles":     []string{"tenant-admin"},
			},
			want: http.Header{},
		},
		{
			name:            "singular role and scope headers are excluded",
			allowDevHeaders: true,
			reqHeaders: http.Header{
				"X-Lenny-Tenant-ID": []string{"acme"},
				"X-Lenny-Role":      []string{"tenant-admin"},
				"X-Lenny-Scope":     []string{"tools:health:read"},
			},
			want: http.Header{"X-Lenny-Tenant-ID": []string{"acme"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var captured http.Header
			inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				captured = MCPCallerIdentityHeaders(r.Context())
			})
			bridge := newMCPBridge(inner, tc.allowDevHeaders)
			r := httptest.NewRequest(http.MethodPost, "/mcp/management", nil)
			for k, vs := range tc.reqHeaders {
				for _, v := range vs {
					r.Header.Add(k, v)
				}
			}
			bridge.ServeHTTP(httptest.NewRecorder(), r)
			if captured == nil {
				t.Fatal("MCPCallerIdentityHeaders returned nil; bridge did not stash identity")
			}
			if len(captured) != len(tc.want) {
				t.Fatalf("forwarded headers = %v, want %v", captured, tc.want)
			}
			for k, wantVals := range tc.want {
				if got := captured.Get(k); got != wantVals[0] {
					t.Errorf("header %s = %q, want %q", k, got, wantVals[0])
				}
			}
		})
	}
}
