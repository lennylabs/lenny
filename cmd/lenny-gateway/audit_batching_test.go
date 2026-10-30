// SPDX-License-Identifier: MIT

package main

import "testing"

// spec: §17.9.1 (Composition Dimensions) "Production mode"; §12.3 (Postgres HA Requirements)
//
// TestAuditBatchingNoSIEM_spec_12_3_99 pins the AuditBatchingNoSIEM
// condition over every combination of its inputs: it holds only in
// production mode with T2 audit batching enabled and no SIEM endpoint
// configured. F-12.3.15.
func TestAuditBatchingNoSIEM_spec_12_3_99(t *testing.T) {
	for _, tc := range []struct {
		production, batchingEnabled, siemConfigured, want bool
	}{
		{true, true, false, true},
		{true, true, true, false},
		{true, false, false, false},
		{true, false, true, false},
		{false, true, false, false},
		{false, true, true, false},
		{false, false, false, false},
		{false, false, true, false},
	} {
		if got := auditBatchingNoSIEM(tc.production, tc.batchingEnabled, tc.siemConfigured); got != tc.want {
			t.Errorf("auditBatchingNoSIEM(%v, %v, %v) = %v, want %v",
				tc.production, tc.batchingEnabled, tc.siemConfigured, got, tc.want)
		}
	}
}
