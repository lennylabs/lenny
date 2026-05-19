// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests that drive a live gateway session onto the e2e
// warm pool through the sessiondriver harness. The harness brings up
// the Kind cluster (skipping when prerequisites are absent), port-
// forwards the gateway Service, and provides the §15.1 session-
// lifecycle operations the tests need.
//
// The scenarios in this file all share the same shape: stand a session
// up on a warm pod, inject a failure against the pod, and assert the
// recovery behaviour the spec requires. Each test cleans up the
// synthetic session and tenant on exit.

package tier8_chaos_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// liveSessionTenant is the synthetic tenant the live-session chaos
// tests bootstrap. Each test reuses the same tenant ID; the driver
// best-effort deletes it on Close.
const liveSessionTenant = "chaos-live-session-tenant"

// warmPoolReplenishBound is the time a live-session chaos test allows
// for the WarmPoolController to bring the pool back to its minWarm
// target after a pod is killed. The e2e workload's minWarm is 1, so a
// single replacement pod needs to schedule and reach Ready; 3 minutes
// covers image-fetch on a fresh node plus probe convergence.
const warmPoolReplenishBound = 3 * time.Minute

// spec: 12.8
// diagnosis: §12.8 / §4.6.1 — the test drives a session onto a warm
// pod, deletes the bound agent pod, and asserts the WarmPoolController
// replenishes the pool to minWarm. Skips when the e2e gateway runs
// without --agent-namespace (the K8s pod binder is nil, so no
// SandboxClaim is written). A failure when the binder IS wired means
// the pool stayed degraded.
func TestPodKillDuringActiveSession(t *testing.T) {
	d := sessiondriver.New(t)
	c := d.Cluster()

	// The agent workload must be Ready before the harness drives a
	// session onto it. RequireAgentWorkload skips the test cleanly when
	// install.sh has not applied agent-workload.yaml.
	pods := kind.RequireAgentWorkload(t, c)
	if len(pods) == 0 {
		t.Skip("not implemented: §12.8 lifecycle failure / pod kill during session — agent workload absent")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	if err := d.BootstrapTenant(ctx, liveSessionTenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}

	// Drive the session onto the sidecar pool. The minimal gateway
	// returns the session in state running with the pod already claimed
	// by the §4.6 binder.
	sess, err := d.CreateAndStart(ctx, liveSessionTenant, sessiondriver.EchoRuntimeSidecar)
	if err != nil {
		t.Fatalf("create-and-start session: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = d.Terminate(ctx, liveSessionTenant, sess.ID)
	})
	t.Logf("created session %s in state %q", sess.ID, sess.State)

	// Find the bound pod via the SandboxClaim the gateway wrote when
	// the session was placed. The claim's .spec.sandboxRef is the
	// Sandbox name, which matches the agent pod name in the e2e
	// install (the Sandbox reconciler stamps the pod with the Sandbox
	// name).
	podName := lookupBoundPod(t, c, sess.ID)
	if podName == "" {
		t.Skipf("precondition not met: no SandboxClaim binding found for session %s; "+
			"the e2e gateway runs without --agent-namespace so the §4.6 K8s pod binder is nil — "+
			"the session is in-memory only and no Sandbox is claimed. Set --agent-namespace=lenny-agents "+
			"on the gateway Deployment to exercise this path.", sess.ID)
	}
	t.Logf("session %s is bound to agent pod %s", sess.ID, podName)

	// Record the pool the killed pod belonged to so the replenishment
	// assertion can scope to it.
	pool := lookupPodPool(t, c, podName)
	if pool == "" {
		t.Fatalf("could not resolve the warm pool for agent pod %s", podName)
	}
	preCount := countIdlePodsInPool(t, c, pool)

	// Kill the bound pod. The Sandbox CR survives (the WarmPoolController
	// observes the Sandbox going to a non-running state and replenishes
	// the pool), and the SandboxClaim survives by §4.6 contract — no
	// ownerReference so the session can be reassigned.
	if out, err := c.KubectlOut(t, "-n", agentNamespace,
		"delete", "pod", podName, "--grace-period=0", "--force"); err != nil {
		t.Fatalf("delete agent pod %s: %v\n%s", podName, err, out)
	}
	t.Logf("deleted agent pod %s", podName)

	// Assert the pool replenishes back to its minWarm target. The
	// WarmPoolController spawns a fresh Sandbox + pod when the running
	// idle pod count drops below minWarm.
	reached := pollUntil(warmPoolReplenishBound, 3*time.Second, func() bool {
		return countIdlePodsInPool(t, c, pool) >= 1
	})
	if !reached {
		t.Errorf("§12.8 violation: warm pool %s did not replenish to minWarm within %s after "+
			"the bound pod was killed (idle count before kill %d, current %d); the §4.6.1 "+
			"WarmPoolController did not replace the lost pod",
			pool, warmPoolReplenishBound, preCount, countIdlePodsInPool(t, c, pool))
	} else {
		t.Logf("§12.8: warm pool %s replenished to minWarm — pool has %d idle pod(s) again",
			pool, countIdlePodsInPool(t, c, pool))
	}

	// Soft signal: the session row should still be reachable via the
	// gateway. A 404 here would mean the gateway dropped the session on
	// pod loss, which is not the §4.6 contract (a SandboxClaim survives
	// pod deletion so the session can be reassigned).
	if got, err := d.GetSession(ctx, liveSessionTenant, sess.ID); err != nil {
		t.Logf("§12.8: GET session after pod kill returned an error: %v", err)
	} else {
		t.Logf("§12.8: GET session after pod kill returned state %q", got.State)
	}
}

// lookupBoundPod returns the Sandbox name (which is also the agent pod
// name in the e2e install) bound to sessionID via a SandboxClaim, or
// "" when no claim exists for the session yet. The claim is written by
// the gateway's §4.6 binder during session start.
func lookupBoundPod(t *testing.T, c *kind.Cluster, sessionID string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandboxclaim",
		"-o", "jsonpath={range .items[*]}{.spec.sessionId}{\"\\t\"}{.spec.sandboxRef}{\"\\n\"}{end}",
	)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			continue
		}
		if fields[0] == sessionID {
			return fields[1]
		}
	}
	return ""
}

// lookupPodPool returns the §4.6 pool an agent pod belongs to, read
// from the pod's lenny.dev/pool label.
func lookupPodPool(t *testing.T, c *kind.Cluster, podName string) string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "pod", podName,
		"-o", "jsonpath={.metadata.labels.lenny\\.dev/pool}",
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// countIdlePodsInPool returns the number of Sandbox CRs in pool whose
// .status.phase is idle. The §4.6.1 WarmPoolController maintains
// minWarm idle Sandboxes per pool.
func countIdlePodsInPool(t *testing.T, c *kind.Cluster, pool string) int {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandbox",
		"-l", "lenny.dev/pool="+pool,
		"-o", "jsonpath={range .items[*]}{.status.phase}{\"\\n\"}{end}",
	)
	if err != nil {
		return -1
	}
	count := 0
	for _, line := range strings.Fields(out) {
		if line == "idle" {
			count++
		}
	}
	return count
}
