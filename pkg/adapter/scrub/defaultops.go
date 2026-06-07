// SPDX-License-Identifier: MIT

package scrub

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// DefaultOps runs the real §5.2 scrub host operations inside the agent pod.
// The process commands (kill -9 -1, ipcrm) are Linux operations executed as
// the sandbox user; on a non-Linux host they fail at runtime, which is why
// the orchestrator records step errors rather than panicking. Unit tests
// inject a fake Ops, so DefaultOps is exercised by the pod, not the suite.
type DefaultOps struct {
	// SandboxUser, when set, is the user the process-kill commands run as
	// (the §5.2 "sandbox user"). Empty runs them as the adapter's own user,
	// which is the dev-loop default where the adapter and runtime share a
	// uid.
	SandboxUser string
}

// KillUserProcesses runs scrub step 1: kill -9 -1 terminates every process
// the sandbox user can signal. spec: §5.2 step 1.
func (d DefaultOps) KillUserProcesses(ctx context.Context) error {
	// `kill -9 -1` signals every process except the caller. Running it
	// through `sh -c` keeps the -1 target literal across su wrapping.
	if d.SandboxUser != "" {
		return run(ctx, "su", d.SandboxUser, "-c", "kill -9 -1")
	}
	return run(ctx, "sh", "-c", "kill -9 -1")
}

// PurgeIPCShm runs scrub step 1b: ipcrm --all=shm removes every System V
// shared-memory segment. spec: §5.2 step 1b.
func (d DefaultOps) PurgeIPCShm(ctx context.Context) error {
	return run(ctx, "ipcrm", "--all=shm")
}

// RemoveAll removes path and its contents (steps 0, 2). A missing path is
// not an error.
func (d DefaultOps) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

// ClearContents empties dir but leaves dir itself (step 4). A missing dir
// is not an error.
func (d DefaultOps) ClearContents(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var firstErr error
	for _, e := range entries {
		if rmErr := os.RemoveAll(filepath.Join(dir, e.Name())); rmErr != nil && firstErr == nil {
			firstErr = rmErr
		}
	}
	return firstErr
}

// PathState reports existence and (for directories) emptiness (step 6).
func (d DefaultOps) PathState(path string) (exists bool, empty bool, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return false, true, nil
		}
		return false, false, statErr
	}
	if !info.IsDir() {
		// A regular file that exists is non-empty for verification purposes
		// (any leftover task file is a dirty path).
		return true, false, nil
	}
	entries, readErr := os.ReadDir(path)
	if readErr != nil {
		return true, false, readErr
	}
	return true, len(entries) == 0, nil
}

// run executes name with args, attributing the command name on failure so
// a step error names which operation failed.
func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, out)
	}
	return nil
}
