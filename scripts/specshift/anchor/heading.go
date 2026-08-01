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
// written in, which is the §-form for a numbered specification heading
// and the file-and-anchor form for every other heading.
//
// The index covers every markdown document of the class read domain,
// because a fragment link addresses a heading in the document it names,
// whichever document that is.
type headings struct {
	byFile map[string]map[string]citation.Heading
	// sections holds every section number the specification files of the
	// tree declare. It is what decides whether a bare §X.Y citation
	// names a section that still exists, so a citation of a section the
	// reduction retired stops the run when the anchor-move map carries
	// no successor for it.
	sections map[string]bool
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
	h := &headings{byFile: map[string]map[string]citation.Heading{}, sections: map[string]bool{}}
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
		if !citation.IsSpecFile(target) {
			continue
		}
		// A §X.Y token names a section of the specification, so only a
		// specification file declares one. The section walk drops the
		// level-one title, which numbers the file rather than a section,
		// which is the same predicate the citation resolver and the
		// per-file ratchet read.
		for _, heading := range citation.Headings(string(content)) {
			if heading.Number != "" {
				h.sections[heading.Number] = true
			}
		}
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
func index(content string) map[string]citation.Heading {
	out := map[string]citation.Heading{}
	claim := func(a string, h citation.Heading) {
		if _, taken := out[a]; taken || a == "" {
			return
		}
		out[a] = h
	}
	headingAt := map[int]citation.Heading{}
	for _, h := range citation.AllHeadings(content) {
		headingAt[h.Line] = h
	}
	var last citation.Heading
	seen := false
	for _, line := range citation.ProseLines(content) {
		if h, ok := headingAt[line.Number]; ok {
			last, seen = h, true
			claim(slug(h.Title), h)
			for _, m := range explicitAnchorExpr.FindAllStringSubmatch(h.Title, -1) {
				claim(m[1], h)
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
	heading, ok := h.byFile[t.File][t.Anchor]
	return heading, ok
}

// citationFor returns the text a bare section citation of the target is
// rewritten to.
//
// A numbered heading of the specification is cited by its number, which
// is the anchor citation form the migration establishes. That covers the
// level-one title a specification file opens with, which states the
// file's own section number, so a citation resolved to it keeps the
// §-form rather than moving off it into a fragment link. Every other
// heading is cited by the file and anchor that address it.
//
// The §-form is held to a specification file because a §X.Y token names
// a section of the specification, so writing it against a heading a
// documentation or a testing page numbers the same way would land a
// citation of a section no specification file declares. Nothing over the
// anchor classes would report it, because the fragment-link gate reads
// links alone and the citation resolver matches the retired
// line-citation form alone.
//
// A heading that carries no number, such as the message-format heading a
// carve-out keeps in place, has no number to cite either.
func (h *headings) citationFor(t Target) (string, error) {
	heading, ok := h.lookup(t)
	if !ok {
		return "", fmt.Errorf("no heading of the tree is addressed by %s", t)
	}
	if citation.IsSpecFile(t.File) && heading.Number != "" {
		return "§" + heading.Number, nil
	}
	return t.String(), nil
}

// carries reports whether the tree carries the markdown document.
func (h *headings) carries(target string) bool {
	_, ok := h.byFile[target]
	return ok
}

// declaresAnchor reports whether the document declares the anchor, which
// is what makes a fragment link into it a reference the reduction left
// alone.
func (h *headings) declaresAnchor(file, anchor string) bool {
	_, ok := h.byFile[file][anchor]
	return ok
}

// declaresSection reports whether a specification file of the tree
// declares the section number, which is what makes a bare citation of it
// a reference the reduction left alone.
func (h *headings) declaresSection(number string) bool {
	return h.sections[number]
}
