// SPDX-License-Identifier: MIT

package kind

import "testing"

// TestAllPodsRunningAndReadyRejectsRunningButNotReady pins the
// fail-closed behavior of the tier-5 install gate. The gate previously
// looked at the pod phase alone, so a control plane whose pods were
// Running with a crash-looping or unready container passed the check and
// the calling test proceeded to port-forward at a pod that serves nothing.
// Requiring the Ready condition makes InstallLenny skip with its install
// hint instead.
func TestAllPodsRunningAndReadyRejectsRunningButNotReady(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{
			name: "all running and ready",
			out:  "Running\tTrue\nRunning\tTrue\n",
			want: true,
		},
		{
			// The case the phase-only gate admitted: the pod is scheduled
			// and its phase is Running, but the container is not passing
			// its readiness probe.
			name: "running but not ready",
			out:  "Running\tTrue\nRunning\tFalse\n",
			want: false,
		},
		{
			// A pod stuck in ContainerCreating (a missing Secret mount,
			// for example) reports phase Pending and carries a Ready
			// condition of False.
			name: "pending",
			out:  "Running\tTrue\nPending\tFalse\n",
			want: false,
		},
		{
			// A pod that has no Ready condition yet renders an empty
			// second field rather than a missing separator.
			name: "ready condition absent",
			out:  "Running\tTrue\nRunning\t\n",
			want: false,
		},
		{
			// jsonpath emitted a line with no separator at all; the
			// output is not the layout the gate parses, so it fails closed.
			name: "malformed line",
			out:  "Running\n",
			want: false,
		},
		{
			name: "no pods",
			out:  "\n",
			want: false,
		},
		{
			name: "empty output",
			out:  "",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := allPodsRunningAndReady(tc.out); got != tc.want {
				t.Errorf("allPodsRunningAndReady(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}
