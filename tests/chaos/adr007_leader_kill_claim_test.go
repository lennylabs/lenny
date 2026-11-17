// SPDX-License-Identifier: MIT

//go:build chaos

// Chaos test for the ADR-007 Phase-1 blocking prerequisite: the
// leader-kill-mid-claim acceptance scenario. §4.6.1 requires a chaos
// test that kills the WarmPoolController leader mid-claim at high
// concurrency (>= 50 concurrent claim goroutines against a pool of 10
// pods) and verifies zero double-claims in the resulting PodClaim
// set. The leader-election failover half lives in leader_election_test.go
// and the concurrent-claim fencing half lives in concurrency_test.go;
// this file combines them into the single scenario the ADR requires,
// asserting that no two PodClaim resources non-terminally bind the
// same AgentPod after a leader kill lands amid the claim race.

package chaos_test

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// leaderKillPoolSize is the pod count of the seeded warm pool. §4.6.1
// fixes the ADR-007 chaos scenario at a pool of 10 pods.
const leaderKillPoolSize = 10

// leaderKillClaimGoroutines is the number of concurrent claim goroutines.
// §4.6.1 requires >= 50; spreading them across the 10-pod pool puts five
// racers on each AgentPod, so every pod is a genuine double-claim
// contention point rather than an uncontended single claim.
const leaderKillClaimGoroutines = 50

// leaderKillTestLabel labels every AgentPod and PodClaim the test
// creates so the whole set can be swept in one cleanup.
const leaderKillTestLabel = "lenny.dev/test=chaos-leaderkill-claim"

// spec: 4.6.1
// diagnosis: §4.6.1 / ADR-007's leader-kill-mid-claim acceptance
// scenario did not hold. ADR-007 is a Phase-1 blocking prerequisite: it
// kills the WarmPoolController leader mid-claim while >= 50 goroutines
// race to claim a 10-pod pool, and requires zero double-claims in the
// resulting PodClaim set — the lenny-podclaim-guard CREATE
// webhook and the API server's resourceVersion / name-uniqueness fencing
// must together admit at most one non-terminal PodClaim per AgentPod
// even across the failover window. The test seeds 10 idle AgentPods,
// fires 50 concurrent claim CREATEs (five per AgentPod), deletes the
// controller leader Pod amid the race, settles through failover, and
// asserts every AgentPod is referenced by at most one non-terminal
// PodClaim. More than one non-terminal claim on any AgentPod is a
// genuine double-claim: the ADR-007 "optimistic locking plus the guard
// webhook is sufficient fencing" hypothesis is violated and Phase 1 must
// not proceed.
func TestLeaderKillMidClaimNoDoubleAssignment(t *testing.T) {
	c := kind.InstallLenny(t)
	sweepClaimFixtureLeftovers(t, c)

	// Precondition: the controller Deployment is fully Ready with the two
	// replicas leader-election failover needs, so the kill under test
	// starts from a known-good HA state (mirrors leader_election_test.go).
	if !deploymentReady(t, c, controllerDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the chaos injection",
			controllerDeployment, deploymentReadyState(t, c, controllerDeployment))
	}
	controllerPods := podNames(t, c, controllerSelector)
	if len(controllerPods) < 2 {
		t.Skipf("precondition not met: leader-kill-mid-claim needs at least 2 controller replicas, found %d (%v)",
			len(controllerPods), controllerPods)
	}

	// The double-claim fencing runs inside the lenny-podclaim-guard
	// CREATE webhook; confirm it is reachable and fail-closed before the
	// race, so a webhook that cannot serve admission does not surface here
	// as a spurious "no double-claim" pass (every CREATE would be rejected
	// and the assertion would be vacuous). requirePodClaimGuardReachable
	// skips with a precise diagnosis when the webhook's API-server egress
	// is blocked.
	requirePodClaimGuardReachable(t, c)

	// Identify the current controller leader from the Lease holderIdentity
	// before the race, so the kill targets the real leader pod (mirrors
	// leader_election_test.go).
	originalHolder := leaseHolderIdentity(t, c, warmPoolControllerLease)
	if originalHolder == "" {
		t.Fatalf("the %s Lease has no holderIdentity; leader election has not converged on a leader",
			warmPoolControllerLease)
	}
	leaderPod := leaseHolderPod(originalHolder)
	if !contains(controllerPods, leaderPod) {
		t.Fatalf("leader pod %q (from Lease holderIdentity %q) is not among the controller Deployment pods %v",
			leaderPod, originalHolder, controllerPods)
	}
	t.Logf("current %s leader: pod %q; seeding a %d-pod pool and racing %d claim goroutines",
		warmPoolControllerLease, leaderPod, leaderKillPoolSize, leaderKillClaimGoroutines)

	// Sweep every AgentPod and PodClaim this test creates on the way
	// out, whether or not the race completed cleanly.
	t.Cleanup(func() {
		_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "podclaims",
			"-l", leaderKillTestLabel, "--ignore-not-found", "--wait=false")
		_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "agentpods",
			"-l", leaderKillTestLabel, "--ignore-not-found", "--wait=false")
	})

	// Seed the 10-pod pool: ten idle AgentPod resources the claim
	// goroutines contend for.
	agentPodRefs := make([]string, leaderKillPoolSize)
	for i := 0; i < leaderKillPoolSize; i++ {
		ref := leaderKillAgentPodName(i)
		agentPodRefs[i] = ref
		manifest := leaderKillLabeledAgentPod(ref)
		if out, err := c.ApplyStdin(t, manifest); err != nil {
			t.Fatalf("failed to seed pool AgentPod %s: %v\n%s", ref, err, out)
		}
	}
	t.Logf("seeded %d idle AgentPod resources", len(agentPodRefs))

	// Fire the race: 50 claim goroutines, each a CREATE of a PodClaim
	// for one of the ten pool AgentPods (five goroutines share each
	// AgentPod). Every claim for a given AgentPod carries the deterministic
	// claim-<podName> name the gateway claim path always uses (§4.6.1), so
	// the five racers on a AgentPod collide on one resource name: the API
	// server's name-uniqueness check plus the lenny-podclaim-guard
	// CREATE webhook together admit exactly one and reject the rest. A
	// plain CREATE (not apply) is used so a loser is rejected outright
	// rather than silently converted into a PATCH of the winner's object.
	// A barrier release maximizes overlap; a separate killer goroutine
	// deletes the leader pod on the same release so the kill lands amid
	// the in-flight CREATEs.
	type result struct {
		agentPodRef string
		admitted    bool
		output      string
	}
	results := make([]result, leaderKillClaimGoroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < leaderKillClaimGoroutines; i++ {
		ref := agentPodRefs[i%leaderKillPoolSize]
		claimName := leaderKillClaimName(ref)
		manifest := leaderKillLabeledClaim(claimName, ref, leaderKillSessionID(i))
		wg.Add(1)
		go func(idx int, agentPodRef, doc string) {
			defer wg.Done()
			cmd := c.Kubectl("-n", agentNamespace, "create", "-f", "-")
			cmd.Stdin = strings.NewReader(doc)
			<-start
			out, err := cmd.CombinedOutput()
			results[idx] = result{agentPodRef: agentPodRef, admitted: err == nil, output: string(out)}
		}(i, ref, manifest)
	}

	// Killer goroutine: on the same barrier release, delete the leader
	// pod so the failover overlaps the claim race. --wait=false returns
	// once the delete is accepted rather than blocking on termination.
	var killErr string
	var killWG sync.WaitGroup
	killWG.Add(1)
	go func() {
		defer killWG.Done()
		<-start
		out, err := c.KubectlOut(t, "-n", lennySystemNamespace, "delete", "pod", leaderPod, "--wait=false")
		if err != nil {
			killErr = out
		}
	}()

	close(start)
	killWG.Wait()
	wg.Wait()

	if killErr != "" {
		t.Fatalf("failed to delete the controller leader pod %q mid-claim: %s", leaderPod, killErr)
	}
	t.Logf("deleted controller leader pod %q amid the claim race", leaderPod)

	// Settle: wait for a surviving replica to acquire the Lease, so the
	// final claim-set read happens after the failover the scenario injects
	// (mirrors leader_election_test.go's failover assertion).
	acquired := pollUntil(failoverAssertBound, 1*time.Second, func() bool {
		h := leaseHolderIdentity(t, c, warmPoolControllerLease)
		return h != "" && h != originalHolder && leaseHolderPod(h) != leaderPod
	})
	if !acquired {
		t.Fatalf("no surviving controller replica acquired the %s Lease within %s after the leader kill; "+
			"leader-election failover stalled and the claim set cannot be assessed post-failover",
			warmPoolControllerLease, failoverAssertBound)
	}
	t.Logf("failover complete: a surviving replica holds the %s Lease", warmPoolControllerLease)

	// Count admissions for liveness and diagnostics.
	admitted := 0
	for _, r := range results {
		if r.admitted {
			admitted++
		}
	}
	t.Logf("claim race outcome: %d of %d CREATEs admitted across %d AgentPods",
		admitted, leaderKillClaimGoroutines, leaderKillPoolSize)

	// Liveness: the fencing must not deadlock the whole claim path. With
	// the guard reachable, exactly one of the five racers on each AgentPod
	// must win its deterministic-name CREATE, so the admitted total must
	// be non-zero (and, with ten contended AgentPods, at most ten). A
	// zero-admission run means every CREATE was rejected — a fencing that
	// admits nothing is as broken as one that admits duplicates, and would
	// also make the double-claim assertion below vacuous.
	if admitted == 0 {
		t.Fatalf("§4.6.1 / ADR-007 violation: every one of %d concurrent claim CREATEs was rejected; "+
			"the fencing must admit exactly one claim per AgentPod, not zero", leaderKillClaimGoroutines)
	}
	if admitted > leaderKillPoolSize {
		t.Errorf("§4.6.1 / ADR-007 fencing weakness: %d CREATEs were admitted across %d AgentPods; "+
			"the deterministic-name plus guard fencing must admit at most one claim per AgentPod",
			admitted, leaderKillPoolSize)
	}

	// The ADR-007 assertion: after the race and failover, every AgentPod is
	// referenced by at most one non-terminal PodClaim. Read the live
	// claim set from the API server (authoritative over the per-goroutine
	// kubectl exit codes) and bucket non-terminal claims by agentPodRef.
	nonTerminal := leaderKillNonTerminalClaimsByAgentPod(t, c)
	doubles := 0
	for _, ref := range agentPodRefs {
		claims := nonTerminal[ref]
		if len(claims) > 1 {
			doubles++
			t.Errorf("§4.6.1 / ADR-007 double-claim: AgentPod %s is referenced by %d non-terminal PodClaims %v; "+
				"the guard webhook plus optimistic-locking fencing must admit at most one even across the leader-kill "+
				"failover window", ref, len(claims), claims)
		}
	}
	if doubles == 0 {
		t.Logf("verified: no AgentPod carries more than one non-terminal PodClaim after the leader-kill race; " +
			"ADR-007 fencing held across failover")
	}
}

// leaderKillNonTerminalClaimsByAgentPod lists every test-labeled
// PodClaim in the agent namespace and returns, per agentPodRef, the
// names of the claims whose binding state is non-terminal. §4.6.1 marks
// only released and failed as terminal PodClaim.status.phase values;
// an empty phase (a freshly created claim with no gateway-written status)
// is non-terminal and counts toward the per-pod uniqueness the guard
// enforces.
func leaderKillNonTerminalClaimsByAgentPod(t *testing.T, c *kind.Cluster) map[string][]string {
	t.Helper()
	// One line per claim: "<name> <agentPodRef> <phase>"; phase is empty
	// when the claim has no status.phase yet.
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "podclaims", "-l", leaderKillTestLabel,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\" \"}{.spec.agentPodRef}{\" \"}{.status.phase}{\"\\n\"}{end}",
	)
	if err != nil {
		t.Fatalf("failed to list the test PodClaims for the double-claim assertion: %v\n%s", err, out)
	}
	byAgentPod := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue // a claim with no agentPodRef cannot double-claim a pod
		}
		name, agentPodRef := fields[0], fields[1]
		phase := ""
		if len(fields) >= 3 {
			phase = fields[2]
		}
		if phase == "released" || phase == "failed" {
			continue // terminal claims free the pod and do not double-claim
		}
		byAgentPod[agentPodRef] = append(byAgentPod[agentPodRef], name)
	}
	return byAgentPod
}

// leaderKillAgentPodName is the deterministic name of the i-th pool
// AgentPod.
func leaderKillAgentPodName(i int) string {
	return "chaos-lk-sandbox-" + strconv.Itoa(i)
}

// leaderKillClaimName is the deterministic per-pod claim name for the
// given AgentPod, modeling the gateway claim path's claim-<podName>
// convention (§4.6.1). Every goroutine racing to claim the same AgentPod
// produces this same name, so their CREATEs collide on one resource name
// and the API server's name-uniqueness check plus the guard webhook admit
// exactly one.
func leaderKillClaimName(agentPodRef string) string {
	return "claim-" + agentPodRef
}

// leaderKillSessionID is the session id carried by the i-th racing claim.
func leaderKillSessionID(i int) string {
	return "sess-lk-" + strconv.Itoa(i)
}

// leaderKillLabeledAgentPod renders a pool AgentPod carrying the sweep
// label so the whole seeded pool is deletable in one cleanup. poolRef and
// runtimeRef are the CRD-required fields; their referents need not exist
// for the claim-fencing scenario, which keys on the AgentPod name via
// .spec.agentPodRef.
func leaderKillLabeledAgentPod(name string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: AgentPod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-leaderkill-claim
spec:
  poolRef: chaos-pool
  runtimeRef: chaos-runtime
`, name, agentNamespace)
}

// leaderKillLabeledClaim renders a racing PodClaim carrying the sweep
// label, binding sessionID to the AgentPod named by agentPodRef.
func leaderKillLabeledClaim(name, agentPodRef, sessionID string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: PodClaim
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-leaderkill-claim
spec:
  agentPodRef: %s
  sessionId: %s
`, name, agentNamespace, agentPodRef, sessionID)
}
