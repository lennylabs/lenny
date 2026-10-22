// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The §12.8 erasure rank table exempts three stores on both erasure paths
// because no production code writes them: the eviction state store, the
// session routing cache, and the token service's access-token cache. An
// exemption with that reason goes stale the moment a production writer
// appears, and the store then holds personal data no eraser reaches. This
// guard fails when the reason stops holding, naming the exemption to
// revisit in pkg/gateway/storage/erasure (userExempt and tenantExempt).

// exgModulePath is the repository's Go module path.
const exgModulePath = "github.com/lennylabs/lenny"

// exgUnwrittenStore is a package an erasure exemption records as having no
// production writer.
type exgUnwrittenStore struct {
	// pkg is the package directory, relative to the repository root.
	pkg string
	// allowed are the repository directories, besides the package's own
	// subtree, whose non-test code may import it without making it a
	// production writer.
	allowed []string
	// exemption names the rank-table exemption a new importer invalidates.
	exemption string
}

// exgUnwrittenStores are the packages the redis_caches and eviction_state
// exemptions rest on.
var exgUnwrittenStores = []exgUnwrittenStore{
	{pkg: "pkg/gateway/storage/evictionstatestore", allowed: []string{"pkg/gateway/storage/evictionfallback"}, exemption: "eviction_state"},
	{pkg: "pkg/gateway/storage/evictionstatestore/pgstore", allowed: []string{"pkg/gateway/storage/evictionfallback"}, exemption: "eviction_state"},
	{pkg: "pkg/gateway/storage/evictionfallback", exemption: "eviction_state"},
	{pkg: "pkg/gateway/session/routingcache", exemption: "redis_caches"},
	// The token service constructs the access-token cache and discards it;
	// the call check below keeps it from becoming a writer.
	{pkg: "pkg/tokenservice/cache", allowed: []string{"cmd/lenny-token-service"}, exemption: "redis_caches"},
}

// exgTokenCachePkg is the access-token cache package, and exgTokenServiceDirs are
// the trees whose code would turn it into a writer by calling it.
var (
	exgTokenCachePkg    = "pkg/tokenservice/cache"
	exgTokenServiceDirs = []string{"cmd/lenny-token-service", "pkg/tokenservice"}
	exgTokenCacheCalls  = map[string]bool{"Get": true, "Put": true, "Invalidate": true}
)

// exgProductionGoFilesUnder returns the non-test Go files under dir, skipping
// testdata and vendor trees.
func exgProductionGoFilesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// exgWithin reports whether the repository-relative dir is rel or under it.
func exgWithin(dir, rel string) bool {
	return dir == rel || strings.HasPrefix(dir, rel+"/")
}

// exgExemptionViolations scans the production Go code under root and returns
// one message per broken exemption reason: a new importer of an unwritten
// store, or a token-service call to the access-token cache.
func exgExemptionViolations(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	for _, top := range []string{"pkg", "cmd", "sdks"} {
		for _, path := range exgProductionGoFilesUnder(t, filepath.Join(root, top)) {
			out = append(out, exgImportViolations(t, root, path)...)
		}
	}
	out = append(out, exgTokenCacheCallViolations(t, root)...)
	sort.Strings(out)
	return out
}

// exgImportViolations returns a message for each import in path of an
// unwritten store from outside the store's own subtree and allowed list.
func exgImportViolations(t *testing.T, root, path string) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	rel, _ := filepath.Rel(root, path)
	dir := filepath.ToSlash(filepath.Dir(rel))
	var out []string
	for _, imp := range f.Imports {
		ip, _ := strconv.Unquote(imp.Path.Value)
		for _, s := range exgUnwrittenStores {
			if ip != exgModulePath+"/"+s.pkg || exgWithin(dir, s.pkg) || exgAllowedImporter(dir, s.allowed) {
				continue
			}
			out = append(out, filepath.ToSlash(rel)+" imports "+s.pkg+
				"; the "+s.exemption+" erasure exemption (no production writer) no longer holds: "+
				"wire an eraser for it on both paths or revisit the exemption in pkg/gateway/storage/erasure")
		}
	}
	return out
}

// exgAllowedImporter reports whether dir is under one of the allowed trees.
func exgAllowedImporter(dir string, allowed []string) bool {
	for _, a := range allowed {
		if exgWithin(dir, a) {
			return true
		}
	}
	return false
}

// exgTokenCacheCallViolations returns a message for each token-service call to
// Get, Put, or Invalidate on a value of the access-token cache's Cache type.
// The cache values are the names any token-service file declares with that
// type or assigns from the package's New; a call is matched in every
// token-service file, because a field declared in one file is called from
// another.
func exgTokenCacheCallViolations(t *testing.T, root string) []string {
	t.Helper()
	files := map[string]*ast.File{}
	caches := map[string]bool{}
	for _, dir := range exgTokenServiceDirs {
		for _, path := range exgProductionGoFilesUnder(t, filepath.Join(root, dir)) {
			rel, _ := filepath.Rel(root, path)
			if exgWithin(filepath.ToSlash(filepath.Dir(rel)), exgTokenCachePkg) {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			files[filepath.ToSlash(rel)] = f
			if alias := exgImportAlias(f, exgModulePath+"/"+exgTokenCachePkg); alias != "" {
				for n := range exgCacheValueNames(f, alias) {
					caches[n] = true
				}
			}
		}
	}
	var out []string
	for rel, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !exgTokenCacheCalls[sel.Sel.Name] || !caches[exgTerminalName(sel.X)] {
				return true
			}
			out = append(out, rel+" calls "+sel.Sel.Name+" on the access-token cache; "+
				"the redis_caches erasure exemption (no production writer) no longer holds: "+
				"wire an eraser for it on both paths or revisit the exemption in pkg/gateway/storage/erasure")
			return true
		})
	}
	return out
}

// exgImportAlias returns the local name f uses for importPath, or "" when f
// does not import it.
func exgImportAlias(f *ast.File, importPath string) string {
	for _, imp := range f.Imports {
		if ip, _ := strconv.Unquote(imp.Path.Value); ip == importPath {
			if imp.Name != nil {
				return imp.Name.Name
			}
			return filepath.Base(importPath)
		}
	}
	return ""
}

// exgCacheValueNames returns the identifiers f declares with type
// *<alias>.Cache or <alias>.Cache, or assigns from <alias>.New.
func exgCacheValueNames(f *ast.File, alias string) map[string]bool {
	names := map[string]bool{}
	isCacheType := func(e ast.Expr) bool {
		if star, ok := e.(*ast.StarExpr); ok {
			e = star.X
		}
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == alias && sel.Sel.Name == "Cache"
	}
	isNewCall := func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := sel.X.(*ast.Ident)
		return ok && id.Name == alias && sel.Sel.Name == "New"
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.Field:
			if isCacheType(d.Type) {
				for _, id := range d.Names {
					names[id.Name] = true
				}
			}
		case *ast.ValueSpec:
			if d.Type != nil && isCacheType(d.Type) {
				for _, id := range d.Names {
					names[id.Name] = true
				}
			}
		case *ast.AssignStmt:
			for i, rhs := range d.Rhs {
				if isNewCall(rhs) && i < len(d.Lhs) {
					names[exgTerminalName(d.Lhs[i])] = true
				}
			}
		}
		return true
	})
	return names
}

// exgTerminalName returns the last identifier of an identifier or selector
// expression, such as accessCache for w.accessCache.
func exgTerminalName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// TestErasureExemptionsHaveNoProductionWriter_spec_12_8 fails when a store
// the erasure rank table exempts as having no production writer gains one.
//
// spec: 12.8 (Compliance Interfaces: Storage backends in erasure scope)
func TestErasureExemptionsHaveNoProductionWriter_spec_12_8(t *testing.T) {
	for _, v := range exgExemptionViolations(t, schematest.RepoRoot(t)) {
		t.Error(v)
	}
}

// TestErasureExemptionGuardCatchesNewWriters_spec_12_8 runs the guard over
// fixture trees: a clean tree passes, a new importer of an unwritten store
// fails naming the eviction_state exemption, and token-service code that
// calls Put on the access-token cache fails naming the redis_caches
// exemption.
//
// spec: 12.8 (Compliance Interfaces: Storage backends in erasure scope)
func TestErasureExemptionGuardCatchesNewWriters_spec_12_8(t *testing.T) {
	fixtures := filepath.Join("testdata", "erasure-exemption")
	cases := []struct {
		dir  string
		want []string
	}{
		{dir: "clean"},
		{dir: "importer", want: []string{"pkg/gateway/session/sessionserver/evict.go imports pkg/gateway/storage/evictionstatestore", "eviction_state"}},
		{dir: "cacheput", want: []string{"cmd/lenny-token-service/exchange.go calls Put on the access-token cache", "redis_caches"}},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			got := exgExemptionViolations(t, filepath.Join(fixtures, tc.dir))
			if len(tc.want) == 0 {
				if len(got) != 0 {
					t.Fatalf("clean fixture reported %v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("violations = %v, want one", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got[0], w) {
					t.Errorf("violation %q does not name %q", got[0], w)
				}
			}
		})
	}
}
