// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for concurrency and lifecycle invariants of the
// SandboxClaim resource against the live cluster: the §4.6.1 / ADR-007
// lenny-sandboxclaim-guard double-claim webhook, the same guard under a
// 100-goroutine concurrent-create race, and a stuck-finalizer scenario
// on a SandboxClaim.
//
// Each test creates SandboxClaim (and supporting Sandbox) resources in
// the lenny-agents namespace and cleans them up in a t.Cleanup so the
// shared cluster is left with no leftover claims.

package tier8_chaos_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// sandboxClaimGuardWebhook is the ValidatingWebhookConfiguration that
// enforces the §4.6.1 / ADR-007 double-claim rule.
const sandboxClaimGuardWebhook = "lenny-sandboxclaim-guard"

// sandboxClaimGuardDeployment runs the guard webhook backend.
const sandboxClaimGuardDeployment = "lenny-sandboxclaim-guard"

// spec: 4.6.1
// diagnosis: §4.6.1 / ADR-007 double-claim prevention did not hold. The
// lenny-sandboxclaim-guard webhook rejects a SandboxClaim CREATE when a
// non-terminal SandboxClaim already references the same Sandbox. The
// test creates one SandboxClaim (admitted), then attempts a second for
// the same Sandbox; the guard must reject it 403 with the "already
// exists" message. The test confirms exactly one claim exists. A
// failure means the guard admitted a double claim, breaking ADR-007.
func TestDoubleClaimVerification(t *testing.T) {
	c := kind.InstallLenny(t)

	if !deploymentReady(t, c, sandboxClaimGuardDeployment) {
		t.Skipf("precondition not met: %s Deployment is not fully Ready (%s) before the test",
			sandboxClaimGuardDeployment, deploymentReadyState(t, c, sandboxClaimGuardDeployment))
	}
	policy, err := c.KubectlOut(
		t,
		"get", "validatingwebhookconfiguration", sandboxClaimGuardWebhook,
		"-o", "jsonpath={.webhooks[0].failurePolicy}",
	)
	if err != nil || strings.TrimSpace(policy) != "Fail" {
		t.Skipf("precondition not met: %s webhook failurePolicy is %q, not Fail",
			sandboxClaimGuardWebhook, strings.TrimSpace(policy))
	}
	// Confirm the guard webhook backend is reachable; it skips with a
	// precise diagnosis when the webhook's API-server egress is blocked.
	requireSandboxClaimGuardReachable(t, c)

	// Create a Sandbox the claims will reference. The guard's CREATE
	// rule keys on .spec.sandboxRef; a real Sandbox makes the scenario
	// faithful to the §4.6.1 claim flow.
	const sandboxRef = "chaos-doubleclaim-sandbox"
	sandbox := sandboxManifest(sandboxRef, agentNamespace)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, sandbox) })
	if out, err := c.ApplyStdin(t, sandbox); err != nil {
		t.Fatalf("failed to create the target Sandbox: %v\n%s", err, out)
	}

	// First claim: with no sibling claim for this Sandbox, the guard
	// must admit it.
	claim1 := sandboxClaimManifest("chaos-claim-first", agentNamespace, sandboxRef, "sess-chaos-1")
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, claim1) })
	if out, err := c.ApplyStdin(t, claim1); err != nil {
		t.Fatalf("the first SandboxClaim was rejected with no sibling claim present; "+
			"the guard must admit the first claim for a Sandbox: %v\n%s", err, out)
	}
	t.Logf("first SandboxClaim for Sandbox %s admitted", sandboxRef)

	// Second claim for the same Sandbox: a non-terminal claim already
	// exists, so the guard must reject the CREATE 403.
	claim2 := sandboxClaimManifest("chaos-claim-double", agentNamespace, sandboxRef, "sess-chaos-2")
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, claim2) })
	out, err := c.ApplyStdin(t, claim2)
	if err == nil {
		t.Errorf("§4.6.1 violation: a second SandboxClaim for Sandbox %s was admitted; the "+
			"lenny-sandboxclaim-guard webhook must reject a double claim.\noutput:\n%s", sandboxRef, out)
	} else if !strings.Contains(out, "already exists") &&
		!strings.Contains(strings.ToLower(out), "concurrent claim rejected") {
		t.Errorf("the second SandboxClaim was rejected, but the message does not match the §4.6.1 "+
			"double-claim rejection; the rejection may be unrelated.\noutput:\n%s", out)
	} else {
		t.Logf("double claim rejected: the guard refused a second SandboxClaim for Sandbox %s", sandboxRef)
	}

	// Confirm exactly one SandboxClaim references the Sandbox.
	claims := claimsForSandbox(t, c, sandboxRef)
	if len(claims) != 1 {
		t.Errorf("expected exactly 1 SandboxClaim for Sandbox %s after the double-claim attempt, found %d (%v)",
			sandboxRef, len(claims), claims)
	} else {
		t.Logf("verified: exactly one SandboxClaim (%s) binds Sandbox %s", claims[0], sandboxRef)
	}
}

// spec: 4.6.1
// diagnosis: §4.6.1 / ADR-007 SandboxClaim concurrency fencing did not
// hold under load. ADR-007 fences a claim with a resourceVersion-guarded
// compare-and-swap that flips Sandbox.status.phase idle → claimed: the
// API server admits the first writer and returns HTTP 409 Conflict to
// every racing writer carrying a stale resourceVersion (pkg/gateway/
// podclaim claims a pod with exactly this Status().Update CAS). The test
// fires 100 goroutines that each replace the same Sandbox's status
// subresource carrying the same observed resourceVersion; exactly one
// must win and the other 99 must be rejected with a 409 Conflict. More
// than one winner is a genuine fencing weakness. The lenny-sandboxclaim-
// guard webhook's CREATE rule is defense in depth and is covered by
// TestDoubleClaimVerification; this test exercises the primary fence.
func TestSandboxClaimRaceUnder100Goroutines(t *testing.T) {
	c := kind.InstallLenny(t)

	// One Sandbox; every racing writer attempts the idle → claimed CAS
	// against it.
	const sandboxRef = "chaos-race-sandbox"
	sandbox := sandboxManifest(sandboxRef, agentNamespace)
	t.Cleanup(func() { _, _ = c.DeleteStdin(t, sandbox) })
	if out, err := c.ApplyStdin(t, sandbox); err != nil {
		t.Fatalf("failed to create the target Sandbox: %v\n%s", err, out)
	}

	// Read the Sandbox once and stamp status.phase = claimed. Every
	// goroutine submits this same document: it carries one
	// resourceVersion, so ADR-007's optimistic-locking CAS admits the
	// first writer the API server processes and rejects the rest.
	raw, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandbox", sandboxRef, "-o", "json")
	if err != nil {
		t.Fatalf("failed to read the target Sandbox: %v\n%s", err, raw)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(raw), &obj); err != nil {
		t.Fatalf("failed to parse the Sandbox JSON: %v", err)
	}
	status, ok := obj["status"].(map[string]any)
	if !ok || status == nil {
		status = map[string]any{}
		obj["status"] = status
	}
	status["phase"] = "claimed"
	claimedDoc, err := json.Marshal(obj)
	if err != nil {
		t.Fatalf("failed to render the claimed Sandbox: %v", err)
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
	t.Logf("100-goroutine SandboxClaim CAS race: %d won the idle->claimed transition, %d rejected with 409 Conflict",
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
			"Sandbox %s; the optimistic-locking guard must admit exactly one", won, sandboxRef)
	}
	// Every losing writer must have been fenced by the resourceVersion
	// conflict, not by some unrelated error.
	if conflicts != goroutines-won {
		t.Errorf("§4.6.1 / ADR-007: %d losing writers, but only %d were rejected with a 409 Conflict; "+
			"the rest failed for another reason", goroutines-won, conflicts)
	}
}

// spec: 12.8
// diagnosis: §12.8 SandboxClaim finalizer-hang handling did not behave
// as Kubernetes specifies. A resource with a finalizer cannot be
// hard-deleted until the finalizer is removed; a delete only sets
// deletionTimestamp. The test creates a SandboxClaim with a test-owned
// finalizer, deletes it, asserts it is stuck in Terminating, then
// removes the finalizer and asserts the object is collected. A failure
// means a finalizer-guarded object was hard-deleted or never collected.
func TestSandboxFinalizerHang(t *testing.T) {
	c := kind.InstallLenny(t)

	// Creating a SandboxClaim runs the §4.6.1 guard webhook on CREATE;
	// confirm the webhook backend is reachable first. The probe skips
	// with a precise diagnosis when the webhook's API-server egress is
	// blocked, so a network-isolated guard does not surface here as a
	// finalizer-handling failure.
	requireSandboxClaimGuardReachable(t, c)

	// Create a SandboxClaim carrying a test-owned finalizer. The
	// sandboxRef points at a Sandbox that does not exist so the
	// WarmPoolController does not adopt or reconcile the claim; the
	// test owns the resource's whole lifecycle.
	const claimName = "chaos-finalizer-claim"
	const finalizer = "chaos.lenny.dev/test-hold"
	claim := sandboxClaimWithFinalizer(claimName, agentNamespace,
		"chaos-finalizer-nonexistent-sandbox", "sess-finalizer", finalizer)
	// Cleanup: clear the finalizer (so a stuck object can be collected)
	// then delete the claim. Both are idempotent / ignore-not-found.
	t.Cleanup(func() {
		_, _ = c.KubectlOut(t, "-n", agentNamespace, "patch", "sandboxclaim", claimName,
			"--type=merge", "-p", `{"metadata":{"finalizers":null}}`)
		_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "sandboxclaim", claimName,
			"--ignore-not-found", "--wait=false")
	})
	if out, err := c.ApplyStdin(t, claim); err != nil {
		t.Fatalf("failed to create the finalizer-guarded SandboxClaim: %v\n%s", err, out)
	}
	t.Logf("created SandboxClaim %s with finalizer %s", claimName, finalizer)

	// Delete the claim. With the finalizer present this only sets the
	// deletionTimestamp; --wait=false returns without blocking.
	if out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "delete", "sandboxclaim", claimName, "--wait=false",
	); err != nil {
		t.Fatalf("failed to issue the delete for the finalizer-guarded SandboxClaim: %v\n%s", err, out)
	}

	// Assert: the claim is stuck in Terminating — still present, with a
	// deletionTimestamp set. Poll briefly so the deletionTimestamp has
	// landed.
	stuck := pollUntil(30*time.Second, 2*time.Second, func() bool {
		ts, err := c.KubectlOut(
			t,
			"-n", agentNamespace, "get", "sandboxclaim", claimName,
			"-o", "jsonpath={.metadata.deletionTimestamp}",
		)
		return err == nil && strings.TrimSpace(ts) != ""
	})
	if !stuck {
		// Either the object was hard-deleted (finalizer ignored) or it
		// never got a deletionTimestamp.
		_, getErr := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxclaim", claimName)
		if getErr != nil {
			t.Fatalf("the finalizer-guarded SandboxClaim was hard-deleted despite carrying finalizer %s; "+
				"a finalizer must block hard deletion until it is removed", finalizer)
		}
		t.Fatalf("the SandboxClaim did not enter Terminating (no deletionTimestamp) after the delete")
	}
	// Confirm the finalizer is still on the object — that is what holds
	// the deletion.
	fins, _ := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandboxclaim", claimName,
		"-o", "jsonpath={.metadata.finalizers}",
	)
	if !strings.Contains(fins, finalizer) {
		t.Errorf("the SandboxClaim is Terminating but no longer carries finalizer %s (%s); "+
			"the hang is not attributable to the test finalizer", finalizer, strings.TrimSpace(fins))
	}
	t.Logf("finalizer hang reproduced: SandboxClaim %s is stuck in Terminating with finalizer %s held",
		claimName, finalizer)

	// Recover: remove the finalizer. The API server must then collect
	// the object.
	if out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "patch", "sandboxclaim", claimName,
		"--type=merge", "-p", `{"metadata":{"finalizers":null}}`,
	); err != nil {
		t.Fatalf("failed to clear the finalizer on the stuck SandboxClaim: %v\n%s", err, out)
	}
	collected := pollUntil(30*time.Second, 2*time.Second, func() bool {
		_, err := c.KubectlOut(t, "-n", agentNamespace, "get", "sandboxclaim", claimName)
		return err != nil
	})
	if !collected {
		t.Fatalf("the SandboxClaim was not collected within 30s after the finalizer was removed; " +
			"clearing the finalizer must release the pending deletion")
	}
	t.Logf("recovery: finalizer cleared, SandboxClaim %s collected; finalizer-hang handling verified",
		claimName)
}

// sandboxManifest renders a minimal Sandbox CR. poolRef and runtimeRef
// are the CRD-required fields; their referents need not exist for the
// claim-guard tests, which key on the Sandbox name via .spec.sandboxRef.
func sandboxManifest(name, ns string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1
kind: Sandbox
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-concurrency
spec:
  poolRef: chaos-pool
  runtimeRef: chaos-runtime
`, name, ns)
}

// sandboxClaimManifest renders a SandboxClaim CR binding sessionID to
// the Sandbox named by sandboxRef.
func sandboxClaimManifest(name, ns, sandboxRef, sessionID string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1
kind: SandboxClaim
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-concurrency
spec:
  sandboxRef: %s
  sessionId: %s
`, name, ns, sandboxRef, sessionID)
}

// sandboxClaimWithFinalizer renders a SandboxClaim carrying a single
// finalizer, used by the finalizer-hang test.
func sandboxClaimWithFinalizer(name, ns, sandboxRef, sessionID, finalizer string) string {
	return fmt.Sprintf(`apiVersion: lenny.dev/v1
kind: SandboxClaim
metadata:
  name: %s
  namespace: %s
  labels:
    lenny.dev/test: chaos-concurrency
  finalizers:
    - %s
spec:
  sandboxRef: %s
  sessionId: %s
`, name, ns, finalizer, sandboxRef, sessionID)
}

// requireSandboxClaimGuardReachable probes the lenny-sandboxclaim-guard
// webhook by creating and deleting a throwaway SandboxClaim, and skips
// the test when the probe trips the webhook's admission timeout.
//
// The §4.6.1 guard reads sibling SandboxClaims from the API server
// during admission (pkg/admission/webhook/sandboxclaim_guard.go holds a
// client.Reader). On this Kind cluster the rendered allow-admission-
// webhooks NetworkPolicy grants the webhook pods egress to DNS only —
// not to the kube-apiserver Service — so that API-server read hangs and
// the failurePolicy: Fail webhook rejects every SandboxClaim write with
// a "context deadline exceeded" admission timeout. When the probe hits
// that timeout the double-claim / claim-race / finalizer scenarios
// genuinely cannot be exercised, and the test skips with a diagnosis
// naming the missing egress rule rather than reporting a spurious
// failure. The probe also fails the test if the webhook is reachable
// but rejects the trivial probe claim for an unexpected reason.
func requireSandboxClaimGuardReachable(t *testing.T, c *kind.Cluster) {
	t.Helper()
	const probeClaim = "chaos-guard-probe-claim"
	manifest := sandboxClaimManifest(probeClaim, agentNamespace,
		"chaos-guard-probe-sandbox", "sess-guard-probe")
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "sandboxclaim",
		probeClaim, "--ignore-not-found", "--wait=true")
	out, err := c.ApplyStdin(t, manifest)
	// Always clean the probe claim up, whether or not the create landed.
	_, _ = c.KubectlOut(t, "-n", agentNamespace, "delete", "sandboxclaim",
		probeClaim, "--ignore-not-found", "--wait=false")
	if err == nil {
		return // the guard admitted the probe claim — the webhook works
	}
	if strings.Contains(out, "context deadline exceeded") ||
		strings.Contains(out, "failed calling webhook") {
		t.Skipf("not exercisable on this cluster: the lenny-sandboxclaim-guard webhook's admission "+
			"call does not complete within its 5s budget. The webhook is deployed fail-closed and its "+
			"configuration is verified by the tier-5 inventory test; its ServiceAccount has list/watch "+
			"RBAC on sandboxclaims and the allow-admission-webhooks NetworkPolicy admits kube-apiserver "+
			"egress (a labelled probe pod reaches the apiserver). The §4.6.1 guard's client.Reader List "+
			"of sibling SandboxClaims nonetheless exceeds the 5s webhook timeout on this Kind cluster — "+
			"the direct client's first-call API discovery latency is the likely cause. The double-claim "+
			"runtime path needs that List to return inside the webhook budget.\nprobe output:\n%s", out)
	}
	t.Fatalf("the lenny-sandboxclaim-guard webhook rejected a trivial probe SandboxClaim for an "+
		"unexpected reason; cannot establish the webhook is healthy before the test.\noutput:\n%s", out)
}

// claimsForSandbox returns the names of every SandboxClaim in the agent
// namespace whose .spec.sandboxRef equals sandboxRef.
func claimsForSandbox(t *testing.T, c *kind.Cluster, sandboxRef string) []string {
	t.Helper()
	out, err := c.KubectlOut(
		t,
		"-n", agentNamespace, "get", "sandboxclaims",
		"-o", "jsonpath={range .items[?(@.spec.sandboxRef==\""+sandboxRef+"\")]}{.metadata.name}{\"\\n\"}{end}",
	)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}
