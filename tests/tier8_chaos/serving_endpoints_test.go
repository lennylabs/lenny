// SPDX-License-Identifier: MIT

//go:build chaos

package tier8_chaos_test

import "testing"

// spec: 25.15 (Failure Mode Analysis), 10.1 (Graceful Drain)
// diagnosis: the outage wait read a draining gateway pod as unreachable.
// kube-proxy routes to a terminating endpoint whose serving condition is
// still true when no ready endpoint remains, and a gateway pod in its
// preStop drain stays serving until the readiness probe observes the
// drain flip. A failure here means a gateway outage test would start
// asserting unreachability while a draining pod still answers requests.
func TestServingEndpointCountTreatsADrainingServingPodAsReachable(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want int
	}{
		{name: "no endpoints", out: "", want: 0},
		{name: "ready and serving", out: "true/true\ntrue/true\n", want: 2},
		{name: "terminating and still serving", out: "false/true\n", want: 1},
		{name: "terminating after the readiness flip", out: "false/false\nfalse/false\n", want: 0},
		{name: "serving unset falls back to ready", out: "true/\nfalse/\n", want: 1},
		{name: "mixed drain progress", out: "false/true\nfalse/false\n", want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countServing(tc.out); got != tc.want {
				t.Errorf("countServing(%q) = %d, want %d", tc.out, got, tc.want)
			}
		})
	}
}
