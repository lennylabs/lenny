// SPDX-License-Identifier: MIT

package gatewaymetrics

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// signingMetrics holds the §10.2 / §4.3 KMS-signing and Token-Service circuit-breaker state.
type signingMetrics struct {
	// kmsSigningErrors counts §10.2 line 225 JWTSigner failures. The
	// §16.5 KMSSigningUnavailable alert keys on
	// `rate(lenny_gateway_kms_signing_errors_total[30s]) > 1`. The
	// `reason` label discriminates `inner` (KMS surfaced an error) from
	// `rejected` (breaker open). spec: §10.2 line 225. F-10.2.6.
	kmsSigningErrors *prometheus.CounterVec
	// kmsSigningCircuitState is the §10.2 line 225 JWTSigner breaker
	// gauge: 0=closed, 1=half-open, 2=open. F-10.2.6.
	kmsSigningCircuitState prometheus.Gauge
	// tokenServiceCircuitState reflects the §4.3 / §4.1 Token Service
	// per-subsystem circuit-breaker state (0 closed, 1 half-open, 2
	// open). The §16.5 TokenServiceUnavailable alert reads it via
	// `lenny_token_service_circuit_state == 2`. spec: §4.3 line 211.
	tokenServiceCircuitState prometheus.Gauge
}

// newSigningMetrics constructs, registers, and materializes the signing metric subsystem
// against reg. spec: §16 observability metrics.
func newSigningMetrics(reg *prometheus.Registry) (signingMetrics, error) {
	var m signingMetrics
	// §4.3 line 211 / §16.5 TokenServiceUnavailable alert reads this
	// gauge. 0 = closed, 1 = half-open, 2 = open. spec: §4.3 line 211.
	tokenServiceCircuitState, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_token_service_circuit_state",
		Help: "§4.3 Token Service circuit breaker state: 0=closed, 1=half-open, 2=open.",
	}, nil)
	if err != nil {
		return m, err
	}
	// spec: §10.2 line 225 / §16.5 KMSSigningUnavailable alert reads
	// rate(lenny_gateway_kms_signing_errors_total[30s]) > 1. F-10.2.6.
	kmsSigningErrors, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_gateway_kms_signing_errors_total",
		Help: "§10.2 JWTSigner signing failures. Labels: reason ∈ {inner, rejected}.",
	}, []string{"reason"})
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
		kmsSigningCircuitState)
	tokenServiceCircuitChild := tokenServiceCircuitState.WithLabelValues()
	kmsSigningCircuitChild := kmsSigningCircuitState.WithLabelValues()
	m.kmsSigningErrors = kmsSigningErrors
	m.kmsSigningCircuitState = kmsSigningCircuitChild
	m.tokenServiceCircuitState = tokenServiceCircuitChild
	return m, nil
}
