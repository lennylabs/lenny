// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// spec: §6.1 lines 5, 16, 24 — "After a session completes or fails in
// `executionMode: session`, the pod is terminated and replaced — never
// recycled for a different session." F-6.1.12.
//
// The §6.1 invariant has two enforcement layers:
//
//  1. Gateway side (this file): binder.Release drains the Sandbox via
//     phase=draining. The §6.2 lifecycle controller then advances
//     draining → terminated when the backing Pod is gone, after which
//     the WarmPoolController's planner creates a fresh idle Sandbox to
//     restore minWarm. The pod itself is therefore terminated — not
//     recycled — and a fresh one takes its place.
//
//  2. Adapter side (pkg/adapter/one_session_only_test.go): the adapter
//     keeps `credSessionID` sticky across Shutdown so a misbehaving
//     controller that somehow re-binds the same pod to a different
//     session would be rejected at the AssignCredentials RPC. This is
//     the defense-in-depth backstop.
//
// This file exercises layer (1) end-to-end and confirms the
// gateway-side teardown actually drives the Sandbox into draining.

func TestSessionModeReleaseDrainsSandbox_spec_6_1_invariant(t *testing.T) {
	srv := adapter.New("adapter-test")
	srv.WorkspaceRoot = t.TempDir()
	srv.Runtime = &fakeRuntime{}

	c := k8sClient(t, idleSandbox("sbx-orig", "10.244.1.10"))
	binder := newBinder(c, adapterDialer(t, srv))

	res, err := binder.Bind(context.Background(), podsession.BindRequest{
		Pool: testPool, SessionID: "sess-original", Runtime: "claude-code",
	})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// Verify the Sandbox is attached after Bind.
	var sb lennyv1.Sandbox
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-orig"}, &sb); err != nil {
		t.Fatalf("get sandbox after bind: %v", err)
	}
	if sb.Status.Phase != string(state.Attached) {
		t.Fatalf("phase after Bind = %q, want attached", sb.Status.Phase)
	}

	// Drive the §6.2 terminal disposition through Release with
	// state.Completed. The §6.1 invariant requires this to drain the
	// Sandbox, not return it to idle.
	if err := binder.Release(context.Background(), res, state.Completed); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "sbx-orig"}, &sb); err != nil {
		t.Fatalf("get sandbox after release: %v", err)
	}
	// The Sandbox MUST be in draining — NOT idle — so the §6.2
	// lifecycle planner advances draining → terminated and the WPC
	// plan provisions a fresh idle Sandbox in its place.
	if sb.Status.Phase != string(state.Draining) {
		t.Errorf("F-6.1.12: phase after Release = %q, want draining (NOT idle — recycling would violate §6.1 invariant)", sb.Status.Phase)
	}

	// Spec invariant safety net: even if the controller resyncs and
	// somehow tries to set phase=idle again, the §6.2 valid-transition
	// table rejects draining → idle so no path can put a session-mode
	// pod back into the idle pool.
	if err := state.IsValid(state.Draining, state.Idle); err == nil {
		t.Errorf("F-6.1.12: state-machine permits draining → idle; that would let a session-mode pod be recycled")
	}
}

// spec: §6.1 — the §6.2 state machine must NOT model a session-mode
// pod-recycling edge (attached → idle, completed → idle, etc.). This
// is the static invariant: even with a misbehaving writer, the only
// path out of a terminal/draining Sandbox phase is termination.
func TestStateMachineHasNoRecyclingEdgeForSessionPod_spec_6_1(t *testing.T) {
	forbidden := []struct {
		from state.State
		to   state.State
	}{
		{state.Attached, state.Idle},
		{state.Completed, state.Idle},
		{state.Failed, state.Idle},
		{state.Cancelled, state.Idle},
		{state.Expired, state.Idle},
		{state.Draining, state.Idle},
		{state.Terminated, state.Idle},
	}
	for _, c := range forbidden {
		if err := state.IsValid(c.from, c.to); err == nil {
			t.Errorf("F-6.1.12: state machine permits %s → %s; that would recycle a session-mode pod and violate §6.1", c.from, c.to)
		}
	}
}
