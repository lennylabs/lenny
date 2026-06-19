// SPDX-License-Identifier: MIT

package adapter

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
)

// chmodWarmDir pins dir to mode so the runtime can traverse it regardless
// of the adapter's inherited umask. The directory is frequently a kubelet-
// created emptyDir mountpoint (/workspace, /workspace/shared) the adapter
// does not own or cannot write, so the chmod fails in two benign ways:
//
//   - EPERM: the mountpoint is owned by root with the pod fsGroup, not by
//     the non-root adapter UID, and chmod(2) requires process ownership.
//   - EROFS: an embedded or SDK-warm runtime that links this package mounts
//     /workspace/shared read-only (it only consumes the shared assets the
//     §6.4 producer wrote), so a chmod of that mount hits a read-only file
//     system.
//
// In both cases the kubelet (EPERM) or the producing adapter (EROFS) has
// already created the directory group/other-traversable, so the chmod is
// only confirming bits that are already present. A bare chmod instead
// crashes the adapter (and with it the runtime that dials the adapter
// socket) on every §6.4 warm pod. When the chmod fails this way but the
// directory already carries the required permission bits the failure is
// benign and treated as success; any other error, or a benign error on a
// directory whose mode is still wrong, is returned. spec: §6.4.
func chmodWarmDir(dir string, mode os.FileMode) error {
	return chmodWarmDirWith(dir, mode, os.Chmod)
}

// chmodWarmDirWith is chmodWarmDir with the chmod operation injected so a
// unit test can drive the tolerance branch, which a non-root test cannot
// force from os.Chmod without ownership or read-only-mount manipulation.
func chmodWarmDirWith(dir string, mode os.FileMode, chmod func(string, os.FileMode) error) error {
	if err := chmod(dir, mode); err != nil {
		if !errors.Is(err, fs.ErrPermission) && !errors.Is(err, syscall.EROFS) {
			return err
		}
		info, statErr := os.Stat(dir)
		if statErr != nil {
			return err
		}
		// The mountpoint already grants at least the requested bits; the
		// denial is on a directory that needs no further relaxing.
		if info.Mode().Perm()&mode.Perm() == mode.Perm() {
			return nil
		}
		return err
	}
	return nil
}

// warmWorkspaceRootMode is the permission mode of the warm-time
// /workspace/current directory. The agent-container runtime reads from
// it, so it carries group/other read+execute (0o755) — the same mode
// workspace materialization creates parent directories with.
const warmWorkspaceRootMode = 0o755

// warmSharedMode is the permission mode of the warm-time
// /workspace/shared directory. The runtime container reads from it
// (through a read-only mount), so it carries group/other read+execute
// (0o755). The directory exists even when no shared assets are
// configured, so the runtime cannot treat the mountpoint as writable
// scratch space (§6.4 line 409).
const warmSharedMode = 0o755

// warmStagingMode is the permission mode of the warm-time staging
// directory. Only the adapter UID stages uploaded content there before
// FinalizeWorkspace materializes it, so it is adapter-private (0o700),
// matching the mode PrepareWorkspace creates it with.
const warmStagingMode = 0o700

// EnsureWarmWorkspaceLayout creates the workspace subdirectories the
// §6.1 warm-pod invariant requires to exist before a pod is claimed:
// "/workspace/current exists but is empty" (spec: §6.1 line 11) and
// "/workspace/staging exists for upload staging" (spec: §6.1 line 12).
// The pod spec mounts a single emptyDir at /workspace, so without this
// step a freshly-warmed pod exposes an empty /workspace and the
// current/ and staging/ subdirectories appear only lazily at claim
// time (the first FinalizeWorkspace / PrepareWorkspace call). The
// adapter calls this once at startup, before it signals READY, so the
// directories are present for the lifetime of the warm pod.
//
// It is idempotent: re-creating an existing directory is a no-op.
// Either directory being unconfigured (empty string) skips that
// directory rather than erroring, so a Basic-level adapter wired
// without a staging area still starts.
func (s *Server) EnsureWarmWorkspaceLayout() error {
	if s.WorkspaceRoot != "" {
		if err := os.MkdirAll(s.WorkspaceRoot, warmWorkspaceRootMode); err != nil {
			return fmt.Errorf("adapter: create workspace root %q: %w", s.WorkspaceRoot, err)
		}
		// os.MkdirAll honors the process umask, which can strip the
		// group/other read+execute bits the runtime needs. Chmod the
		// leaf to the exact mode so the §6.1 "empty but present"
		// directory is readable regardless of the inherited umask. The
		// root is a kubelet-owned emptyDir mountpoint when /workspace is
		// mounted directly, so tolerate the EPERM when it already carries
		// the required bits.
		if err := chmodWarmDir(s.WorkspaceRoot, warmWorkspaceRootMode); err != nil {
			return fmt.Errorf("adapter: chmod workspace root %q: %w", s.WorkspaceRoot, err)
		}
	}
	if s.StagingDir != "" {
		if err := os.MkdirAll(s.StagingDir, warmStagingMode); err != nil {
			return fmt.Errorf("adapter: create staging directory %q: %w", s.StagingDir, err)
		}
	}
	if err := s.ensureSharedAssets(); err != nil {
		return err
	}
	return nil
}

// ensureSharedAssets creates the §6.4 /workspace/shared directory and
// materializes the configured inline shared assets into it, read-only,
// before the pod is claimed. The directory is created even when no
// assets are configured so the runtime cannot use the read-only
// mountpoint as writable scratch space. An empty SharedAssetsDir skips
// the step entirely (the pod spec still mounts the volume read-only), so
// an adapter wired without the layout still starts.
//
// spec: §6.4.
func (s *Server) ensureSharedAssets() error {
	if s.SharedAssetsDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.SharedAssetsDir, warmSharedMode); err != nil {
		return fmt.Errorf("adapter: create shared-assets directory %q: %w", s.SharedAssetsDir, err)
	}
	// MkdirAll honors the umask; pin the exact mode so the runtime can
	// traverse the directory regardless of the inherited umask. The
	// shared dir is a kubelet-owned emptyDir mountpoint, so tolerate the
	// EPERM the non-root adapter UID gets when the mountpoint already
	// carries the required bits.
	if err := chmodWarmDir(s.SharedAssetsDir, warmSharedMode); err != nil {
		return fmt.Errorf("adapter: chmod shared-assets directory %q: %w", s.SharedAssetsDir, err)
	}
	if err := sharedassets.Materialize(s.SharedAssetsDir, s.SharedAssets); err != nil {
		return fmt.Errorf("adapter: %w", err)
	}
	return nil
}
