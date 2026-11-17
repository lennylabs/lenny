// SPDX-License-Identifier: MIT

package docs_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// This file carries no spec: annotation. It reads the historical records
// for the documentation tests and implements no requirement.

// retiredTierDirs maps each numbered test tier directory to the directory
// change 0155 renamed it to. tests/tier4_integration merged into
// tests/integration.
var retiredTierDirs = map[string]string{
	"tier0_static":       "static",
	"tier2_component":    "component",
	"tier3_contract":     "contract",
	"tier4_integration":  "integration",
	"tier5_e2e_kind":     "e2e_kind",
	"tier6_e2e_cloud":    "e2e_cloud",
	"tier7a_load_local":  "load_local",
	"tier7b_load_kind":   "load_kind",
	"tier8_chaos":        "chaos",
	"tier9_security":     "security",
	"tier10_conformance": "conformance",
	"tier11_docs":        "docs",
	"tier12_load_cloud":  "load_cloud",
}

// movedUnitTests maps each test file of the retired tests/tier1_unit
// directory to the file beside the code it tests, as change 0155 moved it
// (changes/0155-test-tiers-by-name/evidence/unit-moves.tsv). The
// concurrency file split into one file per package and has no single
// successor.
var movedUnitTests = map[string]string{
	"tests/tier1_unit/flagdefaults/flagdefaults_test.go":         "cmd/lenny-gateway/flagdefaults_test.go",
	"tests/tier1_unit/helm/helm_test.go":                         "pkg/chart/values/helm_unittest_test.go",
	"tests/tier1_unit/lifecycle/lifecycle_state_machine_test.go": "tests/testinfra/lifecycleharness/lifecycle_state_machine_test.go",
	"tests/tier1_unit/lifecycle/recording_runtime_test.go":       "tests/testinfra/lifecycleharness/recording_runtime_test.go",
	"tests/tier1_unit/lifecycle/resume_exits_test.go":            "tests/testinfra/lifecycleharness/resume_exits_test.go",
	"tests/tier1_unit/lifecycle/stepped_store_test.go":           "tests/testinfra/lifecycleharness/stepped_store_test.go",
	"tests/tier1_unit/lifecycle/terminal_writers_test.go":        "tests/testinfra/lifecycleharness/terminal_writers_test.go",
	"tests/tier1_unit/rbac/rbac_test.go":                         "pkg/chart/values/cluster_role_verbs_test.go",
	"tests/tier1_unit/supervisor/harness_test.go":                "tests/testinfra/supervisorbuild/harness_test.go",
}

// staticPackages are the packages the static tier is split into.
var staticPackages = []string{"build", "chart", "lint", "structure"}

// retiredTierPath matches a path under a numbered test tier directory.
var retiredTierPath = regexp.MustCompile(`\btests/(tier(?:0_static|2_component|3_contract|4_integration|5_e2e_kind|6_e2e_cloud|7a_load_local|7b_load_kind|8_chaos|9_security|10_conformance|11_docs|12_load_cloud))\b`)

// movedUnitTestPath matches a path to a test file of tests/tier1_unit.
var movedUnitTestPath = regexp.MustCompile(`\btests/tier1_unit/[a-z]+/[a-z_]+_test\.go\b`)

// staticFilePath matches a path to a file directly under tests/static, which
// held every static-tier test before the tier was split into packages.
var staticFilePath = regexp.MustCompile(`\btests/static/([a-z0-9_]+\.go)\b`)

// currentTierPaths rewrites the paths a historical record cites under a
// retired test directory to the path that holds the file now: a numbered
// tier directory becomes the tier's name, a tests/tier1_unit test file
// becomes the file beside its code, and a static-tier file becomes the file
// in the static package that holds it under root. The records keep the
// words they were written with, so a test that checks a record's citations
// against the tree reads them through this function.
func currentTierPaths(root, record string) string {
	record = retiredTierPath.ReplaceAllStringFunc(record, func(m string) string {
		return "tests/" + retiredTierDirs[m[len("tests/"):]]
	})
	record = movedUnitTestPath.ReplaceAllStringFunc(record, func(m string) string {
		if moved, ok := movedUnitTests[m]; ok {
			return moved
		}
		return m
	})
	return staticFilePath.ReplaceAllStringFunc(record, func(m string) string {
		name := m[len("tests/static/"):]
		for _, pkg := range staticPackages {
			if _, err := os.Stat(filepath.Join(root, "tests", "static", pkg, name)); err == nil {
				return "tests/static/" + pkg + "/" + name
			}
		}
		return m
	})
}

// readRecord reads a historical record (a gap record, the remediation
// plan, or a proposal's deviations) with its test paths made current.
func readRecord(t testing.TB, path string) string {
	t.Helper()
	return currentTierPaths(repoRoot(t), readDocPage(t, path))
}

// TestCurrentTierPathsRewritesOnlyRetiredTierDirectories pins the path
// translation the record tests read the gap records through.
func TestCurrentTierPathsRewritesOnlyRetiredTierDirectories(t *testing.T) {
	root := repoRoot(t)
	cases := map[string]string{
		"cites tests/tier4_integration/erasure_job_test.go":      "cites tests/integration/erasure_job_test.go",
		"tests/tier7a_load_local/x_test.go::TestX":               "tests/load_local/x_test.go::TestX",
		"tests/tier11_docs/ and tests/tier12_load_cloud/":        "tests/docs/ and tests/load_cloud/",
		"tests/tier0_static/crds_test.go":                        "tests/static/build/crds_test.go",
		"tests/tier0_static/absent_test.go":                      "tests/static/absent_test.go",
		"tests/tier1_unit/rbac/rbac_test.go":                     "pkg/chart/values/cluster_role_verbs_test.go",
		"tests/tier1_unit/concurrency/concurrency_test.go":       "tests/tier1_unit/concurrency/concurrency_test.go",
		"pkg/tier0_static/x.go":                                  "pkg/tier0_static/x.go",
		"tests/static/structure/residual_gate_test.go unchanged": "tests/static/structure/residual_gate_test.go unchanged",
	}
	for in, want := range cases {
		if got := currentTierPaths(root, in); got != want {
			t.Errorf("currentTierPaths(%q) = %q, want %q", in, got, want)
		}
	}
}
