// SPDX-License-Identifier: MIT

package llmproxy

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/credential"
)

// CodeFallbackExhausted is the §4.9 terminal error returned to the agent
// pod when the credentialPolicy fallback chain is exhausted (category
// POLICY). spec: spec/04_system-components.md line 1394.
const CodeFallbackExhausted = "CREDENTIAL_FALLBACK_EXHAUSTED"

// FallbackRotator mints a replacement §4.9 credential lease from the
// fallback chain's next pool and pushes it to the session's pod via the
// §4.7 RotateCredentials RPC (Fallback Flow steps 5-7). Implementations
// are nil-safe at the call site; a nil rotator skips the replacement
// push.
type FallbackRotator interface {
	// Rotate issues a replacement lease for the faulted lease's provider
	// from nextPool and pushes it to the session's pod. trigger is the
	// fault rotation trigger recorded in the rotation context. The
	// faulted lease carries the session id, tenant, and provider the
	// replacement is minted and attributed under.
	Rotate(faulted credential.Lease, nextPool string, trigger credential.RotationTrigger)
}

// FallbackExhaustedEvent carries the §4.9.2 credential.fallback_exhausted
// audit fields. spec: spec/04_system-components.md lines 1393-1396.
type FallbackExhaustedEvent struct {
	TenantID          string
	SessionID         string
	RotationCount     int
	LastFailureReason string
	ChainAttempted    []string
}

// FallbackAuditSink emits the §4.9.2 credential.fallback_exhausted audit
// event.
type FallbackAuditSink interface {
	FallbackExhausted(FallbackExhaustedEvent)
}

// FallbackTerminator terminates a session whose fallback chain is
// exhausted (Fallback Flow step 3 — "The session is terminated with
// CREDENTIAL_FALLBACK_EXHAUSTED").
type FallbackTerminator interface {
	TerminateSession(sessionID, reason string)
}

// FallbackMetrics receives the §16.1 fallback counters.
type FallbackMetrics interface {
	// IncCredentialRotation counts a fault-driven credential rotation by
	// error type (lenny_credential_rotation_total).
	IncCredentialRotation(errorType string)
	// IncCredentialFallbackExhausted counts a chain exhaustion
	// (lenny_gateway_credential_fallback_exhausted_total), labeled by
	// pool, provider, and error type.
	IncCredentialFallbackExhausted(pool, provider, errorType string)
}

// faultTrigger maps a §4.9 translator error type to the Fallback Flow
// rotation trigger it represents. ok is false for translator errors
// that are not upstream credential faults (request-shape errors,
// timeouts, mid-stream interruptions): those do not drive fallback.
//
// spec: spec/04_system-components.md lines 1383-1384 — fallback fires on
// RATE_LIMITED, AUTH_EXPIRED, and PROVIDER_UNAVAILABLE.
func faultTrigger(t ErrorType) (credential.RotationTrigger, bool) {
	switch t {
	case ErrAuthFailed:
		// The translator folds 401/403 (AUTH_EXPIRED) and 429
		// (RATE_LIMITED) into auth_failed; both are auth-layer faults
		// that walk the same per-provider chain.
		return credential.TriggerFaultAuthExpired, true
	case ErrUpstream5xx:
		return credential.TriggerFaultProviderUnavailable, true
	default:
		return "", false
	}
}

// driveFallback runs the §4.9 Fallback Flow for one observed upstream
// credential fault. It records the faulted pool's cooldown and the
// session's rotation budget through the Controller, then either rotates
// to the chain's next pool (returning false so the caller surfaces the
// upstream error and the pod retries with the rotated credential) or
// declares the chain exhausted (writing the terminal
// CREDENTIAL_FALLBACK_EXHAUSTED error and returning true).
//
// spec: spec/04_system-components.md lines 1383-1411.
func (h *Handler) driveFallback(w http.ResponseWriter, lease credential.Lease, trigger credential.RotationTrigger, errorType string) (exhausted bool) {
	if h.Fallback == nil {
		return false
	}
	dec := h.Fallback.Fault(lease.SessionID, lease.Provider, lease.PoolID, trigger)
	if dec.Exhausted {
		if h.FallbackMetrics != nil {
			h.FallbackMetrics.IncCredentialFallbackExhausted(lease.PoolID, string(lease.Provider), errorType)
		}
		if h.FallbackAudit != nil {
			h.FallbackAudit.FallbackExhausted(FallbackExhaustedEvent{
				TenantID:          lease.TenantID,
				SessionID:         lease.SessionID,
				RotationCount:     dec.RotationCount,
				LastFailureReason: errorType,
				ChainAttempted:    dec.ChainAttempted,
			})
		}
		if h.FallbackTerminator != nil {
			h.FallbackTerminator.TerminateSession(lease.SessionID, CodeFallbackExhausted)
		}
		// The session is terminal: drop its fallback state so the
		// per-session rotation map does not retain the entry.
		h.Fallback.Release(lease.SessionID)
		h.writeError(w, http.StatusForbidden, CodeFallbackExhausted,
			"the credential fallback chain is exhausted for this session")
		return true
	}
	// Fallback Flow steps 5-7: a replacement pool is available; mint and
	// push it. The pod retries the request against the rotated lease.
	if h.FallbackMetrics != nil {
		h.FallbackMetrics.IncCredentialRotation(errorType)
	}
	if h.FallbackRotator != nil {
		h.FallbackRotator.Rotate(lease, dec.NextPool, trigger)
	}
	return false
}
