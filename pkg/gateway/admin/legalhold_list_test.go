// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore/artifactcatalog"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §15.1 line 865 — GET /v1/admin/legal-holds.

type holdItem struct {
	ResourceType string `json:"resourceType"`
	ResourceID   string `json:"resourceId"`
	TenantID     string `json:"tenantId"`
	SetBy        string `json:"setBy"`
	SetAt        string `json:"setAt"`
	Note         string `json:"note"`
}

type holdList struct {
	Items   []holdItem `json:"items"`
	Total   int        `json:"total"`
	HasMore bool       `json:"hasMore"`
	Cursor  string     `json:"cursor"`
}

// newLegalHoldListAdmin wires a router over a session store, a tenant
// store, and (when withHolder) a fake artifact holder. The two tenants
// acme and globex are seeded so the platform-admin list-all path has a
// tenant set to enumerate.
func newLegalHoldListAdmin(t *testing.T, withHolder bool) (*admin.Router, sessionstore.Store, *fakeArtifactHolder) {
	t.Helper()
	sessions := memstore.New()
	tenants := tenantstore.NewMemory()
	for _, id := range []string{"acme", "globex"} {
		if err := tenants.Create(context.Background(), tenantstore.Tenant{ID: id}); err != nil {
			t.Fatalf("seed tenant %q: %v", id, err)
		}
	}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC) },
		Audit: &recordingAudit{},
	}).WithSessions(sessions)
	var holder *fakeArtifactHolder
	if withHolder {
		holder = newFakeArtifactHolder()
		router = router.WithArtifactLegalHold(holder)
	}
	return router, sessions, holder
}

func seedHeldSession(t *testing.T, store sessionstore.Store, id, tenant, setBy, note string, at time.Time) {
	t.Helper()
	seedSession(t, store, sessionstore.Session{
		ID: id, TenantID: tenant, UserID: "alice@" + tenant + ".com",
		State:          session.StateRunning,
		LegalHold:      true,
		LegalHoldSetBy: setBy,
		LegalHoldSetAt: at,
		LegalHoldNote:  note,
	})
}

func listHolds(t *testing.T, h http.Handler, query string, as func(*http.Request) *http.Request) (*httptest.ResponseRecorder, holdList) {
	t.Helper()
	req := as(httptest.NewRequest(http.MethodGet, "/v1/admin/legal-holds"+query, nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var out holdList
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode list: %v (body %s)", err, rr.Body.String())
		}
	}
	return rr, out
}

func TestListLegalHoldsTenantScoped(t *testing.T) {
	router, sessions, _ := newLegalHoldListAdmin(t, false)
	at := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	seedHeldSession(t, sessions, "sess_a", "acme", "admin@acme.com", "incident-1", at)
	// A non-held session in the same tenant must not appear.
	seedSession(t, sessions, sessionstore.Session{ID: "sess_free", TenantID: "acme", UserID: "bob@acme.com"})

	rr, list := listHolds(t, router.Handler(), "?tenant_id=acme", withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d body %s", rr.Code, rr.Body.String())
	}
	if list.Total != 1 || len(list.Items) != 1 {
		t.Fatalf("want one hold, got total=%d items=%d", list.Total, len(list.Items))
	}
	got := list.Items[0]
	if got.ResourceType != "session" || got.ResourceID != "sess_a" || got.TenantID != "acme" {
		t.Errorf("entry identity wrong: %+v", got)
	}
	if got.SetBy != "admin@acme.com" || got.Note != "incident-1" || got.SetAt == "" {
		t.Errorf("provenance not reported: %+v", got)
	}
}

// spec: §15.1 line 865 — a platform-admin omitting tenant_id lists across
// every tenant.
func TestListLegalHoldsAllTenants(t *testing.T) {
	router, sessions, _ := newLegalHoldListAdmin(t, false)
	at := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	seedHeldSession(t, sessions, "sess_a", "acme", "admin@acme.com", "incident-1", at)
	seedHeldSession(t, sessions, "sess_g", "globex", "admin@globex.com", "incident-2", at)

	rr, list := listHolds(t, router.Handler(), "", withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	if list.Total != 2 {
		t.Fatalf("cross-tenant list: total=%d, want 2", list.Total)
	}
	tenantsSeen := map[string]bool{}
	for _, it := range list.Items {
		tenantsSeen[it.TenantID] = true
	}
	if !tenantsSeen["acme"] || !tenantsSeen["globex"] {
		t.Errorf("cross-tenant list missing a tenant: %+v", list.Items)
	}
}

// spec: §15.1 line 865 — a tenant-admin caller is automatically scoped to
// its own tenant; the tenant_id param cannot widen it.
func TestListLegalHoldsTenantAdminScoped(t *testing.T) {
	router, sessions, _ := newLegalHoldListAdmin(t, false)
	at := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	seedHeldSession(t, sessions, "sess_a", "acme", "admin@acme.com", "incident-1", at)
	seedHeldSession(t, sessions, "sess_g", "globex", "admin@globex.com", "incident-2", at)

	// The tenant-admin of acme asks for globex; the scope is forced to acme.
	rr, list := listHolds(t, router.Handler(), "?tenant_id=globex", withTenantAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: status %d", rr.Code)
	}
	if list.Total != 1 || list.Items[0].TenantID != "acme" {
		t.Fatalf("tenant-admin must be scoped to acme, got %+v", list.Items)
	}
}

func TestListLegalHoldsResourceTypeFilter(t *testing.T) {
	router, sessions, holder := newLegalHoldListAdmin(t, true)
	at := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	seedHeldSession(t, sessions, "sess_a", "acme", "admin@acme.com", "incident-1", at)
	holder.records["blob://acme/s/f"] = artifactcatalog.Record{
		URI: "blob://acme/s/f", TenantID: "acme", SessionID: "s",
		LegalHold: true, LegalHoldSetBy: "admin@acme.com", LegalHoldSetAt: at, LegalHoldNote: "art-hold",
	}

	// resource_type=artifact returns only the artifact hold.
	_, list := listHolds(t, router.Handler(), "?tenant_id=acme&resource_type=artifact", withAdminPrincipal)
	if list.Total != 1 || list.Items[0].ResourceType != "artifact" {
		t.Fatalf("artifact filter: %+v", list.Items)
	}
	if list.Items[0].ResourceID != "blob://acme/s/f" || list.Items[0].Note != "art-hold" {
		t.Errorf("artifact entry wrong: %+v", list.Items[0])
	}

	// resource_type=session returns only the session hold.
	_, list = listHolds(t, router.Handler(), "?tenant_id=acme&resource_type=session", withAdminPrincipal)
	if list.Total != 1 || list.Items[0].ResourceType != "session" {
		t.Fatalf("session filter: %+v", list.Items)
	}

	// No filter returns both.
	_, list = listHolds(t, router.Handler(), "?tenant_id=acme", withAdminPrincipal)
	if list.Total != 2 {
		t.Fatalf("unfiltered: total=%d, want 2", list.Total)
	}
}

func TestListLegalHoldsResourceIDFilter(t *testing.T) {
	router, sessions, _ := newLegalHoldListAdmin(t, false)
	at := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	seedHeldSession(t, sessions, "sess_a", "acme", "admin@acme.com", "incident-1", at)
	seedHeldSession(t, sessions, "sess_b", "acme", "admin@acme.com", "incident-2", at)

	_, list := listHolds(t, router.Handler(), "?tenant_id=acme&resource_id=sess_b", withAdminPrincipal)
	if list.Total != 1 || list.Items[0].ResourceID != "sess_b" {
		t.Fatalf("resource_id filter: %+v", list.Items)
	}
}

func TestListLegalHoldsRejectsBadResourceType(t *testing.T) {
	router, _, _ := newLegalHoldListAdmin(t, false)
	rr, _ := listHolds(t, router.Handler(), "?resource_type=workspace", withAdminPrincipal)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad resource_type: status %d, want 400", rr.Code)
	}
}

func TestListLegalHoldsEmpty(t *testing.T) {
	router, _, _ := newLegalHoldListAdmin(t, false)
	rr, list := listHolds(t, router.Handler(), "?tenant_id=acme", withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if list.Total != 0 || len(list.Items) != 0 {
		t.Errorf("empty tenant: total=%d items=%d", list.Total, len(list.Items))
	}
}

// spec: §15.1 line 865 — requires platform-admin or tenant-admin; a plain
// user is forbidden.
func TestListLegalHoldsForbiddenForUser(t *testing.T) {
	router, _, _ := newLegalHoldListAdmin(t, false)
	rr, _ := listHolds(t, router.Handler(), "?tenant_id=acme", withUserPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Errorf("plain user: status %d, want 403", rr.Code)
	}
}

// spec: §15.1 lines 1228-1253 — the canonical cursor-paginated envelope
// bounds the page and exposes the total.
func TestListLegalHoldsPagination(t *testing.T) {
	router, sessions, _ := newLegalHoldListAdmin(t, false)
	base := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"sess_1", "sess_2", "sess_3"} {
		seedHeldSession(t, sessions, id, "acme", "admin@acme.com", "incident", base.Add(time.Duration(i)*time.Minute))
	}
	rr, list := listHolds(t, router.Handler(), "?tenant_id=acme&limit=2", withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if len(list.Items) != 2 || list.Total != 3 || !list.HasMore {
		t.Fatalf("first page: items=%d total=%d hasMore=%v", len(list.Items), list.Total, list.HasMore)
	}
	rr2, page2 := listHolds(t, router.Handler(), "?tenant_id=acme&limit=2&cursor="+list.Cursor, withAdminPrincipal)
	if rr2.Code != http.StatusOK {
		t.Fatalf("page 2 status %d", rr2.Code)
	}
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("second page: items=%d hasMore=%v", len(page2.Items), page2.HasMore)
	}
}
