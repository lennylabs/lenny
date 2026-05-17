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
func extractUploadArchive(root, stagingDir string, src *adapterv1.WorkspaceSource) error {
	if stagingDir == "" {
		return errors.New("uploadArchive source requires a staging directory")
	}
	staged, err := StagingPath(stagingDir, src.GetUploadRef())
	if err != nil {
		return err
	}
	strip := int(src.GetStripComponents())
	if strip < 0 {
		return fmt.Errorf("stripComponents %d is negative", strip)
	}
	prefix := src.GetPath()

	switch src.GetFormat() {
	case "tar":
		f, err := os.Open(staged)
		if err != nil {
			return fmt.Errorf("open staged archive: %w", err)
		}
		defer f.Close()
		return extractUploadTar(root, prefix, strip, f)
	case "tar.gz":
		f, err := os.Open(staged)
		if err != nil {
			return fmt.Errorf("open staged archive: %w", err)
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("open gzip stream: %w", err)
		}
		defer gz.Close()
		return extractUploadTar(root, prefix, strip, gz)
	case "zip":
		return extractUploadZip(root, prefix, strip, staged)
	default:
		return fmt.Errorf("unsupported archive format %q", src.GetFormat())
	}
}

func extractUploadTar(root, prefix string, strip int, r io.Reader) error {
	tr := tar.NewReader(r)
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		rel, ok := stripPath(hdr.Name, strip)
		if !ok {
			// §14: an entry with too few segments is skipped, not fatal.
			continue
		}
		dst, err := resolvePath(root, filepath.Join(prefix, rel))
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("create archive directory: %w", err)
			}
		case tar.TypeReg:
			n, err := extractRegular(dst, tr, archiveMode(os.FileMode(hdr.Mode)), maxExtractBytes-written)
			written += n
			if err != nil {
				return err
			}
		}
		// Non-regular, non-directory entries are skipped.
	}
}

func extractUploadZip(root, prefix string, strip int, archivePath string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip archive: %w", err)
	}
	defer zr.Close()
	var written int64
	for _, entry := range zr.File {
		rel, ok := stripPath(entry.Name, strip)
		if !ok {
			continue
		}
		dst, err := resolvePath(root, filepath.Join(prefix, rel))
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("create archive directory: %w", err)
			}
			continue
		}
		if !entry.Mode().IsRegular() {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open zip entry: %w", err)
		}
		n, err := extractRegular(dst, rc, archiveMode(entry.Mode()), maxExtractBytes-written)
		_ = rc.Close()
		written += n
		if err != nil {
			return err
		}
	}
	return nil
}

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
