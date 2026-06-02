// SPDX-License-Identifier: MIT

package failopen

import (
	"strings"
	"testing"
)

// spec: §12.4 line 222 — 0 < quotaUserFailOpenFraction <= 1.0; out-of-range
// values are rejected at startup with the CONFIG_INVALID message.
func TestValidateUserFraction_spec_12_4(t *testing.T) {
	cases := []struct {
		fraction float64
		valid    bool
	}{
		{0.25, true},
		{1.0, true},
		{0.5, true},
		{0, false},
		{-0.1, false},
		{1.0001, false},
		{2, false},
	}
	for _, tc := range cases {
		err := ValidateUserFraction(tc.fraction)
		if tc.valid && err != nil {
			t.Errorf("fraction %v: unexpected error %v", tc.fraction, err)
		}
		if !tc.valid {
			if err == nil {
				t.Errorf("fraction %v: expected a CONFIG_INVALID error", tc.fraction)
				continue
			}
			if !strings.Contains(err.Error(), "CONFIG_INVALID: quotaUserFailOpenFraction") {
				t.Errorf("fraction %v: error %q lacks the spec CONFIG_INVALID message", tc.fraction, err)
			}
		}
	}
}

// spec: §12.4 line 222 — a fraction >= 0.5 is the weakened-posture warning
// threshold.
func TestUserFractionWeakened_spec_12_4(t *testing.T) {
	if UserFractionWeakened(0.25) {
		t.Error("0.25 must not be flagged weakened")
	}
	if !UserFractionWeakened(0.5) {
		t.Error("0.5 must be flagged weakened (at the threshold)")
	}
	if !UserFractionWeakened(0.75) {
		t.Error("0.75 must be flagged weakened")
	}
}
