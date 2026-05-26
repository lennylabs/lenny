// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/egress"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func TestCreateAndGet(t *testing.T) {
	s := poolstore.NewMemory()
	p := poolstore.Pool{
		Name:                 "default-pool",
		RuntimeRef:           "echo",
		IsolationProfile:     isolation.ProfileSandboxed,
		ExecutionMode:        runtimestore.ExecutionModeSession,
		ResourceClass:        "small",
		WarmCount:            3,
		MaxSessionAgeSeconds: 3600,
	}
	if err := s.Create(context.Background(), p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(context.Background(), "default-pool")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.RuntimeRef != "echo" || got.WarmCount != 3 {
		t.Errorf("Get: %+v", got)
	}
}

func TestCreateRejectsStandardWithoutAllow(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:             "runc-pool",
		IsolationProfile: isolation.ProfileStandard,
	})
	if err == nil {
		t.Error("standard isolation without allowStandardIsolation should fail")
	}
}

func TestCreateAdmitsStandardWithAllow(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "runc-pool",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
	})
	if err != nil {
		t.Errorf("standard isolation with allowStandardIsolation should succeed: %v", err)
	}
}

// TestCreateRejectsInternetEgressOnStandard covers the §13.2
// cross-control: a runc pool cannot use the `internet` egress profile.
func TestCreateRejectsInternetEgressOnStandard(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "runc-internet",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
		EgressProfile:          egress.ProfileInternet,
	})
	if err == nil {
		t.Fatal("internet egress on standard isolation should be rejected (§13.2)")
	}
}

func TestCreateAdmitsEgressIsolationCombinations(t *testing.T) {
	cases := []struct {
		name  string
		iso   isolation.Profile
		eg    egress.Profile
		allow bool
	}{
		{"internet+sandboxed", isolation.ProfileSandboxed, egress.ProfileInternet, false},
		{"internet+microvm", isolation.ProfileMicrovm, egress.ProfileInternet, false},
		{"provider-direct+standard", isolation.ProfileStandard, egress.ProfileProviderDirect, true},
		{"restricted+standard", isolation.ProfileStandard, egress.ProfileRestricted, true},
		{"empty-egress+standard", isolation.ProfileStandard, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := poolstore.NewMemory()
			err := s.Create(context.Background(), poolstore.Pool{
				Name:                   "pool",
				IsolationProfile:       tc.iso,
				AllowStandardIsolation: tc.allow,
				EgressProfile:          tc.eg,
			})
			if err != nil {
				t.Errorf("Create(%s) = %v, want success", tc.name, err)
			}
		})
	}
}

// TestCreateRejectsUnknownEgressProfile fails closed on a mistyped
// profile rather than silently ignoring it.
func TestCreateRejectsUnknownEgressProfile(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name:             "bad-egress",
		IsolationProfile: isolation.ProfileSandboxed,
		EgressProfile:    egress.Profile("open"),
	})
	if err == nil {
		t.Fatal("unrecognised egress profile should be rejected")
	}
}

// TestUpdateRejectsInternetEgressOnStandard guards the §13.2
// cross-control on the mutate path, not just create.
func TestUpdateRejectsInternetEgressOnStandard(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name:                   "runc-pool",
		IsolationProfile:       isolation.ProfileStandard,
		AllowStandardIsolation: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.Update(context.Background(), "runc-pool", func(p *poolstore.Pool) error {
		p.EgressProfile = egress.ProfileInternet
		return nil
	})
	if err == nil {
		t.Fatal("updating a runc pool to internet egress should be rejected (§13.2)")
	}
}

func TestCreateRejectsNegativeCounts(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{Name: "a", WarmCount: -1}); err == nil {
		t.Error("WarmCount=-1 should fail")
	}
	if err := s.Create(context.Background(), poolstore.Pool{Name: "b", MaxSessionAgeSeconds: -1}); err == nil {
		t.Error("MaxSessionAgeSeconds=-1 should fail")
	}
}

func TestUpdateAdvancesTimestamp(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	row, _ := s.Get(context.Background(), "p")
	prev := row.UpdatedAt
	updated, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.WarmCount = 5
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.WarmCount != 5 || !updated.UpdatedAt.After(prev) {
		t.Errorf("Update: %+v", updated)
	}
}

func TestUpdateRejectsBadValues(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	_, err := s.Update(context.Background(), "p", func(p *poolstore.Pool) error {
		p.WarmCount = -2
		return nil
	})
	if err == nil {
		t.Error("Update with bad WarmCount should fail")
	}
}

func TestListFilterByRuntime(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "a", RuntimeRef: "echo", IsolationProfile: isolation.ProfileSandboxed})
	_ = s.Create(context.Background(), poolstore.Pool{Name: "b", RuntimeRef: "claude", IsolationProfile: isolation.ProfileSandboxed})
	rows, _ := s.List(context.Background(), poolstore.ListFilter{RuntimeRef: "echo"})
	if len(rows) != 1 || rows[0].Name != "a" {
		t.Errorf("List: %+v", rows)
	}
}

func TestSoftDeleteExcludesByDefault(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	_ = s.SoftDelete(context.Background(), "p", time.Now())
	rows, _ := s.List(context.Background(), poolstore.ListFilter{})
	if len(rows) != 0 {
		t.Errorf("default list should exclude deleted: %+v", rows)
	}
	all, _ := s.List(context.Background(), poolstore.ListFilter{IncludeDeleted: true})
	if len(all) != 1 {
		t.Errorf("includeDeleted list: %d", len(all))
	}
}

func TestSoftDeleteIdempotent(t *testing.T) {
	s := poolstore.NewMemory()
	_ = s.Create(context.Background(), poolstore.Pool{Name: "p", IsolationProfile: isolation.ProfileSandboxed})
	first := time.Now()
	if err := s.SoftDelete(context.Background(), "p", first); err != nil {
		t.Fatalf("SoftDelete 1: %v", err)
	}
	if err := s.SoftDelete(context.Background(), "p", first.Add(time.Hour)); err != nil {
		t.Errorf("SoftDelete 2: %v", err)
	}
	row, _ := s.Get(context.Background(), "p")
	if !row.DeletedAt.Equal(first) {
		t.Errorf("DeletedAt overwritten: got %v want %v", row.DeletedAt, first)
	}
}

func TestGetMissing(t *testing.T) {
	s := poolstore.NewMemory()
	if _, err := s.Get(context.Background(), "missing"); !errors.Is(err, poolstore.ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

func TestValidateName(t *testing.T) {
	for _, n := range []string{"a", "default-pool", "p_1"} {
		if err := poolstore.ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q): %v", n, err)
		}
	}
	for _, n := range []string{"", "With-Caps", "-leading"} {
		if err := poolstore.ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) should fail", n)
		}
	}
}

// spec: 5.2
// TestValidateConcurrentConfig covers the §5.2 / §13.1 admission rules
// for a pool's concurrent-mode configuration: the deployer
// acknowledgment, the slot bound, the categorical cross-tenant-reuse
// rejection, the per-slot cleanup floor, and the rule that
// concurrent-only fields are valid only on a concurrent-mode pool.
func TestValidateConcurrentConfig(t *testing.T) {
	cases := []struct {
		name string
		pool poolstore.Pool
		ok   bool
	}{
		{
			name: "session-mode pool with no concurrent fields is valid",
			pool: poolstore.Pool{Name: "p", ExecutionMode: runtimestore.ExecutionModeSession},
			ok:   true,
		},
		{
			name: "session-mode pool with concurrencyStyle is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeSession,
				ConcurrencyStyle: poolstore.ConcurrencyStyleWorkspace,
			},
		},
		{
			name: "session-mode pool with maxConcurrent is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeSession, MaxConcurrent: 4,
			},
		},
		{
			name: "concurrent pool without a style is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent, MaxConcurrent: 4,
			},
		},
		{
			name: "concurrent pool with maxConcurrent below 1 is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent,
				ConcurrencyStyle: poolstore.ConcurrencyStyleStateless,
			},
		},
		{
			name: "concurrent-workspace pool without acknowledgment is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent,
				ConcurrencyStyle: poolstore.ConcurrencyStyleWorkspace, MaxConcurrent: 8,
			},
		},
		{
			name: "concurrent-workspace pool with the acknowledgment is valid",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent,
				ConcurrencyStyle: poolstore.ConcurrencyStyleWorkspace, MaxConcurrent: 8,
				AcknowledgeProcessLevelIsolation: true,
			},
			ok: true,
		},
		{
			name: "concurrent-stateless pool needs no acknowledgment",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent,
				ConcurrencyStyle: poolstore.ConcurrencyStyleStateless, MaxConcurrent: 4,
			},
			ok: true,
		},
		{
			name: "concurrent pool with allowCrossTenantReuse is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent,
				ConcurrencyStyle: poolstore.ConcurrencyStyleStateless, MaxConcurrent: 4,
				AllowCrossTenantReuse: true,
			},
		},
		{
			name: "concurrent-workspace cleanupTimeoutSeconds below maxConcurrent*5 is rejected",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent,
				ConcurrencyStyle: poolstore.ConcurrencyStyleWorkspace, MaxConcurrent: 8,
				AcknowledgeProcessLevelIsolation: true, CleanupTimeoutSeconds: 30,
			},
		},
		{
			name: "concurrent-workspace cleanupTimeoutSeconds at maxConcurrent*5 is valid",
			pool: poolstore.Pool{
				Name: "p", ExecutionMode: runtimestore.ExecutionModeConcurrent,
				ConcurrencyStyle: poolstore.ConcurrencyStyleWorkspace, MaxConcurrent: 8,
				AcknowledgeProcessLevelIsolation: true, CleanupTimeoutSeconds: 40,
			},
			ok: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := poolstore.ValidateConcurrentConfig(c.pool)
			if c.ok && err != nil {
				t.Errorf("ValidateConcurrentConfig: unexpected error %v", err)
			}
			if !c.ok && err == nil {
				t.Error("ValidateConcurrentConfig: expected a rejection, got nil")
			}
		})
	}
}

// spec: 5.2
// diagnosis: the in-memory poolstore did not run ValidateConcurrentConfig
// on Create — a concurrent-workspace pool without the deployer
// acknowledgment must be rejected at Create time.
func TestCreateRejectsConcurrentWorkspaceWithoutAcknowledgment(t *testing.T) {
	s := poolstore.NewMemory()
	err := s.Create(context.Background(), poolstore.Pool{
		Name: "cw-pool", ExecutionMode: runtimestore.ExecutionModeConcurrent,
		ConcurrencyStyle: poolstore.ConcurrencyStyleWorkspace, MaxConcurrent: 8,
	})
	if err == nil {
		t.Error("concurrent-workspace pool without acknowledgeProcessLevelIsolation should fail")
	}
}

// spec: 5.2
// diagnosis: the in-memory poolstore admitted a concurrent-mode pool
// configuration. A valid concurrent-stateless pool must round-trip
// through Create and Get with its concurrency fields intact.
func TestCreateAdmitsValidConcurrentPool(t *testing.T) {
	s := poolstore.NewMemory()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name: "cs-pool", ExecutionMode: runtimestore.ExecutionModeConcurrent,
		ConcurrencyStyle: poolstore.ConcurrencyStyleStateless, MaxConcurrent: 6,
	}); err != nil {
		t.Fatalf("Create valid concurrent pool: %v", err)
	}
	got, err := s.Get(context.Background(), "cs-pool")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConcurrencyStyle != poolstore.ConcurrencyStyleStateless || got.MaxConcurrent != 6 {
		t.Errorf("concurrent fields did not round-trip: %+v", got)
	}
}

// spec: §5.2 line 396 — the pool controller rejects allowCrossTenantReuse:
// true on any pool whose associated Runtime is workspaceTier T4. The check
// is mode-agnostic (it keys on the pool's reuse flag and the runtime's
// tier), a no-op when reuse is off or the runtime is not T4, and emits the
// verbatim spec error otherwise.
func TestValidateCrossTenantReuseTier_spec_5_2_396(t *testing.T) {
	cases := []struct {
		name string
		pool poolstore.Pool
		tier runtimestore.WorkspaceTier
		ok   bool
	}{
		{
			name: "T4 runtime with cross-tenant reuse is rejected",
			pool: poolstore.Pool{Name: "p", AllowCrossTenantReuse: true},
			tier: runtimestore.WorkspaceTierT4,
		},
		{
			name: "T4 runtime without cross-tenant reuse is allowed",
			pool: poolstore.Pool{Name: "p", AllowCrossTenantReuse: false},
			tier: runtimestore.WorkspaceTierT4,
			ok:   true,
		},
		{
			name: "T3 runtime with cross-tenant reuse is allowed",
			pool: poolstore.Pool{Name: "p", AllowCrossTenantReuse: true},
			tier: runtimestore.WorkspaceTierT3,
			ok:   true,
		},
		{
			name: "empty tier (implicit T3) with cross-tenant reuse is allowed",
			pool: poolstore.Pool{Name: "p", AllowCrossTenantReuse: true},
			tier: "",
			ok:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := poolstore.ValidateCrossTenantReuseTier(tc.pool, tc.tier)
			if tc.ok && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("want rejection, got nil")
				}
				// The error string is verbatim from §5.2 line 396.
				want := "allowCrossTenantReuse: true is not permitted for T4-tier pools " +
					"(workspaceTier: T4); T4 workloads require dedicated node pools (Section 6.4)"
				if err.Error() != want {
					t.Errorf("error string drifted from spec:\n got:  %q\n want: %q", err.Error(), want)
				}
			}
		})
	}
}
