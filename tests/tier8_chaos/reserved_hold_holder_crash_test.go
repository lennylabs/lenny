// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §4.6.1 reserved-hold holder-crash recovery path.
// When a gateway replica reserves a recycled pod's claim it arms an
// in-process hold-TTL expiry timer (HoldCoordinator) that issues the
// precondition-guarded DELETE returning the pod to idle. If that replica
// crashes during the hold window, the in-process timer dies with it and the
// reserved claim is left in etcd with its holdExpiresAt deadline already
// passed. The WarmPoolController's leader-elected orphan GC is then the sole
// recovery path: it reclaims the reserved claim once holdExpiresAt plus a
// grace period has passed, using the same precondition-guarded DELETE, so the
// scrubbed and SDK-warm pod returns to idle rather than being stranded
// occupied forever.
//
// This is the live-cluster companion to the §4.6.1 binding-state-aware GC
// component tests (reclaimReserved) and the recycling-claim crash chaos test
// (orphan_gc_recovery_test.go). It injects the reserved-hold holder-crash
// residue directly on a real warm pod: a SandboxClaim named claim-<podName> in
// the `reserved` binding state whose holdExpiresAt has just passed, the exact
// state a crashed holder leaves behind. The occupancy projection moves the
// Sandbox to reserved from the claim and back to idle once the GC deletes it;
// the fixture writes no Sandbox status itself (see orphan_gc_recovery_test.go
// for why). A real pod is required for the idle assertion: a Sandbox with no
// backing pod is retired from idle by the Sandbox-to-Pod reconciler at once.
// The test skips with a precise diagnosis when the controller Deployment is
// not Ready or the workload has no idle warm pod.

package tier8_chaos_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle), "Reserved hold (claim
// retention across same-tenant sessions)" and "Orphaned SandboxClaim
// detection"), 4.6.3 (CRD Field Ownership and Write Boundaries, the
// holdExpiresAt status field), 6.2 (Pod State Machine, reserved → idle).
// diagnosis: the §4.6.1 orphan GC did not reclaim a `reserved` claim whose
// holding gateway crashed during the hold window (holdExpiresAt already
// passed). A failure means a scrubbed, SDK-warm pod is stranded occupied
// forever after the holder crash, because the gateway's in-process hold-TTL
// timer died with the crashed replica and only the leader-elected GC can
// issue the precondition-guarded DELETE that returns the pod to idle. The
// pod must return to idle (not drain): a reserved pod is already scrubbed and
// re-warmed, so the GC re-pools it rather than retiring it. When the claim is
// never reclaimed, first confirm the lenny-controller log carries the
// "orphan-claim GC registered" line and the Deployment renders
// --agent-namespaces.
func TestOrphanGCReclaimsReservedClaimAfterHolderCrash(t *testing.T) {
	c := kind.InstallLenny(t)
	sweepClaimFixtureLeftovers(t, c)

	if !deploymentReady(t, c, lennyControllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready before the test",
			lennyControllerDeployment)
	}

	sandboxRef := idleWarmSandbox(t, c, orphanGCFixturePool)
	// The occupancy projection associates a claim with its Sandbox by the
	// deterministic per-pod name claim-<podName> (observeClaim). spec: §4.6.1
	// (claim-<podName>).
	claimName := "claim-" + sandboxRef
	t.Logf("injecting the reserved-hold residue on warm pod %s", sandboxRef)

	// Create the claim, then patch its status to the holder-crash residue:
	// `reserved` with a holdExpiresAt that has just passed. The crashed
	// holder's in-process timer never fired, so the deadline elapses with the
	// claim still present. The GC waits the reserved-hold grace (60s) past
	// holdExpiresAt before it reclaims, which leaves the occupancy projection
	// time to move the Sandbox to reserved first, so the test observes the
	// reserved → idle edge rather than a claim deleted before the pod was held.
	claim := sandboxClaimManifest(claimName, agentNamespace, sandboxRef, "sess-reserved-hold")
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, claim) })
	if out, err := c.ApplyStdin(t, claim); err != nil {
		t.Fatalf("failed to create the reserved claim: %v\n%s", err, out)
	}
	now := time.Now().UTC()
	statusPatch := fmt.Sprintf(
		`{"status":{"phase":"reserved","bindingStateTransitionTime":%q,"holdExpiresAt":%q}}`,
		now.Add(-10*time.Second).Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "patch", "sandboxclaim", claimName,
		"--subresource=status", "--type=merge", "-p", statusPatch); err != nil {
		t.Fatalf("failed to stamp the reserved binding state: %v\n%s", err, out)
	}

	// The occupancy projection holds the pod: reserved, excluded from idle
	// inventory. spec: §6.2.
	if !pollUntil(30*time.Second, 2*time.Second, func() bool { return sandboxPhase(t, c, sandboxRef) == "reserved" }) {
		t.Fatalf("Sandbox %s did not project reserved from its reserved claim (phase %q); the "+
			"holder-crash residue is not in place", sandboxRef, sandboxPhase(t, c, sandboxRef))
	}

	// holdExpiresAt plus the 60s grace, then up to two 60s sweeps plus
	// reconcile latency.
	reclaimed := pollUntil(3*time.Minute, 5*time.Second, func() bool {
		out, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxclaim", claimName,
			"--ignore-not-found", "-o", "name")
		return err == nil && strings.TrimSpace(out) == ""
	})
	if !reclaimed {
		t.Fatalf("violation: the reserved claim %s was not reclaimed by the orphan GC "+
			"within the sweep window after holdExpiresAt plus the grace period; a holder crash "+
			"during the hold window must be recovered by the leader-elected GC", claimName)
	}

	// The pod must return to idle (the reserved reclaim): a reserved pod is
	// scrubbed and SDK-warm, so re-pooling it is safe and is the expected
	// outcome. A drain here would retire a good warm pod.
	idle := pollUntil(time.Minute, 2*time.Second, func() bool { return sandboxPhase(t, c, sandboxRef) == "idle" })
	if !idle {
		t.Errorf("§4.6.1 violation: Sandbox %s did not return to idle after the reserved-claim "+
			"reclaim (phase = %q); a scrubbed, SDK-warm reserved pod must re-pool, not drain",
			sandboxRef, sandboxPhase(t, c, sandboxRef))
	}
}
