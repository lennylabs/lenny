// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for §4.6.1 PDB-mediated warm-pod eviction. The
// §4.6.1 "Disruption protection for agent pods" contract states that the
// per-pool PodDisruptionBudget uses an integer minAvailable of minWarm-1,
// evaluated from the count of selected healthy idle pods with no /scale
// subresource. At the steady state of exactly minWarm idle pods
// (disruptionsAllowed == minWarm - (minWarm-1) == 1) a voluntary
// disruption (node drain, cluster upgrade) evicts warm idle pods one at a
// time, and the WarmPoolController proactively creates a replacement pod
// immediately to restore minWarm. The tier-2 unit test asserts the PDB
// object's fields; this test exercises the enforcement path with a real
// eviction against the live Kind warm pool.

package tier8_chaos_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// evictionResult classifies the outcome of an eviction-subresource POST
// against an agent pod.
type evictionResult int

const (
	// evictionAdmitted means the API server accepted the eviction
	// (the PDB budget allowed the voluntary disruption).
	evictionAdmitted evictionResult = iota
	// evictionBlocked means the API server rejected the eviction with
	// 429 TooManyRequests because admitting it would violate the PDB.
	evictionBlocked
	// evictionOther is any other outcome (pod already gone, RBAC, a
	// transport error); the raw output carries the detail.
	evictionOther
)

// evictPod issues a policy/v1 Eviction against the named agent pod via the
// pod's eviction subresource and classifies the result. It is the live
// analogue of a node-drain's per-pod eviction: a node drain evicts each
// pod through exactly this subresource, so the PDB budget the drain
// observes is the budget this call observes.
func evictPod(t *testing.T, c *kind.Cluster, ns, pod string) (evictionResult, string) {
	t.Helper()
	body := fmt.Sprintf(
		`{"apiVersion":"policy/v1","kind":"Eviction","metadata":{"name":%q,"namespace":%q}}`,
		pod, ns,
	)
	file := filepath.Join(t.TempDir(), "eviction.json")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatalf("writing eviction body: %v", err)
	}
	out, err := c.KubectlOut(
		t,
		"create", "--raw",
		fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/eviction", ns, pod),
		"-f", file,
	)
	switch {
	case err == nil:
		return evictionAdmitted, out
	case strings.Contains(out, "disruption budget") || strings.Contains(out, "TooManyRequests"):
		return evictionBlocked, out
	default:
		return evictionOther, out
	}
}

// idleReadyPodsByPool returns, per warm pool, the names of the managed
// agent pods that carry the lenny.dev/state: idle label and are Ready.
// These are exactly the pods the §4.6.1 PDB selects
// (lenny.dev/pool + lenny.dev/state: idle), so a pool whose idle count
// equals its minWarm (>= 2) starts at disruptionsAllowed == 1 and can
// exercise the minAvailable = minWarm-1 one-at-a-time enforcement.
func idleReadyPodsByPool(t *testing.T, c *kind.Cluster) map[string][]string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "pods",
		"-l", "lenny.dev/managed=true,lenny.dev/state=idle",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\t\"}"+
			"{.metadata.labels.lenny\\.dev/pool}{\"\\t\"}"+
			"{.status.conditions[?(@.type==\"Ready\")].status}{\"\\n\"}{end}",
	)
	if err != nil {
		return nil
	}
	byPool := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" {
			continue
		}
		if strings.TrimSpace(fields[2]) != "True" {
			continue
		}
		byPool[fields[1]] = append(byPool[fields[1]], fields[0])
	}
	return byPool
}

// warmPDBMinAvailable returns the .spec.minAvailable of the named warm-pod
// PodDisruptionBudget in the agent namespace, plus whether the PDB exists.
// The value is the integer minAvailable the §4.6.1 mechanism sets; an
// absent PDB (a pool below minWarm 2) returns ok == false.
func warmPDBMinAvailable(t *testing.T, c *kind.Cluster, name string) (string, bool) {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "pdb", name,
		"-o", "jsonpath={.spec.minAvailable}",
	)
	if err != nil {
		return "", false
	}
	v := strings.TrimSpace(out)
	if v == "" {
		return "", false
	}
	return v, true
}

// poolMinWarm returns the .spec.minWarm of the SandboxWarmPool CR whose
// name matches the pool label (reconcilePDB sets LabelPool to pool.Name,
// so the pool label value is the CR name), plus whether the read
// succeeded. minWarm is the steady-state idle target the PDB's
// minAvailable is derived from (minAvailable = minWarm-1).
func poolMinWarm(t *testing.T, c *kind.Cluster, pool string) (int, bool) {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandboxwarmpool", pool,
		"-o", "jsonpath={.spec.minWarm}",
	)
	if err != nil {
		return 0, false
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(out))
	if convErr != nil {
		return 0, false
	}
	return n, true
}

// spec: 4.6.1
// diagnosis: §4.6.1 "Disruption protection for agent pods" — the per-pool
// PDB (integer minAvailable = minWarm-1, selector lenny.dev/state: idle)
// must admit a voluntary eviction of a warm idle pod one at a time at the
// steady state of exactly minWarm idle pods (disruptionsAllowed ==
// minWarm - (minWarm-1) == 1), and the WarmPoolController must proactively
// create a replacement to restore minWarm. The test selects a pool whose
// idle count equals its minWarm (>= 2) so the budget starts at exactly one
// allowed disruption, evicts one idle pod through the eviction subresource
// (the same path a node drain uses), asserts the PDB admits it, asserts a
// concurrent second eviction is blocked while the first pod is
// unavailable, and asserts the pool replenishes to its pre-eviction idle
// count. A failure means either the PDB deadlocks warm-pod eviction (the
// node-drain stall a maxUnavailable budget causes because the idle pods'
// Sandbox owner has no /scale subresource) or the controller did not
// replace the evicted pod.
func TestWarmPodEvictionProactiveReplacement(t *testing.T) {
	c := kind.InstallLenny(t)
	kind.RequireAgentWorkload(t, c)

	// Pick a pool that sits at its steady state: idle count == minWarm,
	// with minWarm >= 2. At that state disruptionsAllowed ==
	// minWarm - (minWarm-1) == 1, so the first eviction is admitted and a
	// concurrent second is deterministically blocked. A pool holding
	// surplus idle pods above minWarm would allow more than one concurrent
	// eviction (§4.6.1), which would make the second-blocked assertion
	// nondeterministic, so those pools are not selected.
	byPool := idleReadyPodsByPool(t, c)
	var pool string
	var idle []string
	var minWarm int
	for p, pods := range byPool {
		mw, ok := poolMinWarm(t, c, p)
		if !ok || mw < 2 || len(pods) != mw {
			continue
		}
		if len(pods) > len(idle) {
			pool, idle, minWarm = p, pods, mw
		}
	}
	if pool == "" {
		t.Skip("precondition not met: no warm pool sits at its steady state (Ready idle count == " +
			"minWarm >= 2) to exercise minAvailable = minWarm-1 one-at-a-time eviction")
	}
	t.Logf("exercising pool %s at steady state: %d Ready idle pods == minWarm %d: %v",
		pool, len(idle), minWarm, idle)

	// The §4.6.1 PDB MUST use an integer minAvailable of minWarm-1. Confirm
	// the object the eviction path is enforced against before injecting the
	// disruption.
	pdb := pool + "-warm"
	wantMin := strconv.Itoa(minWarm - 1)
	if ma, ok := warmPDBMinAvailable(t, c, pdb); !ok {
		t.Skipf("precondition not met: pool %s has no PDB %s (the PDB is optional per §4.6.1)", pool, pdb)
	} else if ma != wantMin {
		t.Fatalf("PDB %s minAvailable = %q, want %q (§4.6.1 mandates an integer minAvailable of minWarm-1, "+
			"minWarm = %d)", pdb, ma, wantMin, minWarm)
	}

	preCount := countIdlePodsInPool(t, c, pool)

	// Inject: evict the first idle pod through the eviction subresource.
	// spec: §4.6.1 — an integer minAvailable of minWarm-1 admits exactly
	// one eviction at the steady state of minWarm idle pods
	// (disruptionsAllowed == minWarm - (minWarm-1) == 1), so this first
	// eviction must be admitted.
	res, out := evictPod(t, c, agentNamespace, idle[0])
	if res != evictionAdmitted {
		t.Fatalf("§4.6.1 violation: eviction of warm idle pod %s was not admitted (%v): %s\n"+
			"the minAvailable = minWarm-1 PDB must admit a voluntary disruption at steady state; a rejection "+
			"here deadlocks node drains of warm pods", idle[0], res, strings.TrimSpace(out))
	}
	t.Logf("evicted warm idle pod %s (admitted by PDB %s)", idle[0], pdb)

	// Assert one-at-a-time: while the first pod is unavailable, a second
	// concurrent eviction must be blocked. The eviction admission
	// decrements disruptionsAllowed to zero synchronously, so a second
	// eviction issued before a replacement becomes Ready is rejected.
	// spec: §4.6.1 — one-at-a-time voluntary disruption at steady state.
	// Break on the first observed block; fail if a second eviction is
	// instead admitted (two concurrent disruptions).
	sawBlock := false
	for i := 0; i < 10; i++ {
		second, secondOut := evictPod(t, c, agentNamespace, idle[1])
		if second == evictionAdmitted {
			t.Fatalf("§4.6.1 violation: a second concurrent eviction of warm idle pod %s was admitted "+
				"while %s was still unavailable; minAvailable = minWarm-1 must permit only one voluntary "+
				"disruption at the steady state of minWarm idle pods", idle[1], idle[0])
		}
		if second == evictionBlocked {
			sawBlock = true
			break
		}
		t.Logf("second eviction attempt %d returned an unexpected result: %s", i, strings.TrimSpace(secondOut))
		time.Sleep(250 * time.Millisecond)
	}
	if !sawBlock {
		t.Errorf("§4.6.1: expected the second concurrent eviction of %s to be blocked by the "+
			"minAvailable = minWarm-1 budget while %s was unavailable, but never observed a block", idle[1], idle[0])
	}

	// Assert proactive replacement: the WarmPoolController creates a fresh
	// pod to restore the pre-eviction idle count. spec: §4.6.1 — "When a
	// warm pod is evicted, the WarmPoolController proactively creates a
	// replacement pod immediately to restore minWarm."
	reached := pollUntil(warmPoolReplenishBound, 3*time.Second, func() bool {
		return countIdlePodsInPool(t, c, pool) >= preCount
	})
	if !reached {
		t.Errorf("§4.6.1 violation: warm pool %s did not replenish to its pre-eviction idle count within %s "+
			"(idle count before eviction %d, current %d); the WarmPoolController did not proactively "+
			"replace the evicted warm pod",
			pool, warmPoolReplenishBound, preCount, countIdlePodsInPool(t, c, pool))
	} else {
		t.Logf("§4.6.1: warm pool %s replenished to %d idle pod(s) after the eviction", pool, preCount)
	}
}
