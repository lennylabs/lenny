// SPDX-License-Identifier: MIT

package leasecontrol

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/leaseextension"
)

// MemoryBudgetSource is the in-memory BudgetSource for the minimal
// gateway and tests. It holds each delegation tree's §8.6 extension
// state in a map keyed by root session.
//
// It is not handoff-safe: a gateway restart loses the extension-denied
// flag and cool-off expiry, which §8.6 forbids for a production
// deployment. The Postgres-backed BudgetSource that ships with the
// Wave 1 store-persistence work persists those fields to the
// delegation_tree_budget table and reads them inside the
// budget-increment transaction. MemoryBudgetSource is the seam target
// for that swap.
type MemoryBudgetSource struct {
	mu    sync.Mutex
	clock func() time.Time

	// trees holds per-tree extension state keyed by root session id.
	trees map[string]*memTree

	// sessionTree maps any session id to its tree's root session id,
	// so a request from a non-root session resolves the right tree.
	sessionTree map[string]string

	// sessionTenant maps a session id to its tenant.
	sessionTenant map[string]string

	// parentLease records the §8.6 line 648 parent-lease ceilings keyed
	// by child session id. A child session's effective extension max
	// is capped at the parent's lease grant per dimension. F-8.6.15.
	parentLease map[string]SessionLease
}

// memTree is one delegation tree's mutable §8.6 extension state.
type memTree struct {
	rootSessionID  string
	tenantID       string
	baseBudget     int64
	deploymentMax  int64
	tenantMax      int64
	tenantBase     int64
	runtimeBase    int64
	deploymentBase int64

	// baseMaxAgeSeconds / effectiveMaxAgeSeconds carry the §8.6 line
	// 643 additionalMaxAge dimension. The seconds dimension is
	// independent of the tokens dimension: a request that exhausts only
	// one of them still applies the other. F-8.6.11.
	baseMaxAgeSeconds      int64
	effectiveMaxAgeSeconds int64

	// extensions records per-requesting-session §8.6 line 737-741
	// extension bumps. Keyed by sessionID, each value carries the
	// session-specific deltas atop the tree's baseBudget /
	// baseMaxAgeSeconds. TreeBudget(sessionID) returns the requesting
	// session's view (base + delta); other sessions in the same tree
	// see the unchanged base, satisfying "Extensions apply to the
	// requesting session only" and "Existing children are unaffected".
	// F-8.6.12.
	// spec: §8.6 line 737-741
	extensions map[string]sessionExtension

	// parentLeaseTokenCeiling / parentLeaseMaxAgeCeiling capture the
	// §8.6 line 648 "parent's own lease limits" hard ceiling. Set by
	// AddSession to the delegated child's parent grant; zero on the
	// root session (no parent). The handler caps EffectiveMax at the
	// parent ceiling when both are positive. F-8.6.15.
	parentLeaseTokenCeiling  int64
	parentLeaseMaxAgeCeiling int64

	// extensionDenied and coolOffExpiry are the §8.6 per-tree denial
	// state. The in-memory source applies them tree-wide; the spec
	// scopes the flag to the requesting subtree, which the Postgres
	// source keys with a per-row subtree id.
	extensionDenied bool
	coolOffExpiry   time.Time

	// rejectionCoolOff is the resolved §8.6 rejectionCoolOffSeconds for
	// the tree. Zero applies DefaultRejectionCoolOff.
	rejectionCoolOff time.Duration

	// approvalMode is the resolved §8.6 extensionApproval mode (auto vs
	// elicitation). The dispatcher that selects between the two lands
	// with the §8.6 line 714 elicitation flow; until then the field is
	// plumbed but not consulted by the handler, which auto-approves
	// under the ceiling. Zero applies DefaultApprovalMode.
	// spec: §8.6 line 674
	approvalMode ApprovalMode

	// successCoolOff is the resolved §8.6 coolOffSeconds for the
	// elicitation-mode post-approval window. Zero applies
	// DefaultSuccessCoolOff once the dispatcher consumes it.
	// spec: §8.6 line 675
	successCoolOff time.Duration
}

// sessionExtension is one requesting session's accumulated §8.6 line
// 737-741 extension deltas atop the tree's base budget. The in-memory
// source records these per-session so a sibling reading the tree
// budget does not observe the requester's bump.
// F-8.6.12.
// spec: §8.6 line 737-741
type sessionExtension struct {
	tokens  int64
	seconds int64
}

// effectiveMax resolves the tree's §8.6 layered ceiling with the pure
// pkg/leaseextension math.
func (t *memTree) effectiveMax() int64 {
	return leaseextension.ResolveEffectiveMax(
		t.deploymentBase,
		t.deploymentMax,
		t.tenantBase,
		t.tenantMax,
		t.runtimeBase,
	)
}

// NewMemoryBudgetSource returns an empty MemoryBudgetSource.
func NewMemoryBudgetSource() *MemoryBudgetSource {
	return &MemoryBudgetSource{
		clock:         func() time.Time { return time.Now().UTC() },
		trees:         map[string]*memTree{},
		sessionTree:   map[string]string{},
		sessionTenant: map[string]string{},
		parentLease:   map[string]SessionLease{},
	}
}

// WithClock overrides the time source, for deterministic cool-off
// tests. It returns the receiver for chaining.
func (m *MemoryBudgetSource) WithClock(clock func() time.Time) *MemoryBudgetSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clock = clock
	return m
}

// TreeConfig is the §8.6 per-tree budget configuration registered with
// a MemoryBudgetSource. The layered fields feed
// leaseextension.ResolveEffectiveMax.
type TreeConfig struct {
	// TenantID owns the tree.
	TenantID string

	// CurrentTokenBudget is the tree's present maxTokenBudget.
	CurrentTokenBudget int64

	// DeploymentBase is the deployment-default maxExtendableBudget.
	DeploymentBase int64

	// DeploymentMax is the deployment ceiling — the §8.6 absolute
	// maximum no override can exceed.
	DeploymentMax int64

	// TenantBase is the tenant-override maxExtendableBudget base.
	TenantBase int64

	// TenantMax is the optional tenant ceiling.
	TenantMax int64

	// RuntimeBase is the runtime-override maxExtendableBudget base.
	RuntimeBase int64

	// CurrentMaxAgeSeconds is the §8.6 line 643 additionalMaxAge
	// dimension's present value (the tree's current perChildMaxAge in
	// seconds). F-8.6.11.
	CurrentMaxAgeSeconds int64

	// EffectiveMaxAgeSeconds is the layered ceiling for the
	// additionalMaxAge dimension. Zero disables the dimension; a
	// non-zero ceiling permits Grant on the seconds dimension up to
	// this value. F-8.6.11.
	EffectiveMaxAgeSeconds int64

	// RejectionCoolOff overrides DefaultRejectionCoolOff for the tree.
	RejectionCoolOff time.Duration

	// ApprovalMode is the §8.6 extensionApproval mode resolved for the
	// tree via the deployment→tenant→runtime layering. The unspecified
	// zero value is normalized to DefaultApprovalMode.
	// spec: §8.6 line 674
	ApprovalMode ApprovalMode

	// SuccessCoolOff is the §8.6 coolOffSeconds for the elicitation-mode
	// post-approval window. Zero applies DefaultSuccessCoolOff.
	// spec: §8.6 line 675
	SuccessCoolOff time.Duration
}

// SessionLease is the §8.6 line 648 "parent's own lease limits" snapshot
// recorded for a delegated child session. The MemoryBudgetSource keys
// it on the child session id; the handler reads it as the second hard
// ceiling so a child cannot extend beyond what the parent's lease
// granted. Zero on either field disables that dimension's cap.
// F-8.6.15.
// spec: §8.6 line 648
type SessionLease struct {
	// TokenCeiling caps the child's per-extension token grant at the
	// parent lease's token grant value. Zero means "no cap on this
	// dimension" (the parent had no lease ceiling, or the row was not
	// resolved).
	TokenCeiling int64

	// MaxAgeCeiling caps the child's per-extension additionalMaxAge
	// grant at the parent lease's perChildMaxAge value. Zero means "no
	// cap on this dimension".
	MaxAgeCeiling int64
}

// RegisterTree records a delegation tree's §8.6 budget configuration,
// keyed by its root session. The root session is registered as a
// member of its own tree.
func (m *MemoryBudgetSource) RegisterTree(rootSessionID string, cfg TreeConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mode := cfg.ApprovalMode
	if mode == ApprovalModeUnspecified {
		mode = DefaultApprovalMode
	}
	m.trees[rootSessionID] = &memTree{
		rootSessionID:          rootSessionID,
		tenantID:               cfg.TenantID,
		baseBudget:             cfg.CurrentTokenBudget,
		deploymentBase:         cfg.DeploymentBase,
		deploymentMax:          cfg.DeploymentMax,
		tenantBase:             cfg.TenantBase,
		tenantMax:              cfg.TenantMax,
		runtimeBase:            cfg.RuntimeBase,
		baseMaxAgeSeconds:      cfg.CurrentMaxAgeSeconds,
		effectiveMaxAgeSeconds: cfg.EffectiveMaxAgeSeconds,
		extensions:             map[string]sessionExtension{},
		rejectionCoolOff:       cfg.RejectionCoolOff,
		approvalMode:           mode,
		successCoolOff:         cfg.SuccessCoolOff,
	}
	m.sessionTree[rootSessionID] = rootSessionID
	m.sessionTenant[rootSessionID] = cfg.TenantID
}

// AddSession records that sessionID belongs to the tree rooted at
// rootSessionID, so an extension request from a delegated child
// resolves the right tree budget.
func (m *MemoryBudgetSource) AddSession(sessionID, rootSessionID, tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionTree[sessionID] = rootSessionID
	m.sessionTenant[sessionID] = tenantID
}

// SetParentLease records the §8.6 line 648 parent-lease ceiling for a
// delegated child session. The handler caps the child's extension
// effective max at this value (per dimension) so the child cannot
// extend beyond what the parent's own lease granted. Calling with a
// zero-valued SessionLease clears the cap (e.g., to reset a session
// that no longer has a parent ceiling). The session must already be
// registered via AddSession or RegisterTree; an unknown session is a
// no-op so callers can call this defensively before AddSession.
// F-8.6.15.
// spec: §8.6 line 648
func (m *MemoryBudgetSource) SetParentLease(sessionID string, parent SessionLease) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.parentLease == nil {
		m.parentLease = map[string]SessionLease{}
	}
	m.parentLease[sessionID] = parent
}

// MarkDenied sets the §8.6 extension-denied flag for the tree and
// starts the rejection cool-off. After expiry, extension requests are
// served normally again. It is the in-memory stand-in for the §8.6
// user-rejection path the elicitation flow drives.
func (m *MemoryBudgetSource) MarkDenied(rootSessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.trees[rootSessionID]
	if !ok {
		return
	}
	coolOff := t.rejectionCoolOff
	if coolOff <= 0 {
		coolOff = DefaultRejectionCoolOff
	}
	t.extensionDenied = true
	t.coolOffExpiry = m.clock().Add(coolOff)
}

// ClearDenial clears the §8.6 extension-denied flag for the tree,
// mirroring the admin API that resets a subtree to normal extension
// behavior regardless of cool-off state.
func (m *MemoryBudgetSource) ClearDenial(rootSessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.trees[rootSessionID]; ok {
		t.extensionDenied = false
		t.coolOffExpiry = time.Time{}
	}
}

// ClearSubtreeDenial clears the §8.6 extension-denied flag for the
// subtree rooted at sessionID inside the delegation tree rooted at
// rootSessionID, backing the §15.1 line 868 admin endpoint
// DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial.
// It returns found=false when rootSessionID names no known tree so the
// admin handler can answer 404 rather than silently succeeding.
//
// The in-memory source records the §8.6 denial tree-wide rather than
// per-subtree (see memTree.extensionDenied), so clearing any subtree of
// a tree clears the tree's denial. The handoff-safe Postgres source
// keys the flag with a per-row subtree id and clears only the named
// subtree; the (found bool, error) signature is the seam for it — a
// Postgres implementation returns the not-found and storage-error cases
// through the same two return values.
// spec: §8.6 line 735; §15.1 line 868
func (m *MemoryBudgetSource) ClearSubtreeDenial(_ context.Context, rootSessionID, _ string) (found bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.trees[rootSessionID]
	if !ok {
		return false, nil
	}
	t.extensionDenied = false
	t.coolOffExpiry = time.Time{}
	return true, nil
}

// TenantOf resolves a session's tenant, satisfying TenantResolver so a
// MemoryBudgetSource can serve both roles in tests and the minimal
// gateway.
func (m *MemoryBudgetSource) TenantOf(_ context.Context, sessionID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.sessionTenant[sessionID]
	if !ok {
		return "", ErrSessionNotFound
	}
	return tenant, nil
}

// TreeBudget returns the §8.6 extension state for the tree that
// contains sessionID. The cool-off comparison uses the source's own
// clock, which the in-memory source treats as authoritative. The
// returned CurrentTokenBudget / CurrentMaxAgeSeconds reflect the
// requesting session's per-session view: the tree base plus any
// extension previously granted to sessionID itself. Siblings see the
// unchanged base — extensions apply to the requesting session only
// per §8.6 line 737-741. F-8.6.12.
func (m *MemoryBudgetSource) TreeBudget(_ context.Context, tenantID, sessionID string) (TreeBudget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(tenantID, sessionID)
	if err != nil {
		return TreeBudget{}, err
	}
	denied := t.extensionDenied && m.clock().Before(t.coolOffExpiry)
	parent := m.parentLease[sessionID]
	ext := t.extensions[sessionID]
	return TreeBudget{
		RootSessionID:             t.rootSessionID,
		CurrentTokenBudget:        t.baseBudget + ext.tokens,
		EffectiveMax:              t.effectiveMax(),
		ParentLeaseTokenCeiling:   parent.TokenCeiling,
		CurrentMaxAgeSeconds:      t.baseMaxAgeSeconds + ext.seconds,
		EffectiveMaxMaxAgeSeconds: t.effectiveMaxAgeSeconds,
		ParentLeaseMaxAgeCeiling:  parent.MaxAgeCeiling,
		ExtensionDenied:           denied,
		CoolOffExpiry:             t.coolOffExpiry,
	}, nil
}

// ApplyGrant raises the requesting session's extension deltas by
// grantedTokens / grantedSeconds. §8.6 lines 737-741 scope an
// extension to the requesting session only — existing children are
// unaffected, only new children spawned after the extension benefit
// from the expanded parent budget — so the bump is recorded against
// requestingSessionID rather than the tree root. ApplyGrant re-checks
// the §8.6 extension-denied flag under the lock — the in-memory
// analogue of the Postgres in-transaction re-check — and returns
// ErrExtensionDenied when a denial landed since the TreeBudget read.
// F-8.6.11; F-8.6.12.
func (m *MemoryBudgetSource) ApplyGrant(_ context.Context, tenantID, rootSessionID, requestingSessionID string, grantedTokens, grantedSeconds int64) (NewLimits, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(tenantID, rootSessionID)
	if err != nil {
		return NewLimits{}, err
	}
	if t.extensionDenied && m.clock().Before(t.coolOffExpiry) {
		return NewLimits{}, ErrExtensionDenied
	}
	if t.extensions == nil {
		t.extensions = map[string]sessionExtension{}
	}
	scope := requestingSessionID
	if scope == "" {
		// The root session (or a caller that omits the requesting id)
		// applies the bump under the tree root id so the legacy single-
		// session test cases continue to read the bumped view.
		scope = rootSessionID
	}
	ext := t.extensions[scope]
	if grantedTokens > 0 {
		ext.tokens += grantedTokens
	}
	if grantedSeconds > 0 {
		ext.seconds += grantedSeconds
	}
	t.extensions[scope] = ext
	return NewLimits{
		TokenBudget:   t.baseBudget + ext.tokens,
		MaxAgeSeconds: t.baseMaxAgeSeconds + ext.seconds,
	}, nil
}

// RejectionCoolOff returns the §8.6 rejectionCoolOffSeconds configured
// for the tree, or zero when none is set so the caller applies
// DefaultRejectionCoolOff.
func (m *MemoryBudgetSource) RejectionCoolOff(_ context.Context, tenantID, rootSessionID string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(tenantID, rootSessionID)
	if err != nil {
		return 0
	}
	return t.rejectionCoolOff
}

// ApprovalMode returns the §8.6 extensionApproval mode the tree was
// registered with, falling back to DefaultApprovalMode when the
// registration left it unspecified. The dispatcher that selects auto
// vs. elicitation behaviour reads it.
// spec: §8.6 line 674
func (m *MemoryBudgetSource) ApprovalMode(_ context.Context, tenantID, rootSessionID string) ApprovalMode {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(tenantID, rootSessionID)
	if err != nil {
		return DefaultApprovalMode
	}
	if t.approvalMode == ApprovalModeUnspecified {
		return DefaultApprovalMode
	}
	return t.approvalMode
}

// SuccessCoolOff returns the §8.6 elicitation-mode post-approval
// cool-off configured for the tree, or zero when none is set so the
// caller applies DefaultSuccessCoolOff.
// spec: §8.6 line 675
func (m *MemoryBudgetSource) SuccessCoolOff(_ context.Context, tenantID, rootSessionID string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, err := m.lookupLocked(tenantID, rootSessionID)
	if err != nil {
		return 0
	}
	return t.successCoolOff
}

// lookupLocked resolves the tree for sessionID and asserts the tenant
// matches, so a cross-tenant session id cannot read another tenant's
// budget. The caller holds m.mu.
func (m *MemoryBudgetSource) lookupLocked(tenantID, sessionID string) (*memTree, error) {
	root, ok := m.sessionTree[sessionID]
	if !ok {
		return nil, ErrSessionNotFound
	}
	t, ok := m.trees[root]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if t.tenantID != tenantID {
		return nil, ErrSessionNotFound
	}
	return t, nil
}
