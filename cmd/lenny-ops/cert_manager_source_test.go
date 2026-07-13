// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/opsservice"
)

// TestCertManagerSource_HealthDerivation_spec_25_8 exercises the real
// §25.8 cert-manager adapter end to end: certManagerSource.CertStatuses
// lists cert-manager Certificate CRs via the dynamic client and
// certStatusFromUnstructured maps each resource's status.notAfter and
// Ready condition into the opsservice.CertStatus the CertManagerCheck
// classifies. The well-tested derivation is fed here from live
// Certificate objects rather than a hand-built CertStatus slice, so a
// field-path or Ready-condition-parsing regression in the adapter is
// caught.
//
// The spec pins the three aggregate states (§25.8):
//   - healthy: all certificates are renewed and valid for >30 days.
//   - degraded: at least one certificate expires within 30 days AND
//     renewal has failed in the last attempt.
//   - unhealthy: at least one certificate expires within 7 days OR has
//     already expired.
//
// diagnosis: a failure means the cert-manager Certificate adapter no
// longer reads status.notAfter or the Ready condition correctly, so
// lenny-ops would misreport certificate health (a valid cert flagged
// unhealthy, or an expiring cert reported healthy).
//
// spec: §25.8 (cert-manager integration, health derivation)
func TestCertManagerSource_HealthDerivation_spec_25_8(t *testing.T) {
	const ns = "lenny-system"
	day := 24 * time.Hour

	cases := []struct {
		name     string
		notAfter time.Duration // relative to now
		ready    bool
		want     opsservice.HealthStatus
	}{
		// Ready, valid for >30 days: renewed and healthy.
		{"valid_31_days", 31 * day, true, opsservice.StatusHealthy},
		// Within 30 days with a failed renewal (Ready != True): degraded.
		{"expiring_29_days_renewal_failed", 29 * day, false, opsservice.StatusDegraded},
		// Within 7 days: unhealthy regardless of renewal state.
		{"expiring_6_days", 6 * day, true, opsservice.StatusUnhealthy},
		// Already expired: unhealthy.
		{"expired", -1 * day, true, opsservice.StatusUnhealthy},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cert := certObj(ns, "gateway-tls", "gateway-tls-secret", time.Now().Add(tc.notAfter), tc.ready)
			var expiryCalls int
			src := certManagerSource{
				client:    certDynClient(cert),
				namespace: ns,
				onExpiry: func(certificate string, secondsRemaining float64) {
					expiryCalls++
					if certificate != ns+"/gateway-tls" {
						t.Errorf("onExpiry certificate = %q, want %s/gateway-tls", certificate, ns)
					}
				},
			}

			// Sanity-check the adapter's mapping directly so a wrong
			// field path surfaces distinctly from a wrong classification.
			statuses, err := src.CertStatuses(context.Background())
			if err != nil {
				t.Fatalf("CertStatuses: %v", err)
			}
			if len(statuses) != 1 {
				t.Fatalf("CertStatuses returned %d certs, want 1", len(statuses))
			}
			if got := statuses[0].RenewalFailed; got == tc.ready {
				t.Errorf("RenewalFailed = %v for ready=%v; Ready condition parsed wrong", got, tc.ready)
			}
			if statuses[0].NotAfter.IsZero() {
				t.Errorf("NotAfter is zero; status.notAfter not parsed from the Certificate")
			}
			if expiryCalls != 1 {
				t.Errorf("onExpiry called %d times, want 1 (one certificate with a known expiry)", expiryCalls)
			}

			got := opsservice.CertManagerCheck(src)()
			if got.Status != tc.want {
				t.Errorf("CertManagerCheck status = %v (%q), want %v", got.Status, got.Detail, tc.want)
			}
			if got.Name != opsservice.CheckCertManager {
				t.Errorf("check name = %q, want %q", got.Name, opsservice.CheckCertManager)
			}
		})
	}
}
