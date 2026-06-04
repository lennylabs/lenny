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
		Environments []admin.EnvironmentPayload `json:"items"`
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

// spec: §10.6 lines 613-625 — the bilateral cross-environment-delegation
// block names the peer under `targetEnvironment` (outbound) and
// `sourceEnvironment` (inbound). The wire shape must round-trip both
// distinct field names rather than collapse them onto a single
// `environment` key that silently drops the peer on every write.
// F-10.6.4.
func TestCrossEnvironmentDelegationWireRoundTrip_spec_10_6_613(t *testing.T) {
	router, envs, _ := newEnvironmentAdmin(t)
	body := validEnvironmentPayload("env-xenv")
	body.CrossEnvironmentDelegation = admin.CrossEnvDelegationPayload{
		Outbound: []admin.CrossEnvOutboundRulePayload{{
			TargetEnvironment: "platform-services",
			Runtimes:          admin.SelectorPayload{MatchLabels: map[string]string{"shared": "true"}},
		}},
		Inbound: []admin.CrossEnvInboundRulePayload{{
			SourceEnvironment: "*",
			Runtimes:          admin.SelectorPayload{MatchLabels: map[string]string{"shared": "true"}},
		}},
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	// The peer names must reach the store, not be dropped to empty.
	got, err := envs.Get(context.Background(), "acme", "env-xenv")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.CrossEnvOutbound) != 1 || got.CrossEnvOutbound[0].Environment != "platform-services" {
		t.Errorf("outbound targetEnvironment did not persist: %+v", got.CrossEnvOutbound)
	}
	if len(got.CrossEnvInbound) != 1 || got.CrossEnvInbound[0].Environment != "*" {
		t.Errorf("inbound sourceEnvironment did not persist: %+v", got.CrossEnvInbound)
	}

	// GET must emit the spec field names, not a legacy `environment` key.
	rr = doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/environments/env-xenv?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var raw struct {
		CrossEnvironmentDelegation struct {
			Outbound []map[string]json.RawMessage `json:"outbound"`
			Inbound  []map[string]json.RawMessage `json:"inbound"`
		} `json:"crossEnvironmentDelegation"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal GET body: %v", err)
	}
	if len(raw.CrossEnvironmentDelegation.Outbound) != 1 {
		t.Fatalf("GET outbound = %+v", raw.CrossEnvironmentDelegation.Outbound)
	}
	if _, ok := raw.CrossEnvironmentDelegation.Outbound[0]["targetEnvironment"]; !ok {
		t.Errorf("outbound rule missing targetEnvironment key: %v", raw.CrossEnvironmentDelegation.Outbound[0])
	}
	if _, ok := raw.CrossEnvironmentDelegation.Outbound[0]["environment"]; ok {
		t.Errorf("outbound rule must not carry a legacy environment key: %v", raw.CrossEnvironmentDelegation.Outbound[0])
	}
	if _, ok := raw.CrossEnvironmentDelegation.Inbound[0]["sourceEnvironment"]; !ok {
		t.Errorf("inbound rule missing sourceEnvironment key: %v", raw.CrossEnvironmentDelegation.Inbound[0])
	}
}

// spec: §10.6 lines 595-599 — connectorSelector carries the tag
// selector plus the allowedCapabilities / deniedCapabilities lists. The
// admin wire shape must round-trip both halves: the capability lists are
// no longer silently dropped on write or invisible on GET. F-10.6.3.
func TestConnectorSelectorCapabilitiesWireRoundTrip_spec_10_6_595(t *testing.T) {
	router, envs, _ := newEnvironmentAdmin(t)
	body := validEnvironmentPayload("env-conn")
	body.ConnectorSelector = admin.ConnectorSelectorPayload{
		MatchLabels:         map[string]string{"team": "security"},
		AllowedCapabilities: []string{"read", "search", "network"},
		DeniedCapabilities:  []string{"write", "delete", "execute", "admin"},
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	got, err := envs.Get(context.Background(), "acme", "env-conn")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConnectorSelector.Selector.MatchLabels["team"] != "security" {
		t.Errorf("connectorSelector matchLabels did not persist: %+v", got.ConnectorSelector.Selector)
	}
	if strings.Join(got.ConnectorSelector.AllowedCapabilities, ",") != "read,search,network" {
		t.Errorf("allowedCapabilities did not persist: %+v", got.ConnectorSelector.AllowedCapabilities)
	}
	if strings.Join(got.ConnectorSelector.DeniedCapabilities, ",") != "write,delete,execute,admin" {
		t.Errorf("deniedCapabilities did not persist: %+v", got.ConnectorSelector.DeniedCapabilities)
	}

	// GET must emit the capability keys under connectorSelector.
	rr = doAdminReq(t, router.Handler(), http.MethodGet, "/v1/admin/environments/env-conn?tenantId=acme",
		nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: status %d", rr.Code)
	}
	var raw struct {
		ConnectorSelector map[string]json.RawMessage `json:"connectorSelector"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal GET body: %v", err)
	}
	if _, ok := raw.ConnectorSelector["allowedCapabilities"]; !ok {
		t.Errorf("GET connectorSelector missing allowedCapabilities key: %v", raw.ConnectorSelector)
	}
	if _, ok := raw.ConnectorSelector["deniedCapabilities"]; !ok {
		t.Errorf("GET connectorSelector missing deniedCapabilities key: %v", raw.ConnectorSelector)
	}
}

// spec: §10.6 lines 595-599 — a capability that appears in both the
// allowed and denied lists is a contradictory rule; admission rejects it.
// F-10.6.3.
func TestConnectorSelectorCapabilitiesRejectsOverlap_spec_10_6_595(t *testing.T) {
	router, _, _ := newEnvironmentAdmin(t)
	body := validEnvironmentPayload("env-bad")
	body.ConnectorSelector = admin.ConnectorSelectorPayload{
		MatchLabels:         map[string]string{"team": "security"},
		AllowedCapabilities: []string{"read"},
		DeniedCapabilities:  []string{"read"},
	}
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/environments", body, withAdminPrincipal)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("overlapping capability: status %d body %s, want 422", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "allowedCapabilities") {
		t.Errorf("validation error should name the offending field: %s", rr.Body.String())
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
