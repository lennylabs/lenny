// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

func newUserAdmin(t *testing.T) (*admin.Router, userstore.Store, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	users := userstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithUsers(users)
	return router, users, audit
}

func withTenantAdminFor(req *http.Request, tenantID string) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "tenant-admin@" + tenantID, TenantID: tenantID,
		Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
	})
	return req.WithContext(ctx)
}

func TestCreateUserPlatformAdmin(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	body, _ := json.Marshal(admin.UserPayload{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Email:    "alice@acme.com",
		Roles:    []pkgauth.Role{pkgauth.RoleUser},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if len(row.Roles) != 1 || row.Roles[0] != pkgauth.RoleUser {
		t.Errorf("Roles: %+v", row.Roles)
	}
	if got := audit.snapshot(); len(got) != 1 || got[0].Type != "admin.user.created" {
		t.Errorf("audit: %+v", got)
	}
}

func TestCreatePlatformAdminRequiresTenantID(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	body, _ := json.Marshal(admin.UserPayload{Subject: "alice@acme.com"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("missing tenantId for platform-admin: got %d, want 403", rr.Code)
	}
}

func TestCreateUserTenantAdminCannotCrossTenant(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	body, _ := json.Marshal(admin.UserPayload{
		Subject:  "alice@globex.com",
		TenantID: "globex",
		Roles:    []pkgauth.Role{pkgauth.RoleUser},
	})
	req := withTenantAdminFor(httptest.NewRequest(http.MethodPost, "/v1/admin/users", bytes.NewReader(body)), "acme")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		// Even though the body asked for globex, tenant-admin gets
		// scoped to its own tenant (acme). The create succeeds in
		// the caller's tenant.
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.UserPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.TenantID != "acme" {
		t.Errorf("tenant-admin cross-tenant: got %q, want acme", resp.TenantID)
	}
}

func TestCreateUserTenantAdminCannotGrantPlatformAdmin(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	body, _ := json.Marshal(admin.UserPayload{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	req := withTenantAdminFor(httptest.NewRequest(http.MethodPost, "/v1/admin/users", bytes.NewReader(body)), "acme")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin granting platform-admin: got %d, want 403", rr.Code)
	}
}

func TestListUsersTenantScoped(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	_ = store.Create(context.Background(), userstore.User{Subject: "a", TenantID: "acme"})
	_ = store.Create(context.Background(), userstore.User{Subject: "b", TenantID: "globex"})

	req := withTenantAdminFor(httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil), "acme")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}
	var resp struct {
		Users []admin.UserPayload `json:"items"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Users) != 1 || resp.Users[0].Subject != "a" {
		t.Errorf("tenant-admin sees own tenant only: %+v", resp.Users)
	}
}

func TestGetUserMissing(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	req := withTenantAdminFor(httptest.NewRequest(http.MethodGet, "/v1/admin/users/missing", nil), "acme")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

func TestUpdateUser(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	_ = store.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme"})

	disabled := true
	body, _ := json.Marshal(admin.UpdateUserRequest{Disabled: &disabled})
	req := withTenantAdminFor(httptest.NewRequest(http.MethodPut, "/v1/admin/users/alice@acme.com", bytes.NewReader(body)), "acme")
	// spec: §15.1 lines 1207-1211 — the PUT requires If-Match; a freshly
	// created user is at version 1.
	req.Header.Set("If-Match", `"1"`)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "alice@acme.com")
	if !row.Disabled {
		t.Errorf("Disabled not set: %+v", row)
	}
	if got := audit.snapshot(); len(got) != 1 || got[0].Type != "admin.user.updated" {
		t.Errorf("audit: %+v", got)
	}
}

func TestUpdateUserTenantAdminCannotGrantPlatformAdmin(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	_ = store.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme"})

	roles := []pkgauth.Role{pkgauth.RolePlatformAdmin}
	body, _ := json.Marshal(admin.UpdateUserRequest{Roles: &roles})
	req := withTenantAdminFor(httptest.NewRequest(http.MethodPut, "/v1/admin/users/alice@acme.com", bytes.NewReader(body)), "acme")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin updating to platform-admin: got %d, want 403", rr.Code)
	}
}

func TestDeleteUserSoftDeletes(t *testing.T) {
	router, store, audit := newUserAdmin(t)
	_ = store.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme"})

	req := withTenantAdminFor(httptest.NewRequest(http.MethodDelete, "/v1/admin/users/alice@acme.com", nil), "acme")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: %d", rr.Code)
	}
	row, _ := store.Get(context.Background(), "acme", "alice@acme.com")
	if row.DeletedAt.IsZero() {
		t.Errorf("DeletedAt should be set")
	}
	if got := audit.snapshot(); len(got) != 1 || got[0].Type != "admin.user.soft_deleted" {
		t.Errorf("audit: %+v", got)
	}
}

func TestUserEndpointsRejectAnonymous(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("anonymous: got %d, want 403", rr.Code)
	}
}

func TestUserEndpointsRejectRegularUser(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles: []pkgauth.Role{pkgauth.RoleUser},
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("regular user: got %d, want 403", rr.Code)
	}
}

// §10.2: a role assignment must name a built-in role or a custom role
// registered in the tenant. A well-formed but unregistered name is
// rejected at the admin layer even though the userstore accepts the
// syntax.
func TestCreateUserRejectsUnknownRole(t *testing.T) {
	router, _, _ := newUserAdmin(t)
	body, _ := json.Marshal(admin.UserPayload{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles: []pkgauth.Role{"data-analyst"},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown role: got %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestCreateUserAcceptsRegisteredCustomRole(t *testing.T) {
	tenants := tenantstore.NewMemory()
	users := userstore.NewMemory()
	roles := customrolestore.NewMemory()
	_ = roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID:    "acme",
		Name:        "session-manager",
		Permissions: []pkgauth.Permission{pkgauth.PermManageOwnSessions},
	})
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithCustomRoles(roles)

	body, _ := json.Marshal(admin.UserPayload{
		Subject: "alice@acme.com", TenantID: "acme",
		Roles: []pkgauth.Role{"session-manager"},
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/users", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("registered custom role: got %d, want 201 (body=%s)", rr.Code, rr.Body.String())
	}
	row, err := users.Get(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if len(row.Roles) != 1 || row.Roles[0] != "session-manager" {
		t.Errorf("Roles: %+v", row.Roles)
	}
}

func TestUpdateUserRejectsUnknownRole(t *testing.T) {
	router, store, _ := newUserAdmin(t)
	_ = store.Create(context.Background(), userstore.User{Subject: "alice@acme.com", TenantID: "acme"})

	roles := []pkgauth.Role{"data-analyst"}
	body, _ := json.Marshal(admin.UpdateUserRequest{Roles: &roles})
	req := withTenantAdminFor(httptest.NewRequest(http.MethodPut, "/v1/admin/users/alice@acme.com", bytes.NewReader(body)), "acme")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("unknown role on update: got %d, want 400 (body=%s)", rr.Code, rr.Body.String())
	}
}

// newUserAdminWithRole builds a user-admin Router with the custom-role
// registry wired and one custom role seeded.
func newUserAdminWithRole(t *testing.T, role customrolestore.CustomRole) (*admin.Router, userstore.Store) {
	t.Helper()
	users := userstore.NewMemory()
	roles := customrolestore.NewMemory()
	if err := roles.Create(context.Background(), role); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithUsers(users).WithCustomRoles(roles)
	return router, users
}

// withRolesFor attaches a principal holding an arbitrary role set,
// scoped to tenantID. Used to exercise custom-role principals.
func withRolesFor(req *http.Request, tenantID string, roles ...pkgauth.Role) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "carol@" + tenantID, TenantID: tenantID,
		Roles: roles,
	})
	return req.WithContext(ctx)
}

// §10.2: a tenant custom role whose permission set includes
// manage_users passes the user-management gate.
func TestUserEndpointsAllowCustomRoleWithManageUsers(t *testing.T) {
	router, _ := newUserAdminWithRole(t, customrolestore.CustomRole{
		TenantID: "acme", Name: "user-manager",
		Permissions: []pkgauth.Permission{pkgauth.PermManageUsers},
	})
	req := withRolesFor(httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil), "acme", "user-manager")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("custom role with manage_users: got %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

// §10.2: a custom role without manage_users is denied user management,
// even though it is a valid custom role.
func TestUserEndpointsRejectCustomRoleWithoutManageUsers(t *testing.T) {
	router, _ := newUserAdminWithRole(t, customrolestore.CustomRole{
		TenantID: "acme", Name: "session-manager",
		Permissions: []pkgauth.Permission{pkgauth.PermManageOwnSessions},
	})
	req := withRolesFor(httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil), "acme", "session-manager")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("custom role without manage_users: got %d, want 403 (body=%s)", rr.Code, rr.Body.String())
	}
}

// §10.2: a custom role is tenant-scoped. A custom-role principal that
// names another tenant in the request body is still scoped to its own
// tenant by the tenant resolver.
func TestCreateUserCustomRoleScopedToOwnTenant(t *testing.T) {
	router, users := newUserAdminWithRole(t, customrolestore.CustomRole{
		TenantID: "acme", Name: "user-manager",
		Permissions: []pkgauth.Permission{pkgauth.PermManageUsers},
	})
	body, _ := json.Marshal(admin.UserPayload{
		Subject: "dave@acme.com", TenantID: "globex",
		Roles: []pkgauth.Role{pkgauth.RoleUser},
	})
	req := withRolesFor(httptest.NewRequest(http.MethodPost, "/v1/admin/users", bytes.NewReader(body)), "acme", "user-manager")
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("custom-role create: got %d, want 201 (body=%s)", rr.Code, rr.Body.String())
	}
	if _, err := users.Get(context.Background(), "acme", "dave@acme.com"); err != nil {
		t.Errorf("user not created in the principal's own tenant: %v", err)
	}
	if _, err := users.Get(context.Background(), "globex", "dave@acme.com"); err == nil {
		t.Error("user leaked into the body-requested tenant globex")
	}
}
