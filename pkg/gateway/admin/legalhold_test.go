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

// spec: §12.8 legal hold — POST /v1/admin/legal-hold.

func newLegalHoldAdmin(t *testing.T) (*admin.Router, sessionstore.Store, *recordingAudit) {
	t.Helper()
	sessions := memstore.New()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithSessions(sessions)
	return router, sessions, audit
}

func seedSession(t *testing.T, store sessionstore.Store, s sessionstore.Session) {
	t.Helper()
	if s.State == "" {
		s.State = session.StateRunning
	}
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatalf("seed session %q: %v", s.ID, err)
	}
}

func setLegalHold(t *testing.T, h http.Handler, body admin.LegalHoldRequest, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := as(httptest.NewRequest(http.MethodPost, "/v1/admin/legal-hold", bytes.NewReader(b)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestSetLegalHold(t *testing.T) {
	router, sessions, audit := newLegalHoldAdmin(t)
	seedSession(t, sessions, sessionstore.Session{ID: "sess_1", TenantID: "acme", UserID: "alice@acme.com"})

	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "sess_1", Hold: true}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("set hold: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, err := sessions.Get(context.Background(), "acme", "sess_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.LegalHold {
		t.Error("POST hold:true must set LegalHold on the session")
	}
	if snap := audit.snapshot(); len(snap) != 1 || snap[0].Type != "legal_hold.set" {
		t.Errorf("audit: %+v, want one legal_hold.set", snap)
	}
}

func TestClearLegalHold(t *testing.T) {
	router, sessions, audit := newLegalHoldAdmin(t)
	seedSession(t, sessions, sessionstore.Session{
		ID: "sess_2", TenantID: "acme", UserID: "bob@acme.com", LegalHold: true,
	})

	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "sess_2", Hold: false}, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear hold: status %d, body %s", rr.Code, rr.Body.String())
	}
	got, _ := sessions.Get(context.Background(), "acme", "sess_2")
	if got.LegalHold {
		t.Error("POST hold:false must clear LegalHold on the session")
	}
	if snap := audit.snapshot(); len(snap) != 1 || snap[0].Type != "legal_hold.cleared" {
		t.Errorf("audit: %+v, want one legal_hold.cleared", snap)
	}
}

func TestSetLegalHoldNotFound(t *testing.T) {
	router, _, _ := newLegalHoldAdmin(t)
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "sess_absent", Hold: true}, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown session: status %d, want 404", rr.Code)
	}
}

func TestSetLegalHoldRequiresPlatformAdmin(t *testing.T) {
	router, sessions, _ := newLegalHoldAdmin(t)
	seedSession(t, sessions, sessionstore.Session{ID: "sess_3", TenantID: "acme", UserID: "carol@acme.com"})

	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "sess_3", Hold: true}, withTenantAdminPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("tenant-admin set hold: status %d, want 403", rr.Code)
	}
	got, _ := sessions.Get(context.Background(), "acme", "sess_3")
	if got.LegalHold {
		t.Error("a tenant-admin must not be able to set a legal hold")
	}
}

func TestSetLegalHoldRequiresSessionID(t *testing.T) {
	router, _, _ := newLegalHoldAdmin(t)
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", Hold: true}, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing sessionId: status %d, want 400", rr.Code)
	}
}
