// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for controller leader-election failover. This file
// exercises the ADR-0007 / §4.6.1 leader-election contract against the
// live Lenny control plane on the Kind cluster: the WarmPoolController
// runs as a 2-replica Deployment with Kubernetes Lease-based leader
// election, and killing the current leader Pod must hand the Lease to a
// surviving replica within the documented crash-case failover window.

package tier8_chaos_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// warmPoolControllerLease is the coordination.k8s.io/v1 Lease the
// WarmPoolController uses for leader election. §4.6.1 fixes the lease
// name as lenny-warm-pool-controller.
const warmPoolControllerLease = "lenny-warm-pool-controller"

// controllerDeployment is the Deployment that runs the controller
// replicas; the leader-election Lease holder is always one of its pods.
const controllerDeployment = "lenny-controller"

// controllerSelector matches the WarmPoolController Deployment's pods.
// The chart labels controller pods with lenny.dev/component=controller.
const controllerSelector = "lenny.dev/component=controller"

// §4.6.1 leader-election lease parameters. leaseDuration is 15s and
// renewDeadline is 10s, so the crash-case worst-case failover window is
// leaseDuration + renewDeadline = 25s — a surviving replica cannot
// acquire the Lease until the dead leader's lease fully expires.
const (
	leaderFailoverWindow = 25 * time.Second

	// failoverAssertBound is the time the test allows for the new
	// leader to appear. It is the 25s §4.6.1 worst case plus a margin
	// for API-server latency and the kubelet observing the Pod delete;
	// §4.6.1 itself notes elevated API latency can extend effective
	// failover by 5-15s, and podClaimQueueTimeout (60s) is sized on the
	// same reasoning. 75s keeps the test inside that documented band.
	failoverAssertBound = 75 * time.Second
)

// spec: 4.6.1
// diagnosis: §4.6.1 / ADR-0007 controller leader-election failover did
// not hold. The WarmPoolController runs 2 replicas with a Kubernetes
// Lease (lenny-warm-pool-controller; 15s duration, 25s crash-case
// failover). The test reads the Lease holderIdentity, deletes that
// leader Pod, then asserts a surviving replica acquires the Lease
// within the failover bound and the controller Deployment returns to
// Ready. A failure means failover stalled or no replica took over; the
// ADR-0007 optimistic-locking fencing depends on bounded failover.
func TestControllerLeaderElectionDisruption(t *testing.T) {
	c := kind.InstallLenny(t)

	// Precondition: the controller Deployment is fully Ready, so the
	// failover under test starts from a known-good 2-replica state.
	if !deploymentReady(t, c, controllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			controllerDeployment, deploymentReadyState(t, c, controllerDeployment))
	}

	// Identify the current leader from the Lease holderIdentity. The
	// holder is formatted "<pod-name>_<uuid>" by client-go; the pod
	// name is what the test deletes.
	originalHolder := leaseHolderIdentity(t, c, warmPoolControllerLease)
	if originalHolder == "" {
		t.Fatalf("the %s Lease has no holderIdentity; leader election has not converged on a leader",
			warmPoolControllerLease)
	}
	leaderPod := leaseHolderPod(originalHolder)
	if leaderPod == "" {
		t.Fatalf("could not extract a pod name from holderIdentity %q", originalHolder)
	}

	// The leader pod must be a live pod of the controller Deployment.
	controllerPods := podNames(t, c, controllerSelector)
	if !contains(controllerPods, leaderPod) {
		t.Fatalf("leader pod %q (from Lease holderIdentity %q) is not among the controller Deployment pods %v",
			leaderPod, originalHolder, controllerPods)
	}
	if len(controllerPods) < 2 {
		t.Skipf("precondition not met: leader-election failover needs at least 2 controller replicas, found %d (%v)",
			len(controllerPods), controllerPods)
	}
	t.Logf("current %s leader: pod %q (holderIdentity %q), %d controller replicas",
		warmPoolControllerLease, leaderPod, originalHolder, len(controllerPods))

	// Inject the failure: delete the leader pod. --wait=false returns
	// once the delete is accepted so the test starts the failover clock
	// immediately rather than blocking on graceful termination. The
	// Deployment's ReplicaSet reschedules a replacement pod.
	if out, err := c.KubectlOut(
		t,
		"-n", lennySystemNamespace, "delete", "pod", leaderPod, "--wait=false",
	); err != nil {
		t.Fatalf("failed to delete the leader pod %q: %v\n%s", leaderPod, err, out)
	}
	injectedAt := time.Now()
	t.Logf("deleted leader pod %q at %s; waiting for failover within %s",
		leaderPod, injectedAt.Format(time.RFC3339), failoverAssertBound)

	// Assert: a surviving replica acquires the Lease. The holderIdentity
	// must change to a *different* identity than the dead leader's.
	var newHolder string
	acquired := pollUntil(failoverAssertBound, 1*time.Second, func() bool {
		h := leaseHolderIdentity(t, c, warmPoolControllerLease)
		if h != "" && h != originalHolder {
			newHolder = h
			return true
		}
		return false
	})
	failoverElapsed := time.Since(injectedAt)
	if !acquired {
		t.Fatalf("no surviving controller replica acquired the %s Lease within %s after the leader pod was killed; "+
			"holderIdentity is still %q — leader-election failover stalled",
			warmPoolControllerLease, failoverAssertBound, leaseHolderIdentity(t, c, warmPoolControllerLease))
	}
	newLeaderPod := leaseHolderPod(newHolder)
	if newLeaderPod == leaderPod {
		t.Fatalf("the %s Lease holderIdentity changed to %q but still names the killed pod %q; "+
			"failover did not move the leadership to a surviving replica",
			warmPoolControllerLease, newHolder, leaderPod)
	}
	t.Logf("failover complete after %s: new leader pod %q (holderIdentity %q)",
		failoverElapsed.Round(time.Second), newLeaderPod, newHolder)

	// The §4.6.1 worst case is 25s; the failover should land within
	// that bound on a healthy cluster. Exceeding it (but still under
	// failoverAssertBound) is flagged as a soft signal rather than a
	// hard failure, because API-server latency legitimately stretches
	// the window and the spec sizes podClaimQueueTimeout for exactly
	// that. A genuine stall is already a Fatalf above.
	if failoverElapsed > leaderFailoverWindow {
		t.Logf("note: failover took %s, above the §4.6.1 25s crash-case worst case; "+
			"acceptable under API-server latency but worth watching",
			failoverElapsed.Round(time.Second))
	}

	// Assert: the new leader keeps the Lease renewed. A replica that
	// acquires the Lease but does not renew it is not actively leading;
	// §4.6.1 requires the holder to renew within the 10s renew
	// deadline. Sample renewTime twice across a renew interval and
	// confirm it advanced while the holder stayed the same.
	firstRenew := leaseRenewTime(t, c, warmPoolControllerLease)
	if firstRenew.IsZero() {
		t.Fatalf("the %s Lease carries no renewTime after failover; the new leader is not renewing the Lease",
			warmPoolControllerLease)
	}
	renewing := pollUntil(leaderFailoverWindow, 1*time.Second, func() bool {
		later := leaseRenewTime(t, c, warmPoolControllerLease)
		holderNow := leaseHolderIdentity(t, c, warmPoolControllerLease)
		return holderNow == newHolder && later.After(firstRenew)
	})
	if !renewing {
		t.Fatalf("the %s Lease renewTime did not advance within %s after failover (last renewTime %s, holder %q); "+
			"the new leader acquired the Lease but is not renewing it",
			warmPoolControllerLease, leaderFailoverWindow,
			firstRenew.Format(time.RFC3339Nano), leaseHolderIdentity(t, c, warmPoolControllerLease))
	}
	t.Logf("new leader %q is actively renewing the %s Lease", newLeaderPod, warmPoolControllerLease)

	// Assert: the controller Deployment recovers to fully Ready. The
	// ReplicaSet must reschedule a replacement for the killed pod and
	// the replacement must reach Ready, restoring the 2-replica HA
	// posture §4.6.1 assumes.
	recovered := pollUntil(2*time.Minute, 2*time.Second, func() bool {
		return deploymentReady(t, c, controllerDeployment)
	})
	if !recovered {
		t.Fatalf("the %s Deployment did not return to fully Ready within 2m after the leader pod kill (state %s); "+
			"the killed replica was not rescheduled to Ready",
			controllerDeployment, deploymentReadyState(t, c, controllerDeployment))
	}
	t.Logf("%s Deployment recovered to fully Ready (%s); leader-election failover verified end to end",
		controllerDeployment, deploymentReadyState(t, c, controllerDeployment))
}

// contains reports whether values contains want.
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
