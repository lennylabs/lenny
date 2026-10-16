// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §4.6.1 binding-state-aware orphan-SandboxClaim
// GC recovery path. It injects the coordinating-gateway-crash signature on
// the live cluster: a SandboxClaim left in the `recycling` binding state
// with no holdExpiresAt and an aged binding-state-transition time, the exact
// residue a gateway replica leaves when it crashes during the recycling
// whole-pod scrub wait. The gateway-side scrub-report timeout dies with the
// replica, so the WarmPoolController's leader-elected GarbageCollect loop is
// the sole recovery path. The test asserts the loop reclaims the claim by
// draining the pod (not returning it to idle), so a pod stuck mid-scrub is
// retired rather than re-pooled unscrubbed.
//
// The residue is placed on a real warm pod of the agent workload: the claim
// carries the deterministic claim-<podName> name, and the occupancy
// projection, the sole writer of Sandbox.status.phase, moves the Sandbox to
// its occupied phase from the claim's binding state. The fixture writes no
// Sandbox status itself. A status write from another field manager would
// co-own status.phase and make every WarmPoolController server-side apply on
// the Sandbox, the GC's drain included, fail with a field-manager conflict,
// which is not a state the platform produces.
//
// The scenario requires the live WarmPoolController leader running its GC
// loop against the cluster with the Postgres-backed active-session lookup
// wired (no session row for the pod, so the pod has no active session and the
// claim is reclaimable). It skips with a precise diagnosis when the
// controller Deployment is not Ready or the workload has no idle warm pod.

package tier8_chaos_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// lennyControllerDeployment runs the WarmPoolController (and its
// leader-elected orphan-claim GarbageCollect loop).
const lennyControllerDeployment = "lenny-controller"

// orphanGCFixturePool is the agent-workload pool the orphan-GC fixtures take
// a warm pod from. install.sh keeps six warm members in it, so one retired
// or briefly held pod leaves the pool claimable.
const orphanGCFixturePool = "echo-pool-sidecar"

// idleWarmSandbox returns the name of a Ready, idle, unclaimed warm pod in
// pool, which is also its Sandbox's name. It skips the test when the pool
// has none, because the orphan-GC scenarios need a live pod for the occupancy
// projection to act on.
func idleWarmSandbox(t *testing.T, c *kind.Cluster, pool string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", agentNamespace, "get", "pods",
		"-l", "lenny.dev/managed=true,lenny.dev/pool="+pool,
		"-o", `jsonpath={range .items[?(@.status.phase=="Running")]}{.metadata.name}{"\n"}{end}`)
	if err != nil {
		t.Skipf("not yet applicable: precondition not met: cannot list %s pods: %v\n%s", pool, err, out)
	}
	for _, name := range strings.Fields(out) {
		phase, perr := c.KubectlOut(t, "-n", agentNamespace, "get", "sandbox", name,
			"-o", "jsonpath={.status.phase}")
		if perr != nil || strings.TrimSpace(phase) != "idle" {
			continue
		}
		claim, cerr := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxclaim", "claim-"+name,
			"--ignore-not-found", "-o", "name")
		if cerr != nil || strings.TrimSpace(claim) != "" {
			continue
		}
		return name
	}
	t.Skipf("not yet applicable: precondition not met: pool %s has no Ready, idle, unclaimed warm pod", pool)
	return ""
}

// sandboxPhase reads a Sandbox's status.phase, or "" on a read error.
func sandboxPhase(t *testing.T, c *kind.Cluster, name string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandbox", name, "-o", "jsonpath={.status.phase}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle), "Orphaned SandboxClaim
// detection", predicate 1: live binding states), 6.2 (Pod State Machine), 5.2
// (Pool Configuration and Execution Modes, scrub-before-idle).
// diagnosis: the §4.6.1 binding-state-aware orphan GC did not reclaim a
// `recycling` claim left with no holdExpiresAt (the coordinating-gateway
// crash during the scrub wait). A failure means a pod stuck mid-scrub is
// stranded forever, because the gateway-side scrub-report timeout died with
// the crashed replica and the GC's reserved predicate and the
// sdkConnectTimeoutSeconds watchdog cannot reach a claim in this state. The
// pod must drain (not return to idle) so an unscrubbed pod is never re-pooled.
// When the claim is never reclaimed, first confirm the lenny-controller log
// carries the "orphan-claim GC registered" line and the Deployment renders
// --agent-namespaces.
func TestOrphanGCDrainsRecyclingClaimAfterGatewayCrash(t *testing.T) {
	c := kind.InstallLenny(t)
	sweepClaimFixtureLeftovers(t, c)

	if !deploymentReady(t, c, lennyControllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready before the test",
			lennyControllerDeployment)
	}

	sandboxRef := idleWarmSandbox(t, c, orphanGCFixturePool)
	// The occupancy projection reads the claim by the deterministic per-pod
	// name. spec: §4.6.1 (claim-<podName>).
	claimName := "claim-" + sandboxRef
	t.Logf("injecting the recycling-claim residue on warm pod %s", sandboxRef)

	// Create the claim, then patch its status to the coordinating-gateway-crash
	// residue: `recycling` with an aged binding-state-transition time and no
	// holdExpiresAt and no rewarmStartedAt. The occupancy projection moves the
	// Sandbox to claimed, the phase a pod sits in while its scrub runs.
	claim := sandboxClaimManifest(claimName, agentNamespace, sandboxRef, "sess-orphan-recycling")
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, claim) })
	if out, err := c.ApplyStdin(t, claim); err != nil {
		t.Fatalf("failed to create the recycling claim: %v\n%s", err, out)
	}
	// Aged well past the default claimOrphanTimeout (5m) so the first sweep
	// after creation treats the claim as orphaned.
	aged := time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	statusPatch := fmt.Sprintf(
		`{"status":{"phase":"recycling","bindingStateTransitionTime":%q}}`, aged,
	)
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "patch", "sandboxclaim", claimName,
		"--subresource=status", "--type=merge", "-p", statusPatch); err != nil {
		t.Fatalf("failed to stamp the recycling binding state: %v\n%s", err, out)
	}

	// The GC sweeps every 60s. Allow a couple of sweeps plus reconcile latency.
	reclaimed := pollUntil(3*time.Minute, 5*time.Second, func() bool {
		out, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxclaim", claimName,
			"--ignore-not-found", "-o", "name")
		return err == nil && strings.TrimSpace(out) == ""
	})
	if !reclaimed {
		t.Fatalf("§4.6.1 violation: the recycling claim %s was not reclaimed by the orphan GC "+
			"within the sweep window (Sandbox phase %q); a coordinating-gateway crash during the "+
			"scrub wait must be recovered by draining the pod", claimName, sandboxPhase(t, c, sandboxRef))
	}

	// The pod must drain (the live-state reclaim), not return to idle: an
	// unscrubbed occupied pod returned to the pool would break the
	// scrub-before-idle invariant.
	switch phase := sandboxPhase(t, c, sandboxRef); phase {
	case "draining", "terminated":
		t.Logf("recycling claim reclaimed by draining; Sandbox %s is %q", sandboxRef, phase)
	case "idle":
		t.Errorf("violation: Sandbox %s returned to idle after a recycling-claim reclaim; an "+
			"unscrubbed pod must drain, not re-pool", sandboxRef)
	default:
		// The drained Sandbox can be retired and garbage-collected before the
		// read; an absent Sandbox is the end of the drain path.
		out, _ := c.KubectlOut(t, "-n", agentNamespace, "get", "sandbox", sandboxRef, "--ignore-not-found", "-o", "name")
		if strings.TrimSpace(out) != "" {
			t.Errorf("Sandbox %s phase = %q after reclaim, want draining or terminated", sandboxRef, phase)
		}
	}
}
