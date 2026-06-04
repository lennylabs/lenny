// SPDX-License-Identifier: MIT

package deadlock_test

import (
	"reflect"
	"testing"
	"time"

	session "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/deadlock"
)

var base = time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

func node(id string, state session.State, awaiting []string, inputs ...string) deadlock.Node {
	var pis []deadlock.PendingInput
	for _, r := range inputs {
		pis = append(pis, deadlock.PendingInput{RequestID: r, BlockedSince: base})
	}
	return deadlock.Node{
		SessionID:        id,
		TenantID:         "acme",
		State:            state,
		AwaitingChildIDs: awaiting,
		PendingInputs:    pis,
	}
}

func snap(ns ...deadlock.Node) deadlock.Snapshot {
	m := map[string]deadlock.Node{}
	for _, n := range ns {
		m[n.SessionID] = n
	}
	return deadlock.Snapshot{Nodes: m}
}

func roots(sts []deadlock.Subtree) []string {
	var out []string
	for _, st := range sts {
		out = append(out, st.Root)
	}
	return out
}

// TestDetectSimpleDeadlock_spec_8_8_981 covers a parent awaiting a single
// child that is blocked on request_input: the canonical subtree deadlock.
func TestDetectSimpleDeadlock_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("P", session.StateRunning, []string{"C"}),
		node("C", session.StateRunning, nil, "r1"),
	))
	if len(got) != 1 {
		t.Fatalf("Detect returned %d subtrees, want 1: %+v", len(got), got)
	}
	st := got[0]
	if st.Root != "P" {
		t.Errorf("Root = %q, want P", st.Root)
	}
	if !reflect.DeepEqual(st.DeepestTasks, []string{"C"}) {
		t.Errorf("DeepestTasks = %v, want [C]", st.DeepestTasks)
	}
	want := []deadlock.BlockedRequest{{RequestID: "r1", TaskID: "C", BlockedSince: base}}
	if !reflect.DeepEqual(st.BlockedRequests, want) {
		t.Errorf("BlockedRequests = %+v, want %+v", st.BlockedRequests, want)
	}
	if st.TenantID != "acme" {
		t.Errorf("TenantID = %q, want acme", st.TenantID)
	}
}

// TestDetectRunningChildIsNotDeadlock_spec_8_8_981 covers the §8.8
// false-negative: an actively-running awaited child can still settle on
// its own, so the parent is not deadlocked.
func TestDetectRunningChildIsNotDeadlock_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("P", session.StateRunning, []string{"C"}),
		node("C", session.StateRunning, nil),
	))
	if len(got) != 0 {
		t.Fatalf("Detect returned %d subtrees, want 0: %+v", len(got), got)
	}
}

// TestDetectTerminalSiblingStillDeadlocks_spec_8_8_981 covers a parent
// awaiting one settled child and one input_required child: the settled
// child is satisfied, so the parent is deadlocked on the blocked sibling.
func TestDetectTerminalSiblingStillDeadlocks_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("P", session.StateRunning, []string{"C1", "C2"}),
		node("C1", session.StateCompleted, nil),
		node("C2", session.StateRunning, nil, "r2"),
	))
	if len(got) != 1 || got[0].Root != "P" {
		t.Fatalf("want one subtree rooted at P, got %+v", got)
	}
	if !reflect.DeepEqual(got[0].DeepestTasks, []string{"C2"}) {
		t.Errorf("DeepestTasks = %v, want [C2]", got[0].DeepestTasks)
	}
}

// TestDetectRunningSiblingSuppresses_spec_8_8_981 covers the false
// negative where one awaited child is actively running while another is
// input_required: the running sibling can still settle, so no deadlock.
func TestDetectRunningSiblingSuppresses_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("P", session.StateRunning, []string{"C1", "C2"}),
		node("C1", session.StateRunning, nil),
		node("C2", session.StateRunning, nil, "r2"),
	))
	if len(got) != 0 {
		t.Fatalf("Detect returned %d subtrees, want 0: %+v", len(got), got)
	}
}

// TestDetectNestedReportsTopmostRoot_spec_8_8_981 covers a three-level
// chain: only the topmost awaiting root is reported, and the deepest
// blocked task is the leaf.
func TestDetectNestedReportsTopmostRoot_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("R", session.StateRunning, []string{"M"}),
		node("M", session.StateRunning, []string{"L"}),
		node("L", session.StateRunning, nil, "rl"),
	))
	if !reflect.DeepEqual(roots(got), []string{"R"}) {
		t.Fatalf("roots = %v, want [R]", roots(got))
	}
	if !reflect.DeepEqual(got[0].DeepestTasks, []string{"L"}) {
		t.Errorf("DeepestTasks = %v, want [L]", got[0].DeepestTasks)
	}
}

// TestDetectDeepestAmongMultipleInputs_spec_8_8_981 covers a chain where
// both an intermediate and a leaf task are blocked on input: the deepest
// task (the leaf) is selected for DEADLOCK_TIMEOUT, but both requests are
// reported in blockedRequests.
func TestDetectDeepestAmongMultipleInputs_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("R", session.StateRunning, []string{"M"}),
		node("M", session.StateRunning, []string{"L"}, "rm"),
		node("L", session.StateRunning, nil, "rl"),
	))
	if len(got) != 1 {
		t.Fatalf("want one subtree, got %+v", got)
	}
	if !reflect.DeepEqual(got[0].DeepestTasks, []string{"L"}) {
		t.Errorf("DeepestTasks = %v, want [L]", got[0].DeepestTasks)
	}
	want := []deadlock.BlockedRequest{
		{RequestID: "rl", TaskID: "L", BlockedSince: base},
		{RequestID: "rm", TaskID: "M", BlockedSince: base},
	}
	if !reflect.DeepEqual(got[0].BlockedRequests, want) {
		t.Errorf("BlockedRequests = %+v, want %+v", got[0].BlockedRequests, want)
	}
}

// TestDetectAllTerminalChildrenNotActionable_spec_8_8_981 covers a parent
// awaiting only settled children: the await is about to return, so the
// subtree is not deadlocked.
func TestDetectAllTerminalChildrenNotActionable_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("P", session.StateRunning, []string{"C1", "C2"}),
		node("C1", session.StateCompleted, nil),
		node("C2", session.StateFailed, nil),
	))
	if len(got) != 0 {
		t.Fatalf("Detect returned %d subtrees, want 0: %+v", len(got), got)
	}
}

// TestDetectCrossSubtreeCycleTerminates_spec_8_8_981 covers the documented
// false-negative cross-subtree circular wait: the detector terminates
// (no infinite recursion) and reports no deadlock when neither side can
// be classified as fully blocked.
func TestDetectCrossSubtreeCycleTerminates_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("A", session.StateRunning, []string{"B"}),
		node("B", session.StateRunning, []string{"A"}),
	))
	if len(got) != 0 {
		t.Fatalf("Detect returned %d subtrees, want 0: %+v", len(got), got)
	}
}

// TestDetectStandaloneInputRequiredIsNotDeadlock_spec_8_8_981 covers a
// leaf blocked on input with no awaiting parent: it can be answered by an
// external client, so it is not a deadlock.
func TestDetectStandaloneInputRequiredIsNotDeadlock_spec_8_8_981(t *testing.T) {
	got := deadlock.Detect(snap(
		node("solo", session.StateRunning, nil, "r"),
	))
	if len(got) != 0 {
		t.Fatalf("Detect returned %d subtrees, want 0: %+v", len(got), got)
	}
}
