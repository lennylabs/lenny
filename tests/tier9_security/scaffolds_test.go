// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test scaffolds. Each test corresponds to a
// TESTING.md-named security check that needs either the production
// stack on a Kind cluster, a signed-image registry, or a third-party
// pen-test driver. Today each calls t.Skip with a diagnosis.

package tier9_security_test

import "testing"

// §13.6 Phase 3 — mTLS enforcement.
func TestMTLS(t *testing.T) {
	t.Skip("not implemented: §10.3 mTLS PKI — requires cert-manager + the gateway / pod / interceptor SPIFFE wiring on a Kind cluster")
}

// §13.30 Phase 14 — image signing.
func TestImageSigning(t *testing.T) {
	t.Skip("not implemented: §13.2 image signing — requires cosign + the ValidatingAdmissionWebhook on a Kind cluster")
}

// §13.30 Phase 14 — NetworkPolicy enforcement.
func TestNetworkPolicy(t *testing.T) {
	t.Skip("not implemented: §13.2 NetworkPolicy enforcement — requires Calico/Cilium CNI on Kind + the rendered NetworkPolicies from the chart")
}

// §13.32 Phase 15 — environment RBAC.
func TestEnvironmentRBAC(t *testing.T) {
	t.Skip("not implemented: §10.6 environment RBAC — requires the admin API + OIDC group resolver + Kind cluster")
}

// §13.30 Phase 14 — external pen-test driver.
func TestPentest(t *testing.T) {
	t.Skip("not implemented: §13 pen-test driver — requires a third-party pen-test runner replay against a live deployment")
}
