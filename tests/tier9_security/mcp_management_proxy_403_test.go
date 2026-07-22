// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §25.12 gateway-proxy RBAC boundary: a
// caller invoking a gateway-owned platform-management tool receives the
// gateway's own authorization decision re-emitted verbatim, so the role
// layer is enforced gateway-side against the forwarded identity rather
// than short-circuited or elevated inside the MCP adapter.
//
// §25.12 ("Security Model") routes a gateway-owned tool through
// GatewayClient with the authenticated MCP identity forwarded as the
// gateway's Authorization/identity headers, and requires that "a
// tenant-admin calling a platform-admin tool receives the gateway's own
// 403." The MCP adapter's scope layer narrows the caller (it can drop a
// tool from reach) but never elevates: a caller who clears the scope gate
// still faces the gateway's role check on the tool's underlying endpoint.
// This test pins that the 403 originates at the gateway (on the forwarded
// role) and reaches the agent unmodified, and that the same tool succeeds
// when the forwarded role is authorized, so the denial is not a blanket
// rejection of the proxy path.
//
// spec: §25.12 (Security Model layer 3 — RBAC on the forwarded identity;
// the gateway's own 403 re-emitted verbatim through the proxy).

package tier9_security_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/gateway"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// gatewayForbiddenBody is the canonical §25.2 FORBIDDEN envelope a stub
// gateway returns when its own role layer rejects the forwarded caller.
// The test asserts this exact envelope reaches the agent verbatim, so a
// re-emission that rewrote or dropped the gateway body would be caught.
const gatewayForbiddenBody = `{"error":{"code":"FORBIDDEN","message":"platform-admin role required for pool creation"}}`

// stubRBACGateway stands in for the gateway admin API's role layer on the
// proxy path. It reads the forwarded X-Lenny-Roles identity header (the
// dev-mode identity the §25.12 bridge forwards under AllowDevHeaders) and
// returns 403 FORBIDDEN unless the forwarded role is platform-admin. It
// records whether it was reached so the test can prove the gateway is
// what denies, not the MCP adapter.
type stubRBACGateway struct {
	reached bool
	sawRole string
}

func (g *stubRBACGateway) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.reached = true
		g.sawRole = r.Header.Get("X-Lenny-Roles")
		w.Header().Set("Content-Type", "application/json")
		// The gateway re-authorizes as the forwarded identity: only a
		// platform-admin may create a pool; a tenant-admin is denied by the
		// gateway's own role layer.
		if g.sawRole != "platform-admin" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, gatewayForbiddenBody)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"name":"acme-pool","warmCount":1}`)
	})
}

// mcpServerWithStubGateway builds the production lenny-ops management
// server wired to a stub gateway on the §25.12 proxy path, behind the
// §25.4 role gate in the dev-header posture so a test can forward a
// dev-mode caller identity to the gateway.
func mcpServerWithStubGateway(t *testing.T, gwURL string) *opsserver.Server {
	t.Helper()
	client, err := gateway.NewClient(gateway.Config{
		BaseURL:           gwURL,
		PerRequestTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("build gateway proxy client: %v", err)
	}
	return opsserver.New(opsserver.Options{
		Gateway: client,
		Auth: &opsserver.AuthConfig{Options: authmw.Options{
			AllowDevHeaders: true,
			AllowDevRoles:   true,
			RequireAuth:     true,
			MultiTenant:     false,
		}},
	})
}

// spec: 25.12
// diagnosis: the §25.12 gateway-proxy RBAC boundary regressed. A caller
// invoking a gateway-owned platform-management tool
// (admin.create_pool, x-lenny-scope tools:pool:write, required-role
// platform-admin) must have its identity forwarded to the gateway, whose
// own role layer decides authorization. A tenant-admin caller must
// receive the gateway's verbatim 403 FORBIDDEN as an isError tool result;
// a platform-admin caller must be admitted. A failure means either the
// MCP adapter denied or elevated the call itself (rather than deferring to
// the gateway's decision on the forwarded identity), the caller identity
// was dropped before the gateway (so the gateway could not enforce role),
// or the gateway body was rewritten instead of re-emitted verbatim — each
// breaks the §25.12 guarantee that RBAC on a gateway-owned tool is
// enforced gateway-side against the real caller.
func TestMCPManagementGatewayProxyReEmitsForbiddenVerbatim_spec_25_12(t *testing.T) {
	gw := &stubRBACGateway{}
	stub := httptest.NewServer(gw.handler())
	defer stub.Close()

	srv := mcpServerWithStubGateway(t, stub.URL)

	// A tenant-admin caller clears the lenny-ops role gate and the MCP
	// scope gate (no scope claim narrows the maximal ceiling), so the
	// call reaches the gateway proxy. admin.create_pool is gateway-owned:
	// the forwarded tenant-admin identity is what the gateway's role layer
	// evaluates.
	tenantAdmin := map[string]string{
		"X-Lenny-User-ID":   "alice@acme.com",
		"X-Lenny-Tenant-ID": "acme",
		"X-Lenny-Roles":     "tenant-admin",
	}
	createPool := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "admin.create_pool",
			"arguments": map[string]any{"name": "acme-pool", "warmCount": 1},
		},
	}

	code, resp := postManagementRPC(t, srv, tenantAdmin, createPool)
	// The JSON-RPC transport succeeds (HTTP 200); the gateway's denial is
	// carried inside the tool result, not at the HTTP layer.
	if code != http.StatusOK {
		t.Fatalf("tenant-admin gateway-owned tools/call HTTP status = %d, want 200 (JSON-RPC envelope); resp=%v", code, resp)
	}
	if !gw.reached {
		t.Fatal("the gateway proxy was never reached; the MCP adapter decided authorization itself instead of forwarding to the gateway")
	}
	if gw.sawRole != "tenant-admin" {
		t.Errorf("gateway saw forwarded X-Lenny-Roles = %q, want tenant-admin (the caller identity must reach the gateway for RBAC)", gw.sawRole)
	}
	result, _ := resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("tools/call carries no result object: %v", resp)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("result.isError = %v, want true (the gateway's 403 must surface as a tool error); result=%v", result["isError"], result)
	}
	// The gateway body re-emits verbatim as the tool content text.
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("result.content is empty; result=%v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if text != gatewayForbiddenBody {
		t.Errorf("gateway 403 body was not re-emitted verbatim:\n got  %q\n want %q", text, gatewayForbiddenBody)
	}
	// The re-emitted body is the gateway's own FORBIDDEN envelope; a body
	// rewritten by the MCP adapter would not parse to this code.
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decode re-emitted gateway body: %v; text=%s", err, text)
	}
	if env.Error.Code != "FORBIDDEN" {
		t.Errorf("re-emitted error code = %q, want FORBIDDEN (the gateway's own role-layer denial)", env.Error.Code)
	}

	// Positive control: the identical tool with a platform-admin identity
	// clears the gateway's role layer, so the proxy returns success. This
	// proves the 403 above came from the gateway's decision on the
	// forwarded role, not a blanket rejection of the gateway-owned path,
	// and that the MCP adapter never elevated or blocked the call itself.
	gw.reached = false
	platformAdmin := map[string]string{
		"X-Lenny-User-ID":   "root@acme.com",
		"X-Lenny-Tenant-ID": "acme",
		"X-Lenny-Roles":     "platform-admin",
	}
	code, resp = postManagementRPC(t, srv, platformAdmin, createPool)
	if code != http.StatusOK {
		t.Fatalf("platform-admin gateway-owned tools/call HTTP status = %d, want 200; resp=%v", code, resp)
	}
	if !gw.reached {
		t.Fatal("the gateway proxy was not reached for the platform-admin control")
	}
	result, _ = resp["result"].(map[string]any)
	if result == nil {
		t.Fatalf("platform-admin tools/call carries no result object: %v", resp)
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Errorf("result.isError = %v, want false (a platform-admin clears the gateway role layer); result=%v", result["isError"], result)
	}
}
