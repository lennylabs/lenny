// SPDX-License-Identifier: MIT

package pagegate

import (
	"fmt"
	"regexp"
	"strings"
)

// stylePattern is one mechanical pattern of .claude/rules/doc-style.md.
type stylePattern struct {
	expr *regexp.Regexp
	// rule names the section of doc-style.md the pattern holds.
	rule string
}

// words returns an expression matching any of the words or phrases as a
// whole, case-insensitively. A phrase's apostrophe matches a straight or
// a typographic one.
func words(list ...string) *regexp.Regexp {
	parts := make([]string, 0, len(list))
	for _, w := range list {
		q := regexp.QuoteMeta(w)
		q = strings.ReplaceAll(q, "'", "['’]")
		q = strings.ReplaceAll(q, " ", `\s+`)
		parts = append(parts, q)
	}
	return regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}_-])(` + strings.Join(parts, "|") + `)(?:$|[^\p{L}\p{N}_-])`)
}

// stylePatterns are the patterns of the documentation style rule that a
// pattern finds without judgment. Patterns that need judgment, such as
// "X, not Y", em-dash overuse, a missing serial conjunction, a fragment,
// and hedging, stay with review.
var stylePatterns = []stylePattern{
	{words("comprehensive", "robust", "seamless", "seamlessly", "powerful", "leverage", "leverages", "leveraged", "leveraging",
		"unlock", "unlocks", "elegant", "elegantly", "world-class", "best-in-class", "industry-leading", "game-changing", "cutting-edge"),
		"Marketing or sales language"},
	{words("load-bearing", "existential"), "Hyperbole"},
	{words("very", "really", "just", "simply", "truly", "actually"), "Intensifiers and emphasis adverbs"},
	{words("importantly", "notably", "it's worth noting", "it is worth noting", "in essence", "simply put", "at its core",
		"in other words", "to put it simply"), "Stock connectors"},
	{words("earns its place", "earns the complexity", "value compounds", "let's dive in", "let's explore",
		"navigate the complexities", "in today's world", "deliberately scoped", "intentionally thin"), "Formulaic AI tics"},
	{words("we'll", "you'll", "let's", "by the end"), "Conversational intros and second-person framing"},
	{regexp.MustCompile(`(?i)(?:^|[^\p{L}])(that['’]s it|nothing more|nothing else needed|end of story)\s*[.!]`), "Rhetorical closures"},
}

// countHeading matches a heading that names a count, such as "The four
// meta-tools" or "3 deployment modes".
var countHeading = regexp.MustCompile(`(?i)(?:^|\s)(\d+|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|twenty)\s+\p{L}`)

// checkStyle reports each line of a page's prose that matches a style
// pattern, and each heading that names a count. The headings of a
// numbered file are left out: a successor stub keeps its section's
// heading as it was, because section citations reach the section through
// that heading's anchor, so the gate cannot ask for the heading to be
// reworded.
func checkStyle(p *Page) []Problem {
	var out []Problem
	for _, l := range proseLines(p) {
		if p.Type == typeNumbered && headingLine.MatchString(l.Text) {
			continue
		}
		for _, sp := range stylePatterns {
			if m := sp.expr.FindStringSubmatch(l.Text); m != nil {
				out = append(out, Problem{Path: p.Path, Line: l.Number, Message: fmt.Sprintf(
					"%q breaks the documentation style rule (.claude/rules/doc-style.md, %q)", strings.Join(strings.Fields(m[1]), " "), sp.rule)})
			}
		}
		if m := headingLine.FindStringSubmatch(l.Text); m != nil {
			if c := countHeading.FindStringSubmatch(m[2]); c != nil {
				out = append(out, Problem{Path: p.Path, Line: l.Number, Message: fmt.Sprintf(
					"the heading %q names the count %q; name the noun instead (.claude/rules/doc-style.md, %q)",
					strings.TrimSpace(m[2]), c[1], "Explicit counts of capabilities, types, SPIs")})
			}
		}
	}
	return out
}
