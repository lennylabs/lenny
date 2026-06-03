// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"fmt"
)

// OpsAdminTLSConfig carries the §25.4 NET-070 admin-API TLS values the
// ops-admin-tls check evaluates.
type OpsAdminTLSConfig struct {
	// Endpoint is the gateway internal admin-API host:port lenny-ops
	// reaches over TLS (the lenny-gateway ClusterIP hostname on the
	// gateway internalTLSPort). Empty skips the check.
	Endpoint string
	// InternalEnabled is the ops.tls.internalEnabled value. When false
	// the operator opted into the plaintext admin API via
	// acknowledgePlaintextAdminAPI, so the live handshake does not apply.
	InternalEnabled bool
	// ExpectedSANHost is the hostname the server certificate's SAN must
	// cover (the lenny-gateway ClusterIP hostname). The prober validates
	// the handshake against this name.
	ExpectedSANHost string
}

// OpsAdminTLSProber performs a live TLS handshake against the gateway
// internal admin-API port, validating the server certificate against the
// expected SAN host using the deployer trust bundle. The lenny-preflight
// Job constructs a real prober; tests pass a fake. A nil prober skips the
// live handshake.
type OpsAdminTLSProber interface {
	Probe(ctx context.Context, endpoint, sanHost string) error
}

// OpsAdminTLSProbeFunc adapts a function to OpsAdminTLSProber.
type OpsAdminTLSProbeFunc func(ctx context.Context, endpoint, sanHost string) error

// Probe calls f.
func (f OpsAdminTLSProbeFunc) Probe(ctx context.Context, endpoint, sanHost string) error {
	return f(ctx, endpoint, sanHost)
}

// OpsAdminTLSCheck is the §17.9 / §25.4 NET-070 ops-admin-tls preflight
// check.
type OpsAdminTLSCheck struct {
	Config OpsAdminTLSConfig
	// Prober runs the live TLS 1.2+ handshake + SAN validation. Nil
	// passes (the value-only posture is carried by the chart's
	// ops-tls-guard render-time guard).
	Prober OpsAdminTLSProber
}

// Decide validates the §25.4 NET-070 internal admin-API TLS posture. The
// check is skipped (passes) when internal TLS is disabled — the operator
// acknowledged plaintext via the chart's acknowledgePlaintextAdminAPI
// guard, so the handshake does not apply. When internal TLS is enabled
// and an endpoint is configured, a wired prober must complete the TLS
// handshake and validate that the server certificate's SAN covers the
// lenny-gateway ClusterIP hostname; a handshake failure or SAN mismatch
// fails the check so an install does not proceed against a misconfigured
// admin-API certificate.
//
// spec: §25.4 lines 2544-2546 (the ops-admin-tls handshake probe). F-25.4.19.
func (c OpsAdminTLSCheck) Decide(ctx context.Context) Decision {
	if !c.Config.InternalEnabled || c.Config.Endpoint == "" {
		return Decision{Passed: true}
	}
	if c.Prober == nil {
		return Decision{Passed: true}
	}
	if err := c.Prober.Probe(ctx, c.Config.Endpoint, c.Config.ExpectedSANHost); err != nil {
		return Decision{Passed: false, Reason: fmt.Sprintf(
			"OPS_ADMIN_TLS_HANDSHAKE_FAILED: TLS handshake against the gateway internal admin API %q failed: %v; "+
				"the gateway admin-API certificate must be signed by a CA in the trust bundle "+
				"(ops.tls.caBundleConfigMap, or the cert-manager CA) and its SAN must cover %q (§25.4 NET-070). "+
				"Set ops.tls.internalEnabled=false with ops.acknowledgePlaintextAdminAPI=true to opt into plaintext.",
			c.Config.Endpoint, err, c.Config.ExpectedSANHost,
		)}
	}
	return Decision{Passed: true}
}
