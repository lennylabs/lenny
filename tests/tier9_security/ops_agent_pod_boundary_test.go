// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §25.1/§25.2 external-only boundary of
// lenny-ops, exercised from the agent-workload namespace against the
// live CNI.
//
// §25.1 makes lenny-ops external-only "by design": no internal cluster
// workload — including Lenny's own agent pods, which run tenant-supplied
// code — may reach the operational control plane. ops_network_policy_test.go
// already pins the lenny-ops NetworkPolicy inventory and the CNI enforcement
// of lenny-ops-deny-all-ingress from an unrelated (default-namespace) peer.
// This file exercises the specific lateral-movement path §25.1 names: an
// agent pod in the lenny-agents namespace (the tenant-code namespace) cannot
// reach lenny-ops on its admin port. A positive control from the Ingress
// controller — the single peer lenny-ops-allow-ingress-from-ingress-controller
// admits — proves the deny is selective (lenny-ops is up and reachable via
// its one permitted ingress path) rather than lenny-ops simply being down.
//
// The test installs the Lenny control plane on a Kind cluster (via the
// install.sh-backed kind.InstallLenny harness). The CNI under test is
// kindnet, which enforces NetworkPolicy on this cluster. Both probes target
// the lenny-ops Service ClusterIP; kube-proxy DNATs to the lenny-ops pod IP
// before the CNI evaluates the ingress policy against the app: lenny-ops pod.
//
// TestAgentPodReachesObjectStoreButNoOtherLennySystemPort_spec_13_2 exercises
// the paired §13.2 egress boundary from the same agent namespace. The
// checkpoint pipeline grants the agent pod one new egress destination — the
// object store on its TLS port — against a gateway-minted presigned capability.
// The allow-pod-egress-objectstore egress policy and the paired MinIO
// agent-namespace ingress clause (NET-071) must open exactly that one port:
// the agent pod reaches MinIO on the TLS port (the positive control) and is
// still dropped at the CNI on every other lenny-system port (lenny-ops admin,
// the token service). Chart render tests prove the rendered YAML; only a live
// CNI probe proves the policy admits the object store and nothing else.

package tier9_security_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// objectStoreTLSPort is the TLS port the e2e MinIO store serves (the same port
// the §25.11 backup-egress positive control targets over https). The
// allow-pod-egress-objectstore policy opens this port to the agent namespace
// and no other.
const objectStoreTLSPort = 9000

// tokenServicePort is the token service's mTLS gRPC port in lenny-system. It is
// a §13.2 lenny-system destination the agent-to-object-store egress policy must
// not open, so it serves as the negative control that proves the new egress
// rule is scoped to the object store rather than to the whole namespace.
const tokenServicePort = 50052

// spec: §13.2 (NET-071 — the allow-pod-egress-objectstore agent-to-object-store
// egress policy and its paired MinIO agent-namespace ingress clause open exactly
// the object-store TLS port to the agent namespace; NET-047/050 is the separate
// selector-consistency audit that covers this policy's selector parity, not the
// egress rule itself)
// diagnosis: a failure means the NET-071 egress policy opened more than the
// object-store port, or the paired MinIO ingress clause is missing and the
// rule is dead. The test schedules a §13.1-compliant probe pod in the
// lenny-agents namespace (where tenant-supplied agent code runs) and asserts
// three CNI outcomes: (1) the probe reaches MinIO on the TLS port — the
// positive control, proving the egress policy and the MinIO ingress clause both
// admit the agent namespace (curl exit != 28); (2) the probe is dropped
// reaching the lenny-ops admin port (curl exit 28); (3) the probe is dropped
// reaching the token service (curl exit 28). If the object-store probe times
// out, the checkpoint data path is dead — the pod cannot upload chunks. If
// either negative control is reached, the egress policy opened more than the
// one destination the presigned-capability model bounds it to.
func TestAgentPodReachesObjectStoreButNoOtherLennySystemPort_spec_13_2(t *testing.T) {
	c := kind.InstallLenny(t)

	minioIP := dataStorePodIPT9(t, c, "minio")
	if minioIP == "" {
		t.Fatalf("no MinIO datastore pod IP found; cannot probe the §13.2 agent-to-object-store egress boundary")
	}
	opsIP := serviceClusterIP(t, c, "lenny-ops")
	if opsIP == "" {
		t.Fatalf("the lenny-ops Service has no ClusterIP; cannot probe the §13.2 egress selectivity")
	}
	tsIP := podIPBySelector(t, c, lennySystemNS, "lenny.dev/component=token-service")
	if tsIP == "" {
		t.Fatalf("no token-service pod IP found; cannot probe the §13.2 egress selectivity")
	}

	createAgentProbe(t, c, "objstore-boundary-agent")

	// Positive control: the agent pod reaches the object store on its TLS port.
	// This is the one destination allow-pod-egress-objectstore opens and the
	// MinIO ingress clause admits. A curl to the https health endpoint that
	// establishes the TCP connection (exit != 28) proves the CNI admits the
	// egress and the ingress clause is live; a TLS trust error (exit 60) still
	// establishes the connection, so the signal is "not a CNI timeout".
	minioTarget := fmt.Sprintf("https://%s:%d/minio/health/live", minioIP, objectStoreTLSPort)
	res := curlFromNS(t, c, agentNamespace, "objstore-boundary-agent", minioTarget, 8*time.Second)
	if res.exitCode == 28 {
		t.Fatalf("positive control failed: an agent pod in %s could not reach the object store at %s "+
			"(curl exit 28, timed out). allow-pod-egress-objectstore and the paired MinIO ingress clause "+
			"must open the object-store TLS port to the agent namespace, or the checkpoint upload path is "+
			"dead.\noutput:\n%s", agentNamespace, minioTarget, res.output)
	}
	t.Logf("positive control: agent pod reached the object store at %s (curl exit %d, TCP connection "+
		"established)", minioTarget, res.exitCode)

	// Negative control 1: the agent pod is dropped reaching the lenny-ops admin
	// port. The object-store egress rule must not have widened agent egress to
	// the rest of lenny-system.
	opsTarget := fmt.Sprintf("http://%s:%d/readyz", opsIP, opsHTTPPort)
	res = curlFromNS(t, c, agentNamespace, "objstore-boundary-agent", opsTarget, 8*time.Second)
	assertAgentEgressDropped(t, "lenny-ops admin port", opsTarget, res)

	// Negative control 2: the agent pod is dropped reaching the token service.
	tsTarget := fmt.Sprintf("http://%s:%d/", tsIP, tokenServicePort)
	res = curlFromNS(t, c, agentNamespace, "objstore-boundary-agent", tsTarget, 8*time.Second)
	assertAgentEgressDropped(t, "token service", tsTarget, res)
}

// assertAgentEgressDropped fails the test when an agent-pod egress probe was
// not cleanly dropped at the CNI. A reached destination (exit 0) is a §13.2
// violation — the object-store egress rule opened more than the one TLS port.
// A non-timeout failure (exit != 28) is not a clean CNI egress block, so it is
// reported as an error the operator must reconcile rather than a pass.
func assertAgentEgressDropped(t *testing.T, name, target string, res curlResult) {
	t.Helper()
	if res.exitCode == 0 {
		t.Fatalf("§13.2 violation: an agent pod reached the %s at %s. The agent-to-object-store egress "+
			"policy must open the object-store TLS port alone; it must not admit any other lenny-system "+
			"destination.\noutput:\n%s", name, target, res.output)
	}
	if res.exitCode != 28 {
		t.Errorf("agent-pod egress to the %s at %s failed with curl exit %d, expected 28 (connection "+
			"timed out). A non-timeout failure is not a clean CNI egress block.\noutput:\n%s",
			name, target, res.exitCode, res.output)
		return
	}
	t.Logf("negative control: agent pod blocked reaching the %s at %s (curl exit 28 — egress scoped to "+
		"the object store only)", name, target)
}

// objectStoreCAConfigMap is the per-agent-namespace ConfigMap the chart
// materializes (agent-objectstore-ca.yaml) from minio.tls.caBundle. The sandbox
// controller projects it read-only into every agent pod and points the
// adapter's --objectstore-ca-bundle at the mounted ca.crt so the adapter trusts
// a self-managed object store's non-public CA during checkpoint chunk PUT/GET.
const (
	objectStoreCAConfigMap = "lenny-objectstore-ca"
	objectStoreCAKey       = "ca.crt"
	objectStoreDNSName     = "lenny-minio.lenny-system.svc"
)

// spec: §13.2 (NET-071 — a self-managed object store serving TLS under a
// non-public CA requires agent pods to trust that CA to complete the checkpoint
// chunk PUT/GET handshake; the chart projects minio.tls.caBundle into each
// agent namespace and the controller mounts it into the adapter)
// diagnosis: a failure means the object-store CA is not trusted inside agent
// pods, so every checkpoint upload fails the TLS handshake, the seal-and-export
// path returns CheckpointFailed, the session is marked failed, and §6.2 recycle
// never runs. The test projects the object-store CA ConfigMap into a
// §13.1-compliant probe pod in the agent namespace and requires a TLS-verified
// request (no -k) to the object store's DNS name — routed through its Service
// ClusterIP, the address the gateway-minted presigned URL resolves to — to
// return HTTP 200 (curl exit 0). It first asserts the CA ConfigMap exists in the
// agent namespace: without minio.tls.caBundle carrying the PEM the chart renders
// no ConfigMap, the adapter has no trust anchor, and the handshake fails closed.
// Unlike the port-boundary probe, which tolerates a TLS error as long as the TCP
// connection is admitted, this probe fails unless the certificate itself
// validates, so it pins the trust wiring the checkpoint upload depends on.
func TestAgentPodTrustsObjectStoreCA_spec_13_2(t *testing.T) {
	c := kind.InstallLenny(t)

	minioClusterIP := serviceClusterIP(t, c, "lenny-minio")
	if minioClusterIP == "" {
		t.Fatalf("the lenny-minio Service has no ClusterIP; cannot probe the §13.2 object-store CA trust boundary")
	}

	// The chart materializes the object-store CA ConfigMap in the agent
	// namespace only when minio.tls.caBundle carries the PEM. Its absence is the
	// pre-fix state: the checkpoint upload has no trust anchor and fails closed.
	if _, err := c.KubectlOut(t, "-n", agentNamespace, "get", "configmap", objectStoreCAConfigMap); err != nil {
		t.Fatalf("the %s ConfigMap is missing from %s: %v. The e2e install must pass the self-managed MinIO CA "+
			"as minio.tls.caBundle so the chart projects it into agent pods; without it the adapter cannot "+
			"trust the object store during checkpoint chunk PUT/GET and every upload fails the TLS handshake.",
			objectStoreCAConfigMap, agentNamespace, err)
	}

	createAgentProbeWithObjectStoreCA(t, c, "objstore-ca-agent")

	// Verified handshake: --cacert points at the projected CA and --resolve pins
	// the object store's DNS name (which carries the serving cert's SAN) to its
	// Service ClusterIP, the address the presigned checkpoint URL resolves to.
	// curl performs full certificate validation (no -k), so a 200 proves the
	// adapter's trust store admits the object store's CA on the real upload path.
	script := fmt.Sprintf(
		"curl -sS -m 8 -o /dev/null -w 'http=%%{http_code}' "+
			"--cacert /etc/lenny/objectstore-ca/%s --resolve %s:%d:%s https://%s:%d/minio/health/live 2>&1; "+
			"echo \" exit=$?\"",
		objectStoreCAKey, objectStoreDNSName, objectStoreTLSPort, minioClusterIP,
		objectStoreDNSName, objectStoreTLSPort,
	)
	out, _ := c.KubectlOut(t, "-n", agentNamespace, "exec", "objstore-ca-agent", "--", "sh", "-c", script)
	if code := parseCurlExit(out); code != 0 {
		t.Fatalf("§13.2 violation: an agent pod could not complete a TLS-verified request to the object store "+
			"at https://%s:%d (curl exit %d, not 0). The adapter must trust the self-managed MinIO CA projected "+
			"from minio.tls.caBundle, or the checkpoint upload fails the handshake and the session is marked "+
			"failed.\noutput:\n%s", objectStoreDNSName, objectStoreTLSPort, code, out)
	}
	if !strings.Contains(out, "http=200") {
		t.Fatalf("§13.2 boundary: the TLS-verified object-store probe from an agent pod did not return HTTP 200 "+
			"(the MinIO health endpoint). The handshake completed but the health check did not succeed.\noutput:\n%s",
			out)
	}
	t.Logf("agent pod completed a TLS-verified request to the object store at https://%s:%d trusting the "+
		"projected CA (http=200) — the checkpoint upload trust anchor is wired", objectStoreDNSName, objectStoreTLSPort)
}

// createAgentProbeWithObjectStoreCA schedules the same §13.1-compliant probe
// pod as createAgentProbe and additionally projects the object-store CA
// ConfigMap read-only at /etc/lenny/objectstore-ca, mirroring the mount the
// sandbox controller adds to a real agent pod's adapter container. The mount
// makes the pod's readiness depend on the ConfigMap existing, so a pre-fix
// install (no projected CA) leaves the pod unschedulable and the test fails
// on the explicit ConfigMap precheck above before this runs.
func createAgentProbeWithObjectStoreCA(t *testing.T, c *kind.Cluster, name string) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/managed: "true"
    lenny.dev/test: ops-agent-boundary-probe
spec:
  nodeName: %s
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  securityContext:
    runAsNonRoot: true
    fsGroup: 65534
    supplementalGroups: [65534]
    seccompProfile:
      type: RuntimeDefault
  volumes:
    - name: objectstore-ca
      configMap:
        name: %s
  containers:
    - name: probe
      image: %s
      imagePullPolicy: Never
      command: ["sleep", "600"]
      volumeMounts:
        - name: objectstore-ca
          mountPath: /etc/lenny/objectstore-ca
          readOnly: true
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: %d
        seccompProfile:
          type: RuntimeDefault
        capabilities:
          drop: ["ALL"]
`, name, agentNamespace, probeNode, objectStoreCAConfigMap, probeImage, probeRunAsUser)

	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create agent probe pod %q in %s: %v\n%s", name, agentNamespace, err, out)
	}
	out, err := c.KubectlOut(t, "-n", agentNamespace, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=90s")
	if err != nil {
		desc, _ := c.KubectlOut(t, "-n", agentNamespace, "describe", "pod", name)
		t.Fatalf("agent probe pod %q in %s did not become Ready: %v\n%s\n--- describe ---\n%s",
			name, agentNamespace, err, out, desc)
	}
}

// ingressControllerNS is the namespace the Ingress controller runs in on
// the e2e Kind install (chart ingressControllerNamespace default). It is
// the single namespace lenny-ops-allow-ingress-from-ingress-controller
// admits ingress from.
const ingressControllerNS = "ingress-nginx"

// ingressControllerPodLabelKey / ...Value are the pod label the chart's
// ingress.controllerPodLabel defaults to and that
// lenny-ops-allow-ingress-from-ingress-controller pairs with the
// ingress-nginx namespaceSelector. A probe pod carrying this label in the
// ingress-nginx namespace is the one peer lenny-ops admits.
const (
	ingressControllerPodLabelKey   = "app.kubernetes.io/name"
	ingressControllerPodLabelValue = "ingress-nginx"
)

// spec: §25.1 (External by design — "No internal cluster workload —
// including Lenny's own agent pods — can reach the operational control
// plane"), §25.4 (lenny-ops-deny-all-ingress + the ingress-controller
// exception)
// diagnosis: the §25.1 external-only boundary is not CNI-enforced against
// Lenny's own agent pods. The test schedules a §13.1-compliant probe pod
// in the lenny-agents namespace (where tenant-supplied agent code runs)
// and targets the lenny-ops admin port (8090). lenny-ops-deny-all-ingress
// must drop the connection at the CNI (curl exit 28). A positive control
// from the Ingress controller namespace — carrying the controller pod
// label lenny-ops-allow-ingress-from-ingress-controller admits — reaches
// lenny-ops (curl exit != 28), proving the deny is selective and lenny-ops
// is up. A success on the agent probe means a tenant-code workload can
// reach the operational control plane, the lateral-movement path §25.1
// forbids; a non-timeout failure is not a clean CNI ingress block.
func TestOpsUnreachableFromAgentPod_spec_25_1(t *testing.T) {
	c := kind.InstallLenny(t)

	opsIP := serviceClusterIP(t, c, "lenny-ops")
	if opsIP == "" {
		t.Fatalf("the lenny-ops Service has no ClusterIP; cannot probe the §25.1 external-only boundary")
	}
	target := fmt.Sprintf("http://%s:%d/readyz", opsIP, opsHTTPPort)

	// Positive control: a probe pod in the Ingress controller namespace
	// carrying the controller pod label. This is the exact peer
	// lenny-ops-allow-ingress-from-ingress-controller admits on TCP 8090,
	// so its connection must reach lenny-ops. Running it first proves the
	// probe image, the ClusterIP target, and lenny-ops itself are sound, so
	// the block on the agent pod below is attributable to the ingress deny
	// rather than lenny-ops being unreachable. A reached-but-non-200 result
	// (for example a TLS listener in a non-dev profile) still establishes
	// the TCP connection, so the signal is "not a CNI timeout" (exit != 28)
	// rather than a strict exit 0.
	createProbeInNamespace(t, c, ingressControllerNS, "ops-boundary-ingressctl", map[string]string{
		ingressControllerPodLabelKey: ingressControllerPodLabelValue,
	})
	res := curlFromNS(t, c, ingressControllerNS, "ops-boundary-ingressctl", target, 8*time.Second)
	if res.exitCode == 28 {
		t.Fatalf("positive control failed: an Ingress-controller-labelled probe in %s could not reach "+
			"lenny-ops at %s (curl exit 28, timed out). lenny-ops-allow-ingress-from-ingress-controller "+
			"must admit the Ingress controller on TCP %d.\noutput:\n%s",
			ingressControllerNS, target, opsHTTPPort, res.output)
	}
	t.Logf("positive control: Ingress-controller-labelled probe reached lenny-ops at %s "+
		"(curl exit %d, %s)", target, res.exitCode, strings.TrimSpace(res.output))

	// The adversarial probe: a §13.1-compliant agent pod in the lenny-agents
	// namespace. This is the exact workload §25.1 forbids from reaching the
	// operational control plane — agent pods run tenant-supplied code, and
	// letting them reach lenny-ops would require reasoning about compromised
	// workloads dialling admin APIs. No lenny-ops ingress rule admits the
	// lenny-agents namespace, so lenny-ops-deny-all-ingress must drop the
	// connection at the CNI (curl exit 28).
	createAgentProbe(t, c, "ops-boundary-agent")
	res = curlFromNS(t, c, agentNamespace, "ops-boundary-agent", target, 8*time.Second)
	if res.exitCode == 0 {
		t.Fatalf("§25.1 violation: an agent pod in the %s namespace reached the lenny-ops admin port at %s. "+
			"lenny-ops is external-only by design; no internal cluster workload — least of all a tenant-code "+
			"agent pod — may reach the operational control plane.\noutput:\n%s", agentNamespace, target, res.output)
	}
	if res.exitCode != 28 {
		t.Errorf("agent-pod ingress to the lenny-ops admin port at %s failed with curl exit %d, expected 28 "+
			"(connection timed out). A non-timeout failure is not a clean CNI ingress block.\noutput:\n%s",
			target, res.exitCode, res.output)
		return
	}
	t.Logf("adversarial probe: agent pod in %s blocked reaching the lenny-ops admin port at %s "+
		"(curl exit 28 — external-only boundary enforced against tenant-code workloads)", agentNamespace, target)
}

// createAgentProbe schedules a curl probe pod in the lenny-agents
// namespace. Unlike createProbeInNamespace (whose pods run in namespaces
// the lenny-pod-security webhook does not gate), a pod in lenny-agents
// must satisfy the §13.1 admission controls: the pod-level securityContext
// carries runAsNonRoot, the lenny-cred-readers fsGroup (65534) and
// supplementalGroups membership, and RuntimeDefault seccomp, or
// pod-security.lenny.dev rejects the CREATE. The container keeps the same
// hardened, non-root, read-only-root, dropped-ALL profile the other probe
// pods use, and is pinned to the node the curl image is loaded on so it
// schedules offline with imagePullPolicy: Never.
//
// The pod carries lenny.dev/managed=true, the label every agent-egress
// NetworkPolicy selects (default-deny-all admits nothing; the supplemental
// allow-pod-egress-* policies re-open one destination each to managed pods).
// A real agent pod is managed by the Sandbox reconciler and carries it; a
// probe that omits it would sit under default-deny with no egress allow and
// could never reach the object store, so the boundary the test asserts would
// be unobservable. The lenny-label-immutability webhook permits any initial
// value on CREATE (it guards only UPDATE mutations), so a probe may carry the
// label from the start.
func createAgentProbe(t *testing.T, c *kind.Cluster, name string) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/managed: "true"
    lenny.dev/test: ops-agent-boundary-probe
spec:
  nodeName: %s
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  securityContext:
    runAsNonRoot: true
    fsGroup: 65534
    supplementalGroups: [65534]
    seccompProfile:
      type: RuntimeDefault
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
        seccompProfile:
          type: RuntimeDefault
        capabilities:
          drop: ["ALL"]
`, name, agentNamespace, probeNode, probeImage, probeRunAsUser)

	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create agent probe pod %q in %s: %v\n%s", name, agentNamespace, err, out)
	}
	out, err := c.KubectlOut(t, "-n", agentNamespace, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=90s")
	if err != nil {
		desc, _ := c.KubectlOut(t, "-n", agentNamespace, "describe", "pod", name)
		t.Fatalf("agent probe pod %q in %s did not become Ready: %v\n%s\n--- describe ---\n%s",
			name, agentNamespace, err, out, desc)
	}
}
