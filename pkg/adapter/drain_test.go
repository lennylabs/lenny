// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"testing"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §15.4.2 / §15.4.3 — a Full-level runtime (lifecycle channel
// wired) drains through the lifecycle channel before the hard runtime
// close: Shutdown sends `terminate` so the runtime coordinates a clean
// drain, then closes the runtime process (MED-010).
func TestShutdownDrainsViaLifecycle_spec_15_4_2(t *testing.T) {
	lc, fr := startLifecycleChannel(t)
	fr.handshake()

	s := New("test")
	rt := &holdRuntime{}
	s.Runtime = rt
	s.Lifecycle = lc
	if err := s.claimSession("sess-1"); err != nil {
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

// spec: §15.4.3 — Basic/Standard runtimes have no lifecycle channel, so
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
