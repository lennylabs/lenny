// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/transcriptstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// spec: §10.2 RBAC permission matrix (session operation categories);
//       §15.1 session-endpoint auth requirements.
//
// The §10.2 matrix splits the session surface into "Create / cancel own
// sessions" (manage_own_sessions) and "Read own session history"
// (read_own_sessions):
//
//   role            manage_own_sessions   read_own_sessions
//   platform-admin   yes                   yes
//   tenant-admin     yes                   yes
//   tenant-viewer    no                    yes
//   billing-viewer   no                    no
//   user             yes                   yes
//
// These tests assert the gateway enforces that matrix on the session
// endpoints, honors a custom role that grants the permission, and
// rejects cross-tenant access.

// rbacSession is a §10.2 session-endpoint authorization scenario.
type rbacSession struct {
	name       string
	roles      []pkgauth.Role
	customRole string // custom-role name, when the principal holds one
	wantManage bool   // matrix grants manage_own_sessions
	wantRead   bool   // matrix grants read_own_sessions
}

// newRBACSessionServer builds a session server wired with an echo
// executor, a transcript store, and the supplied custom-role registry
// so the §10.2 session gate can be exercised end to end. A session row
// owned by acme is seeded so the read / cancel / message endpoints have
// a target.
func newRBACSessionServer(t *testing.T, roles customrolestore.Store) (*sessionserver.Server, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_rbac", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		CustomRoles: roles,
		Clock:       func() time.Time { return now },
		IDFunc:      func() string { return "sess_new" },
	})
	return srv, store
}

// rbacRequest issues a request to the session server as the principal
// described by sc. Each request is stamped with the acme tenant so the
// resolved tenant matches the seeded session.
func rbacRequest(t *testing.T, h http.Handler, sc rbacSession, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	roles := sc.roles
	if sc.customRole != "" {
		roles = []pkgauth.Role{pkgauth.Role(sc.customRole)}
	}
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "caller@acme.com",
		TenantID: "acme",
		Roles:    roles,
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestSessionEndpointAuthorizationMatrix drives every built-in §10.2
// role against the session create / send-message / get / list / cancel
// endpoints and asserts the allowed-vs-403 outcome the matrix dictates.
func TestSessionEndpointAuthorizationMatrix(t *testing.T) {
	scenarios := []rbacSession{
		{name: "platform-admin", roles: []pkgauth.Role{pkgauth.RolePlatformAdmin}, wantManage: true, wantRead: true},
		{name: "tenant-admin", roles: []pkgauth.Role{pkgauth.RoleTenantAdmin}, wantManage: true, wantRead: true},
		{name: "tenant-viewer", roles: []pkgauth.Role{pkgauth.RoleTenantViewer}, wantManage: false, wantRead: true},
		{name: "billing-viewer", roles: []pkgauth.Role{pkgauth.RoleBillingViewer}, wantManage: false, wantRead: false},
		{name: "user", roles: []pkgauth.Role{pkgauth.RoleUser}, wantManage: true, wantRead: true},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			// manage_own_sessions endpoints.
			for _, c := range []struct {
				op           string
				method, path string
				body         any
			}{
				{"create", http.MethodPost, "/v1/sessions", sessionserver.CreateSessionRequest{RuntimeRef: "echo"}},
				{"send-message", http.MethodPost, "/v1/sessions/sess_rbac/messages", sessionserver.MessageRequest{
					Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hi")}},
				}},
				{"cancel", http.MethodDelete, "/v1/sessions/sess_rbac", nil},
			} {
				srv, _ := newRBACSessionServer(t, nil)
				rr := rbacRequest(t, srv.Handler(), sc, c.method, c.path, c.body)
				assertSessionAuth(t, sc.name, c.op, sc.wantManage, rr)
			}
			// read_own_sessions endpoints.
			for _, c := range []struct {
				op           string
				method, path string
			}{
				{"get", http.MethodGet, "/v1/sessions/sess_rbac"},
				{"list", http.MethodGet, "/v1/sessions"},
			} {
				srv, _ := newRBACSessionServer(t, nil)
				rr := rbacRequest(t, srv.Handler(), sc, c.method, c.path, nil)
				assertSessionAuth(t, sc.name, c.op, sc.wantRead, rr)
			}
		})
	}
}

// assertSessionAuth checks that a forbidden outcome matches the matrix.
// When the matrix grants the operation the gate must let the handler
// run, so the status is never 403. When the matrix denies it the gate
// must reject with exactly 403 FORBIDDEN before the handler runs.
func assertSessionAuth(t *testing.T, role, op string, allowed bool, rr *httptest.ResponseRecorder) {
	t.Helper()
	if allowed {
		if rr.Code == http.StatusForbidden {
			t.Errorf("%s %s: got 403, want the matrix to permit the call; body=%s", role, op, rr.Body.String())
		}
		return
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("%s %s: got %d, want 403 (matrix denies the operation)", role, op, rr.Code)
		return
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != "FORBIDDEN" {
		t.Errorf("%s %s: error code = %q, want FORBIDDEN", role, op, env.Error.Code)
	}
}

// TestSessionEndpointCustomRoleGrant verifies the §10.2 custom-role
// path. A tenant custom role that grants manage_own_sessions admits the
// session-mutating endpoints; a custom role that grants only
// read_own_sessions admits the read endpoints but is rejected on the
// mutating ones; a custom role granting an unrelated permission is
// rejected everywhere.
func TestSessionEndpointCustomRoleGrant(t *testing.T) {
	roles := customrolestore.NewMemory()
	ctx := context.Background()
	mustRole := func(r customrolestore.CustomRole) {
		if err := roles.Create(ctx, r); err != nil {
			t.Fatalf("seed custom role %s: %v", r.Name, err)
		}
	}
	// session-manager: full session permission set.
	mustRole(customrolestore.CustomRole{
		TenantID: "acme", Name: "session-manager",
		Permissions: []pkgauth.Permission{
			pkgauth.PermManageOwnSessions, pkgauth.PermReadOwnSessions,
		},
	})
	// session-reader: read-only.
	mustRole(customrolestore.CustomRole{
		TenantID: "acme", Name: "session-reader",
		Permissions: []pkgauth.Permission{pkgauth.PermReadOwnSessions},
	})
	// usage-only: holds neither session permission.
	mustRole(customrolestore.CustomRole{
		TenantID: "acme", Name: "usage-only",
		Permissions: []pkgauth.Permission{pkgauth.PermViewUsage},
	})

	cases := []struct {
		customRole string
		wantManage bool
		wantRead   bool
	}{
		{"session-manager", true, true},
		{"session-reader", false, true},
		{"usage-only", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.customRole, func(t *testing.T) {
			sc := rbacSession{name: tc.customRole, customRole: tc.customRole}
			// manage endpoints.
			for _, c := range []struct {
				op           string
				method, path string
				body         any
			}{
				{"create", http.MethodPost, "/v1/sessions", sessionserver.CreateSessionRequest{RuntimeRef: "echo"}},
				{"send-message", http.MethodPost, "/v1/sessions/sess_rbac/messages", sessionserver.MessageRequest{
					Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hi")}},
				}},
				{"cancel", http.MethodDelete, "/v1/sessions/sess_rbac", nil},
			} {
				srv, _ := newRBACSessionServer(t, roles)
				rr := rbacRequest(t, srv.Handler(), sc, c.method, c.path, c.body)
				assertSessionAuth(t, tc.customRole, c.op, tc.wantManage, rr)
			}
			// read endpoints.
			for _, c := range []struct {
				op           string
				method, path string
			}{
				{"get", http.MethodGet, "/v1/sessions/sess_rbac"},
				{"list", http.MethodGet, "/v1/sessions"},
			} {
				srv, _ := newRBACSessionServer(t, roles)
				rr := rbacRequest(t, srv.Handler(), sc, c.method, c.path, nil)
				assertSessionAuth(t, tc.customRole, c.op, tc.wantRead, rr)
			}
		})
	}
}

// TestSessionEndpointCustomRoleFailsClosedWithoutRegistry confirms a
// caller presenting an unknown custom-role name is denied when no
// custom-role registry is wired — the gate fails closed rather than
// admitting an unresolvable role.
func TestSessionEndpointCustomRoleFailsClosedWithoutRegistry(t *testing.T) {
	srv, _ := newRBACSessionServer(t, nil) // no custom-role registry
	sc := rbacSession{name: "unknown-custom", customRole: "mystery-role"}
	rr := rbacRequest(t, srv.Handler(), sc, http.MethodPost, "/v1/sessions",
		sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	if rr.Code != http.StatusForbidden {
		t.Errorf("unknown custom role with no registry: got %d, want 403", rr.Code)
	}
}

// TestSessionEndpointCrossTenantRejection verifies a caller cannot read
// or mutate a session owned by a different tenant. The caller holds the
// `user` role (which grants both session permissions), so the rejection
// is purely the tenant boundary: the gate admits the call, and the
// handler resolves the caller's own tenant and reports the foreign
// session as absent (404). The caller never reaches the foreign row.
func TestSessionEndpointCrossTenantRejection(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A session owned by globex.
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_globex", TenantID: "globex", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Clock:       func() time.Time { return now },
	})

	// Caller authenticates as a `user` of acme — a different tenant.
	asAcmeUser := func(req *http.Request) *http.Request {
		return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			Subject:  "alice@acme.com",
			TenantID: "acme",
			Roles:    []pkgauth.Role{pkgauth.RoleUser},
		}))
	}
	for _, c := range []struct {
		op           string
		method, path string
		body         any
	}{
		{"get", http.MethodGet, "/v1/sessions/sess_globex", nil},
		{"cancel", http.MethodDelete, "/v1/sessions/sess_globex", nil},
		{"send-message", http.MethodPost, "/v1/sessions/sess_globex/messages", sessionserver.MessageRequest{
			Messages: []sessionserver.MessagePayload{{Role: "user", Content: sessionrecord.MessageContentFromText("hi")}},
		}},
	} {
		var rdr *bytes.Reader
		if c.body != nil {
			b, _ := json.Marshal(c.body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := asAcmeUser(httptest.NewRequest(c.method, c.path, rdr))
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("acme user %s globex session: got %d, want 404 (cross-tenant isolation)", c.op, rr.Code)
		}
	}
}

// TestSessionEndpointAdmitsRolelessPrincipal documents the minimal
// gateway's no-OIDC dev posture: a request that carries no role claim
// (the X-Lenny-Tenant-ID dev-header path, a pre-RBAC service token) is
// admitted in single-tenant mode. The §10.2 gate governs callers whose
// token actually carries roles; a caller that does authenticate with a
// role is always held to the matrix (asserted by
// TestSessionEndpointAuthorizationMatrix).
func TestSessionEndpointAdmitsRolelessPrincipal(t *testing.T) {
	srv, _ := newRBACSessionServer(t, nil)
	// No principal at all — only the dev tenant header.
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions",
		bytes.NewReader(mustJSON(sessionserver.CreateSessionRequest{RuntimeRef: "echo"})))
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Errorf("roleless dev-header request: got 403, want admit (no-OIDC dev posture)")
	}

	// A principal present but carrying no roles is likewise admitted in
	// single-tenant mode.
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess_rbac", nil)
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject: "svc@acme.com", TenantID: "acme",
	}))
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Errorf("roleless principal: got 403, want admit (no-OIDC dev posture)")
	}
}

// TestSessionEndpointMultiTenantRejectsRolelessPrincipal asserts the
// F-10.2.4 fail-closed: with the server in multi-tenant mode, a Bearer
// JWT that authenticated successfully but carries no `roles` claim — and
// a dev-header path without AllowDevRoles — both fail the §10.2 RBAC
// gate. The matrix is unconditional in multi-tenant deployments, so a
// no-role principal cannot exercise any session permission.
// spec: §10.2 lines 256–264.
func TestSessionEndpointMultiTenantRejectsRolelessPrincipal(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_rbac", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{
		Executor:    executor.NewEchoExecutor(),
		Transcripts: transcriptstore.NewMemory(),
		Clock:       func() time.Time { return now },
		IDFunc:      func() string { return "sess_new" },
		MultiTenant: true,
	})
	cases := []struct {
		op           string
		method, path string
		body         any
	}{
		{"create", http.MethodPost, "/v1/sessions", sessionserver.CreateSessionRequest{RuntimeRef: "echo"}},
		{"get", http.MethodGet, "/v1/sessions/sess_rbac", nil},
		{"list", http.MethodGet, "/v1/sessions", nil},
		{"cancel", http.MethodDelete, "/v1/sessions/sess_rbac", nil},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			var rdr *bytes.Reader
			if c.body != nil {
				b, _ := json.Marshal(c.body)
				rdr = bytes.NewReader(b)
			} else {
				rdr = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(c.method, c.path, rdr)
			req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
				Subject: "svc@acme.com", TenantID: "acme",
			}))
			rr := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("multi-tenant + roleless %s: got %d, want 403 FORBIDDEN", c.op, rr.Code)
				return
			}
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &env)
			if env.Error.Code != "FORBIDDEN" {
				t.Errorf("multi-tenant + roleless %s: code=%q, want FORBIDDEN", c.op, env.Error.Code)
			}
		})
	}

	// No-principal case still admits even in multi-tenant mode: the
	// auth middleware's RequireAuth gate is the layer that rejects
	// missing credentials; the session gate fall-through here covers
	// callers that the middleware admitted (healthchecks, etc.).
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusForbidden {
		t.Errorf("multi-tenant + no principal: got 403, want admit (middleware-level rejection)")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
