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
// The restart-pod run and the §15.4.6 shutdown case skip with their reasons.

package tier10_conformance_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
	"github.com/lennylabs/lenny/tests/testinfra/supervisedpod"
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
	t.Skip(supervisedpod.RestartGateReason)
}

// spec: §15.4.6 (Basic shutdown within deadline_ms), §4.7.10 (Runtime
// supervisor, Runtime exits), §28.5.3 (CH-MSGSOCK, Timing)
//
// diagnosis: on a supervised keep pod with a held session, a runtime that
// honors the CH-MSGSOCK shutdown frame did not exit within deadline_ms, or
// its exit was not classified by the shutdown row of the Runtime exits table
// (a requested exit, not a crash).
func TestShutdownWithinDeadlineOnASupervisedKeepPod_spec_15_4_6(t *testing.T) {
	t.Skip("blocked: the adapter writes the CH-MSGSOCK shutdown frame at pod drain only once R16 lands; " +
		"until then the tier-1 Runtime exits cases cover the shutdown row's classification")
}
