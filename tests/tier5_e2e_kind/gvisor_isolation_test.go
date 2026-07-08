// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §5.3 sandboxed isolation profile. The
// cluster.yaml worker labelled lenny.dev/pool=sandbox-gvisor carries a
// real gVisor (runsc) install: tests/testinfra/kind/install-gvisor.sh
// installs the runsc + containerd-shim-runsc-v1 binaries onto that
// node and applies the gvisor RuntimeClass before install.sh's
// Postgres-authoritative gvisor-echo-pool (isolationProfile:
// sandboxed) seeds its warm pod, so this test exercises a genuinely
// gVisor-sandboxed agent pod rather than a RuntimeClass-name assertion
// alone.
//
// Gated on RuntimeClass presence: a cluster brought up with
// LENNY_KIND_SKIP_GVISOR=1, or a custom LENNY_CLUSTER_CONFIG lacking
// the sandbox-gvisor node label, skips this test cleanly rather than
// failing.

package tier5_e2e_kind_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// gvisorAgentNamespace is the namespace the agent-pod workload runs in
// (see kind.RequireAgentWorkload's agentNamespace, unexported to that
// package).
const gvisorAgentNamespace = "lenny-agents"

// gvisorPoolName is the SandboxWarmPool tests/testinfra/kind/install.sh
// seeds with isolationProfile: sandboxed (gvisor-echo-pool in the
// generated bootstrap overlay).
const gvisorPoolName = "gvisor-echo-pool"

// gvisorNodeLabelSelector is the node label install-gvisor.sh installs
// runsc onto and cluster.yaml pre-labels on the second worker.
const gvisorNodeLabelSelector = "lenny.dev/pool=sandbox-gvisor"

// spec: 5.3
// diagnosis: TestGvisorIsolation asserts the default production
// isolation profile (sandboxed) actually runs a pod under a real
// gVisor sandbox rather than only carrying the gvisor RuntimeClass
// name. A failure here means either the SandboxWarmPool never scheduled
// a pod under the gvisor RuntimeClass (the §5.3 profile-to-RuntimeClass
// mapping regressed, or the pod landed on a node without runsc), the
// §13.1 capability-drop invariant stopped applying to sandboxed pods
// specifically, or the pod's kernel is not actually gVisor's
// intercepting sentry (a silent fallback to the host runc kernel would
// defeat the "kernel-level isolation prevents container escape via
// kernel exploits" guarantee §5.3 makes for the sandboxed profile).
func TestGvisorIsolation(t *testing.T) {
	c := kind.InstallLenny(t)
	requireGvisorRuntimeClass(t, c)

	pod := requireGvisorPoolPod(t, c)

	if rc := podField(t, c, pod, "{.spec.runtimeClassName}"); rc != "gvisor" {
		t.Errorf("%s: runtimeClassName = %q, want \"gvisor\" (§5.3 sandboxed isolation profile)", pod, rc)
	}

	assertScheduledOnSandboxGvisorNode(t, c, pod)
	assertCapabilitiesDropped(t, c, pod)
	assertGvisorKernelFingerprint(t, c, pod)
}

// requireGvisorRuntimeClass skips the test when the gvisor RuntimeClass
// or the sandbox-gvisor labelled node is absent, so a cluster brought
// up without install-gvisor.sh (LENNY_KIND_SKIP_GVISOR=1, or a custom
// LENNY_CLUSTER_CONFIG) degrades to a clean skip instead of a failure.
func requireGvisorRuntimeClass(t *testing.T, c *kind.Cluster) {
	t.Helper()
	if _, err := c.KubectlOut(t, "get", "runtimeclass", "gvisor"); err != nil {
		t.Skip("no \"gvisor\" RuntimeClass on the cluster; run tests/testinfra/kind/install-gvisor.sh " +
			"(install.sh runs it automatically unless LENNY_KIND_SKIP_GVISOR=1)")
	}
	out, err := c.KubectlOut(t, "get", "nodes", "-l", gvisorNodeLabelSelector,
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Skip("no node labeled " + gvisorNodeLabelSelector + "; tests/testinfra/kind/cluster.yaml " +
			"labels the second worker with it by default")
	}
}

// requireGvisorPoolPod returns the name of the Ready warm pod in
// gvisor-echo-pool, skipping when the pool has not produced one yet
// (the pool warms asynchronously; install.sh's own wait only requires
// two Ready managed pods across all pools, not this one specifically).
func requireGvisorPoolPod(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	selector := "lenny.dev/pool=" + gvisorPoolName
	if err := c.Kubectl(
		"-n", gvisorAgentNamespace, "wait", "--for=condition=Ready",
		"pod", "-l", selector, "--timeout=120s",
	).Run(); err != nil {
		t.Skip(gvisorPoolName + " has no Ready warm pod yet (pools warm asynchronously); " +
			"re-run after tests/testinfra/kind/install.sh completes")
	}
	out, err := c.KubectlOut(t, "-n", gvisorAgentNamespace, "get", "pods",
		"-l", selector, "-o", "jsonpath={.items[0].metadata.name}")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("resolving the %s warm pod name: %v\n%s", gvisorPoolName, err, out)
	}
	return strings.TrimSpace(out)
}

// assertScheduledOnSandboxGvisorNode asserts pod landed on the node the
// gvisor RuntimeClass's scheduling.nodeSelector pins it to (§17.2: "the
// RuntimeClass scheduling pins gVisor pods to the labelled node pool
// with no per-pod configuration").
func assertScheduledOnSandboxGvisorNode(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	node := podField(t, c, pod, "{.spec.nodeName}")
	label := nodeField(t, c, node, "{.metadata.labels.lenny\\.dev/pool}")
	if label != "sandbox-gvisor" {
		t.Errorf("%s: scheduled on node %q whose lenny.dev/pool label is %q, want \"sandbox-gvisor\"",
			pod, node, label)
	}
}

// nodeField reads a jsonpath field off a cluster-scoped Node object
// (podField in pod_lifecycle_test.go is pod-only and namespaced).
func nodeField(t *testing.T, c *kind.Cluster, node, jsonpath string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "get", "node", node, "-o", "jsonpath="+jsonpath)
	if err != nil {
		t.Fatalf("reading node %s field %s: %v\n%s", node, jsonpath, err, out)
	}
	return strings.TrimSpace(out)
}

// assertCapabilitiesDropped asserts every container in pod drops all
// Linux capabilities (§13.1 Capabilities row), the same posture every
// other isolation profile carries. Verifying it against a sandboxed
// pod specifically closes the gap the process-namespace fingerprint
// check alone would leave: gVisor intercepts syscalls regardless of
// the capability set, so capability drop is an independent §13.1
// control that needs its own assertion.
func assertCapabilitiesDropped(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	names := podContainers(t, c, pod)
	if len(names) == 0 {
		t.Fatalf("%s: no containers found", pod)
	}
	for _, name := range names {
		out, err := c.KubectlOut(t, "-n", gvisorAgentNamespace, "get", "pod", pod,
			"-o", fmt.Sprintf(`jsonpath={.spec.containers[?(@.name=="%s")].securityContext.capabilities.drop}`, name))
		if err != nil {
			t.Fatalf("%s: reading container %q capabilities.drop: %v\n%s", pod, name, err, out)
		}
		if strings.TrimSpace(out) != `["ALL"]` {
			t.Errorf(`%s: container %q securityContext.capabilities.drop = %q, want ["ALL"] (§13.1 Capabilities row)`,
				pod, name, out)
		}
	}
}

// assertGvisorKernelFingerprint attaches a short-lived ephemeral debug
// container to pod's "runtime" container and reads /proc/version. Every
// gVisor sentry reports its emulated kernel version with a "-gvisor"
// build suffix (e.g. "4.19.0-gvisor"); a host runc kernel never does.
// This is the direct verification of the §5.3 "kernel-level isolation"
// claim: a pod merely carrying the gvisor RuntimeClass name without a
// working runsc install would still show the host's real kernel here.
//
// The debug container's securityContext satisfies both admission
// layers a real attach goes through: §13.1 pod-security (non-root,
// read-only root filesystem, all capabilities dropped) and §12.9.3's
// lenny-ephemeral-container-cred-guard, which requires runAsUser,
// runAsGroup, and supplementalGroups to be set explicitly and forbids
// any of them from equaling the pod's adapter/agent UID or the
// lenny-cred-readers GID (a debug container that could read the
// per-slot credential file is exactly what the guard exists to block,
// so a namespace-fingerprint probe must stay outside that boundary
// too).
func assertGvisorKernelFingerprint(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	// A container name unique per test run avoids colliding with an
	// ephemeral container a previous run against the same long-lived
	// warm pod already attached (Kubernetes forbids reusing an
	// ephemeral container name on the same pod).
	containerName := "gvisor-fingerprint-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	// The ephemeralcontainers subresource is a full replace of
	// spec.ephemeralContainers, not an append: a body that lists only
	// the new container is rejected once the pod already carries one
	// from an earlier run ("existing ephemeral containers ... may not
	// be removed"). Read the pod's current list first and carry it
	// forward alongside the new entry. The test deliberately does not
	// delete the pod afterward: gvisor-echo-pool is dedicated to this
	// test (warmCount: 1, no session driver claims it), and deleting a
	// managed pod would race kind.RequireAgentWorkload's blanket
	// Ready-wait in every other tier-5/8/9 test that shares this
	// long-lived cluster. A handful of terminated debug containers
	// accumulating on one warm pod across repeated local runs is
	// harmless; a fresh cluster starts with none.
	existing := podField(t, c, pod, "{.spec.ephemeralContainers}")
	if existing == "" {
		existing = "[]"
	}

	bodyPath := filepath.Join(t.TempDir(), "gvisor-fingerprint-attach.json")
	body, err := gvisorFingerprintAttachBody(pod, containerName, existing)
	if err != nil {
		t.Fatalf("build ephemeral-container attach body: %v", err)
	}
	if err := os.WriteFile(bodyPath, body, 0o600); err != nil {
		t.Fatalf("write ephemeral-container attach body: %v", err)
	}
	raw := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/ephemeralcontainers", gvisorAgentNamespace, pod)
	if out, err := c.KubectlOut(t, "-n", gvisorAgentNamespace, "replace", "--raw", raw, "-f", bodyPath); err != nil {
		t.Fatalf("attach %s debug container to %s: %v\n%s", containerName, pod, err, out)
	}

	deadline := time.Now().Add(60 * time.Second)
	var terminated bool
	for time.Now().Before(deadline) {
		reason := podField(t, c, pod, fmt.Sprintf(
			`{.status.ephemeralContainerStatuses[?(@.name=="%s")].state.terminated.reason}`, containerName,
		))
		if reason != "" {
			terminated = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !terminated {
		t.Fatalf("%s: debug container %q did not terminate within 60s", pod, containerName)
	}

	logs, err := c.KubectlOut(t, "-n", gvisorAgentNamespace, "logs", pod, "-c", containerName)
	if err != nil {
		t.Fatalf("read logs of debug container %q on %s: %v\n%s", containerName, pod, err, logs)
	}
	if !strings.Contains(strings.ToLower(logs), "gvisor") {
		t.Errorf("%s: /proc/version inside the pod's shared kernel context does not report gVisor "+
			"(§5.3 kernel-level isolation) — got %q; the pod may have fallen back to the host kernel "+
			"despite carrying the gvisor RuntimeClass", pod, strings.TrimSpace(logs))
	} else {
		t.Logf("%s: /proc/version = %q (gVisor sentry confirmed)", pod, strings.TrimSpace(logs))
	}
}

// gvisorFingerprintAttachBody renders the JSON pod object posted to the
// pods/ephemeralcontainers subresource: existingEphemeralContainersJSON
// (the pod's current spec.ephemeralContainers, or "[]") carried forward
// verbatim plus the new debug container, since the subresource replaces
// the whole list rather than appending to it. runAsUser/runAsGroup
// 25252 is chosen only to be distinct from every UID the §13.1 podspec
// assigns (adapter, agent, and the lenny-cred-readers GID); it carries
// no other significance. corev1.SecurityContext (container-level) has
// no supplementalGroups field — only PodSecurityContext does — so
// condition (iii) of the §12.9.3 guard evaluates the pod's own
// spec.securityContext.supplementalGroups for that leg; runAsUser and
// runAsGroup are the two fields this ephemeral container controls.
func gvisorFingerprintAttachBody(podName, containerName, existingEphemeralContainersJSON string) ([]byte, error) {
	var existing []json.RawMessage
	if err := json.Unmarshal([]byte(existingEphemeralContainersJSON), &existing); err != nil {
		return nil, fmt.Errorf("parse existing ephemeralContainers %q: %w", existingEphemeralContainersJSON, err)
	}
	newContainer := json.RawMessage(fmt.Sprintf(`{
    "name": %q,
    "image": "busybox:1.36",
    "command": ["cat", "/proc/version"],
    "targetContainerName": "runtime",
    "securityContext": {
      "allowPrivilegeEscalation": false,
      "readOnlyRootFilesystem": true,
      "runAsNonRoot": true,
      "runAsUser": 25252,
      "runAsGroup": 25252,
      "capabilities": {"drop": ["ALL"]},
      "seccompProfile": {"type": "RuntimeDefault"}
    }
  }`, containerName))
	body := struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec struct {
			EphemeralContainers []json.RawMessage `json:"ephemeralContainers"`
		} `json:"spec"`
	}{
		APIVersion: "v1",
		Kind:       "Pod",
	}
	body.Metadata.Name = podName
	body.Metadata.Namespace = gvisorAgentNamespace
	body.Spec.EphemeralContainers = append(existing, newContainer)
	return json.Marshal(body)
}
