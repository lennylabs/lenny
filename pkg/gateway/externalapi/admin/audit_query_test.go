// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/audit/ocsf"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §25.9 Audit Log Query API.
// spec: §4.4 line 232 — OCSF audit-egress translator.

func newAuditQueryRouter(t *testing.T) (*admin.Router, *audit.ChainSet) {
	t.Helper()
	chains := audit.NewChainSet()
	store := tenantstore.NewMemory()
	router := admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: admin.NewChainAuditSink(chains, func() time.Time {
			return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		}),
	}).WithAuditChains(chains)
	return router, chains
}

// TestListAuditEventsReturnsOCSFEnvelope confirms the §4.4 line 232
// audit-egress contract: the list endpoint returns OCSF v1.1.0
// records wrapped in the envelope with `ocsfVersion` and
// `translatorVersion`.
func TestListAuditEventsReturnsOCSFEnvelope(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	// Generate some audit rows by creating tenants.
	for _, id := range []string{"acme", "globex"} {
		body, _ := json.Marshal(admin.TenantPayload{ID: id})
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(
			httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
		))
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %s: %d", id, rr.Code)
		}
	}

	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform", nil),
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if resp.OCSFVersion != ocsf.Version {
		t.Errorf("ocsfVersion = %q, want %q", resp.OCSFVersion, ocsf.Version)
	}
	if resp.TranslatorVersion != ocsf.TranslatorVersion {
		t.Errorf("translatorVersion = %q, want %q", resp.TranslatorVersion, ocsf.TranslatorVersion)
	}
	if resp.TenantID != "platform" {
		t.Errorf("tenantId = %q, want platform", resp.TenantID)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: got %d, want 2", len(resp.Items))
	}
	// Each item is a JSON OCSF record with the required keys.
	var first map[string]any
	if err := json.Unmarshal(resp.Items[0], &first); err != nil {
		t.Fatalf("items[0] is not JSON: %v", err)
	}
	for _, key := range []string{"class_uid", "category_uid", "activity_id", "type_uid", "time", "severity_id", "metadata", "unmapped"} {
		if _, ok := first[key]; !ok {
			t.Errorf("items[0] missing required OCSF key %q", key)
		}
	}
	// metadata.version matches the OCSF wire version pin.
	meta, _ := first["metadata"].(map[string]any)
	if meta["version"] != ocsf.Version {
		t.Errorf("items[0].metadata.version = %v, want %q", meta["version"], ocsf.Version)
	}
	// unmapped.lenny_chain carries the chain extension fields so an
	// external auditor can verify the chain from the OCSF record alone.
	unmapped, _ := first["unmapped"].(map[string]any)
	chain, _ := unmapped["lenny_chain"].(map[string]any)
	if chain == nil {
		t.Errorf("items[0].unmapped.lenny_chain missing")
	}
	if _, hasPrev := chain["prev_hash"]; !hasPrev {
		t.Errorf("items[0].unmapped.lenny_chain.prev_hash missing")
	}
}

// TestGetAuditEventReturnsOCSFEnvelope confirms the single-event
// endpoint also returns the OCSF envelope.
func TestGetAuditEventReturnsOCSFEnvelope(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body)),
	))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/1?tenantId=platform", nil),
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp admin.AuditEventEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if resp.OCSFVersion != ocsf.Version {
		t.Errorf("ocsfVersion = %q, want %q", resp.OCSFVersion, ocsf.Version)
	}
	if resp.TranslatorVersion != ocsf.TranslatorVersion {
		t.Errorf("translatorVersion = %q, want %q", resp.TranslatorVersion, ocsf.TranslatorVersion)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1", len(resp.Items))
	}
}

func TestGetAuditEventMissing(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/99?tenantId=platform", nil),
	))
	if rr.Code != http.StatusNotFound {
		t.Errorf("status: %d, want 404", rr.Code)
	}
}

func TestAuditQueryTenantAdminScopedToOwnTenant(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	// tenant-admin requesting another tenant's chain → 403.
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withTenantAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=globex", nil),
	))
	if rr.Code != http.StatusForbidden {
		t.Errorf("cross-tenant audit read: got %d, want 403", rr.Code)
	}
}

func TestAuditQueryRejectsRegularUser(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events", nil))
	if rr.Code != http.StatusForbidden {
		t.Errorf("anonymous: got %d, want 403", rr.Code)
	}
}

// spec: §25.9 line 3659 — limit default 100, max 1000. An out-of-range
// or unparseable value MUST surface as 400 INVALID_ARGUMENT rather than
// being silently coerced to the default.
func TestListAuditEventsRejectsOutOfRangeLimit_spec_25_9_3659(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	for _, q := range []string{"0", "-1", "1001", "abc", "9999999999"} {
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(
			httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform&limit="+q, nil),
		))
		if rr.Code != http.StatusBadRequest {
			t.Errorf("limit=%q: got %d, want 400", q, rr.Code)
		}
	}
}

// spec: §25.9 line 3659 — limit values inside 1..1000 MUST succeed; the
// boundary values 1 and 1000 are valid.
func TestListAuditEventsAcceptsBoundaryLimits_spec_25_9_3659(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	for _, q := range []string{"1", "100", "1000"} {
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, withAdminPrincipal(
			httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform&limit="+q, nil),
		))
		if rr.Code != http.StatusOK {
			t.Errorf("limit=%q: got %d, want 200", q, rr.Code)
		}
	}
}
