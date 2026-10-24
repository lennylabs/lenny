// SPDX-License-Identifier: MIT

package supervisedpod

import (
	"reflect"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/superviseprobe"
)

// spec: §4.7.10 (Runtime supervisor, End; Sweep), §5.2 (Supervised scrub
// sequence)
//
// TestResidueHelpersNameWhatSurvivedTheBoundary pins the reading the
// restart-pod cases assert on: the merged residue of the planting actions
// keeps each object one action reported, every surviving object is named,
// and only a live daemon grandchild counts as a cleanup command's
// background process.
func TestResidueHelpersNameWhatSurvivedTheBoundary(t *testing.T) {
	shm, msq := 0, 3
	merged := mergeResidue(mergeResidue(mergeResidue(superviseprobe.Residue{},
		superviseprobe.Residue{DaemonPID: 41}),
		superviseprobe.Residue{ShmID: &shm, MsqID: &msq, MQ: "mq"}),
		superviseprobe.Residue{Link: "/workspace/l"})
	want := superviseprobe.Residue{DaemonPID: 41, ShmID: &shm, MsqID: &msq, MQ: "mq", Link: "/workspace/l"}
	if !reflect.DeepEqual(merged, want) {
		t.Errorf("merged residue %+v, want %+v", merged, want)
	}
	if left := ResidueLeft(superviseprobe.ResidueResult{}); len(left) != 0 {
		t.Errorf("a clean residue result names %v", left)
	}
	all := superviseprobe.ResidueResult{DaemonAlive: true, ShmExists: true, MsqExists: true, MQExists: true, LinkExists: true}
	if left := ResidueLeft(all); len(left) != 5 {
		t.Errorf("a residue result with every object present names %v, want all five", left)
	}
	procs := []superviseprobe.Process{
		{PID: 1, Cmdline: []string{"/lenny-supervisor/lenny-supervisor", "run"}, State: "S"},
		{PID: 7, Cmdline: []string{ProbeExe}, State: "S"},
		{PID: 9, Cmdline: []string{ProbeExe, "daemon-grandchild"}, State: "S"},
		{PID: 10, Cmdline: []string{ProbeExe, "daemon-grandchild"}, State: "Z"},
	}
	if d := ProbeDaemons(procs); len(d) != 1 || d[0].PID != 9 {
		t.Errorf("ProbeDaemons = %+v, want only the live grandchild PID 9", d)
	}
}
