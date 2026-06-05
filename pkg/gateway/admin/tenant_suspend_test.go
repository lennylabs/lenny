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

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// spec: §15.1 lines 818-819 — platform-admin tenant suspend/resume.
// F-15.1.3.

var suspendClock = time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

func newSuspendRouter(store *tenantstore.Memory, sink admin.AuditSink) *admin.Router {
	return admin.NewRouter(store, admin.Options{
		Clock: func() time.Time { return suspendClock },
		Audit: sink,
	})
}

func seedSuspendTenant(t *testing.T, store *tenantstore.Memory, tn tenantstore.Tenant) {
	t.Helper()
	if tn.DisplayName == "" {
		tn.DisplayName = tn.ID
	}
	if err := store.Create(context.Background(), tn); err != nil {
		t.Fatalf("seed tenant %q: %v", tn.ID, err)
	}
}

func suspendErrorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body %s)", err, rr.Body.String())
	}
	return env.Error.Code
}

func TestSuspendTenant_Success_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	body := bytes.NewBufferString(`{"reason":"abuse investigation"}`)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var out tenantSuspendView
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Suspended || out.Reason != "abuse investigation" {
		t.Errorf("payload = %+v, want suspended with reason", out)
	}
	if out.SuspendedBy != "admin@acme.com" {
		t.Errorf("suspendedBy = %q, want admin@acme.com", out.SuspendedBy)
	}
	if out.SuspendedAt == "" {
		t.Error("suspendedAt is empty")
	}

	got, _ := store.Get(context.Background(), "acme")
	if !got.Suspended || got.SuspendedReason != "abuse investigation" || got.SuspendedBy != "admin@acme.com" {
		t.Errorf("stored row = %+v, want suspended with reason and operator", got)
	}
	if got.SuspendedAt != suspendClock {
		t.Errorf("stored suspendedAt = %v, want %v", got.SuspendedAt, suspendClock)
	}
	if !sink.has("tenant.suspended") {
		t.Errorf("no tenant.suspended audit event; got %+v", sink.events)
	}
}

func TestSuspendTenant_Idempotent_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme", Suspended: true, SuspendedReason: "prior"})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if len(sink.events) != 0 {
		t.Errorf("idempotent suspend emitted %d events, want 0", len(sink.events))
	}
}

func TestSuspendTenant_NotFound_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/ghost/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	if code := suspendErrorCode(t, rr); code != "RESOURCE_NOT_FOUND" {
		t.Errorf("code = %q, want RESOURCE_NOT_FOUND", code)
	}
}

func TestSuspendTenant_DeletedConflict_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme", State: tenantstore.TenantStateDeleted})
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", rr.Code)
	}
	if code := suspendErrorCode(t, rr); code != "INVALID_STATE_TRANSITION" {
		t.Errorf("code = %q, want INVALID_STATE_TRANSITION", code)
	}
}

func TestSuspendTenant_MalformedBody_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend",
		bytes.NewBufferString("{not json")))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	got, _ := store.Get(context.Background(), "acme")
	if got.Suspended {
		t.Error("malformed body must not suspend the tenant")
	}
}

func TestSuspendTenant_RequiresPlatformAdmin_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rr.Code)
	}
}

func TestSuspendTenant_DrainsActiveSessions_spec_15_1_818(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})

	sessions := memstore.New()
	rows := map[string]sessionstore.Session{
		"sess_run1": {ID: "sess_run1", TenantID: "acme", State: session.StateRunning},
		"sess_run2": {ID: "sess_run2", TenantID: "acme", State: session.StateInputRequired},
		"sess_term": {ID: "sess_term", TenantID: "acme", State: session.StateCompleted},
		"sess_glob": {ID: "sess_glob", TenantID: "globex", State: session.StateRunning},
	}
	for _, s := range rows {
		if err := sessions.Create(context.Background(), s); err != nil {
			t.Fatalf("seed session %q: %v", s.ID, err)
		}
	}
	sa := &fakeSessionAdmin{rows: rows}
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).WithSessions(sessions).WithSessionAdmin(sa).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/suspend", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var out tenantSuspendView
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.DrainedSessions != 2 {
		t.Errorf("drainedSessions = %d, want 2 (terminal skipped, foreign untouched)", out.DrainedSessions)
	}
	// The foreign-tenant session must be untouched.
	for _, id := range sa.terminated {
		if id == "sess_glob" {
			t.Error("drained a session belonging to another tenant")
		}
	}
	if g := sa.rows["sess_glob"]; g.State != session.StateRunning {
		t.Errorf("globex session state = %q, want running", g.State)
	}
}

func TestResumeTenant_Success_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{
		ID: "acme", Suspended: true, SuspendedReason: "abuse", SuspendedBy: "admin@acme.com", SuspendedAt: suspendClock,
	})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := store.Get(context.Background(), "acme")
	if got.Suspended || got.SuspendedReason != "" || got.SuspendedBy != "" || !got.SuspendedAt.IsZero() {
		t.Errorf("resumed row still carries suspension metadata: %+v", got)
	}
	if !sink.has("tenant.resumed") {
		t.Errorf("no tenant.resumed audit event; got %+v", sink.events)
	}
}

func TestResumeTenant_IdempotentNotSuspended_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme"})
	sink := &recordingSink{}
	h := newSuspendRouter(store, sink).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if len(sink.events) != 0 {
		t.Errorf("idempotent resume emitted %d events, want 0", len(sink.events))
	}
}

func TestResumeTenant_NotFound_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	h := newSuspendRouter(store, &recordingSink{}).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/ghost/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

// Resume restores normal operation but does not un-terminate the
// sessions drained by the suspension. spec: §15.1 line 819.
func TestResumeTenant_DoesNotTouchSessions_spec_15_1_819(t *testing.T) {
	store := tenantstore.NewMemory()
	seedSuspendTenant(t, store, tenantstore.Tenant{ID: "acme", Suspended: true})

	sessions := memstore.New()
	_ = sessions.Create(context.Background(), sessionstore.Session{
		ID: "sess_run", TenantID: "acme", State: session.StateRunning,
	})
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_run": {ID: "sess_run", TenantID: "acme", State: session.StateRunning},
	}}
	h := newSuspendRouter(store, &recordingSink{}).WithSessions(sessions).WithSessionAdmin(sa).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/tenants/acme/resume", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if len(sa.terminated) != 0 {
		t.Errorf("resume force-terminated %v, want none", sa.terminated)
	}
}

// tenantSuspendView mirrors the suspend response payload for decoding.
type tenantSuspendView struct {
	TenantID        string `json:"tenantId"`
	Suspended       bool   `json:"suspended"`
	Reason          string `json:"reason"`
	SuspendedAt     string `json:"suspendedAt"`
	SuspendedBy     string `json:"suspendedBy"`
	DrainedSessions int    `json:"drainedSessions"`
}
