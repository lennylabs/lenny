// SPDX-License-Identifier: MIT

//go:build e2e_kind

package tier5_e2e_kind_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

// sandboxPhaseOf reads the pod's Sandbox status.phase.
func sandboxPhaseOf(t *testing.T, c *kind.Cluster, pod string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "sandbox", pod, "-o", "jsonpath={.status.phase}")
	if err != nil {
		t.Fatalf("read the Sandbox phase of %s: %v\n%s", pod, err, out)
	}
	return strings.TrimSpace(out)
}

// requirePodStaysClaimed fails when, over the hold window, the pod's claim
// leaves `bound` or its Sandbox leaves `claimed`, which is the drain a
// deleted shared claim would start.
func requirePodStaysClaimed(t *testing.T, c *kind.Cluster, pod string, hold time.Duration) {
	t.Helper()
	deadline := time.Now().Add(hold)
	for time.Now().Before(deadline) {
		claim, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "sandboxclaim", "claim-"+pod,
			"-o", "jsonpath={.status.phase}")
		if err != nil {
			t.Fatalf("the claim of %s is gone after the not-started session's terminate: %v\n%s", pod, err, claim)
		}
		if got := strings.TrimSpace(claim); got != "bound" {
			t.Fatalf("the claim of %s is %q after the not-started session's terminate, want bound", pod, got)
		}
		if phase := sandboxPhaseOf(t, c, pod); phase != "claimed" {
			t.Fatalf("Sandbox %s is %q after the not-started session's terminate, want claimed", pod, phase)
		}
		time.Sleep(2 * time.Second)
	}
}

// spec: §6.2 (Pod State Machine), §4.6.1 (Occupancy projection), §5.2 (Slot
// assignment atomicity)
// diagnosis: on a concurrent pool, terminating a session that was created
// but never started deleted the pod's shared claim or otherwise drained the
// pod, so the running sibling on the same pod ended with it. The release of
// a not-started session removes only its own slot member; the pod stays
// claimed while a sibling runs, and the sibling keeps answering messages.
func TestConcurrentNotStartedTerminateKeepsTheSiblingRunning_spec_6_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, concurrentPoolName, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	tenant := uniqueName("concurrent-terminate")
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)

	sibling, err := d.CreateAndStart(ctx, tenant, concurrentRuntimeRef)
	if err != nil {
		t.Fatalf("start the sibling on %s: %v", concurrentRuntimeRef, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sibling.ID) })
	pod := sibling.PodAssignment
	if pod == "" {
		t.Fatal("the sibling carries no podAssignment")
	}
	created, err := d.CreateSession(ctx, tenant, concurrentRuntimeRef)
	if err != nil {
		t.Fatalf("create the not-started session: %v", err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, created.ID) })
	if created.PodAssignment != pod {
		t.Skipf("not-yet-applicable: the created session reserved on %q rather than the sibling's pod %q, "+
			"so the case does not share a pod", created.PodAssignment, pod)
	}

	if err := d.Terminate(ctx, tenant, created.ID); err != nil {
		t.Fatalf("terminate the not-started session: %v", err)
	}
	requirePodStaysClaimed(t, c, pod, 20*time.Second)

	if _, err := d.SendMessage(ctx, tenant, sibling.ID, "still here"); err != nil {
		t.Fatalf("the sibling did not answer after the terminate: %v", err)
	}
	if err := d.Terminate(ctx, tenant, sibling.ID); err != nil {
		t.Fatalf("terminate the sibling: %v", err)
	}
	if row, err := d.GetSession(ctx, tenant, sibling.ID); err != nil {
		t.Fatalf("read the sibling: %v", err)
	} else if row.State == "failed" {
		t.Errorf("the sibling ended %s (failureClass %q), want a clean end", row.State, row.FailureClass)
	}
}
