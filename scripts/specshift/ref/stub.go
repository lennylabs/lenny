// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// StubMarker is the line a full successor stub carries directly under its
// heading. It tells the gates and the citation tools that every paragraph
// of the section has migrated and only the successor list remains. A
// partial stub, which keeps part of its text, carries no marker.
const StubMarker = "<!-- successor-stub -->"

// The lead lines that open a successor list. The stub command writes them
// and reads them back, so a second stub of the same section merges its
// successors into the list rather than writing a second one.
const (
	fullStubLead    = "The content of this section has moved to these pages and identifiers:"
	partialStubLead = "Part of the content of this section has moved to these pages and identifiers:"
)

// StubSection is a heading of a numbered file that is a full successor
// stub. Number is empty for a heading that carries no section number,
// such as an unnumbered subheading of a section stubbed recursively.
type StubSection struct {
	Number string
	Title  string
	Anchor string
	Level  int
	Line   int
}

// FullStubs returns the headings of a document that are full successor
// stubs: the first non-blank line under the heading is StubMarker. The
// gates read a numbered file through it to decide which of its sections
// the new tree's rules govern, and the citation tools read it to resolve a
// section citation through the stub.
func FullStubs(content string) []StubSection {
	lines := strings.Split(content, "\n")
	hs := headings(content)
	var out []StubSection
	for i, h := range hs {
		end := len(lines)
		if i+1 < len(hs) {
			end = hs[i+1].line - 1
		}
		if isFullStubBody(lines[h.line:end]) {
			out = append(out, StubSection{Number: h.number, Title: h.title, Anchor: h.linkAnchor(), Level: h.level, Line: h.line})
		}
	}
	return out
}

// isFullStubBody reports whether the first non-blank line of a heading's
// own body is the stub marker.
func isFullStubBody(body []string) bool {
	for _, l := range body {
		if t := strings.TrimSpace(l); t != "" {
			return t == StubMarker
		}
	}
	return false
}

// StubOptions is one stub request.
type StubOptions struct {
	// Section is the section number, without the § sign.
	Section string
	// To names the successors: page codes or aliases, page paths,
	// requirement or operation identifiers, and term or block anchors.
	To []string
	// Keep keeps the section's text below the successor list and writes
	// no marker, for a section that keeps part of its content.
	Keep bool
	// Recursive accepts a section with subsections and applies the stub
	// to every heading under it.
	Recursive bool
}

// StubChange is what the stub command did to one heading.
type StubChange struct {
	Title   string
	Full    bool
	Removed int // non-blank lines of text removed
}

// StubResult is what the stub command changed.
type StubResult struct {
	File       string
	Section    string
	Title      string
	Changes    []StubChange
	Successors []string // the list items written, in order
}

// Stub rewrites one numbered section into a successor stub. It keeps the
// section's heading and anchor, and writes a successor list built from the
// registry, each entry linked by a path relative to the numbered file and
// followed by its title. Without Keep it removes the section's text and
// writes the marker; with Keep it adds the list above the kept text. A
// section that already carries a list has the new successors merged into
// it. Stub refuses an unregistered or retired successor, a missing
// section, and a section with subsections unless Recursive is set, and
// then writes nothing.
func Stub(ctx context.Context, t *Tree, opts StubOptions) (StubResult, error) {
	number := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(opts.Section), "§"))
	if !sectionNumberExpr.MatchString(number) {
		return StubResult{}, fmt.Errorf("§%s is not a section number", number)
	}
	if len(opts.To) == 0 {
		return StubResult{}, fmt.Errorf("stub §%s: no successor given; pass --to", number)
	}
	found, content, err := locateSection(ctx, t, number)
	if err != nil {
		return StubResult{}, err
	}
	succ, err := resolveSuccessors(ctx, t, found.file, opts.To)
	if err != nil {
		return StubResult{}, fmt.Errorf("stub §%s: %w", number, err)
	}
	targets := subtree(headings(content), found.heading)
	if len(targets) > 1 && !opts.Recursive {
		return StubResult{}, fmt.Errorf("stub §%s: the section has %d subsection(s), the first %q; pass --recursive to stub them too", number, len(targets)-1, targets[1].title)
	}
	updated, changes := rewriteStubs(content, targets, succ, opts.Keep)
	if err := t.Write(found.file, updated); err != nil {
		return StubResult{}, err
	}
	res := StubResult{File: found.file, Section: number, Title: found.heading.title, Changes: changes}
	for _, s := range succ {
		res.Successors = append(res.Successors, s.item)
	}
	return res, nil
}

// locateSection finds the numbered heading that carries a section number
// and returns it with the content of its file.
func locateSection(ctx context.Context, t *Tree, number string) (numberedSection, string, error) {
	files, err := t.Files(ctx)
	if err != nil {
		return numberedSection{}, "", err
	}
	for _, f := range files {
		if !isNumbered(f) {
			continue
		}
		content, err := t.Read(f)
		if err != nil {
			return numberedSection{}, "", err
		}
		if found, ok := findSection(f, content, number); ok {
			return found, content, nil
		}
	}
	return numberedSection{}, "", fmt.Errorf("no numbered heading §%s in the numbered files", number)
}

// subtree returns a heading and every heading under it, up to the next
// heading of the same or a higher level.
func subtree(hs []heading, top heading) []heading {
	var out []heading
	for i, h := range hs {
		if h.line != top.line {
			continue
		}
		out = append(out, h)
		for _, next := range hs[i+1:] {
			if next.level <= h.level {
				break
			}
			out = append(out, next)
		}
		break
	}
	return out
}

// rewriteStubs replaces the own body of each target heading with a stub
// body and returns the new content with what changed under each heading.
// The headings are rewritten from the last to the first, so the line
// numbers of the earlier ones stay valid.
func rewriteStubs(content string, targets []heading, succ []successor, keep bool) (string, []StubChange) {
	lines := strings.Split(content, "\n")
	all := headings(content)
	changes := make([]StubChange, len(targets))
	for i := len(targets) - 1; i >= 0; i-- {
		h := targets[i]
		start, end := h.line, len(lines)
		for _, next := range all {
			if next.line > h.line {
				end = next.line - 1
				break
			}
		}
		body, change := stubBody(lines[start:end], succ, keep)
		change.Title = h.title
		changes[i] = change
		lines = append(lines[:start:start], append(body, lines[end:]...)...)
	}
	return strings.Join(lines, "\n"), changes
}

// stubBody returns the stub body that replaces a heading's own body: a
// blank line, the marker for a full stub, the lead line, the merged
// successor list, and for a partial stub the kept text. The body ends
// with a blank line, which separates it from the next heading or, at the
// end of the file, leaves the final newline.
func stubBody(body []string, succ []successor, keep bool) ([]string, StubChange) {
	items, rest, wasFull := parseStubBody(body)
	seen := map[string]bool{}
	for _, it := range items {
		seen[itemKey(it)] = true
	}
	for _, s := range succ {
		if !seen[s.key] {
			items = append(items, s.item)
			seen[s.key] = true
		}
	}
	full := !keep || wasFull
	out := []string{""}
	change := StubChange{Full: full}
	if full {
		out = append(out, StubMarker, "", fullStubLead, "")
		out = append(out, items...)
		for _, l := range rest {
			if strings.TrimSpace(l) != "" {
				change.Removed++
			}
		}
	} else {
		out = append(out, partialStubLead, "")
		out = append(out, items...)
		if kept := trimBlank(rest); len(kept) > 0 {
			out = append(out, "")
			out = append(out, kept...)
		}
	}
	out = append(out, "")
	return out, change
}

// parseStubBody splits a heading's own body into the items of a successor
// list it already carries, the rest of its text, and whether it carried
// the marker.
func parseStubBody(body []string) (items, rest []string, full bool) {
	i := skipBlank(body, 0)
	if i < len(body) && strings.TrimSpace(body[i]) == StubMarker {
		full = true
		i = skipBlank(body, i+1)
	}
	if i < len(body) {
		if lead := strings.TrimSpace(body[i]); lead == fullStubLead || lead == partialStubLead {
			i = skipBlank(body, i+1)
			for i < len(body) && strings.HasPrefix(body[i], "- ") {
				items = append(items, body[i])
				i++
			}
		}
	}
	return items, body[i:], full
}

// skipBlank returns the index of the first non-blank line at or after i.
func skipBlank(lines []string, i int) int {
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	return i
}

// trimBlank drops the leading and trailing blank lines.
func trimBlank(lines []string) []string {
	lo, hi := 0, len(lines)
	for lo < hi && strings.TrimSpace(lines[lo]) == "" {
		lo++
	}
	for hi > lo && strings.TrimSpace(lines[hi-1]) == "" {
		hi--
	}
	return lines[lo:hi]
}

// itemKey returns the link target of a successor list item, which is how
// two items naming the same successor are recognized.
func itemKey(item string) string {
	ls := links(item)
	if len(ls) == 0 {
		return item
	}
	return displayTarget(ls[0].path, ls[0].fragment)
}

// successor is one resolved entry of a successor list.
type successor struct {
	key  string // the link target, relative to the numbered file
	item string // the list item
}

// resolveSuccessors resolves every name against the registry and returns
// the list items, in the order given and without duplicates. The names
// that fail to resolve, whether unknown or retired, are reported in one
// error so a caller fixes them in one pass.
func resolveSuccessors(ctx context.Context, t *Tree, numbered string, names []string) ([]successor, error) {
	committed, err := LoadCommitted(t)
	if err != nil {
		return nil, err
	}
	reg, _, err := Build(ctx, t, committed)
	if err != nil {
		return nil, err
	}
	x := newIndex(reg)
	var out []successor
	var problems []string
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		s, err := resolveSuccessor(t, x, numbered, name)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if !seen[s.key] {
			seen[s.key] = true
			out = append(out, s)
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no successor given")
	}
	return out, nil
}

// resolveSuccessor resolves one name: a page code or alias, a page path
// (from the repository root or from spec/), a requirement or operation
// identifier, or a term or block anchor.
func resolveSuccessor(t *Tree, x *index, numbered, name string) (successor, error) {
	if r, ok := x.retired[name]; ok {
		return successor{}, retiredError(r)
	}
	for _, r := range x.reg.Retired {
		if r.Anchor == name {
			return successor{}, retiredError(r)
		}
	}
	page, isPage := pageNamed(x, name)
	if isPage {
		title, err := pageTitle(t, page.Path)
		if err != nil {
			return successor{}, err
		}
		return newSuccessor(numbered, page.Code, page.Path, "", title), nil
	}
	if anchor, ok := x.idAnchor[name]; ok {
		file := x.anchorFile[anchor]
		title, err := identifierTitle(t, x, name, anchor, file)
		if err != nil {
			return successor{}, err
		}
		return newSuccessor(numbered, name, file, anchor, title), nil
	}
	if file, ok := x.anchorFile[name]; ok {
		title, err := anchorTitle(t, x, name, file)
		if err != nil {
			return successor{}, err
		}
		return newSuccessor(numbered, "`"+name+"`", file, name, title), nil
	}
	return successor{}, fmt.Errorf("%s is not a registered page, identifier, or anchor", name)
}

// retiredError refuses a retired identifier as a successor, naming what
// replaced it.
func retiredError(r Retired) error {
	if r.Successor != "" {
		return fmt.Errorf("%s is retired; name its successor %s", r.ID, r.Successor)
	}
	return fmt.Errorf("%s is retired (%s)", r.ID, r.Removed)
}

// pageNamed returns the page a code, an alias, or a path names.
func pageNamed(x *index, name string) (Page, bool) {
	if p, ok := x.codePath[name]; ok {
		return x.pageByPath[p], true
	}
	for _, p := range []string{name, specDir + strings.TrimPrefix(name, "./")} {
		if page, ok := x.pageByPath[p]; ok {
			return page, true
		}
	}
	return Page{}, false
}

// newSuccessor builds a list item linking a target from the numbered file.
func newSuccessor(numbered, label, file, anchor, title string) successor {
	key := displayTarget(relativeTo(dirOf(numbered), file), anchor)
	item := fmt.Sprintf("- [%s](%s)", label, key)
	if title != "" {
		item += ": " + title
	}
	return successor{key: key, item: item}
}

// dirOf returns the directory of a repository path.
func dirOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}

// pageTitle returns the text of a page's first level-one heading.
func pageTitle(t *Tree, file string) (string, error) {
	content, err := t.Read(file)
	if err != nil {
		return "", err
	}
	for _, h := range headings(content) {
		if h.level == 1 {
			return h.title, nil
		}
	}
	return "", nil
}

// identifierTitle returns the title of a requirement, the bold text after
// its identifier, or for an operation the title of its page.
func identifierTitle(t *Tree, x *index, id, anchor, file string) (string, error) {
	content, err := t.Read(file)
	if err != nil {
		return "", err
	}
	expr := regexp.MustCompile(`<a\s+id="` + regexp.QuoteMeta(anchor) + `"\s*(?:/>|>\s*</a>)\s*\*\*` + regexp.QuoteMeta(id) + `\.\s*(.*?)\*\*`)
	if m := expr.FindStringSubmatch(content); m != nil {
		return strings.TrimSpace(m[1]), nil
	}
	for _, op := range x.reg.Operations {
		if op.ID == id {
			title, err := pageTitle(t, file)
			if err != nil || title == "" {
				return "", err
			}
			return "operation on " + title, nil
		}
	}
	return "", nil
}

// anchorTitle returns the title of a block anchor (its kind and name) or
// of a term anchor (the text after the anchor in its table cell).
func anchorTitle(t *Tree, x *index, anchor, file string) (string, error) {
	for _, b := range x.reg.Blocks {
		if b.Anchor == anchor {
			return fmt.Sprintf("%s `%s`", b.Kind, b.Name), nil
		}
	}
	content, err := t.Read(file)
	if err != nil {
		return "", err
	}
	for _, a := range explicitAnchors(content) {
		if a.id != anchor {
			continue
		}
		text, _, _ := strings.Cut(a.context, "\n")
		text, _, _ = strings.Cut(text, "|")
		return strings.TrimSpace(text), nil
	}
	return "", nil
}
