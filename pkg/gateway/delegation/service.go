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
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
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

	// UserID owns the child session. Inherits the parent's when
	// blank.
	UserID string

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
}

// Result is the outcome of a successful Delegate call.
type Result struct {
	// Child is the newly created child session row.
	Child sessionstore.Session

	// Depth is the child's depth in the delegation tree (root = 0).
	Depth int
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

	// TransitionTs is the server-minted scanExportedFiles weakening
	// timestamp recorded on the policy row.
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
	// §8.4: validate the closed enum at the service boundary so a
	// malformed value is rejected with *lease.InvalidApprovalModeError
	// before any side effects (parent lookup, audit emission, store
	// writes). The §8.5 lenny/delegate_task handler maps the typed
	// error to INVALID_LEASE_FIELD. F-8.4.2.
	if err := lease.ValidateApprovalMode(req.ApprovalMode); err != nil {
		return Result{}, err
	}
	// §8.4 line 521: an `approvalMode: "deny"` lease short-circuits
	// the delegation path before pod allocation and before the §4
	// PreDelegation interceptor. The gateway maps ErrDelegationDenied
	// to DELEGATION_DENIED at the §8.5 handler. F-8.4.1.
	if req.ApprovalMode == lease.ApprovalModeDeny {
		return Result{}, ErrDelegationDenied
	}
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
		return Result{}, ErrParentNoUser
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
	var policyCeiling int
	if s.runtimes != nil {
		if rt, err := runtimestore.Resolve(ctx, s.runtimes, req.RuntimeRef); err == nil {
			if rt.Type == runtimestore.TypeMCP {
				return Result{}, ErrTargetNotAgent
			}
			settings.RuntimeAllowSelfRec = rt.AllowSelfRecursion
			if s.policies != nil && rt.DelegationPolicyRef != "" {
				if pol, err := s.policies.Get(ctx, tenantID, rt.DelegationPolicyRef); err == nil && pol.IsActive() {
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
						return Result{}, cdErr
					}
					settings.PolicyAllowSelfRec = pol.AllowSelfRecursion
					// §8.2.bis layer 4 ceiling. DelegationPolicy has no
					// dedicated MaxDepth field in v1; the policy-ceiling
					// slot remains zero (no policy-imposed ceiling) and
					// the precedence chain falls through to the Helm
					// fallback. Reserved for the §8.3 ceiling extension.
					policyCeiling = 0
				}
			}
		}
	}
	decision := cycle.Decide(lineage, target, settings)
	s.recordCycleDecision(req.PoolRef, tenantID, decision)
	s.recordCycleAudit(ctx, tenantID, req, decision)
	if decision.Outcome == cycle.OutcomeRejected {
		return Result{}, cycle.ToError(decision, target)
	}

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
		PolicyCeiling:  policyCeiling,
		HelmFallback:   s.defaultMaxDepth,
	})
	if err != nil {
		return Result{}, err
	}
	resolvedDepth = lease.EnforcePolicyCeiling(resolvedDepth, policyCeiling)
	if err := lease.CheckDepth(depth, resolvedDepth); err != nil {
		return Result{}, err
	}

	userID := req.UserID
	if userID == "" {
		userID = parent.UserID
	}
	now := s.clock()
	// spec: §8.9 line 1010 — every node in a delegation tree shares
	// the same root_session_id (the session at the tree's apex). A
	// child inherits its parent's RootSessionID rather than minting a
	// new one; the parent's RootSessionID is its tree root when the
	// parent itself was delegated, or the parent's own id when the
	// parent is a standalone session that has not yet been re-keyed by
	// the store. The §12.5 `idx_sessions_root` index supports the
	// single-shard tree-scoped query a §8.9 walker uses. F-8.9.8.
	rootSessionID := parent.RootSessionID
	if rootSessionID == "" {
		rootSessionID = parent.ID
	}
	child := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           userID,
		RuntimeRef:       req.RuntimeRef,
		PoolRef:          req.PoolRef,
		State:            session.StateCreated,
		IsolationProfile: childProfile,
		ParentSessionID:  parent.ID,
		RootSessionID:    rootSessionID,
		// §8.3: the gateway attaches the parent's registered
		// tracingContext to every child it delegates.
		TracingContext: copyTracingContext(parent.TracingContext),
		// §8.3 / §10.7: the parent's experimentContext propagates onto
		// the child per the experiment's propagation mode.
		ExperimentContext: s.propagateExperimentContext(ctx, tenantID, parent),
		CreatedAt:         now,
		UpdatedAt:         now,
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
			return Result{}, err
		}
	}
	if err := s.store.Create(ctx, child); err != nil {
		return Result{}, err
	}
	// §8.2 / §16.1 line 27: observe the admitted child's depth onto
	// the `lenny_delegation_depth` histogram. Depth is invariant once
	// admitted, so sampling at admission and at session completion
	// produces the same distribution.
	childDepth := depth + 1
	if s.metrics != nil {
		s.metrics.ObserveDelegationDepth(req.PoolRef, childDepth)
	}
	// spec: §11.7 line 62 / §16.7 — emit `delegation.spawned` after the
	// child row is committed so audit consumers (billing, SIEM, compliance)
	// observe the same record the store now reflects. The detail carries
	// the §11.7 lineage attribution tuple. F-8.5.8.
	//
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
	if s.auditor != nil {
		s.auditor.EmitDelegationEvent(ctx, "delegation.spawned", map[string]any{
			"parent_session_id":       parent.ID,
			"child_session_id":        child.ID,
			"delegation_depth":        childDepth,
			"runtime_ref":             child.RuntimeRef,
			"pool_ref":                child.PoolRef,
			"isolation_profile":       string(child.IsolationProfile),
			"is_self_recursive":       decision.IsSelfRecursive,
			"approval_mode":           string(declaredMode),
			"effective_approval_mode": string(lease.EffectiveApprovalMode(req.ApprovalMode)),
		})
	}
	return Result{Child: child, Depth: childDepth}, nil
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
			"parent_session_id":           req.ParentSessionID,
			"runtime_ref":                 req.RuntimeRef,
			"pool_ref":                    req.PoolRef,
			"tenant_id":                   tenantID,
			"mode":                        string(d.EffectiveSettings.Mode),
			"platform_allow_self_rec":     d.EffectiveSettings.PlatformAllowSelfRec,
			"runtime_allow_self_rec":      d.EffectiveSettings.RuntimeAllowSelfRec,
			"policy_allow_self_rec":       d.EffectiveSettings.PolicyAllowSelfRec,
		})
	case cycle.OutcomeWouldHaveBlocked:
		s.auditor.EmitDelegationEvent(ctx, "delegation.cycle_warning", map[string]any{
			"parent_session_id":           req.ParentSessionID,
			"runtime_ref":                 req.RuntimeRef,
			"pool_ref":                    req.PoolRef,
			"tenant_id":                   tenantID,
			"mode":                        string(d.EffectiveSettings.Mode),
			"blocked_by":                  string(d.BlockedBy),
			"would_have_blocked_layers":   layersAsStrings(d.WouldHaveBlockedLayers),
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

// randomChildID returns a fresh §12.6 UUIDv8 session identifier for a
// delegated child session.
func randomChildID() string {
	return session.NewID()
}
