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
	// DefaultUsageQuiescenceTimeout is the §11.3 line 224
	// `delegation.usageQuiescenceTimeoutSeconds` window the tree-recovery
	// path waits after the most recent child usage report before
	// declaring the delegation tree quiescent and starting the bottom-up
	// reattach traversal. A node that reports usage during the
	// quiescence window resets the timer. spec: §11.3 line 224; §8.10
	// tree recovery.
	DefaultUsageQuiescenceTimeout = 5 * time.Second
)

// Node is one delegation-tree node to recover. Depth is the node's
// distance from the tree root (root = 0). ResumeWindow, when positive,
// is the §7.3 line 408 / §8.10 line 1027 per-node `maxResumeWindowSeconds`
// budget the recovery traversal must compose with the level- and
// tree-wide caps. Zero leaves the node with no individual bound — the
// level/tree budgets alone govern. spec: §8.10 line 1027.
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
	// UsageQuiescenceTimeout is the §11.3 line 224 wall-clock window the
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
// every new report calls this helper after each report. spec: §11.3
// line 224.
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

// Recover drives the §8.10 bottom-up traversal. It groups nodes by
// depth and recovers each level deepest-first, calling recoverFn per
// node; recoverFn returns nil on a successful reattach. A node is
// marked failed when recoverFn returns an error, when its individual
// `maxResumeWindowSeconds` has elapsed (Node.ResumeWindow), when its
// level has exceeded LevelTimeout, or when the traversal has exceeded
// TreeTimeout — once the tree deadline passes every remaining node is
// failed without a recovery attempt. Per §8.10 line 1027 the effective
// recovery window for any node is
// `min(maxResumeWindowSeconds, remaining maxTreeRecoverySeconds)`; this
// implementation also clamps to the per-level budget. Returns one
// NodeResult per node, in bottom-up traversal order. spec: §8.10 lines
// 1020-1023, 1027.
func Recover(nodes []Node, recoverFn func(Node) error, cfg Config) []NodeResult {
	cfg = cfg.withDefaults()
	results := make([]NodeResult, 0, len(nodes))
	traversalStart := cfg.Now()
	treeDeadline := traversalStart.Add(cfg.TreeTimeout)
	treeExpired := false

	for _, level := range Levels(nodes) {
		levelDeadline := cfg.Now().Add(cfg.LevelTimeout)
		if levelDeadline.After(treeDeadline) {
			levelDeadline = treeDeadline
		}
		for _, node := range level {
			now := cfg.Now()
			// spec: §8.10 line 1027 — effective recovery window is
			// min(node.ResumeWindow, remaining tree window). Composed with
			// the per-level cap so a level still bounds a long-resume-window
			// node when its level peers progress more slowly.
			nodeDeadline := levelDeadline
			if node.ResumeWindow > 0 {
				ndl := traversalStart.Add(node.ResumeWindow)
				if ndl.Before(nodeDeadline) {
					nodeDeadline = ndl
				}
			}
			switch {
			case treeExpired || !now.Before(treeDeadline):
				treeExpired = true
				results = append(results, NodeResult{
					Node: node, Outcome: OutcomeFailed,
					Reason: "tree recovery deadline exceeded",
				})
			case !now.Before(levelDeadline) && (node.ResumeWindow == 0 || now.Before(traversalStart.Add(node.ResumeWindow))):
				// Level cap bit first while the node's individual window is
				// still open — surface the level-cap reason so an operator
				// can grow `maxLevelRecoverySeconds`.
				results = append(results, NodeResult{
					Node: node, Outcome: OutcomeFailed,
					Reason: "level recovery deadline exceeded",
				})
			case node.ResumeWindow > 0 && !now.Before(traversalStart.Add(node.ResumeWindow)):
				// Per-node `maxResumeWindowSeconds` elapsed before the
				// level/tree budgets — the §7.3 per-session resume window
				// is the binding deadline for this node. spec: §8.10 line
				// 1027 ("node transitions to `expired`").
				results = append(results, NodeResult{
					Node: node, Outcome: OutcomeFailed,
					Reason: "node resume window exceeded",
				})
			default:
				_ = nodeDeadline
				if err := recoverFn(node); err != nil {
					results = append(results, NodeResult{
						Node: node, Outcome: OutcomeFailed, Reason: err.Error(),
					})
				} else {
					results = append(results, NodeResult{Node: node, Outcome: OutcomeRecovered})
				}
			}
		}
	}
	return results
}
