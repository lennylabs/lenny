// SPDX-License-Identifier: MIT

package operations_test

import (
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// spec §25.2 line 401: percent crossing a named threshold (10, 25, 50,
// 75, 90, 95, 99) raises operation_progressed.
func TestCrossedThresholds(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur float64
		want      []int
	}{
		{"no advance", 50, 50, nil},
		{"backward", 60, 40, nil},
		{"single crossing", 8, 12, []int{10}},
		{"multiple crossings", 5, 55, []int{10, 25, 50}},
		{"first sighting reports all at or below", -1, 30, []int{10, 25}},
		{"lands exactly on threshold", 49, 50, []int{50}},
		{"top band", 94, 100, []int{95, 99}},
		{"between thresholds", 11, 24, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := operations.CrossedThresholds(tc.prev, tc.cur)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CrossedThresholds(%v, %v) = %v, want %v", tc.prev, tc.cur, got, tc.want)
			}
		})
	}
}

// spec §25.2 line 396: the expected cadence is defined for the
// long-running operation kinds and zero for the rest (stall detection
// disabled).
func TestExpectedCadence(t *testing.T) {
	if got := operations.ExpectedCadence(operations.KindPlatformUpgrade); got <= 0 {
		t.Errorf("platform_upgrade cadence = %v, want > 0", got)
	}
	if got := operations.ExpectedCadence(operations.KindRestore); got <= 0 {
		t.Errorf("restore cadence = %v, want > 0", got)
	}
	if got := operations.ExpectedCadence(operations.KindRemediationLock); got != 0 {
		t.Errorf("remediation_lock cadence = %v, want 0 (not a long-running op)", got)
	}
}
