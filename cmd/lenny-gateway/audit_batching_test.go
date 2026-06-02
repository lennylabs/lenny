// SPDX-License-Identifier: MIT

package main

import "testing"

// spec: §12.3 line 99 — AuditBatchingNoSIEM fires only when production
// mode has T2 audit batching enabled and no SIEM endpoint configured.
// Non-production, batching-disabled, or SIEM-configured deployments do
// not warn. F-12.3.15.
func TestAuditBatchingNoSIEM_spec_12_3_99(t *testing.T) {
	cases := []struct {
		name            string
		env             string
		batchingEnabled bool
		siemConfigured  bool
		want            bool
	}{
		{"production + batching + no siem warns", "production", true, false, true},
		{"production + batching + siem configured", "production", true, true, false},
		{"production + no batching", "production", false, false, false},
		{"non-production + batching + no siem", "staging", true, false, false},
		{"empty env + batching + no siem", "", true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := auditBatchingNoSIEM(tc.env, tc.batchingEnabled, tc.siemConfigured); got != tc.want {
				t.Errorf("auditBatchingNoSIEM(%q, %v, %v) = %v, want %v",
					tc.env, tc.batchingEnabled, tc.siemConfigured, got, tc.want)
			}
		})
	}
}
