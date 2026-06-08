// SPDX-License-Identifier: MIT

// Package sandboxclaim_guard implements the pure-decision logic of the
// lenny-sandboxclaim-guard ValidatingAdmissionWebhook per spec §4.6.1
// (ADR-007). The webhook is deployed with failurePolicy: Fail and runs
// in front of every CREATE, PATCH, and PUT operation on SandboxClaim
// resources in agent namespaces.
//
// The decision logic is split from the webhook HTTP/JSON-AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack. The webhook binary (a later phase) wraps this package with a
// k8s.io/api/admission/v1 handler.
//
// Two rules from §4.6.1:
//
//  1. CREATE: reject when a non-terminal SandboxClaim already exists
//     for the same Sandbox (`.spec.sandboxRef`). Without this rule the
//     Postgres-fallback claim path could race with the normal
//     CRD-based claim path and produce duplicate claims.
//
//  2. PATCH/PUT: read the referenced Sandbox.status.phase via
//     `.spec.sandboxRef` and reject when the pod no longer holds an
//     active claim — that is, when it has been released back to the pool
//     (`idle`/`draining`) or reached a terminal state. A write while the
//     Sandbox is still in a claim-bound, session-serving phase
//     (`claimed` and the §6.2 setup→attached chain, per §4.6.3 line 591)
//     is a legitimate mutation, not a stale write. This stops a stale
//     write from a failed-over gateway/controller mutating a
//     SandboxClaim whose underlying pod is no longer bound to it.
//
// A SandboxClaim that is itself being deleted (a non-zero
// metadata.deletionTimestamp) is exempt from rule 2. Removing a
// finalizer to release a stuck deletion is a teardown write, not a
// claim mutation, and cannot produce a duplicate claim. Without the
// exemption a claim whose Sandbox has already terminated could never
// be collected.
//
// Both rejections return the spec-mandated 403 Forbidden with the
// exact message strings the §4.6.1 webhook spec calls out.
package sandboxclaim_guard

import (
	"errors"
	"fmt"
)

// Operation discriminates the admission operation. The webhook is
// installed on CREATE, PATCH, and PUT only; DELETE flows are not
// subject to this guard.
type Operation string

const (
	OpCreate Operation = "CREATE"
	OpPatch  Operation = "PATCH"
	OpPut    Operation = "PUT"
)

// SandboxPhase mirrors the spec §6.2 enum just enough for this guard to
// decide. The full canonical list lives in pkg/sandbox/state.
type SandboxPhase string

const (
	PhaseWarming             SandboxPhase = "warming"
	PhaseSDKConnecting       SandboxPhase = "sdk_connecting"
	PhaseIdle                SandboxPhase = "idle"
	PhaseClaimed             SandboxPhase = "claimed"
	PhaseSlotActive          SandboxPhase = "slot_active"
	PhaseReceivingUploads    SandboxPhase = "receiving_uploads"
	PhaseFinalizingWorkspace SandboxPhase = "finalizing_workspace"
	PhaseRunningSetup        SandboxPhase = "running_setup"
	PhaseStartingSession     SandboxPhase = "starting_session"
	PhaseAttached            SandboxPhase = "attached"
	PhaseDraining            SandboxPhase = "draining"
	PhaseTerminated          SandboxPhase = "terminated"
	PhaseFailed              SandboxPhase = "failed"
)

// claimBoundPhases is the set of Sandbox phases in which a `bound`
// SandboxClaim's pod still holds an active claim to it, so a PATCH/PUT to
// that claim is a legitimate mutation rather than a stale write. Spec
// §4.6.3 line 591 states that a `bound` claim coexists with a
// `Sandbox.status.phase` of `claimed` "or has progressed past `claimed`
// into session-serving states such as `receiving_uploads`, `attached`,
// `running`" — the §6.2 lines 83-94 claim→setup→attached chain
// (`claimed → receiving_uploads → finalizing_workspace → running_setup →
// starting_session → attached`). The §4.6.1 PATCH/PUT stale-write rule
// (spec line 384) rejects every other phase: the pod has been released
// back to the pool (`idle`/`draining`) or has reached a terminal state
// (`terminated`/`failed`), so it no longer holds an active claim to this
// SandboxClaim. The §5.2 concurrent-mode `slot_active` phase is bound as
// well — the pod is actively serving slots — mirroring the CREATE-rule
// `slot_active` exemption. An unrecognized or future phase is rejected
// (fail-closed), matching the webhook's failurePolicy: Fail posture.
var claimBoundPhases = map[SandboxPhase]bool{
	PhaseClaimed:             true,
	PhaseReceivingUploads:    true,
	PhaseFinalizingWorkspace: true,
	PhaseRunningSetup:        true,
	PhaseStartingSession:     true,
	PhaseAttached:            true,
	PhaseSlotActive:          true,
}

// ClaimStatus is the SandboxClaim binding state owned by the gateway
// (§4.6.3 enumeration). Used by the CREATE rule to determine whether
// an existing claim is terminal.
type ClaimStatus string

const (
	ClaimBound    ClaimStatus = "bound"
	ClaimActive   ClaimStatus = "active"
	ClaimReleased ClaimStatus = "released"
	ClaimFailed   ClaimStatus = "failed"
)

// IsTerminal reports whether a ClaimStatus is terminal per §4.6.3.
func (c ClaimStatus) IsTerminal() bool {
	return c == ClaimReleased || c == ClaimFailed
}

// ExistingClaim describes an existing SandboxClaim that targets the
// same Sandbox as the incoming write. The webhook obtains the list
// from the API server during admission.
type ExistingClaim struct {
	Name   string
	Status ClaimStatus
}

// Request is the input to Decide. It carries everything the webhook
// reads to decide the admission outcome.
type Request struct {
	// Operation is CREATE, PATCH, or PUT.
	Operation Operation

	// ClaimName is the name of the SandboxClaim being created or
	// modified. Used only for the rejection message; the decision is
	// not keyed on the claim name.
	ClaimName string

	// SandboxRef is the value of the SandboxClaim's
	// `.spec.sandboxRef` — the name of the Sandbox this claim
	// targets.
	SandboxRef string

	// SandboxPhase is the current `.status.phase` of the referenced
	// Sandbox. Read from the API server by the webhook before calling
	// Decide. Required on PATCH and PUT; on CREATE, used to skip the
	// "concurrent claim rejected" rule when the Sandbox is in the §5.2
	// `slot_active` phase (concurrent mode actively serving multiple
	// slots). Ignored when UnderDeletion is set.
	SandboxPhase SandboxPhase

	// HasSlotID is true when the inbound SandboxClaim carries a
	// non-empty `.spec.slotId`. A non-empty SlotID is the §5.2
	// concurrent-mode marker on the claim object itself, independent
	// of the referenced Sandbox's status.phase. Used on CREATE to
	// skip the duplicate-claim rule: concurrent-mode dispatch opens
	// multiple non-terminal claims against the same Sandbox by
	// design, and the maxConcurrent cap is enforced upstream by the
	// gateway's Redis Lua slot counter rather than the webhook.
	// The phase-based PhaseSlotActive exemption stays valid for the
	// second-and-later claims after the first slot pushed the phase
	// over to slot_active; HasSlotID covers the first-claim window
	// where the SSA status patch has not yet landed.
	HasSlotID bool

	// UnderDeletion is true when the SandboxClaim being modified carries
	// a non-zero metadata.deletionTimestamp. A claim under deletion is
	// exempt from the PATCH/PUT staleness rule so its finalizers can be
	// cleared. Ignored on CREATE.
	UnderDeletion bool

	// ExistingClaims is the set of SandboxClaims whose `.spec.sandboxRef`
	// equals the inbound write's SandboxRef. Used only on CREATE to
	// detect duplicate-claim races.
	ExistingClaims []ExistingClaim
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when the request passes the guard.
	Allowed bool

	// Reason is the spec-mandated 403 message body when Allowed is
	// false; empty when Allowed is true.
	Reason string

	// Code mirrors the HTTP status code the webhook surfaces back to
	// the API server. 200 on allow, 403 on rejection per §4.6.1.
	Code int
}

// ErrMissingSandboxRef is returned by Decide for any request that does
// not specify SandboxRef. The webhook should treat this as a 400-class
// programming error before invoking the API server lookups.
var ErrMissingSandboxRef = errors.New("sandboxclaim_guard: SandboxRef is required")

// Decide applies the §4.6.1 rules to the inbound admission request.
// Returns a non-nil error only on missing required input; rule-based
// rejections come back as Decision{Allowed: false, Reason: ...}.
func Decide(r Request) (Decision, error) {
	if r.SandboxRef == "" {
		return Decision{}, ErrMissingSandboxRef
	}
	switch r.Operation {
	case OpCreate:
		// §5.2 concurrent-mode: a Sandbox hosting up to MaxConcurrent
		// simultaneous slot claims expects multiple non-terminal
		// claims at once. Two signals identify a concurrent-mode
		// claim:
		//
		//   - HasSlotID (the claim's own `.spec.slotId`). This is
		//     authoritative on the very first slot, where the
		//     Sandbox.status.phase has not yet transitioned from
		//     `idle` to `slot_active` because the SSA mirror patch
		//     lands after the claim CREATE.
		//   - SandboxPhase == PhaseSlotActive. Covers the path where
		//     the inbound claim happens to omit SlotID (defensive),
		//     and stays valid for the second-and-later claims after
		//     the first reservation pushed the pod to slot_active.
		//
		// The maxConcurrent cap is enforced upstream by the gateway's
		// Redis Lua slot counter (§5.2 atomic GET-compare-INCR), so
		// the webhook does not need to count slots itself. The
		// deterministic claim name (claim-<session-id>) prevents the
		// duplicate-session race at the API server's CREATE conflict
		// path. For session-mode Sandboxes (HasSlotID=false and
		// phase `idle`/`claimed`) the duplicate-claim rule remains
		// in force.
		if r.HasSlotID || r.SandboxPhase == PhaseSlotActive {
			return Decision{Allowed: true, Code: 200}, nil
		}
		for _, ec := range r.ExistingClaims {
			if !ec.Status.IsTerminal() {
				return Decision{
					Allowed: false,
					Reason:  fmt.Sprintf("SandboxClaim already exists for Sandbox %s; concurrent claim rejected", r.SandboxRef),
					Code:    403,
				}, nil
			}
		}
		return Decision{Allowed: true, Code: 200}, nil
	case OpPatch, OpPut:
		// A SandboxClaim under deletion is exempt: a finalizer-removal
		// or other teardown write must complete so a claim whose
		// Sandbox has already terminated can be collected.
		if r.UnderDeletion {
			return Decision{Allowed: true, Code: 200}, nil
		}
		// Accept a PATCH/PUT while the referenced Sandbox is still in a
		// claim-bound, session-serving phase (spec §4.6.3 line 591). The
		// §6.2 lines 83-94 setup chain advances a claimed pod through
		// receiving_uploads/finalizing_workspace/running_setup/
		// starting_session before attached; a write during that chain is
		// not a stale write because the pod still holds this claim. Only a
		// pod that has been released to the pool or reached a terminal
		// state ("not claimed" per spec line 384) triggers the
		// stale-write rejection.
		if !claimBoundPhases[r.SandboxPhase] {
			return Decision{
				Allowed: false,
				Reason:  fmt.Sprintf("SandboxClaim stale: referenced Sandbox %s is in phase %s, not claimed; concurrent write rejected", r.SandboxRef, r.SandboxPhase),
				Code:    403,
			}, nil
		}
		return Decision{Allowed: true, Code: 200}, nil
	default:
		return Decision{}, fmt.Errorf("sandboxclaim_guard: unsupported operation %q", r.Operation)
	}
}
