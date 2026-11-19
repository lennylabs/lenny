// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// okRoute is a part's handler that answers 204, so a test reads from the
// status whether the access check let the request through.
var okRoute = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

// clientRoute is a client-listener route with the given access check.
func clientRoute(method, pattern string, access seam.RouteAccess) seam.Route {
	return seam.Route{Listener: seam.ListenerClient, Method: method, Pattern: pattern, Access: access, Handler: okRoute}
}

// TestSessionHandlersMountEachListenersPartRoutes checks that the request
// pipeline's client handler mounts the routes parts registered on the client
// listener, that its forward handler mounts only those registered on the
// forward listener while serving the session endpoints both serve, that a
// route outside the session paths that leaves authentication to its handler
// is left to the listener's mux, and that a part route registered after a
// handler is built is refused rather than left unmounted.
//
// spec: GWSEAM-HandleRoute
func TestSessionHandlersMountEachListenersPartRoutes(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true})
	table := apifront.Routes(srv)
	forwarded := clientRoute(http.MethodPost, "/v1/parts/{id}/forwarded", seam.AccessTenant)
	for _, r := range []seam.Route{
		clientRoute(http.MethodPost, "/v1/parts/{id}", seam.AccessTenant),
		forwarded,
		{Listener: seam.ListenerForward, Method: forwarded.Method, Pattern: forwarded.Pattern, Access: forwarded.Access, Handler: okRoute},
		clientRoute(http.MethodGet, "/v1/self", seam.AccessHandler),
	} {
		if err := table.HandleRoute(r); err != nil {
			t.Fatalf("HandleRoute %s %s: %v", r.Method, r.Pattern, err)
		}
	}
	client, forward := srv.Handler(), srv.ForwardHandler()
	serve := func(h http.Handler, method, path string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	cases := []struct {
		name         string
		handler      http.Handler
		method, path string
		want         int
	}{
		{"client serves its part route", client, http.MethodPost, "/v1/parts/p1", http.StatusNoContent},
		{"client serves the forwarded route", client, http.MethodPost, "/v1/parts/p1/forwarded", http.StatusNoContent},
		{"client leaves a handler route to the API front", client, http.MethodGet, "/v1/self", http.StatusNotFound},
		{"forward serves the route registered on it", forward, http.MethodPost, "/v1/parts/p1/forwarded", http.StatusNoContent},
		{"forward does not serve a client-only route", forward, http.MethodPost, "/v1/parts/p1", http.StatusNotFound},
		// The session read answers its principal check: the session
		// handler served it.
		{"forward serves the session endpoints", forward, http.MethodGet, "/v1/sessions/missing", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serve(tc.handler, tc.method, tc.path); got != tc.want {
				t.Errorf("%s %s = %d, want %d", tc.method, tc.path, got, tc.want)
			}
		})
	}
	if got := serve(forward, http.MethodPut, "/v1/parts/p1/forwarded"); got != http.StatusMethodNotAllowed {
		t.Errorf("PUT on the forwarded route = %d, want 405 from the pipeline's mux", got)
	}
	if err := table.HandleRoute(clientRoute(http.MethodGet, "/v1/late", seam.AccessTenant)); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Fatalf("a part route after Handler = %v, want ErrInvalidRoute", err)
	}
}

// TestRegisterRoutesNamesEachSessionRoutesCheck checks that the session
// core registers each session route with the access check of its §10.2
// permission-matrix row, the routes under /v1/sessions also on the forward
// listener, and the internal runtime metadata route on the internal
// listener with no check by the API front, and that the API front's
// request pipeline, mounted on the session paths, then serves the session
// endpoints and the part routes behind their checks.
//
// spec: GWSEAM-RegisterRoutes
func TestRegisterRoutesNamesEachSessionRoutesCheck(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true})
	table := apifront.Routes(srv)
	if err := table.HandleRoute(clientRoute(http.MethodPost, "/v1/sessions/{id}/part", seam.AccessTenant)); err != nil {
		t.Fatal(err)
	}
	if err := apifront.Pipeline(srv).Mount(); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	type key struct {
		l       seam.Listener
		pattern string
	}
	got := map[key]seam.RouteAccess{}
	for _, r := range table.Routes() {
		got[key{r.Listener, r.MuxPattern()}] = r.Access
	}
	for k, want := range map[key]seam.RouteAccess{
		{seam.ListenerClient, "POST /v1/sessions"}:                          seam.AccessSessionCreate,
		{seam.ListenerClient, "POST /v1/sessions/start"}:                    seam.AccessSessionCreate,
		{seam.ListenerClient, "POST /v1/environments/{name}/sessions"}:      seam.AccessSessionCreate,
		{seam.ListenerClient, "GET /v1/sessions"}:                           seam.AccessSessionList,
		{seam.ListenerClient, "GET /v1/sessions/{id}"}:                      seam.AccessSessionRead,
		{seam.ListenerClient, "GET /v1/sessions/{id}/transcript"}:           seam.AccessSessionRead,
		{seam.ListenerClient, "DELETE /v1/sessions/{id}"}:                   seam.AccessSessionManage,
		{seam.ListenerClient, "POST /v1/sessions/{id}/messages"}:            seam.AccessSessionManage,
		{seam.ListenerClient, "POST /v1/sessions/{id}/eval"}:                seam.AccessSessionEval,
		{seam.ListenerClient, "GET /v1/sessions/{id}/usage"}:                seam.AccessTenant,
		{seam.ListenerClient, "GET /v1/runtimes"}:                           seam.AccessTenant,
		{seam.ListenerClient, "GET /v1/runtimes/{name}/meta/{key}"}:         seam.AccessHandler,
		{seam.ListenerInternal, "GET /internal/runtimes/{name}/meta/{key}"}: seam.AccessHandler,
		{seam.ListenerForward, "GET /v1/sessions/{id}"}:                     seam.AccessSessionRead,
		{seam.ListenerForward, "POST /v1/sessions"}:                         seam.AccessSessionCreate,
	} {
		if a, ok := got[k]; !ok || a != want {
			t.Errorf("listener %d %s: access %d (registered %v), want %d", k.l, k.pattern, a, ok, want)
		}
	}
	for k := range got {
		if k.l == seam.ListenerForward && !strings.Contains(k.pattern, " /v1/sessions") {
			t.Errorf("forward listener carries %s, which lies outside /v1/sessions", k.pattern)
		}
	}
	shared := table.Handler(seam.ListenerClient)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/v1/sessions/s1/part", http.StatusNoContent},
		{http.MethodGet, "/v1/sessions/missing", http.StatusUnauthorized},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("X-Tenant-ID", "acme")
		rr := httptest.NewRecorder()
		shared.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Errorf("%s %s = %d %q, want %d from the request pipeline", tc.method, tc.path, rr.Code, rr.Body.String(), tc.want)
		}
	}
}

// TestPartRoutesRunBehindTheAccessCheckTheyName checks that each access
// check a part's route names wraps the route in the session routes' check:
// the tenant check refuses a request whose tenant does not resolve, read
// and manage access apply the ownership rule, and the evaluation and usage
// checks apply the permission gate of their permission.
//
// spec: GWSEAM-AuthorizeSession; GWSEAM-AuthorizePermission; §10.2 (Session ownership) rules 5, 6, and 7.
func TestPartRoutesRunBehindTheAccessCheckTheyName(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess-part", TenantID: "acme", UserID: "bob@acme.com", State: session.StateCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	srv := sessionserver.New(store, sessionserver.Options{MultiTenant: true})
	table := apifront.Routes(srv)
	for _, r := range []seam.Route{
		clientRoute(http.MethodGet, "/v1/part/tenant", seam.AccessTenant),
		clientRoute(http.MethodGet, "/v1/sessions/{id}/part-read", seam.AccessSessionRead),
		clientRoute(http.MethodPost, "/v1/sessions/{id}/part-manage", seam.AccessSessionManage),
		clientRoute(http.MethodPost, "/v1/sessions/{id}/part-eval", seam.AccessSessionEval),
		clientRoute(http.MethodGet, "/v1/part/usage", seam.AccessUsageView),
	} {
		if err := table.HandleRoute(r); err != nil {
			t.Fatalf("HandleRoute %s %s: %v", r.Method, r.Pattern, err)
		}
	}
	h := srv.Handler()

	as := func(subject string, roles ...auth.Role) *authmw.Principal {
		return &authmw.Principal{Subject: subject, TenantID: "acme", Roles: roles}
	}
	cases := []struct {
		name, method, path string
		principal          *authmw.Principal
		want               int
	}{
		{"tenant check refuses an unresolved tenant", http.MethodGet, "/v1/part/tenant", nil, http.StatusUnauthorized},
		{"tenant check admits a resolved tenant", http.MethodGet, "/v1/part/tenant", as("alice@acme.com", auth.RoleUser), http.StatusNoContent},
		{"read admits the owner", http.MethodGet, "/v1/sessions/sess-part/part-read", as("bob@acme.com", auth.RoleUser), http.StatusNoContent},
		{"read hides the session from another user", http.MethodGet, "/v1/sessions/sess-part/part-read", as("alice@acme.com", auth.RoleUser), http.StatusNotFound},
		{"read admits a tenant reader", http.MethodGet, "/v1/sessions/sess-part/part-read", as("carol@acme.com", auth.RoleTenantViewer), http.StatusNoContent},
		{"manage admits the owner", http.MethodPost, "/v1/sessions/sess-part/part-manage", as("bob@acme.com", auth.RoleUser), http.StatusNoContent},
		{"manage refuses a role without the manage permission", http.MethodPost, "/v1/sessions/sess-part/part-manage", as("carol@acme.com", auth.RoleTenantViewer), http.StatusForbidden},
		{"manage hides the session from another user", http.MethodPost, "/v1/sessions/sess-part/part-manage", as("alice@acme.com", auth.RoleUser), http.StatusNotFound},
		{"eval refuses a role without the eval permission", http.MethodPost, "/v1/sessions/sess-part/part-eval", as("bob@acme.com", auth.RoleBillingViewer), http.StatusForbidden},
		{"eval admits a role with the eval permission", http.MethodPost, "/v1/sessions/sess-part/part-eval", as("bob@acme.com", auth.RoleUser), http.StatusNoContent},
		{"usage refuses a role without view_usage", http.MethodGet, "/v1/part/usage", as("bob@acme.com", auth.RoleUser), http.StatusForbidden},
		{"usage admits a role with view_usage", http.MethodGet, "/v1/part/usage", as("bob@acme.com", auth.RoleBillingViewer), http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.principal != nil {
				req = req.WithContext(authmw.WithPrincipal(req.Context(), *tc.principal))
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}
