// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

const (
	accessAlice = "alice@acme.com"
	accessBob   = "bob@acme.com"
	accessCarol = "carol@acme.com"
	// sessionReader is a tenant custom role that grants the read bypass
	// alongside the session permissions a user holds.
	sessionReader = "session-reader"
	// sessionScorer is a tenant custom role that grants only the §10.7
	// eval-submission permission.
	sessionScorer = "session-scorer"
)

// accessCaller is one principal of the ownership tests. A nil principal
// sends the request with no principal at all.
type accessCaller struct {
	name      string
	principal *authmw.Principal
}

func principalOf(subject string, roles ...pkgauth.Role) *authmw.Principal {
	return &authmw.Principal{Subject: subject, TenantID: "acme", Roles: roles}
}

// accessServer builds a session server over a store holding the
// seedAccessRows rows, with the session-reader custom role registered for
// acme. multiTenant mirrors the gateway's --multi-tenant flag.
func accessServer(t *testing.T, multiTenant bool) http.Handler {
	t.Helper()
	store := memstore.New()
	seedAccessRows(t, store)
	return sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		MultiTenant:     multiTenant,
		CustomRoles:     accessRoles(t),
	}).Handler()
}

// seedAccessRows writes alice's running session sess_alice and bob's
// sess_bob in tenant acme, plus carol's sess_carol and alice's
// sess_alice_globex in tenant globex.
func seedAccessRows(t *testing.T, store sessionstore.Store) {
	t.Helper()
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []sessionstore.Session{
		{ID: "sess_alice", TenantID: "acme", UserID: accessAlice, State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "sess_bob", TenantID: "acme", UserID: accessBob, State: session.StateRunning, CreatedAt: now.Add(time.Second), UpdatedAt: now},
		{ID: "sess_carol", TenantID: "globex", UserID: accessCarol, State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: "sess_alice_globex", TenantID: "globex", UserID: accessAlice, State: session.StateRunning, CreatedAt: now.Add(time.Second), UpdatedAt: now},
	} {
		if err := store.Create(context.Background(), row); err != nil {
			t.Fatalf("seed %s: %v", row.ID, err)
		}
	}
}

// accessRoles returns a custom-role store holding acme's session-reader
// role and its sessionScorer role, which grants only session:eval:write.
func accessRoles(t *testing.T) customrolestore.Store {
	t.Helper()
	roles := customrolestore.NewMemory()
	for _, role := range []customrolestore.CustomRole{
		{TenantID: "acme", Name: sessionReader, Permissions: []pkgauth.Permission{
			pkgauth.PermReadTenantSessions, pkgauth.PermReadOwnSessions, pkgauth.PermManageOwnSessions,
		}},
		{TenantID: "acme", Name: sessionScorer, Permissions: []pkgauth.Permission{pkgauth.PermSessionEvalWrite}},
	} {
		if err := roles.Create(context.Background(), role); err != nil {
			t.Fatalf("seed custom role %s: %v", role.Name, err)
		}
	}
	return roles
}

func accessRequest(t *testing.T, h http.Handler, c accessCaller, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	if c.principal != nil {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), *c.principal))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestSessionRoutesApplyOwnership_spec_10_2 pins the ownership check on a
// read route and an action route. Before the check, every caller the
// permission matrix admitted read and terminated alice's session. A
// non-owner that may not use the session receives a body byte-identical to
// a missing session's, the read bypass comes only from a role that grants
// "Read other users' sessions (same tenant)", a role-less principal reads
// only its own sessions, and the permission gate still answers first.
//
// spec: §10.2 (Session ownership) rules 3, 4, 5, and 7.
func TestSessionRoutesApplyOwnership_spec_10_2(t *testing.T) {
	missing := accessRequest(t, accessServer(t, false),
		accessCaller{"alice", principalOf(accessAlice, pkgauth.RoleUser)}, http.MethodGet, "/v1/sessions/sess_missing")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing session = %d %s, want 404", missing.Code, missing.Body.String())
	}
	cases := []struct {
		caller           accessCaller
		multiTenant      bool
		readStatus       int
		terminateStatus  int
		readNotFound     bool
		terminateMissing bool
	}{
		{caller: accessCaller{"alice/user", principalOf(accessAlice, pkgauth.RoleUser)}, readStatus: http.StatusOK, terminateStatus: http.StatusOK},
		{caller: accessCaller{"bob/user", principalOf(accessBob, pkgauth.RoleUser)}, readStatus: http.StatusNotFound, readNotFound: true, terminateStatus: http.StatusNotFound, terminateMissing: true},
		{caller: accessCaller{"bob role-less single-tenant", principalOf(accessBob)}, readStatus: http.StatusNotFound, readNotFound: true, terminateStatus: http.StatusNotFound, terminateMissing: true},
		{caller: accessCaller{"bob/tenant-viewer", principalOf(accessBob, pkgauth.RoleTenantViewer)}, readStatus: http.StatusOK, terminateStatus: http.StatusForbidden},
		{caller: accessCaller{"bob/tenant-admin", principalOf(accessBob, pkgauth.RoleTenantAdmin)}, readStatus: http.StatusOK, terminateStatus: http.StatusNotFound, terminateMissing: true},
		{caller: accessCaller{"bob/platform-admin", principalOf(accessBob, pkgauth.RolePlatformAdmin)}, readStatus: http.StatusOK, terminateStatus: http.StatusNotFound, terminateMissing: true},
		{caller: accessCaller{"bob/custom session-reader", principalOf(accessBob, pkgauth.Role(sessionReader))}, readStatus: http.StatusOK, terminateStatus: http.StatusNotFound, terminateMissing: true},
		{caller: accessCaller{"bob role-less multi-tenant", principalOf(accessBob)}, multiTenant: true, readStatus: http.StatusForbidden, terminateStatus: http.StatusForbidden},
		{caller: accessCaller{"no principal", nil}, readStatus: http.StatusUnauthorized, terminateStatus: http.StatusUnauthorized},
		{caller: accessCaller{"empty subject with roles", principalOf("", pkgauth.RoleTenantAdmin)}, readStatus: http.StatusUnauthorized, terminateStatus: http.StatusUnauthorized},
		{caller: accessCaller{"empty subject role-less", principalOf("")}, readStatus: http.StatusUnauthorized, terminateStatus: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.caller.name, func(t *testing.T) {
			h := accessServer(t, tc.multiTenant)
			read := accessRequest(t, h, tc.caller, http.MethodGet, "/v1/sessions/sess_alice")
			if read.Code != tc.readStatus {
				t.Fatalf("GET alice's session = %d %s, want %d", read.Code, read.Body.String(), tc.readStatus)
			}
			if tc.readNotFound && read.Body.String() != missing.Body.String() {
				t.Errorf("GET refusal body %s differs from the missing-session body %s", read.Body.String(), missing.Body.String())
			}
			term := accessRequest(t, h, tc.caller, http.MethodPost, "/v1/sessions/sess_alice/terminate")
			if term.Code != tc.terminateStatus {
				t.Fatalf("terminate alice's session = %d %s, want %d", term.Code, term.Body.String(), tc.terminateStatus)
			}
			if tc.terminateMissing && term.Body.String() != missing.Body.String() {
				t.Errorf("terminate refusal body %s differs from the missing-session body %s", term.Body.String(), missing.Body.String())
			}
			// The row's state shows whether the terminate handler ran.
			after := accessRequest(t, h, accessCaller{"alice", principalOf(accessAlice, pkgauth.RoleUser)}, http.MethodGet, "/v1/sessions/sess_alice")
			var row struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal(after.Body.Bytes(), &row)
			terminated := row.State != string(session.StateRunning)
			if want := tc.terminateStatus == http.StatusOK; terminated != want {
				t.Errorf("alice's session state after the terminate = %q; the handler ran = %v, want %v", row.State, terminated, want)
			}
		})
	}
}

// TestSessionListIsOwnerScoped_spec_10_2 pins the list filter: a caller
// whose roles do not grant "Read other users' sessions (same tenant)",
// including a role-less principal, lists only its own sessions, a caller
// whose role grants it lists the tenant, a platform-admin naming another
// tenant lists that tenant, and a request with no principal subject is
// refused. Before the filter every caller listed the whole tenant.
//
// spec: §10.2 (Session ownership) rules 3, 4, and 6; §15.1 (GET
// /v1/sessions ?tenant=).
func TestSessionListIsOwnerScoped_spec_10_2(t *testing.T) {
	cases := []struct {
		caller accessCaller
		query  string
		status int
		ids    []string
	}{
		{caller: accessCaller{"alice/user", principalOf(accessAlice, pkgauth.RoleUser)}, status: http.StatusOK, ids: []string{"sess_alice"}},
		{caller: accessCaller{"bob role-less single-tenant", principalOf(accessBob)}, status: http.StatusOK, ids: []string{"sess_bob"}},
		{caller: accessCaller{"bob/tenant-viewer", principalOf(accessBob, pkgauth.RoleTenantViewer)}, status: http.StatusOK, ids: []string{"sess_alice", "sess_bob"}},
		{caller: accessCaller{"bob/tenant-admin", principalOf(accessBob, pkgauth.RoleTenantAdmin)}, status: http.StatusOK, ids: []string{"sess_alice", "sess_bob"}},
		{caller: accessCaller{"bob/custom session-reader", principalOf(accessBob, pkgauth.Role(sessionReader))}, status: http.StatusOK, ids: []string{"sess_alice", "sess_bob"}},
		{caller: accessCaller{"bob/platform-admin ?tenant=globex", principalOf(accessBob, pkgauth.RolePlatformAdmin)}, query: "?tenant=globex", status: http.StatusOK, ids: []string{"sess_alice_globex", "sess_carol"}},
		{caller: accessCaller{"bob/platform-admin own tenant", principalOf(accessBob, pkgauth.RolePlatformAdmin)}, status: http.StatusOK, ids: []string{"sess_alice", "sess_bob"}},
		{caller: accessCaller{"bob/user ?tenant=globex", principalOf(accessBob, pkgauth.RoleUser)}, query: "?tenant=globex", status: http.StatusOK, ids: []string{"sess_bob"}},
		{caller: accessCaller{"no principal", nil}, status: http.StatusUnauthorized},
		{caller: accessCaller{"empty subject with roles", principalOf("", pkgauth.RoleTenantAdmin)}, status: http.StatusUnauthorized},
		{caller: accessCaller{"empty subject role-less", principalOf("")}, status: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.caller.name, func(t *testing.T) {
			rr := accessRequest(t, accessServer(t, false), tc.caller, http.MethodGet, "/v1/sessions"+tc.query)
			if rr.Code != tc.status {
				t.Fatalf("list = %d %s, want %d", rr.Code, rr.Body.String(), tc.status)
			}
			if tc.status != http.StatusOK {
				return
			}
			var env struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode list: %v", err)
			}
			got := make([]string, 0, len(env.Items))
			for _, it := range env.Items {
				got = append(got, it.ID)
			}
			sort.Strings(got)
			if len(got) != len(tc.ids) {
				t.Fatalf("listed %v, want %v", got, tc.ids)
			}
			for i := range got {
				if got[i] != tc.ids[i] {
					t.Fatalf("listed %v, want %v", got, tc.ids)
				}
			}
		})
	}
}

// TestElicitationRouteOwnershipPrecedesLookup_spec_10_2 pins the ordering
// rule on the tool-use and elicitation routes: a missing session and a
// session the caller may not act on answer RESOURCE_NOT_FOUND from the
// ownership check, where the route-specific lookup answered its own
// not-found code before.
//
// spec: §10.2 (Session ownership) rule 7; §9.2 (respond_to_elicitation
// authorization).
func TestElicitationRouteOwnershipPrecedesLookup_spec_10_2(t *testing.T) {
	h := accessServer(t, false)
	bob := accessCaller{"bob", principalOf(accessBob, pkgauth.RoleUser)}
	for _, path := range []string{
		"/v1/sessions/sess_alice/elicitations/el_1/respond",
		"/v1/sessions/sess_alice/elicitations/el_1/dismiss",
		"/v1/sessions/sess_alice/tool-use/tc_1/approve",
		"/v1/sessions/sess_alice/tool-use/tc_1/deny",
		"/v1/sessions/sess_missing/elicitations/el_1/respond",
		"/v1/sessions/sess_missing/tool-use/tc_1/approve",
	} {
		rr := accessRequest(t, h, bob, http.MethodPost, path)
		if rr.Code != http.StatusNotFound || errorCode(t, rr) != "RESOURCE_NOT_FOUND" {
			t.Errorf("POST %s = %d %s, want 404 RESOURCE_NOT_FOUND", path, rr.Code, rr.Body.String())
		}
	}
}
