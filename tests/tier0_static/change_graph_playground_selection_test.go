// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 27.8 (Web Playground — Metrics); TESTING.md §5 "Every package under
// `pkg/` appears in the change graph (or under an explicit `pkg/**` glob)."
// diagnosis: pkg/gateway/mcpfabric/playground implements the §27 web
//
//	playground and owns an in-package unit suite (playground_test.go,
//	metrics_catalog_test.go, metrics_increment_test.go, auth.go/oidc.go
//	tests, and others). Its sibling packages under pkg/gateway/mcpfabric
//	(mcp, mcpruntimes) each have a tests/change-graph.json glob entry
//	mapping to at least the unit tier. Without a
//	"pkg/gateway/mcpfabric/playground" entry, a change under this
//	package resolves to an empty tier set (static only) and
//	`lenny-test --changed`/`--since` never re-selects the package's own
//	unit suite. Add a "pkg/gateway/mcpfabric/playground" glob entry
//	mapping to at least the unit tier, mirroring the sibling
//	pkg/gateway/mcpfabric/mcp and pkg/gateway/mcpfabric/mcpruntimes
//	entries.
func TestChangeGraphPlaygroundPackageSelectsUnitTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "pkg/gateway/mcpfabric/playground/playground.go")

	if len(tiers) == 0 {
		t.Fatal("a change to pkg/gateway/mcpfabric/playground resolved to an empty tier set (static only); it owns an in-package unit suite, so tests/change-graph.json must map \"pkg/gateway/mcpfabric/playground\" to at least the unit tier")
	}
	if !tiers["unit"] {
		t.Errorf("a change to pkg/gateway/mcpfabric/playground resolved to tiers %v; it owns an in-package unit suite, so the resolution must include %q",
			sortedKeys(tiers), "unit")
	}
}

// playgroundChangeGraphPackages are the two mcpfabric packages the §27
// web playground is implemented in. Both are driven by the tier-4
// playground integration suite, so both must claim it in the change
// graph.
var playgroundChangeGraphPackages = []string{
	"pkg/gateway/mcpfabric/mcp",
	"pkg/gateway/mcpfabric/playground",
}

// spec: 27.7 (Assets and CSP), 27.8 (Metrics), 27.9 (Security), 27.10
// (Roll-forward); TESTING.md §5 "`tests/change-graph.json` maps source
// packages, schemas, migrations, and chart templates to the tests that
// exercise them. The lenny-test harness walks this graph when invoked
// with --changed to determine the minimal test set."
// diagnosis: the tier-4 playground integration suite grew past the one
//
//	file (playground_frame_redaction_test.go) the change graph records
//	against pkg/gateway/mcpfabric/mcp and pkg/gateway/mcpfabric/playground.
//	A failure means tests/tier4_integration holds a playground test that
//	no change-graph entry claims, so the graph under-reports which tests
//	exercise a playground change and a file-scoped selection would skip
//	it. Add the named file to the "integration" list on the reported
//	package entry in tests/change-graph.json. The playground entry owns
//	every tests/tier4_integration/playground_*_test.go file (including
//	the ones that drive the real cmd/lenny-gateway process rather than
//	importing the package); the mcp entry owns every tier-4 file that
//	imports pkg/gateway/mcpfabric/mcp.
func TestChangeGraphPlaygroundPackagesClaimTier4PlaygroundSuite(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	globs := readChangeGraphGlobs(t, root)

	for _, pkg := range playgroundChangeGraphPackages {
		tiers := resolveChangeGraphTiers(t, pkg+"/change.go")
		if !tiers["integration"] {
			t.Errorf("a change to %s resolved to tiers %v; the tier-4 playground suite drives this package, so the resolution must include %q",
				pkg, sortedKeys(tiers), "integration")
		}

		entry, ok := globs[pkg]
		if !ok {
			t.Errorf("tests/change-graph.json has no %q entry", pkg)
			continue
		}
		claimed := entry["integration"]
		for _, want := range expectedTier4PlaygroundTests(t, root, pkg) {
			if !changeGraphClaims(claimed, want) {
				t.Errorf("tests/change-graph.json entry %q does not claim tier-4 test %s in its \"integration\" list (claims %v); that test exercises the package, so a package-scoped selection must reach it",
					pkg, want, claimed)
			}
		}
	}
}

// readChangeGraphGlobs decodes the change graph's glob table.
func readChangeGraphGlobs(t *testing.T, root string) map[string]map[string][]string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, "tests", "change-graph.json"))
	if err != nil {
		t.Fatalf("read change-graph.json: %v", err)
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse change-graph.json: %v", err)
	}
	return doc.Globs
}

// changeGraphClaims reports whether a change-graph test list covers the
// given repo-relative path, either verbatim or through a trailing
// "..." directory glob (the form the harness and the map's own
// convention use for a whole test directory).
func changeGraphClaims(entries []string, path string) bool {
	for _, e := range entries {
		if e == path {
			return true
		}
		if strings.HasSuffix(e, "...") && strings.HasPrefix(path, strings.TrimSuffix(e, "...")) {
			return true
		}
	}
	return false
}

// expectedTier4PlaygroundTests returns the tier-4 playground test files
// the given package must claim. The suite is the
// tests/tier4_integration/playground_*_test.go set. Every one of them
// exercises the playground package, including the config, gating, and
// startup tests that drive the playground through the real
// cmd/lenny-gateway process and so import no package at all. The mcp
// package owns the subset that imports it, which is the set driving MCP
// frames over the playground WebSocket carrier.
func expectedTier4PlaygroundTests(t *testing.T, root, pkg string) []string {
	t.Helper()
	dir := filepath.Join(root, "tests", "tier4_integration")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read tests/tier4_integration: %v", err)
	}
	importPath := "github.com/lennylabs/lenny/" + pkg
	want := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "playground_") || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		rel := "tests/tier4_integration/" + name
		if pkg == "pkg/gateway/mcpfabric/playground" || fileImports(t, filepath.Join(dir, name), importPath) {
			want[rel] = true
		}
	}
	out := make([]string, 0, len(want))
	for p := range want {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// fileImports reports whether the Go file at path imports importPath.
// Parsing the import block (rather than grepping) keeps a prose mention
// of the package in a comment from counting as a dependency.
func fileImports(t *testing.T, path, importPath string) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, imp := range f.Imports {
		if imp.Path != nil && strings.Trim(imp.Path.Value, `"`) == importPath {
			return true
		}
	}
	return false
}
