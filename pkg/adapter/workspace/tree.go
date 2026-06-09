// SPDX-License-Identifier: MIT

package workspace

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// NamedRoot pairs an archive namespace prefix with a filesystem root.
// ArchiveTree records each root's entries under "<Prefix>/<rel>" so a
// single checkpoint archive can carry more than one tree, and
// ExtractTree routes each entry back to the root that owns its prefix.
//
// spec: §7.3 lines 408-409 — a session resume replays the workspace
// checkpoint (step e) and restores the session file to its expected
// path (step f). The workspace (`/workspace/current`, §6.4 line 407)
// and the session-file tmpfs (`/sessions`, §6.4 line 380 / §6.1 line 13)
// are distinct mounts, so the checkpoint bundles both under distinct
// prefixes rather than archiving a single tree.
type NamedRoot struct {
	// Prefix is the archive namespace for this root. It must be a single
	// non-empty path segment (no slash).
	Prefix string
	// Root is the filesystem directory archived under Prefix. An empty
	// Root, or one that does not exist on disk, contributes no entries —
	// the `/sessions` tmpfs is legitimately absent or empty for a
	// runtime that keeps no session file.
	Root string
}

// Reserved checkpoint-bundle prefixes. WorkspacePrefix carries the
// session workspace (`/workspace/current`); SessionsPrefix carries the
// `/sessions` session-file tmpfs the §7.3 step-f restore replays.
const (
	WorkspacePrefix = "workspace"
	SessionsPrefix  = "sessions"
)

// ArchiveTree writes a gzip-compressed tar bundling several roots under
// their prefixes to w and returns the number of compressed bytes
// written. It is the multi-root counterpart of Archive: the §4.4
// checkpoint snapshots the session workspace and the `/sessions`
// session-file tmpfs in one archive so a §7.3 resume can replay both.
//
// Entry names are "<Prefix>/<rel>", slash-separated and relative to
// each root. Symlinks are recorded verbatim and never followed, exactly
// as Archive does. A root whose Root is empty or does not exist on disk
// is skipped without error.
func ArchiveTree(roots []NamedRoot, w io.Writer) (int64, error) {
	counter := &countingWriter{w: w}
	gz := gzip.NewWriter(counter)
	tw := tar.NewWriter(gz)

	for _, nr := range roots {
		if err := validatePrefix(nr.Prefix); err != nil {
			return counter.n, err
		}
		if nr.Root == "" {
			continue
		}
		if _, err := os.Stat(nr.Root); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return counter.n, fmt.Errorf("workspace: stat %s root %q: %w", nr.Prefix, nr.Root, err)
		}
		if err := archiveRoot(tw, nr.Prefix, nr.Root); err != nil {
			return counter.n, err
		}
	}

	if err := tw.Close(); err != nil {
		return counter.n, err
	}
	if err := gz.Close(); err != nil {
		return counter.n, err
	}
	return counter.n, nil
}

// archiveRoot walks one root and writes its entries under prefix into
// tw. It mirrors Archive's per-entry handling (relative names, symlinks
// recorded verbatim, only regular files carry content).
func archiveRoot(tw *tar.Writer, prefix, root string) error {
	return filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // the root itself is implicit under the prefix.
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
		hdr.Name = prefix + "/" + filepath.ToSlash(rel)
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
}

// ExtractTree restores a bundle produced by ArchiveTree. Each entry is
// routed to the root whose prefix it carries; the prefix is stripped and
// the remainder is extracted with the same containment guarantees as
// Extract (no `..`/absolute escape, symlink targets confined to the
// destination root, setuid/setgid dropped). An entry whose leading path
// segment matches no configured root is skipped, so a newer archive that
// carries an unrecognised tree restores forward-compatibly. ExtractTree
// returns the total uncompressed regular-file bytes written across all
// roots.
func ExtractTree(roots []NamedRoot, r io.Reader) (int64, error) {
	rootByPrefix := make(map[string]string, len(roots))
	for _, nr := range roots {
		if err := validatePrefix(nr.Prefix); err != nil {
			return 0, err
		}
		if nr.Root != "" {
			rootByPrefix[nr.Prefix] = filepath.Clean(nr.Root)
		}
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("workspace: open archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var written int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return written, nil
		}
		if err != nil {
			return written, fmt.Errorf("workspace: read archive: %w", err)
		}
		prefix, rest := splitPrefix(hdr.Name)
		rootClean, ok := rootByPrefix[prefix]
		if !ok || rest == "" {
			// Unknown tree (forward-compat) or the prefix dir itself.
			continue
		}
		dest, err := resolvePath(rootClean, rest)
		if err != nil {
			return written, fmt.Errorf("workspace: archive entry %q: %w", hdr.Name, err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return written, fmt.Errorf("workspace: create directory %q: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			n, err := extractRegular(dest, tr, os.FileMode(hdr.Mode).Perm(), maxExtractBytes-written)
			written += n
			if err != nil {
				return written, fmt.Errorf("workspace: archive entry %q: %w", hdr.Name, err)
			}
		case tar.TypeSymlink:
			if err := extractSymlink(rootClean, dest, hdr.Linkname); err != nil {
				return written, fmt.Errorf("workspace: archive entry %q: %w", hdr.Name, err)
			}
		default:
			return written, fmt.Errorf("workspace: archive entry %q has unsupported type %d", hdr.Name, hdr.Typeflag)
		}
	}
}

// splitPrefix splits an archive entry name into its leading namespace
// segment and the remainder. "workspace/foo/bar" -> ("workspace",
// "foo/bar"); "workspace/" -> ("workspace", ""); "workspace" ->
// ("workspace", "").
func splitPrefix(name string) (prefix, rest string) {
	name = strings.TrimPrefix(name, "/")
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[:i], name[i+1:]
	}
	return name, ""
}

// validatePrefix rejects a prefix that is empty or carries a slash; a
// multi-segment prefix would break the splitPrefix routing.
func validatePrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("workspace: archive prefix is empty")
	}
	if strings.ContainsRune(prefix, '/') {
		return fmt.Errorf("workspace: archive prefix %q must be a single path segment", prefix)
	}
	return nil
}
