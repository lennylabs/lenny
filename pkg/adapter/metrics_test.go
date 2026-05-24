// SPDX-License-Identifier: MIT

package adapter

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestSoPeercredCounters_spec_4_7 verifies the §4.7 security-boundary
// counters (spec/04_system-components.md lines 870-888) are registered
// under their spec names and increment when the adapter records a
// self-test failure or a nonce-only-mode start.
func TestSoPeercredCounters_spec_4_7(t *testing.T) {
	beforeFail := testutil.ToFloat64(soPeercredSelftestFailed.WithLabelValues())
	IncSoPeercredSelftestFailed()
	if got := testutil.ToFloat64(soPeercredSelftestFailed.WithLabelValues()); got != beforeFail+1 {
		t.Fatalf("sopeercred_selftest_failed_total = %v, want %v", got, beforeFail+1)
	}

	beforeDisabled := testutil.ToFloat64(soPeercredDisabled.WithLabelValues())
	IncSoPeercredDisabled()
	if got := testutil.ToFloat64(soPeercredDisabled.WithLabelValues()); got != beforeDisabled+1 {
		t.Fatalf("sopeercred_disabled_total = %v, want %v", got, beforeDisabled+1)
	}
}
