// SPDX-License-Identifier: MIT

package tier0_static

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/cloud"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// managedServiceFileMarkers maps each cloud provider to the substring
// every tests/tier6_e2e_cloud/managed_<marker>_test.go file name
// carries for that provider's managed relational-database and
// managed-cache surface (RDS/ElastiCache on AWS, Cloud SQL/
// Memorystore on GCP, Azure Database for PostgreSQL/Azure Cache for
// Redis on Azure). scaffolds_test.go documents the "one dedicated
// file per suite" convention this table keys off.
var managedServiceFileMarkers = map[cloud.Provider][]string{
	cloud.ProviderAWS:   {"rds", "elasticache"},
	cloud.ProviderGCP:   {"cloudsql", "memorystore"},
	cloud.ProviderAzure: {"azuredb", "azurecache"},
}

// spec: tests/tier6_e2e_cloud/scaffolds_test.go ("The user-facing
// contract: tier-6 fails (not skip, not vacuous-pass) when no
// provider is configured, and fails per-provider when a configured
// provider isn't reachable.")
//
// diagnosis: this fails when a provider in the canonical tier-6 set
// (aws, gcp, azure — cloud.ProviderAWS/GCP/Azure) has lost its last
// managed-service test file, or when that file's tests have been
// reduced to skip-only stubs with no genuine t.Fatalf/t.Errorf
// assertion. Either regression reopens the vacuous-pass gap the
// fail-closed contract forbids: a GKE or AKS tier-6 run would
// silently exercise zero managed relational-database/managed-cache
// assertions for that provider instead of failing. Restore the
// missing tests/tier6_e2e_cloud/managed_<service>_test.go file, or
// add a genuine assertion to its Test functions, to fix this.
func TestTier6ManagedServiceCoverageEveryProvider(t *testing.T) {
	t.Parallel()
	root := schematest.RepoRoot(t)
	dir := filepath.Join(root, "tests", "tier6_e2e_cloud")

	matches, err := filepath.Glob(filepath.Join(dir, "managed_*_test.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(matches) == 0 {
		t.Fatalf("no tests/tier6_e2e_cloud/managed_*_test.go files found; the tier-6 managed-service surface has been removed entirely")
	}

	covered := map[cloud.Provider][]string{}
	var unclassified []string
	for _, path := range matches {
		base := filepath.Base(path)
		p, ok := classifyManagedServiceFile(base)
		if !ok {
			unclassified = append(unclassified, base)
			continue
		}
		if !fileHasGenuineAssertion(t, path) {
			// A managed-service file with no t.Fatalf/t.Errorf anywhere
			// in it cannot fail on a bad managed-service configuration —
			// it can only skip or silently return, which is exactly the
			// vacuous-pass state the fail-closed contract forbids.
			continue
		}
		covered[p] = append(covered[p], base)
	}

	if len(unclassified) > 0 {
		sort.Strings(unclassified)
		t.Logf("tests/tier6_e2e_cloud/managed_*_test.go file(s) not recognized by managedServiceFileMarkers: %s; "+
			"register the new file's provider in that table so this check keeps validating it", strings.Join(unclassified, ", "))
	}

	// cloud.go's Provider constants are the single source of truth for
	// the tier-6 provider set; iterate it directly rather than
	// re-declaring aws/gcp/azure here so the two stay in lockstep.
	for _, p := range []cloud.Provider{cloud.ProviderAWS, cloud.ProviderGCP, cloud.ProviderAzure} {
		files := covered[p]
		if len(files) == 0 {
			t.Errorf("provider %q has no non-skipping managed-service test under tests/tier6_e2e_cloud "+
				"(expected a managed_*_test.go file matching one of %v with at least one genuine t.Fatalf/t.Errorf assertion); "+
				"a tier-6 run against this provider would vacuously pass its managed-service surface",
				p, managedServiceFileMarkers[p])
			continue
		}
		sort.Strings(files)
		t.Logf("provider %q: non-skipping managed-service coverage in %s", p, strings.Join(files, ", "))
	}
}

// classifyManagedServiceFile maps a managed_*_test.go base file name
// to the provider it tests, via managedServiceFileMarkers.
func classifyManagedServiceFile(base string) (cloud.Provider, bool) {
	for p, markers := range managedServiceFileMarkers {
		for _, m := range markers {
			if base == fmt.Sprintf("managed_%s_test.go", m) {
				return p, true
			}
		}
	}
	return "", false
}

// fileHasGenuineAssertion parses path and reports whether it contains
// at least one call of the form t.Fatalf(...), t.Errorf(...),
// t.Fatal(...), or t.Error(...) anywhere in the file (Test functions
// and the same-file require* helpers they call both bind their
// *testing.T parameter to the identifier "t" by convention in this
// package, so a file-wide scan catches a helper-mediated assertion
// as well as an inline one).
func fileHasGenuineAssertion(t *testing.T, path string) bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "t" {
			return true
		}
		switch sel.Sel.Name {
		case "Fatalf", "Errorf", "Fatal", "Error":
			found = true
		}
		return true
	})
	return found
}
