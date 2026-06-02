// SPDX-License-Identifier: MIT

package failopen

import (
	"testing"
	"time"
)

// spec: §12.4 line 220 — the in-memory backstop counts per the §11.1
// one-minute window and resets at the minute boundary.
func TestBackstopPerMinuteWindowResets_spec_12_4(t *testing.T) {
	base := time.Date(2026, 6, 2, 12, 0, 30, 0, time.UTC)
	b := NewBackstop(nil)
	if n := b.Incr("u:alice", base); n != 1 {
		t.Fatalf("first incr = %d, want 1", n)
	}
	if n := b.Incr("u:alice", base.Add(10*time.Second)); n != 2 {
		t.Fatalf("second incr same window = %d, want 2", n)
	}
	// Next minute: the counter resets.
	if n := b.Incr("u:alice", base.Add(40*time.Second)); n != 1 {
		t.Fatalf("incr after the minute boundary = %d, want 1 (reset)", n)
	}
}

// Reset clears every counter (Redis-recovery edge).
func TestBackstopReset_spec_12_4(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	b := NewBackstop(nil)
	b.Incr("u:alice", now)
	b.Incr("t:acme", now)
	b.Reset()
	if n := b.Incr("u:alice", now); n != 1 {
		t.Fatalf("after Reset incr = %d, want 1", n)
	}
}

// Sweep drops elapsed-window counters.
func TestBackstopSweep(t *testing.T) {
	now := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	b := NewBackstop(nil)
	b.Incr("u:alice", now)
	b.Sweep(now.Add(2 * time.Minute))
	// alice's window elapsed and was swept, so the next incr starts at 1.
	if n := b.Incr("u:alice", now.Add(2*time.Minute)); n != 1 {
		t.Fatalf("post-sweep incr = %d, want 1", n)
	}
}

// spec: §12.4 lines 222-224 — the ceiling formula divides tenant_limit by
// the replica count and caps at per_replica_hard_cap; the per-user ceiling
// is a pure fraction of the effective tenant ceiling.
func TestComputeCeilings_spec_12_4(t *testing.T) {
	cases := []struct {
		name           string
		tenantLimit    int64
		replicas       int
		hardCap        int64
		userFraction   float64
		wantTenantCeil int64
		wantUserCeil   int64
	}{
		{
			name: "slice below hard cap", tenantLimit: 1000, replicas: 4, hardCap: 600, userFraction: 0.25,
			wantTenantCeil: 250, wantUserCeil: 62,
		},
		{
			name: "hard cap binds", tenantLimit: 1000, replicas: 1, hardCap: 600, userFraction: 0.25,
			wantTenantCeil: 600, wantUserCeil: 150,
		},
		{
			name: "hard cap defaults to tenant_limit/2", tenantLimit: 1000, replicas: 1, hardCap: 0, userFraction: 0.25,
			wantTenantCeil: 500, wantUserCeil: 125,
		},
		{
			name: "cold-start replica count floors at 1", tenantLimit: 800, replicas: 0, hardCap: 800, userFraction: 0.5,
			wantTenantCeil: 800, wantUserCeil: 400,
		},
		{
			name: "user fraction defaults to 0.25", tenantLimit: 400, replicas: 2, hardCap: 400, userFraction: 0,
			wantTenantCeil: 200, wantUserCeil: 50,
		},
		{
			name: "zero tenant limit yields no ceiling", tenantLimit: 0, replicas: 2, hardCap: 100, userFraction: 0.25,
			wantTenantCeil: 0, wantUserCeil: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeCeilings(tc.tenantLimit, tc.replicas, tc.hardCap, tc.userFraction)
			if got.Tenant != tc.wantTenantCeil {
				t.Errorf("tenant ceiling = %d, want %d", got.Tenant, tc.wantTenantCeil)
			}
			if got.User != tc.wantUserCeil {
				t.Errorf("user ceiling = %d, want %d", got.User, tc.wantUserCeil)
			}
		})
	}
}
