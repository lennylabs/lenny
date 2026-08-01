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
// declare, per file, together with the section number of the heading
// each anchor addresses.
//
// The index is what holds a redirect to a heading that exists. A
// successor anchor nothing declares would send every inbound reference
// to a page position that does not resolve, and no gate over the anchor
// classes reads meaning, so the run fails before it writes rather than
// after.
type headings struct {
	byFile map[string]map[string]citation.Heading
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

// sectionNumberExpr matches a dotted section number, which is the form a
// map entry's section field and a bare citation both carry.
var sectionNumberExpr = regexp.MustCompile(`^\d+(?:\.\d+)*$`)

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
	h := &headings{byFile: map[string]map[string]citation.Heading{}}
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
// A heading is addressable by the slug of its text, and by the explicit
// anchor a kramdown attribute declares for it. The attribute is written
// either on the heading line or on the line below it, so the walk reads
// both positions and attaches a standalone attribute to the heading
// above it.
func index(content string) map[string]citation.Heading {
	out := map[string]citation.Heading{}
	claim := func(a string, h citation.Heading) {
		if _, taken := out[a]; taken || a == "" {
			return
		}
		out[a] = h
	}
	headingAt := map[int]citation.Heading{}
	for _, h := range citation.Headings(content) {
		headingAt[h.Line] = h
	}
	var last citation.Heading
	for i, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if h, ok := headingAt[i+1]; ok {
			last = h
			claim(slug(h.Title), h)
			for _, m := range explicitAnchorExpr.FindAllStringSubmatch(h.Title, -1) {
				claim(m[1], h)
			}
			continue
		}
		if m := standaloneAnchorExpr.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
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
// A numbered heading is cited by its section number, which is the anchor
// citation form the migration establishes. A heading that declares no
// number, such as the message-format heading a carve-out keeps in place,
// has no number to cite, so it is cited by the file and anchor that
// address it.
func (h *headings) citationFor(t Target) (string, error) {
	heading, ok := h.lookup(t)
	if !ok {
		return "", fmt.Errorf("no heading of the tree is addressed by %s", t)
	}
	if heading.Number != "" {
		return "§" + heading.Number, nil
	}
	return t.String(), nil
}

// carries reports whether the tree carries the markdown document.
func (h *headings) carries(target string) bool {
	_, ok := h.byFile[target]
	return ok
}
