// SPDX-License-Identifier: MIT

package workspace

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// maxExtractBytes caps the total uncompressed size Extract will write,
// bounding a decompression-bomb archive. The kubelet emptyDir size
// limit is the hard cap on the pod; this is defense in depth.
const maxExtractBytes = 2 << 30

// Extract reads a gzip-compressed tar from r and writes its entries
// into the workspace directory at root. It is the inverse of Archive:
// the §4.7 adapter calls it to restore a session workspace from a §4.4
// checkpoint or a §7.1 snapshot, and to materialize a §14 uploadArchive
// source.
//
// Every entry is contained within root: an entry whose path escapes
// root via `..` or an absolute path fails the extract fail-closed. A
// symlink entry is restored only when its target resolves within root,
// so a restored workspace can never carry a symlink that points
// outside it — closing the tar symlink-traversal vector. Regular files
// keep only their permission bits; setuid and setgid are dropped.
func Extract(root string, r io.Reader) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("workspace: open archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	rootClean := filepath.Clean(root)
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("workspace: read archive: %w", err)
		}
		dest, err := resolvePath(rootClean, hdr.Name)
		if err != nil {
			return fmt.Errorf("workspace: archive entry %q: %w", hdr.Name, err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return fmt.Errorf("workspace: create directory %q: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			n, err := extractRegular(dest, tr, os.FileMode(hdr.Mode).Perm(), maxExtractBytes-written)
			if err != nil {
				return fmt.Errorf("workspace: archive entry %q: %w", hdr.Name, err)
			}
			written += n
		case tar.TypeSymlink:
			if err := extractSymlink(rootClean, dest, hdr.Linkname); err != nil {
				return fmt.Errorf("workspace: archive entry %q: %w", hdr.Name, err)
			}
		default:
			return fmt.Errorf("workspace: archive entry %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
	}
}

// extractRegular writes one regular file, bounded by remaining bytes.
// It returns the number of bytes written.
func extractRegular(dest string, r io.Reader, mode os.FileMode, remaining int64) (int64, error) {
	if remaining < 0 {
		return 0, fmt.Errorf("archive exceeds the %d-byte extract limit", int64(maxExtractBytes))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	// LimitReader admits one byte past the budget so an over-budget
	// file is detected rather than silently truncated.
	n, copyErr := io.Copy(f, io.LimitReader(r, remaining+1))
	closeErr := f.Close()
	if copyErr != nil {
		return n, copyErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	if n > remaining {
		return n, fmt.Errorf("archive exceeds the %d-byte extract limit", int64(maxExtractBytes))
	}
	return n, nil
}

// extractSymlink restores a symlink entry, rejecting a target that
// resolves outside the workspace root.
func extractSymlink(rootClean, dest, linkname string) error {
	if linkname == "" {
		return fmt.Errorf("symlink target is empty")
	}
	target := linkname
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(dest), target)
	}
	target = filepath.Clean(target)
	if target != rootClean && !pathWithin(rootClean, target) {
		return fmt.Errorf("symlink target %q escapes the workspace root", linkname)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.Symlink(linkname, dest)
}
