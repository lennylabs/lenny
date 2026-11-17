// SPDX-License-Identifier: MIT

package scope

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CheckedPath returns the path on disk of a repo-relative, slash-separated
// target under root, after refusing a target that a write or a read of a
// generated file must not follow out of the repository:
//
//   - a target that is not a local path, such as an absolute path or one
//     that climbs out of root with "..";
//   - a target that is itself a symbolic link;
//   - a target whose parent directory, or the nearest ancestor of it that
//     exists, resolves with filepath.EvalSymlinks outside root.
//
// A target that does not exist yet passes, so a writer may create it. The
// passes, the generators, and the digest write through WriteChecked, and
// they read a file they generate through ReadChecked, so a symbolic link
// planted in the tree cannot redirect either one to a file elsewhere.
func CheckedPath(root, target string) (string, error) {
	native := filepath.FromSlash(target)
	if !filepath.IsLocal(native) {
		return "", fmt.Errorf("refuse %s: the path is not inside the repository", target)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve the repository root %s: %w", root, err)
	}
	full := filepath.Join(root, native)
	if info, err := os.Lstat(full); err == nil {
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("refuse %s: the path is a symbolic link", target)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect %s: %w", target, err)
	}
	dir := filepath.Dir(full)
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if !within(realRoot, resolved) {
				return "", fmt.Errorf("refuse %s: its directory resolves to %s, outside the repository", target, resolved)
			}
			return full, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("resolve the directory of %s: %w", target, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("resolve the directory of %s: no ancestor exists", target)
		}
		dir = parent
	}
}

// within reports whether path p lies in or under directory dir. Both are
// resolved, absolute paths.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// WriteChecked writes content to a repo-relative target under root after
// CheckedPath accepts it, creating the target's missing directories. An
// existing file keeps its mode; a new file is written with mode.
func WriteChecked(root, target string, content []byte, mode os.FileMode) error {
	return writeChecked(root, target, content, mode, true)
}

// writeChecked is WriteChecked with the creation of missing directories
// optional: DirWriter writes only into directories that exist.
func writeChecked(root, target string, content []byte, mode os.FileMode, mkdir bool) error {
	full, err := CheckedPath(root, target)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(full); err == nil {
		mode = info.Mode().Perm()
	}
	if mkdir {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("create the folder of %s: %w", target, err)
		}
		// The directories just created are real directories, so the
		// check above still holds; check once more in case a parent was
		// replaced by a link while they were created.
		if _, err := CheckedPath(root, target); err != nil {
			return err
		}
	}
	if err := os.WriteFile(full, content, mode); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	return nil
}

// ReadChecked reads a repo-relative target under root after CheckedPath
// accepts it. A target that does not exist returns an error that wraps
// fs.ErrNotExist.
func ReadChecked(root, target string) ([]byte, error) {
	full, err := CheckedPath(root, target)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}
