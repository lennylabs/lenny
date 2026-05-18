// SPDX-License-Identifier: MIT

// Package leasecontrol implements the gateway side of the §8.6
// lease-extension control plane: the GatewayControl.ExtendLease gRPC
// handler the pod adapter calls when its LLM proxy rejects a call for
// budget exhaustion.
//
// The handler is the stateful counterpart to pkg/leaseextension, which
// holds the pure §8.6 decision math (Grant and ResolveEffectiveMax).
// This package wires that math to the per-tree budget state: the
// current token budget, the layered effective ceiling, the rejection
// cool-off window, and the extension-denied flag.
//
// The per-tree budget state lives behind the BudgetSource interface.
// MemoryBudgetSource is the in-memory implementation used by tests and
// the minimal gateway. The §8.6 durability requirement — persisting the
// extension-denied flag and cool-off expiry to the delegation_tree_budget
// Postgres table, and reading them inside the budget-increment
// transaction so a coordinator handoff cannot bypass a user rejection —
// is satisfied by a Postgres-backed BudgetSource that ships with the
// Wave 1 store-persistence work. The interface is the seam for it.
package leasecontrol

import (
	"context"
	"errors"
	"fmt"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"

	"github.com/lennylabs/lenny/pkg/leaseextension"
)

// DefaultRejectionCoolOff is the §8.6 default rejectionCoolOffSeconds:
// after a user denies an extension, the requesting subtree's further
// requests are auto-rejected for this long. The spec default is 300
// seconds. Deployment, tenant, and runtime configuration override it
// through the same layering as the other lease-extension fields.
const DefaultRejectionCoolOff = 300 * time.Second

// TreeBudget is the §8.6 per-tree extension state for one delegation
// tree, keyed by its root session. A BudgetSource returns it for the
// requesting session's tree.
type TreeBudget struct {
	// RootSessionID identifies the delegation tree (the
	// delegation_tree_budget row key).
	RootSessionID string

	// CurrentTokenBudget is the tree's present maxTokenBudget — the
	// limit an extension increases. leaseextension.Grant treats it as
	// the dimension's current value.
	CurrentTokenBudget int64

	// EffectiveMax is the §8.6 layered ceiling for maxExtendableBudget,
	// already resolved through the deployment, tenant, and runtime
	// configuration (see leaseextension.ResolveEffectiveMax). An
	// extension can never push CurrentTokenBudget above it.
	EffectiveMax int64

	// ExtensionDenied is the §8.6 extension-denied flag for the
	// requesting subtree. When set and the cool-off has not expired,
	// the gateway auto-rejects without entering the elicitation path.
	ExtensionDenied bool

	// CoolOffExpiry is the rejection cool-off expiry for the requesting
	// subtree. It is meaningful only when ExtensionDenied is set. §8.6
	// requires this comparison use the authoritative store clock; the
	// BudgetSource is responsible for supplying a value already
	// compared against that clock, or for reporting the comparison
	// result through ExtensionDenied directly.
	CoolOffExpiry time.Time
}

// BudgetSource resolves and mutates the §8.6 per-tree budget state.
//
// The minimal gateway wires MemoryBudgetSource. The handoff-safe
// Postgres implementation reads the extension-denied flag and
// cool-off expiry, and commits the budget increment, inside one
// transaction under the delegation_tree_budget row lock, so the §8.6
// in-flight-request race and coordinator-handoff bypass are both
// closed. The Service depends only on this interface.
type BudgetSource interface {
	// TreeBudget returns the extension state for the tree that
	// contains sessionID. It returns ErrSessionNotFound when the
	// session is unknown.
	TreeBudget(ctx context.Context, tenantID, sessionID string) (TreeBudget, error)

	// ApplyGrant atomically raises the tree's CurrentTokenBudget by
	// granted and returns the new budget. A Postgres implementation
	// re-checks the extension-denied flag inside the same transaction
	// and returns ErrExtensionDenied when a rejection was persisted
	// between the TreeBudget read and the commit (§8.6 in-flight
	// atomic check). granted of zero is a no-op that still returns the
	// current budget.
	ApplyGrant(ctx context.Context, tenantID, rootSessionID string, granted int64) (int64, error)

	// RejectionCoolOff returns the §8.6 rejectionCoolOffSeconds for the
	// tree, resolved through the deployment, tenant, and runtime
	// layering. A zero return means the caller applies
	// DefaultRejectionCoolOff.
	RejectionCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration
}

// Sentinel errors a BudgetSource returns.
var (
	// ErrSessionNotFound — the requesting session is unknown to the
	// budget source.
	ErrSessionNotFound = errors.New("leasecontrol: session not found")

	// ErrExtensionDenied — the subtree's extension-denied flag was set
	// (found by ApplyGrant inside the commit transaction, closing the
	// §8.6 in-flight race).
	ErrExtensionDenied = errors.New("leasecontrol: subtree extension denied")
)

// TenantResolver maps a session id to its owning tenant. The gateway
// session store satisfies it. ExtendLease needs the tenant because the
// budget state is tenant-scoped per §4.2.
type TenantResolver interface {
	// TenantOf returns the tenant that owns sessionID, or
	// ErrSessionNotFound when the session is unknown.
	TenantOf(ctx context.Context, sessionID string) (string, error)
}

// Service implements adapterv1.GatewayControlServer. It is the
// gateway-hosted endpoint of the §8.6 control plane; the pod adapter
// dials it as a GatewayControl client.
type Service struct {
	adapterv1.UnimplementedGatewayControlServer

	budgets  BudgetSource
	tenants  TenantResolver
	auditing Auditor
	clock    func() time.Time
}

// Auditor records the §8.6 audit entry for an extension request. The
// gateway wires its audit store; tests pass nil to skip auditing.
type Auditor interface {
	// RecordExtension logs one §8.6 extension decision.
	RecordExtension(ctx context.Context, e ExtensionAudit)
}

// ExtensionAudit is the §8.6 audit record for one extension request.
type ExtensionAudit struct {
	TenantID         string
	RootSessionID    string
	RequestSessionID string
	RequestedTokens  int64
	GrantedTokens    int64
	EffectiveMax     int64
	Outcome          string
}

// Options configures a Service.
type Options struct {
	// Budgets resolves the §8.6 per-tree budget state. Required.
	Budgets BudgetSource

	// Tenants maps a session id to its tenant. Required.
	Tenants TenantResolver

	// Auditing records the §8.6 audit entry per request. Optional;
	// nil skips auditing.
	Auditing Auditor

	// Clock overrides time.Now for cool-off comparisons. Pass nil for
	// production.
	Clock func() time.Time
}

// NewService returns a §8.6 ExtendLease Service.
func NewService(opts Options) (*Service, error) {
	if opts.Budgets == nil {
		return nil, errors.New("leasecontrol: Budgets is required")
	}
	if opts.Tenants == nil {
		return nil, errors.New("leasecontrol: Tenants is required")
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		budgets:  opts.Budgets,
		tenants:  opts.Tenants,
		auditing: opts.Auditing,
		clock:    clock,
	}, nil
}

// ExtendLease handles the §8.6 adapter→gateway extension request. It
// resolves the requesting session's tree budget and effective ceiling,
// enforces the rejection cool-off, computes the grant with
// pkg/leaseextension, and applies it.
//
// The §8.6 status mapping is:
//
//   - GRANTED — the full requested increase fit under the ceiling.
//   - PARTIALLY_GRANTED — the request was capped to the remaining
//     headroom, which is non-zero.
//   - CEILING_REACHED — no headroom remained; the grant is zero. The
//     adapter MUST treat this as terminal and not retry.
//   - REJECTED — the subtree's extension-denied flag is set and the
//     cool-off has not expired. The response carries the cool-off
//     expiry.
func (s *Service) ExtendLease(ctx context.Context, req *adapterv1.ExtendLeaseRequest) (*adapterv1.ExtendLeaseResponse, error) {
	sessionID := req.GetSessionId().GetValue()
	if sessionID == "" {
		return nil, fmt.Errorf("leasecontrol: ExtendLease request carries no session id")
	}

	tenantID, err := s.tenants.TenantOf(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, fmt.Errorf("leasecontrol: ExtendLease for unknown session %s: %w", sessionID, err)
		}
		return nil, fmt.Errorf("leasecontrol: resolve tenant for session %s: %w", sessionID, err)
	}

	budget, err := s.budgets.TreeBudget(ctx, tenantID, sessionID)
	if err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return nil, fmt.Errorf("leasecontrol: no tree budget for session %s: %w", sessionID, err)
		}
		return nil, fmt.Errorf("leasecontrol: load tree budget for session %s: %w", sessionID, err)
	}

	// §8.6 rejection cool-off. When the subtree is extension-denied and
	// the cool-off has not expired, auto-reject without entering the
	// elicitation path. The BudgetSource has already compared
	// CoolOffExpiry against the authoritative clock; a zero expiry with
	// the flag set is treated as still in effect.
	if budget.ExtensionDenied && (budget.CoolOffExpiry.IsZero() || s.clock().Before(budget.CoolOffExpiry)) {
		s.audit(ctx, tenantID, budget, sessionID, req.GetRequestedTokens(), 0, "REJECTED")
		return &adapterv1.ExtendLeaseResponse{
			Status:              adapterv1.ExtendLeaseResponse_STATUS_REJECTED,
			GrantedTokens:       0,
			CoolOffExpiryUnixMs: budget.CoolOffExpiry.UnixMilli(),
		}, nil
	}

	granted, outcome := leaseextension.Grant(
		budget.CurrentTokenBudget,
		req.GetRequestedTokens(),
		budget.EffectiveMax,
	)

	if granted > 0 {
		if _, err := s.budgets.ApplyGrant(ctx, tenantID, budget.RootSessionID, granted); err != nil {
			// §8.6 in-flight atomic check: a REJECTED outcome was
			// persisted between the TreeBudget read and the commit.
			// Surface it as a REJECTED extension response rather than
			// granting.
			if errors.Is(err, ErrExtensionDenied) {
				coolOff := s.resolveCoolOff(ctx, tenantID, budget.RootSessionID)
				s.audit(ctx, tenantID, budget, sessionID, req.GetRequestedTokens(), 0, "REJECTED")
				return &adapterv1.ExtendLeaseResponse{
					Status:              adapterv1.ExtendLeaseResponse_STATUS_REJECTED,
					GrantedTokens:       0,
					CoolOffExpiryUnixMs: s.clock().Add(coolOff).UnixMilli(),
				}, nil
			}
			return nil, fmt.Errorf("leasecontrol: apply grant for tree %s: %w", budget.RootSessionID, err)
		}
	}

	status := statusFor(outcome)
	s.audit(ctx, tenantID, budget, sessionID, req.GetRequestedTokens(), granted, outcome.String())
	return &adapterv1.ExtendLeaseResponse{
		Status:        status,
		GrantedTokens: granted,
	}, nil
}

// statusFor maps a §8.6 leaseextension.Outcome to the proto status
// enum. PartiallyGranted with a zero grant cannot occur — Grant returns
// CeilingReached when no headroom remains — so the mapping is total.
func statusFor(o leaseextension.Outcome) adapterv1.ExtendLeaseResponse_Status {
	switch o {
	case leaseextension.Granted:
		return adapterv1.ExtendLeaseResponse_STATUS_GRANTED
	case leaseextension.PartiallyGranted:
		return adapterv1.ExtendLeaseResponse_STATUS_PARTIALLY_GRANTED
	case leaseextension.CeilingReached:
		return adapterv1.ExtendLeaseResponse_STATUS_CEILING_REACHED
	default:
		return adapterv1.ExtendLeaseResponse_STATUS_UNSPECIFIED
	}
}

// resolveCoolOff returns the §8.6 rejection cool-off for the tree,
// falling back to DefaultRejectionCoolOff when the source supplies no
// override.
func (s *Service) resolveCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration {
	d := s.budgets.RejectionCoolOff(ctx, tenantID, rootSessionID)
	if d <= 0 {
		return DefaultRejectionCoolOff
	}
	return d
}

// audit records one §8.6 extension decision when an Auditor is wired.
func (s *Service) audit(ctx context.Context, tenantID string, b TreeBudget, sessionID string, requested, granted int64, outcome string) {
	if s.auditing == nil {
		return
	}
	s.auditing.RecordExtension(ctx, ExtensionAudit{
		TenantID:         tenantID,
		RootSessionID:    b.RootSessionID,
		RequestSessionID: sessionID,
		RequestedTokens:  requested,
		GrantedTokens:    granted,
		EffectiveMax:     b.EffectiveMax,
		Outcome:          outcome,
	})
}
