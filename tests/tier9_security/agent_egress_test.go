// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 §12.9.4 NetworkPolicy adversarial test for the agent-pod
// egress half. TESTING.md §12.9.4: "Test pods attempt to reach
// forbidden endpoints: agent pod to internet, agent pod to Postgres,
// agent pod to Redis, agent pod to another tenant's namespace,
// ephemeral container to credentials, agent pod to cloud metadata
// service. Every attempt times out at the CNI layer."
//
// network_policy_test.go already exercises the lenny-system half of
// §12.9.4 (TestNetworkPolicyAdversarial, TestNetworkPolicyPosture).
// This file exercises the agent-namespace half: a probe pod scheduled
// directly into lenny-agents, carrying the same lenny.dev/managed:
// "true" label and §13.1 hardened SecurityContext a real Sandbox
// reconciler-created pod carries (so the lenny-pod-security and
// lenny-label-immutability webhooks admit it), with no
// lenny.dev/egress-profile label — the restricted default every
// standard-isolation pool in this e2e overlay uses (see
// tests/testinfra/kind/install.sh's bootstrap.pools list). A restricted
// pod's only permitted egress is allow-pod-egress-base (the gateway
// control channel and cluster DNS); every other destination is left to
// default-deny-all.
package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// cloudMetadataAddress is the AWS instance-metadata-service address —
// the first entry of the chart's egressCIDRs.excludeIMDS list
// (charts/lenny/values.yaml), the canonical example of the "cloud
// metadata service" TESTING.md §12.9.4 names as a forbidden endpoint.
const cloudMetadataAddress = "169.254.169.254"

// publicInternetAddress is a stable public IPv4 address outside every
// RFC1918 / link-local / cluster range, used as the "agent pod to
// internet" §12.9.4 target. default-deny-all drops the connection at
// the CNI layer regardless of whether the address is genuinely
// reachable from the Kind host, so the probe does not depend on the
// test runner having real internet egress.
const publicInternetAddress = "1.1.1.1"

// agentEgressProbeNamespace is the namespace the §12.9.4 agent-pod
// probe schedules into — the same namespace the Sandbox reconciler
// places every managed agent pod in.
const agentEgressProbeNamespace = "lenny-agents"

// spec: 13.2 (Network Isolation, spec/13_security-model.md); the test
// plan is TESTING.md §12.9.4: "Test pods attempt to reach forbidden
// endpoints: agent pod to internet, agent pod to Postgres, agent pod to
// Redis, agent pod to another tenant's namespace, ... agent pod to
// cloud metadata service. Every attempt times out at the CNI layer."
// diagnosis: a failure means a restricted-profile agent pod in
// lenny-agents can reach a destination default-deny-all and
// allow-pod-egress-base do not admit — the lenny-agents NetworkPolicy
// set is missing, mis-scoped, or the CNI is not enforcing egress for
// that namespace. The positive control (the gateway control channel,
// the one destination allow-pod-egress-base does grant) must keep
// working so a block on every other destination is attributable to the
// NetworkPolicy set rather than a broken probe pod.
func TestNetworkPolicyAgentEgress(t *testing.T) {
	c := kind.InstallLenny(t)

	gatewayIP := serviceClusterIP(t, c, "lenny-gateway")
	if gatewayIP == "" {
		t.Fatalf("the lenny-gateway Service has no ClusterIP; cannot probe the agent-pod egress boundary")
	}
	postgresIP := serviceClusterIP(t, c, "lenny-postgres")
	if postgresIP == "" {
		t.Fatalf("the lenny-postgres Service has no ClusterIP; cannot probe the agent-pod egress boundary")
	}
	redisIP := serviceClusterIP(t, c, "lenny-redis")
	if redisIP == "" {
		t.Fatalf("the lenny-redis Service has no ClusterIP; cannot probe the agent-pod egress boundary")
	}

	pod := createAgentEgressProbePod(t, c)
	siblingIP := createSiblingTenantTarget(t, c)

	// Positive control: allow-pod-egress-base grants every managed
	// agent pod egress to the gateway's control-channel port
	// (gateway.grpcPort, 50051) regardless of egress profile. The
	// gateway speaks gRPC there, not HTTP, so curl cannot complete a
	// clean HTTP round trip (exit 0) — but a genuine NetworkPolicy
	// block always manifests as curl exit 28 (connect timeout), so any
	// other outcome (here, a fast protocol-mismatch error once the TCP
	// handshake succeeds) proves the connection was not dropped by the
	// CNI. Running this first proves the probe pod, its placement, and
	// the target port are sound, so a block on the forbidden
	// destinations below is attributable to the NetworkPolicy set and
	// not to a broken probe.
	gatewayTarget := fmt.Sprintf("http://%s:50051/", gatewayIP)
	res := curlFromPodInNamespace(t, c, agentEgressProbeNamespace, pod, gatewayTarget, 8*time.Second)
	if res.exitCode == 28 {
		t.Fatalf("positive control failed: an agent-namespace probe pod (lenny.dev/managed: \"true\") could not "+
			"reach the gateway control-channel port at %s (curl exit 28, connection timed out). "+
			"allow-pod-egress-base should permit this path.\noutput:\n%s", gatewayTarget, res.output)
	}
	t.Logf("positive control: agent-namespace probe reached the gateway control channel at %s "+
		"(curl exit %d, not a CNI timeout)", gatewayTarget, res.exitCode)

	cases := []struct {
		name   string
		target string
	}{
		{"internet", fmt.Sprintf("http://%s/", publicInternetAddress)},
		{"Postgres", fmt.Sprintf("http://%s:5432/", postgresIP)},
		{"Redis", fmt.Sprintf("http://%s:6379/", redisIP)},
		{"cloud metadata service", fmt.Sprintf("http://%s/latest/meta-data/", cloudMetadataAddress)},
		{"sibling tenant namespace", fmt.Sprintf("http://%s/", siblingIP)},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) {
			res := curlFromPodInNamespace(t, c, agentEgressProbeNamespace, pod, tc.target, 8*time.Second)
			if res.exitCode == 0 {
				t.Fatalf("§12.9.4 violation: an agent pod reached the forbidden %s endpoint at %s. "+
					"default-deny-all plus the restricted-profile allow-list must block every destination "+
					"other than the gateway control channel and cluster DNS.\noutput:\n%s",
					tc.name, tc.target, res.output)
			}
			if res.exitCode != 28 {
				t.Errorf("agent-pod probe to the %s endpoint %s failed with curl exit %d, expected 28 "+
					"(connection timed out). A non-timeout failure is not a clean CNI egress block.\noutput:\n%s",
					tc.name, tc.target, res.exitCode, res.output)
				return
			}
			t.Logf("§12.9.4 adversarial probe: agent pod blocked reaching the %s endpoint at %s "+
				"(curl exit 28, connection timed out — egress boundary enforced)", tc.name, tc.target)
		})
	}
}

// createAgentEgressProbePod schedules a probe pod directly into
// lenny-agents and waits for it to become Ready, registering a
// t.Cleanup to remove it. The pod carries the same lenny.dev/managed:
// "true" label and §13.1 hardened SecurityContext (fsGroup,
// supplementalGroups, seccompProfile, dropped capabilities,
// non-root) a real Sandbox reconciler-created pod carries, so the
// lenny-pod-security and lenny-label-immutability webhooks admit it on
// CREATE the same way they admit a real agent pod. It carries no
// lenny.dev/egress-profile label, matching the restricted default every
// standard-isolation pool on this e2e overlay uses (agent-workload.yaml
// declares no egress-profile override for any pool).
func createAgentEgressProbePod(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	name := fmt.Sprintf("np-agent-egress-probe-%d", time.Now().UnixNano())
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: agent-egress-probe
    lenny.dev/managed: "true"
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  securityContext:
    runAsNonRoot: true
    fsGroup: %d
    supplementalGroups: [%d]
    seccompProfile:
      type: RuntimeDefault
  containers:
    - name: probe
      image: %s
      imagePullPolicy: Never
      command: ["sleep", "600"]
      resources:
        requests:
          cpu: "10m"
          memory: "32Mi"
        limits:
          cpu: "100m"
          memory: "64Mi"
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: %d
        capabilities:
          drop: ["ALL"]
`, name, agentEgressProbeNamespace, podspec.CredReadersGID, podspec.CredReadersGID, probeImage, probeRunAsUser)

	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create the §12.9.4 agent-egress probe pod: %v\n%s", err, out)
	}
	out, err := c.KubectlOut(
		t,
		"-n", agentEgressProbeNamespace, "wait", "--for=condition=Ready",
		"pod/"+name, "--timeout=90s",
	)
	if err != nil {
		desc, _ := c.KubectlOut(t, "-n", agentEgressProbeNamespace, "describe", "pod", name)
		t.Fatalf("§12.9.4 agent-egress probe pod %q did not become Ready: %v\n%s\n--- describe ---\n%s",
			name, err, out, desc)
	}
	return name
}

// createSiblingTenantTarget schedules a throwaway namespace and target
// pod standing in for "another tenant's namespace" (TESTING.md
// §12.9.4), registers a t.Cleanup to delete the namespace (which
// cascades to the pod), and returns the target pod's IP. The namespace
// carries none of the lenny.dev/agent-namespace admission-webhook
// scoping, so the target pod needs no §13.1 SecurityContext of its own;
// the assertion under test is whether the agent-namespace probe pod's
// own egress policy permits reaching it, not whether the target
// namespace enforces anything.
func createSiblingTenantTarget(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	ns := fmt.Sprintf("tier9-sibling-tenant-%d", time.Now().UnixNano())
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: Pod
metadata:
  name: sibling-tenant-target
  namespace: %s
  labels:
    lenny.dev/test: sibling-tenant-target
spec:
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  containers:
    - name: target
      image: %s
      imagePullPolicy: Never
      command: ["sleep", "600"]
`, ns, ns, probeImage)

	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create the §12.9.4 sibling-tenant-namespace fixture: %v\n%s", err, out)
	}
	out, err := c.KubectlOut(
		t,
		"-n", ns, "wait", "--for=condition=Ready",
		"pod/sibling-tenant-target", "--timeout=90s",
	)
	if err != nil {
		t.Fatalf("§12.9.4 sibling-tenant-namespace target pod did not become Ready: %v\n%s", err, out)
	}
	ip, err := c.KubectlOut(
		t,
		"-n", ns, "get", "pod", "sibling-tenant-target", "-o", "jsonpath={.status.podIP}",
	)
	if err != nil || strings.TrimSpace(ip) == "" {
		t.Fatalf("could not resolve the sibling-tenant-namespace target pod's IP: %v\n%s", err, ip)
	}
	return strings.TrimSpace(ip)
}
