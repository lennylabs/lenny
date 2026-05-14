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
//     `.spec.sandboxRef` and reject when it is not "claimed". A stale
//     write from a failed-over gateway/controller cannot mutate a
//     SandboxClaim whose underlying pod has already moved past the
//     claimed phase.
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
	PhaseReceivingUploads    SandboxPhase = "receiving_uploads"
	PhaseFinalizingWorkspace SandboxPhase = "finalizing_workspace"
	PhaseRunningSetup        SandboxPhase = "running_setup"
	PhaseAttached            SandboxPhase = "attached"
	PhaseDraining            SandboxPhase = "draining"
	PhaseTerminated          SandboxPhase = "terminated"
	PhaseFailed              SandboxPhase = "failed"
)

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
	// Decide. Required on PATCH and PUT; ignored on CREATE.
	SandboxPhase SandboxPhase

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
		if r.SandboxPhase != PhaseClaimed {
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
