//go:build component

// SPDX-License-Identifier: MIT

package lifecycle_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// spec: §5.2 (Slot assignment atomicity), §6.2 (Pod State Machine), §4.6.3
// (SandboxClaim: a release that ends the occupancy without reuse)
// diagnosis: on a concurrent-workspace pool, a DELETE of a session that never
// started released more than its own slot: the shared per-pod claim was
// deleted under a live sibling, the sibling's member was removed, or the
// session's own member stayed counted. The checker names the slot set, the
// claim, or the double removal it found.
func TestNotStartedDeleteKeepsTheSiblingsSlot_spec_5_2(t *testing.T) {
	h := lifecycleharness.New(t, lifecycleharness.Options{Replicas: 1, Pods: 2, MaxConcurrentSessions: 4})
	r := h.Replicas[0]
	sibling := h.StartSession(r)
	pod := h.Row(sibling).PodAssignment
	created := h.CreateSession(r)
	if got := h.Row(created).PodAssignment; got != pod {
		t.Fatalf("the created session reserved on %q, want the sibling's pod %q", got, pod)
	}
	if code, body := h.Do(r, http.MethodDelete, "/v1/sessions/"+created, nil); code >= http.StatusBadRequest {
		t.Fatalf("DELETE = %d %v", code, body)
	}
	h.RunToQuiescence()
	members, err := h.SlotMembers(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0] != sibling {
		t.Errorf("slot set of %s = %v, want only the sibling %s", pod, members, sibling)
	}
	if phase, ok, err := h.Cluster.ClaimPhase(context.Background(), pod); err != nil || !ok || phase != "bound" {
		t.Errorf("claim of %s = %q (exists %v, err %v), want bound", pod, phase, ok, err)
	}
	if n := h.Slots.Removals(pod, created); n != 1 {
		t.Errorf("removals of %s's member = %d, want 1", created, n)
	}
	if st := h.Row(sibling).State; st != session.StateRunning {
		t.Errorf("sibling state = %s, want running", st)
	}
	h.Check(t)
}
