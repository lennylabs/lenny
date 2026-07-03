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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/delegation/cycle"
	"github.com/lennylabs/lenny/pkg/delegation/lease"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/fileexport"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// DefaultMaxDepth is the §8.2.bis Helm fallback for
// `gateway.delegation.defaultMaxDepth`. The spec mandates a positive
// integer maxDepth on every effective lease, so the service uses this
// when no preceding precedence layer supplied one.
//
// spec: §8.2.bis line 89.
const DefaultMaxDepth = 10

// DefaultInterceptorWeakeningCooldown is the §8.3 line 181 cluster-
// scoped default for `gateway.interceptorWeakeningCooldownSeconds`
// (60s). During the window following a DelegationPolicy
// `scanExportedFiles: true → false` transition every `delegate_task`
// resolving to that policy is rejected with
// `INTERCEPTOR_WEAKENING_COOLDOWN`. F-8.7.12 / F-13.5.7.
const DefaultInterceptorWeakeningCooldown = 60 * time.Second

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

	// LeaseSlice is the §8.2 `lease_slice` the caller declares on the
	// child lease: the per-subtree resource ceiling (maxTokenBudget,
	// maxChildrenTotal, maxTreeSize, maxParallelChildren, perChildMaxAge).
	// Delegate validates it against the parent's granted slice
	// (`parent.DelegationLease`): a child may tighten but never widen the
	// parent's budget, and a slice that exceeds the parent's remaining
	// budget on any axis is rejected with *lease.BudgetExceededError,
	// which the §8.5 handler maps to BUDGET_EXHAUSTED. The resolved slice
	// is stamped onto the child session row so the child's own
	// descendants validate against it in turn. The zero value (no axes
	// set) imposes no budget binding.
	//
	// spec: §8.2 lines 38-48, 127. F-8.2.2.
	LeaseSlice lease.LeaseSlice

	// UserID owns the child session. Inherits the parent's when
	// blank.
	UserID string

	// ParentToken is the §8.2 line 59 RFC 8693 `actor_token` material —
	// the parent session token the delegating pod presents. When set
	// (and a ChildTokenMinter is wired), Delegate runs the in-process
	// child-token exchange after admission: it mints the child session
	// token with narrowed scope, the parent in the `act` chain,
	// delegation_depth = parent + 1, and a capped exp, and reads the
	// parent `jti` against the §13.3 revocation cache. A revoked parent
	// rejects with ErrParentRevoked (DELEGATION_PARENT_REVOKED) and no
	// child is created. Nil skips the exchange leg.
	//
	// spec: §8.2 lines 59-61. F-8.1.2 / F-8.2.7.
	ParentToken *ParentToken

	// ApprovalMode is the §8.4 closed enum on the delegation lease.
	// The empty string and "policy"/"approval" share the auto-approval
	// path (with "approval" recorded verbatim on audit per the v1
	// alias rule); "deny" short-circuits with ErrDelegationDenied
	// before pod allocation and before the §4 PreDelegation
	// interceptor chain runs. Any other value is rejected with
	// *lease.InvalidApprovalModeError so the §8.5 lenny/delegate_task
	// handler can surface INVALID_LEASE_FIELD.
	//
	// spec: §8.4 lines 515-521. F-8.4.1, F-8.4.2, F-8.4.3.
	ApprovalMode lease.ApprovalMode

	// TreeVisibility is the §8.5 visibility boundary the parent declares
	// on the child lease. Empty inherits the parent's effective value
	// per the §8.3 inheritance rule (the child lease's declared value if
	// present, otherwise the parent's effective value). A value broader
	// than the parent's effective visibility is rejected with
	// *TreeVisibilityWeakeningError before the child row is created. The
	// resolved value is stamped onto the child session row.
	//
	// spec: §8.3 lines 311-319; §8.5 line 540. F-8.5.2 / F-8.9.2 / F-13.5.8.
	TreeVisibility session.TreeVisibility

	// FileExport carries the §8.7 `fileExport` entries declared on the
	// `lenny/delegate_task` lease: each names a source glob resolved
	// inside the parent's /workspace/current and the relative destPrefix
	// the matched files are rebased under in the child workspace. Empty
	// (the spec default) skips the §8.7 export-materialization phase
	// entirely. When non-empty, Delegate runs the export Materializer to
	// pull, validate, scan, and persist the files before the child row is
	// created, stamping the resulting §14 upload sources onto the child's
	// WorkspacePlan so the §6.3 binder delivers them at materialization.
	//
	// spec: §8.7 (file export model); §8.2 lines 91-95 (steps 3, 4, 6).
	FileExport []export.Spec

	// FileExportLimits is the §8.3 line 264 lease `fileExportLimits`
	// structural ceiling (max file count and aggregate bytes) enforced
	// across all FileExport entries. The zero value selects the §8.3
	// defaults (100 files, 100 MiB).
	//
	// spec: §8.3 line 264.
	FileExportLimits fileexport.FileExportLimits

	// EffectiveMessagingScope is the child's resolved effective §7.2
	// messagingScope (the narrowest of deployment maxScope, tenant scope,
	// and the top-most parent runtime scope). It is not a per-delegation
	// lease field; the caller resolves it from the configuration
	// hierarchy and passes it here. Empty resolves to the §7.2 default
	// `direct`. When it resolves to `siblings` and the child's effective
	// treeVisibility is not `full`, Delegate rejects with
	// *TreeVisibilityMessagingScopeError so a child cannot gain sibling
	// messaging without the visibility needed to discover those siblings.
	//
	// spec: §8.3 lines 321-324; §7.2 lines 236-266. F-13.5.8.
	EffectiveMessagingScope session.MessagingScope
}

// Result is the outcome of a successful Delegate call.
type Result struct {
	// Child is the newly created child session row.
	Child sessionstore.Session

	// Depth is the child's depth in the delegation tree (root = 0).
	Depth int

	// ChildToken is the §8.2 line 59 minted child session token (narrowed
	// scope, `act` chain, capped exp, delegation_depth = parent + 1). Nil
	// when no ChildTokenMinter is wired or the request carried no
	// ParentToken. The gateway hands it to the child pod at materialization
	// so the child runs with a parent-derived identity. F-8.1.2 / F-8.2.7.
	ChildToken *ChildToken
}

// MetricsRecorder receives §8.2 delegation admission and cycle-gate
// observations. *gatewaymetrics.Metrics implements it. A nil recorder
// makes Delegate skip metric emission.
type MetricsRecorder interface {
	ObserveDelegationDepth(pool string, depth int)
	IncDelegationWouldHaveBlocked(pool, tenantID, layer, mode string)
}

// Auditor records §11.7 / §16.7 delegation audit events emitted by
// the service: `delegation.spawned` on a successful admission,
// `delegation.self_recursion_allowed` on an admitted self-recursive
// hop under `enforce` mode, and `delegation.cycle_warning` on a
// `would_have_blocked` outcome under `warn` mode. A nil Auditor makes
// the service skip every emission.
//
// spec: §8.2 lines 70-79 (cycle decision matrix);
// §11.7 line 62 (`delegation.spawned`); §16.7 catalog.
// F-8.5.8 / F-8.5.9.
type Auditor interface {
	EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any)
}

// ExperimentRouter routes a delegation child afresh through the §10.7
// ExperimentRouter when the parent's propagation mode is `independent`
// (or the parent carries no experiment context). spec: §8.2 line 90
// (independent propagation evaluates the child for experiment
// eligibility independently). The session server's
// `*Server.ApplyExperimentRouting` implements this interface.
type ExperimentRouter interface {
	ApplyExperimentRouting(ctx context.Context, row *sessionstore.Session) error
}

// ExportMaterializer runs the §8.7 gateway-side file-export pipeline for
// one delegation: it pulls the declared `fileExport` specs from the
// running parent pod, validates and optionally content-scans them,
// persists each surviving file durably, and returns the §14 child
// WorkspacePlan upload sources. *export.Materializer implements it. A nil
// materializer on the Service makes a delegation that declares
// `fileExport` entries fail with ErrExportNotConfigured rather than
// silently dropping the export.
//
// spec: §8.7; §8.2 lines 91-95 (steps 3, 4, 6).
type ExportMaterializer interface {
	Materialize(ctx context.Context, p export.Params) (export.Result, error)
}

// ExportScanChainResolver resolves the §8.3 `contentPolicy.interceptorRef`
// named RequestInterceptor into the PreExportMaterialization chain and the
// §11.7 / §16.1 ExportScanContext used by the per-file content scan. It is
// consulted only when the effective DelegationPolicy sets
// `contentPolicy.scanExportedFiles: true`. A nil resolver makes a
// scan-required export fail closed with ErrExportScanUnavailable: the
// spec forbids honoring `scanExportedFiles` without a resolvable
// interceptor (§8.3 rule 1).
//
// spec: §8.3 lines 160-181 (contentPolicy + fail-closed scan).
type ExportScanChainResolver interface {
	ResolveExportScanChain(ctx context.Context, tenantID, interceptorRef string) (*interceptor.Chain, interceptor.ExportScanContext, error)
}

// TreeBudgetReserver atomically reserves a delegation's structural
// budget axes (tree node count, tree memory footprint, per-parent
// concurrent children, per-parent total descendants, tree token pool)
// against the resolved lease caps, backed by the §12.4 Redis counters.
// *treebudget.Reserver implements it. A nil reserver on the Service
// skips the Redis-backed admission gate, leaving only the static
// per-call ValidateChildSlice ceiling enforced (used by the in-process
// minimal gateway and by tests that do not stand up Redis).
//
// spec: §8.2 lines 57, 127; §12.4 lines 193, 213.
type TreeBudgetReserver interface {
	Reserve(ctx context.Context, r treebudget.Reservation) (treebudget.Totals, error)
	Return(ctx context.Context, r treebudget.Reservation) error
}

// Service creates child sessions from a delegation request.
type Service struct {
	store            sessionstore.Store
	experiments      experimentstore.Store
	runtimes         runtimestore.Store
	policies         delegationpolicystore.Store
	metrics          MetricsRecorder
	auditor          Auditor
	experimentRouter ExperimentRouter
	clock            func() time.Time
	idFn             func() string
	mode             cycle.Mode
	// platformAllowSelfRec is the §8.2 Layer-1 platform-level opt-in
	// for self-recursive delegation hops (Helm value
	// `gateway.allowSelfRecursion`, default false). When false the
	// platform layer of the three-layer AND gate rejects every
	// self-recursive hop regardless of runtime / policy opt-ins.
	platformAllowSelfRec bool
	// defaultMaxDepth is the §8.2.bis Helm fallback maxDepth applied
	// when no explicit caller / preset / runtime-default / policy
	// ceiling supplied one. Always positive (DefaultMaxDepth when the
	// caller passed 0).
	defaultMaxDepth int
	// interceptorWeakeningCooldown is the §8.3 line 181 cluster-scoped
	// window after a DelegationPolicy `scanExportedFiles: true → false`
	// transition during which `delegate_task` rejects with
	// INTERCEPTOR_WEAKENING_COOLDOWN. Zero disables enforcement (used
	// by tests that exercise the rest of the admission path without
	// the cooldown gate).
	interceptorWeakeningCooldown time.Duration
	// interceptorCooldown resolves the §8.3 SEC-013 fail-policy weakening
	// cooldown for an interceptor named by a policy's interceptorRef. Nil
	// disables the interceptor-failPolicy cooldown gate. F-4.8.17.
	interceptorCooldown InterceptorCooldownResolver
	// exportMat runs the §8.7 file-export materialization when a
	// delegation declares `fileExport` entries. Nil makes such a
	// delegation fail with ErrExportNotConfigured. F-8.7.1.
	exportMat ExportMaterializer
	// exportScanResolver resolves the §8.3 contentPolicy.interceptorRef
	// chain for the optional per-file export content scan. Nil makes a
	// `scanExportedFiles: true` policy fail closed. F-8.7.1.
	exportScanResolver ExportScanChainResolver
	// treeBudget atomically reserves the §8.2 structural budget axes
	// against the §12.4 Redis counters on every admission. Nil skips
	// the Redis-backed gate (in-process minimal path). F-8.2.18 /
	// F-8.2.12 / F-8.1.1.
	treeBudget TreeBudgetReserver
	// tokenMinter performs the §8.2 line 59 internal RFC 8693
	// child-token exchange after admission passes: it narrows scope,
	// builds the `act` chain, fixes delegation_depth at parent + 1,
	// caps exp, and runs the §13.3 actor-token freshness check. Nil
	// skips the exchange leg. F-8.1.2 / F-8.2.7.
	tokenMinter ChildTokenMinter
	// maxActiveChildrenPerUser is the §11.1 line 9 per-user
	// active-delegated-children admission cap: the maximum count of live
	// (non-terminal) delegated children a single user may hold across all
	// their sessions and trees. The per-session breadth is bounded by the
	// §8.2 lease/treebudget axes; this scope bounds the aggregate a user
	// can spread across many sessions. Zero or less leaves the scope
	// unlimited. Operator-tunable via the gateway Helm value
	// `gateway.delegation.maxActiveChildrenPerUser`. F-11.1.4.
	maxActiveChildrenPerUser int
	// leaseRegistrar, when set, registers every admitted child session
	// with the §8.6 lease-extension budget source so a later in-process
	// budget-exhaustion extension (the gateway LLM Proxy's ExtendForBudget
	// trigger) from the child resolves its tree instead of failing
	// ErrSessionNotFound. The child is added to its root's tree and its
	// per-extension ceiling is capped at the parent's own granted lease
	// (§8.6 line 648). Nil leaves extension state unregistered (the
	// in-process minimal gateway with no GatewayControl listener).
	// F-15.3.5.
	leaseRegistrar LeaseChildRegistrar
}

// LeaseChildRegistrar registers a delegated child session with the §8.6
// lease-extension budget source. *leasecontrol.MemoryBudgetSource
// satisfies it. The delegation Service calls AddSession to bind the
// child to its root's extension tree and SetParentLease to cap the
// child's per-extension grant at the parent's own lease (§8.6 line
// 648). F-15.3.5.
// spec: §8.6 line 648
type LeaseChildRegistrar interface {
	AddSession(sessionID, rootSessionID, tenantID string)
	SetParentLease(sessionID string, parent leasecontrol.SessionLease)
}

// parentLeaseCeiling projects a parent session's granted §8.2
// DelegationLease onto the §8.6 line 648 SessionLease ceiling its
// children inherit: a child can never extend a dimension beyond what
// the parent's lease itself granted. A root parent carries no granted
// lease (nil), which yields the zero ceiling (no per-parent cap; the
// tree's effective-max ceiling still applies). F-15.3.5.
// spec: §8.6 line 648
func parentLeaseCeiling(parent sessionstore.Session) leasecontrol.SessionLease {
	l := parent.DelegationLease
	if l == nil {
		return leasecontrol.SessionLease{}
	}
	return leasecontrol.SessionLease{
		TokenCeiling:            l.MaxTokenBudget,
		MaxAgeCeiling:           int64(l.PerChildMaxAge),
		ChildrenCeiling:         int64(l.MaxChildrenTotal),
		ParallelChildrenCeiling: int64(l.MaxParallelChildren),
		TreeSizeCeiling:         int64(l.MaxTreeSize),
	}
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

	// Experiments, when set, lets Delegate propagate the parent's §8.3
	// experimentContext onto the child per the experiment's propagation
	// mode. Nil disables propagation — a child is created with no
	// experiment context.
	Experiments experimentstore.Store

	// Runtimes, when set, supplies the §5.1 runtime registry so the §8.2
	// cycle-detection gate reads the resolved target runtime's
	// allowSelfRecursion (the LayerRuntime input). Nil leaves the runtime
	// layer at its conservative false default, rejecting self-recursive
	// hops regardless of the target runtime's declared value.
	Runtimes runtimestore.Store

	// Policies, when set, supplies the §8.3 DelegationPolicy registry
	// so the §8.2 cycle-detection gate reads the effective policy's
	// allowSelfRecursion (the LayerPolicy input) and the §8.2.bis
	// maxDepth precedence chain can consult the policy ceiling. Nil
	// leaves the policy layer at its conservative false default,
	// rejecting self-recursive hops regardless of the policy's
	// declared value.
	//
	// spec: §8.2 line 75 (LayerPolicy); §8.2.bis line 86 (policy ceiling).
	Policies delegationpolicystore.Store

	// PlatformAllowSelfRecursion drives the §8.2 LayerPlatform input
	// to the three-layer AND gate (Helm value
	// `gateway.allowSelfRecursion`). Defaults to false — the platform
	// layer rejects every self-recursive hop unless the operator
	// explicitly opts in.
	//
	// spec: §8.2 line 73 (LayerPlatform).
	PlatformAllowSelfRecursion bool

	// DefaultMaxDepth is the §8.2.bis Helm fallback for
	// `gateway.delegation.defaultMaxDepth`. Defaults to DefaultMaxDepth
	// (10) when zero. The §8.2.bis precedence chain consults it last so
	// every admitted delegation lease carries a positive integer
	// maxDepth, even when the caller and the policy ceiling are both
	// unset.
	//
	// spec: §8.2.bis line 89.
	DefaultMaxDepth int

	// Metrics, when set, receives §8.2 delegation admission and
	// cycle-gate observations. Nil disables emission.
	Metrics MetricsRecorder

	// Auditor, when set, receives §11.7 / §16.7 delegation audit
	// events: `delegation.spawned`, `delegation.self_recursion_allowed`,
	// `delegation.cycle_warning`. Nil disables emission.
	// spec: F-8.5.8 / F-8.5.9.
	Auditor Auditor

	// ExperimentRouter, when set, routes a delegation child afresh
	// under §8.2 line 90 `independent` propagation (or when the
	// parent carries no experiment enrollment). Nil leaves the child
	// at the propagated context only, which silently unenrolls the
	// child under `independent`.
	ExperimentRouter ExperimentRouter

	// InterceptorWeakeningCooldown is the §8.3 line 181 cluster-scoped
	// `gateway.interceptorWeakeningCooldownSeconds`. During the window
	// following a DelegationPolicy `scanExportedFiles: true → false`
	// transition every `delegate_task` resolving to the affected
	// policy is rejected with INTERCEPTOR_WEAKENING_COOLDOWN
	// (TRANSIENT, HTTP 503). Zero selects DefaultInterceptorWeakeningCooldown
	// (60s). A negative value disables the gate — tests that exercise
	// the rest of the admission path can opt out. F-8.7.12 / F-13.5.7.
	InterceptorWeakeningCooldown time.Duration

	// ExportMaterializer, when set, runs the §8.7 file-export pipeline
	// for a delegation that declares `fileExport` entries. Nil makes
	// such a delegation fail with ErrExportNotConfigured; a delegation
	// with no `fileExport` entries is unaffected. F-8.7.1.
	ExportMaterializer ExportMaterializer

	// ExportScanChainResolver, when set, resolves the §8.3
	// contentPolicy.interceptorRef chain for the optional per-file
	// export content scan. Nil makes a `scanExportedFiles: true` policy
	// fail closed with ErrExportScanUnavailable. F-8.7.1.
	ExportScanChainResolver ExportScanChainResolver

	// TreeBudgetReserver, when set, gates every admission on the §12.4
	// Redis-backed delegation tree budget counters (maxTreeSize,
	// maxTreeMemoryBytes, maxParallelChildren, maxChildrenTotal, tree
	// token pool). Nil skips the Redis gate, leaving only the static
	// ValidateChildSlice ceiling enforced. F-8.2.18 / F-8.2.12 /
	// F-8.1.1.
	TreeBudgetReserver TreeBudgetReserver

	// ChildTokenMinter, when set, performs the §8.2 line 59 internal
	// RFC 8693 child-token exchange on every admitted delegation: it
	// mints the child session token with narrowed scope, a complete
	// `act` chain, delegation_depth = parent + 1, and a capped exp, and
	// runs the §13.3 actor-token freshness check. Nil skips the
	// exchange leg, leaving the child without a parent-derived token
	// (the in-process minimal gateway). F-8.1.2 / F-8.2.7.
	ChildTokenMinter ChildTokenMinter

	// MaxActiveChildrenPerUser is the §11.1 line 9 per-user
	// active-delegated-children admission cap. Before reserving tree
	// budget, Delegate counts the owning user's live (non-terminal)
	// delegated children across all their sessions; a count at or above
	// this cap rejects with ErrUserChildrenExhausted. Zero or less leaves
	// the scope unlimited. Operator-tunable via the gateway Helm value
	// `gateway.delegation.maxActiveChildrenPerUser`. F-11.1.4.
	MaxActiveChildrenPerUser int

	// LeaseRegistrar, when set, registers every admitted child session
	// with the §8.6 lease-extension budget source (AddSession +
	// SetParentLease) so an in-process budget-exhaustion extension (the
	// gateway LLM Proxy's ExtendForBudget trigger) from the child resolves
	// its tree instead of failing ErrSessionNotFound. Nil leaves
	// extension state unregistered. F-15.3.5.
	LeaseRegistrar LeaseChildRegistrar

	// InterceptorCooldown, when set, resolves the §8.3 SEC-013 fail-policy
	// weakening cooldown for the interceptor named by a policy's
	// `contentPolicy.interceptorRef`. Delegate and the
	// InterceptorFailPolicyCooldown helper consult it to reject a
	// `delegate_task` / `lenny/send_message` whose effective interceptor
	// is inside the cluster-scoped weakening window. Nil disables the
	// interceptor-failPolicy cooldown gate (the in-process minimal gateway
	// and the unit suite, which register no external interceptors).
	// F-4.8.17.
	InterceptorCooldown InterceptorCooldownResolver
}

// InterceptorCooldownResolver reports the §8.3 SEC-013 active fail-open
// weakening cooldown for a named external interceptor. It is satisfied
// by interceptorstore.CooldownResolver in production. The resolver is
// the single source of truth read per invocation (§8.3 rule 1): the
// service never snapshots the interceptor configuration into a lease.
type InterceptorCooldownResolver interface {
	// FailOpenCooldown returns the server-minted transition timestamp and
	// the cooldown seconds that were in force at that transition for the
	// named interceptor. ok is false when the interceptor is unknown or
	// not currently weakened.
	FailOpenCooldown(ctx context.Context, name string) (transitionTs time.Time, cooldownSeconds int, ok bool)
}

// NewService returns a delegation Service.
func NewService(store sessionstore.Store, opts Options) *Service {
	clock := opts.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	// idFn is left nil unless a test overrides the child-id generator.
	// With no override the production path mints the child id from the
	// root session's routing prefix (session.NewChildID); see newChildID.
	idFn := opts.IDFunc
	mode := opts.CycleMode
	if mode == "" {
		mode = cycle.ModeEnforce
	}
	maxDepth := opts.DefaultMaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	cooldown := opts.InterceptorWeakeningCooldown
	if cooldown == 0 {
		cooldown = DefaultInterceptorWeakeningCooldown
	}
	if cooldown < 0 {
		cooldown = 0
	}
	return &Service{
		store:                        store,
		experiments:                  opts.Experiments,
		runtimes:                     opts.Runtimes,
		policies:                     opts.Policies,
		metrics:                      opts.Metrics,
		auditor:                      opts.Auditor,
		experimentRouter:             opts.ExperimentRouter,
		clock:                        clock,
		idFn:                         idFn,
		mode:                         mode,
		platformAllowSelfRec:         opts.PlatformAllowSelfRecursion,
		defaultMaxDepth:              maxDepth,
		interceptorWeakeningCooldown: cooldown,
		interceptorCooldown:          opts.InterceptorCooldown,
		exportMat:                    opts.ExportMaterializer,
		exportScanResolver:           opts.ExportScanChainResolver,
		treeBudget:                   opts.TreeBudgetReserver,
		tokenMinter:                  opts.ChildTokenMinter,
		maxActiveChildrenPerUser:     opts.MaxActiveChildrenPerUser,
		leaseRegistrar:               opts.LeaseRegistrar,
	}
}

// Errors surfaced by Delegate. Each maps to a §15.1 error code.
var (
	// ErrParentNotFound — the parent session does not exist.
	ErrParentNotFound = errors.New("delegation: parent session not found")

	// ErrParentNotRunning — the parent is not in a state that may
	// delegate (§8.2 requires the parent be running).
	ErrParentNotRunning = errors.New("delegation: parent session is not running")

	// ErrParentNoUser — the parent session carries no authenticated
	// user identity, so the §8.2 child-token exchange (line 58) has no
	// `subject_token` to bind the child to. The §11.2 quota gates,
	// §10.6 environment resolver, §11.7 audit attribution, and §9.2
	// elicitation routing all key on a non-empty user_id; spawning a
	// userless child would silently downgrade every one of those
	// invariants for the entire subtree.
	ErrParentNoUser = errors.New("delegation: parent session has no authenticated user identity")

	// ErrUserChildrenExhausted — the owning user already holds the §11.1
	// line 9 maximum count of live (non-terminal) delegated children
	// across all their sessions. The admission is rejected before any
	// tree budget is reserved. The §8.5 handler maps it to QUOTA_EXCEEDED.
	// spec: §11.1 line 9 (Active delegated children — per-user). F-11.1.4.
	ErrUserChildrenExhausted = errors.New("delegation: per-user active-delegated-children limit reached (§11.1)")

	// ErrTargetNotAgent — the delegation target resolves to a
	// `type: mcp` runtime. spec: §8.2 line 50 mandates `lenny/
	// delegate_task` reject these with `target_not_an_agent` before any
	// child session is admitted.
	ErrTargetNotAgent = errors.New("delegation: target_not_an_agent: target runtime is type:mcp (§8.2)")

	// ErrDelegationDenied — the §8.4 lease declares `approvalMode:
	// "deny"`, which short-circuits the delegation path before pod
	// allocation and before the §4 PreDelegation interceptor chain.
	// The gateway surfaces this as `DELEGATION_DENIED` per §15.1.
	//
	// spec: §8.4 line 521. F-8.4.1.
	ErrDelegationDenied = errors.New("delegation: lease approvalMode=deny rejects this hop (§8.4)")

	// ErrExportNotConfigured — the delegation declares §8.7 `fileExport`
	// entries but the gateway wired no ExportMaterializer, so the export
	// path cannot run. Failing closed prevents silently dropping the
	// parent's declared files (which a caller would read as a successful
	// export of an empty set). spec: §8.7. F-8.7.1.
	ErrExportNotConfigured = errors.New("delegation: fileExport declared but no export materializer is configured (§8.7)")

	// ErrExportScanUnavailable — the effective DelegationPolicy sets
	// `contentPolicy.scanExportedFiles: true` but no ExportScanChainResolver
	// is wired, so the mandated per-file scan cannot run. The §8.3 rule-1
	// fail-closed posture rejects the export rather than materializing
	// unscanned files. spec: §8.3 lines 160-181. F-8.7.1.
	ErrExportScanUnavailable = errors.New("delegation: contentPolicy.scanExportedFiles is true but no export-scan interceptor is configured (§8.3)")

	// ErrBudgetUnavailable — the Redis-backed delegation tree budget
	// counters could not be consulted (outage or script error). Per
	// §12.4 line 213 the admission path fails closed: the §8.5 handler
	// surfaces this as the retryable DELEGATION_BUDGET_UNAVAILABLE
	// rather than admitting an unbudgeted delegation. spec: §12.4 line
	// 213. F-8.2.18.
	ErrBudgetUnavailable = errors.New("delegation: tree budget counters unavailable (§12.4 fail-closed)")
)

// InterceptorWeakeningCooldownError reports a §8.3 line 181 rejection:
// the effective DelegationPolicy is inside the cluster-scoped
// `gateway.interceptorWeakeningCooldownSeconds` window that opened
// when an admin flipped `contentPolicy.scanExportedFiles` from true
// to false. The gateway returns INTERCEPTOR_WEAKENING_COOLDOWN
// (TRANSIENT, HTTP 503) so callers retry after the window closes.
// F-8.7.12 / F-13.5.7.
type InterceptorWeakeningCooldownError struct {
	// PolicyName is the §8.3 affected DelegationPolicy.
	PolicyName string

	// InterceptorRef is the §4.8 interceptor whose `fail-closed →
	// fail-open` transition opened the window. Empty for the
	// scanExportedFiles trigger (F-8.7.12); set for the interceptor
	// failPolicy trigger (F-4.8.17).
	InterceptorRef string

	// TransitionTs is the server-minted weakening timestamp: the policy
	// row's scanExportedFiles flip (F-8.7.12) or the interceptor row's
	// failPolicy flip (F-4.8.17).
	TransitionTs time.Time

	// CooldownSeconds is the cluster-scoped cooldown duration that
	// was in force at TransitionTs.
	CooldownSeconds int

	// RetryAfterSeconds is the integer ceiling of the remaining
	// cooldown window from "now". Callers map it onto the §15.1
	// `details.retryAfterSeconds` field and the HTTP Retry-After
	// header.
	RetryAfterSeconds int
}

func (e *InterceptorWeakeningCooldownError) Error() string {
	if e.InterceptorRef != "" {
		return fmt.Sprintf(
			"delegation: interceptor %q is inside the §8.3 fail-policy weakening cooldown; retry after %ds",
			e.InterceptorRef, e.RetryAfterSeconds,
		)
	}
	return fmt.Sprintf(
		"delegation: policy %q is inside the §8.3 scanExportedFiles weakening cooldown; retry after %ds",
		e.PolicyName, e.RetryAfterSeconds,
	)
}

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

// TreeVisibilityWeakeningError reports a §8.3 lines 313-317 monotonicity
// failure: the child lease declares a `treeVisibility` broader than the
// parent's effective value. The §8.5 handler maps it to
// TREE_VISIBILITY_WEAKENING (POLICY, HTTP 422) with
// `details.parentTreeVisibility` and `details.childTreeVisibility`.
// F-8.5.2 / F-8.9.2 / F-13.5.8.
type TreeVisibilityWeakeningError struct {
	ParentVisibility session.TreeVisibility
	ChildVisibility  session.TreeVisibility
}

func (e *TreeVisibilityWeakeningError) Error() string {
	return fmt.Sprintf(
		"delegation: child treeVisibility %q widens the parent's effective %q (§8.3 lines 313-317; ordering full → parent-and-self → self-only is strict)",
		e.ChildVisibility, e.ParentVisibility,
	)
}

// TreeVisibilityMessagingScopeError reports a §8.3 lines 321-324
// compatibility failure: the child's resolved effective messagingScope
// is `siblings` but its effective treeVisibility is not `full`, so a
// child could gain sibling messaging without the visibility required to
// discover those siblings via lenny/get_task_tree. The §8.5 handler maps
// it to TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE (POLICY, HTTP
// 422) with `details.effectiveMessagingScope`,
// `details.effectiveTreeVisibility`, and `details.requiredTreeVisibility`
// (`"full"`). F-13.5.8.
type TreeVisibilityMessagingScopeError struct {
	EffectiveMessagingScope session.MessagingScope
	EffectiveTreeVisibility session.TreeVisibility
}

func (e *TreeVisibilityMessagingScopeError) Error() string {
	return fmt.Sprintf(
		"delegation: effective messagingScope %q requires treeVisibility full, but the child lease resolves to %q (§8.3 lines 321-324)",
		e.EffectiveMessagingScope, e.EffectiveTreeVisibility,
	)
}

// ContentPolicyWeakeningError reports a §8.3 lines 157-187 contentPolicy
// monotonicity failure: the child's resolved `contentPolicy` weakens the
// parent's effective policy on one of the inheritance axes — a larger
// `maxInputSize`, a larger `maxExportedFileSize`, a `scanExportedFiles`
// `true → false` transition, or a non-null `interceptorRef` set back to
// null. The §8.5 delegate_task handler maps it to CONTENT_POLICY_WEAKENING
// (POLICY, HTTP 422) with `details.axis`, `details.parentValue`, and
// `details.childValue`. spec: §8.3 lines 157, 177, 179, 187. F-13.5.10.
type ContentPolicyWeakeningError struct {
	Axis        string
	ParentValue string
	ChildValue  string
}

func (e *ContentPolicyWeakeningError) Error() string {
	return fmt.Sprintf(
		"delegation: child contentPolicy.%s %q weakens the parent's effective %q (§8.3 lines 157-187; a child lease may only make contentPolicy stricter)",
		e.Axis, e.ChildValue, e.ParentValue,
	)
}

// ContentPolicyInterceptorSubstitutionError reports the §8.3 line 188
// identity-based rejection: the child names a different non-null
// `interceptorRef` than the parent's effective reference. The gateway
// cannot verify an unrelated interceptor is equally restrictive, so the
// substitution is rejected unconditionally. The §8.5 handler maps it to
// CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION (POLICY, HTTP 422) with
// `details.parentInterceptorRef` and `details.childInterceptorRef`.
// spec: §8.3 line 188. F-13.5.10.
type ContentPolicyInterceptorSubstitutionError struct {
	ParentRef string
	ChildRef  string
}

func (e *ContentPolicyInterceptorSubstitutionError) Error() string {
	return fmt.Sprintf(
		"delegation: child contentPolicy.interceptorRef %q substitutes the parent's %q (§8.3 line 188; a child cannot swap the parent's named interceptor for an unrelated one)",
		e.ChildRef, e.ParentRef,
	)
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
func (s *Service) Delegate(ctx context.Context, tenantID string, req Request) (result Result, retErr error) {
	// spec: §16.3 line 343 — the gateway spawn-child path runs under a
	// `delegation.spawn_child` span so a distributed trace shows the
	// admission gate (isolation, cycle, depth, budget, token mint, child
	// INSERT) as one unit. The tracer resolves the process-global provider
	// tracing.InitProvider installs; tenant_id/session_id ride from the
	// correlation context Start projects. The deferred RecordError stamps
	// any error path (every gate rejection returns through retErr).
	ctx, span := tracing.NewTracer(nil).Start(ctx, tracing.SpanDelegationSpawnChild)
	defer func() {
		tracing.RecordError(span, retErr)
		span.End()
	}()
	// §8.2 / §12.4: budgetReservation holds the structural budget this
	// admission reserved from the §12.4 Redis counters once the gate
	// passes. If any later step fails before the child row is committed
	// the deferred release returns the reserved slice so a transient
	// downstream error does not permanently consume tree budget. The
	// pointer is set by insertChildSession (the §8.2 budget gate lives in
	// the atomic-insert stage); the deferred release stays here on the
	// named-return path so it fires on any error after the reserve.
	// F-8.2.18 / F-8.2.12.
	var budgetReservation *treebudget.Reservation
	defer func() {
		if retErr != nil && budgetReservation != nil && s.treeBudget != nil {
			_ = s.treeBudget.Return(ctx, *budgetReservation)
		}
	}()

	// Stage 1 (validation, §8.2/§8.3): approval mode, parent lookup and
	// running-state, isolation/visibility monotonicity, lineage, and the
	// parent's effective contentPolicy. Produces the validated admission
	// context the cycle and insert stages read.
	adm, err := s.validateDelegation(ctx, tenantID, req)
	if err != nil {
		return Result{}, err
	}

	// Stage 2 (§8.2 cycle detection): resolve the runtime/policy
	// self-recursion layers and the child's effective contentPolicy, run
	// the three-layer AND gate, and record the decision. A rejected
	// decision returns the typed cycle error.
	if err := s.detectCycle(ctx, tenantID, req, &adm); err != nil {
		return Result{}, err
	}

	// Stage 3 (§8.2.bis depth check): resolve the maxDepth precedence
	// chain and enforce the depth ceiling against the parent's lineage
	// depth.
	if err := s.checkDelegationDepth(req, adm); err != nil {
		return Result{}, err
	}

	// Stage 4 (atomic child-session insert, §8.2): the lease-slice,
	// per-user, and tree-budget gates, the export materialization and
	// child-token mint, the child-row INSERT, and the §11.7 audit. The
	// budget reservation it takes is threaded back here so the deferred
	// release above can return it on a later error.
	return s.insertChildSession(ctx, tenantID, req, adm, &budgetReservation)
}

// admission carries the validated and resolved state across the
// Delegate stages (validate → cycle → depth → insert). It holds only
// values one stage produces and a later stage reads, so each stage
// helper takes a small, explicit input rather than a long parameter
// list. The fields mirror the locals the monolithic Delegate threaded.
type admission struct {
	// parent is the running parent session resolved by validateDelegation.
	parent sessionstore.Session
	// childProfile is the §8.3 SEC-001 resolved child isolation profile.
	childProfile isolation.Profile
	// childVis is the §8.3 monotonically-resolved child tree visibility.
	childVis session.TreeVisibility
	// lineage and depth are the §8.2 parent-chain Identity tuples and the
	// parent's depth, built by buildLineage for cycle and depth checks.
	lineage cycle.Lineage
	depth   int
	// parentContentEff is the parent's effective (transitively-narrowest)
	// §8.3 contentPolicy, resolved in stage 1 and consumed in stage 2.
	parentContentEff effContentPolicy

	// The following are resolved by detectCycle (stage 2) and consumed by
	// the depth check and the child insert.

	// decision is the §8.2 three-layer cycle-gate decision.
	decision cycle.Decision
	// effectivePolicy / haveEffectivePolicy is the target runtime's
	// resolved DelegationPolicy, when one applies.
	effectivePolicy     delegationpolicystore.DelegationPolicy
	haveEffectivePolicy bool
	// delegationPolicyRef is the §8.10 lines 1044-1049 lease-scoped policy
	// reference stamped on the child lease for tree recovery. F-8.10.5.
	delegationPolicyRef string
	// policyCeiling is the §8.2.bis layer-4 policy depth ceiling (zero in
	// v1; reserved for the §8.3 ceiling extension).
	policyCeiling int
	// childContentEff is the child's resolved effective §8.3 contentPolicy
	// stamped on the child lease.
	childContentEff effContentPolicy
}

// validateDelegation runs the §8 stage-1 validation gates that reject a
// delegation before any cycle or budget evaluation: the §8.4 approval
// enum, the §8.2 parent lookup and running-state check, the §8.2 line 58
// parent-user requirement, the §8.3 SEC-001 isolation monotonicity, the
// §8.3 treeVisibility inheritance and messagingScope gate, the §8.2
// lineage build, and the parent's effective §8.3 contentPolicy. It
// returns the validated admission context the later stages read.
//
// spec: §8.4 lines 515-521; §8.2 lines 38-58; §8.3 lines 311-324.
// F-8.4.1, F-8.4.2.
func (s *Service) validateDelegation(ctx context.Context, tenantID string, req Request) (admission, error) {
	// §8.4: validate the closed enum at the service boundary so a
	// malformed value is rejected with *lease.InvalidApprovalModeError
	// before any side effects (parent lookup, audit emission, store
	// writes). The §8.5 lenny/delegate_task handler maps the typed
	// error to INVALID_LEASE_FIELD. F-8.4.2.
	if err := lease.ValidateApprovalMode(req.ApprovalMode); err != nil {
		return admission{}, err
	}
	// §8.4 line 521: an `approvalMode: "deny"` lease short-circuits
	// the delegation path before pod allocation and before the §4
	// PreDelegation interceptor. The gateway maps ErrDelegationDenied
	// to DELEGATION_DENIED at the §8.5 handler. F-8.4.1.
	if req.ApprovalMode == lease.ApprovalModeDeny {
		return admission{}, ErrDelegationDenied
	}
	parent, err := s.store.Get(ctx, tenantID, req.ParentSessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return admission{}, ErrParentNotFound
		}
		return admission{}, err
	}
	if parent.State != session.StateRunning {
		return admission{}, ErrParentNotRunning
	}

	// §8.2 line 58: the gateway mints the child session's token via
	// the canonical token-exchange endpoint with `subject_token` set
	// to the parent's authenticated user JWT. A parent without a
	// user identity has no subject to bind the child to, so the
	// subtree is rejected at admission rather than spawning a child
	// whose §11.2 quotas, §10.6 environment scope, §11.7 audit
	// attribution, and §9.2 elicitation routing are undifferentiated.
	// req.UserID is rejected as a substitute: the spec ties the
	// child's identity to the *authenticated* parent, not to a
	// caller-supplied label.
	if parent.UserID == "" {
		return admission{}, ErrParentNoUser
	}

	// §8.3 SEC-001 isolation monotonicity. The child profile
	// defaults to the parent's when unset.
	childProfile := req.IsolationProfile
	if childProfile == "" {
		childProfile = parent.IsolationProfile
	}
	if !isolation.IsValid(childProfile) {
		return admission{}, fmt.Errorf("delegation: child isolationProfile %q is not a recognised §5.3 profile", childProfile)
	}
	if parent.IsolationProfile != "" && !isolation.AtLeastAsRestrictive(childProfile, parent.IsolationProfile) {
		return admission{}, &IsolationViolationError{
			ParentProfile: parent.IsolationProfile,
			ChildProfile:  childProfile,
		}
	}

	// §8.3 lines 311-319: treeVisibility inheritance + monotonicity. The
	// child inherits the parent's effective visibility when it declares
	// none; a declared value may narrow but never widen it. The check
	// runs before pod allocation (the lineage / cycle / create steps
	// below), mirroring the isolation-monotonicity rejection above.
	childVis, err := resolveChildTreeVisibility(parent.TreeVisibility, req.TreeVisibility)
	if err != nil {
		return admission{}, err
	}
	// §8.3 lines 321-324: a resolved effective messagingScope of
	// `siblings` requires treeVisibility `full` so children can discover
	// one another via lenny/get_task_tree. messagingScope is resolved by
	// the caller from the §7.2 deployment/tenant/runtime hierarchy and
	// defaults to `direct`, so this gate is inert until a `siblings`
	// scope is configured for the child.
	if req.EffectiveMessagingScope.OrDefault() == session.MessagingScopeSiblings && childVis != session.VisibilityFull {
		return admission{}, &TreeVisibilityMessagingScopeError{
			EffectiveMessagingScope: session.MessagingScopeSiblings,
			EffectiveTreeVisibility: childVis,
		}
	}

	// Build the lineage from the parent chain so cycle detection can
	// run over (runtime, pool) Identity tuples.
	lineage, depth, err := s.buildLineage(ctx, tenantID, parent)
	if err != nil {
		return admission{}, err
	}

	// §8.3 lines 157, 240: resolve the parent's effective
	// (transitively-narrowest) contentPolicy here so stage 2 can apply
	// the four-axis monotonicity check against the child's declared
	// policy. F-13.5.10.
	parentContentEff := s.effectiveParentContentPolicy(ctx, tenantID, parent)

	return admission{
		parent:           parent,
		childProfile:     childProfile,
		childVis:         childVis,
		lineage:          lineage,
		depth:            depth,
		parentContentEff: parentContentEff,
	}, nil
}

// detectCycle runs the §8.2 stage-2 cycle gate. It resolves the target
// runtime (the §8.2 line 50 type gate, the §8.3 self-recursion runtime
// and policy layers, and the interceptor weakening cooldowns), resolves
// the child's effective §8.3 contentPolicy, runs the three-layer AND
// gate, records the §8.2/§16.7 decision metrics and audit, and rejects a
// cycle with the typed cycle error. It writes the resolved policy state
// (effectivePolicy, delegationPolicyRef, policyCeiling, childContentEff,
// decision) back onto adm for the depth and insert stages.
//
// spec: §8.2 lines 50, 66-79; §8.3 lines 100, 157-188, 181, 218.
// F-8.7.12, F-13.5.7, F-4.8.17, F-13.5.10.
func (s *Service) detectCycle(ctx context.Context, tenantID string, req Request, adm *admission) error {
	target := cycle.Identity{RuntimeName: req.RuntimeRef, PoolName: req.PoolRef}
	// §8.2 three-layer AND gate (spec lines 66-79). Each layer defaults
	// to the conservative false (rejects self-recursive hops). A layer
	// flips true only when the operator explicitly opted in:
	//   - Layer 1 (Platform): Helm value `gateway.allowSelfRecursion`,
	//     carried on the Service as platformAllowSelfRec.
	//   - Layer 2 (Runtime): the resolved target Runtime's
	//     allowSelfRecursion field (§5.1 line 69). A missing registry
	//     or unresolvable runtime leaves the layer false.
	//   - Layer 3 (Policy): the resolved DelegationPolicy's
	//     allowSelfRecursion field (§8.3 line 100). The policy is
	//     named on the runtime via DelegationPolicyRef; a missing
	//     reference, missing policy, or absent policy registry leaves
	//     the layer false.
	//
	// The resolved runtime is also the §8.2 line 50 type gate: a
	// `type: mcp` runtime is rejected here as defence-in-depth so a
	// caller that bypasses the MCP shim (REST / non-MCP entry points)
	// still cannot delegate onto a non-agent target.
	settings := cycle.Settings{
		Mode:                 s.mode,
		PlatformAllowSelfRec: s.platformAllowSelfRec,
	}
	if s.runtimes != nil {
		if rt, err := runtimestore.Resolve(ctx, s.runtimes, req.RuntimeRef); err == nil {
			if rt.Type == runtimestore.TypeMCP {
				return ErrTargetNotAgent
			}
			settings.RuntimeAllowSelfRec = rt.AllowSelfRecursion
			// spec: §8.10 lines 1044-1049 — the lease-scoped policy
			// reference captured at approval time so tree recovery
			// resumes the node against the persisted lease rather than
			// re-evaluating live policy. F-8.10.5.
			adm.delegationPolicyRef = rt.DelegationPolicyRef
			if s.policies != nil && rt.DelegationPolicyRef != "" {
				if pol, err := s.policies.Get(ctx, tenantID, rt.DelegationPolicyRef); err == nil && pol.IsActive() {
					adm.effectivePolicy = pol
					adm.haveEffectivePolicy = true
					// spec: §8.3 line 181 (F-8.7.12 / F-13.5.7) —
					// reject every `delegate_task` whose effective
					// DelegationPolicy is inside the cluster-scoped
					// scanExportedFiles weakening cooldown window so
					// a stolen admin credential cannot use a brief
					// fail-open gap to push delegations past a
					// now-disabled scanner. The check is structural:
					// it fires regardless of whether this particular
					// call carries any exported files.
					if cdErr := s.checkInterceptorWeakeningCooldown(pol); cdErr != nil {
						return cdErr
					}
					// spec: §4.8 line 1034 / §8.3 line 218 (SEC-013,
					// F-4.8.17) — reject every `delegate_task` whose
					// effective policy names an interceptor inside the
					// `fail-closed → fail-open` weakening cooldown, the
					// same protection against a timing-observable
					// fail-open window applied to the interceptor's own
					// failPolicy flip rather than the policy's
					// scanExportedFiles flip.
					if cdErr := s.InterceptorFailPolicyCooldown(ctx, pol.ContentPolicy.InterceptorRef); cdErr != nil {
						return cdErr
					}
					settings.PolicyAllowSelfRec = pol.AllowSelfRecursion
					// §8.2.bis layer 4 ceiling. DelegationPolicy has no
					// dedicated MaxDepth field in v1; the policy-ceiling
					// slot remains zero (no policy-imposed ceiling) and
					// the precedence chain falls through to the Helm
					// fallback. Reserved for the §8.3 ceiling extension.
					adm.policyCeiling = 0
				}
			}
		}
	}
	// §8.3 lines 157-188: contentPolicy inheritance + four-axis
	// monotonicity. The child's resolved contentPolicy (from the target
	// runtime's DelegationPolicy, when one applies) may only tighten the
	// parent's effective policy; a larger maxInputSize / maxExportedFileSize,
	// a scanExportedFiles true→false transition, or an interceptorRef
	// non-null→null rejects with *ContentPolicyWeakeningError, and a
	// different non-null interceptorRef rejects with
	// *ContentPolicyInterceptorSubstitutionError. The resolved effective
	// policy (childContentEff) is stamped on the child lease below so the
	// next hop inherits the transitively-narrowest cap (§8.3 line 240).
	// The check runs before pod allocation, alongside the SEC-001
	// isolation and treeVisibility monotonicity gates above. F-13.5.10.
	var childContentInput delegationpolicystore.ContentPolicy
	if adm.haveEffectivePolicy {
		childContentInput = adm.effectivePolicy.ContentPolicy
	}
	childContentEff, err := resolveChildContentPolicy(adm.parentContentEff, childContentInput, adm.haveEffectivePolicy)
	if err != nil {
		return err
	}
	adm.childContentEff = childContentEff

	decision := cycle.Decide(adm.lineage, target, settings)
	s.recordCycleDecision(req.PoolRef, tenantID, decision)
	s.recordCycleAudit(ctx, tenantID, req, decision)
	if decision.Outcome == cycle.OutcomeRejected {
		return cycle.ToError(decision, target)
	}
	adm.decision = decision
	return nil
}

// checkDelegationDepth runs the §8.2.bis stage-3 depth check. It resolves
// the maxDepth precedence chain (explicit-client → policy-ceiling →
// Helm-fallback) and enforces that the child's depth (the parent's
// lineage depth + 1) does not exceed the resolved ceiling.
//
// spec: §8.2.bis lines 81-89.
func (s *Service) checkDelegationDepth(req Request, adm admission) error {
	// §8.2.bis depth check (lines 81-89). The precedence chain is
	// explicit-client → preset → runtime-default → policy-ceiling →
	// Helm-fallback. v1 wires layers 1, 4, 5 (caller, policy ceiling,
	// Helm fallback); presets and runtime-level default leases are
	// future work. ResolveMaxDepth returns the first positive value,
	// always falling through to defaultMaxDepth so the resolved value
	// is positive. CheckDepth then enforces "the child's depth +1 must
	// not exceed maxDepth".
	resolvedDepth, err := lease.ResolveMaxDepth(lease.MaxDepthInputs{
		ExplicitClient: req.MaxDepth,
		PolicyCeiling:  adm.policyCeiling,
		HelmFallback:   s.defaultMaxDepth,
	})
	if err != nil {
		return err
	}
	resolvedDepth = lease.EnforcePolicyCeiling(resolvedDepth, adm.policyCeiling)
	return lease.CheckDepth(adm.depth, resolvedDepth)
}

// insertChildSession runs the §8.2 stage-4 atomic child-session insert.
// It validates the lease slice against the parent's granted budget,
// enforces the §11.1 per-user active-children cap, reserves the §8.2/§12.4
// tree budget (threading the reservation back to Delegate's deferred
// release through resvOut), materializes the §8.7 file export, mints the
// §8.2 child token, builds and commits the child row, registers it with
// the §8.6 lease budget source, observes the §16.1 depth metric, and
// emits the §11.7 `delegation.spawned` audit. It returns the §8.5 Result.
//
// spec: §8.2 lines 38-48, 57-61, 127; §8.6 line 648; §8.9 line 1010;
// §11.1 line 9; §11.7 line 62. F-8.2.2, F-8.2.18, F-11.1.4, F-15.3.5.
func (s *Service) insertChildSession(ctx context.Context, tenantID string, req Request, adm admission, resvOut **treebudget.Reservation) (Result, error) {
	parent := adm.parent
	// spec: §8.9 line 1010 — every node in a delegation tree shares the
	// same root_session_id (the apex session). The §12.4 budget counters
	// are keyed by it.
	rootSessionID := parent.RootSessionID
	if rootSessionID == "" {
		rootSessionID = parent.ID
	}

	// §8.2 / §11.1 / §12.4: the budget gates — static lease-slice ceiling,
	// per-user active-children cap, and the Redis-backed tree-budget
	// reservation. A successful reserve writes the reservation back through
	// resvOut so Delegate's deferred release returns it on a later error.
	if err := s.reserveTreeBudget(ctx, tenantID, req, parent, rootSessionID, resvOut); err != nil {
		return Result{}, err
	}

	// Build the child session row (id mint, §8.7 export materialization,
	// §8.2 child-token exchange, struct assembly, and §10.7 experiment
	// routing). The token is returned alongside the row for the audit and
	// the Result.
	child, childToken, err := s.buildChildSession(ctx, tenantID, req, adm, rootSessionID)
	if err != nil {
		return Result{}, err
	}
	if err := s.store.Create(ctx, child); err != nil {
		return Result{}, err
	}
	// §8.6 line 648: register the committed child with the lease-extension
	// budget source so an in-process budget-exhaustion extension (the
	// gateway LLM Proxy's ExtendForBudget trigger) from the child resolves
	// its tree (AddSession) and is capped at the parent's own granted lease
	// (SetParentLease). Done after the row commits so a registered child
	// always has a persisted backing row. F-15.3.5.
	if s.leaseRegistrar != nil {
		s.leaseRegistrar.AddSession(child.ID, rootSessionID, tenantID)
		s.leaseRegistrar.SetParentLease(child.ID, parentLeaseCeiling(parent))
	}
	// §8.2 / §16.1 line 27: observe the admitted child's depth onto
	// the `lenny_delegation_depth` histogram. Depth is invariant once
	// admitted, so sampling at admission and at session completion
	// produces the same distribution.
	childDepth := adm.depth + 1
	if s.metrics != nil {
		s.metrics.ObserveDelegationDepth(req.PoolRef, childDepth)
	}
	s.emitSpawnedAudit(ctx, req, parent, child, adm.decision, childToken, childDepth)
	return Result{Child: child, Depth: childDepth, ChildToken: childToken}, nil
}

// reserveTreeBudget runs the §8.2 budget gates that precede the child-row
// INSERT: the static §8.2 lease-slice ceiling against the parent's granted
// slice, the §11.1 per-user active-children cap, and the §8.2/§12.4
// Redis-backed tree-budget reservation. A successful reservation is written
// back through resvOut so Delegate's deferred release returns it if any
// later step before the commit fails. The gates run in this order so an
// over-limit delegation consumes no §12.4 counter state.
//
// spec: §8.2 lines 38-48, 57, 127; §11.1 line 9; §12.4 line 213.
// F-8.2.2, F-8.2.18, F-8.2.12, F-8.1.1, F-11.1.4.
func (s *Service) reserveTreeBudget(ctx context.Context, tenantID string, req Request, parent sessionstore.Session, rootSessionID string, resvOut **treebudget.Reservation) error {
	// §8.2 lines 38-48: validate the caller's requested lease_slice
	// against the parent's granted slice. A child may only tighten the
	// budget; a slice that exceeds the parent's remaining budget on any
	// axis is rejected with *lease.BudgetExceededError, which the §8.5
	// handler maps to BUDGET_EXHAUSTED (spec line 127). The parent's
	// granted slice (DelegationLease) is the v1 "remaining budget": the
	// per-call Redis debit of consumed tokens/children is the §8.2.12
	// follow-on, so admission here enforces the static subtree ceiling
	// (a child can never request more than the ancestor ever held). A
	// parent with no granted slice (root/standalone, or a child whose
	// lease declared no slice) leaves every parent axis zero, and
	// ValidateChildSlice admits any child against a zero axis. F-8.2.2.
	parentSlice := leaseSliceFromStore(parent.DelegationLease)
	if err := lease.ValidateChildSlice(
		parentSlice, req.LeaseSlice,
		parentSlice.MaxTokenBudget, parentSlice.MaxChildrenTotal, parentSlice.MaxTreeSize,
	); err != nil {
		return err
	}

	// §11.1 line 9: the per-user active-delegated-children admission cap.
	// The per-session breadth is bounded by the §8.2 lease/treebudget
	// axes reserved below; this scope bounds the aggregate count of live
	// children a single user can spread across all their sessions and
	// trees. Counted and rejected before any tree budget is reserved so an
	// over-limit delegation consumes no §12.4 counter state. The owning
	// user is the child's effective user_id (req override, else the
	// parent's, which §8.2 line 58 guarantees is non-empty). F-11.1.4.
	if s.maxActiveChildrenPerUser > 0 {
		owner := req.UserID
		if owner == "" {
			owner = parent.UserID
		}
		active, err := s.store.CountActiveDelegatedChildrenByUser(ctx, tenantID, owner)
		if err != nil {
			return err
		}
		if active >= s.maxActiveChildrenPerUser {
			return ErrUserChildrenExhausted
		}
	}

	// §8.2 lines 57, 127 / §12.4 line 213: gate the admission on the
	// Redis-backed per-tree budget counters. Unlike the static
	// ValidateChildSlice ceiling above (which only proves the child's
	// declared slice is no wider than the ancestor's), this reserves
	// against the live accumulated tree state: tree node count, the
	// ~12 KB-per-node gateway memory footprint, the delegating parent's
	// concurrent-children and total-descendant counters, and the tree
	// token pool. A cap breach rejects with *treebudget.BudgetExhaustedError
	// (mapped to BUDGET_EXHAUSTED); a Redis outage fails closed with
	// ErrBudgetUnavailable (mapped to the retryable
	// DELEGATION_BUDGET_UNAVAILABLE per §12.4 line 213). The reservation
	// is threaded back to Delegate through resvOut so its deferred release
	// returns the slice if any later step in the insert fails. F-8.2.18 /
	// F-8.2.12 / F-8.1.1.
	if s.treeBudget == nil {
		return nil
	}
	memCap := parentSlice.MaxTreeMemoryBytes
	if memCap == 0 {
		// §8.2 line 127: the lease carries a maxTreeMemoryBytes
		// default of 2 MB even when no explicit value was declared,
		// so the tree's gateway footprint is always bounded.
		memCap = treebudget.DefaultMaxTreeMemoryBytes
	}
	reservation := treebudget.Reservation{
		RootSessionID:         rootSessionID,
		ParentSessionID:       parent.ID,
		TreeSizeCap:           int64(parentSlice.MaxTreeSize),
		TreeSizeDelta:         1,
		TreeMemoryCap:         memCap,
		TreeMemoryDelta:       treebudget.PerNodeMemoryBytes,
		ParallelChildrenCap:   int64(parentSlice.MaxParallelChildren),
		ParallelChildrenDelta: 1,
		ChildrenTotalCap:      int64(parentSlice.MaxChildrenTotal),
		ChildrenTotalDelta:    1,
		TokenCap:              parentSlice.MaxTokenBudget,
		TokenDelta:            req.LeaseSlice.MaxTokenBudget,
	}
	if _, err := s.treeBudget.Reserve(ctx, reservation); err != nil {
		var bx *treebudget.BudgetExhaustedError
		if errors.As(err, &bx) {
			return &lease.BudgetExceededError{Violations: []string{bx.Error()}}
		}
		if errors.Is(err, treebudget.ErrBudgetUnavailable) {
			return ErrBudgetUnavailable
		}
		return err
	}
	*resvOut = &reservation
	return nil
}

// buildChildSession assembles the §8.2 child session row after every
// admission gate has passed. It mints the §12.6 child id, runs the §8.7
// file-export materialization when the lease declares exports, runs the
// §8.2 in-process child-token exchange when a minter and ParentToken are
// present, builds the child Session row (stamping the resolved lease,
// content policy, visibility, tracing, and experiment context), and runs
// the §10.7 ExperimentRouter for an independent-propagation child. It
// returns the constructed (uncommitted) row and the minted child token.
//
// spec: §8.2 lines 59-61, 90; §8.7; §8.9 line 1010; §10.7 lines 868, 905.
// F-8.1.2, F-8.2.7, F-8.7.1, F-8.9.8, F-10.7.5.
func (s *Service) buildChildSession(ctx context.Context, tenantID string, req Request, adm admission, rootSessionID string) (sessionstore.Session, *ChildToken, error) {
	parent := adm.parent
	userID := req.UserID
	if userID == "" {
		userID = parent.UserID
	}
	now := s.clock()
	// §8.7 / §8.2 steps 3, 4, 6: when the lease declares `fileExport`
	// entries, run the gateway-side export materialization before the
	// child row is committed. The child id is minted here (rather than
	// inline in the struct literal below) so the durable export blobs are
	// keyed to the same id the child row carries. A materialization
	// failure (validation, scan REJECT, persistence) aborts the
	// delegation before any child row exists, so there is no partially
	// materialized child workspace. F-8.7.1 / F-8.7.5 / F-8.7.6.
	childID, err := s.newChildID(rootSessionID)
	if err != nil {
		return sessionstore.Session{}, nil, err
	}
	var childPlan json.RawMessage
	if len(req.FileExport) > 0 {
		plan, err := s.materializeExport(ctx, tenantID, req, parent, childID, adm.effectivePolicy, adm.haveEffectivePolicy)
		if err != nil {
			return sessionstore.Session{}, nil, err
		}
		childPlan = plan
	}
	// §8.2 lines 59-61: mint the child session token via the in-process
	// RFC 8693 token exchange. This runs after every admission gate has
	// passed (cycle, depth, budget, export) but before the child row is
	// committed, so a revoked parent (the §13.3 actor-token freshness
	// check) rejects with ErrParentRevoked and no child session is
	// created. The minter narrows scope per the lease, builds the `act`
	// chain naming the parent, fixes delegation_depth at parent + 1, and
	// caps exp. A nil minter or a request without ParentToken skips the
	// leg (the in-process minimal gateway). F-8.1.2 / F-8.2.7.
	var childToken *ChildToken
	if s.tokenMinter != nil && req.ParentToken != nil {
		minted, err := s.tokenMinter.MintChildToken(ctx, ChildTokenParams{
			TenantID:              tenantID,
			ChildSessionID:        childID,
			ParentSessionID:       parent.ID,
			ParentSubject:         req.ParentToken.Subject,
			ParentJTI:             req.ParentToken.JTI,
			ParentDelegationDepth: int(parent.DelegationDepth),
			ParentScope:           req.ParentToken.Scope,
			RequestedScope:        req.ParentToken.Scope,
			ParentCallerType:      req.ParentToken.CallerType,
			Now:                   now,
		})
		if err != nil {
			return sessionstore.Session{}, nil, err
		}
		childToken = &minted
	}
	// rootSessionID was resolved above (the §12.4 budget counters are
	// keyed by it). A child inherits its parent's RootSessionID rather
	// than minting a new one; the §12.5 `idx_sessions_root` index
	// supports the single-shard tree-scoped query a §8.9 walker uses.
	// spec: §8.9 line 1010. F-8.9.8.
	child := sessionstore.Session{
		ID:               childID,
		TenantID:         tenantID,
		UserID:           userID,
		RuntimeRef:       req.RuntimeRef,
		PoolRef:          req.PoolRef,
		State:            session.StateCreated,
		IsolationProfile: adm.childProfile,
		ParentSessionID:  parent.ID,
		RootSessionID:    rootSessionID,
		// §10.7 lines 868, 905 — the child's delegation depth is the
		// parent's depth + 1, fixed at admission. Recording it here lets
		// the built-in eval endpoint populate EvalResult.delegation_depth
		// without re-walking the lineage on every submission. `depth` is
		// the parent's depth resolved by buildLineage above. F-10.7.5.
		DelegationDepth: uint32(adm.depth + 1),
		// §8.2 lines 38-48: stamp the granted lease_slice onto the child
		// so the child's own descendants validate against this ceiling.
		// Nil when the lease declared no slice (no budget binding at this
		// scope). §8.10 lines 1044-1049: the resolved lease-scoped policy
		// reference (delegationPolicyRef, effective maxDelegationPolicy,
		// contentPolicy interceptor) is captured here so a later tree
		// recovery resumes the node against the persisted lease record
		// instead of re-evaluating live policy. The resolved min
		// isolation profile rides the IsolationProfile column above.
		// F-8.2.2 / F-8.10.5.
		DelegationLease: stampLeasePolicy(
			storeLeaseFromSlice(req.LeaseSlice),
			adm.delegationPolicyRef, adm.effectivePolicy, adm.haveEffectivePolicy, adm.childContentEff,
		),
		// §8.3 lines 311-319: the monotonically-resolved visibility
		// boundary (inherited from the parent or narrowed by the lease)
		// is stamped on the child so lenny/get_task_tree scopes the
		// child's view from creation. F-8.5.2 / F-8.9.2.
		TreeVisibility: adm.childVis,
		// §8.3: the gateway attaches the parent's registered
		// tracingContext to every child it delegates.
		TracingContext: copyTracingContext(parent.TracingContext),
		// §8.3 / §10.7: the parent's experimentContext propagates onto
		// the child per the experiment's propagation mode.
		ExperimentContext: s.propagateExperimentContext(ctx, tenantID, parent),
		// §8.7 / §14: the materialized fileExport upload sources, stamped
		// here so the §6.3 binder delivers the exported files when it
		// materializes the child workspace (§8.2 step 6). Nil when the
		// lease declared no fileExport entries. F-8.7.1.
		WorkspacePlan: childPlan,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	// spec: §8.2 line 90 / §10.7 — under `independent` propagation
	// (or when the parent carries no experimentContext), the
	// ExperimentRouter evaluates the child afresh and may newly
	// enroll it. propagateExperimentContext returned nil in those
	// cases; running the router here completes the §10.7 routing
	// path that §8.2 step 2b mandates. `inherit` and `control`
	// leave the child with the propagated context and skip the
	// router. The router enforces isolation-monotonicity itself, so
	// a fail-closed *variantIsolationError aborts the delegation
	// rather than creating an unenrolled child.
	if s.experimentRouter != nil && child.ExperimentContext == nil {
		if err := s.experimentRouter.ApplyExperimentRouting(ctx, &child); err != nil {
			return sessionstore.Session{}, nil, err
		}
	}
	return child, childToken, nil
}

// emitSpawnedAudit emits the §11.7 / §16.7 `delegation.spawned` audit
// record after the child row commits, so audit consumers (billing, SIEM,
// compliance) observe the same record the store now reflects. The detail
// carries the §11.7 lineage attribution tuple, the §8.4 declared and
// effective approval modes, and (when the §8.2 child-token exchange ran)
// the minted `act` chain and child `jti` for the §13.3 recursive-revocation
// link. A nil auditor skips the emission.
//
// spec: §11.7 line 62; §16.7 catalog; §8.4 (F-8.4.3); §8.2 line 59.
// F-8.5.8, F-8.1.2, F-8.2.7.
func (s *Service) emitSpawnedAudit(ctx context.Context, req Request, parent, child sessionstore.Session, decision cycle.Decision, childToken *ChildToken, childDepth int) {
	if s.auditor == nil {
		return
	}
	// §8.4 / F-8.4.3: the audit record names both `approval_mode`
	// (the value declared by the lease, including the v1 `approval`
	// alias) and `effective_approval_mode` (the value the gateway
	// actually evaluated) so an auditor can confirm a v1 `approval`
	// is being treated as `policy`. An empty declared mode is
	// recorded as `policy` (the spec-default at evaluation time) so
	// the field is always populated.
	declaredMode := req.ApprovalMode
	if declaredMode == "" {
		declaredMode = lease.ApprovalModePolicy
	}
	detail := map[string]any{
		"parent_session_id":       parent.ID,
		"child_session_id":        child.ID,
		"delegation_depth":        childDepth,
		"runtime_ref":             child.RuntimeRef,
		"pool_ref":                child.PoolRef,
		"isolation_profile":       string(child.IsolationProfile),
		"is_self_recursive":       decision.IsSelfRecursive,
		"approval_mode":           string(declaredMode),
		"effective_approval_mode": string(lease.EffectiveApprovalMode(req.ApprovalMode)),
	}
	// §8.2 line 59 / §11.7: when the child-token exchange ran, the
	// audit record carries the minted `act` chain and child `jti` so
	// audit attribution and the §13.3 recursive-revocation path can
	// follow the parent→child identity link. F-8.1.2 / F-8.2.7.
	if childToken != nil {
		detail["child_token_jti"] = childToken.JTI
		detail["act_chain"] = childToken.Act
		detail["delegation_token_scope"] = childToken.Scope
	}
	s.auditor.EmitDelegationEvent(ctx, "delegation.spawned", detail)
}

// ResolveMaxInputSize returns the effective §8.3
// `contentPolicy.maxInputSize` byte cap for a delegation issued by
// parentSessionID, so the §4.8 DelegationPolicyEvaluator (PreDelegation,
// priority 250) measures the TaskSpec.input against the per-policy
// ceiling rather than the cluster-wide default alone. The cap is read
// from the DelegationPolicy named by the parent's runtime
// (`DelegationPolicyRef`): the same resolution chain Delegate runs for
// the cycle gate and the export scan. It returns ok == false when no
// runtime registry is wired, the runtime resolves no active policy, or
// the policy leaves `maxInputSize` at zero, leaving the evaluator on its
// configured default (§8.3 128 KiB).
//
// *Service implements policy.MaxInputSizeResolver. spec: §8.3 lines
// 149-157; §4.8 line 974. F-13.5.1 / F-8.2.9.
func (s *Service) ResolveMaxInputSize(ctx context.Context, tenantID, parentSessionID string) (int, bool) {
	maxSize, _, ok := s.ResolveContentPolicy(ctx, tenantID, parentSessionID)
	if !ok || maxSize <= 0 {
		return 0, false
	}
	return maxSize, true
}

// ResolveContentPolicy returns the effective §8.3 contentPolicy
// maxInputSize byte cap and interceptorRef for the given session — the
// parent session for a lenny/delegate_task call, the target session for a
// lenny/send_message delivery. The §4.8 PreDelegation and
// PreMessageDelivery interceptor phases use the interceptorRef to run the
// policy-named external content scanner alone rather than every
// registered external interceptor, and the message path enforces
// maxInputSize on the body (§4.8 line 1040, §13.5 mitigation 3).
//
// The fields are read from the DelegationPolicy named by the session's
// runtime (DelegationPolicyRef), the same resolution chain
// ResolveMaxInputSize and the export scan already use. ok is false when
// no runtime registry or policy store is wired, the session id is empty,
// the session or its runtime does not resolve, or the runtime names no
// active policy; the caller then falls back to the platform defaults (no
// external content scan, default maxInputSize). maxInputSize is returned
// verbatim from the policy and may be zero, which the caller treats as
// "use the default" rather than "no limit".
//
// spec: §8.3 lines 149-188; §4.8 lines 1036, 1040; §13.5 mitigations 2-3.
// F-8.2.9 / F-13.5.2.
func (s *Service) ResolveContentPolicy(ctx context.Context, tenantID, sessionID string) (maxInputSize int, interceptorRef string, ok bool) {
	if s.runtimes == nil || s.policies == nil || sessionID == "" {
		return 0, "", false
	}
	sess, err := s.store.Get(ctx, tenantID, sessionID)
	if err != nil {
		return 0, "", false
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, sess.RuntimeRef)
	if err != nil || rt.DelegationPolicyRef == "" {
		return 0, "", false
	}
	pol, err := s.policies.Get(ctx, tenantID, rt.DelegationPolicyRef)
	if err != nil || !pol.IsActive() {
		return 0, "", false
	}
	return pol.ContentPolicy.MaxInputSize, pol.ContentPolicy.InterceptorRef, true
}

// effContentPolicy is the §8.3 line-157 resolved effective contentPolicy
// carried on a delegation lease. The four axes (maxInputSize,
// interceptorRef, scanExportedFiles, maxExportedFileSize) are the
// inheritance and monotonicity subjects of §8.3 lines 157-188. The size
// axes are always concrete here (defaults applied), so a comparison never
// has to special-case an unset value. spec: §8.3 lines 157-188. F-13.5.10.
type effContentPolicy struct {
	MaxInputSize        int
	InterceptorRef      string
	ScanExportedFiles   bool
	MaxExportedFileSize int64
}

// normalizeContentPolicy lifts a stored DelegationPolicy.ContentPolicy
// into the concrete effective form, filling the §8.3 platform defaults for
// any size axis the policy left at zero. A policy persisted through the
// admin API already carries defaults (ApplyDefaults runs on write), but a
// directly-constructed policy may not, so the fill is defensive.
func normalizeContentPolicy(cp delegationpolicystore.ContentPolicy) effContentPolicy {
	out := effContentPolicy{
		MaxInputSize:        cp.MaxInputSize,
		InterceptorRef:      cp.InterceptorRef,
		ScanExportedFiles:   cp.ScanExportedFiles,
		MaxExportedFileSize: cp.MaxExportedFileSize,
	}
	if out.MaxInputSize <= 0 {
		out.MaxInputSize = delegationpolicystore.DefaultMaxInputSize
	}
	if out.MaxExportedFileSize <= 0 {
		out.MaxExportedFileSize = delegationpolicystore.DefaultMaxExportedFileSize
	}
	return out
}

// platformDefaultContentPolicy is the §8.3 baseline used when no
// DelegationPolicy applies anywhere on the path from root to the parent:
// the default size caps, no interceptor, and no export scanning.
func platformDefaultContentPolicy() effContentPolicy {
	return effContentPolicy{
		MaxInputSize:        delegationpolicystore.DefaultMaxInputSize,
		MaxExportedFileSize: delegationpolicystore.DefaultMaxExportedFileSize,
	}
}

// tighterThanDefault reports whether the effective policy departs from the
// §8.3 platform default on any axis, so a default-only effective policy is
// not persisted onto every child lease.
func (e effContentPolicy) tighterThanDefault() bool {
	return e.InterceptorRef != "" || e.ScanExportedFiles ||
		e.MaxInputSize < delegationpolicystore.DefaultMaxInputSize ||
		e.MaxExportedFileSize < delegationpolicystore.DefaultMaxExportedFileSize
}

// effectiveParentContentPolicy resolves the parent session's effective
// (transitively-narrowest) contentPolicy. A parent that was itself
// delegated under this feature carries the resolved policy on its lease;
// the gateway reads it back rather than re-walking the chain. A root or
// pre-feature parent has no stamped policy, so the gateway derives the
// effective policy from the parent's own runtime DelegationPolicy, falling
// back to the §8.3 platform default. spec: §8.3 lines 157, 240. F-13.5.10.
func (s *Service) effectiveParentContentPolicy(ctx context.Context, tenantID string, parent sessionstore.Session) effContentPolicy {
	if l := parent.DelegationLease; l != nil &&
		(l.ContentMaxInputSize > 0 || l.ContentPolicyRef != "" ||
			l.ContentScanExportedFiles || l.ContentMaxExportedFileSize > 0) {
		eff := effContentPolicy{
			MaxInputSize:        l.ContentMaxInputSize,
			InterceptorRef:      l.ContentPolicyRef,
			ScanExportedFiles:   l.ContentScanExportedFiles,
			MaxExportedFileSize: l.ContentMaxExportedFileSize,
		}
		if eff.MaxInputSize <= 0 {
			eff.MaxInputSize = delegationpolicystore.DefaultMaxInputSize
		}
		if eff.MaxExportedFileSize <= 0 {
			eff.MaxExportedFileSize = delegationpolicystore.DefaultMaxExportedFileSize
		}
		return eff
	}
	if s.runtimes != nil && s.policies != nil {
		if rt, err := runtimestore.Resolve(ctx, s.runtimes, parent.RuntimeRef); err == nil && rt.DelegationPolicyRef != "" {
			if pol, err := s.policies.Get(ctx, tenantID, rt.DelegationPolicyRef); err == nil && pol.IsActive() {
				return normalizeContentPolicy(pol.ContentPolicy)
			}
		}
	}
	return platformDefaultContentPolicy()
}

// resolveChildContentPolicy applies the §8.3 lines 157-188 contentPolicy
// inheritance and monotonicity rules and returns the child's effective
// policy. parentEff is the parent's effective (transitively-narrowest)
// policy. When the child's target runtime resolved a DelegationPolicy
// (haveChild), that policy's declared contentPolicy is the child's
// declaration and must be at least as strict as parentEff on every axis;
// otherwise the child inherits parentEff verbatim. A weakening on any axis
// returns *ContentPolicyWeakeningError, and a different non-null
// interceptorRef returns *ContentPolicyInterceptorSubstitutionError. The
// returned effective policy is the per-axis narrowest, which (because each
// axis is verified no looser than the parent) equals the child's declared
// policy when one applies. spec: §8.3 lines 157-188, 240. F-13.5.10.
func resolveChildContentPolicy(parentEff effContentPolicy, child delegationpolicystore.ContentPolicy, haveChild bool) (effContentPolicy, error) {
	if !haveChild {
		return parentEff, nil
	}
	declared := normalizeContentPolicy(child)
	// §8.3 line 157: maxInputSize may only shrink across a hop.
	if declared.MaxInputSize > parentEff.MaxInputSize {
		return effContentPolicy{}, &ContentPolicyWeakeningError{
			Axis:        "maxInputSize",
			ParentValue: strconv.Itoa(parentEff.MaxInputSize),
			ChildValue:  strconv.Itoa(declared.MaxInputSize),
		}
	}
	// §8.3 line 179: maxExportedFileSize is a protective ceiling; a child
	// may set a smaller value but never a larger one.
	if declared.MaxExportedFileSize > parentEff.MaxExportedFileSize {
		return effContentPolicy{}, &ContentPolicyWeakeningError{
			Axis:        "maxExportedFileSize",
			ParentValue: strconv.FormatInt(parentEff.MaxExportedFileSize, 10),
			ChildValue:  strconv.FormatInt(declared.MaxExportedFileSize, 10),
		}
	}
	// §8.3 lines 170-177: scanExportedFiles uses the ordering false < true;
	// a parent `true` child `false` removes the scan requirement and is
	// rejected.
	if parentEff.ScanExportedFiles && !declared.ScanExportedFiles {
		return effContentPolicy{}, &ContentPolicyWeakeningError{
			Axis:        "scanExportedFiles",
			ParentValue: "true",
			ChildValue:  "false",
		}
	}
	// §8.3 lines 183-188: interceptorRef restrictiveness is identity-based.
	switch {
	case parentEff.InterceptorRef == "":
		// Null parent: any child (including a new ref) is permitted.
	case declared.InterceptorRef == parentEff.InterceptorRef:
		// Condition 1: same reference, always permitted.
	case declared.InterceptorRef == "":
		// Condition 3: non-null → null removes a content check (line 187).
		return effContentPolicy{}, &ContentPolicyWeakeningError{
			Axis:        "interceptorRef",
			ParentValue: parentEff.InterceptorRef,
			ChildValue:  "",
		}
	default:
		// Condition 4: a different non-null reference cannot be verified
		// as equally restrictive (line 188).
		return effContentPolicy{}, &ContentPolicyInterceptorSubstitutionError{
			ParentRef: parentEff.InterceptorRef,
			ChildRef:  declared.InterceptorRef,
		}
	}
	return declared, nil
}

// materializeExport runs the §8.7 file-export pipeline for one delegation
// and returns the §14 child WorkspacePlan JSON the caller stamps on the
// child row. It resolves the §8.3 fileExportLimits ceiling (defaulting to
// the §8.3 line 264 defaults when the lease left it unset) and, when the
// effective DelegationPolicy sets contentPolicy.scanExportedFiles, the
// per-file content-scan chain. A scan-required policy with no resolver
// fails closed with ErrExportScanUnavailable so unscanned files are never
// materialized (§8.3 rule 1). A nil materializer fails with
// ErrExportNotConfigured. F-8.7.1 / F-8.7.5 / F-8.7.6.
func (s *Service) materializeExport(ctx context.Context, tenantID string, req Request, parent sessionstore.Session, childID string, pol delegationpolicystore.DelegationPolicy, havePol bool) (json.RawMessage, error) {
	if s.exportMat == nil {
		return nil, ErrExportNotConfigured
	}
	// spec: §8.3 line 264 — fileExportLimits defaults to 100 files /
	// 100 MiB when the lease omits it. The zero Request value selects the
	// defaults; a partially-set limit is taken verbatim.
	limits := req.FileExportLimits
	if limits.MaxFiles == 0 && limits.MaxTotalSize == 0 {
		limits = fileexport.DefaultFileExportLimits
	}
	params := export.Params{
		ParentSessionID: parent.ID,
		ChildSessionID:  childID,
		TenantID:        tenantID,
		Specs:           append([]export.Spec(nil), req.FileExport...),
		Limits:          limits,
	}
	// spec: §8.3 lines 160-181 — the per-file content scan runs only when
	// the effective DelegationPolicy enables scanExportedFiles. Without a
	// resolvable interceptor the scan cannot run; fail closed rather than
	// materialize unscanned files.
	if havePol && pol.ContentPolicy.ScanExportedFiles {
		if s.exportScanResolver == nil {
			return nil, ErrExportScanUnavailable
		}
		chain, scanCtx, err := s.exportScanResolver.ResolveExportScanChain(ctx, tenantID, pol.ContentPolicy.InterceptorRef)
		if err != nil {
			return nil, err
		}
		// spec: §11.7 line 119 / §16.1 line 80 — the policy_name and pool
		// labels come from the per-delegation context the resolver does not
		// see (it is keyed only on tenant + interceptorRef).
		scanCtx.PolicyName = pol.Name
		scanCtx.Pool = req.PoolRef
		params.Scan = export.ContentScan{
			Enabled:     true,
			MaxFileSize: pol.ContentPolicy.MaxExportedFileSize,
			Chain:       chain,
			ScanCtx:     scanCtx,
		}
	}
	res, err := s.exportMat.Materialize(ctx, params)
	if err != nil {
		return nil, err
	}
	return res.WorkspacePlanJSON()
}

// recordCycleAudit emits the §8.2 / §16.7 cycle-gate audit pair:
//
//   - `delegation.self_recursion_allowed` fires when the §8.2 three-layer
//     gate admitted a self-recursive hop under `mode: enforce`.
//   - `delegation.cycle_warning` fires when the gate would have rejected
//     the hop but `mode: warn` admitted it anyway; the detail names the
//     blocking layers so an operator can attribute the would-have-blocked
//     counter row to a config knob.
//
// Outside the self-recursive path the function is a no-op so the audit
// log is not polluted with every successful non-recursive admission.
//
// spec: §8.2 lines 70-79; §16.7 catalog. F-8.5.9.
func (s *Service) recordCycleAudit(ctx context.Context, tenantID string, req Request, d cycle.Decision) {
	if s.auditor == nil || !d.IsSelfRecursive {
		return
	}
	switch d.Outcome {
	case cycle.OutcomeAdmitted:
		if d.EffectiveSettings.Mode != cycle.ModeEnforce {
			return
		}
		s.auditor.EmitDelegationEvent(ctx, "delegation.self_recursion_allowed", map[string]any{
			"parent_session_id":       req.ParentSessionID,
			"runtime_ref":             req.RuntimeRef,
			"pool_ref":                req.PoolRef,
			"tenant_id":               tenantID,
			"mode":                    string(d.EffectiveSettings.Mode),
			"platform_allow_self_rec": d.EffectiveSettings.PlatformAllowSelfRec,
			"runtime_allow_self_rec":  d.EffectiveSettings.RuntimeAllowSelfRec,
			"policy_allow_self_rec":   d.EffectiveSettings.PolicyAllowSelfRec,
		})
	case cycle.OutcomeWouldHaveBlocked:
		s.auditor.EmitDelegationEvent(ctx, "delegation.cycle_warning", map[string]any{
			"parent_session_id":         req.ParentSessionID,
			"runtime_ref":               req.RuntimeRef,
			"pool_ref":                  req.PoolRef,
			"tenant_id":                 tenantID,
			"mode":                      string(d.EffectiveSettings.Mode),
			"blocked_by":                string(d.BlockedBy),
			"would_have_blocked_layers": layersAsStrings(d.WouldHaveBlockedLayers),
		})
	}
}

// layersAsStrings converts a slice of cycle.Layer values to plain
// strings so the audit Detail map carries portable JSON values.
func layersAsStrings(in []cycle.Layer) []string {
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, string(l))
	}
	return out
}

// recordCycleDecision emits the §8.2 three-layer-gate
// `lenny_delegation_would_have_blocked_total` attribution rows for the
// cycle decision. Rules:
//   - OutcomeAdmitted with no would-have-blocked layers: no emission.
//   - OutcomeRejected (mode=enforce): one row per failing layer with
//     `mode="enforce"` — the per-layer rejection-cause attribution.
//   - OutcomeWouldHaveBlocked (mode=warn): one row per failing layer
//     with `mode="warn"` — the per-layer diagnostic attribution.
//   - OutcomePermissive: never emitted (mode=permissive disables
//     evaluation per §16.1 catalog).
//
// spec: §8.2 line 70; §16.1 line 79.
func (s *Service) recordCycleDecision(pool, tenantID string, d cycle.Decision) {
	if s.metrics == nil || len(d.WouldHaveBlockedLayers) == 0 {
		return
	}
	mode := string(d.EffectiveSettings.Mode)
	if mode == "" {
		mode = string(cycle.ModeEnforce)
	}
	if mode == string(cycle.ModePermissive) {
		return
	}
	for _, layer := range d.WouldHaveBlockedLayers {
		s.metrics.IncDelegationWouldHaveBlocked(pool, tenantID, string(layer), mode)
	}
}

// propagateExperimentContext applies the §8.3/§10.7 experiment-context
// propagation rule to a delegation child. When the parent is enrolled
// in an experiment, the experiment's propagation mode decides the
// child's context: `inherit` copies the parent's enrollment verbatim,
// `control` forces the parent's experiment onto the control variant,
// and `independent` leaves the child unenrolled here — it is routed
// afresh by the ExperimentRouter at its own creation. A propagated
// context is recorded with inherited=true. Returns nil when no
// experiment store is wired, the parent carries no experiment context,
// or the experiment is unresolvable.
func (s *Service) propagateExperimentContext(ctx context.Context, tenantID string, parent sessionstore.Session) *sessionstore.ExperimentContext {
	if s.experiments == nil || parent.ExperimentContext == nil {
		return nil
	}
	exp, err := s.experiments.Get(ctx, tenantID, parent.ExperimentContext.ExperimentID)
	if err != nil {
		return nil
	}
	prop := experiment.PropagateContext(
		parent.ExperimentContext.ExperimentID,
		parent.ExperimentContext.VariantID,
		exp.Definition().Propagation,
	)
	if !prop.UseParentContext {
		return nil
	}
	return &sessionstore.ExperimentContext{
		ExperimentID: prop.ExperimentID,
		VariantID:    prop.VariantID,
		Inherited:    true,
	}
}

// checkInterceptorWeakeningCooldown returns a typed
// InterceptorWeakeningCooldownError when the resolved DelegationPolicy
// is still inside the §8.3 line 181 cluster-scoped
// `gateway.interceptorWeakeningCooldownSeconds` window opened by a
// `scanExportedFiles: true → false` admin update. Returns nil when the
// cooldown is disabled (interceptorWeakeningCooldown == 0), the policy
// row never weakened (ScanExportedFilesWeakenedAt zero), or the window
// has expired. The §8.5 `lenny/delegate_task` MCP handler maps the
// typed error to `INTERCEPTOR_WEAKENING_COOLDOWN` (TRANSIENT, HTTP 503)
// with `details.policyName` and `details.retryAfterSeconds`.
// F-8.7.12 / F-13.5.7.
func (s *Service) checkInterceptorWeakeningCooldown(pol delegationpolicystore.DelegationPolicy) error {
	if s.interceptorWeakeningCooldown <= 0 || pol.ScanExportedFilesWeakenedAt.IsZero() {
		return nil
	}
	elapsed := s.clock().Sub(pol.ScanExportedFilesWeakenedAt)
	if elapsed < 0 {
		// Clock skew: treat as freshly-armed so the window still
		// applies. elapsed=0 maps to the full cooldown remaining.
		elapsed = 0
	}
	remaining := s.interceptorWeakeningCooldown - elapsed
	if remaining <= 0 {
		return nil
	}
	// Round-up so a sub-second tail still produces retryAfter=1.
	retryAfter := int((remaining + time.Second - 1) / time.Second)
	if retryAfter < 1 {
		retryAfter = 1
	}
	return &InterceptorWeakeningCooldownError{
		PolicyName:        pol.Name,
		TransitionTs:      pol.ScanExportedFilesWeakenedAt,
		CooldownSeconds:   int(s.interceptorWeakeningCooldown / time.Second),
		RetryAfterSeconds: retryAfter,
	}
}

// InterceptorFailPolicyCooldown returns a typed
// InterceptorWeakeningCooldownError when interceptorRef names an external
// interceptor still inside the §4.8 line 1034 / §8.3 SEC-013
// `fail-closed → fail-open` weakening cooldown window. The window length
// is the cluster-scoped `gateway.interceptorWeakeningCooldownSeconds`
// value that was in force at the transition (the §8.3 meta-cooldown rule
// pins a pending cooldown to that recorded value rather than the live
// config, so cutting the cluster value never shortens an active
// cooldown). Returns nil when interceptorRef is empty, no resolver is
// wired, the interceptor never weakened, or the window has expired. The
// §8.5 `lenny/delegate_task` and `lenny/send_message` MCP handlers map
// the typed error to `INTERCEPTOR_WEAKENING_COOLDOWN` (TRANSIENT, HTTP
// 503). F-4.8.17.
func (s *Service) InterceptorFailPolicyCooldown(ctx context.Context, interceptorRef string) error {
	if interceptorRef == "" || s.interceptorCooldown == nil {
		return nil
	}
	transitionTs, cooldownSeconds, ok := s.interceptorCooldown.FailOpenCooldown(ctx, interceptorRef)
	if !ok || transitionTs.IsZero() || cooldownSeconds <= 0 {
		return nil
	}
	window := time.Duration(cooldownSeconds) * time.Second
	elapsed := s.clock().Sub(transitionTs)
	if elapsed < 0 {
		// Clock skew: treat as freshly-armed so the window still applies.
		elapsed = 0
	}
	remaining := window - elapsed
	if remaining <= 0 {
		return nil
	}
	retryAfter := int((remaining + time.Second - 1) / time.Second)
	if retryAfter < 1 {
		retryAfter = 1
	}
	return &InterceptorWeakeningCooldownError{
		InterceptorRef:    interceptorRef,
		TransitionTs:      transitionTs,
		CooldownSeconds:   cooldownSeconds,
		RetryAfterSeconds: retryAfter,
	}
}

// EffectiveDelegationPolicy resolves the §8.3 effective DelegationPolicy
// for a calling session: the policy named by the session's resolved
// runtime via DelegationPolicyRef. It returns (policy, true, nil) when
// an active policy resolves, and (zero, false, nil) when the session
// names no policy, the runtime or policy is missing or soft-deleted, or
// no runtime/policy registry is configured. In every "false" case the
// caller imposes no policy-level restriction, matching the way the
// Delegate path treats an absent DelegationPolicyRef (it leaves the
// policy layer at its default rather than denying).
//
// The §8.5 lenny/discover_agents handler calls this to narrow the
// discoverable agent set to the targets the caller's effective policy
// authorizes (§8.3 line 244). The resolution reads the runtime-level
// DelegationPolicyRef, the same input the runtime layer of Delegate
// consults, so discovery and delegation agree on which policy governs a
// session. The lease-level maxDelegationPolicy intersection and the
// ancestral-narrowing refinement are not yet part of either path; when
// they land in Delegate they extend here too.
//
// spec: §8.3 line 244. F-8.5.7.
func (s *Service) EffectiveDelegationPolicy(ctx context.Context, tenantID, sessionID string) (delegationpolicystore.DelegationPolicy, bool, error) {
	if s.policies == nil || s.runtimes == nil || sessionID == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	sess, err := s.store.Get(ctx, tenantID, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return delegationpolicystore.DelegationPolicy{}, false, nil
		}
		return delegationpolicystore.DelegationPolicy{}, false, err
	}
	if sess.RuntimeRef == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, sess.RuntimeRef)
	if err != nil {
		// An unresolvable runtime imposes no policy filter — the same
		// conservative fall-through the Delegate path takes when the
		// runtime registry cannot resolve the target.
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	if rt.DelegationPolicyRef == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	pol, err := s.policies.Get(ctx, tenantID, rt.DelegationPolicyRef)
	if err != nil || !pol.IsActive() {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	return pol, true, nil
}

// ResolveActivePolicy returns the named §8.3 DelegationPolicy when the
// policy registry is wired, the name is non-empty, and the policy
// resolves to an active (non-soft-deleted) row. Every other case —
// no registry, empty name, a missing policy, or a soft-deleted policy —
// returns (zero, false, nil), so a caller treats an unresolved
// reference as "no policy-level restriction", matching the conservative
// fall-through EffectiveDelegationPolicy applies to a runtime-level
// reference.
//
// The §10.6 environment layer in the gateway calls this to apply an
// environment's defaultDelegationPolicy (§10.6 line 601 — "the
// DelegationPolicy applied to sessions created in this environment") as
// an additional intersection in the §10.6 line 629 effective-scope
// formula. The delegation Service does not itself read the §10.6
// Environment registry; the gateway resolves the policy name from the
// environment and hands it here for a uniform active-policy lookup.
// spec: §10.6 line 601, line 629. F-10.6.7.
func (s *Service) ResolveActivePolicy(ctx context.Context, tenantID, name string) (delegationpolicystore.DelegationPolicy, bool, error) {
	if s.policies == nil || name == "" {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	pol, err := s.policies.Get(ctx, tenantID, name)
	if err != nil {
		if errors.Is(err, delegationpolicystore.ErrNotFound) {
			return delegationpolicystore.DelegationPolicy{}, false, nil
		}
		return delegationpolicystore.DelegationPolicy{}, false, err
	}
	if !pol.IsActive() {
		return delegationpolicystore.DelegationPolicy{}, false, nil
	}
	return pol, true, nil
}

// lineageWalkSafetyMargin is the additive cap above the Helm
// fallback maxDepth applied to the buildLineage walk. The walk stops
// once chain length reaches defaultMaxDepth + safetyMargin even when
// further ancestors are reachable; the §8.2.bis depth check below
// will then reject the delegation with a saturated depth value.
// F-8.2.16 — bounds the per-delegate-task store-lookup fan-out under
// a misbehaving or pathologically deep store.
const lineageWalkSafetyMargin = 4

// buildLineage walks the ParentSessionID chain from the parent up to
// the root, returning the §8.2 Lineage (root-first) and the
// parent's depth (root = 0). A cycle in the stored chain is
// defended against with a visited set. The walk is additionally
// bounded by defaultMaxDepth + lineageWalkSafetyMargin so a
// pathological chain (or a stored chain longer than the active
// maxDepth ceiling) does not produce an unbounded store fan-out per
// delegate_task call. spec: §8.2.
func (s *Service) buildLineage(ctx context.Context, tenantID string, parent sessionstore.Session) (cycle.Lineage, int, error) {
	walkCap := s.defaultMaxDepth + lineageWalkSafetyMargin
	if walkCap <= 0 {
		walkCap = DefaultMaxDepth + lineageWalkSafetyMargin
	}
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
		if len(chain) >= walkCap {
			// F-8.2.16: chain already exceeds the active maxDepth
			// ceiling plus a safety margin; the depth check below
			// will reject the delegation regardless of whether
			// further ancestors exist. Stop walking to bound the
			// store fan-out.
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

// resolveChildTreeVisibility applies the §8.3 lines 313-319
// treeVisibility inheritance and monotonicity rules. An absent child
// value (field omitted) inherits the parent's effective value; a present
// value must be at least as narrow as the parent's effective value (the
// strict ordering full → parent-and-self → self-only: a child may narrow
// at any hop but never widen). An unrecognised explicit value is
// rejected with a plain error the §8.5 handler maps to
// INVALID_LEASE_FIELD; a widening is rejected with the typed
// *TreeVisibilityWeakeningError. spec: §8.3 lines 311-319.
func resolveChildTreeVisibility(parent, child session.TreeVisibility) (session.TreeVisibility, error) {
	parentEff := parent.OrDefault()
	if child == "" {
		return parentEff, nil
	}
	if !child.IsValid() {
		return "", fmt.Errorf("delegation: treeVisibility %q is not a recognised §8.5 value (full, parent-and-self, self-only)", child)
	}
	if !child.AtLeastAsNarrow(parentEff) {
		return "", &TreeVisibilityWeakeningError{ParentVisibility: parentEff, ChildVisibility: child}
	}
	return child, nil
}

// copyTracingContext returns a deep copy of the §8.3 tracingContext so
// a delegated child does not alias the parent's map. Returns nil when
// the parent registered no context.
func copyTracingContext(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// leaseSliceFromStore translates the persisted §8.2 granted lease into
// the pure lease.LeaseSlice the arithmetic operates on. A nil persisted
// lease yields the zero slice (no budget binding), so ValidateChildSlice
// admits any child against it. F-8.2.2.
func leaseSliceFromStore(l *sessionstore.DelegationLease) lease.LeaseSlice {
	if l == nil {
		return lease.LeaseSlice{}
	}
	return lease.LeaseSlice{
		MaxTokenBudget:      l.MaxTokenBudget,
		MaxChildrenTotal:    l.MaxChildrenTotal,
		MaxTreeSize:         l.MaxTreeSize,
		MaxTreeMemoryBytes:  l.MaxTreeMemoryBytes,
		MaxParallelChildren: l.MaxParallelChildren,
		PerChildMaxAge:      l.PerChildMaxAge,
	}
}

// storeLeaseFromSlice translates a caller-declared §8.2 lease_slice into
// the persisted granted lease stamped on the child row. An all-zero
// slice (no budget binding) yields nil so the column stays NULL. F-8.2.2.
func storeLeaseFromSlice(s lease.LeaseSlice) *sessionstore.DelegationLease {
	dl := &sessionstore.DelegationLease{
		MaxTokenBudget:      s.MaxTokenBudget,
		MaxChildrenTotal:    s.MaxChildrenTotal,
		MaxTreeSize:         s.MaxTreeSize,
		MaxTreeMemoryBytes:  s.MaxTreeMemoryBytes,
		MaxParallelChildren: s.MaxParallelChildren,
		PerChildMaxAge:      s.PerChildMaxAge,
	}
	if dl.IsZero() {
		return nil
	}
	return dl
}

// stampLeasePolicy records the §8.10 lines 1044-1049 lease-scoped policy
// reference and the §8.3 line-157 resolved effective contentPolicy onto
// the granted lease so tree recovery can bring the node back up against
// the persisted record instead of re-evaluating the live policy state, and
// so the next delegation hop inherits the transitively-narrowest
// contentPolicy (§8.3 line 240). It captures the resolved
// `delegationPolicyRef`, the effective `maxDelegationPolicy` (the resolved
// DelegationPolicy name), and the four contentPolicy axes via contentEff.
// Only a contentPolicy that departs from the §8.3 platform default is
// persisted (interceptor set, scanExportedFiles true, or a size below the
// default), so a default-only effective policy leaves the content fields
// empty and the read path treats a zero size as the default. When the
// resource slice was nil but a policy reference or a non-default
// contentPolicy exists, it allocates a lease record so those fields
// persist. The lease-scoped min isolation profile rides the session's
// first-class IsolationProfile column, so it is not duplicated here. v1
// does not implement `snapshotPolicyAtLease`, so `snapshotted_pool_ids`
// stays empty and post-recovery delegations evaluate live pool labels
// exactly as a pre-failure call would. F-8.10.5 / F-13.5.10.
func stampLeasePolicy(dl *sessionstore.DelegationLease, delegationPolicyRef string, pol delegationpolicystore.DelegationPolicy, havePolicy bool, contentEff effContentPolicy) *sessionstore.DelegationLease {
	stampContent := contentEff.tighterThanDefault()
	if delegationPolicyRef == "" && !havePolicy && !stampContent {
		return dl
	}
	if dl == nil {
		dl = &sessionstore.DelegationLease{}
	}
	if delegationPolicyRef != "" {
		dl.DelegationPolicyRef = delegationPolicyRef
	}
	if havePolicy {
		dl.MaxDelegationPolicy = pol.Name
	}
	if stampContent {
		dl.ContentPolicyRef = contentEff.InterceptorRef
		dl.ContentScanExportedFiles = contentEff.ScanExportedFiles
		// A size at the platform default is left unstamped (zero) so the
		// read path resolves it back to the default; only a tightened
		// value is persisted.
		if contentEff.MaxInputSize < delegationpolicystore.DefaultMaxInputSize {
			dl.ContentMaxInputSize = contentEff.MaxInputSize
		}
		if contentEff.MaxExportedFileSize < delegationpolicystore.DefaultMaxExportedFileSize {
			dl.ContentMaxExportedFileSize = contentEff.MaxExportedFileSize
		}
	}
	if dl.IsZero() {
		return nil
	}
	return dl
}

// newChildID mints the §12.6 session id for a delegated child. A test
// override (Options.IDFunc) takes precedence and returns its fixed id
// verbatim. Otherwise the child id copies the routing prefix from
// rootSessionID (session.NewChildID) so every session in the delegation
// tree co-locates on the same shard. spec: §12.6 line 577. F-12.6.13.
func (s *Service) newChildID(rootSessionID string) (string, error) {
	if s.idFn != nil {
		return s.idFn(), nil
	}
	return session.NewChildID(rootSessionID)
}
