// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/tenancy/customrolestore"
)

// createEntryPoint is one in-process creation entry point under test.
type createEntryPoint struct {
	name string
	path string // the REST route whose permission gate the entry point mirrors
	call func(*sessionserver.Server, context.Context) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError)
}

// createEntryPoints lists CreateSessionService and CreateAndStartService,
// each calling with the echo runtime in tenant acme.
var createEntryPoints = []createEntryPoint{
	{"CreateSessionService", "/v1/sessions", func(s *sessionserver.Server, ctx context.Context) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError) {
		return s.CreateSessionService(ctx, "acme", sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	}},
	{"CreateAndStartService", "/v1/sessions/start", func(s *sessionserver.Server, ctx context.Context) (sessionserver.CreateSessionResponse, *sessionserver.ServiceError) {
		return s.CreateAndStartService(ctx, "acme", sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
	}},
}

// createPermissionServer builds a session server with the acme custom roles
// "session-creator", which grants manage_own_sessions, and "session-reader",
// which grants read_own_sessions alone. Every create is assigned the
// session ID sess_perm.
func createPermissionServer(t *testing.T, opts sessionserver.Options) (*sessionserver.Server, sessionstore.Store) {
	t.Helper()
	roles := customrolestore.NewMemory()
	for _, cr := range []customrolestore.CustomRole{
		{TenantID: "acme", Name: "session-creator", Permissions: []pkgauth.Permission{pkgauth.PermManageOwnSessions}},
		{TenantID: "acme", Name: "session-reader", Permissions: []pkgauth.Permission{pkgauth.PermReadOwnSessions}},
	} {
		if err := roles.Create(context.Background(), cr); err != nil {
			t.Fatalf("seed custom role %s: %v", cr.Name, err)
		}
	}
	store := memstore.New()
	opts.DevTenantHeader = true
	opts.CustomRoles = roles
	opts.Clock = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	opts.IDFunc = func() string { return "sess_perm" }
	srv := sessionserver.New(store, opts)
	t.Cleanup(srv.Close)
	return srv, store
}

// principalCtx returns a context carrying an acme principal for subject with
// roles.
func principalCtx(subject string, roles ...pkgauth.Role) context.Context {
	return authmw.WithPrincipal(context.Background(), authmw.Principal{
		Subject: subject, TenantID: "acme", Roles: roles,
	})
}

// assertForbidden checks that a create was refused with the 403 FORBIDDEN of
// the create permission and wrote no row.
func assertForbidden(t *testing.T, store sessionstore.Store, svcErr *sessionserver.ServiceError) {
	t.Helper()
	if svcErr == nil {
		t.Fatal("the create was admitted; want 403 FORBIDDEN")
	}
	if svcErr.HTTPStatus != http.StatusForbidden || svcErr.Code != "FORBIDDEN" {
		t.Fatalf("error = %d %s (%s), want 403 FORBIDDEN", svcErr.HTTPStatus, svcErr.Code, svcErr.Message)
	}
	if svcErr.Retryable {
		t.Error("a permission refusal must not be retryable")
	}
	if _, err := store.Get(context.Background(), "acme", "sess_perm"); err == nil {
		t.Error("a refused create left a session row")
	}
}

// assertCreatedFor checks that a create was admitted and that the session it
// wrote is owned by subject.
func assertCreatedFor(t *testing.T, store sessionstore.Store, subject string, resp sessionserver.CreateSessionResponse, svcErr *sessionserver.ServiceError) {
	t.Helper()
	if svcErr != nil {
		t.Fatalf("the create was refused: %d %s (%s)", svcErr.HTTPStatus, svcErr.Code, svcErr.Message)
	}
	row, err := store.Get(context.Background(), "acme", resp.ID)
	if err != nil {
		t.Fatalf("the admitted create wrote no row: %v", err)
	}
	if row.UserID != subject {
		t.Errorf("owner = %q, want %q", row.UserID, subject)
	}
}

// TestInProcessCreateEntryPointsFollowTheCreatePermissionRow pins that
// CreateSessionService and CreateAndStartService, which serve the MCP
// create tools and the single-shot path, admit exactly the roles the
// "Create / cancel own sessions" row grants: platform-admin, tenant-admin,
// user, and a custom role granting manage_own_sessions are admitted, and
// tenant-viewer, billing-viewer, and a custom role granting only
// read_own_sessions are refused with 403 FORBIDDEN and no row.
//
// spec: §10.2 (Authorization and RBAC) Session creation surfaces, §10.2 (Session ownership) rule 7
func TestInProcessCreateEntryPointsFollowTheCreatePermissionRow(t *testing.T) {
	cases := []struct {
		name    string
		roles   []pkgauth.Role
		allowed bool
	}{
		{"platform-admin", []pkgauth.Role{pkgauth.RolePlatformAdmin}, true},
		{"tenant-admin", []pkgauth.Role{pkgauth.RoleTenantAdmin}, true},
		{"user", []pkgauth.Role{pkgauth.RoleUser}, true},
		{"custom-role-granting-create", []pkgauth.Role{"session-creator"}, true},
		{"viewer-plus-user", []pkgauth.Role{pkgauth.RoleTenantViewer, pkgauth.RoleUser}, true},
		{"tenant-viewer", []pkgauth.Role{pkgauth.RoleTenantViewer}, false},
		{"billing-viewer", []pkgauth.Role{pkgauth.RoleBillingViewer}, false},
		{"custom-role-granting-read-only", []pkgauth.Role{"session-reader"}, false},
		{"unknown-custom-role", []pkgauth.Role{"no-such-role"}, false},
	}
	for _, ep := range createEntryPoints {
		for _, tc := range cases {
			t.Run(ep.name+"/"+tc.name, func(t *testing.T) {
				srv, store := createPermissionServer(t, sessionserver.Options{})
				resp, svcErr := ep.call(srv, principalCtx("carol@acme.com", tc.roles...))
				if tc.allowed {
					assertCreatedFor(t, store, "carol@acme.com", resp, svcErr)
					return
				}
				assertForbidden(t, store, svcErr)
			})
		}
	}
}

// TestInProcessCreateRefusalMatchesTheRESTRoute pins that a role the create
// permission refuses receives, from each in-process entry point, the same
// HTTP status, code, and message that its REST route returns through the
// API front's request pipeline.
//
// spec: §10.2 (Authorization and RBAC) Session creation surfaces, §15.2.1 (REST/MCP Consistency Contract)
func TestInProcessCreateRefusalMatchesTheRESTRoute(t *testing.T) {
	for _, ep := range createEntryPoints {
		for _, role := range []pkgauth.Role{pkgauth.RoleTenantViewer, pkgauth.RoleBillingViewer} {
			t.Run(ep.name+"/"+string(role), func(t *testing.T) {
				restSrv, _ := createPermissionServer(t, sessionserver.Options{})
				rr := rbacRequest(t, restSrv.Handler(), rbacSession{roles: []pkgauth.Role{role}}, http.MethodPost, ep.path,
					sessionserver.CreateSessionRequest{RuntimeRef: "echo"})
				var env struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
					t.Fatalf("decode REST envelope: %v; body=%s", err, rr.Body.String())
				}

				srv, _ := createPermissionServer(t, sessionserver.Options{})
				_, svcErr := ep.call(srv, principalCtx("caller@acme.com", role))
				if svcErr == nil {
					t.Fatalf("%s admitted %s; REST answered %d", ep.name, role, rr.Code)
				}
				if svcErr.HTTPStatus != rr.Code || svcErr.Code != env.Error.Code || svcErr.Message != env.Error.Message {
					t.Errorf("in-process = %d %s %q, REST = %d %s %q",
						svcErr.HTTPStatus, svcErr.Code, svcErr.Message, rr.Code, env.Error.Code, env.Error.Message)
				}
			})
		}
	}
}

// TestInProcessCreateRolelessPrincipalFollowsTheTenancyMode pins the
// boundary of the permission gate on the in-process entry points: a
// principal with no roles is admitted in a single-tenant deployment and
// refused with 403 FORBIDDEN in a multi-tenant one, and a call with no
// principal passes the permission gate and is refused with 401 UNAUTHORIZED
// by the owner binding, as on REST.
//
// spec: §10.2 (Session ownership) rules 3 and 7, §10.2 (Authorization and RBAC)
func TestInProcessCreateRolelessPrincipalFollowsTheTenancyMode(t *testing.T) {
	for _, ep := range createEntryPoints {
		t.Run(ep.name+"/single-tenant-roleless", func(t *testing.T) {
			srv, store := createPermissionServer(t, sessionserver.Options{})
			resp, svcErr := ep.call(srv, principalCtx("dave@acme.com"))
			assertCreatedFor(t, store, "dave@acme.com", resp, svcErr)
		})
		t.Run(ep.name+"/multi-tenant-roleless", func(t *testing.T) {
			srv, store := createPermissionServer(t, sessionserver.Options{MultiTenant: true})
			_, svcErr := ep.call(srv, principalCtx("dave@acme.com"))
			assertForbidden(t, store, svcErr)
		})
		t.Run(ep.name+"/no-principal", func(t *testing.T) {
			srv, store := createPermissionServer(t, sessionserver.Options{})
			_, svcErr := ep.call(srv, context.Background())
			if svcErr == nil || svcErr.HTTPStatus != http.StatusUnauthorized || svcErr.Code != "UNAUTHORIZED" {
				t.Fatalf("error = %+v, want 401 UNAUTHORIZED", svcErr)
			}
			if _, err := store.Get(context.Background(), "acme", "sess_perm"); err == nil {
				t.Error("a refused create left a session row")
			}
		})
	}
}

// TestInProcessCreateRefusesAForbiddenRoleBeforeAdmissionGates pins that the
// permission refusal precedes every admission gate on the in-process entry
// points, as it does on REST: while both stores are unavailable, a
// tenant-viewer receives 403 FORBIDDEN, and a user, whom the permission
// admits, receives the 503 PLATFORM_DEGRADED refusal.
//
// spec: §10.2 (Authorization and RBAC) Session creation surfaces, §10.1.3 (Dual-Store Unavailability) item 2
func TestInProcessCreateRefusesAForbiddenRoleBeforeAdmissionGates(t *testing.T) {
	for _, ep := range createEntryPoints {
		t.Run(ep.name, func(t *testing.T) {
			srv, store := createPermissionServer(t, sessionserver.Options{DualStore: stubDualStore{unavailable: true}})
			_, svcErr := ep.call(srv, principalCtx("carol@acme.com", pkgauth.RoleTenantViewer))
			assertForbidden(t, store, svcErr)

			_, svcErr = ep.call(srv, principalCtx("carol@acme.com", pkgauth.RoleUser))
			if svcErr == nil || svcErr.HTTPStatus != http.StatusServiceUnavailable || svcErr.Code != "PLATFORM_DEGRADED" {
				t.Fatalf("user error = %+v, want 503 PLATFORM_DEGRADED", svcErr)
			}
		})
	}
}
