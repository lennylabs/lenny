// SPDX-License-Identifier: MIT

package main

import "testing"

// spec: 26 line 8 ("CI fails the release if conformance tests for the
// declared level regress ... `lenny runtime validate` flags
// declared-vs-observed drift"); TESTING.md §12.10 ("The nine reference
// runtimes ... run conformance on every nightly ... the harness
// asserts the level-specific battery passes and that the runtime
// advertises only the capabilities it actually implements").
func TestRunConformanceTierReferenceCatalogSubsetExecutesChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("shells out to `go test -tags=conformance` against ./tests/tier10_conformance/...; skipped under -short")
	}
	status, detail, tr := runConformanceTier([]string{"reference-catalog"})
	if status == "skip" || status == "skipped" {
		t.Fatalf(
			"reference-catalog subset reported status %q (detail: %q); "+
				"nightly, weekly, and pre-release (tiers.go tiersForGroup) all "+
				"select this subset expecting it to run the §26 catalog "+
				"conformance battery, not silently report nothing to check",
			status, detail,
		)
	}
	if status != "pass" {
		t.Fatalf("reference-catalog subset = %q, want pass (detail: %s)", status, detail)
	}
	if tr == nil || tr.Total == 0 {
		t.Fatalf("reference-catalog subset executed zero checks (tierResult=%+v); "+
			"the nightly \"run conformance on every nightly\" requirement is unmet "+
			"when this subset resolves to no executable test", tr)
	}
}
