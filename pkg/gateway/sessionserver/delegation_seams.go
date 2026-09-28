// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
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

// LeaseTreeRegistrar registers a root session's §8.6 lease-extension
// budget tree so a later in-process budget-exhaustion extension (the
// gateway LLM Proxy's ExtendForBudget trigger) from the root session or
// its delegated descendants resolves the tree instead of failing
// ErrSessionNotFound. *leasecontrol.MemoryBudgetSource satisfies it.
// F-15.3.5.
// spec: §8.6
type LeaseTreeRegistrar interface {
	RegisterTree(rootSessionID string, cfg leasecontrol.TreeConfig)
}

// LeaseExtensionDefaults is the §8.6 deployment-level lease-extension
// configuration (Helm `leaseExtension.defaults` / `leaseExtension.max`)
// the gateway registers each root tree with. The token dimension's
// effective ceiling is resolved from DeploymentBudget and
// DeploymentMaxBudget through leaseextension.ResolveEffectiveMax; the
// remaining §8.6 dimensions have no deployment-level config
// surface and are registered without extension headroom. F-15.3.5.
// spec: §8.6
type LeaseExtensionDefaults struct {
	// DeploymentBudget is the §8.6 deployment-default maxExtendableBudget
	// (Helm leaseExtension.defaults.maxExtendableBudget). Zero registers a
	// tree with no token-extension headroom.
	DeploymentBudget int64
	// DeploymentMaxBudget is the §8.6 absolute ceiling no override may
	// exceed (Helm leaseExtension.max.maxExtendableBudget).
	DeploymentMaxBudget int64
	// ApprovalMode is the §8.6 deployment-default extensionApproval mode.
	// Unspecified resolves to leasecontrol.DefaultApprovalMode.
	ApprovalMode leasecontrol.ApprovalMode
	// SuccessCoolOff is the §8.6 coolOffSeconds post-approval
	// window. Zero applies leasecontrol.DefaultSuccessCoolOff.
	SuccessCoolOff time.Duration
	// RejectionCoolOff is the §8.6 rejectionCoolOffSeconds. Zero
	// applies leasecontrol.DefaultRejectionCoolOff.
	RejectionCoolOff time.Duration
	// AutoMaxPerMinute is the §8.6 autoModeRateLimit
	// maxAutoExtensionsPerMinute. Zero means no limit.
	AutoMaxPerMinute int
}

// registerLeaseTree registers a newly created root session with the
// §8.6 lease-extension budget source. It is a no-op when no registrar
// is wired or the row is a delegated child (children are registered by
// the delegation Service, keyed to their root's tree). The token
// dimension's current value is seeded from a granted DelegationLease
// when the row carries one; the deployment-level ceiling comes from the
// configured defaults. F-15.3.5.
// spec: §8.6
func (s *Server) registerLeaseTree(row sessionstore.Session) {
	if s.leaseRegistrar == nil || row.ParentSessionID != "" {
		return
	}
	cfg := leasecontrol.TreeConfig{
		TenantID:         row.TenantID,
		DeploymentBase:   s.leaseExtDefaults.DeploymentBudget,
		DeploymentMax:    s.leaseExtDefaults.DeploymentMaxBudget,
		ApprovalMode:     s.leaseExtDefaults.ApprovalMode,
		SuccessCoolOff:   s.leaseExtDefaults.SuccessCoolOff,
		RejectionCoolOff: s.leaseExtDefaults.RejectionCoolOff,
		AutoMaxPerMinute: s.leaseExtDefaults.AutoMaxPerMinute,
	}
	if l := row.DelegationLease; l != nil {
		cfg.CurrentTokenBudget = l.MaxTokenBudget
		cfg.CurrentChildren = int64(l.MaxChildrenTotal)
		cfg.CurrentParallelChildren = int64(l.MaxParallelChildren)
		cfg.CurrentTreeSize = int64(l.MaxTreeSize)
		cfg.CurrentMaxAgeSeconds = int64(l.PerChildMaxAge)
	}
	s.leaseRegistrar.RegisterTree(row.ID, cfg)
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
