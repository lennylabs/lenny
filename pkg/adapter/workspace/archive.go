// SPDX-License-Identifier: MIT

package workspace

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
)

// Archive writes a gzip-compressed tar of the workspace directory at
// root to w and returns the number of compressed bytes written. It is
// the inverse of Materialize: the §4.7 adapter calls Archive to
// snapshot a session's workspace for a §4.4 checkpoint or the §7.1
// seal-and-export.
//
// Entry names are relative to root and slash-separated. A symlink is
// recorded as a symlink entry carrying its target verbatim; the target
// is never followed, so Archive cannot read or embed content outside
// the workspace root. Restore-side path containment is Materialize's
// responsibility.
func Archive(root string, w io.Writer) (int64, error) {
	counter := &countingWriter{w: w}
	gz := gzip.NewWriter(counter)
	tw := tar.NewWriter(gz)

	walkErr := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // the workspace root itself is implicit.
		}

		link := ""
		if fi.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
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
		f, err := os.Open(path)
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
		return counter.n, walkErr
	}

	if err := tw.Close(); err != nil {
		return counter.n, err
	}
	if err := gz.Close(); err != nil {
		return counter.n, err
	}
	return counter.n, nil
}

// countingWriter tallies the bytes written through it so Archive can
// report the compressed checkpoint size without buffering the archive.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
