// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §15.4.2 / §15.4.3 — a Full-level runtime (CH-RUNTIMEOPS
// wired) drains through the CH-RUNTIMEOPS before the hard runtime
// close: Shutdown sends `terminate` so the runtime coordinates a clean
// drain, then closes the runtime process (MED-010).
func TestShutdownDrainsViaLifecycle_spec_15_4_2(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()

	s := New("test")
	rt := &holdRuntime{}
	s.Runtime = rt
	s.Lifecycle = lc
	if err := s.claimSessionForTest("sess-1"); err != nil {
		t.Fatalf("claimSession: %v", err)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		DeadlineMs: 4000,
		Reason:     "budget_exhausted",
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	got := fr.read()
	if got.Type != "terminate" || got.DeadlineMs != 4000 || got.Reason != "budget_exhausted" {
		t.Errorf("drain frame = %+v, want terminate deadlineMs 4000 reason budget_exhausted", got)
	}
	if len(rt.closed) != 1 || rt.closed[0] != "sess-1" {
		t.Errorf("runtime closed = %v, want [sess-1] after the drain signal", rt.closed)
	}
}

// spec: §15.4.3 — Basic/Standard runtimes have no CH-RUNTIMEOPS, so
// the drain is a no-op and never blocks or fails shutdown.
func TestDrainViaLifecycleNilIsNoOp_spec_15_4_3(t *testing.T) {
	s := New("test")
	// No Lifecycle wired — must not panic.
	s.drainViaLifecycle(1000, "session_complete")
}

// spec: §15.4.2 — the lifecycle terminate frame's reason must be a valid
// enum value; an empty or unrecognized ShutdownRequest reason normalizes
// to session_complete.
func TestDrainReason_spec_15_4_2(t *testing.T) {
	cases := map[string]string{
		"session_complete": "session_complete",
		"budget_exhausted": "budget_exhausted",
		"eviction":         "eviction",
		"operator":         "operator",
		"":                 "session_complete",
		"drain":            "session_complete",
		"garbage":          "session_complete",
	}
	for in, want := range cases {
		if got := drainReason(in); got != want {
			t.Errorf("drainReason(%q) = %q, want %q", in, got, want)
		}
	}
}

// spec: §5.2 (recycling "requires no runtime cooperation ... with no
// CH-RUNTIMEOPS exchange between sessions", and the pod "keeps the
// process alive and reuses it for the next session"), §15.4.2 (the
// DRAINING terminate frame), §4.7 (Shutdown recycle disposition)
//
// The recycle disposition says the gateway is holding this pod for its
// next session. The §15.4.2 terminate frame names no session and tells
// the pod's shared runtime to finish and exit, so it is the pod's own end
// rather than the ending session's: at the recycle boundary the adapter
// closes the ending session's runtime and sends no terminate frame. A
// Shutdown carrying no recycle disposition still drains, which
// TestShutdownDrainsViaLifecycle_spec_15_4_2 above pins.
func TestShutdownWithRecycleSendsNoRuntimeOpsTerminate_spec_5_2(t *testing.T) {
	lc, fr := startRuntimeOps(t)
	fr.handshake()

	s := New("test")
	rt := &holdRuntime{}
	s.Runtime = rt
	s.Lifecycle = lc
	if err := s.claimSessionForTest("sess-1"); err != nil {
		t.Fatalf("claimSession: %v", err)
	}

	if _, err := s.Shutdown(context.Background(), &adapterv1.ShutdownRequest{
		SessionId:  &adapterv1.SessionId{Value: "sess-1"},
		DeadlineMs: 4000,
		Reason:     "session_complete",
		Recycle:    &adapterv1.RecycleScrub{PodId: "pod-recycled"},
	}); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if got, ok := fr.readWithin(500 * time.Millisecond); ok {
		t.Errorf("drain frame = %+v on the recycle disposition; the pod keeps its runtime for the "+
			"next session, so no terminate frame goes out", got)
	}
	if len(rt.closed) != 1 || rt.closed[0] != "sess-1" {
		t.Errorf("runtime closed = %v, want [sess-1] on the recycle disposition", rt.closed)
	}
}
