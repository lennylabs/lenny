// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// inPackageCase describes one fixture repository for the in-package half
// of the spec-map orphan sweep: which sections carry which `tests`
// references and `packages` claims, which test files exist on disk (with
// or without a `// spec:` annotation), and which paths the in-package
// pending file waives.
type inPackageCase struct {
	tests     map[string][]string
	packages  map[string][]string
	annotated []string
	plain     []string
	waived    []string
}

// runInPackageFixture materialises the case in a temp repo root and
// returns the test-files-mapped result for it.
func runInPackageFixture(t *testing.T, c inPackageCase) checkResult {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	for _, rel := range c.annotated {
		write(rel, "package p\n\n// spec: 27.6 (fixture)\nfunc TestFixture(t *testing.T) {}\n")
	}
	for _, rel := range c.plain {
		write(rel, "package p\n\nfunc helper() {}\n")
	}

	sections := map[string]any{}
	for name, refs := range c.tests {
		sections[name] = map[string]any{"tests": refs, "packages": c.packages[name]}
	}
	for name, pkgs := range c.packages {
		if _, ok := sections[name]; ok {
			continue
		}
		sections[name] = map[string]any{"packages": pkgs}
	}
	specMapPath := filepath.Join(root, "spec-map.json")
	raw, err := json.Marshal(map[string]any{"version": 1, "sections": sections})
	if err != nil {
		t.Fatalf("marshal spec-map: %v", err)
	}
	if err := os.WriteFile(specMapPath, raw, 0o644); err != nil {
		t.Fatalf("write spec-map: %v", err)
	}

	pendingPath := filepath.Join(root, "spec-map-inpackage-pending.txt")
	body := "# fixture waivers\n"
	for _, w := range c.waived {
		body += w + "\n"
	}
	if err := os.WriteFile(pendingPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write pending: %v", err)
	}
	return validateTestFilesMapped(specMapPath, pendingPath, root)
}

// TestValidateTestFilesMappedCoversInPackageTests pins the in-package
// half of the spec-map orphan sweep. A test that sits next to the code
// it exercises (the playground session-lifecycle and mint-scope suites
// live in pkg/gateway/mcpfabric/playground and pkg/gateway/sessionserver
// rather than under tests/tierN) used to escape the sweep entirely,
// because the walk visited only the tier directories. A section could
// claim a package, the package could gain an annotated test file, and
// `validate-maps` would report no orphan while the section traced to
// none of that coverage.
//
// spec: TESTING.md §5 ("`tests/spec-map.json` maps every spec section to
// the tests, packages, migrations, and chart templates that encode it";
// validation confirms "Every test function with a `// spec:` annotation
// appears in the spec map under each section it lists"). The obligation
// attaches to the annotated test, so where the file lives does not
// change it, and a test file carrying no annotation is outside it.
func TestValidateTestFilesMappedCoversInPackageTests(t *testing.T) {
	pkg := "pkg/gateway/mcpfabric/playground"

	// Every annotated in-package test file of a claimed package is
	// referenced: pass.
	r := runInPackageFixture(t, inPackageCase{
		tests: map[string][]string{"27.6": {
			pkg + "/playground_test.go::TestEffectiveIdleAndDurationCaps",
			pkg + "/sessionrecord_test.go::TestLogoutRevokesSessionBearers",
		}},
		packages:  map[string][]string{"27.6": {pkg}},
		annotated: []string{pkg + "/playground_test.go", pkg + "/sessionrecord_test.go"},
	})
	expectPass(t, r)

	// A sibling annotated test file lands in the claimed package and
	// nobody adds it to the map: the sweep names it.
	r = runInPackageFixture(t, inPackageCase{
		tests:     map[string][]string{"27.6": {pkg + "/playground_test.go::TestEffectiveIdleAndDurationCaps"}},
		packages:  map[string][]string{"27.6": {pkg}},
		annotated: []string{pkg + "/playground_test.go", pkg + "/sessionrecord_test.go"},
	})
	expectFail(t, r, pkg+"/sessionrecord_test.go")

	// The same unmapped file, waived in the in-package pending file:
	// tolerated, so the gate ratchets on new drift instead of failing on
	// the pre-existing backlog.
	r = runInPackageFixture(t, inPackageCase{
		tests:     map[string][]string{"27.6": {pkg + "/playground_test.go::TestEffectiveIdleAndDurationCaps"}},
		packages:  map[string][]string{"27.6": {pkg}},
		annotated: []string{pkg + "/playground_test.go", pkg + "/sessionrecord_test.go"},
		waived:    []string{pkg + "/sessionrecord_test.go"},
	})
	expectPass(t, r)

	// A test file that carries no `// spec:` annotation (a helper file,
	// say) is outside the mapping obligation and is not reported.
	r = runInPackageFixture(t, inPackageCase{
		tests:     map[string][]string{"27.6": {pkg + "/playground_test.go::TestEffectiveIdleAndDurationCaps"}},
		packages:  map[string][]string{"27.6": {pkg}},
		annotated: []string{pkg + "/playground_test.go"},
		plain:     []string{pkg + "/helpers_test.go"},
	})
	expectPass(t, r)

	// A package-level reference covers every file in the package,
	// matching how the tier walk already resolves a directory entry.
	r = runInPackageFixture(t, inPackageCase{
		tests:     map[string][]string{"27.6": {pkg + "/..."}},
		packages:  map[string][]string{"27.6": {pkg}},
		annotated: []string{pkg + "/playground_test.go", pkg + "/sessionrecord_test.go"},
	})
	expectPass(t, r)

	// An ancestor selection glob does not stand in for a reference. A
	// `pkg/gateway/...` entry selects the unit tier across dozens of
	// packages, so honouring it here would waive the playground package
	// and every other package beneath the gateway tree.
	r = runInPackageFixture(t, inPackageCase{
		tests:     map[string][]string{"27.6": {"pkg/gateway/..."}},
		packages:  map[string][]string{"27.6": {pkg}},
		annotated: []string{pkg + "/sessionrecord_test.go"},
	})
	expectFail(t, r, pkg+"/sessionrecord_test.go")

	// Claiming a package does not pull in its subdirectories: each Go
	// package is claimed on its own, so the sweep stays at the directory
	// the section names.
	r = runInPackageFixture(t, inPackageCase{
		tests:     map[string][]string{"27.6": {pkg + "/playground_test.go::TestEffectiveIdleAndDurationCaps"}},
		packages:  map[string][]string{"27.6": {pkg}},
		annotated: []string{pkg + "/playground_test.go", pkg + "/assets/assets_test.go"},
	})
	expectPass(t, r)

	// A section may claim a package that has not shipped yet, and may
	// name a chart template rather than a directory. Neither breaks the
	// walk.
	r = runInPackageFixture(t, inPackageCase{
		packages: map[string][]string{"27.6": {
			"pkg/not/shipped/yet",
			"charts/lenny/templates/playground.yaml",
			"migrations/",
		}},
		annotated: []string{"charts/lenny/templates/playground.yaml"},
	})
	expectPass(t, r)

	// The tier-directory half of the sweep still reports its own
	// orphans, and reports them whether or not the file is annotated.
	r = runInPackageFixture(t, inPackageCase{
		tests: map[string][]string{"27.5": {"tests/tier3_contract/playground/playground_test.go"}},
		annotated: []string{
			"tests/tier3_contract/playground/playground_test.go",
			"tests/tier4_integration/playground_ws_carrier_test.go",
		},
	})
	expectFail(t, r, "tests/tier4_integration/playground_ws_carrier_test.go")
}
