// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security render checks for the §17.4 C3 chart gates. The §17.4
// development profile disables the fail-closed admission stack, the
// Token Service, and mTLS so the embedded cluster can run with no
// cert-manager and no Token Service Deployment. The security property
// these gates must preserve is that the gates default to the production
// posture: a stock production render must keep the fail-closed
// admission webhooks, the §4.3 Token Service trust boundary, and render
// no gateway dev-bearer-trust mount unless the operator supplies the
// Secret. A gate that silently weakened the production default would
// disable a security control on every install.
//
// These are chart-render assertions through the helm CLI, so they need
// no live cluster, complementing the live-cluster admission probes in
// admission_security_test.go.
package tier9_security_test

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

const certManagerGVR = "cert-manager.io/v1"

// renderProdDefaults renders the chart with stock production values
// (only the required SPIFFE trust domain and CoreDNS clusterIP), so the
// admissionWebhooks.enabled / tokenService.enabled gates take their
// defaults.
func renderProdDefaults(t *testing.T) helm.Manifests {
	t.Helper()
	return helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set: []string{
			"global.spiffeTrustDomain=lenny-test",
			"coredns.clusterIP=10.96.0.10",
		},
	})
}

// gatewayHasArg reports whether the lenny-gateway Deployment's gateway
// container carries an arg matching prefix (`--flag` or `--flag=value`).
func gatewayHasArg(t *testing.T, m helm.Manifests, prefix string) bool {
	t.Helper()
	dep := m.MustFind(t, "Deployment", "lenny-gateway")
	spec, _ := dep.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		if n, _ := cm["name"].(string); n != "gateway" {
			continue
		}
		args, _ := cm["args"].([]any)
		for _, a := range args {
			s, _ := a.(string)
			if s == prefix || strings.HasPrefix(s, prefix+"=") {
				return true
			}
		}
	}
	return false
}

// TestProductionRenderKeepsFailClosedAdmissionStack asserts that a stock
// production render keeps admissionWebhooks.enabled true: the §17.2
// fail-closed validating webhooks render with failurePolicy: Fail. A
// gate that defaulted to disabled would silently drop the §13.1
// pod-security boundary on every install.
//
// spec: §17.4 (C3 chart gates), §17.2 (admission webhooks fail-closed)
// diagnosis: the C3 admissionWebhooks.enabled gate defaults to disabled,
// dropping the fail-closed admission stack on a production render.
func TestProductionRenderKeepsFailClosedAdmissionStack(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := renderProdDefaults(t)

	vwcs := m.FindAll("ValidatingWebhookConfiguration")
	if len(vwcs) == 0 {
		t.Fatalf("production render must keep the §17.2 fail-closed admission webhooks; got 0 ValidatingWebhookConfiguration")
	}
	for _, vwc := range vwcs {
		webhooks, _ := vwc.Raw["webhooks"].([]any)
		for _, w := range webhooks {
			wm, _ := w.(map[string]any)
			fp, _ := wm["failurePolicy"].(string)
			if fp != "Fail" {
				name, _ := wm["name"].(string)
				t.Errorf("webhook %q in %s carries failurePolicy %q, want Fail (fail-closed admission)", name, vwc.Name, fp)
			}
		}
	}
	if got := countCertManagerObjects(m); got == 0 {
		t.Errorf("production render must keep the cert-manager-issued webhook serving certificates; got 0 cert-manager.io/v1 objects")
	}
}

// TestProductionRenderKeepsTokenServiceTrustBoundary asserts that a
// stock production render keeps tokenService.enabled true: the Token
// Service Deployment renders and the gateway dials it for §4.9 leases
// and the §4.3 /v1/oauth/* proxy. A gate that defaulted to disabled
// would route every lease through the in-process dev path on a
// production install, collapsing the §4.3 trust boundary.
//
// spec: §17.4 (C3 chart gates), §4.3 (token-service trust boundary)
// diagnosis: the C3 tokenService.enabled gate defaults to disabled,
// collapsing the §4.3 trust boundary onto the in-process dev path on a
// production render.
func TestProductionRenderKeepsTokenServiceTrustBoundary(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := renderProdDefaults(t)

	if _, ok := m.Find("Deployment", "lenny-token-service"); !ok {
		t.Errorf("production render must keep the lenny-token-service Deployment")
	}
	if !gatewayHasArg(t, m, "--token-service-grpc-addr") {
		t.Errorf("production render must keep the gateway --token-service-grpc-addr flag")
	}
	if !gatewayHasArg(t, m, "--token-service-http-url") {
		t.Errorf("production render must keep the gateway --token-service-http-url flag")
	}
}

// TestProductionRenderHasNoGatewayBearerTrustMount asserts that a stock
// production render (empty security.oidc.bearerTrustKeySecret) renders
// no gateway --bearer-trust-hmac-key-file flag. The dev-bearer-trust
// second verifier must light up only when the operator supplies the
// Secret, so a production gateway trusts only its standard §10.2
// verifiers and never an HMAC key it was not given.
//
// spec: §17.4 (C3 chart gates), §10.2 (bearer-trust verifier)
// diagnosis: the gateway renders the dev-bearer-trust verifier even when
// no bearerTrustKeySecret is supplied, trusting an HMAC key on a
// production install.
func TestProductionRenderHasNoGatewayBearerTrustMount(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := renderProdDefaults(t)

	if gatewayHasArg(t, m, "--bearer-trust-hmac-key-file") {
		t.Errorf("production render (empty bearerTrustKeySecret) must render no gateway --bearer-trust-hmac-key-file flag")
	}
}

// TestDevDisablingRenderDropsCertManagerAndTokenServiceDial asserts the
// §17.4 development settings (admissionWebhooks.enabled=false,
// tokenService.enabled=false, mtls.enabled=false) render no
// cert-manager.io/v1 object and strip the gateway's Token Service dial,
// so the embedded cluster runs with no cert-manager and the gateway
// never dials an absent Service. This is the security counterpart to the
// production-default checks: the gates must actually disable the
// surfaces when the development profile sets them false.
//
// spec: §17.4 (C3 chart gates), §17.2 (admission webhooks), §4.3
// (token-service trust boundary)
// diagnosis: the C3 gates leave a cert-manager object or a Token Service
// dial in the development render, so the embedded cluster cannot run
// without cert-manager or the gateway dials a Service that never
// schedules.
func TestDevDisablingRenderDropsCertManagerAndTokenServiceDial(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set: []string{
			"global.spiffeTrustDomain=lenny-test",
			"coredns.clusterIP=10.96.0.10",
			"admissionWebhooks.enabled=false",
			"tokenService.enabled=false",
			"mtls.enabled=false",
			"certmanager.enabled=false",
		},
	})

	if got := countCertManagerObjects(m); got != 0 {
		t.Errorf("development render must emit no cert-manager.io/v1 object so the embedded cluster needs no cert-manager; got %d", got)
	}
	if _, ok := m.Find("Deployment", "lenny-token-service"); ok {
		t.Errorf("development render must not render the lenny-token-service Deployment")
	}
	if gatewayHasArg(t, m, "--token-service-grpc-addr") {
		t.Errorf("development render must strip the gateway --token-service-grpc-addr flag so the gateway uses the in-process dev path")
	}
}

// countCertManagerObjects returns how many rendered manifests carry the
// cert-manager.io/v1 apiVersion.
func countCertManagerObjects(m helm.Manifests) int {
	n := 0
	for _, x := range m {
		if x.APIVersion == certManagerGVR {
			n++
		}
	}
	return n
}
