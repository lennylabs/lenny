// SPDX-License-Identifier: MIT

package releasechannel_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/releasechannel"
)

// spec: §25.8 line 3410 — the minUpgradeFrom prerequisite filter.
func TestMeetsMinUpgradeFrom(t *testing.T) {
	cases := []struct {
		name           string
		minUpgradeFrom string
		current        string
		want           bool
	}{
		{"below floor", "1.3.0", "1.2.9", false},
		{"at floor", "1.3.0", "1.3.0", true},
		{"above floor", "1.3.0", "1.4.3", true},
		{"major above", "1.3.0", "2.0.0", true},
		{"empty current is opt-in pass", "1.3.0", "", true},
		{"no floor passes any", "", "0.0.1", true},
		{"v-prefix tolerated", "1.3.0", "v1.4.0", true},
		{"prerelease suffix stripped", "1.3.0", "1.3.0-rc.1", true},
		{"patch below floor", "1.3.5", "1.3.4", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := releasechannel.Manifest{Version: "1.5.0", MinUpgradeFrom: tc.minUpgradeFrom}
			if got := m.MeetsMinUpgradeFrom(tc.current); got != tc.want {
				t.Errorf("MeetsMinUpgradeFrom(%q) with floor %q = %v, want %v",
					tc.current, tc.minUpgradeFrom, got, tc.want)
			}
		})
	}
}
