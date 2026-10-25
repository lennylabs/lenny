// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos tests for the runtime supervisor on the Kind cluster's
// supervised probe pools (tests/testinfra/kind/install.sh). They inject the
// failures the §4.7.10 Runtime supervisor Signals and Liveness tables name:
// signals to the supervisor from inside and from outside its PID namespace,
// the adapter's death, the runtime process's death on an idle or reserved
// pod, and pod deletion. A signal from outside the namespace is sent as root
// on the Kind node to the supervisor's host PID, which crictl reports, so
// these tests need docker access to the Kind nodes.

package tier8_chaos_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
	"github.com/lennylabs/lenny/tests/testinfra/supervisedpod"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// supLivenessWindow is longer than the Kind install's rendered ping interval
// plus ping timeout (2s + 2s), so a supervisor that stopped answering has
// missed a pong by the time it elapses.
const supLivenessWindow = 10 * time.Second

// spec: §4.7.10 (Runtime supervisor, Signals; Liveness), §4.7.11 item 8
//
// diagnosis: SIGKILL or SIGSTOP sent to the supervisor from inside its PID
// namespace (by the agent UID) retired the pod, stopped PID 1, or stopped
// the supervisor answering ping, which the adapter records as a CH-SUPERVISE
// link failure or a not-live cause in its log. On runc the namespace init is
// immune to both, so a failure here means the supervisor is not PID 1 of the
// runtime container or the kernel delivered the signal.
func TestSupervisorIgnoresSIGKILLAndSIGSTOPFromInsideItsNamespace_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)[0]
	for _, sig := range []string{"9", "19"} {
		supervisedpod.Attack(t, c, pod.Name, superviseprobe.AttackSignalPID1, sig)
	}
	time.Sleep(supLivenessWindow)
	st, found := supervisedpod.GetPod(t, c, pod.Name)
	if !found || st.Deleting || !st.Ready {
		t.Fatalf("pod %s retired after SIGKILL and SIGSTOP from inside the namespace: found=%v %s", pod.Name, found, st.Describe())
	}
	if a := st.Container["adapter"]; !a.Running || a.RestartCount != 0 {
		t.Errorf("pod %s: the adapter is not running after in-namespace signals to the supervisor: %s", pod.Name, st.Describe())
	}
	// The pod and adapter state above cannot see a missed pong: on an idle
	// keep pod it goes down the Liveness Start channel and leaves the pod
	// Ready. The adapter's log records it.
	supervisedpod.RequireSupervisorStillLive(t, c, pod.Name)
}

// spec: §4.7.10 (Runtime supervisor, Signals; Liveness), §6.2
//
// diagnosis: SIGKILL sent to the supervisor from outside its PID namespace
// (through the node's container runtime) did not retire the pod: the runtime
// container's termination was not observed, the pod was not deleted, or no
// replacement became Ready.
func TestSupervisorKilledFromOutsideItsNamespaceRetiresThePod_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)[0]
	node, pid := supervisedpod.HostPID(t, pod, "runtime")
	supervisedpod.HostSignal(t, node, pid, "KILL")
	if last, gone := supervisedpod.WaitGone(t, c, pod.Name, 4*time.Minute); !gone {
		t.Fatalf("pod %s still exists 4m after its supervisor was killed from outside: %s", pod.Name, last.Describe())
	}
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 4*time.Minute)
}

// spec: §28.5.3 (CH-SUPERVISE, Degradation), §4.7.10 (Runtime supervisor,
// End)
//
// diagnosis: killing the adapter of a supervised pod with a live runtime
// process left the supervisor or the runtime process running. The
// supervisor exits when its CH-SUPERVISE connection closes, and its exit
// ends every process in the runtime container.
func TestAdapterDeathEndsTheSupervisorAndTheRuntimeProcess_spec_28_5_3(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	tenant := supervisedpod.NewTenant(ctx, t, d, "supervisor-adapter-death")
	sess, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start a session: %v", err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
	if n := len(supervisedpod.ServingProbes(supervisedpod.Processes(t, c, sess.PodAssignment))); n != 1 {
		t.Fatalf("pod %s runs %d runtime processes during a session, want 1", sess.PodAssignment, n)
	}
	st, _ := supervisedpod.GetPod(t, c, sess.PodAssignment)
	node, pid := supervisedpod.HostPID(t, st, "adapter")
	supervisedpod.HostSignal(t, node, pid, "KILL")

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		st, found := supervisedpod.GetPod(t, c, sess.PodAssignment)
		if !found || !st.Container["runtime"].Running {
			return
		}
		time.Sleep(time.Second)
	}
	st, _ = supervisedpod.GetPod(t, c, sess.PodAssignment)
	t.Fatalf("pod %s: the runtime container is still running 2m after the adapter was killed: %s", sess.PodAssignment, st.Describe())
}

// spec: §4.7.10 (Runtime supervisor, Launch; Liveness), §6.1 (Adapter SIGTERM
// behavior during sdk_connecting)
//
// diagnosis: a service-mode pod whose supervisor was stopped from outside
// its namespace during the warm-up launch's dial delay was not deleted: the
// adapter did not treat the missed pong as a link failure before READY, or
// did not exit 1, so the pod was not retired through the Exit channel.
func TestSupervisorStoppedDuringTheWarmUpLaunchRetiresTheServicePod_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := warmingSupervisedPod(t, c, supervisedpod.ServicePool)
	watch := supervisedpod.WatchPod(t, c, pod.Name)
	node, pid := supervisedpod.HostPID(t, pod, "runtime")
	supervisedpod.HostSignal(t, node, pid, "STOP")
	last, gone := supervisedpod.WaitGone(t, c, pod.Name, 4*time.Minute)
	if !gone {
		supervisedpod.DeletePod(t, c, pod.Name)
		t.Fatalf("service pod %s was not deleted within 4m after its supervisor was stopped during warm-up: %s",
			pod.Name, last.Describe())
	}
	a, seen := supervisedpod.FirstTerminated(watch.States(), "adapter")
	if !seen {
		t.Fatalf("pod %s: no adapter termination was observed before the pod was deleted", pod.Name)
	}
	if a.ExitCode != 1 {
		t.Errorf("pod %s: the adapter exited %d after a missed pong before READY, want 1", pod.Name, a.ExitCode)
	}
}

// spec: §4.7.10 (Runtime supervisor, Liveness), §6.2
//
// diagnosis: a SIGSTOP sent from outside its namespace to the supervisor of
// an idle service-mode pod did not end with the pod deleted: the ping did
// not detect the stopped supervisor, or the service-mode adapter did not
// exit after READY.
func TestSupervisorStoppedOnAnIdleServicePodRetiresIt_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := supervisedpod.WaitReadyPods(t, c, supervisedpod.ServicePool, 1, 4*time.Minute)[0]
	node, pid := supervisedpod.HostPID(t, pod, "runtime")
	supervisedpod.HostSignal(t, node, pid, "STOP")
	t.Cleanup(func() { supervisedpod.DeletePod(t, c, pod.Name) })
	if last, gone := supervisedpod.WaitGone(t, c, pod.Name, 4*time.Minute); !gone {
		t.Fatalf("idle service pod %s was not deleted within 4m after its supervisor was stopped: %s", pod.Name, last.Describe())
	}
}

// warmingSupervisedPod returns a pod of pool whose warm-up process is
// launched and inside its dial delay: it deletes one Ready pod and waits for
// the replacement's launch.
func warmingSupervisedPod(t *testing.T, c *kind.Cluster, pool string) supervisedpod.PodState {
	t.Helper()
	ready := supervisedpod.WaitReadyPods(t, c, pool, 1, 4*time.Minute)
	known := map[string]bool{}
	for _, p := range supervisedpod.PoolPods(t, c, pool) {
		known[p.Name] = true
	}
	supervisedpod.DeletePod(t, c, ready[0].Name)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		for _, p := range supervisedpod.PoolPods(t, c, pool) {
			if known[p.Name] || p.Ready || p.Deleting || !p.Container["runtime"].Running {
				continue
			}
			out, err := supervisedpod.ExecRuntime(t, c, p.Name, superviseprobe.SubProcesses)
			if err == nil && strings.Contains(out, `"cmdline":["`+supervisedpod.ProbeExe+`"]`) {
				return p
			}
		}
		time.Sleep(time.Second)
	}
	t.Skipf("not-yet-applicable: no replacement pod of %s reached its warm-up launch within 2m", pool)
	return supervisedpod.PodState{}
}

// spec: §4.7.10 (Runtime supervisor, Liveness; Crash), §5.2 (Recycle
// lifecycle; Pinned idle inventory)
//
// diagnosis: a supervised keep pod whose runtime process died while it was
// idle (it had served a session, its reserved hold had ended, and it stayed
// pinned to its tenant as pinned idle inventory) took the Exit channel
// instead of the Start channel: its adapter exited or restarted after
// handling the exit, or the pod was deleted or claimed before any start. Or
// the next session start by its pinned tenant was not placed on it,
// succeeded and ran a session on it, or was not followed by the pod's drain.
// The kept process is gone and a keep pod does not relaunch, so the adapter
// must keep running and refuse the start that follows the gateway's claim of
// the idle pod.
func TestIdleKeepPodWhoseRuntimeDiedFailsTheNextStartAndDrains_spec_4_7_10(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.IdlePool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	tenant, pod := supervisedpod.IdleUsedPod(ctx, t, d, c)
	out, err := supervisedpod.ExecRuntime(t, c, pod, superviseprobe.SubKillRuntime)
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("kill the runtime process of %s: %v\n%s", pod, err, out)
	}
	requireStartChannelAfterIdleExit(t, c, pod)
	if !supervisedpod.FirstIdleInClaimOrder(t, c, supervisedpod.IdlePool, pod) {
		t.Skipf("not-yet-applicable: another idle pod of %s sorted before %s while its runtime process died",
			supervisedpod.IdlePool, pod)
	}

	// The pod is pinned to the tenant that used it and is claimable only by
	// that tenant, so the next start is that tenant's.
	claim := supervisedpod.WatchClaim(t, c, pod)
	next, err := d.CreateAndStart(ctx, tenant, supervisedpod.IdleRuntime)
	if err == nil {
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, next.ID) })
		t.Fatalf("the start by tenant %s, whose pinned pod %s is the pool's first idle pod, succeeded on %s; a "+
			"start on a pod whose kept runtime process died must fail and run no session", tenant, pod, next.PodAssignment)
	}
	if !claim.Claimed() {
		t.Fatalf("the failed start (%v) never claimed idle pod %s, so it failed for another reason, such as "+
			"capacity, rather than on the pod whose runtime process died", err, pod)
	}
	if last, gone := supervisedpod.WaitGone(t, c, pod, 4*time.Minute); !gone {
		t.Fatalf("pod %s was not drained within 4m after refusing a start: %s", pod, last.Describe())
	}
}

// requireStartChannelAfterIdleExit waits until pod's adapter has recorded
// the idle runtime process's exit as a not-live cause, lets the window an
// Exit-channel exit would need pass, and asserts the Start channel's state:
// the adapter still running with no restart, and the pod still idle, with no
// claim and no deletion. spec: §4.7.10 (Runtime supervisor, Liveness).
func requireStartChannelAfterIdleExit(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	deadline := time.Now().Add(supLivenessWindow * 3)
	for len(supervisedpod.AdapterEventTimes(t, c, pod, adapter.LogRuntimeNotLive)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("pod %s: the adapter logged no %s within %s after the idle runtime process died",
				pod, adapter.LogRuntimeNotLive, supLivenessWindow*3)
		}
		time.Sleep(time.Second)
	}
	time.Sleep(supLivenessWindow)
	st, found := supervisedpod.GetPod(t, c, pod)
	if !found || st.Deleting {
		t.Fatalf("pod %s was retired before any start after its idle runtime process died: found=%v %s",
			pod, found, st.Describe())
	}
	if a := st.Container["adapter"]; !a.Running || a.RestartCount != 0 {
		t.Fatalf("pod %s: the adapter exited after the idle runtime process died; the Start channel keeps it "+
			"running until the next start: %s", pod, st.Describe())
	}
	if supervisedpod.ClaimExists(t, c, pod) {
		t.Fatalf("pod %s was claimed before the test's start", pod)
	}
}

// spec: §4.7.10 (Runtime supervisor, Liveness), §5.2 (Recycle lifecycle)
//
// diagnosis: a supervised keep pod whose supervisor was stopped from outside
// its namespace while it was reserved accepted the tenant's rebind, or was
// not drained after it refused the rebind.
func TestReservedPodWithAStoppedSupervisorRefusesTheRebindAndDrains_spec_4_7_10(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	tenant, pod := supervisedpod.ReservedKeepPod(ctx, t, d, c)
	st, _ := supervisedpod.GetPod(t, c, pod)
	node, pid := supervisedpod.HostPID(t, st, "runtime")
	supervisedpod.HostSignal(t, node, pid, "STOP")
	t.Cleanup(func() { supervisedpod.DeletePod(t, c, pod) })
	// Wait past the ping interval plus timeout, inside the reserved hold, so
	// the adapter has seen the missed pong before the rebind.
	time.Sleep(5 * time.Second)
	requireNextStartAvoidsAndDrains(ctx, t, d, c, tenant, pod)
}

// requireNextStartAvoidsAndDrains starts the tenant's next session and
// asserts that it was not served on pod and that pod is drained.
func requireNextStartAvoidsAndDrains(ctx context.Context, t *testing.T, d *sessiondriver.Driver, c *kind.Cluster, tenant, pod string) {
	t.Helper()
	next, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err == nil {
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, next.ID) })
		if next.PodAssignment == pod {
			t.Fatalf("the tenant's next session was served on %s, whose runtime is not live", pod)
		}
	}
	if last, gone := supervisedpod.WaitGone(t, c, pod, 4*time.Minute); !gone {
		t.Fatalf("pod %s was not drained within 4m after refusing a start: %s", pod, last.Describe())
	}
}

// spec: §4.7.10 (Runtime supervisor, Signals)
//
// diagnosis: deleting a supervised pod whose image's STOPSIGNAL is SIGINT
// delivered SIGINT to its runtime process. The supervisor forwards no stop
// signal, and its end sends SIGTERM, so a SIGINT in the probe's log means
// the kubelet's stop signal reached the runtime process.
func TestPodDeletionDeliversNoStopSignalToTheRuntimeProcess_spec_4_7_10(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tenant := supervisedpod.NewTenant(ctx, t, d, "supervisor-stopsignal")
	sess, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start a session: %v", err)
	}
	logs := supervisedpod.StreamLogs(t, c, sess.PodAssignment, "runtime")
	supervisedpod.DeletePod(t, c, sess.PodAssignment)
	if last, gone := supervisedpod.WaitGone(t, c, sess.PodAssignment, 4*time.Minute); !gone {
		t.Fatalf("pod %s still exists 4m after deletion: %s", sess.PodAssignment, last.Describe())
	}
	if out := logs.Stop(); strings.Contains(out, superviseprobe.SignalLogPrefix+" interrupt") {
		t.Errorf("the runtime process of %s received SIGINT at pod deletion:\n%s", sess.PodAssignment, out)
	}
}
