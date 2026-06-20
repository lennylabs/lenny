// SPDX-License-Identifier: MIT

//go:build unix

package k3s

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// process_unix.go is the POSIX half of the build-tagged process-control
// substrate that lets pkg/embedded/k3s compile on every target OS of
// Embedded Mode while keeping the Linux launcher's behavior verbatim. It
// carries the original Setpgid process-group start and the group-signal
// SIGTERM/SIGKILL termination the Supervisor used inline, so the Linux
// embedded-cluster launcher behaves exactly as before. The Windows half
// (process_windows.go) reimplements the same surface with Win32 job
// objects. All OS branching for process control is confined to these two
// files; k3s.go calls only this surface.
//
// spec: §17.4 (Embedded Mode runs the production stack on a host; the
// substrate is provisioned per host operating system), §24.19 (lenny
// up/down manage the supervised k3s child process).

// processGroupSysProcAttr returns the attributes that start k3s in its
// own process group, so terminateProcessGroup can signal the whole tree
// (k3s and the containerd/kubelet children it spawns) at once.
func processGroupSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// terminateProcessGroup signals the child process group and waits for it
// to exit, escalating to SIGKILL after grace elapses. The cmd has already
// been started with processGroupSysProcAttr, so the negative-PID signal
// reaches the whole group (k3s plus its containerd and kubelet children).
func terminateProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(grace):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
}

// runningAsRoot reports whether the current process is root. k3s rootless
// mode is only added when the supervisor itself is non-root. On Windows
// the launcher is unsupported, so the substrate reports false there.
func runningAsRoot() bool {
	return os.Geteuid() == 0
}
