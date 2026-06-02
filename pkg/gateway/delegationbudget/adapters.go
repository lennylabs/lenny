// SPDX-License-Identifier: MIT

package delegationbudget

import (
	"context"
	"errors"

	sessionapi "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/treebudget"
)

// TenantLister enumerates the tenant ids whose active trees are
// checkpointed. cmd/lenny-gateway wires the same tenantstore-backed
// lister it uses for the storage-quota rehydration.
type TenantLister func(ctx context.Context) ([]string, error)

// CounterAdapter bridges a *treebudget.Reserver to the CounterStore seam
// so the Reconciler reads and restores the tree-wide Redis counters
// without importing treebudget's concrete type signatures.
type CounterAdapter struct {
	Reserver *treebudget.Reserver
}

// Snapshot reads the tree-wide counters for rootSessionID.
func (a CounterAdapter) Snapshot(ctx context.Context, rootSessionID string) (TreeCounters, error) {
	c, err := a.Reserver.Snapshot(ctx, rootSessionID)
	if err != nil {
		return TreeCounters{}, err
	}
	return TreeCounters{TreeSize: c.TreeSize, Tokens: c.Tokens, TreeMemory: c.TreeMemory}, nil
}

// Restore writes the reconstructed counters back to Redis.
func (a CounterAdapter) Restore(ctx context.Context, rootSessionID string, c TreeCounters) error {
	return a.Reserver.Restore(ctx, rootSessionID, treebudget.TreeCounters{
		TreeSize:   c.TreeSize,
		Tokens:     c.Tokens,
		TreeMemory: c.TreeMemory,
	})
}

var _ CounterStore = CounterAdapter{}

// SessionTreeLister enumerates active delegation trees from the
// SessionStore: it iterates the tenants and collects the distinct
// RootSessionID of every non-terminal session. A standalone session
// (its own root, no delegation) is included, but the checkpoint skips it
// because its Redis counters are all zero, so the table tracks only
// trees with real budget state.
type SessionTreeLister struct {
	Sessions sessionstore.Store
	Tenants  TenantLister
}

// ListActiveTrees implements TreeLister.
func (l SessionTreeLister) ListActiveTrees(ctx context.Context) ([]TreeRef, error) {
	ids, err := l.Tenants(ctx)
	if err != nil {
		return nil, err
	}
	var refs []TreeRef
	seen := make(map[string]struct{})
	for _, tenantID := range ids {
		rows, err := l.Sessions.List(ctx, tenantID, sessionstore.ListFilter{})
		if err != nil {
			return nil, err
		}
		for _, s := range rows {
			if sessionapi.IsTerminal(s.State) {
				continue
			}
			root := s.RootSessionID
			if root == "" {
				root = s.ID
			}
			key := tenantID + "\x00" + root
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			refs = append(refs, TreeRef{TenantID: tenantID, RootSessionID: root})
		}
	}
	return refs, nil
}

var _ TreeLister = SessionTreeLister{}

// SessionEnumerator derives a tree's §11.2 line 48 live estimate from the
// SessionStore's single-shard tree projection. The SessionStore is
// Postgres-authoritative and survives a gateway replica loss, so it can
// enumerate live descendants even when the coordinating replica is gone;
// RootExists is false only when the tree's rows cannot be read.
type SessionEnumerator struct {
	Sessions sessionstore.Store
}

// LiveTree implements LiveEnumerator. It counts non-terminal nodes
// (alive descendants, including the root) and sums their granted token
// budgets. A tree whose rows are absent returns RootExists=false, which
// the Reconciler treats as "live enumeration not possible".
func (e SessionEnumerator) LiveTree(ctx context.Context, tenantID, rootSessionID string) (LiveTree, error) {
	rows, err := e.Sessions.ListByRoot(ctx, tenantID, rootSessionID)
	if err != nil {
		return LiveTree{}, err
	}
	if len(rows) == 0 {
		return LiveTree{RootExists: false}, nil
	}
	lt := LiveTree{RootExists: true}
	for _, s := range rows {
		if sessionapi.IsTerminal(s.State) {
			continue
		}
		lt.NodeCount++
		if s.DelegationLease != nil {
			lt.TokenAllocations += s.DelegationLease.MaxTokenBudget
		}
	}
	return lt, nil
}

var _ LiveEnumerator = SessionEnumerator{}

// SessionUnrecoverableMarker moves a tree root to awaiting_client_action
// when its budget state is irrecoverable. The transition is idempotent:
// a root already in a terminal state or already awaiting client action is
// left unchanged, and a root that no longer exists is a no-op (nothing to
// pause).
type SessionUnrecoverableMarker struct {
	Sessions sessionstore.Store
}

// MarkBudgetUnrecoverable implements SessionMarker.
func (m SessionUnrecoverableMarker) MarkBudgetUnrecoverable(ctx context.Context, tenantID, rootSessionID, reason string) error {
	_, err := m.Sessions.Update(ctx, tenantID, rootSessionID, func(s *sessionstore.Session) error {
		if sessionapi.IsTerminal(s.State) || s.State == sessionapi.StateAwaitingClientAction {
			return nil
		}
		s.State = sessionapi.StateAwaitingClientAction
		s.FailureReason = reason
		return nil
	})
	if errors.Is(err, sessionstore.ErrNotFound) {
		return nil
	}
	return err
}

var _ SessionMarker = SessionUnrecoverableMarker{}
