// SPDX-License-Identifier: MIT

package tier0_static

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/lenny/tests/testinfra/helm"
)

// devProfilePath is the §17.4 Embedded Mode development values file the
// `lenny up` build render layers on the chart and embeds under
// pkg/embedded/manifests/.
const devProfilePath = "../../charts/lenny/presets/dev.yaml"

// renderDevProfile renders the chart with the dev profile layered on the
// base values. The harness injects a global.spiffeTrustDomain placeholder
// only when no --set carries one; the dev profile sets it via -f, so the
// harness does not clobber it. The structural assertions here do not
// depend on the trust-domain value, which TestDevProfileFileValues pins
// directly from the file.
func renderDevProfile(t *testing.T) helm.Manifests {
	t.Helper()
	helm.SkipUnlessAvailable(t)
	return helm.Render(t, helm.Options{
		Chart:     "../../charts/lenny",
		Release:   "lenny",
		Namespace: "lenny-system",
		Values:    devProfilePath,
	})
}

// TestDevProfileDisablesAdmissionAndCertManager asserts the §17.4
// development profile renders no cert-manager.io/v1 object and no
// ValidatingWebhookConfiguration, the load-bearing property that lets the
// embedded cluster run with no cert-manager: a single leftover
// cert-manager object the dynamic applier cannot resolve on a bare k3s
// would abort bring-up.
//
// spec: §17.4 (dev profile)
func TestDevProfileDisablesAdmissionAndCertManager(t *testing.T) {
	m := renderDevProfile(t)

	if got := countCertManager(m); got != 0 {
		var names []string
		for _, x := range m {
			if x.APIVersion == certManagerGVR {
				names = append(names, x.Kind+"/"+x.Name)
			}
		}
		t.Errorf("dev profile must render no cert-manager.io/v1 object, got %d: %v", got, names)
	}
	if got := len(m.FindAll("ValidatingWebhookConfiguration")); got != 0 {
		t.Errorf("dev profile must render no ValidatingWebhookConfiguration, got %d", got)
	}
	if _, ok := m.Find("Issuer", "lenny-webhook-selfsign"); ok {
		t.Errorf("dev profile must not render the shared lenny-webhook-selfsign Issuer")
	}
}

// TestDevProfileDisablesTokenService asserts the dev profile renders no
// Token Service Deployment or Service and strips the gateway's
// --token-service-grpc-addr / --token-service-http-url flags, so the
// gateway uses its in-process MintLease / credassign dev path rather than
// dialing a Service whose image is absent from the embedded import bundle.
//
// spec: §17.4 (dev profile)
func TestDevProfileDisablesTokenService(t *testing.T) {
	m := renderDevProfile(t)

	if _, ok := m.Find("Deployment", "lenny-token-service"); ok {
		t.Errorf("dev profile must not render the lenny-token-service Deployment")
	}
	if _, ok := m.Find("Service", "lenny-token-service"); ok {
		t.Errorf("dev profile must not render the lenny-token-service Service")
	}
	args := gatewayArgs(t, m)
	if hasArg(args, "--token-service-grpc-addr") {
		t.Errorf("dev profile must strip the gateway --token-service-grpc-addr flag")
	}
	if hasArg(args, "--token-service-http-url") {
		t.Errorf("dev profile must strip the gateway --token-service-http-url flag")
	}
}

// TestDevProfileDisablesDedicatedCoreDNS asserts the dev profile renders
// none of the chart's dedicated CoreDNS workload (Deployment, Service,
// ConfigMap, PDB), so Embedded Mode relies on the k3s built-in CoreDNS.
// The dedicated CoreDNS enforces the agent-egress split-horizon DNS
// policy, part of the network-isolation surface not exercised locally.
//
// spec: §17.4 (dev profile)
func TestDevProfileDisablesDedicatedCoreDNS(t *testing.T) {
	m := renderDevProfile(t)

	if _, ok := m.Find("Deployment", "lenny-agent-dns"); ok {
		t.Errorf("dev profile must not render the dedicated lenny-agent-dns CoreDNS Deployment")
	}
	if _, ok := m.Find("Service", "lenny-agent-dns"); ok {
		t.Errorf("dev profile must not render the dedicated lenny-agent-dns CoreDNS Service")
	}
}

// TestDevProfileRendersRuncRuntimeClass asserts the dev profile renders
// exactly the runc RuntimeClass (handler runc) the echo pool's `standard`
// isolation profile resolves to, and renders neither the gvisor nor the
// kata RuntimeClass the local cluster has no runtime for. Without the runc
// RuntimeClass the WarmPoolController marks the echo pool Degraded and
// suppresses pod creation.
//
// spec: §17.4 (dev profile), §5.3 (isolation profiles / runc RuntimeClass)
func TestDevProfileRendersRuncRuntimeClass(t *testing.T) {
	m := renderDevProfile(t)

	rcs := m.FindAll("RuntimeClass")
	if len(rcs) != 1 {
		var names []string
		for _, rc := range rcs {
			names = append(names, rc.Name)
		}
		t.Fatalf("dev profile must render exactly one RuntimeClass (runc), got %d: %v", len(rcs), names)
	}
	rc := rcs[0]
	if rc.Name != "runc" {
		t.Errorf("dev profile RuntimeClass name = %q, want runc", rc.Name)
	}
	if handler, _ := rc.Raw["handler"].(string); handler != "runc" {
		t.Errorf("dev profile RuntimeClass handler = %q, want runc", handler)
	}
	// The gvisor and kata RuntimeClasses must not render: the local
	// cluster ships no gVisor or Kata runtime handler.
	for _, name := range []string{"gvisor", "kata"} {
		if _, ok := m.Find("RuntimeClass", name); ok {
			t.Errorf("dev profile must not render the %s RuntimeClass", name)
		}
	}
}

// TestDevProfileGatewayNodePortAndBearerTrust asserts the gateway Service
// is a NodePort on the fixed dev node port (the loopback host-side
// forwarder targets it) and that the gateway carries the
// --bearer-trust-hmac-key-file flag and the oidc-bearer-trust-key mount,
// so the in-cluster gateway trusts the CLI's minted dev bearer.
//
// spec: §17.4 (dev profile)
func TestDevProfileGatewayNodePortAndBearerTrust(t *testing.T) {
	m := renderDevProfile(t)

	svc := m.MustFind(t, "Service", "lenny-gateway")
	spec, _ := svc.Raw["spec"].(map[string]any)
	if got, _ := spec["type"].(string); got != "NodePort" {
		t.Errorf("dev profile gateway Service type = %q, want NodePort", got)
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
		t.Errorf("dev profile gateway http nodePort = %d, want 30080", httpNodePort)
	}

	args := gatewayArgs(t, m)
	if !hasArg(args, "--bearer-trust-hmac-key-file") {
		t.Errorf("dev profile must wire the gateway --bearer-trust-hmac-key-file flag for the dev bearer")
	}
	if !gatewayHasVolume(t, m, "oidc-bearer-trust-key") {
		t.Errorf("dev profile must mount the oidc-bearer-trust-key volume on the gateway")
	}
	if !gatewayHasVolumeMount(t, m, "oidc-bearer-trust-key") {
		t.Errorf("dev profile must mount oidc-bearer-trust-key into the gateway container")
	}
}

// TestDevProfileSingleReplicaControlPlane asserts the dev profile pins
// every control-plane Deployment to a single replica and disables the
// gateway PodDisruptionBudget, so the single-node embedded cluster
// schedules without a second replica or a PDB that a one-replica
// Deployment cannot satisfy.
//
// spec: §17.4 (dev profile)
func TestDevProfileSingleReplicaControlPlane(t *testing.T) {
	m := renderDevProfile(t)

	for _, name := range []string{"lenny-gateway", "lenny-controller", "lenny-ops"} {
		dep := m.MustFind(t, "Deployment", name)
		spec, _ := dep.Raw["spec"].(map[string]any)
		if got := toInt(spec["replicas"]); got != 1 {
			t.Errorf("dev profile %s replicas = %d, want 1", name, got)
		}
	}
	if _, ok := m.Find("PodDisruptionBudget", "lenny-gateway"); ok {
		t.Errorf("dev profile must not render the gateway PodDisruptionBudget at a single replica")
	}
}

// TestDevProfileTagBasedImagesAndAdapter asserts the control-plane
// Deployments carry concrete tag-based image references with
// pullPolicy IfNotPresent (the locally-imported image is never re-pulled)
// and that the controller is wired with the --adapter-image flag the
// sidecar-model walkthrough needs.
//
// spec: §17.4 (dev profile)
func TestDevProfileTagBasedImagesAndAdapter(t *testing.T) {
	m := renderDevProfile(t)

	wantImages := map[string]string{
		"lenny-gateway":    "ghcr.io/lennylabs/lenny-gateway:0.1.0",
		"lenny-controller": "ghcr.io/lennylabs/lenny-controller:0.1.0",
		"lenny-ops":        "ghcr.io/lennylabs/lenny-ops:0.1.0",
	}
	for dep, wantImage := range wantImages {
		image, policy := primaryContainerImage(t, m, dep)
		if image != wantImage {
			t.Errorf("dev profile %s image = %q, want %q", dep, image, wantImage)
		}
		if policy != "IfNotPresent" {
			t.Errorf("dev profile %s imagePullPolicy = %q, want IfNotPresent", dep, policy)
		}
	}

	args := controllerArgs(t, m)
	if !hasArg(args, "--adapter-image") {
		t.Errorf("dev profile must wire the controller --adapter-image flag for sidecar-model pods")
	}
}

// TestDevProfileNoStorePods asserts the dev profile deploys no store
// workloads: no in-cluster Postgres (CloudNativePG Cluster), no Redis
// Sentinel/Cluster, and no MinIO StatefulSet, so the gateway and
// controller run on the in-process in-memory backends.
//
// spec: §17.4 (dev profile)
func TestDevProfileNoStorePods(t *testing.T) {
	m := renderDevProfile(t)

	for _, kind := range []string{"Cluster", "StatefulSet"} {
		if got := len(m.FindAll(kind)); got != 0 {
			var names []string
			for _, x := range m.FindAll(kind) {
				names = append(names, x.Name)
			}
			t.Errorf("dev profile must render no %s (store workload), got %d: %v", kind, got, names)
		}
	}
	// The pool-scaling controller renders only when postgres.dsn is set;
	// the dev profile sets no DSN, so it must be absent.
	if _, ok := m.Find("Deployment", "lenny-pool-scaling-controller"); ok {
		t.Errorf("dev profile must not render the lenny-pool-scaling-controller (no Postgres DSN)")
	}
}

// TestDevProfileFileValues parses the dev profile file directly and pins
// the values a `--set` placeholder in the render harness would otherwise
// clobber (global.spiffeTrustDomain) or that are not visible as a single
// rendered field (global.devMode, the fixed bearer-trust Secret name, the
// single fixed agent namespace).
//
// spec: §17.4 (dev profile)
func TestDevProfileFileValues(t *testing.T) {
	raw, err := os.ReadFile(devProfilePath)
	if err != nil {
		t.Fatalf("read dev profile: %v", err)
	}
	var v struct {
		Global struct {
			DevMode           bool   `yaml:"devMode"`
			SpiffeTrustDomain string `yaml:"spiffeTrustDomain"`
		} `yaml:"global"`
		AgentNamespaces []struct {
			Name string `yaml:"name"`
		} `yaml:"agentNamespaces"`
		Security struct {
			OIDC struct {
				BearerTrustKeySecret string `yaml:"bearerTrustKeySecret"`
			} `yaml:"oidc"`
		} `yaml:"security"`
		Controller struct {
			AdapterImage string `yaml:"adapterImage"`
		} `yaml:"controller"`
	}
	if err := yaml.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse dev profile: %v", err)
	}

	if !v.Global.DevMode {
		t.Errorf("dev profile must set global.devMode true (precondition for --bearer-trust-hmac-key-file)")
	}
	if v.Global.SpiffeTrustDomain == "" {
		t.Errorf("dev profile must set a fixed global.spiffeTrustDomain (REQUIRED chart value)")
	}
	if len(v.AgentNamespaces) != 1 {
		t.Errorf("dev profile must pin exactly one agent namespace, got %d", len(v.AgentNamespaces))
	}
	if v.Security.OIDC.BearerTrustKeySecret == "" {
		t.Errorf("dev profile must set security.oidc.bearerTrustKeySecret to a fixed dev Secret name")
	}
	if v.Controller.AdapterImage == "" {
		t.Errorf("dev profile must set controller.adapterImage to the imported lenny-adapter tag")
	}
}

// primaryContainerImage returns the image and imagePullPolicy of the
// named Deployment's primary (component-named) container. The component
// container is the one whose name matches the Deployment's component
// suffix, falling back to the first container.
func primaryContainerImage(t *testing.T, m helm.Manifests, depName string) (image, pullPolicy string) {
	t.Helper()
	dep := m.MustFind(t, "Deployment", depName)
	containers := podContainers(dep)
	// Prefer a container whose name matches the workload role; controllers
	// and the gateway name their main container after the role.
	wantNames := map[string]string{
		"lenny-gateway":    "gateway",
		"lenny-controller": "controller",
		"lenny-ops":        "ops",
	}
	want := wantNames[depName]
	for _, c := range containers {
		cm, _ := c.(map[string]any)
		if n, _ := cm["name"].(string); n == want {
			image, _ = cm["image"].(string)
			pullPolicy, _ = cm["imagePullPolicy"].(string)
			return image, pullPolicy
		}
	}
	if len(containers) > 0 {
		cm, _ := containers[0].(map[string]any)
		image, _ = cm["image"].(string)
		pullPolicy, _ = cm["imagePullPolicy"].(string)
	}
	return image, pullPolicy
}

// podContainers returns the pod-spec containers list of a Deployment.
func podContainers(dep helm.Manifest) []any {
	spec, _ := dep.Raw["spec"].(map[string]any)
	tmpl, _ := spec["template"].(map[string]any)
	podSpec, _ := tmpl["spec"].(map[string]any)
	containers, _ := podSpec["containers"].([]any)
	return containers
}

// controllerArgs extracts the --flag args of the lenny-controller
// Deployment's controller container.
func controllerArgs(t *testing.T, m helm.Manifests) []string {
	t.Helper()
	dep := m.MustFind(t, "Deployment", "lenny-controller")
	for _, c := range podContainers(dep) {
		cm, _ := c.(map[string]any)
		if name, _ := cm["name"].(string); name != "controller" {
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
	t.Fatalf("lenny-controller Deployment has no controller container")
	return nil
}
