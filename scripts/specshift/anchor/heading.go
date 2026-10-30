// SPDX-License-Identifier: MIT

package anchor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lennylabs/lenny/scripts/specshift/citation"
	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// headings indexes every anchor the markdown documents of the tree
// declare, per file, together with the heading each anchor addresses.
//
// The index is what holds a redirect to a heading that exists. A
// successor anchor nothing declares would send every inbound reference
// to a page position that does not resolve, and no gate over the anchor
// classes reads meaning, so the run fails before it writes rather than
// after.
//
// The index is also what decides the spelling a redirected citation is
// written in, which is the §-form for a heading of the specification and
// the file-and-anchor form for every other heading. A specification
// heading that carries no number of its own is cited by the number of
// the numbered heading that encloses it, so the index records that
// enclosing number beside each anchor.
//
// The index covers every markdown document of the class read domain,
// because a fragment link addresses a heading in the document it names,
// whichever document that is.
// The index carries no register of which section numbers the
// specification states. Whether a section still exists decides nothing
// here: the anchor-move map is what records the retirement this pass
// migrates, and a citation the map does not name is a citation the
// reduction did not invalidate whether or not the tree still numbers a
// heading for it.
type headings struct {
	byFile map[string]map[string]destination
}

// destination is one anchor's entry in the index: the heading the anchor
// addresses, and the number of the nearest numbered heading that
// encloses it.
//
// The enclosing number is read while the document is walked, because the
// walk is the only place the order of the headings is known. An anchor
// alone states nothing about what surrounds it, and the map from anchor
// to heading the index is keyed by has already lost the order.
type destination struct {
	heading citation.Heading
	// section is the number of the nearest preceding heading that
	// carries a number and sits at a shallower level, which is the
	// section the heading is written inside. It is empty when no heading
	// of the document encloses this one, such as for the level-one title
	// a document opens with, or for a heading that stands above the
	// document's first numbered one.
	//
	// The level test is what makes this the enclosing section rather than
	// the nearest number written above. In spec/15 the carve-out heading
	// `#### Translation Fidelity Matrix` follows `#### 15.4.1`, which is
	// its sibling rather than its parent: the section it is written
	// inside is `### 15.4`, and that is the number a citation of it
	// names.
	section string
}

// explicitAnchorExpr reads the kramdown attribute that gives a heading
// an anchor of its own. A document that declares one addresses the
// heading by it rather than by the slug of the heading text, so the
// index carries both.
var explicitAnchorExpr = regexp.MustCompile(`\{:[^}\n]*#([A-Za-z0-9._-]+)[^}\n]*\}`)

// standaloneAnchorExpr reads the same attribute written on a line of its
// own, which is the position the documentation writes it in. It
// addresses the heading above it.
var standaloneAnchorExpr = regexp.MustCompile(`^\{:[^}\n]*#([A-Za-z0-9._-]+)[^}\n]*\}$`)

// newHeadings indexes the markdown documents of the class read domain.
//
// The domain comes from the scope package, so the pass reads the walk,
// the read exclusion, and the anchor class's own register exclusion the
// residual scan over the same class reads, rather than a second
// statement of any of them.
func newHeadings(ctx context.Context, list scope.Lister, read scope.FileReader) (*headings, error) {
	if list == nil || read == nil {
		return nil, fmt.Errorf("index the headings of the tree: a lister and a reader are required")
	}
	domain, err := scope.ClassReadDomain(ctx, list, scope.ClassAnchor)
	if err != nil {
		return nil, fmt.Errorf("index the headings of the tree: %w", err)
	}
	h := &headings{byFile: map[string]map[string]destination{}}
	for _, target := range domain {
		if filepath.Ext(target) != ".md" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("index the headings of the tree: %w", err)
		}
		content, err := read(target)
		if err != nil {
			return nil, fmt.Errorf("read %s to index its headings: %w", target, err)
		}
		h.byFile[target] = index(string(content))
	}
	if len(h.byFile) == 0 {
		return nil, fmt.Errorf("index the headings of the tree: no markdown document in the read domain")
	}
	return h, nil
}

// index returns the anchors one document declares, each with the
// heading it addresses. The first claim on an anchor keeps it, which is
// how a renderer that suffixes a repeated slug addresses the first of
// them.
//
// The walk admits every heading level a renderer derives an anchor
// from, so the level-one title a document opens with is addressable
// here. Reading only the levels a section's range is computed from
// would leave a link into such a title, and a redirect whose successor
// is one, resolving against no heading at all.
//
// A heading is addressable by the slug of its text, and by the explicit
// anchor a kramdown attribute declares for it. The attribute is written
// either on the heading line or on the line below it, so the walk reads
// both positions and attaches a standalone attribute to the heading
// above it.
//
// The walk reads the lines outside every fenced code block, which is the
// same set the heading half of the index reads. An attribute written
// inside a fence is the example a page documenting how an anchor is
// declared shows, and claiming it would address whichever heading
// preceded the fence: a successor or a destination naming that id would
// pass the pre-write existence check and land its inbound references at
// a page position that does not resolve, and a link into a retired
// anchor whose id appears in such an example would be read as a link the
// reduction left alone. An attribute standing above the document's first
// heading addresses no heading at all, so it declares nothing rather
// than binding to a heading with no number and no line.
//
// The walk also carries the numbered headings that are open at each
// line, so every anchor is indexed with the section it is written
// inside. The stack holds the numbered headings alone and is popped by
// level, so a heading carrying no number closes the deeper headings it
// follows without ever standing as an enclosing section itself. That is
// the same reading of the levels a section's range is computed with.
func index(content string) map[string]destination {
	out := map[string]destination{}
	claim := func(a string, d destination) {
		if _, taken := out[a]; taken || a == "" {
			return
		}
		out[a] = d
	}
	headingAt := map[int]citation.Heading{}
	for _, h := range citation.AllHeadings(content) {
		headingAt[h.Line] = h
	}
	var open []citation.Heading
	var last destination
	seen := false
	for _, line := range citation.ProseLines(content) {
		if h, ok := headingAt[line.Number]; ok {
			for len(open) > 0 && open[len(open)-1].Level >= h.Level {
				open = open[:len(open)-1]
			}
			d := destination{heading: h}
			if len(open) > 0 {
				d.section = open[len(open)-1].Number
			}
			if h.Number != "" {
				open = append(open, h)
			}
			last, seen = d, true
			claim(slug(h.Title), d)
			for _, m := range explicitAnchorExpr.FindAllStringSubmatch(h.Title, -1) {
				claim(m[1], d)
			}
			continue
		}
		if !seen {
			continue
		}
		if m := standaloneAnchorExpr.FindStringSubmatch(strings.TrimSpace(line.Text)); m != nil {
			claim(m[1], last)
		}
	}
	return out
}

// slug returns the anchor a renderer derives from a heading's text: the
// text lowercased, with every character that is not a letter, a digit, a
// hyphen, or an underscore dropped, and every space written as a hyphen.
func slug(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// lookup returns the heading a target addresses.
func (h *headings) lookup(t Target) (citation.Heading, bool) {
	d, ok := h.byFile[t.File][t.Anchor]
	return d.heading, ok
}

// citationFor returns the text a bare section citation of the target is
// rewritten to.
//
// A bare citation and a fragment link name the same heading in two
// different forms, and each keeps its own. A link's destination is a
// path and an anchor, which is what a renderer resolves, so a redirected
// link is written by linkTarget as the path from the citing page to the
// successor's file with the successor's anchor on it. A bare citation is
// a §X.Y token standing inside a sentence, and a sentence carrying a
// file path where a section number belongs stops reading as prose:
// "returns a spec/15_external-api-surface.md#messageenvelope-shaped
// message id" is neither a citation nor a link. The two forms are
// therefore rendered by two functions, and this one never produces the
// link form for a heading of the specification.
//
// A heading of the specification is cited by a section number. When the
// heading carries a number of its own, that number is the citation. That
// covers the level-one title a specification file opens with, which
// states the file's own section number, so a citation resolved to it
// keeps the §-form.
//
// A heading that carries no number of its own, such as the message-format
// heading a carve-out keeps in place, is still written inside a numbered
// section, and that section is what a bare citation of it names. The
// carve-out headings of spec/15 sit under `## 15.4`, so a citation
// resolving to either is written §15.4. This is the rule §29.1 fixes for
// specification prose: a citation names the surviving parent rather than
// the retired anchor.
//
// A specification destination with no enclosing numbered heading at all
// fails the run rather than falling back to the path form. There is no
// number to write, and the two candidates for carrying on are both
// worse: writing the path puts a link into a sentence, which is the
// defect this rule replaced, and leaving the citation as it stands
// would exit zero over a citation of an anchor the map retired. Failing
// names the destination and the register entry that chose it, so the
// change that seeded it either points the entry at a numbered heading
// or corrects the site by hand. Every specification heading the tree
// carries today sits under the level-one title that states the file's
// number, so the case is a defect in the register rather than a shape
// the specification is written in.
//
// A heading outside the specification is cited by the file and anchor
// that address it, because a §X.Y token names a section of the
// specification: writing the §-form against a heading a documentation or
// a testing page numbers the same way would land a citation of a section
// no specification file declares. Nothing over the anchor classes would
// report it, because the fragment-link gate reads links alone and the
// citation resolver matches the retired line-citation form alone.
func (h *headings) citationFor(t Target) (string, error) {
	d, ok := h.byFile[t.File][t.Anchor]
	if !ok {
		return "", fmt.Errorf("no heading of the tree is addressed by %s", t)
	}
	if !citation.IsSpecFile(t.File) {
		return t.String(), nil
	}
	if d.heading.Number != "" {
		return "§" + d.heading.Number, nil
	}
	if d.section == "" {
		return "", fmt.Errorf("%s addresses a heading that carries no number and sits under no numbered heading, so a bare citation of it has no section to name", t)
	}
	return "§" + d.section, nil
}

// carries reports whether the tree carries the markdown document.
func (h *headings) carries(target string) bool {
	_, ok := h.byFile[target]
	return ok
}
