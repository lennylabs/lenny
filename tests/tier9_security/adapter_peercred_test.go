// SPDX-License-Identifier: MIT

//go:build security

// Tier-5/tier-9 e2e tests for the §4.7 mandatory SO_PEERCRED startup
// self-test, exercised inside real agent pods on the Kind cluster. The
// pkg/adapter unit tests cover PeercredSelftest in-process; these tests
// close the gap the unit tests cannot reach: that the adapter's
// self-test actually gates READY under the runc and gVisor isolation
// runtimes (validating that gVisor's SO_PEERCRED semantics match the
// host kernel, per spec/04_system-components.md §4.7), and that a pod
// whose SO_PEERCRED self-test cannot pass crash-loops (fail closed)
// instead of joining the warm pool with an unenforceable security
// boundary.

package tier9_security_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// gvisorPool is the §5.3 sandboxed reference pool install.sh seeds; its
// warm pod runs under the gvisor RuntimeClass (runsc). The positive
// self-test assertion against it validates the §4.7 requirement that
// gVisor's SO_PEERCRED semantics be checked against the host kernel.
const gvisorPool = "gvisor-echo-pool"

// selftestFatalLog is the substring the §4.7 adapter logs and then exits
// non-zero on when the SO_PEERCRED self-test fails. It is the observable
// the crash-loop assertion keys on.
const selftestFatalLog = "SO_PEERCRED self-test failed"

// spec: 4.7
// diagnosis: TestAdapterPeercredSelftestPassesUnderRuncAndGvisor asserts
// the §4.7 mandatory SO_PEERCRED startup self-test passes inside live
// agent pods under both the runc sidecar model and the gVisor sandboxed
// profile. A failure means either the self-test regressed so that a Ready
// adapter never actually ran it, the adapter was silently downgraded to
// nonce-only mode (--require-so-peercred=false) where the spec expects
// the mandatory self-test, or gVisor's SO_PEERCRED behaviour diverged
// from the host kernel so the sandboxed pod could not enforce the §13
// intra-pod peer-credential boundary. Because a failing self-test exits
// non-zero before the adapter serves, a Ready adapter container running
// with require-so-peercred defaulted true is the observable proof the
// self-test passed on that pod's start.
func TestAdapterPeercredSelftestPassesUnderRuncAndGvisor(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := kind.RequireAgentWorkload(t, c)

	// runc: any sidecar-model warm pod carries the standalone adapter
	// container running under the default runc runtime.
	var runcPod string
	for _, p := range pods {
		if p.Model == "sidecar" && !strings.Contains(p.Pool, "gvisor") {
			runcPod = p.Name
			break
		}
	}
	if runcPod == "" {
		t.Skip("no runc sidecar-model agent pod in the workload")
	}
	t.Run("runc", func(t *testing.T) {
		assertSelftestEnforcedAndPassed(t, c, runcPod)
	})

	// gVisor: the sandboxed reference pool's warm pod, if the cluster
	// carries the gvisor RuntimeClass and a Ready pod.
	t.Run("gvisor", func(t *testing.T) {
		if _, err := c.KubectlOut(t, "get", "runtimeclass", "gvisor"); err != nil {
			t.Skip("no gvisor RuntimeClass on the cluster; run tests/testinfra/kind/install-gvisor.sh")
		}
		pod := gvisorPoolPod(t, c)
		if rc := podField(t, c, agentNamespace, pod, "{.spec.runtimeClassName}"); rc != "gvisor" {
			t.Fatalf("%s: runtimeClassName = %q, want \"gvisor\"", pod, rc)
		}
		assertSelftestEnforcedAndPassed(t, c, pod)
	})
}

// assertSelftestEnforcedAndPassed asserts the adapter container of pod in
// the agent namespace ran the §4.7 self-test with it mandatory and
// passed: the container is Ready (so the process got past the self-test
// to serve), it was launched with require-so-peercred defaulted true
// (never --require-so-peercred=false), and its current logs carry no
// self-test failure line.
func assertSelftestEnforcedAndPassed(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	if ready := podField(t, c, agentNamespace, pod,
		`{.status.containerStatuses[?(@.name=="adapter")].ready}`); ready != "true" {
		t.Fatalf("%s: adapter container ready = %q, want \"true\" (a passing §4.7 self-test is a "+
			"prerequisite for the adapter to serve and become Ready)", pod, ready)
	}
	args := podField(t, c, agentNamespace, pod, `{.spec.containers[?(@.name=="adapter")].args}`)
	if strings.Contains(args, "--require-so-peercred=false") {
		t.Fatalf("%s: adapter launched with --require-so-peercred=false; the mandatory §4.7 self-test "+
			"is not being enforced on this pod, so a Ready state does not prove SO_PEERCRED is functional",
			pod)
	}
	logs, err := c.KubectlOut(t, "-n", agentNamespace, "logs", pod, "-c", "adapter")
	if err != nil {
		t.Fatalf("%s: reading adapter logs: %v\n%s", pod, err, logs)
	}
	if strings.Contains(logs, selftestFatalLog) {
		t.Errorf("%s: adapter logs report a §4.7 SO_PEERCRED self-test failure on a Ready pod:\n%s",
			pod, logs)
	}
}

// gvisorPoolPod returns the name of the Ready warm pod in the sandboxed
// reference pool, skipping when the pool has not produced one yet (pools
// warm asynchronously).
func gvisorPoolPod(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	selector := "lenny.dev/pool=" + gvisorPool
	if err := c.Kubectl("-n", agentNamespace, "wait", "--for=condition=Ready",
		"pod", "-l", selector, "--timeout=120s").Run(); err != nil {
		t.Skip(gvisorPool + " has no Ready warm pod yet (pools warm asynchronously)")
	}
	out, err := c.KubectlOut(t, "-n", agentNamespace, "get", "pods", "-l", selector,
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("resolving the %s warm pod name: %v\n%s", gvisorPool, err, out)
	}
	return strings.TrimSpace(out)
}

// podField reads a jsonpath field off a namespaced pod.
func podField(t *testing.T, c *kind.Cluster, ns, pod, jsonpath string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", ns, "get", "pod", pod, "-o", "jsonpath="+jsonpath)
	if err != nil {
		t.Fatalf("reading pod %s field %s: %v\n%s", pod, jsonpath, err, out)
	}
	return strings.TrimSpace(out)
}

// spec: 4.7
// diagnosis: TestAdapterPeercredSelftestFaultCrashLoops asserts the §4.7
// fail-closed contract in a real pod: when the adapter's SO_PEERCRED
// self-test cannot pass, the adapter logs FATAL, exits non-zero, and the
// pod enters CrashLoopBackOff, so it never becomes Ready and is never
// handed a session. The fault is injected by a native-sidecar helper
// (tests/testinfra/sopeercredhog) that holds the abstract socket the
// self-test binds, sharing the pod network namespace so the adapter's own
// bind fails with EADDRINUSE. A failure here means the adapter did not
// treat a broken self-test as fatal (fail open), which would let a pod
// with an unenforceable §13 peer-credential boundary join the warm pool.
func TestAdapterPeercredSelftestFaultCrashLoops(t *testing.T) {
	kind.PrerequisitesAvailable(t)
	c := kind.InstallLenny(t)
	// A live agent pod proves the lenny-adapter:e2e image is loaded on a
	// node and gives us that node to pin the fault pod to, so its
	// imagePullPolicy: Never resolves locally.
	agentPods := kind.RequireAgentWorkload(t, c)
	node := podField(t, c, agentNamespace, agentPods[0].Name, "{.spec.nodeName}")
	arch := nodeArch(t, c, node)

	buildAndLoadHogImage(t, c, arch)

	const (
		faultNS  = "lenny-peercred-fault"
		faultPod = "peercred-fault"
	)
	manifest := faultPodManifest(faultNS, faultPod, node)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, manifest) })
	if out, err := c.ApplyStdin(t, manifest); err != nil {
		t.Fatalf("applying the fault pod: %v\n%s", err, out)
	}

	// Wait for the adapter container to crash-loop: either the kubelet
	// reports the CrashLoopBackOff waiting reason or it has already
	// restarted at least once with a failure.
	deadline := time.Now().Add(180 * time.Second)
	var crashed bool
	for time.Now().Before(deadline) {
		waiting := podField(t, c, faultNS, faultPod,
			`{.status.containerStatuses[?(@.name=="adapter")].state.waiting.reason}`)
		restarts := podField(t, c, faultNS, faultPod,
			`{.status.containerStatuses[?(@.name=="adapter")].restartCount}`)
		if waiting == "CrashLoopBackOff" || (restarts != "" && restarts != "0") {
			crashed = true
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !crashed {
		desc, _ := c.KubectlOut(t, "-n", faultNS, "describe", "pod", faultPod)
		t.Fatalf("%s: adapter container did not enter CrashLoopBackOff within the deadline "+
			"(§4.7 requires a failed SO_PEERCRED self-test to exit non-zero)\n%s", faultPod, desc)
	}

	// The adapter must have logged the §4.7 FATAL self-test line. The
	// crashed instance's logs are the previous container generation.
	logs := crashedAdapterLogs(t, c, faultNS, faultPod)
	if !strings.Contains(logs, selftestFatalLog) {
		t.Errorf("%s: adapter did not log the §4.7 SO_PEERCRED self-test failure before exiting; got:\n%s",
			faultPod, logs)
	}

	// Fail-closed consequence: the pod never reaches Ready, so the
	// gateway can never assign it a session.
	ready := podField(t, c, faultNS, faultPod,
		`{.status.conditions[?(@.type=="Ready")].status}`)
	if ready == "True" {
		t.Errorf("%s: pod reached Ready despite a failing SO_PEERCRED self-test; §4.7 requires it to "+
			"crash-loop and stay unschedulable for sessions", faultPod)
	}
}

// crashedAdapterLogs returns the adapter container logs, preferring the
// previous (crashed) generation and falling back to the current one.
func crashedAdapterLogs(t *testing.T, c *kind.Cluster, ns, pod string) string {
	t.Helper()
	if prev, err := c.KubectlOut(t, "-n", ns, "logs", pod, "-c", "adapter", "--previous"); err == nil &&
		strings.TrimSpace(prev) != "" {
		return prev
	}
	cur, _ := c.KubectlOut(t, "-n", ns, "logs", pod, "-c", "adapter")
	return cur
}

// nodeArch returns a node's GOARCH (amd64 / arm64), used to
// cross-compile the fault helper for the node it will run on.
func nodeArch(t *testing.T, c *kind.Cluster, node string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "get", "node", node, "-o",
		"jsonpath={.status.nodeInfo.architecture}")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Skipf("cannot resolve node %s architecture: %v\n%s", node, err, out)
	}
	return strings.TrimSpace(out)
}

// hogImage is the fault-injection helper image reference the negative
// test builds and kind-loads.
const hogImage = "lenny-sopeercred-hog:e2e"

// buildAndLoadHogImage cross-compiles tests/testinfra/sopeercredhog for
// the target node arch, packages it in a scratch image, and loads it onto
// the Kind cluster. Any tooling gap (go, docker, kind) skips the test
// rather than failing it, so the negative path is exercised where the
// toolchain is present and skips cleanly where it is not.
func buildAndLoadHogImage(t *testing.T, c *kind.Cluster, arch string) {
	t.Helper()
	for _, bin := range []string{"go", "docker"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH; cannot build the fault-injection image: %v", bin, err)
		}
	}
	root := repoRoot(t)
	dir := t.TempDir()
	binPath := filepath.Join(dir, "sockethog")

	build := exec.Command("go", "build", "-o", binPath, "./tests/testinfra/sopeercredhog")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("cross-compiling the fault-injection helper failed: %v\n%s", err, out)
	}

	dockerfile := "FROM scratch\nCOPY sockethog /sockethog\nENTRYPOINT [\"/sockethog\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		t.Fatalf("writing the fault-image Dockerfile: %v", err)
	}
	bd := exec.Command("docker", "build", "--platform", "linux/"+arch, "-t", hogImage, ".")
	bd.Dir = dir
	if out, err := bd.CombinedOutput(); err != nil {
		t.Skipf("building the fault-injection image failed: %v\n%s", err, out)
	}
	ld := exec.Command("kind", "load", "docker-image", "--name", c.Name, hogImage)
	if out, err := ld.CombinedOutput(); err != nil {
		t.Skipf("loading the fault-injection image onto the cluster failed: %v\n%s", err, out)
	}
}

// faultPodManifest renders the namespace and fault pod. The pod runs the
// real lenny-adapter with require-so-peercred=true alongside a native
// sidecar (tests/testinfra/sopeercredhog) that binds the self-test's
// abstract socket first. A tcpSocket startupProbe on the sidecar gates the
// adapter container's start on the socket being held, so the adapter's own
// self-test bind fails deterministically. The namespace carries no
// lenny.dev/agent-namespace label, so the agent-pod admission webhooks do
// not scope it. The container securityContext mirrors the §13.1 warm-pod
// posture so the pod is admissible.
func faultPodManifest(ns, pod, node string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: v1
kind: Pod
metadata:
  name: %[2]s
  namespace: %[1]s
spec:
  restartPolicy: Always
  nodeName: %[3]s
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  initContainers:
    - name: sockethog
      image: %[4]s
      imagePullPolicy: Never
      restartPolicy: Always
      args: ["--ready-port=:8081"]
      startupProbe:
        tcpSocket:
          port: 8081
        periodSeconds: 1
        failureThreshold: 30
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: 65532
        capabilities:
          drop: ["ALL"]
  containers:
    - name: adapter
      image: lenny-adapter:e2e
      imagePullPolicy: Never
      args: ["--addr=:50051", "--require-so-peercred=true"]
      securityContext:
        allowPrivilegeEscalation: false
        readOnlyRootFilesystem: true
        runAsNonRoot: true
        runAsUser: 65532
        capabilities:
          drop: ["ALL"]
`, ns, pod, node, hogImage)
}
