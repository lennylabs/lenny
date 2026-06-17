// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// spec: §6.1 lines 5, 16, 24 — "After a session completes or fails in
// `executionMode: session`, the pod is terminated and replaced — never
// recycled for a different session." F-6.1.12.
//
// The §6.1 invariant has two enforcement layers:
//
//  1. Gateway side (this file): binder.Release releases the pod by deleting
//     its per-pod claim; the gateway writes no Sandbox.status (§4.6.3). On a
//     recycle.enabled:false pool the WarmPoolController projects draining
//     then terminated from the claim DELETE, after which the planner creates
//     a fresh idle Sandbox to restore minWarm. The pod is therefore
//     terminated and a fresh one takes its place; the §6.2 state machine has
//     no draining → idle edge, so no path can recycle a session-mode pod.
//
//  2. Adapter side (pkg/adapter/one_session_only_test.go): the adapter
//     keeps `credSessionID` sticky across Shutdown so a misbehaving
//     controller that somehow re-binds the same pod to a different
//     session would be rejected at the AssignCredentials RPC. This is
//     the defense-in-depth backstop.
//
// This file exercises layer (1) end-to-end and confirms the gateway-side
// teardown deletes the claim and that the static §6.2 state machine forbids
// the only edge that would recycle the pod.

func TestSessionModeReleaseDeletesClaim_spec_6_1_invariant(t *testing.T) {
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

	// The gateway no longer writes Sandbox.status; the acquisition is
	// recorded on the per-pod claim's `bound` binding state, from which the
	// WPC projects `claimed`. The session reaching `running` is a
	// session-model state on the Postgres session row, not a CRD phase.
	var claim lennyv1.SandboxClaim
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-orig"}, &claim); err != nil {
		t.Fatalf("get per-pod claim after bind: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Bound) {
		t.Fatalf("claim binding state after Bind = %q, want bound", claim.Status.Phase)
	}

	// Drive the §6.2 terminal disposition through Release with the
	// `completed` disposition. The gateway releases the pod by deleting its
	// per-pod claim; it does not write Sandbox.status.phase. The §6.1
	// invariant requires the WPC to drain the pod (claim DELETE on a
	// recycle.enabled:false pod projects draining then terminated), not
	// return it to idle.
	if err := binder.Release(context.Background(), res, "completed"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Gateway side: the per-pod claim is deleted so the WPC projection
	// reclaims the pod. The gateway wrote no Sandbox.status.phase.
	err = c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: "claim-sbx-orig"}, &claim)
	if !apierrors.IsNotFound(err) {
		t.Errorf("F-6.1.12: per-pod claim get after Release = %v, want NotFound (gateway deletes the claim)", err)
	}

	// Static invariant: even if the controller resyncs and somehow tries to
	// set phase=idle, the §6.2 valid-transition table rejects draining → idle
	// so no path can put a session-mode pod back into the idle pool.
	if err := state.IsValid(state.Draining, state.Idle); err == nil {
		t.Errorf("F-6.1.12: state-machine permits draining → idle; that would let a session-mode pod be recycled")
	}
}

// spec: §6.1, §6.2 — a session-mode pod (recycle.enabled: false) is
// terminated and replaced after its session ends; it is never returned to
// idle for a different session. The fine session-terminal states moved to
// the Postgres session model (§6.2, §6.37), so the static §6.2 invariant is
// that no coarse terminal or draining phase has an edge back to idle: a pod
// that reaches failed, draining, or terminated can only proceed toward
// termination, never re-enter the claimable pool. The recycle path's
// reserved → idle hold-expiry edge is reachable only on a recycling pool
// and never from a session-mode pod, which drains directly from claimed.
func TestStateMachineHasNoRecyclingEdgeForSessionPod_spec_6_1(t *testing.T) {
	forbidden := []struct {
		from state.State
		to   state.State
	}{
		{state.Failed, state.Idle},
		{state.Draining, state.Idle},
		{state.Terminated, state.Idle},
	}
	for _, c := range forbidden {
		if err := state.IsValid(c.from, c.to); err == nil {
			t.Errorf("F-6.1.12: state machine permits %s → %s; that would recycle a session-mode pod and violate §6.1", c.from, c.to)
		}
	}
}
