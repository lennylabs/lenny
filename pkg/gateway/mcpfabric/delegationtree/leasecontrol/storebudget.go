// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/leaseextension"
)

// SessionReader reads one session row under its tenant. The gateway
// sessionstore.Store satisfies it. It returns sessionstore.ErrNotFound
// when no row matches, including a cross-tenant miss.
// spec: §8.6 (Extension state)
type SessionReader interface {
	Get(ctx context.Context, tenantID, id string) (sessionstore.Session, error)
}

// ExtensionConfig is the deployment-level §8.6 lease-extension
// configuration every tree is served with (Helm
// `leaseExtension.defaults` and `leaseExtension.max`).
// spec: §8.6
type ExtensionConfig struct {
	// DeploymentBudget is the deployment-default maxExtendableBudget. Zero
	// leaves the token dimension with no extension headroom.
	DeploymentBudget int64
	// DeploymentMaxBudget is the absolute ceiling no override may exceed.
	DeploymentMaxBudget int64
	// DimensionCeilings holds the flat effective ceiling of every
	// dimension except tokens, whose ceiling DeploymentBudget and
	// DeploymentMaxBudget resolve. The Tokens slot is ignored. A zero
	// ceiling disables that dimension, which is what a deployment with no
	// per-dimension configuration gets.
	DimensionCeilings Dimensions
	// ApprovalMode is the deployment-default extensionApproval mode.
	// Unspecified resolves to DefaultApprovalMode.
	ApprovalMode ApprovalMode
	// SuccessCoolOff is the coolOffSeconds post-approval window. Zero
	// applies DefaultSuccessCoolOff.
	SuccessCoolOff time.Duration
	// RejectionCoolOff is the rejectionCoolOffSeconds. Zero applies
	// DefaultRejectionCoolOff.
	RejectionCoolOff time.Duration
	// AutoMaxPerMinute is the autoModeRateLimit
	// maxAutoExtensionsPerMinute. Zero means no limit.
	AutoMaxPerMinute int
}

// effectiveMax resolves the per-dimension effective ceilings: tokens
// through the leaseextension layering, every other dimension from its
// flat ceiling.
func (c ExtensionConfig) effectiveMax() Dimensions {
	d := c.DimensionCeilings
	d.Tokens = leaseextension.ResolveEffectiveMax(c.DeploymentBudget, c.DeploymentMaxBudget, 0, 0, 0)
	return d
}

// StoreBudgetSource is the §8.6 BudgetSource that reads its extension
// state per request. The requesting, root, and parent rows come from the
// SessionStore under the request's tenant, so any gateway replica serves
// any session without prior registration, and denial, cool-off, and the
// tree's cumulative grants come from the DenialStore.
// spec: §8.6 (Extension state)
type StoreBudgetSource struct {
	sessions SessionReader
	denials  DenialStore
	cfg      ExtensionConfig
}

// NewStoreBudgetSource returns a StoreBudgetSource over sessions and
// denials, serving every tree with cfg. Both stores are required.
func NewStoreBudgetSource(sessions SessionReader, denials DenialStore, cfg ExtensionConfig) (*StoreBudgetSource, error) {
	if sessions == nil {
		return nil, errors.New("leasecontrol: StoreBudgetSource requires a session reader")
	}
	if denials == nil {
		return nil, errors.New("leasecontrol: StoreBudgetSource requires a denial store")
	}
	return &StoreBudgetSource{sessions: sessions, denials: denials, cfg: cfg}, nil
}

var _ BudgetSource = (*StoreBudgetSource)(nil)

// treeView is the per-request resolution of the requesting session's
// tree: its root, the root's base limits, the effective ceilings, and the
// parent ceiling.
type treeView struct {
	root          string
	base          Dimensions
	effectiveMax  Dimensions
	parentCeiling Dimensions
}

// clampLimit returns the per-dimension bound on the tree's cumulative
// grant: the effective max less the root's base limit, never below zero.
// The bound is a property of the tree's one pool, so it is the same
// whichever session in the tree asks; the parent ceiling bounds only the
// requester's pre-commit grant in ExtendLease.
// spec: §8.6 (Extension state)
func (v treeView) clampLimit() Dimensions {
	var lim Dimensions
	for _, d := range allDims {
		l := d.get(v.effectiveMax) - d.get(v.base)
		if l < 0 {
			l = 0
		}
		d.set(&lim, l)
	}
	return lim
}

// resolve reads the requesting row, its root row, and, for a delegated
// child, its parent row. A missing requesting or root row is
// ErrSessionNotFound and a terminal requesting row is ErrSessionTerminal.
// spec: §8.6 (Extension state)
func (s *StoreBudgetSource) resolve(ctx context.Context, tenantID, sessionID string) (treeView, error) {
	row, err := s.read(ctx, tenantID, sessionID)
	if err != nil {
		return treeView{}, err
	}
	if session.IsTerminal(row.State) {
		return treeView{}, fmt.Errorf("leasecontrol: session %s is %s: %w", sessionID, row.State, ErrSessionTerminal)
	}
	rootRow := row
	if row.RootSessionID != "" && row.RootSessionID != row.ID {
		if rootRow, err = s.read(ctx, tenantID, row.RootSessionID); err != nil {
			return treeView{}, fmt.Errorf("leasecontrol: root of %s: %w", sessionID, err)
		}
	}
	v := treeView{
		root:         rootRow.ID,
		base:         leaseLimits(rootRow.DelegationLease),
		effectiveMax: s.cfg.effectiveMax(),
	}
	// spec: §8.6 — only a delegated child carries the parent ceiling. The
	// depth is the classifier (sessionstore.CreationMarkers.IsDelegatedChild
	// records the same test) because only the delegation service writes a
	// non-zero depth. ParentSessionID and RootSessionID do not classify: the
	// parent foreign key clears ParentSessionID on delete, and a derived or
	// replayed row can carry a parent while its derive source's lease is no
	// ceiling on it.
	if row.DelegationDepth > 0 {
		if row.ParentSessionID == "" {
			return treeView{}, fmt.Errorf("leasecontrol: delegated child %s has no parent row: %w", sessionID, ErrSessionNotFound)
		}
		parent, perr := s.read(ctx, tenantID, row.ParentSessionID)
		if perr != nil {
			return treeView{}, fmt.Errorf("leasecontrol: parent of %s: %w", sessionID, perr)
		}
		v.parentCeiling = leaseLimits(parent.DelegationLease)
	}
	return v, nil
}

// read loads one row and maps sessionstore.ErrNotFound to
// ErrSessionNotFound.
func (s *StoreBudgetSource) read(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	row, err := s.sessions.Get(ctx, tenantID, id)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return sessionstore.Session{}, fmt.Errorf("leasecontrol: session %s: %w", id, ErrSessionNotFound)
	}
	if err != nil {
		return sessionstore.Session{}, fmt.Errorf("leasecontrol: read session %s: %w", id, err)
	}
	return row, nil
}

// leaseLimits projects a granted DelegationLease onto the extendable
// dimensions. A nil lease yields zero on every dimension, and the
// fileExportLimits components have no lease field, so they read zero.
// spec: §8.6
func leaseLimits(l *sessionstore.DelegationLease) Dimensions {
	if l == nil {
		return Dimensions{}
	}
	return Dimensions{
		Tokens:           l.MaxTokenBudget,
		Seconds:          int64(l.PerChildMaxAge),
		Children:         int64(l.MaxChildrenTotal),
		ParallelChildren: int64(l.MaxParallelChildren),
		TreeSize:         int64(l.MaxTreeSize),
	}
}

// TreeBudget returns the §8.6 extension state for the tree that contains
// sessionID. Current is the root's base limits plus the tree's cumulative
// grants from the DenialStore, so every replica reads one pool.
// spec: §8.6 (Extension state)
func (s *StoreBudgetSource) TreeBudget(ctx context.Context, tenantID, sessionID string) (TreeBudget, error) {
	v, err := s.resolve(ctx, tenantID, sessionID)
	if err != nil {
		return TreeBudget{}, err
	}
	denied, expiry, err := s.denials.Denied(ctx, tenantID, v.root)
	if err != nil {
		return TreeBudget{}, fmt.Errorf("leasecontrol: read denial for tree %s: %w", v.root, err)
	}
	grants, err := s.denials.Grants(ctx, tenantID, v.root)
	if err != nil {
		return TreeBudget{}, fmt.Errorf("leasecontrol: read grants for tree %s: %w", v.root, err)
	}
	return TreeBudget{
		RootSessionID:   v.root,
		Current:         v.base.add(grants),
		EffectiveMax:    v.effectiveMax,
		ParentCeiling:   v.parentCeiling,
		ExtensionDenied: denied,
		CoolOffExpiry:   expiry,
	}, nil
}

// ApplyGrant re-reads the requesting row, refuses a terminal one, and
// adds the grant to the tree's cumulative grants through
// DenialStore.Grant, which clamps it under the row lock in the
// transaction that re-checks the denial. The returned AppliedGrant
// carries the clamped amount and the resulting limits.
// spec: §8.6 (Extension state), §8.6 (In-flight requests)
func (s *StoreBudgetSource) ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, granted Dimensions) (AppliedGrant, error) {
	v, err := s.resolve(ctx, tenantID, requestingSessionID)
	if err != nil {
		return AppliedGrant{}, err
	}
	if v.root != rootSessionID {
		return AppliedGrant{}, fmt.Errorf("leasecontrol: session %s is not in tree %s: %w", requestingSessionID, rootSessionID, ErrSessionNotFound)
	}
	res, err := s.denials.Grant(ctx, tenantID, v.root, granted, v.clampLimit())
	if err != nil {
		return AppliedGrant{}, err
	}
	return AppliedGrant{Granted: res.Applied, NewLimits: v.base.add(res.Total)}, nil
}

// Deny persists the tree's extension denial with the configured rejection
// cool-off.
// spec: §8.6 (Durability)
func (s *StoreBudgetSource) Deny(ctx context.Context, tenantID, rootSessionID, _ string) error {
	return s.denials.Deny(ctx, tenantID, rootSessionID, s.rejectionCoolOff())
}

// ClearSubtreeDenial clears the tree's extension denial, backing the
// §15.1 admin endpoint. The caller has already resolved the root row
// and its tenant.
// spec: §8.6; §15.1
func (s *StoreBudgetSource) ClearSubtreeDenial(ctx context.Context, tenantID, rootSessionID, _ string) error {
	return s.denials.Clear(ctx, tenantID, rootSessionID)
}

// rejectionCoolOff resolves the configured rejection cool-off.
func (s *StoreBudgetSource) rejectionCoolOff() time.Duration {
	if s.cfg.RejectionCoolOff > 0 {
		return s.cfg.RejectionCoolOff
	}
	return DefaultRejectionCoolOff
}

// RejectionCoolOff returns the configured rejectionCoolOffSeconds, or
// zero so the caller applies DefaultRejectionCoolOff.
func (s *StoreBudgetSource) RejectionCoolOff(context.Context, string, string) time.Duration {
	return s.cfg.RejectionCoolOff
}

// ApprovalMode returns the configured extensionApproval mode, falling
// back to DefaultApprovalMode.
// spec: §8.6
func (s *StoreBudgetSource) ApprovalMode(context.Context, string, string) ApprovalMode {
	if s.cfg.ApprovalMode == ApprovalModeUnspecified {
		return DefaultApprovalMode
	}
	return s.cfg.ApprovalMode
}

// SuccessCoolOff returns the configured post-approval cool-off, or zero
// so the caller applies DefaultSuccessCoolOff.
// spec: §8.6
func (s *StoreBudgetSource) SuccessCoolOff(context.Context, string, string) time.Duration {
	return s.cfg.SuccessCoolOff
}

// AutoExtensionsPerMinute returns the configured
// maxAutoExtensionsPerMinute. Zero means no limit.
// spec: §8.6
func (s *StoreBudgetSource) AutoExtensionsPerMinute(context.Context, string, string) int {
	return s.cfg.AutoMaxPerMinute
}
