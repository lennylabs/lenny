// SPDX-License-Identifier: MIT

// Package drain_readiness implements the pure-decision logic of the
// lenny-drain-readiness ValidatingAdmissionWebhook per spec §12.5 and
// §13.2. The webhook intercepts CREATE on the pods/eviction subresource
// in agent namespaces and blocks a planned node drain while the
// artifact store is degraded, so a drain cannot evict agent pods into a
// MinIO outage and lose un-checkpointed workspace state.
//
// Before admitting an eviction the webhook queries the gateway's
// §12.5 GET /internal/drain-readiness endpoint, which runs a MinIO
// liveness probe. This package turns that probe result, together with
// the evicted pod's node drain-force override, into the admission
// decision:
//
//	(i)   the node carries lenny.dev/drain-force: "true" — the eviction
//	      is admitted as a forced drain, and the webhook emits a
//	      node.drain.forced critical audit event.
//	(ii)  the MinIO probe reports healthy — the eviction is admitted.
//	(iii) the MinIO probe reports unhealthy or could not be reached —
//	      the eviction is rejected fail-closed.
//
// The decision logic is split from the webhook HTTP/AdmissionReview
// transport so it can be unit-tested without the controller-runtime
// stack or a live gateway endpoint.
package drain_readiness

// RejectionMessage is the §12.5 message returned on a blocked drain.
const RejectionMessage = "Node drain blocked: MinIO health check failed — defer drain until MinIO is healthy (STR-006)"

// MinIOStatus is the result of the §12.5 pre-drain MinIO health probe
// the gateway runs at GET /internal/drain-readiness.
type MinIOStatus string

const (
	// MinIOHealthy reports that the probe returned 200: the artifact
	// store answered a HeadBucket within the timeout.
	MinIOHealthy MinIOStatus = "healthy"
	// MinIOUnhealthy reports that the probe returned 503: the artifact
	// store is reachable but degraded.
	MinIOUnhealthy MinIOStatus = "unhealthy"
	// MinIOUnreachable reports that the gateway drain-readiness
	// endpoint itself could not be reached.
	MinIOUnreachable MinIOStatus = "unreachable"
)

// Request is the input to Decide.
type Request struct {
	// DrainForced reports whether the evicted pod's node carries the
	// lenny.dev/drain-force: "true" emergency-drain override.
	DrainForced bool
	// MinIO is the result of the §12.5 pre-drain MinIO health probe.
	MinIO MinIOStatus
}

// Decision is the admission outcome.
type Decision struct {
	// Allowed is true when the eviction may proceed.
	Allowed bool
	// Forced is true when the eviction was admitted only because the
	// node carried the drain-force override. The webhook emits a
	// node.drain.forced critical audit event when Forced is true.
	Forced bool
	// Reason is the rejection message relayed to the evicting client
	// when Allowed is false; empty when Allowed is true.
	Reason string
	// Code is the HTTP status the webhook surfaces: 200 on allow, 403
	// on rejection.
	Code int
}

// Decide applies the §12.5 pre-drain rules. The drain-force override
// admits the eviction ahead of the health check; otherwise a healthy
// MinIO probe admits it and any other probe result rejects it
// fail-closed.
func Decide(r Request) Decision {
	if r.DrainForced {
		return Decision{Allowed: true, Forced: true, Code: 200}
	}
	if r.MinIO == MinIOHealthy {
		return Decision{Allowed: true, Code: 200}
	}
	return Decision{Allowed: false, Code: 403, Reason: RejectionMessage}
}
