// SPDX-License-Identifier: MIT

package quota

import "testing"

func TestAllResetPeriodsIsExhaustive(t *testing.T) {
	if got := len(AllResetPeriods()); got != 4 {
		t.Errorf("AllResetPeriods() returned %d, want 4 per §11.2", got)
	}
	for _, p := range AllResetPeriods() {
		if !p.IsValid() {
			t.Errorf("AllResetPeriods() returned invalid %q", p)
		}
	}
	if ResetPeriod("yearly").IsValid() {
		t.Errorf("unknown reset period must not be IsValid")
	}
}

func TestCheckOK(t *testing.T) {
	cases := []struct {
		used, limit int64
	}{
		{0, 100},
		{50, 100},
		{79, 100},
		{799, 1000},
	}
	for _, c := range cases {
		if got := Check(c.used, c.limit); got != StateOK {
			t.Errorf("Check(%d, %d) = %q, want ok", c.used, c.limit, got)
		}
	}
}

func TestCheckSoftWarning(t *testing.T) {
	cases := []struct {
		used, limit int64
	}{
		{80, 100},
		{99, 100},
		{800, 1000},
		{999, 1000},
	}
	for _, c := range cases {
		if got := Check(c.used, c.limit); got != StateSoftWarning {
			t.Errorf("Check(%d, %d) = %q, want soft_warning", c.used, c.limit, got)
		}
	}
}

func TestCheckHardExceeded(t *testing.T) {
	cases := []struct {
		used, limit int64
	}{
		{100, 100},
		{1000, 100},
		{1000, 1000},
		{1001, 1000},
	}
	for _, c := range cases {
		if got := Check(c.used, c.limit); got != StateHardExceeded {
			t.Errorf("Check(%d, %d) = %q, want hard_exceeded", c.used, c.limit, got)
		}
	}
}

func TestCheckTreatsZeroLimitAsUnlimited(t *testing.T) {
	if got := Check(1<<40, 0); got != StateOK {
		t.Errorf("zero limit must be unlimited, got %q", got)
	}
	if got := Check(1<<40, -1); got != StateOK {
		t.Errorf("negative limit must be unlimited, got %q", got)
	}
}

func TestCheckRejectsNegativeUsed(t *testing.T) {
	if got := Check(-1, 100); got != StateHardExceeded {
		t.Errorf("negative used must fail closed, got %q", got)
	}
}

func TestHierarchyValidateAcceptsNested(t *testing.T) {
	cases := []Hierarchy{
		{Global: 1000, Tenant: 500, User: 100},
		{Global: 0, Tenant: 500, User: 100}, // unlimited global ok
		{Global: 0, Tenant: 0, User: 0},
		{Global: 1000, Tenant: 1000, User: 1000},
	}
	for _, h := range cases {
		if err := h.Validate(); err != nil {
			t.Errorf("Hierarchy %+v should validate, got %v", h, err)
		}
	}
}

func TestHierarchyValidateRejectsInverted(t *testing.T) {
	cases := []Hierarchy{
		{Global: 100, Tenant: 1000, User: 10},  // tenant > global
		{Global: 1000, Tenant: 100, User: 200}, // user > tenant
	}
	for _, h := range cases {
		if err := h.Validate(); err == nil {
			t.Errorf("inverted Hierarchy %+v should reject", h)
		}
	}
}

func TestHierarchicalCheckPicksMostRestrictive(t *testing.T) {
	h := Hierarchy{Global: 1000, Tenant: 500, User: 100}
	// User at 90% should soft-warn at the user scope, ahead of
	// tenant at 50% and global at 25%.
	r := HierarchicalCheck(250, 250, 90, h)
	if r.State != StateSoftWarning || r.Scope != "user" {
		t.Errorf("user soft-warning expected, got %+v", r)
	}
	// Tenant fully consumed: hard exceed at tenant scope.
	r = HierarchicalCheck(700, 500, 50, h)
	if r.State != StateHardExceeded || r.Scope != "tenant" {
		t.Errorf("tenant hard exceeded expected, got %+v", r)
	}
	// All scopes OK.
	r = HierarchicalCheck(100, 100, 10, h)
	if r.State != StateOK || r.Scope != "" {
		t.Errorf("OK expected, got %+v", r)
	}
}

// User > Tenant > Global ordering: if user, tenant, and global all
// hit hard limit, user wins because it's closer to the caller.
func TestHierarchicalCheckTieBreaksToCloserScope(t *testing.T) {
	h := Hierarchy{Global: 100, Tenant: 100, User: 100}
	r := HierarchicalCheck(100, 100, 100, h)
	if r.State != StateHardExceeded || r.Scope != "user" {
		t.Errorf("tie-break: user scope should win, got %+v", r)
	}
}

func TestFailOpenCeilingFormula(t *testing.T) {
	// §11.2 worked example: tenant_limit=1000, replicas=4 → slice=250
	if got := FailOpenCeiling(1000, 4, 0); got != 250 {
		t.Errorf("FailOpenCeiling(1000, 4, 0) = %d, want 250", got)
	}
	// per_replica_hard_cap binds when smaller than the slice.
	if got := FailOpenCeiling(1000, 4, 100); got != 100 {
		t.Errorf("FailOpenCeiling(1000, 4, 100) = %d, want 100", got)
	}
	// Zero or negative replica count clamps to 1.
	if got := FailOpenCeiling(1000, 0, 0); got != 1000 {
		t.Errorf("FailOpenCeiling(1000, 0, 0) = %d, want 1000", got)
	}
	// Zero tenant limit returns 0 (no admission).
	if got := FailOpenCeiling(0, 4, 0); got != 0 {
		t.Errorf("FailOpenCeiling(0, 4, 0) = %d, want 0", got)
	}
}

func TestPerUserFailOpenCeiling(t *testing.T) {
	// Default userFailOpenFraction = 0.25 against ceiling=400 → 100.
	if got := PerUserFailOpenCeiling(400, 0.25); got != 100 {
		t.Errorf("PerUserFailOpenCeiling(400, 0.25) = %d, want 100", got)
	}
	// Fraction clamped to 1.
	if got := PerUserFailOpenCeiling(400, 1.5); got != 400 {
		t.Errorf("PerUserFailOpenCeiling(400, 1.5) = %d, want 400 (clamped)", got)
	}
	// Zero ceiling or fraction returns 0.
	if got := PerUserFailOpenCeiling(0, 0.25); got != 0 {
		t.Errorf("PerUserFailOpenCeiling(0, 0.25) = %d, want 0", got)
	}
}

func TestMaxOvershootFormula(t *testing.T) {
	// 30s sync × 100 tokens/s × 10 sessions = 30000 tokens.
	if got := MaxOvershoot(30, 100, 10); got != 30000 {
		t.Errorf("MaxOvershoot(30, 100, 10) = %g, want 30000", got)
	}
	// Any zero input collapses to 0.
	if got := MaxOvershoot(0, 100, 10); got != 0 {
		t.Errorf("MaxOvershoot(0, ...) = %g, want 0", got)
	}
}

func TestReconcileMaxImplementsMaxRule(t *testing.T) {
	if got := ReconcileMax(100, 50); got != 100 {
		t.Errorf("ReconcileMax(100, 50) = %d, want 100", got)
	}
	if got := ReconcileMax(50, 100); got != 100 {
		t.Errorf("ReconcileMax(50, 100) = %d, want 100", got)
	}
	if got := ReconcileMax(100, 100); got != 100 {
		t.Errorf("ReconcileMax(100, 100) = %d, want 100", got)
	}
}
