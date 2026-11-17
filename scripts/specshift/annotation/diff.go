// SPDX-License-Identifier: MIT

package annotation

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// BaseRefs are the integration refs a branch is compared with, in the
// order they are tried.
var BaseRefs = []string{"main", "origin/main"}

// ErrNoBase reports that no integration ref has a merge base with HEAD,
// such as in a shallow clone, so there is no branch diff to lint.
var ErrNoBase = errors.New("no merge base with " + strings.Join(BaseRefs, " or "))

// MergeBase returns the merge base of HEAD with the first integration ref
// that has one, and the ref it used.
func MergeBase(ctx context.Context, root string) (base, ref string, err error) {
	for _, ref := range BaseRefs {
		if _, err := git(ctx, root, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
			continue
		}
		out, err := git(ctx, root, "merge-base", "HEAD", ref)
		if err != nil {
			continue
		}
		if base := strings.TrimSpace(out); base != "" {
			return base, ref, nil
		}
	}
	return "", "", ErrNoBase
}

// ChangedGoLines returns, for each Go file the working tree adds or
// modifies relative to base, the line numbers it adds or changes. The
// comparison is with the working tree, so lines committed on the branch
// and lines not yet committed are both included, and so is every line
// of a Go file that is not yet tracked. A deleted file has no lines to
// lint and is left out.
func ChangedGoLines(ctx context.Context, root, base string) (map[string]map[int]bool, error) {
	out, err := git(ctx, root, "diff", "--no-color", "--no-ext-diff", "--unified=0",
		"--diff-filter=AMR", "--find-renames", base, "--", "*.go")
	if err != nil {
		return nil, err
	}
	files, err := ParseUnifiedDiff(out)
	if err != nil {
		return nil, err
	}
	untracked, err := git(ctx, root, "ls-files", "--others", "--exclude-standard", "-z", "--", "*.go")
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Split(untracked, "\x00") {
		if p == "" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return nil, fmt.Errorf("read untracked %s: %w", p, err)
		}
		lines := map[int]bool{}
		for n := 1; n <= bytes.Count(src, []byte("\n"))+1; n++ {
			lines[n] = true
		}
		files[p] = lines
	}
	return files, nil
}

// hunkHeader matches the new-file range of a unified diff hunk header.
var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ParseUnifiedDiff reads a zero-context unified diff into the added or
// changed line numbers of each new file path.
func ParseUnifiedDiff(diff string) (map[string]map[int]bool, error) {
	files := map[string]map[int]bool{}
	var current string
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			target := strings.TrimPrefix(line, "+++ ")
			if target == "/dev/null" {
				current = ""
				continue
			}
			current = strings.TrimPrefix(target, "b/")
		case strings.HasPrefix(line, "@@"):
			if current == "" {
				continue
			}
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				return nil, fmt.Errorf("unreadable hunk header in the diff of %s: %q", current, line)
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			if files[current] == nil {
				files[current] = map[int]bool{}
			}
			for n := start; n < start+count; n++ {
				files[current][n] = true
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read the diff: %w", err)
	}
	return files, nil
}

// git runs one git command in root and returns its standard output.
func git(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
