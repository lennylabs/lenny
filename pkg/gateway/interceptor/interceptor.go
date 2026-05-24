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
// interceptor declares a non-positive timeout.
const DefaultTimeout = 500 * time.Millisecond

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
	content := req.Content
	modified := false
	for _, ic := range c.ordered(req.Phase) {
		call := req
		call.Content = content
		res, err := invoke(ctx, ic, call)
		if err != nil {
			if ic.FailPolicy() == FailOpen {
				continue
			}
			return Result{
				Action:          ActionReject,
				Code:            CodeInterceptorTimeout,
				Reason:          fmt.Sprintf("interceptor %q failed and is fail-closed: %v", ic.Name(), err),
				ModifiedContent: content,
				RejectedBy:      ic.Name(),
				TimeoutMs:       effectiveTimeout(ic).Milliseconds(),
			}
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
			content = res.ModifiedContent
			modified = true
		case ActionAllow:
			// pass the content through unchanged
		}
	}
	if modified {
		return Result{Action: ActionModify, ModifiedContent: content}
	}
	return Result{Action: ActionAllow, ModifiedContent: content}
}

// effectiveTimeout returns the per-interceptor deadline, selecting
// DefaultTimeout when the interceptor declares a non-positive value.
func effectiveTimeout(ic Interceptor) time.Duration {
	if t := ic.Timeout(); t > 0 {
		return t
	}
	return DefaultTimeout
}

// invoke runs one interceptor under its per-interceptor deadline.
func invoke(ctx context.Context, ic Interceptor, req Request) (Result, error) {
	cctx, cancel := context.WithTimeout(ctx, effectiveTimeout(ic))
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
