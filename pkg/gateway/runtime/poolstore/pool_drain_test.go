// SPDX-License-Identifier: MIT

package poolstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// TestPoolPhase verifies the §15.1 line 797 phase derivation: a pool with
// no DrainingSince is active, one with a set DrainingSince is draining.
func TestPoolPhase_spec_15_1_797(t *testing.T) {
	active := poolstore.Pool{Name: "p"}
	if active.Phase() != poolstore.PhaseActive || active.IsDraining() {
		t.Errorf("active pool: phase=%q draining=%v", active.Phase(), active.IsDraining())
	}
	draining := poolstore.Pool{Name: "p", DrainingSince: time.Now()}
	if draining.Phase() != poolstore.PhaseDraining || !draining.IsDraining() {
		t.Errorf("draining pool: phase=%q draining=%v", draining.Phase(), draining.IsDraining())
	}
}

// TestEstimatedDrainSeconds covers the §15.1 line 797 drain-estimate
// formula: the longest active session age, capped at maxSessionAgeSeconds,
// floored at zero, uncapped when the pool has no lifetime bound.
func TestEstimatedDrainSeconds_spec_15_1_797(t *testing.T) {
	cases := []struct {
		name     string
		maxAge   int
		ageInput int
		wantSecs int
	}{
		{"under cap", 3600, 100, 100},
		{"over cap clamps", 30, 100, 30},
		{"no cap returns age", 0, 250, 250},
		{"negative age floors to zero", 3600, -5, 0},
		{"exactly at cap", 100, 100, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := poolstore.EstimatedDrainSeconds(poolstore.Pool{MaxSessionAgeSeconds: tc.maxAge}, tc.ageInput)
			if got != tc.wantSecs {
				t.Errorf("EstimatedDrainSeconds(max=%d, age=%d) = %d, want %d", tc.maxAge, tc.ageInput, got, tc.wantSecs)
			}
		})
	}
}

// TestDrainingSurvivesStoreRoundTrip verifies the Memory store preserves
// DrainingSince across an Update mutation (the drain handler sets it via
// Update). spec: §15.1 line 797.
func TestDrainingSurvivesStoreRoundTrip_spec_15_1_797(t *testing.T) {
	s := poolstore.NewMemory()
	ctx := context.Background()
	if err := s.Create(ctx, poolstore.Pool{Name: "p", RuntimeRef: "echo"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	when := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.Update(ctx, "p", func(p *poolstore.Pool) error {
		p.DrainingSince = when
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.Get(ctx, "p")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.IsDraining() || !got.DrainingSince.Equal(when) {
		t.Errorf("DrainingSince not preserved: %+v", got)
	}
}
