// SPDX-License-Identifier: MIT

package semver_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/core/semver"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.4.0", "1.4.0", 0},
		{"1.3.0", "1.4.0", -1},
		{"1.5.0", "1.4.0", 1},
		{"1.9.0", "1.10.0", -1}, // numeric, not lexical
		{"1.10.0", "1.9.0", 1},
		{"v1.4.0", "1.4.0", 0},    // leading v tolerated
		{"1.4.0-rc1", "1.4.0", 0}, // pre-release suffix dropped
		{"1.4", "1.4.0", 0},       // missing patch defaults to zero
		{"2", "1.9.9", 1},
		{"dev", "1.0.0", -1}, // unparseable sorts below parseable
		{"1.0.0", "dev", 1},
		{"dev", "nightly", 0}, // two unparseable compare equal
		{"", "1.0.0", -1},     // empty sorts below
	}
	for _, tc := range cases {
		if got := semver.Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParse(t *testing.T) {
	if p, ok := semver.Parse("v2.7.3-beta"); !ok || p != [3]int{2, 7, 3} {
		t.Errorf("Parse(v2.7.3-beta) = %v, %v; want [2 7 3], true", p, ok)
	}
	if _, ok := semver.Parse("not-a-version"); ok {
		t.Error("Parse must report ok=false for a non-numeric version")
	}
	if _, ok := semver.Parse(""); ok {
		t.Error("Parse must report ok=false for an empty version")
	}
}
