// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// signingMetrics holds the §10.2 / §4.3 KMS-signing and Token-Service circuit-breaker state.
type signingMetrics struct {
	// kmsSigningErrors counts §10.2 JWTSigner failures. The
	// §16.5 KMSSigningUnavailable alert keys on
	// `rate(lenny_gateway_kms_signing_errors_total[30s]) > 1`. The
	// `reason` label discriminates `inner` (the KMS returned an error
	// other than throttling) from `rejected` (breaker open); throttled
	// sign requests are not counted. spec: §10.2, §16.1. F-10.2.6.
	kmsSigningErrors *prometheus.CounterVec
	// jwtSignRequests counts every §10.2 sign request by outcome, with
	// `component="gateway"`. The §16.5 KMSSignQuotaApproaching alert
	// reads its non-`circuit_open` rate. spec: §16.1 (JWT sign requests).
	jwtSignRequests *prometheus.CounterVec
	// kmsSignQuota mirrors the configured provider asymmetric Sign quota
	// (`kms.signQuotaPerSecond`); KMSSignQuotaApproaching reads it via
	// scalar(...). Zero means unset. spec: §16.5, §17.8.2.
	kmsSignQuota prometheus.Gauge
	// kmsSigningCircuitState is the §10.2 JWTSigner breaker
	// gauge: 0=closed, 1=half-open, 2=open. F-10.2.6.
	kmsSigningCircuitState prometheus.Gauge
	// tokenServiceCircuitState reflects the §4.3 / §4.1 Token Service
	// per-subsystem circuit-breaker state (0 closed, 1 half-open, 2
	// open). The §16.5 TokenServiceUnavailable alert reads it via
	// `lenny_token_service_circuit_state == 2`. spec: §4.3.
	tokenServiceCircuitState prometheus.Gauge
}

// newSigningMetrics constructs, registers, and materializes the signing metric subsystem
// against reg. spec: §16 observability metrics.
func newSigningMetrics(reg *prometheus.Registry) (signingMetrics, error) {
	var m signingMetrics
	// §4.3 / §16.5 TokenServiceUnavailable alert reads this
	// gauge. 0 = closed, 1 = half-open, 2 = open. spec: §4.3.
	tokenServiceCircuitState, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_token_service_circuit_state",
		Help: "§4.3 Token Service circuit breaker state: 0=closed, 1=half-open, 2=open.",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §10.2 / §16.5 KMSSigningUnavailable alert reads
	// rate(lenny_gateway_kms_signing_errors_total[30s]) > 1. F-10.2.6.
	kmsSigningErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_kms_signing_errors_total",
		Help: "§10.2 JWTSigner signing failures on the gateway and Token Service signers, throttling excluded. Labels: reason ∈ {inner, rejected}.",
	}, []string{"reason"})
	if err != nil {
		return m, err
	}
	jwtSignRequests, err := newJWTSignRequestsCounter()
	if err != nil {
		return m, err
	}
	kmsSignQuota, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_kms_sign_quota_per_second",
		Help: "§16.5 configured KMS asymmetric Sign quota per second (kms.signQuotaPerSecond), read by KMSSignQuotaApproaching; 0 means unset.",
	}, nil)
	if err != nil {
		return m, err
	}
	kmsSigningCircuitState, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_gateway_kms_signing_circuit_state",
		Help: "§10.2 JWTSigner circuit breaker state: 0=closed, 1=half-open, 2=open.",
	}, nil)
	if err != nil {
		return m, err
	}
	reg.MustRegister(tokenServiceCircuitState,
		kmsSigningErrors,
		jwtSignRequests,
		kmsSignQuota,
		kmsSigningCircuitState)
	tokenServiceCircuitChild := tokenServiceCircuitState.WithLabelValues()
	kmsSigningCircuitChild := kmsSigningCircuitState.WithLabelValues()
	m.kmsSigningErrors = kmsSigningErrors
	m.jwtSignRequests = jwtSignRequests
	m.kmsSignQuota = kmsSignQuota.WithLabelValues()
	m.kmsSigningCircuitState = kmsSigningCircuitChild
	m.tokenServiceCircuitState = tokenServiceCircuitChild
	return m, nil
}

// newJWTSignRequestsCounter builds the §16.1
// `lenny_jwt_sign_requests_total{component,outcome}` counter.
func newJWTSignRequestsCounter() (*prometheus.CounterVec, error) {
	return metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_jwt_sign_requests_total",
		Help: "§16.1 JWT sign requests by signer component and outcome (ok, throttled, circuit_open, error).",
	}, []string{"component", "outcome"})
}

// RecordJWTSignRequest increments
// `lenny_jwt_sign_requests_total{component="gateway",outcome}` once per
// gateway sign request. outcome is one of the jwt.SignOutcome* values
// the breaker reports through OnOutcome.
//
// spec: §16.1 (JWT sign requests); §10.2.
func (m *Metrics) RecordJWTSignRequest(outcome string) {
	if m == nil {
		return
	}
	m.jwtSignRequests.WithLabelValues(jwt.SignComponentGateway, outcome).Inc()
}

// SetKMSSignQuotaPerSecond emits the configured provider Sign quota on
// the `lenny_kms_sign_quota_per_second` gauge at startup, as
// SetStreamCeiling emits the stream ceiling. The §16.5
// KMSSignQuotaApproaching alert reads it via scalar(...) and stays
// silent while it is zero.
//
// spec: §16.5 (KMSSignQuotaApproaching); §17.8.2 "KMS Sign rate".
func (m *Metrics) SetKMSSignQuotaPerSecond(value int) {
	if m == nil {
		return
	}
	m.kmsSignQuota.Set(float64(value))
}
