// SPDX-License-Identifier: MIT

package adapter

import (
	"fmt"
	"os"

	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
)

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
		// directory is readable regardless of the inherited umask.
		if err := os.Chmod(s.WorkspaceRoot, warmWorkspaceRootMode); err != nil {
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
// spec: §6.4 line 409 — F-6.4.3.
func (s *Server) ensureSharedAssets() error {
	if s.SharedAssetsDir == "" {
		return nil
	}
	if err := os.MkdirAll(s.SharedAssetsDir, warmSharedMode); err != nil {
		return fmt.Errorf("adapter: create shared-assets directory %q: %w", s.SharedAssetsDir, err)
	}
	// MkdirAll honors the umask; pin the exact mode so the runtime can
	// traverse the directory regardless of the inherited umask.
	if err := os.Chmod(s.SharedAssetsDir, warmSharedMode); err != nil {
		return fmt.Errorf("adapter: chmod shared-assets directory %q: %w", s.SharedAssetsDir, err)
	}
	if err := sharedassets.Materialize(s.SharedAssetsDir, s.SharedAssets); err != nil {
		return fmt.Errorf("adapter: %w", err)
	}
	return nil
}
