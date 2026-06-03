// SPDX-License-Identifier: MIT

package coordination_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// resetTTLBounds restores the built-in §25.4 lock TTL policy after a test
// that mutated the process-wide bounds, so test order does not matter.
func resetTTLBounds(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { coordination.SetTTLBounds(0, 0, 0) })
}

// spec: §25.4 ops.locks.{minTTLSeconds,defaultTTLSeconds,maxTTLSeconds} —
// F-25.4.9. A zero flag reproduces the built-in 0/300/3600 policy.
func TestTTLBoundsBuiltInDefaults_spec_25_4(t *testing.T) {
	resetTTLBounds(t)
	coordination.SetTTLBounds(0, 0, 0)
	min, def, max := coordination.TTLBounds()
	if min != 0 || def != 300 || max != 3600 {
		t.Fatalf("built-in bounds = (%d,%d,%d), want (0,300,3600)", min, def, max)
	}
	if got := coordination.NormalizeTTL(0); got != 300 {
		t.Errorf("omitted TTL = %d, want default 300", got)
	}
	if got := coordination.NormalizeTTL(10000); got != 3600 {
		t.Errorf("over-ceiling TTL = %d, want clamped 3600", got)
	}
}

// spec: §25.4 ops.locks.minTTLSeconds — F-25.4.9. A request below the
// floor is raised to the floor; the default is also clamped up to the
// floor when it would otherwise sit below it.
func TestTTLBoundsFloor_spec_25_4(t *testing.T) {
	resetTTLBounds(t)
	coordination.SetTTLBounds(120, 0, 0) // floor 120; default 300 stays in window
	if got := coordination.NormalizeTTL(30); got != 120 {
		t.Errorf("below-floor TTL = %d, want raised to 120", got)
	}
	if got := coordination.NormalizeTTL(0); got != 300 {
		t.Errorf("omitted TTL = %d, want default 300", got)
	}
}

// spec: §25.4 ops.locks.{defaultTTLSeconds,maxTTLSeconds} — F-25.4.9. The
// configured ceiling raises the effective clamp above the built-in 3600.
func TestTTLBoundsCeilingOverride_spec_25_4(t *testing.T) {
	resetTTLBounds(t)
	coordination.SetTTLBounds(0, 600, 7200)
	if got := coordination.NormalizeTTL(0); got != 600 {
		t.Errorf("omitted TTL = %d, want configured default 600", got)
	}
	if got := coordination.NormalizeTTL(7000); got != 7000 {
		t.Errorf("in-window TTL = %d, want unchanged (ceiling raised to 7200)", got)
	}
	if got := coordination.NormalizeTTL(9000); got != 7200 {
		t.Errorf("over-ceiling TTL = %d, want clamped 7200", got)
	}
}

// spec: §25.4 ops.locks — F-25.4.9. An inconsistent operator policy
// (default outside [min,max], or max below min) is repaired so the bounds
// always satisfy min <= default <= max.
func TestTTLBoundsSelfConsistent_spec_25_4(t *testing.T) {
	resetTTLBounds(t)
	// max below min: max is raised to the floor.
	coordination.SetTTLBounds(500, 0, 100)
	min, def, max := coordination.TTLBounds()
	if min != 500 || max < min || def < min || def > max {
		t.Fatalf("repaired bounds = (%d,%d,%d), want min<=def<=max with min=500", min, def, max)
	}
	// default above max: default is pulled down to the ceiling.
	resetTTLBounds(t)
	coordination.SetTTLBounds(0, 9000, 1000)
	_, def2, max2 := coordination.TTLBounds()
	if def2 != max2 {
		t.Errorf("default %d clamped to max %d expected equal", def2, max2)
	}
}

// spec: §25.4 ops.locks — F-25.4.9. The configured policy reaches the
// in-memory tier store through the shared normalizer, so a lock acquired
// without an explicit ttlSeconds expires at the configured default.
func TestConfiguredDefaultAppliesToMemStore_spec_25_4(t *testing.T) {
	resetTTLBounds(t)
	coordination.SetTTLBounds(0, 90, 0)
	s := coordination.NewMemStore()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	l, err := s.Acquire(context.Background(), coordination.LockRequest{
		Scope: "pool:default-gvisor", Operation: "scale", AcquiredBy: "alice",
		// TTLSeconds omitted → configured default.
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if got := l.ExpiresAt.Sub(now); got != 90*time.Second {
		t.Errorf("TTL = %v, want configured default 90s", got)
	}
}
