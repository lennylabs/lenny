// SPDX-License-Identifier: MIT

//go:build load_local

package low_resource_startup

import (
	"runtime"
	"testing"
	"time"
)

// spec: §17.7.
// diagnosis: the scenario's scheduled-wait clock no longer converts the
// process's CPU time into per-P wait time, so the healthz bound is applied in
// the wrong units.
func TestScheduledWaitDividesProcessCPUAcrossPs(t *testing.T) {
	cases := []struct {
		cpu   time.Duration
		procs int
		want  time.Duration
	}{
		{cpu: 400 * time.Millisecond, procs: 8, want: 50 * time.Millisecond},
		{cpu: time.Second, procs: 4, want: 250 * time.Millisecond},
		{cpu: 30 * time.Millisecond, procs: 1, want: 30 * time.Millisecond},
		{cpu: 30 * time.Millisecond, procs: 0, want: 30 * time.Millisecond},
	}
	for _, c := range cases {
		if got := scheduledWait(c.cpu, c.procs); got != c.want {
			t.Errorf("scheduledWait(%v, %d) = %v, want %v", c.cpu, c.procs, got, c.want)
		}
	}
}

// The negative control for the host-load-independent clock: a healthz wait
// during which the burners hold every P must still read as a wait. The
// goroutine blocks for a full second while the burners run, which is how a
// healthz goroutine starved until the burners stop would look, and the
// scheduled wait must exceed the bound even when other processes on the host
// take most of its cores.
// spec: §17.7.
// diagnosis: the process CPU clock does not advance while the burners run,
// so the scenario would no longer detect a healthz goroutine starved behind
// them; check processCPUTime and scheduledWait.
func TestScheduledWaitCountsAWaitWhileTheBurnersRun(t *testing.T) {
	b := &burner{stop: make(chan struct{})}
	b.start()
	defer b.shutdown()

	cpuStart, err := processCPUTime()
	if err != nil {
		t.Fatalf("processCPUTime: %v", err)
	}
	time.Sleep(time.Second)
	cpuEnd, err := processCPUTime()
	if err != nil {
		t.Fatalf("processCPUTime: %v", err)
	}
	waited := scheduledWait(cpuEnd-cpuStart, runtime.GOMAXPROCS(0))
	if waited <= healthzBound {
		t.Errorf("a 1s wait with every P burning read as %v of scheduled wait, want more than the %v bound", waited, healthzBound)
	}
}
