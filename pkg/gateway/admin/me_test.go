// SPDX-License-Identifier: MIT

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/openapi"
	"github.com/lennylabs/lenny/pkg/ops/me"
)

func TestMeReturnsPrincipal(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/me", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var p me.Identity
	_ = json.Unmarshal(rr.Body.Bytes(), &p)
	if p.Subject != "admin@acme.com" {
		t.Errorf("Subject: got %q", p.Subject)
	}
	if !contains(p.Roles, pkgauth.RolePlatformAdmin) {
		t.Errorf("Roles: missing platform-admin: %v", p.Roles)
	}
}

func TestMeRejectsAnonymous(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/me", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: got %d, want 401", rr.Code)
	}
}

func TestAuthorizedToolsForPlatformAdmin(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp admin.AuthorizedToolsPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	// Platform-admin should see every tool (including tenant-admin
	// scoped ones since platform-admin >= tenant-admin in semantic
	// breadth, but our gate checks the literal role name. Verify the
	// admin gets at least the platform-only tools.)
	want := []string{"admin.create_tenant", "admin.bootstrap", "admin.create_pool"}
	got := map[string]bool{}
	for _, t := range resp.Tools {
		got[t.Tool] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing tool %q in authorized-tools for platform-admin: %+v", w, resp.Tools)
		}
	}
}

// spec: §24.4 / §25.14 — the §24.4 pool actions that have mounted
// endpoints (warm-count, sync-status, resume-reconciliation) appear in
// the agent-discovery catalog so an AI DevOps agent can find the
// warm-pool-exhaustion and PoolScalingAdmissionStuck remediation surface.
func TestAuthorizedToolsAdvertisesMountedPoolActions_spec_24_4(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp admin.AuthorizedToolsPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	got := map[string]bool{}
	for _, tool := range resp.Tools {
		got[tool.Tool] = true
	}
	for _, w := range []string{
		"admin.set_pool_warm_count",
		"admin.pool_sync_status",
		"admin.resume_pool_reconciliation",
		"admin.grant_pool_tenant_access",
		"admin.list_pool_tenant_access",
		"admin.revoke_pool_tenant_access",
	} {
		if !got[w] {
			t.Errorf("missing pool tool %q in authorized-tools: %+v", w, resp.Tools)
		}
	}
}

func TestAuthorizedToolsForTenantAdminScopedToTenant(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp admin.AuthorizedToolsPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	// tenant-admin should not see platform-admin-only tools.
	for _, tool := range resp.Tools {
		if tool.Tool == "admin.create_tenant" || tool.Tool == "admin.bootstrap" {
			t.Errorf("tenant-admin should not see %q: %+v", tool.Tool, resp.Tools)
		}
	}
}

// TestAuthorizedToolsIncludeTenantAccess_spec_24_3 asserts the runtime
// and pool tenant-access operations are discoverable in the
// authorized-tools catalog so a §25.14 agent enumerating its tools
// finds the §24.3 grant/list/revoke operations. F-24.3.3.
func TestAuthorizedToolsIncludeTenantAccess_spec_24_3(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp admin.AuthorizedToolsPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	got := map[string]bool{}
	for _, tool := range resp.Tools {
		got[tool.Tool] = true
	}
	want := []string{
		"admin.grant_runtime_tenant_access",
		"admin.list_runtime_tenant_access",
		"admin.revoke_runtime_tenant_access",
		"admin.grant_pool_tenant_access",
		"admin.list_pool_tenant_access",
		"admin.revoke_pool_tenant_access",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing tenant-access tool %q in authorized-tools: %+v", w, resp.Tools)
		}
	}
}

// TestAuthorizedToolsIncludeCredentialPools_spec_24_5 asserts the §24.5
// credential-pool operations are discoverable in the authorized-tools
// catalog so a §25.14 agent finds the credential-management surface. The
// manage operations are tenant-admin-visible (the manage_credential_pools
// permission); re-enable is platform-admin per §15.1 line 811. F-24.5.5.
func TestAuthorizedToolsIncludeCredentialPools_spec_24_5(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})

	taReq := withTenantAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil))
	taRR := httptest.NewRecorder()
	router.Handler().ServeHTTP(taRR, taReq)
	var taResp admin.AuthorizedToolsPayload
	_ = json.Unmarshal(taRR.Body.Bytes(), &taResp)
	taGot := map[string]bool{}
	for _, tool := range taResp.Tools {
		taGot[tool.Tool] = true
	}
	for _, w := range []string{
		"admin.create_credential_pool",
		"admin.list_credential_pools",
		"admin.get_credential_pool",
		"admin.update_credential_pool",
		"admin.delete_credential_pool",
		"admin.add_credential_to_pool",
		"admin.update_pool_credential",
		"admin.remove_pool_credential",
		"admin.revoke_pool_credential",
		"admin.revoke_credential_pool",
	} {
		if !taGot[w] {
			t.Errorf("tenant-admin missing credential-pool tool %q: %+v", w, taResp.Tools)
		}
	}
	// re-enable is platform-admin only.
	if taGot["admin.re_enable_pool_credential"] {
		t.Error("tenant-admin should not see admin.re_enable_pool_credential (platform-admin per §15.1:811)")
	}

	paReq := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/me/authorized-tools", nil))
	paRR := httptest.NewRecorder()
	router.Handler().ServeHTTP(paRR, paReq)
	var paResp admin.AuthorizedToolsPayload
	_ = json.Unmarshal(paRR.Body.Bytes(), &paResp)
	paGot := map[string]bool{}
	for _, tool := range paResp.Tools {
		paGot[tool.Tool] = true
	}
	if !paGot["admin.re_enable_pool_credential"] {
		t.Errorf("platform-admin missing admin.re_enable_pool_credential: %+v", paResp.Tools)
	}
}

// TestCredentialPoolCatalogMirrorsOpenAPI_spec_25_14 asserts every
// credential-pool MCP tool the catalog advertises is declared in the
// OpenAPI document, so a discovered tool resolves to a real route
// (§25.14 — the catalog is the source of truth for agent-callable ops).
// F-24.5.5.
func TestCredentialPoolCatalogMirrorsOpenAPI_spec_25_14(t *testing.T) {
	doc := openapi.Document()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}
	declared := map[string]bool{}
	paths, _ := parsed["paths"].(map[string]any)
	for _, raw := range paths {
		methods, _ := raw.(map[string]any)
		for _, m := range methods {
			body, _ := m.(map[string]any)
			if tool, ok := body["x-lenny-mcp-tool"].(string); ok {
				declared[tool] = true
			}
		}
	}
	for _, tool := range []string{
		"admin.create_credential_pool", "admin.list_credential_pools",
		"admin.get_credential_pool", "admin.update_credential_pool",
		"admin.delete_credential_pool", "admin.add_credential_to_pool",
		"admin.update_pool_credential", "admin.remove_pool_credential",
		"admin.revoke_pool_credential", "admin.revoke_credential_pool",
		"admin.re_enable_pool_credential",
	} {
		if !declared[tool] {
			t.Errorf("credential-pool tool %q not declared in OpenAPI document", tool)
		}
	}
}

func contains(roles []pkgauth.Role, r pkgauth.Role) bool {
	for _, q := range roles {
		if q == r {
			return true
		}
	}
	return false
}
