// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §10.3 mTLS PKI. The test installs the
// Lenny control plane on a Kind cluster via the install.sh-backed
// kind.InstallLenny harness and asserts the cert-manager Certificate
// resources for the internal mTLS control plane and the
// admission-webhook serving certs are all issued (Ready).

package tier5_e2e_kind_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// mtlsPKICertificates names the §10.3 internal-mTLS Certificate
// resources the chart renders: the cluster-internal CA and the
// per-component serving leaves for the gateway, controller, and token
// service.
var mtlsPKICertificates = []string{
	"lenny-mtls-ca",
	"lenny-gateway-tls",
	"lenny-controller-tls",
	"lenny-token-service-tls",
}

// webhookServingCertificates names the admission-webhook serving
// Certificate resources for the Phase 3.5 baseline webhooks. Each
// webhook's ValidatingWebhookConfiguration carries a
// cert-manager.io/inject-ca-from annotation that points at the
// matching Certificate, so an un-issued cert leaves the webhook
// without a serving identity and the API server cannot call it.
var webhookServingCertificates = []string{
	"lenny-label-immutability",
	"lenny-sandboxclaim-guard",
	"lenny-pool-config-validator",
	"lenny-ephemeral-container-cred-guard",
	"lenny-pod-security",
}

// spec: 10.3
// diagnosis: §10.3 mTLS enforcement is not in place. The test reads
// every cert-manager Certificate in lenny-system and asserts the
// §10.3 PKI certificates (the internal CA, the gateway, controller,
// and token-service serving leaves) and the admission-webhook serving
// certs all report the Ready condition. An un-Ready Certificate means
// cert-manager could not issue it: the gateway-to-component mTLS path
// has no serving identity, or a webhook the API server must call has
// no TLS cert. The test also confirms the three cert-manager Issuers
// the PKI rests on are Ready.
func TestMTLSEnforcement(t *testing.T) {
	c := kind.InstallLenny(t)

	ready := certificateReadiness(t, c)

	for _, name := range mtlsPKICertificates {
		assertCertReady(t, ready, name, "§10.3 internal-mTLS PKI")
	}
	for _, name := range webhookServingCertificates {
		assertCertReady(t, ready, name, "admission-webhook serving cert")
	}

	// The §10.3 PKI rests on a two-issuer cert-manager pattern: a
	// self-signed Issuer mints the CA, and a CA-kind Issuer signs every
	// leaf. The webhook serving certs chain to lenny-webhook-selfsign.
	// None of the leaf certs can be Ready unless their Issuers are.
	issuerReady := issuerReadiness(t, c)
	for _, name := range []string{"lenny-mtls-selfsign", "lenny-mtls-ca", "lenny-webhook-selfsign"} {
		cond, present := issuerReady[name]
		if !present {
			t.Errorf("cert-manager Issuer %q is not installed; the §10.3 PKI cannot issue leaf certs without it", name)
			continue
		}
		if cond != "True" {
			t.Errorf("cert-manager Issuer %q is not Ready (condition %q)", name, cond)
		}
	}
	t.Logf("mTLS PKI: %d Certificates checked, all Ready", len(mtlsPKICertificates)+len(webhookServingCertificates))
}

// assertCertReady fails the test when the named Certificate is absent
// from the readiness map or its Ready condition is not "True".
func assertCertReady(t *testing.T, ready map[string]string, name, role string) {
	t.Helper()
	cond, present := ready[name]
	if !present {
		t.Errorf("Certificate %q (%s) is not installed in lenny-system", name, role)
		return
	}
	if cond != "True" {
		t.Errorf("Certificate %q (%s) is not Ready; cert-manager reports Ready=%q", name, role, cond)
	}
}

// certificateReadiness reads every cert-manager Certificate in
// lenny-system and returns a map from Certificate name to the status
// of its Ready condition ("True", "False", or "" when no Ready
// condition is present yet).
func certificateReadiness(t *testing.T, c *kind.Cluster) map[string]string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", "lenny-system", "get", "certificates.cert-manager.io",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"=\"}"+
			"{range .status.conditions[?(@.type==\"Ready\")]}{.status}{end}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("list cert-manager Certificates in lenny-system: %v\n%s", err, out)
	}
	return parseNameStatus(out)
}

// issuerReadiness reads every cert-manager Issuer in lenny-system and
// returns a map from Issuer name to the status of its Ready condition.
func issuerReadiness(t *testing.T, c *kind.Cluster) map[string]string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", "lenny-system", "get", "issuers.cert-manager.io",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"=\"}"+
			"{range .status.conditions[?(@.type==\"Ready\")]}{.status}{end}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("list cert-manager Issuers in lenny-system: %v\n%s", err, out)
	}
	return parseNameStatus(out)
}

// parseNameStatus parses the `name=status` lines a jsonpath range
// emits into a map. Lines with no `=` are skipped.
func parseNameStatus(out string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, status, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || name == "" {
			continue
		}
		result[name] = status
	}
	return result
}
