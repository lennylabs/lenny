// SPDX-License-Identifier: MIT

//go:build e2e_kind

// Tier-5 e2e Kind tests for the runtime supervisor on sidecar pods of a
// type: agent runtime. They run against the supervised probe pools
// tests/testinfra/kind/install.sh seeds on the probe runtime
// (tests/testinfra/superviseprobe): a reusing recycling pool under
// recycle.runtimeProcess: keep, a sidecar preConnect pool created while the
// SDK-warm circuit breaker is closed, one created while it is open, and a
// service-mode pool. The preConnect and service-mode probes delay their
// CH-MSGSOCK dial by supervisedpod.DialDelay.
//
// Gateway pool admission refuses recycle.runtimeProcess: restart until
// proposal 0084 lands, so the cases that need a `restart` pod are written
// against supervisedpod.RestartPool and skip with a blocked reason first.

package tier5_e2e_kind_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
	"github.com/lennylabs/lenny/tests/testinfra/supervisedpod"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// supervisorBinary is the runtime container's first process on a supervised
// pod, staged into the read-only supervisor volume. spec: §13.1 (Runtime
// supervisor volume).
const supervisorBinary = "/lenny-supervisor/lenny-supervisor"

// supervisedAgentUID is the agent UID the Kind install renders
// (--agent-uid in the controller argv), at which the supervisor launches the
// runtime process and runs cleanupCommands.
const supervisedAgentUID = 65533

// supStart opens a session on runtime for a fresh tenant and returns the
// tenant and the session.
func supStart(ctx context.Context, t *testing.T, d *sessiondriver.Driver, runtime string, plan json.RawMessage) (string, *sessiondriver.Session) {
	t.Helper()
	tenant := supervisedpod.NewTenant(ctx, t, d, "supervisor")
	return tenant, supStartFor(ctx, t, d, tenant, runtime, plan)
}

// supStartFor opens a session on runtime for tenant.
func supStartFor(ctx context.Context, t *testing.T, d *sessiondriver.Driver, tenant, runtime string, plan json.RawMessage) *sessiondriver.Session {
	t.Helper()
	var (
		s   *sessiondriver.Session
		err error
	)
	if plan != nil {
		s, err = d.CreateAndStartWithPlan(ctx, tenant, runtime, plan)
	} else {
		s, err = d.CreateAndStart(ctx, tenant, runtime)
	}
	if err != nil {
		t.Fatalf("start a session on %s: %v", runtime, err)
	}
	return s
}

// spec: §4.7.10 (Runtime supervisor), §13.1 (Runtime supervisor volume),
// §4.7.9 (startup sequence)
//
// diagnosis: a sidecar pod of a type: agent runtime became Ready without the
// runtime supervisor as its runtime container's first process, without the
// lenny-supervisor-stage init container having staged the binary, or with a
// runtime process already running on an idle session-mode pod, where the
// first launch belongs to startup step 7 after the manifest exists. Either
// the pod builder stopped rendering the supervisor or the launch moved ahead
// of the manifest (the first-session manifest defect the supervisor closes).
func TestSupervisedPodsReadyOnlyWithTheSupervisorAsFirstProcess_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	pods := supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)

	for _, p := range pods {
		procs := supervisedpod.Processes(t, c, p.Name)
		var pid1 []string
		for _, pr := range procs {
			if pr.PID == 1 {
				pid1 = pr.Cmdline
			}
		}
		if len(pid1) < 2 || pid1[0] != supervisorBinary || pid1[1] != "run" {
			t.Errorf("pod %s: PID 1 of the runtime container is %q, want %s run ...", p.Name, pid1, supervisorBinary)
		}
		if probes := supervisedpod.ServingProbes(procs); len(probes) != 0 {
			t.Errorf("pod %s: an idle session-mode pod runs %d runtime processes before any session; the first "+
				"launch belongs to startup step 7, after the manifest exists", p.Name, len(probes))
		}
		stage := podInitState(t, c, p.Name, "lenny-supervisor-stage")
		if stage != "Completed:0" {
			t.Errorf("pod %s: lenny-supervisor-stage init container state %q, want Completed:0", p.Name, stage)
		}
	}
	requireEverySidecarAgentPodSupervised(t, c)
}

// supervisedPools are every supervised probe pool the Kind install seeds:
// the session-mode pools that launch at step 7 (keep, idle, and the pool
// created while the SDK-warm breaker is open), and the pools whose warm-up
// launch runs before READY (preConnect and service mode).
var supervisedPools = []string{
	supervisedpod.KeepPool,
	supervisedpod.IdlePool,
	supervisedpod.PodWarmPool,
	supervisedpod.PreconnectPool,
	supervisedpod.ServicePool,
}

// spec: §4.7.10 (Runtime supervisor, Launch), §4.7.9 (step 4)
//
// diagnosis: a supervised pod turned Ready before its adapter accepted the
// runtime supervisor's ready on CH-SUPERVISE, or its adapter log records no
// accepted ready at all. The adapter binds its gRPC listener, which the
// readiness probe reads as READY, only after the supervisor gate returns, so
// either the bind moved ahead of the gate or the gate stopped waiting for
// the supervisor's connection. The pools that do not warm up (keep, idle,
// and pod-warm) carry no dial delay that would hide the reordering, so they
// are the discriminating cases.
func TestEverySupervisedPodIsReadyOnlyAfterTheSupervisorConnects_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	for _, pool := range supervisedPools {
		t.Run(pool, func(t *testing.T) {
			for _, p := range supervisedpod.WaitReadyPods(t, c, pool, 1, 4*time.Minute) {
				connects := supervisedpod.AdapterEventTimes(t, c, p.Name, adapter.LogSupervisorReady)
				if err := supervisedpod.CheckReadyAfterSupervisor(p.ReadyAt, connects); err != nil {
					t.Errorf("pod %s: %v", p.Name, err)
				}
			}
		})
	}
}

// podInitState is "REASON:EXIT" of a pod's init container.
func podInitState(t *testing.T, c *kind.Cluster, pod, name string) string {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", supervisedpod.Namespace, "get", "pod", pod, "-o",
		fmt.Sprintf(`jsonpath={.status.initContainerStatuses[?(@.name=="%s")].state.terminated.reason}:{.status.initContainerStatuses[?(@.name=="%s")].state.terminated.exitCode}`, name, name))
	if err != nil {
		t.Fatalf("read init container %s of %s: %v\n%s", name, pod, err, out)
	}
	return strings.TrimSpace(out)
}

// requireEverySidecarAgentPodSupervised asserts that every Ready sidecar pod
// in the agent namespace whose runtime container runs a command runs the
// supervisor as that command. spec: §4.7.10 (Runtime supervisor).
func requireEverySidecarAgentPodSupervised(t *testing.T, c *kind.Cluster) {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", supervisedpod.Namespace, "get", "pods", "-l", "lenny.dev/managed=true", "-o",
		`jsonpath={range .items[*]}{.metadata.name}{"\t"}{.spec.containers[?(@.name=="adapter")].name}{"\t"}{.spec.containers[?(@.name=="runtime")].command[0]}{"\n"}{end}`)
	if err != nil {
		t.Fatalf("list agent pods: %v\n%s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 3 || f[1] != "adapter" {
			continue // embedded pod: no adapter container and no supervisor
		}
		if f[2] != supervisorBinary {
			t.Errorf("sidecar pod %s: runtime container command %q, want the supervisor %s", f[0], f[2], supervisorBinary)
		}
	}
}

// spec: §5.2 (Runtime-process lifetime; Supervised scrub sequence), §4.7.10
// (Runtime supervisor, Cleanup; Environment)
//
// diagnosis: on a supervised `keep` pod, either the second session was not
// served by the runtime process that served the first (the pod ended its
// runtime process at the boundary, which only a `restart` pod does), or the
// deployer's cleanupCommands did not run in the runtime container at the
// agent UID with the Runtime's env and the workspace base as working
// directory, or a symbolic link the agent planted toward /run/lenny gave the
// command access the agent itself lacks.
func TestKeepPodKeepsOneProcessAndRunsCleanupAtTheAgentUID_spec_5_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant, sessA := supStart(ctx, t, d, supervisedpod.KeepRuntime, nil)
	recA := supervisedpod.Report(ctx, t, d, tenant, sessA.ID)
	plant := supervisedpod.Action(ctx, t, d, tenant, sessA.ID, superviseprobe.CmdSymlink)
	if !plant.OK {
		t.Fatalf("plant a link under the workspace base: %s", plant.Error)
	}
	if err := d.Terminate(ctx, tenant, sessA.ID); err != nil {
		t.Fatalf("terminate session A: %v", err)
	}
	waitClaimPhase(t, c, sessA.PodAssignment, "reserved", 2*time.Minute)

	sessB := supStartFor(ctx, t, d, tenant, supervisedpod.KeepRuntime, nil)
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sessB.ID) })
	if sessB.PodAssignment != sessA.PodAssignment {
		t.Fatalf("session B landed on %s, want the reused keep pod %s", sessB.PodAssignment, sessA.PodAssignment)
	}
	recB := supervisedpod.Report(ctx, t, d, tenant, sessB.ID)
	if recB.Token != recA.Token {
		t.Errorf("keep pod %s: session B's runtime process token %s differs from session A's %s; a keep pod "+
			"keeps one runtime process across its sessions", sessA.PodAssignment, recB.Token, recA.Token)
	}
	requireCleanupRecord(t, recB, plant)
}

// requireCleanupRecord asserts the cleanup command's record on the kept
// runtime process: agent UID, workspace base, the Runtime's env and no image
// ENV, and no access through the planted link beyond the agent's own.
func requireCleanupRecord(t *testing.T, rec superviseprobe.Record, plant superviseprobe.ActionResult) {
	t.Helper()
	if len(rec.Cleanups) == 0 {
		t.Fatalf("no cleanup command reported to the kept runtime process (record server error %q); the "+
			"supervisor did not run cleanupCommands in the runtime container", rec.RecordServerError)
	}
	cr := rec.Cleanups[len(rec.Cleanups)-1]
	if cr.UID != supervisedAgentUID {
		t.Errorf("cleanup command ran at UID %d, want the agent UID %d", cr.UID, supervisedAgentUID)
	}
	if cr.Cwd != superviseprobe.WorkspaceBase {
		t.Errorf("cleanup command working directory %q, want the workspace base %s", cr.Cwd, superviseprobe.WorkspaceBase)
	}
	if v, _ := supervisedpod.EnvValue(cr.Env, superviseprobe.RuntimeEnvVar); v != "from-runtime-env" {
		t.Errorf("cleanup command %s = %q, want the Runtime env value", superviseprobe.RuntimeEnvVar, v)
	}
	if _, ok := supervisedpod.EnvValue(cr.Env, superviseprobe.ImageEnvVar); ok {
		t.Errorf("cleanup command carries the image ENV %s", superviseprobe.ImageEnvVar)
	}
	direct := map[string]bool{}
	for _, a := range plant.Direct {
		direct[a.Op] = a.OK
	}
	for _, a := range cr.Link {
		if a.OK && !direct[a.Op] {
			t.Errorf("cleanup command could %s through the planted link %s, which the agent cannot do at %s",
				a.Op, a.Path, superviseprobe.AdapterRunDir)
		}
	}
}

// spec: §5.2 (Runtime-process lifetime; Supervised scrub sequence), §4.7.10
// (Runtime supervisor, End; Sweep)
//
// diagnosis: on a `restart` pod two sequential sessions reported the same
// runtime process, or a daemon, IPC objects, a planted link, or a background
// process a cleanup command started survived into the next session, so the
// boundary end and sweep did not reach the runtime container.
func TestRestartPodLaunchesAFreshProcessAndRemovesResidue_spec_5_2(t *testing.T) {
	t.Skip("blocked: pool admission refuses recycle.runtimeProcess restart until proposal 0084 rebinds the " +
		"intra-pod MCP servers per runtime generation, so no restart pool exists on the Kind cluster")
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.RestartPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant, sessA := supStart(ctx, t, d, supervisedpod.RestartRuntime, nil)
	recA := supervisedpod.Report(ctx, t, d, tenant, sessA.ID)
	residue := supervisedpod.PlantAgentResidue(ctx, t, d, tenant, sessA.ID)
	sessB := supervisedpod.NextSessionOnPod(ctx, t, d, c, tenant, supervisedpod.RestartRuntime, sessA)
	pod := sessA.PodAssignment

	t.Run("two sessions report different process tokens", func(t *testing.T) {
		if recB := supervisedpod.Report(ctx, t, d, tenant, sessB.ID); recB.Token == recA.Token {
			t.Errorf("restart pod %s served both sessions on runtime process %s; the boundary end must end it "+
				"and the next session's step 7 launch a fresh one", pod, recA.Token)
		}
	})
	t.Run("daemon, IPC objects, and planted link absent in the next session", func(t *testing.T) {
		if left := supervisedpod.ResidueLeft(supervisedpod.Residue(ctx, t, d, tenant, sessB.ID, residue)); len(left) != 0 {
			t.Errorf("restart pod %s: %v from session A survived into session B", pod, left)
		}
	})
	t.Run("background process a cleanup command started is gone before the next session", func(t *testing.T) {
		if daemons := supervisedpod.ProbeDaemons(supervisedpod.Processes(t, c, pod)); len(daemons) != 0 {
			t.Errorf("restart pod %s: background processes %+v a cleanup command started still run in the "+
				"runtime container; the second end and sweep must remove them", pod, daemons)
		}
	})
}

// spec: §4.7.10 (Runtime supervisor, Environment; Launch), §4.7.6 (Adapter
// Manifest Field Reference, mcpNonce row), §15.4.3 (Authentication)
//
// diagnosis: the first session's runtime process started from an
// environment other than the Runtime's env plus the platform variables (an
// image ENV or a kubelet-injected variable leaked through, so the supervisor
// did not clear its environment), read a manifest other than the one the
// adapter wrote for the session (the launch ran before the final manifest),
// or could not authenticate its platform MCP call with the nonce it read.
func TestFirstSessionProcessReadsTheFinalManifestWithTheLaunchEnvironment_spec_4_7_10(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	tenant, sess := supStart(ctx, t, d, supervisedpod.KeepRuntime, nil)
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
	rec := supervisedpod.Report(ctx, t, d, tenant, sess.ID)

	if v, _ := supervisedpod.EnvValue(rec.Env, superviseprobe.RuntimeEnvVar); v != "from-runtime-env" {
		t.Errorf("runtime process %s = %q, want the Runtime env value", superviseprobe.RuntimeEnvVar, v)
	}
	if v, _ := supervisedpod.EnvValue(rec.Env, "LENNY_ADAPTER_SOCKET"); v == "" {
		t.Error("runtime process carries no LENNY_ADAPTER_SOCKET platform variable")
	}
	for _, leaked := range []string{superviseprobe.ImageEnvVar, "KUBERNETES_SERVICE_HOST", "HOSTNAME"} {
		if v, ok := supervisedpod.EnvValue(rec.Env, leaked); ok {
			t.Errorf("runtime process carries %s=%q, which the supervisor's cleared environment must drop", leaked, v)
		}
	}
	if !rec.ManifestAtStart.Present || !rec.ManifestAtStart.PlatformMCP {
		t.Fatalf("the first session's process read no manifest with a platform MCP server at start: %+v", rec.ManifestAtStart)
	}
	if rec.ManifestAtStart.NonceDigest != rec.ManifestNow.NonceDigest {
		t.Errorf("the first session's process read manifest nonce %s at start, but the session's manifest carries %s",
			rec.ManifestAtStart.NonceDigest, rec.ManifestNow.NonceDigest)
	}
	if mcp := supervisedpod.Action(ctx, t, d, tenant, sess.ID, superviseprobe.CmdMCP); !mcp.OK {
		t.Errorf("platform MCP call from the launched process failed: %s", mcp.Error)
	}
}

// spec: §4.7.10 (Runtime supervisor, SDK-warm; Launch), §6.1 (Circuit-breaker
// for SDK-warm pools)
//
// diagnosis: a sidecar preConnect pod did not launch its runtime process at
// warm-up, did not serve a session on that process, did not serve a session
// whose workspace forced a demotion (DemoteSDK must end the process and a
// step-7 launch serve the session), or a pod created while the pool's
// SDK-warm circuit breaker was open launched at warm-up instead of at the
// session's step 7. A step-7 launch that reports the probe's dial delay
// slept a delay sized for the warm-up timeout inside the launching start's
// accept bound; the probe must take it only on the pod's warm-up launch.
func TestSidecarPreconnectPodsServeWarmDemotedAndPodWarmSessions_spec_6_1(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 90 * time.Second})
	c := d.Cluster()
	warm := supervisedpod.WaitReadyPods(t, c, supervisedpod.PreconnectPool, 2, 4*time.Minute)
	warmPID := map[string]int{}
	for _, p := range warm {
		probes := supervisedpod.ServingProbes(supervisedpod.Processes(t, c, p.Name))
		if len(probes) != 1 {
			t.Fatalf("SDK-warm pod %s runs %d runtime processes at warm-up, want 1", p.Name, len(probes))
		}
		warmPID[p.Name] = probes[0].PID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	t.Run("warm session on the warm-up process", func(t *testing.T) {
		tenant, sess := supStart(ctx, t, d, supervisedpod.PreconnectRuntime, nil)
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
		rec := supervisedpod.Report(ctx, t, d, tenant, sess.ID)
		if pid, ok := warmPID[sess.PodAssignment]; ok && rec.PID != pid {
			t.Errorf("SDK-warm pod %s served the session on PID %d, want the warm-up process %d", sess.PodAssignment, rec.PID, pid)
		}
		if want := supervisedpod.DialDelay.String(); rec.DialDelay != want {
			t.Errorf("the warm-up process on pod %s reported dial delay %q, want %q", sess.PodAssignment, rec.DialDelay, want)
		}
	})
	t.Run("demoted session on a step-7 launch", func(t *testing.T) {
		tenant, sess := supStart(ctx, t, d, supervisedpod.PreconnectRuntime, inlineWorkspacePlan("CLAUDE.md", "demote"))
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
		rec := supervisedpod.Report(ctx, t, d, tenant, sess.ID)
		if pid, ok := warmPID[sess.PodAssignment]; ok && rec.PID == pid {
			t.Errorf("demoted pod %s served the session on its warm-up process %d; DemoteSDK must end it", sess.PodAssignment, pid)
		}
		if rec.DialDelay != "" {
			t.Errorf("the step-7 launch on demoted pod %s slept dial delay %s; only the warm-up launch takes it",
				sess.PodAssignment, rec.DialDelay)
		}
	})
	t.Run("pod created while the breaker is open launches at step 7", func(t *testing.T) {
		podWarm := supervisedpod.WaitReadyPods(t, c, supervisedpod.PodWarmPool, 1, 3*time.Minute)
		for _, p := range podWarm {
			if n := len(supervisedpod.ServingProbes(supervisedpod.Processes(t, c, p.Name))); n != 0 {
				t.Errorf("pod-warm pod %s runs %d runtime processes before a session, want none", p.Name, n)
			}
		}
		tenant, sess := supStart(ctx, t, d, supervisedpod.PodWarmRuntime, nil)
		t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
		if rec := supervisedpod.Report(ctx, t, d, tenant, sess.ID); !rec.ManifestAtStart.Present {
			t.Errorf("the step-7 launch on pod-warm pod %s read no manifest at start", sess.PodAssignment)
		}
	})
}

// spec: §4.7.10 (Runtime supervisor, Launch), §6.1 (Adapter SIGTERM behavior
// during sdk_connecting)
//
// diagnosis: a preConnect or service-mode pod became Ready before its
// warm-up process connected on CH-MSGSOCK (the warm-up READY gate does not
// hold), or a preConnect pod deleted during its warm-up launch did not end
// the launched process through its signal path before the adapter exited:
// the process logged no SIGTERM or more than one (ExitOnSignal sends one
// `end`), the adapter exited non-zero or was killed at the grace period, or
// no adapter termination was observed at all.
func TestWarmUpLaunchGatesReadyAndEndsOnSIGTERM_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	t.Run("Ready only after the delayed dial connects", func(t *testing.T) {
		for _, pool := range []string{supervisedpod.PreconnectPool, supervisedpod.ServicePool} {
			for _, p := range supervisedpod.WaitReadyPods(t, c, pool, 1, 4*time.Minute) {
				rt := p.Container["runtime"]
				if rt.StartedAt.IsZero() || p.ReadyAt.IsZero() {
					t.Fatalf("pod %s carries no runtime start or Ready time: %s", p.Name, p.Describe())
				}
				if gap := p.ReadyAt.Sub(rt.StartedAt); gap < supervisedpod.DialDelay-2*time.Second {
					t.Errorf("pod %s became Ready %s after its runtime container started, before the probe's %s "+
						"dial delay ended", p.Name, gap, supervisedpod.DialDelay)
				}
			}
		}
	})
	t.Run("SIGTERM during the warm-up launch ends the process once and exits 0", func(t *testing.T) {
		pod := warmingPreconnectPod(t, c)
		logs := supervisedpod.StreamLogs(t, c, pod, "runtime")
		watch := supervisedpod.WatchPod(t, c, pod)
		supervisedpod.DeletePod(t, c, pod)
		last, gone := supervisedpod.WaitGone(t, c, pod, 2*time.Minute)
		if !gone {
			t.Fatalf("pod %s still exists 2m after its deletion: %s", pod, last.Describe())
		}
		requireOneEndThenAdapterExit0(t, pod, logs.Stop(), watch.States())
	})
}

// requireOneEndThenAdapterExit0 asserts the §6.1 SIGTERM-during-sdk_connecting
// outcome on a supervised pod: the launched process received exactly one
// SIGTERM, the signal the supervisor's `end` sends, and the adapter was
// observed to terminate with exit code 0. A pod whose adapter the kubelet
// killed at the grace period exits 137, and one whose process was never
// ended logs no SIGTERM, so both fail here. spec: §6.1 (Adapter SIGTERM
// behavior during sdk_connecting); §4.7.10 (Runtime supervisor, End).
func requireOneEndThenAdapterExit0(t *testing.T, pod, runtimeLog string, states []supervisedpod.PodState) {
	t.Helper()
	if n := strings.Count(runtimeLog, superviseprobe.SignalLogPrefix+" terminated"); n != 1 {
		t.Errorf("pod %s: the warm-up process logged %d SIGTERMs, want exactly the one ExitOnSignal's end sends:\n%s",
			pod, n, runtimeLog)
	}
	a, seen := supervisedpod.FirstTerminated(states, "adapter")
	if !seen {
		t.Fatalf("pod %s: no adapter termination was observed across %d pod updates", pod, len(states))
	}
	if a.ExitCode != 0 {
		t.Errorf("pod %s: the adapter exited %d (%s) after SIGTERM during the warm-up launch, want 0 after "+
			"ExitOnSignal's end", pod, a.ExitCode, a.Reason)
	}
}

// warmingPreconnectPod returns a preConnect pod whose warm-up process is
// launched and still inside its dial delay. It deletes one Ready pod so the
// controller creates a replacement and waits for the replacement's launch.
func warmingPreconnectPod(t *testing.T, c *kind.Cluster) string {
	t.Helper()
	ready := supervisedpod.WaitReadyPods(t, c, supervisedpod.PreconnectPool, 1, 4*time.Minute)
	known := map[string]bool{}
	for _, p := range supervisedpod.PoolPods(t, c, supervisedpod.PreconnectPool) {
		known[p.Name] = true
	}
	supervisedpod.DeletePod(t, c, ready[0].Name)
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		for _, p := range supervisedpod.PoolPods(t, c, supervisedpod.PreconnectPool) {
			if known[p.Name] || p.Ready || p.Deleting || !p.Container["runtime"].Running {
				continue
			}
			out, err := supervisedpod.ExecRuntime(t, c, p.Name, superviseprobe.SubProcesses)
			if err == nil && strings.Contains(out, `"cmdline":["`+supervisedpod.ProbeExe+`"]`) {
				return p.Name
			}
		}
		time.Sleep(time.Second)
	}
	t.Skip("not-yet-applicable: no replacement preConnect pod reached its warm-up launch within 2m")
	return ""
}

// spec: §4.7.10 (Runtime supervisor, Liveness; Runtime exits), §6.2
//
// diagnosis: on a supervised service-mode pod, the runtime process's death
// while the pod was idle did not make the adapter exit non-zero (a service
// pod has no claim and no boundary report, so the adapter's exit is the only
// channel that retires it), the pod was not deleted through the Exit
// channel, or no replacement pod became Ready.
func TestServicePodRetiresWhenItsRuntimeProcessDies_spec_4_7_10(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := supervisedpod.WaitReadyPods(t, c, supervisedpod.ServicePool, 1, 4*time.Minute)[0]
	watch := supervisedpod.WatchPod(t, c, pod.Name)
	out, err := supervisedpod.ExecRuntime(t, c, pod.Name, superviseprobe.SubKillRuntime)
	if err != nil || !strings.Contains(out, `"ok":true`) {
		t.Fatalf("kill the runtime process of %s: %v\n%s", pod.Name, err, out)
	}
	last, gone := supervisedpod.WaitGone(t, c, pod.Name, 4*time.Minute)
	if !gone {
		t.Fatalf("service pod %s was not deleted within 4m after its runtime process died: %s", pod.Name, last.Describe())
	}
	a, seen := supervisedpod.FirstTerminated(watch.States(), "adapter")
	if !seen {
		t.Fatalf("service pod %s: no adapter termination was observed before the pod was deleted", pod.Name)
	}
	if a.ExitCode == 0 {
		t.Errorf("service pod %s: the adapter exited 0 after its runtime process died, want non-zero", pod.Name)
	}
	supervisedpod.WaitReadyPods(t, c, supervisedpod.ServicePool, 1, 4*time.Minute)
}

// spec: §5.2 (Supervised scrub sequence), §4.7.9 (startup sequence)
//
// diagnosis: a process a setup command detached from its process group, or
// a System V segment a setup command created, survived in the adapter
// container into the next session on a supervised pod, so steps 1 and 1b of
// the supervised scrub did not reach the adapter container. A failure
// before the termination means the setup command never left the residue
// (the e2e adapter image lacks lenny-setup-probe, or the keep runtime does
// not exec setup commands as argv).
func TestSetupCommandResidueIsGoneBeforeTheNextSession_spec_5_2(t *testing.T) {
	t.Run("keep pod", func(t *testing.T) {
		requireSetupResidueGoneOnReuse(t, supervisedpod.KeepPool, supervisedpod.KeepRuntime)
	})
	t.Run("restart pod", func(t *testing.T) {
		t.Skip("blocked: pool admission refuses recycle.runtimeProcess restart until proposal 0084 rebinds the " +
			"intra-pod MCP servers per runtime generation, so no restart pool exists on the Kind cluster")
		requireSetupResidueGoneOnReuse(t, supervisedpod.RestartPool, supervisedpod.RestartRuntime)
	})
}

// requireSetupResidueGoneOnReuse runs a session whose setup command leaves a
// detached daemon and a System V segment in the adapter container of a pod
// of pool, and asserts both are gone after the scrub and in the next
// session the reused pod serves. spec: §5.2 (Supervised scrub sequence).
func requireSetupResidueGoneOnReuse(t *testing.T, pool, runtime string) {
	t.Helper()
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 90 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, pool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	key := int(time.Now().UnixNano()%(1<<30)) + 1

	tenant, sessA := supStart(ctx, t, d, runtime, supervisedpod.SetupResiduePlan(key))
	pod := sessA.PodAssignment
	if r := supervisedpod.AdapterSetupResidue(t, c, pod, key); len(r.DaemonPIDs) == 0 || !r.ShmExists {
		t.Fatalf("the setup command left no residue in the adapter container of %s: %+v", pod, r)
	}
	if err := d.Terminate(ctx, tenant, sessA.ID); err != nil {
		t.Fatalf("terminate session A: %v", err)
	}
	waitClaimPhase(t, c, pod, "reserved", 2*time.Minute)
	requireNoSetupResidue(t, c, pod, key, "after the scrub")

	sessB := supStartFor(ctx, t, d, tenant, runtime, nil)
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sessB.ID) })
	if sessB.PodAssignment != pod {
		t.Fatalf("session B landed on %s, want the reused pod %s", sessB.PodAssignment, pod)
	}
	requireNoSetupResidue(t, c, pod, key, "in the next session")
}

// requireNoSetupResidue asserts that no setup-command daemon runs in pod's
// adapter container and that the segment under key is gone.
func requireNoSetupResidue(t *testing.T, c *kind.Cluster, pod string, key int, when string) {
	t.Helper()
	r := supervisedpod.AdapterSetupResidue(t, c, pod, key)
	if len(r.DaemonPIDs) != 0 {
		t.Errorf("pod %s %s: setup-command daemons %v still run in the adapter container", pod, when, r.DaemonPIDs)
	}
	if r.ShmExists {
		t.Errorf("pod %s %s: the System V segment under key %d a setup command created still exists", pod, when, key)
	}
}

// spec: §24.2, §4.7.11 item 8 (Runtime supervisor, Property self-test)
//
// diagnosis: `lenny-ctl preflight supervisor` on the Kind agent namespace
// under runc did not report every self-test property, reported a gating
// property failing on runc, or left its self-test pod behind.
func TestPreflightSupervisorReportsEachPropertyAndLeavesNoPod_spec_24_2(t *testing.T) {
	c := kind.InstallLenny(t)
	bin := filepath.Join(t.TempDir(), "lenny-ctl")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/lenny-ctl").CombinedOutput(); err != nil {
		t.Fatalf("build lenny-ctl: %v\n%s", err, out)
	}
	values := filepath.Join(t.TempDir(), "values.yaml")
	body := "agentNamespaces:\n  - name: " + supervisedpod.Namespace + "\n" +
		"security:\n  podUIDs:\n    adapter: 65532\n    agent: 65533\n    credReadersGID: 65534\n" +
		"controller:\n  supervisorImage: lenny-supervisor:e2e\n"
	if err := os.WriteFile(values, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "preflight", "supervisor", "--runtime-class", "runc", "--config", values, "--wait-timeout", "2m")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+c.KubeconfigPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("lenny-ctl preflight supervisor: %v\n%s", err, out)
	}
	for _, prop := range []string{"nondumpable", "killReach", "initSigkillImmune", "initSigstopImmune"} {
		if !strings.Contains(string(out), prop) {
			t.Errorf("preflight output names no %s property:\n%s", prop, out)
		}
	}
	// The CLI deletes its pod before it returns; the pod object can stay
	// for its termination grace, so absence is polled rather than read once.
	deadline := time.Now().Add(90 * time.Second)
	for {
		left, err := c.KubectlOut(t, "-n", supervisedpod.Namespace, "get", "pods", "-o", "name")
		if err != nil {
			t.Fatalf("list pods: %v\n%s", err, left)
		}
		if !strings.Contains(left, "lenny-supervisor-preflight-") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("preflight left its self-test pod 90s after it returned:\n%s", left)
		}
		time.Sleep(2 * time.Second)
	}
}
