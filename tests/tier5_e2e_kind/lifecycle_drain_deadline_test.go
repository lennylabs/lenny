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

// TestSuspendReleaseBoundIsSweeperIntervalPlusSeal_spec_12_8 pins the
// suspend case's deadline to the §12.8 bound with no added slack.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means the Kind suspend case measures pod release
// against more than the Sweeper interval plus
// maxWorkspaceSealDurationSeconds, so a replica that releases a drained
// session's pod late still passes.
func TestSuspendReleaseBoundIsSweeperIntervalPlusSeal_spec_12_8(t *testing.T) {
	for _, tc := range []struct{ interval, seal time.Duration }{
		{15 * time.Second, 300 * time.Second},
		{5 * time.Second, 30 * time.Second},
		{0, 0},
	} {
		if got, want := suspendReleaseBound(tc.interval, tc.seal), tc.interval+tc.seal; got != want {
			t.Errorf("suspendReleaseBound(%s, %s) = %s, want %s", tc.interval, tc.seal, got, want)
		}
	}
}

// TestErasureReleaseBoundIsBindReleaseWindowPlusSeal_spec_12_8 pins the
// erasure case's deadline to the CH-BINDRELEASE apply window plus
// maxWorkspaceSealDurationSeconds with no added slack, and pins that the
// bound at the chart defaults stays below the default claimOrphanTimeout,
// so the orphan GC cannot satisfy it.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation), 28.5.7 (Gateway-to-store)
// diagnosis: a failure means the Kind erasure case measures the claim
// leaving bound against more than the apply window plus the seal bound, so
// a binding the drain's release never reached still passes on the orphan
// GC, or the bound at the defaults reaches claimOrphanTimeout.
func TestErasureReleaseBoundIsBindReleaseWindowPlusSeal_spec_12_8(t *testing.T) {
	for _, tc := range []struct{ window, seal time.Duration }{
		{bindReleaseApplyWindow, 300 * time.Second},
		{5 * time.Second, 30 * time.Second},
		{0, 0},
	} {
		if got, want := erasureReleaseBound(tc.window, tc.seal), tc.window+tc.seal; got != want {
			t.Errorf("erasureReleaseBound(%s, %s) = %s, want %s", tc.window, tc.seal, got, want)
		}
	}
	const defaultSeal, defaultClaimOrphanTimeout = 300 * time.Second, 900 * time.Second
	bound := erasureReleaseBound(bindReleaseApplyWindow, defaultSeal)
	if bound >= defaultClaimOrphanTimeout {
		t.Fatalf("the erasure bound %s at the defaults is not below claimOrphanTimeout %s", bound, defaultClaimOrphanTimeout)
	}
	// A pod released one poll interval past the bound fails the case.
	start := time.Unix(1_700_000_000, 0)
	f := &fakeClock{now: start}
	late := start.Add(bound + claimPollInterval)
	pending := awaitAllBy(start.Add(bound), f.clock(), map[string]func() (bool, string){"pod": releasedAt(f, late)})
	if _, ok := pending["pod"]; !ok {
		t.Fatalf("a pod released %s after the erase passed the %s bound", bound+claimPollInterval, bound)
	}
}

// TestPodReturnedToPoolRequiresDeletedClaimAndUnoccupiedSandbox_spec_12_8
// pins what the Kind suspend case accepts as a pod returned to the pool.
// The erasure case holds a weaker condition, which
// TestErasureClaimProbeAcceptsAnyNonBoundClaim_spec_12_8 pins.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 4.6.1 (occupancy projection on claim DELETE), 4.6.3 (SandboxClaim binding state)
// diagnosis: a failure means the probe accepts a pod still held out of the
// warm pool: a claim that still exists in a non-bound phase (recycling,
// reserved, released, or failed), or a deleted claim whose Sandbox still
// projects an occupied phase.
func TestPodReturnedToPoolRequiresDeletedClaimAndUnoccupiedSandbox_spec_12_8(t *testing.T) {
	cases := []struct {
		name         string
		claimFound   bool
		claimPhase   string
		sandboxPhase string
		want         bool
	}{
		{"claim bound", true, "bound", "claimed", false},
		{"claim recycling", true, "recycling", "claimed", false},
		{"claim reserved for its tenant", true, "reserved", "reserved", false},
		{"claim released but not deleted", true, "released", "draining", false},
		{"claim failed but not deleted", true, "failed", "draining", false},
		{"claim with no phase yet", true, "", "idle", false},
		{"claim deleted, sandbox still claimed", false, "", "claimed", false},
		{"claim deleted, sandbox still reserved", false, "", "reserved", false},
		{"claim deleted, sandbox re-warming", false, "", "sdk_connecting", false},
		{"claim deleted, sandbox failed", false, "", "failed", false},
		{"claim deleted, pod returned idle", false, "", "idle", true},
		{"claim deleted, pod retiring", false, "", "draining", true},
		{"claim deleted, pod retired", false, "", "terminated", true},
		{"claim and sandbox deleted", false, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, state := podReturnedToPool(tc.claimFound, tc.claimPhase, tc.sandboxPhase)
			if got != tc.want {
				t.Fatalf("podReturnedToPool = %v (%s), want %v", got, state, tc.want)
			}
		})
	}
}

// TestErasureClaimProbeAcceptsAnyNonBoundClaim_spec_12_8 pins what the Kind
// erasure case accepts: the erased session's claim is deleted or in any
// phase other than bound, whatever the Sandbox's phase. It is weaker than
// podReturnedToPool, which the suspend case holds.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation), 4.6.3 (SandboxClaim binding state)
// diagnosis: a failure means the erasure probe rejects a claim the release
// already moved out of bound (released, failed, or deleted before the
// Sandbox re-warms), holding the erasure case to the suspend case's
// pool-return condition, or it accepts a claim still bound to the erased
// user's session.
func TestErasureClaimProbeAcceptsAnyNonBoundClaim_spec_12_8(t *testing.T) {
	cases := []struct {
		name       string
		claimFound bool
		claimPhase string
		want       bool
	}{
		{"claim still bound", true, "bound", false},
		{"claim released, not yet deleted", true, "released", true},
		{"claim failed, not yet deleted", true, "failed", true},
		{"claim recycling", true, "recycling", true},
		{"claim reserved", true, "reserved", true},
		{"claim deleted", false, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, state := claimNoLongerBound(tc.claimFound, tc.claimPhase)
			if got != tc.want {
				t.Fatalf("claimNoLongerBound = %v (%s), want %v", got, state, tc.want)
			}
		})
	}
	// A claim released while the Sandbox still reads claimed
	// passes the erasure probe and fails the suspend probe.
	if ok, _ := claimNoLongerBound(true, "released"); !ok {
		t.Fatal("erasure probe rejects a released claim")
	}
	if ok, _ := podReturnedToPool(true, "released", "claimed"); ok {
		t.Fatal("suspend probe accepts a released claim that has not been deleted")
	}
}
