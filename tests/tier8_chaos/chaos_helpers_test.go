// SPDX-License-Identifier: MIT

//go:build chaos

// Shared helpers for the tier-8 chaos tests that drive the live Lenny
// control plane on the Kind cluster. The helpers wrap kubectl reads
// (Lease holder identity, Lease renew timestamps, Deployment readiness,
// Service endpoint counts) and the poll loop the chaos assertions use
// to wait for recovery within a documented bound.

package tier8_chaos_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// lennySystemNamespace is the namespace the Lenny control plane runs in.
const lennySystemNamespace = "lenny-system"

// pollUntil polls cond every interval until it returns true or timeout
// elapses. It reports whether cond became true within the bound. The
// chaos tests use it to wait for failover or reschedule recovery.
func pollUntil(timeout, interval time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// leaseHolderIdentity returns the .spec.holderIdentity of the named
// coordination.k8s.io/v1 Lease in lenny-system. An empty string means
// the Lease is absent or currently unheld.
func leaseHolderIdentity(t *testing.T, c *kind.Cluster, lease string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "lease", lease,
		"-o", "jsonpath={.spec.holderIdentity}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// leaseRenewTime returns the .spec.renewTime of the named Lease parsed
// as a time.Time. A zero time means the Lease is absent or carries no
// renew timestamp.
func leaseRenewTime(t *testing.T, c *kind.Cluster, lease string) time.Time {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "lease", lease,
		"-o", "jsonpath={.spec.renewTime}",
	)
	if err != nil {
		return time.Time{}
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return time.Time{}
	}
	// The Lease renewTime is a microsecond-precision RFC 3339 timestamp
	// (for example 2026-05-19T05:23:27.459709Z). RFC3339Nano parses the
	// fractional seconds without a fixed-width assumption.
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts
}

// leaseHolderPod extracts the pod name from a leader-election Lease
// holderIdentity. client-go formats the identity as
// "<pod-name>_<uuid>"; callers need the pod name to delete the leader.
// When the identity carries no underscore the whole string is returned.
func leaseHolderPod(holderIdentity string) string {
	if i := strings.LastIndex(holderIdentity, "_"); i >= 0 {
		return holderIdentity[:i]
	}
	return holderIdentity
}

// deploymentReady reports whether the named Deployment in lenny-system
// has its full desired replica count Ready. A read error or an
// unparseable status yields false so callers keep polling.
func deploymentReady(t *testing.T, c *kind.Cluster, deployment string) bool {
	t.Helper()
	// .status.readyReplicas is absent until at least one replica is
	// ready; the spec/status pair with a literal fallback keeps the
	// jsonpath total well-formed in that window.
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "deployment", deployment,
		"-o", "jsonpath={.spec.replicas}/{.status.readyReplicas}",
	)
	if err != nil {
		return false
	}
	desired, ready, ok := strings.Cut(strings.TrimSpace(out), "/")
	if !ok || desired == "" {
		return false
	}
	if ready == "" {
		ready = "0"
	}
	return desired == ready
}

// deploymentReadyState returns the "<ready>/<desired>" replica string
// for the named Deployment, for diagnostic logging.
func deploymentReadyState(t *testing.T, c *kind.Cluster, deployment string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "deployment", deployment,
		"-o", "jsonpath={.status.readyReplicas}/{.spec.replicas}",
	)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(out)
}

// podNames returns the names of every pod in lenny-system matching the
// label selector. The chaos tests use it to count Deployment pods and
// to confirm a deleted pod was replaced by a new one.
func podNames(t *testing.T, c *kind.Cluster, selector string) []string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "pods",
		"-l", selector,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// endpointCount returns the number of ready endpoint addresses backing
// the named Service in lenny-system. The chaos tests assert a Service
// keeps a non-zero endpoint set while one of its pods is rescheduled.
func endpointCount(t *testing.T, c *kind.Cluster, service string) int {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "endpoints", service,
		"-o", "jsonpath={range .subsets[*].addresses[*]}{.ip}{\"\\n\"}{end}",
	)
	if err != nil {
		return 0
	}
	return len(strings.Fields(out))
}

// --- Store-outage injection -------------------------------------------

// storeRecoveryBound is the time a store-outage chaos test allows for a
// scaled-down data-store Deployment to return to Ready after the
// replica count is restored. The single-replica e2e stores
// (datastores.yaml) have no persistent volume to reattach, so a
// restored pod only needs an image (already on the node) plus probe
// convergence; 3m is generous headroom.
const storeRecoveryBound = 3 * time.Minute

// deploymentDesiredReplicas returns the named lenny-system Deployment's
// .spec.replicas. It is sampled before a scale-to-zero so the outage
// can be reverted to the exact pre-injection replica count — the e2e
// data stores are single-replica, but lenny-token-service and the
// admission webhooks run two, and a blind restore to one would leave
// the shared cluster under-replicated.
func deploymentDesiredReplicas(t *testing.T, c *kind.Cluster, deployment string) int {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "deployment", deployment,
		"-o", "jsonpath={.spec.replicas}",
	)
	if err != nil {
		return 0
	}
	n := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(out), "%d", &n); err != nil {
		return 0
	}
	return n
}

// scaleDeployment sets the named lenny-system Deployment's replica
// count. The chaos tests use it to inject a store/component outage
// (replicas=0) and to restore the original count. A scale failure fails
// the test, since an un-injected outage is not the scenario under test
// and a failed restore would leave the shared cluster degraded.
func scaleDeployment(t *testing.T, c *kind.Cluster, deployment string, replicas int) {
	t.Helper()
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "scale", "deployment", deployment,
		fmt.Sprintf("--replicas=%d", replicas),
	); err != nil {
		t.Fatalf("failed to scale Deployment %s to %d replicas: %v\n%s",
			deployment, replicas, err, out)
	}
}

// scaleDownAndRestore scales the named Deployment to zero, registers a
// t.Cleanup that restores it to its pre-injection replica count and
// waits for it to return to Ready, and returns the original count. It
// is the single entry point a store/component-outage test uses to
// inject the outage so the shared cluster is always left at its
// original replica count even on a mid-test failure.
//
// postRestore, when supplied, runs inside the cleanup after the
// Deployment is Ready again. The Postgres and MinIO data stores use
// emptyDir, so a restored pod comes back empty; postRestore is where
// such a test re-seeds the store (re-migrate the schema, recreate the
// artifact bucket) so the shared cluster is left fully healthy.
func scaleDownAndRestore(t *testing.T, c *kind.Cluster, deployment string, postRestore ...func()) int {
	t.Helper()
	original := deploymentDesiredReplicas(t, c, deployment)
	if original == 0 {
		original = 1 // a Deployment under chaos is expected to run >= 1
	}
	scaleDeployment(t, c, deployment, 0)
	t.Cleanup(func() { restoreDeployment(t, c, deployment, original, postRestore...) })
	return original
}

// restoreDeployment scales the named Deployment back to replicas and
// waits for it to return to Ready within storeRecoveryBound, then runs
// any postRestore callbacks. A restore that does not reach Ready fails
// the test, since it would leave the shared cluster degraded for every
// test that follows.
func restoreDeployment(t *testing.T, c *kind.Cluster, deployment string, replicas int, postRestore ...func()) {
	t.Helper()
	scaleDeployment(t, c, deployment, replicas)
	recovered := pollUntil(storeRecoveryBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, deployment)
	})
	if !recovered {
		t.Errorf("Deployment %s did not return to Ready within %s after the replica count was restored to %d (state %s); "+
			"the shared cluster may be left degraded",
			deployment, storeRecoveryBound, replicas, deploymentReadyState(t, c, deployment))
	}
	for _, fn := range postRestore {
		fn()
	}
}

// waitDeploymentScaledDown polls until the named Deployment reports zero
// available replicas (its pod is gone). It reports whether the
// Deployment scaled down within the bound. The store-outage tests call
// it after scaleDeployment(...,0) so the outage assertion starts only
// once the store is actually unreachable.
func waitDeploymentScaledDown(t *testing.T, c *kind.Cluster, deployment string, timeout time.Duration) bool {
	t.Helper()
	return pollUntil(timeout, 2*time.Second, func() bool {
		out, err := c.KubectlOut(
			t,
			"-n", lennySystemNamespace, "get", "deployment", deployment,
			"-o", "jsonpath={.status.replicas}",
		)
		if err != nil {
			return false
		}
		got := strings.TrimSpace(out)
		return got == "" || got == "0"
	})
}

// deploymentRestartCount returns the summed container restart count
// across every pod matching the label selector. The chaos tests sample
// it before and after an outage: a stable count proves the component
// rode out a dependency failure without crash-looping.
func deploymentRestartCount(t *testing.T, c *kind.Cluster, selector string) int {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "pods",
		"-l", selector,
		"-o", "jsonpath={range .items[*].status.containerStatuses[*]}{.restartCount}{\"\\n\"}{end}",
	)
	if err != nil {
		return -1
	}
	total := 0
	for _, f := range strings.Fields(out) {
		n := 0
		if _, err := fmt.Sscanf(f, "%d", &n); err != nil {
			return -1
		}
		total += n
	}
	return total
}

// --- Gateway HTTP probe -----------------------------------------------

// probeImage is the probe-pod container image. curlimages/curl is a
// busybox-based image with a shell, already loaded on the e2e worker
// node, so the probe pod runs imagePullPolicy: Never.
const probeImage = "curlimages/curl:8.11.0"

// probeNode is the node the curl image is loaded on. The probe pod is
// pinned here so the kubelet finds the image locally.
const probeNode = "lenny-e2e-worker"

// probeRunAsUser is the numeric UID curlimages/curl runs as; the image
// declares a non-numeric user, so runAsNonRoot needs an explicit UID.
const probeRunAsUser = 100

// probeManifest renders a long-sleeping probe-pod manifest. The pod
// carries lenny.dev/component: bootstrap so allow-bootstrap-egress and
// allow-gateway-ingress permit its egress to the gateway through the
// lenny-system default-deny NetworkPolicy, and runs with the §13.1
// hardened securityContext so the lenny-pod-security webhook admits it.
func probeManifest(name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/component: bootstrap
    lenny.dev/test: chaos-probe
spec:
  nodeName: %s
  restartPolicy: Never
  terminationGracePeriodSeconds: 1
  containers:
    - name: probe
      image: %s
      imagePullPolicy: Never
      command: ["sleep", "1800"]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: %d
        capabilities:
          drop: ["ALL"]
`, name, lennySystemNamespace, probeNode, probeImage, probeRunAsUser)
}

// startGatewayProbePod creates a probe pod, registers a t.Cleanup to
// delete it, waits for it to become Ready, and returns the lenny-gateway
// Service ClusterIP the probe targets. A probe pod that never becomes
// Ready fails the test, since a later HTTP probe would otherwise
// misreport a scheduling failure as a gateway outage.
func startGatewayProbePod(t *testing.T, c *kind.Cluster, name string) string {
	t.Helper()
	manifest := probeManifest(name)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("failed to create probe pod %s: %v\n%s", name, err, out)
	}
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "wait", "--for=condition=Ready",
		"pod/"+name, "--timeout=120s",
	); err != nil {
		desc, _ := c.KubectlOut(t, "-n", lennySystemNamespace, "describe", "pod", name)
		t.Fatalf("probe pod %s did not become Ready: %v\n%s\n--- describe ---\n%s",
			name, err, out, desc)
	}
	ip, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "service", "lenny-gateway",
		"-o", "jsonpath={.spec.clusterIP}",
	)
	if err != nil || strings.TrimSpace(ip) == "" {
		t.Fatalf("the lenny-gateway Service has no ClusterIP: %v", err)
	}
	return strings.TrimSpace(ip)
}

// httpProbe is the outcome of a curl run inside the probe pod.
type httpProbe struct {
	curlExit   int
	statusCode int
	body       string
}

// ok reports whether the HTTP request completed (curl exit 0) and the
// server replied with the expected status code.
func (p httpProbe) ok(wantStatus int) bool {
	return p.curlExit == 0 && p.statusCode == wantStatus
}

// curlGateway runs curl inside the probe pod against a gateway path and
// returns the curl exit code, the HTTP status, and the response body.
// The -m bound caps how long a dropped connection hangs.
func curlGateway(t *testing.T, c *kind.Cluster, pod, gatewayIP, path string) httpProbe {
	t.Helper()
	url := fmt.Sprintf("http://%s:8080%s", gatewayIP, path)
	// curl writes the body to stdout then the test appends a marker line
	// carrying the HTTP status and curl's exit code so both travel out
	// of the exec together.
	script := fmt.Sprintf(
		"curl -sS -m 8 -w '\\nLENNYPROBE status=%%{http_code} exit=%%{exitcode}\\n' %s 2>&1",
		url,
	)
	out, _ := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "exec", pod, "--", "sh", "-c", script,
	)
	return parseHTTPProbe(out)
}

// parseHTTPProbe extracts the status code, curl exit code, and body
// from curlGateway's output. The body is everything before the
// LENNYPROBE marker line.
func parseHTTPProbe(out string) httpProbe {
	p := httpProbe{curlExit: -1, statusCode: -1}
	marker := "LENNYPROBE "
	idx := strings.LastIndex(out, marker)
	if idx < 0 {
		p.body = out
		return p
	}
	p.body = strings.TrimRight(out[:idx], "\n")
	for _, field := range strings.Fields(out[idx+len(marker):]) {
		if v, ok := strings.CutPrefix(field, "status="); ok {
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				p.statusCode = n
			}
		}
		if v, ok := strings.CutPrefix(field, "exit="); ok {
			n := 0
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				p.curlExit = n
			}
		}
	}
	return p
}

// healthReport is the subset of the §25.3 /v1/admin/health document the
// chaos tests assert against.
type healthReport struct {
	Status     string            `json:"status"`
	Components []healthComponent `json:"components"`
}

// healthComponent is one entry in the §25.3 health report.
type healthComponent struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	RunbookRef string `json:"runbookRef"`
}

// component returns the named component from the report and whether it
// was present.
func (r healthReport) component(name string) (healthComponent, bool) {
	for _, c := range r.Components {
		if c.Name == name {
			return c, true
		}
	}
	return healthComponent{}, false
}

// gatewayHealth probes the unauthenticated §25.3 /v1/admin/health
// endpoint and decodes the report. ok reports whether the probe
// completed and the body parsed; a failed probe yields ok=false so the
// caller keeps polling.
func gatewayHealth(t *testing.T, c *kind.Cluster, pod, gatewayIP string) (healthReport, bool) {
	t.Helper()
	p := curlGateway(t, c, pod, gatewayIP, "/v1/admin/health")
	if p.curlExit != 0 {
		return healthReport{}, false
	}
	var report healthReport
	if err := json.Unmarshal([]byte(p.body), &report); err != nil {
		return healthReport{}, false
	}
	return report, true
}

// waitHealthComponent polls /v1/admin/health until the named component
// reports one of wantStatuses. It reports whether the component reached
// the target state within the bound and the last observed status.
func waitHealthComponent(t *testing.T, c *kind.Cluster, pod, gatewayIP, component string, timeout time.Duration, wantStatuses ...string) (bool, string) {
	t.Helper()
	last := "unknown"
	reached := pollUntil(timeout, 3*time.Second, func() bool {
		report, ok := gatewayHealth(t, c, pod, gatewayIP)
		if !ok {
			return false
		}
		comp, present := report.component(component)
		if !present {
			return false
		}
		last = comp.Status
		for _, want := range wantStatuses {
			if comp.Status == want {
				return true
			}
		}
		return false
	})
	return reached, last
}

// reprogramKindnet restarts the kindnet DaemonSet and waits for the
// rollout. A chaos test that mutates a NetworkPolicy and restores it
// leaves the policy object correct, but kindnet's per-node datapath
// can hold stale enforcement state after the rapid mutate-then-restore
// cycle, intermittently breaking egress for unrelated pods on the
// affected node. Restarting kindnet forces every node to reprogram its
// NetworkPolicy rules from the restored object set, so a
// NetworkPolicy-mutating test leaves the cluster's enforcement
// genuinely consistent for the tiers that run after it.
func reprogramKindnet(t *testing.T, c *kind.Cluster) {
	t.Helper()
	if out, err := c.Kubectl("-n", "kube-system", "rollout", "restart",
		"daemonset/kindnet").CombinedOutput(); err != nil {
		t.Logf("reprogramKindnet: rollout restart: %v\n%s", err, out)
		return
	}
	if out, err := c.Kubectl("-n", "kube-system", "rollout", "status",
		"daemonset/kindnet", "--timeout=150s").CombinedOutput(); err != nil {
		t.Logf("reprogramKindnet: rollout status: %v\n%s", err, out)
	}
}
