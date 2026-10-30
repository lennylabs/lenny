// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the §13.2 lenny-system NetworkPolicy
// posture. The tests install the Lenny control plane on a Kind cluster
// (via the install.sh-backed kind.InstallLenny harness) and assert the
// §13.2 default-deny plus per-component allow-list behaviour against the
// live cluster.
//
// The adversarial test schedules a throwaway probe pod in lenny-system
// that carries none of the lenny.dev/component allow-list labels and
// asserts the default-deny-all NetworkPolicy blocks its egress to the
// gateway. The CNI under test is kindnet, which enforces NetworkPolicy
// including egress rules on this cluster. As a positive control a
// second probe pod carrying lenny.dev/component: bootstrap is permitted
// to reach the gateway, because allow-bootstrap-egress (egress) and
// allow-gateway-ingress (ingress) both admit that label.

package tier9_security_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// lennySystemNS is the namespace the Lenny control plane runs in.
const lennySystemNS = "lenny-system"

// probeImage is the probe-pod container image. curlimages/curl is a
// busybox-based image with a shell (so `kubectl exec ... sh -c` works)
// and is already loaded on the lenny-e2e-worker node, so the probe pods
// run with imagePullPolicy: Never and never reach a remote registry.
const probeImage = "curlimages/curl:8.11.0"

// probeNode is the node the curl image is loaded on. The probe pods are
// pinned here with spec.nodeName so the kubelet finds the image locally
// rather than attempting (and failing) a pull on another node.
const probeNode = "lenny-e2e-worker"

// probeRunAsUser is the numeric UID curlimages/curl runs as. The image
// declares a non-numeric user (curl_user), so runAsNonRoot: true needs
// an explicit numeric runAsUser to satisfy the kubelet's root check.
const probeRunAsUser = 100

// expectedSystemPolicies names the §13.2 NetworkPolicy resources the
// chart renders into lenny-system: the default-deny baseline plus the
// per-component allow-lists for every control-plane component the
// dev-mode e2e install runs.
var expectedSystemPolicies = []string{
	"default-deny-all",
	"allow-gateway-ingress",
	"allow-gateway-egress",
	"allow-controller-egress",
	"allow-token-service",
	"allow-admission-webhooks",
	// allow-deletion-guard-gateway-egress is the §13.2 sub-rule (c)
	// per-webhook egress policy the deletion-guard needs to reach the
	// gateway GET /internal/runtime-upgrade/active probe. It renders
	// unconditionally under admissionWebhooks.enabled (unlike
	// allow-drain-readiness-gateway-egress, which is additionally gated
	// on features.drainReadiness and so is absent from the dev-mode
	// install this suite runs). F-13.2.24.
	"allow-deletion-guard-gateway-egress",
}

// spec: 13.2
// diagnosis: §13.2 NetworkPolicy isolation is not enforced. The test
// schedules a probe pod in lenny-system with none of the
// lenny.dev/component allow-list labels and asserts default-deny-all
// blocks its egress to the gateway (curl times out). A bootstrap-
// labelled probe is the positive control and must reach the gateway. A
// failure means the CNI is not enforcing egress, default-deny-all is
// missing, or an allow-list is over-broad.
func TestNetworkPolicyAdversarial(t *testing.T) {
	c := kind.InstallLenny(t)

	gatewayIP := serviceClusterIP(t, c, "lenny-gateway")
	if gatewayIP == "" {
		t.Fatalf("the lenny-gateway Service has no ClusterIP; cannot probe NetworkPolicy enforcement")
	}

	// Positive control first: a probe pod whose lenny.dev/component
	// label is `bootstrap`. allow-bootstrap-egress permits its egress to
	// the gateway on TCP 8080 and allow-gateway-ingress admits bootstrap
	// pods on 8080, so the connection must succeed. Running this first
	// proves the probe image, the node pinning, and the gateway target
	// are sound, so a later block on the unlabelled pod is attributable
	// to the NetworkPolicy and not to a broken probe.
	allowedPod := networkProbePod("np-probe-allowed", map[string]string{
		"lenny.dev/component": "bootstrap",
	})
	createProbePod(t, c, allowedPod)
	allowedTarget := fmt.Sprintf("http://%s:8080/", gatewayIP)
	res := curlFromPod(t, c, "np-probe-allowed", allowedTarget, 8*time.Second)
	if res.exitCode != 0 {
		t.Fatalf("positive control failed: a lenny.dev/component=bootstrap probe pod could not reach the "+
			"gateway at %s (curl exit %d). allow-bootstrap-egress and allow-gateway-ingress should permit "+
			"this path.\noutput:\n%s", allowedTarget, res.exitCode, res.output)
	}
	t.Logf("positive control: bootstrap-labelled probe reached the gateway at %s (curl exit 0, %s)",
		allowedTarget, strings.TrimSpace(res.output))

	// The adversarial probe: a pod carrying none of the
	// lenny.dev/component allow-list labels. Only default-deny-all
	// selects it (its podSelector is {}), and default-deny-all denies
	// all egress. Its connection to the gateway must time out at the CNI
	// layer. Probe both the HTTP admin port (8080) and the gRPC control
	// port (50051): default-deny blocks egress regardless of port, and
	// covering both rules out a port-specific allow leak.
	unlabelledPod := networkProbePod("np-probe-unlabelled", nil)
	createProbePod(t, c, unlabelledPod)
	for _, port := range []string{"8080", "50051"} {
		target := fmt.Sprintf("http://%s:%s/", gatewayIP, port)
		res := curlFromPod(t, c, "np-probe-unlabelled", target, 8*time.Second)
		if res.exitCode == 0 {
			t.Fatalf("§13.2 violation: an unlabelled probe pod reached the gateway at %s. "+
				"default-deny-all must block egress from a pod that carries no lenny.dev/component "+
				"allow-list label.\noutput:\n%s", target, res.output)
		}
		// curl exit 28 is the timeout this test wants — the CNI dropped
		// the SYN and the connect never completed. Exit 7 (connection
		// refused) would mean the packet reached a host that actively
		// rejected it, which is not a NetworkPolicy block; flag it.
		if res.exitCode != 28 {
			t.Errorf("unlabelled probe to %s failed with curl exit %d, expected 28 (connection timed out). "+
				"A non-timeout failure is not a clean CNI egress block.\noutput:\n%s",
				target, res.exitCode, res.output)
			continue
		}
		t.Logf("adversarial probe: unlabelled pod blocked reaching the gateway at %s "+
			"(curl exit 28, connection timed out — default-deny-all enforced)", target)
	}
}

// gatewayInternalPort is the gateway's internal HTTP port (§13.2,
// values.yaml gateway.internalPort default 8080). It serves
// GET /internal/runtime-upgrade/active, the endpoint the
// sandboxtemplate-deletion-guard webhook probes before admitting a
// SandboxTemplate DELETE. The §13.2 sub-rule (c) gateway-egress grant
// is narrowed to this port for the deletion-guard pods only.
const gatewayInternalPort = "8080"

// deletionGuardWebhookName is the additive per-webhook label value the
// §13.2 sub-rule (c) selector pairs with the canonical
// lenny.dev/component: admission-webhook key to narrow the
// gateway-internal-port egress to the deletion-guard pods alone
// (NET-068). It matches the egressLabel the chart stamps via
// lenny.admissionWebhookWorkload.
const deletionGuardWebhookName = "sandboxtemplate-deletion-guard"

// spec: 13.2 (NET-068 least-privilege additive-label narrowing, sub-rule (c)),
// 17.2 (only lenny-drain-readiness and lenny-sandboxtemplate-deletion-guard
// carry the additive gateway-egress webhook-name label)
// diagnosis: the §13.2 sub-rule (c) gateway-egress grant is broader than
// the deletion-guard alone, or the additive-label narrowing is not
// enforced by the CNI. The test schedules two probe pods in lenny-system,
// both carrying the canonical lenny.dev/component: admission-webhook label
// so allow-gateway-ingress admits them on the internal port. The first
// pod carries ONLY that label — an in-process webhook without the
// deletion-guard webhook-name — and allow-deletion-guard-gateway-egress
// must NOT select it, so the lenny-system default-deny drops its egress
// to the gateway internal port and its curl times out (exit 28). The
// second pod additionally carries lenny.dev/webhook-name:
// sandboxtemplate-deletion-guard, so allow-deletion-guard-gateway-egress
// selects it and its egress to the internal port is permitted (curl exit
// 0). A failure means sub-rule (c) grants gateway egress to every
// admission-webhook pod (broadening least privilege past the deletion-
// guard) or the egress policy is missing entirely, so the deletion-guard
// itself cannot reach the probe.
func TestDeletionGuardGatewayEgressLeastPrivilege(t *testing.T) {
	c := kind.InstallLenny(t)

	gatewayIP := serviceClusterIP(t, c, "lenny-gateway")
	if gatewayIP == "" {
		t.Fatalf("the lenny-gateway Service has no ClusterIP; cannot probe sub-rule (c) narrowing")
	}
	target := fmt.Sprintf("http://%s:%s/internal/runtime-upgrade/active", gatewayIP, gatewayInternalPort)

	// Positive control: a probe pod carrying the paired canonical +
	// additive labels the deletion-guard pods carry. This is the exact
	// selector allow-deletion-guard-gateway-egress matches, so its egress
	// to the gateway internal port must be permitted. Running it first
	// proves the egress policy, the ingress rule, and the target port are
	// sound, so the block on the component-only pod below is attributable
	// to the missing webhook-name label rather than a broken probe or a
	// wrong port.
	guardPod := networkProbePod("np-probe-deletion-guard", map[string]string{
		"lenny.dev/component":    "admission-webhook",
		"lenny.dev/webhook-name": deletionGuardWebhookName,
	})
	createProbePod(t, c, guardPod)
	res := curlFromPod(t, c, "np-probe-deletion-guard", target, 8*time.Second)
	if res.exitCode != 0 {
		t.Fatalf("sub-rule (c) positive control failed: a probe pod carrying the paired "+
			"lenny.dev/component=admission-webhook + lenny.dev/webhook-name=%s labels could not reach the "+
			"gateway internal port at %s (curl exit %d). allow-deletion-guard-gateway-egress must permit "+
			"the deletion-guard's runtime-upgrade probe.\noutput:\n%s",
			deletionGuardWebhookName, target, res.exitCode, res.output)
	}
	t.Logf("sub-rule (c) positive control: deletion-guard-labelled probe reached the gateway internal "+
		"port at %s (curl exit 0, %s)", target, strings.TrimSpace(res.output))

	// The adversarial probe: a pod carrying ONLY the canonical
	// lenny.dev/component: admission-webhook label — an in-process webhook
	// (pod-security, sandboxclaim-guard, etc.) that lacks the deletion-
	// guard webhook-name. allow-admission-webhooks grants it base egress
	// (CoreDNS) but NOT gateway egress, and allow-deletion-guard-gateway-
	// egress does not select it (no matching webhook-name), so the
	// lenny-system default-deny drops its egress to the gateway internal
	// port. Its connection must time out at the CNI layer (curl exit 28).
	componentOnlyPod := networkProbePod("np-probe-webhook-nolabel", map[string]string{
		"lenny.dev/component": "admission-webhook",
	})
	createProbePod(t, c, componentOnlyPod)
	res = curlFromPod(t, c, "np-probe-webhook-nolabel", target, 8*time.Second)
	if res.exitCode == 0 {
		t.Fatalf("§13.2 sub-rule (c) violation: an admission-webhook probe pod WITHOUT the "+
			"lenny.dev/webhook-name=%s label reached the gateway internal port at %s. sub-rule (c) "+
			"must narrow gateway-internal-port egress to the deletion-guard pods only; granting it to "+
			"every admission-webhook pod broadens least privilege (NET-068).\noutput:\n%s",
			deletionGuardWebhookName, target, res.output)
	}
	// curl exit 28 is the timeout this test wants — the CNI dropped the
	// SYN because no egress policy selected the component-only pod for the
	// gateway internal port. Exit 7 (connection refused) would mean the
	// packet reached a host that actively rejected it, which is not a
	// NetworkPolicy block; flag it.
	if res.exitCode != 28 {
		t.Errorf("component-only admission-webhook probe to %s failed with curl exit %d, expected 28 "+
			"(connection timed out). A non-timeout failure is not a clean CNI egress block.\noutput:\n%s",
			target, res.exitCode, res.output)
	} else {
		t.Logf("sub-rule (c) adversarial probe: component-only admission-webhook pod blocked reaching "+
			"the gateway internal port at %s (curl exit 28 — least privilege preserved)", target)
	}
}

// spec: 13.2
// diagnosis: the §13.2 lenny-system NetworkPolicy inventory is
// incomplete or default-deny-all is mis-scoped. The test asserts every
// expected lenny-system NetworkPolicy exists and that default-deny-all
// selects all pods (empty podSelector) for both Ingress and Egress. A
// missing policy or a default-deny-all that omits a policyType leaves a
// least-privilege gap the per-component allow-lists assume is closed.
func TestNetworkPolicyPosture(t *testing.T) {
	c := kind.InstallLenny(t)

	present := systemNetworkPolicyNames(t, c)
	for _, name := range expectedSystemPolicies {
		if !present[name] {
			t.Errorf("§13.2 NetworkPolicy %q is not installed in lenny-system; "+
				"a least-privilege network rule is missing", name)
		}
	}

	// default-deny-all is the baseline every allow-list narrows. It must
	// select every pod in the namespace (an empty podSelector) and apply
	// to both directions, or a pod could egress or ingress freely.
	selector := jsonpathOut(t, c, "networkpolicy", "default-deny-all",
		"{.spec.podSelector}")
	if strings.TrimSpace(selector) != "{}" {
		t.Errorf("§13.2 default-deny-all has podSelector %q; it must be empty ({}) so the policy "+
			"selects every pod in lenny-system", selector)
	}
	policyTypes := jsonpathOut(t, c, "networkpolicy", "default-deny-all",
		"{.spec.policyTypes}")
	for _, want := range []string{"Ingress", "Egress"} {
		if !strings.Contains(policyTypes, want) {
			t.Errorf("§13.2 default-deny-all policyTypes %q is missing %q; the baseline must deny "+
				"both directions", policyTypes, want)
		}
	}
	t.Logf("§13.2 posture: %d lenny-system NetworkPolicies present, default-deny-all selects all "+
		"pods for %s", len(present), strings.TrimSpace(policyTypes))
}

// curlResult holds the outcome of a curl run inside a probe pod.
type curlResult struct {
	exitCode int
	output   string
}

// networkProbePod renders a probe-pod manifest. The pod sleeps so the
// test can kubectl-exec curl into it; it is pinned to probeNode (where
// the curl image is loaded) and runs with the §13.1 hardened
// securityContext so the lenny-pod-security webhook — which is not
// scoped to lenny-system — does not interfere. The optional labels add
// a lenny.dev/component value to bring an allow-list policy into scope.
func networkProbePod(name string, labels map[string]string) string {
	var labelLines strings.Builder
	labelLines.WriteString("    lenny.dev/test: netpol-probe\n")
	for k, v := range labels {
		fmt.Fprintf(&labelLines, "    %s: %q\n", k, v)
	}
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
%sspec:
  nodeName: %s
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  containers:
    - name: probe
      image: %s
      imagePullPolicy: Never
      command: ["sleep", "600"]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: %d
        runAsGroup: %d
        capabilities:
          drop: ["ALL"]
`, name, lennySystemNS, labelLines.String(), probeNode, probeImage, probeRunAsUser, probeRunAsUser)
}

// createProbePod applies a probe-pod manifest, registers a t.Cleanup to
// delete it, and waits for the pod to become Ready. A pod that never
// becomes Ready fails the test, because the connectivity probe that
// follows would otherwise misreport a scheduling failure as a
// NetworkPolicy block.
func createProbePod(t *testing.T, c *kind.Cluster, manifest string) {
	t.Helper()
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })

	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create probe pod: %v\n%s", err, out)
	}
	name := probePodName(manifest)
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "wait", "--for=condition=Ready",
		"pod/"+name, "--timeout=90s",
	)
	if err != nil {
		desc, _ := c.KubectlOut(t, "-n", lennySystemNS, "describe", "pod", name)
		t.Fatalf("probe pod %q did not become Ready: %v\n%s\n--- describe ---\n%s",
			name, err, out, desc)
	}
}

// probePodName extracts the metadata.name line from a probe-pod
// manifest. The manifests this file builds always carry `  name: <n>`
// as the first indented name field.
func probePodName(manifest string) string {
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "name:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// curlFromPod runs curl inside the named probe pod (in lenny-system)
// against target with a bounded connect timeout and returns curl's exit
// code and combined output. The -m timeout caps how long a CNI-dropped
// connection hangs, so a blocked path returns curl exit 28 within the
// bound rather than stalling the test.
func curlFromPod(t *testing.T, c *kind.Cluster, pod, target string, timeout time.Duration) curlResult {
	t.Helper()
	return curlFromPodInNamespace(t, c, lennySystemNS, pod, target, timeout)
}

// curlFromPodInNamespace is curlFromPod generalized to an arbitrary
// namespace. agent_egress_test.go reuses it against lenny-agents (and a
// throwaway sibling-tenant namespace) for the TESTING.md §12.9.4 agent-pod egress
// probes.
func curlFromPodInNamespace(t *testing.T, c *kind.Cluster, namespace, pod, target string, timeout time.Duration) curlResult {
	t.Helper()
	secs := int(timeout.Seconds())
	// curl prints the connect result; `echo exit=$?` carries the curl
	// exit status out of the exec so the test can distinguish a timeout
	// (28) from a refused connection (7) from a success (0).
	script := fmt.Sprintf(
		"curl -sS -m %d -o /dev/null -w 'http=%%{http_code}' %s 2>&1; echo \" exit=$?\"",
		secs, target,
	)
	out, _ := c.KubectlOut(
		t,
		"-n", namespace, "exec", pod, "--", "sh", "-c", script,
	)
	return curlResult{exitCode: parseCurlExit(out), output: out}
}

// parseCurlExit extracts the integer N from the `exit=N` marker
// curlFromPod's script appends. A missing or unparseable marker yields
// -1, which no caller treats as success.
func parseCurlExit(out string) int {
	idx := strings.LastIndex(out, "exit=")
	if idx < 0 {
		return -1
	}
	field := strings.TrimSpace(out[idx+len("exit="):])
	if i := strings.IndexFunc(field, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		field = field[:i]
	}
	n := 0
	if _, err := fmt.Sscanf(field, "%d", &n); err != nil {
		return -1
	}
	return n
}

// serviceClusterIP returns the .spec.clusterIP of the named Service in
// lenny-system, or "" when the read fails.
func serviceClusterIP(t *testing.T, c *kind.Cluster, service string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "get", "service", service,
		"-o", "jsonpath={.spec.clusterIP}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// systemNetworkPolicyNames returns the set of NetworkPolicy names in
// lenny-system.
func systemNetworkPolicyNames(t *testing.T, c *kind.Cluster) map[string]bool {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "get", "networkpolicy",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("list NetworkPolicies in lenny-system: %v\n%s", err, out)
	}
	names := map[string]bool{}
	for _, name := range strings.Fields(out) {
		names[name] = true
	}
	return names
}

// jsonpathOut runs a single jsonpath query against a named resource in
// lenny-system and returns the trimmed output. A query error fails the
// test, since every caller depends on the resource existing.
func jsonpathOut(t *testing.T, c *kind.Cluster, kind, name, jsonpath string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNS, "get", kind, name, "-o", "jsonpath="+jsonpath,
	)
	if err != nil {
		t.Fatalf("query %s on %s/%s: %v\n%s", jsonpath, kind, name, err, out)
	}
	return strings.TrimSpace(out)
}

// holderForwardPort is the chart default of gateway.holderForwardPort, the
// CH-HOLDERFORWARD inter-replica forward port (§10.1.1, §13.2.4).
const holderForwardPort = 50054

// gatewayPodSelector selects gateway pods by both labels the lenny-gateway
// Service selects on, so the probe target is a gateway pod rather than a
// pod that carries only the component label.
const gatewayPodSelector = "app.kubernetes.io/name=lenny,lenny.dev/component=gateway"

// gatewayPeerOnlyProbeLabels carries only the component label the
// CH-HOLDERFORWARD self-peer rules select, so the NetworkPolicy treats the
// probe pod as a gateway peer while the lenny-gateway Service does not
// select it.
var gatewayPeerOnlyProbeLabels = map[string]string{"lenny.dev/component": "gateway"}

// spec: 13.2.4 (Gateway row), 28.5.4 (Inter-replica), 10.3 (mTLS PKI)
// diagnosis: a failure in the gateway-peer case means allow-gateway-egress
// or allow-gateway-ingress drops a gateway pod's connection to a peer
// gateway pod on the CH-HOLDERFORWARD port, so every off-holder forward
// fails as not delivered. A failure in the default-namespace case means a
// pod outside the gateway reaches the forward port, so the §13.2.4 Gateway
// row's gateway-only ingress on that port is not enforced. The Kind install
// runs no forward listener, so a peer probe that the policy admits ends in
// a refused connection (curl exit 7), and only a dropped one times out
// (curl exit 28).
func TestHolderForwardPortAdmitsOnlyGatewayPeers_spec_13_2_4(t *testing.T) {
	c := kind.InstallLenny(t)

	gatewayIP := podIPBySelector(t, c, lennySystemNS, gatewayPodSelector)
	if gatewayIP == "" {
		t.Fatalf("no gateway pod IP found for %s; cannot probe the CH-HOLDERFORWARD port", gatewayPodSelector)
	}
	target := fmt.Sprintf("http://%s:%d/", gatewayIP, holderForwardPort)

	t.Run("gateway peer in lenny-system is admitted", func(t *testing.T) {
		createProbePod(t, c, networkProbePod("np-probe-gateway-peer", gatewayPeerOnlyProbeLabels))
		res := curlFromPod(t, c, "np-probe-gateway-peer", target, 8*time.Second)
		if res.exitCode == 28 {
			t.Fatalf("§13.2.4 violation: a lenny-system probe carrying lenny.dev/component=gateway timed out "+
				"reaching the CH-HOLDERFORWARD port at %s. allow-gateway-egress and allow-gateway-ingress must "+
				"admit a gateway peer on that port.\noutput:\n%s", target, res.output)
		}
		t.Logf("gateway-peer probe reached the CH-HOLDERFORWARD port at %s (curl exit %d, not dropped)",
			target, res.exitCode)
	})

	t.Run("default-namespace pod is dropped", func(t *testing.T) {
		createProbeInNamespace(t, c, defaultNS, "hf-default-src", nil)
		requireDefaultNamespaceEgress(t, c, "hf-default-src")
		res := curlFromNS(t, c, defaultNS, "hf-default-src", target, 8*time.Second)
		if res.exitCode != 28 {
			t.Fatalf("§13.2.4 violation: a default-namespace probe reaching the CH-HOLDERFORWARD port at %s "+
				"ended with curl exit %d, want 28 (connection timed out). Only gateway pods may reach the "+
				"forward port; a refused or completed connection means the packet reached the gateway pod.\n"+
				"output:\n%s", target, res.exitCode, res.output)
		}
		t.Logf("default-namespace probe dropped at the CH-HOLDERFORWARD port at %s (curl exit 28)", target)
	})
}

// requireDefaultNamespaceEgress is the egress positive control the
// lenny-ops ingress case uses: the default-namespace probe must reach the
// CoreDNS metrics endpoint, so a later timeout is attributable to the
// target's ingress policy rather than to a probe with no working egress.
func requireDefaultNamespaceEgress(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	dnsIP := podIPBySelector(t, c, "kube-system", "k8s-app=kube-dns")
	if dnsIP == "" {
		t.Fatalf("no CoreDNS pod IP found; cannot establish the egress positive control")
	}
	dnsTarget := fmt.Sprintf("http://%s:9153/metrics", dnsIP)
	res := curlFromNS(t, c, defaultNS, pod, dnsTarget, 8*time.Second)
	if res.exitCode != 0 {
		t.Fatalf("egress positive control failed: the default-namespace probe could not reach CoreDNS at %s "+
			"(curl exit %d), so a timeout on the target cannot be attributed to its ingress policy.\noutput:\n%s",
			dnsTarget, res.exitCode, res.output)
	}
}

// callingSessionHeader is the gateway-internal header that names the
// calling session of a forwarded platform tool call. Only the
// CH-HOLDERFORWARD listener reads it.
const callingSessionHeader = "X-Lenny-Calling-Session"

// publicRequestAs sends one request to the public listener through d's
// port-forward under the dev-mode identity of user in tenant, with extra
// headers, and returns the status and body. The identity carries the
// tenant-scoped platform-admin role the sessiondriver session calls use, so
// a refusal comes from the tenant scope rather than from a missing
// permission.
func publicRequestAs(ctx context.Context, t *testing.T, d *sessiondriver.Driver, method, path, tenant, user string, body string, extra map[string]string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.BaseURL()+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("X-Lenny-Tenant-ID", tenant)
	req.Header.Set("X-Lenny-Roles", "platform-admin")
	req.Header.Set("X-Lenny-User-ID", user)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

// spec: 29.3 (Interactive message send) Forwarding rule 3, 13.2.4 (Gateway row), 28.5.4 (Inter-replica)
// diagnosis: a failure means the public listener honored the
// gateway-internal calling-session header and ran a request from another
// tenant's user under the named session's principal, so any client could
// act as any session by naming it. Only the CH-HOLDERFORWARD listener,
// which admits gateway peers alone, may read that header.
func TestPublicListenerDoesNotRunARequestAsTheCallingSession_spec_29_3(t *testing.T) {
	d := sessiondriver.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	suffix := fmt.Sprintf("-%d", time.Now().UnixNano())
	owner, intruder := "t9-callsess-acme"+suffix, "t9-callsess-globex"+suffix
	for _, tenant := range []string{owner, intruder} {
		if err := d.BootstrapTenant(ctx, tenant); err != nil {
			t.Fatalf("bootstrap tenant %s: %v", tenant, err)
		}
	}
	if err := d.AllowSessionsWithNoEnvironment(ctx, owner); err != nil {
		t.Fatalf("allow sessions with no environment for %s: %v", owner, err)
	}
	sess, err := d.CreateAndStart(ctx, owner, sessiondriver.EchoRuntimeSidecar)
	if errors.Is(err, sessiondriver.ErrPoolNotReady) {
		t.Skipf("precondition not met: warm pool not ready, no session to name: %v", err)
	}
	if err != nil {
		t.Fatalf("create-and-start for %s: %v", owner, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, owner, sess.ID)
	})
	// Control: the owner reads its own session, so a 404 below is the
	// intruder's refusal rather than a missing session.
	if _, err := d.GetSession(ctx, owner, sess.ID); err != nil {
		t.Fatalf("control: %s could not read its own session %s: %v", owner, sess.ID, err)
	}

	naming := map[string]string{callingSessionHeader: sess.ID}
	path := "/v1/sessions/" + sess.ID
	if code, body := publicRequestAs(ctx, t, d, http.MethodGet, path, intruder, "bob", "", naming); code != http.StatusNotFound {
		t.Errorf("GET %s from tenant %s naming the session in %s = %d %s, want 404: the public listener "+
			"must not run the request as the calling session", path, intruder, callingSessionHeader, code, body)
	}
	msg := `{"messages":[{"role":"user","content":"calling-session probe"}]}`
	if code, body := publicRequestAs(ctx, t, d, http.MethodPost, path+"/messages", intruder, "bob", msg, naming); code < 400 {
		t.Errorf("POST %s/messages from tenant %s naming the session in %s = %d %s, want a refusal: the "+
			"public listener must not run the request as the calling session", path, intruder, callingSessionHeader, code, body)
	}
}
