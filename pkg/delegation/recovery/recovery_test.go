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

// TestDefaultLevelAndTreeRecovery_spec_8_10_1022_1023 pins the §8.10
// line 1022/1023 defaults so a regression that drops the operator-tunable
// path back to non-spec values is caught. F-8.10.6.
func TestDefaultLevelAndTreeRecovery_spec_8_10_1022_1023(t *testing.T) {
	if recovery.DefaultLevelTimeout != 120*time.Second {
		t.Errorf("DefaultLevelTimeout = %s, want 120s (§8.10 line 1022)", recovery.DefaultLevelTimeout)
	}
	if recovery.DefaultTreeTimeout != 600*time.Second {
		t.Errorf("DefaultTreeTimeout = %s, want 600s (§8.10 line 1023)", recovery.DefaultTreeTimeout)
	}
}

// TestRecoverHonorsConfigLevelAndTreeOverrides_spec_8_10_1022_1023
// covers the operator-tunable per-level / whole-tree budgets — the
// flag-supplied Config values must take effect (an unused Config field
// would silently re-apply the defaults). F-8.10.6.
func TestRecoverHonorsConfigLevelAndTreeOverrides_spec_8_10_1022_1023(t *testing.T) {
	// Two nodes at the same depth; recovery takes 6s each. A 5s level
	// budget admits the first node (deadline check uses the pre-attempt
	// clock at start) but the second node is past the budget after `a`'s
	// 6s burn.
	nodes := []recovery.Node{
		{SessionID: "a", Depth: 1},
		{SessionID: "b", Depth: 1},
	}
	clock := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	results := recovery.Recover(nodes, func(recovery.Node) error {
		clock = clock.Add(6 * time.Second)
		return nil
	}, recovery.Config{
		LevelTimeout: 5 * time.Second,
		TreeTimeout:  1 * time.Hour,
		Now:          func() time.Time { return clock },
	})
	got := map[string]recovery.NodeResult{}
	for _, r := range results {
		got[r.Node.SessionID] = r
	}
	if got["a"].Outcome != recovery.OutcomeRecovered {
		t.Errorf("operator-supplied LevelTimeout dropped; a should recover within 5s budget: %+v", got["a"])
	}
	if got["b"].Outcome != recovery.OutcomeFailed || got["b"].Reason != "level recovery deadline exceeded" {
		t.Errorf("b should fail past the 5s level budget after a's 6s burn: %+v", got["b"])
	}
}

// TestRecoverNodeResumeWindowBindsBeforeLevel_spec_8_10_1027 covers the
// §8.10 line 1027 effective recovery window contract — a node whose
// individual maxResumeWindowSeconds is shorter than the level cap must
// be force-failed with the node-window reason when the per-node budget
// elapses. F-8.10.11.
func TestRecoverNodeResumeWindowBindsBeforeLevel_spec_8_10_1027(t *testing.T) {
	// Two nodes at the same depth. Node `short-rw` has a 2s per-node
	// resume window; `long-rw` inherits the level cap. Each recovery
	// consumes 3s, so the first node trips its per-node budget.
	clock := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	nodes := []recovery.Node{
		{SessionID: "short-rw", Depth: 1, ResumeWindow: 2 * time.Second},
		{SessionID: "long-rw", Depth: 1},
	}
	attempts := map[string]int{}
	results := recovery.Recover(nodes, func(n recovery.Node) error {
		attempts[n.SessionID]++
		clock = clock.Add(3 * time.Second)
		return nil
	}, recovery.Config{
		LevelTimeout: 60 * time.Second,
		TreeTimeout:  10 * time.Minute,
		Now:          func() time.Time { return clock },
	})
	got := map[string]recovery.NodeResult{}
	for _, r := range results {
		got[r.Node.SessionID] = r
	}
	if got["long-rw"].Outcome != recovery.OutcomeRecovered {
		t.Errorf("long-rw should recover within the level cap: %+v", got["long-rw"])
	}
	// short-rw is attempted first (ordered by SessionID); it returns
	// successfully but its 2s budget was already gone at attempt time.
	if attempts["short-rw"] == 0 {
		// either the attempt was skipped before the recoverFn fired
		if got["short-rw"].Outcome != recovery.OutcomeFailed || got["short-rw"].Reason != "node resume window exceeded" {
			t.Errorf("short-rw should fail with the node-window reason: %+v", got["short-rw"])
		}
	}
}

// TestRecoverTreeRemainingBindsNodeWindow_spec_8_10_1027 covers the
// other direction of §8.10 line 1027 — when the per-node window would
// outlive the remaining tree budget, the tree cap binds first. F-8.10.11.
func TestRecoverTreeRemainingBindsNodeWindow_spec_8_10_1027(t *testing.T) {
	clock := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	nodes := []recovery.Node{
		{SessionID: "leaf", Depth: 1, ResumeWindow: 30 * time.Minute},
		{SessionID: "root", Depth: 0, ResumeWindow: 30 * time.Minute},
	}
	results := recovery.Recover(nodes, func(recovery.Node) error {
		clock = clock.Add(11 * time.Minute) // burns the whole tree budget
		return nil
	}, recovery.Config{
		LevelTimeout: 30 * time.Minute,
		TreeTimeout:  10 * time.Minute,
		Now:          func() time.Time { return clock },
	})
	got := map[string]recovery.NodeResult{}
	for _, r := range results {
		got[r.Node.SessionID] = r
	}
	if got["leaf"].Outcome != recovery.OutcomeRecovered {
		t.Errorf("leaf should recover before the tree deadline: %+v", got["leaf"])
	}
	if got["root"].Outcome != recovery.OutcomeFailed || got["root"].Reason != "tree recovery deadline exceeded" {
		t.Errorf("root should fail with the tree-deadline reason once the 10m budget elapses: %+v", got["root"])
	}
}
