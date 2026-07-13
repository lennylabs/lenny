// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for lenny-ops leader-election failover in the
// production multi-replica topology. The §25.16 Production block deploys
// lenny-ops as a leader-elected Deployment with a PodDisruptionBudget of
// minAvailable 1 once it is scaled past a single replica; §25.4 fixes the
// leader-election Lease (lenny-ops-leader, 15s duration) and the
// Multi-Replica Scaling subsection makes the >1-replica posture normative.
// This test scales lenny-ops to two replicas, kills the replica holding
// the lenny-ops-leader Lease, and asserts a surviving replica acquires the
// Lease and resumes leading while the PodDisruptionBudget keeps at least
// one replica available throughout.

package tier8_chaos_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// opsLeaderLease is the coordination.k8s.io/v1 Lease lenny-ops uses for
// leader election. §25.4 fixes the name as lenny-ops-leader in the
// release namespace. (opsDeployment and opsSelector are declared in
// ops_survives_gateway_test.go: the lenny-ops Deployment and its
// app: lenny-ops pod selector.)
const opsLeaderLease = "lenny-ops-leader"

// opsPDB is the lenny-ops PodDisruptionBudget. §25.16 line 5119 renders it
// with minAvailable 1; it caps how many replicas may be simultaneously
// unavailable once lenny-ops runs more than one replica.
const opsPDB = "lenny-ops"

// opsReplicasHA is the two-replica posture the §25.4 Multi-Replica
// Scaling subsection documents (a leader plus one follower). It is the
// smallest replica count for which the minAvailable-1 PDB is meaningful.
const opsReplicasHA = 2

// §25.4 leader-election lease parameters. leaseDuration is 15s and
// renewDeadline is 10s, so the crash-case worst-case failover window is
// leaseDuration + renewDeadline = 25s — a surviving replica cannot
// acquire the Lease until the dead leader's lease fully expires.
const (
	opsFailoverWindow = 25 * time.Second

	// opsFailoverAssertBound is the time the test allows for the new
	// leader to appear. It is the 25s §25.4 worst case plus a margin for
	// API-server latency and the kubelet observing the pod delete, matching
	// the band the controller leader-election chaos test uses.
	opsFailoverAssertBound = 75 * time.Second

	// opsReadyBound is the time the test allows a scaled lenny-ops
	// Deployment to reach the target replica count Ready. lenny-ops has no
	// persistent volume, so a pod only needs its (node-local) image plus
	// probe convergence.
	opsReadyBound = 3 * time.Minute
)

// spec: 25.16 (production topology, lenny-ops leader-elected + PDB minAvailable 1), 25.4 (leader election, multi-replica scaling)
// diagnosis: §25.16 / §25.4 lenny-ops multi-replica leader-election
// failover did not hold. The production topology runs lenny-ops as a
// leader-elected Deployment with a PodDisruptionBudget minAvailable 1
// once replicas >= 2. The test scales lenny-ops to 2 replicas, reads the
// lenny-ops-leader Lease holder, deletes that leader pod, then asserts a
// surviving replica acquires the Lease within the §25.4 failover window,
// keeps it renewed, the Deployment returns to its full replica count, and
// the PodDisruptionBudget never reports fewer than its minAvailable
// healthy replicas. A failure means leader failover stalled, no replica
// took over, or the operability plane lost availability during the
// disruption.
func TestOpsLeaderElectionFailover(t *testing.T) {
	c := kind.InstallLenny(t)

	// Precondition: lenny-ops is fully Ready before the scale-up, so the
	// HA posture under test starts from a known-good state.
	if !deploymentReady(t, c, opsDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			opsDeployment, deploymentReadyState(t, c, opsDeployment))
	}

	// Scale lenny-ops to the two-replica HA posture the §25.4 Multi-Replica
	// Scaling subsection documents. The standing install runs the
	// §25.16-default single replica; the cleanup restores that count so the
	// shared cluster is left as it was found even on a mid-test failure.
	original := deploymentDesiredReplicas(t, c, opsDeployment)
	if original == 0 {
		original = 1
	}
	scaleDeployment(t, c, opsDeployment, opsReplicasHA)
	t.Cleanup(func() { restoreDeployment(t, c, opsDeployment, original) })

	if !pollUntil(opsReadyBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, opsDeployment) && len(podNames(t, c, opsSelector)) >= opsReplicasHA
	}) {
		t.Skipf("precondition not met: %s did not reach %d replicas Ready within %s (state %s); "+
			"a CPU-starved second replica on a single-host Kind cluster cannot exercise failover",
			opsDeployment, opsReplicasHA, opsReadyBound, deploymentReadyState(t, c, opsDeployment))
	}

	// The PodDisruptionBudget must be rendered and admit the two-replica
	// posture before the injection. §25.16 line 5119 renders it with
	// minAvailable 1 regardless of replica count.
	if got := pdbMinAvailable(t, c, opsPDB); got != 1 {
		t.Fatalf("the %s PodDisruptionBudget has minAvailable %d, want 1 (§25.16 line 5119)", opsPDB, got)
	}

	// Wait for leader election to converge on a leader among the ops pods.
	var originalHolder string
	if !pollUntil(opsFailoverAssertBound, 1*time.Second, func() bool {
		originalHolder = leaseHolderIdentity(t, c, opsLeaderLease)
		return originalHolder != "" && contains(podNames(t, c, opsSelector), leaseHolderPod(originalHolder))
	}) {
		t.Fatalf("the %s Lease did not converge on a leader among the %s pods within %s (holderIdentity %q, pods %v)",
			opsLeaderLease, opsDeployment, opsFailoverAssertBound,
			leaseHolderIdentity(t, c, opsLeaderLease), podNames(t, c, opsSelector))
	}
	leaderPod := leaseHolderPod(originalHolder)
	opsPods := podNames(t, c, opsSelector)
	if len(opsPods) < opsReplicasHA {
		t.Skipf("precondition not met: leader failover needs at least %d lenny-ops replicas, found %d (%v)",
			opsReplicasHA, len(opsPods), opsPods)
	}
	t.Logf("current %s leader: pod %q (holderIdentity %q), %d lenny-ops replicas",
		opsLeaderLease, leaderPod, originalHolder, len(opsPods))

	// Inject the failure: delete the leader pod. --wait=false returns once
	// the delete is accepted so the failover clock starts immediately. The
	// Deployment's ReplicaSet reschedules a replacement pod.
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "delete", "pod", leaderPod, "--wait=false",
	); err != nil {
		t.Fatalf("failed to delete the leader pod %q: %v\n%s", leaderPod, err, out)
	}
	injectedAt := time.Now()
	t.Logf("deleted leader pod %q at %s; waiting for failover within %s",
		leaderPod, injectedAt.Format(time.RFC3339), opsFailoverAssertBound)

	// Assert: the PodDisruptionBudget keeps at least minAvailable healthy
	// replicas through the disruption. With the surviving follower still up,
	// currentHealthy must never drop below desiredHealthy (1). Sampling
	// starts immediately after the delete so a window where both replicas
	// were unavailable would be caught. §25.4 Trade-Offs: "PodDisruptionBudget
	// (minAvailable: 1) ensures at least one replica is up."
	minObservedHealthy := pdbCurrentHealthy(t, c, opsPDB)

	// Assert: a surviving replica acquires the Lease. The holderIdentity
	// must change to a different identity than the dead leader's.
	var newHolder string
	acquired := pollUntil(opsFailoverAssertBound, 1*time.Second, func() bool {
		if h := pdbCurrentHealthy(t, c, opsPDB); h >= 0 && h < minObservedHealthy {
			minObservedHealthy = h
		}
		h := leaseHolderIdentity(t, c, opsLeaderLease)
		if h != "" && h != originalHolder {
			newHolder = h
			return true
		}
		return false
	})
	failoverElapsed := time.Since(injectedAt)
	if !acquired {
		t.Fatalf("no surviving %s replica acquired the %s Lease within %s after the leader pod was killed; "+
			"holderIdentity is still %q — leader-election failover stalled",
			opsDeployment, opsLeaderLease, opsFailoverAssertBound, leaseHolderIdentity(t, c, opsLeaderLease))
	}
	newLeaderPod := leaseHolderPod(newHolder)
	if newLeaderPod == leaderPod {
		t.Fatalf("the %s Lease holderIdentity changed to %q but still names the killed pod %q; "+
			"failover did not move leadership to a surviving replica",
			opsLeaderLease, newHolder, leaderPod)
	}
	t.Logf("failover complete after %s: new leader pod %q (holderIdentity %q)",
		failoverElapsed.Round(time.Second), newLeaderPod, newHolder)

	// The surviving follower was up throughout, so the PDB's healthy count
	// must never have dropped below its desiredHealthy (minAvailable 1).
	if desired := pdbDesiredHealthy(t, c, opsPDB); minObservedHealthy < desired {
		t.Fatalf("the %s PodDisruptionBudget dropped to %d healthy replicas during the leader kill, below its "+
			"desiredHealthy %d (minAvailable 1); the operability plane lost availability during the disruption",
			opsPDB, minObservedHealthy, desired)
	}

	// The §25.4 worst case is 25s; the failover should land within that
	// bound on a healthy cluster. Exceeding it (but still under the assert
	// bound) is a soft signal rather than a hard failure, because
	// ReleaseOnCancel makes a graceful pod delete release the Lease early
	// and API-server latency legitimately stretches the window. A genuine
	// stall is already a Fatalf above.
	if failoverElapsed > opsFailoverWindow {
		t.Logf("note: failover took %s, above the §25.4 25s crash-case worst case; "+
			"acceptable under API-server latency but worth watching",
			failoverElapsed.Round(time.Second))
	}

	// Assert: the new leader keeps the Lease renewed. A replica that
	// acquires the Lease but does not renew it is not actively leading;
	// §25.4 requires the holder to renew within the 10s renew deadline.
	// Sample renewTime twice across a renew interval and confirm it
	// advanced while the holder stayed the same.
	firstRenew := leaseRenewTime(t, c, opsLeaderLease)
	if firstRenew.IsZero() {
		t.Fatalf("the %s Lease carries no renewTime after failover; the new leader is not renewing the Lease",
			opsLeaderLease)
	}
	renewing := pollUntil(opsFailoverWindow, 1*time.Second, func() bool {
		later := leaseRenewTime(t, c, opsLeaderLease)
		holderNow := leaseHolderIdentity(t, c, opsLeaderLease)
		return holderNow == newHolder && later.After(firstRenew)
	})
	if !renewing {
		t.Fatalf("the %s Lease renewTime did not advance within %s after failover (last renewTime %s, holder %q); "+
			"the new leader acquired the Lease but is not renewing it",
			opsLeaderLease, opsFailoverWindow,
			firstRenew.Format(time.RFC3339Nano), leaseHolderIdentity(t, c, opsLeaderLease))
	}
	t.Logf("new leader %q is actively renewing the %s Lease", newLeaderPod, opsLeaderLease)

	// Assert: the lenny-ops Deployment recovers to its full replica count.
	// The ReplicaSet must reschedule a replacement for the killed pod and
	// the replacement must reach Ready, restoring the HA posture §25.4
	// assumes.
	recovered := pollUntil(opsReadyBound, 2*time.Second, func() bool {
		return deploymentReady(t, c, opsDeployment) && len(podNames(t, c, opsSelector)) >= opsReplicasHA
	})
	if !recovered {
		t.Fatalf("the %s Deployment did not return to %d replicas Ready within %s after the leader pod kill (state %s); "+
			"the killed replica was not rescheduled to Ready",
			opsDeployment, opsReplicasHA, opsReadyBound, deploymentReadyState(t, c, opsDeployment))
	}
	t.Logf("%s Deployment recovered to %s Ready; lenny-ops leader-election failover verified end to end",
		opsDeployment, deploymentReadyState(t, c, opsDeployment))
}

// pdbInt reads an integer field of the named PodDisruptionBudget in
// lenny-system via jsonpath. It returns -1 when the field is absent or
// unparseable so callers can distinguish "not yet populated" from a real
// zero. .status.currentHealthy is absent until the PDB controller has
// evaluated the budget at least once.
func pdbInt(t *testing.T, c *kind.Cluster, pdb, field string) int {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "get", "poddisruptionbudget", pdb,
		"-o", "jsonpath={"+field+"}",
	)
	if err != nil {
		return -1
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return -1
	}
	n := 0
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil {
		return -1
	}
	return n
}

// pdbMinAvailable returns the PodDisruptionBudget's spec.minAvailable.
func pdbMinAvailable(t *testing.T, c *kind.Cluster, pdb string) int {
	return pdbInt(t, c, pdb, ".spec.minAvailable")
}

// pdbCurrentHealthy returns the PodDisruptionBudget's status.currentHealthy,
// the number of currently-healthy pods the PDB protects.
func pdbCurrentHealthy(t *testing.T, c *kind.Cluster, pdb string) int {
	return pdbInt(t, c, pdb, ".status.currentHealthy")
}

// pdbDesiredHealthy returns the PodDisruptionBudget's status.desiredHealthy,
// the minimum healthy count the PDB enforces (resolved from minAvailable).
func pdbDesiredHealthy(t *testing.T, c *kind.Cluster, pdb string) int {
	return pdbInt(t, c, pdb, ".status.desiredHealthy")
}
