// SPDX-License-Identifier: MIT

// Package recovery implements the §8.10 bottom-up delegation-tree
// reattach traversal. When delegation-tree nodes fail, the gateway
// recovers them leaves-first: it groups the failed nodes by depth and
// recovers each level deepest-first, bounding each level by
// maxLevelRecoverySeconds and the whole traversal by
// maxTreeRecoverySeconds. A node not recovered within its budget is
// recorded as failed and the traversal continues upward; the caller marks
// it terminally failed only while it is still in resume_pending.
//
// The traversal is pure: the caller injects the per-node recovery
// action and the clock, so the ordering and deadline rules are
// unit-testable without the pod-resume machinery.
package recovery

import (
	"context"
	"errors"
	"sort"
	"time"
)

// §8.10 recovery-window defaults.
const (
	// DefaultLevelTimeout bounds recovery of all nodes at one depth
	// (`maxLevelRecoverySeconds`).
	DefaultLevelTimeout = 120 * time.Second
	// DefaultTreeTimeout bounds the whole traversal
	// (`maxTreeRecoverySeconds`). It overrides the per-level budgets.
	DefaultTreeTimeout = 600 * time.Second
	// DefaultUsageQuiescenceTimeout is the §11.3
	// `delegation.usageQuiescenceTimeoutSeconds` window the tree-recovery
	// path waits after the most recent child usage report before
	// declaring the delegation tree quiescent and starting the bottom-up
	// reattach traversal. A node that reports usage during the
	// quiescence window resets the timer. spec: §11.3; §8.10
	// tree recovery.
	DefaultUsageQuiescenceTimeout = 5 * time.Second
)

// Node is one delegation-tree node to recover. Depth is the node's
// distance from the tree root (root = 0). The traversal bounds a node only
// by the level and tree budgets: a node's individual resume window is owned
// by the §6.2 resume_pending wall-clock cap, which moves the node out of
// resume_pending without the traversal's involvement. spec: §8.10.
type Node struct {
	SessionID string
	Depth     int
}

// Outcome is a node's recovery result.
type Outcome int

const (
	// OutcomeRecovered — the node was reattached successfully.
	OutcomeRecovered Outcome = iota
	// OutcomeFailed — the node could not be recovered. The caller marks
	// it terminally failed only while it is still in resume_pending.
	OutcomeFailed
)

func (o Outcome) String() string {
	if o == OutcomeRecovered {
		return "recovered"
	}
	return "failed"
}

// NodeResult pairs a node with its recovery outcome. Reason carries
// the cause when Outcome is OutcomeFailed.
type NodeResult struct {
	Node    Node
	Outcome Outcome
	Reason  string
}

// Config bounds a traversal. A zero timeout selects its §8.10 default;
// a nil Now selects time.Now.
type Config struct {
	LevelTimeout time.Duration
	TreeTimeout  time.Duration
	// UsageQuiescenceTimeout is the §11.3 wall-clock window the
	// caller observes between the last child usage report and the start
	// of the bottom-up reattach traversal. Exposed on Config so the
	// gateway can thread an operator-tuned value through to whatever
	// orchestrator polls for quiescence; the traversal itself does not
	// consume the value. Zero selects DefaultUsageQuiescenceTimeout.
	UsageQuiescenceTimeout time.Duration
	Now                    func() time.Time

	// OnResult, when non-nil, receives each node's result at the moment
	// Recover decides it, on Recover's own goroutine. Every call for a
	// level precedes the next level's start calls, so a caller that
	// fails a node from OnResult fails each level before the traversal
	// continues upward. spec: §8.10.
	OnResult func(NodeResult)
}

func (c Config) withDefaults() Config {
	if c.LevelTimeout <= 0 {
		c.LevelTimeout = DefaultLevelTimeout
	}
	if c.TreeTimeout <= 0 {
		c.TreeTimeout = DefaultTreeTimeout
	}
	if c.UsageQuiescenceTimeout <= 0 {
		c.UsageQuiescenceTimeout = DefaultUsageQuiescenceTimeout
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now() }
	}
	return c
}

// QuiescenceDeadline returns the wall-clock time the tree-recovery
// orchestrator must wait until before declaring the tree quiescent and
// starting the reattach traversal. lastUsageReport is the timestamp of
// the most recent child usage report; an orchestrator that resets on
// every new report calls this helper after each report. spec: §11.3.
func (c Config) QuiescenceDeadline(lastUsageReport time.Time) time.Time {
	cfg := c.withDefaults()
	return lastUsageReport.Add(cfg.UsageQuiescenceTimeout)
}

// Levels groups nodes by depth and returns them deepest-first — the
// §8.10 bottom-up recovery order. Nodes within a level are ordered by
// SessionID so the traversal is deterministic.
func Levels(nodes []Node) [][]Node {
	byDepth := map[int][]Node{}
	for _, n := range nodes {
		byDepth[n.Depth] = append(byDepth[n.Depth], n)
	}
	depths := make([]int, 0, len(byDepth))
	for d := range byDepth {
		depths = append(depths, d)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(depths)))
	levels := make([][]Node, 0, len(depths))
	for _, d := range depths {
		level := byDepth[d]
		sort.Slice(level, func(i, j int) bool { return level[i].SessionID < level[j].SessionID })
		levels = append(levels, level)
	}
	return levels
}

// Failure reasons Recover records for a node it could not recover within a
// budget. A wait that a node's deadline ends is recorded with the reason of
// the budget that set the deadline, so a caller's timeout counters count it.
const (
	ReasonTreeTimeout  = "tree recovery deadline exceeded"
	ReasonLevelTimeout = "level recovery deadline exceeded"
)

// Recover drives the §8.10 bottom-up traversal. It groups nodes by depth and
// recovers each level deepest-first. For each level it first calls start for
// every node whose tree and level budgets have not elapsed, so every selected
// node of the level is driven before any of them is waited on, because
// maxLevelRecoverySeconds bounds the wait for all nodes at one depth
// together. It then calls recoverFn for those nodes in order; recoverFn
// returns nil once the node's restore committed. Each recoverFn call
// receives a context whose deadline is the node's deadline: the earlier of
// the level deadline and the tree deadline. A recoverFn error fails the
// node; when the node's deadline ended the wait, the node is recorded with
// the reason of the budget that set the deadline. A node whose budget has
// already elapsed is failed without a start, and once the tree deadline
// passes every remaining node is failed.
//
// Recover appends each node's result as it decides it and, when
// cfg.OnResult is non-nil, calls it synchronously with that result: for a
// node failed without a start, at that point, and for a driven node, when
// its wait returns. Every OnResult call for a level therefore precedes the
// next level's start calls. start may be nil. Returns one NodeResult per
// node, in bottom-up traversal order. spec: §8.10.
func Recover(ctx context.Context, nodes []Node, start func(Node), recoverFn func(context.Context, Node) error, cfg Config) []NodeResult {
	cfg = cfg.withDefaults()
	results := make([]NodeResult, 0, len(nodes))
	record := func(res NodeResult) {
		results = append(results, res)
		if cfg.OnResult != nil {
			cfg.OnResult(res)
		}
	}
	treeDeadline := cfg.Now().Add(cfg.TreeTimeout)

	for _, level := range Levels(nodes) {
		levelDeadline := cfg.Now().Add(cfg.LevelTimeout)
		if levelDeadline.After(treeDeadline) {
			levelDeadline = treeDeadline
		}
		deadline, deadlineReason := nodeDeadline(levelDeadline, treeDeadline)
		var driven []Node
		for _, node := range level {
			if reason, failed := budgetElapsed(cfg.Now(), levelDeadline, treeDeadline); failed {
				record(NodeResult{Node: node, Outcome: OutcomeFailed, Reason: reason})
				continue
			}
			if start != nil {
				start(node)
			}
			driven = append(driven, node)
		}
		for _, node := range driven {
			record(waitNode(ctx, node, deadline, deadlineReason, recoverFn, cfg.Now))
		}
	}
	return results
}

// budgetElapsed reports whether the tree or level budget has already elapsed
// at now, with the reason Recover records.
func budgetElapsed(now, levelDeadline, treeDeadline time.Time) (string, bool) {
	switch {
	case !now.Before(treeDeadline):
		return ReasonTreeTimeout, true
	case !now.Before(levelDeadline):
		return ReasonLevelTimeout, true
	default:
		return "", false
	}
}

// nodeDeadline returns the deadline of a driven node, the level deadline
// already capped at the tree deadline, and the reason of the budget that
// sets it.
func nodeDeadline(levelDeadline, treeDeadline time.Time) (time.Time, string) {
	if !treeDeadline.After(levelDeadline) {
		return levelDeadline, ReasonTreeTimeout
	}
	return levelDeadline, ReasonLevelTimeout
}

// waitNode runs recoverFn for one driven node under a context bounded by the
// node's deadline, measured on the traversal clock.
func waitNode(ctx context.Context, node Node, deadline time.Time, reason string,
	recoverFn func(context.Context, Node) error, now func() time.Time,
) NodeResult {
	nctx, cancel := context.WithTimeout(ctx, deadline.Sub(now()))
	defer cancel()
	err := recoverFn(nctx, node)
	if err == nil {
		return NodeResult{Node: node, Outcome: OutcomeRecovered}
	}
	if errors.Is(nctx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return NodeResult{Node: node, Outcome: OutcomeFailed, Reason: reason}
	}
	return NodeResult{Node: node, Outcome: OutcomeFailed, Reason: err.Error()}
}
