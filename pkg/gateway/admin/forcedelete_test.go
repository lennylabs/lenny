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
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §12.8 lines 880-889; §24.10 row 4 —
// POST /v1/admin/tenants/{id}/force-delete. F-12.8.2, F-24.10.2, F-24.10.5.

func newForceDeleteAdmin(t *testing.T) (*admin.Router, *tenantstore.Memory, sessionstore.Store, *recordingAudit) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	sessions := memstore.New()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithSessions(sessions)
	return router, tenants, sessions, audit
}

func forceDelete(t *testing.T, h http.Handler, id string, body admin.ForceDeleteTenantRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/"+id+"/force-delete", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// An unheld tenant force-delete is an ordinary deletion: 202, lifecycle
// initiated, no override stamped.
func TestForceDeleteUnheldTenant_spec_24_10(t *testing.T) {
	router, tenants, _, _ := newForceDeleteAdmin(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})

	rr := forceDelete(t, router.Handler(), "acme", admin.ForceDeleteTenantRequest{}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rr.Code, rr.Body.String())
	}
	row, _ := tenants.Get(context.Background(), "acme")
	if row.State != tenantstore.TenantStateDisabling {
		t.Errorf("state = %q, want disabling", row.State)
	}
	if row.ForceDeleteHoldOverride {
		t.Error("an unheld force-delete must not stamp the override")
	}
}

// §12.8 line 889: force-delete without acknowledgeHoldOverride while
// active holds exist is rejected, not silently overridden.
func TestForceDeleteHeldTenantWithoutOverrideBlocked_spec_12_8_889(t *testing.T) {
	router, tenants, sessions, audit := newForceDeleteAdmin(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	seedSession(t, sessions, sessionstore.Session{ID: "sess_1", TenantID: "acme", UserID: "alice@acme.com", LegalHold: true})

	rr := forceDelete(t, router.Handler(), "acme", admin.ForceDeleteTenantRequest{}, withAdminPrincipal)
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD")) {
		t.Errorf("body missing TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD: %s", rr.Body.String())
	}
	row, _ := tenants.Get(context.Background(), "acme")
	if row.State == tenantstore.TenantStateDisabling {
		t.Error("a blocked force-delete must not transition the tenant")
	}
	if snap := audit.snapshot(); len(snap) == 0 || snap[len(snap)-1].Type != "admin.tenant.deletion_blocked" {
		t.Errorf("audit: %+v, want a trailing admin.tenant.deletion_blocked", snap)
	}
}

// §12.8 line 880: a valid override stamps the durable override fields and
// initiates the lifecycle so the controller escrows at Phase 3.5.
func TestForceDeleteHeldTenantWithOverride_spec_12_8_880(t *testing.T) {
	router, tenants, sessions, audit := newForceDeleteAdmin(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	seedSession(t, sessions, sessionstore.Session{ID: "sess_1", TenantID: "acme", UserID: "alice@acme.com", LegalHold: true})

	rr := forceDelete(t, router.Handler(), "acme",
		admin.ForceDeleteTenantRequest{AcknowledgeHoldOverride: true, Justification: "business wind-down"}, withAdminPrincipal)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rr.Code, rr.Body.String())
	}
	row, _ := tenants.Get(context.Background(), "acme")
	if !row.ForceDeleteHoldOverride {
		t.Error("override not stamped on tenant row")
	}
	if row.ForceDeleteJustification != "business wind-down" {
		t.Errorf("justification = %q", row.ForceDeleteJustification)
	}
	if row.ForceDeleteBy != "admin@acme.com" {
		t.Errorf("overrideBy = %q, want the platform-admin subject", row.ForceDeleteBy)
	}
	if row.ForceDeleteAt.IsZero() {
		t.Error("overrideAt not stamped")
	}
	if row.State != tenantstore.TenantStateDisabling {
		t.Errorf("state = %q, want disabling", row.State)
	}
	if snap := audit.snapshot(); len(snap) == 0 || snap[len(snap)-1].Type != "admin.tenant.force_delete_initiated" {
		t.Errorf("audit: %+v, want admin.tenant.force_delete_initiated", snap)
	}
}

// §12.8 line 880: the override requires a non-empty justification.
func TestForceDeleteOverrideRequiresJustification_spec_12_8_880(t *testing.T) {
	router, tenants, sessions, _ := newForceDeleteAdmin(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	seedSession(t, sessions, sessionstore.Session{ID: "sess_1", TenantID: "acme", UserID: "alice@acme.com", LegalHold: true})

	rr := forceDelete(t, router.Handler(), "acme",
		admin.ForceDeleteTenantRequest{AcknowledgeHoldOverride: true, Justification: ""}, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("INVALID_REQUEST")) {
		t.Errorf("body missing INVALID_REQUEST: %s", rr.Body.String())
	}
	row, _ := tenants.Get(context.Background(), "acme")
	if row.ForceDeleteHoldOverride {
		t.Error("override must not stamp without a justification")
	}
}

// §12.8 line 880: a tenant-admin cannot self-override.
func TestForceDeleteTenantAdminForbidden_spec_12_8_880(t *testing.T) {
	router, tenants, _, _ := newForceDeleteAdmin(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})

	rr := forceDelete(t, router.Handler(), "acme",
		admin.ForceDeleteTenantRequest{AcknowledgeHoldOverride: true, Justification: "x"}, withTenantAdminPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rr.Code, rr.Body.String())
	}
}

// A force-delete on a tombstoned tenant reads as not-found.
func TestForceDeleteTombstoneNotFound_spec_24_10(t *testing.T) {
	router, tenants, _, _ := newForceDeleteAdmin(t)
	_ = tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"})
	_ = tenants.SoftDelete(context.Background(), "acme", time.Now().UTC())

	rr := forceDelete(t, router.Handler(), "acme",
		admin.ForceDeleteTenantRequest{AcknowledgeHoldOverride: true, Justification: "x"}, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
