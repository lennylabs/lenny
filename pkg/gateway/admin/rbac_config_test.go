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

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §10.6 / §15.1 GET|PUT /v1/admin/tenants/{id}/rbac-config.

func newRBACConfigAdmin(t *testing.T) (*admin.Router, tenantstore.Store) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
	})
	return router, tenants
}

func TestRBACConfigGetDefaultsToDenyAll(t *testing.T) {
	router, _ := newRBACConfigAdmin(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/rbac-config", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.RBACConfigPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.NoEnvironmentPolicy != "deny-all" {
		t.Errorf("unset noEnvironmentPolicy: got %q, want deny-all", resp.NoEnvironmentPolicy)
	}
}

func TestRBACConfigPutAllowAll(t *testing.T) {
	router, tenants := newRBACConfigAdmin(t)
	body, _ := json.Marshal(admin.RBACConfigPayload{NoEnvironmentPolicy: "allow-all"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/rbac-config", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	// §10.6: setting allow-all attaches an advisory Warning header.
	if rr.Header().Get("Warning") == "" {
		t.Error("an allow-all PUT must attach a Warning response header")
	}
	row, _ := tenants.Get(context.Background(), "acme")
	if row.NoEnvironmentPolicy != "allow-all" {
		t.Errorf("stored noEnvironmentPolicy: got %q, want allow-all", row.NoEnvironmentPolicy)
	}
}

func TestRBACConfigPutDenyAllNoWarning(t *testing.T) {
	router, _ := newRBACConfigAdmin(t)
	body, _ := json.Marshal(admin.RBACConfigPayload{NoEnvironmentPolicy: "deny-all"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/rbac-config", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if rr.Header().Get("Warning") != "" {
		t.Error("a deny-all PUT must not attach a Warning header")
	}
}

func TestRBACConfigPutRejectsInvalidPolicy(t *testing.T) {
	router, _ := newRBACConfigAdmin(t)
	body, _ := json.Marshal(admin.RBACConfigPayload{NoEnvironmentPolicy: "permit-everything"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/rbac-config", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("invalid policy: got %d, want 400", rr.Code)
	}
}

func TestRBACConfigPutOmittedIsDenyAll(t *testing.T) {
	// §10.6: a PUT body without noEnvironmentPolicy is treated as deny-all.
	router, tenants := newRBACConfigAdmin(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/rbac-config", bytes.NewReader([]byte(`{}`))))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", rr.Code, rr.Body.String())
	}
	row, _ := tenants.Get(context.Background(), "acme")
	if row.NoEnvironmentPolicy != "deny-all" {
		t.Errorf("omitted noEnvironmentPolicy: stored %q, want deny-all", row.NoEnvironmentPolicy)
	}
}

func TestRBACConfigTenantNotFound(t *testing.T) {
	router, _ := newRBACConfigAdmin(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/ghost/rbac-config", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("missing tenant: got %d, want 404", rr.Code)
	}
}

// fakeRBACMetrics records the §10.6 allow-all counter calls.
type fakeRBACMetrics struct{ allowAllTenants []string }

func (f *fakeRBACMetrics) RecordNoEnvironmentPolicyAllowAll(tenantID string) {
	f.allowAllTenants = append(f.allowAllTenants, tenantID)
}

func TestRBACConfigAllowAllRecordsMetric(t *testing.T) {
	tenants := tenantstore.NewMemory()
	if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	fake := &fakeRBACMetrics{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock:   func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Metrics: fake,
	})
	put := func(policy string) {
		body, _ := json.Marshal(admin.RBACConfigPayload{NoEnvironmentPolicy: policy})
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
			"/v1/admin/tenants/acme/rbac-config", bytes.NewReader(body)))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("PUT %s: status %d, body=%s", policy, rr.Code, rr.Body.String())
		}
	}
	// §10.6: deny-all does not increment the allow-all counter.
	put("deny-all")
	if len(fake.allowAllTenants) != 0 {
		t.Errorf("deny-all must not record the allow-all counter: %v", fake.allowAllTenants)
	}
	// allow-all records the counter for the written tenant.
	put("allow-all")
	if len(fake.allowAllTenants) != 1 || fake.allowAllTenants[0] != "acme" {
		t.Errorf("allow-all must record the counter for acme: got %v", fake.allowAllTenants)
	}
}
