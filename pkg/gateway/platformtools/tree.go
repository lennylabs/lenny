// SPDX-License-Identifier: MIT

package platformtools

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/resultrollup"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/seamrow"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// BudgetCounters is the tree budget counter surface TreeBudgets serves the
// session core over. *treebudget.Reserver satisfies it.
type BudgetCounters interface {
	Return(ctx context.Context, r treebudget.Reservation) error
	RearmTree(ctx context.Context, rootSessionID string, parentIDs []string) error
	ObserveHighWatermark(ctx context.Context, rootSessionID string) (value int64, found bool, err error)
}

// TreeBudgets serves the tree budget operations over the Redis-backed tree
// budget counters.
type TreeBudgets struct {
	r BudgetCounters
}

// NewTreeBudgets returns the tree budget operations over r, or nil when r
// is nil, so a gateway without Redis hands the session core no tree
// budgets and the core skips the budget return, the re-arm, and the
// high-watermark read.
func NewTreeBudgets(r *treebudget.Reserver) seam.TreeBudgets {
	if r == nil {
		return nil
	}
	return TreeBudgets{r: r}
}

// TreeBudgetsOver returns the tree budget operations over any counters.
func TreeBudgetsOver(c BudgetCounters) TreeBudgets { return TreeBudgets{r: c} }

// Reservation is the tree budget reservation a budget return gives back.
func Reservation(r seam.BudgetReturn) treebudget.Reservation {
	return treebudget.Reservation{
		RootSessionID:         r.RootSessionID,
		ParentSessionID:       r.ParentSessionID,
		TreeSizeCap:           r.TreeSizeCap,
		TreeSizeDelta:         r.TreeSizeDelta,
		TreeMemoryCap:         r.TreeMemoryCap,
		TreeMemoryDelta:       r.TreeMemoryDelta,
		ParallelChildrenCap:   r.ParallelChildrenCap,
		ParallelChildrenDelta: r.ParallelChildrenDelta,
		ChildrenTotalCap:      r.ChildrenTotalCap,
		ChildrenTotalDelta:    r.ChildrenTotalDelta,
		TokenCap:              r.TokenCap,
		TokenDelta:            r.TokenDelta,
	}
}

// ReturnBudget returns a node's reservation to its tree.
//
// spec: GWSEAM-ReturnBudget
func (b TreeBudgets) ReturnBudget(ctx context.Context, r seam.BudgetReturn) error {
	return b.r.Return(ctx, Reservation(r))
}

// RearmBudget restores the expiry of a tree's budget keys.
//
// spec: GWSEAM-RearmBudget
func (b TreeBudgets) RearmBudget(ctx context.Context, rootSessionID string, parentSessionIDs []string) error {
	return b.r.RearmTree(ctx, rootSessionID, parentSessionIDs)
}

// ObserveHighWatermark reads and clears a tree's parallel-children
// high-watermark.
//
// spec: GWSEAM-ObserveHighWatermark
func (b TreeBudgets) ObserveHighWatermark(ctx context.Context, rootSessionID string) (int64, bool, error) {
	return b.r.ObserveHighWatermark(ctx, rootSessionID)
}

// TreeArchive serves the tree archive operations over the archive store.
type TreeArchive struct {
	a treearchive.Store
}

// NewTreeArchive returns the tree archive operations over a, or nil when a
// is nil, so the session core archives nothing without an archive store.
func NewTreeArchive(a treearchive.Store) seam.TreeArchive {
	if a == nil {
		return nil
	}
	return TreeArchive{a: a}
}

// ArchiveNode records a settled node in the archive.
//
// spec: GWSEAM-ArchiveNode
func (t TreeArchive) ArchiveNode(ctx context.Context, n seam.ArchivedNode) error {
	return t.a.Archive(ctx, archiveNode(n))
}

// ArchivedNodes returns a tree's archived nodes in settle order.
//
// spec: GWSEAM-ArchivedNodes
func (t TreeArchive) ArchivedNodes(ctx context.Context, tenantID, rootSessionID string) ([]seam.ArchivedNode, error) {
	nodes, err := t.a.Replay(ctx, tenantID, rootSessionID)
	if err != nil {
		return nil, err
	}
	out := make([]seam.ArchivedNode, len(nodes))
	for i, n := range nodes {
		out[i] = seamNode(n)
	}
	return out, nil
}

// ArchivedNodeFor returns one session's archived node. A node the archive
// does not hold matches seam.ErrSessionNotFound.
//
// spec: GWSEAM-ArchivedNodeFor
func (t TreeArchive) ArchivedNodeFor(ctx context.Context, tenantID, nodeSessionID string) (seam.ArchivedNode, error) {
	n, err := t.a.GetByNode(ctx, tenantID, nodeSessionID)
	if errors.Is(err, treearchive.ErrNotFound) {
		return seam.ArchivedNode{}, errors.Join(seam.ErrSessionNotFound, err)
	}
	if err != nil {
		return seam.ArchivedNode{}, err
	}
	return seamNode(n), nil
}

func archiveNode(n seam.ArchivedNode) treearchive.ArchivedNode {
	return treearchive.ArchivedNode{
		TenantID:        n.TenantID,
		RootSessionID:   n.RootSessionID,
		NodeSessionID:   n.NodeSessionID,
		ParentSessionID: n.ParentSessionID,
		State:           n.State,
		Result:          n.Result,
		SettledAt:       n.SettledAt,
		ArchivedAt:      n.ArchivedAt,
		CompletionSeq:   n.CompletionSeq,
	}
}

func seamNode(n treearchive.ArchivedNode) seam.ArchivedNode {
	return seam.ArchivedNode{
		TenantID:        n.TenantID,
		RootSessionID:   n.RootSessionID,
		NodeSessionID:   n.NodeSessionID,
		ParentSessionID: n.ParentSessionID,
		State:           n.State,
		Result:          n.Result,
		SettledAt:       n.SettledAt,
		ArchivedAt:      n.ArchivedAt,
		CompletionSeq:   n.CompletionSeq,
	}
}

// TreeRollups serves the usage rollup operations over the rollup builder.
type TreeRollups struct {
	b *resultrollup.Builder
}

// NewTreeRollups returns the usage rollup operations over b, or nil when b
// is nil, so the session core stamps no usage without a builder.
func NewTreeRollups(b *resultrollup.Builder) seam.TreeRollups {
	if b == nil {
		return nil
	}
	return TreeRollups{b: b}
}

// NodeUsage returns one node's usage.
//
// spec: GWSEAM-NodeUsage
func (t TreeRollups) NodeUsage(ctx context.Context, node seam.Session) *sessionrecord.Usage {
	return t.b.Usage(ctx, seamrow.Row(node))
}

// TreeUsage returns a subtree's rolled-up usage.
//
// spec: GWSEAM-TreeUsage
func (t TreeRollups) TreeUsage(ctx context.Context, node seam.Session, root *sessionrecord.Usage) *sessionrecord.TreeUsage {
	return t.b.TreeUsage(ctx, seamrow.Row(node), root)
}

// SubtreeTokens returns the tokens a session's subtree used.
//
// spec: GWSEAM-SubtreeTokens
func (t TreeRollups) SubtreeTokens(ctx context.Context, node seam.Session) (int64, error) {
	return t.b.SubtreeTokens(ctx, seamrow.Row(node))
}

// TreeOrphans serves the orphan classification of the orphan cleanup.
type TreeOrphans struct{}

// Orphaned reports whether node's chain of ancestors among tree is
// terminal or broken, by the orphan cleanup's own rule.
//
// spec: GWSEAM-Orphaned
func (TreeOrphans) Orphaned(node seam.Session, tree map[string]seam.Session) bool {
	return orphancleanup.OrphanedRecord(node, tree)
}
