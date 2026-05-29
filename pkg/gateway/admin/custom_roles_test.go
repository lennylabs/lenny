// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §10.2 / §15.1 admin custom-role CRUD.

func newCustomRoleAdmin(t *testing.T) (*admin.Router, *customrolestore.Memory) {
	t.Helper()
	store := customrolestore.NewMemory()
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCustomRoles(store)
	return router, store
}

func validCustomRole(name string) admin.CustomRolePayload {
	return admin.CustomRolePayload{
		Name:        name,
		Permissions: []auth.Permission{auth.PermManageOwnSessions, auth.PermViewUsage},
	}
}

func TestCreateCustomRole(t *testing.T) {
	router, store := newCustomRoleAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/tenants/acme/roles",
		validCustomRole("session-manager"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, err := store.Get(context.Background(), "acme", "session-manager")
	if err != nil {
		t.Fatalf("store missing role: %v", err)
	}
	if len(row.Permissions) != 2 {
		t.Errorf("stored permissions = %+v, want 2", row.Permissions)
	}
}

func TestCreateCustomRoleAsTenantAdmin(t *testing.T) {
	router, store := newCustomRoleAdmin(t)
	// withTenantAdminPrincipal is tenant-admin of "acme"; the path tenant matches.
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/tenants/acme/roles",
		validCustomRole("r1"), withTenantAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("tenant-admin create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := store.Get(context.Background(), "acme", "r1"); err != nil {
		t.Errorf("role not stored: %v", err)
	}
}

func TestCreateCustomRoleTenantAdminCannotCrossTenant(t *testing.T) {
	router, _ := newCustomRoleAdmin(t)
	// A tenant-admin of "acme" targeting "globex" in the path is rejected.
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/tenants/globex/roles",
		validCustomRole("r1"), withTenantAdminPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-tenant create: status %d, want 403", rr.Code)
	}
}

func TestCreateCustomRoleRejectsExceedingPermission(t *testing.T) {
	router, _ := newCustomRoleAdmin(t)
	body := validCustomRole("overreach")
	// §10.2: a custom role may not exceed the tenant-admin permission set.
	body.Permissions = append(body.Permissions, auth.PermAccessCrossTenantData)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/tenants/acme/roles",
		body, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("permission exceeding tenant-admin: status %d, want 400", rr.Code)
	}
}

func TestCreateCustomRoleRejectsBuiltinName(t *testing.T) {
	router, _ := newCustomRoleAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/tenants/acme/roles",
		validCustomRole("tenant-admin"), withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("custom role colliding with a built-in role: status %d, want 400", rr.Code)
	}
}

func TestCreateCustomRoleRejectsDuplicate(t *testing.T) {
	router, _ := newCustomRoleAdmin(t)
	h := router.Handler()
	doAdminReq(t, h, http.MethodPost, "/v1/admin/tenants/acme/roles", validCustomRole("r1"), withAdminPrincipal)
	rr := doAdminReq(t, h, http.MethodPost, "/v1/admin/tenants/acme/roles", validCustomRole("r1"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate create: status %d, want 409", rr.Code)
	}
}

func TestGetCustomRole(t *testing.T) {
	router, store := newCustomRoleAdmin(t)
	if err := store.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "r1", Permissions: []auth.Permission{auth.PermViewUsage},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/tenants/acme/roles/r1",
		nil, withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var resp admin.CustomRolePayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Name != "r1" || len(resp.Permissions) != 1 {
		t.Errorf("response = %+v", resp)
	}
}

func TestGetCustomRoleMissing(t *testing.T) {
	router, _ := newCustomRoleAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/tenants/acme/roles/ghost",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get missing: status %d, want 404", rr.Code)
	}
}

func TestListCustomRoles(t *testing.T) {
	router, store := newCustomRoleAdmin(t)
	ctx := context.Background()
	_ = store.Create(ctx, customrolestore.CustomRole{TenantID: "acme", Name: "r1"})
	_ = store.Create(ctx, customrolestore.CustomRole{TenantID: "acme", Name: "r2"})
	_ = store.Create(ctx, customrolestore.CustomRole{TenantID: "globex", Name: "r3"})

	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/tenants/acme/roles",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	var resp struct {
		Roles []admin.CustomRolePayload `json:"roles"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Roles) != 2 {
		t.Errorf("list acme: got %d roles, want 2", len(resp.Roles))
	}
}

func TestUpdateCustomRoleReplacesPermissions(t *testing.T) {
	router, store := newCustomRoleAdmin(t)
	if err := store.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "r1", Permissions: []auth.Permission{auth.PermViewUsage},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body := admin.UpdateCustomRoleRequest{
		Permissions: []auth.Permission{auth.PermManageRuntimes, auth.PermManagePools},
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/tenants/acme/roles/r1",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme", "r1")
	if len(row.Permissions) != 2 {
		t.Errorf("updated permissions = %+v, want 2", row.Permissions)
	}
}

func TestDeleteCustomRole(t *testing.T) {
	router, store := newCustomRoleAdmin(t)
	if err := store.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "r1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodDelete, "/v1/admin/tenants/acme/roles/r1",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rr.Code)
	}
	if _, err := store.Get(context.Background(), "acme", "r1"); err == nil {
		t.Error("role should be gone after delete")
	}
}

// fakeUserLister is a userstore.Store stub whose List returns a fixed
// set. It supplies a user carrying a custom role for the deletion-guard
// test, since userstore.Memory does not yet accept custom-role names in
// User.Roles (a noted §10.2 gap).
type fakeUserLister struct{ users []userstore.User }

func (f fakeUserLister) List(context.Context, string, userstore.ListFilter) ([]userstore.User, error) {
	return f.users, nil
}
func (fakeUserLister) Create(context.Context, userstore.User) error { return nil }
func (fakeUserLister) Get(context.Context, string, string) (userstore.User, error) {
	return userstore.User{}, userstore.ErrNotFound
}

func (fakeUserLister) Update(context.Context, string, string, func(*userstore.User) error) (userstore.User, error) {
	return userstore.User{}, userstore.ErrNotFound
}
func (fakeUserLister) SoftDelete(context.Context, string, string, time.Time) error { return nil }
func (fakeUserLister) DeleteByUser(context.Context, string, string) (int, error)    { return 0, nil }
func (fakeUserLister) DeleteByTenant(context.Context, string) (int, error)          { return 0, nil }

func TestDeleteCustomRoleBlockedByAssignedUser(t *testing.T) {
	roles := customrolestore.NewMemory()
	ctx := context.Background()
	if err := roles.Create(ctx, customrolestore.CustomRole{TenantID: "acme", Name: "session-manager"}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	users := fakeUserLister{users: []userstore.User{
		{Subject: "alice@acme.com", TenantID: "acme", Roles: []auth.Role{auth.Role("session-manager")}},
	}}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCustomRoles(roles).WithUsers(users)

	rr := doAdminReq(t, router.Handler(), http.MethodDelete,
		"/v1/admin/tenants/acme/roles/session-manager", nil, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete an assigned role: status %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "RESOURCE_HAS_DEPENDENTS" {
		t.Errorf("error code = %q, want RESOURCE_HAS_DEPENDENTS", env.Error.Code)
	}
	if _, err := roles.Get(ctx, "acme", "session-manager"); err != nil {
		t.Error("a role blocked by the deletion guard must remain")
	}
}

func TestCustomRoleRejectsNonAdmin(t *testing.T) {
	router, _ := newCustomRoleAdmin(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme/roles", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("anonymous list: status %d, want 403", rr.Code)
	}
}

// §10.2: custom roles are stored in the tenant RBAC config, so the
// custom-role endpoints are gated on manage_rbac_config. A tenant
// custom role holding that permission can manage custom roles; one
// without it cannot.
func TestCustomRoleManagementCustomRoleEnforcement(t *testing.T) {
	store := customrolestore.NewMemory()
	_ = store.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "rbac-manager",
		Permissions: []auth.Permission{auth.PermManageRBACConfig},
	})
	_ = store.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "weak-role",
		Permissions: []auth.Permission{auth.PermViewUsage},
	})
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithCustomRoles(store)

	asRBACManager := func(req *http.Request) *http.Request { return withRolesFor(req, "acme", "rbac-manager") }
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/tenants/acme/roles",
		validCustomRole("session-manager"), asRBACManager)
	if rr.Code != http.StatusCreated {
		t.Fatalf("custom role with manage_rbac_config: got %d, want 201 (body=%s)", rr.Code, rr.Body.String())
	}

	asWeak := func(req *http.Request) *http.Request { return withRolesFor(req, "acme", "weak-role") }
	rr = doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/tenants/acme/roles", nil, asWeak)
	if rr.Code != http.StatusForbidden {
		t.Errorf("custom role without manage_rbac_config: got %d, want 403 (body=%s)", rr.Code, rr.Body.String())
	}
}
