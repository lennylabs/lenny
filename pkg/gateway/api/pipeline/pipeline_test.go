// SPDX-License-Identifier: MIT

package pipeline_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/gateway/api/pipeline"
	"github.com/lennylabs/lenny/pkg/gateway/api/routetable"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// fakeChecks answers the session checks and the permission gate from
// maps, and records the calls it receives.
type fakeChecks struct {
	sessionErr map[string]error
	denied     map[auth.Permission]bool
	calls      []string
}

func (f *fakeChecks) LookupSession(context.Context, string, string) (seam.Session, error) {
	return seam.Session{}, nil
}

func (f *fakeChecks) AuthorizeSession(_ context.Context, tenantID, sessionID string, action seam.SessionAction) (seam.Session, error) {
	f.calls = append(f.calls, fmt.Sprintf("session %s/%s %d", tenantID, sessionID, action))
	return seam.Session{ID: sessionID}, f.sessionErr[sessionID]
}

func (f *fakeChecks) AuthorizePermission(_ context.Context, perm auth.Permission) error {
	f.calls = append(f.calls, "permission "+string(perm))
	if f.denied[perm] {
		return fmt.Errorf("permission %s: %w", perm, seam.ErrForbidden)
	}
	return nil
}

var ok = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

// build registers routes on a new table and returns a pipeline over it.
func build(t *testing.T, checks *fakeChecks, dev bool, routes ...seam.Route) (*routetable.Table, *pipeline.Pipeline) {
	t.Helper()
	table := routetable.New()
	for _, r := range routes {
		if err := table.HandleRoute(r); err != nil {
			t.Fatalf("HandleRoute %s %s: %v", r.Method, r.Pattern, err)
		}
	}
	p, err := pipeline.New(pipeline.Config{Table: table, Sessions: checks, Permissions: checks, DevTenantHeader: dev})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return table, p
}

func route(l seam.Listener, method, pattern string, access seam.RouteAccess) seam.Route {
	return seam.Route{Listener: l, Method: method, Pattern: pattern, Access: access, Handler: ok}
}

// envelope is the §15.1 error envelope a refusal writes.
type envelope struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func serve(h http.Handler, method, path string, p *authmw.Principal) (int, envelope) {
	req := httptest.NewRequest(method, path, nil)
	if p != nil {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), *p))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var env envelope
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	return rr.Code, env
}

// TestNewRefusesMissingChecks checks that the pipeline refuses a missing
// route table and a missing or typed-nil session check or permission gate,
// so a gateway wired without the session core's checks fails at startup.
//
// spec: GWSEAM-AuthorizeSession; GWSEAM-AuthorizePermission
func TestNewRefusesMissingChecks(t *testing.T) {
	checks := &fakeChecks{}
	var typedNil *fakeChecks
	for name, cfg := range map[string]pipeline.Config{
		"no table":              {Sessions: checks, Permissions: checks},
		"no sessions":           {Table: routetable.New(), Permissions: checks},
		"typed nil sessions":    {Table: routetable.New(), Sessions: typedNil, Permissions: checks},
		"no permissions":        {Table: routetable.New(), Sessions: checks},
		"typed nil permissions": {Table: routetable.New(), Sessions: checks, Permissions: typedNil},
	} {
		t.Run(name, func(t *testing.T) {
			if p, err := pipeline.New(cfg); err == nil || p != nil {
				t.Fatalf("New = %v, %v; want an error", p, err)
			}
		})
	}
}

// TestEachAccessCheckRunsItsSeamOperation checks that each access check
// calls the operation and the permission the seam page names before the
// handler runs, and that the handler runs only when the check admits.
//
// spec: GWSEAM-AuthorizeSession; GWSEAM-AuthorizePermission; §10.2
// (Session ownership) rules 5, 6, and 7.
func TestEachAccessCheckRunsItsSeamOperation(t *testing.T) {
	cases := []struct {
		access     seam.RouteAccess
		method     string
		pattern    string
		path       string
		wantCall   string
		wantDenied auth.Permission
	}{
		{seam.AccessTenant, http.MethodGet, "/v1/models", "/v1/models", "", ""},
		{seam.AccessSessionRead, http.MethodGet, "/v1/sessions/{id}", "/v1/sessions/s1", "session acme/s1 1", auth.PermReadOwnSessions},
		{seam.AccessSessionManage, http.MethodPost, "/v1/sessions/{id}/start", "/v1/sessions/s1/start", "session acme/s1 2", auth.PermManageOwnSessions},
		{seam.AccessSessionEval, http.MethodPost, "/v1/sessions/{id}/eval", "/v1/sessions/s1/eval", "permission " + string(auth.PermSessionEvalWrite), auth.PermSessionEvalWrite},
		{seam.AccessUsageView, http.MethodGet, "/v1/sessions/{id}/usage", "/v1/sessions/s1/usage", "permission " + string(auth.PermViewUsage), auth.PermViewUsage},
		{seam.AccessSessionCreate, http.MethodPost, "/v1/sessions", "/v1/sessions", "permission " + string(auth.PermManageOwnSessions), auth.PermManageOwnSessions},
		{seam.AccessSessionList, http.MethodGet, "/v1/sessions", "/v1/sessions", "permission " + string(auth.PermReadOwnSessions), auth.PermReadOwnSessions},
	}
	alice := &authmw.Principal{Subject: "alice@acme.com", TenantID: "acme", Roles: []auth.Role{auth.RoleUser}}
	for _, tc := range cases {
		t.Run(tc.pattern+" "+tc.method, func(t *testing.T) {
			checks := &fakeChecks{}
			_, p := build(t, checks, false, route(seam.ListenerClient, tc.method, tc.pattern, tc.access))
			h := p.Handler(seam.ListenerClient)
			if code, _ := serve(h, tc.method, tc.path, alice); code != http.StatusNoContent {
				t.Fatalf("admitted request = %d, want 204", code)
			}
			if tc.wantCall == "" {
				if len(checks.calls) != 0 {
					t.Errorf("calls = %v, want none for a tenant check", checks.calls)
				}
				return
			}
			if len(checks.calls) != 1 || checks.calls[0] != tc.wantCall {
				t.Errorf("calls = %v, want [%s]", checks.calls, tc.wantCall)
			}
			// The session operation applies its own permission gate, so a
			// refusal it reports names the permission of the action.
			checks.denied = map[auth.Permission]bool{tc.wantDenied: true}
			checks.sessionErr = map[string]error{"s1": seam.ErrForbidden}
			code, env := serve(h, tc.method, tc.path, alice)
			if code != http.StatusForbidden || env.Error.Code != "FORBIDDEN" ||
				env.Error.Message != "this endpoint requires the "+string(tc.wantDenied)+" permission" {
				t.Errorf("refused request = %d %+v, want 403 FORBIDDEN naming %s", code, env.Error, tc.wantDenied)
			}
		})
	}
}

// TestSessionCheckRefusalsAnswerTheirEnvelope checks the envelope of each
// refusal the session check reports: 401 for a request with no principal
// subject, 404 RESOURCE_NOT_FOUND for a session that does not exist or that
// the caller may not see, and 500 INTERNAL_ERROR for any other failure.
//
// spec: GWSEAM-AuthorizeSession; §10.2 (Session ownership) rules 3 and 7.
func TestSessionCheckRefusalsAnswerTheirEnvelope(t *testing.T) {
	checks := &fakeChecks{sessionErr: map[string]error{
		"anon":   seam.ErrUnauthenticated,
		"hidden": fmt.Errorf("session hidden: %w", seam.ErrSessionNotFound),
		"broken": errors.New("store unavailable"),
	}}
	_, p := build(t, checks, true, route(seam.ListenerClient, http.MethodGet, "/v1/sessions/{id}", seam.AccessSessionRead))
	h := p.Handler(seam.ListenerClient)
	for _, tc := range []struct {
		id, code, message string
		status            int
		reason            string
	}{
		{"anon", "UNAUTHORIZED", "request requires an authenticated principal with a subject", http.StatusUnauthorized, "principal_subject_required"},
		{"hidden", "RESOURCE_NOT_FOUND", "session not found", http.StatusNotFound, ""},
		{"broken", "INTERNAL_ERROR", "store unavailable", http.StatusInternalServerError, ""},
	} {
		status, env := serve(h, http.MethodGet, "/v1/sessions/"+tc.id, nil)
		if status != tc.status || env.Error.Code != tc.code || env.Error.Message != tc.message {
			t.Errorf("%s = %d %+v, want %d %s %q", tc.id, status, env.Error, tc.status, tc.code, tc.message)
		}
		if tc.reason != "" && env.Error.Details["reason"] != tc.reason {
			t.Errorf("%s details = %v, want reason %s", tc.id, env.Error.Details, tc.reason)
		}
	}
}

// TestTenantCheckGuardsEveryCheckedRoute checks that a request on a route
// with a check is refused with 401 when no tenant resolves, that the dev
// header resolves the tenant only under DevTenantHeader, that a route that
// leaves authentication to its handler runs with no tenant, and that a
// path the pipeline does not route answers the mux's own 404 or 405 with no
// tenant check.
//
// spec: §10.2 (Authentication); §15.1 (UNAUTHORIZED); §5.1
// (publishedMetadata visibility levels).
func TestTenantCheckGuardsEveryCheckedRoute(t *testing.T) {
	routes := []seam.Route{
		route(seam.ListenerClient, http.MethodGet, "/v1/models", seam.AccessTenant),
		route(seam.ListenerClient, http.MethodGet, "/v1/runtimes/{name}/meta/{key}", seam.AccessHandler),
	}
	_, prod := build(t, &fakeChecks{}, false, routes...)
	h := prod.Handler(seam.ListenerClient)
	code, env := serve(h, http.MethodGet, "/v1/models", nil)
	if code != http.StatusUnauthorized || env.Error.Details["reason"] != "auth_required" {
		t.Errorf("no tenant = %d %+v, want 401 auth_required", code, env.Error)
	}
	if code, _ := serve(h, http.MethodGet, "/v1/runtimes/r/meta/k", nil); code != http.StatusNoContent {
		t.Errorf("a route that leaves authentication to its handler = %d, want 204", code)
	}
	if code, _ := serve(h, http.MethodGet, "/v1/unrouted", nil); code != http.StatusNotFound {
		t.Errorf("an unrouted path = %d, want 404", code)
	}
	if code, _ := serve(h, http.MethodPost, "/v1/models", nil); code != http.StatusMethodNotAllowed {
		t.Errorf("an unrouted method = %d, want 405", code)
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "globex")
	if got := prod.RequestTenant(req); got != "" {
		t.Errorf("RequestTenant outside dev mode = %q, want none", got)
	}
	_, dev := build(t, &fakeChecks{}, true, routes...)
	rr := httptest.NewRecorder()
	dev.Handler(seam.ListenerClient).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent || dev.RequestTenant(req) != "globex" {
		t.Errorf("dev header = %d tenant %q, want 204 globex", rr.Code, dev.RequestTenant(req))
	}
	svc := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	svc = svc.WithContext(authmw.WithServiceTenant(svc.Context(), "initech"))
	if got := prod.RequestTenant(svc); got != "initech" {
		t.Errorf("RequestTenant of a service call = %q, want initech", got)
	}
}

// TestMountServesEachListenersRoutesOnItsSessionPaths checks that Mount
// mounts each listener's handler on its session paths, that the forward
// listener serves only the routes registered on it, that the route
// patterns name each route's check, and that a route the pipeline would
// mount is refused once its listener is mounted.
//
// spec: GWSEAM-HandleRoute
func TestMountServesEachListenersRoutesOnItsSessionPaths(t *testing.T) {
	alice := &authmw.Principal{Subject: "alice@acme.com", TenantID: "acme"}
	table, p := build(
		t, &fakeChecks{}, false,
		route(seam.ListenerClient, http.MethodPost, "/v1/sessions/{id}/upload", seam.AccessSessionManage),
		route(seam.ListenerClient, http.MethodPost, "/v1/sessions/{id}/upload-to-session", seam.AccessSessionManage),
		route(seam.ListenerForward, http.MethodPost, "/v1/sessions/{id}/upload-to-session", seam.AccessSessionManage),
		route(seam.ListenerClient, http.MethodGet, "/v1/blobs/{ref...}", seam.AccessTenant),
	)
	if err := p.Mount(); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	client, forward := table.Handler(seam.ListenerClient), table.Handler(seam.ListenerForward)
	for _, tc := range []struct {
		h            http.Handler
		method, path string
		want         int
	}{
		{client, http.MethodPost, "/v1/sessions/s1/upload", http.StatusNoContent},
		{client, http.MethodGet, "/v1/blobs/b1", http.StatusNoContent},
		{forward, http.MethodPost, "/v1/sessions/s1/upload-to-session", http.StatusNoContent},
		{forward, http.MethodPost, "/v1/sessions/s1/upload", http.StatusNotFound},
		{forward, http.MethodGet, "/v1/blobs/b1", http.StatusNotFound},
	} {
		if code, _ := serve(tc.h, tc.method, tc.path, alice); code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, code, tc.want)
		}
	}
	want := "POST /v1/sessions/{id}/upload [session-manage]|POST /v1/sessions/{id}/upload-to-session [session-manage]|GET /v1/blobs/{ref...} [tenant]"
	if got := strings.Join(p.RoutePatterns(seam.ListenerClient), "|"); got != want {
		t.Errorf("RoutePatterns(client) = %s, want %s", got, want)
	}
	if err := table.HandleRoute(route(seam.ListenerClient, http.MethodGet, "/v1/sessions/{id}/late", seam.AccessSessionRead)); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("a route after Mount = %v, want ErrInvalidRoute", err)
	}
	if err := p.Mount(); !errors.Is(err, seam.ErrInvalidRoute) {
		t.Errorf("a second Mount = %v, want ErrInvalidRoute", err)
	}
}
