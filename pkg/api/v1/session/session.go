// SPDX-License-Identifier: MIT

// Package session encodes the externally-visible Session API surface
// from spec §15.1: the SessionState enum returned by GET /v1/sessions/{id},
// the FailureClass enum, and the state-mutating-endpoint precondition
// table that maps each POST endpoint to its valid precondition states
// and resulting transitions.
//
// The internal pod-state machine (spec §6.2) is intentionally NOT in
// this package — the REST surface returns only the externally visible
// states. Internal-only states (warming, idle, claimed, resuming,
// receiving_uploads, running_setup, sdk_connecting) live in
// pkg/sandbox/state and are never returned by the REST API.
//
// The PreconditionValidator function below mirrors the §15.1
// state-mutating-endpoint table verbatim: a call to an endpoint in an
// invalid precondition state returns the gateway's
// 409 INVALID_STATE_TRANSITION error with the spec-mandated details
// fields (currentState and allowedStates).
package session

import (
	"crypto/rand"
	"fmt"
	"sort"
)

// NewID returns a fresh §12.6 session identifier: a UUIDv8 (RFC 9562)
// whose first 32 bits are the shard routing prefix. A root session
// gets a fully random prefix; in a single-shard v1 deployment the
// prefix is not yet load-bearing, so child and derived sessions also
// receive a fresh random identifier here. The version nibble is 8 and
// the variant bits are 10, per the §12.6 bit layout.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x80
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// State is the externally-visible session state from §15.1. Twelve
// values; four are terminal.
type State string

const (
	StateCreated              State = "created"
	StateFinalizing           State = "finalizing"
	StateReady                State = "ready"
	StateStarting             State = "starting"
	StateRunning              State = "running"
	StateSuspended            State = "suspended"
	StateResumePending        State = "resume_pending"
	StateAwaitingClientAction State = "awaiting_client_action"
	StateCompleted            State = "completed"
	StateFailed               State = "failed"
	StateCancelled            State = "cancelled"
	StateExpired              State = "expired"
)

// AllStates returns every canonical state in §15.1 table order.
func AllStates() []State {
	return []State{
		StateCreated, StateFinalizing, StateReady, StateStarting,
		StateRunning, StateSuspended, StateResumePending,
		StateAwaitingClientAction, StateCompleted, StateFailed,
		StateCancelled, StateExpired,
	}
}

// TerminalStates returns the terminal subset.
func TerminalStates() []State {
	return []State{StateCompleted, StateFailed, StateCancelled, StateExpired}
}

// IsTerminal reports whether s is terminal per §15.1.
func IsTerminal(s State) bool {
	for _, t := range TerminalStates() {
		if s == t {
			return true
		}
	}
	return false
}

// FailureClass is the §7.1 enum that appears on `state == failed`
// session responses. nil when state != failed.
type FailureClass string

const (
	FailureClassRuntime              FailureClass = "runtime_failure"
	FailureClassStartingTimeout      FailureClass = "starting_timeout"
	FailureClassBudgetKeysExpired    FailureClass = "budget_keys_expired"
	FailureClassWorkspaceSealTimeout FailureClass = "workspace_seal_timeout"
	FailureClassDeriveFailure        FailureClass = "derive_failure"
)

// AllFailureClasses returns the closed enum in §7.1 table order.
func AllFailureClasses() []FailureClass {
	return []FailureClass{
		FailureClassRuntime, FailureClassStartingTimeout,
		FailureClassBudgetKeysExpired, FailureClassWorkspaceSealTimeout,
		FailureClassDeriveFailure,
	}
}

// Endpoint identifies a state-mutating REST endpoint by name. The
// strings match the §15.1 precondition table verbatim so log lines
// and error envelopes can use these constants without re-quoting.
type Endpoint string

const (
	EndpointUpload    Endpoint = "POST /v1/sessions/{id}/upload"
	EndpointFinalize  Endpoint = "POST /v1/sessions/{id}/finalize"
	EndpointStart     Endpoint = "POST /v1/sessions/{id}/start"
	EndpointInterrupt Endpoint = "POST /v1/sessions/{id}/interrupt"
	EndpointTerminate Endpoint = "POST /v1/sessions/{id}/terminate"
	EndpointResume    Endpoint = "POST /v1/sessions/{id}/resume"
	EndpointMessages  Endpoint = "POST /v1/sessions/{id}/messages"
	EndpointDerive    Endpoint = "POST /v1/sessions/{id}/derive"
	EndpointDelete    Endpoint = "DELETE /v1/sessions/{id}"
)

// preconditionTable mirrors the §15.1 precondition table verbatim.
// Endpoints whose entry depends on a runtime capability (upload's
// mid-session path requires `capabilities.midSessionUpload: true`) or
// a request flag (derive's `allowStale: true`) carry a CapabilityGate
// describing the lift; callers that hold the gated capability or flag
// pass the corresponding bit on the PreconditionRequest.
var preconditionTable = map[Endpoint]endpointRule{
	EndpointUpload: {
		baseStates: []State{StateCreated},
		gated: []gatedStates{
			{flag: CapabilityMidSessionUpload, states: []State{StateRunning}},
		},
	},
	EndpointFinalize: {baseStates: []State{StateCreated}},
	EndpointStart:    {baseStates: []State{StateReady}},
	EndpointInterrupt: {
		baseStates: []State{StateRunning},
	},
	EndpointTerminate: {
		baseStates: []State{
			StateCreated, StateFinalizing, StateReady, StateStarting,
			StateRunning, StateSuspended, StateResumePending,
			StateAwaitingClientAction,
		},
	},
	EndpointResume: {baseStates: []State{StateAwaitingClientAction}},
	EndpointMessages: {
		// §15.1 says "any non-terminal state". The terminal-state
		// rejection is left to AllowedStates / Validate; the rule
		// here is "every non-terminal state".
		baseStates: nonTerminalStates(),
	},
	EndpointDerive: {
		baseStates: []State{StateCompleted, StateFailed, StateCancelled, StateExpired},
		gated: []gatedStates{
			{flag: CapabilityAllowStaleDerive, states: []State{StateRunning, StateSuspended, StateResumePending, StateAwaitingClientAction}},
		},
	},
	EndpointDelete: {
		baseStates: nonTerminalStates(),
	},
}

// Capability is a request-level flag or runtime capability that lifts
// an endpoint's precondition gate. Capabilities are surfaced on the
// PreconditionRequest.Capabilities set; absent capabilities mean the
// base state set applies.
type Capability string

const (
	// CapabilityMidSessionUpload lifts the upload precondition to
	// admit StateRunning. Set by the gateway when the runtime declares
	// `capabilities.midSessionUpload: true` AND the deployer policy
	// allows mid-session upload.
	CapabilityMidSessionUpload Capability = "midSessionUpload"

	// CapabilityAllowStaleDerive lifts the derive precondition to admit
	// non-terminal source-session states. Set by the gateway when the
	// caller passes `allowStale: true` in the derive request body.
	CapabilityAllowStaleDerive Capability = "allowStaleDerive"
)

type gatedStates struct {
	flag   Capability
	states []State
}

type endpointRule struct {
	baseStates []State
	gated      []gatedStates
}

// PreconditionRequest is the input to Validate. Endpoint is required;
// CurrentState is the current session state; Capabilities lifts the
// applicable gates.
type PreconditionRequest struct {
	Endpoint     Endpoint
	CurrentState State
	Capabilities map[Capability]bool
}

// PreconditionError is returned when the precondition check fails. It
// mirrors the §15.1 error envelope: HTTP 409 INVALID_STATE_TRANSITION
// with details.currentState and details.allowedStates.
type PreconditionError struct {
	Endpoint      Endpoint
	CurrentState  State
	AllowedStates []State
}

func (e *PreconditionError) Error() string {
	return fmt.Sprintf("session: %s rejected: state is %q, allowed states %v", e.Endpoint, e.CurrentState, e.AllowedStates)
}

// Code returns the HTTP status code the gateway should surface for
// this error per §15.1.
func (e *PreconditionError) Code() int { return 409 }

// ErrorCode returns the spec-mandated error code string for the §15.1
// error envelope.
func (e *PreconditionError) ErrorCode() string { return "INVALID_STATE_TRANSITION" }

// Validate applies the §15.1 precondition rule for r.Endpoint. Returns
// nil on success and *PreconditionError on rejection. The error carries
// the AllowedStates slice the gateway echoes back in
// details.allowedStates.
func Validate(r PreconditionRequest) error {
	rule, ok := preconditionTable[r.Endpoint]
	if !ok {
		return fmt.Errorf("session: unknown state-mutating endpoint %q", r.Endpoint)
	}
	allowed := append([]State{}, rule.baseStates...)
	for _, g := range rule.gated {
		if r.Capabilities[g.flag] {
			allowed = append(allowed, g.states...)
		}
	}
	for _, s := range allowed {
		if s == r.CurrentState {
			return nil
		}
	}
	sort.Slice(allowed, func(i, j int) bool { return allowed[i] < allowed[j] })
	return &PreconditionError{
		Endpoint:      r.Endpoint,
		CurrentState:  r.CurrentState,
		AllowedStates: allowed,
	}
}

// AllowedStates returns the set of valid precondition states for an
// endpoint given a set of capabilities. Useful for the
// details.allowedStates field of the §15.1 error envelope.
func AllowedStates(endpoint Endpoint, capabilities map[Capability]bool) []State {
	rule, ok := preconditionTable[endpoint]
	if !ok {
		return nil
	}
	out := append([]State{}, rule.baseStates...)
	for _, g := range rule.gated {
		if capabilities[g.flag] {
			out = append(out, g.states...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// nonTerminalStates returns every state from AllStates() that is not
// terminal. Computed eagerly so the precondition table can read it as
// a literal slice at init.
func nonTerminalStates() []State {
	out := make([]State, 0, len(AllStates()))
	for _, s := range AllStates() {
		if !IsTerminal(s) {
			out = append(out, s)
		}
	}
	return out
}
