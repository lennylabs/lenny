// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for the runtime supervisor on the Kind cluster's
// supervised probe pools (tests/testinfra/kind/install.sh). The attacks run
// in a supervised pod's runtime container at the agent UID through the
// probe's attack subcommands (tests/testinfra/superviseprobe); the admission
// cases send a modified copy of a live supervised pod to the API server as a
// server-side dry run, so the webhooks judge it and nothing is created.

package tier9_security_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
	"github.com/lennylabs/lenny/tests/testinfra/supervisedpod"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// spec: §4.7.11 item 8 (Runtime supervisor), §13.1 (Runtime supervisor
// volume)
//
// diagnosis: from the agent UID, a ptrace attach to the supervisor, a read of
// its memory or its descriptor table, or a write under the supervisor mount
// succeeded. The supervisor must be non-dumpable and its binary must sit on
// a volume the runtime container mounts read-only, or the agent can take
// over the process that ends its runtime at each boundary.
func TestAgentUIDCannotTraceReadOrRewriteTheSupervisor_spec_4_7_11(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)[0]
	for _, attack := range []string{
		superviseprobe.AttackPtracePID1,
		superviseprobe.AttackReadProc1Mem,
		superviseprobe.AttackReadProc1FD,
		superviseprobe.AttackWriteSupervisor,
	} {
		if r := supervisedpod.Attack(t, c, pod.Name, attack); r.OK {
			t.Errorf("pod %s: %s succeeded from the agent UID (%+v)", pod.Name, attack, r)
		}
	}
}

// catchableSignals are the signals the agent UID sends to PID 1: every
// signal other than SIGKILL and SIGSTOP, SIGCHLD, the fault signals, and the
// real-time signals included.
func catchableSignals() []int {
	var out []int
	for s := 1; s <= 64; s++ {
		if s == 9 || s == 19 {
			// SIGKILL and SIGSTOP are the tier-8 cases.
			continue
		}
		out = append(out, s)
	}
	return out
}

// spec: §4.7.10 (Runtime supervisor, Signals; Liveness), §5.2 (Supervised
// scrub sequence; Recycle lifecycle)
//
// diagnosis: a catchable signal the agent UID sent to the supervisor (PID 1)
// stopped it answering ping, stopped it outright, retired the pod, or reached
// the runtime process. The supervisor acts on no signal's sender and forwards
// nothing, so the probe that serves the session must count no signal. The
// probe registers every catchable signal, the fault signals, SIGPIPE, and
// (with asynchronous preemption off) SIGURG included, and it exits on SIGTERM,
// so a forwarded SIGTERM shows as a changed process token. On a
// claimed keep pod a missed pong takes the Claim channel, which leaves the
// pod Ready and running until the release, so the case reads the outcomes a
// missed pong changes: the adapter's link-failure and not-live records, the
// supervisor's process state, and the boundary, where a not-live pod retires
// instead of reaching reserved and serving the tenant's next session.
func TestSignalsToTheSupervisorReachNeitherItNorTheRuntime_spec_4_7_10(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	tenant := supervisedpod.NewTenant(ctx, t, d, "supervisor-signals")
	sess, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start a session: %v", err)
	}
	// The boundary check terminates the session itself; this cleanup only
	// covers a failure before it, and its error is ignored.
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
	pod := sess.PodAssignment
	before := supervisedpod.Report(ctx, t, d, tenant, sess.ID)
	if len(before.Uncounted) != 0 {
		t.Fatalf("the keep pool's probe does not count %v, so a forwarded copy would go unseen; its Runtime env "+
			"must set GODEBUG=asyncpreemptoff=1", before.Uncounted)
	}

	for _, s := range catchableSignals() {
		r := supervisedpod.Attack(t, c, pod, superviseprobe.AttackSignalPID1, strconv.Itoa(s))
		if !r.OK {
			t.Logf("signal %d to PID 1 refused by the kernel: %s", s, r.Errno)
		}
	}
	// Longer than the rendered ping interval plus timeout, so a supervisor
	// a signal stopped or wedged has missed a pong by now.
	time.Sleep(10 * time.Second)

	requireSupervisorStillLive(t, c, pod)
	after := supervisedpod.Report(ctx, t, d, tenant, sess.ID)
	if after.Token != before.Token {
		t.Errorf("the runtime process changed from %s to %s after the signals", before.Token, after.Token)
	}
	if len(after.Signals) != 0 {
		t.Errorf("the runtime process received signals sent to the supervisor: %v", after.Signals)
	}
	requireBoundaryKeepsThePod(ctx, t, d, c, tenant, sess, before.Token)
}

// requireSupervisorStillLive asserts what a missed pong on a claimed keep
// pod would change while the pod is still claimed: the supervisor (PID 1) is
// running and not stopped, and the adapter recorded no CH-SUPERVISE link
// failure and no not-live cause. spec: §4.7.10 (Runtime supervisor,
// Liveness).
func requireSupervisorStillLive(t *testing.T, c *kind.Cluster, pod string) {
	t.Helper()
	st, found := supervisedpod.GetPod(t, c, pod)
	if !found || st.Deleting || !st.Container["adapter"].Running || !st.Container["runtime"].Running {
		t.Fatalf("pod %s did not survive the signals to its supervisor: found=%v %s", pod, found, st.Describe())
	}
	var pid1 *superviseprobe.Process
	procs := supervisedpod.Processes(t, c, pod)
	for i := range procs {
		if procs[i].PID == 1 {
			pid1 = &procs[i]
		}
	}
	if pid1 == nil || pid1.State == "T" || pid1.State == "t" || pid1.State == "Z" {
		t.Errorf("pod %s: the supervisor (PID 1) is stopped or gone after the signals: %+v", pod, pid1)
	}
	for _, event := range []string{adapter.LogSuperviseLinkFailure, adapter.LogRuntimeNotLive} {
		if at := supervisedpod.AdapterEventTimes(t, c, pod, event); len(at) != 0 {
			t.Errorf("pod %s: the adapter logged %s at %v after the signals to the supervisor", pod, event, at)
		}
	}
}

// requireBoundaryKeepsThePod ends the session and asserts the pod passed its
// boundary with a live runtime: the claim reaches reserved, which needs a
// scrub report with the runtime live, and the tenant's next session lands on
// the same pod and the same runtime process. A pod whose supervisor missed a
// pong is not live, so it retires at the release instead. spec: §5.2
// (Supervised scrub sequence; Recycle lifecycle); §4.7.10 (Runtime
// supervisor, Liveness).
func requireBoundaryKeepsThePod(ctx context.Context, t *testing.T, d *sessiondriver.Driver, c *kind.Cluster,
	tenant string, sess *sessiondriver.Session, token string,
) {
	t.Helper()
	if err := d.Terminate(ctx, tenant, sess.ID); err != nil {
		t.Fatalf("terminate session %s: %v", sess.ID, err)
	}
	supervisedpod.WaitClaimPhase(t, c, sess.PodAssignment, "reserved", 2*time.Minute)
	next, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start the tenant's next session: %v", err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, next.ID) })
	if next.PodAssignment != sess.PodAssignment {
		t.Fatalf("the tenant's next session landed on %s, want the reserved pod %s", next.PodAssignment, sess.PodAssignment)
	}
	if rec := supervisedpod.Report(ctx, t, d, tenant, next.ID); rec.Token != token {
		t.Errorf("pod %s served the next session on runtime process %s, want the kept process %s",
			sess.PodAssignment, rec.Token, token)
	}
}

// spec: §28.5.3 (CH-SUPERVISE, Endpoint)
//
// diagnosis: a process at the agent UID that bound the CH-SUPERVISE socket
// name after the adapter accepted the supervisor received a connection. The
// adapter accepts once and closes its listener, and the supervisor dials
// only at its start, so a squatter must receive nothing. The attack runs
// on a pod that has served no session, because a used pod's post-session
// work ends stray processes in the runtime container, the attack among
// them.
func TestSquattedSuperviseSocketReceivesNoConnection_spec_28_5_3(t *testing.T) {
	c := kind.InstallLenny(t)
	pod := supervisedpod.FreshIdlePods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)[0]
	r := supervisedpod.Attack(t, c, pod.Name, superviseprobe.AttackBindSupervise, "10")
	if r.Connections != 0 {
		t.Errorf("pod %s: a squatted @lenny-supervise received %d connections", pod.Name, r.Connections)
	}
}

// livePodSpec returns a live supervised pod rewritten as a creatable
// manifest: server-set metadata and status dropped, a fresh name, and the
// test label for the start-time sweep.
func livePodSpec(t *testing.T, c *kind.Cluster, pod string) map[string]any {
	t.Helper()
	out, err := c.KubectlOut(t, "-n", supervisedpod.Namespace, "get", "pod", pod, "-o", "json")
	if err != nil {
		t.Fatalf("read pod %s: %v\n%s", pod, err, out)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("decode pod %s: %v", pod, err)
	}
	delete(obj, "status")
	meta := obj["metadata"].(map[string]any)
	labels, _ := meta["labels"].(map[string]any)
	if labels == nil {
		labels = map[string]any{}
	}
	labels[supervisedpod.TestLabel] = "true"
	obj["metadata"] = map[string]any{
		"name":      fmt.Sprintf("supervisor-admission-%d", time.Now().UnixNano()),
		"namespace": supervisedpod.Namespace,
		"labels":    labels,
	}
	spec := obj["spec"].(map[string]any)
	delete(spec, "nodeName")
	return obj
}

// dryRunCreate sends obj to the API server as a server-side dry-run create
// and returns the combined output and error.
func dryRunCreate(t *testing.T, c *kind.Cluster, obj map[string]any) (string, error) {
	t.Helper()
	body, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pod.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return c.KubectlOut(t, "create", "--dry-run=server", "-f", path)
}

// supervisorVolumeMount is a read-write mount of the supervisor volume.
func supervisorVolumeMount() map[string]any {
	return map[string]any{"name": "lenny-supervisor", "mountPath": "/stage"}
}

// spec: §13.1 (Runtime supervisor volume; Container identity)
//
// diagnosis: the pod-security webhook admitted a supervised pod with a
// container injected at the agent UID, or with an init container other
// than the non-restartable lenny-supervisor-stage mounting the supervisor
// volume read-write. Either lets a process other than the stage step write
// the binary the runtime container runs as its first process, or run beside
// the runtime at its UID.
func TestWebhookRefusesInjectedContainersAndSupervisorVolumeWriters_spec_13_1(t *testing.T) {
	c := kind.InstallLenny(t)
	supervisedpod.SweepTestObjects(t, c)
	pod := supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)[0]
	agentUID := int64(65533)

	cases := []struct {
		name   string
		mutate func(spec map[string]any)
		want   string
	}{
		{
			name: "container injected at the agent UID",
			mutate: func(spec map[string]any) {
				spec["containers"] = append(spec["containers"].([]any), map[string]any{
					"name": "intruder", "image": "lenny-supervisor:e2e",
					"securityContext": map[string]any{"runAsUser": agentUID, "runAsNonRoot": true},
				})
			},
			want: "reserved",
		},
		{
			name: "restartable lenny-supervisor-stage mounting the volume read-write",
			mutate: func(spec map[string]any) {
				for _, ic := range spec["initContainers"].([]any) {
					m := ic.(map[string]any)
					if m["name"] == "lenny-supervisor-stage" {
						m["restartPolicy"] = "Always"
					}
				}
			},
			want: "runtime supervisor volume",
		},
		{
			name: "another init container mounting the volume read-write",
			mutate: func(spec map[string]any) {
				ics := spec["initContainers"].([]any)
				stage := ics[len(ics)-1].(map[string]any)
				other := map[string]any{}
				for k, v := range stage {
					other[k] = v
				}
				other["name"] = "rewriter"
				other["volumeMounts"] = []any{supervisorVolumeMount()}
				spec["initContainers"] = append(ics, other)
			},
			want: "runtime supervisor volume",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obj := livePodSpec(t, c, pod.Name)
			tc.mutate(obj["spec"].(map[string]any))
			out, err := dryRunCreate(t, c, obj)
			if err == nil {
				t.Fatalf("the API server admitted the pod:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the refusal does not name the %s rule:\n%s", tc.want, out)
			}
		})
	}
}

// spec: §17.2 (item 5, lenny-label-immutability), §5.2 (Runtime-process
// lifetime)
//
// diagnosis: a patch that relabeled a used keep pod's
// lenny.dev/runtime-process was admitted. A pod's lifetime is fixed when the
// pod is created, so a relabel would let a used keep pod pass as a restart
// pod at acquisition.
func TestLabelImmutabilityRefusesARuntimeProcessRelabel_spec_17_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tenant := supervisedpod.NewTenant(ctx, t, d, "supervisor-relabel")
	sess, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start a session: %v", err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, sess.ID) })
	out, err := c.KubectlOut(t, "-n", supervisedpod.Namespace, "label", "pod", sess.PodAssignment,
		"lenny.dev/runtime-process=restart", "--overwrite")
	if err == nil {
		t.Fatalf("relabel of used keep pod %s admitted:\n%s", sess.PodAssignment, out)
	}
	if st, _ := supervisedpod.GetPod(t, c, sess.PodAssignment); st.Labels["lenny.dev/runtime-process"] != "keep" {
		t.Errorf("pod %s lenny.dev/runtime-process = %q after the refused relabel, want keep",
			sess.PodAssignment, st.Labels["lenny.dev/runtime-process"])
	}
}

// spec: §5.2 (Supervised scrub sequence; Lenny scrub procedure), §4.7.10
// (Runtime supervisor, Cleanup)
//
// diagnosis: the whole-pod scrub followed a symbolic link the agent planted
// toward /run/lenny and removed the adapter's files there. The adapter's
// scrub file operations must resolve beneath their root with no symlink
// traversal.
func TestScrubDoesNotFollowAPlantedLink_spec_5_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tenant := supervisedpod.NewTenant(ctx, t, d, "supervisor-scrub-link")
	sess, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start a session: %v", err)
	}
	before := listRunLenny(t, c, sess.PodAssignment)
	if plant := supervisedpod.Action(ctx, t, d, tenant, sess.ID, superviseprobe.CmdSymlink); !plant.OK {
		t.Fatalf("plant a link: %s", plant.Error)
	}
	if err := d.Terminate(ctx, tenant, sess.ID); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	supervisedpod.WaitClaimPhase(t, c, sess.PodAssignment, "reserved", 2*time.Minute)
	after := listRunLenny(t, c, sess.PodAssignment)
	for _, name := range before {
		if !contains(after, name) {
			t.Errorf("/run/lenny/%s is gone after the scrub of a pod with a link planted toward /run/lenny", name)
		}
	}
}

// listRunLenny lists /run/lenny in pod's runtime container.
func listRunLenny(t *testing.T, c *kind.Cluster, pod string) []string {
	t.Helper()
	out, err := supervisedpod.ExecRuntime(t, c, pod, superviseprobe.SubList, superviseprobe.AdapterRunDir)
	if err != nil {
		t.Fatalf("list /run/lenny in %s: %v\n%s", pod, err, out)
	}
	var names []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &names); err != nil {
		t.Fatalf("/run/lenny in %s is unreadable from the runtime container: %s", pod, out)
	}
	return names
}

// contains reports whether list holds s.
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// spec: §4.7.10 (Runtime supervisor, Sweep), §5.2 (Supervised scrub sequence)
//
// diagnosis: on a restart pod, a daemon, IPC objects, or a planted link the
// agent left in one session survived the boundary end and sweep.
func TestRestartPodSweepRemovesAgentResidue_spec_4_7_10(t *testing.T) {
	t.Skip("blocked: pool admission refuses recycle.runtimeProcess restart until proposal 0084 rebinds the " +
		"intra-pod MCP servers per runtime generation, so no restart pool exists on the Kind cluster")
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.RestartPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tenant := supervisedpod.NewTenant(ctx, t, d, "supervisor-restart-sweep")
	first, err := d.CreateAndStart(ctx, tenant, supervisedpod.RestartRuntime)
	if err != nil {
		t.Fatalf("start a session on %s: %v", supervisedpod.RestartRuntime, err)
	}
	residue := supervisedpod.PlantAgentResidue(ctx, t, d, tenant, first.ID)
	next := supervisedpod.NextSessionOnPod(ctx, t, d, c, tenant, supervisedpod.RestartRuntime, first)
	if left := supervisedpod.ResidueLeft(supervisedpod.Residue(ctx, t, d, tenant, next.ID, residue)); len(left) != 0 {
		t.Errorf("restart pod %s: %v the agent left in one session survived the boundary end and sweep",
			first.PodAssignment, left)
	}
}
