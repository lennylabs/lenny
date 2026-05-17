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
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/userstore"
)

// spec: §24.1 lenny-ctl bootstrap; §15.1 POST /v1/admin/bootstrap.

func newBootstrapRouter(t *testing.T) (*admin.Router, *tenantstore.Memory, *runtimestore.Memory, *userstore.Memory, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	users := userstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithRuntimes(runtimes).WithUsers(users)
	return router, tenants, runtimes, users, audit
}

func TestBootstrapHappyPath(t *testing.T) {
	router, tenants, runtimes, users, audit := newBootstrapRouter(t)
	body := admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{
			{ID: "acme", DisplayName: "Acme Corp"},
		},
		Runtimes: []admin.RuntimePayload{
			{Name: "echo", Image: "lenny/echo@sha256:abc", Type: "agent"},
		},
		Users: []admin.UserPayload{
			{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}},
		},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Tenants.CreatedCount != 1 || resp.Runtimes.CreatedCount != 1 || resp.Users.CreatedCount != 1 {
		t.Fatalf("counts: %+v", resp)
	}

	if _, err := tenants.Get(context.Background(), "acme"); err != nil {
		t.Errorf("tenant not stored: %v", err)
	}
	if _, err := runtimes.Get(context.Background(), "echo"); err != nil {
		t.Errorf("runtime not stored: %v", err)
	}
	if _, err := users.Get(context.Background(), "acme", "alice@acme.com"); err != nil {
		t.Errorf("user not stored: %v", err)
	}

	if got := audit.snapshot(); len(got) != 1 || got[0].Type != "admin.bootstrap.applied" {
		t.Errorf("audit: %+v", got)
	}
}

func TestBootstrapUpsertsExisting(t *testing.T) {
	router, tenants, runtimes, _, _ := newBootstrapRouter(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", DisplayName: "Old name"})
	_ = runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo", Image: "old@sha256:a"})

	body := admin.BootstrapRequest{
		Tenants:  []admin.TenantPayload{{ID: "acme", DisplayName: "New name"}},
		Runtimes: []admin.RuntimePayload{{Name: "echo", Image: "new@sha256:b"}},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d", rr.Code)
	}

	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Tenants.UpdatedCount != 1 || resp.Runtimes.UpdatedCount != 1 {
		t.Errorf("counts: %+v", resp)
	}

	t1, _ := tenants.Get(context.Background(), "acme")
	if t1.DisplayName != "New name" {
		t.Errorf("tenant displayName: got %q", t1.DisplayName)
	}
	r1, _ := runtimes.Get(context.Background(), "echo")
	if r1.Image != "new@sha256:b" {
		t.Errorf("runtime image: got %q", r1.Image)
	}
}

func TestBootstrapReportsPerEntryErrors(t *testing.T) {
	router, tenants, _, _, _ := newBootstrapRouter(t)
	body := admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{
			{ID: "acme"},
			{ID: ""},           // missing id
			{ID: "with space"}, // invalid format
		},
	}
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(buf)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMultiStatus {
		t.Fatalf("status: got %d, want 207", rr.Code)
	}
	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Tenants.CreatedCount != 1 || len(resp.Tenants.Errors) != 2 {
		t.Errorf("partial result: %+v", resp.Tenants)
	}
	// Successful entry should have been stored.
	if _, err := tenants.Get(context.Background(), "acme"); err != nil {
		t.Errorf("acme tenant not stored: %v", err)
	}
}

func TestBootstrapRequiresPlatformAdmin(t *testing.T) {
	router, _, _, _, _ := newBootstrapRouter(t)
	body, _ := json.Marshal(admin.BootstrapRequest{})
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin: got %d, want 403", rr.Code)
	}
}

func TestBootstrapRejectsMalformedJSON(t *testing.T) {
	router, _, _, _, _ := newBootstrapRouter(t)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap", bytes.NewReader([]byte("not-json"))))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}
