// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// readOutcome is what the access-read fake answers for one read.
type readOutcome int

const (
	readRow readOutcome = iota
	readNotFound
	readError
	readBlocks
)

func (o readOutcome) String() string {
	return [...]string{"row", "not found", "error", "blocks"}[o]
}

var errReadPool = errors.New("read pool unavailable")

// accessReadStore serves the ownership check's replica Get and the primary
// re-read with fixed outcomes. Both honor an ended context first, so a
// read issued under an expired bound reports the bound's error, and both
// count their calls.
type accessReadStore struct {
	*memstore.Store
	row             sessionstore.Session
	get, primary    readOutcome
	gets, primaries atomic.Int64
}

func (a *accessReadStore) answer(ctx context.Context, o readOutcome) (sessionstore.Session, error) {
	if err := ctx.Err(); err != nil {
		return sessionstore.Session{}, err
	}
	switch o {
	case readRow:
		return a.row, nil
	case readNotFound:
		return sessionstore.Session{}, sessionstore.ErrNotFound
	case readError:
		return sessionstore.Session{}, errReadPool
	default:
		<-ctx.Done()
		return sessionstore.Session{}, ctx.Err()
	}
}

func (a *accessReadStore) Get(ctx context.Context, _, _ string) (sessionstore.Session, error) {
	a.gets.Add(1)
	return a.answer(ctx, a.get)
}

func (a *accessReadStore) GetPrimary(ctx context.Context, _, _ string) (sessionstore.Session, error) {
	a.primaries.Add(1)
	return a.answer(ctx, a.primary)
}

// accessTableResponse is the response the wrapper writes for one decision
// table rule: call next (200 from the spy), 404, or 500.
func accessTableResponse(getO, primaryO readOutcome, owner bool) (status int, primaryIssued bool) {
	if getO == readRow {
		if owner {
			return http.StatusOK, false
		}
		return http.StatusNotFound, false
	}
	switch primaryO {
	case readRow:
		if owner {
			return http.StatusOK, true
		}
		return http.StatusNotFound, true
	case readNotFound:
		return http.StatusNotFound, true
	default:
		return http.StatusInternalServerError, true
	}
}

// TestSessionAccessWrapperReadTable_spec_10_2 drives the ownership check's
// read rule through every combination of replica result, primary result,
// caller, and action route. A replica row decides alone; any replica
// error, including expiry of the primary-read bound on a hung read,
// re-reads the row once from the primary; a primary miss answers 404 and a
// primary error 500; and a refusal never reaches the handler. The
// discriminating cases are a blocking replica read, which must return
// within the bound rather than hold the request, and a blocking replica
// read followed by the owner's primary row, which must reach the handler
// rather than answer 500, so the bound scopes the replica read only.
//
// spec: §10.2 (Session ownership) rules 1, 5, and 7; §12.3 (Postgres HA
// Requirements).
func TestSessionAccessWrapperReadTable_spec_10_2(t *testing.T) {
	alice := authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}
	bob := authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}
	outcomes := []readOutcome{readRow, readNotFound, readError, readBlocks}
	patterns := []string{"POST /v1/sessions/{id}/start", "POST /v1/sessions/{id}/resume"}
	for _, pattern := range patterns {
		for _, getO := range outcomes {
			for _, primaryO := range []readOutcome{readRow, readNotFound, readError} {
				for _, caller := range []authmw.Principal{alice, bob} {
					name := pattern + "/get " + getO.String() + "/primary " + primaryO.String() + "/" + caller.Subject
					t.Run(name, func(t *testing.T) {
						owner := caller.Subject == alice.Subject
						wantStatus, wantPrimary := accessTableResponse(getO, primaryO, owner)
						runAccessCase(t, 100*time.Millisecond, pattern, getO, primaryO, caller, wantStatus, wantPrimary)
					})
				}
			}
		}
	}
	// A zero PrimaryReadTimeout bounds the replica read by the default
	// primary-read bound rather than expiring it at once.
	t.Run("zero timeout uses the default bound", func(t *testing.T) {
		runAccessCase(t, 0, patterns[0], readRow, readError, alice, http.StatusOK, false)
	})
}

func runAccessCase(t *testing.T, timeout time.Duration, pattern string, getO, primaryO readOutcome, caller authmw.Principal, wantStatus int, wantPrimary bool) {
	t.Helper()
	st := &accessReadStore{
		Store:   memstore.New(),
		row:     sessionstore.Session{ID: "sess_alice", TenantID: "acme", UserID: "alice@acme.com", State: session.StateSuspended},
		get:     getO,
		primary: primaryO,
	}
	s := New(st, Options{DevTenantHeader: true, PrimaryReadTimeout: timeout})
	var spy atomic.Int64
	next := func(w http.ResponseWriter, _ *http.Request) {
		spy.Add(1)
		w.WriteHeader(http.StatusOK)
	}
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, s.requireSessionPermission(pkgauth.PermManageOwnSessions, s.requireSessionAccess(true, next)))
	path := "/v1/sessions/sess_alice/start"
	if pattern == "POST /v1/sessions/{id}/resume" {
		path = "/v1/sessions/sess_alice/resume"
	}
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), caller))
	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(rr, req)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the ownership check held the request past the primary-read bound")
	}
	if rr.Code != wantStatus {
		t.Fatalf("status = %d %s, want %d", rr.Code, rr.Body.String(), wantStatus)
	}
	wantSpy := int64(0)
	if wantStatus == http.StatusOK {
		wantSpy = 1
	}
	if got := spy.Load(); got != wantSpy {
		t.Errorf("handler calls = %d, want %d", got, wantSpy)
	}
	wantPrimaries := int64(0)
	if wantPrimary {
		wantPrimaries = 1
	}
	if got := st.primaries.Load(); got != wantPrimaries {
		t.Errorf("primary reads = %d, want %d", got, wantPrimaries)
	}
	assertAccessBody(t, rr, wantStatus)
}

// assertAccessBody checks a refusal's envelope: the missing-session body
// for 404 and INTERNAL_ERROR for 500.
func assertAccessBody(t *testing.T, rr *httptest.ResponseRecorder, status int) {
	t.Helper()
	if status == http.StatusOK {
		return
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body %s", err, rr.Body.String())
	}
	switch status {
	case http.StatusNotFound:
		if env.Error.Code != "RESOURCE_NOT_FOUND" || env.Error.Message != "session not found" {
			t.Errorf("404 envelope = %+v, want RESOURCE_NOT_FOUND \"session not found\"", env.Error)
		}
	case http.StatusInternalServerError:
		if env.Error.Code != "INTERNAL_ERROR" {
			t.Errorf("500 envelope = %+v, want INTERNAL_ERROR", env.Error)
		}
	}
}

// TestSessionAccessibleDecision_spec_10_2 pins the predicate the route
// wrapper, the MCP tools, and the attach authorizer share. The read
// bypass resolves only an assigned role, so a role-less principal in a
// single-tenant deployment, which the permission gate admits to every
// session permission, is not granted a read of another user's session.
//
// spec: §10.2 (Session ownership) rules 4 and 5.
func TestSessionAccessibleDecision_spec_10_2(t *testing.T) {
	s := New(memstore.New(), Options{})
	row := sessionstore.Session{ID: "s", TenantID: "acme", UserID: "alice@acme.com"}
	cases := []struct {
		name      string
		p         *authmw.Principal
		read, act bool
	}{
		{"owner", &authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"}, true, true},
		{"role-less non-owner", &authmw.Principal{Subject: "bob@acme.com", TenantID: "acme"}, false, false},
		{"user non-owner", &authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleUser}}, false, false},
		{"tenant-viewer non-owner", &authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RoleTenantViewer}}, true, false},
		{"platform-admin non-owner", &authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin}}, true, false},
		{"unknown custom role, no registry", &authmw.Principal{Subject: "bob@acme.com", TenantID: "acme", Roles: []pkgauth.Role{"session-reader"}}, false, false},
		{"empty subject", &authmw.Principal{TenantID: "acme", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin}}, false, false},
		{"no principal", nil, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.p != nil {
				ctx = authmw.WithPrincipal(ctx, *tc.p)
			}
			if got := s.SessionAccessible(ctx, row, false); got != tc.read {
				t.Errorf("read = %v, want %v", got, tc.read)
			}
			if got := s.SessionAccessible(ctx, row, true); got != tc.act {
				t.Errorf("act = %v, want %v", got, tc.act)
			}
		})
	}
	// A row with no owner is granted to nobody but a reader holding the
	// bypass, and an empty subject never matches it.
	empty := sessionstore.Session{ID: "e", TenantID: "acme"}
	if s.SessionAccessible(authmw.WithPrincipal(context.Background(), authmw.Principal{TenantID: "acme"}), empty, true) {
		t.Error("an empty subject was granted an action on an owner-less row")
	}
}
