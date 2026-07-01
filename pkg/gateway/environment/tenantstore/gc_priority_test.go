// SPDX-License-Identifier: MIT

package tenantstore

import "testing"

// spec: §12.5 line 317 — GCPriority is a closed enum (normal/high); the
// empty value is valid and read as normal. F-12.5.18.
func TestValidGCPriority_spec_12_5_317(t *testing.T) {
	for _, s := range []string{"", GCPriorityNormal, GCPriorityHigh} {
		if !ValidGCPriority(s) {
			t.Errorf("ValidGCPriority(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"HIGH", "urgent", "low", "none"} {
		if ValidGCPriority(s) {
			t.Errorf("ValidGCPriority(%q) = true, want false", s)
		}
	}
}

// spec: §12.5 line 317 — only `high` fires the immediate tenant-scoped
// sweep on erasure-job completion; `normal` and the empty default do not.
// F-12.5.18.
func TestTriggersImmediateGC_spec_12_5_317(t *testing.T) {
	cases := map[string]bool{
		"":               false,
		GCPriorityNormal: false,
		GCPriorityHigh:   true,
	}
	for prio, want := range cases {
		if got := (Tenant{GCPriority: prio}).TriggersImmediateGC(); got != want {
			t.Errorf("Tenant{GCPriority:%q}.TriggersImmediateGC() = %v, want %v", prio, got, want)
		}
	}
}
