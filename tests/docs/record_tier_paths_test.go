// SPDX-License-Identifier: MIT

package docs_test

import (
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

// retiredTierPath matches a path under a numbered test tier directory.
var retiredTierPath = regexp.MustCompile(`\btests/(tier(?:0_static|2_component|3_contract|4_integration|5_e2e_kind|6_e2e_cloud|7a_load_local|7b_load_kind|8_chaos|9_security|10_conformance|11_docs|12_load_cloud))\b`)

// currentTierPaths rewrites the paths a historical record cites under a
// numbered test tier directory to the directory that holds them now. The
// records keep the words they were written with, so a test that checks a
// record's citations against the tree reads them through this function.
func currentTierPaths(record string) string {
	return retiredTierPath.ReplaceAllStringFunc(record, func(m string) string {
		return "tests/" + retiredTierDirs[m[len("tests/"):]]
	})
}

// readRecord reads a historical record (a gap record, the remediation
// plan, or a proposal's deviations) with its tier paths made current.
func readRecord(t testing.TB, path string) string {
	t.Helper()
	return currentTierPaths(readDocPage(t, path))
}

// TestCurrentTierPathsRewritesOnlyRetiredTierDirectories pins the path
// translation the record tests read the gap records through.
func TestCurrentTierPathsRewritesOnlyRetiredTierDirectories(t *testing.T) {
	cases := map[string]string{
		"cites tests/tier4_integration/erasure_job_test.go": "cites tests/integration/erasure_job_test.go",
		"tests/tier7a_load_local/x_test.go::TestX":          "tests/load_local/x_test.go::TestX",
		"tests/tier11_docs/ and tests/tier12_load_cloud/":   "tests/docs/ and tests/load_cloud/",
		"tests/tier1_unit/helm/helm_test.go":                "tests/tier1_unit/helm/helm_test.go",
		"pkg/tier0_static/x.go":                             "pkg/tier0_static/x.go",
	}
	for in, want := range cases {
		if got := currentTierPaths(in); got != want {
			t.Errorf("currentTierPaths(%q) = %q, want %q", in, got, want)
		}
	}
}
