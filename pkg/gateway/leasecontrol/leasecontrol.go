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

	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
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

// Dimensions carries the §8.6 line 643 extendable budget dimensions as
// a fixed-shape value so the handler runs one Grant pass per dimension
// without duplicating the logic six times. The same struct plays every
// role in the extension flow: a request's requested amounts, a tree's
// current values, its layered effective ceilings, the parent-lease
// caps, the per-dimension grant, and the post-grant absolute limits.
// Zero on a field means "no value" for whatever role the struct plays
// (no request, no ceiling, no grant).
//
// The §8.6 extendable set is maxTokenBudget, perChildMaxAge,
// maxChildrenTotal, maxParallelChildren, maxTreeSize, and
// fileExportLimits (decomposed into its maxFiles and maxTotalSize
// components). maxDepth, minIsolationProfile, delegationPolicyRef,
// perChildRetryBudget, treeVisibility, and allowSelfRecursion are
// security or reliability boundaries and are deliberately absent.
// spec: §8.6 line 643; F-8.6.1
type Dimensions struct {
	// Tokens is the maxTokenBudget dimension (additionalTokenBudget).
	Tokens int64
	// Seconds is the perChildMaxAge dimension (additionalMaxAge).
	Seconds int64
	// Children is the maxChildrenTotal dimension (additionalChildren).
	Children int64
	// ParallelChildren is the maxParallelChildren dimension.
	ParallelChildren int64
	// TreeSize is the maxTreeSize dimension.
	TreeSize int64
	// FileExportFiles is the fileExportLimits.maxFiles component.
	FileExportFiles int64
	// FileExportBytes is the fileExportLimits.maxTotalSize component.
	FileExportBytes int64
}

// anyPositive reports whether any dimension carries a positive value —
// the test the handler uses to skip ApplyGrant when nothing was granted.
func (d Dimensions) anyPositive() bool {
	return d.Tokens > 0 || d.Seconds > 0 || d.Children > 0 ||
		d.ParallelChildren > 0 || d.TreeSize > 0 ||
		d.FileExportFiles > 0 || d.FileExportBytes > 0
}

// add returns the per-dimension sum of d and o. Grant amounts are never
// negative, so this is how a session's base value plus its accumulated
// extension delta (or two deltas) compose. F-8.6.1.
func (d Dimensions) add(o Dimensions) Dimensions {
	return Dimensions{
		Tokens:           d.Tokens + o.Tokens,
		Seconds:          d.Seconds + o.Seconds,
		Children:         d.Children + o.Children,
		ParallelChildren: d.ParallelChildren + o.ParallelChildren,
		TreeSize:         d.TreeSize + o.TreeSize,
		FileExportFiles:  d.FileExportFiles + o.FileExportFiles,
		FileExportBytes:  d.FileExportBytes + o.FileExportBytes,
	}
}

// dimLens reads and writes one §8.6 dimension on a Dimensions value, so
// the handler and the budget source iterate every dimension uniformly
// rather than repeating per-dimension code. F-8.6.1.
type dimLens struct {
	name string
	get  func(Dimensions) int64
	set  func(*Dimensions, int64)
}

// allDims is the §8.6 line 643 extendable dimension set in a fixed
// order. Iterating it is the single place that enumerates the
// dimensions; adding one is a single append here. F-8.6.1.
var allDims = []dimLens{
	{"tokens", func(d Dimensions) int64 { return d.Tokens }, func(d *Dimensions, v int64) { d.Tokens = v }},
	{"seconds", func(d Dimensions) int64 { return d.Seconds }, func(d *Dimensions, v int64) { d.Seconds = v }},
	{"children", func(d Dimensions) int64 { return d.Children }, func(d *Dimensions, v int64) { d.Children = v }},
	{"parallel_children", func(d Dimensions) int64 { return d.ParallelChildren }, func(d *Dimensions, v int64) { d.ParallelChildren = v }},
	{"tree_size", func(d Dimensions) int64 { return d.TreeSize }, func(d *Dimensions, v int64) { d.TreeSize = v }},
	{"file_export_files", func(d Dimensions) int64 { return d.FileExportFiles }, func(d *Dimensions, v int64) { d.FileExportFiles = v }},
	{"file_export_bytes", func(d Dimensions) int64 { return d.FileExportBytes }, func(d *Dimensions, v int64) { d.FileExportBytes = v }},
}

// TreeBudget is the §8.6 per-tree extension state for one delegation
// tree, keyed by its root session. A BudgetSource returns it for the
// requesting session's tree.
type TreeBudget struct {
	// RootSessionID identifies the delegation tree (the
	// delegation_tree_budget row key).
	RootSessionID string

	// Current holds each extendable dimension's present value — the
	// limit an extension increases. leaseextension.Grant treats each as
	// its dimension's current value.
	Current Dimensions

	// EffectiveMax holds each dimension's §8.6 layered ceiling, already
	// resolved through the deployment, tenant, and runtime
	// configuration (see leaseextension.ResolveEffectiveMax for the
	// tokens dimension). An extension can never push a dimension's
	// Current above its EffectiveMax. A zero ceiling disables that
	// dimension: a request against it yields CEILING_REACHED for that
	// dimension while the others proceed independently. F-8.6.1.
	EffectiveMax Dimensions

	// ParentCeiling holds the §8.6 line 648 second hard ceiling per
	// dimension: "a child requesting an extension cannot exceed what the
	// parent was granted". Zero on a dimension means no parent ceiling
	// applies (the requesting session is the tree root, or the
	// BudgetSource has not resolved a parent lease). When positive, the
	// dimension's effective ceiling is further capped at this value
	// before Grant runs. F-8.6.15.
	// spec: §8.6 line 648
	ParentCeiling Dimensions

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

// NewLimits is the §8.6 line 743 "resulting new limits" the audit
// record carries after a grant lands. Every dimension is an absolute
// post-grant value (pre-grant + granted, subject to the §8.6 ceilings)
// for the requesting session, so the audit reader does not add a delta
// to a prior snapshot to reconstruct a limit. The extension scope is
// per requesting session (§8.6 line 737-741), so the values reflect
// what the requesting session sees — sibling and existing-child views
// are unaffected. F-8.6.10; F-8.6.12.
// spec: §8.6 line 743
type NewLimits = Dimensions

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
	// the tree budget by the per-dimension grant in granted. §8.6 lines
	// 737-741 scope an extension to the requesting session only:
	// existing children's leases are unaffected, only new children
	// spawned after the extension benefit from the expanded parent
	// budget. The source therefore records the bump against
	// requestingSessionID rather than the root, and TreeBudget keyed
	// by another session in the same tree returns the unchanged
	// base. A Postgres implementation re-checks the extension-denied
	// flag inside the same transaction and returns ErrExtensionDenied
	// when a rejection was persisted between the TreeBudget read and
	// the commit (§8.6 in-flight atomic check). A zero on a dimension
	// is a no-op for that dimension; the returned NewLimits reflect
	// whichever dimensions did land. F-8.6.1; F-8.6.10; F-8.6.11;
	// F-8.6.12.
	ApplyGrant(ctx context.Context, tenantID, rootSessionID, requestingSessionID string, granted Dimensions) (NewLimits, error)

	// RejectionCoolOff returns the §8.6 rejectionCoolOffSeconds for the
	// tree, resolved through the deployment, tenant, and runtime
	// layering. A zero return means the caller applies
	// DefaultRejectionCoolOff.
	RejectionCoolOff(ctx context.Context, tenantID, rootSessionID string) time.Duration

	// Deny marks the requesting subtree extension-denied and starts the
	// §8.6 line 734 rejection cool-off, the durable record of a user's
	// rejection of a budget elicitation. The elicitation coordinator
	// calls it on the §8.6 line 729 reject path so subsequent requests
	// from the subtree are auto-rejected during the cool-off. A Postgres
	// implementation keys the flag with a per-row subtree id and persists
	// it to delegation_tree_budget so a coordinator handoff cannot bypass
	// the denial (§8.6 line 730); the in-memory source applies it
	// tree-wide. spec: §8.6 line 729, line 734.
	Deny(ctx context.Context, tenantID, rootSessionID, requestingSessionID string) error
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

	// coordinator drives the §8.6 line 714 elicitation-mode approval flow.
	// It is nil when no Elicitor is wired, in which case an
	// elicitation-mode request fails closed rather than auto-granting.
	// F-8.6.2.
	coordinator *elicitCoordinator

	// autoLimiter enforces the §8.6 line 712 auto-mode rate limit. It is
	// always present (in-memory counter when none is supplied) but inert
	// unless a tree or the deployment default sets a positive
	// maxAutoExtensionsPerMinute. F-8.6.7.
	autoLimiter *autoExtensionLimiter

	// defaultAutoMaxPerMinute is the §8.6 line 712 deployment-default
	// maxAutoExtensionsPerMinute applied when a tree carries no more
	// specific value. Zero means no limit. F-8.6.7.
	defaultAutoMaxPerMinute int
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

	// RecordAutoRateLimitExceeded logs the §8.6 line 712
	// `lease_extension_auto_rate_limit_exceeded` event: an auto-mode
	// extension request tripped the tree's maxAutoExtensionsPerMinute, so
	// the gateway paused auto-approval and fell back to elicitation for
	// the remainder of the window. F-8.6.7.
	RecordAutoRateLimitExceeded(ctx context.Context, e AutoRateLimitAudit)
}

// AutoRateLimitAudit is the §8.6 line 712 audit record for an auto-mode
// rate-limit fallback. It carries the tree, the requesting session, and
// the issuing replica so an operator can correlate the safety-valve
// trip with the surrounding extension activity. F-8.6.7.
type AutoRateLimitAudit struct {
	TenantID         string
	RootSessionID    string
	RequestSessionID string
	// MaxPerMinute is the resolved maxAutoExtensionsPerMinute the request
	// exceeded. F-8.6.7.
	MaxPerMinute int
	// ServiceInstanceID is the §16.1.1 service.instance.id of the gateway
	// replica that recorded the fallback.
	ServiceInstanceID string
	// ClientIP is the originating IP from the gRPC peer, empty for
	// in-process callers and unit tests.
	ClientIP string
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
	// Requested and Granted carry every §8.6 line 643 extendable
	// dimension (tokens, seconds, children, parallel-children,
	// tree-size, and the two fileExportLimits components) so the audit
	// reflects all budget dimensions, not just tokens. A zero on a
	// Requested dimension indicates the caller did not ask for it.
	// F-8.6.1; F-8.6.11.
	Requested Dimensions
	Granted   Dimensions
	// EffectiveMax is the §8.6 line 743 "effective max at time of
	// request" for the tokens dimension — the primary maxExtendableBudget
	// ceiling the spec's configuration layering resolves.
	EffectiveMax int64
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

	// Elicitor presents the §8.6 line 718 generic budget elicitation and
	// blocks for the user's decision. When set, ExtendLease enforces
	// elicitation-mode consent: an elicitation-mode tree no longer
	// auto-grants. When nil, an elicitation-mode request fails closed
	// (the gateway cannot obtain consent), while auto-mode trees are
	// unaffected. Production wires an Elicitor over the §9.2 interaction
	// store and client event stream. F-8.6.2.
	Elicitor Elicitor

	// AutoExtensionCounter backs the §8.6 line 712 auto-mode rate limit.
	// ExtendLease tracks auto-mode extension requests per tree per minute
	// and falls back to elicitation once a tree exceeds its resolved
	// maxAutoExtensionsPerMinute. Nil selects an in-memory per-replica
	// counter; a Redis-backed counter makes the window cross-replica.
	// F-8.6.7.
	AutoExtensionCounter ratelimit.Counter

	// DefaultAutoMaxPerMinute is the §8.6 line 712 deployment-default
	// maxAutoExtensionsPerMinute. It applies to a tree that does not carry
	// a more specific (tenant/runtime) value, so the safety valve is
	// operable from a single deployment knob even before per-tree config
	// is registered. Zero is the spec default (no limit). F-8.6.7.
	// spec: §8.6 line 712
	DefaultAutoMaxPerMinute int
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
	svc := &Service{
		budgets:           opts.Budgets,
		tenants:           opts.Tenants,
		auditing:          opts.Auditing,
		metrics:           opts.Metrics,
		clock:             clock,
		serviceInstanceID: opts.ServiceInstanceID,
		batchIDGen:        batchGen,
		peerIPFn:          peerFn,
	}
	if opts.Elicitor != nil {
		svc.coordinator = newElicitCoordinator(opts.Elicitor, opts.Budgets, clock)
	}
	svc.autoLimiter = newAutoExtensionLimiter(opts.AutoExtensionCounter)
	svc.defaultAutoMaxPerMinute = opts.DefaultAutoMaxPerMinute
	return svc, nil
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
	requested := requestedDimensions(req)

	// §8.6 rejection cool-off. When the subtree is extension-denied and
	// the cool-off has not expired, auto-reject without entering the
	// elicitation path. The BudgetSource has already compared
	// CoolOffExpiry against the authoritative clock; a zero expiry with
	// the flag set is treated as still in effect.
	if budget.ExtensionDenied && (budget.CoolOffExpiry.IsZero() || s.clock().Before(budget.CoolOffExpiry)) {
		s.audit(ctx, tenantID, budget, sessionID, requested, AuditOutcomeDenied, mode)
		// spec: §15.1 line 1080 — REJECTED carries details.subtreeId and
		// details.coolOffExpiresAt (UTC RFC 3339) plus the typed
		// EXTENSION_COOL_OFF_ACTIVE error so admin tooling can distinguish
		// "in an existing cool-off window" from other REJECTED outcomes.
		// F-8.6.9.
		return rejectedResponse(sessionID, budget.CoolOffExpiry), nil
	}

	// §8.6 approval gate (F-8.6.2; F-8.6.7). In auto mode the request is
	// granted independently up to the ceiling unless the tree has tripped
	// its maxAutoExtensionsPerMinute, in which case it falls back to
	// elicitation. In elicitation mode the gate solicits the user's
	// consent before any budget moves; a rejection returns a terminal
	// REJECTED response and persists the subtree denial. The resolved
	// approver attribution flows into the §8.6 line 743 audit.
	g := s.gate(ctx, mode, tenantID, budget, sessionID, requested)
	if g.err != nil {
		return nil, g.err
	}
	if !g.proceed {
		return g.resp, nil
	}

	// spec: §8.6 line 643/648 — run the same Grant math over every
	// extendable dimension against the smaller of its layered effective
	// ceiling and its parent-lease cap (§8.6 line 648). Dimensions are
	// independent: a request that exhausts one of them still applies the
	// others. allDims is the single enumeration of the extendable set.
	// F-8.6.1; F-8.6.11; F-8.6.15.
	var granted Dimensions
	outcomes := make([]dimOutcome, 0, len(allDims))
	for _, d := range allDims {
		ceiling := dimCeiling(d.get(budget.EffectiveMax), d.get(budget.ParentCeiling))
		g, o := leaseextension.Grant(d.get(budget.Current), d.get(requested), ceiling)
		d.set(&granted, g)
		outcomes = append(outcomes, dimOutcome{requested: d.get(requested), granted: g, outcome: o})
	}

	newLimits := budget.Current
	if granted.anyPositive() {
		applied, err := s.budgets.ApplyGrant(ctx, tenantID, budget.RootSessionID, sessionID, granted)
		if err != nil {
			// §8.6 in-flight atomic check: a REJECTED outcome was
			// persisted between the TreeBudget read and the commit.
			// Surface it as a REJECTED extension response rather than
			// granting, with the same typed EXTENSION_COOL_OFF_ACTIVE
			// reason as the pre-grant rejection. F-8.6.9.
			if errors.Is(err, ErrExtensionDenied) {
				expiry := s.clock().Add(s.resolveCoolOff(ctx, tenantID, budget.RootSessionID))
				s.audit(ctx, tenantID, budget, sessionID, requested, AuditOutcomeDenied, mode)
				return rejectedResponse(sessionID, expiry), nil
			}
			return nil, fmt.Errorf("leasecontrol: apply grant for tree %s: %w", budget.RootSessionID, err)
		}
		newLimits = applied
	}

	combined := combineOutcomes(outcomes)
	status := statusFor(combined)
	s.auditFull(ctx, tenantID, budget, sessionID, requested, granted,
		auditOutcomeFor(combined),
		auditExtras{approvalMode: mode, newLimits: newLimits, approverOverride: g.approver})
	return &adapterv1.ExtendLeaseResponse{
		Status:                  status,
		GrantedTokens:           granted.Tokens,
		GrantedSeconds:          int32(granted.Seconds),
		GrantedChildren:         granted.Children,
		GrantedParallelChildren: granted.ParallelChildren,
		GrantedTreeSize:         granted.TreeSize,
		GrantedFileExportLimits: fileExportDelta(granted),
	}, nil
}

// requestedDimensions lifts the §8.6 line 643 requested amounts off the
// wire request into a Dimensions value. The proto getters are nil-safe,
// so an absent file_export_limits message reads as zero on both of its
// components. F-8.6.1.
func requestedDimensions(req *adapterv1.ExtendLeaseRequest) Dimensions {
	fe := req.GetRequestedFileExportLimits()
	return Dimensions{
		Tokens:           req.GetRequestedTokens(),
		Seconds:          int64(req.GetRequestedSeconds()),
		Children:         req.GetRequestedChildren(),
		ParallelChildren: req.GetRequestedParallelChildren(),
		TreeSize:         req.GetRequestedTreeSize(),
		FileExportFiles:  fe.GetAdditionalMaxFiles(),
		FileExportBytes:  fe.GetAdditionalMaxBytes(),
	}
}

// fileExportDelta packs the §8.6 fileExportLimits grant back onto the
// wire, returning nil when neither component was granted so the response
// omits an empty message. F-8.6.1.
func fileExportDelta(d Dimensions) *adapterv1.FileExportLimitsDelta {
	if d.FileExportFiles == 0 && d.FileExportBytes == 0 {
		return nil
	}
	return &adapterv1.FileExportLimitsDelta{
		AdditionalMaxFiles: d.FileExportFiles,
		AdditionalMaxBytes: d.FileExportBytes,
	}
}

// rejectedResponse builds the §8.6 / §15.1 REJECTED ExtendLease response
// for an extension auto-rejected because the subtree is in a rejection
// cool-off window. Both the pre-grant check and the in-flight atomic
// re-check return it. F-8.6.9.
// spec: §15.1 line 1080
func rejectedResponse(subtreeID string, expiry time.Time) *adapterv1.ExtendLeaseResponse {
	return &adapterv1.ExtendLeaseResponse{
		Status:              adapterv1.ExtendLeaseResponse_STATUS_REJECTED,
		CoolOffExpiryUnixMs: expiry.UnixMilli(),
		SubtreeId:           subtreeID,
		CoolOffExpiresAt:    formatCoolOffExpiry(expiry),
		Error:               coolOffActiveError(),
	}
}

// dimCeiling returns the §8.6 effective ceiling for one dimension: its
// layered effective max, further capped by the parent-lease ceiling
// when that is positive and smaller (§8.6 line 648). F-8.6.1; F-8.6.15.
func dimCeiling(effMax, parentCap int64) int64 {
	if parentCap > 0 && parentCap < effMax {
		return parentCap
	}
	return effMax
}

// dimOutcome pairs one dimension's requested and granted amounts with
// its Grant outcome, for combineOutcomes to fold. F-8.6.1.
type dimOutcome struct {
	requested int64
	granted   int64
	outcome   leaseextension.Outcome
}

// combineOutcomes folds the per-dimension §8.6 grant outcomes into one
// response-level outcome. A dimension the caller did not request (zero
// requested) is treated as already satisfied and does not pull the
// response status down. When every requested dimension landed Granted,
// the response is Granted; when no requested dimension was granted any
// amount, the response is CeilingReached; otherwise PartiallyGranted. A
// request with no requested dimension at all is Granted.
// spec: §8.6 line 643; F-8.6.1; F-8.6.11
func combineOutcomes(dims []dimOutcome) leaseextension.Outcome {
	anyRequested := false
	allGranted := true
	anyGrantedAmount := false
	for _, d := range dims {
		if d.requested <= 0 {
			continue
		}
		anyRequested = true
		if d.outcome != leaseextension.Granted {
			allGranted = false
		}
		if d.granted > 0 {
			anyGrantedAmount = true
		}
	}
	switch {
	case !anyRequested, allGranted:
		return leaseextension.Granted
	case !anyGrantedAmount:
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

// gateResult is the outcome of the §8.6 approval gate for one extension
// request. Exactly one of proceed, resp, or err is meaningful: proceed
// true means run the grant math under approver; resp non-nil is a
// terminal REJECTED response; err non-nil is a handler error.
type gateResult struct {
	// proceed reports that the request may run the grant math.
	proceed bool
	// approver is the §8.6 line 743 approver attribution for the grant
	// audit ("gateway-auto" or "client").
	approver string
	// resp is the terminal response when the gate rejects without an
	// error (a user-denied elicitation).
	resp *adapterv1.ExtendLeaseResponse
	// err is a handler-level error (rate-limit store failure, elicitation
	// transport failure, or a misconfigured elicitation mode).
	err error
}

// gate applies the §8.6 approval decision before any budget moves. In
// auto mode it grants independently unless the tree has tripped its
// maxAutoExtensionsPerMinute, in which case it records the §8.6 line 712
// fallback and proceeds through the elicitation path. In elicitation
// mode it solicits the user's consent via the coordinator; a rejection
// persists the subtree denial and returns a terminal REJECTED response.
// F-8.6.2; F-8.6.7.
func (s *Service) gate(ctx context.Context, mode ApprovalMode, tenantID string, budget TreeBudget, sessionID string, requested Dimensions) gateResult {
	if mode == ApprovalModeAuto {
		over, maxPerMin, err := s.autoOverLimit(ctx, tenantID, budget.RootSessionID)
		if err != nil {
			return gateResult{err: fmt.Errorf("leasecontrol: auto-mode rate check for tree %s: %w", budget.RootSessionID, err)}
		}
		if !over {
			return gateResult{proceed: true, approver: "gateway-auto"}
		}
		// §8.6 line 712 — the tree exceeded maxAutoExtensionsPerMinute;
		// pause auto-approval, log the fallback, and require elicitation
		// for the remainder of the window.
		s.recordAutoRateLimited(ctx, tenantID, budget.RootSessionID, sessionID, maxPerMin)
	}

	// Elicitation mode, or an auto-mode request that fell back to it.
	if s.coordinator == nil {
		// §8.6 line 714 — elicitation requires the user's consent. With no
		// Elicitor wired the gateway cannot obtain it, so it fails closed
		// rather than silently auto-granting, which is the bug F-8.6.2
		// fixes. Production always wires an Elicitor.
		return gateResult{err: fmt.Errorf("leasecontrol: elicitation approval mode for tree %s requires an elicitor; none configured", budget.RootSessionID)}
	}
	c, err := s.coordinator.requestConsent(ctx, tenantID, budget.RootSessionID, sessionID)
	if err != nil {
		return gateResult{err: fmt.Errorf("leasecontrol: elicitation for session %s: %w", sessionID, err)}
	}
	if !c.approved {
		// §8.6 lines 727-734 — the user rejected; the coordinator persisted
		// the subtree denial. Audit the denial and return the cool-off
		// expiry so the adapter surfaces BUDGET_EXHAUSTED.
		s.audit(ctx, tenantID, budget, sessionID, requested, AuditOutcomeDenied, mode)
		expiry := s.clock().Add(s.resolveCoolOff(ctx, tenantID, budget.RootSessionID))
		return gateResult{resp: rejectedResponse(sessionID, expiry)}
	}
	return gateResult{proceed: true, approver: c.approver}
}

// autoRateLimitProvider is the optional BudgetSource extension that
// reports the tree's resolved §8.6 line 712 maxAutoExtensionsPerMinute.
// MemoryBudgetSource implements it; a source that does not yields zero,
// which disables the auto-mode rate limit for the tree. The pattern
// mirrors approvalModeProvider so the core BudgetSource interface stays
// minimal. F-8.6.7.
// spec: §8.6 line 712
type autoRateLimitProvider interface {
	AutoExtensionsPerMinute(ctx context.Context, tenantID, rootSessionID string) int
}

// autoOverLimit reports whether the tree has exceeded its §8.6 line 712
// auto-mode rate limit on this request, returning the resolved limit so
// the audit can record it. It is a no-op (never over) when no counter is
// wired or the tree sets no limit. F-8.6.7.
func (s *Service) autoOverLimit(ctx context.Context, tenantID, rootSessionID string) (over bool, maxPerMin int, err error) {
	if s.autoLimiter == nil {
		return false, 0, nil
	}
	maxPerMin = s.autoMaxPerMinute(ctx, tenantID, rootSessionID)
	if maxPerMin <= 0 {
		return false, 0, nil
	}
	over, err = s.autoLimiter.over(ctx, tenantID, rootSessionID, maxPerMin, s.clock())
	return over, maxPerMin, err
}

// autoMaxPerMinute resolves the tree's §8.6 line 712
// maxAutoExtensionsPerMinute. A tree-specific (tenant/runtime) value wins;
// otherwise the deployment default applies, following the §8.6 line 654
// "more specific overrides" resolution. Zero means no limit. F-8.6.7.
// spec: §8.6 line 712
func (s *Service) autoMaxPerMinute(ctx context.Context, tenantID, rootSessionID string) int {
	if p, ok := s.budgets.(autoRateLimitProvider); ok {
		if v := p.AutoExtensionsPerMinute(ctx, tenantID, rootSessionID); v > 0 {
			return v
		}
	}
	return s.defaultAutoMaxPerMinute
}

// recordAutoRateLimited logs the §8.6 line 712
// lease_extension_auto_rate_limit_exceeded fallback when an Auditor is
// wired. F-8.6.7.
func (s *Service) recordAutoRateLimited(ctx context.Context, tenantID, rootSessionID, sessionID string, maxPerMin int) {
	if s.auditing == nil {
		return
	}
	s.auditing.RecordAutoRateLimitExceeded(ctx, AutoRateLimitAudit{
		TenantID:          tenantID,
		RootSessionID:     rootSessionID,
		RequestSessionID:  sessionID,
		MaxPerMinute:      maxPerMin,
		ServiceInstanceID: s.serviceInstanceID,
		ClientIP:          s.peerIPFn(ctx),
	})
}

// audit records one §8.6 extension cool-off rejection. No grant is
// applied, so the audit records the requested dimensions as the
// elicitation surface the user denied, the unchanged post-grant new
// limits (the tree's current values), and the resolved approval mode
// for the tree. The cool-off rejection is always attributed to the user
// via the Approver override, since the rejection itself is the user's
// prior denial echoed back per §8.6 line 743. F-8.6.1; F-8.6.10.
// spec: §8.6 line 743
func (s *Service) audit(ctx context.Context, tenantID string, b TreeBudget, sessionID string, requested Dimensions, outcome AuditOutcome, mode ApprovalMode) {
	s.auditFull(ctx, tenantID, b, sessionID, requested, Dimensions{}, outcome, auditExtras{
		approvalMode:     mode,
		newLimits:        b.Current,
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
	approvalMode     ApprovalMode
	newLimits        NewLimits
	approverOverride string
}

func (s *Service) auditFull(ctx context.Context, tenantID string, b TreeBudget, sessionID string, requested, granted Dimensions, outcome AuditOutcome, extras auditExtras) {
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
		Requested:         requested,
		Granted:           granted,
		EffectiveMax:      b.EffectiveMax.Tokens,
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
