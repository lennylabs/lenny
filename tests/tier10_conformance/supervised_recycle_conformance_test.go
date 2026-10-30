// SPDX-License-Identifier: MIT

//go:build conformance

// Tier-10 conformance cases for the recycle property on supervised pods. The
// in-process cases in recycle_scrub_conformance_test.go drive the adapter
// Server against a runtime double; these run the property against the Kind
// cluster's supervised keep pool (tests/testinfra/kind/install.sh), where the
// runtime process is launched by the runtime supervisor and the whole-pod
// scrub runs the deployer's cleanupCommands through it:
//
//   - Runtime process kept: the recycled pod serves the next session on the
//     runtime process that served the first.
//   - Replacement session served: the next session lands on the recycled pod.
//   - Runtime live reported: the pod is held for its tenant after the scrub,
//     which the gateway does only on a report that the runtime can serve the
//     next session.
//
// The restart-pod run and the §15.4.6 shutdown case are written against the
// cluster and skip with a blocked reason first: no restart pool exists while
// gateway pool admission refuses recycle.runtimeProcess: restart, and the
// adapter writes the CH-MSGSOCK shutdown frame at pod drain only once R16
// lands.

package tier10_conformance_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
	"github.com/lennylabs/lenny/tests/testinfra/supervisedpod"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// spec: §4.7.10 (Runtime supervisor; Runtime process lifetime), §5.2
// (Supervised scrub sequence; Runtime-process lifetime)
//
// diagnosis: the recycle property does not hold on a supervised keep pod:
// the pod was not held for its tenant after the scrub (the scrub reported
// the runtime not live), the next session was not placed on it, or the next
// session ran on a runtime process other than the one that served the
// first.
func TestRecyclePropertyOnASupervisedKeepPod_spec_5_2(t *testing.T) {
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant := supervisedpod.NewTenant(ctx, t, d, "supervised-recycle")
	first, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start the first session: %v", err)
	}
	firstRec := supervisedpod.Report(ctx, t, d, tenant, first.ID)
	if err := d.Terminate(ctx, tenant, first.ID); err != nil {
		t.Fatalf("terminate the first session: %v", err)
	}
	supervisedpod.WaitClaimPhase(t, c, first.PodAssignment, "reserved", 2*time.Minute)

	next, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start the replacement session: %v", err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, next.ID) })
	if next.PodAssignment != first.PodAssignment {
		t.Fatalf("the replacement session landed on %s, want the recycled pod %s", next.PodAssignment, first.PodAssignment)
	}
	if rec := supervisedpod.Report(ctx, t, d, tenant, next.ID); rec.Token != firstRec.Token {
		t.Errorf("the recycled keep pod served the replacement session on process %s, want the kept process %s",
			rec.Token, firstRec.Token)
	}
}

// spec: §4.7.10 (Runtime supervisor), §5.2 (Supervised scrub sequence;
// Runtime-process lifetime)
//
// diagnosis: the recycle property does not hold on a supervised restart
// pod: the next session ran on the previous session's runtime process, or
// the pod was not held after an attested end and sweep.
func TestRecyclePropertyOnASupervisedRestartPod_spec_5_2(t *testing.T) {
	t.Skip("blocked: pool admission refuses recycle.runtimeProcess restart until proposal 0084 rebinds the " +
		"intra-pod MCP servers per runtime generation, so no restart pool exists on the Kind cluster")
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.RestartPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant := supervisedpod.NewTenant(ctx, t, d, "supervised-recycle-restart")
	first, err := d.CreateAndStart(ctx, tenant, supervisedpod.RestartRuntime)
	if err != nil {
		t.Fatalf("start the first session: %v", err)
	}
	firstRec := supervisedpod.Report(ctx, t, d, tenant, first.ID)
	next := supervisedpod.NextSessionOnPod(ctx, t, d, c, tenant, supervisedpod.RestartRuntime, first)
	if rec := supervisedpod.Report(ctx, t, d, tenant, next.ID); rec.Token == firstRec.Token {
		t.Errorf("the recycled restart pod served the replacement session on the first session's process %s, "+
			"want a process launched at the replacement's step 7", firstRec.Token)
	}
}

// spec: §15.4.6 (Basic shutdown within deadline_ms), §4.7.10 (Runtime
// supervisor, Runtime exits), §28.5.3 (CH-MSGSOCK, Timing)
//
// diagnosis: on a supervised keep pod with a held session, a runtime that
// honors the CH-MSGSOCK shutdown frame did not exit within deadline_ms (the
// probe logged the SIGTERM of the deadline end), or its exit was not
// classified by the shutdown row of the Runtime exits table: the held
// session failed with runtime_crash, or the adapter logged runtime_crash for
// a requested exit.
func TestShutdownWithinDeadlineOnASupervisedKeepPod_spec_15_4_6(t *testing.T) {
	t.Skip("blocked: the adapter writes the CH-MSGSOCK shutdown frame at pod drain only once R16 lands; " +
		"until then the tier-1 Runtime exits cases cover the shutdown row's classification")
	d := sessiondriver.New(t, sessiondriver.Options{HTTPTimeout: 60 * time.Second})
	c := d.Cluster()
	supervisedpod.WaitReadyPods(t, c, supervisedpod.KeepPool, 1, 3*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tenant := supervisedpod.NewTenant(ctx, t, d, "supervised-shutdown")
	held, err := d.CreateAndStart(ctx, tenant, supervisedpod.KeepRuntime)
	if err != nil {
		t.Fatalf("start the held session: %v", err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, held.ID) })
	pod := held.PodAssignment
	runtimeLog := supervisedpod.StreamLogs(t, c, pod, "runtime")
	adapterLog := supervisedpod.StreamLogs(t, c, pod, "adapter")
	supervisedpod.DeletePod(t, c, pod)
	if last, gone := supervisedpod.WaitGone(t, c, pod, 2*time.Minute); !gone {
		t.Fatalf("pod %s still exists 2m after its deletion: %s", pod, last.Describe())
	}
	requireShutdownHonoredWithinDeadline(t, pod, runtimeLog.Stop(), adapterLog.Stop())
	if s, err := d.GetSession(ctx, tenant, held.ID); err != nil {
		t.Errorf("read the held session %s after the drain: %v", held.ID, err)
	} else if s.FailureClass == "runtime_crash" {
		t.Errorf("held session %s failed with runtime_crash; the shutdown row of Runtime exits fails no session", held.ID)
	}
}

// requireShutdownHonoredWithinDeadline asserts, from the logs of a drained
// supervised pod, that the runtime process exited by itself inside
// deadline_ms rather than through the deadline `end`, whose SIGTERM the
// probe logs, and that the adapter classified the exit under the shutdown
// row rather than as a crash. spec: §4.7.10 (Runtime supervisor, Runtime
// exits); §28.5.3 (CH-MSGSOCK, Timing).
func requireShutdownHonoredWithinDeadline(t *testing.T, pod, runtimeLog, adapterLog string) {
	t.Helper()
	if strings.Contains(runtimeLog, superviseprobe.SignalLogPrefix+" terminated") {
		t.Errorf("pod %s: the runtime process received the deadline end's SIGTERM, so it did not exit within "+
			"deadline_ms of the shutdown frame:\n%s", pod, runtimeLog)
	}
	if strings.Contains(adapterLog, "runtime_crash") {
		t.Errorf("pod %s: the adapter logged runtime_crash for an exit the shutdown frame requested:\n%s", pod, adapterLog)
	}
}
