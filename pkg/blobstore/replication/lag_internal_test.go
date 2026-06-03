// SPDX-License-Identifier: MIT

package replication

import "testing"

// TestDeriveLagSeconds covers the §25.11 line 4085 lag estimate: empty
// queue is zero lag, a queue draining at a known rate is queue/rate, and a
// non-empty queue with no throughput floors the rate so the lag reads as a
// large positive value rather than zero.
func TestDeriveLagSeconds_spec_25_11_4085(t *testing.T) {
	cases := []struct {
		name        string
		queued      float64
		bandwidth   float64
		want        float64
		wantAtLeast float64
	}{
		{name: "empty queue is zero lag", queued: 0, bandwidth: 1000, want: 0},
		{name: "drains at observed rate", queued: 1000, bandwidth: 100, want: 10},
		{name: "negative queue is zero lag", queued: -5, bandwidth: 100, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := deriveLagSeconds(tc.queued, tc.bandwidth); got != tc.want {
				t.Errorf("deriveLagSeconds(%v,%v) = %v, want %v", tc.queued, tc.bandwidth, got, tc.want)
			}
		})
	}

	// Stalled queue: non-empty queue, zero throughput → floored rate yields
	// a large positive lag that trips the lag alerts.
	if got := deriveLagSeconds(1<<20, 0); got <= 0 {
		t.Errorf("stalled queue lag = %v, want > 0", got)
	}
}
