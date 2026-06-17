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
// The scenario requires the live WarmPoolController leader running its GC
// loop against the cluster with the Postgres-backed active-session lookup
// wired (no session row for the pod, so the pod has no active session and the
// claim is reclaimable). It skips with a precise diagnosis when the
// controller Deployment is not Ready.

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

// spec: 4.6.1 (orphaned SandboxClaim detection, live binding-state drain),
// 6.10 (recycling claim with no holdExpiresAt reclaimed by draining), 3.3
// (drain rather than return-to-idle; fail-closed on coordinating-gateway
// crash).
// diagnosis: the §4.6.1 binding-state-aware orphan GC did not reclaim a
// `recycling` claim left with no holdExpiresAt (the coordinating-gateway
// crash during the scrub wait). A failure means a pod stuck mid-scrub is
// stranded forever, because the gateway-side scrub-report timeout died with
// the crashed replica and the GC's reserved predicate and the
// sdkConnectTimeoutSeconds watchdog cannot reach a claim in this state. The
// pod must drain (not return to idle) so an unscrubbed pod is never re-pooled.
func TestOrphanGCDrainsRecyclingClaimAfterGatewayCrash(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, lennyControllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready before the test",
			lennyControllerDeployment)
	}

	const sandboxRef = "chaos-orphan-recycling-sandbox"
	const claimName = "chaos-orphan-recycling-claim"

	// A Sandbox the orphan GC can transition. It starts claimed, the phase a
	// pod sits in while its `recycling` claim's whole-pod scrub runs.
	sandbox := sandboxManifest(sandboxRef, agentNamespace)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, sandbox) })
	if out, err := c.ApplyStdin(t, sandbox); err != nil {
		t.Fatalf("failed to create the target Sandbox: %v\n%s", err, out)
	}
	// Stamp the Sandbox claimed via the status subresource so the occupancy
	// projection has an occupied phase to drain from.
	if out, err := c.KubectlOut(t, "-n", agentNamespace, "patch", "sandbox", sandboxRef,
		"--subresource=status", "--type=merge", "-p", `{"status":{"phase":"claimed"}}`); err != nil {
		t.Fatalf("failed to stamp the Sandbox claimed: %v\n%s", err, out)
	}

	// Create the claim, then patch its status to the coordinating-gateway-crash
	// residue: `recycling` with an aged binding-state-transition time and no
	// holdExpiresAt and no rewarmStartedAt.
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
			"within the sweep window; a coordinating-gateway crash during the scrub wait must be "+
			"recovered by draining the pod", claimName)
	}

	// The pod must drain (the live-state reclaim), not return to idle: an
	// unscrubbed occupied pod returned to the pool would break the
	// scrub-before-idle invariant.
	phase, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandbox", sandboxRef,
		"-o", "jsonpath={.status.phase}")
	if err != nil {
		t.Fatalf("failed to read the Sandbox phase after reclaim: %v\n%s", err, phase)
	}
	switch strings.TrimSpace(phase) {
	case "draining", "terminated":
		t.Logf("recycling claim reclaimed by draining; Sandbox %s is %q", sandboxRef, strings.TrimSpace(phase))
	case "idle":
		t.Errorf("§3.3 violation: Sandbox %s returned to idle after a recycling-claim reclaim; an "+
			"unscrubbed pod must drain, not re-pool", sandboxRef)
	default:
		t.Errorf("Sandbox %s phase = %q after reclaim, want draining or terminated", sandboxRef, strings.TrimSpace(phase))
	}
}
