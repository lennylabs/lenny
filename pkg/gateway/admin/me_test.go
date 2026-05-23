// SPDX-License-Identifier: MIT

package admin_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
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

func contains(roles []pkgauth.Role, r pkgauth.Role) bool {
	for _, q := range roles {
		if q == r {
			return true
		}
	}
	return false
}
