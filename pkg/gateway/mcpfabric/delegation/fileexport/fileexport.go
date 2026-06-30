// SPDX-License-Identifier: MIT

// Package fileexport implements the §8.7 delegation file-export
// validation rules: destPrefix rebasing constraints, the
// fileExportLimits structural ceilings, and the realpath
// symlink-containment check against the parent's /workspace/current.
//
// These are the validators the §8.7 materialization path (delegate_task
// fileExport resolution → parent-pod file fetch → child-workspace
// persistence) runs before any byte crosses to the child. That
// materialization path is a follow-on (see the SEAM in
// pkg/gateway/interceptor/export.go); the validators are built and
// independently tested here so the path can call them without
// reimplementing the security checks. The per-file content scan
// (PreExportMaterialization) and the archive-validator inheritance for
// exported archives are handled separately by
// pkg/gateway/interceptor.RunPreExportMaterialization and pkg/upload.
package fileexport

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/lennylabs/lenny/pkg/upload"
)

// destPrefix validation errors. spec: §8.7 line 789.
var (
	// ErrDestPrefixAbsolute — destPrefix is an absolute path. §8.7
	// requires a relative path so the rebased file lands under the
	// child's workspace root.
	ErrDestPrefixAbsolute = errors.New("fileexport: destPrefix must be a relative path, not absolute")
	// ErrDestPrefixParentSegment — destPrefix contains a `..` segment,
	// which could rebase an exported file above the child's workspace
	// root.
	ErrDestPrefixParentSegment = errors.New("fileexport: destPrefix must not contain a `..` segment")
)

// ValidateDestPrefix enforces the §8.7 line 789 destPrefix rules: a
// destPrefix must be a relative path with no `..` segment and no leading
// `/`. An empty destPrefix is valid and means "rebase to the child's
// workspace root" (the "none" column in the §8.7 rebasing table). The
// check is purely lexical; it does not touch the filesystem.
func ValidateDestPrefix(destPrefix string) error {
	if destPrefix == "" {
		return nil
	}
	if path.IsAbs(destPrefix) {
		return fmt.Errorf("%w: %q", ErrDestPrefixAbsolute, destPrefix)
	}
	// Reject a `..` component even one path.Clean would absorb (e.g.
	// "a/../../x" cleans to "../x" but "ok/../bad" cleans to "bad"); the
	// segment scan catches the escape attempt regardless of where Clean
	// lands. Reuse the upload package's traversal rule rather than
	// duplicating it.
	if upload.ContainsParentSegment(destPrefix) {
		return fmt.Errorf("%w: %q", ErrDestPrefixParentSegment, destPrefix)
	}
	return nil
}

// FileExportLimits is the §8.3 line 264 fileExportLimits structural
// ceiling carried on the delegation lease: the maximum number of
// exported files and the maximum aggregate exported size in bytes,
// checked across all of a delegation's fileExport entries. It is
// distinct from contentPolicy.maxExportedFileSize (the §8.7 per-file
// scan ceiling, enforced by RunPreExportMaterialization).
type FileExportLimits struct {
	// MaxFiles caps the total number of exported files. A non-positive
	// value disables the file-count check (operator opt-out), matching
	// the platform's other zero-means-unlimited limiters.
	MaxFiles int
	// MaxTotalSize caps the aggregate exported size in bytes. A
	// non-positive value disables the size check.
	MaxTotalSize int64
}

// DefaultFileExportLimits is the §8.3 line 264 default
// `{ maxFiles: 100, maxTotalSize: 100MB }`.
var DefaultFileExportLimits = FileExportLimits{
	MaxFiles:     100,
	MaxTotalSize: 100 * 1024 * 1024,
}

// fileExportLimits violation errors. spec: §8.7 lines 790-791.
var (
	// ErrTooManyFiles — the export's file count exceeds MaxFiles.
	ErrTooManyFiles = errors.New("fileexport: exported file count exceeds fileExportLimits.maxFiles")
	// ErrTotalSizeExceeded — the export's aggregate byte size exceeds
	// MaxTotalSize.
	ErrTotalSizeExceeded = errors.New("fileexport: aggregate exported size exceeds fileExportLimits.maxTotalSize")
)

// Check enforces the §8.7 lines 790-791 aggregate ceilings against a
// resolved export's file count and total byte size. The §15.1 line 1071
// note positions these as "the export validation" that surfaces before
// the per-file contentPolicy.maxExportedFileSize scan gate.
func (l FileExportLimits) Check(fileCount int, totalBytes int64) error {
	if l.MaxFiles > 0 && fileCount > l.MaxFiles {
		return fmt.Errorf("%w: %d > %d", ErrTooManyFiles, fileCount, l.MaxFiles)
	}
	if l.MaxTotalSize > 0 && totalBytes > l.MaxTotalSize {
		return fmt.Errorf("%w: %d > %d bytes", ErrTotalSizeExceeded, totalBytes, l.MaxTotalSize)
	}
	return nil
}

// ErrPathEscapesRoot — a matched export source resolved (via realpath)
// to a location outside the parent's workspace root. spec: §8.7 line
// 787.
var ErrPathEscapesRoot = errors.New("fileexport: resolved source path escapes the workspace root")

// ResolveWithinRoot implements the §8.7 line 787 symlink defense: it
// resolves candidate to its real path (following symlinks, like
// realpath) and rejects it when the resolved path falls outside
// workspaceRoot. This stops an agent that planted a symlink (for
// example `./data → /etc/passwd`) inside its workspace from exfiltrating
// the target through a delegation export. Both workspaceRoot and
// candidate are resolved so a symlinked root is compared against a
// symlinked candidate consistently.
//
// It returns the resolved real path on success. A candidate that does
// not exist, or a workspaceRoot that cannot be resolved, returns a
// wrapped filesystem error. A resolved candidate outside the root
// returns ErrPathEscapesRoot.
func ResolveWithinRoot(workspaceRoot, candidate string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("fileexport: resolve workspace root %q: %w", workspaceRoot, err)
	}
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("fileexport: resolve export source %q: %w", candidate, err)
	}
	rel, err := filepath.Rel(realRoot, realCandidate)
	if err != nil {
		return "", fmt.Errorf("%w: %q resolved to %q", ErrPathEscapesRoot, candidate, realCandidate)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q resolved to %q", ErrPathEscapesRoot, candidate, realCandidate)
	}
	return realCandidate, nil
}
