// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// A fixture a package builds once and reuses across its tests, such as a
// runtime binary a TestMain compiles or a directory a sync.Once fills,
// outlives any one test. When it sits under /tmp, a host cleanup of /tmp
// part-way through the run removes it, and every later test in the package
// fails with `fork/exec ... no such file or directory` or a socket error
// (TEST-GAPS T-4.7.23, the shared cause of T-4.7.19 to T-4.7.22). Such a
// fixture is created with testtmp.MkdirTemp or testtmp.ShortDir, which
// resolve a root off /tmp, rather than with os.MkdirTemp in the default
// temp directory. A literal /tmp parent is refused anywhere in a test.

// spec: TESTING.md §17.10 (Flake budget), TESTING.md §6 (The lenny-test harness)
// diagnosis: a build-once fixture (in a TestMain or a sync.Once) is created
// with os.MkdirTemp in the default temp directory, or a test names a
// literal /tmp parent. Under a TMPDIR of /tmp that fixture can vanish
// mid-run; create it with testtmp.MkdirTemp, or testtmp.ShortDir for a
// Unix socket directory.
func TestBuildOnceFixturesStayOutOfTmp_spec_17_10(t *testing.T) {
	root := schematest.RepoRoot(t)
	var offenders []string
	for _, top := range []string{"pkg", "cmd", "tests", "sdks"} {
		dir := filepath.Join(root, top)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			for _, o := range fixtureTempOffenders(t, path) {
				offenders = append(offenders, rel+": "+o)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	for _, o := range offenders {
		t.Error(o)
	}
}

// fixtureTempOffenders returns a description of each os.MkdirTemp call in
// the file that names a literal /tmp parent, or that names the default
// temp directory inside a TestMain body or a function literal passed to a
// Do method (sync.Once).
func fixtureTempOffenders(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	check := func(n ast.Node, buildOnce bool) {
		ast.Inspect(n, func(n ast.Node) bool {
			parent, ok := mkdirTempParent(n)
			if !ok {
				return true
			}
			switch {
			case parent == "/tmp" || strings.HasPrefix(parent, "/tmp/"):
				out = append(out, fset.Position(n.Pos()).String()+": os.MkdirTemp under a literal /tmp")
			case parent == "" && buildOnce:
				out = append(out, fset.Position(n.Pos()).String()+": build-once fixture in the default temp directory; use testtmp.MkdirTemp")
			}
			return true
		})
	}
	check(f, false)
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Name.Name == "TestMain" && x.Recv == nil && x.Body != nil {
				check(x.Body, true)
			}
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Do" && len(x.Args) == 1 {
				if lit, ok := x.Args[0].(*ast.FuncLit); ok {
					check(lit.Body, true)
				}
			}
		}
		return true
	})
	return dedupe(out)
}

// mkdirTempParent returns the literal parent directory of an
// os.MkdirTemp call, and false for any other node or a non-literal parent.
func mkdirTempParent(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "MkdirTemp" {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// dedupe drops repeated entries, keeping order; a call inside a TestMain
// is visited by both passes.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
