// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.6 / §15.1 environment admin CRUD.

func newEnvironmentAdmin(t *testing.T) (*admin.Router, environmentstore.Store, *recordingAudit) {
	t.Helper()
	envs := environmentstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithEnvironments(envs)
	return router, envs, audit
}

func validEnvironmentPayload(name string) admin.EnvironmentPayload {
	return admin.EnvironmentPayload{
		Name:        name,
		TenantID:    "acme",
		Description: "security engineering workspace",
		Members: []admin.MemberPayload{
			{Identity: admin.IdentityPayload{Type: "oidc-group", Value: "security-engineers"}, Role: "creator"},
		},
		RuntimeSelector: admin.SelectorPayload{MatchLabels: map[string]string{"team": "security"}},
	}
}

func TestCreateEnvironment(t *testing.T) {
	router, envs, audit := newEnvironmentAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("security-team"), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, err := envs.Get(context.Background(), "acme", "security-team")
	if err != nil {
		t.Fatalf("Get after create: %v", err)
	}
	if len(got.Members) != 1 || got.Members[0].Identity.Value != "security-engineers" {
		t.Errorf("stored environment members = %+v", got.Members)
	}
	if snap := audit.snapshot(); len(snap) != 1 || snap[0].Type != "admin.environment.created" {
		t.Errorf("audit: %+v", snap)
	}
}

// spec: §10.6 line 562 — a tenant-admin asserting a tenantId different
// from its own authorized tenant must be rejected, not silently
// rewritten to its own tenant. F-10.6.12.
func TestCreateEnvironmentRejectsMismatchedTenantID_spec_10_6_562(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	body := validEnvironmentPayload("env-x")
	body.TenantID = "globex" // tenant-admin is "acme"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		body, withTenantAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("mismatched tenant id: status %d body %s, want 400", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "TENANT_ID_MISMATCH") {
		t.Errorf("response body should carry TENANT_ID_MISMATCH code: %s", rr.Body.String())
	}
}

// spec: §10.6 line 562 — same cross-check on Update. F-10.6.12.
func TestUpdateEnvironmentRejectsMismatchedTenantID_spec_10_6_562(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-y"), withTenantAdminPrincipal)
	body := validEnvironmentPayload("env-y")
	body.TenantID = "globex"
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/environments/env-y",
		body, withTenantAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("mismatched tenant id on update: status %d body %s, want 400", rr.Code, rr.Body.String())
	}
}

func TestCreateEnvironmentRejectsInvalidRole(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	body := validEnvironmentPayload("env-bad")
	body.Members[0].Role = "superuser"
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid member role: status %d, want 422", rr.Code)
	}
}

func TestCreateEnvironmentRejectsDuplicate(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-dup"), withAdminPrincipal)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-dup"), withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Errorf("duplicate create: status %d, want 409", rr.Code)
	}
}

func TestListEnvironments(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-a"), withAdminPrincipal)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-b"), withAdminPrincipal)

	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/environments?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	var resp struct {
		Environments []admin.EnvironmentPayload `json:"environments"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Environments) != 2 {
		t.Errorf("list returned %d environments, want 2", len(resp.Environments))
	}
}

func TestGetEnvironmentNotFound(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/environments/absent?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("get unknown: status %d, want 404", rr.Code)
	}
}

func TestUpdateEnvironment(t *testing.T) {
	router, envs, _ := newEnvironmentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-u"), withAdminPrincipal)

	body := validEnvironmentPayload("env-u")
	body.Description = "renamed workspace"
	body.DefaultDelegationPolicy = "security-policy"
	rr := doAdminReq(t, router.Handler(), http.MethodPut, "/v1/admin/environments/env-u",
		body, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := envs.Get(context.Background(), "acme", "env-u")
	if got.Description != "renamed workspace" || got.DefaultDelegationPolicy != "security-policy" {
		t.Errorf("update not persisted: %+v", got)
	}
}

func TestUpdateEnvironmentRoundTripsSelectorExpressions(t *testing.T) {
	router, envs, _ := newEnvironmentAdmin(t)
	body := validEnvironmentPayload("env-sel")
	body.RuntimeSelector.MatchExpressions = []admin.RequirementPayload{
		{Key: "approved", Operator: "In", Values: []string{"true"}},
	}
	body.RuntimeSelector.Types = []string{"agent", "mcp"}
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)

	got, err := envs.Get(context.Background(), "acme", "env-sel")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.RuntimeSelector.MatchExpressions) != 1 ||
		got.RuntimeSelector.MatchExpressions[0].Key != "approved" {
		t.Errorf("matchExpressions did not round-trip: %+v", got.RuntimeSelector.MatchExpressions)
	}
	if len(got.RuntimeSelector.Types) != 2 {
		t.Errorf("selector types did not round-trip: %v", got.RuntimeSelector.Types)
	}
}

func TestDeleteEnvironment(t *testing.T) {
	router, envs, _ := newEnvironmentAdmin(t)
	doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-d"), withAdminPrincipal)

	rr := doAdminReq(t, router.Handler(), http.MethodDelete, "/v1/admin/environments/env-d?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rr.Code)
	}
	if _, err := envs.Get(context.Background(), "acme", "env-d"); err == nil {
		t.Error("environment still present after delete")
	}
}

func TestEnvironmentRequiresAdmin(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	asPlainUser := func(req *http.Request) *http.Request {
		ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: "alice@acme.com", TenantID: "acme",
			Roles: []pkgauth.Role{pkgauth.RoleUser},
		})
		return req.WithContext(ctx)
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		validEnvironmentPayload("env-x"), asPlainUser)
	if rr.Code != http.StatusForbidden {
		t.Errorf("plain user create: status %d, want 403", rr.Code)
	}
}

func TestEnvironmentTenantAdminScoped(t *testing.T) {
	router, envs, _ := newEnvironmentAdmin(t)
	body := validEnvironmentPayload("env-ta")
	body.TenantID = ""
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments",
		body, withTenantAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("tenant-admin create: status %d, body %s", rr.Code, rr.Body.String())
	}
	if _, err := envs.Get(context.Background(), "acme", "env-ta"); err != nil {
		t.Errorf("tenant-admin's environment not stored under its tenant: %v", err)
	}
}

// §10.2: environment endpoints are gated on manage_environments. A
// tenant custom role that holds the permission is admitted; one that
// does not is rejected.
func TestEnvironmentCustomRoleEnforcement(t *testing.T) {
	envs := environmentstore.NewMemory()
	roles := customrolestore.NewMemory()
	_ = roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "env-admin",
		Permissions: []pkgauth.Permission{pkgauth.PermManageEnvironments},
	})
	_ = roles.Create(context.Background(), customrolestore.CustomRole{
		TenantID: "acme", Name: "usage-viewer",
		Permissions: []pkgauth.Permission{pkgauth.PermViewUsage},
	})
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithEnvironments(envs).WithCustomRoles(roles)

	asEnvAdmin := func(req *http.Request) *http.Request { return withRolesFor(req, "acme", "env-admin") }
	body := validEnvironmentPayload("env-cr")
	body.TenantID = ""
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, asEnvAdmin)
	if rr.Code != http.StatusCreated {
		t.Fatalf("custom role with manage_environments: got %d, want 201 (body=%s)", rr.Code, rr.Body.String())
	}
	if _, err := envs.Get(context.Background(), "acme", "env-cr"); err != nil {
		t.Errorf("custom-role environment not stored under the principal's tenant: %v", err)
	}

	asUsageViewer := func(req *http.Request) *http.Request { return withRolesFor(req, "acme", "usage-viewer") }
	rr = doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/environments", nil, asUsageViewer)
	if rr.Code != http.StatusForbidden {
		t.Errorf("custom role without manage_environments: got %d, want 403 (body=%s)", rr.Code, rr.Body.String())
	}
}
