// SPDX-License-Identifier: MIT

package leaseextension_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/leaseextension"
)

func TestResolveEffectiveMax(t *testing.T) {
	// The first five cases are the §8.6 worked example: deployment
	// default 500K, deployment max 2M, tenant max 1M.
	const (
		depDefault = 500_000
		depMax     = 2_000_000
		tenantMax  = 1_000_000
	)
	cases := []struct {
		name        string
		tenantBase  int64
		runtimeBase int64
		want        int64
	}{
		{"runtime overrides tenant, under both ceilings", 300_000, 800_000, 800_000},
		{"runtime overrides default, capped by tenant max", 0, 1_500_000, 1_000_000},
		{"tenant overrides default", 300_000, 0, 300_000},
		{"deployment default applies", 0, 0, 500_000},
		{"runtime capped by tenant max below deployment max", 0, 2_500_000, 1_000_000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := leaseextension.ResolveEffectiveMax(depDefault, depMax, c.tenantBase, tenantMax, c.runtimeBase)
			if got != c.want {
				t.Errorf("ResolveEffectiveMax = %d, want %d", got, c.want)
			}
		})
	}
}

func TestResolveEffectiveMaxWithoutTenantCeiling(t *testing.T) {
	// No tenant ceiling: the runtime base is capped only by the
	// deployment max.
	got := leaseextension.ResolveEffectiveMax(500_000, 2_000_000, 0, 0, 2_500_000)
	if got != 2_000_000 {
		t.Errorf("ResolveEffectiveMax = %d, want 2000000 (capped by deployment max)", got)
	}
}

func TestResolveEffectiveMaxWithoutAnyCeiling(t *testing.T) {
	// A zero deployment max means "unset"; no ceiling caps the value.
	got := leaseextension.ResolveEffectiveMax(500_000, 0, 0, 0, 2_500_000)
	if got != 2_500_000 {
		t.Errorf("ResolveEffectiveMax = %d, want 2500000 (no ceiling set)", got)
	}
}

func TestGrant(t *testing.T) {
	cases := []struct {
		name        string
		current     int64
		requested   int64
		ceiling     int64
		wantGranted int64
		wantOutcome leaseextension.Outcome
	}{
		{"full request fits", 100_000, 50_000, 200_000, 50_000, leaseextension.Granted},
		{"request exactly fills headroom", 150_000, 50_000, 200_000, 50_000, leaseextension.Granted},
		{"request capped to headroom", 150_000, 100_000, 200_000, 50_000, leaseextension.PartiallyGranted},
		{"ceiling already reached", 200_000, 50_000, 200_000, 0, leaseextension.CeilingReached},
		{"current already over ceiling", 250_000, 10_000, 200_000, 0, leaseextension.CeilingReached},
		{"zero request grants nothing", 100_000, 0, 200_000, 0, leaseextension.Granted},
		{"negative request grants nothing", 100_000, -10, 200_000, 0, leaseextension.Granted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			granted, outcome := leaseextension.Grant(c.current, c.requested, c.ceiling)
			if granted != c.wantGranted {
				t.Errorf("granted = %d, want %d", granted, c.wantGranted)
			}
			if outcome != c.wantOutcome {
				t.Errorf("outcome = %v, want %v", outcome, c.wantOutcome)
			}
		})
	}
}

func TestOutcomeString(t *testing.T) {
	cases := map[leaseextension.Outcome]string{
		leaseextension.Granted:          "GRANTED",
		leaseextension.PartiallyGranted: "PARTIALLY_GRANTED",
		leaseextension.CeilingReached:   "CEILING_REACHED",
	}
	for outcome, want := range cases {
		if got := outcome.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", outcome, got, want)
		}
	}
}
