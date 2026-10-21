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

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §24.11 — platform-admin session investigation:
// GET /v1/admin/sessions/{id} and POST .../force-terminate. F-24.11.2 /
// F-24.11.3.

// fakeSessionAdmin is an in-memory admin.SessionAdmin: GetByID resolves
// across the seeded set, and ForceTerminate applies the §24.11
// transition to a row of the named tenant (idempotent on an
// already-terminal row). forceCalls records every ForceTerminate call as
// tenant/id, so a test can assert what the handler passed.
type fakeSessionAdmin struct {
	rows       map[string]sessionstore.Session
	terminated []string
	forceCalls []string
	forceErr   error
}

func (f *fakeSessionAdmin) GetByID(_ context.Context, id string) (sessionstore.Session, error) {
	s, ok := f.rows[id]
	if !ok {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return s, nil
}

func (f *fakeSessionAdmin) ForceTerminate(_ context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error) {
	f.forceCalls = append(f.forceCalls, tenantID+"/"+id)
	if f.forceErr != nil {
		return sessionstore.Session{}, "", false, f.forceErr
	}
	s, ok := f.rows[id]
	if !ok || s.TenantID != tenantID {
		return sessionstore.Session{}, "", false, sessionstore.ErrNotFound
	}
	prev := s.State
	if session.IsTerminal(s.State) {
		return s, prev, false, nil
	}
	s.State = session.StateFailed
	s.FailureClass = session.FailureClassRuntime
	s.FailureReason = "FORCE_TERMINATED"
	f.rows[id] = s
	f.terminated = append(f.terminated, id)
	return s, prev, true, nil
}

func newSessionAdminRouter(t *testing.T, sa admin.SessionAdmin, audit admin.AuditSink) *admin.Router {
	t.Helper()
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithSessionAdmin(sa)
}

func TestAdminGetSession_Found_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_1": {
			ID: "sess_1", TenantID: "acme", UserID: "alice@acme.com",
			State: session.StateRunning, PodAssignment: "pod-7", RuntimeRef: "rt-python",
		},
	}}
	h := newSessionAdminRouter(t, sa, nil).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/sessions/sess_1", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out["state"] != "running" {
		t.Errorf("state = %v, want running", out["state"])
	}
	if out["assignedPod"] != "pod-7" {
		t.Errorf("assignedPod = %v, want pod-7", out["assignedPod"])
	}
}

func TestAdminGetSession_NotFound_spec_24_11(t *testing.T) {
	h := newSessionAdminRouter(t, &fakeSessionAdmin{rows: map[string]sessionstore.Session{}}, nil).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/sessions/missing", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

func TestAdminGetSession_Forbidden_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_1": {ID: "sess_1", TenantID: "acme", State: session.StateRunning},
	}}
	h := newSessionAdminRouter(t, sa, nil).Handler()
	// A plain user (no admin role) must be rejected before reaching the store.
	req := withUserPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/sessions/sess_1", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rr.Code)
	}
}

func TestAdminForceTerminate_Transitions_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_1": {ID: "sess_1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-7"},
	}}
	audit := &recordingAudit{}
	h := newSessionAdminRouter(t, sa, audit).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/sess_1/force-terminate", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out["state"] != "failed" {
		t.Errorf("state = %v, want failed", out["state"])
	}
	if len(sa.terminated) != 1 || sa.terminated[0] != "sess_1" {
		t.Errorf("ForceTerminate not invoked: %v", sa.terminated)
	}
	events := audit.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Type != "session.force_terminated" {
		t.Errorf("event type = %q, want session.force_terminated", ev.Type)
	}
	if ev.Detail["previous_state"] != "running" {
		t.Errorf("previous_state = %v, want running", ev.Detail["previous_state"])
	}
	if ev.Detail["session_id"] != "sess_1" {
		t.Errorf("session_id = %v, want sess_1", ev.Detail["session_id"])
	}
}

func TestAdminForceTerminate_WithReason_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_1": {ID: "sess_1", TenantID: "acme", State: session.StateRunning},
	}}
	audit := &recordingAudit{}
	h := newSessionAdminRouter(t, sa, audit).Handler()

	body := bytes.NewReader([]byte(`{"reason":"unresponsive runtime"}`))
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/sess_1/force-terminate", body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	ev := audit.snapshot()[0]
	if ev.Detail["reason"] != "unresponsive runtime" {
		t.Errorf("reason = %v, want unresponsive runtime", ev.Detail["reason"])
	}
}

func TestAdminForceTerminate_IdempotentOnTerminal_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_done": {ID: "sess_done", TenantID: "acme", State: session.StateCompleted},
	}}
	audit := &recordingAudit{}
	h := newSessionAdminRouter(t, sa, audit).Handler()

	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/sess_done/force-terminate", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// An already-terminal session is a 200 no-op: no force, no audit event.
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if len(sa.terminated) != 0 {
		t.Errorf("ForceTerminate transitioned a terminal session: %v", sa.terminated)
	}
	if n := len(audit.snapshot()); n != 0 {
		t.Errorf("audit events = %d, want 0 for an idempotent no-op", n)
	}
}

func TestAdminForceTerminate_NotFound_spec_24_11(t *testing.T) {
	h := newSessionAdminRouter(t, &fakeSessionAdmin{rows: map[string]sessionstore.Session{}}, nil).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/missing/force-terminate", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
}

// TestAdminForceTerminate_UnknownIDAnswers404BeforeTransition_spec_24_11
// pins that the handler resolves the id before the forced transition: an
// unknown id answers 404 with no ForceTerminate call.
// spec: §24.11 (Session investigation), §12.8 (Lifecycle fence and drain).
func TestAdminForceTerminate_UnknownIDAnswers404BeforeTransition_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{}}
	h := newSessionAdminRouter(t, sa, nil).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/missing/force-terminate", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rr.Code)
	}
	if len(sa.forceCalls) != 0 {
		t.Errorf("ForceTerminate called for an unknown id: %v", sa.forceCalls)
	}
}

// TestAdminForceTerminate_PassesRowTenant_spec_24_11 pins that the
// forced transition runs under the tenant of the row the handler
// resolved, since the operator supplies only the id.
// spec: §24.11 (Session investigation), §12.8 (Lifecycle fence and drain).
func TestAdminForceTerminate_PassesRowTenant_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_g": {ID: "sess_g", TenantID: "globex", State: session.StateRunning},
	}}
	h := newSessionAdminRouter(t, sa, nil).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/sess_g/force-terminate", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rr.Code, rr.Body.String())
	}
	if len(sa.forceCalls) != 1 || sa.forceCalls[0] != "globex/sess_g" {
		t.Errorf("ForceTerminate calls = %v, want [globex/sess_g]", sa.forceCalls)
	}
}

// TestAdminForceTerminate_LookupErrorAnswers500_spec_24_11 pins that a
// resolution error other than not-found answers 500 with no transition.
// spec: §24.11 (Session investigation).
func TestAdminForceTerminate_LookupErrorAnswers500_spec_24_11(t *testing.T) {
	sa := &erroringLookupAdmin{}
	h := newSessionAdminRouter(t, sa, nil).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/sess_1/force-terminate", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rr.Code)
	}
	if sa.forced {
		t.Error("ForceTerminate called after a lookup error")
	}
}

// TestAdminForceTerminate_TransitionErrorAnswers500_spec_24_11 pins that
// a transition error after a successful lookup answers 500.
// spec: §24.11 (Session investigation).
func TestAdminForceTerminate_TransitionErrorAnswers500_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{
		rows: map[string]sessionstore.Session{
			"sess_1": {ID: "sess_1", TenantID: "acme", State: session.StateRunning},
		},
		forceErr: errors.New("primary unavailable"),
	}
	h := newSessionAdminRouter(t, sa, nil).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/sess_1/force-terminate", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rr.Code)
	}
}

// erroringLookupAdmin is an admin.SessionAdmin whose GetByID fails with
// a non-not-found error.
type erroringLookupAdmin struct{ forced bool }

func (e *erroringLookupAdmin) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("read pool unavailable")
}

func (e *erroringLookupAdmin) ForceTerminate(context.Context, string, string) (sessionstore.Session, session.State, bool, error) {
	e.forced = true
	return sessionstore.Session{}, "", false, nil
}

func TestAdminForceTerminate_MalformedBody_spec_24_11(t *testing.T) {
	sa := &fakeSessionAdmin{rows: map[string]sessionstore.Session{
		"sess_1": {ID: "sess_1", TenantID: "acme", State: session.StateRunning},
	}}
	h := newSessionAdminRouter(t, sa, nil).Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost,
		"/v1/admin/sessions/sess_1/force-terminate", bytes.NewReader([]byte(`{not-json`))))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rr.Code)
	}
	if len(sa.terminated) != 0 {
		t.Errorf("a malformed body must not force-terminate")
	}
}

func TestAdminSessionRoutesUnregisteredWhenNil_spec_24_11(t *testing.T) {
	// With no SessionAdmin wired the §24.11 routes are absent, so the
	// request falls through to the default mux 404.
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	h := router.Handler()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/sessions/sess_1", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404 (route unregistered)", rr.Code)
	}
}
