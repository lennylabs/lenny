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
// warm-pod invariant: /workspace/slots/ and the staging directory exist
// (and slots/ is empty) before the pod is claimed, and no pod-global
// `current` leaf is created. A session's slot tree is created at slot
// assignment under §6.4.
// spec: 6.1 (warm-pod invariant), 6.4 (per-slot workspace layout)
func TestEnsureWarmWorkspaceLayout_CreatesSubdirs(t *testing.T) {
	base := t.TempDir()
	staging := filepath.Join(base, "staging")
	slots := filepath.Join(base, "slots")

	s := &Server{WorkspaceBase: base, StagingDir: staging}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}

	for _, dir := range []string{slots, staging} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}

	// spec: §6.1 — /workspace/slots/ "exists and is empty".
	entries, err := os.ReadDir(slots)
	if err != nil {
		t.Fatalf("read slots: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("workspace slots = %d entries, want empty", len(entries))
	}

	// spec: §6.4 — the pod-global `current` directory is retired, so the
	// warm layout must not create one for a runtime to fall back on.
	if _, err := os.Stat(filepath.Join(base, "current")); !os.IsNotExist(err) {
		t.Fatalf("stat %s/current = %v, want the leaf to be absent", base, err)
	}
}

// TestEnsureWarmWorkspaceLayout_Idempotent verifies a second call over
// existing directories succeeds (the adapter may restart on the same
// workspace volume).
func TestEnsureWarmWorkspaceLayout_Idempotent(t *testing.T) {
	base := t.TempDir()
	s := &Server{
		WorkspaceBase: base,
		StagingDir:    filepath.Join(base, "staging"),
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
// empty WorkspaceBase or StagingDir skips that directory.
func TestEnsureWarmWorkspaceLayout_UnconfiguredDirsSkipped(t *testing.T) {
	base := t.TempDir()

	s := &Server{WorkspaceBase: base, StagingDir: ""}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "slots")); err != nil {
		t.Fatalf("workspace slots directory not created: %v", err)
	}

	// Fully unconfigured: no-op, no error.
	empty := &Server{}
	if err := empty.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("unconfigured: %v", err)
	}
}

// TestEnsureWarmWorkspaceLayout_RootModeReadable verifies the
// /workspace/slots container carries the group/other read+execute bits
// the agent runtime needs to traverse into its own session's slot tree,
// independent of the process umask.
// spec: 6.1 (warm-pod invariant), 6.4 (per-slot workspace layout)
func TestEnsureWarmWorkspaceLayout_RootModeReadable(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	base := t.TempDir()
	slots := filepath.Join(base, "slots")
	s := &Server{WorkspaceBase: base}
	if err := s.EnsureWarmWorkspaceLayout(); err != nil {
		t.Fatalf("EnsureWarmWorkspaceLayout: %v", err)
	}
	info, err := os.Stat(slots)
	if err != nil {
		t.Fatalf("stat slots: %v", err)
	}
	if got := info.Mode().Perm(); got != warmSlotsMode {
		t.Fatalf("workspace slots mode = %o, want %o", got, warmSlotsMode)
	}
}

// TestChmodWarmDir_AlreadyCorrectModeIsNoop verifies chmodWarmDir
// succeeds when the directory already carries the requested mode, the
// common case for a kubelet-created emptyDir mountpoint the non-root
// adapter UID does not own. spec: §6.4 — F-6.4.3.
func TestChmodWarmDir_AlreadyCorrectModeIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, warmSharedMode); err != nil {
		t.Fatalf("seed mode: %v", err)
	}
	if err := chmodWarmDir(dir, warmSharedMode); err != nil {
		t.Fatalf("chmodWarmDir on a correctly-moded dir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != warmSharedMode {
		t.Fatalf("mode = %o, want %o", got, warmSharedMode)
	}
}

// TestChmodWarmDir_RaisesInadequateMode verifies chmodWarmDir lifts a
// directory whose mode is missing the requested bits up to the requested
// mode when the chmod is permitted (the adapter owns a dir it created with
// a restrictive umask). spec: §6.4 — F-6.4.3.
func TestChmodWarmDir_RaisesInadequateMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("seed restrictive mode: %v", err)
	}
	if err := chmodWarmDir(dir, warmSharedMode); err != nil {
		t.Fatalf("chmodWarmDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != warmSharedMode {
		t.Fatalf("mode = %o, want %o", got, warmSharedMode)
	}
}

// TestChmodWarmDir_DeniedButAdequateModeTolerated verifies the EPERM
// tolerance branch directly: when the chmod returns a permission error
// (the non-root adapter UID does not own the kubelet-created mountpoint)
// but the directory already grants the requested bits, chmodWarmDir
// returns nil rather than crashing the adapter (and the runtime that dials
// its socket). os.Chmod cannot be forced to return EPERM portably from a
// non-root unit test without ownership manipulation, so the branch is
// driven through chmodWarmDirWith, which injects the chmod result.
// spec: §6.4 — F-6.4.3.
func TestChmodWarmDir_DeniedButAdequateModeTolerated(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, warmSharedMode); err != nil {
		t.Fatalf("seed adequate mode: %v", err)
	}
	denied := func(string, os.FileMode) error { return os.ErrPermission }
	if err := chmodWarmDirWith(dir, warmSharedMode, denied); err != nil {
		t.Fatalf("chmodWarmDirWith tolerating a benign EPERM: %v", err)
	}

	// A denial on a directory whose mode is still inadequate is fatal: the
	// runtime would not be able to traverse it.
	restrictive := t.TempDir()
	if err := os.Chmod(restrictive, 0o700); err != nil {
		t.Fatalf("seed restrictive mode: %v", err)
	}
	if err := chmodWarmDirWith(restrictive, warmSharedMode, denied); err == nil {
		t.Fatal("chmodWarmDirWith should fail when the denied dir lacks the required bits")
	}

	// EROFS is tolerated the same way: an embedded/SDK-warm runtime mounts
	// /workspace/shared read-only, so its chmod of the already-populated
	// shared tree hits a read-only file system. spec: §6.4.
	readonly := func(string, os.FileMode) error { return syscall.EROFS }
	if err := chmodWarmDirWith(dir, warmSharedMode, readonly); err != nil {
		t.Fatalf("chmodWarmDirWith tolerating a benign EROFS: %v", err)
	}
}

// TestEnsureWarmWorkspaceLayout_PopulatesSharedAssets verifies the §6.4 read-only shared-asset tree is created and populated before
// the pod is claimed: the directory exists at a runtime-readable mode and
// each configured inline asset is written read-only.
func TestEnsureWarmWorkspaceLayout_PopulatesSharedAssets_spec_6_4(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "shared")
	s := &Server{
		WorkspaceBase:   base,
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
	// spec: §6.4 — default asset mode is 0444 (read-only).
	if got := assetInfo.Mode().Perm(); got != 0o444 {
		t.Errorf("shared asset mode = %o, want 0444", got)
	}
}

// TestEnsureWarmWorkspaceLayout_SharedDirEmptyWhenNoAssets verifies the
// §6.4 scrub-space-prevention guarantee: the shared directory is
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
