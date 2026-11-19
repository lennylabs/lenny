// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// claimThenPrepareAgainstEnvtest claims sbx-1 through the production claim
// path against a real kube-apiserver, then runs Binder.Prepare for the same
// session against an adapter whose FinalizeWorkspace answers finalizeErr. It
// returns the client, the AgentPod's resourceVersion recorded between the
// claim and Prepare, and Prepare's error, so a caller can tell whether
// Prepare wrote the AgentPod.
func claimThenPrepareAgainstEnvtest(t *testing.T, finalizeErr error) (client.Client, string, error) {
	t.Helper()
	ns := testNamespace(t)
	ctx := context.Background()
	c := k8sClient(t, idleAgentPod(ns, "sbx-1", "10.244.1.7"))
	a := &stageAdapter{}
	b := newBinder(ns, c, stageAdapterDialer(t, a))

	claimed, err := b.Claim(ctx, podsession.BindRequest{Pool: testPool, SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	var claim lennyv1.PodClaim
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "claim-" + claimed.AgentPodName}, &claim); err != nil {
		t.Fatalf("per-pod claim after Claim: %v", err)
	}
	var before lennyv1.AgentPod
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: claimed.AgentPodName}, &before); err != nil {
		t.Fatalf("get sandbox before Prepare: %v", err)
	}

	a.mu.Lock()
	a.errs = map[string]error{"FinalizeWorkspace": finalizeErr}
	a.mu.Unlock()
	_, perr := b.Prepare(ctx, podsession.BindRequest{
		Pool: testPool, SessionID: "sess-1", AgentPodName: claimed.AgentPodName,
	})
	return c, before.ResourceVersion, perr
}

// A Binder.Prepare whose FinalizeWorkspace answers a typed slot-bind refusal
// leaves the per-pod PodClaim present at the apiserver and the AgentPod
// untouched.
//
// spec: §4.7.1 (role and gateway RPC contract); §6.2 (pod state machine)
//
// diagnosis: a failure means a typed §4.7.1 slot-bind refusal is retiring a
// pod that is serving the session correctly: Prepare's reclaim closure
// deleted the per-pod PodClaim (the drain) or wrote the AgentPod on a
// refusal instead of returning the refusal alone.
func TestPrepareSlotBindRefusalKeepsPodClaimAtApiserver(t *testing.T) {
	ns := testNamespace(t)
	refusal := slotBindRefusal(t, codes.Aborted, adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED)
	c, rvBefore, err := claimThenPrepareAgainstEnvtest(t, refusal)
	if !adapterclient.IsSlotBindRefusal(err) {
		t.Fatalf("Prepare error = %v, want the typed slot-bind refusal", err)
	}
	ctx := context.Background()
	var claim lennyv1.PodClaim
	if gerr := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "claim-sbx-1"}, &claim); gerr != nil {
		t.Fatalf("per-pod claim after a refused Prepare = %v, want present (the pod must not be drained)", gerr)
	}
	if !claim.DeletionTimestamp.IsZero() {
		t.Errorf("per-pod claim carries a deletionTimestamp after a refused Prepare, want none")
	}
	var after lennyv1.AgentPod
	if gerr := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "sbx-1"}, &after); gerr != nil {
		t.Fatalf("get sandbox after Prepare: %v", gerr)
	}
	if after.ResourceVersion != rvBefore {
		t.Errorf("AgentPod resourceVersion %s → %s across a refused Prepare, want untouched", rvBefore, after.ResourceVersion)
	}
}

// The same Binder.Prepare meeting an ordinary FinalizeWorkspace failure
// deletes the per-pod PodClaim, which is the drain failPhase performs.
//
// spec: §4.7.1 (role and gateway RPC contract); §6.2 (pod state machine)
//
// diagnosis: a failure means Prepare's reclaim closure no longer drains a pod
// on an ordinary bind-stage failure, so a pod whose setup chain aborted keeps
// its per-pod PodClaim and is never retired and replaced.
func TestPrepareOrdinaryFailureDeletesPodClaimAtApiserver(t *testing.T) {
	ns := testNamespace(t)
	c, _, err := claimThenPrepareAgainstEnvtest(t, status.Error(codes.Internal, "finalize broke"))
	if err == nil || adapterclient.IsSlotBindRefusal(err) {
		t.Fatalf("Prepare error = %v, want an ordinary failure", err)
	}
	var claim lennyv1.PodClaim
	gerr := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: "claim-sbx-1"}, &claim)
	if !apierrors.IsNotFound(gerr) {
		t.Errorf("per-pod claim after an ordinary Prepare failure = %v, want NotFound (claim deleted)", gerr)
	}
}
