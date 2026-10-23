// SPDX-License-Identifier: MIT

// Package treerecovery drives the §8.10 bottom-up delegation-tree
// recovery. When a delegation tree's coordinating context is restored
// (the root is resumed, or a crashed replica's trees are reattached),
// the orphaned non-terminal descendant nodes must be brought back onto
// fresh pods leaves-first, bounded by the per-level and whole-tree
// recovery windows.
//
// The traversal engine lives in pkg/delegation/recovery: it groups
// nodes by depth, recovers each level deepest-first, and applies the
// `maxLevelRecoverySeconds` / `maxTreeRecoverySeconds` budgets. That
// package is pure and never touches the store, so this package is the
// production driver the §8.10 audit found missing — it enumerates the
// tree's failed nodes from the SessionStore, builds the recovery.Node
// set (depth from the immutable DelegationDepth, the per-node resume
// window from ResumeEligibleUntil), invokes the traversal with a
// concrete reattach action, marks the nodes that exhaust their budget
// terminally failed, and emits the §16.1 tree-recovery metrics.
//
// spec: §8.10 (bottom-up recovery, level/tree timeouts,
// maxResumeWindowSeconds interaction).
package treerecovery

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/recovery"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// TreeLister enumerates every session row in a delegation tree (the
// root and all descendants), the §8.9 single-shard tree query.
type TreeLister interface {
	ListByRoot(ctx context.Context, tenantID, rootSessionID string) ([]sessionstore.Session, error)
}

// NodeReattacher drives and waits for the restore of one node of a
// delegation tree. StartNode starts the node's restore and returns at once;
// the traversal calls it for every selected node of a level before it waits
// on any of them. ReattachNode waits until the node's restore has an
// outcome: a nil error means the restore committed; a non-nil error fails
// the node, exactly as an elapsed recovery budget does. The context passed
// to ReattachNode carries the node's recovery deadline. spec: §8.10.
type NodeReattacher interface {
	StartNode(ctx context.Context, node sessionstore.Session)
	ReattachNode(ctx context.Context, node sessionstore.Session) error
}

// TerminalMarker transitions an unrecovered node to a terminal state.
// It is called for every node the traversal could not recover — a
// reattach error, an exhausted per-node resume window, or an exhausted
// level/tree budget. spec: §8.10.
type TerminalMarker interface {
	FailNode(ctx context.Context, node sessionstore.Session, reason string)
}

// Metrics records the §16.1 tree-recovery telemetry.
// *gatewaymetrics.Metrics satisfies it.
type Metrics interface {
	ObserveTreeRecoveryDuration(pool, outcome string, seconds float64)
	IncTreeRecoveryTimeout(pool, timeoutType string)
}

// Recovery outcome labels for ObserveTreeRecoveryDuration.
const (
	outcomeFullSuccess    = "full_success"
	outcomePartialFailure = "partial_failure"
	outcomeTotalTimeout   = "total_timeout"
)

// recovery.Recover failure reasons the traversal returns. The driver
// maps the two budget-exhaustion reasons onto the §16.1
// timeout_type label.
const (
	reasonLevelTimeout = recovery.ReasonLevelTimeout
	reasonTreeTimeout  = recovery.ReasonTreeTimeout
)

// Config builds an Orchestrator. Lister, Reattacher, and Terminal are
// required; Metrics, Recoverable, and Clock are optional.
type Config struct {
	Lister     TreeLister
	Reattacher NodeReattacher
	Terminal   TerminalMarker
	Metrics    Metrics
	// Recoverable selects which non-terminal descendant nodes need
	// §8.10 recovery. The gateway selects a node by its row state, so a
	// root that resumed for its own reasons does not drive descendants
	// still running on their pods. When nil, every non-terminal
	// descendant is treated as recoverable.
	Recoverable func(sessionstore.Session) bool
	// LevelTimeout and TreeTimeout are the §8.10
	// maxLevelRecoverySeconds / maxTreeRecoverySeconds budgets. A
	// non-positive value selects the recovery-package default.
	LevelTimeout time.Duration
	TreeTimeout  time.Duration
	// Clock supplies the current time. Nil defaults to time.Now.
	Clock func() time.Time
}

// Orchestrator runs the §8.10 bottom-up recovery for one delegation
// tree per RecoverTree call.
type Orchestrator struct {
	lister       TreeLister
	reattacher   NodeReattacher
	terminal     TerminalMarker
	metrics      Metrics
	recoverable  func(sessionstore.Session) bool
	levelTimeout time.Duration
	treeTimeout  time.Duration
	clock        func() time.Time
}

// New returns an Orchestrator for cfg. It returns nil when a required
// seam is missing, so a partially-wired gateway degrades to a no-op
// rather than panicking on the first resume.
func New(cfg Config) *Orchestrator {
	if cfg.Lister == nil || cfg.Reattacher == nil || cfg.Terminal == nil {
		return nil
	}
	clock := cfg.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Orchestrator{
		lister:       cfg.Lister,
		reattacher:   cfg.Reattacher,
		terminal:     cfg.Terminal,
		metrics:      cfg.Metrics,
		recoverable:  cfg.Recoverable,
		levelTimeout: cfg.LevelTimeout,
		treeTimeout:  cfg.TreeTimeout,
		clock:        clock,
	}
}

// Summary reports a RecoverTree pass.
type Summary struct {
	// Recoverable is the number of nodes the traversal attempted.
	Recoverable int
	// Recovered is the number reattached successfully.
	Recovered int
	// Failed is the number marked terminally failed (Recoverable -
	// Recovered).
	Failed int
}

// RecoverTree runs the §8.10 bottom-up recovery for the tree rooted at
// rootID. It enumerates the tree, selects the orphaned non-terminal
// descendants, recovers them leaves-first under the level/tree budgets,
// marks the unrecovered ones terminally failed, and records the §16.1
// metrics. A tree with no recoverable node is a no-op and emits no
// metric. The traversal is bounded by the configured budgets, so the
// call returns within maxTreeRecoverySeconds.
//
// spec: §8.10.
func (o *Orchestrator) RecoverTree(ctx context.Context, tenantID, rootID string) (Summary, error) {
	if o == nil || rootID == "" {
		return Summary{}, nil
	}
	rows, err := o.lister.ListByRoot(ctx, tenantID, rootID)
	if err != nil {
		return Summary{}, err
	}

	// Locate the root row for the pool label and collect the
	// recoverable descendant nodes keyed by session id so the per-node
	// reattach / terminal callbacks can recover the full row.
	var pool string
	byID := make(map[string]sessionstore.Session, len(rows))
	nodes := make([]recovery.Node, 0, len(rows))
	now := o.clock()
	for _, row := range rows {
		if row.ID == rootID {
			pool = poolLabel(row)
			continue
		}
		if session.IsTerminal(row.State) {
			continue
		}
		if o.recoverable != nil && !o.recoverable(row) {
			continue
		}
		byID[row.ID] = row
		nodes = append(nodes, recovery.Node{
			SessionID:    row.ID,
			Depth:        int(row.DelegationDepth),
			ResumeWindow: remainingResumeWindow(row, now),
		})
	}
	if len(nodes) == 0 {
		return Summary{}, nil
	}

	start := o.clock()
	results := recovery.Recover(ctx, nodes, func(n recovery.Node) {
		o.reattacher.StartNode(ctx, byID[n.SessionID])
	}, func(nctx context.Context, n recovery.Node) error {
		return o.reattacher.ReattachNode(nctx, byID[n.SessionID])
	}, recovery.Config{
		LevelTimeout: o.levelTimeout,
		TreeTimeout:  o.treeTimeout,
		Now:          o.clock,
	})

	summary := Summary{Recoverable: len(nodes)}
	treeTimedOut := false
	for _, res := range results {
		if res.Outcome == recovery.OutcomeRecovered {
			summary.Recovered++
			continue
		}
		summary.Failed++
		row := byID[res.Node.SessionID]
		o.terminal.FailNode(ctx, row, res.Reason)
		switch res.Reason {
		case reasonLevelTimeout:
			o.incTimeout(pool, "level")
		case reasonTreeTimeout:
			treeTimedOut = true
			o.incTimeout(pool, "tree")
		}
	}

	o.observeDuration(pool, outcomeFor(summary, treeTimedOut), o.clock().Sub(start).Seconds())
	return summary, nil
}

// outcomeFor maps a pass summary to the §16.1 outcome label.
// A tree-budget exhaustion is the strongest signal (total_timeout); any
// other partial loss is partial_failure; a clean sweep is full_success.
func outcomeFor(s Summary, treeTimedOut bool) string {
	switch {
	case treeTimedOut:
		return outcomeTotalTimeout
	case s.Failed > 0:
		return outcomePartialFailure
	default:
		return outcomeFullSuccess
	}
}

// poolLabel returns the metric pool label for a root row, preferring
// the resolved §5 warm-pool ref over the client-requested target.
func poolLabel(root sessionstore.Session) string {
	if root.PoolRef != "" {
		return root.PoolRef
	}
	if root.Pool != "" {
		return root.Pool
	}
	return "unknown"
}

// remainingResumeWindow is the §8.10 per-node
// maxResumeWindowSeconds budget that runs concurrently with tree
// recovery: the wall-clock left until the node's ResumeEligibleUntil. A
// zero or past deadline yields zero, which the traversal reads as "no
// individual bound" — a node already past its resume window is caught by
// the level/tree budgets instead (and a node with no recorded window is
// likewise governed by them).
func remainingResumeWindow(row sessionstore.Session, now time.Time) time.Duration {
	if row.ResumeEligibleUntil.IsZero() {
		return 0
	}
	d := row.ResumeEligibleUntil.Sub(now)
	if d <= 0 {
		return 0
	}
	return d
}

func (o *Orchestrator) incTimeout(pool, kind string) {
	if o.metrics != nil {
		o.metrics.IncTreeRecoveryTimeout(pool, kind)
	}
}

func (o *Orchestrator) observeDuration(pool, outcome string, seconds float64) {
	if o.metrics != nil {
		o.metrics.ObserveTreeRecoveryDuration(pool, outcome, seconds)
	}
}
