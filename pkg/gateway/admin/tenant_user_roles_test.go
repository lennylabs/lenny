// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §15.1 lines 826-828 — the tenant-scoped user listing and the
// platform-managed role assignment surface. F-15.1.3.

func seedRoleUser(t *testing.T, s userstore.Store, u userstore.User) {
	t.Helper()
	if err := s.Create(context.Background(), u); err != nil {
		t.Fatalf("seed user %s: %v", u.Subject, err)
	}
}

func roleReq(method, path, ifMatch string, body string, as func(*http.Request) *http.Request) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	return as(r)
}

func eventTypes(evs []admin.AuditEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

func TestPutTenantUserRoleAssigns_spec_15_1_827(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})

	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"1"`,
		`{"role":"tenant-viewer"}`, withAdminPrincipal))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "alice@acme.com")
	if !row.RoleAssigned || len(row.Roles) != 1 || row.Roles[0] != pkgauth.RoleTenantViewer {
		t.Errorf("assignment: assigned=%v roles=%v", row.RoleAssigned, row.Roles)
	}
	if row.RoleAssignedBy != "admin@acme.com" || row.RoleAssignedAt.IsZero() {
		t.Errorf("provenance: by=%q at=%v", row.RoleAssignedBy, row.RoleAssignedAt)
	}
	if got := rr.Header().Get("ETag"); got != `"2"` {
		t.Errorf("ETag = %q, want \"2\"", got)
	}
	if got := eventTypes(audit.snapshot()); len(got) != 1 || got[0] != "user.role_assigned" {
		t.Errorf("audit = %v, want [user.role_assigned]", got)
	}
}

func TestPutTenantUserRoleRequiresIfMatch_spec_15_1_827(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", "",
		`{"role":"user"}`, withAdminPrincipal))
	if rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("missing If-Match: status %d, want 428; body %s", rr.Code, rr.Body.String())
	}
}

func TestPutTenantUserRoleStaleIfMatch_spec_15_1_827(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"99"`,
		`{"role":"user"}`, withAdminPrincipal))
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match: status %d, want 412; body %s", rr.Code, rr.Body.String())
	}
}

func TestPutTenantUserRoleRejectsPlatformAdmin_spec_15_1_827(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"1"`,
		`{"role":"platform-admin"}`, withAdminPrincipal))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("platform-admin assignment: status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestPutTenantUserRoleRejectsUnknownRole_spec_15_1_827(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"1"`,
		`{"role":"wizard"}`, withAdminPrincipal))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown role: status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestPutTenantUserRoleEmptyRole_spec_15_1_827(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"1"`,
		`{"role":""}`, withAdminPrincipal))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty role: status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestPutTenantUserRoleUnknownUser_spec_15_1_827(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/ghost@acme.com/role", `"1"`,
		`{"role":"user"}`, withAdminPrincipal))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown user: status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

func TestPutTenantUserRoleCustomRole_spec_15_1_827(t *testing.T) {
	tenants := tenantstore.NewMemory()
	users := userstore.NewMemory()
	roles := customrolestore.NewMemory()
	if err := roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "session-manager",
		Permissions: []pkgauth.Permission{pkgauth.PermViewUsage},
	}); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithUsers(users).WithCustomRoles(roles)
	seedRoleUser(t, users, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})

	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"1"`,
		`{"role":"session-manager"}`, withAdminPrincipal))
	if rr.Code != http.StatusOK {
		t.Fatalf("custom-role assignment: status %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	// A custom role absent from the tenant is rejected.
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"2"`,
		`{"role":"phantom-role"}`, withAdminPrincipal))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown custom role: status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

func TestPutTenantUserRoleTenantAdminCrossTenant_spec_15_1_827(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodPut,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"1"`,
		`{"role":"user"}`, func(r *http.Request) *http.Request { return withTenantAdminFor(r, "globex") }))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant assignment: status %d, want 403; body %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteTenantUserRoleRemoves_spec_15_1_828(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles:          []pkgauth.Role{pkgauth.RoleTenantViewer},
		RoleAssigned:   true,
		RoleAssignedBy: "admin@acme.com",
		RoleAssignedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodDelete,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", "", "", withAdminPrincipal))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d, want 204; body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "alice@acme.com")
	if row.RoleAssigned || len(row.Roles) != 0 || row.RoleAssignedBy != "" || !row.RoleAssignedAt.IsZero() {
		t.Errorf("after removal: assigned=%v roles=%v by=%q at=%v",
			row.RoleAssigned, row.Roles, row.RoleAssignedBy, row.RoleAssignedAt)
	}
	if got := eventTypes(audit.snapshot()); len(got) != 1 || got[0] != "user.role_removed" {
		t.Errorf("audit = %v, want [user.role_removed]", got)
	}
}

func TestDeleteTenantUserRoleIdempotent_spec_15_1_828(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme", RoleAssigned: false})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodDelete,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", "", "", withAdminPrincipal))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("idempotent delete: status %d, want 204; body %s", rr.Code, rr.Body.String())
	}
	if got := audit.snapshot(); len(got) != 0 {
		t.Errorf("no-op delete must not emit: %v", eventTypes(got))
	}
}

func TestDeleteTenantUserRoleStaleIfMatch_spec_15_1_828(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{
		Subject: "alice@acme.com", TenantID: "acme", RoleAssigned: true,
		Roles: []pkgauth.Role{pkgauth.RoleUser},
	})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodDelete,
		"/v1/admin/tenants/acme/users/alice@acme.com/role", `"99"`, "", withAdminPrincipal))
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale If-Match on delete: status %d, want 412; body %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteTenantUserRoleUnknownUser_spec_15_1_828(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodDelete,
		"/v1/admin/tenants/acme/users/ghost@acme.com/role", "", "", withAdminPrincipal))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown user delete: status %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

func TestListTenantUsersProjectsRoles_spec_15_1_826(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles:          []pkgauth.Role{pkgauth.RoleTenantViewer},
		RoleAssigned:   true,
		RoleAssignedBy: "admin@acme.com",
		RoleAssignedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	seedRoleUser(t, store, userstore.User{Subject: "bob@acme.com", TenantID: "acme", RoleAssigned: false})
	seedRoleUser(t, store, userstore.User{Subject: "carol@globex.com", TenantID: "globex", RoleAssigned: true,
		Roles: []pkgauth.Role{pkgauth.RoleUser}})

	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodGet,
		"/v1/admin/tenants/acme/users", "", "", withAdminPrincipal))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Items []struct {
			UserID     string `json:"user_id"`
			Role       string `json:"role"`
			AssignedBy string `json:"assignedBy"`
			ETag       string `json:"etag"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Items) != 2 {
		t.Fatalf("items = %d, want 2 (tenant-scoped); body %s", len(env.Items), rr.Body.String())
	}
	byID := map[string]string{}
	for _, it := range env.Items {
		byID[it.UserID] = it.Role
		if it.UserID == "carol@globex.com" {
			t.Errorf("globex user leaked into acme listing")
		}
		if it.ETag == "" {
			t.Errorf("item %s missing etag", it.UserID)
		}
	}
	if byID["alice@acme.com"] != "tenant-viewer" {
		t.Errorf("alice role = %q, want tenant-viewer", byID["alice@acme.com"])
	}
	if byID["bob@acme.com"] != "" {
		t.Errorf("bob (no assignment) role = %q, want empty", byID["bob@acme.com"])
	}
}

func TestListTenantUsersTenantAdminScoped_spec_15_1_826(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	seedRoleUser(t, store, userstore.User{Subject: "alice@acme.com", TenantID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, roleReq(http.MethodGet,
		"/v1/admin/tenants/globex/users", "", "",
		func(r *http.Request) *http.Request { return withTenantAdminFor(r, "acme") }))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin cross-tenant list: status %d, want 403; body %s", rr.Code, rr.Body.String())
	}
}
