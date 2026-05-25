// SPDX-License-Identifier: MIT

package adapter

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestEnsureWarmWorkspaceLayout_CreatesSubdirs verifies the §6.1
// warm-pod invariant: /workspace/current and the staging directory
// exist (and current/ is empty) before the pod is claimed.
func TestEnsureWarmWorkspaceLayout_CreatesSubdirs(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "current")
	staging := filepath.Join(root, "staging")

	s := &Server{WorkspaceRoot: current, StagingDir: staging}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}

	for _, dir := range []string{current, staging} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}

	// spec: §6.1 line 11 — /workspace/current "exists but is empty".
	entries, err := os.ReadDir(current)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace current = %d entries, want empty", len(entries))
	}
}

// TestEnsureWarmWorkspaceLayout_Idempotent verifies a second call over
// existing directories succeeds (the adapter may restart on the same
// workspace volume).
func TestEnsureWarmWorkspaceLayout_Idempotent(t *testing.T) {
	root := t.TempDir()
	s := &Server{
		WorkspaceRoot: filepath.Join(root, "current"),
		StagingDir:    filepath.Join(root, "staging"),
	}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

// TestEnsureWarmWorkspaceLayout_UnconfiguredDirsSkipped verifies a
// Basic-level adapter wired without a staging area still starts: an
// empty WorkspaceRoot or StagingDir skips that directory.
func TestEnsureWarmWorkspaceLayout_UnconfiguredDirsSkipped(t *testing.T) {
	root := t.TempDir()
	current := filepath.Join(root, "current")

	s := &Server{WorkspaceRoot: current, StagingDir: ""}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}
	if _, err := os.Stat(current); err != nil {
		t.Fatalf("workspace root not created: %v", err)
	}

	// Fully unconfigured: no-op, no error.
	empty := &Server{}
	if err := empty.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("unconfigured: %v", err)
	}
}

// TestEnsureWarmWorkspaceLayout_RootModeReadable verifies the
// /workspace/current leaf carries the group/other read+execute bits the
// agent runtime needs, independent of the process umask.
func TestEnsureWarmWorkspaceLayout_RootModeReadable(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	root := t.TempDir()
	current := filepath.Join(root, "current")
	s := &Server{WorkspaceRoot: current}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}
	info, err := os.Stat(current)
	if err != nil {
		t.Fatalf("stat current: %v", err)
	}
	if got := info.Mode().Perm(); got != warmWorkspaceRootMode {
		t.Fatalf("workspace current mode = %o, want %o", got, warmWorkspaceRootMode)
	}
}
