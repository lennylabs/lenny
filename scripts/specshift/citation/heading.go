// SPDX-License-Identifier: MIT

package citation

import "strings"

// Heading is one markdown heading of a document: the level of its hash
// run, the dotted section number its text opens with when it carries
// one, the rest of the text, and its 1-based line.
//
// The type is exported because two consumers read the same headings for
// different answers. The section index computes the range of every
// numbered section from them, and the anchor pass computes the slug of
// every heading, numbered or not, so it can hold a redirect's successor
// to a heading that exists. One walk keeps the heading predicate, the
// fence rule, and the number rule stated once.
type Heading struct {
	Level  int
	Number string
	Title  string
	Line   int
}

// SectionHeadingLevel is the shallowest heading level that declares a
// section. A level-one heading is the title of a page, so a number in
// its text names the file rather than a section of the specification,
// and a consumer that computes sections from AllHeadings filters on this
// level.
const SectionHeadingLevel = 2

// Headings returns every heading of one markdown document a section's
// range is computed from, in source order.
//
// A line inside a fenced code block is example text rather than a
// heading, and the walk drops the levels below SectionHeadingLevel, so a
// level-one title declares no section and carries no heading here.
func Headings(content string) []Heading {
	var out []Heading
	for _, h := range AllHeadings(content) {
		if h.Level < SectionHeadingLevel {
			continue
		}
		out = append(out, h)
	}
	return out
}

// Line is one line of a markdown document that sits outside every fenced
// code block, with its 1-based line number.
type Line struct {
	Number int
	Text   string
}

// ProseLines returns the lines of one markdown document that sit outside
// every fenced code block, in source order. The fence's own delimiter
// lines are dropped with the block they delimit, because neither carries
// prose.
//
// The fence rule is stated here once because two consumers read it. The
// heading walk drops a heading written inside a fence, and the anchor
// pass drops a kramdown anchor attribute written inside one. Both are
// example text: a page that documents how an anchor is declared shows
// the attribute in a fenced block, and reading that line as a
// declaration would claim an anchor no renderer gives the page.
func ProseLines(content string) []Line {
	var out []Line
	fenced := false
	for i, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
		if fenceExpr.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		out = append(out, Line{Number: i + 1, Text: line})
	}
	return out
}

// AllHeadings returns every heading of one markdown document at every
// level a renderer derives an anchor from, which begins at the level-one
// page title, in source order.
//
// The anchor pass reads this walk rather than Headings, because the
// anchor space covers every heading a link can address, including the
// level-one title a documentation page opens with. A level-one heading
// still declares no section: its Number field carries the number its
// text opens with, and a consumer computing sections drops it by level.
//
// A line inside a fenced code block is example text rather than a
// heading.
func AllHeadings(content string) []Heading {
	var out []Heading
	for _, line := range ProseLines(content) {
		m := anyHeadingExpr.FindStringSubmatch(line.Text)
		if m == nil {
			continue
		}
		h := Heading{Level: len(m[1]), Title: strings.TrimSpace(m[2]), Line: line.Number}
		if n := numberExpr.FindStringSubmatch(h.Title); n != nil {
			h.Number = n[1]
		}
		out = append(out, h)
	}
	return out
}
