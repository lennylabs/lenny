// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/platformtools/toolapproval"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/tests/testinfra/apifront"
)

// platformToolsRoutes is every client route the platform tools seam took
// from the session server, with a path that matches its pattern.
var platformToolsRoutes = []struct {
	method, path string
}{
	{http.MethodPost, "/v1/sessions/sess_x/memory"},
	{http.MethodGet, "/v1/sessions/sess_x/memory"},
	{http.MethodDelete, "/v1/sessions/sess_x/memory/mem_1"},
	{http.MethodGet, "/v1/sessions/sess_x/tree"},
	{http.MethodPost, "/v1/sessions/sess_x/tool-use/call_1/approve"},
	{http.MethodPost, "/v1/sessions/sess_x/tool-use/call_1/deny"},
	{http.MethodPost, "/v1/sessions/sess_x/elicitations/el_1/respond"},
	{http.MethodPost, "/v1/sessions/sess_x/elicitations/el_1/dismiss"},
}

// TestAttachToolRoutesMountsEveryPlatformToolsRoute pins that the session
// server the composition root attaches the platform tools seam's routes to
// serves each of them. A route the mux does not hold answers the mux's own
// plain-text 404 or 405; a mounted route answers the client API's JSON
// error envelope, whatever its access check or store decides.
//
// spec: GWSEAM-RegisterRoutes
func TestAttachToolRoutesMountsEveryPlatformToolsRoute(t *testing.T) {
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{DevTenantHeader: true})
	attachToolRoutes(srv, apifront.Routes(srv), store, nil, toolapproval.NewRegistry(), nil, nil)
	h := apifront.Handler(srv)
	for _, rt := range platformToolsRoutes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "acme")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code == http.StatusMethodNotAllowed || strings.HasPrefix(body, "404 page not found") ||
				!strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				t.Errorf("%s %s = %d %q (Content-Type %q), want the route mounted and answering a JSON body",
					rt.method, rt.path, rec.Code, body, rec.Header().Get("Content-Type"))
			}
		})
	}
}

// TestBuildSessionServerAttachesThePlatformToolsRoutes pins that the
// composition root's session server build step attaches the platform tools
// seam's routes to the session server it returns, so the production
// gateway serves them.
//
// spec: GWSEAM-RegisterRoutes
func TestBuildSessionServerAttachesThePlatformToolsRoutes(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sessionsrv.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var build *ast.FuncDecl
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "buildSessionServer" {
			build = fn
		}
	}
	if build == nil {
		t.Fatal("sessionsrv.go declares no buildSessionServer")
	}
	calls := 0
	ast.Inspect(build.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "attachToolRoutes" {
			if len(call.Args) > 0 {
				if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == "srv" {
					calls++
				}
			}
		}
		return true
	})
	if calls != 1 {
		t.Errorf("buildSessionServer calls attachToolRoutes(srv, ...) %d times, want once", calls)
	}
}
