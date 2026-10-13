// SPDX-License-Identifier: MIT

package recovery_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
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

// recoverAll is a recoverFn that reports every node recovered.
func recoverAll(context.Context, recovery.Node) error { return nil }

// waitForDeadline is a recoverFn whose restore never commits: it waits until
// the node's deadline ends the wait.
func waitForDeadline(ctx context.Context, _ recovery.Node) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRecoverAllSucceed(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "root", Depth: 0},
		{SessionID: "leaf", Depth: 1},
	}
	results := recovery.Recover(context.Background(), nodes, nil, recoverAll, recovery.Config{})
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
	var started, waited []string
	recovery.Recover(context.Background(), nodes, func(n recovery.Node) {
		started = append(started, n.SessionID)
	}, func(_ context.Context, n recovery.Node) error {
		waited = append(waited, n.SessionID)
		return nil
	}, recovery.Config{})
	want := []string{"leaf", "mid", "root"}
	if !reflect.DeepEqual(started, want) || !reflect.DeepEqual(waited, want) {
		t.Errorf("start order %v, wait order %v, want both %v (bottom-up)", started, waited, want)
	}
}

func TestRecoverMarksNodeFailureFailed(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "ok", Depth: 1},
		{SessionID: "broken", Depth: 1},
	}
	results := recovery.Recover(context.Background(), nodes, nil, func(_ context.Context, n recovery.Node) error {
		if n.SessionID == "broken" {
			return errors.New("pod gone")
		}
		return nil
	}, recovery.Config{})
	got := map[string]recovery.NodeResult{}
	for _, r := range results {
		got[r.Node.SessionID] = r
	}
	if got["ok"].Outcome != recovery.OutcomeRecovered {
		t.Errorf("ok outcome = %s, want recovered", got["ok"].Outcome)
	}
	if got["broken"].Outcome != recovery.OutcomeFailed || got["broken"].Reason != "pod gone" {
		t.Errorf("broken = %+v, want failed with the restore's error", got["broken"])
	}
}

// TestRecoverDrivesEveryNodeOfALevelBeforeWaiting_spec_8_10 asserts that the
// traversal starts every selected node of a level before it waits on any of
// them, because maxLevelRecoverySeconds bounds the wait for all nodes at one
// depth together: two siblings whose restores each take more than half the
// level budget both recover.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestRecoverDrivesEveryNodeOfALevelBeforeWaiting_spec_8_10(t *testing.T) {
	nodes := []recovery.Node{
		{SessionID: "a", Depth: 1},
		{SessionID: "b", Depth: 1},
	}
	var mu sync.Mutex
	commitAt := map[string]time.Time{}
	start := func(n recovery.Node) {
		mu.Lock()
		defer mu.Unlock()
		commitAt[n.SessionID] = time.Now().Add(120 * time.Millisecond)
	}
	results := recovery.Recover(context.Background(), nodes, start, func(ctx context.Context, n recovery.Node) error {
		mu.Lock()
		at, ok := commitAt[n.SessionID]
		started := len(commitAt)
		mu.Unlock()
		if !ok || started != 2 {
			return errors.New("waited on a node before every sibling was driven")
		}
		select {
		case <-time.After(time.Until(at)):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, recovery.Config{LevelTimeout: 200 * time.Millisecond, TreeTimeout: time.Minute})
	for _, r := range results {
		if r.Outcome != recovery.OutcomeRecovered {
			t.Errorf("node %s = %+v, want recovered: each restore runs concurrently within the level budget", r.Node.SessionID, r)
		}
	}
}

// TestRecoverWaitEndsAtNodeDeadline_spec_8_10 asserts that a wait the node's
// deadline ends returns at that deadline and is recorded with the reason of
// the budget that set it, and that no later node is failed because of it.
//
// spec: §8.10 (Delegation Tree Recovery).
func TestRecoverWaitEndsAtNodeDeadline_spec_8_10(t *testing.T) {
	cases := []struct {
		name   string
		node   recovery.Node
		cfg    recovery.Config
		reason string
	}{
		{
			"level budget",
			recovery.Node{SessionID: "a", Depth: 1},
			recovery.Config{LevelTimeout: 40 * time.Millisecond, TreeTimeout: time.Minute},
			recovery.ReasonLevelTimeout,
		},
		{
			"tree budget",
			recovery.Node{SessionID: "a", Depth: 1},
			recovery.Config{LevelTimeout: time.Minute, TreeTimeout: 40 * time.Millisecond},
			recovery.ReasonTreeTimeout,
		},
		{
			"node resume window",
			recovery.Node{SessionID: "a", Depth: 1, ResumeWindow: 40 * time.Millisecond},
			recovery.Config{LevelTimeout: time.Minute, TreeTimeout: time.Minute},
			recovery.ReasonResumeWindow,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes := []recovery.Node{tc.node, {SessionID: "b", Depth: 1}}
			begin := time.Now()
			results := recovery.Recover(context.Background(), nodes, nil, func(ctx context.Context, n recovery.Node) error {
				if n.SessionID == "a" {
					return waitForDeadline(ctx, n)
				}
				return nil
			}, tc.cfg)
			if elapsed := time.Since(begin); elapsed > 5*time.Second {
				t.Fatalf("traversal took %v; the wait outlived its node's deadline", elapsed)
			}
			got := map[string]recovery.NodeResult{}
			for _, r := range results {
				got[r.Node.SessionID] = r
			}
			if got["a"].Outcome != recovery.OutcomeFailed || got["a"].Reason != tc.reason {
				t.Errorf("a = %+v, want failed with %q", got["a"], tc.reason)
			}
			if got["b"].Outcome != recovery.OutcomeRecovered {
				t.Errorf("b = %+v, want recovered: a sibling's expired wait fails no later node", got["b"])
			}
		})
	}
}

func TestRecoverTreeTimeoutFailsRemainingLevels(t *testing.T) {
	// A leaf and a root. Waiting on the leaf consumes the whole tree
	// budget, so the root (a shallower level) is failed without a start.
	nodes := []recovery.Node{
		{SessionID: "leaf", Depth: 1},
		{SessionID: "root", Depth: 0},
	}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var started []string
	results := recovery.Recover(context.Background(), nodes, func(n recovery.Node) {
		started = append(started, n.SessionID)
	}, func(context.Context, recovery.Node) error {
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
	if got["root"].Outcome != recovery.OutcomeFailed || got["root"].Reason != recovery.ReasonTreeTimeout {
		t.Fatalf("root = %+v, want failed past the tree deadline", got["root"])
	}
	if len(started) != 1 || started[0] != "leaf" {
		t.Errorf("started = %v, want only [leaf] — the root is past the tree deadline", started)
	}
}

func TestRecoverEmptyTree(t *testing.T) {
	results := recovery.Recover(context.Background(), nil, nil, recoverAll, recovery.Config{})
	if len(results) != 0 {
		t.Errorf("an empty tree produced %d results, want 0", len(results))
	}
}

// TestQuiescenceDeadlineUsesDefault_spec_11_3_224 verifies the default
// usage-quiescence window matches the §11.3 spec value (5s).
// F-11.3.19.
func TestQuiescenceDeadlineUsesDefault_spec_11_3_224(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cfg := recovery.Config{}
	got := cfg.QuiescenceDeadline(now).Sub(now)
	if got != recovery.DefaultUsageQuiescenceTimeout {
		t.Errorf("QuiescenceDeadline default delta = %s, want %s", got, recovery.DefaultUsageQuiescenceTimeout)
	}
	if recovery.DefaultUsageQuiescenceTimeout != 5*time.Second {
		t.Errorf("DefaultUsageQuiescenceTimeout = %s, want 5s (§11.3)", recovery.DefaultUsageQuiescenceTimeout)
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

// TestDefaultLevelAndTreeRecovery_spec_8_10_1022_1023 pins the §8.10 defaults so a regression that drops the operator-tunable
// path back to non-spec values is caught. F-8.10.6.
func TestDefaultLevelAndTreeRecovery_spec_8_10_1022_1023(t *testing.T) {
	if recovery.DefaultLevelTimeout != 120*time.Second {
		t.Errorf("DefaultLevelTimeout = %s, want 120s (§8.10)", recovery.DefaultLevelTimeout)
	}
	if recovery.DefaultTreeTimeout != 600*time.Second {
		t.Errorf("DefaultTreeTimeout = %s, want 600s (§8.10)", recovery.DefaultTreeTimeout)
	}
}

// TestRecoverHonorsConfigLevelAndTreeOverrides_spec_8_10_1022_1023
// covers the operator-tunable per-level / whole-tree budgets — the
// flag-supplied Config values must take effect (an unused Config field
// would silently re-apply the defaults). A level whose wait outlives a 40ms
// level budget ends at that budget rather than at the 120s default.
// F-8.10.6.
func TestRecoverHonorsConfigLevelAndTreeOverrides_spec_8_10_1022_1023(t *testing.T) {
	nodes := []recovery.Node{{SessionID: "a", Depth: 1}}
	begin := time.Now()
	results := recovery.Recover(context.Background(), nodes, nil, waitForDeadline, recovery.Config{
		LevelTimeout: 40 * time.Millisecond,
		TreeTimeout:  time.Hour,
	})
	if elapsed := time.Since(begin); elapsed > 5*time.Second {
		t.Fatalf("operator-supplied LevelTimeout dropped: the wait took %v", elapsed)
	}
	if len(results) != 1 || results[0].Outcome != recovery.OutcomeFailed || results[0].Reason != recovery.ReasonLevelTimeout {
		t.Errorf("results = %+v, want a failed node with the level reason", results)
	}
}

// TestRecoverNodeResumeWindowBindsBeforeLevel_spec_8_10_1027 covers the
// §8.10 effective recovery window contract — a node whose individual
// maxResumeWindowSeconds already elapsed when its level starts is failed with
// the node-window reason without a start, while a sibling recovers. F-8.10.11.
func TestRecoverNodeResumeWindowBindsBeforeLevel_spec_8_10_1027(t *testing.T) {
	clock := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	nodes := []recovery.Node{
		{SessionID: "deep", Depth: 2},
		{SessionID: "short-rw", Depth: 1, ResumeWindow: 2 * time.Second},
		{SessionID: "long-rw", Depth: 1},
	}
	var started []string
	results := recovery.Recover(context.Background(), nodes, func(n recovery.Node) {
		started = append(started, n.SessionID)
	}, func(context.Context, recovery.Node) error {
		clock = clock.Add(3 * time.Second) // the deeper level burns short-rw's window
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
	if got["short-rw"].Outcome != recovery.OutcomeFailed || got["short-rw"].Reason != recovery.ReasonResumeWindow {
		t.Errorf("short-rw = %+v, want failed with the node-window reason", got["short-rw"])
	}
	for _, id := range started {
		if id == "short-rw" {
			t.Error("short-rw was started past its resume window")
		}
	}
}

// TestRecoverTreeRemainingBindsNodeWindow_spec_8_10_1027 covers the
// other direction of §8.10 — when the per-node window would
// outlive the remaining tree budget, the tree cap binds first. F-8.10.11.
func TestRecoverTreeRemainingBindsNodeWindow_spec_8_10_1027(t *testing.T) {
	clock := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	nodes := []recovery.Node{
		{SessionID: "leaf", Depth: 1, ResumeWindow: 30 * time.Minute},
		{SessionID: "root", Depth: 0, ResumeWindow: 30 * time.Minute},
	}
	results := recovery.Recover(context.Background(), nodes, nil, func(context.Context, recovery.Node) error {
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
	if got["root"].Outcome != recovery.OutcomeFailed || got["root"].Reason != recovery.ReasonTreeTimeout {
		t.Errorf("root should fail with the tree-deadline reason once the 10m budget elapses: %+v", got["root"])
	}
}
