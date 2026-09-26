// SPDX-License-Identifier: MIT

package tier0_static

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// slotRegistryFields are the adapter Server's slot registry maps: the live
// entries keyed by slot identifier and the §5.2 reclaim holds.
var slotRegistryFields = map[string]bool{"slots": true, "reclaiming": true}

// exportedSlotRegistryReaders returns "file:Name" for every exported
// function or method in src that names a slot registry map in its body.
// §15.4 publishes the adapter's exported gRPC handlers as the contract a
// third-party adapter implements, and §4.7.1 has a caller observe the
// registry only through the answers those handlers give. The handlers reach
// the registry through unexported helpers, so an exported declaration that
// reads the maps itself is an introspection surface outside that contract.
func exportedSlotRegistryReaders(t *testing.T, name, src string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var found []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !fn.Name.IsExported() {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if ok && slotRegistryFields[sel.Sel.Name] {
				found = append(found, filepath.Base(name)+":"+fn.Name.Name)
				return false
			}
			return true
		})
	}
	return found
}

// diagnosis: an exported function or method in pkg/adapter's non-test build
// reads the slot registry maps directly, which hands every importer of the
// package a view of registry state that no §4.7.1 answer carries and widens
// the surface §15.4 publishes for third-party adapters. Observe the registry
// in tests through the handlers' answers (probe Shutdown outcomes), and keep
// any in-package helper in an _test.go file.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
func TestAdapterServerExportsNoSlotRegistryReader_spec_15_4(t *testing.T) {
	dir := filepath.Join(schematest.RepoRoot(t), "pkg", "adapter")
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("list pkg/adapter: %v", err)
	}
	var offenders []string
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		offenders = append(offenders, exportedSlotRegistryReaders(t, path, string(src))...)
	}
	sort.Strings(offenders)
	for _, o := range offenders {
		t.Errorf("%s reads the slot registry from an exported declaration; the registry is observable only through the §4.7.1 handler answers", o)
	}
}

// diagnosis: the registry encapsulation gate no longer recognizes an
// exported accessor that reads the registry maps, so it would pass a
// production build that exports one.
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
func TestSlotRegistryReaderGateFlagsAnExportedAccessor_spec_15_4(t *testing.T) {
	const src = `package adapter

func (s *Server) InspectRegistry(id string) bool {
	_, held := s.reclaiming[id]
	return held
}

func (s *Server) Handler() { s.lookup() }

func (s *Server) lookup() bool {
	_, ok := s.slots["alice"]
	return ok
}
`
	got := exportedSlotRegistryReaders(t, "fixture.go", src)
	if len(got) != 1 || got[0] != "fixture.go:InspectRegistry" {
		t.Errorf("gate found %v, want only the exported accessor fixture.go:InspectRegistry", got)
	}
}
