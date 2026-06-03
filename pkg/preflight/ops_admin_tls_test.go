// SPDX-License-Identifier: MIT

package preflight

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestOpsAdminTLSCheck covers the §25.4 NET-070 ops-admin-tls decision
// matrix (lines 2544-2546): skipped when internal TLS is off, skipped
// when no endpoint is configured, skipped when no prober is wired,
// passing on a successful handshake, and failing on a handshake or SAN
// failure.
func TestOpsAdminTLSCheck_spec_25_4(t *testing.T) {
	okProbe := OpsAdminTLSProbeFunc(func(context.Context, string, string) error { return nil })
	failProbe := OpsAdminTLSProbeFunc(func(context.Context, string, string) error {
		return errors.New("x509: certificate is valid for other-host, not lenny-gateway")
	})

	tests := []struct {
		name       string
		cfg        OpsAdminTLSConfig
		prober     OpsAdminTLSProber
		wantPassed bool
	}{
		{
			name:       "internal TLS disabled skips",
			cfg:        OpsAdminTLSConfig{Endpoint: "lenny-gateway:8443", InternalEnabled: false},
			prober:     failProbe,
			wantPassed: true,
		},
		{
			name:       "no endpoint skips",
			cfg:        OpsAdminTLSConfig{InternalEnabled: true},
			prober:     failProbe,
			wantPassed: true,
		},
		{
			name:       "no prober passes (render-time guard carries the posture)",
			cfg:        OpsAdminTLSConfig{Endpoint: "lenny-gateway:8443", InternalEnabled: true},
			prober:     nil,
			wantPassed: true,
		},
		{
			name:       "successful handshake passes",
			cfg:        OpsAdminTLSConfig{Endpoint: "lenny-gateway:8443", InternalEnabled: true, ExpectedSANHost: "lenny-gateway"},
			prober:     okProbe,
			wantPassed: true,
		},
		{
			name:       "handshake failure fails",
			cfg:        OpsAdminTLSConfig{Endpoint: "lenny-gateway:8443", InternalEnabled: true, ExpectedSANHost: "lenny-gateway"},
			prober:     failProbe,
			wantPassed: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec := OpsAdminTLSCheck{Config: tc.cfg, Prober: tc.prober}.Decide(context.Background())
			if dec.Passed != tc.wantPassed {
				t.Fatalf("Passed = %v, want %v (reason: %q)", dec.Passed, tc.wantPassed, dec.Reason)
			}
			if !dec.Passed && !strings.Contains(dec.Reason, "OPS_ADMIN_TLS_HANDSHAKE_FAILED") {
				t.Fatalf("failure reason missing the error code: %q", dec.Reason)
			}
		})
	}
}
