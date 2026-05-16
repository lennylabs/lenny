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
)

// Node is one delegation-tree node to recover. Depth is the node's
// distance from the tree root (root = 0).
type Node struct {
	SessionID string
	Depth     int
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
	Now          func() time.Time
}

func (c Config) withDefaults() Config {
	if c.LevelTimeout <= 0 {
		c.LevelTimeout = DefaultLevelTimeout
	}
	if c.TreeTimeout <= 0 {
		c.TreeTimeout = DefaultTreeTimeout
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now() }
	}
	return c
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
// marked failed when recoverFn returns an error, when its level has
// exceeded LevelTimeout, or when the traversal has exceeded
// TreeTimeout — once the tree deadline passes every remaining node is
// failed without a recovery attempt. Returns one NodeResult per node,
// in bottom-up traversal order.
func Recover(nodes []Node, recoverFn func(Node) error, cfg Config) []NodeResult {
	cfg = cfg.withDefaults()
	results := make([]NodeResult, 0, len(nodes))
	treeDeadline := cfg.Now().Add(cfg.TreeTimeout)
	treeExpired := false

	for _, level := range Levels(nodes) {
		levelDeadline := cfg.Now().Add(cfg.LevelTimeout)
		if levelDeadline.After(treeDeadline) {
			levelDeadline = treeDeadline
		}
		for _, node := range level {
			now := cfg.Now()
			switch {
			case treeExpired || !now.Before(treeDeadline):
				treeExpired = true
				results = append(results, NodeResult{
					Node: node, Outcome: OutcomeFailed,
					Reason: "tree recovery deadline exceeded",
				})
			case !now.Before(levelDeadline):
				results = append(results, NodeResult{
					Node: node, Outcome: OutcomeFailed,
					Reason: "level recovery deadline exceeded",
				})
			default:
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
