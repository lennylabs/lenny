// SPDX-License-Identifier: MIT

package adapter

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
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

// TestEnsureWarmWorkspaceLayout_PopulatesSharedAssets verifies the §6.4
// line 409 read-only shared-asset tree is created and populated before
// the pod is claimed: the directory exists at a runtime-readable mode and
// each configured inline asset is written read-only.
func TestEnsureWarmWorkspaceLayout_PopulatesSharedAssets_spec_6_4(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	s := &Server{
		WorkspaceRoot:   filepath.Join(root, "current"),
		SharedAssetsDir: shared,
		SharedAssets: []sharedassets.FileSpec{
			{Path: "common/config.yaml", Content: "shared: true\n"},
		},
	}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}

	info, err := os.Stat(shared)
	if err != nil {
		t.Fatalf("stat shared: %v", err)
	}
	if got := info.Mode().Perm(); got != warmSharedMode {
		t.Errorf("shared dir mode = %o, want %o", got, warmSharedMode)
	}

	got, err := os.ReadFile(filepath.Join(shared, "common/config.yaml"))
	if err != nil {
		t.Fatalf("read shared asset: %v", err)
	}
	if string(got) != "shared: true\n" {
		t.Errorf("shared asset content = %q, want %q", got, "shared: true\n")
	}
	assetInfo, err := os.Stat(filepath.Join(shared, "common/config.yaml"))
	if err != nil {
		t.Fatalf("stat shared asset: %v", err)
	}
	// spec: §6.4 line 409 — default asset mode is 0444 (read-only).
	if got := assetInfo.Mode().Perm(); got != 0o444 {
		t.Errorf("shared asset mode = %o, want 0444", got)
	}
}

// TestEnsureWarmWorkspaceLayout_SharedDirEmptyWhenNoAssets verifies the
// §6.4 line 409 scrub-space-prevention guarantee: the shared directory is
// created (so the runtime cannot use the mountpoint as writable scratch)
// even when no assets are configured, and it stays empty.
func TestEnsureWarmWorkspaceLayout_SharedDirEmptyWhenNoAssets_spec_6_4(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	s := &Server{SharedAssetsDir: shared}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}
	entries, err := os.ReadDir(shared)
	if err != nil {
		t.Fatalf("read shared: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("shared dir has %d entries, want 0", len(entries))
	}
}

// TestEnsureWarmWorkspaceLayout_SharedDirUnconfiguredSkipped verifies an
// empty SharedAssetsDir skips the populate step (the pod spec still mounts
// the volume read-only), so an adapter wired without the layout starts.
func TestEnsureWarmWorkspaceLayout_SharedDirUnconfiguredSkipped_spec_6_4(t *testing.T) {
	s := &Server{SharedAssets: []sharedassets.FileSpec{{Path: "x", Content: "y"}}}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}
}

// TestEnsureWarmWorkspaceLayout_SharedAssetEscapeRejected verifies a
// path-escaping shared asset fails the warm-layout step (and crash-loops
// the pod) rather than writing outside the shared root.
func TestEnsureWarmWorkspaceLayout_SharedAssetEscapeRejected_spec_6_4(t *testing.T) {
	root := t.TempDir()
	s := &Server{
		SharedAssetsDir: filepath.Join(root, "shared"),
		SharedAssets:    []sharedassets.FileSpec{{Path: "../escape.txt", Content: "x"}},
	}
	if err := s.EnsureWarmWorkspaceLayout(); err == nil {
		t.Fatal("EnsureWarmWorkspaceLayout accepted an escaping shared asset")
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); err == nil {
		t.Fatal("escaping shared asset landed outside the shared root")
	}
}
