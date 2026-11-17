// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// RegistryPath is the repository path of the committed registry.
const RegistryPath = "spec/registry.json"

// specDir is the directory the specification lives in, with its
// trailing slash.
const specDir = "spec/"

// numberedExpr matches a numbered file of the specification, such as
// spec/04_system-components.md. The numbered files carry no front matter
// and keep the link rules of the existing citation gates.
var numberedExpr = regexp.MustCompile(`^spec/\d{2}_[^/]*\.md$`)

// isNumbered reports whether a repository path is a numbered file.
func isNumbered(p string) bool { return numberedExpr.MatchString(p) }

// excludedPrefixes and excludedFiles are the parts of the tracked tree the
// reference pass neither checks nor rewrites. The archives and the frozen
// gap records keep the paths they were written with, and a testdata
// directory holds fixtures that present broken links on purpose.
var (
	excludedPrefixes = []string{"changes/archive/", "proposals/"}
	excludedFiles    = map[string]bool{"BUILD-GAPS.md": true, "TEST-GAPS.md": true, "CODE-REVIEW.md": true}
)

// inDomain reports whether a repository path is inside the domain the
// check reads and the fix writes.
func inDomain(p string) bool {
	if excludedFiles[p] {
		return false
	}
	for _, prefix := range excludedPrefixes {
		if strings.HasPrefix(p, prefix) {
			return false
		}
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if seg == "testdata" {
			return false
		}
	}
	return true
}

// isMarkdown reports whether a path is a Markdown document.
func isMarkdown(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".md" || ext == ".markdown"
}

// Git runs a git command in the repository and returns its standard
// output. Lookup reads the frozen reference through it, and a test
// substitutes it.
type Git func(ctx context.Context, args ...string) ([]byte, error)

// Tree is the repository the pass reads and writes.
type Tree struct {
	// Root is the repository root on disk.
	Root string
	// List lists the tracked paths. A listed path that no longer exists
	// on disk, such as the old path of an unstaged move, is dropped.
	List scope.Lister
	// Git runs git in the repository.
	Git Git

	files map[string]bool
}

// NewTree returns the tracked tree at root, listed by git.
func NewTree(root string) *Tree {
	return &Tree{Root: root, List: scope.GitLister(root), Git: gitIn(root)}
}

// gitIn returns a Git that runs in dir.
func gitIn(dir string) Git {
	return func(ctx context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return out, nil
	}
}

// load lists the tree once.
func (t *Tree) load(ctx context.Context) error {
	if t.files != nil {
		return nil
	}
	listed, err := t.List(ctx)
	if err != nil {
		return fmt.Errorf("list the tracked tree: %w", err)
	}
	t.files = map[string]bool{}
	for _, p := range listed {
		if _, err := os.Stat(t.abs(p)); err == nil {
			t.files[p] = true
		}
	}
	return nil
}

// Files returns the listed paths that exist, sorted.
func (t *Tree) Files(ctx context.Context) ([]string, error) {
	if err := t.load(ctx); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(t.files))
	for p := range t.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// exists reports whether a repository path is a listed file or a
// directory on disk.
func (t *Tree) exists(p string) bool {
	if t.files[p] {
		return true
	}
	info, err := os.Stat(t.abs(p))
	return err == nil && info.IsDir()
}

// abs returns the path on disk of a repository path.
func (t *Tree) abs(p string) string { return filepath.Join(t.Root, filepath.FromSlash(p)) }

// Read returns the content of a repository path.
func (t *Tree) Read(p string) (string, error) {
	b, err := scope.ReadChecked(t.Root, p)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p, err)
	}
	return string(b), nil
}

// Write replaces the content of a repository path, keeping its mode.
func (t *Tree) Write(p, content string) error {
	return scope.DirWriter(t.Root)(p, []byte(content))
}

// resolve returns the repository path a relative link path written in
// file `from` reaches, and false when it climbs out of the repository. An
// empty path is a same-page link and reaches `from` itself.
func resolve(from, linkPath string) (string, bool) {
	if linkPath == "" {
		return from, true
	}
	p := path.Clean(path.Join(path.Dir(from), decodePath(linkPath)))
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", false
	}
	return p, true
}

// relativeTo returns the link path that reaches target from a file in
// directory dir.
func relativeTo(dir, target string) string {
	rel, err := filepath.Rel(filepath.FromSlash(dir), filepath.FromSlash(target))
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

// underSpec reports whether a repository path lies in the specification.
func underSpec(p string) bool { return p == "spec" || strings.HasPrefix(p, specDir) }

// newTreeTarget reports whether a resolved link target is governed by the
// reference pass: a path under spec/ other than a numbered file.
func newTreeTarget(p string) bool { return underSpec(p) && !isNumbered(p) }
