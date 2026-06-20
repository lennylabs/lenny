// SPDX-License-Identifier: MIT

//go:build windows

package k3s

import (
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// process_windows.go is the Windows half of the build-tagged
// process-control substrate that lets pkg/embedded/k3s compile on
// Windows, a target OS of Embedded Mode. It reimplements the same
// consumer-facing surface the POSIX half (process_unix.go) provides —
// process-group start and tree termination — using Win32 primitives:
// CREATE_NEW_PROCESS_GROUP for the start and a kill-on-close job object
// for the tree termination that a process-group signal carries on unix.
// All OS branching for process control is confined to this file and
// process_unix.go; k3s.go calls only this surface.
//
// The Linux child-process launcher does not run on Windows
// (SupportedPlatform reports false; the Docker-backed launcher is a
// separate concern), so this file exists to keep the package linking on
// Windows rather than to start k3s directly there. The job-object
// termination is shared substrate so a future Docker-backed launcher and
// any other Windows-side managed child reuse one tree-kill path.
//
// spec: §17.4 (Embedded Mode runs the production stack on a host; the
// substrate is provisioned per host operating system), §24.19 (lenny
// up/down manage the supervised child process).

// processGroupSysProcAttr returns the attributes that start a managed
// child in its own process group so termination does not propagate to the
// supervisor. terminateProcessGroup then terminates the child and its
// tree.
func processGroupSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// terminateProcessGroup terminates the child and the tree it leads and
// waits for it to exit, escalating to a second forced tree kill after
// grace elapses. Windows has no process-group signal, so termination
// assigns the child to a kill-on-close job object and closes it, which
// terminates the child and every descendant it spawned.
func terminateProcessGroup(cmd *exec.Cmd, grace time.Duration) {
	pid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	killProcessTree(pid)
	select {
	case <-done:
	case <-time.After(grace):
		killProcessTree(pid)
		<-done
	}
}

// killProcessTree terminates the process identified by pid and the tree
// it leads by assigning it to a kill-on-close job object and closing the
// job. A process can be assigned to a job it is not already part of; the
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE limit terminates every process in the
// job when the last handle closes. On any failure it falls back to a
// direct TerminateProcess of the single PID so a leak fails closed.
func killProcessTree(pid int) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false, uint32(pid),
	)
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		_ = windows.TerminateProcess(handle, 1)
		return
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.TerminateProcess(handle, 1)
		_ = windows.CloseHandle(job)
		return
	}
	if err := windows.AssignProcessToJobObject(job, handle); err != nil {
		// The process may already belong to another job. Terminate it
		// directly so it does not leak.
		_ = windows.TerminateProcess(handle, 1)
		_ = windows.CloseHandle(job)
		return
	}
	// Closing the last job handle terminates every process in the job.
	_ = windows.CloseHandle(job)
}

// runningAsRoot reports whether the supervisor runs with elevated
// privileges. The Windows child-process launcher is unsupported (k3s
// needs a Linux kernel), so serverArgs' rootless gating is moot here; the
// substrate reports false to keep one consumer surface across operating
// systems.
func runningAsRoot() bool {
	return false
}
