// SPDX-License-Identifier: MIT

package jwt

import (
	"strings"
	"testing"
	"time"
)

// spec: 10.2 (Authentication)
//
// TestValidateCapabilityTokenTTLBound_spec_10_2 pins the
// auth.capabilityTokenTtlSeconds bound 120 ≤ ttl ≤ 3600 at both edges, and
// the error text names the value and the bound.
func TestValidateCapabilityTokenTTLBound_spec_10_2(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		ok      bool
	}{
		{0, false},
		{60, false},
		{119, false},
		{120, true},
		{900, true},
		{3600, true},
		{3601, false},
	} {
		err := ValidateCapabilityTokenTTL(time.Duration(tc.seconds) * time.Second)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateCapabilityTokenTTL(%ds) = %v, want ok=%v", tc.seconds, err, tc.ok)
			continue
		}
		if err != nil {
			msg := err.Error()
			if !strings.Contains(msg, "auth.capabilityTokenTtlSeconds") || !strings.Contains(msg, "120s..3600s") {
				t.Errorf("error %q does not name auth.capabilityTokenTtlSeconds and the 120s..3600s bound", msg)
			}
		}
	}
	if err := ValidateCapabilityTokenTTL(DefaultCapabilityTokenTTL); err != nil {
		t.Errorf("default %s is outside the bound: %v", DefaultCapabilityTokenTTL, err)
	}
}
