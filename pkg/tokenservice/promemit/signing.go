// SPDX-License-Identifier: MIT

package promemit

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// Reasons of the §16.1 KMS signing errors counter: `inner` for an inner
// error the breaker counted as a failure, and `rejected` for a call the
// open breaker refused.
const (
	signingErrorReasonInner    = "inner"
	signingErrorReasonRejected = "rejected"
)

// signingCounters holds the §16.1 signing counters the Token Service
// signer feeds through its JWTSigner breaker observer.
type signingCounters struct {
	// signRequests is lenny_jwt_sign_requests_total{component,outcome};
	// the Token Service stamps component="token-service".
	signRequests *prometheus.CounterVec
	// signingErrors is lenny_gateway_kms_signing_errors_total{reason}.
	// The name carries the gateway prefix because the §16.1 row names
	// one counter emitted by both signers; the scrape `job` label
	// identifies the process.
	signingErrors *prometheus.CounterVec
}

// newSigningCounters builds the §16.1 signing counters.
//
// spec: §16.1 (KMS signing errors, JWT sign requests).
func newSigningCounters() (signingCounters, error) {
	var c signingCounters
	signRequests, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_jwt_sign_requests_total",
		Help: "§16.1 JWT sign requests by signer component and outcome (ok, throttled, circuit_open, error).",
	}, []string{"component", "outcome"})
	if err != nil {
		return c, err
	}
	signingErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_kms_signing_errors_total",
		Help: "§10.2 JWTSigner signing failures on the gateway and Token Service signers, throttling excluded. Labels: reason ∈ {inner, rejected}.",
	}, []string{"reason"})
	if err != nil {
		return c, err
	}
	c.signRequests = signRequests
	c.signingErrors = signingErrors
	return c, nil
}

// SigningObserver returns the jwt.BreakerObserver the Token Service
// signer reports through. It counts every sign request by outcome, an
// inner failure the breaker counted as `reason="inner"`, and a breaker
// refusal as `reason="rejected"`, with the same semantics as the
// gateway's observer, so throttled requests never reach the errors
// counter. The circuit transitions are not mirrored on a gauge: the
// §16.1 circuit-state gauge is the gateway's.
//
// spec: §16.1 (KMS signing errors, JWT sign requests); §10.2 "KMS
// signing failure mode".
func (e *Emitter) SigningObserver() jwt.BreakerObserver {
	return signingObserver{c: e.signing}
}

// signingObserver adapts signingCounters to jwt.BreakerObserver.
type signingObserver struct {
	c signingCounters
}

// OnSigningFailure implements jwt.BreakerObserver.
func (o signingObserver) OnSigningFailure() {
	o.c.signingErrors.WithLabelValues(signingErrorReasonInner).Inc()
}

// OnRejected implements jwt.BreakerObserver.
func (o signingObserver) OnRejected() {
	o.c.signingErrors.WithLabelValues(signingErrorReasonRejected).Inc()
}

// OnOutcome implements jwt.BreakerObserver.
func (o signingObserver) OnOutcome(outcome string) {
	o.c.signRequests.WithLabelValues(jwt.SignComponentTokenService, outcome).Inc()
}

// OnCircuitOpen implements jwt.BreakerObserver.
func (signingObserver) OnCircuitOpen() {}

// OnCircuitClosed implements jwt.BreakerObserver.
func (signingObserver) OnCircuitClosed() {}
