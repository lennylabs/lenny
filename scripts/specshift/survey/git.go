// SPDX-License-Identifier: MIT

package survey

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// repo is the git repository the commands read.
type repo struct {
	root string
}

// git runs a git command in the repository and returns its standard
// output.
func (r repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// gitBytes runs a git command and returns its raw standard output.
func (r repo) gitBytes(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// lines splits command output into its non-empty lines.
func lines(out string) []string {
	var res []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			res = append(res, l)
		}
	}
	return res
}

// mergeBaseRefs are the integration refs a branch is compared with, in
// the order they are tried.
var mergeBaseRefs = []string{"main", "origin/main"}

// mergeBase returns the merge base of HEAD with the first integration ref
// that has one, and the ref it used.
func (r repo) mergeBase(ctx context.Context) (string, string, error) {
	for _, ref := range mergeBaseRefs {
		if _, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
			continue
		}
		out, err := r.git(ctx, "merge-base", "HEAD", ref)
		if err != nil {
			continue
		}
		if base := strings.TrimSpace(out); base != "" {
			return base, ref, nil
		}
	}
	return "", "", fmt.Errorf("HEAD has no merge base with %s; pass -base", strings.Join(mergeBaseRefs, " or "))
}

// commit is one commit with its short hash, date, and subject.
type commit struct {
	Hash, Short, Date, Subject string
}

// commitFormat is the log format commits are read with: the fields
// separated by the unit separator.
const commitFormat = "--format=%H%x1f%h%x1f%ad%x1f%s"

// parseCommits reads log output written with commitFormat.
func parseCommits(out string) []commit {
	var res []commit
	for _, l := range strings.Split(out, "\n") {
		parts := strings.Split(l, "\x1f")
		if len(parts) != 4 {
			continue
		}
		res = append(res, commit{Hash: parts[0], Short: parts[1], Date: parts[2], Subject: parts[3]})
	}
	return res
}

// resolveCommit returns the full hash of the commit a user-given
// revision names. A revision that starts with "-" is refused before git
// sees it, because git would read it as an option (such as --output=,
// which writes a file). The revision is passed after --end-of-options to
// rev-parse --verify, so only the verified hash reaches any later git
// command.
func (r repo) resolveCommit(ctx context.Context, rev string) (string, error) {
	if rev == "" {
		return "", errors.New("the revision is empty")
	}
	if strings.HasPrefix(rev, "-") {
		return "", fmt.Errorf("revision %q starts with \"-\"; a revision names a commit, never an option", rev)
	}
	out, err := r.git(ctx, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%s names no commit", rev)
	}
	hash := strings.TrimSpace(out)
	if !hashExpr.MatchString(hash) {
		return "", fmt.Errorf("%s names no commit", rev)
	}
	return hash, nil
}

// hashExpr matches a full object name as rev-parse prints it.
var hashExpr = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// describe returns the commit a revision names, or an error. The revision
// is resolved to a full hash first, so the log command never sees the
// user-given text.
func (r repo) describe(ctx context.Context, rev string) (commit, error) {
	hash, err := r.resolveCommit(ctx, rev)
	if err != nil {
		return commit{}, err
	}
	out, err := r.git(ctx, "log", "-1", "--date=short", commitFormat, hash, "--")
	if err != nil {
		return commit{}, err
	}
	cs := parseCommits(out)
	if len(cs) == 0 {
		return commit{}, fmt.Errorf("%s names no commit", rev)
	}
	return cs[0], nil
}

// dateExpr matches a calendar date given in place of a commit.
var dateExpr = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// resolveSince returns the commit a `since` argument names: a commit
// directly, or for a date the last commit on the first-parent line of
// HEAD made before that date. A date before the first commit resolves to
// no commit, and the caller compares with an empty tree.
func (r repo) resolveSince(ctx context.Context, arg string) (commit, bool, error) {
	if dateExpr.MatchString(arg) {
		out, err := r.git(ctx, "rev-list", "-1", "--first-parent", "--before="+arg+" 00:00:00", "HEAD")
		if err != nil {
			return commit{}, false, err
		}
		hash := strings.TrimSpace(out)
		if hash == "" {
			return commit{}, false, nil
		}
		c, err := r.describe(ctx, hash)
		return c, err == nil, err
	}
	c, err := r.describe(ctx, arg)
	if err != nil {
		return commit{}, false, fmt.Errorf("%s is neither a date (YYYY-MM-DD) nor a commit: %w", arg, err)
	}
	return c, true, nil
}

// worktreeFiles lists the files of the working tree under the given
// pathspecs: the tracked files that exist on disk and the untracked files
// that are not ignored.
func (r repo) worktreeFiles(ctx context.Context, pathspecs ...string) ([]string, error) {
	args := append([]string{"ls-files", "-z", "--cached", "--others", "--exclude-standard", "--"}, pathspecs...)
	out, err := r.gitBytes(ctx, args...)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var res []string
	for _, raw := range bytes.Split(out, []byte{0}) {
		p := string(raw)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if info, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(p))); err == nil && !info.IsDir() {
			res = append(res, p)
		}
	}
	return res, nil
}

// changedFiles lists the files the working tree adds, modifies, or
// deletes relative to a commit under the given pathspecs, with the
// untracked files under them. An empty base compares with an empty tree.
func (r repo) changedFiles(ctx context.Context, base string, pathspecs ...string) ([]string, error) {
	seen := map[string]bool{}
	var res []string
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			res = append(res, p)
		}
	}
	if base == "" {
		files, err := r.worktreeFiles(ctx, pathspecs...)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			add(f)
		}
		return res, nil
	}
	args := append([]string{"diff", "--name-only", "-z", "--no-renames", base, "--"}, pathspecs...)
	out, err := r.gitBytes(ctx, args...)
	if err != nil {
		return nil, err
	}
	for _, raw := range bytes.Split(out, []byte{0}) {
		add(string(raw))
	}
	untracked, err := r.gitBytes(ctx, append([]string{"ls-files", "-z", "--others", "--exclude-standard", "--"}, pathspecs...)...)
	if err != nil {
		return nil, err
	}
	for _, raw := range bytes.Split(untracked, []byte{0}) {
		add(string(raw))
	}
	return res, nil
}

// show returns a file's content at a commit, and false when the commit
// has no such file.
func (r repo) show(ctx context.Context, rev, p string) (string, bool) {
	out, err := r.gitBytes(ctx, "show", rev+":"+p)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// extract writes the files under dir at a commit into a new directory
// below parent and returns it. A commit without dir yields an empty
// directory.
func (r repo) extract(ctx context.Context, rev, dir, parent string) (string, error) {
	dest, err := os.MkdirTemp(parent, "rev-")
	if err != nil {
		return "", fmt.Errorf("create a directory for %s at %s: %w", dir, rev, err)
	}
	listed, err := r.git(ctx, "ls-tree", "--name-only", rev, "--", dir)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(listed) == "" {
		return dest, nil
	}
	archive, err := r.gitBytes(ctx, "archive", "--format=tar", rev, "--", dir)
	if err != nil {
		return "", err
	}
	tr := tar.NewReader(bytes.NewReader(archive))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read the archive of %s at %s: %w", dir, rev, err)
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return "", fmt.Errorf("the archive of %s at %s holds the path %s outside its root", dir, rev, h.Name)
		}
		target := filepath.Join(dest, name)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return "", fmt.Errorf("read %s from the archive at %s: %w", h.Name, rev, err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return "", err
		}
	}
	return dest, nil
}

// branch returns the name of the checked-out branch, or "HEAD" when
// detached.
func (r repo) branch(ctx context.Context) string {
	out, err := r.git(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(out)
}
