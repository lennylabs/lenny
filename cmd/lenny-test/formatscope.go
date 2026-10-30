// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
)

// The formatter checks of the static tier (`gofumpt -l .` and
// `goimports -l .`) walk the directory tree from the repository root.
// Neither tool consults git, so the walk also reaches trees that are not
// part of the checkout under test:
//
//   - the nested git worktrees under .claude/worktrees/, where other
//     agents edit the same files concurrently and leave them unformatted
//     until they run the formatter themselves;
//   - git-ignored scratch trees such as scratchpad/, which hold spikes and
//     copies of source that nobody formats.
//
// A run in the main checkout therefore failed on another worktree's
// in-progress edit, and a rerun after that agent had formatted its files
// passed, so the verdict depended on state outside the tree the gate
// claims to judge. scopeFormatterOutput restricts the formatter output to
// the files git tracks or would track, which is the tree a commit from
// this checkout can contain.

// repoGoFiles returns the slash-separated, root-relative paths of the Go
// files git tracks under root, plus the untracked Go files .gitignore does
// not exclude, so a new source file that is not yet committed is still
// judged. git does not descend into a nested worktree or repository, so
// none of its files are listed. It returns nil when git is unavailable or
// root is not a git checkout; the caller then keeps the formatter output
// whole, so the check stays as strict as an unscoped walk.
func repoGoFiles(root string) map[string]bool {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	files := map[string]bool{}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			files[filepath.ToSlash(filepath.Clean(string(p)))] = true
		}
	}
	return files
}

// scopeToRepoFiles drops every line of a formatter's output whose leading
// path names a Go file outside files, and returns the remaining lines
// joined by newlines. A line is either a bare path (the -l listing) or a
// "path:line:col: message" parse error; both carry the file in the
// leading path. A line whose leading token is not a .go path (a tool
// error that names no source file) is kept, because dropping it could
// hide a failure the check must report. A nil files keeps every line.
func scopeToRepoFiles(out string, files map[string]bool) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if files != nil {
			path := trimmed
			if i := strings.IndexByte(path, ':'); i >= 0 {
				path = path[:i]
			}
			path = filepath.ToSlash(filepath.Clean(path))
			if strings.HasSuffix(path, ".go") && !files[path] {
				continue
			}
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "\n")
}

// scopeFormatterOutput restricts a formatter's output, produced with root
// as its working directory, to the Go files of the checkout at root.
func scopeFormatterOutput(out, root string) string {
	return scopeToRepoFiles(out, repoGoFiles(root))
}
