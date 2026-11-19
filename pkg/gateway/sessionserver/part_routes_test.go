// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// okRoute is a part's handler that answers 204, so a test reads from the
// status whether the access check let the request through.
var okRoute = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

// clientRoute is a client-listener route with the given access check.
func clientRoute(method, pattern string, access seam.RouteAccess) seam.Route {
	return seam.Route{Listener: seam.ListenerClient, Method: method, Pattern: pattern, Access: access, Handler: okRoute}
}

// TestHandleRouteRefusesARouteTheSessionServerCannotMount checks that the
// session server refuses, with ErrInvalidRoute and before mounting
// anything, a route Route.Validate refuses, a route on another listener, a
// route that leaves authentication to its handler, a route that repeats a
// registered one, and any route once Handler has built the mux.
//
// spec: GWSEAM-HandleRoute
func TestHandleRouteRefusesARouteTheSessionServerCannotMount(t *testing.T) {
	srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true})
	if err := srv.HandleRoute(clientRoute(http.MethodPost, "/v1/parts/{id}", seam.AccessSessionManage)); err != nil {
		t.Fatalf("a valid client route: %v", err)
	}
	cases := []struct {
		name  string
		route seam.Route
	}{
		{"zero listener", seam.Route{Method: http.MethodGet, Pattern: "/v1/x", Access: seam.AccessTenant, Handler: okRoute}},
		{"forward listener", seam.Route{Listener: seam.ListenerForward, Method: http.MethodGet, Pattern: "/v1/x", Access: seam.AccessTenant, Handler: okRoute}},
		{"pod proxy listener", seam.Route{Listener: seam.ListenerPodProxy, Method: http.MethodGet, Pattern: "/v1/x", Access: seam.AccessTenant, Handler: okRoute}},
		{"handler authenticates", clientRoute(http.MethodGet, "/v1/x", seam.AccessHandler)},
		{"repeated method and pattern", clientRoute(http.MethodPost, "/v1/parts/{id}", seam.AccessSessionRead)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := srv.HandleRoute(tc.route); !errors.Is(err, seam.ErrInvalidRoute) {
				t.Fatalf("HandleRoute = %v, want ErrInvalidRoute", err)
			}
		})
	}

	_ = srv.Handler()
	if err := srv.HandleRoute(clientRoute(http.MethodGet, "/v1/late", seam.AccessTenant)); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Fatalf("HandleRoute after Handler = %v, want ErrInvalidRoute", err)
	}
}

// TestPartRoutesRunBehindTheAccessCheckTheyName checks that each access
// check a part's route names wraps the route in the session routes' check:
// the tenant check refuses a request whose tenant does not resolve, read
// and manage access apply the ownership rule, and the evaluation and usage
// checks apply the permission gate of their permission.
//
// spec: GWSEAM-HandleRoute; §10.2 (Session ownership) rules 5, 6, and 7.
func TestPartRoutesRunBehindTheAccessCheckTheyName(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess-part", TenantID: "acme", UserID: "bob@acme.com", State: session.StateCreated, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	srv := sessionserver.New(store, sessionserver.Options{MultiTenant: true})
	for _, r := range []seam.Route{
		clientRoute(http.MethodGet, "/v1/part/tenant", seam.AccessTenant),
		clientRoute(http.MethodGet, "/v1/sessions/{id}/part-read", seam.AccessSessionRead),
		clientRoute(http.MethodPost, "/v1/sessions/{id}/part-manage", seam.AccessSessionManage),
		clientRoute(http.MethodPost, "/v1/sessions/{id}/part-eval", seam.AccessSessionEval),
		clientRoute(http.MethodGet, "/v1/part/usage", seam.AccessUsageView),
	} {
		if err := srv.HandleRoute(r); err != nil {
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
