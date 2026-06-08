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
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// NewID returns a fresh §12.6 session identifier: a UUIDv8 (RFC 9562)
// whose first 32 bits are the shard routing prefix. A root session gets a
// fully random prefix (the prefix the whole delegation tree inherits via
// NewChildID). Derived sessions are new independent roots, so they also
// use NewID. The version nibble is 8 and the variant bits are 10, per the
// §12.6 bit layout.
//
// spec: §12.6 line 576 — root session creation generates a UUIDv8 with a
// fully random routing prefix.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x80
	b[8] = (b[8] & 0x3f) | 0x80
	return formatUUID(b)
}

// NewChildID returns a §12.6 child session identifier whose first 32 bits
// (the shard routing prefix) are copied from rootID and whose remaining
// bits are random. This guarantees every session in a delegation tree
// shares the same routing prefix and lands on the same shard, so the
// single-shard tree traversal, cascade cancellation, and delegation
// lineage queries the spec relies on hold the moment a multi-shard router
// is deployed. rootID must be a session id produced by NewID or
// NewChildID; a malformed routing prefix returns an error.
//
// spec: §12.6 line 577 — "the gateway copies the first 32 bits from the
// root session's UUID into the child's UUID. The remaining bits are
// random."
func NewChildID(rootID string) (string, error) {
	prefix, err := routingPrefix(rootID)
	if err != nil {
		return "", err
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	copy(b[0:4], prefix[:])
	b[6] = (b[6] & 0x0f) | 0x80
	b[8] = (b[8] & 0x3f) | 0x80
	return formatUUID(b), nil
}

// RoutingPrefix returns the §12.6 shard routing prefix of a session id:
// the first 32 bits, rendered as the leading 8 hex characters. Two ids
// with the same RoutingPrefix consistent-hash to the same session shard,
// so a delegation tree co-locates. It returns an error for an id whose
// leading group is not 8 hex characters.
func RoutingPrefix(id string) (string, error) {
	p, err := routingPrefix(id)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(p[:]), nil
}

// routingPrefix extracts the first 32 bits (4 bytes) of a §12.6 session id
// — the hyphen-delimited leading group of a UUIDv8.
func routingPrefix(id string) ([4]byte, error) {
	var p [4]byte
	if strings.IndexByte(id, '-') != 8 {
		return p, fmt.Errorf("session: %q is not a routable §12.6 session id", id)
	}
	if _, err := hex.Decode(p[:], []byte(id[:8])); err != nil {
		return p, fmt.Errorf("session: %q has a malformed routing prefix: %w", id, err)
	}
	return p, nil
}

// formatUUID renders the 16-byte array in the canonical 8-4-4-4-12 form.
func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// State is the externally-visible session state from §15.1. Four of the
// listed values are terminal. StateInputRequired is the §7.2 sub-state of
// StateRunning observed when a runtime calls lenny/request_input; the
// gateway emits it on the GET /v1/sessions/{id} surface and the
// status_change SSE stream so clients can see the agent is blocked. The
// session state machine (pkg/session/state) is the canonical source of
// valid transitions; this package mirrors the §15.1 REST projection.
type State string

const (
	StateCreated              State = "created"
	StateFinalizing           State = "finalizing"
	StateReady                State = "ready"
	StateStarting             State = "starting"
	StateRunning              State = "running"
	StateInputRequired        State = "input_required" // §7.2 sub-state of Running
	StateSuspended            State = "suspended"
	StateResumePending        State = "resume_pending"
	StateAwaitingClientAction State = "awaiting_client_action"
	StateCompleted            State = "completed"
	StateFailed               State = "failed"
	StateCancelled            State = "cancelled"
	StateExpired              State = "expired"

	// StateResuming is the §7.2 line 195 internal transient between
	// resume_pending and running. §15.1 line 621 normalises the GET
	// /v1/sessions/{id} envelope's `state` field to `resume_pending` →
	// `running` so the resuming value is never returned from a polling
	// read. It does, however, appear on the wire in two cases:
	//   - the §10.4 coordinator-handoff reattach synthesises a
	//     `status_change(resuming)` SSE frame so a reattaching client
	//     observes the transient on the event stream, and
	//   - the §6.2 pod-state-machine `resuming` failure transitions
	//     surface it on internal traces.
	// The constant lives in the §15.1 surface so the SSE emitter can
	// reference it without depending on the internal pkg/session/state
	// package; it is intentionally NOT a member of AllStates() because
	// the §15.1 polling envelope never returns it.
	// spec: §7.2 line 195; §15.1 line 621; §10.4 coordinator-handoff
	// reattach synthesis.
	StateResuming State = "resuming"
)

// AllStates returns every canonical state in §15.1 table order.
func AllStates() []State {
	return []State{
		StateCreated, StateFinalizing, StateReady, StateStarting,
		StateRunning, StateInputRequired, StateSuspended, StateResumePending,
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

// RecoveryStates returns the retry/recovery subset: states in which a
// session is not actively serving because it is waiting on a resume
// (resume_pending), mid-resume (resuming), or stalled awaiting a client
// action (awaiting_client_action). The §16.5 Session availability SLO is
// the "uptime of sessions not in retry/recovery state", so these states
// are the unavailable fraction the SessionAvailabilityBurnRate alert
// reads. spec: §16.5 line 616, §6.2 lines 246-254, §7.3.
func RecoveryStates() []State {
	return []State{StateResumePending, StateResuming, StateAwaitingClientAction}
}

// IsRecovery reports whether s is a retry/recovery state per §16.5
// line 616.
func IsRecovery(s State) bool {
	for _, r := range RecoveryStates() {
		if s == r {
			return true
		}
	}
	return false
}

// CascadePolicy is the §8.10 cascadeOnFailure policy: it governs the
// fate of a session's children when the session reaches a terminal
// state. The name is historical — it applies on every terminal
// transition, not only failure.
type CascadePolicy string

const (
	// CascadeCancelAll cancels every descendant immediately. It is the
	// §8.10 default.
	CascadeCancelAll CascadePolicy = "cancel_all"
	// CascadeAwaitCompletion lets running children finish, bounded by
	// cascadeTimeoutSeconds.
	CascadeAwaitCompletion CascadePolicy = "await_completion"
	// CascadeDetach lets children outlive the terminated parent.
	CascadeDetach CascadePolicy = "detach"
)

// DefaultCascadePolicy is the §8.10 policy applied when a session names
// no cascadeOnFailure value.
const DefaultCascadePolicy = CascadeCancelAll

// AllCascadePolicies returns the closed §8.10 enum.
func AllCascadePolicies() []CascadePolicy {
	return []CascadePolicy{CascadeCancelAll, CascadeAwaitCompletion, CascadeDetach}
}

// IsValid reports whether p is a known cascade policy.
func (p CascadePolicy) IsValid() bool {
	for _, v := range AllCascadePolicies() {
		if p == v {
			return true
		}
	}
	return false
}

// Resolve returns p when it is a valid policy, and the §8.10 default
// otherwise — so an unset or unrecognized value resolves to cancel_all.
func (p CascadePolicy) Resolve() CascadePolicy {
	if p.IsValid() {
		return p
	}
	return DefaultCascadePolicy
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
		// §7.2 line 178 — input_required is a sub-state of running where
		// the agent is blocked in lenny/request_input; an interrupt must
		// still suspend the underlying session, so the precondition
		// admits both running and input_required.
		baseStates: []State{StateRunning, StateInputRequired},
	},
	EndpointTerminate: {
		// §7.2 line 178 input_required → cancelled requires terminate to
		// accept the sub-state too.
		baseStates: []State{
			StateCreated, StateFinalizing, StateReady, StateStarting,
			StateRunning, StateInputRequired, StateSuspended, StateResumePending,
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
		// spec: §7.2 line 197 — `resuming → cancelled` via DELETE is
		// the canonical mid-resume cancel edge. StateResuming is
		// excluded from AllStates() (per §15.1 line 621 collapse rule)
		// so nonTerminalStates() does not pick it up; admit it
		// explicitly so a DELETE landing during the internal
		// `resuming` transient drives the §7.2 line 214 snapshot-close
		// fence. F-7.1.14.
		baseStates: append(nonTerminalStates(), StateResuming),
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
