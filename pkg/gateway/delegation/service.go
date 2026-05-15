// SPDX-License-Identifier: MIT

// Package delegation implements the §8 recursive-delegation
// gateway service. It composes the pure §8.2 primitives
// (pkg/delegation/cycle for cycle detection, pkg/delegation/lease
// for depth + budget arithmetic) with the §5.3 isolation
// monotonicity check and the session store to create a child
// session from a parent.
//
// The Service is the single place the gateway turns a delegate
// request into a child session. The §8.5 `lenny/delegate_task` MCP
// tool handler is a thin shim over Service.Delegate; building the
// service independently keeps the delegation rules unit-testable
// without the MCP transport.
package delegation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Request is a §8.5 `lenny/delegate_task` invocation.
type Request struct {
	// ParentSessionID is the session issuing the delegation. Must be
	// running per §8.2.
	ParentSessionID string

	// RuntimeRef is the child session's runtime.
	RuntimeRef string

	// PoolRef is the child session's pool. Cycle detection keys on
	// (runtime, pool) Identity tuples per §8.2 — the same runtime in
	// a different pool is NOT a cycle.
	PoolRef string

	// IsolationProfile is the child's §5.3 isolation profile. Must
	// be at least as restrictive as the parent's per §8.3 SEC-001
	// (delegation downgrades are categorically rejected — no admin
	// override, unlike derive).
	IsolationProfile isolation.Profile

	// MaxDepth caps the delegation tree depth per §8.2.bis. Zero
	// means "inherit / use the resolved ceiling".
	MaxDepth int

	// UserID owns the child session. Inherits the parent's when
	// blank.
	UserID string
}

// Result is the outcome of a successful Delegate call.
type Result struct {
	// Child is the newly created child session row.
	Child sessionstore.Session

	// Depth is the child's depth in the delegation tree (root = 0).
	Depth int
}

// Service creates child sessions from a delegation request.
type Service struct {
	store sessionstore.Store
	clock func() time.Time
	idFn  func() string
	mode  cycle.Mode
}

// Options configures a Service.
type Options struct {
	// Clock overrides time.Now. Pass nil for production.
	Clock func() time.Time

	// IDFunc overrides the child-session id generator. Pass nil for
	// a crypto/rand-backed default.
	IDFunc func() string

	// CycleMode is the §8.2 cycle-detection mode. Defaults to
	// `enforce`.
	CycleMode cycle.Mode
}

// NewService returns a delegation Service.
func NewService(store sessionstore.Store, opts Options) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	idFn := opts.IDFunc
	if idFn == nil {
		idFn = randomChildID
	}
	mode := opts.CycleMode
	if mode == "" {
		mode = cycle.ModeEnforce
	}
	return &Service{store: store, clock: clock, idFn: idFn, mode: mode}
}

// Errors surfaced by Delegate. Each maps to a §15.1 error code.
var (
	// ErrParentNotFound — the parent session does not exist.
	ErrParentNotFound = errors.New("delegation: parent session not found")

	// ErrParentNotRunning — the parent is not in a state that may
	// delegate (§8.2 requires the parent be running).
	ErrParentNotRunning = errors.New("delegation: parent session is not running")
)

// IsolationViolationError reports a §8.3 SEC-001 monotonicity
// failure. The gateway maps it to ISOLATION_MONOTONICITY_VIOLATED
// (HTTP 422).
type IsolationViolationError struct {
	ParentProfile isolation.Profile
	ChildProfile  isolation.Profile
}

func (e *IsolationViolationError) Error() string {
	return fmt.Sprintf("delegation: child isolation %q is weaker than parent %q (SEC-001)",
		e.ChildProfile, e.ParentProfile)
}

// Delegate validates a §8 delegation request against the parent's
// lineage and creates the child session. The validation order is:
//
//  1. parent lookup + running-state check (§8.2).
//  2. §8.3 SEC-001 isolation monotonicity — child must be at least
//     as restrictive as the parent; no admin override on the
//     delegation path.
//  3. §8.2 cycle detection over the parent's (runtime, pool)
//     lineage.
//  4. §8.2.bis depth check against MaxDepth.
//  5. atomic child-session INSERT with ParentSessionID set.
func (s *Service) Delegate(ctx context.Context, tenantID string, req Request) (Result, error) {
	parent, err := s.store.Get(ctx, tenantID, req.ParentSessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return Result{}, ErrParentNotFound
		}
		return Result{}, err
	}
	if parent.State != session.StateRunning {
		return Result{}, ErrParentNotRunning
	}

	// §8.3 SEC-001 isolation monotonicity. The child profile
	// defaults to the parent's when unset.
	childProfile := req.IsolationProfile
	if childProfile == "" {
		childProfile = parent.IsolationProfile
	}
	if !isolation.IsValid(childProfile) {
		return Result{}, fmt.Errorf("delegation: child isolationProfile %q is not a recognised §5.3 profile", childProfile)
	}
	if parent.IsolationProfile != "" && !isolation.AtLeastAsRestrictive(childProfile, parent.IsolationProfile) {
		return Result{}, &IsolationViolationError{
			ParentProfile: parent.IsolationProfile,
			ChildProfile:  childProfile,
		}
	}

	// Build the lineage from the parent chain so cycle detection can
	// run over (runtime, pool) Identity tuples.
	lineage, depth, err := s.buildLineage(ctx, tenantID, parent)
	if err != nil {
		return Result{}, err
	}

	target := cycle.Identity{RuntimeName: req.RuntimeRef, PoolName: req.PoolRef}
	decision := cycle.Decide(lineage, target, cycle.Settings{Mode: s.mode})
	if decision.Outcome == cycle.OutcomeRejected {
		return Result{}, cycle.ToError(decision, target)
	}

	// §8.2.bis depth check. The child's depth is the parent's depth
	// + 1; CheckDepth rejects when that would exceed MaxDepth.
	if req.MaxDepth > 0 {
		if err := lease.CheckDepth(depth, req.MaxDepth); err != nil {
			return Result{}, err
		}
	}

	userID := req.UserID
	if userID == "" {
		userID = parent.UserID
	}
	now := s.clock()
	child := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           userID,
		RuntimeRef:       req.RuntimeRef,
		PoolRef:          req.PoolRef,
		State:            session.StateCreated,
		IsolationProfile: childProfile,
		ParentSessionID:  parent.ID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.store.Create(ctx, child); err != nil {
		return Result{}, err
	}
	return Result{Child: child, Depth: depth + 1}, nil
}

// buildLineage walks the ParentSessionID chain from the parent up to
// the root, returning the §8.2 Lineage (root-first) and the
// parent's depth (root = 0). A cycle in the stored chain is
// defended against with a visited set.
func (s *Service) buildLineage(ctx context.Context, tenantID string, parent sessionstore.Session) (cycle.Lineage, int, error) {
	var chain []sessionstore.Session
	visited := map[string]bool{}
	cur := parent
	for {
		if visited[cur.ID] {
			break // defensive: corrupt stored chain
		}
		visited[cur.ID] = true
		chain = append(chain, cur)
		if cur.ParentSessionID == "" {
			break
		}
		next, err := s.store.Get(ctx, tenantID, cur.ParentSessionID)
		if err != nil {
			if errors.Is(err, sessionstore.ErrNotFound) {
				break // ancestor GC'd — treat current as the root
			}
			return nil, 0, err
		}
		cur = next
	}
	// chain is parent-first; reverse to root-first for Lineage.
	lineage := make(cycle.Lineage, 0, len(chain))
	for i := len(chain) - 1; i >= 0; i-- {
		lineage = append(lineage, cycle.Identity{
			RuntimeName: chain[i].RuntimeRef,
			PoolName:    chain[i].PoolRef,
		})
	}
	// The parent's depth is its index in the root-first chain.
	return lineage, len(chain) - 1, nil
}

// randomChildID returns a fresh child session id.
func randomChildID() string {
	return "sess_" + randomHex(8)
}
