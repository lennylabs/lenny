// SPDX-License-Identifier: MIT

//go:build e2e_kind

// In-process checks of the shared-deadline poller the Kind lifecycle drain
// cases use to hold the §12.8 pod-release bound. They drive a fake clock
// and need no cluster.
package tier5_e2e_kind_test

import (
	"testing"
	"time"
)

// fakeClock advances only when the poller sleeps.
type fakeClock struct{ now time.Time }

func (f *fakeClock) clock() pollClock {
	return pollClock{now: func() time.Time { return f.now }, sleep: func(d time.Duration) { f.now = f.now.Add(d) }}
}

// releasedAt returns a probe that reports a claim released once the fake
// clock reaches at.
func releasedAt(f *fakeClock, at time.Time) func() (bool, string) {
	return func() (bool, string) {
		if f.now.Before(at) {
			return false, "Bound"
		}
		return true, ""
	}
}

// TestDrainBoundSharesOneDeadlineAcrossPods_spec_12_8 pins the deadline the
// suspend case measures every pod release against.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means the poller gave a later pod its own budget
// measured from an earlier pod's release, so a replica that released a pod
// up to twice the sweeper interval plus the seal bound after the suspend
// would still pass the Kind drain case.
func TestDrainBoundSharesOneDeadlineAcrossPods_spec_12_8(t *testing.T) {
	const bound = 100 * time.Second
	start := time.Unix(1_700_000_000, 0)
	cases := []struct {
		name        string
		releaseA    time.Duration
		releaseB    time.Duration
		wantPending []string
	}{
		{name: "both released within the bound", releaseA: 10 * time.Second, releaseB: 90 * time.Second},
		{name: "released together at the start", releaseA: 0, releaseB: 0},
		// A per-pod deadline started after pod A's release would allow pod B
		// until 99s+100s; the shared deadline fails it at 100s.
		{name: "second pod late after the first used most of the bound", releaseA: 99 * time.Second, releaseB: 150 * time.Second, wantPending: []string{"pod-b"}},
		{name: "first pod late", releaseA: 101 * time.Second, releaseB: 5 * time.Second, wantPending: []string{"pod-a"}},
		{name: "both late", releaseA: 120 * time.Second, releaseB: 180 * time.Second, wantPending: []string{"pod-a", "pod-b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeClock{now: start}
			probes := map[string]func() (bool, string){
				"pod-a": releasedAt(f, start.Add(tc.releaseA)),
				"pod-b": releasedAt(f, start.Add(tc.releaseB)),
			}
			pending := awaitAllBy(start.Add(bound), f.clock(), probes)
			if len(pending) != len(tc.wantPending) {
				t.Fatalf("pending = %v, want %v", pending, tc.wantPending)
			}
			for _, name := range tc.wantPending {
				if state, ok := pending[name]; !ok || state != "Bound" {
					t.Fatalf("pending = %v, want %s still Bound", pending, name)
				}
			}
			if f.now.After(start.Add(bound + claimPollInterval)) {
				t.Fatalf("poller ran to %s, past the shared deadline %s", f.now.Sub(start), bound)
			}
		})
	}
}

// TestDrainBoundPollsOnceAfterThePassedDeadline_spec_12_8 pins that a
// deadline already passed still reads every claim once rather than failing
// a pod that was released in time.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means the poller skipped the final read at the
// deadline, so a pod released exactly at the bound reads as still bound.
func TestDrainBoundPollsOnceAfterThePassedDeadline_spec_12_8(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	f := &fakeClock{now: start}
	probes := map[string]func() (bool, string){
		"released": releasedAt(f, start),
		"bound":    releasedAt(f, start.Add(time.Hour)),
	}
	pending := awaitAllBy(start.Add(-time.Second), f.clock(), probes)
	if _, ok := pending["released"]; ok || len(pending) != 1 {
		t.Fatalf("pending = %v, want only the bound pod", pending)
	}
	if !f.now.Equal(start) {
		t.Fatalf("poller slept past a deadline already passed: now %s", f.now.Sub(start))
	}
}
