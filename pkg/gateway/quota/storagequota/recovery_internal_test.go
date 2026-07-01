// SPDX-License-Identifier: MIT

package storagequota

import (
	"context"
	"errors"
	"testing"
)

// spec: §12.4 line 210 — on a Redis down-to-up edge the reconciler writes
// each tenant's storage_bytes_used back to Redis from the Postgres sum.
func TestRecoveryReconcilerRehydratesOnRecoveryEdge_spec_12_4_210(t *testing.T) {
	mem := NewMemory()
	sums := map[string]int64{"acme": 1000, "globex": 250}
	rehydrated := 0
	r := &RecoveryReconciler{
		Probe:       func(context.Context) bool { return true },
		Primary:     mem,
		Tenants:     func(context.Context) ([]string, error) { return []string{"acme", "globex"}, nil },
		SizeOf:      func(_ context.Context, id string) (int64, error) { return sums[id], nil },
		OnRehydrate: func(n int) { rehydrated = n },
	}
	ctx := context.Background()

	// Redis was unreachable on the prior tick; this tick observes it back.
	reachable := r.tick(ctx, false)
	if !reachable {
		t.Fatal("tick should report Redis reachable")
	}
	if used, _ := mem.Used(ctx, "acme"); used != 1000 {
		t.Errorf("acme counter after recovery = %d, want 1000", used)
	}
	if used, _ := mem.Used(ctx, "globex"); used != 250 {
		t.Errorf("globex counter after recovery = %d, want 250", used)
	}
	if rehydrated != 2 {
		t.Errorf("OnRehydrate tenant count = %d, want 2", rehydrated)
	}
}

// No recovery edge means no write-back: a steady-reachable tick, a
// still-down tick, and a going-down tick must all leave the counter
// untouched.
func TestRecoveryReconcilerNoEdgeNoRehydrate_spec_12_4_210(t *testing.T) {
	cases := []struct {
		name         string
		probe        bool
		wasReachable bool
	}{
		{"steady reachable", true, true},
		{"still down", false, false},
		{"going down", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			r := &RecoveryReconciler{
				Probe:   func(context.Context) bool { return tc.probe },
				Primary: NewMemory(),
				Tenants: func(context.Context) ([]string, error) {
					calls++
					return nil, nil
				},
				SizeOf: func(context.Context, string) (int64, error) { return 0, nil },
			}
			got := r.tick(context.Background(), tc.wasReachable)
			if got != tc.probe {
				t.Errorf("tick returned %v, want %v", got, tc.probe)
			}
			if calls != 0 {
				t.Errorf("Tenants was listed %d times with no recovery edge, want 0", calls)
			}
		})
	}
}

// A tenant-list fault aborts that round's rehydration without panicking
// and without invoking the per-tenant write-back.
func TestRecoveryReconcilerTenantListFault(t *testing.T) {
	mem := NewMemory()
	r := &RecoveryReconciler{
		Probe:   func(context.Context) bool { return true },
		Primary: mem,
		Tenants: func(context.Context) ([]string, error) { return nil, errors.New("postgres down") },
		SizeOf: func(context.Context, string) (int64, error) {
			t.Fatal("SizeOf must not be called when the tenant list fails")
			return 0, nil
		},
	}
	if got := r.tick(context.Background(), false); !got {
		t.Fatal("tick should still report reachable")
	}
}

// A reconciler missing a required field is an inert no-op: Run returns
// immediately rather than panicking on the nil probe.
func TestRecoveryReconcilerNilFieldsRunIsNoOp(t *testing.T) {
	(&RecoveryReconciler{}).Run(context.Background())
}
