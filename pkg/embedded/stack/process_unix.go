// SPDX-License-Identifier: MIT

//go:build unix

package stack

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// process_unix.go is the POSIX half of the build-tagged process-control
// substrate that lets pkg/embedded/stack build and run on both Linux and
// Windows. It carries the original Setpgid/Setsid detach, group-signal
// termination, signal-0 liveness, and SIGHUP-based supervisor wakeup
// verbatim, so the Linux Embedded Mode launcher behaves exactly as
// before. The Windows half (process_windows.go) reimplements the same
// consumer-facing surface with job objects and named events. All OS
// branching for process control is confined to these two files; the
// stack's business logic (process.go, lifecycle.go, restart.go,
// state.go) calls only this surface.
//
// spec: §17.4 (Embedded Mode runs the production stack on a host; the
// substrate is provisioned per host operating system), §24.19 (lenny
// up/down/restart manage the supervised child processes).

// detachSysProcAttr returns the attributes that detach a child into its
// own session so it outlives the foreground process and its controlling
// terminal. lifecycle.go's RunUp uses it to launch the supervisor.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// processGroupSysProcAttr returns the attributes that start a child in
// its own process group, so terminateManagedProcess can signal the whole
// tree (the child and any grandchildren) at once.
func processGroupSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// terminateManagedProcess signals the child process group and waits for
// it to exit, escalating to SIGKILL after grace elapses. The cmd has
// already been started with processGroupSysProcAttr, so the negative-PID
// signal reaches the group.
func terminateManagedProcess(cmd *exec.Cmd, grace time.Duration) {
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

// terminateByPID signals an external process group by PID and escalates
// to SIGKILL after the grace deadline. It signals the process group
// first and falls back to the bare PID when the process is not a group
// leader. stopByPID uses it to terminate processes recorded in the state
// file when the orchestrating Stack object is not in memory.
func terminateByPID(pid int, grace time.Duration) {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// pidAlive reports whether a process with the given PID is currently
// running and signalable. Signal 0 performs error checking in the kernel
// without delivering a signal, so a nil error means the process exists.
func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// supervisorSignals is the supervisor's wakeup substrate: a restart
// channel and a teardown channel. RunSupervisor blocks on both. On unix
// they are backed by signals — SIGHUP requests a single-component
// restart, SIGTERM/SIGINT request a graceful teardown — preserving the
// original signal-driven supervisor loop verbatim.
type supervisorSignals struct {
	sigCh chan os.Signal
}

// newSupervisorSignals installs the supervisor's signal handlers. root
// is unused on unix (the wakeup is process-global signals); the Windows
// implementation keys named events off it.
func newSupervisorSignals(_ string) (*supervisorSignals, error) {
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	return &supervisorSignals{sigCh: sigCh}, nil
}

// wait blocks until a restart or teardown wakeup arrives, the context is
// cancelled, or the supervisor is otherwise asked to stop. It reports
// restart=true for a restart wakeup (the caller services it and loops)
// and restart=false for a teardown wakeup or a cancelled context (the
// caller tears the stack down).
func (s *supervisorSignals) wait(ctx context.Context) (restart bool) {
	select {
	case sig := <-s.sigCh:
		// spec: §24.19 — SIGHUP restarts one component in place; SIGTERM
		// and SIGINT trigger a graceful teardown.
		return sig == syscall.SIGHUP
	case <-ctx.Done():
		return false
	}
}

// close releases the supervisor's signal handlers.
func (s *supervisorSignals) close() {
	signal.Stop(s.sigCh)
}

// sendRestartSignal asks the supervisor process to restart one component.
// On unix it sends SIGHUP, which the supervisor's signal handler catches
// to read the restart-request file. paths is unused on unix; the Windows
// implementation opens the named restart event it identifies.
func sendRestartSignal(pid int, _ Paths) error {
	return syscall.Kill(pid, syscall.SIGHUP)
}

// gracefulStopSupervisor attempts an OS-specific graceful teardown of the
// supervisor before stopByPID escalates. On unix it is a no-op: stopByPID
// sends SIGTERM, which the supervisor's signal handler already catches to
// run its graceful Stop path, so no separate wakeup is needed. It reports
// false so the caller proceeds to stopByPID. pid and paths are unused on
// unix.
func gracefulStopSupervisor(_ int, _ Paths) bool {
	return false
}
