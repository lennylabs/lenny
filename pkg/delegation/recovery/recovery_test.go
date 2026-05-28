// SPDX-License-Identifier: MIT

package recovery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/delegation/recovery"
)

func TestLevelsGroupsBottomUp(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "root", Depth: 0},
		{SessionID: "mid", Depth: 1},
		{SessionID: "leaf-b", Depth: 2},
		{SessionID: "leaf-a", Depth: 2},
	}
	levels := recovery.Levels(nodes)
	if len(levels) != 3 {
		t.Fatalf("Levels returned %d levels, want 3", len(levels))
	}
	// Deepest level first; within a level, ordered by SessionID.
	if levels[0][0].SessionID != "leaf-a" || levels[0][1].SessionID != "leaf-b" {
		t.Errorf("level 0 = %v, want [leaf-a leaf-b]", levels[0])
	}
	if levels[1][0].SessionID != "mid" {
		t.Errorf("level 1 = %v, want [mid]", levels[1])
	}
	if levels[2][0].SessionID != "root" {
		t.Errorf("level 2 = %v, want [root]", levels[2])
	}
}

func TestRecoverAllSucceed(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "root", Depth: 0},
		{SessionID: "leaf", Depth: 1},
	}
	results := recovery.Recover(nodes, func(recovery.Node) error { return nil }, recovery.Config{})
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, r := range results {
		if r.Outcome != recovery.OutcomeRecovered {
			t.Errorf("node %s outcome = %s, want recovered", r.Node.SessionID, r.Outcome)
		}
	}
}

func TestRecoverOrderIsBottomUp(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "root", Depth: 0},
		{SessionID: "mid", Depth: 1},
		{SessionID: "leaf", Depth: 2},
	}
	var order []string
	recovery.Recover(nodes, func(n recovery.Node) error {
		order = append(order, n.SessionID)
		return nil
	}, recovery.Config{})
	want := []string{"leaf", "mid", "root"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("recovery order = %v, want %v (bottom-up)", order, want)
			break
		}
	}
}

func TestRecoverMarksNodeFailureFailed(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "ok", Depth: 1},
		{SessionID: "broken", Depth: 1},
	}
	results := recovery.Recover(nodes, func(n recovery.Node) error {
		if n.SessionID == "broken" {
			return errors.New("pod gone")
		}
		return nil
	}, recovery.Config{})
	got := map[string]recovery.Outcome{}
	for _, r := range results {
		got[r.Node.SessionID] = r.Outcome
	}
	if got["ok"] != recovery.OutcomeRecovered {
		t.Errorf("ok outcome = %s, want recovered", got["ok"])
	}
	if got["broken"] != recovery.OutcomeFailed {
		t.Errorf("broken outcome = %s, want failed", got["broken"])
	}
}

func TestRecoverLevelTimeoutFailsRemainingLevelNodes(t *testing.T) {
	// Three nodes at the same depth. Each recovery consumes 60s; the
	// level budget is 100s, so the third node is past the level
	// deadline before it is attempted.
	nodes := []recovery.Node{
		{SessionID: "a", Depth: 1},
		{SessionID: "b", Depth: 1},
		{SessionID: "c", Depth: 1},
	}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var attempted []string
	results := recovery.Recover(nodes, func(n recovery.Node) error {
		attempted = append(attempted, n.SessionID)
		clock = clock.Add(60 * time.Second)
		return nil
	}, recovery.Config{
		LevelTimeout: 100 * time.Second,
		TreeTimeout:  10 * time.Minute,
		Now:          func() time.Time { return clock },
	})
	got := map[string]recovery.NodeResult{}
	for _, r := range results {
		got[r.Node.SessionID] = r
	}
	if got["a"].Outcome != recovery.OutcomeRecovered || got["b"].Outcome != recovery.OutcomeRecovered {
		t.Errorf("a/b should recover within the level budget: %+v", got)
	}
	if got["c"].Outcome != recovery.OutcomeFailed {
		t.Fatalf("c should fail past the level deadline: %+v", got["c"])
	}
	if got["c"].Reason != "level recovery deadline exceeded" {
		t.Errorf("c reason = %q, want the level-deadline reason", got["c"].Reason)
	}
	// c was never attempted — a node past the level deadline is not retried.
	for _, id := range attempted {
		if id == "c" {
			t.Error("node c was attempted despite being past the level deadline")
		}
	}
}

func TestRecoverTreeTimeoutFailsRemainingLevels(t *testing.T) {
	// A leaf and a root. Recovering the leaf consumes the whole tree
	// budget, so the root (a shallower level) is failed without an
	// attempt.
	nodes := []recovery.Node{
		{SessionID: "leaf", Depth: 1},
		{SessionID: "root", Depth: 0},
	}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var attempted []string
	results := recovery.Recover(nodes, func(n recovery.Node) error {
		attempted = append(attempted, n.SessionID)
		clock = clock.Add(11 * time.Minute) // past the 10-minute tree budget
		return nil
	}, recovery.Config{
		LevelTimeout: 5 * time.Minute,
		TreeTimeout:  10 * time.Minute,
		Now:          func() time.Time { return clock },
	})
	got := map[string]recovery.NodeResult{}
	for _, r := range results {
		got[r.Node.SessionID] = r
	}
	if got["leaf"].Outcome != recovery.OutcomeRecovered {
		t.Errorf("leaf should recover: %+v", got["leaf"])
	}
	if got["root"].Outcome != recovery.OutcomeFailed {
		t.Fatalf("root should fail past the tree deadline: %+v", got["root"])
	}
	if got["root"].Reason != "tree recovery deadline exceeded" {
		t.Errorf("root reason = %q, want the tree-deadline reason", got["root"].Reason)
	}
	if len(attempted) != 1 || attempted[0] != "leaf" {
		t.Errorf("attempted = %v, want only [leaf] — the root is past the tree deadline", attempted)
	}
}

func TestRecoverEmptyTree(t *testing.T) {
	results := recovery.Recover(nil, func(recovery.Node) error { return nil }, recovery.Config{})
	if len(results) != 0 {
		t.Errorf("an empty tree produced %d results, want 0", len(results))
	}
}

// TestQuiescenceDeadlineUsesDefault_spec_11_3_224 verifies the default
// usage-quiescence window matches the §11.3 line 224 spec value (5s).
// F-11.3.19.
func TestQuiescenceDeadlineUsesDefault_spec_11_3_224(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cfg := recovery.Config{}
	got := cfg.QuiescenceDeadline(now).Sub(now)
	if got != recovery.DefaultUsageQuiescenceTimeout {
		t.Errorf("QuiescenceDeadline default delta = %s, want %s", got, recovery.DefaultUsageQuiescenceTimeout)
	}
	if recovery.DefaultUsageQuiescenceTimeout != 5*time.Second {
		t.Errorf("DefaultUsageQuiescenceTimeout = %s, want 5s (§11.3 line 224)", recovery.DefaultUsageQuiescenceTimeout)
	}
}

// TestQuiescenceDeadlineHonorsOverride_spec_11_3_224 verifies the
// operator-tunable override flows through to QuiescenceDeadline. F-11.3.19.
func TestQuiescenceDeadlineHonorsOverride_spec_11_3_224(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cfg := recovery.Config{UsageQuiescenceTimeout: 12 * time.Second}
	got := cfg.QuiescenceDeadline(now).Sub(now)
	if got != 12*time.Second {
		t.Errorf("QuiescenceDeadline = %s, want 12s", got)
	}
}
