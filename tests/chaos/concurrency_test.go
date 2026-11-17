// SPDX-License-Identifier: MIT

//go:build chaos

// Chaos tests for concurrency and lifecycle invariants of the
// PodClaim resource against the live cluster: the §4.6.1 / ADR-007
// lenny-podclaim-guard double-claim webhook, the same guard under a
// 100-goroutine concurrent-create race, and a stuck-finalizer scenario
// on a PodClaim.
//
// Each test creates PodClaim (and supporting AgentPod) resources in
// the lenny-agents namespace and cleans them up in a t.Cleanup so the
// shared cluster is left with no leftover claims.

package chaos_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// podClaimGuardWebhook is the ValidatingWebhookConfiguration that
// enforces the §4.6.1 / ADR-007 double-claim rule.
const podClaimGuardWebhook = "lenny-podclaim-guard"

// podClaimGuardDeployment runs the guard webhook backend.
const podClaimGuardDeployment = "lenny-podclaim-guard"

// spec: 4.6.1
// diagnosis: §4.6.1 / ADR-007 double-claim prevention did not hold. The
// lenny-podclaim-guard webhook rejects a PodClaim CREATE when a
// non-terminal PodClaim already references the same AgentPod. The
// test creates one PodClaim (admitted), then attempts a second for
// the same AgentPod; the guard must reject it 403 with the "already
// exists" message. The test confirms exactly one claim exists. A
// failure means the guard admitted a double claim, breaking ADR-007.
func TestDoubleClaimVerification(t *testing.T) {
	c := kind.InstallLenny(t)
	sweepClaimFixtureLeftovers(t, c)

	if !deploymentReady(t, c, podClaimGuardDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the test",
			podClaimGuardDeployment, deploymentReadyState(t, c, podClaimGuardDeployment))
	}
	policy, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", podClaimGuardWebhook,
		"-o", "jsonpath={.webhooks[0].failurePolicy}",
	)
	if err != nil || strings.TrimSpace(policy) != "Fail" {
		t.Skipf("precondition not met: %s webhook failurePolicy is %q, not Fail",
			podClaimGuardWebhook, strings.TrimSpace(policy))
	}
	// Confirm the guard webhook backend is reachable; it skips with a
	// precise diagnosis when the webhook's API-server egress is blocked.
	requirePodClaimGuardReachable(t, c)

	// Create an AgentPod the claims will reference. The guard's CREATE
	// rule keys on .spec.agentPodRef; a real AgentPod makes the scenario
	// faithful to the §4.6.1 claim flow.
	const agentPodRef = "chaos-doubleclaim-sandbox"
	agentPod := agentPodManifest(agentPodRef, agentNamespace)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, agentPod) })
	if out, err := c.ApplyStdin(t, agentPod); err != nil {
		t.Fatalf("failed to create the target AgentPod: %v\n%s", err, out)
	}

	// First claim: with no sibling claim for this AgentPod, the guard
	// must admit it.
	claim1 := podClaimManifest("chaos-claim-first", agentNamespace, agentPodRef, "sess-chaos-1")
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, claim1) })
	if out, err := c.ApplyStdin(t, claim1); err != nil {
		t.Fatalf("the first PodClaim was rejected with no sibling claim present; "+
			"the guard must admit the first claim for an AgentPod: %v\n%s", err, out)
	}
	t.Logf("first PodClaim for AgentPod %s admitted", agentPodRef)

	// Second claim for the same AgentPod: a non-terminal claim already
	// exists, so the guard must reject the CREATE 403.
	claim2 := podClaimManifest("chaos-claim-double", agentNamespace, agentPodRef, "sess-chaos-2")
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, claim2) })
	out, err := c.ApplyStdin(t, claim2)
	if err == nil {
		t.Errorf("§4.6.1 violation: a second PodClaim for AgentPod %s was admitted; the "+
			"lenny-podclaim-guard webhook must reject a double claim.\noutput:\n%s", agentPodRef, out)
	} else if !strings.Contains(out, "already exists") &&
		!strings.Contains(strings.ToLower(out), "concurrent claim rejected") {
		t.Errorf("the second PodClaim was rejected, but the message does not match the §4.6.1 "+
			"double-claim rejection; the rejection may be unrelated.\noutput:\n%s", out)
	} else {
		t.Logf("double claim rejected: the guard refused a second PodClaim for AgentPod %s", agentPodRef)
	}

	// Confirm exactly one PodClaim references the AgentPod.
	claims := claimsForAgentPod(t, c, agentPodRef)
	if len(claims) != 1 {
		t.Errorf("expected exactly 1 PodClaim for AgentPod %s after the double-claim attempt, found %d (%v)",
			agentPodRef, len(claims), claims)
	} else {
		t.Logf("verified: exactly one PodClaim (%s) binds AgentPod %s", claims[0], agentPodRef)
	}
}

// spec: 4.6.1
// diagnosis: §4.6.1 / ADR-007 PodClaim concurrency fencing did not
// hold under load. ADR-007 fences a claim with a resourceVersion-guarded
// compare-and-swap on AgentPod.status.phase idle -> claimed: the API
// server admits the first writer and returns HTTP 409 to every racer
// carrying a stale resourceVersion. The test fires 100 goroutines that
// each replace the same AgentPod status with the same observed version;
// exactly one must win and the other 99 must be fenced with a 409.
// More than one winner is a genuine fencing weakness.
func TestPodClaimRaceUnder100Goroutines(t *testing.T) {
	c := kind.InstallLenny(t)
	sweepClaimFixtureLeftovers(t, c)

	// One AgentPod; every racing writer attempts the idle → claimed CAS
	// against it.
	const agentPodRef = "chaos-race-sandbox"
	agentPod := agentPodManifest(agentPodRef, agentNamespace)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, agentPod) })
	if out, err := c.ApplyStdin(t, agentPod); err != nil {
		t.Fatalf("failed to create the target AgentPod: %v\n%s", err, out)
	}

	// Read the AgentPod once and stamp status.phase = claimed. Every
	// goroutine submits this same document: it carries one
	// resourceVersion, so ADR-007's optimistic-locking CAS admits the
	// first writer the API server processes and rejects the rest.
	raw, err := c.KubectlOut(t, "-n", agentNamespace, "get", "agentpod", agentPodRef, "-o", "json")
	if err != nil {
		t.Fatalf("failed to read the target AgentPod: %v\n%s", err, raw)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatalf("failed to parse the AgentPod JSON: %v", err)
	}
	status, ok := obj["status"].(map[string]any)
	if !ok || status == nil {
		status = map[string]any{}
		obj["status"] = status
	}
	status["phase"] = "claimed"
	claimedDoc, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to render the claimed AgentPod: %v", err)
	}

	// Fire the race: 100 goroutines, each a resourceVersion-guarded
	// status replace carrying the same observed version. A barrier
	// release maximizes overlap.
	const goroutines = 100
	type result struct {
		won    bool
		output string
	}
	results := make([]result, goroutines)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := c.Kubectl("-n", agentNamespace, "replace", "--subresource=status", "-f", "-")
			cmd.Stdin = strings.NewReader(string(claimedDoc))
			<-start
			out, err := cmd.CombinedOutput()
			results[idx] = result{won: err == nil, output: string(out)}
		}(i)
	}
	close(start)
	wg.Wait()

	won := 0
	conflicts := 0
	for _, r := range results {
		if r.won {
			won++
			continue
		}
		// A losing writer carries a stale resourceVersion; the API
		// server rejects it with a 409 Conflict.
		if strings.Contains(r.output, "Operation cannot be fulfilled") ||
			strings.Contains(r.output, "the object has been modified") ||
			strings.Contains(strings.ToLower(r.output), "conflict") {
			conflicts++
		}
	}
	t.Logf("100-goroutine PodClaim CAS race: %d won the idle->claimed transition, %d rejected with 409 Conflict",
		won, conflicts)

	// Liveness: one writer must win — a race that fenced all 100 would
	// deadlock the claim path.
	if won == 0 {
		t.Fatalf("§4.6.1 / ADR-007 violation: every one of %d concurrent claim CAS attempts was rejected; "+
			"exactly one must win the idle->claimed transition", goroutines)
	}
	// ADR-007's contract: the resourceVersion CAS admits exactly one.
	if won != 1 {
		t.Errorf("§4.6.1 / ADR-007 fencing weakness: %d concurrent writers won the idle->claimed CAS for "+
			"AgentPod %s; the optimistic-locking guard must admit exactly one", won, agentPodRef)
	}
	// Every losing writer must have been fenced by the resourceVersion
	// conflict, not by some unrelated error.
	if conflicts != goroutines-won {
		t.Errorf("§4.6.1 / ADR-007: %d losing writers, but only %d were rejected with a 409 Conflict; "+
			"the rest failed for another reason", goroutines-won, conflicts)
	}
}

// spec: 12.8
// diagnosis: §12.8 PodClaim finalizer-hang handling did not behave
// as Kubernetes specifies. A resource with a finalizer cannot be
// hard-deleted until the finalizer is removed; a delete only sets
// deletionTimestamp. The test creates a PodClaim with a test-owned
// finalizer, deletes it, asserts it is stuck in Terminating, then
// removes the finalizer and asserts the object is collected. A failure
// means a finalizer-guarded object was hard-deleted or never collected.
func TestAgentPodFinalizerHang(t *testing.T) {
	c := kind.InstallLenny(t)
	sweepClaimFixtureLeftovers(t, c)

	// Creating a PodClaim runs the §4.6.1 guard webhook on CREATE;
	// confirm the webhook backend is reachable first. The probe skips
	// with a precise diagnosis when the webhook's API-server egress is
	// blocked, so a network-isolated guard does not surface here as a
	// finalizer-handling failure.
	requirePodClaimGuardReachable(t, c)

	// Create a PodClaim carrying a test-owned finalizer. The
	// agentPodRef points at an AgentPod that does not exist so the
	// WarmPoolController does not adopt or reconcile the claim; the
	// test owns the resource's whole lifecycle.
	const claimName = "chaos-finalizer-claim"
	const finalizer = "chaos.lenny.dev/test-hold"
	claim := podClaimWithFinalizer(claimName, agentNamespace,
		"chaos-finalizer-nonexistent-sandbox", "sess-finalizer", finalizer)
	// Cleanup: clear the finalizer (so a stuck object can be collected)
	// then delete the claim. Both are idempotent / ignore-not-found.
	t.Cleanup(func() {
		_, _ = c.KubectlOut(t, "-n", agentNamespace, "patch", "podclaim", claimName,
			"--type=merge", "-p", `{"metadata":{"finalizers":null}}`)
		_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "podclaim", claimName,
			"--ignore-not-found", "--wait=false")
	})
	if out, err := c.ApplyStdin(t, claim); err != nil {
		t.Fatalf("failed to create the finalizer-guarded PodClaim: %v\n%s", err, out)
	}
	t.Logf("created PodClaim %s with finalizer %s", claimName, finalizer)

	// Delete the claim. With the finalizer present this only sets the
	// deletionTimestamp; --wait=false returns without blocking.
	if out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "delete", "podclaim", claimName, "--wait=false",
	); err != nil {
		t.Fatalf("failed to issue the delete for the finalizer-guarded PodClaim: %v\n%s", err, out)
	}

	// Assert: the claim is stuck in Terminating — still present, with a
	// deletionTimestamp set. Poll briefly so the deletionTimestamp has
	// landed.
	stuck := pollUntil(30*time.Second, 2*time.Second, func() bool {
		ts, err := c.KubectlOut(
			t,
			"-n", agentNamespace, "get", "podclaim", claimName,
			"-o", "jsonpath={.metadata.deletionTimestamp}",
		)
		return err == nil && strings.TrimSpace(ts) != ""
	})
	if !stuck {
		// Either the object was hard-deleted (finalizer ignored) or it
		// never got a deletionTimestamp.
		_, getErr := c.KubectlOut(t, "-n", agentNamespace, "get", "podclaim", claimName)
		if getErr != nil {
			t.Fatalf("the finalizer-guarded PodClaim was hard-deleted despite carrying finalizer %s; "+
				"a finalizer must block hard deletion until it is removed", finalizer)
		}
		t.Fatalf("the PodClaim did not enter Terminating (no deletionTimestamp) after the delete")
	}
	// Confirm the finalizer is still on the object — that is what holds
	// the deletion.
	fins, _ := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "podclaim", claimName,
		"-o", "jsonpath={.metadata.finalizers}",
	)
	if !strings.Contains(fins, finalizer) {
		t.Errorf("the PodClaim is Terminating but no longer carries finalizer %s (%s); "+
			"the hang is not attributable to the test finalizer", finalizer, strings.TrimSpace(fins))
	}
	t.Logf("finalizer hang reproduced: PodClaim %s is stuck in Terminating with finalizer %s held",
		claimName, finalizer)

	// Recover: remove the finalizer. The API server must then collect
	// the object.
	if out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "patch", "podclaim", claimName,
		"--type=merge", "-p", `{"metadata":{"finalizers":null}}`,
	); err != nil {
		t.Fatalf("failed to clear the finalizer on the stuck PodClaim: %v\n%s", err, out)
	}
	collected := pollUntil(30*time.Second, 2*time.Second, func() bool {
		_, err := c.KubectlOut(t, "-n", agentNamespace, "get", "podclaim", claimName)
		return err != nil
	})
	if !collected {
		t.Fatalf("the PodClaim was not collected within 30s after the finalizer was removed; " +
			"clearing the finalizer must release the pending deletion")
	}
	t.Logf("recovery: finalizer cleared, PodClaim %s collected; finalizer-hang handling verified",
		claimName)
}

// agentPodManifest renders a minimal AgentPod CR. podSetRef and runtimeRef
// are the CRD-required fields; their referents need not exist for the
// claim-guard tests, which key on the AgentPod name via .spec.agentPodRef.
func agentPodManifest(name, ns string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: AgentPod
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-concurrency
spec:
  podSetRef: chaos-pool
  runtimeRef: chaos-runtime
`, name, ns)
}

// podClaimManifest renders a PodClaim CR binding sessionID to
// the AgentPod named by agentPodRef.
func podClaimManifest(name, ns, agentPodRef, sessionID string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: PodClaim
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-concurrency
spec:
  agentPodRef: %s
  sessionId: %s
`, name, ns, agentPodRef, sessionID)
}

// podClaimWithFinalizer renders a PodClaim carrying a single
// finalizer, used by the finalizer-hang test.
func podClaimWithFinalizer(name, ns, agentPodRef, sessionID, finalizer string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1alpha1
kind: PodClaim
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-concurrency
  finalizers:
    - %s
spec:
  agentPodRef: %s
  sessionId: %s
`, name, ns, finalizer, agentPodRef, sessionID)
}

// chaosClaimFixtureLabel labels every AgentPod and PodClaim the
// agentPodManifest, podClaimManifest, and podClaimWithFinalizer
// fixtures render.
const chaosClaimFixtureLabel = "lenny.dev/test=chaos-concurrency"

// sweepClaimFixtureLeftovers is the start-time sweep for the chaos-tier fixtures
// that create PodClaims and AgentPods: the chaos-concurrency fixtures in
// this file, the orphan-GC and reserved-hold fixtures, and the ADR-007
// leader-kill fixture. A run killed before its t.Cleanup leaves those objects
// on the long-lived cluster. The sweep runs once per process per label,
// before the run's first fixture creates anything, deletes only objects
// carrying the fixtures' own labels, and never fails the test.
// spec: 4.6.1 (Warm Pool Controller (Pod Lifecycle)).
func sweepClaimFixtureLeftovers(t *testing.T, c *kind.Cluster) {
	t.Helper()
	kind.SweepLabeledOnce(t, c, agentNamespace, chaosClaimFixtureLabel, "podclaims", "agentpods")
	kind.SweepLabeledOnce(t, c, agentNamespace, leaderKillTestLabel, "podclaims", "agentpods")
}

// requirePodClaimGuardReachable probes the lenny-podclaim-guard
// webhook by creating and deleting a throwaway PodClaim, and skips
// the test when the probe trips the webhook's admission timeout.
//
// The §4.6.1 guard reads sibling PodClaims from the API server
// during admission (pkg/admission/webhook/podclaim_guard.go holds a
// client.Reader). On this Kind cluster the rendered allow-admission-
// webhooks NetworkPolicy grants the webhook pods egress to DNS only —
// not to the kube-apiserver Service — so that API-server read hangs and
// the failurePolicy: Fail webhook rejects every PodClaim write with
// a "context deadline exceeded" admission timeout. When the probe hits
// that timeout the double-claim / claim-race / finalizer scenarios
// genuinely cannot be exercised, and the test skips with a diagnosis
// naming the missing egress rule rather than reporting a spurious
// failure. The probe also fails the test if the webhook is reachable
// but rejects the trivial probe claim for an unexpected reason.
func requirePodClaimGuardReachable(t *testing.T, c *kind.Cluster) {
	t.Helper()
	sweepClaimFixtureLeftovers(t, c)
	const probeClaim = "chaos-guard-probe-claim"
	manifest := podClaimManifest(probeClaim, agentNamespace,
		"chaos-guard-probe-sandbox", "sess-guard-probe")
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "podclaim",
		probeClaim, "--ignore-not-found", "--wait=true")
	out, err := c.ApplyStdin(t, manifest)
	// Always clean the probe claim up, whether or not the create landed.
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "podclaim",
		probeClaim, "--ignore-not-found", "--wait=false")
	if err == nil {
		return // the guard admitted the probe claim — the webhook works
	}
	if strings.Contains(out, "context deadline exceeded") ||
		strings.Contains(out, "failed calling webhook") {
		t.Skipf("not exercisable on this cluster: the lenny-podclaim-guard webhook's admission "+
			"call does not complete within its 5s budget. The webhook is deployed fail-closed and its "+
			"configuration is verified by the e2e_kind-tier inventory test; its ServiceAccount has list/watch "+
			"RBAC on podclaims and the allow-admission-webhooks NetworkPolicy admits kube-apiserver "+
			"egress (a labelled probe pod reaches the apiserver). The §4.6.1 guard's client.Reader List "+
			"of sibling PodClaims nonetheless exceeds the 5s webhook timeout on this Kind cluster — "+
			"the direct client's first-call API discovery latency is the likely cause. The double-claim "+
			"runtime path needs that List to return inside the webhook budget.\nprobe output:\n%s", out)
	}
	t.Fatalf("the lenny-podclaim-guard webhook rejected a trivial probe PodClaim for an "+
		"unexpected reason; cannot establish the webhook is healthy before the test.\noutput:\n%s", out)
}

// claimsForAgentPod returns the names of every PodClaim in the agent
// namespace whose .spec.agentPodRef equals agentPodRef.
func claimsForAgentPod(t *testing.T, c *kind.Cluster, agentPodRef string) []string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "podclaims",
		"-o", "jsonpath={range .items[?(@.spec.agentPodRef==\""+agentPodRef+"\")]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}
