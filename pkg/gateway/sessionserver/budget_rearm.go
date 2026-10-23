// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// BudgetKeyRearmer re-arms the TTL of the delegation budget keys of the tree
// rooted at rootSessionID: the tree-wide keys and the per-parent keys of each
// parent in parentIDs. *treebudget.Reserver satisfies it.
// spec: §8.3 (Delegation Policy and Lease)
type BudgetKeyRearmer interface {
	RearmTree(ctx context.Context, rootSessionID string, parentIDs []string) error
}

// rearmBudgetKeys re-arms the delegation budget keys of the tree row belongs
// to, after a committed transition into or out of `suspended`, so a
// suspended session's keys outlive its suspended lifetime. The tree root is
// row.RootSessionID, or row.ID for a root. It lists the tree's rows and
// re-arms only when at least one row names a delegating parent, because a
// tree with no delegation has no budget keys; the re-arm names each distinct
// parent. A listing or re-arm error is logged at WARN and leaves the
// committed transition in place. Never call it inside an Update mutator.
//
// spec: §8.3 (Delegation Policy and Lease)
func (s *Server) rearmBudgetKeys(ctx context.Context, row sessionstore.Session) {
	if s.budgetRearmer == nil {
		return
	}
	root := row.RootSessionID
	if root == "" {
		root = row.ID
	}
	rows, err := s.store.ListByRoot(ctx, row.TenantID, root)
	if err != nil {
		slog.Warn("sessionserver: list the delegation tree to re-arm its budget keys",
			"session_id", row.ID, "error", err)
		return
	}
	parents := distinctParents(rows)
	if len(parents) == 0 {
		return
	}
	if err := s.budgetRearmer.RearmTree(ctx, root, parents); err != nil {
		slog.Warn("sessionserver: re-arm the delegation budget keys",
			"session_id", row.ID, "error", err)
	}
}

// distinctParents returns the distinct non-empty ParentSessionID values of
// rows, in first-seen order.
func distinctParents(rows []sessionstore.Session) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, r := range rows {
		if r.ParentSessionID == "" {
			continue
		}
		if _, ok := seen[r.ParentSessionID]; ok {
			continue
		}
		seen[r.ParentSessionID] = struct{}{}
		out = append(out, r.ParentSessionID)
	}
	return out
}
