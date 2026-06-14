// SPDX-License-Identifier: MIT

// Package sandboxclaim_guard implements the pure-decision logic of the
// lenny-sandboxclaim-guard ValidatingAdmissionWebhook per spec §4.6.1
// (ADR-007). The webhook is deployed with failurePolicy: Fail and runs
// in front of every CREATE operation on SandboxClaim resources in agent
// namespaces. PATCH and PUT are admitted without inspection and are not
// registered with the webhook.
//
// The decision logic is split from the webhook HTTP/JSON-AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack. The webhook binary wraps this package with a
// k8s.io/api/admission/v1 handler.
//
// The single rule, per §4.6.1:
//
//	CREATE: reject when a non-terminal SandboxClaim already exists for
//	the same Sandbox (`.spec.sandboxRef`). The rule is per-pod
//	uniqueness with no concurrency exemption: a pool with
//	sessionPolicy.maxConcurrentSessions > 1 multiplexes its sessions
//	onto the single per-pod claim (§5.2), so a second non-terminal
//	claim for the same Sandbox is always a duplicate. Without this rule
//	the Postgres-fallback claim path could race with the normal
//	CRD-based claim path and produce duplicate claims.
//
// The guard reads no phase: it queries existing SandboxClaim resources
// by `.spec.sandboxRef` and does not read Sandbox.status.phase or
// SandboxClaim.status.phase. The deterministic claim-<podName> name
// means a duplicate CREATE under the canonical name also fails the API
// server's name-uniqueness check; the webhook check additionally covers
// a claim created under any other name (§4.6.1).
//
// The rejection returns the spec-mandated 403 Forbidden with the exact
// message string the §4.6.1 webhook spec calls out.
package sandboxclaim_guard

import (
	"errors"
	"fmt"
)

// Operation discriminates the admission operation. The webhook is
// installed on CREATE only; PATCH, PUT, and DELETE flows are not subject
// to this guard.
type Operation string

const (
	OpCreate Operation = "CREATE"
)

// ClaimStatus is the SandboxClaim binding state owned by the gateway
// (§4.6.3 enumeration). Used by the CREATE rule to determine whether
// an existing claim is terminal.
type ClaimStatus string

const (
	ClaimBound     ClaimStatus = "bound"
	ClaimRecycling ClaimStatus = "recycling"
	ClaimReserved  ClaimStatus = "reserved"
	ClaimReleased  ClaimStatus = "released"
	ClaimFailed    ClaimStatus = "failed"
)

// IsTerminal reports whether a ClaimStatus is terminal per §4.6.3. Only
// `released` and `failed` are terminal; `bound`, `recycling`, and
// `reserved` are live binding states.
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
	// Operation is CREATE. The webhook is installed on CREATE only.
	Operation Operation

	// ClaimName is the name of the SandboxClaim being created. Used only
	// for the rejection message; the decision is not keyed on the claim
	// name.
	ClaimName string

	// SandboxRef is the value of the SandboxClaim's
	// `.spec.sandboxRef` — the name of the Sandbox this claim
	// targets.
	SandboxRef string

	// ExistingClaims is the set of SandboxClaims whose `.spec.sandboxRef`
	// equals the inbound write's SandboxRef. Used to detect
	// duplicate-claim races.
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

// Decide applies the §4.6.1 CREATE rule to the inbound admission
// request. Returns a non-nil error only on missing required input or an
// unsupported operation; the rule-based rejection comes back as
// Decision{Allowed: false, Reason: ...}.
//
// spec: §4.6.1 (sandboxclaim-guard webhook) — CREATE-only per-pod
// uniqueness, no phase read, no concurrency exemption.
func Decide(r Request) (Decision, error) {
	if r.SandboxRef == "" {
		return Decision{}, ErrMissingSandboxRef
	}
	if r.Operation != OpCreate {
		return Decision{}, fmt.Errorf("sandboxclaim_guard: unsupported operation %q", r.Operation)
	}
	// Per-pod uniqueness with no concurrency exemption: a pool with
	// maxConcurrentSessions > 1 multiplexes its sessions onto the single
	// per-pod claim (§5.2), so any non-terminal claim already present for
	// this Sandbox makes the inbound CREATE a duplicate. The deterministic
	// claim-<podName> name additionally subjects a same-name duplicate to
	// the API server's name-uniqueness check; this webhook check covers a
	// claim created under any other name.
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
}
