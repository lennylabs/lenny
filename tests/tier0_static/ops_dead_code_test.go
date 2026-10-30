// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
//
// The check counts references from non-test code only. A predicate or accessor
// whose last production caller was rewired away, but whose test still calls it,
// compiles and passes while asserting the behavior of a path no request takes.
// That is the state a source-selection or metrics rewiring leaves behind, so a
// test reference alone does not keep a declaration alive here.

// opsDeadCodePackages are the directories the check covers, relative to the
// repository root.
var opsDeadCodePackages = [][]string{
	{"cmd", "lenny-ops"},
	{"pkg", "ops", "events"},
	{"pkg", "ops", "eventsubscription"},
	{"pkg", "ops", "opsservice"},
}

// productionRoots are the trees whose non-test files count as a production
// reference. A declaration reachable from none of them is dead regardless of
// how many tests call it.
var productionRoots = []string{"pkg", "cmd", "sdks", "migrations"}

// interfaceMethodNames are method names a type carries to satisfy an interface
// declared outside this repository (the standard library, a dependency), which
// callers invoke through that interface rather than by name. They are exempt:
// their absence from the repository's own identifiers is the normal case rather
// than evidence of a dead surface.
var interfaceMethodNames = map[string]bool{
	"Close": true, "Error": true, "Flush": true, "Handle": true,
	"Header": true, "MarshalJSON": true, "Read": true, "ServeHTTP": true,
	"Start": true, "Stop": true, "String": true, "UnmarshalJSON": true,
	"Write": true, "WriteHeader": true,
}

// trackedUnwiredDeclarations are declarations the check would otherwise report
// whose fix is to wire them into the path their own documentation names rather
// than to delete them, and which are recorded as OPEN findings in TEST-GAPS.md.
// Exempting one here keeps the guard useful for the removals it exists to catch
// while the missing wiring is tracked where the audit can act on it.
var trackedUnwiredDeclarations = map[string]string{
	// The §25.5 secret-rotation overlap predicate. The delivery worker signs
	// with the current secret alone and never consults it; the missing wiring
	// is recorded in the coverage audit (TEST-GAPS.md).
	"method Record.WithinRotationOverlap": "the §25.5 secret-rotation overlap predicate, unwired and tracked in the coverage audit",
}

// TestOpsPackagesHaveNoUnreferencedFunctions asserts every function and method
// declared in the operational event-stream packages is referenced from
// non-test code somewhere in the repository. A failure names a declaration
// whose last production caller was removed; delete it together with whatever
// else the removal orphaned, or give it a production consumer.
func TestOpsPackagesHaveNoUnreferencedFunctions(t *testing.T) {
	production := productionIdentifiers(t)
	for _, parts := range opsDeadCodePackages {
		dir := filepath.Join(append([]string{schematest.RepoRoot(t)}, parts...)...)
		name := strings.Join(parts, "/")
		t.Run(name, func(t *testing.T) {
			for fn, pos := range unreferencedFuncs(t, dir, production) {
				if finding, tracked := trackedUnwiredDeclarations[fn]; tracked {
					t.Logf("%s: %s is unwired and tracked as %s", pos, fn, finding)
					continue
				}
				t.Errorf("%s: %s has no reference in non-test code; a removed surface left it compiling but reachable only from tests", pos, fn)
			}
		})
	}
}

// productionIdentifiers counts every identifier occurrence in the non-test Go
// files under the production roots, which is the reference set a declaration
// must appear in beyond its own declaration site.
func productionIdentifiers(t *testing.T) map[string]int {
	t.Helper()
	used := map[string]int{}
	root := schematest.RepoRoot(t)
	for _, tree := range productionRoots {
		base := filepath.Join(root, tree)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if perr != nil {
				// A file the parser rejects contributes no references; the
				// build and vet stages of tier 0 report it.
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					used[id.Name]++
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
	return used
}

// unreferencedFuncs returns the functions and methods declared in the non-test
// files of dir that no non-test file in the repository refers to beyond their
// own declaration.
func unreferencedFuncs(t *testing.T, dir string, production map[string]int) map[string]token.Position {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	declared := map[string]token.Position{}
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			if strings.HasSuffix(path, "_test.go") {
				// A helper declared in a test file is exercised by that file's
				// own suite.
				continue
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name == nil {
					continue
				}
				name := fn.Name.Name
				if name == "init" || name == "main" || strings.HasPrefix(name, "Test") {
					continue
				}
				if fn.Recv != nil {
					if interfaceMethodNames[name] {
						continue
					}
					declared["method "+receiverName(fn)+"."+name] = fset.Position(fn.Pos())
					continue
				}
				declared["func "+name] = fset.Position(fn.Pos())
			}
		}
	}

	out := map[string]token.Position{}
	for label, pos := range declared {
		name := label[strings.LastIndex(label, ".")+1:]
		if i := strings.LastIndex(label, " "); strings.HasPrefix(label, "func ") {
			name = label[i+1:]
		}
		// The declaration itself contributes one identifier occurrence.
		if production[name] <= 1 {
			out[label] = pos
		}
	}
	return out
}

// receiverName returns the receiver type's name for a method declaration, so a
// failure names the method rather than a bare identifier.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch typ := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := typ.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return typ.Name
	}
	return ""
}
