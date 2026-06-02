// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
	"strings"
)

// OTLPTLSConfig carries the §13.2 OTLP-068 observability values the
// otlp-tls check evaluates.
type OTLPTLSConfig struct {
	// Endpoint is the observability.otlpEndpoint value. Empty skips the
	// check entirely (no OTLP export configured).
	Endpoint string
	// TLSEnabled is the observability.otlpTlsEnabled value. When false
	// the operator has opted into plaintext OTLP via the chart's
	// acknowledgeOtlpPlaintext guard, so the live-handshake and scheme
	// checks do not apply.
	TLSEnabled bool
}

// OTLPTLSProber performs a live TLS handshake against the OTLP collector
// endpoint, validating the server certificate against the endpoint
// hostname using the deployer's trust bundle. The lenny-preflight Job
// constructs a real prober; tests pass a fake. A nil prober skips the
// live handshake (the scheme guard still runs).
type OTLPTLSProber interface {
	Probe(ctx context.Context, endpoint string) error
}

// OTLPTLSProbeFunc adapts a function to OTLPTLSProber.
type OTLPTLSProbeFunc func(ctx context.Context, endpoint string) error

// Probe calls f.
func (f OTLPTLSProbeFunc) Probe(ctx context.Context, endpoint string) error { return f(ctx, endpoint) }

// OTLPTLSCheck is the §17.9 otlp-tls preflight check.
type OTLPTLSCheck struct {
	Config OTLPTLSConfig
	// Prober runs the live TLS 1.2+ handshake. Nil runs only the
	// scheme guard (the pure-value check that never needs a live
	// collector).
	Prober OTLPTLSProber
}

// Decide validates the §13.2 OTLP-068 TLS posture. When TLS is enabled
// and an endpoint is configured it (1) fails if the endpoint scheme is
// http:// (the common misconfiguration where the scheme contradicts the
// TLS flag), and (2) when a prober is wired, fails if the live TLS 1.2+
// handshake does not complete or the server certificate SAN does not
// match the endpoint host. When TLS is disabled the operator acknowledged
// plaintext via the chart's acknowledgeOtlpPlaintext guard, so the check
// passes (the chart's render-time guard and NOTES banner carry that
// posture).
//
// spec: §13.2 lines 176-178 (otlp-tls live handshake + http:// scheme
// rejection when otlpTlsEnabled is true). F-13.2.9.
func (c OTLPTLSCheck) Decide(ctx context.Context) Decision {
	if c.Config.Endpoint == "" || !c.Config.TLSEnabled {
		return Decision{Passed: true}
	}
	if strings.HasPrefix(strings.ToLower(c.Config.Endpoint), "http://") {
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"OTLP_ENDPOINT_SCHEME_MISMATCH: observability.otlpEndpoint %q begins with http:// while "+
				"observability.otlpTlsEnabled is true; plaintext OTLP export exposes tenant/session metadata "+
				"in-cluster. Use https:// or set otlpTlsEnabled=false with acknowledgeOtlpPlaintext=true (§13.2 OTLP-068)",
			c.Config.Endpoint,
		)}
	}
	if c.Prober == nil {
		return Decision{Passed: true}
	}
	if err := c.Prober.Probe(ctx, c.Config.Endpoint); err != nil {
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"OTLP_TLS_HANDSHAKE_FAILED: TLS handshake against observability.otlpEndpoint %q failed: %v; "+
				"the collector certificate must be signed by a CA in the deployer trust bundle "+
				"(observability.otlpCaBundle) and its SAN must cover the endpoint host (§13.2 OTLP-068)",
			c.Config.Endpoint, err,
		)}
	}
	return Decision{Passed: true}
}
