// SPDX-License-Identifier: MIT

package quota

import "testing"

// spec: §12.4 line 268 — the closed quotaEnforcementMode enum.
func TestParseEnforcementMode_spec_12_4_268(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    EnforcementMode
		wantErr bool
	}{
		{"empty selects default redis", "", EnforcementModeRedis, false},
		{"redis", "redis", EnforcementModeRedis, false},
		{"in_memory_reconciled", "in_memory_reconciled", EnforcementModeInMemoryReconciled, false},
		{"unknown rejected", "in-memory", "", true},
		{"case-sensitive rejected", "REDIS", "", true},
		{"typo rejected", "in_memory", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseEnforcementMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseEnforcementMode(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseEnforcementMode(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseEnforcementMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEnforcementModeIsValid(t *testing.T) {
	for _, m := range AllEnforcementModes() {
		if !m.IsValid() {
			t.Errorf("AllEnforcementModes()[%q].IsValid() = false", m)
		}
	}
	if EnforcementMode("nonsense").IsValid() {
		t.Errorf("EnforcementMode(nonsense).IsValid() = true, want false")
	}
	if DefaultEnforcementMode != EnforcementModeRedis {
		t.Errorf("DefaultEnforcementMode = %q, want %q", DefaultEnforcementMode, EnforcementModeRedis)
	}
}

// spec: §12.4 line 268 — the per-replica budget slice is 1/N of the
// tenant's remaining budget.
func TestDrawBudgetSlice_spec_12_4_268(t *testing.T) {
	cases := []struct {
		name      string
		remaining int64
		replicas  int
		want      int64
	}{
		{"single replica takes whole remaining", 1000, 1, 1000},
		{"four replicas split evenly", 1000, 4, 250},
		{"three replicas floor-divide", 1000, 3, 333},
		{"cold-start zero replicas floors at 1", 1000, 0, 1000},
		{"negative replicas floors at 1", 1000, -5, 1000},
		{"exhausted budget yields zero slice", 0, 4, 0},
		{"negative remaining yields zero slice", -10, 4, 0},
		{"tiny remaining rounds to zero per replica", 3, 8, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DrawBudgetSlice(tc.remaining, tc.replicas); got != tc.want {
				t.Fatalf("DrawBudgetSlice(%d, %d) = %d, want %d", tc.remaining, tc.replicas, got, tc.want)
			}
		})
	}
}

func TestBudgetSliceReconcileRatio(t *testing.T) {
	// spec: §12.4 line 268 — reconcile when the local slice is 80% consumed.
	if BudgetSliceReconcileRatio != 0.80 {
		t.Fatalf("BudgetSliceReconcileRatio = %v, want 0.80", BudgetSliceReconcileRatio)
	}
}
