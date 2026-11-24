// SPDX-License-Identifier: MIT

package main

import (
	"testing"
	"time"
)

// TestLineageFlagsAreBoundedAndReachTheStore pins the startup gate on the
// lineage depth cap and the revocation timeout, which refuses a value
// below 1, and the store bounds the flags set.
//
// spec: §13.3 (Credential Flow)
func TestLineageFlagsAreBoundedAndReachTheStore(t *testing.T) {
	for _, tc := range []struct {
		depth, ms int
		ok        bool
	}{
		{32, 5000, true},
		{1, 1, true},
		{0, 5000, false},
		{32, 0, false},
	} {
		if err := validateLineageFlags(tc.depth, tc.ms); (err == nil) != tc.ok {
			t.Errorf("validateLineageFlags(%d, %d) = %v, want ok=%v", tc.depth, tc.ms, err, tc.ok)
		}
	}
	depth, ms := 7, 250
	f := &tokenServiceFlags{lineageMaxDepth: &depth, revocationTimeoutMs: &ms}
	if l := f.lineageLimits(); l.LineageMaxDepth != 7 || l.RevocationTimeout != 250*time.Millisecond {
		t.Errorf("lineageLimits() = %+v, want 7 and 250ms", l)
	}
}
