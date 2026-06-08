// SPDX-License-Identifier: MIT

// Package interceptor is the §4 RequestInterceptor extension point: a
// per-phase chain of policy hooks the gateway runs around a request.
//
// Each gateway phase (PreDelegation, PreMessageDelivery, and the rest)
// runs its own independent chain. Within a phase, interceptors execute
// in ascending priority order; at an equal priority a built-in
// interceptor runs before an external one, and registration order
// breaks any remaining tie. An interceptor returns ALLOW, REJECT, or
// MODIFY: a MODIFY rewrites the content for the interceptors that
// follow, and the first REJECT short-circuits the chain. An
// interceptor error or timeout is resolved by that interceptor's
// FailPolicy.
//
// This package is the transport-agnostic core. A built-in interceptor
// is a Go value implementing Interceptor; an external interceptor is
// an adapter over the §4 gRPC RequestInterceptor service that also
// implements Interceptor.
package interceptor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Phase names a gateway interception point. Each phase runs its own
// independent chain.
type Phase string

// The §4 interception phases.
const (
	PhasePreAuth                  Phase = "PreAuth"
	PhasePostAuth                 Phase = "PostAuth"
	PhasePreRoute                 Phase = "PreRoute"
	PhasePreDelegation            Phase = "PreDelegation"
	PhasePreExportMaterialization Phase = "PreExportMaterialization"
	PhasePreMessageDelivery       Phase = "PreMessageDelivery"
	PhasePostRoute                Phase = "PostRoute"
	PhasePreToolResult            Phase = "PreToolResult"
	PhasePostAgentOutput          Phase = "PostAgentOutput"
	PhasePreLLMRequest            Phase = "PreLLMRequest"
	PhasePostLLMResponse          Phase = "PostLLMResponse"
	PhasePreConnectorRequest      Phase = "PreConnectorRequest"
	PhasePostConnectorResponse    Phase = "PostConnectorResponse"
)

// AllPhases returns every interception phase.
func AllPhases() []Phase {
	return []Phase{
		PhasePreAuth, PhasePostAuth, PhasePreRoute, PhasePreDelegation,
		PhasePreExportMaterialization, PhasePreMessageDelivery, PhasePostRoute,
		PhasePreToolResult, PhasePostAgentOutput, PhasePreLLMRequest,
		PhasePostLLMResponse, PhasePreConnectorRequest, PhasePostConnectorResponse,
	}
}

// IsValid reports whether p is a known phase.
func (p Phase) IsValid() bool {
	for _, v := range AllPhases() {
		if p == v {
			return true
		}
	}
	return false
}

// Action is an interceptor's decision on a request.
type Action int

// The §4 interceptor actions. The values match the gRPC
// RequestInterceptor.InterceptResponse.Action enum.
const (
	// ActionAllow passes the request through unchanged.
	ActionAllow Action = iota
	// ActionReject blocks the request and short-circuits the chain.
	ActionReject
	// ActionModify rewrites the content for the interceptors that
	// follow.
	ActionModify
)

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "ALLOW"
	case ActionReject:
		return "REJECT"
	case ActionModify:
		return "MODIFY"
	default:
		return fmt.Sprintf("Action(%d)", int(a))
	}
}

// FailPolicy governs what a chain does when an interceptor errors or
// times out.
type FailPolicy string

const (
	// FailClosed treats an interceptor error or timeout as a REJECT.
	// It is the default so a misbehaving interceptor cannot silently
	// bypass policy.
	FailClosed FailPolicy = "fail-closed"
	// FailOpen treats an interceptor error or timeout as an ALLOW.
	FailOpen FailPolicy = "fail-open"
)

// ReservedPriorityCeiling is the highest priority reserved for
// built-in security-critical interceptors. An external interceptor
// must register above it.
const ReservedPriorityCeiling = 100

// DefaultTimeout is the per-interceptor deadline applied when an
// interceptor declares a non-positive timeout at a phase with no tighter
// phase-specific default.
const DefaultTimeout = 500 * time.Millisecond

// DefaultLLMTimeout is the per-interceptor deadline for the LLM proxy
// phases (PreLLMRequest, PostLLMResponse). These run on the LLM call
// hot path where every millisecond of interceptor latency adds directly
// to request latency, so the default is tighter than DefaultTimeout
// (spec: §4.8 line 1075).
const DefaultLLMTimeout = 100 * time.Millisecond

// DefaultConnectorTimeout is the per-interceptor deadline for the
// connector proxy phases (PreConnectorRequest, PostConnectorResponse).
// The tool-call path is latency-sensitive but less extreme than
// streaming LLM calls (spec: §4.8 line 1077).
const DefaultConnectorTimeout = 200 * time.Millisecond

// phaseDefaultTimeout returns the per-phase default per-interceptor
// deadline selected when an interceptor declares a non-positive
// Timeout(). The LLM and connector phases carry tighter budgets; every
// other phase uses DefaultTimeout (spec: §4.8 lines 1075, 1077).
func phaseDefaultTimeout(phase Phase) time.Duration {
	switch phase {
	case PhasePreLLMRequest, PhasePostLLMResponse:
		return DefaultLLMTimeout
	case PhasePreConnectorRequest, PhasePostConnectorResponse:
		return DefaultConnectorTimeout
	default:
		return DefaultTimeout
	}
}

// CodeInterceptorTimeout is the §15.1 error code a fail-closed chain
// returns when an interceptor errors or times out.
const CodeInterceptorTimeout = "INTERCEPTOR_TIMEOUT"

// Request is the payload handed to an interceptor. Content is the
// phase-specific payload; an interceptor that returns ActionModify
// supplies a rewritten Content for the rest of the chain.
type Request struct {
	Phase     Phase
	SessionID string
	TenantID  string
	Content   []byte
	Metadata  map[string]string
}

// Result is an interceptor's decision. On ActionReject, Reason is the
// human-readable cause and Code is the §15.1 error code (empty for a
// deliberate policy rejection, CodeInterceptorTimeout for a
// fail-closed error). ModifiedContent carries the rewritten payload on
// ActionModify and the payload as it stood at rejection on
// ActionReject.
//
// RejectedBy and TimeoutMs are set by Chain.Run so the §16.7
// interceptor.rejected audit row identifies the actual rejecting
// interceptor rather than assuming a fixed built-in (spec: §4.8 line
// 981, §4.8 line 1032). RejectedBy is the Name() of the interceptor
// that returned ActionReject or failed closed. TimeoutMs is the
// interceptor's effective per-call deadline in milliseconds, set only
// on the fail-closed timeout/error path (Code == CodeInterceptorTimeout)
// so the §15.1 INTERCEPTOR_TIMEOUT envelope and audit row can report
// `timeout_ms`.
type Result struct {
	Action          Action
	Reason          string
	Code            string
	ModifiedContent []byte
	RejectedBy      string
	TimeoutMs       int64

	// FailOpenSkips lists the interceptors the chain skipped on this run
	// because they errored or timed out under a fail-open FailPolicy
	// without crossing the §4.8 line 1030 escalation ceiling. It is
	// populated only on the ALLOW/MODIFY return: a fail-open skip that is
	// followed by a REJECT does not admit the request, so the skip is
	// moot on that path and is not reported. The §8.7
	// PreExportMaterialization caller reads this to emit
	// delegation.export_scan_failed_open and the
	// lenny_export_file_scans_total{outcome="failed_open"} metric, which
	// it cannot otherwise tell apart from a clean ALLOW. spec: §11.7
	// line 70.
	FailOpenSkips []FailOpenSkip
}

// FailOpenSkip records one interceptor the chain skipped under its
// fail-open FailPolicy after an error or timeout. Reason is the §11.7
// line 122 classified cause: "timeout" for a context deadline,
// otherwise "grpc_error". The transport-agnostic core reports the two
// cases it can observe; the external-interceptor adapter is the layer
// that can further distinguish "unreachable" from a call-level
// "grpc_error".
type FailOpenSkip struct {
	// Interceptor is the skipped interceptor's Name().
	Interceptor string
	// Reason is the §11.7 line 122 token: "timeout" or "grpc_error".
	Reason string
}

// Interceptor is one policy hook. A built-in interceptor reports
// Builtin() == true and may register at any priority and phase; an
// external interceptor must use a priority above ReservedPriorityCeiling
// and may not register for PhasePreAuth.
type Interceptor interface {
	// Name identifies the interceptor in audit records and errors.
	Name() string
	// Priority orders execution within a phase; lower runs first.
	Priority() int32
	// Builtin reports whether this is a built-in (priority ≤ 100 and
	// PhasePreAuth permitted) rather than an external interceptor.
	Builtin() bool
	// FailPolicy governs the chain's response to an error or timeout.
	FailPolicy() FailPolicy
	// Timeout is the per-interceptor deadline. A non-positive value
	// selects DefaultTimeout.
	Timeout() time.Duration
	// Intercept evaluates the request.
	Intercept(ctx context.Context, req Request) (Result, error)
}

// Registration errors.
var (
	// ErrInvalidPriority — an external interceptor registered at a
	// priority reserved for built-ins. Maps to INVALID_INTERCEPTOR_PRIORITY.
	ErrInvalidPriority = errors.New("interceptor: external interceptor priority must exceed the reserved ceiling")
	// ErrInvalidPhase — an external interceptor targeted PhasePreAuth.
	// Maps to INVALID_INTERCEPTOR_PHASE.
	ErrInvalidPhase = errors.New("interceptor: external interceptors may not register for the PreAuth phase")
	// ErrUnknownPhase — registration named a phase that does not exist.
	ErrUnknownPhase = errors.New("interceptor: unknown phase")
)

// §15.1 error codes a rejected external-interceptor registration maps
// to. The gateway's interceptor-registration handler translates the
// Register sentinel errors to these codes with HTTP 400.
const (
	// CodeInvalidInterceptorPriority is the §4.8 line 1021 code for an
	// external interceptor registered at a priority within the reserved
	// built-in ceiling.
	CodeInvalidInterceptorPriority = "INVALID_INTERCEPTOR_PRIORITY"
	// CodeInvalidInterceptorPhase is the §4.8 line 1023 code for an
	// external interceptor that targets the PreAuth phase.
	CodeInvalidInterceptorPhase = "INVALID_INTERCEPTOR_PHASE"
)

// RegistrationErrorCode maps a Register error to its §15.1 error code
// and HTTP status. It returns ok == false for an error that is not a
// registration-validation sentinel (the caller then falls back to a
// generic 400/500). spec: §4.8 lines 1021, 1023.
func RegistrationErrorCode(err error) (code string, httpStatus int, ok bool) {
	switch {
	case errors.Is(err, ErrInvalidPriority):
		return CodeInvalidInterceptorPriority, 400, true
	case errors.Is(err, ErrInvalidPhase):
		return CodeInvalidInterceptorPhase, 400, true
	default:
		return "", 0, false
	}
}

// entry pairs an interceptor with its registration index so an
// equal-priority, equal-builtin tie resolves to registration order.
type entry struct {
	ic    Interceptor
	order int
}

// Chain holds the per-phase interceptor registry. The zero value is
// not usable; construct with NewChain.
type Chain struct {
	byPhase map[Phase][]entry
	count   int

	// §4.8 line 1030 cumulative fail-open escalation state. Configured
	// via SetFailOpenEscalation; the zero value disables escalation
	// (a fail-open error is always skipped). failMu guards failStates,
	// the escalation config, and the observer so Run is concurrency-safe.
	failMu         sync.Mutex
	failEnabled    bool
	failOpenMax    int
	failOpenWindow time.Duration
	failClock      func() time.Time
	failObserver   FailOpenObserver
	failStates     map[string]*failOpenState
}

// NewChain returns an empty Chain.
func NewChain() *Chain {
	return &Chain{byPhase: map[Phase][]entry{}}
}

// Register adds ic to the chain for phase. An external interceptor
// (Builtin() == false) must use a priority above ReservedPriorityCeiling
// and may not target PhasePreAuth; violations return ErrInvalidPriority
// or ErrInvalidPhase. An unknown phase returns ErrUnknownPhase.
func (c *Chain) Register(phase Phase, ic Interceptor) error {
	if !phase.IsValid() {
		return fmt.Errorf("%w: %q", ErrUnknownPhase, phase)
	}
	if !ic.Builtin() {
		if ic.Priority() <= ReservedPriorityCeiling {
			return fmt.Errorf("%w: %q has priority %d", ErrInvalidPriority, ic.Name(), ic.Priority())
		}
		if phase == PhasePreAuth {
			return fmt.Errorf("%w: %q", ErrInvalidPhase, ic.Name())
		}
	}
	c.byPhase[phase] = append(c.byPhase[phase], entry{ic: ic, order: c.count})
	c.count++
	return nil
}

// Len returns the number of interceptors registered for phase.
func (c *Chain) Len(phase Phase) int { return len(c.byPhase[phase]) }

// LenRange returns the number of interceptors registered for phase whose
// priority falls in the half-open window [minPriority, maxPriority). It
// lets a caller short-circuit a RunRange that would select no
// interceptor (spec: §4.8 line 12 priority ordering).
func (c *Chain) LenRange(phase Phase, minPriority, maxPriority int32) int {
	n := 0
	for _, e := range c.byPhase[phase] {
		if p := e.ic.Priority(); p >= minPriority && p < maxPriority {
			n++
		}
	}
	return n
}

// Run executes the chain for req.Phase and returns the chain's final
// decision. Interceptors run in ascending priority order, built-ins
// before external interceptors at an equal priority, registration
// order breaking any remaining tie. A MODIFY rewrites the content for
// the interceptors that follow; the first REJECT short-circuits the
// chain and returns its result with the payload as it stood at
// rejection. An interceptor error or timeout is resolved by that
// interceptor's FailPolicy: fail-closed returns a REJECT carrying
// CodeInterceptorTimeout, fail-open skips the interceptor. An empty
// chain returns ActionAllow.
func (c *Chain) Run(ctx context.Context, req Request) Result {
	return c.run(ctx, req, nil)
}

// RunRange executes the chain for req.Phase over only the interceptors
// whose priority falls in the half-open window [minPriority,
// maxPriority), with the same ordering, MODIFY, short-circuit, and
// fail-policy semantics as Run. It is the building block for splitting a
// single phase chain around an in-process built-in that sits at a fixed
// priority: the §4.8 ExperimentRouter is a PreRoute built-in at priority
// 300 (spec: §4.8 line 115), so the gateway runs the external PreRoute
// interceptors below 300 before it and those at or above 300 after it,
// preserving the §4.8 line-12 ascending-priority order across the
// built-in. An external interceptor registered at exactly the pivot
// priority sorts after the equal-priority built-in (spec: §4.8 line 12,
// "built-in interceptors run before external ones"), so the at-or-above
// window includes the pivot value itself.
func (c *Chain) RunRange(ctx context.Context, req Request, minPriority, maxPriority int32) Result {
	return c.run(ctx, req, func(ic Interceptor) bool {
		p := ic.Priority()
		return p >= minPriority && p < maxPriority
	})
}

// HasExternalNamed reports whether an external interceptor named ref is
// registered for phase. The §8.3 contentPolicy.interceptorRef names one
// such external content scanner; RunPolicyScoped uses this to fail closed
// when a DelegationPolicy references an interceptor that is not
// registered in this gateway process rather than silently running no
// content scan. spec: §8.3 line 183, §4.8 line 1036.
func (c *Chain) HasExternalNamed(phase Phase, ref string) bool {
	if ref == "" {
		return false
	}
	for _, e := range c.byPhase[phase] {
		if !e.ic.Builtin() && e.ic.Name() == ref {
			return true
		}
	}
	return false
}

// RunPolicyScoped executes req.Phase with external interceptors scoped to
// the §8.3 contentPolicy.interceptorRef. Every built-in interceptor runs
// — so the PreDelegation DelegationPolicyEvaluator maxInputSize cap is
// always enforced — while an external interceptor runs only when
// interceptorRef names it. External interceptors the effective policy
// does not name are skipped, so a registered content scanner fires only
// for the delegations and messages whose policy selects it, and a policy
// with interceptorRef: null runs no external content scan. This realizes
// the §4.8 line 1036/1040 model in which PreDelegation and
// PreMessageDelivery are the hook points for contentPolicy.interceptorRef
// rather than phases at which every registered external interceptor runs
// unconditionally.
//
// When interceptorRef is non-empty but no external interceptor of that
// name is registered for the phase, the call fails closed with
// CodeInterceptorTimeout: a configured content scanner the gateway cannot
// reach is treated as a degraded interceptor (§4.8 line 1032) rather than
// a silent bypass. spec: §8.3 lines 157-188; §4.8 lines 1032, 1036, 1040;
// §13.5 mitigations 2-3.
func (c *Chain) RunPolicyScoped(ctx context.Context, req Request, interceptorRef string) Result {
	if interceptorRef != "" && !c.HasExternalNamed(req.Phase, interceptorRef) {
		return Result{
			Action:          ActionReject,
			Code:            CodeInterceptorTimeout,
			Reason:          fmt.Sprintf("contentPolicy.interceptorRef %q is not a registered %s interceptor", interceptorRef, req.Phase),
			ModifiedContent: req.Content,
			RejectedBy:      interceptorRef,
		}
	}
	return c.run(ctx, req, func(ic Interceptor) bool {
		if ic.Builtin() {
			return true
		}
		return interceptorRef != "" && ic.Name() == interceptorRef
	})
}

// run executes the phase chain, optionally restricted to the
// interceptors that accept reports true for. A nil accept runs every
// registered interceptor.
func (c *Chain) run(ctx context.Context, req Request, accept func(Interceptor) bool) Result {
	content := req.Content
	modified := false
	var failOpenSkips []FailOpenSkip
	for _, ic := range c.ordered(req.Phase) {
		if accept != nil && !accept(ic) {
			continue
		}
		call := req
		call.Content = content
		res, err := invoke(ctx, ic, call)
		if err != nil {
			if ic.FailPolicy() == FailOpen {
				// spec: §4.8 line 1030 — track fail-open errors in a
				// rolling window; once the interceptor crosses the
				// escalation ceiling it is auto-promoted to fail-closed so
				// a persistently broken content filter cannot bypass
				// policy indefinitely.
				if c.recordFailOpenError(ctx, ic, call) {
					return Result{
						Action:          ActionReject,
						Code:            CodeInterceptorTimeout,
						Reason:          fmt.Sprintf("interceptor %q failed and was auto-escalated to fail-closed after exceeding the fail-open error ceiling: %v", ic.Name(), err),
						ModifiedContent: content,
						RejectedBy:      ic.Name(),
						TimeoutMs:       effectiveTimeout(ic, req.Phase).Milliseconds(),
					}
				}
				// spec: §11.7 line 70 — the interceptor was skipped under
				// its fail-open policy; record it so the export-scan caller
				// can emit delegation.export_scan_failed_open for an admitted
				// file rather than mistaking the skip for a clean ALLOW.
				failOpenSkips = append(failOpenSkips, FailOpenSkip{
					Interceptor: ic.Name(),
					Reason:      classifyFailOpenReason(err),
				})
				continue
			}
			return Result{
				Action:          ActionReject,
				Code:            CodeInterceptorTimeout,
				Reason:          fmt.Sprintf("interceptor %q failed and is fail-closed: %v", ic.Name(), err),
				ModifiedContent: content,
				RejectedBy:      ic.Name(),
				TimeoutMs:       effectiveTimeout(ic, req.Phase).Milliseconds(),
			}
		}
		// spec: §4.8 line 1030 — an error-free call by a fail-open
		// interceptor that had been escalated restores it to fail-open.
		if ic.FailPolicy() == FailOpen {
			c.recordFailOpenSuccess(ctx, ic, call)
		}
		switch res.Action {
		case ActionReject:
			if res.ModifiedContent == nil {
				res.ModifiedContent = content
			}
			// spec: §4.8 line 981 — the audit row identifies the
			// rejecting interceptor. A built-in may leave RejectedBy
			// empty; stamp the chain's view of which interceptor rejected.
			if res.RejectedBy == "" {
				res.RejectedBy = ic.Name()
			}
			return res
		case ActionModify:
			// spec: §4.8 line 1060 — validate the MODIFY against the
			// phase's immutable fields before applying it. A MODIFY that
			// alters an identity/scope/routing field is rejected with
			// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION, the chain
			// short-circuits, and the pre-modification payload is preserved
			// so no subsequent interceptor sees the illegal modification.
			if violations := checkModifyImmutability(req.Phase, content, res.ModifiedContent); len(violations) > 0 {
				return Result{
					Action:          ActionReject,
					Code:            CodeInterceptorImmutableFieldViolation,
					Reason:          fmt.Sprintf("interceptor %q MODIFY altered immutable %s field(s): %s", ic.Name(), req.Phase, strings.Join(violations, ", ")),
					ModifiedContent: content,
					RejectedBy:      ic.Name(),
				}
			}
			content = res.ModifiedContent
			modified = true
		case ActionAllow:
			// pass the content through unchanged
		}
	}
	if modified {
		return Result{Action: ActionModify, ModifiedContent: content, FailOpenSkips: failOpenSkips}
	}
	return Result{Action: ActionAllow, ModifiedContent: content, FailOpenSkips: failOpenSkips}
}

// classifyFailOpenReason maps an interceptor error to the §11.7 line 122
// delegation.export_scan_failed_open `reason` token. A context deadline
// is reported as "timeout"; every other error is reported as
// "grpc_error". The transport-agnostic core cannot distinguish a dial
// failure ("unreachable") from a call-level error, so it reports the
// coarser "grpc_error"; the external-interceptor adapter refines that
// when it can classify the transport failure.
func classifyFailOpenReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "grpc_error"
}

// effectiveTimeout returns the per-interceptor deadline, selecting the
// phase-specific default when the interceptor declares a non-positive
// value (spec: §4.8 lines 1075, 1077).
func effectiveTimeout(ic Interceptor, phase Phase) time.Duration {
	if t := ic.Timeout(); t > 0 {
		return t
	}
	return phaseDefaultTimeout(phase)
}

// invoke runs one interceptor under its per-interceptor deadline.
func invoke(ctx context.Context, ic Interceptor, req Request) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, effectiveTimeout(ic, req.Phase))
	defer cancel()
	return ic.Intercept(cctx, req)
}

// ordered returns the phase's interceptors in execution order.
func (c *Chain) ordered(phase Phase) []Interceptor {
	entries := append([]entry(nil), c.byPhase[phase]...)
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.ic.Priority() != b.ic.Priority() {
			return a.ic.Priority() < b.ic.Priority()
		}
		if a.ic.Builtin() != b.ic.Builtin() {
			return a.ic.Builtin()
		}
		return a.order < b.order
	})
	out := make([]Interceptor, len(entries))
	for i, e := range entries {
		out[i] = e.ic
	}
	return out
}
