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

// A reverted tunable leaves its helper behind: the flag, the option, and the
// test go, and the parsing helper the flag was introduced with keeps compiling
// with no caller. The lenny-ops command package is where the §25.5 event-stream
// read source is wired, so an unreferenced helper there reads as a live
// operator control that no longer exists. The tier-0 golangci-lint pass is
// advisory while its pre-existing findings are burned down, so this check pins
// the property for this package directly.

// TestOpsCommandHasNoUnreferencedFunctions asserts every unexported plain
// function declared in cmd/lenny-ops is referenced somewhere in the package,
// counting its tests. A failure names a function whose last caller was removed;
// delete it together with whatever else the removal orphaned.
func TestOpsCommandHasNoUnreferencedFunctions(t *testing.T) {
	dir := filepath.Join(schematest.RepoRoot(t), "cmd", "lenny-ops")
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

	for name, pos := range declared {
		// The declaration itself contributes one identifier occurrence.
		if used[name] <= 1 {
			t.Errorf("%s: func %s has no reference in cmd/lenny-ops; a removed surface left it compiling but dead", pos, name)
		}
	}
}

// unexportedFuncName reports whether name is an unexported Go identifier.
func unexportedFuncName(name string) bool {
	if name == "" || name == "main" {
		return false
	}
	first := name[0]
	return first >= 'a' && first <= 'z'
}
