// SPDX-License-Identifier: MIT

// Package upload encodes the §13.4 archive-validator constants and
// the §13.4 path-traversal / entry-kind / size invariants the gateway
// applies to every client upload and to every delegation file export.
//
// The package is pure: no filesystem I/O, no zip / tar parsing. The
// gateway's Upload Handler subsystem ([§4.1](spec/04)) streams the
// archive entry-by-entry through the parsers and feeds each entry's
// metadata into ValidateEntry; the resulting aggregate is checked
// against ValidateArchive at the end. Streaming-enforcement details
// (mid-extraction abort, partial cleanup) live in §7.4.
//
// The platform ceilings here are NORMATIVE and non-tunable per
// §13.4. Deployer policy may lower them; raising is forbidden.
package upload

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// §13.4 normative ceilings. Constants are exported so callers (and
// tests) can reference the spec values directly.
const (
	// MaxDecompressedSize is the §13.4 maximum decompressed size per
	// archive: 256 MiB.
	MaxDecompressedSize int64 = 256 * 1024 * 1024

	// MaxDecompressionRatio is the §13.4 maximum compressed:
	// uncompressed ratio: 100:1.
	MaxDecompressionRatio int64 = 100

	// MaxEntryCount is the §13.4 maximum entry count per archive.
	MaxEntryCount int = 10_000

	// MaxPerEntrySize is the §13.4 maximum per-entry size: 64 MiB.
	MaxPerEntrySize int64 = 64 * 1024 * 1024

	// MaxPathDepth is the §13.4 maximum path depth: 32 components.
	MaxPathDepth int = 32

	// MaxPathLength is the §13.4 maximum path length in UTF-8 bytes.
	MaxPathLength int = 4096
)

// EntryKind discriminates the §13.4 archive-entry kinds.
type EntryKind string

const (
	KindRegular     EntryKind = "regular"
	KindDirectory   EntryKind = "directory"
	KindSymlink     EntryKind = "symlink"
	KindHardlink    EntryKind = "hardlink"
	KindCharDevice  EntryKind = "char_device"
	KindBlockDevice EntryKind = "block_device"
	KindFIFO        EntryKind = "fifo"
	KindSocket      EntryKind = "socket"
)

// AllEntryKinds returns every §13.4 entry kind the validator knows
// about.
func AllEntryKinds() []EntryKind {
	return []EntryKind{
		KindRegular, KindDirectory, KindSymlink,
		KindHardlink, KindCharDevice, KindBlockDevice,
		KindFIFO, KindSocket,
	}
}

// IsForbidden reports whether the kind is in the §13.4 outright-
// rejected list (hardlink, char-device, block-device, FIFO, socket).
// Symlinks are NOT in this list — they are conditionally accepted
// when AllowSymlinks is true on the runtime config.
func (k EntryKind) IsForbidden() bool {
	switch k {
	case KindHardlink, KindCharDevice, KindBlockDevice, KindFIFO, KindSocket:
		return true
	}
	return false
}

// Entry is the subset of an archive entry the validator reads. The
// gateway's tar/zip parser populates this struct for each entry
// before calling ValidateEntry.
type Entry struct {
	Path       string // Path as declared in the archive (pre-canonicalization)
	Kind       EntryKind
	Size       int64  // Uncompressed size in bytes
	LinkTarget string // For symlinks: the target path
}

// Archive captures the aggregate properties the validator checks once
// the entire archive has been streamed.
type Archive struct {
	CompressedBytes   int64 // Bytes consumed from the wire
	DecompressedBytes int64 // Sum of Entry.Size across all admitted entries
	EntryCount        int
}

// RuntimeAllow captures the per-runtime opt-ins from §13.4: the
// AllowSymlinks flag plus the workspace-root the post-promotion
// symlink target check uses (`/workspace/current` in the default
// shape; concurrent-workspace pools use slot-scoped paths).
type RuntimeAllow struct {
	AllowSymlinks bool
	WorkspaceRoot string // typically "/workspace/current"
}

// Reason is the §13.4 sub-code the validator surfaces to clients as
// the `details.reason` field of `UPLOAD_ARCHIVE_LIMIT_EXCEEDED`. Each
// value matches a label of `lenny_upload_extraction_aborted_total
// {error_type}` per §16.1.
type Reason string

const (
	ReasonZipBomb               Reason = "zip_bomb"
	ReasonSizeLimit             Reason = "size_limit"
	ReasonPathTraversal         Reason = "path_traversal"
	ReasonSymlink               Reason = "symlink"
	ReasonFormatError           Reason = "format_error"
	ReasonMaxDecompressedSize   Reason = "max_decompressed_size"
	ReasonMaxDecompressionRatio Reason = "max_decompression_ratio"
	ReasonMaxEntryCount         Reason = "max_entry_count"
	ReasonMaxEntrySize          Reason = "max_entry_size"
	ReasonMaxPathDepth          Reason = "max_path_depth"
	ReasonMaxPathLength         Reason = "max_path_length"
	ReasonPathEscapesRoot       Reason = "path_escapes_root"
	ReasonNonRegularEntry       Reason = "non_regular_entry"
)

// ValidationError is the typed error returned by ValidateEntry and
// ValidateArchive. The Reason matches the §13.4 sub-code; Path is
// populated for per-entry rejections.
type ValidationError struct {
	Reason Reason
	Path   string
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("upload: %s on %q: %s", e.Reason, e.Path, e.Detail)
	}
	return fmt.Sprintf("upload: %s: %s", e.Reason, e.Detail)
}

// ErrMissingWorkspaceRoot is returned when the symlink path check is
// invoked without a RuntimeAllow.WorkspaceRoot.
var ErrMissingWorkspaceRoot = errors.New("upload: WorkspaceRoot is required for symlink validation")

// ValidateEntry applies every per-entry §13.4 rule. Returns nil when
// the entry is admissible. Callers MUST call this for every archive
// entry before extraction.
func ValidateEntry(e Entry, allow RuntimeAllow) error {
	if e.Kind.IsForbidden() {
		return &ValidationError{Reason: ReasonNonRegularEntry, Path: e.Path, Detail: fmt.Sprintf("entry kind %q is forbidden by §13.4", e.Kind)}
	}

	if len(e.Path) > MaxPathLength {
		return &ValidationError{Reason: ReasonMaxPathLength, Path: e.Path, Detail: fmt.Sprintf("path length %d exceeds maximum %d bytes", len(e.Path), MaxPathLength)}
	}

	if path.IsAbs(e.Path) {
		return &ValidationError{Reason: ReasonPathTraversal, Path: e.Path, Detail: "absolute paths are forbidden"}
	}

	// Reject any `..` segment outright, even one that path.Clean
	// would absorb (e.g., "a/b/../../etc" cleans to "etc" but is
	// still attempting to escape upward through the archive's
	// implicit root).
	if containsParentSegment(e.Path) {
		return &ValidationError{Reason: ReasonPathTraversal, Path: e.Path, Detail: "path contains `..` segment"}
	}
	cleaned := path.Clean(e.Path)

	depth := pathDepth(cleaned)
	if depth > MaxPathDepth {
		return &ValidationError{Reason: ReasonMaxPathDepth, Path: e.Path, Detail: fmt.Sprintf("path depth %d exceeds maximum %d", depth, MaxPathDepth)}
	}

	if e.Size < 0 {
		return &ValidationError{Reason: ReasonFormatError, Path: e.Path, Detail: "negative entry size"}
	}
	if e.Size > MaxPerEntrySize {
		return &ValidationError{Reason: ReasonMaxEntrySize, Path: e.Path, Detail: fmt.Sprintf("size %d exceeds maximum %d", e.Size, MaxPerEntrySize)}
	}

	if e.Kind == KindSymlink {
		if !allow.AllowSymlinks {
			return &ValidationError{Reason: ReasonSymlink, Path: e.Path, Detail: "symlinks rejected by default; runtime must opt in via allowSymlinks: true"}
		}
		if allow.WorkspaceRoot == "" {
			return ErrMissingWorkspaceRoot
		}
		if err := ValidateSymlinkTarget(e.Path, e.LinkTarget, allow.WorkspaceRoot); err != nil {
			return err
		}
	}
	return nil
}

// ValidateSymlinkTarget enforces the §13.4 symlink-target invariants:
// the canonicalised target must lie under workspaceRoot and must NOT
// traverse /proc, /sys, /dev, or /run/lenny.
func ValidateSymlinkTarget(linkPath, target, workspaceRoot string) error {
	if target == "" {
		return &ValidationError{Reason: ReasonSymlink, Path: linkPath, Detail: "symlink target is empty"}
	}
	// Resolve the target relative to the workspace root for relative
	// targets, or as-is for absolute targets.
	resolved := target
	if !path.IsAbs(resolved) {
		// Place the link at workspaceRoot/linkPath then resolve target
		// relative to its parent directory.
		parent := path.Dir(path.Join(workspaceRoot, linkPath))
		resolved = path.Clean(path.Join(parent, target))
	} else {
		resolved = path.Clean(resolved)
	}
	root := path.Clean(workspaceRoot)
	if !strings.HasPrefix(resolved, root+"/") && resolved != root {
		return &ValidationError{Reason: ReasonPathEscapesRoot, Path: linkPath, Detail: fmt.Sprintf("symlink target %q escapes workspace root %q", resolved, root)}
	}
	for _, forbidden := range []string{"/proc", "/sys", "/dev", "/run/lenny"} {
		if resolved == forbidden || strings.HasPrefix(resolved, forbidden+"/") {
			return &ValidationError{Reason: ReasonPathEscapesRoot, Path: linkPath, Detail: fmt.Sprintf("symlink target %q traverses forbidden directory %q", resolved, forbidden)}
		}
	}
	return nil
}

// ValidateArchive applies the aggregate §13.4 ceilings after every
// entry has been streamed.
func ValidateArchive(a Archive) error {
	if a.EntryCount < 0 {
		return &ValidationError{Reason: ReasonFormatError, Detail: "negative entry count"}
	}
	if a.EntryCount > MaxEntryCount {
		return &ValidationError{Reason: ReasonMaxEntryCount, Detail: fmt.Sprintf("entry count %d exceeds maximum %d", a.EntryCount, MaxEntryCount)}
	}
	if a.DecompressedBytes < 0 {
		return &ValidationError{Reason: ReasonFormatError, Detail: "negative decompressed bytes"}
	}
	if a.DecompressedBytes > MaxDecompressedSize {
		return &ValidationError{Reason: ReasonMaxDecompressedSize, Detail: fmt.Sprintf("decompressed bytes %d exceeds maximum %d", a.DecompressedBytes, MaxDecompressedSize)}
	}
	if a.CompressedBytes > 0 {
		ratio := a.DecompressedBytes / a.CompressedBytes
		if ratio > MaxDecompressionRatio {
			return &ValidationError{Reason: ReasonMaxDecompressionRatio, Detail: fmt.Sprintf("decompression ratio %d:1 exceeds maximum %d:1", ratio, MaxDecompressionRatio)}
		}
	}
	return nil
}

// pathDepth counts path components after Clean. "a/b/c" → 3.
// Leading/trailing slashes and "." are normalised away by Clean.
func pathDepth(p string) int {
	if p == "." || p == "" {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// containsParentSegment reports whether p contains a `..` component
// anywhere — at the start, in the middle, or at the end. Used to
// reject traversal attempts that path.Clean would otherwise absorb
// into an apparently-clean result (e.g., "a/b/../../etc" → "etc").
// The check operates on `/`-separated segments, not on substring
// occurrences, so "foo..bar" (legitimate filename) is admitted.
func containsParentSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}
