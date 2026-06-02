// SPDX-License-Identifier: MIT

package controllermetrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestCertExpirySetAndClear_spec_10_3 verifies the §10.3 line 342/343
// lenny_cert_expiry_seconds gauge records a pod's remaining certificate
// validity and that Clear removes the series so a deleted pod's value
// cannot pin the CertExpiryImminent alert.
func TestCertExpirySetAndClear_spec_10_3(t *testing.T) {
	const ns, pod = "lenny-agents", "cert-gauge-pod"
	m := CertExpiry{}

	m.Set(ns, pod, 1800)
	if got := testutil.ToFloat64(certExpirySeconds.WithLabelValues(ns, pod)); got != 1800 {
		t.Fatalf("lenny_cert_expiry_seconds=%v after Set, want 1800", got)
	}

	m.Clear(ns, pod)
	if got := testutil.CollectAndCount(certExpirySeconds); got != 0 {
		t.Fatalf("lenny_cert_expiry_seconds series count=%d after Clear, want 0", got)
	}
}
