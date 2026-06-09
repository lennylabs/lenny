// SPDX-License-Identifier: MIT

package workspacepack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// IgnoreFileNames lists the ignore files the packer consults, in
// precedence order: a `.lennyignore` at the workspace root takes priority
// over `.gitignore`. Only the root file is read; nested ignore files are
// not consulted.
//
// spec: §26.2 line 114.
var IgnoreFileNames = []string{".lennyignore", ".gitignore"}

// vcsMetadataDir is the version-control metadata directory the packer
// always excludes. The §26.2 gitClone source is the supported path for
// carrying git history into a session, so the uploadArchive path omits the
// repository's `.git` directory to keep the archive small.
const vcsMetadataDir = ".git"

// Result reports what a Pack call produced.
type Result struct {
	// Data is the gzip-compressed tar of the workspace.
	Data []byte

	// Files is the number of regular files written into the archive.
	Files int

	// IgnoreFile is the basename of the ignore file that was applied, or
	// the empty string when no ignore file was present.
	IgnoreFile string
}

// Pack walks dir and returns a gzip-compressed tar of its regular files
// and directories, honoring the root `.lennyignore` (or `.gitignore`)
// patterns and always excluding the `.git` directory. Symlinks and other
// non-regular entries are skipped. The archive paths are workspace
// relative (no leading directory component), so the gateway extracts them
// under an `uploadArchive` source's pathPrefix.
//
// spec: §26.2 lines 95-114; §14 uploadArchive.
func Pack(dir string) (*Result, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("workspacepack: stat %q: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspacepack: %q is not a directory", dir)
	}

	matcher, ignoreName := loadIgnore(dir)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	res := &Result{IgnoreFile: ignoreName}

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if d.Name() == vcsMetadataDir {
				return filepath.SkipDir
			}
			if matcher.Match(rel, true) {
				return filepath.SkipDir
			}
			return writeDirHeader(tw, path, rel)
		}

		// Skip symlinks and other non-regular entries: the gateway's
		// archive extractor validates symlink targets and a non-regular
		// entry would abort extraction, so the packer omits them.
		if !d.Type().IsRegular() {
			return nil
		}
		if matcher.Match(rel, false) {
			return nil
		}
		if err := writeFile(tw, path, rel); err != nil {
			return err
		}
		res.Files++
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("workspacepack: walk %q: %w", dir, walkErr)
	}

	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("workspacepack: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("workspacepack: close gzip: %w", err)
	}
	res.Data = buf.Bytes()
	return res, nil
}

// loadIgnore reads the highest-precedence ignore file present at the
// workspace root and returns the compiled matcher and the file's basename.
// When no ignore file is present it returns an empty matcher.
func loadIgnore(dir string) (*ignoreMatcher, string) {
	for _, name := range IgnoreFileNames {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		return newIgnoreMatcher(string(content)), name
	}
	return newIgnoreMatcher(""), ""
}

// writeDirHeader writes a directory entry so empty directories survive the
// round-trip through the archive.
func writeDirHeader(tw *tar.Writer, path, rel string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	sanitizeHeader(hdr)
	hdr.Name = rel + "/"
	return tw.WriteHeader(hdr)
}

// writeFile writes one regular file's header and content into the tar.
func writeFile(tw *tar.Writer, path, rel string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	sanitizeHeader(hdr)
	hdr.Name = rel
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

// sanitizeHeader strips host-specific identity from a tar header so the
// archive does not leak the packing user's uid/gid or account names.
func sanitizeHeader(hdr *tar.Header) {
	hdr.Uid = 0
	hdr.Gid = 0
	hdr.Uname = ""
	hdr.Gname = ""
}
