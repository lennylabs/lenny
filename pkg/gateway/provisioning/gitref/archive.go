// SPDX-License-Identifier: MIT

package gitref

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// CloneArchive clones the repository at commitSHA into a temporary
// directory and returns the directory tree as a gzip-compressed tar.
// It is the gateway-side delivery half of a §14 gitClone source: the
// gateway clones on its own network path (the pod never sees VCS
// credentials), and the returned archive is streamed to the pod's
// staging area via PrepareWorkspace, where the adapter extracts it.
// The archive includes the `.git` directory so the in-pod git client
// can operate on the checked-out tree.
func CloneArchive(ctx context.Context, url, commitSHA string, opts CloneOptions) ([]byte, error) {
	dir, err := os.MkdirTemp("", "lenny-gitclone-")
	if err != nil {
		return nil, fmt.Errorf("gitref: create clone directory: %w", err)
	}
	defer os.RemoveAll(dir)

	if err := Clone(ctx, url, commitSHA, dir, opts); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := archiveDir(dir, &buf); err != nil {
		return nil, fmt.Errorf("gitref: archive clone: %w", err)
	}
	return buf.Bytes(), nil
}

// archiveDir writes the directory tree at root to w as a
// gzip-compressed tar. Entry names are slash-separated and relative to
// root. A symlink is recorded as a symlink entry carrying its target
// verbatim; the target is never followed.
func archiveDir(root string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		link := ""
		if fi.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if fi.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr != nil {
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
