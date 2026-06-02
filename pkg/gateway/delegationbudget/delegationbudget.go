// SPDX-License-Identifier: MIT

// Package delegationbudget is the §11.2 / §12.4 durability layer for the
// §8.2 delegation tree budget counters. The fast path lives in Redis
// (pkg/gateway/treebudget) under the tree-scoped {root_session_id}:dlg:*
// keys; those counters are volatile, so this package adds the two
// obligations the spec attaches to them:
//
//   - the periodic Postgres checkpoint (§11.2 line 44): every
//     quotaSyncIntervalSeconds the gateway persists each active tree's
//     tree_size, token_budget_consumed, and tree_memory_bytes to the
//     delegation_tree_budget table, and
//   - the two-source reconstruction on Redis recovery (§11.2 line 48,
//     §12.4 line 218): the gateway restores each tree's counters to
//     max(postgres_checkpoint, live_estimate) before resuming delegation
//     operations, moving a tree whose state cannot be reconstructed with
//     confidence to awaiting_client_action with reason
//     BUDGET_STATE_UNRECOVERABLE.
//
// The Reconciler ties the seams together; production wires the adapters
// in adapters.go over the SessionStore and *treebudget.Reserver. The
// in-flight reconciliation window (§12.4 line 218 "new delegate_task
// requests are rejected during the reconciliation window") is covered at
// the coarse level by treebudget's fail-closed posture during the Redis
// outage that precedes recovery; reconstruction completes within one
// probe tick.
//
// spec: §11.2 lines 29, 44, 48; §12.4 lines 193, 218.
package delegationbudget

import (
	"context"
	"time"
)

// BudgetStateUnrecoverableReason is the §11.2 line 48 reason code stamped
// on a tree root that is moved to awaiting_client_action because its
// budget state could not be reconstructed. It is already classified
// retryable in pkg/gateway/errorclassify.
//
// spec: §11.2 line 48; §15.1 line 1027.
const BudgetStateUnrecoverableReason = "BUDGET_STATE_UNRECOVERABLE"

// DefaultNodeMemoryFootprintBytes is the §11.2 line 48 per-node in-memory
// footprint estimate (`nodeMemoryFootprintBytes`, default 12288 / 12 KB)
// used to derive liveMemoryBytes during reconstruction. It matches
// treebudget.PerNodeMemoryBytes; the gateway Helm value
// delegationNodeMemoryFootprintBytes overrides it.
//
// spec: §11.2 line 48.
const DefaultNodeMemoryFootprintBytes int64 = 12 * 1024

// Reconstruction outcome labels for lenny_delegation_budget_reconstruction_total.
//
// spec: §11.2 line 48; §16.1.
const (
	OutcomeSuccess       = "success"
	OutcomeIrrecoverable = "irrecoverable"
)

// Checkpoint is one delegation_tree_budget row: the tree-wide structural
// budget for a single delegation tree, identified tree-wide by its root
// session. CheckpointAt is populated on read from the server-side
// clock_timestamp() so the reconstruction can measure the checkpoint's
// age against the §11.2 line 48 staleness threshold.
//
// spec: §11.2 line 29.
type Checkpoint struct {
	TenantID            string
	RootSessionID       string
	TreeSize            int64
	TokenBudgetConsumed int64
	TreeMemoryBytes     int64
	CheckpointAt        time.Time
}

// Store persists and reads the delegation_tree_budget checkpoint rows.
// The Postgres implementation lives in the pgstore subpackage.
type Store interface {
	// Write upserts the counter columns and checkpoint_at for each row,
	// leaving the §8.6 extension_denied / cool_off_expiry columns
	// untouched on conflict. Rows are grouped by tenant so each tenant's
	// writes run under its own RLS transaction.
	Write(ctx context.Context, rows []Checkpoint) error

	// ListActive returns every checkpoint row across all tenants, for the
	// recovery reconstruction. It is a platform-scoped read.
	ListActive(ctx context.Context) ([]Checkpoint, error)

	// DeleteByTenant removes every checkpoint row for tenantID and returns
	// the count deleted. It is the §12.8 Phase-4 tenant-deletion erasure
	// adapter; a tenant with no rows is a no-op returning (0, nil).
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// TreeRef identifies one active delegation tree to checkpoint.
type TreeRef struct {
	TenantID      string
	RootSessionID string
}

// TreeLister enumerates the active delegation trees whose Redis counters
// the periodic checkpoint persists.
type TreeLister interface {
	ListActiveTrees(ctx context.Context) ([]TreeRef, error)
}

// LiveTree is the §11.2 line 48 reconstruction's live-side estimate for
// one tree, derived from the SessionStore (which survives a replica
// loss).
type LiveTree struct {
	// RootExists reports whether the tree's sessions could be enumerated
	// from the SessionStore. False means live pod enumeration is not
	// possible (the §11.2 line 48 "coordinating replica lost" half of the
	// irrecoverability test).
	RootExists bool
	// NodeCount is the number of currently-alive (non-terminal) nodes in
	// the tree, including the root — the reconstructed treeSize and the
	// multiplier for liveMemoryBytes.
	NodeCount int64
	// TokenAllocations is the sum of allocated token budgets across the
	// alive nodes — the reconstructed tokenBudgetConsumed lower bound.
	TokenAllocations int64
}

// LiveEnumerator derives a tree's LiveTree estimate from the
// SessionStore.
type LiveEnumerator interface {
	LiveTree(ctx context.Context, tenantID, rootSessionID string) (LiveTree, error)
}

// SessionMarker moves a tree root to awaiting_client_action when its
// budget state is irrecoverable.
type SessionMarker interface {
	MarkBudgetUnrecoverable(ctx context.Context, tenantID, rootSessionID, reason string) error
}

// CounterStore reads and restores the tree-wide Redis counters.
// *treebudget.Reserver satisfies it via Snapshot / Restore.
type CounterStore interface {
	Snapshot(ctx context.Context, rootSessionID string) (TreeCounters, error)
	Restore(ctx context.Context, rootSessionID string, c TreeCounters) error
}

// TreeCounters mirrors treebudget.TreeCounters at this package boundary
// so the Reconciler does not import treebudget directly (the adapter in
// adapters.go bridges the two). The field set is identical.
type TreeCounters struct {
	TreeSize   int64
	Tokens     int64
	TreeMemory int64
}

// MetricEmitter records each reconstruction event by outcome.
type MetricEmitter interface {
	IncDelegationBudgetReconstruction(outcome string)
}

// maxInt64 returns the larger of a and b — the §11.2 MAX rule applied per
// axis during reconstruction.
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
