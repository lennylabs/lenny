// SPDX-License-Identifier: MIT

package kind

import (
	"slices"
	"testing"
)

// TestSetFlagArgOverridesAndRestoresClaimOrphanTimeout pins the argument
// rewrite the tier-5 erasure case uses to lower the controller's
// claimOrphanTimeout for the run and to put it back afterwards, including on
// a later run after a killed one. A rewrite that leaves the original
// argument beside the override, or a restore that loses it, would leave the
// controller on a value the chart never rendered.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 4.6.1 (Orphan claim detection)
func TestSetFlagArgOverridesAndRestoresClaimOrphanTimeout(t *testing.T) {
	const flag = "claim-orphan-timeout"
	cases := []struct {
		name     string
		args     []string
		wantArgs []string
		wantPrev string
	}{
		{
			name:     "present flag replaced in place",
			args:     []string{"--a=1", "--claim-orphan-timeout=900s", "--b=2"},
			wantArgs: []string{"--a=1", "--claim-orphan-timeout=60s", "--b=2"},
			wantPrev: "--claim-orphan-timeout=900s",
		},
		{
			name:     "absent flag appended",
			args:     []string{"--a=1"},
			wantArgs: []string{"--a=1", "--claim-orphan-timeout=60s"},
			wantPrev: flagAbsent,
		},
		{
			name:     "duplicate flag collapsed to one",
			args:     []string{"--claim-orphan-timeout=900s", "--claim-orphan-timeout=1200s"},
			wantArgs: []string{"--claim-orphan-timeout=60s"},
			wantPrev: "--claim-orphan-timeout=900s",
		},
		{
			name:     "flag with a shared prefix untouched",
			args:     []string{"--claim-orphan-timeout-extra=1", "--claim-orphan-timeout=900s"},
			wantArgs: []string{"--claim-orphan-timeout-extra=1", "--claim-orphan-timeout=60s"},
			wantPrev: "--claim-orphan-timeout=900s",
		},
		{
			name:     "nil args",
			wantArgs: []string{"--claim-orphan-timeout=60s"},
			wantPrev: flagAbsent,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := slices.Clone(tc.args)
			got, prev := SetFlagArg(in, flag, "60s")
			if !slices.Equal(got, tc.wantArgs) || prev != tc.wantPrev {
				t.Fatalf("SetFlagArg(%v) = %v, %q; want %v, %q", tc.args, got, prev, tc.wantArgs, tc.wantPrev)
			}
			if !slices.Equal(in, tc.args) {
				t.Fatalf("SetFlagArg modified its input: %v", in)
			}
			restored := RestoreFlagArg(got, flag, prev)
			want := tc.args
			if tc.name == "duplicate flag collapsed to one" {
				want = []string{"--claim-orphan-timeout=900s"}
			}
			if !slices.Equal(restored, want) {
				t.Fatalf("RestoreFlagArg(%v, %q) = %v, want %v", got, prev, restored, want)
			}
		})
	}
}
