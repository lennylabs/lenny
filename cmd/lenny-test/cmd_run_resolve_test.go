// SPDX-License-Identifier: MIT

package main

import (
	"sort"
	"testing"
)

// TestTiersForChangedPathReadsGlobKeysLikeTheCompletenessGate pins the
// selector and the change-graph completeness gate to one reading of a
// glob key. The gate certifies a source path when a key names it or an
// ancestor directory, reading the key through changeGraphGlobPrefix. If
// this selector matched the raw key instead, a key spelled
// `pkg/foo/...` would certify every path under pkg/foo/ while resolving
// to no tier, so a change there would run no tests under `--changed`,
// which is the outcome the gate exists to prevent.
func TestTiersForChangedPathReadsGlobKeysLikeTheCompletenessGate(t *testing.T) {
	cases := []struct {
		key  string
		path string
	}{
		{"pkg/foo/...", "pkg/foo/bar.go"},
		{"pkg/foo/", "pkg/foo/bar.go"},
		{"pkg/foo", "pkg/foo/bar.go"},
		{"scripts/lint-schema.sh", "scripts/lint-schema.sh"},
	}
	for _, tc := range cases {
		globs := map[string]map[string][]string{
			tc.key: {"unit": {"pkg/..."}, "static": {"./..."}},
		}
		if !coveredByChangeGraphGlob(tc.path, map[string]bool{changeGraphGlobPrefix(tc.key): true}) {
			t.Fatalf("fixture is wrong: key %q does not cover %q in the gate", tc.key, tc.path)
		}
		got := tiersForChangedPathIn(globs, tc.path)
		sort.Strings(got)
		if len(got) != 2 || got[0] != "static" || got[1] != "unit" {
			t.Errorf("key %q covers %q in the gate but selected %v here", tc.key, tc.path, got)
		}
	}

	// A key that covers nothing in the gate selects nothing here either.
	got := tiersForChangedPathIn(
		map[string]map[string][]string{"pkg/other/...": {"unit": {"pkg/..."}}},
		"pkg/foo/bar.go",
	)
	if len(got) != 0 {
		t.Errorf("an unrelated key selected tiers: %v", got)
	}
}
