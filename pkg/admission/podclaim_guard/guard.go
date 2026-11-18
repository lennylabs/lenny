// SPDX-License-Identifier: MIT

// Package podclaim_guard implements the pure-decision logic of the
// lenny-podclaim-guard ValidatingAdmissionWebhook per spec §4.6.1
// (ADR-007). The webhook is deployed with failurePolicy: Fail and runs
// in front of every CREATE operation on PodClaim resources in agent
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
//	CREATE: reject when a non-terminal PodClaim already exists for
//	the same AgentPod (`.spec.agentPodRef`). The rule is per-pod
//	uniqueness with no concurrency exemption: a pool with
//	sessionPolicy.maxConcurrentSessions > 1 multiplexes its sessions
//	onto the single per-pod claim (§5.2), so a second non-terminal
//	claim for the same AgentPod is always a duplicate. Without this rule
//	the Postgres-fallback claim path could race with the normal
//	CRD-based claim path and produce duplicate claims.
//
// The guard reads no phase: it lists the PodClaim resources whose
// `.spec.agentPodRef` names the pod, and it reads neither
// AgentPod.status.phase nor PodClaim.status.phase. The deterministic claim-<podName> name
// means a duplicate CREATE under the canonical name also fails the API
// server's name-uniqueness check; the webhook check additionally covers
// a claim created under any other name (§4.6.1).
//
// The rejection returns the spec-mandated 403 Forbidden with the exact
// message string the §4.6.1 webhook spec calls out.
package podclaim_guard

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

// ClaimStatus is the PodClaim binding state owned by the gateway
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

// ExistingClaim describes an existing PodClaim that targets the
// same AgentPod as the incoming write. The webhook obtains the list
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

	// ClaimName is the name of the PodClaim being created. Used only
	// for the rejection message; the decision is not keyed on the claim
	// name.
	ClaimName string

	// AgentPodRef is the value of the PodClaim's
	// `.spec.agentPodRef` — the name of the AgentPod this claim
	// targets.
	AgentPodRef string

	// ExistingClaims is the set of PodClaims whose `.spec.agentPodRef`
	// equals the inbound write's AgentPodRef. Used to detect
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

// ErrMissingAgentPodRef is returned by Decide for any request that does
// not specify AgentPodRef. The webhook should treat this as a 400-class
// programming error before invoking the API server lookups.
var ErrMissingAgentPodRef = errors.New("podclaim_guard: AgentPodRef is required")

// Decide applies the §4.6.1 CREATE rule to the inbound admission
// request. Returns a non-nil error only on missing required input or an
// unsupported operation; the rule-based rejection comes back as
// Decision{Allowed: false, Reason: ...}.
//
// spec: §4.6.1 (podclaim-guard webhook) — CREATE-only per-pod
// uniqueness, no phase read, no concurrency exemption.
func Decide(r Request) (Decision, error) {
	if r.AgentPodRef == "" {
		return Decision{}, ErrMissingAgentPodRef
	}
	if r.Operation != OpCreate {
		return Decision{}, fmt.Errorf("podclaim_guard: unsupported operation %q", r.Operation)
	}
	// Per-pod uniqueness with no concurrency exemption: a pool with
	// maxConcurrentSessions > 1 multiplexes its sessions onto the single
	// per-pod claim (§5.2), so any non-terminal claim already present for
	// this AgentPod makes the inbound CREATE a duplicate. The deterministic
	// claim-<podName> name additionally subjects a same-name duplicate to
	// the API server's name-uniqueness check; this webhook check covers a
	// claim created under any other name.
	for _, ec := range r.ExistingClaims {
		if !ec.Status.IsTerminal() {
			return Decision{
				Allowed: false,
				Reason:  fmt.Sprintf("PodClaim already exists for AgentPod %s; concurrent claim rejected", r.AgentPodRef),
				Code:    403,
			}, nil
		}
	}
	return Decision{Allowed: true, Code: 200}, nil
}
