// SPDX-License-Identifier: MIT

package ref

import (
	"strings"
)

// Stub kinds of a numbered heading.
const (
	// NotStub is a heading whose content has not migrated.
	NotStub = ""
	// FullStub is a heading that carries the stub marker.
	FullStub = "full"
	// PartialStub is a heading whose body opens with a successor list and
	// keeps part of its text.
	PartialStub = "partial"
)

// StubLink is one link of a successor list.
type StubLink struct {
	// Path is the link's target as a repository path, and Fragment the
	// anchor after `#`, empty for a link to a whole page.
	Path, Fragment string
}

// NumberedHeading is one numbered heading of a numbered file, with what
// its successor stub lists when the section has migrated.
type NumberedHeading struct {
	// File is the numbered file, Number the section number without the
	// `§` sign, Title the heading text with its number, and Anchor the
	// anchor a link to the heading uses.
	File, Number, Title, Anchor string
	// Line is the heading's line.
	Line int
	// Stub is NotStub, FullStub, or PartialStub.
	Stub string
	// Successors are the links of the successor list under the heading,
	// in order. A heading that is not a stub has none.
	Successors []StubLink
}

// IsNumberedFile reports whether a repository path is a numbered file of
// the specification, such as spec/04_system-components.md.
func IsNumberedFile(p string) bool { return isNumbered(p) }

// NumberedHeadings returns the numbered headings of a numbered file's
// content, in document order, each with the successor list its stub
// carries. The coverage view reads a section citation through these
// lists: a test that cites a migrated section reaches the identifiers
// the section's stub names.
func NumberedHeadings(file, content string) []NumberedHeading {
	lines := strings.Split(content, "\n")
	hs := headings(content)
	var out []NumberedHeading
	for i, h := range hs {
		if h.number == "" {
			continue
		}
		end := len(lines)
		if i+1 < len(hs) {
			end = hs[i+1].line - 1
		}
		nh := NumberedHeading{File: file, Number: h.number, Title: h.title, Anchor: h.linkAnchor(), Line: h.line}
		nh.Stub, nh.Successors = stubSuccessors(file, lines[h.line:end])
		out = append(out, nh)
	}
	return out
}

// stubSuccessors reads the stub kind and the successor list of one
// heading's own body.
func stubSuccessors(file string, body []string) (string, []StubLink) {
	items, _, full := parseStubBody(body)
	kind := NotStub
	switch {
	case full:
		kind = FullStub
	case len(items) > 0:
		kind = PartialStub
	}
	var out []StubLink
	for _, item := range items {
		for _, l := range links(item) {
			target, ok := resolve(file, l.path)
			if !ok {
				continue
			}
			out = append(out, StubLink{Path: target, Fragment: l.fragment})
		}
	}
	return kind, out
}
