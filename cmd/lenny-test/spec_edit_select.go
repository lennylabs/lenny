// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"fmt"
	"go/build/constraint"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/ref"
	"github.com/lennylabs/lenny/scripts/specshift/survey"
)

// This file holds the selection from specification edits (change 0116,
// CS-5). A changed numbered file selects, for each hunk, the tests that
// cite the numbered heading enclosing it or a subsection of it, and through
// a successor stub, the tests of the identifiers the stub lists. A changed
// page of the new tree selects, for each requirement, operation, or
// definition block whose text the diff changes, the tests that cite it
// directly or through a stub, and treats the packages whose code cites it
// as changed. An edit outside every identifier of a page, such as its
// introduction, selects the unit tests of the packages whose doc.go names
// the page's code. The selection reads the annotation index of the working
// tree. The harness is contributor tooling, so this file carries no
// specification annotation.

// diffSides names the two revisions a diff compares: the base commit and
// the head commit, with an empty head standing for the working tree.
type diffSides struct {
	base, head string
}

// resolveDiffSides reads the revisions behind a `--since` value: a plain
// ref compares its merge base with HEAD, `A..B` compares A with B, and
// `A...B` compares their merge base with B. An empty ref compares HEAD with
// the working tree, as `--changed` does. Every named revision is resolved
// to a commit hash first, so a value that git would read as an option, such
// as one that begins with `-`, fails rather than reaching a later git
// command as a flag.
func resolveDiffSides(ctx context.Context, root, since string) (diffSides, error) {
	var a, b string
	threeDot := false
	switch {
	case since == "":
		return diffSides{base: "HEAD"}, nil
	case strings.Contains(since, "..."):
		a, b, _ = strings.Cut(since, "...")
		threeDot = true
	case strings.Contains(since, ".."):
		a, b, _ = strings.Cut(since, "..")
	default:
		a, b, threeDot = since, "HEAD", true
	}
	base, err := commitOf(ctx, root, orHEAD(a))
	if err != nil {
		return diffSides{}, err
	}
	head, err := commitOf(ctx, root, orHEAD(b))
	if err != nil {
		return diffSides{}, err
	}
	if threeDot {
		if base, err = gitOutput(ctx, root, "merge-base", base, head); err != nil {
			return diffSides{}, err
		}
	}
	return diffSides{base: base, head: head}, nil
}

// commitOf resolves a revision to its commit hash, refusing a revision that
// begins with `-`.
func commitOf(ctx context.Context, root, rev string) (string, error) {
	if strings.HasPrefix(rev, "-") {
		return "", fmt.Errorf("revision %q begins with '-'", rev)
	}
	return gitOutput(ctx, root, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

func orHEAD(rev string) string {
	if rev == "" {
		return "HEAD"
	}
	return rev
}

// gitOutput runs git in root and returns its trimmed standard output.
func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// readAt returns a file's content at a revision, or in the working tree
// when rev is empty. A file absent at the revision reads as empty.
func readAt(ctx context.Context, root, rev, p string) string {
	if rev == "" {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			return ""
		}
		return string(b)
	}
	cmd := exec.CommandContext(ctx, "git", "show", rev+":"+p)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// hunk is one hunk of a unified diff with no context lines: the old and the
// new line ranges. A count of zero is an insertion point rather than a
// range.
type hunk struct {
	oldStart, oldCount, newStart, newCount int
}

// hunkHeaderExpr reads a hunk header: `@@ -a[,b] +c[,d] @@`.
var hunkHeaderExpr = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseHunks reads `git diff -U0` output into the hunks of each file. The
// body of each hunk is skipped by its line counts, so a removed or added
// line whose text opens with `++ ` or `-- ` is never read as a file header.
func parseHunks(diff string) map[string][]hunk {
	out := map[string][]hunk{}
	var file, oldFile string
	body := 0
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		l := sc.Text()
		if body > 0 {
			if !strings.HasPrefix(l, "\\") {
				// A "\ No newline at end of file" marker is no body line.
				body--
			}
			continue
		}
		switch {
		case strings.HasPrefix(l, "--- "):
			oldFile = strings.TrimPrefix(strings.TrimPrefix(l, "--- "), "a/")
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
			if file == "/dev/null" {
				file = oldFile
			}
		case strings.HasPrefix(l, "@@ "):
			m := hunkHeaderExpr.FindStringSubmatch(l)
			if m == nil || file == "" {
				continue
			}
			h := hunk{
				oldStart: atoi(m[1]), oldCount: countOf(m[2]),
				newStart: atoi(m[3]), newCount: countOf(m[4]),
			}
			out[file] = append(out[file], h)
			body = h.oldCount + h.newCount
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// countOf reads a hunk count, which is 1 when the header omits it.
func countOf(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}

// specDiffHunks returns the hunks of each changed file under spec/. In the
// working tree it adds each untracked file under spec/ as one hunk that
// adds the whole file.
func specDiffHunks(ctx context.Context, root string, sides diffSides) (map[string][]hunk, error) {
	args := []string{"diff", "-U0", "--no-color", "--no-ext-diff", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", sides.base}
	if sides.head != "" {
		args = append(args, sides.head)
	}
	out, err := gitOutput(ctx, root, append(args, "--", "spec/")...)
	if err != nil {
		return nil, err
	}
	hunks := parseHunks(out)
	if sides.head == "" {
		untracked, err := gitOutput(ctx, root, "ls-files", "--others", "--exclude-standard", "--", "spec/")
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Fields(untracked) {
			n := strings.Count(readAt(ctx, root, "", p), "\n") + 1
			hunks[p] = append(hunks[p], hunk{newStart: 1, newCount: n})
		}
	}
	return hunks, nil
}

// numberedHunkSections returns the numbers of the numbered headings the
// hunks of one numbered file touch: on each side, the heading that encloses
// the first line of a hunk's range and every heading inside the range. The
// old side names the sections lines were removed from; the new side the
// sections lines were added to. A hunk before the first numbered heading
// touches none.
func numberedHunkSections(file, base, head string, hunks []hunk) []string {
	baseHeadings := ref.NumberedHeadings(file, base)
	headHeadings := ref.NumberedHeadings(file, head)
	seen := map[string]bool{}
	var out []string
	add := func(hs []ref.NumberedHeading, start, count int) {
		end := start + count - 1
		if count == 0 {
			// An insertion point: the hunk sits after line start.
			end = start
		}
		for i, h := range hs {
			last := i+1 == len(hs) || hs[i+1].Line > start
			if (h.Line <= start && last) || (h.Line > start && h.Line <= end) {
				if !seen[h.Number] {
					seen[h.Number] = true
					out = append(out, h.Number)
				}
			}
		}
	}
	for _, h := range hunks {
		if h.oldCount > 0 {
			add(baseHeadings, h.oldStart, h.oldCount)
		}
		if h.newCount > 0 || h.oldCount == 0 {
			add(headHeadings, h.newStart, h.newCount)
		}
	}
	return out
}

// sectionTests returns the tests a numbered section selects: through the
// model when the section is a heading of the working tree, which adds the
// tests of the identifiers its stub lists, and otherwise the tests that
// cite the section or a subsection, as for a heading the diff removed.
func (m *specModel) sectionTests(number string) []annotation.TestFunc {
	if tests, ok := m.testsFor("§" + number); ok {
		return tests
	}
	return m.testsCitingSection(number)
}

// itemEdits is what the changed identifiers of the new tree select.
type itemEdits struct {
	ids          []string
	tests        []annotation.TestFunc
	codeDirs     []string
	outsideCodes []string
}

// pageItemEdits reads the changed identifiers of the new tree from a
// comparison of the two snapshots, and the pages whose text the diff
// changes outside their identifiers. contents maps each changed page to its
// base and head content.
func pageItemEdits(m *specModel, sc *survey.SpecChanges, contents map[string][2]string) itemEdits {
	var e itemEdits
	for _, c := range sc.Changes {
		it := c.Item()
		if it == nil || contains(e.ids, it.ID) {
			continue
		}
		e.ids = append(e.ids, it.ID)
		if tests, ok := m.testsFor(it.ID); ok {
			e.tests = append(e.tests, tests...)
		} else {
			// A retired identifier is no longer in the model; the tests
			// that still cite it are the ones the edit concerns.
			e.tests = append(e.tests, m.testsCitingID(it.ID)...)
		}
		for _, s := range m.codeCitingID(it.ID) {
			if s.Kind == annotation.SiteCode && !contains(e.codeDirs, path.Dir(s.Path)) {
				e.codeDirs = append(e.codeDirs, path.Dir(s.Path))
			}
		}
	}
	files := make([]string, 0, len(contents))
	for f := range contents {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		code := pageCode(sc, f)
		if code == "" {
			continue
		}
		c := contents[f]
		if residual(c[0], itemTexts(sc.Before, f)) != residual(c[1], itemTexts(sc.After, f)) {
			e.outsideCodes = append(e.outsideCodes, code)
		}
	}
	return e
}

// pageCode returns the code of a page on either side of the comparison, or
// "" when the file is no page.
func pageCode(sc *survey.SpecChanges, file string) string {
	for _, s := range []*survey.Snapshot{sc.After, sc.Before} {
		if s == nil {
			continue
		}
		if p, ok := s.ByPath[file]; ok {
			return p.Code
		}
	}
	return ""
}

// itemTexts returns the texts of the items a snapshot holds on one page,
// longest first so a text that contains another is removed whole.
func itemTexts(s *survey.Snapshot, file string) []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, it := range s.Items {
		if it.File == file && it.Text != "" {
			out = append(out, it.Text)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// residual returns a page's words outside its items: the text with
// trailing space trimmed from every line, which is the normalization the
// snapshot applies to an item's text, then each item's text removed, then
// every run of white space collapsed. Two residuals differ when the diff
// changes text outside every identifier; an added or retired item leaves
// only blank lines behind, which the collapse ignores.
func residual(content string, texts []string) string {
	ls := strings.Split(content, "\n")
	for i, l := range ls {
		ls[i] = strings.TrimRight(l, " \t\r")
	}
	out := strings.Join(ls, "\n")
	for _, t := range texts {
		out = strings.Replace(out, t, "", 1)
	}
	return strings.Join(strings.Fields(out), " ")
}

// packageDocDirs returns the directories whose package doc comment names a
// page code.
func (m *specModel) packageDocDirs(code string) []string {
	var out []string
	for _, s := range m.idx.Sites(code) {
		if s.Kind == annotation.SitePackage && !contains(out, path.Dir(s.Path)) {
			out = append(out, path.Dir(s.Path))
		}
	}
	sort.Strings(out)
	return out
}

// selectSpecEdits resolves the specification edits of a diff (CS-5). It
// reads nothing when no changed path is under spec/.
func selectSpecEdits(ctx context.Context, root, since string, paths []string) specEdits {
	var specPaths []string
	for _, p := range paths {
		if strings.HasPrefix(p, "spec/") && strings.HasSuffix(p, ".md") {
			specPaths = append(specPaths, p)
		}
	}
	if len(specPaths) == 0 {
		return specEdits{}
	}
	fail := func(err error) specEdits {
		return specEdits{failed: true, notes: []string{fmt.Sprintf("the specification edits are unreadable (%v); the unit, component, contract, and integration tiers run whole", err)}}
	}
	sides, err := resolveDiffSides(ctx, root, since)
	if err != nil {
		return fail(err)
	}
	m, err := loadSpecModel(ctx, root)
	if err != nil {
		return fail(err)
	}
	hunks, err := specDiffHunks(ctx, root, sides)
	if err != nil {
		return fail(err)
	}
	var e specEdits
	newTree := map[string][2]string{}
	for _, p := range specPaths {
		base, head := readAt(ctx, root, sides.base, p), readAt(ctx, root, sides.head, p)
		if ref.IsNumberedFile(p) {
			for _, n := range numberedHunkSections(p, base, head, hunks[p]) {
				e.tests = append(e.tests, m.sectionTests(n)...)
			}
			continue
		}
		newTree[p] = [2]string{base, head}
	}
	if len(newTree) > 0 {
		sc, err := survey.CompareSpecRevisions(ctx, root, sides.base, sides.head)
		if err != nil {
			e.failed = true
			e.notes = append(e.notes, fmt.Sprintf("the edits to the new specification tree are unreadable (%v); the unit, component, contract, and integration tiers run whole", err))
		} else {
			ie := pageItemEdits(m, sc, newTree)
			e.tests = append(e.tests, ie.tests...)
			e.changedDirs = ie.codeDirs
			for _, code := range ie.outsideCodes {
				e.unitDirs = append(e.unitDirs, m.packageDocDirs(code)...)
			}
		}
	}
	e.tests = dedupeTests(e.tests)
	e.tiers = taggedTestTiers(e.tests, func(p string) string { return readAt(ctx, root, "", p) })
	return e
}

// taggedTestTiers returns, for each test outside the tier directories
// whose file carries a tier's build tag, that tier, keyed by the test's
// `path::Name`. Such a test runs only under its tag, so a by-name run in
// the unit tier would match nothing.
func taggedTestTiers(tests []annotation.TestFunc, read func(path string) string) map[string]string {
	out := map[string]string{}
	for _, t := range tests {
		if tierOfDir(t.Dir) != "" {
			continue
		}
		if tier := fileTier(read(t.Path)); tier != "" {
			out[t.String()] = tier
		}
	}
	return out
}

// fileTier returns the tier whose build tag a Go file's constraint needs:
// the first tier whose tag alone satisfies the constraint, when the
// constraint does not hold with no tag set. It returns "" for an untagged
// file.
func fileTier(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "package ") {
			return ""
		}
		if !constraint.IsGoBuild(line) {
			continue
		}
		expr, err := constraint.Parse(line)
		if err != nil || expr.Eval(func(string) bool { return false }) {
			return ""
		}
		for _, t := range allTiers() {
			tag := tierBuildTag(t)
			if tag != "" && expr.Eval(func(s string) bool { return s == tag }) {
				return t
			}
		}
		return ""
	}
	return ""
}
