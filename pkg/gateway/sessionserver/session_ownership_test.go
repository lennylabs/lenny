// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// mountedRoute is one mux.HandleFunc call in Handler(): its pattern and the
// name of the wrapper its handler is passed through, or "" for a handler
// mounted bare.
type mountedRoute struct {
	pattern string
	wrapper string
}

// method returns the HTTP method of the route's pattern.
func (m mountedRoute) method() string {
	return strings.Fields(m.pattern)[0]
}

// path returns the route's pattern path with {id} naming sess and every
// other wildcard naming a fixed id.
func (m mountedRoute) path(sess string) string {
	p := strings.Fields(m.pattern)[1]
	p = strings.ReplaceAll(p, "{id}", sess)
	for _, w := range []string{"{toolCallId}", "{elicitationId}", "{memoryId}"} {
		p = strings.ReplaceAll(p, w, "x_1")
	}
	return p
}

// sessionIDRoute reports whether the route names one session through the
// /v1/sessions/{id} prefix.
func (m mountedRoute) sessionIDRoute() bool {
	return strings.HasPrefix(strings.Fields(m.pattern)[1], "/v1/sessions/{id}")
}

// ownershipExempt reports whether the route is one of the two
// /v1/sessions/{id} routes the ownership check does not wrap: eval keeps
// its §10.7 caller rule and usage keeps its view_usage gate.
func (m mountedRoute) ownershipExempt() bool {
	return m.pattern == "POST /v1/sessions/{id}/eval" || m.pattern == "GET /v1/sessions/{id}/usage"
}

// handlerRoutes parses route_table.go and returns every route Handler()
// mounts, so a route added to the table is a row of every test that reads
// it without being listed here.
func handlerRoutes(t *testing.T) []mountedRoute {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "route_table.go", nil, 0)
	if err != nil {
		t.Fatalf("parse route_table.go: %v", err)
	}
	var routes []mountedRoute
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "HandleFunc" {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		pattern, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("unquote %s: %v", lit.Value, err)
		}
		route := mountedRoute{pattern: pattern}
		if wrap, ok := call.Args[1].(*ast.CallExpr); ok {
			if id, ok := wrap.Fun.(*ast.Ident); ok {
				route.wrapper = id.Name
			}
		}
		routes = append(routes, route)
		return true
	})
	if len(routes) < 30 {
		t.Fatalf("parsed %d routes from route_table.go; the table parser no longer matches Handler()", len(routes))
	}
	return routes
}

// ownedSessionRoutes returns the /v1/sessions/{id} routes the read or
// manage ownership wrapper mounts.
func ownedSessionRoutes(t *testing.T) []mountedRoute {
	t.Helper()
	var out []mountedRoute
	for _, r := range handlerRoutes(t) {
		if r.sessionIDRoute() && (r.wrapper == "readID" || r.wrapper == "manageID") {
			out = append(out, r)
		}
	}
	return out
}

// countingGetStore counts the store Get calls a request makes. The
// ownership check issues exactly one Get on a refusal over this store, so a
// further Get shows that a handler ran.
type countingGetStore struct {
	*memstore.Store
	gets atomic.Int64
}

func (c *countingGetStore) Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	c.gets.Add(1)
	return c.Store.Get(ctx, tenantID, id)
}

// ownershipMatrixServer builds a session server over the seedAccessRows
// rows and the accessRoles custom roles, with an eval store, and returns it
// with the counting store.
func ownershipMatrixServer(t *testing.T, multiTenant bool) (http.Handler, *countingGetStore) {
	t.Helper()
	store := &countingGetStore{Store: memstore.New()}
	seedAccessRows(t, store.Store)
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader: true,
		MultiTenant:     multiTenant,
		CustomRoles:     accessRoles(t),
		Evals:           evalstore.NewMemory(0, nil),
	})
	t.Cleanup(srv.Close)
	return srv.Handler(), store
}

// matrixRequest sends one request as c with body, bounded so a streaming
// handler returns, and returns the recorder.
func matrixRequest(t *testing.T, h http.Handler, c accessCaller, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body))).WithContext(ctx)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	req.Header.Set("Content-Type", "application/json")
	if c.principal != nil {
		req = req.WithContext(authmw.WithPrincipal(req.Context(), *c.principal))
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// missingSessionBody returns the body a route answers for a session id
// that does not exist.
func missingSessionBody(t *testing.T, route mountedRoute) string {
	t.Helper()
	h, _ := ownershipMatrixServer(t, false)
	rr := matrixRequest(t, h, accessCaller{"alice", principalOf(accessAlice, pkgauth.RoleUser)}, route.method(), route.path("sess_missing"), "{}")
	if rr.Code != http.StatusNotFound || errorCode(t, rr) != "RESOURCE_NOT_FOUND" {
		t.Fatalf("%s on a missing session = %d %s, want 404 RESOURCE_NOT_FOUND", route.pattern, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

// Outcomes of one matrix cell.
const (
	cellGranted   = "granted"
	cellNotFound  = "not found"
	cellForbidden = "forbidden"
	cellNoSubject = "unauthorized"
)

// ownershipMatrixCallers are the callers of the route matrix with their
// outcome on a read route and on an action route naming alice's session.
var ownershipMatrixCallers = []struct {
	caller      accessCaller
	multiTenant bool
	read, act   string
}{
	{caller: accessCaller{"alice/user", principalOf(accessAlice, pkgauth.RoleUser)}, read: cellGranted, act: cellGranted},
	{caller: accessCaller{"bob/user", principalOf(accessBob, pkgauth.RoleUser)}, read: cellNotFound, act: cellNotFound},
	{caller: accessCaller{"bob role-less single-tenant", principalOf(accessBob)}, read: cellNotFound, act: cellNotFound},
	{caller: accessCaller{"bob/tenant-viewer", principalOf(accessBob, pkgauth.RoleTenantViewer)}, read: cellGranted, act: cellForbidden},
	{caller: accessCaller{"bob/tenant-admin", principalOf(accessBob, pkgauth.RoleTenantAdmin)}, read: cellGranted, act: cellNotFound},
	{caller: accessCaller{"bob/platform-admin", principalOf(accessBob, pkgauth.RolePlatformAdmin)}, read: cellGranted, act: cellNotFound},
	{caller: accessCaller{"bob/custom session-reader", principalOf(accessBob, pkgauth.Role(sessionReader))}, read: cellGranted, act: cellNotFound},
	{caller: accessCaller{"bob role-less multi-tenant", principalOf(accessBob)}, multiTenant: true, read: cellForbidden, act: cellForbidden},
	{caller: accessCaller{"no principal", nil}, read: cellNoSubject, act: cellNoSubject},
	{caller: accessCaller{"empty subject with roles", principalOf("", pkgauth.RoleTenantAdmin)}, read: cellNoSubject, act: cellNoSubject},
	{caller: accessCaller{"empty subject role-less", principalOf("")}, read: cellNoSubject, act: cellNoSubject},
}

// TestSessionRouteOwnershipMatrix_spec_10_2 drives every /v1/sessions/{id}
// route the read or manage ownership wrapper mounts, taken from
// route_table.go, through every caller of the §10.2 matrix on alice's
// session. A read succeeds for the owner and for a principal whose role
// grants "Read other users' sessions (same tenant)"; an action succeeds for
// the owner alone; a tenant-viewer on an action route keeps the permission
// gate's 403; a request with no principal subject receives 401; and every
// other refusal answers a body byte-identical to a missing session's
// without reaching the handler. The discriminating row is the role-less
// single-tenant bob, who the permission gate admits to every permission
// and who must still receive 404 on alice's session.
//
// spec: §10.2 (Session ownership) rules 3, 4, 5, 6, and 7.
func TestSessionRouteOwnershipMatrix_spec_10_2(t *testing.T) {
	for _, route := range ownedSessionRoutes(t) {
		missing := missingSessionBody(t, route)
		act := route.wrapper == "manageID"
		for _, mc := range ownershipMatrixCallers {
			want := mc.read
			if act {
				want = mc.act
			}
			t.Run(route.pattern+"/"+mc.caller.name, func(t *testing.T) {
				h, store := ownershipMatrixServer(t, mc.multiTenant)
				rr := matrixRequest(t, h, mc.caller, route.method(), route.path("sess_alice"), "{}")
				assertMatrixCell(t, rr, store.gets.Load(), want, missing)
			})
		}
	}
}

// assertMatrixCell checks one matrix response against its outcome. gets is
// the number of store Gets the request made: a refusal reads the row at
// most once, in the ownership check, and never reaches the handler.
func assertMatrixCell(t *testing.T, rr *httptest.ResponseRecorder, gets int64, want, missing string) {
	t.Helper()
	body := rr.Body.String()
	switch want {
	case cellGranted:
		if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden || body == missing {
			t.Fatalf("granted caller refused: %d %s", rr.Code, body)
		}
		return
	case cellNotFound:
		if rr.Code != http.StatusNotFound || body != missing {
			t.Fatalf("refusal = %d %s, want the missing-session body %s", rr.Code, body, missing)
		}
		if gets != 1 {
			t.Errorf("store Gets = %d, want 1 (the ownership check's read only); the handler ran on a refusal", gets)
		}
		return
	case cellForbidden:
		if rr.Code != http.StatusForbidden || errorCode(t, rr) != "FORBIDDEN" {
			t.Fatalf("status = %d %s, want 403 FORBIDDEN from the permission gate", rr.Code, body)
		}
	case cellNoSubject:
		if rr.Code != http.StatusUnauthorized || errorCode(t, rr) != "UNAUTHORIZED" {
			t.Fatalf("status = %d %s, want 401 UNAUTHORIZED", rr.Code, body)
		}
	}
	if gets != 0 {
		t.Errorf("store Gets = %d, want 0; the refusal read the session or reached the handler", gets)
	}
}

// TestEvalRouteStaysOpenToNonOwnerScorer_spec_10_7 pins that the eval
// route keeps its §10.7 caller rule: a principal that does not own the
// session and holds only session:eval:write through a custom role records
// a score on it, while the ownership check still refuses that principal on
// an owner-scoped route.
//
// spec: §10.7 (Experiment Primitives); §10.2 (Session ownership).
func TestEvalRouteStaysOpenToNonOwnerScorer_spec_10_7(t *testing.T) {
	h, _ := ownershipMatrixServer(t, false)
	scorer := accessCaller{"bob/custom session-scorer", principalOf(accessBob, pkgauth.Role(sessionScorer))}
	rr := matrixRequest(t, h, scorer, http.MethodPost, "/v1/sessions/sess_alice/eval", `{"scorer":"rubric","score":0.5}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("non-owner eval = %d %s, want 201", rr.Code, rr.Body.String())
	}
	if rr := matrixRequest(t, h, scorer, http.MethodGet, "/v1/sessions/sess_alice", ""); rr.Code != http.StatusForbidden {
		t.Errorf("scorer GET alice's session = %d %s, want the permission gate's 403", rr.Code, rr.Body.String())
	}
}
