// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// legalHoldScopeRevisit names what a failure of these tripwires means:
// the legal-hold setter has gained a scope beyond session and artifact.
// The tenant `audit` eraser in Phase 4 deletes the tenant's audit rows
// with no escrow skip, which is correct only while no audit-range hold can
// exist, so it has to be revisited before such a scope ships.
const legalHoldScopeRevisit = "the legal-hold setter accepts a scope beyond session and artifact; revisit the tenant deletion `audit` eraser, which deletes audit rows with no legal-hold escrow skip"

// TestLegalHoldSetterAcceptsOnlySessionAndArtifactScopes_spec_12_8 pins the
// legal-hold setter's scopes: the request body carries only a session or
// an artifact identifier, and a body naming any other resource is refused
// with no hold recorded.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 4), 12.8 (Compliance Interfaces: --acknowledge-hold-override admin action (tenant scope))
func TestLegalHoldSetterAcceptsOnlySessionAndArtifactScopes_spec_12_8(t *testing.T) {
	var fields []string
	typ := reflect.TypeOf(admin.LegalHoldRequest{})
	for i := 0; i < typ.NumField(); i++ {
		fields = append(fields, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	if got, want := strings.Join(fields, ","), "tenantId,sessionId,artifactId,hold,note"; got != want {
		t.Fatalf("LegalHoldRequest fields = %s, want %s: %s", got, want, legalHoldScopeRevisit)
	}

	router, _, audit := newLegalHoldAdmin(t)
	body := []byte(`{"tenantId":"acme","resourceType":"audit_range","resourceId":"r1","hold":true,"note":"order"}`)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPost, "/v1/admin/legal-hold", bytes.NewReader(body)))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("audit_range hold: status %d, want 400: %s", rr.Code, legalHoldScopeRevisit)
	}
	if n := len(audit.snapshot()); n != 0 {
		t.Fatalf("a refused audit_range hold wrote %d audit events", n)
	}
}

// TestLegalHoldEventsNameOnlySessionAndArtifact_spec_12_8 scans the admin
// package source and requires every emitLegalHold and
// writeLegalHoldCleared call to pass the resource type "session" or
// "artifact". The one exception is the emitLegalHold call inside
// writeLegalHoldCleared, which forwards its own resourceType parameter.
//
// spec: 12.8 (Compliance Interfaces: Tenant deletion lifecycle Phase 4)
func TestLegalHoldEventsNameOnlySessionAndArtifact_spec_12_8(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "emitLegalHold" && sel.Sel.Name != "writeLegalHoldCleared") || len(call.Args) < 3 {
					return true
				}
				calls++
				arg := call.Args[2]
				if id, ok := arg.(*ast.Ident); ok && id.Name == "resourceType" &&
					sel.Sel.Name == "emitLegalHold" && fn.Name.Name == "writeLegalHoldCleared" {
					return true
				}
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Errorf("%s: %s passes a non-literal resource type: %s", fset.Position(call.Pos()), sel.Sel.Name, legalHoldScopeRevisit)
					return true
				}
				if v, _ := strconv.Unquote(lit.Value); v != "session" && v != "artifact" {
					t.Errorf("%s: %s passes resource type %q: %s", fset.Position(call.Pos()), sel.Sel.Name, v, legalHoldScopeRevisit)
				}
				return true
			})
		}
	}
	if calls == 0 {
		t.Fatal("no emitLegalHold or writeLegalHoldCleared call found; the scan is vacuous")
	}
}
