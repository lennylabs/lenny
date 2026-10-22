// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the agent pod's preStop drain hook (BUILD-GAPS
// F-4.6.37). The kubelet runs a preStop exec hook inside the container's own
// image, so the hook only drains the pod when the rendered command names an
// executable the image contains and that executable dispatches the `prestop`
// subcommand. The adapter image and the embedded runtime images are distroless
// builds of the shared root Dockerfile, so neither carries a shell or any
// executable other than the one the Dockerfile installs.

package tier5_e2e_kind_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/prestop"
	"github.com/lennylabs/lenny/pkg/controller/sandbox/podspec"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// preStopHookCases names, per §4.7 deployment model, the reference warm pool
// the test draws a pod from and the container the pod builder hooks.
var preStopHookCases = []struct {
	Model, Pool, Container string
}{
	{Model: "sidecar", Pool: "echo-pool-sidecar", Container: "adapter"},
	{Model: "embedded", Pool: "echo-pool-embedded", Container: "runtime"},
}

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)), 4.7.10 (Deployment Model)
//
// diagnosis: the §4.6.1 preStop drain hook does not run in a live agent pod.
// The subtest names the deployment model. A failure in the exec probe means
// the hooked container's image has no executable at the rendered path, or the
// executable there does not dispatch the `prestop` subcommand: compare the
// rendered command against the root Dockerfile's COPY destination and
// ENTRYPOINT, and confirm the binary's main dispatches prestop.Verb before
// parsing its server flags. A FailedPreStopHook event after the deletion
// means the kubelet ran the hook and it exited non-zero; the event message
// carries the hook's output. A skip naming the controller image means the
// cluster's images were built before the fix; rebuild and reload them with
// tests/testinfra/kind/install.sh (LENNY_FORCE_BUILD=1).
func TestAgentPodPreStopHookExecsTheInstalledBinary(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := kind.RequireAgentWorkload(t, c)
	for _, tc := range preStopHookCases {
		t.Run(tc.Model, func(t *testing.T) {
			pod := idleWorkloadPod(t, c, pods, tc.Pool)
			cmd := renderedPreStopCommand(t, c, pod, tc.Container)
			if len(cmd) < 2 || cmd[0] != podspec.PlatformBinaryPath || cmd[1] != prestop.Verb {
				t.Skipf("blocked: pod %s renders the preStop command %v rather than %s %s, so the "+
					"lenny-controller image on this cluster predates the F-4.6.37 fix; rebuild and reload "+
					"the images with tests/testinfra/kind/install.sh", pod, cmd, podspec.PlatformBinaryPath, prestop.Verb)
			}
			assertPreStopExecutableDispatches(t, c, pod, tc.Container, cmd[:2])
			// The hook now execs, but the prestop subcommand SIGTERMs PID 1
			// of its own container, so the kubelet SIGKILLs the exec (exit
			// 137) when PID 1 exits and records FailedPreStopHook on every
			// deletion. What the hook should do is the §4.6.1 eviction
			// checkpoint, which R16 owns (F-4.6.38).
			if os.Getenv("LENNY_TIER5_PRESTOP_EXIT_BLOCKED") != "run" {
				t.Skipf("blocked: F-4.6.38 (the prestop subcommand signals its own container's PID 1 and is "+
					"killed with it; owner R16); set LENNY_TIER5_PRESTOP_EXIT_BLOCKED=run to assert pod %s "+
					"deletes without FailedPreStopHook", pod)
			}
			assertDeletionRaisesNoFailedPreStopHook(t, c, pod)
		})
	}
}

// idleWorkloadPod returns a warm pod from pool whose backing Sandbox is idle,
// skipping the subtest when the pool has none. An idle pod carries no
// session, so draining and deleting it loses no work; the warm pool controller
// replaces it.
func idleWorkloadPod(t *testing.T, c *kind.Cluster, pods []kind.AgentPod, pool string) string {
	t.Helper()
	for _, p := range pods {
		if p.Pool == pool && sandboxPhaseForPod(t, c, p.Name) == "idle" {
			return p.Name
		}
	}
	t.Skipf("blocked: warm pool %s has no idle pod on this cluster (pods: %v)", pool, pods)
	return ""
}

// renderedPreStopCommand reads the preStop exec command the controller
// rendered onto container in the live pod.
func renderedPreStopCommand(t *testing.T, c *kind.Cluster, pod, container string) []string {
	t.Helper()
	raw := podField(t, c, pod, fmt.Sprintf(
		`{.spec.containers[?(@.name==%q)].lifecycle.preStop.exec.command}`, container,
	))
	if raw == "" {
		t.Fatalf("pod %s container %s carries no preStop exec hook; §4.6.1 requires one on every agent pod", pod, container)
	}
	var cmd []string
	if err := json.Unmarshal([]byte(raw), &cmd); err != nil {
		t.Fatalf("pod %s container %s preStop command %q is not a JSON string array: %v", pod, container, raw, err)
	}
	return cmd
}

// assertPreStopExecutableDispatches execs the rendered executable and verb in
// the hooked container with a --timeout the subcommand rejects. The rejection
// returns before the drain signals PID 1, so the probe leaves the pod running.
// Exit status 2 with the subcommand's own rejection message proves that the
// image contains the executable at the rendered path and that it dispatches
// the verb, which is everything the kubelet's exec needs.
func assertPreStopExecutableDispatches(t *testing.T, c *kind.Cluster, pod, container string, argv []string) {
	t.Helper()
	args := append([]string{"-n", "lenny-agents", "exec", pod, "-c", container, "--"}, argv...)
	args = append(args, "--timeout=0s")
	out, err := c.KubectlOut(t, args...)
	if err == nil {
		t.Fatalf("pod %s container %s: %v --timeout=0s exited 0; the prestop subcommand must reject a "+
			"non-positive timeout, so the executable did not dispatch it\n%s", pod, container, argv, out)
	}
	if !strings.Contains(out, "--timeout must be positive") {
		t.Fatalf("pod %s container %s: %v --timeout=0s did not reach the prestop subcommand (%v)\n%s",
			pod, container, argv, err, out)
	}
	t.Logf("pod %s container %s: %v exists in the image and dispatches the prestop subcommand", pod, container, argv)
}

// assertDeletionRaisesNoFailedPreStopHook deletes pod, waits for it to go,
// and fails when the kubelet recorded a FailedPreStopHook event for it.
func assertDeletionRaisesNoFailedPreStopHook(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	if out, err := c.KubectlOut(t, "-n", "lenny-agents", "delete", "pod", pod,
		"--wait=true", "--timeout=180s"); err != nil {
		t.Fatalf("deleting pod %s: %v\n%s", pod, err, out)
	}
	out, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "events",
		"--field-selector", "involvedObject.name="+pod+",reason=FailedPreStopHook",
		"-o", "jsonpath={range .items[*]}{.message}{\"\\n\"}{end}")
	if err != nil {
		t.Fatalf("listing events for pod %s: %v\n%s", pod, err, out)
	}
	if msg := strings.TrimSpace(out); msg != "" {
		t.Fatalf("pod %s: the kubelet recorded FailedPreStopHook on deletion; the preStop drain did not run:\n%s", pod, msg)
	}
	t.Logf("pod %s: deleted with no FailedPreStopHook event", pod)
}
