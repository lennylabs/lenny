// SPDX-License-Identifier: MIT

// Package recovery implements the §8.10 bottom-up delegation-tree
// reattach traversal. When delegation-tree nodes fail, the gateway
// recovers them leaves-first: it groups the failed nodes by depth and
// recovers each level deepest-first, bounding each level by
// maxLevelRecoverySeconds and the whole traversal by
// maxTreeRecoverySeconds. A node not recovered within its budget is
// marked terminally failed and the traversal continues upward.
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
// distance from the tree root (root = 0). ResumeWindow, when positive,
// is the §7.3 / §8.10 per-node `maxResumeWindowSeconds`
// budget the recovery traversal must compose with the level- and
// tree-wide caps. Zero leaves the node with no individual bound — the
// level/tree budgets alone govern. spec: §8.10.
type Node struct {
	SessionID    string
	Depth        int
	ResumeWindow time.Duration
}

// Outcome is a node's recovery result.
type Outcome int

const (
	// OutcomeRecovered — the node was reattached successfully.
	OutcomeRecovered Outcome = iota
	// OutcomeFailed — the node could not be recovered and is marked
	// terminally failed.
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
	ReasonResumeWindow = "node resume window exceeded"
)

// Recover drives the §8.10 bottom-up traversal. It groups nodes by depth and
// recovers each level deepest-first. For each level it first calls start for
// every node that passes its tree, level, and resume-window checks, so every
// selected node of the level is driven before any of them is waited on,
// because maxLevelRecoverySeconds bounds the wait for all nodes at one depth
// together. It then calls recoverFn for those nodes in order; recoverFn
// returns nil once the node's restore committed. Each recoverFn call
// receives a context whose deadline is the node's deadline: the earliest of
// the level deadline, the tree deadline, and the end of the node's
// individual `maxResumeWindowSeconds` (Node.ResumeWindow). A recoverFn error
// fails the node; when the node's deadline ended the wait, the node is
// recorded with the reason of the budget that set the deadline. A node whose
// budget has already elapsed is failed without a start, and once the tree
// deadline passes every remaining node is failed. start may be nil. Returns
// one NodeResult per node, in bottom-up traversal order. spec: §8.10.
func Recover(ctx context.Context, nodes []Node, start func(Node), recoverFn func(context.Context, Node) error, cfg Config) []NodeResult {
	cfg = cfg.withDefaults()
	results := make([]NodeResult, 0, len(nodes))
	traversalStart := cfg.Now()
	treeDeadline := traversalStart.Add(cfg.TreeTimeout)

	for _, level := range Levels(nodes) {
		levelDeadline := cfg.Now().Add(cfg.LevelTimeout)
		if levelDeadline.After(treeDeadline) {
			levelDeadline = treeDeadline
		}
		type selected struct {
			node     Node
			deadline time.Time
			reason   string
		}
		var driven []selected
		for _, node := range level {
			now := cfg.Now()
			if reason, failed := budgetElapsed(node, now, traversalStart, levelDeadline, treeDeadline); failed {
				results = append(results, NodeResult{Node: node, Outcome: OutcomeFailed, Reason: reason})
				continue
			}
			deadline, reason := nodeDeadline(node, traversalStart, levelDeadline, treeDeadline)
			if start != nil {
				start(node)
			}
			driven = append(driven, selected{node: node, deadline: deadline, reason: reason})
		}
		for _, d := range driven {
			results = append(results, waitNode(ctx, d.node, d.deadline, d.reason, recoverFn, cfg.Now))
		}
	}
	return results
}

// budgetElapsed reports whether a node's tree, level, or resume-window budget
// has already elapsed at now, with the reason Recover records.
func budgetElapsed(node Node, now, traversalStart, levelDeadline, treeDeadline time.Time) (string, bool) {
	windowOpen := node.ResumeWindow == 0 || now.Before(traversalStart.Add(node.ResumeWindow))
	switch {
	case !now.Before(treeDeadline):
		return ReasonTreeTimeout, true
	case !now.Before(levelDeadline) && windowOpen:
		// Level cap bit first while the node's individual window is still
		// open — surface the level-cap reason so an operator can grow
		// `maxLevelRecoverySeconds`.
		return ReasonLevelTimeout, true
	case !windowOpen:
		// Per-node `maxResumeWindowSeconds` elapsed before the level/tree
		// budgets. spec: §8.10.
		return ReasonResumeWindow, true
	default:
		return "", false
	}
}

// nodeDeadline returns the earliest of the node's budgets and the reason of
// the budget that sets it.
func nodeDeadline(node Node, traversalStart, levelDeadline, treeDeadline time.Time) (time.Time, string) {
	deadline, reason := levelDeadline, ReasonLevelTimeout
	if !treeDeadline.After(levelDeadline) {
		reason = ReasonTreeTimeout
	}
	if node.ResumeWindow > 0 {
		if ndl := traversalStart.Add(node.ResumeWindow); ndl.Before(deadline) {
			deadline, reason = ndl, ReasonResumeWindow
		}
	}
	return deadline, reason
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
