// SPDX-License-Identifier: MIT

package platformtools

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treerecovery"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/seamrow"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// RecoveryConfig configures the bottom-up tree recovery.
type RecoveryConfig struct {
	// Metrics records the recovery durations and timeouts. Nil records
	// nothing.
	Metrics treerecovery.Metrics
	// LevelTimeout and TreeTimeout bound one depth level and the whole
	// tree; zero takes the traversal's defaults.
	LevelTimeout time.Duration
	TreeTimeout  time.Duration
	// Clock selects the time source; nil uses the wall clock.
	Clock func() time.Time
}

// TreeRecovery serves GWSEAM-RecoverTree: the bottom-up recovery of a
// delegation tree whose coordinator was lost, which drives each node
// through the tree node operations the session core serves.
type TreeRecovery struct {
	cfg RecoveryConfig
}

// NewTreeRecovery returns the tree recovery with cfg.
func NewTreeRecovery(cfg RecoveryConfig) TreeRecovery {
	return TreeRecovery{cfg: cfg}
}

var _ seam.TreeRecovery = TreeRecovery{}

// RecoverTree recovers the tree rooted at rootSessionID leaves first. It
// selects the tree's resume_pending nodes, starts and waits for each
// through nodes, and fails each node it could not recover through nodes.
//
// spec: GWSEAM-RecoverTree
func (r TreeRecovery) RecoverTree(ctx context.Context, tenantID, rootSessionID string, nodes seam.TreeNodes) (seam.TreeRecoverySummary, error) {
	o := treerecovery.New(treerecovery.Config{
		Lister:       treeNodes{nodes},
		Reattacher:   treeNodes{nodes},
		Terminal:     treeNodes{nodes},
		Metrics:      r.cfg.Metrics,
		Recoverable:  nodeNeedsRecovery,
		LevelTimeout: r.cfg.LevelTimeout,
		TreeTimeout:  r.cfg.TreeTimeout,
		Clock:        r.cfg.Clock,
	})
	sum, err := o.RecoverTree(ctx, tenantID, rootSessionID)
	if err != nil {
		return seam.TreeRecoverySummary{}, err
	}
	return seam.TreeRecoverySummary{Recoverable: sum.Recoverable, Recovered: sum.Recovered, Failed: sum.Failed}, nil
}

// nodeNeedsRecovery selects exactly the resume_pending nodes. A node is
// selected by its row state rather than by its absence from a replica's
// pod registry, because the registry holds only that replica's bindings and
// a node bound on a peer is not orphaned.
//
// spec: §8.10 (Delegation Tree Recovery)
func nodeNeedsRecovery(node sessionstore.Session) bool {
	return node.State == session.StateResumePending
}

// treeNodes adapts the session core's tree node operations to the
// traversal's lister, reattacher, and terminal marker.
type treeNodes struct {
	nodes seam.TreeNodes
}

func (t treeNodes) ListByRoot(ctx context.Context, tenantID, rootSessionID string) ([]sessionstore.Session, error) {
	recs, err := t.nodes.ListTree(ctx, tenantID, rootSessionID)
	if err != nil {
		return nil, err
	}
	rows := make([]sessionstore.Session, len(recs))
	for i, rec := range recs {
		rows[i] = seamrow.Row(rec)
	}
	return rows, nil
}

func (t treeNodes) StartNode(ctx context.Context, node sessionstore.Session) {
	t.nodes.StartNode(ctx, seamrow.Record(node))
}

func (t treeNodes) ReattachNode(ctx context.Context, node sessionstore.Session) error {
	return t.nodes.ReattachNode(ctx, seamrow.Record(node))
}

func (t treeNodes) FailNode(ctx context.Context, node sessionstore.Session, reason string) {
	t.nodes.FailNode(ctx, seamrow.Record(node), reason)
}
