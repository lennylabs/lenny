// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §10.6 / §15.1 GET /v1/admin/tenants/{id}/access-report.

func accessReportRouter(t *testing.T, envs ...environmentstore.Environment) *admin.Router {
	t.Helper()
	store := environmentstore.NewMemory()
	for _, e := range envs {
		if err := store.Create(context.Background(), e); err != nil {
			t.Fatalf("seed environment %q: %v", e.Name, err)
		}
	}
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	}).WithEnvironments(store)
}

func member(typ, value string, role environment.Role) environmentstore.Member {
	return environmentstore.Member{
		Identity: environmentstore.Identity{Type: typ, Value: value},
		Role:     role,
	}
}

func TestTenantAccessReportMatrix(t *testing.T) {
	router := accessReportRouter(
		t,
		environmentstore.Environment{
			Name: "security-team", TenantID: "acme",
			Members: []environmentstore.Member{
				member("oidc-group", "security-engineers", environment.RoleCreator),
				member("oidc-group", "security-leads", environment.RoleAdmin),
			},
		},
		environmentstore.Environment{
			Name: "platform-services", TenantID: "acme",
			Members: []environmentstore.Member{
				member("oidc-group", "security-engineers", environment.RoleViewer),
			},
		},
	)

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/access-report", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.TenantAccessReportPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Environments are the name-sorted matrix columns.
	if len(resp.Environments) != 2 ||
		resp.Environments[0] != "platform-services" || resp.Environments[1] != "security-team" {
		t.Errorf("environments: %+v", resp.Environments)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("entries: got %d, want 2 (%+v)", len(resp.Entries), resp.Entries)
	}
	// security-engineers appears in both environments.
	se := resp.Entries[0]
	if se.Identity.Value != "security-engineers" || len(se.Access) != 2 {
		t.Fatalf("first entry: %+v", se)
	}
	if se.Access[0].Environment != "platform-services" || se.Access[0].Role != "viewer" {
		t.Errorf("se access[0]: %+v", se.Access[0])
	}
	if se.Access[1].Environment != "security-team" || se.Access[1].Role != "creator" {
		t.Errorf("se access[1]: %+v", se.Access[1])
	}
	// security-leads appears only in security-team.
	sl := resp.Entries[1]
	if sl.Identity.Value != "security-leads" || len(sl.Access) != 1 ||
		sl.Access[0].Environment != "security-team" || sl.Access[0].Role != "admin" {
		t.Errorf("second entry: %+v", sl)
	}
}

func TestTenantAccessReportEmpty(t *testing.T) {
	router := accessReportRouter(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/access-report", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var resp admin.TenantAccessReportPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if len(resp.Entries) != 0 || len(resp.Environments) != 0 {
		t.Errorf("a tenant with no environments must report an empty matrix: %+v", resp)
	}
}

func TestTenantAccessReportRejectsAnonymous(t *testing.T) {
	router := accessReportRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme/access-report", nil)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("anonymous: got %d, want 403", rr.Code)
	}
}
