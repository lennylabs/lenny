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

// pidAliveUnix reports whether a PID is running, using signal 0 (the
// kernel performs error checking without delivering a signal).
func pidAliveUnix(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
