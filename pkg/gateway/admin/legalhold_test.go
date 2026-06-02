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
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fakeArtifactHolder is an in-memory admin.ArtifactLegalHolder for the
// §12.8 artifact-scoped legal-hold tests.
type fakeArtifactHolder struct {
	records map[string]artifactcatalog.Record // keyed by URI
}

func newFakeArtifactHolder() *fakeArtifactHolder {
	return &fakeArtifactHolder{records: map[string]artifactcatalog.Record{}}
}

func (f *fakeArtifactHolder) Get(_ context.Context, uri string) (artifactcatalog.Record, error) {
	r, ok := f.records[uri]
	if !ok {
		return artifactcatalog.Record{}, artifactcatalog.ErrNotFound
	}
	return r, nil
}

func (f *fakeArtifactHolder) SetLegalHold(_ context.Context, uri string, hold bool) error {
	r, ok := f.records[uri]
	if !ok {
		return artifactcatalog.ErrNotFound
	}
	r.LegalHold = hold
	f.records[uri] = r
	return nil
}

func (f *fakeArtifactHolder) IsLegalHeldAt(_ context.Context, tenantID, sessionID string) (bool, error) {
	for _, r := range f.records {
		if r.TenantID == tenantID && r.SessionID == sessionID && r.LegalHold {
			return true, nil
		}
	}
	return false, nil
}

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

func TestSetLegalHoldRequiresSessionOrArtifact(t *testing.T) {
	router, _, _ := newLegalHoldAdmin(t)
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", Hold: true}, withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing sessionId/artifactId: status %d, want 400", rr.Code)
	}
}

// spec: §12.8 line 735 — exactly one of sessionId / artifactId.
func TestSetLegalHoldRejectsBothScopes(t *testing.T) {
	router, sessions, _ := newLegalHoldAdmin(t)
	seedSession(t, sessions, sessionstore.Session{ID: "sess_x", TenantID: "acme", UserID: "alice@acme.com"})
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", SessionID: "sess_x", ArtifactID: "blob://a", Hold: true},
		withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("both sessionId and artifactId: status %d, want 400", rr.Code)
	}
}

// spec: §12.8 line 735 — POST /v1/admin/legal-hold accepts an artifact
// ID and flips artifacts.legal_hold.
func TestSetArtifactLegalHold(t *testing.T) {
	sessions := memstore.New()
	audit := &recordingAudit{}
	holder := newFakeArtifactHolder()
	holder.records["blob://acme/s1/file"] = artifactcatalog.Record{
		URI: "blob://acme/s1/file", TenantID: "acme", SessionID: "s1",
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithSessions(sessions).WithArtifactLegalHold(holder)

	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", ArtifactID: "blob://acme/s1/file", Hold: true},
		withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("set artifact hold: status %d, body %s", rr.Code, rr.Body.String())
	}
	if !holder.records["blob://acme/s1/file"].LegalHold {
		t.Error("POST artifactId hold:true must set legal_hold on the artifact")
	}
	snap := audit.snapshot()
	if len(snap) != 1 || snap[0].Type != "legal_hold.set" {
		t.Fatalf("audit: %+v, want one legal_hold.set", snap)
	}
	if snap[0].Detail["resourceType"] != "artifact" {
		t.Errorf("event resourceType = %v, want artifact", snap[0].Detail["resourceType"])
	}

	// A clear flips it back.
	rr = setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", ArtifactID: "blob://acme/s1/file", Hold: false},
		withAdminPrincipal)
	if rr.Code != http.StatusOK || holder.records["blob://acme/s1/file"].LegalHold {
		t.Errorf("clear artifact hold: status %d, held=%v", rr.Code, holder.records["blob://acme/s1/file"].LegalHold)
	}
}

// A cross-tenant or unknown artifact reads as not-found so the catalog
// is not probed across the tenant boundary.
func TestSetArtifactLegalHoldCrossTenantNotFound(t *testing.T) {
	sessions := memstore.New()
	holder := newFakeArtifactHolder()
	holder.records["blob://globex/s1/file"] = artifactcatalog.Record{
		URI: "blob://globex/s1/file", TenantID: "globex", SessionID: "s1",
	}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: &recordingAudit{},
	}).WithSessions(sessions).WithArtifactLegalHold(holder)

	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", ArtifactID: "blob://globex/s1/file", Hold: true},
		withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant artifact: status %d, want 404", rr.Code)
	}
	if holder.records["blob://globex/s1/file"].LegalHold {
		t.Error("a cross-tenant artifact hold must not be applied")
	}
}

// Without an artifact holder wired, an artifactId request is rejected.
func TestSetArtifactLegalHoldUnavailable(t *testing.T) {
	router, _, _ := newLegalHoldAdmin(t)
	rr := setLegalHold(t, router.Handler(),
		admin.LegalHoldRequest{TenantID: "acme", ArtifactID: "blob://acme/s1/file", Hold: true},
		withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("artifact hold without holder: status %d, want 400", rr.Code)
	}
}
