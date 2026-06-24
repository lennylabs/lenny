// SPDX-License-Identifier: MIT

//go:build unix

package k3s

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// spec: §17.4 (the embedded k3s substrate is provisioned per host
// operating system), §24.19 (lenny up/down manage the supervised k3s
// child process). These tests exercise the POSIX process-control
// substrate (process_unix.go) that the Supervisor's Start and Stop call.

// TestProcessGroupSysProcAttrSetsGroup covers processGroupSysProcAttr: k3s
// starts in its own process group so Stop can terminate the whole tree.
func TestProcessGroupSysProcAttrSetsGroup(t *testing.T) {
	attr := processGroupSysProcAttr()
	if attr == nil || !attr.Setpgid {
		t.Errorf("processGroupSysProcAttr = %+v, want Setpgid=true", attr)
	}
}

// TestRunningAsRootMatchesEuid covers runningAsRoot: it reports root
// exactly when the process euid is zero, which gates the --rootless flag
// in serverArgs.
func TestRunningAsRootMatchesEuid(t *testing.T) {
	if got, want := runningAsRoot(), os.Geteuid() == 0; got != want {
		t.Errorf("runningAsRoot() = %v, want %v for euid %d", got, want, os.Geteuid())
	}
}

// TestTerminateProcessGroupKillsChild covers terminateProcessGroup: a
// child started in its own process group is terminated and reaped within
// the grace window.
func TestTerminateProcessGroupKillsChild(t *testing.T) {
	// A long-sleeping child stands in for the k3s process tree.
	cmd := exec.Command("sleep", "120")
	cmd.SysProcAttr = processGroupSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	pid := cmd.Process.Pid
	if !pidAliveUnix(pid) {
		t.Fatalf("child pid %d not alive after start", pid)
	}
	terminateProcessGroup(cmd, 10*time.Second)
	if pidAliveUnix(pid) {
		t.Errorf("child pid %d still alive after terminateProcessGroup", pid)
	}
}

// TestStopProcessGroupTerminatesByPID covers StopProcessGroup: lenny down
// runs in a fresh process with no live launcher, so it terminates the
// recorded k3s process group by its leader PID. A child started in its own
// process group is terminated within the grace window when signaled by PID.
//
// spec: §17.4 (the Linux substrate outlives the CLI; lenny down stops it by
// the recorded process-group PID).
func TestStopProcessGroupTerminatesByPID(t *testing.T) {
	cmd := exec.Command("sleep", "120")
	cmd.SysProcAttr = processGroupSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	pid := cmd.Process.Pid
	// Reap the child so the kernel does not keep it as a zombie (which would
	// keep signal-0 reporting it alive).
	go func() { _ = cmd.Wait() }()
	if !pidAliveUnix(pid) {
		t.Fatalf("child pid %d not alive after start", pid)
	}
	StopProcessGroup(pid)
	// The SIGTERM polling loop returns once the group exits; allow a brief
	// settle for the kernel to reap.
	deadline := time.Now().Add(5 * time.Second)
	for pidAliveUnix(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if pidAliveUnix(pid) {
		t.Errorf("child pid %d still alive after StopProcessGroup", pid)
	}
}

// TestStopProcessGroupZeroPIDIsNoOp covers the guard: a zero or negative PID
// (the Docker-backed launcher records no Linux PID) is a no-op rather than a
// broadcast signal to the caller's own process group (kill(0, ...)).
//
// spec: §17.4 (the recorded k3s PID is zero on the Docker-backed launcher).
func TestStopProcessGroupZeroPIDIsNoOp(t *testing.T) {
	// Must not panic, block, or signal the test's own process group.
	StopProcessGroup(0)
	StopProcessGroup(-1)
}

// pidAliveUnix reports whether a PID is running, using signal 0 (the
// kernel performs error checking without delivering a signal).
func pidAliveUnix(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
