// SPDX-License-Identifier: MIT

package survey

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// changesDir is the folder of the change records.
const changesDir = "changes"

// stepArgExpr matches a step, or a range of steps of one change that share
// a branch: `0115.S8` or `0113.S1-S3`.
var stepArgExpr = regexp.MustCompile(`^(\d{4})\.S(\d+)(?:-S(\d+))?$`)

// stepHeadExpr matches a step heading in steps.md.
var stepHeadExpr = regexp.MustCompile(`(?m)^### (\d{4}\.S\d+) (.+)$`)

// DigestOptions are the inputs of one digest.
type DigestOptions struct {
	// Step is the step or range of steps, such as 0115.S8 or 0113.S1-S3.
	Step string
	// Base is the commit to compare with; empty means the merge base of
	// HEAD with main.
	Base string
}

// stepRef is one step the digest covers.
type stepRef struct {
	ID, Title string
}

// changeFolder returns the folder of the change a number names, relative
// to the repository root. Archived changes are not digested.
func changeFolder(root, number string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, changesDir))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", changesDir, err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), number+"-") {
			if _, err := os.Stat(filepath.Join(root, changesDir, e.Name(), "change.md")); err == nil {
				return changesDir + "/" + e.Name(), nil
			}
		}
	}
	return "", fmt.Errorf("no change folder numbered %s under %s/", number, changesDir)
}

// digestSteps returns the steps a step argument covers, with their titles
// from the change's steps.md.
func digestSteps(root, folder, arg string) ([]stepRef, error) {
	m := stepArgExpr.FindStringSubmatch(arg)
	first, last := atoi(m[2]), atoi(m[2])
	if m[3] != "" {
		last = atoi(m[3])
	}
	if last < first {
		return nil, fmt.Errorf("step range %s ends before it starts", arg)
	}
	titles := map[string]string{}
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(folder), "steps.md")); err == nil {
		for _, h := range stepHeadExpr.FindAllStringSubmatch(string(b), -1) {
			titles[h[1]] = strings.TrimSpace(h[2])
		}
	}
	var out []stepRef
	for n := first; n <= last; n++ {
		id := fmt.Sprintf("%s.S%d", m[1], n)
		t, ok := titles[id]
		if !ok {
			return nil, fmt.Errorf("%s has no step %s in steps.md", folder, id)
		}
		out = append(out, stepRef{ID: id, Title: t})
	}
	return out, nil
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

// DigestPath returns the repository path of a step's digest.
func DigestPath(root, step string) (string, error) {
	m := stepArgExpr.FindStringSubmatch(step)
	if m == nil {
		return "", fmt.Errorf("%q is not a step such as 0115.S8 or a range such as 0113.S1-S3", step)
	}
	folder, err := changeFolder(root, m[1])
	if err != nil {
		return "", err
	}
	return folder + "/digests/" + step + ".md", nil
}

// digestData is everything a digest reports.
type digestData struct {
	steps      []stepRef
	branch     string
	base       commit
	baseRef    string
	commits    []commit
	changes    map[string][]ItemChange // kind -> changes
	sites      map[string][]Site
	docs       *DocIndex
	docsEdited map[string]bool
	pages      []PageInfo // touched pages, by code
	removed    []removedName
	busy       []busyRequirement
}

// removedName is a catalog name the branch removed, with what replaced it
// and where the documentation still uses it.
type removedName struct {
	block    *Item
	renamed  []string
	mentions []Mention
}

// busyRequirement is a requirement cited from more than decidersWarning
// functions.
type busyRequirement struct {
	id    string
	funcs []string
}

// Digest builds the digest of a step's branch and returns its text.
func Digest(ctx context.Context, root string, opts DigestOptions) (string, error) {
	r := repo{root: root}
	m := stepArgExpr.FindStringSubmatch(opts.Step)
	if m == nil {
		return "", fmt.Errorf("%q is not a step such as 0115.S8 or a range such as 0113.S1-S3", opts.Step)
	}
	folder, err := changeFolder(root, m[1])
	if err != nil {
		return "", err
	}
	d := digestData{branch: r.branch(ctx), changes: map[string][]ItemChange{}}
	if d.steps, err = digestSteps(root, folder, opts.Step); err != nil {
		return "", err
	}
	baseRev := opts.Base
	d.baseRef = "the commit given with -base"
	if baseRev == "" {
		if baseRev, d.baseRef, err = r.mergeBase(ctx); err != nil {
			return "", err
		}
		d.baseRef = "its merge base with `" + d.baseRef + "`"
	}
	if d.base, err = r.describe(ctx, baseRev); err != nil {
		return "", err
	}
	out, err := r.git(ctx, "log", "--reverse", "--date=short", commitFormat, d.base.Hash+"..HEAD", "--")
	if err != nil {
		return "", err
	}
	d.commits = parseCommits(out)

	scratch, err := os.MkdirTemp("", "specshift-digest-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	before, err := revSnapshot(ctx, r, d.base.Hash, scratch)
	if err != nil {
		return "", err
	}
	after, err := worktreeSnapshot(ctx, r)
	if err != nil {
		return "", err
	}
	var wanted []string
	for _, kind := range []string{KindRequirement, KindOperation, KindBlock} {
		d.changes[kind] = compare(before, after, kind)
		for _, c := range d.changes[kind] {
			wanted = append(wanted, c.Item().ID)
		}
	}
	cited, err := citedOnChangedLines(ctx, root, d.base.Hash)
	if err != nil {
		return "", err
	}
	wanted = append(wanted, cited...)
	if d.sites, err = findSites(ctx, r, dedupe(wanted)); err != nil {
		return "", err
	}
	if d.docs, err = loadDocs(ctx, r); err != nil {
		return "", err
	}
	editedDocs, err := r.changedFiles(ctx, d.base.Hash, docsDir)
	if err != nil {
		return "", err
	}
	d.docsEdited = map[string]bool{}
	for _, p := range editedDocs {
		d.docsEdited[p] = true
	}
	changedSpec, err := r.changedFiles(ctx, d.base.Hash, "spec")
	if err != nil {
		return "", err
	}
	d.pages = touchedPages(before, after, d.changes, changedSpec)
	d.removed = removedNames(r, d.docs, d.changes[KindBlock])
	d.busy = busyRequirements(d.sites)
	return renderDigest(d), nil
}

// citedOnChangedLines returns the requirements cited by annotations on the
// Go lines the working tree adds or changes relative to base.
func citedOnChangedLines(ctx context.Context, root, base string) ([]string, error) {
	changed, err := annotation.ChangedGoLines(ctx, root, base)
	if err != nil {
		return nil, err
	}
	var out []string
	for p, lns := range changed {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, a := range annotation.Annotations(annotation.Comments(p, src)) {
			touched := false
			for n := a.Line; n <= a.EndLine; n++ {
				touched = touched || lns[n]
			}
			if !touched {
				continue
			}
			for _, id := range annotation.Identifiers(a.Payload) {
				if k, _ := annotation.Classify(id); k == annotation.Requirement {
					out = append(out, id)
				}
			}
		}
	}
	return out, nil
}

// dedupe returns the distinct strings in order.
func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// touchedPages returns the pages that hold a changed item, before or
// after, and the pages whose file the branch changed, by code.
func touchedPages(before, after *Snapshot, changes map[string][]ItemChange, changedFiles []string) []PageInfo {
	byCode := map[string]PageInfo{}
	note := func(p PageInfo) {
		if p.Code != "" {
			byCode[p.Code] = p
		}
	}
	lookup := func(file string) {
		if p, ok := after.ByPath[file]; ok {
			note(p)
		} else if p, ok := before.ByPath[file]; ok {
			note(p)
		}
	}
	for _, cs := range changes {
		for _, c := range cs {
			if c.Old != nil {
				lookup(c.Old.File)
			}
			if c.New != nil {
				lookup(c.New.File)
			}
		}
	}
	for _, f := range changedFiles {
		if specPage(f) {
			lookup(f)
		}
	}
	out := make([]PageInfo, 0, len(byCode))
	for _, p := range byCode {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// removedNames returns the definition blocks the branch retired, with the
// blocks of the same kind it added on the same page, which are the likely
// renames, and the documentation lines that still use the old name.
func removedNames(r repo, docs *DocIndex, blocks []ItemChange) []removedName {
	var out []removedName
	for _, c := range blocks {
		if c.Change != Retired {
			continue
		}
		rn := removedName{block: c.Old}
		if c.HasRecord && c.Record.Successor != "" {
			rn.renamed = append(rn.renamed, c.Record.Successor)
		}
		for _, a := range blocks {
			if a.Change == Added && a.New.BlockKind == c.Old.BlockKind && a.New.Code == c.Old.Code {
				rn.renamed = append(rn.renamed, a.New.Name)
			}
		}
		rn.renamed = dedupe(rn.renamed)
		rn.mentions = mentions(r, docs, c.Old.Name)
		out = append(out, rn)
	}
	return out
}

// busyRequirements returns the requirements cited from more than
// decidersWarning functions outside tests.
func busyRequirements(sites map[string][]Site) []busyRequirement {
	var out []busyRequirement
	for id, ss := range sites {
		if k, _ := annotation.Classify(id); k != annotation.Requirement {
			continue
		}
		if fs := deciders(ss); len(fs) > decidersWarning {
			out = append(out, busyRequirement{id: id, funcs: fs})
		}
	}
	sort.Slice(out, func(i, j int) bool { return naturalLess(out[i].id, out[j].id) })
	return out
}

// renderDigest writes the digest.
func renderDigest(d digestData) string {
	var b strings.Builder
	title := d.steps[0].ID + " " + d.steps[0].Title
	if len(d.steps) > 1 {
		title = d.steps[0].ID + " to " + d.steps[len(d.steps)-1].ID
	}
	arg := d.steps[0].ID
	if len(d.steps) > 1 {
		arg = d.steps[0].ID + "-" + strings.SplitN(d.steps[len(d.steps)-1].ID, ".", 2)[1]
	}
	fmt.Fprintf(&b, "# Digest: %s\n\n", title)
	fmt.Fprintf(&b, "Generated by `go run ./scripts/specshift digest %s` from branch `%s`, compared with `%s` (%s), %s. The sections from \"Steps\" to \"Requirements cited from more than three functions\" are generated from the tree. The session fills in \"Gate results\", \"Review outcomes\", and \"Defaults applied\".\n\n",
		arg, d.branch, d.base.Short, d.base.Date, d.baseRef)

	b.WriteString("## Steps\n\n| Step | Title |\n| :-- | :-- |\n")
	for _, s := range d.steps {
		fmt.Fprintf(&b, "| %s | %s |\n", s.ID, cell(s.Title))
	}
	b.WriteString("\n## Commits\n\n")
	if len(d.commits) == 0 {
		b.WriteString("The branch has no commits of its own yet.\n")
	} else {
		b.WriteString("| Commit | Date | Subject |\n| :-- | :-- | :-- |\n")
		for _, c := range d.commits {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", c.Short, c.Date, cell(c.Subject))
		}
	}

	sections := []struct{ kind, heading, noun string }{
		{KindRequirement, "Requirements", "requirement"},
		{KindOperation, "Operations", "operation"},
		{KindBlock, "Definition blocks", "definition block"},
	}
	for _, s := range sections {
		fmt.Fprintf(&b, "\n## %s\n\n", s.heading)
		cs := d.changes[s.kind]
		if len(cs) == 0 {
			fmt.Fprintf(&b, "None. The branch adds, modifies, and retires no %s.\n", s.noun)
			continue
		}
		counts := map[string]int{}
		for _, c := range cs {
			counts[c.Change]++
		}
		fmt.Fprintf(&b, "The branch adds %d, modifies %d, and retires %d.\n", counts[Added], counts[Modified], counts[Retired])
		for _, c := range cs {
			renderChange(&b, c, d.sites[c.Item().ID])
		}
	}

	b.WriteString("\n## Documentation\n\n")
	b.WriteString(d.docs.coverageNote() + "\n\n")
	if len(d.pages) == 0 {
		b.WriteString("The branch touches no specification page.\n")
	} else {
		codes := make([]string, 0, len(d.pages))
		for _, p := range d.pages {
			codes = append(codes, "`"+p.Code+"`")
		}
		if len(codes) <= 40 {
			fmt.Fprintf(&b, "The branch touches %s: %s.\n\n", pluralf(len(codes), "specification page", "specification pages"), strings.Join(codes, ", "))
		} else {
			fmt.Fprintf(&b, "The branch touches %d specification pages; `git diff --stat %s -- spec` lists them.\n\n", len(codes), d.base.Short)
		}
		described := false
		for _, p := range d.pages {
			docs := d.docs.ByCode[p.Code]
			if len(docs) == 0 {
				continue
			}
			if !described {
				b.WriteString("| Specification page | Documentation page | Edited on the branch |\n| :-- | :-- | :-- |\n")
				described = true
			}
			for _, doc := range docs {
				edited := "no"
				if d.docsEdited[doc] {
					edited = "yes"
				}
				fmt.Fprintf(&b, "| `%s` | %s | %s |\n", p.Code, doc, edited)
			}
		}
		if !described {
			b.WriteString("No documentation page names a touched specification page in its front matter.\n")
		}
	}

	b.WriteString("\n## Catalog names the documentation still uses\n\n")
	if len(d.removed) == 0 {
		b.WriteString("None. The branch renames and removes no definition block.\n")
	} else {
		for _, rn := range d.removed {
			fmt.Fprintf(&b, "- `%s` (%s, %s)", rn.block.Name, rn.block.BlockKind, pageLabel(PageInfo{Code: rn.block.Code, Path: rn.block.File}))
			if len(rn.renamed) > 0 {
				fmt.Fprintf(&b, ", replaced by `%s`", strings.Join(rn.renamed, "`, `"))
			}
			if len(rn.mentions) == 0 {
				b.WriteString(": no documentation page names it.\n")
				continue
			}
			b.WriteString(": named by\n")
			for _, m := range rn.mentions {
				fmt.Fprintf(&b, "  - %s:%d\n", m.Path, m.Line)
			}
		}
	}

	b.WriteString("\n## Requirements cited from more than three functions\n\n")
	if len(d.busy) == 0 {
		b.WriteString("None. No requirement the branch touches, or cites on a changed line, is cited from more than three functions.\n")
	} else {
		b.WriteString("Check that each decision is made once and that the other functions only pass its result along.\n\n")
		for _, br := range d.busy {
			fmt.Fprintf(&b, "- %s, cited from %d functions: %s\n", br.id, len(br.funcs), strings.Join(br.funcs, "; "))
		}
	}

	b.WriteString("\n## Gate results\n\nNot filled in yet. List tier 0 and every tier the step reached, with the result of each and the pre-existing failures by name.\n")
	b.WriteString("\n## Review outcomes\n\nNot filled in yet. List the fixes made, the open questions raised, and the issues filed.\n")
	b.WriteString("\n## Defaults applied\n\nNot filled in yet. List every choice made without asking, with the option taken.\n")
	return b.String()
}

// renderChange writes one changed item with its text before and after
// and its sites.
func renderChange(b *strings.Builder, c ItemChange, sites []Site) {
	it := c.Item()
	fmt.Fprintf(b, "\n### %s (%s)\n\n", it.ID, c.Change)
	page := PageInfo{Code: it.Code, Path: it.File}
	fmt.Fprintf(b, "Page: %s.", pageLabel(page))
	if c.Moved() {
		fmt.Fprintf(b, " Moved from %s.", c.Old.File)
	}
	if c.Change == Retired {
		switch {
		case c.HasRecord && c.Record.Successor != "":
			fmt.Fprintf(b, " Successor: %s.", c.Record.Successor)
		case c.HasRecord && c.Record.Removed != "":
			fmt.Fprintf(b, " Record: %s.", strings.TrimSuffix(c.Record.Removed, "."))
		default:
			b.WriteString(" The page records no successor and no removal for it, which `specshift ref check` refuses.")
		}
	}
	b.WriteString("\n\n| Before | After |\n| :-- | :-- |\n")
	old, now := "(none)", "(none)"
	if c.Old != nil {
		old = cell(c.Old.Text)
	}
	if c.New != nil {
		now = cell(c.New.Text)
	}
	fmt.Fprintf(b, "| %s | %s |\n\n", old, now)
	code, tests, packages := splitSites(sites)
	code = append(code, packages...)
	fmt.Fprintf(b, "- Code sites: %s\n", siteList(code))
	fmt.Fprintf(b, "- Tests: %s\n", siteList(tests))
}

// siteList renders sites inline, or says none were found.
func siteList(sites []Site) string {
	if len(sites) == 0 {
		return "none found by annotation."
	}
	parts := make([]string, 0, len(sites))
	for _, s := range sites {
		parts = append(parts, "`"+s.String()+"`")
	}
	return strings.Join(parts, ", ") + "."
}

// cell renders text for one table cell: pipes escaped and line breaks as
// <br>.
func cell(text string) string {
	if text == "" {
		return "(empty)"
	}
	text = strings.ReplaceAll(text, "|", `\|`)
	return strings.ReplaceAll(text, "\n", "<br>")
}

// WriteDigest builds the digest and writes it to the step's digest file,
// refusing to replace a file that exists unless force is set, because the
// session fills in parts of it by hand. It returns the path written. It
// writes through scope.WriteChecked, which refuses a digest path that is a
// symbolic link or whose directory resolves outside the repository.
func WriteDigest(ctx context.Context, root string, opts DigestOptions, force bool) (string, error) {
	p, err := DigestPath(root, opts.Step)
	if err != nil {
		return "", err
	}
	abs, err := scope.CheckedPath(root, p)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil && !force {
		return "", fmt.Errorf("%s exists and may hold text filled in by hand; pass -force to replace it", p)
	}
	text, err := Digest(ctx, root, opts)
	if err != nil {
		return "", err
	}
	if err := scope.WriteChecked(root, p, []byte(text), 0o644); err != nil {
		return "", err
	}
	return p, nil
}
