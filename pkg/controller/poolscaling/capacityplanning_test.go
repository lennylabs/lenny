// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// TestDefaultCapacityPlanningAtDefaults verifies the default workload
// profile reports AtDefaults. spec: §16.5 lines 594-601.
func TestDefaultCapacityPlanningAtDefaults_spec_16_5_601(t *testing.T) {
	if !poolscaling.DefaultCapacityPlanning().AtDefaults() {
		t.Error("DefaultCapacityPlanning().AtDefaults() = false, want true")
	}
	// Pin the spec defaults so a value change is caught here.
	def := poolscaling.DefaultCapacityPlanning()
	want := poolscaling.CapacityPlanning{
		AvgSessionDurationSeconds:          333,
		DelegationParticipationRate:        0.05,
		AvgDelegationsPerDelegatingSession: 10,
		AvgChildSessionSeconds:             60,
		AvgWorkspaceSizeMB:                 100,
		SessionIdleFraction:                0.30,
	}
	if def != want {
		t.Errorf("DefaultCapacityPlanning() = %+v, want %+v", def, want)
	}
}

// TestAtDefaultsFalseWhenAnyFieldOverridden verifies that substituting
// any single workload-profile value clears the at-defaults flag. spec:
// §16.5 line 601.
func TestAtDefaultsFalseWhenAnyFieldOverridden_spec_16_5_601(t *testing.T) {
	mutators := map[string]func(*poolscaling.CapacityPlanning){
		"avgSessionDurationSeconds":          func(c *poolscaling.CapacityPlanning) { c.AvgSessionDurationSeconds = 600 },
		"delegationParticipationRate":        func(c *poolscaling.CapacityPlanning) { c.DelegationParticipationRate = 0.2 },
		"avgDelegationsPerDelegatingSession": func(c *poolscaling.CapacityPlanning) { c.AvgDelegationsPerDelegatingSession = 25 },
		"avgChildSessionSeconds":             func(c *poolscaling.CapacityPlanning) { c.AvgChildSessionSeconds = 120 },
		"avgWorkspaceSizeMB":                 func(c *poolscaling.CapacityPlanning) { c.AvgWorkspaceSizeMB = 500 },
		"sessionIdleFraction":                func(c *poolscaling.CapacityPlanning) { c.SessionIdleFraction = 0.5 },
	}
	for field, mut := range mutators {
		t.Run(field, func(t *testing.T) {
			c := poolscaling.DefaultCapacityPlanning()
			mut(&c)
			if c.AtDefaults() {
				t.Errorf("AtDefaults() = true after overriding %s, want false", field)
			}
		})
	}
}

// TestShouldWarnCapacityPlanningDefaults verifies the warning fires only
// for a Tier 2 / Tier 3 deployment running unsubstituted defaults. spec:
// §16.5 line 601.
func TestShouldWarnCapacityPlanningDefaults_spec_16_5_601(t *testing.T) {
	def := poolscaling.DefaultCapacityPlanning()
	custom := def
	custom.AvgSessionDurationSeconds = 600

	cases := []struct {
		name string
		cp   poolscaling.CapacityPlanning
		tier string
		want bool
	}{
		{"tier1 at defaults — dev/CI, no warning", def, "tier1", false},
		{"tier2 at defaults — warn", def, "tier2", true},
		{"tier3 at defaults — warn", def, "tier3", true},
		{"tier2 with overrides — no warning", custom, "tier2", false},
		{"tier3 with overrides — no warning", custom, "tier3", false},
		{"unknown tier at defaults — no warning", def, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := poolscaling.ShouldWarnCapacityPlanningDefaults(tc.cp, tc.tier); got != tc.want {
				t.Errorf("ShouldWarnCapacityPlanningDefaults(%+v, %q) = %v, want %v", tc.cp, tc.tier, got, tc.want)
			}
		})
	}
}
