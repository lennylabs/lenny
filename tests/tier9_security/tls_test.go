// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §10.3 mTLS PKI and the admission-webhook
// TLS trust path. The tests install the Lenny control plane on a Kind
// cluster (via the install.sh-backed kind.InstallLenny harness) and
// assert two distinct security properties against the live cluster:
//
//  1. Every §10.3 cert-manager Certificate and Issuer that backs the
//     internal mTLS control plane and the webhook serving identities is
//     issued (Ready=True).
//  2. Every fail-closed ValidatingWebhookConfiguration carries a
//     non-empty caBundle, i.e. cert-manager's CA injection completed.
//     An empty caBundle leaves the API server unable to verify the
//     webhook's serving certificate, which on a failurePolicy=Fail
//     webhook blocks all admission for the resources it guards.

package tier9_security_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// mtlsCertificates names the §10.3 internal-mTLS Certificate resources
// the chart renders: the cluster-internal CA and the per-component
// serving leaves for the gateway, controller, and token service.
var mtlsCertificates = []string{
	"lenny-mtls-ca",
	"lenny-gateway-tls",
	"lenny-controller-tls",
	"lenny-token-service-tls",
}

// mtlsIssuers names the cert-manager Issuers the §10.3 PKI rests on: a
// self-signed Issuer mints the internal CA, a CA-kind Issuer signs the
// component leaves, and a separate self-signed Issuer anchors the
// webhook serving certs.
var mtlsIssuers = []string{
	"lenny-mtls-selfsign",
	"lenny-mtls-ca",
	"lenny-webhook-selfsign",
}

// caInjectedWebhooks names the fail-closed ValidatingWebhookConfigurations
// the Phase 3.5 baseline install renders. Each carries a
// cert-manager.io/inject-ca-from annotation; cert-manager's cainjector
// populates webhooks[*].clientConfig.caBundle from the referenced
// Certificate's CA once the Certificate is Ready.
var caInjectedWebhooks = []string{
	"lenny-label-immutability",
	"lenny-sandboxclaim-guard",
	"lenny-pool-config-validator",
	"lenny-ephemeral-container-cred-guard",
	"lenny-pod-security",
}

// spec: 10.3
// diagnosis: §10.3 mTLS enforcement is not in place. The test reads
// every cert-manager Certificate and Issuer in lenny-system and asserts
// the §10.3 PKI resources (internal CA, gateway/controller/token-service
// serving leaves, webhook serving certs) and the three Issuers all
// report Ready=True. An un-Ready Certificate means the gateway-to-
// component mTLS link or a webhook serving identity has no usable cert.
func TestMTLSCertificatesReady(t *testing.T) {
	c := kind.InstallLenny(t)

	certReady := conditionReadiness(t, c, "certificates.cert-manager.io")
	for _, name := range mtlsCertificates {
		assertReady(t, certReady, "Certificate", name, "§10.3 internal-mTLS PKI")
	}
	for _, name := range caInjectedWebhooks {
		assertReady(t, certReady, "Certificate", name, "admission-webhook serving cert")
	}

	issuerReady := conditionReadiness(t, c, "issuers.cert-manager.io")
	for _, name := range mtlsIssuers {
		assertReady(t, issuerReady, "Issuer", name, "§10.3 PKI issuer")
	}
	t.Logf("§10.3 PKI: %d Certificates and %d Issuers checked, all Ready",
		len(mtlsCertificates)+len(caInjectedWebhooks), len(mtlsIssuers))
}

// spec: 10.3
// diagnosis: cert-manager CA injection into the admission webhooks did
// not complete. The test reads webhooks[0].clientConfig.caBundle for
// every fail-closed ValidatingWebhookConfiguration and asserts it is a
// non-empty, base64-decodable PEM bundle. An empty caBundle means the
// API server cannot verify the webhook's serving cert; on a
// failurePolicy=Fail webhook that blocks all admission for the resources
// it guards, so an empty caBundle is a fail-closed admission outage.
func TestWebhookCABundlesPopulated(t *testing.T) {
	c := kind.InstallLenny(t)

	for _, webhook := range caInjectedWebhooks {
		out, err := c.KubectlOut(
			t,
			"get", "validatingwebhookconfiguration", webhook,
			"-o", "jsonpath={.webhooks[0].clientConfig.caBundle}",
		)
		if err != nil {
			t.Errorf("read caBundle for webhook %q: %v\n%s", webhook, err, out)
			continue
		}
		bundle := strings.TrimSpace(out)
		if bundle == "" {
			t.Errorf("webhook %q has an empty clientConfig.caBundle; cert-manager CA injection did not "+
				"complete and the API server cannot verify the webhook's serving cert", webhook)
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(bundle)
		if err != nil {
			t.Errorf("webhook %q caBundle is not valid base64: %v", webhook, err)
			continue
		}
		if !strings.Contains(string(decoded), "BEGIN CERTIFICATE") {
			t.Errorf("webhook %q caBundle decodes but contains no PEM certificate block; "+
				"the injected bundle is not a CA certificate", webhook)
			continue
		}
		t.Logf("webhook %q caBundle populated: %d-byte PEM bundle injected", webhook, len(decoded))
	}
}

// assertReady fails the test when the named resource is absent from the
// readiness map or its Ready condition is not "True".
func assertReady(t *testing.T, ready map[string]string, kind, name, role string) {
	t.Helper()
	cond, present := ready[name]
	if !present {
		t.Errorf("%s %q (%s) is not installed in lenny-system", kind, name, role)
		return
	}
	if cond != "True" {
		t.Errorf("%s %q (%s) is not Ready; cert-manager reports Ready=%q", kind, name, role, cond)
	}
}

// conditionReadiness reads every resource of the given kind in
// lenny-system and returns a map from resource name to the status of
// its Ready condition ("True", "False", or "" when no Ready condition
// is present yet).
func conditionReadiness(t *testing.T, c *kind.Cluster, resourceKind string) map[string]string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "get", resourceKind,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"=\"}"+
			"{range .status.conditions[?(@.type==\"Ready\")]}{.status}{end}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("list %s in lenny-system: %v\n%s", resourceKind, err, out)
	}
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
