// SPDX-License-Identifier: MIT

package workspace

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// extractUploadArchive materializes a §14 uploadArchive source. The
// staged archive is decoded by format (tar, tar.gz, or zip) and its
// entries are written under the source pathPrefix (carried in the proto
// path field) relative to the workspace root. stripComponents leading
// path segments are dropped from each entry per §14.
//
// Non-regular, non-directory entries (symlinks, hardlinks, devices) are
// skipped so a malicious archive cannot plant a symlink that a later
// entry writes through. Every destination is re-checked for containment
// within the workspace root, and the total uncompressed size is bounded
// by maxExtractBytes.
//
// The returned []Warning carries the §7.4 line 459
// `workspace_plan_strip_components_skip` advisory: one entry per
// archive entry the strip-components rule discarded. spec: §7.4 line
// 459. F-7.4.15.
func extractUploadArchive(root, stagingDir string, sourceIndex int, src *adapterv1.WorkspaceSource) ([]Warning, error) {
	if stagingDir == "" {
		return nil, errors.New("uploadArchive source requires a staging directory")
	}
	staged, err := StagingPath(stagingDir, src.GetUploadRef())
	if err != nil {
		return nil, err
	}
	strip := int(src.GetStripComponents())
	if strip < 0 {
		return nil, fmt.Errorf("stripComponents %d is negative", strip)
	}
	prefix := src.GetPath()

	switch src.GetFormat() {
	case "tar":
		f, err := os.Open(staged)
		if err != nil {
			return nil, fmt.Errorf("open staged archive: %w", err)
		}
		defer f.Close()
		return extractUploadTar(root, prefix, strip, sourceIndex, f)
	case "tar.gz":
		f, err := os.Open(staged)
		if err != nil {
			return nil, fmt.Errorf("open staged archive: %w", err)
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("open gzip stream: %w", err)
		}
		defer gz.Close()
		return extractUploadTar(root, prefix, strip, sourceIndex, gz)
	case "zip":
		return extractUploadZip(root, prefix, strip, sourceIndex, staged)
	default:
		return nil, fmt.Errorf("unsupported archive format %q", src.GetFormat())
	}
}

func extractUploadTar(root, prefix string, strip, sourceIndex int, r io.Reader) ([]Warning, error) {
	tr := tar.NewReader(r)
	var written int64
	var warnings []Warning
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return warnings, nil
		}
		if err != nil {
			return warnings, fmt.Errorf("read tar entry: %w", err)
		}
		rel, ok := stripPath(hdr.Name, strip)
		if !ok {
			// spec: §7.4 line 459 — an entry with too few segments after
			// stripComponents is skipped without aborting extraction and
			// emits a workspace_plan_strip_components_skip warning event
			// per skipped entry. F-7.4.15.
			warnings = append(warnings, Warning{
				Code:        stripComponentsSkipCode,
				SourceIndex: sourceIndex,
				Entry:       hdr.Name,
				Message:     fmt.Sprintf("entry has fewer than stripComponents=%d segments", strip),
			})
			continue
		}
		dst, err := resolvePath(root, filepath.Join(prefix, rel))
		if err != nil {
			return warnings, err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return warnings, fmt.Errorf("create archive directory: %w", err)
			}
		case tar.TypeReg:
			n, err := extractRegular(dst, tr, archiveMode(os.FileMode(hdr.Mode)), maxExtractBytes-written)
			written += n
			if err != nil {
				return warnings, err
			}
		}
		// Non-regular, non-directory entries are skipped.
	}
}

func extractUploadZip(root, prefix string, strip, sourceIndex int, archivePath string) ([]Warning, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}
	defer zr.Close()
	var written int64
	var warnings []Warning
	for _, entry := range zr.File {
		rel, ok := stripPath(entry.Name, strip)
		if !ok {
			// spec: §7.4 line 459 — strip-components skip per entry.
			// F-7.4.15.
			warnings = append(warnings, Warning{
				Code:        stripComponentsSkipCode,
				SourceIndex: sourceIndex,
				Entry:       entry.Name,
				Message:     fmt.Sprintf("entry has fewer than stripComponents=%d segments", strip),
			})
			continue
		}
		dst, err := resolvePath(root, filepath.Join(prefix, rel))
		if err != nil {
			return warnings, err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return warnings, fmt.Errorf("create archive directory: %w", err)
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return warnings, fmt.Errorf("open zip entry: %w", err)
		}
		n, err := extractRegular(dst, rc, archiveMode(entry.Mode()), maxExtractBytes-written)
		_ = rc.Close()
		written += n
		if err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

// stripComponentsSkipCode is the §14 closed-enum WarningCode for the
// §7.4 line 459 "entries with fewer than N segments are skipped"
// advisory. The string matches pkg/workspaceplan.WarnStripComponentsSkip
// so a future deduplication can drop one of the two definitions.
// F-7.4.15.
const stripComponentsSkipCode = "workspace_plan_strip_components_skip"

// archiveMode reduces an archive entry mode to its permission bits,
// dropping setuid, setgid, and sticky bits. A zero mode (an archive
// entry recorded without permission metadata) falls back to 0644.
func archiveMode(mode os.FileMode) os.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o644
	}
	return perm
}

// stripPath drops the first n path segments from an archive entry path
// and returns the remainder joined with "/". ok is false when, after
// removing leading and trailing empty segments, the entry has n or
// fewer segments: per §14 such an entry is skipped rather than failing
// extraction.
func stripPath(entryPath string, n int) (string, bool) {
	trimmed := strings.Trim(entryPath, "/")
	if trimmed == "" {
		return "", false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) <= n {
		return "", false
	}
	rest := strings.Join(parts[n:], "/")
	if rest == "" {
		return "", false
	}
	return rest, true
}
