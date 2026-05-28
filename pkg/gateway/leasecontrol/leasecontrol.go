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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"google.golang.org/grpc/peer"

	"github.com/lennylabs/lenny/pkg/leaseextension"
)

// DefaultRejectionCoolOff is the §8.6 default rejectionCoolOffSeconds:
// after a user denies an extension, the requesting subtree's further
// requests are auto-rejected for this long. The spec default is 300
// seconds. Deployment, tenant, and runtime configuration override it
// through the same layering as the other lease-extension fields.
// spec: §8.6 line 734
const DefaultRejectionCoolOff = 300 * time.Second

// DefaultSuccessCoolOff is the §8.6 default coolOffSeconds for the
// post-approval window in elicitation mode: after the user approves an
// elicitation, the gateway auto-grants further extension requests for
// this long without re-eliciting. The spec deployment default is 5
// seconds (line 675). Deployment, tenant, and runtime configuration
// override it through the same layering as the other lease-extension
// fields.
//
// The elicitation-mode dispatcher itself is the §8.6 line 714 stateful
// surface that lands separately; this constant is the value the
// dispatcher consumes when no override is present, so the spec default
// is fixed in code now and the dispatcher does not need to re-derive
// it.
// spec: §8.6 line 675
const DefaultSuccessCoolOff = 5 * time.Second

// ApprovalMode is the §8.6 extensionApproval mode resolved through the
// deployment→tenant→runtime layering. The dispatcher that selects auto
// vs. elicitation behaviour lands with the §8.6 line 714 elicitation
// flow; this type and its default constant are the plumbing the
// dispatcher will read.
// spec: §8.6 line 654, line 674
type ApprovalMode string

const (
	// ApprovalModeUnspecified — the layered configuration left the mode
	// blank; resolution falls back to DefaultApprovalMode.
	ApprovalModeUnspecified ApprovalMode = ""
	// ApprovalModeAuto — every extension is auto-approved up to the
	// effective max. No elicitation, no queuing, no success cool-off.
	// spec: §8.6 line 712
	ApprovalModeAuto ApprovalMode = "auto"
	// ApprovalModeElicitation — requests are serialized per task tree
	// with a generic elicitation and a success cool-off window.
	// spec: §8.6 line 714
	ApprovalModeElicitation ApprovalMode = "elicitation"
)

// DefaultApprovalMode is the §8.6 line 674 deployment-default
// extensionApproval mode. The spec calls for elicitation (the
// human-in-the-loop budget gate) so a deployment with no override
// receives the safer mode.
// spec: §8.6 line 674
const DefaultApprovalMode = ApprovalModeElicitation

// ResolveApprovalMode returns the effective approval mode for the
// deployment→tenant→runtime layering. A later layer overrides an
// earlier one when it carries a non-unspecified value. A fully empty
// stack falls back to DefaultApprovalMode.
// spec: §8.6 line 654
func ResolveApprovalMode(deployment, tenant, runtime ApprovalMode) ApprovalMode {
	v := deployment
	if tenant != ApprovalModeUnspecified {
		v = tenant
	}
	if runtime != ApprovalModeUnspecified {
		v = runtime
	}
	if v == ApprovalModeUnspecified {
		return DefaultApprovalMode
	}
	return v
}

// ResolveSuccessCoolOff returns the effective post-approval cool-off
// duration for the deployment→tenant→runtime layering. A later layer
// overrides an earlier one when it carries a positive duration. A
// fully zero stack falls back to DefaultSuccessCoolOff.
// spec: §8.6 line 654, line 675
func ResolveSuccessCoolOff(deployment, tenant, runtime time.Duration) time.Duration {
	v := deployment
	if tenant > 0 {
		v = tenant
	}
	if runtime > 0 {
		v = runtime
	}
	if v <= 0 {
		return DefaultSuccessCoolOff
	}
	return v
}

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

	// ParentLeaseTokenCeiling is the §8.6 line 648 second hard ceiling:
	// "a child requesting an extension cannot exceed what the parent
	// was granted". Zero means no parent ceiling applies (the
	// requesting session is the tree root, or the BudgetSource has not
	// resolved a parent lease). When positive, EffectiveMax is further
	// capped at this value before Grant runs so the child cannot
	// overshoot the parent's lease envelope. F-8.6.15.
	// spec: §8.6 line 648
	ParentLeaseTokenCeiling int64

	// CurrentMaxAgeSeconds is the §8.6 line 643 additionalMaxAge
	// dimension's present value (the tree's current perChildMaxAge in
	// seconds). leaseextension.Grant uses it the same way as
	// CurrentTokenBudget for the seconds dimension. F-8.6.11.
	// spec: §8.6 line 643
	CurrentMaxAgeSeconds int64

	// EffectiveMaxMaxAgeSeconds is the §8.6 layered ceiling for the
	// additionalMaxAge dimension, resolved through the
	// deployment/tenant/runtime layering. Zero disables the dimension
	// (a request that asks for additional seconds against a
	// zero-ceiling tree gets a CEILING_REACHED outcome on the seconds
	// dimension while the tokens dimension proceeds independently).
	// F-8.6.11.
	// spec: §8.6 line 643
	EffectiveMaxMaxAgeSeconds int64

	// ParentLeaseMaxAgeCeiling mirrors ParentLeaseTokenCeiling for the
	// seconds dimension: the parent's perChildMaxAge grant value caps
	// the child's effective max-age. Zero disables the per-parent cap.
	// F-8.6.15.
	// spec: §8.6 line 648
	ParentLeaseMaxAgeCeiling int64

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

// NewLimits is the §8.6 line 743 "resulting new limits" pair the audit
// record carries after a grant lands. Both dimensions are absolute
// values (the post-grant currentTokenBudget and currentMaxAgeSeconds
// for the requesting session) so the audit reader does not have to
// add a delta to a prior snapshot to reconstruct the limit. The
// extension scope is per requesting session (§8.6 line 737-741), so
// the values reflect what the requesting session sees — sibling and
// existing-child views are unaffected. F-8.6.10; F-8.6.12.
// spec: §8.6 line 743
type NewLimits struct {
	// TokenBudget is the post-grant currentTokenBudget for the
	// requesting session. Equal to pre-grant + grantedTokens (subject
	// to the §8.6 ceilings).
	TokenBudget int64
	// MaxAgeSeconds is the post-grant currentMaxAgeSeconds for the
	// requesting session. Equal to pre-grant + grantedSeconds (subject
	// to the §8.6 ceilings).
	MaxAgeSeconds int64
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

	// ApplyGrant atomically raises the requesting session's view of
	// the tree budget by grantedTokens / grantedSeconds. §8.6 lines
	// 737-741 scope an extension to the requesting session only:
	// existing children's leases are unaffected, only new children
	// spawned after the extension benefit from the expanded parent
	// budget. The source therefore records the bump against
	// requestingSessionID rather than the root, and TreeBudget keyed
	// by another session in the same tree returns the unchanged
	// base. A Postgres implementation re-checks the extension-denied
	// flag inside the same transaction and returns ErrExtensionDenied
	// when a rejection was persisted between the TreeBudget read and
	// the commit (§8.6 in-flight atomic check). A zero on either
	// dimension is a no-op for that dimension; the returned NewLimits
	// reflect whichever dimensions did land. F-8.6.10; F-8.6.11;
	// F-8.6.12.
	ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, grantedTokens, grantedSeconds int64) (NewLimits, error)

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

	budgets           BudgetSource
	tenants           TenantResolver
	auditing          Auditor
	metrics           MetricEmitter
	clock             func() time.Time
	serviceInstanceID string
	batchIDGen        func() string
	peerIPFn          func(context.Context) string
}

// MetricEmitter is the §16 counter callback the Service drives on
// every ExtendLease decision. *gatewaymetrics.Metrics satisfies it.
// The interface keeps the leasecontrol package free of a direct
// gatewaymetrics dependency (which would create an import cycle once
// gatewaymetrics consumes leasecontrol types). spec: §16 line 66;
// F-8.6.13.
type MetricEmitter interface {
	// IncDelegationLeaseExtension bumps lenny_delegation_lease_extension_total
	// with the §8.6 line 743 (`approved`/`capped`/`denied`) outcome the
	// audit recorded for the same request.
	IncDelegationLeaseExtension(tenantID, outcome string)
}

// Auditor records the §8.6 audit entry for an extension request. The
// gateway wires its audit store; tests pass nil to skip auditing.
type Auditor interface {
	// RecordExtension logs one §8.6 extension decision.
	RecordExtension(ctx context.Context, e ExtensionAudit)
}

// AuditOutcome is the §8.6 line 743 "outcome (approved/denied/capped)"
// classification recorded on every extension audit. It groups the
// proto-level statuses (GRANTED/PARTIALLY_GRANTED/CEILING_REACHED/
// REJECTED) into the three audit-facing categories the spec calls out:
// a full grant is approved, a user rejection (or in-flight denial) is
// denied, and any ceiling-capped outcome is capped — including the
// zero-grant CEILING_REACHED case, which the spec treats as a cap to
// zero rather than a separate audit class.
type AuditOutcome string

const (
	// AuditOutcomeApproved — the full requested amount was granted under
	// the §8.6 ceiling (proto STATUS_GRANTED).
	// spec: §8.6 line 743
	AuditOutcomeApproved AuditOutcome = "approved"
	// AuditOutcomeCapped — the ceiling reduced the grant. Either the
	// grant was non-zero but capped (STATUS_PARTIALLY_GRANTED) or the
	// ceiling was already reached and the grant is zero
	// (STATUS_CEILING_REACHED). The audit class merges both because the
	// spec frames the §8.6 line 743 distinction as approved/denied/capped
	// rather than approved/denied/ceiling.
	// spec: §8.6 line 743
	AuditOutcomeCapped AuditOutcome = "capped"
	// AuditOutcomeDenied — the §8.6 extension-denied flag was set, either
	// when the TreeBudget read returned it or when ApplyGrant detected an
	// in-flight denial (the §8.6 line 732 atomic re-check). The proto
	// status is STATUS_REJECTED in both paths.
	// spec: §8.6 line 743
	AuditOutcomeDenied AuditOutcome = "denied"
)

// auditOutcomeFor maps a §8.6 grant outcome to the audit category. The
// REJECTED path does not go through leaseextension.Grant, so the
// handler picks AuditOutcomeDenied directly.
// spec: §8.6 line 743
func auditOutcomeFor(o leaseextension.Outcome) AuditOutcome {
	switch o {
	case leaseextension.Granted:
		return AuditOutcomeApproved
	case leaseextension.PartiallyGranted, leaseextension.CeilingReached:
		return AuditOutcomeCapped
	default:
		return AuditOutcomeCapped
	}
}

// ExtensionAudit is the §8.6 line 743 audit record for one extension
// request. Every field the spec line 743 enumerates rides on this
// struct: requesting session, requested amounts, approval mode,
// outcome, approver, granted amount, effective max at time of request,
// resulting new limits, batch id, service_instance_id, client_ip.
// F-8.6.10.
type ExtensionAudit struct {
	TenantID         string
	RootSessionID    string
	RequestSessionID string
	RequestedTokens  int64
	GrantedTokens    int64
	// RequestedSeconds and GrantedSeconds carry the §8.6 line 643
	// additionalMaxAge dimension so the audit reflects both budget
	// dimensions, not just tokens. Zero on either pair indicates the
	// caller did not request that dimension. F-8.6.11.
	RequestedSeconds int64
	GrantedSeconds   int64
	EffectiveMax     int64
	// Outcome is the §8.6 line 743 approved/denied/capped classification.
	Outcome AuditOutcome
	// ApprovalMode is the §8.6 line 743 resolved approval mode the
	// audit reader uses to correlate auto-grants vs elicitation
	// outcomes. The resolved mode is the deployment→tenant→runtime
	// layered value the dispatcher consumes; the §8.6 line 714
	// elicitation flow lands separately, so v1 always records the
	// tree's registered mode (defaulting to elicitation per spec line
	// 674) without yet driving the dispatcher. F-8.6.10.
	ApprovalMode ApprovalMode
	// Approver is the §8.6 line 743 "approver (gateway-auto or client)"
	// classification: `gateway-auto` when the grant did not surface an
	// elicitation (auto-mode, or a fallthrough during cool-off), and
	// `client` on the rejection path because the cool-off itself is
	// the user's denial echoed back. The §8.6 line 714 elicitation
	// path will tag approved/denied client outcomes with the same
	// `client` value when it lands; the spec value space is closed
	// at these two strings. F-8.6.10.
	Approver string
	// BatchID is the §8.6 line 743 batch identifier that groups
	// requests tied to the same elicitation + cool-off period. Until
	// the §8.6 line 714 dispatcher lands, every request is its own
	// elicitation episode and the BatchID is generated fresh per
	// request — a deterministic ULID-shaped string carrying the
	// request's clock instant so a batch id sorts chronologically.
	// F-8.6.10.
	BatchID string
	// ServiceInstanceID is the §16.1.1 service.instance.id of the
	// gateway replica that handled this request. Sourced from the
	// gateway's --replica-id flag (the same value §10.1 coordination
	// uses) so the audit reader can identify which replica issued the
	// grant when investigating a fleet-wide incident. Empty when the
	// Service was constructed without a ServiceInstanceID option.
	// F-8.6.10.
	// spec: §16.1.1
	ServiceInstanceID string
	// ClientIP is the §8.6 line 743 originator IP, sourced from the
	// gRPC peer.FromContext on the inbound ExtendLease call. Empty
	// when no peer is available (unit tests; in-process callers).
	// F-8.6.10.
	ClientIP string
	// NewLimits is the §8.6 line 743 "resulting new limits" — the
	// post-grant per-session view of currentTokenBudget and
	// currentMaxAgeSeconds. F-8.6.10; F-8.6.12.
	NewLimits NewLimits
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

	// Metrics drives the §16 lenny_delegation_lease_extension_total
	// counter. Optional; nil skips metric emission. F-8.6.13.
	Metrics MetricEmitter

	// Clock overrides time.Now for cool-off comparisons. Pass nil for
	// production.
	Clock func() time.Time

	// ServiceInstanceID is the §16.1.1 service.instance.id this
	// replica reports on every ExtensionAudit so the audit reader can
	// trace which gateway replica handled the request. Empty is
	// permitted for tests; production wires the gateway's resolved
	// replica id. F-8.6.10.
	// spec: §16.1.1
	ServiceInstanceID string

	// BatchIDGen produces the §8.6 line 743 batch identifier the audit
	// carries. Nil selects a built-in generator that returns a
	// chronologically-sortable random string per invocation — until the
	// §8.6 line 714 elicitation dispatcher lands, every request is its
	// own batch. F-8.6.10.
	BatchIDGen func() string

	// PeerIPFn extracts the §8.6 line 743 client_ip from the request
	// context. Nil selects defaultPeerIP, which reads
	// google.golang.org/grpc/peer; tests can substitute a deterministic
	// fake. F-8.6.10.
	PeerIPFn func(context.Context) string
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
	batchGen := opts.BatchIDGen
	if batchGen == nil {
		batchGen = defaultBatchID
	}
	peerFn := opts.PeerIPFn
	if peerFn == nil {
		peerFn = defaultPeerIP
	}
	return &Service{
		budgets:           opts.Budgets,
		tenants:           opts.Tenants,
		auditing:          opts.Auditing,
		metrics:           opts.Metrics,
		clock:             clock,
		serviceInstanceID: opts.ServiceInstanceID,
		batchIDGen:        batchGen,
		peerIPFn:          peerFn,
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

	mode := s.approvalModeFor(ctx, tenantID, budget.RootSessionID)

	// §8.6 rejection cool-off. When the subtree is extension-denied and
	// the cool-off has not expired, auto-reject without entering the
	// elicitation path. The BudgetSource has already compared
	// CoolOffExpiry against the authoritative clock; a zero expiry with
	// the flag set is treated as still in effect.
	if budget.ExtensionDenied && (budget.CoolOffExpiry.IsZero() || s.clock().Before(budget.CoolOffExpiry)) {
		s.audit(ctx, tenantID, budget, sessionID, req.GetRequestedTokens(), 0, AuditOutcomeDenied, mode)
		// spec: §15.1 line 1080 — REJECTED carries details.subtreeId and
		// details.coolOffExpiresAt (UTC RFC 3339) so clients can identify
		// the denied subtree and the cool-off window end without parsing
		// the legacy unix-ms field. The Error envelope mirrors the §15.1
		// EXTENSION_COOL_OFF_ACTIVE typed code so admin tooling can
		// distinguish "in an existing cool-off window" from other
		// REJECTED outcomes without re-parsing details. F-8.6.9.
		return &adapterv1.ExtendLeaseResponse{
			Status:              adapterv1.ExtendLeaseResponse_STATUS_REJECTED,
			GrantedTokens:       0,
			CoolOffExpiryUnixMs: budget.CoolOffExpiry.UnixMilli(),
			SubtreeId:           sessionID,
			CoolOffExpiresAt:    formatCoolOffExpiry(budget.CoolOffExpiry),
			Error:               coolOffActiveError(),
		}, nil
	}

	// spec: §8.6 line 648 — the parent's own lease limits cap the child's
	// extension. ParentLeaseTokenCeiling is set by the BudgetSource when
	// the requesting session is a delegated child whose parent's lease
	// envelope is narrower than the layered deployment/tenant/runtime
	// ceiling. The handler treats it as a second hard ceiling: the grant
	// math runs against the smaller of the two. A zero ceiling means
	// "no parent cap" (root session or unknown parent). F-8.6.15.
	tokenCeiling := budget.EffectiveMax
	if budget.ParentLeaseTokenCeiling > 0 && budget.ParentLeaseTokenCeiling < tokenCeiling {
		tokenCeiling = budget.ParentLeaseTokenCeiling
	}
	granted, outcome := leaseextension.Grant(
		budget.CurrentTokenBudget,
		req.GetRequestedTokens(),
		tokenCeiling,
	)

	// spec: §8.6 line 643 — the additionalMaxAge dimension shares the
	// same Grant math against its own §8.6 ceiling and parent-lease
	// cap. The seconds dimension is independent of the tokens
	// dimension: a request that exhausts only one of them still
	// applies the other. F-8.6.11.
	maxAgeCeiling := budget.EffectiveMaxMaxAgeSeconds
	if budget.ParentLeaseMaxAgeCeiling > 0 && budget.ParentLeaseMaxAgeCeiling < maxAgeCeiling {
		maxAgeCeiling = budget.ParentLeaseMaxAgeCeiling
	}
	requestedSeconds := int64(req.GetRequestedSeconds())
	grantedSeconds, secondsOutcome := leaseextension.Grant(
		budget.CurrentMaxAgeSeconds,
		requestedSeconds,
		maxAgeCeiling,
	)

	newLimits := NewLimits{
		TokenBudget:   budget.CurrentTokenBudget,
		MaxAgeSeconds: budget.CurrentMaxAgeSeconds,
	}
	if granted > 0 || grantedSeconds > 0 {
		applied, err := s.budgets.ApplyGrant(ctx, tenantID, budget.RootSessionID, sessionID, granted, grantedSeconds)
		if err != nil {
			// §8.6 in-flight atomic check: a REJECTED outcome was
			// persisted between the TreeBudget read and the commit.
			// Surface it as a REJECTED extension response rather than
			// granting.
			if errors.Is(err, ErrExtensionDenied) {
				coolOff := s.resolveCoolOff(ctx, tenantID, budget.RootSessionID)
				expiry := s.clock().Add(coolOff)
				s.audit(ctx, tenantID, budget, sessionID, req.GetRequestedTokens(), 0, AuditOutcomeDenied, mode)
				// spec: §15.1 line 1080 — REJECTED carries the subtreeId
				// and the UTC RFC 3339 cool-off expiry. The §8.6 line 731
				// in-flight atomic re-check surfaces the same typed
				// EXTENSION_COOL_OFF_ACTIVE code as the pre-grant rejection
				// so clients see one canonical reason for both paths.
				// F-8.6.9.
				return &adapterv1.ExtendLeaseResponse{
					Status:              adapterv1.ExtendLeaseResponse_STATUS_REJECTED,
					GrantedTokens:       0,
					CoolOffExpiryUnixMs: expiry.UnixMilli(),
					SubtreeId:           sessionID,
					CoolOffExpiresAt:    formatCoolOffExpiry(expiry),
					Error:               coolOffActiveError(),
				}, nil
			}
			return nil, fmt.Errorf("leasecontrol: apply grant for tree %s: %w", budget.RootSessionID, err)
		}
		newLimits = applied
	}

	combined := combineOutcomes(
		req.GetRequestedTokens(), requestedSeconds,
		granted, grantedSeconds,
		outcome, secondsOutcome,
	)
	status := statusFor(combined)
	s.auditFull(ctx, tenantID, budget, sessionID,
		req.GetRequestedTokens(), granted,
		requestedSeconds, grantedSeconds,
		auditOutcomeFor(combined),
		auditExtras{approvalMode: mode, newLimits: newLimits})
	return &adapterv1.ExtendLeaseResponse{
		Status:         status,
		GrantedTokens:  granted,
		GrantedSeconds: int32(grantedSeconds),
	}, nil
}

// combineOutcomes folds the per-dimension §8.6 grant outcomes for
// tokens and seconds into one response-level outcome. A dimension the
// caller did not request (zero requested) is treated as already
// satisfied and does not pull the response status down. When both
// dimensions land Granted, the response is Granted. When the combined
// grant is zero across all requested dimensions the response is
// CeilingReached. Otherwise the response is PartiallyGranted.
// spec: §8.6 line 643; F-8.6.11
func combineOutcomes(reqTokens, reqSeconds, grantedTokens, grantedSeconds int64, tokensOut, secondsOut leaseextension.Outcome) leaseextension.Outcome {
	switch {
	case reqTokens == 0 && reqSeconds == 0:
		return leaseextension.Granted
	case reqTokens == 0:
		return secondsOut
	case reqSeconds == 0:
		return tokensOut
	case tokensOut == leaseextension.Granted && secondsOut == leaseextension.Granted:
		return leaseextension.Granted
	case grantedTokens == 0 && grantedSeconds == 0:
		return leaseextension.CeilingReached
	default:
		return leaseextension.PartiallyGranted
	}
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

// formatCoolOffExpiry renders the §15.1 line 1080 details.coolOffExpiresAt
// field as a UTC RFC 3339 string. A zero time renders empty so clients
// can distinguish "no cool-off recorded" from a valid expiry.
// spec: §15.1 line 1080
func formatCoolOffExpiry(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// coolOffActiveError returns the §15.1 line 1080 typed error envelope
// embedded on every REJECTED ExtendLease response whose rejection
// reason is "the requesting subtree is in an extension cool-off
// window" — both the pre-grant rejection check and the §8.6 line 731
// in-flight atomic re-check. The category is POLICY because the
// rejection is a deployment policy decision the caller cannot retry
// around; retryable is false because the adapter MUST NOT loop on the
// extension request inside the window (re-issuing trips the same
// cool-off, see §8.6 line 629 retry contract).
// spec: §15.1 line 1080; F-8.6.9
func coolOffActiveError() *adapterv1.Error {
	return &adapterv1.Error{
		Code:      adapterv1.Error_ERROR_CODE_EXTENSION_COOL_OFF_ACTIVE,
		Category:  adapterv1.Error_CATEGORY_POLICY,
		Message:   "subtree is in an extension cool-off window after a user-denied extension elicitation",
		Retryable: false,
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

// approvalModeProvider is the optional BudgetSource extension that
// reports the tree's resolved §8.6 approval mode. MemoryBudgetSource
// implements it; the Postgres-backed source will satisfy it once the
// dispatcher (F-8.6.2) lands. The handler falls back to
// DefaultApprovalMode when a source does not provide the mode.
// F-8.6.10.
// spec: §8.6 line 674
type approvalModeProvider interface {
	ApprovalMode(ctx context.Context, tenantID, rootSessionID string) ApprovalMode
}

// approvalModeFor resolves the §8.6 approval mode for the tree the
// requesting session belongs to. Sources that satisfy
// approvalModeProvider report the registered mode; others fall back
// to DefaultApprovalMode. F-8.6.10.
// spec: §8.6 line 674
func (s *Service) approvalModeFor(ctx context.Context, tenantID, rootSessionID string) ApprovalMode {
	if p, ok := s.budgets.(approvalModeProvider); ok {
		return p.ApprovalMode(ctx, tenantID, rootSessionID)
	}
	return DefaultApprovalMode
}

// audit records one §8.6 extension cool-off rejection. The cool-off
// path never carries a seconds dimension because no grant is applied;
// the audit records the requested tokens as the elicitation surface
// the user denied, the (zero) post-grant new limits, and the resolved
// approval mode for the tree. The cool-off rejection is always
// attributed to the user via the Approver override, since the
// rejection itself is the user's prior denial echoed back per §8.6
// line 743. F-8.6.10.
// spec: §8.6 line 743
func (s *Service) audit(ctx context.Context, tenantID string, b TreeBudget, sessionID string, requested, granted int64, outcome AuditOutcome, mode ApprovalMode) {
	s.auditFull(ctx, tenantID, b, sessionID, requested, granted, 0, 0, outcome, auditExtras{
		approvalMode: mode,
		newLimits: NewLimits{
			TokenBudget:   b.CurrentTokenBudget,
			MaxAgeSeconds: b.CurrentMaxAgeSeconds,
		},
		approverOverride: "client",
	})
}

// auditFull records one §8.6 extension decision with both budget
// dimensions, when an Auditor is wired. The metric counter side-effect
// rides on the same audit-emit so a deployment with no Auditor still
// drives the §16 dashboard counter — operators rely on the metric
// regardless of whether they have wired the §11.7 audit chain. The
// `extra` carries the per-request fields that do not derive from the
// TreeBudget snapshot: ApprovalMode (resolved), the post-grant
// NewLimits, and (for the cool-off helper) a forced Approver override.
// F-8.6.10; F-8.6.11; F-8.6.13.
// spec: §8.6 line 743
type auditExtras struct {
	approvalMode    ApprovalMode
	newLimits       NewLimits
	approverOverride string
}

func (s *Service) auditFull(ctx context.Context, tenantID string, b TreeBudget, sessionID string, requestedTokens, grantedTokens, requestedSeconds, grantedSeconds int64, outcome AuditOutcome, extras auditExtras) {
	if s.metrics != nil {
		s.metrics.IncDelegationLeaseExtension(tenantID, string(outcome))
	}
	if s.auditing == nil {
		return
	}
	mode := extras.approvalMode
	if mode == ApprovalModeUnspecified {
		mode = DefaultApprovalMode
	}
	approver := extras.approverOverride
	if approver == "" {
		approver = approverFor(outcome, mode)
	}
	s.auditing.RecordExtension(ctx, ExtensionAudit{
		TenantID:          tenantID,
		RootSessionID:     b.RootSessionID,
		RequestSessionID:  sessionID,
		RequestedTokens:   requestedTokens,
		GrantedTokens:     grantedTokens,
		RequestedSeconds:  requestedSeconds,
		GrantedSeconds:    grantedSeconds,
		EffectiveMax:      b.EffectiveMax,
		Outcome:           outcome,
		ApprovalMode:      mode,
		Approver:          approver,
		BatchID:           s.batchIDGen(),
		ServiceInstanceID: s.serviceInstanceID,
		ClientIP:          s.peerIPFn(ctx),
		NewLimits:         extras.newLimits,
	})
}

// approverFor maps the §8.6 line 743 outcome + resolved approval mode
// to the closed-string Approver vocabulary. The spec lists two
// values: `gateway-auto` for grants the gateway issued without
// soliciting client input, and `client` for outcomes where a user (or
// the user's denial via the cool-off window) drove the decision.
// F-8.6.10.
// spec: §8.6 line 743
func approverFor(outcome AuditOutcome, mode ApprovalMode) string {
	switch {
	case outcome == AuditOutcomeDenied:
		// Cool-off rejection is the user's previous denial echoed back.
		return "client"
	case mode == ApprovalModeAuto:
		return "gateway-auto"
	default:
		// Elicitation mode where the gateway auto-grants up to the
		// ceiling without the dispatcher (§8.6 line 714) yet wired
		// remains `gateway-auto` until that dispatcher lands and a
		// client decision is observed.
		return "gateway-auto"
	}
}

// defaultBatchID returns a chronologically-sortable 24-character batch
// identifier: 13 hex chars of the current millis followed by 11 hex
// chars of crypto/rand entropy. The clock prefix means a batch id
// sorts in approximate temporal order without requiring the audit
// reader to load every prior record. Until the §8.6 line 714
// elicitation dispatcher lands and groups multiple requests under one
// batch, every request is its own batch. F-8.6.10.
// spec: §8.6 line 743
func defaultBatchID() string {
	now := time.Now().UTC().UnixMilli()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read does not return an error on Unix/macOS/Windows;
		// the failure mode is unreachable. Use a zero suffix when it
		// somehow does, so the batch id stays well-formed.
		for i := range b {
			b[i] = 0
		}
	}
	return fmt.Sprintf("ext_%013x_%s", now, hex.EncodeToString(b[:]))
}

// defaultPeerIP extracts the §8.6 line 743 client_ip from the gRPC
// peer attached to the context. Returns an empty string when no peer
// is present (in-process callers, unit tests) or when the address
// cannot be parsed. F-8.6.10.
// spec: §8.6 line 743
func defaultPeerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p == nil || p.Addr == nil {
		return ""
	}
	addr := p.Addr.String()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		// SplitHostPort fails for bufconn-style "bufconn" addresses
		// and any other format without a port; fall through to the
		// raw address so the audit reader still sees something.
		return strings.TrimSpace(addr)
	}
	return host
}
