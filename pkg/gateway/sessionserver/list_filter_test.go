// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// listSessions issues GET /v1/sessions with the supplied raw query and an
// optional request mutator (e.g. to attach a Principal), returning the
// decoded session ids in the §15.1 `{"sessions":[...]}` envelope order.
func listSessions(t *testing.T, h http.Handler, rawQuery string, mods ...func(*http.Request)) []sessionserver.SessionResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions?"+rawQuery, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	for _, m := range mods {
		m(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Sessions []sessionserver.SessionResponse `json:"sessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode list envelope: %v; body=%s", err, rr.Body.String())
	}
	return env.Sessions
}

func listIDs(rows []sessionserver.SessionResponse) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// spec: §15.1 line 598 — GET /v1/sessions is filterable by labels. The
// create path persists the §14 labels and the list `?label=k=v`
// (repeatable, AND) filter narrows on them. F-15.1.15.
func TestListFilterByLabels_spec_15_1_598(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "gold", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(3, 0), Labels: map[string]string{"team": "payments", "tier": "gold"}})
	_ = store.Create(ctx, sessionstore.Session{ID: "silver", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(2, 0), Labels: map[string]string{"team": "payments", "tier": "silver"}})
	_ = store.Create(ctx, sessionstore.Session{ID: "search", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(1, 0), Labels: map[string]string{"team": "search"}})
	srv := sessionserver.New(store, sessionserver.Options{})
	h := srv.Handler()

	got := listSessions(t, h, "label=team=payments")
	if len(got) != 2 {
		t.Fatalf("single-label list: want 2 rows, got %v", listIDs(got))
	}
	// Two repeated label params AND together.
	got = listSessions(t, h, "label=team=payments&label=tier=gold")
	if len(got) != 1 || got[0].ID != "gold" {
		t.Errorf("AND-label list: want [gold], got %v", listIDs(got))
	}
	// The echoed envelope carries the labels back.
	if got[0].Labels["tier"] != "gold" {
		t.Errorf("labels echo: got %v, want tier=gold", got[0].Labels)
	}
}

// spec: §15.1 line 598 — a platform-admin may scope the listing to another
// tenant via `?tenant=`; a non-admin's `?tenant=` is ignored so they only
// ever see their own tenant. F-15.1.15.
func TestListTenantFilterPlatformAdmin_spec_15_1_598(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "acme_s", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(1, 0)})
	_ = store.Create(ctx, sessionstore.Session{ID: "globex_s", TenantID: "globex", State: session.StateRunning, CreatedAt: time.Unix(1, 0)})
	srv := sessionserver.New(store, sessionserver.Options{})
	h := srv.Handler()

	admin := func(req *http.Request) {
		*req = *req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: "root@acme.com", TenantID: "acme",
			Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
		}))
	}
	got := listSessions(t, h, "tenant=globex", admin)
	if len(got) != 1 || got[0].ID != "globex_s" {
		t.Errorf("platform-admin ?tenant=globex: want [globex_s], got %v", listIDs(got))
	}

	// A non-admin principal's ?tenant=globex override is ignored: they see
	// only their own tenant's rows.
	nonAdmin := func(req *http.Request) {
		*req = *req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject: "alice@acme.com", TenantID: "acme",
			Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
		}))
	}
	got = listSessions(t, h, "tenant=globex", nonAdmin)
	if len(got) != 1 || got[0].ID != "acme_s" {
		t.Errorf("non-admin ?tenant=globex must be ignored: want [acme_s], got %v", listIDs(got))
	}
}

// spec: §15.1 lines 652, 661 — derive_failure audit rows are included in
// GET /v1/sessions by default and excluded only with
// `?includeDeriveFailures=false`. F-15.1.14.
func TestListIncludeDeriveFailures_spec_15_1_652(t *testing.T) {
	store := memstore.New()
	ctx := context.Background()
	_ = store.Create(ctx, sessionstore.Session{ID: "live", TenantID: "acme", State: session.StateRunning, CreatedAt: time.Unix(2, 0)})
	_ = store.Create(ctx, sessionstore.Session{ID: "df", TenantID: "acme", State: session.StateFailed, FailureClass: session.FailureClassDeriveFailure, CreatedAt: time.Unix(1, 0)})
	srv := sessionserver.New(store, sessionserver.Options{})
	h := srv.Handler()

	// Default includes the derive_failure row.
	if got := listSessions(t, h, ""); len(got) != 2 {
		t.Fatalf("default list: want 2 rows (incl derive_failure), got %v", listIDs(got))
	}
	// ?includeDeriveFailures=false drops it.
	got := listSessions(t, h, "includeDeriveFailures=false")
	if len(got) != 1 || got[0].ID != "live" {
		t.Errorf("includeDeriveFailures=false: want [live], got %v", listIDs(got))
	}
	// ?failureClass=derive_failure narrows to only the audit row.
	got = listSessions(t, h, "failureClass=derive_failure")
	if len(got) != 1 || got[0].ID != "df" {
		t.Errorf("failureClass=derive_failure: want [df], got %v", listIDs(got))
	}
}

// spec: §14 line 311 — a label key that is empty is rejected at the create
// boundary so the on-row selector set stays well-formed. F-15.1.15.
func TestCreateRejectsEmptyLabelKey_spec_14_311(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Labels:     map[string]string{"": "oops"},
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty-label-key create: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	code, _, _ := decodeError(t, rr)
	if code != "VALIDATION_ERROR" {
		t.Errorf("error code: got %q, want VALIDATION_ERROR", code)
	}
}

// spec: §14 line 311 / §15.1 line 598 — labels submitted at create survive
// the round trip onto the GET /v1/sessions/{id} envelope. F-15.1.15.
func TestCreateLabelsRoundTripOnGet_spec_15_1_598(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	h := srv.Handler()
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		Labels:     map[string]string{"team": "payments"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var created sessionserver.CreateSessionResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	got := sessionRequest(t, h, http.MethodGet, "/v1/sessions/"+created.ID)
	if got.Code != http.StatusOK {
		t.Fatalf("get: status %d, body %s", got.Code, got.Body.String())
	}
	var resp sessionserver.SessionResponse
	_ = json.Unmarshal(got.Body.Bytes(), &resp)
	if resp.Labels["team"] != "payments" {
		t.Errorf("labels echo on GET: got %v, want team=payments", resp.Labels)
	}
}
