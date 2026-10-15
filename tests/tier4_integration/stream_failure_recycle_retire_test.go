// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// failStream ends the session's CH-ATTACH stream with codes.Internal and
// waits until the coordinator marked the binding crashed.
func failStream(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id, pod string) {
	t.Helper()
	bind, ok := r.Registry.Get(id)
	if !ok {
		t.Fatalf("%s binds no session %s", r.ID, id)
	}
	h.Cluster.PodOf(pod).FailAttach()
	deadline := time.Now().Add(10 * time.Second)
	for !bind.Crashed() {
		if time.Now().After(deadline) {
			t.Fatal("the failed stream did not mark the binding crashed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// requireRetired fails unless the pod's claim is gone, so the release took
// the retire path rather than the recycle path, whose claim stays
// `recycling` until the adapter's scrub report.
func requireRetired(t *testing.T, h *lifecycleharness.Harness, pod string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
		if err != nil {
			t.Fatalf("read the claim of %s: %v", pod, err)
		}
		if phase == string(claimstate.Recycling) {
			t.Fatalf("the claim of %s was patched recycling; a pod whose stream failed must retire", pod)
		}
		if !exists {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the claim of %s is still %q, want it deleted by the retire path", pod, phase)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// spec: 5.2 (Pool Configuration and Execution Modes), 28.5.1
// (Gateway-to-pod)
// diagnosis: on a recycling pool, a pod whose CH-ATTACH stream ended with
// Internal was recycled for the next session: a release that ran with a
// clean disposition (the post-terminal path after a terminal write, or an
// idle suspension's release after its write won the race against the
// failure report) ignored the binding's crash mark and patched the claim
// recycling. Or a session with no stream failure was retired, so the
// recycle path itself is broken.
func TestStreamFailureRetiresARecyclingPod_spec_5_2(t *testing.T) {
	t.Run("terminal row released by the coordinator", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 4, Recycling: true})
		coordinator, peer := h.Replicas[0], h.Replicas[1]
		id := h.StartSession(coordinator)
		if code, body := h.Send(coordinator, id, "open-stream"); code != http.StatusOK {
			t.Fatalf("message = %d %v", code, body)
		}
		pod := h.Row(id).PodAssignment
		// The peer writes the terminal state, so the coordinator's binding
		// is released by its terminal binding release on the next sweep.
		if code, body := h.Do(peer, http.MethodDelete, "/v1/sessions/"+id, nil); code >= http.StatusBadRequest {
			t.Fatalf("DELETE on the peer = %d %v", code, body)
		}
		if st := h.Row(id).State; st != session.StateCancelled {
			t.Fatalf("state = %q, want cancelled", st)
		}
		failStream(t, h, coordinator, id, pod)
		h.Pass()
		requireRetired(t, h, pod)
	})

	t.Run("idle suspension release after the failure", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 4, Recycling: true})
		r := h.Replicas[0]
		id := h.StartSession(r)
		if code, body := h.Send(r, id, "open-stream"); code != http.StatusOK {
			t.Fatalf("message = %d %v", code, body)
		}
		pod := h.Row(id).PodAssignment
		// The stream fails right after the idle suspension's write lands
		// and before its release, so the release runs with the completed
		// disposition on a binding marked crashed.
		h.Trace.ArmMatching("Update", "writeIdleSuspension", lifecycleharness.FaultPeerTakeover,
			func(lifecycleharness.TraceEntry, lifecycleharness.FaultKind) { failStream(t, h, r, id, pod) })
		h.SuspendIdle(r, id)
		h.Trace.Start()
		if row := h.Row(id); row.State != session.StateSuspended || row.PodAssignment != "" {
			t.Fatalf("the idle suspension left the row %q on %q, want podless suspended", row.State, row.PodAssignment)
		}
		requireRetired(t, h, pod)
	})

	t.Run("control: no stream failure recycles", func(t *testing.T) {
		h := newComposeWith(t, lifecycleharness.Options{Replicas: 1, Pods: 4, Recycling: true})
		r := h.Replicas[0]
		id := h.StartSession(r)
		if code, body := h.Send(r, id, "open-stream"); code != http.StatusOK {
			t.Fatalf("message = %d %v", code, body)
		}
		pod := h.Row(id).PodAssignment
		if code, body := h.Do(r, http.MethodPost, "/v1/sessions/"+id+"/terminate", nil); code != http.StatusOK {
			t.Fatalf("terminate = %d %v", code, body)
		}
		h.Settle()
		phase, exists, err := h.Cluster.ClaimPhase(context.Background(), pod)
		if err != nil || !exists || phase != string(claimstate.Recycling) {
			t.Fatalf("claim of %s after a clean end = %q (exists %v, err %v), want recycling", pod, phase, exists, err)
		}
	})
}
