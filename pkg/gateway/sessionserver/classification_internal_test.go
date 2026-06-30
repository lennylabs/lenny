// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// spec: §12.9 line 1048 — requireTenantClassification rejects a session
// create for a tenant whose workspaceTier is not a recognized §12.9 tier
// with 422 CLASSIFICATION_CONTROL_VIOLATION.
func TestRequireTenantClassificationRejectsStaleTier(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", WorkspaceTier: "T2"})
	s := &Server{tenants: store}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantClassification(rec, req, "acme"); ok {
		t.Fatal("requireTenantClassification admitted a misconfigured tier")
	}
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := decodeEnvelope(t, rec)
	if body.Code != "CLASSIFICATION_CONTROL_VIOLATION" {
		t.Errorf("code = %q, want CLASSIFICATION_CONTROL_VIOLATION", body.Code)
	}
	if body.Details["reason"] != "invalid_workspace_tier" {
		t.Errorf("reason = %v, want invalid_workspace_tier", body.Details["reason"])
	}
	if body.Details["tier"] != "T2" {
		t.Errorf("tier = %v, want T2", body.Details["tier"])
	}
}

// A recognized tier (T4) admits the create.
func TestRequireTenantClassificationAdmitsValidTier(t *testing.T) {
	store := tenantstore.NewMemory()
	_ = store.Create(context.Background(), tenantstore.Tenant{ID: "acme", WorkspaceTier: "T4"})
	s := &Server{tenants: store}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantClassification(rec, req, "acme"); !ok {
		t.Fatalf("requireTenantClassification rejected a valid T4 tenant: %s", rec.Body.String())
	}
}

// An unknown tenant carries no classification to validate; the create
// proceeds (the §10.2 tenant-claim path governs unknown tenants).
func TestRequireTenantClassificationUnknownTenantAdmits(t *testing.T) {
	s := &Server{tenants: tenantstore.NewMemory()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	if ok := s.requireTenantClassification(rec, req, "ghost"); !ok {
		t.Fatalf("unknown tenant must admit, got %d %s", rec.Code, rec.Body.String())
	}
}

// spec: §15.2.1 (classification consistency) — the session writeError
// routes through the status-aware errorclassify.ClassifyStatus so an
// unmapped code resolves from its HTTP status rather than the code-only
// (TRANSIENT, true) fallback. An unmapped non-5xx (here 409) code
// classifies (PERMANENT, false), matching the admin surface (W3, 0018);
// an unmapped 5xx or 429 stays (TRANSIENT, true). A mapped code keeps
// its catalog pair regardless of status.
func TestWriteErrorClassifiesUnmappedCodeByStatus(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		code          string
		wantCategory  string
		wantRetryable bool
	}{
		{
			name:          "unmapped non-5xx is permanent and not retryable",
			status:        http.StatusConflict,
			code:          "SESSION_TOPOLOGY_NONSENSE", // not in the errorclassify table
			wantCategory:  "PERMANENT",
			wantRetryable: false,
		},
		{
			name:          "unmapped 5xx stays transient and retryable",
			status:        http.StatusServiceUnavailable,
			code:          "SESSION_TOPOLOGY_NONSENSE",
			wantCategory:  "TRANSIENT",
			wantRetryable: true,
		},
		{
			name:          "mapped permanent code keeps its catalog pair under any status",
			status:        http.StatusServiceUnavailable, // 5xx, but the table wins
			code:          "VALIDATION_ERROR",
			wantCategory:  "PERMANENT",
			wantRetryable: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{}
			rec := httptest.NewRecorder()
			s.writeError(rec, tc.status, tc.code, "test", nil)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			body := decodeEnvelope(t, rec)
			if body.Code != tc.code {
				t.Errorf("code = %q, want %q", body.Code, tc.code)
			}
			if body.Category != tc.wantCategory {
				t.Errorf("category = %q, want %q", body.Category, tc.wantCategory)
			}
			if body.Retryable != tc.wantRetryable {
				t.Errorf("retryable = %v, want %v", body.Retryable, tc.wantRetryable)
			}
		})
	}
}
