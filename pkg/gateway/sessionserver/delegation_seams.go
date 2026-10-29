// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
)

// TreeBudgetReturner releases the §12.4 delegation tree budget a
// settled child consumed. The §8.2 completed-subtree offload
// decrements the tree's maxTreeMemoryBytes counter when a node is
// archived and the per-parent parallel_children counter when the child
// stops running, so a long-running tree's freed concurrency slot and
// in-memory footprint are returned to the budget. *treebudget.Reserver
// implements it. A nil returner on the Server disables the decrement.
type TreeBudgetReturner interface {
	Return(ctx context.Context, r treebudget.Reservation) error
}

// DelegationHighWatermarkReader reads and clears the §8.3
// per-tree parallel-children high-watermark when a delegation tree
// completes. *treebudget.Reserver implements it. Nil on the Server
// disables the §16.1 high-watermark observation (the in-process
// minimal path with no Redis-backed budget). F-8.9.6.
type DelegationHighWatermarkReader interface {
	ObserveHighWatermark(ctx context.Context, rootSessionID string) (value int64, found bool, err error)
}

// DelegationHighWatermarkObserver records the §8.3 per-tree
// parallel-children high-watermark onto the
// `lenny_delegation_parallel_children_high_watermark` histogram.
// *gatewaymetrics.Metrics implements it. Nil drops the observation.
// F-8.9.6.
type DelegationHighWatermarkObserver interface {
	ObserveDelegationParallelChildrenHighWatermark(pool, tenantID string, value int64)
}
