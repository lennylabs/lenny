// SPDX-License-Identifier: MIT

package stack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// managedProcess is a child process the Embedded Mode stack owns: the
// production gateway or a controller. The process is started in its own
// process group (or, on Windows, its own console process group and
// kill-on-close job) so Stop can terminate the whole tree. The
// OS-specific group/detach attributes and termination live in the
// build-tagged process-control substrate (process_unix.go,
// process_windows.go); this file holds the cross-platform consumer API.
type managedProcess struct {
	name    string
	cmd     *exec.Cmd
	logFile *os.File
}

// processSpec configures a managedProcess.
type processSpec struct {
	// Name labels the process in log lines and lenny status output.
	Name string
	// BinPath is the executable to run.
	BinPath string
	// Args are the command-line arguments.
	Args []string
	// Env is the full environment for the child process.
	Env []string
	// LogPath is the file the process output is written to. lenny logs
	// reads it.
	LogPath string
}

// startProcess launches a child process per spec. The process output
// is redirected to spec.LogPath.
func startProcess(spec processSpec) (*managedProcess, error) {
	if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o700); err != nil {
		return nil, fmt.Errorf("embedded: create log directory for %s: %w", spec.Name, err)
	}
	logFile, err := os.OpenFile(spec.LogPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("embedded: open %s log: %w", spec.Name, err)
	}
	cmd := exec.Command(spec.BinPath, spec.Args...)
	cmd.Env = spec.Env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Run the child in its own process group so Stop terminates it and
	// any grandchildren together. The attributes are OS-specific.
	cmd.SysProcAttr = processGroupSysProcAttr()
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("embedded: start %s (%s): %w", spec.Name, spec.BinPath, err)
	}
	return &managedProcess{name: spec.Name, cmd: cmd, logFile: logFile}, nil
}

// PID returns the child process identifier, or zero when the process
// is not running.
func (m *managedProcess) PID() int {
	if m == nil || m.cmd == nil || m.cmd.Process == nil {
		return 0
	}
	return m.cmd.Process.Pid
}

// Running reports whether the child process is still alive.
func (m *managedProcess) Running() bool {
	return m != nil && m.cmd != nil && m.cmd.Process != nil &&
		(m.cmd.ProcessState == nil || !m.cmd.ProcessState.Exited())
}

// Stop terminates the child process tree and waits for it to exit,
// escalating to a forced kill after a grace period. Stop is idempotent.
func (m *managedProcess) Stop() error {
	if m == nil || m.cmd == nil || m.cmd.Process == nil {
		return nil
	}
	terminateManagedProcess(m.cmd, 10*time.Second)
	if m.logFile != nil {
		_ = m.logFile.Close()
		m.logFile = nil
	}
	m.cmd = nil
	return nil
}

// stopByPID terminates an external process tree by PID. lenny down uses
// it to terminate the gateway and controllers recorded in the state
// file when the orchestrating Stack object is not in memory.
func stopByPID(pid int) {
	if pid <= 0 {
		return
	}
	terminateByPID(pid, 15*time.Second)
}
