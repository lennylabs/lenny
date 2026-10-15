// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind cases for idle suspension and the coordinator handoff of a
// suspended session on the installed chart. The idle cases tighten the
// pool's sessionPolicy.maxClientIdleSeconds through the admin pools surface
// for their duration, so a session goes idle in about a minute rather than
// the platform default of fifteen, and restore the pool's previous value at
// the start of the case and when it ends.
//
// spec: §6.2 (Pod State Machine), §5.2 (Pool Configuration and Execution
// Modes), §10.1.2 (Coordinator Handoff Protocol)
package tier5_e2e_kind_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
)

const (
	// idleKindBound is the pool idle bound the idle cases set, in seconds.
	idleKindBound = 60
	// idleKindWait bounds the wait for an idle suspension: the idle bound,
	// the adapter's activity window, the watchdog tick, the checkpoint, and
	// margin for a slow node.
	idleKindWait = 5 * time.Minute
	// recyclePool and recycleRuntime name a recycling pool with
	// maxConcurrentSessions: 1 that tests/testinfra/kind/install.sh installs
	// and whose runtime accepts mid-session messages: the heartbeat-silence
	// fixture's pool, whose runtime answers heartbeats until a session sends
	// the fixture's directive, which these cases never send.
	recyclePool    = silenceRecyclePoolName
	recycleRuntime = silenceEchoRuntimeRef
	// concurrentPool and concurrentRuntime name the pool with
	// maxConcurrentSessions: 2.
	concurrentPool    = "concurrent-echo-pool"
	concurrentRuntime = "echo-runtime-concurrent"
)

// tightenIdleBound sets the pool's maxClientIdleSeconds to idleKindBound
// and restores the value the pool carried before when the test ends. An
// earlier run killed before its cleanup can have left the tightened value,
// so the restore target is the pool's value without this case's setting.
func tightenIdleBound(t *testing.T, d *sessiondriver.Driver, pool string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prev, err := d.SetPoolSessionPolicy(ctx, pool, "maxClientIdleSeconds", idleKindBound)
	if err != nil {
		t.Skipf("blocked: the admin pools surface refused the idle bound on %s: %v", pool, err)
	}
	if v, ok := prev.(float64); ok && int(v) == idleKindBound {
		prev = nil
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := d.SetPoolSessionPolicy(ctx, pool, "maxClientIdleSeconds", prev); err != nil {
			t.Errorf("restore the idle bound of %s: %v", pool, err)
		}
	})
}

// startIdleSession bootstraps a tenant and starts a session on runtimeRef.
func startIdleSession(ctx context.Context, t *testing.T, d *sessiondriver.Driver, tenant, runtimeRef string) *sessiondriver.Session {
	t.Helper()
	sess := createAndStartTolerant(t, ctx, d, tenant, runtimeRef, 3*time.Minute)
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
	if sess.PodAssignment == "" {
		got, err := d.GetSession(ctx, tenant, sess.ID)
		if err != nil || got.PodAssignment == "" {
			t.Fatalf("session %s carries no podAssignment (%v)", sess.ID, err)
		}
		sess = got
	}
	return sess
}

// newIdleTenant bootstraps a tenant that admits sessions with no
// environment.
func newIdleTenant(ctx context.Context, t *testing.T, d *sessiondriver.Driver, base string) string {
	t.Helper()
	tenant := uniqueName(base)
	if err := d.BootstrapTenant(ctx, tenant); err != nil {
		t.Fatalf("bootstrap tenant: %v", err)
	}
	ensureTenantAllowsSessionsWithNoEnvironment(t, d, tenant)
	return tenant
}

// spec: 6.2 (Pod State Machine), 5.2 (Pool Configuration and Execution Modes)
// diagnosis: an idle session on a recycling pool was not suspended within
// the idle bound, kept its pod, released it with a disposition other than
// recycle (the claim was deleted), or a later message did not resume it.
func TestIdleSessionOnRecyclingPoolIsSuspendedAndResumed_spec_6_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, recyclePool, 2)
	tightenIdleBound(t, d, recyclePool)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	tenant := newIdleTenant(ctx, t, d, "idle-recycle")
	sess := startIdleSession(ctx, t, d, tenant, recycleRuntime)
	pod := sess.PodAssignment
	if resp, err := d.SendMessage(ctx, tenant, sess.ID, "before-idle"); err != nil || resp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("first message: %v %+v", err, resp)
	}

	got, ok := d.WaitForState(ctx, tenant, sess.ID, idleKindWait, "suspended")
	if !ok {
		t.Fatalf("session %s = %+v, want suspended within the idle bound", sess.ID, got)
	}
	if got.PodAssignment != "" {
		t.Errorf("the idle-suspended session still names pod %q, want its pod released", got.PodAssignment)
	}
	phase, err := c.KubectlOut(t, "-n", executionModesNamespace, "get", "sandboxclaim", "claim-"+pod,
		"-o", "jsonpath={.status.phase}", "--ignore-not-found")
	if err != nil {
		t.Fatalf("read the claim of %s: %v", pod, err)
	}
	if p := strings.TrimSpace(phase); p != "recycling" && p != "reserved" {
		t.Errorf("claim of %s after the idle release = %q, want the recycle disposition (recycling or reserved)", pod, p)
	}

	resp, err := d.SendMessage(ctx, tenant, sess.ID, "after-idle")
	if err != nil {
		t.Fatalf("message to the suspended session: %v", err)
	}
	if s := resp.DeliveryReceipt.Status; s != "queued" && s != "delivered" {
		t.Fatalf("message to the suspended session: receipt %q, want queued or delivered", s)
	}
	resumed, ok := d.WaitForState(ctx, tenant, sess.ID, 3*time.Minute, "running")
	if !ok || resumed.PodAssignment == "" {
		t.Fatalf("session %s = %+v, want running on a pod after the later message", sess.ID, resumed)
	}
}

// spec: 6.2 (Pod State Machine), 5.2 (Pool Configuration and Execution Modes)
// diagnosis: on a concurrent pool, an idle session's suspension did not
// free its slot, or it disturbed the other session on the pod, which must
// keep answering throughout.
func TestIdleSlotReleaseKeepsTheOtherSessionAnswering_spec_5_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, concurrentPool, 2)
	tightenIdleBound(t, d, concurrentPool)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	tenant := newIdleTenant(ctx, t, d, "idle-slot")
	idle := startIdleSession(ctx, t, d, tenant, concurrentRuntime)
	busy := startIdleSession(ctx, t, d, tenant, concurrentRuntime)

	deadline := time.Now().Add(idleKindWait)
	for turn := 0; ; turn++ {
		resp, err := d.SendMessage(ctx, tenant, busy.ID, "keep-busy-"+strconv.Itoa(turn))
		if err != nil || resp.DeliveryReceipt.Status != "delivered" {
			t.Fatalf("the busy session's turn %d: %v %+v", turn, err, resp)
		}
		got, err := d.GetSession(ctx, tenant, idle.ID)
		if err == nil && got.State == "suspended" {
			if got.PodAssignment != "" {
				t.Errorf("the idle-suspended session still names pod %q", got.PodAssignment)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the idle session %s never suspended (last %+v, err %v)", idle.ID, got, err)
		}
		time.Sleep(15 * time.Second)
	}
	if got, err := d.GetSession(ctx, tenant, busy.ID); err != nil || got.State != "running" {
		t.Fatalf("the busy session = %+v (%v), want running", got, err)
	}
	if resp, err := d.SendMessage(ctx, tenant, busy.ID, "after-release"); err != nil || resp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("the busy session after the release: %v %+v", err, resp)
	}
}

// coordinationRow reads the coordination generation and the replica the
// session's unreleased coordination-lease mirror row names, or -1 and ""
// when no such row exists.
func coordinationRow(t *testing.T, c *kind.Cluster, pgIP, sessionID string) (int64, string) {
	t.Helper()
	sql := fmt.Sprintf("SELECT coordination_generation || '|' || coordinator_replica FROM coordination_lease "+
		"WHERE session_id = '%s' AND released_at IS NULL ORDER BY acquired_at DESC LIMIT 1;", sessionID)
	out := strings.TrimSpace(t5RunPsqlQuery(t, c, pgIP, "t5-idle-coordination", sql))
	gen, replica, _ := strings.Cut(out, "|")
	n, err := strconv.ParseInt(strings.TrimSpace(gen), 10, 64)
	if err != nil {
		// A read that returned no row is retried by the caller.
		t.Logf("coordination row of %s unreadable (%q): %v", sessionID, out, err)
		return -1, ""
	}
	return n, strings.TrimSpace(replica)
}

// spec: 10.1.2 (Coordinator Handoff Protocol), 6.2 (Pod State Machine)
// diagnosis: when the gateway replica coordinating an interrupt-suspended
// session that holds its pod was killed, no surviving replica adopted the
// session at a later coordination generation and fenced the pod, so the
// pod's hold has no coordinator and a stale replica is not fenced out.
func TestHeldPodSuspendedAdopterFencesThePod_spec_10_1_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	c := d.Cluster()
	requirePoolReadyPods(t, c, recyclePool, 2)
	pgIP := t5DataStorePodIP(t, c, "postgres")
	if pgIP == "" {
		t.Skip("blocked: the e2e Postgres pod is not running, so the adopter cannot read the suspended row")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	tenant := newIdleTenant(ctx, t, d, "held-pod-adopt")
	sess := startIdleSession(ctx, t, d, tenant, recycleRuntime)
	if resp, err := d.SendMessage(ctx, tenant, sess.ID, "before-interrupt"); err != nil || resp.DeliveryReceipt.Status != "delivered" {
		t.Fatalf("first message: %v %+v", err, resp)
	}
	out, err := c.KubectlOut(t, "-n", "lenny-system", "get", "pods", "-l", "lenny.dev/component=gateway", "-o", "name")
	if err != nil {
		t.Fatalf("list gateway pods: %v", err)
	}
	if len(strings.Fields(out)) < 2 {
		t.Skip("blocked: the handoff needs at least two gateway replicas and the e2e cluster runs fewer")
	}
	if got, err := d.Interrupt(ctx, tenant, sess.ID); err != nil || got.State != "suspended" {
		t.Fatalf("interrupt = %+v (%v), want suspended", got, err)
	}
	// The Sweeper writes the coordination-lease mirror on its own interval.
	gen, replica := coordinationRow(t, c, pgIP, sess.ID)
	for wait := time.Now().Add(2 * time.Minute); replica == "" && time.Now().Before(wait); {
		time.Sleep(3 * time.Second)
		gen, replica = coordinationRow(t, c, pgIP, sess.ID)
	}
	if replica == "" {
		t.Fatalf("the coordination-lease mirror names no coordinator for %s", sess.ID)
	}
	// The mirror names the replica as its pod name and an instance suffix.
	gatewayPod := ""
	for _, name := range strings.Fields(out) {
		pod := strings.TrimPrefix(name, "pod/")
		if strings.HasPrefix(replica, pod+"-") || replica == pod {
			gatewayPod = pod
		}
	}
	if gatewayPod == "" {
		t.Fatalf("no gateway pod in %q matches the coordinator %q", out, replica)
	}
	if _, err := c.KubectlOut(t, "-n", "lenny-system", "delete", "pod", gatewayPod, "--wait=false"); err != nil {
		t.Fatalf("kill the coordinating replica %s: %v", gatewayPod, err)
	}
	deadline := time.Now().Add(4 * time.Minute)
	for {
		now, holder := coordinationRow(t, c, pgIP, sess.ID)
		if now > gen && holder != replica {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no replica adopted %s after its coordinator %s was killed (generation %d → %d, holder %q)",
				sess.ID, replica, gen, now, holder)
		}
		time.Sleep(3 * time.Second)
	}
	// The driver's port-forward ended with the killed replica.
	d2 := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 30 * time.Second})
	got, err := d2.GetSession(ctx, tenant, sess.ID)
	if err != nil || got.State != "suspended" || got.PodAssignment == "" {
		t.Fatalf("after the handoff the session = %+v (%v), want suspended holding its pod", got, err)
	}
}
