// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// treeConfig describes one test tree: the root lease values the tree's
// base limits are read from, and the deployment-level extension
// configuration the store-backed source serves it with.
type treeConfig struct {
	TenantID string

	CurrentTokenBudget      int64
	CurrentMaxAgeSeconds    int64
	CurrentChildren         int64
	CurrentParallelChildren int64
	CurrentTreeSize         int64

	DeploymentBase int64
	DeploymentMax  int64

	EffectiveMaxAgeSeconds       int64
	EffectiveMaxChildren         int64
	EffectiveMaxParallelChildren int64
	EffectiveMaxTreeSize         int64
	EffectiveMaxFileExportFiles  int64
	EffectiveMaxFileExportBytes  int64

	RejectionCoolOff time.Duration
	ApprovalMode     leasecontrol.ApprovalMode
	SuccessCoolOff   time.Duration
	AutoMaxPerMinute int
}

// parentLease is the granted lease a test parent row carries, which the
// store-backed source reads as a delegated child's parent ceiling.
type parentLease struct {
	TokenCeiling            int64
	MaxAgeCeiling           int64
	ChildrenCeiling         int64
	ParallelChildrenCeiling int64
	TreeSizeCeiling         int64
}

// memBudget is the test fixture over a StoreBudgetSource: an in-memory
// session reader plus a MemoryDenialStore. Its helpers write session rows
// (seedRoot for a root, seedChild for a delegated child,
// seedParentLease for the child's parent row), so every read the source
// makes is a store read rather than a registration.
type memBudget struct {
	mu      sync.Mutex
	rows    map[string]sessionstore.Session
	cfg     leasecontrol.ExtensionConfig
	clock   func() time.Time
	denials leasecontrol.DenialStore
}

// newMemBudget returns an empty fixture on the wall clock.
func newMemBudget() *memBudget {
	b := &memBudget{rows: map[string]sessionstore.Session{}, clock: func() time.Time { return time.Now().UTC() }}
	b.denials = leasecontrol.NewMemoryDenialStore(func() time.Time { return b.now() })
	return b
}

func (b *memBudget) now() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clock()
}

// WithClock sets the clock the MemoryDenialStore compares cool-offs on.
func (b *memBudget) WithClock(clock func() time.Time) *memBudget {
	b.mu.Lock()
	b.clock = clock
	b.mu.Unlock()
	return b
}

// WithDenialStore replaces the MemoryDenialStore.
func (b *memBudget) WithDenialStore(ds leasecontrol.DenialStore) *memBudget {
	b.mu.Lock()
	b.denials = ds
	b.mu.Unlock()
	return b
}

// Get satisfies leasecontrol.SessionReader.
func (b *memBudget) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.rows[id]
	if !ok || r.TenantID != tenantID {
		return sessionstore.Session{}, sessionstore.ErrNotFound
	}
	return r, nil
}

// seedRoot writes the root row and sets the extension configuration.
func (b *memBudget) seedRoot(root string, cfg treeConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rows[root] = sessionstore.Session{
		ID: root, TenantID: cfg.TenantID, RootSessionID: root, State: session.StateRunning,
		DelegationLease: &sessionstore.DelegationLease{
			MaxTokenBudget:      cfg.CurrentTokenBudget,
			PerChildMaxAge:      int(cfg.CurrentMaxAgeSeconds),
			MaxChildrenTotal:    int(cfg.CurrentChildren),
			MaxParallelChildren: int(cfg.CurrentParallelChildren),
			MaxTreeSize:         int(cfg.CurrentTreeSize),
		},
	}
	b.cfg = leasecontrol.ExtensionConfig{
		DeploymentBudget:    cfg.DeploymentBase,
		DeploymentMaxBudget: cfg.DeploymentMax,
		DimensionCeilings: leasecontrol.Dimensions{
			Seconds:          cfg.EffectiveMaxAgeSeconds,
			Children:         cfg.EffectiveMaxChildren,
			ParallelChildren: cfg.EffectiveMaxParallelChildren,
			TreeSize:         cfg.EffectiveMaxTreeSize,
			FileExportFiles:  cfg.EffectiveMaxFileExportFiles,
			FileExportBytes:  cfg.EffectiveMaxFileExportBytes,
		},
		ApprovalMode:     cfg.ApprovalMode,
		SuccessCoolOff:   cfg.SuccessCoolOff,
		RejectionCoolOff: cfg.RejectionCoolOff,
		AutoMaxPerMinute: cfg.AutoMaxPerMinute,
	}
}

// parentOf names the synthetic parent row seedChild writes for a child.
func parentOf(id string) string { return "parent-of-" + id }

// seedChild writes a delegated child row (depth 1) under root, with a
// parent row that carries no lease until seedParentLease gives it one.
func (b *memBudget) seedChild(id, root, tenantID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rows[parentOf(id)] = sessionstore.Session{
		ID: parentOf(id), TenantID: tenantID, RootSessionID: root, State: session.StateRunning,
	}
	b.rows[id] = sessionstore.Session{
		ID: id, TenantID: tenantID, RootSessionID: root, ParentSessionID: parentOf(id),
		DelegationDepth: 1, State: session.StateRunning,
	}
}

// seedParentLease gives the child's parent row the granted lease l.
func (b *memBudget) seedParentLease(id string, l parentLease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.rows[parentOf(id)]
	p.DelegationLease = &sessionstore.DelegationLease{
		MaxTokenBudget:      l.TokenCeiling,
		PerChildMaxAge:      int(l.MaxAgeCeiling),
		MaxChildrenTotal:    int(l.ChildrenCeiling),
		MaxParallelChildren: int(l.ParallelChildrenCeiling),
		MaxTreeSize:         int(l.TreeSizeCeiling),
	}
	b.rows[parentOf(id)] = p
}

// SetState moves a row to state.
func (b *memBudget) SetState(id string, state session.State) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.rows[id]
	r.State = state
	b.rows[id] = r
}

// source builds the StoreBudgetSource over the fixture's current state.
func (b *memBudget) source() *leasecontrol.StoreBudgetSource {
	b.mu.Lock()
	cfg, ds := b.cfg, b.denials
	b.mu.Unlock()
	src, err := leasecontrol.NewStoreBudgetSource(b, ds, cfg)
	if err != nil {
		panic(err)
	}
	return src
}

// tenantOf returns the tenant of the row id, empty when unknown.
func (b *memBudget) tenantOf(id string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.rows[id].TenantID
}

// MarkDenied denies the tree with the configured rejection cool-off.
func (b *memBudget) MarkDenied(root string) {
	_ = b.source().Deny(context.Background(), b.tenantOf(root), root, root)
}

// ClearDenial clears the tree's denial.
func (b *memBudget) ClearDenial(root string) {
	_ = b.source().ClearSubtreeDenial(context.Background(), b.tenantOf(root), root, root)
}

func (b *memBudget) TreeBudget(ctx context.Context, tenantID, sessionID string) (leasecontrol.TreeBudget, error) {
	return b.source().TreeBudget(ctx, tenantID, sessionID)
}

func (b *memBudget) ApplyGrant(ctx context.Context, tenantID, root, requesting string, granted leasecontrol.Dimensions) (leasecontrol.AppliedGrant, error) {
	return b.source().ApplyGrant(ctx, tenantID, root, requesting, granted)
}

func (b *memBudget) RejectionCoolOff(ctx context.Context, tenantID, root string) time.Duration {
	return b.source().RejectionCoolOff(ctx, tenantID, root)
}

func (b *memBudget) Deny(ctx context.Context, tenantID, root, requesting string) error {
	return b.source().Deny(ctx, tenantID, root, requesting)
}

func (b *memBudget) ApprovalMode(ctx context.Context, tenantID, root string) leasecontrol.ApprovalMode {
	return b.source().ApprovalMode(ctx, tenantID, root)
}

func (b *memBudget) SuccessCoolOff(ctx context.Context, tenantID, root string) time.Duration {
	return b.source().SuccessCoolOff(ctx, tenantID, root)
}

func (b *memBudget) AutoExtensionsPerMinute(ctx context.Context, tenantID, root string) int {
	return b.source().AutoExtensionsPerMinute(ctx, tenantID, root)
}

func (b *memBudget) ClearSubtreeDenial(ctx context.Context, tenantID, root, sub string) error {
	return b.source().ClearSubtreeDenial(ctx, tenantID, root, sub)
}
