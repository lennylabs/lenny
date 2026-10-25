// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
	"github.com/lennylabs/lenny/tests/testinfra/sessiondriver"
	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// PlantAgentResidue makes a session's probe leave the agent residue the
// boundary end and sweep of a `restart` pod remove: a double-forked daemon,
// System V and POSIX IPC objects, and a symbolic link under the workspace
// base. It returns the residue for a later session's Residue check.
// spec: §4.7.10 (Runtime supervisor, End; Sweep).
func PlantAgentResidue(ctx context.Context, t testing.TB, d *sessiondriver.Driver, tenant, session string) superviseprobe.Residue {
	t.Helper()
	var res superviseprobe.Residue
	for _, cmd := range []string{superviseprobe.CmdDaemon, superviseprobe.CmdIPC, superviseprobe.CmdSymlink} {
		r := Action(ctx, t, d, tenant, session, cmd)
		if !r.OK {
			t.Fatalf("session %s: probe %s failed: %s", session, cmd, r.Error)
		}
		res = mergeResidue(res, r.Residue)
	}
	return res
}

// mergeResidue fills each unset field of acc from add.
func mergeResidue(acc, add superviseprobe.Residue) superviseprobe.Residue {
	if acc.DaemonPID == 0 {
		acc.DaemonPID = add.DaemonPID
	}
	if acc.ShmID == nil {
		acc.ShmID = add.ShmID
	}
	if acc.MsqID == nil {
		acc.MsqID = add.MsqID
	}
	if acc.MQ == "" {
		acc.MQ = add.MQ
	}
	if acc.Link == "" {
		acc.Link = add.Link
	}
	return acc
}

// ResidueLeft names each object a ResidueResult reports as still existing.
func ResidueLeft(r superviseprobe.ResidueResult) []string {
	var left []string
	for _, o := range []struct {
		name   string
		exists bool
	}{
		{"daemon", r.DaemonAlive},
		{"System V shared-memory segment", r.ShmExists},
		{"System V message queue", r.MsqExists},
		{"POSIX message queue", r.MQExists},
		{"planted link", r.LinkExists},
	} {
		if o.exists {
			left = append(left, o.name)
		}
	}
	return left
}

// ProbeDaemons returns the live probe daemons of a process list.
func ProbeDaemons(procs []superviseprobe.Process) []superviseprobe.Process {
	var out []superviseprobe.Process
	for _, p := range procs {
		if superviseprobe.IsProbeDaemon(p, ProbeExe) {
			out = append(out, p)
		}
	}
	return out
}

// NextSessionOnPod terminates first, waits for its pod to be reserved for
// tenant after the whole-pod scrub, and starts tenant's next session on
// runtime, failing the test unless the next session lands on the same pod.
// The gateway holds the pod for its tenant only on a scrub report that the
// runtime can serve the next session, so the reserved state is the
// runtime-live report. spec: §5.2 (Recycle lifecycle; Supervised scrub
// sequence).
func NextSessionOnPod(ctx context.Context, t testing.TB, d *sessiondriver.Driver, c *kind.Cluster, tenant, runtime string, first *sessiondriver.Session) *sessiondriver.Session {
	t.Helper()
	if err := d.Terminate(ctx, tenant, first.ID); err != nil {
		t.Fatalf("terminate session %s: %v", first.ID, err)
	}
	WaitClaimPhase(t, c, first.PodAssignment, "reserved", 2*time.Minute)
	next, err := d.CreateAndStart(ctx, tenant, runtime)
	if err != nil {
		t.Fatalf("start the next session on %s: %v", runtime, err)
	}
	t.Cleanup(func() { _ = d.Terminate(context.Background(), tenant, next.ID) })
	if next.PodAssignment != first.PodAssignment {
		t.Fatalf("the next session landed on %s, want the recycled pod %s", next.PodAssignment, first.PodAssignment)
	}
	return next
}
