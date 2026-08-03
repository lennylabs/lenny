// SPDX-License-Identifier: MIT

package main

import (
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
