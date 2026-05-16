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

// spec: §9.2 — tenant elicitation-content-integrity admin endpoints.

type integrityResp struct {
	TenantID string `json:"tenantId"`
	Mode     string `json:"mode"`
}

// seedAdminTenant inserts a tenant directly into the store.
func seedAdminTenant(t *testing.T, store *tenantstore.Memory, id string) {
	t.Helper()
	if err := store.Create(context.Background(), tenantstore.Tenant{ID: id}); err != nil {
		t.Fatalf("seed tenant %s: %v", id, err)
	}
}

func TestGetElicitationIntegrityDefaultsToEnforce(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var resp integrityResp
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Mode != "enforce" {
		t.Errorf("mode = %q, want enforce — the §9.2 tenant default", resp.Mode)
	}
}

func TestPutElicitationIntegrityEnforceNeedsNoJustification(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "enforce"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "enforce" {
		t.Errorf("stored mode = %q, want enforce", row.ElicitationContentIntegrity)
	}
}

func TestPutElicitationIntegrityWeakeningRequiresJustification(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "detect-only"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body %s", rr.Code, rr.Body.String())
	}
	var errResp struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &errResp)
	if errResp.Error.Code != "ELICITATION_INTEGRITY_JUSTIFICATION_REQUIRED" {
		t.Errorf("error code = %q, want ELICITATION_INTEGRITY_JUSTIFICATION_REQUIRED", errResp.Error.Code)
	}
	// The mode was not stored.
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "" {
		t.Errorf("stored mode = %q, want unchanged after a rejected weakening", row.ElicitationContentIntegrity)
	}
}

func TestPutElicitationIntegrityWeakensWithJustification(t *testing.T) {
	store := tenantstore.NewMemory()
	audit := &recordingAudit{}
	router := admin.NewRouter(store, admin.Options{
		Audit: audit,
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "off", "justification": "staging tenant, integrity tooling offline"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	row, _ := store.Get(context.Background(), "acme")
	if row.ElicitationContentIntegrity != "off" {
		t.Errorf("stored mode = %q, want off", row.ElicitationContentIntegrity)
	}
	// The weakening emitted the §11.7 audit event.
	var found *admin.AuditEvent
	for _, e := range audit.snapshot() {
		if e.Type == "tenant.elicitation_content_integrity_mode_changed" {
			ev := e
			found = &ev
		}
	}
	if found == nil {
		t.Fatal("no tenant.elicitation_content_integrity_mode_changed audit event")
	}
	if found.Detail["newMode"] != "off" || found.Detail["oldMode"] != "enforce" {
		t.Errorf("audit detail = %v, want oldMode=enforce newMode=off", found.Detail)
	}
}

func TestPutElicitationIntegrityRejectsInvalidMode(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	body, _ := json.Marshal(map[string]string{"mode": "paranoid"})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut,
		"/v1/admin/tenants/acme/elicitation-content-integrity", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400 for an unrecognized mode", rr.Code)
	}
}

func TestElicitationIntegrityRequiresPlatformAdmin(t *testing.T) {
	router, store := newAdminServer(t)
	seedAdminTenant(t, store, "acme")

	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodGet,
		"/v1/admin/tenants/acme/elicitation-content-integrity", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin GET: got %d, want 403", rr.Code)
	}
}
