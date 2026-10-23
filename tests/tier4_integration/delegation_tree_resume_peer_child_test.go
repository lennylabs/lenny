// SPDX-License-Identifier: MIT

//go:build integration

package tier4_integration_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// delegatedChildSeq numbers the delegated children the scenario commits.
var delegatedChildSeq atomic.Int64

// delegatedChild commits a child of rootID in created, as the delegation
// service does, and materializes it on r, which then coordinates it.
func delegatedChild(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, rootID string) string {
	t.Helper()
	id := fmt.Sprintf("00000000-0000-4000-9000-%012d", delegatedChildSeq.Add(1))
	ctx := context.Background()
	if err := h.Store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: lifecycleharness.Tenant, UserID: "alice@acme.com",
		RuntimeRef: lifecycleharness.RuntimeRef, IsolationProfile: isolation.ProfileSandboxed,
		State: session.StateCreated, ParentSessionID: rootID, RootSessionID: rootID,
	}); err != nil {
		t.Fatalf("commit the delegated child: %v", err)
	}
	h.Track(id)
	if st, err := r.Server.MaterializeDelegatedChild(ctx, lifecycleharness.Tenant, id); err != nil || st != session.StateRunning {
		t.Fatalf("materialize the delegated child on %s = %q, %v; want running", r.ID, st, err)
	}
	return id
}

// toAwaitingClientAction spends the session's retry budget, one restored
// crash at a time, until the next crash parks it in awaiting_client_action.
func toAwaitingClientAction(t *testing.T, h *lifecycleharness.Harness, r *lifecycleharness.Replica, id string) {
	t.Helper()
	for i := 0; i <= lifecycleharness.MaxRetries; i++ {
		lifecycleharness.Report(t, r, id, string(session.FailureRuntimeCrash))
		h.AwaitState(id, session.StateRunning, recoveryPasses)
	}
	if st := h.Row(id).State; st != session.StateAwaitingClientAction {
		t.Fatalf("after the retry budget %s is %q, want awaiting_client_action", id, st)
	}
}

// treeRecoverySettle is how long a scenario lets the tree recovery a
// resume starts on its own goroutine run before it reads the tree. A tree
// with no resume_pending node returns after one tree read.
const treeRecoverySettle = 500 * time.Millisecond

// spec: 8.10 (Delegation Tree Recovery), 10.1.1 (Stateless Replicas and
// Per-Session Coordination)
// diagnosis: the tree recovery a resume ran on one replica treated a
// delegated child that another replica coordinates as orphaned: it claimed
// the child a second pod, failed it, or recorded a partial_failure outcome,
// because it selected nodes by this replica's pod registry rather than by
// the node's row state.
func TestTreeRecoveryLeavesAPeerBoundChildRunning_spec_8_10(t *testing.T) {
	h := newComposeWith(t, lifecycleharness.Options{Replicas: 2, Pods: 32})
	a, b := h.Replicas[0], h.Replicas[1]
	root := h.StartSession(a)
	child := delegatedChild(t, h, b, root)
	sibling := delegatedChild(t, h, a, root)
	childPod := h.Row(child).PodAssignment
	if h.Bound(child) != b {
		t.Fatalf("the child is not bound on %s", b.ID)
	}

	check := func(t *testing.T, before int) {
		t.Helper()
		time.Sleep(treeRecoverySettle)
		h.Settle()
		for _, o := range h.TreeMetrics.Outcomes()[before:] {
			if o == "partial_failure" {
				t.Errorf("tree recovery outcome = %s, want no failure for a running peer-bound child", o)
			}
		}
		row := h.Row(child)
		if row.State != session.StateRunning || row.PodAssignment != childPod {
			t.Errorf("the child is %q on %q, want running on %q", row.State, row.PodAssignment, childPod)
		}
		if h.Bound(child) != b {
			t.Error("the child is no longer bound on its coordinator")
		}
		claims, err := h.Cluster.BoundClaims(context.Background())
		if err != nil {
			t.Fatalf("list claims: %v", err)
		}
		held := map[string]bool{}
		for _, id := range []string{root, child, sibling} {
			if p := h.Row(id).PodAssignment; p != "" {
				held[p] = true
			}
		}
		for _, c := range claims {
			if !held[c] {
				t.Errorf("a claim on %s names no session of the tree: the recovery claimed a second pod", c)
			}
		}
	}

	t.Run("sibling resumed", func(t *testing.T) {
		toAwaitingClientAction(t, h, a, sibling)
		before := len(h.TreeMetrics.Outcomes())
		if code, body := h.Do(a, http.MethodPost, "/v1/sessions/"+sibling+"/resume", nil); code != http.StatusOK {
			t.Fatalf("resume of the sibling = %d %v", code, body)
		}
		if row := h.AwaitState(sibling, session.StateRunning, recoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the sibling reached %q, want running", row.State)
		}
		check(t, before)
	})

	t.Run("root resumed", func(t *testing.T) {
		toAwaitingClientAction(t, h, a, root)
		before := len(h.TreeMetrics.Outcomes())
		if code, body := h.Do(a, http.MethodPost, "/v1/sessions/"+root+"/resume", nil); code != http.StatusOK {
			t.Fatalf("resume of the root = %d %v", code, body)
		}
		if row := h.AwaitState(root, session.StateRunning, recoveryPasses); row.State != session.StateRunning {
			t.Fatalf("the root reached %q, want running", row.State)
		}
		check(t, before)
	})
}
