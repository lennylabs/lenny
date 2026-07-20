// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// A refactor that replaces a surface leaves the old one behind: the caller
// moves, the last reference goes with it, and the function it called keeps
// compiling with nothing reaching it. A reader then takes a dead §25.5 write
// path or a reverted operator control for a live one. The tier-0 golangci-lint
// pass is advisory while its pre-existing findings are burned down, so this
// check pins the property directly for the packages that host the operational
// event-stream read side and its wiring.

// opsDeadCodePackages are the directories the check covers, relative to the
// repository root.
var opsDeadCodePackages = [][]string{
	{"cmd", "lenny-ops"},
	{"pkg", "ops", "events"},
	{"pkg", "ops", "eventsubscription"},
	{"pkg", "ops", "opsservice"},
}

// TestOpsPackagesHaveNoUnreferencedFunctions asserts every unexported plain
// function declared in the operational event-stream packages is referenced
// somewhere in its own package, counting its tests. A failure names a function
// whose last caller was removed; delete it together with whatever else the
// removal orphaned.
func TestOpsPackagesHaveNoUnreferencedFunctions(t *testing.T) {
	for _, parts := range opsDeadCodePackages {
		dir := filepath.Join(append([]string{schematest.RepoRoot(t)}, parts...)...)
		name := strings.Join(parts, "/")
		t.Run(name, func(t *testing.T) {
			for fn, pos := range unreferencedFuncs(t, dir) {
				t.Errorf("%s: func %s has no reference in %s; a removed surface left it compiling but dead", pos, fn, name)
			}
		})
	}
}

// unreferencedFuncs returns the unexported plain functions declared in the
// non-test files of dir that no identifier in the directory (its own files and
// its tests) refers to.
func unreferencedFuncs(t *testing.T, dir string) map[string]token.Position {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	declared := map[string]token.Position{}
	used := map[string]int{}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Name == nil {
					// Methods can be referenced only through an interface
					// they satisfy, so they are outside this check.
					continue
				}
				name := fn.Name.Name
				if !unexportedFuncName(name) || name == "init" || strings.HasPrefix(name, "Test") {
					continue
				}
				if strings.HasSuffix(path, "_test.go") {
					// A helper declared in a test file is exercised by that
					// file's own suite.
					continue
				}
				declared[name] = fset.Position(fn.Pos())
			}
			ast.Inspect(file, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				used[id.Name]++
				return true
			})
		}
	}

	out := map[string]token.Position{}
	for name, pos := range declared {
		// The declaration itself contributes one identifier occurrence.
		if used[name] <= 1 {
			out[name] = pos
		}
	}
	return out
}

// unexportedFuncName reports whether name is an unexported Go identifier.
func unexportedFuncName(name string) bool {
	if name == "" || name == "main" {
		return false
	}
	first := name[0]
	return first >= 'a' && first <= 'z'
}
