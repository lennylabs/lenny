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
	// The §12.9.8 egress-capture sidecar is optional per
	// SandboxTemplate annotation. Filter it from the topology check
	// so the §4.7 model assertion stays focused on the runtime /
	// adapter pair the model defines.
	containersForModel := filterOutContainers(containers, "egress-capture")
	switch p.Model {
	case "sidecar":
		if !sameStringSet(containersForModel, []string{"adapter", "runtime"}) {
			t.Errorf("%s (sidecar pool %s): containers = %v, want {adapter, runtime} — "+
				"the §4.7 sidecar model runs the adapter and the runtime in separate containers",
				p.Name, p.Pool, containersForModel)
		}
	case "embedded":
		if !sameStringSet(containersForModel, []string{"runtime"}) {
			t.Errorf("%s (embedded pool %s): containers = %v, want {runtime} — "+
				"the §4.7 embedded model runs a single container whose image embeds the adapter",
				p.Name, p.Pool, containersForModel)
		}
	default:
		t.Errorf("%s: pool %s declares no recognized §4.7 deployment model", p.Name, p.Pool)
	}

	// The Sandbox reconciler stamps the §5.3 isolation RuntimeClass onto
	// every agent pod. Every reference pool on this overlay is
	// isolationProfile: standard (runc) except gvisor-echo-pool
	// (isolationProfile: sandboxed), which tests/tier5_e2e_kind/
	// gvisor_isolation_test.go covers in full; this assertion only
	// needs the right expectation per pool so it does not misreport a
	// correctly-sandboxed pod as a RuntimeClass regression.
	want := expectedRuntimeClass(p.Pool)
	if rc := podField(t, c, p.Name, "{.spec.runtimeClassName}"); rc != want {
		t.Errorf("%s: runtimeClassName = %q, want %q (§5.3 isolation profile for pool %s)", p.Name, rc, want, p.Pool)
	}

	// A warmed, unclaimed pod's Sandbox sits in the idle phase.
	if phase := sandboxPhaseForPod(t, c, p.Name); phase != "idle" {
		t.Errorf("%s: backing Sandbox phase = %q, want \"idle\" (a warm, claimable pod)", p.Name, phase)
	}

	t.Logf("%s: §4.7 %s model verified — containers %v, RuntimeClass %s, Sandbox idle",
		p.Name, p.Model, containers, want)
}

// expectedRuntimeClass returns the §5.3 RuntimeClass the Sandbox
// reconciler stamps onto a warm pod from the named pool. Every pool on
// this overlay is isolationProfile: standard (runc) except
// gvisor-echo-pool (isolationProfile: sandboxed); see
// tests/testinfra/kind/install.sh's generated bootstrap overlay.
func expectedRuntimeClass(pool string) string {
	if pool == "gvisor-echo-pool" {
		return "gvisor"
	}
	return "runc"
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

// filterOutContainers returns the input container-name list with
// any name in the drop set removed. Used to hide optional sidecars
// (the §12.9.8 egress-capture container is opt-in per template) so
// the §4.7 model assertion compares the runtime/adapter pair alone.
func filterOutContainers(in []string, drop ...string) []string {
	dropped := make(map[string]struct{}, len(drop))
	for _, d := range drop {
		dropped[d] = struct{}{}
	}
	out := make([]string, 0, len(in))
	for _, n := range in {
		if _, skip := dropped[n]; skip {
			continue
		}
		out = append(out, n)
	}
	return out
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
