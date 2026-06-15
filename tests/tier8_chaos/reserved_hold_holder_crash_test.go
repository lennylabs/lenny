// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §3.2 reserved-hold holder-crash recovery path.
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
// residue directly: a SandboxClaim in the `reserved` binding state with a
// holdExpiresAt in the past, the exact state a crashed holder leaves behind.
// The test skips with a precise diagnosis when the controller Deployment is
// not Ready.

package tier8_chaos_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// spec: 3.2 (reserved hold, precondition-guarded expiry DELETE, holder-crash
// recovery via orphan GC), 4.6.1 (reserved-claim orphan GC reclaim after
// holdExpiresAt plus grace, return-to-idle), 4.6.3 (holdExpiresAt status
// field).
// diagnosis: the §4.6.1 orphan GC did not reclaim a `reserved` claim whose
// holding gateway crashed during the hold window (holdExpiresAt already
// passed). A failure means a scrubbed, SDK-warm pod is stranded occupied
// forever after the holder crash, because the gateway's in-process hold-TTL
// timer died with the crashed replica and only the leader-elected GC can
// issue the precondition-guarded DELETE that returns the pod to idle. The
// pod must return to idle (not drain): a reserved pod is already scrubbed and
// re-warmed, so the GC re-pools it rather than retiring it.
func TestOrphanGCReclaimsReservedClaimAfterHolderCrash(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, lennyControllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready before the test",
			lennyControllerDeployment)
	}

	const sandboxRef = "chaos-reserved-hold-sandbox"
	// The occupancy projection associates a claim with its Sandbox by the
	// deterministic per-pod name claim-<podName> (observeClaim), so the
	// holder-crash residue must use that name for the projection to read it
	// back during the hold window. spec: §3.2, §6.5 (claim-<podName>).
	const claimName = "claim-" + sandboxRef

	// A Sandbox the orphan GC can transition. It ends in the `reserved`
	// phase: a held pod sits in `reserved` (scrubbed and SDK-warm, pinned to
	// the tenant and excluded from idle inventory) until the hold expires and
	// the claim is deleted, at which point the occupancy projection takes the
	// reserved → idle edge. The projection drives the Sandbox to `reserved`
	// only while it observes the deterministic claim in the reserved binding
	// state, so the reserved claim is created and stamped first, then the
	// Sandbox phase is stamped last. Stamping the Sandbox before the reserved
	// claim exists would let a reconcile in the gap observe no-claim while the
	// phase is already `reserved` and take the reserved → idle edge before the
	// holder-crash residue is in place. spec: §6.2 (reserved → idle hold-expiry
	// edge).
	sandbox := sandboxManifest(sandboxRef, agentNamespace)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, sandbox) })
	if out, err := c.ApplyStdin(t, sandbox); err != nil {
		t.Fatalf("failed to create the target Sandbox: %v\n%s", err, out)
	}

	// Create the claim, then patch its status to the holder-crash residue:
	// `reserved` with a holdExpiresAt in the past. The crashed holder's
	// in-process timer never fired, so the deadline has elapsed with the claim
	// still present and the GC must reclaim it.
	claim := sandboxClaimManifest(claimName, agentNamespace, sandboxRef, "sess-reserved-hold")
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, claim) })
	if out, err := c.ApplyStdin(t, claim); err != nil {
		t.Fatalf("failed to create the reserved claim: %v\n%s", err, out)
	}
	// holdExpiresAt aged well past the default reserved-hold grace (60s) so the
	// first sweep after creation treats the hold as expired and the holder as
	// crashed. The binding-state-transition time is stamped at the same instant
	// the hold was entered.
	reserved := time.Now().Add(-30 * time.Minute).UTC()
	statusPatch := fmt.Sprintf(
		`{"status":{"phase":"reserved","bindingStateTransitionTime":%q,"holdExpiresAt":%q}}`,
		reserved.Format(time.RFC3339), reserved.Add(10*time.Second).Format(time.RFC3339),
	)
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "patch", "sandboxclaim", claimName,
		"--subresource=status", "--type=merge", "-p", statusPatch); err != nil {
		t.Fatalf("failed to stamp the reserved binding state: %v\n%s", err, out)
	}

	// Stamp the Sandbox to `reserved` last, after the reserved claim exists, so
	// the occupancy projection observes the held pod's phase and only takes the
	// reserved → idle edge once the GC's precondition-guarded DELETE removes the
	// claim.
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "patch", "sandbox", sandboxRef,
		"--subresource=status", "--type=merge", "-p", `{"status":{"phase":"reserved"}}`); err != nil {
		t.Fatalf("failed to stamp the Sandbox reserved: %v\n%s", err, out)
	}

	// The GC sweeps every 60s. Allow a couple of sweeps plus reconcile latency.
	reclaimed := pollUntil(3*time.Minute, 5*time.Second, func() bool {
		out, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxclaim", claimName,
			"--ignore-not-found", "-o", "name")
		return err == nil && strings.TrimSpace(out) == ""
	})
	if !reclaimed {
		t.Fatalf("§3.2 violation: the reserved claim %s was not reclaimed by the orphan GC "+
			"within the sweep window after holdExpiresAt plus the grace period; a holder crash "+
			"during the hold window must be recovered by the leader-elected GC", claimName)
	}

	// The pod must return to idle (the reserved reclaim): a reserved pod is
	// scrubbed and SDK-warm, so re-pooling it is safe and is the expected
	// outcome. A drain here would retire a perfectly good warm pod.
	idle := pollUntil(time.Minute, 5*time.Second, func() bool {
		phase, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandbox", sandboxRef,
			"-o", "jsonpath={.status.phase}")
		return err == nil && strings.TrimSpace(phase) == "idle"
	})
	if !idle {
		phase, _ := c.KubectlOut(t, "-n", agentNamespace, "get", "sandbox", sandboxRef,
			"-o", "jsonpath={.status.phase}")
		t.Errorf("§4.6.1 violation: Sandbox %s did not return to idle after the reserved-claim "+
			"reclaim (phase = %q); a scrubbed, SDK-warm reserved pod must re-pool, not drain",
			sandboxRef, strings.TrimSpace(phase))
	}
}
