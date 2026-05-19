// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the §13.6 warm-pool pod lifecycle against
// the live agent-pod workload. install.sh applies
// tests/testinfra/kind/agent-workload.yaml, which declares two
// SandboxWarmPools, one per §4.7 deployment model. The test asserts
// each pool warms a Ready agent pod whose container topology matches
// its model.

package tier5_e2e_kind_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 13.6
// diagnosis: the §13.6 warm-pool pod lifecycle is exercised against
// the live agent-pod workload install.sh applies. The workload
// declares two SandboxWarmPools, one per §4.7 deployment model. The
// test asserts both models are represented, that each pool warmed a
// Ready pod whose container set matches its model (sidecar: an
// adapter and a runtime container; embedded: one runtime container),
// that every pod has the §5.3 isolation RuntimeClass stamped, and
// that the backing Sandbox is idle. The claim, assign, drain, and
// return transitions run in the tier-2/3 component suites.
func TestPodLifecycle(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := kind.RequireAgentWorkload(t, c)

	seen := map[string]bool{}
	for _, p := range pods {
		seen[p.Model] = true
	}
	for _, model := range []string{"sidecar", "embedded"} {
		if !seen[model] {
			t.Errorf("no warm pod for the §4.7 %s deployment model (pods: %v)", model, pods)
		}
	}

	for _, p := range pods {
		assertPodLifecycle(t, c, p)
	}
}

// assertPodLifecycle checks one warm agent pod against the §4.7
// contract for its pool's deployment model.
func assertPodLifecycle(t *testing.T, c *kind.Cluster, p kind.AgentPod) {
	t.Helper()

	containers := podContainers(t, c, p.Name)
	switch p.Model {
	case "sidecar":
		if !sameStringSet(containers, []string{"adapter", "runtime"}) {
			t.Errorf("%s (sidecar pool %s): containers = %v, want {adapter, runtime} — "+
				"the §4.7 sidecar model runs the adapter and the runtime in separate containers",
				p.Name, p.Pool, containers)
		}
	case "embedded":
		if !sameStringSet(containers, []string{"runtime"}) {
			t.Errorf("%s (embedded pool %s): containers = %v, want {runtime} — "+
				"the §4.7 embedded model runs a single container whose image embeds the adapter",
				p.Name, p.Pool, containers)
		}
	default:
		t.Errorf("%s: pool %s declares no recognized §4.7 deployment model", p.Name, p.Pool)
	}

	// The Sandbox reconciler stamps the §5.3 isolation RuntimeClass onto
	// every agent pod; the standard profile maps to the runc handler.
	if rc := podField(t, c, p.Name, "{.spec.runtimeClassName}"); rc != "runc" {
		t.Errorf("%s: runtimeClassName = %q, want \"runc\" (§5.3 standard isolation profile)", p.Name, rc)
	}

	// A warmed, unclaimed pod's Sandbox sits in the idle phase.
	if phase := sandboxPhaseForPod(t, c, p.Name); phase != "idle" {
		t.Errorf("%s: backing Sandbox phase = %q, want \"idle\" (a warm, claimable pod)", p.Name, phase)
	}

	t.Logf("%s: §4.7 %s model verified — containers %v, RuntimeClass runc, Sandbox idle",
		p.Name, p.Model, containers)
}

// podContainers returns the names of a pod's containers.
func podContainers(t *testing.T, c *kind.Cluster, pod string) []string {
	t.Helper()
	out := podField(t, c, pod, "{range .spec.containers[*]}{.name}{\"\\n\"}{end}")
	var names []string
	for _, n := range strings.Split(strings.TrimSpace(out), "\n") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// podField reads a single jsonpath field from an agent pod.
func podField(t *testing.T, c *kind.Cluster, pod, jsonpath string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "pod", pod, "-o", "jsonpath="+jsonpath)
	if err != nil {
		t.Fatalf("reading pod %s field %s: %v\n%s", pod, jsonpath, err, out)
	}
	return strings.TrimSpace(out)
}

// sandboxPhaseForPod returns the status.phase of the Sandbox whose
// status.podName names the given pod.
func sandboxPhaseForPod(t *testing.T, c *kind.Cluster, pod string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "sandbox",
		"-o", "jsonpath={range .items[?(@.status.podName==\""+pod+"\")]}{.status.phase}{end}")
	if err != nil {
		t.Fatalf("reading the Sandbox phase for pod %s: %v\n%s", pod, err, out)
	}
	return strings.TrimSpace(out)
}

// sameStringSet reports whether got and want hold the same names
// regardless of order.
func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	for i := range g {
		if g[i] != w[i] {
			return false
		}
	}
	return true
}
