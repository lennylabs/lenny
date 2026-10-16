// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind test for the TESTING.md §13.6 warm-pool pod lifecycle against
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
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 4.7 (Runtime Adapter), 5.3 (Isolation Profiles), 6.2 (Pod State
// Machine), 4.6.1 (Warm Pool Controller (Pod Lifecycle))
//
// diagnosis: the TESTING.md §13.6 warm-pool pod lifecycle is exercised against
// the live agent-pod workload install.sh applies. The workload
// declares two SandboxWarmPools, one per §4.7 deployment model. The
// test asserts both models are represented, that each pool warmed a
// Ready pod whose container set matches its model (sidecar: an
// adapter and a runtime container; embedded: one runtime container),
// that every pod has the §5.3 isolation RuntimeClass stamped, and
// that the backing Sandbox is idle. A pod left claimed by an earlier
// run's SandboxClaim is reclaimed by the §4.6.1 orphan-claim GC before
// the assertions run; a pod still claimed afterwards means either the GC
// is not running (check the lenny-controller log for the registration
// line and its --agent-namespaces argument) or a live session holds the
// pod. The claim, assign, drain, and return transitions run in the
// tier-2/3 component suites.
func TestPodLifecycle(t *testing.T) {
	c := kind.InstallLenny(t)
	awaitOrphanedWorkloadClaimsReclaimed(t, c)
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

// chartClaimOrphanTimeout is the chart default of the lenny-controller
// --claim-orphan-timeout flag: a live or empty-status SandboxClaim older than
// this with no active session is an orphan the §4.6.1 GC reclaims.
const chartClaimOrphanTimeout = 5 * time.Minute

// orphanReclaimBudget bounds the start-time wait for the GC: two 60-second
// sweep intervals plus the drain and pod-termination latency.
const orphanReclaimBudget = 4 * time.Minute

// workloadClaim is one SandboxClaim on a workload pod.
type workloadClaim struct {
	Name, SandboxRef, Phase string
	OrphanKey               time.Time
}

// awaitOrphanedWorkloadClaimsReclaimed is TestPodLifecycle's start-time sweep
// (check 6 of the environment preflight in .claude/rules/test-coverage.md). A
// session test killed before its cleanup, or one whose coordinating replica
// was killed, leaves its SandboxClaim bound with no session behind it, and
// the pod stays claimed. The sweep's scope is the fixture's own pods, the
// lenny.dev/managed=true agent pods RequireAgentWorkload returns. It selects
// the claims on those pods whose §4.6.1 orphan key is older than the orphan
// timeout, logs them, and waits up to orphanReclaimBudget for the platform's
// orphan-claim GC to reclaim each one and for the drained pod to terminate.
// It deletes nothing itself: reclaiming a claim the test cannot prove
// orphaned (it has no session-store access) belongs to the GC. The sweep
// never fails the test; the idle assertion that follows reports any pod the
// GC did not release.
func awaitOrphanedWorkloadClaimsReclaimed(t *testing.T, c *kind.Cluster) {
	t.Helper()
	aged := agedWorkloadClaims(t, c, time.Now())
	if len(aged) == 0 {
		return
	}
	names := make([]string, 0, len(aged))
	for _, cl := range aged {
		names = append(names, cl.Name+" ("+cl.Phase+", since "+cl.OrphanKey.UTC().Format(time.RFC3339)+")")
	}
	t.Logf("start-time sweep: %d SandboxClaim(s) on workload pods are older than the %s orphan timeout; "+
		"waiting up to %s for the §4.6.1 orphan-claim GC: %s",
		len(aged), chartClaimOrphanTimeout, orphanReclaimBudget, strings.Join(names, ", "))

	var remaining []string
	pollUntilCondition(orphanReclaimBudget, 10*time.Second, func() bool {
		remaining = remaining[:0]
		for _, cl := range aged {
			if objectExists(t, c, "sandboxclaim", cl.Name) || objectExists(t, c, "pod", cl.SandboxRef) {
				remaining = append(remaining, cl.Name)
			}
		}
		return len(remaining) == 0
	})
	t.Logf("start-time sweep: the GC reclaimed %d of %d aged claim(s) and their pods terminated; still present: %v",
		len(aged)-len(remaining), len(aged), remaining)
}

// agedWorkloadClaims lists the SandboxClaims on managed agent pods whose
// §4.6.1 orphan key (the binding-state transition time, or the creation time
// before a binding state is written) is older than the orphan timeout. A
// claim in a terminal binding state is excluded, because the GC does not
// reclaim it. A list failure yields no claims, keeping the sweep best-effort.
func agedWorkloadClaims(t *testing.T, c *kind.Cluster, now time.Time) []workloadClaim {
	t.Helper()
	podsOut, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "pods", "-l", "lenny.dev/managed=true",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		t.Logf("start-time sweep: listing workload pods failed (continuing): %v\n%s", err, podsOut)
		return nil
	}
	managed := map[string]bool{}
	for _, name := range strings.Fields(podsOut) {
		managed[name] = true
	}
	out, err := c.KubectlOut(t, "-n", "lenny-agents", "get", "sandboxclaims", "-o",
		"jsonpath={range .items[*]}{.metadata.name}{\"\\t\"}{.spec.sandboxRef}{\"\\t\"}"+
			"{.status.phase}{\"\\t\"}{.status.bindingStateTransitionTime}{\"\\t\"}"+
			"{.metadata.creationTimestamp}{\"\\n\"}{end}")
	if err != nil {
		t.Logf("start-time sweep: listing SandboxClaims failed (continuing): %v\n%s", err, out)
		return nil
	}
	var aged []workloadClaim
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 || !managed[f[1]] {
			continue
		}
		if f[2] == "released" || f[2] == "failed" {
			continue
		}
		keyText := f[3]
		if keyText == "" {
			keyText = f[4]
		}
		key, perr := time.Parse(time.RFC3339, keyText)
		if perr != nil || now.Sub(key) < chartClaimOrphanTimeout {
			continue
		}
		aged = append(aged, workloadClaim{Name: f[0], SandboxRef: f[1], Phase: f[2], OrphanKey: key})
	}
	return aged
}

// objectExists reports whether a namespaced lenny-agents object exists. A
// read error counts as present, so the sweep keeps waiting rather than
// reporting a reclaim it did not observe.
func objectExists(t *testing.T, c *kind.Cluster, kindName, name string) bool {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", "lenny-agents", "get", kindName, name, "--ignore-not-found", "-o", "name")
	return err != nil || strings.TrimSpace(out) != ""
}

// assertPodLifecycle checks one warm agent pod against the §4.7
// contract for its pool's deployment model.
func assertPodLifecycle(t *testing.T, c *kind.Cluster, p kind.AgentPod) {
	t.Helper()

	containers := podContainers(t, c, p.Name)
	// The TESTING.md §12.9.8 egress-capture sidecar is optional per
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
// (the TESTING.md §12.9.8 egress-capture container is opt-in per template) so
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
