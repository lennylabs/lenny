// SPDX-License-Identifier: MIT

package ref

import (
	"context"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// FrozenTag is the git tag that marks the specification as it stood
// before the first area migrated.
const FrozenTag = "spec-frozen"

var sectionNumberExpr = regexp.MustCompile(`^\d+(?:\.\d+)*$`)

// Lookup prints where a code, an identifier, an anchor, or a numbered
// section (`§N.M`) is.
//
// For an identifier it prints the file and anchor, and for a retired one
// its successor or its removal. For a section it prints the numbered
// heading's file and anchor, the successor links listed under the
// heading when the section has become a successor stub, and the
// section's text at the frozen tag when the tag exists.
func Lookup(ctx context.Context, t *Tree, query string, out io.Writer) error {
	query = strings.TrimSpace(query)
	if strings.HasPrefix(query, "§") {
		return lookupSection(ctx, t, strings.TrimSpace(strings.TrimPrefix(query, "§")), out)
	}
	committed, err := LoadCommitted(t)
	if err != nil {
		return err
	}
	reg, _, err := Build(ctx, t, committed)
	if err != nil {
		return err
	}
	return lookupIdentifier(newIndex(reg), query, out)
}

// lookupIdentifier prints the place of a code, identifier, or anchor.
func lookupIdentifier(x *index, query string, out io.Writer) error {
	if p, ok := x.codePath[query]; ok {
		if code, alias := x.aliasOf[query]; alias {
			fmt.Fprintf(out, "%s %s (alias of %s)\n", query, p, code)
			return nil
		}
		fmt.Fprintf(out, "%s %s\n", query, p)
		return nil
	}
	anchor, ok := x.idAnchor[query]
	if !ok {
		anchor = strings.ToLower(query)
	}
	file, ok := x.anchorFile[anchor]
	if !ok {
		return fmt.Errorf("%s is not a registered code, identifier, or anchor", query)
	}
	fmt.Fprintf(out, "%s %s#%s\n", query, file, anchor)
	if r, retired := x.retired[query]; retired {
		if r.Removed != "" {
			fmt.Fprintf(out, "retired: %s\n", r.Removed)
			return nil
		}
		fmt.Fprintf(out, "retired: successor %s\n", r.Successor)
		return lookupIdentifier(x, r.Successor, out)
	}
	return nil
}

// numberedSection is one numbered heading and the text under it.
type numberedSection struct {
	file    string
	heading heading
	body    []string // the lines up to the next heading of any level
	text    []string // the lines up to the next heading of the same or a higher level
}

// findSection returns the numbered heading of a document that carries
// the number.
func findSection(file, content, number string) (numberedSection, bool) {
	lines := strings.Split(content, "\n")
	hs := headings(content)
	for i, h := range hs {
		if h.number != number {
			continue
		}
		s := numberedSection{file: file, heading: h}
		bodyEnd, textEnd := len(lines), len(lines)
		if i+1 < len(hs) {
			bodyEnd = hs[i+1].line - 1
		}
		for _, next := range hs[i+1:] {
			if next.level <= h.level {
				textEnd = next.line - 1
				break
			}
		}
		s.body = lines[h.line:bodyEnd]
		s.text = lines[h.line-1 : textEnd]
		return s, true
	}
	return numberedSection{}, false
}

// linkAnchor returns the anchor a link to a heading uses: its first
// explicit attribute, or its slug.
func (h heading) linkAnchor() string {
	if len(h.attrs) > 0 {
		return h.attrs[0]
	}
	return h.anchor
}

// lookupSection prints a numbered section, its successor stub, and its
// frozen text.
func lookupSection(ctx context.Context, t *Tree, number string, out io.Writer) error {
	if !sectionNumberExpr.MatchString(number) {
		return fmt.Errorf("§%s is not a section number", number)
	}
	found, content, err := locateSection(ctx, t, number)
	if err != nil {
		return err
	}
	full := false
	for _, s := range FullStubs(content) {
		full = full || s.Line == found.heading.line
	}
	fmt.Fprintf(out, "§%s: %s#%s\n", found.heading.title, found.file, found.heading.linkAnchor())
	printSuccessors(found, number, full, out)
	return printFrozen(ctx, t, found, number, out)
}

// printSuccessors states whether the section's content has migrated and
// prints the links into the new tree, and the rows of a successor table,
// that the section's own text lists. A full stub carries the stub marker,
// a section that lists successors without the marker keeps part of its
// text, and a section that lists none has not migrated.
//
// A full stub holds nothing but its successor list, so every link it
// carries outside the numbered files is a successor, including one to a
// record outside the specification, such as a program under changes/ or
// a documentation page. A partial stub keeps text whose links are not
// successors, so its links into the new tree are listed, and so is every
// link in the list that follows the partial-stub lead line, wherever it
// points, because the stub command writes the successors there.
func printSuccessors(s numberedSection, number string, full bool, out io.Writer) {
	var lines []string
	seen := map[string]bool{}
	addLinks := func(text string, anyTarget bool) {
		for _, l := range links(text) {
			target, ok := resolve(s.file, l.path)
			if !ok || isNumbered(target) || (!anyTarget && !newTreeTarget(target)) {
				continue
			}
			shown := displayTarget(target, l.fragment)
			if !seen[shown] {
				seen[shown] = true
				lines = append(lines, "  "+shown)
			}
		}
	}
	if !full {
		addLinks(strings.Join(partialSuccessorList(s.body), "\n"), true)
	}
	addLinks(strings.Join(s.body, "\n"), full)
	inTable := false
	for _, l := range s.body {
		trimmed := strings.TrimSpace(l)
		switch {
		case !strings.HasPrefix(trimmed, "|"):
			inTable = false
		case strings.Contains(strings.ToLower(trimmed), "successor"):
			inTable = true
		case inTable && !strings.HasPrefix(strings.ReplaceAll(trimmed, " ", ""), "|:-") && !strings.HasPrefix(strings.ReplaceAll(trimmed, " ", ""), "|--"):
			lines = append(lines, "  "+trimmed)
		}
	}
	switch {
	case full:
		fmt.Fprintf(out, "successor stub: the content of §%s has migrated\n", number)
	case len(lines) > 0:
		fmt.Fprintf(out, "partial successor stub: part of the content of §%s has migrated, and the rest stays under the heading\n", number)
	default:
		fmt.Fprintf(out, "not a successor stub: the content of §%s has not migrated\n", number)
		return
	}
	fmt.Fprintln(out, "successors:")
	for _, l := range lines {
		fmt.Fprintln(out, l)
	}
}

// partialSuccessorList returns the list items that follow the partial-stub
// lead line of a section body, or nil when the body carries no such lead.
func partialSuccessorList(body []string) []string {
	var items []string
	for i, l := range body {
		if strings.TrimSpace(l) != partialStubLead {
			continue
		}
		for _, item := range body[i+1:] {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" && len(items) == 0 {
				continue
			}
			if !strings.HasPrefix(trimmed, "- ") {
				break
			}
			items = append(items, trimmed)
		}
		break
	}
	return items
}

// printFrozen prints the section's text as it stood at the frozen tag.
func printFrozen(ctx context.Context, t *Tree, s numberedSection, number string, out io.Writer) error {
	if _, err := t.Git(ctx, "rev-parse", "-q", "--verify", "refs/tags/"+FrozenTag); err != nil {
		fmt.Fprintf(out, "frozen text: the tag %s does not exist yet\n", FrozenTag)
		return nil
	}
	content, err := t.Git(ctx, "show", FrozenTag+":"+s.file)
	if err != nil {
		fmt.Fprintf(out, "frozen text: %s does not exist at %s\n", s.file, FrozenTag)
		return nil
	}
	frozen, ok := findSection(s.file, string(content), number)
	if !ok {
		fmt.Fprintf(out, "frozen text: %s has no heading §%s at %s\n", path.Base(s.file), number, FrozenTag)
		return nil
	}
	fmt.Fprintf(out, "frozen text at %s:\n%s\n", FrozenTag, strings.TrimRight(strings.Join(frozen.text, "\n"), "\n"))
	return nil
}
