// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

func postBootstrap(t *testing.T, router *admin.Router, body admin.BootstrapRequest, query string) (*httptest.ResponseRecorder, admin.BootstrapResponse) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/bootstrap"+query, bytes.NewReader(buf)))
	rec := httptest.NewRecorder()
	router.Handler().ServeHTTP(rec, req)
	var resp admin.BootstrapResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

// spec: §12.9 line 1048 — a seed entry with an out-of-enum workspaceTier
// is rejected per-entry (SEED_VALIDATION) without persisting the tenant.
func TestBootstrapRejectsInvalidWorkspaceTier(t *testing.T) {
	router, tenants, _, _, _ := newBootstrapRouter(t)
	rec, resp := postBootstrap(t, router, admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{{ID: "acme", WorkspaceTier: "T2"}},
	}, "")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body=%s", rec.Code, rec.Body.String())
	}
	if len(resp.Tenants.Errors) != 1 {
		t.Fatalf("errors = %+v, want one validation error", resp.Tenants.Errors)
	}
	if _, err := tenants.Get(context.Background(), "acme"); err == nil {
		t.Error("tenant with invalid tier must not be persisted")
	}
}

// spec: §12.9 line 1033 — a bootstrap re-run that names a looser tier on a
// currently-stricter tenant is rejected even under forceUpdate; the tier
// is not downgraded.
func TestBootstrapRejectsWorkspaceTierDowngrade(t *testing.T) {
	router, tenants, _, _, _ := newBootstrapRouter(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})

	rec, resp := postBootstrap(t, router, admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{{ID: "acme", WorkspaceTier: "T3"}},
	}, "?forceUpdate=true")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body=%s", rec.Code, rec.Body.String())
	}
	if len(resp.Tenants.Errors) != 1 {
		t.Fatalf("errors = %+v, want one ratchet error", resp.Tenants.Errors)
	}
	row, _ := tenants.Get(context.Background(), "acme")
	if row.WorkspaceTier != "T4" {
		t.Errorf("tier = %q, want T4 preserved", row.WorkspaceTier)
	}
}

// spec: §12.5 line 301 — a seed that creates a T4 tenant runs the
// admin-time KMS probe; a probe failure rejects the entry per-entry and
// does not persist the tenant.
func TestBootstrapT4CreateProbeFailure(t *testing.T) {
	probe := newFakeKMSProbe()
	probe.failNext = errors.New("kms key unreachable")
	tenants := tenantstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithKMSProbe(probe)

	rec, resp := postBootstrap(t, router, admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{{ID: "acme", WorkspaceTier: "T4"}},
	}, "")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 207; body=%s", rec.Code, rec.Body.String())
	}
	if len(resp.Tenants.Errors) != 1 {
		t.Fatalf("errors = %+v, want one probe failure", resp.Tenants.Errors)
	}
	if _, err := tenants.Get(context.Background(), "acme"); err == nil {
		t.Error("T4 tenant must not be persisted when the KMS probe fails")
	}
}

// A T4 seed create with a healthy KMS key probes once and persists.
func TestBootstrapT4CreateProbeSuccess(t *testing.T) {
	probe := newFakeKMSProbe()
	tenants := tenantstore.NewMemory()
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithKMSProbe(probe)

	rec, resp := postBootstrap(t, router, admin.BootstrapRequest{
		Tenants: []admin.TenantPayload{{ID: "acme", WorkspaceTier: "T4"}},
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if resp.Tenants.CreatedCount != 1 {
		t.Fatalf("createdCount = %d, want 1", resp.Tenants.CreatedCount)
	}
	if len(probe.probes) != 1 || probe.probes[0] != "acme:T4" {
		t.Errorf("probes = %v, want one acme:T4", probe.probes)
	}
	row, err := tenants.Get(context.Background(), "acme")
	if err != nil || row.WorkspaceTier != "T4" {
		t.Errorf("tenant = %+v (err=%v), want stored at T4", row, err)
	}
}
