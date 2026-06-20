// SPDX-License-Identifier: MIT

package stack

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// spec: §17.4 (Embedded Mode supervises the production gateway and
// controllers as managed child processes; the substrate is provisioned
// per host operating system), §24.19 (lenny up/down manage those
// processes). These tests exercise the cross-platform process-control
// substrate (process_unix.go, process_windows.go) through the
// consumer-facing managedProcess API so the same behavior is verified on
// every target OS.

// spawnSleeper starts a managed child that blocks until killed: it
// re-executes the test binary, which the TestMain dispatcher parks in a
// long sleep when LENNY_TEST_SLEEPER is set. The helper returns the
// started managedProcess.
func spawnSleeper(t *testing.T) *managedProcess {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test binary: %v", err)
	}
	mp, err := startProcess(processSpec{
		Name:    "sleeper",
		BinPath: self,
		Args:    []string{"-test.run=TestSubstrateNoop"},
		Env:     append(os.Environ(), "LENNY_TEST_SLEEPER=1"),
		LogPath: filepath.Join(t.TempDir(), "sleeper.log"),
	})
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}
	return mp
}

// TestSubstrateNoop is the re-executed child body. The real test binary
// runs it as a sleeper subprocess; in the normal test run it returns
// immediately.
func TestSubstrateNoop(t *testing.T) {
	if os.Getenv("LENNY_TEST_SLEEPER") != "1" {
		return
	}
	// Park until the parent terminates this process group.
	time.Sleep(2 * time.Minute)
}

// TestStartProcessRunsAndStops covers startProcess (processGroupSysProcAttr)
// and managedProcess.Stop (terminateManagedProcess): a started child is
// alive and signalable, and Stop terminates it.
func TestStartProcessRunsAndStops(t *testing.T) {
	mp := spawnSleeper(t)
	pid := mp.PID()
	if pid <= 0 {
		t.Fatalf("started child has non-positive PID %d", pid)
	}
	if !mp.Running() {
		t.Fatal("started child reports not running")
	}
	// The substrate liveness probe must agree the child is alive.
	if !processAlive(pid) {
		t.Fatalf("processAlive(%d) = false for a just-started child", pid)
	}
	if err := mp.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// After Stop the process must no longer be alive within a short
	// window (terminateManagedProcess waits for the child to exit).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		t.Errorf("child pid %d still alive after Stop", pid)
	}
	// Stop is idempotent: a second call is a no-op.
	if err := mp.Stop(); err != nil {
		t.Errorf("second Stop errored: %v", err)
	}
}

// TestStopByPIDTerminatesExternalProcess covers stopByPID
// (terminateByPID): a process recorded only by PID, with no in-memory
// managedProcess, is terminated.
func TestStopByPIDTerminatesExternalProcess(t *testing.T) {
	mp := spawnSleeper(t)
	pid := mp.PID()
	cmd := mp.cmd
	// Reap the child once it dies. In production stopByPID terminates a
	// process owned by a different process (lenny down vs the supervisor
	// that started it), so the OS reaps it; in this test the test process
	// is the parent, so it must Wait or the killed child lingers as a
	// zombie that the liveness probe still reports as alive.
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	// Detach the in-memory handle so the child is reachable only by PID,
	// mirroring lenny down against a state file.
	mp.cmd = nil
	stopByPID(pid)
	select {
	case <-reaped:
	case <-time.After(20 * time.Second):
		t.Fatalf("child pid %d not reaped after stopByPID", pid)
	}
	// stopByPID ignores a non-positive PID without panicking.
	stopByPID(0)
	stopByPID(-1)
}

// TestProcessAliveBoundaries covers the pidAlive substrate through
// processAlive at the zero, negative, self, and unused-high boundaries on
// every OS.
func TestProcessAliveBoundaries(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("processAlive reported the current process as dead")
	}
	if processAlive(0) {
		t.Error("processAlive reported PID 0 as alive")
	}
	if processAlive(-1) {
		t.Error("processAlive reported a negative PID as alive")
	}
	if processAlive(1 << 30) {
		t.Error("processAlive reported an unused high PID as alive")
	}
}
