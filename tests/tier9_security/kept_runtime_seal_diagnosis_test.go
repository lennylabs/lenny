// SPDX-License-Identifier: MIT

//go:build security

package tier9_security_test

import (
	"strings"
	"testing"
)

// spec: 7.1 (Seal-and-export invariant), 5.2 (Recycle lifecycle)
func TestKeptRuntimeSealDiagnosisNamesOnlyAFailedSeal(t *testing.T) {
	cases := []struct {
		name, state, failureClass string
		want                      bool
	}{
		{"failed seal", "failed", "workspace_seal_timeout", true},
		{"clean terminal", "completed", "", false},
		{"cancelled", "cancelled", "", false},
		{"runtime failure", "failed", "runtime_failure", false},
		{"seal class on a non-failed row", "completed", "workspace_seal_timeout", false},
		{"empty row", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := keptRuntimeSealDiagnosis(tc.state, tc.failureClass)
			if (got != "") != tc.want {
				t.Fatalf("keptRuntimeSealDiagnosis(%q, %q) = %q, want message %v", tc.state, tc.failureClass, got, tc.want)
			}
			if tc.want && !strings.Contains(got, "workspace_seal_timeout") {
				t.Errorf("message %q does not name workspace_seal_timeout", got)
			}
		})
	}
}
