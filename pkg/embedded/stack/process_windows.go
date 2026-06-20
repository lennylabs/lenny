// SPDX-License-Identifier: MIT

//go:build windows

package stack

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// process_windows.go is the Windows half of the build-tagged
// process-control substrate that lets pkg/embedded/stack build and run
// on Windows, a target OS of Embedded Mode. It reimplements the same
// consumer-facing surface the POSIX half (process_unix.go) provides —
// detach, termination, liveness, and supervisor wakeup — using Win32
// primitives: CREATE_NEW_PROCESS_GROUP / a kill-on-close job object for
// detach and tree termination, OpenProcess + GetExitCodeProcess for
// liveness, and named auto-reset events for the cross-process restart and
// teardown wakeups that SIGHUP / SIGTERM carry on unix. All OS branching
// for process control is confined to this file and process_unix.go; the
// stack's business logic calls only this surface.
//
// spec: §17.4 (Embedded Mode runs the production stack on a host;
// the substrate is provisioned per host operating system), §24.19
// (lenny up/down/restart manage the supervised child processes).

// stillActive is the GetExitCodeProcess sentinel (STILL_ACTIVE,
// STATUS_PENDING) returned while a process has not exited.
const stillActive = 259

// detachSysProcAttr returns the attributes that detach a child from the
// foreground console so it outlives the foreground process. On Windows
// there is no session leader; CREATE_NEW_PROCESS_GROUP plus DETACHED
// PROCESS gives the supervisor an independent console-free process group.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
}

// processGroupSysProcAttr returns the attributes that start a managed
// child in its own process group so termination does not propagate to
// the supervisor. terminateManagedProcess then terminates the child and
// its tree.
func processGroupSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// terminateManagedProcess terminates the child and waits for it to exit,
// escalating to a forced tree kill after grace elapses. Windows has no
// process-group signal, so termination assigns the child to a
// kill-on-close job object and closes it, which terminates the child and
// every descendant it spawned.
func terminateManagedProcess(cmd *exec.Cmd, grace time.Duration) {
	pid := cmd.Process.Pid
	// Ask the child to exit by terminating its process tree, then wait
	// up to grace for cmd.Wait to reap it.
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

// terminateByPID terminates an external process tree by PID and waits up
// to grace for it to exit. stopByPID uses it for processes recorded in
// the state file when the orchestrating Stack object is not in memory.
func terminateByPID(pid int, grace time.Duration) {
	killProcessTree(pid)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	killProcessTree(pid)
}

// killProcessTree terminates the process identified by pid and the tree
// it leads by assigning it to a kill-on-close job object and closing the
// job. A process can be assigned to a job it is not already part of; the
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE limit terminates every process in
// the job when the last handle closes. On any failure it falls back to a
// direct TerminateProcess of the single PID so a leak fails closed.
func killProcessTree(pid int) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false, uint32(pid),
	)
	if err != nil {
		return
	}
	defer windows.CloseHandle(handle)

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

// pidAlive reports whether a process with the given PID is currently
// running. OpenProcess succeeds for a live PID; GetExitCodeProcess
// returns STILL_ACTIVE while the process has not exited. A handle that
// cannot be opened, or an exit code other than STILL_ACTIVE, reports the
// process as not running.
func pidAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return false
	}
	return code == stillActive
}

// supervisorSignals is the supervisor's wakeup substrate on Windows: two
// named auto-reset events keyed off the state root, standing in for the
// SIGHUP (restart) and SIGTERM (teardown) wakeups the unix path uses.
// lenny restart and lenny down open the same named events and signal
// them, so the cross-process wakeup carries across the process boundary
// without a POSIX signal.
type supervisorSignals struct {
	restart  windows.Handle
	teardown windows.Handle
}

// newSupervisorSignals creates the supervisor's named restart and
// teardown events. root identifies the stack so the sender (lenny restart
// / lenny down) opens the matching events.
func newSupervisorSignals(root string) (*supervisorSignals, error) {
	restartName, err := windows.UTF16PtrFromString(restartEventName(root))
	if err != nil {
		return nil, fmt.Errorf("embedded: restart event name: %w", err)
	}
	teardownName, err := windows.UTF16PtrFromString(teardownEventName(root))
	if err != nil {
		return nil, fmt.Errorf("embedded: teardown event name: %w", err)
	}
	// Auto-reset (manualReset=0), initially non-signaled.
	restart, err := windows.CreateEvent(nil, 0, 0, restartName)
	if err != nil {
		return nil, fmt.Errorf("embedded: create restart event: %w", err)
	}
	teardown, err := windows.CreateEvent(nil, 0, 0, teardownName)
	if err != nil {
		_ = windows.CloseHandle(restart)
		return nil, fmt.Errorf("embedded: create teardown event: %w", err)
	}
	return &supervisorSignals{restart: restart, teardown: teardown}, nil
}

// wait blocks until the restart event, the teardown event, or the
// context fires. It reports restart=true for a restart wakeup (the caller
// services it and loops) and restart=false for a teardown wakeup or a
// cancelled context (the caller tears the stack down). It polls the two
// events on a short interval so a cancelled context unblocks promptly.
func (s *supervisorSignals) wait(ctx context.Context) (restart bool) {
	for {
		switch ev, _ := windows.WaitForSingleObject(s.restart, 250); ev {
		case windows.WAIT_OBJECT_0:
			return true
		}
		switch ev, _ := windows.WaitForSingleObject(s.teardown, 0); ev {
		case windows.WAIT_OBJECT_0:
			return false
		}
		select {
		case <-ctx.Done():
			return false
		default:
		}
	}
}

// close releases the supervisor's event handles.
func (s *supervisorSignals) close() {
	_ = windows.CloseHandle(s.restart)
	_ = windows.CloseHandle(s.teardown)
}

// sendRestartSignal asks the supervisor process to restart one component.
// On Windows it opens the supervisor's named restart event and sets it,
// which wakes the supervisor's wait loop; the supervisor then reads the
// restart-request file. pid is unused on Windows (the event is keyed off
// the state root, not the PID); paths supplies the root.
func sendRestartSignal(_ int, paths Paths) error {
	return setNamedEvent(restartEventName(paths.Root))
}

// gracefulStopSupervisor asks the supervisor to tear the stack down
// gracefully and waits for it to exit. Windows has no SIGTERM the
// supervisor can catch, so lenny down sets the supervisor's named
// teardown event, which wakes the supervisor's wait loop and runs its
// graceful Stop path. It reports true when the supervisor exits within
// the grace window, so the caller can skip the forced stopByPID
// escalation; on any failure or timeout it reports false and the caller
// falls back to a forced termination, failing closed against a leak.
func gracefulStopSupervisor(pid int, paths Paths) bool {
	if err := setNamedEvent(teardownEventName(paths.Root)); err != nil {
		return false
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// setNamedEvent opens an existing named event and signals it. A missing
// event (no supervisor listening) returns an error the caller treats as
// "no graceful path available".
func setNamedEvent(name string) error {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("embedded: event name: %w", err)
	}
	handle, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, ptr)
	if err != nil {
		return fmt.Errorf("embedded: open event %q: %w", name, err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.SetEvent(handle); err != nil {
		return fmt.Errorf("embedded: set event %q: %w", name, err)
	}
	return nil
}

// restartEventName and teardownEventName derive Local-namespace event
// names from the state root so concurrent stacks under different roots do
// not collide. The root path is sanitized into a valid object name.
func restartEventName(root string) string  { return eventName(root, "restart") }
func teardownEventName(root string) string { return eventName(root, "teardown") }

func eventName(root, kind string) string {
	clean := strings.Map(func(r rune) rune {
		if r == '\\' || r == '/' || r == ':' {
			return '_'
		}
		return r
	}, filepath.Clean(root))
	return `Local\lenny-embedded-` + kind + "-" + clean
}
