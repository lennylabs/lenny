// SPDX-License-Identifier: MIT

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// migrationToolingPath is the import path of the migration tooling's
// command, which is the one package a run of the tooling starts in.
const migrationToolingPath = "github.com/lennylabs/lenny/scripts/specshift"

// gateBatteryDir holds the static-tier gates of the migration, which are
// the tooling's second entry point: a gate runs the tooling's predicates
// over the tracked tree on every static-tier run.
const gateBatteryDir = "../../tests/tier0_static"

// TestEveryMigrationToolingPackageIsReachableFromAnEntryPoint pins that
// the tooling ships no package with no caller. The tooling has two entry
// points, the command and the static-tier gate battery, and every
// package under the command's directory is reached from one of them,
// directly or transitively.
//
// A package neither entry point reaches is implemented and has no
// caller, so no run of the tooling and no gate ever enters it while the
// tree still compiles and every test still passes. A consistency check
// elsewhere in the suite that imports such a package does not make it
// reachable: the check reads the repository state and runs no pass.
//
// This is migration tooling rather than a platform behavior, so it
// carries no spec-section annotation.
func TestEveryMigrationToolingPackageIsReachableFromAnEntryPoint(t *testing.T) {
	t.Parallel()

	imports, err := toolingImportGraph(".")
	if err != nil {
		t.Fatalf("read the import graph of the migration tooling: %v", err)
	}
	if len(imports) < 2 {
		t.Fatalf("the import graph carries %d package(s), so the walk read no tree", len(imports))
	}
	gateImports, err := gateBatteryImports(gateBatteryDir)
	if err != nil {
		t.Fatalf("read the tooling packages the static-tier gates import: %v", err)
	}
	if len(gateImports) == 0 {
		t.Fatalf("the static-tier gates under %s import no package of the tooling, so the walk read no gate", gateBatteryDir)
	}

	reached := map[string]bool{migrationToolingPath: true}
	queue := []string{migrationToolingPath}
	for _, imported := range gateImports {
		if !reached[imported] {
			reached[imported] = true
			queue = append(queue, imported)
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, imported := range imports[current] {
			if reached[imported] {
				continue
			}
			reached[imported] = true
			queue = append(queue, imported)
		}
	}

	var orphaned []string
	for pkg := range imports {
		if !reached[pkg] {
			orphaned = append(orphaned, pkg)
		}
	}
	sort.Strings(orphaned)
	for _, pkg := range orphaned {
		t.Errorf("package %s is reachable neither from %s nor from the gates under %s, so the tooling ships it with no caller; wire it into a pass or a gate, or remove it",
			pkg, migrationToolingPath, gateBatteryDir)
	}
}

// gateBatteryImports returns the packages of the migration tooling the
// static-tier gates import. A gate is written as a test file, so both
// forms are read here.
func gateBatteryImports(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var imported []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		current := filepath.Join(dir, entry.Name())
		source, err := os.ReadFile(current)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), current, source, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, spec := range file.Imports {
			target, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(target, migrationToolingPath+"/") {
				imported = append(imported, target)
			}
		}
	}
	return imported, nil
}

// toolingImportGraph returns, for every package of the migration tooling
// rooted at dir, the packages of the tooling its non-test sources
// import. A fixture directory carries no package of the tooling and is
// skipped.
func toolingImportGraph(root string) (map[string][]string, error) {
	graph := map[string][]string{}
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		pkg := toolingImportPath(root, filepath.Dir(current))
		if _, ok := graph[pkg]; !ok {
			graph[pkg] = nil
		}
		source, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), current, source, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if imported == migrationToolingPath || strings.HasPrefix(imported, migrationToolingPath+"/") {
				graph[pkg] = append(graph[pkg], imported)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return graph, nil
}

// toolingImportPath returns the import path of the package in dir, given
// the root the walk started at.
func toolingImportPath(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return migrationToolingPath
	}
	return path.Join(migrationToolingPath, filepath.ToSlash(rel))
}

// exportedSymbolsWithTestCallersOnly is the inventory of exported
// symbols of the tooling that no pass and no gate references, recorded
// as it stood when this case landed. Each is reachable from the
// tooling's own unit tests or from a check elsewhere in the suite and
// from nowhere a migration run enters.
//
// The inventory only shrinks. A symbol added to it is a surface the
// tooling ships with no caller, which is the state the package-level
// case above rejects one level up, so a new one fails this case instead
// of joining the list. A listed symbol that gains a caller in a pass or
// a gate, or that is removed, fails the case too, so the record cannot
// outlive what it records.
var exportedSymbolsWithTestCallersOnly = map[string]bool{
	"citation.Ref":                true,
	"citation.Resolves":           true,
	"line.ServedArtifacts":        true,
	"name.FindReservedPhrases":    true,
	"pass.AsAbort":                true,
	"pass.NewHarnessOver":         true,
	"pass.Paths":                  true,
	"pass.Unwrap":                 true,
	"register.Load":               true,
	"register.Residual":           true,
	"register.RewriteDownward":    true,
	"register.Save":               true,
	"scope.Classes":               true,
	"scope.PathKeyedRegisterRule": true,
	"scope.Producers":             true,
	"scope.Registers":             true,
}

// TestEveryExportedMigrationToolingSymbolHasANonTestCaller pins the same
// rule one level below the package: the tooling ships no exported symbol
// a pass or a gate does not enter. A package-level walk cannot report
// one, because a package the command enters covers every symbol declared
// in it, so an exported function added to a live package and called from
// a check elsewhere in the suite alone reads as reachable.
//
// An exported symbol whose only references are test files is a surface
// of the tooling that no run of the migration reaches. The walk it
// implements belongs with the case that wants it, where the package's
// own unexported predicates answer the same question and no surface is
// added to reach them.
//
// The symbols that stood in that state when the case landed are the
// inventory above, and the case holds the population to it in both
// directions.
//
// The tooling implements the migration's operational model rather than a
// specification behavior, so the case carries no `// spec:` annotation.
func TestEveryExportedMigrationToolingSymbolHasANonTestCaller(t *testing.T) {
	t.Parallel()

	declared, err := exportedToolingSymbols(".")
	if err != nil {
		t.Fatalf("read the exported symbols of the migration tooling: %v", err)
	}
	if len(declared) == 0 {
		t.Fatalf("the walk found no exported symbol, so it read no tree")
	}
	referenced, err := toolingIdentReferences(".")
	if err != nil {
		t.Fatalf("read the identifier references of the migration tooling: %v", err)
	}
	// A gate is written as a test file and is an entry point all the
	// same, on the terms the package walk above reads it, so a symbol a
	// gate calls is entered on every static-tier run.
	gateReferences, err := gateBatteryIdentReferences(gateBatteryDir)
	if err != nil {
		t.Fatalf("read the identifier references of the static-tier gates: %v", err)
	}
	if len(gateReferences) == 0 {
		t.Fatalf("the gates under %s reference no identifier, so the walk read no gate", gateBatteryDir)
	}
	for name, count := range gateReferences {
		referenced[name] += count
	}

	found := map[string]bool{}
	var added []string
	for _, symbol := range declared {
		if referenced[symbol.name] > symbol.declarations {
			continue
		}
		key := symbol.key()
		found[key] = true
		if !exportedSymbolsWithTestCallersOnly[key] {
			added = append(added, key)
		}
	}
	sort.Strings(added)
	for _, key := range added {
		t.Errorf("%s is exported and referenced by no pass and no gate, so the tooling ships it with test callers alone; move the walk into the case that wants it, or give it a caller in a pass or a gate",
			key)
	}

	var stale []string
	for key := range exportedSymbolsWithTestCallersOnly {
		if !found[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("%s is recorded as exported with test callers alone and is no longer in that state; drop it from the inventory so the record states the tree",
			key)
	}
}

// toolingSymbol is one exported identifier the tooling declares, with
// the number of declaration sites carrying its name, so a reference is
// an occurrence beyond them.
type toolingSymbol struct {
	name         string
	where        string
	declarations int
}

// key names the symbol as the inventory records it: the last element of
// the package path and the symbol name.
func (s toolingSymbol) key() string {
	return path.Base(s.where) + "." + s.name
}

// exportedEntryPointSymbol is the one exported name of the command that
// carries no in-tree caller, because the Go runtime enters it.
const exportedEntryPointSymbol = "Main"

// exportedToolingSymbols returns every exported top-level function,
// method, type, constant, and variable the tooling's non-test sources
// declare.
func exportedToolingSymbols(root string) ([]toolingSymbol, error) {
	byName := map[string]*toolingSymbol{}
	err := walkToolingSources(root, func(pkg, path string, file *ast.File) error {
		for _, name := range exportedDeclarations(file) {
			if name == exportedEntryPointSymbol {
				continue
			}
			if existing, ok := byName[name]; ok {
				existing.declarations++
				continue
			}
			byName[name] = &toolingSymbol{name: name, where: pkg, declarations: 1}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	symbols := make([]toolingSymbol, 0, len(byName))
	for _, symbol := range byName {
		symbols = append(symbols, *symbol)
	}
	return symbols, nil
}

// exportedDeclarations returns the exported names one file declares at
// the top level, methods included.
func exportedDeclarations(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.IsExported() {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						names = append(names, s.Name.Name)
					}
				case *ast.ValueSpec:
					for _, ident := range s.Names {
						if ident.IsExported() {
							names = append(names, ident.Name)
						}
					}
				}
			}
		}
	}
	return names
}

// toolingIdentReferences counts, per identifier name, every occurrence in
// the tooling's non-test sources, declaration sites included. A name
// occurring no more often than it is declared is referenced nowhere
// outside its own declaration.
func toolingIdentReferences(root string) (map[string]int, error) {
	counts := map[string]int{}
	err := walkToolingSources(root, func(pkg, path string, file *ast.File) error {
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok {
				counts[ident.Name]++
			}
			return true
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// walkToolingSources parses every non-test Go source of the tooling and
// hands each to visit with the import path of the package it sits in.
func walkToolingSources(root string, visit func(pkg, path string, file *ast.File) error) error {
	return filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), current, source, 0)
		if err != nil {
			return err
		}
		return visit(toolingImportPath(root, filepath.Dir(current)), current, parsed)
	})
}

// gateBatteryIdentReferences counts, per identifier name, every
// occurrence in the static-tier gates.
func gateBatteryIdentReferences(dir string) (map[string]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		current := filepath.Join(dir, entry.Name())
		source, err := os.ReadFile(current)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), current, source, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok {
				counts[ident.Name]++
			}
			return true
		})
	}
	return counts, nil
}
