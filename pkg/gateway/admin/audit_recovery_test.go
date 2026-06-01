// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/common/scopes"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §25.9 line 3653 (raw-canonical), line 3662 (retranslate);
// §11.7 line 424 (DEADLETTER_REDACTED on a redacted dead-letter row).

// withAuditScopePrincipal authenticates as a platform-admin carrying
// the §25.1 scope claim, so HasScope enforces the per-endpoint scope
// rather than deferring to the role ceiling.
func withAuditScopePrincipal(t *testing.T, req *http.Request, claim string) *http.Request {
	t.Helper()
	set, err := scopes.Parse(claim)
	if err != nil {
		t.Fatalf("parse scope claim %q: %v", claim, err)
	}
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@acme.com",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
		Scopes:   set,
	})
	return req.WithContext(ctx)
}

// fakeTranslationLog is an admin.AuditLog that also tracks §11.7
// ocsf_translation_state per row, so the retranslate endpoint's
// eligibility branches are exercisable without Postgres.
type fakeTranslationLog struct {
	rows  []audit.Row
	state map[uint64]audit.OCSFTranslationState
	set   map[uint64]audit.OCSFTranslationState // captures SetTranslationState calls
}

func (f *fakeTranslationLog) Append(context.Context, string, string, json.RawMessage, time.Time) (audit.Row, error) {
	return audit.Row{}, nil
}

func (f *fakeTranslationLog) Rows(_ context.Context, _ string) ([]audit.Row, error) {
	return f.rows, nil
}

func (f *fakeTranslationLog) Verify(context.Context, string) (audit.VerifyResult, error) {
	return audit.VerifyResult{Integrity: audit.ChainVerified}, nil
}

func (f *fakeTranslationLog) TranslationState(_ context.Context, _ string, seq uint64) (audit.OCSFTranslationState, int, error) {
	return f.state[seq], 0, nil
}

func (f *fakeTranslationLog) SetTranslationState(_ context.Context, _ string, seq uint64, state audit.OCSFTranslationState, _ int) error {
	if f.set == nil {
		f.set = map[uint64]audit.OCSFTranslationState{}
	}
	f.set[seq] = state
	return nil
}

func newFakeAuditRouter(fake *fakeTranslationLog) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithAuditLog(fake)
}

// TestGetAuditEventRawCanonical_spec_25_9_3653 confirms the
// scope-gated raw-canonical wire form returns the pre-OCSF tuple with
// the hash-chain fields a chain auditor recomputes against.
func TestGetAuditEventRawCanonical_spec_25_9_3653(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/1?tenantId=platform&format=raw-canonical", nil),
		"tools:audit:raw_canonical_read"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, body=%s", rr.Code, rr.Body.String())
	}
	var tuple admin.AuditEventPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &tuple); err != nil {
		t.Fatalf("decode canonical tuple: %v", err)
	}
	if tuple.Seq != 1 || tuple.Hash == "" || tuple.PrevHash == "" {
		t.Fatalf("canonical tuple missing hash-chain fields: %+v", tuple)
	}
	// The raw-canonical form is NOT the OCSF envelope.
	if strings.Contains(rr.Body.String(), "ocsfVersion") {
		t.Errorf("raw-canonical response leaked the OCSF envelope: %s", rr.Body.String())
	}
}

// TestGetAuditEventRawCanonical_forbiddenWithoutScope confirms a token
// that carries a scope claim lacking audit:raw-canonical:read is
// rejected with 403.
func TestGetAuditEventRawCanonical_forbiddenWithoutScope(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events/1?tenantId=platform&format=raw-canonical", nil),
		"tools:audit:read"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: %d, want 403, body=%s", rr.Code, rr.Body.String())
	}
}

// TestListAuditEventsTranslationStateFilter_spec_25_9_3659 confirms the
// ?ocsf_translation_state filter validates the enum and, on the
// inline-translating in-memory backend, returns every row for
// `succeeded` and none for `dead_lettered`.
func TestListAuditEventsTranslationStateFilter_spec_25_9_3659(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}

	// Invalid value → 400.
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform&ocsf_translation_state=bogus", nil)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid state: status %d, want 400", rr.Code)
	}

	// succeeded → all rows present.
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform&ocsf_translation_state=succeeded", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("succeeded filter: status %d", rr.Code)
	}
	var got admin.AuditEventEnvelope
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Items) == 0 {
		t.Errorf("succeeded filter returned no rows on inline-translating backend")
	}

	// dead_lettered → empty on the inline-translating backend.
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodGet, "/v1/admin/audit-events?tenantId=platform&ocsf_translation_state=dead_lettered", nil)))
	if rr.Code != http.StatusOK {
		t.Fatalf("dead_lettered filter: status %d", rr.Code)
	}
	got = admin.AuditEventEnvelope{}
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Items) != 0 {
		t.Errorf("dead_lettered filter returned %d rows on inline backend, want 0", len(got.Items))
	}
}

// TestRetranslateInMemoryNotRetryable confirms a backend without
// translation-state tracking reports every row ineligible (409).
func TestRetranslateInMemoryNotRetryable(t *testing.T) {
	router, _ := newAuditQueryRouter(t)
	body, _ := json.Marshal(admin.TenantPayload{ID: "acme"})
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAdminPrincipal(
		httptest.NewRequest(http.MethodPost, "/v1/admin/tenants", bytes.NewReader(body))))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create tenant: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/retranslate?tenantId=platform", nil),
		"tools:audit:retranslate"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: %d, want 409, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "ocsf_translation_not_retryable") {
		t.Errorf("missing ocsf_translation_not_retryable code: %s", rr.Body.String())
	}
}

func TestRetranslateEligibleDeadLettered(t *testing.T) {
	fake := &fakeTranslationLog{
		rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created"}},
		state: map[uint64]audit.OCSFTranslationState{1: audit.OCSFDeadLettered},
	}
	router := newFakeAuditRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/retranslate?tenantId=platform",
			strings.NewReader(`{"translatorVersion":"2"}`)),
		"tools:audit:retranslate"))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: %d, want 200, body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Seq                  uint64 `json:"seq"`
		OCSFTranslationState string `json:"ocsfTranslationState"`
		TranslatorVersion    string `json:"translatorVersion"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.OCSFTranslationState != "pending" {
		t.Errorf("state = %q, want pending", resp.OCSFTranslationState)
	}
	if resp.TranslatorVersion != "2" {
		t.Errorf("translatorVersion = %q, want 2", resp.TranslatorVersion)
	}
	if fake.set[1] != audit.OCSFPending {
		t.Errorf("SetTranslationState not called with pending: %v", fake.set)
	}
}

// TestRetranslateRedactedDeadLetter_spec_11_7_424 confirms a redacted
// dead-letter row is rejected with 410 DEADLETTER_REDACTED before any
// state transition.
func TestRetranslateRedactedDeadLetter_spec_11_7_424(t *testing.T) {
	fake := &fakeTranslationLog{
		rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created", Redacted: true}},
		state: map[uint64]audit.OCSFTranslationState{1: audit.OCSFDeadLettered},
	}
	router := newFakeAuditRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/retranslate?tenantId=platform", nil),
		"tools:audit:retranslate"))
	if rr.Code != http.StatusGone {
		t.Fatalf("status: %d, want 410, body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "DEADLETTER_REDACTED") {
		t.Errorf("missing DEADLETTER_REDACTED code: %s", rr.Body.String())
	}
	if len(fake.set) != 0 {
		t.Errorf("state transitioned on a redacted dead-letter row: %v", fake.set)
	}
}

func TestRetranslateSucceededNotRetryable(t *testing.T) {
	fake := &fakeTranslationLog{
		rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created"}},
		state: map[uint64]audit.OCSFTranslationState{1: audit.OCSFSucceeded},
	}
	router := newFakeAuditRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/retranslate?tenantId=platform", nil),
		"tools:audit:retranslate"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: %d, want 409, body=%s", rr.Code, rr.Body.String())
	}
}

func TestRetranslateForbiddenWithoutScope(t *testing.T) {
	fake := &fakeTranslationLog{
		rows:  []audit.Row{{Seq: 1, TenantID: "platform", EventType: "admin.tenant.created"}},
		state: map[uint64]audit.OCSFTranslationState{1: audit.OCSFDeadLettered},
	}
	router := newFakeAuditRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/1/retranslate?tenantId=platform", nil),
		"tools:audit:read"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: %d, want 403, body=%s", rr.Code, rr.Body.String())
	}
	if len(fake.set) != 0 {
		t.Errorf("state transitioned without scope: %v", fake.set)
	}
}

// TestRetranslateNotFound confirms a missing seq returns 404.
func TestRetranslateNotFound(t *testing.T) {
	fake := &fakeTranslationLog{state: map[uint64]audit.OCSFTranslationState{}}
	router := newFakeAuditRouter(fake)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, withAuditScopePrincipal(t,
		httptest.NewRequest(http.MethodPost, "/v1/admin/audit-events/99/retranslate?tenantId=platform", nil),
		"tools:audit:retranslate"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: %d, want 404, body=%s", rr.Code, rr.Body.String())
	}
}
