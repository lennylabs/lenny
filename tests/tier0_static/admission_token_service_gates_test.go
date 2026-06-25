// SPDX-License-Identifier: MIT

package tier0_static

import (
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// renderArgs are the chart values every render in this file needs: the
// required §10.3 SPIFFE trust domain and a CoreDNS clusterIP (required
// once agentNamespaces is non-empty, the stock default).
func renderArgs(extra ...string) []string {
	base := []string{
		"global.spiffeTrustDomain=lenny-test",
		"coredns.clusterIP=10.96.0.10",
	}
	return append(base, extra...)
}

// certManagerGVR is the apiVersion every cert-manager object the chart
// renders (the webhook Certificates and the shared Issuer) carries. The
// §17.4 development render must emit none of these so a cert-manager-free
// embedded k3s never has to resolve the cert-manager.io/v1 GVR.
const certManagerGVR = "cert-manager.io/v1"

// countCertManager returns how many rendered manifests carry the
// cert-manager.io/v1 apiVersion.
func countCertManager(m helm.Manifests) int {
	n := 0
	for _, x := range m {
		if x.APIVersion == certManagerGVR {
			n++
		}
	}
	return n
}

// gatewayArgs extracts the --flag args list from the lenny-gateway
// Deployment's gateway container, so a test can assert a flag is present
// or absent without grepping raw YAML.
func gatewayArgs(t *testing.T, m helm.Manifests) []string {
	t.Helper()
	dep := m.MustFind(t, "Deployment", "lenny-gateway")
	spec, _ := dep.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		name, _ := cm["name"].(string)
		if name != "gateway" {
			continue
		}
		rawArgs, _ := cm["args"].([]any)
		out := make([]string, 0, len(rawArgs))
		for _, a := range rawArgs {
			if s, ok := a.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	t.Fatalf("lenny-gateway Deployment has no gateway container")
	return nil
}

// hasArg reports whether any arg in args starts with prefix (matching
// `--flag` or `--flag=value`).
func hasArg(args []string, prefix string) bool {
	for _, a := range args {
		if a == prefix || strings.HasPrefix(a, prefix+"=") {
			return true
		}
	}
	return false
}

// TestAdmissionWebhooksDefaultRendersFullStack asserts the production
// default (admissionWebhooks.enabled true) renders the §17.2 fail-closed
// admission stack: the validating webhooks, the shared self-signed
// Issuer, and at least one cert-manager Certificate. This pins that the
// gate defaults to the production posture rather than silently disabling
// admission.
//
// spec: §17.4 (C3 chart gates), §17.2 (admission webhooks)
func TestAdmissionWebhooksDefaultRendersFullStack(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       renderArgs(),
	})

	if _, ok := m.Find("ValidatingWebhookConfiguration", "lenny-pod-security"); !ok {
		t.Errorf("admissionWebhooks.enabled defaults true: the lenny-pod-security ValidatingWebhookConfiguration must render")
	}
	if _, ok := m.Find("Issuer", "lenny-webhook-selfsign"); !ok {
		t.Errorf("admissionWebhooks.enabled defaults true: the shared lenny-webhook-selfsign Issuer must render")
	}
	if got := countCertManager(m); got == 0 {
		t.Errorf("admissionWebhooks.enabled defaults true: at least one cert-manager.io/v1 object (webhook Certificate / Issuer) must render, got 0")
	}
	if _, ok := m.Find("Deployment", "lenny-pod-security"); !ok {
		t.Errorf("admissionWebhooks.enabled defaults true: the lenny-pod-security webhook Deployment must render")
	}
}

// TestAdmissionWebhooksDisabledRendersNoCertManagerObject asserts that
// admissionWebhooks.enabled=false (the §17.4 development setting) renders
// no cert-manager.io/v1 object at all: no webhook Certificate, no shared
// Issuer, and no conversion-webhook or cosign Certificate. This is the
// load-bearing property that lets the embedded cluster run with no
// cert-manager installed — a single missing gate would leave a
// cert-manager.io/v1 object the dynamic applier cannot resolve on a bare
// k3s, aborting bring-up.
//
// spec: §17.4 (C3 chart gates), §17.2 (admission webhooks)
func TestAdmissionWebhooksDisabledRendersNoCertManagerObject(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set: renderArgs(
			"admissionWebhooks.enabled=false",
			"mtls.enabled=false",
			"certmanager.enabled=false",
		),
	})

	if got := countCertManager(m); got != 0 {
		var names []string
		for _, x := range m {
			if x.APIVersion == certManagerGVR {
				names = append(names, x.Kind+"/"+x.Name)
			}
		}
		t.Errorf("admissionWebhooks.enabled=false must render no cert-manager.io/v1 object, got %d: %v", got, names)
	}
	if got := len(m.FindAll("ValidatingWebhookConfiguration")); got != 0 {
		t.Errorf("admissionWebhooks.enabled=false must render no ValidatingWebhookConfiguration, got %d", got)
	}
	if _, ok := m.Find("Issuer", "lenny-webhook-selfsign"); ok {
		t.Errorf("admissionWebhooks.enabled=false must not render the shared lenny-webhook-selfsign Issuer")
	}
	// The shared helper gates the webhook Deployment, Service, PDB, and
	// Certificate; none of the per-webhook workloads may render.
	for _, name := range []string{
		"lenny-pod-security",
		"lenny-sandboxclaim-guard",
		"lenny-direct-mode-isolation",
		"lenny-ephemeral-container-cred-guard",
		"lenny-pool-config-validator",
		"lenny-sandboxtemplate-deletion-guard",
		"lenny-label-immutability",
		"lenny-crd-conversion",
	} {
		if _, ok := m.Find("Deployment", name); ok {
			t.Errorf("admissionWebhooks.enabled=false must not render the %s webhook Deployment", name)
		}
	}
}

// TestTokenServiceDefaultRendersDeploymentAndGatewayFlags asserts the
// production default (tokenService.enabled true) renders the Token
// Service Deployment and Service and wires the gateway's
// --token-service-grpc-addr / --token-service-http-url flags, preserving
// the §4.3 trust boundary.
//
// spec: §17.4 (C3 chart gates), §4.3 (token-service trust boundary)
func TestTokenServiceDefaultRendersDeploymentAndGatewayFlags(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       renderArgs(),
	})

	if _, ok := m.Find("Deployment", "lenny-token-service"); !ok {
		t.Errorf("tokenService.enabled defaults true: the lenny-token-service Deployment must render")
	}
	if _, ok := m.Find("Service", "lenny-token-service"); !ok {
		t.Errorf("tokenService.enabled defaults true: the lenny-token-service Service must render")
	}
	args := gatewayArgs(t, m)
	if !hasArg(args, "--token-service-grpc-addr") {
		t.Errorf("tokenService.enabled defaults true: the gateway must carry --token-service-grpc-addr")
	}
	if !hasArg(args, "--token-service-http-url") {
		t.Errorf("tokenService.enabled defaults true: the gateway must carry --token-service-http-url")
	}
}

// TestTokenServiceDisabledRemovesDeploymentAndGatewayFlags asserts that
// tokenService.enabled=false (the §17.4 development setting) renders no
// Token Service Deployment or Service and strips the gateway's
// --token-service-grpc-addr / --token-service-http-url flags, so the
// gateway falls back to its in-process MintLease / credassign dev path
// rather than dialing an absent Service. A leftover flag would make the
// gateway dial a Service that never schedules.
//
// spec: §17.4 (C3 chart gates), §4.3 (token-service trust boundary)
func TestTokenServiceDisabledRemovesDeploymentAndGatewayFlags(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	m := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       renderArgs("tokenService.enabled=false"),
	})

	if _, ok := m.Find("Deployment", "lenny-token-service"); ok {
		t.Errorf("tokenService.enabled=false must not render the lenny-token-service Deployment")
	}
	if _, ok := m.Find("Service", "lenny-token-service"); ok {
		t.Errorf("tokenService.enabled=false must not render the lenny-token-service Service")
	}
	args := gatewayArgs(t, m)
	if hasArg(args, "--token-service-grpc-addr") {
		t.Errorf("tokenService.enabled=false must strip the gateway --token-service-grpc-addr flag")
	}
	if hasArg(args, "--token-service-http-url") {
		t.Errorf("tokenService.enabled=false must strip the gateway --token-service-http-url flag")
	}
}

// TestGatewayBearerTrustMountGatedOnSecret asserts the gateway's
// --bearer-trust-hmac-key-file flag and the oidc-bearer-trust-key volume
// render only when security.oidc.bearerTrustKeySecret is set, mirroring
// the lenny-ops mount of the same Secret. When the value is empty
// (production default) the gateway renders no such flag or mount.
//
// spec: §17.4 (C3 chart gates), §10.2 (bearer-trust verifier)
func TestGatewayBearerTrustMountGatedOnSecret(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	// Default: no bearerTrustKeySecret -> no gateway flag or mount.
	def := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       renderArgs(),
	})
	if hasArg(gatewayArgs(t, def), "--bearer-trust-hmac-key-file") {
		t.Errorf("empty security.oidc.bearerTrustKeySecret must render no gateway --bearer-trust-hmac-key-file flag")
	}
	if gatewayHasVolume(t, def, "oidc-bearer-trust-key") {
		t.Errorf("empty security.oidc.bearerTrustKeySecret must render no gateway oidc-bearer-trust-key volume")
	}

	// Set: the gateway gains the flag, the volume, and the volumeMount.
	with := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       renderArgs("security.oidc.bearerTrustKeySecret=lenny-dev-bearer-key"),
	})
	if !hasArg(gatewayArgs(t, with), "--bearer-trust-hmac-key-file") {
		t.Errorf("set security.oidc.bearerTrustKeySecret must render the gateway --bearer-trust-hmac-key-file flag")
	}
	if !gatewayHasVolume(t, with, "oidc-bearer-trust-key") {
		t.Errorf("set security.oidc.bearerTrustKeySecret must render the gateway oidc-bearer-trust-key volume")
	}
	if !gatewayHasVolumeMount(t, with, "oidc-bearer-trust-key") {
		t.Errorf("set security.oidc.bearerTrustKeySecret must render the gateway oidc-bearer-trust-key volumeMount")
	}
}

// TestGatewayServiceTypeAndNodePort asserts the gateway Service defaults
// to ClusterIP and that gateway.service.type=NodePort with a fixed
// gateway.service.nodePort pins the node port on the http port, the path
// the §17.4 development profile uses for the loopback host-side
// forwarder.
//
// spec: §17.4 (C3 chart gates)
func TestGatewayServiceTypeAndNodePort(t *testing.T) {
	helm.SkipUnlessAvailable(t)

	def := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set:       renderArgs(),
	})
	svc := def.MustFind(t, "Service", "lenny-gateway")
	spec, _ := svc.Raw["spec"].(map[string]any)
	if got, _ := spec["type"].(string); got != "ClusterIP" {
		t.Errorf("gateway Service type defaults to ClusterIP, got %q", got)
	}

	np := helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Set: renderArgs(
			"gateway.service.type=NodePort",
			"gateway.service.nodePort=30080",
		),
	})
	svc = np.MustFind(t, "Service", "lenny-gateway")
	spec, _ = svc.Raw["spec"].(map[string]any)
	if got, _ := spec["type"].(string); got != "NodePort" {
		t.Errorf("gateway.service.type=NodePort must render type: NodePort, got %q", got)
	}
	ports, _ := spec["ports"].([]any)
	var httpNodePort int
	for _, p := range ports {
		pm, _ := p.(map[string]any)
		if name, _ := pm["name"].(string); name == "http" {
			httpNodePort = toInt(pm["nodePort"])
		}
	}
	if httpNodePort != 30080 {
		t.Errorf("gateway.service.nodePort=30080 must set the http port nodePort to 30080, got %d", httpNodePort)
	}
}

// gatewayHasVolume reports whether the lenny-gateway Deployment carries a
// pod volume of the given name.
func gatewayHasVolume(t *testing.T, m helm.Manifests, name string) bool {
	t.Helper()
	dep := m.MustFind(t, "Deployment", "lenny-gateway")
	spec, _ := dep.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	volumes, _ := podSpec["volumes"].([]any)
	for _, v := range volumes {
		vm, _ := v.(map[string]any)
		if n, _ := vm["name"].(string); n == name {
			return true
		}
	}
	return false
}

// gatewayHasVolumeMount reports whether the gateway container mounts a
// volume of the given name.
func gatewayHasVolumeMount(t *testing.T, m helm.Manifests, name string) bool {
	t.Helper()
	dep := m.MustFind(t, "Deployment", "lenny-gateway")
	spec, _ := dep.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		if name2, _ := cm["name"].(string); name2 != "gateway" {
			continue
		}
		mounts, _ := cm["volumeMounts"].([]any)
		for _, mnt := range mounts {
			mm, _ := mnt.(map[string]any)
			if n, _ := mm["name"].(string); n == name {
				return true
			}
		}
	}
	return false
}

// toInt coerces a YAML-decoded numeric value (int or float64) to int.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
